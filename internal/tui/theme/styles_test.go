package theme

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestNewBuildsBaseAndDimmedOnce(t *testing.T) {
	styles := New(true)
	if styles.Dimmed == nil {
		t.Fatal("New must build the dimmed variant beside the base palette")
	}
	if styles.Dimmed.Dimmed != nil {
		t.Error("the dimmed instance must not carry a dimmed variant of its own")
	}
	if styles.Pal[Card] == styles.Dimmed.Pal[Card] {
		t.Error("the dimmed palette must differ from the base palette")
	}
}

func TestNewResolvesBothBackgrounds(t *testing.T) {
	dark := New(true)
	light := New(false)
	// Light-background adaptation is fog per map #136: the seam exists and
	// resolves, the light column deliberately mirrors the dark one.
	if dark.Pal[Canvas] != light.Pal[Canvas] {
		t.Error("the light column is not designed yet and must mirror the dark one")
	}
}

func TestDimBlendsTowardCanvas(t *testing.T) {
	dimmed := darkPalette.dim()
	if dimmed[Canvas] != darkPalette[Canvas] {
		t.Errorf("Canvas dimmed to %s, it is the blend target and must not move", dimmed[Canvas].hex())
	}
	// 37*0.34 + 11*0.66 rounds to 20 by the spec's round-half-up, and so on
	// per channel.
	if got, want := dimmed[Card].hex(), "#141922"; got != want {
		t.Errorf("Card dimmed to %s, want %s", got, want)
	}
	ground := darkPalette[Canvas]
	for slot := Slot(0); slot < numSlots; slot++ {
		if distanceTo(dimmed[slot], ground) > distanceTo(darkPalette[slot], ground) {
			t.Errorf("slot %d moved away from Canvas when dimmed", slot)
		}
	}
}

func TestMixChannelClamps(t *testing.T) {
	if got := mixChannel(255, 255, 2); got != 255 {
		t.Errorf("over-range blend = %d, want 255", got)
	}
	if got := mixChannel(0, 10, -2); got != 0 {
		t.Errorf("under-range blend = %d, want 0", got)
	}
}

func TestHexRendersLowercaseSixDigits(t *testing.T) {
	if got := (rgb{0x0a, 0xb0, 0xff}).hex(); got != "#0ab0ff" {
		t.Errorf("hex = %s, want #0ab0ff", got)
	}
}

func TestRGBAScalesToSixteenBit(t *testing.T) {
	red, green, blue, alpha := (rgb{0xff, 0x00, 0x80}).RGBA()
	if red != 0xffff || green != 0 || blue != 0x8080 || alpha != 0xffff {
		t.Errorf("RGBA = %d %d %d %d", red, green, blue, alpha)
	}
}

func TestColorsCoverEverySlot(t *testing.T) {
	pal := darkPalette.colors()
	for slot := Slot(0); slot < numSlots; slot++ {
		if pal[slot] == nil {
			t.Errorf("slot %d resolved to no color", slot)
		}
	}
}

func TestPrioritySlotFallsBackToPrio3(t *testing.T) {
	cases := map[int]Slot{1: Prio1, 2: Prio2, 3: Prio3, 4: Prio4, 0: Prio3, 9: Prio3, -1: Prio3}
	for priority, want := range cases {
		if got := PrioritySlot(priority); got != want {
			t.Errorf("PrioritySlot(%d) = %d, want %d", priority, got, want)
		}
	}
}

func TestLabelSlotWrapsTheWheel(t *testing.T) {
	cases := map[int]Slot{0: Label1, 4: Label5, 5: Label1, -1: Label5}
	for index, want := range cases {
		if got := LabelSlot(index); got != want {
			t.Errorf("LabelSlot(%d) = %d, want %d", index, got, want)
		}
	}
}

func TestOnAppliesBothSlots(t *testing.T) {
	styles := New(true)
	rendered := styles.On(FgBase, Card).Render("x")
	if !strings.Contains(rendered, "x") {
		t.Fatalf("rendered content lost: %q", rendered)
	}
	if !strings.Contains(rendered, "\x1b[") {
		t.Errorf("On produced no color sequences: %q", rendered)
	}
	if styles.OnBold(FgBase, Card).Render("x") == rendered {
		t.Error("OnBold must differ from On")
	}
	if styles.Fg(FgBase).Render("x") == rendered {
		t.Error("Fg must not set a background")
	}
}

func TestSurfacePicksTheCardTier(t *testing.T) {
	styles := New(true)
	cases := []struct {
		selected  bool
		alternate bool
		want      Slot
	}{
		{true, false, Raised},
		{true, true, Raised},
		{false, true, Zebra},
		{false, false, Card},
	}
	for _, testCase := range cases {
		if got := styles.Surface(testCase.selected, testCase.alternate); got != testCase.want {
			t.Errorf("Surface(%v, %v) = %d, want %d", testCase.selected, testCase.alternate, got, testCase.want)
		}
	}
}

func TestChipRunsMatchTheCachedDefaults(t *testing.T) {
	styles := New(true)
	if !sameRuns(styles.ChipRuns(Brand, Card), styles.Chip) {
		t.Error("Styles.Chip must be ChipRuns against the resting card surface")
	}
	for index := range styles.Label {
		if !sameRuns(styles.ChipRuns(LabelSlot(index), Card), styles.Label[index]) {
			t.Errorf("Styles.Label[%d] is not the wheel slot composed onto the card surface", index)
		}
	}
	if sameRuns(styles.ChipRuns(Brand, Card), styles.ChipRuns(Brand, Raised)) {
		t.Error("the same fill on a different surface must render differently")
	}
}

// TestChipCapsCarryTheFillHue is issue #219: the end caps are the pill's own
// hue over whatever ground it lands on, in both pill forms and in the scoped
// variant's first cap too. The scoped cap was Surface from section 3.6 onwards,
// which reads as a grey bar bolted to the left of the pill once the palette is
// quantized; the hue brackets the pill instead and leaves the dark key half
// inside the bracket.
func TestChipCapsCarryTheFillHue(t *testing.T) {
	styles := New(true)
	const probe = "chip"
	for _, surface := range []Slot{Canvas, Card, Zebra, Raised, OverlaySurf} {
		for index := range styles.Label {
			slot := LabelSlot(index)
			hue := styles.On(slot, surface).Render(probe)
			grey := styles.On(Surface, surface).Render(probe)
			forms := []struct {
				name string
				runs ChipStyles
			}{
				{"filled", styles.ChipRuns(slot, surface)},
				{"tinted", styles.ChipRunsTint(slot, surface)},
			}
			for _, form := range forms {
				caps := []struct {
					name     string
					rendered string
				}{
					{"CapLeft", form.runs.CapLeft.Render(probe)},
					{"CapRight", form.runs.CapRight.Render(probe)},
					{"ScopedCap", form.runs.ScopedCap.Render(probe)},
				}
				for _, end := range caps {
					if end.rendered != hue {
						t.Errorf("%s %s on surface %d slot %d does not carry the fill hue",
							form.name, end.name, surface, index)
					}
					if end.rendered == grey {
						t.Errorf("%s %s on surface %d slot %d is still the grey cap",
							form.name, end.name, surface, index)
					}
				}
			}
		}
	}
}

// sameRuns compares chip runs by what they render, since lipgloss styles are
// not comparable.
func sameRuns(left, right ChipStyles) bool {
	const probe = "chip"
	return left.CapLeft.Render(probe) == right.CapLeft.Render(probe) &&
		left.CapRight.Render(probe) == right.CapRight.Render(probe) &&
		left.ScopedCap.Render(probe) == right.ScopedCap.Render(probe) &&
		left.Body.Render(probe) == right.Body.Render(probe) &&
		left.BodyHover.Render(probe) == right.BodyHover.Render(probe) &&
		left.ScopedKey.Render(probe) == right.ScopedKey.Render(probe) &&
		left.Flat.Render(probe) == right.Flat.Render(probe) &&
		left.FlatHover.Render(probe) == right.FlatHover.Render(probe)
}

// TestChipRunsTintWithdrawTheFillAndKeepTheHue is the inactive pill of the
// filter bar: the section 3.6 anatomy with the wheel hue taken off the fill and
// left on the body text, the section 1.9 blurred-button pattern. The fill drops
// to Surface so the pill cannot read as selected, the hue survives so the pill
// can still be matched by eye to the card's label pill (issue #208), and the
// scoped pill's two-tone split survives the withdrawal rather than collapsing
// into one run.
func TestChipRunsTintWithdrawTheFillAndKeepTheHue(t *testing.T) {
	styles := New(true)
	const probe = "feature"
	for _, surface := range []Slot{Canvas, Card, OverlaySurf} {
		for index := range styles.Label {
			slot := LabelSlot(index)
			tint := styles.ChipRunsTint(slot, surface)
			filled := styles.ChipRuns(slot, surface)
			if sameRuns(tint, filled) {
				t.Errorf("the tinted runs on surface %d match the filled wheel slot %d", surface, index)
			}
			// Every cap is the same run, which is what makes the tinted pill read
			// as one chip bracketed in its own hue rather than as a lit half.
			if tint.CapLeft.Render(probe) != tint.ScopedCap.Render(probe) ||
				tint.CapRight.Render(probe) != tint.ScopedCap.Render(probe) {
				t.Errorf("surface %d slot %d: the tinted caps are not all one run", surface, index)
			}
			// Issue #219: the withdrawal takes the fill, never the identity, so
			// the caps carry the wheel hue in the tinted form exactly as the
			// filled form carries it.
			if tint.CapLeft.Render(probe) != filled.CapLeft.Render(probe) {
				t.Errorf("surface %d slot %d: the tinted cap lost the fill hue", surface, index)
			}
			// The hue lives on the body run now, which is the whole point: the
			// tinted body must differ from every other wheel slot's tinted body.
			for other := range styles.Label {
				if other == index {
					continue
				}
				if styles.ChipRunsTint(LabelSlot(other), surface).Body.Render(probe) ==
					tint.Body.Render(probe) {
					t.Errorf("surface %d: tinted slots %d and %d render the same body", surface, index, other)
				}
			}
			if tint.Body.Render(probe) == tint.ScopedKey.Render(probe) {
				t.Errorf("surface %d slot %d: the tinted key and body runs collapsed", surface, index)
			}
			if ansi.Strip(tint.Body.Render(probe)) != probe {
				t.Errorf("surface %d slot %d: the tinted body run changed the text it draws", surface, index)
			}
		}
	}
	if sameRuns(styles.ChipRunsTint(Label1, Canvas), styles.ChipRunsTint(Label1, Card)) {
		t.Error("the tinted runs on a different surface must render differently")
	}
}

// TestChipHoverRunsUnderlineAndChangeNothingElse is spec section 10.5.1: a pill
// has no tier left to raise and no cell to spare on a bigger cap, so its hover
// cue is an underline on the body run. It changes no color, so it cannot move
// the pair's contrast, and it costs no cell.
func TestChipHoverRunsUnderlineAndChangeNothingElse(t *testing.T) {
	styles := New(true)
	const probe = "feature"
	for _, runs := range []ChipStyles{
		styles.Chip, styles.ChipRuns(StatusWarn, OverlaySurf), styles.ChipRunsTint(Label3, Canvas),
	} {
		pairs := [][2]string{
			{runs.Body.Render(probe), runs.BodyHover.Render(probe)},
			{runs.Flat.Render(probe), runs.FlatHover.Render(probe)},
		}
		for _, pair := range pairs {
			rest, hovered := pair[0], pair[1]
			if rest == hovered {
				t.Error("the hovered chip run renders no cue")
			}
			if ansi.Strip(rest) != ansi.Strip(hovered) {
				t.Error("the hovered chip run changed the text it draws")
			}
			// The underline parameter, whether it opens the run or follows the
			// bold the flat form already spends.
			if !strings.Contains(hovered, "\x1b[4;") && !strings.Contains(hovered, ";4;") {
				t.Errorf("the hovered chip run carries no underline: %q", hovered)
			}
		}
	}
}

// TestRowSurfaceIsTheOneTierRaise is the overlay choice row of section 10.5.1:
// hover raises the whole row, and the raised slot is the pair section 1.9
// already measured as the Neutral hovered button, so a hovered row and a hovered
// Neutral button in the same panel read as one system.
func TestRowSurfaceIsTheOneTierRaise(t *testing.T) {
	styles := New(true)
	if got := styles.RowSurface(false); got != OverlaySurf {
		t.Errorf("resting row surface = %d, want OverlaySurf", got)
	}
	if got := styles.RowSurface(true); got != OverlayBand {
		t.Errorf("hovered row surface = %d, want OverlayBand", got)
	}
	if buttonTokens[ButtonNeutral].hovered.bg != styles.RowSurface(true) {
		t.Error("the hovered row and the hovered Neutral button no longer share a fill")
	}
}

func TestEmbeddedComponentStylesAreBuilt(t *testing.T) {
	styles := New(true)
	if styles.Input.Focused.Prompt.Render("x") == "" {
		t.Error("textinput styles were not built")
	}
	if styles.Area.Focused.Prompt.Render("x") == "" {
		t.Error("textarea styles were not built")
	}
	if styles.Help.ShortKey.Render("x") == "" {
		t.Error("help styles were not built")
	}
	if len(styles.Spinner.Frames) == 0 {
		t.Error("spinner token was not built")
	}
	if styles.Markdown.Document.Color == nil || *styles.Markdown.Document.Color != darkPalette[FgBase].hex() {
		t.Error("markdown config was not derived from the palette")
	}
	if styles.Markdown.Document.Margin == nil || *styles.Markdown.Document.Margin != 0 {
		t.Error("markdown document margin must be zeroed")
	}
	if styles.Huh == nil || styles.Huh.Focused.Title.Render("x") == "" {
		t.Error("huh styles were not built")
	}
	if styles.Huh.Focused.SelectSelector.String() == "" {
		t.Error("huh select selector lost its cursor string")
	}
}

// TestHuhThemeHandsOverTheBuiltStyles pins spec section 6.3: a huh field takes
// the styles New already resolved and never rebuilds the palette per frame.
func TestHuhThemeHandsOverTheBuiltStyles(t *testing.T) {
	styles := New(true)
	theme := styles.HuhTheme()
	if theme == nil {
		t.Fatal("HuhTheme returned no theme")
	}
	for _, isDark := range []bool{true, false} {
		if theme.Theme(isDark) != styles.Huh {
			t.Errorf("HuhTheme(%v) rebuilt the styles instead of reusing them", isDark)
		}
	}
}

func TestRailStylesExistForEveryPriority(t *testing.T) {
	styles := New(true)
	for priority := 1; priority < len(styles.Rail); priority++ {
		if styles.Rail[priority].Render("x") == "" {
			t.Errorf("resting rail style missing for priority %d", priority)
		}
		if styles.RailSel[priority].Render("x") == styles.Rail[priority].Render("x") {
			t.Errorf("selected rail style for priority %d does not change surface", priority)
		}
	}
}

// TestButtonVariantFallsBackToNeutral pins the render-path guarantee of the
// variant lookup: a caller that hands it a variant outside the set gets the
// calmest surface, never a panic in a View.
func TestButtonVariantFallsBackToNeutral(t *testing.T) {
	styles := New(true)
	neutral := styles.Button.Variant(ButtonNeutral)
	if got := styles.Button.Variant(numButtonVariants); got.Rest.Render("x") != neutral.Rest.Render("x") {
		t.Error("an out-of-range variant must resolve to Neutral")
	}
}

func TestPressedIsReverseVideo(t *testing.T) {
	styles := New(true)
	rendered := styles.Pressed.Render("x")
	if !strings.Contains(rendered, "\x1b[7m") {
		t.Errorf("Pressed = %q, want SGR 7 reverse video", rendered)
	}
	for variant := ButtonVariant(0); variant < numButtonVariants; variant++ {
		if styles.Button.Variant(variant).Pressed.Render("x") != rendered {
			t.Errorf("variant %d Pressed must be the same promoted token", variant)
		}
	}
}

// TestPressedRunSurvivesInnerResets is spec section 9.1: the pointer package no
// longer writes the escape by hand, and a run composed of themed styles keeps
// the attribute past every reset those styles emit. The run closes by clearing
// the attribute alone, so it can be substituted into a styled line.
func TestPressedRunSurvivesInnerResets(t *testing.T) {
	styles := New(true)
	plain := styles.PressedRun("[Close]")
	if plain != "\x1b[7m[Close]\x1b[27m" {
		t.Fatalf("PressedRun = %q", plain)
	}
	composed := styles.PressedRun(styles.Overlay.Surf.Render("a") + "\x1b[0m" + "b")
	if strings.Count(composed, "\x1b[7m") != 3 || !strings.HasSuffix(composed, "\x1b[27m") {
		t.Fatalf("composed PressedRun = %q", composed)
	}
}

// TestSurfaceRunPaintsGapsAnAdoptedComponentLeaves covers the seam issue #153
// needed: bubbles/help writes plain spaces between its key and description
// columns and where lipgloss joins columns of unequal length. Laying the run on
// a surface slot has to arm the background and re-arm it after every reset the
// component's own styles emit, or those cells punch holes in the panel.
func TestSurfaceRunPaintsGapsAnAdoptedComponentLeaves(t *testing.T) {
	styles := New(true)
	armed := styles.SurfaceRun(OverlaySurf, "")
	if armed == "" || !strings.HasSuffix(armed, "\x1b[m") {
		t.Fatalf("SurfaceRun of empty content = %q", armed)
	}
	background := strings.TrimSuffix(armed, "\x1b[m")
	composed := styles.SurfaceRun(OverlaySurf, styles.Help.FullKey.Render("j/k")+" "+"select card"+"\x1b[0m")
	if got := strings.Count(composed, background); got != 3 {
		t.Fatalf("re-armed %d times, want 3: %q", got, composed)
	}
	if styles.SurfaceRun(OverlayBand, "x") == styles.SurfaceRun(OverlaySurf, "x") {
		t.Fatal("SurfaceRun ignored the surface slot")
	}
}

// TestHoverRunRaisesOneRunAndRestoresTheRowsSurface is spec section 10.5.1 for
// an inline hit region (issue #212): the raise spans the run's own cells, the
// row's surface comes back at the end rather than a reset that would strip the
// rest of the line, and the tier is re-armed past every reset the run carries.
func TestHoverRunRaisesOneRunAndRestoresTheRowsSurface(t *testing.T) {
	styles := New(true)
	raised := styles.HoverRun(OverlaySurf, OverlayBand, "kb://task/7")
	if !strings.Contains(raised, "kb://task/7") || strings.Contains(raised, "\x1b[m") {
		t.Fatalf("HoverRun = %q", raised)
	}
	surface := styles.SurfaceRun(OverlaySurf, "")
	if !strings.HasSuffix(raised, strings.TrimSuffix(surface, "\x1b[m")) {
		t.Fatalf("HoverRun did not restore the row surface: %q", raised)
	}
	band := strings.TrimSuffix(styles.SurfaceRun(OverlayBand, ""), "\x1b[m")
	composed := styles.HoverRun(OverlaySurf, OverlayBand, styles.Overlay.Surf.Render("a")+"\x1b[0m"+"b")
	if got := strings.Count(composed, band); got != 3 {
		t.Fatalf("re-armed %d times, want 3: %q", got, composed)
	}
	if styles.HoverRun(numSlots, OverlayBand, "x") != "x" || styles.HoverRun(OverlaySurf, numSlots, "x") != "x" {
		t.Fatal("HoverRun accepted a slot outside the palette")
	}
}

func distanceTo(from, to rgb) int {
	return from.distance(to)
}
