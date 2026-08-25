package widget

import (
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// CardOpts describes one board card. Spec section 3: the row grid is fixed by
// density, not by content, so a short description does not pull the chip rows
// up.
//
// Meta entries arrive already rendered because the chip row of section 3.4
// mixes pill and non-pill runs (the priority marker, the age text and the
// effort marker are not pills). Labels arrive as raw tags because the wheel
// hue, the surface and the compact degradation are all card-local knowledge.
type CardOpts struct {
	Title    string
	Emoji    string
	Seq      string
	Desc     string
	Meta     []string
	Labels   []string
	Priority int
	Selected bool
	Alt      bool
	Width    int
	Density  Density

	// Blocked draws the section 3.2 alarm beside the sequence number. Issue
	// #232 moved it off the meta chip row: the fact is about the card's
	// identity rather than about its schedule, and a two-cell mark beside the
	// reference costs the meta row nothing.
	Blocked bool

	// The row grid of spec section 3.1, resolved by the caller from
	// theme.Metrics so the panel that reserved the column's height and the card
	// that fills it cannot disagree. Every one is a function of density and
	// frame height and never of this card's content.
	TitleLines int
	DescLines  int
	LabelRows  int

	// PadRows is the interior vertical rhythm of issue #240: blank rows carrying
	// the card's own fill, spent on the boundaries between its sections. One row
	// separates the shared title/description block from the meta row; a second,
	// on a frame tall enough to afford it, separates the meta row from the label
	// rows. Compact ignores this outright.
	PadRows int

	// Hovered raises the card's rail cell one tier, per spec section 10.5.1.
	// It is an affordance cue and nothing more: ratified call 9 keeps the board
	// cursor off the pointer, so a hovered card is never the acting selection.
	Hovered bool
	// HoverTag is the label pill under the pointer, empty for none. A card's
	// labels are a set, so the tag identifies the pill without an index whose
	// zero value would name the first one.
	HoverTag string
}

// CardSpan locates one rendered label pill inside the rows a card returned.
// Coordinates are relative to the card's own top-left cell, so a view that
// places the card only has to add the card's origin to reach a hit region.
type CardSpan struct {
	Row    int
	X0, X1 int
	Index  int    // position in CardOpts.Labels
	Tag    string // the label as the card rendered it
}

// Card renders one card as its content rows, without the inter-card gutter:
// stacking and gutters belong to the panel. Spec section 3.1 as issue #232
// rewrote it: title rows, description rows, one meta row and label rows at
// normal density, two rows when compact.
func Card(styles *theme.Styles, opts CardOpts) []string {
	rows, _ := CardWithSpans(styles, opts)
	return rows
}

// CardWithSpans is Card plus the position of every label pill it drew. The
// board keys a pointer hit region to each label, and the card owns the wheel,
// the compact degradation and the individual chip-survival rule of spec section
// 3.4, so it is the only place those positions are known.
func CardWithSpans(styles *theme.Styles, opts CardOpts) ([]string, []CardSpan) {
	if opts.Width <= 0 {
		return nil, nil
	}
	metrics := styles.Metrics
	surface := styles.Surface(opts.Selected, opts.Alt)
	surfaceStyle := styles.On(theme.FgBase, surface)
	inner := metrics.CardInner(opts.Width, opts.Density)
	titleLines, descLines, labelRows := max(opts.TitleLines, 1), opts.DescLines, opts.LabelRows
	padRows := max(opts.PadRows, 0)
	if opts.Density.Compact() {
		titleLines, descLines, labelRows, padRows = 1, 0, 0, 0
	}

	rows := titleLines + descLines + padRows + 1 + labelRows
	content := make([]string, 0, rows)
	var spans []CardSpan
	if inner >= metrics.CardMinInner {
		// The title and the description share one fixed block of rows. The card's
		// total height stays a pure function of density and frame height - which
		// is what lets the panel reserve a column's height before its cards are
		// rendered - while a title short enough to fit one row hands its spare row
		// to the description instead of spending it on blank surface. The meta and
		// label rows below sit at a fixed offset from the card's top either way,
		// so a long title never pushes them down.
		title := cardTitle(styles, opts, surface, inner, titleLines)
		content = append(content, title...)
		content = append(content, cardDesc(styles, opts, surface, inner, titleLines+descLines-len(title))...)
		// The interior separators of issue #240. They are emitted as empty
		// content and picked up by the same surface fill every other row of the
		// card takes, so a blank row is card ground rather than a hole in it:
		// selection, hover and the zebra stripe reach it because they are the
		// card's surface and not a per-row decoration.
		//
		// The block above them is always exactly titleLines+descLines rows -
		// the title takes what it needs and cardDesc fills the rest of the
		// allotment whether or not there is description left to draw into it -
		// so the first separator lands at the same offset on every card.
		if padRows >= 1 {
			content = append(content, "")
		}
		meta, labels, chipSpans := cardChips(styles, opts, surface, inner, labelRows)
		base := len(content)
		content = append(content, meta)
		if padRows >= 2 {
			content = append(content, "")
		}
		if !opts.Density.Compact() {
			base = len(content)
		}
		spans = offsetSpans(chipSpans, base, metrics.CardRail+metrics.CardPad(opts.Density), inner)
		content = append(content, labels...)
	}
	for len(content) < rows {
		content = append(content, "")
	}

	rail := Rail(styles, opts.Priority, railSurface(opts, surface), opts.Selected)
	left := pad(surfaceStyle, metrics.CardPad(opts.Density))
	right := pad(surfaceStyle, metrics.CardPadRight)
	out := make([]string, 0, rows)
	for _, line := range content[:rows] {
		out = append(out, rail+left+fill(surfaceStyle, clip(line, inner), inner)+right)
	}
	return out, spans
}

// railSurface is the ground the rail cell is drawn on. Spec section 10.5.1:
// where an element's selected state already spends the tier step - the card,
// and only the card - hover raises the rail cell instead of the surface, so a
// hovered card reads as a hue half-block against a lighter half and a selected
// one as a full block with no ground showing. A selected card renders no hover
// at all: its surface is already Raised edge to edge, so the step has nowhere
// to go, and the pointer over the already-selected card is offering nothing new.
func railSurface(opts CardOpts, surface theme.Slot) theme.Slot {
	if opts.Hovered && !opts.Selected {
		return theme.Raised
	}
	return surface
}

// offsetSpans moves chip-row-relative spans onto the card's own grid: down by
// the rows drawn above the chips, right by the rail and the left padding. The
// right edge is clamped because the row itself is clipped to the content field.
func offsetSpans(spans []CardSpan, row, column, inner int) []CardSpan {
	out := make([]CardSpan, 0, len(spans))
	for _, span := range spans {
		span.Row += row
		span.X0 += column
		span.X1 = min(span.X1+column, column+inner)
		out = append(out, span)
	}
	return out
}

// clip hard-truncates already-styled content that overran its field.
func clip(content string, width int) string {
	if ansi.StringWidth(content) <= width {
		return content
	}
	return ansi.Truncate(content, width, "")
}

// cardTitle is the title rows of spec section 3.2 as issue #232 rewrote it: the
// title wraps across its whole allotment instead of being ellipsized to one
// line, and only the last allotted row carries the ellipsis.
//
// The trailer - the blocked alarm and the sequence number - sits at the right
// end of the first row and is never truncated. It narrows the first row's field
// alone; the rows under it take the full content width, which is where a
// wrapped title gets the space a one-line title never had.
func cardTitle(styles *theme.Styles, opts CardOpts, surface theme.Slot, inner, lines int) []string {
	titleStyle := styles.On(theme.FgBase, surface)
	if opts.Selected {
		titleStyle = styles.OnBold(theme.FgBase, surface)
	}
	surfaceStyle := styles.On(theme.FgBase, surface)
	head := opts.Title
	if opts.Emoji != "" {
		head = opts.Emoji + " " + opts.Title
	}

	trailer, trailerWidth := cardTrailer(styles, opts, surface)
	field := inner
	if trailerWidth > 0 {
		field = max(inner-trailerWidth-1, 0)
	}
	fields := make([]int, lines)
	for index := range fields {
		fields[index] = inner
	}
	fields[0] = field

	// Trailing rows the title did not need are not emitted at all: the block
	// they belong to is shared with the description, which takes whatever the
	// title leaves. The first row is always emitted, because it carries the
	// trailer even for an empty title.
	wrapped := wrapFields(styles, head, fields)
	for len(wrapped) > 1 && wrapped[len(wrapped)-1] == "" {
		wrapped = wrapped[:len(wrapped)-1]
	}

	out := make([]string, 0, len(wrapped))
	for index, text := range wrapped {
		style := titleStyle
		if index > 0 {
			// A continuation row is the same title, one step quieter: the first
			// row is what a scan reads, and bolding the whole wrap would make a
			// long title shout louder than a short one.
			style = styles.On(theme.FgMuted, surface)
		}
		if index > 0 {
			out = append(out, style.Render(text))
			continue
		}
		// The emoji and the column beside it are their own run (issue #229): a
		// terminal shapes a styled run as a unit, so a title left inside the
		// emoji's run is drawn pushed right by the pictograph's excess advance
		// and its last character is then clipped by the padding run, which lands
		// on the cell the grid gave it.
		row := fill(surfaceStyle, MarkRun(styles, opts.Emoji, text, style, surface), field)
		if trailerWidth > 0 {
			row += pad(surfaceStyle, inner-field-trailerWidth) + trailer
		}
		out = append(out, row)
	}
	return out
}

// cardTrailer is the right end of the title row: the blocked alarm of spec
// section 3.2 and the sequence number, with the alarm owning the column after
// it under the section 10.4.1 adjacency rule.
//
// The alarm is the one pictograph left in the vocabulary (issue #232). It sits
// here rather than in the meta chip row because being blocked is a fact about
// the card rather than about its schedule, and because a binary alarm is the
// one thing a substituted glyph cannot garble: tofu beside a sequence number
// still says this card is flagged, where a substituted effort square lost which
// of three values it carried.
func cardTrailer(styles *theme.Styles, opts CardOpts, surface theme.Slot) (string, int) {
	trailer, width := "", 0
	if opts.Blocked {
		mark := styles.Glyph.Blocked
		trailer = MarkRun(styles, mark, mark+" ", styles.OnBold(theme.StatusWarn, surface), surface)
		width = ansi.StringWidth(mark) + 1
	}
	if sequence := ansi.StringWidth(opts.Seq); sequence > 0 {
		trailer += styles.On(theme.FgMuted, surface).Render(opts.Seq)
		width += sequence
	}
	return trailer, width
}

// cardDesc is the description of spec section 3.3 as issue #232 rewrote it: the
// frozen markdown grammar of the mdparity package, rendered at card scale
// across the card's whole description allotment. A description shorter than
// that allotment leaves its remaining rows blank and the rows under it do not
// move up.
//
// The grammar is the card-detail pane's grammar and not a second one. What
// differs is the output stage: the pane hands the reduced source to glamour,
// which owns a document's margins and blank lines; a card has neither the rows
// nor the width for either, so it takes the runs and wraps them itself.
func cardDesc(styles *theme.Styles, opts CardOpts, surface theme.Slot, inner, lines int) []string {
	if lines <= 0 {
		return nil
	}
	base := styles.On(theme.FgMuted, surface)
	return wrapWords(styles, descWords(styles, opts.Desc, surface), base, inner, lines)
}

// cardChips is the meta chip row of spec section 3.4 and the label rows of
// section 3.5. Compact merges the labels onto the meta row and flattens the
// pills, which is step 5 of the section 2.6 drop order.
//
// Span rows are relative to the block the pills landed in - the meta row when
// compact merged them onto it, the first label row otherwise - because the
// interior padding of issue #240 sits between the two and only the caller knows
// how many rows of it there are.
func cardChips(styles *theme.Styles, opts CardOpts, surface theme.Slot, inner, labelRows int) (string, []string, []CardSpan) {
	surfaceStyle := styles.On(theme.FgBase, surface)
	flat := opts.Density.Compact()
	labels := make([]string, 0, len(opts.Labels))
	for _, tag := range opts.Labels {
		labels = append(labels, Label(styles, tag, surface, flat, tag != "" && tag == opts.HoverTag))
	}
	if flat {
		entries := append(append([]string{}, opts.Meta...), labels...)
		line, starts := joinAt(surfaceStyle, entries, inner)
		return line, nil, labelSpans(opts.Labels, labels, starts[len(opts.Meta):], 0)
	}
	rows, starts := wrapLabels(surfaceStyle, labels, inner, labelRows)
	spans := make([]CardSpan, 0, len(labels))
	for index, start := range starts {
		if start.column < 0 {
			continue
		}
		spans = append(spans, CardSpan{
			Row: start.row, X0: start.column,
			X1:    start.column + ansi.StringWidth(labels[index]),
			Index: index,
			Tag:   opts.Labels[index],
		})
	}
	return join(surfaceStyle, opts.Meta, inner), rows, spans
}

// labelStart is where one label pill landed: which of the allotted label rows,
// and the column it starts at inside that row. A pill that did not fit reports
// a negative column.
type labelStart struct {
	row    int
	column int
}

// wrapLabels lays the label pills out across the card's label rows. Spec
// section 3.5 as issue #232 rewrote it.
//
// Spacing is a fixed one-cell gutter, which is what "equally spaced" resolves
// to on a cell grid: every gap is the same cell whatever the row holds, so a
// label that changes, appears or disappears moves the pills after it and
// nothing else. Distributing slack between pills instead would make the gutter
// a function of the row's contents, so the same label would sit at a different
// column on two cards and every gap would move whenever any pill changed width
// - a reflow section 10.4.4 spends real effort avoiding elsewhere.
//
// The wrap is greedy in the order the caller supplied, which is the survival
// order of section 3.5: a pill that does not fit the rest of the current row
// starts the next one, and a pill too wide for a row of its own is skipped
// rather than truncated into an unreadable stub. Pills still unplaced when the
// allotment runs out are dropped, the same rule section 3.4 applies to a chip
// that does not fit.
func wrapLabels(style lipgloss.Style, labels []string, inner, rows int) ([]string, []labelStart) {
	starts := make([]labelStart, len(labels))
	for index := range starts {
		starts[index] = labelStart{column: -1}
	}
	if rows <= 0 || inner <= 0 {
		return nil, starts
	}
	out := make([]string, 0, rows)
	line, used, row := "", 0, 0
	for index, label := range labels {
		width := ansi.StringWidth(label)
		if label == "" || width > inner {
			continue
		}
		separator := 0
		if used > 0 {
			separator = 1
		}
		if used+separator+width > inner {
			if row+1 >= rows {
				break
			}
			out = append(out, line)
			line, used, separator, row = "", 0, 0, row+1
		}
		if separator == 1 {
			line += pad(style, 1)
		}
		starts[index] = labelStart{row: row, column: used + separator}
		line += label
		used += separator + width
	}
	out = append(out, line)
	for len(out) < rows {
		out = append(out, "")
	}
	return out, starts
}

// labelSpans pairs the emitted label pills with the tags they carry.
func labelSpans(tags, rendered []string, starts []int, row int) []CardSpan {
	spans := make([]CardSpan, 0, len(starts))
	for index, start := range starts {
		if start < 0 {
			continue
		}
		spans = append(spans, CardSpan{
			Row: row, X0: start,
			X1:    start + ansi.StringWidth(rendered[index]),
			Index: index,
			Tag:   tags[index],
		})
	}
	return spans
}
