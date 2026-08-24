package cardeditor

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/tui/formview"
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

func TestEditorFieldsSelectAll(t *testing.T) {
	for _, test := range []struct {
		focus string
		value func(*Model) string
	}{
		{"title", func(m *Model) string { return m.title.Value() }},
		{"emoji", func(m *Model) string { return m.emoji.Value() }},
		{"desc", func(m *Model) string { return m.desc.Value() }},
		{"checks", func(m *Model) string { return m.checks.Value() }},
		{"labels", func(m *Model) string { return m.label.Value() }},
	} {
		t.Run(test.focus, func(t *testing.T) {
			model := newTestEditor(newTestStore(t), "u")
			model.OpenAdd(board.StatusTodo)
			model.focus = test.focus
			model.applyFocus()
			assertSelectAllContract(t, &model,
				func() string { return test.value(&model) },
				func() bool { return model.mark.Active(test.focus) })
		})
	}
}

// Escape drops the mark and is consumed: the editor stays open, and only the
// second Escape reaches the close path.
func TestEditorSelectAllEscapeConsumesThenCloses(t *testing.T) {
	model := newTestEditor(newTestStore(t), "u")
	model.OpenAdd(board.StatusTodo)
	model.focus = "title"
	model.applyFocus()
	typeRunes(&model, "login")

	model.Update(ctrlAKey())
	model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !model.IsOpen() || model.mark.Active("title") {
		t.Fatalf("first escape = open:%v marked:%v", model.IsOpen(), model.mark.Active("title"))
	}
	if got := model.title.Value(); got != "login" {
		t.Fatalf("escape changed the value to %q", got)
	}
	model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if model.IsOpen() && !model.guardClose {
		t.Fatal("the second escape did not reach the editor's close path")
	}
}

// Moving focus by any route drops the mark, so a field never comes back marked.
func TestEditorSelectAllDropsOnFocusMove(t *testing.T) {
	model := newTestEditor(newTestStore(t), "u")
	model.OpenAdd(board.StatusTodo)
	model.focus = "title"
	model.applyFocus()
	typeRunes(&model, "login")
	model.Update(ctrlAKey())

	model.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if model.mark.Active("title") || model.focus == "title" {
		t.Fatalf("tab left the mark = focus:%q marked:%v", model.focus, model.mark.Active("title"))
	}
	if got := model.title.Value(); got != "login" {
		t.Fatalf("tab changed the marked value to %q", got)
	}
}

// The marked field is visually obvious: its row carries the theme's pressed
// treatment, which the unmarked render does not.
func TestEditorMarkedFieldRendersTheSelectionTreatment(t *testing.T) {
	model := newTestEditor(newTestStore(t), "u")
	model.OpenAdd(board.StatusTodo)
	model.focus = "title"
	model.applyFocus()
	typeRunes(&model, "login")

	before := model.View(80, 30)
	model.Update(ctrlAKey())
	after := model.View(80, 30)
	if before == after {
		t.Fatal("the marked field renders exactly like the unmarked one")
	}
	// The expected run is derived from the theme rather than spelled out, so
	// this asserts the treatment and not one palette's escape codes.
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

// TestEditorMarkedFieldColorGolden pins the marked state's cell grid: the mark
// is an attribute, so only a truecolor golden records it.
func TestEditorMarkedFieldColorGolden(t *testing.T) {
	model := newTestEditor(newTestStore(t), "default")
	model.OpenEdit(fullEditorTask())
	model.focus = "title"
	model.applyFocus()
	model.Update(ctrlAKey())
	golden.RequireEqual(t, []byte(theme.Downsample(model.View(48, 14), theme.ColorProfile)))
}
