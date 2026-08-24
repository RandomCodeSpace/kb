package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/board"
)

// noticeModel is a board carrying a raised action notice, the state the footer
// hands its whole hint ladder over to.
func noticeModel(t *testing.T) Model {
	t.Helper()
	m, _, tasks := actionTestModel(t, board.Task{Title: "Noticed", Status: board.StatusTodo})
	m.width, m.height = 90, 24
	m.boardView.focusTask(m.filteredBoard(), tasks[0].ID)
	m.setActionStatus("Shipped Noticed", false)
	return m
}

func TestNoticeKeyIgnoresErrorState(t *testing.T) {
	m := noticeModel(t)
	if m.noticeKey() != "action:Shipped Noticed" {
		t.Fatalf("action notice key = %q", m.noticeKey())
	}
	m.actionNotice = false
	m.loadErr = errors.New("store unreachable")
	m.pollErr = errors.New("watcher unreachable")
	m.preferenceErr = errors.New("preferences unreadable")
	if key := m.noticeKey(); key != "" {
		t.Fatalf("an error raised a notice key %q", key)
	}
	m.move.notice = true
	m.move.status = "Move cancelled: A restored."
	if key := m.noticeKey(); key != "move:Move cancelled: A restored." {
		t.Fatalf("move notice key = %q", key)
	}
}

// TestNoticeTTLRestoresTheHintLadder is the defect spec section 10.3.7 names:
// noticeOwnsFooter suppressed the whole hint ladder and its hit regions while a
// notice stood, indefinitely.
func TestNoticeTTLRestoresTheHintLadder(t *testing.T) {
	m := noticeModel(t)
	if !m.noticeOwnsFooter() {
		t.Fatal("a raised notice does not own the footer")
	}
	beforeContent, beforeHits := m.renderBoard()
	if strings.Contains(beforeContent, "? help") {
		t.Fatal("the hint ladder is visible while a notice stands")
	}

	m.noticeSeq = 7
	updated, _ := m.Update(noticeExpiredMsg{seq: 3})
	m = updated.(Model)
	if !m.actionNotice {
		t.Fatal("a stale expiry dismissed the notice")
	}
	updated, _ = m.Update(noticeExpiredMsg{seq: 7})
	m = updated.(Model)
	if m.actionNotice || m.noticeOwnsFooter() {
		t.Fatal("the live expiry did not dismiss the notice")
	}
	afterContent, afterHits := m.renderBoard()
	if !strings.Contains(afterContent, "? help") {
		t.Fatal("the hint ladder did not come back")
	}
	if len(afterHits) <= len(beforeHits) {
		t.Fatalf("footer hit regions did not come back: %d then %d", len(beforeHits), len(afterHits))
	}
}

// TestNoticeTTLIsScheduledOnEveryRaise pins the sequence guard: a notice raised
// while an older one stands takes ownership of its own expiry.
func TestNoticeTTLIsScheduledOnEveryRaise(t *testing.T) {
	m, _, tasks := actionTestModel(t, board.Task{
		Title: "Already cancelled", Status: board.StatusCancelled,
	})
	m.boardView.showCancelled = true
	m.boardView.focusTask(m.filteredBoard(), tasks[0].ID)

	// x on a cancelled card raises the "already Cancelled" notice.
	command := updateTestModel(t, &m, tea.KeyPressMsg{Code: 'x', Text: "x"})
	if !m.actionNotice || m.noticeSeq != 1 {
		t.Fatalf("first raise = notice %v seq %d", m.actionNotice, m.noticeSeq)
	}
	assertNoDomainMessage(t, "notice raise", command)

	before := m.noticeSeq
	updateTestModel(t, &m, tea.KeyPressMsg{Code: 't', Text: "t"})
	if m.noticeSeq == before {
		t.Fatal("replacing a notice did not take a new sequence")
	}
	// The superseded expiry must not dismiss the notice that replaced it.
	updated, _ := m.Update(noticeExpiredMsg{seq: before})
	m = updated.(Model)
	if !m.actionNotice {
		t.Fatal("a superseded expiry dismissed the newer notice")
	}
}

// TestNoticeInputDismissalStaysTheFasterPath: the TTL is a second route, not a
// replacement.
func TestNoticeInputDismissalStaysTheFasterPath(t *testing.T) {
	m := noticeModel(t)
	updateTestModel(t, &m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	if m.actionNotice {
		t.Fatal("board input no longer dismisses a notice")
	}
}

// TestNoticeTTLLeavesAnInFlightMoveAlone keeps the timer off a status that is
// still describing something in progress.
func TestNoticeTTLLeavesAnInFlightMoveAlone(t *testing.T) {
	m := noticeModel(t)
	m.actionNotice = false
	m.move.notice = true
	m.move.status = "Move cancelled: A restored."
	m.move.saving = true
	m.noticeSeq = 2
	updated, _ := m.Update(noticeExpiredMsg{seq: 2})
	m = updated.(Model)
	if !m.move.notice {
		t.Fatal("the TTL dismissed a notice describing an in-flight move")
	}
}

// TestNoticeExpiryLeavesTheFrameByteStable is the determinism half: the settled
// frame after expiry is the frame with no notice, and expiring again is inert.
func TestNoticeExpiryLeavesTheFrameByteStable(t *testing.T) {
	m := noticeModel(t)
	m.noticeSeq = 1
	updated, _ := m.Update(noticeExpiredMsg{seq: 1})
	m = updated.(Model)
	settled := m.View().Content
	updated, command := m.Update(noticeExpiredMsg{seq: 1})
	m = updated.(Model)
	if command != nil {
		t.Fatal("a second expiry rescheduled itself")
	}
	if got := m.View().Content; got != settled {
		t.Fatal("a repeated expiry changed the settled frame")
	}
}
