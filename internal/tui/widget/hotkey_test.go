package widget

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

func key(letter rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: letter, Text: string(letter)}
}

// TestHotkeyStepOneLeavesTheLabelUnmarked is the first row of spec section
// 10.4.2: a control whose message is not a single printable rune with no
// modifier carries no underline, because there is no key to state.
func TestHotkeyStepOneLeavesTheLabelUnmarked(t *testing.T) {
	cases := map[string]tea.Msg{
		"no message":  nil,
		"not a key":   struct{}{},
		"enter":       tea.KeyPressMsg{Code: tea.KeyEnter},
		"escape":      tea.KeyPressMsg{Code: tea.KeyEscape},
		"tab":         tea.KeyPressMsg{Code: tea.KeyTab},
		"chorded":     tea.KeyPressMsg{Code: 'e', Text: "e", Mod: tea.ModCtrl},
		"multi-rune":  tea.KeyPressMsg{Code: 'e', Text: "ab"},
		"unprintable": tea.KeyPressMsg{Code: 0x7f, Text: "\x7f"},
	}
	for name, message := range cases {
		text, underline := Hotkey("Close", message)
		if text != "Close" {
			t.Errorf("%s: label = %q, want it unchanged", name, text)
		}
		if underline != -1 {
			t.Errorf("%s: underline = %d, want -1", name, underline)
		}
	}
}

// TestHotkeyStepTwoUnderlinesTheSpelledRune is the second row: the label
// already spells the key, so the cue costs no cells at all.
func TestHotkeyStepTwoUnderlinesTheSpelledRune(t *testing.T) {
	cases := []struct {
		label string
		key   rune
		want  int
	}{
		{"Edit", 'e', 0},
		{"Edit", 'E', 0},
		{"Comment", 'c', 0},
		{"Purge", 'D', -1},
		{"Restore", 'r', 0},
		{"Check", 't', -1},
		{"Delete", 'e', 1},
	}
	for _, testCase := range cases {
		text, underline := Hotkey(testCase.label, key(testCase.key))
		if testCase.want < 0 {
			if text == testCase.label {
				t.Errorf("%q with key %q kept its label; the key is not spelled in it", testCase.label, testCase.key)
			}
			continue
		}
		if text != testCase.label {
			t.Errorf("%q with key %q became %q", testCase.label, testCase.key, text)
		}
		if underline != testCase.want {
			t.Errorf("%q with key %q underlines %d, want %d", testCase.label, testCase.key, underline, testCase.want)
		}
	}
}

// TestHotkeyStepThreeAppendsTheKeyAtFourCells pins the normative width figure
// of spec section 10.4.2: space, open paren, key, close paren, and the
// underline lands on the key inside them.
func TestHotkeyStepThreeAppendsTheKeyAtFourCells(t *testing.T) {
	text, underline := Hotkey("Check", key('t'))
	if text != "Check (t)" {
		t.Errorf("label = %q, want %q", text, "Check (t)")
	}
	if want := len([]rune("Check")) + 2; underline != want {
		t.Errorf("underline = %d, want %d", underline, want)
	}
	if runes := []rune(text); runes[underline] != 't' {
		t.Errorf("underline lands on %q, want the key", runes[underline])
	}
	if got := ansi.StringWidth(text) - ansi.StringWidth("Check"); got != 4 {
		t.Errorf("appended key costs %d cells, want 4", got)
	}
}

// TestHotkeyUnderlineLandsOnALabelRune is the invariant of spec section 10.4.2:
// the offset is a rune offset into the label, and Button renders its padding
// runs separately, so a padded button never underlines a padding cell.
func TestHotkeyUnderlineLandsOnALabelRune(t *testing.T) {
	styles := theme.New(true)
	text, underline := Hotkey("Kill", key('x'))
	rendered := Button(styles, ButtonOpts{Text: text, UnderlineIndex: underline, Padding: [2]int{2, 2}})
	plain := ansi.Strip(rendered)
	if plain != "  Kill (x)  " {
		t.Fatalf("button = %q", plain)
	}
	marked := Button(styles, ButtonOpts{Text: text, UnderlineIndex: underline, Padding: [2]int{2, 2}})
	unmarked := Button(styles, ButtonOpts{Text: text, UnderlineIndex: -1, Padding: [2]int{2, 2}})
	if marked == unmarked {
		t.Error("the resolved hotkey must produce an underline")
	}
	if ansi.StringWidth(marked) != ansi.StringWidth(unmarked) {
		t.Error("the underline is an attribute and must cost no cells")
	}
}

// TestHotkeyIsStateInvariant keeps the cue a fact about the keymap rather than
// about focus: every button state carries it, including the reversed pressed
// token, which reverses the color pair and leaves the attribute set.
func TestHotkeyIsStateInvariant(t *testing.T) {
	styles := theme.New(true)
	text, underline := Hotkey("Edit", key('e'))
	states := []ButtonOpts{{}, {Selected: true}, {Hovered: true}, {Armed: true}, {Pressed: true}}
	for index, state := range states {
		state.Text, state.UnderlineIndex = text, underline
		marked := Button(styles, state)
		state.UnderlineIndex = -1
		if marked == Button(styles, state) {
			t.Errorf("state %d dropped the hotkey underline", index)
		}
	}
}
