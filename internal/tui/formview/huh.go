package formview

import (
	"strings"

	huh "charm.land/huh/v2"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// huh field adapters. Spec sections 5.2 and 7: huh is compatible with kb's
// stack and owns the Confirm, Select and Note roles, but *huh.Form is not a
// tea.Model - huh keeps a v1-shaped interface whose Update returns huh.Model -
// so a field is driven through the three-line sub-model adapter below: update
// it, assert the returned huh.Model back to the concrete field, render it.
//
// kb drives these fields rather than embedding them in a form: the keymap,
// the focus order and the pointer hit regions are frozen by v1.0.1 (map #136),
// and huh owns all three when it owns the loop. Each render therefore builds a
// field from kb's current state, renders it once, and drops it. That costs a
// small allocation on a dialog frame and buys the themed field vocabulary
// without moving a single key binding.
//
// They live here rather than beside one overlay because three packages render
// them: the task action dialogs, the ADR split review and the forge import.

// HuhView is the sub-model adapter: it settles the field - a Select sizes its
// viewport in Update and renders nothing until it has - and then renders the
// huh.Model that came back.
func HuhView(field huh.Field) string {
	settled, _ := field.Update(nil)
	if settled == nil {
		return field.View()
	}
	return settled.View()
}

// HuhConfirm renders the yes/no core of a confirm prompt. Spec section 5.2
// assigns huh's Confirm to the ship and kill prompts. The first choice is the
// affirmative because huh renders the affirmative on the left, and the buttons
// must appear in the order kb's frozen tab cycle walks them.
func HuhConfirm(styles *theme.Styles, first, second string, firstSelected bool, width int) string {
	field := huh.NewConfirm().
		Affirmative(first).
		Negative(second).
		Value(&firstSelected).
		Inline(true)
	field.WithTheme(styles.HuhTheme())
	field.WithWidth(width)
	field.Focus()
	return HuhView(field)
}

// HuhInlineSelect renders one choice as a single row: the current option
// between the previous and next indicators. Spec section 5.2 assigns Select to
// the Source, Max stories, Priority and Effort choice fields, all of which are
// single rows in the frozen v1.0.1 layout with one pointer target each, so the
// inline form is the only one that fits without moving a hit region.
func HuhInlineSelect(styles *theme.Styles, choices []string, selected, width int) string {
	if len(choices) == 0 || width <= 0 {
		return ""
	}
	value := choices[min(max(selected, 0), len(choices)-1)]
	field := huh.NewSelect[string]().
		Options(huh.NewOptions(choices...)...).
		Value(&value).
		Inline(true)
	field.WithTheme(styles.HuhTheme())
	field.WithWidth(width)
	field.Focus()
	return HuhView(field)
}

// HuhNote renders a disclaimer block. Spec section 5.2 assigns huh's Note to
// the blocks that explain what an action will do.
func HuhNote(styles *theme.Styles, lines []string, width int) string {
	field := huh.NewNote().Description(strings.Join(lines, "\n"))
	field.WithTheme(styles.HuhTheme())
	field.WithWidth(width)
	return HuhView(field)
}
