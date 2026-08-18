package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestAIEndpoint(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		// The SDK appends chat/completions to this root, so it must keep the
		// trailing slash or the last path segment is resolved away.
		{"https://api.example.com", "https://api.example.com/v1/", false},
		{"https://api.example.com/", "https://api.example.com/v1/", false},
		{"https://api.example.com/v1", "https://api.example.com/v1/", false},
		{"https://api.example.com/v1/", "https://api.example.com/v1/", false},
		{"http://localhost:1234/proxy", "http://localhost:1234/proxy/v1/", false},
		{" https://api.example.com/v1 ", "https://api.example.com/v1/", false},
		// Userinfo would be a credential stored in the clear and echoed back
		// to the browser by GET /api/settings.
		{"https://user:pass@api.example.com/v1", "", true},
		{"https://token@api.example.com/v1", "", true},
		{"https://api.example.com/v1?x=y", "", true},
		{"https://api.example.com/v1#fragment", "", true},
		{"ftp://api.example.com", "", true},
		{"file:///etc/passwd", "", true},
		{"not a url", "", true},
		{"", "", true},
	}
	for _, tt := range tests {
		got, err := aiEndpoint(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("aiEndpoint(%q) = %q, want error", tt.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("aiEndpoint(%q) unexpected error: %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("aiEndpoint(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// probeToolName and probeMaxTokens mirror the connection probe rig puts on the
// wire. Both are unexported there, so the shape the settings form depends on is
// restated here and asserted rather than assumed.
const (
	probeToolName  = "ping"
	probeMaxTokens = 256
)

// fakeOpenAI records the last request and answers with content as the
// assistant message, adding a tool call to tool when one is set.
type fakeOpenAI struct {
	auth    string
	path    string
	header  http.Header
	reqBody []byte
	content string
	tool    string // when set, the reply calls this tool
	finish  string // finish_reason; empty means "stop"
	status  int    // 0 means 200
	calls   int
}

func (f *fakeOpenAI) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.calls++
		f.auth = r.Header.Get("Authorization")
		f.path = r.URL.Path
		f.header = r.Header.Clone()
		f.reqBody, _ = io.ReadAll(r.Body)
		if f.status != 0 {
			http.Error(w, "upstream boom", f.status)
			return
		}
		message := map[string]any{"role": "assistant", "content": f.content}
		if f.tool != "" {
			message["tool_calls"] = []any{map[string]any{
				"id":       "call-1",
				"type":     "function",
				"function": map[string]any{"name": f.tool, "arguments": `{"message":"pong"}`},
			}}
		}
		finish := f.finish
		if finish == "" {
			finish = "stop"
		}
		resp := map[string]any{
			"choices": []any{
				map[string]any{"index": 0, "message": message, "finish_reason": finish},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// wireChatMessage is one prompt message as an OpenAI-compatible server reads
// it off the wire.
type wireChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// wireChatRequest is what the client actually puts on the wire, decoded the way
// an OpenAI-compatible server would read it.
type wireChatRequest struct {
	Model               string            `json:"model"`
	Messages            []wireChatMessage `json:"messages"`
	MaxTokens           *int64            `json:"max_tokens"`
	MaxCompletionTokens *int64            `json:"max_completion_tokens"`
	ResponseFormat      *struct {
		Type string `json:"type"`
	} `json:"response_format"`
	Tools []struct {
		Type     string `json:"type"`
		Function struct {
			Name       string         `json:"name"`
			Parameters map[string]any `json:"parameters"`
		} `json:"function"`
	} `json:"tools"`
}

// decodeAIRequest reads a recorded upstream request and asserts the one
// invariant every AI call shares: an explicit output budget, stated under
// exactly one of the two field names. Omitting it lets the upstream default
// truncate a JSON reply mid-object; stating it twice is rejected upstream.
func decodeAIRequest(t *testing.T, body []byte) wireChatRequest {
	t.Helper()
	var sent wireChatRequest
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatalf("upstream request JSON: %v (body=%s)", err, body)
	}
	if (sent.MaxTokens == nil) == (sent.MaxCompletionTokens == nil) {
		t.Fatalf("request must state exactly one output budget field: %s", body)
	}
	if got := sent.budget(); got < 1 || got > aiMaxTokensCeiling {
		t.Fatalf("output budget = %d, want 1..%d: %s", got, aiMaxTokensCeiling, body)
	}
	return sent
}

// budget is the output budget the request states, whichever field carries it.
func (r wireChatRequest) budget() int64 {
	if r.MaxTokens != nil {
		return *r.MaxTokens
	}
	if r.MaxCompletionTokens != nil {
		return *r.MaxCompletionTokens
	}
	return 0
}

// configureAI stores AI settings for the open-mode "default" user through
// the API so the key round-trips through encryption.
func configureAI(t *testing.T, h http.Handler, baseURL, model, key string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"ai_base_url": baseURL, "ai_model": model, "ai_key": key})
	if w := doReq(t, h, "PUT", "/api/settings", string(body), nil); w.Code != http.StatusNoContent {
		t.Fatalf("PUT settings: got %d (body=%s)", w.Code, w.Body)
	}
}

// recordingUpstream serves the scripted fake and remembers the credential and
// path of the last request, which the script itself does not keep.
type recordingUpstream struct {
	auth string
	path string
}

func newRecordingUpstream(t *testing.T, fake *scriptedOpenAI) (*httptest.Server, *recordingUpstream) {
	t.Helper()
	rec := &recordingUpstream{}
	inner := fake.handler()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.auth, rec.path = r.Header.Get("Authorization"), r.URL.Path
		inner(w, r)
	}))
	t.Cleanup(upstream.Close)
	return upstream, rec
}

// draftStory configures the server against upstream and posts one story
// request. Every story test drives the endpoint the frozen UI drives.
func draftStory(t *testing.T, upstreamURL, model, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	h, _ := newTestServer(t, Config{})
	configureAI(t, h, upstreamURL, model, key)
	return doReq(t, h, "POST", "/api/ai/story", body, nil)
}

// The endpoint's response is one flat draft and stays one flat draft under the
// skill runner: the card the model proposed through propose_card, coerced and
// clamped, with no run envelope around it.
func TestAIStoryHappyPath(t *testing.T) {
	fake := &scriptedOpenAI{replies: []fakeReply{
		{toolCalls: []fakeToolCall{{name: "propose_card", args: `{"title":" Ship it ","emoji":"🛠️","desc":"Do the thing","prio":9,"due":"not-a-date","effort":"m","tags":["backend",42,""],"checks":[{"text":"step one","done":true},{"text":""},"junk"]}`}}},
		{content: "Proposed one card."},
	}}
	t.Setenv("KB_AI_ALLOW_PRIVATE", "1") // test upstream is on loopback
	upstream, rec := newRecordingUpstream(t, fake)

	w := draftStory(t, upstream.URL, "test-model", "sk-test-123", `{"mode":"create","prompt":"write a card"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("POST story: got %d (body=%s)", w.Code, w.Body)
	}
	// The decrypted key reached the upstream, on the joined /v1 path.
	if rec.auth != "Bearer sk-test-123" {
		t.Errorf("upstream Authorization = %q, want Bearer sk-test-123", rec.auth)
	}
	if rec.path != "/v1/chat/completions" {
		t.Errorf("upstream path = %q, want /v1/chat/completions", rec.path)
	}
	reqs := fake.requests()
	if len(reqs) != 2 {
		t.Fatalf("upstream rounds = %d, want 2", len(reqs))
	}
	// Every round goes to the configured model with the draft budget, and the
	// request carries the prompt verbatim as the only user message.
	for i, body := range reqs {
		sent := decodeAIRequest(t, body)
		if sent.Model != "test-model" {
			t.Errorf("request %d model = %v, want test-model", i, sent.Model)
		}
		if sent.budget() != aiStoryMaxTokens {
			t.Errorf("request %d output budget = %d, want %d", i, sent.budget(), aiStoryMaxTokens)
		}
	}
	sent := decodeAIRequest(t, reqs[0])
	if len(sent.Messages) != 2 || sent.Messages[1].Role != "user" {
		t.Fatalf("first request messages = %+v, want a system prompt and the request", sent.Messages)
	}
	if want := "Create a new kanban card for this request:\nwrite a card"; sent.Messages[1].Content != want {
		t.Errorf("user message = %q, want %q", sent.Messages[1].Content, want)
	}
	if !strings.Contains(sent.Messages[0].Content, "Skill: story-draft") {
		t.Errorf("system prompt does not inject the story-draft skill: %q", sent.Messages[0].Content)
	}
	// A card is proposed through the tool, never written as JSON in the reply,
	// and the run stays read-only: the draft is not accepted yet.
	if !offersTool(sent, "propose_card") {
		t.Errorf("tools = %+v, want propose_card offered", sent.Tools)
	}
	for _, name := range []string{"update_task", "fetch_link"} {
		if offersTool(sent, name) {
			t.Errorf("story run offered %q: %+v", name, sent.Tools)
		}
	}
	// The draft is coerced and clamped.
	var draft storyDraft
	if err := json.Unmarshal(w.Body.Bytes(), &draft); err != nil {
		t.Fatalf("draft JSON: %v (body=%s)", err, w.Body)
	}
	want := storyDraft{
		Title:  "Ship it",
		Emoji:  "🛠️",
		Desc:   "Do the thing",
		Prio:   4,   // clamped from 9
		Due:    "",  // invalid date dropped
		Effort: "M", // uppercased
		Tags:   []string{"backend"},
		Checks: []draftCheck{{Text: "step one", Done: true}},
	}
	if !reflect.DeepEqual(draft, want) {
		t.Errorf("draft = %+v, want %+v", draft, want)
	}
	// The frozen client reads the draft's own fields off the response root, so
	// the run envelope must not appear.
	var raw map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("raw story JSON: %v", err)
	}
	if _, ok := raw["title"]; !ok {
		t.Errorf("response = %v, want a flat draft object", raw)
	}
	for _, key := range []string{"cards", "commentary", "stories"} {
		if _, ok := raw[key]; ok {
			t.Errorf("response wrapped the draft in %q: %v", key, raw)
		}
	}
}

// A run that never called propose_card owes the caller nothing it can render,
// so it is an upstream failure — and, like every other 502 here, an opaque one.
func TestAIStoryWithoutAProposalIsAnUpstreamFailure(t *testing.T) {
	fake := &scriptedOpenAI{replies: []fakeReply{{content: "I would rather not."}}}
	upstream := httptest.NewServer(fake.handler())
	defer upstream.Close()

	t.Setenv("KB_AI_ALLOW_PRIVATE", "1")
	w := draftStory(t, upstream.URL, "m", "sk-t", `{"mode":"create","prompt":"write a card"}`)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("POST story: got %d (body=%s), want 502", w.Code, w.Body)
	}
	if got := strings.TrimSpace(w.Body.String()); got != connectionFailedMessage {
		t.Errorf("error body = %q, want the opaque %q", got, connectionFailedMessage)
	}
}

// Decorative model output must never make an otherwise usable draft fail the
// same field validation that protects every board write.
func TestAIDraftEmojiIsNormalizedNotRejected(t *testing.T) {
	tests := []struct {
		name  string
		emoji string
		want  string
	}{
		{name: "multiple emoji keep the leading token", emoji: "🚀✨", want: "🚀"},
		{name: "a shortcode is dropped", emoji: ":rocket:", want: ""},
		{name: "one valid emoji passes through", emoji: "🧭", want: "🧭"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args, err := json.Marshal(map[string]any{"title": "Ship the draft", "emoji": tt.emoji})
			if err != nil {
				t.Fatalf("marshal tool arguments: %v", err)
			}
			fake := &scriptedOpenAI{replies: []fakeReply{
				{toolCalls: []fakeToolCall{{name: "propose_card", args: string(args)}}},
				{content: "Proposed one story."},
			}}
			upstream := httptest.NewServer(fake.handler())
			defer upstream.Close()

			t.Setenv("KB_AI_ALLOW_PRIVATE", "1")
			h, _ := newTestServer(t, Config{})
			configureAI(t, h, upstream.URL, "m", "")

			w := doReq(t, h, "POST", "/api/ai/stories", `{"adr":"# Ship the draft"}`, nil)
			if w.Code != http.StatusOK {
				t.Fatalf("POST stories: got %d (body=%s)", w.Code, w.Body)
			}
			var response struct {
				Stories []storyDraft `json:"stories"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatalf("stories JSON: %v (body=%s)", err, w.Body)
			}
			if len(response.Stories) != 1 {
				t.Fatalf("got %d stories, want the usable draft to survive: %+v", len(response.Stories), response.Stories)
			}
			draft := response.Stories[0]
			if draft.Emoji != tt.want {
				t.Errorf("emoji = %q, want %q", draft.Emoji, tt.want)
			}
			if err := validateDraft(draft); err != nil {
				t.Errorf("normalized draft was rejected: %v", err)
			}
		})
	}
}

// Update mode carries the card being rewritten into the run, so the model
// rewrites that card rather than inventing one from the prompt alone.
func TestAIStoryUpdateModePassesTask(t *testing.T) {
	fake := &scriptedOpenAI{replies: []fakeReply{
		{toolCalls: []fakeToolCall{{name: "propose_card", args: `{"title":"Updated"}`}}},
		{content: "Rewrote the card."},
	}}
	t.Setenv("KB_AI_ALLOW_PRIVATE", "1") // test upstream is on loopback
	upstream, rec := newRecordingUpstream(t, fake)

	// Base URL already ending in /v1 must not double the segment.
	body := `{"mode":"update","prompt":"tighten the title","task":{"title":"Old title","prio":2}}`
	w := draftStory(t, upstream.URL+"/v1", "m", "", body)
	if w.Code != http.StatusOK {
		t.Fatalf("POST story update: got %d (body=%s)", w.Code, w.Body)
	}
	if rec.path != "/v1/chat/completions" {
		t.Errorf("upstream path = %q, want /v1/chat/completions", rec.path)
	}
	// No key configured -> no Authorization header.
	if rec.auth != "" {
		t.Errorf("Authorization = %q, want empty", rec.auth)
	}
	sent := decodeAIRequest(t, fake.requests()[0])
	if len(sent.Messages) != 2 {
		t.Fatalf("messages = %+v, want a system prompt and the request", sent.Messages)
	}
	want := "Update the kanban card according to this request:\ntighten the title" +
		"\n\nCurrent card JSON:\n{\"title\":\"Old title\",\"prio\":2}"
	if sent.Messages[1].Content != want {
		t.Errorf("user message = %q, want %q", sent.Messages[1].Content, want)
	}
	var draft storyDraft
	if err := json.Unmarshal(w.Body.Bytes(), &draft); err != nil {
		t.Fatalf("draft JSON: %v", err)
	}
	if draft.Title != "Updated" || draft.Prio != 3 {
		t.Errorf("draft = %+v, want title Updated, default prio 3", draft)
	}
}

func TestAIStoryUpstreamError(t *testing.T) {
	fake := &scriptedOpenAI{replies: []fakeReply{{status: http.StatusInternalServerError}}}
	upstream := httptest.NewServer(fake.handler())
	defer upstream.Close()

	t.Setenv("KB_AI_ALLOW_PRIVATE", "1") // test upstream is on loopback
	w := draftStory(t, upstream.URL, "m", "sk-super-secret", `{"mode":"create","prompt":"p"}`)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("upstream 500: got %d, want 502 (body=%s)", w.Code, w.Body)
	}
	// Same opaque mapping as /api/ai/test: the upstream status stays in the
	// log, or the endpoint becomes a host/port reachability oracle.
	if got := strings.TrimSpace(w.Body.String()); got != "connection failed" {
		t.Errorf("error body = %q, want the opaque %q", got, "connection failed")
	}
	if strings.Contains(w.Body.String(), "500") {
		t.Errorf("error response leaks the upstream status: %q", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "sk-super-secret") {
		t.Errorf("error response leaks the key: %q", w.Body.String())
	}
}

// A misconfigured base URL is the caller's own setting, not a probe result,
// so it must still say what is wrong instead of collapsing to 502.
func TestAIStoryConfigErrorsStayVisible(t *testing.T) {
	h, _ := newTestServer(t, Config{})

	w := doReq(t, h, "POST", "/api/ai/story", `{"mode":"create","prompt":"p"}`, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unconfigured: got %d, want 400 (body=%s)", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), "not configured") {
		t.Errorf("error body = %q, want the configuration reason", w.Body.String())
	}
}

func TestAITestBlocksPrivateUpstreamOpaquely(t *testing.T) {
	fake := &fakeOpenAI{tool: probeToolName}
	upstream := httptest.NewServer(fake.handler())
	defer upstream.Close()

	// Without the opt-in, the dialer refuses the loopback upstream, and the
	// test endpoint reports one opaque message — never the refusal reason or
	// an upstream status, which would make it a reachability oracle.
	t.Setenv("KB_AI_ALLOW_PRIVATE", "")
	h, _ := newTestServer(t, Config{})
	configureAI(t, h, upstream.URL, "m", "sk-t")

	w := doReq(t, h, "POST", "/api/ai/test", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("test: got %d, want 200", w.Code)
	}
	var res struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("test JSON: %v", err)
	}
	if res.OK {
		t.Fatal("private upstream must be refused")
	}
	if res.Error != "connection failed" {
		t.Errorf("error = %q, want the opaque %q", res.Error, "connection failed")
	}
	if fake.reqBody != nil {
		t.Error("request reached the private upstream")
	}
}

// A proposal is untrusted input: it is arguments the model chose, checked by
// propose_card before the collector keeps it. Every value that lands on a
// single markdown line comes back stripped, or the proposal forges board lines.
func TestAIStoryRejectsWireBreakingProposal(t *testing.T) {
	hostile := map[string]any{
		"title": "Ship it\n- [x] forged !1 @2026-01-01",
		"desc":  "line one\nline two",
		"tags":  []any{"back\nend", "two words", "#hash", "ok"},
		"checks": []any{
			map[string]any{"text": "step\n- [ ] forged check", "done": false},
			map[string]any{"text": "clean step", "done": true},
		},
	}
	args, err := json.Marshal(hostile)
	if err != nil {
		t.Fatalf("marshal hostile draft: %v", err)
	}
	fake := &scriptedOpenAI{replies: []fakeReply{
		{toolCalls: []fakeToolCall{{name: "propose_card", args: string(args)}}},
		{content: "Proposed one card."},
	}}
	upstream := httptest.NewServer(fake.handler())
	defer upstream.Close()

	t.Setenv("KB_AI_ALLOW_PRIVATE", "1")
	w := draftStory(t, upstream.URL, "m", "", `{"mode":"create","prompt":"p"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("POST story: got %d (body=%s)", w.Code, w.Body)
	}
	var draft storyDraft
	if err := json.Unmarshal(w.Body.Bytes(), &draft); err != nil {
		t.Fatalf("draft JSON: %v", err)
	}
	if strings.ContainsAny(draft.Title, "\r\n") {
		t.Errorf("title kept a newline: %q", draft.Title)
	}
	if draft.Title != "Ship it- [x] forged !1 @2026-01-01" {
		t.Errorf("title = %q, want the newline stripped, not the text dropped", draft.Title)
	}
	// Multi-line descriptions are legal — the serializer indents each line.
	if draft.Desc != "line one\nline two" {
		t.Errorf("desc = %q, want both lines kept", draft.Desc)
	}
	// A tag is one wire token: newlines stripped, and the values that could
	// not survive as tokens are dropped rather than mangled into new ones.
	if want := []string{"backend", "ok"}; !reflect.DeepEqual(draft.Tags, want) {
		t.Errorf("tags = %v, want %v", draft.Tags, want)
	}
	for _, c := range draft.Checks {
		if strings.ContainsAny(c.Text, "\r\n") {
			t.Errorf("check text kept a newline: %q", c.Text)
		}
	}
	// The whole draft must survive the shared field validation used by every
	// other write path.
	if err := validateDraft(draft); err != nil {
		t.Errorf("coerced draft still fails wire validation: %v", err)
	}
}

// Coercion is not a rescue for every proposal: one that still fails the shared
// field validation is refused with an error the model reads, and only the card
// it proposes after fixing that reaches the wire.
func TestAIStoryRefusesAnUnusableProposal(t *testing.T) {
	fake := &scriptedOpenAI{replies: []fakeReply{
		{toolCalls: []fakeToolCall{{name: "propose_card", args: `{"title":"   "}`}}},
		{toolCalls: []fakeToolCall{{name: "propose_card", args: `{"title":"Ship the fixed card"}`}}},
		{content: "Fixed the title."},
	}}
	upstream := httptest.NewServer(fake.handler())
	defer upstream.Close()

	t.Setenv("KB_AI_ALLOW_PRIVATE", "1")
	w := draftStory(t, upstream.URL, "m", "sk-t", `{"mode":"create","prompt":"p"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("POST story: got %d (body=%s)", w.Code, w.Body)
	}
	var draft storyDraft
	if err := json.Unmarshal(w.Body.Bytes(), &draft); err != nil {
		t.Fatalf("draft JSON: %v (body=%s)", err, w.Body)
	}
	if draft.Title != "Ship the fixed card" {
		t.Errorf("draft = %+v, want the card proposed after the refusal", draft)
	}
	reqs := fake.requests()
	if len(reqs) < 2 {
		t.Fatalf("upstream rounds = %d, want the refusal fed back", len(reqs))
	}
	if !strings.Contains(string(reqs[1]), "card rejected") {
		t.Errorf("the refused proposal was not reported to the model: %s", reqs[1])
	}
}

// The model round trip now runs through the skill runner and is covered in
// ai_stories_runner_test.go; what stays here is the request validation that
// happens before any endpoint is contacted.
func TestAIStories(t *testing.T) {
	t.Run("bad requests", func(t *testing.T) {
		h, _ := newTestServer(t, Config{})

		big, err := json.Marshal(map[string]string{"adr": strings.Repeat("x", maxADRBytes+1)})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if w := doReq(t, h, "POST", "/api/ai/stories", string(big), nil); w.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("oversized ADR: got %d, want 413", w.Code)
		}
		for _, body := range []string{`{}`, `{"adr":"   "}`, `{"adr":"x","url":"https://forge.example/issues/1","source":"primary"}`} {
			w := doReq(t, h, "POST", "/api/ai/stories", body, nil)
			if w.Code != http.StatusBadRequest || strings.TrimSpace(w.Body.String()) != "provide adr or url" {
				t.Errorf("ADR/url XOR for %s: got %d %q, want exact 400", body, w.Code, w.Body.String())
			}
		}
		if w := doReq(t, h, "POST", "/api/ai/stories", `not json`, nil); w.Code != http.StatusBadRequest {
			t.Errorf("bad JSON: got %d, want 400", w.Code)
		}
		// An ADR exactly at the bound is accepted by the guard and only then
		// fails on the unconfigured endpoint.
		ok, err := json.Marshal(map[string]string{"adr": strings.Repeat("x", maxADRBytes)})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if w := doReq(t, h, "POST", "/api/ai/stories", string(ok), nil); w.Code != http.StatusBadRequest {
			t.Errorf("ADR at the 64 KiB bound: got %d, want 400 (unconfigured)", w.Code)
		}
	})
}

func TestAIStoriesFromForgeIssue(t *testing.T) {
	t.Run("caps discussion and adds server-owned provenance", func(t *testing.T) {
		body := strings.Repeat("a", maxADRBytes-80)
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.EscapedPath() {
			case "/forge/api/v4/projects/group%2Fproject/issues/42":
				_, _ = fmt.Fprintf(w, `{"iid":42,"title":"Issue title","description":%q,"web_url":"https://forge.example/group/project/-/issues/42"}`, body)
			case "/forge/api/v4/projects/group%2Fproject/issues/42/notes":
				_, _ = fmt.Fprintf(w, `[{"body":%q,"system":false}]`, strings.Repeat("界", 8<<10))
			default:
				http.NotFound(w, r)
			}
		}))
		defer upstream.Close()
		fake := &scriptedOpenAI{replies: []fakeReply{
			{toolCalls: []fakeToolCall{
				{name: "propose_card", args: `{"title":"first","tags":["model","link::evil#1"]}`},
				{name: "propose_card", args: `{"title":"second","tags":["link::evil#2"]}`},
			}},
			{content: "Proposed two stories."},
		}}
		aiUpstream := httptest.NewServer(fake.handler())
		defer aiUpstream.Close()

		t.Setenv("KB_FORGE_ALLOW_PRIVATE", "127.0.0.1")
		t.Setenv("KB_AI_ALLOW_PRIVATE", "1")
		h, st := newTestServer(t, Config{})
		configureAI(t, h, aiUpstream.URL, "m", "sk-t")
		baseURL, pat := upstream.URL+"/forge", "glpat-test"
		if _, err := st.SetForgeSource("default", "gitlab-main", "gitlab", &baseURL, &pat); err != nil {
			t.Fatalf("seed forge source: %v", err)
		}

		w := doReq(t, h, "POST", "/api/ai/stories", fmt.Sprintf(`{"url":%q,"source":"GITLAB-MAIN"}`, upstream.URL+"/forge/group/project/-/issues/42"), nil)
		if w.Code != http.StatusOK {
			t.Fatalf("POST forge stories: got %d body=%s", w.Code, w.Body)
		}
		var res struct {
			Stories []storyDraft `json:"stories"`
			Link    string       `json:"link"`
			URL     string       `json:"url"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
			t.Fatalf("forge stories JSON: %v", err)
		}
		if res.Link != "gitlab#42" || res.URL != "https://forge.example/group/project/-/issues/42" || len(res.Stories) != 2 {
			t.Fatalf("forge stories = %+v", res)
		}
		for i, story := range res.Stories {
			if strings.Contains(strings.Join(story.Tags, ","), "link::evil") || !strings.HasSuffix(strings.Join(story.Tags, ","), "link::gitlab#42") {
				t.Fatalf("story lacks server-owned link tag: %+v", story)
			}
			if i == 0 && strings.Join(story.Tags, ",") != "model,link::gitlab#42" {
				t.Fatalf("story tags = %q, want model plus only the server link", strings.Join(story.Tags, ","))
			}
		}
		// The prompt is the ADR alone: no story count is stated to the model.
		sent := decodeAIRequest(t, fake.requests()[0])
		adr := sent.Messages[len(sent.Messages)-1].Content
		if len(adr) > maxADRBytes || !utf8.ValidString(adr) || !strings.Contains(adr, "# Issue title\n\n") || !strings.Contains(adr, "## Discussion\n-") {
			t.Fatalf("bounded forge ADR = %d bytes %q", len(adr), adr)
		}
	})

	t.Run("multibyte title and body alone stay inside the ADR cap", func(t *testing.T) {
		body := strings.Repeat("界", maxADRBytes)
		var requests int
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests++
			switch r.URL.EscapedPath() {
			case "/api/v4/projects/group%2Fproject/issues/42":
				_, _ = fmt.Fprintf(w, `{"iid":42,"title":"界 title","description":%q,"web_url":"https://forge.example/issues/42"}`, body)
			case "/api/v4/projects/group%2Fproject/issues/42/notes":
				_, _ = io.WriteString(w, `[{"body":"comment","system":false}]`)
			default:
				http.NotFound(w, r)
			}
		}))
		defer upstream.Close()
		fake := &scriptedOpenAI{replies: []fakeReply{{content: "The document holds no shippable work."}}}
		aiUpstream := httptest.NewServer(fake.handler())
		defer aiUpstream.Close()

		t.Setenv("KB_FORGE_ALLOW_PRIVATE", "127.0.0.1")
		t.Setenv("KB_AI_ALLOW_PRIVATE", "1")
		h, st := newTestServer(t, Config{})
		configureAI(t, h, aiUpstream.URL, "m", "sk-t")
		baseURL := upstream.URL
		if _, err := st.SetForgeSource("default", "gitlab-main", "gitlab", &baseURL, nil); err != nil {
			t.Fatalf("seed forge source: %v", err)
		}

		w := doReq(t, h, "POST", "/api/ai/stories", fmt.Sprintf(`{"url":%q,"source":"gitlab-main"}`, upstream.URL+"/group/project/-/issues/42"), nil)
		if w.Code != http.StatusOK || requests != 2 {
			t.Fatalf("multibyte forge stories: got %d requests=%d body=%s", w.Code, requests, w.Body)
		}
		sent := decodeAIRequest(t, fake.requests()[0])
		adr := sent.Messages[len(sent.Messages)-1].Content
		if len(adr) > maxADRBytes || !utf8.ValidString(adr) || strings.Contains(adr, "## Discussion") {
			t.Fatalf("body-only capped ADR = %d bytes %q", len(adr), adr)
		}
	})

	t.Run("selected source mismatch makes no egress", func(t *testing.T) {
		forgeAHits, forgeBHits := 0, 0
		forgeA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			forgeAHits++
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer forgeA.Close()
		forgeB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			forgeBHits++
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer forgeB.Close()
		fake := &fakeOpenAI{content: `{"stories":[{"title":"unexpected"}]}`}
		aiUpstream := httptest.NewServer(fake.handler())
		defer aiUpstream.Close()

		t.Setenv("KB_FORGE_ALLOW_PRIVATE", "127.0.0.1")
		t.Setenv("KB_AI_ALLOW_PRIVATE", "1")
		h, st := newTestServer(t, Config{})
		configureAI(t, h, aiUpstream.URL, "m", "sk-t")
		baseA, baseB := forgeA.URL, forgeB.URL
		if _, err := st.SetForgeSource("default", "source-a", "gitlab", &baseA, nil); err != nil {
			t.Fatalf("seed source A: %v", err)
		}
		if _, err := st.SetForgeSource("default", "source-b", "gitlab", &baseB, nil); err != nil {
			t.Fatalf("seed source B: %v", err)
		}
		w := doReq(t, h, "POST", "/api/ai/stories", fmt.Sprintf(`{"url":%q,"source":"SOURCE-A"}`, baseB+"/group/project/-/issues/42"), nil)
		if w.Code != http.StatusBadRequest || strings.TrimSpace(w.Body.String()) != "reference does not match selected source" || forgeAHits != 0 || forgeBHits != 0 || fake.calls != 0 {
			t.Fatalf("mismatch response = %d %q forge A=%d forge B=%d AI=%d", w.Code, w.Body.String(), forgeAHits, forgeBHits, fake.calls)
		}
	})

	t.Run("forge failures use the shared opaque AI error", func(t *testing.T) {
		forge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer forge.Close()
		fake := &fakeOpenAI{content: `{"stories":[{"title":"unexpected"}]}`}
		aiUpstream := httptest.NewServer(fake.handler())
		defer aiUpstream.Close()

		t.Setenv("KB_FORGE_ALLOW_PRIVATE", "127.0.0.1")
		t.Setenv("KB_AI_ALLOW_PRIVATE", "1")
		h, st := newTestServer(t, Config{})
		configureAI(t, h, aiUpstream.URL, "m", "sk-t")
		baseURL := forge.URL
		if _, err := st.SetForgeSource("default", "gitlab-main", "gitlab", &baseURL, nil); err != nil {
			t.Fatalf("seed forge source: %v", err)
		}

		w := doReq(t, h, "POST", "/api/ai/stories", fmt.Sprintf(`{"url":%q,"source":"gitlab-main"}`, forge.URL+"/group/project/-/issues/42"), nil)
		if w.Code != http.StatusBadGateway || strings.TrimSpace(w.Body.String()) != "connection failed" || fake.calls != 0 {
			t.Fatalf("forge failure response = %d %q AI calls=%d", w.Code, w.Body.String(), fake.calls)
		}
	})
}

func TestAIStoryBadRequests(t *testing.T) {
	h, _ := newTestServer(t, Config{})

	if w := doReq(t, h, "POST", "/api/ai/story", `{"mode":"bogus","prompt":"p"}`, nil); w.Code != http.StatusBadRequest {
		t.Errorf("bad mode: got %d, want 400", w.Code)
	}
	if w := doReq(t, h, "POST", "/api/ai/story", `{"mode":"create","prompt":"  "}`, nil); w.Code != http.StatusBadRequest {
		t.Errorf("empty prompt: got %d, want 400", w.Code)
	}
	if w := doReq(t, h, "POST", "/api/ai/story", `not json`, nil); w.Code != http.StatusBadRequest {
		t.Errorf("bad JSON: got %d, want 400", w.Code)
	}
	// Configured base URL missing -> 400, not 502.
	if w := doReq(t, h, "POST", "/api/ai/story", `{"mode":"create","prompt":"p"}`, nil); w.Code != http.StatusBadRequest {
		t.Errorf("unconfigured: got %d, want 400", w.Code)
	}
}

func TestAITestEndpoint(t *testing.T) {
	fake := &fakeOpenAI{tool: probeToolName}
	upstream := httptest.NewServer(fake.handler())
	defer upstream.Close()

	t.Setenv("KB_AI_ALLOW_PRIVATE", "1") // test upstream is on loopback
	h, _ := newTestServer(t, Config{})

	// Unconfigured -> ok:false with an error, still 200.
	w := doReq(t, h, "POST", "/api/ai/test", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("unconfigured test: got %d, want 200", w.Code)
	}
	var res struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("test JSON: %v", err)
	}
	if res.OK || res.Error == "" {
		t.Errorf("unconfigured test = %+v, want ok:false with error", res)
	}

	configureAI(t, h, upstream.URL, "test-model", "sk-t")
	w = doReq(t, h, "POST", "/api/ai/test", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("test: got %d, want 200", w.Code)
	}
	res.OK, res.Error = false, ""
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("test JSON: %v", err)
	}
	if !res.OK || res.Error != "" {
		t.Errorf("test = %+v, want ok:true", res)
	}
	// It was a tool-call probe with room to answer, not a 1-token ping.
	sent := decodeAIRequest(t, fake.reqBody)
	if *sent.MaxTokens != probeMaxTokens {
		t.Errorf("max_tokens = %d, want %d", *sent.MaxTokens, probeMaxTokens)
	}
	if len(sent.Tools) != 1 || sent.Tools[0].Function.Name != probeToolName {
		t.Fatalf("tools = %+v, want one %q function tool", sent.Tools, probeToolName)
	}
	if sent.Tools[0].Type != "function" || sent.Tools[0].Function.Parameters["type"] != "object" {
		t.Errorf("probe tool = %+v, want a function tool with an object schema", sent.Tools[0])
	}
	if fake.auth != "Bearer sk-t" {
		t.Errorf("Authorization = %q, want Bearer sk-t", fake.auth)
	}
}

// The probe is the gate for every AI feature: tool calling is a prerequisite,
// so a reachable endpoint whose model only produces prose must fail — and an
// upstream failure must stay opaque, or the test endpoint reports whether a
// host is reachable.
func TestAITestProbeRequiresAToolCall(t *testing.T) {
	tests := []struct {
		name      string
		fake      fakeOpenAI
		wantOK    bool
		wantError string
	}{
		{name: "tool call passes", fake: fakeOpenAI{tool: probeToolName}, wantOK: true},
		{
			name:      "text-only reply fails",
			fake:      fakeOpenAI{content: "I would call ping."},
			wantError: toolCallRequiredMessage,
		},
		{
			name:      "another tool is not the probe",
			fake:      fakeOpenAI{tool: "not_ping"},
			wantError: toolCallRequiredMessage,
		},
		{
			name:      "an upstream rejecting tools stays opaque",
			fake:      fakeOpenAI{status: http.StatusBadRequest},
			wantError: connectionFailedMessage,
		},
		// A reply cut off at the probe budget says nothing about tool calling:
		// a reasoning model can spend the whole budget thinking before it emits
		// a call. Reporting that as "no tool calling" blames the model for a
		// budget, so it is reported as the truncation it is.
		{
			name:      "a truncated reply is truncation, not a missing tool",
			fake:      fakeOpenAI{content: "thinking about it", finish: "length"},
			wantError: truncatedReplyMessage,
		},
		{
			name:      "an upstream error stays opaque",
			fake:      fakeOpenAI{status: http.StatusInternalServerError},
			wantError: connectionFailedMessage,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := tt.fake
			upstream := httptest.NewServer(fake.handler())
			defer upstream.Close()

			t.Setenv("KB_AI_ALLOW_PRIVATE", "1")
			h, _ := newTestServer(t, Config{})
			configureAI(t, h, upstream.URL, "test-model", "sk-t")

			res := postAITest(t, h, "")
			if res.OK != tt.wantOK || res.Error != tt.wantError {
				t.Fatalf("probe result = %+v, want ok=%t error=%q", res, tt.wantOK, tt.wantError)
			}
			// One user action is one upstream request: the SDK's retries are
			// off, so a failing probe cannot be multiplied against a host.
			if fake.calls != 1 {
				t.Fatalf("upstream calls = %d, want exactly 1", fake.calls)
			}
			sent := decodeAIRequest(t, fake.reqBody)
			if len(sent.Tools) != 1 || sent.Tools[0].Function.Name != probeToolName {
				t.Fatalf("probe request tools = %+v", sent.Tools)
			}
			if sent.ResponseFormat != nil {
				t.Errorf("probe asked for %+v, want no response_format", sent.ResponseFormat)
			}
		})
	}
}

// The SDK reads OPENAI_* from the process environment. A credential that
// happens to sit in the server's environment must never ride along to a
// user-configured endpoint, and an ambient base URL must not redirect the
// call: what travels is the key stored for that origin, or nothing. The same
// holds for everything else the SDK derives from the environment — the org and
// project identifiers, and the arbitrary headers of OPENAI_CUSTOM_HEADERS,
// which is exactly where a gateway token ends up.
func TestAIRequestsIgnoreAmbientOpenAICredentials(t *testing.T) {
	for _, tt := range []struct{ name, stored, want string }{
		{name: "no stored key sends none", want: ""},
		{name: "stored key wins", stored: "sk-stored", want: "Bearer sk-stored"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeOpenAI{tool: probeToolName}
			upstream := httptest.NewServer(fake.handler())
			defer upstream.Close()

			t.Setenv("KB_AI_ALLOW_PRIVATE", "1")
			t.Setenv("OPENAI_API_KEY", "sk-ambient")
			t.Setenv("OPENAI_ADMIN_KEY", "sk-ambient-admin")
			t.Setenv("OPENAI_BASE_URL", "https://ambient.invalid")
			t.Setenv("OPENAI_ORG_ID", "org-ambient")
			t.Setenv("OPENAI_PROJECT_ID", "proj-ambient")
			t.Setenv("OPENAI_CUSTOM_HEADERS", "X-Gateway-Token: sk-gateway\nX-Ambient: leaked")
			h, _ := newTestServer(t, Config{})
			configureAI(t, h, upstream.URL, "m", tt.stored)

			if res := postAITest(t, h, ""); !res.OK {
				t.Fatalf("probe = %+v, want ok:true", res)
			}
			if fake.calls != 1 || fake.auth != tt.want {
				t.Fatalf("upstream calls=%d Authorization=%q, want 1 and %q", fake.calls, fake.auth, tt.want)
			}
			for _, header := range []string{"OpenAI-Organization", "OpenAI-Project", "X-Gateway-Token", "X-Ambient"} {
				if got := fake.header.Get(header); got != "" {
					t.Errorf("upstream received %s: %q, want it dropped", header, got)
				}
			}
		})
	}
}

// The key still travels when the environment also declares an Authorization
// header: the per-request key owns that header, and the scrub must not undo it.
func TestAmbientAuthorizationHeaderDoesNotSuppressTheStoredKey(t *testing.T) {
	fake := &fakeOpenAI{tool: probeToolName}
	upstream := httptest.NewServer(fake.handler())
	defer upstream.Close()

	t.Setenv("KB_AI_ALLOW_PRIVATE", "1")
	t.Setenv("OPENAI_CUSTOM_HEADERS", "Authorization: Bearer sk-ambient")
	h, _ := newTestServer(t, Config{})
	configureAI(t, h, upstream.URL, "m", "sk-stored")

	if res := postAITest(t, h, ""); !res.OK {
		t.Fatalf("probe = %+v, want ok:true", res)
	}
	if fake.auth != "Bearer sk-stored" {
		t.Fatalf("Authorization = %q, want Bearer sk-stored", fake.auth)
	}
}

// Hitting the stated budget is the failure this phase exists to make legible:
// it must be reported as truncation, not as the "not valid JSON" it used to
// masquerade as, and not as the opaque upstream failure a 502 collapses to.
func TestAIStoryReportsATruncatedReply(t *testing.T) {
	fake := &scriptedOpenAI{replies: []fakeReply{{content: `{"title":"Ship i`, finish: "length"}}}
	upstream := httptest.NewServer(fake.handler())
	defer upstream.Close()

	t.Setenv("KB_AI_ALLOW_PRIVATE", "1")
	w := draftStory(t, upstream.URL, "test-model", "sk-t", `{"mode":"create","prompt":"write a card"}`)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST story: got %d (body=%s), want 422", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), truncatedReplyMessage) {
		t.Fatalf("body = %q, want %q", w.Body, truncatedReplyMessage)
	}
}

// The budget is bounded on both ends: never absent (the upstream default is
// what truncates a reply), never above what a small model accepts (a request
// over the model's own cap is rejected outright). Every request the suite
// records is checked against the ceiling by decodeAIRequest; the call sites
// are checked here, before one is ever sent.
func TestCallSiteBudgetsStayUnderTheCeiling(t *testing.T) {
	for _, budget := range []int64{aiStoryMaxTokens, aiStoriesMaxTokens, aiImportMaxTokens, aiDriftMaxTokens, probeMaxTokens} {
		if budget < 1 || budget > aiMaxTokensCeiling {
			t.Errorf("call-site budget %d is outside 1..%d", budget, aiMaxTokensCeiling)
		}
	}
}

// max_tokens is deprecated and rejected by the reasoning models; the plain
// chat models and the OpenAI-compatible servers are the ones that only know
// max_tokens. The model name is all the server has to choose between them.
func TestUsesMaxCompletionTokens(t *testing.T) {
	for _, tt := range []struct {
		model string
		want  bool
	}{
		{model: "o1", want: true},
		{model: "o3-mini", want: true},
		{model: "o4-mini-2025-04-16", want: true},
		{model: "openai/o3", want: true},
		{model: "GPT-5-mini", want: true},
		{model: " o1 ", want: true},
		{model: "gpt-4o", want: false},
		{model: "gpt-4o-mini", want: false},
		{model: "gpt-3.5-turbo", want: false},
		{model: "openhermes", want: false},
		{model: "olmo-7b", want: false},
		{model: "", want: false},
	} {
		if got := usesMaxCompletionTokens(tt.model); got != tt.want {
			t.Errorf("usesMaxCompletionTokens(%q) = %t, want %t", tt.model, got, tt.want)
		}
	}
}

// On the wire: a reasoning model gets max_completion_tokens and never the
// deprecated field, which it would reject with a 400 no caller can act on.
func TestAIRequestPicksTheBudgetFieldForTheModel(t *testing.T) {
	for _, tt := range []struct {
		model            string
		wantCompletionTk bool
	}{
		{model: "o3-mini", wantCompletionTk: true},
		{model: "gpt-4o", wantCompletionTk: false},
	} {
		t.Run(tt.model, func(t *testing.T) {
			fake := &scriptedOpenAI{replies: []fakeReply{
				{toolCalls: []fakeToolCall{{name: "propose_card", args: `{"title":"Ship it"}`}}},
				{content: "Proposed one card."},
			}}
			upstream := httptest.NewServer(fake.handler())
			defer upstream.Close()

			t.Setenv("KB_AI_ALLOW_PRIVATE", "1")
			w := draftStory(t, upstream.URL, tt.model, "sk-t", `{"mode":"create","prompt":"write a card"}`)
			if w.Code != http.StatusOK {
				t.Fatalf("POST story: got %d (body=%s)", w.Code, w.Body)
			}
			sent := decodeAIRequest(t, fake.requests()[0])
			gotCompletionTk := sent.MaxCompletionTokens != nil
			if gotCompletionTk != tt.wantCompletionTk || sent.budget() != aiStoryMaxTokens {
				t.Fatalf("budget field max_completion_tokens=%t value=%d, want %t and %d",
					gotCompletionTk, sent.budget(), tt.wantCompletionTk, aiStoryMaxTokens)
			}
		})
	}
}

// aiTestResult decodes POST /api/ai/test, which reports failure in the body
// rather than the status.
type aiTestResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

func postAITest(t *testing.T, h http.Handler, body string) aiTestResult {
	t.Helper()
	w := doReq(t, h, "POST", "/api/ai/test", body, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/ai/test: got %d, want 200 (body=%s)", w.Code, w.Body)
	}
	var res aiTestResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("test JSON: %v", err)
	}
	return res
}

// The point of the optional body: a connection can be tested before it is
// saved, so nobody has to store a key to find out whether it works. The
// supplied values must reach the upstream and must not reach the store.
func TestAITestUsesSuppliedValuesWithoutSaving(t *testing.T) {
	stored := &fakeOpenAI{tool: probeToolName}
	storedUp := httptest.NewServer(stored.handler())
	defer storedUp.Close()
	candidate := &fakeOpenAI{tool: probeToolName}
	candidateUp := httptest.NewServer(candidate.handler())
	defer candidateUp.Close()

	t.Setenv("KB_AI_ALLOW_PRIVATE", "1") // test upstreams are on loopback
	h, st := newTestServer(t, Config{})
	configureAI(t, h, storedUp.URL, "stored-model", "sk-stored")

	body := fmt.Sprintf(`{"ai_base_url":%q,"ai_model":"new-model","ai_key":"sk-new"}`, candidateUp.URL)
	if res := postAITest(t, h, body); !res.OK || res.Error != "" {
		t.Fatalf("supplied-values test = %+v, want ok:true", res)
	}
	if stored.reqBody != nil {
		t.Error("the saved endpoint was called; the supplied one was ignored")
	}
	if candidate.auth != "Bearer sk-new" {
		t.Errorf("candidate Authorization = %q, want Bearer sk-new", candidate.auth)
	}
	if sent := decodeAIRequest(t, candidate.reqBody); sent.Model != "new-model" {
		t.Errorf("model = %v, want new-model", sent.Model)
	}

	// Nothing about the test was persisted — least of all the key, which the
	// user has only just typed and may well have typed wrong.
	if key, err := st.AIKey("default"); err != nil || key != "sk-stored" {
		t.Errorf("stored key = %q, %v, want sk-stored (a tested key must never be saved)", key, err)
	}
	w := doReq(t, h, "GET", "/api/settings", "", nil)
	var set settingsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &set); err != nil {
		t.Fatalf("settings JSON: %v", err)
	}
	if set.BaseURL != storedUp.URL || set.Model != "stored-model" || !set.HasKey {
		t.Errorf("settings after test = %+v, want the saved values untouched", set)
	}

	// And with no body the endpoint still means "test what is saved".
	stored.reqBody, candidate.reqBody = nil, nil
	if res := postAITest(t, h, ""); !res.OK {
		t.Fatalf("bodyless test = %+v, want ok:true", res)
	}
	if stored.reqBody == nil {
		t.Error("bodyless test did not reach the saved endpoint")
	}
	if stored.auth != "Bearer sk-stored" {
		t.Errorf("bodyless test Authorization = %q, want Bearer sk-stored", stored.auth)
	}
	if candidate.reqBody != nil {
		t.Error("bodyless test reached the candidate endpoint")
	}
}

// The settings response never returns the key, so a form that has one saved
// shows a blank key field. Blank therefore has to mean "use the saved key",
// or changing only the model would force the user to retype it.
func TestAITestBlankKeyFallsBackToStoredKey(t *testing.T) {
	candidate := &fakeOpenAI{tool: probeToolName}
	candidateUp := httptest.NewServer(candidate.handler())
	defer candidateUp.Close()

	t.Setenv("KB_AI_ALLOW_PRIVATE", "1")
	h, _ := newTestServer(t, Config{})
	configureAI(t, h, candidateUp.URL, "stored-model", "sk-stored")

	body := fmt.Sprintf(`{"ai_base_url":%q,"ai_model":"new-model","ai_key":""}`, candidateUp.URL)
	if res := postAITest(t, h, body); !res.OK || res.Error != "" {
		t.Fatalf("blank-key test = %+v, want ok:true", res)
	}
	if candidate.auth != "Bearer sk-stored" {
		t.Errorf("Authorization = %q, want the stored Bearer sk-stored", candidate.auth)
	}
	if sent := decodeAIRequest(t, candidate.reqBody); sent.Model != "new-model" {
		t.Errorf("model = %v, want the supplied new-model", sent.Model)
	}
}

// A stored key must never follow a re-pointed endpoint. Saving enforces that
// (SetAISettings drops the key when the host changes without one), and a test
// has to enforce it too: the key field is blank by default — the settings
// response never returns the key — so without this, one form edit plus "Test
// connection" would hand the decrypted key to any host named in the body, and
// nothing would be written to show it happened.
func TestAITestBlankKeyRefusesADifferentOrigin(t *testing.T) {
	stored := &fakeOpenAI{tool: probeToolName}
	storedUp := httptest.NewServer(stored.handler())
	defer storedUp.Close()
	attacker := &fakeOpenAI{tool: probeToolName}
	attackerUp := httptest.NewServer(attacker.handler())
	defer attackerUp.Close()

	t.Setenv("KB_AI_ALLOW_PRIVATE", "1")
	h, st := newTestServer(t, Config{})
	configureAI(t, h, storedUp.URL, "stored-model", "sk-stored")

	res := postAITest(t, h, fmt.Sprintf(`{"ai_base_url":%q}`, attackerUp.URL))
	if res.OK {
		t.Fatal("a blank key must not be tested against another origin")
	}
	if !strings.Contains(res.Error, "API key") {
		t.Errorf("error = %q, want one asking for the key", res.Error)
	}
	if attacker.reqBody != nil {
		t.Errorf("the other origin was called with Authorization %q", attacker.auth)
	}

	// Supplying a key makes it deliberate, and only that key travels.
	res = postAITest(t, h, fmt.Sprintf(`{"ai_base_url":%q,"ai_key":"sk-typed"}`, attackerUp.URL))
	if !res.OK {
		t.Fatalf("supplied-key test = %+v, want ok:true", res)
	}
	if attacker.auth != "Bearer sk-typed" {
		t.Errorf("Authorization = %q, want Bearer sk-typed", attacker.auth)
	}
	if key, err := st.AIKey("default"); err != nil || key != "sk-stored" {
		t.Errorf("stored key = %q, %v, want sk-stored untouched", key, err)
	}

	// With no key stored there is nothing to leak, so a bare URL still tests.
	h2, _ := newTestServer(t, Config{})
	configureAI(t, h2, storedUp.URL, "stored-model", "")
	attacker.reqBody, attacker.auth = nil, ""
	if res = postAITest(t, h2, fmt.Sprintf(`{"ai_base_url":%q}`, attackerUp.URL)); !res.OK {
		t.Fatalf("keyless test = %+v, want ok:true", res)
	}
	if attacker.auth != "" {
		t.Errorf("Authorization = %q, want none", attacker.auth)
	}
}

// A caller-supplied URL must not buy anything a saved one does not: same
// SSRF guard, same opaque failure. Otherwise the endpoint is a port scanner
// that reports its findings one request at a time.
func TestAITestSuppliedURLKeepsGuards(t *testing.T) {
	blocked := &fakeOpenAI{tool: probeToolName}
	blockedUp := httptest.NewServer(blocked.handler())
	defer blockedUp.Close()

	t.Setenv("KB_AI_ALLOW_PRIVATE", "")
	h, _ := newTestServer(t, Config{})

	res := postAITest(t, h, fmt.Sprintf(`{"ai_base_url":%q}`, blockedUp.URL))
	if res.OK {
		t.Fatal("a supplied private upstream must be refused")
	}
	if res.Error != "connection failed" {
		t.Errorf("error = %q, want the opaque %q", res.Error, "connection failed")
	}
	if blocked.reqBody != nil {
		t.Error("request reached the private upstream")
	}

	// A malformed URL is the caller's own configuration, so it stays legible
	// — and it is rejected before anything is dialed.
	res = postAITest(t, h, `{"ai_base_url":"ftp://api.example.com"}`)
	if res.OK || !strings.Contains(res.Error, "scheme") {
		t.Errorf("bad-scheme test = %+v, want ok:false naming the scheme", res)
	}
	if res = postAITest(t, h, `{"ai_base_url":"file:///etc/passwd"}`); res.OK {
		t.Errorf("file:// test = %+v, want ok:false", res)
	}

	// An unparseable body is a client bug, not a failed connection.
	if w := doReq(t, h, "POST", "/api/ai/test", `{"ai_base_url":`, nil); w.Code != http.StatusBadRequest {
		t.Errorf("malformed body: got %d, want 400", w.Code)
	}
}

// End to end: a story draft survives a backend that inlines reasoning. The
// card rides the tool call, so the leaked reasoning is prose nothing parses —
// including the sketch of a card that used to be mistaken for the reply.
func TestAIStoryToleratesInlineReasoning(t *testing.T) {
	fake := &scriptedOpenAI{replies: []fakeReply{
		{
			content:   "<think>They want rate limiting. Shape: {\"title\": ...}.</think>",
			toolCalls: []fakeToolCall{{name: "propose_card", args: `{"title":"Add rate limiting","prio":2}`}},
		},
		{content: "<think>Done.</think>\nProposed one card."},
	}}
	upstream := httptest.NewServer(fake.handler())
	defer upstream.Close()

	t.Setenv("KB_AI_ALLOW_PRIVATE", "1") // test upstream is on loopback
	w := draftStory(t, upstream.URL, "test-model", "sk-test-123", `{"mode":"create","prompt":"rate limit logins"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("POST story: got %d (body=%s)", w.Code, w.Body)
	}
	var draft storyDraft
	if err := json.Unmarshal(w.Body.Bytes(), &draft); err != nil {
		t.Fatalf("draft JSON: %v (body=%s)", err, w.Body)
	}
	if draft.Title != "Add rate limiting" || draft.Prio != 2 {
		t.Errorf("draft = %+v, want title %q prio 2", draft, "Add rate limiting")
	}
}
