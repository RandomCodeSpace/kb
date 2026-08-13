package cliapp

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
)

func TestRunEmptyAndHelpAliases(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run(nil, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "usage: kb") {
		t.Fatalf("empty invocation: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	for _, args := range [][]string{
		{"-h"}, {"--help"},
		{"add", "--help"}, {"list", "--help"}, {"update", "--help"},
		{"move", "--help"}, {"done", "--help"}, {"cancel", "--help"},
		{"restore", "--help"}, {"rm", "--help"},
	} {
		stdout.Reset()
		stderr.Reset()
		if code := Run(args, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "usage: kb") {
			t.Errorf("kb %s: code=%d stdout=%q stderr=%q", strings.Join(args, " "), code, stdout.String(), stderr.String())
		}
	}
}

func TestUpdateValidationBranches(t *testing.T) {
	dir := localEnv(t)
	cases := [][]string{
		{"update", "--desc", "x", "--data", dir},
		{"update", "id", "--bogus", "x", "--data", dir},
		{"update", "id", "--prio", "0", "--data", dir},
		{"update", "id", "--due", "tomorrow", "--data", dir},
		{"update", "id", "--due", "2026-02-30", "--data", dir},
		{"update", "id", "--effort", "huge", "--data", dir},
		{"update", "id", "--blocked", "--no-blocked", "--data", dir},
		{"update", "id", "--status", "waiting", "--data", dir},
		{"done", "a", "b", "--data", dir},
		{"rm", "--data", dir},
	}
	for _, args := range cases {
		if _, stderr, code := runCmd(t, args...); code != 2 {
			t.Errorf("kb %s: code=%d stderr=%q, want usage error", strings.Join(args, " "), code, stderr)
		}
	}
}

func TestBackendSelectionDefaultsAndOpenFailures(t *testing.T) {
	t.Setenv("KB_SERVER", "")
	t.Setenv("KB_DATA", "/tmp/kb-explicit-data")
	if got, err := defaultDataDir(); err != nil || got != "/tmp/kb-explicit-data" {
		t.Fatalf("KB_DATA default: got=%q err=%v", got, err)
	}
	t.Setenv("KB_DATA", "")
	t.Setenv("HOME", "/tmp/kb-home")
	if got, err := defaultDataDir(); err != nil || got != "/tmp/kb-home/.local/share/kb" {
		t.Fatalf("HOME default: got=%q err=%v", got, err)
	}
	t.Setenv("HOME", "")
	if _, err := defaultDataDir(); err == nil || !strings.Contains(err.Error(), "cannot determine home") {
		t.Fatalf("missing HOME: err=%v", err)
	}

	var stderr bytes.Buffer
	if be, err := openBackend("  ", t.TempDir(), &stderr); err != nil {
		t.Fatalf("blank user should normalize to default: %v", err)
	} else if err := be.close(); err != nil {
		t.Fatal(err)
	}
	if _, err := openBackend("bad/user", t.TempDir(), &stderr); err == nil {
		t.Fatal("invalid user unexpectedly opened a backend")
	}

	notDir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notDir, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := openLocal("default", notDir, &stderr); err == nil || !strings.Contains(err.Error(), "create data dir") {
		t.Fatalf("file data path: err=%v", err)
	}
	if _, stderr, code := runCmd(t, "list", "--user", "bad/user", "--data", t.TempDir()); code != 1 || !strings.Contains(stderr, "user identity") {
		t.Fatalf("invalid user command: code=%d stderr=%q", code, stderr)
	}
}

func TestLocalBackendPropagatesClosedStoreErrors(t *testing.T) {
	be, err := openLocal("default", t.TempDir(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	local := be.(*localBackend)
	if err := local.close(); err != nil {
		t.Fatal(err)
	}
	if _, err := local.list(store.TaskFilter{}); err == nil {
		t.Error("list on closed store succeeded")
	}
	if _, err := local.add(board.Task{Title: "closed"}); err == nil {
		t.Error("add on closed store succeeded")
	}
	if _, err := local.remove("missing"); err == nil {
		t.Error("remove on closed store succeeded")
	}
}

func TestFindItemResolutionBranches(t *testing.T) {
	items := []item{
		{ref: "i2", task: board.Task{Title: "remote"}},
		{ref: "abcdef-1", task: board.Task{Title: "one"}},
		{ref: "abcdef-2", task: board.Task{Title: "two"}},
		{ref: "unique-3", task: board.Task{Title: "three"}},
	}
	for _, tc := range []struct {
		ref, title string
	}{
		{"i2", "remote"},
		{"2", "remote"},
		{"unique", "three"},
	} {
		got, err := findItem(items, tc.ref)
		if err != nil || got.task.Title != tc.title {
			t.Errorf("findItem(%q): got=%+v err=%v", tc.ref, got, err)
		}
	}
	if _, err := findItem(items, "absent"); err == nil || !strings.Contains(err.Error(), "no task") {
		t.Fatalf("missing ref: %v", err)
	}
	if _, err := findItem(items, "abcdef"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous ref: %v", err)
	}
}

type coverageFailWriter struct{ err error }

func (w coverageFailWriter) Write([]byte) (int, error) { return 0, w.err }

func TestJSONOutputPropagatesWriterFailure(t *testing.T) {
	want := errors.New("output unavailable")
	err := writeJSON(coverageFailWriter{err: want}, []item{{
		ref:  "i1",
		task: board.Task{Title: "one", Status: board.StatusTodo, Prio: 3},
	}})
	if !errors.Is(err, want) {
		t.Fatalf("writeJSON err=%v, want %v", err, want)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[{"id":"a","seq":1,"title":"One","status":"todo","prio":3}]`)
	}))
	defer srv.Close()
	t.Setenv("KB_SERVER", srv.URL)
	t.Setenv("KB_SERVER_TOKEN", "")
	var stderr bytes.Buffer
	if code := Run([]string{"list", "--json"}, coverageFailWriter{err: want}, &stderr); code != 1 || !strings.Contains(stderr.String(), want.Error()) {
		t.Fatalf("list writer failure: code=%d stderr=%q", code, stderr.String())
	}
}

type coverageRoundTripFunc func(*http.Request) (*http.Response, error)

func (f coverageRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type coverageErrorBody struct{ err error }

func (b coverageErrorBody) Read([]byte) (int, error) { return 0, b.err }
func (coverageErrorBody) Close() error               { return nil }

func response(status int, body io.ReadCloser, etag string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     http.Header{"Etag": []string{etag}},
		Body:       body,
	}
}

func TestRemoteTransportRequestAndReadFailures(t *testing.T) {
	transportErr := errors.New("transport down")
	broken := &remoteBackend{
		base: "http://example.test",
		user: "default",
		client: &http.Client{Transport: coverageRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, transportErr
		})},
	}
	if _, err := broken.list(store.TaskFilter{}); !errors.Is(err, transportErr) {
		t.Fatalf("list transport err=%v", err)
	}
	if _, err := broken.add(board.Task{Title: "x"}); !errors.Is(err, transportErr) {
		t.Fatalf("add transport err=%v", err)
	}
	if _, err := broken.update("1", store.TaskPatch{}, nil, false); !errors.Is(err, transportErr) {
		t.Fatalf("update transport err=%v", err)
	}
	if _, err := broken.remove("1"); !errors.Is(err, transportErr) {
		t.Fatalf("remove transport err=%v", err)
	}

	readErr := errors.New("body broke")
	readBroken := &remoteBackend{
		base: "http://example.test",
		user: "default",
		client: &http.Client{Transport: coverageRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return response(http.StatusOK, coverageErrorBody{err: readErr}, `"r1"`), nil
		})},
	}
	if _, err := readBroken.list(store.TaskFilter{}); !errors.Is(err, readErr) {
		t.Fatalf("read body err=%v", err)
	}

	malformed := &remoteBackend{base: "://bad", user: "default", client: http.DefaultClient}
	if _, err := malformed.list(store.TaskFilter{}); err == nil {
		t.Fatal("malformed GET base unexpectedly succeeded")
	}

	blankError := &remoteBackend{
		base: "http://example.test",
		user: "default",
		client: &http.Client{Transport: coverageRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return response(http.StatusServiceUnavailable, io.NopCloser(strings.NewReader("")), ""), nil
		})},
	}
	if _, err := blankError.list(store.TaskFilter{}); err == nil || !strings.Contains(err.Error(), "Service Unavailable") {
		t.Fatalf("blank HTTP error=%v", err)
	}

	// The retired i-N indexes fail fast, before any request is made.
	if _, err := broken.update("i2", store.TaskPatch{}, nil, false); err == nil || !strings.Contains(err.Error(), "stable number") {
		t.Fatalf("i-N ref = %v, want the retirement error", err)
	}
}
