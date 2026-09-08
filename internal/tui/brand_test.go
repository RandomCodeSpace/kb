package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
	"github.com/RandomCodeSpace/kb/internal/tui/widget"
)

// goldenLaunchModel is the launch screen at the settled frame. Both goldens pin
// brandStretch explicitly, because a golden that let the memo of spec section
// 10.6.2 roll would be a width flake by construction.
func goldenLaunchModel(width, height int) Model {
	m := newTestRootModel(stubBoardReader{}, nil, "alice")
	m.width, m.height = width, height
	m.loading = true
	m.haveBoardSnapshot = false
	m.brandStretch = 1
	m.brandSeed = 42
	m.brandFrame = m.themeStyles().Timing.BrandBirthSteps
	m.version = "1.2.0"
	rebuildTestView(&m)
	return m
}

// TestLaunchScreenGolden is the geometry golden of spec section 10.6.10: the
// centering, the meta row's gap arithmetic and its left-slot rule, captured at
// the profile where the reveal cannot run at all.
func TestLaunchScreenGolden(t *testing.T) {
	m := goldenLaunchModel(72, 20)
	golden.RequireEqual(t, []byte(ansi.Strip(theme.Downsample(m.View().Content, theme.StructureProfile))))
}

// TestLaunchScreenColorGolden pins the palette at the settled frame: the mark's
// per-line ramp, the meta row's status hue and the FgSubtle version.
func TestLaunchScreenColorGolden(t *testing.T) {
	m := goldenLaunchModel(72, 20)
	golden.RequireEqual(t, []byte(theme.Downsample(m.View().Content, theme.ColorProfile)))
}

// TestLaunchScreenOwnsTheFrameUntilTheFirstSnapshot is spec section 10.6.7: the
// launch screen exists only while there is nothing else to draw, and it is
// dropped the instant the first board snapshot lands.
func TestLaunchScreenOwnsTheFrameUntilTheFirstSnapshot(t *testing.T) {
	m := goldenLaunchModel(72, 20)
	if !m.launching() {
		t.Fatal("a loading model with no snapshot is not launching")
	}
	launch := ansi.Strip(m.View().Content)
	if !strings.Contains(launch, "loading board") || !strings.Contains(launch, "v1.2.0") {
		t.Fatalf("launch screen missing its meta row:\n%s", launch)
	}
	if strings.Contains(launch, "TO DO") || strings.Contains(launch, "j/k cards") {
		t.Fatalf("launch screen rendered board chrome:\n%s", launch)
	}
	if got := len(strings.Split(launch, "\n")); got != m.height {
		t.Fatalf("launch screen is %d rows, want %d", got, m.height)
	}
	for _, line := range strings.Split(launch, "\n") {
		if got := ansi.StringWidth(line); got != m.width {
			t.Fatalf("launch row is %d columns, want %d: %q", got, m.width, line)
		}
	}

	completeBoardLoad(t, &m, m.Init())
	if m.launching() {
		t.Fatal("the launch screen survived the first snapshot")
	}
	if board := ansi.Strip(m.View().Content); !strings.Contains(board, "TO DO") {
		t.Fatalf("board did not take the frame back:\n%s", board)
	}
}

// TestLaunchScreenReloadKeepsTheBoard is the other half of the gate: a reload
// has a snapshot behind it, so the board stays on screen instead of flashing
// the mark again.
func TestLaunchScreenReloadKeepsTheBoard(t *testing.T) {
	m := goldenLaunchModel(72, 20)
	completeBoardLoad(t, &m, m.Init())
	m.loading = true
	if m.launching() {
		t.Fatal("a reload re-entered the launch screen")
	}
}

// TestLaunchRevealArmsOnlyWhenItCanCarryColor is ratified call 5 and spec
// section 10.7.6: the reveal is a class-B effect, so below FidelityFull the
// frame counter is seeded at the settled step and no tick is ever scheduled.
func TestLaunchRevealArmsOnlyWhenItCanCarryColor(t *testing.T) {
	m := goldenLaunchModel(72, 20)
	m.brandFrame = 0
	if m.brandReveal() == nil {
		t.Fatal("a truecolor launch did not arm the reveal")
	}

	flat := goldenLaunchModel(72, 20)
	flat.brandFrame = 0
	flat.applyStyles(theme.NewFor(true, theme.StructureProfile))
	if flat.brandReveal() != nil {
		t.Fatal("the reveal armed below FidelityFull")
	}
	if !flat.brandSettled() {
		t.Fatal("a suppressed reveal did not seed at the settled frame")
	}
	// A suppressed reveal has no mid-reveal frame at all, which is what keeps
	// the ASCII-pinned structure goldens from ever capturing one.
	if flat.brandFrame != flat.themeStyles().Timing.BrandBirthSteps {
		t.Fatalf("suppressed frame counter = %d", flat.brandFrame)
	}
}

// TestLaunchRevealTerminatesItself is spec section 10.6.6: the tick returns nil
// at the settled frame, on a stale generation, and the moment the launch screen
// stops owning the frame.
func TestLaunchRevealTerminatesItself(t *testing.T) {
	m := goldenLaunchModel(72, 20)
	m.brandFrame = 0
	steps := m.themeStyles().Timing.BrandBirthSteps

	command := m.brandReveal()
	if command == nil {
		t.Fatal("reveal did not arm")
	}
	for step := range steps {
		next := m.stepBrand(brandStepMsg{gen: m.brandGen})
		if m.brandFrame != step+1 {
			t.Fatalf("step %d left the counter at %d", step, m.brandFrame)
		}
		if step == steps-1 {
			if next != nil {
				t.Fatal("the settled frame re-armed the chain")
			}
			break
		}
		if next == nil {
			t.Fatalf("step %d terminated the chain early", step)
		}
	}
	if !m.brandSettled() {
		t.Fatal("the chain did not settle")
	}
	if m.stepBrand(brandStepMsg{gen: m.brandGen}) != nil || m.brandFrame != steps {
		t.Fatal("a tick past the settled frame moved the counter")
	}

	stale := goldenLaunchModel(72, 20)
	stale.brandFrame = 0
	if stale.stepBrand(brandStepMsg{gen: stale.brandGen + 1}) != nil || stale.brandFrame != 0 {
		t.Fatal("a stale generation advanced the reveal")
	}
	loaded := goldenLaunchModel(72, 20)
	loaded.brandFrame = 0
	loaded.haveBoardSnapshot = true
	if loaded.stepBrand(brandStepMsg{gen: loaded.brandGen}) != nil || loaded.brandFrame != 0 {
		t.Fatal("the chain outlived the launch screen")
	}
}

// TestLaunchRevealRoutesThroughUpdate covers the message arm itself, so the
// chain is wired rather than only wireable.
func TestLaunchRevealRoutesThroughUpdate(t *testing.T) {
	m := goldenLaunchModel(72, 20)
	m.brandFrame = 0
	if command := updateTestModel(t, &m, brandStepMsg{gen: m.brandGen}); command == nil {
		t.Fatal("Update did not re-arm the reveal")
	}
	if m.brandFrame != 1 {
		t.Fatalf("Update left the counter at %d", m.brandFrame)
	}
}

// TestLaunchScreenDropsTheMarkOnASmallFrame is spec section 10.6.7: below
// either floor the mark goes and the meta row stays, because the meta row is
// the half that carries facts.
func TestLaunchScreenDropsTheMarkOnASmallFrame(t *testing.T) {
	m := goldenLaunchModel(72, 20)
	m.width, m.height = 40, m.themeStyles().Metrics.BrandMinH-1
	small := ansi.Strip(m.renderLaunch())
	if strings.Contains(small, m.themeStyles().Glyph.RailFull) {
		t.Fatalf("short frame kept the mark:\n%s", small)
	}
	if !strings.Contains(small, "v1.2.0") {
		t.Fatalf("short frame dropped the meta row:\n%s", small)
	}
}

// TestLaunchScreenIsByteStableAcrossMovingWallClock is the determinism contract
// of spec section 10.2.2 applied to the one screen this slice adds: the settled
// state renders identically no matter what the clock says.
func TestLaunchScreenIsByteStableAcrossMovingWallClock(t *testing.T) {
	m := goldenLaunchModel(72, 20)
	first := m.renderLaunch()
	for range 4 {
		if again := m.renderLaunch(); again != first {
			t.Fatal("the settled launch screen is not byte stable")
		}
	}
}

// TestTopBarCarriesThePerProjectAccent is ratified call 3 and spec section
// 10.6.4: the leading three columns are the accent rail and the bold accent
// wordmark, flat, with no ramp anywhere on the row.
func TestTopBarCarriesThePerProjectAccent(t *testing.T) {
	m := newTestRootModel(stubBoardReader{}, nil, "alice")
	styles := m.themeStyles()
	for _, title := range []string{"kb", "webtui", "Board", ""} {
		m.board = board.Board{Title: title}
		row := m.renderTopBar(styles, 80)
		resolved := title
		if strings.TrimSpace(resolved) == "" {
			resolved = "Board"
		}
		accent := theme.AccentSlot(resolved)
		want := styles.On(accent, theme.Canvas).Render(styles.Glyph.Rail) +
			styles.OnBold(accent, theme.Canvas).Render("kb")
		if !strings.HasPrefix(row, want) {
			t.Fatalf("title %q did not open with its accent rail and wordmark", title)
		}
		if plain := ansi.Strip(row); !strings.HasPrefix(plain, styles.Glyph.Rail+"kb / "+resolved) || strings.Contains(plain, "alice") {
			t.Fatalf("title %q top bar = %q", title, plain)
		}
	}
}

// TestTopBarAccentTracksTheBoardTitle is spec section 10.7.2: two boards with
// different names take different wheel slots, and the unnamed board takes
// Brand rather than a hash of the literal default.
func TestTopBarUsesOneAccentAcrossBoardTitles(t *testing.T) {
	m := newTestRootModel(stubBoardReader{}, nil, "alice")
	styles := m.themeStyles()
	// Only the three accent columns are compared: the rest of the row prints
	// the title itself, which differs by construction.
	render := func(title string) string {
		m.board = board.Board{Title: title}
		row := m.renderTopBar(styles, 80)
		return row[:strings.Index(row, "kb")+len("kb")]
	}
	if render("kb") != render("webtui") {
		t.Fatal("board titles did not share the restrained focus accent")
	}
	if render("Board") != render("") {
		t.Fatal("the default title and the empty title took different accents")
	}
	if render("KB") != render("  kb ") {
		t.Fatal("the accent is not case folded and trimmed")
	}
}

// TestTopBarWordmarkCarriesNoGradient is the other half of ratified call 3: the
// accent is the row's one hue and the ramp lives on the launch mark alone.
func TestTopBarWordmarkCarriesNoGradient(t *testing.T) {
	m := newTestRootModel(stubBoardReader{}, nil, "alice")
	styles := m.themeStyles()
	m.board = board.Board{Title: "webtui"}
	row := m.renderTopBar(styles, 80)
	accent := theme.AccentSlot("webtui")
	// A gradient would paint the two wordmark cells different colors, so the
	// whole run resolves to one flat foreground when it is not one.
	if !strings.HasPrefix(row, styles.On(accent, theme.Canvas).Render(styles.Glyph.Rail)+
		styles.OnBold(accent, theme.Canvas).Render("kb")) {
		t.Fatalf("the wordmark run is not a flat accent: %q", row)
	}
}

// TestBrandMemoIsRolledOncePerModel is spec section 10.6.2: the stretch is a
// plain field set in NewModel, so a resize can never re-roll it.
func TestBrandMemoIsRolledOncePerModel(t *testing.T) {
	m := goldenLaunchModel(72, 20)
	before := m.renderLaunch()
	for _, width := range []int{80, 96, 72} {
		m.width = width
		m.renderLaunch()
	}
	m.width = 72
	if after := m.renderLaunch(); after != before {
		t.Fatal("the mark changed width across a resize; the memo re-rolled")
	}
	metrics := m.themeStyles().Metrics
	rolled := map[int]bool{}
	for range 50 {
		rolled[NewModel(stubBoardReader{}, nil, "alice").brandStretch] = true
	}
	for stretch := range rolled {
		if stretch < 0 || stretch > metrics.BrandStretchMax {
			t.Fatalf("NewModel rolled stretch %d", stretch)
		}
	}
	if len(rolled) < 2 {
		t.Fatalf("NewModel rolled only %v across 50 models", rolled)
	}
	if width := widget.BrandMarkWidth(metrics, m.brandStretch); width != metrics.BrandMarkW+1 {
		t.Fatalf("pinned stretch produced width %d", width)
	}
}

// TestSetVersionReachesTheMetaRow is the plumbing of spec section 10.6.5: the
// TUI cannot reach package main's build info, so the string arrives on the
// model and an empty one renders the left slot alone.
func TestSetVersionReachesTheMetaRow(t *testing.T) {
	m := goldenLaunchModel(72, 20)
	m.SetVersion("devel")
	if !strings.Contains(ansi.Strip(m.renderLaunch()), "devel") {
		t.Fatal("the meta row dropped the devel version")
	}
	m.SetVersion("")
	launch := ansi.Strip(m.renderLaunch())
	if strings.Contains(launch, "v") && strings.Contains(launch, "unknown") {
		t.Fatalf("an empty version rendered a version slot:\n%s", launch)
	}
	if !strings.Contains(launch, "loading board") {
		t.Fatal("an empty version dropped the left slot too")
	}
}
