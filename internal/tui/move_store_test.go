package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/RandomCodeSpace/kb/internal/board"
)

func TestCardCompletionGuard(t *testing.T) {
	if guard := cardCompletionGuard(board.StatusDoing); guard != nil {
		t.Fatalf("non-done target returned a guard")
	}

	guard := cardCompletionGuard(board.StatusDone)
	if guard == nil {
		t.Fatalf("done target returned no guard")
	}
	clear := board.Task{ID: "t1", Title: "Clear"}
	if err := guard(clear); err != nil {
		t.Fatalf("clear task blocked: %v", err)
	}

	blocked := board.Task{ID: "t2", Title: "Blocked", Blocked: true}
	err := guard(blocked)
	if err == nil || !strings.Contains(err.Error(), `t2 "Blocked"`) {
		t.Fatalf("blocked task without seq = %v", err)
	}

	blocked.Seq = 7
	err = guard(blocked)
	if err == nil || !strings.Contains(err.Error(), `#7 "Blocked"`) {
		t.Fatalf("blocked task with seq = %v", err)
	}
}

func TestStartCardDropIgnoresMissingLiftAndInFlightSave(t *testing.T) {
	s := &moveTestStore{board: moveFixture()}
	m := loadedMoveModel(s)
	if cmd := m.startCardDrop(); cmd != nil {
		t.Fatalf("drop without lift produced a command")
	}

	updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeySpace})
	m.move.saving = true
	if cmd := m.startCardDrop(); cmd != nil {
		t.Fatalf("drop during in-flight save produced a command")
	}
	if s.writeCount() != 0 {
		t.Fatalf("guarded drops wrote to the store %d times", s.writeCount())
	}
}

func TestFinishCardDropIntoDoneRecordsShipped(t *testing.T) {
	s := &moveTestStore{board: moveFixture()}
	m := loadedMoveModel(s)
	m.savePreferences = func(tuiPreferences) error { return nil }
	canonical := moveFixture()
	for i := range canonical.Tasks {
		if canonical.Tasks[i].ID == "a" {
			canonical.Tasks[i].Status = board.StatusDone
			canonical.Tasks[i].Position = 0
		}
	}

	preference := updateTestModel(t, &m, cardMoveStoredMsg{
		taskID: "a", title: "A",
		from: board.StatusTodo, to: board.StatusDone,
		board: canonical,
	})
	if m.move.status != "Shipped A" || m.move.statusError {
		t.Fatalf("ship status = error %v %q", m.move.statusError, m.move.status)
	}
	shipped := false
	for _, id := range m.shipped.IDs {
		if id == "a" {
			shipped = true
		}
	}
	if !shipped {
		t.Fatalf("shipped record missing task: %#v", m.shipped)
	}
	if preference == nil {
		t.Fatalf("ship did not queue a preference save")
	}
}
