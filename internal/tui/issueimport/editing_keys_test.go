package issueimport

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/store"
)

func typeRunes(model *Model, text string) {
	for _, r := range text {
		model.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func refModel(t *testing.T) *Model {
	t.Helper()
	backend := &fakeBackend{sources: []store.ForgeSource{{Name: "primary", Kind: "github"}}}
	model := openModel(t, backend, &fakeStore{})
	model.focus = 1
	model.applyFocus()
	return &model
}

// The reference field is a text input, so letters and cursor motions belong to
// it - the source and count steppers only own left/right while they have focus.
func TestImportReferenceFieldReceivesLineEditingKeys(t *testing.T) {
	model := refModel(t)
	typeRunes(model, "owner/repo")
	if got := model.ref.Value(); got != "owner/repo" {
		t.Fatalf("typed value = %q, want %q", got, "owner/repo")
	}
	model.Update(tea.KeyPressMsg{Code: tea.KeyHome})
	typeRunes(model, "<")
	if got := model.ref.Value(); got != "<owner/repo" {
		t.Fatalf("after home = %q, want %q", got, "<owner/repo")
	}
	model.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	typeRunes(model, ">")
	if got := model.ref.Value(); got != "<owner/repo>" {
		t.Fatalf("after end = %q, want %q", got, "<owner/repo>")
	}
}

func TestImportReferenceFieldReceivesArrowsAndLetters(t *testing.T) {
	model := refModel(t)
	typeRunes(model, "hl")
	if got := model.ref.Value(); got != "hl" {
		t.Fatalf("typed value = %q, want %q", got, "hl")
	}
	model.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	typeRunes(model, "#")
	if got := model.ref.Value(); got != "h#l" {
		t.Fatalf("after left = %q, want %q", got, "h#l")
	}
	model.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	typeRunes(model, "!")
	if got := model.ref.Value(); got != "h#l!" {
		t.Fatalf("after right = %q, want %q", got, "h#l!")
	}
}

// A digit typed into the reference field is text, not a count change.
func TestImportReferenceFieldReceivesDigits(t *testing.T) {
	model := refModel(t)
	before := model.max
	typeRunes(model, "9")
	if got := model.ref.Value(); got != "9" {
		t.Fatalf("typed value = %q, want %q", got, "9")
	}
	if model.max != before {
		t.Fatalf("count = %d, want %d", model.max, before)
	}
}
