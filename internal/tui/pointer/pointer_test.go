package pointer_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/tui/pointer"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

type activatedMsg struct {
	name  string
	point pointer.Point
}

type scrolledMsg struct {
	name  string
	delta int
}

type unknownMouseMsg tea.Mouse

func (message unknownMouseMsg) String() string { return "unknown mouse message" }
func (message unknownMouseMsg) Mouse() tea.Mouse {
	return tea.Mouse(message)
}

func TestViewportRowProjectsAndClipsVisibleTerminalCells(t *testing.T) {
	viewport := pointer.Viewport{
		Rect:   pointer.Rect{X0: 10, Y0: 4, X1: 30, Y1: 7},
		Scroll: 2,
	}

	tests := []struct {
		name       string
		logicalRow int
		x0, x1     int
		want       pointer.Rect
		visible    bool
	}{
		{name: "above", logicalRow: 1, x0: 0, x1: 20},
		{name: "first visible clipped horizontally", logicalRow: 2, x0: -3, x1: 8, want: pointer.Rect{X0: 10, Y0: 4, X1: 18, Y1: 5}, visible: true},
		{name: "last visible", logicalRow: 4, x0: 7, x1: 25, want: pointer.Rect{X0: 17, Y0: 6, X1: 30, Y1: 7}, visible: true},
		{name: "below", logicalRow: 5, x0: 0, x1: 20},
		{name: "empty span", logicalRow: 3, x0: 9, x1: 9},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, visible := viewport.Row(test.logicalRow, test.x0, test.x1)
			if got != test.want || visible != test.visible {
				t.Fatalf("Row(%d, %d, %d) = (%+v, %v), want (%+v, %v)",
					test.logicalRow, test.x0, test.x1, got, visible, test.want, test.visible)
			}
		})
	}
}

func TestMapActivatesTopmostRegionOnRelease(t *testing.T) {
	var hitMap pointer.Map
	hitMap.Add(pointer.Rect{X0: 1, Y0: 1, X1: 8, Y1: 4}, func(point pointer.Point) tea.Msg {
		return activatedMsg{name: "bottom", point: point}
	})
	hitMap.Add(pointer.Rect{X0: 3, Y0: 2, X1: 6, Y1: 3}, func(point pointer.Point) tea.Msg {
		return activatedMsg{name: "top", point: point}
	})
	handler := hitMap.Handler()

	if command := handler(tea.MouseClickMsg{X: 4, Y: 2, Button: tea.MouseLeft}); command != nil {
		t.Fatalf("press activated a control: %#v", command())
	}
	command := handler(tea.MouseReleaseMsg{X: 4, Y: 2, Button: tea.MouseLeft})
	if command == nil {
		t.Fatal("release inside a control was ignored")
	}
	if got, ok := command().(activatedMsg); !ok || got != (activatedMsg{name: "top", point: pointer.Point{X: 4, Y: 2}}) {
		t.Fatalf("release message = %#v", command())
	}
	if command := handler(tea.MouseReleaseMsg{X: 9, Y: 2, Button: tea.MouseLeft}); command != nil {
		t.Fatalf("release outside controls activated: %#v", command())
	}
	if command := handler(tea.MouseReleaseMsg{X: 2, Y: 2, Button: tea.MouseRight}); command != nil {
		t.Fatalf("right release activated: %#v", command())
	}
}

func TestTrackedControlReportsPressedStateThenActivatesOnRelease(t *testing.T) {
	const save pointer.ControlID = "editor.save"
	var hitMap pointer.Map
	hitMap.AddControl(save, pointer.Rect{X0: 2, Y0: 1, X1: 8, Y1: 3}, func(point pointer.Point) tea.Msg {
		return activatedMsg{name: "save", point: point}
	})
	handler := hitMap.Handler()
	state := pointer.State{}

	press := handler(tea.MouseClickMsg{X: 4, Y: 2, Button: tea.MouseLeft})
	if press == nil {
		t.Fatal("tracked press did not request visual feedback")
	}
	var handled bool
	var action tea.Cmd
	state, action, handled = state.Update(press())
	if !handled || action != nil || !state.IsPressed(save) {
		t.Fatalf("press state = (%v, %#v, %v), want tracked pressed state", state.IsPressed(save), action, handled)
	}
	if state.IsPressed(pointer.ControlID("editor.cancel")) {
		t.Fatal("unrelated control reported pressed")
	}

	// A press feedback message causes Bubble Tea to render a new immutable
	// pointer snapshot before the release arrives.
	handler = hitMap.Handler()
	release := handler(tea.MouseReleaseMsg{X: 4, Y: 2, Button: tea.MouseLeft})
	if release == nil {
		t.Fatal("tracked release did not clear visual feedback")
	}
	state, action, handled = state.Update(release())
	if !handled || state.IsPressed(save) || action == nil {
		t.Fatalf("release state = (%v, %#v, %v), want cleared state and activation", state.IsPressed(save), action, handled)
	}
	if got := action(); got != (activatedMsg{name: "save", point: pointer.Point{X: 4, Y: 2}}) {
		t.Fatalf("activation = %#v", got)
	}

	unchanged, command, handled := state.Update(activatedMsg{name: "unrelated"})
	if handled || command != nil || unchanged != state {
		t.Fatalf("unrelated message was consumed: (%+v, %#v, %v)", unchanged, command, handled)
	}
}

func TestTrackedControlClearsPressedStateWithoutActivationWhenGestureIsCancelled(t *testing.T) {
	const control pointer.ControlID = "dialog.confirm"
	var hitMap pointer.Map
	hitMap.AddControl(control, pointer.Rect{X0: 2, Y0: 2, X1: 7, Y1: 4}, func(point pointer.Point) tea.Msg {
		return activatedMsg{name: "confirm", point: point}
	})
	handler := hitMap.Handler()

	pressState := func() pointer.State {
		command := handler(tea.MouseClickMsg{X: 3, Y: 2, Button: tea.MouseLeft})
		if command == nil {
			t.Fatal("tracked press was ignored")
		}
		state, action, handled := (pointer.State{}).Update(command())
		if !handled || action != nil || !state.IsPressed(control) {
			t.Fatalf("press did not produce pressed state: (%+v, %#v, %v)", state, action, handled)
		}
		return state
	}
	assertCleared := func(state pointer.State, command tea.Cmd) pointer.State {
		t.Helper()
		if command == nil {
			t.Fatal("cancelled gesture did not clear visual feedback")
		}
		state, action, handled := state.Update(command())
		if !handled || action != nil || state.IsPressed(control) {
			t.Fatalf("cancel state = (%v, %#v, %v), want cleared without activation", state.IsPressed(control), action, handled)
		}
		return state
	}

	state := pressState()
	handler = hitMap.Handler()
	state = assertCleared(state, handler(tea.MouseMotionMsg{X: 4, Y: 2, Button: tea.MouseLeft}))
	if command := handler(tea.MouseReleaseMsg{X: 4, Y: 2, Button: tea.MouseLeft}); command != nil {
		state, action, handled := state.Update(command())
		if !handled || action != nil || state.IsPressed(control) {
			t.Fatalf("drag release state = (%v, %#v, %v)", state.IsPressed(control), action, handled)
		}
	}

	state = pressState()
	handler = hitMap.Handler()
	assertCleared(state, handler(tea.MouseReleaseMsg{X: 10, Y: 2, Button: tea.MouseLeft}))

	state = pressState()
	handler = hitMap.Handler()
	assertCleared(state, handler(tea.MouseReleaseMsg{X: 3, Y: 2, Button: tea.MouseRight}))
}

func TestTrackedDragReleaseCannotActivateBeforeCancellationFeedbackIsConsumed(t *testing.T) {
	const control pointer.ControlID = "dialog.confirm"
	var hitMap pointer.Map
	hitMap.AddControl(control, pointer.Rect{X0: 2, Y0: 2, X1: 7, Y1: 4}, func(point pointer.Point) tea.Msg {
		return activatedMsg{name: "confirm", point: point}
	})
	handler := hitMap.Handler()

	press := handler(tea.MouseClickMsg{X: 3, Y: 2, Button: tea.MouseLeft})
	state, _, handled := (pointer.State{}).Update(press())
	if !handled || !state.IsPressed(control) {
		t.Fatal("tracked control was not pressed")
	}
	motion := handler(tea.MouseMotionMsg{X: 4, Y: 2, Button: tea.MouseLeft})
	if motion == nil || !pointer.IsMessage(motion()) {
		t.Fatal("drag did not emit cancellation feedback")
	}

	// Bubble Tea commands are asynchronous. Even if the release feedback is
	// consumed before the drag cancellation, the gesture must not activate.
	release := handler(tea.MouseReleaseMsg{X: 4, Y: 2, Button: tea.MouseLeft})
	if release == nil {
		t.Fatal("drag release did not clear pressed feedback")
	}
	state, action, handled := state.Update(release())
	if !handled || action != nil || state.IsPressed(control) {
		t.Fatalf("drag release state = (%v, %#v, %v), want cleared without activation", state.IsPressed(control), action, handled)
	}
}

func TestIsMessageRecognizesOnlyPointerInteractionMessages(t *testing.T) {
	var hitMap pointer.Map
	hitMap.AddControl("open", pointer.Rect{X0: 1, Y0: 1, X1: 5, Y1: 3}, func(point pointer.Point) tea.Msg {
		return activatedMsg{name: "open", point: point}
	})
	handler := hitMap.Handler()

	press := handler(tea.MouseClickMsg{X: 2, Y: 2, Button: tea.MouseLeft})
	if press == nil || !pointer.IsMessage(press()) {
		t.Fatal("press was not recognized as a pointer interaction message")
	}
	release := handler(tea.MouseReleaseMsg{X: 2, Y: 2, Button: tea.MouseLeft})
	if release == nil || !pointer.IsMessage(release()) {
		t.Fatal("release was not recognized as a pointer interaction message")
	}
	if pointer.IsMessage(activatedMsg{name: "open"}) {
		t.Fatal("domain message was misidentified as a pointer interaction")
	}
}

func TestTrackedControlSupportsX10ReleaseAndWheelCancellation(t *testing.T) {
	const control pointer.ControlID = "list.open"
	var hitMap pointer.Map
	hitMap.AddControl(control, pointer.Rect{X0: 1, Y0: 1, X1: 6, Y1: 3}, func(point pointer.Point) tea.Msg {
		return activatedMsg{name: "open", point: point}
	})
	hitMap.AddWheel(pointer.Rect{X0: 0, Y0: 0, X1: 10, Y1: 6}, func(delta int) tea.Msg {
		return scrolledMsg{name: "list", delta: delta}
	})
	handler := hitMap.Handler()

	press := handler(tea.MouseClickMsg{X: 3, Y: 2, Button: tea.MouseLeft})
	state, _, _ := (pointer.State{}).Update(press())
	handler = hitMap.Handler()
	release := handler(tea.MouseReleaseMsg{X: 3, Y: 2, Button: tea.MouseNone})
	state, action, handled := state.Update(release())
	if !handled || state.IsPressed(control) || action == nil {
		t.Fatalf("X10 release state = (%v, %#v, %v)", state.IsPressed(control), action, handled)
	}
	if got := action(); got != (activatedMsg{name: "open", point: pointer.Point{X: 3, Y: 2}}) {
		t.Fatalf("X10 activation = %#v", got)
	}

	press = handler(tea.MouseClickMsg{X: 3, Y: 2, Button: tea.MouseLeft})
	state, _, _ = state.Update(press())
	handler = hitMap.Handler()
	wheel := handler(tea.MouseWheelMsg{X: 3, Y: 2, Button: tea.MouseWheelDown})
	state, action, handled = state.Update(wheel())
	if !handled || state.IsPressed(control) || action == nil {
		t.Fatalf("wheel cancellation state = (%v, %#v, %v)", state.IsPressed(control), action, handled)
	}
	if got := action(); got != (scrolledMsg{name: "list", delta: 1}) {
		t.Fatalf("wheel action = %#v", got)
	}
}

func TestTrackedControlClearsFeedbackWhenAnotherPointerGestureTakesOwnership(t *testing.T) {
	const control pointer.ControlID = "menu.open"
	var hitMap pointer.Map
	hitMap.AddControl(control, pointer.Rect{X0: 1, Y0: 1, X1: 6, Y1: 3}, func(point pointer.Point) tea.Msg {
		return activatedMsg{name: "open", point: point}
	})
	handler := hitMap.Handler()

	press := func() pointer.State {
		command := handler(tea.MouseClickMsg{X: 3, Y: 2, Button: tea.MouseLeft})
		state, _, handled := (pointer.State{}).Update(command())
		if !handled || !state.IsPressed(control) {
			t.Fatal("tracked control was not pressed")
		}
		return state
	}
	assertCleared := func(state pointer.State, command tea.Cmd) {
		t.Helper()
		if command == nil {
			t.Fatal("replacement gesture did not clear feedback")
		}
		state, action, handled := state.Update(command())
		if !handled || action != nil || state.IsPressed(control) {
			t.Fatalf("replacement state = (%v, %#v, %v)", state.IsPressed(control), action, handled)
		}
	}

	state := press()
	assertCleared(state, handler(tea.MouseWheelMsg{X: 3, Y: 2, Button: tea.MouseWheelLeft}))

	state = press()
	assertCleared(state, handler(tea.MouseWheelMsg{X: 20, Y: 20, Button: tea.MouseWheelDown}))

	state = press()
	assertCleared(state, handler(tea.MouseClickMsg{X: 20, Y: 20, Button: tea.MouseLeft}))
}

func TestMapRequiresCleanPressAndReleaseOnTheSameRegion(t *testing.T) {
	var hitMap pointer.Map
	hitMap.Add(pointer.Rect{X0: 2, Y0: 2, X1: 6, Y1: 4}, func(point pointer.Point) tea.Msg {
		return activatedMsg{name: "button", point: point}
	})
	handler := hitMap.Handler()

	if command := handler(tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft}); command != nil {
		t.Fatal("background press activated")
	}
	if command := handler(tea.MouseReleaseMsg{X: 3, Y: 2, Button: tea.MouseLeft}); command != nil {
		t.Fatalf("release over button after background press activated: %#v", command())
	}

	if command := handler(tea.MouseClickMsg{X: 3, Y: 2, Button: tea.MouseLeft}); command != nil {
		t.Fatal("button press activated before release")
	}
	if command := handler(tea.MouseMotionMsg{X: 4, Y: 2, Button: tea.MouseLeft}); command != nil {
		t.Fatal("drag motion activated")
	}
	if command := handler(tea.MouseReleaseMsg{X: 4, Y: 2, Button: tea.MouseLeft}); command != nil {
		t.Fatalf("drag release activated: %#v", command())
	}
	if command := handler(tea.MouseMotionMsg{X: 4, Y: 2, Button: tea.MouseNone}); command != nil {
		t.Fatalf("hover motion activated: %#v", command())
	}

	handler(tea.MouseClickMsg{X: 3, Y: 2, Button: tea.MouseLeft})
	command := handler(tea.MouseReleaseMsg{X: 3, Y: 2, Button: tea.MouseNone})
	if command == nil {
		t.Fatal("X10 release after a button press was ignored")
	}
	if got := command().(activatedMsg); got.name != "button" {
		t.Fatalf("X10 release message = %#v", got)
	}
}

func TestMapRoutesWheelToTheTopmostZoneWithUnitDelta(t *testing.T) {
	var hitMap pointer.Map
	hitMap.AddWheel(pointer.Rect{X0: 0, Y0: 0, X1: 20, Y1: 10}, func(delta int) tea.Msg {
		return scrolledMsg{name: "pane", delta: delta}
	})
	hitMap.AddWheel(pointer.Rect{X0: 5, Y0: 2, X1: 10, Y1: 6}, func(delta int) tea.Msg {
		return scrolledMsg{name: "list", delta: delta}
	})
	handler := hitMap.Handler()

	tests := []struct {
		name   string
		event  tea.MouseWheelMsg
		want   scrolledMsg
		handle bool
	}{
		{name: "up in overlap", event: tea.MouseWheelMsg{X: 6, Y: 3, Button: tea.MouseWheelUp}, want: scrolledMsg{name: "list", delta: -1}, handle: true},
		{name: "down in outer", event: tea.MouseWheelMsg{X: 2, Y: 3, Button: tea.MouseWheelDown}, want: scrolledMsg{name: "pane", delta: 1}, handle: true},
		{name: "outside", event: tea.MouseWheelMsg{X: 30, Y: 3, Button: tea.MouseWheelDown}},
		{name: "horizontal", event: tea.MouseWheelMsg{X: 6, Y: 3, Button: tea.MouseWheelLeft}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := handler(test.event)
			if !test.handle {
				if command != nil {
					t.Fatalf("wheel was handled: %#v", command())
				}
				return
			}
			if command == nil {
				t.Fatal("wheel was ignored")
			}
			if got, ok := command().(scrolledMsg); !ok || got != test.want {
				t.Fatalf("wheel message = %#v, want %#v", command(), test.want)
			}
		})
	}
}

func TestMapBackdropExcludesTheVisiblePane(t *testing.T) {
	var hitMap pointer.Map
	hitMap.AddBackdrop(
		pointer.Rect{X0: 0, Y0: 0, X1: 12, Y1: 8},
		pointer.Rect{X0: 3, Y0: 2, X1: 9, Y1: 6},
		func(point pointer.Point) tea.Msg { return activatedMsg{name: "dismiss", point: point} },
	)
	handler := hitMap.Handler()

	activate := func(x, y int) tea.Cmd {
		handler(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
		return handler(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	}
	for _, point := range []pointer.Point{{X: 1, Y: 1}, {X: 5, Y: 1}, {X: 1, Y: 4}, {X: 10, Y: 4}, {X: 5, Y: 7}} {
		command := activate(point.X, point.Y)
		if command == nil {
			t.Fatalf("backdrop point %+v was ignored", point)
		}
		if got := command().(activatedMsg); got.point != point {
			t.Fatalf("backdrop point = %+v, want %+v", got.point, point)
		}
	}
	if command := activate(5, 4); command != nil {
		t.Fatalf("pane interior dismissed: %#v", command())
	}
	if command := activate(20, 4); command != nil {
		t.Fatalf("outside bounds dismissed: %#v", command())
	}
}

func TestMapIgnoresInvalidAndNilRegistrations(t *testing.T) {
	var hitMap pointer.Map
	valid := pointer.Rect{X0: 1, Y0: 1, X1: 4, Y1: 3}
	hitMap.Add(pointer.Rect{}, func(point pointer.Point) tea.Msg {
		return activatedMsg{name: "empty action", point: point}
	})
	hitMap.Add(valid, nil)
	hitMap.AddControl("empty", pointer.Rect{}, func(point pointer.Point) tea.Msg {
		return activatedMsg{name: "empty tracked action", point: point}
	})
	hitMap.AddControl("nil", valid, nil)
	hitMap.AddWheel(pointer.Rect{}, func(delta int) tea.Msg {
		return scrolledMsg{name: "empty wheel", delta: delta}
	})
	hitMap.AddWheel(valid, nil)
	hitMap.AddBackdrop(pointer.Rect{}, valid, func(point pointer.Point) tea.Msg {
		return activatedMsg{name: "empty backdrop", point: point}
	})
	hitMap.AddBackdrop(valid, pointer.Rect{}, nil)
	handler := hitMap.Handler()

	handler(tea.MouseClickMsg{X: 2, Y: 2, Button: tea.MouseLeft})
	if command := handler(tea.MouseReleaseMsg{X: 2, Y: 2, Button: tea.MouseLeft}); command != nil {
		t.Fatalf("invalid action registration activated: %#v", command())
	}
	if command := handler(tea.MouseWheelMsg{X: 2, Y: 2, Button: tea.MouseWheelDown}); command != nil {
		t.Fatalf("invalid wheel registration activated: %#v", command())
	}
}

func TestBackdropOutsideBoundsLeavesTheEntireBoundedAreaDismissible(t *testing.T) {
	var hitMap pointer.Map
	hitMap.AddBackdrop(
		pointer.Rect{X0: 2, Y0: 2, X1: 8, Y1: 6},
		pointer.Rect{X0: 20, Y0: 20, X1: 30, Y1: 30},
		func(point pointer.Point) tea.Msg { return activatedMsg{name: "dismiss", point: point} },
	)
	handler := hitMap.Handler()
	handler(tea.MouseClickMsg{X: 5, Y: 4, Button: tea.MouseLeft})
	command := handler(tea.MouseReleaseMsg{X: 5, Y: 4, Button: tea.MouseLeft})
	if command == nil {
		t.Fatal("backdrop was lost when the pane was outside its bounds")
	}
	if got := command().(activatedMsg); got != (activatedMsg{name: "dismiss", point: pointer.Point{X: 5, Y: 4}}) {
		t.Fatalf("backdrop message = %#v", got)
	}
}

func TestMapIgnoresNilActionResultsAndUnknownMouseMessages(t *testing.T) {
	var hitMap pointer.Map
	rect := pointer.Rect{X0: 1, Y0: 1, X1: 4, Y1: 3}
	hitMap.Add(rect, func(pointer.Point) tea.Msg { return nil })
	hitMap.AddWheel(rect, func(int) tea.Msg { return nil })
	handler := hitMap.Handler()

	if command := handler(unknownMouseMsg{X: 2, Y: 2, Button: tea.MouseLeft}); command != nil {
		t.Fatalf("unknown mouse message activated: %#v", command())
	}
	if command := handler(tea.MouseWheelMsg{X: 2, Y: 2, Button: tea.MouseWheelUp}); command != nil {
		t.Fatalf("nil wheel result became a command: %#v", command())
	}
	handler(tea.MouseClickMsg{X: 2, Y: 2, Button: tea.MouseLeft})
	if command := handler(tea.MouseReleaseMsg{X: 2, Y: 2, Button: tea.MouseLeft}); command != nil {
		t.Fatalf("nil action result became a command: %#v", command())
	}

	var tracked pointer.Map
	tracked.AddControl("nil-result", rect, func(pointer.Point) tea.Msg { return nil })
	trackedHandler := tracked.Handler()
	press := trackedHandler(tea.MouseClickMsg{X: 2, Y: 2, Button: tea.MouseLeft})
	state, _, _ := (pointer.State{}).Update(press())
	trackedHandler = tracked.Handler()
	release := trackedHandler(tea.MouseReleaseMsg{X: 2, Y: 2, Button: tea.MouseLeft})
	state, action, handled := state.Update(release())
	if !handled || action != nil || state.IsPressed("nil-result") {
		t.Fatalf("tracked nil result = (%v, %#v, %v)", state.IsPressed("nil-result"), action, handled)
	}
}

func TestStateRenderMarksOnlyThePressedControl(t *testing.T) {
	state := pointer.State{}
	var hitMap pointer.Map
	hitMap.AddControl("save", pointer.Rect{X0: 0, Y0: 0, X1: 8, Y1: 1}, func(pointer.Point) tea.Msg { return nil })
	press := hitMap.Handler()(tea.MouseClickMsg{X: 1, Y: 0, Button: tea.MouseLeft})
	state, _, _ = state.Update(press())
	styles := theme.New(true)
	if got := state.Render(styles, "other", "[Save]"); got != "[Save]" {
		t.Fatalf("inactive render = %q", got)
	}
	if got := state.Render(styles, "save", "[Save]"); got != "\x1b[7m[Save]\x1b[27m" {
		t.Fatalf("pressed render = %q", got)
	}
	// The token is the theme's; a caller without one renders the plain content
	// rather than a half-applied attribute.
	if got := state.Render(nil, "save", "[Save]"); got != "[Save]" {
		t.Fatalf("themeless render = %q", got)
	}
	// A themed control's own reset would cancel the attribute for the rest of
	// the run, so it is re-armed after every inner reset.
	inner := styles.Overlay.Surf.Render("x") + "\x1b[0m"
	if got := state.Render(styles, "save", inner); strings.Count(got, "\x1b[7m") != 3 {
		t.Fatalf("re-armed render = %q", got)
	}
}

func TestCancelClearsAControlRemovedByRerender(t *testing.T) {
	var hitMap pointer.Map
	hitMap.AddControl("gone", pointer.Rect{X1: 4, Y1: 1}, func(pointer.Point) tea.Msg { return nil })
	press := hitMap.Handler()(tea.MouseClickMsg{X: 1, Y: 0, Button: tea.MouseLeft})
	state, _, handled := (pointer.State{}).Update(press())
	if !handled || !state.Active() {
		t.Fatalf("press state active=%v handled=%v", state.Active(), handled)
	}
	state, command, handled := state.Update(pointer.Cancel()())
	if !handled || command != nil || state.Active() {
		t.Fatalf("cancel state active=%v command=%v handled=%v", state.Active(), command, handled)
	}
}
