package widget

import (
	"sort"
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
			Priority(styles, 1, theme.Card, false),
			Chip(styles, ChipOpts{Text: "blocked", Fill: theme.StatusWarn, On: theme.Card, Flat: density.Compact()}),
		},
		Labels:     []string{"type::feature", "area"},
		Priority:   1,
		Width:      40,
		TitleLines: 2,
		DescLines:  descLines,
		LabelRows:  1,
		Density:    density,
	}
}

func TestCardIsEmptyWithoutWidth(t *testing.T) {
	styles := theme.New(true)
	if got := Card(styles, CardOpts{Title: "x"}); got != nil {
		t.Errorf("zero-width card rendered %v", got)
	}
}

// TestCardRowsFollowItsContentUnderTheCeiling is issue #243. The section 2.6
// ladder is a ceiling now, not an allotment: the card takes a title row, the
// rows its description fills, the meta row and the rows its pills wrap onto,
// and a description that fills one row of a two-row ceiling makes the card one
// row shorter rather than leaving a blank one under it.
func TestCardRowsFollowItsContentUnderTheCeiling(t *testing.T) {
	styles := theme.New(true)
	cases := []struct {
		name      string
		density   Density
		descLines int
		rows      int
	}{
		// title(1) + description + meta(1) + labels(1).
		{"one description row", DensityNormal, 1, 4},
		{"two description rows", DensityNormal, 2, 5},
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

// TestCardGivesBackTheDescriptionRowsItDidNotFill is the defect issue #243 was
// opened for: a description of one line under a ceiling of two used to leave the
// second row blank inside the card. It now gives the row back to the column, and
// the meta row sits directly under the prose.
func TestCardGivesBackTheDescriptionRowsItDidNotFill(t *testing.T) {
	styles := theme.New(true)
	opts := cardFixture(styles, DensityNormal, 2)
	opts.Desc = "short"
	rows := Card(styles, opts)
	if len(rows) != 4 {
		t.Fatalf("card rendered %d rows, want 4", len(rows))
	}
	if plain := ansi.Strip(rows[1]); !strings.Contains(plain, "short") {
		t.Errorf("row 1 = %q, want the one description row", plain)
	}
	if !strings.Contains(ansi.Strip(rows[2]), " 1 ") {
		t.Errorf("row 2 = %q, want the meta chip row directly under the description", ansi.Strip(rows[2]))
	}
}

// TestCardWithoutADescriptionDrawsNoDescriptionRow is the same rule at zero.
// An empty description is not a short one: the card carries no description row
// at all, and the prose block is the title alone.
func TestCardWithoutADescriptionDrawsNoDescriptionRow(t *testing.T) {
	styles := theme.New(true)
	opts := cardFixture(styles, DensityNormal, 5)
	opts.Desc = ""
	rows := Card(styles, opts)
	// title(1) + meta(1) + labels(1).
	if len(rows) != 3 {
		t.Fatalf("card rendered %d rows, want 3", len(rows))
	}
	if !strings.Contains(ansi.Strip(rows[1]), " 1 ") {
		t.Errorf("row 1 = %q, want the meta chip row directly under the title", ansi.Strip(rows[1]))
	}
}

// TestCardWithoutLabelsDropsTheSeparatorAboveThem is the section 3.1 separator
// rule as issue #243 re-cut it: an interior separator sits between two sections
// the card actually drew. With no labels there is nothing under the meta row for
// the second separator to separate it from, so it goes with them.
func TestCardWithoutLabelsDropsTheSeparatorAboveThem(t *testing.T) {
	styles := theme.New(true)
	opts := cardFixture(styles, DensityNormal, 1)
	opts.PadRows = 2
	opts.LabelRows = 2
	withLabels := Card(styles, opts)
	opts.Labels = nil
	bare := Card(styles, opts)
	// title(1) + description(1) + separator + meta(1) + separator + labels(1).
	if len(withLabels) != 6 {
		t.Fatalf("a labelled card rendered %d rows, want 6", len(withLabels))
	}
	// title(1) + description(1) + separator + meta(1): the label rows and the
	// separator that was holding them off the meta row both go.
	if len(bare) != 4 {
		t.Fatalf("a card with no labels rendered %d rows, want 4", len(bare))
	}
	if plain := strings.TrimSpace(ansi.Strip(bare[len(bare)-1])); plain == "▌" {
		t.Error("the last row of a card with no labels is blank; no separator may abut blank space")
	}
}

// TestCardKeepsTheProseSeparatorWithoutADescription is the other arm. The prose
// block is at least the title and the meta row is always drawn, so the first
// separator has two sections to sit between whatever the description did.
func TestCardKeepsTheProseSeparatorWithoutADescription(t *testing.T) {
	styles := theme.New(true)
	opts := cardFixture(styles, DensityNormal, 3)
	opts.Desc = ""
	opts.PadRows = 1
	rows := Card(styles, opts)
	// title(1) + separator + meta(1) + labels(1).
	if len(rows) != 4 {
		t.Fatalf("card rendered %d rows, want 4", len(rows))
	}
	if plain := strings.TrimSpace(ansi.Strip(rows[1])); plain != "▌" {
		t.Errorf("row 1 = %q, want the separator between the title and the meta row", plain)
	}
	if !strings.Contains(ansi.Strip(rows[2]), " 1 ") {
		t.Errorf("row 2 = %q, want the meta chip row", ansi.Strip(rows[2]))
	}
}

// TestCardTitleWrapsInsteadOfEllipsizingToOneLine is issue #232's first item.
// The title used to be cut to the first row's field whatever the card's height
// budget was; it now wraps across its whole allotment, and only the last
// allotted row carries the ellipsis. The sequence number keeps the right end of
// the first row and is still never truncated.
func TestCardTitleWrapsInsteadOfEllipsizingToOneLine(t *testing.T) {
	styles := theme.New(true)
	opts := cardFixture(styles, DensityNormal, 1)
	opts.Width = 24
	rows := Card(styles, opts)
	first := ansi.Strip(rows[0])
	if !strings.HasSuffix(strings.TrimRight(first, " "), "#142") {
		t.Errorf("title row = %q, want the sequence right-aligned and never truncated", first)
	}
	if strings.Contains(first, "…") {
		t.Errorf("title row = %q, the first row wraps rather than ellipsizing", first)
	}
	if second := ansi.Strip(rows[1]); !strings.Contains(second, "resize") {
		t.Errorf("second title row = %q, want the rest of the title on it", second)
	}
	for index, row := range rows[:2] {
		if got := ansi.StringWidth(row); got != 24 {
			t.Errorf("title row %d is %d cells, want 24", index, got)
		}
	}
}

// TestCardTitleEllipsizesOnItsLastAllottedRow is the other arm: a title too
// long for the whole allotment still cannot run past the card.
func TestCardTitleEllipsizesOnItsLastAllottedRow(t *testing.T) {
	styles := theme.New(true)
	opts := cardFixture(styles, DensityNormal, 1)
	opts.Width = 20
	opts.Title = "a title far too long to fit inside two narrow rows of a board card"
	rows := Card(styles, opts)
	if got := ansi.Strip(rows[0]); strings.Contains(got, "…") {
		t.Errorf("first title row = %q, only the last allotted row carries the ellipsis", got)
	}
	if got := ansi.Strip(rows[1]); !strings.Contains(got, "…") {
		t.Errorf("last title row = %q, want the ellipsis", got)
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
	if !strings.Contains(merged, "1") || !strings.Contains(merged, "feature") {
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
	// Issue #243: a card with no room for content is one row of rail and
	// surface. There is no grid left to hold open, and every row under the first
	// would be blank space the reader cannot tell from the gutter.
	if len(rows) != 1 {
		t.Fatalf("card rendered %d rows, want 1", len(rows))
	}
	for index, row := range rows {
		if plain := strings.TrimSpace(ansi.Strip(row)); plain != "▌" {
			t.Errorf("row %d = %q, want the rail and blank surface only", index, plain)
		}
		if got := ansi.StringWidth(row); got != opts.Width {
			t.Errorf("row %d is %d cells, want %d", index, got, opts.Width)
		}
	}
}

func TestCardWithoutLabelsOrMeta(t *testing.T) {
	styles := theme.New(true)
	rows := Card(styles, CardOpts{Title: "bare", Width: 20, TitleLines: 2, DescLines: 1, LabelRows: 1})
	// title(1) + meta(1): no description, no labels, nothing to hold open.
	if len(rows) != 2 {
		t.Fatalf("card rendered %d rows, want 2", len(rows))
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
	got := wrapFields(styles, "one two three four", []int{9, 9})
	if len(got) != 2 {
		t.Fatalf("wrapFields returned %d lines, want 2", len(got))
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
	got := wrapFields(styles, "supercalifragilistic tail", []int{8, 8})
	if !strings.HasSuffix(got[0], "…") {
		t.Errorf("first line = %q, want a hard-truncated word", got[0])
	}
	if ansi.StringWidth(got[0]) > 8 {
		t.Errorf("first line is %d cells, want at most 8", ansi.StringWidth(got[0]))
	}
}

func TestWrapLeavesUnusedLinesBlank(t *testing.T) {
	styles := theme.New(true)
	got := wrapFields(styles, "one", []int{20, 20, 20})
	if got[0] != "one" || got[1] != "" || got[2] != "" {
		t.Errorf("wrapFields = %q, want the remaining rows blank", got)
	}
}

func TestWrapWithoutAFieldReturnsBlankLines(t *testing.T) {
	styles := theme.New(true)
	got := wrapFields(styles, "anything", []int{0, 0})
	if len(got) != 2 || got[0] != "" || got[1] != "" {
		t.Errorf("wrapFields = %q, want two blank lines", got)
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

// TestCardTitleSplitsItsEmojiOffTheTitle is the title arm of issue #229. The
// card's emoji is a wide pictograph and the row ends in a padding run, so a title
// left inside the emoji's run was drawn pushed right by the pictograph's excess
// advance and lost its last character under that padding. The emoji and the
// column beside it are their own run.
func TestCardTitleSplitsItsEmojiOffTheTitle(t *testing.T) {
	styles := theme.New(true)
	opts := cardFixture(styles, DensityNormal, 1)
	opts.Emoji = styles.Glyph.Blocked
	opts.Seq = ""
	row := cardTitle(styles, opts, theme.Card, 40, planCard(styles, opts))[0]
	segments := markRunSegments(row)
	if len(segments) == 0 || segments[0] != opts.Emoji+" " {
		t.Fatalf("title runs = %q, want the emoji and its column first", segments)
	}
	if len(segments) < 2 || !strings.HasPrefix(segments[1], opts.Title) {
		t.Errorf("title runs = %q, want the title in a run of its own", segments)
	}
	if got, want := ansi.StringWidth(ansi.Strip(row)), 40; got != want {
		t.Errorf("title row is %d cells, want %d", got, want)
	}
}

// TestCardTitleWithoutAWideEmojiKeepsOneRun is the other arm: a title with no
// emoji, or with a one-cell one, has no pictograph advance to escape.
func TestCardTitleWithoutAWideEmojiKeepsOneRun(t *testing.T) {
	styles := theme.New(true)
	opts := cardFixture(styles, DensityNormal, 1)
	opts.Seq = ""
	for _, emoji := range []string{"", "*"} {
		opts.Emoji = emoji
		head := opts.Title
		if emoji != "" {
			head = emoji + " " + opts.Title
		}
		row := cardTitle(styles, opts, theme.Card, 40, planCard(styles, opts))[0]
		if segments := markRunSegments(row); len(segments) == 0 || !strings.HasPrefix(segments[0], head) {
			t.Errorf("emoji %q: title runs = %q, want the head in one run", emoji, segments)
		}
	}
}

// TestCardLabelsWrapOntoTheSecondRow is issue #232's sixth item. Labels own
// rows of their own below the meta line and wrap onto the next when one row
// does not hold them, instead of the row silently dropping everything past the
// first that did not fit.
func TestCardLabelsWrapOntoTheSecondRow(t *testing.T) {
	styles := theme.New(true)
	opts := cardFixture(styles, DensityNormal, 1)
	opts.Width = 28
	opts.LabelRows = 2
	opts.Labels = []string{"alpha", "bravo", "charlie"}
	rows, spans := CardWithSpans(styles, opts)
	if len(spans) != 3 {
		t.Fatalf("card recorded %d label spans, want 3: %+v", len(spans), spans)
	}
	first, second := spans[0].Row, spans[len(spans)-1].Row
	if first == second {
		t.Fatalf("every label landed on row %d; the row cannot have held them all", first)
	}
	if second != first+1 {
		t.Errorf("label rows = %d and %d, want consecutive rows", first, second)
	}
	for index, row := range rows {
		if got := ansi.StringWidth(row); got != opts.Width {
			t.Errorf("row %d is %d cells, want %d", index, got, opts.Width)
		}
	}
}

// TestCardLabelGutterIsOneCellEverywhere is what "equally spaced" resolves to on
// a cell grid. A fixed gutter makes every gap the same cell whatever the row
// holds, so a label that changes width moves the pills after it and nothing
// else; distributing slack instead would put the same label at a different
// column on two cards and move every gap whenever any pill changed.
func TestCardLabelGutterIsOneCellEverywhere(t *testing.T) {
	styles := theme.New(true)
	opts := cardFixture(styles, DensityNormal, 1)
	opts.Width = 60
	opts.LabelRows = 1
	opts.Labels = []string{"a", "longer", "mid"}
	_, spans := CardWithSpans(styles, opts)
	if len(spans) != 3 {
		t.Fatalf("card recorded %d label spans, want 3", len(spans))
	}
	for index := 1; index < len(spans); index++ {
		if gap := spans[index].X0 - spans[index-1].X1; gap != 1 {
			t.Errorf("gap between label %d and %d is %d cells, want 1", index-1, index, gap)
		}
	}
}

// TestCardLabelsDropWhatTheAllotmentCannotHold pins the other end of the wrap:
// the order is the caller's survival order, and a pill still unplaced when the
// rows run out is dropped rather than truncated into an unreadable stub. So is
// a pill too wide for a row of its own.
func TestCardLabelsDropWhatTheAllotmentCannotHold(t *testing.T) {
	styles := theme.New(true)
	opts := cardFixture(styles, DensityNormal, 1)
	opts.Width = 18
	opts.LabelRows = 1
	opts.Labels = []string{"keep", "alsokeptmaybe", "dropped"}
	_, spans := CardWithSpans(styles, opts)
	if len(spans) == 0 || spans[0].Index != 0 {
		t.Fatalf("the first label did not survive: %+v", spans)
	}
	if len(spans) == len(opts.Labels) {
		t.Fatalf("every label fit a %d-cell card; the case asserts nothing", opts.Width)
	}
	for _, span := range spans {
		if span.Row != spans[0].Row {
			t.Errorf("span %+v landed past the single allotted label row", span)
		}
	}
}

// TestCardDescriptionRendersTheParityGrammar is issue #232's second item. The
// card draws the frozen markdown grammar at card scale rather than the raw
// source: the syntax that asked for emphasis never reaches a cell, a bullet
// gets the vocabulary's own marker, and an ordinal keeps the author's number.
func TestCardDescriptionRendersTheParityGrammar(t *testing.T) {
	styles := theme.New(true)
	opts := cardFixture(styles, DensityNormal, 4)
	opts.Width = 40
	opts.TitleLines = 1
	opts.Desc = "## Plan\n**bold** and *slant*\n- first\n7. seventh"
	rows := Card(styles, opts)
	body := ansi.Strip(strings.Join(rows[1:5], "\n"))
	for _, want := range []string{"Plan", "bold and slant", styles.Glyph.Bullet + " first", "7. seventh"} {
		if !strings.Contains(body, want) {
			t.Errorf("description missing %q:\n%s", want, body)
		}
	}
	for _, reject := range []string{"##", "**", "*slant*", "- first"} {
		if strings.Contains(body, reject) {
			t.Errorf("description leaked the markup %q:\n%s", reject, body)
		}
	}
	// Emphasis is an attribute or a foreground and never a cell, so the rows are
	// exactly as wide as the card whatever the source carried.
	for index, row := range rows {
		if got := ansi.StringWidth(row); got != opts.Width {
			t.Errorf("row %d is %d cells, want %d", index, got, opts.Width)
		}
	}
}

// TestCardDescriptionEllipsizesOnItsLastAllottedRow keeps the section 3.3 rule
// through the rewrite: the wrap can never run past the card, whatever the
// grammar found in the source.
func TestCardDescriptionEllipsizesOnItsLastAllottedRow(t *testing.T) {
	styles := theme.New(true)
	opts := cardFixture(styles, DensityNormal, 1)
	opts.Width = 24
	opts.TitleLines = 1
	opts.Desc = strings.Repeat("wordy ", 40)
	rows := Card(styles, opts)
	if got := ansi.Strip(rows[1]); !strings.Contains(got, "…") {
		t.Errorf("last description row = %q, want the ellipsis", got)
	}
	if got := ansi.StringWidth(rows[1]); got != opts.Width {
		t.Errorf("last description row is %d cells, want %d", got, opts.Width)
	}
}

// TestCardTitleWrapCostsItsOwnRowAndNothingElse retires the shared block issue
// #232 introduced and issue #243 made unnecessary. The title used to hand a
// spare row to the description so the card's height could stay content-blind;
// with the height content-sized there is no spare row to hand over - a one-line
// title simply makes the card one row shorter, and the description takes the
// same rows either way.
func TestCardTitleWrapCostsItsOwnRowAndNothingElse(t *testing.T) {
	styles := theme.New(true)
	short := cardFixture(styles, DensityNormal, 2)
	short.Width = 40
	short.Title = "short"
	short.Desc = "one two three four five six seven eight nine ten eleven twelve"
	long := short
	long.Title = "a title long enough that it must wrap onto its second allotted row"

	shortRows, longRows := Card(styles, short), Card(styles, long)
	if len(longRows) != len(shortRows)+1 {
		t.Fatalf("card heights are %d and %d rows; the wrapped title must cost exactly its own row",
			len(shortRows), len(longRows))
	}
	// title(1) + description(2) + meta(1) + labels(1).
	if got := len(shortRows); got != 5 {
		t.Fatalf("the one-line card rendered %d rows, want 5", got)
	}
	// The description is the same two rows on both: its ceiling is its own, and
	// the title's spare row goes back to the column rather than to it.
	shortBody := ansi.Strip(strings.Join(shortRows[1:3], ""))
	longBody := ansi.Strip(strings.Join(longRows[2:4], ""))
	if strings.TrimSpace(shortBody) != strings.TrimSpace(longBody) {
		t.Errorf("the two cards drew different descriptions:\n%q\n%q", shortBody, longBody)
	}
	for name, rows := range map[string][]string{"short title": shortRows, "wrapped title": longRows} {
		if got := ansi.Strip(rows[len(rows)-2]); !strings.Contains(got, " 1 ") {
			t.Errorf("%s: the meta row is not directly above the labels: %q", name, got)
		}
	}
}

// TestCardInteriorPaddingSitsOnTheSectionBoundaries is issue #240's rhythm. One
// blank row separates the shared title/description block from the meta row and
// a second separates the meta row from the label rows, so the card reads as
// prose, then data, then navigation rather than as one packed slab of text.
func TestCardInteriorPaddingSitsOnTheSectionBoundaries(t *testing.T) {
	styles := theme.New(true)
	opts := cardFixture(styles, DensityNormal, 2)
	opts.Width = 40
	opts.Title = "short"
	opts.Desc = "one two three four five six seven eight nine ten"
	opts.LabelRows = 2
	opts.PadRows = 2
	rows := Card(styles, opts)
	// title(1) + description(2) + pad + meta + pad + labels(1).
	if got := len(rows); got != 7 {
		t.Fatalf("card rendered %d rows, want 7", got)
	}
	for _, row := range []int{3, 5} {
		if plain := strings.TrimSpace(ansi.Strip(rows[row])); plain != "▌" {
			t.Errorf("row %d = %q, want a blank interior separator carrying the rail only", row, plain)
		}
	}
	if got := ansi.Strip(rows[4]); !strings.Contains(got, " 1 ") {
		t.Errorf("row 4 = %q, want the meta chip row between the two separators", got)
	}
	if got := ansi.Strip(rows[6]); !strings.Contains(got, "feature") {
		t.Errorf("row 6 = %q, want the first label row under the second separator", got)
	}
}

// TestCardInteriorPaddingIsCardSurface is the constraint that keeps the card one
// slab: a separator row is blank content on the card's own fill, not a gap in
// it. It takes the surface every other row of the card takes, so selection,
// hover and the zebra stripe reach it without the row knowing they exist.
func TestCardInteriorPaddingIsCardSurface(t *testing.T) {
	styles := theme.New(true)
	base := cardFixture(styles, DensityNormal, 1)
	base.Width = 40
	base.PadRows = 2
	base.LabelRows = 1
	grounds := map[string]string{}
	for _, testCase := range []struct {
		name     string
		selected bool
		alt      bool
	}{
		{"plain", false, false},
		{"selected", true, false},
		{"striped", false, true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			opts := base
			opts.Selected, opts.Alt = testCase.selected, testCase.alt
			rows := Card(styles, opts)
			surface := styles.Surface(opts.Selected, opts.Alt)
			want := rowBackgrounds(styles.On(theme.FgBase, surface).Render(" "))
			// The separators are rows 2 and 4, either side of the meta row. Each
			// paints one ground and it is the card's, so the row highlights and
			// stripes with everything else on the card.
			for _, row := range []int{2, 4} {
				if got := rowBackgrounds(rows[row]); got != want {
					t.Errorf("separator row %d ground = %q, want the card's own %q", row, got, want)
				}
			}
			grounds[testCase.name] = want
		})
	}
	if grounds["plain"] == grounds["selected"] || grounds["plain"] == grounds["striped"] {
		t.Errorf("the card's three grounds are not distinct: %v; the case asserts nothing", grounds)
	}
}

// rowBackgrounds is the sorted set of background colors a rendered row paints.
// Two rows of the same card must agree on it: the depth model is background
// color, so a row that painted a ground the rest of the card did not is a row
// that left the card.
//
// The walk follows the SGR parameter grammar rather than matching substrings,
// because an extended color's own components are parameters too and a naive
// scan would read a blue channel of 48 as a background introducer.
func rowBackgrounds(row string) string {
	seen := map[string]bool{}
	for _, segment := range strings.Split(row, "\x1b[")[1:] {
		body, _, found := strings.Cut(segment, "m")
		if !found {
			continue
		}
		params := strings.Split(body, ";")
		for index := 0; index < len(params); index++ {
			switch params[index] {
			case "38", "48":
				span := 0
				if index+1 < len(params) {
					switch params[index+1] {
					case "5":
						span = 2
					case "2":
						span = 4
					}
				}
				if span == 0 || index+span >= len(params) {
					index = len(params)
					break
				}
				if params[index] == "48" {
					seen[strings.Join(params[index+1:index+span+1], ";")] = true
				}
				index += span
			case "49":
				seen["default"] = true
			}
		}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

// TestCardCompactCarriesNoInteriorPadding is the other half of issue #240:
// compact exists to be dense (section 2.6), so it ignores the rhythm outright
// even when a caller hands it one. Two rows, edge to edge, as before.
func TestCardCompactCarriesNoInteriorPadding(t *testing.T) {
	styles := theme.New(true)
	opts := cardFixture(styles, DensityCompact, 0)
	opts.PadRows = 2
	rows := Card(styles, opts)
	if got := len(rows); got != 2 {
		t.Fatalf("compact card rendered %d rows, want 2", got)
	}
	for index, row := range rows {
		if plain := strings.TrimSpace(ansi.Strip(row)); plain == "▌" || plain == "" {
			t.Errorf("compact row %d is blank: %q", index, plain)
		}
	}
}

// TestCardLabelSpansClearTheInteriorPadding keeps the pointer honest about the
// rhythm. The label rows moved down by the separator between them and the meta
// row, and a hit region resolved against the old offset would put the filter
// click on a blank row.
func TestCardLabelSpansClearTheInteriorPadding(t *testing.T) {
	styles := theme.New(true)
	opts := cardFixture(styles, DensityNormal, 1)
	opts.Width = 40
	opts.LabelRows = 1
	opts.Labels = []string{"alpha"}
	for pads := range 3 {
		opts.PadRows = pads
		rows, spans := CardWithSpans(styles, opts)
		if len(spans) != 1 {
			t.Fatalf("pads %d: card recorded %d label spans, want 1", pads, len(spans))
		}
		span := spans[0]
		if span.Row < 0 || span.Row >= len(rows) {
			t.Fatalf("pads %d: span row %d is outside the card's %d rows", pads, span.Row, len(rows))
		}
		if got := ansi.Strip(rows[span.Row]); !strings.Contains(got, "alpha") {
			t.Errorf("pads %d: span points at row %d = %q, want the row carrying the pill", pads, span.Row, got)
		}
	}
}

// cardShapes is the content matrix the measure invariant runs over. Card height
// is a function of content now, so the matrix that proves measure and render
// agree has to vary content rather than frame size: every branch of the plan -
// an empty section, a section that exactly fills its ceiling, a section that
// overruns it, a word too wide for the field, a source line break - is a way
// the two could come apart.
func cardShapes(styles *theme.Styles) []CardOpts {
	titles := []string{
		"",
		"t",
		"Drag ghost sticks on resize",
		"a title long enough that it must wrap onto its second row and then run well past the end of it",
		"supercalifragilisticexpialidociousandthensomemore",
	}
	descs := []string{
		"",
		"short",
		"one two three four five six seven eight nine ten eleven twelve thirteen",
		"first source line\nsecond source line\n\nfourth after a blank",
		"## Heading\n- bullet one\n7. seventh\n`code` and [link](http://example.com)",
		strings.Repeat("wordy ", 80),
		"antidisestablishmentarianismandthensomemorelettersbesides",
	}
	labels := [][]string{
		nil,
		{"one"},
		{"type::feature", "area", "urgent", "team::platform", "needs::review"},
		{"averyverylongsinglelabelnorowcouldeverhold"},
	}
	var out []CardOpts
	for _, density := range []Density{DensityNormal, DensityCompact} {
		for _, width := range []int{4, 8, 18, 24, 40, 72} {
			for _, ceiling := range [][4]int{{1, 0, 0, 1}, {1, 1, 1, 1}, {2, 2, 1, 2}, {2, 5, 2, 2}} {
				for _, title := range titles {
					for _, desc := range descs {
						for _, tags := range labels {
							out = append(out, CardOpts{
								Title: title, Emoji: "*", Seq: "#142", Desc: desc,
								Meta:       []string{Priority(styles, 1, theme.Card, density.Compact())},
								Labels:     tags,
								Priority:   1,
								Blocked:    len(desc)%2 == 0,
								Width:      width,
								TitleLines: ceiling[0], DescLines: ceiling[1],
								PadRows: ceiling[2], LabelRows: ceiling[3],
								Density: density,
							})
						}
					}
				}
			}
		}
	}
	return out
}

// TestCardHeightMatchesTheRowsItDraws is the invariant issue #243 put in place
// of the CardRows reservation. Content-independence is gone; determinism is not.
// The column packs against CardHeight and the card then draws Card, and the two
// are the same plan, so a card can never draw a row the column did not pack.
func TestCardHeightMatchesTheRowsItDraws(t *testing.T) {
	styles := theme.New(true)
	for _, opts := range cardShapes(styles) {
		measured, drawn := CardHeight(styles, opts), len(Card(styles, opts))
		if measured != drawn {
			t.Fatalf("width %d, density %v, title %q, desc %q, labels %v: measured %d rows, drew %d",
				opts.Width, opts.Density, opts.Title, opts.Desc, opts.Labels, measured, drawn)
		}
	}
}

// TestCardNeverExceedsItsCeiling is the other half: the section 2.6 ladder is a
// ceiling rather than an allotment, so a card takes less than it than it used to
// and never more. The sum here is theme.Metrics.CardRows written out in the
// caller's terms - every section at its cap, plus the two interior separators.
func TestCardNeverExceedsItsCeiling(t *testing.T) {
	styles := theme.New(true)
	for _, opts := range cardShapes(styles) {
		ceiling := opts.TitleLines + opts.DescLines + opts.PadRows + 1 + opts.LabelRows
		if opts.Density.Compact() {
			ceiling = 2
		}
		if got := CardHeight(styles, opts); got > ceiling {
			t.Fatalf("width %d, density %v, title %q, desc %q: card is %d rows, ceiling is %d",
				opts.Width, opts.Density, opts.Title, opts.Desc, got, ceiling)
		}
	}
}

// TestCardRowsAreAlwaysTheFrameWidth is the composition rule every golden pins
// at board scale, asserted here over the whole content matrix: a card is a
// filled slab, and a row short of the width would punch a hole in the panel it
// sits on.
func TestCardRowsAreAlwaysTheFrameWidth(t *testing.T) {
	styles := theme.New(true)
	for _, opts := range cardShapes(styles) {
		for index, row := range Card(styles, opts) {
			if got := ansi.StringWidth(row); got != opts.Width {
				t.Fatalf("width %d, density %v, title %q, desc %q: row %d is %d cells",
					opts.Width, opts.Density, opts.Title, opts.Desc, index, got)
			}
		}
	}
}
