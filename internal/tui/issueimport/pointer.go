package issueimport

import (
	"strings"

	tea "charm.land/bubbletea/v2"
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

// MouseHandler returns a release-only immutable map derived from the rendered
// frame. Every target comes from the row that carries it, never from matching
// the rendered text: a forge draft's title is untrusted and must not be able to
// impersonate a control.
func (m Model) MouseHandler(width, height int) func(tea.MouseMsg) tea.Cmd {
	if !m.open {
		return nil
	}
	width, height = max(width, 1), max(height, 1)
	frame := m.layout(width, height)
	inset := m.themeStyles().Metrics.OverlayInsetX
	var hitMap pointer.Map
	body := max(frame.height-2, 0)
	for index, row := range frame.rows[:min(body, len(frame.rows))] {
		y := frame.y + 1 + index
		if row.target != "" {
			addPointerRegion(&hitMap, pointer.Rect{
				X0: frame.x, Y0: y, X1: frame.x + frame.width, Y1: y + 1,
			}, row.target, m, width, height)
		}
		for _, button := range row.buttons {
			addPointerRegion(&hitMap, pointer.Rect{
				X0: frame.x + inset + button.x0, Y0: y,
				X1: frame.x + inset + button.x0 + ansi.StringWidth(button.label), Y1: y + 1,
			}, button.target, m, width, height)
		}
	}
	pane := pointer.Rect{X0: frame.x, Y0: frame.y, X1: frame.x + frame.width, Y1: frame.y + frame.height}
	if m.operation == "" && m.pointerBackdropSafe() {
		hitMap.AddBackdrop(pointer.Rect{X1: width, Y1: height}, pane, func(pointer.Point) tea.Msg {
			return pointerActionMsg{target: "backdrop", session: m.session, generation: m.generation}
		})
	}
	if m.stage == stageReview && m.operation == "" {
		maxScroll := max(len(m.rows)-reviewLimit(max(frame.height-2, 1)), 0)
		hitMap.AddWheel(pane, func(delta int) tea.Msg {
			return pointerActionMsg{target: "scroll", session: m.session, generation: m.generation, scrollDelta: delta * 3, maxScroll: maxScroll}
		})
	}
	return hitMap.Handler()
}

func (m Model) pointerBackdropSafe() bool {
	return m.stage == stageInput && strings.TrimSpace(m.ref.Value()) == "" && m.source == 0 && m.max == defaultMax
}

func addPointerRegion(hitMap *pointer.Map, rect pointer.Rect, target string, m Model, width, height int) {
	width, height = max(width, 1), max(height, 1)
	rect.X0 = max(rect.X0, 0)
	rect.Y0 = max(rect.Y0, 0)
	rect.X1 = min(rect.X1, width)
	rect.Y1 = min(rect.Y1, height)
	if rect.X0 >= rect.X1 || rect.Y0 >= rect.Y1 {
		return
	}
	session, generation := m.session, m.generation
	hitMap.AddControl(controlID(target), rect, func(pointer.Point) tea.Msg {
		return pointerActionMsg{target: target, session: session, generation: generation}
	})
}

func controlID(target string) pointer.ControlID { return pointer.ControlID("issueimport." + target) }
