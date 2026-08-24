package widget

import (
	"math/rand/v2"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// brandCell is one cell of a letterform on the half-block grid of spec section
// 10.6.1. Each terminal cell carries the full block, the upper half, the lower
// half or nothing, which gives five rows ten half-rows of vertical resolution.
//
// The letterforms are a cell grid rather than crush's joined string block for
// one reason: the reveal of spec section 10.6.6 is a per-column effect, so the
// mark has to be addressable by column before it is painted. Joining rendered
// strings horizontally would hand the reveal a run it would have to re-split.
type brandCell uint8

const (
	cellBlank brandCell = iota
	cellFull
	cellTop
	cellBottom
)

// letterK is the four-column k of spec section 10.6.1. The stem is rows 0-4;
// the arm is a pair of diagonal steps, which is why k is not the stretchable
// letter - repeating a step breaks the slope instead of lengthening the arm.
var letterK = [][]brandCell{
	{cellFull, cellBlank, cellBlank, cellBlank},
	{cellFull, cellBlank, cellBlank, cellBlank},
	{cellFull, cellBlank, cellBottom, cellTop},
	{cellFull, cellFull, cellBlank, cellBlank},
	{cellFull, cellBlank, cellTop, cellBottom},
}

// letterB is the five-column b of spec section 10.6.1. Column index
// brandStretchColumn is the repeatable vertical slice the stretch of section
// 10.6.2 duplicates.
var letterB = [][]brandCell{
	{cellFull, cellBlank, cellBlank, cellBlank, cellBlank},
	{cellFull, cellBlank, cellBlank, cellBlank, cellBlank},
	{cellFull, cellTop, cellTop, cellTop, cellBottom},
	{cellFull, cellBlank, cellBlank, cellBlank, cellFull},
	{cellFull, cellBottom, cellBottom, cellBottom, cellTop},
}

// brandStretchColumn is b's repeatable slice: the bowl's top rail at row 2 and
// its bottom rail at row 4, blank elsewhere (spec section 10.6.2).
const brandStretchColumn = 2

// BrandOpts is the whole input of the launch block. Stretch, Frame and Seed are
// all resolved by the caller, which is what makes both the memoized width of
// spec section 10.6.2 and the reveal of section 10.6.6 pinnable without any
// package-level cache: this widget is pure and draws nothing on its own.
type BrandOpts struct {
	Styles *theme.Styles
	Width  int // frame width
	Height int // frame height

	Stretch int   // extra columns of bowl, memoized by the caller
	Frame   int   // reveal frame; at or above Timing.BrandBirthSteps the mark is settled
	Seed    int64 // reveal seed, memoized by the caller

	Status     string     // the board's resolved state string
	StatusSlot theme.Slot // the hue that resolver returned
	Version    string     // the build version, unprefixed
	On         theme.Slot // the surface every returned row carries edge to edge
}

// Brand renders the launch block of spec section 10.6: the mark, the blank
// rows under it and the meta row, one string per row, each carrying On edge to
// edge at the caller's frame width. Vertical placement is the caller's, which
// is what keeps the block itself frame-height independent.
//
// Below the frame floors of section 10.6.7 the mark is dropped and the meta row
// is returned alone, because the meta row is the half that carries facts.
func Brand(o BrandOpts) []string {
	if o.Styles == nil {
		return nil
	}
	metrics := o.Styles.Metrics
	width := max(o.Width, 0)
	meta := brandMetaRow(o, width)
	if !metrics.BrandFits(width, o.Height) {
		return []string{meta}
	}
	rows := brandMarkRows(o, width)
	for range metrics.BrandMetaGapRows {
		rows = append(rows, brandBlankRow(o.Styles, o.On, width))
	}
	return append(rows, meta)
}

// RollBrand draws the once-per-process stretch and reveal seed of spec section
// 10.6.2. It is called from NewModel and nowhere else: a mark that re-rolled
// its width on every render would jitter on resize and read as a rendering
// fault rather than as character.
func RollBrand(metrics theme.Metrics) (stretch int, seed int64) {
	// A collapsed stretch range degenerates to a single width rather than
	// handing rand.IntN an empty interval.
	return rand.IntN(max(metrics.BrandStretchMax+1, 1)), rand.Int64()
}

// BrandMarkWidth is the assembled mark's column count: the two letterforms, the
// kern between them and the memoized stretch.
func BrandMarkWidth(metrics theme.Metrics, stretch int) int {
	return metrics.BrandMarkW + brandStretch(metrics, stretch)
}

// brandStretch clamps a caller's stretch onto [0, BrandStretchMax], so a pinned
// value outside the roll cannot widen the mark past its declared range.
func brandStretch(metrics theme.Metrics, stretch int) int {
	if stretch < 0 {
		return 0
	}
	if stretch > metrics.BrandStretchMax {
		return metrics.BrandStretchMax
	}
	return stretch
}

// brandGrid assembles the padded cell grid of the whole mark. Every row is
// padded to the full mark width before it is painted, which spec section 10.6.3
// depends on: a sparse row ramped over its own painted cells would tilt the
// mark's color against its own geometry.
func brandGrid(metrics theme.Metrics, stretch int) [][]brandCell {
	extra := brandStretch(metrics, stretch)
	rows := make([][]brandCell, 0, len(letterK))
	for index, left := range letterK {
		right := letterB[index]
		row := make([]brandCell, 0, BrandMarkWidth(metrics, extra))
		row = append(row, left...)
		for range metrics.BrandKern {
			row = append(row, cellBlank)
		}
		row = append(row, right[:brandStretchColumn+1]...)
		for range extra {
			row = append(row, right[brandStretchColumn])
		}
		row = append(row, right[brandStretchColumn+1:]...)
		rows = append(rows, row)
	}
	return rows
}

// brandBirths is the per-column birth schedule of spec section 10.6.6, drawn
// from a PCG source seeded with the caller's memoized seed. Every value is
// strictly below Timing.BrandBirthSteps, so the frame at that count is the
// settled frame by construction and the reveal terminates on its own.
func brandBirths(steps, columns int, seed int64) []int {
	births := make([]int, columns)
	if steps < 1 {
		return births
	}
	prng := rand.New(rand.NewPCG(uint64(seed), 0))
	for column := range births {
		births[column] = prng.IntN(steps)
	}
	return births
}

// brandMarkRows paints the mark centered on the frame. The reveal is a staggered
// color wipe, never a shape wipe: an unborn column draws its own final glyphs in
// FgMuted and only its hue changes when it is born (spec section 10.6.6).
func brandMarkRows(o BrandOpts, width int) []string {
	styles := o.Styles
	metrics := styles.Metrics
	grid := brandGrid(metrics, o.Stretch)
	markWidth := BrandMarkWidth(metrics, o.Stretch)
	births := brandBirths(styles.Timing.BrandBirthSteps, markWidth, o.Seed)
	unborn := styles.On(theme.FgMuted, o.On)
	left := max((width-markWidth)/2, 0)

	rows := make([]string, 0, len(grid))
	for _, cells := range grid {
		var row strings.Builder
		row.WriteString(strings.Repeat(" ", left))
		painted := left
		for column, cell := range cells {
			if painted >= width {
				break
			}
			glyph := brandGlyph(styles, cell)
			if o.Frame < births[column] {
				row.WriteString(unborn.Render(glyph))
			} else {
				row.WriteString(styles.GradCell(theme.GradWork, column, markWidth, glyph))
			}
			painted++
		}
		rows = append(rows, styles.SurfaceRun(o.On, row.String()+strings.Repeat(" ", max(width-painted, 0))))
	}
	return rows
}

// brandGlyph resolves one cell to its vocabulary token. A blank cell is a space
// so the padded row still carries its surface across the gap.
func brandGlyph(styles *theme.Styles, cell brandCell) string {
	switch cell {
	case cellFull:
		return styles.Glyph.RailFull
	case cellTop:
		return styles.Glyph.HalfTop
	case cellBottom:
		return styles.Glyph.HalfBottom
	default:
		return " "
	}
}

// brandMetaRow is the row of spec section 10.6.5: brand context left, the build
// version right-aligned by computed gap, centered on the same frame center as
// the mark.
//
// The version is never truncated and the left slot is, which is the same rule
// as the #seq of spec section 3.2. A left allotment below brandMetaLeftMin is
// dropped entirely and the version renders alone.
func brandMetaRow(o BrandOpts, width int) string {
	styles := o.Styles
	metrics := styles.Metrics
	metaWidth := metrics.BrandMetaWidth(width)
	left := max((width-metaWidth)/2, 0)

	version := brandVersionText(o.Version)
	versionWidth := ansi.StringWidth(version)
	room := metaWidth
	if version != "" {
		room = metaWidth - versionWidth - metrics.BrandMetaGap
	}
	status := ""
	if room >= brandMetaLeftMin {
		status = truncate(styles, o.Status, room)
	}

	statusWidth := ansi.StringWidth(status)
	gap := max(metaWidth-statusWidth-versionWidth, 0)
	var row strings.Builder
	row.WriteString(strings.Repeat(" ", left))
	if status != "" {
		row.WriteString(styles.On(o.StatusSlot, o.On).Render(status))
	}
	row.WriteString(strings.Repeat(" ", gap))
	if version != "" {
		row.WriteString(styles.On(theme.FgSubtle, o.On).Render(version))
	}
	// A frame too narrow to hold the version at all still clips at its own
	// edge: the width cap is the frame, and the no-truncation rule of section
	// 10.6.5 is about the meta row's own allotment, not about overrunning it.
	line := ansi.Truncate(row.String(), width, "")
	painted := min(left+statusWidth+gap+versionWidth, width)
	return styles.SurfaceRun(o.On, line+strings.Repeat(" ", max(width-painted, 0)))
}

// brandMetaLeftMin is the left slot's floor of spec section 10.6.5: below four
// columns the slot is dropped rather than rendered as an ellipsis and a letter.
const brandMetaLeftMin = 4

// brandVersionText is the version slot's content: the display version prefixed
// with v, or devel and unknown unprefixed (spec section 10.6.5).
//
// The prefix is skipped when the version already carries one, because
// debug.BuildInfo reports a tagged module as v1.2.0 and vv1.2.0 is not a
// version anyone can quote into a bug report.
func brandVersionText(version string) string {
	switch version {
	case "", "devel", "unknown":
		return version
	}
	if strings.HasPrefix(version, "v") {
		return version
	}
	return "v" + version
}

// brandBlankRow is one Canvas row of the gap between the mark and the meta row.
func brandBlankRow(styles *theme.Styles, on theme.Slot, width int) string {
	return styles.SurfaceRun(on, strings.Repeat(" ", max(width, 0)))
}
