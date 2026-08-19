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
}

// Card renders one card as its content rows, without the inter-card gutter:
// stacking and gutters belong to the panel. Spec section 3.1: four content rows
// normally, five on a tall frame, two when compact.
func Card(styles *theme.Styles, opts CardOpts) []string {
	if opts.Width <= 0 {
		return nil
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
	if inner >= metrics.CardMinInner {
		content = append(content, cardTitle(styles, opts, surface, inner))
		content = append(content, cardDesc(styles, opts, surface, inner, descLines)...)
		content = append(content, cardChips(styles, opts, surface, inner)...)
	}
	for len(content) < rows {
		content = append(content, "")
	}

	rail := Rail(styles, opts.Priority, surface, opts.Selected)
	left := pad(surfaceStyle, metrics.CardPad(opts.Density))
	right := pad(surfaceStyle, metrics.CardPadRight)
	out := make([]string, 0, rows)
	for _, line := range content[:rows] {
		out = append(out, rail+left+fill(surfaceStyle, clip(line, inner), inner)+right)
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
	text := truncate(styles, head, field)
	row := fill(surfaceStyle, titleStyle.Render(text), field)
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
func cardChips(styles *theme.Styles, opts CardOpts, surface theme.Slot, inner int) []string {
	surfaceStyle := styles.On(theme.FgBase, surface)
	flat := opts.Density.Compact()
	labels := make([]string, 0, len(opts.Labels))
	for _, tag := range opts.Labels {
		labels = append(labels, Label(styles, tag, surface, flat))
	}
	if flat {
		return []string{join(surfaceStyle, append(append([]string{}, opts.Meta...), labels...), inner)}
	}
	return []string{
		join(surfaceStyle, opts.Meta, inner),
		join(surfaceStyle, labels, inner),
	}
}
