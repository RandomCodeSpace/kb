package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/tui/pointer"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

type pointerFlushMsg struct{ sequence uint64 }

type pointerTargetKind uint8

const (
	pointerTargetHover pointerTargetKind = iota + 1
	pointerTargetDrag
	pointerTargetWheel
)

type pointerTarget struct {
	kind         pointerTargetKind
	control      pointer.ControlID
	status       board.Status
	beforeTaskID string
	wheelKey     string
	position     int
	wheelStep    int
}

func (t pointerTarget) sameDestination(other pointerTarget) bool {
	if t.kind != other.kind {
		return false
	}
	switch t.kind {
	case pointerTargetHover:
		return t.control == other.control
	case pointerTargetDrag:
		return t.status == other.status && t.beforeTaskID == other.beforeTaskID
	case pointerTargetWheel:
		return t.wheelKey == other.wheelKey && t.position == other.position
	default:
		return false
	}
}

type pointerIntent struct {
	message       tea.Msg
	target        pointerTarget
	route         pointerRouteIdentity
	sourceRoute   pointerRouteIdentity
	raw           tea.MouseMsg
	wheelDelta    int
	advanced      bool
	pendingTravel bool
}

func (i pointerIntent) sameSourceGeneration(route pointerRouteIdentity) bool {
	source := i.sourceRoute
	if source == (pointerRouteIdentity{}) {
		source = i.route
	}
	return source.sameGeneration(route)
}

type pointerWheelResolution uint8

const (
	pointerWheelResolved pointerWheelResolution = iota
	pointerWheelStale
	pointerWheelBoundary
)

type pointerAdmissionState struct {
	sequence uint64
	timer    bool

	pending     pointerIntent
	havePending bool

	last       pointerTarget
	haveLast   bool
	lastAt     time.Time
	lastRoute  pointerRouteIdentity
	lastIntent pointerIntent

	captureKey          string
	captureOwnerSession uint64
	hoverObserved       bool
	hoverControl        pointer.ControlID

	accepted  uint64
	deferred  uint64
	flushes   uint64
	discarded uint64
}

func (s *pointerAdmissionState) resetCadence() {
	if s == nil {
		return
	}
	s.sequence++
	s.timer = false
	s.pending = pointerIntent{}
	s.havePending = false
	s.last = pointerTarget{}
	s.haveLast = false
	s.lastAt = time.Time{}
	s.lastRoute = pointerRouteIdentity{}
	s.lastIntent = pointerIntent{}
}

func (s *pointerAdmissionState) resetAll() {
	s.resetCadence()
	s.captureKey = ""
	s.captureOwnerSession = 0
	s.hoverObserved = false
	s.hoverControl = ""
}

func (s *pointerAdmissionState) cancelPending() bool {
	if s == nil || !s.havePending {
		return false
	}
	s.pending = pointerIntent{}
	s.havePending = false
	s.sequence++
	s.timer = false
	return true
}

func (m Model) admitResolvedPointer(message tea.Msg, route pointerRouteIdentity, raw tea.MouseMsg) (Model, modelUpdateCommands) {
	currentRoute := m.pointerRoute()
	if !route.sameOwner(currentRoute) {
		m.discardPointer()
		return m, modelUpdateCommands{}
	}
	if m.mouseLiftedForWheel(raw) {
		if intent, ok := m.pointerIntent(message, route, raw); ok && intent.target.kind == pointerTargetWheel {
			return m.admitCancelThenWheel(intent)
		}
		return m.cancelMouseLiftForDiscardedWheel(currentRoute)
	}
	if interaction, ok := pointer.ObserveInteraction(message); ok &&
		interaction.Kind == pointer.InteractionCancel && interaction.Followup != nil {
		if intent, ok := m.pointerIntent(interaction.Followup, route, raw); ok &&
			intent.target.kind == pointerTargetWheel {
			return m.admitCancelThenWheel(intent)
		}
	}
	if intent, ok := m.pointerIntent(message, route, raw); ok {
		return m.admitPointerIntent(intent)
	}
	if !route.sameGeneration(currentRoute) {
		var ok bool
		message, ok = m.translateExactPointer(message, route)
		if !ok {
			return m.failClosedPointer()
		}
	}
	if !m.exactPointerValid(message, route) {
		return m.failClosedPointer()
	}
	m.pointerAdmission.resetCadence()
	m.preparePointerCapture(message, route)
	return m.applyResolvedPointer(message, true)
}

func (m Model) mouseLiftedForWheel(raw tea.MouseMsg) bool {
	_, wheel := raw.(tea.MouseWheelMsg)
	return wheel && m.move.lifted != nil && m.move.lifted.fromMouse
}

func (m Model) clearPointerCaptureWithoutPublish(route pointerRouteIdentity) Model {
	state := &m.pointerAdmission
	preservingWheelCadence := state.captureKey == "" && !m.pointerState.Active() &&
		(m.move.lifted == nil || !m.move.lifted.fromMouse) &&
		((state.havePending && state.pending.target.kind == pointerTargetWheel) ||
			(state.haveLast && state.last.kind == pointerTargetWheel))
	if !preservingWheelCadence {
		state.resetCadence()
	}
	message := pointer.Cancel()()
	m.preparePointerCapture(message, route)
	m, _ = m.route(message)
	return m
}

func (m Model) cancelMouseLiftForDiscardedWheel(route pointerRouteIdentity) (Model, modelUpdateCommands) {
	m = m.clearPointerCaptureWithoutPublish(route)
	return m.applyDiscardedMouseLiftCancel()
}

func (m Model) applyDiscardedMouseLiftCancel() (Model, modelUpdateCommands) {
	m.discardPointer()
	return m.applyResolvedPointer(boardPointerCancelMoveMsg{}, true)
}

func (m Model) translateExactPointer(message tea.Msg, route pointerRouteIdentity) (tea.Msg, bool) {
	if !route.sameOwner(m.pointerRoute()) || m.current == nil {
		return nil, false
	}
	if interaction, ok := pointer.ObserveInteraction(message); ok {
		switch interaction.Kind {
		case pointer.InteractionCancel:
			return message, m.pointerAdmission.captureOwnerSession == route.ownerSession
		case pointer.InteractionPress, pointer.InteractionRelease:
			return m.current.semantics.topology.RebindExact(message)
		default:
			return nil, false
		}
	}
	switch msg := message.(type) {
	case boardPointerDownMsg:
		return message, m.currentVisibleBoardTask(msg.taskID)
	case boardPointerUpMsg:
		taskID, ok := boardCaptureTaskID(m.pointerAdmission.captureKey)
		if !ok || m.pointerAdmission.captureOwnerSession != route.ownerSession || !m.currentVisibleBoardTask(taskID) {
			return nil, false
		}
		if !msg.resolved || !msg.valid {
			return message, true
		}
		return message, m.currentVisibleDropIdentity(msg.status, msg.beforeTaskID)
	default:
		return nil, false
	}
}

func (m Model) currentVisibleBoardTask(taskID string) bool {
	if taskID == "" || m.current == nil || m.current.semantics.handler != renderHandlerBoard {
		return false
	}
	for _, hit := range m.current.semantics.hits {
		if hit.kind == boardHitDefault && hit.taskID == taskID {
			return true
		}
	}
	return false
}

func (m Model) currentVisibleDropIdentity(status board.Status, beforeTaskID string) bool {
	if m.current == nil || m.current.semantics.handler != renderHandlerBoard {
		return false
	}
	for _, hit := range m.current.semantics.hits {
		if hit.kind == boardHitDefault && hit.status == status && hit.taskID == beforeTaskID {
			return true
		}
	}
	return false
}

func boardCaptureTaskID(key string) (string, bool) {
	const prefix = "board-card:"
	if len(key) <= len(prefix) || key[:len(prefix)] != prefix {
		return "", false
	}
	return key[len(prefix):], true
}

func (m Model) admitCancelThenWheel(intent pointerIntent) (Model, modelUpdateCommands) {
	hadMouseLift := m.move.lifted != nil && m.move.lifted.fromMouse
	var resolution pointerWheelResolution
	intent, resolution = m.resolvePointerWheelIntent(intent)
	if resolution != pointerWheelResolved {
		if hadMouseLift {
			m = m.clearPointerCaptureWithoutPublish(intent.route)
			return m.applyDiscardedMouseLiftCancel()
		}
		return m.failClosedPointer()
	}
	m = m.clearPointerCaptureWithoutPublish(intent.route)
	return m.admitPointerIntent(intent)
}

func (m Model) exactPointerValid(message tea.Msg, route pointerRouteIdentity) bool {
	interaction, ok := pointer.ObserveInteraction(message)
	if ok && (interaction.Kind == pointer.InteractionRelease || interaction.Kind == pointer.InteractionCancel) {
		state := m.pointerAdmission
		if state.captureOwnerSession != route.ownerSession {
			return false
		}
		if interaction.Kind == pointer.InteractionRelease {
			return state.captureKey != "" && state.captureKey == string(interaction.ID)
		}
		return state.captureKey != ""
	}
	if _, release := message.(boardPointerUpMsg); release {
		return m.pointerAdmission.captureKey != "" &&
			m.pointerAdmission.captureOwnerSession == route.ownerSession
	}
	return true
}

func (m *Model) preparePointerCapture(message tea.Msg, route pointerRouteIdentity) {
	if interaction, ok := pointer.ObserveInteraction(message); ok {
		switch interaction.Kind {
		case pointer.InteractionPress:
			m.pointerAdmission.captureKey = string(interaction.ID)
			m.pointerAdmission.captureOwnerSession = route.ownerSession
		case pointer.InteractionRelease, pointer.InteractionCancel:
			m.pointerAdmission.captureKey = ""
			m.pointerAdmission.captureOwnerSession = 0
		}
		return
	}
	switch msg := message.(type) {
	case boardPointerDownMsg:
		m.pointerAdmission.captureKey = "board-card:" + msg.taskID
		m.pointerAdmission.captureOwnerSession = route.ownerSession
	case boardPointerUpMsg:
		m.pointerAdmission.captureKey = ""
		m.pointerAdmission.captureOwnerSession = 0
	}
}

func (m Model) pointerIntent(message tea.Msg, route pointerRouteIdentity, raw tea.MouseMsg) (pointerIntent, bool) {
	if wheel, rebuild, ok := m.resolveWheelIntent(message); ok {
		step := pointerDeltaMagnitude(wheel.Target - wheel.Current)
		delta := wheel.Target - wheel.Current
		if direction, ok := rawWheelDirection(raw); ok {
			if step == 0 {
				step = m.previousWheelStep(route, wheel.Key)
			}
			delta = direction * step
		}
		return pointerIntent{message: rebuild(wheel.Target), route: route, sourceRoute: route, raw: raw, wheelDelta: delta,
			target: pointerTarget{kind: pointerTargetWheel, wheelKey: wheel.Key,
				position: wheel.Target, wheelStep: step}}, true
	}
	if interaction, ok := pointer.ObserveInteraction(message); ok && interaction.Kind == pointer.InteractionHover {
		return pointerIntent{message: message, route: route, sourceRoute: route, raw: raw,
			target: pointerTarget{kind: pointerTargetHover, control: interaction.ID}}, true
	}
	if move, ok := message.(boardPointerMoveMsg); ok {
		return pointerIntent{message: message, route: route, sourceRoute: route, raw: raw,
			target: pointerTarget{kind: pointerTargetDrag, status: move.status, beforeTaskID: move.beforeTaskID}}, true
	}
	return pointerIntent{}, false
}

// reuseBoundaryWheel carries a wheel gesture across a stale flushed frame when
// that frame's resolver returned nil because it was already at its boundary.
// The raw event still has to describe the same cell and wheel axis as the
// retained intent. Otherwise a nil resolver result is simply a discarded
// event; inventing a target from unrelated input is how pointer state escapes
// its hit map.
func (m Model) reuseBoundaryWheel(raw tea.MouseMsg, route pointerRouteIdentity) (Model, modelUpdateCommands, bool) {
	direction, ok := rawWheelDirection(raw)
	if !ok || !route.sameOwner(m.pointerRoute()) {
		return m, modelUpdateCommands{}, false
	}

	state := m.pointerAdmission
	candidates := make([]pointerIntent, 0, 2)
	if state.havePending {
		candidates = append(candidates, state.pending)
	}
	if state.haveLast {
		candidates = append(candidates, state.lastIntent)
	}
	for _, candidate := range candidates {
		if !candidate.sameSourceGeneration(route) || candidate.target.kind != pointerTargetWheel ||
			candidate.target.wheelKey == "" || !sameWheelCellAxisAndModifiers(candidate.raw, raw) {
			continue
		}
		wheel, _, ok := m.resolveWheelIntent(candidate.message)
		if !ok || wheel.Key != candidate.target.wheelKey || candidate.target.wheelStep <= 0 {
			continue
		}
		candidate.raw = raw
		candidate.wheelDelta = direction * candidate.target.wheelStep
		candidate.advanced = false
		candidate, resolution := m.resolvePointerWheelIntent(candidate)
		if resolution != pointerWheelResolved {
			continue
		}
		next, commands := m.admitPointerIntent(candidate)
		return next, commands, true
	}
	return m, modelUpdateCommands{}, false
}

func sameWheelCellAxisAndModifiers(previous, current tea.MouseMsg) bool {
	if previous == nil || current == nil {
		return false
	}
	previousWheel, previousOK := previous.(tea.MouseWheelMsg)
	currentWheel, currentOK := current.(tea.MouseWheelMsg)
	if !previousOK || !currentOK || previousWheel.X != currentWheel.X || previousWheel.Y != currentWheel.Y ||
		previousWheel.Mod != currentWheel.Mod {
		return false
	}
	return wheelAxis(previousWheel.Button) == wheelAxis(currentWheel.Button)
}

func wheelAxis(button tea.MouseButton) uint8 {
	switch button {
	case tea.MouseWheelUp, tea.MouseWheelDown:
		return 1
	case tea.MouseWheelLeft, tea.MouseWheelRight:
		return 2
	default:
		return 0
	}
}

func (m Model) previousWheelStep(route pointerRouteIdentity, wheelKey string) int {
	state := m.pointerAdmission
	if state.havePending && state.pending.route.sameOwner(route) &&
		state.pending.target.kind == pointerTargetWheel && state.pending.target.wheelKey == wheelKey {
		return state.pending.target.wheelStep
	}
	if state.haveLast && state.lastRoute.sameOwner(route) &&
		state.last.kind == pointerTargetWheel && state.last.wheelKey == wheelKey {
		return state.last.wheelStep
	}
	return 0
}

func (m Model) resolveWheelIntent(message tea.Msg) (pointer.WheelIntent, func(int) tea.Msg, bool) {
	base := message
	wrapped := false
	if interaction, ok := pointer.ObserveInteraction(message); ok && interaction.Followup != nil {
		base = interaction.Followup
		wrapped = true
	}
	if boardScroll, ok := base.(boardColumnScrolledMsg); ok {
		intent := pointer.WheelIntent{Key: "board:" + string(boardScroll.status), Current: boardScroll.from,
			Target: boardScroll.offset, Min: 0, Max: boardScroll.max}
		return intent, func(target int) tea.Msg {
			target = min(max(target, 0), boardScroll.max)
			rebuilt := boardScroll
			rebuilt.offset = target
			rebuilt.anchor = m.boardScrollAnchor(rebuilt.status, target)
			if wrapped {
				return pointer.ReplaceFollowup(message, rebuilt)
			}
			return rebuilt
		}, true
	}
	wheel, ok := base.(pointer.WheelMessage)
	if !ok || wheel == nil {
		return pointer.WheelIntent{}, nil, false
	}
	intent := wheel.PointerWheelIntent()
	if intent.Key == "" {
		return pointer.WheelIntent{}, nil, false
	}
	return intent, func(target int) tea.Msg {
		rebuilt := wheel.PointerWheelTarget(target)
		if wrapped {
			return pointer.ReplaceFollowup(message, rebuilt)
		}
		return rebuilt
	}, true
}

func (m Model) admitPointerIntent(intent pointerIntent) (Model, modelUpdateCommands) {
	state := &m.pointerAdmission
	currentRoute := m.pointerRoute()
	if !intent.route.sameOwner(currentRoute) {
		m.discardPointer()
		return m, modelUpdateCommands{}
	}
	if intent.target.kind == pointerTargetWheel {
		var resolution pointerWheelResolution
		intent, resolution = m.resolvePointerWheelIntent(intent)
		if resolution == pointerWheelStale {
			return m.failClosedPointer()
		}
		if resolution == pointerWheelBoundary {
			if state.havePending && state.pending.route.sameOwner(intent.route) &&
				state.pending.target.kind == pointerTargetWheel &&
				state.pending.target.wheelKey == intent.target.wheelKey &&
				!intent.pendingTravel {
				state.cancelPending()
			}
			m.discardPointer()
			return m, modelUpdateCommands{}
		}
	} else if !intent.route.sameGeneration(currentRoute) {
		translated, ok := m.translatePointerIntent(intent)
		if !ok {
			return m.failClosedPointer()
		}
		intent = translated
	}
	if intent.target.kind == pointerTargetDrag &&
		(state.captureKey == "" || state.captureOwnerSession != intent.route.ownerSession) {
		m.discardPointer()
		return m, modelUpdateCommands{}
	}
	if state.haveLast && state.lastRoute.sameOwner(intent.route) && intent.target.sameDestination(state.last) {
		if intent.target.kind == pointerTargetHover {
			state.hoverObserved = true
			state.hoverControl = intent.target.control
			return m.applyResolvedPointer(intent.message, false)
		}
		state.cancelPending()
		m.discardPointer()
		return m, modelUpdateCommands{}
	}
	now := m.pointerNow()
	window := m.themeStyles().Timing.InputCoalesce
	if window <= 0 || !state.haveLast || !state.lastRoute.sameOwner(intent.route) || now.Sub(state.lastAt) >= window {
		if state.havePending {
			state.resetCadence()
			m.discardPointer()
		}
		return m.publishPointerIntent(intent, now)
	}
	if state.havePending {
		m.discardPointer()
	}
	state.pending = intent
	state.havePending = true
	state.deferred++
	if state.timer {
		return m, modelUpdateCommands{}
	}
	state.sequence++
	state.timer = true
	delay := max(window-now.Sub(state.lastAt), time.Duration(0))
	return m, modelUpdateCommands{followUp: theme.Tick(delay, pointerFlushMsg{sequence: state.sequence})}
}

func (m Model) resolvePointerWheelIntent(intent pointerIntent) (pointerIntent, pointerWheelResolution) {
	currentRoute := m.pointerRoute()
	if !intent.route.sameGeneration(currentRoute) {
		translated, ok := m.translatePointerIntent(intent)
		if !ok {
			return pointerIntent{}, pointerWheelStale
		}
		translated.route = currentRoute
		intent = translated
	}
	if !intent.advanced {
		intent = m.advanceWheelIntent(intent)
	}
	if intent.message == nil {
		return intent, pointerWheelBoundary
	}
	return intent, pointerWheelResolved
}

func (m Model) advanceWheelIntent(intent pointerIntent) pointerIntent {
	wheel, rebuild, ok := m.resolveWheelIntent(intent.message)
	if !ok {
		intent.message = nil
		return intent
	}
	step := intent.wheelDelta
	base := wheel.Current
	state := &m.pointerAdmission
	usedPending := false
	if state.havePending && state.pending.route.sameOwner(intent.route) &&
		state.pending.target.kind == pointerTargetWheel && state.pending.target.wheelKey == wheel.Key {
		base = state.pending.target.position
		usedPending = true
	} else if state.haveLast && state.lastRoute.sameGeneration(intent.route) &&
		state.last.kind == pointerTargetWheel && state.last.wheelKey == wheel.Key {
		base = state.last.position
	}
	base = min(max(base, wheel.Min), wheel.Max)
	intent.pendingTravel = usedPending && base != wheel.Current
	target := min(max(base+step, wheel.Min), wheel.Max)
	if target == base {
		intent.target.position = base
		intent.message = nil
		return intent
	}
	intent.target.position = target
	intent.target.wheelStep = max(intent.target.wheelStep, pointerDeltaMagnitude(step))
	intent.message = rebuild(target)
	intent.advanced = true
	return intent
}

func (m Model) publishPointerIntent(intent pointerIntent, now time.Time) (Model, modelUpdateCommands) {
	next, commands := m.applyResolvedPointer(intent.message, true)
	if intent.target.kind == pointerTargetHover {
		next.pointerAdmission.hoverObserved = true
		next.pointerAdmission.hoverControl = intent.target.control
	}
	next.pointerAdmission.last = intent.target
	next.pointerAdmission.haveLast = true
	next.pointerAdmission.lastAt = now
	next.pointerAdmission.lastRoute = next.pointerRoute()
	next.pointerAdmission.lastIntent = intent
	return next, commands
}

func (m Model) flushPointerIntent(message pointerFlushMsg) (Model, modelUpdateCommands) {
	state := &m.pointerAdmission
	if !state.timer || message.sequence != state.sequence {
		return m, modelUpdateCommands{}
	}
	state.timer = false
	if !state.havePending {
		return m, modelUpdateCommands{}
	}
	intent := state.pending
	state.pending = pointerIntent{}
	state.havePending = false
	translated, ok := m.translatePointerIntent(intent)
	if !ok {
		m.discardPointer()
		state.resetCadence()
		return m, modelUpdateCommands{}
	}
	intent = translated
	now := m.pointerNow()
	window := m.themeStyles().Timing.InputCoalesce
	if window > 0 && state.haveLast && now.Sub(state.lastAt) < window {
		state.pending = intent
		state.havePending = true
		state.sequence++
		state.timer = true
		return m, modelUpdateCommands{followUp: theme.Tick(window-now.Sub(state.lastAt), pointerFlushMsg{sequence: state.sequence})}
	}
	state.flushes++
	return m.publishPointerIntent(intent, now)
}

func (m Model) translatePointerIntent(intent pointerIntent) (pointerIntent, bool) {
	currentRoute := m.pointerRoute()
	if !intent.route.sameOwner(currentRoute) || m.current == nil {
		return pointerIntent{}, false
	}
	if intent.route.sameGeneration(currentRoute) {
		return intent, true
	}
	current := intent
	switch intent.target.kind {
	case pointerTargetHover:
		if !m.current.semantics.topology.HasControl(intent.target.control) {
			return pointerIntent{}, false
		}
	case pointerTargetDrag:
		if m.current.semantics.handler != renderHandlerBoard ||
			!m.currentVisibleDropIdentity(intent.target.status, intent.target.beforeTaskID) {
			return pointerIntent{}, false
		}
	case pointerTargetWheel:
		message, bounds, ok := m.current.semantics.topology.RebindWheel(
			intent.target.wheelKey, intent.target.position,
		)
		if !ok {
			return pointerIntent{}, false
		}
		wheel, rebuild, ok := m.resolveWheelIntent(message)
		if !ok {
			return pointerIntent{}, false
		}
		if !intent.advanced {
			// A direct event from an old flushed resolver carries that frame's
			// one-step absolute target. Rebase its signed delta onto the current
			// topology; advanceWheelIntent will also include any current pending
			// travel. Preserving the old absolute target here drops wheel bursts.
			current.message = rebuild(min(max(wheel.Current, bounds.Min), bounds.Max))
			current.target.position = min(max(wheel.Current, bounds.Min), bounds.Max)
			current.advanced = false
			break
		}
		// A deferred intent has already accumulated its signed travel. Keep
		// that absolute destination, clamped by the current published bounds.
		target := min(max(intent.target.position, bounds.Min), bounds.Max)
		target = min(max(target, wheel.Min), wheel.Max)
		current.message = rebuild(target)
		current.target.position = target
		current.target.wheelStep = intent.target.wheelStep
		current.advanced = true
	default:
		return pointerIntent{}, false
	}
	return current, true
}

func rawWheelDirection(raw tea.MouseMsg) (int, bool) {
	if raw == nil {
		return 0, false
	}
	wheel, ok := raw.(tea.MouseWheelMsg)
	if !ok {
		return 0, false
	}
	switch wheel.Button {
	case tea.MouseWheelUp:
		return -1, true
	case tea.MouseWheelDown:
		return 1, true
	default:
		return 0, false
	}
}

func pointerDeltaMagnitude(delta int) int {
	if delta < 0 {
		return -delta
	}
	return delta
}

func (m Model) pointerNow() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

func (m *Model) discardPointer() {
	m.pointerAdmission.discarded++
}

func (m Model) boardScrollAnchor(status board.Status, offset int) boardTaskAnchor {
	if m.current == nil {
		return boardTaskAnchor{}
	}
	column := statusIndex(status)
	projection := m.currentProjection()
	if projection == nil || column < 0 || column >= len(m.current.geometry.columns) {
		return boardTaskAnchor{}
	}
	return m.current.geometry.columns[column].anchorAt(offset, projection)
}
