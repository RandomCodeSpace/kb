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
			got := ansi.Strip(Label(styles, testCase.tag, theme.Card, testCase.flat, false))
			if got != testCase.want {
				t.Errorf("Label(%q, flat=%v) = %q, want %q", testCase.tag, testCase.flat, got, testCase.want)
			}
		})
	}
}

func TestLabelIsEmptyWithoutATag(t *testing.T) {
	if got := Label(theme.New(true), "", theme.Card, false, false); got != "" {
		t.Errorf("empty tag rendered %q", got)
	}
}

func TestLabelHuesFollowTheWheel(t *testing.T) {
	styles := theme.New(true)
	seen := map[string]bool{}
	for _, tag := range []string{"a", "b", "c", "d", "e"} {
		seen[Label(styles, tag, theme.Card, true, false)] = true
	}
	if len(seen) < 2 {
		t.Error("the wheel must produce more than one hue across tags")
	}
}

func TestFilterLabelForms(t *testing.T) {
	styles := theme.New(true)
	cases := []struct {
		name     string
		tag      string
		selected bool
		focused  bool
		want     string
	}{
		{"unselected plain", "bug", false, false, "▐+ #bug▌"},
		{"selected plain", "bug", true, false, "▐x #bug▌"},
		{"unselected scoped", "type::feature", false, false, "▐+ type:feature▌"},
		{"selected scoped", "type::feature", true, false, "▐x type:feature▌"},
		{"focused thickens both caps", "bug", false, true, "█+ #bug█"},
		{"focused and selected", "type::feature", true, true, "█x type:feature█"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := ansi.Strip(FilterLabel(styles, testCase.tag, theme.Canvas,
				testCase.selected, testCase.focused, false))
			if got != testCase.want {
				t.Errorf("FilterLabel(%q, selected=%v, focused=%v) = %q, want %q",
					testCase.tag, testCase.selected, testCase.focused, got, testCase.want)
			}
		})
	}
}

func TestFilterLabelIsEmptyWithoutATag(t *testing.T) {
	if got := FilterLabel(theme.New(true), "", theme.Canvas, true, true, true); got != "" {
		t.Errorf("empty tag rendered %q", got)
	}
}

// TestFilterLabelWithdrawsTheHueWhenUnselected is the toggle affordance the
// filter bar spends the hue on: a selected pill wears its wheel fill, an
// unselected one the dim form of spec section 1.2, and the two are not the same
// bytes at the same width.
func TestFilterLabelWithdrawsTheHueWhenUnselected(t *testing.T) {
	styles := theme.New(true)
	for _, tag := range []string{"bug", "type::feature"} {
		off := FilterLabel(styles, tag, theme.Canvas, false, false, false)
		on := FilterLabel(styles, tag, theme.Canvas, true, false, false)
		if off == on {
			t.Errorf("%q renders identically selected and unselected", tag)
		}
		if strings.Contains(off, hueRun(styles, tag)) {
			t.Errorf("the unselected %q pill kept its wheel hue: %q", tag, off)
		}
		if !strings.Contains(on, hueRun(styles, tag)) {
			t.Errorf("the selected %q pill lost its wheel hue: %q", tag, on)
		}
	}
}

// hueRun is the tag's own wheel fill as the pill's right cap writes it onto the
// toolbar's Canvas tier. The right cap is the one run both pill forms carry: the
// left cap of a scoped pill is Surface in the hued form too, so it cannot tell
// the two apart.
func hueRun(styles *theme.Styles, tag string) string {
	fill := theme.LabelSlot(LabelWheel(tag))
	return styles.ChipRuns(fill, theme.Canvas).CapRight.Render(styles.Glyph.CapR)
}

func TestFilterLabelHoverUnderlinesWithoutRecoloring(t *testing.T) {
	styles := theme.New(true)
	for _, selected := range []bool{false, true} {
		rest := FilterLabel(styles, "bug", theme.Canvas, selected, false, false)
		hovered := FilterLabel(styles, "bug", theme.Canvas, selected, false, true)
		if rest == hovered {
			t.Fatalf("selected=%v: hover changed nothing", selected)
		}
		if !strings.Contains(hovered, "4;") {
			t.Errorf("selected=%v: hovered pill is not underlined: %q", selected, hovered)
		}
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
