// Package widget holds kb's hand-crafted TUI elements: the last resort of the
// charm-first sourcing rule of map #136, allowed only for the elements charm
// does not ship. Their reference shape is crush's internal/ui/common.
//
// Every function here is a pure render helper. State lives with the caller, and
// so do styles: a widget takes a *theme.Styles and the palette slot of the
// surface it is drawn onto, because no code under internal/tui outside the
// theme package may construct a lipgloss style.
package widget

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// Density is the layout density of spec section 2.6, resolved by the caller
// from theme.Metrics.DensityFor.
type Density = theme.Density

// The two densities, re-exported so callers of this package do not need to
// name the theme package for a widget argument.
const (
	DensityNormal  = theme.DensityNormal
	DensityCompact = theme.DensityCompact
)

// Truncate is the width-aware shortening primitive of spec section 3.3,
// exported for the spin subpackage, which composes rows the same way the
// widgets in this package do and must not carry a second copy of the rule.
func Truncate(styles *theme.Styles, content string, width int) string {
	return truncate(styles, content, width)
}

// truncate shortens content to width using the width-aware primitive of spec
// section 3.3: ansi.Truncate to one cell short, then the ellipsis glyph.
func truncate(styles *theme.Styles, content string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(content) <= width {
		return content
	}
	if width == 1 {
		return styles.Glyph.Ellipsis
	}
	return ansi.Truncate(content, width-1, "") + styles.Glyph.Ellipsis
}

// pad renders spaces on a surface, so a padded row carries its shade tier all
// the way to the edge rather than punching a hole in it.
func pad(style lipgloss.Style, width int) string {
	if width <= 0 {
		return ""
	}
	return style.Render(strings.Repeat(" ", width))
}

// fill right-pads already-styled content to width on a surface.
func fill(style lipgloss.Style, content string, width int) string {
	return content + pad(style, width-ansi.StringWidth(content))
}

// join lays out entries left to right separated by one space, skipping any
// entry that does not fit. Spec section 3.4: chips that do not fit are skipped
// individually and shorter chips behind them are still attempted, never a
// blanket right-trim.
func join(style lipgloss.Style, entries []string, width int) string {
	line, _ := joinAt(style, entries, width)
	return line
}

// joinAt is join plus the starting column of every entry it emitted, so a
// caller can map a rendered pill back onto a pointer hit region. A skipped or
// empty entry reports -1.
func joinAt(style lipgloss.Style, entries []string, width int) (string, []int) {
	line := ""
	used := 0
	starts := make([]int, len(entries))
	for index, entry := range entries {
		starts[index] = -1
		if entry == "" {
			continue
		}
		separator := 0
		if used > 0 {
			separator = 1
		}
		cost := separator + ansi.StringWidth(entry)
		if used+cost > width {
			continue
		}
		if separator == 1 {
			line += pad(style, 1)
		}
		starts[index] = used + separator
		line += entry
		used += cost
	}
	return line, starts
}
