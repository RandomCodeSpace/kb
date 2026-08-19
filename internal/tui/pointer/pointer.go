// Package pointer maps rendered terminal cells to pointer interactions.
package pointer

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// Point is a zero-based terminal cell.
type Point struct {
	X int
	Y int
}

// Surface couples rendered content with the pointer map from that render pass.
type Surface struct {
	Content string
	Pointer func(tea.MouseMsg) tea.Cmd
}

// Rect is a half-open rectangle in zero-based terminal cells.
type Rect struct {
	X0 int
	Y0 int
	X1 int
	Y1 int
}

// Viewport projects logical content rows into a visible terminal rectangle.
type Viewport struct {
	Rect   Rect
	Scroll int
}

// Action builds the model message produced by one pointer activation.
type Action func(Point) tea.Msg

// ControlID is a stable identifier for one rendered control.
type ControlID string

// State tracks transient pointer feedback independently from domain state.
type State struct {
	pressed ControlID
}

// IsPressed reports whether the identified control owns the active pointer
// press. Callers use this while rendering the control's pressed style.
func (s State) IsPressed(id ControlID) bool {
	return id != "" && s.pressed == id
}

// Active reports whether any rendered control owns the current press.
func (s State) Active() bool { return s.pressed != "" }

// Render applies same-width reverse-video feedback to the active control.
// The caller supplies already-sanitized terminal content.
//
// A themed control renders its own styles inside this run, and every one of
// them ends in a reset that would cancel the feedback for the rest of the row,
// so the attribute is re-armed after each reset.
func (s State) Render(id ControlID, content string) string {
	if !s.IsPressed(id) {
		return content
	}
	rearmed := strings.ReplaceAll(content, "\x1b[m", "\x1b[m\x1b[7m")
	rearmed = strings.ReplaceAll(rearmed, "\x1b[0m", "\x1b[0m\x1b[7m")
	return "\x1b[7m" + rearmed + "\x1b[27m"
}

// Update consumes pointer feedback messages. The returned command emits the
// control's domain message after release feedback has been cleared.
func (s State) Update(message tea.Msg) (State, tea.Cmd, bool) {
	event, ok := message.(interactionMsg)
	if !ok {
		return s, nil, false
	}
	switch event.kind {
	case interactionPress:
		s.pressed = event.id
		return s, nil, true
	case interactionRelease:
		matched := s.pressed != "" && s.pressed == event.id
		s.pressed = ""
		if !matched || event.activate == nil {
			return s, nil, true
		}
		result := event.activate(event.point)
		if result == nil {
			return s, nil, true
		}
		return s, func() tea.Msg { return result }, true
	default:
		s.pressed = ""
		if event.followup == nil {
			return s, nil, true
		}
		return s, func() tea.Msg { return event.followup }, true
	}
}

type interactionKind uint8

const (
	interactionPress interactionKind = iota + 1
	interactionRelease
	interactionCancel
)

type interactionMsg struct {
	kind     interactionKind
	id       ControlID
	point    Point
	activate Action
	followup tea.Msg
}

// IsMessage reports whether message carries pointer feedback that State.Update
// must consume before an active overlay routes domain messages.
func IsMessage(message tea.Msg) bool {
	_, ok := message.(interactionMsg)
	return ok
}

// Cancel clears any active press when the original control disappeared between
// render passes, for example after a resize or asynchronous refresh.
func Cancel() tea.Cmd { return cancelCommand(nil) }

type region struct {
	rect   Rect
	id     ControlID
	action Action
}

type wheelRegion struct {
	rect   Rect
	action func(int) tea.Msg
}

// Map resolves rendered terminal regions to model messages.
type Map struct {
	regions []region
	wheels  []wheelRegion
}

type handlerSnapshot struct {
	regions []region
	wheels  []wheelRegion
	tracked bool
	pressed int
	dragged bool
}

// Add registers an action region. Later regions take precedence when they overlap.
func (m *Map) Add(rect Rect, action Action) {
	if rect.empty() || action == nil {
		return
	}
	m.regions = append(m.regions, region{rect: rect, action: action})
}

// AddControl registers an action region with opt-in pressed feedback. IDs must
// remain stable across render passes. An empty ID retains Add's legacy behavior.
func (m *Map) AddControl(id ControlID, rect Rect, action Action) {
	if rect.empty() || action == nil {
		return
	}
	m.regions = append(m.regions, region{rect: rect, id: id, action: action})
}

// AddBackdrop registers the portion of bounds outside pane as one action.
func (m *Map) AddBackdrop(bounds, pane Rect, action Action) {
	if bounds.empty() || action == nil {
		return
	}
	pane = pane.intersect(bounds)
	if pane.empty() {
		m.Add(bounds, action)
		return
	}
	m.Add(Rect{X0: bounds.X0, Y0: bounds.Y0, X1: bounds.X1, Y1: pane.Y0}, action)
	m.Add(Rect{X0: bounds.X0, Y0: pane.Y1, X1: bounds.X1, Y1: bounds.Y1}, action)
	m.Add(Rect{X0: bounds.X0, Y0: pane.Y0, X1: pane.X0, Y1: pane.Y1}, action)
	m.Add(Rect{X0: pane.X1, Y0: pane.Y0, X1: bounds.X1, Y1: pane.Y1}, action)
}

// AddWheel registers a wheel zone. The action receives -1 for up and +1 for down.
func (m *Map) AddWheel(rect Rect, action func(delta int) tea.Msg) {
	if rect.empty() || action == nil {
		return
	}
	m.wheels = append(m.wheels, wheelRegion{rect: rect, action: action})
}

// Handler returns the immutable render snapshot's mouse callback.
func (m Map) Handler() func(tea.MouseMsg) tea.Cmd {
	snapshot := &handlerSnapshot{
		regions: append([]region(nil), m.regions...),
		wheels:  append([]wheelRegion(nil), m.wheels...),
		pressed: -1,
	}
	for _, candidate := range snapshot.regions {
		if candidate.id != "" {
			snapshot.tracked = true
			break
		}
	}
	return snapshot.handle
}

func (h *handlerSnapshot) handle(message tea.MouseMsg) tea.Cmd {
	mouse := message.Mouse()
	point := Point{X: mouse.X, Y: mouse.Y}
	if _, wheel := message.(tea.MouseWheelMsg); wheel {
		return h.handleWheel(mouse, point)
	}
	switch message.(type) {
	case tea.MouseClickMsg:
		return h.handleClick(mouse, point)
	case tea.MouseMotionMsg:
		return h.handleMotion(mouse)
	case tea.MouseReleaseMsg:
		return h.handleRelease(mouse, point)
	default:
		return nil
	}
}

func (h *handlerSnapshot) handleWheel(mouse tea.Mouse, point Point) tea.Cmd {
	h.resetGesture()
	delta, valid := wheelDelta(mouse.Button)
	if !valid {
		return h.cancelTracked(nil)
	}
	for index := len(h.wheels) - 1; index >= 0; index-- {
		candidate := h.wheels[index]
		if candidate.action == nil || !candidate.rect.contains(point) {
			continue
		}
		result := candidate.action(delta)
		if h.tracked {
			return cancelCommand(result)
		}
		return messageCommand(result)
	}
	return h.cancelTracked(nil)
}

func (h *handlerSnapshot) handleClick(mouse tea.Mouse, point Point) tea.Cmd {
	h.resetGesture()
	if mouse.Button == tea.MouseLeft {
		h.pressed = h.hit(point)
	}
	if h.pressed >= 0 && h.regions[h.pressed].id != "" {
		return pressCommand(h.regions[h.pressed].id)
	}
	if h.pressed < 0 {
		return h.cancelTracked(nil)
	}
	return nil
}

func (h *handlerSnapshot) handleMotion(mouse tea.Mouse) tea.Cmd {
	if mouse.Button != tea.MouseLeft {
		return nil
	}
	if h.pressed >= 0 {
		h.dragged = true
	}
	return h.cancelTracked(nil)
}

func (h *handlerSnapshot) handleRelease(mouse tea.Mouse, point Point) tea.Cmd {
	if mouse.Button != tea.MouseLeft && mouse.Button != tea.MouseNone {
		h.resetGesture()
		return h.cancelTracked(nil)
	}
	pressedIndex := h.pressed
	wasDragged := h.dragged
	h.resetGesture()
	if wasDragged {
		return h.cancelTracked(nil)
	}
	releaseIndex := h.hit(point)
	if h.tracked && releaseIndex >= 0 && h.regions[releaseIndex].id != "" {
		candidate := h.regions[releaseIndex]
		return releaseCommand(candidate.id, point, candidate.action)
	}
	if h.tracked && pressedIndex < 0 {
		return cancelCommand(nil)
	}
	if pressedIndex < 0 || releaseIndex != pressedIndex {
		return nil
	}
	return messageCommand(h.regions[pressedIndex].action(point))
}

func (h *handlerSnapshot) cancelTracked(followup tea.Msg) tea.Cmd {
	if !h.tracked {
		return messageCommand(followup)
	}
	return cancelCommand(followup)
}

func (h *handlerSnapshot) hit(point Point) int {
	for index := len(h.regions) - 1; index >= 0; index-- {
		candidate := h.regions[index]
		if candidate.action != nil && candidate.rect.contains(point) {
			return index
		}
	}
	return -1
}

func (h *handlerSnapshot) resetGesture() {
	h.pressed = -1
	h.dragged = false
}

func wheelDelta(button tea.MouseButton) (int, bool) {
	switch button {
	case tea.MouseWheelUp:
		return -1, true
	case tea.MouseWheelDown:
		return 1, true
	default:
		return 0, false
	}
}

func messageCommand(message tea.Msg) tea.Cmd {
	if message == nil {
		return nil
	}
	return func() tea.Msg { return message }
}

func pressCommand(id ControlID) tea.Cmd {
	return func() tea.Msg {
		return interactionMsg{kind: interactionPress, id: id}
	}
}

func releaseCommand(id ControlID, point Point, action Action) tea.Cmd {
	return func() tea.Msg {
		return interactionMsg{kind: interactionRelease, id: id, point: point, activate: action}
	}
}

func cancelCommand(followup tea.Msg) tea.Cmd {
	return func() tea.Msg {
		return interactionMsg{kind: interactionCancel, followup: followup}
	}
}

// Row projects one logical content-row span into the viewport and clips it.
func (v Viewport) Row(logicalRow, x0, x1 int) (Rect, bool) {
	row := Rect{
		X0: v.Rect.X0 + x0,
		Y0: v.Rect.Y0 + logicalRow - v.Scroll,
		X1: v.Rect.X0 + x1,
		Y1: v.Rect.Y0 + logicalRow - v.Scroll + 1,
	}.intersect(v.Rect)
	if row.empty() {
		return Rect{}, false
	}
	return row, true
}

func (r Rect) empty() bool {
	return r.X0 >= r.X1 || r.Y0 >= r.Y1
}

func (r Rect) contains(point Point) bool {
	return point.X >= r.X0 && point.X < r.X1 && point.Y >= r.Y0 && point.Y < r.Y1
}

func (r Rect) intersect(other Rect) Rect {
	return Rect{
		X0: max(r.X0, other.X0),
		Y0: max(r.Y0, other.Y0),
		X1: min(r.X1, other.X1),
		Y1: min(r.Y1, other.Y1),
	}
}
