package widget

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// TestBusyShapeIsFrameGapLabel is spec section 10.8.4: frame, BusyGap columns,
// then a lowercase present-continuous label with no ellipsis of its own.
func TestBusyShapeIsFrameGapLabel(t *testing.T) {
	styles := theme.New(true)
	row := Busy(styles, BusyOpts{Frame: "*", Label: "loading", On: theme.Surface, Width: 40})
	want := "*" + strings.Repeat(" ", styles.Metrics.BusyGap) + "loading"
	if got := ansi.Strip(row); got != want {
		t.Errorf("busy row = %q, want %q", got, want)
	}
	if !strings.Contains(row, styles.On(theme.FgSubtle, theme.Surface).Render("loading")) {
		t.Errorf("label is not FgSubtle on the row's surface:\n%q", row)
	}
}

// TestBusyWithoutAFrameIsTheLabelAlone is rule 4 of spec section 10.8.4: at most
// one spinner animates on a surface, and the suppressed one renders its label
// with no frame and no reserved gap.
func TestBusyWithoutAFrameIsTheLabelAlone(t *testing.T) {
	styles := theme.New(true)
	got := ansi.Strip(Busy(styles, BusyOpts{Label: "saving", On: theme.OverlaySurf, Width: 40}))
	if got != "saving" {
		t.Errorf("frameless busy row = %q, want %q", got, "saving")
	}
}

// TestBusyNeverOverrunsItsWidth keeps the row inside the surface that asked for
// it, and cuts the label with the section 3.3 primitive rather than bare.
func TestBusyNeverOverrunsItsWidth(t *testing.T) {
	styles := theme.New(true)
	opts := BusyOpts{Frame: "*", Label: "fetching and drafting", On: theme.Surface}
	for width := 0; width <= 30; width++ {
		opts.Width = width
		row := ansi.Strip(Busy(styles, opts))
		if ansi.StringWidth(row) > width {
			t.Fatalf("width %d: row is %d cells: %q", width, ansi.StringWidth(row), row)
		}
		if width > 3 && !strings.HasSuffix(row, styles.Glyph.Ellipsis) && row != "* fetching and drafting" {
			t.Fatalf("width %d: cut label carries no ellipsis: %q", width, row)
		}
	}
	if got := Busy(styles, BusyOpts{Frame: "*", Label: "x", On: theme.Surface, Width: 0}); got != "" {
		t.Errorf("zero width rendered %q", got)
	}
}
