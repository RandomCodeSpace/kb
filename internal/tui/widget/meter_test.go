package widget

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// TestMeterCostsItsCellsPlusTwoCaps pins the geometry of spec section 10.1.3:
// the meter is a pill whose interior is a bar, so it costs Cells+2 columns and
// nothing about its state changes that.
func TestMeterCostsItsCellsPlusTwoCaps(t *testing.T) {
	styles := theme.New(true)
	for _, cells := range []int{6, 12, 24, 40} {
		for _, done := range []int{0, 1, cells / 2, cells} {
			rendered := Meter(styles, MeterOpts{Done: done, Total: cells, Cells: cells, Ground: theme.OverlaySurf})
			if got := ansi.StringWidth(rendered); got != cells+2 {
				t.Errorf("meter of %d cells at %d/%d is %d columns, want %d", cells, done, cells, got, cells+2)
			}
			if got := MeterWidth(styles, cells); got != cells+2 {
				t.Errorf("MeterWidth(%d) = %d, want %d", cells, got, cells+2)
			}
		}
	}
}

// TestMeterDefaultsToTheMeterCellsToken keeps the literal that used to live in
// the issue-import view a token.
func TestMeterDefaultsToTheMeterCellsToken(t *testing.T) {
	styles := theme.New(true)
	want := styles.Metrics.MeterCells + 2
	for _, cells := range []int{0, -1} {
		if got := ansi.StringWidth(Meter(styles, MeterOpts{Done: 1, Total: 4, Cells: cells, Ground: theme.OverlaySurf})); got != want {
			t.Errorf("meter with Cells %d is %d columns, want %d", cells, got, want)
		}
		if got := MeterWidth(styles, cells); got != want {
			t.Errorf("MeterWidth(%d) = %d, want %d", cells, got, want)
		}
	}
}

// TestMeterBelowTheFloorRendersNothing is the degradation of spec section
// 10.1.3: below MeterMinCells the caps and the bar are dropped and the caller's
// own i/N text stands alone.
func TestMeterBelowTheFloorRendersNothing(t *testing.T) {
	styles := theme.New(true)
	for cells := 1; cells < styles.Metrics.MeterMinCells; cells++ {
		if got := Meter(styles, MeterOpts{Done: 1, Total: 2, Cells: cells, Ground: theme.OverlaySurf}); got != "" {
			t.Errorf("meter of %d cells rendered %q, want nothing", cells, got)
		}
		if got := MeterWidth(styles, cells); got != 0 {
			t.Errorf("MeterWidth(%d) = %d, want 0", cells, got)
		}
	}
}

// TestMeterFillTracksTheFraction is the whole point of the widget: the fill
// count, and so the ramp's cut, follows the caller's own progress.
func TestMeterFillTracksTheFraction(t *testing.T) {
	styles := theme.New(true)
	const cells = 12
	fills := make([]int, 0, 5)
	for _, done := range []int{0, 3, 6, 9, 12} {
		rendered := Meter(styles, MeterOpts{Done: done, Total: cells, Cells: cells, Ground: theme.OverlaySurf})
		// The right end cap is spelled with the same half block as the fill,
		// so one occurrence of it is always the cap.
		body := ansi.Strip(rendered)
		fills = append(fills, strings.Count(body, styles.Glyph.Rail)-1)
	}
	want := []int{0, 3, 6, 9, 12}
	for index, got := range fills {
		if got != want[index] {
			t.Errorf("fill %d = %d cells, want %d", index, got, want[index])
		}
	}
}

// TestMeterFractionIsClamped keeps a caller that over- or under-counts off the
// end of the blend.
func TestMeterFractionIsClamped(t *testing.T) {
	cases := []struct {
		done, total int
		want        float64
	}{
		{0, 0, 0},
		{4, 0, 0},
		{-3, 10, 0},
		{0, 10, 0},
		{5, 10, 0.5},
		{10, 10, 1},
		{99, 10, 1},
		{1, -2, 0},
	}
	for _, testCase := range cases {
		if got := meterFraction(testCase.done, testCase.total); got != testCase.want {
			t.Errorf("meterFraction(%d, %d) = %v, want %v", testCase.done, testCase.total, got, testCase.want)
		}
	}
}

// TestMeterStatesItsPositionWithoutColor is why Glyph.Track exists: Downsample
// strips color and leaves runes, so an ASCII-pinned structure golden of the one
// widget whose job is position must still read a position.
func TestMeterStatesItsPositionWithoutColor(t *testing.T) {
	styles := theme.New(true)
	const cells = 12
	structural := func(done int) string {
		rendered := Meter(styles, MeterOpts{Done: done, Total: cells, Cells: cells, Ground: theme.OverlaySurf})
		return ansi.Strip(theme.Downsample(rendered, theme.StructureProfile))
	}
	quarter, half := structural(3), structural(6)
	if quarter == half {
		t.Error("two different positions render the same structure; the track glyph is not doing its job")
	}
	if !strings.Contains(quarter, styles.Glyph.Track) {
		t.Errorf("partial meter = %q, want a visible track", quarter)
	}
	if strings.Contains(structural(cells), styles.Glyph.Track) {
		t.Errorf("complete meter = %q, want no track left", structural(cells))
	}
	if !strings.HasPrefix(half, styles.Glyph.CapL) || !strings.HasSuffix(half, styles.Glyph.CapR) {
		t.Errorf("meter = %q, want the pill end caps", half)
	}
}

// TestMeterCapsWearTheRampEndpoints keeps the pill continuous with the bar it
// wraps: the caps name the ramp rather than restating its colors.
func TestMeterCapsWearTheRampEndpoints(t *testing.T) {
	styles := theme.New(true)
	lead, tail := theme.RampStops(theme.GradMeter)
	rendered := Meter(styles, MeterOpts{Done: 1, Total: 2, Cells: 8, Ground: theme.OverlaySurf})
	if !strings.Contains(rendered, styles.On(lead, theme.OverlaySurf).Render(styles.Glyph.CapL)) {
		t.Error("the left cap must wear the ramp's lead color")
	}
	if !strings.Contains(rendered, styles.On(tail, theme.OverlaySurf).Render(styles.Glyph.CapR)) {
		t.Error("the right cap must wear the ramp's tail color")
	}
}

// TestMeterCarriesItsGroundEdgeToEdge is spec section 10.1.1's one rule with no
// exceptions: the component closes every cell it paints with a reset, which
// would drop the surface for the rest of the row.
func TestMeterCarriesItsGroundEdgeToEdge(t *testing.T) {
	styles := theme.New(true)
	rendered := Meter(styles, MeterOpts{Done: 1, Total: 4, Cells: 8, Ground: theme.OverlaySurf})
	surface := ansi.Strip(styles.Overlay.Surf.Render(""))
	if surface != "" {
		t.Fatalf("surface probe rendered %q", surface)
	}
	if !strings.HasPrefix(rendered, styles.SurfaceRun(theme.OverlaySurf, "")[:len(styles.SurfaceRun(theme.OverlaySurf, ""))-len(ansi.ResetStyle)]) {
		t.Errorf("meter = %q, want it opened on its ground", rendered)
	}
	if Meter(styles, MeterOpts{Done: 1, Total: 4, Cells: 8, Ground: theme.Raised}) == rendered {
		t.Error("the ground must reach the render")
	}
}
