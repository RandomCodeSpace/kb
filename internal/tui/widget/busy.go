package widget

import (
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// BusyOpts describes one loading row. Spec section 10.8.6.
//
// Frame is the tier's already-rendered spinner frame, and it is the one part of
// the row that carries a color of its own. It is empty for a surface whose
// motion is already spoken for: spec section 10.8.4 rule 4 allows one moving
// thing per surface, and the suppressed one renders its label with no frame.
//
// On is the surface slot the row sits on; the caller names its tier, never its
// hue.
type BusyOpts struct {
	Frame string
	Label string
	On    theme.Slot
	Width int
}

// Busy renders the loading row of spec section 10.8.4 as exactly one row:
// frame, BusyGap columns, then the label in FgSubtle on the row's own surface.
//
// The label is lowercase and present continuous and carries no ellipsis,
// because the animation is the ellipsis. A label too long for the row is cut
// with the section 3.3 primitive rather than hard-truncated, so a narrow column
// still says that something was dropped.
func Busy(styles *theme.Styles, opts BusyOpts) string {
	if opts.Width <= 0 {
		return ""
	}
	text := styles.On(theme.FgSubtle, opts.On)
	frame := opts.Frame
	head := ansi.StringWidth(frame)
	if head > 0 {
		head += styles.Metrics.BusyGap
		frame += pad(text, styles.Metrics.BusyGap)
	}
	if head >= opts.Width {
		return clip(frame, opts.Width)
	}
	return frame + text.Render(truncate(styles, opts.Label, opts.Width-head))
}
