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
// Spec section 10.4.1 makes this struct the only place under internal/tui where
// a display glyph or a separator may be written as a literal: views and widgets
// name tokens, and a view that needs a mark the vocabulary does not carry has
// found a missing token, not a reason for a literal.
type Glyphs struct {
	Rail     string // U+258C, resting card rail, unfocused band rail, meter fill
	RailFull string // U+2588, selected card rail, scrollbar thumb
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
	Blocked  string // U+26D4, the compact blocked mark of spec section 3.4
	Track    string // U+2591, progress meter and scrollbar track (section 10.1.3)
	Empty    string // U+25CB, empty-state mark (section 10.8.3)
	Alert    string // U+25B2, failure mark (section 10.8.5)

	// The half-block pair of spec section 10.6.1. These are the only glyphs in
	// the vocabulary that widen the block-glyph risk section 3.6 records rather
	// than inheriting it, and they are accepted for the launch mark alone: the
	// mark is decoration, and every fact the launch screen carries lives in the
	// meta row as plain text (section 10.6.1, glyph vocabulary cost).
	HalfTop    string // U+2580, brand letterform upper half
	HalfBottom string // U+2584, brand letterform lower half

	// Markers are the ASCII prefixes of spec section 10.4.1 that are display
	// vocabulary rather than prose. MarkSeq and MarkTag are a same-text alias,
	// deliberate and not a collision: they answer to different sections and
	// either may be re-spelled without the other.
	MarkPrio string // priority chip prefix, "P1" (section 3.4)
	MarkSeq  string // card reference prefix, "#142" (section 3.2)
	MarkTag  string // plain label pill prefix, "#tag" (section 3.5)
	MarkDue  string // compact due prefix, "!2d" (section 3.4)
}

// defaultGlyphs is the vocabulary of spec sections 2.2, 2.4, 3.4, 3.6 and
// 10.4.1. Every token is one cell wide except Blocked, which is two; the width
// table of section 10.4.1 is asserted in tokens_test.go so a re-spelling that
// silently changes a mark's width fails the build rather than the layout.
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
	Blocked:  "⛔",
	Track:    "░",
	Empty:    "○",
	Alert:    "▲",

	HalfTop:    "▀",
	HalfBottom: "▄",

	MarkPrio: "P",
	MarkSeq:  "#",
	MarkTag:  "#",
	MarkDue:  "!",
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
	MinColumnWidth  int // narrowest panel a column may shrink to and still hold a title
	BandHeadW       int // band prefix before its label, fixed across focus (section 10.4.4)
	ButtonPadX      int // left and right padding of one button (section 10.4.2)
	ButtonGap       int // surface-filled gap between two buttons in a row
	FocusGutterW    int // gutter column, reserved on every focusable non-card row
	FocusGutterGap  int // column between the gutter and the row's content
	MeterCells      int // default bar width of the progress meter (section 10.1.3)
	MeterMinCells   int // below this a meter renders its label only, no bar

	// The empty-state row of section 10.8.3. The two minimums are the rungs of
	// its width ladder: the headline is dropped before the action tail, because
	// the tail is the actionable half.
	EmptyHeadlineMin int // inner width at or above which an empty row keeps its headline
	EmptyActionMin   int // inner width at or above which an empty row keeps its action tail
	ActionGap        int // columns before an empty row's action tail

	// The busy and error rows of section 10.8.4 and 10.8.5.
	BusyGap       int // columns between a spinner frame and its label
	ErrorMaxLines int // lines an error message may wrap to inside a panel

	// The brand mark of spec section 10.6.8. The reveal's span is a timing
	// token and lives in Timing.BrandBirthSteps; the two half-block glyphs are
	// vocabulary and live in Glyphs.
	BrandMarkW       int // unstretched mark width, k(4) + BrandKern(1) + b(5)
	BrandMarkH       int // mark height, both letterforms
	BrandKern        int // blank columns between letterforms
	BrandStretchMax  int // inclusive upper bound of the memoized stretch
	BrandMetaW       int // meta row width before the frame cap
	BrandMetaGap     int // minimum columns between the meta row's two slots
	BrandMetaGapRows int // blank Canvas rows between the mark and the meta row
	BrandMinW        int // frame width below which the full mark is dropped
	BrandMinH        int // frame height below which it is dropped

	OverlayInsetX int // overlay content inset from the panel edge
	OverlayLabelW int // fixed label gutter of an overlay field row
	TableGutter   int // columns between two cells of a lipgloss table row
	CompactBelow  int // frame height below which density compacts
	CompactInnerW int // column inner width below which density compacts
	DescTwoLines  int // frame height at or above which the snippet gets a second line
	Overlay       OverlayMetrics
}

// OverlayMetrics is the proportional panel geometry of spec section 4: every
// content overlay spans a percentage of the frame rather than a fixed cap, so a
// laptop-sized terminal gets a laptop-sized panel. The two content-sized
// dialogs (the task action confirm and the keyboard help) keep their own width
// caps because their height is their content and blowing them up would frame a
// handful of rows in a screenful of surface.
type OverlayMetrics struct {
	WidthPct     int // percent of the frame width a content panel spans
	HeightPct    int // percent of the frame height a content panel spans
	FrameSlackW  int // columns a proportional panel always leaves free
	FrameSlackH  int // rows a proportional panel always leaves free
	NarrowSlackW int // columns a narrow-frame panel leaves free
	NarrowSlackH int // rows a narrow-frame panel leaves free
	MinPaneW     int // narrowest panel the proportional rule will produce
	MinPaneH     int // shortest panel the proportional rule will produce
	MinW         int // below this the overlay falls back to full frame
	MinH         int
	ContentMax   int // readable measure cap for prose inside a panel
	TaskAction   int
	Help         int
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
	MinColumnWidth:  16,
	BandHeadW:       5,
	ButtonPadX:      1,
	ButtonGap:       1,
	FocusGutterW:    1,
	FocusGutterGap:  1,
	MeterCells:      24,
	MeterMinCells:   6,

	EmptyHeadlineMin: 24,
	EmptyActionMin:   10,
	ActionGap:        2,

	BusyGap:       1,
	ErrorMaxLines: 3,

	BrandMarkW:       10,
	BrandMarkH:       5,
	BrandKern:        1,
	BrandStretchMax:  2,
	BrandMetaW:       48,
	BrandMetaGap:     2,
	BrandMetaGapRows: 1,
	BrandMinW:        16,
	BrandMinH:        9,

	OverlayInsetX: 2,
	OverlayLabelW: 12,
	TableGutter:   1,
	CompactBelow:  30,
	CompactInnerW: 22,
	DescTwoLines:  45,
	Overlay: OverlayMetrics{
		WidthPct:     85,
		HeightPct:    88,
		FrameSlackW:  2,
		FrameSlackH:  2,
		NarrowSlackW: 4,
		NarrowSlackH: 2,
		MinPaneW:     24,
		MinPaneH:     8,
		MinW:         24,
		MinH:         8,
		ContentMax:   96,
		TaskAction:   72,
		Help:         56,
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

// BrandBlockH is the launch block height of spec section 10.6.7: the mark, the
// blank rows under it, and the meta row.
func (m Metrics) BrandBlockH() int {
	return m.BrandMarkH + m.BrandMetaGapRows + 1
}

// BrandMetaWidth is the meta row's width on a frame of this width: the section
// 10.6.5 token capped to the frame less a page margin either side. It never
// goes below zero, so a one-column frame renders an empty row rather than a
// negative allotment.
func (m Metrics) BrandMetaWidth(frameWidth int) int {
	width := m.BrandMetaW
	if capped := frameWidth - 2*m.PageMarginX; capped < width {
		width = capped
	}
	if width < 0 {
		return 0
	}
	return width
}

// BrandFits reports whether a frame is large enough for the full mark. Below
// either floor the launch screen drops the mark and renders the meta row alone
// (spec section 10.6.7).
func (m Metrics) BrandFits(frameWidth, frameHeight int) bool {
	return frameWidth >= m.BrandMinW && frameHeight >= m.BrandMinH
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

// OverlayPane is the panel geometry of spec section 4. It has two regimes,
// split at the same WideFrame threshold the board collapses on, which is how a
// responsive modal behaves: a narrow frame has no width to give away, so the
// panel takes all of it but the slack; a wide frame gets a proportional panel
// with real backdrop around it instead of a fixed cap stranded in dead canvas.
func (m Metrics) OverlayPane(frameWidth, frameHeight int) (paneWidth, paneHeight int) {
	frameWidth, frameHeight = max(frameWidth, 1), max(frameHeight, 1)
	overlay := m.Overlay
	if frameWidth < m.WideFrame {
		return max(frameWidth-overlay.NarrowSlackW, 1), max(frameHeight-overlay.NarrowSlackH, 1)
	}
	paneWidth = clampPane(frameWidth, overlay.WidthPct, overlay.FrameSlackW, overlay.MinPaneW)
	paneHeight = clampPane(frameHeight, overlay.HeightPct, overlay.FrameSlackH, overlay.MinPaneH)
	return paneWidth, paneHeight
}

// OverlayElevated reports whether a panel of this size renders as an elevated
// panel. Spec section 4: below the minimum the overlay falls back to the full
// frame, which is what keeps the frozen dismissal behaviors reachable on a
// terminal too small to center anything in.
func (m Metrics) OverlayElevated(paneWidth, paneHeight int) bool {
	return paneWidth >= m.Overlay.MinW && paneHeight >= m.Overlay.MinH
}

// OverlayContent is the readable measure inside a panel of this width: the
// panel grows with the frame, the prose column inside it does not grow past the
// point where a line stops being scannable.
func (m Metrics) OverlayContent(paneWidth int) int {
	return max(min(paneWidth-2*m.OverlayInsetX, m.Overlay.ContentMax), 1)
}

// OverlayFocusContent is the readable measure inside a panel for a row that can
// take focus. Spec section 10.4.3: the focus gutter and its gap are reserved in
// every state, so a focusable row's prose column is two narrower than a static
// row on the same panel rather than reflowing when focus arrives.
func (m Metrics) OverlayFocusContent(paneWidth int) int {
	return max(m.OverlayContent(paneWidth)-m.FocusGutterW-m.FocusGutterGap, 1)
}

// clampPane resolves one axis of the proportional panel rule: the share of the
// frame, raised to the floor, then held clear of the frame edge. The slack is
// applied last so the panel never touches the edge it casts its shadow onto; a
// frame too small to honor the floor within that slack fails the elevation
// check instead and falls back to the full frame.
func clampPane(frame, percent, slack, floor int) int {
	pane := (frame*percent + 50) / 100
	pane = max(pane, floor)
	return max(min(pane, frame-slack), 1)
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
