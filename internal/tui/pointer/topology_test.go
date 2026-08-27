package pointer

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

type topologyActionMsg string

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
