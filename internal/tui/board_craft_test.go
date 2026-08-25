package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/tui/pointer"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
	"github.com/RandomCodeSpace/kb/internal/tui/widget"
)

// boardHitFor is the first rendered hit region matching want, so a pointer test
// aims at a cell the board actually claimed rather than at a guessed offset.
func boardHitFor(t *testing.T, model Model, want func(boardHit) bool) boardHit {
	t.Helper()
	_, hits := model.renderBoard()
	for _, hit := range hits {
		if want(hit) {
			return hit
		}
	}
	t.Fatal("no hit region matched")
	return boardHit{}
}

// hoverBoard drives one bare motion over a cell and delivers whatever the board
// answered with, which is the hover step of spec section 10.5.1 end to end.
func hoverBoard(t *testing.T, model *Model, x, y int) {
	t.Helper()
	handler := requireMouseHandler(t, model.View().OnMouse, "board")
	command := handler(tea.MouseMotionMsg{X: x, Y: y, Button: tea.MouseNone})
	if command == nil {
		t.Fatalf("bare motion at %d,%d produced no hover", x, y)
	}
	updateTestModel(t, model, command())
}

// TestBoardCardHoverRaisesItsRailAndNothingElse is spec section 10.5.1: where an
// element's selected state already spends the tier step - the card, and only the
// card - hover raises the rail cell instead of the surface. Ratified call 9
// keeps the board cursor off the pointer, so the hover is an affordance cue and
// never the acting selection.
func TestBoardCardHoverRaisesItsRailAndNothingElse(t *testing.T) {
	m := mouseRoutingTestModel(t)
	before := m.boardView
	card := boardHitFor(t, m, func(hit boardHit) bool {
		return hit.kind == boardHitDefault && hit.taskID == "task-1"
	})
	hoverBoard(t, &m, card.x0+2, card.y0)

	if got := m.pointerState.Hovered(); got != boardCardControlID("task-1") {
		t.Fatalf("hovered control = %q", got)
	}
	if m.boardView != before {
		t.Fatal("hover moved the board cursor")
	}
	styles := m.themeStyles()
	row := strings.Split(m.render(), "\n")[card.y0]
	if !strings.Contains(row, widget.Rail(styles, 0, theme.Raised, false)) {
		t.Fatalf("hovered card did not raise its rail cell:\n%q", row)
	}
	// The rail keeps its priority hue and its resting glyph: hover is a ground
	// step, selection is the full block, and the two must stay distinguishable.
	if strings.Contains(row, widget.Rail(styles, 0, theme.Raised, true)) {
		t.Fatalf("hover thickened the rail; only selection does that:\n%q", row)
	}
	// The card's own surface is untouched: only the rail cell steps a tier, so
	// the row still carries its resting ground behind the rail. At this frame
	// the density is compact and card 1 is the Zebra stripe (spec section 2.6).
	if !strings.Contains(row, styles.On(theme.FgBase, theme.Zebra).Render(" ")) {
		t.Fatalf("hover raised the whole card, not its rail:\n%q", row)
	}
}

// TestBoardColumnBandHoverThickensItsRailGlyph is the band half of the same
// table: the band is already bold and cannot change background without becoming
// the focused band, so the rail glyph is the one slot it has spare. A focused
// band renders no hover at all.
func TestBoardColumnBandHoverThickensItsRailGlyph(t *testing.T) {
	m := mouseRoutingTestModel(t)
	// Below the wide-frame threshold the board shows the focused column alone
	// (spec section 2.5), and an unfocused band is what hover is about.
	m.width, m.height = 160, 20
	m.board.Tasks = append(m.board.Tasks, board.Task{ID: "doing", Title: "Doing", Status: board.StatusDoing})
	band := boardHitFor(t, m, func(hit boardHit) bool {
		return hit.kind == boardHitColumnHeading && hit.status == board.StatusDoing
	})
	hoverBoard(t, &m, band.x0+1, band.y0)
	if got := m.pointerState.Hovered(); got != boardColumnControlID(board.StatusDoing) {
		t.Fatalf("hovered control = %q", got)
	}
	// The band is rendered as one styled run over the whole row, so the cue is
	// asserted on the glyph it substituted rather than on a fragment's style.
	styles := m.themeStyles()
	if !strings.Contains(plain(m.render()), styles.Glyph.RailFull+styles.Glyph.Dot+" 2 ") {
		t.Fatalf("hovered unfocused band did not thicken its rail glyph:\n%s", plain(m.render()))
	}

	// The focused band is already the acting column; there is nothing for hover
	// to promise, so its caret is unchanged.
	focused := boardHitFor(t, m, func(hit boardHit) bool {
		return hit.kind == boardHitColumnHeading && hit.status == board.StatusTodo
	})
	hoverBoard(t, &m, focused.x0+1, focused.y0)
	if !strings.Contains(plain(m.render()), styles.Glyph.Focus+styles.Glyph.Dot+" 1 ") {
		t.Fatal("hover rewrote the focused band's caret")
	}
}

// TestBoardHoverClearsWhenThePointerLeavesEveryRegion is the other half of spec
// section 10.5.2: mouse mode is on for a surface exactly when hover resolves to
// one of its regions, so turning it off is clearing the one bit of state.
func TestBoardHoverClearsWhenThePointerLeavesEveryRegion(t *testing.T) {
	m := mouseRoutingTestModel(t)
	card := boardHitFor(t, m, func(hit boardHit) bool {
		return hit.kind == boardHitDefault && hit.taskID == "task-0"
	})
	hoverBoard(t, &m, card.x0+2, card.y0)
	if m.pointerState.Hovered() == "" {
		t.Fatal("motion over a card set no hover")
	}
	hoverBoard(t, &m, 0, 0)
	if got := m.pointerState.Hovered(); got != "" {
		t.Fatalf("motion off every region left %q hovered", got)
	}
}

// TestBoardCardLabelHoverBeatsTheCardUnderIt keeps the two hover contracts of
// ratified call 9 in the right order: a label pill carries its own control id
// and is registered after the row it sits on, so the topmost-wins scan resolves
// the pill rather than the card.
func TestBoardCardLabelHoverBeatsTheCardUnderIt(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	m := newTestRootModel(stubBoardReader{board: boardViewFixture(now)}, nil, "alice")
	m.loading, m.haveBoardSnapshot = false, true
	m.board = boardViewFixture(now)
	m.now = func() time.Time { return now }
	m.renderedAt = now
	m.width, m.height = 160, 40
	label := boardHitFor(t, m, func(hit boardHit) bool {
		return hit.kind == boardHitFilterLabel && hit.taskID == "todo-1"
	})
	hoverBoard(t, &m, label.x0, label.y0)
	if got := m.pointerState.Hovered(); got != boardCardLabelControlID("todo-1", label.tag) {
		t.Fatalf("hovered control = %q, want the label pill", got)
	}
	styles := m.themeStyles()
	plainRow := widget.Label(styles, label.tag, theme.Card, false, false)
	if strings.Contains(m.render(), plainRow) {
		t.Fatal("the hovered pill rendered its resting form")
	}
}

// TestFilterLabelPillsCarryTheirHitRegions is the half of issue #206 the pill
// form could have broken: the toolbar's labels are multi-run strings now, so
// every click region has to be measured from the rendered run rather than from a
// plain-text length. The region is asserted against the pill the widget renders,
// and the pointer is then hovered inside it.
func TestFilterLabelPillsCarryTheirHitRegions(t *testing.T) {
	m := newTestRootModel(stubBoardReader{}, nil, "u")
	m.loading, m.haveBoardSnapshot = false, true
	m.board = filterFixture()
	m.width, m.height = 160, 40
	styles := m.themeStyles()

	_, hits := m.renderFilterBar(m.width)
	for _, hit := range hits {
		if hit.kind != boardHitFilterLabel {
			continue
		}
		pill := widget.FilterLabel(styles, hit.tag, theme.Canvas, m.filter.hasTag(hit.tag), false, false)
		if got, want := hit.x1-hit.x0, ansi.StringWidth(pill); got != want {
			t.Errorf("hit region for %q is %d cells, the pill is %d", hit.tag, got, want)
		}
	}

	label := boardHitFor(t, m, func(hit boardHit) bool {
		return hit.kind == boardHitFilterLabel && hit.taskID == ""
	})
	hoverBoard(t, &m, label.x0, label.y0)
	if got := m.pointerState.Hovered(); got != pointer.ControlID("board-filter:label:"+label.tag) {
		t.Fatalf("hovered control = %q, want the filter pill for %q", got, label.tag)
	}
	if strings.Contains(m.render(), widget.FilterLabel(styles, label.tag, theme.Canvas, false, false, false)) {
		t.Fatal("the hovered filter pill rendered its resting form")
	}
	// Toggling the label lights the pill without moving the row behind it.
	before, _ := m.renderFilterBar(m.width)
	updateTestModel(t, &m, filterLabelClickedMsg{tag: label.tag})
	after, _ := m.renderFilterBar(m.width)
	if ansi.StringWidth(before) != ansi.StringWidth(after) {
		t.Errorf("toggling %q reflowed the toolbar: %d cells became %d",
			label.tag, ansi.StringWidth(before), ansi.StringWidth(after))
	}
	if !strings.Contains(plain(after), styles.Glyph.MarkFilterOn+styles.Glyph.MarkTag+label.tag) {
		t.Errorf("the selected pill kept the unselected mark: %q", plain(after))
	}
}

// TestBoardColumnEmptyStatesNameTheirAction is spec section 10.8.7's two board
// column rows. The tail comes from the action registry, so a board built without
// a card editor falls through to the import binding rather than naming a key it
// does not bind.
func TestBoardColumnEmptyStatesNameTheirAction(t *testing.T) {
	m := newTestRootModel(stubBoardReader{board: board.Board{Title: "Board"}}, nil, "u")
	m.loading, m.haveBoardSnapshot = false, true
	m.width, m.height = 160, 40

	if got := plain(m.render()); !strings.Contains(got, "○ no cards") {
		t.Fatalf("empty column row missing:\n%s", got)
	}
	headline, key, verb := m.columnEmptyRow()
	if headline != "no cards" || key != "" || verb != "" {
		t.Fatalf("board with no editor named %q %q %q", headline, key, verb)
	}

	m.filter.restore(boardFilter{Text: "nothing matches this"})
	if !m.filter.active() {
		t.Fatal("filter did not activate")
	}
	headline, key, verb = m.columnEmptyRow()
	if headline != "no matches" || key != "X" || verb != "clear filter" {
		t.Fatalf("filtered empty row = %q %q %q", headline, key, verb)
	}
	if got := plain(m.render()); !strings.Contains(got, "○ no matches  X clear filter") {
		t.Fatalf("filtered empty column row missing:\n%s", got)
	}
}

// TestBoardColumnLoadingBeatsEmpty is the fix for finding 1 of spec section
// 10.8.1: until the first snapshot lands every column said the board was empty
// while the footer two rows away said it was loading.
func TestBoardColumnLoadingBeatsEmpty(t *testing.T) {
	m := newTestRootModel(stubBoardReader{board: board.Board{Title: "Board"}}, nil, "u")
	m.width, m.height = 160, 40
	if !m.boardLoading() {
		t.Fatal("a fresh board is not loading")
	}
	view := plain(m.render())
	if !strings.Contains(view, "loading") || strings.Contains(view, "no cards") {
		t.Fatalf("loading column body:\n%s", view)
	}
	if !strings.Contains(view, "loading board") {
		t.Fatalf("loading footer:\n%s", view)
	}
	m.loading, m.haveBoardSnapshot = false, true
	if got := plain(m.render()); !strings.Contains(got, "○ no cards") {
		t.Fatalf("settled column body:\n%s", got)
	}
}

// TestBoardBusyChainIsSelfTerminating is the gate rule of spec section 10.2.3,
// normative for every tick chain in the TUI: the command is derived from the
// busy predicate on every tick, so a settled board does not re-arm and an idle
// board costs no timer.
func TestBoardBusyChainIsSelfTerminating(t *testing.T) {
	// The watcher-pending window is where the board renders its own loading
	// state. While m.loading is set with no snapshot the launch screen of spec
	// section 10.6.7 owns the frame, and rule 4 of section 10.8.4 gives that
	// surface's one motion to the reveal rather than to a second spinner.
	m := newTestRootModel(stubBoardReader{board: board.Board{Title: "Board"}}, stubVersionReader{}, "u")
	tick := spinner.TickMsg{ID: m.spin.ID()}
	if command := m.boardSpinTick(tick); command == nil {
		t.Fatal("a loading board did not continue its chain")
	}
	m.haveVersion, m.haveBoardSnapshot = true, true
	if command := m.boardSpinTick(tick); command != nil {
		t.Fatal("a settled board re-armed its chain")
	}
	if command := m.trackBoardBusy(false); command != nil {
		t.Fatal("a settled board opened a chain")
	}
	m.move.saving = true
	if command := m.trackBoardBusy(true); command != nil {
		t.Fatal("an already-busy board opened a second chain")
	}
	if command := m.trackBoardBusy(false); command == nil {
		t.Fatal("a newly busy board opened no chain")
	}
	// An overlay's own plain tier is routed to the overlay, not swallowed here.
	foreign := spinner.TickMsg{ID: m.spin.ID() + 1}
	if _, ok := interface{}(foreign).(spinner.TickMsg); !ok || foreign.ID == m.spin.ID() {
		t.Fatal("foreign tick was not distinguishable")
	}
}

// TestBoardBusyFrameIsFlatAndTickInvariantWhenSettled is item 7 of the
// determinism contract: the settled frame of an animated surface is
// tick-invariant, and the busy state's motion is absent rather than frozen.
func TestBoardBusyFrameIsFlatAndTickInvariantWhenSettled(t *testing.T) {
	m := newTestRootModel(stubBoardReader{board: board.Board{Title: "Board"}}, stubVersionReader{}, "u")
	m.width, m.height = 120, 20
	m.haveVersion, m.haveBoardSnapshot = true, true
	settled := m.render()
	for range 4 {
		updateTestModel(t, &m, spinner.TickMsg{ID: m.spin.ID()})
		if m.render() != settled {
			t.Fatal("a settled board moved under tick")
		}
	}
	if m.busyFrameText() != "" || m.busyFrame(m.themeStyles(), theme.Surface) != "" {
		t.Fatal("a settled board rendered a spinner frame")
	}
	// While busy the frame advances, and it does so without a wall-clock read.
	m.haveVersion = false
	first := m.busyFrameText()
	updateTestModel(t, &m, spinner.TickMsg{ID: m.spin.ID()})
	if second := m.busyFrameText(); second == "" || second == first {
		t.Fatalf("busy frame did not advance: %q then %q", first, second)
	}
}

// TestBoardBusyRowSurvivesAModelWithNoSpinner is the zero-value contract of the
// package: a Model assembled field by field carries no configured spinner, and
// the busy row is then its label alone rather than a frame slot with nothing in
// it. It is the same rule as rule 4 of spec section 10.8.4, reached from the
// other side.
func TestBoardBusyRowSurvivesAModelWithNoSpinner(t *testing.T) {
	zero := Model{watcher: stubVersionReader{}}
	if !zero.boardBusy() {
		t.Fatal("a loading zero-value model is not busy")
	}
	if frame := zero.busyFrameText(); frame != "" {
		t.Fatalf("an unconfigured spinner rendered %q", frame)
	}
	state, slot := zero.boardState()
	if state != "loading board" || slot != theme.FgSubtle {
		t.Fatalf("frameless busy state = %q, %v", state, slot)
	}
	if row := zero.columnPlaceholder(zero.themeStyles(), 40); plain(row) != "loading" {
		t.Fatalf("frameless busy row = %q", plain(row))
	}
}

// TestBoardColumnScrollbarReservesItsColumnAndDimsAtRest is spec section 10.3.4:
// kb dims, it does not hide. The column is reserved for the whole time the body
// overflows, the settled tint is FgMuted, and a scroll lights it to FgSubtle
// until its own expiry message settles it again.
func TestBoardColumnScrollbarReservesItsColumnAndDimsAtRest(t *testing.T) {
	m := mouseRoutingTestModel(t)
	styles := m.themeStyles()
	rest := styles.On(theme.FgMuted, theme.Surface).Render(styles.Glyph.RailFull)
	active := styles.On(theme.FgSubtle, theme.Surface).Render(styles.Glyph.RailFull)

	view := m.render()
	if !strings.Contains(view, rest) {
		t.Fatal("an overflowing column drew no settled scroll affordance")
	}
	if strings.Contains(view, active) {
		t.Fatal("a settled column drew the active affordance")
	}

	column := statusIndex(board.StatusTodo)
	command := m.armScrollLinger(column)
	if command == nil {
		t.Fatal("a scroll armed no linger")
	}
	if !strings.Contains(m.render(), active) {
		t.Fatal("a just-scrolled column did not light its affordance")
	}
	expiry, ok := command().(scrollSettledMsg)
	if !ok {
		t.Fatalf("linger scheduled %T", command())
	}
	// A stale expiry is dropped rather than settling a linger a later scroll
	// re-armed, which is the sequence guard the footer notice runs too.
	m.scroll.settle(scrollSettledMsg{column: column, seq: expiry.seq - 1})
	if !m.scroll.lingering(column) {
		t.Fatal("a stale expiry settled the linger")
	}
	updateTestModel(t, &m, expiry)
	if m.scroll.lingering(column) {
		t.Fatal("the linger did not settle")
	}
	if strings.Contains(m.render(), active) {
		t.Fatal("the settled column kept the active affordance")
	}
	// An out-of-range column is not a panic in a render path.
	if got := m.armScrollLinger(len(boardStatuses)); got != nil {
		t.Fatal("an out-of-range column armed a linger")
	}
	m.scroll.settle(scrollSettledMsg{column: -1})
}

// TestBoardColumnWithoutOverflowReservesNoColumn is the first row of section
// 10.3.4's table: a body that fits carries no affordance and no reserved column,
// so the cards keep the full panel measure.
func TestBoardColumnWithoutOverflowReservesNoColumn(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	m := newTestRootModel(stubBoardReader{board: boardViewFixture(now)}, nil, "alice")
	m.loading, m.haveBoardSnapshot = false, true
	m.board = boardViewFixture(now)
	m.now = func() time.Time { return now }
	m.renderedAt = now
	m.width, m.height = 160, 60
	styles := m.themeStyles()
	track := styles.On(theme.FgMuted, theme.Surface).Render(styles.Glyph.Track)
	if strings.Contains(m.render(), track) {
		t.Fatal("a column that fits reserved a scroll column")
	}
}

// TestBoardMetaDropIsUniformAcrossCards is the responsive half of ticket #186:
// the chip categories a column renders are a property of the column, so a
// narrow board loses the same information on every card rather than showing a
// due chip on one card and not on the card below it.
func TestBoardMetaDropIsUniformAcrossCards(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	tasks := []board.Task{
		{ID: "short", Seq: 1, Title: "A", Status: board.StatusTodo, Prio: 1, Due: "2026-08-18", Effort: "M",
			CreatedAt: now, MovedAt: now},
		{ID: "long", Seq: 2, Title: "B", Status: board.StatusTodo, Prio: 2, Due: "2026-07-01", Effort: "XL",
			Blocked: true, CreatedAt: now.Add(-400 * time.Hour), MovedAt: now.Add(-400 * time.Hour)},
	}
	m := newTestRootModel(stubBoardReader{board: board.Board{Title: "Narrow", Tasks: tasks}}, nil, "u")
	m.loading, m.haveBoardSnapshot = false, true
	m.board = board.Board{Title: "Narrow", Tasks: tasks}
	m.now = func() time.Time { return now }
	m.renderedAt = now
	styles := m.themeStyles()

	for width := 12; width <= 60; width++ {
		metas := [][]string{
			m.cardMeta(styles, tasks[0], theme.Card, theme.DensityNormal),
			m.cardMeta(styles, tasks[1], theme.Card, theme.DensityNormal),
		}
		inner := styles.Metrics.CardInner(width, theme.DensityNormal)
		depth := metaDepth(metas, inner)
		if depth < 1 || depth > cardMetaSlots {
			t.Fatalf("width %d: depth %d out of range", width, depth)
		}
		for index, meta := range metas {
			if got := metaRowWidth(meta[:depth]); depth > 1 && got > inner {
				t.Fatalf("width %d card %d: kept %d cells of %d", width, index, got, inner)
			}
		}
		if depth < cardMetaSlots && metaRowsFit(metas, depth+1, inner) {
			// The drop is reverse survival order and stops at the first depth
			// every card in the column can hold.
			t.Fatalf("width %d: dropped to %d when %d fit every card", width, depth, depth+1)
		}
	}
}

// TestBoardMetaDropTakesTheWidestCardInTheColumn is the uniformity itself: the
// column drops a category the widest card cannot hold even though a shorter
// card in the same column could.
func TestBoardMetaDropTakesTheWidestCardInTheColumn(t *testing.T) {
	narrow := []string{" 1 ", "new", "", ""}
	wide := []string{" 2 ", "400d", " !20d ", " M "}
	metas := [][]string{narrow, append([]string{}, wide...)}
	inner := metaRowWidth(wide) - 1
	depth := metaDepth(metas, inner)
	if depth == cardMetaSlots {
		t.Fatal("the column kept a category the widest card cannot hold")
	}
	if got := metaDepth([][]string{narrow}, inner); got != cardMetaSlots {
		t.Fatalf("a column of short cards dropped to depth %d", got)
	}
}

// TestBoardFooterErrorCarriesTheAlertMarkAndEllipsizes is spec section 10.8.5
// at board scope: the mark is what tells a terminal with no color that the row
// failed, the hue stays StatusDanger on the board tiers, and the message is
// ellipsized with the section 3.3 primitive rather than cut bare.
func TestBoardFooterErrorCarriesTheAlertMarkAndEllipsizes(t *testing.T) {
	m := newTestRootModel(stubBoardReader{board: board.Board{Title: "Board"}}, nil, "u")
	m.loading, m.haveBoardSnapshot = false, true
	m.width, m.height = 60, 12
	m.loadErr = fmt.Errorf("open %s: no such file or directory", strings.Repeat("kb/store/", 12))
	styles := m.themeStyles()
	state, slot := m.boardState()
	if slot != theme.StatusDanger || !strings.HasPrefix(state, styles.Glyph.Alert+" ") {
		t.Fatalf("board error state = %q, %v", state, slot)
	}
	footer := lastRenderLine(m)
	if !strings.Contains(footer, styles.Glyph.Ellipsis) {
		t.Fatalf("an overlong error was cut bare: %q", footer)
	}
	if ansi.StringWidth(footer) > m.width {
		t.Fatalf("error footer is %d cells wide", ansi.StringWidth(footer))
	}
}

// TestBoardMoveFailureIsDistinguishableFromASuccessfulDrop reads move.statusError
// for the first time (spec section 10.8.1): it was written in five places and
// read in none, so a failed write looked exactly like a successful one.
func TestBoardMoveFailureIsDistinguishableFromASuccessfulDrop(t *testing.T) {
	m := newTestRootModel(stubBoardReader{board: board.Board{Title: "Board"}}, nil, "u")
	m.loading, m.haveBoardSnapshot = false, true
	m.move.notice, m.move.status = true, "Dropped A, DOING, position 1 of 2"
	if _, slot := m.boardState(); slot != theme.StatusWarn {
		t.Fatalf("a successful drop notice = %v", slot)
	}
	m.move.status, m.move.statusError = "Move failed for A: disk full", true
	state, slot := m.boardState()
	if slot != theme.StatusDanger || !strings.HasPrefix(state, m.themeStyles().Glyph.Alert) {
		t.Fatalf("a failed move = %q, %v", state, slot)
	}
}

// TestBoardStackHeightMatchesTheRenderedStack is what lets the scroll affordance
// claim its column before the cards are rendered. Issue #243 turned the
// reservation into a measurement: the column measures the cards it is about to
// draw, at the width it is about to draw them at, and the sum must be the rows
// the render then emits.
func TestBoardStackHeightMatchesTheRenderedStack(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	fixture := boardViewFixture(now)
	m := newTestRootModel(stubBoardReader{board: fixture}, nil, "alice")
	m.loading, m.haveBoardSnapshot = false, true
	m.board = fixture
	m.now = func() time.Time { return now }
	m.renderedAt = now
	for _, height := range []int{20, 40, 60, 80} {
		m.height = height
		for _, density := range []theme.Density{theme.DensityNormal, theme.DensityCompact} {
			for _, width := range []int{18, 26, 40, 64} {
				for _, status := range []board.Status{board.StatusTodo, board.StatusDoing, board.StatusCancelled} {
					tasks := tasksInStatus(m.filteredBoard(), status)
					lines, owners, _ := m.renderTaskLines(tasks, status, width, density)
					heights := m.measureCards(tasks, status, width, density)
					if got := m.columnStackHeight(heights, density); got != len(lines) {
						t.Fatalf("height %d width %d density %v status %s: measured %d rows, rendered %d",
							height, width, density, status, got, len(lines))
					}
					// Per card, not only in total: two cards that were wrong by
					// the same amount in opposite directions would sum right and
					// still put a hit region on the wrong row.
					for index, drawn := range drawnCardRows(owners) {
						if index < len(heights) && heights[index] != drawn {
							t.Fatalf("height %d width %d density %v status %s: card %d measured %d rows, drew %d",
								height, width, density, status, index, heights[index], drawn)
						}
					}
				}
			}
		}
	}
}

// drawnCardRows counts the rows each card actually took, in column order, from
// the owner track the render returns.
func drawnCardRows(owners []string) []int {
	var rows []int
	previous := ""
	for _, owner := range owners {
		if owner == "" {
			previous = owner
			continue
		}
		if owner != previous {
			rows = append(rows, 0)
		}
		rows[len(rows)-1]++
		previous = owner
	}
	return rows
}

// TestBoardHoverIdsAreStableAndScoped keys the two hover identities apart: a
// card and its column band must never resolve to the same control, or a hover on
// one would light the other.
func TestBoardHoverIdsAreStableAndScoped(t *testing.T) {
	if boardCardControlID("x") == boardColumnControlID(board.StatusTodo) {
		t.Fatal("card and band control ids collide")
	}
	if boardCardHoverID(boardHit{kind: boardHitColumnHeading, taskID: "x"}) != "" {
		t.Fatal("a band hit claimed a card hover identity")
	}
	if boardCardHoverID(boardHit{kind: boardHitDefault}) != "" {
		t.Fatal("a column body hit claimed a card hover identity")
	}
	if got := boardCardHoverID(boardHit{kind: boardHitDefault, taskID: "x"}); got != boardCardControlID("x") {
		t.Fatalf("card hover identity = %q", got)
	}
	if hoverOnly(pointer.Point{}) != nil {
		t.Fatal("a hover-only region delivered a message")
	}
}

// TestBoardRowsAreExactlyFrameWidthAtEveryPinnedSize is the determinism
// obligation of spec section 10.2.2 applied to the card anatomy issue #232
// rewrote. Every row the board composes is exactly the frame width, at every
// pinned size and in both densities: a card whose title now wraps, whose
// description now renders markdown and whose labels now wrap onto a second row
// has three new ways to compose a row one cell wrong, and a golden only catches
// the sizes it happens to pin.
func TestBoardRowsAreExactlyFrameWidthAtEveryPinnedSize(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	fixture := boardViewFixture(now)
	// A description that exercises every branch of the frozen grammar at card
	// scale, and a title long enough to wrap and then still ellipsize.
	fixture.Tasks[0].Title = "Pointer capture leaks when the column scrolls under the drag ghost on resize"
	fixture.Tasks[0].Desc = "## Plan\n**bold** and *slant* and `mono`\n- a bullet item\n7. an ordinal\nhttps://example.test/x kb://task/9"
	fixture.Tasks[0].Tags = []string{"type::feature", "area::tui", "#backend", "regression", "needs-review"}
	fixture.Tasks[1].Due = "2026-08-01"

	sizes := []struct{ width, height int }{
		{120, 40}, // normal density, four columns
		{116, 40}, // the compaction width axis
		{100, 28}, // the compaction height axis
		{60, 50},  // single column, tall: three description rows
		{60, 80},  // single column, taller: the description ladder's cap
		{40, 20},  // single column, compact
		{24, 12},  // below the readable floor, chips dropping
	}
	for _, size := range sizes {
		m := newTestRootModel(stubBoardReader{board: fixture}, nil, "alice")
		m.loading = false
		m.board = fixture
		m.now = func() time.Time { return now }
		m.renderedAt = now
		m.boardView.showCancelled = true
		sized, _ := m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
		m = sized.(Model)
		for row, line := range strings.Split(m.render(), "\n") {
			if got := ansi.StringWidth(line); got != size.width {
				t.Errorf("%dx%d row %d is %d cells, want %d: %q",
					size.width, size.height, row, got, size.width, plain(line))
			}
		}
	}
}

// TestBoardCardNeverExceedsItsCeiling is what replaced the reservation invariant
// of issues #232 and #240. A card's height is its content's business now, but
// the section 2.6 ladder still bounds it: Metrics.CardRows is the tallest a card
// may be at a density and frame height, and a card that drew past it would be
// spending rows the ladder never budgeted at that terminal size.
func TestBoardCardNeverExceedsItsCeiling(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	fixture := boardViewFixture(now)
	fixture.Tasks[0].Title = "a title long enough to wrap onto the second allotted row and beyond it"
	fixture.Tasks[0].Desc = strings.Repeat("description ", 40)
	for _, size := range []struct{ width, height int }{{120, 40}, {60, 50}, {60, 80}, {40, 20}, {200, 90}} {
		m := newTestRootModel(stubBoardReader{board: fixture}, nil, "alice")
		m.loading = false
		m.board = fixture
		m.now = func() time.Time { return now }
		m.renderedAt = now
		sized, _ := m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
		m = sized.(Model)
		metrics := m.themeStyles().Metrics
		layout := boardColumnLayout(metrics, size.width, len(m.boardView.visibleStatuses()))
		density := metrics.DensityFor(size.height, layout.inner)
		ceiling := metrics.CardRows(max(m.height, 8), density)

		tasks := tasksInStatus(m.filteredBoard(), board.StatusTodo)
		width := max(layout.widths[0]-2*metrics.ColumnPad(density), 0)
		_, owners, _ := m.renderTaskLines(tasks, board.StatusTodo, width, density)
		for index, rows := range drawnCardRows(owners) {
			if rows > ceiling {
				t.Errorf("%dx%d: card %d drew %d rows, the ladder caps it at %d",
					size.width, size.height, index, rows, ceiling)
			}
			if rows < 1 {
				t.Errorf("%dx%d: card %d drew no rows at all", size.width, size.height, index)
			}
		}
	}
}

// TestBoardColumnPacksWholeCardsAndCountsTheRest is the column half of the same
// contract, and the one the "+N more" cue depends on. The body window is a fixed
// number of rows and the stack packs measured cards into it until the next one
// does not fit whole. Nothing the column draws may run past the panel, no card
// is drawn in part, and every card the window could not hold is in the cue -
// under content-sized cards a clipped card and a short card are the same rows,
// so a cue that undercounted would be hiding a card in plain sight.
func TestBoardColumnPacksWholeCardsAndCountsTheRest(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	fixture := boardViewFixture(now)
	fixture.Tasks[0].Desc = strings.Repeat("description ", 40)
	for _, size := range []struct{ width, height int }{{120, 40}, {60, 50}, {80, 32}, {200, 90}, {100, 24}} {
		for _, budget := range []int{6, 9, 14, 30} {
			m := newTestRootModel(stubBoardReader{board: fixture}, nil, "alice")
			m.loading, m.haveBoardSnapshot = false, true
			m.board = fixture
			m.now = func() time.Time { return now }
			m.renderedAt = now
			sized, _ := m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
			m = sized.(Model)
			metrics := m.themeStyles().Metrics
			statuses := m.boardView.visibleStatuses()
			layout := boardColumnLayout(metrics, size.width, len(statuses))
			density := metrics.DensityFor(max(m.height, 8), layout.inner)
			for _, status := range statuses {
				width := layout.widths[0]
				column := m.renderBoardColumnAt(status, width, budget, density)
				if got := len(column.lines); got != budget {
					t.Fatalf("%dx%d budget %d %s: the column drew %d rows, want %d",
						size.width, size.height, budget, status, got, budget)
				}
				for row, line := range column.lines {
					if got := ansi.StringWidth(line); got != width {
						t.Errorf("%dx%d budget %d %s: row %d is %d cells, want %d",
							size.width, size.height, budget, status, row, got, width)
					}
				}
				// Every card that reached the body reached it whole: its hit
				// region covers exactly the rows it measured.
				tasks := tasksInStatus(m.filteredBoard(), status)
				bodyWidth := max(width-2*metrics.ColumnPad(density), 0)
				drawn := map[string]int{}
				for _, hit := range column.hits {
					if hit.taskID != "" && hit.kind == boardHitDefault {
						drawn[hit.taskID] += hit.y1 - hit.y0
					}
				}
				if len(drawn) > len(tasks) {
					t.Fatalf("%dx%d budget %d %s: %d cards drawn, %d exist",
						size.width, size.height, budget, status, len(drawn), len(tasks))
				}
				heights := m.measureCards(tasks, status, bodyWidth, density)
				partial := 0
				for index, task := range tasks {
					rows, ok := drawn[task.ID]
					if !ok || rows == heights[index] {
						continue
					}
					partial++
					// The one card a column may clip is the first one in the
					// window, and only when the window is too short to hold it
					// whole - there is nothing to drop to there, and an empty
					// body under a "+N more" says less than a partial card does.
					if partial > 1 || index > 0 || rows >= heights[index] {
						t.Errorf("%dx%d budget %d %s: card %s drew %d rows of its %d",
							size.width, size.height, budget, status, task.ID, rows, heights[index])
					}
				}
			}
		}
	}
}
