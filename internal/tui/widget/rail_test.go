package widget

import (
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

func TestRailIsOneCell(t *testing.T) {
	styles := theme.New(true)
	for _, surface := range []theme.Slot{theme.Card, theme.Raised, theme.Zebra} {
		for _, selected := range []bool{false, true} {
			rendered := Rail(styles, 1, surface, selected)
			if got := ansi.StringWidth(rendered); got != 1 {
				t.Errorf("rail on surface %d is %d cells, want 1", surface, got)
			}
		}
	}
}

func TestRailThickensOnSelection(t *testing.T) {
	styles := theme.New(true)
	if got := ansi.Strip(Rail(styles, 2, theme.Card, false)); got != "▌" {
		t.Errorf("resting rail = %q, want the half block", got)
	}
	if got := ansi.Strip(Rail(styles, 2, theme.Raised, true)); got != "█" {
		t.Errorf("selected rail = %q, want the full block", got)
	}
}

func TestRailUsesTheCachedStylesForTheCommonSurfaces(t *testing.T) {
	styles := theme.New(true)
	if Rail(styles, 1, theme.Card, false) != styles.Rail[1].Render(styles.Glyph.Rail) {
		t.Error("a resting rail on the card surface must use the cached style")
	}
	if Rail(styles, 1, theme.Raised, true) != styles.RailSel[1].Render(styles.Glyph.RailFull) {
		t.Error("a selected rail must use the cached raised style")
	}
}

func TestRailComposesUncachedSurfaces(t *testing.T) {
	styles := theme.New(true)
	if Rail(styles, 1, theme.Zebra, false) == Rail(styles, 1, theme.Card, false) {
		t.Error("the alternating tier must render a different rail background")
	}
}

func TestRailReservesDangerForHighPriority(t *testing.T) {
	styles := theme.New(true)
	seen := map[string]bool{}
	for _, priority := range []int{1, 2, 3} {
		seen[Rail(styles, priority, theme.Card, false)] = true
	}
	if len(seen) != 2 {
		t.Errorf("priority rails produced %d treatments, want danger and neutral", len(seen))
	}
	// Issue #232 collapsed the scale to three values at the render seam. The
	// stored 4 the migration has not reached yet reads as the low rail, which is
	// what it always meant, and every value off the scale does the same.
	for _, legacy := range []int{4, 9, 0, -1} {
		if Rail(styles, legacy, theme.Card, false) != Rail(styles, 3, theme.Card, false) {
			t.Errorf("priority %d did not fall back to the low rail", legacy)
		}
	}
}
