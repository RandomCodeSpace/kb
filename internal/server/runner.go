package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

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

// Budgets for one skill run. The loop is several upstream round trips, so it
// outlives both AITimeout and the server-wide write timeout: the run context
// bounds the whole loop and the response write deadline is extended past it
// per request, leaving room to write the answer after the last round.
const (
	skillRunDeadline   = 4 * time.Minute
	skillWriteDeadline = 5 * time.Minute
	skillMaxIterations = 12
)

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

// rigClient builds the agent-loop client for cfg. The transport stays this
// server's SSRF-guarded aiClient; rig owns the ambient credential scrub and
// the response size cap.
func (s *server) rigClient(cfg aiConfig) (*rig.Client, error) {
	if strings.TrimSpace(cfg.baseURL) == "" {
		return nil, &aiError{http.StatusBadRequest, "AI base URL not configured"}
	}
	base, err := aiEndpoint(cfg.baseURL)
	if err != nil {
		return nil, &aiError{http.StatusBadRequest, err.Error()}
	}
	client, err := rig.NewClient(base, cfg.key, rig.WithHTTPClient(s.loopClient(cfg.model)))
	if err != nil {
		return nil, &aiError{http.StatusBadRequest, err.Error()}
	}
	return client, nil
}

// loopClient is the HTTP client one run's completions go through. rig always
// states the output budget as max_tokens, which the o-series and gpt-5 models
// reject outright, and it offers no hook for the other field name -- so for
// exactly the models the single-shot path sends max_completion_tokens to, the
// request body is rewritten on the way out. Every other model keeps the shared
// client untouched.
func (s *server) loopClient(model string) *http.Client {
	if s.aiClient == nil || !usesMaxCompletionTokens(model) {
		return s.aiClient
	}
	clone := *s.aiClient
	clone.Transport = budgetFieldTransport{base: s.aiClient.Transport}
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
// propose_card, and its closing prose.
type skillRunResult struct {
	Cards      []storyDraft `json:"cards"`
	Commentary string       `json:"commentary"`
}

// skillScope selects what one run is allowed to do. The input of a run is
// prompt, and prompt is instruction: /api/ai/stories splits a document that
// can be a forge issue body and its comments, authored by anyone who can
// comment on that issue. Such a run gets the read-only set — it proposes
// drafts the user still has to accept, and nothing in it writes to the board
// or opens an outbound request the injected text could aim. The full set
// belongs to /api/ai/run-skill, whose input the requesting user wrote.
type skillScope int

const (
	skillScopeReadOnly skillScope = iota
	skillScopeFull
)

// runSkill executes one skill against the user's configured endpoint. The
// cards come from the collector the propose_card tool writes into, never from
// parsing the reply, so the model cannot smuggle a card past validateDraft and
// the count is capped server-side.
func (s *server) runSkill(ctx context.Context, user string, scope skillScope, skillName, input string, maxCards int) (skillRunResult, error) {
	cfg, err := s.storedAIConfig(user)
	if err != nil {
		return skillRunResult{}, err
	}
	client, err := s.rigClient(cfg)
	if err != nil {
		return skillRunResult{}, err
	}
	skills, err := s.loadSkills()
	if err != nil {
		log.Printf("ai: loading skills for %s failed: %s", logSafe(user), logSafe(err.Error()))
		return skillRunResult{}, &aiError{http.StatusInternalServerError, skillsUnavailableMessage}
	}
	skill, others, found := splitSkills(skills, skillName)
	if !found {
		return skillRunResult{}, &aiError{http.StatusNotFound, unknownSkillMessage}
	}

	collector := &cardCollector{max: normalizeStoryCount(maxCards)}
	tools := []rig.Tool{
		proposeCardTool(collector),
		s.findSimilarTool(user),
		s.listTasksTool(user),
		s.getTaskTool(user),
	}
	// The two tools that reach past a draft — a board write and an outbound
	// request — are offered only to a run whose input the user authored.
	if scope == skillScopeFull {
		tools = append(tools, s.fetchLinkTool(), s.updateTaskTool(user))
	}
	// The invoked skill is force-injected into the system prompt; only the
	// others are loadable, so a run cannot lose its own instructions to a
	// model that never calls load_skill.
	if len(others) > 0 {
		tools = append(tools, rig.LoadSkillTool(others))
	}

	result, err := client.Run(ctx, rig.RunRequest{
		Model:         cfg.model,
		System:        runnerSystem(skill, others),
		Prompt:        input,
		Tools:         tools,
		MaxTokens:     aiStoriesMaxTokens,
		MaxIterations: skillMaxIterations,
	})
	cards := collector.cards
	if cards == nil {
		cards = []storyDraft{}
	}
	if err != nil {
		// Cards the collector already accepted are work the model finished
		// before the run hit a budget, and discarding them makes the whole run
		// a loss. Only the two budgets carry partial work forward: a transport
		// failure says nothing about what the endpoint did with the run, so it
		// stays a failure whatever the collector holds.
		if len(cards) > 0 && partialRun(err) {
			return skillRunResult{Cards: cards, Commentary: partialRunCommentary(err)}, nil
		}
		return skillRunResult{Cards: cards}, runnerError(err)
	}
	return skillRunResult{Cards: cards, Commentary: result.Text}, nil
}

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
		return truncatedReplyMessage
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

// runnerError maps a loop failure onto the status the caller sees. The two
// outcomes a caller can act on — a reply cut off at the budget, and a loop that
// never stopped calling tools — are reported as themselves; everything else is
// an upstream problem, and writeAIError collapses those into one opaque
// message so a configured endpoint cannot become a reachability oracle.
func runnerError(err error) error {
	switch {
	case errors.Is(err, rig.ErrOutputLimit):
		return &aiError{http.StatusUnprocessableEntity, truncatedReplyMessage}
	case errors.Is(err, rig.ErrIterationLimit):
		return &aiError{http.StatusUnprocessableEntity, skillIterationLimitMessage}
	}
	return &aiError{http.StatusBadGateway, "upstream request failed"}
}

// runSkillForRequest runs one skill on behalf of an HTTP request: the write
// deadline is extended first, then the loop runs under the request context
// with its own deadline.
func (s *server) runSkillForRequest(w http.ResponseWriter, r *http.Request, user string, scope skillScope, skillName, input string, maxCards int) (skillRunResult, error) {
	extendWriteDeadline(w)
	ctx, cancel := context.WithTimeout(r.Context(), skillRunDeadline)
	defer cancel()
	return s.runSkill(ctx, user, scope, skillName, input, maxCards)
}

// extendWriteDeadline pushes this response past the server-wide write timeout,
// which Go arms when the request headers are read — a budget sized for one
// upstream round trip, while the loop is several. Every writer in the served
// chain unwraps down to the connection; a writer that does not (a recorder in
// a test) reports ErrNotSupported and has no deadline to extend.
func extendWriteDeadline(w http.ResponseWriter) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(skillWriteDeadline))
}

// aiRunSkillRequest is the body of POST /api/ai/run-skill.
type aiRunSkillRequest struct {
	Skill string `json:"skill"`
	Input string `json:"input"`
	Max   int    `json:"max"`
}

// handleAIRunSkill runs one named skill over the user's board and returns what
// it proposed plus its commentary. The input is prose, not a payload, so it is
// bounded by the same 64 KiB an ADR gets, and it is never stored.
func (s *server) handleAIRunSkill(w http.ResponseWriter, r *http.Request, user string) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	var req aiRunSkillRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, invalidJSONBodyMessage, http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Skill) == "" {
		http.Error(w, "skill required", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Input) == "" {
		http.Error(w, "input required", http.StatusBadRequest)
		return
	}
	if len(req.Input) > maxADRBytes {
		http.Error(w, "input too large (max 64 KiB)", http.StatusRequestEntityTooLarge)
		return
	}
	result, err := s.runSkillForRequest(w, r, user, skillScopeFull, req.Skill, req.Input, req.Max)
	if err != nil {
		writeAIError(w, user, "run-skill", err)
		return
	}
	writeJSON(w, result)
}
