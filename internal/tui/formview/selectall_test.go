package formview

import (
	"testing"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

func ctrlA() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl} }

func rune_(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Text: string(r)} }

func TestMarkInputContract(t *testing.T) {
	input := textinput.New()
	input.SetValue("hello")
	var mark Mark

	if !mark.Input("field", &input, ctrlA()) {
		t.Fatal("ctrl+a was not consumed")
	}
	if !mark.Active("field") || mark.Active("other") {
		t.Fatalf("mark = %q, want only field marked", mark.field)
	}
	if input.Value() != "hello" {
		t.Fatalf("marking changed the value to %q", input.Value())
	}

	// A printable key is not consumed - the field itself inserts it - but the
	// marked content is gone before it lands.
	if mark.Input("field", &input, rune_('x')) {
		t.Fatal("a printable key was consumed")
	}
	if input.Value() != "" || mark.Active("field") {
		t.Fatalf("after typing = %q marked:%v", input.Value(), mark.Active("field"))
	}

	input.SetValue("hello")
	mark.Input("field", &input, ctrlA())
	if !mark.Input("field", &input, tea.KeyPressMsg{Code: tea.KeyBackspace}) {
		t.Fatal("backspace was not consumed")
	}
	if input.Value() != "" || mark.Active("field") {
		t.Fatalf("after backspace = %q marked:%v", input.Value(), mark.Active("field"))
	}

	input.SetValue("hello")
	mark.Input("field", &input, ctrlA())
	if !mark.Input("field", &input, tea.KeyPressMsg{Code: tea.KeyDelete}) {
		t.Fatal("delete was not consumed")
	}
	if input.Value() != "" {
		t.Fatalf("after delete = %q", input.Value())
	}

	input.SetValue("hello")
	mark.Input("field", &input, ctrlA())
	if mark.Input("field", &input, tea.KeyPressMsg{Code: tea.KeyHome}) {
		t.Fatal("a navigation key was consumed")
	}
	if input.Value() != "hello" || mark.Active("field") {
		t.Fatalf("after home = %q marked:%v", input.Value(), mark.Active("field"))
	}

	input.SetValue("hello")
	mark.Input("field", &input, ctrlA())
	if !mark.Input("field", &input, tea.KeyPressMsg{Code: tea.KeyEscape}) {
		t.Fatal("escape was not consumed by the mark")
	}
	if input.Value() != "hello" || mark.Active("field") {
		t.Fatalf("after escape = %q marked:%v", input.Value(), mark.Active("field"))
	}
	if mark.Input("field", &input, tea.KeyPressMsg{Code: tea.KeyEscape}) {
		t.Fatal("the second escape was consumed too")
	}
}

func TestMarkAreaAndEdges(t *testing.T) {
	area := textarea.New()
	area.SetValue("one\ntwo")
	var mark Mark

	if !mark.Area("desc", &area, ctrlA()) || !mark.Active("desc") {
		t.Fatal("ctrl+a did not mark the area")
	}
	if mark.Area("desc", &area, rune_('z')) || area.Value() != "" {
		t.Fatalf("typing over a marked area = %q", area.Value())
	}

	// An empty field marks nothing, but ctrl+a is still consumed: it is never
	// the line-start motion bubbles binds it to by default.
	if !mark.Area("desc", &area, ctrlA()) || mark.Active("desc") {
		t.Fatal("ctrl+a on an empty field marked it")
	}

	// A key routed for another field finds no mark of its own.
	area.SetValue("one")
	mark.Area("desc", &area, ctrlA())
	if mark.Area("checks", &area, tea.KeyPressMsg{Code: tea.KeyBackspace}) {
		t.Fatal("a sibling field consumed the mark's key")
	}
	if area.Value() != "one" {
		t.Fatalf("a sibling field cleared the marked field: %q", area.Value())
	}

	// Drop is the pointer-focus path: no key, no mark.
	mark.Drop()
	if mark.Active("desc") {
		t.Fatal("drop left the mark set")
	}

	// A field with no name has nothing to mark, and a nil model is inert.
	if mark.Area("", &area, ctrlA()) {
		t.Fatal("an unnamed field was marked")
	}
	if mark.apply("desc", nil, ctrlA()) {
		t.Fatal("a nil field model was marked")
	}
}

func TestSelectionAppliesThePressedAttribute(t *testing.T) {
	style := theme.New(true).Overlay.FieldValue
	if got := Selection(style, false).Render("kb"); got != style.Render("kb") {
		t.Fatalf("unmarked style changed: %q", got)
	}
	if got := Selection(style, true).Render("kb"); got == style.Render("kb") {
		t.Fatalf("marked style is unchanged: %q", got)
	}
	if !Selection(style, true).GetReverse() {
		t.Fatal("the marked style carries no reverse attribute")
	}
}
