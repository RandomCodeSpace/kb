package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/tui/pointer"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// clickModel is a loaded board whose one card can be lifted with the pointer.
func clickModel(t *testing.T) (Model, board.Task) {
	t.Helper()
	m, _, tasks := actionTestModel(t, board.Task{Title: "Clickable", Status: board.StatusTodo})
	m.width, m.height = 90, 24
	return m, tasks[0]
}

// TestBoardClickArmsTheDoubleClickWindow pins the binding of spec section
// 10.3.5: the classifier runs on the completed board gesture, keyed by the card
// the lift started on.
func TestBoardClickArmsTheDoubleClickWindow(t *testing.T) {
	m, task := clickModel(t)
	m = collapseInteractionTiming(m)
	// A real window so the arming command schedules instead of dispatching.
	timing := m.themeStyles().Timing
	timing.DoubleClickWindow = theme.DefaultTiming.DoubleClickWindow
	m.applyStyles(theme.NewWith(true, timing))

	updateTestModel(t, &m, boardPointerDownMsg{taskID: task.ID})
	updateTestModel(t, &m, boardPointerUpMsg{})
	if !m.detail.IsOpen() {
		t.Fatal("a completed click did not open the card detail overlay")
	}
	id, armed := m.clicks.Armed()
	if !armed || id != pointer.ControlID(task.ID) {
		t.Fatalf("armed region = %q %v, want the card id", id, armed)
	}
}

// TestBoardDragClosesTheDoubleClickWindow is the drag exclusion at the model
// level: a lift that moved is a drag, never half of a double-click.
func TestBoardDragClosesTheDoubleClickWindow(t *testing.T) {
	m, task := clickModel(t)
	updateTestModel(t, &m, boardPointerDownMsg{taskID: task.ID})
	updateTestModel(t, &m, boardPointerMoveMsg{status: board.StatusDoing})
	updateTestModel(t, &m, boardPointerUpMsg{})
	if _, armed := m.clicks.Armed(); armed {
		t.Fatal("a drag armed the double-click window")
	}
}

// TestBoardClickWindowExpiryIsConsumedByTheRoot keeps the window's own message
// off every surface below it.
func TestBoardClickWindowExpiryIsConsumedByTheRoot(t *testing.T) {
	m, task := clickModel(t)
	timing := m.themeStyles().Timing
	timing.DoubleClickWindow = theme.DefaultTiming.DoubleClickWindow
	m.applyStyles(theme.NewWith(true, timing))

	clicks, _, arm := pointer.Clicks{}.Click(pointer.ControlID(task.ID), false, 0)
	m.clicks = clicks
	if arm == nil {
		t.Fatal("the classifier produced no arming command")
	}
	rebuildTestView(&m)
	before := m.View().Content
	updated, command := m.Update(arm())
	m = updated.(Model)
	if command != nil {
		t.Fatal("the window expiry rescheduled itself")
	}
	if _, armed := m.clicks.Armed(); armed {
		t.Fatal("the window expiry did not close the window")
	}
	if got := m.View().Content; got != before {
		t.Fatal("the window expiry changed the settled frame")
	}
}

// TestBoardClickWindowIgnoresAnUnliftedRelease keeps the classifier off a
// gesture the board never started.
func TestBoardClickWindowIgnoresAnUnliftedRelease(t *testing.T) {
	m, _ := clickModel(t)
	updateTestModel(t, &m, boardPointerUpMsg{})
	if _, armed := m.clicks.Armed(); armed {
		t.Fatal("a release with no lift armed the window")
	}
	if command := updateTestModel(t, &m, tea.KeyPressMsg{Code: 'j', Text: "j"}); command != nil {
		t.Fatalf("an ordinary keystroke returned %v", command)
	}
}
