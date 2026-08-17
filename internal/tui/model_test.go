package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/RandomCodeSpace/kb/internal/board"
)

type stubBoardReader struct {
	board board.Board
	err   error
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
	for _, want := range []string{"kb / Work / alice", "[1 TO DO  1]", "one", "DOING", "DONE", "ready"} {
		if !strings.Contains(wide.Content, want) {
			t.Errorf("wide view missing %q:\n%s", want, wide.Content)
		}
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m = updated.(Model)
	updated, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 0})
	m = updated.(Model)
	narrow := m.View().Content
	if !strings.Contains(narrow, "[2 DOING  0]") || strings.Contains(narrow, "TO DO") {
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
			if width >= 3 && !strings.Contains(output, "┌") {
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

func TestEmptyBoardGolden(t *testing.T) {
	m := NewModel(stubBoardReader{board: board.Board{Title: "Board"}}, nil, "default")
	// Start from a loaded snapshot so the golden records the frame, not a
	// renderer-timing-dependent diff from "loading" to "ready".
	m.loading = false
	tm := teatest.NewTestModel(t, m,
		teatest.WithInitialTermSize(120, 20),
		teatest.WithProgramOptions(tea.WithColorProfile(colorprofile.ASCII)),
	)
	t.Cleanup(func() { _ = tm.Quit() })
	var captured bytes.Buffer
	teatest.WaitFor(t, io.TeeReader(tm.Output(), &captured), func(output []byte) bool {
		return bytes.Contains(output, []byte("ready | j/k cards"))
	}, teatest.WithDuration(2*time.Second), teatest.WithCheckInterval(10*time.Millisecond))
	teatest.RequireEqualOutput(t, captured.Bytes())
	tm.Send(tea.KeyPressMsg{Code: 'q'})
	tm.WaitFinished(t, teatest.WithFinalTimeout(time.Second))
}
