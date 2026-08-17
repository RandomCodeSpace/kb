package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func (m *settingsModel) View(width, height int) string {
	width = max(width, 1)
	height = max(height, 6)
	inputWidth := max(width-18, 8)
	m.aiBase.SetWidth(inputWidth)
	m.aiModel.SetWidth(inputWidth)
	m.aiKey.SetWidth(inputWidth)
	for i := range m.rows {
		m.rows[i].name.SetWidth(inputWidth)
		m.rows[i].baseURL.SetWidth(inputWidth)
		m.rows[i].token.SetWidth(inputWidth)
	}

	lines := []string{settingsFit("kb / settings / "+m.user, width), "", "AI SETTINGS"}
	if !m.loaded {
		lines = append(lines, "loading settings...")
	} else {
		lines = append(lines,
			m.inputLine("ai:base", "Base URL", m.aiBase.View(), width),
			m.inputLine("ai:model", "Model", m.aiModel.View(), width),
			m.inputLine("ai:key", keyLabel("API key", m.hasKey), m.aiKey.View(), width),
			m.actionLine("ai:test", "Test connection", width),
			m.actionLine("ai:save", "Save AI settings", width),
			"", "FORGE INTEGRATIONS",
		)
		if len(m.rows) == 0 {
			lines = append(lines, "(none configured)")
		}
		for i := range m.rows {
			lines = append(lines, m.renderForgeRow(&m.rows[i], width)...)
		}
		lines = append(lines, m.actionLine("forge:add", "+ Add integration", width))
	}
	status := m.status
	if status == "" {
		status = "ready"
	}
	if m.statusIsError {
		status = "error: " + status
	}
	footer := settingsFit(status+" | tab navigate | enter act | esc back", width)
	if len(lines)+1 > height {
		lines = lines[:max(height-1, 1)]
	}
	return strings.Join(append(lines, footer), "\n")
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
			m.inputLine(prefix+"name", "Name", row.name.View(), width),
		)
	}
	lines = append(lines,
		m.inputLine(prefix+"base", "Base URL", row.baseURL.View(), width),
		m.inputLine(prefix+"token", keyLabel("Token", row.hasToken), row.token.View(), width),
	)
	if row.persisted {
		lines = append(lines, m.actionLine(prefix+"test", "Test", width))
	}
	remove := "Remove"
	if m.armedRemove == row.id {
		remove = "Confirm remove"
	}
	return append(lines,
		m.actionLine(prefix+"save", "Save", width),
		m.actionLine(prefix+"remove", remove, width),
	)
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
	return ansi.Truncate(line, max(width, 0), "")
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
