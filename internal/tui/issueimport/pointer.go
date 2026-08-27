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
	scroll      int
	scrollDelta int
	maxScroll   int
}

func (m pointerActionMsg) PointerWheelIntent() pointer.WheelIntent {
	return pointer.WheelIntent{Key: "import", Current: m.scroll,
		Target: min(max(m.scroll+m.scrollDelta, 0), m.maxScroll), Min: 0, Max: m.maxScroll}
}

func (m pointerActionMsg) PointerWheelTarget(target int) tea.Msg {
	target = min(max(target, 0), m.maxScroll)
	m.scrollDelta = target - m.scroll
	return m
}

// MouseHandler returns a release-only immutable map derived from the rendered
// frame. Every target comes from the row that carries it, never from matching
// the rendered text: a forge draft's title is untrusted and must not be able to
// impersonate a control.
func (m *Model) MouseHandler(width, height int) func(tea.MouseMsg) tea.Cmd {
	return m.PointerSurface(width, height).Pointer
}

// PointerSurface publishes the rendered handler together with its immutable
// stable-control topology for root-level stale-generation admission.
func (m *Model) PointerSurface(width, height int) pointer.Surface {
	if !m.open {
		return pointer.Surface{}
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
			}, row.target, *m, width, height)
		}
		for _, button := range row.buttons {
			addPointerRegion(&hitMap, pointer.Rect{
				X0: frame.x + inset + button.x0, Y0: y,
				X1: frame.x + inset + button.x0 + ansi.StringWidth(button.label), Y1: y + 1,
			}, button.target, *m, width, height)
		}
	}
	pane := pointer.Rect{X0: frame.x, Y0: frame.y, X1: frame.x + frame.width, Y1: frame.y + frame.height}
	if m.operation == "" && m.pointerBackdropSafe() {
		hitMap.AddBackdropControl(controlID("backdrop"),
			pointer.Rect{X1: width, Y1: height}, pane, func(pointer.Point) tea.Msg {
				return pointerActionMsg{target: "backdrop", session: m.session, generation: m.generation}
			})
	}
	if m.stage == stageReview && m.operation == "" {
		maxScroll := max(len(m.rows)-reviewLimit(max(frame.height-2, 1)), 0)
		hitMap.AddWheel(pane, func(delta int) tea.Msg {
			return pointerActionMsg{target: "scroll", session: m.session, generation: m.generation,
				scroll: m.scroll, scrollDelta: delta * 3, maxScroll: maxScroll}
		})
	}
	// Rows 6 and 9 of spec section 10.5.2: the pointer can stand still while the
	// content moves under it, so a wheel scroll or a resize re-resolves hover
	// from the retained point against the map this frame just built. A point
	// that no longer lands on a region clears hover and mouse mode with it.
	m.pointerState = m.pointerState.Reresolve(hitMap)
	return pointer.Surface{Pointer: hitMap.Handler(), Topology: hitMap.Topology()}
}

// PointerSession identifies this open import owner across harmless renders.
func (m Model) PointerSession() uint64 { return m.session }

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
