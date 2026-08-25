package theme

import (
	"fmt"
	"math"
	"sort"
	"testing"
)

// specIndices is the verified 256-color audit of spec section 1.7. It is
// recorded so 256-color collisions stay a decided fact rather than a surprise:
// a new slot that lands on an occupied index with a different hex fails here
// until it is re-hued or the alias is justified in the spec.
var specIndices = map[Slot]uint8{
	Shadow:       232,
	Canvas:       233,
	Surface:      234,
	Zebra:        235,
	Card:         236,
	Raised:       238,
	OverlaySurf:  239,
	OverlayBand:  59,
	FgBase:       255,
	FgSubtle:     248,
	FgMuted:      243,
	FgOnAccent:   233,
	Brand:        69,
	HueTodo:      111,
	HueDoing:     215,
	HueDone:      72,
	HueCancelled: 102,
	Prio1:        203,
	Prio2:        214,
	Prio3:        69,
	Prio4:        7,
	StatusOK:     72,
	StatusWarn:   214,
	StatusDanger: 203,
	StatusInfo:   69,
	StatusAlarm:  124,
	TintPrimary:  147,
	TintSuccess:  115,
	TintDanger:   217,
	Label1:       209,
	Label2:       69,
	Label3:       71,
	Label4:       141,
	Label5:       214,
}

// specHexes is the palette of spec section 1, slot by slot.
var specHexes = map[Slot]string{
	Shadow:       "#05070a",
	Canvas:       "#0b0e14",
	Surface:      "#171d27",
	Zebra:        "#1e2632",
	Card:         "#252f3d",
	Raised:       "#35404f",
	OverlaySurf:  "#3c495c",
	OverlayBand:  "#4a5970",
	FgBase:       "#e3e9f2",
	FgSubtle:     "#9aa5b6",
	FgMuted:      "#6b7686",
	FgOnAccent:   "#0b0e14",
	Brand:        "#4f8ef7",
	HueTodo:      "#7aa2f7",
	HueDoing:     "#f2a33c",
	HueDone:      "#3fbf7f",
	HueCancelled: "#7b8494",
	Prio1:        "#ff5a48",
	Prio2:        "#ffb020",
	Prio3:        "#4f8ef7",
	Prio4:        "#b8bdc7",
	StatusOK:     "#3fbf7f",
	StatusWarn:   "#ffb020",
	StatusDanger: "#ff5a48",
	StatusInfo:   "#4f8ef7",
	StatusAlarm:  "#b31f14",
	TintPrimary:  "#a8b6ff",
	TintSuccess:  "#7fe0b0",
	TintDanger:   "#ffa7a0",
	Label1:       "#ff7b54",
	Label2:       "#4f8ef7",
	Label3:       "#3f9d58",
	Label4:       "#b98af7",
	Label5:       "#ffb020",
}

// specAliases are the same-hex aliases of spec section 1.7: intended, not
// collisions. Each group is one hex shared by several roles.
var specAliases = [][]Slot{
	{Canvas, FgOnAccent},
	{Brand, Prio3, StatusInfo, Label2},
	{Prio2, StatusWarn, Label5},
	{HueDone, StatusOK},
	{Prio1, StatusDanger},
}

func TestPaletteMatchesSpecHexes(t *testing.T) {
	if len(specHexes) != int(numSlots) {
		t.Fatalf("spec records %d hexes, palette has %d slots", len(specHexes), numSlots)
	}
	for slot := Slot(0); slot < numSlots; slot++ {
		if got := darkPalette[slot].hex(); got != specHexes[slot] {
			t.Errorf("slot %d hex = %s, spec says %s", slot, got, specHexes[slot])
		}
	}
}

func TestBandRestAliasesRaised(t *testing.T) {
	if BandRest != Raised {
		t.Fatalf("BandRest = %d, want the Raised tier %d", BandRest, Raised)
	}
}

func TestPaletteQuantizesToSpecIndices(t *testing.T) {
	for slot := Slot(0); slot < numSlots; slot++ {
		got := index256(darkPalette[slot])
		if want := specIndices[slot]; got != want {
			t.Errorf("slot %d (%s) quantizes to %d, spec says %d", slot, darkPalette[slot].hex(), got, want)
		}
	}
}

// TestPaletteHasNoRealCollisions is the guard of spec section 1.7. Two slots
// may share a 256 index only when they share a hex, and only when the spec
// records that alias in writing.
func TestPaletteHasNoRealCollisions(t *testing.T) {
	allowed := map[string]bool{}
	for _, group := range specAliases {
		for _, left := range group {
			for _, right := range group {
				allowed[aliasKey(left, right)] = true
			}
		}
	}
	for left := Slot(0); left < numSlots; left++ {
		for right := left + 1; right < numSlots; right++ {
			if index256(darkPalette[left]) != index256(darkPalette[right]) {
				continue
			}
			if darkPalette[left] != darkPalette[right] {
				t.Errorf("slots %d and %d collide on index %d with different hexes %s and %s",
					left, right, index256(darkPalette[left]), darkPalette[left].hex(), darkPalette[right].hex())
				continue
			}
			if !allowed[aliasKey(left, right)] {
				t.Errorf("slots %d and %d share hex %s but the spec records no alias for them",
					left, right, darkPalette[left].hex())
			}
		}
	}
}

// TestSpecAliasesAreExact fails when a recorded alias stops being one, so the
// alias list cannot rot into documentation of a palette that changed.
func TestSpecAliasesAreExact(t *testing.T) {
	for _, group := range specAliases {
		for _, slot := range group[1:] {
			if darkPalette[slot] != darkPalette[group[0]] {
				t.Errorf("slot %d is %s, aliased to slot %d which is %s",
					slot, darkPalette[slot].hex(), group[0], darkPalette[group[0]].hex())
			}
		}
	}
}

// TestRejectedHexesStayRejected keeps the two candidates of spec section 1.7
// from being re-proposed: both quantize onto Zebra's index with a different hex.
func TestRejectedHexesStayRejected(t *testing.T) {
	rejected := map[string]rgb{
		"#1a1f2b": {0x1a, 0x1f, 0x2b},
		"#1b2330": {0x1b, 0x23, 0x30},
	}
	zebra := index256(darkPalette[Zebra])
	for hex, value := range rejected {
		if index256(value) != zebra {
			t.Errorf("rejected hex %s no longer collides with Zebra; the spec's reasoning changed", hex)
		}
		if value == darkPalette[Zebra] {
			t.Errorf("rejected hex %s is now Zebra itself", hex)
		}
	}
}

// buttonContrastFloor is the readability floor every button token pair clears,
// in truecolor and again after 256-color quantization: WCAG 2.x AA for normal
// text. A button label is the smallest run in the TUI a user must read before
// acting on it, so it gets no large-text exemption. Spec section 1.9.
const buttonContrastFloor = 4.5

// TestButtonTokensStayReadable is the contrast half of the section 1.9 audit:
// every variant, in every state, on both color profiles. A re-hued variant that
// puts a label below the floor fails here rather than in a user's terminal.
func TestButtonTokensStayReadable(t *testing.T) {
	for variant := ButtonVariant(0); variant < numButtonVariants; variant++ {
		tokens := buttonTokens[variant]
		states := map[string]buttonToken{
			"rest":    tokens.rest,
			"hovered": tokens.hovered,
			"focused": tokens.focused,
			"armed":   tokens.armed,
		}
		for state, token := range states {
			truecolor := contrastRatio(darkPalette[token.fg], darkPalette[token.bg])
			if truecolor < buttonContrastFloor {
				t.Errorf("variant %d %s: truecolor contrast %.2f, floor is %.2f",
					variant, state, truecolor, buttonContrastFloor)
			}
			quantized := contrastRatio(
				xterm256(int(index256(darkPalette[token.fg]))),
				xterm256(int(index256(darkPalette[token.bg]))),
			)
			if quantized < buttonContrastFloor {
				t.Errorf("variant %d %s: 256-color contrast %.2f, floor is %.2f",
					variant, state, quantized, buttonContrastFloor)
			}
		}
	}
}

// TestButtonVariantsStaySeparableAt256 is the honesty half: the dogfood finding
// of issue #157 was buttons that all looked alike, and a variant that collapses
// onto another variant's index at 256 colors reproduces it on the terminals
// least able to afford it.
func TestButtonVariantsStaySeparableAt256(t *testing.T) {
	seen := map[uint8]ButtonVariant{}
	for variant := ButtonVariant(0); variant < numButtonVariants; variant++ {
		for _, token := range []buttonToken{buttonTokens[variant].rest, buttonTokens[variant].focused} {
			index := index256(darkPalette[token.fg])
			if token.fg == FgOnAccent {
				index = index256(darkPalette[token.bg])
			}
			if other, ok := seen[index]; ok && other != variant {
				t.Errorf("variants %d and %d both read as index %d at 256 colors", other, variant, index)
			}
			seen[index] = variant
		}
	}
}

// TestArmedIsNotAFocusedDangerButton keeps the two-step arm state distinct from
// the destructive button it arms. Spec section 1.9: armed is the state a user
// must not misread.
func TestArmedIsNotAFocusedDangerButton(t *testing.T) {
	armed := buttonTokens[ButtonDanger].armed
	focused := buttonTokens[ButtonDanger].focused
	if darkPalette[armed.bg] == darkPalette[focused.bg] {
		t.Errorf("armed and focused danger share the fill %s", darkPalette[armed.bg].hex())
	}
	if index256(darkPalette[armed.bg]) == index256(darkPalette[focused.bg]) {
		t.Error("armed and focused danger collapse onto one index at 256 colors")
	}
}

// TestInactivePillTextStaysReadable is the contrast half of the issue #208
// audit, run the way section 1.9 runs the button audit: every text pair the
// inactive pill of section 3.6 draws, in truecolor and again after 256-color
// quantization, against the same AA floor. It is what decided the shipped form.
// The wheel hue on Surface clears the floor on all five slots in both profiles,
// so the hue rides the body text; had any slot failed, the hue would have had
// to retreat to the end caps, where the floor does not bind decorative glyphs.
//
// Measured, dark palette, hue on Surface (#171d27):
//
//	slot            truecolor  256-color
//	Label1 #ff7b54       6.62       7.21
//	Label2 #4f8ef7       5.27       5.19
//	Label3 #3f9d58       4.97       6.32
//	Label4 #b98af7       6.48       6.28
//	Label5 #ffb020       9.25       9.24
//	FgSubtle #9aa5b6     6.79       7.17
func TestInactivePillTextStaysReadable(t *testing.T) {
	type pillPair struct {
		name             string
		foreground, back Slot
	}
	pairs := []pillPair{}
	for index := 0; index < LabelWheel; index++ {
		// The tinted body run: the wheel hue over the withdrawn fill.
		pairs = append(pairs, pillPair{
			name:       fmt.Sprintf("wheel slot %d body", index),
			foreground: LabelSlot(index),
			back:       Surface,
		})
	}
	// The scoped key run of the tinted pill, which also carries the section
	// 10.4.1 toggle marker and so must clear the floor as prose, not as glyph.
	pairs = append(pairs, pillPair{name: "scoped key", foreground: FgSubtle, back: Surface})
	for _, pair := range pairs {
		name, foreground, background := pair.name, pair.foreground, pair.back
		truecolor := contrastRatio(darkPalette[foreground], darkPalette[background])
		if truecolor < buttonContrastFloor {
			t.Errorf("inactive pill %s: truecolor contrast %.2f, floor is %.2f",
				name, truecolor, buttonContrastFloor)
		}
		quantized := contrastRatio(
			xterm256(int(index256(darkPalette[foreground]))),
			xterm256(int(index256(darkPalette[background]))),
		)
		if quantized < buttonContrastFloor {
			t.Errorf("inactive pill %s: 256-color contrast %.2f, floor is %.2f",
				name, quantized, buttonContrastFloor)
		}
		t.Logf("%-18s %s on %s  truecolor %.2f  256-color %.2f",
			name, darkPalette[foreground].hex(), darkPalette[background].hex(), truecolor, quantized)
	}
}

// TestInactivePillKeepsItsWheelIdentity is the identity half of the issue #208
// audit: an inactive pill that carries a hue is only worth the cell if the hue
// still separates the wheel. Two slots that collapse onto one 256-color index
// as body text would put two different labels in the same offer color on the
// terminals least able to afford the confusion, exactly the section 1.7 rule
// the palette audit already applies to the slots themselves.
func TestInactivePillKeepsItsWheelIdentity(t *testing.T) {
	seen := map[uint8]int{}
	for index := 0; index < LabelWheel; index++ {
		quantized := index256(darkPalette[LabelSlot(index)])
		if other, ok := seen[quantized]; ok {
			t.Errorf("wheel slots %d and %d read as index %d at 256 colors", other, index, quantized)
		}
		seen[quantized] = index
		// The hue must also stay separable from the achromatic runs it sits
		// beside, or the tinted pill degrades into the withdrawn form it
		// replaced.
		for _, role := range []Slot{FgSubtle, FgMuted} {
			if index256(darkPalette[LabelSlot(index)]) == index256(darkPalette[role]) {
				t.Errorf("wheel slot %d collapses onto an achromatic text role at 256 colors", index)
			}
		}
	}
}

// contrastRatio is the WCAG 2.x contrast ratio of two colors.
func contrastRatio(left, right rgb) float64 {
	lighter, darker := left.luminance(), right.luminance()
	if lighter < darker {
		lighter, darker = darker, lighter
	}
	return (lighter + 0.05) / (darker + 0.05)
}

// luminance is the WCAG relative luminance of an 8-bit color.
func (c rgb) luminance() float64 {
	return 0.2126*channelLuminance(c.R) + 0.7152*channelLuminance(c.G) + 0.0722*channelLuminance(c.B)
}

func channelLuminance(value uint8) float64 {
	channel := float64(value) / 255
	if channel <= 0.04045 {
		return channel / 12.92
	}
	return math.Pow((channel+0.055)/1.055, 2.4)
}

func TestXterm256CoversEveryBand(t *testing.T) {
	cases := []struct {
		index int
		want  rgb
	}{
		{0, rgb{0x00, 0x00, 0x00}},
		{7, rgb{0xc0, 0xc0, 0xc0}},
		{15, rgb{0xff, 0xff, 0xff}},
		{16, rgb{0, 0, 0}},
		{69, rgb{95, 135, 255}},
		{231, rgb{255, 255, 255}},
		{232, rgb{8, 8, 8}},
		{255, rgb{238, 238, 238}},
	}
	for _, testCase := range cases {
		if got := xterm256(testCase.index); got != testCase.want {
			t.Errorf("xterm256(%d) = %v, want %v", testCase.index, got, testCase.want)
		}
	}
}

func TestIndex256ResolvesTiesToTheLowerIndex(t *testing.T) {
	// Index 16 and index 0 are both pure black; the lower index must win.
	if got := index256(rgb{0, 0, 0}); got != 0 {
		t.Errorf("index256(black) = %d, want 0", got)
	}
}

func aliasKey(left, right Slot) string {
	pair := []int{int(left), int(right)}
	sort.Ints(pair)
	return fmt.Sprintf("%d/%d", pair[0], pair[1])
}
