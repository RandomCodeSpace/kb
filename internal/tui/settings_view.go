package tui

import (
	"strings"
	"unicode/utf8"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func (m *settingsModel) View(width, height int) string {
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
	body := []string{"", "AI SETTINGS"}
	if !m.loaded {
		body = append(body, "loading settings...")
	} else {
		body = append(body,
			m.inputLine("ai:base", "Base URL", settingsInputDisplay(m.aiBase, false), width),
			m.inputLine("ai:model", "Model", settingsInputDisplay(m.aiModel, false), width),
			m.inputLine("ai:key", keyLabel("API key", m.hasKey), settingsInputDisplay(m.aiKey, true), width),
			m.actionLine("ai:test", "Test connection", width),
			m.actionLine("ai:save", "Save AI settings", width),
			"", "FORGE INTEGRATIONS",
		)
		if len(m.rows) == 0 {
			body = append(body, "(none configured)")
		}
		for i := range m.rows {
			body = append(body, m.renderForgeRow(&m.rows[i], width)...)
		}
		body = append(body, m.actionLine("forge:add", "+ Add integration", width))
	}
	status := m.status
	if status == "" {
		status = "ready"
	}
	if m.statusIsError {
		status = "error: " + status
	}
	footer := settingsFit(status+" | tab navigate | enter act | esc back", width)

	bodyHeight := height - 2
	focusLine := -1
	for i, line := range body {
		if strings.HasPrefix(line, ">") {
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
	for _, line := range visible {
		lines = append(lines, settingsFit(line, width))
	}
	lines = append(lines, footer)
	return strings.Join(lines, "\n")
}

func (m *settingsModel) renderForgeRow(row *integrationSettingsRow, width int) []string {
	prefix := "forge:" + row.id + ":"
	marker := "new"
	if row.persisted {
		marker = "saved"
	}
	lines := []string{"", settingsFit("-- "+row.name.Value()+" ("+marker+") --", width)}
	if row.persisted {
		lines = append(lines,
			settingsFit("  Name: "+row.name.Value()+" (locked)", width),
			settingsFit("  Kind: "+row.kind+" (locked)", width),
		)
	} else {
		lines = append(lines,
			m.inputLine(prefix+"kind", "Kind", row.kind, width),
			m.inputLine(prefix+"name", "Name", settingsInputDisplay(row.name, false), width),
		)
	}
	lines = append(lines,
		m.inputLine(prefix+"base", "Base URL", settingsInputDisplay(row.baseURL, false), width),
		m.inputLine(prefix+"project", "Project", settingsInputDisplay(row.project, false), width),
		m.inputLine(prefix+"token", keyLabel("Token", row.hasToken), settingsInputDisplay(row.token, true), width),
		m.actionLine(prefix+"test", "Test", width),
	)
	remove := "Remove"
	if m.armedRemove == row.id {
		remove = "Confirm remove"
	}
	return append(lines,
		m.actionLine(prefix+"save", "Save", width),
		m.actionLine(prefix+"remove", remove, width),
	)
}

func settingsInputDisplay(input textinput.Model, secret bool) string {
	value := input.Value()
	if value == "" {
		return sanitizeTerminal(input.Placeholder)
	}
	safe := sanitizeTerminal(value)
	if !secret {
		return safe
	}
	return strings.Repeat("*", max(utf8.RuneCountInString(safe), 1))
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
		forgeSettingsTestedMsg, forgeSettingsSavedMsg, forgeSettingsRemovedMsg:
		return true
	default:
		return false
	}
}
