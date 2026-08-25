package widget

import (
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// The hover contract of spec section 10.5.4, assertions 1 and 2. Hover is a
// recolor and nothing else, so the proof is that stripping what a state change
// is allowed to touch leaves the two renders byte for byte identical, and that
// the region set the next frame resolves hover against does not move either.
//
// theme.Downsample to the structure profile rewrites color and leaves the
// underline and bold attributes in place, so these assertions strip SGR
// outright with ansi.Strip: that is the stronger form of the same proof, and it
// is the one the chip's underline cue needs.

// hoverCases is every hover treatment of section 10.5.1 that is a pure recolor.
// The band is not here: its cue is a glyph substitution, which section 10.4.4
// permits for another glyph of identical cell width and which a stripped
// comparison would therefore flag. It gets its own test below.
func hoverCases(styles *theme.Styles) map[string][2][]string {
	card := func(hovered, selected, alt bool, density Density) []string {
		return Card(styles, CardOpts{
			Title: "ship the pointer slice", Seq: "#42", Desc: "hover is a recolor",
			Meta:     []string{Priority(styles, 1, styles.Surface(selected, alt), false)},
			Labels:   []string{"type::feature", "urgent"},
			Priority: 1, Selected: selected, Alt: alt, Hovered: hovered,
			Width: 34, TitleLines: 2, DescLines: 1, LabelRows: 1, Density: density,
		})
	}
	pill := func(hovered, flat bool, tag string) []string {
		return []string{Label(styles, tag, theme.Card, flat, hovered)}
	}
	row := func(hovered bool) []string {
		on := styles.RowSurface(hovered)
		return []string{
			OverlayRowOn(styles, Check(styles, "write the parity guard", CheckOpen, on, false), 40, on),
			OverlayRowOn(styles, Check(styles, "write the parity guard", CheckDone, on, true), 40, on),
		}
	}
	cases := map[string][2][]string{
		"card":            {card(false, false, false, DensityNormal), card(true, false, false, DensityNormal)},
		"card zebra":      {card(false, false, true, DensityCompact), card(true, false, true, DensityCompact)},
		"card selected":   {card(false, true, false, DensityNormal), card(true, true, false, DensityNormal)},
		"pill scoped":     {pill(false, false, "type::feature"), pill(true, false, "type::feature")},
		"pill plain":      {pill(false, false, "urgent"), pill(true, false, "urgent")},
		"pill flat":       {pill(false, true, "type::feature"), pill(true, true, "type::feature")},
		"pill flat plain": {pill(false, true, "urgent"), pill(true, true, "urgent")},
		"overlay row":     {row(false), row(true)},
	}
	return cases
}

// TestHoverNeverReflows is assertion 1: rendered hovered and unhovered from
// identical opts, every row is the same cell width and the two renders are
// identical once the attributes and colors a state change may spend are gone.
func TestHoverNeverReflows(t *testing.T) {
	styles := theme.New(true)
	for name, pair := range hoverCases(styles) {
		t.Run(name, func(t *testing.T) {
			rest, hovered := pair[0], pair[1]
			if len(rest) != len(hovered) {
				t.Fatalf("hover changed the row count from %d to %d", len(rest), len(hovered))
			}
			for index := range rest {
				if got, want := ansi.StringWidth(hovered[index]), ansi.StringWidth(rest[index]); got != want {
					t.Errorf("row %d is %d cells hovered and %d cells at rest", index, got, want)
				}
				if got, want := ansi.Strip(hovered[index]), ansi.Strip(rest[index]); got != want {
					t.Errorf("row %d hovered strips to %q, want %q", index, got, want)
				}
			}
		})
	}
}

// TestHoverIsVisibleBeforeItIsStripped keeps the previous test honest: an
// assertion that two renders are identical after stripping proves nothing if
// they were identical before it.
func TestHoverIsVisibleBeforeItIsStripped(t *testing.T) {
	styles := theme.New(true)
	for name, pair := range hoverCases(styles) {
		t.Run(name, func(t *testing.T) {
			if name == "card selected" {
				// Spec section 10.5.1: a selected card renders no hover. Its
				// surface is already Raised edge to edge, so the tier step has
				// nowhere to go, and the pointer over the already-selected card
				// is offering nothing new.
				if !reflect.DeepEqual(pair[0], pair[1]) {
					t.Fatal("a selected card rendered a hover state")
				}
				return
			}
			if reflect.DeepEqual(pair[0], pair[1]) {
				t.Fatal("hover changed nothing at all")
			}
		})
	}
}

// TestHoveredCardRaisesOnlyItsRailCell is the card row of section 10.5.1: where
// selection already spends the tier step, hover raises the rail cell instead of
// the surface, so the two stay legible as different things rather than one
// thing seen twice.
func TestHoveredCardRaisesOnlyItsRailCell(t *testing.T) {
	styles := theme.New(true)
	opts := CardOpts{Title: "rail only", Priority: 1, Width: 24, DescLines: 1, Density: DensityNormal}
	rest := Card(styles, opts)
	opts.Hovered = true
	hovered := Card(styles, opts)

	restRail := Rail(styles, 1, theme.Card, false)
	wantRail := Rail(styles, 1, theme.Raised, false)
	for index := range hovered {
		if rest[index] == hovered[index] {
			t.Fatalf("row %d did not change on hover", index)
		}
		if !strings.HasPrefix(hovered[index], wantRail) {
			t.Errorf("row %d does not open with the raised rail %q", index, wantRail)
			continue
		}
		if !strings.HasPrefix(rest[index], restRail) {
			t.Fatalf("row %d does not open with the resting rail %q", index, restRail)
		}
		// Everything right of the rail is untouched: the surface keeps its tier.
		if got, want := strings.TrimPrefix(hovered[index], wantRail),
			strings.TrimPrefix(rest[index], restRail); got != want {
			t.Errorf("row %d body changed on hover:\n got %q\nwant %q", index, got, want)
		}
	}
}

// TestHoveredBandThickensItsRailAndOnlyThat is the band row of section 10.5.1.
// The band is already bold and cannot change background without becoming the
// focused band, so the cue is the rail glyph, borrowing selection's own
// vocabulary in the one slot the band has spare. Cost: zero cells.
func TestHoveredBandThickensItsRailAndOnlyThat(t *testing.T) {
	styles := theme.New(true)
	base := BandOpts{Index: 2, Label: "DOING", Count: 3, Hue: theme.HueDoing, Width: 42}
	for _, width := range []int{42, 30, 20, 16} {
		opts := base
		opts.Width = width
		rest := Band(styles, opts)
		opts.Hovered = true
		hovered := Band(styles, opts)

		if got, want := ansi.StringWidth(hovered), ansi.StringWidth(rest); got != want {
			t.Errorf("width %d: hovered band is %d cells, want %d", width, got, want)
		}
		strippedRest, strippedHover := ansi.Strip(rest), ansi.Strip(hovered)
		if column(strippedHover, "DOING") != column(strippedRest, "DOING") {
			t.Errorf("width %d: the label column moved on hover", width)
		}
		if len(strippedHover) == 0 || len(strippedRest) == 0 {
			continue
		}
		wantHead := styles.Glyph.RailFull + strippedRest[len(styles.Glyph.Rail):]
		if strippedHover != wantHead {
			t.Errorf("width %d: hovered band strips to %q, want %q", width, strippedHover, wantHead)
		}

		// The focused band is already the acting column: there is nothing for
		// hover to promise, and ratified call 9 keeps focus off the pointer.
		opts.Focused, opts.Hovered = true, false
		focused := Band(styles, opts)
		opts.Hovered = true
		if got := Band(styles, opts); got != focused {
			t.Errorf("width %d: a focused band rendered a hover state", width)
		}
	}
}

// TestHoverLeavesTheRegionSetByteForByteIdentical is assertion 2. Hover is
// resolved at draw time against the map the previous frame recorded, and that
// is only correct because hover cannot have moved anything: a treatment that
// shifted a label pill by one cell would feed its own reflow back into the
// pointer.
func TestHoverLeavesTheRegionSetByteForByteIdentical(t *testing.T) {
	styles := theme.New(true)
	for _, density := range []Density{DensityNormal, DensityCompact} {
		opts := CardOpts{
			Title: "spans do not move", Seq: "#7", Desc: "region-set stability",
			Meta:   []string{Priority(styles, 2, theme.Card, false)},
			Labels: []string{"type::feature", "urgent", "area::tui"},
			// Priority 2 and a normal density card so both chip rows render.
			Priority: 2, Width: 36, TitleLines: 2, DescLines: 1, LabelRows: 1, Density: density,
		}
		_, rest := CardWithSpans(styles, opts)
		if len(rest) == 0 {
			t.Fatalf("density %v recorded no label spans to compare", density)
		}
		for _, tag := range append([]string{""}, opts.Labels...) {
			hoverOpts := opts
			hoverOpts.Hovered, hoverOpts.HoverTag = true, tag
			_, hovered := CardWithSpans(styles, hoverOpts)
			if !reflect.DeepEqual(hovered, rest) {
				t.Errorf("density %v hovering %q moved the spans:\n got %+v\nwant %+v",
					density, tag, hovered, rest)
			}
		}
	}
}

// TestHoverTagUnderlinesOnlyTheHoveredPill keeps the card's pill hover keyed to
// the pointer rather than to the card: a hovered card is not a hovered label.
func TestHoverTagUnderlinesOnlyTheHoveredPill(t *testing.T) {
	styles := theme.New(true)
	opts := CardOpts{
		Title: "one pill at a time", Labels: []string{"type::feature", "urgent"},
		Priority: 3, Width: 36, TitleLines: 2, DescLines: 1, LabelRows: 1, Density: DensityNormal,
	}
	rest := Card(styles, opts)
	opts.HoverTag = "urgent"
	one := Card(styles, opts)
	if reflect.DeepEqual(rest, one) {
		t.Fatal("hovering a label pill changed nothing")
	}
	opts.HoverTag = "type::feature"
	other := Card(styles, opts)
	if reflect.DeepEqual(one, other) {
		t.Fatal("both label pills rendered the same hover")
	}
	opts.HoverTag = "not-on-this-card"
	if absent := Card(styles, opts); !reflect.DeepEqual(rest, absent) {
		t.Fatal("a tag this card does not carry lit a pill")
	}
	opts.Labels, opts.HoverTag = []string{""}, ""
	if empty := Card(styles, opts); len(empty) == 0 {
		t.Fatal("an empty tag dropped the card")
	}
}

// TestRowSurfaceIsTheNeutralHoveredButtonPair pins the reuse section 10.5.1
// makes deliberate: a hovered row and a hovered Neutral button inside the same
// panel are the same surface, so the panel reads as one system.
func TestRowSurfaceIsTheNeutralHoveredButtonPair(t *testing.T) {
	styles := theme.New(true)
	if got := styles.RowSurface(false); got != theme.OverlaySurf {
		t.Errorf("resting row surface = %d, want OverlaySurf", got)
	}
	if got := styles.RowSurface(true); got != theme.OverlayBand {
		t.Errorf("hovered row surface = %d, want OverlayBand", got)
	}
	button := Button(styles, ButtonOpts{Text: "Cancel", Variant: theme.ButtonNeutral, Hovered: true})
	row := OverlayRowOn(styles, "", 8, styles.RowSurface(true))
	if !sharesBackground(button, row) {
		t.Errorf("the hovered row and the hovered Neutral button do not share a fill:\nrow=%q\nbtn=%q", row, button)
	}
}

// sharesBackground reports whether both runs open with the same background SGR
// parameters, which is what "the same surface" means at the cell level.
func sharesBackground(left, right string) bool {
	return background(left) != "" && background(left) == background(right)
}

func background(run string) string {
	const marker = "48;2;"
	start := strings.Index(run, marker)
	if start < 0 {
		return ""
	}
	tail := run[start+len(marker):]
	end := 0
	for end < len(tail) && (tail[end] == ';' || (tail[end] >= '0' && tail[end] <= '9')) {
		end++
	}
	return tail[:end]
}

// TestOverlayRowOnRejectsAnEmptyWidth keeps the widened entry point on the same
// contract as the one it generalizes.
func TestOverlayRowOnRejectsAnEmptyWidth(t *testing.T) {
	styles := theme.New(true)
	if got := OverlayRowOn(styles, "body", 0, theme.OverlayBand); got != "" {
		t.Errorf("zero width rendered %q", got)
	}
	if got, want := OverlayRow(styles, "body", 20),
		OverlayRowOn(styles, "body", 20, theme.OverlaySurf); got != want {
		t.Errorf("OverlayRow diverged from its own surface:\n got %q\nwant %q", got, want)
	}
}
