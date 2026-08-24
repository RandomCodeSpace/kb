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
//
// MetaLit is the ship celebration of issue #191 at its lit phase. It moves the
// meta row's foreground from FgMuted to StatusOK and nothing else: no cell
// changes width, so the no-reflow parity of spec section 10.4.4 holds across
// the phase. The caller owns whether the effect runs at all - the widget is
// told, never asked, because the decision is a fidelity gate and spec section
// 10.7.5 keeps that out of every render path.
type PanelOpts struct {
	Header  BandOpts
	Meta    string
	MetaLit bool
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
		rows = append(rows, metaRow(styles, opts.Meta, opts.Width, opts.MetaLit))
	}
	for _, line := range opts.Body {
		rows = append(rows, pad(panel, inset)+fill(panel, clip(line, inner), inner)+pad(panel, inset))
	}
	if opts.More > 0 {
		rows = append(rows, metaRow(styles, styles.Glyph.More+strconv.Itoa(opts.More)+" more", opts.Width, false))
	}
	for len(rows) < opts.Height {
		rows = append(rows, pad(panel, opts.Width))
	}
	return rows[:opts.Height]
}

// metaRow is the inset Surface row shared by the column meta line of section
// 2.3 and the overflow cue of section 3.7.
//
// lit is the celebration phase and it changes the foreground slot alone. The
// row is Surface either way, so the flash stays inside the tier the panel body
// already owns and no band, rail or card hue moves for it.
func metaRow(styles *theme.Styles, text string, width int, lit bool) string {
	inset := styles.Metrics.ColumnMetaInset
	panel := styles.Column.Panel
	field := width - inset
	if field < 0 {
		field = 0
	}
	ink := styles.Column.Meta
	if lit {
		ink = styles.OnBold(theme.StatusOK, theme.Surface)
	}
	body := truncate(styles, text, field)
	return pad(panel, min(inset, width)) + fill(panel, ink.Render(body), field)
}
