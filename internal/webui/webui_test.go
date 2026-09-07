package webui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"testing/fstest"
	"time"
)

func TestStaticServing(t *testing.T) {
	h, _ := newTestHandler(t)
	index, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		t.Fatalf("index.html must be embedded: %v", err)
	}
	for _, path := range []string{"/", "/index.html", "/board/12", "/missing.css"} {
		rec := call(t, h, "GET", path, nil)
		if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), index) {
			t.Fatalf("%s: status %d, body matches index: %v", path, rec.Code, bytes.Equal(rec.Body.Bytes(), index))
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Fatalf("%s: Content-Type %q", path, ct)
		}
		if rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("Cache-Control") != "" {
			t.Fatalf("%s: headers %v", path, rec.Header())
		}
	}
	// Real files are served by extension; the exact set depends on the UI,
	// so only files that are present are checked.
	entries, _ := fs.ReadDir(staticFS, "static")
	for _, e := range entries {
		name := e.Name()
		if name == "index.html" || strings.HasPrefix(name, ".") || e.IsDir() {
			continue
		}
		rec := call(t, h, "GET", "/"+name, nil)
		want, _ := staticFS.ReadFile("static/" + name)
		if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), want) {
			t.Fatalf("/%s: status %d", name, rec.Code)
		}
		if rec.Header().Get("Content-Type") == "" {
			t.Fatalf("/%s: no Content-Type", name)
		}
	}
	// /api never falls back to the UI.
	rec := call(t, h, "GET", "/api/", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("/api/: %d", rec.Code)
	}
}

func TestStaticHandlerEdgeCases(t *testing.T) {
	files := fstest.MapFS{
		"index.html":   {Data: []byte("<html>x</html>")},
		"dir/file.txt": {Data: []byte("f")},
	}
	h := &staticHandler{files: files, index: []byte("<html>x</html>")}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/dir", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "<html>x</html>" {
		t.Fatalf("directory path: %d %q", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/dir/file.txt", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "f" {
		t.Fatalf("nested file: %d %q", rec.Code, rec.Body.String())
	}
	missing := &staticHandler{files: fstest.MapFS{}}
	rec = httptest.NewRecorder()
	missing.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("no index: %d", rec.Code)
	}
}

func TestListen(t *testing.T) {
	ln, err := Listen("", false)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if host, _, _ := net.SplitHostPort(ln.Addr().String()); host != "127.0.0.1" {
		t.Fatalf("default addr = %s", ln.Addr())
	}
	local, err := Listen("localhost:0", false)
	if err != nil {
		t.Fatal(err)
	}
	local.Close()

	if _, err := Listen("0.0.0.0:0", false); err == nil || !strings.Contains(err.Error(), "non-loopback") {
		t.Fatalf("wildcard accepted: %v", err)
	}
	if _, err := Listen(":0", false); err == nil || !strings.Contains(err.Error(), "non-loopback") {
		t.Fatalf("empty host accepted: %v", err)
	}
	if _, err := Listen("127.0.0.1", false); err == nil || !strings.Contains(err.Error(), "invalid address") {
		t.Fatalf("missing port accepted: %v", err)
	}
	remote, err := Listen("0.0.0.0:0", true)
	if err != nil {
		t.Fatalf("allowRemote refused: %v", err)
	}
	remote.Close()
	if _, err := Listen(ln.Addr().String(), false); err == nil || !strings.Contains(err.Error(), "listen") {
		t.Fatalf("busy port accepted: %v", err)
	}
}

func TestBrowserCommand(t *testing.T) {
	cases := map[string]string{"darwin": "open", "windows": "rundll32", "linux": "xdg-open"}
	for goos, want := range cases {
		name, args := browserCommand(goos, "http://x")
		if name != want || args[len(args)-1] != "http://x" {
			t.Fatalf("%s: %s %v", goos, name, args)
		}
	}
	t.Setenv("PATH", t.TempDir())
	if err := launchBrowser("http://127.0.0.1:1"); err == nil {
		t.Fatal("launcher succeeded with no opener on PATH")
	}
}

func TestServeListenerError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln.Close()
	if err := serve(context.Background(), &http.Server{}, ln); err == nil {
		t.Fatal("closed listener served")
	}
}

// waitForURL blocks until stdout carries the serving line and returns the
// URL from it.
func waitForURL(t *testing.T, stdout *syncBuffer) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if line := stdout.String(); strings.HasPrefix(line, "kb web: serving http://") {
			return strings.TrimSpace(strings.TrimPrefix(line, "kb web: serving "))
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no serving line, stdout = %q", stdout.String())
	return ""
}

func TestRunServesUntilCancelled(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KB_PROJECT", "work")
	opened := make(chan string, 1)
	launchBrowser = func(url string) error {
		opened <- url
		return errors.New("no browser")
	}
	t.Cleanup(func() {
		launchBrowser = func(url string) error { return nil }
	})
	stdout, stderr := &syncBuffer{}, &syncBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, Options{DataDir: dir, Version: "v1", OpenBrowser: true}, stdout, stderr)
	}()
	url := waitForURL(t, stdout)
	if got := <-opened; got != url {
		t.Fatalf("browser opened %q, served %q", got, url)
	}
	resp, err := http.Get(url + "/api/meta")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"version":"v1"`) {
		t.Fatalf("meta over the wire: %d %s", resp.StatusCode, body)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not stop after cancel")
	}
	if !strings.Contains(stderr.String(), "open browser: no browser") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if lines := strings.Count(stdout.String(), "\n"); lines != 1 {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunErrors(t *testing.T) {
	t.Setenv("KB_PROJECT", "work")
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), Options{DataDir: blocked}, io.Discard, io.Discard); err == nil {
		t.Fatal("store opened inside a regular file")
	}
	if err := run(context.Background(), Options{DataDir: t.TempDir(), Addr: "0.0.0.0:0"}, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "non-loopback") {
		t.Fatalf("remote addr: %v", err)
	}
}

func TestRunStopsOnSignal(t *testing.T) {
	t.Setenv("KB_PROJECT", "work")
	stdout := &syncBuffer{}
	done := make(chan error, 1)
	go func() {
		done <- Run(Options{DataDir: t.TempDir(), User: "default"}, stdout, io.Discard)
	}()
	waitForURL(t, stdout)
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not stop on SIGINT")
	}
}

// syncBuffer is a bytes.Buffer safe to read while the server goroutine
// writes to it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
