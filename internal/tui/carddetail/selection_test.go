package carddetail

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestReadOnlySelectAllAndCopyUseDisplayedDetailText(t *testing.T) {
	m := openRefModel(t, "first description line\n\nsecond line")
	if command := m.Update(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl}); command != nil {
		t.Fatalf("ctrl+a returned command %v", command)
	}
	selected := m.selectedText()
	if selected == "" {
		t.Fatalf("ctrl+a selected no text; rows=%+v", m.selectable)
	}
	for _, want := range []string{"first description line", "second line", "also kb://task/9"} {
		if !strings.Contains(selected, want) {
			t.Errorf("selection missing %q:\n%s", want, selected)
		}
	}
	if strings.Contains(selected, "alice") || strings.Contains(selected, "UTC") {
		t.Errorf("selection included comment identity or timestamp:\n%s", selected)
	}
	command := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if command == nil || fmt.Sprint(command()) != selected {
		t.Fatalf("clipboard request did not carry selected text")
	}
	if m.statusMessage != "Copy requested" {
		t.Fatalf("copy status = %q", m.statusMessage)
	}
	if m.selectedText() != selected {
		t.Fatal("copy status rebuild cleared the selection")
	}
}

func TestDescriptionDragSelectsAndReferenceDragDoesNotActivate(t *testing.T) {
	m := openRefModel(t, "select these words before kb://task/111")
	surface := m.PointerSurface("board", pointerWidth, pointerHeight)
	x, y := referencePoint(surface.Content, "kb://task/111")
	if x < 0 {
		t.Fatal("reference is not visible")
	}
	deliver := func(command tea.Cmd) tea.Cmd {
		t.Helper()
		if command == nil {
			t.Fatal("selection gesture produced no message")
		}
		message, guarded := m.ResolvePointerMessage(command())
		if !guarded || message == nil {
			t.Fatal("selection gesture escaped the detail session guard")
		}
		return m.Update(message)
	}
	if command := deliver(surface.Pointer(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})); command != nil {
		t.Fatal("selection press activated the reference")
	}
	surface = m.PointerSurface("board", pointerWidth, pointerHeight)
	if command := deliver(surface.Pointer(tea.MouseMotionMsg{X: x + 5, Y: y, Button: tea.MouseLeft})); command != nil {
		t.Fatal("selection drag activated the reference")
	}
	surface = m.PointerSurface("board", pointerWidth, pointerHeight)
	if command := deliver(surface.Pointer(tea.MouseReleaseMsg{X: x + 5, Y: y, Button: tea.MouseLeft})); command != nil {
		t.Fatal("selection release activated the reference")
	}
	if m.selectedText() == "" || !containsReverseVideo(strings.Join(m.bodyLines, "\n")) {
		t.Fatalf("drag did not produce a visible text selection: %q", m.selectedText())
	}
}

func TestTerminalSelectionFreezesTextAndKeepsEscapeVisibleAtNarrowWidth(t *testing.T) {
	m := openRefModel(t, "immutable description")
	m.Update(tea.KeyPressMsg{Code: 'V', Text: "V", Mod: tea.ModShift})
	if !m.TerminalSelectionActive() || !m.OwnsInput() {
		t.Fatal("terminal selection did not take input ownership")
	}
	snapshot := m.terminalSnapshot
	m.task.Desc = "changed in the background"
	m.rebuildBody()
	if m.terminalSnapshot != snapshot {
		t.Fatal("background rebuild changed the terminal selection snapshot")
	}
	lines := strings.Split(ansi.Strip(m.TerminalSelectionView(12, 5)), "\n")
	if got := lines[len(lines)-1]; !strings.HasPrefix(got, "esc return") {
		t.Fatalf("narrow fallback footer = %q", got)
	}
	for _, height := range []int{1, 2} {
		lines = strings.Split(ansi.Strip(m.TerminalSelectionView(12, height)), "\n")
		if len(lines) != height {
			t.Fatalf("height %d fallback emitted %d rows: %q", height, len(lines), lines)
		}
		if got := lines[len(lines)-1]; !strings.HasPrefix(got, "esc return") {
			t.Fatalf("height %d fallback exit hint = %q", height, got)
		}
		for index, line := range lines {
			if got := ansi.StringWidth(line); got > 12 {
				t.Fatalf("height %d row %d width = %d: %q", height, index, got, line)
			}
		}
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.TerminalSelectionActive() {
		t.Fatal("escape did not restore the detail view")
	}
}

func TestTerminalSelectionRejectsQueuedPointerControlAndDragMessages(t *testing.T) {
	m := openRefModel(t, "see kb://task/111 before selecting this text")
	surface := m.PointerSurface("board", pointerWidth, pointerHeight)
	x, y := referencePoint(surface.Content, "kb://task/111")
	if x < 0 {
		t.Fatalf("reference is not visible:\n%s", ansi.Strip(surface.Content))
	}
	queuedPress := surface.Pointer(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	if queuedPress == nil {
		t.Fatal("reference press produced no queued pointer message")
	}
	oldSession := m.pointerSession
	m.Update(tea.KeyPressMsg{Code: 'V', Text: "V", Mod: tea.ModShift})
	if !m.TerminalSelectionActive() || m.pointerSession == oldSession {
		t.Fatalf("terminal entry did not invalidate pointer session %d", oldSession)
	}
	if got, guarded := m.ResolvePointerMessage(busyResult(t, queuedPress)); !guarded || got != nil {
		t.Fatalf("queued previous-frame press resolved during terminal selection: guarded=%v message=%#v", guarded, got)
	}

	nativeSession := m.pointerSession
	queued := []tea.Msg{
		selectionMoveMsg{point: textPoint{row: 1, cell: 2}},
		selectionEndMsg{point: textPoint{row: 1, cell: 2}},
		OpenTaskRefMsg{Seq: 111},
	}
	for _, message := range queued {
		wrapped := pointerControlMsg{
			pointerSession: nativeSession, actionSession: m.actionSession,
			driftSession: m.driftSession, driftGeneration: m.driftGeneration,
			message: message,
		}
		if got, guarded := m.ResolvePointerMessage(wrapped); !guarded || got != nil {
			t.Errorf("queued %T resolved during terminal selection: guarded=%v message=%#v", message, guarded, got)
		}
	}

	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.TerminalSelectionActive() || m.pointerSession == nativeSession {
		t.Fatalf("keyboard exit did not restore detail with a fresh pointer session")
	}
	staleExit := pointerControlMsg{
		pointerSession: nativeSession, actionSession: m.actionSession,
		driftSession: m.driftSession, driftGeneration: m.driftGeneration,
		message: OpenTaskRefMsg{Seq: 111},
	}
	if got, guarded := m.ResolvePointerMessage(staleExit); !guarded || got != nil {
		t.Fatalf("terminal-frame message resolved after exit: guarded=%v message=%#v", guarded, got)
	}
}

func TestEmptyDetailDoesNotSelectOrCopyStaleText(t *testing.T) {
	m := openRefModel(t, "")
	m.comments = nil
	m.rebuildBody()
	m.Update(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	if m.selectedText() != "" {
		t.Fatalf("empty detail selected stale text %q", m.selectedText())
	}
	if command := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"}); command != nil {
		t.Fatalf("empty detail requested clipboard write: %v", command)
	}
}

func TestSelectionAffordancesStayAccurateAtNarrowWidths(t *testing.T) {
	m := openRefModel(t, "description")
	ladder := m.footerLadder(80)
	if !strings.Contains(strings.Join(ladder.Middle, " "), "drag description/comments") {
		t.Fatalf("detail selection hint = %v", ladder.Middle)
	}
	for _, width := range []int{1, 12, 24} {
		controls := m.pointerFooterControls(width)
		found := false
		for _, control := range controls {
			found = found || control.label == "Terminal"
		}
		if !found {
			t.Fatalf("width %d omitted terminal selection control: %+v", width, controls)
		}
	}
}

func TestNearestSelectionPointClampsAcrossBlankRowsAndClearIsIdempotent(t *testing.T) {
	m := openRefModel(t, "selection source")
	m.selectable = []selectableRow{
		{row: 2, width: 0},
		{row: 4, text: "alpha", width: 5, block: 1},
		{row: 8, text: "omega", width: 5, block: 2},
	}
	inset := m.styles.Metrics.OverlayInsetX
	point, ok := m.nearestSelectablePoint(6, inset-20)
	if !ok || point != (textPoint{row: 4, cell: 0}) {
		t.Fatalf("nearest point above blank gap = %+v, %t", point, ok)
	}
	point, ok = m.nearestSelectablePoint(99, inset+99)
	if !ok || point != (textPoint{row: 8, cell: 4}) {
		t.Fatalf("nearest point below content = %+v, %t", point, ok)
	}
	first, last := orderedPoints(textPoint{row: 8, cell: 3}, textPoint{row: 4, cell: 1})
	if first != (textPoint{row: 4, cell: 1}) || last != (textPoint{row: 8, cell: 3}) {
		t.Fatalf("reversed selection ordered as %+v to %+v", first, last)
	}

	m.selectable = []selectableRow{{row: 2, width: 0}}
	if point, ok := m.nearestSelectablePoint(2, inset); ok || point != (textPoint{}) {
		t.Fatalf("blank-only point = %+v, %t", point, ok)
	}
	if m.ClearSelection() {
		t.Fatal("clearing an inactive selection reported a change")
	}
	m.textSelection = textSelection{active: true, moved: true}
	if !m.ClearSelection() || m.textSelection.active || m.ClearSelection() {
		t.Fatalf("selection clear was not one-shot: %+v", m.textSelection)
	}
}

func TestTerminalSelectionReportsEmptyReadableDetail(t *testing.T) {
	m := openRefModel(t, "")
	m.comments = nil
	m.rebuildBody()
	m.Update(tea.KeyPressMsg{Code: 'V', Text: "V", Mod: tea.ModShift})
	if m.TerminalSelectionActive() || m.statusMessage != "No description or comment text" {
		t.Fatalf("empty terminal selection = active:%t status:%q", m.TerminalSelectionActive(), m.statusMessage)
	}
}
