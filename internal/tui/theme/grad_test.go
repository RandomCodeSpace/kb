package theme

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// TestRampStopsAreTheNormativeTable pins spec section 10.1.2: four surfaces,
// five named ramps, every endpoint an audited palette slot. A sixth ramp is a
// spec change and comes back to the spec, not to a slice.
func TestRampStopsAreTheNormativeTable(t *testing.T) {
	cases := []struct {
		ramp       Ramp
		lead, tail Slot
	}{
		{GradSection, FgBase, FgSubtle},
		{GradSectionDanger, TintDanger, FgSubtle},
		{GradSectionArmed, TintDanger, StatusDanger},
		{GradMeter, StatusInfo, StatusOK},
		{GradWork, Brand, TintPrimary},
	}
	if len(cases) != int(numRamps) {
		t.Fatalf("the table covers %d ramps, the package defines %d", len(cases), numRamps)
	}
	for _, testCase := range cases {
		lead, tail := RampStops(testCase.ramp)
		if lead != testCase.lead || tail != testCase.tail {
			t.Errorf("ramp %d = (%d, %d), want (%d, %d)", testCase.ramp, lead, tail, testCase.lead, testCase.tail)
		}
	}
}

// TestRampStopsClampsAnUnknownRamp keeps an out-of-range ramp off the array
// bounds: a render path must degrade, not panic.
func TestRampStopsClampsAnUnknownRamp(t *testing.T) {
	lead, tail := RampStops(numRamps + 7)
	sectionLead, sectionTail := RampStops(GradSection)
	if lead != sectionLead || tail != sectionTail {
		t.Errorf("unknown ramp = (%d, %d), want the GradSection pair", lead, tail)
	}
}

// TestGradHitsBothEndpoints is the resampling contract of spec section 10.1.1:
// cluster i of n takes round(i*(GradSteps-1)/(n-1)), so the first cluster wears
// the ramp's lead color and the last wears its tail exactly.
func TestGradHitsBothEndpoints(t *testing.T) {
	styles := New(true)
	clusters := strings.Split(ansi.Strip(styles.Grad(GradWork, "abcdef")), "")
	if len(clusters) != 6 {
		t.Fatalf("gradient run stripped to %d clusters, want 6", len(clusters))
	}
	lead, tail := RampStops(GradWork)
	blend := lipgloss.Blend1D(GradSteps, styles.Pal[lead], styles.Pal[tail])
	run := styles.Grad(GradWork, "abcdef")
	first := styles.blank.Foreground(blend[0]).Render("a")
	last := styles.blank.Foreground(blend[GradSteps-1]).Render("f")
	if !strings.HasPrefix(run, first) {
		t.Errorf("gradient run = %q, want it to open on the ramp's lead color", run)
	}
	if !strings.HasSuffix(run, last) {
		t.Errorf("gradient run = %q, want it to close on the ramp's tail color", run)
	}
}

// TestGradKeepsGraphemeClustersWhole is the reason rivo/uniseg is a direct
// dependency: rune splitting recolors the inside of an emoji ZWJ sequence and
// puts an SGR change between a base character and its combining mark.
func TestGradKeepsGraphemeClustersWhole(t *testing.T) {
	styles := New(true)
	const family = "\U0001f468‍\U0001f469‍\U0001f467"
	run := styles.Grad(GradWork, family)
	if ansi.Strip(run) != family {
		t.Errorf("gradient changed the run to %q", ansi.Strip(run))
	}
	if strings.Count(run, "‍") != 2 {
		t.Errorf("gradient run = %q, want both zero-width joiners intact", run)
	}
	if index := strings.Index(run, "‍"); index >= 0 && strings.Contains(run[index:index+4], "\x1b") {
		t.Errorf("gradient run = %q, want no style change inside the cluster", run)
	}
	// A combining mark stays with its base character for the same reason.
	combining := styles.Grad(GradWork, "éx")
	if ansi.Strip(combining) != "éx" {
		t.Errorf("combining run = %q", ansi.Strip(combining))
	}
	if strings.Contains(combining, "e\x1b") {
		t.Errorf("combining run = %q, want the mark styled with its base", combining)
	}
}

// TestGradEdgeCases is the normative list of spec section 10.1.1.
func TestGradEdgeCases(t *testing.T) {
	styles := New(true)
	if got := styles.Grad(GradSection, ""); got != "" {
		t.Errorf("empty run = %q, want the empty string", got)
	}
	if got := styles.GradBold(GradSection, ""); got != "" {
		t.Errorf("empty bold run = %q, want the empty string", got)
	}
	lead, tail := RampStops(GradSection)
	blend := lipgloss.Blend1D(GradSteps, styles.Pal[lead], styles.Pal[tail])
	if got, want := styles.Grad(GradSection, "x"), styles.blank.Foreground(blend[0]).Render("x"); got != want {
		t.Errorf("single-cluster run = %q, want the ramp's lead color", got)
	}
	pair := styles.Grad(GradSection, "ab")
	if want := styles.blank.Foreground(blend[GradSteps-1]).Render("b"); !strings.HasSuffix(pair, want) {
		t.Errorf("two-cluster run = %q, want a two-tone pair", pair)
	}
	if styles.Grad(numRamps+1, "abc") != styles.Grad(GradSection, "abc") {
		t.Error("an unknown ramp must resolve to GradSection")
	}
	if styles.GradBold(numRamps+1, "abc") != styles.GradBold(GradSection, "abc") {
		t.Error("an unknown bold ramp must resolve to GradSection")
	}
}

// TestGradBoldCarriesTheBoldAttribute keeps the two weights distinct without a
// second ramp: the colors are identical, the attribute is not.
func TestGradBoldCarriesTheBoldAttribute(t *testing.T) {
	styles := New(true)
	bold := styles.GradBold(GradWork, "kb")
	if ansi.Strip(bold) != "kb" {
		t.Errorf("bold run changed the text to %q", ansi.Strip(bold))
	}
	if bold == styles.Grad(GradWork, "kb") {
		t.Error("the bold ramp must render differently from the plain one")
	}
	if !strings.Contains(bold, "\x1b[1") && !strings.Contains(bold, ";1m") {
		t.Errorf("bold run = %q, want the bold attribute", bold)
	}
}

// TestStepIndexRoundsHalfUp pins the resampling arithmetic itself, including
// the runs longer than the ramp that repeat colors by design.
func TestStepIndexRoundsHalfUp(t *testing.T) {
	cases := []struct {
		index, count, want int
	}{
		{0, 1, 0},
		{0, 2, 0},
		{1, 2, GradSteps - 1},
		{1, 3, 12},
		{0, 0, 0},
		{2, 5, 12},
		{23, 24, GradSteps - 1},
		{47, 48, GradSteps - 1},
		{1, 48, 0},
		{2, 48, 1},
	}
	for _, testCase := range cases {
		if got := stepIndex(testCase.index, testCase.count); got != testCase.want {
			t.Errorf("stepIndex(%d, %d) = %d, want %d", testCase.index, testCase.count, got, testCase.want)
		}
	}
}

// TestRampsAreBuiltOnce is spec section 6.2 applied to gradients: 5 ramps x 24
// steps x 2 weights are constructed inside New and resampled by index, never
// blended per frame.
func TestRampsAreBuiltOnce(t *testing.T) {
	styles := New(true)
	for ramp := Ramp(0); ramp < numRamps; ramp++ {
		for step := 0; step < GradSteps; step++ {
			if styles.grad[ramp][step].Render("x") == "x" {
				t.Fatalf("ramp %d step %d carries no color", ramp, step)
			}
			if styles.gradBold[ramp][step].Render("x") == styles.grad[ramp][step].Render("x") {
				t.Fatalf("ramp %d step %d has no bold form", ramp, step)
			}
		}
	}
	if styles.Dimmed.grad[GradWork][0].Render("x") == styles.grad[GradWork][0].Render("x") {
		t.Error("the dimmed variant must carry its own ramps")
	}
}

// TestMeterModelIsConfiguredForTheCutRamp pins spec section 10.1.3: the ramp
// spans the whole bar and the fill cuts it, the fill glyph is the half block so
// the component doubles the ramp, and the spring is never engaged.
func TestMeterModelIsConfiguredForTheCutRamp(t *testing.T) {
	styles := New(true)
	model := styles.Progress
	if model.Full != '▌' {
		t.Errorf("meter fill = %q, want the half block that doubles the ramp", model.Full)
	}
	if model.Empty != '░' {
		t.Errorf("meter track = %q, want the track glyph", model.Empty)
	}
	if model.ShowPercentage {
		t.Error("the meter renders no percentage; the caller already renders i/N")
	}
	if model.Width() != defaultMetrics.MeterCells {
		t.Errorf("meter width = %d, want %d", model.Width(), defaultMetrics.MeterCells)
	}
	if model.EmptyColor != styles.Pal[FgMuted] {
		t.Errorf("meter track color = %v, want the FgMuted slot", model.EmptyColor)
	}
	if model.IsAnimating() {
		t.Error("the meter is a pure function of its caller's progress and never animates")
	}
	// The bar's color position, not just its length, encodes progress: two
	// different fractions must not paint the same leading cell run.
	half := model.ViewAs(0.5)
	full := model.ViewAs(1)
	if ansi.StringWidth(half) != defaultMetrics.MeterCells || ansi.StringWidth(full) != defaultMetrics.MeterCells {
		t.Fatalf("meter widths = %d and %d", ansi.StringWidth(half), ansi.StringWidth(full))
	}
	filled := defaultMetrics.MeterCells / 2
	halfCells := strings.SplitAfter(half, ansi.ResetStyle)
	fullCells := strings.SplitAfter(full, ansi.ResetStyle)
	if len(halfCells) <= filled || len(fullCells) <= filled {
		t.Fatalf("meter emitted %d and %d runs, want more than %d", len(halfCells), len(fullCells), filled)
	}
	for cell := 0; cell < filled; cell++ {
		if halfCells[cell] != fullCells[cell] {
			t.Fatalf("cell %d = %q at half and %q at full; the ramp must span the bar", cell, halfCells[cell], fullCells[cell])
		}
	}
	if strings.Contains(full, "░") {
		t.Error("a complete meter leaves no track")
	}
	if !strings.Contains(half, "░") {
		t.Error("a half-filled meter shows its track")
	}
}

// TestGlyphRuneFallsBackToASpace keeps the component options total over a token
// that was emptied by a re-spelling.
func TestGlyphRuneFallsBackToASpace(t *testing.T) {
	if got := glyphRune(""); got != ' ' {
		t.Errorf("glyphRune(\"\") = %q, want a space", got)
	}
	if got := glyphRune("░x"); got != '░' {
		t.Errorf("glyphRune = %q, want the first rune", got)
	}
}
