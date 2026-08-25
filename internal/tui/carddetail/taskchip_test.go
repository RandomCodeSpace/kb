package carddetail

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// The three rows a blocker chip is drawn on, named by text that appears on that
// row and nowhere else, so a test can say which of two identical chips it means.
const (
	blocksRow    = "blocks "
	blockedByRow = "blocked by "
	gateRow      = "completion gate"
)

// chipLinks are the links every chip test opens on: one card this one blocks,
// and three blockers covering the mark vocabulary - open, done and cancelled.
func chipLinks() store.TaskLinks {
	return store.TaskLinks{
		Blocks: []board.Task{{ID: "b-1", Seq: 21, Status: board.StatusDoing}},
		BlockedBy: []board.Task{
			{ID: "d-1", Seq: 31, Status: board.StatusTodo},
			{ID: "d-2", Seq: 32, Status: board.StatusDone},
			{ID: "d-3", Seq: 33, Status: board.StatusCancelled},
		},
	}
}

// openChipModel is a loaded detail pane whose card carries blocker links, sized
// for the frame every detail pointer test renders at.
func openChipModel(t *testing.T, links store.TaskLinks) *Model {
	t.Helper()
	m := New(stubReader{links: links}, "alice", testStyles())
	m.Update(busyResult(t, m.Open(board.Task{
		ID: "task-1", Seq: 7, Title: "Linked", Status: board.StatusDoing,
	})))
	m.Resize(pointerWidth, pointerHeight)
	return &m
}

// chipPoint is the top-left cell of a chip on the row that carries marker, or
// (-1, -1) when it is not on screen. The same chip is drawn on the blocked-by
// row and inside the completion gate, so the row has to be named.
func chipPoint(content, marker, chip string) (int, int) {
	for row, line := range strings.Split(ansi.Strip(content), "\n") {
		if !strings.Contains(line, marker) {
			continue
		}
		if column := strings.Index(line, chip); column >= 0 {
			return ansi.StringWidth(line[:column]), row
		}
	}
	return -1, -1
}

// activateChip drives one press-release gesture on a rendered chip through the
// same immutable snapshot the frame that showed it produced, and returns the
// message the pane hands the root.
func activateChip(t *testing.T, m *Model, marker, chip string) tea.Msg {
	t.Helper()
	surface := m.PointerSurface("board", pointerWidth, pointerHeight)
	x, y := chipPoint(surface.Content, marker, chip)
	if x < 0 {
		t.Fatalf("chip %q on the %q row is not on screen:\n%s", chip, marker, ansi.Strip(surface.Content))
	}
	press := surface.Pointer(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	if press == nil {
		t.Fatalf("chip %q ignored the press", chip)
	}
	m.Update(busyResult(t, press))
	pressed := m.PointerSurface("board", pointerWidth, pointerHeight)
	if !containsReverseVideo(pressed.Content) {
		t.Fatalf("chip %q rendered no pressed feedback", chip)
	}
	release := pressed.Pointer(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	if release == nil {
		t.Fatalf("chip %q ignored the release", chip)
	}
	activate := m.Update(busyResult(t, release))
	if activate == nil {
		t.Fatalf("chip %q release produced no activation", chip)
	}
	message, recognized := m.ResolvePointerMessage(activate())
	if !recognized {
		t.Fatalf("chip %q activation escaped the session guard", chip)
	}
	return message
}

// TestBlockerChipsOpenTheCardTheyName is issue #222: every #<seq> chip the
// detail body draws - both link directions and the completion gate's reason
// clause - hands the root the same OpenTaskRefMsg an inline kb://task reference
// does, so a chip and a reference to the same card are one mechanism.
func TestBlockerChipsOpenTheCardTheyName(t *testing.T) {
	m := openChipModel(t, chipLinks())
	cases := []struct {
		marker, chip string
		want         int
	}{
		{blocksRow, "[#21 doing]", 21},
		{blockedByRow, "[#31 todo]", 31},
		{blockedByRow, "[✓ #32 done]", 32},
		{blockedByRow, "[☒ #33 cancelled]", 33},
		{gateRow, "[#31 todo]", 31},
	}
	for _, test := range cases {
		if got := activateChip(t, m, test.marker, test.chip); got != (OpenTaskRefMsg{Seq: test.want}) {
			t.Errorf("chip %q on the %q row activated %#v, want seq %d",
				test.chip, test.marker, got, test.want)
		}
	}
}

// TestBlockerChipMarksSayWhichBlockersAreResolved is the mark vocabulary of the
// scope addition to issue #222: a resolved blocker is distinguishable from one
// that still holds the card without reading the status word, and the ambiguous
// tick keeps the column after it per the adjacency rule of spec section 10.4.1.
func TestBlockerChipMarksSayWhichBlockersAreResolved(t *testing.T) {
	m := openChipModel(t, chipLinks())
	body := ansi.Strip(m.renderBody(pointerWidth))
	for _, want := range []string{"[#31 todo]", "[✓ #32 done]", "[☒ #33 cancelled]"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing chip %q:\n%s", want, body)
		}
	}
	// The gate names only the blockers that still hold the card, so it never
	// draws a mark; the marks are the exception the eye is looking for.
	gate := ""
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, gateRow) {
			gate = line
		}
	}
	if !strings.Contains(gate, "1 open linked blocker [#31 todo]") {
		t.Fatalf("completion gate = %q", gate)
	}
	if strings.ContainsAny(gate, m.styles.Glyph.Tick+m.styles.Glyph.CheckOff) {
		t.Fatalf("the completion gate drew a resolved-blocker mark: %q", gate)
	}
	// Spec section 10.4.1 adjacency: an East Asian Ambiguous mark owns the
	// column after it. The board walk of TestAmbiguousGlyphsAreNeverAbutted
	// does not reach this overlay, so the rule is held here too.
	marks := m.styles.Glyph.Tick + m.styles.Glyph.CheckOff
	for row, line := range strings.Split(ansi.Strip(m.View(pointerWidth, pointerHeight)), "\n") {
		cells := []rune(line)
		for column := 0; column < len(cells)-1; column++ {
			if strings.ContainsRune(marks, cells[column]) && cells[column+1] != ' ' {
				t.Errorf("row %d column %d: %q is abutted by %q\n%s",
					row, column, string(cells[column]), string(cells[column+1]), line)
			}
		}
	}
}

// TestBlockerChipHoverRaisesTheRunAndMovesNothing is spec section 10.5.1 for the
// element issue #222 adds: the chip is bracketed text on the panel surface
// rather than a section 3.6 pill, so the tier step is available and hover takes
// it, scoped to the chip's own cells. Section 10.4.4 holds - the hovered and
// resting frames are the same cells, the same widths and the same structure.
func TestBlockerChipHoverRaisesTheRunAndMovesNothing(t *testing.T) {
	m := openChipModel(t, chipLinks())
	resting := m.PointerSurface("board", pointerWidth, pointerHeight)
	restingHits := append([]taskChipHit(nil), m.chipHits...)
	if len(restingHits) != 5 {
		t.Fatalf("recorded %d chip regions, want 5: %#v", len(restingHits), restingHits)
	}

	x, y := chipPoint(resting.Content, blockedByRow, "[#31 todo]")
	if x < 0 {
		t.Fatalf("chip is not on screen:\n%s", ansi.Strip(resting.Content))
	}
	motion := resting.Pointer(tea.MouseMotionMsg{X: x, Y: y})
	if motion == nil {
		t.Fatal("motion onto the chip produced no hover")
	}
	m.Update(busyResult(t, motion))
	if !m.pointerState.IsHovered(taskChipControlID(chipSectionBlockedBy, 0)) {
		t.Fatalf("hover resolved to %q, want the blocked-by chip", m.pointerState.Hovered())
	}

	hovered := m.PointerSurface("board", pointerWidth, pointerHeight)
	raised := m.styles.On(theme.FgBase, theme.OverlayBand).Render("[#31 todo]")
	lit := strings.Join(m.bodyLines, "\n")
	if !strings.Contains(lit, raised) {
		t.Fatalf("hovered chip did not raise its run a depth tier:\n%s", lit)
	}
	if hovered.Content == resting.Content {
		t.Fatal("the hovered frame is the resting frame")
	}

	restingRows := strings.Split(resting.Content, "\n")
	hoveredRows := strings.Split(hovered.Content, "\n")
	if len(restingRows) != len(hoveredRows) {
		t.Fatalf("hover changed the frame height: %d then %d", len(restingRows), len(hoveredRows))
	}
	for index := range restingRows {
		if ansi.StringWidth(restingRows[index]) != ansi.StringWidth(hoveredRows[index]) {
			t.Fatalf("hover changed row %d width: %d then %d", index,
				ansi.StringWidth(restingRows[index]), ansi.StringWidth(hoveredRows[index]))
		}
	}
	if ansi.Strip(theme.Downsample(resting.Content, theme.StructureProfile)) !=
		ansi.Strip(theme.Downsample(hovered.Content, theme.StructureProfile)) {
		t.Fatal("hover changed more than color and attributes")
	}
	// Spec section 10.5.3: the recorded bounds are identical between the two
	// renders of the same content, so the region a hovered chip occupies is the
	// region it was lit on.
	for index, hit := range m.chipHits {
		if hit != restingHits[index] {
			t.Fatalf("hover moved region %d: %#v then %#v", index, restingHits[index], hit)
		}
	}
}

// TestBlockerChipRegionsTrackTheScrollOffset is row 6 of spec section 10.5.2 for
// a recorded-bounds region: the chip is clickable where it is drawn after a
// scroll, and registers nothing once it leaves the window.
func TestBlockerChipRegionsTrackTheScrollOffset(t *testing.T) {
	m := openChipModel(t, chipLinks())
	m.Refresh(board.Task{
		ID: "task-1", Seq: 7, Title: "Linked", Status: board.StatusDoing,
		Desc: strings.Repeat("filler line\n\n", 40),
	})
	before := m.PointerSurface("board", pointerWidth, pointerHeight)
	if x, y := chipPoint(before.Content, blockedByRow, "[#31 todo]"); x >= 0 {
		t.Fatalf("the chip is on screen at %d,%d before scrolling to it", x, y)
	}

	m.body.GotoBottom()
	m.rebuildBody()
	after := m.PointerSurface("board", pointerWidth, pointerHeight)
	x, y := chipPoint(after.Content, blockedByRow, "[#31 todo]")
	if x < 0 {
		t.Fatalf("the chip is not on screen after scrolling to it:\n%s", ansi.Strip(after.Content))
	}
	if got := activateChip(t, m, blockedByRow, "[#31 todo]"); got != (OpenTaskRefMsg{Seq: 31}) {
		t.Fatalf("scrolled chip activated %#v, want seq 31", got)
	}

	m.body.GotoTop()
	m.rebuildBody()
	offscreen := m.PointerSurface("board", pointerWidth, pointerHeight)
	if command := offscreen.Pointer(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}); command != nil {
		next, _, _ := m.pointerState.Update(busyResult(t, command))
		if next.Active() {
			t.Fatal("a scrolled-away chip kept a hit region")
		}
	}
}

// TestBlockerChipsWithoutASequenceNumberAreNotActivatable keeps the mechanism
// honest about what it can open: the root resolves a sequence number against the
// board, so a legacy card that carries none is named by its id and offered as
// text rather than as a control that would open nothing.
func TestBlockerChipsWithoutASequenceNumberAreNotActivatable(t *testing.T) {
	m := openChipModel(t, store.TaskLinks{
		BlockedBy: []board.Task{{ID: "legacy", Status: board.StatusTodo}},
	})
	if len(m.chipHits) != 0 {
		t.Fatalf("a chip with no sequence number recorded a region: %#v", m.chipHits)
	}
	surface := m.PointerSurface("board", pointerWidth, pointerHeight)
	x, y := chipPoint(surface.Content, blockedByRow, "[legacy todo]")
	if x < 0 {
		t.Fatalf("the chip is not on screen:\n%s", ansi.Strip(surface.Content))
	}
	if command := surface.Pointer(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}); command != nil {
		next, _, _ := m.pointerState.Update(busyResult(t, command))
		if next.Active() {
			t.Fatal("a chip with no sequence number pressed a control")
		}
	}
}

// TestBlockerChipRegionsStopAtTheRowEdge is the truncation half of the recorded
// bounds: a chip the row clipped away is not a region over whatever replaced it.
func TestBlockerChipRegionsStopAtTheRowEdge(t *testing.T) {
	spans := []taskChipSpan{
		{id: "a", offset: 0, width: 10, seq: 1},
		{id: "b", offset: 12, width: 10, seq: 2},
	}
	hits := appendChipHits(nil, spans, 3, 20, 34)
	if len(hits) != 1 || hits[0].seq != 1 || hits[0].row != 3 || hits[0].column != 20 {
		t.Fatalf("clipped spans resolved to %#v", hits)
	}
	if got := appendChipHits(nil, spans, 3, 20, 100); len(got) != 2 {
		t.Fatalf("unclipped spans resolved to %#v", got)
	}
	// A run wider than its field loses one more cell to the ellipsis tail.
	for _, test := range []struct {
		run         string
		cells, want int
	}{
		{"1234567890", 20, 10},
		{"1234567890", 6, 5},
		{"1234567890", 1, 0},
		{"1234567890", 0, 0},
	} {
		if got := visibleCells(test.run, test.cells); got != test.want {
			t.Errorf("visibleCells(%q, %d) = %d, want %d", test.run, test.cells, got, test.want)
		}
	}
	// A narrow pane truncates the row for real, and the chip beyond the edge
	// stops registering rather than moving.
	m := openChipModel(t, chipLinks())
	m.Resize(34, pointerHeight)
	m.rebuildBody()
	for _, hit := range m.chipHits {
		if hit.column+hit.width > 34 {
			t.Fatalf("a clipped chip kept a region past the panel edge: %#v", hit)
		}
	}
}

// TestCompletionGateKeepsItsUnknownClauseBehindTheChips is the one gate outcome
// that carries text on both sides of the chip run: the blockers are known and
// hold the card, and the load that would have named more of them is still out.
func TestCompletionGateKeepsItsUnknownClauseBehindTheChips(t *testing.T) {
	links := store.TaskLinks{BlockedBy: []board.Task{{ID: "d-1", Seq: 31, Status: board.StatusTodo}}}
	head, chips, tail, state := completionGate(board.Task{}, links, true, nil)
	if state != theme.StatusDanger || len(chips) != 1 {
		t.Fatalf("loading blocked gate = state %v chips %#v", state, chips)
	}
	if head != "completion gate  blocked: 1 open linked blocker " || tail != "; linked blockers loading" {
		t.Fatalf("loading blocked gate = head %q tail %q", head, tail)
	}
	if _, _, tail, _ := completionGate(board.Task{}, links, false, errors.New("broken")); tail != "; linked blockers unavailable" {
		t.Fatalf("failed blocked gate tail = %q", tail)
	}
	plural, chips, _, _ := completionGate(board.Task{}, chipLinks(), false, nil)
	if len(chips) != 1 {
		t.Fatalf("chipLinks holds %d open blockers, want 1", len(chips))
	}
	if plural != "completion gate  blocked: 1 open linked blocker " {
		t.Fatalf("singular gate head = %q", plural)
	}
	many := store.TaskLinks{BlockedBy: append(chipLinks().BlockedBy,
		board.Task{ID: "d-4", Seq: 34, Status: board.StatusDoing})}
	if head, _, _, _ := completionGate(board.Task{}, many, false, nil); head != "completion gate  blocked: 2 open linked blockers " {
		t.Fatalf("plural gate head = %q", head)
	}
	m := openChipModel(t, links)
	m.loading = true
	m.rebuildBody()
	body := ansi.Strip(m.renderBody(pointerWidth))
	if !strings.Contains(body, "1 open linked blocker [#31 todo]; linked blockers loading") {
		t.Fatalf("gate row lost a clause around its chips:\n%s", body)
	}
	if len(m.chipHits) == 0 {
		t.Fatal("the gate chip lost its region to the trailing clause")
	}
}
