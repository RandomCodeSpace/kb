package widget

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

func TestBandIsEmptyWithoutWidth(t *testing.T) {
	if got := Band(theme.New(true), BandOpts{Label: "TO DO"}); got != "" {
		t.Errorf("zero-width band rendered %q", got)
	}
}

func TestBandUnfocusedCarriesRailAndDot(t *testing.T) {
	styles := theme.New(true)
	rendered := Band(styles, BandOpts{Index: 1, Label: "TO DO", Count: 4, Hue: theme.HueTodo, Width: 42})
	plain := ansi.Strip(rendered)
	if !strings.HasPrefix(plain, "▌● 1 TO DO") {
		t.Errorf("band = %q, want the rail, dot, index and label prefix", plain)
	}
	if !strings.HasSuffix(plain, "4 ") {
		t.Errorf("band = %q, want the count right-aligned with one trailing space", plain)
	}
	if got := ansi.StringWidth(rendered); got != 42 {
		t.Errorf("band width = %d, want 42", got)
	}
}

func TestBandFocusedReplacesTheRailWithACaret(t *testing.T) {
	styles := theme.New(true)
	rendered := Band(styles, BandOpts{Index: 2, Label: "DOING", Count: 3, Hue: theme.HueDoing, Focused: true, Width: 42})
	plain := ansi.Strip(rendered)
	if !strings.HasPrefix(plain, "▸● 2 DOING") {
		t.Errorf("focused band = %q, want the focus caret prefix", plain)
	}
	if !strings.Contains(plain, "●") {
		t.Error("the focused band keeps the status dot, so the label column does not move")
	}
	if ansi.StringWidth(rendered) != 42 {
		t.Errorf("focused band width = %d, want 42", ansi.StringWidth(rendered))
	}
	if rendered == Band(styles, BandOpts{Index: 2, Label: "DOING", Count: 3, Hue: theme.HueDoing, Width: 42}) {
		t.Error("the focused band must not render like the unfocused one")
	}
}

func TestBandEllipsizesALongLabel(t *testing.T) {
	styles := theme.New(true)
	rendered := Band(styles, BandOpts{Index: 1, Label: strings.Repeat("LONG ", 20), Count: 12, Hue: theme.HueDone, Width: 24})
	plain := ansi.Strip(rendered)
	if !strings.Contains(plain, "…") {
		t.Errorf("band = %q, want an ellipsized label", plain)
	}
	if !strings.HasSuffix(plain, "12 ") {
		t.Errorf("band = %q, the count must survive a long label", plain)
	}
	if ansi.StringWidth(rendered) != 24 {
		t.Errorf("band width = %d, want 24", ansi.StringWidth(rendered))
	}
}

func TestBandSurvivesAWidthSmallerThanItsChrome(t *testing.T) {
	styles := theme.New(true)
	for width := 1; width <= 10; width++ {
		rendered := Band(styles, BandOpts{Index: 1, Label: "TO DO", Count: 400, Hue: theme.HueTodo, Width: width})
		if got := ansi.StringWidth(rendered); got != width {
			t.Errorf("band of width %d rendered %d cells", width, got)
		}
	}
}

func TestExactPadsAndTruncates(t *testing.T) {
	if got := exact("ab", 4); got != "ab  " {
		t.Errorf("exact padded to %q", got)
	}
	if got := exact("abcd", 2); got != "ab" {
		t.Errorf("exact truncated to %q", got)
	}
	if got := exact("ab", 2); got != "ab" {
		t.Errorf("exact rewrote an already exact string to %q", got)
	}
}

func TestSpacesNeverGoNegative(t *testing.T) {
	if got := spaces(-3); got != "" {
		t.Errorf("spaces(-3) = %q", got)
	}
}
