package pointer_test

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/tui/pointer"
)

type activatedMsg struct {
	name  string
	point pointer.Point
}

type scrolledMsg struct {
	name  string
	delta int
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
