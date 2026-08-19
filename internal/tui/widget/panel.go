package widget

import (
	"strconv"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// PanelOpts describes one column panel. Spec sections 2.1 to 2.3 and 3.7: a
// header band, an optional meta line, the card stack inset by ColumnPadX, and
// an overflow cue on the last row when the stack did not fit.
//
// More is the overflow count. It is not in the spec's API sketch, but section
// 3.7 puts the cue inside the panel and no caller may style a row itself, so
// the panel has to own it.
type PanelOpts struct {
	Header  BandOpts
	Meta    string
	Body    []string
	More    int
	Width   int
	Height  int
	Density Density
}

// Panel renders one column panel as its rows. The body rows are already
// rendered cards; the panel insets them and carries the Surface tier behind
// them so the shade step between panel and card is the only separation.
func Panel(styles *theme.Styles, opts PanelOpts) []string {
	if opts.Width <= 0 || opts.Height <= 0 {
		return nil
	}
	metrics := styles.Metrics
	panel := styles.Column.Panel
	inset := metrics.ColumnPad(opts.Density)
	inner := opts.Width - 2*inset
	if inner < 0 {
		inner = 0
	}

	header := opts.Header
	header.Width = opts.Width
	rows := make([]string, 0, opts.Height)
	rows = append(rows, Band(styles, header))
	if !opts.Density.Compact() && opts.Meta != "" {
		rows = append(rows, metaRow(styles, opts.Meta, opts.Width))
	}
	for _, line := range opts.Body {
		rows = append(rows, pad(panel, inset)+fill(panel, clip(line, inner), inner)+pad(panel, inset))
	}
	if opts.More > 0 {
		rows = append(rows, metaRow(styles, styles.Glyph.More+strconv.Itoa(opts.More)+" more", opts.Width))
	}
	for len(rows) < opts.Height {
		rows = append(rows, pad(panel, opts.Width))
	}
	return rows[:opts.Height]
}

// metaRow is the inset Surface row shared by the column meta line of section
// 2.3 and the overflow cue of section 3.7.
func metaRow(styles *theme.Styles, text string, width int) string {
	inset := styles.Metrics.ColumnMetaInset
	panel := styles.Column.Panel
	field := width - inset
	if field < 0 {
		field = 0
	}
	body := truncate(styles, text, field)
	return pad(panel, min(inset, width)) +
		fill(panel, styles.Column.Meta.Render(body), field)
}
