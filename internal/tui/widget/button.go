package widget

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// ButtonOpts describes one button. The shape is crush's internal/ui/common
// ButtonOpts (map #136), plus Armed, kb's addition for the purge and remove
// two-step of spec section 5.1.
//
// UnderlineIndex is the rune offset of the hotkey letter, or a negative value
// for no hotkey. Padding is the left and right padding in cells.
//
// Variant is what the button does (issue #157): the zero value is Neutral, so a
// caller that states no meaning gets the calmest surface rather than an
// accidental accent.
type ButtonOpts struct {
	Text           string
	Variant        theme.ButtonVariant
	Selected       bool
	Hovered        bool
	Armed          bool
	Pressed        bool
	UnderlineIndex int
	Padding        [2]int
}

// Button renders one button. State precedence is armed, then selected, then
// hovered, then resting: the two-step confirm has to win, because it is the
// state the user must not misread.
func Button(styles *theme.Styles, opts ButtonOpts) string {
	style := buttonStyle(styles, opts)
	if opts.Pressed {
		// The pressed token is an attribute on the button's own style, not a
		// wrapper: a wrapping run is cancelled by the reset the inner style
		// emits, and the feedback would vanish under composition.
		style = style.Reverse(true)
	}
	return style.Render(strings.Repeat(" ", max(opts.Padding[0], 0))) +
		underlined(style, opts.Text, opts.UnderlineIndex) +
		style.Render(strings.Repeat(" ", max(opts.Padding[1], 0)))
}

// ButtonGroup lays buttons out left to right with gap cells between them. The
// gap is rendered on the surface behind the group so the group does not punch
// a hole in the shade tier it sits on.
func ButtonGroup(styles *theme.Styles, on theme.Slot, gap int, buttons ...string) string {
	separator := pad(styles.On(theme.FgBase, on), max(gap, 0))
	rendered := make([]string, 0, len(buttons))
	for _, button := range buttons {
		if button == "" {
			continue
		}
		rendered = append(rendered, button)
	}
	return strings.Join(rendered, separator)
}

func buttonStyle(styles *theme.Styles, opts ButtonOpts) lipgloss.Style {
	variant := styles.Button.Variant(opts.Variant)
	switch {
	case opts.Armed:
		return variant.Armed
	case opts.Selected:
		return variant.Focused
	case opts.Hovered:
		return variant.Hovered
	default:
		return variant.Rest
	}
}

// underlined renders text with one rune underlined, the hotkey cue crush draws
// with StyleRanges. Rendering the three runs directly keeps the cue exact for
// multi-byte runes.
func underlined(style lipgloss.Style, text string, index int) string {
	runes := []rune(text)
	if index < 0 || index >= len(runes) {
		return style.Render(text)
	}
	return style.Render(string(runes[:index])) +
		style.Underline(true).Render(string(runes[index])) +
		style.Render(string(runes[index+1:]))
}
