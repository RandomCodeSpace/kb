package widget

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// TestGutterMarksFocusWithTheRail is spec section 10.4.3: a blurred row's
// gutter cell is the row's own surface and a focused row's is the Rail glyph in
// the row's accent slot.
func TestGutterMarksFocusWithTheRail(t *testing.T) {
	styles := theme.New(true)
	blurred := ansi.Strip(Gutter(styles, false, theme.Brand, theme.OverlaySurf))
	if strings.TrimSpace(blurred) != "" {
		t.Errorf("blurred gutter = %q, want spaces", blurred)
	}
	focused := ansi.Strip(Gutter(styles, true, theme.Brand, theme.OverlaySurf))
	if !strings.HasPrefix(focused, styles.Glyph.Rail) {
		t.Errorf("focused gutter = %q, want the Rail glyph first", focused)
	}
	if !strings.Contains(Gutter(styles, true, theme.Brand, theme.OverlaySurf),
		styles.On(theme.Brand, theme.OverlaySurf).Render(styles.Glyph.Rail)) {
		t.Error("focused gutter does not carry the Rail in its accent slot")
	}
}

// TestGutterIsAlwaysReserved is the section 10.4.4 corollary that makes the
// gutter worth having at all: both states cost the same cells, so focus moving
// onto a row never reflows the text it lands on.
func TestGutterIsAlwaysReserved(t *testing.T) {
	styles := theme.New(true)
	want := styles.Metrics.FocusGutterW + styles.Metrics.FocusGutterGap
	for _, accent := range []theme.Slot{theme.Brand, theme.HueDoing, theme.HueDone} {
		for _, on := range []theme.Slot{theme.OverlaySurf, theme.Surface, theme.Card} {
			for _, focused := range []bool{false, true} {
				got := ansi.StringWidth(Gutter(styles, focused, accent, on))
				if got != want {
					t.Errorf("accent %d on %d focused=%v is %d cells, want %d",
						accent, on, focused, got, want)
				}
			}
		}
	}
}
