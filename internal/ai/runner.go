package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strings"

	"github.com/RandomCodeSpace/plasmid/oneshot"
	"google.golang.org/adk/v2/tool"
)

// runnerSystemPrompt is the kb context every skill run starts from. It states
// what the model may touch and how a proposal leaves the loop: propose_card is
// the only structured output channel, so the final reply is prose the caller
// can show and never a payload the caller has to parse.
const runnerSystemPrompt = `You are the assistant of kb, a kanban board. You reach the user's board only through the tools listed below; nothing else you write touches it.

Rules:
- Propose a new card only by calling propose_card, once per card. Never write a card as JSON or markdown in your reply.
- Before proposing a card, call find_similar to check the board does not already track that work.
- Read the board with list_tasks and get_task; change an existing card with update_task.
- Call fetch_link only for a URL the user supplied.
- Follow the skill instructions below. They describe the task you were given.
- Finish with a short markdown commentary of what you proposed or changed and why. No JSON, no code fences, no card bodies.`

const RunnerSystemPrompt = runnerSystemPrompt

// Budgets for one skill run. The loop is several upstream round trips, so it
// outlives both AITimeout and the server-wide write timeout: the run context
// bounds the whole loop and the response write deadline is extended past it
// per request, leaving room to write the answer after the last round.
const (
	skillMaxIterations      = 12
	maxToolCallsPerResponse = 32
	maxReturnedTextBytes    = 1 << 20
)

const SkillMaxIterations = skillMaxIterations

const (
	unknownSkillMessage = "unknown skill"
	// skillsUnavailableMessage covers a broken skill file. The parse error
	// names the offending file, which is server-side detail.
	skillsUnavailableMessage = "skills are unavailable"
	// skillIterationLimitMessage is the model spending every round on tool
	// calls without ever answering. Like truncation it is the caller's to act
	// on, so it is reported as itself rather than collapsed into a 502.
	skillIterationLimitMessage = "the model kept calling tools without finishing — narrow the request"
)

const (
	UnknownSkillMessage        = unknownSkillMessage
	SkillsUnavailableMessage   = skillsUnavailableMessage
	SkillIterationLimitMessage = skillIterationLimitMessage
)

// RunResult is one completed run: the cards the model proposed through
// propose_card, and its closing prose. Partial marks a run that hit a budget
// with cards already collected — the cards are real, the set is not complete.
// It stays off the wire: /api/ai/run-skill states the same fact in the
// commentary, and a caller that drops the commentary has to say so itself.
type RunResult struct {
	Cards      []Draft `json:"cards"`
	Commentary string  `json:"commentary"`
	Partial    bool    `json:"-"`
}

// Scope selects what one run is allowed to do. The input of a run is
// prompt, and prompt is instruction: /api/ai/stories splits a document that
// can be a forge issue body and its comments, authored by anyone who can
// comment on that issue. Such a run gets the read-only set — it proposes
// drafts the user still has to accept, and nothing in it writes to the board
// or opens an outbound request the injected text could aim. The full set
// belongs to /api/ai/run-skill, whose input the requesting user wrote.
type Scope int

const (
	ScopeReadOnly Scope = iota
	ScopeFull
)

// runSkill executes one skill against the user's configured endpoint. The
// cards come from the collector the propose_card tool writes into, never from
// parsing the reply, so the model cannot smuggle a card past validateDraft and
// the count is capped server-side. maxTokens is the per-flow output budget:
// one card needs far less room than a whole ADR split, and the caller knows
// which flow it is.
func (r *Runner) RunSkill(ctx context.Context, user string, scope Scope, skillName, input string, maxCards int, maxTokens int64) (RunResult, error) {
	cfg, err := r.storedConfig(user)
	if err != nil {
		return RunResult{}, err
	}
	model, err := r.newModel(ctx, cfg)
	if err != nil {
		return RunResult{}, err
	}
	skills, err := r.LoadSkills()
	if err != nil {
		log.Printf("ai: loading skills for %s failed: %s", logSafe(user), logSafe(err.Error()))
		return RunResult{}, &Error{Code: http.StatusInternalServerError, Message: skillsUnavailableMessage, Cause: err}
	}
	skill, others, found := splitSkills(skills, skillName)
	if !found {
		return RunResult{}, &Error{Code: http.StatusNotFound, Message: unknownSkillMessage}
	}

	collector := &cardCollector{max: normalizeStoryCount(maxCards)}
	tools := r.skillTools(user, scope, others, collector)

	result, err := oneshot.Run(ctx, oneshot.Request{
		Model:                   model,
		Instruction:             runnerSystem(skill, others),
		Prompt:                  input,
		Tools:                   tools,
		MaxOutputTokens:         int32(skillBudget(maxTokens)),
		MaxReturnedTextBytes:    maxReturnedTextBytes,
		MaxModelCalls:           skillMaxIterations,
		MaxToolCallsPerResponse: maxToolCallsPerResponse,
		ToolExecution:           oneshot.ToolExecutionSequential,
	})
	return mapSkillRunResult(collector.cards, result, err)
}

func mapSkillRunResult(cards []Draft, result oneshot.Result, err error) (RunResult, error) {
	if cards == nil {
		cards = []Draft{}
	}
	if err != nil {
		// Cards the collector already accepted are work the model finished
		// before the run hit a budget, and discarding them makes the whole run
		// a loss. Only output bounds and model-call exhaustion carry partial
		// work forward. Backend failures stay failures whatever the collector
		// holds.
		if len(cards) > 0 && partialRun(err) {
			return RunResult{Cards: cards, Commentary: partialRunCommentary(err), Partial: true}, nil
		}
		return RunResult{Cards: cards}, runnerError(err)
	}
	return RunResult{Cards: cards, Commentary: result.Text}, nil
}

// skillTools builds the exact native ADK declaration set for one skill run.
// Tool authority and declaration order live here instead of in the runtime
// adapter, so switching the loop cannot silently broaden a scope.
func (r *Runner) skillTools(user string, scope Scope, others []Skill, collector *cardCollector) []tool.Tool {
	tools := []tool.Tool{
		proposeCardTool(collector),
		r.findSimilarTool(user),
		r.listTasksTool(user),
		r.getTaskTool(user),
	}
	// The two tools that reach past a draft — a board write and an outbound
	// request — are offered only to a run whose input the user authored.
	if scope == ScopeFull {
		tools = append(tools, r.fetchLinkTool(), r.updateTaskTool(user))
	}
	// The invoked skill is force-injected into the system prompt; only the
	// others are loadable, so a run cannot lose its own instructions to a
	// model that never calls load_skill.
	if len(others) > 0 {
		tools = append(tools, loadSkillTool(others))
	}
	return tools
}

// RunText performs one tool-free completion using stored configuration
// without exposing the decrypted API key to callers.
func (r *Runner) RunText(ctx context.Context, user, system, prompt string, maxTokens int64) (string, error) {
	cfg, err := r.storedConfig(user)
	if err != nil {
		return "", err
	}
	model, err := r.newModel(ctx, cfg)
	if err != nil {
		return "", err
	}
	result, err := oneshot.Run(ctx, oneshot.Request{
		Model:                   model,
		Instruction:             system,
		Prompt:                  prompt,
		MaxOutputTokens:         int32(skillBudget(maxTokens)),
		MaxReturnedTextBytes:    maxReturnedTextBytes,
		MaxModelCalls:           1,
		MaxToolCallsPerResponse: maxToolCallsPerResponse,
		ToolExecution:           oneshot.ToolExecutionSequential,
	})
	if err != nil {
		return "", runnerError(err)
	}
	return result.Text, nil
}

// skillBudget bounds one run's output budget to the range the endpoint is
// known to accept. The ceiling is not advice: a model is only known by the
// name the user typed, and asking for more than its own completion cap is
// rejected outright by strict servers — a 400 no caller can act on. Clamping
// on the way to the wire rather than trusting each call site means a new flow
// cannot reintroduce that failure by passing a larger constant.
func skillBudget(maxTokens int64) int64 {
	if maxTokens < 1 {
		return 1
	}
	if maxTokens > maxTokensCeiling {
		return maxTokensCeiling
	}
	return maxTokens
}

// SkillBudget clamps a requested completion budget to the supported range.
func SkillBudget(maxTokens int64) int64 { return skillBudget(maxTokens) }

// partialRun reports whether a failed loop still produced usable work. Tool
// call limits and execution failures do not prove that the collected set is a
// valid prefix, so only output bounds and model-call exhaustion qualify.
func partialRun(err error) bool {
	if backendRunFailure(err) || errors.Is(err, oneshot.ErrToolCallLimit) {
		return false
	}
	return errors.Is(err, oneshot.ErrOutputTruncated) || errors.Is(err, oneshot.ErrTextTruncated) || errors.Is(err, oneshot.ErrModelCallLimit)
}

// partialRunCommentary stands in for the closing prose the run never wrote, so
// a caller that reads the commentary learns the run was cut short rather than
// reading an empty summary as "nothing more to say".
func partialRunCommentary(err error) string {
	if errors.Is(err, oneshot.ErrOutputTruncated) || errors.Is(err, oneshot.ErrTextTruncated) {
		return TruncatedReplyMessage
	}
	return skillIterationLimitMessage
}

// splitSkills separates the invoked skill from the rest of the catalogue.
func splitSkills(skills []Skill, name string) (Skill, []Skill, bool) {
	name = strings.TrimSpace(name)
	var (
		selected Skill
		found    bool
	)
	others := make([]Skill, 0, len(skills))
	for _, skill := range skills {
		if !found && skill.Name == name {
			selected, found = skill, true
			continue
		}
		others = append(others, skill)
	}
	return selected, others, found
}

// runnerSystem assembles the system prompt: kb context, the catalogue of the
// skills that stay loadable, then the invoked skill in full.
func runnerSystem(skill Skill, others []Skill) string {
	var b strings.Builder
	b.WriteString(runnerSystemPrompt)
	if advertisement := advertiseSkills(others); advertisement != "" {
		b.WriteString("\n\n")
		b.WriteString(advertisement)
	}
	b.WriteString("\n\nSkill: ")
	b.WriteString(skill.Name)
	b.WriteString("\n")
	b.WriteString(skill.Description)
	b.WriteString("\n\n")
	b.WriteString(skill.Body)
	return b.String()
}

// loadSkillTool exposes kb's skill catalogue through the same native ADK tool
// interface as the board tools.
func loadSkillTool(skills []Skill) *kbTool {
	bodies := make(map[string]string, len(skills))
	names := make([]string, 0, len(skills))
	for _, skill := range skills {
		if _, exists := bodies[skill.Name]; !exists {
			names = append(names, skill.Name)
		}
		bodies[skill.Name] = skill.Body
	}
	slices.Sort(names)
	available := strings.Join(names, ", ")

	return newKBTool(
		loadSkillToolName,
		"Load the full instructions for one of the skills listed in the system prompt. Call this before acting on a skill.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Exact skill name as advertised in the system prompt.",
				},
			},
			"required":             []any{"name"},
			"additionalProperties": false,
		},
		toolResultText,
		func(_ context.Context, input json.RawMessage) (string, error) {
			var args struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(input, &args); err != nil {
				return "", fmt.Errorf("invalid input, expected {\"name\": string}: %w", err)
			}
			name := strings.TrimSpace(args.Name)
			if body, ok := bodies[name]; ok {
				return body, nil
			}
			if len(names) == 0 {
				return "", errors.New("no skills are available")
			}
			return "", fmt.Errorf("unknown skill %q, available skills: %s", name, available)
		},
	)
}

// runnerError maps a loop failure onto the status the caller sees. The two
// outcomes a caller can act on — a reply cut off at the budget, and a loop that
// never stopped calling tools — are reported as themselves; everything else is
// an upstream problem, and writeAIError collapses those into one opaque
// message so a configured endpoint cannot become a reachability oracle.
func runnerError(err error) error {
	if backendRunFailure(err) {
		return &Error{Code: http.StatusBadGateway, Message: "upstream request failed", Cause: err}
	}
	switch {
	case errors.Is(err, oneshot.ErrOutputTruncated), errors.Is(err, oneshot.ErrTextTruncated):
		return &Error{Code: http.StatusUnprocessableEntity, Message: TruncatedReplyMessage}
	case errors.Is(err, oneshot.ErrModelCallLimit), errors.Is(err, oneshot.ErrToolCallLimit):
		return &Error{Code: http.StatusUnprocessableEntity, Message: skillIterationLimitMessage}
	}
	return &Error{Code: http.StatusBadGateway, Message: "upstream request failed", Cause: err}
}

func backendRunFailure(err error) bool {
	return errors.Is(err, oneshot.ErrInvalidArgument) ||
		errors.Is(err, oneshot.ErrCanceled) ||
		errors.Is(err, oneshot.ErrModelPanic) ||
		errors.Is(err, oneshot.ErrToolPanic) ||
		errors.Is(err, oneshot.ErrToolCallingUnsupported) ||
		errors.Is(err, oneshot.ErrNoFinalResponse) ||
		errors.Is(err, oneshot.ErrExecutionFailed) ||
		errors.Is(err, oneshot.ErrCleanupFailed)
}
