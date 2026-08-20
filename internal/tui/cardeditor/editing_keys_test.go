package cardeditor

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/board"
)

func typeRunes(model *Model, text string) {
	for _, r := range text {
		model.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
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
	model.Update(tea.KeyPressMsg{Code: tea.KeyHome})
	typeRunes(model, "<")
	if got := value(); got != "<"+typed {
		t.Fatalf("after home = %q, want %q", got, "<"+typed)
	}
	model.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	typeRunes(model, ">")
	if got := value(); got != "<"+typed+">" {
		t.Fatalf("after end = %q, want %q", got, "<"+typed+">")
	}
}

func TestEditorFieldsReceiveLineEditingKeys(t *testing.T) {
	for _, test := range []struct {
		focus string
		value func(*Model) string
	}{
		{"title", func(m *Model) string { return m.title.Value() }},
		{"emoji", func(m *Model) string { return m.emoji.Value() }},
		{"desc", func(m *Model) string { return m.desc.Value() }},
		{"due", func(m *Model) string { return m.due.Value() }},
		{"checks", func(m *Model) string { return m.checks.Value() }},
		{"labels", func(m *Model) string { return m.label.Value() }},
		{"ai-prompt", func(m *Model) string { return m.draftPrompt.Value() }},
	} {
		t.Run(test.focus, func(t *testing.T) {
			model := New(newTestStore(t), "u")
			model.OpenAdd(board.StatusTodo)
			model.focus = test.focus
			model.applyFocus()
			assertLineEditing(t, &model, func() string { return test.value(&model) }, "abc")
		})
	}
}

// The due field is a text input, so the word motions bubbles binds by default
// belong to it: alt+arrows move the cursor and leave the date alone. The date
// stepper keeps its own [ and ] bindings.
func TestDueFieldReceivesWordMotionKeys(t *testing.T) {
	model := New(newTestStore(t), "u")
	model.OpenAdd(board.StatusTodo)
	model.focus = "due"
	model.applyFocus()
	model.due.SetValue("2026-08-20")
	model.Update(tea.KeyPressMsg{Code: tea.KeyEnd})

	model.Update(tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModAlt})
	if got := model.due.Value(); got != "2026-08-20" {
		t.Fatalf("alt+left changed the date to %q", got)
	}
	typeRunes(&model, "#")
	if got := model.due.Value(); got != "#2026-08-20" {
		t.Fatalf("after alt+left = %q, want %q", got, "#2026-08-20")
	}
	model.Update(tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModAlt})
	if got := model.due.Value(); got != "#2026-08-20" {
		t.Fatalf("alt+right changed the date to %q", got)
	}
	typeRunes(&model, "!")
	if got := model.due.Value(); got != "#2026-08-20!" {
		t.Fatalf("after alt+right = %q, want %q", got, "#2026-08-20!")
	}
}

// Word motion on prose fields: alt+left parks the cursor at the start of the
// word under it, alt+right at the end of the next one.
func TestTitleFieldReceivesWordMotionKeys(t *testing.T) {
	model := New(newTestStore(t), "u")
	model.OpenAdd(board.StatusTodo)
	model.focus = "title"
	model.applyFocus()
	typeRunes(&model, "one two")

	model.Update(tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModAlt})
	typeRunes(&model, "#")
	if got := model.title.Value(); got != "one #two" {
		t.Fatalf("after alt+left = %q, want %q", got, "one #two")
	}
	model.Update(tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModAlt})
	typeRunes(&model, "!")
	if got := model.title.Value(); got != "one #two!" {
		t.Fatalf("after alt+right = %q, want %q", got, "one #two!")
	}
}

func TestDueDateStepperKeepsBracketKeys(t *testing.T) {
	model := New(newTestStore(t), "u")
	model.OpenAdd(board.StatusTodo)
	model.focus = "due"
	model.applyFocus()
	model.due.SetValue("2026-08-20")

	model.Update(tea.KeyPressMsg{Code: ']', Text: "]"})
	if got := model.due.Value(); got != "2026-08-21" {
		t.Fatalf("after ] = %q, want 2026-08-21", got)
	}
	model.Update(tea.KeyPressMsg{Code: '[', Text: "["})
	if got := model.due.Value(); got != "2026-08-20" {
		t.Fatalf("after [ = %q, want 2026-08-20", got)
	}
}
