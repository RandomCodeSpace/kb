package adrsplit

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/tui/formview"
)

const maxPaneWidth = 100

// View renders the overlay without its board background.
func (m *Model) View(width, height int) string {
	if !m.open {
		return ""
	}
	frame, _, _ := m.frame(width, height)
	return lipgloss.Place(max(width, 1), max(height, 1), lipgloss.Center, lipgloss.Center, frame)
}

// Overlay composes the split review over the current board/detail surface.
func (m *Model) Overlay(background string, width, height int) string {
	if !m.open {
		return background
	}
	width, height = max(width, 1), max(height, 1)
	frame, paneWidth, paneHeight := m.frame(width, height)
	return lipgloss.NewCompositor(
		lipgloss.NewLayer(background),
		lipgloss.NewLayer(frame).X(max((width-paneWidth)/2, 0)).Y(max((height-paneHeight)/2, 0)).Z(3),
	).Render()
}

func (m *Model) frame(width, height int) (string, int, int) {
	width, height = max(width, 1), max(height, 1)
	paneWidth := min(max(width-4, 18), maxPaneWidth, width)
	paneHeight := min(max(height-2, 7), height)
	innerWidth := max(paneWidth-4, 1)
	bodyHeight := max(paneHeight-4, 1)
	body := m.bodyLines(innerWidth)
	maxScroll := max(len(body)-bodyHeight, 0)
	if !m.manualScroll {
		focusLine := focusedLine(body)
		if focusLine < m.scroll {
			m.scroll = focusLine
		}
		if focusLine >= m.scroll+bodyHeight {
			m.scroll = focusLine - bodyHeight + 1
		}
	}
	m.scroll = min(max(m.scroll, 0), maxScroll)
	end := min(m.scroll+bodyHeight, len(body))
	visible := make([]string, 0, bodyHeight)
	for _, line := range body[m.scroll:end] {
		visible = append(visible, fit(line, innerWidth))
	}
	for len(visible) < bodyHeight {
		visible = append(visible, "")
	}
	footer := "tab navigate | esc close"
	if m.guardClose {
		footer = m.confirmFooter()
	} else if m.operation != "" {
		footer = m.operation + "... | esc cancel"
	} else if m.adding {
		footer = m.status
	} else if m.status != "" {
		prefix := "status: "
		if m.statusIsError {
			prefix = "error: "
		}
		footer = prefix + sanitize(m.status)
	}
	content := strings.Join(visible, "\n") + "\n" + fit(footer, innerWidth)
	frame := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1).
		Width(innerWidth).
		Height(paneHeight - 2).
		Render(content)
	frame = fitBlock(frame, width, height)
	return frame, lipgloss.Width(frame), lipgloss.Height(frame)
}

func (m *Model) bodyLines(width int) []string {
	if m.stage == stageInput {
		return m.inputLines(width)
	}
	return m.reviewLines(width)
}

func (m *Model) inputLines(width int) []string {
	source := "paste"
	if m.source == sourceFile {
		source = "file"
	}
	lines := []string{
		"SPLIT ADR INTO STORIES",
		"",
		m.choiceLine("source", "Source", source+"  left/right"),
	}
	if m.source == sourcePaste {
		lines = append(lines, m.areaBlock("adr", "ADR markdown", m.adr, width, 8)...)
		lines = append(lines, fmt.Sprintf("  UTF-8 bytes: %d / %d", len([]byte(m.adr.Value())), maxADRBytes))
	} else {
		lines = append(lines, m.inputLine("file", "ADR file", m.filePath, width))
		lines = append(lines, "  read is bounded before AI receives the file")
	}
	lines = append(lines,
		m.choiceLine("max", "Max stories", fmt.Sprintf("%d  left/right (1-20)", m.max)),
		"",
		m.actionLine("cancel", "Cancel"),
		m.actionLine("split", "Propose stories"),
	)
	return lines
}

func (m *Model) reviewLines(width int) []string {
	lines := []string{
		"REVIEW PROPOSED STORIES",
		"Nothing is created until Add selected.",
		"",
	}
	for i := range m.rows {
		row := &m.rows[i]
		mark := " "
		if row.include {
			mark = "x"
		}
		if row.created {
			mark = "*"
		}
		lines = append(lines, m.choiceLine(fmt.Sprintf("include:%d", i), fmt.Sprintf("%d", i+1), "["+mark+"] include"))
		lines = append(lines, m.inputLine(fmt.Sprintf("title:%d", i), "  Title", row.title, width))
		lines = append(lines,
			m.choiceLine(fmt.Sprintf("prio:%d", i), "  Priority", fmt.Sprintf("%d  left/right", row.prio)),
			m.choiceLine(fmt.Sprintf("effort:%d", i), "  Effort", effortName(row.effort)+"  left/right"),
		)
		if row.created {
			lines = append(lines, "    created")
		} else if row.err != "" {
			lines = append(lines, "    error: "+sanitize(row.err))
		}
		lines = append(lines, "")
	}
	lines = append(lines,
		m.choiceLine("dest", "Destination", statusName(m.dest)+"  left/right"),
		"",
		m.actionLine("back", "Back to source"),
		m.actionLine("cancel", "Close"),
		m.actionLine("add", fmt.Sprintf("Add selected (%d)", m.selectedCount())),
	)
	return lines
}

func (m Model) selectedCount() int {
	count := 0
	for _, row := range m.rows {
		if row.include && !row.created && strings.TrimSpace(row.title.Value()) != "" {
			count++
		}
	}
	return count
}

func (m Model) inputLine(target, label string, input textinput.Model, width int) string {
	prefix := m.controlPrefix(target)
	available := max(width-len([]rune(prefix+label+": ")), 1)
	return prefix + label + ": " + inputDisplay(input, m.focus == target, available)
}

func (m Model) choiceLine(target, label, value string) string {
	prefix := m.controlPrefix(target)
	return prefix + label + ": " + sanitize(value)
}

func (m Model) actionLine(target, label string) string {
	prefix := "  [ "
	suffix := " ]"
	if m.pressed(target) {
		prefix, suffix = "  [>", "<]"
	} else if m.focus == target {
		prefix = "> [ "
	}
	return prefix + sanitize(label) + suffix
}

func (m Model) areaBlock(target, label string, area textarea.Model, width, rows int) []string {
	prefix := m.controlPrefix(target)
	lines := []string{prefix + label + ":"}
	return append(lines, areaDisplay(area, m.focus == target, width, rows)...)
}

func (m Model) controlPrefix(target string) string {
	if m.pressed(target) {
		return "! "
	}
	if m.focus == target {
		return "> "
	}
	return "  "
}

func (m Model) pressed(target string) bool { return m.pointerState.IsPressed(controlID(target)) }

func (m Model) confirmFooter() string {
	discard, stay := "[ Discard ]", "[ Stay ]"
	if m.pressed("discard") {
		discard = "[>Discard<]"
	}
	if m.pressed("stay") {
		stay = "[>Stay<]"
	}
	return discard + "  " + stay
}

func inputDisplay(input textinput.Model, focused bool, width int) string {
	return formview.Input(input, focused, width, sanitize, cursorViewport)
}

func areaDisplay(area textarea.Model, focused bool, width, rows int) []string {
	return formview.Area(area, focused, width, rows, sanitize, cursorViewport)
}

func cursorViewport(value string, position, width int) string {
	if width <= 0 {
		return ""
	}
	if width == 1 {
		return "|"
	}
	runes := []rune(value)
	position = min(max(position, 0), len(runes))
	start := max(position-width+2, 0)
	end := min(start+width-1, len(runes))
	visible := append([]rune(nil), runes[start:end]...)
	cursor := position - start
	if cursor >= len(visible) {
		visible = append(visible, '|')
	} else {
		visible = append(visible[:cursor], append([]rune{'|'}, visible[cursor:]...)...)
	}
	return ansi.Truncate(string(visible), width, "")
}

func focusedLine(lines []string) int {
	for i, line := range lines {
		if strings.HasPrefix(line, ">") {
			return i
		}
	}
	return 0
}

func statusName(status board.Status) string {
	switch status {
	case board.StatusTodo:
		return "To Do"
	case board.StatusDoing:
		return "Doing"
	case board.StatusDone:
		return "Done"
	case board.StatusCancelled:
		return "Cancelled"
	default:
		return sanitize(string(status))
	}
}

func effortName(value string) string {
	if value == "" {
		return "none"
	}
	return value
}

func fit(line string, width int) string { return ansi.Truncate(line, max(width, 0), "") }

func fitBlock(block string, width, height int) string {
	lines := strings.Split(block, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for i := range lines {
		lines[i] = fit(lines[i], width)
	}
	return strings.Join(lines, "\n")
}
