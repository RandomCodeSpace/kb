package cliapp

import (
	"bytes"
	"encoding/json"
	"fmt"
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
	// Projects are mandatory: give the seeded tasks one so tests that are
	// about something else do not have to spell it out.
	t.Setenv("KB_PROJECT", inboxProject)
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
	if strings.Join(t1.Tags, ",") != "docs,cli,project::inbox" || len(t1.Checks) != 1 || t1.Checks[0].Text != "outline" || t1.Checks[0].Done {
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
		"#1  todo    2     -        Write the docs  docs,cli,project::inbox\n" +
		"#2  doing   3     -        Fix login bug   bug,project::inbox\n"
	if out != want {
		t.Errorf("list table:\n%q\nwant:\n%q", out, want)
	}

	// Patch by unique id prefix: only provided flags change.
	out, errS, code = runCmd(t, "update", t1.ID, "--data", dir, "--title", "Write the docs v2", "--prio", "1")
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
	if _, errS, code = runCmd(t, "update", t1.ID, "--data", dir, "--status", "doing"); code != 0 {
		t.Fatalf("update --status failed (code %d): %s", code, errS)
	}
	if n := len(listJSON(t, "--data", dir, "--status", "doing")); n != 2 {
		t.Errorf("after update --status doing: %d doing tasks, want 2", n)
	}

	// move and done.
	out, errS, code = runCmd(t, "move", t2.ID, "done", "--data", dir)
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
	_, errS, code = runCmd(t, "rm", t2.ID, "--data", dir)
	if code != 1 || !strings.Contains(errS, "--yes") {
		t.Errorf("rm without --yes: code %d stderr %q, want code 1 mentioning --yes", code, errS)
	}
	if n := len(listJSON(t, "--data", dir)); n != 2 {
		t.Fatalf("rm without --yes deleted something: %d tasks left", n)
	}
	out, errS, code = runCmd(t, "rm", t2.ID, "--yes", "--data", dir)
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

	_, errS, code := runCmd(t, "done", id, "--data", dir)
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
	if _, _, code = runCmd(t, "move", id, "done", "--data", dir); code != 1 {
		t.Errorf("move ... done with open checks: code %d, want 1", code)
	}

	// Ticking everything off clears the warning without --force.
	if _, errS, code = runCmd(t, "update", id, "--data", dir, "--check", "tag build"); code != 0 {
		t.Fatalf("update --check failed (code %d): %s", code, errS)
	}
	if _, errS, code = runCmd(t, "update", id, "--data", dir, "--blocked"); code != 0 {
		t.Fatalf("update --blocked failed (code %d): %s", code, errS)
	}
	// Still blocked, so still refused — and the message says so.
	_, errS, code = runCmd(t, "done", id, "--data", dir)
	if code != 1 || !strings.Contains(errS, "flagged blocked") {
		t.Fatalf("done while blocked: code %d stderr %q", code, errS)
	}
	if _, errS, code = runCmd(t, "update", id, "--data", dir, "--no-blocked"); code != 0 {
		t.Fatalf("update --no-blocked failed (code %d): %s", code, errS)
	}
	// One open item remains ("tag build" replaced the list, unticked), so
	// --force is the way through.
	if _, errS, code = runCmd(t, "done", id, "--force", "--data", dir); code != 0 {
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
	_, errS, code := runCmd(t, "update", id, "--check", "reproduce locally", "--status", "done", "--data", dir)
	if code != 1 || !strings.Contains(errS, "1 of 1 checklist items are still open") {
		t.Fatalf("update leaving the item open: code %d stderr %q", code, errS)
	}
	if got := listJSON(t, "--data", dir)[0].Status; got != "todo" {
		t.Errorf("refused update moved the task anyway: status %q", got)
	}

	// Ticking it in the very same call clears the guard: no --force needed.
	if _, errS, code = runCmd(t, "update", id, "--check", "x reproduce locally", "--status", "done", "--data", dir); code != 0 {
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
	_, errS, code := runCmd(t, "update", id, "--status", "done", "--title", "Renamed", "--data", dir)
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
	if _, errS, code = runCmd(t, "update", id, "--status", "done", "--title", "Shipped", "--force", "--data", dir); code != 0 {
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
	if _, _, code := runCmd(t, "update", blocked.ID, "--status", "done", "--data", dir); code != 1 {
		t.Errorf("update --status done on a blocked task: code %d, want 1", code)
	}
	if _, errS, code := runCmd(t, "update", blocked.ID, "--no-blocked", "--status", "done", "--data", dir); code != 0 {
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
		"#1  todo    3     yes      Waiting on legal  project::inbox\n"
	if out != want {
		t.Errorf("blocked list table:\n%q\nwant:\n%q", out, want)
	}
	if _, errS, code := runCmd(t, "update", task.ID, "--no-blocked", "--data", dir); code != 0 {
		t.Fatalf("update --no-blocked failed (code %d): %s", code, errS)
	}
	if listJSON(t, "--data", dir)[0].Blocked {
		t.Error("--no-blocked did not clear the flag")
	}
	// A patch that says nothing about blocked leaves it alone.
	if _, _, code := runCmd(t, "update", task.ID, "--blocked", "--data", dir); code != 0 {
		t.Fatal("re-block failed")
	}
	if _, _, code := runCmd(t, "update", task.ID, "--prio", "1", "--data", dir); code != 0 {
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

	out, errS, code := runCmd(t, "cancel", drop, "--data", dir)
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
	if _, errS, code = runCmd(t, "restore", drop, "--data", dir); code != 0 {
		t.Fatalf("restore failed (code %d): %s", code, errS)
	}
	if tasks := listJSON(t, "--data", dir); len(tasks) != 2 {
		t.Fatalf("after restore: %d tasks, want 2", len(tasks))
	}
	if got := listJSON(t, "--data", dir, "--status", "todo"); len(got) != 2 {
		t.Errorf("restored task is not in todo: %+v", got)
	}

	// rm --yes keeps its hard-delete meaning.
	if _, errS, code = runCmd(t, "rm", drop, "--yes", "--data", dir); code != 0 {
		t.Fatalf("rm --yes failed (code %d): %s", code, errS)
	}
	if n := len(listJSON(t, "--data", dir, "--all")); n != 1 {
		t.Errorf("after rm --yes: %d tasks, want 1", n)
	}
}

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
