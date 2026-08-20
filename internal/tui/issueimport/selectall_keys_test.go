package issueimport

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/RandomCodeSpace/kb/internal/tui/formview"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// ctrlAKey is the select-all binding of issue #159.
func ctrlAKey() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl} }

// TestImportReferenceFieldSelectAll drives the whole behaviour contract of
// issue #159 against the reference input: ctrl+a marks the content, the next
// typed rune replaces it, Backspace clears it, and a navigation key drops the
// mark without changing the content while still moving the cursor.
func TestImportReferenceFieldSelectAll(t *testing.T) {
	model := refModel(t)
	value := func() string { return model.ref.Value() }
	marked := func() bool { return model.mark.Active(refMarkField) }

	typeRunes(model, "owner/repo")
	model.Update(ctrlAKey())
	if !marked() {
		t.Fatal("ctrl+a did not mark the field")
	}
	if got := value(); got != "owner/repo" {
		t.Fatalf("marking changed the value to %q", got)
	}

	typeRunes(model, "x")
	if got, mark := value(), marked(); got != "x" || mark {
		t.Fatalf("replace on type = %q marked:%v, want \"x\" marked:false", got, mark)
	}

	typeRunes(model, "yz")
	model.Update(ctrlAKey())
	model.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if got, mark := value(), marked(); got != "" || mark {
		t.Fatalf("clear on backspace = %q marked:%v, want empty marked:false", got, mark)
	}

	typeRunes(model, "owner/repo")
	model.Update(ctrlAKey())
	model.Update(tea.KeyPressMsg{Code: tea.KeyHome})
	if got, mark := value(), marked(); got != "owner/repo" || mark {
		t.Fatalf("navigation = %q marked:%v, want the value marked:false", got, mark)
	}
	typeRunes(model, "<")
	if got := value(); got != "<owner/repo" {
		t.Fatalf("the navigation key was swallowed: %q", got)
	}
}

// Escape drops the mark and is consumed: the overlay stays open, and only the
// second Escape closes it.
func TestImportSelectAllEscapeConsumesThenCloses(t *testing.T) {
	model := refModel(t)
	typeRunes(model, "owner/repo")

	model.Update(ctrlAKey())
	model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !model.IsOpen() || model.mark.Active(refMarkField) {
		t.Fatalf("first escape = open:%v marked:%v", model.IsOpen(), model.mark.Active(refMarkField))
	}
	if got := model.ref.Value(); got != "owner/repo" {
		t.Fatalf("escape changed the value to %q", got)
	}
	model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if model.IsOpen() {
		t.Fatal("the second escape did not close the overlay")
	}
}

// The marked field is visually obvious: its row carries the theme's pressed
// treatment, which the unmarked render does not.
func TestImportMarkedFieldRendersTheSelectionTreatment(t *testing.T) {
	model := refModel(t)
	typeRunes(model, "owner/repo")

	before := model.View(80, 24)
	model.Update(ctrlAKey())
	after := model.View(80, 24)
	if before == after {
		t.Fatal("the marked field renders exactly like the unmarked one")
	}
	styles := model.themeStyles()
	sample := formview.Selection(styles.OnBold(theme.FgBase, theme.OverlaySurf), true).Render("kb")
	opening, _, _ := strings.Cut(sample, "kb")
	if !strings.Contains(after, opening) {
		t.Fatalf("the marked field carries no selection treatment: %q", opening)
	}
	if strings.Contains(before, opening) {
		t.Fatal("the unmarked field already carries the selection treatment")
	}
}

// TestIssueImportMarkedRefColorGolden pins the marked state's cell grid: the
// mark is an attribute, so only a truecolor golden records it.
func TestIssueImportMarkedRefColorGolden(t *testing.T) {
	model := refModel(t)
	model.ref.SetValue("owner/repo")
	model.Update(ctrlAKey())
	background := strings.TrimSuffix(strings.Repeat(strings.Repeat("b", 56)+"\n", 18), "\n")
	golden.RequireEqual(t, []byte(theme.Downsample(model.Overlay(background, 56, 18), theme.ColorProfile)))
}
