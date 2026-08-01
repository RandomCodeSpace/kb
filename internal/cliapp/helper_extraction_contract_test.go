package cliapp

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/store"
)

func readLocalSnapshot(t *testing.T, dir string) store.BoardSnapshot {
	t.Helper()
	secret, err := store.LoadOrCreateSecret(dir)
	if err != nil {
		t.Fatalf("load secret: %v", err)
	}
	st, err := store.Open(filepath.Join(dir, dbFile), secret)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	snapshot, err := st.ReadBoardSnapshot("default")
	if err != nil {
		_ = st.Close()
		t.Fatalf("read board snapshot: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	return snapshot
}

func TestAddRejectsConflictingBlockedFlagsWithoutMutation(t *testing.T) {
	dir := localEnv(t)
	if _, stderr, code := runCmd(t, "add", "Existing", "--data", dir); code != 0 {
		t.Fatalf("seed add: code=%d stderr=%q", code, stderr)
	}
	before := readLocalSnapshot(t, dir)

	stdout, stderr, code := runCmd(t, "add", "Rejected", "--blocked", "--no-blocked", "--data", dir)
	if code != 2 || stdout != "" || stderr != "kb: --blocked and --no-blocked cannot be combined\n" {
		t.Fatalf("conflicting flags: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	after := readLocalSnapshot(t, dir)
	if after.Revision != before.Revision || len(after.Board.Tasks) != 1 || after.Board.Tasks[0].Title != "Existing" {
		t.Fatalf("rejected add mutated board: before=%+v after=%+v", before, after)
	}
}

func TestListStatusFilterTakesPrecedenceOverAll(t *testing.T) {
	dir := localEnv(t)
	for _, args := range [][]string{
		{"add", "Todo", "--data", dir},
		{"add", "Doing", "--status", "doing", "--data", dir},
		{"add", "Cancelled", "--status", "cancelled", "--data", dir},
	} {
		if _, stderr, code := runCmd(t, args...); code != 0 {
			t.Fatalf("seed %q: code=%d stderr=%q", args[1], code, stderr)
		}
	}

	tasks := listJSON(t, "--all", "--status", "doing", "--data", dir)
	if len(tasks) != 1 || tasks[0].Title != "Doing" {
		t.Fatalf("combined filters returned %+v, want only Doing", tasks)
	}
}

func TestListAllPreservesColumnThenInsertionOrder(t *testing.T) {
	dir := localEnv(t)
	for _, args := range [][]string{
		{"add", "Doing first", "--status", "doing", "--data", dir},
		{"add", "Todo first", "--data", dir},
		{"add", "Cancelled first", "--status", "cancelled", "--data", dir},
		{"add", "Todo second", "--data", dir},
	} {
		if _, stderr, code := runCmd(t, args...); code != 0 {
			t.Fatalf("seed %q: code=%d stderr=%q", args[1], code, stderr)
		}
	}

	tasks := listJSON(t, "--all", "--data", dir)
	got := make([]string, 0, len(tasks))
	for _, task := range tasks {
		got = append(got, task.Title)
	}
	want := []string{"Todo first", "Todo second", "Doing first", "Cancelled first"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("list order = %v, want %v", got, want)
	}
}

func TestUpdateClearsScalarFieldsWithoutChangingIdentity(t *testing.T) {
	dir := localEnv(t)
	if _, stderr, code := runCmd(t, "add", "Clear me", "--desc", "details", "--due", "2026-08-15", "--effort", "L", "--emoji", "🚀", "--data", dir); code != 0 {
		t.Fatalf("seed add: code=%d stderr=%q", code, stderr)
	}
	before := readLocalSnapshot(t, dir)
	id := before.Board.Tasks[0].ID

	stdout, stderr, code := runCmd(t, "update", id[:8], "--desc", "", "--due", "", "--effort", "", "--emoji", "", "--data", dir)
	if code != 0 || stderr != "" || stdout != "updated "+id[:8]+" Clear me\n" {
		t.Fatalf("clear update: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	after := readLocalSnapshot(t, dir)
	got := after.Board.Tasks[0]
	if got.ID != id || got.Desc != "" || got.Due != "" || got.Effort != "" || got.Emoji != "" {
		t.Fatalf("cleared task = %+v, want same identity and empty scalar fields", got)
	}
	if after.Revision != before.Revision+1 {
		t.Fatalf("clear update revision = %d, want %d", after.Revision, before.Revision+1)
	}
}

func TestUpdateCompletionRefusalLeavesRevisionUnchanged(t *testing.T) {
	dir := localEnv(t)
	if _, stderr, code := runCmd(t, "add", "Guarded", "--check", "open item", "--data", dir); code != 0 {
		t.Fatalf("seed add: code=%d stderr=%q", code, stderr)
	}
	before := readLocalSnapshot(t, dir)
	id := before.Board.Tasks[0].ID

	stdout, stderr, code := runCmd(t, "update", id[:8], "--status", "done", "--title", "Should not persist", "--data", dir)
	if code != 1 || stdout != "" || !strings.Contains(stderr, "1 of 1 checklist items are still open") {
		t.Fatalf("guarded update: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	after := readLocalSnapshot(t, dir)
	got := after.Board.Tasks[0]
	if after.Revision != before.Revision || got.ID != id || got.Title != "Guarded" || got.Status != "todo" {
		t.Fatalf("refused update mutated board: before=%+v after=%+v", before, after)
	}
}
