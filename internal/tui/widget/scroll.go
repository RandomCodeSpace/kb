package widget

import (
	"strconv"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// ScrollHint renders the scroll position of an overlay as "12/40" in FgMuted.
// kb's overlays scroll by a hand-managed offset and bubbles/viewport does not
// expose that offset in a form the pointer regions can consume, which is why
// this is a kb widget and not a charm component (spec section 5.1).
func ScrollHint(styles *theme.Styles, current, total int, on theme.Slot) string {
	if total <= 0 {
		return ""
	}
	if current < 0 {
		current = 0
	}
	if current > total {
		current = total
	}
	return styles.On(theme.FgMuted, on).
		Render(strconv.Itoa(current) + "/" + strconv.Itoa(total))
}

// ScrollbarW is the column a shown scrollbar spends. The column is reserved for
// the whole time the body overflows and never appears or disappears with
// activity: kb composes strings, so a column that came and went would reflow
// the body measure of every row under it, twice, for a cue (spec sections
// 10.3.4 and 10.4.4).
const ScrollbarW = 1

// ScrollbarOpts describes one scroll affordance. Total is the content in lines,
// Visible the lines the viewport shows, Offset the first visible line, and
// Height the rows the bar is rendered over.
//
// Active is the linger state of spec section 10.3.4, resolved by the caller
// against its own timing token: kb dims rather than hides, so a settled bar is
// FgMuted and a bar within the linger of the last scroll is FgSubtle. Geometry
// is identical in both.
type ScrollbarOpts struct {
	Total   int
	Visible int
	Offset  int
	Height  int
	Active  bool
	On      theme.Slot
}

// ScrollbarShown reports whether a body of this size overflows its viewport,
// and so whether the caller reserves the affordance column at all. A body that
// fits carries no affordance and no reserved column.
func ScrollbarShown(total, visible int) bool {
	return visible > 0 && total > visible
}

// Scrollbar renders the affordance as one cell per row: a thumb of the track's
// own length proportional to the visible share, positioned by the offset. It
// returns nil when the body fits, which is the signal to reserve no column.
//
// The thumb and the track are distinguished by glyph as well as by hue, so the
// affordance still states a position under a profile that strips color.
func Scrollbar(styles *theme.Styles, opts ScrollbarOpts) []string {
	if opts.Height <= 0 || !ScrollbarShown(opts.Total, opts.Visible) {
		return nil
	}
	hue := theme.FgMuted
	if opts.Active {
		hue = theme.FgSubtle
	}
	style := styles.On(hue, opts.On)
	thumb := style.Render(styles.Glyph.RailFull)
	track := style.Render(styles.Glyph.Track)
	length := thumbLength(opts.Visible, opts.Total, opts.Height)
	start := thumbStart(opts.Offset, opts.Visible, opts.Total, opts.Height, length)
	rows := make([]string, opts.Height)
	for row := range rows {
		rows[row] = track
		if row >= start && row < start+length {
			rows[row] = thumb
		}
	}
	return rows
}

// thumbLength is the visible share of the content, rounded half up and held to
// at least one row so the thumb never vanishes on a very long body.
func thumbLength(visible, total, height int) int {
	length := (2*visible*height + total) / (2 * total)
	return min(max(length, 1), height)
}

// thumbStart is the thumb's first row: the scrolled share of the travel the
// thumb has, rounded half up. An offset past the last page pins it to the end,
// so the bar reads as bottomed-out rather than running off the track.
func thumbStart(offset, visible, total, height, length int) int {
	travel := height - length
	span := total - visible
	if travel <= 0 || span <= 0 {
		return 0
	}
	position := (2*min(max(offset, 0), span)*travel + span) / (2 * span)
	return min(position, travel)
}
