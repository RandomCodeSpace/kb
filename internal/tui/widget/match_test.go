package widget

import (
	"reflect"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// TestMatchRunsCoalescesContiguousHits is the reason the helper exists: a fuzzy
// hit on a word is one offset per rune, and three abutting ranges would emit
// three redundant style runs for one word.
func TestMatchRunsCoalescesContiguousHits(t *testing.T) {
	for _, test := range []struct {
		name    string
		text    string
		matched []int
		want    [][2]int
	}{
		{name: "no hits", text: "select card", matched: nil, want: nil},
		{name: "empty text", text: "", matched: []int{0}, want: nil},
		{name: "leading word", text: "select card", matched: []int{0, 1, 2}, want: [][2]int{{0, 3}}},
		{name: "two runs", text: "select card", matched: []int{0, 7, 8}, want: [][2]int{{0, 1}, {7, 9}}},
		{name: "whole string", text: "ship", matched: []int{0, 1, 2, 3}, want: [][2]int{{0, 4}}},
		{name: "single hit", text: "quit", matched: []int{3}, want: [][2]int{{3, 4}}},
		{name: "out of range", text: "quit", matched: []int{9, 40}, want: nil},
		{name: "unsorted", text: "quit", matched: []int{3, 0}, want: [][2]int{{0, 1}, {3, 4}}},
		{name: "repeats", text: "quit", matched: []int{1, 1, 2}, want: [][2]int{{1, 3}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := MatchRuns(test.text, test.matched); !reflect.DeepEqual(got, test.want) {
				t.Errorf("MatchRuns(%q, %v) = %v, want %v", test.text, test.matched, got, test.want)
			}
		})
	}
}

// TestMatchRunsMapsByteOffsetsOntoDisplayColumns is the conversion the whole
// helper exists for. sahilm/fuzzy reports byte offsets; lipgloss.StyleRanges
// cuts by display cell. A match on a multi-byte or double-width rune would
// otherwise be styled at the wrong columns, and this pins the mapping so a
// dependency bump that changed the offset convention fails here rather than in
// a golden nobody reads closely.
func TestMatchRunsMapsByteOffsetsOntoDisplayColumns(t *testing.T) {
	// U+65E5 and U+672C are three bytes each and two cells each.
	const text = "\u65e5\u672c card" // two three-byte, two-cell runes
	for _, test := range []struct {
		name    string
		matched []int
		want    [][2]int
	}{
		{name: "first wide rune", matched: []int{0}, want: [][2]int{{0, 2}}},
		{name: "second wide rune", matched: []int{3}, want: [][2]int{{2, 4}}},
		{name: "both wide runes", matched: []int{0, 3}, want: [][2]int{{0, 4}}},
		{name: "past the wide runes", matched: []int{7}, want: [][2]int{{5, 6}}},
		{name: "interior byte of a rune", matched: []int{1}, want: [][2]int{{0, 2}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := MatchRuns(text, test.matched); !reflect.DeepEqual(got, test.want) {
				t.Errorf("MatchRuns(%q, %v) = %v, want %v", text, test.matched, got, test.want)
			}
		})
	}
}

// TestMatchRunsStylesAGraphemeWhole keeps a combining mark with the rune it
// modifies: half a grapheme cannot carry a color.
func TestMatchRunsStylesAGraphemeWhole(t *testing.T) {
	const text = "e\u0301tat" // "e" then a combining acute: two runes, one cell
	want := [][2]int{{0, 1}}
	if got := MatchRuns(text, []int{0}); !reflect.DeepEqual(got, want) {
		t.Errorf("a hit on the base rune = %v, want the whole cluster %v", got, want)
	}
	if got := MatchRuns(text, []int{1}); !reflect.DeepEqual(got, want) {
		t.Errorf("a hit on the combining mark alone = %v, want the whole cluster %v", got, want)
	}
	// The cluster is one cell, so the letter after it starts at column 1.
	if got := MatchRuns(text, []int{3}); !reflect.DeepEqual(got, [][2]int{{1, 2}}) {
		t.Errorf("the letter after the cluster = %v, want [[1 2]]", got)
	}
}

// TestHighlightCostsNoCells is spec section 10.4.4 applied to the match cue: it
// recolors, it never reflows.
func TestHighlightCostsNoCells(t *testing.T) {
	styles := theme.New(true)
	base := styles.On(theme.FgBase, theme.OverlaySurf)
	hit := styles.OnBold(theme.Brand, theme.OverlaySurf)
	for _, text := range []string{"select card", "\u65e5\u672c card", "q", "permanently delete"} {
		for _, matched := range [][]int{nil, {0}, {0, 1, 2}, {0, 3, 4}, {len(text) - 1}} {
			got := ansi.StringWidth(Highlight(base, hit, text, matched))
			if want := ansi.StringWidth(text); got != want {
				t.Errorf("Highlight(%q, %v) is %d cells, want %d", text, matched, got, want)
			}
		}
	}
}

// TestHighlightKeepsThePlainText asserts the cue is styling and nothing else:
// stripping the escapes returns exactly what went in.
func TestHighlightKeepsThePlainText(t *testing.T) {
	styles := theme.New(true)
	base := styles.On(theme.FgBase, theme.OverlaySurf)
	hit := styles.OnBold(theme.Brand, theme.OverlaySurf)
	const text = "import forge issue"
	if got := ansi.Strip(Highlight(base, hit, text, []int{0, 1, 7, 8})); got != text {
		t.Errorf("stripped highlight = %q, want %q", got, text)
	}
	if got := ansi.Strip(Highlight(base, hit, text, nil)); got != text {
		t.Errorf("stripped unmatched highlight = %q, want %q", got, text)
	}
}
