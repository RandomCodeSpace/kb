package widget

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

func TestOverlayIsEmptyWithoutAPanel(t *testing.T) {
	styles := theme.New(true)
	if got := Overlay(styles, OverlayOpts{Width: 0, Height: 8}); got != "" {
		t.Errorf("zero-width overlay rendered %q", got)
	}
	if got := Overlay(styles, OverlayOpts{Width: 40, Height: 0}); got != "" {
		t.Errorf("zero-height overlay rendered %q", got)
	}
	if got := OverlayLayers(styles, OverlayOpts{Width: 0, Height: 0}, 1, 1); got != nil {
		t.Errorf("empty overlay produced %d layers", len(got))
	}
	if got := OverlayRow(styles, "x", 0); got != "" {
		t.Errorf("zero-width row rendered %q", got)
	}
	if got := Section(styles, "DETAIL", "", 0); got != "" {
		t.Errorf("zero-width section rendered %q", got)
	}
}

func TestOverlayStacksHeaderBodyAndFooterBands(t *testing.T) {
	styles := theme.New(true)
	panel := Overlay(styles, OverlayOpts{
		Title:  "Map it",
		Seq:    "#7",
		Body:   []string{OverlayRow(styles, "body", 30)},
		Footer: "[Close]",
		Hint:   "1/4",
		Width:  30,
		Height: 5,
	})
	rows := strings.Split(panel, "\n")
	if len(rows) != 5 {
		t.Fatalf("panel rendered %d rows, want 5", len(rows))
	}
	if plain := ansi.Strip(rows[0]); !strings.HasPrefix(plain, "  Map it") || !strings.HasSuffix(plain, "#7") {
		t.Errorf("header band = %q, want the title inset and the reference right-aligned", plain)
	}
	if plain := ansi.Strip(rows[1]); !strings.HasPrefix(plain, "  body") {
		t.Errorf("body row = %q, want the content inset", plain)
	}
	if plain := ansi.Strip(rows[4]); !strings.HasPrefix(plain, "  [Close]") || !strings.HasSuffix(plain, "1/4") {
		t.Errorf("footer band = %q, want the hints inset and the scroll hint right-aligned", plain)
	}
	for index, row := range rows {
		if got := ansi.StringWidth(row); got != 30 {
			t.Errorf("row %d is %d cells, want 30", index, got)
		}
	}
}

func TestOverlayFillsUnusedBodyRowsWithTheSurface(t *testing.T) {
	styles := theme.New(true)
	rows := strings.Split(Overlay(styles, OverlayOpts{
		Title:  "T",
		Body:   []string{OverlayRow(styles, "one", 20)},
		Width:  20,
		Height: 6,
	}), "\n")
	for index := 2; index < 5; index++ {
		if plain := ansi.Strip(rows[index]); strings.TrimSpace(plain) != "" || ansi.StringWidth(rows[index]) != 20 {
			t.Errorf("row %d = %q, want a blank surface row", index, plain)
		}
	}
}

func TestOverlayClipsBodyRowsAndPadsShortOnes(t *testing.T) {
	styles := theme.New(true)
	rows := strings.Split(Overlay(styles, OverlayOpts{
		Body:   []string{strings.Repeat("x", 40), "short"},
		Width:  16,
		Height: 4,
	}), "\n")
	for index := 1; index <= 2; index++ {
		if got := ansi.StringWidth(rows[index]); got != 16 {
			t.Errorf("body row %d is %d cells, want 16", index, got)
		}
	}
}

func TestOverlayKeepsTheSingleRowHeader(t *testing.T) {
	styles := theme.New(true)
	rows := strings.Split(Overlay(styles, OverlayOpts{Title: "T", Width: 10, Height: 1}), "\n")
	if len(rows) != 1 {
		t.Fatalf("one-row panel rendered %d rows", len(rows))
	}
	if !strings.Contains(ansi.Strip(rows[0]), "T") {
		t.Errorf("one-row panel = %q, want the header band", ansi.Strip(rows[0]))
	}
}

func TestOverlayLayersCastTheShadowDownAndRight(t *testing.T) {
	styles := theme.New(true)
	layers := OverlayLayers(styles, OverlayOpts{Title: "T", Width: 20, Height: 6}, 3, 2)
	if len(layers) != 3 {
		t.Fatalf("overlay produced %d layers, want 3", len(layers))
	}
	bottom, right, panel := layers[0], layers[1], layers[2]
	if bottom.GetX() != 4 || bottom.GetY() != 8 || bottom.Width() != 20 || bottom.Height() != 1 {
		t.Errorf("bottom shadow = %d,%d %dx%d", bottom.GetX(), bottom.GetY(), bottom.Width(), bottom.Height())
	}
	if right.GetX() != 23 || right.GetY() != 3 || right.Width() != 1 || right.Height() != 6 {
		t.Errorf("right shadow = %d,%d %dx%d", right.GetX(), right.GetY(), right.Width(), right.Height())
	}
	if panel.GetX() != 3 || panel.GetY() != 2 || panel.GetZ() <= bottom.GetZ() {
		t.Errorf("panel layer = %d,%d z%d, want it above the shadow", panel.GetX(), panel.GetY(), panel.GetZ())
	}
}

func TestBandTailWinsWhenTheLabelCannotFit(t *testing.T) {
	styles := theme.New(true)
	plain := ansi.Strip(Section(styles, strings.Repeat("SECTION", 5), "12", 16))
	if !strings.HasSuffix(plain, "12") {
		t.Errorf("section band = %q, want the count kept", plain)
	}
	if got := ansi.StringWidth(plain); got != 16 {
		t.Errorf("section band is %d cells, want 16", got)
	}
	narrow := ansi.Strip(Section(styles, "DETAIL", "12", 3))
	if strings.Contains(narrow, "12") {
		t.Errorf("section band = %q, want the count dropped when it cannot fit", narrow)
	}
}

func TestFieldRowUsesTheLabelGutter(t *testing.T) {
	styles := theme.New(true)
	plain := ansi.Strip(Field(styles, "status", "doing", 40))
	if !strings.HasPrefix(plain, "  status      doing") {
		t.Errorf("field row = %q, want a twelve-column label gutter", plain)
	}
	if got := ansi.StringWidth(plain); got != 40 {
		t.Errorf("field row is %d cells, want 40", got)
	}
	long := ansi.Strip(Field(styles, "blocked by more", strings.Repeat("value ", 20), 30))
	if got := ansi.StringWidth(long); got != 30 {
		t.Errorf("truncated field row is %d cells, want 30", got)
	}
	if got := ansi.StringWidth(ansi.Strip(Field(styles, "status", "doing", 8))); got != 8 {
		t.Errorf("narrow field row is %d cells, want 8", got)
	}
}

func TestCheckMarksEveryChecklistState(t *testing.T) {
	styles := theme.New(true)
	for state, want := range map[CheckState]string{
		CheckOpen:    styles.Glyph.Check,
		CheckDone:    styles.Glyph.CheckOn,
		CheckDropped: styles.Glyph.CheckOff,
	} {
		got := ansi.Strip(Check(styles, "ship it", state, theme.OverlaySurf, false))
		if !strings.HasPrefix(got, want) || !strings.HasSuffix(got, " ship it") {
			t.Errorf("state %d rendered %q, want the %q mark", state, got, want)
		}
	}
	if focused := Check(styles, "ship it", CheckOpen, theme.OverlaySurf, true); focused == Check(styles, "ship it", CheckOpen, theme.OverlaySurf, false) {
		t.Error("a focused checklist row renders the same as a resting one")
	}
}
