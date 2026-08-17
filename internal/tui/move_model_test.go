package tui

import (
	"bytes"
	"errors"
	"fmt"
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

func (s *moveTestStore) writeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writes
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
	s.board = oracleMoveBoard(s.board, id, *target, *index)
	for _, task := range s.board.Tasks {
		if task.ID == id {
			return task, nil
		}
	}
	return board.Task{}, errors.New("task not found")
}

// oracleMoveBoard is deliberately independent from the preview implementation.
// The fake store is routing scaffolding; sharing production reorder code here
// would let preview and persistence be wrong in exactly the same way.
func oracleMoveBoard(current board.Board, taskID string, target board.Status, index int) board.Board {
	columns := map[board.Status][]board.Task{}
	var moving board.Task
	for _, task := range current.Tasks {
		if task.ID == taskID {
			moving = task
			continue
		}
		columns[task.Status] = append(columns[task.Status], task)
	}
	moving.Status = target
	destination := columns[target]
	index = min(max(index, 0), len(destination))
	destination = append(destination[:index], append([]board.Task{moving}, destination[index:]...)...)
	columns[target] = destination
	next := board.Board{Title: current.Title}
	for _, status := range boardStatuses {
		for position, task := range columns[status] {
			task.Position = position
			next.Tasks = append(next.Tasks, task)
		}
	}
	return next
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
		updateTestModel(t, &m, cardMoveStoredMsg{taskID: "a", title: "A", board: canonical})
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
	handler := boardMouseHandler(hits, false)
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
		teatest.WithDuration(5*time.Second), teatest.WithCheckInterval(10*time.Millisecond))
	for _, key := range []tea.KeyPressMsg{
		{Code: tea.KeySpace},
		{Code: tea.KeyRight},
		{Code: tea.KeyDown},
		{Code: tea.KeyEnter},
	} {
		tm.Send(key)
	}
	teatest.WaitFor(t, output, func(got []byte) bool {
		// Bubble Tea renders the Dropping -> Dropped change as an in-place
		// two-cell diff in some terminals. Pair the visible position with the
		// synchronized fake-store receipt instead of depending on diff shape.
		return bytes.Contains(got, []byte("position 2 of 3")) && s.writeCount() == 1
	}, teatest.WithDuration(5*time.Second), teatest.WithCheckInterval(10*time.Millisecond))
	tm.Send(tea.KeyPressMsg{Code: 'q'})
	tm.WaitFinished(t, teatest.WithFinalTimeout(time.Second))
}

func TestMoveModelCoverageEdges(t *testing.T) {
	current := moveFixture()
	allStatuses := []board.Status{board.StatusTodo, board.StatusDoing, board.StatusDone}

	var empty cardMoveState
	if _, ok := empty.previewKey("down"); ok {
		t.Fatal("preview without lift succeeded")
	}
	if _, ok := empty.previewMouse(board.StatusTodo, ""); ok {
		t.Fatal("mouse preview without lift succeeded")
	}
	if got := empty.cancel(""); len(got.Tasks) != 0 {
		t.Fatalf("empty cancel = %+v", got)
	}
	empty.announcePosition("ignored")

	missing := board.Task{ID: "missing", Title: "Missing", Status: board.StatusTodo}
	empty.begin(current, missing, allStatuses, false)
	if empty.lifted.slot != 3 {
		t.Fatalf("missing-card slot = %d", empty.lifted.slot)
	}
	empty.saving = true
	if _, ok := empty.previewKey("down"); ok {
		t.Fatal("preview while saving succeeded")
	}
	if _, ok := empty.previewMouse(board.StatusTodo, ""); ok {
		t.Fatal("mouse preview while saving succeeded")
	}

	var keyboard cardMoveState
	keyboard.begin(current, taskNamed(t, current, "A"), allStatuses, false)
	if _, ok := keyboard.previewKey("unknown"); ok {
		t.Fatal("unknown move key was handled")
	}
	keyboard.previewKey("l")
	keyboard.previewKey("h")
	if _, ok := keyboard.previewMouse(board.StatusDoing, "x"); ok {
		t.Fatal("keyboard lift accepted mouse preview")
	}

	var mouse cardMoveState
	mouse.begin(current, taskNamed(t, current, "A"), []board.Status{board.StatusTodo}, true)
	if _, ok := mouse.previewMouse("bogus", ""); ok {
		t.Fatal("invalid mouse status was handled")
	}
	if _, ok := mouse.previewMouse(board.StatusDoing, "x"); ok {
		t.Fatal("hidden mouse column was handled")
	}
	if preview, changed := mouse.previewMouse(board.StatusTodo, "a"); changed || len(preview.Tasks) != 0 {
		t.Fatal("motion over lifted card rebuilt preview")
	}
	if preview, ok := mouse.previewMouse(board.StatusTodo, ""); !ok || columnNames(preview, board.StatusTodo) != "B,C,A" {
		t.Fatalf("blank-column mouse preview = %q,%v", columnNames(preview, board.StatusTodo), ok)
	}

	filtered := board.Board{Tasks: []board.Task{{ID: "a", Status: board.StatusTodo}}}
	if got := visibleSlotToFullColumnIndex(filtered, board.StatusTodo, "", []string{"gone"}, 0); got != 1 {
		t.Fatalf("missing visible anchor = %d", got)
	}
	if got := moveTaskInBoard(current, "gone", board.StatusDone, -5); columnNames(got, board.StatusTodo) != "A,B,C" {
		t.Fatalf("missing move changed board = %q", columnNames(got, board.StatusTodo))
	}
	if got := moveTaskInBoard(current, "a", board.StatusDoing, -5); columnNames(got, board.StatusDoing) != "A,X,Y" {
		t.Fatalf("negative preview index = %q", columnNames(got, board.StatusDoing))
	}
	if containsStatus(allStatuses, board.StatusCancelled) || statusIndexExact("bogus") != -1 {
		t.Fatal("status helpers accepted unknown values")
	}
	for status, want := range map[board.Status]string{
		board.StatusTodo: "To Do", board.StatusDoing: "Doing", board.StatusDone: "Done",
		board.StatusCancelled: "Cancelled", board.Status("bogus"): "bogus",
	} {
		if got := statusLabelTitle(status); got != want {
			t.Errorf("statusLabelTitle(%q) = %q, want %q", status, got, want)
		}
	}
}

func TestMoveRootRoutingCoverageEdges(t *testing.T) {
	s := &moveTestStore{board: moveFixture()}

	t.Run("saving ignores additional input", func(t *testing.T) {
		m := loadedMoveModel(s)
		updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeySpace})
		m.move.saving = true
		if command := updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyDown}); command != nil || !m.move.saving {
			t.Fatalf("saving input = command %v state %#v", command, m.move)
		}
		if command := m.startBoardLoad(); command != nil || !m.reloadPending {
			t.Fatalf("saving load = command %v pending %v", command, m.reloadPending)
		}
	})

	t.Run("escape and focus changes cancel", func(t *testing.T) {
		m := loadedMoveModel(s)
		updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeySpace})
		updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEsc})
		if m.move.lifted != nil || !strings.Contains(m.move.status, "cancelled") {
			t.Fatalf("escape state = %#v", m.move)
		}
		updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeySpace})
		updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyTab})
		if m.move.lifted != nil || m.boardView.column != 1 {
			t.Fatalf("tab focus change = move %#v column %d", m.move, m.boardView.column)
		}
	})

	t.Run("loading and empty boards cannot lift", func(t *testing.T) {
		m := loadedMoveModel(s)
		m.loading = true
		updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeySpace})
		if m.move.lifted != nil {
			t.Fatal("loading board lifted a card")
		}
		m.loading, m.board = false, board.Board{}
		updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeySpace})
		if m.move.lifted != nil {
			t.Fatal("empty board lifted a card")
		}
	})

	t.Run("click and column focus cancel keyboard lift", func(t *testing.T) {
		m := loadedMoveModel(s)
		updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeySpace})
		updateTestModel(t, &m, boardColumnClickedMsg{status: board.StatusDoing})
		if m.move.lifted != nil || m.boardView.column != 1 {
			t.Fatalf("column click = move %#v column %d", m.move, m.boardView.column)
		}
		m.boardView.column = 0
		updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeySpace})
		updateTestModel(t, &m, boardCardClickedMsg{taskID: "b"})
		if m.move.lifted != nil || !m.detail.IsOpen() {
			t.Fatalf("card click = move %#v detail %v", m.move, m.detail.IsOpen())
		}
	})

	t.Run("pointer guards and click release", func(t *testing.T) {
		m := loadedMoveModel(s)
		m.loading = true
		updateTestModel(t, &m, boardPointerDownMsg{taskID: "a"})
		m.loading = false
		updateTestModel(t, &m, boardPointerDownMsg{taskID: "missing"})
		if m.move.lifted != nil {
			t.Fatal("guarded pointer down lifted a card")
		}
		updateTestModel(t, &m, boardPointerUpMsg{})
		updateTestModel(t, &m, boardPointerDownMsg{taskID: "a"})
		if command := updateTestModel(t, &m, boardPointerUpMsg{}); command != nil || !m.detail.IsOpen() {
			t.Fatalf("click release = command %v detail %v", command, m.detail.IsOpen())
		}
	})
}

func TestMoveBoardViewCoverageEdges(t *testing.T) {
	if got := taskIndex(moveFixture(), board.StatusDone, "missing"); got != 0 {
		t.Fatalf("missing task index = %d", got)
	}
	if body, hits := joinColumns(nil); body != "" || hits != nil {
		t.Fatalf("empty columns = %q,%v", body, hits)
	}
	if got := settingsBoardFooter("ready", "off", 1); got != "q" {
		t.Fatalf("tiny footer = %q", got)
	}
	hits := []boardHit{{x1: 5, y1: 5, status: board.StatusDoing}}
	handler := boardMouseHandler(hits, false)
	if command := handler(tea.MouseClickMsg{X: 1, Y: 1, Button: tea.MouseLeft}); command == nil {
		t.Fatal("column click was ignored")
	} else if msg := command().(boardColumnClickedMsg); msg.status != board.StatusDoing {
		t.Fatalf("column click status = %s", msg.status)
	}
	if command := handler(tea.MouseMotionMsg{X: 9, Y: 9, Button: tea.MouseLeft}); command != nil {
		t.Fatalf("off-board motion = %v", command)
	}
	if command := handler(tea.MouseWheelMsg{X: 1, Y: 1, Button: tea.MouseLeft}); command != nil {
		t.Fatalf("left-button wheel = %v", command)
	}
}

func TestMouseReleaseProtocolsClickAndDrag(t *testing.T) {
	protocols := []struct {
		name   string
		button tea.MouseButton
	}{
		{name: "SGR", button: tea.MouseLeft},
		{name: "X10", button: tea.MouseNone},
	}
	for _, protocol := range protocols {
		for _, drag := range []bool{false, true} {
			name := protocol.name + "/click"
			if drag {
				name = protocol.name + "/drag"
			}
			t.Run(name, func(t *testing.T) {
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
				down := boardMouseHandler(hits, false)(tea.MouseClickMsg{
					X: source.x0 + 1, Y: source.y0, Button: tea.MouseLeft,
				})
				updateTestModel(t, &m, down())
				active := boardMouseHandler(hits, true)
				if drag {
					motion := active(tea.MouseMotionMsg{
						X: destination.x0 + 1, Y: destination.y0, Button: tea.MouseLeft,
					})
					updateTestModel(t, &m, motion())
				}
				release := active(tea.MouseReleaseMsg{
					X: destination.x0 + 1, Y: destination.y0, Button: protocol.button,
				})
				if release == nil {
					t.Fatalf("%s release was ignored", protocol.name)
				}
				command := updateTestModel(t, &m, release())
				if drag {
					if command == nil {
						t.Fatal("drag release did not start drop")
					}
					updateTestModel(t, &m, command())
					if s.writes != 1 || s.target != board.StatusDoing {
						t.Fatalf("drag write = %d/%s", s.writes, s.target)
					}
				} else if !m.detail.IsOpen() || s.writes != 0 {
					t.Fatalf("click release = detail %v writes %d", m.detail.IsOpen(), s.writes)
				}
			})
		}
	}
	if command := boardMouseHandler(nil, false)(tea.MouseReleaseMsg{Button: tea.MouseNone}); command != nil {
		t.Fatalf("unrelated X10 release produced %v", command)
	}
}

func TestMoveFooterSanitizesTitlesStatusesAndStoreErrors(t *testing.T) {
	hostile := "\x1b]8;;https://evil.example\x07pwn\x1b]8;;\x07\x1b[31mred\x1b[0m\x00\x01\x7f\u0085"
	assertSafeFooter := func(t *testing.T, model Model, wants ...string) {
		t.Helper()
		lines := strings.Split(model.render(), "\n")
		footer := lines[len(lines)-1]
		for _, want := range wants {
			if !strings.Contains(footer, want) {
				t.Errorf("footer missing %q: %q", want, footer)
			}
		}
		for _, r := range footer {
			if r <= 0x1f || (r >= 0x7f && r <= 0x9f) {
				t.Fatalf("footer retained control U+%04X: %q", r, footer)
			}
		}
		if strings.Contains(footer, "evil.example") || strings.ContainsRune(footer, '\x1b') {
			t.Fatalf("footer retained terminal control payload: %q", footer)
		}
	}

	t.Run("lift title and status", func(t *testing.T) {
		s := &moveTestStore{board: moveFixture()}
		s.board.Tasks[0].Title = hostile
		m := loadedMoveModel(s)
		updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeySpace})
		assertSafeFooter(t, m, "pwnred", "Arrows or hjkl")
	})

	t.Run("store error", func(t *testing.T) {
		s := &moveTestStore{board: moveFixture(), writeErr: errors.New(hostile)}
		m := loadedMoveModel(s)
		updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeySpace})
		drop := updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEnter})
		updateTestModel(t, &m, drop())
		assertSafeFooter(t, m, "Move failed for A", "pwnred")
	})
}

func TestRepeatedSameCellMotionDoesNotRebuildLargePreview(t *testing.T) {
	const cards = 20_000
	current := board.Board{Title: "Large"}
	for i := 0; i < cards; i++ {
		current.Tasks = append(current.Tasks, board.Task{
			ID: fmt.Sprintf("t-%d", i), Title: fmt.Sprintf("Task %d", i), Status: board.StatusTodo,
		})
	}
	current.Tasks = append(current.Tasks, board.Task{ID: "doing", Title: "Doing", Status: board.StatusDoing})
	var state cardMoveState
	state.begin(current, current.Tasks[0], []board.Status{board.StatusTodo, board.StatusDoing}, true)
	preview, changed := state.previewMouse(board.StatusTodo, "t-10000")
	if !changed || len(preview.Tasks) != len(current.Tasks) {
		t.Fatalf("meaningful motion = changed %v tasks %d", changed, len(preview.Tasks))
	}
	rebuilds := 0
	allocations := testing.AllocsPerRun(1000, func() {
		preview, changed := state.previewMouse(board.StatusTodo, "t-10000")
		if changed || len(preview.Tasks) != 0 {
			rebuilds++
		}
	})
	if rebuilds != 0 || allocations != 0 {
		t.Fatalf("same-cell motion rebuilt preview %d times with %.1f allocations/run", rebuilds, allocations)
	}
}

func TestDropAnnouncementUsesCanonicalTaskStatusTitleAndPosition(t *testing.T) {
	s := &moveTestStore{board: moveFixture()}
	m := loadedMoveModel(s)
	updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeySpace})
	updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyRight}) // requested Doing
	canonical := moveFixture()
	canonical.Tasks = append(canonical.Tasks, board.Task{
		ID: "killed-first", Title: "Killed first", Status: board.StatusCancelled,
	})
	for i := range canonical.Tasks {
		if canonical.Tasks[i].ID == "a" {
			canonical.Tasks[i].Title = "Renamed concurrently"
			canonical.Tasks[i].Status = board.StatusCancelled
		}
	}
	updateTestModel(t, &m, cardMoveStoredMsg{taskID: "a", title: "A", board: canonical})
	if m.move.statusError || m.move.status != "Dropped Renamed concurrently, Cancelled, position 1 of 2" {
		t.Fatalf("canonical announcement = error %v status %q", m.move.statusError, m.move.status)
	}
	if m.boardView.column == statusIndex(board.StatusCancelled) {
		t.Fatal("hidden Cancelled task stole visible focus")
	}
}

func TestActiveMoveStatusPrecedesLingeringErrors(t *testing.T) {
	for name, install := range map[string]func(*Model){
		"load":       func(m *Model) { m.loadErr = errors.New("old load error") },
		"poll":       func(m *Model) { m.pollErr = errors.New("old poll error") },
		"preference": func(m *Model) { m.preferenceErr = errors.New("old preference error") },
	} {
		t.Run(name, func(t *testing.T) {
			s := &moveTestStore{board: moveFixture()}
			m := loadedMoveModel(s)
			install(&m)
			updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeySpace})
			footer := lastRenderLine(m)
			if !strings.Contains(footer, "Lifted A") || strings.Contains(footer, "old ") {
				t.Fatalf("active footer = %q", footer)
			}
			updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEsc})
			footer = lastRenderLine(m)
			if !strings.Contains(footer, "old ") {
				t.Fatalf("restored error footer = %q", footer)
			}
		})
	}
}

func lastRenderLine(model Model) string {
	lines := strings.Split(ansi.Strip(model.render()), "\n")
	return lines[len(lines)-1]
}

func TestPreviewMatchesRealSQLiteIndexedMoves(t *testing.T) {
	for _, test := range []struct {
		name       string
		titles     []string
		statuses   []board.Status
		moving     string
		target     board.Status
		visible    []string
		slot       int
		wantColumn string
	}{
		{
			name: "same column", titles: []string{"A", "B", "C"},
			statuses: []board.Status{board.StatusTodo, board.StatusTodo, board.StatusTodo},
			moving:   "B", target: board.StatusTodo, visible: []string{"A", "C"}, slot: 2,
			wantColumn: "A,C,B",
		},
		{
			name: "cross column", titles: []string{"A", "X", "Y"},
			statuses: []board.Status{board.StatusTodo, board.StatusDoing, board.StatusDoing},
			moving:   "A", target: board.StatusDoing, visible: []string{"X", "Y"}, slot: 1,
			wantColumn: "X,A,Y",
		},
		{
			name: "filtered append", titles: []string{"hidden-0", "visible-a", "hidden-1", "visible-b", "hidden-2", "moving"},
			statuses: []board.Status{board.StatusTodo, board.StatusTodo, board.StatusTodo, board.StatusTodo, board.StatusTodo, board.StatusDoing},
			moving:   "moving", target: board.StatusTodo, visible: []string{"visible-a", "visible-b"}, slot: 2,
			wantColumn: "hidden-0,visible-a,hidden-1,visible-b,hidden-2,moving",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			st, err := store.Open(t.TempDir()+"/kb.db", []byte("preview-store-parity"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = st.Close() })
			ids := map[string]string{}
			for i, title := range test.titles {
				added, addErr := st.AddTask("u", board.Task{Title: title, Status: test.statuses[i]})
				if addErr != nil {
					t.Fatal(addErr)
				}
				ids[title] = added.ID
			}
			canonical, err := st.Board("u")
			if err != nil {
				t.Fatal(err)
			}
			visibleIDs := make([]string, len(test.visible))
			for i, title := range test.visible {
				visibleIDs[i] = ids[title]
			}
			lift := cardLift{
				canonical: canonical, taskID: ids[test.moving], title: test.moving,
				target: test.target, slot: test.slot,
				visibleIDs: map[board.Status][]string{test.target: visibleIDs},
			}
			preview := previewLift(lift)
			index := visibleSlotToFullColumnIndex(canonical, test.target, lift.taskID, visibleIDs, test.slot)
			if _, err := st.UpdateAndMoveTask("u", lift.taskID, store.TaskPatch{}, &test.target, &index, nil); err != nil {
				t.Fatal(err)
			}
			persisted, err := st.Board("u")
			if err != nil {
				t.Fatal(err)
			}
			if got := columnNames(preview, test.target); got != test.wantColumn {
				t.Fatalf("preview = %q, want %q", got, test.wantColumn)
			}
			if got := columnNames(persisted, test.target); got != test.wantColumn {
				t.Fatalf("persisted = %q, want %q", got, test.wantColumn)
			}
		})
	}
}
