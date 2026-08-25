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

func TestChipCostsTextPlusTwoPadCells(t *testing.T) {
	styles := theme.New(true)
	rendered := Chip(styles, ChipOpts{Text: "blocked", Fill: theme.StatusWarn, On: theme.Card})
	plain := ansi.Strip(rendered)
	if want := " blocked "; plain != want {
		t.Errorf("chip = %q, want %q", plain, want)
	}
	if got, want := ansi.StringWidth(rendered), len("blocked")+2; got != want {
		t.Errorf("chip width = %d, want %d", got, want)
	}
}

// TestChipDrawsNoBlockGlyph is issue #227 at the widget: the pill's end cells
// are padding on the pill's own ground, so nothing the widget writes depends on
// a font drawing a half block flush to the cell edge.
func TestChipDrawsNoBlockGlyph(t *testing.T) {
	styles := theme.New(true)
	for _, opts := range []ChipOpts{
		{Text: "blocked", Fill: theme.StatusWarn, On: theme.Card},
		{Text: "feature", Key: "type:", Fill: theme.Label1, On: theme.Card},
		{Text: "bug", Mark: styles.Glyph.MarkFilterOn, Fill: theme.Label2, On: theme.Canvas, Dim: true},
		{Text: "bug", Mark: styles.Glyph.MarkFilterOff, Fill: theme.Label2, On: theme.Canvas, Dim: true, Focused: true},
	} {
		plain := ansi.Strip(Chip(styles, opts))
		for _, glyph := range []string{styles.Glyph.CapL, styles.Glyph.CapR, styles.Glyph.RailFull} {
			if strings.Contains(plain, glyph) {
				t.Errorf("chip %q still draws the block glyph %q", plain, glyph)
			}
		}
	}
}

func TestChipScopedFormUsesADarkKeyHalf(t *testing.T) {
	styles := theme.New(true)
	rendered := Chip(styles, ChipOpts{Text: "feature", Key: "type:", Fill: theme.Label1, On: theme.Card})
	if plain, want := ansi.Strip(rendered), " type:feature "; plain != want {
		t.Errorf("scoped chip = %q, want %q", plain, want)
	}
	unscoped := Chip(styles, ChipOpts{Text: "feature", Fill: theme.Label1, On: theme.Card})
	if strings.Count(rendered, "\x1b[") <= strings.Count(unscoped, "\x1b[") {
		t.Error("the scoped form must carry more color runs than the plain one")
	}
}

func TestChipFlatFormDropsThePadding(t *testing.T) {
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
		{"plain", "bug", false, " #bug "},
		{"plain compact", "bug", true, "#bug"},
		{"scoped", "type::feature", false, " type:feature "},
		{"scoped compact", "type::feature", true, "feature"},
		{"empty key falls back to plain", "::feature", false, " #::feature "},
		{"empty value falls back to plain", "type::", false, " #type:: "},
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
		{"unselected plain", "bug", false, false, " + #bug "},
		{"selected plain", "bug", true, false, " x #bug "},
		{"unselected scoped", "type::feature", false, false, " + type:feature "},
		{"selected scoped", "type::feature", true, false, " x type:feature "},
		{"focus draws no glyph", "bug", false, true, " + #bug "},
		{"focused and selected", "type::feature", true, true, " x type:feature "},
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

// TestFilterLabelWithdrawsTheFillAndKeepsTheHue is the toggle affordance the
// filter bar spends the hue on, after issue #208: a selected pill wears its
// wheel fill, an unselected one withdraws that fill to Surface but keeps the
// same wheel hue on its body text, so the offer can still be matched by eye to
// the label pill on the card it filters for. The two forms are not the same
// bytes, and neither is a different width.
func TestFilterLabelWithdrawsTheFillAndKeepsTheHue(t *testing.T) {
	styles := theme.New(true)
	for _, tag := range []string{"bug", "type::feature"} {
		off := FilterLabel(styles, tag, theme.Canvas, false, false, false)
		on := FilterLabel(styles, tag, theme.Canvas, true, false, false)
		if off == on {
			t.Errorf("%q renders identically selected and unselected", tag)
		}
		if strings.Contains(off, fillRun(styles, tag)) {
			t.Errorf("the unselected %q pill kept its wheel fill: %q", tag, off)
		}
		if !strings.Contains(on, fillRun(styles, tag)) {
			t.Errorf("the selected %q pill lost its wheel fill: %q", tag, on)
		}
		if !strings.Contains(off, tintRun(styles, tag)) {
			t.Errorf("the unselected %q pill lost its wheel hue: %q", tag, off)
		}
		if ansi.StringWidth(off) != ansi.StringWidth(on) {
			t.Errorf("%q changed width across the toggle: %d then %d",
				tag, ansi.StringWidth(off), ansi.StringWidth(on))
		}
	}
}

// TestFilterLabelPadsAreTheSpanItself is issue #227 at the call site: the pill
// ends in a padding cell drawn on the ground the run beside it owns, in both
// toggle states and in the scoped form, so the offer reads as one flat span of
// color. The end caps this replaced drew two hued half-blocks flanking an
// inactive pill that had no fill for them to fuse into.
func TestFilterLabelPadsAreTheSpanItself(t *testing.T) {
	styles := theme.New(true)
	for _, tag := range []string{"bug", "type::feature"} {
		fill := theme.LabelSlot(LabelWheel(tag))
		for _, selected := range []bool{false, true} {
			runs := styles.ChipRuns(fill, theme.Canvas)
			if !selected {
				runs = styles.ChipRunsTint(fill, theme.Canvas)
			}
			rendered := FilterLabel(styles, tag, theme.Canvas, selected, false, false)
			if !strings.HasSuffix(rendered, runs.Pad.Render(" ")) {
				t.Errorf("%q selected=%v: the trailing pad is not the body ground: %q", tag, selected, rendered)
			}
			head := runs.Pad
			if strings.Contains(tag, "::") {
				head = runs.ScopedPad
			}
			if !strings.HasPrefix(rendered, head.Render(" ")) {
				t.Errorf("%q selected=%v: the leading pad is not the run it abuts: %q", tag, selected, rendered)
			}
		}
	}
}

// TestFilterLabelFocusBoldsWithoutMovingACell is the traversal cue that replaced
// the thickened end caps (issue #227): bold on the body run, composing with
// hover's underline, and costing nothing in either axis (spec section 10.4.4).
func TestFilterLabelFocusBoldsWithoutMovingACell(t *testing.T) {
	styles := theme.New(true)
	for _, tag := range []string{"bug", "type::feature"} {
		for _, selected := range []bool{false, true} {
			rest := FilterLabel(styles, tag, theme.Canvas, selected, false, false)
			focused := FilterLabel(styles, tag, theme.Canvas, selected, true, false)
			both := FilterLabel(styles, tag, theme.Canvas, selected, true, true)
			if rest == focused {
				t.Fatalf("%q selected=%v: focus changed nothing", tag, selected)
			}
			if !strings.Contains(focused, "1;") {
				t.Errorf("%q selected=%v: the focused pill is not bold: %q", tag, selected, focused)
			}
			if both == focused || both == FilterLabel(styles, tag, theme.Canvas, selected, false, true) {
				t.Errorf("%q selected=%v: focus and hover did not compose", tag, selected)
			}
			for _, rendered := range []string{focused, both} {
				if ansi.Strip(rendered) != ansi.Strip(rest) {
					t.Errorf("%q selected=%v: a state changed the cells drawn: %q", tag, selected, rendered)
				}
			}
		}
	}
}

// fillRun is the tag's own wheel slot as the *fill* behind a lit pill's body
// text: the sequence that must be present when the pill is selected and absent
// when it is not. Since issue #227 the pill is that fill and its two padding
// cells and nothing else, so the withdrawal is readable on the body run alone.
func fillRun(styles *theme.Styles, tag string) string {
	const probe = "probe"
	fill := theme.LabelSlot(LabelWheel(tag))
	rendered := styles.ChipRuns(fill, theme.Canvas).Body.Render(probe)
	return rendered[:strings.Index(rendered, probe)]
}

// tintRun is the same wheel hue as the inactive pill writes it: foreground over
// the withdrawn Surface fill rather than a fill of its own. It is the sequence
// that has to survive for an unselected offer to stay matchable to its card.
func tintRun(styles *theme.Styles, tag string) string {
	const probe = "probe"
	fill := theme.LabelSlot(LabelWheel(tag))
	rendered := styles.ChipRunsTint(fill, theme.Canvas).Body.Render(probe)
	return rendered[:strings.Index(rendered, probe)]
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
