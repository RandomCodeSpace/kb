package tui

import (
	"sync"

	tea "charm.land/bubbletea/v2"
)

type pointerRawKind uint8

const (
	pointerRawClick pointerRawKind = iota + 1
	pointerRawRelease
	pointerRawWheel
	pointerRawMotion
)

type pointerRawIdentity struct {
	kind  pointerRawKind
	mouse tea.Mouse
}

func identifyPointerRaw(message tea.MouseMsg) (pointerRawIdentity, bool) {
	var kind pointerRawKind
	switch message.(type) {
	case tea.MouseClickMsg:
		kind = pointerRawClick
	case tea.MouseReleaseMsg:
		kind = pointerRawRelease
	case tea.MouseWheelMsg:
		kind = pointerRawWheel
	case tea.MouseMotionMsg:
		kind = pointerRawMotion
	default:
		return pointerRawIdentity{}, false
	}
	return pointerRawIdentity{kind: kind, mouse: message.Mouse()}, true
}

type pointerRouteIdentity struct {
	owner        renderHandlerTopology
	ownerSession uint64
	geometry     uint64
	snapshot     uint64
}

func (r pointerRouteIdentity) sameOwner(other pointerRouteIdentity) bool {
	return r.owner == other.owner && r.ownerSession == other.ownerSession
}

func (r pointerRouteIdentity) sameGeneration(other pointerRouteIdentity) bool {
	return r.sameOwner(other) && r.geometry == other.geometry && r.snapshot == other.snapshot
}

type pointerMailboxSlot struct {
	raw     pointerRawIdentity
	command tea.Cmd
	route   pointerRouteIdentity
}

type pointerMailboxTake uint8

const (
	pointerMailboxEmpty pointerMailboxTake = iota
	pointerMailboxMatched
	pointerMailboxFailed
)

// pointerMailbox is the one synchronous handoff between Bubble Tea's
// last-flushed renderer and the immediately following raw Model.Update. Model
// values copy freely, so the mailbox is intentionally shared by pointer.
type pointerMailbox struct {
	mu       sync.Mutex
	slot     pointerMailboxSlot
	occupied bool
	failed   bool
}

func newPointerMailbox() *pointerMailbox { return &pointerMailbox{} }

func (m *pointerMailbox) wrap(handler func(tea.MouseMsg) tea.Cmd, route pointerRouteIdentity) func(tea.MouseMsg) tea.Cmd {
	if m == nil || handler == nil {
		return handler
	}
	return func(raw tea.MouseMsg) tea.Cmd {
		identity, ok := identifyPointerRaw(raw)
		if !ok {
			m.reset()
			return nil
		}
		// Resolution itself is synchronous and belongs to the immutable handler
		// Bubble Tea flushed. Only command execution is deferred to raw Update.
		command := handler(raw)
		m.mu.Lock()
		if m.occupied || m.failed {
			m.slot = pointerMailboxSlot{}
			m.occupied = false
			m.failed = true
			m.mu.Unlock()
			return nil
		}
		m.slot = pointerMailboxSlot{raw: identity, command: command, route: route}
		m.occupied = true
		m.mu.Unlock()
		return nil
	}
}

func (m *pointerMailbox) take(raw tea.MouseMsg) (tea.Cmd, pointerRouteIdentity, pointerMailboxTake) {
	if m == nil {
		return nil, pointerRouteIdentity{}, pointerMailboxFailed
	}
	identity, ok := identifyPointerRaw(raw)
	m.mu.Lock()
	defer m.mu.Unlock()
	if !ok || m.failed {
		m.slot = pointerMailboxSlot{}
		m.occupied = false
		m.failed = false
		return nil, pointerRouteIdentity{}, pointerMailboxFailed
	}
	if !m.occupied {
		return nil, pointerRouteIdentity{}, pointerMailboxEmpty
	}
	slot := m.slot
	m.slot = pointerMailboxSlot{}
	m.occupied = false
	if slot.raw != identity {
		return nil, pointerRouteIdentity{}, pointerMailboxFailed
	}
	return slot.command, slot.route, pointerMailboxMatched
}

func (m *pointerMailbox) reset() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.slot = pointerMailboxSlot{}
	m.occupied = false
	m.failed = false
	m.mu.Unlock()
}
