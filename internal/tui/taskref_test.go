package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/board"
)

// referenceModel is a loaded board whose open detail pane shows a card whose
// description carries reference.
func referenceModel(t *testing.T, reference string) (Model, []board.Task) {
	t.Helper()
	m, _, tasks := actionTestModel(t,
		board.Task{Title: "Target", Status: board.StatusTodo},
		board.Task{Title: "Source", Status: board.StatusTodo})
	m.width, m.height = 110, 30
	source := tasks[1]
	source.Desc = "see " + reference + " for the rest"
	for index := range m.board.Tasks {
		if m.board.Tasks[index].ID == source.ID {
			m.board.Tasks[index] = source
		}
	}
	m.detail.Resize(m.width, m.height)
	if command := m.detail.Open(source); command != nil {
		updateTestModel(t, &m, singleCommandMessage(t, command))
	}
	return m, tasks
}

// clickReference drives the whole pointer round trip the root wires: the frame
// the reference was drawn in produces the press, the frame after it produces
// the release, and every message travels the root's own routing.
func clickReference(t *testing.T, m *Model, reference string) {
	t.Helper()
	view := m.View()
	x, y := -1, -1
	for row, line := range strings.Split(ansi.Strip(view.Content), "\n") {
		if column := strings.Index(line, reference); column >= 0 {
			x, y = ansi.StringWidth(line[:column]), row
			break
		}
	}
	if x < 0 {
		t.Fatalf("reference %q is not on the frame:\n%s", reference, ansi.Strip(view.Content))
	}
	if view.OnMouse == nil {
		t.Fatal("the detail frame routed no mouse handler")
	}
	updateRootPointerTest(t, m, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	if followup := updateRootPointerTest(t, m,
		tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft}); followup != nil {
		updateTestModel(t, m, singleCommandMessage(t, followup))
	}
}

// TestTaskReferenceOpensTheCardItNames is issue #212 end to end: a rendered
// kb://task reference is clickable, and clicking it opens that card the way a
// click on the board card would, board cursor included.
func TestTaskReferenceOpensTheCardItNames(t *testing.T) {
	m, tasks := referenceModel(t, "kb://task/1")
	target, source := tasks[0], tasks[1]
	if target.Seq != 1 {
		t.Fatalf("target card is #%d, want #1", target.Seq)
	}
	if m.detail.TaskID() != source.ID {
		t.Fatalf("detail opened %q, want the source card", m.detail.TaskID())
	}

	clickReference(t, &m, "kb://task/1")
	if m.detail.TaskID() != target.ID {
		t.Fatalf("reference opened %q, want %q", m.detail.TaskID(), target.ID)
	}
	if selected, ok := m.selectedTask(); !ok || selected.ID != target.ID {
		t.Fatalf("board cursor = %q %v, want the referenced card", selected.ID, ok)
	}

	// The reference to the card already open is not a reopen: the pane would
	// throw away the scroll position and reload for no change of subject.
	if command := m.openTaskRef(target.Seq); command != nil {
		t.Fatal("a reference to the open card reopened it")
	}
	if command := m.openTaskRef(0); command != nil || m.detail.TaskID() != target.ID {
		t.Fatalf("a zero reference produced %v on card %q", command, m.detail.TaskID())
	}
}

// TestUnknownTaskReferenceNoticesInsteadOfCrashing is the failure mode of issue
// #212: card text names a card no board holds, and the pane says so.
func TestUnknownTaskReferenceNoticesInsteadOfCrashing(t *testing.T) {
	m, tasks := referenceModel(t, "kb://task/4242")
	source := tasks[1]

	clickReference(t, &m, "kb://task/4242")
	if m.detail.TaskID() != source.ID {
		t.Fatalf("an unresolvable reference navigated to %q", m.detail.TaskID())
	}
	notice := fmt.Sprintf("no card #%d on this board", 4242)
	if got := ansi.Strip(m.View().Content); !strings.Contains(got, notice) {
		t.Fatalf("unresolvable reference notice missing:\n%s", got)
	}
}
