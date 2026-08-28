package cmdpalette

import (
	"strconv"

	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/tui/pointer"
)

// rowPrefix keys one result row's control id. Spec section 10.5.3: hoverable is
// opt-in on the region that already exists, so the id a row registers for its
// click is the same id hover resolves against, and the machine of section 10.5.2
// reads the row index straight back out of it.
const rowPrefix = "cmdpalette.row."

// controlID is the stable id of one result row, keyed by its index into the
// filtered entries rather than by its display line: a section band shifts the
// display line without changing which action the row runs.
func controlID(entry int) pointer.ControlID {
	return pointer.ControlID(rowPrefix + strconv.Itoa(entry))
}

// pointerActionMsg is the palette's own pointer activation. It carries the
// generation the map was built in, so a release resolved against a frame the
// query has since re-filtered runs nothing rather than the wrong action.
type pointerActionMsg struct {
	entry      int
	dismiss    bool
	generation uint64
}

type pointerWheelMsg struct {
	generation uint64
	current    int
	target     int
	max        int
}

func (m pointerWheelMsg) PointerWheelIntent() pointer.WheelIntent {
	return pointer.WheelIntent{Key: "palette", Current: m.current, Target: m.target, Min: 0, Max: m.max}
}

func (m pointerWheelMsg) PointerWheelTarget(target int) tea.Msg {
	m.target = min(max(target, 0), m.max)
	return m
}

// machine is the mouse-mode state machine of spec section 10.5.2 for this
// surface. The palette's result list is an overlay choice surface, which is
// exactly the scope ratified call 9 gives the machine.
func (m Model) machine() pointer.Choice {
	return pointer.Choice{Cursor: m.cursor, Rows: pointer.RowsWithPrefix(rowPrefix)}
}

// acting is the row that renders the cursor cue: the hovered row while mouse
// mode is on, the keyboard cursor otherwise.
func (m Model) acting() int { return m.machine().Acting(m.pointerState) }

// MouseHandler returns the immutable pointer map of the frame just rendered.
// Every region is derived from the row that drew it, never from matching
// rendered text: an action's name is a table value and must not be able to
// address a control by spelling one.
//
// The panel's surround is the palette's own backdrop rather than a hole, per
// spec section 10.5.3: an overlay whose panel does not contain the point still
// consumes the motion - clearing its own hover - so the board underneath can
// never light a card beneath a dimmed backdrop.
func (m Model) MouseHandler(width, height int) func(tea.MouseMsg) tea.Cmd {
	return m.PointerSurface(width, height).Pointer
}

// PointerSurface publishes the rendered handler together with its immutable
// stable-control topology for root-level stale-generation admission.
func (m Model) PointerSurface(width, height int) pointer.Surface {
	if !m.open {
		return pointer.Surface{}
	}
	width, height = max(width, 1), max(height, 1)
	panel := m.layout(width, height)
	bounds := pointer.Rect{X1: width, Y1: height}
	pane := pointer.Rect{
		X0: panel.x, Y0: panel.y,
		X1: min(panel.x+panel.width, width), Y1: min(panel.y+panel.height, height),
	}
	var hitMap pointer.Map
	// A click outside the panel dismisses. Nothing is lost by it: opening the
	// palette is always a fresh search, so the query a dismissal drops is the
	// same query the next open would have cleared anyway.
	generation := m.generation
	hitMap.AddBackdropControl(pointer.ControlID("palette.backdrop"), bounds, pane, func(pointer.Point) tea.Msg {
		return pointerActionMsg{dismiss: true, generation: generation}
	})
	// Body row 0 is the query field, which is not activatable and so, per spec
	// section 10.5.1, not hoverable either. The result rows follow it.
	for index, row := range m.visibleRows(panel) {
		if row.kind != rowEntry {
			continue
		}
		// A row clipped away by a frame too small to hold it registers nothing:
		// Map.AddControl drops an empty rect, which is the one place that rule
		// needs to live.
		rect := pointer.Rect{
			X0: max(panel.x, 0), Y0: max(panel.y+2+index, pane.Y0),
			X1: pane.X1, Y1: min(panel.y+3+index, pane.Y1),
		}
		entry := row.entry
		hitMap.AddControl(controlID(entry), rect, func(pointer.Point) tea.Msg {
			return pointerActionMsg{entry: entry, generation: generation}
		})
	}
	maxEntry := max(len(m.entries)-1, 0)
	hitMap.AddWheel(pane, func(delta int) tea.Msg {
		return pointerWheelMsg{generation: generation, current: m.cursor,
			target: min(max(m.cursor+delta, 0), maxEntry), max: maxEntry}
	})
	return pointer.Surface{Pointer: hitMap.Handler(), Topology: hitMap.Topology()}
}

// PointerSession identifies the current filtered palette owner. Query changes
// advance it because row identities may be replaced while the palette stays open.
func (m Model) PointerSession() uint64 { return m.generation }

// pointerAction runs one activation from the map of the frame it was built in.
func (m *Model) pointerAction(msg pointerActionMsg) tea.Cmd {
	if msg.generation != m.generation {
		return nil
	}
	if msg.dismiss {
		m.Close()
		return nil
	}
	if msg.entry < 0 || msg.entry >= len(m.entries) {
		return nil
	}
	m.cursor = msg.entry
	return m.choose()
}
