package cardeditor

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// TestFocusGutterIsUnbrokenAcrossWrappedLines is spec section 10.4.3: the bar is
// emitted per rendered line, not per logical row, so a textarea focused at its
// label carries the same cell down every line it wraps to.
func TestFocusGutterIsUnbrokenAcrossWrappedLines(t *testing.T) {
	model := newTestEditor(newTestStore(t), "u")
	model.OpenEdit(fullEditorTask())
	model.desc.SetValue("one\ntwo\nthree")
	model.focus = "desc"

	bar := strings.Repeat(theme.New(true).Glyph.Rail, 1)
	rows := model.bodyRows(60)
	barred := 0
	for _, row := range rows {
		if row.target != "desc" {
			continue
		}
		if !strings.HasPrefix(row.mark, bar) {
			t.Fatalf("a focused desc line carries no bar: %q", row.plain())
		}
		barred++
	}
	if barred < 2 {
		t.Fatalf("the focused block spans %d lines; the bar is not being tested", barred)
	}

	// A blank spacer between rows carries no gutter: the bar spans exactly the
	// lines its own row occupies.
	for _, row := range rows {
		if row.target == "" && row.mark != "" {
			t.Fatalf("a static row reserved a gutter: %q", row.plain())
		}
	}
}

// TestFocusNeverReflowsTheEditor is spec section 10.4.4: the gutter is reserved
// in every state, so moving the keyboard across the pane changes colors and
// attributes and never a cell.
func TestFocusNeverReflowsTheEditor(t *testing.T) {
	model := newTestEditor(newTestStore(t), "u")
	model.OpenEdit(fullEditorTask())
	targets := []string{"title", "emoji", "desc", "prio", "due", "project", "labels", "cancel", "save"}

	model.focus = targets[0]
	want := paneWidths(&model)
	for _, target := range targets[1:] {
		model.focus = target
		if got := paneWidths(&model); !equalWidths(got, want) {
			t.Fatalf("focusing %q reflowed the pane: %v vs %v", target, got, want)
		}
	}
}

func paneWidths(model *Model) []int {
	lines := strings.Split(ansi.Strip(model.View(80, 30)), "\n")
	widths := make([]int, len(lines))
	for index, line := range lines {
		widths[index] = ansi.StringWidth(line)
	}
	return widths
}

func equalWidths(got, want []int) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

// TestEditorErrorRowsCarryTheirTailAndTier is ratified call 12 and spec section
// 10.8.5: a panel error is TintDanger above the action row, and the row names
// the control that will run the failed operation again.
func TestEditorErrorRowsCarryTheirTailAndTier(t *testing.T) {
	model := newTestEditor(newTestStore(t), "u")
	model.OpenEdit(fullEditorTask())
	model.statusMessage, model.statusIsError, model.statusTail = "save refused: store locked", true, "Save card"

	view := model.View(90, 30)
	plain := ansi.Strip(view)
	if !strings.Contains(plain, "▲ save refused: store locked") {
		t.Fatalf("error row missing its alert glyph:\n%s", plain)
	}
	if !strings.Contains(plain, "Save card") {
		t.Fatalf("error row named no control:\n%s", plain)
	}
	styles := model.themeStyles()
	opening, _, _ := strings.Cut(styles.On(theme.TintDanger, theme.OverlaySurf).Render("\x00"), "\x00")
	if !strings.Contains(view, opening) {
		t.Fatal("the error row is not TintDanger on the panel tier")
	}
	// The band never carries an error: it goes back to its hint ladder.
	lines := strings.Split(plain, "\n")
	if band := lines[len(lines)-2]; strings.Contains(band, "save refused") {
		t.Fatalf("the footer band carried the error: %q", band)
	}
}

// TestEditorSectionEmptyAndBusyRowsUseTheWidgets is spec sections 10.8.3 and
// 10.8.4: the label block names its next action when it has no suggestions, and
// the similar-items lookup is a plain-tier busy row with no ellipsis of its own.
func TestEditorSectionEmptyAndBusyRowsUseTheWidgets(t *testing.T) {
	model := newTestEditor(newTestStore(t), "u")
	model.OpenEdit(fullEditorTask())
	model.focus, model.labelsOpen = "labels", true
	model.labels, model.tags = []string{"alpha"}, []string{"alpha"}
	if got := rowText(model.labelSuggestionRows(72)); !strings.Contains(got, "○ no label suggestions  enter add typed labels") {
		t.Fatalf("empty suggestions row = %q", got)
	}

	model.similarLoading = true
	got := rowText(model.similarRows(72))
	if !strings.Contains(got, similarLabel) || strings.Contains(got, similarLabel+"...") {
		t.Fatalf("similar busy row = %q", got)
	}
	// Rule 4: one motion per surface. The footer band wins, so a body busy row
	// under a busy footer renders its label with no frame.
	model.saving = true
	if quiet := rowText(model.similarRows(72)); strings.Contains(quiet, ansi.Strip(model.plainFrame())+" "+similarLabel) {
		t.Fatalf("a second motion ran under the busy band: %q", quiet)
	}
	model.saving = false

	model.similarLoading, model.similarErr = false, errors.New("lookup refused")
	if failed := rowText(model.similarRows(72)); !strings.Contains(failed, "▲ lookup refused") {
		t.Fatalf("similar error row = %q", failed)
	}
}

// TestNarrowPanelFallsBackToThePanelSurface covers the rung below a button's own
// padding, where the label no longer fits and the row renders as plain surface.
func TestNarrowPanelFallsBackToThePanelSurface(t *testing.T) {
	model := newTestEditor(newTestStore(t), "u")
	model.OpenAdd(board.StatusTodo)
	row := model.actionRow("save", "Save card", theme.ButtonPrimary)
	if got := ansi.Strip(model.renderRow(row, 3)); ansi.StringWidth(got) > 3 {
		t.Fatalf("clipped button row is %d cells: %q", ansi.StringWidth(got), got)
	}
	if got := model.renderRow(row, 2); ansi.Strip(got) != "  " {
		t.Fatalf("a row with room for the gutter alone rendered %q", ansi.Strip(got))
	}
}

// TestSimilarChoiceRowsRaiseOnHover is spec section 10.5.1: a hovered
// activatable row raises one tier, and hover is never a gutter state.
func TestSimilarChoiceRowsRaiseOnHover(t *testing.T) {
	model := newTestEditor(newTestStore(t), "u")
	model.OpenEdit(fullEditorTask())
	model.similar = []store.SimilarHit{{Title: "close enough"}}
	rows := model.similarRows(72)
	var choice editorRow
	for _, row := range rows {
		if row.kind == rowChoice {
			choice = row
			break
		}
	}
	if choice.target == "" {
		t.Fatal("similar items rendered no choice row")
	}
	resting := model.renderRow(choice, 60)
	styles := model.themeStyles()
	raised, _, _ := strings.Cut(styles.On(theme.FgBase, styles.RowSurface(true)).Render("\x00"), "\x00")
	if strings.Contains(resting, raised) {
		t.Fatal("a resting row already carries the hovered surface")
	}
	if ansi.StringWidth(resting) == 0 {
		t.Fatal("the choice row rendered nothing")
	}
}
