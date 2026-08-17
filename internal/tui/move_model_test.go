package tui

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
)

func moveFixture() board.Board {
	stamp := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	return board.Board{Title: "Moves", Tasks: []board.Task{
		{ID: "a", Title: "A", Status: board.StatusTodo, Position: 0, MovedAt: stamp},
		{ID: "b", Title: "B", Status: board.StatusTodo, Position: 1, MovedAt: stamp.Add(time.Hour)},
		{ID: "c", Title: "C", Status: board.StatusTodo, Position: 2, MovedAt: stamp.Add(2 * time.Hour)},
		{ID: "x", Title: "X", Status: board.StatusDoing, Position: 0, MovedAt: stamp},
		{ID: "y", Title: "Y", Status: board.StatusDoing, Position: 1, MovedAt: stamp},
	}}
}

func taskNamed(t *testing.T, current board.Board, title string) board.Task {
	t.Helper()
	for _, task := range current.Tasks {
		if task.Title == title {
			return task
		}
	}
	t.Fatalf("task %q not found", title)
	return board.Task{}
}

func columnNames(current board.Board, status board.Status) string {
	var names []string
	for _, task := range current.Tasks {
		if task.Status == status {
			names = append(names, task.Title)
		}
	}
	return strings.Join(names, ",")
}

func TestCardMovePreviewIsLocalClampedAndDoesNotWrap(t *testing.T) {
	current := moveFixture()
	before := taskNamed(t, current, "B")
	var state cardMoveState
	state.begin(current, before, []board.Status{board.StatusTodo, board.StatusDoing, board.StatusDone}, false)
	if !strings.Contains(state.status, "Arrows or hjkl") || !strings.Contains(state.status, "Enter/Space") {
		t.Fatalf("lift status = %q", state.status)
	}

	preview, handled := state.previewKey("up")
	got := columnNames(preview, board.StatusTodo)
	if !handled || got != "B,A,C" {
		t.Fatalf("preview up = %q handled=%v", got, handled)
	}
	if got := taskNamed(t, preview, "B").MovedAt; !got.Equal(before.MovedAt) {
		t.Fatalf("preview reset age: got %v want %v", got, before.MovedAt)
	}
	preview, _ = state.previewKey("right")
	if got := columnNames(preview, board.StatusDoing); got != "B,X,Y" {
		t.Fatalf("preview right = %q", got)
	}
	preview, _ = state.previewKey("down")
	if got := columnNames(preview, board.StatusDoing); got != "X,B,Y" {
		t.Fatalf("preview down = %q", got)
	}
	if !strings.Contains(state.status, "B, Doing, position 2 of 3") {
		t.Fatalf("position status = %q", state.status)
	}

	state.previewKey("right")
	state.previewKey("right") // Done is the last visible column; no wrap.
	if state.lifted.target != board.StatusDone {
		t.Fatalf("right edge wrapped to %s", state.lifted.target)
	}
	for range 5 {
		state.previewKey("down")
	}
	if state.lifted.slot != 0 || !strings.Contains(state.status, "position 1 of 1") {
		t.Fatalf("empty-column clamp = slot %d status %q", state.lifted.slot, state.status)
	}

	restored := state.cancel("")
	if got := columnNames(restored, board.StatusTodo); got != "A,B,C" || state.lifted != nil {
		t.Fatalf("cancel restored %q state=%#v", got, state)
	}
	if !strings.Contains(state.status, "Move cancelled: B restored") {
		t.Fatalf("cancel status = %q", state.status)
	}
}

func TestVisibleSlotToFullColumnIndexKeepsFilterSemantics(t *testing.T) {
	current := board.Board{Tasks: []board.Task{
		{ID: "hidden-0", Status: board.StatusTodo},
		{ID: "visible-a", Status: board.StatusTodo},
		{ID: "moving", Status: board.StatusDoing},
		{ID: "hidden-1", Status: board.StatusTodo},
		{ID: "visible-b", Status: board.StatusTodo},
		{ID: "hidden-2", Status: board.StatusTodo},
	}}
	visible := []string{"visible-a", "visible-b"}
	for _, test := range []struct {
		slot int
		want int
	}{
		{slot: -5, want: 1},
		{slot: 0, want: 1},
		{slot: 1, want: 3},
		{slot: 2, want: 5}, // past the last visible card appends after hidden-2
		{slot: 99, want: 5},
	} {
		if got := visibleSlotToFullColumnIndex(current, board.StatusTodo, "moving", visible, test.slot); got != test.want {
			t.Errorf("slot %d = full index %d, want %d", test.slot, got, test.want)
		}
	}
}

type moveTestStore struct {
	mu        sync.Mutex
	board     board.Board
	writeErr  error
	reloadErr error
	writes    int
	target    board.Status
	index     int
}

func (s *moveTestStore) Board(string) (board.Board, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneBoard(s.board), s.reloadErr
}

func (s *moveTestStore) UpdateAndMoveTask(
	_ string,
	id string,
	_ store.TaskPatch,
	target *board.Status,
	index *int,
	_ func(board.Task) error,
) (board.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes++
	if target == nil || index == nil {
		return board.Task{}, errors.New("missing explicit destination")
	}
	s.target, s.index = *target, *index
	if s.writeErr != nil {
		return board.Task{}, s.writeErr
	}
	s.board = moveTaskInBoard(s.board, id, *target, *index)
	for _, task := range s.board.Tasks {
		if task.ID == id {
			return task, nil
		}
	}
	return board.Task{}, errors.New("task not found")
}

func loadedMoveModel(s *moveTestStore) Model {
	m := NewModel(s, nil, "u")
	m.loading = false
	m.board = cloneBoard(s.board)
	return m
}

func TestModelKeyboardDropWritesExplicitMoveAndAnnouncesCanonicalPosition(t *testing.T) {
	s := &moveTestStore{board: moveFixture()}
	m := loadedMoveModel(s)
	m.boardView.rows[0] = 1 // B

	updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeySpace})
	if m.move.lifted == nil || !strings.Contains(ansi.Strip(m.View().Content), "Arrows or hjkl") {
		t.Fatalf("lift state/view = %#v\n%s", m.move, ansi.Strip(m.View().Content))
	}
	updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyRight})
	updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyDown})
	drop := updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if drop == nil || !m.move.saving {
		t.Fatalf("drop did not start: %#v command=%v", m.move, drop)
	}
	updateTestModel(t, &m, drop())
	if s.writes != 1 || s.target != board.StatusDoing || s.index != 2 {
		t.Fatalf("store write = count %d target %s index %d", s.writes, s.target, s.index)
	}
	if got := columnNames(m.board, board.StatusDoing); got != "X,Y,B" {
		t.Fatalf("canonical board = %q", got)
	}
	if selected, ok := m.selectedTask(); !ok || selected.ID != "b" {
		t.Fatalf("truthful focus = %+v,%v", selected, ok)
	}
	if got := m.move.status; got != "Dropped B, Doing, position 3 of 3" {
		t.Fatalf("drop status = %q", got)
	}
}

func TestFailedMoveRestoresCanonicalBoardFocusAndError(t *testing.T) {
	want := errors.New("write refused")
	canonical := moveFixture()
	s := &moveTestStore{board: canonical, writeErr: want}
	m := loadedMoveModel(s)
	m.boardView.rows[0] = 1
	updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeySpace})
	updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyDown})
	if got := columnNames(m.board, board.StatusTodo); got != "A,C,B" {
		t.Fatalf("local preview = %q", got)
	}
	drop := updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeySpace})
	updateTestModel(t, &m, drop())
	if got := columnNames(m.board, board.StatusTodo); got != "A,B,C" {
		t.Fatalf("failed write board = %q", got)
	}
	selected, ok := m.selectedTask()
	if !ok || selected.ID != "b" || !m.move.statusError || !strings.Contains(m.move.status, want.Error()) {
		t.Fatalf("failed write focus/status = %+v,%v error=%v status=%q", selected, ok, m.move.statusError, m.move.status)
	}
}

func TestMoveFailurePathsStayTruthful(t *testing.T) {
	t.Run("unsupported store", func(t *testing.T) {
		m := NewModel(stubBoardReader{board: moveFixture()}, nil, "u")
		completeBoardLoad(t, &m, m.Init())
		updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeySpace})
		if command := updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEnter}); command != nil {
			t.Fatalf("unsupported store returned command %v", command)
		}
		if !m.move.statusError || !strings.Contains(m.move.status, "does not support") || columnNames(m.board, board.StatusTodo) != "A,B,C" {
			t.Fatalf("unsupported store = error %v status %q board %q", m.move.statusError, m.move.status, columnNames(m.board, board.StatusTodo))
		}
	})

	t.Run("write and canonical reload fail", func(t *testing.T) {
		s := &moveTestStore{board: moveFixture(), writeErr: errors.New("write failed"), reloadErr: errors.New("reload failed")}
		m := loadedMoveModel(s)
		updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeySpace})
		updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyDown})
		drop := updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEnter})
		reload := updateTestModel(t, &m, drop())
		if reload == nil || !m.loading || columnNames(m.board, board.StatusTodo) != "A,B,C" {
			t.Fatalf("double failure = loading %v board %q command %v", m.loading, columnNames(m.board, board.StatusTodo), reload)
		}
		if !strings.Contains(m.move.status, "write failed") || !strings.Contains(m.move.status, "canonical reload failed") {
			t.Fatalf("double failure status = %q", m.move.status)
		}
	})

	t.Run("successful write missing from canonical board", func(t *testing.T) {
		s := &moveTestStore{board: moveFixture()}
		m := loadedMoveModel(s)
		updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeySpace})
		m.move.saving = true
		canonical := moveFixture()
		canonical.Tasks = canonical.Tasks[1:]
		updateTestModel(t, &m, cardMoveStoredMsg{taskID: "a", title: "A", target: board.StatusTodo, board: canonical})
		if !m.move.statusError || !strings.Contains(m.move.status, "absent") {
			t.Fatalf("missing canonical task status = error %v %q", m.move.statusError, m.move.status)
		}
		if selected, ok := m.selectedTask(); !ok || selected.ID == "a" {
			t.Fatalf("missing canonical task retained stale focus = %+v,%v", selected, ok)
		}
	})
}

func TestWatcherChangeCancelsUnsavedPreviewBeforeRefresh(t *testing.T) {
	s := &moveTestStore{board: moveFixture()}
	m := loadedMoveModel(s)
	m.watcher = stubVersionReader{version: 2}
	m.haveVersion, m.dataVersion = true, 1
	updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeySpace})
	updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyDown})
	command := updateTestModel(t, &m, dataVersionMsg{version: 2})
	if command == nil || !m.loading || m.move.lifted != nil || columnNames(m.board, board.StatusTodo) != "A,B,C" {
		t.Fatalf("watcher cancel = loading %v lifted %v board %q command %v", m.loading, m.move.lifted, columnNames(m.board, board.StatusTodo), command)
	}
	if !strings.Contains(m.move.status, "board changed; refreshing") {
		t.Fatalf("watcher cancel status = %q", m.move.status)
	}
}

func TestMoveSerializesWatcherRefreshBehindStoreWrite(t *testing.T) {
	s := &moveTestStore{board: moveFixture()}
	m := loadedMoveModel(s)
	updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeySpace})
	drop := updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if command := updateTestModel(t, &m, dataVersionMsg{version: 2}); command == nil {
		t.Fatal("watcher update stopped the poll chain")
	}
	if !m.reloadPending || m.loading {
		t.Fatalf("watcher raced write: loading=%v pending=%v", m.loading, m.reloadPending)
	}
	reload := updateTestModel(t, &m, drop())
	if reload == nil || !m.loading || m.reloadPending {
		t.Fatalf("write did not start serialized reload: loading=%v pending=%v command=%v", m.loading, m.reloadPending, reload)
	}
	completeBoardLoad(t, &m, reload)
	if m.loading || m.move.saving || m.move.lifted != nil {
		t.Fatalf("serialized refresh incomplete: %#v", m)
	}
}

func TestMouseDragPreviewsAndDropsBetweenColumns(t *testing.T) {
	s := &moveTestStore{board: moveFixture()}
	m := loadedMoveModel(s)
	m.width, m.height = 140, 20
	_, hits := m.renderBoard()
	var source, destination boardHit
	for _, hit := range hits {
		switch hit.taskID {
		case "a":
			source = hit
		case "y":
			destination = hit
		}
	}
	handler := boardMouseHandler(hits)
	down := handler(tea.MouseClickMsg{X: source.x0 + 1, Y: source.y0, Button: tea.MouseLeft})
	updateTestModel(t, &m, down())
	move := handler(tea.MouseMotionMsg{X: destination.x0 + 1, Y: destination.y0, Button: tea.MouseLeft})
	updateTestModel(t, &m, move())
	if got := columnNames(m.board, board.StatusDoing); got != "X,A,Y" {
		t.Fatalf("mouse preview = %q", got)
	}
	up := handler(tea.MouseReleaseMsg{X: destination.x0 + 1, Y: destination.y0, Button: tea.MouseLeft})
	drop := updateTestModel(t, &m, up())
	updateTestModel(t, &m, drop())
	if s.target != board.StatusDoing || s.index != 1 || columnNames(m.board, board.StatusDoing) != "X,A,Y" {
		t.Fatalf("mouse drop = target %s index %d board %q", s.target, s.index, columnNames(m.board, board.StatusDoing))
	}
}

func TestSameColumnDropPreservesStoredCardAge(t *testing.T) {
	st, err := store.Open(t.TempDir()+"/kb.db", []byte("move-age-test"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	first, err := st.AddTask("u", board.Task{Title: "First"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddTask("u", board.Task{Title: "Second"}); err != nil {
		t.Fatal(err)
	}
	m := NewModel(st, nil, "u")
	completeBoardLoad(t, &m, m.Init())
	updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeySpace})
	updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyDown})
	drop := updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEnter})
	updateTestModel(t, &m, drop())
	after := taskNamed(t, m.board, "First")
	if !after.MovedAt.Equal(first.MovedAt) || after.Position != 1 {
		t.Fatalf("same-column age/position = %v/%d, want %v/1", after.MovedAt, after.Position, first.MovedAt)
	}
}

func TestCardMoveTeatestInteraction(t *testing.T) {
	s := &moveTestStore{board: moveFixture()}
	m := loadedMoveModel(s)
	tm := teatest.NewTestModel(t, m,
		teatest.WithInitialTermSize(120, 20),
		teatest.WithProgramOptions(tea.WithColorProfile(colorprofile.ASCII)),
	)
	t.Cleanup(func() { _ = tm.Quit() })
	var captured bytes.Buffer
	output := io.TeeReader(tm.Output(), &captured)
	teatest.WaitFor(t, output, func(got []byte) bool { return bytes.Contains(got, []byte("ready")) },
		teatest.WithDuration(2*time.Second), teatest.WithCheckInterval(10*time.Millisecond))
	for _, key := range []tea.KeyPressMsg{
		{Code: tea.KeySpace},
		{Code: tea.KeyRight},
		{Code: tea.KeyDown},
		{Code: tea.KeyEnter},
	} {
		tm.Send(key)
	}
	teatest.WaitFor(t, output, func(got []byte) bool {
		return bytes.Contains(got, []byte("Dropped A, Doing, position 2 of 3"))
	}, teatest.WithDuration(2*time.Second), teatest.WithCheckInterval(10*time.Millisecond))
	tm.Send(tea.KeyPressMsg{Code: 'q'})
	tm.WaitFinished(t, teatest.WithFinalTimeout(time.Second))
}
