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
	target      string
	session     uint64
	generation  uint64
	scrollDelta int
	maxScroll   int
}

// MouseHandler returns a release-only immutable map derived from View.
func (m Model) MouseHandler(width, height int) func(tea.MouseMsg) tea.Cmd {
	if !m.open {
		return nil
	}
	view := ansi.Strip(m.View(width, height))
	viewLines := strings.Split(view, "\n")
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
		start, end := m.reviewWindow(max(1, min(height-10, 12)))
		for index := start; index < end; index++ {
			addPointerRegion(&hitMap, pointer.Rect{X0: x0, Y0: y0 + line, X1: x0 + frameWidth, Y1: y0 + line + 1}, "row:"+strconv.Itoa(index), m, width, height)
			line++
			if m.rows[index].err != "" {
				line++
			}
		}
	}
	pane := pointer.Rect{X0: x0, Y0: y0, X1: x0 + frameWidth, Y1: y0 + frameHeight}
	if m.operation == "" && m.pointerBackdropSafe() {
		hitMap.AddBackdrop(pointer.Rect{X1: max(width, 1), Y1: max(height, 1)}, pane, func(pointer.Point) tea.Msg {
			return pointerActionMsg{target: "backdrop", session: m.session, generation: m.generation}
		})
	}
	if m.stage == stageReview && m.operation == "" {
		limit := max(1, min(height-10, 12))
		maxScroll := max(len(m.rows)-limit, 0)
		hitMap.AddWheel(pane, func(delta int) tea.Msg {
			return pointerActionMsg{target: "scroll", session: m.session, generation: m.generation, scrollDelta: delta * 3, maxScroll: maxScroll}
		})
	}
	for line, text := range viewLines {
		content := strings.TrimSpace(strings.Trim(text, "│"))
		if strings.HasPrefix(content, "> ") || strings.HasPrefix(content, "! ") {
			content = strings.TrimSpace(content[2:])
		}
		if m.stage == stageInput {
			target := ""
			switch {
			case strings.HasPrefix(content, "source  "):
				target = "source"
			case strings.HasPrefix(content, "ref     "):
				target = "ref"
			case strings.HasPrefix(content, "max     "):
				target = "max"
			}
			if target != "" {
				addPointerRegion(&hitMap, pointer.Rect{X0: x0, Y0: y0 + line, X1: x0 + frameWidth, Y1: y0 + line + 1}, target, m, width, height)
			}
		}
		if !strings.HasPrefix(content, "[ Import ]") && !strings.HasPrefix(content, "[>Import<]") {
			continue
		}
		for _, control := range []struct {
			label  string
			target string
		}{{"Import", "import"}, {"Back", "back"}, {"Close", "close"}, {"Cancel", "cancel"}} {
			for _, needle := range []string{"[ " + control.label + " ]", "[>" + control.label + "<]"} {
				if start := strings.Index(text, needle); start >= 0 {
					addPointerRegion(&hitMap, pointer.Rect{X0: x0 + start, Y0: y0 + line, X1: x0 + start + ansi.StringWidth(needle), Y1: y0 + line + 1}, control.target, m, width, height)
				}
			}
		}
	}
	return hitMap.Handler()
}

func (m Model) pointerBackdropSafe() bool {
	return m.stage == stageInput && strings.TrimSpace(m.ref.Value()) == "" && m.source == 0 && m.max == defaultMax
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
	hitMap.AddControl(controlID(target), rect, func(pointer.Point) tea.Msg {
		return pointerActionMsg{target: target, session: session, generation: generation}
	})
}

func controlID(target string) pointer.ControlID { return pointer.ControlID("issueimport." + target) }
