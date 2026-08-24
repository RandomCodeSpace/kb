package theme

import (
	"reflect"
	"testing"

	"github.com/charmbracelet/x/ansi"
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
		"Diamond":  {glyphs.Diamond, "◇"},
		"Focus":    {glyphs.Focus, "▸"},
		"More":     {glyphs.More, "+"},
		"Ellipsis": {glyphs.Ellipsis, "…"},
		"Blocked":  {glyphs.Blocked, "⛔"},
		"Track":    {glyphs.Track, "░"},
		"Empty":    {glyphs.Empty, "○"},
		"Alert":    {glyphs.Alert, "▲"},
		"HintSep":  {glyphs.HintSep, " | "},

		"HalfTop":    {glyphs.HalfTop, "▀"},
		"HalfBottom": {glyphs.HalfBottom, "▄"},

		"MarkPrio": {glyphs.MarkPrio, "P"},
		"MarkSeq":  {glyphs.MarkSeq, "#"},
		"MarkTag":  {glyphs.MarkTag, "#"},
		"MarkDue":  {glyphs.MarkDue, "!"},
	}
	for name, pair := range cases {
		if pair[0] != pair[1] {
			t.Errorf("glyph %s = %q, spec says %q", name, pair[0], pair[1])
		}
	}
}

// TestGlyphWidthsMatchTheSpecTable is the second guard of spec section 10.4.1:
// a future re-spelling that silently changes a mark's width fails the build
// rather than the layout. Every token is one cell except Blocked, which is two,
// and that is what makes Blocked ineligible as a state alternative to any
// one-cell mark under the no-reflow rule of section 10.4.4.
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
		"Diamond":  {ansi.StringWidth(glyphs.Diamond), 1},
		"Focus":    {ansi.StringWidth(glyphs.Focus), 1},
		"More":     {ansi.StringWidth(glyphs.More), 1},
		"Ellipsis": {ansi.StringWidth(glyphs.Ellipsis), 1},
		"Blocked":  {ansi.StringWidth(glyphs.Blocked), 2},
		"Track":    {ansi.StringWidth(glyphs.Track), 1},
		"Empty":    {ansi.StringWidth(glyphs.Empty), 1},
		"Alert":    {ansi.StringWidth(glyphs.Alert), 1},
		"HintSep":  {ansi.StringWidth(glyphs.HintSep), 3},

		"HalfTop":    {ansi.StringWidth(glyphs.HalfTop), 1},
		"HalfBottom": {ansi.StringWidth(glyphs.HalfBottom), 1},

		"MarkPrio": {ansi.StringWidth(glyphs.MarkPrio), 1},
		"MarkSeq":  {ansi.StringWidth(glyphs.MarkSeq), 1},
		"MarkTag":  {ansi.StringWidth(glyphs.MarkTag), 1},
		"MarkDue":  {ansi.StringWidth(glyphs.MarkDue), 1},
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
