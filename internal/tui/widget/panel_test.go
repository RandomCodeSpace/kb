package widget

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

func TestPanelIsEmptyWithoutAFrame(t *testing.T) {
	styles := theme.New(true)
	if got := Panel(styles, PanelOpts{Width: 0, Height: 10}); got != nil {
		t.Errorf("zero-width panel rendered %v", got)
	}
	if got := Panel(styles, PanelOpts{Width: 10, Height: 0}); got != nil {
		t.Errorf("zero-height panel rendered %v", got)
	}
}

func TestPanelStacksBandMetaAndBody(t *testing.T) {
	styles := theme.New(true)
	rows := Panel(styles, PanelOpts{
		Header: BandOpts{Index: 1, Label: "TO DO", Count: 4, Hue: theme.HueTodo},
		Meta:   "4 cards · 1 blocked",
		Body:   []string{"card row"},
		Width:  30,
		Height: 6,
	})
	if len(rows) != 6 {
		t.Fatalf("panel rendered %d rows, want 6", len(rows))
	}
	if !strings.Contains(ansi.Strip(rows[0]), "TO DO") {
		t.Errorf("row 0 = %q, want the header band", ansi.Strip(rows[0]))
	}
	if plain := ansi.Strip(rows[1]); !strings.HasPrefix(plain, "  4 cards") {
		t.Errorf("row 1 = %q, want the meta line inset two columns", plain)
	}
	if !strings.Contains(ansi.Strip(rows[2]), "card row") {
		t.Errorf("row 2 = %q, want the body", ansi.Strip(rows[2]))
	}
	for index, row := range rows {
		if got := ansi.StringWidth(row); got != 30 {
			t.Errorf("row %d is %d cells, want 30", index, got)
		}
	}
}

func TestPanelCompactDropsTheMetaLine(t *testing.T) {
	styles := theme.New(true)
	rows := Panel(styles, PanelOpts{
		Header:  BandOpts{Index: 1, Label: "TO DO", Count: 4, Hue: theme.HueTodo},
		Meta:    "4 cards",
		Body:    []string{"card row"},
		Width:   30,
		Height:  4,
		Density: DensityCompact,
	})
	if strings.Contains(ansi.Strip(rows[1]), "cards") {
		t.Errorf("row 1 = %q, the meta line is dropped when compact", ansi.Strip(rows[1]))
	}
	if !strings.HasPrefix(ansi.Strip(rows[1]), "card row") {
		t.Errorf("row 1 = %q, compact drops the column padding too", ansi.Strip(rows[1]))
	}
}

func TestPanelWithoutAMetaLine(t *testing.T) {
	styles := theme.New(true)
	rows := Panel(styles, PanelOpts{
		Header: BandOpts{Index: 1, Label: "TO DO", Hue: theme.HueTodo},
		Body:   []string{"card row"},
		Width:  20,
		Height: 3,
	})
	if !strings.Contains(ansi.Strip(rows[1]), "card row") {
		t.Errorf("row 1 = %q, want the body directly under the band", ansi.Strip(rows[1]))
	}
}

func TestPanelCarriesTheOverflowCue(t *testing.T) {
	styles := theme.New(true)
	rows := Panel(styles, PanelOpts{
		Header: BandOpts{Index: 1, Label: "TO DO", Hue: theme.HueTodo},
		Body:   []string{"card row"},
		More:   7,
		Width:  24,
		Height: 4,
	})
	if plain := ansi.Strip(rows[2]); !strings.HasPrefix(plain, "  +7 more") {
		t.Errorf("overflow cue = %q, want it inset two columns", plain)
	}
}

func TestPanelTruncatesToItsHeight(t *testing.T) {
	styles := theme.New(true)
	rows := Panel(styles, PanelOpts{
		Header: BandOpts{Index: 1, Label: "TO DO", Hue: theme.HueTodo},
		Body:   []string{"one", "two", "three", "four"},
		Width:  20,
		Height: 3,
	})
	if len(rows) != 3 {
		t.Fatalf("panel rendered %d rows, want 3", len(rows))
	}
}

func TestPanelClipsBodyRowsWiderThanTheInset(t *testing.T) {
	styles := theme.New(true)
	rows := Panel(styles, PanelOpts{
		Header: BandOpts{Index: 1, Label: "TO DO", Hue: theme.HueTodo},
		Body:   []string{strings.Repeat("x", 40)},
		Width:  12,
		Height: 2,
	})
	if got := ansi.StringWidth(rows[1]); got != 12 {
		t.Errorf("body row is %d cells, want 12", got)
	}
}

func TestPanelMetaRowSurvivesANarrowColumn(t *testing.T) {
	styles := theme.New(true)
	for width := 1; width <= 4; width++ {
		rows := Panel(styles, PanelOpts{
			Header: BandOpts{Index: 1, Label: "TO DO", Hue: theme.HueTodo},
			Meta:   "4 cards",
			Width:  width,
			Height: 2,
		})
		if got := ansi.StringWidth(rows[1]); got != width {
			t.Errorf("meta row of width %d rendered %d cells", width, got)
		}
	}
}
