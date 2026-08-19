package widget

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

func TestChipIsEmptyWithoutText(t *testing.T) {
	styles := theme.New(true)
	if got := Chip(styles, ChipOpts{Fill: theme.StatusWarn, On: theme.Card}); got != "" {
		t.Errorf("empty chip rendered %q", got)
	}
}

func TestChipCostsTextPlusTwoCaps(t *testing.T) {
	styles := theme.New(true)
	rendered := Chip(styles, ChipOpts{Text: "blocked", Fill: theme.StatusWarn, On: theme.Card})
	plain := ansi.Strip(rendered)
	if want := "▐blocked▌"; plain != want {
		t.Errorf("chip = %q, want %q", plain, want)
	}
	if got, want := ansi.StringWidth(rendered), len("blocked")+2; got != want {
		t.Errorf("chip width = %d, want %d", got, want)
	}
}

func TestChipScopedFormUsesADarkKeyHalf(t *testing.T) {
	styles := theme.New(true)
	rendered := Chip(styles, ChipOpts{Text: "feature", Key: "type:", Fill: theme.Label1, On: theme.Card})
	if plain, want := ansi.Strip(rendered), "▐type:feature▌"; plain != want {
		t.Errorf("scoped chip = %q, want %q", plain, want)
	}
	unscoped := Chip(styles, ChipOpts{Text: "feature", Fill: theme.Label1, On: theme.Card})
	if strings.Count(rendered, "\x1b[") <= strings.Count(unscoped, "\x1b[") {
		t.Error("the scoped form must carry more color runs than the plain one")
	}
}

func TestChipFlatFormDropsTheCaps(t *testing.T) {
	styles := theme.New(true)
	rendered := Chip(styles, ChipOpts{Text: "blocked", Fill: theme.StatusWarn, On: theme.Card, Flat: true})
	if plain, want := ansi.Strip(rendered), "blocked"; plain != want {
		t.Errorf("flat chip = %q, want %q", plain, want)
	}
	if !strings.Contains(rendered, "\x1b[1;") {
		t.Errorf("flat chip must be bold, got %q", rendered)
	}
}

func TestLabelWheelMatchesTheBoardHash(t *testing.T) {
	cases := map[string]int{
		"":     0,
		"bug":  (3 + int('b')) % 5,
		"area": (4 + int('a')) % 5,
		"数":    (1 + int('数')) % 5,
	}
	for tag, want := range cases {
		if got := LabelWheel(tag); got != want {
			t.Errorf("LabelWheel(%q) = %d, want %d", tag, got, want)
		}
	}
}

func TestLabelWheelStaysInsideThePalette(t *testing.T) {
	for _, tag := range []string{"a", "bb", "ccc", "dddd", "eeeee", "ffffff"} {
		index := LabelWheel(tag)
		if index < 0 || index >= theme.LabelWheel {
			t.Fatalf("LabelWheel(%q) = %d, outside the wheel", tag, index)
		}
	}
}

func TestLabelForms(t *testing.T) {
	styles := theme.New(true)
	cases := []struct {
		name string
		tag  string
		flat bool
		want string
	}{
		{"plain", "bug", false, "▐#bug▌"},
		{"plain compact", "bug", true, "#bug"},
		{"scoped", "type::feature", false, "▐type:feature▌"},
		{"scoped compact", "type::feature", true, "feature"},
		{"empty key falls back to plain", "::feature", false, "▐#::feature▌"},
		{"empty value falls back to plain", "type::", false, "▐#type::▌"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := ansi.Strip(Label(styles, testCase.tag, theme.Card, testCase.flat))
			if got != testCase.want {
				t.Errorf("Label(%q, flat=%v) = %q, want %q", testCase.tag, testCase.flat, got, testCase.want)
			}
		})
	}
}

func TestLabelIsEmptyWithoutATag(t *testing.T) {
	if got := Label(theme.New(true), "", theme.Card, false); got != "" {
		t.Errorf("empty tag rendered %q", got)
	}
}

func TestLabelHuesFollowTheWheel(t *testing.T) {
	styles := theme.New(true)
	seen := map[string]bool{}
	for _, tag := range []string{"a", "b", "c", "d", "e"} {
		seen[Label(styles, tag, theme.Card, true)] = true
	}
	if len(seen) < 2 {
		t.Error("the wheel must produce more than one hue across tags")
	}
}

func TestPriorityMarkerIsTwoCells(t *testing.T) {
	styles := theme.New(true)
	cases := map[int]string{1: "P1", 2: "P2", 3: "P3", 4: "P4", 0: "P3", 7: "P3"}
	for priority, want := range cases {
		rendered := Priority(styles, priority, theme.Card)
		if got := ansi.Strip(rendered); got != want {
			t.Errorf("Priority(%d) = %q, want %q", priority, got, want)
		}
		if got := ansi.StringWidth(rendered); got != 2 {
			t.Errorf("Priority(%d) is %d cells, want 2", priority, got)
		}
	}
}

func TestPriorityUsesTheHueOfItsSlot(t *testing.T) {
	styles := theme.New(true)
	if Priority(styles, 1, theme.Card) == Priority(styles, 2, theme.Card) {
		t.Error("P1 and P2 must not render identically")
	}
	if Priority(styles, 9, theme.Card) != Priority(styles, 3, theme.Card) {
		t.Error("an unknown priority must fall back to P3 exactly")
	}
}
