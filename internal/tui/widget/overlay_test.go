package widget

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

func TestOverlayRowsAreEmptyWithoutAPanel(t *testing.T) {
	styles := theme.New(true)
	if got := OverlayRows(styles, OverlayOpts{W: 0, H: 5}); got != nil {
		t.Errorf("zero-width overlay rendered %v", got)
	}
	if got := OverlayRows(styles, OverlayOpts{W: 20, H: 0}); got != nil {
		t.Errorf("zero-height overlay rendered %v", got)
	}
}

func TestOverlayRowsBandTheBodyAndTheFooter(t *testing.T) {
	styles := theme.New(true)
	rows := OverlayRows(styles, OverlayOpts{
		Title:  "EDIT CARD",
		Seq:    "#90",
		Body:   []string{"first", "second"},
		Footer: "esc close",
		W:      24,
		H:      5,
	})
	if len(rows) != 5 {
		t.Fatalf("overlay rendered %d rows, want 5", len(rows))
	}
	header := ansi.Strip(rows[0])
	if !strings.HasPrefix(header, "  EDIT CARD") || !strings.HasSuffix(header, "#90  ") {
		t.Errorf("header band = %q", header)
	}
	if plain := ansi.Strip(rows[1]); plain != "  first"+strings.Repeat(" ", 24-7) {
		t.Errorf("body row = %q", plain)
	}
	if plain := ansi.Strip(rows[3]); strings.TrimSpace(plain) != "" {
		t.Errorf("unused body row = %q, want blank", plain)
	}
	if plain := ansi.Strip(rows[4]); !strings.HasPrefix(plain, "  esc close") {
		t.Errorf("footer band = %q", plain)
	}
	for index, row := range rows {
		if width := ansi.StringWidth(row); width != 24 {
			t.Errorf("row %d width = %d, want 24", index, width)
		}
	}
}

func TestOverlayRowsDropTheBandsTheyCannotFit(t *testing.T) {
	styles := theme.New(true)
	one := OverlayRows(styles, OverlayOpts{Title: "T", Body: []string{"body"}, Footer: "f", W: 10, H: 1})
	if len(one) != 1 || !strings.Contains(ansi.Strip(one[0]), "T") {
		t.Fatalf("one-row overlay = %q, want its header", one)
	}
	two := OverlayRows(styles, OverlayOpts{Title: "T", Body: []string{"body"}, Footer: "f", W: 10, H: 2})
	if len(two) != 2 || !strings.Contains(ansi.Strip(two[1]), "body") {
		t.Fatalf("two-row overlay = %q, want header and body", two)
	}
	narrow := OverlayRows(styles, OverlayOpts{Title: "T", Footer: "f", W: 3, H: 3})
	for index, row := range narrow {
		if width := ansi.StringWidth(row); width != 3 {
			t.Errorf("narrow row %d width = %d, want 3", index, width)
		}
	}
	if got := ansi.StringWidth(overlayFooter(styles, "f", 2, 0, 4)); got != 4 {
		t.Errorf("footer with no field = %d cells, want 4", got)
	}
}

func TestOverlayCastsAShadowInsideTheFrame(t *testing.T) {
	styles := theme.New(true)
	background := Fill(styles, theme.Canvas, 20, 8)
	composed := Overlay(styles, background, OverlayOpts{
		Title: "TITLE", Body: []string{"body"}, Footer: "hint",
		X: 2, Y: 1, W: 12, H: 4,
	})
	lines := strings.Split(composed, "\n")
	if len(lines) != 8 {
		t.Fatalf("composed %d rows, want the background's 8", len(lines))
	}
	for index, line := range lines {
		if width := ansi.StringWidth(line); width != 20 {
			t.Errorf("composed row %d width = %d, want 20", index, width)
		}
	}
	if !strings.Contains(ansi.Strip(lines[1]), "TITLE") {
		t.Errorf("panel row = %q, want the header band at y=1", ansi.Strip(lines[1]))
	}
	if lines[5] == lines[7] {
		t.Errorf("row below the panel carries no shadow band: %q", lines[5])
	}
	if strings.TrimSpace(ansi.Strip(lines[5])) != "" {
		t.Errorf("shadow row is not blank: %q", ansi.Strip(lines[5]))
	}
}

func TestOverlayKeepsTheFrameWhenThePanelTouchesTheEdge(t *testing.T) {
	styles := theme.New(true)
	background := Fill(styles, theme.Canvas, 6, 3)
	composed := Overlay(styles, background, OverlayOpts{Title: "T", W: 6, H: 3})
	lines := strings.Split(composed, "\n")
	if len(lines) != 3 {
		t.Fatalf("edge-touching overlay grew to %d rows", len(lines))
	}
	for index, line := range lines {
		if width := ansi.StringWidth(line); width != 6 {
			t.Errorf("row %d width = %d, want 6", index, width)
		}
	}
	if got := Overlay(styles, background, OverlayOpts{W: 0, H: 0}); got != background {
		t.Error("an overlay with no panel replaced the background")
	}
	if got := clipBlock("abc", 0, 0); got != "abc" {
		t.Errorf("clip to no frame = %q", got)
	}
}

func TestSectionAndFieldRenderTheOverlayRows(t *testing.T) {
	styles := theme.New(true)
	if got := Section(styles, "", 0); got != "" {
		t.Errorf("zero-width section = %q", got)
	}
	section := Section(styles, "COMMENTS", 20)
	if plain := ansi.Strip(section); plain != "COMMENTS"+strings.Repeat(" ", 12) {
		t.Errorf("section = %q", plain)
	}
	if got := Field(styles, "label", "value", 0); got != "" {
		t.Errorf("zero-width field = %q", got)
	}
	field := ansi.Strip(Field(styles, "Priority", "P1", 20))
	if !strings.HasPrefix(field, "Priority") || !strings.HasSuffix(field, "P1") {
		t.Errorf("field row = %q", field)
	}
	if ansi.StringWidth(field) != 14 {
		t.Errorf("field row width = %d, want the gutter plus the value", ansi.StringWidth(field))
	}
	if got := Field(styles, "verylonglabelthatoverflows", "v", 8); ansi.StringWidth(got) != 8 {
		t.Errorf("truncated field = %q", ansi.Strip(got))
	}
}

func TestFillPaintsASlot(t *testing.T) {
	styles := theme.New(true)
	if got := Fill(styles, theme.Canvas, 0, 4); got != "" {
		t.Errorf("zero-width fill = %q", got)
	}
	if got := Fill(styles, theme.Canvas, 4, 0); got != "" {
		t.Errorf("zero-height fill = %q", got)
	}
	rows := strings.Split(Fill(styles, theme.Canvas, 4, 2), "\n")
	if len(rows) != 2 || ansi.StringWidth(rows[0]) != 4 {
		t.Errorf("fill = %q", rows)
	}
}
