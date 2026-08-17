package tui

import (
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
	hidden, err := st.AddTask("alice", board.Task{
		Title: "Hidden card", Status: board.StatusTodo, Prio: 3, Tags: []string{"ui"},
	})
	if err != nil {
		t.Fatal(err)
	}

	m := NewModel(st, nil, "alice")
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
	beforeColumn := m.boardView.column
	updateTestModel(t, &m, filterLabelClickedMsg{tag: "ui"})
	updateTestModel(t, &m, filterClearClickedMsg{})
	updateTestModel(t, &m, boardCardClickedMsg{taskID: hidden.ID})
	updateTestModel(t, &m, boardColumnClickedMsg{status: board.StatusDoing})
	if !reflect.DeepEqual(m.filter.value(), beforeFilter) || m.boardView.column != beforeColumn {
		t.Fatalf("editor leaked board mouse input: filter=%+v column=%d", m.filter.value(), m.boardView.column)
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
	m := NewModel(stubBoardReader{}, nil, "alice")
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
			m := NewModel(stubBoardReader{board: test.fresh}, nil, "alice")
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
