package widget

import (
	"charm.land/lipgloss/v2"
	"github.com/rivo/uniseg"
)

// MatchRuns coalesces the byte offsets a fuzzy search matched into contiguous
// display-column runs, each a half-open [start, end) pair.
//
// Two conversions happen here and nowhere else. The first is offsets to
// columns: sahilm/fuzzy reports byte offsets into the haystack, while
// lipgloss.StyleRanges cuts by display cell, so a match on a multi-byte or
// double-width rune would otherwise land on the wrong cells. Runs are measured
// per grapheme cluster, which is what keeps a combining mark styled with the
// rune it modifies rather than as a zero-width run of its own.
//
// The second is coalescing: adjacent matched clusters become one run. A fuzzy
// hit on "select card" for the query "sel" is three offsets and must be one
// range, because StyleRanges re-renders its style around every range it is
// given and three abutting ranges emit three redundant SGR pairs for one word.
//
// Offsets outside text, and repeated offsets inside one cluster, are ignored:
// the caller passes a library's output straight through and this is where it is
// made safe rather than at every call site.
func MatchRuns(text string, matched []int) [][2]int {
	if text == "" || len(matched) == 0 {
		return nil
	}
	hits := make(map[int]bool, len(matched))
	for _, offset := range matched {
		hits[offset] = true
	}
	var runs [][2]int
	column := 0
	iterator := uniseg.NewGraphemes(text)
	for iterator.Next() {
		start, end := iterator.Positions()
		width := iterator.Width()
		if clusterHit(hits, start, end) {
			if last := len(runs) - 1; last >= 0 && runs[last][1] == column {
				runs[last][1] = column + width
			} else {
				runs = append(runs, [2]int{column, column + width})
			}
		}
		column += width
	}
	return runs
}

// clusterHit reports whether any matched offset falls inside one cluster's byte
// span. A cluster is styled whole: half of a grapheme cannot carry a color.
func clusterHit(hits map[int]bool, start, end int) bool {
	for offset := start; offset < end; offset++ {
		if hits[offset] {
			return true
		}
	}
	return false
}

// Highlight renders text in base with the matched runs re-rendered in hit.
//
// Spec section 10.4.4: the cue costs zero cells. Both styles are the caller's
// already-cached tokens and this composes them, so the rule that only the theme
// package builds a style is untouched.
func Highlight(base, hit lipgloss.Style, text string, matched []int) string {
	runs := MatchRuns(text, matched)
	if len(runs) == 0 {
		return base.Render(text)
	}
	ranges := make([]lipgloss.Range, 0, len(runs))
	for _, run := range runs {
		ranges = append(ranges, lipgloss.NewRange(run[0], run[1], hit))
	}
	return lipgloss.StyleRanges(base.Render(text), ranges...)
}
