package tui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// stepClock is the injected clock every coalescer test drives. No test under
// internal/tui sleeps (spec section 10.3.9 rule 5), so the window is advanced
// by assignment rather than by waiting.
type stepClock struct{ at time.Time }

func (c *stepClock) now() time.Time { return c.at }

func (c *stepClock) advance(d time.Duration) { c.at = c.at.Add(d) }

func newTestCoalescer(window time.Duration) (*inputCoalescer, *stepClock, *[]tea.Msg) {
	clock := &stepClock{at: time.Unix(0, 0)}
	var sent []tea.Msg
	coalescer := newInputCoalescer(window, clock.now, func(message tea.Msg) {
		sent = append(sent, message)
	})
	return coalescer, clock, &sent
}

func wheelDown(x, y int) tea.MouseWheelMsg {
	return tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelDown}
}

func wheelUp(x, y int) tea.MouseWheelMsg {
	return tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelUp}
}

func TestCoalescerThrottlesMotionAndKeepsTheLast(t *testing.T) {
	coalescer, clock, _ := newTestCoalescer(16 * time.Millisecond)
	first := tea.MouseMotionMsg{X: 1, Y: 1}
	if got := coalescer.Filter(nil, first); got != tea.Msg(first) {
		t.Fatalf("first motion = %v, want it delivered", got)
	}
	clock.advance(5 * time.Millisecond)
	if got := coalescer.Filter(nil, tea.MouseMotionMsg{X: 2, Y: 2}); got != nil {
		t.Fatalf("intermediate motion = %v, want dropped", got)
	}
	clock.advance(5 * time.Millisecond)
	if got := coalescer.Filter(nil, tea.MouseMotionMsg{X: 3, Y: 3}); got != nil {
		t.Fatalf("intermediate motion = %v, want dropped", got)
	}
	clock.advance(6 * time.Millisecond)
	last := tea.MouseMotionMsg{X: 4, Y: 4}
	if got := coalescer.Filter(nil, last); got != tea.Msg(last) {
		t.Fatalf("motion after the window = %v, want the latest coordinate", got)
	}
}

func TestCoalescerLeavesOtherMessagesAlone(t *testing.T) {
	coalescer, _, _ := newTestCoalescer(16 * time.Millisecond)
	for _, message := range []tea.Msg{
		tea.KeyPressMsg{Code: 'j', Text: "j"},
		tea.MouseClickMsg{X: 1, Y: 1, Button: tea.MouseLeft},
		tea.MouseReleaseMsg{X: 1, Y: 1, Button: tea.MouseLeft},
		pollTickMsg{},
	} {
		if got := coalescer.Filter(nil, message); got != message {
			t.Fatalf("%T = %v, want it delivered untouched", message, got)
		}
	}
}

// TestCoalescerSumsWheelNotches is the rule that separates wheel from motion:
// a dropped notch is distance the user asked for, so the window sums instead.
func TestCoalescerSumsWheelNotches(t *testing.T) {
	coalescer, clock, sent := newTestCoalescer(16 * time.Millisecond)
	if got := coalescer.Filter(nil, wheelDown(3, 4)); got == nil {
		t.Fatal("first notch was dropped")
	}
	if len(*sent) != 0 {
		t.Fatalf("first notch re-injected %d messages", len(*sent))
	}
	clock.advance(4 * time.Millisecond)
	for range 3 {
		if got := coalescer.Filter(nil, wheelDown(3, 4)); got != nil {
			t.Fatalf("notch inside the window = %v, want accumulated", got)
		}
	}
	clock.advance(20 * time.Millisecond)
	if got := coalescer.Filter(nil, wheelDown(3, 4)); got == nil {
		t.Fatal("flush notch was dropped")
	}
	// Three accumulated plus the flushing notch is four notches of travel: one
	// arrives as the returned message, three as re-injections.
	if len(*sent) != 3 {
		t.Fatalf("flush re-injected %d notches, want 3", len(*sent))
	}
	for index, message := range *sent {
		wheel, ok := message.(tea.MouseWheelMsg)
		if !ok || wheel.Button != tea.MouseWheelDown {
			t.Fatalf("re-injected notch %d = %#v, want a wheel-down", index, message)
		}
	}
}

// TestCoalescerResetsOnDirectionReversal pins the rule without which a flick up
// followed immediately by a flick down nets to zero and the board does not move.
func TestCoalescerResetsOnDirectionReversal(t *testing.T) {
	coalescer, clock, sent := newTestCoalescer(16 * time.Millisecond)
	coalescer.Filter(nil, wheelUp(1, 1))
	clock.advance(2 * time.Millisecond)
	coalescer.Filter(nil, wheelUp(1, 1))
	coalescer.Filter(nil, wheelUp(1, 1))
	if coalescer.pending != -2 {
		t.Fatalf("upward backlog = %d, want -2", coalescer.pending)
	}
	coalescer.Filter(nil, wheelDown(1, 1))
	if coalescer.pending != 1 {
		t.Fatalf("backlog after reversal = %d, want 1", coalescer.pending)
	}
	clock.advance(20 * time.Millisecond)
	coalescer.Filter(nil, wheelDown(1, 1))
	if len(*sent) != 1 {
		t.Fatalf("reversed flush re-injected %d notches, want 1", len(*sent))
	}
	wheel, ok := (*sent)[0].(tea.MouseWheelMsg)
	if !ok || wheel.Button != tea.MouseWheelDown {
		t.Fatalf("reversed flush direction = %#v, want a wheel-down", (*sent)[0])
	}
}

// TestCoalescerDrainsABacklogLeftByAStoppedFlick keeps the lossless guarantee
// when a gesture ends mid-window with nothing left to flush it.
func TestCoalescerDrainsABacklogLeftByAStoppedFlick(t *testing.T) {
	coalescer, clock, sent := newTestCoalescer(16 * time.Millisecond)
	coalescer.Filter(nil, wheelDown(1, 1))
	clock.advance(2 * time.Millisecond)
	coalescer.Filter(nil, wheelDown(1, 1))
	coalescer.Filter(nil, wheelDown(1, 1))
	clock.advance(20 * time.Millisecond)
	if got := coalescer.Filter(nil, pollTickMsg{}); got != (pollTickMsg{}) {
		t.Fatalf("unrelated message = %v, want it delivered", got)
	}
	if len(*sent) != 2 {
		t.Fatalf("stale backlog drained %d notches, want 2", len(*sent))
	}
	if coalescer.pending != 0 {
		t.Fatalf("backlog after drain = %d, want 0", coalescer.pending)
	}
}

// TestCoalescerPassesReinjectedNotchesThrough is the guard that keeps a flush
// from feeding itself back into its own accumulator.
func TestCoalescerPassesReinjectedNotchesThrough(t *testing.T) {
	coalescer, clock, sent := newTestCoalescer(16 * time.Millisecond)
	coalescer.Filter(nil, wheelDown(1, 1))
	clock.advance(2 * time.Millisecond)
	coalescer.Filter(nil, wheelDown(1, 1))
	clock.advance(20 * time.Millisecond)
	coalescer.Filter(nil, wheelDown(1, 1))
	if len(*sent) != 1 {
		t.Fatalf("flush re-injected %d notches, want 1", len(*sent))
	}
	// The re-injection arrives inside the window it was flushed in.
	if got := coalescer.Filter(nil, (*sent)[0]); got == nil {
		t.Fatal("re-injected notch was swallowed by its own accumulator")
	}
	if coalescer.pending != 0 || coalescer.injected != 0 {
		t.Fatalf("after drain backlog = %d injected = %d, want 0/0", coalescer.pending, coalescer.injected)
	}
}

// TestCoalescerIsInertWithoutATimingWindow is the collapse rule applied to the
// filter: zero means no timing, and no timing means nothing is held back.
func TestCoalescerIsInertWithoutATimingWindow(t *testing.T) {
	coalescer, _, sent := newTestCoalescer(0)
	for range 4 {
		if got := coalescer.Filter(nil, wheelDown(1, 1)); got == nil {
			t.Fatal("collapsed timing dropped a notch")
		}
		if got := coalescer.Filter(nil, tea.MouseMotionMsg{X: 1, Y: 1}); got == nil {
			t.Fatal("collapsed timing dropped a motion")
		}
	}
	if len(*sent) != 0 {
		t.Fatalf("collapsed timing re-injected %d messages", len(*sent))
	}
	var absent *inputCoalescer
	if got := absent.Filter(nil, wheelDown(1, 1)); got == nil {
		t.Fatal("a nil coalescer must not eat input")
	}
}

// TestCoalescerWithoutASenderDoesNotCoalesceWheels: without a way to deliver a
// summed flush, passing every notch through is the only lossless option.
func TestCoalescerWithoutASenderDoesNotCoalesceWheels(t *testing.T) {
	clock := &stepClock{at: time.Unix(0, 0)}
	coalescer := newInputCoalescer(16*time.Millisecond, clock.now, nil)
	for range 3 {
		if got := coalescer.Filter(nil, wheelDown(1, 1)); got == nil {
			t.Fatal("a senderless coalescer dropped a notch")
		}
	}
	if coalescer.pending != 0 {
		t.Fatalf("senderless backlog = %d, want 0", coalescer.pending)
	}
}

func TestCoalescerIgnoresHorizontalWheels(t *testing.T) {
	coalescer, _, sent := newTestCoalescer(16 * time.Millisecond)
	horizontal := tea.MouseWheelMsg{X: 1, Y: 1, Button: tea.MouseWheelLeft}
	if got := coalescer.Filter(nil, horizontal); got != tea.Msg(horizontal) {
		t.Fatalf("horizontal wheel = %v, want it delivered untouched", got)
	}
	if len(*sent) != 0 || coalescer.pending != 0 {
		t.Fatal("a horizontal wheel entered the vertical accumulator")
	}
}

func TestNewInputCoalescerDefaultsItsClock(t *testing.T) {
	coalescer := newInputCoalescer(time.Hour, nil, func(tea.Msg) {})
	if coalescer.now == nil {
		t.Fatal("a coalescer built without a clock has none")
	}
	if got := coalescer.Filter(nil, wheelDown(1, 1)); got == nil {
		t.Fatal("default clock dropped the first notch")
	}
}

// TestAsyncSenderLeavesTheEventLoop pins the one thing the re-injection must
// not do: block the goroutine the filter runs on.
func TestAsyncSenderLeavesTheEventLoop(t *testing.T) {
	delivered := make(chan tea.Msg, 1)
	send := asyncSender(func(message tea.Msg) { delivered <- message })
	send(pollTickMsg{})
	if got := <-delivered; got != (pollTickMsg{}) {
		t.Fatalf("asyncSender delivered %#v", got)
	}
}

func TestAbsIsSignless(t *testing.T) {
	if abs(-3) != 3 || abs(3) != 3 || abs(0) != 0 {
		t.Fatal("abs is not signless")
	}
}
