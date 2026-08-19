package theme

import (
	"fmt"
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
