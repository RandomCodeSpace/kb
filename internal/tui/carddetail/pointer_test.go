package carddetail

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/store"
)

const (
	pointerWidth  = 80
	pointerHeight = 24
)

// clickControl drives the immutable pointer snapshot produced by the same
// render that showed the control. It deliberately knows no internal hit map.
func clickControl(t *testing.T, m *Model, label string) tea.Cmd {
	t.Helper()
	surface := m.PointerSurface("board", pointerWidth, pointerHeight)
	if surface.Pointer == nil {
		t.Fatalf("%q rendered without a pointer handler:\n%s", label, ansi.Strip(surface.Content))
	}
	x, y := renderedControlPoint(t, surface.Content, label)
	if command := surface.Pointer(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}); command != nil {
		t.Fatalf("%q activated on pointer press", label)
	}
	command := surface.Pointer(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	if command == nil {
		t.Fatalf("%q ignored pointer release at %d,%d:\n%s", label, x, y, ansi.Strip(surface.Content))
	}
	return m.Update(command())
}

func renderedControlPoint(t *testing.T, content, label string) (int, int) {
	t.Helper()
	needle := "[" + label + "]"
	for y, line := range strings.Split(ansi.Strip(content), "\n") {
		if index := strings.Index(line, needle); index >= 0 {
			return ansi.StringWidth(line[:index]) + 1, y
		}
	}
	t.Fatalf("rendered control %q missing:\n%s", needle, ansi.Strip(content))
	return 0, 0
}

func TestPointerSurfaceAddsCommentLinksAndDeletesThroughExistingMutations(t *testing.T) {
	st := &actionStore{comments: []store.Comment{{ID: 4, Author: "alice", Body: "existing"}}}
	m := openActionModel(t, st)

	if command := clickControl(t, m, "Comment"); command != nil {
		t.Fatalf("opening comment returned command %v", command)
	}
	for _, char := range "pointer save" {
		m.Update(key(char))
	}
	save := clickControl(t, m, "Save comment")
	if save == nil {
		t.Fatal("visible comment save did not start the existing mutation")
	}
	reload := m.Update(save())
	if st.addedBody != "pointer save" || reload == nil {
		t.Fatalf("pointer comment save = body:%q reload:%v", st.addedBody, reload)
	}
	m.Update(reload())
	m.Update(key('z'))

	if command := clickControl(t, m, "Link"); command != nil {
		t.Fatalf("opening link returned command %v", command)
	}
	for _, char := range "2" {
		m.Update(key(char))
	}
	link := clickControl(t, m, "Add link")
	if link == nil {
		t.Fatal("visible link save did not start the existing mutation")
	}
	reload = m.Update(link())
	if st.blockerRef != "task-7" || st.blockedRef != "2" || reload == nil {
		t.Fatalf("pointer link = %q -> %q reload:%v", st.blockerRef, st.blockedRef, reload)
	}
	m.Update(reload())
	m.Update(key('z'))

	if command := clickControl(t, m, "Del"); command != nil {
		t.Fatalf("opening comment delete returned command %v", command)
	}
	if command := clickControl(t, m, "Delete"); command != nil {
		t.Fatalf("arming comment delete returned command %v", command)
	}
	remove := clickControl(t, m, "Confirm delete")
	if remove == nil {
		t.Fatal("explicit confirmation did not start comment deletion")
	}
	reload = m.Update(remove())
	if st.deletedID != 4 || reload == nil {
		t.Fatalf("pointer comment delete = id:%d reload:%v", st.deletedID, reload)
	}
}

func TestPointerSurfaceCancelAndDestructiveBackdropHaveSafeSemantics(t *testing.T) {
	st := &actionStore{comments: []store.Comment{{ID: 4, Author: "alice", Body: "existing"}}}
	m := openActionModel(t, st)
	clickControl(t, m, "Comment")
	if command := clickControl(t, m, "Cancel"); command != nil {
		t.Fatalf("comment cancel returned command %v", command)
	}
	if got := ansi.Strip(m.View(pointerWidth, pointerHeight)); !strings.Contains(got, "comments") || strings.Contains(got, "ADD COMMENT") {
		t.Fatalf("pointer cancel did not return to detail:\n%s", got)
	}

	clickControl(t, m, "Del")
	clickControl(t, m, "Delete")
	surface := m.PointerSurface("board", pointerWidth, pointerHeight)
	if surface.Pointer == nil {
		t.Fatal("destructive confirmation lost pointer ownership")
	}
	if command := surface.Pointer(tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft}); command != nil {
		t.Fatal("destructive backdrop activated on press")
	}
	if command := surface.Pointer(tea.MouseReleaseMsg{X: 0, Y: 0, Button: tea.MouseLeft}); command != nil {
		t.Fatal("destructive confirmation dismissed through its backdrop")
	}
	if got := ansi.Strip(m.View(pointerWidth, pointerHeight)); !strings.Contains(got, "Confirm delete") {
		t.Fatalf("destructive confirmation was not retained:\n%s", got)
	}
}

func TestPointerSurfaceWheelAndIdleBackdropRespectPaneOwnership(t *testing.T) {
	m := New(nil, "alice")
	task := fullTask()
	task.Desc = strings.Repeat("long detail\n", 80)
	m.Open(task)

	surface := m.PointerSurface("board", pointerWidth, pointerHeight)
	if surface.Pointer == nil {
		t.Fatal("idle detail has no pointer surface")
	}
	if command := surface.Pointer(tea.MouseWheelMsg{X: 0, Y: 0, Button: tea.MouseWheelDown}); command != nil {
		t.Fatal("wheel outside the detail pane was handled")
	}
	if command := surface.Pointer(tea.MouseWheelMsg{X: 40, Y: 8, Button: tea.MouseWheelDown}); command == nil {
		t.Fatal("wheel inside the detail pane was ignored")
	} else {
		m.Update(command())
	}
	if m.scroll == 0 {
		t.Fatal("detail wheel did not use its existing scroll path")
	}

	if command := surface.Pointer(tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft}); command != nil {
		t.Fatal("idle backdrop activated on press")
	}
	command := surface.Pointer(tea.MouseReleaseMsg{X: 0, Y: 0, Button: tea.MouseLeft})
	if command == nil {
		t.Fatal("idle backdrop did not request detail dismissal")
	}
	m.Update(command())
	if m.IsOpen() {
		t.Fatal("idle backdrop did not close detail")
	}
}
