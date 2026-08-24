package widget

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// Ladder is one hint line declared once and packed to the frame. Spec section
// 10.4.6: a ladder is a pinned head, a droppable middle and a pinned tail, and
// the rungs are ordered by importance rather than by where they fit.
//
// The tail is pinned because a frame too narrow for the ladder is exactly the
// frame where a user most needs the rungs that get them out.
type Ladder struct {
	Head   []string
	Middle []string
	Tail   []string
}

// Hints packs a ladder to width and reports the start column of every rung it
// admitted, in the order Head, Middle, Tail. A dropped rung reports -1, and so
// does every rung on the truncating path: a truncated line has no offset a hit
// region could trust.
//
// The separator belongs to the rung that follows it, so dropping a rung drops
// its leading separator with it and a packed line can never begin or end with
// HintSep. A dropped rung is terminal: the rungs behind it are not attempted,
// which is the opposite of the meta chip row of section 3.4 and is deliberate.
// A chip row is a set of independent facts about one card; a ladder is ordered
// by importance, and admitting a short rung after dropping a more important one
// misreports what the ladder is for.
//
// The hit regions of a rendered ladder are built from the returned columns,
// never by re-splitting the rendered line on the separator: a rendered
// separator carries its own SGR runs and is not a safe split key.
func Hints(styles *theme.Styles, ladder Ladder, width int) (string, []int) {
	separator := styles.Glyph.HintSep
	rungs := ladder.rungs()
	head, middle, tail := ladder.sections()
	if width <= 0 {
		return "", dropped(len(rungs))
	}

	// Step 1: the pinned rungs alone do not fit. Tail rungs go from the end,
	// then the head is truncated. This is the only path that truncates a rung.
	pinned := packed(rungs, head, nil, "", tail)
	if lineWidth(pinned, separator) > width {
		for len(tail) > 0 && lineWidth(packed(rungs, head, nil, "", tail), separator) > width {
			tail = tail[:len(tail)-1]
		}
		kept := packed(rungs, head, nil, "", tail)
		if lineWidth(kept, separator) > width {
			return truncate(styles, render(kept, separator), width), dropped(len(rungs))
		}
		return render(kept, separator), columnsOf(kept, separator, len(rungs))
	}

	// Step 2: drop middle rungs from the end until the line with its ellipsis
	// mark fits. The mark costs one cell plus a separator, so "ellipsis only if
	// it fits" falls out of the same loop rather than needing its own rule.
	mark := styles.Glyph.Ellipsis
	count := len(middle)
	for count > 0 && lineWidth(packed(rungs, head, middle, mark, tail, count), separator) > width {
		count--
	}
	kept := packed(rungs, head, middle, mark, tail, count)

	// Step 3: nothing of the middle survived, so the mark is suppressed
	// entirely. A mark with no admitted middle rung reports only that
	// everything useful is gone, at the price of the cells that could carry the
	// most important middle rung - "[Close] | …" where "[Close] | ? or esc
	// close help" fits is the mark working against its purpose. Retry once
	// without it, then fall back to the pinned set, which step 1 has already
	// established fits.
	if count == 0 && len(middle) > 0 {
		kept = packed(rungs, head, middle, "", tail, 1)
		if lineWidth(kept, separator) > width {
			kept = packed(rungs, head, nil, "", tail)
		}
	}
	return render(kept, separator), columnsOf(kept, separator, len(rungs))
}

// slot is one admitted run of the packed line: a rung at its declared index, or
// the ellipsis mark, which belongs to no rung and reports no column.
type slot struct {
	index int
	text  string
}

const markSlot = -1

// packed is the candidate slot list for a middle of count rungs. The mark is
// present exactly when a middle rung was dropped, and it occupies the line, so
// the rungs behind it take their columns from here rather than from a second
// walk that would forget it.
func packed(rungs []string, head, middle []int, mark string, tail []int, count ...int) []slot {
	kept := len(middle)
	if len(count) > 0 {
		kept = count[0]
	}
	out := make([]slot, 0, len(head)+kept+1+len(tail))
	for _, index := range head {
		out = append(out, slot{index: index, text: rungs[index]})
	}
	for _, index := range middle[:kept] {
		out = append(out, slot{index: index, text: rungs[index]})
	}
	if kept < len(middle) && mark != "" {
		out = append(out, slot{index: markSlot, text: mark})
	}
	for _, index := range tail {
		out = append(out, slot{index: index, text: rungs[index]})
	}
	return out
}

func render(slots []slot, separator string) string {
	parts := make([]string, 0, len(slots))
	for _, one := range slots {
		parts = append(parts, one.text)
	}
	return strings.Join(parts, separator)
}

func lineWidth(slots []slot, separator string) int {
	return ansi.StringWidth(render(slots, separator))
}

// columnsOf is the start column of every admitted rung, mapped back onto the
// caller's declaration order. The mark holds a column of its own on the line and
// carries none in the result.
func columnsOf(slots []slot, separator string, size int) []int {
	columns := dropped(size)
	offset := 0
	for position, one := range slots {
		if position > 0 {
			offset += ansi.StringWidth(separator)
		}
		if one.index != markSlot {
			columns[one.index] = offset
		}
		offset += ansi.StringWidth(one.text)
	}
	return columns
}

// rungs is the ladder flattened into declaration order, which is the order the
// returned column slice is indexed by.
func (l Ladder) rungs() []string {
	out := make([]string, 0, len(l.Head)+len(l.Middle)+len(l.Tail))
	out = append(out, l.Head...)
	out = append(out, l.Middle...)
	return append(out, l.Tail...)
}

// sections is the index set of each part, with empty rungs already dropped so a
// caller may declare a conditional rung as an empty string rather than building
// its slice twice.
func (l Ladder) sections() (head, middle, tail []int) {
	index := 0
	take := func(part []string) []int {
		out := make([]int, 0, len(part))
		for _, rung := range part {
			if rung != "" {
				out = append(out, index)
			}
			index++
		}
		return out
	}
	return take(l.Head), take(l.Middle), take(l.Tail)
}

func dropped(size int) []int {
	columns := make([]int, size)
	for index := range columns {
		columns[index] = -1
	}
	return columns
}
