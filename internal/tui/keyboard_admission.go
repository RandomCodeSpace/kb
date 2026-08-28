package tui

import (
	"sync"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// inputAdmissionStats is shared by the program-level filters and Model copies.
// A rejected raw event never reaches Update, so a shared counter is the only
// honest place to account for it in RenderPlanStats.
type inputAdmissionStats struct {
	discarded     atomic.Uint64
	keyboardEpoch atomic.Uint64
	keyboardMu    sync.RWMutex
	keyboard      boardNavigationSnapshot
	keyboardOK    bool
}

func (s *inputAdmissionStats) discard() {
	if s != nil {
		s.discarded.Add(1)
	}
}

func (s *inputAdmissionStats) discardedEvents() uint64 {
	if s == nil {
		return 0
	}
	return s.discarded.Load()
}

func (s *inputAdmissionStats) nextKeyboardEpoch() uint64 {
	if s == nil {
		return 0
	}
	return s.keyboardEpoch.Add(1)
}

func (s *inputAdmissionStats) currentKeyboardEpoch() uint64 {
	if s == nil {
		return 0
	}
	return s.keyboardEpoch.Load()
}

func (s *inputAdmissionStats) publishKeyboard(snapshot boardNavigationSnapshot, ok bool) {
	if s == nil {
		return
	}
	s.keyboardMu.Lock()
	s.keyboard = snapshot
	s.keyboardOK = ok
	s.keyboardMu.Unlock()
}

func (s *inputAdmissionStats) keyboardSnapshot() (boardNavigationSnapshot, bool) {
	if s == nil {
		return boardNavigationSnapshot{}, false
	}
	s.keyboardMu.RLock()
	snapshot, ok := s.keyboard, s.keyboardOK
	s.keyboardMu.RUnlock()
	return snapshot, ok
}

type keyboardNavigationRoute uint8

const keyboardNavigationRoutePlainBoard keyboardNavigationRoute = 1

// boardNavigationIntentMsg is the bounded semantic message admitted in place
// of a raw repeat. It names an exact target instead of carrying a delta, so no
// later frame can replay surplus movement against newer board state.
type boardNavigationIntentMsg struct {
	epoch                uint64
	boardGeneration      uint64
	projectionGeneration uint64
	layoutGeneration     uint64
	route                keyboardNavigationRoute
	column               int
	direction            int
	sourceTaskID         string
	targetTaskID         string
}

type boardNavigationIdentity struct {
	boardGeneration      uint64
	projectionGeneration uint64
	layoutGeneration     uint64
	route                keyboardNavigationRoute
	column               int
	status               board.Status
}

type boardNavigationSnapshot struct {
	identity        boardNavigationIdentity
	projection      *renderProjection
	selectedTaskID  string
	selectedOrdinal int
	count           int
}

// keyboardAdmission is the program-level vertical navigation gate. It owns no
// timer and no queue: raw repeats replace one desired task target, while the
// next serialized WithFilter call is the proof that the previous View returned.
type keyboardAdmission struct {
	interval time.Duration
	quiet    time.Duration
	now      func() time.Time
	stats    *inputAdmissionStats

	active         bool
	epoch          uint64
	direction      int
	identity       boardNavigationIdentity
	desiredTaskID  string
	desiredOrdinal int
	lastRaw        time.Time
	lastAdmitted   time.Time
}

func newKeyboardAdmission(now func() time.Time, stats *inputAdmissionStats, timing theme.Timing) *keyboardAdmission {
	if now == nil {
		now = time.Now
	}
	return &keyboardAdmission{
		interval: timing.KeyboardNavigationInterval,
		quiet:    timing.KeyboardNavigationQuiet,
		now:      now,
		stats:    stats,
	}
}

// Filter admits only passive plain-board vertical navigation. Every other key
// remains the original concrete message and preserves Bubble Tea ordering.
func (a *keyboardAdmission) Filter(_ tea.Model, message tea.Msg) tea.Msg {
	if a == nil {
		return message
	}
	if release, ok := message.(tea.KeyReleaseMsg); ok {
		if direction, vertical := verticalNavigationDirection(release.String()); vertical {
			hadActive := a.active && direction == a.direction
			if hadActive {
				a.clear()
			}
		}
		// ReportEventTypes produces a release for every enhanced key press. No
		// root or submodel owns keyboard releases; admitting them would publish a
		// second no-op frame for ordinary, modal, and text input alike.
		a.discard()
		return nil
	}
	if a.interval <= 0 || a.quiet <= 0 {
		return message
	}
	now := a.now()
	if a.active && now.Sub(a.lastRaw) >= a.quiet {
		a.clear()
	}

	press, ok := message.(tea.KeyPressMsg)
	if !ok {
		if invalidatesKeyboardNavigation(message) {
			a.clear()
		}
		return message
	}
	direction, vertical := verticalNavigationDirection(press.String())
	if !vertical {
		a.clear()
		return message
	}
	snapshot, eligible := a.stats.keyboardSnapshot()
	if !eligible {
		a.clear()
		return message
	}

	if a.active && a.identity != snapshot.identity {
		a.clear()
	}
	// A repeat received after release or invalidation is stale. Legacy terminals
	// do not set IsRepeat, so their next press starts a fresh immediate gesture.
	if !a.active && press.IsRepeat {
		a.discard()
		return nil
	}
	if !a.active || direction != a.direction {
		return a.begin(snapshot, direction, now)
	}

	a.lastRaw = now
	if ordinal, found := snapshot.projection.ordinalForTask(snapshot.identity.status, a.desiredTaskID); !found {
		a.clear()
		if press.IsRepeat {
			a.discard()
			return nil
		}
		return a.begin(snapshot, direction, now)
	} else {
		a.desiredOrdinal = ordinal
	}
	next := min(max(a.desiredOrdinal+direction, 0), snapshot.count-1)
	if next != a.desiredOrdinal {
		a.desiredOrdinal = next
		target, _ := snapshot.projection.taskAtStatus(snapshot.identity.status, next)
		a.desiredTaskID = target.ID
	}

	// At a committed boundary there is neither movement nor pending state. A
	// desired boundary ahead of the committed selection remains replaceable and
	// may be admitted once the cadence opens.
	if a.desiredTaskID == snapshot.selectedTaskID {
		a.clear()
		a.discard()
		return nil
	}
	if now.Sub(a.lastAdmitted) < a.interval {
		a.discard()
		return nil
	}
	a.lastAdmitted = now
	return a.intent(snapshot)
}

func (a *keyboardAdmission) begin(snapshot boardNavigationSnapshot, direction int, now time.Time) tea.Msg {
	next := snapshot.selectedOrdinal + direction
	if next < 0 || next >= snapshot.count {
		a.clear()
		a.discard()
		return nil
	}
	target, ok := snapshot.projection.taskAtStatus(snapshot.identity.status, next)
	if !ok {
		a.clear()
		a.discard()
		return nil
	}
	a.active = true
	a.epoch = a.stats.nextKeyboardEpoch()
	a.direction = direction
	a.identity = snapshot.identity
	a.desiredTaskID = target.ID
	a.desiredOrdinal = next
	a.lastRaw = now
	a.lastAdmitted = now
	return a.intent(snapshot)
}

func (a *keyboardAdmission) intent(snapshot boardNavigationSnapshot) boardNavigationIntentMsg {
	return boardNavigationIntentMsg{
		epoch:                a.epoch,
		boardGeneration:      snapshot.identity.boardGeneration,
		projectionGeneration: snapshot.identity.projectionGeneration,
		layoutGeneration:     snapshot.identity.layoutGeneration,
		route:                snapshot.identity.route,
		column:               snapshot.identity.column,
		direction:            a.direction,
		sourceTaskID:         snapshot.selectedTaskID,
		targetTaskID:         a.desiredTaskID,
	}
}

func (a *keyboardAdmission) clear() {
	if a == nil || !a.active {
		return
	}
	a.active = false
	a.epoch = a.stats.nextKeyboardEpoch()
	a.direction = 0
	a.identity = boardNavigationIdentity{}
	a.desiredTaskID = ""
	a.desiredOrdinal = 0
	a.lastRaw = time.Time{}
	a.lastAdmitted = time.Time{}
}

func (a *keyboardAdmission) discard() {
	if a != nil {
		a.stats.discard()
	}
}

func verticalNavigationDirection(key string) (int, bool) {
	switch key {
	case "up", "k":
		return -1, true
	case "down", "j":
		return 1, true
	default:
		return 0, false
	}
}

func invalidatesKeyboardNavigation(message tea.Msg) bool {
	switch message.(type) {
	case tea.WindowSizeMsg, boardLoadedMsg, dataVersionMsg,
		tea.MouseClickMsg, tea.MouseReleaseMsg,
		boardCardClickedMsg, boardColumnClickedMsg,
		boardPointerDownMsg, boardPointerMoveMsg, boardPointerUpMsg,
		filterTextClickedMsg, filterLabelClickedMsg, filterClearClickedMsg, filterProjectClickedMsg:
		return true
	default:
		return false
	}
}

func (m Model) plainBoardNavigationSnapshot() (boardNavigationSnapshot, bool) {
	if m.stopped || m.overlayOpen() || m.action.busy || m.filter.focus != filterUnfocused ||
		m.move.lifted != nil || m.move.saving || m.current == nil {
		return boardNavigationSnapshot{}, false
	}
	projection := m.currentProjection()
	if projection == nil || m.boardView.column < 0 || m.boardView.column >= len(boardStatuses) {
		return boardNavigationSnapshot{}, false
	}
	column := m.boardView.column
	status := boardStatuses[column]
	view := m.boardView
	view.restoreColumnCursorFrom(projection, column)
	selected, ok := view.selectedProjectionTask(projection)
	if !ok {
		return boardNavigationSnapshot{}, false
	}
	ordinal, ok := projection.ordinalForTask(status, selected.ID)
	if !ok {
		return boardNavigationSnapshot{}, false
	}
	return boardNavigationSnapshot{
		identity: boardNavigationIdentity{
			boardGeneration:      projection.sourceGeneration,
			projectionGeneration: projection.generation,
			layoutGeneration:     m.current.geometry.generation,
			route:                keyboardNavigationRoutePlainBoard,
			column:               column,
			status:               status,
		},
		projection:      projection,
		selectedTaskID:  selected.ID,
		selectedOrdinal: ordinal,
		count:           projection.statusCount(status),
	}, true
}

func (m *Model) publishKeyboardAdmissionSnapshot() {
	if m == nil || m.inputAdmission == nil {
		return
	}
	snapshot, ok := m.plainBoardNavigationSnapshot()
	m.inputAdmission.publishKeyboard(snapshot, ok)
}

func (m Model) matchesBoardNavigationIntent(intent boardNavigationIntentMsg) bool {
	if intent.route != keyboardNavigationRoutePlainBoard || m.inputAdmission == nil ||
		intent.epoch != m.inputAdmission.currentKeyboardEpoch() {
		return false
	}
	snapshot, ok := m.plainBoardNavigationSnapshot()
	if !ok || snapshot.identity.boardGeneration != intent.boardGeneration ||
		snapshot.identity.projectionGeneration != intent.projectionGeneration ||
		snapshot.identity.layoutGeneration != intent.layoutGeneration ||
		snapshot.identity.column != intent.column || snapshot.selectedTaskID != intent.sourceTaskID {
		return false
	}
	targetOrdinal, ok := snapshot.projection.ordinalForTask(snapshot.identity.status, intent.targetTaskID)
	if !ok {
		return false
	}
	return intent.direction < 0 && targetOrdinal < snapshot.selectedOrdinal ||
		intent.direction > 0 && targetOrdinal > snapshot.selectedOrdinal
}

func (m *Model) applyBoardNavigationIntent(intent boardNavigationIntentMsg) bool {
	if m == nil || !m.matchesBoardNavigationIntent(intent) {
		return false
	}
	projection := m.currentProjection()
	status := boardStatuses[intent.column]
	ordinal, ok := projection.ordinalForTask(status, intent.targetTaskID)
	if !ok {
		return false
	}
	m.boardView.setCursorAtFrom(projection, intent.column, ordinal)
	m.boardView.manualScroll[intent.column] = false
	return true
}
