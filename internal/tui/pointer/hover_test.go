package pointer_test

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/tui/pointer"
)

// The mouse-mode machine of spec section 10.5.2, one test per numbered row of
// its table. No wall clock and no teatest: every row is a pure state
// transition, and none of them is timed.

const (
	rowOne   pointer.ControlID = "choice:0"
	rowTwo   pointer.ControlID = "choice:1"
	rowThree pointer.ControlID = "choice:2"
)

// choiceMap is one overlay choice surface: three stacked rows inside a panel,
// with the panel's surround registered as an unhoverable backdrop the way
// Map.AddBackdrop registers it.
func choiceMap() pointer.Map {
	var hits pointer.Map
	hits.AddBackdrop(
		pointer.Rect{X0: 0, Y0: 0, X1: 40, Y1: 20},
		pointer.Rect{X0: 4, Y0: 4, X1: 36, Y1: 7},
		func(pointer.Point) tea.Msg { return activatedMsg{name: "backdrop"} },
	)
	for index, id := range []pointer.ControlID{rowOne, rowTwo, rowThree} {
		name := string(id)
		hits.AddControl(id, pointer.Rect{X0: 4, Y0: 4 + index, X1: 36, Y1: 5 + index},
			func(point pointer.Point) tea.Msg { return activatedMsg{name: name, point: point} })
	}
	return hits
}

// choice is the machine under test, anchored on the keyboard cursor.
func choice(cursor int) pointer.Choice {
	return pointer.Choice{Cursor: cursor, Rows: pointer.RowsWithPrefix("choice:")}
}

// motion drives one snapshot with a bare motion and applies whatever feedback
// it produced to state.
func motion(t *testing.T, handler func(tea.MouseMsg) tea.Cmd, state pointer.State, x, y int) (pointer.State, bool) {
	t.Helper()
	command := handler(tea.MouseMotionMsg{X: x, Y: y})
	if command == nil {
		return state, false
	}
	next, _, consumed := state.Update(command())
	if !consumed {
		t.Fatalf("motion at (%d,%d) produced a message the pointer state did not consume", x, y)
	}
	return next, true
}

// TestMotionAtTheSameCellIsDroppedBeforeTheRegionScan is row 1. The guard is on
// raw cell coordinates and comes before any region is touched, so idle motion
// inside one large region costs one comparison rather than a linear scan.
func TestMotionAtTheSameCellIsDroppedBeforeTheRegionScan(t *testing.T) {
	handler := choiceMap().Handler()
	state, emitted := motion(t, handler, pointer.State{}, 10, 5)
	if !emitted || !state.IsHovered(rowTwo) {
		t.Fatalf("first motion hovered %q", state.Hovered())
	}
	if command := handler(tea.MouseMotionMsg{X: 10, Y: 5}); command != nil {
		t.Fatal("a repeat motion at the same cell produced feedback")
	}
	moved, emitted := motion(t, handler, state, 11, 5)
	if !emitted || !moved.IsHovered(rowTwo) {
		t.Fatalf("motion to a new cell in the same region hovered %q", moved.Hovered())
	}
}

// TestMotionOntoAHoverableRegionTurnsMouseModeOn is row 2.
func TestMotionOntoAHoverableRegionTurnsMouseModeOn(t *testing.T) {
	state, _ := motion(t, choiceMap().Handler(), pointer.State{}, 10, 6)
	if !state.IsHovered(rowThree) {
		t.Fatalf("hovered %q, want %q", state.Hovered(), rowThree)
	}
	point, ok := state.HoverPoint()
	if !ok || point != (pointer.Point{X: 10, Y: 6}) {
		t.Fatalf("hover point = (%+v, %v)", point, ok)
	}
	if !choice(0).Mode(state) {
		t.Fatal("mouse mode is off for the surface the hovered row belongs to")
	}
	if choice(0).Acting(state) != 2 {
		t.Fatalf("acting selection = %d, want the hovered row 2", choice(0).Acting(state))
	}
}

// TestMotionOntoNoRegionTurnsMouseModeOff is row 3: the keyboard cursor renders
// again where it stood. The panel's own backdrop is a region and not a hole, so
// the overlay consumes the motion rather than passing it down to the board.
func TestMotionOntoNoRegionTurnsMouseModeOff(t *testing.T) {
	handler := choiceMap().Handler()
	state, _ := motion(t, handler, pointer.State{}, 10, 5)
	cleared, emitted := motion(t, handler, state, 10, 18)
	if !emitted {
		t.Fatal("motion onto the backdrop produced no feedback")
	}
	if cleared.Hovered() != "" {
		t.Fatalf("hovered %q over the backdrop", cleared.Hovered())
	}
	if choice(1).Mode(cleared) || choice(1).Acting(cleared) != 1 {
		t.Fatalf("mouse mode stayed on: acting = %d", choice(1).Acting(cleared))
	}
}

// TestMotionWithTheLeftButtonHeldStaysOnTheDragPath is row 4: hover is neither
// read nor written while a press is active.
func TestMotionWithTheLeftButtonHeldStaysOnTheDragPath(t *testing.T) {
	handler := choiceMap().Handler()
	state, _ := motion(t, handler, pointer.State{}, 10, 4)
	if !state.IsHovered(rowOne) {
		t.Fatalf("hovered %q before the press", state.Hovered())
	}
	state, _, _ = state.Update(handler(tea.MouseClickMsg{X: 10, Y: 4, Button: tea.MouseLeft})())
	command := handler(tea.MouseMotionMsg{X: 12, Y: 4, Button: tea.MouseLeft})
	if command == nil {
		t.Fatal("a held drag produced no feedback")
	}
	dragged, _, _ := state.Update(command())
	if !dragged.IsHovered(rowOne) {
		t.Fatalf("the drag path rewrote hover to %q", dragged.Hovered())
	}
	if dragged.Active() {
		t.Fatal("the drag path left the press armed")
	}
}

// TestHoverIsRetainedAcrossTheClickAndReleaseGesture is row 5.
func TestHoverIsRetainedAcrossTheClickAndReleaseGesture(t *testing.T) {
	handler := choiceMap().Handler()
	state, _ := motion(t, handler, pointer.State{}, 10, 5)
	state, _, _ = state.Update(handler(tea.MouseClickMsg{X: 10, Y: 5, Button: tea.MouseLeft})())
	if !state.IsPressed(rowTwo) || !state.IsHovered(rowTwo) {
		t.Fatalf("press dropped hover: pressed=%v hovered=%q", state.Active(), state.Hovered())
	}
	released, command, _ := state.Update(handler(tea.MouseReleaseMsg{X: 10, Y: 5, Button: tea.MouseLeft})())
	if command == nil {
		t.Fatal("release did not activate the row")
	}
	if released.Active() {
		t.Fatal("release left the press armed")
	}
	if !released.IsHovered(rowTwo) {
		t.Fatalf("release cleared hover to %q", released.Hovered())
	}
}

// TestWheelRetainsHoverAndReresolvesAgainstTheNewFrame is row 6. The pointer
// can stand still while the content moves under it; without the re-resolve the
// hover stays lit on a row the pointer is no longer over and no further event
// ever corrects it.
func TestWheelRetainsHoverAndReresolvesAgainstTheNewFrame(t *testing.T) {
	var scrolled pointer.Map
	scrolled.AddWheel(pointer.Rect{X0: 0, Y0: 0, X1: 40, Y1: 20},
		func(delta int) tea.Msg { return scrolledMsg{name: "list", delta: delta} })
	for index, id := range []pointer.ControlID{rowOne, rowTwo, rowThree} {
		scrolled.AddControl(id, pointer.Rect{X0: 4, Y0: 4 + index, X1: 36, Y1: 5 + index},
			func(pointer.Point) tea.Msg { return activatedMsg{name: string(id)} })
	}
	state, _ := motion(t, scrolled.Handler(), pointer.State{}, 10, 5)
	if !state.IsHovered(rowTwo) {
		t.Fatalf("hovered %q before the wheel", state.Hovered())
	}
	if command := scrolled.Handler()(tea.MouseWheelMsg{X: 10, Y: 5, Button: tea.MouseWheelDown}); command != nil {
		next, _, _ := state.Update(command())
		state = next
	}
	if !state.IsHovered(rowTwo) {
		t.Fatalf("the wheel dropped the retained hover to %q", state.Hovered())
	}

	// The next frame scrolled the list by one row, so the same cell is a
	// different row and the row under the pointer changes without a motion.
	var after pointer.Map
	for index, id := range []pointer.ControlID{rowOne, rowTwo, rowThree} {
		after.AddControl(id, pointer.Rect{X0: 4, Y0: 3 + index, X1: 36, Y1: 4 + index},
			func(pointer.Point) tea.Msg { return activatedMsg{name: string(id)} })
	}
	if got := state.Reresolve(after); !got.IsHovered(rowThree) {
		t.Fatalf("re-resolve after the scroll hovered %q, want %q", got.Hovered(), rowThree)
	}
}

// TestArrowKeyAdoptsTheHoveredAnchorThenMoves is row 7, whose ordering is
// normative and is the row most easily got wrong: adopt, then move. A down
// arrow while row 2 is hovered lands on row 3, not on cursor+1.
func TestArrowKeyAdoptsTheHoveredAnchorThenMoves(t *testing.T) {
	state, _ := motion(t, choiceMap().Handler(), pointer.State{}, 10, 6)
	anchor, next := choice(0).Adopt(state, true)
	if anchor != 2 {
		t.Fatalf("adopted anchor = %d, want the hovered row 2", anchor)
	}
	if anchor+1 != 3 {
		t.Fatalf("moving from the adopted anchor lands on %d, want 3", anchor+1)
	}
	if next.Hovered() != "" || choice(0).Mode(next) {
		t.Fatalf("mouse mode stayed on after the arrow: hovered %q", next.Hovered())
	}
}

// TestAnyOtherKeyRunsUnadoptedAgainstTheKeyboardCursor is row 8, the opposite
// of row 7 and equally deliberate: a hotkey, Enter or Esc acts on the keyboard
// cursor, never on whatever the pointer happens to be resting over.
func TestAnyOtherKeyRunsUnadoptedAgainstTheKeyboardCursor(t *testing.T) {
	state, _ := motion(t, choiceMap().Handler(), pointer.State{}, 10, 6)
	anchor, next := choice(0).Adopt(state, false)
	if anchor != 0 {
		t.Fatalf("anchor = %d, want the unadopted keyboard cursor 0", anchor)
	}
	if next.Hovered() != "" || choice(0).Mode(next) {
		t.Fatalf("mouse mode stayed on after the key: hovered %q", next.Hovered())
	}
}

// TestARenderWithAChangedRegionSetReresolvesOrClears is row 9: a resize, a
// refresh or a changed filter re-resolves hover from the retained point, and an
// unresolvable point clears it.
func TestARenderWithAChangedRegionSetReresolvesOrClears(t *testing.T) {
	state, _ := motion(t, choiceMap().Handler(), pointer.State{}, 10, 5)

	var filtered pointer.Map
	filtered.AddControl(rowOne, pointer.Rect{X0: 4, Y0: 4, X1: 36, Y1: 5},
		func(pointer.Point) tea.Msg { return activatedMsg{name: string(rowOne)} })
	if got := state.Reresolve(filtered); got.Hovered() != "" {
		t.Fatalf("a point the filtered map cannot resolve hovered %q", got.Hovered())
	}

	var widened pointer.Map
	widened.AddControl(rowOne, pointer.Rect{X0: 0, Y0: 0, X1: 40, Y1: 20},
		func(pointer.Point) tea.Msg { return activatedMsg{name: string(rowOne)} })
	if got := state.Reresolve(widened); !got.IsHovered(rowOne) {
		t.Fatalf("a resized map re-resolved to %q, want %q", got.Hovered(), rowOne)
	}

	if got := (pointer.State{}).Reresolve(widened); got.Hovered() != "" {
		t.Fatalf("a state with no hover to re-resolve gained %q", got.Hovered())
	}
}

// TestHoverIsOptInOnTheRegionThatAlreadyExists is spec section 10.5.3: a region
// opts into hover by carrying a non-empty control id, so Map.Add stays
// clickable only and a map with no ids never emits hover at all.
func TestHoverIsOptInOnTheRegionThatAlreadyExists(t *testing.T) {
	var plain pointer.Map
	plain.Add(pointer.Rect{X0: 0, Y0: 0, X1: 10, Y1: 10},
		func(pointer.Point) tea.Msg { return activatedMsg{name: "plain"} })
	if command := plain.Handler()(tea.MouseMotionMsg{X: 5, Y: 5}); command != nil {
		t.Fatal("an untracked map emitted hover feedback")
	}
	if id, ok := plain.Resolve(pointer.Point{X: 5, Y: 5}); ok {
		t.Fatalf("Map.Add region resolved as hoverable: %q", id)
	}

	mixed := choiceMap()
	mixed.Add(pointer.Rect{X0: 4, Y0: 4, X1: 36, Y1: 5},
		func(pointer.Point) tea.Msg { return activatedMsg{name: "overlay"} })
	if id, ok := mixed.Resolve(pointer.Point{X: 10, Y: 4}); !ok || id != rowOne {
		t.Fatalf("Resolve skipped the topmost hoverable region: (%q, %v)", id, ok)
	}
	if id, ok := mixed.Resolve(pointer.Point{X: 10, Y: 18}); ok {
		t.Fatalf("the backdrop resolved as hoverable: %q", id)
	}
}

// TestHoverIsNotSetForAControlIDOfAnotherSurface keeps mouse mode surface-local:
// exactly one cursor is visible, and a hovered row belonging to the panel next
// door must not take this surface's cursor away from the keyboard.
func TestHoverIsNotSetForAControlIDOfAnotherSurface(t *testing.T) {
	state := pointer.State{}.Hover("settings:2", pointer.Point{X: 1, Y: 1})
	surface := choice(1)
	if surface.Mode(state) {
		t.Fatal("mouse mode turned on for a control id of another surface")
	}
	if surface.Acting(state) != 1 {
		t.Fatalf("acting selection = %d, want the keyboard cursor 1", surface.Acting(state))
	}
	anchor, next := surface.Adopt(state, true)
	if anchor != 1 || next.Hovered() != "settings:2" {
		t.Fatalf("an unrelated surface consumed the hover: anchor=%d hovered=%q", anchor, next.Hovered())
	}
}

// TestChoiceWithoutAResolverNeverEntersMouseMode covers the zero value: a
// surface that names no rows has no rows for the pointer to act on.
func TestChoiceWithoutAResolverNeverEntersMouseMode(t *testing.T) {
	state := pointer.State{}.Hover(rowOne, pointer.Point{X: 10, Y: 4})
	var surface pointer.Choice
	if surface.Mode(state) || surface.Acting(state) != 0 {
		t.Fatal("a Choice with no resolver entered mouse mode")
	}
	_, next := surface.Adopt(state, true)
	if !next.IsHovered(rowOne) {
		t.Fatal("a Choice with no resolver cleared a hover it does not own")
	}
}

// TestRowsWithPrefixAcceptsOnlyItsOwnCanonicalIndices keeps a surface's
// resolver from claiming an id that merely starts the same way.
func TestRowsWithPrefixAcceptsOnlyItsOwnCanonicalIndices(t *testing.T) {
	rows := pointer.RowsWithPrefix("choice:")
	tests := []struct {
		id   pointer.ControlID
		want int
		ok   bool
	}{
		{id: "choice:0", want: 0, ok: true},
		{id: "choice:12", want: 12, ok: true},
		{id: "choice:", ok: false},
		{id: "choice:-1", ok: false},
		{id: "choice:+1", ok: false},
		{id: "choice:01", ok: false},
		{id: "choice:two", ok: false},
		{id: "settings:1", ok: false},
		{id: "", ok: false},
	}
	for _, test := range tests {
		got, ok := rows(test.id)
		if got != test.want || ok != test.ok {
			t.Errorf("rows(%q) = (%d, %v), want (%d, %v)", test.id, got, ok, test.want, test.ok)
		}
	}
}

// TestClearHoverLeavesThePressAlone keeps the two feedback bits independent:
// clearing mouse mode is not cancelling a gesture.
func TestClearHoverLeavesThePressAlone(t *testing.T) {
	handler := choiceMap().Handler()
	state, _ := motion(t, handler, pointer.State{}, 10, 5)
	state, _, _ = state.Update(handler(tea.MouseClickMsg{X: 10, Y: 5, Button: tea.MouseLeft})())
	cleared := state.ClearHover()
	if !cleared.IsPressed(rowTwo) {
		t.Fatal("clearing hover cleared the press")
	}
	if _, ok := cleared.HoverPoint(); ok {
		t.Fatal("a cleared hover still reports a point to re-resolve from")
	}
	if cleared.IsHovered("") {
		t.Fatal("the empty control id reported itself as hovered")
	}
}
