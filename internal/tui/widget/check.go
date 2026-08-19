package widget

import (
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// CheckState is the state of a checklist row (spec section 5.1): open, done, or
// dropped.
type CheckState uint8

// The three checkbox states of spec section 5.1.
const (
	CheckOpen CheckState = iota
	CheckDone
	CheckDropped
)

// Check renders one checklist row on the overlay panel tier. The focused row
// carries the brand foreground; a done row is StatusOK and a dropped row is
// FgMuted, so the state reads without relying on the glyph alone.
func Check(styles *theme.Styles, label string, state CheckState, focused bool) string {
	glyph, foreground := styles.Glyph.Check, theme.FgBase
	switch state {
	case CheckDone:
		glyph, foreground = styles.Glyph.CheckOn, theme.StatusOK
	case CheckDropped:
		glyph, foreground = styles.Glyph.CheckOff, theme.FgMuted
	}
	if focused {
		return styles.OnBold(theme.Brand, theme.OverlaySurf).Render(glyph + " " + label)
	}
	return styles.On(foreground, theme.OverlaySurf).Render(glyph + " " + label)
}
