package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

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
		{"GET", "/api/similar?q=needle", ""},
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

func TestSimilarEndpoint(t *testing.T) {
	t.Run("caps excludes and uses lowercase keys without Origin", func(t *testing.T) {
		h, st := newTestServer(t, Config{})
		var excluded board.Task
		for _, title := range []string{"needle alpha", "needle beta", "needle gamma", "needle delta", "needle epsilon"} {
			task, err := st.AddTask("default", board.Task{Title: title})
			if err != nil {
				t.Fatalf("AddTask: %v", err)
			}
			if excluded.ID == "" {
				excluded = task
			}
		}

		w := doReq(t, h, "GET", "/api/similar?q=needle&exclude="+excluded.ID, "", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("GET similar: got %d, want 200 (body=%s)", w.Code, w.Body)
		}
		var got struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("similar JSON: %v", err)
		}
		if len(got.Items) != 3 {
			t.Fatalf("similar items = %d, want fixed limit 3", len(got.Items))
		}
		for _, item := range got.Items {
			if item["id"] == excluded.ID {
				t.Fatal("excluded card appeared in similar results")
			}
			if len(item) != 4 {
				t.Errorf("card similar item keys = %v, want only lowercase id/title/status/via", item)
			}
			for _, key := range []string{"id", "title", "status", "via"} {
				if _, ok := item[key]; !ok {
					t.Errorf("similar item has no lowercase %q key: %v", key, item)
				}
			}
		}
		if err := st.RecordImportLinks("default", []store.ImportLink{{
			ExternalKey: "archive-import", Link: "link::gitlab#7", Title: "archive provenance import",
		}}); err != nil {
			t.Fatalf("RecordImportLinks: %v", err)
		}
		w = doReq(t, h, "GET", "/api/similar?q=archive", "", nil)
		var importGot struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &importGot); err != nil || len(importGot.Items) != 1 {
			t.Fatalf("linked similar JSON = %s (err=%v)", w.Body, err)
		}
		if len(importGot.Items[0]) != 3 || importGot.Items[0]["link"] != "link::gitlab#7" ||
			importGot.Items[0]["title"] != "archive provenance import" || importGot.Items[0]["via"] != "import" {
			t.Errorf("import similar item = %v, want lowercase title/link/via only", importGot.Items[0])
		}
	})

	t.Run("returns initialized empty items without querying a closed store for fewer than three runes", func(t *testing.T) {
		st := newTestStore(t)
		h := New(Config{}, testStatic, st)
		if err := st.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}

		w := doReq(t, h, "GET", "/api/similar?q=%C3%A9%C3%A9", "", nil)
		if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"items":[]}` {
			t.Fatalf("short similar = %d %q, want 200 initialized empty items", w.Code, w.Body)
		}
		if w = doReq(t, h, "GET", "/api/similar?q=three", "", nil); w.Code != http.StatusInternalServerError {
			t.Fatalf("closed store similar = %d, want 500", w.Code)
		}
	})
}
