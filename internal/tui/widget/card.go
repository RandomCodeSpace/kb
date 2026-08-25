package widget

import (
	"strings"

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
	Title     string
	Emoji     string
	Seq       string
	Desc      string
	Meta      []string
	Labels    []string
	Priority  int
	Selected  bool
	Alt       bool
	Width     int
	DescLines int
	Density   Density

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
// stacking and gutters belong to the panel. Spec section 3.1: four content rows
// normally, five on a tall frame, two when compact.
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
	descLines := opts.DescLines
	if opts.Density.Compact() {
		descLines = 0
	}

	rows := 2
	if !opts.Density.Compact() {
		rows = 3 + descLines
	}
	content := make([]string, 0, rows)
	var spans []CardSpan
	if inner >= metrics.CardMinInner {
		content = append(content, cardTitle(styles, opts, surface, inner))
		content = append(content, cardDesc(styles, opts, surface, inner, descLines)...)
		chips, chipSpans := cardChips(styles, opts, surface, inner)
		spans = offsetSpans(chipSpans, len(content), metrics.CardRail+metrics.CardPad(opts.Density), inner)
		content = append(content, chips...)
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

// cardTitle is row 0 of spec section 3.2: emoji, title, and a right-aligned
// sequence number that is never truncated.
func cardTitle(styles *theme.Styles, opts CardOpts, surface theme.Slot, inner int) string {
	titleStyle := styles.On(theme.FgBase, surface)
	if opts.Selected {
		titleStyle = styles.OnBold(theme.FgBase, surface)
	}
	head := opts.Title
	if opts.Emoji != "" {
		head = opts.Emoji + " " + opts.Title
	}
	surfaceStyle := styles.On(theme.FgBase, surface)
	sequence := ansi.StringWidth(opts.Seq)
	field := inner
	if sequence > 0 {
		field = inner - sequence - 1
	}
	if field < 0 {
		field = 0
	}
	// The emoji and the column beside it are their own run (issue #229): a
	// terminal shapes a styled run as a unit, so a title left inside the emoji's
	// run is drawn pushed right by the pictograph's excess advance and its last
	// character is then clipped by the padding run, which lands on the cell the
	// grid gave it.
	text := truncate(styles, head, field)
	row := fill(surfaceStyle, MarkRun(styles, opts.Emoji, text, titleStyle, surface), field)
	if sequence == 0 {
		return row
	}
	return row + pad(surfaceStyle, inner-field-sequence) +
		styles.On(theme.FgMuted, surface).Render(opts.Seq)
}

// cardDesc is the description snippet of spec section 3.3. A description
// shorter than its allotment leaves its remaining rows blank.
func cardDesc(styles *theme.Styles, opts CardOpts, surface theme.Slot, inner, lines int) []string {
	if lines <= 0 {
		return nil
	}
	style := styles.On(theme.FgMuted, surface)
	out := make([]string, 0, lines)
	for _, line := range wrap(styles, opts.Desc, inner, lines) {
		if line == "" {
			out = append(out, "")
			continue
		}
		out = append(out, style.Render(line))
	}
	return out
}

// wrap is the greedy word wrap of spec section 3.3: a word longer than the
// field is hard-truncated rather than overflowing, and the last allotted line
// carries the ellipsis when text remains.
func wrap(styles *theme.Styles, text string, width, lines int) []string {
	out := make([]string, 0, lines)
	if width <= 0 {
		for len(out) < lines {
			out = append(out, "")
		}
		return out
	}
	words := strings.Fields(text)
	index := 0
	for len(out) < lines {
		line := ""
		for index < len(words) {
			candidate := words[index]
			if line != "" {
				candidate = line + " " + words[index]
			}
			if ansi.StringWidth(candidate) <= width {
				line = candidate
				index++
				continue
			}
			if line == "" {
				line = truncate(styles, words[index], width)
				index++
			}
			break
		}
		if len(out) == lines-1 && index < len(words) {
			line = truncate(styles, strings.TrimSpace(line+" "+strings.Join(words[index:], " ")), width)
			index = len(words)
		}
		out = append(out, line)
	}
	return out
}

// cardChips is the meta chip row and the label pill row of spec sections 3.4
// and 3.5. Compact merges the labels onto the meta row and flattens the pills.
func cardChips(styles *theme.Styles, opts CardOpts, surface theme.Slot, inner int) ([]string, []CardSpan) {
	surfaceStyle := styles.On(theme.FgBase, surface)
	flat := opts.Density.Compact()
	labels := make([]string, 0, len(opts.Labels))
	for _, tag := range opts.Labels {
		labels = append(labels, Label(styles, tag, surface, flat, tag != "" && tag == opts.HoverTag))
	}
	if flat {
		entries := append(append([]string{}, opts.Meta...), labels...)
		line, starts := joinAt(surfaceStyle, entries, inner)
		return []string{line}, labelSpans(opts.Labels, labels, starts[len(opts.Meta):], 0)
	}
	line, starts := joinAt(surfaceStyle, labels, inner)
	return []string{
		join(surfaceStyle, opts.Meta, inner),
		line,
	}, labelSpans(opts.Labels, labels, starts, 1)
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
