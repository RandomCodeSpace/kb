package widget

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

func cardFixture(styles *theme.Styles, density Density, descLines int) CardOpts {
	return CardOpts{
		Title: "Drag ghost sticks on resize",
		Emoji: "*",
		Seq:   "#142",
		Desc:  "Pointer capture leaks when the column scrolls under the cursor.",
		Meta: []string{
			Priority(styles, 1, theme.Card),
			Chip(styles, ChipOpts{Text: "blocked", Fill: theme.StatusWarn, On: theme.Card, Flat: density.Compact()}),
		},
		Labels:    []string{"type::feature", "area"},
		Priority:  1,
		Width:     40,
		DescLines: descLines,
		Density:   density,
	}
}

func TestCardIsEmptyWithoutWidth(t *testing.T) {
	styles := theme.New(true)
	if got := Card(styles, CardOpts{Title: "x"}); got != nil {
		t.Errorf("zero-width card rendered %v", got)
	}
}

func TestCardRowGridIsFixedByDensity(t *testing.T) {
	styles := theme.New(true)
	cases := []struct {
		name      string
		density   Density
		descLines int
		rows      int
	}{
		{"normal", DensityNormal, 1, 4},
		{"tall", DensityNormal, 2, 5},
		{"compact", DensityCompact, 2, 2},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			rows := Card(styles, cardFixture(styles, testCase.density, testCase.descLines))
			if len(rows) != testCase.rows {
				t.Fatalf("card rendered %d rows, want %d", len(rows), testCase.rows)
			}
			for index, row := range rows {
				if got := ansi.StringWidth(row); got != 40 {
					t.Errorf("row %d is %d cells, want 40", index, got)
				}
			}
		})
	}
}

func TestCardKeepsTheChipRowsPinnedForAShortDescription(t *testing.T) {
	styles := theme.New(true)
	opts := cardFixture(styles, DensityNormal, 2)
	opts.Desc = "short"
	rows := Card(styles, opts)
	if len(rows) != 5 {
		t.Fatalf("card rendered %d rows, want 5", len(rows))
	}
	if plain := strings.TrimSpace(ansi.Strip(rows[2])); plain != "▌" {
		t.Errorf("row 2 = %q, the unused description row must stay blank", plain)
	}
	if !strings.Contains(ansi.Strip(rows[3]), "P1") {
		t.Errorf("row 3 = %q, want the meta chip row", ansi.Strip(rows[3]))
	}
}

func TestCardTitleRowKeepsTheSequenceNumber(t *testing.T) {
	styles := theme.New(true)
	opts := cardFixture(styles, DensityNormal, 1)
	opts.Width = 24
	title := ansi.Strip(Card(styles, opts)[0])
	if !strings.HasSuffix(strings.TrimRight(title, " "), "#142") {
		t.Errorf("title row = %q, want the sequence right-aligned and never truncated", title)
	}
	if !strings.Contains(title, "…") {
		t.Errorf("title row = %q, want the title ellipsized to fit", title)
	}
	if got := ansi.StringWidth(Card(styles, opts)[0]); got != 24 {
		t.Errorf("title row is %d cells, want 24", got)
	}
}

func TestCardTitleRowWhenTheSequenceFillsTheField(t *testing.T) {
	styles := theme.New(true)
	opts := cardFixture(styles, DensityNormal, 1)
	opts.Width = 10
	opts.Seq = "#1234567"
	rows := Card(styles, opts)
	if got := ansi.StringWidth(rows[0]); got != 10 {
		t.Errorf("title row is %d cells, want 10", got)
	}
	plain := ansi.Strip(rows[0])
	if !strings.Contains(plain, "#12345") {
		t.Errorf("title row = %q, the title field collapses so the sequence survives", plain)
	}
	if strings.Contains(plain, "Drag") {
		t.Errorf("title row = %q, the title yields the whole field to the sequence", plain)
	}
}

func TestCardTitleRowWithoutASequence(t *testing.T) {
	styles := theme.New(true)
	opts := cardFixture(styles, DensityNormal, 1)
	opts.Seq = ""
	opts.Emoji = ""
	opts.Title = "short"
	rows := Card(styles, opts)
	if plain := ansi.Strip(rows[0]); !strings.HasPrefix(plain, "▌ short") {
		t.Errorf("title row = %q", plain)
	}
	if got := ansi.StringWidth(rows[0]); got != 40 {
		t.Errorf("title row is %d cells, want 40", got)
	}
}

func TestCardSelectionThickensTheRailAndKeepsThePriorityHue(t *testing.T) {
	styles := theme.New(true)
	resting := Card(styles, cardFixture(styles, DensityNormal, 1))
	opts := cardFixture(styles, DensityNormal, 1)
	opts.Selected = true
	selected := Card(styles, opts)
	if !strings.HasPrefix(ansi.Strip(selected[0]), "█") {
		t.Errorf("selected rail = %q, want the full block", ansi.Strip(selected[0]))
	}
	if !strings.HasPrefix(ansi.Strip(resting[0]), "▌") {
		t.Errorf("resting rail = %q, want the half block", ansi.Strip(resting[0]))
	}
	hue := "38;2;255;90;72"
	if !strings.Contains(selected[0], hue) {
		t.Error("the selected rail must keep the P1 hue")
	}
}

func TestCardCompactMergesLabelsOntoTheChipRow(t *testing.T) {
	styles := theme.New(true)
	rows := Card(styles, cardFixture(styles, DensityCompact, 0))
	merged := ansi.Strip(rows[1])
	if !strings.Contains(merged, "P1") || !strings.Contains(merged, "feature") {
		t.Errorf("compact chip row = %q, want the meta chips and the labels merged", merged)
	}
	// Spec section 2.6 step 7: the compact chip is flat colored bold text, so it
	// drops the padding cell the normal-density pill spends at each end (issue
	// #227 replaced the end caps with that padding and left the drop step alone).
	if !strings.Contains(rows[1], Label(styles, "type::feature", theme.Card, true, false)) {
		t.Errorf("compact chip row = %q, want the flat label run", merged)
	}
	if strings.Contains(rows[1], Label(styles, "type::feature", theme.Card, false, false)) {
		t.Errorf("compact chip row = %q, the pill padding is dropped", merged)
	}
}

func TestCardAlternateTierIsUsedForStriping(t *testing.T) {
	styles := theme.New(true)
	opts := cardFixture(styles, DensityCompact, 0)
	opts.Alt = true
	striped := Card(styles, opts)
	plain := Card(styles, cardFixture(styles, DensityCompact, 0))
	if striped[0] == plain[0] {
		t.Error("the alternating tier must render differently from the resting one")
	}
}

func TestCardTooNarrowRendersSurfaceAndRailOnly(t *testing.T) {
	styles := theme.New(true)
	opts := cardFixture(styles, DensityNormal, 1)
	opts.Width = 8
	rows := Card(styles, opts)
	if len(rows) != 4 {
		t.Fatalf("card rendered %d rows, want 4", len(rows))
	}
	for index, row := range rows {
		if plain := strings.TrimSpace(ansi.Strip(row)); plain != "▌" {
			t.Errorf("row %d = %q, want the rail and blank surface only", index, plain)
		}
	}
}

func TestCardWithoutLabelsOrMeta(t *testing.T) {
	styles := theme.New(true)
	rows := Card(styles, CardOpts{Title: "bare", Width: 20, DescLines: 1})
	if len(rows) != 4 {
		t.Fatalf("card rendered %d rows, want 4", len(rows))
	}
	for _, row := range rows {
		if got := ansi.StringWidth(row); got != 20 {
			t.Errorf("row is %d cells, want 20", got)
		}
	}
}

// TestCardLabelPillsAreComposedOnTheCardsOwnGround is the ground half of issue
// #219. A pill's end caps carry the hue as foreground over the ground behind
// them, so the surface a pill is composed against has to be the surface it
// actually lands on: compose it against Card while the card is striped or
// selected and each end cap paints one cell of the wrong tier, which is a seam
// the eye reads as a bar. The card wears three tiers - Card, Zebra when striped
// at compact density, Raised when selected - so this pins the pill the card
// draws to the pill the card's own resolved surface draws.
func TestCardLabelPillsAreComposedOnTheCardsOwnGround(t *testing.T) {
	styles := theme.New(true)
	cases := []struct {
		name     string
		density  Density
		selected bool
		alt      bool
		hovered  bool
	}{
		{"resting card", theme.DensityNormal, false, false, false},
		{"selected card", theme.DensityNormal, true, false, false},
		{"hovered card", theme.DensityNormal, false, false, true},
		{"striped compact card", theme.DensityCompact, false, true, false},
		{"selected compact card", theme.DensityCompact, true, true, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			opts := cardFixture(styles, testCase.density, 1)
			opts.Selected, opts.Alt, opts.Hovered = testCase.selected, testCase.alt, testCase.hovered
			rendered := strings.Join(Card(styles, opts), "\n")
			ground := styles.Surface(testCase.selected, testCase.alt)
			for _, tag := range opts.Labels {
				pill := Label(styles, tag, ground, testCase.density.Compact(), false)
				if !strings.Contains(rendered, pill) {
					t.Errorf("the %q pill is not composed on the card's own ground %d", tag, ground)
				}
			}
		})
	}
}

func TestWrapGreedilyFillsEachLine(t *testing.T) {
	styles := theme.New(true)
	got := wrap(styles, "one two three four", 9, 2)
	if len(got) != 2 {
		t.Fatalf("wrap returned %d lines, want 2", len(got))
	}
	if got[0] != "one two" {
		t.Errorf("first line = %q, want %q", got[0], "one two")
	}
	if !strings.Contains(got[1], "…") {
		t.Errorf("last line = %q, want the ellipsis for the remaining text", got[1])
	}
}

func TestWrapHardTruncatesAWordLongerThanTheField(t *testing.T) {
	styles := theme.New(true)
	got := wrap(styles, "supercalifragilistic tail", 8, 2)
	if !strings.HasSuffix(got[0], "…") {
		t.Errorf("first line = %q, want a hard-truncated word", got[0])
	}
	if ansi.StringWidth(got[0]) > 8 {
		t.Errorf("first line is %d cells, want at most 8", ansi.StringWidth(got[0]))
	}
}

func TestWrapLeavesUnusedLinesBlank(t *testing.T) {
	styles := theme.New(true)
	got := wrap(styles, "one", 20, 3)
	if got[0] != "one" || got[1] != "" || got[2] != "" {
		t.Errorf("wrap = %q, want the remaining rows blank", got)
	}
}

func TestWrapWithoutAFieldReturnsBlankLines(t *testing.T) {
	styles := theme.New(true)
	got := wrap(styles, "anything", 0, 2)
	if len(got) != 2 || got[0] != "" || got[1] != "" {
		t.Errorf("wrap = %q, want two blank lines", got)
	}
}

func TestClipTrimsOverrunContent(t *testing.T) {
	if got := clip("abcdef", 3); got != "abc" {
		t.Errorf("clip = %q, want abc", got)
	}
	if got := clip("ab", 5); got != "ab" {
		t.Errorf("clip rewrote fitting content to %q", got)
	}
}
