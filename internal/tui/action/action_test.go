package action

import (
	"strings"
	"testing"
	"unicode"

	tea "charm.land/bubbletea/v2"
)

// TestRegistryRowsAreWellFormed is the table's own shape check. Every field is
// load-bearing on two surfaces at once, so a row missing one of them is a row
// that renders a blank in the help pane or an unrunnable entry in the palette.
func TestRegistryRowsAreWellFormed(t *testing.T) {
	seen := map[ID]bool{}
	keys := map[string]ID{}
	for _, entry := range All() {
		if seen[entry.ID] {
			t.Errorf("action id %d appears twice", entry.ID)
		}
		seen[entry.ID] = true
		if entry.Key == "" || entry.Hint == "" || entry.Name == "" {
			t.Errorf("action id %d has an empty field: %+v", entry.ID, entry)
		}
		if other, ok := keys[entry.Key]; ok {
			t.Errorf("key %q is bound by both action %d and action %d", entry.Key, other, entry.ID)
		}
		keys[entry.Key] = entry.ID
	}
}

// TestActionNamesAreTheFooterVoice is spec section 10.8.3's copy rule, which the
// palette inherits because Name is both the help description and the palette
// label: a lowercase noun or verb phrase, never a sentence.
func TestActionNamesAreTheFooterVoice(t *testing.T) {
	for _, entry := range All() {
		first := []rune(entry.Name)[0]
		if unicode.IsUpper(first) {
			t.Errorf("action %d name %q starts capitalized", entry.ID, entry.Name)
		}
		if strings.HasSuffix(entry.Name, ".") {
			t.Errorf("action %d name %q ends in a period", entry.ID, entry.Name)
		}
	}
}

// TestAllReturnsACopy keeps the table the durable artifact: a caller that edits
// what it was handed edits its own slice and nothing else.
func TestAllReturnsACopy(t *testing.T) {
	first := All()
	first[0].Name = "clobbered"
	if All()[0].Name == "clobbered" {
		t.Fatal("All handed out the registry itself")
	}
}

// TestInGroupKeepsDisabledRows is the self-managing-keymap property the help
// pane relies on: a feature this board lacks is reported disabled, not dropped,
// so the pane renders a stable two-column shape.
func TestInGroupKeepsDisabledRows(t *testing.T) {
	bare := InGroup(Act)
	full := InGroup(Act)
	if len(bare) != len(full) || len(bare) == 0 {
		t.Fatalf("InGroup(Act) is %d rows", len(bare))
	}
	none := Features{}
	disabled := 0
	for _, entry := range bare {
		if !entry.Enabled(none) {
			disabled++
		}
	}
	if disabled == 0 {
		t.Fatal("no action group row is gated on a feature; the test proves nothing")
	}
	for _, group := range []Group{Navigate, Act, Dismiss} {
		for _, entry := range InGroup(group) {
			if entry.Group != group {
				t.Errorf("InGroup(%d) returned a row from group %d", group, entry.Group)
			}
		}
	}
}

// TestEnabledGatesOnTheRightFeature walks the whole capability matrix, because a
// row wired to the wrong flag would offer an action the board cannot run.
func TestEnabledGatesOnTheRightFeature(t *testing.T) {
	for _, test := range []struct {
		name     string
		need     Feature
		features Features
		want     bool
	}{
		{name: "always", need: Always, features: Features{}, want: true},
		{name: "editor off", need: Editor, features: Features{}, want: false},
		{name: "editor on", need: Editor, features: Features{Editor: true}, want: true},
		{name: "settings off", need: Settings, features: Features{}, want: false},
		{name: "settings on", need: Settings, features: Features{Settings: true}, want: true},
		{name: "adr off", need: ADR, features: Features{}, want: false},
		{name: "adr on", need: ADR, features: Features{ADR: true}, want: true},
		{name: "issues off", need: Issues, features: Features{}, want: false},
		{name: "issues on", need: Issues, features: Features{Issues: true}, want: true},
		{name: "unknown feature", need: Feature(200), features: Features{}, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			entry := Action{Need: test.need}
			if got := entry.Enabled(test.features); got != test.want {
				t.Errorf("Enabled(%+v) = %v, want %v", test.features, got, test.want)
			}
		})
	}
}

// TestListedOffersOnlyRunnableRows is the palette's contract: enabled rows of
// the two board groups, never the dismissal ladder and never the binding that
// opens the palette the user is already looking at.
func TestListedOffersOnlyRunnableRows(t *testing.T) {
	full := Features{Editor: true, Settings: true, ADR: true, Issues: true}
	listed := Listed(full)
	if len(listed) == 0 {
		t.Fatal("a fully featured board lists no actions")
	}
	for _, entry := range listed {
		if entry.Group == Dismiss {
			t.Errorf("palette offers dismissal row %d", entry.ID)
		}
		if entry.ID == OpenPalette {
			t.Error("palette offers the binding that opens the palette")
		}
		if _, ok := entry.KeyPress(); !ok {
			t.Errorf("palette offers action %d, whose key %q cannot be replayed", entry.ID, entry.Key)
		}
	}
	bare := Listed(Features{})
	if len(bare) >= len(listed) {
		t.Fatalf("a bare board lists %d actions, a full board %d", len(bare), len(listed))
	}
	for _, entry := range bare {
		if entry.Need != Always {
			t.Errorf("a bare board lists action %d, which needs feature %d", entry.ID, entry.Need)
		}
	}
}

// TestKeyPressReplaysTheRegistryKey is what lets the palette run an action
// without owning a second copy of the board's dispatch: the press it hands back
// must stringify to exactly the key the board's handler matches.
func TestKeyPressReplaysTheRegistryKey(t *testing.T) {
	for _, entry := range All() {
		press, ok := entry.KeyPress()
		if !ok {
			if entry.ID != OpenPalette {
				t.Errorf("action %d key %q has no press form", entry.ID, entry.Key)
			}
			continue
		}
		if got := press.String(); got != entry.Key {
			t.Errorf("action %d replays %q, but its key is %q", entry.ID, got, entry.Key)
		}
	}
}

// TestKeyPressRejectsAChord keeps the replay honest about what it cannot build,
// rather than handing back a press for some other key.
func TestKeyPressRejectsAChord(t *testing.T) {
	if _, ok := (Action{Key: PaletteKey}).KeyPress(); ok {
		t.Errorf("KeyPress built a press for %q", PaletteKey)
	}
	if _, ok := (Action{Key: ""}).KeyPress(); ok {
		t.Error("KeyPress built a press for an empty key")
	}
	press, ok := (Action{Key: "enter"}).KeyPress()
	if !ok || press.Code != tea.KeyEnter {
		t.Errorf("enter = %+v ok=%v", press, ok)
	}
	press, ok = (Action{Key: "space"}).KeyPress()
	if !ok || press.Code != tea.KeySpace {
		t.Errorf("space = %+v ok=%v", press, ok)
	}
}

// TestGroupLabelsAreTheSectionBands covers the palette's section titles,
// including the out-of-range fallback that keeps a widened enum from rendering
// an empty band.
func TestGroupLabelsAreTheSectionBands(t *testing.T) {
	for _, test := range []struct {
		group Group
		want  string
	}{
		{group: Navigate, want: "NAVIGATE"},
		{group: Act, want: "ACTIONS"},
		{group: Dismiss, want: "SESSION"},
		{group: Group(200), want: "NAVIGATE"},
	} {
		if got := test.group.Label(); got != test.want {
			t.Errorf("group %d label = %q, want %q", test.group, got, test.want)
		}
	}
}

// TestPaletteKeyIsAChord pins the one binding the registry names for itself.
func TestPaletteKeyIsAChord(t *testing.T) {
	if PaletteKey != "ctrl+k" {
		t.Errorf("PaletteKey = %q, want ctrl+k", PaletteKey)
	}
	press := tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl}
	if press.String() != PaletteKey {
		t.Errorf("ctrl+k stringifies as %q", press.String())
	}
}
