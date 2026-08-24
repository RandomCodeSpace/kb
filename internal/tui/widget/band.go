package widget

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// BandOpts describes one column header band. Spec section 2.2: one row, full
// column width, no rule, no border and no separator line under it. The tier
// step from the band to the panel body is the separation.
type BandOpts struct {
	Index   int
	Label   string
	Count   int
	Hue     theme.Slot
	Focused bool
	Width   int

	// Hovered thickens the unfocused band's rail glyph, per spec section
	// 10.5.1. The band is already bold, so bold is not available as a cue, and
	// it cannot change background without becoming the focused band; the rail
	// slot is the one cell it has spare. A focused band renders no hover: it is
	// already the acting column, and ratified call 9 keeps focus off the
	// pointer, so there is nothing for hover to promise.
	Hovered bool
}

// Band renders one column header band. Unfocused it sits on the Raised tier
// with the column hue as foreground; focused it fills solid with the column hue
// and the rail becomes a focus caret so the band reads as filled edge to edge.
//
// The status dot survives the focus change. Spec section 10.4.4, ratified as
// contestable call 11: dropping it moved the label from column 5 to column 4,
// so moving focus across the board jittered every label one cell. Keeping the
// dot restores the BandHeadW reserve in both states and holds the label column
// fixed, and it costs nothing.
func Band(styles *theme.Styles, opts BandOpts) string {
	if opts.Width <= 0 {
		return ""
	}
	text := styles.OnBold(opts.Hue, theme.BandRest)
	head := styles.Glyph.Rail
	switch {
	case opts.Focused:
		text = styles.OnBold(theme.FgOnAccent, opts.Hue)
		head = styles.Glyph.Focus
	case opts.Hovered:
		head = styles.Glyph.RailFull
	}
	head += styles.Glyph.Dot + " " + strconv.Itoa(opts.Index) + " "

	count := strconv.Itoa(opts.Count) + " "
	available := opts.Width - ansi.StringWidth(head)
	field := min(available-ansi.StringWidth(count), opts.Width-styles.Metrics.BandHeadW)
	if field < 0 {
		field = 0
	}
	label := truncate(styles, opts.Label, field)
	gap := available - field - ansi.StringWidth(count)
	if gap < 0 {
		gap = 0
	}
	row := head + label + spaces(field-ansi.StringWidth(label)) + spaces(gap) + count
	return text.Render(exact(row, opts.Width))
}

func spaces(width int) string {
	if width <= 0 {
		return ""
	}
	return strings.Repeat(" ", width)
}

// exact pads or hard-truncates plain content to a fixed cell width.
func exact(content string, width int) string {
	measured := ansi.StringWidth(content)
	if measured == width {
		return content
	}
	if measured > width {
		return ansi.Truncate(content, width, "")
	}
	return content + spaces(width-measured)
}
