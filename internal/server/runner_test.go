package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeToolCall is one tool call an assistant reply carries. args is the raw
// JSON arguments string, exactly as an upstream puts it on the wire.
type fakeToolCall struct {
	name string
	args string
}

// fakeReply is one scripted assistant reply.
type fakeReply struct {
	content   string
	toolCalls []fakeToolCall
	finish    string // finish_reason; empty means "stop"
	status    int    // 0 means 200
}

// scriptedOpenAI answers each POST with the next reply in the script and keeps
// every request body. Once the script runs out the last reply repeats, so a
// loop that asks for more rounds than were scripted still terminates — at the
// iteration cap when that reply keeps calling tools.
type scriptedOpenAI struct {
	mu      sync.Mutex
	replies []fakeReply
	reqs    [][]byte
}

func (f *scriptedOpenAI) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		reply := f.next(body)
		if reply.status != 0 {
			http.Error(w, "upstream boom", reply.status)
			return
		}
		message := map[string]any{"role": "assistant", "content": reply.content}
		if len(reply.toolCalls) > 0 {
			calls := make([]any, 0, len(reply.toolCalls))
			for i, call := range reply.toolCalls {
				calls = append(calls, map[string]any{
					"id":       fmt.Sprintf("call-%d", i+1),
					"type":     "function",
					"function": map[string]any{"name": call.name, "arguments": call.args},
				})
			}
			message["tool_calls"] = calls
		}
		finish := reply.finish
		if finish == "" {
			finish = "stop"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finish}},
		})
	}
}

func (f *scriptedOpenAI) next(body []byte) fakeReply {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs = append(f.reqs, body)
	if len(f.replies) == 0 {
		return fakeReply{content: "no reply scripted"}
	}
	if len(f.reqs) <= len(f.replies) {
		return f.replies[len(f.reqs)-1]
	}
	return f.replies[len(f.replies)-1]
}

func (f *scriptedOpenAI) requests() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]byte(nil), f.reqs...)
}

// newSkillServer wires a server whose AI endpoint is the scripted fake. The
// private-address opt-in is set before the server is built, because the
// guarded dialer is fixed at construction and the fake is on loopback.
func newSkillServer(t *testing.T, fake *scriptedOpenAI, cfg Config) http.Handler {
	t.Helper()
	upstream := httptest.NewServer(fake.handler())
	t.Cleanup(upstream.Close)
	t.Setenv("KB_AI_ALLOW_PRIVATE", "1")
	h, _ := newTestServer(t, cfg)
	configureAI(t, h, upstream.URL, "test-model", "sk-test")
	return h
}

// newSkillServerWithModel is newSkillServer for a run whose model name decides
// how the request states its output budget.
func newSkillServerWithModel(t *testing.T, fake *scriptedOpenAI, cfg Config, model string) http.Handler {
	t.Helper()
	upstream := httptest.NewServer(fake.handler())
	t.Cleanup(upstream.Close)
	t.Setenv("KB_AI_ALLOW_PRIVATE", "1")
	h, _ := newTestServer(t, cfg)
	configureAI(t, h, upstream.URL, model, "sk-test")
	return h
}

// writeSkill drops one skill file into an override directory.
func writeSkill(t *testing.T, dir, file, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create skills dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o600); err != nil {
		t.Fatalf("write skill file: %v", err)
	}
}

const extraSkillFile = `---
name: extra
description: An override skill used by the runner tests
---
Override body sentinel: do the extra thing.`

func runSkillReq(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	return doReq(t, h, http.MethodPost, "/api/ai/run-skill", body, nil)
}

func decodeSkillRun(t *testing.T, w *httptest.ResponseRecorder) skillRunResult {
	t.Helper()
	var got skillRunResult
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("run-skill JSON: %v (body=%s)", err, w.Body)
	}
	return got
}

// systemPrompt returns the system message of one recorded upstream request.
func systemPrompt(t *testing.T, body []byte) string {
	t.Helper()
	sent := decodeAIRequest(t, body)
	for _, msg := range sent.Messages {
		if msg.Role == "system" {
			return msg.Content
		}
	}
	t.Fatalf("request carried no system message: %s", body)
	return ""
}

func toolNames(t *testing.T, body []byte) []string {
	t.Helper()
	sent := decodeAIRequest(t, body)
	names := make([]string, 0, len(sent.Tools))
	for _, tool := range sent.Tools {
		names = append(names, tool.Function.Name)
	}
	return names
}

func hasTool(names []string, want string) bool {
	return slices.Contains(names, want)
}

// A skill run is several rounds: the model researches, proposes cards through
// the collector, and finishes with prose. Cards come from propose_card only,
// so the closing reply is never parsed.
func TestRunSkillProposesCardsAcrossRounds(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "skills")
	writeSkill(t, dir, "extra.md", extraSkillFile)

	fake := &scriptedOpenAI{replies: []fakeReply{
		{toolCalls: []fakeToolCall{{name: "find_similar", args: `{"query":"payment retries"}`}}},
		{toolCalls: []fakeToolCall{
			{name: "propose_card", args: `{"title":"Add a retry policy","desc":"Retry failed charges","effort":"M"}`},
			{name: "propose_card", args: `{"title":"Add a dead letter queue"}`},
		}},
		{content: "Proposed two stories: retries and a dead letter queue."},
	}}
	h := newSkillServer(t, fake, Config{SkillsDir: dir})

	w := runSkillReq(t, h, `{"skill":"extra","input":"# Payments ADR"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("POST run-skill: got %d (body=%s)", w.Code, w.Body)
	}
	got := decodeSkillRun(t, w)
	if len(got.Cards) != 2 || got.Cards[0].Title != "Add a retry policy" || got.Cards[1].Title != "Add a dead letter queue" {
		t.Fatalf("cards = %+v, want the two proposed titles", got.Cards)
	}
	if got.Cards[0].Effort != "M" {
		t.Errorf("effort = %q, want M", got.Cards[0].Effort)
	}
	if got.Commentary != "Proposed two stories: retries and a dead letter queue." {
		t.Errorf("commentary = %q", got.Commentary)
	}

	reqs := fake.requests()
	if len(reqs) != 3 {
		t.Fatalf("upstream rounds = %d, want 3", len(reqs))
	}
	// The invoked skill is injected in full; the others stay loadable.
	system := systemPrompt(t, reqs[0])
	for _, want := range []string{runnerSystemPrompt, "Skill: extra", "Override body sentinel", "- adr-split:"} {
		if !strings.Contains(system, want) {
			t.Errorf("system prompt missing %q: %s", want, system)
		}
	}
	if strings.Contains(system, "Skill: adr-split") {
		t.Error("system prompt injected a skill that was not invoked")
	}
	names := toolNames(t, reqs[0])
	for _, want := range []string{"propose_card", "find_similar", "fetch_link", "list_tasks", "get_task", "update_task", "load_skill"} {
		if !hasTool(names, want) {
			t.Errorf("tool %q not advertised: %v", want, names)
		}
	}
	// The last request carries the whole transcript, so the loop fed both
	// tool results back before the model answered.
	if sent := decodeAIRequest(t, reqs[2]); len(sent.Messages) < 6 {
		t.Errorf("final request carried %d messages, want the full transcript", len(sent.Messages))
	}
}

// The one skill being run is not also loadable, so a catalogue of one leaves
// nothing to advertise and no load_skill tool to call. The embedded catalogue
// now carries several skills, so the single-skill case is exercised on the two
// helpers the run composes rather than through a request.
func TestRunSkillWithoutOtherSkillsOmitsLoadSkill(t *testing.T) {
	only := skill{Name: "solo", Description: "the only skill", Body: "Do the thing."}
	selected, others, found := splitSkills([]skill{only}, "solo")
	if !found {
		t.Fatal("splitSkills did not find the only skill")
	}
	if len(others) != 0 {
		t.Fatalf("others = %v, want the invoked skill to be the whole catalogue", others)
	}
	if system := runnerSystem(selected, others); strings.Contains(system, "Available skills") {
		t.Errorf("skills advertised with no other skills: %s", system)
	}
}

// A run advertises every other skill and never the one it is running: the
// invoked skill is force-injected, so a model that ignores load_skill cannot
// lose its own instructions.
func TestRunSkillAdvertisesOnlyTheOtherSkills(t *testing.T) {
	fake := &scriptedOpenAI{replies: []fakeReply{{content: "Nothing to split."}}}
	h := newSkillServer(t, fake, Config{})

	w := runSkillReq(t, h, `{"skill":"adr-split","input":"# Tiny ADR"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("POST run-skill: got %d (body=%s)", w.Code, w.Body)
	}
	// Cards are always an array; the frozen client indexes it directly.
	if body := strings.TrimSpace(w.Body.String()); !strings.Contains(body, `"cards":[]`) {
		t.Errorf("body = %s, want an empty cards array", body)
	}
	reqs := fake.requests()
	if names := toolNames(t, reqs[0]); !hasTool(names, "load_skill") {
		t.Errorf("load_skill missing with other skills present: %v", names)
	}
	system := systemPrompt(t, reqs[0])
	advertisement, _, ok := strings.Cut(system, "\n\nSkill: adr-split")
	if !ok {
		t.Fatalf("system prompt does not inject the invoked skill: %s", system)
	}
	if !strings.Contains(advertisement, "story-draft") || !strings.Contains(advertisement, "import-transform") {
		t.Errorf("advertisement = %s, want the other embedded skills", advertisement)
	}
	if strings.Contains(advertisement, "adr-split") {
		t.Errorf("advertisement = %s, want the invoked skill excluded", advertisement)
	}
}

// The cap is the collector's, not the prompt's: proposals past it are refused
// with an error the model can see, never silently dropped.
func TestRunSkillCapsProposedCards(t *testing.T) {
	fake := &scriptedOpenAI{replies: []fakeReply{
		{toolCalls: []fakeToolCall{
			{name: "propose_card", args: `{"title":"First card"}`},
			{name: "propose_card", args: `{"title":"Second card"}`},
			{name: "propose_card", args: `{"title":"Third card"}`},
		}},
		{content: "Two proposed, one refused."},
	}}
	h := newSkillServer(t, fake, Config{})

	w := runSkillReq(t, h, `{"skill":"adr-split","input":"# ADR","max":2}`)
	if w.Code != http.StatusOK {
		t.Fatalf("POST run-skill: got %d (body=%s)", w.Code, w.Body)
	}
	got := decodeSkillRun(t, w)
	if len(got.Cards) != 2 {
		t.Fatalf("cards = %+v, want the cap of 2", got.Cards)
	}
	reqs := fake.requests()
	if len(reqs) != 2 {
		t.Fatalf("upstream rounds = %d, want 2", len(reqs))
	}
	sent := decodeAIRequest(t, reqs[1])
	refused := false
	for _, msg := range sent.Messages {
		if msg.Role == "tool" && strings.HasPrefix(msg.Content, "error: ") {
			refused = true
		}
	}
	if !refused {
		t.Errorf("the refused proposal was not fed back to the model: %s", reqs[1])
	}
}

func TestRunSkillUnknownSkill(t *testing.T) {
	fake := &scriptedOpenAI{replies: []fakeReply{{content: "unreachable"}}}
	h := newSkillServer(t, fake, Config{})

	w := runSkillReq(t, h, `{"skill":"no-such-skill","input":"# ADR"}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("POST run-skill: got %d (body=%s), want 404", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), unknownSkillMessage) {
		t.Errorf("body = %q, want %q", w.Body, unknownSkillMessage)
	}
	if len(fake.requests()) != 0 {
		t.Error("an unknown skill reached the upstream")
	}
}

// A broken override file fails the run instead of vanishing from the
// catalogue; the parse error names a server-side path, so it is logged only.
func TestRunSkillBrokenOverrideIsLoud(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "skills")
	writeSkill(t, dir, "broken.md", "no frontmatter here\n")

	fake := &scriptedOpenAI{replies: []fakeReply{{content: "unreachable"}}}
	h := newSkillServer(t, fake, Config{SkillsDir: dir})

	w := runSkillReq(t, h, `{"skill":"adr-split","input":"# ADR"}`)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("POST run-skill: got %d (body=%s), want 500", w.Code, w.Body)
	}
	if body := w.Body.String(); !strings.Contains(body, skillsUnavailableMessage) || strings.Contains(body, "broken.md") {
		t.Errorf("body = %q, want %q without the file name", body, skillsUnavailableMessage)
	}
}

func TestRunSkillWithoutAIConfig(t *testing.T) {
	h, _ := newTestServer(t, Config{})

	w := runSkillReq(t, h, `{"skill":"adr-split","input":"# ADR"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("POST run-skill: got %d (body=%s), want 400", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), "AI base URL not configured") {
		t.Errorf("body = %q", w.Body)
	}
}

// A reply cut off at the budget and a loop that never stops calling tools are
// both the caller's to act on, so each is reported as itself.
func TestRunSkillReportsRecoverableLoopFailures(t *testing.T) {
	tests := []struct {
		name    string
		replies []fakeReply
		want    string
	}{
		{
			name:    "truncated reply",
			replies: []fakeReply{{content: "Proposing the fi", finish: "length"}},
			want:    truncatedReplyMessage,
		},
		{
			name:    "iteration limit",
			replies: []fakeReply{{toolCalls: []fakeToolCall{{name: "list_tasks", args: `{}`}}}},
			want:    skillIterationLimitMessage,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &scriptedOpenAI{replies: tt.replies}
			h := newSkillServer(t, fake, Config{})

			w := runSkillReq(t, h, `{"skill":"adr-split","input":"# ADR"}`)
			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("POST run-skill: got %d (body=%s), want 422", w.Code, w.Body)
			}
			if !strings.Contains(w.Body.String(), tt.want) {
				t.Errorf("body = %q, want %q", w.Body, tt.want)
			}
		})
	}
}

// Every upstream problem collapses to one opaque message: the status the
// endpoint returned would make this a reachability oracle.
func TestRunSkillUpstreamFailureIsOpaque(t *testing.T) {
	fake := &scriptedOpenAI{replies: []fakeReply{{status: http.StatusInternalServerError}}}
	h := newSkillServer(t, fake, Config{})

	w := runSkillReq(t, h, `{"skill":"adr-split","input":"# ADR"}`)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("POST run-skill: got %d (body=%s), want 502", w.Code, w.Body)
	}
	if body := w.Body.String(); !strings.Contains(body, connectionFailedMessage) || strings.Contains(body, "500") {
		t.Errorf("body = %q, want the opaque %q", body, connectionFailedMessage)
	}
}

func TestRunSkillRequestValidation(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{name: "not JSON", body: `{`, want: http.StatusBadRequest},
		{name: "no skill", body: `{"input":"# ADR"}`, want: http.StatusBadRequest},
		{name: "blank skill", body: `{"skill":"  ","input":"# ADR"}`, want: http.StatusBadRequest},
		{name: "no input", body: `{"skill":"adr-split"}`, want: http.StatusBadRequest},
		{name: "blank input", body: `{"skill":"adr-split","input":"\n"}`, want: http.StatusBadRequest},
		{
			name: "oversized input",
			body: `{"skill":"adr-split","input":"` + strings.Repeat("a", maxADRBytes+1) + `"}`,
			want: http.StatusRequestEntityTooLarge,
		},
	}
	fake := &scriptedOpenAI{replies: []fakeReply{{content: "unreachable"}}}
	h := newSkillServer(t, fake, Config{})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if w := runSkillReq(t, h, tt.body); w.Code != tt.want {
				t.Fatalf("POST run-skill: got %d (body=%s), want %d", w.Code, w.Body, tt.want)
			}
		})
	}
	if len(fake.requests()) != 0 {
		t.Error("a rejected request reached the upstream")
	}
}

// A body past the shared 1 MiB request cap is refused by readBody before the
// input length is looked at.
func TestRunSkillRejectsOversizedBody(t *testing.T) {
	fake := &scriptedOpenAI{replies: []fakeReply{{content: "unreachable"}}}
	h := newSkillServer(t, fake, Config{})

	body := `{"skill":"adr-split","input":"` + strings.Repeat("a", maxBodyBytes+1) + `"}`
	if w := runSkillReq(t, h, body); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("POST run-skill: got %d (body=%s), want 413", w.Code, w.Body)
	}
}

// The endpoint sits behind the same auth check as every other /api route.
func TestRunSkillRequiresAuth(t *testing.T) {
	h, _ := newTestServer(t, Config{Token: "s3cret"})
	w := runSkillReq(t, h, `{"skill":"adr-split","input":"# ADR"}`)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("POST run-skill without auth: got %d, want 401", w.Code)
	}
}

// A configuration problem is the caller's own settings, so it is reported as
// a 400 rather than collapsed into the opaque upstream failure.
func TestRigClientRejectsUnusableConfig(t *testing.T) {
	st := newTestStore(t)
	s := newServer(Config{}, st)

	tests := []struct {
		name string
		cfg  aiConfig
	}{
		{name: "no base URL", cfg: aiConfig{model: "m"}},
		{name: "blank base URL", cfg: aiConfig{baseURL: "   ", model: "m"}},
		{name: "not a URL", cfg: aiConfig{baseURL: "not a url", model: "m"}},
		{name: "wrong scheme", cfg: aiConfig{baseURL: "ftp://ai.invalid", model: "m"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := s.rigClient(tt.cfg)
			if client != nil {
				t.Fatalf("rigClient returned a client for %+v", tt.cfg)
			}
			var ae *aiError
			if !errors.As(err, &ae) || ae.code != http.StatusBadRequest {
				t.Fatalf("rigClient error = %v, want a 400 aiError", err)
			}
		})
	}
	if client, err := s.rigClient(aiConfig{baseURL: "https://ai.invalid", model: "m", key: "k"}); client == nil || err != nil {
		t.Fatalf("rigClient(valid) = %v, %v", client, err)
	}
}

// A run outlives the server-wide write timeout, so the handler extends its own
// write deadline. That only works if every writer between the connection and
// the handler unwraps, which the logging wrapper is the one link in.
func TestExtendWriteDeadlineReachesTheConnection(t *testing.T) {
	errs := make(chan error, 1)
	srv := httptest.NewServer(withLogging(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		errs <- http.NewResponseController(w).SetWriteDeadline(time.Now().Add(skillWriteDeadline))
		w.WriteHeader(http.StatusNoContent)
	})))
	defer srv.Close()

	res, err := srv.Client().Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer res.Body.Close()
	if err := <-errs; err != nil {
		t.Fatalf("SetWriteDeadline through the served chain: %v", err)
	}
}

// The o-series and gpt-5 models reject max_tokens outright, so a run against
// one states its budget under the other field name — the same rule the
// single-shot completions follow.
func TestRunSkillBudgetFieldFollowsTheModel(t *testing.T) {
	tests := []struct {
		model                   string
		wantMaxCompletionTokens bool
	}{
		{model: "test-model"},
		{model: "gpt-5", wantMaxCompletionTokens: true},
		{model: "o3-mini", wantMaxCompletionTokens: true},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			fake := &scriptedOpenAI{replies: []fakeReply{
				{toolCalls: []fakeToolCall{{name: "propose_card", args: `{"title":"Ship it"}`}}},
				{content: "Proposed one story."},
			}}
			h := newSkillServerWithModel(t, fake, Config{}, tt.model)

			w := runSkillReq(t, h, `{"skill":"adr-split","input":"# ADR"}`)
			if w.Code != http.StatusOK {
				t.Fatalf("POST run-skill: got %d (body=%s)", w.Code, w.Body)
			}
			reqs := fake.requests()
			if len(reqs) != 2 {
				t.Fatalf("upstream rounds = %d, want 2", len(reqs))
			}
			for i, body := range reqs {
				// decodeAIRequest already refuses a request that states both
				// fields or neither; which one it is, is the point here.
				sent := decodeAIRequest(t, body)
				if got := sent.MaxCompletionTokens != nil; got != tt.wantMaxCompletionTokens {
					t.Fatalf("round %d max_completion_tokens = %t, want %t: %s", i, got, tt.wantMaxCompletionTokens, body)
				}
				if sent.budget() != aiStoriesMaxTokens {
					t.Errorf("round %d budget = %d, want %d", i, sent.budget(), aiStoriesMaxTokens)
				}
				if sent.Model != tt.model {
					t.Errorf("round %d model = %q, want %q", i, sent.Model, tt.model)
				}
			}
		})
	}
}

// A loop that runs out of rounds after proposing cards has still done the
// work: the accepted cards come back rather than being dropped, and the
// commentary says the run was cut short.
func TestRunSkillKeepsCardsProposedBeforeTheLimit(t *testing.T) {
	fake := &scriptedOpenAI{replies: []fakeReply{
		{toolCalls: []fakeToolCall{
			{name: "propose_card", args: `{"title":"Add the store package"}`},
			{name: "propose_card", args: `{"title":"Write migrations"}`},
		}},
		// Repeats to the end of the script, so the loop stops at the cap.
		{toolCalls: []fakeToolCall{{name: "list_tasks", args: `{}`}}},
	}}
	h := newSkillServer(t, fake, Config{})

	w := runSkillReq(t, h, `{"skill":"adr-split","input":"# ADR"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("POST run-skill: got %d (body=%s), want the proposed cards", w.Code, w.Body)
	}
	got := decodeSkillRun(t, w)
	if len(got.Cards) != 2 || got.Cards[0].Title != "Add the store package" {
		t.Fatalf("cards = %+v, want the two proposed before the limit", got.Cards)
	}
	if got.Commentary != skillIterationLimitMessage {
		t.Errorf("commentary = %q, want %q", got.Commentary, skillIterationLimitMessage)
	}
	if rounds := len(fake.requests()); rounds != skillMaxIterations {
		t.Errorf("upstream rounds = %d, want the %d-round cap", rounds, skillMaxIterations)
	}
}

// A truncated reply after a proposal is the same partial run; a transport
// failure is not, because nothing says the endpoint ran the loop at all.
func TestRunSkillPartialResultsCoverOnlyBudgets(t *testing.T) {
	proposal := fakeReply{toolCalls: []fakeToolCall{{name: "propose_card", args: `{"title":"Ship it"}`}}}
	tests := []struct {
		name     string
		last     fakeReply
		wantCode int
		wantText string
	}{
		{
			name:     "truncated reply keeps the card",
			last:     fakeReply{content: "Proposing the fi", finish: "length"},
			wantCode: http.StatusOK,
			wantText: truncatedReplyMessage,
		},
		{
			name:     "upstream failure keeps nothing",
			last:     fakeReply{status: http.StatusInternalServerError},
			wantCode: http.StatusBadGateway,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &scriptedOpenAI{replies: []fakeReply{proposal, tt.last}}
			h := newSkillServer(t, fake, Config{})

			w := runSkillReq(t, h, `{"skill":"adr-split","input":"# ADR"}`)
			if w.Code != tt.wantCode {
				t.Fatalf("POST run-skill: got %d (body=%s), want %d", w.Code, w.Body, tt.wantCode)
			}
			if tt.wantCode != http.StatusOK {
				return
			}
			got := decodeSkillRun(t, w)
			if len(got.Cards) != 1 || got.Commentary != tt.wantText {
				t.Fatalf("run = %+v, want one card and %q", got, tt.wantText)
			}
		})
	}
}

// splitSkills keeps the invoked skill out of the loadable set, so a run cannot
// spend a round loading instructions it already has.
func TestSplitSkills(t *testing.T) {
	catalogue := []skill{
		{Name: "adr-split", Description: "split", Body: "split body"},
		{Name: "extra", Description: "extra", Body: "extra body"},
		{Name: "third", Description: "third", Body: "third body"},
	}
	tests := []struct {
		name       string
		want       string
		wantOthers []string
		wantFound  bool
	}{
		{name: "adr-split", want: "split body", wantOthers: []string{"extra", "third"}, wantFound: true},
		{name: " extra ", want: "extra body", wantOthers: []string{"adr-split", "third"}, wantFound: true},
		{name: "missing", wantOthers: []string{"adr-split", "extra", "third"}},
		{name: "", wantOthers: []string{"adr-split", "extra", "third"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			skill, others, found := splitSkills(catalogue, tt.name)
			if found != tt.wantFound || skill.Body != tt.want {
				t.Fatalf("splitSkills(%q) = %+v, %t; want body %q, found %t", tt.name, skill, found, tt.want, tt.wantFound)
			}
			got := make([]string, 0, len(others))
			for _, other := range others {
				got = append(got, other.Name)
			}
			if strings.Join(got, ",") != strings.Join(tt.wantOthers, ",") {
				t.Fatalf("others = %v, want %v", got, tt.wantOthers)
			}
		})
	}
}

// Every budget that reaches the wire is clamped to the ceiling, whatever the
// call site asked for. The constants are all under it today; the clamp is what
// keeps a future flow from sending a budget a strict server rejects with a 400
// no caller can act on.
func TestSkillRunBudgetIsClampedToTheCeiling(t *testing.T) {
	for _, tt := range []struct {
		name string
		ask  int64
		want int64
	}{
		{name: "over the ceiling", ask: aiMaxTokensCeiling * 4, want: aiMaxTokensCeiling},
		{name: "unstated", ask: 0, want: 1},
		{name: "in range", ask: aiStoryMaxTokens, want: aiStoryMaxTokens},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := skillBudget(tt.ask); got != tt.want {
				t.Errorf("skillBudget(%d) = %d, want %d", tt.ask, got, tt.want)
			}
			fake := &scriptedOpenAI{replies: []fakeReply{{content: "Nothing to propose."}}}
			upstream := httptest.NewServer(fake.handler())
			defer upstream.Close()
			t.Setenv("KB_AI_ALLOW_PRIVATE", "1")
			st := newTestStore(t)
			s := newServer(Config{}, st)
			configureAI(t, s.handler(), upstream.URL, "test-model", "sk-test")

			if _, err := s.runSkill(context.Background(), "default", skillScopeReadOnly, "adr-split", "# ADR", 1, tt.ask); err != nil {
				t.Fatalf("runSkill: %v", err)
			}
			reqs := fake.requests()
			if len(reqs) != 1 {
				t.Fatalf("upstream rounds = %d, want 1", len(reqs))
			}
			if got := decodeAIRequest(t, reqs[0]).budget(); got != tt.want {
				t.Errorf("wire budget = %d, want %d", got, tt.want)
			}
		})
	}
}

// runnerSystemPrompt is prepended to every skill body in the same system
// message, so a skill that forbids the tools it mandates leaves the run's
// behaviour to the model. story-draft carries the extra rule that a
// find_similar hit never cancels the proposal: POST /api/ai/story owes its
// caller one card, and a run that proposes none is reported as an upstream
// failure the user reads as "connection failed".
func TestBuiltInSkillsAgreeWithTheRunnerRules(t *testing.T) {
	st := newTestStore(t)
	s := newServer(Config{}, st)
	skills, err := s.loadSkills()
	if err != nil {
		t.Fatalf("loadSkills: %v", err)
	}
	if len(skills) == 0 {
		t.Fatal("no built-in skills loaded")
	}
	for _, skill := range skills {
		if strings.Contains(strings.ToLower(skill.Body), "any other tool") {
			t.Errorf("skill %q forbids the tools runnerSystemPrompt mandates", skill.Name)
		}
	}
	story, _, found := splitSkills(skills, storyDraftSkillName)
	if !found {
		t.Fatalf("skill %q is not in the built-in catalogue", storyDraftSkillName)
	}
	for _, want := range []string{"find_similar", "Always call it"} {
		if !strings.Contains(story.Body, want) {
			t.Errorf("story-draft body is missing %q: %s", want, story.Body)
		}
	}
}
