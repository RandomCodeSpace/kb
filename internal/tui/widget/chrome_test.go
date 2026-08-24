package widget

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// TestArmedIsTheOnlyHeaderBandRecolor is ratified call 6 of spec section 10.1.4.
// The armed band wears the pair section 1.9 gives the armed button, so the frame
// and the button say the same thing in the same color; every other state keeps
// the solid Brand band of section 4 step 4.
func TestArmedIsTheOnlyHeaderBandRecolor(t *testing.T) {
	styles := theme.New(true)
	opts := OverlayOpts{Title: "DELETE CARD", Seq: "#7", Width: 40, Height: 5}

	resting := strings.Split(Overlay(styles, opts), "\n")[0]
	if !strings.HasPrefix(resting, opening(styles.Overlay.HeaderBand)) {
		t.Errorf("resting header band is not the brand fill:\n%q", resting)
	}

	opts.Armed = true
	armed := strings.Split(Overlay(styles, opts), "\n")[0]
	if !strings.HasPrefix(armed, opening(styles.Overlay.HeaderBandArmed)) {
		t.Errorf("armed header band is not the alarm fill:\n%q", armed)
	}
	if ansi.Strip(resting) != ansi.Strip(armed) {
		t.Error("arming moved a cell in the header band")
	}
	if ansi.StringWidth(resting) != ansi.StringWidth(armed) {
		t.Error("arming changed the header band width")
	}
}

// TestSectionLabelCarriesItsRamp is spec section 10.1.2: the label carries the
// ramp rather than a trailing rule, the fill and the count stay flat, and the
// three ramps are three different runs.
func TestSectionLabelCarriesItsRamp(t *testing.T) {
	styles := theme.New(true)
	resting := Section(styles, "COMMENTS", "3", 40)
	danger := SectionRamp(styles, "COMMENTS", "3", 40, theme.GradSectionDanger)
	armed := SectionRamp(styles, "COMMENTS", "3", 40, theme.GradSectionArmed)

	for _, pair := range [][2]string{{resting, danger}, {danger, armed}, {resting, armed}} {
		if pair[0] == pair[1] {
			t.Error("two section modes rendered the same run")
		}
		if ansi.Strip(pair[0]) != ansi.Strip(pair[1]) {
			t.Error("a section mode moved a cell")
		}
	}
	plain := ansi.Strip(resting)
	if !strings.HasPrefix(plain, "  COMMENTS") || !strings.HasSuffix(plain, "3") {
		t.Errorf("section row = %q", plain)
	}
	// The fill is the band's own background painted with spaces, never a rule.
	if strings.ContainsAny(plain, "-─═.") {
		t.Errorf("section fill carries a rule: %q", plain)
	}
	// Every cluster of the label is painted on its own, which is what a run
	// carried by a ramp looks like and what a flat label never is.
	if got := strings.Count(resting, "38;2;"); got < len("COMMENTS") {
		t.Errorf("section label carries %d colored clusters, want at least %d:\n%q",
			got, len("COMMENTS"), resting)
	}
	flat := ansi.Strip(Section(styles, "COMMENTS", "3", 40))
	if strings.Count(flat, "\x1b") != 0 {
		t.Error("the stripped band still carries escapes")
	}
}

// opening is the SGR prefix a cached style opens with, which is the run a band
// arms its whole row with.
func opening(style interface{ Render(...string) string }) string {
	prefix, _, _ := strings.Cut(style.Render("\x00"), "\x00")
	return prefix
}

// TestBandOrderOfSacrifice is the table of spec section 10.4.5: the info is
// never truncated, the fill never drops below one cell, and below the info's own
// width plus two the info is dropped and the title takes the whole band.
func TestBandOrderOfSacrifice(t *testing.T) {
	styles := theme.New(true)
	for _, test := range []struct {
		name  string
		width int
		want  string
	}{
		{name: "fits", width: 30, want: "  A REALLY LONG TITLE      #12"},
		{name: "title truncated", width: 20, want: "  A REALLY LONG… #12"},
		{name: "minimum fill", width: 18, want: "  A REALLY LO… #12"},
		{name: "info dropped", width: 6, want: "  A R…"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := ansi.Strip(Section(styles, "A REALLY LONG TITLE", "#12", test.width))
			if got != test.want {
				t.Errorf("band at %d = %q, want %q", test.width, got, test.want)
			}
			if ansi.StringWidth(got) != test.width {
				t.Errorf("band at %d is %d cells", test.width, ansi.StringWidth(got))
			}
		})
	}
	if got := Section(styles, "X", "", 0); got != "" {
		t.Errorf("zero-width band = %q", got)
	}
}
