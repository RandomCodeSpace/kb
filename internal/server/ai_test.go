package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
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

// wireChatRequest is what the SDK actually puts on the wire, decoded the way
// an OpenAI-compatible server would read it.
type wireChatRequest struct {
	Model               string        `json:"model"`
	Messages            []chatMessage `json:"messages"`
	MaxTokens           *int64        `json:"max_tokens"`
	MaxCompletionTokens *int64        `json:"max_completion_tokens"`
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

// Import source indices are accepted only as positive integral positions; bad
// model values remain unlinked for the server-side provenance step.
func TestCoerceDraftsTracksOnlyValidImportSources(t *testing.T) {
	drafts, err := coerceDrafts(`{"stories":[
		{"title":"one","source":1},
		{"title":"fraction","source":1.5},
		{"title":"zero","source":0},
		{"title":"missing"}
	]}`, 4)
	if err != nil || len(drafts) != 4 {
		t.Fatalf("coerceDrafts = %+v, %v", drafts, err)
	}
	if drafts[0].Source != 1 || drafts[1].Source != 0 || drafts[2].Source != 0 || drafts[3].Source != 0 {
		t.Fatalf("sources = [%d %d %d %d], want [1 0 0 0]", drafts[0].Source, drafts[1].Source, drafts[2].Source, drafts[3].Source)
	}
}

// Import packs cap each issue body, each comment, and the total prompt while
// retaining a source index for every issue that fits into the bounded input.
func TestPackImportIssuesBoundsForgeText(t *testing.T) {
	issues := make([]forgeIssue, maxImportIssues)
	for i := range issues {
		issues[i] = forgeIssue{
			Ref:      fmt.Sprintf("gitlab#%d", i+1),
			Title:    "Import issue",
			Body:     strings.Repeat("é", maxImportIssueBodyBytes),
			Labels:   []string{"team::auth"},
			Comments: []string{strings.Repeat("界", maxImportCommentBytes)},
		}
	}
	packed, count := packImportIssues(issues)
	if len(packed) > maxImportPackBytes || count == 0 || count > len(issues) {
		t.Fatalf("pack len=%d count=%d, want <=%d and 1..%d", len(packed), count, maxImportPackBytes, len(issues))
	}
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

func TestAIStoryHappyPath(t *testing.T) {
	fake := &fakeOpenAI{content: `{"title":" Ship it ","emoji":"🛠️","desc":"Do the thing","prio":9,"due":"not-a-date","effort":"m","tags":["backend",42,""],"checks":[{"text":"step one","done":true},{"text":""},"junk"]}`}
	upstream := httptest.NewServer(fake.handler())
	defer upstream.Close()

	t.Setenv("KB_AI_ALLOW_PRIVATE", "1") // test upstream is on loopback
	h, _ := newTestServer(t, Config{})
	configureAI(t, h, upstream.URL, "test-model", "sk-test-123")

	w := doReq(t, h, "POST", "/api/ai/story", `{"mode":"create","prompt":"write a card"}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("POST story: got %d (body=%s)", w.Code, w.Body)
	}
	// The decrypted key reached the upstream, on the joined /v1 path.
	if fake.auth != "Bearer sk-test-123" {
		t.Errorf("upstream Authorization = %q, want Bearer sk-test-123", fake.auth)
	}
	if fake.path != "/v1/chat/completions" {
		t.Errorf("upstream path = %q, want /v1/chat/completions", fake.path)
	}
	// The request asked for strict JSON from the configured model, with an
	// output budget large enough for a whole card.
	sent := decodeAIRequest(t, fake.reqBody)
	if sent.Model != "test-model" {
		t.Errorf("model = %v, want test-model", sent.Model)
	}
	if sent.ResponseFormat == nil || sent.ResponseFormat.Type != "json_object" {
		t.Errorf("response_format = %+v, want json_object", sent.ResponseFormat)
	}
	if *sent.MaxTokens != aiStoryMaxTokens {
		t.Errorf("max_tokens = %d, want %d", *sent.MaxTokens, aiStoryMaxTokens)
	}
	if len(sent.Tools) != 0 {
		t.Errorf("tools = %+v, want none on a JSON-mode draft call", sent.Tools)
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
			content, err := json.Marshal(map[string]any{
				"stories": []any{map[string]any{
					"title": "Ship the draft",
					"emoji": tt.emoji,
				}},
			})
			if err != nil {
				t.Fatalf("marshal assistant reply: %v", err)
			}
			fake := &fakeOpenAI{content: string(content)}
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

func TestAIStoryUpdateModePassesTask(t *testing.T) {
	fake := &fakeOpenAI{content: `{"title":"Updated"}`}
	upstream := httptest.NewServer(fake.handler())
	defer upstream.Close()

	t.Setenv("KB_AI_ALLOW_PRIVATE", "1") // test upstream is on loopback
	h, _ := newTestServer(t, Config{})
	// Base URL already ending in /v1 must not double the segment.
	configureAI(t, h, upstream.URL+"/v1", "m", "")

	body := `{"mode":"update","prompt":"tighten the title","task":{"title":"Old title","prio":2}}`
	w := doReq(t, h, "POST", "/api/ai/story", body, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("POST story update: got %d (body=%s)", w.Code, w.Body)
	}
	if fake.path != "/v1/chat/completions" {
		t.Errorf("upstream path = %q, want /v1/chat/completions", fake.path)
	}
	// No key configured -> no Authorization header.
	if fake.auth != "" {
		t.Errorf("Authorization = %q, want empty", fake.auth)
	}
	if !strings.Contains(string(fake.reqBody), "Old title") {
		t.Errorf("current card not passed upstream: %s", fake.reqBody)
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
	fake := &fakeOpenAI{status: http.StatusInternalServerError}
	upstream := httptest.NewServer(fake.handler())
	defer upstream.Close()

	t.Setenv("KB_AI_ALLOW_PRIVATE", "1") // test upstream is on loopback
	h, _ := newTestServer(t, Config{})
	configureAI(t, h, upstream.URL, "m", "sk-super-secret")

	w := doReq(t, h, "POST", "/api/ai/story", `{"mode":"create","prompt":"p"}`, nil)
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
	fake := &fakeOpenAI{tool: aiProbeToolName}
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

// An upstream reply is untrusted input. Every value that lands on a single
// markdown line must come back stripped, or the reply forges board lines.
func TestAIStoryRejectsWireBreakingReply(t *testing.T) {
	hostile := map[string]any{
		"title": "Ship it\n- [x] forged !1 @2026-01-01",
		"desc":  "line one\nline two",
		"tags":  []any{"back\nend", "two words", "#hash", "ok"},
		"checks": []any{
			map[string]any{"text": "step\n- [ ] forged check", "done": false},
			map[string]any{"text": "clean step", "done": true},
		},
	}
	body, err := json.Marshal(hostile)
	if err != nil {
		t.Fatalf("marshal hostile draft: %v", err)
	}
	fake := &fakeOpenAI{content: string(body)}
	upstream := httptest.NewServer(fake.handler())
	defer upstream.Close()

	t.Setenv("KB_AI_ALLOW_PRIVATE", "1")
	h, _ := newTestServer(t, Config{})
	configureAI(t, h, upstream.URL, "m", "")

	w := doReq(t, h, "POST", "/api/ai/story", `{"mode":"create","prompt":"p"}`, nil)
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

func TestAIStories(t *testing.T) {
	const adr = "# ADR 7: adopt SQLite\n\nWe will store boards in SQLite.\n"

	t.Run("splits an ADR into validated drafts", func(t *testing.T) {
		fake := &fakeOpenAI{content: `{"stories":[
			{"title":"Add the store package","prio":1,"effort":"s","tags":["backend"],"checks":[{"text":"open the db","done":false}]},
			{"title":"Write migrations","prio":9,"due":"2026-13-45"},
			{"title":"Bad\nnewline","desc":"still fine"},
			{"title":"   "},
			"not an object"
		]}`}
		upstream := httptest.NewServer(fake.handler())
		defer upstream.Close()

		t.Setenv("KB_AI_ALLOW_PRIVATE", "1")
		h, _ := newTestServer(t, Config{})
		configureAI(t, h, upstream.URL, "m", "sk-t")

		w := doReq(t, h, "POST", "/api/ai/stories", `{"adr":`+strconv.Quote(adr)+`}`, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("POST stories: got %d (body=%s)", w.Code, w.Body)
		}
		var res struct {
			Stories []storyDraft `json:"stories"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
			t.Fatalf("stories JSON: %v (body=%s)", err, w.Body)
		}
		var raw map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
			t.Fatalf("raw stories JSON: %v", err)
		}
		if _, ok := raw["link"]; ok {
			t.Fatalf("ADR response unexpectedly has link: %v", raw)
		}
		if _, ok := raw["url"]; ok {
			t.Fatalf("ADR response unexpectedly has url: %v", raw)
		}
		// The blank-title story and the non-object are dropped; everything
		// returned passes the shared validation.
		if len(res.Stories) != 3 {
			t.Fatalf("got %d stories, want 3: %+v", len(res.Stories), res.Stories)
		}
		for _, d := range res.Stories {
			if err := validateDraft(d); err != nil {
				t.Errorf("returned story fails wire validation: %v (%+v)", err, d)
			}
		}
		want := storyDraft{
			Title: "Add the store package", Prio: 1, Effort: "S",
			Tags: []string{"backend"}, Checks: []draftCheck{{Text: "open the db"}},
		}
		if !reflect.DeepEqual(res.Stories[0], want) {
			t.Errorf("story[0] = %+v, want %+v", res.Stories[0], want)
		}
		if res.Stories[1].Prio != 4 || res.Stories[1].Due != "" {
			t.Errorf("story[1] = %+v, want prio clamped to 4 and the impossible date dropped", res.Stories[1])
		}
		if res.Stories[2].Title != "Badnewline" {
			t.Errorf("story[2] title = %q, want the newline stripped", res.Stories[2].Title)
		}
		// The ADR reached the model, and the request rode the shared proxy
		// with the split-sized output budget.
		if !strings.Contains(string(fake.reqBody), "adopt SQLite") {
			t.Errorf("ADR not passed upstream: %s", fake.reqBody)
		}
		if sent := decodeAIRequest(t, fake.reqBody); *sent.MaxTokens != aiStoriesMaxTokens {
			t.Errorf("max_tokens = %d, want %d", *sent.MaxTokens, aiStoriesMaxTokens)
		}
		if fake.auth != "Bearer sk-t" {
			t.Errorf("Authorization = %q, want the stored key", fake.auth)
		}
		if fake.path != "/v1/chat/completions" {
			t.Errorf("upstream path = %q, want /v1/chat/completions", fake.path)
		}
	})

	t.Run("story count is clamped and capped", func(t *testing.T) {
		fake := &fakeOpenAI{content: `{"stories":[{"title":"a"},{"title":"b"},{"title":"c"}]}`}
		upstream := httptest.NewServer(fake.handler())
		defer upstream.Close()

		t.Setenv("KB_AI_ALLOW_PRIVATE", "1")
		h, _ := newTestServer(t, Config{})
		configureAI(t, h, upstream.URL, "m", "")

		asked := func(t *testing.T, max string) string {
			t.Helper()
			body := `{"adr":"x"` + max + `}`
			if w := doReq(t, h, "POST", "/api/ai/stories", body, nil); w.Code != http.StatusOK {
				t.Fatalf("POST stories: got %d (body=%s)", w.Code, w.Body)
			}
			sent := decodeAIRequest(t, fake.reqBody)
			return sent.Messages[len(sent.Messages)-1].Content
		}
		if got := asked(t, ""); !strings.Contains(got, "at most 8 stories") {
			t.Errorf("absent max: prompt = %q, want the default of 8", got)
		}
		if got := asked(t, `,"max":3`); !strings.Contains(got, "at most 3 stories") {
			t.Errorf("max 3: prompt = %q", got)
		}
		if got := asked(t, `,"max":999`); !strings.Contains(got, "at most 20 stories") {
			t.Errorf("max 999: prompt = %q, want the cap of 20", got)
		}
		// Only an absent max takes the default; a supplied one is clamped
		// into 1..20, so a negative value asks for one story, not eight.
		if got := asked(t, `,"max":-5`); !strings.Contains(got, "at most 1 stories") {
			t.Errorf("negative max: prompt = %q, want the floor of 1", got)
		}

		// The cap also bounds what a model returning more than asked can add.
		w := doReq(t, h, "POST", "/api/ai/stories", `{"adr":"x","max":2}`, nil)
		var res struct {
			Stories []storyDraft `json:"stories"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
			t.Fatalf("stories JSON: %v", err)
		}
		if len(res.Stories) != 2 {
			t.Errorf("got %d stories for max 2, want 2", len(res.Stories))
		}
	})

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
				_, _ = fmt.Fprintf(w, `[{"body":%q,"system":false}]`, strings.Repeat("界", maxForgeCommentLen))
			default:
				http.NotFound(w, r)
			}
		}))
		defer upstream.Close()
		fake := &fakeOpenAI{content: `{"stories":[{"title":"first","tags":["model","link::evil#1"]},{"title":"second","tags":["link::evil#2"]}]}`}
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
		sent := decodeAIRequest(t, fake.reqBody)
		prompt := sent.Messages[len(sent.Messages)-1].Content
		adr := strings.TrimPrefix(prompt, "Split this ADR into at most 8 stories:\n\n")
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
		fake := &fakeOpenAI{content: `{"stories":[{"title":"split"}]}`}
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
		sent := decodeAIRequest(t, fake.reqBody)
		adr := strings.TrimPrefix(sent.Messages[len(sent.Messages)-1].Content, "Split this ADR into at most 8 stories:\n\n")
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
	fake := &fakeOpenAI{tool: aiProbeToolName}
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
	if *sent.MaxTokens != aiProbeMaxTokens {
		t.Errorf("max_tokens = %d, want %d", *sent.MaxTokens, aiProbeMaxTokens)
	}
	if len(sent.Tools) != 1 || sent.Tools[0].Function.Name != aiProbeToolName {
		t.Fatalf("tools = %+v, want one %q function tool", sent.Tools, aiProbeToolName)
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
		{name: "tool call passes", fake: fakeOpenAI{tool: aiProbeToolName}, wantOK: true},
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
			if len(sent.Tools) != 1 || sent.Tools[0].Function.Name != aiProbeToolName {
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
			fake := &fakeOpenAI{tool: aiProbeToolName}
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

// A header the request needs is not scrubbed just because the environment
// names it: OPENAI_CUSTOM_HEADERS is attacker-adjacent configuration, but
// deleting Authorization or Content-Type on its say-so breaks the call
// instead of containing anything.
func TestAmbientHeaderScrubKeepsRequestOwnedHeaders(t *testing.T) {
	t.Setenv("OPENAI_CUSTOM_HEADERS", "Authorization: Bearer sk-ambient\ncontent-type: text/plain\nAccept: text/plain\nX-Leak: v\nnot-a-header\n: blank")
	got := ambientHeaderNames()
	want := []string{"OpenAI-Organization", "OpenAI-Project", "X-Leak"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ambientHeaderNames() = %v, want %v", got, want)
	}
}

// The key still travels when the environment also declares an Authorization
// header: the per-request key owns that header, and the scrub must not undo it.
func TestAmbientAuthorizationHeaderDoesNotSuppressTheStoredKey(t *testing.T) {
	fake := &fakeOpenAI{tool: aiProbeToolName}
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

// endlessBody is an upstream that never stops answering. Without a cap the SDK
// buffers all of it: io.ReadAll grows by doubling, so the resident cost is a
// multiple of what the host actually sent.
type endlessBody struct{ read int64 }

func (b *endlessBody) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'a'
	}
	b.read += int64(len(p))
	return len(p), nil
}

func (b *endlessBody) Close() error { return nil }

// The endpoint is user-configured, so the reply is bounded before it is
// buffered — on the success path and on the error path alike, since both read
// the whole body.
func TestChatCapsUpstreamResponseBody(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusBadRequest} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			body := &endlessBody{}
			s := &server{aiClient: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: status,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       body,
				}, nil
			})}}
			call := chatCall{msgs: []chatMessage{{Role: "user", Content: "hi"}}, maxTokens: 10}
			_, err := s.chat("u", aiConfig{baseURL: "https://ai.invalid", model: "m"}, call)
			if err == nil {
				t.Fatal("chat accepted an endless response body")
			}
			// One buffer's worth of slack over the cap: the reader stops at
			// cap+1 bytes, the last Read may have been issued for more.
			if body.read > 2*aiMaxResponseBytes {
				t.Fatalf("read %d bytes from the upstream, want it stopped near %d", body.read, aiMaxResponseBytes)
			}
		})
	}
}

// A reply of exactly the cap is not a failure: the limit exists to stop an
// endless body, not to truncate a legitimate one.
func TestChatAcceptsAReplyAtTheSizeCap(t *testing.T) {
	content := strings.Repeat("a", aiMaxResponseBytes-256)
	reply, _ := json.Marshal(map[string]any{"choices": []any{
		map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": content}, "finish_reason": "stop"},
	}})
	if len(reply) > aiMaxResponseBytes {
		t.Fatalf("test reply is %d bytes, want at most %d", len(reply), aiMaxResponseBytes)
	}
	s := &server{aiClient: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(string(reply))),
		}, nil
	})}}
	msg, err := s.chat("u", aiConfig{baseURL: "https://ai.invalid", model: "m"}, chatCall{
		msgs: []chatMessage{{Role: "user", Content: "hi"}}, maxTokens: 10,
	})
	if err != nil || msg.Content != content {
		t.Fatalf("chat = %d bytes, %v; want the whole reply", len(msg.Content), err)
	}
}

// Hitting the stated budget is the failure this phase exists to make legible:
// it must be reported as truncation, not as the "not valid JSON" it used to
// masquerade as, and not as the opaque upstream failure a 502 collapses to.
func TestAIStoryReportsATruncatedReply(t *testing.T) {
	fake := &fakeOpenAI{content: `{"title":"Ship i`, finish: "length"}
	upstream := httptest.NewServer(fake.handler())
	defer upstream.Close()

	t.Setenv("KB_AI_ALLOW_PRIVATE", "1")
	h, _ := newTestServer(t, Config{})
	configureAI(t, h, upstream.URL, "test-model", "sk-t")

	w := doReq(t, h, "POST", "/api/ai/story", `{"mode":"create","prompt":"write a card"}`, nil)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST story: got %d (body=%s), want 422", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), truncatedReplyMessage) {
		t.Fatalf("body = %q, want %q", w.Body, truncatedReplyMessage)
	}
}

// The budget is bounded on both ends: never absent (the upstream default is
// what truncates a reply), never above what a small model accepts (a request
// over the model's own cap is rejected outright).
func TestAIBudgetIsFlooredAndCapped(t *testing.T) {
	for _, tt := range []struct{ in, want int64 }{
		{in: 0, want: aiDefaultMaxTokens},
		{in: -1, want: aiDefaultMaxTokens},
		{in: 1, want: 1},
		{in: aiMaxTokensCeiling, want: aiMaxTokensCeiling},
		{in: aiMaxTokensCeiling + 1, want: aiMaxTokensCeiling},
		{in: 1 << 20, want: aiMaxTokensCeiling},
	} {
		if got := aiBudget(tt.in); got != tt.want {
			t.Errorf("aiBudget(%d) = %d, want %d", tt.in, got, tt.want)
		}
	}
	for _, budget := range []int64{aiDefaultMaxTokens, aiStoryMaxTokens, aiStoriesMaxTokens, aiImportMaxTokens, aiDriftMaxTokens, aiProbeMaxTokens} {
		if budget > aiMaxTokensCeiling {
			t.Errorf("call-site budget %d exceeds the ceiling %d", budget, aiMaxTokensCeiling)
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
			fake := &fakeOpenAI{content: `{"title":"Ship it"}`}
			upstream := httptest.NewServer(fake.handler())
			defer upstream.Close()

			t.Setenv("KB_AI_ALLOW_PRIVATE", "1")
			h, _ := newTestServer(t, Config{})
			configureAI(t, h, upstream.URL, tt.model, "sk-t")

			w := doReq(t, h, "POST", "/api/ai/story", `{"mode":"create","prompt":"write a card"}`, nil)
			if w.Code != http.StatusOK {
				t.Fatalf("POST story: got %d (body=%s)", w.Code, w.Body)
			}
			sent := decodeAIRequest(t, fake.reqBody)
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
	stored := &fakeOpenAI{tool: aiProbeToolName}
	storedUp := httptest.NewServer(stored.handler())
	defer storedUp.Close()
	candidate := &fakeOpenAI{tool: aiProbeToolName}
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
	candidate := &fakeOpenAI{tool: aiProbeToolName}
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
	stored := &fakeOpenAI{tool: aiProbeToolName}
	storedUp := httptest.NewServer(stored.handler())
	defer storedUp.Close()
	attacker := &fakeOpenAI{tool: aiProbeToolName}
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
	blocked := &fakeOpenAI{tool: aiProbeToolName}
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

// Some backends leave the model's chain-of-thought in the message content as
// an inline <think> block instead of a separate field. Reasoning about a JSON
// answer routinely contains braces, so a first-{-to-last-} extraction fails
// intermittently — whenever the sampled reasoning happens to include one.
func TestDecodeJSONObjectToleratesInlineReasoning(t *testing.T) {
	tests := []struct {
		name, content, wantTitle, wantErr string
	}{
		{
			name:      "think block with braces before the reply",
			content:   "<think>The user wants a card. Maybe {\"title\": something} — no, refine.</think>\n{\"title\":\"real\"}",
			wantTitle: "real",
		},
		{
			name:      "prose with braces around the reply",
			content:   "Here is the card (schema: {title, desc}):\n{\"title\":\"real\"}\nLet me know!",
			wantTitle: "real",
		},
		{
			name:      "fenced reply after a think block",
			content:   "<think>brace test {</think>```json\n{\"title\":\"real\"}\n```",
			wantTitle: "real",
		},
		{
			name:    "a draft inside the think block is not the reply",
			content: "<think>{\"title\":\"draft\"}</think>",
			wantErr: "assistant reply is not JSON",
		},
		{
			name:    "think block with no reply at all",
			content: "<think>still thinking</think>",
			wantErr: "assistant reply is not JSON",
		},
		{
			name:    "braces that never parse",
			content: "reasoning { with braces } and no object",
			wantErr: "assistant reply is not valid JSON",
		},
		{
			name:      "reasoning with a consumed opening think tag",
			content:   "The user wants a split. Sketch: {\"title\":\"sketch\"}. Refine it.</think>\n{\"title\":\"real\"}",
			wantTitle: "real",
		},
		{
			name:      "untagged reasoning sketch before a longer reply",
			content:   "The shape is {\"title\":\"sketch\"}. Final answer:\n{\"title\":\"real\",\"desc\":\"the actual card\"}",
			wantTitle: "real",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := decodeJSONObject(tt.content)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("decodeJSONObject: %v", err)
			}
			if got, _ := m["title"].(string); got != tt.wantTitle {
				t.Errorf("title = %q, want %q", got, tt.wantTitle)
			}
		})
	}
}

// End to end: a story draft survives a backend that inlines reasoning.
func TestAIStoryToleratesInlineReasoning(t *testing.T) {
	fake := &fakeOpenAI{content: "<think>They want rate limiting. Shape: {\"title\": ...}.</think>\n{\"title\":\"Add rate limiting\",\"prio\":2}"}
	upstream := httptest.NewServer(fake.handler())
	defer upstream.Close()

	t.Setenv("KB_AI_ALLOW_PRIVATE", "1") // test upstream is on loopback
	h, _ := newTestServer(t, Config{})
	configureAI(t, h, upstream.URL, "test-model", "sk-test-123")

	w := doReq(t, h, "POST", "/api/ai/story", `{"mode":"create","prompt":"rate limit logins"}`, nil)
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

// Regression: leaked reasoning that sketches the schema with a one-element
// example ({"stories":[{...}]}) must not be mistaken for the reply — that
// returned exactly one story from every ADR split, regardless of max.
func TestAIStoriesPreferTheReplyOverAReasoningSketch(t *testing.T) {
	fake := &fakeOpenAI{content: "I need up to 6 stories. The shape is " +
		`{"stories":[{"title":"example"}]}` + ". Now the real split.</think>\n" +
		`{"stories":[{"title":"one"},{"title":"two"},{"title":"three"}]}`}
	upstream := httptest.NewServer(fake.handler())
	defer upstream.Close()

	t.Setenv("KB_AI_ALLOW_PRIVATE", "1") // test upstream is on loopback
	h, _ := newTestServer(t, Config{})
	configureAI(t, h, upstream.URL, "test-model", "sk-test-123")

	w := doReq(t, h, "POST", "/api/ai/stories", `{"adr":"# ADR\nSplit the monolith."}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("POST stories: got %d (body=%s)", w.Code, w.Body)
	}
	var res struct {
		Stories []storyDraft `json:"stories"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("stories JSON: %v (body=%s)", err, w.Body)
	}
	if len(res.Stories) != 3 {
		t.Fatalf("got %d stories, want 3 (body=%s)", len(res.Stories), w.Body)
	}
	if res.Stories[0].Title != "one" || res.Stories[2].Title != "three" {
		t.Errorf("stories = %+v, want titles one..three", res.Stories)
	}
}
