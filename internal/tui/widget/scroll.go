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
