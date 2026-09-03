package cliapp

import (
	"bytes"
	"errors"
	"io"
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
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"--help"}, &stdout, &stderr); code != 0 ||
		!strings.Contains(stdout.String(), "--json is available on every data-producing") ||
		strings.Contains(stdout.String(), "every\n                 other command prints the affected task") {
		t.Fatalf("global JSON help: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
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

func TestLocalStorageDefaultsAndOpenFailures(t *testing.T) {
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
	if be, err := openLocal(defaultUser, t.TempDir(), &stderr); err != nil {
		t.Fatalf("open local store: %v", err)
	} else if err := be.close(); err != nil {
		t.Fatal(err)
	}

	notDir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notDir, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := openLocal(defaultUser, notDir, &stderr); err == nil || !strings.Contains(err.Error(), "create data dir") {
		t.Fatalf("file data path: err=%v", err)
	}
	if _, stderr, code := runCmd(t, "list", "--user", "alice", "--data", t.TempDir()); code != 2 || !strings.Contains(stderr, "flag provided but not defined: -user") {
		t.Fatalf("--user should be rejected: code=%d stderr=%q", code, stderr)
	}
}

func TestLocalBackendPropagatesClosedStoreErrors(t *testing.T) {
	local, err := openLocal(defaultUser, t.TempDir(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
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
		{ref: "abcdef-1", task: board.Task{Seq: 1, Title: "one"}},
		{ref: "abcdef-2", task: board.Task{Seq: 2, Title: "two"}},
		{ref: "unique-3", task: board.Task{Seq: 3, Title: "three"}},
	}
	for _, tc := range []struct {
		ref, title string
	}{
		{"abcdef-1", "one"},
		{"2", "two"},
		{"unique", "three"},
	} {
		got, err := findItem(items, tc.ref)
		if err != nil || got.task.Title != tc.title {
			t.Errorf("findItem(%q): got=%+v err=%v", tc.ref, got, err)
		}
	}
	if _, err := findItem(items, "9"); err == nil || !strings.Contains(err.Error(), "no task") {
		t.Fatalf("missing sequence: %v", err)
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
		ref:  "task-id",
		task: board.Task{Title: "one", Status: board.StatusTodo, Prio: 3},
	}})
	if !errors.Is(err, want) {
		t.Fatalf("writeJSON err=%v, want %v", err, want)
	}

	dir := localEnv(t)
	if _, stderr, code := runCmd(t, "add", "One", "--data", dir); code != 0 {
		t.Fatalf("seed task: code=%d stderr=%q", code, stderr)
	}
	var stderr bytes.Buffer
	if code := Run([]string{"list", "--json", "--data", dir}, coverageFailWriter{err: want}, &stderr); code != 1 || !strings.Contains(stderr.String(), want.Error()) {
		t.Fatalf("list writer failure: code=%d stderr=%q", code, stderr.String())
	}
}
