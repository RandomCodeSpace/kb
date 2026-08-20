package widget

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

func TestButtonPaddingSurroundsTheLabel(t *testing.T) {
	styles := theme.New(true)
	rendered := Button(styles, ButtonOpts{Text: "Ship", UnderlineIndex: -1, Padding: [2]int{2, 3}})
	if plain, want := ansi.Strip(rendered), "  Ship   "; plain != want {
		t.Errorf("button = %q, want %q", plain, want)
	}
}

func TestButtonNegativePaddingIsClampedToZero(t *testing.T) {
	styles := theme.New(true)
	rendered := Button(styles, ButtonOpts{Text: "Ship", UnderlineIndex: -1, Padding: [2]int{-4, -1}})
	if plain := ansi.Strip(rendered); plain != "Ship" {
		t.Errorf("button = %q, want Ship", plain)
	}
}

func TestButtonStatePrecedence(t *testing.T) {
	styles := theme.New(true)
	base := ButtonOpts{Text: "Purge", UnderlineIndex: -1}
	rest := Button(styles, base)

	hovered := base
	hovered.Hovered = true
	selected := base
	selected.Selected = true
	armed := base
	armed.Armed = true

	both := base
	both.Selected = true
	both.Hovered = true
	all := base
	all.Armed = true
	all.Selected = true
	all.Hovered = true

	if Button(styles, hovered) == rest {
		t.Error("a hovered button must render differently from a resting one")
	}
	if Button(styles, selected) == Button(styles, hovered) {
		t.Error("selection must win over hover")
	}
	if Button(styles, both) != Button(styles, selected) {
		t.Error("selection must win over hover when both are set")
	}
	if Button(styles, all) != Button(styles, armed) {
		t.Error("the armed state must win over every other state")
	}
}

// TestButtonVariantsRenderDistinctly is the dogfood finding of issue #157: a
// row of resting buttons that all render the same surface says nothing about
// what any of them does.
func TestButtonVariantsRenderDistinctly(t *testing.T) {
	styles := theme.New(true)
	variants := []theme.ButtonVariant{
		theme.ButtonNeutral, theme.ButtonPrimary, theme.ButtonSuccess, theme.ButtonDanger,
	}
	seen := map[string]theme.ButtonVariant{}
	for _, variant := range variants {
		for _, state := range []ButtonOpts{{}, {Selected: true}, {Hovered: true}} {
			state.Text, state.UnderlineIndex, state.Variant = "Ship", -1, variant
			rendered := Button(styles, state)
			if ansi.Strip(rendered) != "Ship" {
				t.Fatalf("variant %d changed the label to %q", variant, ansi.Strip(rendered))
			}
			if other, ok := seen[rendered]; ok {
				t.Errorf("variants %d and %d render identically: %q", other, variant, rendered)
			}
			seen[rendered] = variant
		}
	}
}

// TestArmedIgnoresTheVariant keeps the two-step arm state one look: arming is
// only ever destructive, and the state precedence of spec section 5.1 puts it
// above every other.
func TestArmedIgnoresTheVariant(t *testing.T) {
	styles := theme.New(true)
	armed := ButtonOpts{Text: "Purge", UnderlineIndex: -1, Armed: true}
	want := Button(styles, armed)
	for _, variant := range []theme.ButtonVariant{theme.ButtonPrimary, theme.ButtonSuccess, theme.ButtonDanger} {
		armed.Variant = variant
		if got := Button(styles, armed); got != want {
			t.Errorf("armed variant %d = %q, want the one armed look %q", variant, got, want)
		}
	}
}

// reverseVideoPattern matches the SGR reverse attribute in any parameter
// position: a button carries its colors in the same sequence, so the attribute
// is never emitted as a standalone escape.
var reverseVideoPattern = regexp.MustCompile(`\x1b\[[0-9;]*\b7[;m]`)

func TestButtonPressedCarriesReverseVideo(t *testing.T) {
	styles := theme.New(true)
	pressed := Button(styles, ButtonOpts{Text: "Ship", UnderlineIndex: -1, Pressed: true})
	if !reverseVideoPattern.MatchString(pressed) {
		t.Errorf("pressed button = %q, want reverse video", pressed)
	}
	if ansi.Strip(pressed) != "Ship" {
		t.Errorf("pressed button content = %q", ansi.Strip(pressed))
	}
}

func TestButtonUnderlinesTheHotkeyRune(t *testing.T) {
	styles := theme.New(true)
	rendered := Button(styles, ButtonOpts{Text: "Ship", UnderlineIndex: 0})
	if !strings.Contains(rendered, "\x1b[4") && !strings.Contains(rendered, ";4m") {
		t.Errorf("button = %q, want an underlined hotkey", rendered)
	}
	if ansi.Strip(rendered) != "Ship" {
		t.Errorf("underlining changed the label to %q", ansi.Strip(rendered))
	}
}

func TestButtonUnderlineIndexOutOfRangeIsIgnored(t *testing.T) {
	styles := theme.New(true)
	plain := Button(styles, ButtonOpts{Text: "Ship", UnderlineIndex: -1})
	for _, index := range []int{-3, 4, 40} {
		if got := Button(styles, ButtonOpts{Text: "Ship", UnderlineIndex: index}); got != plain {
			t.Errorf("UnderlineIndex %d changed the render", index)
		}
	}
}

func TestButtonUnderlinesMultiByteRunesByPosition(t *testing.T) {
	styles := theme.New(true)
	rendered := Button(styles, ButtonOpts{Text: "日本語", UnderlineIndex: 1})
	if ansi.Strip(rendered) != "日本語" {
		t.Errorf("multi-byte label became %q", ansi.Strip(rendered))
	}
}

func TestButtonGroupSeparatesOnTheSurfaceBehind(t *testing.T) {
	styles := theme.New(true)
	left := Button(styles, ButtonOpts{Text: "Yes", UnderlineIndex: -1})
	right := Button(styles, ButtonOpts{Text: "No", UnderlineIndex: -1})
	group := ButtonGroup(styles, theme.OverlaySurf, 2, left, "", right)
	if plain, want := ansi.Strip(group), "Yes  No"; plain != want {
		t.Errorf("group = %q, want %q", plain, want)
	}
	if ansi.StringWidth(ButtonGroup(styles, theme.OverlaySurf, -1, left, right)) != ansi.StringWidth(left)+ansi.StringWidth(right) {
		t.Error("a negative gap must clamp to zero")
	}
}

func TestButtonGroupOfNothingIsEmpty(t *testing.T) {
	styles := theme.New(true)
	if got := ButtonGroup(styles, theme.OverlaySurf, 2); got != "" {
		t.Errorf("empty group rendered %q", got)
	}
}
