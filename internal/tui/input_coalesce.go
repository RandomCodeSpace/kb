package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// inputCoalescer is the program-level input filter of spec section 10.3.6. It
// throttles pointer motion and wheel messages to Timing.InputCoalesce, which is
// why scrolling feels smooth without any smooth-scroll code.
//
// Three rules govern it:
//
//   - Motion coalescing keeps the last message, wheel coalescing sums. Dropping
//     a wheel notch loses distance the user asked for; dropping an intermediate
//     motion coordinate loses nothing, because hover is resolved from the latest
//     coordinate at draw time.
//   - The accumulator resets on direction reversal. Without it a flick up
//     followed immediately by a flick down inside one window nets to zero and
//     the board does not move.
//   - Per-notch distance is unchanged. Coalescing changes how many notches
//     arrive per message, never how far one notch travels.
//
// The type is a pure state machine over an injected clock, so every rule above
// is a unit test that steps the clock rather than sleeps against it.
type inputCoalescer struct {
	window time.Duration
	now    func() time.Time
	// send re-injects the surplus notches of a coalesced flush. bubbletea
	// dispatches only its own four concrete mouse types to a View's OnMouse
	// handler, so a summed flush of n notches leaves as n messages rather than
	// as one message carrying n: the sum is what the filter computes, the
	// re-injection is how kb's pointer plumbing can receive it. A nil send
	// disables wheel coalescing outright rather than dropping distance.
	send func(tea.Msg)

	motionAt   time.Time
	haveMotion bool

	wheelAt   time.Time
	haveWheel bool
	lastWheel tea.MouseWheelMsg
	// pending is the signed notch backlog of the open window.
	pending int
	// injected counts re-injected notches still in flight. They pass straight
	// through so a flush cannot feed itself back into the accumulator.
	injected int
}

// newInputCoalescer builds the filter for one program run. A non-positive
// window disables coalescing: collapsed timing is no timing, and a filter that
// throttles on a zero window would drop input on a clock that does not run.
func newInputCoalescer(window time.Duration, now func() time.Time, send func(tea.Msg)) *inputCoalescer {
	if now == nil {
		now = time.Now
	}
	return &inputCoalescer{window: window, now: now, send: send}
}

// Filter is the tea.WithFilter callback. It returns nil for a message the
// window absorbs, and the message itself otherwise.
func (c *inputCoalescer) Filter(_ tea.Model, message tea.Msg) tea.Msg {
	if c == nil || c.window <= 0 {
		return message
	}
	if wheel, ok := message.(tea.MouseWheelMsg); ok {
		return c.wheel(wheel)
	}
	// A backlog left by a flick that stopped mid-window is flushed by the next
	// message of any kind. Without this the trailing notches of every gesture
	// are exactly the distance the accumulator exists to keep.
	c.drainStale()
	if motion, ok := message.(tea.MouseMotionMsg); ok {
		return c.motion(motion)
	}
	return message
}

// drainStale re-injects a backlog whose window has closed with no further
// wheel message to flush it.
func (c *inputCoalescer) drainStale() {
	if c.pending == 0 || !c.haveWheel || c.now().Sub(c.wheelAt) < c.window {
		return
	}
	surplus := abs(c.pending)
	c.pending = 0
	for range surplus {
		c.send(c.lastWheel)
	}
	c.injected += surplus
}

// motion drops every coordinate inside the open window. The first motion after
// the window carries the latest coordinate, which is the whole of what hover
// resolves against.
func (c *inputCoalescer) motion(msg tea.MouseMotionMsg) tea.Msg {
	now := c.now()
	if c.haveMotion && now.Sub(c.motionAt) < c.window {
		return nil
	}
	c.haveMotion = true
	c.motionAt = now
	return msg
}

// wheel accumulates notches inside the window and flushes the sum when it
// opens. Every notch of one flush shares a direction by construction, because
// a reversal zeroes the backlog before the new notch is added.
func (c *inputCoalescer) wheel(msg tea.MouseWheelMsg) tea.Msg {
	delta, ok := wheelNotch(msg.Button)
	if !ok || c.send == nil {
		return msg
	}
	if c.injected > 0 {
		c.injected--
		return msg
	}
	now := c.now()
	c.lastWheel = msg
	if c.haveWheel && now.Sub(c.wheelAt) < c.window {
		c.accumulate(delta)
		return nil
	}
	c.haveWheel = true
	c.wheelAt = now
	c.accumulate(delta)
	total := c.pending
	c.pending = 0
	surplus := abs(total) - 1
	for range surplus {
		c.send(msg)
	}
	c.injected += surplus
	return msg
}

// accumulate adds one notch, resetting the backlog when the direction reverses.
func (c *inputCoalescer) accumulate(delta int) {
	if c.pending != 0 && (c.pending > 0) != (delta > 0) {
		c.pending = 0
	}
	c.pending += delta
}

// wheelNotch resolves a vertical wheel button to a signed notch. Horizontal
// wheels are not coalesced: kb binds no horizontal scroll, and summing a wheel
// axis nothing consumes would only defer the message.
func wheelNotch(button tea.MouseButton) (int, bool) {
	switch button {
	case tea.MouseWheelUp:
		return -1, true
	case tea.MouseWheelDown:
		return 1, true
	default:
		return 0, false
	}
}

// asyncSender re-injects a coalesced flush from off the event loop. The filter
// runs on the loop's own goroutine, where a send into the program's message
// channel would deadlock; the notches of one flush are identical by
// construction, so their arrival order carries no information to lose.
func asyncSender(send func(tea.Msg)) func(tea.Msg) {
	return func(message tea.Msg) { go send(message) }
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
