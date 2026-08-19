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
}

// bandLabelReserve is the prefix the unfocused band spends before its label:
// rail, status dot, space, index digit, space (spec section 2.2).
const bandLabelReserve = 5

// Band renders one column header band. Unfocused it sits on the Raised tier
// with the column hue as foreground; focused it fills solid with the column hue
// and the rail becomes a focus caret so the band reads as filled edge to edge.
func Band(styles *theme.Styles, opts BandOpts) string {
	if opts.Width <= 0 {
		return ""
	}
	text := styles.OnBold(opts.Hue, theme.BandRest)
	head := styles.Glyph.Rail + styles.Glyph.Dot
	if opts.Focused {
		text = styles.OnBold(theme.FgOnAccent, opts.Hue)
		head = styles.Glyph.Focus
	}
	head += " " + strconv.Itoa(opts.Index) + " "

	count := strconv.Itoa(opts.Count) + " "
	available := opts.Width - ansi.StringWidth(head)
	field := min(available-ansi.StringWidth(count), opts.Width-bandLabelReserve)
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
