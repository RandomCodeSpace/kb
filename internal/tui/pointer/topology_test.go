package pointer

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

type topologyActionMsg string

type topologyWheelMsg struct {
	name   string
	intent WheelIntent
	target int
}

func (message topologyWheelMsg) PointerWheelIntent() WheelIntent { return message.intent }

func (message topologyWheelMsg) PointerWheelTarget(target int) tea.Msg {
	message.target = target
	return message
}

func TestTopologyRebindExactUsesCurrentActionWithoutCoordinateResolution(t *testing.T) {
	const id ControlID = "stable"
	oldCalls, currentCalls := 0, 0
	var oldMap Map
	oldMap.AddControl(id, Rect{X1: 2, Y1: 1}, func(Point) tea.Msg {
		oldCalls++
		return topologyActionMsg("old")
	})
	handler := oldMap.Handler()
	press := handler(tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft})()
	release := handler(tea.MouseReleaseMsg{X: 0, Y: 0, Button: tea.MouseLeft})()

	var currentMap Map
	currentMap.AddControl(id, Rect{X0: 10, X1: 12, Y1: 1}, func(Point) tea.Msg {
		currentCalls++
		return topologyActionMsg("current")
	})
	topology := currentMap.Topology()
	if _, ok := topology.RebindExact(press); !ok {
		t.Fatal("current topology rejected the stable press")
	}
	rebound, ok := topology.RebindExact(release)
	if !ok {
		t.Fatal("current topology rejected the stable release")
	}
	state, _, _ := (State{}).Update(press)
	state, command, _ := state.Update(rebound)
	if command == nil || command() != topologyActionMsg("current") {
		t.Fatal("release did not execute the current stable action")
	}
	if oldCalls != 0 || currentCalls != 1 || state.Active() {
		t.Fatalf("old calls=%d current calls=%d active=%t", oldCalls, currentCalls, state.Active())
	}
}

func TestTopologyRebindExactRejectsRemovedStableID(t *testing.T) {
	var oldMap Map
	oldMap.AddControl("removed", Rect{X1: 1, Y1: 1}, func(Point) tea.Msg { return topologyActionMsg("old") })
	handler := oldMap.Handler()
	release := handler(tea.MouseReleaseMsg{X: 0, Y: 0, Button: tea.MouseLeft})()

	var currentMap Map
	currentMap.AddControl("replacement", Rect{X1: 1, Y1: 1}, func(Point) tea.Msg {
		return topologyActionMsg("replacement")
	})
	if _, ok := currentMap.Topology().RebindExact(release); ok {
		t.Fatal("removed ID rebound to the coordinate replacement")
	}
}

func TestInteractionAndStateResetSemantics(t *testing.T) {
	const id ControlID = "stable"
	point := Point{X: 7, Y: 4}
	newHandler := func() func(tea.MouseMsg) tea.Cmd {
		var hitMap Map
		hitMap.AddControl(id, Rect{X0: 1, Y0: 1, X1: 12, Y1: 8}, func(Point) tea.Msg {
			return topologyActionMsg("activated")
		})
		return hitMap.Handler()
	}
	handler := newHandler()
	pressCommand := handler(tea.MouseClickMsg{X: point.X, Y: point.Y, Button: tea.MouseLeft})
	if pressCommand == nil {
		t.Fatal("handler did not emit press feedback")
	}
	base, _, handled := (State{}).Update(pressCommand())
	if !handled || base.Pressed() != id {
		t.Fatalf("press state = pressed:%q handled:%t, want %q and handled", base.Pressed(), handled, id)
	}
	hoverCommand := newHandler()(tea.MouseMotionMsg{X: point.X, Y: point.Y, Button: tea.MouseNone})
	if hoverCommand == nil {
		t.Fatal("handler did not emit hover feedback")
	}
	base, _, handled = base.Update(hoverCommand())
	if !handled || base.Hovered() != id {
		t.Fatalf("hover state = hovered:%q handled:%t, want %q and handled", base.Hovered(), handled, id)
	}

	if got := base.Pressed(); got != id {
		t.Fatalf("Pressed() = %q, want %q", got, id)
	}
	resetCases := []struct {
		name        string
		reset       func(State) State
		wantPressed bool
		wantHover   ControlID
		wantPoint   bool
	}{
		{name: "capture", reset: State.ClearCapture, wantHover: id, wantPoint: true},
		{name: "hover", reset: State.ClearHover, wantPressed: true, wantPoint: true},
		{name: "observation", reset: State.ClearHoverObservation, wantPressed: true},
	}
	for _, test := range resetCases {
		t.Run(test.name, func(t *testing.T) {
			state := test.reset(base)
			if state.Active() != test.wantPressed || state.IsPressed(id) != test.wantPressed {
				t.Fatalf("pressed state = active:%t pressed:%t, want active:%t", state.Active(), state.IsPressed(id), test.wantPressed)
			}
			if state.Hovered() != test.wantHover {
				t.Fatalf("Hovered() = %q, want %q", state.Hovered(), test.wantHover)
			}
			gotPoint, got := state.HoverPoint()
			if got != test.wantPoint || (got && gotPoint != point) {
				t.Fatalf("HoverPoint() = (%+v, %t), want (%+v, %t)", gotPoint, got, point, test.wantPoint)
			}
		})
	}

	nilResultHandler := func() func(tea.MouseMsg) tea.Cmd {
		var hitMap Map
		hitMap.AddControl(id, Rect{X0: 1, Y0: 1, X1: 12, Y1: 8}, func(Point) tea.Msg { return nil })
		return hitMap.Handler()
	}
	interactionCases := []struct {
		name       string
		prepare    func() (State, tea.Msg)
		wantHandle bool
		wantActive bool
		wantHover  ControlID
		wantPoint  Point
		wantHave   bool
		wantResult tea.Msg
	}{
		{name: "unrelated", prepare: func() (State, tea.Msg) { return State{}, topologyActionMsg("domain") }},
		{name: "hover", prepare: func() (State, tea.Msg) {
			return State{}, newHandler()(tea.MouseMotionMsg{X: point.X, Y: point.Y, Button: tea.MouseNone})()
		}, wantHandle: true, wantHover: id, wantPoint: point, wantHave: true},
		{name: "reset hover", prepare: func() (State, tea.Msg) { return base, ResetHover()() }, wantHandle: true, wantActive: true, wantHave: false},
		{name: "press", prepare: func() (State, tea.Msg) {
			return State{}, newHandler()(tea.MouseClickMsg{X: point.X, Y: point.Y, Button: tea.MouseLeft})()
		}, wantHandle: true, wantActive: true},
		{name: "matched release", prepare: func() (State, tea.Msg) {
			handler := newHandler()
			state, _, _ := (State{}).Update(handler(tea.MouseClickMsg{X: point.X, Y: point.Y, Button: tea.MouseLeft})())
			release := handler(tea.MouseReleaseMsg{X: point.X, Y: point.Y, Button: tea.MouseLeft})
			return state, release()
		}, wantHandle: true, wantResult: topologyActionMsg("activated")},
		{name: "mismatched release", prepare: func() (State, tea.Msg) {
			var hitMap Map
			hitMap.AddControl("other", Rect{X0: 1, Y0: 1, X1: 5, Y1: 8}, func(Point) tea.Msg { return topologyActionMsg("other") })
			hitMap.AddControl(id, Rect{X0: 6, Y0: 1, X1: 12, Y1: 8}, func(Point) tea.Msg { return topologyActionMsg("stable") })
			handler := hitMap.Handler()
			state, _, _ := (State{}).Update(handler(tea.MouseClickMsg{X: 2, Y: point.Y, Button: tea.MouseLeft})())
			return state, handler(tea.MouseReleaseMsg{X: 7, Y: point.Y, Button: tea.MouseLeft})()
		}, wantHandle: true},
		{name: "release with nil result", prepare: func() (State, tea.Msg) {
			handler := nilResultHandler()
			state, _, _ := (State{}).Update(handler(tea.MouseClickMsg{X: point.X, Y: point.Y, Button: tea.MouseLeft})())
			return state, handler(tea.MouseReleaseMsg{X: point.X, Y: point.Y, Button: tea.MouseLeft})()
		}, wantHandle: true},
		{name: "cancel with followup", prepare: func() (State, tea.Msg) {
			return base, CancelWith(func() tea.Msg { return topologyActionMsg("cancelled") })()
		}, wantHandle: true, wantActive: false, wantHover: id, wantPoint: point, wantHave: true, wantResult: topologyActionMsg("cancelled")},
		{name: "cancel without followup", prepare: func() (State, tea.Msg) { return base, Cancel()() }, wantHandle: true, wantActive: false, wantHover: id, wantPoint: point, wantHave: true},
	}
	for _, test := range interactionCases {
		t.Run(test.name, func(t *testing.T) {
			initial, message := test.prepare()
			state, command, handled := initial.Update(message)
			if handled != test.wantHandle {
				t.Fatalf("handled = %t, want %t", handled, test.wantHandle)
			}
			if state.Active() != test.wantActive {
				t.Fatalf("active = %t, want %t", state.Active(), test.wantActive)
			}
			if state.Hovered() != test.wantHover {
				t.Fatalf("hovered = %q, want %q", state.Hovered(), test.wantHover)
			}
			gotPoint, gotHave := state.HoverPoint()
			if gotHave != test.wantHave || (gotHave && gotPoint != test.wantPoint) {
				t.Fatalf("hover point = (%+v, %t), want (%+v, %t)", gotPoint, gotHave, test.wantPoint, test.wantHave)
			}
			if test.wantResult == nil {
				if command != nil {
					t.Fatalf("unexpected command result: %#v", command())
				}
				return
			}
			if command == nil || command() != test.wantResult {
				t.Fatalf("command = %#v, want %#v", command, test.wantResult)
			}
		})
	}

	observedCases := []struct {
		name      string
		message   tea.Msg
		want      InteractionKind
		wantID    ControlID
		wantPoint bool
	}{
		{name: "press", message: newHandler()(tea.MouseClickMsg{X: point.X, Y: point.Y, Button: tea.MouseLeft})(), want: InteractionPress, wantID: id},
		{name: "release", message: func() tea.Msg {
			handler := newHandler()
			handler(tea.MouseClickMsg{X: point.X, Y: point.Y, Button: tea.MouseLeft})
			return handler(tea.MouseReleaseMsg{X: point.X, Y: point.Y, Button: tea.MouseLeft})()
		}(), want: InteractionRelease, wantID: id, wantPoint: true},
		{name: "cancel", message: Cancel()(), want: InteractionCancel},
		{name: "hover", message: newHandler()(tea.MouseMotionMsg{X: point.X, Y: point.Y, Button: tea.MouseNone})(), want: InteractionHover, wantID: id, wantPoint: true},
		{name: "reset hover", message: ResetHover()(), want: InteractionResetHover},
	}
	for _, test := range observedCases {
		t.Run("observe "+test.name, func(t *testing.T) {
			got, ok := ObserveInteraction(test.message)
			if !ok || got.Kind != test.want || got.ID != test.wantID || (test.wantPoint && got.Point != point) {
				t.Fatalf("ObserveInteraction() = (%+v, %t), want kind %d and identity", got, ok, test.want)
			}
		})
	}
	if _, ok := ObserveInteraction(topologyActionMsg("domain")); ok {
		t.Fatal("domain message was observed as pointer interaction")
	}

	followup := topologyActionMsg("followup")
	handler = newHandler()
	handler(tea.MouseClickMsg{X: point.X, Y: point.Y, Button: tea.MouseLeft})
	original := handler(tea.MouseReleaseMsg{X: point.X, Y: point.Y, Button: tea.MouseLeft})()
	replacedFollowup := ReplaceFollowup(original, followup)
	if observed, ok := ObserveInteraction(replacedFollowup); !ok || observed.Followup != followup {
		t.Fatalf("ReplaceFollowup() = (%+v, %t), want followup %q", observed, ok, followup)
	}
	if got := ReplaceFollowup(topologyActionMsg("domain"), followup); got != followup {
		t.Fatalf("non-interaction followup = %#v, want %#v", got, followup)
	}
	activation := func(Point) tea.Msg { return topologyActionMsg("replacement") }
	state, _, _ := (State{}).Update(newHandler()(tea.MouseClickMsg{X: point.X, Y: point.Y, Button: tea.MouseLeft})())
	replacedActivation := ReplaceActivation(original, activation)
	state, command, handled := state.Update(replacedActivation)
	if !handled || state.Active() || command == nil || command() != topologyActionMsg("replacement") {
		t.Fatalf("ReplaceActivation result = active:%t command:%#v handled:%t", state.Active(), command, handled)
	}
	if got := ReplaceActivation(topologyActionMsg("domain"), activation); got != topologyActionMsg("domain") {
		t.Fatalf("non-interaction activation = %#v", got)
	}

	cancel := CancelWith(func() tea.Msg { return followup })()
	observed, ok := ObserveInteraction(cancel)
	if !ok || observed.Kind != InteractionCancel || observed.Followup != followup {
		t.Fatalf("CancelWith() = (%+v, %t), want cancel with followup", observed, ok)
	}
	if observed, ok := ObserveInteraction(CancelWith(nil)()); !ok || observed.Kind != InteractionCancel || observed.Followup != nil {
		t.Fatalf("CancelWith(nil) = (%+v, %t), want empty cancel", observed, ok)
	}
	if observed, ok := ObserveInteraction(ResetHover()()); !ok || observed.Kind != InteractionResetHover {
		t.Fatalf("ResetHover() = (%+v, %t), want reset-hover interaction", observed, ok)
	}
}

func TestTopologyWheelAndParitySemantics(t *testing.T) {
	const (
		id       ControlID = "stable"
		wheelKey           = "list"
	)
	intent := WheelIntent{Key: wheelKey, Current: 2, Target: 2, Min: 0, Max: 4}
	var hitMap Map
	hitMap.Add(Rect{X0: 1, Y0: 1, X1: 4, Y1: 3}, func(Point) tea.Msg { return topologyActionMsg("untracked") })
	hitMap.AddControl(id, Rect{X0: 4, Y0: 1, X1: 8, Y1: 3}, func(Point) tea.Msg { return topologyActionMsg("first") })
	hitMap.AddControl(id, Rect{X0: 8, Y0: 1, X1: 12, Y1: 3}, func(Point) tea.Msg { return topologyActionMsg("last") })
	hitMap.AddWheel(Rect{X0: 0, Y0: 0, X1: 20, Y1: 10}, func(int) tea.Msg {
		return topologyWheelMsg{name: "primary", intent: intent}
	})
	hitMap.AddWheel(Rect{X0: 0, Y0: 0, X1: 20, Y1: 10}, func(int) tea.Msg { return topologyActionMsg("not wheel") })
	hitMap.AddWheel(Rect{X0: 0, Y0: 0, X1: 20, Y1: 10}, func(int) tea.Msg {
		return topologyWheelMsg{name: "ignored", intent: WheelIntent{}}
	})
	topology := hitMap.Topology()
	if !topology.HasControl(id) || topology.HasControl("") || topology.HasControl("missing") {
		t.Fatal("HasControl did not distinguish present, empty, and missing IDs")
	}
	if _, ok := topology.RebindExact(topologyActionMsg("domain")); ok {
		t.Fatal("domain message rebound as exact interaction")
	}
	if hover := hitMap.Handler()(tea.MouseMotionMsg{X: 9, Y: 2, Button: tea.MouseNone}); hover == nil {
		t.Fatal("handler did not emit hover interaction")
	} else if _, ok := topology.RebindExact(hover()); ok {
		t.Fatal("hover interaction rebound as exact press/release")
	}

	oldHandler := hitMap.Handler()
	press := oldHandler(tea.MouseClickMsg{X: 9, Y: 2, Button: tea.MouseLeft})()
	press, ok := topology.RebindExact(press)
	if !ok {
		t.Fatal("stable press was not rebound")
	}
	if observed, ok := ObserveInteraction(press); !ok || observed.ID != id {
		t.Fatalf("rebound press = (%+v, %t)", observed, ok)
	}
	release := oldHandler(tea.MouseReleaseMsg{X: 9, Y: 2, Button: tea.MouseLeft})()
	release, ok = topology.RebindExact(release)
	if !ok {
		t.Fatal("stable release was not rebound")
	}
	if observed, ok := ObserveInteraction(release); !ok || observed.ID != id || observed.Point != (Point{X: 9, Y: 2}) {
		t.Fatalf("rebound release = (%+v, %t)", observed, ok)
	}
	state, _, handled := (State{}).Update(press)
	state, command, handled := state.Update(release)
	if !handled || state.Active() || command == nil || command() != topologyActionMsg("last") {
		t.Fatalf("rebound release = state active:%t command:%#v handled:%t", state.Active(), command, handled)
	}

	if message, gotIntent, ok := topology.RebindWheel("missing", 2); ok || message != nil || gotIntent != (WheelIntent{}) {
		t.Fatalf("missing wheel = (%#v, %+v, %t)", message, gotIntent, ok)
	}
	message, gotIntent, ok := topology.RebindWheel(wheelKey, 99)
	if !ok || gotIntent != intent {
		t.Fatalf("clamped wheel = (%#v, %+v, %t), want intent %+v", message, gotIntent, ok, intent)
	}
	if got := message.(topologyWheelMsg).target; got != intent.Max {
		t.Fatalf("wheel target = %d, want max %d", got, intent.Max)
	}
	if message, _, ok := topology.RebindWheel("", 2); ok || message != nil {
		t.Fatalf("empty wheel key = (%#v, %t), want no binding", message, ok)
	}

	otherIntent := WheelIntent{Key: wheelKey, Current: 5, Target: 5, Min: 3, Max: 7}
	var otherMap Map
	otherMap.AddControl("other", Rect{X0: 1, Y0: 1, X1: 2, Y1: 2}, func(Point) tea.Msg { return topologyActionMsg("other") })
	other := otherMap.Topology().WithWheel(otherIntent, func(target int) tea.Msg {
		return topologyWheelMsg{name: "other", intent: otherIntent, target: target}
	})
	if got := other.WithWheel(WheelIntent{}, func(int) tea.Msg { return topologyActionMsg("invalid") }); !other.SameControls(got) {
		t.Fatal("invalid wheel intent changed topology")
	}
	empty := Topology{}
	if got := empty.WithWheel(otherIntent, nil); !empty.SameControls(got) {
		t.Fatal("nil wheel rebuild changed topology")
	}
	merged := topology.Merge(other)
	if !merged.HasControl("other") {
		t.Fatal("merged topology lost the other control")
	}
	message, gotIntent, ok = merged.RebindWheel(wheelKey, -99)
	if !ok || gotIntent != otherIntent || message.(topologyWheelMsg).target != otherIntent.Min {
		t.Fatalf("merged wheel precedence = (%#v, %+v, %t), want other binding clamped to %d", message, gotIntent, ok, otherIntent.Min)
	}

	parityCases := []struct {
		name  string
		left  Topology
		right Topology
		want  bool
	}{
		{name: "same semantics different closures", left: topology, right: mapTopologyWithAction(id, "replacement", intent), want: true},
		{name: "different controls", left: topology, right: other, want: false},
		{name: "different wheels", left: topology, right: mapTopologyWithAction(id, "replacement", otherIntent), want: false},
		{name: "different lengths", left: topology, right: Topology{}, want: false},
	}
	for _, test := range parityCases {
		t.Run("parity "+test.name, func(t *testing.T) {
			if got := test.left.SameControls(test.right); got != test.want {
				t.Fatalf("SameControls() = %t, want %t", got, test.want)
			}
		})
	}
}

func mapTopologyWithAction(id ControlID, result topologyActionMsg, intent WheelIntent) Topology {
	var hitMap Map
	hitMap.AddControl(id, Rect{X0: 20, Y0: 1, X1: 30, Y1: 3}, func(Point) tea.Msg { return result })
	hitMap.AddControl(id, Rect{X0: 30, Y0: 1, X1: 40, Y1: 3}, func(Point) tea.Msg { return result })
	hitMap.AddWheel(Rect{X0: 0, Y0: 0, X1: 20, Y1: 10}, func(int) tea.Msg {
		return topologyWheelMsg{name: "replacement", intent: intent}
	})
	return hitMap.Topology()
}
