package carddetail

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
// against one action field: ctrl+a marks the content, the next typed rune
// replaces it, Backspace clears it, and a navigation key drops the mark without
// changing the content while still moving the cursor.
func assertSelectAllContract(t *testing.T, model *Model, value func() string, marked func() bool) {
	t.Helper()
	send := func(msg tea.KeyPressMsg) { model.updateActionKey(msg) }

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

func TestCommentInputSelectAll(t *testing.T) {
	model := detailActionModel(t, actionAddComment)
	assertSelectAllContract(t, model,
		func() string { return model.commentInput.Value() },
		func() bool { return model.mark.Active(commentMarkField) })
}

func TestLinkInputSelectAll(t *testing.T) {
	model := detailActionModel(t, actionAddLink)
	assertSelectAllContract(t, model,
		func() string { return model.linkInput.Value() },
		func() bool { return model.mark.Active(linkMarkField) })
}

// Escape drops the mark and is consumed: the action pane stays open, and only
// the second Escape cancels it.
func TestDetailSelectAllEscapeConsumesThenCancels(t *testing.T) {
	model := detailActionModel(t, actionAddComment)
	typeRunes(model, "login")

	model.updateActionKey(ctrlAKey())
	model.updateActionKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if model.action != actionAddComment || model.mark.Active(commentMarkField) {
		t.Fatalf("first escape = action:%v marked:%v", model.action, model.mark.Active(commentMarkField))
	}
	if got := model.commentInput.Value(); got != "login" {
		t.Fatalf("escape changed the value to %q", got)
	}
	model.updateActionKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if model.action != actionNone {
		t.Fatalf("second escape = action:%v, want none", model.action)
	}
}

// The marked field is visually obvious: its value carries the theme's pressed
// run, which the unmarked render does not.
func TestDetailMarkedFieldRendersTheSelectionTreatment(t *testing.T) {
	model := detailActionModel(t, actionAddLink)
	model.width, model.height = 80, 24
	typeRunes(model, "task")

	model.rebuildBody()
	before := strings.Join(model.bodyLines, "\n")
	model.updateActionKey(ctrlAKey())
	after := strings.Join(model.bodyLines, "\n")
	if before == after {
		t.Fatal("the marked field renders exactly like the unmarked one")
	}
	opening, _, _ := strings.Cut(model.styles.PressedRun("kb"), "kb")
	if !strings.Contains(after, opening) {
		t.Fatalf("the marked field carries no selection treatment: %q", opening)
	}
	if strings.Contains(before, opening) {
		t.Fatal("the unmarked field already carries the selection treatment")
	}
}

// TestCardDetailMarkedFieldColorGolden pins the marked state's cell grid: the
// mark is an attribute, so only a truecolor golden records it.
func TestCardDetailMarkedFieldColorGolden(t *testing.T) {
	model := detailActionModel(t, actionAddLink)
	model.linkInput.SetValue("task-42")
	model.updateActionKey(ctrlAKey())
	background := strings.TrimRight(strings.Repeat(strings.Repeat("-", 60)+"\n", 20), "\n")
	surface := model.PointerSurface(background, 60, 20)
	golden.RequireEqual(t, theme.Downsample(surface.Content, theme.ColorProfile)+"\n")
}
