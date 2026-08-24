package tui

import (
	"regexp"
	"strings"
	"testing"

	"charm.land/bubbles/v2/spinner"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
	"github.com/RandomCodeSpace/kb/internal/tui/widget"
)

// TestPurgeArmRecolorsTheDialogHeaderBand is ratified call 6 of spec section
// 10.1.4 on the surface that owns the two-step: the band re-fills to StatusAlarm
// with FgBase bold once the purge is armed, and the pending prompt before it
// leaves the frame alone.
func TestPurgeArmRecolorsTheDialogHeaderBand(t *testing.T) {
	m, _, tasks := actionTestModel(t, board.Task{Title: "Purge me", Status: board.StatusCancelled})
	m.width, m.height = 70, 16
	m.openPurgePrompt(tasks[0])
	styles := m.themeStyles()
	alarm := bandBackground(t, styles.Overlay.HeaderBandArmed)
	brand := bandBackground(t, styles.Overlay.HeaderBand)

	pending := headerRow(m.taskActionSurface(actionBackground(m.width, m.height)).Content)
	if strings.Contains(pending, alarm) {
		t.Fatal("a pending purge prompt recolored the header band")
	}
	if !strings.Contains(pending, brand) {
		t.Fatal("the pending prompt lost the brand header band")
	}

	m.action.armed = true
	armed := headerRow(m.taskActionSurface(actionBackground(m.width, m.height)).Content)
	if !strings.Contains(armed, alarm) {
		t.Fatalf("an armed purge prompt did not re-fill the header band:\n%q", armed)
	}
	if strings.Contains(armed, brand) {
		t.Error("the armed band kept the brand fill beside the alarm fill")
	}
	if ansi.StringWidth(pending) != ansi.StringWidth(armed) {
		t.Error("arming changed the header band width")
	}
	if ansi.Strip(pending) != ansi.Strip(armed) {
		t.Error("arming moved a cell in the header band")
	}
}

// TestDialogBusyMovesOffTheHeadlineAndIntoTheBand is spec section 10.8.4 rule 5
// and rule 1: the " (saving...)" headline suffix is gone, the plain tier owns
// the footer band, and the dismissal rung survives as the ladder's tail.
func TestDialogBusyMovesOffTheHeadlineAndIntoTheBand(t *testing.T) {
	m, _, tasks := actionTestModel(t, board.Task{Title: "Ship me", Status: board.StatusTodo})
	m.width, m.height = 70, 16
	m.openShipPrompt(tasks[0], 0)
	m.action.busy = true
	frame := ansi.Strip(m.taskActionSurface(actionBackground(m.width, m.height)).Content)
	if strings.Contains(frame, "(saving...)") {
		t.Fatal("the busy state still rides on the dialog headline")
	}
	footer := ansi.Strip(m.taskActionFooter(60))
	if !strings.Contains(footer, taskActionBusyLabel) || !strings.Contains(footer, "Esc cancel") {
		t.Fatalf("busy dialog footer = %q", footer)
	}
	if command := m.updateTaskAction(spinnerTick()); command == nil {
		t.Error("a busy dialog dropped its plain tick chain")
	}
	m.action.busy = false
	if command := m.updateTaskAction(spinnerTick()); command != nil {
		t.Error("an idle dialog kept ticking")
	}
}

// TestDialogErrorIsTintDangerAndWrapped is ratified call 12 and spec section
// 10.8.5: a panel error is TintDanger on OverlaySurf with the Alert glyph, it
// wraps to at most ErrorMaxLines instead of being cut bare, and its tail names
// the dialog's own affirmative.
func TestDialogErrorIsTintDangerAndWrapped(t *testing.T) {
	m, _, tasks := actionTestModel(t, board.Task{Title: "Ship me", Status: board.StatusTodo})
	m.openShipPrompt(tasks[0], 0)
	styles := m.themeStyles()
	long := "store refused the write: /home/alice/.local/share/kb/boards/alice/board.json is locked by another process"
	m.action.errorText = long

	rows := appendActionError(styles, nil, long, shipRetryLabel(m.action.warning), 40)
	if len(rows) < 2 {
		t.Fatalf("error rows = %d", len(rows))
	}
	body := rows[1:]
	if len(body) > styles.Metrics.ErrorMaxLines {
		t.Fatalf("error wrapped to %d rows, cap is %d", len(body), styles.Metrics.ErrorMaxLines)
	}
	if !strings.Contains(body[0].content, bandBackground(t, styles.On(theme.TintDanger, theme.OverlaySurf))) ||
		!strings.Contains(body[0].content, foregroundOf(t, styles.On(theme.TintDanger, theme.OverlaySurf))) {
		t.Errorf("dialog error is not TintDanger on OverlaySurf:\n%q", body[0].content)
	}
	joined := ansi.Strip(rowText(body))
	if !strings.Contains(joined, "▲ store refused the write") {
		t.Errorf("error block = %q", joined)
	}
	if !strings.Contains(joined, "Ship anyway") {
		t.Errorf("error block carries no affirmative tail: %q", joined)
	}
	if rows := appendActionError(styles, nil, "", "Ship anyway", 40); rows != nil {
		t.Errorf("an empty message produced %d rows", len(rows))
	}
}

// TestTaskActionLadderPacksItsHints is spec section 10.4.6 on the dialog band:
// the rungs are declared once and the packer resolves the frame, so a narrow
// band drops whole rungs and never leaves a dangling separator.
func TestTaskActionLadderPacksItsHints(t *testing.T) {
	m, _, tasks := actionTestModel(t, board.Task{Title: "Checklist", Status: board.StatusTodo,
		Checks: []board.Check{{Text: "one"}}})
	m.openChecklist(tasks[0])
	styles := m.themeStyles()
	if got := ansi.Strip(m.taskActionFooter(60)); got != "j/k choose | Space toggle | Esc close" {
		t.Fatalf("checklist ladder = %q", got)
	}
	for width := 1; width <= 60; width++ {
		line := ansi.Strip(m.taskActionFooter(width))
		if ansi.StringWidth(line) > width {
			t.Fatalf("width %d: ladder is %d cells: %q", width, ansi.StringWidth(line), line)
		}
		if strings.HasSuffix(line, styles.Glyph.HintSep) || strings.HasPrefix(line, styles.Glyph.HintSep) {
			t.Fatalf("width %d: ladder ends on a separator: %q", width, line)
		}
	}
	m.openPurgePrompt(board.Task{ID: "p", Title: "Purge", Status: board.StatusCancelled})
	if got := ansi.Strip(m.taskActionFooter(60)); !strings.Contains(got, "Enter arm") {
		t.Fatalf("purge ladder = %q", got)
	}
}

// TestHelpFooterIsAPackedLadder pins the pane spec section 10.4.6 names as the
// hand-rolled shape the packer generalizes: the control is the pinned head and
// survives every frame the band can render at all.
func TestHelpFooterIsAPackedLadder(t *testing.T) {
	m := goldenHelpModel(t)
	styles, keys := m.themeStyles(), m.helpKeyMap()
	for width := 1; width <= 80; width++ {
		line := ansi.Strip(m.helpFooter(styles, keys, width))
		if ansi.StringWidth(line) > width {
			t.Fatalf("width %d: footer is %d cells: %q", width, ansi.StringWidth(line), line)
		}
		if strings.HasPrefix(line, styles.Glyph.HintSep) || strings.HasSuffix(line, styles.Glyph.HintSep) {
			t.Fatalf("width %d: footer ends on a separator: %q", width, line)
		}
		if width >= ansi.StringWidth(helpCloseLabel) && !strings.Contains(line, helpCloseLabel) {
			t.Fatalf("width %d: footer dropped the pinned control: %q", width, line)
		}
	}
}

// TestHintsSeparatorIsTheOneToken keeps the ladder's separator in the glyph
// vocabulary of spec section 10.4.1 rather than as a literal in five views.
func TestHintsSeparatorIsTheOneToken(t *testing.T) {
	styles := theme.New(true)
	line, columns := widget.Hints(styles, widget.Ladder{Head: []string{"a"}, Tail: []string{"b"}}, 20)
	if line != "a"+styles.Glyph.HintSep+"b" {
		t.Errorf("packed line = %q", line)
	}
	if columns[0] != 0 || columns[1] != 1+ansi.StringWidth(styles.Glyph.HintSep) {
		t.Errorf("columns = %v", columns)
	}
}

// spinnerTick is one plain-tier tick, the message the dialog's chain carries.
func spinnerTick() spinner.TickMsg { return spinner.TickMsg{} }

func rowText(rows []actionRow) string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.content)
	}
	return strings.Join(out, "\n")
}

// headerRow is the composed frame's header band, found by the title it carries:
// a centered dialog does not start at row 0 of the terminal.
func headerRow(content string) string {
	for _, row := range strings.Split(content, "\n") {
		if strings.Contains(ansi.Strip(row), "DELETE CARD") {
			return row
		}
	}
	return ""
}

// bandBackground is the truecolor background run a cached band style paints, the
// one part of it that survives composition unchanged.
func bandBackground(t *testing.T, style interface{ Render(...string) string }) string {
	t.Helper()
	match := backgroundPattern.FindString(style.Render(" "))
	if match == "" {
		t.Fatalf("style carries no truecolor background: %q", style.Render(" "))
	}
	return match
}

// foregroundOf is the truecolor foreground run a cached style paints.
func foregroundOf(t *testing.T, style interface{ Render(...string) string }) string {
	t.Helper()
	match := foregroundPattern.FindString(style.Render(" "))
	if match == "" {
		t.Fatalf("style carries no truecolor foreground: %q", style.Render(" "))
	}
	return match
}

var (
	backgroundPattern = regexp.MustCompile(`48;2;\d+;\d+;\d+`)
	foregroundPattern = regexp.MustCompile(`38;2;\d+;\d+;\d+`)
)
