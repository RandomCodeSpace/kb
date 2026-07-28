package server

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
)

var testStatic = fstest.MapFS{
	"index.html": &fstest.MapFile{Data: []byte("<html>spa</html>")},
}

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "kb.db"), []byte("test-secret"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func newTestServer(t *testing.T, cfg Config) (http.Handler, *store.Store) {
	t.Helper()
	st := newTestStore(t)
	return New(cfg, testStatic, st), st
}

// doReq drives a request the way a legitimate client does: a loopback Host
// and the content type the target actually parses, both of which the /api/*
// guard requires. Either can be overridden through hdr ("Host" is lifted onto
// the request, since Go keeps it out of the header map) so tests can forge
// them.
func doReq(t *testing.T, h http.Handler, method, target, body string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
		if strings.HasPrefix(target, "/api/board") {
			r.Header.Set("Content-Type", "text/markdown")
		} else {
			r.Header.Set("Content-Type", "application/json")
		}
	}
	r.Host = "127.0.0.1:8080"
	for k, v := range hdr {
		if strings.EqualFold(k, "Host") {
			r.Host = v
			continue
		}
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// canonical is the markdown the server returns for a stored board: the wire
// format survives a Parse/Serialize round trip, not raw bytes.
func canonical(md string) string {
	return board.Serialize(board.Parse(md))
}

func TestSanitizeUser(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"Alice@Example.COM", "alice@example.com", false},
		{"bob", "bob", false},
		{"first.last_1-2", "first.last_1-2", false},
		{"AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE", "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", false},
		// Chars outside [a-z0-9._@-] are rejected, never substituted:
		// substitution would map distinct identities to the same board.
		{"user name+tag", "", true},
		{"foo/bar", "", true},
		{`foo\..\bar`, "", true},
		{"héllo", "", true},
		{"../../etc/passwd", "", true},
		{"..", "", true},
		{".", "", true},
		{".hidden", "", true},
		{"", "", true},
		{strings.Repeat("a", 300), "", true},
	}
	for _, tt := range tests {
		got, err := sanitizeUser(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("sanitizeUser(%q) = %q, want error", tt.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("sanitizeUser(%q) unexpected error: %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("sanitizeUser(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestOpenModeRoundTrip(t *testing.T) {
	h, st := newTestServer(t, Config{})

	if w := doReq(t, h, "GET", "/api/board", "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("GET before save: got %d, want 404", w.Code)
	}
	const in = "# My Board\n\n## To Do\n\n- [ ] 🚀 thing !1 @2026-08-01 ~M #backend\n  detail line\n  - [ ] substep\n"
	if w := doReq(t, h, "PUT", "/api/board", in, nil); w.Code != http.StatusNoContent {
		t.Fatalf("PUT: got %d, want 204 (body=%s)", w.Code, w.Body)
	}
	w := doReq(t, h, "GET", "/api/board", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET after save: got %d, want 200", w.Code)
	}
	if want := canonical(in); w.Body.String() != want {
		t.Errorf("GET body = %q, want %q", w.Body.String(), want)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/markdown") {
		t.Errorf("Content-Type = %q, want text/markdown", ct)
	}
	// The board lives in the store now, keyed by user.
	b, err := st.Board("default")
	if err != nil {
		t.Fatalf("store.Board: %v", err)
	}
	if b.Title != "My Board" || len(b.Tasks) != 1 || b.Tasks[0].Title != "thing" {
		t.Errorf("stored board = %q with %d tasks, want My Board with 1", b.Title, len(b.Tasks))
	}

	// Named user gets an independent board under the sanitized identity.
	hdr := map[string]string{"X-KB-User": "Alice"}
	if w := doReq(t, h, "PUT", "/api/board", "# alice\n", hdr); w.Code != http.StatusNoContent {
		t.Fatalf("PUT as Alice: got %d, want 204", w.Code)
	}
	if w := doReq(t, h, "GET", "/api/board", "", hdr); w.Body.String() != canonical("# alice\n") {
		t.Errorf("GET as Alice = %q, want %q", w.Body.String(), canonical("# alice\n"))
	}
	if w := doReq(t, h, "GET", "/api/board", "", nil); !strings.Contains(w.Body.String(), "thing") {
		t.Errorf("default board lost after Alice write: %q", w.Body.String())
	}
	if has, err := st.HasBoard("alice"); err != nil || !has {
		t.Errorf("HasBoard(alice) = %v, %v; want true", has, err)
	}
}

func TestTokenMode(t *testing.T) {
	h, _ := newTestServer(t, Config{Token: "s3cret"})

	if w := doReq(t, h, "GET", "/api/board", "", nil); w.Code != http.StatusUnauthorized {
		t.Errorf("no auth: got %d, want 401", w.Code)
	}
	wrong := map[string]string{"Authorization": "Bearer wrong", "X-KB-User": "bob"}
	if w := doReq(t, h, "GET", "/api/board", "", wrong); w.Code != http.StatusUnauthorized {
		t.Errorf("wrong token: got %d, want 401", w.Code)
	}
	noUser := map[string]string{"Authorization": "Bearer s3cret"}
	if w := doReq(t, h, "PUT", "/api/board", "# b\n", noUser); w.Code != http.StatusBadRequest {
		t.Errorf("missing X-KB-User: got %d, want 400", w.Code)
	}
	good := map[string]string{"Authorization": "Bearer s3cret", "X-KB-User": "bob"}
	if w := doReq(t, h, "PUT", "/api/board", "# b\n", good); w.Code != http.StatusNoContent {
		t.Fatalf("PUT with token: got %d, want 204 (body=%s)", w.Code, w.Body)
	}
	w := doReq(t, h, "GET", "/api/board", "", good)
	if w.Code != http.StatusOK || w.Body.String() != canonical("# b\n") {
		t.Errorf("GET with token: got %d body %q", w.Code, w.Body.String())
	}
	// Health stays unauthenticated even in token mode.
	if w := doReq(t, h, "GET", "/api/health", "", nil); w.Code != http.StatusOK {
		t.Errorf("health: got %d, want 200", w.Code)
	}
	// Every new endpoint sits behind the same auth check.
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/api/labels", ""},
		{"GET", "/api/settings", ""},
		{"PUT", "/api/settings", `{"ai_model":"m"}`},
		{"POST", "/api/ai/test", ""},
		{"POST", "/api/ai/story", `{"mode":"create","prompt":"p"}`},
	} {
		if w := doReq(t, h, tc.method, tc.path, tc.body, nil); w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without auth: got %d, want 401", tc.method, tc.path, w.Code)
		}
	}
}

func TestPutBodyLimit(t *testing.T) {
	h, _ := newTestServer(t, Config{})

	over := strings.Repeat("x", maxBodyBytes+1)
	if w := doReq(t, h, "PUT", "/api/board", over, nil); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized PUT: got %d, want 413", w.Code)
	}
	exact := strings.Repeat("x", maxBodyBytes)
	if w := doReq(t, h, "PUT", "/api/board", exact, nil); w.Code != http.StatusNoContent {
		t.Errorf("exactly 1 MiB PUT: got %d, want 204", w.Code)
	}
}

func TestHealthAndStatic(t *testing.T) {
	h, _ := newTestServer(t, Config{})

	w := doReq(t, h, "GET", "/api/health", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("health: got %d, want 200", w.Code)
	}
	if w.Body.String() != `{"ok":true}` {
		t.Errorf("health body = %q", w.Body.String())
	}
	if w := doReq(t, h, "GET", "/", "", nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "spa") {
		t.Errorf("GET /: got %d body %q", w.Code, w.Body.String())
	}
	// SPA fallback for unknown non-/api paths.
	if w := doReq(t, h, "GET", "/some/client/route", "", nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "spa") {
		t.Errorf("SPA fallback: got %d body %q", w.Code, w.Body.String())
	}
	// Unknown /api paths must 404, not fall back to index.html.
	if w := doReq(t, h, "GET", "/api/nope", "", nil); w.Code != http.StatusNotFound {
		t.Errorf("GET /api/nope: got %d, want 404", w.Code)
	}
	// Wrong method on /api/board falls through to the catch-all, whose /api
	// guard rejects it rather than serving the SPA.
	if w := doReq(t, h, "POST", "/api/board", "x", nil); w.Code != http.StatusNotFound {
		t.Errorf("POST /api/board: got %d, want 404", w.Code)
	}
}

// The released binary is built without VITE_* values, so the SPA reads the
// Entra IDs here. Both are public by design, but the endpoint is
// unauthenticated, so it must expose those two and nothing else.
func TestConfigEndpoint(t *testing.T) {
	t.Run("unset yields empty strings", func(t *testing.T) {
		h, _ := newTestServer(t, Config{})
		w := doReq(t, h, "GET", "/api/config", "", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("GET config: got %d, want 200", w.Code)
		}
		var got map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("config JSON: %v (body=%s)", err, w.Body)
		}
		want := map[string]any{"azure_client_id": "", "azure_tenant_id": ""}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("config = %v, want %v", got, want)
		}
	})

	t.Run("configured and readable without auth", func(t *testing.T) {
		const tenant = "11111111-2222-3333-4444-555555555555"
		st := newTestStore(t)
		// Token mode as well, to prove the secret never appears here and that
		// the endpoint answers with no Authorization header — the SPA needs
		// these IDs before it can log in.
		h := New(Config{TenantID: tenant, ClientID: "app-client-id", Token: "s3cret"}, testStatic, st)
		w := doReq(t, h, "GET", "/api/config", "", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("GET config: got %d, want 200 (body=%s)", w.Code, w.Body)
		}
		var got map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("config JSON: %v", err)
		}
		want := map[string]any{"azure_client_id": "app-client-id", "azure_tenant_id": tenant}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("config = %v, want exactly the two public IDs", got)
		}
		if strings.Contains(w.Body.String(), "s3cret") {
			t.Errorf("config leaks the shared secret: %s", w.Body)
		}
	})
}

// The phase-3 additions (the %blocked title token and the Cancelled section)
// have to survive the whole loop: store -> server -> wire -> client -> wire ->
// server -> store. The client leg is the shared codec fixture that
// src/lib/markdown.test.ts and internal/board/fixtures_test.go both pin, so
// replaying that exact file through the API covers the rest of the loop.
func TestBlockedAndCancelledSurviveTheAPIRoundTrip(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "board", "testdata", "phase3.md"))
	if err != nil {
		t.Fatal(err)
	}
	wire := string(raw)
	h, st := newTestServer(t, Config{})

	if w := doReq(t, h, "PUT", "/api/board", wire, nil); w.Code != http.StatusNoContent {
		t.Fatalf("PUT: got %d, want 204 (body=%s)", w.Code, w.Body)
	}

	// The store keeps the flags as columns, not as leftover wire text.
	stored, err := st.Board("default")
	if err != nil {
		t.Fatalf("store.Board: %v", err)
	}
	byTitle := map[string]board.Task{}
	for _, task := range stored.Tasks {
		byTitle[task.Title] = task
	}
	if got := byTitle["Waiting on legal"]; !got.Blocked || got.Status != board.StatusTodo {
		t.Errorf("blocked todo stored as %+v, want blocked in todo", got)
	}
	if got := byTitle["Why %blocked matters"]; got.Blocked {
		t.Error("an escaped literal blocked token in the title set the flag through the API")
	}
	if got := byTitle["Dropped experiment"]; got.Status != board.StatusCancelled || !got.Blocked {
		t.Errorf("cancelled task stored as %+v, want blocked in cancelled", got)
	}

	// And the board the SPA reads back is byte-identical to what it sent, so
	// the next client-side parse/serialize is a no-op rather than an edit.
	w := doReq(t, h, "GET", "/api/board", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET: got %d, want 200", w.Code)
	}
	if w.Body.String() != wire {
		t.Errorf("GET body drifted from the fixture:\n--- got ---\n%s\n--- want ---\n%s", w.Body, wire)
	}

	// A second lap (the client re-serializing what it read) changes nothing,
	// including the version token.
	etag := w.Header().Get("ETag")
	if w := doReq(t, h, "PUT", "/api/board", canonical(w.Body.String()),
		map[string]string{"If-Match": etag}); w.Code != http.StatusNoContent {
		t.Fatalf("second PUT: got %d, want 204 (body=%s)", w.Code, w.Body)
	}
	if again := doReq(t, h, "GET", "/api/board", "", nil); again.Body.String() != wire ||
		again.Header().Get("ETag") != etag {
		t.Errorf("second lap changed the board or its token: etag %q -> %q",
			etag, again.Header().Get("ETag"))
	}
}

// A whole-board PUT would otherwise delete tasks the CLI or MCP wrote since
// the client last read. The ETag is the version token that makes the write
// conditional.
func TestBoardVersionToken(t *testing.T) {
	h, st := newTestServer(t, Config{})

	const seed = "# B\n\n## To Do\n\n- [ ] first\n"
	if w := doReq(t, h, "PUT", "/api/board", seed, nil); w.Code != http.StatusNoContent {
		t.Fatalf("seed PUT: got %d (body=%s)", w.Code, w.Body)
	}
	w := doReq(t, h, "GET", "/api/board", "", nil)
	etag := w.Header().Get("ETag")
	if etag == "" {
		t.Fatal("GET board returned no ETag")
	}

	// A conditional write with the token from that GET goes through.
	w = doReq(t, h, "PUT", "/api/board", "# B\n\n## To Do\n\n- [ ] first\n- [ ] second\n",
		map[string]string{"If-Match": etag})
	if w.Code != http.StatusNoContent {
		t.Fatalf("matching If-Match PUT: got %d, want 204 (body=%s)", w.Code, w.Body)
	}
	if next := w.Header().Get("ETag"); next == "" || next == etag {
		t.Errorf("PUT ETag = %q, want a new token (was %q)", next, etag)
	}

	// The stale token from the first GET must now lose.
	stale := doReq(t, h, "PUT", "/api/board", "# B\n\n## To Do\n\n- [ ] clobbered\n",
		map[string]string{"If-Match": etag})
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale If-Match PUT: got %d, want 409", stale.Code)
	}
	if stale.Header().Get("ETag") == "" {
		t.Error("409 carried no current ETag for the client to refetch against")
	}
	b, err := st.Board("default")
	if err != nil {
		t.Fatalf("store.Board: %v", err)
	}
	if len(b.Tasks) != 2 {
		t.Errorf("rejected PUT still wrote: board has %d tasks, want 2", len(b.Tasks))
	}

	// A write by another process (CLI, MCP) moves the token even though it
	// never went through this handler — which is why the token is derived
	// from content rather than from a counter this server keeps.
	current := doReq(t, h, "GET", "/api/board", "", nil).Header().Get("ETag")
	if err := st.ReplaceBoard("default", board.Parse("# B\n\n## To Do\n\n- [ ] from the CLI\n")); err != nil {
		t.Fatalf("out-of-band write: %v", err)
	}
	if again := doReq(t, h, "GET", "/api/board", "", nil).Header().Get("ETag"); again == current {
		t.Error("token did not change after an out-of-band write")
	}
	if w := doReq(t, h, "PUT", "/api/board", "# B\n", map[string]string{"If-Match": current}); w.Code != http.StatusConflict {
		t.Errorf("PUT after an out-of-band write: got %d, want 409", w.Code)
	}

	// If-Match: * only asserts existence, and an unconditional PUT keeps the
	// old last-writer-wins behavior for clients that send no token.
	if w := doReq(t, h, "PUT", "/api/board", "# B\n\n## To Do\n\n- [ ] star\n", map[string]string{"If-Match": "*"}); w.Code != http.StatusNoContent {
		t.Errorf("If-Match * PUT: got %d, want 204", w.Code)
	}
	if w := doReq(t, h, "PUT", "/api/board", "# B\n\n## To Do\n\n- [ ] plain\n", nil); w.Code != http.StatusNoContent {
		t.Errorf("unconditional PUT: got %d, want 204", w.Code)
	}
}

// A client whose first GET 404s must still learn a version token, or its
// first PUT is unconditional — and that write is exactly the one most likely
// to land on a board the CLI or MCP created moments earlier.
func TestBoardVersionTokenOnMissingBoard(t *testing.T) {
	h, st := newTestServer(t, Config{})

	w := doReq(t, h, "GET", "/api/board", "", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("GET with no board: got %d, want 404", w.Code)
	}
	etag := w.Header().Get("ETag")
	if etag == "" {
		t.Fatal("404 carried no ETag, so the client's first PUT would be unconditional")
	}

	// Another surface creates the board between the client's GET and its PUT.
	if err := st.ReplaceBoard("default", board.Parse("# Real\n\n## To Do\n\n- [ ] important CLI task\n")); err != nil {
		t.Fatalf("out-of-band write: %v", err)
	}
	seed := "# kb\n\n## To Do\n\n- [ ] demo card\n"
	if w := doReq(t, h, "PUT", "/api/board", seed, map[string]string{"If-Match": etag}); w.Code != http.StatusConflict {
		t.Fatalf("first PUT after an out-of-band create: got %d, want 409", w.Code)
	}
	b, err := st.Board("default")
	if err != nil {
		t.Fatalf("store.Board: %v", err)
	}
	if len(b.Tasks) != 1 || b.Tasks[0].Title != "important CLI task" {
		t.Errorf("board after the rejected PUT = %+v, want the CLI task intact", b.Tasks)
	}

	// With nothing written in between, the same token still matches.
	h2, _ := newTestServer(t, Config{})
	fresh := doReq(t, h2, "GET", "/api/board", "", nil).Header().Get("ETag")
	if w := doReq(t, h2, "PUT", "/api/board", seed, map[string]string{"If-Match": fresh}); w.Code != http.StatusNoContent {
		t.Errorf("first PUT onto an empty board: got %d, want 204 (body=%s)", w.Code, w.Body)
	}
}

func TestLabels(t *testing.T) {
	h, _ := newTestServer(t, Config{})

	w := doReq(t, h, "GET", "/api/labels", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET labels: got %d, want 200", w.Code)
	}
	if got := strings.TrimSpace(w.Body.String()); got != "[]" {
		t.Errorf("empty labels body = %q, want []", got)
	}

	const in = "# B\n\n## To Do\n\n- [ ] t1 #backend #type::bug\n"
	if w := doReq(t, h, "PUT", "/api/board", in, nil); w.Code != http.StatusNoContent {
		t.Fatalf("PUT board: got %d", w.Code)
	}
	w = doReq(t, h, "GET", "/api/labels", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET labels: got %d, want 200", w.Code)
	}
	var labels []string
	if err := json.Unmarshal(w.Body.Bytes(), &labels); err != nil {
		t.Fatalf("labels JSON: %v (body=%s)", err, w.Body)
	}
	// Most recently used first.
	if want := []string{"type::bug", "backend"}; !reflect.DeepEqual(labels, want) {
		t.Errorf("labels = %v, want %v", labels, want)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	h, _ := newTestServer(t, Config{})

	getSettings := func() settingsResponse {
		t.Helper()
		w := doReq(t, h, "GET", "/api/settings", "", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("GET settings: got %d", w.Code)
		}
		if strings.Contains(w.Body.String(), "sk-secret-123") {
			t.Fatalf("settings response leaks the key: %s", w.Body)
		}
		var got settingsResponse
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("settings JSON: %v", err)
		}
		return got
	}

	if got := getSettings(); got != (settingsResponse{}) {
		t.Errorf("default settings = %+v, want zero", got)
	}
	put := `{"ai_base_url":"https://api.example.com/v1","ai_model":"gpt-test","ai_key":"sk-secret-123"}`
	if w := doReq(t, h, "PUT", "/api/settings", put, nil); w.Code != http.StatusNoContent {
		t.Fatalf("PUT settings: got %d (body=%s)", w.Code, w.Body)
	}
	want := settingsResponse{BaseURL: "https://api.example.com/v1", Model: "gpt-test", HasKey: true}
	if got := getSettings(); got != want {
		t.Errorf("settings = %+v, want %+v", got, want)
	}
	// Subset PUT patches only the given field.
	if w := doReq(t, h, "PUT", "/api/settings", `{"ai_model":"other"}`, nil); w.Code != http.StatusNoContent {
		t.Fatalf("PUT subset: got %d", w.Code)
	}
	want.Model = "other"
	if got := getSettings(); got != want {
		t.Errorf("after subset PUT = %+v, want %+v", got, want)
	}
	// Empty key clears the stored key.
	if w := doReq(t, h, "PUT", "/api/settings", `{"ai_key":""}`, nil); w.Code != http.StatusNoContent {
		t.Fatalf("PUT clear key: got %d", w.Code)
	}
	want.HasKey = false
	if got := getSettings(); got != want {
		t.Errorf("after key clear = %+v, want %+v", got, want)
	}
	// Bad inputs.
	if w := doReq(t, h, "PUT", "/api/settings", `{"ai_base_url":"ftp://example.com"}`, nil); w.Code != http.StatusBadRequest {
		t.Errorf("non-http scheme: got %d, want 400", w.Code)
	}
	if w := doReq(t, h, "PUT", "/api/settings", `not json`, nil); w.Code != http.StatusBadRequest {
		t.Errorf("bad JSON: got %d, want 400", w.Code)
	}
}

func TestPutSettingsBaseURLChangeClearsKey(t *testing.T) {
	h, _ := newTestServer(t, Config{})

	put := `{"ai_base_url":"https://api.example.com/v1","ai_model":"m","ai_key":"sk-secret-123"}`
	if w := doReq(t, h, "PUT", "/api/settings", put, nil); w.Code != http.StatusNoContent {
		t.Fatalf("seed PUT settings: got %d (body=%s)", w.Code, w.Body)
	}
	// Re-pointing the base URL to another host without re-sending the key
	// must drop the stored key and say so.
	w := doReq(t, h, "PUT", "/api/settings", `{"ai_base_url":"https://evil.example.net"}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("cross-host PUT: got %d, want 200 (body=%s)", w.Code, w.Body)
	}
	var res struct {
		KeyCleared bool `json:"key_cleared"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil || !res.KeyCleared {
		t.Fatalf("cross-host PUT body = %s (err=%v), want key_cleared true", w.Body, err)
	}
	w = doReq(t, h, "GET", "/api/settings", "", nil)
	var got settingsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("settings JSON: %v", err)
	}
	if got.HasKey {
		t.Error("key survived a cross-host base URL change")
	}
}

func writeJWKS(w http.ResponseWriter, pub *rsa.PublicKey, kid string) {
	doc := map[string]any{
		"keys": []map[string]string{{
			"kty": "RSA",
			"use": "sig",
			"kid": kid,
			"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
		}},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(doc)
}

func TestEntraAuth(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	const kid = "test-kid"
	var fetches atomic.Int32
	jwksSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetches.Add(1)
		writeJWKS(w, &key.PublicKey, kid)
	}))
	defer jwksSrv.Close()

	const tenant = "11111111-2222-3333-4444-555555555555"
	const clientID = "app-client-id"
	issuer := "https://login.microsoftonline.com/" + tenant + "/v2.0"
	st := newTestStore(t)
	h := New(Config{
		TenantID: tenant,
		ClientID: clientID,
		JWKSURL:  jwksSrv.URL,
	}, testStatic, st)

	const oid = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	base := func() jwt.MapClaims {
		now := time.Now()
		return jwt.MapClaims{
			"iss":   issuer,
			"aud":   clientID,
			"exp":   now.Add(time.Hour).Unix(),
			"nbf":   now.Add(-time.Minute).Unix(),
			"iat":   now.Add(-time.Minute).Unix(),
			"oid":   oid,
			"email": "user@example.com",
		}
	}
	sign := func(claims jwt.MapClaims, kid string) string {
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		if kid != "" {
			tok.Header["kid"] = kid
		}
		s, err := tok.SignedString(key)
		if err != nil {
			t.Fatalf("sign: %v", err)
		}
		return s
	}

	tests := []struct {
		name  string
		token func() string
		want  int
	}{
		{"valid", func() string { return sign(base(), kid) }, http.StatusNoContent},
		{"wrong issuer", func() string {
			c := base()
			c["iss"] = "https://login.microsoftonline.com/other-tenant/v2.0"
			return sign(c, kid)
		}, http.StatusUnauthorized},
		{"wrong audience", func() string {
			c := base()
			c["aud"] = "other-app"
			return sign(c, kid)
		}, http.StatusUnauthorized},
		{"expired", func() string {
			c := base()
			c["exp"] = time.Now().Add(-time.Hour).Unix()
			return sign(c, kid)
		}, http.StatusUnauthorized},
		{"not yet valid", func() string {
			c := base()
			c["nbf"] = time.Now().Add(time.Hour).Unix()
			return sign(c, kid)
		}, http.StatusUnauthorized},
		{"missing exp", func() string {
			c := base()
			delete(c, "exp")
			return sign(c, kid)
		}, http.StatusUnauthorized},
		{"unknown kid", func() string { return sign(base(), "other-kid") }, http.StatusUnauthorized},
		{"missing kid", func() string { return sign(base(), "") }, http.StatusUnauthorized},
		{"hs256 rejected", func() string {
			tok := jwt.NewWithClaims(jwt.SigningMethodHS256, base())
			tok.Header["kid"] = kid
			s, err := tok.SignedString([]byte("shared-secret"))
			if err != nil {
				t.Fatalf("sign hs256: %v", err)
			}
			return s
		}, http.StatusUnauthorized},
		{"no oid claim", func() string {
			c := base()
			delete(c, "oid")
			return sign(c, kid)
		}, http.StatusUnauthorized},
		{"garbage token", func() string { return "not.a.jwt" }, http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hdr := map[string]string{"Authorization": "Bearer " + tt.token()}
			w := doReq(t, h, "PUT", "/api/board", "# board\n", hdr)
			if w.Code != tt.want {
				t.Fatalf("got %d, want %d (body=%s)", w.Code, tt.want, w.Body)
			}
		})
	}

	// The immutable oid claim decides the board identity — never email.
	if has, err := st.HasBoard(oid); err != nil || !has {
		t.Errorf("HasBoard(oid) = %v, %v; want true", has, err)
	}
	if has, err := st.HasBoard("user@example.com"); err != nil || has {
		t.Errorf("board keyed on mutable email claim; want oid only (has=%v err=%v)", has, err)
	}

	// Mutable claims are never accepted as identity, even when present.
	c := base()
	delete(c, "oid")
	c["preferred_username"] = "Fallback.User@Example.com"
	hdr := map[string]string{"Authorization": "Bearer " + sign(c, kid)}
	if w := doReq(t, h, "PUT", "/api/board", "# fb\n", hdr); w.Code != http.StatusUnauthorized {
		t.Fatalf("email/preferred_username without oid: got %d, want 401 (body=%s)", w.Code, w.Body)
	}

	// No Authorization header at all.
	if w := doReq(t, h, "GET", "/api/board", "", nil); w.Code != http.StatusUnauthorized {
		t.Errorf("missing header: got %d, want 401", w.Code)
	}

	// JWKS is fetched once and cached (unknown kid within cooldown does not refetch).
	if n := fetches.Load(); n != 1 {
		t.Errorf("jwks fetched %d times, want 1", n)
	}
}
