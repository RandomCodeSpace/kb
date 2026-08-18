package issueimport

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/pointer"
)

type pointerActionMsg struct {
	target     string
	session    uint64
	generation uint64
}

// MouseHandler returns a release-only immutable map derived from View.
func (m Model) MouseHandler(width, height int) func(tea.MouseMsg) tea.Cmd {
	if !m.open {
		return nil
	}
	view := ansi.Strip(m.View(width, height))
	frameWidth, frameHeight := lipgloss.Width(view), lipgloss.Height(view)
	x0 := max((max(width, 1)-frameWidth)/2, 0)
	y0 := max((max(height, 1)-frameHeight)/2, 0)
	var hitMap pointer.Map
	if m.stage == stageReview {
		line := 2
		line++
		if m.preview.Note != "" {
			line++
		}
		line++
		start, end := rowWindow(len(m.rows), m.selection, max(1, min(height-10, 12)))
		for index := start; index < end; index++ {
			addPointerRegion(&hitMap, pointer.Rect{X0: x0, Y0: y0 + line, X1: x0 + frameWidth, Y1: y0 + line + 1}, "row:"+strconv.Itoa(index), m, width, height)
			line++
			if m.rows[index].err != "" {
				line++
			}
		}
	}
	for line, text := range strings.Split(view, "\n") {
		target := ""
		switch {
		case m.stage == stageInput && strings.Contains(text, "source"):
			target = "source"
		case m.stage == stageInput && strings.Contains(text, "ref"):
			target = "ref"
		case m.stage == stageInput && strings.Contains(text, "max"):
			target = "max"
		case strings.Contains(text, "[ Back ]"):
			if start := strings.Index(text, "[ Import ]"); start >= 0 {
				addPointerRegion(&hitMap, pointer.Rect{X0: x0 + start, Y0: y0 + line, X1: x0 + start + len("[ Import ]"), Y1: y0 + line + 1}, "import", m, width, height)
			}
			start := strings.Index(text, "[ Back ]")
			addPointerRegion(&hitMap, pointer.Rect{X0: x0 + start, Y0: y0 + line, X1: x0 + start + len("[ Back ]"), Y1: y0 + line + 1}, "back", m, width, height)
			continue
		case strings.Contains(text, "[ Import ]"):
			target = "import"
		}
		if target != "" {
			addPointerRegion(&hitMap, pointer.Rect{X0: x0, Y0: y0 + line, X1: x0 + frameWidth, Y1: y0 + line + 1}, target, m, width, height)
			continue
		}
	}
	return hitMap.Handler()
}

func addPointerRegion(hitMap *pointer.Map, rect pointer.Rect, target string, m Model, width, height int) {
	width, height = max(width, 1), max(height, 1)
	if rect.X0 < 0 {
		rect.X0 = 0
	}
	if rect.Y0 < 0 {
		rect.Y0 = 0
	}
	if rect.X1 > width {
		rect.X1 = width
	}
	if rect.Y1 > height {
		rect.Y1 = height
	}
	if rect.X0 >= rect.X1 || rect.Y0 >= rect.Y1 {
		return
	}
	session, generation := m.session, m.generation
	hitMap.Add(rect, func(pointer.Point) tea.Msg {
		return pointerActionMsg{target: target, session: session, generation: generation}
	})
}
