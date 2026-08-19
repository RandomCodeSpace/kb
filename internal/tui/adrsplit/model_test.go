package adrsplit

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/ai"
	"github.com/RandomCodeSpace/kb/internal/board"
)

type runnerCall struct {
	ctx                context.Context
	user, skill, input string
	scope              ai.Scope
	maxCards           int
	maxTokens          int64
}

type fakeRunner struct {
	run   ai.RunResult
	err   error
	calls []runnerCall
}

func (r *fakeRunner) RunSkill(ctx context.Context, user string, scope ai.Scope, skill, input string, maxCards int, maxTokens int64) (ai.RunResult, error) {
	r.calls = append(r.calls, runnerCall{ctx: ctx, user: user, scope: scope, skill: skill, input: input, maxCards: maxCards, maxTokens: maxTokens})
	return r.run, r.err
}

type fakeStore struct {
	calls []board.Task
	errs  map[string]error
}

func (s *fakeStore) AddTask(_ string, task board.Task) (board.Task, error) {
	s.calls = append(s.calls, task)
	if err := s.errs[task.Title]; err != nil {
		return board.Task{}, err
	}
	task.ID = "id-" + task.Title
	return task, nil
}

func testDraft(title string) ai.Draft {
	return ai.Draft{
		Title: title, Emoji: "🧭", Desc: "desc", Prio: 2, Due: "2026-08-31", Effort: "M",
		Tags: []string{"tui"}, Checks: []ai.DraftCheck{{Text: "test"}, {Text: "ship", Done: true}},
	}
}

func newTestModel() (*Model, *fakeStore, *fakeRunner) {
	st := &fakeStore{errs: make(map[string]error)}
	runner := &fakeRunner{run: ai.RunResult{Cards: []ai.Draft{testDraft("one"), testDraft("two")}}}
	m := New(st, runner, "alice", context.Background())
	m.Open()
	return &m, st, runner
}

// commandMsg runs a command and returns the overlay message it produced. An
// operation that also starts the busy spinner returns a batch, so the batch is
// walked and the spinner tick - which is a timer, not a result - is skipped.
func commandMsg(t *testing.T, command tea.Cmd) tea.Msg {
	t.Helper()
	if command == nil {
		t.Fatal("command is nil")
	}
	message := command()
	batch, batched := message.(tea.BatchMsg)
	if !batched {
		return message
	}
	for _, sub := range batch {
		if sub == nil {
			continue
		}
		if result := sub(); !isSpinnerTick(result) {
			return result
		}
	}
	t.Fatal("batch produced no overlay message")
	return nil
}

func isSpinnerTick(message tea.Msg) bool {
	_, tick := message.(spinner.TickMsg)
	return tick
}

func TestAvailabilitySessionsAndCloseLifecycle(t *testing.T) {
	disabled := New(nil, nil, "u", nil)
	if disabled.Enabled() || disabled.IsOpen() || disabled.Open() != nil || disabled.ConsumeChanged() {
		t.Fatal("nil dependencies should keep overlay disabled")
	}
	if disabled.Update(tea.KeyPressMsg{Code: 'x'}) != nil || IsMessage("plain") {
		t.Fatal("closed model or unrelated message was handled")
	}
	if !IsMessage(fileLoadedMsg{}) || !IsMessage(splitCompletedMsg{}) || !IsMessage(cardAddedMsg{}) || !IsMessage(pointerActionMsg{}) {
		t.Fatal("async message classifier missed a message")
	}

	m, _, _ := newTestModel()
	firstSession := m.session
	if !m.Enabled() || !m.IsOpen() || m.stage != stageInput || m.source != sourcePaste || m.max != defaultMax || m.dest != board.StatusTodo {
		t.Fatalf("open state = %+v", m)
	}
	m.changed = true
	if !m.ConsumeChanged() || m.ConsumeChanged() {
		t.Fatal("changed acknowledgement was not exactly once")
	}
	m.adding, m.changed = true, true
	if m.ConsumeChanged() {
		t.Fatal("batch exposed refresh before completion")
	}
	m.adding = false
	m.Close()
	if m.IsOpen() || m.session <= firstSession {
		t.Fatalf("close state open=%v session=%d", m.open, m.session)
	}
	m.Close()
}

func TestPasteSplitRunsReadOnlySharedSkillAndBuildsReview(t *testing.T) {
	m, _, runner := newTestModel()
	m.adr.SetValue("# ADR\n\nChoose the boring thing.")
	m.max = 12
	command := m.startSplit()
	if m.operation != "splitting ADR" || !strings.Contains(m.status, "splitting") {
		t.Fatalf("progress = operation:%q status:%q", m.operation, m.status)
	}
	message := commandMsg(t, command)
	if len(runner.calls) != 1 {
		t.Fatalf("runner calls = %d", len(runner.calls))
	}
	call := runner.calls[0]
	if call.user != "alice" || call.scope != ai.ScopeReadOnly || call.skill != "adr-split" || call.input != m.adr.Value() || call.maxCards != 12 || call.maxTokens != splitMaxTokens {
		t.Fatalf("runner call = %+v", call)
	}
	if next := m.Update(message); next != nil || m.stage != stageReview || len(m.rows) != 2 || m.focus != "include:0" {
		t.Fatalf("review state rows=%d focus=%q stage=%d next=%v", len(m.rows), m.focus, m.stage, next)
	}
	if !errors.Is(call.ctx.Err(), context.Canceled) {
		t.Fatalf("completed split context = %v", call.ctx.Err())
	}
	if !m.rows[0].include || m.rows[0].title.Value() != "one" || m.rows[0].prio != 2 || m.rows[0].effort != "M" {
		t.Fatalf("first row = %+v", m.rows[0])
	}

	runner.run.Partial = true
	m.stage, m.source = stageInput, sourcePaste
	m.adr.SetValue("# ADR")
	m.Update(commandMsg(t, m.startSplit()))
	if !strings.Contains(m.status, "partial") {
		t.Fatalf("partial status = %q", m.status)
	}
}

func TestSplitValidationErrorsAndStaleCompletions(t *testing.T) {
	m, _, runner := newTestModel()
	if command := m.startSplit(); command != nil || !m.statusIsError || !strings.Contains(m.status, "paste") {
		t.Fatalf("empty paste = command:%v status:%q", command, m.status)
	}
	m.adr.SetValue(strings.Repeat("x", maxADRBytes+1))
	if command := m.startSplit(); command != nil || !errors.Is(errADRTooLarge, errADRTooLarge) || !strings.Contains(m.status, "64 KiB") {
		t.Fatalf("oversize paste = command:%v status:%q", command, m.status)
	}
	m.runner = nil
	m.adr.SetValue("# ADR")
	if command := m.startSplit(); command != nil || !strings.Contains(m.status, "unavailable") {
		t.Fatalf("runner unavailable = command:%v status:%q", command, m.status)
	}
	m.runner = runner

	runner.err = &ai.Error{Code: http.StatusBadGateway, Message: "upstream refused", Cause: errors.New("secret detail")}
	message := commandMsg(t, m.startSplit())
	m.Update(message)
	if m.status != "upstream refused" || !m.statusIsError || strings.Contains(m.status, "secret") {
		t.Fatalf("safe runner error = %q", m.status)
	}
	runner.err = nil
	runner.run.Cards = nil
	m.Update(commandMsg(t, m.startSplit()))
	if !strings.Contains(m.status, "no usable stories") {
		t.Fatalf("empty run status = %q", m.status)
	}

	m.operation = "splitting ADR"
	m.generation = 10
	if command := m.Update(splitCompletedMsg{session: m.session + 1, generation: 10, run: ai.RunResult{Cards: []ai.Draft{testDraft("stale")}}}); command != nil || len(m.rows) != 0 {
		t.Fatal("stale session completion mutated overlay")
	}
	if command := m.Update(splitCompletedMsg{session: m.session, generation: 9, run: ai.RunResult{Cards: []ai.Draft{testDraft("stale")}}}); command != nil || len(m.rows) != 0 {
		t.Fatal("stale generation completion mutated overlay")
	}
}

func TestCancellationPreservesSourceAndScopesLateResult(t *testing.T) {
	m, _, runner := newTestModel()
	m.adr.SetValue("# ADR")
	command := m.startSplit()
	ctx := m.cancel
	_ = ctx
	generation := m.generation
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.operation != "" || !strings.Contains(m.status, "preserved") || m.generation == generation {
		t.Fatalf("cancel state operation=%q status=%q gen=%d", m.operation, m.status, m.generation)
	}
	message := commandMsg(t, command)
	if !errors.Is(runner.calls[len(runner.calls)-1].ctx.Err(), context.Canceled) {
		t.Fatal("runner context was not cancelled")
	}
	m.Update(message)
	if m.stage != stageInput {
		t.Fatal("late cancelled result changed stage")
	}

	m.operation = "reading file"
	m.cancel = func() {}
	m.Update(tea.KeyPressMsg{Code: 'x'})
	if m.operation != "reading file" {
		t.Fatal("non-Escape input interrupted operation")
	}
}

func TestFileModeReadsBoundedUTF8ThenRunsSplit(t *testing.T) {
	m, _, runner := newTestModel()
	dir := t.TempDir()
	path := filepath.Join(dir, "adr.md")
	if err := os.WriteFile(path, []byte("# ADR\nUse SQLite."), 0o600); err != nil {
		t.Fatal(err)
	}
	m.source, m.focus = sourceFile, "file"
	m.filePath.SetValue(path)
	read := m.startSplit()
	if m.operation != "reading file" {
		t.Fatalf("file progress = %q", m.operation)
	}
	run := m.Update(commandMsg(t, read))
	if m.operation != "splitting ADR" || run == nil {
		t.Fatalf("post-read operation=%q command=%v", m.operation, run)
	}
	m.Update(commandMsg(t, run))
	if runner.calls[len(runner.calls)-1].input != "# ADR\nUse SQLite." || m.stage != stageReview {
		t.Fatalf("file split input=%q stage=%d", runner.calls[len(runner.calls)-1].input, m.stage)
	}

	m.Open()
	m.source = sourceFile
	if command := m.startSplit(); command != nil || !strings.Contains(m.status, "path required") {
		t.Fatalf("empty path = command:%v status:%q", command, m.status)
	}
	m.filePath.SetValue(filepath.Join(dir, "missing.md"))
	m.Update(commandMsg(t, m.startSplit()))
	if !m.statusIsError || !strings.Contains(m.status, "read ADR file") {
		t.Fatalf("missing file error = %q", m.status)
	}

	for name, data := range map[string]struct {
		data []byte
		want string
	}{
		"large": {data: []byte(strings.Repeat("x", maxADRBytes+1)), want: "64 KiB"},
		"bad":   {data: []byte{0xff, 0xfe}, want: "UTF-8"},
		"empty": {data: []byte(" \n"), want: "empty"},
	} {
		t.Run(name, func(t *testing.T) {
			file := filepath.Join(dir, name)
			if err := os.WriteFile(file, data.data, 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := readADRFile(context.Background(), file)
			if err == nil || !strings.Contains(err.Error(), data.want) {
				t.Fatalf("error = %v, want %q", err, data.want)
			}
		})
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readADRFile(cancelled, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read = %v", err)
	}
	if _, err := readADRFile(context.Background(), dir); err == nil {
		t.Fatal("reading directory should fail")
	}
}

func TestReviewEditsAndSequentialBatchReportsEveryFailure(t *testing.T) {
	m, st, _ := newTestModel()
	m.stage = stageReview
	m.rows = rowsFromDrafts([]ai.Draft{testDraft("one"), testDraft("two"), testDraft("three")})
	m.focus = "include:0"
	m.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	if m.rows[0].include {
		t.Fatalf("include toggle did not clear; key=%q parse=%v", tea.KeyPressMsg{Code: tea.KeySpace}.String(), m.focus)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	m.focus = "title:0"
	m.rows[0].title.SetValue("renamed")
	m.applyFocus()
	m.Update(tea.KeyPressMsg{Code: '!', Text: "!"})
	if !strings.Contains(m.rows[0].title.Value(), "!") {
		t.Fatalf("title edit = %q", m.rows[0].title.Value())
	}
	m.focus = "prio:0"
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m.focus = "effort:0"
	m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	m.focus = "dest"
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	if m.rows[0].prio != 3 || m.rows[0].effort != "S" || m.dest != board.StatusDoing {
		t.Fatalf("review edits prio=%d effort=%q dest=%q", m.rows[0].prio, m.rows[0].effort, m.dest)
	}

	m.rows[1].title.SetValue(" ")
	st.errs["three"] = errors.New("sqlite\x1b[31m\nrefused")
	command := m.startAdd()
	if !m.adding || len(m.addQueue) != 2 || m.failedCount != 1 || command == nil {
		t.Fatalf("batch start adding=%v queue=%v failed=%d", m.adding, m.addQueue, m.failedCount)
	}
	command = m.Update(commandMsg(t, command))
	if command == nil || !m.adding || m.ConsumeChanged() {
		t.Fatal("batch did not continue sequentially or refreshed early")
	}
	command = m.Update(commandMsg(t, command))
	if command != nil || m.adding || m.createdCount != 1 || m.failedCount != 2 || !m.ConsumeChanged() || m.ConsumeChanged() {
		t.Fatalf("batch finish command=%v adding=%v created=%d failed=%d status=%q", command, m.adding, m.createdCount, m.failedCount, m.status)
	}
	if len(st.calls) != 2 || st.calls[0].Status != board.StatusDoing || st.calls[0].Prio != 3 || st.calls[0].Effort != "S" || len(st.calls[0].Checks) != 2 {
		t.Fatalf("store calls = %+v", st.calls)
	}
	if !m.rows[0].created || m.rows[0].include || m.rows[2].created || !m.rows[2].include || strings.Contains(m.rows[2].err, "\x1b") || strings.Contains(m.rows[2].err, "\n") {
		t.Fatalf("row results first=%+v third=%+v", m.rows[0], m.rows[2])
	}

	m.rows[2].include = false
	m.rows[1].include = false
	if command := m.startAdd(); command != nil || !strings.Contains(m.status, "select") {
		t.Fatalf("empty selection command=%v status=%q", command, m.status)
	}
	m.rows[1].include = true
	if command := m.startAdd(); command != nil || !strings.Contains(m.status, "no valid") {
		t.Fatalf("blank-only selection command=%v status=%q", command, m.status)
	}
}

func TestAllSuccessBatchAndStaleWriteMessages(t *testing.T) {
	m, _, _ := newTestModel()
	m.stage = stageReview
	m.rows = rowsFromDrafts([]ai.Draft{testDraft("one")})
	command := m.startAdd()
	stale := cardAddedMsg{session: m.session + 1, generation: m.addGeneration, row: 0}
	if got := m.Update(stale); got != nil || !m.adding {
		t.Fatal("stale session write changed batch")
	}
	stale.session, stale.generation = m.session, m.addGeneration+1
	m.Update(stale)
	stale.generation, stale.row = m.addGeneration, 99
	m.Update(stale)
	m.Update(commandMsg(t, command))
	if m.status != "created 1 cards" || m.statusIsError || !m.rows[0].created {
		t.Fatalf("success status=%q error=%v row=%+v", m.status, m.statusIsError, m.rows[0])
	}
}

func TestKeyboardNavigationCloseGuardAndBackPreserveSource(t *testing.T) {
	m, _, _ := newTestModel()
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	if m.source != sourceFile || m.focus != "file" {
		t.Fatalf("source toggle source=%d focus=%q", m.source, m.focus)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.focus != "max" {
		t.Fatalf("tab focus = %q", m.focus)
	}
	m.max = 1
	m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	if m.max != 1 {
		t.Fatalf("max lower clamp = %d", m.max)
	}
	m.max = maxStories
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	if m.max != maxStories {
		t.Fatalf("max upper clamp = %d", m.max)
	}
	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}))
	if m.focus != "file" {
		t.Fatalf("shift-tab focus = %q", m.focus)
	}
	m.filePath.SetValue("decision.md")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !m.guardClose || !m.open {
		t.Fatal("dirty Escape did not guard")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.guardClose == true || !m.open {
		t.Fatal("guard Escape did not stay")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m.Update(tea.KeyPressMsg{Code: 'D'})
	if m.open {
		t.Fatal("confirmed discard did not close")
	}

	m.Open()
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.open {
		t.Fatal("clean Escape did not close")
	}
	m.Open()
	m.stage = stageReview
	m.rows = rowsFromDrafts([]ai.Draft{testDraft("one")})
	m.focus = "back"
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.stage != stageInput || len(m.rows) != 0 || m.focus != "source" || !strings.Contains(m.status, "preserved") {
		t.Fatalf("back state stage=%d rows=%d focus=%q status=%q", m.stage, len(m.rows), m.focus, m.status)
	}
}

func TestReviewKeyboardCyclesAndHelpers(t *testing.T) {
	m, _, _ := newTestModel()
	m.stage = stageReview
	m.rows = rowsFromDrafts([]ai.Draft{testDraft("one")})
	for _, target := range []string{"prio:0", "effort:0", "dest"} {
		m.focus = target
		m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
		m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
		m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	}
	m.focus = "cancel"
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.guardClose {
		t.Fatal("review close did not guard")
	}
	m.guardClose = false
	m.adding = true
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !m.open {
		t.Fatal("Escape closed an active batch")
	}
	m.adding = false

	if index, field, ok := parseRowFocus("title:12"); !ok || index != 12 || field != "title" {
		t.Fatalf("parsed row = %d %q %v", index, field, ok)
	}
	for _, invalid := range []string{"title", "title:nope", "a:b:c"} {
		if _, _, ok := parseRowFocus(invalid); ok {
			t.Fatalf("parsed invalid focus %q", invalid)
		}
	}
	if cycleInt(1, 1, 4, "left") != 4 || cycleInt(4, 1, 4, "right") != 1 || cycleInt(2, 1, 4, "x") != 2 {
		t.Fatal("priority cycling failed")
	}
	if cycleEffort("", "left") != "L" || cycleEffort("L", "right") != "" || cycleEffort("M", "x") != "M" {
		t.Fatal("effort cycling failed")
	}
	if cycleStatus(board.StatusTodo, "left") != board.StatusCancelled || cycleStatus(board.StatusCancelled, "right") != board.StatusTodo || cycleStatus(board.StatusDoing, "x") != board.StatusDoing {
		t.Fatal("status cycling failed")
	}
	if sanitize("ok\x1b[31m\n") != "ok" || safeError(nil) != "" || safeError(errors.New(" \n")) != "operation failed" {
		t.Fatal("sanitization helpers failed")
	}
	long := safeError(errors.New(strings.Repeat("x", 200)))
	if len([]rune(long)) != 180 || !strings.HasSuffix(long, "...") {
		t.Fatalf("bounded error length = %d", len([]rune(long)))
	}

	row := rowsFromDrafts([]ai.Draft{testDraft("one")})[0]
	task := taskFromRow(row, board.StatusDone)
	row.draft.Tags[0] = "changed"
	row.draft.Checks[0].Text = "changed"
	if task.Status != board.StatusDone || !reflect.DeepEqual(task.Tags, []string{"tui"}) || task.Checks[0].Text != "test" {
		t.Fatalf("task conversion aliased draft: %+v", task)
	}
}

func TestKeyboardActionBranchesAndFocusTargets(t *testing.T) {
	m, _, _ := newTestModel()
	if command := m.Update(struct{}{}); command != nil {
		t.Fatal("unknown message returned a command")
	}
	if got := m.focusTargets(); !reflect.DeepEqual(got, []string{"source", "adr", "max", "cancel", "split"}) {
		t.Fatalf("paste targets = %#v", got)
	}
	m.focus = "adr"
	m.applyFocus()
	m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if m.adr.Value() != "x" {
		t.Fatalf("ADR input = %q", m.adr.Value())
	}
	m.source, m.focus = sourceFile, "file"
	m.applyFocus()
	m.Update(tea.KeyPressMsg{Code: 'p', Text: "p"})
	if m.filePath.Value() != "p" || m.inputTarget() != "file" {
		t.Fatalf("file input=%q target=%q", m.filePath.Value(), m.inputTarget())
	}
	if got := m.focusTargets(); !reflect.DeepEqual(got, []string{"source", "file", "max", "cancel", "split"}) {
		t.Fatalf("file targets = %#v", got)
	}

	m.filePath.SetValue("")
	m.adr.SetValue("")
	m.focus = "split"
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !strings.Contains(m.status, "path required") {
		t.Fatalf("split action status = %q", m.status)
	}
	m.focus = "cancel"
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.open {
		t.Fatal("clean cancel action did not close")
	}

	m.Open()
	m.stage = stageReview
	m.rows = rowsFromDrafts([]ai.Draft{testDraft("one"), testDraft("two")})
	m.rows[1].created = true
	targets := m.focusTargets()
	if len(targets) != 8 || targets[0] != "include:0" || targets[len(targets)-1] != "add" {
		t.Fatalf("review targets = %#v", targets)
	}
	m.focus = "unknown"
	m.moveFocus(1)
	if m.focus != "title:0" {
		t.Fatalf("unknown current focus advanced to %q", m.focus)
	}
	m.focus = "add"
	if command := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); command == nil || !m.adding {
		t.Fatalf("add action command=%v adding=%v", command, m.adding)
	}

	if safeError(context.Canceled) != "split cancelled" {
		t.Fatalf("cancelled error = %q", safeError(context.Canceled))
	}
}

func TestPointerControlsAreRenderDerivedAndSessionScoped(t *testing.T) {
	m, _, _ := newTestModel()
	beforeScroll := m.scroll
	handler := m.MouseHandler(80, 24)
	if m.scroll != beforeScroll {
		t.Fatal("building pointer snapshot mutated render state")
	}
	before := m.focus
	line, x := visibleTextPosition(t, m.View(80, 24), "ADR markdown")
	command := pointerRelease(m, handler, x, line)
	if command == nil || m.focus != before {
		t.Fatalf("pointer release mutated model before update: command=%v focus=%q", command, m.focus)
	}
	message := command()
	m.Update(message)
	if m.focus != "adr" {
		t.Fatalf("pointer input activation focus=%q message=%T", m.focus, message)
	}

	m.stage = stageReview
	m.rows = rowsFromDrafts([]ai.Draft{testDraft("one"), testDraft("two")})
	m.focus = "include:0"
	handler = m.MouseHandler(80, 24)
	line, x = visibleTextPosition(t, m.View(80, 24), "1 include")
	message = pointerRelease(m, handler, x, line)()
	if _, ok := message.(pointerActionMsg); !ok {
		t.Fatalf("review release message=%T", message)
	}
	m.Update(message)
	if m.rows[0].include {
		t.Fatal("pointer row activation did not toggle inclusion")
	}

	old := m.MouseHandler(80, 24)
	m.Close()
	m.Open()
	if command := pointerRelease(m, old, x, line); command != nil {
		if m.Update(command()) != nil || m.focus != "source" {
			t.Fatal("stale pointer action changed reopened session")
		}
	}
	if command := m.MouseHandler(80, 24)(tea.MouseMotionMsg{X: x, Y: line}); command != nil {
		t.Fatal("pointer motion produced a model action")
	}
}

func TestPointerControlsClipScrolledRowsAndGuardBusyWork(t *testing.T) {
	m, _, _ := newTestModel()
	m.stage = stageReview
	drafts := make([]ai.Draft, 20)
	for i := range drafts {
		drafts[i] = testDraft(fmt.Sprintf("story-%d", i))
	}
	m.rows = rowsFromDrafts(drafts)
	m.scroll = 0
	view := m.View(50, 10)
	if strings.Contains(ansi.Strip(view), "story-19") {
		t.Fatal("small viewport rendered an offscreen row")
	}
	handler := m.MouseHandler(50, 10)
	line, x := visibleTextPosition(t, view, "1 include")
	if command := pointerRelease(m, handler, x, line); command == nil {
		t.Fatal("visible review control had no hit region")
	}
	if command := pointerRelease(m, handler, 3, 0); command == nil {
		t.Fatal("idle backdrop had no dismiss action")
	}
	m.operation = "splitting ADR"
	if command := pointerRelease(m, handler, 3, 8); command != nil {
		if m.Update(command()) != nil || m.focus != "source" {
			t.Fatal("busy overlay exposed pointer action")
		}
	}
}

func TestPointerWheelReachesShortReviewRowsAndBackdropGuardIsExplicit(t *testing.T) {
	m, _, _ := newTestModel()
	m.stage = stageReview
	drafts := make([]ai.Draft, 20)
	for i := range drafts {
		drafts[i] = testDraft(fmt.Sprintf("story-%d", i))
	}
	m.rows = rowsFromDrafts(drafts)
	m.focus = "include:0"

	for range 32 {
		pointerWheel(t, m, m.MouseHandler(50, 10), 25, 5, tea.MouseWheelDown)
	}
	if m.scroll == 0 || !m.manualScroll || m.focus != "include:0" {
		t.Fatalf("wheel state scroll=%d manual=%v focus=%q", m.scroll, m.manualScroll, m.focus)
	}
	view := m.View(50, 10)
	line, x := visibleTextPosition(t, view, "story-19")
	command := pointerRelease(m, m.MouseHandler(50, 10), x, line)
	if command == nil {
		t.Fatal("scrolled final review row had no pointer action")
	}
	m.Update(command())
	if m.focus != "title:19" {
		t.Fatalf("final row pointer focus=%q", m.focus)
	}

	guard, _, _ := newTestModel()
	guard.adr.SetValue("draft")
	backdrop := pointerRelease(guard, guard.MouseHandler(80, 24), 0, 0)
	if backdrop == nil {
		t.Fatal("idle ADR backdrop had no action")
	}
	guard.Update(backdrop())
	if !guard.open || !guard.guardClose {
		t.Fatal("dirty ADR backdrop did not open the discard guard")
	}
	if got := ansi.Strip(guard.View(80, 24)); !strings.Contains(got, "[ Discard ]") || !strings.Contains(got, "[ Stay ]") {
		t.Fatalf("pointer confirmation controls missing:\n%s", got)
	}
	stay := pointerActivate(t, guard, "[ Stay ]")
	if stay != nil || !guard.open || guard.guardClose {
		t.Fatalf("stay action closed=%v guard=%v command=%v", !guard.open, guard.guardClose, stay)
	}
	guard.Update(pointerRelease(guard, guard.MouseHandler(80, 24), 0, 0)())
	if !guard.guardClose {
		t.Fatal("second backdrop did not reopen discard guard")
	}
	discard := pointerActivate(t, guard, "[ Discard ]")
	if discard != nil || guard.open {
		t.Fatalf("discard action open=%v command=%v", guard.open, discard)
	}
}

func TestPointerControlsActivateEveryVisibleInputAndReviewControl(t *testing.T) {
	closed, _, _ := newTestModel()
	closed.Close()
	if handler := closed.MouseHandler(80, 24); handler != nil {
		t.Fatal("closed overlay exposed a mouse handler")
	}

	input, _, _ := newTestModel()
	pointerActivate(t, input, "Source:")
	if input.source != sourceFile || input.focus != "file" {
		t.Fatalf("source click state = source:%d focus:%q", input.source, input.focus)
	}
	pointerActivate(t, input, "ADR file:")
	if input.focus != "file" {
		t.Fatalf("file click focus = %q", input.focus)
	}
	pointerActivate(t, input, "Source:")
	if input.source != sourcePaste || input.focus != "adr" {
		t.Fatalf("source return state = source:%d focus:%q", input.source, input.focus)
	}
	pointerActivate(t, input, "Max stories:")
	if input.focus != "max" || input.max != defaultMax+1 {
		t.Fatalf("max click focus=%q value=%d", input.focus, input.max)
	}
	pointerActivate(t, input, "Propose stories")
	if !input.statusIsError || !strings.Contains(input.status, "paste an ADR") {
		t.Fatalf("empty pointer split status = %q error=%v", input.status, input.statusIsError)
	}
	input.adr.SetValue("draft")
	pointerActivate(t, input, "Cancel")
	if !input.guardClose {
		t.Fatal("dirty pointer cancel did not open the discard guard")
	}

	review, _, _ := newTestModel()
	review.stage = stageReview
	review.rows = rowsFromDrafts([]ai.Draft{testDraft("one"), testDraft("two")})
	pointerActivate(t, review, "1 include")
	if review.rows[0].include {
		t.Fatal("pointer include did not toggle the selected row")
	}
	pointerActivate(t, review, "Title:")
	if review.focus != "title:0" {
		t.Fatalf("title click focus = %q", review.focus)
	}
	pointerActivate(t, review, "Priority:")
	if review.focus != "prio:0" || review.rows[0].prio != cycleInt(testDraft("one").Prio, 1, 4, "enter") {
		t.Fatalf("priority click focus=%q value=%d", review.focus, review.rows[0].prio)
	}
	pointerActivate(t, review, "Effort:")
	if review.focus != "effort:0" || review.rows[0].effort != cycleEffort(testDraft("one").Effort, "enter") {
		t.Fatalf("effort click focus=%q value=%q", review.focus, review.rows[0].effort)
	}
	pointerActivate(t, review, "Destination:")
	if review.focus != "dest" || review.dest != cycleStatus(board.StatusTodo, "enter") {
		t.Fatalf("destination click focus=%q value=%q", review.focus, review.dest)
	}
	pointerActivate(t, review, "Back to source")
	if review.stage != stageInput || review.focus != "source" || len(review.rows) != 0 {
		t.Fatalf("pointer back state = stage:%d focus:%q rows:%d", review.stage, review.focus, len(review.rows))
	}

	add, store, _ := newTestModel()
	add.stage = stageReview
	add.rows = rowsFromDrafts([]ai.Draft{testDraft("one")})
	command := pointerActivate(t, add, "Add selected (1)")
	if !add.adding || command == nil {
		t.Fatalf("pointer add state = adding:%v command:%v", add.adding, command)
	}
	if next := add.Update(commandMsg(t, command)); next != nil || add.adding || add.createdCount != 1 || len(store.calls) != 1 {
		t.Fatalf("pointer add completion = next:%v adding:%v created:%d calls:%d", next, add.adding, add.createdCount, len(store.calls))
	}
}

func TestPointerPressFeedbackReleasesOnceAndDragCancels(t *testing.T) {
	m, _, _ := newTestModel()
	before := ansi.Strip(m.View(80, 24))
	line, x := visibleTextPosition(t, before, "Max stories:")
	handler := m.MouseHandler(80, 24)
	press := handler(tea.MouseClickMsg{X: x, Y: line, Button: tea.MouseLeft})
	if press == nil || !IsMessage(press()) {
		t.Fatal("max press did not produce an overlay feedback message")
	}
	if command := m.Update(press()); command != nil || !m.pointerState.IsPressed(controlID("max")) {
		t.Fatalf("press state command=%v pressed=%v", command, m.pointerState.IsPressed(controlID("max")))
	}
	pressed := ansi.Strip(m.View(80, 24))
	if !strings.Contains(pressed, "! Max stories:") || ansi.StringWidth(strings.Split(before, "\n")[line]) != ansi.StringWidth(strings.Split(pressed, "\n")[line]) {
		t.Fatalf("max press feedback was not same-width:\n%s", pressed)
	}
	release := m.MouseHandler(80, 24)(tea.MouseReleaseMsg{X: x, Y: line, Button: tea.MouseLeft})
	if release == nil {
		t.Fatal("max release had no action")
	}
	command := m.Update(release())
	if command == nil || m.pointerState.IsPressed(controlID("max")) || m.max != defaultMax {
		t.Fatalf("release feedback command=%v pressed=%v max=%d", command, m.pointerState.IsPressed(controlID("max")), m.max)
	}
	m.Update(command())
	if m.max != defaultMax+1 {
		t.Fatalf("release activation max=%d", m.max)
	}
	if duplicate := handler(tea.MouseReleaseMsg{X: x, Y: line, Button: tea.MouseLeft}); duplicate != nil {
		if next := m.Update(duplicate()); next != nil {
			m.Update(next())
		}
	}
	if m.max != defaultMax+1 {
		t.Fatalf("duplicate release activated max=%d", m.max)
	}

	drag, _, _ := newTestModel()
	line, x = visibleTextPosition(t, ansi.Strip(drag.View(80, 24)), "Max stories:")
	handler = drag.MouseHandler(80, 24)
	drag.Update(handler(tea.MouseClickMsg{X: x, Y: line, Button: tea.MouseLeft})())
	if !drag.pointerState.IsPressed(controlID("max")) {
		t.Fatal("drag setup did not render pressed max")
	}
	cancel := handler(tea.MouseMotionMsg{X: x + 1, Y: line, Button: tea.MouseLeft})
	if cancel == nil || drag.Update(cancel()) != nil || drag.pointerState.IsPressed(controlID("max")) {
		t.Fatal("drag did not cancel pressed feedback")
	}
	if release := handler(tea.MouseReleaseMsg{X: x + 1, Y: line, Button: tea.MouseLeft}); release != nil {
		if next := drag.Update(release()); next != nil {
			drag.Update(next())
		}
	}
	if drag.max != defaultMax {
		t.Fatalf("drag release activated max=%d", drag.max)
	}
}

func TestDirtyCloseButtonActivatesAfterPressedRerender(t *testing.T) {
	m, _, _ := newTestModel()
	m.adr.SetValue("dirty ADR")
	m.requestClose()
	line, x := visibleTextPosition(t, ansi.Strip(m.View(80, 24)), "[ Discard ]")
	press := m.MouseHandler(80, 24)(tea.MouseClickMsg{X: x, Y: line, Button: tea.MouseLeft})
	if press == nil || m.Update(press()) != nil {
		t.Fatal("dirty-close discard did not enter pressed state")
	}
	// The feedback is theme.Styles.Pressed now, not a text substitution, so it
	// is the reverse-video attribute in the composed frame that says "pressed".
	view := m.View(80, 24)
	if !strings.Contains(view, "\x1b[7m") || !strings.Contains(ansi.Strip(view), "[ Discard ]") {
		t.Fatalf("dirty-close discard omitted pressed feedback:\n%s", ansi.Strip(view))
	}
	release := m.MouseHandler(80, 24)(tea.MouseReleaseMsg{X: x, Y: line, Button: tea.MouseNone})
	if release == nil {
		t.Fatal("rerendered dirty-close discard ignored release")
	}
	activate := m.Update(release())
	if activate == nil {
		t.Fatal("dirty-close discard release produced no activation")
	}
	m.Update(activate())
	if m.IsOpen() {
		t.Fatal("dirty-close discard did not close overlay")
	}
}

func TestDirtySourceCannotImpersonateDiscardControl(t *testing.T) {
	m, _, _ := newTestModel()
	m.adr.SetValue("[ Discard ]\nkeep this ADR")
	m.requestClose()
	line, x := visibleTextPosition(t, ansi.Strip(m.View(80, 24)), "[ Discard ]")
	command := pointerRelease(m, m.MouseHandler(80, 24), x, line)
	if command != nil {
		m.Update(commandMsg(t, command))
	}
	if !m.IsOpen() || !m.guardClose {
		t.Fatal("dirty ADR text impersonated the discard footer")
	}
}

func TestCreatedReviewRowsKeepPointerTargetsAligned(t *testing.T) {
	m, _, _ := newTestModel()
	m.stage = stageReview
	m.rows = rowsFromDrafts([]ai.Draft{testDraft("created"), testDraft("pending")})
	m.rows[0].created = true
	for _, row := range m.bodyRows(80) {
		want := ""
		switch {
		case strings.Contains(row.plain(), "Destination:"):
			want = "dest"
		case strings.Contains(row.plain(), "[ Back to source ]"):
			want = "back"
		case strings.Contains(row.plain(), "[ Add selected"):
			want = "add"
		}
		if want != "" && row.target != want {
			t.Fatalf("row %q target=%q want %q", row.plain(), row.target, want)
		}
	}
	// A created story keeps its rows in the list but never a focus target, so
	// the batch cannot be told to write it twice.
	for _, row := range m.bodyRows(80) {
		if strings.Contains(row.plain(), "created") && row.target != "" {
			t.Fatalf("created row %q carries target %q", row.plain(), row.target)
		}
	}
}

func pointerActivate(t *testing.T, model *Model, needle string) tea.Cmd {
	t.Helper()
	view := model.View(100, 30)
	line, x := visibleTextPosition(t, view, needle)
	command := pointerRelease(model, model.MouseHandler(100, 30), x, line)
	if command == nil {
		t.Fatalf("pointer control %q did not produce an action", needle)
	}
	return model.Update(commandMsg(t, command))
}

func visibleTextPosition(t *testing.T, view, needle string) (int, int) {
	t.Helper()
	for y, line := range strings.Split(ansi.Strip(view), "\n") {
		if x := strings.Index(line, needle); x >= 0 {
			return y, x
		}
	}
	t.Fatalf("visible control %q missing:\n%s", needle, ansi.Strip(view))
	return 0, 0
}

func pointerRelease(model *Model, handler func(tea.MouseMsg) tea.Cmd, x, y int) tea.Cmd {
	if command := handler(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}); command != nil {
		model.Update(command())
	}
	command := handler(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	if command == nil {
		return nil
	}
	message := command()
	state, next, handled := model.pointerState.Update(message)
	if handled {
		model.pointerState = state
		return next
	}
	return func() tea.Msg { return message }
}

func pointerWheel(t *testing.T, model *Model, handler func(tea.MouseMsg) tea.Cmd, x, y int, button tea.MouseButton) {
	t.Helper()
	command := handler(tea.MouseWheelMsg{X: x, Y: y, Button: button})
	if command == nil {
		t.Fatal("wheel had no action")
	}
	next := model.Update(command())
	if next == nil {
		t.Fatal("wheel feedback did not forward its domain action")
	}
	model.Update(next())
}
