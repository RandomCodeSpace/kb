package cmdpalette

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/pointer"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

const paneWidth, paneHeight = 84, 26

// rowCell is the terminal cell the result row at display index lands on, which
// is the panel origin plus the header band and the query row above the list.
func rowCell(m *Model, display int) (int, int) {
	panel := m.layout(paneWidth, paneHeight)
	return panel.x + 2, panel.y + 2 + display
}

// hover drives one bare motion through the frame's own map and applies the
// feedback it produced, exactly as the root model's routing would.
func hover(t *testing.T, m *Model, x, y int) {
	t.Helper()
	handler := m.MouseHandler(paneWidth, paneHeight)
	if handler == nil {
		t.Fatal("an open palette installed no pointer handler")
	}
	command := handler(tea.MouseMotionMsg{X: x, Y: y})
	if command == nil {
		t.Fatalf("motion at (%d,%d) produced no feedback", x, y)
	}
	m.Update(command())
}

// activate drives a click and release on one cell and routes both results.
func activate(t *testing.T, m *Model, x, y int) tea.Cmd {
	t.Helper()
	handler := m.MouseHandler(paneWidth, paneHeight)
	if handler == nil {
		t.Fatal("an open palette installed no pointer handler")
	}
	if command := handler(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}); command != nil {
		m.Update(command())
	}
	command := handler(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	if command == nil {
		return nil
	}
	// The release clears the press feedback and hands back the activation the
	// region carried, which is a second message the caller routes in turn.
	activation := m.Update(command())
	if activation == nil {
		return nil
	}
	return m.Update(activation())
}

// firstEntryRow is the display index of the first row that carries an action,
// skipping whatever section band the unfiltered list opens with.
func firstEntryRow(m *Model) int {
	panel := m.layout(paneWidth, paneHeight)
	for index, row := range m.visibleRows(panel) {
		if row.kind == rowEntry {
			return index
		}
	}
	return -1
}

// TestClosedPaletteInstallsNoPointerMap keeps a palette nobody opened from
// claiming the board's mouse.
func TestClosedPaletteInstallsNoPointerMap(t *testing.T) {
	m := New()
	if handler := m.MouseHandler(paneWidth, paneHeight); handler != nil {
		t.Error("a closed palette installed a pointer handler")
	}
}

// TestHoverBecomesTheActingSelection is spec section 10.5.2 on a real surface:
// the hovered row renders the cursor cue and the keyboard cursor's own position
// renders nothing, so exactly one row is marked at any moment.
func TestHoverBecomesTheActingSelection(t *testing.T) {
	m := openModel(t)
	typeQuery(m, "car")
	if len(m.entries) < 2 {
		t.Skip("the ranked list is too short to distinguish two rows")
	}
	first := firstEntryRow(m)
	x, y := rowCell(m, first+1)
	hover(t, m, x, y)

	if !m.machine().Mode(m.pointerState) {
		t.Fatal("motion onto a result row did not turn mouse mode on")
	}
	if got := m.acting(); got != 1 {
		t.Fatalf("acting selection = %d, want the hovered row 1", got)
	}
	if m.cursor != 0 {
		t.Fatalf("hover moved the keyboard cursor to %d", m.cursor)
	}

	panel := m.layout(paneWidth, paneHeight)
	gutter := ansi.Strip(m.themeStyles().Glyph.Rail)
	cued := 0
	for _, row := range m.visibleRows(panel) {
		if row.kind != rowEntry {
			continue
		}
		if strings.HasPrefix(strings.TrimLeft(ansi.Strip(m.renderListRow(panel, row)), " "), gutter) {
			cued++
		}
	}
	if cued != 1 {
		t.Fatalf("%d rows wear the cursor cue, want exactly one", cued)
	}
}

// TestHoveredRowRaisesItsFillWithoutReflowing is section 10.5.1 plus the
// no-reflow parity rule of 10.4.4 on the row this slice actually wires.
func TestHoveredRowRaisesItsFillWithoutReflowing(t *testing.T) {
	m := openModel(t)
	typeQuery(m, "car")
	panel := m.layout(paneWidth, paneHeight)
	first := firstEntryRow(m)
	row := m.visibleRows(panel)[first]

	rest := m.renderListRow(panel, row)
	x, y := rowCell(m, first)
	hover(t, m, x, y)
	hovered := m.renderListRow(panel, row)

	if rest == hovered {
		t.Fatal("the hovered row rendered no cue at all")
	}
	if got, want := ansi.StringWidth(hovered), ansi.StringWidth(rest); got != want {
		t.Errorf("the hovered row is %d cells, want %d", got, want)
	}
	if got, want := ansi.Strip(hovered), ansi.Strip(rest); got != want {
		t.Errorf("hover changed the row's text:\n got %q\nwant %q", got, want)
	}
	if !strings.Contains(hovered, band(m.themeStyles(), theme.OverlayBand)) {
		t.Errorf("the hovered row does not wear the raised fill:\n%q", hovered)
	}
}

// band is the background escape of one slot, which is what "the row raised a
// tier" means at the cell level.
func band(styles *theme.Styles, slot theme.Slot) string {
	rendered := styles.On(theme.FgBase, slot).Render(" ")
	start := strings.Index(rendered, "48;")
	if start < 0 {
		return rendered
	}
	end := strings.Index(rendered[start:], "m")
	if end < 0 {
		return rendered
	}
	return rendered[start : start+end]
}

// TestClickOnARowRunsItsAction is the gesture: single click activates, which is
// the shipped reality the hover slice builds on rather than re-litigates.
func TestClickOnARowRunsItsAction(t *testing.T) {
	m := openModel(t)
	typeQuery(m, "car")
	first := firstEntryRow(m)
	panel := m.layout(paneWidth, paneHeight)
	want := m.entries[m.visibleRows(panel)[first].entry].Action

	x, y := rowCell(m, first)
	activate(t, m, x, y)
	choice, ok := m.ConsumeChoice()
	if !ok {
		t.Fatal("clicking a result row committed nothing")
	}
	if choice.Name != want.Name {
		t.Errorf("ran %q, want %q", choice.Name, want.Name)
	}
	if m.IsOpen() {
		t.Error("running an action left the palette open")
	}
}

// TestClickOnTheBackdropDismisses is section 10.5.3's backdrop: the region
// outside the panel is the overlay's own, not a hole through to the board.
func TestClickOnTheBackdropDismisses(t *testing.T) {
	m := openModel(t)
	activate(t, m, 0, 0)
	if m.IsOpen() {
		t.Error("a click outside the panel did not dismiss the palette")
	}
	if _, ok := m.ConsumeChoice(); ok {
		t.Error("a dismissal committed an action")
	}
}

// TestMotionOntoTheBackdropClearsMouseMode is row 3 on this surface: the
// overlay consumes the motion and turns its own mouse mode off rather than
// leaving a row lit under a pointer that has left it.
func TestMotionOntoTheBackdropClearsMouseMode(t *testing.T) {
	m := openModel(t)
	first := firstEntryRow(m)
	x, y := rowCell(m, first)
	hover(t, m, x, y)
	if !m.machine().Mode(m.pointerState) {
		t.Fatal("motion onto a result row did not turn mouse mode on")
	}
	hover(t, m, 0, 0)
	if m.machine().Mode(m.pointerState) {
		t.Fatal("motion onto the backdrop left mouse mode on")
	}
	if got := m.acting(); got != m.cursor {
		t.Fatalf("acting selection = %d, want the keyboard cursor %d", got, m.cursor)
	}
}

// TestArrowAdoptsTheHoveredRowThenMoves is row 7 end to end: down while row 1
// is hovered lands on row 2, not on cursor+1.
func TestArrowAdoptsTheHoveredRowThenMoves(t *testing.T) {
	m := openModel(t)
	typeQuery(m, "c")
	if len(m.entries) < 4 {
		t.Skip("the ranked list is too short to distinguish adopt-then-move")
	}
	first := firstEntryRow(m)
	x, y := rowCell(m, first+2)
	hover(t, m, x, y)
	if got := m.acting(); got != 2 {
		t.Fatalf("acting selection = %d, want the hovered row 2", got)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.cursor != 3 {
		t.Fatalf("cursor = %d, want the adopted anchor plus one", m.cursor)
	}
	if m.machine().Mode(m.pointerState) {
		t.Fatal("the arrow left mouse mode on")
	}
}

// TestAnyOtherKeyRunsAgainstTheKeyboardCursor is row 8: enter commits the
// keyboard cursor's row, never the one the pointer happens to rest over.
func TestAnyOtherKeyRunsAgainstTheKeyboardCursor(t *testing.T) {
	m := openModel(t)
	typeQuery(m, "c")
	if len(m.entries) < 2 {
		t.Skip("the ranked list is too short to distinguish two rows")
	}
	want := m.entries[0].Action
	first := firstEntryRow(m)
	x, y := rowCell(m, first+1)
	hover(t, m, x, y)
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	choice, ok := m.ConsumeChoice()
	if !ok {
		t.Fatal("enter committed nothing")
	}
	if choice.Name != want.Name {
		t.Errorf("enter ran the hovered row %q, want the keyboard cursor's %q", choice.Name, want.Name)
	}
}

// TestAStaleActivationRunsNothing is the generation guard: a release resolved
// against a frame the query has since re-filtered must not run whatever action
// slid into that row's place.
func TestAStaleActivationRunsNothing(t *testing.T) {
	m := openModel(t)
	stale := pointerActionMsg{entry: 0, generation: m.generation}
	typeQuery(m, "car")
	if command := m.Update(stale); command != nil {
		t.Errorf("a stale activation returned %v", command)
	}
	if _, ok := m.ConsumeChoice(); ok {
		t.Error("a stale activation committed an action")
	}
	if !m.IsOpen() {
		t.Error("a stale activation closed the palette")
	}
	if command := m.Update(pointerActionMsg{dismiss: true, generation: stale.generation}); command != nil {
		t.Errorf("a stale dismissal returned %v", command)
	}
	if !m.IsOpen() {
		t.Error("a stale dismissal closed the palette")
	}
}

// TestAnActivationOutsideTheEntriesRunsNothing bounds the index a map region
// carries against the list the model holds now.
func TestAnActivationOutsideTheEntriesRunsNothing(t *testing.T) {
	m := openModel(t)
	if command := m.Update(pointerActionMsg{entry: len(m.entries), generation: m.generation}); command != nil {
		t.Errorf("an out-of-range activation returned %v", command)
	}
	if _, ok := m.ConsumeChoice(); ok {
		t.Error("an out-of-range activation committed an action")
	}
	if command := m.Update(pointerActionMsg{entry: -1, generation: m.generation}); command != nil {
		t.Errorf("a negative activation returned %v", command)
	}
}

// TestTheQueryFieldIsNotHoverable is section 10.5.1's exclusion: a row that is
// not activatable is not hoverable, and the query field is a text field.
func TestTheQueryFieldIsNotHoverable(t *testing.T) {
	m := openModel(t)
	panel := m.layout(paneWidth, paneHeight)
	hitMap := m.MouseHandler(paneWidth, paneHeight)
	if hitMap == nil {
		t.Fatal("an open palette installed no pointer handler")
	}
	command := hitMap(tea.MouseMotionMsg{X: panel.x + 2, Y: panel.y + 1})
	if command == nil {
		t.Fatal("motion over the query row produced no feedback")
	}
	m.Update(command())
	if m.machine().Mode(m.pointerState) {
		t.Fatalf("the query row is hoverable: %q", m.pointerState.Hovered())
	}
}

// TestAnEmptyResultListRegistersNoRows keeps the empty state of section 10.8.3
// from carrying a hit region for a row it did not draw.
func TestAnEmptyResultListRegistersNoRows(t *testing.T) {
	m := openModel(t)
	typeQuery(m, "zzzzzzq")
	panel := m.layout(paneWidth, paneHeight)
	if rows := m.visibleRows(panel); rows != nil {
		t.Fatalf("an empty result list drew %d rows", len(rows))
	}
	x, y := rowCell(m, 0)
	handler := m.MouseHandler(paneWidth, paneHeight)
	if command := handler(tea.MouseMotionMsg{X: x, Y: y}); command != nil {
		m.Update(command())
	}
	if m.machine().Mode(m.pointerState) {
		t.Fatalf("an empty result list hovered %q", m.pointerState.Hovered())
	}
}

// TestAPanelWithNoBodyRegistersNoRows is the degenerate frame: a panel too short
// for its own query row has no list to address.
func TestAPanelWithNoBodyRegistersNoRows(t *testing.T) {
	m := openModel(t)
	panel := m.layout(4, 2)
	if rows := m.visibleRows(panel); rows != nil {
		t.Fatalf("a bodyless panel drew %d rows", len(rows))
	}
	if handler := m.MouseHandler(4, 2); handler == nil {
		t.Fatal("a small frame installed no pointer handler")
	}
}

// TestControlIDsAreTheMachinesOwnRows keeps the id a row registers for its
// click and the id the machine reads a row index out of as one string.
func TestControlIDsAreTheMachinesOwnRows(t *testing.T) {
	rows := pointer.RowsWithPrefix(rowPrefix)
	for _, entry := range []int{0, 1, 17} {
		got, ok := rows(controlID(entry))
		if !ok || got != entry {
			t.Errorf("rows(%q) = (%d, %v), want (%d, true)", controlID(entry), got, ok, entry)
		}
	}
}
