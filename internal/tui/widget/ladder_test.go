package widget

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// ladders is every hint ladder shape the TUI packs, in the declaration order the
// column slice is indexed by. The board's own footer is the longest and is what
// the property guard of spec section 10.4.6 is written against.
func ladders() map[string]Ladder {
	return map[string]Ladder{
		"board footer": {
			Head: []string{"ready"},
			Middle: []string{
				"n new", "e edit", "j/k cards", "h/l/tab columns",
				"1-4 jump", "i import", "c cancelled:on",
			},
			Tail: []string{"? help", "q quit"},
		},
		"help pane":   {Head: []string{"[Close]"}, Middle: []string{"? or esc close help", "q quit"}},
		"detail idle": {Head: []string{"up/down scroll"}, Tail: []string{"esc close"}},
		"detail delete": {
			Head:   []string{"up/down choose"},
			Middle: []string{"enter delete"},
			Tail:   []string{"esc back"},
		},
		"dialog":       {Head: []string{"Tab choose"}, Middle: []string{"Enter confirm"}, Tail: []string{"Esc cancel"}},
		"busy":         {Head: []string{"checking drift"}, Tail: []string{"esc cancel"}},
		"tail only":    {Tail: []string{"esc back"}},
		"conditional":  {Head: []string{"head"}, Middle: []string{"", "kept", ""}, Tail: []string{""}},
		"empty ladder": {},
	}
}

// TestHintsPacksEveryLadderAtEveryWidth is the guard of spec section 10.4.6: one
// property test over every ladder in the TUI across widths 1 to 200.
func TestHintsPacksEveryLadderAtEveryWidth(t *testing.T) {
	styles := theme.New(true)
	separator := styles.Glyph.HintSep
	for name, ladder := range ladders() {
		t.Run(name, func(t *testing.T) {
			rungs := ladder.rungs()
			head, middle, tail := ladder.sections()
			for width := 1; width <= 200; width++ {
				line, columns := Hints(styles, ladder, width)
				plain := ansi.Strip(line)
				markCost := ansi.StringWidth(styles.Glyph.Ellipsis) + ansi.StringWidth(separator)

				if got := ansi.StringWidth(line); got > width {
					t.Fatalf("width %d: line is %d cells: %q", width, got, plain)
				}
				if plain != "" &&
					(strings.HasPrefix(plain, separator) || strings.HasSuffix(plain, separator)) {
					t.Fatalf("width %d: line begins or ends with the separator: %q", width, plain)
				}
				if len(columns) != len(rungs) {
					t.Fatalf("width %d: %d columns for %d rungs", width, len(columns), len(rungs))
				}

				// The mark is present if and only if a middle rung was dropped and
				// at least one middle rung is admitted. Step 1 is the exception on
				// the absent side: the pinned set does not fit there and the middle
				// is never considered at all.
				pinned := append(append([]int{}, head...), tail...)
				pinnedFits := lineWidth(packed(rungs, pinned, nil, "", nil), separator) <= width
				dropped := droppedMiddle(columns, middle)
				admitted := admittedMiddle(columns, middle)
				mark := strings.Contains(plain, styles.Glyph.Ellipsis)
				roomForMark := ansi.StringWidth(plain)+markCost <= width
				if pinnedFits && dropped && admitted && !mark && roomForMark {
					t.Fatalf("width %d: dropped a middle rung with no mark: %q", width, plain)
				}
				// The pinnedFits guard is what keeps step 1 out of both directions:
				// its truncated head ends in the same glyph the mark is spelled
				// with, and that glyph is a cut, not a rung.
				if pinnedFits && mark && !dropped {
					t.Fatalf("width %d: marked a ladder that dropped nothing: %q", width, plain)
				}
				if pinnedFits && mark && !admitted {
					t.Fatalf("width %d: marked a ladder with no admitted middle rung: %q", width, plain)
				}

				// Every pinned rung is present whenever the pinned set fits.
				if pinnedFits {
					for _, index := range pinned {
						if columns[index] < 0 {
							t.Fatalf("width %d: dropped pinned rung %q from %q", width, rungs[index], plain)
						}
					}
				}

				// The reported start columns match the rendered offsets.
				for index, column := range columns {
					if column < 0 {
						continue
					}
					if got := ansi.StringWidth(ansi.Cut(plain, 0, column)); got != column {
						t.Fatalf("width %d: rung %d column %d is not a cell offset", width, index, column)
					}
					if !strings.HasPrefix(string([]rune(plain)[len([]rune(ansi.Cut(plain, 0, column))):]), rungs[index]) {
						t.Fatalf("width %d: rung %q not rendered at column %d in %q",
							width, rungs[index], column, plain)
					}
				}
			}
		})
	}
}

func admittedAny(columns []int) bool {
	for _, column := range columns {
		if column >= 0 {
			return true
		}
	}
	return false
}

func droppedMiddle(columns []int, middle []int) bool {
	for _, index := range middle {
		if columns[index] < 0 {
			return true
		}
	}
	return false
}

func admittedMiddle(columns []int, middle []int) bool {
	for _, index := range middle {
		if columns[index] >= 0 {
			return true
		}
	}
	return false
}

// TestHintsDropsFromTheEndAndIsTerminal is the contrast with the meta chip row
// of spec section 3.4: a ladder is ordered by importance, so a dropped rung is
// terminal and the shorter rungs behind it are not attempted.
func TestHintsDropsFromTheEndAndIsTerminal(t *testing.T) {
	styles := theme.New(true)
	ladder := Ladder{
		Head:   []string{"head"},
		Middle: []string{"first", "a very long middle rung", "x"},
		Tail:   []string{"tail"},
	}
	line, columns := Hints(styles, ladder, 23)
	if got := ansi.Strip(line); got != "head | first | … | tail" {
		t.Fatalf("packed line = %q", got)
	}
	if columns[2] >= 0 || columns[3] >= 0 {
		t.Fatalf("a short rung was admitted behind a dropped one: %v", columns)
	}
	// The mark holds a column of its own, so the pinned tail sits behind it:
	// head(4) + sep(3) + first(5) + sep(3) + mark(1) + sep(3).
	if columns[0] != 0 || columns[1] != 7 || columns[4] != 19 {
		t.Fatalf("pinned columns = %v", columns)
	}
}

// TestHintsSuppressesTheMarkWhenNoMiddleRungIsAdmitted is step 3 of spec section
// 10.4.6 as amended after the #187 dogfood: a mark with no admitted middle rung
// spends the cells that could carry the most important rung on saying nothing.
func TestHintsSuppressesTheMarkWhenNoMiddleRungIsAdmitted(t *testing.T) {
	styles := theme.New(true)
	// The help pane's own ladder, which is what the dogfood caught: at 27 cells
	// the mark form fits and is still the wrong answer, because the one rung
	// that dismisses the pane fits too.
	pane := Ladder{Head: []string{"[Close]"}, Middle: []string{"? or esc close help", "q quit"}}
	line, columns := Hints(styles, pane, 29)
	if got := ansi.Strip(line); got != "[Close] | ? or esc close help" {
		t.Fatalf("help pane line = %q", got)
	}
	if columns[0] != 0 || columns[1] != 10 || columns[2] >= 0 {
		t.Fatalf("help pane columns = %v", columns)
	}

	// One cell short of the retry, the pinned set is all that is left - and the
	// mark is not resurrected to fill the room it frees.
	line, columns = Hints(styles, pane, 28)
	if got := ansi.Strip(line); got != "[Close]" {
		t.Fatalf("narrow help pane line = %q", got)
	}
	if columns[0] != 0 || columns[1] >= 0 || columns[2] >= 0 {
		t.Fatalf("narrow help pane columns = %v", columns)
	}

	// With a pinned tail, the fallback is the whole pinned set, never a bare
	// head: step 1 has already established that the pinned set fits.
	tailed := Ladder{
		Head:   []string{"head"},
		Middle: []string{"a very long middle rung"},
		Tail:   []string{"tail"},
	}
	if got := ansi.Strip(mustLine(Hints(styles, tailed, 20))); got != "head | tail" {
		t.Fatalf("pinned fallback = %q", got)
	}
}

// TestHintsTruncatesOnlyWhenThePinnedSetCannotFit is step 1 of spec section
// 10.4.6, the only path that truncates a rung. Tail rungs go from the end first.
func TestHintsTruncatesOnlyWhenThePinnedSetCannotFit(t *testing.T) {
	styles := theme.New(true)
	ladder := Ladder{Head: []string{"board is loading"}, Tail: []string{"? help", "q quit"}}
	// Tail rungs go from the end, so the rung that gets the user out of the
	// frame outlives the one behind it.
	if got := ansi.Strip(mustLine(Hints(styles, ladder, 26))); got != "board is loading | ? help" {
		t.Fatalf("one dropped tail rung = %q", got)
	}
	line, columns := Hints(styles, ladder, 10)
	if got := ansi.Strip(line); got != "board is …" {
		t.Fatalf("truncated head = %q", got)
	}
	if admittedAny(columns) {
		t.Fatalf("a truncated line reported hit columns: %v", columns)
	}
	if line, _ := Hints(styles, ladder, 0); line != "" {
		t.Fatalf("zero width line = %q", line)
	}
}

// TestHintsKeepsTheWholeMiddleWhenItFits is the no-mark case: the ellipsis rung
// exists only to say a rung was dropped.
func TestHintsKeepsTheWholeMiddleWhenItFits(t *testing.T) {
	styles := theme.New(true)
	ladder := Ladder{Head: []string{"a"}, Middle: []string{"b", "c"}, Tail: []string{"d"}}
	line, columns := Hints(styles, ladder, 40)
	if got := ansi.Strip(line); got != "a | b | c | d" {
		t.Fatalf("full ladder = %q", got)
	}
	for index, column := range columns {
		if column != index*4 {
			t.Fatalf("columns = %v", columns)
		}
	}
}

func mustLine(line string, _ []int) string { return line }
