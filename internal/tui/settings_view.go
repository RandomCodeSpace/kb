package tui

import (
	"strings"
	"unicode/utf8"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/pointer"
)

type settingsRenderRow struct {
	line   string
	target string
}

func (m *settingsModel) View(width, height int) string {
	return m.Surface(width, height).Content
}

func (m *settingsModel) Surface(width, height int) pointer.Surface {
	width = max(width, 1)
	height = max(height, 3)
	inputWidth := max(width-18, 8)
	m.aiBase.SetWidth(inputWidth)
	m.aiModel.SetWidth(inputWidth)
	m.aiKey.SetWidth(inputWidth)
	for i := range m.rows {
		m.rows[i].name.SetWidth(inputWidth)
		m.rows[i].baseURL.SetWidth(inputWidth)
		m.rows[i].project.SetWidth(inputWidth)
		m.rows[i].token.SetWidth(inputWidth)
	}

	header := settingsFit("kb / settings / "+m.user, width)
	body := []settingsRenderRow{{line: ""}, {line: "AI SETTINGS"}}
	if !m.loaded {
		body = append(body, settingsRenderRow{line: "loading settings..."})
	} else {
		body = append(body,
			settingsRenderRow{line: m.inputModelLine("ai:base", "Base URL", m.aiBase, false, width), target: "ai:base"},
			settingsRenderRow{line: m.inputModelLine("ai:model", "Model", m.aiModel, false, width), target: "ai:model"},
			settingsRenderRow{line: m.inputModelLine("ai:key", keyLabel("API key", m.hasKey), m.aiKey, true, width), target: "ai:key"},
			settingsRenderRow{line: m.actionLine("ai:test", "Test connection", width), target: "ai:test"},
			settingsRenderRow{line: m.actionLine("ai:save", "Save AI settings", width), target: "ai:save"},
			settingsRenderRow{line: ""}, settingsRenderRow{line: "FORGE INTEGRATIONS"},
		)
		if len(m.rows) == 0 {
			body = append(body, settingsRenderRow{line: "(none configured)"})
		}
		for i := range m.rows {
			body = append(body, m.renderForgeRow(&m.rows[i], width)...)
		}
		body = append(body, settingsRenderRow{line: m.actionLine("forge:add", "+ Add integration", width), target: "forge:add"})
	}
	status := m.status
	if status == "" {
		status = "ready"
	}
	if m.statusIsError {
		status = "error: " + status
	}
	footer := settingsFit("[Close] | "+status+" | tab navigate | enter act", width)

	bodyHeight := height - 2
	focusLine := -1
	for i, row := range body {
		if strings.HasPrefix(row.line, ">") {
			focusLine = i
			break
		}
	}
	maxScroll := max(len(body)-bodyHeight, 0)
	if focusLine >= 0 && focusLine < m.scroll {
		m.scroll = focusLine
	}
	if focusLine >= m.scroll+bodyHeight {
		m.scroll = focusLine - bodyHeight + 1
	}
	m.scroll = min(max(m.scroll, 0), maxScroll)
	end := min(m.scroll+bodyHeight, len(body))
	visible := body[m.scroll:end]
	lines := make([]string, 0, height)
	lines = append(lines, header)
	var hitMap pointer.Map
	viewport := pointer.Viewport{
		Rect:   pointer.Rect{X0: 0, Y0: 1, X1: width, Y1: height - 1},
		Scroll: m.scroll,
	}
	for logicalRow, row := range body {
		if row.target == "" {
			continue
		}
		if rect, ok := viewport.Row(logicalRow, 0, width); ok {
			target := row.target
			hitMap.Add(rect, func(pointer.Point) tea.Msg { return settingsPointerMsg{target: target} })
		}
	}
	for _, row := range visible {
		lines = append(lines, settingsFit(row.line, width))
	}
	footerY := len(lines)
	lines = append(lines, footer)
	hitMap.AddWheel(viewport.Rect, func(delta int) tea.Msg { return settingsWheelMsg{delta: delta} })
	hitMap.Add(
		pointer.Rect{X0: 0, Y0: footerY, X1: min(width, 7), Y1: footerY + 1},
		func(pointer.Point) tea.Msg { return settingsPointerMsg{target: "close"} },
	)
	return pointer.Surface{Content: strings.Join(lines, "\n"), Pointer: hitMap.Handler()}
}

func (m *settingsModel) renderForgeRow(row *integrationSettingsRow, width int) []settingsRenderRow {
	prefix := "forge:" + row.id + ":"
	marker := "new"
	if row.persisted {
		marker = "saved"
	}
	rowTarget := prefix + "kind"
	if row.persisted {
		rowTarget = prefix + "base"
	}
	lines := []settingsRenderRow{
		{line: ""},
		{line: settingsFit("-- "+row.name.Value()+" ("+marker+") --", width), target: rowTarget},
	}
	if row.persisted {
		lines = append(lines,
			settingsRenderRow{line: settingsFit("  Name: "+row.name.Value()+" (locked)", width)},
			settingsRenderRow{line: settingsFit("  Kind: "+row.kind+" (locked)", width)},
		)
	} else {
		lines = append(lines,
			settingsRenderRow{line: m.inputLine(prefix+"kind", "Kind", row.kind, width), target: prefix + "kind"},
			settingsRenderRow{line: m.inputModelLine(prefix+"name", "Name", row.name, false, width), target: prefix + "name"},
		)
	}
	lines = append(lines,
		settingsRenderRow{line: m.inputModelLine(prefix+"base", "Base URL", row.baseURL, false, width), target: prefix + "base"},
		settingsRenderRow{line: m.inputModelLine(prefix+"project", "Project", row.project, false, width), target: prefix + "project"},
		settingsRenderRow{line: m.inputModelLine(prefix+"token", keyLabel("Token", row.hasToken), row.token, true, width), target: prefix + "token"},
		settingsRenderRow{line: m.actionLine(prefix+"test", "Test", width), target: prefix + "test"},
	)
	remove := "Remove"
	if m.armedRemove == row.id {
		remove = "Confirm remove"
	}
	return append(lines,
		settingsRenderRow{line: m.actionLine(prefix+"save", "Save", width), target: prefix + "save"},
		settingsRenderRow{line: m.actionLine(prefix+"remove", remove, width), target: prefix + "remove"},
	)
}

func settingsInputDisplay(input textinput.Model, secret, focused bool, width int) string {
	value := input.Value()
	if value == "" {
		value = input.Placeholder
	}
	raw := []rune(value)
	position := min(max(input.Position(), 0), len(raw))
	safe := sanitizeTerminal(value)
	safePosition := utf8.RuneCountInString(sanitizeTerminal(string(raw[:position])))
	safePosition = min(safePosition, utf8.RuneCountInString(safe))
	if secret && input.Value() != "" {
		safe = strings.Repeat("*", max(utf8.RuneCountInString(safe), 1))
		safePosition = min(safePosition, utf8.RuneCountInString(safe))
	}
	if !focused {
		return ansi.Truncate(safe, max(width, 0), "")
	}
	return settingsCursorViewport(safe, safePosition, width)
}

func settingsCursorViewport(value string, position, width int) string {
	if width <= 0 {
		return ""
	}
	const cursor = "|"
	if width == 1 {
		return cursor
	}
	runes := []rune(value)
	position = min(max(position, 0), len(runes))
	before, after := string(runes[:position]), string(runes[position:])
	contentWidth := width - 1
	cursorColumn := ansi.StringWidth(before)
	left := max(cursorColumn-contentWidth, 0)
	visibleBefore := ansi.Cut(before, left, cursorColumn)
	remaining := max(contentWidth-ansi.StringWidth(visibleBefore), 0)
	visibleAfter := ansi.Truncate(after, remaining, "")
	return visibleBefore + cursor + visibleAfter
}

func keyLabel(label string, saved bool) string {
	if saved {
		return label + " (saved)"
	}
	return label
}

func (m *settingsModel) inputLine(target, label, value string, width int) string {
	marker := "  "
	if m.focus == target {
		marker = "> "
	}
	return settingsFit(marker+label+": "+value, width)
}

func (m *settingsModel) inputModelLine(
	target, label string,
	input textinput.Model,
	secret bool,
	width int,
) string {
	marker := "  "
	if m.focus == target {
		marker = "> "
	}
	prefix := marker + label + ": "
	available := max(width-ansi.StringWidth(prefix), 1)
	value := settingsInputDisplay(input, secret, m.focus == target, available)
	return settingsFit(prefix+value, width)
}

func (m *settingsModel) actionLine(target, label string, width int) string {
	marker := "  "
	if m.focus == target {
		marker = "> "
	}
	return settingsFit(marker+"["+label+"]", width)
}

func settingsFit(line string, width int) string {
	return ansi.Truncate(sanitizeTerminal(line), max(width, 0), "")
}

func sanitizeTerminal(value string) string {
	value = ansi.Strip(value)
	return strings.Map(func(r rune) rune {
		if r <= 0x1f || (r >= 0x7f && r <= 0x9f) {
			return -1
		}
		return r
	}, value)
}

func isSettingsMessage(message tea.Msg) bool {
	switch message.(type) {
	case settingsLoadedMsg, aiSettingsTestedMsg, aiSettingsSavedMsg,
		forgeSettingsTestedMsg, forgeSettingsSavedMsg, forgeSettingsRemovedMsg,
		settingsPointerMsg, settingsWheelMsg:
		return true
	default:
		return false
	}
}
