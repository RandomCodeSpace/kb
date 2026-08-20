package carddetail

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/board"
)

func typeRunes(model *Model, text string) {
	for _, r := range text {
		model.updateActionKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

// assertLineEditing drives the readline motions every bubbles text field binds
// by default: Home parks the cursor at the line start, End at the line end.
func assertLineEditing(t *testing.T, model *Model, value func() string, typed string) {
	t.Helper()
	typeRunes(model, typed)
	if got := value(); got != typed {
		t.Fatalf("typed value = %q, want %q", got, typed)
	}
	model.updateActionKey(tea.KeyPressMsg{Code: tea.KeyHome})
	typeRunes(model, "<")
	if got := value(); got != "<"+typed {
		t.Fatalf("after home = %q, want %q", got, "<"+typed)
	}
	model.updateActionKey(tea.KeyPressMsg{Code: tea.KeyEnd})
	typeRunes(model, ">")
	if got := value(); got != "<"+typed+">" {
		t.Fatalf("after end = %q, want %q", got, "<"+typed+">")
	}
}

func detailActionModel(t *testing.T, action actionMode) *Model {
	t.Helper()
	model := New(&actionStore{}, "u", testStyles())
	load := model.Open(board.Task{ID: "task", Seq: 1, Status: board.StatusTodo})
	model.Update(load())
	model.beginAction(action)
	if model.action != action {
		t.Fatalf("action = %v, want %v", model.action, action)
	}
	return &model
}

func TestCommentInputReceivesLineEditingKeys(t *testing.T) {
	model := detailActionModel(t, actionAddComment)
	assertLineEditing(t, model, model.commentInput.Value, "note")
}

func TestLinkInputReceivesLineEditingKeys(t *testing.T) {
	model := detailActionModel(t, actionAddLink)
	assertLineEditing(t, model, func() string { return model.linkInput.Value() }, "task")
}
