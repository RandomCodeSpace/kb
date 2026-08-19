package widget

import (
	"charm.land/lipgloss/v2"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// Rail renders a card's left edge: one cell, always reserved. Spec section 2.4:
// the glyph thickens from a half block to a full block on selection and the
// hue stays the card's priority hue even then, because re-hueing it to the
// brand accent erases the P1 signal on the exact card the user is looking at.
func Rail(styles *theme.Styles, priority int, surface theme.Slot, selected bool) string {
	glyph := styles.Glyph.Rail
	if selected {
		glyph = styles.Glyph.RailFull
	}
	return railStyle(styles, priority, surface).Render(glyph)
}

// railStyle prefers the cached rail styles of the two surfaces a card normally
// sits on and composes the rest, so the common path is a lookup.
func railStyle(styles *theme.Styles, priority int, surface theme.Slot) lipgloss.Style {
	switch surface {
	case theme.Card:
		return styles.Rail[priorityLabel(priority)]
	case theme.Raised:
		return styles.RailSel[priorityLabel(priority)]
	default:
		return styles.On(theme.PrioritySlot(priority), surface)
	}
}
