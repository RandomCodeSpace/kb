package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/RandomCodeSpace/rig"
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
	skillMaxIterations = 12
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

// rigClient builds the agent-loop client for cfg. The transport stays this
// server's SSRF-guarded aiClient; rig owns the ambient credential scrub and
// the response size cap.
func (r *Runner) rigClient(cfg Config) (*rig.Client, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, &Error{Code: http.StatusBadRequest, Message: "AI base URL not configured"}
	}
	base, err := endpoint(cfg.BaseURL)
	if err != nil {
		return nil, &Error{Code: http.StatusBadRequest, Message: err.Error()}
	}
	client, err := rig.NewClient(base, cfg.Key, rig.WithHTTPClient(r.loopClient(cfg.Model)))
	if err != nil {
		return nil, &Error{Code: http.StatusBadRequest, Message: err.Error()}
	}
	return client, nil
}

// Client builds the rig client for a resolved configuration.
func (r *Runner) Client(cfg Config) (*rig.Client, error) { return r.rigClient(cfg) }

// loopClient is the HTTP client one run's completions go through. rig always
// states the output budget as max_tokens, which the o-series and gpt-5 models
// reject outright, and it offers no hook for the other field name -- so for
// exactly the models the single-shot path sends max_completion_tokens to, the
// request body is rewritten on the way out. Every other model keeps the shared
// client untouched.
func (r *Runner) loopClient(model string) *http.Client {
	if r.aiClient == nil || !usesMaxCompletionTokens(model) {
		return r.aiClient
	}
	clone := *r.aiClient
	clone.Transport = budgetFieldTransport{base: r.aiClient.Transport}
	return &clone
}

// budgetFieldTransport renames the output budget field of one completion
// request. It sits above the guarded transport, so the SSRF policy, timeout
// and redirect rules are unchanged; only the body it forwards differs.
type budgetFieldTransport struct {
	base http.RoundTripper
}

func (t budgetFieldTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	if req.Body == nil {
		return base.RoundTrip(req)
	}
	body, err := io.ReadAll(req.Body)
	req.Body.Close()
	if err != nil {
		return nil, err
	}
	if rewritten, ok := renameMaxTokens(body); ok {
		body = rewritten
	}
	// RoundTrip must not modify its argument, and the body was consumed to
	// read it, so the request is replaced rather than patched in place.
	out := req.Clone(req.Context())
	out.Body = io.NopCloser(bytes.NewReader(body))
	out.ContentLength = int64(len(body))
	out.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	return base.RoundTrip(out)
}

// renameMaxTokens moves the budget from max_tokens to max_completion_tokens.
// A body that is not a JSON object, states no budget, or already states the
// other field is left alone: the rewrite exists to fix one field name, not to
// take over what the request says.
func renameMaxTokens(body []byte) ([]byte, bool) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, false
	}
	budget, ok := fields["max_tokens"]
	if !ok {
		return nil, false
	}
	if _, exists := fields["max_completion_tokens"]; exists {
		return nil, false
	}
	delete(fields, "max_tokens")
	fields["max_completion_tokens"] = budget
	out, err := json.Marshal(fields)
	if err != nil {
		return nil, false
	}
	return out, true
}

// skillRunResult is one completed run: the cards the model proposed through
// propose_card, and its closing prose. Partial marks a run that hit a budget
// with cards already collected — the cards are real, the set is not complete.
// It stays off the wire: /api/ai/run-skill states the same fact in the
// commentary, and a caller that drops the commentary has to say so itself.
type RunResult struct {
	Cards      []Draft `json:"cards"`
	Commentary string  `json:"commentary"`
	Partial    bool    `json:"-"`
}

// skillScope selects what one run is allowed to do. The input of a run is
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
	client, err := r.rigClient(cfg)
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
	tools := []rig.Tool{
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
		tools = append(tools, rig.LoadSkillTool(others))
	}

	result, err := client.Run(ctx, rig.RunRequest{
		Model:         cfg.Model,
		System:        runnerSystem(skill, others),
		Prompt:        input,
		Tools:         tools,
		MaxTokens:     skillBudget(maxTokens),
		MaxIterations: skillMaxIterations,
	})
	cards := collector.cards
	if cards == nil {
		cards = []Draft{}
	}
	if err != nil {
		// Cards the collector already accepted are work the model finished
		// before the run hit a budget, and discarding them makes the whole run
		// a loss. Only the two budgets carry partial work forward: a transport
		// failure says nothing about what the endpoint did with the run, so it
		// stays a failure whatever the collector holds.
		if len(cards) > 0 && partialRun(err) {
			return RunResult{Cards: cards, Commentary: partialRunCommentary(err), Partial: true}, nil
		}
		return RunResult{Cards: cards}, runnerError(err)
	}
	return RunResult{Cards: cards, Commentary: result.Text}, nil
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

// partialRun reports whether a failed loop still produced usable work: a reply
// cut off at the token budget and a loop stopped at the round cap both end a
// run that already ran, unlike a transport or context failure.
func partialRun(err error) bool {
	return errors.Is(err, rig.ErrOutputLimit) || errors.Is(err, rig.ErrIterationLimit)
}

// partialRunCommentary stands in for the closing prose the run never wrote, so
// a caller that reads the commentary learns the run was cut short rather than
// reading an empty summary as "nothing more to say".
func partialRunCommentary(err error) string {
	if errors.Is(err, rig.ErrOutputLimit) {
		return TruncatedReplyMessage
	}
	return skillIterationLimitMessage
}

// splitSkills separates the invoked skill from the rest of the catalogue.
func splitSkills(skills []rig.Skill, name string) (rig.Skill, []rig.Skill, bool) {
	name = strings.TrimSpace(name)
	var (
		selected rig.Skill
		found    bool
	)
	others := make([]rig.Skill, 0, len(skills))
	for _, skill := range skills {
		if !found && skill.Name == name {
			selected, found = skill, true
			continue
		}
		others = append(others, skill)
	}
	return selected, others, found
}

func SplitSkills(skills []rig.Skill, name string) (rig.Skill, []rig.Skill, bool) {
	return splitSkills(skills, name)
}

// runnerSystem assembles the system prompt: kb context, the catalogue of the
// skills that stay loadable, then the invoked skill in full.
func runnerSystem(skill rig.Skill, others []rig.Skill) string {
	var b strings.Builder
	b.WriteString(runnerSystemPrompt)
	if advertisement := rig.Advertise(others); advertisement != "" {
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

func RunnerSystem(skill rig.Skill, others []rig.Skill) string { return runnerSystem(skill, others) }

// runnerError maps a loop failure onto the status the caller sees. The two
// outcomes a caller can act on — a reply cut off at the budget, and a loop that
// never stopped calling tools — are reported as themselves; everything else is
// an upstream problem, and writeAIError collapses those into one opaque
// message so a configured endpoint cannot become a reachability oracle.
func runnerError(err error) error {
	switch {
	case errors.Is(err, rig.ErrOutputLimit):
		return &Error{Code: http.StatusUnprocessableEntity, Message: TruncatedReplyMessage}
	case errors.Is(err, rig.ErrIterationLimit):
		return &Error{Code: http.StatusUnprocessableEntity, Message: skillIterationLimitMessage}
	}
	return &Error{Code: http.StatusBadGateway, Message: "upstream request failed", Cause: err}
}
