package pointer

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// Clicks is the double-click classifier of spec section 10.3.5. It lives here
// rather than in a view because classification is a pointer concern: a view
// that had to remember the previous click would grow a second, parallel gesture
// machine beside this one.
//
// A click is a double-click when it lands within Timing.DoubleClickWindow of
// the previous click, on the same region id, and the previous click's gesture
// ended with dragged == false. A click on a different id resets the window; so
// does any drag. The drag exclusion is not optional - kb's board is a
// drag-and-drop miller board and a lift that ends on its origin must never
// register as a double-click.
//
// The window closes because a message arrived, not because a render compared
// time.Since to a token: Click hands back the command that arms it, and Expire
// consumes it. Under collapsed timing the arming command dispatches
// immediately, so a collapsed program classifies every click as a single -
// which is the deterministic reading a golden needs.
type Clicks struct {
	armed ControlID
	seq   uint64
}

// clickWindowMsg closes an armed double-click window. The tag is the generation
// guard: a window armed at t+1 must not be closed by the previous window's
// expiry.
type clickWindowMsg struct{ tag uint64 }

// Armed reports whether a click window is open, and on which region.
func (c Clicks) Armed() (ControlID, bool) { return c.armed, c.armed != "" }

// Click records a completed click on id and reports whether it closes a
// double-click. dragged excludes a lift that ended on its origin.
//
// The returned command arms the window for the next click. It is nil when this
// click was itself the second half of a double-click, and when the gesture
// dragged: neither leaves a window open.
func (c Clicks) Click(id ControlID, dragged bool, window time.Duration) (Clicks, bool, tea.Cmd) {
	if id == "" || dragged {
		c.armed = ""
		c.seq++
		return c, false, nil
	}
	if c.armed == id {
		c.armed = ""
		c.seq++
		return c, true, nil
	}
	c.armed = id
	c.seq++
	return c, false, theme.Tick(window, clickWindowMsg{tag: c.seq})
}

// Reset closes any open window. A drag, a resize or an overlay taking the input
// focus all end the gesture the window belonged to.
func (c Clicks) Reset() Clicks {
	c.armed = ""
	c.seq++
	return c
}

// Expire consumes the window-expiry message. The second return reports whether
// the message belonged to this machine, so a caller can stop routing it.
func (c Clicks) Expire(message tea.Msg) (Clicks, bool) {
	msg, ok := message.(clickWindowMsg)
	if !ok {
		return c, false
	}
	if msg.tag == c.seq {
		c.armed = ""
	}
	return c, true
}
