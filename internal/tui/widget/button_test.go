package widget

import (
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

func TestButtonPressedWrapsInReverseVideo(t *testing.T) {
	styles := theme.New(true)
	pressed := Button(styles, ButtonOpts{Text: "Ship", UnderlineIndex: -1, Pressed: true})
	if !strings.Contains(pressed, "\x1b[7m") {
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
