package pointer

import (
	"strconv"
	"strings"
)

// Rows maps a hovered control id onto a row index of one surface. A control
// that belongs to a different surface, or to no row at all, reports false: that
// is how a surface decides whether mouse mode is on for it rather than for the
// panel next door.
type Rows func(ControlID) (int, bool)

// Choice is the mouse-mode machine of spec section 10.5.2, for one overlay
// choice surface. Ratified call 9: the machine runs on the checklist, the drift
// list, the ADR-split stories, the issue-import list, the pickers and the
// settings rows, and on nothing else. The board cursor never follows the
// pointer, because it is the drag source, the anchor every board keybinding
// resolves against and the card the detail overlay opens.
//
// The machine is pure. It holds no clock, no flag and no copy of hover: mouse
// mode is on exactly when the caller's State has a hovered id that Rows
// resolves, so there is one bit of state and it lives in State.
type Choice struct {
	Cursor int  // the keyboard cursor
	Rows   Rows // this surface's own id-to-row resolver
}

// Mode reports whether mouse mode is on for this surface.
func (c Choice) Mode(state State) bool {
	_, ok := c.row(state)
	return ok
}

// Acting returns the row that renders this surface's cursor cue: the hovered
// row while mouse mode is on, the keyboard cursor otherwise. Exactly one cursor
// is visible at any moment, which is the failure the machine exists to prevent.
func (c Choice) Acting(state State) int {
	if row, ok := c.row(state); ok {
		return row
	}
	return c.Cursor
}

// Adopt applies rows 7 and 8 to one key press and returns the anchor the key's
// own motion runs from, plus the state with mouse mode turned off.
//
// The ordering of row 7 is normative and is the row most easily got wrong:
// adopt, then move. A down arrow while row 7 is hovered lands on row 8, not on
// cursor+1, so the caller applies its own motion to the returned anchor. Row 8
// is the opposite and equally deliberate: a hotkey, Enter or Esc acts on the
// keyboard cursor, never on whatever the pointer happens to be resting over,
// because a key typed without looking at the mouse must not be redirected by it.
//
// A press while mouse mode is off for this surface leaves both results alone.
func (c Choice) Adopt(state State, arrow bool) (int, State) {
	row, ok := c.row(state)
	if !ok {
		return c.Cursor, state
	}
	if !arrow {
		return c.Cursor, state.ClearHover()
	}
	return row, state.ClearHover()
}

// row resolves the hovered control against this surface.
func (c Choice) row(state State) (int, bool) {
	if state.hovered == "" || c.Rows == nil {
		return 0, false
	}
	return c.Rows(state.hovered)
}

// RowsWithPrefix is the resolver for the surfaces that key a row's control id
// as a fixed prefix plus the row index, which is every choice surface in the
// TUI. An id that does not carry the prefix, or whose tail is not a
// non-negative decimal, belongs to another surface.
func RowsWithPrefix(prefix string) Rows {
	return func(id ControlID) (int, bool) {
		tail, ok := strings.CutPrefix(string(id), prefix)
		if !ok || tail == "" {
			return 0, false
		}
		row, err := strconv.Atoi(tail)
		if err != nil || row < 0 || strconv.Itoa(row) != tail {
			return 0, false
		}
		return row, true
	}
}
