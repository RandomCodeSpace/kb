package cliapp

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/board"
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

func decodeExactJSONList(t *testing.T, stdout string) []taskJSON {
	t.Helper()
	var tasks []taskJSON
	if err := json.Unmarshal([]byte(stdout), &tasks); err != nil {
		t.Fatalf("decode JSON list %q: %v", stdout, err)
	}
	want, err := json.MarshalIndent(tasks, "", "  ")
	if err != nil {
		t.Fatalf("encode expected JSON list: %v", err)
	}
	want = append(want, '\n')
	if stdout != string(want) {
		t.Fatalf("JSON list output = %q, want exact %q", stdout, want)
	}
	return tasks
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
	if !reflect.DeepEqual(after, before) {
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

	before := readLocalSnapshot(t, dir)
	stdout, stderr, code := runCmd(t, "list", "--json", "--all", "--status", "doing", "--data", dir)
	if code != 0 || stderr != "" {
		t.Fatalf("combined list filters: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	tasks := decodeExactJSONList(t, stdout)
	if len(tasks) != 1 || tasks[0].Title != "Doing" {
		t.Fatalf("combined filters returned %+v, want only Doing", tasks)
	}
	if after := readLocalSnapshot(t, dir); !reflect.DeepEqual(after, before) {
		t.Fatalf("combined list filters mutated board: before=%+v after=%+v", before, after)
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

	before := readLocalSnapshot(t, dir)
	stdout, stderr, code := runCmd(t, "list", "--json", "--all", "--data", dir)
	if code != 0 || stderr != "" {
		t.Fatalf("ordered list: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	tasks := decodeExactJSONList(t, stdout)
	got := make([]string, 0, len(tasks))
	for _, task := range tasks {
		got = append(got, task.Title)
	}
	want := []string{"Todo first", "Todo second", "Doing first", "Cancelled first"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("list order = %v, want %v", got, want)
	}
	if after := readLocalSnapshot(t, dir); !reflect.DeepEqual(after, before) {
		t.Fatalf("ordered list mutated board: before=%+v after=%+v", before, after)
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
	if code != 0 || stderr != "" || stdout != "updated #1 Clear me\n" {
		t.Fatalf("clear update: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	after := readLocalSnapshot(t, dir)
	want := before
	want.Board.Tasks = append([]board.Task(nil), before.Board.Tasks...)
	want.Board.Tasks[0].Desc = ""
	want.Board.Tasks[0].Due = ""
	want.Board.Tasks[0].Effort = ""
	want.Board.Tasks[0].Emoji = ""
	want.Revision++
	if !reflect.DeepEqual(after, want) {
		t.Fatalf("cleared snapshot = %+v, want %+v", after, want)
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
	wantError := "kb: 1 of 1 checklist items are still open on #1 \"Should not persist\"; re-run with --force to finish it anyway\n"
	if code != 1 || stdout != "" || stderr != wantError {
		t.Fatalf("guarded update: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	after := readLocalSnapshot(t, dir)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("refused update mutated board: before=%+v after=%+v", before, after)
	}
}
