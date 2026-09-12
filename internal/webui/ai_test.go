package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RandomCodeSpace/kb/internal/ai"
	"github.com/RandomCodeSpace/kb/internal/cliapp"
	"github.com/RandomCodeSpace/kb/internal/forge"
	"github.com/RandomCodeSpace/kb/internal/store"
)

// aiToolCall is one propose_card (or other tool) call the fake upstream
// scripts into a reply.
type aiToolCall struct {
	name string
	args string
}

// aiReply is one scripted OpenAI-compatible chat completion.
type aiReply struct {
	content string
	calls   []aiToolCall
	status  int
}

// aiUpstream speaks the chat-completions shape the ai package expects, so a
// run exercises the real runner without a model.
type aiUpstream struct {
	mu      sync.Mutex
	replies []aiReply
	reqs    []string
}

func (u *aiUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	u.mu.Lock()
	u.reqs = append(u.reqs, string(body))
	index := len(u.reqs) - 1
	if index >= len(u.replies) {
		index = len(u.replies) - 1
	}
	reply := aiReply{content: "done"}
	if index >= 0 {
		reply = u.replies[index]
	}
	u.mu.Unlock()
	if reply.status != 0 {
		http.Error(w, "upstream failure", reply.status)
		return
	}
	message := map[string]any{"role": "assistant", "content": reply.content}
	if len(reply.calls) > 0 {
		calls := make([]any, 0, len(reply.calls))
		for i, call := range reply.calls {
			calls = append(calls, map[string]any{
				"id": fmt.Sprintf("call-%d-%d", len(u.reqs), i), "type": "function",
				"function": map[string]any{"name": call.name, "arguments": call.args},
			})
		}
		message["tool_calls"] = calls
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{
		"index": 0, "message": message, "finish_reason": "stop",
	}}})
}

func (u *aiUpstream) requests() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.reqs...)
}

// aiHandler opens a store in a temp data dir and returns the handler, the
// store and the data dir.
func aiHandler(t *testing.T) (http.Handler, *store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	// Deterministic regardless of the developer's environment: the fake
	// upstream is on loopback, which the guarded dialer refuses otherwise.
	t.Setenv("KB_AI_ALLOW_PRIVATE", "1")
	st, err := cliapp.OpenLocalStore(dir, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return NewHandler(st, "default", dir, "v-test"), st, dir
}

// aiConfiguredHandler points the store's AI settings at a fake upstream
// serving the given replies.
func aiConfiguredHandler(t *testing.T, replies ...aiReply) (http.Handler, *aiUpstream) {
	t.Helper()
	h, st, _ := aiHandler(t)
	upstream := &aiUpstream{replies: replies}
	srv := httptest.NewServer(upstream)
	t.Cleanup(srv.Close)
	base, model, key := srv.URL, "gpt-4o", "sk-test"
	if _, err := st.SetAISettings("default", &base, &model, &key); err != nil {
		t.Fatal(err)
	}
	return h, upstream
}

// wantNoTasks asserts the review-only contract: a run never writes a card.
func wantNoTasks(t *testing.T, h http.Handler) {
	t.Helper()
	body := wantStatus(t, call(t, h, "GET", "/api/tasks", nil), http.StatusOK)
	if tasks, _ := body["tasks"].([]any); len(tasks) != 0 {
		t.Fatalf("run persisted %d tasks", len(tasks))
	}
}

func TestAIStatus(t *testing.T) {
	h, st, _ := aiHandler(t)

	body := wantStatus(t, call(t, h, "GET", "/api/ai/status", nil), http.StatusOK)
	if body["configured"] != false || body["baseURL"] != "" || body["model"] != "" || body["hasKey"] != false {
		t.Fatalf("unconfigured status = %v", body)
	}

	base, model, key := "https://api.example.com", "gpt-4o", "sk-test"
	if _, err := st.SetAISettings("default", &base, &model, &key); err != nil {
		t.Fatal(err)
	}
	body = wantStatus(t, call(t, h, "GET", "/api/ai/status", nil), http.StatusOK)
	if body["configured"] != true || body["baseURL"] != base || body["model"] != model || body["hasKey"] != true {
		t.Fatalf("configured status = %v", body)
	}
	if strings.Contains(rec(t, h, "GET", "/api/ai/status"), key) {
		t.Fatal("status leaked the API key")
	}
}

func rec(t *testing.T, h http.Handler, method, path string) string {
	t.Helper()
	return call(t, h, method, path, nil).Body.String()
}

func TestAIStatusStoreError(t *testing.T) {
	h, st, _ := aiHandler(t)
	st.Close()
	wantError(t, call(t, h, "GET", "/api/ai/status", nil), http.StatusInternalServerError, "closed")
}

func TestAISkills(t *testing.T) {
	h, _, dir := aiHandler(t)
	body := wantStatus(t, call(t, h, "GET", "/api/ai/skills", nil), http.StatusOK)
	skills, _ := body["skills"].([]any)
	names := map[string]bool{}
	for _, entry := range skills {
		skill, _ := entry.(map[string]any)
		name, _ := skill["name"].(string)
		if desc, _ := skill["description"].(string); desc == "" {
			t.Fatalf("skill %q has no description", name)
		}
		names[name] = true
	}
	for _, want := range []string{"story-draft", "adr-split", "import-transform"} {
		if !names[want] {
			t.Fatalf("skills %v missing %q", names, want)
		}
	}

	// A broken operator override fails the whole catalogue.
	if err := os.MkdirAll(filepath.Join(dir, "skills"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "skills", "bad.md"), []byte("no frontmatter"), 0o600); err != nil {
		t.Fatal(err)
	}
	wantError(t, call(t, h, "GET", "/api/ai/skills", nil), http.StatusBadGateway, "frontmatter")
}

func TestAIRunnerWithoutDataDir(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "kb.db"), []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if newAIRunner(st, "") == nil {
		t.Fatal("runner is nil")
	}
}

func TestAIDraft(t *testing.T) {
	h, upstream := aiConfiguredHandler(t,
		aiReply{calls: []aiToolCall{{name: "propose_card", args: `{"title":"Ship the web UI","emoji":"🚀","desc":"body","prio":2,"due":"2030-01-02","effort":"M","tags":["web"],"checks":[{"text":"review","done":true}]}`}}},
		aiReply{content: "Proposed one card."},
	)

	body := wantStatus(t, call(t, h, "POST", "/api/ai/draft", map[string]any{"prompt": "ship the web ui"}), http.StatusOK)
	card, _ := body["card"].(map[string]any)
	if card["title"] != "Ship the web UI" || card["emoji"] != "🚀" || card["desc"] != "body" ||
		card["prio"] != float64(2) || card["due"] != "2030-01-02" || card["effort"] != "M" {
		t.Fatalf("card = %v", card)
	}
	if checks, _ := card["checks"].([]any); len(checks) != 1 {
		t.Fatalf("checks = %v", card["checks"])
	}
	if _, hasID := card["id"]; hasID {
		t.Fatal("draft carries an id")
	}
	if body["commentary"] != "Proposed one card." {
		t.Fatalf("commentary = %v", body["commentary"])
	}
	request := upstream.requests()[0]
	for _, want := range []string{"Skill: story-draft", "Create a new kanban card for this request:", "ship the web ui"} {
		if !strings.Contains(request, want) {
			t.Fatalf("request missing %q", want)
		}
	}
	// Read-only scope: the write and fetch tools are never offered.
	for _, forbidden := range []string{"update_task", "fetch_link"} {
		if strings.Contains(request, forbidden) {
			t.Fatalf("request offered %q", forbidden)
		}
	}
	wantNoTasks(t, h)
}

func TestAIDraftRewritesCurrentCard(t *testing.T) {
	h, upstream := aiConfiguredHandler(t,
		aiReply{calls: []aiToolCall{{name: "propose_card", args: `{"title":"Rewritten"}`}}},
		aiReply{content: "Updated."},
	)

	body := wantStatus(t, call(t, h, "POST", "/api/ai/draft", map[string]any{
		"prompt": "tighten the title",
		"card":   map[string]any{"title": "old", "desc": "old body", "prio": 1},
	}), http.StatusOK)
	if card, _ := body["card"].(map[string]any); card["title"] != "Rewritten" {
		t.Fatalf("card = %v", body["card"])
	}
	request := upstream.requests()[0]
	for _, want := range []string{"Update the kanban card according to this request:", "Current card JSON:", `\"title\":\"old\"`, `\"checks\":[]`} {
		if !strings.Contains(request, want) {
			t.Fatalf("request missing %q in %s", want, request)
		}
	}
	wantNoTasks(t, h)
}

func TestAIDraftNoUsableCard(t *testing.T) {
	h, _ := aiConfiguredHandler(t, aiReply{content: "I could not draft anything."})
	wantError(t, call(t, h, "POST", "/api/ai/draft", map[string]any{"prompt": "x"}), http.StatusBadGateway, "no usable card")
}

func TestAISplit(t *testing.T) {
	h, upstream := aiConfiguredHandler(t,
		aiReply{calls: []aiToolCall{
			{name: "propose_card", args: `{"title":"Story one","checks":[{"text":"done"}]}`},
			{name: "propose_card", args: `{"title":"Story two","prio":1}`},
		}},
		aiReply{content: "Split into two stories."},
	)

	body := wantStatus(t, call(t, h, "POST", "/api/ai/split", map[string]any{
		"text": "# ADR 1\n\nUse SQLite.", "max": 2,
	}), http.StatusOK)
	cards, _ := body["cards"].([]any)
	if len(cards) != 2 {
		t.Fatalf("cards = %v", body["cards"])
	}
	first, _ := cards[0].(map[string]any)
	if first["title"] != "Story one" {
		t.Fatalf("first card = %v", first)
	}
	if body["commentary"] != "Split into two stories." {
		t.Fatalf("commentary = %v", body["commentary"])
	}
	if request := upstream.requests()[0]; !strings.Contains(request, "Skill: adr-split") || !strings.Contains(request, "Use SQLite.") {
		t.Fatalf("request = %s", request)
	}
	wantNoTasks(t, h)
}

func TestAISplitDefaultsCardCount(t *testing.T) {
	// An omitted max is the overlay's default of 8, not zero cards.
	h, _ := aiConfiguredHandler(t,
		aiReply{calls: []aiToolCall{{name: "propose_card", args: `{"title":"Only"}`}}},
		aiReply{content: "one"},
	)
	body := wantStatus(t, call(t, h, "POST", "/api/ai/split", map[string]any{"text": "# ADR"}), http.StatusOK)
	if cards, _ := body["cards"].([]any); len(cards) != 1 {
		t.Fatalf("cards = %v", body["cards"])
	}
}

func TestAIUnconfigured(t *testing.T) {
	h, _, _ := aiHandler(t)
	for _, tc := range []struct {
		path string
		body map[string]any
	}{
		{"/api/ai/draft", map[string]any{"prompt": "x"}},
		{"/api/ai/split", map[string]any{"text": "# ADR"}},
	} {
		body := wantError(t, call(t, h, "POST", tc.path, tc.body), http.StatusConflict, "not configured")
		if body["aiUnconfigured"] != true {
			t.Fatalf("%s: body = %v", tc.path, body)
		}
	}
}

func TestAIUpstreamFailure(t *testing.T) {
	h, _ := aiConfiguredHandler(t, aiReply{status: http.StatusInternalServerError})
	wantError(t, call(t, h, "POST", "/api/ai/draft", map[string]any{"prompt": "x"}), http.StatusBadGateway, "AI endpoint")
	wantError(t, call(t, h, "POST", "/api/ai/split", map[string]any{"text": "# ADR"}), http.StatusBadGateway, "AI endpoint")
}

func TestAIRunStoreError(t *testing.T) {
	h, st, _ := aiHandler(t)
	st.Close()
	wantError(t, call(t, h, "POST", "/api/ai/draft", map[string]any{"prompt": "x"}), http.StatusInternalServerError, "closed")
}

// stubRunner replaces the real runner so a failure mode with no upstream
// equivalent (a request that outlives its deadline) is still exercised.
type stubRunner struct{ err error }

func (s stubRunner) RunSkill(context.Context, string, ai.Scope, string, string, int, int64) (ai.RunResult, error) {
	return ai.RunResult{}, s.err
}

func (s stubRunner) LoadSkills() ([]ai.Skill, error) { return nil, s.err }

func TestAITimeout(t *testing.T) {
	h, _ := aiConfiguredHandler(t)
	previous := newAIRunner
	t.Cleanup(func() { newAIRunner = previous })
	newAIRunner = func(*store.Store, string) aiRunner {
		return stubRunner{err: &ai.Error{
			Code:    http.StatusBadGateway,
			Message: "the AI endpoint did not answer before the request timed out",
			Cause:   context.DeadlineExceeded,
		}}
	}
	wantError(t, call(t, h, "POST", "/api/ai/draft", map[string]any{"prompt": "x"}), http.StatusGatewayTimeout, "timed out")
}

func TestAIBadRequests(t *testing.T) {
	h, _ := aiConfiguredHandler(t)
	cases := []struct {
		name, path string
		body       any
		code       int
		contains   string
	}{
		{"draft malformed", "/api/ai/draft", "{", http.StatusBadRequest, "malformed JSON"},
		{"draft empty body", "/api/ai/draft", nil, http.StatusBadRequest, "JSON object"},
		{"draft blank prompt", "/api/ai/draft", map[string]any{"prompt": "  "}, http.StatusBadRequest, "prompt must not be empty"},
		{"split malformed", "/api/ai/split", `{"max":"eight"}`, http.StatusBadRequest, "malformed JSON"},
		{"split blank text", "/api/ai/split", map[string]any{"text": ""}, http.StatusBadRequest, "text must not be empty"},
		{"split oversized", "/api/ai/split", map[string]any{"text": strings.Repeat("a", maxADRBytes+1)}, http.StatusRequestEntityTooLarge, "64 KiB"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wantError(t, call(t, h, "POST", tc.path, tc.body), tc.code, tc.contains)
		})
	}
}

func TestAIMethodNotAllowed(t *testing.T) {
	h, _, _ := aiHandler(t)
	for _, tc := range []struct{ method, path string }{
		{"POST", "/api/ai/status"},
		{"POST", "/api/ai/skills"},
		{"GET", "/api/ai/draft"},
		{"GET", "/api/ai/split"},
	} {
		rec := call(t, h, tc.method, tc.path, nil)
		wantError(t, rec, http.StatusMethodNotAllowed, "not allowed")
		if rec.Header().Get("Allow") == "" {
			t.Fatalf("%s %s: no Allow header", tc.method, tc.path)
		}
	}
}

// The runner observes the actual context passed by both web skill endpoints.
type contextRunner struct {
	stubRunner
	run func(context.Context) (ai.RunResult, error)
}

func (s contextRunner) RunSkill(ctx context.Context, _ string, _ ai.Scope, _, _ string, _ int, _ int64) (ai.RunResult, error) {
	return s.run(ctx)
}

func TestAISkillDeadlineAndCancellation(t *testing.T) {
	for _, path := range []string{"/api/ai/draft", "/api/ai/split"} {
		t.Run(path, func(t *testing.T) {
			h, _ := aiConfiguredHandler(t)
			previous := newAIRunner
			t.Cleanup(func() { newAIRunner = previous })
			for _, mode := range []string{"bounded", "parent deadline", "parent cancelled"} {
				t.Run(mode, func(t *testing.T) {
					checkAISkillContext(t, h, path, mode)
				})
			}
		})
	}
}

func aiSkillParentContext(t *testing.T, mode string) (context.Context, context.CancelFunc, time.Time) {
	t.Helper()
	parent, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	var deadline time.Time
	if mode == "parent deadline" {
		deadline = time.Now().Add(time.Minute)
		var cancelDeadline context.CancelFunc
		parent, cancelDeadline = context.WithDeadline(parent, deadline)
		t.Cleanup(cancelDeadline)
	}
	return parent, cancel, deadline
}

func checkAISkillDeadline(t *testing.T, ctx context.Context, mode string, before, parentDeadline time.Time) {
	t.Helper()
	deadline, ok := ctx.Deadline()
	if !ok || deadline.Before(before) || deadline.After(time.Now().Add(forge.SkillRunDeadline)) {
		t.Fatalf("skill deadline = %v, present %v", deadline, ok)
	}
	if mode == "bounded" && deadline.Before(before.Add(forge.SkillRunDeadline)) {
		t.Fatalf("skill deadline %v shorter than shared budget", deadline)
	}
	if mode == "parent deadline" && !deadline.Equal(parentDeadline) {
		t.Fatalf("parent deadline changed: %v, want %v", deadline, parentDeadline)
	}
}

func cancelAISkillRequest(t *testing.T, ctx context.Context, cancel context.CancelFunc) (ai.RunResult, error) {
	t.Helper()
	cancel()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("request cancellation did not reach runner")
	}
	return ai.RunResult{}, ctx.Err()
}

func checkAISkillContext(t *testing.T, h http.Handler, path, mode string) {
	t.Helper()
	parent, cancel, parentDeadline := aiSkillParentContext(t, mode)
	var runContext context.Context
	before := time.Now()
	newAIRunner = func(*store.Store, string) aiRunner {
		return contextRunner{run: func(ctx context.Context) (ai.RunResult, error) {
			runContext = ctx
			checkAISkillDeadline(t, ctx, mode, before, parentDeadline)
			if mode == "parent cancelled" {
				return cancelAISkillRequest(t, ctx, cancel)
			}
			return ai.RunResult{Cards: []ai.Draft{{Title: "draft"}}}, nil
		}}
	}
	body := `{"prompt":"draft"}`
	if path == "/api/ai/split" {
		body = `{"text":"split"}`
	}
	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1"+path, strings.NewReader(body)).WithContext(parent)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if runContext == nil {
		t.Fatalf("runner not called: %d %s", w.Code, w.Body.String())
	}
	if runContext.Err() != context.Canceled {
		t.Fatalf("runner context not released after request: %v", runContext.Err())
	}
	want := http.StatusOK
	if mode == "parent cancelled" {
		want = http.StatusBadGateway
	}
	if w.Code != want {
		t.Fatalf("status = %d, want %d: %s", w.Code, want, w.Body.String())
	}
}
