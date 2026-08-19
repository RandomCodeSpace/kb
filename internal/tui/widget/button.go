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
type ButtonOpts struct {
	Text           string
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
	body := style.Render(strings.Repeat(" ", max(opts.Padding[0], 0))) +
		underlined(style, opts.Text, opts.UnderlineIndex) +
		style.Render(strings.Repeat(" ", max(opts.Padding[1], 0)))
	if opts.Pressed {
		return styles.Pressed.Render(body)
	}
	return body
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
	switch {
	case opts.Armed:
		return styles.Button.Armed
	case opts.Selected:
		return styles.Button.Focused
	case opts.Hovered:
		return styles.Button.Hovered
	default:
		return styles.Button.Rest
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
