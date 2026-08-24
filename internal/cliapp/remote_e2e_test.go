package cliapp

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/server"
	"github.com/RandomCodeSpace/kb/internal/store"
)

// remoteEnv spins up a real kb server (token mode) over httptest and points
// the CLI at it, so remote tests exercise the genuine CLI -> HTTP -> store
// path instead of a hand-written mock.
func remoteEnv(t *testing.T) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "kb.db"), []byte("test-secret"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	srv := httptest.NewServer(server.New(server.Config{Token: "sekrit"}, st))
	t.Cleanup(srv.Close)
	t.Setenv("KB_SERVER", srv.URL)
	t.Setenv("KB_SERVER_TOKEN", "sekrit")
	t.Setenv("KB_USER", "")
	t.Setenv("KB_PROJECT", inboxProject)
}

func TestRemoteLifecycleEndToEnd(t *testing.T) {
	remoteEnv(t)

	// add: stable numbers from the very first task.
	out, errS, code := runCmd(t, "add", "Alpha", "--tag", "a")
	if code != 0 {
		t.Fatalf("remote add failed (code %d): %s", code, errS)
	}
	if out != "added #1 Alpha\n" {
		t.Fatalf("remote add output = %q", out)
	}
	if _, _, code = runCmd(t, "add", "Beta", "--check", "step"); code != 0 {
		t.Fatal("second add failed")
	}

	// list: same shapes as local mode, seq included.
	tasks := listJSON(t)
	if len(tasks) != 2 || tasks[0].Seq != 1 || tasks[1].Seq != 2 || tasks[0].ID == "" {
		t.Fatalf("remote list = %+v", tasks)
	}

	// update by stable number; done guard fires server-side (409 -> error).
	if out, _, code = runCmd(t, "update", "2", "--prio", "1"); code != 0 || out != "updated #2 Beta\n" {
		t.Fatalf("remote update: code=%d out=%q", code, out)
	}
	if _, errS, code = runCmd(t, "done", "2"); code != 1 || !strings.Contains(errS, "checklist items are still open") {
		t.Fatalf("remote gated done: code=%d stderr=%q", code, errS)
	}
	if out, _, code = runCmd(t, "done", "2", "--force"); code != 0 || out != "moved #2 -> done\n" {
		t.Fatalf("remote forced done: code=%d out=%q", code, out)
	}

	// comments work remotely, author is the acting identity.
	if out, _, code = runCmd(t, "comment", "add", "1", "remote note"); code != 0 || out != "commented c1 on #1\n" {
		t.Fatalf("remote comment add: code=%d out=%q", code, out)
	}
	out, _, code = runCmd(t, "comment", "list", "1")
	if code != 0 || !strings.Contains(out, "default") || !strings.Contains(out, "remote note") {
		t.Fatalf("remote comment list:\n%s", out)
	}

	// links and the blocker gate, remotely.
	if out, _, code = runCmd(t, "link", "1", "blocks", "2"); code != 0 || out != "linked: #1 blocks #2\n" {
		t.Fatalf("remote link: code=%d out=%q", code, out)
	}
	if _, errS, code = runCmd(t, "restore", "2"); code != 0 {
		t.Fatalf("restore failed: %s", errS)
	}
	if _, errS, code = runCmd(t, "done", "2"); code != 1 || !strings.Contains(errS, "open blocker") {
		t.Fatalf("remote blocker gate: code=%d stderr=%q", code, errS)
	}
	if _, _, code = runCmd(t, "unlink", "2", "1"); code != 0 {
		t.Fatal("remote unlink failed")
	}

	// view shows the full task with comments.
	out, _, code = runCmd(t, "view", "1")
	if code != 0 || !strings.Contains(out, "#1 Alpha") || !strings.Contains(out, "remote note") {
		t.Fatalf("remote view:\n%s", out)
	}

	// comment rm and task rm round-trip.
	if out, _, code = runCmd(t, "comment", "rm", "c1", "--yes"); code != 0 || out != "deleted c1\n" {
		t.Fatalf("remote comment rm: code=%d out=%q", code, out)
	}
	if out, _, code = runCmd(t, "rm", "2", "--yes"); code != 0 || out != "deleted #2 Beta\n" {
		t.Fatalf("remote rm: code=%d out=%q", code, out)
	}

	// The retired i-N indexes point at their replacement.
	if _, errS, code = runCmd(t, "done", "i1"); code != 1 || !strings.Contains(errS, "stable number") {
		t.Fatalf("i-N ref: code=%d stderr=%q", code, errS)
	}

	// users remains local-only.
	if _, errS, code = runCmd(t, "users"); code != 1 || !strings.Contains(errS, "KB_SERVER") {
		t.Fatalf("remote users: code=%d stderr=%q", code, errS)
	}

	// Errors surface with the server's message: unknown task is 404.
	if _, errS, code = runCmd(t, "view", "9"); code != 1 || !strings.Contains(errS, "task not found") {
		t.Fatalf("remote missing task: code=%d stderr=%q", code, errS)
	}
}

func TestRemoteValidatesFieldsEndToEnd(t *testing.T) {
	remoteEnv(t)
	if _, _, code := runCmd(t, "add", "Valid"); code != 0 {
		t.Fatal("seed add failed")
	}
	// The server refuses invalid fields with a 400 the CLI reports.
	title := "Multi\nline"
	if _, errS, code := runCmd(t, "update", "1", "--title", title); code != 1 || errS == "" {
		t.Fatalf("remote invalid title: code=%d stderr=%q", code, errS)
	}
	// Wrong token is a 401.
	t.Setenv("KB_SERVER_TOKEN", "wrong")
	if _, errS, code := runCmd(t, "list"); code != 1 || !strings.Contains(errS, "401") {
		t.Fatalf("bad token: code=%d stderr=%q", code, errS)
	}
}
