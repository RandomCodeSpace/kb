package tui

import (
	"strings"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// The board's own motion, spec sections 10.2.4, 10.3.4 and 10.8.4.
//
// Two chains live here and both are self-terminating by construction
// (determinism contract, item 3): the plain-tier busy spinner, whose command is
// derived from the busy predicate on every tick rather than from a flag a code
// path can forget to clear, and the scroll-activity linger, whose expiry
// carries the sequence it was scheduled for.
//
// Every board wait is plain tier. Spec section 10.2.4 assigns the branded tier
// by latency - forge and AI round trips - and the board's waits are a local
// store read and a local store write, however important the write is. The board
// mounts no branded engine at all, which is also why internal/tui/motion.go's
// concurrency ceiling does not name it.

// boardBusyLabel is the board's plain-tier busy state, or the empty string when
// the board is not busy. The strings are the normative copy of spec section
// 10.8.7: lowercase, present continuous, and carrying no ellipsis, because the
// animation is the ellipsis.
func (m Model) boardBusyLabel() string {
	switch {
	case m.move.saving:
		return "saving move"
	case m.action.busy:
		return "saving"
	case m.boardLoading():
		return "loading board"
	default:
		return ""
	}
}

// boardLoading reports whether the first snapshot is still outstanding. It is
// the gate on both the footer's busy line and the column body's, so the two can
// never disagree about whether the board has loaded.
func (m Model) boardLoading() bool {
	return (m.loading && !m.haveBoardSnapshot) || (m.watcher != nil && !m.haveVersion)
}

// boardBusy is the gate that owns the tick chain.
//
// The launch screen is excluded by rule 4 of spec section 10.8.4: at most one
// spinner animates on a surface, and while the launch reveal is running it is
// the one that does. The busy row still renders its label - the state resolver
// is shared with the launch screen's meta row (spec section 10.6.5) - it just
// renders it with no frame, and no second chain is armed to drive one.
func (m Model) boardBusy() bool { return m.boardBusyLabel() != "" && !m.launching() }

// boardSpinTick advances the plain busy indicator. The gate is read on every
// tick, so the chain stops the moment the board settles and an idle board costs
// no timer.
func (m *Model) boardSpinTick(msg spinner.TickMsg) tea.Cmd {
	if !m.boardBusy() {
		return nil
	}
	var command tea.Cmd
	m.spin, command = m.spin.Update(msg)
	return command
}

// trackBoardBusy opens the chain when the board became busy across one Update.
// A board that was already busy keeps the chain it has, so a second write does
// not start a second tick loop.
func (m Model) trackBoardBusy(before bool) tea.Cmd {
	if before || !m.boardBusy() {
		return nil
	}
	return m.spin.Tick
}

// busyFrame is the plain tier's current frame, already laid onto a surface, or
// the empty string when the board is not busy. Spec section 10.2.4: the plain
// tier is bubbles dots in FgSubtle, flat, with no ramp - a branded frame spent
// on a local store write would stop meaning anything.
func (m Model) busyFrame(styles *theme.Styles, on theme.Slot) string {
	frame := m.busyFrameText()
	if frame == "" {
		return ""
	}
	return styles.On(theme.FgSubtle, on).Render(frame)
}

// busyFrameText is the frame as plain text, for the footer's state segment.
// That segment is rendered as one styled run carrying the state's own hue, so a
// frame with a color of its own would close with a reset and drop the segment
// for every cell after it.
// The trailing space bubbles ships inside spinner.Dot's own frames is trimmed:
// the gap between a frame and its label is BusyGap (spec section 10.8.2) and a
// component's padding is not a design token.
func (m Model) busyFrameText() string {
	if !m.boardBusy() || len(m.spin.Spinner.Frames) == 0 {
		return ""
	}
	return strings.TrimSpace(ansi.Strip(m.spin.View()))
}

// scrollSettledMsg ends one column's scroll linger.
type scrollSettledMsg struct {
	column int
	seq    uint64
}

// boardScrollState is the scroll activity of spec section 10.3.4. kb dims, it
// does not hide: the affordance column is reserved for the whole time the body
// overflows, and this state governs the tint alone. The settled form is the
// muted one, so a golden captures it on initial paint with no edit and the
// active form is a unit test that injects the scroll message.
type boardScrollState struct {
	active [len(boardStatuses)]bool
	seq    [len(boardStatuses)]uint64
}

// touch marks a column as just-scrolled and returns the sequence its expiry
// must carry. The guard is the same one the footer notice runs: without it a
// scroll at t+1s is settled by the previous scroll's expiry at t+2s.
func (s *boardScrollState) touch(column int) (uint64, bool) {
	if column < 0 || column >= len(s.active) {
		return 0, false
	}
	s.active[column] = true
	s.seq[column]++
	return s.seq[column], true
}

// lingering reports whether a column is inside ScrollActiveLinger of its last
// scroll.
func (s boardScrollState) lingering(column int) bool {
	return column >= 0 && column < len(s.active) && s.active[column]
}

// settle consumes an expiry. A stale sequence is dropped rather than clearing a
// linger that a later scroll re-armed.
func (s *boardScrollState) settle(msg scrollSettledMsg) {
	if msg.column < 0 || msg.column >= len(s.active) || msg.seq != s.seq[msg.column] {
		return
	}
	s.active[msg.column] = false
}

// armScrollLinger records the scroll and schedules its expiry.
func (m *Model) armScrollLinger(column int) tea.Cmd {
	seq, ok := m.scroll.touch(column)
	if !ok {
		return nil
	}
	return theme.Tick(m.themeStyles().Timing.ScrollActiveLinger,
		scrollSettledMsg{column: column, seq: seq})
}

// settleScroll consumes the linger expiry ahead of routing, the way the footer
// notice TTL is consumed: it is a timer, not an interaction, and no surface
// above the board has any business seeing it.
func (m *Model) settleScroll(message tea.Msg) bool {
	msg, ok := message.(scrollSettledMsg)
	if !ok {
		return false
	}
	m.scroll.settle(msg)
	return true
}
