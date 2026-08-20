package adrsplit

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// ctrlAKey is the select-all binding of issue #159.
func ctrlAKey() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl} }

// assertSelectAllContract drives the whole behaviour contract of issue #159
// against one focused field: ctrl+a marks the content, the next typed rune
// replaces it, Backspace clears it, and a navigation key drops the mark without
// changing the content while still moving the cursor.
func assertSelectAllContract(t *testing.T, model *Model, value func() string, marked func() bool) {
	t.Helper()
	send := func(msg tea.KeyPressMsg) { model.Update(msg) }

	typeRunes(model, "login")
	send(ctrlAKey())
	if !marked() {
		t.Fatal("ctrl+a did not mark the field")
	}
	if got := value(); got != "login" {
		t.Fatalf("marking changed the value to %q", got)
	}

	typeRunes(model, "x")
	if got, mark := value(), marked(); got != "x" || mark {
		t.Fatalf("replace on type = %q marked:%v, want \"x\" marked:false", got, mark)
	}

	typeRunes(model, "yz")
	send(ctrlAKey())
	send(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if got, mark := value(), marked(); got != "" || mark {
		t.Fatalf("clear on backspace = %q marked:%v, want empty marked:false", got, mark)
	}

	typeRunes(model, "login")
	send(ctrlAKey())
	send(tea.KeyPressMsg{Code: tea.KeyHome})
	if got, mark := value(), marked(); got != "login" || mark {
		t.Fatalf("navigation = %q marked:%v, want \"login\" marked:false", got, mark)
	}
	typeRunes(model, "<")
	if got := value(); got != "<login" {
		t.Fatalf("the navigation key was swallowed: %q", got)
	}
}

func TestADRPasteFieldSelectAll(t *testing.T) {
	model, _, _ := newTestModel()
	model.focus = "adr"
	model.applyFocus()
	assertSelectAllContract(t, model,
		func() string { return model.adr.Value() },
		func() bool { return model.mark.Active("adr") })
}

func TestADRFilePathFieldSelectAll(t *testing.T) {
	model, _, _ := newTestModel()
	model.source = sourceFile
	model.focus = "file"
	model.applyFocus()
	assertSelectAllContract(t, model,
		func() string { return model.filePath.Value() },
		func() bool { return model.mark.Active("file") })
}

func TestADRRowTitleFieldSelectAll(t *testing.T) {
	model, _, _ := newTestModel()
	model.adr.SetValue("# ADR\nUse SQLite.")
	model.Update(commandMsg(t, model.startSplit()))
	if model.stage != stageReview || len(model.rows) == 0 {
		t.Fatalf("review stage=%d rows=%d", model.stage, len(model.rows))
	}
	model.focus = "title:0"
	model.applyFocus()
	model.rows[0].title.SetValue("")
	assertSelectAllContract(t, model,
		func() string { return model.rows[0].title.Value() },
		func() bool { return model.mark.Active("title:0") })
}

// Escape drops the mark and is consumed: the overlay stays open, and only the
// second Escape reaches the close path.
func TestADRSelectAllEscapeConsumesThenCloses(t *testing.T) {
	model, _, _ := newTestModel()
	model.focus = "adr"
	model.applyFocus()
	typeRunes(model, "login")

	model.Update(ctrlAKey())
	model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !model.IsOpen() || model.mark.Active("adr") {
		t.Fatalf("first escape = open:%v marked:%v", model.IsOpen(), model.mark.Active("adr"))
	}
	if got := model.adr.Value(); got != "login" {
		t.Fatalf("escape changed the value to %q", got)
	}
	model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if model.IsOpen() && !model.guardClose {
		t.Fatal("the second escape did not reach the overlay's close path")
	}
}

// TestADRSplitMarkedFieldColorGolden pins the marked state's cell grid: the
// mark is an attribute, so only a truecolor golden records it.
func TestADRSplitMarkedFieldColorGolden(t *testing.T) {
	model, _, _ := newTestModel()
	model.focus = "adr"
	model.applyFocus()
	model.adr.SetValue("Use SQLite for the ledger")
	model.Update(ctrlAKey())
	background := strings.TrimSuffix(strings.Repeat(strings.Repeat("b", 56)+"\n", 16), "\n")
	golden.RequireEqual(t, []byte(theme.Downsample(model.Overlay(background, 56, 16), theme.ColorProfile)))
}
