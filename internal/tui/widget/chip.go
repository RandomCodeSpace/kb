package widget

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// ChipOpts describes one pill. Spec section 3.6: the pill is the language's
// chip primitive, a flat span of colored cells with one padding cell at each
// end, so it owes a terminal font nothing but the ability to paint a cell.
type ChipOpts struct {
	Text    string     // the pill body
	Key     string     // non-empty selects the scoped two-tone form
	Mark    string     // optional state mark drawn at the head of the body
	Fill    theme.Slot // the pill fill
	On      theme.Slot // the surface the pill is drawn onto
	Flat    bool       // the compact degradation: flat colored bold text
	Hovered bool       // the pointer rests on this pill
	Dim     bool       // the inactive form: the fill withdrawn, the hue kept on the text
	Focused bool       // the keyboard cursor rests on this pill
}

// chipPad is the pill's end padding: one cell of the pill's own ground, spent
// where spec section 3.6 spent a half-block end cap until issue #227. The cap
// was the one piece of the pill language that depended on font geometry rather
// than on color cells, and it failed both ways - a seam on a filled pill wherever
// the half block was not drawn flush to the cell edge, and a colored bar on an
// inactive pill, which has no fill for it to fuse into.
const chipPad = " "

// Chip renders one pill. Cost is width(Mark)+width(Key)+width(Text)+2 columns
// for the padded form and width(Mark)+width(Text) for the flat form, in every
// state (spec section 10.4.4): the padding is one cell per end whatever the
// state, spec section 10.5.1 spends an underline on the body run for hover, and
// keyboard focus bolds that same run. Both cues are attributes, so no state in
// the widget moves a column.
func Chip(styles *theme.Styles, opts ChipOpts) string {
	if opts.Text == "" {
		return ""
	}
	runs := styles.ChipRuns(opts.Fill, opts.On)
	if opts.Dim {
		runs = styles.ChipRunsTint(opts.Fill, opts.On)
	}
	body, flat := chipBody(runs, opts)
	if opts.Flat {
		return flat.Render(opts.Mark + opts.Text)
	}
	if opts.Key == "" {
		return runs.Pad.Render(chipPad) +
			body.Render(opts.Mark+opts.Text) +
			runs.Pad.Render(chipPad)
	}
	// The scoped variant substitutes FgSubtle on Surface for the key run (spec
	// sections 1.6 and 3.6). With the caps gone the two tones meet as a hard
	// color boundary between two padded spans: the Surface span is the leading
	// pad plus the key, the fill span is the body plus the trailing pad. The mark
	// rides the key run - it belongs to the dark half, the half that is FgSubtle
	// on Surface in both the filled and the tinted form, so the mark's own
	// contrast never moves.
	return runs.ScopedPad.Render(chipPad) +
		runs.ScopedKey.Render(opts.Mark+opts.Key) +
		body.Render(opts.Text) +
		runs.Pad.Render(chipPad)
}

// chipBody picks the body and flat runs for the pill's pointer and keyboard
// state. Hover underlines and focus bolds, both on the body run only and both
// costing zero cells (spec sections 10.4.4 and 10.5.1); they are orthogonal
// axes, so the filter bar can carry the pair at once. The flat form has no
// focus state - it is the compact degradation of section 2.6, which is already
// bold and is never traversed - so focus leaves it alone.
func chipBody(runs theme.ChipStyles, opts ChipOpts) (body, flat lipgloss.Style) {
	switch {
	case opts.Focused && opts.Hovered:
		return runs.BodyFocusHover, runs.FlatHover
	case opts.Focused:
		return runs.BodyFocus, runs.Flat
	case opts.Hovered:
		return runs.BodyHover, runs.FlatHover
	default:
		return runs.Body, runs.Flat
	}
}

// labelParts splits one tag into the pill runs of spec section 3.5: a scoped
// key::value tag becomes a two-tone pill, a plain tag a single-tone #tag pill,
// and the compact form drops the padding, keeping only the value when scoped and
// the hash prefix when plain. It is the one place the wheel hue and the scoped
// cut are decided, so the board pill and the filter pill cannot fork.
func labelParts(styles *theme.Styles, tag string, flat bool) (text, key string, fill theme.Slot) {
	fill = theme.LabelSlot(LabelWheel(tag))
	scopeKey, value, scoped := strings.Cut(tag, "::")
	if !scoped || scopeKey == "" || value == "" {
		return styles.Glyph.MarkTag + tag, "", fill
	}
	if flat {
		return value, "", fill
	}
	return value, scopeKey + ":", fill
}

// Label renders one label pill, wheel-hued by the hash of spec section 1.6.
// hovered underlines the body run per section 10.5.1 and costs no cell in
// either form.
func Label(styles *theme.Styles, tag string, on theme.Slot, flat, hovered bool) string {
	if tag == "" {
		return ""
	}
	text, key, fill := labelParts(styles, tag, flat)
	return Chip(styles, ChipOpts{
		Text: text, Key: key, Fill: fill, On: on, Flat: flat, Hovered: hovered,
	})
}

// FilterLabel renders one filter-bar label pill: the section 3.6 pill the board
// cards already carry, plus the two states the toolbar needs on top of it.
//
// selected fills the pill with its wheel hue; unselected withdraws the fill to
// the tinted form of ChipRunsTint, which keeps that same wheel hue on the body
// text, so the row reads as a set of offers with the active ones lit and every
// offer still matchable by eye to the label pills on the cards (issue #208).
// focused bolds the body run, the zero-cell cue that replaced the thickened end
// caps when issue #227 retired them. Neither changes a cell count,
// so toggling or traversing a label never reflows the toolbar (section 10.4.4),
// and the leading toggle mark keeps both distinctions legible at the flat
// fidelity floor, where neither hue nor tier survives.
func FilterLabel(styles *theme.Styles, tag string, on theme.Slot, selected, focused, hovered bool) string {
	if tag == "" {
		return ""
	}
	mark := styles.Glyph.MarkFilterOff
	if selected {
		mark = styles.Glyph.MarkFilterOn
	}
	text, key, fill := labelParts(styles, tag, false)
	return Chip(styles, ChipOpts{
		Text: text, Key: key, Mark: mark, Fill: fill, On: on,
		Dim: !selected, Focused: focused, Hovered: hovered,
	})
}

// LabelWheel is the label color hash of spec section 1.6, unchanged. It lives
// beside the palette now, because the per-board accent of section 10.7.2
// derives from the same wheel and the two must not fork.
func LabelWheel(tag string) int { return theme.WheelIndex(tag) }

// Priority renders the priority marker of spec section 3.4: bold, in the
// priority hue, never a pill, and only two cells, which is why it survives
// longest when the chip row runs out of width.
func Priority(styles *theme.Styles, priority int, on theme.Slot) string {
	return styles.OnBold(theme.PrioritySlot(priority), on).
		Render(styles.Glyph.MarkPrio + strconv.Itoa(priorityLabel(priority)))
}

// priorityLabel is the number the marker prints. Spec section 1.4 maps anything
// that is not 1, 2 or 4 onto P3, hue and label alike.
func priorityLabel(priority int) int {
	switch priority {
	case 1, 2, 4:
		return priority
	default:
		return 3
	}
}
