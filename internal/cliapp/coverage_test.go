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
	if _, err := local.list(""); err == nil {
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

	ws := &wireServer{doc: "# B\n\n## To Do\n\n- [ ] One\n", has: true}
	srv := httptest.NewServer(ws.handler())
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

func TestRemoteStatusFilteringAndHTTPFailures(t *testing.T) {
	doc := "# B\n\n## To Do\n\n- [ ] One\n\n## Doing\n\n- [ ] Two\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"r1"`)
		if r.Method == http.MethodPut {
			http.Error(w, "write rejected", http.StatusConflict)
			return
		}
		_, _ = io.WriteString(w, doc)
	}))
	defer srv.Close()
	r := newRemote(srv.URL, "", "default").(*remoteBackend)
	filtered, err := r.list(board.StatusDoing)
	if err != nil || len(filtered) != 1 || filtered[0].task.Title != "Two" {
		t.Fatalf("filtered list=%+v err=%v", filtered, err)
	}
	if _, err := r.list(board.StatusDone); err != nil {
		t.Fatalf("empty filtered list: %v", err)
	}
	if _, err := r.add(board.Task{Title: "Three"}); err == nil || !strings.Contains(err.Error(), "write rejected") {
		t.Fatalf("add PUT failure: %v", err)
	}
	title := "Renamed"
	if _, err := r.update("i1", store.TaskPatch{Title: &title}, nil, false); err == nil || !strings.Contains(err.Error(), "write rejected") {
		t.Fatalf("update PUT failure: %v", err)
	}
	if _, err := r.remove("i1"); err == nil || !strings.Contains(err.Error(), "write rejected") {
		t.Fatalf("remove PUT failure: %v", err)
	}
	if _, err := r.remove("i9"); err == nil || !strings.Contains(err.Error(), "no task") {
		t.Fatalf("remove bad ref: %v", err)
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
	if _, err := broken.list(""); !errors.Is(err, transportErr) {
		t.Fatalf("list transport err=%v", err)
	}
	if _, err := broken.add(board.Task{Title: "x"}); !errors.Is(err, transportErr) {
		t.Fatalf("add transport err=%v", err)
	}
	if _, err := broken.update("i1", store.TaskPatch{}, nil, false); !errors.Is(err, transportErr) {
		t.Fatalf("update transport err=%v", err)
	}
	if _, err := broken.remove("i1"); !errors.Is(err, transportErr) {
		t.Fatalf("remove transport err=%v", err)
	}
	if err := broken.putBoard(board.Board{Title: "B"}, `"r1"`); !errors.Is(err, transportErr) {
		t.Fatalf("PUT transport err=%v", err)
	}

	readErr := errors.New("body broke")
	readBroken := &remoteBackend{
		base: "http://example.test",
		user: "default",
		client: &http.Client{Transport: coverageRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return response(http.StatusOK, coverageErrorBody{err: readErr}, `"r1"`), nil
		})},
	}
	if _, err := readBroken.fetchBoard(); !errors.Is(err, readErr) {
		t.Fatalf("read body err=%v", err)
	}

	malformed := &remoteBackend{base: "://bad", user: "default", client: http.DefaultClient}
	if _, err := malformed.fetchBoard(); err == nil {
		t.Fatal("malformed GET base unexpectedly succeeded")
	}
	if err := malformed.putBoard(board.Board{Title: "B"}, `"r1"`); err == nil {
		t.Fatal("malformed PUT base unexpectedly succeeded")
	}

	blankError := &remoteBackend{
		base: "http://example.test",
		user: "default",
		client: &http.Client{Transport: coverageRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return response(http.StatusServiceUnavailable, io.NopCloser(strings.NewReader("")), ""), nil
		})},
	}
	if _, err := blankError.fetchBoard(); err == nil || !strings.Contains(err.Error(), "Service Unavailable") {
		t.Fatalf("blank HTTP error=%v", err)
	}
}

func TestApplyPatchAllFields(t *testing.T) {
	emoji, title, desc := "x", "new", "description"
	due, effort, blocked, prio := "2026-08-01", "L", true, 1
	tags := []string{"coverage"}
	checks := []board.Check{{Text: "tested", Done: true}}
	task := board.Task{}
	applyPatch(&task, store.TaskPatch{
		Emoji: &emoji, Title: &title, Desc: &desc, Due: &due, Effort: &effort,
		Blocked: &blocked, Prio: &prio, Tags: &tags, Checks: &checks,
	})
	if task.Emoji != emoji || task.Title != title || task.Desc != desc || task.Due != due ||
		task.Effort != effort || !task.Blocked || task.Prio != prio || len(task.Tags) != 1 ||
		len(task.Checks) != 1 || !task.Checks[0].Done {
		t.Fatalf("patched task=%+v", task)
	}
}
