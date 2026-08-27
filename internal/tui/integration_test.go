package tui

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/board"
)

func TestIntegratedFilterEditorRoutingAndRefresh(t *testing.T) {
	st := newSettingsTestStore(t)
	task, err := st.AddTask("alice", board.Task{
		Title: "Original card", Status: board.StatusTodo, Prio: 3, Tags: []string{"bug"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddTask("alice", board.Task{
		Title: "Hidden card", Status: board.StatusTodo, Prio: 3, Tags: []string{"ui"},
	}); err != nil {
		t.Fatal(err)
	}

	m := newTestRootModel(st, nil, "alice")
	completeBoardLoad(t, &m, m.Init())
	updateTestModel(t, &m, tea.KeyPressMsg{Code: '/'})
	for _, key := range "ne" {
		updateTestModel(t, &m, tea.KeyPressMsg{Code: key, Text: string(key)})
	}
	if m.editor.IsOpen() || m.filter.input.Value() != "ne" {
		t.Fatalf("focused filter routed n/e to editor: open=%v text=%q", m.editor.IsOpen(), m.filter.input.Value())
	}

	m.filter.input.SetValue("")
	m.filter.tags = []string{"bug"}
	m.filter.blur()
	m.boardView.adoptBoard(m.board, m.filteredBoard())
	loadLabels := updateTestModel(t, &m, tea.KeyPressMsg{Code: 'e'})
	if !m.editor.IsOpen() || m.editor.TaskID() != task.ID {
		t.Fatalf("filtered edit route = open:%v id:%q", m.editor.IsOpen(), m.editor.TaskID())
	}
	if loadLabels != nil {
		updateTestModel(t, &m, loadLabels())
	}
	updateTestModel(t, &m, tea.KeyPressMsg{Code: 'X', Text: "X"})

	beforeFilter := m.filter.value()
	updateTestModel(t, &m, boardPointerDownMsg{taskID: task.ID})
	updateTestModel(t, &m, filterLabelClickedMsg{tag: "ui"})
	if m.move.lifted != nil || !reflect.DeepEqual(m.filter.value(), beforeFilter) {
		t.Fatalf("editor leaked board mouse input: move=%#v filter=%+v", m.move, m.filter.value())
	}

	remote := m.board
	for i := range remote.Tasks {
		if remote.Tasks[i].ID == task.ID {
			remote.Tasks[i].Title = "Remote card"
		}
	}
	updateTestModel(t, &m, boardLoadedMsg{board: remote})
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "Original cardX") || !strings.Contains(view, "current edits were preserved") {
		t.Fatalf("filtered refresh overwrote dirty editor:\n%s", view)
	}
	if got := len(m.filteredBoard().Tasks); got != 1 {
		t.Fatalf("filtered refresh exposed %d tasks, want 1", got)
	}
}

func TestIntegratedSavedSelectionRespectsFilterVisibility(t *testing.T) {
	visible := board.Task{ID: "visible", Title: "keep existing", Status: board.StatusTodo}
	created := board.Task{ID: "created", Title: "keep created", Status: board.StatusTodo}
	hidden := board.Task{ID: "hidden", Title: "discarded", Status: board.StatusTodo}
	m := newTestRootModel(stubBoardReader{}, nil, "alice")
	m.loading = false
	m.board = board.Board{Tasks: []board.Task{visible, hidden}}
	m.filter.input.SetValue("keep")
	m.boardView.focusTask(m.filteredBoard(), visible.ID)

	m.selectAfterLoad = created.ID
	m.finishBoardLoad(boardLoadedMsg{board: board.Board{Tasks: []board.Task{visible, created, hidden}}})
	if selected, ok := m.selectedTask(); !ok || selected.ID != created.ID || m.selectAfterLoad != "" {
		t.Fatalf("visible saved selection = %+v,%v pending=%q", selected, ok, m.selectAfterLoad)
	}

	m.selectAfterLoad = hidden.ID
	m.finishBoardLoad(boardLoadedMsg{board: m.board})
	if selected, ok := m.selectedTask(); !ok || selected.ID != created.ID || m.selectAfterLoad != "" {
		t.Fatalf("hidden saved selection disturbed board = %+v,%v pending=%q", selected, ok, m.selectAfterLoad)
	}
}

func TestIntegratedSavedSelectionWaitsForFreshSuccessor(t *testing.T) {
	old := board.Task{ID: "old", Title: "keep old", Status: board.StatusTodo}
	visible := board.Task{ID: "visible", Title: "keep visible", Status: board.StatusTodo}
	hidden := board.Task{ID: "hidden", Title: "filtered out", Status: board.StatusTodo}
	for _, test := range []struct {
		name    string
		savedID string
		fresh   board.Board
		wantID  string
	}{
		{name: "visible", savedID: visible.ID, fresh: board.Board{Tasks: []board.Task{old, visible}}, wantID: visible.ID},
		{name: "hidden", savedID: hidden.ID, fresh: board.Board{Tasks: []board.Task{old, hidden}}, wantID: old.ID},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := newTestRootModel(stubBoardReader{board: test.fresh}, nil, "alice")
			m.board = board.Board{Tasks: []board.Task{old}}
			m.filter.input.SetValue("keep")
			m.boardView.focusTask(m.filteredBoard(), old.ID)
			m.loading = true
			m.reloadPending = true
			m.selectAfterLoad = test.savedID

			successor := m.finishBoardLoad(boardLoadedMsg{board: board.Board{Tasks: []board.Task{old}}})
			if successor == nil || m.selectAfterLoad != test.savedID {
				t.Fatalf("stale load discarded saved id: successor=%v pending=%q", successor, m.selectAfterLoad)
			}
			completeBoardLoad(t, &m, successor)
			selected, ok := m.selectedTask()
			if !ok || selected.ID != test.wantID || m.selectAfterLoad != "" {
				t.Fatalf("fresh successor selection = %+v,%v pending=%q", selected, ok, m.selectAfterLoad)
			}
		})
	}
}

func TestIntegratedMoveCancellationRestoresIdentity(t *testing.T) {
	t.Run("edit", func(t *testing.T) {
		m, first, _ := integratedMultiCardModel(t)
		previewFirstBelowSecond(t, &m, first.ID)
		updateTestModel(t, &m, tea.KeyPressMsg{Code: 'e'})
		if m.move.lifted != nil || !m.editor.IsOpen() || m.editor.TaskID() != first.ID {
			t.Fatalf("edit after cancel = move:%#v editor:%v task:%q", m.move, m.editor.IsOpen(), m.editor.TaskID())
		}
	})

	t.Run("new", func(t *testing.T) {
		m, first, _ := integratedMultiCardModel(t)
		previewFirstBelowSecond(t, &m, first.ID)
		updateTestModel(t, &m, tea.KeyPressMsg{Code: 'n'})
		selected, ok := m.selectedTask()
		if m.move.lifted != nil || !m.editor.IsOpen() || !ok || selected.ID != first.ID {
			t.Fatalf("new after cancel = move:%#v editor:%v selected:%+v,%v", m.move, m.editor.IsOpen(), selected, ok)
		}
	})

	t.Run("filter hides lifted card", func(t *testing.T) {
		m, first, second := integratedMultiCardModel(t)
		previewFirstBelowSecond(t, &m, first.ID)
		updateTestModel(t, &m, filterLabelClickedMsg{tag: "ui"})
		selected, ok := m.selectedTask()
		if m.move.lifted != nil || !reflect.DeepEqual(m.filter.tags, []string{"ui"}) || !ok || selected.ID != second.ID {
			t.Fatalf("filter after cancel = move:%#v tags:%v selected:%+v,%v", m.move, m.filter.tags, selected, ok)
		}
	})

	t.Run("watcher refresh", func(t *testing.T) {
		m, first, _ := integratedMultiCardModel(t)
		previewFirstBelowSecond(t, &m, first.ID)
		load := m.requireFreshBoard()
		selected, ok := m.selectedTask()
		if load == nil || m.move.lifted != nil || !ok || selected.ID != first.ID {
			t.Fatalf("watcher cancel = load:%v move:%#v selected:%+v,%v", load, m.move, selected, ok)
		}
		completeBoardLoad(t, &m, load)
		selected, ok = m.selectedTask()
		if !ok || selected.ID != first.ID {
			t.Fatalf("watcher refresh selection = %+v,%v", selected, ok)
		}
	})
}

func TestIntegratedHungMoveWriteStillQuits(t *testing.T) {
	m, first, _ := integratedMultiCardModel(t)
	previewFirstBelowSecond(t, &m, first.ID)
	m.move.saving = true
	quit := updateTestModel(t, &m, tea.KeyPressMsg{Code: 'q'})
	if quit == nil || !m.stopped {
		t.Fatalf("q during move write = command:%v stopped:%v", quit, m.stopped)
	}
}

func TestIntegratedFilteredMovePreservesHiddenOrder(t *testing.T) {
	current := board.Board{Tasks: []board.Task{
		{ID: "hidden-0", Title: "H0", Status: board.StatusTodo},
		{ID: "visible-a", Title: "A", Status: board.StatusTodo, Tags: []string{"bug"}},
		{ID: "moving", Title: "M", Status: board.StatusTodo, Tags: []string{"bug"}},
		{ID: "hidden-1", Title: "H1", Status: board.StatusTodo},
		{ID: "visible-b", Title: "B", Status: board.StatusTodo, Tags: []string{"bug"}},
		{ID: "hidden-2", Title: "H2", Status: board.StatusTodo},
	}}
	filter := newBoardFilterState()
	filter.tags = []string{"bug"}
	visible := filter.project(current)
	moving, _ := boardTaskByID(current, "moving")
	statuses := []board.Status{board.StatusTodo, board.StatusDoing, board.StatusDone}

	var keyboard cardMoveState
	keyboard.beginVisible(current, visible, moving, statuses, false)
	preview, handled := keyboard.previewKey("up")
	if !handled || columnNames(preview, board.StatusTodo) != "H0,M,A,H1,B,H2" {
		t.Fatalf("filtered key preview = %q handled=%v", columnNames(preview, board.StatusTodo), handled)
	}
	if got := hiddenIDs(preview); !reflect.DeepEqual(got, []string{"hidden-0", "hidden-1", "hidden-2"}) {
		t.Fatalf("filtered key move reordered hidden cards: %v", got)
	}

	var mouse cardMoveState
	mouse.beginVisible(current, visible, moving, statuses, true)
	preview, handled = mouse.previewMouse(board.StatusTodo, "")
	if !handled || columnNames(preview, board.StatusTodo) != "H0,A,H1,B,H2,M" {
		t.Fatalf("filtered mouse preview = %q handled=%v", columnNames(preview, board.StatusTodo), handled)
	}
	if got := hiddenIDs(preview); !reflect.DeepEqual(got, []string{"hidden-0", "hidden-1", "hidden-2"}) {
		t.Fatalf("filtered mouse move reordered hidden cards: %v", got)
	}
}

func TestIntegratedMouseHitAndReleaseProtocols(t *testing.T) {
	handler := boardMouseHandler([]boardHit{
		{x0: 0, x1: 8, y0: 1, y1: 2, kind: boardHitDefault, taskID: "card", status: board.StatusTodo},
		{x0: 0, x1: 8, y0: 1, y1: 2, kind: boardHitFilterLabel, tag: "bug"},
	}, true)
	click := handler(tea.MouseClickMsg{X: 1, Y: 1, Button: tea.MouseLeft})
	if click == nil {
		t.Fatal("filter label click was ignored")
	}
	if msg, ok := click().(filterLabelClickedMsg); !ok || msg.tag != "bug" {
		t.Fatalf("filter label became drag anchor: %#v", msg)
	}
	motion := handler(tea.MouseMotionMsg{X: 1, Y: 1, Button: tea.MouseLeft})
	if motion == nil {
		t.Fatal("card tag did not retain its drag anchor")
	}
	if msg, ok := motion().(boardPointerMoveMsg); !ok || msg.beforeTaskID != "card" {
		t.Fatalf("card tag drag anchor = %#v", msg)
	}
	for _, button := range []tea.MouseButton{tea.MouseLeft, tea.MouseNone} {
		release := handler(tea.MouseReleaseMsg{Button: button})
		if release == nil {
			t.Fatalf("active pointer release %v was ignored", button)
		}
		if _, ok := release().(boardPointerUpMsg); !ok {
			t.Fatalf("active pointer release %v = %T", button, release())
		}
	}
	if release := boardMouseHandler(nil, false)(tea.MouseReleaseMsg{Button: tea.MouseNone}); release == nil {
		t.Fatal("X10 release did not reach model-owned capture validation")
	} else if message, ok := release().(boardPointerUpMsg); !ok || !message.resolved || message.valid {
		t.Fatalf("X10 release resolved as %#v", message)
	}
}

func TestIntegratedMoveModalAndFooterPrecedence(t *testing.T) {
	st := newSettingsTestStore(t)
	task, err := st.AddTask("alice", board.Task{Title: "Move me", Status: board.StatusTodo, Prio: 3, Tags: []string{"bug"}})
	if err != nil {
		t.Fatal(err)
	}
	m := newTestRootModel(st, nil, "alice")
	completeBoardLoad(t, &m, m.Init())
	m.filter.tags = []string{"bug"}
	m.loadErr = errors.New("stale load error")
	updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeySpace})
	if m.move.lifted == nil {
		t.Fatal("move did not lift filtered card")
	}
	updateTestModel(t, &m, tea.KeyPressMsg{Code: 'n'})
	if !m.editor.IsOpen() || m.move.lifted != nil {
		t.Fatalf("move modal did not cancel before editor: editor=%v move=%#v", m.editor.IsOpen(), m.move)
	}
	updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEscape})
	updateTestModel(t, &m, tea.KeyPressMsg{Code: 'd'})
	updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeySpace})
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "Lifted Move me") || strings.Contains(view, "stale load error") || !strings.Contains(view, "1 of 1 cards") {
		t.Fatalf("active move footer precedence failed:\n%s", view)
	}

	m.move.saving = true
	column := m.boardView.column
	filterBefore := m.filter.value()
	updateTestModel(t, &m, boardColumnClickedMsg{status: board.StatusDoing})
	updateTestModel(t, &m, filterLabelClickedMsg{tag: "ui"})
	updateTestModel(t, &m, boardPointerDownMsg{taskID: task.ID})
	if m.boardView.column != column || !reflect.DeepEqual(m.filter.value(), filterBefore) || m.move.lifted == nil {
		t.Fatalf("saving move accepted modal input: column=%d filter=%+v move=%#v", m.boardView.column, m.filter.value(), m.move)
	}

	m.move.saving = false
	m.move.lifted = nil
	m.move.notice = false
	rebuildTestView(&m)
	view = ansi.Strip(m.View().Content)
	if !strings.Contains(view, "stale load error") || strings.Contains(view, "Lifted Move me") {
		t.Fatalf("stale move notice masked root error:\n%s", view)
	}
}

func hiddenIDs(current board.Board) []string {
	var ids []string
	for _, task := range current.Tasks {
		if strings.HasPrefix(task.ID, "hidden-") {
			ids = append(ids, task.ID)
		}
	}
	return ids
}

func integratedMultiCardModel(t *testing.T) (Model, board.Task, board.Task) {
	t.Helper()
	st := newSettingsTestStore(t)
	first, err := st.AddTask("alice", board.Task{
		Title: "A", Status: board.StatusTodo, Prio: 3, Tags: []string{"bug"},
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.AddTask("alice", board.Task{
		Title: "B", Status: board.StatusTodo, Prio: 3, Tags: []string{"ui"},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := newTestRootModel(st, nil, "alice")
	completeBoardLoad(t, &m, m.Init())
	if !m.boardView.focusTask(m.filteredBoard(), first.ID) {
		t.Fatal("could not focus first card")
	}
	return m, first, second
}

func previewFirstBelowSecond(t *testing.T, m *Model, firstID string) {
	t.Helper()
	updateTestModel(t, m, tea.KeyPressMsg{Code: tea.KeySpace})
	updateTestModel(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	selected, ok := m.selectedTask()
	if m.move.lifted == nil || !ok || selected.ID != firstID || m.boardView.rows[0] != 1 {
		t.Fatalf("preview setup = move:%#v selected:%+v,%v row:%d", m.move, selected, ok, m.boardView.rows[0])
	}
}
