package widget

import (
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// EmptyOpts describes one empty-state row. Spec section 10.8.6.
//
// On is the surface slot the row sits on: the caller names its tier and never
// its hue. Key and Verb are the action tail; a surface with no eligible action
// leaves them empty and gets the headline alone.
type EmptyOpts struct {
	Headline string
	Key      string
	Verb     string
	On       theme.Slot
	Width    int
}

// Empty renders the empty state of spec section 10.8.3 as exactly one row: the
// Empty glyph in FgMuted, a lowercase headline in FgSubtle, then the action
// tail, whose key is FgBase bold because it is the only part of the row the
// user has to act on.
//
// The width ladder is applied whole at each rung rather than scanned part by
// part, because the tail is the actionable half and a narrow surface is more
// useful saying what to press than saying what is missing:
//
//	>= EmptyHeadlineMin  glyph + headline + tail
//	>= EmptyActionMin    glyph + tail
//	below                glyph alone
//
// The rule this exists to enforce is that no surface renders a bare blank
// panel. A section that is simply absent when it holds nothing stays absent;
// this row is for a surface that would otherwise have no body at all.
func Empty(styles *theme.Styles, opts EmptyOpts) string {
	if opts.Width <= 0 {
		return ""
	}
	metrics := styles.Metrics
	glyph := styles.On(theme.FgMuted, opts.On).Render(styles.Glyph.Empty)
	if opts.Width < metrics.EmptyActionMin {
		return clip(glyph, opts.Width)
	}
	text := styles.On(theme.FgSubtle, opts.On)
	tail := emptyTail(styles, opts)
	if opts.Width >= metrics.EmptyHeadlineMin && opts.Headline != "" {
		head := glyph + text.Render(" "+opts.Headline)
		if tail == "" {
			return clip(head, opts.Width)
		}
		gap := pad(styles.On(theme.FgBase, opts.On), metrics.ActionGap)
		return clip(head+gap+tail, opts.Width)
	}
	if tail == "" {
		return clip(glyph+text.Render(" "+opts.Headline), opts.Width)
	}
	return clip(glyph+pad(styles.On(theme.FgBase, opts.On), 1)+tail, opts.Width)
}

// emptyTail is the key and its verb, or nothing when the surface has no
// eligible action to name. The key is the brightest run in the row.
func emptyTail(styles *theme.Styles, opts EmptyOpts) string {
	if opts.Key == "" {
		return ""
	}
	tail := styles.OnBold(theme.FgBase, opts.On).Render(opts.Key)
	if opts.Verb == "" {
		return tail
	}
	return tail + styles.On(theme.FgSubtle, opts.On).Render(" "+opts.Verb)
}
