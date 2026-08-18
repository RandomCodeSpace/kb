package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

func (m Model) keyboardHelpOverlay(background string) string {
	width := max(m.width, 1)
	height := max(m.height, 1)
	if width < 4 || height < 3 {
		return background
	}
	lines := []string{
		"Keyboard help",
		"",
		"enter  open card        space  lift or drop card",
		"j/k    select card      h/l    select column",
		"1-4    jump to column",
		"",
		"/      text filter      f      label filter",
		"X      clear filter",
		"",
		"t      ship card        x      cancel card",
		"r      restore card     D      permanently delete",
	}
	if m.editor.Enabled() {
		lines = append(lines, "", "n      new card         e      edit card")
	}
	if m.settingsNew != nil {
		lines = append(lines, "s      settings")
	}
	if m.adr.Enabled() {
		lines = append(lines, "a      split ADR")
	}
	if m.issueImport.Enabled() {
		lines = append(lines, "i      import forge issue")
	}
	lines = append(lines, "", "? or esc  close help", "q         quit")

	innerWidth := min(max(width-4, 1), 56)
	maxLines := max(height-2, 1)
	if len(lines) > maxLines {
		lines = append(lines[:maxLines-1], "esc  close help")
	}
	for index := range lines {
		lines[index] = fitLine(lines[index], innerWidth)
	}
	frame := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1).
		Width(innerWidth + 2).
		Render(strings.Join(lines, "\n"))
	return lipgloss.NewCompositor(
		lipgloss.NewLayer(background),
		lipgloss.NewLayer(frame).
			X(max((width-lipgloss.Width(frame))/2, 0)).
			Y(max((height-lipgloss.Height(frame))/2, 0)).
			Z(4),
	).Render()
}
