package cmdpalette

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/action"
	"github.com/RandomCodeSpace/kb/internal/tui/formview"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// openModel is an open palette on a fully featured board.
func openModel(t *testing.T) *Model {
	t.Helper()
	m := New()
	m.SetStyles(theme.New(true))
	m.SetFeatures(full)
	m.Open()
	if !m.IsOpen() {
		t.Fatal("palette did not open")
	}
	return &m
}

// press sends one rune.
func press(m *Model, letter rune) tea.Cmd {
	return m.Update(tea.KeyPressMsg{Code: letter, Text: string(letter)})
}

// typeQuery sends a whole query.
func typeQuery(m *Model, query string) {
	for _, letter := range query {
		press(m, letter)
	}
}

// TestClosedPaletteIgnoresEverything keeps the root's routing honest: a palette
// nobody opened consumes nothing and renders nothing.
func TestClosedPaletteIgnoresEverything(t *testing.T) {
	m := New()
	if m.IsOpen() {
		t.Fatal("a new palette is open")
	}
	if command := press(&m, 'j'); command != nil {
		t.Errorf("closed palette returned command %v", command)
	}
	if view := m.View(80, 24); view != "" {
		t.Errorf("closed palette rendered %q", view)
	}
	if got := m.Overlay("board", 80, 24); got != "board" {
		t.Errorf("closed palette overlaid the board: %q", got)
	}
	if _, ok := m.ConsumeChoice(); ok {
		t.Error("closed palette reported a choice")
	}
}

// TestOpenStartsAFreshSearch is why the query is not remembered: a palette that
// reopened onto yesterday's query makes the next use guess what the last one
// was looking for.
func TestOpenStartsAFreshSearch(t *testing.T) {
	m := openModel(t)
	typeQuery(m, "ship")
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.cursor == 0 && len(m.entries) > 1 {
		t.Fatal("the cursor did not move, so the reset proves nothing")
	}
	m.Close()
	m.Open()
	if got := m.query.Value(); got != "" {
		t.Errorf("reopened with query %q", got)
	}
	if m.cursor != 0 || m.offset != 0 {
		t.Errorf("reopened at cursor %d offset %d", m.cursor, m.offset)
	}
	if len(m.entries) != len(action.Listed(full)) {
		t.Errorf("reopened with %d entries, want the whole registry", len(m.entries))
	}
}

// TestTypingNarrowsAndReseatsTheCursor is the core interaction: every keystroke
// reruns the search and puts the cursor back on the new best match, because a
// cursor left where it was would be pointing at whatever slid under it.
func TestTypingNarrowsAndReseatsTheCursor(t *testing.T) {
	m := openModel(t)
	before := len(m.entries)
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	typeQuery(m, "ship")
	if len(m.entries) >= before {
		t.Errorf("query kept %d of %d entries", len(m.entries), before)
	}
	if m.cursor != 0 {
		t.Errorf("cursor is %d after typing, want 0", m.cursor)
	}
	if m.entries[0].Action.Name != "ship card" {
		t.Errorf("best match is %q", m.entries[0].Action.Name)
	}
}

// TestCursorClampsAtBothEnds keeps the arrow keys from wrapping: wrapping a
// ranked list sends the key that was reaching for the best match to the worst.
func TestCursorClampsAtBothEnds(t *testing.T) {
	m := openModel(t)
	m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	if m.cursor != 0 {
		t.Errorf("up from the top moved to %d", m.cursor)
	}
	for range len(m.entries) + 5 {
		m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if want := len(m.entries) - 1; m.cursor != want {
		t.Errorf("cursor is %d after running off the end, want %d", m.cursor, want)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	if want := len(m.entries) - 2; m.cursor != want {
		t.Errorf("up from the end moved to %d, want %d", m.cursor, want)
	}
}

// TestCtrlPAndCtrlNMoveTheCursor covers the emacs pair, which is what a reader
// who never leaves the home row reaches for.
func TestCtrlPAndCtrlNMoveTheCursor(t *testing.T) {
	m := openModel(t)
	m.Update(tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl})
	if m.cursor != 1 {
		t.Fatalf("ctrl+n moved to %d, want 1", m.cursor)
	}
	m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if m.cursor != 0 {
		t.Errorf("ctrl+p moved to %d, want 0", m.cursor)
	}
}

// TestCursorOnAnEmptyResultStaysSeated is the empty state's other half: the
// arrows have nothing to move over and must not walk off into a negative index.
func TestCursorOnAnEmptyResultStaysSeated(t *testing.T) {
	m := openModel(t)
	typeQuery(m, "zzzzzzq")
	if len(m.entries) != 0 {
		t.Fatalf("nonsense query matched %d entries", len(m.entries))
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	if m.cursor != 0 {
		t.Errorf("cursor is %d on an empty result", m.cursor)
	}
	if command := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); command != nil {
		t.Errorf("enter on an empty result returned %v", command)
	}
	if _, ok := m.ConsumeChoice(); ok {
		t.Error("enter on an empty result reported a choice")
	}
	if !m.IsOpen() {
		t.Error("enter on an empty result closed the palette, losing the query")
	}
}

// TestEnterChoosesAndClosesOnce is the handoff to the root: the choice is
// reported exactly once, and the palette is closed by the time it is.
func TestEnterChoosesAndClosesOnce(t *testing.T) {
	m := openModel(t)
	typeQuery(m, "ship")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.IsOpen() {
		t.Error("enter did not close the palette")
	}
	choice, ok := m.ConsumeChoice()
	if !ok {
		t.Fatal("enter reported no choice")
	}
	if choice.Name != "ship card" {
		t.Errorf("chose %q", choice.Name)
	}
	if _, ok := m.ConsumeChoice(); ok {
		t.Error("the choice was reported twice")
	}
}

// TestEscapeAndTheChordClose covers both dismissals, and asserts neither of them
// leaves a choice behind for the root to run.
func TestEscapeAndTheChordClose(t *testing.T) {
	for _, test := range []struct {
		name string
		key  tea.KeyPressMsg
	}{
		{name: "escape", key: tea.KeyPressMsg{Code: tea.KeyEscape}},
		{name: "the chord again", key: tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl}},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := openModel(t)
			typeQuery(m, "ship")
			m.Update(test.key)
			if m.IsOpen() {
				t.Error("palette stayed open")
			}
			if _, ok := m.ConsumeChoice(); ok {
				t.Error("a dismissal reported a choice")
			}
		})
	}
}

// TestSelectAllMarksTheQuery is the ctrl+a contract every kb text field carries.
func TestSelectAllMarksTheQuery(t *testing.T) {
	m := openModel(t)
	typeQuery(m, "ship")
	m.Update(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	if !m.mark.Active(queryMarkField) {
		t.Fatal("ctrl+a did not mark the query")
	}
	press(m, 'x')
	if got := m.query.Value(); got != "x" {
		t.Errorf("typing over the mark left %q, want x", got)
	}
	if len(m.entries) == 0 {
		t.Error("typing over the mark did not rerun the search")
	}
}

// TestSetFeaturesRefreshesAnOpenPalette keeps the offered list in step with a
// board whose backends were wired after the palette opened.
func TestSetFeaturesRefreshesAnOpenPalette(t *testing.T) {
	m := New()
	m.SetStyles(theme.New(true))
	m.Open()
	bare := len(m.entries)
	m.SetFeatures(full)
	if len(m.entries) <= bare {
		t.Errorf("features went from bare to full and the list stayed at %d", len(m.entries))
	}
	m.Close()
	m.SetFeatures(action.Features{})
	if m.entries != nil {
		t.Error("setting features reopened a closed palette")
	}
}

// TestNonKeyMessagesAreIgnored keeps the palette out of the way of every async
// message the root is still routing while it is open.
func TestNonKeyMessagesAreIgnored(t *testing.T) {
	m := openModel(t)
	if command := m.Update(tea.WindowSizeMsg{Width: 10, Height: 10}); command != nil {
		t.Errorf("a resize returned %v", command)
	}
	if !m.IsOpen() {
		t.Error("a resize closed the palette")
	}
	if IsMessage(tea.WindowSizeMsg{}) {
		t.Error("IsMessage claimed a resize")
	}
	if !IsMessage(tea.KeyPressMsg{Code: 'j'}) {
		t.Error("IsMessage disclaimed a key press")
	}
}

// TestSetStylesIgnoresNil keeps a caller that has not built a theme yet from
// dropping the fallback on the floor.
func TestSetStylesIgnoresNil(t *testing.T) {
	m := New()
	m.SetStyles(nil)
	if m.themeStyles() == nil {
		t.Fatal("themeStyles returned nil")
	}
	styles := theme.New(false)
	m.SetStyles(styles)
	if m.themeStyles() != styles {
		t.Error("SetStyles did not adopt the theme")
	}
}

// TestQueryIsSanitized keeps untrusted control sequences out of a rendered
// frame and out of the search.
func TestQueryIsSanitized(t *testing.T) {
	if got := sanitize("ship\x1b[31m\x07card"); got != "shipcard" {
		t.Errorf("sanitize left %q", got)
	}
	m := openModel(t)
	m.query.SetValue("sh\x07ip")
	m.refresh()
	if len(m.entries) == 0 || m.entries[0].Action.Name != "ship card" {
		t.Errorf("a bell in the query broke the search: %d entries", len(m.entries))
	}
}

// TestMarkKeyIsTheSharedConstant keeps the field name the palette marks in step
// with the shared form helper.
func TestMarkKeyIsTheSharedConstant(t *testing.T) {
	m := openModel(t)
	typeQuery(m, "ship")
	m.Update(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	if formview.SelectAllKey != "ctrl+a" {
		t.Fatalf("the shared select-all key is %q", formview.SelectAllKey)
	}
	if !m.mark.Active(queryMarkField) {
		t.Error("the mark is not held under the palette's field name")
	}
	if m.mark.Active("somewhere else") {
		t.Error("the mark leaked onto a sibling field name")
	}
}

// TestViewRendersThePanelAtTheThemeGeometry keeps the palette off literals: the
// panel is the proportional one of spec section 4 and nothing else.
func TestViewRendersThePanelAtTheThemeGeometry(t *testing.T) {
	m := openModel(t)
	styles := theme.New(true)
	for _, size := range [][2]int{{120, 40}, {80, 24}, {60, 18}} {
		width, height := size[0], size[1]
		paneWidth, paneHeight := styles.Metrics.OverlayPane(width, height)
		panel := m.layout(width, height)
		if panel.width != paneWidth || panel.height != paneHeight {
			t.Errorf("%dx%d: panel is %dx%d, want %dx%d",
				width, height, panel.width, panel.height, paneWidth, paneHeight)
		}
		lines := strings.Split(m.View(width, height), "\n")
		if len(lines) != height {
			t.Errorf("%dx%d: view is %d rows", width, height, len(lines))
		}
		for index, line := range lines {
			if got := ansi.StringWidth(line); got != width {
				t.Errorf("%dx%d: row %d is %d cells", width, height, index, got)
			}
		}
	}
}

// TestOverlayKeepsTheFrame is the compositor contract: the shadow and the panel
// land inside the frame the board handed over, never past it.
func TestOverlayKeepsTheFrame(t *testing.T) {
	m := openModel(t)
	const width, height = 90, 28
	background := strings.TrimSuffix(strings.Repeat(strings.Repeat("b", width)+"\n", height), "\n")
	lines := strings.Split(m.Overlay(background, width, height), "\n")
	if len(lines) != height {
		t.Fatalf("overlay is %d rows, want %d", len(lines), height)
	}
	for index, line := range lines {
		if got := ansi.StringWidth(line); got != width {
			t.Errorf("row %d is %d cells, want %d", index, got, width)
		}
	}
}

// TestTinyFrameStillRenders keeps the palette from panicking on a terminal too
// small to center anything in.
func TestTinyFrameStillRenders(t *testing.T) {
	m := openModel(t)
	for _, size := range [][2]int{{1, 1}, {4, 3}, {0, 0}, {-3, -3}, {24, 8}} {
		view := m.View(size[0], size[1])
		overlay := m.Overlay("x", size[0], size[1])
		if view == "" && overlay == "" {
			t.Errorf("%dx%d rendered nothing at all", size[0], size[1])
		}
	}
}
