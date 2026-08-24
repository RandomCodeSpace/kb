package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/tui/pointer"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// The destructive-prompt grace of spec section 10.3.3.
//
// crush swallows input into an async dialog until input has been quiet
// DialogGraceQuiet or DialogGraceMax absolute has elapsed since open, and skips
// the grace entirely when the same dialog reopens within DialogGraceReopen.
//
// The values are adopted unchanged; the scope is not. kb has no async dialog -
// every prompt opens from a deliberate keystroke - so kb's equivalent hazard
// class is the prompt whose affirmative is destructive, which spec section 5.4
// already marks structurally. The grace applies to, and only to, a prompt
// carrying a ButtonDanger affirmative. Every other overlay opens ungraced.
//
// Nothing here reads a clock. An effect expires because a message arrived,
// never because a render compared time.Since to a token, which is what lets a
// unit test step the whole machine synchronously.

// graceSlot is the overlay z-order slot half of a prompt's identity.
type graceSlot uint8

const (
	graceNone graceSlot = iota
	graceDetail
	graceSettings
	graceAction
)

// graceID is prompt identity: the overlay's z-order slot plus the target card
// id. The arm/confirm transition of a two-step prompt re-renders the same
// identity, so it does not re-arm the grace - that is what keeps the grace and
// the Armed two-step of spec section 1.9 from compounding.
type graceID struct {
	slot   graceSlot
	cardID string
}

func (id graceID) set() bool { return id.slot != graceNone }

type graceQuietMsg struct{ tag uint64 }

type graceMaxMsg struct{ tag uint64 }

type graceReopenMsg struct{ tag uint64 }

// graceState is the swallow window and the reopen suppression beside it.
type graceState struct {
	id     graceID
	active bool

	seq      uint64
	quietTag uint64
	openTag  uint64

	reopenID  graceID
	reopenTag uint64
	reopen    bool
}

// open arms the grace for a prompt that just appeared. A reopen of the same
// identity inside DialogGraceReopen skips the grace entirely.
func (g *graceState) open(id graceID, timing theme.Timing) tea.Cmd {
	if !id.set() {
		return nil
	}
	if g.reopen && g.reopenID == id {
		g.active = false
		g.id = id
		return nil
	}
	g.id = id
	g.active = true
	g.openTag = g.next()
	g.quietTag = g.next()
	return tea.Batch(
		theme.Tick(timing.DialogGraceQuiet, graceQuietMsg{tag: g.quietTag}),
		theme.Tick(timing.DialogGraceMax, graceMaxMsg{tag: g.openTag}),
	)
}

// close ends the grace and opens the reopen window for the identity that left.
func (g *graceState) close(id graceID, timing theme.Timing) tea.Cmd {
	g.active = false
	g.id = graceID{}
	if !id.set() {
		return nil
	}
	g.reopen = true
	g.reopenID = id
	g.reopenTag = g.next()
	return theme.Tick(timing.DialogGraceReopen, graceReopenMsg{tag: g.reopenTag})
}

// swallows reports whether the grace eats this message.
//
// The dismissal ladder is never swallowed. crush swallows every key; kb does
// not, because the two directions have asymmetric costs: a swallowed
// affirmative is the entire point of the mechanism, while a swallowed cancel is
// a user who concludes the app has hung. The grace guards the commit, never the
// exit.
func (g graceState) swallows(message tea.Msg) bool {
	if !g.active {
		return false
	}
	switch msg := message.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "esc", "q", ctrlCKey:
			return false
		}
		return true
	case taskActionPointerMsg, settingsPointerMsg:
		return true
	}
	// A click has to be included: the second half of a double-click is exactly
	// the input the grace exists to eat. Overlay controls activate through the
	// pointer interaction family, so swallowing it stops the press before it
	// ever reaches a domain message.
	return pointer.IsMessage(message)
}

// absorb consumes one swallowed message and restarts the quiet window. Either a
// key or a click resets it; the absolute ceiling keeps its original tag and is
// therefore not extended.
func (g *graceState) absorb(timing theme.Timing) tea.Cmd {
	g.quietTag = g.next()
	return theme.Tick(timing.DialogGraceQuiet, graceQuietMsg{tag: g.quietTag})
}

// expire consumes the three window messages. A stale tag is dropped, which is
// the same generation guard the notice TTL and the frame clock both carry: a
// grace armed at t+1s must not be ended by the previous prompt's ceiling.
func (g *graceState) expire(message tea.Msg) bool {
	switch msg := message.(type) {
	case graceQuietMsg:
		if msg.tag == g.quietTag {
			g.active = false
		}
	case graceMaxMsg:
		if msg.tag == g.openTag {
			g.active = false
		}
	case graceReopenMsg:
		if msg.tag == g.reopenTag {
			g.reopen = false
			g.reopenID = graceID{}
		}
	default:
		return false
	}
	return true
}

func (g *graceState) next() uint64 {
	g.seq++
	return g.seq
}

// graceIdentity is the destructive prompt the model is showing, in z-order.
// The table is spec section 10.3.3's:
//
//	Card detail, delete confirm   Delete, Confirm delete
//	Kill prompt                   Kill without reason, Kill with reason
//	Purge prompt                  the single arm button
//	Settings                      Remove, Confirm remove
//	Ship guard                    Ship anyway
func (m Model) graceIdentity() graceID {
	if m.action.open() && !m.action.busy && m.action.mode != taskActionChecklist {
		return graceID{slot: graceAction, cardID: m.action.task.ID}
	}
	if m.settings != nil && m.settings.armedRemove != "" {
		return graceID{slot: graceSettings, cardID: m.settings.armedRemove}
	}
	if m.detail.IsOpen() && m.detail.IsDestructivePrompt() {
		return graceID{slot: graceDetail, cardID: m.detail.TaskID()}
	}
	return graceID{}
}

// trackGrace arms or releases the grace when the destructive prompt on screen
// changed across one Update.
func (m *Model) trackGrace(before graceID) tea.Cmd {
	after := m.graceIdentity()
	if before == after {
		return nil
	}
	timing := m.themeStyles().Timing
	var commands []tea.Cmd
	if before.set() {
		commands = append(commands, m.grace.close(before, timing))
	}
	if after.set() {
		commands = append(commands, m.grace.open(after, timing))
	}
	return batchCommands(commands...)
}
