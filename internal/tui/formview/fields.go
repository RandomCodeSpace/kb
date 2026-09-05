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
	area *textarea.Model,
	focused bool,
	width, rows int,
	clean func(string) string,
	cursor func(string, int, int) string,
) []string {
	rows = max(rows, 0)
	out := make([]string, 0, rows)
	if area == nil || rows == 0 {
		return out
	}

	// The textarea model owns soft wrapping and its viewport. Keep its actual
	// dimensions in sync with the rendered control so vertical cursor movement
	// and the next Update use the same line breaks the user can see.
	contentWidth := max(width-4, 1)
	area.SetWidth(contentWidth)
	area.SetHeight(rows)
	area.SetVirtualCursor(false)
	// View primes Bubbles' viewport content after a width change. Reapplying
	// the height then lets its own cursor-aware scroll logic place the current
	// soft-wrapped line inside the viewport on this same frame.
	_ = area.View()
	area.SetHeight(rows)
	rendered := strings.Split(ansi.Strip(area.View()), "\n")

	var cursorX, cursorY int
	hasCursor := false
	if focused {
		if position := area.Cursor(); position != nil {
			cursorX, cursorY, hasCursor = position.Position.X, position.Position.Y, true
		}
	}
	for index := 0; index < len(rendered) && len(out) < rows; index++ {
		content := strings.TrimRight(clean(rendered[index]), " ")
		if hasCursor && index == cursorY && cursor != nil {
			content = cursor(content, runeIndexAtCell(content, cursorX), contentWidth)
		}
		out = append(out, "    "+ansi.Truncate(content, contentWidth, ""))
	}
	for len(out) < rows {
		out = append(out, "    ")
	}
	return out
}

func runeIndexAtCell(value string, cell int) int {
	cell = max(cell, 0)
	runes := []rune(value)
	for index := range runes {
		if ansi.StringWidth(string(runes[:index+1])) > cell {
			return index
		}
	}
	return len(runes)
}
