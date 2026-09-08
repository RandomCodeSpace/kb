package widget

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/RandomCodeSpace/kb/internal/board"
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
	Neutral bool       // ordinary label treatment: neutral depth and text, no category hue
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
	if opts.Neutral {
		runs = styles.NeutralChipRuns(opts.On)
	} else if opts.Dim {
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
// key::value tag becomes a scope and value pair, a plain tag a #tag pill,
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
	return value, scopeKey + " ", fill
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
// Both states keep the label's muted wheel fill, so the filter stays matchable
// by eye to the same label on a card. The leading mark distinguishes selected
// from unselected without changing the tag's color identity.
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
		Focused: focused, Hovered: hovered,
	})
}

// Effort renders the effort marker of spec section 3.4. Issue #232 replaced the
// colored square the marker wore between #223 and #230 with the effort letter
// on a colored fill: a one-letter pill in the section 3.6 anatomy, pad, letter,
// pad, three cells at normal density.
//
// The pill form is the anatomy verbatim rather than a tighter one because the
// tighter one already means something. A colored letter with no padding is the
// flat chip of section 2.6 step 7, the compact degradation of a pill, and
// spending it at normal density would say the row had run out of width when it
// had not. The compact row does spend it, and the marker is one cell there.
//
// ASCII on background color is the whole of it: the letter carries the value,
// the hue reinforces it, and neither depends on a terminal font drawing a
// pictograph. That is the trade #232 recorded - the squares carried one of
// three values and lost which one whenever a font substituted or clipped them.
//
// A value the S/M/L scale does not name keeps the Diamond fallback the marker
// wore before the squares: the mark, the column section 10.4.1's adjacency rule
// gives it, and the value as the board spells it.
func Effort(styles *theme.Styles, value string, on theme.Slot, flat bool) string {
	if value == "" {
		return ""
	}
	fill, onScale := theme.EffortSlot(value)
	if !onScale {
		mark := styles.Glyph.Diamond
		return MarkRun(styles, mark, mark+" "+value, styles.On(theme.FgSubtle, on), on)
	}
	return Chip(styles, ChipOpts{Text: value, Fill: fill, On: on, Flat: flat})
}

// LabelWheel is the label color hash of spec section 1.6, unchanged. It lives
// beside the palette now, because the per-board accent of section 10.7.2
// derives from the same wheel and the two must not fork.
func LabelWheel(tag string) int { return theme.WheelIndex(tag) }

// Priority renders the priority marker of spec section 3.4. Issue #232 replaced
// the "P1" text treatment with the digit alone on a fill of the priority hue:
// the one-character pill of section 3.6, the same anatomy the effort marker now
// wears, so the meta row reads as one grammar rather than as four unrelated
// treatments that happen to share a line.
//
// The digit carries the fact and the hue reinforces it, which is section 1.9's
// floor: the four priority hues are all readable against FgOnAccent, and a
// terminal that renders no color at all still shows a numeral. The "P" the
// marker used to carry said nothing the column and the digit did not - the rail
// beside the card is already the priority hue - and it cost a cell on the row
// section 3.4 spends the most effort keeping short.
//
// Cost is three cells padded and one flat, against the two the old marker spent
// in both densities. It is still the chip that survives longest: nothing on the
// row is shorter.
func Priority(styles *theme.Styles, priority int, on theme.Slot, flat bool) string {
	return Chip(styles, ChipOpts{
		Text:    strconv.Itoa(priorityLabel(priority)),
		Fill:    theme.PrioritySlot(priority),
		On:      on,
		Flat:    flat,
		Neutral: priorityLabel(priority) != 1,
	})
}

// priorityLabel is the digit the marker prints. Spec section 1.4 as issue #232
// rewrote it: the scale is three values - 1 high, 2 medium, 3 low - and
// anything else is low, digit and hue alike.
//
// Digits and not H/M/L letters, because the effort marker on the same meta row
// is already a letter on a fill and renders an M of its own. Two letter-on-fill
// Ms a few cells apart would be one grammar saying two unrelated things; a
// digit and a letter are two.
//
// Issue #234 migrated the store onto the same three values, so the legacy 4
// this mapping was written to absorb no longer reaches it. The default arm
// stays because the digit must be total: it also indexes the rail styles, and
// an unset priority on a struct that never reached the store still has to
// print something. Low is what an unset priority means everywhere else.
func priorityLabel(priority int) int {
	return board.NormalizePrio(priority)
}
