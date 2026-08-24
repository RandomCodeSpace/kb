package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// The ship celebration of issue #191: the board's one moment of applause, spent
// on the transition a card makes into DONE and on nothing else.
//
// It is a flash, not a sweep. Spec section 10.1.2 spends the gradient budget on
// four surfaces and closes it - "a fifth surface is a spec change and comes back
// here" - so a celebration ramp is not a slice decision to make. What is left is
// the conservative form: the DONE column's meta row (spec section 2.3) pulses
// from FgMuted to StatusOK, the completion hue section 1.5 already owns, twice,
// over CelebrateSteps ticks, and then it is over.
//
// The row is chosen because it is the row that just changed. "4 cards" became
// "5 cards" on the drop, the row is column chrome rather than anything a user
// reads as data, and every neighbouring surface is spoken for: the header band
// never re-hues (spec section 10.1.4), the card rail is priority-hued in every
// state including selection (spec section 2.4), and the scroll affordance's tint
// is the scroll linger's (spec section 10.3.4).
//
// Class B, suppressed rather than approximated (spec section 10.7.6). The whole
// signal is a color difference the settled frame does not contain, so below
// FidelityFull the effect does not run and - the second half of the rule - its
// tick chain is never armed. That gate is read once, at the arm, and never on a
// render path (spec section 10.7.5).
//
// The determinism contract holds by construction: the settled state is the
// absence of the effect, so View() is tick-invariant once the chain ends; the
// chain terminates on its own step count; and every intermediate frame is a unit
// test stepping shipCelebrationMsg synchronously, never a golden.

// shipCelebrationMsg advances the flash. It carries the generation it was
// scheduled for, the same guard the footer notice TTL and the scroll linger run:
// a second ship mid-flourish restarts the effect, and the first chain's next
// message must die rather than terminate the second one.
type shipCelebrationMsg struct{ gen uint64 }

// shipCelebration is the effect's whole state. The zero value is settled.
type shipCelebration struct {
	status board.Status
	step   int
	gen    uint64
	active bool
}

// lit reports whether the current step falls in a lit phase. Beats alternate
// from lit, so a celebration that is cut short by a busy state still had its
// first frame be the bright one.
func (c shipCelebration) lit(timing theme.Timing) bool {
	beat := timing.CelebrateBeat()
	if !c.active || beat <= 0 {
		return false
	}
	return (c.step/beat)%2 == 0
}

// celebrateShip arms the flourish for a card that just landed in DONE, or
// returns nil when it must not run.
//
// Four gates, and every one of them is a rule from somewhere else: the terminal
// floor (class B, spec section 10.7.6), the collapse rule (a zero token means
// "do not run", spec section 10.3.1), and one motion at a time - rule 4 of spec
// section 10.8.4 gives a busy surface's single animation to the busy state and
// the launch surface's to the brand reveal.
func (m *Model) celebrateShip() tea.Cmd {
	styles := m.themeStyles()
	timing := styles.Timing
	if !styles.Graded() || timing.CelebrateSteps <= 0 || timing.Interval() <= 0 {
		return nil
	}
	if m.boardBusy() || m.launching() {
		return nil
	}
	m.celebrateGen++
	m.celebrate = shipCelebration{
		status: board.StatusDone,
		gen:    m.celebrateGen,
		active: true,
	}
	return theme.Tick(timing.Interval(), shipCelebrationMsg{gen: m.celebrateGen})
}

// stepCelebration advances the flash and reports whether it consumed the
// message. Like the scroll linger's expiry it is consumed ahead of routing: it
// is a timer, and no surface above the board has any business seeing it.
//
// The gate owns the chain (spec section 10.2.3). The busy predicate is read on
// every step rather than at the arm alone, so a write that starts while the
// board is applauding takes the surface's one motion back on the same update it
// began, and the flourish ends settled instead of ticking underneath it.
func (m *Model) stepCelebration(message tea.Msg) (tea.Cmd, bool) {
	msg, ok := message.(shipCelebrationMsg)
	if !ok {
		return nil, false
	}
	if msg.gen != m.celebrateGen || !m.celebrate.active {
		return nil, true
	}
	timing := m.themeStyles().Timing
	m.celebrate.step++
	if m.celebrate.step >= timing.CelebrateSteps || m.boardBusy() || m.launching() {
		m.celebrate = shipCelebration{}
		return nil, true
	}
	return theme.Tick(timing.Interval(), shipCelebrationMsg{gen: msg.gen}), true
}

// celebrateLit reports whether one column is drawing its meta row in the
// celebration's lit phase. It is the only read on a render path, it takes no
// clock, and it is false in the settled state - which is what makes the settled
// frame tick-invariant.
func (m Model) celebrateLit(status board.Status) bool {
	if !m.celebrate.active || m.celebrate.status != status {
		return false
	}
	if m.boardBusy() || m.launching() {
		return false
	}
	return m.celebrate.lit(m.themeStyles().Timing)
}
