package widget

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// OverlayOpts describes one elevated panel. Spec section 4: elevation is a
// shade step plus a shadow, never a frame, so the panel carries a solid Brand
// header band, an OverlaySurf body and an OverlayBand footer, and casts a
// Shadow band one cell down and right.
//
// Body rows arrive already rendered because every overlay in kb scrolls its own
// window over a taller logical body; the panel insets them by OverlayInsetX and
// carries the panel tier behind them.
type OverlayOpts struct {
	Title  string
	Seq    string
	Body   []string
	Footer string
	X, Y   int
	W, H   int
}

// Overlay composes an elevated panel over background at opts.X, opts.Y. The
// backdrop is dimmed by the caller, which renders it through styles.Dimmed
// (spec section 4 step 1); this function owns steps 2 through 6.
func Overlay(styles *theme.Styles, background string, opts OverlayOpts) string {
	rows := OverlayRows(styles, opts)
	if len(rows) == 0 {
		return background
	}
	panel := strings.Join(rows, "\n")
	shadow := styles.Overlay.Shadow
	layers := []*lipgloss.Layer{lipgloss.NewLayer(background)}
	if opts.W > 0 && opts.H > 0 {
		bottom := pad(shadow, opts.W)
		right := strings.TrimSuffix(strings.Repeat(pad(shadow, 1)+"\n", opts.H), "\n")
		layers = append(layers,
			lipgloss.NewLayer(bottom).X(opts.X+1).Y(opts.Y+opts.H).Z(1),
			lipgloss.NewLayer(right).X(opts.X+opts.W).Y(opts.Y+1).Z(1),
		)
	}
	layers = append(layers, lipgloss.NewLayer(panel).X(opts.X).Y(opts.Y).Z(2))
	// An overlay is an elevation, never a resize: the shadow of a panel that
	// touches the frame edge must not grow the cell grid (issue #131).
	return clipBlock(lipgloss.NewCompositor(layers...).Render(),
		lipgloss.Width(background), lipgloss.Height(background))
}

// clipBlock cuts a composed block back to the cell grid it was composed onto.
func clipBlock(content string, width, height int) string {
	if width <= 0 || height <= 0 {
		return content
	}
	lines := strings.Split(content, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for index := range lines {
		lines[index] = clip(lines[index], width)
	}
	return strings.Join(lines, "\n")
}

// OverlayRows renders the panel of spec section 4 as its rows, without casting
// the shadow: header band, body on the panel tier, footer band. A panel too
// short for its bands drops them from the bottom up, so a one-row overlay is
// still its header.
func OverlayRows(styles *theme.Styles, opts OverlayOpts) []string {
	if opts.W <= 0 || opts.H <= 0 {
		return nil
	}
	inset := min(styles.Metrics.OverlayInsetX, opts.W/2)
	inner := max(opts.W-2*inset, 0)
	surf := styles.Overlay.Surf

	rows := make([]string, 0, opts.H)
	rows = append(rows, overlayHeader(styles, opts, inset, inner))
	body := opts.H - 2
	if opts.H == 2 {
		body = 1
	}
	for index := 0; index < body && len(rows) < opts.H; index++ {
		line := ""
		if index < len(opts.Body) {
			line = opts.Body[index]
		}
		rows = append(rows, pad(surf, inset)+fill(surf, clip(line, inner), inner)+pad(surf, inset))
	}
	if len(rows) < opts.H {
		rows = append(rows, overlayFooter(styles, opts.Footer, inset, inner, opts.W))
	}
	return rows[:opts.H]
}

// overlayHeader is spec section 4 step 4: row 0, solid Brand, bold title inset
// OverlayInsetX, right-aligned sequence tag.
func overlayHeader(styles *theme.Styles, opts OverlayOpts, inset, inner int) string {
	band := styles.Overlay.HeaderBand
	sequence := ansi.StringWidth(opts.Seq)
	field := inner
	if sequence > 0 {
		field = max(inner-sequence-1, 0)
	}
	title := truncate(styles, opts.Title, field)
	row := pad(band, inset) + fill(band, band.Render(title), field)
	if sequence > 0 {
		row += pad(band, inner-field-sequence) + band.Render(opts.Seq)
	}
	return row + pad(band, inset)
}

// overlayFooter is spec section 4 step 6: the last row, OverlayBand, FgSubtle,
// action hints inset OverlayInsetX.
func overlayFooter(styles *theme.Styles, text string, inset, inner, width int) string {
	band := styles.Overlay.FooterBand
	if width <= 2*inset {
		return pad(band, width)
	}
	return pad(band, inset) + fill(band, band.Render(truncate(styles, text, inner)), inner) + pad(band, inset)
}

// Section renders an overlay section break (spec section 4 step 5): an
// OverlayBand row carrying a bold FgSubtle label, not a rule.
//
// The spec's OverlayOpts sketch carries the sections inside the panel. They are
// rendered by the caller here because kb's overlays scroll a window over their
// body, so a section break can land anywhere in that window or be scrolled off.
func Section(styles *theme.Styles, label string, width int) string {
	if width <= 0 {
		return ""
	}
	band := styles.Overlay.SectionBand
	return fill(band, band.Render(truncate(styles, label, width)), width)
}

// Field renders one key/value row of spec section 4: the label in FgMuted, a
// fixed OverlayLabelW gutter, the value in FgBase.
func Field(styles *theme.Styles, label, value string, width int) string {
	if width <= 0 {
		return ""
	}
	gutter := min(styles.Metrics.OverlayLabelW, width)
	name := styles.Overlay.FieldLabel
	return fill(name, name.Render(truncate(styles, label, gutter)), gutter) +
		styles.Overlay.FieldValue.Render(truncate(styles, value, width-gutter))
}

// Fill renders a width by height block of one palette slot, the ground an
// overlay is placed on when there is no board behind it.
func Fill(styles *theme.Styles, slot theme.Slot, width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	row := pad(styles.On(theme.FgBase, slot), width)
	rows := make([]string, height)
	for index := range rows {
		rows[index] = row
	}
	return strings.Join(rows, "\n")
}
