package carddetail

import (
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
	"github.com/RandomCodeSpace/kb/internal/tui/mdparity"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// openRefModel is a loaded detail pane whose card carries desc, sized for the
// pointer frame every detail pointer test renders at.
func openRefModel(t *testing.T, desc string) *Model {
	t.Helper()
	m := New(stubReader{comments: []store.Comment{{ID: 3, Author: "alice", Body: "also kb://task/9"}}},
		"alice", testStyles())
	m.Update(busyResult(t, m.Open(board.Task{
		ID: "task-1", Seq: 7, Title: "Ref", Desc: desc, Status: board.StatusTodo,
	})))
	m.Resize(pointerWidth, pointerHeight)
	return &m
}

// referencePoint is the top-left cell of a rendered reference, or (-1, -1) when
// the reference is not on screen.
func referencePoint(content, reference string) (int, int) {
	for row, line := range strings.Split(ansi.Strip(content), "\n") {
		if column := strings.Index(line, reference); column >= 0 {
			return ansi.StringWidth(line[:column]), row
		}
	}
	return -1, -1
}

// activateReference drives one press-release gesture on a rendered reference
// through the same immutable snapshot the frame that showed it produced, and
// returns the message the pane hands the root.
func activateReference(t *testing.T, m *Model, reference string) tea.Msg {
	t.Helper()
	surface := m.PointerSurface("board", pointerWidth, pointerHeight)
	x, y := referencePoint(surface.Content, reference)
	if x < 0 {
		t.Fatalf("reference %q is not on screen:\n%s", reference, ansi.Strip(surface.Content))
	}
	press := surface.Pointer(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	if press == nil {
		t.Fatalf("reference %q ignored the press", reference)
	}
	m.Update(busyResult(t, press))
	pressed := m.PointerSurface("board", pointerWidth, pointerHeight)
	if !containsReverseVideo(pressed.Content) {
		t.Fatalf("reference %q rendered no pressed feedback", reference)
	}
	release := pressed.Pointer(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	if release == nil {
		t.Fatalf("reference %q ignored the release", reference)
	}
	activate := m.Update(busyResult(t, release))
	if activate == nil {
		t.Fatalf("reference %q release produced no activation", reference)
	}
	message, recognized := m.ResolvePointerMessage(activate())
	if !recognized {
		t.Fatalf("reference %q activation escaped the session guard", reference)
	}
	return message
}

// TestTaskReferenceRendersAsALinkAndActivatesItsCard is issue #212: the
// reference reaches glamour as an autolink, survives it literally, and the
// pointer map anchored on the rendered run hands the root the card to open.
func TestTaskReferenceRendersAsALinkAndActivatesItsCard(t *testing.T) {
	m := openRefModel(t, "blocked on kb://task/111 until friday")

	body := ansi.Strip(m.renderBody(pointerWidth))
	for _, want := range []string{"blocked on kb://task/111 until friday", "also kb://task/9"} {
		if !strings.Contains(body, want) {
			t.Fatalf("rendered body damaged the reference, want %q:\n%s", want, body)
		}
	}
	link := m.styles.Markdown.Link.Color
	if link == nil || !strings.Contains(m.renderBody(pointerWidth), "kb://task/111") {
		t.Fatalf("reference lost its rendered form: link color=%v", link)
	}

	if got := activateReference(t, m, "kb://task/111"); got != (OpenTaskRefMsg{Seq: 111}) {
		t.Fatalf("description reference activated %#v, want seq 111", got)
	}
	if got := activateReference(t, m, "kb://task/9"); got != (OpenTaskRefMsg{Seq: 9}) {
		t.Fatalf("comment reference activated %#v, want seq 9", got)
	}
}

// TestTaskReferenceHoverRaisesTheRunAndMovesNothing is spec section 10.5.1 for
// the one element the reference adds to its table: the raise spans the run, and
// section 10.4.4 holds - the hovered and resting frames are the same cells, the
// same widths, and the same structure once color is stripped.
func TestTaskReferenceHoverRaisesTheRunAndMovesNothing(t *testing.T) {
	m := openRefModel(t, "see kb://task/111 for the rest")
	resting := m.PointerSurface("board", pointerWidth, pointerHeight)
	restingHits := taskRefHits(m.bodyLines)
	restingRow := m.bodyLines[restingHits[0].row]

	x, y := referencePoint(resting.Content, "kb://task/111")
	if x < 0 {
		t.Fatalf("reference is not on screen:\n%s", ansi.Strip(resting.Content))
	}
	motion := resting.Pointer(tea.MouseMotionMsg{X: x, Y: y})
	if motion == nil {
		t.Fatal("motion onto the reference produced no hover")
	}
	m.Update(busyResult(t, motion))
	if !m.pointerState.IsHovered(detailTaskRefControlID(restingHits[0])) {
		t.Fatalf("hover resolved to %q, want the reference", m.pointerState.Hovered())
	}

	hovered := m.PointerSurface("board", pointerWidth, pointerHeight)
	raised := m.styles.HoverRun(theme.OverlaySurf, theme.OverlayBand, "kb://task/111")
	if !strings.Contains(m.bodyLines[restingHits[0].row], raised) {
		t.Fatalf("hovered reference did not raise its run a depth tier: %q", m.bodyLines[restingHits[0].row])
	}
	if strings.Contains(restingRow, raised) {
		t.Fatal("a resting reference rendered the hovered ground")
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
	// Spec section 10.5.3: the region set is byte-for-byte identical between the
	// two renders of the same content. The byte span the feedback was
	// substituted into is not part of it - it is where the attribute went, not
	// where the region is.
	hits := taskRefHits(m.bodyLines)
	if len(hits) != len(restingHits) {
		t.Fatalf("hover changed the region count: %d then %d", len(restingHits), len(hits))
	}
	for index, hit := range hits {
		was := restingHits[index]
		if hit.row != was.row || hit.column != was.column || hit.width != was.width || hit.seq != was.seq {
			t.Fatalf("hover moved region %d: %#v then %#v", index, was, hit)
		}
	}
}

// TestTaskReferenceRegionsTrackTheScrollOffset is row 6 of spec section 10.5.2
// for a region that lives in scrolled content: the reference is clickable where
// it is drawn after a scroll, and registers nothing once it leaves the window.
func TestTaskReferenceRegionsTrackTheScrollOffset(t *testing.T) {
	m := openRefModel(t, "top kb://task/111\n\n"+strings.Repeat("filler line\n\n", 40))
	before := m.PointerSurface("board", pointerWidth, pointerHeight)
	x, y := referencePoint(before.Content, "kb://task/111")
	if x < 0 {
		t.Fatalf("reference is not on screen:\n%s", ansi.Strip(before.Content))
	}

	m.scrollBy(2)
	after := m.PointerSurface("board", pointerWidth, pointerHeight)
	scrolledX, scrolledY := referencePoint(after.Content, "kb://task/111")
	if scrolledY != y-2 || scrolledX != x {
		t.Fatalf("scrolled reference is at %d,%d, want %d,%d", scrolledX, scrolledY, x, y-2)
	}
	if command := after.Pointer(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}); command != nil {
		next, _, _ := m.pointerState.Update(busyResult(t, command))
		if next.Active() {
			t.Fatal("the vacated cell pressed the scrolled reference")
		}
	}
	if got := activateReference(t, m, "kb://task/111"); got != (OpenTaskRefMsg{Seq: 111}) {
		t.Fatalf("scrolled reference activated %#v, want seq 111", got)
	}

	m.body.GotoBottom()
	m.rebuildBody()
	offscreen := m.PointerSurface("board", pointerWidth, pointerHeight)
	if column, row := referencePoint(offscreen.Content, "kb://task/111"); column >= 0 {
		t.Fatalf("reference is still visible at %d,%d after scrolling past it", column, row)
	}
	if command := offscreen.Pointer(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}); command != nil {
		next, _, _ := m.pointerState.Update(busyResult(t, command))
		if next.Active() {
			t.Fatal("a scrolled-away reference kept a hit region")
		}
	}
}

// TestUnknownTaskReferenceIsANotice is the failure mode of issue #212: card
// text names a card that does not exist, and the pane says so in one line.
func TestUnknownTaskReferenceIsANotice(t *testing.T) {
	m := openRefModel(t, "see kb://task/4242")
	if got := activateReference(t, m, "kb://task/4242"); got != (OpenTaskRefMsg{Seq: 4242}) {
		t.Fatalf("unknown reference activated %#v, want seq 4242", got)
	}
	m.NoticeUnknownTaskRef(4242)
	if body := ansi.Strip(m.renderBody(pointerWidth)); !strings.Contains(body, "no card #4242 on this board") {
		t.Fatalf("unknown reference notice missing:\n%s", body)
	}
	if m.statusIsError {
		t.Fatal("an unresolvable reference was reported as a failure")
	}

	closed := New(nil, "alice", testStyles())
	closed.NoticeUnknownTaskRef(4242)
	if closed.statusMessage != "" {
		t.Fatalf("a closed pane took a notice: %q", closed.statusMessage)
	}
}

// TestTaskReferenceScanIgnoresWhatIsNotAReference keeps the scan honest about
// the two things that look like a reference and are not: the copy glamour
// writes into the OSC 8 hyperlink parameters, which occupies no cells, and a
// sequence number too large for a card.
func TestTaskReferenceScanIgnoresWhatIsNotAReference(t *testing.T) {
	m := openRefModel(t, "one kb://task/111 two kb://task/222")
	hits := taskRefHits(m.bodyLines)
	if len(hits) != 3 {
		t.Fatalf("scan found %d references, want 3 (two in the description, one in the comment): %#v", len(hits), hits)
	}
	// The rows are ASCII, so a cell column is a byte index into the stripped
	// row: the anchored span has to be the reference itself and nothing else.
	for _, hit := range hits {
		line := ansi.Strip(m.bodyLines[hit.row])
		want := taskRefScheme + strconv.Itoa(hit.seq)
		if hit.column+hit.width > len(line) || line[hit.column:hit.column+hit.width] != want {
			t.Fatalf("reference #%d anchored at column %d width %d, which is not %q:\n%s",
				hit.seq, hit.column, hit.width, want, line)
		}
	}

	overflow := "kb://task/99999999999999999999"
	if hits := taskRefHits([]string{overflow}); len(hits) != 0 {
		t.Fatalf("an unparseable sequence number was anchored: %#v", hits)
	}
	if !strings.Contains(mdparity.Parity(overflow), "<"+overflow+">") {
		t.Fatalf("an unparseable sequence number lost its link form: %q", mdparity.Parity(overflow))
	}
	if hits := taskRefHits([]string{"plain row"}); len(hits) != 0 {
		t.Fatalf("a row with no reference was scanned: %#v", hits)
	}
}
