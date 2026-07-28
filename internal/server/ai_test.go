package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
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
	if !strings.Contains(w.Body.String(), "upstream returned status 500") {
		t.Errorf("error body = %q, want short upstream status message", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "sk-super-secret") {
		t.Errorf("error response leaks the key: %q", w.Body.String())
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
