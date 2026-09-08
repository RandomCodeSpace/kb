package webui

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	kbai "github.com/RandomCodeSpace/kb/internal/ai"
	"github.com/RandomCodeSpace/kb/internal/forge"
	"github.com/RandomCodeSpace/kb/internal/store"
)

// stubAIProbe replaces the AI connection test for one test, so no endpoint is
// ever dialled.
func stubAIProbe(t *testing.T, fn func(context.Context, *store.Store, string, kbai.Config) error) {
	t.Helper()
	previous := aiProbe
	aiProbe = fn
	t.Cleanup(func() { aiProbe = previous })
}

func stubForgeProbe(t *testing.T, fn func(context.Context, *store.Store, string, forge.ForgeProbeConfig) error) {
	t.Helper()
	previous := forgeProbe
	forgeProbe = fn
	t.Cleanup(func() { forgeProbe = previous })
}

func TestGetAISettingsEmpty(t *testing.T) {
	h, _ := newTestHandler(t)
	rec := call(t, h, "GET", "/api/settings/ai", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != `{"ai":{"baseURL":"","model":"","hasKey":false}}` {
		t.Fatalf("body = %s", got)
	}
}

func TestPutAISettingsFields(t *testing.T) {
	h, st := newTestHandler(t)

	body := wantStatus(t, call(t, h, "PUT", "/api/settings/ai", map[string]any{
		"baseURL": " https://api.example.com/v1 ", "model": " gpt-test ", "apiKey": "sk-secret-1234",
	}), http.StatusOK)
	ai, _ := body["ai"].(map[string]any)
	if ai["baseURL"] != "https://api.example.com/v1" || ai["model"] != "gpt-test" || ai["hasKey"] != true {
		t.Fatalf("ai = %v", ai)
	}
	if _, ok := body["keyCleared"]; ok {
		t.Fatalf("keyCleared reported on a plain save: %v", body)
	}
	key, err := st.AIKey("default")
	if err != nil || key != "sk-secret-1234" {
		t.Fatalf("stored key = %q, %v", key, err)
	}

	// An omitted field is left alone.
	body = wantStatus(t, call(t, h, "PUT", "/api/settings/ai", map[string]any{"model": "gpt-other"}), http.StatusOK)
	ai, _ = body["ai"].(map[string]any)
	if ai["baseURL"] != "https://api.example.com/v1" || ai["model"] != "gpt-other" || ai["hasKey"] != true {
		t.Fatalf("ai = %v", ai)
	}

	// An explicit empty key clears it; the same-origin base URL keeps the rest.
	body = wantStatus(t, call(t, h, "PUT", "/api/settings/ai", map[string]any{"apiKey": "   "}), http.StatusOK)
	ai, _ = body["ai"].(map[string]any)
	if ai["hasKey"] != false {
		t.Fatalf("key survived an explicit clear: %v", ai)
	}
	if key, err := st.AIKey("default"); err != nil || key != "" {
		t.Fatalf("stored key = %q, %v", key, err)
	}

	// An empty base URL clears the endpoint.
	body = wantStatus(t, call(t, h, "PUT", "/api/settings/ai", map[string]any{"baseURL": ""}), http.StatusOK)
	ai, _ = body["ai"].(map[string]any)
	if ai["baseURL"] != "" {
		t.Fatalf("base URL survived a clear: %v", ai)
	}
}

func TestPutAISettingsNeverLeaksKey(t *testing.T) {
	h, _ := newTestHandler(t)
	const secret = "sk-do-not-print"
	rec := call(t, h, "PUT", "/api/settings/ai", map[string]any{"baseURL": "https://api.example.com/v1", "apiKey": secret})
	if strings.Contains(rec.Body.String(), secret) {
		t.Fatalf("write echoed the key: %s", rec.Body.String())
	}
	rec = call(t, h, "GET", "/api/settings/ai", nil)
	if strings.Contains(rec.Body.String(), secret) {
		t.Fatalf("read echoed the key: %s", rec.Body.String())
	}
}

func TestPutAISettingsKeyClearedOnOriginChange(t *testing.T) {
	h, _ := newTestHandler(t)
	wantStatus(t, call(t, h, "PUT", "/api/settings/ai", map[string]any{
		"baseURL": "https://api.example.com/v1", "apiKey": "sk-secret",
	}), http.StatusOK)

	// A path change on the same origin keeps the key.
	body := wantStatus(t, call(t, h, "PUT", "/api/settings/ai", map[string]any{"baseURL": "https://api.example.com/other/v1"}), http.StatusOK)
	if _, ok := body["keyCleared"]; ok {
		t.Fatalf("same-origin move cleared the key: %v", body)
	}

	body = wantStatus(t, call(t, h, "PUT", "/api/settings/ai", map[string]any{"baseURL": "https://evil.example.net/v1"}), http.StatusOK)
	if body["keyCleared"] != true {
		t.Fatalf("keyCleared = %v", body)
	}
	ai, _ := body["ai"].(map[string]any)
	if ai["hasKey"] != false {
		t.Fatalf("key followed the endpoint: %v", ai)
	}
}

func TestPutAISettingsRejects(t *testing.T) {
	h, st := newTestHandler(t)
	wantError(t, call(t, h, "PUT", "/api/settings/ai", map[string]any{"baseURL": "not-a-url"}), http.StatusBadRequest, "invalid AI base URL")
	wantError(t, call(t, h, "PUT", "/api/settings/ai", map[string]any{"baseURL": "https://api.example.com/v1?k=1"}), http.StatusBadRequest, "query or fragment")
	wantError(t, call(t, h, "PUT", "/api/settings/ai", "{"), http.StatusBadRequest, "malformed JSON")
	wantError(t, call(t, h, "PUT", "/api/settings/ai", nil), http.StatusBadRequest, "JSON object")

	st.Close()
	wantStatus(t, call(t, h, "PUT", "/api/settings/ai", map[string]any{"model": "m"}), http.StatusBadRequest)
	wantStatus(t, call(t, h, "GET", "/api/settings/ai", nil), http.StatusInternalServerError)
}

func TestAISettingsMethodNotAllowed(t *testing.T) {
	h, _ := newTestHandler(t)
	rec := call(t, h, "DELETE", "/api/settings/ai", nil)
	wantError(t, rec, http.StatusMethodNotAllowed, "not allowed")
	if allow := rec.Header().Get("Allow"); !strings.Contains(allow, "PUT") {
		t.Fatalf("Allow = %q", allow)
	}
	wantError(t, call(t, h, "PATCH", "/api/settings/ai/test", nil), http.StatusMethodNotAllowed, "not allowed")
}

func TestTestAISettings(t *testing.T) {
	h, _ := newTestHandler(t)
	var seen kbai.Config
	stubAIProbe(t, func(_ context.Context, _ *store.Store, user string, cfg kbai.Config) error {
		if user != "default" {
			t.Fatalf("user = %q", user)
		}
		seen = cfg
		return nil
	})
	wantStatus(t, call(t, h, "POST", "/api/settings/ai/test", map[string]any{
		"baseURL": " https://api.example.com/v1 ", "model": " gpt-test ", "apiKey": "sk-secret",
	}), http.StatusNoContent)
	if seen != (kbai.Config{BaseURL: "https://api.example.com/v1", Model: "gpt-test", Key: "sk-secret"}) {
		t.Fatalf("probe config = %+v", seen)
	}

	// A bare POST tests the stored configuration.
	seen = kbai.Config{}
	wantStatus(t, call(t, h, "POST", "/api/settings/ai/test", nil), http.StatusNoContent)
	if seen != (kbai.Config{}) {
		t.Fatalf("probe config = %+v", seen)
	}
}

func TestTestAISettingsFailures(t *testing.T) {
	h, _ := newTestHandler(t)

	stubAIProbe(t, func(context.Context, *store.Store, string, kbai.Config) error {
		return &kbai.Error{Code: http.StatusBadRequest, Message: "enter the API key to test a different endpoint"}
	})
	wantError(t, call(t, h, "POST", "/api/settings/ai/test", map[string]any{"baseURL": "https://other.example.com/v1"}),
		http.StatusBadRequest, "enter the API key")

	stubAIProbe(t, func(context.Context, *store.Store, string, kbai.Config) error {
		return errors.New("upstream rejected sk-secret\nwith\ttrash")
	})
	body := wantError(t, call(t, h, "POST", "/api/settings/ai/test", map[string]any{"apiKey": "sk-secret"}),
		http.StatusBadGateway, "[redacted]")
	if msg, _ := body["error"].(string); strings.Contains(msg, "sk-secret") || strings.Contains(msg, "\n") {
		t.Fatalf("error = %q", msg)
	}

	// A code the AI package never assigns as a status falls back to 502.
	stubAIProbe(t, func(context.Context, *store.Store, string, kbai.Config) error {
		return &kbai.Error{Code: 7, Message: "odd"}
	})
	wantError(t, call(t, h, "POST", "/api/settings/ai/test", nil), http.StatusBadGateway, "odd")

	wantError(t, call(t, h, "POST", "/api/settings/ai/test", "{"), http.StatusBadRequest, "malformed JSON")
}

// TestProbeAIDefault exercises the real probe with a configuration it rejects
// before any connection is attempted.
func TestProbeAIDefault(t *testing.T) {
	_, st := newTestHandler(t)
	if err := probeAI(context.Background(), st, "default", kbai.Config{}); err == nil {
		t.Fatal("probe accepted an unconfigured endpoint")
	}
}

func TestListForgeSourcesEmpty(t *testing.T) {
	h, _ := newTestHandler(t)
	rec := call(t, h, "GET", "/api/settings/forge", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != `{"sources":[]}` {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
}

func TestPutForgeSource(t *testing.T) {
	h, _ := newTestHandler(t)
	rec := call(t, h, "PUT", "/api/settings/forge/Work", map[string]any{
		"kind": "gitlab", "baseURL": "gitlab.example.com", "token": "glpat-secret",
	})
	body := wantStatus(t, rec, http.StatusOK)
	source, _ := body["source"].(map[string]any)
	if source["name"] != "work" || source["kind"] != "gitlab" || source["baseURL"] != "https://gitlab.example.com" || source["hasToken"] != true {
		t.Fatalf("source = %v", source)
	}
	if created, _ := source["createdAt"].(string); !strings.HasSuffix(created, "Z") {
		t.Fatalf("createdAt = %v", source["createdAt"])
	}
	if strings.Contains(rec.Body.String(), "glpat-secret") {
		t.Fatalf("write echoed the token: %s", rec.Body.String())
	}

	list := wantStatus(t, call(t, h, "GET", "/api/settings/forge", nil), http.StatusOK)
	sources, _ := list["sources"].([]any)
	if len(sources) != 1 {
		t.Fatalf("sources = %v", list)
	}

	// An origin change without a token drops the stored one.
	body = wantStatus(t, call(t, h, "PUT", "/api/settings/forge/work", map[string]any{
		"kind": "gitlab", "baseURL": "https://gitlab.other.example",
	}), http.StatusOK)
	if body["tokenCleared"] != true {
		t.Fatalf("tokenCleared = %v", body)
	}
	source, _ = body["source"].(map[string]any)
	if source["hasToken"] != false {
		t.Fatalf("token followed the endpoint: %v", source)
	}

	// An explicit empty token clears it, an omitted one keeps it.
	wantStatus(t, call(t, h, "PUT", "/api/settings/forge/work", map[string]any{"kind": "gitlab", "token": "glpat-2"}), http.StatusOK)
	body = wantStatus(t, call(t, h, "PUT", "/api/settings/forge/work", map[string]any{"kind": "github"}), http.StatusOK)
	source, _ = body["source"].(map[string]any)
	if source["hasToken"] != true || source["kind"] != "github" {
		t.Fatalf("source = %v", source)
	}
	body = wantStatus(t, call(t, h, "PUT", "/api/settings/forge/work", map[string]any{"kind": "github", "token": ""}), http.StatusOK)
	source, _ = body["source"].(map[string]any)
	if source["hasToken"] != false {
		t.Fatalf("token survived a clear: %v", source)
	}
}

func TestPutForgeSourceRejects(t *testing.T) {
	h, _ := newTestHandler(t)
	body := wantError(t, call(t, h, "PUT", "/api/settings/forge/work", map[string]any{
		"kind": "svn", "baseURL": "example.com", "token": "glpat-secret",
	}), http.StatusBadRequest, "invalid forge kind")
	if msg, _ := body["error"].(string); strings.Contains(msg, "glpat-secret") {
		t.Fatalf("rejected write echoed the token: %q", msg)
	}
	wantError(t, call(t, h, "PUT", "/api/settings/forge/not!a!name", map[string]any{"kind": "gitlab", "baseURL": "example.com"}),
		http.StatusBadRequest, "invalid forge source name")
	wantError(t, call(t, h, "PUT", "/api/settings/forge/work", map[string]any{"kind": "gitlab"}),
		http.StatusBadRequest, "base URL is required")
	wantError(t, call(t, h, "PUT", "/api/settings/forge/work", "{"), http.StatusBadRequest, "malformed JSON")
	wantError(t, call(t, h, "PATCH", "/api/settings/forge/work", nil), http.StatusMethodNotAllowed, "not allowed")
	wantError(t, call(t, h, "POST", "/api/settings/forge", nil), http.StatusMethodNotAllowed, "not allowed")
}

func TestDeleteForgeSource(t *testing.T) {
	h, _ := newTestHandler(t)
	wantStatus(t, call(t, h, "PUT", "/api/settings/forge/work", map[string]any{"kind": "gitlab", "baseURL": "gitlab.example.com"}), http.StatusOK)
	wantStatus(t, call(t, h, "DELETE", "/api/settings/forge/work", nil), http.StatusNoContent)
	list := wantStatus(t, call(t, h, "GET", "/api/settings/forge", nil), http.StatusOK)
	if sources, _ := list["sources"].([]any); len(sources) != 0 {
		t.Fatalf("sources = %v", list)
	}
	// Deleting what is not there succeeds; an unusable name does not.
	wantStatus(t, call(t, h, "DELETE", "/api/settings/forge/work", nil), http.StatusNoContent)
	wantError(t, call(t, h, "DELETE", "/api/settings/forge/not!a!name", nil), http.StatusBadRequest, "invalid forge source name")
}

func TestForgeSourcesStoreError(t *testing.T) {
	h, st := newTestHandler(t)
	st.Close()
	wantStatus(t, call(t, h, "GET", "/api/settings/forge", nil), http.StatusInternalServerError)
}

// TestForgeSourceLookup covers the read-back a write echoes: a source that is
// not there and a store that cannot answer both report 500 rather than an
// empty source, and neither is reachable through a single request.
func TestForgeSourceLookup(t *testing.T) {
	_, st := newTestHandler(t)
	s := &server{st: st, user: "default"}
	rec := httptest.NewRecorder()
	s.writeForgeSource(rec, "missing", false)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("missing source = %d %s", rec.Code, rec.Body.String())
	}
	if _, err := s.forgeSource("missing"); err == nil {
		t.Fatal("lookup accepted a missing source")
	}
	st.Close()
	if _, err := s.forgeSource("missing"); err == nil {
		t.Fatal("lookup accepted a closed store")
	}
}

func TestTestForgeSource(t *testing.T) {
	h, _ := newTestHandler(t)
	var seen forge.ForgeProbeConfig
	stubForgeProbe(t, func(_ context.Context, _ *store.Store, user string, cfg forge.ForgeProbeConfig) error {
		if user != "default" {
			t.Fatalf("user = %q", user)
		}
		seen = cfg
		return nil
	})
	wantStatus(t, call(t, h, "POST", "/api/settings/forge/work/test", map[string]any{
		"kind": " gitlab ", "baseURL": "gitlab.example.com", "project": "owner/repo", "token": "glpat-secret", "saved": true,
	}), http.StatusNoContent)
	want := forge.ForgeProbeConfig{Name: "work", Kind: "gitlab", BaseURL: "gitlab.example.com", Project: "owner/repo", Token: "glpat-secret", Saved: true}
	if seen != want {
		t.Fatalf("probe config = %+v", seen)
	}

	seen = forge.ForgeProbeConfig{}
	wantStatus(t, call(t, h, "POST", "/api/settings/forge/work/test", nil), http.StatusNoContent)
	if seen != (forge.ForgeProbeConfig{Name: "work"}) {
		t.Fatalf("probe config = %+v", seen)
	}
}

func TestTestForgeSourceFailures(t *testing.T) {
	h, _ := newTestHandler(t)

	stubForgeProbe(t, func(context.Context, *store.Store, string, forge.ForgeProbeConfig) error {
		return errors.New("connection failed")
	})
	wantError(t, call(t, h, "POST", "/api/settings/forge/work/test", map[string]any{"kind": "gitlab"}),
		http.StatusBadGateway, "connection failed")

	stubForgeProbe(t, func(context.Context, *store.Store, string, forge.ForgeProbeConfig) error {
		return errors.New("enter the token to test a different endpoint: glpat-secret")
	})
	body := wantError(t, call(t, h, "POST", "/api/settings/forge/work/test", map[string]any{"kind": "gitlab", "token": "glpat-secret"}),
		http.StatusBadRequest, "[redacted]")
	if msg, _ := body["error"].(string); strings.Contains(msg, "glpat-secret") {
		t.Fatalf("error = %q", msg)
	}

	wantError(t, call(t, h, "POST", "/api/settings/forge/work/test", "{"), http.StatusBadRequest, "malformed JSON")
}

// TestTestForgeSourceAgainstServer drives the real forge prober against a
// local test server, so the endpoint is exercised end to end without reaching
// the network.
func TestTestForgeSourceAgainstServer(t *testing.T) {
	h, _ := newTestHandler(t)
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	stubForgeProbe(t, func(ctx context.Context, st *store.Store, user string, cfg forge.ForgeProbeConfig) error {
		return forge.NewForgeProberWithClient(st, srv.Client()).Probe(ctx, user, cfg)
	})
	wantStatus(t, call(t, h, "POST", "/api/settings/forge/work/test", map[string]any{
		"kind": "gitlab", "baseURL": srv.URL, "token": "glpat-secret",
	}), http.StatusNoContent)
	if path != "/api/v4/version" {
		t.Fatalf("probe path = %q", path)
	}
}

// TestProbeForgeDefault exercises the real prober with a configuration it
// rejects before any connection is attempted.
func TestProbeForgeDefault(t *testing.T) {
	_, st := newTestHandler(t)
	if err := probeForge(context.Background(), st, "default", forge.ForgeProbeConfig{}); err == nil {
		t.Fatal("probe accepted a nameless integration")
	}
}

func TestSettingsMessage(t *testing.T) {
	if got := settingsMessage(nil); got != "" {
		t.Fatalf("nil error = %q", got)
	}
	if got := settingsMessage(errors.New("\x1b[31m")); got != "operation failed" {
		t.Fatalf("empty message = %q", got)
	}
	if got := settingsMessage(errors.New("said  sk-key\nhere"), " sk-key "); got != "said [redacted] here" {
		t.Fatalf("redacted = %q", got)
	}
	if got := settingsMessage(errors.New("bad\x07value")); got != "badvalue" {
		t.Fatalf("control character survived: %q", got)
	}
	long := settingsMessage(errors.New(strings.Repeat("a", 400)))
	if len([]rune(long)) != settingsMessageLimit || !strings.HasSuffix(long, "...") {
		t.Fatalf("capped = %q (%d runes)", long, len([]rune(long)))
	}
	if got := settingsValue(nil); got != "" {
		t.Fatalf("settingsValue(nil) = %q", got)
	}
}
