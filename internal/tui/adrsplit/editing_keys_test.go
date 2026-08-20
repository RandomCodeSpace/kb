package adrsplit

import (
	"testing"

	tea "charm.land/bubbletea/v2"
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

func TestADRPasteFieldReceivesLineEditingKeys(t *testing.T) {
	model, _, _ := newTestModel()
	model.focus = "adr"
	model.applyFocus()
	assertLineEditing(t, model, model.adr.Value, "abc")
}

func TestADRFilePathFieldReceivesLineEditingKeys(t *testing.T) {
	model, _, _ := newTestModel()
	model.source = sourceFile
	model.focus = "file"
	model.applyFocus()
	assertLineEditing(t, model, func() string { return model.filePath.Value() }, "abc")
}

func TestADRRowTitleFieldReceivesLineEditingKeys(t *testing.T) {
	model, _, _ := newTestModel()
	model.adr.SetValue("# ADR\nUse SQLite.")
	model.Update(commandMsg(t, model.startSplit()))
	if model.stage != stageReview || len(model.rows) == 0 {
		t.Fatalf("review stage=%d rows=%d", model.stage, len(model.rows))
	}
	model.focus = "title:0"
	model.applyFocus()
	model.rows[0].title.SetValue("")
	assertLineEditing(t, model, func() string { return model.rows[0].title.Value() }, "abc")
}
