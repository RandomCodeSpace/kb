package widget

import (
	"charm.land/lipgloss/v2"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// Rail renders a card's left edge: one cell, always reserved. Spec section 2.4:
// the glyph thickens from a half block to a full block on selection and the
// selected cards use the shared focus accent. Resting priority one keeps its
// danger cue; ordinary priorities use the muted text scale.
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
		if selectedSurface := surface == theme.Raised; selectedSurface {
			return styles.On(theme.Brand, surface)
		}
		if priorityLabel(priority) == 1 {
			return styles.On(theme.StatusDanger, surface)
		}
		return styles.On(theme.FgMuted, surface)
	}
}
