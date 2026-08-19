package carddetail

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/board"
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
	press := surface.Pointer(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	if press == nil || m.Update(press()) != nil {
		t.Fatalf("%q did not enter pressed state", label)
	}
	pressed := m.PointerSurface("board", pointerWidth, pointerHeight)
	if !strings.Contains(pressed.Content, "\x1b[7m") {
		t.Fatalf("%q did not render pressed feedback", label)
	}
	command := pressed.Pointer(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	if command == nil {
		t.Fatalf("%q ignored pointer release at %d,%d:\n%s", label, x, y, ansi.Strip(surface.Content))
	}
	activate := m.Update(command())
	if activate == nil {
		t.Fatalf("%q release produced no action", label)
	}
	message := activate()
	if resolved, ok := m.ResolvePointerMessage(message); ok {
		message = resolved
	}
	return m.Update(message)
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

func clickDetailText(t *testing.T, m *Model, text string) tea.Cmd {
	t.Helper()
	surface := m.PointerSurface("board", pointerWidth, pointerHeight)
	x, y := -1, -1
	for row, line := range strings.Split(ansi.Strip(surface.Content), "\n") {
		if column := strings.Index(line, text); column >= 0 {
			x, y = ansi.StringWidth(line[:column]), row
			break
		}
	}
	if x < 0 {
		t.Fatalf("detail text %q is not visible:\n%s", text, ansi.Strip(surface.Content))
	}
	press := surface.Pointer(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	if press == nil || m.Update(press()) != nil {
		t.Fatalf("detail text %q did not enter pressed state", text)
	}
	pressed := m.PointerSurface("board", pointerWidth, pointerHeight)
	pressedVisible := false
	for _, line := range strings.Split(ansi.Strip(pressed.Content), "\n") {
		if strings.Contains(line, text) && strings.Contains(line, "! ") {
			pressedVisible = true
			break
		}
	}
	if !pressedVisible {
		t.Fatalf("detail text %q omitted pressed feedback:\n%s", text, ansi.Strip(pressed.Content))
	}
	release := pressed.Pointer(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseNone})
	if release == nil {
		t.Fatalf("detail text %q ignored rerendered release", text)
	}
	activate := m.Update(release())
	if activate == nil {
		t.Fatalf("detail text %q release produced no activation", text)
	}
	message := activate()
	if resolved, ok := m.ResolvePointerMessage(message); ok {
		message = resolved
	}
	return m.Update(message)
}

func TestPointerSelectsExactCommentAndLinkRows(t *testing.T) {
	st := &actionStore{comments: []store.Comment{
		{ID: 4, Author: "alice", Body: "first target"},
		{ID: 9, Author: "bob", Body: "second target"},
	}}
	m := openActionModel(t, st)
	clickControl(t, m, "Del")
	clickDetailText(t, m, "second target")
	if m.selection != 1 || m.confirm {
		t.Fatalf("comment row selection=%d confirm=%v", m.selection, m.confirm)
	}
	clickControl(t, m, "Delete")
	write := clickControl(t, m, "Confirm delete")
	if write == nil {
		t.Fatal("selected comment delete did not start write")
	}
	m.Update(write())
	if st.deletedID != 9 {
		t.Fatalf("pointer deleted comment %d, want 9", st.deletedID)
	}

	m = openActionModel(t, &actionStore{links: store.TaskLinks{
		Blocks: []board.Task{{ID: "a", Seq: 2, Title: "first link"}, {ID: "b", Seq: 3, Title: "second link"}},
	}})
	clickControl(t, m, "Unlink")
	clickDetailText(t, m, "#3")
	if m.selection != 1 || m.confirm {
		t.Fatalf("link row selection=%d confirm=%v", m.selection, m.confirm)
	}
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
	if got := ansi.Strip(m.View(pointerWidth, pointerHeight)); !strings.Contains(got, "COMMENTS") || strings.Contains(got, "ADD COMMENT") {
		t.Fatalf("pointer cancel did not return to detail:\n%s", got)
	}

	clickControl(t, m, "Del")
	clickControl(t, m, "Delete")
	surface := m.PointerSurface("board", pointerWidth, pointerHeight)
	if surface.Pointer == nil {
		t.Fatal("destructive confirmation lost pointer ownership")
	}
	if command := surface.Pointer(tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft}); command != nil {
		m.Update(command())
	}
	if command := surface.Pointer(tea.MouseReleaseMsg{X: 0, Y: 0, Button: tea.MouseLeft}); command != nil {
		m.Update(command())
	}
	if got := ansi.Strip(m.View(pointerWidth, pointerHeight)); !strings.Contains(got, "Confirm delete") {
		t.Fatalf("destructive confirmation was not retained:\n%s", got)
	}
}

func TestPointerSurfaceWheelAndIdleBackdropRespectPaneOwnership(t *testing.T) {
	m := New(nil, "alice", testStyles())
	task := fullTask()
	task.Desc = strings.Repeat("long detail\n", 80)
	m.Open(task)

	surface := m.PointerSurface("board", pointerWidth, pointerHeight)
	if surface.Pointer == nil {
		t.Fatal("idle detail has no pointer surface")
	}
	if command := surface.Pointer(tea.MouseWheelMsg{X: 0, Y: 0, Button: tea.MouseWheelDown}); command != nil {
		m.Update(command())
		if m.scroll != 0 {
			t.Fatal("wheel outside the detail pane scrolled content")
		}
	}
	if command := surface.Pointer(tea.MouseWheelMsg{X: 40, Y: 8, Button: tea.MouseWheelDown}); command == nil {
		t.Fatal("wheel inside the detail pane was ignored")
	} else {
		followup := m.Update(command())
		if followup == nil {
			t.Fatal("detail wheel did not produce scroll action")
		}
		message, _ := m.ResolvePointerMessage(followup())
		m.Update(message)
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
	message, _ := m.ResolvePointerMessage(command())
	m.Update(message)
	if m.IsOpen() {
		t.Fatal("idle backdrop did not close detail")
	}
}

func TestPointerSurfaceRejectsReleaseFromPriorDetailSession(t *testing.T) {
	m := openActionModel(t, &actionStore{})
	surface := m.PointerSurface("board", pointerWidth, pointerHeight)
	x, y := renderedControlPoint(t, surface.Content, "Comment")
	press := surface.Pointer(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	if press == nil || m.Update(press()) != nil {
		t.Fatal("detail control did not enter pressed state")
	}
	release := m.PointerSurface("board", pointerWidth, pointerHeight).Pointer(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	if release == nil {
		t.Fatal("detail release produced no message")
	}
	stale := m.Update(release())
	if stale == nil {
		t.Fatal("detail release produced no guarded action")
	}
	task := m.task
	m.Close()
	m.Open(task)
	message, pointerMessage := m.ResolvePointerMessage(stale())
	if !pointerMessage || message != nil || m.action != actionNone {
		t.Fatalf("stale detail release crossed session: recognized=%v message=%#v action=%v", pointerMessage, message, m.action)
	}
}
