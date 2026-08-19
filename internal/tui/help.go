package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/pointer"
)

type helpClosedMsg struct{}

func (m Model) keyboardHelpOverlay(background string) string {
	return m.keyboardHelpSurface(background).Content
}

func (m Model) keyboardHelpSurface(background string) pointer.Surface {
	width := max(m.width, 1)
	height := max(m.height, 1)
	if width < 4 || height < 3 {
		return pointer.Surface{Content: background}
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
	closeID := pointer.ControlID("help:close")
	for index, line := range lines {
		if strings.Contains(line, "close help") {
			lines[index] = strings.Replace(line, "close help", m.pointerState.Render(m.themeStyles(), closeID, "close help"), 1)
		}
	}
	for index := range lines {
		lines[index] = fitLine(lines[index], innerWidth)
	}
	frame := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1).
		Width(innerWidth + 2).
		Render(strings.Join(lines, "\n"))
	x := max((width-lipgloss.Width(frame))/2, 0)
	y := max((height-lipgloss.Height(frame))/2, 0)
	content := lipgloss.NewCompositor(
		lipgloss.NewLayer(background),
		lipgloss.NewLayer(frame).
			X(x).
			Y(y).
			Z(4),
	).Render()
	var hits pointer.Map
	pane := pointer.Rect{X0: x, Y0: y, X1: x + lipgloss.Width(frame), Y1: y + lipgloss.Height(frame)}
	closeAction := func(pointer.Point) tea.Msg { return helpClosedMsg{} }
	hits.AddBackdrop(pointer.Rect{X1: width, Y1: height}, pane, closeAction)
	for row, line := range strings.Split(ansi.Strip(frame), "\n") {
		if index := strings.Index(line, "close help"); index >= 0 {
			start := ansi.StringWidth(line[:index])
			hits.AddControl(closeID, pointer.Rect{X0: x + start, Y0: y + row, X1: x + start + len("close help"), Y1: y + row + 1}, closeAction)
			break
		}
	}
	return pointer.Surface{Content: content, Pointer: hits.Handler()}
}
