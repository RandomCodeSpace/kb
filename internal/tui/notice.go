package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// The footer notice TTL of spec section 10.3.7.
//
// kb's footer notice - m.actionNotice and m.move.notice - takes over the
// footer's state segment and was cleared only by the next board user input.
// That is a defect, not a design: noticeOwnsFooter suppresses the entire hint
// ladder and its hit regions, so a board left unattended after a move keeps a
// stale "moved to DOING" where its affordances should be, indefinitely.
//
// Timing.NoticeTTL is adopted as a second dismissal path. Input dismissal stays
// and remains the faster one. Two rules keep it honest:
//
//   - Sequence guard. noticeSeq increments on every raise; the expiry message
//     carries the sequence it was scheduled for and is dropped when it does not
//     match. Without it, a notice raised at t+1s is killed by the previous
//     notice's expiry at t+5s.
//   - Errors are not notices. loadErr, pollErr and preferenceErr are state:
//     they clear when the condition clears and they never expire on a timer. A
//     board that cannot reach its store says so until it can.
//
// This deliberately changes behavior frozen in v1.0.1 (spec section 10.9 call
// 8).

type noticeExpiredMsg struct{ seq uint64 }

// noticeKey identifies the notice currently owning the footer, or the empty
// string when none does. The text is part of the identity so that replacing one
// notice with another counts as a raise and restarts the TTL.
//
// The three error slots are deliberately absent: they are state, not notices.
func (m Model) noticeKey() string {
	if m.actionNotice && m.actionStatus != "" {
		return "action:" + m.actionStatus
	}
	if m.move.notice && m.move.status != "" {
		return "move:" + m.move.status
	}
	return ""
}

// trackNotice schedules the TTL when the footer notice changed across one
// Update. A notice that is unchanged keeps the expiry it already has, so a
// repeated identical raise cannot extend a standing notice indefinitely.
func (m *Model) trackNotice(before string) tea.Cmd {
	after := m.noticeKey()
	if after == "" || after == before {
		return nil
	}
	m.noticeSeq++
	return theme.Tick(m.themeStyles().Timing.NoticeTTL, noticeExpiredMsg{seq: m.noticeSeq})
}

// expireNotice consumes the TTL message. A stale sequence is dropped; a live
// one clears both notice flags, leaving errors and an in-flight move alone.
func (m *Model) expireNotice(message tea.Msg) bool {
	msg, ok := message.(noticeExpiredMsg)
	if !ok {
		return false
	}
	if msg.seq != m.noticeSeq {
		return true
	}
	m.actionNotice = false
	if m.move.lifted == nil && !m.move.saving {
		if m.move.notice {
			m.move.notice = false
			m.move.status = ""
			m.move.statusError = false
		}
	}
	return true
}
