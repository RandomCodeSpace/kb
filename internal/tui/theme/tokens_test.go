package theme

import "testing"

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
	}
	for name, pair := range cases {
		if pair[0] != pair[1] {
			t.Errorf("glyph %s = %q, spec says %q", name, pair[0], pair[1])
		}
	}
}
