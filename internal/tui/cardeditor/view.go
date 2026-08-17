package cardeditor

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
)

const maxEditorWidth = 96

// View renders the editor pane without its board background.
func (m *Model) View(width, height int) string {
	if !m.open {
		return ""
	}
	frame, _, _ := m.frame(width, height)
	return lipgloss.Place(max(width, 1), max(height, 1), lipgloss.Center, lipgloss.Center, frame)
}

// Overlay composes the editor over the board/detail surface.
func (m *Model) Overlay(background string, width, height int) string {
	if !m.open {
		return background
	}
	width, height = max(width, 1), max(height, 1)
	frame, paneWidth, paneHeight := m.frame(width, height)
	return lipgloss.NewCompositor(
		lipgloss.NewLayer(background),
		lipgloss.NewLayer(frame).X(max((width-paneWidth)/2, 0)).Y(max((height-paneHeight)/2, 0)).Z(2),
	).Render()
}

func (m *Model) frame(width, height int) (string, int, int) {
	width, height = max(width, 1), max(height, 1)
	paneWidth := min(max(width-4, 18), maxEditorWidth, width)
	paneHeight := min(max(height-2, 7), height)
	innerWidth := max(paneWidth-4, 1)
	bodyHeight := max(paneHeight-4, 1)

	body := m.bodyLines(innerWidth)
	focusLine := 0
	for i, line := range body {
		if strings.HasPrefix(line, ">") {
			focusLine = i
			break
		}
	}
	maxScroll := max(len(body)-bodyHeight, 0)
	if focusLine < m.scroll {
		m.scroll = focusLine
	}
	if focusLine >= m.scroll+bodyHeight {
		m.scroll = focusLine - bodyHeight + 1
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

	footer := "tab navigate | ctrl+s save | esc close"
	if m.statusMessage != "" {
		prefix := "status: "
		if m.statusIsError {
			prefix = "error: "
		}
		footer = prefix + sanitize(m.statusMessage)
	}
	if m.guardClose {
		footer = "D discard | esc keep editing"
	} else if m.saving {
		footer = "saving card..."
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
	title := "CREATE CARD / " + string(m.status)
	if m.mode == modeEdit {
		title = "EDIT CARD"
		if m.base.Seq > 0 {
			title += fmt.Sprintf(" #%d", m.base.Seq)
		}
	}
	lines := []string{title, ""}
	lines = append(lines,
		m.inputLine("title", "Title", m.title, width),
		m.inputLine("emoji", "Emoji", m.emoji, width),
	)
	if emoji := strings.TrimSpace(m.emoji.Value()); emoji != "" && !board.IsSingleEmoji(emoji) {
		lines = append(lines, "  error: one Extended_Pictographic character plus optional variation selector")
	}
	lines = append(lines, m.areaBlock("desc", "Description", m.desc, width, 3)...)
	lines = append(lines,
		m.choiceLine("prio", "Priority", fmt.Sprintf("%d (%s)  left/right", m.prio, priorityName(m.prio))),
		m.inputLine("due", "Due", m.due, width)+"  [/] day; ctrl+x clear",
		m.choiceLine("effort", "Effort", effortName(m.effort)+"  left/right; ctrl+x clear"),
		m.choiceLine("blocked", "Blocked", boolName(m.blocked)+"  space toggle"),
		m.inputLine("labels", "Labels", m.label, width),
		"  selected: "+safeList(m.tags),
	)
	if m.focus == "labels" && m.labelsOpen {
		lines = append(lines, m.labelSuggestionLines()...)
	}
	if m.labelsErr != nil {
		lines = append(lines, "  labels error: "+safeError(m.labelsErr))
	}
	lines = append(lines, m.areaBlock("checks", "Checklist (x prefix = done)", m.checks, width, 3)...)
	lines = append(lines, m.similarLines()...)
	if m.stale {
		lines = append(lines, "  external refresh withheld while form is dirty")
	}
	if m.statusMessage != "" {
		prefix := "  status: "
		if m.statusIsError {
			prefix = "  error: "
		}
		lines = append(lines, prefix+sanitize(m.statusMessage))
	}
	lines = append(lines, "", m.actionLine("cancel", "Cancel"), m.actionLine("save", "Save card"))
	return lines
}

func (m *Model) inputLine(target, label string, input textinput.Model, width int) string {
	marker := "  "
	if m.focus == target {
		marker = "> "
	}
	prefix := marker + label + ": "
	available := max(width-ansi.StringWidth(prefix), 1)
	return prefix + inputDisplay(input, m.focus == target, available)
}

func (m *Model) choiceLine(target, label, value string) string {
	marker := "  "
	if m.focus == target {
		marker = "> "
	}
	return marker + label + ": " + sanitize(value)
}

func (m *Model) actionLine(target, label string) string {
	marker := "  "
	if m.focus == target {
		marker = "> "
	}
	return marker + "[" + label + "]"
}

func (m *Model) areaBlock(target, label string, area textarea.Model, width, rows int) []string {
	marker := "  "
	if m.focus == target {
		marker = "> "
	}
	lines := []string{marker + label + ":"}
	return append(lines, areaDisplay(area, m.focus == target, width, rows)...)
}

func (m Model) labelSuggestionLines() []string {
	suggestions := m.filteredLabels()
	if len(suggestions) == 0 {
		return []string{"    (no label suggestions; Enter adds typed labels)"}
	}
	lines := []string{"    suggestions (up/down, Enter add):"}
	for i, suggestion := range suggestions {
		marker := "  "
		if i == min(m.labelHighlight, len(suggestions)-1) {
			marker = "› "
		}
		lines = append(lines, "    "+marker+sanitize(suggestion))
	}
	return lines
}

func (m Model) similarLines() []string {
	lines := []string{}
	switch {
	case m.similarLoading:
		return []string{"", "  similar items: searching..."}
	case m.similarErr != nil:
		return []string{"", "  similar items error: " + safeError(m.similarErr)}
	}
	hits := m.visibleSimilar()
	if len(hits) == 0 {
		return lines
	}
	lines = append(lines, "", fmt.Sprintf("  similar items (%d):", len(hits)))
	for _, hit := range hits {
		target := "similar:" + similarKey(hit)
		marker := "    "
		if m.focus == target {
			marker = ">   "
		}
		lines = append(lines, marker+similarText(hit)+"  [Enter dismiss]")
	}
	marker := "    "
	if m.focus == "similar:all" {
		marker = ">   "
	}
	return append(lines, marker+"[Dismiss all similar items]")
}

func similarText(hit store.SimilarHit) string {
	via := hit.Via
	if via == "" {
		via = "card"
	}
	if hit.Via == "killed" {
		context := "killed"
		if hit.KilledAt != "" {
			context += " " + hit.KilledAt
		}
		if reason := strings.TrimSpace(hit.Reason); reason != "" {
			context += " — " + reason
		}
		return "[" + sanitize(context) + "] " + sanitize(hit.Title)
	}
	return "[" + sanitize(via) + "] " + sanitize(hit.Title)
}

func inputDisplay(input textinput.Model, focused bool, width int) string {
	value := input.Value()
	if value == "" {
		value = input.Placeholder
	}
	runes := []rune(value)
	position := min(max(input.Position(), 0), len(runes))
	safe := sanitize(value)
	safePosition := len([]rune(sanitize(string(runes[:position]))))
	if !focused {
		return ansi.Truncate(safe, max(width, 0), "")
	}
	return cursorViewport(safe, safePosition, width)
}

func areaDisplay(area textarea.Model, focused bool, width, rows int) []string {
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
		content := sanitize(logical[i])
		if focused && i == line {
			content = cursorViewport(content, min(area.Column(), len([]rune(content))), max(width-4, 1))
		}
		out = append(out, "    "+ansi.Truncate(content, max(width-4, 0), ""))
	}
	for len(out) < rows {
		out = append(out, "    ")
	}
	return out
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
	before, after := string(runes[:position]), string(runes[position:])
	contentWidth := width - 1
	cursorColumn := ansi.StringWidth(before)
	left := max(cursorColumn-contentWidth, 0)
	visibleBefore := ansi.Cut(before, left, cursorColumn)
	remaining := max(contentWidth-ansi.StringWidth(visibleBefore), 0)
	return visibleBefore + "|" + ansi.Truncate(after, remaining, "")
}

func priorityName(priority int) string {
	return map[int]string{1: "urgent", 2: "high", 3: "normal", 4: "low"}[priority]
}

func effortName(effort string) string {
	if effort == "" {
		return "none"
	}
	return effort
}

func boolName(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func safeList(values []string) string {
	if len(values) == 0 {
		return "(none)"
	}
	safe := make([]string, len(values))
	for i, value := range values {
		safe[i] = "[" + sanitize(value) + "]"
	}
	return strings.Join(safe, " ")
}

func fit(line string, width int) string {
	return ansi.Truncate(sanitize(line), max(width, 0), "")
}

func fitBlock(block string, width, height int) string {
	lines := strings.Split(block, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], max(width, 0), "")
	}
	return strings.Join(lines, "\n")
}
