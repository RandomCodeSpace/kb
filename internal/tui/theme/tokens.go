package theme

// Density is the layout density the frame resolved to. Spec section 2.6:
// compaction is not gradual, crossing the threshold applies all of it at once.
type Density uint8

// The two densities of spec section 2.6.
const (
	DensityNormal Density = iota
	DensityCompact
)

// Compact reports whether the density drops the description snippet, the
// gutters and the pill end caps.
func (d Density) Compact() bool { return d == DensityCompact }

// Glyphs are the accent vocabulary of the design language. They are tokens and
// live beside the colors.
//
// Known risk, carried consciously (map #136): the accent vocabulary is built
// from U+2588 / U+258C / U+2590. On fonts without block glyphs it degrades
// worse than a border would. Accepted at the issue #137 resolution.
type Glyphs struct {
	Rail     string // U+258C, resting card rail and unfocused band rail
	RailFull string // U+2588, selected card rail
	CapL     string // U+2590, pill left end cap (spec section 3.6)
	CapR     string // U+258C, pill right end cap
	Dot      string // U+25CF, column status dot
	Check    string // U+2610, unchecked checklist row
	CheckOn  string // U+2611, checked checklist row
	CheckOff string // U+2612, dropped checklist row
	Diamond  string // U+25C7, effort marker
	Focus    string // U+25B8, focused band caret
	More     string // overflow cue prefix, rendered as "+N more"
	Ellipsis string // U+2026, the truncation tail of spec section 3.3
}

// defaultGlyphs is the vocabulary of spec sections 2.2, 2.4, 3.4 and 3.6.
var defaultGlyphs = Glyphs{
	Rail:     "▌",
	RailFull: "█",
	CapL:     "▐",
	CapR:     "▌",
	Dot:      "●",
	Check:    "☐",
	CheckOn:  "☑",
	CheckOff: "☒",
	Diamond:  "◇",
	Focus:    "▸",
	More:     "+",
	Ellipsis: "…",
}

// Metrics are the gutter, padding and threshold tokens of spec section 2.5,
// plus the compaction thresholds of section 2.6 and the overlay width caps of
// section 4. Every number here is normative; a slice that needs a value not
// written here has found a spec gap.
type Metrics struct {
	WideFrame       int // frame width at or above which the page keeps a margin
	PageMarginX     int // left/right page margin on a wide frame
	PagePadTop      int // canvas row between the toolbar and the columns, normal only
	ColumnGutter    int // columns between panels
	ColumnPadX      int // inset of the card stack inside its panel, normal only
	ColumnMetaInset int // meta line and "+N more" inset from the panel edge
	CardGap         int // rows between stacked cards, normal only
	CardRail        int // reserved on the card's left edge, always
	CardPadLeft     int // between rail and content, normal only
	CardPadRight    int // always
	CardMinInner    int // below this a card renders surface and rail only
	MaxColumnWidth  int // terminal analogue of a web max-width container
	OverlayInsetX   int // overlay content inset from the panel edge
	OverlayLabelW   int // fixed label gutter of an overlay field row
	CompactBelow    int // frame height below which density compacts
	CompactInnerW   int // column inner width below which density compacts
	DescTwoLines    int // frame height at or above which the snippet gets a second line
	Overlay         OverlayMetrics
}

// OverlayMetrics are the panel geometry and the per-overlay width caps that
// already exist in the views, carried over as tokens (spec section 4).
type OverlayMetrics struct {
	PaneW       int // card detail panel width cap
	PaneH       int // card detail panel height cap
	FrameSlackW int // frame width the panel leaves free
	FrameSlackH int // frame height the panel leaves free
	MinW        int // below this the overlay falls back to full frame
	MinH        int
	CardDetail  int
	Editor      int
	ADRSplit    int
	IssueImport int
	TaskAction  int
	Help        int
}

// defaultMetrics is spec section 2.5, 2.6 and 4.
var defaultMetrics = Metrics{
	WideFrame:       100,
	PageMarginX:     1,
	PagePadTop:      1,
	ColumnGutter:    1,
	ColumnPadX:      1,
	ColumnMetaInset: 2,
	CardGap:         1,
	CardRail:        1,
	CardPadLeft:     1,
	CardPadRight:    1,
	CardMinInner:    6,
	MaxColumnWidth:  52,
	OverlayInsetX:   2,
	OverlayLabelW:   12,
	CompactBelow:    30,
	CompactInnerW:   22,
	DescTwoLines:    45,
	Overlay: OverlayMetrics{
		PaneW:       72,
		PaneH:       13,
		FrameSlackW: 8,
		FrameSlackH: 6,
		MinW:        24,
		MinH:        8,
		CardDetail:  92,
		Editor:      96,
		ADRSplit:    100,
		IssueImport: 88,
		TaskAction:  72,
		Help:        56,
	},
}

// DensityFor resolves the frame against the compaction thresholds. Spec
// section 2.6: compaction fires when the frame is short or a column is narrow.
func (m Metrics) DensityFor(frameHeight, columnInnerWidth int) Density {
	if frameHeight < m.CompactBelow || columnInnerWidth < m.CompactInnerW {
		return DensityCompact
	}
	return DensityNormal
}

// PageMargin is the left/right page margin for a frame of this width.
func (m Metrics) PageMargin(frameWidth int) int {
	if frameWidth >= m.WideFrame {
		return m.PageMarginX
	}
	return 0
}

// PagePad is the canvas row between the toolbar and the columns.
func (m Metrics) PagePad(density Density) int {
	return m.compactZero(m.PagePadTop, density)
}

// ColumnPad is the inset of the card stack inside its panel.
func (m Metrics) ColumnPad(density Density) int {
	return m.compactZero(m.ColumnPadX, density)
}

// CardGapRows is the number of rows between stacked cards.
func (m Metrics) CardGapRows(density Density) int {
	return m.compactZero(m.CardGap, density)
}

// CardPad is the gap between a card's rail and its content.
func (m Metrics) CardPad(density Density) int {
	return m.compactZero(m.CardPadLeft, density)
}

func (m Metrics) compactZero(value int, density Density) int {
	if density.Compact() {
		return 0
	}
	return value
}

// CardInner is the content width of a card of this total width. Spec section
// 2.5: normal spends rail, left pad and right pad; compact drops the left pad.
func (m Metrics) CardInner(width int, density Density) int {
	inner := width - m.CardRail - m.CardPad(density) - m.CardPadRight
	if inner < 0 {
		return 0
	}
	return inner
}

// DescLines is the number of description snippet rows a card carries. Spec
// section 3.3: none when compact, one normally, two on a tall frame.
func (m Metrics) DescLines(frameHeight int, density Density) int {
	if density.Compact() {
		return 0
	}
	if frameHeight >= m.DescTwoLines {
		return 2
	}
	return 1
}
