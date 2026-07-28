package main

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/server"
	"github.com/RandomCodeSpace/kb/internal/store"
)

// TestWiring exercises the startup path main performs: secret creation,
// SQLite store at <data>/kb.db, legacy markdown import from the data dir,
// and server.New over the embedded dist. Handler behavior itself is covered
// in internal/server.
func TestWiring(t *testing.T) {
	dataDir := t.TempDir()
	legacy := "# Alice\n\n## To Do\n\n- [ ] imported task #tag1\n"
	if err := os.WriteFile(filepath.Join(dataDir, "alice.md"), []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy board: %v", err)
	}

	secret, err := store.LoadOrCreateSecret(dataDir)
	if err != nil {
		t.Fatalf("LoadOrCreateSecret: %v", err)
	}
	st, err := store.Open(filepath.Join(dataDir, "kb.db"), secret)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	imported, err := st.ImportMarkdownDir(dataDir)
	if err != nil {
		t.Fatalf("ImportMarkdownDir: %v", err)
	}
	if imported != 1 {
		t.Fatalf("imported = %d, want 1", imported)
	}

	static, err := fs.Sub(distFS, "dist")
	if err != nil {
		t.Fatalf("embedded dist: %v", err)
	}
	h := server.New(server.Config{}, static, st)

	get := func(target, user string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("GET", target, nil)
		if user != "" {
			r.Header.Set("X-KB-User", user)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	if w := get("/", ""); w.Code != http.StatusOK {
		t.Errorf("GET / (embedded SPA): got %d, want 200", w.Code)
	}
	w := get("/api/board", "alice")
	if w.Code != http.StatusOK {
		t.Fatalf("GET imported board: got %d, want 200 (body=%s)", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), "imported task") {
		t.Errorf("imported board body = %q, want the legacy task", w.Body.String())
	}
	if w := get("/api/board", ""); w.Code != http.StatusNotFound {
		t.Errorf("GET board for fresh user: got %d, want 404", w.Code)
	}
}
