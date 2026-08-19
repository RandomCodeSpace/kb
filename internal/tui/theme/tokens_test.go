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
		"MaxColumnWidth":  {metrics.MaxColumnWidth, 52},
		"OverlayInsetX":   {metrics.OverlayInsetX, 2},
		"OverlayLabelW":   {metrics.OverlayLabelW, 12},
		"CompactBelow":    {metrics.CompactBelow, 30},
		"CompactInnerW":   {metrics.CompactInnerW, 22},
		"DescTwoLines":    {metrics.DescTwoLines, 45},
		"OverlayPaneW":    {metrics.Overlay.PaneW, 72},
		"OverlayPaneH":    {metrics.Overlay.PaneH, 13},
		"OverlayMinW":     {metrics.Overlay.MinW, 24},
		"OverlayMinH":     {metrics.Overlay.MinH, 8},
		"CardDetailWidth": {metrics.Overlay.CardDetail, 92},
		"EditorWidth":     {metrics.Overlay.Editor, 96},
		"ADRSplitWidth":   {metrics.Overlay.ADRSplit, 100},
		"IssueImport":     {metrics.Overlay.IssueImport, 88},
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
