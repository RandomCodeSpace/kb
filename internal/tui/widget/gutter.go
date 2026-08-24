package widget

import (
	"strings"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// Gutter renders the focus gutter of spec section 10.4.3: one column carrying
// the Rail glyph in accent when the row has the keyboard and a surface space
// when it does not, followed by FocusGutterGap columns of surface.
//
// The gutter is always reserved. Both states cost FocusGutterW + FocusGutterGap
// cells, which is the whole point: a row that only drew its bar when focused
// would reflow its own text as focus moved onto it (section 10.4.4).
//
// It is a literal cell rendered here rather than a lipgloss left border,
// because the widget owns the wrap and so owns the continuation lines, and
// because a block-level border paints cells the row's own background does not
// reach under.
func Gutter(styles *theme.Styles, focused bool, accent, on theme.Slot) string {
	metrics := styles.Metrics
	surface := styles.On(theme.FgBase, on)
	bar := pad(surface, metrics.FocusGutterW)
	if focused {
		// The repeat is over the metric rather than the literal 1 the metric
		// currently holds, so widening the reserve widens the bar with it.
		bar = styles.On(accent, on).Render(strings.Repeat(styles.Glyph.Rail, max(metrics.FocusGutterW, 0)))
	}
	return bar + pad(surface, metrics.FocusGutterGap)
}
