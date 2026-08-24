package widget

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// OverlayOpts describes one elevated panel. Spec section 4: elevation is a
// shade step plus a shadow, never a frame, so the panel is a header band, an
// OverlaySurf body and a footer band, with the shadow cast behind it.
//
// Body rows arrive already rendered at the panel width because the caller owns
// the scroll window: an overlay slices its own body lines, and a section break
// or a field row is just one more row in that slice.
type OverlayOpts struct {
	Title  string   // header band title, inset OverlayInsetX
	Seq    string   // header band reference, right-aligned
	Body   []string // panel-width body rows
	Footer string   // footer band hints, inset OverlayInsetX
	Hint   string   // footer band scroll indicator, right-aligned
	Width  int
	Height int
}

// Overlay renders the panel of spec section 4 steps 3 to 6: the OverlaySurf
// fill, the solid Brand header band, the body rows and the OverlayBand footer.
// Rows the body does not fill carry the panel surface to the edge.
func Overlay(styles *theme.Styles, opts OverlayOpts) string {
	rows := overlayRows(styles, opts)
	if len(rows) == 0 {
		return ""
	}
	return strings.Join(rows, "\n")
}

// OverlayLayers returns the panel and its drop shadow as compositor layers
// anchored at x, y. Spec section 4 step 2: two Shadow-filled bands offset one
// cell down and right of the panel. The shadow is folded in here and is never
// separately callable, so no caller can elevate a panel without it.
func OverlayLayers(styles *theme.Styles, opts OverlayOpts, x, y int) []*lipgloss.Layer {
	panel := Overlay(styles, opts)
	if panel == "" {
		return nil
	}
	shadow := styles.Overlay.Shadow
	right := make([]string, opts.Height)
	for row := range right {
		right[row] = pad(shadow, 1)
	}
	return []*lipgloss.Layer{
		lipgloss.NewLayer(pad(shadow, opts.Width)).X(x + 1).Y(y + opts.Height).Z(1),
		lipgloss.NewLayer(strings.Join(right, "\n")).X(x + opts.Width).Y(y + 1).Z(1),
		lipgloss.NewLayer(panel).X(x).Y(y).Z(2),
	}
}

// Section renders one section break of spec section 4 step 5: an OverlayBand
// row carrying a bold FgSubtle label, never a rule. Count is rendered
// right-aligned when it is not empty, which is how a section says how much it
// holds without spending a body row on it.
func Section(styles *theme.Styles, label, count string, width int) string {
	return bandRow(styles, theme.BandSection, styles.Overlay.SectionBand, label, count, width)
}

// OverlayRow renders one body row: already-styled content inset OverlayInsetX,
// padded to the panel width so the row carries the panel surface edge to edge.
func OverlayRow(styles *theme.Styles, content string, width int) string {
	return OverlayRowOn(styles, content, width, theme.OverlaySurf)
}

// OverlayRowOn is OverlayRow on a named surface, which is how a choice row
// wears its hovered fill. Spec section 10.5.1: hover raises the whole row, and
// the raise is panel edge to panel edge, so the row's own padding has to carry
// the same slot as its content or the raise would stop at the text.
//
// Callers reach the slot through Styles.RowSurface rather than naming a tier
// here; a row that is not activatable is not hoverable and keeps OverlayRow.
func OverlayRowOn(styles *theme.Styles, content string, width int, on theme.Slot) string {
	if width <= 0 {
		return ""
	}
	surface := styles.On(theme.FgBase, on)
	inset := min(styles.Metrics.OverlayInsetX, width)
	field := max(width-2*inset, 0)
	body := clip(content, field)
	return pad(surface, inset) +
		body +
		pad(surface, field-ansi.StringWidth(body)) +
		pad(surface, width-inset-field)
}

// Field renders one key/value row of spec section 4: the label at inset
// OverlayInsetX in FgMuted, a fixed OverlayLabelW gutter, and the value in
// FgBase at inset plus the gutter.
func Field(styles *theme.Styles, label, value string, width int) string {
	gutter := styles.Metrics.OverlayLabelW
	inset := styles.Metrics.OverlayInsetX
	field := max(width-2*inset, 0)
	name := styles.Overlay.FieldLabel.Render(exact(truncate(styles, label, max(gutter-1, 0)), min(gutter, field)))
	text := styles.Overlay.FieldValue.Render(truncate(styles, value, max(field-gutter, 0)))
	return OverlayRow(styles, name+text, width)
}

// CheckState is the state of one checklist row. Spec section 5.1 names three
// marks; a source that only knows done and not-done uses the first two.
type CheckState uint8

// The three checklist marks of spec section 5.1.
const (
	CheckOpen CheckState = iota
	CheckDone
	CheckDropped
)

// Check renders one checklist row: the state mark and its label, hued by state
// so a finished item reads as finished without a second column.
func Check(styles *theme.Styles, label string, state CheckState, on theme.Slot, focused bool) string {
	glyph, hue := styles.Glyph.Check, theme.FgBase
	switch state {
	case CheckDone:
		glyph, hue = styles.Glyph.CheckOn, theme.StatusOK
	case CheckDropped:
		glyph, hue = styles.Glyph.CheckOff, theme.FgMuted
	case CheckOpen:
	}
	mark := styles.On(hue, on)
	text := styles.On(theme.FgBase, on)
	if focused {
		text = styles.OnBold(theme.FgBase, on)
	}
	return mark.Render(glyph) + text.Render(" "+label)
}

// overlayRows renders the panel row by row so the caller can join or layer it.
func overlayRows(styles *theme.Styles, opts OverlayOpts) []string {
	if opts.Width <= 0 || opts.Height <= 0 {
		return nil
	}
	rows := make([]string, 0, opts.Height)
	rows = append(rows, bandRow(styles, theme.BandHeader, styles.Overlay.HeaderBand, opts.Title, opts.Seq, opts.Width))
	body := max(opts.Height-2, 0)
	for index := 0; index < body; index++ {
		if index < len(opts.Body) {
			rows = append(rows, fill(styles.Overlay.Surf, clip(opts.Body[index], opts.Width), opts.Width))
			continue
		}
		rows = append(rows, pad(styles.Overlay.Surf, opts.Width))
	}
	if opts.Height > 1 {
		rows = append(rows, bandRow(styles, theme.BandFooter, styles.Overlay.FooterBand, opts.Footer, opts.Hint, opts.Width))
	}
	return rows[:opts.Height]
}

// bandRow renders one full-width band: content inset OverlayInsetX from the
// left with the tail right-aligned at the band's own edge. The tail wins when
// the two cannot both fit, because it is the count or the scroll position and
// it must not be overwritten.
//
// A band is one styled run over the whole row, so content that carries its own
// color - the branded spinner frame of spec section 10.2.5 - is passed through
// BandRun, which re-arms the band after every reset inside it. Plain content is
// returned untouched and renders the bytes it always did.
func bandRow(styles *theme.Styles, band theme.Band, style lipgloss.Style, content, tail string, width int) string {
	if width <= 0 {
		return ""
	}
	inset := min(styles.Metrics.OverlayInsetX, width)
	field := max(width-inset, 0)
	if ansi.StringWidth(tail) > field {
		tail = ""
	}
	separator := 0
	if tail != "" {
		separator = 1
	}
	head := truncate(styles, content, max(field-ansi.StringWidth(tail)-separator, 0))
	gap := max(field-ansi.StringWidth(head)-ansi.StringWidth(tail), 0)
	row := spaces(inset) + styles.BandRun(band, head) + spaces(gap) + tail
	return style.Render(exact(row, width))
}
