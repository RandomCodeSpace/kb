package theme

import (
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

// TestRampsFlattenToTheirLeadBelowFullFidelity is rule 2 of spec section 10.7.5,
// ratified as contestable call 5: below the truecolor floor a graded run renders
// as its lead slot, flat, across the whole run. Not the midpoint, not the
// endpoint, and not the per-cluster quantization of the ramp, which is two or
// three visible bands with boundaries falling wherever the color cube lands.
func TestRampsFlattenToTheirLeadBelowFullFidelity(t *testing.T) {
	const run = "CHECKLIST"
	for _, profile := range []colorprofile.Profile{colorprofile.ANSI256, colorprofile.ANSI, colorprofile.ASCII} {
		styles := NewFor(true, profile)
		if styles.Graded() {
			t.Fatalf("profile %v resolved to full fidelity", profile)
		}
		for ramp := Ramp(0); ramp < numRamps; ramp++ {
			lead, _ := RampStops(ramp)
			flat := styles.blank.Foreground(styles.Pal[lead])
			var want strings.Builder
			for _, cluster := range graphemes(run) {
				want.WriteString(flat.Render(cluster))
			}
			if got := styles.Grad(ramp, run); got != want.String() {
				t.Errorf("profile %v ramp %d did not flatten to its lead: %q", profile, ramp, got)
			}
			if got := styles.GradCell(ramp, 7, 9, "x"); got != flat.Render("x") {
				t.Errorf("profile %v ramp %d cell 7 left the lead: %q", profile, ramp, got)
			}
		}
	}
}

// TestRampsStayGradedAtFullFidelity is the other arm: the flattening is a
// degradation of the reference target, never a second design that leaks up.
func TestRampsStayGradedAtFullFidelity(t *testing.T) {
	styles := NewFor(true, colorprofile.TrueColor)
	if !styles.Graded() {
		t.Fatal("truecolor did not resolve to full fidelity")
	}
	for ramp := Ramp(0); ramp < numRamps; ramp++ {
		if styles.GradCell(ramp, 0, 9, "x") == styles.GradCell(ramp, 8, 9, "x") {
			t.Errorf("ramp %d rendered flat at full fidelity", ramp)
		}
	}
	// New is NewFor at the reference target, so the huh ThemeFunc seam of spec
	// section 6.3 keeps a graded theme.
	if !New(true).Graded() || !NewWith(true, TimingCollapsed).Graded() {
		t.Fatal("a constructor other than NewFor dropped below full fidelity")
	}
}

// TestDimmedVariantFlattensWithItsBase keeps the overlay backdrop of spec
// section 1.8 on the same floor as the board behind it: both are built from one
// newStyles call and a fidelity that reached only one of them would put a graded
// run beside a flat one on the same screen.
func TestDimmedVariantFlattensWithItsBase(t *testing.T) {
	styles := NewFor(true, colorprofile.ANSI256)
	dimmed := styles.Dimmed
	if dimmed == nil {
		t.Fatal("no dimmed variant was built")
	}
	if dimmed.Graded() {
		t.Fatal("the dimmed variant stayed at full fidelity")
	}
	if dimmed.GradCell(GradWork, 0, 9, "x") != dimmed.GradCell(GradWork, 8, 9, "x") {
		t.Fatal("the dimmed variant kept a graded ramp")
	}
}

// TestMeterFlattensWithTheRestOfTheRamps covers the one ramp kb hands to a
// component that blends for itself: below the floor the progress model is
// configured with the GradMeter lead alone (spec section 10.1.3). The bar keeps
// its position regardless, because position is the fill and track glyph split.
func TestMeterFlattensWithTheRestOfTheRamps(t *testing.T) {
	graded := NewFor(true, colorprofile.TrueColor)
	flat := NewFor(true, colorprofile.ANSI256)
	gradedBar := graded.Progress.ViewAs(1)
	flatBar := flat.Progress.ViewAs(1)
	if gradedBar == flatBar {
		t.Fatal("the meter rendered the same bar at both floors")
	}
	if ansi.Strip(gradedBar) != ansi.Strip(flatBar) {
		t.Fatalf("flattening changed the meter's glyphs: %q vs %q",
			ansi.Strip(gradedBar), ansi.Strip(flatBar))
	}
	tail := ansi.Style{}.ForegroundColor(flat.Pal[StatusOK]).String()
	if strings.Contains(flatBar, tail) {
		t.Fatalf("the flattened meter still carries the ramp's tail slot: %q", flatBar)
	}
	// The track is what states position when the hue does not survive.
	if !strings.Contains(ansi.Strip(flat.Progress.ViewAs(0.5)), flat.Glyph.Track) {
		t.Fatal("the flattened meter dropped its track glyph")
	}
}
