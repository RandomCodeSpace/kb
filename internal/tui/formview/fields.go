// Package formview renders terminal form controls shared by TUI overlays.
package formview

import (
	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	"github.com/charmbracelet/x/ansi"
)

// Input renders one text input within the requested terminal width.
func Input(
	input textinput.Model,
	focused bool,
	width int,
	clean func(string) string,
	cursor func(string, int, int) string,
) string {
	value := input.Value()
	if value == "" {
		value = input.Placeholder
	}
	runes := []rune(value)
	position := min(max(input.Position(), 0), len(runes))
	safe := clean(value)
	safePosition := len([]rune(clean(string(runes[:position]))))
	if !focused {
		return ansi.Truncate(safe, max(width, 0), "")
	}
	return cursor(safe, safePosition, width)
}

// Area renders a fixed-height textarea viewport.
func Area(
	area textarea.Model,
	focused bool,
	width, rows int,
	clean func(string) string,
	cursor func(string, int, int) string,
) []string {
	value := area.Value()
	if value == "" {
		value = area.Placeholder
	}
	logical := strings.Split(value, "\n")
	line := min(max(area.Line(), 0), len(logical)-1)
	start := max(line-rows+1, 0)
	end := min(start+rows, len(logical))
	out := make([]string, 0, rows)
	for i := start; i < end; i++ {
		content := clean(logical[i])
		if focused && i == line {
			content = cursor(content, min(area.Column(), len([]rune(content))), max(width-4, 1))
		}
		out = append(out, "    "+ansi.Truncate(content, max(width-4, 0), ""))
	}
	for len(out) < rows {
		out = append(out, "    ")
	}
	return out
}
