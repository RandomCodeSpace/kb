package cmdpalette

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
	"github.com/RandomCodeSpace/kb/internal/tui/widget"
)

// structure is the ASCII-pinned form of a rendered frame, per spec section 6.4.
func structure(rendered string) string {
	lines := strings.Split(ansi.Strip(theme.Downsample(rendered, theme.StructureProfile)), "\n")
	for index := range lines {
		lines[index] = strings.TrimRight(lines[index], " ")
	}
	return strings.Trim(strings.Join(lines, "\n"), "\n") + "\n"
}

// TestPaletteUnfilteredGolden is the opening frame: every action the board
// offers, under its section bands, with the cursor on the first row.
func TestPaletteUnfilteredGolden(t *testing.T) {
	m := openModel(t)
	golden.RequireEqual(t, []byte(structure(m.View(84, 26))))
}

// TestPaletteFilteredGolden is a ranked list: no section bands, and the match
// runs highlighted inside each label.
func TestPaletteFilteredGolden(t *testing.T) {
	m := openModel(t)
	typeQuery(m, "car")
	golden.RequireEqual(t, []byte(structure(m.View(84, 26))))
}

// TestPaletteEmptyGolden is spec section 10.8.3: a palette with no matches is an
// empty state, not a blank panel.
func TestPaletteEmptyGolden(t *testing.T) {
	m := openModel(t)
	typeQuery(m, "zzzzzzq")
	golden.RequireEqual(t, []byte(structure(m.View(84, 26))))
}

// TestPaletteColorGolden pins the hues over a board surface: the shadow bands,
// the header and footer bands, the focus gutter and the match highlight.
func TestPaletteColorGolden(t *testing.T) {
	m := openModel(t)
	typeQuery(m, "car")
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	const width, height = 64, 20
	background := strings.TrimSuffix(strings.Repeat(strings.Repeat("b", width)+"\n", height), "\n")
	golden.RequireEqual(t, []byte(theme.Downsample(m.Overlay(background, width, height), theme.ColorProfile)))
}

// TestNoMatchesRendersTheEmptyRow asserts the empty state by construction
// rather than by golden, so a copy change fails here with a readable message.
func TestNoMatchesRendersTheEmptyRow(t *testing.T) {
	m := openModel(t)
	typeQuery(m, "zzzzzzq")
	view := ansi.Strip(m.View(84, 26))
	styles := theme.New(true)
	if !strings.Contains(view, styles.Glyph.Empty+" no matching actions") {
		t.Errorf("empty palette is missing its row:\n%s", view)
	}
	if !strings.Contains(view, "esc close") {
		t.Errorf("empty palette named no action to take:\n%s", view)
	}
}

// TestSectionBandsOnlyWithoutAQuery is the rendered half of the grouping rule.
func TestSectionBandsOnlyWithoutAQuery(t *testing.T) {
	m := openModel(t)
	if view := ansi.Strip(m.View(84, 40)); !strings.Contains(view, "NAVIGATE") {
		t.Errorf("unfiltered palette has no section band:\n%s", view)
	}
	typeQuery(m, "car")
	if view := ansi.Strip(m.View(84, 40)); strings.Contains(view, "NAVIGATE") {
		t.Errorf("a ranked list kept its section band:\n%s", view)
	}
}

// TestMatchHighlightIsRenderedThroughStyleRanges checks the cue reaches the
// frame, and that it costs no cells doing it.
func TestMatchHighlightIsRenderedThroughStyleRanges(t *testing.T) {
	m := openModel(t)
	typeQuery(m, "ship")
	styles := theme.New(true)
	panel := m.layout(84, 26)
	entry := m.entries[0]
	rendered := m.entryText(entry, true, panel.focus, theme.OverlaySurf)
	// The four matched runes are contiguous, so they must arrive as one styled
	// range rather than four abutting ones. That coalescing is the whole reason
	// the offsets go through widget.MatchRuns.
	hit := styles.OnBold(theme.Brand, theme.OverlaySurf).Render("ship")
	if !strings.Contains(rendered, hit) {
		t.Errorf("the matched run is not styled as one range:\n%q", rendered)
	}
	if got := ansi.StringWidth(rendered); got != panel.focus {
		t.Errorf("entry row is %d cells, want the focusable measure %d", got, panel.focus)
	}
	plain := m.entryText(entry, false, panel.focus, theme.OverlaySurf)
	if ansi.StringWidth(plain) != ansi.StringWidth(rendered) {
		t.Error("the focused and blurred entry rows are different widths")
	}
	if ansi.Strip(plain) != ansi.Strip(rendered) {
		t.Error("focus changed the text of an entry row")
	}
}

// TestEntryRowsAreStateInvariant is spec section 10.4.4 applied to the list: a
// row's width and its content column are the same focused and blurred.
func TestEntryRowsAreStateInvariant(t *testing.T) {
	m := openModel(t)
	for _, width := range []int{100, 84, 60, 40, 26} {
		panel := m.layout(width, 26)
		var blurred, focused string
		for _, state := range []bool{false, true} {
			row := m.renderListRow(panel, listRow{kind: rowEntry, entry: cursorAt(m, state)})
			if got := ansi.StringWidth(row); got != panel.width {
				t.Errorf("width %d focused=%v: row is %d cells", width, state, got)
			}
			if state {
				focused = ansi.Strip(row)
				continue
			}
			blurred = ansi.Strip(row)
		}
		if len(blurred) == 0 || len(focused) == 0 {
			t.Fatalf("width %d rendered nothing", width)
		}
	}
}

// cursorAt returns the cursor's entry when focused is wanted and a different one
// when it is not, so the caller renders one row in each state.
func cursorAt(m *Model, focused bool) int {
	if focused {
		return m.cursor
	}
	return min(m.cursor+1, len(m.entries)-1)
}

// TestEntryTextDropsTheHintOnANarrowRow is the width ladder of the list row: the
// key hint is the first thing to go, because the label is what was searched for.
func TestEntryTextDropsTheHintOnANarrowRow(t *testing.T) {
	m := openModel(t)
	entry := m.entries[0]
	wide := ansi.Strip(m.entryText(entry, false, 40, theme.OverlaySurf))
	if !strings.Contains(wide, entry.Action.Hint) {
		t.Errorf("a wide row dropped the hint: %q", wide)
	}
	narrow := ansi.Strip(m.entryText(entry, false, 6, theme.OverlaySurf))
	if strings.Contains(narrow, entry.Action.Hint) {
		t.Errorf("a narrow row kept the hint: %q", narrow)
	}
	if got := ansi.StringWidth(m.entryText(entry, false, 6, theme.OverlaySurf)); got != 6 {
		t.Errorf("a narrow row is %d cells, want 6", got)
	}
}

// TestTruncationDropsTheMatchOffsetsWithTheText is the bug this guards against:
// styling a run that truncation removed would put the cue on whatever text slid
// into its place.
func TestTruncationDropsTheMatchOffsetsWithTheText(t *testing.T) {
	entry := Entry{Matched: []int{0, 1, 12, 13}}
	if got := within(entry.Matched, "permanent"); len(got) != 2 {
		t.Errorf("kept %v against a nine-byte name", got)
	}
	if got := within([]int{-1, 0}, "abc"); len(got) != 1 {
		t.Errorf("kept %v, want the in-range offset only", got)
	}
	if got := within(nil, "abc"); len(got) != 0 {
		t.Errorf("kept %v from no offsets", got)
	}
}

// TestScrollHintTracksTheCursor is what a windowed list cannot say for itself.
func TestScrollHintTracksTheCursor(t *testing.T) {
	m := openModel(t)
	styles := theme.New(true)
	want := widget.ScrollHint(styles, 1, len(m.entries), theme.OverlayBand)
	if got := m.scrollHint(); got != want {
		t.Errorf("hint = %q, want %q", got, want)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if got := ansi.Strip(m.scrollHint()); !strings.HasPrefix(got, "2/") {
		t.Errorf("hint after one step = %q", got)
	}
	typeQuery(m, "zzzzzzq")
	if got := m.scrollHint(); got != "" {
		t.Errorf("an empty result reported position %q", got)
	}
}

// TestWindowFollowsTheCursor keeps the selected row on screen at both ends of a
// list longer than the panel.
func TestWindowFollowsTheCursor(t *testing.T) {
	list := make([]listRow, 20)
	for index := range list {
		list[index] = listRow{kind: rowEntry, entry: index}
	}
	for _, test := range []struct {
		name  string
		focus int
		size  int
		first int
		last  int
	}{
		{name: "top", focus: 0, size: 6, first: 0, last: 5},
		{name: "middle", focus: 10, size: 6, first: 7, last: 12},
		{name: "end", focus: 19, size: 6, first: 14, last: 19},
		{name: "fits whole", focus: 3, size: 40, first: 0, last: 19},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := window(list, test.focus, test.size)
			if got[0].entry != test.first || got[len(got)-1].entry != test.last {
				t.Errorf("window is %d..%d, want %d..%d",
					got[0].entry, got[len(got)-1].entry, test.first, test.last)
			}
			inside := false
			for _, row := range got {
				if row.entry == test.focus {
					inside = true
				}
			}
			if !inside {
				t.Error("the window does not hold the focused row")
			}
		})
	}
	if got := window(list, 0, 0); got != nil {
		t.Errorf("a zero-height window returned %v", got)
	}
	if got := window(nil, 0, 4); got != nil {
		t.Errorf("an empty list returned %v", got)
	}
}

// TestCursorRowSkipsSectionBands keeps the scroll window following the entry the
// cursor is on rather than a band it cannot select.
func TestCursorRowSkipsSectionBands(t *testing.T) {
	m := openModel(t)
	list := m.listRows()
	if list[0].kind != rowSection {
		t.Fatal("an unfiltered list does not open with a section band")
	}
	if got := m.cursorRow(list); got != 1 {
		t.Errorf("cursor row is %d, want 1, one past the opening band", got)
	}
	if got := m.cursorRow(nil); got != 0 {
		t.Errorf("an empty list put the cursor at row %d", got)
	}
}

// TestFooterStatesTheDismissals keeps the frozen dismissal reachable: the band
// says what closes the palette, in hints only, because a band re-arms its own
// style and a button in it would drop the band background.
func TestFooterStatesTheDismissals(t *testing.T) {
	m := openModel(t)
	view := ansi.Strip(m.View(84, 26))
	for _, want := range []string{"enter run", "esc close", "COMMAND PALETTE"} {
		if !strings.Contains(view, want) {
			t.Errorf("frame is missing %q:\n%s", want, view)
		}
	}
}

// TestCursorViewportWindowsALongQuery covers the text cursor's own scroll.
func TestCursorViewportWindowsALongQuery(t *testing.T) {
	if got := cursorViewport("abcdef", 6, 4); got != "def|" {
		t.Errorf("tail window = %q, want %q", got, "def|")
	}
	if got := cursorViewport("abcdef", 0, 4); got != "|abc" {
		t.Errorf("head window = %q, want %q", got, "|abc")
	}
	if got := cursorViewport("abc", 9, 1); got != "|" {
		t.Errorf("one column = %q", got)
	}
	if got := cursorViewport("abc", 1, 0); got != "" {
		t.Errorf("no columns = %q", got)
	}
	if got := cursorViewport("abc", -4, 6); got != "|abc" {
		t.Errorf("a negative position = %q", got)
	}
}

// TestFitTruncatesWithoutStyling keeps the plain form measurable and offsetable,
// which is what the highlight needs before it styles anything.
func TestFitTruncatesWithoutStyling(t *testing.T) {
	if got := fit("permanently delete", 6); got != "perman" {
		t.Errorf("fit = %q", got)
	}
	if got := fit("short", 40); got != "short" {
		t.Errorf("fit widened %q", got)
	}
	if got := fit("anything", 0); got != "" {
		t.Errorf("fit at zero = %q", got)
	}
	if got := fit("a\x07b", 8); got != "ab" {
		t.Errorf("fit left a control character: %q", got)
	}
}

// TestBodyRowsFitTheirPanel keeps every row exactly panel-wide at every size the
// panel is allowed to be, which is what lets the widget slice them.
func TestBodyRowsFitTheirPanel(t *testing.T) {
	m := openModel(t)
	for _, size := range [][2]int{{120, 40}, {84, 26}, {60, 18}, {40, 12}, {24, 8}} {
		panel := m.layout(size[0], size[1])
		rows := m.bodyRows(panel)
		if len(rows) > panel.rows {
			t.Errorf("%dx%d: %d body rows for a panel with %d", size[0], size[1], len(rows), panel.rows)
		}
		for index, row := range rows {
			if got := ansi.StringWidth(row); got != panel.width {
				t.Errorf("%dx%d: body row %d is %d cells, want %d",
					size[0], size[1], index, got, panel.width)
			}
		}
	}
}

// TestFitBlockSquaresUpAFrame covers both directions of the frame fit: a block
// taller or wider than the frame is cut, a shorter or narrower one is padded.
func TestFitBlockSquaresUpAFrame(t *testing.T) {
	tall := strings.Join([]string{"aaaa", "bbbb", "cccc", "dddd"}, "\n")
	if got := fitBlock(tall, 4, 2); got != "aaaa\nbbbb" {
		t.Errorf("a tall block was not cut: %q", got)
	}
	if got := fitBlock("aaaaaa", 3, 1); got != "aaa" {
		t.Errorf("a wide row was not cut: %q", got)
	}
	if got := fitBlock("ab", 4, 2); got != "ab  \n    " {
		t.Errorf("a short block was not padded: %q", got)
	}
}

// TestBodyRowsOnANoRowPanel keeps a panel with no body from indexing past its
// own geometry.
func TestBodyRowsOnANoRowPanel(t *testing.T) {
	m := openModel(t)
	if rows := m.bodyRows(frame{width: 20, height: 2, inner: 16, focus: 14, rows: 0}); rows != nil {
		t.Errorf("a bodyless panel produced %d rows", len(rows))
	}
	rows := m.bodyRows(frame{width: 20, height: 3, inner: 16, focus: 14, rows: 1})
	if len(rows) != 1 {
		t.Errorf("a one-row panel produced %d rows, want the query field alone", len(rows))
	}
}
