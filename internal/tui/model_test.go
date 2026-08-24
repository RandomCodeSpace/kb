package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/RandomCodeSpace/kb/internal/ai"
	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/forge"
	"github.com/RandomCodeSpace/kb/internal/store"
	"github.com/RandomCodeSpace/kb/internal/tui/issueimport"
)

type stubBoardReader struct {
	board board.Board
	err   error
}

type stubDetailBoardReader struct{ stubBoardReader }

func (stubDetailBoardReader) Comments(string, string) ([]store.Comment, error) {
	return nil, nil
}

func (stubDetailBoardReader) TaskLinks(string, string) (store.TaskLinks, error) {
	return store.TaskLinks{}, nil
}

func (stubDetailBoardReader) Tombstone(string, string) (store.Tombstone, bool, error) {
	return store.Tombstone{}, false, nil
}

type mutableDetailReader struct {
	board        board.Board
	commentLoads int
}

func (r *mutableDetailReader) Board(string) (board.Board, error) { return r.board, nil }

func (r *mutableDetailReader) Comments(string, string) ([]store.Comment, error) {
	r.commentLoads++
	return []store.Comment{{ID: r.commentLoads, Author: "watcher", Body: fmt.Sprintf("enrichment %d", r.commentLoads)}}, nil
}

func (*mutableDetailReader) TaskLinks(string, string) (store.TaskLinks, error) {
	return store.TaskLinks{}, nil
}

func (*mutableDetailReader) Tombstone(string, string) (store.Tombstone, bool, error) {
	return store.Tombstone{}, false, nil
}

func (s stubBoardReader) Board(string) (board.Board, error) { return s.board, s.err }

type stubVersionReader struct {
	version int64
	err     error
}

type boardResult struct {
	board board.Board
	err   error
}

type sequenceBoardReader struct {
	results []boardResult
	calls   int
}

func (s *sequenceBoardReader) Board(string) (board.Board, error) {
	result := s.results[s.calls]
	s.calls++
	return result.board, result.err
}

type countingVersionReader struct {
	version int64
	calls   int
}

func (s *countingVersionReader) DataVersion(context.Context) (int64, error) {
	s.calls++
	return s.version, nil
}

func (s stubVersionReader) DataVersion(context.Context) (int64, error) {
	return s.version, s.err
}

func updateTestModel(t *testing.T, model *Model, message tea.Msg) tea.Cmd {
	t.Helper()
	updated, command := model.Update(message)
	*model = updated.(Model)
	return command
}

type rootImportStore struct{ added int }

func (s *rootImportStore) AddTask(string, board.Task) (board.Task, error) {
	s.added++
	return board.Task{ID: "created"}, nil
}

type rootImportBackend struct{}

func (rootImportBackend) Sources(string) ([]store.ForgeSource, error) {
	return []store.ForgeSource{{Name: "primary", Kind: "github"}}, nil
}
func (rootImportBackend) Preview(context.Context, string, forge.PreviewRequest) (forge.Preview, error) {
	return forge.Preview{}, nil
}
func (rootImportBackend) CreateTask(string, string, board.Task, forge.LinkInput) (board.Task, error) {
	return board.Task{ID: "created"}, nil
}

type rootDriftBackend struct{}

func (rootDriftBackend) Provenance(string, string) ([]store.ImportLink, error) {
	return []store.ImportLink{{Source: "primary", ExternalKey: "qualified", Link: "github#1", URL: "https://example.test/1", Title: "issue"}}, nil
}
func (rootDriftBackend) CheckDrift(context.Context, string, string, string) (forge.Drift, error) {
	return forge.Drift{State: "drifted", Revision: strings.Repeat("a", 64)}, nil
}
func (rootDriftBackend) AcceptDrift(context.Context, string, string, string, string) (string, error) {
	return "now", nil
}

func boardLoadFromBatch(t *testing.T, command tea.Cmd) tea.Cmd {
	t.Helper()
	if command == nil {
		t.Fatal("load and poll command is nil")
	}
	batch, ok := command().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("load and poll command = %#v, want two-command batch", batch)
	}
	return batch[0]
}

// resultSkippingSpinnerTick runs a command and returns the result message it
// produced. An overlay operation batches its busy-spinner tick alongside the
// work, so the batch is walked and the tick - a timer, not a result - skipped.
func resultSkippingSpinnerTick(t *testing.T, command tea.Cmd) tea.Msg {
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
		if result := sub(); !isSpinnerTickMsg(result) {
			return result
		}
	}
	t.Fatal("batch produced no result message")
	return nil
}

func isSpinnerTickMsg(message tea.Msg) bool {
	_, tick := message.(spinner.TickMsg)
	return tick
}

func TestIssueImportOwnsRootInputAndCancelsLiftOnOpen(t *testing.T) {
	task := board.Task{ID: "one", Title: "One", Status: board.StatusTodo, Prio: 3}
	m := newModel(stubBoardReader{board: board.Board{Title: "Board", Tasks: []board.Task{task}}}, nil, "alice", context.Background())
	m.board = board.Board{Title: "Board", Tasks: []board.Task{task}}
	m.loading = false
	importStore := &rootImportStore{}
	m.issueImport = issueimport.New(importStore, rootImportBackend{}, "alice", context.Background())
	m.move.begin(m.board, task, boardStatuses[:], false)
	command := updateTestModel(t, &m, tea.KeyPressMsg{Code: 'i'})
	if command == nil || m.move.lifted != nil || !m.issueImport.IsOpen() {
		t.Fatalf("import open = cmd %v lifted %v open %t", command, m.move.lifted != nil, m.issueImport.IsOpen())
	}
	updateTestModel(t, &m, command())
	before := m.boardView
	for _, key := range []tea.KeyPressMsg{
		{Code: 't', Text: "t"}, {Code: 'x', Text: "x"}, {Code: 'r', Text: "r"},
		{Code: 'D', Text: "D"}, {Code: tea.KeyDelete}, {Code: tea.KeyBackspace},
	} {
		updateTestModel(t, &m, key)
	}
	for _, message := range []tea.Msg{
		boardCardClickedMsg{taskID: task.ID}, boardColumnClickedMsg{status: board.StatusDoing},
		boardPointerDownMsg{taskID: task.ID}, boardPointerMoveMsg{status: board.StatusDoing}, boardPointerUpMsg{},
	} {
		updateTestModel(t, &m, message)
	}
	if m.boardView != before || m.detail.IsOpen() || m.action.open() || !m.issueImport.IsOpen() {
		t.Fatal("active import leaked board input")
	}
	view := m.View()
	if view.OnMouse == nil || !strings.Contains(ansi.Strip(view.Content), "FORGE ISSUE IMPORT") {
		t.Fatal("active import did not own rendering/mouse")
	}
	updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.issueImport.IsOpen() {
		t.Fatal("escape did not close import")
	}
}

func TestIssueImportCannotOpenDuringMoveWrite(t *testing.T) {
	for _, busy := range []func(*Model){
		func(m *Model) { m.move.saving = true },
		func(m *Model) { m.action.busy = true },
	} {
		m := newModel(stubBoardReader{}, nil, "alice", context.Background())
		m.issueImport = issueimport.New(&rootImportStore{}, rootImportBackend{}, "alice", context.Background())
		busy(&m)
		if command := updateTestModel(t, &m, tea.KeyPressMsg{Code: 'i'}); command != nil || m.issueImport.IsOpen() {
			t.Fatal("import opened during active write")
		}
	}
}

func TestIssueImportPreservesGlobalInterrupt(t *testing.T) {
	m := newModel(stubBoardReader{}, nil, "alice", context.Background())
	m.issueImport = issueimport.New(&rootImportStore{}, rootImportBackend{}, "alice", context.Background())
	if command := updateTestModel(t, &m, tea.KeyPressMsg{Code: 'i'}); command == nil || !m.issueImport.IsOpen() {
		t.Fatal("import did not open")
	}
	quit := updateTestModel(t, &m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if quit == nil || !m.stopped || m.issueImport.IsOpen() {
		t.Fatalf("global interrupt = command:%v stopped:%t open:%t", quit, m.stopped, m.issueImport.IsOpen())
	}
}

func TestDriftReviewBlocksTaskActionsAndBoardMouse(t *testing.T) {
	task := board.Task{ID: "one", Title: "One", Status: board.StatusTodo, Prio: 3, Tags: []string{"link::github#1"}}
	m := newModel(stubBoardReader{board: board.Board{Title: "Board", Tasks: []board.Task{task}}}, nil, "alice", context.Background())
	m.board = board.Board{Title: "Board", Tasks: []board.Task{task}}
	m.loading = false
	m.detail.SetDriftBackend(rootDriftBackend{}, context.Background())
	drainModelCommands(t, &m, updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEnter}))
	provenance := updateTestModel(t, &m, tea.KeyPressMsg{Code: 'v', Text: "v"})
	if provenance == nil {
		t.Fatal("drift provenance command is nil")
	}
	updateTestModel(t, &m, provenance())
	check := updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if check == nil {
		t.Fatal("drift check command is nil")
	}
	updateTestModel(t, &m, check())
	if !m.detail.OwnsInput() {
		t.Fatal("drift review does not own detail input")
	}
	before := m.boardView
	for _, key := range []tea.KeyPressMsg{
		{Code: 't', Text: "t"}, {Code: 'x', Text: "x"}, {Code: 'r', Text: "r"},
		{Code: 'D', Text: "D"}, {Code: tea.KeyDelete}, {Code: tea.KeyBackspace},
	} {
		updateTestModel(t, &m, key)
	}
	for _, message := range []tea.Msg{
		boardCardClickedMsg{taskID: task.ID}, boardColumnClickedMsg{status: board.StatusDoing},
		boardPointerDownMsg{taskID: task.ID}, boardPointerMoveMsg{status: board.StatusDoing}, boardPointerUpMsg{},
	} {
		updateTestModel(t, &m, message)
	}
	if m.action.open() || m.boardView != before || !m.detail.IsOpen() || !m.detail.OwnsInput() || m.View().OnMouse == nil {
		t.Fatalf("active drift review leaked input: action=%#v boardChanged=%t detailOpen=%t owns=%t mouse=%t",
			m.action, m.boardView != before, m.detail.IsOpen(), m.detail.OwnsInput(), m.View().OnMouse != nil)
	}
}

func TestDriftReviewPreservesGlobalInterrupt(t *testing.T) {
	for _, stage := range []string{"selection", "busy", "review"} {
		t.Run(stage, func(t *testing.T) {
			task := board.Task{ID: "one", Title: "One", Status: board.StatusTodo, Tags: []string{"link::github#1"}}
			m := newModel(stubBoardReader{board: board.Board{Title: "Board", Tasks: []board.Task{task}}}, nil, "alice", context.Background())
			m.board, m.loading = board.Board{Title: "Board", Tasks: []board.Task{task}}, false
			m.detail.SetDriftBackend(rootDriftBackend{}, context.Background())
			drainModelCommands(t, &m, updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEnter}))
			provenance := updateTestModel(t, &m, tea.KeyPressMsg{Code: 'v', Text: "v"})
			if stage != "busy" {
				updateTestModel(t, &m, provenance())
			}
			if stage == "review" {
				updateTestModel(t, &m, updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEnter})())
			}
			quit := updateTestModel(t, &m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
			if quit == nil || !m.stopped {
				t.Fatalf("ctrl+c swallowed at %s: command=%v stopped=%t", stage, quit, m.stopped)
			}
		})
	}
}

func completeBoardLoad(t *testing.T, model *Model, command tea.Cmd) tea.Cmd {
	t.Helper()
	if command == nil {
		t.Fatal("board load command is nil")
	}
	message := command()
	if _, ok := message.(boardLoadedMsg); !ok {
		t.Fatalf("board load command returned %T", message)
	}
	return updateTestModel(t, model, message)
}

func drainModelCommands(t *testing.T, model *Model, commands ...tea.Cmd) {
	t.Helper()
	queue := append([]tea.Cmd(nil), commands...)
	for steps := 0; len(queue) > 0; steps++ {
		if steps > 30 {
			t.Fatal("command drain did not settle")
		}
		command := queue[0]
		queue = queue[1:]
		if command == nil {
			continue
		}
		message := command()
		if batch, ok := message.(tea.BatchMsg); ok {
			queue = append(queue, batch...)
			continue
		}
		if next := updateTestModel(t, model, message); next != nil {
			queue = append(queue, next)
		}
	}
}

func runPoll(t *testing.T, model *Model) tea.Cmd {
	t.Helper()
	read := updateTestModel(t, model, pollTickMsg{})
	if read == nil {
		t.Fatal("poll tick did not start a data_version read")
	}
	return updateTestModel(t, model, read())
}

func TestModelLoadsRoutesAndRenders(t *testing.T) {
	loaded := board.Board{Title: "Work", Tasks: []board.Task{{Title: "one", Status: board.StatusTodo}}}
	m := NewModel(stubBoardReader{board: loaded}, nil, "alice")
	initial := m.Init()
	updated, command := m.Update(initial())
	m = updated.(Model)
	if command != nil || m.loading || m.board.Title != "Work" {
		t.Fatalf("loaded model = %#v, command=%v", m, command)
	}

	updated, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 20})
	m = updated.(Model)
	wide := m.View()
	if !wide.AltScreen || wide.MouseMode != tea.MouseModeCellMotion {
		t.Fatalf("view terminal modes = alt:%v mouse:%v", wide.AltScreen, wide.MouseMode)
	}
	for _, want := range []string{"kb / Work / alice", "▸● 1 TO DO", "one", "DOING", "DONE", "ready"} {
		if !strings.Contains(ansi.Strip(wide.Content), want) {
			t.Errorf("wide view missing %q:\n%s", want, wide.Content)
		}
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m = updated.(Model)
	updated, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 0})
	m = updated.(Model)
	narrow := ansi.Strip(m.View().Content)
	if !strings.Contains(narrow, "▸● 2 DOING") || strings.Contains(narrow, "TO DO") {
		t.Fatalf("narrow focused view:\n%s", narrow)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	m = updated.(Model)
	if m.boardView.column != 0 {
		t.Fatalf("left focus = %d, want 0", m.boardView.column)
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'h'})
	m = updated.(Model)
	if m.boardView.column != 2 {
		t.Fatalf("wrapped focus = %d, want 2", m.boardView.column)
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'l'})
	m = updated.(Model)
	if m.boardView.column != 0 {
		t.Fatalf("l focus = %d, want 0", m.boardView.column)
	}

	_, quit := m.Update(tea.KeyPressMsg{Code: 'q'})
	if quit == nil {
		t.Fatal("q did not return a quit command")
	}
	m.board.Title = "   "
	m.width = 1
	m.height = 1
	for _, line := range strings.Split(m.render(), "\n") {
		if width := ansi.StringWidth(line); width > 1 {
			t.Fatalf("one-column line width = %d: %q", width, line)
		}
	}
}

func TestCardDetailOpensFromKeyboardAndClick(t *testing.T) {
	tasks := []board.Task{
		{ID: "first", Seq: 1, Title: "First card", Desc: "detail-only description", Status: board.StatusTodo},
		{ID: "second", Seq: 2, Title: "Second card", Status: board.StatusTodo},
	}
	m := NewModel(stubDetailBoardReader{stubBoardReader{board: board.Board{Title: "Work", Tasks: tasks}}}, nil, "alice")
	completeBoardLoad(t, &m, m.Init())

	load := updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.detail.IsOpen() || m.detail.TaskID() != "first" || load == nil {
		t.Fatalf("enter detail state = open %v task %q command %v", m.detail.IsOpen(), m.detail.TaskID(), load)
	}
	updateTestModel(t, &m, load())
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "detail-only description") || !strings.Contains(view, " Close ") {
		t.Fatalf("detail overlay missing content:\n%s", view)
	}
	columnBefore := m.boardView.column
	rowBefore := m.boardView.rows[0]
	updateTestModel(t, &m, tea.KeyPressMsg{Code: 'j'})
	updateTestModel(t, &m, boardColumnClickedMsg{status: board.StatusDoing})
	if m.boardView.column != columnBefore || m.boardView.rows[0] != rowBefore {
		t.Fatalf("overlay input leaked to board: %+v", m.boardView)
	}
	updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.detail.IsOpen() {
		t.Fatal("escape did not close detail")
	}

	updateTestModel(t, &m, boardCardClickedMsg{taskID: "second"})
	if !m.detail.IsOpen() || m.detail.TaskID() != "second" || m.boardView.rows[0] != 1 {
		t.Fatalf("click detail state = open %v task %q row %d", m.detail.IsOpen(), m.detail.TaskID(), m.boardView.rows[0])
	}
	if command := m.View().OnMouse(tea.MouseClickMsg{}); command != nil {
		updateTestModel(t, &m, command())
	}
	if !m.detail.IsOpen() || m.boardView.rows[0] != 1 {
		t.Fatal("non-left detail pointer event leaked to the board")
	}
	if quit := updateTestModel(t, &m, tea.KeyPressMsg{Code: 'q'}); quit == nil || !m.stopped {
		t.Fatal("q did not preserve root quit while detail was open")
	}
}

func mouseRoutingTestModel(t *testing.T) Model {
	t.Helper()
	tasks := make([]board.Task, 0, 8)
	for i := range 8 {
		tasks = append(tasks, board.Task{
			ID:     fmt.Sprintf("task-%d", i),
			Seq:    i + 1,
			Title:  fmt.Sprintf("Card %d", i),
			Desc:   strings.Repeat(fmt.Sprintf("detail line %d\n", i), 8),
			Status: board.StatusTodo,
		})
	}
	m := NewModel(stubDetailBoardReader{stubBoardReader{board: board.Board{Title: "Work", Tasks: tasks}}}, nil, "alice")
	m.width, m.height = 80, 10
	completeBoardLoad(t, &m, m.Init())
	return m
}

func requireMouseHandler(t *testing.T, handler func(tea.MouseMsg) tea.Cmd, surface string) func(tea.MouseMsg) tea.Cmd {
	t.Helper()
	if handler == nil {
		t.Fatalf("%s mouse handler is nil", surface)
	}
	return handler
}

func requireMouseCommand(t *testing.T, command tea.Cmd, action string) tea.Cmd {
	t.Helper()
	if command == nil {
		t.Fatalf("%s was ignored", action)
	}
	return command
}

// reverseVideoPattern matches the SGR reverse attribute in any parameter
// position. A themed control carries its colors in the same sequence, so the
// composed frame emits "...;7m" rather than a standalone "\x1b[7m".
var reverseVideoPattern = regexp.MustCompile(`\x1b\[[0-9;]*\b7[;m]`)

func containsReverseVideo(content string) bool {
	return reverseVideoPattern.MatchString(content)
}

func pointerCommandForLabel(t *testing.T, model *Model, label string) tea.Cmd {
	t.Helper()
	view := model.View()
	handler := requireMouseHandler(t, view.OnMouse, label)
	for row, line := range strings.Split(ansi.Strip(view.Content), "\n") {
		if index := strings.Index(line, label); index >= 0 {
			x := ansi.StringWidth(line[:index])
			press := requireMouseCommand(t,
				handler(tea.MouseClickMsg{X: x, Y: row, Button: tea.MouseLeft}),
				"press "+label,
			)
			if command := updateTestModel(t, model, press()); command != nil {
				t.Fatalf("press %q returned domain command", label)
			}
			pressedView := model.View()
			if !containsReverseVideo(pressedView.Content) {
				t.Fatalf("press %q did not render feedback", label)
			}
			release := requireMouseCommand(t,
				requireMouseHandler(t, pressedView.OnMouse, label)(tea.MouseReleaseMsg{X: x, Y: row, Button: tea.MouseLeft}),
				"click "+label,
			)
			return updateTestModel(t, model, release())
		}
	}
	t.Fatalf("rendered label %q not found:\n%s", label, ansi.Strip(view.Content))
	return nil
}

func TestPointerOpensAndClosesHelpFromVisibleControls(t *testing.T) {
	m := mouseRoutingTestModel(t)
	updateTestModel(t, &m, pointerCommandForLabel(t, &m, "? help")())
	if !m.helpOpen {
		t.Fatal("pointer did not open keyboard help")
	}
	updateTestModel(t, &m, pointerCommandForLabel(t, &m, helpCloseLabel)())
	if m.helpOpen {
		t.Fatal("pointer did not close keyboard help")
	}
}

func TestPointerBoardFooterOpensPrimarySurfaces(t *testing.T) {
	for _, tc := range []struct {
		label string
		open  func(Model) bool
	}{
		{label: "n new", open: func(m Model) bool { return m.editor.IsOpen() }},
		{label: "s settings", open: func(m Model) bool { return m.settings != nil }},
		{label: "a split ADR", open: func(m Model) bool { return m.adr.IsOpen() }},
		{label: "i import", open: func(m Model) bool { return m.issueImport.IsOpen() }},
	} {
		t.Run(tc.label, func(t *testing.T) {
			st := newSettingsTestStore(t)
			m := NewModel(st, nil, "alice")
			m.width, m.height = 427, 73
			m.settingsNew = func() *settingsModel { return newSettingsModel(st, "alice", context.Background()) }
			m.configureAI(ai.NewRunner(st, "", nil, nil), context.Background())
			completeBoardLoad(t, &m, m.Init())
			updateTestModel(t, &m, pointerCommandForLabel(t, &m, tc.label)())
			if !tc.open(m) {
				t.Fatalf("pointer did not open %s", tc.label)
			}
		})
	}
}

func TestPointerBoardFooterRoutesRemainingActions(t *testing.T) {
	for _, tc := range []struct {
		label string
		check func(Model) bool
	}{
		{label: "? help", check: func(m Model) bool { return m.helpOpen }},
		{label: "c cancelled:off", check: func(m Model) bool { return m.boardView.showCancelled }},
		{label: "e edit", check: func(m Model) bool { return m.editor.IsOpen() }},
		{label: "q quit", check: func(m Model) bool { return m.stopped }},
	} {
		t.Run(tc.label, func(t *testing.T) {
			st := newSettingsTestStore(t)
			if tc.label == "e edit" {
				if _, err := st.AddTask("alice", board.Task{Title: "Pointer edit", Status: board.StatusTodo}); err != nil {
					t.Fatal(err)
				}
			}
			m := NewModel(st, nil, "alice")
			m.width, m.height = 427, 73
			m.settingsNew = func() *settingsModel { return newSettingsModel(st, "alice", context.Background()) }
			m.configureAI(ai.NewRunner(st, "", nil, nil), context.Background())
			completeBoardLoad(t, &m, m.Init())
			updateTestModel(t, &m, pointerCommandForLabel(t, &m, tc.label)())
			if !tc.check(m) {
				t.Fatalf("pointer did not route %s", tc.label)
			}
		})
	}
}

func TestRootRoutesPointerPressFeedbackToEveryTopmostOwner(t *testing.T) {
	st := newSettingsTestStore(t)
	newRoot := func() Model {
		m := NewModel(st, nil, "alice")
		m.width, m.height = 120, 30
		completeBoardLoad(t, &m, m.Init())
		return m
	}
	press := func(t *testing.T, m *Model, label string) {
		t.Helper()
		before := m.View()
		for row, line := range strings.Split(ansi.Strip(before.Content), "\n") {
			index := strings.Index(line, label)
			if index < 0 {
				continue
			}
			x := ansi.StringWidth(line[:index])
			command := requireMouseCommand(t, before.OnMouse(tea.MouseClickMsg{X: x, Y: row, Button: tea.MouseLeft}), "press "+label)
			if next := updateTestModel(t, m, command()); next != nil {
				t.Fatalf("press %q returned domain command", label)
			}
			if after := m.View().Content; after == before.Content {
				t.Fatalf("press %q produced no visible feedback", label)
			}
			return
		}
		t.Fatalf("visible control %q not found:\n%s", label, ansi.Strip(before.Content))
	}

	for _, test := range []struct {
		name  string
		label string
		open  func(*Model)
	}{
		{name: "board", label: "? help", open: func(*Model) {}},
		{name: "detail", label: "Comment", open: func(m *Model) {
			_ = m.detail.Open(board.Task{ID: "detail", Title: "Detail", Status: board.StatusTodo})
		}},
		{name: "settings", label: "Model:", open: func(m *Model) {
			m.settings = newSettingsModel(st, "alice", context.Background())
			loadSettingsForTest(t, m.settings)
		}},
		{name: "ADR", label: "Source:", open: func(m *Model) {
			m.configureAI(ai.NewRunner(st, "", nil, nil), context.Background())
			_ = m.adr.Open()
		}},
		{name: "editor", label: "Title:", open: func(m *Model) {
			_ = m.editor.OpenAdd(board.StatusTodo)
		}},
		{name: "task action", label: "Ship anyway", open: func(m *Model) {
			m.openShipPrompt(board.Task{ID: "ship", Title: "Ship", Status: board.StatusTodo}, 0)
		}},
		{name: "issue import", label: "source", open: func(m *Model) {
			m.issueImport = issueimport.New(&rootImportStore{}, rootImportBackend{}, "alice", context.Background())
			_ = m.issueImport.Open()
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := newRoot()
			test.open(&m)
			press(t, &m, test.label)
		})
	}
}

func TestQueuedBoardFooterCannotEscapeTopmostOwner(t *testing.T) {
	st := newSettingsTestStore(t)
	for _, test := range []struct {
		name string
		open func(*Model)
	}{
		{name: "help", open: func(m *Model) { m.helpOpen = true }},
		{name: "detail", open: func(m *Model) {
			_ = m.detail.Open(board.Task{ID: "detail", Title: "Detail", Status: board.StatusTodo})
		}},
		{name: "settings", open: func(m *Model) {
			m.settings = newSettingsModel(st, "alice", context.Background())
		}},
		{name: "editor", open: func(m *Model) { _ = m.editor.OpenAdd(board.StatusTodo) }},
		{name: "ADR", open: func(m *Model) {
			m.configureAI(ai.NewRunner(st, "", nil, nil), context.Background())
			_ = m.adr.Open()
		}},
		{name: "task action", open: func(m *Model) {
			m.openShipPrompt(board.Task{ID: "ship", Title: "Ship", Status: board.StatusTodo}, 0)
		}},
		{name: "issue import", open: func(m *Model) {
			m.issueImport = issueimport.New(&rootImportStore{}, rootImportBackend{}, "alice", context.Background())
			_ = m.issueImport.Open()
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := NewModel(st, nil, "alice")
			m.width, m.height = 120, 30
			completeBoardLoad(t, &m, m.Init())
			test.open(&m)
			if command := updateTestModel(t, &m, boardFooterClickedMsg{key: "q"}); command != nil || m.stopped {
				t.Fatalf("queued board footer escaped %s: command=%v stopped=%v", test.name, command, m.stopped)
			}
		})
	}
}

func TestBoardPrimaryControlsRenderPressAndActivateOnRelease(t *testing.T) {
	for _, test := range []struct {
		name  string
		match func(boardHit) bool
		check func(Model) bool
	}{
		{
			name:  "filter text",
			match: func(hit boardHit) bool { return hit.kind == boardHitFilterText },
			check: func(m Model) bool { return m.filter.focus == filterText },
		},
		{
			name: "column heading",
			match: func(hit boardHit) bool {
				return hit.kind == boardHitColumnHeading && hit.status == board.StatusDoing
			},
			check: func(m Model) bool { return m.boardView.column == statusIndex(board.StatusDoing) },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := mouseRoutingTestModel(t)
			m.width, m.height = 120, 30
			_, hits := m.renderBoard()
			var target boardHit
			for _, hit := range hits {
				if test.match(hit) {
					target = hit
					break
				}
			}
			id := boardHitControlID(target)
			if id == "" {
				t.Fatal("rendered board control has no stable pointer identity")
			}
			x, y := target.x0, target.y0
			press := requireMouseCommand(t, m.View().OnMouse(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}), "board press")
			if command := updateTestModel(t, &m, press()); command != nil || !m.pointerState.IsPressed(id) {
				t.Fatalf("board press command=%v pressed=%v", command, m.pointerState.IsPressed(id))
			}
			if !strings.Contains(m.View().Content, "\x1b[7m") {
				t.Fatal("board control press produced no visible feedback")
			}
			release := requireMouseCommand(t, m.View().OnMouse(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseNone}), "board release")
			activate := updateTestModel(t, &m, release())
			if activate == nil {
				t.Fatal("board release produced no domain action")
			}
			updateTestModel(t, &m, activate())
			if !test.check(m) || m.pointerState.IsPressed(id) {
				t.Fatalf("board release state did not activate %s", test.name)
			}
		})
	}
}

func TestBoardCardLabelsHavePerRegionPressedIdentity(t *testing.T) {
	m := mouseRoutingTestModel(t)
	m.width, m.height = 120, 30
	m.board.Tasks[0].Tags = []string{"shared"}
	m.board.Tasks[1].Tags = []string{"shared"}
	_, hits := m.renderBoard()
	var first boardHit
	for _, hit := range hits {
		if hit.kind == boardHitFilterLabel && hit.taskID == m.board.Tasks[0].ID {
			first = hit
			break
		}
	}
	if first.taskID == "" {
		t.Fatal("card label hit was not rendered")
	}
	firstID := boardHitControlID(first)
	secondID := boardCardLabelControlID(m.board.Tasks[1].ID, "shared")
	press := requireMouseCommand(t, m.View().OnMouse(tea.MouseClickMsg{X: first.x0, Y: first.y0, Button: tea.MouseLeft}), "card label press")
	if command := updateTestModel(t, &m, press()); command != nil {
		t.Fatalf("card label press returned domain command %v", command)
	}
	if !m.pointerState.IsPressed(firstID) || m.pointerState.IsPressed(secondID) {
		t.Fatalf("card label pressed state first=%v second=%v", m.pointerState.IsPressed(firstID), m.pointerState.IsPressed(secondID))
	}
	if !strings.Contains(m.View().Content, "\x1b[7m") {
		t.Fatal("pressed card label produced no visible feedback")
	}
}

func TestBoardReleaseClearsPressedControlRemovedByRefresh(t *testing.T) {
	m := mouseRoutingTestModel(t)
	m.width, m.height = 120, 30
	m.board.Tasks[0].Tags = []string{"temporary"}
	_, hits := m.renderBoard()
	var label boardHit
	for _, hit := range hits {
		if hit.kind == boardHitFilterLabel && hit.taskID == m.board.Tasks[0].ID {
			label = hit
			break
		}
	}
	if label.taskID == "" {
		t.Fatal("temporary card label was not rendered")
	}
	press := requireMouseCommand(t, m.View().OnMouse(tea.MouseClickMsg{X: label.x0, Y: label.y0, Button: tea.MouseLeft}), "temporary label press")
	updateTestModel(t, &m, press())
	if !m.pointerState.Active() {
		t.Fatal("temporary label did not own the press")
	}
	m.board.Tasks[0].Tags = nil
	release := requireMouseCommand(t, m.View().OnMouse(tea.MouseReleaseMsg{X: label.x0, Y: label.y0, Button: tea.MouseNone}), "removed label release")
	updateTestModel(t, &m, release())
	if m.pointerState.Active() {
		t.Fatal("removed control left pressed state stuck")
	}
}

func TestNarrowColumnHeadingStillRendersPressedFeedback(t *testing.T) {
	m := mouseRoutingTestModel(t)
	column := m.renderBoardColumn(board.StatusDoing, 2, 4)
	var heading boardHit
	for _, hit := range column.hits {
		if hit.kind == boardHitColumnHeading {
			heading = hit
			break
		}
	}
	handler := boardMouseHandlerWithFeedback(column.hits, false, m.pointerState)
	press := requireMouseCommand(t, handler(tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft}), "narrow column press")
	if command := updateTestModel(t, &m, press()); command != nil {
		t.Fatalf("narrow column press returned domain command %v", command)
	}
	if !m.pointerState.IsPressed(boardHitControlID(heading)) {
		t.Fatal("narrow column did not retain pressed state")
	}
	if rendered := strings.Join(m.renderBoardColumn(board.StatusDoing, 2, 4).lines, "\n"); !strings.Contains(rendered, "\x1b[7m") {
		t.Fatalf("narrow column omitted pressed feedback: %q", rendered)
	}
}

func TestPointerBoardFooterCancelsLiftBeforeChangingFocus(t *testing.T) {
	m := mouseRoutingTestModel(t)
	updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeySpace})
	if m.move.lifted == nil {
		t.Fatal("test did not lift the selected card")
	}
	updateTestModel(t, &m, boardFooterClickedMsg{key: "?"})
	if m.move.lifted != nil || !m.helpOpen {
		t.Fatalf("pointer footer focus change = lifted:%v help:%v", m.move.lifted != nil, m.helpOpen)
	}
}

func TestBoardMouseWheelScrollsHoveredColumn(t *testing.T) {
	m := mouseRoutingTestModel(t)

	beforeBoard := ansi.Strip(m.View().Content)
	boardMouse := requireMouseHandler(t, m.View().OnMouse, "board")
	// The column group is centered inside the frame (spec section 2.5), so the
	// hovered cell is the middle of the frame rather than its left edge.
	scrollDown := requireMouseCommand(t,
		boardMouse(tea.MouseWheelMsg{X: 40, Y: 4, Button: tea.MouseWheelDown}),
		"board wheel down",
	)
	if command := updateTestModel(t, &m, scrollDown()); command != nil {
		t.Fatalf("board wheel started command %v", command)
	}
	if !m.boardView.manualScroll[0] || m.boardView.scrolls[0] == 0 {
		t.Fatalf("board scroll state = manual %v offset %d", m.boardView.manualScroll[0], m.boardView.scrolls[0])
	}
	afterBoard := ansi.Strip(m.View().Content)
	if afterBoard == beforeBoard || strings.Contains(afterBoard, "Card 0") {
		t.Fatalf("board did not scroll the hovered column:\n%s", afterBoard)
	}
	if command := m.View().OnMouse(tea.MouseWheelMsg{X: 40, Y: 4, Button: tea.MouseWheelUp}); command == nil {
		t.Fatal("board wheel up was ignored")
	} else {
		updateTestModel(t, &m, command())
	}
	if m.boardView.scrolls[0] != 0 {
		t.Fatalf("board wheel up offset = %d", m.boardView.scrolls[0])
	}
}

func TestBoardMouseWheelAtTopDoesNothing(t *testing.T) {
	m := mouseRoutingTestModel(t)
	m.boardView.manualScroll[0] = true
	m.boardView.scrolls[0] = 0
	handler := requireMouseHandler(t, m.View().OnMouse, "board top scroll")
	if command := handler(tea.MouseWheelMsg{X: 1, Y: 4, Button: tea.MouseWheelUp}); command != nil {
		t.Fatalf("top-bound wheel up returned %v", command)
	}
	if command := handler(tea.MouseWheelMsg{X: 1, Y: 0, Button: tea.MouseWheelUp}); command != nil {
		t.Fatalf("header wheel returned %v", command)
	}
}

func TestBoardPointerMotionCancelsControlPress(t *testing.T) {
	m := mouseRoutingTestModel(t)
	m.width, m.height = 120, 30
	_, hits := m.renderBoard()
	var target boardHit
	for _, hit := range hits {
		if hit.kind == boardHitFilterText {
			target = hit
			break
		}
	}
	if target.kind != boardHitFilterText {
		t.Fatal("filter text control was not rendered")
	}
	view := m.View()
	press := requireMouseCommand(t, view.OnMouse(tea.MouseClickMsg{X: target.x0, Y: target.y0, Button: tea.MouseLeft}), "filter press")
	updateTestModel(t, &m, press())
	if !m.pointerState.Active() {
		t.Fatal("filter press did not enter feedback state")
	}
	motion := requireMouseCommand(t, m.View().OnMouse(tea.MouseMotionMsg{X: target.x0 + 1, Y: target.y0, Button: tea.MouseLeft}), "filter motion")
	updateTestModel(t, &m, motion())
	if m.pointerState.Active() {
		t.Fatal("pointer motion left filter feedback pressed")
	}
}

func TestDetailOverlayMouseWheelAndOutsideClick(t *testing.T) {
	m := mouseRoutingTestModel(t)

	load := updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if load == nil {
		t.Fatal("detail open did not start enrichment")
	}
	updateTestModel(t, &m, load())
	detailBefore := ansi.Strip(m.View().Content)
	detailMouse := requireMouseHandler(t, m.View().OnMouse, "detail")
	if command := detailMouse(tea.MouseWheelMsg{X: 0, Y: 0, Button: tea.MouseWheelDown}); command != nil {
		updateTestModel(t, &m, command())
	}
	if command := detailMouse(tea.MouseClickMsg{X: 40, Y: 5, Button: tea.MouseLeft}); command != nil {
		updateTestModel(t, &m, command())
	}
	if !m.detail.IsOpen() {
		t.Fatal("click inside detail dismissed it")
	}
	detailScroll := requireMouseCommand(t,
		detailMouse(tea.MouseWheelMsg{X: 40, Y: 5, Button: tea.MouseWheelDown}),
		"detail wheel down",
	)
	followup := updateTestModel(t, &m, detailScroll())
	if followup == nil {
		t.Fatal("detail wheel did not produce scroll action")
	}
	updateTestModel(t, &m, followup())
	detailAfter := ansi.Strip(m.View().Content)
	if detailAfter == detailBefore {
		t.Fatal("detail wheel did not change the viewport")
	}
	dismiss := requireMouseHandler(t, m.View().OnMouse, "detail backdrop")
	if command := dismiss(tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft}); command != nil {
		t.Fatalf("detail backdrop activated on press: %v", command)
	}
	if command := dismiss(tea.MouseReleaseMsg{X: 0, Y: 0, Button: tea.MouseLeft}); command == nil {
		t.Fatal("outside click was ignored")
	} else {
		updateTestModel(t, &m, command())
	}
	if m.detail.IsOpen() {
		t.Fatal("outside click did not close detail")
	}
}

func TestCardDetailOpenWithoutASelectedTaskIsNoop(t *testing.T) {
	m := NewModel(stubBoardReader{board: board.Board{Title: "Empty"}}, nil, "u")
	completeBoardLoad(t, &m, m.Init())
	if command := updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEnter}); command != nil || m.detail.IsOpen() {
		t.Fatalf("empty-board enter = command %v open %v", command, m.detail.IsOpen())
	}
	updateTestModel(t, &m, boardCardClickedMsg{taskID: "missing"})
	if m.detail.IsOpen() {
		t.Fatal("missing card click opened detail")
	}
}

func TestBoardHelpOverlayDocumentsCoreMutationKeys(t *testing.T) {
	m := NewModel(stubBoardReader{}, nil, "alice")
	m.loading = false
	m.board = boardViewFixture(time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC))
	m.width, m.height = 80, 24

	updateTestModel(t, &m, tea.KeyPressMsg{Code: '?', Text: "?"})
	view := m.View()
	plainView := ansi.Strip(view.Content)
	for _, want := range []string{
		"Keyboard help", "enter open card", "space lift or drop card",
		"/     text filter", "f     label filter", "x cancel card", "X     clear filter",
	} {
		if !strings.Contains(plainView, want) {
			t.Errorf("help missing %q:\n%s", want, plainView)
		}
	}
	if view.OnMouse == nil {
		t.Fatal("help overlay did not own pointer input")
	}
	updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.helpOpen {
		t.Fatal("escape did not close help")
	}
}

func TestBoardHelpRoutingAndAvailableFeatureHints(t *testing.T) {
	st := newSettingsTestStore(t)
	m := NewModel(st, nil, "alice")
	m.loading = false
	m.settingsNew = func() *settingsModel { return newSettingsModel(st, "alice", context.Background()) }
	m.configureAI(ai.NewRunner(st, "", nil, nil), context.Background())
	m.helpOpen = true

	updateTestModel(t, &m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	updateTestModel(t, &m, boardCardClickedMsg{taskID: "ignored"})
	if !m.helpOpen {
		t.Fatal("help closed on ignored board input")
	}
	for _, want := range []string{"n new card", "e edit card", "s settings", "a split ADR", "i import forge issue"} {
		if view := ansi.Strip(m.View().Content); !strings.Contains(view, want) {
			t.Errorf("enabled help missing %q:\n%s", want, view)
		}
	}
	updateTestModel(t, &m, tea.KeyPressMsg{Code: '?', Text: "?"})
	if m.helpOpen {
		t.Fatal("question mark did not close help")
	}
	m.helpOpen = true
	if command := updateTestModel(t, &m, tea.KeyPressMsg{Code: 'q', Text: "q"}); command == nil || !m.stopped {
		t.Fatal("help did not preserve root quit")
	}

	tiny := NewModel(stubBoardReader{}, nil, "alice")
	tiny.width, tiny.height = 3, 2
	if got := tiny.keyboardHelpOverlay("board"); got != "board" {
		t.Fatalf("tiny help changed background: %q", got)
	}
	// The footer ladder drops its spelled-out hints on a frame this narrow, but
	// never the control: clicking it is a frozen dismissal.
	tiny.width, tiny.height = 30, 4
	if got := ansi.Strip(tiny.keyboardHelpOverlay("board")); !strings.Contains(got, helpCloseLabel) {
		t.Fatalf("short help lost close control:\n%s", got)
	}
	tiny.width, tiny.height = 80, 24
	if got := ansi.Strip(tiny.keyboardHelpOverlay("board")); !strings.Contains(got, "? or esc close help | q quit") {
		t.Fatalf("wide help lost the dismissal ladder:\n%s", got)
	}
}

func TestRootDetailCommentAndLinkActionsOwnInputAndRefresh(t *testing.T) {
	st := newSettingsTestStore(t)
	current, err := st.AddTask("alice", board.Task{Title: "Current", Status: board.StatusTodo, Prio: 3})
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.AddTask("alice", board.Task{Title: "Blocker", Status: board.StatusDoing, Prio: 3})
	if err != nil {
		t.Fatal(err)
	}
	m := NewModel(st, nil, "alice")
	completeBoardLoad(t, &m, m.Init())
	drainModelCommands(t, &m, updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEnter}))

	updateTestModel(t, &m, tea.KeyPressMsg{Code: 'c', Text: "c"})
	if !m.detail.IsOpen() || !m.detail.OwnsInput() || !strings.Contains(ansi.Strip(m.View().Content), "ADD COMMENT") {
		t.Fatalf("comment composer did not own detail input:\n%s", ansi.Strip(m.View().Content))
	}
	for _, char := range "xdraq" {
		updateTestModel(t, &m, tea.KeyPressMsg{Code: char, Text: string(char)})
	}
	updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyDelete})
	updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if !m.detail.IsOpen() || m.detail.OwnsInput() {
		t.Fatal("first Escape did not return from composer to detail")
	}
	updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.detail.IsOpen() {
		t.Fatal("second Escape did not close detail")
	}

	// Reopen and persist the same collision-heavy text. If root shortcuts had
	// stolen x/d/r/a/Delete, this would either mutate the task or save less text.
	drainModelCommands(t, &m, updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEnter}))
	updateTestModel(t, &m, tea.KeyPressMsg{Code: 'c', Text: "c"})
	for _, char := range "xdraq" {
		updateTestModel(t, &m, tea.KeyPressMsg{Code: char, Text: string(char)})
	}
	updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyDelete})
	drainModelCommands(t, &m, updateTestModel(t, &m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}))
	comments, err := st.Comments("alice", current.ID)
	if err != nil || len(comments) != 1 || comments[0].Body != "xdraq" || !m.detail.IsOpen() || m.detail.OwnsInput() {
		t.Fatalf("saved comments = %+v, err:%v detail:%v owned:%v", comments, err, m.detail.IsOpen(), m.detail.OwnsInput())
	}
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "comment c1 added") {
		t.Fatalf("comment acknowledgement missing from status line:\n%s", view)
	}

	// Idle d is deliberately detail-scoped comment deletion, not task kill.
	updateTestModel(t, &m, tea.KeyPressMsg{Code: 'd', Text: "d"})
	if !m.detail.OwnsInput() || !strings.Contains(ansi.Strip(m.View().Content), "DELETE COMMENT") {
		t.Fatalf("idle d did not open comment deletion:\n%s", ansi.Strip(m.View().Content))
	}
	updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEnter})
	drainModelCommands(t, &m, updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEnter}))
	comments, err = st.Comments("alice", current.ID)
	if err != nil || len(comments) != 0 {
		t.Fatalf("comments after confirmed delete = %+v, %v", comments, err)
	}

	// Add the incoming direction: target blocks the current card.
	updateTestModel(t, &m, tea.KeyPressMsg{Code: 'b', Text: "b"})
	updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyTab})
	for _, char := range fmt.Sprintf("%d", other.Seq) {
		updateTestModel(t, &m, tea.KeyPressMsg{Code: char, Text: string(char)})
	}
	drainModelCommands(t, &m, updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEnter}))
	links, err := st.TaskLinks("alice", current.ID)
	if err != nil || len(links.BlockedBy) != 1 || links.BlockedBy[0].ID != other.ID {
		t.Fatalf("incoming links = %+v, %v", links, err)
	}
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "blocked by") || !strings.Contains(view, "completion gate") {
		t.Fatalf("link and completion gate missing:\n%s", view)
	}

	updateTestModel(t, &m, tea.KeyPressMsg{Code: 'u', Text: "u"})
	updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEnter})
	drainModelCommands(t, &m, updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEnter}))
	links, err = st.TaskLinks("alice", current.ID)
	if err != nil || len(links.BlockedBy) != 0 || len(links.Blocks) != 0 {
		t.Fatalf("links after confirmed unlink = %+v, %v", links, err)
	}
}

func TestPointerDetailCommentSavePersistsWithoutCtrlS(t *testing.T) {
	st := newSettingsTestStore(t)
	task, err := st.AddTask("alice", board.Task{Title: "Pointer comment", Status: board.StatusTodo, Prio: 3})
	if err != nil {
		t.Fatal(err)
	}
	m := NewModel(st, nil, "alice")
	m.width, m.height = 120, 30
	completeBoardLoad(t, &m, m.Init())
	drainModelCommands(t, &m, updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEnter}))
	updateTestModel(t, &m, pointerCommandForLabel(t, &m, "Comment")())
	for _, value := range "saved by visible button" {
		updateTestModel(t, &m, tea.KeyPressMsg{Code: value, Text: string(value)})
	}
	save := updateTestModel(t, &m, pointerCommandForLabel(t, &m, "Save comment")())
	if save == nil {
		t.Fatal("visible Save comment did not start the existing write path")
	}
	drainModelCommands(t, &m, save)
	comments, err := st.Comments("alice", task.ID)
	if err != nil || len(comments) != 1 || comments[0].Body != "saved by visible button" {
		t.Fatalf("pointer comment persistence = %+v, %v", comments, err)
	}
}

func TestPointerDetailLinkLifecyclePersistsThroughVisibleControls(t *testing.T) {
	st := newSettingsTestStore(t)
	current, err := st.AddTask("alice", board.Task{Title: "Pointer link", Status: board.StatusTodo, Prio: 3})
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.AddTask("alice", board.Task{Title: "Pointer blocker", Status: board.StatusDoing, Prio: 3})
	if err != nil {
		t.Fatal(err)
	}
	m := NewModel(st, nil, "alice")
	m.width, m.height = 120, 30
	completeBoardLoad(t, &m, m.Init())
	drainModelCommands(t, &m, updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEnter}))
	updateTestModel(t, &m, pointerCommandForLabel(t, &m, "Link")())
	for _, value := range fmt.Sprintf("%d", other.Seq) {
		updateTestModel(t, &m, tea.KeyPressMsg{Code: value, Text: string(value)})
	}
	add := updateTestModel(t, &m, pointerCommandForLabel(t, &m, "Add link")())
	if add == nil {
		t.Fatal("visible Add link did not start the existing write path")
	}
	drainModelCommands(t, &m, add)
	links, err := st.TaskLinks("alice", current.ID)
	if err != nil || len(links.Blocks)+len(links.BlockedBy) != 1 {
		t.Fatalf("pointer link persistence = %+v, %v", links, err)
	}

	updateTestModel(t, &m, pointerCommandForLabel(t, &m, "Unlink")())
	updateTestModel(t, &m, pointerCommandForLabel(t, &m, "Delete")())
	remove := updateTestModel(t, &m, pointerCommandForLabel(t, &m, "Confirm delete")())
	if remove == nil {
		t.Fatal("visible Confirm delete did not start unlink")
	}
	drainModelCommands(t, &m, remove)
	links, err = st.TaskLinks("alice", current.ID)
	if err != nil || len(links.Blocks)+len(links.BlockedBy) != 0 {
		t.Fatalf("pointer unlink persistence = %+v, %v", links, err)
	}
}

func TestPurgedDetailIgnoresLateMutationResult(t *testing.T) {
	st := newSettingsTestStore(t)
	task, err := st.AddTask("alice", board.Task{Title: "Soon gone", Status: board.StatusTodo, Prio: 3})
	if err != nil {
		t.Fatal(err)
	}
	m := NewModel(st, nil, "alice")
	completeBoardLoad(t, &m, m.Init())
	drainModelCommands(t, &m, updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEnter}))
	updateTestModel(t, &m, tea.KeyPressMsg{Code: 'c', Text: "c"})
	for _, char := range "late" {
		updateTestModel(t, &m, tea.KeyPressMsg{Code: char, Text: string(char)})
	}
	save := updateTestModel(t, &m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if save == nil || !m.detail.OwnsInput() {
		t.Fatal("comment write did not start")
	}
	if _, err := st.DeleteTask("alice", task.ID); err != nil {
		t.Fatal(err)
	}
	updateTestModel(t, &m, boardLoadedMsg{board: board.Board{Title: "Board"}})
	if m.detail.IsOpen() || m.detail.TaskID() != "" {
		t.Fatal("purged card retained a detail pane")
	}
	if next := updateTestModel(t, &m, save()); next != nil || m.detail.IsOpen() || m.detail.TaskID() != "" {
		t.Fatalf("late result reopened detail: command:%v open:%v task:%q", next, m.detail.IsOpen(), m.detail.TaskID())
	}
	if _, err := st.Comments("alice", task.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("purged task became readable after late result: %v", err)
	}
}

func TestRootRoutesCreateEditorAndRefreshesAcknowledgedSave(t *testing.T) {
	st := newSettingsTestStore(t)
	existing, err := st.AddTask("alice", board.Task{Title: "Existing card", Status: board.StatusTodo, Prio: 3})
	if err != nil {
		t.Fatal(err)
	}
	m := NewModel(st, nil, "alice")
	m.SetActiveProject("kb")
	completeBoardLoad(t, &m, m.Init())
	loadLabels := updateTestModel(t, &m, tea.KeyPressMsg{Code: 'n'})
	if !m.editor.IsOpen() || loadLabels == nil {
		t.Fatalf("new editor = open:%v command:%v", m.editor.IsOpen(), loadLabels)
	}
	updateTestModel(t, &m, loadLabels())
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "CREATE CARD / todo") || m.View().OnMouse == nil {
		t.Fatalf("create overlay missing or editor pointer inactive:\n%s", view)
	}
	for _, char := range "Root-created card" {
		updateTestModel(t, &m, tea.KeyPressMsg{Code: char, Text: string(char)})
	}
	save := updateTestModel(t, &m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if save == nil {
		t.Fatal("root ctrl+s did not dispatch editor save")
	}
	refresh := updateTestModel(t, &m, resultSkippingSpinnerTick(t, save))
	if m.editor.IsOpen() || refresh == nil || !m.loading {
		t.Fatalf("save acknowledgement = editor:%v refresh:%v loading:%v", m.editor.IsOpen(), refresh, m.loading)
	}
	completeBoardLoad(t, &m, refresh)
	if len(m.board.Tasks) != 2 {
		t.Fatalf("refreshed board = %+v", m.board)
	}
	selected, ok := m.selectedTask()
	if !ok || selected.Title != "Root-created card" || selected.ID == "" || selected.ID == existing.ID || m.selectAfterLoad != "" {
		t.Fatalf("created card selection = selected:%+v ok:%v pending:%q", selected, ok, m.selectAfterLoad)
	}
	// The card the board created carries the active project, spelled once.
	if got := selected.Tags; len(got) != 1 || got[0] != "project::kb" {
		t.Fatalf("created card tags = %v, want exactly [project::kb]", got)
	}
}

func TestRootEditRoutingAndDirtyWatcherRefreshPreservesInput(t *testing.T) {
	st := newSettingsTestStore(t)
	task, err := st.AddTask("alice", board.Task{Title: "Original card", Status: board.StatusTodo, Prio: 3})
	if err != nil {
		t.Fatal(err)
	}
	m := NewModel(st, nil, "alice")
	completeBoardLoad(t, &m, m.Init())
	updateTestModel(t, &m, tea.KeyPressMsg{Code: 'e'})
	if !m.editor.IsOpen() || m.editor.TaskID() != task.ID {
		t.Fatalf("board edit route = open:%v id:%q", m.editor.IsOpen(), m.editor.TaskID())
	}
	updateTestModel(t, &m, tea.KeyPressMsg{Code: 'X', Text: "X"})
	remote := task
	remote.Title = "Remote card"
	updateTestModel(t, &m, boardLoadedMsg{board: board.Board{Title: "Board", Tasks: []board.Task{remote}}})
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "Original cardX") || !strings.Contains(view, "current edits were preserved") {
		t.Fatalf("dirty watcher refresh overwrote form:\n%s", view)
	}
	updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if !m.editor.IsOpen() || !strings.Contains(ansi.Strip(m.View().Content), "D discard") {
		t.Fatal("root escape bypassed editor unsaved guard")
	}
	updateTestModel(t, &m, tea.KeyPressMsg{Code: 'd'})
	if m.editor.IsOpen() {
		t.Fatal("root did not route explicit discard")
	}

	// Detail launches the same editor and returns to detail when cancelled.
	m.board = board.Board{Title: "Board", Tasks: []board.Task{task}}
	m.boardView = boardViewState{}
	detailLoad := updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if detailLoad == nil || !m.detail.IsOpen() {
		t.Fatal("detail did not open")
	}
	updateTestModel(t, &m, tea.KeyPressMsg{Code: 'e'})
	if !m.editor.IsOpen() || !m.detail.IsOpen() {
		t.Fatal("detail edit did not layer editor over detail")
	}
	updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.editor.IsOpen() || !m.detail.IsOpen() {
		t.Fatal("clean editor close did not return to detail")
	}
}

func TestBoardReloadReconcilesOpenDetailAndCoalescesEnrichment(t *testing.T) {
	reader := &mutableDetailReader{board: board.Board{Title: "Work", Tasks: []board.Task{{
		ID: "same", Title: "Old", Desc: "old description", Status: board.StatusTodo,
	}}}}
	m := NewModel(reader, nil, "u")
	completeBoardLoad(t, &m, m.Init())
	firstDetailLoad := updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEnter})

	updated := board.Task{
		ID: "same", Title: "Updated", Desc: "updated description", Status: board.StatusDoing,
		Checks: []board.Check{{Text: "fresh check", Done: true}},
	}
	latest := updated
	latest.Title = "Latest"
	latest.Desc = "latest description"
	for _, snapshot := range []board.Board{
		{Title: "Work", Tasks: []board.Task{updated}},
		{Title: "Work", Tasks: []board.Task{latest}},
	} {
		if command := updateTestModel(t, &m, boardLoadedMsg{board: snapshot}); command != nil {
			t.Fatal("board refresh overlapped an active detail enrichment load")
		}
	}
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "Latest") || !strings.Contains(view, "latest description") || !strings.Contains(view, "fresh check") {
		t.Fatalf("board refresh did not replace the open task snapshot:\n%s", view)
	}

	successor := updateTestModel(t, &m, firstDetailLoad())
	if successor == nil {
		t.Fatal("root discarded the coalesced detail successor")
	}
	updateTestModel(t, &m, successor())
	if reader.commentLoads != 2 || !strings.Contains(ansi.Strip(m.View().Content), "enrichment 2") {
		t.Fatalf("coalesced enrichment = loads %d view:\n%s", reader.commentLoads, ansi.Strip(m.View().Content))
	}

	idleUpdate := latest
	idleUpdate.Desc = "idle refresh"
	refresh := updateTestModel(t, &m, boardLoadedMsg{board: board.Board{Title: "Work", Tasks: []board.Task{idleUpdate}}})
	if refresh == nil {
		t.Fatal("idle board refresh did not reload detail enrichment")
	}
	updateTestModel(t, &m, refresh())
	if reader.commentLoads != 3 || !strings.Contains(ansi.Strip(m.View().Content), "idle refresh") {
		t.Fatalf("idle detail refresh = loads %d view:\n%s", reader.commentLoads, ansi.Strip(m.View().Content))
	}

	wantErr := errors.New("external read failed")
	updateTestModel(t, &m, boardLoadedMsg{err: wantErr})
	if !m.detail.IsOpen() || !strings.Contains(ansi.Strip(m.View().Content), "idle refresh") {
		t.Fatal("failed board reload discarded the last-good detail")
	}
	updateTestModel(t, &m, boardLoadedMsg{board: board.Board{Title: "Work"}})
	if m.detail.IsOpen() {
		t.Fatal("deleted task left a ghost detail snapshot")
	}
}

func TestRenderFitsNarrowTerminal(t *testing.T) {
	m := NewModel(stubBoardReader{}, nil, strings.Repeat("owner", 20))
	m.loading = false
	m.board = board.Board{
		Title: strings.Repeat("wide 界🙂 ", 10),
		Tasks: []board.Task{{Title: "one", Status: board.StatusTodo}},
	}
	m.loadErr = errors.New("a deliberately long database error that must not widen the terminal")
	for _, width := range []int{1, 2, 3, 8, 16, 23} {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			m.width = width
			output := m.render()
			for lineNumber, line := range strings.Split(output, "\n") {
				if got := ansi.StringWidth(line); got > width {
					t.Errorf("line %d width = %d, terminal = %d: %q", lineNumber+1, got, width, line)
				}
			}
			// The frame is borderless now: the focused column's band caret is
			// the last thing a narrow terminal keeps (spec section 2.2).
			if !strings.Contains(ansi.Strip(output), "▸") {
				t.Errorf("width %d lost the board frame:\n%s", width, output)
			}
		})
	}
}

func TestModelLoadAndPollFailures(t *testing.T) {
	want := errors.New("database unavailable")
	m := NewModel(stubBoardReader{err: want}, stubVersionReader{version: 7}, "u")
	if message := m.readDataVersion()(); message.(dataVersionMsg).version != 7 {
		t.Fatalf("data-version message = %#v", message)
	}

	updated, _ := m.Update(boardLoadedMsg{err: want})
	m = updated.(Model)
	if m.loading || !strings.Contains(m.View().Content, want.Error()) {
		t.Fatalf("load failure state/view = %#v / %q", m, m.View().Content)
	}

	updated, command := m.Update(dataVersionMsg{version: 7})
	m = updated.(Model)
	if !m.haveVersion || m.dataVersion != 7 || command == nil {
		t.Fatalf("initial version state = %#v command=%v", m, command)
	}
	updated, command = m.Update(dataVersionMsg{version: 8})
	m = updated.(Model)
	if !m.loading || m.dataVersion != 8 || command == nil {
		t.Fatalf("changed version state = %#v command=%v", m, command)
	}

	updated, command = m.Update(dataVersionMsg{err: want})
	m = updated.(Model)
	if !errors.Is(m.pollErr, want) || command == nil {
		t.Fatalf("version failure state = %#v command=%v", m, command)
	}
	updated, _ = m.Update(dataVersionMsg{version: 8})
	m = updated.(Model)
	if m.pollErr != nil {
		t.Fatalf("successful retry retained poll error: %v", m.pollErr)
	}

	updated, command = m.Update(pollTickMsg{})
	if command == nil {
		t.Fatal("poll tick did not read data_version")
	}
	if message := command(); message.(dataVersionMsg).version != 7 {
		t.Fatalf("poll result = %#v", message)
	}
}

func TestCanceledVersionReadDoesNotRestartPolling(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	m := newModel(stubBoardReader{}, stubVersionReader{}, "u", ctx)
	cancel()

	command := updateTestModel(t, &m, dataVersionMsg{err: fmt.Errorf("read: %w", context.Canceled)})
	if command != nil || m.pollErr != nil || m.loading {
		t.Fatalf("cancelled read = model:%#v command:%v", m, command)
	}
}

func TestVersionBaselineDuringFallbackQueuesSerializedReload(t *testing.T) {
	reader := &sequenceBoardReader{results: []boardResult{
		{board: board.Board{Title: "Fallback"}},
		{board: board.Board{Title: "Current"}},
	}}
	m := NewModel(reader, stubVersionReader{}, "u")

	fallback := boardLoadFromBatch(t, updateTestModel(t, &m, dataVersionMsg{err: errors.New("version unavailable")}))
	if !m.loading || m.reloadPending || m.haveVersion || reader.calls != 0 {
		t.Fatalf("fallback start = model:%#v calls:%d", m, reader.calls)
	}
	if nextPoll := updateTestModel(t, &m, dataVersionMsg{version: 1}); nextPoll == nil {
		t.Fatal("successful baseline did not continue the poll chain")
	}
	if !m.loading || !m.reloadPending || !m.haveVersion || m.dataVersion != 1 {
		t.Fatalf("baseline during fallback = %#v", m)
	}

	successor := completeBoardLoad(t, &m, fallback)
	if successor == nil || !m.loading || m.reloadPending || m.board.Title != "Fallback" || reader.calls != 1 {
		t.Fatalf("fallback completion = model:%#v calls:%d command:%v", m, reader.calls, successor)
	}
	if next := completeBoardLoad(t, &m, successor); next != nil {
		t.Fatalf("serialized completion scheduled %v", next)
	}
	if m.loading || m.reloadPending || m.board.Title != "Current" || reader.calls != 2 {
		t.Fatalf("serialized reload = model:%#v calls:%d", m, reader.calls)
	}
}

func TestInitialVersionSuccessLoadsBoard(t *testing.T) {
	reader := &sequenceBoardReader{results: []boardResult{{board: board.Board{Title: "Initial"}}}}
	m := NewModel(reader, stubVersionReader{}, "u")

	load := boardLoadFromBatch(t, updateTestModel(t, &m, dataVersionMsg{version: 7}))
	if !m.haveVersion || m.dataVersion != 7 || !m.loading || m.reloadPending {
		t.Fatalf("initial baseline = %#v", m)
	}
	if next := completeBoardLoad(t, &m, load); next != nil {
		t.Fatalf("initial load completion scheduled %v", next)
	}
	if m.loading || m.reloadPending || m.loadErr != nil || m.board.Title != "Initial" || reader.calls != 1 {
		t.Fatalf("initial load = model:%#v calls:%d", m, reader.calls)
	}
}

func TestWatcherRefreshDoesNotFlashLoadingFooter(t *testing.T) {
	reader := &sequenceBoardReader{results: []boardResult{
		{board: board.Board{Title: "Initial"}},
		{board: board.Board{Title: "Changed"}},
	}}
	m := NewModel(reader, stubVersionReader{}, "u")

	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "loading board...") {
		t.Fatalf("first load footer = %q", view)
	}
	initial := boardLoadFromBatch(t, updateTestModel(t, &m, dataVersionMsg{version: 1}))
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "loading board...") {
		t.Fatalf("active first load footer = %q", view)
	}
	completeBoardLoad(t, &m, initial)
	if !m.haveBoardSnapshot {
		t.Fatal("successful first load did not record a board snapshot")
	}

	refresh := boardLoadFromBatch(t, updateTestModel(t, &m, dataVersionMsg{version: 2}))
	if !m.loading {
		t.Fatal("watcher change did not start a board refresh")
	}
	if view := ansi.Strip(m.View().Content); strings.Contains(view, "loading board...") || !strings.Contains(view, "ready") {
		t.Fatalf("watcher refresh footer = %q", view)
	}
	completeBoardLoad(t, &m, refresh)
	if m.loading || m.board.Title != "Changed" {
		t.Fatalf("watcher refresh completion = loading:%v board:%q", m.loading, m.board.Title)
	}
}

func TestInitialLoadFailureRetriesAfterUnchangedPoll(t *testing.T) {
	want := errors.New("transient read failure")
	reader := &sequenceBoardReader{results: []boardResult{
		{err: want},
		{board: board.Board{Title: "Recovered"}},
	}}
	watcher := &countingVersionReader{version: 9}
	m := NewModel(reader, watcher, "u")
	m.board = board.Board{Title: "Last good"}

	first := boardLoadFromBatch(t, updateTestModel(t, &m, dataVersionMsg{version: 9}))
	if next := completeBoardLoad(t, &m, first); next != nil {
		t.Fatalf("failed load completion scheduled %v", next)
	}
	if !errors.Is(m.loadErr, want) || m.loading || m.board.Title != "Last good" {
		t.Fatalf("initial failure = %#v", m)
	}

	retry := boardLoadFromBatch(t, runPoll(t, &m))
	if !m.loading || m.reloadPending || watcher.calls != 1 {
		t.Fatalf("unchanged retry start = model:%#v watcher calls:%d", m, watcher.calls)
	}
	if next := completeBoardLoad(t, &m, retry); next != nil {
		t.Fatalf("retry completion scheduled %v", next)
	}
	if m.loading || m.loadErr != nil || m.board.Title != "Recovered" || reader.calls != 2 {
		t.Fatalf("retry recovery = model:%#v calls:%d", m, reader.calls)
	}
}

func TestRepeatedPollsDoNotOverlapHeldRetry(t *testing.T) {
	want := errors.New("transient read failure")
	reader := &sequenceBoardReader{results: []boardResult{
		{err: want},
		{board: board.Board{Title: "Recovered"}},
	}}
	watcher := &countingVersionReader{version: 3}
	m := NewModel(reader, watcher, "u")

	first := boardLoadFromBatch(t, updateTestModel(t, &m, dataVersionMsg{version: 3}))
	completeBoardLoad(t, &m, first)
	retry := boardLoadFromBatch(t, runPoll(t, &m))
	for attempt := 1; attempt <= 3; attempt++ {
		if nextPoll := runPoll(t, &m); nextPoll == nil {
			t.Fatalf("held retry poll %d stopped the poll chain", attempt)
		}
		if !m.loading || m.reloadPending || reader.calls != 1 {
			t.Fatalf("held retry poll %d = model:%#v calls:%d", attempt, m, reader.calls)
		}
	}
	if next := completeBoardLoad(t, &m, retry); next != nil {
		t.Fatalf("held retry completion scheduled %v", next)
	}
	if m.loading || m.loadErr != nil || m.board.Title != "Recovered" || reader.calls != 2 {
		t.Fatalf("held retry recovery = model:%#v calls:%d", m, reader.calls)
	}
}

func TestVersionChangesDuringLoadCoalesceOneSuccessor(t *testing.T) {
	reader := &sequenceBoardReader{results: []boardResult{
		{board: board.Board{Title: "V1"}},
		{board: board.Board{Title: "V2"}},
		{board: board.Board{Title: "V3"}},
	}}
	m := NewModel(reader, stubVersionReader{}, "u")
	initial := boardLoadFromBatch(t, updateTestModel(t, &m, dataVersionMsg{version: 1}))
	completeBoardLoad(t, &m, initial)

	v2 := boardLoadFromBatch(t, updateTestModel(t, &m, dataVersionMsg{version: 2}))
	for _, version := range []int64{3, 3} {
		if nextPoll := updateTestModel(t, &m, dataVersionMsg{version: version}); nextPoll == nil {
			t.Fatalf("version %d stopped the poll chain", version)
		}
	}
	if !m.loading || !m.reloadPending || m.dataVersion != 3 || reader.calls != 1 {
		t.Fatalf("coalesced changes = model:%#v calls:%d", m, reader.calls)
	}

	successor := completeBoardLoad(t, &m, v2)
	if successor == nil || !m.loading || m.reloadPending || m.board.Title != "V2" || reader.calls != 2 {
		t.Fatalf("first changed load = model:%#v calls:%d command:%v", m, reader.calls, successor)
	}
	if next := completeBoardLoad(t, &m, successor); next != nil {
		t.Fatalf("coalesced successor scheduled a third load: %v", next)
	}
	if m.loading || m.board.Title != "V3" || reader.calls != 3 {
		t.Fatalf("coalesced successor = model:%#v calls:%d", m, reader.calls)
	}
}

func TestFailedLoadWithPendingChangeStartsSerializedSuccessor(t *testing.T) {
	want := errors.New("changed snapshot failed")
	reader := &sequenceBoardReader{results: []boardResult{
		{board: board.Board{Title: "V1"}},
		{err: want},
		{board: board.Board{Title: "V3"}},
	}}
	m := NewModel(reader, stubVersionReader{}, "u")
	initial := boardLoadFromBatch(t, updateTestModel(t, &m, dataVersionMsg{version: 1}))
	completeBoardLoad(t, &m, initial)

	v2 := boardLoadFromBatch(t, updateTestModel(t, &m, dataVersionMsg{version: 2}))
	updateTestModel(t, &m, dataVersionMsg{version: 3})
	successor := completeBoardLoad(t, &m, v2)
	if successor == nil || !m.loading || m.reloadPending || !errors.Is(m.loadErr, want) || m.board.Title != "V1" {
		t.Fatalf("failed load with pending change = %#v", m)
	}
	if next := completeBoardLoad(t, &m, successor); next != nil {
		t.Fatalf("pending recovery scheduled %v", next)
	}
	if m.loading || m.loadErr != nil || m.board.Title != "V3" || reader.calls != 3 {
		t.Fatalf("pending recovery = model:%#v calls:%d", m, reader.calls)
	}
}

func TestShutdownIgnoresLateResults(t *testing.T) {
	reader := &sequenceBoardReader{results: []boardResult{{board: board.Board{Title: "Late"}}}}
	m := NewModel(reader, stubVersionReader{}, "u")
	load := boardLoadFromBatch(t, updateTestModel(t, &m, dataVersionMsg{version: 1}))
	if quit := updateTestModel(t, &m, tea.KeyPressMsg{Code: 'q'}); quit == nil || !m.stopped {
		t.Fatalf("shutdown = model:%#v command:%v", m, quit)
	}

	lateLoad := load()
	for _, message := range []tea.Msg{lateLoad, dataVersionMsg{version: 2}, pollTickMsg{}} {
		if command := updateTestModel(t, &m, message); command != nil {
			t.Fatalf("late %T scheduled %v", message, command)
		}
	}
	if m.board.Title == "Late" || m.dataVersion != 1 || m.reloadPending || m.Init() != nil {
		t.Fatalf("late results changed stopped model: %#v", m)
	}
}

func TestADRSplitRootRoutingMoveCancellationAndShutdown(t *testing.T) {
	st := newSettingsTestStore(t)
	task, err := st.AddTask("u", board.Task{Title: "card", Status: board.StatusTodo, Prio: 3})
	if err != nil {
		t.Fatal(err)
	}
	m := NewModel(st, nil, "u")
	m.configureAI(ai.NewRunner(st, "", nil, nil), context.Background())
	m.board = board.Board{Title: "Work", Tasks: []board.Task{task}}
	m.boardView.adoptBoard(m.board, m.board)
	m.loading = false
	if !m.adr.Enabled() || m.adr.IsOpen() {
		t.Fatalf("ADR wiring enabled=%v open=%v", m.adr.Enabled(), m.adr.IsOpen())
	}
	m.move.beginVisible(m.board, m.board, task, m.boardView.visibleStatuses(), false)
	updateTestModel(t, &m, tea.KeyPressMsg{Code: 'a'})
	if !m.adr.IsOpen() || m.move.lifted != nil || m.board.Tasks[0].ID != task.ID {
		t.Fatalf("ADR open did not restore lift: open=%v move=%#v board=%#v", m.adr.IsOpen(), m.move, m.board)
	}
	if view := m.View(); view.OnMouse == nil || !strings.Contains(ansi.Strip(view.Content), "SPLIT ADR INTO STORIES") {
		t.Fatalf("ADR view routing mouse=%v content:\n%s", view.OnMouse != nil, ansi.Strip(view.Content))
	}

	m.adr.Close()
	m.move.beginVisible(m.board, m.board, task, m.boardView.visibleStatuses(), false)
	m.move.saving = true
	if command := updateTestModel(t, &m, tea.KeyPressMsg{Code: 'a'}); command != nil || m.adr.IsOpen() || m.move.lifted == nil {
		t.Fatalf("move save allowed ADR command=%v open=%v lifted=%v", command, m.adr.IsOpen(), m.move.lifted != nil)
	}
	m.move.saving = false
	m.cancelCardMove("")
	m.move.status, m.move.notice = "", false

	detailLoad := m.detail.Open(task)
	if detailLoad == nil {
		t.Fatal("detail did not open")
	}
	updateTestModel(t, &m, tea.KeyPressMsg{Code: 'a'})
	if m.adr.IsOpen() || !m.detail.IsOpen() {
		t.Fatalf("ADR opened behind detail: adr=%v detail=%v", m.adr.IsOpen(), m.detail.IsOpen())
	}
	m.detail.Close()
	m.settingsNew = func() *settingsModel { return newSettingsModel(st, "u", context.Background()) }
	footer := ansi.Strip(m.View().Content)
	if !strings.Contains(footer, "a split ADR") {
		t.Fatalf("ADR shortcut undiscoverable:\n%s", footer)
	}

	updateTestModel(t, &m, tea.KeyPressMsg{Code: 'a'})
	quit := updateTestModel(t, &m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if quit == nil || !m.stopped || m.adr.IsOpen() {
		t.Fatalf("ctrl-c shutdown command=%v stopped=%v adr=%v", quit, m.stopped, m.adr.IsOpen())
	}
}

func TestTaskActionsRespectDetailEditorAndADRInputOwnership(t *testing.T) {
	st := newSettingsTestStore(t)
	_, err := st.AddTask("u", board.Task{
		Title: "routing", Status: board.StatusTodo, Prio: 3,
		Checks: []board.Check{{Text: "check"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := NewModel(st, nil, "u")
	m.configureAI(ai.NewRunner(st, "", nil, nil), context.Background())
	completeBoardLoad(t, &m, m.Init())

	loadLabels := updateTestModel(t, &m, tea.KeyPressMsg{Code: 'e'})
	if loadLabels != nil {
		updateTestModel(t, &m, loadLabels())
	}
	for _, key := range []tea.KeyPressMsg{
		{Code: 'x', Text: "x"}, {Code: 'r', Text: "r"}, {Code: 't', Text: "t"}, {Code: tea.KeyDelete},
	} {
		updateTestModel(t, &m, key)
	}
	if m.action.open() || !m.editor.IsOpen() {
		t.Fatalf("editor input leaked to task action: action=%#v editor=%v", m.action, m.editor.IsOpen())
	}

	adrModel := NewModel(st, nil, "u")
	adrModel.configureAI(ai.NewRunner(st, "", nil, nil), context.Background())
	completeBoardLoad(t, &adrModel, adrModel.Init())
	updateTestModel(t, &adrModel, tea.KeyPressMsg{Code: 'a'})
	if !adrModel.adr.IsOpen() {
		t.Fatal("ADR input did not open")
	}
	for _, key := range []tea.KeyPressMsg{
		{Code: 'x', Text: "x"}, {Code: 'r', Text: "r"}, {Code: 't', Text: "t"}, {Code: tea.KeyDelete},
	} {
		updateTestModel(t, &adrModel, key)
	}
	if adrModel.action.open() || !adrModel.adr.IsOpen() {
		t.Fatalf("ADR input leaked to task action: action=%#v adr=%v", adrModel.action, adrModel.adr.IsOpen())
	}

	actionModel := NewModel(st, nil, "u")
	actionModel.configureAI(ai.NewRunner(st, "", nil, nil), context.Background())
	completeBoardLoad(t, &actionModel, actionModel.Init())
	drainModelCommands(t, &actionModel, updateTestModel(t, &actionModel, tea.KeyPressMsg{Code: tea.KeyEnter}))
	updateTestModel(t, &actionModel, tea.KeyPressMsg{Code: 'x', Text: "x"})
	if actionModel.action.mode != taskActionKill || !actionModel.detail.IsOpen() {
		t.Fatalf("idle detail x route = action:%#v detail:%v", actionModel.action, actionModel.detail.IsOpen())
	}
	updateTestModel(t, &actionModel, tea.KeyPressMsg{Code: 'a', Text: "a"})
	if actionModel.adr.IsOpen() || actionModel.action.reason.Value() != "a" {
		t.Fatalf("task dialog lost priority: adr=%v reason=%q", actionModel.adr.IsOpen(), actionModel.action.reason.Value())
	}
	updateTestModel(t, &actionModel, tea.KeyPressMsg{Code: tea.KeyEscape})
	if actionModel.action.open() || !actionModel.detail.IsOpen() {
		t.Fatalf("task dialog close disturbed detail: action=%#v detail=%v", actionModel.action, actionModel.detail.IsOpen())
	}
}

func TestDelayedAutoShipDoesNotStealNestedDetailInput(t *testing.T) {
	setup := func(t *testing.T) (Model, *store.Store, board.Task, tea.Cmd) {
		t.Helper()
		st := newSettingsTestStore(t)
		task, err := st.AddTask("u", board.Task{
			Title: "delayed", Status: board.StatusTodo, Prio: 3,
			Checks: []board.Check{{Text: "last"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		m := NewModel(st, nil, "u")
		completeBoardLoad(t, &m, m.Init())
		updateTestModel(t, &m, tea.KeyPressMsg{Code: 't', Text: "t"})
		write := updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeySpace})
		timer := finishActionCommand(t, &m, write)
		if timer == nil {
			t.Fatal("last checklist tick did not schedule auto-ship")
		}
		return m, st, task, timer
	}

	t.Run("timer check", func(t *testing.T) {
		m, st, task, timer := setup(t)
		updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEscape})
		drainModelCommands(t, &m, updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEnter}))
		updateTestModel(t, &m, tea.KeyPressMsg{Code: 'c', Text: "c"})
		if !m.detail.OwnsInput() {
			t.Fatal("comment input did not own focus")
		}
		if command := updateTestModel(t, &m, timer()); command != nil {
			t.Fatalf("auto-ship read started behind detail input: %v", command)
		}
		current, err := st.Task("u", task.ID)
		if err != nil || current.Status != board.StatusTodo || m.action.open() || !m.detail.OwnsInput() {
			t.Fatalf("timer stole nested input: task=%+v err=%v action=%#v owned=%v", current, err, m.action, m.detail.OwnsInput())
		}
	})

	t.Run("ready result", func(t *testing.T) {
		m, st, task, timer := setup(t)
		read := updateTestModel(t, &m, timer())
		if read == nil {
			t.Fatal("eligible timer did not start canonical read")
		}
		updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEscape})
		drainModelCommands(t, &m, updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEnter}))
		updateTestModel(t, &m, tea.KeyPressMsg{Code: 'c', Text: "c"})
		if command := updateTestModel(t, &m, read()); command != nil {
			t.Fatalf("auto-ship write started behind detail input: %v", command)
		}
		current, err := st.Task("u", task.ID)
		if err != nil || current.Status != board.StatusTodo || m.action.open() || !m.detail.OwnsInput() {
			t.Fatalf("ready result stole nested input: task=%+v err=%v action=%#v owned=%v", current, err, m.action, m.detail.OwnsInput())
		}
	})
}

func TestAutoShipInputOwnershipMatrix(t *testing.T) {
	st := newSettingsTestStore(t)
	task, err := st.AddTask("u", board.Task{Title: "owner", Status: board.StatusTodo, Prio: 3})
	if err != nil {
		t.Fatal(err)
	}
	newRoot := func() Model {
		m := NewModel(st, nil, "u")
		completeBoardLoad(t, &m, m.Init())
		return m
	}
	for _, test := range []struct {
		name string
		own  func(*Model)
	}{
		{name: "settings", own: func(m *Model) { m.settings = &settingsModel{} }},
		{name: "help", own: func(m *Model) { m.helpOpen = true }},
		{name: "editor", own: func(m *Model) { _ = m.editor.OpenAdd(board.StatusTodo) }},
		{name: "ADR", own: func(m *Model) {
			m.configureAI(ai.NewRunner(st, "", nil, nil), context.Background())
			_ = m.adr.Open()
		}},
		{name: "issue import", own: func(m *Model) {
			m.issueImport = issueimport.New(&rootImportStore{}, rootImportBackend{}, "u", context.Background())
			_ = m.issueImport.Open()
		}},
		{name: "detail", own: func(m *Model) { _ = m.detail.Open(task) }},
		{name: "filter", own: func(m *Model) { _ = m.filter.focusText() }},
		{name: "move preview", own: func(m *Model) {
			m.move.beginVisible(m.board, m.board, task, m.boardView.visibleStatuses(), false)
		}},
		{name: "move write", own: func(m *Model) { m.move.saving = true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := newRoot()
			if m.autoShipInputOwned() {
				t.Fatal("idle root unexpectedly owned auto-ship input")
			}
			test.own(&m)
			if !m.autoShipInputOwned() {
				t.Fatal("active owner did not block delayed auto-ship")
			}
		})
	}
}

var fullScreenClear = []byte("\x1b[H\x1b[2J")

// finalFullScreenFrame drops terminal capability/mode negotiation and any
// preliminary renderer clear, retaining the last complete full-screen frame.
// Bubble Tea may order those startup writes differently under load; the frame
// after its full-screen clear is the user-visible contract the golden owns.
func finalFullScreenFrame(output []byte) ([]byte, bool) {
	start := bytes.LastIndex(output, fullScreenClear)
	if start < 0 {
		return nil, false
	}
	return output[start:], true
}

func TestFinalFullScreenFrame(t *testing.T) {
	first := append(append([]byte("negotiation"), fullScreenClear...), []byte("old")...)
	second := append(append(first, []byte("resize-diff")...), fullScreenClear...)
	output := append(second, []byte("stable")...)
	frame, ok := finalFullScreenFrame(output)
	expected := append(append([]byte(nil), fullScreenClear...), []byte("stable")...)
	if !ok || !bytes.Equal(frame, expected) {
		t.Fatalf("frame = %q, %v", frame, ok)
	}
	if frame, ok := finalFullScreenFrame([]byte("no clear")); ok || frame != nil {
		t.Fatalf("missing clear = %q, %v", frame, ok)
	}
}

type renderedCell struct {
	value rune
	style string
}

type renderedScreen struct {
	cells       [][]renderedCell
	cursorX     int
	cursorY     int
	savedCursor [2]int
	style       sgrStyle
}

type sgrStyle struct {
	bold       bool
	faint      bool
	italic     bool
	underline  bool
	blink      bool
	reverse    bool
	conceal    bool
	strike     bool
	foreground string
	background string
}

func (s *sgrStyle) reset() {
	*s = sgrStyle{}
}

func (s *sgrStyle) applyBasic(code int) bool {
	switch code {
	case 0:
		s.reset()
	case 1:
		s.bold, s.faint = true, false
	case 2:
		s.faint, s.bold = true, false
	case 3:
		s.italic = true
	case 4:
		s.underline = true
	case 5, 6:
		s.blink = true
	case 7:
		s.reverse = true
	case 8:
		s.conceal = true
	case 9:
		s.strike = true
	case 22:
		s.bold, s.faint = false, false
	case 23:
		s.italic = false
	case 24:
		s.underline = false
	case 25:
		s.blink = false
	case 27:
		s.reverse = false
	case 28:
		s.conceal = false
	case 29:
		s.strike = false
	case 39:
		s.foreground = ""
	case 49:
		s.background = ""
	default:
		return false
	}
	return true
}

func (s *sgrStyle) applyColor(parts []string, index *int, code int) {
	switch {
	case code >= 30 && code <= 37, code >= 90 && code <= 97:
		s.foreground = strconv.Itoa(code)
	case code >= 40 && code <= 47, code >= 100 && code <= 107:
		s.background = strconv.Itoa(code)
	case code == 38 || code == 48:
		// 38;5;N is three parameters, 38;2;R;G;B is five. Reading the indexed
		// length for a truecolor run would keep one channel and re-parse the
		// other two as attributes, which collapses distinct colors onto the
		// same cell key.
		length := 0
		if *index+1 < len(parts) {
			switch parts[*index+1] {
			case "5":
				length = 2
			case "2":
				length = 4
			}
		}
		if length == 0 || *index+length >= len(parts) {
			return
		}
		value := strings.Join(parts[*index:*index+length+1], ";")
		if code == 38 {
			s.foreground = value
		} else {
			s.background = value
		}
		*index += length
	}
}

func (s *sgrStyle) apply(params string) {
	if params == "" {
		s.reset()
		return
	}
	parts := strings.Split(params, ";")
	for i := 0; i < len(parts); i++ {
		code, err := strconv.Atoi(parts[i])
		if err == nil && !s.applyBasic(code) {
			s.applyColor(parts, &i, code)
		}
	}
}

func (s sgrStyle) key() string {
	params := make([]string, 0, 10)
	if s.bold {
		params = append(params, "1")
	}
	if s.faint {
		params = append(params, "2")
	}
	if s.italic {
		params = append(params, "3")
	}
	if s.underline {
		params = append(params, "4")
	}
	if s.blink {
		params = append(params, "5")
	}
	if s.reverse {
		params = append(params, "7")
	}
	if s.conceal {
		params = append(params, "8")
	}
	if s.strike {
		params = append(params, "9")
	}
	if s.foreground != "" {
		params = append(params, s.foreground)
	}
	if s.background != "" {
		params = append(params, s.background)
	}
	return strings.Join(params, ";")
}

func newRenderedScreen(width, height int) *renderedScreen {
	cells := make([][]renderedCell, height)
	for y := range cells {
		cells[y] = make([]renderedCell, width)
		for x := range cells[y] {
			cells[y][x].value = ' '
		}
	}
	return &renderedScreen{cells: cells}
}

func (s *renderedScreen) clearCell(x, y int) {
	if y < 0 || y >= len(s.cells) || x < 0 || x >= len(s.cells[y]) {
		return
	}
	s.cells[y][x] = renderedCell{value: ' ', style: s.style.key()}
}

func (s *renderedScreen) clearAll() {
	for y := range s.cells {
		for x := range s.cells[y] {
			s.cells[y][x] = renderedCell{value: ' '}
		}
	}
}

func (s *renderedScreen) clearToEndOfLine(mode int) {
	if s.cursorY < 0 || s.cursorY >= len(s.cells) {
		return
	}
	start, end := 0, len(s.cells[s.cursorY])-1
	switch mode {
	case 0:
		start = s.cursorX
	case 1:
		end = s.cursorX
	case 2:
	default:
		return
	}
	for x := start; x <= end; x++ {
		s.clearCell(x, s.cursorY)
	}
}

func (s *renderedScreen) clearToEndOfScreen(mode int) {
	if mode == 2 {
		s.clearAll()
		return
	}
	if s.cursorY < 0 || s.cursorY >= len(s.cells) {
		return
	}
	if mode == 0 {
		s.clearToEndOfLine(0)
		for y := s.cursorY + 1; y < len(s.cells); y++ {
			for x := range s.cells[y] {
				s.clearCell(x, y)
			}
		}
	} else if mode == 1 {
		for y := 0; y < s.cursorY; y++ {
			for x := range s.cells[y] {
				s.clearCell(x, y)
			}
		}
		s.clearToEndOfLine(1)
	}
}

func csiParam(params string, index, fallback int) int {
	params = strings.TrimPrefix(params, "?")
	parts := strings.Split(params, ";")
	if index >= len(parts) || parts[index] == "" {
		return fallback
	}
	value, err := strconv.Atoi(parts[index])
	if err != nil || value < 0 {
		return fallback
	}
	return value
}

func (s *renderedScreen) applyCSI(params string, final byte) {
	switch final {
	case 'A':
		s.cursorY -= csiParam(params, 0, 1)
	case 'B':
		s.cursorY += csiParam(params, 0, 1)
	case 'C':
		s.cursorX += csiParam(params, 0, 1)
	case 'D':
		s.cursorX -= csiParam(params, 0, 1)
	case 'G':
		s.cursorX = csiParam(params, 0, 1) - 1
	case 'H', 'f':
		s.cursorY = csiParam(params, 0, 1) - 1
		s.cursorX = csiParam(params, 1, 1) - 1
	case 'd':
		s.cursorY = csiParam(params, 0, 1) - 1
	case 'J':
		s.clearToEndOfScreen(csiParam(params, 0, 0))
	case 'K':
		s.clearToEndOfLine(csiParam(params, 0, 0))
	case 'X':
		for x := s.cursorX; x < s.cursorX+csiParam(params, 0, 1); x++ {
			s.clearCell(x, s.cursorY)
		}
	case 'm':
		s.style.apply(strings.TrimPrefix(params, "?"))
	case 's':
		s.savedCursor = [2]int{s.cursorX, s.cursorY}
	case 'u':
		s.cursorX, s.cursorY = s.savedCursor[0], s.savedCursor[1]
	}
}

func (s *renderedScreen) consumeCSI(frame []byte) (int, error) {
	end := 2
	for end < len(frame) && (frame[end] < 0x40 || frame[end] > 0x7e) {
		end++
	}
	if end == len(frame) {
		return 0, fmt.Errorf("unterminated CSI sequence")
	}
	s.applyCSI(string(frame[2:end]), frame[end])
	return end + 1, nil
}

func (s *renderedScreen) consumeOSC(frame []byte) (int, error) {
	for i := 2; i < len(frame); i++ {
		if frame[i] == '\a' {
			return i + 1, nil
		}
		if frame[i] == '\x1b' && i+1 < len(frame) && frame[i+1] == '\\' {
			return i + 2, nil
		}
	}
	return 0, fmt.Errorf("unterminated OSC sequence")
}

func (s *renderedScreen) consumeEscape(frame []byte) (int, error) {
	if len(frame) < 2 {
		return len(frame), nil
	}
	switch frame[1] {
	case '[':
		return s.consumeCSI(frame)
	case ']':
		return s.consumeOSC(frame)
	default:
		return 2, nil
	}
}

func (s *renderedScreen) writeRune(value rune, width, height int) error {
	cellWidth := ansi.StringWidth(string(value))
	if cellWidth < 1 {
		cellWidth = 1
	}
	if s.cursorX < 0 || s.cursorX+cellWidth > width {
		return fmt.Errorf("printable cell crosses right margin at x=%d width=%d", s.cursorX, cellWidth)
	}
	if s.cursorY < 0 || s.cursorY >= height {
		return fmt.Errorf("printable cell crosses bottom margin at y=%d", s.cursorY)
	}
	s.cells[s.cursorY][s.cursorX] = renderedCell{value: value, style: s.style.key()}
	s.cursorX += cellWidth
	return nil
}

func (s *renderedScreen) consumeByte(frame []byte, width, height int) (int, error) {
	switch frame[0] {
	case '\x1b':
		return s.consumeEscape(frame)
	case '\r':
		s.cursorX = 0
	case '\n':
		// Bubble Tea renders view rows with LF. The terminal's newline mode
		// returns to column zero as it advances to the next row.
		s.cursorX = 0
		s.cursorY++
	case '\b':
		s.cursorX--
	case '\t':
		s.cursorX = (s.cursorX/8 + 1) * 8
	default:
		if frame[0] < 0x20 {
			return 1, nil
		}
		value, size := utf8.DecodeRune(frame)
		if value == utf8.RuneError && size == 1 {
			return 0, fmt.Errorf("invalid UTF-8")
		}
		if err := s.writeRune(value, width, height); err != nil {
			return 0, err
		}
		return size, nil
	}
	return 1, nil
}

func writeRenderedStyle(output *strings.Builder, current *string, next string) {
	if next == *current {
		return
	}
	if next == "" {
		output.WriteString("\x1b[m")
	} else {
		output.WriteString("\x1b[" + next + "m")
	}
	*current = next
}

func renderedRow(row []renderedCell) string {
	var output strings.Builder
	rowEnd := len(row)
	for rowEnd > 0 && row[rowEnd-1].value == ' ' {
		rowEnd--
	}
	style := ""
	for _, cell := range row[:rowEnd] {
		writeRenderedStyle(&output, &style, cell.style)
		output.WriteRune(cell.value)
	}
	// Trailing spaces are real cells, not discarded output. Encode them
	// as a visible marker so the golden remains diff-check clean.
	for _, cell := range row[rowEnd:] {
		writeRenderedStyle(&output, &style, cell.style)
		output.WriteRune('·')
	}
	return output.String()
}

func (s *renderedScreen) grid() []byte {
	rows := make([]string, len(s.cells))
	for y, row := range s.cells {
		rows[y] = renderedRow(row)
	}
	return []byte(strings.Join(rows, "\n"))
}

func renderedCellGrid(frame []byte, width, height int) ([]byte, error) {
	screen := newRenderedScreen(width, height)
	for i := 0; i < len(frame); {
		consumed, err := screen.consumeByte(frame[i:], width, height)
		if err != nil {
			return nil, fmt.Errorf("parse terminal output at byte %d: %w", i, err)
		}
		if consumed < 1 {
			return nil, fmt.Errorf("terminal parser consumed no bytes at byte %d", i)
		}
		i += consumed
	}
	return screen.grid(), nil
}

func TestRenderedCellGridNormalizesEraseAndCursor(t *testing.T) {
	frame := append([]byte{}, fullScreenClear...)
	frame = append(frame, []byte("header")...)
	frame = append(frame, []byte("\x1b[2;1Hbody\x1b[2;8H\x1b[3X\x1b[3;4Htail")...)
	got, err := renderedCellGrid(frame, 12, 3)
	if err != nil {
		t.Fatal(err)
	}
	want := "header······\nbody········\n   tail·····"
	if string(got) != want {
		t.Fatalf("grid = %q, want %q", got, want)
	}
	first, err := renderedCellGrid(append(append([]byte{}, fullScreenClear...), []byte("\x1b[1mA")...), 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := renderedCellGrid(append(append([]byte{}, fullScreenClear...), []byte("\x1b[0;1mA")...), 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("equivalent SGR grids differ: %q != %q", first, second)
	}
	if _, err := renderedCellGrid(append(append([]byte{}, fullScreenClear...), []byte("abc")...), 2, 2); err == nil {
		t.Fatal("terminal parser accepted printable output beyond the right margin")
	}
	if _, err := renderedCellGrid(append(append([]byte{}, fullScreenClear...), []byte("ab\nc")...), 2, 2); err != nil {
		t.Fatalf("terminal parser rejected a full-width row followed by newline: %v", err)
	}
	if _, err := renderedCellGrid(append(append([]byte{}, fullScreenClear...), []byte("a\nb\nc")...), 2, 2); err == nil {
		t.Fatal("terminal parser accepted printable output below the bottom margin")
	}
}

func TestWideDetailPanelRemainsInsideTerminalGrid(t *testing.T) {
	task := board.Task{
		ID: "wide", Seq: 10, Title: strings.Repeat("wide detail ", 20),
		Desc:   strings.Repeat("## Section\nbody with wide text and an emoji 🧭\n\n", 40),
		Status: board.StatusTodo,
	}
	reader := stubDetailBoardReader{stubBoardReader{board: board.Board{Title: "Work", Tasks: []board.Task{task}}}}
	m := NewModel(reader, nil, "alice")
	completeBoardLoad(t, &m, m.Init())
	updateTestModel(t, &m, tea.WindowSizeMsg{Width: 427, Height: 73})
	load := updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEnter})
	updateTestModel(t, &m, load())

	assertGrid := func(width, height int) {
		t.Helper()
		content := ansi.Strip(m.View().Content)
		lines := strings.Split(content, "\n")
		if len(lines) > height {
			t.Fatalf("%dx%d root detail has %d rows", width, height, len(lines))
		}
		for row, line := range lines {
			if got := ansi.StringWidth(line); got > width {
				t.Fatalf("%dx%d row %d width = %d", width, height, row, got)
			}
		}
		// Spec section 4: the overlay elevates with a shade step and a shadow,
		// so containment is asserted on the panel's own bands, not on a frame.
		for _, edge := range []string{"╭", "╮", "╰", "╯"} {
			if strings.Contains(content, edge) {
				t.Fatalf("%dx%d detail drew border rune %q", width, height, edge)
			}
		}
		if !strings.Contains(content, " Close ") {
			t.Fatalf("%dx%d detail lost its action row", width, height)
		}
	}

	assertGrid(427, 73)
	for range 20 {
		updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyPgDown})
	}
	assertGrid(427, 73)
	updateTestModel(t, &m, tea.WindowSizeMsg{Width: 80, Height: 20})
	assertGrid(80, 20)
	updateTestModel(t, &m, tea.WindowSizeMsg{Width: 427, Height: 73})
	assertGrid(427, 73)
}

func TestWideDetailPTYRendersCompletePanelInsideCellGrid(t *testing.T) {
	task := board.Task{
		ID: "wide", Seq: 10, Title: "Long detail",
		Desc:   strings.Repeat("## Section\nbody with wide text and an emoji 🧭\n\n", 40),
		Status: board.StatusTodo,
	}
	reader := stubDetailBoardReader{stubBoardReader{board: board.Board{Title: "Work", Tasks: []board.Task{task}}}}
	m := NewModel(reader, nil, "alice")
	completeBoardLoad(t, &m, m.Init())
	updateTestModel(t, &m, tea.WindowSizeMsg{Width: 427, Height: 73})
	load := updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEnter})
	updateTestModel(t, &m, load())

	tm := teatest.NewTestModel(t, m,
		teatest.WithInitialTermSize(427, 73),
		teatest.WithProgramOptions(tea.WithColorProfile(colorprofile.ASCII)),
	)
	t.Cleanup(func() { _ = tm.Quit() })
	output := tm.Output()
	var captured bytes.Buffer
	waitFor := func(t *testing.T, marker string) {
		t.Helper()
		teatest.WaitFor(t, io.TeeReader(output, &captured), func(output []byte) bool {
			return bytes.Contains(output, []byte(marker))
		}, teatest.WithDuration(5*time.Second), teatest.WithCheckInterval(10*time.Millisecond))
	}
	assertGrid := func(t *testing.T, width, height int) {
		t.Helper()
		frame, ok := finalFullScreenFrame(captured.Bytes())
		if !ok {
			t.Fatal("detail output did not contain a full-screen frame")
		}
		grid, err := renderedCellGrid(frame, width, height)
		if err != nil {
			t.Fatal(err)
		}
		plain := ansi.Strip(string(grid))
		for _, edge := range []string{"╭", "╮", "╰", "╯"} {
			if strings.Contains(plain, edge) {
				t.Fatalf("cell grid detail drew border rune %q", edge)
			}
		}
		// The grid drops a trailing blank cell at the end of a rendered run, so
		// the button's right padding is not part of the marker.
		if !strings.Contains(plain, " Close") {
			t.Fatal("cell grid lost the detail action row")
		}
	}

	// The scroll position is asserted on the parsed cell grid: a scrolled
	// repetitive body changes one digit of the footer band's scroll hint, and
	// the renderer emits that single cell rather than a matchable run.
	waitForGrid := func(t *testing.T, width, height int, marker string) {
		t.Helper()
		teatest.WaitFor(t, io.TeeReader(output, &captured), func([]byte) bool {
			frame, ok := finalFullScreenFrame(captured.Bytes())
			if !ok {
				return false
			}
			grid, err := renderedCellGrid(frame, width, height)
			if err != nil {
				return false
			}
			return strings.Contains(ansi.Strip(string(grid)), marker)
		}, teatest.WithDuration(5*time.Second), teatest.WithCheckInterval(10*time.Millisecond))
	}

	waitFor(t, " Close ")
	assertGrid(t, 427, 73)
	tm.Send(tea.KeyPressMsg{Code: tea.KeyPgDown})
	waitForGrid(t, 427, 73, "9/")
	assertGrid(t, 427, 73)
	tm.Send(tea.MouseWheelMsg{X: 213, Y: 36, Button: tea.MouseWheelDown})
	waitForGrid(t, 427, 73, "12/")
	assertGrid(t, 427, 73)
	tm.Send(tea.WindowSizeMsg{Width: 80, Height: 20})
	waitFor(t, " Close ")
	assertGrid(t, 80, 20)
	tm.Send(tea.WindowSizeMsg{Width: 427, Height: 73})
	waitFor(t, " Close ")
	assertGrid(t, 427, 73)
	tm.Send(tea.KeyPressMsg{Code: 'q'})
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))
}

func TestEmptyBoardGolden(t *testing.T) {
	m := NewModel(stubBoardReader{board: board.Board{Title: "Board"}}, nil, "default")
	// Start from a loaded snapshot so the golden records the frame, not a
	// renderer-timing-dependent diff from "loading" to "ready".
	m.loading = false
	// teatest starts the Bubble Tea program before it delivers the size from
	// WithInitialTermSize. Under race/load, the renderer can therefore write an
	// 80-column first frame and a cursor-positioned resize diff before WaitFor
	// returns. Size the model through its real update contract first so the
	// first rendered frame is already the 120-column golden; teatest still sends
	// the same WindowSizeMsg and exercises that program path.
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 20})
	m = sized.(Model)
	tm := teatest.NewTestModel(t, m,
		teatest.WithInitialTermSize(120, 20),
		teatest.WithProgramOptions(tea.WithColorProfile(colorprofile.ASCII)),
	)
	t.Cleanup(func() { _ = tm.Quit() })
	var captured bytes.Buffer
	teatest.WaitFor(t, io.TeeReader(tm.Output(), &captured), func(output []byte) bool {
		return bytes.Contains(output, []byte("ready | j/k cards"))
	}, teatest.WithDuration(2*time.Second), teatest.WithCheckInterval(10*time.Millisecond))
	frame, ok := finalFullScreenFrame(captured.Bytes())
	if !ok {
		t.Fatal("teatest output did not contain a full-screen frame")
	}
	grid, err := renderedCellGrid(frame, 120, 20)
	if err != nil {
		t.Fatal(err)
	}
	teatest.RequireEqualOutput(t, grid)
	tm.Send(tea.KeyPressMsg{Code: 'q'})
	tm.WaitFinished(t, teatest.WithFinalTimeout(time.Second))
}
