package widget

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// TestErrorRowIsGlyphSpaceMessage is the row of spec section 10.8.5.
func TestErrorRowIsGlyphSpaceMessage(t *testing.T) {
	styles := theme.New(true)
	rows := Error(styles, ErrorOpts{
		Message: "dial tcp: connection refused", On: theme.OverlaySurf, Width: 48, MaxLines: 3,
	})
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if got := ansi.Strip(rows[0]); got != "▲ dial tcp: connection refused" {
		t.Errorf("error row = %q", got)
	}
}

// TestErrorHueIsSelectedByTheTier is ratified call 12: StatusDanger fails AA on
// OverlaySurf, so a panel takes TintDanger and the board tiers keep
// StatusDanger. The caller names its tier and never its hue.
func TestErrorHueIsSelectedByTheTier(t *testing.T) {
	styles := theme.New(true)
	panel := Error(styles, ErrorOpts{Message: "nope", On: theme.OverlaySurf, Width: 40, MaxLines: 3})
	if !strings.Contains(panel[0], styles.On(theme.TintDanger, theme.OverlaySurf).Render("nope")) {
		t.Errorf("panel error is not TintDanger on OverlaySurf:\n%q", panel[0])
	}
	board := Error(styles, ErrorOpts{Message: "nope", On: theme.Surface, Width: 40, MaxLines: 1})
	if !strings.Contains(board[0], styles.On(theme.StatusDanger, theme.Surface).Render("nope")) {
		t.Errorf("board error is not StatusDanger on Surface:\n%q", board[0])
	}
}

// TestErrorSanitizesAndCollapsesNewlines is step 1 of the wrapping rule: an
// embedded newline must never reach the row grid.
func TestErrorSanitizesAndCollapsesNewlines(t *testing.T) {
	styles := theme.New(true)
	rows := Error(styles, ErrorOpts{
		Message: "write failed:\n\tstore locked\x07", On: theme.OverlaySurf, Width: 48, MaxLines: 3,
	})
	joined := strings.Join(rows, "\n")
	if strings.Contains(ansi.Strip(joined), "\t") || strings.Contains(ansi.Strip(joined), "\x07") {
		t.Errorf("control characters survived:\n%q", joined)
	}
	if got := ansi.Strip(rows[0]); got != "▲ write failed: store locked" {
		t.Errorf("sanitized row = %q", got)
	}
}

// TestErrorWrapsWithAHangingIndent is step 4: continuation lines hang-indent by
// the glyph's width plus one so the block reads as one object, and neither the
// glyph nor the word "error" is repeated per line.
func TestErrorWrapsWithAHangingIndent(t *testing.T) {
	styles := theme.New(true)
	rows := Error(styles, ErrorOpts{
		Message:  "forge fetch failed because the configured base url refused the connection twice",
		On:       theme.OverlaySurf,
		Width:    24,
		MaxLines: 3,
	})
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	if !strings.HasPrefix(ansi.Strip(rows[0]), "▲ ") {
		t.Errorf("first row does not carry the alert glyph: %q", ansi.Strip(rows[0]))
	}
	for _, row := range rows[1:] {
		plain := ansi.Strip(row)
		if strings.Contains(plain, styles.Glyph.Alert) {
			t.Errorf("continuation row repeats the glyph: %q", plain)
		}
		if !strings.HasPrefix(plain, "  ") {
			t.Errorf("continuation row is not hang-indented: %q", plain)
		}
	}
}

// TestErrorMarksItsTruncation is step 3: the bare ansi.Truncate the fit helpers
// use is never the primitive for an error, so the last allotted line carries
// the ellipsis when text remains.
func TestErrorMarksItsTruncation(t *testing.T) {
	styles := theme.New(true)
	rows := Error(styles, ErrorOpts{
		Message:  strings.Repeat("overflowing ", 40),
		On:       theme.OverlaySurf,
		Width:    20,
		MaxLines: 2,
	})
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if !strings.HasSuffix(ansi.Strip(rows[1]), styles.Glyph.Ellipsis) {
		t.Errorf("last row is not marked: %q", ansi.Strip(rows[1]))
	}
}

// TestErrorHardTruncatesAWordLongerThanTheMeasure is step 2: a word that cannot
// fit on a line of its own is cut rather than overflowing the panel.
func TestErrorHardTruncatesAWordLongerThanTheMeasure(t *testing.T) {
	styles := theme.New(true)
	rows := Error(styles, ErrorOpts{
		Message:  strings.Repeat("x", 80) + " and more text after it to force a second line",
		On:       theme.OverlaySurf,
		Width:    20,
		MaxLines: 3,
	})
	for _, row := range rows {
		if got := ansi.StringWidth(row); got > 20 {
			t.Errorf("row is %d cells: %q", got, ansi.Strip(row))
		}
	}
}

// TestErrorRetryTailNamesTheControl is the retry rule: an errored operation
// does not grow a Retry button, the row names the control that started it.
func TestErrorRetryTailNamesTheControl(t *testing.T) {
	styles := theme.New(true)
	rows := Error(styles, ErrorOpts{
		Message: "forge fetch failed", Key: "Import", On: theme.OverlaySurf, Width: 48, MaxLines: 3,
	})
	if got := ansi.Strip(rows[len(rows)-1]); !strings.HasSuffix(got, "  Import") {
		t.Errorf("tail is not separated by ActionGap: %q", got)
	}
	if !strings.Contains(rows[len(rows)-1], styles.OnBold(theme.FgBase, theme.OverlaySurf).Render("Import")) {
		t.Errorf("tail key is not FgBase bold:\n%q", rows[len(rows)-1])
	}
	verbed := Error(styles, ErrorOpts{
		Message: "failed", Key: "r", Verb: "retry", On: theme.OverlaySurf, Width: 48, MaxLines: 1,
	})
	if got := ansi.Strip(verbed[0]); !strings.HasSuffix(got, "  r retry") {
		t.Errorf("verbed tail = %q", got)
	}
}

// TestErrorDropsATailThatDoesNotFit keeps the block inside its measure: the
// message is what the row is for, and a tail that would overrun the panel is
// not rendered at all.
func TestErrorDropsATailThatDoesNotFit(t *testing.T) {
	styles := theme.New(true)
	rows := Error(styles, ErrorOpts{
		Message: "short", Key: "a very long button label indeed", On: theme.OverlaySurf, Width: 14, MaxLines: 1,
	})
	if got := ansi.StringWidth(rows[0]); got > 14 {
		t.Errorf("row is %d cells: %q", got, ansi.Strip(rows[0]))
	}
	if strings.Contains(ansi.Strip(rows[0]), "button label") {
		t.Errorf("tail was rendered anyway: %q", ansi.Strip(rows[0]))
	}
}

// TestErrorNeverOverrunsItsWidth is the invariant every caller depends on.
func TestErrorNeverOverrunsItsWidth(t *testing.T) {
	styles := theme.New(true)
	for width := 0; width <= 40; width++ {
		rows := Error(styles, ErrorOpts{
			Message:  "forge fetch failed: dial tcp 10.0.0.4:443: connection refused",
			Key:      "Import",
			On:       theme.OverlaySurf,
			Width:    width,
			MaxLines: 3,
		})
		if width == 0 && rows != nil {
			t.Errorf("width 0 rendered %d rows", len(rows))
		}
		if len(rows) > 3 {
			t.Errorf("width %d: %d rows exceed the cap", width, len(rows))
		}
		for _, row := range rows {
			if got := ansi.StringWidth(row); got > width {
				t.Errorf("width %d: row is %d cells: %q", width, got, ansi.Strip(row))
			}
		}
	}
}

// TestErrorWithNoMessageRendersNothing keeps a cleared status from painting an
// empty glyph row: a surface with nothing to report renders no error block.
func TestErrorWithNoMessageRendersNothing(t *testing.T) {
	styles := theme.New(true)
	if rows := Error(styles, ErrorOpts{On: theme.OverlaySurf, Width: 40, MaxLines: 3}); rows != nil {
		t.Errorf("empty message rendered %d rows", len(rows))
	}
}
