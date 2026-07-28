package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestAIEndpoint(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"https://api.example.com", "https://api.example.com/v1/chat/completions", false},
		{"https://api.example.com/", "https://api.example.com/v1/chat/completions", false},
		{"https://api.example.com/v1", "https://api.example.com/v1/chat/completions", false},
		{"https://api.example.com/v1/", "https://api.example.com/v1/chat/completions", false},
		{"http://localhost:1234/proxy", "http://localhost:1234/proxy/v1/chat/completions", false},
		{" https://api.example.com/v1 ", "https://api.example.com/v1/chat/completions", false},
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
// assistant message.
type fakeOpenAI struct {
	auth    string
	path    string
	reqBody []byte
	content string
	status  int // 0 means 200
}

func (f *fakeOpenAI) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.auth = r.Header.Get("Authorization")
		f.path = r.URL.Path
		f.reqBody, _ = io.ReadAll(r.Body)
		if f.status != 0 {
			http.Error(w, "upstream boom", f.status)
			return
		}
		resp := map[string]any{
			"choices": []any{
				map[string]any{"message": map[string]any{"content": f.content}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
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
	fake := &fakeOpenAI{content: `{"title":" Ship it ","desc":"Do the thing","prio":9,"due":"not-a-date","effort":"m","tags":["backend",42,""],"checks":[{"text":"step one","done":true},{"text":""},"junk"]}`}
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
	// The request asked for strict JSON from the configured model.
	var sent map[string]any
	if err := json.Unmarshal(fake.reqBody, &sent); err != nil {
		t.Fatalf("upstream request JSON: %v", err)
	}
	if sent["model"] != "test-model" {
		t.Errorf("model = %v, want test-model", sent["model"])
	}
	rf, _ := sent["response_format"].(map[string]any)
	if rf["type"] != "json_object" {
		t.Errorf("response_format = %v, want json_object", sent["response_format"])
	}
	// The draft is coerced and clamped.
	var draft storyDraft
	if err := json.Unmarshal(w.Body.Bytes(), &draft); err != nil {
		t.Fatalf("draft JSON: %v (body=%s)", err, w.Body)
	}
	want := storyDraft{
		Title:  "Ship it",
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
	fake := &fakeOpenAI{content: "pong"}
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
		// The ADR reached the model, and the request rode the shared proxy.
		if !strings.Contains(string(fake.reqBody), "adopt SQLite") {
			t.Errorf("ADR not passed upstream: %s", fake.reqBody)
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
			var sent struct {
				Messages []chatMessage `json:"messages"`
			}
			if err := json.Unmarshal(fake.reqBody, &sent); err != nil {
				t.Fatalf("upstream request JSON: %v", err)
			}
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
		if w := doReq(t, h, "POST", "/api/ai/stories", `{"adr":"   "}`, nil); w.Code != http.StatusBadRequest {
			t.Errorf("blank ADR: got %d, want 400", w.Code)
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
	fake := &fakeOpenAI{content: "pong"}
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
	// It was a 1-token completion.
	var sent map[string]any
	if err := json.Unmarshal(fake.reqBody, &sent); err != nil {
		t.Fatalf("upstream request JSON: %v", err)
	}
	if mt, _ := sent["max_tokens"].(float64); mt != 1 {
		t.Errorf("max_tokens = %v, want 1", sent["max_tokens"])
	}
	if fake.auth != "Bearer sk-t" {
		t.Errorf("Authorization = %q, want Bearer sk-t", fake.auth)
	}
}
