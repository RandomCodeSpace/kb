package widget

import (
	"unicode"

	tea "charm.land/bubbletea/v2"
)

// Hotkey resolves a button's rendered label and its underline offset from the
// message the control already sends. Spec section 10.4.2: every button label
// underlines exactly one rune, the key that drives it, and the resolver is a
// display convention that never touches the keymap.
//
// The three steps are normative:
//
//  1. the message is not a single printable rune with no modifier: the label is
//     unchanged and carries no underline;
//  2. the label spells the rune, case-insensitively: the first matching rune
//     offset is underlined;
//  3. otherwise the key is appended as " (k)", four cells for a one-cell key,
//     and the underline lands on the key inside the parentheses.
//
// The returned offset is a rune offset into the label, never into a rendered
// button: a primitive that styles the padded string offsets it by the button's
// left padding, and Button, which renders its padding runs separately and
// slices the label, does not.
func Hotkey(label string, message tea.Msg) (text string, underline int) {
	hotkey, ok := hotkeyRune(message)
	if !ok {
		return label, -1
	}
	if index := hotkeyIndex(label, hotkey); index >= 0 {
		return label, index
	}
	return label + " (" + string(hotkey) + ")", len([]rune(label)) + 2
}

// hotkeyRune reports the single printable rune a control sends, if it sends
// one. Enter, Escape, Tab and chorded keys carry no text and are stated by a
// hint ladder instead.
func hotkeyRune(message tea.Msg) (rune, bool) {
	press, ok := message.(tea.KeyPressMsg)
	if !ok || press.Mod != 0 {
		return 0, false
	}
	runes := []rune(press.Text)
	if len(runes) != 1 || !unicode.IsPrint(runes[0]) {
		return 0, false
	}
	return runes[0], true
}

// hotkeyIndex is the first offset in label that spells hotkey, ignoring case,
// or a negative value when the label does not carry it.
func hotkeyIndex(label string, hotkey rune) int {
	wanted := unicode.ToLower(hotkey)
	for index, letter := range []rune(label) {
		if unicode.ToLower(letter) == wanted {
			return index
		}
	}
	return -1
}
