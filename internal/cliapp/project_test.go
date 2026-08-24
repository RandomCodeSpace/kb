package cliapp

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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

// noProjectEnv is localEnv without the ambient KB_PROJECT: the state a user
// upgrading into mandatory projects starts from.
func noProjectEnv(t *testing.T) string {
	t.Helper()
	dir := localEnv(t)
	t.Setenv("KB_PROJECT", "")
	return dir
}

func TestProjectAddRefusesWithoutAProject(t *testing.T) {
	dir := noProjectEnv(t)
	_, stderr, code := runCmd(t, "add", "Orphan", "--data", dir)
	if code != 2 {
		t.Fatalf("add without a project: code=%d stderr=%q", code, stderr)
	}
	for _, want := range []string{"no project set", `kb project use <name>`, "-p <name>", "KB_PROJECT"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("refusal %q does not name %q", stderr, want)
		}
	}
	if tasks := listJSON(t, "--data", dir); len(tasks) != 0 {
		t.Fatalf("refused add still wrote %+v", tasks)
	}
}

func TestProjectResolutionOrder(t *testing.T) {
	dir := noProjectEnv(t)
	if out, stderr, code := runCmd(t, "project", "use", "stored", "--data", dir); code != 0 || out != "active project: stored\n" {
		t.Fatalf("project use: code=%d out=%q stderr=%q", code, out, stderr)
	}

	// Stored only.
	assertCurrent(t, dir, "stored", "stored")
	if _, stderr, code := runCmd(t, "add", "From store", "--data", dir); code != 0 {
		t.Fatalf("add: code=%d stderr=%q", code, stderr)
	}

	// KB_PROJECT beats the stored active project.
	t.Setenv("KB_PROJECT", "fromenv")
	assertCurrent(t, dir, "fromenv", "env")
	if _, stderr, code := runCmd(t, "add", "From env", "--data", dir); code != 0 {
		t.Fatalf("add: code=%d stderr=%q", code, stderr)
	}

	// -p beats both, and --project is the same flag.
	assertCurrent(t, dir, "fromflag", "flag", "-p", "fromflag")
	assertCurrent(t, dir, "fromflag", "flag", "--project", "fromflag")
	if _, stderr, code := runCmd(t, "add", "From flag", "--data", dir, "-p", "fromflag"); code != 0 {
		t.Fatalf("add: code=%d stderr=%q", code, stderr)
	}
	assertOneProject(t, listJSON(t, "--data", dir), "stored", "fromenv", "fromflag")
}

// assertCurrent checks what kb project current resolves to and where from.
func assertCurrent(t *testing.T, dir, wantProject, wantSource string, extra ...string) {
	t.Helper()
	args := append([]string{"project", "current", "--json", "--data", dir}, extra...)
	out, stderr, code := runCmd(t, args...)
	if code != 0 {
		t.Fatalf("project current: code=%d stderr=%q", code, stderr)
	}
	var got projectCurrentJSON
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("project current --json: %v\n%s", err, out)
	}
	if got.Project != wantProject || got.Source != wantSource {
		t.Errorf("project current = %+v, want %s from %s", got, wantProject, wantSource)
	}
	plain, _, code := runCmd(t, append([]string{"project", "current", "--data", dir}, extra...)...)
	if code != 0 || plain != wantProject+"\n" {
		t.Errorf("project current plain = %q (code %d), want %q", plain, code, wantProject+"\n")
	}
}

func TestProjectCurrentRefusesWithoutAProject(t *testing.T) {
	dir := noProjectEnv(t)
	if _, stderr, code := runCmd(t, "project", "current", "--data", dir); code != 1 || !strings.Contains(stderr, "no project set") {
		t.Fatalf("project current: code=%d stderr=%q", code, stderr)
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

	// KB_PROJECT loses to the spelled-out label; an explicit -p that
	// contradicts it is a refusal rather than a silent winner.
	t.Setenv("KB_PROJECT", "other")
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
	dir := localEnv(t) // KB_PROJECT=inbox
	if _, stderr, code := runCmd(t, "add", "Task", "--data", dir, "--tag", "keep"); code != 0 {
		t.Fatalf("add: code=%d stderr=%q", code, stderr)
	}

	// Replacing the labels wholesale keeps the task in its project instead of
	// dropping the label or re-filing the task under whatever is active.
	t.Setenv("KB_PROJECT", "elsewhere")
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
	t.Setenv("KB_PROJECT", "")
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

func TestProjectListCountsAndMarksActive(t *testing.T) {
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
	if _, stderr, code := runCmd(t, "project", "use", "api", "--data", dir); code != 0 {
		t.Fatalf("project use: code=%d stderr=%q", code, stderr)
	}
	out, stderr, code := runCmd(t, "project", "list", "--data", dir)
	if code != 0 {
		t.Fatalf("project list: code=%d stderr=%q", code, stderr)
	}
	want := "PROJECT  TASKS  ACTIVE\n" +
		"api      1      *\n" +
		"web      2      -\n"
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
	if len(rows) != 2 || rows[0] != (projectCountJSON{Project: "api", Tasks: 1, Active: true}) ||
		rows[1] != (projectCountJSON{Project: "web", Tasks: 2}) {
		t.Errorf("project list --json = %+v", rows)
	}
}

func TestProjectListShowsEmptyActiveProject(t *testing.T) {
	dir := noProjectEnv(t)
	if _, _, code := runCmd(t, "project", "use", "fresh", "--data", dir); code != 0 {
		t.Fatal("project use failed")
	}
	out, _, code := runCmd(t, "project", "list", "--data", dir)
	want := "PROJECT  TASKS  ACTIVE\nfresh    0      *\n"
	if code != 0 || out != want {
		t.Errorf("project list = %q (code %d), want %q", out, code, want)
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
	if _, stderr, code := runCmd(t, "project", "use", "bad name", "--data", dir); code != 2 ||
		!strings.Contains(stderr, "must not contain whitespace") {
		t.Fatalf("project use bad name: code=%d stderr=%q", code, stderr)
	}
	if _, stderr, code := runCmd(t, "add", "Task", "--data", dir, "-p", "a::b"); code != 2 ||
		!strings.Contains(stderr, `must not contain "::"`) {
		t.Fatalf("add -p a::b: code=%d stderr=%q", code, stderr)
	}
	if _, stderr, code := runCmd(t, "add", "Task", "--data", dir, "--tag", "project::"); code != 2 ||
		!strings.Contains(stderr, "must not be empty") {
		t.Fatalf("add --tag project:: : code=%d stderr=%q", code, stderr)
	}
	t.Setenv("KB_PROJECT", "bad name")
	if _, stderr, code := runCmd(t, "add", "Task", "--data", dir); code != 2 ||
		!strings.Contains(stderr, "KB_PROJECT:") {
		t.Fatalf("add with bad KB_PROJECT: code=%d stderr=%q", code, stderr)
	}
	if _, stderr, code := runCmd(t, "project", "current", "--data", dir); code != 1 ||
		!strings.Contains(stderr, "KB_PROJECT:") {
		t.Fatalf("current with bad KB_PROJECT: code=%d stderr=%q", code, stderr)
	}
	if _, stderr, code := runCmd(t, "project", "list", "--data", dir); code != 1 ||
		!strings.Contains(stderr, "KB_PROJECT:") {
		t.Fatalf("list with bad KB_PROJECT: code=%d stderr=%q", code, stderr)
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
		{[]string{"project", "use"}, 2, "exactly one <name>"},
		{[]string{"project", "use", "a", "b"}, 2, "exactly one <name>"},
		{[]string{"project", "use", "--nope"}, 2, "flag provided but not defined"},
		{[]string{"project", "current", "extra"}, 2, "takes no arguments"},
		{[]string{"project", "current", "--nope"}, 2, "flag provided but not defined"},
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
	out, _, code = runCmd(t, "project", "use", "-h")
	if code != 0 || !strings.Contains(out, "usage: kb") {
		t.Errorf("project use -h = %q (code %d)", out, code)
	}
}

func TestCLIStateRoundTripAndFailures(t *testing.T) {
	dir := t.TempDir()
	if state, err := loadCLIState(dir); err != nil || state.ActiveProject != "" {
		t.Fatalf("missing state = %+v, %v", state, err)
	}
	if err := saveCLIState(filepath.Join(dir, "nested"), cliState{ActiveProject: "web"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	state, err := loadCLIState(filepath.Join(dir, "nested"))
	if err != nil || state.ActiveProject != "web" {
		t.Fatalf("round trip = %+v, %v", state, err)
	}
	if err := os.WriteFile(filepath.Join(dir, cliStateFile), []byte("{"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := loadCLIState(dir); err == nil || !strings.Contains(err.Error(), "decode cli state") {
		t.Fatalf("corrupt state err = %v", err)
	}
	// Root ignores the mode bits, so the permission branches only assert
	// where they can actually be provoked.
	if err := os.Chmod(filepath.Join(dir, cliStateFile), 0); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if _, err := loadCLIState(dir); err == nil && os.Geteuid() != 0 {
		t.Error("unreadable state file loaded without error")
	} else if err != nil && !strings.Contains(err.Error(), "read cli state") {
		t.Errorf("unreadable state err = %v", err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if err := saveCLIState(dir, cliState{ActiveProject: "web"}); err == nil && os.Geteuid() != 0 {
		t.Error("save into a read-only directory succeeded")
	}
}

func TestProjectStateFollowsDataDir(t *testing.T) {
	dir := noProjectEnv(t)
	t.Setenv("KB_DATA", dir)
	if _, _, code := runCmd(t, "project", "use", "viaenv"); code != 0 {
		t.Fatal("project use without --data failed")
	}
	if _, err := os.Stat(filepath.Join(dir, cliStateFile)); err != nil {
		t.Fatalf("state file not written to KB_DATA: %v", err)
	}
	assertCurrent(t, dir, "viaenv", "stored")
	// The state file is per data directory, like the board it sits beside.
	other := t.TempDir()
	if _, stderr, code := runCmd(t, "project", "current", "--data", other); code != 1 ||
		!strings.Contains(stderr, "no project set") {
		t.Fatalf("other data dir: code=%d stderr=%q", code, stderr)
	}
}

func TestProjectRemoteModeUsesTheSameResolution(t *testing.T) {
	remoteEnv(t) // KB_PROJECT=inbox
	if _, stderr, code := runCmd(t, "add", "Remote", "-p", "web"); code != 0 {
		t.Fatalf("remote add: code=%d stderr=%q", code, stderr)
	}
	assertOneProject(t, listJSON(t), "web")
	if _, stderr, code := runCmd(t, "update", "1", "-p", "api"); code != 0 {
		t.Fatalf("remote update: code=%d stderr=%q", code, stderr)
	}
	assertOneProject(t, listJSON(t), "api")
	out, _, code := runCmd(t, "project", "list")
	if code != 0 || !strings.Contains(out, "api      1") {
		t.Errorf("remote project list = %q (code %d)", out, code)
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

func TestProjectCommandsReportUnreadableState(t *testing.T) {
	dir := noProjectEnv(t)
	if err := os.WriteFile(filepath.Join(dir, cliStateFile), []byte("not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	for _, args := range [][]string{
		{"project", "use", "web"},
		{"project", "current"},
		{"project", "list"},
		{"add", "Task"},
	} {
		_, stderr, code := runCmd(t, append(args, "--data", dir)...)
		if code == 0 || !strings.Contains(stderr, "decode cli state") {
			t.Errorf("%v: code=%d stderr=%q, want a decode failure", args, code, stderr)
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

// stubTempFile is a stateTempFile whose write, sync, and close can each be
// made to fail.
type stubTempFile struct {
	name                        string
	writeErr, syncErr, closeErr error
	closed                      bool
}

func (f *stubTempFile) Write(p []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	return len(p), nil
}
func (f *stubTempFile) Name() string { return f.name }
func (f *stubTempFile) Sync() error  { return f.syncErr }
func (f *stubTempFile) Close() error { f.closed = true; return f.closeErr }

// stubStateOps builds ops around one stub temp file, defaulting every
// filesystem call to success.
func stubStateOps(file *stubTempFile, removed *bool) stateFileOps {
	return stateFileOps{
		mkdirAll:  func(string, os.FileMode) error { return nil },
		createTmp: func(string, string) (stateTempFile, error) { return file, nil },
		rename:    func(string, string) error { return nil },
		remove:    func(string) error { *removed = true; return nil },
	}
}

func TestSaveCLIStateReportsEveryWriteFailure(t *testing.T) {
	boom := errors.New("boom")
	cases := []struct {
		name string
		ops  func(*stubTempFile, *bool) stateFileOps
		file *stubTempFile
		want string
	}{
		{"mkdir", func(f *stubTempFile, r *bool) stateFileOps {
			ops := stubStateOps(f, r)
			ops.mkdirAll = func(string, os.FileMode) error { return boom }
			return ops
		}, &stubTempFile{}, "create data dir"},
		{"createTemp", func(f *stubTempFile, r *bool) stateFileOps {
			ops := stubStateOps(f, r)
			ops.createTmp = func(string, string) (stateTempFile, error) { return nil, boom }
			return ops
		}, &stubTempFile{}, "create cli state temp file"},
		{"write", stubStateOps, &stubTempFile{writeErr: boom}, "write cli state"},
		{"sync", stubStateOps, &stubTempFile{syncErr: boom}, "sync cli state"},
		{"close", stubStateOps, &stubTempFile{closeErr: boom}, "close cli state"},
		{"rename", func(f *stubTempFile, r *bool) stateFileOps {
			ops := stubStateOps(f, r)
			ops.rename = func(string, string) error { return boom }
			return ops
		}, &stubTempFile{}, "publish cli state"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			removed := false
			err := saveCLIStateWithOps(t.TempDir(), cliState{ActiveProject: "web"}, tc.ops(tc.file, &removed))
			if err == nil || !strings.Contains(err.Error(), tc.want) || !errors.Is(err, boom) {
				t.Fatalf("err = %v, want %q wrapping boom", err, tc.want)
			}
			// Everything past the temp file's creation must clean it up.
			if tc.name != "mkdir" && tc.name != "createTemp" && !removed {
				t.Error("failed save left the temp file behind")
			}
		})
	}
	// The happy path publishes and leaves no temp file to remove.
	removed := false
	file := &stubTempFile{name: "tmp"}
	if err := saveCLIStateWithOps(t.TempDir(), cliState{ActiveProject: "web"}, stubStateOps(file, &removed)); err != nil {
		t.Fatalf("save: %v", err)
	}
	if removed || !file.closed {
		t.Errorf("published save removed=%v closed=%v", removed, file.closed)
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
	for _, args := range [][]string{
		{"project", "use", "web"},
		{"project", "current"},
		{"project", "list"},
		{"add", "Task"},
	} {
		_, stderr, code := runCmd(t, args...)
		if code == 0 || !strings.Contains(stderr, "cannot determine home directory") {
			t.Errorf("%v: code=%d stderr=%q, want a data-directory failure", args, code, stderr)
		}
	}
}

func TestProjectUseReportsAnUnwritableDataDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := noProjectEnv(t)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if _, stderr, code := runCmd(t, "project", "use", "web", "--data", dir); code != 1 ||
		!strings.Contains(stderr, "cli state") {
		t.Fatalf("project use into a read-only dir: code=%d stderr=%q", code, stderr)
	}
}

func TestProjectCurrentPropagatesWriterFailure(t *testing.T) {
	dir := localEnv(t)
	want := errors.New("output unavailable")
	var stderr strings.Builder
	code := Run([]string{"project", "current", "--json", "--data", dir}, coverageFailWriter{err: want}, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), want.Error()) {
		t.Fatalf("project current writer failure: code=%d stderr=%q", code, stderr.String())
	}
}

func TestProjectListPropagatesBackendFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	t.Setenv("KB_SERVER", srv.URL)
	t.Setenv("KB_SERVER_TOKEN", "")
	t.Setenv("KB_PROJECT", inboxProject)
	if _, stderr, code := runCmd(t, "project", "list"); code != 1 || stderr == "" {
		t.Fatalf("project list against a failing server: code=%d stderr=%q", code, stderr)
	}
}

func TestProjectUpdateReportsUnknownTask(t *testing.T) {
	dir := localEnv(t)
	if _, stderr, code := runCmd(t, "update", "9", "--data", dir, "-p", "web"); code != 1 ||
		!strings.Contains(stderr, "no task matches id") {
		t.Fatalf("update unknown: code=%d stderr=%q", code, stderr)
	}
}
