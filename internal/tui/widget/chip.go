package widget

import (
	"strconv"
	"strings"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// ChipOpts describes one pill. Spec section 3.6: the pill is the language's
// chip primitive, half-block end caps carrying the fill color as foreground
// over the surface behind, so it reads as rounded at half-cell resolution.
type ChipOpts struct {
	Text string     // the pill body
	Key  string     // non-empty selects the scoped two-tone form
	Fill theme.Slot // the pill fill
	On   theme.Slot // the surface the pill is drawn onto
	Flat bool       // the compact degradation: flat colored bold text
}

// Chip renders one pill. Cost is width(Text)+2 columns for the capped form and
// width(Text) for the flat form.
func Chip(styles *theme.Styles, opts ChipOpts) string {
	if opts.Text == "" {
		return ""
	}
	runs := styles.ChipRuns(opts.Fill, opts.On)
	if opts.Flat {
		return runs.Flat.Render(opts.Text)
	}
	if opts.Key == "" {
		return runs.CapLeft.Render(styles.Glyph.CapL) +
			runs.Body.Render(opts.Text) +
			runs.CapRight.Render(styles.Glyph.CapR)
	}
	// The scoped variant substitutes Surface and FgSubtle for the first cap
	// and the key run (spec sections 1.6 and 3.6).
	return styles.On(theme.Surface, opts.On).Render(styles.Glyph.CapL) +
		runs.ScopedKey.Render(opts.Key) +
		runs.Body.Render(opts.Text) +
		runs.CapRight.Render(styles.Glyph.CapR)
}

// Label renders one label pill, wheel-hued by the hash of spec section 1.6.
// A scoped key::value tag becomes a two-tone pill, a plain tag a single-tone
// #tag pill, and the compact form drops the caps: scoped keeps only its value,
// plain keeps its hash prefix.
func Label(styles *theme.Styles, tag string, on theme.Slot, flat bool) string {
	if tag == "" {
		return ""
	}
	fill := theme.LabelSlot(LabelWheel(tag))
	key, value, scoped := strings.Cut(tag, "::")
	if !scoped || key == "" || value == "" {
		return Chip(styles, ChipOpts{Text: styles.Glyph.MarkTag + tag, Fill: fill, On: on, Flat: flat})
	}
	if flat {
		return Chip(styles, ChipOpts{Text: value, Fill: fill, On: on, Flat: true})
	}
	return Chip(styles, ChipOpts{Text: value, Key: key + ":", Fill: fill, On: on})
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
