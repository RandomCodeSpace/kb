package theme_test

import (
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// TestPlainSpinnerTakesTheOneClock is obligation 12 of spec section 10.2.7.
// Leaving Styles.Spinner.FPS at the bubbles default would be a second authored
// interval by another name (spec section 10.2.1).
func TestPlainSpinnerTakesTheOneClock(t *testing.T) {
	styles := theme.New(true)
	timing := styles.Timing
	if styles.Spinner.FPS != timing.PlainFrame() {
		t.Fatalf("Spinner.FPS = %v, want %v", styles.Spinner.FPS, timing.PlainFrame())
	}
	// The stride reproduces today's spinner.Dot cadence exactly, so collapsing
	// three independent tickers onto one constant changes nothing a user sees.
	if got := timing.PlainStride * int(timing.Interval()); got != int(styles.Spinner.FPS) {
		t.Fatalf("PlainStride * Interval = %d, want %d", got, styles.Spinner.FPS)
	}
	collapsed := theme.NewWith(true, theme.TimingCollapsed)
	if collapsed.Spinner.FPS != 0 {
		t.Fatalf("a collapsed clock left the plain tier at %v", collapsed.Spinner.FPS)
	}
	if collapsed.Dimmed.Spinner.FPS != 0 {
		t.Fatal("the dimmed instance kept a second clock")
	}
}

// TestFidelityResolvesTheTerminalFloor is the token of spec section 10.7.5.
func TestFidelityResolvesTheTerminalFloor(t *testing.T) {
	for _, test := range []struct {
		profile colorprofile.Profile
		want    theme.Fidelity
	}{
		{colorprofile.TrueColor, theme.FidelityFull},
		{colorprofile.Unknown, theme.FidelityFull},
		{colorprofile.ANSI256, theme.FidelityIndexed},
		{colorprofile.ANSI, theme.FidelityIndexed},
		{colorprofile.Ascii, theme.FidelityFlat},
		{colorprofile.NoTTY, theme.FidelityFlat},
	} {
		if got := theme.FidelityFor(test.profile); got != test.want {
			t.Fatalf("FidelityFor(%v) = %d, want %d", test.profile, got, test.want)
		}
	}
	// New is NewFor at the reference target, so the huh ThemeFunc seam of spec
	// section 6.3 keeps its exact signature.
	if !theme.New(true).Graded() {
		t.Fatal("New did not resolve the reference target")
	}
	indexed := theme.NewFor(true, colorprofile.ANSI256)
	if indexed.Graded() || indexed.Fidelity != theme.FidelityIndexed {
		t.Fatal("NewFor did not carry the floor onto the base instance")
	}
	if indexed.Dimmed.Graded() {
		t.Fatal("NewFor did not carry the floor onto the dimmed instance")
	}
	// The floor decides whether an effect starts, never which color it picks:
	// the palette is identical at every fidelity (spec section 10.7.5 rule 1).
	if indexed.Pal != theme.New(true).Pal {
		t.Fatal("a lesser profile resolved a second palette")
	}
}

// TestGradCellResamplesTheRampByColumn is the ramp indexing of spec section
// 10.2.5: the branded engine rebuilds one cell of one frame at a time, so it
// takes the ramp by position rather than by whole-run walk.
func TestGradCellResamplesTheRampByColumn(t *testing.T) {
	styles := theme.New(true)
	lead, tail := theme.RampStops(theme.GradWork)

	// The endpoints are compared against the ramp's own palette slots rather
	// than a literal: both ends of every ramp are slots the section 1.7 audit
	// already covers.
	if got := styles.GradCell(theme.GradWork, 0, 48, "x"); !strings.Contains(got, ansi.Style{}.ForegroundColor(styles.Pal[lead]).String()) {
		t.Fatalf("column 0 did not wear the ramp lead: %q", got)
	}
	if got := styles.GradCell(theme.GradWork, 47, 48, "x"); !strings.Contains(got, ansi.Style{}.ForegroundColor(styles.Pal[tail]).String()) {
		t.Fatalf("the last column did not wear the ramp tail: %q", got)
	}
	// A run longer than the ramp shares a style across neighbouring columns
	// rather than blending a new one per cell (spec section 6.2).
	if styles.GradCell(theme.GradWork, 0, 48, "x") != styles.GradCell(theme.GradWork, 1, 48, "x") {
		t.Fatal("a 48-column run built one style per column")
	}
	// A single-column run and an out-of-range column both resolve on the ramp
	// rather than off the end of it.
	single := styles.GradCell(theme.GradWork, 0, 1, "x")
	if single != styles.GradCell(theme.GradWork, -3, 1, "x") {
		t.Fatal("a negative column left the ramp lead")
	}
	if got := styles.GradCell(theme.GradWork, 99, 2, "x"); got != styles.GradCell(theme.GradWork, 1, 2, "x") {
		t.Fatalf("an overlong column ran off the ramp: %q", got)
	}
	if got := styles.GradCell(theme.GradWork, 2, 1, "x"); got != styles.GradCell(theme.GradWork, 99, 2, "x") {
		t.Fatalf("a zero-span run ran off the ramp: %q", got)
	}
	if got := ansi.Strip(styles.GradCell(theme.Ramp(99), 0, 4, "x")); got != "x" {
		t.Fatalf("an out-of-range ramp panicked a render path: %q", got)
	}
}

// TestBandRunRearmsTheBandAfterAColoredRun is the composition rule a branded
// busy row depends on: an overlay band is one styled run over the whole row, so
// a colored fragment inside it would otherwise drop the band for every cell
// after it.
func TestBandRunRearmsTheBandAfterAColoredRun(t *testing.T) {
	styles := theme.New(true)
	plain := "loading board"
	for _, band := range []theme.Band{theme.BandHeader, theme.BandSection, theme.BandFooter} {
		if got := styles.BandRun(band, plain); got != plain {
			t.Fatalf("band %d rewrote plain content: %q", band, got)
		}
	}
	colored := styles.GradCell(theme.GradWork, 0, 4, "ab")
	rearmed := styles.BandRun(theme.BandFooter, colored)
	if rearmed == colored {
		t.Fatal("a colored run was not re-armed")
	}
	if !strings.HasSuffix(rearmed, ansi.Style{}.ForegroundColor(styles.Pal[theme.FgSubtle]).BackgroundColor(styles.Pal[theme.OverlayBand]).String()) {
		t.Fatalf("the footer band was not re-armed after the run: %q", rearmed)
	}
	if ansi.Strip(rearmed) != "ab" {
		t.Fatalf("re-arming changed the rendered text: %q", ansi.Strip(rearmed))
	}
	// An explicit reset - the form glamour and huh emit - is re-armed too.
	explicit := "a\x1b[0mb"
	if got := styles.BandRun(theme.BandFooter, explicit); got == explicit {
		t.Fatal("an explicit reset was not re-armed")
	}
	// An out-of-range band returns the content rather than indexing off the
	// end of the table.
	if got := styles.BandRun(theme.Band(99), colored); got != colored {
		t.Fatalf("an out-of-range band rewrote content: %q", got)
	}
}

// TestWorkStylesCarryNoBackground is the composition contract of spec section
// 10.2.5: the caller lays the engine's output onto its own shade tier, so a
// work style that carried a background would fight the surface behind it.
func TestWorkStylesCarryNoBackground(t *testing.T) {
	styles := theme.New(true)
	for name, style := range map[string]string{
		"Label":  styles.Work.Label.Render("x"),
		"Birth":  styles.Work.Birth.Render("x"),
		"Suffix": styles.Work.Suffix.Render("x"),
	} {
		if strings.Contains(style, "48;2;") {
			t.Fatalf("Work.%s carried a background: %q", name, style)
		}
		if ansi.Strip(style) != "x" {
			t.Fatalf("Work.%s rewrote its content", name)
		}
	}
	if styles.Work.Birth.Render("x") == styles.Work.Suffix.Render("x") {
		t.Fatal("the pre-birth cell and the suffix wear the same hue")
	}
}
