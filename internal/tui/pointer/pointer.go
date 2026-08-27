// Package pointer maps rendered terminal cells to pointer interactions.
package pointer

import (
	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// Point is a zero-based terminal cell.
type Point struct {
	X int
	Y int
}

// Surface couples rendered content with the pointer map from that render pass.
type Surface struct {
	Content  string
	Pointer  func(tea.MouseMsg) tea.Cmd
	Topology Topology
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
//
// Hover mirrors press: one control id and the cell it resolved from. Spec
// section 10.5.2: mouse mode is not a stored flag, it is the hovered id being
// set and resolving to one of a surface's own regions, so there is exactly one
// bit of state to clear and no second copy of the same fact to disagree with.
type State struct {
	pressed   ControlID
	hovered   ControlID
	hoverAt   Point
	haveHover bool
}

// IsPressed reports whether the identified control owns the active pointer
// press. Callers use this while rendering the control's pressed style.
func (s State) IsPressed(id ControlID) bool {
	return id != "" && s.pressed == id
}

// Active reports whether any rendered control owns the current press.
func (s State) Active() bool { return s.pressed != "" }

// ClearCapture cancels pressed feedback without changing the last hover
// observation. Root pointer admission uses it when raw correlation fails.
func (s State) ClearCapture() State {
	s.pressed = ""
	return s
}

// IsHovered reports whether the identified control is under the pointer.
// Callers use this while rendering the control's hovered style.
func (s State) IsHovered(id ControlID) bool {
	return id != "" && s.hovered == id
}

// Hovered returns the control under the pointer, empty when there is none.
func (s State) Hovered() ControlID { return s.hovered }

// HoverPoint returns the last observed pointer cell. The second result is false
// only before any hover observation; an observation that resolved to no control
// still retains its cell so stationary re-resolution can discover moved content.
func (s State) HoverPoint() (Point, bool) {
	if !s.haveHover {
		return Point{}, false
	}
	return s.hoverAt, true
}

// Pressed returns the stable identity that owns capture.
func (s State) Pressed() ControlID { return s.pressed }

// Hover sets the hovered control and the cell it resolved from. An empty id
// clears hover while retaining the point, which is what a motion onto an
// overlay's own backdrop reports.
func (s State) Hover(id ControlID, at Point) State {
	s.hovered = id
	s.hoverAt = at
	s.haveHover = true
	return s
}

// ClearHover turns mouse mode off for every surface. Spec section 10.5.2:
// turning mouse mode off is clearing hover, and that is the whole of it.
func (s State) ClearHover() State {
	s.hovered = ""
	return s
}

// ClearHoverObservation removes both hover feedback and the retained terminal
// cell. Admission uses it when raw input cannot be correlated with the frame
// that resolved it; keeping that cell would let a later render resurrect a
// hover the user never actually delivered.
func (s State) ClearHoverObservation() State {
	s.hovered = ""
	s.hoverAt = Point{}
	s.haveHover = false
	return s
}

// Reresolve re-derives hover from the retained point against a freshly built
// map. Rows 6 and 9 of spec section 10.5.2: the pointer can stand still while
// the content moves under it, so a scroll, a resize or a changed filter has to
// re-resolve rather than wait for a motion that never comes. A point that no
// longer lands on a hoverable region clears hover.
func (s State) Reresolve(m Map) State {
	point, ok := s.HoverPoint()
	if !ok {
		return s
	}
	id, hit := m.Resolve(point)
	if !hit {
		return s.ClearHover()
	}
	s.hovered = id
	return s
}

// Render applies same-width pressed feedback to the active control. The caller
// supplies already-sanitized terminal content.
//
// Spec section 9.1: the feedback is theme.Styles.Pressed, not a raw escape
// written here. The theme owns the attribute and the re-arming a composed run
// needs; this package only decides which control wears it.
func (s State) Render(styles *theme.Styles, id ControlID, content string) string {
	if !s.IsPressed(id) || styles == nil {
		return content
	}
	return styles.PressedRun(content)
}

// Update consumes pointer feedback messages. The returned command emits the
// control's domain message after release feedback has been cleared.
func (s State) Update(message tea.Msg) (State, tea.Cmd, bool) {
	event, ok := message.(interactionMsg)
	if !ok {
		return s, nil, false
	}
	switch event.kind {
	case interactionHover:
		return s.Hover(event.id, event.point), nil, true
	case interactionResetHover:
		return s.ClearHoverObservation(), nil, true
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
	interactionHover
	interactionResetHover
)

type interactionMsg struct {
	kind     interactionKind
	id       ControlID
	point    Point
	activate Action
	followup tea.Msg
}

// Interaction is the immutable semantic identity resolved by a rendered map.
// Activation remains private to State.Update.
type Interaction struct {
	Kind     InteractionKind
	ID       ControlID
	Point    Point
	Followup tea.Msg
}

type InteractionKind uint8

const (
	InteractionPress InteractionKind = iota + 1
	InteractionRelease
	InteractionCancel
	InteractionHover
	InteractionResetHover
)

func ObserveInteraction(message tea.Msg) (Interaction, bool) {
	event, ok := message.(interactionMsg)
	if !ok {
		return Interaction{}, false
	}
	kind := InteractionKind(0)
	switch event.kind {
	case interactionPress:
		kind = InteractionPress
	case interactionRelease:
		kind = InteractionRelease
	case interactionCancel:
		kind = InteractionCancel
	case interactionHover:
		kind = InteractionHover
	case interactionResetHover:
		kind = InteractionResetHover
	}
	return Interaction{Kind: kind, ID: event.id, Point: event.point, Followup: event.followup}, true
}

func ReplaceFollowup(message tea.Msg, followup tea.Msg) tea.Msg {
	event, ok := message.(interactionMsg)
	if !ok {
		return followup
	}
	event.followup = followup
	return event
}

// ReplaceActivation binds a resolved release to the current stable action for
// the same control identity. It does not perform a coordinate lookup.
func ReplaceActivation(message tea.Msg, action Action) tea.Msg {
	event, ok := message.(interactionMsg)
	if !ok {
		return message
	}
	event.activate = action
	return event
}

type WheelIntent struct {
	Key     string
	Current int
	Target  int
	Min     int
	Max     int
}

type WheelMessage interface {
	PointerWheelIntent() WheelIntent
	PointerWheelTarget(int) tea.Msg
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

// CancelWith clears capture before forwarding a resolved domain command. The
// command is executed only when the mailbox drains it inside ordered Update.
func CancelWith(followup tea.Cmd) tea.Cmd {
	return func() tea.Msg {
		var message tea.Msg
		if followup != nil {
			message = followup()
		}
		return interactionMsg{kind: interactionCancel, followup: message}
	}
}

// ResetHover clears both the active owner's hover feedback and its retained
// observation. Capture is deliberately left to Cancel.
func ResetHover() tea.Cmd {
	return func() tea.Msg { return interactionMsg{kind: interactionResetHover} }
}

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

// Snapshot is an immutable copy of one rendered pointer map.
type Snapshot struct {
	regions []region
	wheels  []wheelRegion
}

type controlBinding struct {
	id     ControlID
	action Action
}

type wheelBinding struct {
	intent  WheelIntent
	rebuild func(int) tea.Msg
}

// Topology is the immutable semantic half of one published pointer map. It
// deliberately carries no rectangles: stale input may rebind a stable control
// or wheel identity to the current action and bounds, but it may never repeat a
// coordinate lookup against a frame the terminal did not display.
type Topology struct {
	controls []controlBinding
	wheels   []wheelBinding
}

// Topology returns the stable controls and wheel bounds owned by this map.
// Later registrations win just as they do in the hit map.
func (m Map) Topology() Topology { return m.Snapshot().Topology() }

// Topology returns the stable controls and wheel bounds owned by this snapshot.
func (s Snapshot) Topology() Topology {
	topology := Topology{controls: make([]controlBinding, 0, len(s.regions))}
	for _, candidate := range s.regions {
		if candidate.id == "" || candidate.action == nil {
			continue
		}
		topology.controls = append(topology.controls, controlBinding{id: candidate.id, action: candidate.action})
	}
	for _, candidate := range s.wheels {
		if candidate.action == nil {
			continue
		}
		message, ok := candidate.action(0).(WheelMessage)
		if !ok || message == nil {
			continue
		}
		intent := message.PointerWheelIntent()
		if intent.Key == "" {
			continue
		}
		topology.wheels = append(topology.wheels, wheelBinding{
			intent:  intent,
			rebuild: func(target int) tea.Msg { return message.PointerWheelTarget(target) },
		})
	}
	return topology
}

// Merge returns an immutable union in which bindings from other take
// precedence. It is used by composite surfaces such as the board, whose hover
// and activation maps share one published owner.
func (t Topology) Merge(other Topology) Topology {
	return Topology{
		controls: append(append([]controlBinding(nil), t.controls...), other.controls...),
		wheels:   append(append([]wheelBinding(nil), t.wheels...), other.wheels...),
	}
}

// WithWheel returns a topology extended with one current absolute wheel
// binding. Custom surfaces whose wheel resolver does not use Map.AddWheel use
// this without exposing their geometry.
func (t Topology) WithWheel(intent WheelIntent, rebuild func(int) tea.Msg) Topology {
	if intent.Key == "" || rebuild == nil {
		return t
	}
	next := Topology{
		controls: append([]controlBinding(nil), t.controls...),
		wheels:   append([]wheelBinding(nil), t.wheels...),
	}
	next.wheels = append(next.wheels, wheelBinding{intent: intent, rebuild: rebuild})
	return next
}

// HasControl reports whether the current published owner still exposes id.
func (t Topology) HasControl(id ControlID) bool {
	_, ok := t.control(id)
	return ok
}

func (t Topology) control(id ControlID) (Action, bool) {
	if id == "" {
		return nil, false
	}
	for index := len(t.controls) - 1; index >= 0; index-- {
		candidate := t.controls[index]
		if candidate.id == id && candidate.action != nil {
			return candidate.action, true
		}
	}
	return nil, false
}

// RebindExact validates a stale exact interaction by stable ID and, for a
// release, replaces the old frame's activation closure with the current one.
// Coordinates are retained only as action arguments; they are never resolved.
func (t Topology) RebindExact(message tea.Msg) (tea.Msg, bool) {
	event, ok := message.(interactionMsg)
	if !ok || (event.kind != interactionPress && event.kind != interactionRelease) {
		return nil, false
	}
	action, found := t.control(event.id)
	if !found {
		return nil, false
	}
	if event.kind == interactionRelease {
		event.activate = action
	}
	return event, true
}

// RebindWheel rebuilds target against the current binding for key. The caller
// owns accumulation; topology owns the current absolute bounds and message.
func (t Topology) RebindWheel(key string, target int) (tea.Msg, WheelIntent, bool) {
	if key == "" {
		return nil, WheelIntent{}, false
	}
	for index := len(t.wheels) - 1; index >= 0; index-- {
		candidate := t.wheels[index]
		if candidate.intent.Key != key || candidate.rebuild == nil {
			continue
		}
		intent := candidate.intent
		target = min(max(target, intent.Min), intent.Max)
		return candidate.rebuild(target), intent, true
	}
	return nil, WheelIntent{}, false
}

// SameControls reports semantic topology parity without comparing closures.
func (t Topology) SameControls(other Topology) bool {
	if len(t.controls) != len(other.controls) || len(t.wheels) != len(other.wheels) {
		return false
	}
	for index := range t.controls {
		if t.controls[index].id != other.controls[index].id {
			return false
		}
	}
	for index := range t.wheels {
		if t.wheels[index].intent != other.wheels[index].intent {
			return false
		}
	}
	return true
}

type handlerSnapshot struct {
	regions    []region
	wheels     []wheelRegion
	tracked    bool
	pressed    int
	dragged    bool
	lastMotion Point
	haveMotion bool
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
	m.AddBackdropControl("", bounds, pane, action)
}

// AddBackdropControl registers a stable tracked dismissal region outside pane.
// One identity spans the four non-overlapping strips, so capture survives a
// handler replacement between press and release.
func (m *Map) AddBackdropControl(id ControlID, bounds, pane Rect, action Action) {
	if bounds.empty() || action == nil {
		return
	}
	pane = pane.intersect(bounds)
	if pane.empty() {
		m.addBackdropRegion(id, bounds, action)
		return
	}
	m.addBackdropRegion(id, Rect{X0: bounds.X0, Y0: bounds.Y0, X1: bounds.X1, Y1: pane.Y0}, action)
	m.addBackdropRegion(id, Rect{X0: bounds.X0, Y0: pane.Y1, X1: bounds.X1, Y1: bounds.Y1}, action)
	m.addBackdropRegion(id, Rect{X0: bounds.X0, Y0: pane.Y0, X1: pane.X0, Y1: pane.Y1}, action)
	m.addBackdropRegion(id, Rect{X0: pane.X1, Y0: pane.Y0, X1: bounds.X1, Y1: pane.Y1}, action)
}

func (m *Map) addBackdropRegion(id ControlID, rect Rect, action Action) {
	if id == "" {
		m.Add(rect, action)
		return
	}
	m.AddControl(id, rect, action)
}

// AddWheel registers a wheel zone. The action receives -1 for up and +1 for down.
func (m *Map) AddWheel(rect Rect, action func(delta int) tea.Msg) {
	if rect.empty() || action == nil {
		return
	}
	m.wheels = append(m.wheels, wheelRegion{rect: rect, action: action})
}

// Resolve returns the hoverable control containing point, topmost first. It is
// the region scan of Handler's motion path, exposed for the re-resolve rows 6
// and 9 of spec section 10.5.2 drive after the content moved under a still
// pointer.
func (m Map) Resolve(point Point) (ControlID, bool) {
	return m.Snapshot().Resolve(point)
}

func (m Map) Snapshot() Snapshot {
	return Snapshot{
		regions: append([]region(nil), m.regions...),
		wheels:  append([]wheelRegion(nil), m.wheels...),
	}
}

func (s Snapshot) Resolve(point Point) (ControlID, bool) {
	id := resolveHover(s.regions, point)
	return id, id != ""
}

// resolveHover is the region scan restricted to the regions that opted into
// hover by carrying a control id. Same list as clicks, same topmost-wins
// precedence: spec section 10.5.3 forbids a second region list and a second
// scan, so both the motion path and Resolve run through here.
func resolveHover(regions []region, point Point) ControlID {
	for index := len(regions) - 1; index >= 0; index-- {
		candidate := regions[index]
		if candidate.id != "" && candidate.action != nil && candidate.rect.contains(point) {
			return candidate.id
		}
	}
	return ""
}

// Handler returns the immutable render snapshot's mouse callback.
func (m Map) Handler() func(tea.MouseMsg) tea.Cmd {
	return m.Snapshot().Handler()
}

func (s Snapshot) Handler() func(tea.MouseMsg) tea.Cmd {
	snapshot := &handlerSnapshot{
		regions: s.regions,
		wheels:  s.wheels,
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
		return h.handleMotion(mouse, point)
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

// handleMotion is the hover half of spec section 10.5.2's table. The coord
// guard of section 10.5.3 comes before the region scan and is on raw cell
// coordinates rather than on the resolved id, so idle motion inside one large
// region costs one comparison rather than a linear scan (row 1).
func (h *handlerSnapshot) handleMotion(mouse tea.Mouse, point Point) tea.Cmd {
	if h.haveMotion && h.lastMotion == point {
		return nil
	}
	h.haveMotion, h.lastMotion = true, point
	if mouse.Button == tea.MouseLeft {
		// Row 4: the drag path, unchanged. Hover is neither read nor written.
		if h.pressed >= 0 {
			h.dragged = true
		}
		return h.cancelTracked(nil)
	}
	if !h.tracked {
		return nil
	}
	// Rows 2 and 3: hoverable is opt-in on the region that already exists, so
	// a map with no control ids never emits hover and never re-renders for it,
	// and a point that resolves to none of them clears hover rather than
	// leaving the previous one lit.
	return hoverCommand(resolveHover(h.regions, point), point)
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

func hoverCommand(id ControlID, point Point) tea.Cmd {
	return func() tea.Msg {
		return interactionMsg{kind: interactionHover, id: id, point: point}
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
