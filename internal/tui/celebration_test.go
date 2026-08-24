package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// celebrationModel is a loaded board wide enough to draw every column's meta
// row, with the DONE column already carrying a card so the row it celebrates
// exists before the ship as well as after it.
func celebrationModel(t *testing.T) Model {
	t.Helper()
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	fixture := boardViewFixture(now)
	m := newTestRootModel(stubBoardReader{board: fixture}, nil, "alice")
	m.loading = false
	m.haveBoardSnapshot = true
	m.board = fixture
	m.now = func() time.Time { return now }
	m.renderedAt = now
	m.width, m.height = 120, 40
	return m
}

// doneMetaRow is the DONE column's meta row as rendered, hue and all. The row
// is found by its text rather than by a guessed offset, so a layout change
// fails the assertion it belongs to instead of this helper.
func doneMetaRow(t *testing.T, m Model) string {
	t.Helper()
	rendered := m.renderBoardColumn(board.StatusDone, 30, 12)
	for _, line := range rendered.lines {
		if strings.Contains(plain(line), "card") {
			return line
		}
	}
	t.Fatal("the DONE column drew no meta row")
	return ""
}

// TestShipCelebrationArmsOnlyOnTheShipTransition is the gate: the effect
// belongs to a card landing in DONE and to nothing else on the board.
func TestShipCelebrationArmsOnlyOnTheShipTransition(t *testing.T) {
	s := &moveTestStore{board: moveFixture()}
	m := loadedMoveModel(s)
	m.savePreferences = func(tuiPreferences) error { return nil }
	if m.celebrate.active {
		t.Fatal("a fresh board arrived mid-celebration")
	}

	canonical := moveFixture()
	for i := range canonical.Tasks {
		if canonical.Tasks[i].ID == "a" {
			canonical.Tasks[i].Status = board.StatusDoing
		}
	}
	updateTestModel(t, &m, cardMoveStoredMsg{
		taskID: "a", title: "A",
		from: board.StatusTodo, to: board.StatusDoing, board: canonical,
	})
	if m.celebrate.active {
		t.Fatal("a move that was not a ship armed the celebration")
	}

	for i := range canonical.Tasks {
		if canonical.Tasks[i].ID == "a" {
			canonical.Tasks[i].Status = board.StatusDone
		}
	}
	command := updateTestModel(t, &m, cardMoveStoredMsg{
		taskID: "a", title: "A",
		from: board.StatusDoing, to: board.StatusDone, board: canonical,
	})
	if !m.celebrate.active || m.celebrate.status != board.StatusDone {
		t.Fatalf("the ship transition did not arm the celebration: %#v", m.celebrate)
	}
	if command == nil {
		t.Fatal("the ship transition armed no tick chain")
	}
	if !m.celebrateLit(board.StatusDone) {
		t.Fatal("the celebration's first frame was not lit")
	}
	if m.celebrateLit(board.StatusTodo) {
		t.Fatal("a column that took no card lit up")
	}
}

// TestShipCelebrationStepsAndTerminates walks every frame synchronously, which
// is the only way this repo asserts an intermediate frame (determinism
// contract, point 4). It pins the two-pulse shape and the self-termination of
// point 3 in one pass.
func TestShipCelebrationStepsAndTerminates(t *testing.T) {
	m := celebrationModel(t)
	timing := m.themeStyles().Timing
	beat := timing.CelebrateBeat()
	if beat <= 0 || timing.CelebrateSteps <= beat {
		t.Fatalf("celebration span %d and beat %d cannot pulse", timing.CelebrateSteps, beat)
	}
	if m.celebrateShip() == nil {
		t.Fatal("celebrateShip armed nothing on a settled board")
	}

	phases := make([]bool, 0, timing.CelebrateSteps)
	for step := 0; step < timing.CelebrateSteps; step++ {
		phases = append(phases, m.celebrateLit(board.StatusDone))
		command, handled := m.stepCelebration(shipCelebrationMsg{gen: m.celebrateGen})
		if !handled {
			t.Fatalf("step %d was not consumed", step)
		}
		if step == timing.CelebrateSteps-1 {
			if command != nil {
				t.Fatal("the final step re-armed the chain")
			}
			break
		}
		if command == nil {
			t.Fatalf("step %d ended the chain early", step)
		}
	}
	if m.celebrate.active {
		t.Fatalf("the celebration did not settle: %#v", m.celebrate)
	}
	if m.celebrateLit(board.StatusDone) {
		t.Fatal("a settled celebration still lit its row")
	}

	// Beats alternate, and the first one is lit: the flourish opens bright and
	// ends dark rather than arriving as a single repaint.
	for step, lit := range phases {
		if want := (step/beat)%2 == 0; lit != want {
			t.Fatalf("step %d lit = %v, want %v (phases %v)", step, lit, want, phases)
		}
	}
	if !phases[0] || phases[len(phases)-1] {
		t.Fatalf("celebration phases = %v", phases)
	}
}

// TestShipCelebrationDropsStaleAndForeignSteps is the generation guard. A
// second ship mid-flourish restarts the effect and the first chain's next
// message has to die rather than settle the second one.
func TestShipCelebrationDropsStaleAndForeignSteps(t *testing.T) {
	m := celebrationModel(t)
	if m.celebrateShip() == nil {
		t.Fatal("the first ship armed nothing")
	}
	first := m.celebrateGen
	m.stepCelebration(shipCelebrationMsg{gen: first})
	if m.celebrate.step != 1 {
		t.Fatalf("live step = %d, want 1", m.celebrate.step)
	}

	if m.celebrateShip() == nil {
		t.Fatal("the second ship armed nothing")
	}
	if m.celebrateGen == first {
		t.Fatal("a second ship reused the first generation")
	}
	if m.celebrate.step != 0 {
		t.Fatalf("a second ship did not restart the flourish: %#v", m.celebrate)
	}
	command, handled := m.stepCelebration(shipCelebrationMsg{gen: first})
	if !handled {
		t.Fatal("a stale step escaped the root and reached a surface")
	}
	if command != nil {
		t.Fatal("a stale step re-armed the chain")
	}
	if !m.celebrate.active || m.celebrate.step != 0 {
		t.Fatalf("a stale step disturbed the live celebration: %#v", m.celebrate)
	}

	m.celebrate = shipCelebration{}
	if command, handled := m.stepCelebration(shipCelebrationMsg{gen: m.celebrateGen}); !handled || command != nil {
		t.Fatalf("a step against a settled celebration = %v, %v", command, handled)
	}
	if _, handled := m.stepCelebration(tea.KeyPressMsg{Code: 'j'}); handled {
		t.Fatal("the celebration consumed a keystroke")
	}
}

// TestShipCelebrationIsSuppressedBelowTheFloor is the class-B rule of spec
// section 10.7.6, both halves: the effect does not run, and its tick chain is
// never armed.
func TestShipCelebrationIsSuppressedBelowTheFloor(t *testing.T) {
	for _, profile := range []colorprofile.Profile{colorprofile.ANSI256, colorprofile.ANSI, colorprofile.Ascii} {
		m := celebrationModel(t)
		updateTestModel(t, &m, tea.ColorProfileMsg{Profile: profile})
		if m.themeStyles().Graded() {
			t.Fatalf("%v resolved to the reference target", profile)
		}
		if command := m.celebrateShip(); command != nil {
			t.Fatalf("%v armed a celebration chain", profile)
		}
		if m.celebrate.active || m.celebrateLit(board.StatusDone) {
			t.Fatalf("%v ran a suppressed effect: %#v", profile, m.celebrate)
		}
	}
}

// TestShipCelebrationCollapsesWithItsToken is the other half of the collapse
// rule of spec section 10.3.1: zero means "do not run" to a clock, so a test
// harness that collapses the span gets no chain rather than a spin.
func TestShipCelebrationCollapsesWithItsToken(t *testing.T) {
	m := celebrationModel(t)
	timing := m.themeStyles().Timing
	timing.CelebrateSteps = 0
	m.applyStyles(theme.NewWith(true, timing))
	if command := m.celebrateShip(); command != nil {
		t.Fatal("a collapsed span armed a chain")
	}
	if m.celebrate.lit(timing) {
		t.Fatal("a collapsed span lit a row")
	}

	stopped := m.themeStyles().Timing
	stopped.CelebrateSteps, stopped.FPS = 12, 0
	m.applyStyles(theme.NewWith(true, stopped))
	if command := m.celebrateShip(); command != nil {
		t.Fatal("a stopped clock armed a chain")
	}
}

// TestShipCelebrationYieldsToTheBoardsOneMotion is rule 4 of spec section
// 10.8.4 from both sides: a busy or launching board never starts the flourish,
// and a flourish already running ends the moment the surface's one motion is
// claimed by something else.
func TestShipCelebrationYieldsToTheBoardsOneMotion(t *testing.T) {
	busy := celebrationModel(t)
	busy.action.busy = true
	if !busy.boardBusy() {
		t.Fatal("an action write did not make the board busy")
	}
	if command := busy.celebrateShip(); command != nil {
		t.Fatal("a busy board armed a celebration")
	}

	launching := celebrationModel(t)
	launching.loading, launching.haveBoardSnapshot = true, false
	if !launching.launching() {
		t.Fatal("the launch screen was not up")
	}
	if command := launching.celebrateShip(); command != nil {
		t.Fatal("the launch surface armed a celebration")
	}

	m := celebrationModel(t)
	if m.celebrateShip() == nil {
		t.Fatal("a settled board armed nothing")
	}
	m.action.busy = true
	if m.celebrateLit(board.StatusDone) {
		t.Fatal("a celebration drew under a busy state")
	}
	command, handled := m.stepCelebration(shipCelebrationMsg{gen: m.celebrateGen})
	if !handled || command != nil {
		t.Fatalf("a busy step = %v, %v; want the chain ended", command, handled)
	}
	if m.celebrate.active {
		t.Fatalf("a busy state left the chain armed: %#v", m.celebrate)
	}
}

// TestShipCelebrationMovesOnlyTheMetaRowsHue is the no-reflow parity of spec
// section 10.4.4 applied to the effect: the lit and dark phases differ in SGR
// alone, and every other row of the column is byte-identical across them.
func TestShipCelebrationMovesOnlyTheMetaRowsHue(t *testing.T) {
	m := celebrationModel(t)
	dark := m.renderBoardColumn(board.StatusDone, 30, 12)
	darkMeta := doneMetaRow(t, m)

	if m.celebrateShip() == nil {
		t.Fatal("the celebration armed nothing")
	}
	lit := m.renderBoardColumn(board.StatusDone, 30, 12)
	litMeta := doneMetaRow(t, m)

	if litMeta == darkMeta {
		t.Fatal("the lit phase rendered the same bytes as the settled row")
	}
	if plain(litMeta) != plain(darkMeta) {
		t.Fatalf("the flash changed the row's text: %q then %q", plain(darkMeta), plain(litMeta))
	}
	if len(lit.lines) != len(dark.lines) {
		t.Fatalf("the flash changed the column's height: %d then %d", len(dark.lines), len(lit.lines))
	}
	for i := range lit.lines {
		if lit.lines[i] == litMeta {
			continue
		}
		if lit.lines[i] != dark.lines[i] {
			t.Fatalf("row %d moved for the celebration:\n%q\n%q", i, dark.lines[i], lit.lines[i])
		}
	}
	// The board's other columns are untouched: the effect is scoped to the
	// column the card landed in.
	for _, status := range []board.Status{board.StatusTodo, board.StatusDoing, board.StatusCancelled} {
		if m.celebrateLit(status) {
			t.Fatalf("%v lit up for a card that landed in DONE", status)
		}
	}
}

// TestShipCelebrationSettledFrameIsTickInvariant is point 7 of the determinism
// contract for this surface. The settled state of the flourish is its absence,
// so the frame is stable under any number of ticks and
// TestViewIsByteStableAcrossMovingWallClock keeps its meaning.
func TestShipCelebrationSettledFrameIsTickInvariant(t *testing.T) {
	m := celebrationModel(t)
	settled := m.render()
	for range 3 {
		updateTestModel(t, &m, shipCelebrationMsg{gen: m.celebrateGen})
		if m.render() != settled {
			t.Fatal("a settled board moved under a celebration tick")
		}
	}

	if m.celebrateShip() == nil {
		t.Fatal("the celebration armed nothing")
	}
	for range m.themeStyles().Timing.CelebrateSteps {
		updateTestModel(t, &m, shipCelebrationMsg{gen: m.celebrateGen})
	}
	if m.celebrate.active {
		t.Fatalf("the celebration did not settle through the root: %#v", m.celebrate)
	}
	if m.render() != settled {
		t.Fatal("a finished celebration left residue in the settled frame")
	}
	for range 3 {
		updateTestModel(t, &m, shipCelebrationMsg{gen: m.celebrateGen})
		if m.render() != settled {
			t.Fatal("a settled celebration moved under a further tick")
		}
	}
}

// TestShipCelebrationSettledColorGolden is the settled-state golden. It pins
// the frame the flourish leaves behind - the DONE column back at FgMuted, the
// footer carrying the ship notice - because that is the only frame of the
// effect a golden may capture (determinism contract, point 4).
func TestShipCelebrationSettledColorGolden(t *testing.T) {
	m := celebrationModel(t)
	m.actionStatus, m.actionNotice = "Shipped Released", true
	if m.celebrateShip() == nil {
		t.Fatal("the celebration armed nothing")
	}
	for range m.themeStyles().Timing.CelebrateSteps {
		if _, handled := m.stepCelebration(shipCelebrationMsg{gen: m.celebrateGen}); !handled {
			t.Fatal("a celebration step was not consumed")
		}
	}
	if m.celebrate.active {
		t.Fatalf("the golden captured a running effect: %#v", m.celebrate)
	}
	golden.RequireEqual(t, []byte(theme.Downsample(m.render(), theme.ColorProfile)))
}
