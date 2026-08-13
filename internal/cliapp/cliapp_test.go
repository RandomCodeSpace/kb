package cliapp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
)

func TestLocalDoneGuardReevaluatesAfterConcurrentUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kb.db")
	a, err := store.Open(path, []byte("test-secret"))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := store.Open(path, []byte("test-secret"))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	task, err := a.AddTask("u", board.Task{Title: "Race"})
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	resume := make(chan struct{})
	var once sync.Once
	backend := &localBackend{st: a, user: "u", beforeDoneGuard: func() {
		once.Do(func() {
			close(entered)
			<-resume
		})
	}}
	result := make(chan error, 1)
	go func() {
		_, err := backend.move(task.ID, board.StatusDone, false)
		result <- err
	}()
	<-entered
	blocked := true
	if _, err := b.UpdateTask("u", task.ID, store.TaskPatch{Blocked: &blocked}); err != nil {
		t.Fatal(err)
	}
	close(resume)
	if err := <-result; err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("guarded move err=%v, want blocked refusal", err)
	}
	stored, err := a.ListTasks("u", "")
	if err != nil || len(stored) != 1 || stored[0].Status != board.StatusTodo || !stored[0].Blocked {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
}

// runCmd invokes Run capturing both streams.
func runCmd(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errb bytes.Buffer
	code = Run(args, &out, &errb)
	return out.String(), errb.String(), code
}

// localEnv forces local mode into a fresh temp data dir.
func localEnv(t *testing.T) string {
	t.Helper()
	t.Setenv("KB_SERVER", "")
	t.Setenv("KB_SERVER_TOKEN", "")
	t.Setenv("KB_USER", "")
	t.Setenv("KB_SECRET", "test-secret")
	return t.TempDir()
}

// jsonTask mirrors the CLI's --json output shape.
type jsonTask struct {
	ID      string   `json:"id"`
	Seq     int      `json:"seq"`
	Emoji   string   `json:"emoji"`
	Title   string   `json:"title"`
	Desc    string   `json:"desc"`
	Status  string   `json:"status"`
	Blocked bool     `json:"blocked"`
	Prio    int      `json:"prio"`
	Due     string   `json:"due"`
	Effort  string   `json:"effort"`
	Tags    []string `json:"tags"`
	Checks  []struct {
		Text string `json:"text"`
		Done bool   `json:"done"`
	} `json:"checks"`
	Position int `json:"position"`
}

// listJSON runs `kb list --json` and decodes the result.
func listJSON(t *testing.T, extra ...string) []jsonTask {
	t.Helper()
	args := append([]string{"list", "--json"}, extra...)
	out, errS, code := runCmd(t, args...)
	if code != 0 {
		t.Fatalf("list --json failed (code %d): %s", code, errS)
	}
	var tasks []jsonTask
	if err := json.Unmarshal([]byte(out), &tasks); err != nil {
		t.Fatalf("list --json output not valid JSON: %v\n%s", err, out)
	}
	return tasks
}

var addedRe = regexp.MustCompile(`^added #1 Write the docs\n$`)

func TestLocalLifecycle(t *testing.T) {
	dir := localEnv(t)

	out, errS, code := runCmd(t, "add", "Write the docs", "--data", dir,
		"--desc", "Cover the CLI", "--prio", "2", "--due", "2026-08-01",
		"--effort", "m", "--tag", "docs", "--tag", "cli", "--check", "outline", "--emoji", "\U0001F4DA")
	if code != 0 {
		t.Fatalf("add failed (code %d): %s", code, errS)
	}
	if !addedRe.MatchString(out) {
		t.Fatalf("add output = %q, want match of %s", out, addedRe)
	}

	if _, errS, code = runCmd(t, "add", "Fix login bug", "--data", dir, "--status", "doing", "--tag", "bug"); code != 0 {
		t.Fatalf("second add failed (code %d): %s", code, errS)
	}

	tasks := listJSON(t, "--data", dir)
	if len(tasks) != 2 {
		t.Fatalf("got %d tasks, want 2: %+v", len(tasks), tasks)
	}
	t1, t2 := tasks[0], tasks[1] // listing order: todo before doing
	if t1.Title != "Write the docs" || t1.Status != "todo" || t1.Prio != 2 ||
		t1.Due != "2026-08-01" || t1.Effort != "M" || t1.Emoji != "\U0001F4DA" ||
		t1.Desc != "Cover the CLI" {
		t.Errorf("task 1 fields wrong: %+v", t1)
	}
	if strings.Join(t1.Tags, ",") != "docs,cli" || len(t1.Checks) != 1 || t1.Checks[0].Text != "outline" || t1.Checks[0].Done {
		t.Errorf("task 1 tags/checks wrong: %+v", t1)
	}
	if t2.Title != "Fix login bug" || t2.Status != "doing" || t2.Prio != 3 {
		t.Errorf("task 2 fields wrong: %+v", t2)
	}

	// Golden table (ids substituted; widths from tabwriter minwidth 2,
	// tabwidth 4, padding 2).
	out, _, code = runCmd(t, "list", "--data", dir)
	if code != 0 {
		t.Fatalf("list failed: code %d", code)
	}
	want := "ID  STATUS  PRIO  BLOCKED  TITLE           TAGS\n" +
		"#1  todo    2     -        Write the docs  docs,cli\n" +
		"#2  doing   3     -        Fix login bug   bug\n"
	if out != want {
		t.Errorf("list table:\n%q\nwant:\n%q", out, want)
	}

	// Patch by unique id prefix: only provided flags change.
	out, errS, code = runCmd(t, "update", t1.ID[:8], "--data", dir, "--title", "Write the docs v2", "--prio", "1")
	if code != 0 {
		t.Fatalf("update failed (code %d): %s", code, errS)
	}
	if want := "updated #1 Write the docs v2\n"; out != want {
		t.Errorf("update output = %q, want %q", out, want)
	}
	got := listJSON(t, "--data", dir)[0]
	if got.Title != "Write the docs v2" || got.Prio != 1 || got.Desc != "Cover the CLI" || got.Due != "2026-08-01" {
		t.Errorf("patch semantics broken: %+v", got)
	}

	// update --status moves the task.
	if _, errS, code = runCmd(t, "update", t1.ID[:8], "--data", dir, "--status", "doing"); code != 0 {
		t.Fatalf("update --status failed (code %d): %s", code, errS)
	}
	if n := len(listJSON(t, "--data", dir, "--status", "doing")); n != 2 {
		t.Errorf("after update --status doing: %d doing tasks, want 2", n)
	}

	// move and done.
	out, errS, code = runCmd(t, "move", t2.ID[:8], "done", "--data", dir)
	if code != 0 {
		t.Fatalf("move failed (code %d): %s", code, errS)
	}
	if want := "moved #2 -> done\n"; out != want {
		t.Errorf("move output = %q, want %q", out, want)
	}
	// t1 still has an open checklist item, so done needs --force (see
	// TestDoneRefusesOpenWork). Addressing by stable number works too.
	if out, _, code = runCmd(t, "done", "1", "--force", "--data", dir); code != 0 || out != "moved #1 -> done\n" {
		t.Errorf("done: code %d output %q", code, out)
	}
	if n := len(listJSON(t, "--data", dir, "--status", "done")); n != 2 {
		t.Errorf("got %d done tasks, want 2", n)
	}

	// rm refuses without --yes, deletes with it.
	_, errS, code = runCmd(t, "rm", t2.ID[:8], "--data", dir)
	if code != 1 || !strings.Contains(errS, "--yes") {
		t.Errorf("rm without --yes: code %d stderr %q, want code 1 mentioning --yes", code, errS)
	}
	if n := len(listJSON(t, "--data", dir)); n != 2 {
		t.Fatalf("rm without --yes deleted something: %d tasks left", n)
	}
	out, errS, code = runCmd(t, "rm", t2.ID[:8], "--yes", "--data", dir)
	if code != 0 {
		t.Fatalf("rm --yes failed (code %d): %s", code, errS)
	}
	if want := "deleted #2 Fix login bug\n"; out != want {
		t.Errorf("rm output = %q, want %q", out, want)
	}
	if n := len(listJSON(t, "--data", dir)); n != 1 {
		t.Errorf("after rm: %d tasks, want 1", n)
	}
}

func TestLocalPrefixErrors(t *testing.T) {
	dir := localEnv(t)
	if _, errS, code := runCmd(t, "add", "Solo", "--data", dir); code != 0 {
		t.Fatalf("add failed (code %d): %s", code, errS)
	}

	// UUIDs are hex, so "zz" can never match.
	_, errS, code := runCmd(t, "update", "zz", "--prio", "2", "--data", dir)
	if code != 1 || !strings.Contains(errS, "no task matches") {
		t.Errorf("unknown prefix: code %d stderr %q", code, errS)
	}

	// Digits-only refs mean sequence numbers now, so the ambiguity probe must
	// use a letter prefix (a-f). Keep adding tasks until two UUIDs share a
	// first letter; 6 buckets make this fast in practice.
	prefix := ""
	for i := 0; prefix == "" && i < 100; i++ {
		if _, errS, code := runCmd(t, "add", fmt.Sprintf("Task %d", i), "--data", dir); code != 0 {
			t.Fatalf("add %d failed (code %d): %s", i, code, errS)
		}
		seen := map[byte]bool{}
		for _, task := range listJSON(t, "--data", dir) {
			c := task.ID[0]
			if c >= '0' && c <= '9' {
				continue
			}
			if seen[c] {
				prefix = string(c)
				break
			}
			seen[c] = true
		}
	}
	if prefix == "" {
		t.Fatal("no shared first letter across 100 UUIDs (vanishingly unlikely)")
	}
	_, errS, code = runCmd(t, "move", prefix, "done", "--data", dir)
	if code != 1 || !strings.Contains(errS, "ambiguous") {
		t.Errorf("ambiguous prefix: code %d stderr %q", code, errS)
	}
}

func TestUsageErrors(t *testing.T) {
	dir := localEnv(t)
	cases := [][]string{
		{"add", "--data", dir},                                   // missing title
		{"add", "", "--data", dir},                               // empty title
		{"add", "x", "--prio", "9", "--data", dir},               // prio out of range
		{"add", "x", "--due", "8/1", "--data", dir},              // malformed due
		{"add", "x", "--due", "2026-13-99", "--data", dir},       // impossible due
		{"add", "x", "--status", "blocked", "--data", dir},       // bad status
		{"add", "x", "--effort", "XL", "--data", dir},            // bad effort
		{"add", "x", "--bogus", "y", "--data", dir},              // unknown flag
		{"update", "abc", "--data", dir},                         // no field flags
		{"update", "abc", "--title", "", "--data", dir},          // empty title patch
		{"move", "abc", "--data", dir},                           // missing status
		{"move", "abc", "nowhere", "--data", dir},                // bad status
		{"list", "extra", "--data", dir},                         // stray positional
		{"frobnicate"},                                           // unknown command
		{"add", "x", "--blocked", "--no-blocked", "--data", dir}, // contradictory blocked flags
		{"cancel", "--data", dir},                                // cancel without an id
		{"restore", "a", "b", "--data", dir},                     // restore with two ids
	}
	for _, args := range cases {
		if _, _, code := runCmd(t, args...); code != 2 {
			t.Errorf("kb %s: code %d, want 2", strings.Join(args, " "), code)
		}
	}

	out, _, code := runCmd(t, "help")
	if code != 0 || !strings.Contains(out, "usage: kb") {
		t.Errorf("help: code %d output %q", code, out)
	}
	for _, want := range []string{"cancel <id>", "restore <id>", "--blocked", "--no-blocked", "--force", "--all"} {
		if !strings.Contains(out, want) {
			t.Errorf("help does not document %q", want)
		}
	}
	// cancel and rm are not synonyms, and help must say so outright: one is
	// reversible, the other erases the task.
	for _, want := range []string{"soft delete", "hard delete", "no undo", "undo with", "not a synonym for rm"} {
		if !strings.Contains(out, want) {
			t.Errorf("help does not spell out the cancel/rm distinction: missing %q", want)
		}
	}
	// The done guard applies to both spellings of the transition.
	if !strings.Contains(out, "move <id> done") {
		t.Error("help does not say --force covers move <id> done as well as done <id>")
	}
	// The "x " marker is only discoverable if help states it outright, with
	// the space requirement that keeps "xml parser fails" intact.
	for _, want := range []string{`"x "`, `--check "x reproduce locally"`, "The space is required", "the list is replaced"} {
		if !strings.Contains(out, want) {
			t.Errorf("help does not document the checklist done marker: missing %q", want)
		}
	}
}

// TestDoneRefusesOpenWork covers F9/F10 on the CLI: finishing a task with
// unticked checklist items or a blocked flag is refused (kb never prompts)
// until --force says otherwise.
func TestDoneRefusesOpenWork(t *testing.T) {
	dir := localEnv(t)
	if _, errS, code := runCmd(t, "add", "Ship release", "--data", dir,
		"--check", "tag build", "--check", "write notes"); code != 0 {
		t.Fatalf("add failed (code %d): %s", code, errS)
	}
	id := listJSON(t, "--data", dir)[0].ID

	_, errS, code := runCmd(t, "done", id[:8], "--data", dir)
	if code != 1 {
		t.Fatalf("done with open checks: code %d, want 1", code)
	}
	if !strings.Contains(errS, "2 of 2 checklist items are still open") || !strings.Contains(errS, "--force") {
		t.Errorf("done stderr = %q, want the open-item count and --force", errS)
	}
	if got := listJSON(t, "--data", dir)[0].Status; got != "todo" {
		t.Errorf("refused done still moved the task: status %q", got)
	}

	// move <id> done is the same transition and refuses identically.
	if _, _, code = runCmd(t, "move", id[:8], "done", "--data", dir); code != 1 {
		t.Errorf("move ... done with open checks: code %d, want 1", code)
	}

	// Ticking everything off clears the warning without --force.
	if _, errS, code = runCmd(t, "update", id[:8], "--data", dir, "--check", "tag build"); code != 0 {
		t.Fatalf("update --check failed (code %d): %s", code, errS)
	}
	if _, errS, code = runCmd(t, "update", id[:8], "--data", dir, "--blocked"); code != 0 {
		t.Fatalf("update --blocked failed (code %d): %s", code, errS)
	}
	// Still blocked, so still refused — and the message says so.
	_, errS, code = runCmd(t, "done", id[:8], "--data", dir)
	if code != 1 || !strings.Contains(errS, "flagged blocked") {
		t.Fatalf("done while blocked: code %d stderr %q", code, errS)
	}
	if _, errS, code = runCmd(t, "update", id[:8], "--data", dir, "--no-blocked"); code != 0 {
		t.Fatalf("update --no-blocked failed (code %d): %s", code, errS)
	}
	// One open item remains ("tag build" replaced the list, unticked), so
	// --force is the way through.
	if _, errS, code = runCmd(t, "done", id[:8], "--force", "--data", dir); code != 0 {
		t.Fatalf("done --force failed (code %d): %s", code, errS)
	}
	if got := listJSON(t, "--data", dir)[0].Status; got != "done" {
		t.Errorf("after done --force: status %q, want done", got)
	}
}

// TestCheckDonePrefix covers the "x " marker on --check: the CLI can tick a
// checklist item, using the same convention the card modal's checklist box
// documents, so --force is no longer the only way to finish work from the
// terminal.
func TestCheckDonePrefix(t *testing.T) {
	dir := localEnv(t)
	if _, errS, code := runCmd(t, "add", "Ship release", "--data", dir,
		"--check", "x reproduce locally",
		"--check", "write failing test",
		"--check", "xml parser fails",
		"--check", `\x ray the build`,
		"--check", "X SHOUTED MARKER"); code != 0 {
		t.Fatalf("add with checks failed (code %d): %s", code, errS)
	}
	got := listJSON(t, "--data", dir)[0].Checks
	want := []struct {
		text string
		done bool
	}{
		{"reproduce locally", true},   // the marker ticks it and is stripped
		{"write failing test", false}, // no marker, open
		{"xml parser fails", false},   // needs the space: text kept whole
		{"x ray the build", false},    // backslash escapes the marker
		{"SHOUTED MARKER", true},      // case-insensitive on the x
	}
	if len(got) != len(want) {
		t.Fatalf("got %d checks, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Text != w.text || got[i].Done != w.done {
			t.Errorf("check %d = {%q %v}, want {%q %v}", i, got[i].Text, got[i].Done, w.text, w.done)
		}
	}
}

// TestCheckDonePrefixClosesTheDoneGuard is the F9 payoff: ticking the last
// item and finishing in one update now succeeds on the CLI, where before the
// only way through was --force.
func TestCheckDonePrefixClosesTheDoneGuard(t *testing.T) {
	dir := localEnv(t)
	if _, errS, code := runCmd(t, "add", "Ship release", "--data", dir, "--check", "reproduce locally"); code != 0 {
		t.Fatalf("add failed (code %d): %s", code, errS)
	}
	id := listJSON(t, "--data", dir)[0].ID

	// The same call with the item left open is still refused.
	_, errS, code := runCmd(t, "update", id[:8], "--check", "reproduce locally", "--status", "done", "--data", dir)
	if code != 1 || !strings.Contains(errS, "1 of 1 checklist items are still open") {
		t.Fatalf("update leaving the item open: code %d stderr %q", code, errS)
	}
	if got := listJSON(t, "--data", dir)[0].Status; got != "todo" {
		t.Errorf("refused update moved the task anyway: status %q", got)
	}

	// Ticking it in the very same call clears the guard: no --force needed.
	if _, errS, code = runCmd(t, "update", id[:8], "--check", "x reproduce locally", "--status", "done", "--data", dir); code != 0 {
		t.Fatalf("ticking the last item and finishing in one update failed (code %d): %s", code, errS)
	}
	task := listJSON(t, "--data", dir)[0]
	if task.Status != "done" {
		t.Errorf("status %q, want done", task.Status)
	}
	if len(task.Checks) != 1 || !task.Checks[0].Done || task.Checks[0].Text != "reproduce locally" {
		t.Errorf("checks = %+v, want the one item ticked", task.Checks)
	}
}

// TestUpdateStatusDoneGuard covers the third way into done — update --status
// done. It is guarded exactly like done and move, the guard reads the task as
// the patch leaves it, and patch plus move land (or roll back) together.
func TestUpdateStatusDoneGuard(t *testing.T) {
	dir := localEnv(t)
	if _, errS, code := runCmd(t, "add", "Ship release", "--data", dir,
		"--check", "tag build", "--check", "write notes"); code != 0 {
		t.Fatalf("add failed (code %d): %s", code, errS)
	}
	id := listJSON(t, "--data", dir)[0].ID

	// Refused — and the field patch that rode along in the same call must be
	// rolled back with the move, not left behind.
	_, errS, code := runCmd(t, "update", id[:8], "--status", "done", "--title", "Renamed", "--data", dir)
	if code != 1 {
		t.Fatalf("update --status done with open checks: code %d, want 1", code)
	}
	if !strings.Contains(errS, "2 of 2 checklist items are still open") || !strings.Contains(errS, "--force") {
		t.Errorf("update stderr = %q, want the open-item count and --force", errS)
	}
	got := listJSON(t, "--data", dir)[0]
	if got.Status != "todo" {
		t.Errorf("refused update still moved the task: status %q", got.Status)
	}
	if got.Title != "Ship release" {
		t.Errorf("refused update persisted the field patch anyway: title %q, want %q", got.Title, "Ship release")
	}

	// --force is the way through, and it carries the patch with it.
	if _, errS, code = runCmd(t, "update", id[:8], "--status", "done", "--title", "Shipped", "--force", "--data", dir); code != 0 {
		t.Fatalf("update --status done --force failed (code %d): %s", code, errS)
	}
	if got = listJSON(t, "--data", dir)[0]; got.Status != "done" || got.Title != "Shipped" {
		t.Errorf("forced update: status %q title %q, want done/Shipped", got.Status, got.Title)
	}

	// A patch that itself clears the way needs no --force, because the guard
	// judges the post-patch task rather than the one it started from.
	if _, errS, code := runCmd(t, "add", "Waiting on legal", "--blocked", "--data", dir); code != 0 {
		t.Fatalf("add --blocked failed (code %d): %s", code, errS)
	}
	var blocked jsonTask
	for _, task := range listJSON(t, "--data", dir) {
		if task.Title == "Waiting on legal" {
			blocked = task
		}
	}
	if blocked.ID == "" {
		t.Fatal("blocked task missing from the listing")
	}
	if _, _, code := runCmd(t, "update", blocked.ID[:8], "--status", "done", "--data", dir); code != 1 {
		t.Errorf("update --status done on a blocked task: code %d, want 1", code)
	}
	if _, errS, code := runCmd(t, "update", blocked.ID[:8], "--no-blocked", "--status", "done", "--data", dir); code != 0 {
		t.Fatalf("clearing blocked and finishing in one update failed (code %d): %s", code, errS)
	}
	for _, task := range listJSON(t, "--data", dir) {
		if task.ID == blocked.ID && (task.Status != "done" || task.Blocked) {
			t.Errorf("after unblock+done: status %q blocked %v", task.Status, task.Blocked)
		}
	}
}

// TestBlockedFlag covers F10 on the CLI: the flag pair, the JSON field, and
// the list marker column.
func TestBlockedFlag(t *testing.T) {
	dir := localEnv(t)
	if _, errS, code := runCmd(t, "add", "Waiting on legal", "--blocked", "--data", dir); code != 0 {
		t.Fatalf("add --blocked failed (code %d): %s", code, errS)
	}
	task := listJSON(t, "--data", dir)[0]
	if !task.Blocked {
		t.Fatalf("add --blocked did not set blocked: %+v", task)
	}
	out, _, code := runCmd(t, "list", "--data", dir)
	if code != 0 {
		t.Fatalf("list failed: code %d", code)
	}
	want := "ID  STATUS  PRIO  BLOCKED  TITLE             TAGS\n" +
		"#1  todo    3     yes      Waiting on legal  \n"
	if out != want {
		t.Errorf("blocked list table:\n%q\nwant:\n%q", out, want)
	}
	if _, errS, code := runCmd(t, "update", task.ID[:8], "--no-blocked", "--data", dir); code != 0 {
		t.Fatalf("update --no-blocked failed (code %d): %s", code, errS)
	}
	if listJSON(t, "--data", dir)[0].Blocked {
		t.Error("--no-blocked did not clear the flag")
	}
	// A patch that says nothing about blocked leaves it alone.
	if _, _, code := runCmd(t, "update", task.ID[:8], "--blocked", "--data", dir); code != 0 {
		t.Fatal("re-block failed")
	}
	if _, _, code := runCmd(t, "update", task.ID[:8], "--prio", "1", "--data", dir); code != 0 {
		t.Fatal("prio patch failed")
	}
	if got := listJSON(t, "--data", dir)[0]; !got.Blocked || got.Prio != 1 {
		t.Errorf("unrelated patch disturbed blocked: %+v", got)
	}
}

// TestCancelRestore covers F11 on the CLI: cancel is a soft delete that hides
// the task from the default listing, restore brings it back, and rm --yes
// still means a permanent delete.
func TestCancelRestore(t *testing.T) {
	dir := localEnv(t)
	for _, title := range []string{"Keep me", "Drop me"} {
		if _, errS, code := runCmd(t, "add", title, "--data", dir); code != 0 {
			t.Fatalf("add %q failed (code %d): %s", title, code, errS)
		}
	}
	drop := listJSON(t, "--data", dir)[1].ID

	out, errS, code := runCmd(t, "cancel", drop[:8], "--data", dir)
	if code != 0 {
		t.Fatalf("cancel failed (code %d): %s", code, errS)
	}
	if want := "moved #2 -> cancelled\n"; out != want {
		t.Errorf("cancel output = %q, want %q", out, want)
	}

	// Hidden by default, visible with --all or by name.
	if tasks := listJSON(t, "--data", dir); len(tasks) != 1 || tasks[0].Title != "Keep me" {
		t.Errorf("default list shows cancelled tasks: %+v", tasks)
	}
	if n := len(listJSON(t, "--data", dir, "--all")); n != 2 {
		t.Errorf("list --all: %d tasks, want 2", n)
	}
	byStatus := listJSON(t, "--data", dir, "--status", "cancelled")
	if len(byStatus) != 1 || byStatus[0].ID != drop {
		t.Errorf("list --status cancelled: %+v", byStatus)
	}
	if out, _, _ = runCmd(t, "list", "--data", dir); strings.Contains(out, "Drop me") {
		t.Errorf("default table shows the cancelled task:\n%s", out)
	}

	// Restore puts it back in To Do.
	if _, errS, code = runCmd(t, "restore", drop[:8], "--data", dir); code != 0 {
		t.Fatalf("restore failed (code %d): %s", code, errS)
	}
	if tasks := listJSON(t, "--data", dir); len(tasks) != 2 {
		t.Fatalf("after restore: %d tasks, want 2", len(tasks))
	}
	if got := listJSON(t, "--data", dir, "--status", "todo"); len(got) != 2 {
		t.Errorf("restored task is not in todo: %+v", got)
	}

	// rm --yes keeps its hard-delete meaning.
	if _, errS, code = runCmd(t, "rm", drop[:8], "--yes", "--data", dir); code != 0 {
		t.Fatalf("rm --yes failed (code %d): %s", code, errS)
	}
	if n := len(listJSON(t, "--data", dir, "--all")); n != 1 {
		t.Errorf("after rm --yes: %d tasks, want 1", n)
	}
}

// --- remote mode ---

// wireServer is a minimal in-memory kb server speaking the markdown wire.
type wireServer struct {
	mu       sync.Mutex
	doc      string
	has      bool
	revision int
	lastAuth string
	lastUser string
	lastETag string
}

func (s *wireServer) etag() string { return fmt.Sprintf(`"r%d"`, s.revision) }

func (s *wireServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/board", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.lastAuth = r.Header.Get("Authorization")
		s.lastUser = r.Header.Get("X-KB-User")
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("ETag", s.etag())
			if !s.has {
				http.Error(w, "no board saved", http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
			_, _ = io.WriteString(w, s.doc)
		case http.MethodPut:
			s.lastETag = r.Header.Get("If-Match")
			if s.lastETag != s.etag() {
				w.Header().Set("ETag", s.etag())
				http.Error(w, "board changed since it was read", http.StatusConflict)
				return
			}
			b, _ := io.ReadAll(r.Body)
			s.doc, s.has = string(b), true
			s.revision++
			w.Header().Set("ETag", s.etag())
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	return mux
}

func (s *wireServer) snapshot() (doc, auth, user string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.doc, s.lastAuth, s.lastUser
}

func TestRemoteLifecycle(t *testing.T) {
	ws := &wireServer{}
	srv := httptest.NewServer(ws.handler())
	defer srv.Close()
	t.Setenv("KB_SERVER", srv.URL)
	t.Setenv("KB_SERVER_TOKEN", "sekrit")

	// First add starts from a 404 (no board saved yet).
	out, errS, code := runCmd(t, "add", "Alpha", "--tag", "a", "--user", "Alice")
	if code != 0 {
		t.Fatalf("remote add failed (code %d): %s", code, errS)
	}
	if out != "added i1 Alpha\n" {
		t.Errorf("remote add output = %q, want %q", out, "added i1 Alpha\n")
	}
	doc, auth, user := ws.snapshot()
	wantDoc := "# Board\n\n## To Do\n\n- [ ] Alpha #a\n\n## Doing\n\n\n## Done\n\n"
	if doc != wantDoc {
		t.Errorf("doc after add:\n%q\nwant:\n%q", doc, wantDoc)
	}
	if auth != "Bearer sekrit" {
		t.Errorf("Authorization = %q, want %q", auth, "Bearer sekrit")
	}
	if user != "alice" { // CLI lowercases to match server identity sanitization
		t.Errorf("X-KB-User = %q, want %q", user, "alice")
	}
	ws.mu.Lock()
	if ws.lastETag != `"r0"` {
		t.Errorf("first mutating PUT If-Match = %q, want 404 token %q", ws.lastETag, `"r0"`)
	}
	ws.mu.Unlock()

	if out, errS, code = runCmd(t, "add", "Beta", "--status", "doing", "--tag", "b"); code != 0 || out != "added i2 Beta\n" {
		t.Fatalf("remote add Beta: code %d output %q stderr %s", code, out, errS)
	}

	// Table with ephemeral ids.
	out, _, code = runCmd(t, "list")
	if code != 0 {
		t.Fatalf("remote list failed: code %d", code)
	}
	want := "ID  STATUS  PRIO  BLOCKED  TITLE  TAGS\n" +
		"i1  todo    3     -        Alpha  a\n" +
		"i2  doing   3     -        Beta   b\n"
	if out != want {
		t.Errorf("remote list:\n%q\nwant:\n%q", out, want)
	}
	tasks := listJSON(t)
	if len(tasks) != 2 || tasks[0].ID != "i1" || tasks[1].ID != "i2" {
		t.Errorf("remote list --json ids wrong: %+v", tasks)
	}

	// Patch round-trips through the wire markdown.
	if out, errS, code = runCmd(t, "update", "i1", "--prio", "1", "--desc", "notes"); code != 0 || out != "updated i1 Alpha\n" {
		t.Fatalf("remote update: code %d output %q stderr %s", code, out, errS)
	}
	doc, _, _ = ws.snapshot()
	wantDoc = "# Board\n\n## To Do\n\n- [ ] Alpha !1 #a\n  notes\n\n## Doing\n\n- [ ] Beta #b\n\n## Done\n\n"
	if doc != wantDoc {
		t.Errorf("doc after update:\n%q\nwant:\n%q", doc, wantDoc)
	}

	// Move appends to the target column; the id reported is the new index.
	if out, errS, code = runCmd(t, "move", "i1", "doing"); code != 0 || out != "moved i2 -> doing\n" {
		t.Fatalf("remote move: code %d output %q stderr %s", code, out, errS)
	}
	doc, _, _ = ws.snapshot()
	if !strings.Contains(doc, "## Doing\n\n- [ ] Beta #b\n- [ ] Alpha !1 #a\n  notes\n") {
		t.Errorf("doc after move lacks reordered Doing column:\n%q", doc)
	}

	// done = move done; Alpha is i2 within doing.
	if out, errS, code = runCmd(t, "done", "i2"); code != 0 || out != "moved i2 -> done\n" {
		t.Fatalf("remote done: code %d output %q stderr %s", code, out, errS)
	}
	doc, _, _ = ws.snapshot()
	if !strings.Contains(doc, "## Done\n\n- [x] Alpha !1 #a\n  notes\n") {
		t.Errorf("doc after done lacks completed Alpha:\n%q", doc)
	}

	// rm accepts a bare index and reports the normalized id.
	if out, errS, code = runCmd(t, "rm", "1", "--yes"); code != 0 || out != "deleted i1 Beta\n" {
		t.Fatalf("remote rm: code %d output %q stderr %s", code, out, errS)
	}
	if doc, _, _ = ws.snapshot(); strings.Contains(doc, "Beta") {
		t.Errorf("Beta still present after rm:\n%q", doc)
	}

	// Out-of-range and malformed ids are runtime errors.
	if _, errS, code = runCmd(t, "move", "i9", "todo"); code != 1 || !strings.Contains(errS, "no task") {
		t.Errorf("remote bad index: code %d stderr %q", code, errS)
	}
	if _, errS, code = runCmd(t, "done", "ix"); code != 1 || !strings.Contains(errS, "invalid remote task id") {
		t.Errorf("remote malformed id: code %d stderr %q", code, errS)
	}
}

// TestRemoteValidatesFields is S2: remote mode serializes the wire markdown
// itself, so it must run the same field validation the store applies. Before
// the fix a newline in --check (or a title, tag, or emoji the wire cannot
// represent) forged extra board lines.
func TestRemoteValidatesFields(t *testing.T) {
	ws := &wireServer{}
	srv := httptest.NewServer(ws.handler())
	defer srv.Close()
	t.Setenv("KB_SERVER", srv.URL)
	t.Setenv("KB_SERVER_TOKEN", "")

	if _, errS, code := runCmd(t, "add", "Real task"); code != 0 {
		t.Fatalf("seed add failed (code %d): %s", code, errS)
	}
	before, _, _ := ws.snapshot()

	cases := []struct {
		name string
		args []string
	}{
		{"newline in check", []string{"add", "Forger", "--check", "ok\n- [ ] forged !1 #pwned"}},
		{"newline in title", []string{"add", "Forger\n- [x] forged"}},
		{"newline in tag", []string{"add", "Forger", "--tag", "a\nb"}},
		{"space in tag", []string{"add", "Forger", "--tag", "two words"}},
		{"leading # in tag", []string{"add", "Forger", "--tag", "#hash"}},
		{"multi-rune emoji", []string{"add", "Forger", "--emoji", "abc"}},
		{"patched title newline", []string{"update", "i1", "--title", "Real\n- [ ] forged"}},
		{"patched check newline", []string{"update", "i1", "--check", "ok\n- [ ] forged"}},
	}
	for _, tc := range cases {
		_, errS, code := runCmd(t, tc.args...)
		if code != 1 {
			t.Errorf("%s: code %d, want 1 (stderr %q)", tc.name, code, errS)
		}
		if !strings.Contains(errS, "store:") {
			t.Errorf("%s: stderr %q, want the shared validator's message", tc.name, errS)
		}
		if doc, _, _ := ws.snapshot(); doc != before {
			t.Fatalf("%s: board changed on a rejected write:\n%q", tc.name, doc)
		}
	}
}

// board.Parse is deliberately more tolerant than the field validator, so a
// board written by the SPA's whole-board PUT or a legacy import can hold
// values ValidateTaskFields rejects. Local mode moves such a task without
// complaint (store.patchTask returns early on an empty patch, so MoveTask
// never validates); remote mode must behave the same or the card cannot be
// moved, shipped, cancelled or restored at all.
func TestRemoteMoveSkipsFieldValidation(t *testing.T) {
	ws := &wireServer{doc: "# B\n\n## To Do\n\n- [ ] Ship v2 @2026-02-30\n", has: true}
	srv := httptest.NewServer(ws.handler())
	defer srv.Close()
	t.Setenv("KB_SERVER", srv.URL)
	t.Setenv("KB_SERVER_TOKEN", "")

	for _, args := range [][]string{
		{"move", "i1", "doing"},
		{"done", "i1"},
		{"restore", "i1"},
		{"cancel", "i1"},
	} {
		if _, errS, code := runCmd(t, args...); code != 0 {
			t.Fatalf("remote %v: code %d stderr %q", args, code, errS)
		}
	}
	if doc, _, _ := ws.snapshot(); !strings.Contains(doc, "## Cancelled\n\n- [ ] Ship v2 @2026-02-30\n") {
		t.Errorf("board after the moves:\n%q", doc)
	}
	// A move is not a field write; an actual field write on the same task is
	// still validated.
	if _, errS, code := runCmd(t, "update", "i1", "--title", "x\n- [ ] forged"); code != 1 ||
		!strings.Contains(errS, "store:") {
		t.Errorf("update on a parse-tolerant task: code %d stderr %q, want the validator's refusal", code, errS)
	}
}

// TestRemoteBlockedAndCancelled covers F10/F11 over the wire: the %blocked
// token round-trips, the Cancelled section appears, and cancelled tasks stay
// out of the default listing.
func TestRemoteBlockedAndCancelled(t *testing.T) {
	ws := &wireServer{}
	srv := httptest.NewServer(ws.handler())
	defer srv.Close()
	t.Setenv("KB_SERVER", srv.URL)
	t.Setenv("KB_SERVER_TOKEN", "")

	if _, errS, code := runCmd(t, "add", "Blocked one", "--blocked"); code != 0 {
		t.Fatalf("remote add --blocked failed (code %d): %s", code, errS)
	}
	if doc, _, _ := ws.snapshot(); !strings.Contains(doc, "- [ ] Blocked one %blocked\n") {
		t.Errorf("wire lacks the %%blocked token:\n%q", doc)
	}
	if got := listJSON(t)[0]; !got.Blocked {
		t.Errorf("blocked did not round-trip through the wire: %+v", got)
	}
	if _, errS, code := runCmd(t, "update", "i1", "--no-blocked"); code != 0 {
		t.Fatalf("remote update --no-blocked failed (code %d): %s", code, errS)
	}
	if doc, _, _ := ws.snapshot(); strings.Contains(doc, "%blocked") {
		t.Errorf("--no-blocked left the token on the wire:\n%q", doc)
	}

	if _, errS, code := runCmd(t, "add", "Doomed"); code != 0 {
		t.Fatalf("remote add failed (code %d): %s", code, errS)
	}
	if _, errS, code := runCmd(t, "cancel", "i2"); code != 0 {
		t.Fatalf("remote cancel failed (code %d): %s", code, errS)
	}
	doc, _, _ := ws.snapshot()
	if !strings.Contains(doc, "## Cancelled\n\n- [ ] Doomed\n") {
		t.Errorf("wire lacks the Cancelled section:\n%q", doc)
	}
	if tasks := listJSON(t); len(tasks) != 1 || tasks[0].Title != "Blocked one" {
		t.Errorf("default remote list shows cancelled tasks: %+v", tasks)
	}
	if n := len(listJSON(t, "--all")); n != 2 {
		t.Errorf("remote list --all: %d tasks, want 2", n)
	}
	// The cancelled task keeps a listing id after the live columns.
	if _, errS, code := runCmd(t, "restore", "i2"); code != 0 {
		t.Fatalf("remote restore failed (code %d): %s", code, errS)
	}
	if doc, _, _ = ws.snapshot(); strings.Contains(doc, "## Cancelled") {
		t.Errorf("Cancelled section survived the restore:\n%q", doc)
	}
}

// TestRemoteDoneWarning is F9 over the wire: the guard reads the task the
// remote backend lists, so it works without any local database.
func TestRemoteDoneWarning(t *testing.T) {
	ws := &wireServer{}
	srv := httptest.NewServer(ws.handler())
	defer srv.Close()
	t.Setenv("KB_SERVER", srv.URL)
	t.Setenv("KB_SERVER_TOKEN", "")

	if _, errS, code := runCmd(t, "add", "Half done", "--check", "a", "--check", "b"); code != 0 {
		t.Fatalf("remote add failed (code %d): %s", code, errS)
	}
	_, errS, code := runCmd(t, "done", "i1")
	if code != 1 || !strings.Contains(errS, "2 of 2 checklist items are still open") {
		t.Fatalf("remote done: code %d stderr %q", code, errS)
	}
	if doc, _, _ := ws.snapshot(); strings.Contains(doc, "- [x]") {
		t.Errorf("refused done still wrote the board:\n%q", doc)
	}
	if _, errS, code = runCmd(t, "done", "i1", "--force"); code != 0 {
		t.Fatalf("remote done --force failed (code %d): %s", code, errS)
	}
	if doc, _, _ := ws.snapshot(); !strings.Contains(doc, "## Done\n\n- [x] Half done\n") {
		t.Errorf("done --force did not ship the task:\n%q", doc)
	}
}

func TestRemoteMutationConflictsWithoutRetryingOrBypassingDoneGuard(t *testing.T) {
	const initial = "# B\n\n## To Do\n\n- [ ] Race\n"
	var mu sync.Mutex
	doc := initial
	revision := 1
	gets, puts := 0, 0
	var putIfMatch string
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		etag := fmt.Sprintf(`"r%d"`, revision)
		switch r.Method {
		case http.MethodGet:
			gets++
			w.Header().Set("ETag", etag)
			_, _ = io.WriteString(w, doc)
		case http.MethodPut:
			puts++
			putIfMatch = r.Header.Get("If-Match")
			// Deterministically model another writer adding an open checklist
			// item after the CLI read but before its conditional write.
			if puts == 1 {
				doc = "# B\n\n## To Do\n\n- [ ] Race\n  - [ ] newly blocked work\n"
				revision++
				etag = fmt.Sprintf(`"r%d"`, revision)
			}
			if putIfMatch != etag {
				w.Header().Set("ETag", etag)
				http.Error(w, "board changed since it was read", http.StatusConflict)
				return
			}
			t.Fatal("stale remote PUT unexpectedly matched")
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	srv := httptest.NewServer(h)
	defer srv.Close()
	t.Setenv("KB_SERVER", srv.URL)
	t.Setenv("KB_SERVER_TOKEN", "")

	_, errS, code := runCmd(t, "done", "i1")
	if code == 0 || !strings.Contains(errS, "409 Conflict") {
		t.Fatalf("remote done = code %d stderr %q, want surfaced conflict", code, errS)
	}
	mu.Lock()
	defer mu.Unlock()
	if gets != 1 || puts != 1 || putIfMatch != `"r1"` {
		t.Fatalf("requests = GET %d PUT %d If-Match %q, want one bound snapshot and %q", gets, puts, putIfMatch, `"r1"`)
	}
	if strings.Contains(doc, "## Done") || !strings.Contains(doc, "newly blocked work") {
		t.Fatalf("remote board after race = %q, want intervening guarded task intact", doc)
	}
}

func TestRemoteMutationFailsClosedWithoutETag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, "# B\n\n## To Do\n\n- [ ] One\n")
			return
		}
		t.Fatal("remote client attempted an unconditional PUT")
	}))
	defer srv.Close()
	t.Setenv("KB_SERVER", srv.URL)
	t.Setenv("KB_SERVER_TOKEN", "")

	_, errS, code := runCmd(t, "done", "i1", "--force")
	if code == 0 || !strings.Contains(errS, "no ETag") {
		t.Fatalf("missing ETag = code %d stderr %q", code, errS)
	}
}

// TestRemoteRefusesCrossHostRedirect pins the egress promise for `KB_SERVER`
// mode: every request carries the server token and replays the whole board, so
// a redirect off the configured host would hand both to a third party.
func TestRemoteRefusesCrossHostRedirect(t *testing.T) {
	var sinkHits int
	var mu sync.Mutex
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		sinkHits++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer sink.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, sink.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()

	t.Setenv("KB_SERVER", redirector.URL)
	t.Setenv("KB_SERVER_TOKEN", "sekrit")

	_, errS, code := runCmd(t, "list")
	if code == 0 {
		t.Fatalf("list followed a cross-host redirect (code %d)", code)
	}
	if !strings.Contains(errS, "refusing cross-host redirect") {
		t.Errorf("stderr = %q, want it to name the refused redirect", errS)
	}
	mu.Lock()
	defer mu.Unlock()
	if sinkHits != 0 {
		t.Errorf("the other host was contacted %d times, want 0", sinkHits)
	}
}
