package cardeditor

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
	"github.com/RandomCodeSpace/kb/internal/tui/formview"
	"github.com/RandomCodeSpace/kb/internal/tui/pointer"
)

const maxEditorWidth = 96

type pointerHit struct {
	x0, x1 int
	y0, y1 int
	target string
}

// MouseHandler maps clicks in the rendered editor to symbolic control targets.
// The root model can install this callback while the editor owns the screen;
// the resulting message is routed through Update just like a key press.
func (m Model) MouseHandler(width, height int) func(tea.MouseMsg) tea.Cmd {
	if !m.open {
		return nil
	}
	session := m.session
	var hitMap pointer.Map
	for _, hit := range m.pointerHits(width, height) {
		target := hit.target
		hitMap.AddControl(pointer.ControlID(target), pointer.Rect{X0: hit.x0, Y0: hit.y0, X1: hit.x1, Y1: hit.y1}, func(pointer.Point) tea.Msg {
			return pointerClickMsg{session: session, target: target}
		})
	}
	width, height = max(width, 1), max(height, 1)
	_, paneWidth, paneHeight := m.frame(width, height)
	x0 := max((width-paneWidth)/2, 0)
	y0 := max((height-paneHeight)/2, 0)
	innerWidth := max(paneWidth-4, 1)
	bodyHeight := max(paneHeight-4, 1)
	maxScroll := max(len(m.bodyLines(innerWidth))-bodyHeight, 0)
	hitMap.AddWheel(pointer.Rect{X0: x0, Y0: y0, X1: x0 + paneWidth, Y1: y0 + paneHeight}, func(delta int) tea.Msg {
		return pointerWheelMsg{session: session, delta: delta, maxScroll: maxScroll}
	})
	return hitMap.Handler()
}

func (m Model) pointerHits(width, height int) []pointerHit {
	_, paneWidth, paneHeight := m.frame(width, height)
	width, height = max(width, 1), max(height, 1)
	x0 := max((width-paneWidth)/2, 0)
	y0 := max((height-paneHeight)/2, 0)
	innerWidth := max(paneWidth-4, 1)
	bodyHeight := max(paneHeight-3, 1)
	body := m.bodyLines(innerWidth)
	maxScroll := max(len(body)-bodyHeight, 0)
	scroll := min(max(m.scroll, 0), maxScroll)
	hits := make([]pointerHit, 0, len(body))
	areaTarget, areaRows := "", 0
	similarRows, similarIndex := m.visibleSimilar(), 0
	for index, line := range body {
		target := ""
		if areaRows > 0 {
			target = areaTarget
			areaRows--
		} else {
			lineText := pointerLineText(line)
			if similarIndex < len(similarRows) && lineText == strings.TrimSpace(similarText(similarRows[similarIndex])+"  [Enter dismiss]") {
				target = "similar:" + similarKey(similarRows[similarIndex])
				similarIndex++
			} else {
				target = pointerTarget(m, line)
			}
			if target == "ai-prompt" {
				areaTarget, areaRows = target, 2
			} else if target == "desc" || target == "checks" {
				areaTarget, areaRows = target, 3
			}
		}
		if target == "" {
			continue
		}
		y := y0 + 1 + index - scroll
		if y < y0+1 || y >= y0+1+bodyHeight {
			continue
		}
		hits = append(hits, pointerHit{
			x0: x0 + 1, x1: x0 + paneWidth - 1,
			y0: y, y1: y + 1,
			target: target,
		})
	}
	if m.guardClose {
		footerY := y0 + paneHeight - 2
		footerX := x0 + 2
		for _, target := range []struct {
			label  string
			target string
		}{
			{label: "[Discard]", target: "discard"},
			{label: "[Keep editing]", target: "keep"},
		} {
			start := strings.Index(ansi.Strip(m.footerLine(innerWidth)), target.label)
			if start < 0 {
				continue
			}
			hits = append(hits, pointerHit{
				x0: footerX + start, x1: footerX + start + ansi.StringWidth(target.label),
				y0: footerY, y1: footerY + 1, target: target.target,
			})
		}
	}
	return hits
}

func pointerTarget(model Model, line string) string {
	trimmed := pointerLineText(line)
	if model.labelsOpen {
		for _, suggestion := range model.filteredLabels() {
			if trimmed == "› "+sanitize(suggestion) || trimmed == sanitize(suggestion) {
				return "label:" + suggestion
			}
		}
	}
	switch {
	case strings.HasPrefix(trimmed, "Request:"):
		return "ai-prompt"
	case trimmed == "[Draft]" || strings.HasPrefix(trimmed, "[Cancel draft"):
		return "ai-draft"
	case strings.HasPrefix(trimmed, "Title:"):
		return "title"
	case strings.HasPrefix(trimmed, "Emoji:"):
		return "emoji"
	case trimmed == "Description:":
		return "desc"
	case strings.HasPrefix(trimmed, "Priority:"):
		return "prio"
	case strings.HasPrefix(trimmed, "Due:"):
		return "due"
	case strings.HasPrefix(trimmed, "Effort:"):
		return "effort"
	case strings.HasPrefix(trimmed, "Blocked:"):
		return "blocked"
	case strings.HasPrefix(trimmed, "Labels:"):
		return "labels"
	case trimmed == "Checklist (x prefix = done):":
		return "checks"
	case trimmed == "[Cancel]":
		return "cancel"
	case trimmed == "[Save card]":
		return "save"
	case trimmed == "[Discard]":
		return "discard"
	case trimmed == "[Keep editing]":
		return "keep"
	case trimmed == "[Dismiss all similar items]":
		return "similar:all"
	}
	return ""
}

func pointerLineText(line string) string {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, ">"))
	return strings.TrimSpace(strings.TrimPrefix(trimmed, "!"))
}

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
	if !m.manualScroll {
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

	content := strings.Join(visible, "\n") + "\n" + m.footerLine(innerWidth)
	frame := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1).
		Width(innerWidth).
		Height(paneHeight - 2).
		Render(content)
	frame = fitBlock(frame, width, height)
	return frame, lipgloss.Width(frame), lipgloss.Height(frame)
}

func (m *Model) footerLine(width int) string {
	footer := "tab navigate | ctrl+s/ctrl+enter save | esc close"
	if m.statusMessage != "" {
		prefix := "status: "
		if m.statusIsError {
			prefix = "error: "
		}
		footer = prefix + sanitize(m.statusMessage)
	}
	if m.guardClose {
		line := fit("[Discard] [Keep editing] | D discard | esc keep editing", width)
		line = strings.Replace(line, "[Discard]", m.pressedLabel("discard", "[Discard]"), 1)
		line = strings.Replace(line, "[Keep editing]", m.pressedLabel("keep", "[Keep editing]"), 1)
		return line
	}
	if m.drafting {
		footer = "drafting card... | esc cancel"
	} else if m.saving {
		footer = "saving card..."
	}
	return fit(footer, width)
}

func (m Model) pressedLabel(target, label string) string {
	if !m.pointerState.IsPressed(pointer.ControlID(target)) {
		return label
	}
	return lipgloss.NewStyle().Reverse(true).Render(label)
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
	if m.runner != nil {
		lines = append(lines, "Draft with AI (fills the form; review before Save)")
		lines = append(lines, m.areaBlock("ai-prompt", "Request", m.draftPrompt, width, 2)...)
		action := "Draft"
		if m.drafting {
			action = "Cancel draft (Esc)"
		}
		lines = append(lines, m.actionLine("ai-draft", action), "")
	}
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
	marker := m.controlMarker(target)
	prefix := marker + label + ": "
	available := max(width-ansi.StringWidth(prefix), 1)
	return prefix + inputDisplay(input, m.focus == target, available)
}

func (m *Model) choiceLine(target, label, value string) string {
	return m.controlMarker(target) + label + ": " + sanitize(value)
}

func (m *Model) actionLine(target, label string) string {
	return m.controlMarker(target) + "[" + label + "]"
}

func (m *Model) areaBlock(target, label string, area textarea.Model, width, rows int) []string {
	lines := []string{m.controlMarker(target) + label + ":"}
	return append(lines, areaDisplay(area, m.focus == target, width, rows)...)
}

func (m Model) controlMarker(target string) string {
	if m.pointerState.IsPressed(pointer.ControlID(target)) {
		return "! "
	}
	if m.focus == target {
		return "> "
	}
	return "  "
}

func (m Model) labelSuggestionLines() []string {
	suggestions := m.filteredLabels()
	if len(suggestions) == 0 {
		return []string{"    (no label suggestions; Enter adds typed labels)"}
	}
	lines := []string{"    suggestions (up/down, Enter add):"}
	for i, suggestion := range suggestions {
		marker := "  "
		if m.pointerState.IsPressed(pointer.ControlID("label:" + suggestion)) {
			marker = "! "
		} else if i == min(m.labelHighlight, len(suggestions)-1) {
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
		if m.pointerState.IsPressed(pointer.ControlID(target)) {
			marker = "!   "
		} else if m.focus == target {
			marker = ">   "
		}
		lines = append(lines, marker+similarText(hit)+"  [Enter dismiss]")
	}
	marker := "    "
	if m.pointerState.IsPressed(pointer.ControlID("similar:all")) {
		marker = "!   "
	} else if m.focus == "similar:all" {
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
