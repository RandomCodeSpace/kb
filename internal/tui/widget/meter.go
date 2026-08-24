package widget

import (
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// MeterOpts describes one progress meter. Spec section 10.1.3: the meter is the
// only pill whose interior carries a gradient, because it is the only pill
// whose interior is a bounded fraction. A pill with no denominator would encode
// a position that does not exist, so every other pill stays flat.
type MeterOpts struct {
	Done   int        // work finished
	Total  int        // work in total; zero or less renders an empty bar
	Cells  int        // bar width; zero or less takes the MeterCells default
	Ground theme.Slot // the surface the pill is drawn onto
}

// Meter renders the gradient progress pill. The bar is the adopted progress
// component with the GradMeter ramp spanning its full width and the fill
// cutting it, wrapped in the section 3.6 end caps: the left cap in the ramp's
// lead color, the right in its tail.
//
// Cost is Cells+2 columns. Below MeterMinCells the caps and the bar are dropped
// and the empty string is returned, so the caller's own i/N text stands alone.
func Meter(styles *theme.Styles, opts MeterOpts) string {
	cells := opts.Cells
	if cells <= 0 {
		cells = styles.Metrics.MeterCells
	}
	if cells < styles.Metrics.MeterMinCells {
		return ""
	}
	lead, tail := theme.RampStops(theme.GradMeter)
	bar := styles.Progress
	bar.SetWidth(cells)
	// The whole pill is one gradient run laid on the caller's ground: the
	// component closes every cell it paints with a reset, which would drop the
	// surface for the rest of the row (spec sections 5.3 and 10.1.1).
	return styles.SurfaceRun(opts.Ground,
		styles.On(lead, opts.Ground).Render(styles.Glyph.CapL)+
			bar.ViewAs(meterFraction(opts.Done, opts.Total))+
			styles.On(tail, opts.Ground).Render(styles.Glyph.CapR))
}

// MeterWidth is the cell cost of a meter of this bar width, end caps included,
// or zero when the width is below the floor that drops the bar.
func MeterWidth(styles *theme.Styles, cells int) int {
	if cells <= 0 {
		cells = styles.Metrics.MeterCells
	}
	if cells < styles.Metrics.MeterMinCells {
		return 0
	}
	return cells + 2
}

// meterFraction is the filled share of the bar, clamped to the unit interval so
// a caller that over-counts does not index the blend off its end.
func meterFraction(done, total int) float64 {
	if total <= 0 || done <= 0 {
		return 0
	}
	if done >= total {
		return 1
	}
	return float64(done) / float64(total)
}
