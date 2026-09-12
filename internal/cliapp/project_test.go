package cliapp

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
)

// projectsOf returns the project names a listed task carries, so a test can
// assert the invariant instead of eyeballing a tag slice.
func projectsOf(t *testing.T, task jsonTask) []string {
	t.Helper()
	names, _ := SplitProjectTags(task.Tags)
	return names
}

// assertOneProject pins the CLI invariant: every task carries exactly one
// project:: label, never zero and never two.
func assertOneProject(t *testing.T, tasks []jsonTask, want ...string) {
	t.Helper()
	if len(tasks) != len(want) {
		t.Fatalf("got %d tasks, want %d: %+v", len(tasks), len(want), tasks)
	}
	for i, task := range tasks {
		names := projectsOf(t, task)
		if len(names) != 1 {
			t.Fatalf("task %d carries %d project labels, want exactly 1: %+v", i, len(names), task.Tags)
		}
		if names[0] != want[i] {
			t.Errorf("task %d project = %q, want %q", i, names[0], want[i])
		}
	}
}

// noProjectEnv is localEnv without the implicit -p the harness adds to add:
// the commands run exactly as typed.
func noProjectEnv(t *testing.T) string {
	t.Helper()
	dir := localEnv(t)
	implicitProject = false
	t.Cleanup(func() { implicitProject = true })
	return dir
}

// TestProjectFlagIsTheOnlySource pins the rule: nothing but the command
// names a project. An environment variable and a leftover state.json from an
// older kb are both ignored, so parallel shells cannot file into each
// other's project.
func TestProjectFlagIsTheOnlySource(t *testing.T) {
	dir := noProjectEnv(t)
	t.Setenv("KB_PROJECT", "fromenv")
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{"active_project":"stored"}`+"\n"), 0o600); err != nil {
		t.Fatalf("write legacy state: %v", err)
	}
	if _, stderr, code := runCmd(t, "add", "Orphan", "--data", dir); code != 2 || !strings.Contains(stderr, "no project given") {
		t.Fatalf("add with env and stored project: code=%d stderr=%q", code, stderr)
	}
	if _, stderr, code := runCmd(t, "add", "Short", "--data", dir, "-p", "web"); code != 0 {
		t.Fatalf("add -p: code=%d stderr=%q", code, stderr)
	}
	if _, stderr, code := runCmd(t, "add", "Long", "--data", dir, "--project", "api"); code != 0 {
		t.Fatalf("add --project: code=%d stderr=%q", code, stderr)
	}
	assertOneProject(t, listJSON(t, "--data", dir), "web", "api")
	// list -p keeps one project; without it every project is listed.
	if tasks := listJSON(t, "--data", dir, "-p", "api"); len(tasks) != 1 || tasks[0].Title != "Long" {
		t.Fatalf("list -p api = %+v", tasks)
	}
	if _, stderr, code := runCmd(t, "list", "--data", dir, "-p", "bad name"); code != 2 || !strings.Contains(stderr, "whitespace") {
		t.Fatalf("list -p bad name: code=%d stderr=%q", code, stderr)
	}
}

func TestProjectAddRefusesWithoutAProject(t *testing.T) {
	dir := noProjectEnv(t)
	_, stderr, code := runCmd(t, "add", "Orphan", "--data", dir)
	if code != 2 {
		t.Fatalf("add without a project: code=%d stderr=%q", code, stderr)
	}
	for _, want := range []string{"no project given", "-p <name>", "project::<name>"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("refusal %q does not name %q", stderr, want)
		}
	}
	if tasks := listJSON(t, "--data", dir); len(tasks) != 0 {
		t.Fatalf("refused add still wrote %+v", tasks)
	}
}

func TestProjectAddHonoursSpelledOutLabel(t *testing.T) {
	dir := noProjectEnv(t)
	// A project:: label in --tag is as explicit as -p, so it stands in for
	// the active project even when there is none.
	if _, stderr, code := runCmd(t, "add", "Tagged", "--data", dir, "--tag", "project::web", "--tag", "ui"); code != 0 {
		t.Fatalf("add: code=%d stderr=%q", code, stderr)
	}
	tasks := listJSON(t, "--data", dir)
	assertOneProject(t, tasks, "web")
	if !slices.Contains(tasks[0].Tags, "ui") {
		t.Errorf("plain tags dropped: %+v", tasks[0].Tags)
	}

	// An explicit -p that contradicts the spelled-out label is a refusal
	// rather than a silent winner.
	if _, stderr, code := runCmd(t, "add", "Also tagged", "--data", dir, "--tag", "project::web"); code != 0 {
		t.Fatalf("add: code=%d stderr=%q", code, stderr)
	}
	_, stderr, code := runCmd(t, "add", "Conflict", "--data", dir, "-p", "api", "--tag", "project::web")
	if code != 2 || !strings.Contains(stderr, "contradicts") {
		t.Fatalf("conflicting add: code=%d stderr=%q", code, stderr)
	}
	assertOneProject(t, listJSON(t, "--data", dir), "web", "web")
}

func TestProjectAddRefusesTwoProjectLabels(t *testing.T) {
	dir := localEnv(t)
	_, stderr, code := runCmd(t, "add", "Two", "--data", dir, "--tag", "project::a", "--tag", "project::b")
	if code != 2 || !strings.Contains(stderr, "exactly one project:: label, got 2") {
		t.Fatalf("two project labels: code=%d stderr=%q", code, stderr)
	}
	if tasks := listJSON(t, "--data", dir); len(tasks) != 0 {
		t.Fatalf("refused add still wrote %+v", tasks)
	}
}

func TestProjectInvariantSurvivesUpdates(t *testing.T) {
	dir := noProjectEnv(t)
	if _, stderr, code := runCmd(t, "add", "Task", "--data", dir, "-p", inboxProject, "--tag", "keep"); code != 0 {
		t.Fatalf("add: code=%d stderr=%q", code, stderr)
	}

	// Replacing the labels wholesale keeps the task in its project instead of
	// dropping the label or demanding -p again.
	if _, stderr, code := runCmd(t, "update", "1", "--data", dir, "--tag", "fresh"); code != 0 {
		t.Fatalf("update --tag: code=%d stderr=%q", code, stderr)
	}
	tasks := listJSON(t, "--data", dir)
	assertOneProject(t, tasks, inboxProject)
	if !slices.Contains(tasks[0].Tags, "fresh") || slices.Contains(tasks[0].Tags, "keep") {
		t.Errorf("tags not replaced: %+v", tasks[0].Tags)
	}

	// -p alone is a complete update: it moves the task and leaves the rest.
	if out, stderr, code := runCmd(t, "update", "1", "--data", dir, "-p", "web"); code != 0 || out != "updated #1 Task\n" {
		t.Fatalf("update -p: code=%d out=%q stderr=%q", code, out, stderr)
	}
	tasks = listJSON(t, "--data", dir)
	assertOneProject(t, tasks, "web")
	if !slices.Contains(tasks[0].Tags, "fresh") {
		t.Errorf("update -p dropped plain tags: %+v", tasks[0].Tags)
	}

	// Spelling the label in --tag moves it too, and two of them are refused.
	if _, stderr, code := runCmd(t, "update", "1", "--data", dir, "--tag", "project::api"); code != 0 {
		t.Fatalf("update --tag project::: code=%d stderr=%q", code, stderr)
	}
	assertOneProject(t, listJSON(t, "--data", dir), "api")
	if _, stderr, code := runCmd(t, "update", "1", "--data", dir, "--tag", "project::a", "--tag", "project::b"); code != 1 ||
		!strings.Contains(stderr, "exactly one project:: label") {
		t.Fatalf("two labels on update: code=%d stderr=%q", code, stderr)
	}
	assertOneProject(t, listJSON(t, "--data", dir), "api")

	// A patch that says nothing about labels leaves the project alone.
	if _, stderr, code := runCmd(t, "update", "1", "--data", dir, "--prio", "1"); code != 0 {
		t.Fatalf("update --prio: code=%d stderr=%q", code, stderr)
	}
	assertOneProject(t, listJSON(t, "--data", dir), "api")
}

func TestProjectUpdateNeedsAResolvableProject(t *testing.T) {
	dir := localEnv(t)
	if _, stderr, code := runCmd(t, "add", "Task", "--data", dir); code != 0 {
		t.Fatalf("add: code=%d stderr=%q", code, stderr)
	}
	// A task whose project label was stripped outside the CLI, updated with
	// nothing to resolve: refused rather than left project-less.
	st := openTestStore(t, dir)
	tasks, err := st.ListTasks(defaultUser, "")
	if err != nil || len(tasks) != 1 {
		t.Fatalf("list tasks: %+v %v", tasks, err)
	}
	none := []string{}
	if _, err := st.UpdateTask(defaultUser, tasks[0].ID, store.TaskPatch{Tags: &none}); err != nil {
		t.Fatalf("strip labels: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	// Opening the store backfills, so strip again through the same command.
	if _, stderr, code := runCmd(t, "update", "1", "--data", dir, "--tag", "plain"); code != 0 {
		t.Fatalf("update after backfill: code=%d stderr=%q", code, stderr)
	}
	assertOneProject(t, listJSON(t, "--data", dir), inboxProject)
}

// openTestStore opens the same database the CLI writes, for tests that need
// to reach past the CLI to set a board up.
func openTestStore(t *testing.T, dir string) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(dir, dbFile), []byte("test-secret"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return st
}

func TestProjectBackfillIsIdempotent(t *testing.T) {
	dir := localEnv(t)
	st := openTestStore(t, dir)
	defer st.Close()
	for _, tags := range [][]string{nil, {"docs"}, {"project::web"}, {"project::a", "project::b"}} {
		if _, err := st.AddTask(defaultUser, board.Task{Title: "seed", Tags: tags}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	changed, err := BackfillProjects(st, defaultUser)
	if err != nil || changed != 3 {
		t.Fatalf("backfill changed=%d err=%v, want 3", changed, err)
	}
	tasks, err := st.ListTasks(defaultUser, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	want := []string{inboxProject, inboxProject, "web", "a"}
	for i, task := range tasks {
		names, _ := SplitProjectTags(task.Tags)
		if len(names) != 1 || names[0] != want[i] {
			t.Errorf("task %d projects = %v, want [%s]", i, names, want[i])
		}
	}
	if !slices.Contains(tasks[1].Tags, "docs") {
		t.Errorf("backfill dropped plain tags: %+v", tasks[1].Tags)
	}
	// Second pass has nothing to do.
	if changed, err := BackfillProjects(st, defaultUser); err != nil || changed != 0 {
		t.Fatalf("second backfill changed=%d err=%v, want 0", changed, err)
	}
}

func TestProjectBackfillRunsOnLocalOpen(t *testing.T) {
	dir := localEnv(t)
	st := openTestStore(t, dir)
	if _, err := st.AddTask(defaultUser, board.Task{Title: "legacy"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	assertOneProject(t, listJSON(t, "--data", dir), inboxProject)
}

func TestProjectListCounts(t *testing.T) {
	dir := noProjectEnv(t)
	for _, args := range [][]string{
		{"add", "One", "-p", "web"},
		{"add", "Two", "-p", "web"},
		{"add", "Three", "-p", "api"},
	} {
		if _, stderr, code := runCmd(t, append(args, "--data", dir)...); code != 0 {
			t.Fatalf("%v: code=%d stderr=%q", args, code, stderr)
		}
	}
	// Cancelled tasks still belong to their project and still count.
	if _, stderr, code := runCmd(t, "cancel", "3", "--data", dir); code != 0 {
		t.Fatalf("cancel: code=%d stderr=%q", code, stderr)
	}
	out, stderr, code := runCmd(t, "project", "list", "--data", dir)
	if code != 0 {
		t.Fatalf("project list: code=%d stderr=%q", code, stderr)
	}
	want := "PROJECT  TASKS\n" +
		"api      1\n" +
		"web      2\n"
	if out != want {
		t.Errorf("project list:\n%q\nwant:\n%q", out, want)
	}
	out, _, code = runCmd(t, "project", "list", "--json", "--data", dir)
	if code != 0 {
		t.Fatalf("project list --json: code=%d", code)
	}
	var rows []projectCountJSON
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("project list --json: %v\n%s", err, out)
	}
	if len(rows) != 2 || rows[0] != (projectCountJSON{Project: "api", Tasks: 1}) ||
		rows[1] != (projectCountJSON{Project: "web", Tasks: 2}) {
		t.Errorf("project list --json = %+v", rows)
	}
}

func TestProjectNameValidation(t *testing.T) {
	for _, tc := range []struct {
		name, want string
	}{
		{"", "must not be empty"},
		{"   ", "must not be empty"},
		{"two words", "must not contain whitespace"},
		{"#hash", "must not start with '#'"},
		{"a::b", `must not contain "::"`},
	} {
		if _, err := ValidateProjectName(tc.name); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("ValidateProjectName(%q) = %v, want %q", tc.name, err, tc.want)
		}
	}
	if got, err := ValidateProjectName("  web-ui  "); err != nil || got != "web-ui" {
		t.Errorf("ValidateProjectName = %q, %v", got, err)
	}
}

func TestProjectInvalidNamesAreRefusedEverywhere(t *testing.T) {
	dir := noProjectEnv(t)
	if _, stderr, code := runCmd(t, "add", "Task", "--data", dir, "-p", "bad name"); code != 2 ||
		!strings.Contains(stderr, "must not contain whitespace") {
		t.Fatalf("add -p bad name: code=%d stderr=%q", code, stderr)
	}
	if _, stderr, code := runCmd(t, "add", "Task", "--data", dir, "-p", "a::b"); code != 2 ||
		!strings.Contains(stderr, `must not contain "::"`) {
		t.Fatalf("add -p a::b: code=%d stderr=%q", code, stderr)
	}
	if _, stderr, code := runCmd(t, "add", "Task", "--data", dir, "--tag", "project::"); code != 2 ||
		!strings.Contains(stderr, "must not be empty") {
		t.Fatalf("add --tag project:: : code=%d stderr=%q", code, stderr)
	}
}

func TestProjectUsageErrors(t *testing.T) {
	dir := localEnv(t)
	cases := []struct {
		args []string
		code int
		want string
	}{
		{[]string{"project", "wat"}, 2, `unknown project subcommand "wat"`},
		{[]string{"project", "use", "web"}, 2, `unknown project subcommand "use"`},
		{[]string{"project", "current"}, 2, `unknown project subcommand "current"`},
		{[]string{"project", "list", "extra"}, 2, "takes no arguments"},
		{[]string{"project", "list", "--nope"}, 2, "flag provided but not defined"},
	}
	for _, tc := range cases {
		_, stderr, code := runCmd(t, append(tc.args, "--data", dir)...)
		if code != tc.code || !strings.Contains(stderr, tc.want) {
			t.Errorf("%v: code=%d stderr=%q, want code %d containing %q", tc.args, code, stderr, tc.code, tc.want)
		}
	}
	if _, stderr, code := runCmd(t, "project"); code != 2 || !strings.Contains(stderr, "usage: kb project") {
		t.Errorf("bare project: code=%d stderr=%q", code, stderr)
	}
	out, _, code := runCmd(t, "project", "help")
	if code != 0 || !strings.Contains(out, "usage: kb project") {
		t.Errorf("project help = %q (code %d)", out, code)
	}
	out, _, code = runCmd(t, "project", "list", "-h")
	if code != 0 || !strings.Contains(out, "usage: kb") {
		t.Errorf("project list -h = %q (code %d)", out, code)
	}
}

func TestCurrentProjectOfWantsExactlyOne(t *testing.T) {
	for _, tc := range []struct {
		tags []string
		want string
	}{
		{nil, ""},
		{[]string{"plain"}, ""},
		{[]string{"plain", "project::web"}, "web"},
		{[]string{"project::a", "project::b"}, ""},
	} {
		if got := CurrentProjectOf(board.Task{Tags: tc.tags}); got != tc.want {
			t.Errorf("CurrentProjectOf(%v) = %q, want %q", tc.tags, got, tc.want)
		}
	}
}

func TestProjectBackfillReportsStoreFailures(t *testing.T) {
	dir := localEnv(t)
	st := openTestStore(t, dir)
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := BackfillProjects(st, defaultUser); err == nil {
		t.Error("backfill over a closed store succeeded")
	}
}

// failingBackfiller lets the update half of the backfill fail.
type failingBackfiller struct {
	tasks []board.Task
	err   error
}

func (f failingBackfiller) FilterTasks(string, store.TaskFilter) ([]board.Task, error) {
	return f.tasks, nil
}

func (f failingBackfiller) UpdateTask(string, string, store.TaskPatch) (board.Task, error) {
	return board.Task{}, f.err
}

func TestProjectBackfillStopsOnUpdateFailure(t *testing.T) {
	boom := errors.New("boom")
	stub := failingBackfiller{tasks: []board.Task{{ID: "a"}, {ID: "b"}}, err: boom}
	changed, err := BackfillProjects(stub, defaultUser)
	if !errors.Is(err, boom) || changed != 0 {
		t.Fatalf("backfill changed=%d err=%v, want 0 and boom", changed, err)
	}
}

func TestProjectCommandsNeedAResolvableDataDir(t *testing.T) {
	noProjectEnv(t)
	// No --data, no KB_DATA, and no home directory to fall back on.
	t.Setenv("KB_DATA", "")
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	for _, args := range [][]string{
		{"project", "list"},
		{"add", "Task", "-p", "web"},
	} {
		_, stderr, code := runCmd(t, args...)
		if code == 0 || !strings.Contains(stderr, "cannot determine home directory") {
			t.Errorf("%v: code=%d stderr=%q, want a data-directory failure", args, code, stderr)
		}
	}
}

func TestProjectUpdateReportsUnknownTask(t *testing.T) {
	dir := localEnv(t)
	if _, stderr, code := runCmd(t, "update", "9", "--data", dir, "-p", "web"); code != 1 ||
		!strings.Contains(stderr, "no task matches id") {
		t.Fatalf("update unknown: code=%d stderr=%q", code, stderr)
	}
}

func TestOpenLocalStoreWarnsAboutProjectBackfill(t *testing.T) {
	for _, partialFailure := range []bool{false, true} {
		name := "successful backfill"
		if partialFailure {
			name = "partial backfill"
		}
		t.Run(name, func(t *testing.T) {
			dir := localEnv(t)
			st := openTestStore(t, dir)
			if _, err := st.AddTask(defaultUser, board.Task{Title: "repair", Tags: []string{"project::first", "project::dropped"}}); err != nil {
				t.Fatal(err)
			}
			if partialFailure {
				if _, err := st.AddTask(defaultUser, board.Task{Title: "fail"}); err != nil {
					t.Fatal(err)
				}
				db, err := sql.Open("sqlite", filepath.Join(dir, dbFile))
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				if _, err := db.Exec(`CREATE TRIGGER refuse_backfill BEFORE UPDATE ON tasks WHEN OLD.title = 'fail' BEGIN SELECT RAISE(FAIL, 'backfill refused'); END`); err != nil {
					t.Fatal(err)
				}
			}
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			opened, err := OpenLocalStore(dir, &stderr)
			if err != nil {
				t.Fatal(err)
			}
			if err := opened.Close(); err != nil {
				t.Fatal(err)
			}
			want := "kb: warning: project backfill changed labels on 1 task(s)\n"
			if strings.Count(stderr.String(), want) != 1 {
				t.Fatalf("stderr = %q, want one %q", stderr.String(), want)
			}
			if partialFailure {
				if !strings.Contains(stderr.String(), "backfill refused") {
					t.Fatalf("missing failure warning: %q", stderr.String())
				}
				return
			}
			stderr.Reset()
			opened, err = OpenLocalStore(dir, &stderr)
			if err != nil {
				t.Fatal(err)
			}
			defer opened.Close()
			if stderr.Len() != 0 {
				t.Fatalf("clean board stderr = %q", stderr.String())
			}
		})
	}
}
