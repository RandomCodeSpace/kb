package carddetail

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/tui/pointer"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// The three places a blocker chip is drawn. A section is part of the control id
// rather than part of the geometry, so the same card appearing as a blocker and
// in the completion gate's reason clause is two controls, and each keeps its
// identity while the body scrolls under the pointer (spec section 10.5.2 row 6).
const (
	chipSectionBlocks    = "blocks"
	chipSectionBlockedBy = "blockedby"
	chipSectionGate      = "gate"
)

// taskChipGap separates two chips inside one run. Two columns, so a pair of
// bracketed chips reads as two objects rather than one.
const taskChipGap = "  "

// taskChipSpan is one activatable chip inside a rendered run: the control it
// registers as, the cells it occupies measured from the run's own start, and
// the card it opens.
type taskChipSpan struct {
	id     pointer.ControlID
	offset int
	width  int
	seq    int
}

// taskChipHit is a taskChipSpan resolved against the body: the logical row it
// landed on and the cell column it starts at, measured from the panel's left
// edge, which is the coordinate space the detail body viewport registers in.
type taskChipHit struct {
	id     pointer.ControlID
	row    int
	column int
	width  int
	seq    int
}

// taskChipControlID identifies one chip by its section and its position in that
// section, never by where it was drawn. A chip that scrolls keeps its id, and a
// hovered chip therefore stays lit across the frame that moved it.
func taskChipControlID(section string, index int) pointer.ControlID {
	return pointer.ControlID("detail.taskchip." + section + "." + strconv.Itoa(index))
}

// taskChipMark is the state mark a blocker chip carries inside its brackets, so
// a blocker that no longer holds the card is distinguishable from one that does
// without reading the status word (issue #222).
//
// Done takes Tick and cancelled takes CheckOff, whose place in the vocabulary is
// already "dropped rather than completed" (spec section 10.4.1). Both are
// followed by a space: Tick is East Asian Ambiguous, so the adjacency rule of
// section 10.4.1 gives it the column after it, and CheckOff matches it so the
// two marks are the same width and a chip cannot move by changing status.
//
// An open blocker takes no mark. The mark is the exception the eye is looking
// for, and giving every chip one would make the row uniform again.
func taskChipMark(styles *theme.Styles, status board.Status) string {
	switch status {
	case board.StatusDone:
		return styles.Glyph.Tick + " "
	case board.StatusCancelled:
		return styles.Glyph.CheckOff + " "
	default:
		return ""
	}
}

// taskChipText is one chip's plain text. A card with a sequence number is named
// by it, because that is the reference the reader can act on; a legacy card
// without one falls back to its id and is not activatable.
func taskChipText(styles *theme.Styles, task board.Task) string {
	ref := task.ID
	if task.Seq > 0 {
		ref = styles.Glyph.MarkSeq + strconv.Itoa(task.Seq)
	}
	return "[" + taskChipMark(styles, task.Status) + ref + " " + string(task.Status) + "]"
}

// taskChips is the plain, unactivatable form: one run of chips with no hit
// regions behind it, for the rows that are not the detail body.
func taskChips(styles *theme.Styles, tasks []board.Task) string {
	chips := make([]string, 0, len(tasks))
	for _, task := range tasks {
		chips = append(chips, taskChipText(styles, task))
	}
	return strings.Join(chips, taskChipGap)
}

// chipRun renders one section's chips as a single styled run and reports where
// each activatable chip landed inside it.
//
// Spec section 10.5.1: a blocker chip is not the section 3.6 pill, which spends
// its cue on an underline because a saturated fill has no tier left to raise. It
// is bracketed text on the panel surface, so the tier step is available and the
// cue is the same one the inline reference of section 10.5.1 takes - the run's
// own ground raised OverlaySurf to OverlayBand, scoped to the chip's cells and
// costing none of them (section 10.4.4).
//
// foreground is the color the row draws its text in, so the chips of a field row
// and the chips inside the completion gate's danger clause each stay the color
// of the row they belong to; hover changes the ground under them and nothing
// else. The raise is drawn as the chip's own surface rather than substituted
// into it the way an inline reference's is: the chip is a run this widget
// composes, so it can be composed on the raised ground directly, and a run that
// sets its own background would overwrite a raise wrapped around it.
func (m Model) chipRun(foreground theme.Slot, section string, tasks []board.Task) (string, []taskChipSpan) {
	if len(tasks) == 0 {
		return "", nil
	}
	resting := m.styles.On(foreground, theme.OverlaySurf)
	raised := m.styles.On(foreground, theme.OverlayBand)
	var built strings.Builder
	var spans []taskChipSpan
	column := 0
	for index, task := range tasks {
		if index > 0 {
			built.WriteString(resting.Render(taskChipGap))
			column += len(taskChipGap)
		}
		text := taskChipText(m.styles, task)
		width := ansi.StringWidth(text)
		run := resting.Render(text)
		if task.Seq > 0 {
			id := taskChipControlID(section, index)
			switch {
			case m.pointerState.IsPressed(id):
				run = m.styles.PressedRun(run)
			case m.pointerState.IsHovered(id):
				run = raised.Render(text)
			}
			spans = append(spans, taskChipSpan{id: id, offset: column, width: width, seq: task.Seq})
		}
		built.WriteString(run)
		column += width
	}
	return built.String(), spans
}

// appendChipHits resolves the spans of one rendered run against the row it was
// drawn on. column is where the run starts, measured from the panel's left edge;
// limit is the first column the row clips at, so a chip the row truncated away
// registers nothing rather than a region over the text that replaced it.
func appendChipHits(hits []taskChipHit, spans []taskChipSpan, row, column, limit int) []taskChipHit {
	for _, span := range spans {
		start := column + span.offset
		if start+span.width > limit {
			continue
		}
		hits = append(hits, taskChipHit{
			id: span.id, row: row, column: start, width: span.width, seq: span.seq,
		})
	}
	return hits
}

// visibleCells is how many of a run's leading cells survive a widget truncation:
// a run wider than its field loses one more cell to the ellipsis tail.
func visibleCells(run string, cells int) int {
	width := ansi.StringWidth(run)
	if width <= cells {
		return width
	}
	return max(cells-1, 0)
}
