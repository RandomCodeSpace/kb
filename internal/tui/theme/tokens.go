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
	CapL     string // U+2590, meter left end cap (spec section 10.1.3; the pill lost its caps to issue #227)
	CapR     string // U+258C, meter right end cap
	Dot      string // U+25CF, column status dot
	Check    string // U+2610, unchecked checklist row
	CheckOn  string // U+2611, checked checklist row
	CheckOff string // U+2612, dropped checklist row
	Tick     string // U+2713, resolved-blocker mark inside a blocker chip
	Diamond  string // U+25C7, effort marker for a value outside the S/M/L scale
	Focus    string // U+25B8, focused band caret
	More     string // overflow cue prefix, rendered as "+N more"
	Ellipsis string // U+2026, the truncation tail of spec section 3.3
	// Blocked is the card's blocked alarm, drawn beside the sequence number of
	// spec section 3.2. It is the vocabulary's only pictograph, and it answers
	// to the emoji admission rule of section 10.4.1: a single code point with
	// Emoji_Presentation=Yes, no variation selector, no zero-width joiner and no
	// modifier, so the terminal has one rune to draw and the width every layout
	// calculation assumes is the width it takes. It is East Asian Wide and so
	// two cells, and it is bound by the adjacency rule - the render site writes
	// a space after it.
	//
	// It is admitted where the three effort squares were retired (issue #232)
	// because it carries a binary alarm rather than a value: a font with no
	// pictograph draws tofu beside the sequence number, and tofu beside a
	// sequence number still says this card is flagged. The squares carried one
	// of three values, and a partial or substituted glyph there lost which.
	Blocked string // U+26D4, the blocked alarm of spec section 3.2
	Track   string // U+2591, progress meter and scrollbar track (section 10.1.3)
	Empty   string // U+25CB, empty-state mark (section 10.8.3)
	Alert   string // U+25B2, failure mark (section 10.8.5)
	Bullet  string // U+00B7, meta separator and card description list marker
	HintSep string // hint ladder separator, three cells (section 10.4.6)

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
	MarkSeq string // card reference prefix, "#142" (section 3.2)
	MarkTag string // plain label pill prefix, "#tag" (section 3.5)
	MarkDue string // compact due prefix, "!2d" (section 3.4)

	// The filter bar's toggle marks. A filter label pill is the section 3.6
	// pill plus one of these inside its caps, so the toggle survives the flat
	// fidelity floor of section 10.7.5, where the filled/tinted distinction that
	// carries the state everywhere else has no color to carry it with. Both are
	// two cells - the mark and the column that separates it from the pill body -
	// so the pill's width is the same in both states (section 10.4.4).
	MarkFilterOff string // unselected filter label pill, "+ #tag"
	MarkFilterOn  string // selected filter label pill, "x #tag"
}

// defaultGlyphs is the vocabulary of spec sections 2.2, 2.4, 3.4, 3.6 and
// 10.4.1. Every token is one cell wide except Blocked and the two filter marks,
// which are two, and HintSep, which is three; the width table of section 10.4.1
// is asserted in tokens_test.go so a re-spelling that silently changes a mark's
// width fails the build rather than the layout.
var defaultGlyphs = Glyphs{
	Rail:     "▌",
	RailFull: "█",
	CapL:     "▐",
	CapR:     "▌",
	Dot:      "●",
	Check:    "☐",
	CheckOn:  "☑",
	CheckOff: "☒",
	Tick:     "✓",
	Diamond:  "◇",
	Focus:    "▸",
	More:     "+",
	Ellipsis: "…",
	Blocked:  "⛔",
	Track:    "░",
	Empty:    "○",
	Alert:    "▲",
	Bullet:   "·",
	HintSep:  " | ",

	HalfTop:    "▀",
	HalfBottom: "▄",

	MarkSeq: "#",
	MarkTag: "#",
	MarkDue: "!",

	MarkFilterOff: "+ ",
	MarkFilterOn:  "x ",
}

// EffortSlot resolves the fill hue of the section 3.4 effort pill for one
// effort value: the scale is S, M and L, and the ramp runs cool to warm.
//
// Issue #232 retired the three colored squares the chip wore between #223 and
// #230. A pictograph proved font-dependent on real terminals however carefully
// its width was pinned, and the value it carried was one of three rather than a
// yes or no, so a substituted or clipped glyph lost information. The letter now
// sits on the fill instead of beside a square: ASCII on background color, which
// is the one thing section 3.6 says a pill owes a terminal font.
//
// The three hues are palette slots that already exist and are already audited
// against FgOnAccent as pill fills. No new hex enters section 1.7's table for a
// three-value scale, and the letter - not the hue - is what carries the value.
//
// An effort value the scale does not name resolves false, and the render site
// falls back to the Diamond mark it wore before the squares.
func EffortSlot(value string) (Slot, bool) {
	switch value {
	case "S":
		return StatusInfo, true
	case "M":
		return StatusWarn, true
	case "L":
		return Label1, true
	default:
		return StatusInfo, false
	}
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

	// The card row grid of spec section 3.1, rewritten by issue #232 and given
	// interior vertical rhythm by issue #240. Every allotment here is a function
	// of density and frame height and never of content, which is the invariant
	// that lets columnStackHeight resolve a column's height before the cards in
	// it are rendered.
	CardTitleLines   int // title rows a card carries on a frame at or above DescTwoLines
	CardDescMax      int // description rows a card may carry at its tallest
	CardDescStep     int // frame rows that buy one more description line
	CardLabelRows    int // label rows a card carries on a frame tall enough for them
	CardInnerPadRows int // interior blank rows a card carries at its tallest
	CardInnerPadTwo  int // frame height at or above which the card carries both

	Overlay OverlayMetrics
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

	CardTitleLines:   2,
	CardDescMax:      5,
	CardDescStep:     10,
	CardLabelRows:    2,
	CardInnerPadRows: 2,
	CardInnerPadTwo:  55,

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

// DescLines is the number of description rows a card carries. Spec section 3.3
// as issue #232 rewrote it: none when compact, one on a short normal frame, and
// one more for every CardDescStep rows above DescTwoLines up to CardDescMax.
//
// The ladder spends height the frame has rather than height it might have: a
// 45-row terminal buys the second line the original rule bought, and only a
// terminal tall enough to keep the same number of cards on screen buys the
// third, fourth and fifth. The allotment is a frame property and never a
// content one, so a one-line description leaves its remaining rows blank and
// the rows under it do not move up.
func (m Metrics) DescLines(frameHeight int, density Density) int {
	if density.Compact() {
		return 0
	}
	if frameHeight < m.DescTwoLines {
		return 1
	}
	return min(2+(frameHeight-m.DescTwoLines)/m.CardDescStep, m.CardDescMax)
}

// TitleRows is the number of title rows a card carries. Spec section 3.2 as
// issue #232 rewrote it: the title wraps rather than being ellipsized to one
// line, and it is the last allotted row that carries the ellipsis when the
// title still does not fit. Compact keeps the single row it always had.
//
// Amended by issue #240. The short normal rung - a frame below DescTwoLines -
// now keeps a single title row too, and spends the continuation row it gives up
// on the interior separator of InnerPadRows. That is step 3 of the section 2.6
// drop order applied one rung early, and it is the cheapest row on the card to
// spend: the spec already ranks a wrapped title's second line below the
// description, the first row still carries the whole of what a scan reads, and
// the ellipsis says the rest exists. The trade is exact - one row out, one row
// in - so the shortest normal frame keeps the card count it had before the
// rhythm arrived.
func (m Metrics) TitleRows(frameHeight int, density Density) int {
	if density.Compact() || frameHeight < m.DescTwoLines {
		return 1
	}
	return m.CardTitleLines
}

// InnerPadRows is the number of blank interior rows a card carries: the
// vertical rhythm of spec section 3.1 as issue #240 added it. The rows carry the
// card's own fill rather than the panel's, so the card still reads as one slab
// and the blank rows highlight, stripe and take a click with every other row of
// it.
//
// The count is a rung of its own. Compact carries none - compact exists to be
// dense (section 2.6). Below CardInnerPadTwo the card affords exactly one, and
// it goes between the shared title/description block and the meta row, which is
// the boundary that carries the grouping: title and description are one unit of
// prose, and everything under them is chips. At or above CardInnerPadTwo the
// frame has the surplus for the second, which goes between the meta row and the
// label rows - data above it, navigation below.
func (m Metrics) InnerPadRows(frameHeight int, density Density) int {
	if density.Compact() {
		return 0
	}
	if frameHeight < m.CardInnerPadTwo {
		return 1
	}
	return m.CardInnerPadRows
}

// LabelRows is the number of label rows a card carries. Spec section 3.5 as
// issue #232 rewrote it: labels own rows of their own below the meta line and
// wrap onto the second when one row does not hold them. Compact reports zero,
// because step 5 of the section 2.6 drop order merges the labels onto the meta
// row instead of giving them one.
func (m Metrics) LabelRows(frameHeight int, density Density) int {
	if density.Compact() {
		return 0
	}
	if frameHeight >= m.DescTwoLines {
		return m.CardLabelRows
	}
	return 1
}

// CardRows is the content row count of one card: the whole of spec section
// 3.1's grid in one place, so the panel that reserves a column's height and the
// widget that draws the card can never disagree about it.
//
// Issue #240 added the interior padding rows to the sum. They are rows like any
// other here, which is what keeps the count a pure function of density and
// frame height: the card cannot decide it has nothing worth separating and give
// a row back, any more than a short description can pull the meta row up.
func (m Metrics) CardRows(frameHeight int, density Density) int {
	if density.Compact() {
		return 2
	}
	return m.TitleRows(frameHeight, density) + m.DescLines(frameHeight, density) +
		m.InnerPadRows(frameHeight, density) + 1 + m.LabelRows(frameHeight, density)
}
