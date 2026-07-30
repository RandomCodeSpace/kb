package main

import (
	"io/fs"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/kb/internal/server"
	"github.com/RandomCodeSpace/kb/internal/store"
)

// Redirected logs can contain private board metadata, so the file must be
// private, retain earlier entries, and leave the process logger recoverable.
func TestConfigureLoggingCreatesAPrivateAppendOnlyFile(t *testing.T) {
	previousWriter := log.Writer()
	previousFlags := log.Flags()
	t.Cleanup(func() {
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
	})
	log.SetFlags(log.LstdFlags)

	path := filepath.Join(t.TempDir(), "kb.log")
	first, err := configureLogging(path)
	if err != nil {
		t.Fatalf("configureLogging(first): %v", err)
	}
	log.Print("first log line")
	if err := first.Close(); err != nil {
		t.Fatalf("close first log file: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat log file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("log file mode = %04o, want 0600", got)
	}
	if got := log.Flags(); got != log.LstdFlags {
		t.Errorf("log flags = %d, want %d", got, log.LstdFlags)
	}

	second, err := configureLogging(path)
	if err != nil {
		t.Fatalf("configureLogging(second): %v", err)
	}
	log.Print("second log line")
	if err := second.Close(); err != nil {
		t.Fatalf("close second log file: %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	got := string(content)
	firstAt := strings.Index(got, "first log line")
	secondAt := strings.Index(got, "second log line")
	if firstAt < 0 || secondAt < 0 || firstAt >= secondAt {
		t.Errorf("appended log = %q, want both lines in order", got)
	}
}

// An unset destination is the compatibility path: it must not alter stderr or
// leave callers with a closer that panics when deferred.
func TestConfigureLoggingWithAnEmptyPathLeavesTheLoggerUnchanged(t *testing.T) {
	previousWriter := log.Writer()
	previousFlags := log.Flags()
	t.Cleanup(func() {
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
	})

	closer, err := configureLogging("")
	if err != nil {
		t.Fatalf("configureLogging(empty): %v", err)
	}
	if closer == nil {
		t.Fatal("configureLogging(empty) returned a nil closer")
	}
	if got := log.Writer(); got != previousWriter {
		t.Errorf("log writer = %T, want original %T", got, previousWriter)
	}
	if got := log.Flags(); got != previousFlags {
		t.Errorf("log flags = %d, want %d", got, previousFlags)
	}
	if err := closer.Close(); err != nil {
		t.Errorf("close empty-path closer: %v", err)
	}
}

// A typo in the destination must stop startup instead of silently sending
// sensitive logs somewhere the operator is not watching.
func TestConfigureLoggingReturnsAnErrorForAnUnopenablePath(t *testing.T) {
	previousWriter := log.Writer()
	previousFlags := log.Flags()
	t.Cleanup(func() {
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
	})

	path := filepath.Join(t.TempDir(), "missing", "kb.log")
	closer, err := configureLogging(path)
	if err == nil {
		if closer != nil {
			_ = closer.Close()
		}
		t.Fatal("configureLogging(unopenable) returned no error")
	}
	if got := log.Writer(); got != previousWriter {
		t.Errorf("log writer changed after error: got %T, want %T", got, previousWriter)
	}
}

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
		// httptest defaults Host to example.com; open mode accepts loopback
		// only (see internal/server: DNS-rebinding guard).
		r.Host = "127.0.0.1:8080"
		if user != "" {
			r.Header.Set("X-KB-User", user)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	// dist/ is a build artifact and is not tracked on the branch (only
	// dist/.gitkeep is, so the go:embed directive still resolves). A
	// source-only checkout therefore embeds no index.html and cannot serve
	// the SPA; release commits and any tree where `vite build` has run can.
	if _, err := fs.Stat(static, "index.html"); err != nil {
		t.Log("no embedded index.html — source-only checkout, skipping the SPA check (run `npx vite build` to cover it)")
	} else if w := get("/", ""); w.Code != http.StatusOK {
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

// A zero-byte or truncated secret would encrypt every stored provider key
// under SHA-256 of a value an attacker can guess, so startup must stop rather
// than proceed with a known key.
func TestCheckSecret(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name    string
		secret  []byte
		wantErr bool
	}{
		{"empty file", []byte{}, true},
		{"nil", nil, true},
		{"whitespace only", []byte("\n"), true},
		{"short passphrase", []byte("hunter2"), true},
		{"one byte short", make([]byte, 15), true},
		{"minimum", make([]byte, 16), false},
		{"generated length", make([]byte, 32), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkSecret(tt.secret, dir)
			if (err != nil) != tt.wantErr {
				t.Fatalf("checkSecret(%d bytes) error = %v, wantErr %v", len(tt.secret), err, tt.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), filepath.Join(dir, "secret")) {
				t.Errorf("error %q does not name the secret file", err)
			}
		})
	}
}

// http.ListenAndServe leaves every timeout at zero, which lets one stalled
// connection be held open forever.
func TestHTTPServerTimeouts(t *testing.T) {
	srv := newHTTPServer("127.0.0.1:8080", http.NotFoundHandler())
	for _, tt := range []struct {
		name string
		got  time.Duration
		want time.Duration
	}{
		{"ReadHeaderTimeout", srv.ReadHeaderTimeout, 10 * time.Second},
		{"ReadTimeout", srv.ReadTimeout, 30 * time.Second},
		// Strictly longer than the AI proxy's upstream round trip (asserted
		// below), so the handler still has budget to write its answer.
		{"WriteTimeout", srv.WriteTimeout, 90 * time.Second},
		{"IdleTimeout", srv.IdleTimeout, 120 * time.Second},
	} {
		if tt.got != tt.want {
			t.Errorf("%s = %v, want %v", tt.name, tt.got, tt.want)
		}
	}
	// The write deadline is armed when the request headers are read, not when
	// the handler starts writing: with an equal budget the AI proxy spends it
	// all upstream and the 502 it produces on a timeout can never be sent.
	if srv.WriteTimeout <= server.AITimeout {
		t.Errorf("WriteTimeout = %v, want more than the AI upstream timeout (%v)",
			srv.WriteTimeout, server.AITimeout)
	}
	if srv.Addr != "127.0.0.1:8080" || srv.Handler == nil {
		t.Errorf("server addr/handler = %q/%v, want the arguments through", srv.Addr, srv.Handler)
	}
}
