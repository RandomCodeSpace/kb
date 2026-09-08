package theme

import "testing"

// TestAccentSlotWorkedExamples is the normative table of spec section 10.7.2.
// The accent is a recognition cue and nothing else, so what matters is that it
// is deterministic, total over {Brand, Label1..Label5}, and that the unnamed
// board looks exactly like kb looks without one.
func TestAccentSlotUsesTheSharedFocusAccent(t *testing.T) {
	cases := []struct {
		title string
		want  Slot
	}{
		{"Board", Brand},
		{"", Brand},
		{"   ", Brand},
		{"board", Brand},
		{"  BOARD  ", Brand},
		{"webtui", Brand},
		{"Strata", Brand},
		{"roadmap", Brand},
		{"kb-tui", Brand},
		{"kb", Brand},
	}
	for _, testCase := range cases {
		if got := AccentSlot(testCase.title); got != testCase.want {
			t.Errorf("AccentSlot(%q) = %d, want %d", testCase.title, got, testCase.want)
		}
	}
}

// TestAccentSlotFoldsCaseOnly pins the one transformation of spec section
// 10.7.2: TrimSpace then ToLower, so KB and kb are one board, and nothing
// beyond that, because any further folding would make the accent disagree with
// the label wheel on the same string.
func TestAccentSlotIsIndependentOfProjectName(t *testing.T) {
	if AccentSlot("KB") != AccentSlot("  kb ") {
		t.Error("case and surrounding space must not change a board's accent")
	}
	if AccentSlot("kb") != AccentSlot("kb-tui") {
		t.Error("project names must share the restrained focus accent")
	}
}

// TestAccentSlotIsTotalOverTheWheel is what keeps the section 1.7 audit closed
// by construction: the derivation can emit no hex the audit has not cleared.
func TestAccentSlotIsTotalOverTheWheel(t *testing.T) {
	allowed := map[Slot]bool{Brand: true}
	for index := 0; index < LabelWheel; index++ {
		allowed[LabelSlot(index)] = true
	}
	titles := []string{"", "Board", "a", "zz", "über", "日本語", "kb", "webtui", "Strata", "roadmap", "kb-tui", "x y z"}
	for _, title := range titles {
		if !allowed[AccentSlot(title)] {
			t.Errorf("AccentSlot(%q) emitted slot %d, which is outside the wheel", title, AccentSlot(title))
		}
	}
}

// TestWheelIndexIsTheShippedHash keeps the accent and the label pill on one
// hash: the two must not fork, and existing label colors must stay stable.
func TestWheelIndexIsTheShippedHash(t *testing.T) {
	cases := []struct {
		tag  string
		want int
	}{
		{"", 0},
		{"webtui", 0},
		{"strata", 1},
		{"roadmap", 1},
		{"kb-tui", 3},
		{"kb", 4},
	}
	for _, testCase := range cases {
		if got := WheelIndex(testCase.tag); got != testCase.want {
			t.Errorf("WheelIndex(%q) = %d, want %d", testCase.tag, got, testCase.want)
		}
		if got := WheelIndex(testCase.tag); got < 0 || got >= LabelWheel {
			t.Errorf("WheelIndex(%q) = %d, outside the wheel", testCase.tag, got)
		}
	}
}
