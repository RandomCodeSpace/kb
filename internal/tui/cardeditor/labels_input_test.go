package cardeditor

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/RandomCodeSpace/kb/internal/board"
)

func TestTypingInLabelFieldOpensSuggestions(t *testing.T) {
	model := New(newTestStore(t), "u")
	model.OpenAdd(board.StatusTodo)
	model.focus = "labels"
	model.applyFocus()
	model.labelHighlight = 3

	model.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	if model.label.Value() != "a" {
		t.Fatalf("label value = %q", model.label.Value())
	}
	if !model.labelsOpen || model.labelHighlight != 0 {
		t.Fatalf("typing did not open suggestions: open %v highlight %d",
			model.labelsOpen, model.labelHighlight)
	}
}
