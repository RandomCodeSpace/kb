package theme

import (
	"reflect"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"golang.org/x/text/width"
)

func TestDensityForBothAxes(t *testing.T) {
	metrics := defaultMetrics
	cases := []struct {
		height int
		inner  int
		want   Density
	}{
		{40, 30, DensityNormal},
		{30, 22, DensityNormal},
		{29, 30, DensityCompact},
		{40, 21, DensityCompact},
		{10, 10, DensityCompact},
	}
	for _, testCase := range cases {
		if got := metrics.DensityFor(testCase.height, testCase.inner); got != testCase.want {
			t.Errorf("DensityFor(%d, %d) = %d, want %d", testCase.height, testCase.inner, got, testCase.want)
		}
	}
}

func TestDensityCompactReports(t *testing.T) {
	if DensityNormal.Compact() {
		t.Error("normal density must not report compact")
	}
	if !DensityCompact.Compact() {
		t.Error("compact density must report compact")
	}
}

func TestPageMarginOnlyOnAWideFrame(t *testing.T) {
	metrics := defaultMetrics
	if got := metrics.PageMargin(100); got != 1 {
		t.Errorf("PageMargin(100) = %d, want 1", got)
	}
	if got := metrics.PageMargin(99); got != 0 {
		t.Errorf("PageMargin(99) = %d, want 0", got)
	}
}

func TestCompactZeroesTheSpacingTokens(t *testing.T) {
	metrics := defaultMetrics
	cases := []struct {
		name   string
		normal int
		value  func(Density) int
	}{
		{"PagePad", 1, metrics.PagePad},
		{"ColumnPad", 1, metrics.ColumnPad},
		{"CardGapRows", 1, metrics.CardGapRows},
		{"CardPad", 1, metrics.CardPad},
	}
	for _, testCase := range cases {
		if got := testCase.value(DensityNormal); got != testCase.normal {
			t.Errorf("%s at normal density = %d, want %d", testCase.name, got, testCase.normal)
		}
		if got := testCase.value(DensityCompact); got != 0 {
			t.Errorf("%s at compact density = %d, want 0", testCase.name, got)
		}
	}
}

// TestOverlayPaneHasTwoRegimes pins the geometry of ticket #151: a narrow frame
// keeps the near-full-frame panel v1.0.1 shipped, a wide frame gets a panel
// proportional to the frame instead of a fixed cap stranded in dead canvas.
func TestOverlayPaneHasTwoRegimes(t *testing.T) {
	metrics := defaultMetrics
	cases := []struct {
		frameW, frameH int
		wantW, wantH   int
	}{
		{200, 50, 170, 44}, // reference laptop terminal
		{220, 60, 187, 53},
		{120, 35, 102, 31},
		{100, 30, 85, 26}, // the wide-frame threshold itself
		{99, 30, 95, 28},  // one cell below it, narrow regime
		{80, 24, 76, 22},
		{30, 10, 26, 8},
		{20, 6, 16, 4},
		{1, 1, 1, 1},
		{0, 0, 1, 1},
	}
	for _, testCase := range cases {
		gotW, gotH := metrics.OverlayPane(testCase.frameW, testCase.frameH)
		if gotW != testCase.wantW || gotH != testCase.wantH {
			t.Errorf("OverlayPane(%d, %d) = %dx%d, want %dx%d",
				testCase.frameW, testCase.frameH, gotW, gotH, testCase.wantW, testCase.wantH)
		}
		if gotW > max(testCase.frameW, 1) || gotH > max(testCase.frameH, 1) {
			t.Errorf("OverlayPane(%d, %d) = %dx%d overflows the frame",
				testCase.frameW, testCase.frameH, gotW, gotH)
		}
	}
}

// TestOverlayPaneFloorsAWidePanel covers the floor arm of the proportional
// rule: a wide but very short frame cannot reach the minimum panel height, so
// the slack wins and the elevation check sends the overlay full-frame.
func TestOverlayPaneFloorsAWidePanel(t *testing.T) {
	metrics := defaultMetrics
	paneWidth, paneHeight := metrics.OverlayPane(200, 6)
	if paneWidth != 170 || paneHeight != 4 {
		t.Fatalf("OverlayPane(200, 6) = %dx%d, want 170x4", paneWidth, paneHeight)
	}
	if metrics.OverlayElevated(paneWidth, paneHeight) {
		t.Error("a panel below the section 4 minimum height must not elevate")
	}
}

func TestOverlayElevatedGuardsBothAxes(t *testing.T) {
	metrics := defaultMetrics
	cases := []struct {
		width, height int
		want          bool
	}{
		{24, 8, true},
		{170, 44, true},
		{23, 8, false},
		{24, 7, false},
	}
	for _, testCase := range cases {
		if got := metrics.OverlayElevated(testCase.width, testCase.height); got != testCase.want {
			t.Errorf("OverlayElevated(%d, %d) = %v, want %v",
				testCase.width, testCase.height, got, testCase.want)
		}
	}
}

// TestOverlayContentKeepsAReadableMeasure is the other half of ticket #151: the
// panel grows with the frame, the prose column inside it does not grow past the
// point where a line stops being scannable.
func TestOverlayContentKeepsAReadableMeasure(t *testing.T) {
	metrics := defaultMetrics
	cases := [][2]int{{170, 96}, {102, 96}, {100, 96}, {76, 72}, {26, 22}, {4, 1}, {0, 1}}
	for _, testCase := range cases {
		if got := metrics.OverlayContent(testCase[0]); got != testCase[1] {
			t.Errorf("OverlayContent(%d) = %d, want %d", testCase[0], got, testCase[1])
		}
	}
}

func TestCardInnerSpendsTheRailAndPadding(t *testing.T) {
	metrics := defaultMetrics
	if got := metrics.CardInner(30, DensityNormal); got != 27 {
		t.Errorf("normal inner of a 30-cell card = %d, want 27", got)
	}
	if got := metrics.CardInner(30, DensityCompact); got != 28 {
		t.Errorf("compact inner of a 30-cell card = %d, want 28", got)
	}
	if got := metrics.CardInner(1, DensityNormal); got != 0 {
		t.Errorf("inner never goes negative, got %d", got)
	}
}

func TestDescLinesFollowTheFrameHeight(t *testing.T) {
	metrics := defaultMetrics
	if got := metrics.DescLines(40, DensityCompact); got != 0 {
		t.Errorf("compact desc lines = %d, want 0", got)
	}
	if got := metrics.DescLines(44, DensityNormal); got != 1 {
		t.Errorf("normal desc lines = %d, want 1", got)
	}
	if got := metrics.DescLines(45, DensityNormal); got != 2 {
		t.Errorf("tall desc lines = %d, want 2", got)
	}
}

// TestDescLinesClimbTheHeightLadder is the section 3.3 allotment as issue #232
// rewrote it: one line on a short normal frame, and one more for every
// CardDescStep rows above DescTwoLines, capped at CardDescMax. The ladder
// spends height the frame actually has, so a terminal buys a longer description
// only where it can afford one without losing a card off the bottom.
func TestDescLinesClimbTheHeightLadder(t *testing.T) {
	metrics := defaultMetrics
	cases := map[int]int{30: 1, 44: 1, 45: 2, 54: 2, 55: 3, 64: 3, 65: 4, 74: 4, 75: 5, 200: 5}
	for height, want := range cases {
		if got := metrics.DescLines(height, DensityNormal); got != want {
			t.Errorf("DescLines(%d) = %d, want %d", height, got, want)
		}
	}
}

// TestCardRowGridIsAFunctionOfDensityAndHeight is the invariant the whole of
// section 3.1 rests on: a card's height is decided before its content is, which
// is what lets a column reserve its scroll affordance and its overflow cue
// before the cards in it are rendered. Nothing here takes a card.
func TestCardRowGridIsAFunctionOfDensityAndHeight(t *testing.T) {
	metrics := defaultMetrics
	if got := metrics.TitleRows(DensityCompact); got != 1 {
		t.Errorf("compact title rows = %d, want 1", got)
	}
	if got := metrics.TitleRows(DensityNormal); got != metrics.CardTitleLines {
		t.Errorf("normal title rows = %d, want %d", got, metrics.CardTitleLines)
	}
	labels := map[int]int{30: 1, 44: 1, 45: 2, 100: 2}
	for height, want := range labels {
		if got := metrics.LabelRows(height, DensityNormal); got != want {
			t.Errorf("LabelRows(%d) = %d, want %d", height, got, want)
		}
	}
	if got := metrics.LabelRows(100, DensityCompact); got != 0 {
		t.Errorf("compact label rows = %d, want 0; step 5 merges them onto the meta row", got)
	}
	// Total rows: title + description + the one meta row + labels.
	rows := map[int]int{30: 5, 44: 5, 45: 7, 55: 8, 75: 10}
	for height, want := range rows {
		if got := metrics.CardRows(height, DensityNormal); got != want {
			t.Errorf("CardRows(%d) = %d, want %d", height, got, want)
		}
	}
	for _, height := range []int{12, 30, 45, 100} {
		if got := metrics.CardRows(height, DensityCompact); got != 2 {
			t.Errorf("compact CardRows(%d) = %d, want 2", height, got)
		}
	}
}

func TestMetricsCarrySpecNumbers(t *testing.T) {
	metrics := New(true).Metrics
	cases := map[string][2]int{
		"ColumnGutter":    {metrics.ColumnGutter, 1},
		"ColumnMetaInset": {metrics.ColumnMetaInset, 2},
		"CardRail":        {metrics.CardRail, 1},
		"CardPadRight":    {metrics.CardPadRight, 1},
		"CardMinInner":    {metrics.CardMinInner, 6},
		"MinColumnWidth":  {metrics.MinColumnWidth, 16},
		"OverlayInsetX":   {metrics.OverlayInsetX, 2},
		"OverlayLabelW":   {metrics.OverlayLabelW, 12},
		"TableGutter":     {metrics.TableGutter, 1},
		"CompactBelow":    {metrics.CompactBelow, 30},
		"CompactInnerW":   {metrics.CompactInnerW, 22},
		"DescTwoLines":    {metrics.DescTwoLines, 45},
		"OverlayWidthPct": {metrics.Overlay.WidthPct, 85},
		"OverlayHeightPc": {metrics.Overlay.HeightPct, 88},
		"OverlaySlackW":   {metrics.Overlay.FrameSlackW, 2},
		"OverlaySlackH":   {metrics.Overlay.FrameSlackH, 2},
		"OverlayNarrowW":  {metrics.Overlay.NarrowSlackW, 4},
		"OverlayNarrowH":  {metrics.Overlay.NarrowSlackH, 2},
		"OverlayMinPaneW": {metrics.Overlay.MinPaneW, 24},
		"OverlayMinPaneH": {metrics.Overlay.MinPaneH, 8},
		"OverlayMinW":     {metrics.Overlay.MinW, 24},
		"OverlayMinH":     {metrics.Overlay.MinH, 8},
		"OverlayContent":  {metrics.Overlay.ContentMax, 96},
		"TaskAction":      {metrics.Overlay.TaskAction, 72},
		"HelpWidth":       {metrics.Overlay.Help, 56},
	}
	for name, pair := range cases {
		if pair[0] != pair[1] {
			t.Errorf("%s = %d, spec says %d", name, pair[0], pair[1])
		}
	}
}

func TestGlyphsCarryTheAccentVocabulary(t *testing.T) {
	glyphs := New(true).Glyph
	cases := map[string][2]string{
		"Rail":     {glyphs.Rail, "▌"},
		"RailFull": {glyphs.RailFull, "█"},
		"CapL":     {glyphs.CapL, "▐"},
		"CapR":     {glyphs.CapR, "▌"},
		"Dot":      {glyphs.Dot, "●"},
		"Check":    {glyphs.Check, "☐"},
		"CheckOn":  {glyphs.CheckOn, "☑"},
		"CheckOff": {glyphs.CheckOff, "☒"},
		"Tick":     {glyphs.Tick, "✓"},
		"Diamond":  {glyphs.Diamond, "◇"},
		"Focus":    {glyphs.Focus, "▸"},
		"More":     {glyphs.More, "+"},
		"Ellipsis": {glyphs.Ellipsis, "…"},
		"Blocked":  {glyphs.Blocked, "⛔"},
		"Track":    {glyphs.Track, "░"},
		"Empty":    {glyphs.Empty, "○"},
		"Alert":    {glyphs.Alert, "▲"},
		"Bullet":   {glyphs.Bullet, "·"},
		"HintSep":  {glyphs.HintSep, " | "},

		"HalfTop":    {glyphs.HalfTop, "▀"},
		"HalfBottom": {glyphs.HalfBottom, "▄"},

		"MarkSeq": {glyphs.MarkSeq, "#"},
		"MarkTag": {glyphs.MarkTag, "#"},
		"MarkDue": {glyphs.MarkDue, "!"},

		"MarkFilterOff": {glyphs.MarkFilterOff, "+ "},
		"MarkFilterOn":  {glyphs.MarkFilterOn, "x "},
	}
	for name, pair := range cases {
		if pair[0] != pair[1] {
			t.Errorf("glyph %s = %q, spec says %q", name, pair[0], pair[1])
		}
	}
}

// TestGlyphWidthsMatchTheSpecTable is the second guard of spec section 10.4.1:
// a future re-spelling that silently changes a mark's width fails the build
// rather than the layout. Every token is one cell except Blocked and the two
// filter marks, which are two, and HintSep, which is three; that is what makes
// Blocked ineligible as a state alternative to any one-cell mark under the
// no-reflow rule of section 10.4.4, and what makes the filter marks eligible as
// alternatives to each other.
func TestGlyphWidthsMatchTheSpecTable(t *testing.T) {
	glyphs := New(true).Glyph
	cases := map[string][2]int{
		"Rail":     {ansi.StringWidth(glyphs.Rail), 1},
		"RailFull": {ansi.StringWidth(glyphs.RailFull), 1},
		"CapL":     {ansi.StringWidth(glyphs.CapL), 1},
		"CapR":     {ansi.StringWidth(glyphs.CapR), 1},
		"Dot":      {ansi.StringWidth(glyphs.Dot), 1},
		"Check":    {ansi.StringWidth(glyphs.Check), 1},
		"CheckOn":  {ansi.StringWidth(glyphs.CheckOn), 1},
		"CheckOff": {ansi.StringWidth(glyphs.CheckOff), 1},
		"Tick":     {ansi.StringWidth(glyphs.Tick), 1},
		"Diamond":  {ansi.StringWidth(glyphs.Diamond), 1},
		"Focus":    {ansi.StringWidth(glyphs.Focus), 1},
		"More":     {ansi.StringWidth(glyphs.More), 1},
		"Ellipsis": {ansi.StringWidth(glyphs.Ellipsis), 1},
		"Blocked":  {ansi.StringWidth(glyphs.Blocked), 2},
		"Track":    {ansi.StringWidth(glyphs.Track), 1},
		"Empty":    {ansi.StringWidth(glyphs.Empty), 1},
		"Alert":    {ansi.StringWidth(glyphs.Alert), 1},
		"Bullet":   {ansi.StringWidth(glyphs.Bullet), 1},
		"HintSep":  {ansi.StringWidth(glyphs.HintSep), 3},

		"HalfTop":    {ansi.StringWidth(glyphs.HalfTop), 1},
		"HalfBottom": {ansi.StringWidth(glyphs.HalfBottom), 1},

		"MarkSeq": {ansi.StringWidth(glyphs.MarkSeq), 1},
		"MarkTag": {ansi.StringWidth(glyphs.MarkTag), 1},
		"MarkDue": {ansi.StringWidth(glyphs.MarkDue), 1},

		// The filter marks are two cells each - the mark and its separating
		// column - and equal to each other, which is what lets a filter label
		// pill toggle in place under section 10.4.4.
		"MarkFilterOff": {ansi.StringWidth(glyphs.MarkFilterOff), 2},
		"MarkFilterOn":  {ansi.StringWidth(glyphs.MarkFilterOn), 2},
	}
	if got := reflect.TypeOf(glyphs).NumField(); got != len(cases) {
		t.Fatalf("Glyphs has %d fields, the width table covers %d", got, len(cases))
	}
	for name, pair := range cases {
		if pair[0] != pair[1] {
			t.Errorf("glyph %s is %d cells, spec says %d", name, pair[0], pair[1])
		}
	}
}

// The code points a glyph token may never carry, whatever else it is made of.
// A variation selector re-presents the rune before it, a zero-width joiner
// welds two pictographs into one, and a skin-tone modifier recolors the one
// before it - every one of them makes the token a sequence whose rendered width
// is a property of the terminal's grapheme segmentation rather than of the
// runes, which is exactly the thing the width table of spec section 10.4.1
// promises kb has pinned.
const (
	variationSelector15 = 0xFE0E
	variationSelector16 = 0xFE0F
	zeroWidthJoiner     = 0x200D
	skinToneLo          = 0x1F3FB
	skinToneHi          = 0x1F3FF
)

// TestEmojiGlyphsAreSingleWideCodePoints is the emoji admission rule of spec
// section 10.4.1, added by issue #223 with the effort squares. A pictograph in
// the vocabulary must be one code point with Emoji_Presentation=Yes: no
// variation-selector form, no zero-width-joiner sequence, no modifier. Such a
// glyph is East Asian Wide by construction, so it is two cells to
// ansi.StringWidth and two cells to every terminal that measures the same way,
// and the width table can declare Cells = 2 and mean it.
//
// The walk checks the consequence rather than the property name, because
// Emoji_Presentation is not in the standard library and is not worth vendoring
// a table for: East Asian Wide is what Emoji_Presentation=Yes guarantees under
// UAX #11, and it is also the only part of the property the layout arithmetic
// actually consumes. So the rule enforced here is that every East Asian Wide
// token is a lone rune measuring two cells, and that no token anywhere in the
// vocabulary carries a selector, a joiner or a modifier. A pictograph smuggled
// in as a VS16 sequence fails the second clause; one smuggled in with its text
// presentation fails the first, because it is not Wide and so cannot claim the
// two cells the table would have to give it.
func TestEmojiGlyphsAreSingleWideCodePoints(t *testing.T) {
	glyphs := reflect.ValueOf(New(true).Glyph)
	wide := 0
	for index := range glyphs.NumField() {
		name := glyphs.Type().Field(index).Name
		token := glyphs.Field(index).String()
		for _, r := range token {
			switch {
			case r == variationSelector15 || r == variationSelector16:
				t.Errorf("glyph %s carries a variation selector U+%04X; the vocabulary admits single code points only", name, r)
			case r == zeroWidthJoiner:
				t.Errorf("glyph %s carries a zero-width joiner; the vocabulary admits single code points only", name)
			case r >= skinToneLo && r <= skinToneHi:
				t.Errorf("glyph %s carries a skin-tone modifier U+%04X; the vocabulary admits single code points only", name, r)
			}
		}
		runes := []rune(token)
		isWide := false
		for _, r := range runes {
			if width.LookupRune(r).Kind() == width.EastAsianWide {
				isWide = true
			}
		}
		if !isWide {
			continue
		}
		wide++
		if len(runes) != 1 {
			t.Errorf("glyph %s is %d code points; a wide pictograph must be exactly one", name, len(runes))
			continue
		}
		if got := ansi.StringWidth(token); got != 2 {
			t.Errorf("glyph %s is East Asian Wide but measures %d cells, not the 2 the width table declares", name, got)
		}
	}
	if wide == 0 {
		t.Fatal("no wide glyphs resolved; the walk would assert nothing")
	}
}

// TestEffortResolvesTheScale pins the section 3.4 effort marker as issue #232
// rewrote it: one palette fill per value of the S/M/L scale for the letter to
// sit on, and no fill at all for a value a hand-edited board carries that the
// scale does not name - the render site draws the Diamond fallback for those,
// and nothing here invents a hue it could be mistaken for a scale value in.
func TestEffortResolvesTheScale(t *testing.T) {
	cases := map[string]struct {
		slot    Slot
		onScale bool
	}{
		"S":  {slot: StatusInfo, onScale: true},
		"M":  {slot: StatusWarn, onScale: true},
		"L":  {slot: Label1, onScale: true},
		"XL": {onScale: false},
		"s":  {onScale: false},
		"":   {onScale: false},
	}
	for value, want := range cases {
		slot, onScale := EffortSlot(value)
		if onScale != want.onScale {
			t.Errorf("EffortSlot(%q) on-scale = %v, want %v", value, onScale, want.onScale)
			continue
		}
		if onScale && slot != want.slot {
			t.Errorf("EffortSlot(%q) = slot %d, want %d", value, slot, want.slot)
		}
	}
	// The ramp runs cool to warm and no two values share a fill: the letter
	// carries the value, but a scale whose hues collided would say the two were
	// the same thing at a glance.
	seen := map[Slot]string{}
	for _, value := range []string{"S", "M", "L"} {
		slot, _ := EffortSlot(value)
		if other, found := seen[slot]; found {
			t.Errorf("effort %q and %q share fill slot %d", other, value, slot)
		}
		seen[slot] = value
	}
}

// TestCraftMetricsAreTheSpecValues pins the tokens section 10 adds to the
// metric set: the band head that holds the label column fixed across focus, the
// button geometry the card-detail pane used to spell as local constants, the
// always-reserved focus gutter, and the meter's two widths.
func TestCraftMetricsAreTheSpecValues(t *testing.T) {
	metrics := defaultMetrics
	cases := map[string][2]int{
		"BandHeadW":      {metrics.BandHeadW, 5},
		"ButtonPadX":     {metrics.ButtonPadX, 1},
		"ButtonGap":      {metrics.ButtonGap, 1},
		"FocusGutterW":   {metrics.FocusGutterW, 1},
		"FocusGutterGap": {metrics.FocusGutterGap, 1},
		"MeterCells":     {metrics.MeterCells, 24},
		"MeterMinCells":  {metrics.MeterMinCells, 6},
		"GradSteps":      {GradSteps, 24},

		"EmptyHeadlineMin": {metrics.EmptyHeadlineMin, 24},
		"EmptyActionMin":   {metrics.EmptyActionMin, 10},
		"ActionGap":        {metrics.ActionGap, 2},

		"BusyGap":       {metrics.BusyGap, 1},
		"ErrorMaxLines": {metrics.ErrorMaxLines, 3},
	}
	for name, pair := range cases {
		if pair[0] != pair[1] {
			t.Errorf("%s = %d, spec says %d", name, pair[0], pair[1])
		}
	}
}

// TestBrandMetricsAreTheSpecValues is the token table of spec section 10.6.8.
// None of these may be a literal at a call site, and the mark width identity is
// asserted here rather than in the widget so a re-spelled kern fails the token
// that owns it.
func TestBrandMetricsAreTheSpecValues(t *testing.T) {
	metrics := defaultMetrics
	cases := map[string][2]int{
		"BrandMarkW":       {metrics.BrandMarkW, 10},
		"BrandMarkH":       {metrics.BrandMarkH, 5},
		"BrandKern":        {metrics.BrandKern, 1},
		"BrandStretchMax":  {metrics.BrandStretchMax, 2},
		"BrandMetaW":       {metrics.BrandMetaW, 48},
		"BrandMetaGap":     {metrics.BrandMetaGap, 2},
		"BrandMetaGapRows": {metrics.BrandMetaGapRows, 1},
		"BrandMinW":        {metrics.BrandMinW, 16},
		"BrandMinH":        {metrics.BrandMinH, 9},
		"BrandBirthSteps":  {DefaultTiming.BrandBirthSteps, 12},
		"BrandBlockH":      {metrics.BrandBlockH(), 7},
	}
	for name, pair := range cases {
		if pair[0] != pair[1] {
			t.Errorf("%s = %d, spec says %d", name, pair[0], pair[1])
		}
	}
	// k(4) + kern(1) + b(5) is the unstretched width, and the stretch adds to
	// it, so the mark is 10 to 12 columns and never anything else.
	if metrics.BrandMarkW != 4+metrics.BrandKern+5 {
		t.Errorf("BrandMarkW %d is not k + kern + b", metrics.BrandMarkW)
	}
}

// TestBrandMetaWidthTakesTheFrameCap is the width ladder of spec section
// 10.6.5: the meta row is BrandMetaW until the frame cannot hold it, then the
// frame less a page margin either side, and never negative.
func TestBrandMetaWidthTakesTheFrameCap(t *testing.T) {
	metrics := defaultMetrics
	cases := map[int]int{
		200: metrics.BrandMetaW,
		50:  metrics.BrandMetaW,
		40:  38,
		16:  14,
		2:   0,
		0:   0,
		-8:  0,
	}
	for frame, want := range cases {
		if got := metrics.BrandMetaWidth(frame); got != want {
			t.Errorf("BrandMetaWidth(%d) = %d, want %d", frame, got, want)
		}
	}
}

// TestBrandFitsGuardsBothAxes is the drop rule of spec section 10.6.7: below
// either floor the full mark is dropped and the meta row renders alone.
func TestBrandFitsGuardsBothAxes(t *testing.T) {
	metrics := defaultMetrics
	cases := []struct {
		width, height int
		want          bool
	}{
		{80, 24, true},
		{metrics.BrandMinW, metrics.BrandMinH, true},
		{metrics.BrandMinW - 1, metrics.BrandMinH, false},
		{metrics.BrandMinW, metrics.BrandMinH - 1, false},
		{0, 0, false},
	}
	for _, test := range cases {
		if got := metrics.BrandFits(test.width, test.height); got != test.want {
			t.Errorf("BrandFits(%d, %d) = %v, want %v", test.width, test.height, got, test.want)
		}
	}
}

// TestOverlayFocusContentReservesTheGutter is spec section 10.4.3: a focusable
// body row's prose column is two narrower than a static row on the same panel,
// in every state, so focus never reflows the text it lands on.
func TestOverlayFocusContentReservesTheGutter(t *testing.T) {
	metrics := defaultMetrics
	for _, pane := range []int{200, 120, 40, 24, 6, 1, 0, -3} {
		focus := metrics.OverlayFocusContent(pane)
		static := metrics.OverlayContent(pane)
		if focus > static {
			t.Errorf("pane %d: focusable measure %d exceeds the static %d", pane, focus, static)
		}
		if focus < 1 {
			t.Errorf("pane %d: focusable measure %d, want at least one column", pane, focus)
		}
		if static > 3 && focus != static-2 {
			t.Errorf("pane %d: focusable measure %d, want %d", pane, focus, static-2)
		}
	}
}
