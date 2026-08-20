package theme

import (
	"strings"
	"testing"
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

// sameRuns compares chip runs by what they render, since lipgloss styles are
// not comparable.
func sameRuns(left, right ChipStyles) bool {
	const probe = "chip"
	return left.CapLeft.Render(probe) == right.CapLeft.Render(probe) &&
		left.CapRight.Render(probe) == right.CapRight.Render(probe) &&
		left.Body.Render(probe) == right.Body.Render(probe) &&
		left.ScopedKey.Render(probe) == right.ScopedKey.Render(probe) &&
		left.Flat.Render(probe) == right.Flat.Render(probe)
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

func distanceTo(from, to rgb) int {
	return from.distance(to)
}
