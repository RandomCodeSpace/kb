package formview

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// TerminalTextView renders an immutable plain-text snapshot with terminal
// mouse reporting disabled by the owning root view. The terminal can then own
// ordinary drag selection and its native copy command.
func TerminalTextView(snapshot string, offset, width, height int) string {
	width, height = max(width, 1), max(height, 1)
	if height == 1 {
		return ansi.Truncate("esc return", width, "")
	}
	lines := terminalTextLines(snapshot, width)
	bodyRows := terminalTextBodyRows(height)
	offset = terminalTextOffset(lines, offset, bodyRows)
	end := min(offset+bodyRows, len(lines))
	body := append([]string(nil), lines[offset:end]...)
	for len(body) < bodyRows {
		body = append(body, "")
	}
	header := ansi.Truncate("Select text with the terminal, then use its copy command", width, "")
	footer := fmt.Sprintf("esc return  %d-%d/%d  up/down scroll  scroll or resize may clear terminal selection",
		offset+1, max(end, offset+1), max(len(lines), 1))
	footer = ansi.Truncate(footer, width, "")
	if height == 2 {
		return strings.Join(append(body, footer), "\n")
	}
	return strings.Join(append(append([]string{header}, body...), footer), "\n")
}

// UpdateTerminalText applies navigation to an immutable snapshot. Exit is
// true only for Escape; callers retain ownership of their surrounding model.
func UpdateTerminalText(snapshot string, offset, width, height int, key string) (next int, exit bool) {
	if key == "esc" {
		return 0, true
	}
	lines := terminalTextLines(snapshot, max(width, 1))
	bodyRows := terminalTextBodyRows(max(height, 1))
	maxOffset := max(len(lines)-bodyRows, 0)
	switch key {
	case "up", "k":
		offset--
	case "down", "j":
		offset++
	case "pgup":
		offset -= bodyRows
	case "pgdown":
		offset += bodyRows
	case "home", "g":
		offset = 0
	case "end", "G":
		offset = maxOffset
	}
	return min(max(offset, 0), maxOffset), false
}

func terminalTextLines(snapshot string, width int) []string {
	return strings.Split(ansi.Hardwrap(snapshot, max(width, 1), true), "\n")
}

func terminalTextBodyRows(height int) int {
	switch {
	case height <= 1:
		return 0
	case height == 2:
		return 1
	default:
		return height - 2
	}
}

func terminalTextOffset(lines []string, offset, bodyRows int) int {
	return min(max(offset, 0), max(len(lines)-bodyRows, 0))
}
