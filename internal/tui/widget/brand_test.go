package widget

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// brandTestOpts is the settled block on a wide frame, which is the shape every
// geometry assertion below varies one field of.
func brandTestOpts(styles *theme.Styles) BrandOpts {
	return BrandOpts{
		Styles:     styles,
		Width:      60,
		Height:     24,
		Frame:      styles.Timing.BrandBirthSteps,
		Status:     "loading board...",
		StatusSlot: theme.FgMuted,
		Version:    "1.2.0",
		On:         theme.Canvas,
	}
}

// plainRows strips the styling a row carries so an assertion can read its
// geometry.
func plainRows(rows []string) []string {
	out := make([]string, len(rows))
	for index, row := range rows {
		out[index] = ansi.Strip(row)
	}
	return out
}

// TestBrandLetterformsAreTheSpecArt is the normative art of spec section
// 10.6.1: two lowercase letterforms on the half-block grid, five rows by ten
// columns assembled, every row padded to the full mark width.
func TestBrandLetterformsAreTheSpecArt(t *testing.T) {
	styles := theme.New(true)
	options := brandTestOpts(styles)
	options.Width = styles.Metrics.BrandMarkW
	rows := plainRows(brandMarkRows(options, options.Width))
	want := []string{
		"█    █    ",
		"█    █    ",
		"█ ▄▀ █▀▀▀▄",
		"██   █   █",
		"█ ▀▄ █▄▄▄▀",
	}
	if len(rows) != len(want) {
		t.Fatalf("mark is %d rows, want %d", len(rows), len(want))
	}
	for index, row := range rows {
		if row != want[index] {
			t.Errorf("mark row %d = %q, want %q", index, row, want[index])
		}
	}
}

// TestBrandStretchRepeatsTheBowlSlice is spec section 10.6.2: the random
// dimension is the amount, not the letter, and the repeated slice is b's column
// index 2 - the bowl's two rails and blank elsewhere.
func TestBrandStretchRepeatsTheBowlSlice(t *testing.T) {
	styles := theme.New(true)
	want := map[int][]string{
		0: {
			"█    █    ",
			"█    █    ",
			"█ ▄▀ █▀▀▀▄",
			"██   █   █",
			"█ ▀▄ █▄▄▄▀",
		},
		2: {
			"█    █      ",
			"█    █      ",
			"█ ▄▀ █▀▀▀▀▀▄",
			"██   █     █",
			"█ ▀▄ █▄▄▄▄▄▀",
		},
	}
	for stretch, rows := range want {
		options := brandTestOpts(styles)
		options.Stretch = stretch
		options.Width = BrandMarkWidth(styles.Metrics, stretch)
		got := plainRows(brandMarkRows(options, options.Width))
		for index, row := range got {
			if row != rows[index] {
				t.Errorf("stretch %d row %d = %q, want %q", stretch, index, row, rows[index])
			}
		}
	}
}

// TestBrandMarkWidthStaysInsideItsDeclaredRange is the other half of section
// 10.6.2: the mark is 10 to 12 columns, and a stretch outside the roll is
// clamped rather than widening it past the range the tokens declare.
func TestBrandMarkWidthStaysInsideItsDeclaredRange(t *testing.T) {
	metrics := theme.New(true).Metrics
	for stretch := -3; stretch <= metrics.BrandStretchMax+3; stretch++ {
		width := BrandMarkWidth(metrics, stretch)
		if width < metrics.BrandMarkW || width > metrics.BrandMarkW+metrics.BrandStretchMax {
			t.Errorf("stretch %d gives width %d, outside [%d, %d]",
				stretch, width, metrics.BrandMarkW, metrics.BrandMarkW+metrics.BrandStretchMax)
		}
	}
}

// TestRollBrandStaysInsideTheStretchRange pins the memo's draw. The roll runs
// once per process in NewModel; what matters here is only that it can never
// hand the widget a width outside the declared range.
func TestRollBrandStaysInsideTheStretchRange(t *testing.T) {
	metrics := theme.New(true).Metrics
	seen := map[int]bool{}
	for range 200 {
		stretch, seed := RollBrand(metrics)
		if stretch < 0 || stretch > metrics.BrandStretchMax {
			t.Fatalf("RollBrand stretch %d is outside [0, %d]", stretch, metrics.BrandStretchMax)
		}
		seen[stretch] = true
		_ = seed
	}
	if len(seen) < 2 {
		t.Fatalf("RollBrand produced only %v across 200 draws", seen)
	}
	// A collapsed metric set is the test configuration of spec section 10.3.1
	// applied to a count: the roll degenerates to a single width rather than
	// panicking on an empty range.
	if stretch, _ := RollBrand(theme.Metrics{}); stretch != 0 {
		t.Fatalf("a zero stretch range rolled %d", stretch)
	}
}

// TestBrandMarkClipsAtTheFrameEdge covers the mark's own clip. The frame floors
// of spec section 10.6.7 keep Brand from ever reaching it, so it is asserted
// here directly: a mark wider than its frame stops at the edge instead of
// overrunning the row.
func TestBrandMarkClipsAtTheFrameEdge(t *testing.T) {
	styles := theme.New(true)
	options := brandTestOpts(styles)
	for _, width := range []int{6, 3, 1} {
		options.Width = width
		for index, row := range plainRows(brandMarkRows(options, width)) {
			if len([]rune(row)) != width {
				t.Errorf("width %d row %d = %q", width, index, row)
			}
		}
	}
}

// TestBrandBlockIsTheSpecGeometry is spec section 10.6.7: the block is the mark,
// BrandMetaGapRows blank rows and the meta row, every row exactly the frame
// width, and the mark centered on the frame.
func TestBrandBlockIsTheSpecGeometry(t *testing.T) {
	styles := theme.New(true)
	metrics := styles.Metrics
	options := brandTestOpts(styles)
	rows := Brand(options)
	if len(rows) != metrics.BrandBlockH() {
		t.Fatalf("block is %d rows, want %d", len(rows), metrics.BrandBlockH())
	}
	for index, row := range rows {
		if got := ansi.StringWidth(row); got != options.Width {
			t.Errorf("block row %d is %d columns, want %d", index, got, options.Width)
		}
	}
	plain := plainRows(rows)
	if strings.TrimSpace(plain[metrics.BrandMarkH]) != "" {
		t.Errorf("gap row = %q, want blank", plain[metrics.BrandMarkH])
	}
	markWidth := BrandMarkWidth(metrics, options.Stretch)
	wantLeft := (options.Width - markWidth) / 2
	if got := len(plain[0]) - len(strings.TrimLeft(plain[0], " ")); got != wantLeft {
		t.Errorf("mark origin column %d, want %d", got, wantLeft)
	}
}

// TestBrandDropsTheMarkBelowTheFrameFloors is the other arm of section 10.6.7:
// the meta row is the half that carries facts, so it is the half that survives.
func TestBrandDropsTheMarkBelowTheFrameFloors(t *testing.T) {
	styles := theme.New(true)
	metrics := styles.Metrics
	for _, frame := range [][2]int{
		{metrics.BrandMinW - 1, 40},
		{40, metrics.BrandMinH - 1},
		{4, 4},
	} {
		options := brandTestOpts(styles)
		options.Width, options.Height = frame[0], frame[1]
		rows := Brand(options)
		if len(rows) != 1 {
			t.Fatalf("frame %dx%d returned %d rows, want the meta row alone", frame[0], frame[1], len(rows))
		}
		if got := ansi.StringWidth(rows[0]); got != max(frame[0], 0) {
			t.Errorf("frame %dx%d meta row is %d columns, want %d", frame[0], frame[1], got, frame[0])
		}
	}
}

// TestBrandMetaRowTruncatesTheLeftSlotOnly is spec section 10.6.5: the version
// is never truncated, the left slot is, and a left allotment below four columns
// is dropped so the version renders alone.
func TestBrandMetaRowTruncatesTheLeftSlotOnly(t *testing.T) {
	styles := theme.New(true)
	metrics := styles.Metrics
	long := "loading a board with a deliberately unreasonable status line"
	for _, width := range []int{60, 40, 24, metrics.BrandMinW} {
		options := brandTestOpts(styles)
		options.Width = width
		options.Status = long
		row := ansi.Strip(brandMetaRow(options, width))
		if !strings.Contains(row, "v1.2.0") {
			t.Fatalf("width %d dropped the version: %q", width, row)
		}
		if ansi.StringWidth(row) != width {
			t.Fatalf("width %d meta row is %d columns", width, ansi.StringWidth(row))
		}
		body := strings.TrimSpace(row)
		if body != "v1.2.0" && !strings.HasSuffix(body, styles.Glyph.Ellipsis+strings.Repeat(" ", 0)+"") {
			// The left slot either survives truncated or is dropped entirely;
			// a surviving slot always wears the section 3.3 ellipsis here
			// because the fixture status is longer than any allotment.
			if !strings.Contains(body, styles.Glyph.Ellipsis) {
				t.Fatalf("width %d left slot neither truncated nor dropped: %q", width, row)
			}
		}
	}
}

// TestBrandMetaRowDropsTheLeftSlotUnderFourColumns pins the floor itself.
func TestBrandMetaRowDropsTheLeftSlotUnderFourColumns(t *testing.T) {
	styles := theme.New(true)
	options := brandTestOpts(styles)
	// A frame whose meta allotment leaves the left slot under brandMetaLeftMin:
	// 13 columns caps the row at 11, and 11 less the version and the gap is 3.
	options.Width = 13
	options.Status = "loading board..."
	row := ansi.Strip(brandMetaRow(options, options.Width))
	if strings.TrimSpace(row) != "v1.2.0" {
		t.Fatalf("meta row = %q, want the version alone", row)
	}
	if !strings.HasSuffix(strings.TrimRight(row, " "), "v1.2.0") {
		t.Fatalf("version is not right-aligned on the meta row: %q", row)
	}
}

// TestBrandMetaRowKeepsTheMinimumGap is the gap arithmetic of section 10.6.5:
// the two slots are never closer than BrandMetaGap columns.
func TestBrandMetaRowKeepsTheMinimumGap(t *testing.T) {
	styles := theme.New(true)
	metrics := styles.Metrics
	for _, status := range []string{"ready", "loading board...", strings.Repeat("x", 80)} {
		options := brandTestOpts(styles)
		options.Status = status
		row := ansi.Strip(brandMetaRow(options, options.Width))
		body := strings.TrimSpace(row)
		if body == "v1.2.0" {
			continue
		}
		gap := strings.LastIndex(body, " ")
		trimmed := strings.TrimRight(body[:gap+1], " ")
		if run := gap + 1 - len(trimmed); run < metrics.BrandMetaGap {
			t.Errorf("status %q left a %d column gap, want at least %d", status, run, metrics.BrandMetaGap)
		}
	}
}

// TestBrandVersionSlotSpelling is the version column of section 10.6.5: the
// display version wears a v, devel and unknown do not, an empty version renders
// the left slot alone, and an already-prefixed version is not double-prefixed.
func TestBrandVersionSlotSpelling(t *testing.T) {
	cases := map[string]string{
		"":        "",
		"devel":   "devel",
		"unknown": "unknown",
		"1.2.0":   "v1.2.0",
		"v1.2.0":  "v1.2.0",
	}
	for version, want := range cases {
		if got := brandVersionText(version); got != want {
			t.Errorf("brandVersionText(%q) = %q, want %q", version, got, want)
		}
	}
	styles := theme.New(true)
	options := brandTestOpts(styles)
	options.Version = ""
	options.Status = "ready"
	row := ansi.Strip(brandMetaRow(options, options.Width))
	if strings.TrimSpace(row) != "ready" {
		t.Fatalf("empty version meta row = %q, want the left slot alone", row)
	}
}

// TestBrandRevealIsAStaggeredColorWipe is spec section 10.6.6: the glyphs never
// change, only their hue does, and the frame at Timing.BrandBirthSteps is the
// settled frame by construction.
func TestBrandRevealIsAStaggeredColorWipe(t *testing.T) {
	styles := theme.New(true)
	settled := brandTestOpts(styles)
	settled.Seed = 7
	first := settled
	first.Frame = 0

	settledRows := Brand(settled)
	firstRows := Brand(first)
	if strings.Join(plainRows(settledRows), "\n") != strings.Join(plainRows(firstRows), "\n") {
		t.Fatal("the reveal changed a glyph; it is a color wipe, not a shape wipe")
	}
	if strings.Join(settledRows, "\n") == strings.Join(firstRows, "\n") {
		t.Fatal("frame 0 renders identically to the settled frame; the reveal is not animating")
	}
	// The frame before the last column's birth still differs, so a reveal that
	// silently stopped animating fails rather than passing quietly.
	markWidth := BrandMarkWidth(styles.Metrics, settled.Stretch)
	latest := 0
	for _, birth := range brandBirths(styles.Timing.BrandBirthSteps, markWidth, settled.Seed) {
		latest = max(latest, birth)
	}
	if latest < 1 {
		t.Fatalf("every column was born at frame 0; the schedule collapsed")
	}
	last := settled
	last.Frame = latest - 1
	if strings.Join(Brand(last), "\n") == strings.Join(settledRows, "\n") {
		t.Fatalf("frame %d already matches the settled frame; the reveal stopped animating", latest-1)
	}
	// Past the settled frame nothing moves again.
	beyond := settled
	beyond.Frame = styles.Timing.BrandBirthSteps * 4
	if strings.Join(Brand(beyond), "\n") != strings.Join(settledRows, "\n") {
		t.Fatal("the mark kept changing after the settled frame")
	}
}

// TestBrandRevealIsSeeded is the determinism half of section 10.6.6: the same
// seed reproduces the same schedule in every process, and a different seed does
// not have to but must still settle onto the same final frame.
func TestBrandRevealIsSeeded(t *testing.T) {
	styles := theme.New(true)
	options := brandTestOpts(styles)
	options.Frame = 3
	options.Seed = 11
	first := strings.Join(Brand(options), "\n")
	if second := strings.Join(Brand(options), "\n"); first != second {
		t.Fatal("the same seed rendered two different frames")
	}
	settledA, settledB := options, options
	settledA.Frame, settledB.Frame = styles.Timing.BrandBirthSteps, styles.Timing.BrandBirthSteps
	settledB.Seed = 99
	if strings.Join(Brand(settledA), "\n") != strings.Join(Brand(settledB), "\n") {
		t.Fatal("two seeds settled onto different marks")
	}
}

// TestBrandBirthsStayBelowTheSpan is the termination guarantee: every birth
// value is strictly below the span, so the frame at the span is settled.
func TestBrandBirthsStayBelowTheSpan(t *testing.T) {
	for _, span := range []int{0, 1, 12, 40} {
		for _, seed := range []int64{0, 1, -5, 1 << 40} {
			for column, birth := range brandBirths(span, 12, seed) {
				if birth < 0 || (span > 0 && birth >= span) || (span <= 0 && birth != 0) {
					t.Fatalf("span %d seed %d column %d birth %d", span, seed, column, birth)
				}
			}
		}
	}
}

// TestBrandWithoutStylesRendersNothing keeps the widget's zero contract: it is
// pure and draws nothing on its own.
func TestBrandWithoutStylesRendersNothing(t *testing.T) {
	if rows := Brand(BrandOpts{Width: 40, Height: 20}); rows != nil {
		t.Fatalf("Brand without styles returned %d rows", len(rows))
	}
}
