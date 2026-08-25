package widget

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// The no-reflow guard of spec section 10.4.4. For every element with state
// variants, the rendered cell width is a function of its content and its width
// argument only, never of its state. A state change may alter colors and
// attributes freely and may substitute a glyph only for another of identical
// cell width; bold, underline and reverse cost zero cells.
//
// These are pure string tests: no terminal, no tick, no golden.

// TestButtonWidthIsStateInvariant covers section 1.9's state matrix, which
// varies color and weight only.
func TestButtonWidthIsStateInvariant(t *testing.T) {
	styles := theme.New(true)
	base := ButtonOpts{Text: "Purge", UnderlineIndex: 0, Padding: [2]int{1, 1}}
	states := map[string]func(*ButtonOpts){
		"rest":     func(*ButtonOpts) {},
		"hovered":  func(o *ButtonOpts) { o.Hovered = true },
		"focused":  func(o *ButtonOpts) { o.Selected = true },
		"armed":    func(o *ButtonOpts) { o.Armed = true },
		"pressed":  func(o *ButtonOpts) { o.Pressed = true },
		"unmarked": func(o *ButtonOpts) { o.UnderlineIndex = -1 },
	}
	want := ansi.StringWidth(Button(styles, base))
	for _, variant := range []theme.ButtonVariant{
		theme.ButtonNeutral, theme.ButtonPrimary, theme.ButtonSuccess, theme.ButtonDanger,
	} {
		for name, apply := range states {
			opts := base
			opts.Variant = variant
			apply(&opts)
			if got := ansi.StringWidth(Button(styles, opts)); got != want {
				t.Errorf("variant %d %s button is %d cells, want %d", variant, name, got, want)
			}
		}
	}
}

// TestRailWidthIsStateInvariant covers section 2.4: the glyph thickens from a
// half block to a full block and both are one cell, so CardRail = 1 is reserved
// in both states.
func TestRailWidthIsStateInvariant(t *testing.T) {
	styles := theme.New(true)
	for _, priority := range []int{1, 2, 3, 4, 9} {
		for _, surface := range []theme.Slot{theme.Card, theme.Zebra, theme.Raised} {
			for _, selected := range []bool{false, true} {
				if got := ansi.StringWidth(Rail(styles, priority, surface, selected)); got != 1 {
					t.Errorf("P%d rail on surface %d selected=%v is %d cells, want 1", priority, surface, selected, got)
				}
			}
		}
	}
}

// TestCheckWidthIsStateInvariant covers the checklist row: all three marks are
// one cell, so the label never moves as an item is ticked or dropped.
func TestCheckWidthIsStateInvariant(t *testing.T) {
	styles := theme.New(true)
	want := -1
	for _, state := range []CheckState{CheckOpen, CheckDone, CheckDropped} {
		for _, focused := range []bool{false, true} {
			got := ansi.StringWidth(Check(styles, "ship the thing", state, theme.OverlaySurf, focused))
			if want < 0 {
				want = got
				continue
			}
			if got != want {
				t.Errorf("check state %d focused=%v is %d cells, want %d", state, focused, got, want)
			}
		}
	}
}

// TestBandWidthAndLabelColumnAreStateInvariant is the assertion the band failed
// before spec section 10.4.4 restored its status dot: the band's total width was
// already identical in both states, but its label started at column 5 unfocused
// and column 4 focused, so moving focus across the board jittered every label
// one cell.
func TestBandWidthAndLabelColumnAreStateInvariant(t *testing.T) {
	styles := theme.New(true)
	for _, width := range []int{42, 30, 20, 16} {
		var blurred, focused string
		for _, state := range []bool{false, true} {
			rendered := Band(styles, BandOpts{
				Index: 2, Label: "DOING", Count: 3, Hue: theme.HueDoing, Focused: state, Width: width,
			})
			if got := ansi.StringWidth(rendered); got != width {
				t.Errorf("band at width %d focused=%v is %d cells", width, state, got)
			}
			if state {
				focused = ansi.Strip(rendered)
				continue
			}
			blurred = ansi.Strip(rendered)
		}
		if column(blurred, "DOING") != column(focused, "DOING") {
			t.Errorf("width %d: label column moved from %d to %d on focus",
				width, column(blurred, "DOING"), column(focused, "DOING"))
		}
		if got := column(blurred, "DOING"); got >= 0 && got != styles.Metrics.BandHeadW {
			t.Errorf("width %d: label starts at column %d, want the BandHeadW reserve of %d",
				width, got, styles.Metrics.BandHeadW)
		}
	}
}

// column is the cell column of needle inside an already-stripped row, or a
// negative value when the row does not carry it.
func column(row, needle string) int {
	index := strings.Index(row, needle)
	if index < 0 {
		return -1
	}
	return ansi.StringWidth(row[:index])
}

// TestFocusableRowWidthAndContentColumnAreStateInvariant is section 10.4.4's
// focusable overlay row: two cells of surface blurred against a Rail plus a gap
// focused, so the row's prose starts in the same column in both states and the
// row costs the same width.
func TestFocusableRowWidthAndContentColumnAreStateInvariant(t *testing.T) {
	styles := theme.New(true)
	const content = "select card"
	for _, width := range []int{60, 40, 24, 16} {
		var blurred, focused string
		for _, state := range []bool{false, true} {
			row := OverlayRow(styles, Gutter(styles, state, theme.Brand, theme.OverlaySurf)+
				styles.On(theme.FgBase, theme.OverlaySurf).Render(content), width)
			if got := ansi.StringWidth(row); got != width {
				t.Errorf("width %d focused=%v row is %d cells", width, state, got)
			}
			if state {
				focused = ansi.Strip(row)
				continue
			}
			blurred = ansi.Strip(row)
		}
		if column(blurred, content) != column(focused, content) {
			t.Errorf("width %d: content column moved from %d to %d on focus",
				width, column(blurred, content), column(focused, content))
		}
	}
}

// TestScrollAffordanceGeometryIsStateInvariant is section 10.4.4's scroll row:
// the tint changes with the linger, the geometry does not.
func TestScrollAffordanceGeometryIsStateInvariant(t *testing.T) {
	styles := theme.New(true)
	base := ScrollbarOpts{Total: 120, Visible: 12, Offset: 36, Height: 12, On: theme.OverlaySurf}
	settled := Scrollbar(styles, base)
	base.Active = true
	active := Scrollbar(styles, base)
	if len(settled) != len(active) {
		t.Fatalf("settled has %d rows and active %d", len(settled), len(active))
	}
	for index := range settled {
		if ansi.StringWidth(settled[index]) != ansi.StringWidth(active[index]) {
			t.Errorf("row %d changed width with the linger state", index)
		}
	}
}

// TestMeterGeometryIsStateInvariant keeps the meter's cost a function of its
// width argument alone: a pill whose interior is a bounded fraction still costs
// Cells+2 columns at every fraction.
func TestMeterGeometryIsStateInvariant(t *testing.T) {
	styles := theme.New(true)
	const cells = 16
	want := ansi.StringWidth(Meter(styles, MeterOpts{Done: 0, Total: cells, Cells: cells, Ground: theme.OverlaySurf}))
	for done := -2; done <= cells+2; done++ {
		got := ansi.StringWidth(Meter(styles, MeterOpts{Done: done, Total: cells, Cells: cells, Ground: theme.OverlaySurf}))
		if got != want {
			t.Errorf("meter at %d/%d is %d cells, want %d", done, cells, got, want)
		}
	}
}

// TestFilterLabelWidthIsStateInvariant covers the filter bar's pill, whose three
// state axes are the toggle (wheel hue against the dim form), keyboard focus
// (half-block end caps against full blocks) and hover (an underline on the body).
// None of them may move a cell, or toggling one label would shuffle every label
// behind it and the row's hit regions with them.
func TestFilterLabelWidthIsStateInvariant(t *testing.T) {
	styles := theme.New(true)
	for _, tag := range []string{"bug", "type::feature", "数", "::feature"} {
		want := -1
		for _, selected := range []bool{false, true} {
			for _, focused := range []bool{false, true} {
				for _, hovered := range []bool{false, true} {
					got := ansi.StringWidth(FilterLabel(styles, tag, theme.Canvas, selected, focused, hovered))
					if want < 0 {
						want = got
						continue
					}
					if got != want {
						t.Errorf("%q selected=%v focused=%v hovered=%v is %d cells, want %d",
							tag, selected, focused, hovered, got, want)
					}
				}
			}
		}
		if plain := ansi.StringWidth(Label(styles, tag, theme.Canvas, false, false)); want != plain+2 {
			t.Errorf("%q filter pill is %d cells, want the board pill's %d plus the toggle mark",
				tag, want, plain)
		}
	}
}

// TestGradientCostsNoCells keeps the one rule that makes a ramp safe on chrome
// that shares a row with anything else: it recolors, it never reflows.
func TestGradientCostsNoCells(t *testing.T) {
	styles := theme.New(true)
	for _, text := range []string{"CHECKLIST", "DETAIL", "x", "a much longer run than the ramp has steps"} {
		for ramp := theme.Ramp(0); ramp < theme.GradSection+5; ramp++ {
			if got := ansi.StringWidth(styles.Grad(ramp, text)); got != ansi.StringWidth(text) {
				t.Errorf("ramp %d on %q is %d cells, want %d", ramp, text, got, ansi.StringWidth(text))
			}
			if got := ansi.StringWidth(styles.GradBold(ramp, text)); got != ansi.StringWidth(text) {
				t.Errorf("bold ramp %d on %q is %d cells, want %d", ramp, text, got, ansi.StringWidth(text))
			}
		}
	}
}
