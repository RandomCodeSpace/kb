package widget

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// CardOpts describes one board card. Spec section 3 as issue #243 re-cut it:
// the row grid is a ceiling set by density, and every section takes only the
// rows its content fills, so a card with no description is shorter than one
// with five lines of it rather than carrying four blank rows.
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

	// The row ceilings of spec section 2.6, resolved by the caller from
	// theme.Metrics. Issue #243 turned them from allotments into caps: the title
	// takes one row or two as its text needs, the description takes the rows its
	// rendered text fills and none at all when it is empty, and the labels take
	// one row or two as the pills wrap. Each is a function of density and frame
	// height; what the card draws inside it is a function of this card.
	TitleLines int
	DescLines  int
	LabelRows  int

	// PadRows is the interior vertical rhythm of issue #240: blank rows carrying
	// the card's own fill, spent on the boundaries between its sections. One row
	// separates the prose block from the meta row; a second, on a frame tall
	// enough to afford it, separates the meta row from the label rows. Issue
	// #243 made them conditional on both neighbours existing, so a separator
	// never abuts a section the card did not draw. Compact ignores this outright.
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

// cardPlan is one card's resolved row plan: every section already broken into
// the rows its content fills, under the section 2.6 ceilings. It is the single
// answer to "how tall is this card", and both paths read it - CardHeight takes
// nothing but the row count, CardWithSpans draws the content it carries - so a
// column that packed against a measured height and the card that lands in it
// cannot disagree about a row. Issue #243 replaced the reserve-before-render
// invariant with that one.
//
// The plan carries content rather than counts alone because the count *is* the
// wrap: the only way to know a description fills three rows is to break it into
// three. Rendering from the plan is what keeps the wrap done once per drawn
// card rather than once to measure and once to draw.
type cardPlan struct {
	inner        int
	blank        bool     // inner below CardMinInner: rail and surface only
	compact      bool     // dense identity/meta rows with complete labels below
	title        []string // wrapped title text, unstyled, one entry per row
	trailer      string
	trailerWidth int
	desc         [][]styledWord
	descMore     bool // description text remained, so the last row ellipsizes
	pills        []string
	labelRows    []string
	labelStarts  []labelStart
	proseSep     int // separator between the prose block and the meta row
	metaSep      int // separator between the meta row and the label rows
	rows         int
}

// planCard resolves one card's rows without drawing any of them. Spec section
// 2.6 as issue #243 re-cut it: the ladder values are ceilings, and each section
// takes only what its content needs under its own cap.
//
// The separators of issue #240 are conditional on both their neighbours: the
// prose block is always at least the title row and the meta row is always
// drawn, so the first separator survives an absent description, but a card with
// no labels loses the second rather than stacking a blank row against the blank
// space where the label rows would have been.
func planCard(styles *theme.Styles, opts CardOpts) cardPlan {
	metrics := styles.Metrics
	plan := cardPlan{compact: opts.Density.Compact()}
	if opts.Width <= 0 {
		return plan
	}
	surface := styles.Surface(opts.Selected, opts.Alt)
	plan.inner = metrics.CardInner(opts.Width, opts.Density)
	plan.blank = plan.inner < metrics.CardMinInner
	if plan.compact {
		// Compact still drops prose and padding, but labels are data. They wrap
		// below the two dense identity/meta rows instead of being discarded.
		plan.rows = 2
		if !plan.blank {
			plan.trailer, plan.trailerWidth = cardTrailer(styles, opts, surface)
			plan.title = cardTitleText(styles, opts, plan.inner, plan.trailerWidth, 1)
			plan.pills = cardPills(styles, opts, surface, true)
			plan.labelRows, plan.labelStarts = wrapLabels(styles.On(theme.FgBase, surface), plan.pills, plan.inner)
			plan.rows += len(plan.labelRows)
		}
		return plan
	}
	if plan.blank {
		// A card with no room for content is one row of rail and surface. There
		// is no grid left to hold open: every row under the first would be blank
		// space the reader cannot tell from the gutter.
		plan.rows = 1
		return plan
	}

	plan.trailer, plan.trailerWidth = cardTrailer(styles, opts, surface)
	plan.title = cardTitleText(styles, opts, plan.inner, plan.trailerWidth, max(opts.TitleLines, 1))
	plan.desc, plan.descMore = layoutWords(descWords(styles, opts.Desc, surface), plan.inner, max(opts.DescLines, 0))
	plan.pills = cardPills(styles, opts, surface, false)
	plan.labelRows, plan.labelStarts = wrapLabels(styles.On(theme.FgBase, surface), plan.pills, plan.inner)

	padRows := max(opts.PadRows, 0)
	if padRows >= 1 {
		plan.proseSep = 1
	}
	if padRows >= 2 && len(plan.labelRows) > 0 {
		plan.metaSep = 1
	}
	plan.rows = len(plan.title) + len(plan.desc) + plan.proseSep + 1 + plan.metaSep + len(plan.labelRows)
	return plan
}

// CardHeight is the number of rows Card will draw for these options. It is the
// measure half of the measure-before-render rule of spec section 2.5: the
// column packs its stack against this and the card then draws exactly it,
// because both come out of the same plan.
func CardHeight(styles *theme.Styles, opts CardOpts) int {
	return planCard(styles, opts).rows
}

// Card renders one card as its content rows, without the inter-card gutter:
// stacking and gutters belong to the panel. Spec section 3.1 as issue #243
// re-cut it: title rows, description rows when there is a description, one meta
// row and label rows when there are labels, each taking what it needs under the
// section 2.6 ceiling; two rows when compact.
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
	plan := planCard(styles, opts)
	inner := plan.inner

	content := make([]string, 0, plan.rows)
	var spans []CardSpan
	if !plan.blank {
		content = append(content, cardTitle(styles, opts, surface, inner, plan)...)
		content = append(content, cardDesc(styles, surface, inner, plan)...)
		// The interior separators of issue #240. They are emitted as empty
		// content and picked up by the same surface fill every other row of the
		// card takes, so a blank row is card ground rather than a hole in it:
		// selection, hover and the zebra stripe reach it because they are the
		// card's surface and not a per-row decoration.
		for range plan.proseSep {
			content = append(content, "")
		}
		meta, labels, chipSpans := cardChips(styles, opts, surfaceStyle, inner, plan)
		base := len(content)
		content = append(content, meta)
		for range plan.metaSep {
			content = append(content, "")
		}
		base = len(content)
		spans = offsetSpans(chipSpans, base, metrics.CardRail+metrics.CardPad(opts.Density), inner)
		content = append(content, labels...)
	}
	for len(content) < plan.rows {
		content = append(content, "")
	}

	rail := Rail(styles, opts.Priority, railSurface(opts, surface), opts.Selected)
	left := pad(surfaceStyle, metrics.CardPad(opts.Density))
	right := pad(surfaceStyle, metrics.CardPadRight)
	out := make([]string, 0, plan.rows)
	for _, line := range content[:plan.rows] {
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

// cardTitleText is the title wrap of spec section 3.2, in plain text and with
// no styling yet: the row count is the plan's business and the styling is the
// render's, and separating them is what lets a card be measured without being
// drawn.
//
// The title wraps across its ceiling instead of being ellipsized to one line,
// and only the last allotted row carries the ellipsis. Rows the title did not
// need are not emitted at all - issue #243 gives them back to the column rather
// than holding them open - but the first row is always emitted, because it
// carries the trailer even for an empty title.
//
// The trailer - the blocked alarm and the sequence number - sits at the right
// end of the first row and is never truncated. It narrows the first row's field
// alone; the rows under it take the full content width, which is where a
// wrapped title gets the space a one-line title never had.
func cardTitleText(styles *theme.Styles, opts CardOpts, inner, trailerWidth, lines int) []string {
	head := opts.Title
	if opts.Emoji != "" {
		head = opts.Emoji + " " + opts.Title
	}
	fields := make([]int, max(lines, 1))
	for index := range fields {
		fields[index] = inner
	}
	if trailerWidth > 0 {
		fields[0] = max(inner-trailerWidth-1, 0)
	}
	wrapped := wrapFields(styles, head, fields)
	for len(wrapped) > 1 && wrapped[len(wrapped)-1] == "" {
		wrapped = wrapped[:len(wrapped)-1]
	}
	return wrapped
}

// cardTitle styles the title rows the plan already wrapped. The first row is
// the one that carries the trailer and the emoji's own run; the rows under it
// are the same title one step quieter.
func cardTitle(styles *theme.Styles, opts CardOpts, surface theme.Slot, inner int, plan cardPlan) []string {
	titleStyle := styles.On(theme.FgBase, surface)
	if opts.Selected {
		titleStyle = styles.OnBold(theme.FgBase, surface)
	}
	surfaceStyle := styles.On(theme.FgBase, surface)
	trailer, trailerWidth := plan.trailer, plan.trailerWidth
	field := inner
	if trailerWidth > 0 {
		field = max(inner-trailerWidth-1, 0)
	}

	out := make([]string, 0, len(plan.title))
	for index, text := range plan.title {
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

// cardDesc is the description of spec section 3.3 as issue #232 rewrote it and
// issue #243 re-cut it: the frozen markdown grammar of the mdparity package,
// rendered at card scale over the rows its text actually fills. A description
// shorter than its ceiling takes fewer rows; an empty one takes none, and the
// meta row moves up onto the row it would have wasted.
//
// The grammar is the card-detail pane's grammar and not a second one. What
// differs is the output stage: the pane hands the reduced source to glamour,
// which owns a document's margins and blank lines; a card has neither the rows
// nor the width for either, so it takes the runs and wraps them itself.
func cardDesc(styles *theme.Styles, surface theme.Slot, inner int, plan cardPlan) []string {
	if len(plan.desc) == 0 {
		return nil
	}
	return renderWordRows(styles, plan.desc, styles.On(theme.FgMuted, surface), inner, plan.descMore)
}

// cardPills renders one pill per label, on the card's own resolved ground. The
// plan needs them because their widths decide how many label rows the card
// takes, and the render needs the same strings back rather than a second set.
func cardPills(styles *theme.Styles, opts CardOpts, surface theme.Slot, flat bool) []string {
	pills := make([]string, 0, len(opts.Labels))
	for _, tag := range opts.Labels {
		pills = append(pills, Label(styles, tag, surface, flat, tag != "" && tag == opts.HoverTag))
	}
	return pills
}

// cardChips is the meta chip row of spec section 3.4 and the label rows of
// section 3.5. Compact keeps the short flat treatment, while complete labels
// wrap below the dense identity and metadata rows.
//
// The pills and their wrap come off the plan: they are what decided the card's
// height, and rendering a second set could disagree with the height the column
// packed against.
//
// Span rows are relative to the block the pills landed in - the meta row when
// compact merged them onto it, the first label row otherwise - because the
// interior padding of issue #240 sits between the two and only the caller knows
// how many rows of it there are.
func cardChips(styles *theme.Styles, opts CardOpts, surfaceStyle lipgloss.Style, inner int, plan cardPlan) (string, []string, []CardSpan) {
	if plan.compact {
		line, _ := joinAt(surfaceStyle, opts.Meta, inner)
		return line, plan.labelRows, cardLabelSpans(opts.Labels, plan.labelStarts)
	}
	spans := cardLabelSpans(opts.Labels, plan.labelStarts)
	return join(surfaceStyle, opts.Meta, inner), plan.labelRows, spans
}

// labelStart is where one label pill landed: which of the allotted label rows,
// and the column it starts at inside that row. A pill that did not fit reports
// a negative column.
type labelStart struct {
	row    int
	column int
	width  int
	index  int
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
// The wrap is greedy in caller order. A pill that does not fit the current
// row starts the next one. A pill wider than a row is broken at terminal cell
// boundaries, and every fragment keeps the raw tag identity in its span.
//
// Issue #243 made the return the rows the pills actually filled rather than the
// whole allotment: a card with one row of labels is one row shorter than a card
// with two, and a card with no labels at all draws no label row and loses the
// separator above it.
func wrapLabels(style lipgloss.Style, labels []string, inner int) ([]string, []labelStart) {
	if inner <= 0 {
		return nil, nil
	}
	var starts []labelStart
	var out []string
	line, used := "", 0
	gapBeforeNextGroup := false
	for index, label := range labels {
		width := ansi.StringWidth(label)
		if label == "" || width == 0 {
			continue
		}
		if gapBeforeNextGroup {
			out = append(out, "")
			gapBeforeNextGroup = false
		}
		separator := 0
		if used > 0 {
			separator = 1
		}
		if used+separator+width > inner {
			if used > 0 {
				out = append(out, line, "")
			}
			line, used, separator = "", 0, 0
		}
		if width > inner {
			for _, wrapped := range strings.Split(ansi.Hardwrap(label, inner, true), "\n") {
				// Hardwrap owns grapheme boundaries. Repaint each fragment because
				// a style opened on a prior row cannot cross the card's row fill.
				fragment := style.Render(ansi.Strip(wrapped))
				cells := ansi.StringWidth(fragment)
				if cells == 0 {
					continue
				}
				starts = append(starts, labelStart{row: len(out), column: 0, width: cells, index: index})
				out = append(out, fragment)
			}
			line, used = "", 0
			gapBeforeNextGroup = true
			continue
		}
		if separator == 1 {
			line += pad(style, 1)
		}
		starts = append(starts, labelStart{row: len(out), column: used + separator, width: width, index: index})
		line += label
		used += separator + width
	}
	if used > 0 {
		out = append(out, line)
	}
	return out, starts
}

func cardLabelSpans(tags []string, starts []labelStart) []CardSpan {
	spans := make([]CardSpan, 0, len(starts))
	for _, start := range starts {
		if start.index < 0 || start.index >= len(tags) || start.width <= 0 {
			continue
		}
		spans = append(spans, CardSpan{
			Row: start.row, X0: start.column, X1: start.column + start.width,
			Index: start.index, Tag: tags[start.index],
		})
	}
	return spans
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
