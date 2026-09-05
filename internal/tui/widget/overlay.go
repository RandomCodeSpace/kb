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

	// Armed re-fills the header band to StatusAlarm with FgBase bold. Spec
	// section 10.1.4, ratified call 6: this is the only header-band recolor in
	// the TUI, it fires on the Armed state of section 1.9 and on nothing else,
	// and it carries the mode structurally rather than recovering it from a
	// rendered label.
	Armed bool
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
// row carrying a bold label, never a rule. Count is rendered right-aligned when
// it is not empty, which is how a section says how much it holds without
// spending a body row on it.
//
// The label carries the resting ramp of spec section 10.1.2. The donor
// gradient-paints a diagonal rule trailing its dialog title; section 4 forbids
// that shape, so the ramp moves onto the label and the band keeps one element.
func Section(styles *theme.Styles, label, count string, width int) string {
	return SectionRamp(styles, label, count, width, theme.GradSection)
}

// SectionRamp is Section with the state-dependent ramp of spec section 10.1.4
// named by the caller: GradSectionDanger while a destructive action is pending,
// GradSectionArmed once it is armed. The lead is the same tint in both, so
// arming deepens the tail rather than re-tinting the label - an escalation of a
// state the user is already in, not a new one.
//
// The mode is a property of the overlay and is passed in structurally; it is
// never recovered by matching a rendered label.
func SectionRamp(styles *theme.Styles, label, count string, width int, ramp theme.Ramp) string {
	return bandRow(styles, theme.BandSection, styles.Overlay.SectionBand, label, count, width,
		func(head string) string { return styles.GradBold(ramp, head) })
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
	text := styles.Overlay.FieldValue.Render(truncate(styles, value, max(field-gutter, 0)))
	return OverlayRow(styles, fieldLabel(styles, label, gutter, field)+text, width)
}

// FieldRun is Field for a value that arrives already styled: the blocker chip
// row of issue #222, whose chips carry their own hover and pressed runs and so
// cannot be handed to FieldValue as text. Layout is Field's, cell for cell, so
// a row that gains an activatable chip moves nothing (spec section 10.4.4).
//
// It reports where the value field starts, as a cell column from the row's own
// left edge, and how many cells that field holds. Those are the recorded bounds
// spec section 10.5.3 asks a widget-rendered control to be anchored at: the
// caller placed the runs inside the value, so it knows their offsets, and never
// has to recover them by scanning the rendered row.
func FieldRun(styles *theme.Styles, label, value string, width int) (row string, column, cells int) {
	gutter := styles.Metrics.OverlayLabelW
	inset := styles.Metrics.OverlayInsetX
	field := max(width-2*inset, 0)
	cells = max(field-gutter, 0)
	row = OverlayRow(styles, fieldLabel(styles, label, gutter, field)+truncate(styles, value, cells), width)
	return row, inset + min(gutter, field), cells
}

// fieldLabel is the key half of a field row: the label in FgMuted, truncated a
// column short of the gutter so a label and its value never touch, padded out
// to hold the value column straight down the panel.
func fieldLabel(styles *theme.Styles, label string, gutter, field int) string {
	return styles.Overlay.FieldLabel.Render(exact(truncate(styles, label, max(gutter-1, 0)), min(gutter, field)))
}

// FieldWrap is Field for a value that arrives as already-styled runs rather
// than as text: the label pill row of spec sections 3.5 and 3.6, whose runs
// carry their own fills and cannot be handed to FieldValue.
//
// The runs are laid left to right one column apart and wrap whole to the next
// row, which keeps the caps of section 3.6 paired: a pill is the smallest unit
// the row can break on, and one split across two rows would read as two pills
// with one cap each. A run wider than the value field is truncated onto a row of
// its own rather than dropped, because a field row is the card's only statement
// of that label. Continuation rows repeat the gutter and leave the label column
// blank, so the value column is one straight edge.
func FieldWrap(styles *theme.Styles, label string, runs []string, width int) []string {
	gutter := styles.Metrics.OverlayLabelW
	inset := styles.Metrics.OverlayInsetX
	field := max(width-2*inset, 0)
	value := max(field-gutter, 0)
	surface := styles.On(theme.FgBase, theme.OverlaySurf)
	head := styles.Overlay.FieldLabel.Render(exact(truncate(styles, label, max(gutter-1, 0)), min(gutter, field)))
	blank := pad(surface, min(gutter, field))
	var rows []string
	for _, line := range wrapRuns(surface, runs, value) {
		lead := blank
		if len(rows) == 0 {
			lead = head
		}
		rows = append(rows, OverlayRow(styles, lead+line, width))
	}
	return rows
}

// wrapRuns packs already-styled runs into rows of at most width cells, one
// column of surface between neighbours. It is the wrapping counterpart of
// joinAt, which skips what does not fit because a card's chip row is exactly one
// row; a field row may grow downward instead.
func wrapRuns(style lipgloss.Style, runs []string, width int) []string {
	if width <= 0 {
		return nil
	}
	var rows []string
	line, used := "", 0
	gapBeforeNextGroup := false
	for _, run := range runs {
		if run == "" {
			continue
		}
		if gapBeforeNextGroup {
			rows = append(rows, "")
			gapBeforeNextGroup = false
		}
		cost := ansi.StringWidth(run)
		separator := 0
		if used > 0 {
			separator = 1
		}
		if used+separator+cost > width {
			if used > 0 {
				rows = append(rows, line, "")
				line, used, separator = "", 0, 0
			}
			if cost > width {
				for _, fragment := range strings.Split(ansi.Hardwrap(run, width, true), "\n") {
					if ansi.StringWidth(fragment) == 0 {
						continue
					}
					rows = append(rows, style.Render(ansi.Strip(fragment)))
				}
				gapBeforeNextGroup = true
				continue
			}
		}
		if separator == 1 {
			line += pad(style, 1)
		}
		line += run
		used += separator + cost
	}
	if used > 0 {
		rows = append(rows, line)
	}
	return rows
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
	header, headerStyle := theme.BandHeader, styles.Overlay.HeaderBand
	if opts.Armed {
		header, headerStyle = theme.BandHeaderArmed, styles.Overlay.HeaderBandArmed
	}
	rows = append(rows, bandRow(styles, header, headerStyle, opts.Title, opts.Seq, opts.Width, nil))
	body := max(opts.Height-2, 0)
	for index := 0; index < body; index++ {
		if index < len(opts.Body) {
			rows = append(rows, fill(styles.Overlay.Surf, clip(opts.Body[index], opts.Width), opts.Width))
			continue
		}
		rows = append(rows, pad(styles.Overlay.Surf, opts.Width))
	}
	if opts.Height > 1 {
		rows = append(rows, bandRow(styles, theme.BandFooter, styles.Overlay.FooterBand, opts.Footer, opts.Hint, opts.Width, nil))
	}
	return rows[:opts.Height]
}

// bandRow renders one full-width band: content inset OverlayInsetX from the
// left with the tail right-aligned at the band's own edge. Spec section 10.4.5
// fixes the order of sacrifice - the info is never truncated, the title is
// truncated per section 3.3, and the fill never drops below one cell - and the
// arithmetic below is that table.
//
// A band is one styled run over the whole row, so content that carries its own
// color - the branded spinner frame of spec section 10.2.5, the graded section
// label of section 10.1.2 - is passed through BandRun, which re-arms the band
// after every reset inside it. Plain content is returned untouched and renders
// the bytes it always did.
//
// paint is applied to the already-truncated head, never before: a gradient
// walks grapheme clusters and emits an SGR run per cluster, and truncating that
// by cell width would cut an escape sequence in half.
func bandRow(
	styles *theme.Styles,
	band theme.Band,
	style lipgloss.Style,
	content, tail string,
	width int,
	paint func(string) string,
) string {
	if width <= 0 {
		return ""
	}
	inset := min(styles.Metrics.OverlayInsetX, width)
	field := max(width-inset, 0)
	// Row three of the table: below the info's own width plus two there is no
	// room for a title and a fill beside it, so the info is dropped outright and
	// the title takes the whole field. Dropping the title instead would leave a
	// band that says only how much a section holds and never which one it is.
	if ansi.StringWidth(tail)+2 > field {
		tail = ""
	}
	separator := 0
	if tail != "" {
		separator = 1
	}
	head := truncate(styles, content, max(field-ansi.StringWidth(tail)-separator, 0))
	gap := max(field-ansi.StringWidth(head)-ansi.StringWidth(tail), 0)
	if paint != nil && head != "" {
		head = paint(head)
	}
	row := spaces(inset) + styles.BandRun(band, head) + spaces(gap) + tail
	return style.Render(exact(row, width))
}
