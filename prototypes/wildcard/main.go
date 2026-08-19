// Command wildcard renders one static frame of a kb board in the "Strata"
// visual language - a throwaway prototype for ticket #137 (map #136).
//
// Strata in one line: no box-drawing borders anywhere. Depth is carried by
// background shade tiers (canvas -> column surface -> card surface -> raised
// -> overlay), panel identity by a tinted column header band, and every accent
// by half-block glyphs (a 1/2-cell card rail, pill-capped chips).
//
// The argument, in rows and columns:
//
//	rounded-border card, Padding(0,1)   2 content rows + 2 border rows
//	                                    + 1 gutter        = 5 rows / card
//	                                    4 cols of chrome per card
//	Strata card, normal density         4 content rows (title, description,
//	                                    meta chips, label pills) + 0 border
//	                                    + 1 gutter        = 5 rows / card
//	                                    2 cols of chrome (rail + right pad)
//	Strata card, tall frames (h >= 45)  the description gets a second line
//	                                                       = 6 rows / card
//	Strata card, compact density        2 content rows, no description, zebra
//	                                    instead of a gutter = 2 rows / card
//	                                    1 col of chrome (the rail)
//
// A bordered board fits 4 cards in a 20-row column; Strata fits 4 at normal
// density (the description line costs the row the borders gave back) and 10 at
// compact. The card still reads as a distinct surface because it is literally
// a different shade, not because anything is drawn around it.
//
// The description is a muted single line (two on tall frames) under the title,
// word-wrapped to the card's inner width and ellipsized - it never wraps past
// the card. It is the first thing compaction drops.
//
// Compaction (map #136's density decision) fires below 30 rows and drops, in
// order: the description, the page-padding row, the column meta line, the
// inter-card gutter (replaced by zebra striping), the card's label line, the
// card's inner left padding and the column's side padding, and finally the pill
// end caps - chips degrade to flat colored text, scoped labels to their value
// half.
//
// Known risk, flagged rather than hidden: the whole accent vocabulary is
// U+2588/258C/2590 half-blocks. On fonts without block glyphs this degrades
// worse than a rounded border would; there is no ASCII analogue for a pill
// cap. The shade tiers survive that font failure, the pills do not.
//
// Everything here is hardcoded fake data. Nothing in this file is meant to be
// merged into internal/tui; it exists to be looked at and argued with.
//
// Architecture note (follows docs/research/lipgloss-design-system.md):
// a semantic palette of named color slots feeds a cached style factory keyed
// by (fg, bg, bold, dim). No lipgloss.Style is constructed per cell or per
// frame; the frame is composed in a cell grid and styles are looked up per
// run. Overlay separation is done by re-keying the whole board grid to the
// dimmed palette variant and compositing the panel on top - the same thing
// lipgloss.Canvas/Layer would do, minus the trailing-space trim.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"image/color"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// ---------------------------------------------------------------------------
// semantic palette
// ---------------------------------------------------------------------------

// Palette slots are semantic roles, not hues. Truecolor-dark is the reference
// design; every slot is audited for 256-color collisions (-audit256).
type slot uint8

const (
	canvas      slot = iota // page ground, darkest tier
	surface                 // column panel tier
	card                    // card tier
	raised                  // selected / hovered card tier
	overlaySurf             // overlay panel tier (highest)
	overlayBand             // overlay header/footer band
	shadow                  // drop shadow under the overlay
	zebra                   // alternate card tier used by compact density

	fgBase
	fgSubtle
	fgMuted
	fgOnAccent // text drawn on a saturated fill

	brand
	hueTodo
	hueDoing
	hueDone
	hueCancelled

	prio1
	prio2
	prio3
	prio4

	statusOK
	statusWarn
	statusDanger
	statusInfo

	labelKey // dark half of a scoped label pill
	label1
	label2
	label3
	label4
	label5

	numSlots
)

var palette = [numSlots]color.Color{
	// Shade tiers are spaced so their 256-color neighbours stay distinct;
	// see -audit256. This is the whole load-bearing decision of the language.
	canvas:      lipgloss.Color("#0b0e14"),
	surface:     lipgloss.Color("#171d27"),
	zebra:       lipgloss.Color("#1e2632"),
	card:        lipgloss.Color("#252f3d"),
	raised:      lipgloss.Color("#35404f"),
	overlaySurf: lipgloss.Color("#3c495c"),
	overlayBand: lipgloss.Color("#4a5970"),
	shadow:      lipgloss.Color("#05070a"),

	fgBase:     lipgloss.Color("#e3e9f2"),
	fgSubtle:   lipgloss.Color("#9aa5b6"),
	fgMuted:    lipgloss.Color("#6b7686"),
	fgOnAccent: lipgloss.Color("#0b0e14"),

	brand:        lipgloss.Color("#4f8ef7"),
	hueTodo:      lipgloss.Color("#7aa2f7"),
	hueDoing:     lipgloss.Color("#f2a33c"),
	hueDone:      lipgloss.Color("#3fbf7f"),
	hueCancelled: lipgloss.Color("#7b8494"),

	prio1: lipgloss.Color("#ff5a48"),
	prio2: lipgloss.Color("#ffb020"),
	prio3: lipgloss.Color("#4f8ef7"),
	prio4: lipgloss.Color("#b8bdc7"),

	statusOK:     lipgloss.Color("#3fbf7f"),
	statusWarn:   lipgloss.Color("#ffb020"),
	statusDanger: lipgloss.Color("#ff5a48"),
	statusInfo:   lipgloss.Color("#4f8ef7"),

	// labelKey quantizes onto the same 256 index as zebra. They never share a
	// frame: scoped pills only render at normal density, zebra only at compact.
	labelKey: lipgloss.Color("#1b2330"),
	label1:   lipgloss.Color("#ff7b54"),
	label2:   lipgloss.Color("#4f8ef7"),
	label3:   lipgloss.Color("#3f9d58"),
	label4:   lipgloss.Color("#b98af7"),
	label5:   lipgloss.Color("#ffb020"),
}

var slotNames = [numSlots]string{
	canvas: "canvas", surface: "surface", card: "card", raised: "raised",
	overlaySurf: "overlaySurf", overlayBand: "overlayBand", shadow: "shadow", zebra: "zebra",
	fgBase: "fgBase", fgSubtle: "fgSubtle", fgMuted: "fgMuted", fgOnAccent: "fgOnAccent",
	brand: "brand", hueTodo: "hueTodo", hueDoing: "hueDoing", hueDone: "hueDone",
	hueCancelled: "hueCancelled",
	prio1:        "prio1", prio2: "prio2", prio3: "prio3", prio4: "prio4",
	statusOK: "statusOK", statusWarn: "statusWarn", statusDanger: "statusDanger", statusInfo: "statusInfo",
	labelKey: "labelKey", label1: "label1", label2: "label2", label3: "label3",
	label4: "label4", label5: "label5",
}

// dimPalette is the board-under-overlay variant: every slot blended toward the
// canvas. Built once, not per frame.
var dimPalette = func() [numSlots]color.Color {
	var out [numSlots]color.Color
	for i := range palette {
		out[i] = blend(palette[i], palette[canvas], 0.66)
	}
	return out
}()

func blend(a, b color.Color, t float64) color.Color {
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	mix := func(x, y uint32) uint8 {
		v := float64(x>>8)*(1-t) + float64(y>>8)*t
		return uint8(v + 0.5)
	}
	return color.RGBA{R: mix(ar, br), G: mix(ag, bg), B: mix(ab, bb), A: 0xff}
}

// ---------------------------------------------------------------------------
// cached style factory
// ---------------------------------------------------------------------------

type styleKey struct {
	fg, bg slot
	bold   bool
	dim    bool
}

var styleCache = map[styleKey]lipgloss.Style{}

func styleFor(k styleKey) lipgloss.Style {
	if s, ok := styleCache[k]; ok {
		return s
	}
	pal := &palette
	if k.dim {
		pal = &dimPalette
	}
	s := lipgloss.NewStyle().Foreground(pal[k.fg]).Background(pal[k.bg]).Bold(k.bold)
	styleCache[k] = s
	return s
}

// ---------------------------------------------------------------------------
// cell grid
// ---------------------------------------------------------------------------

type cell struct {
	r   rune // 0 means "continuation of the wide rune to the left"
	key styleKey
}

type grid struct {
	w, h  int
	cells []cell
}

func newGrid(w, h int) *grid {
	g := &grid{w: w, h: h, cells: make([]cell, w*h)}
	base := styleKey{fg: fgBase, bg: canvas}
	for i := range g.cells {
		g.cells[i] = cell{r: ' ', key: base}
	}
	return g
}

func (g *grid) at(x, y int) *cell {
	if x < 0 || y < 0 || x >= g.w || y >= g.h {
		return nil
	}
	return &g.cells[y*g.w+x]
}

func (g *grid) fill(x, y, w, h int, k styleKey) {
	for row := y; row < y+h; row++ {
		for col := x; col < x+w; col++ {
			if c := g.at(col, row); c != nil {
				*c = cell{r: ' ', key: k}
			}
		}
	}
}

// paint writes s at (x,y) clipped to maxw columns and returns the columns used.
func (g *grid) paint(x, y int, s string, k styleKey, maxw int) int {
	used := 0
	for _, r := range s {
		rw := ansi.StringWidth(string(r))
		if rw == 0 {
			rw = 1
		}
		if used+rw > maxw {
			break
		}
		if c := g.at(x+used, y); c != nil {
			*c = cell{r: r, key: k}
		}
		for pad := 1; pad < rw; pad++ {
			if c := g.at(x+used+pad, y); c != nil {
				*c = cell{r: 0, key: k}
			}
		}
		used += rw
	}
	return used
}

// dimAll re-keys every cell to the dimmed palette variant. This is the whole
// overlay-separation mechanism: one pass over the grid, no restyling.
func (g *grid) dimAll() {
	for i := range g.cells {
		g.cells[i].key.dim = true
	}
}

func (g *grid) renderANSI() string {
	var b strings.Builder
	for y := 0; y < g.h; y++ {
		start := 0
		for start < g.w {
			key := g.at(start, y).key
			end := start
			var runeBuf strings.Builder
			for end < g.w && g.at(end, y).key == key {
				if r := g.at(end, y).r; r != 0 {
					runeBuf.WriteRune(r)
				}
				end++
			}
			b.WriteString(styleFor(key).Render(runeBuf.String()))
			start = end
		}
		if y < g.h-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func (g *grid) renderPlain() string {
	var b strings.Builder
	for y := 0; y < g.h; y++ {
		for x := 0; x < g.w; x++ {
			if r := g.at(x, y).r; r != 0 {
				b.WriteRune(r)
			}
		}
		if y < g.h-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// runs and chips
// ---------------------------------------------------------------------------

type run struct {
	text string
	key  styleKey
}

func runsWidth(rs []run) int {
	total := 0
	for _, r := range rs {
		total += ansi.StringWidth(r.text)
	}
	return total
}

func (g *grid) paintRuns(x, y int, rs []run, maxw int) int {
	used := 0
	for _, r := range rs {
		if used >= maxw {
			break
		}
		used += g.paint(x+used, y, r.text, r.key, maxw-used)
	}
	return used
}

const (
	capLeft  = "▐" // right half of the cell filled -> reads as a rounded pill start
	capRight = "▌" // left half filled -> pill end
	rail     = "▌" // 1/2-cell accent bar down the left edge of a card
	railFull = "█" // selected card: the rail thickens instead of a border appearing
)

// pill renders a single-tone chip with half-block end caps. Caps carry the fill
// color as *foreground* over the surface they sit on, so the chip appears to
// have rounded ends at half-cell resolution. Cost: 2 columns.
func pill(text string, fill, on slot) []run {
	return []run{
		{capLeft, styleKey{fg: fill, bg: on}},
		{text, styleKey{fg: fgOnAccent, bg: fill}},
		{capRight, styleKey{fg: fill, bg: on}},
	}
}

// scopedPill renders key::value as a two-tone badge - dark key half, hue value
// half - which is the github label idiom translated to half-block caps.
func scopedPill(key, value string, fill, on slot) []run {
	return []run{
		{capLeft, styleKey{fg: labelKey, bg: on}},
		{key + ":", styleKey{fg: fgSubtle, bg: labelKey}},
		{value, styleKey{fg: fgOnAccent, bg: fill}},
		{capRight, styleKey{fg: fill, bg: on}},
	}
}

// flatChip is the compact-density degradation of a pill: the caps are dropped
// (-2 columns) and the fill becomes foreground-only.
func flatChip(text string, fill, on slot) []run {
	return []run{{text, styleKey{fg: fill, bg: on, bold: true}}}
}

func text(s string, fg, bg slot, bold bool) []run {
	return []run{{s, styleKey{fg: fg, bg: bg, bold: bold}}}
}

// ---------------------------------------------------------------------------
// fake data
// ---------------------------------------------------------------------------

type task struct {
	emoji   string
	title   string
	desc    string
	seq     int
	age     string
	prio    int
	blocked bool
	due     string
	overdue bool
	effort  string
	tags    []string
}

type column struct {
	index int
	label string
	hue   slot
	tasks []task
}

var board = []column{
	{index: 1, label: "TO DO", hue: hueTodo, tasks: []task{
		{"🐛", "Drag ghost sticks after drop",
			"The drag preview keeps rendering after the mouse button is released, so the board shows two copies of the card until the next repaint.",
			142, "3d old", 1, true, "overdue 2d", true, "M", []string{"type::bug", "github#12"}},
		{"✨", "Card detail overlay polish",
			"Give the detail panel a real header band, a section break, and keyboard hints in the footer so it reads as a modal rather than a box.",
			145, "new", 2, false, "in 2d", false, "S", []string{"type::feature", "area::tui"}},
		{"🔧", "Pin color profile in goldens",
			"CI renders in 256 colors while local runs are truecolor, so golden files churn. Pin the profile in the test harness.",
			147, "1d old", 3, false, "", false, "S", []string{"type::chore"}},
		{"📦", "Draft v1.2 release notes",
			"Summarize the restyle, the pointer work, and the URL filter for the v1.2 tag, with upgrade notes for the dropped compat package.",
			151, "5d old", 4, false, "", false, "L", []string{"type::docs"}},
	}},
	{index: 2, label: "DOING", hue: hueDoing, tasks: []task{
		{"🎨", "Semantic palette + style factory",
			"Replace the ad-hoc color literals with named semantic slots and a cached style factory keyed by foreground, background, and weight.",
			138, "6h here", 1, false, "today", false, "L", []string{"type::feature", "area::theme"}},
		{"🧪", "Golden regen for the restyle",
			"Regenerate every golden after the new card language lands, then diff the frames by hand once to make sure nothing silently reflowed.",
			149, "2d here", 2, true, "in 4d", false, "M", []string{"type::test", "github#41"}},
		{"🚀", "Adaptive compaction threshold",
			"Pick the density tier from the frame height instead of a fixed flag, and drop card lines in a documented order as the terminal shrinks.",
			150, "1d here", 2, false, "today", false, "M", []string{"type::feature"}},
	}},
	{index: 3, label: "DONE", hue: hueDone, tasks: []task{
		{"🔍", "Research lipgloss v2 patterns",
			"Read the v2 canvas and layer APIs and write down which parts of the redesign they cover and which need hand-rolled cell composition.",
			139, "shipped", 2, false, "", false, "M", []string{"type::research"}},
		{"🧭", "Board filter persisted in URL",
			"Keep the active query and label filter in the address bar so a filtered board survives a reload and can be shared as a link.",
			133, "shipped", 3, false, "", false, "S", []string{"type::feature"}},
		{"🧹", "Drop the compat package",
			"Nothing imports the shim any more. Delete it and fold the two remaining helpers into the packages that actually call them.",
			131, "shipped", 4, false, "", false, "S", []string{"type::chore"}},
	}},
}

const (
	cancelledCount = 4
	shippedToday   = 3
	focusedColumn  = 1 // DOING
	focusedRow     = 1 // #149
	boardTitle     = "kb visual redesign"
	boardUser      = "ak"
	filterQuery    = "restyle"
	filterLabel    = "type::feature"
	cardsShown     = 10
	cardsTotal     = 14
)

func prioSlot(p int) slot {
	switch p {
	case 1:
		return prio1
	case 2:
		return prio2
	case 4:
		return prio4
	default:
		return prio3
	}
}

func labelSlot(tag string) slot {
	sum := 0
	for _, r := range tag {
		sum += int(r)
	}
	return []slot{label1, label2, label3, label4, label5}[sum%5]
}

// ---------------------------------------------------------------------------
// layout
// ---------------------------------------------------------------------------

// compactBelow is the density threshold from map #136: web-like padding and
// pill caps at normal sizes, adaptive compaction under it.
const compactBelow = 30

// descTwoLines is the height above which a card can afford a second
// description line. Below it the snippet is a single ellipsized line.
const descTwoLines = 45

type layout struct {
	compact   bool
	margin    int
	gutter    int
	cardH     int
	cardGap   int
	descLines int
}

func newLayout(w, h int) layout {
	compact := h < compactBelow
	// Normal card rows: title + description + meta chips + label pills.
	l := layout{compact: compact, gutter: 1, cardGap: 1, descLines: 1}
	if h >= descTwoLines {
		l.descLines = 2
	}
	l.cardH = 3 + l.descLines
	if compact {
		// Compaction drops the description first, then the label line.
		l.descLines, l.cardH, l.cardGap = 0, 2, 0
	}
	if w >= 100 {
		l.margin = 1
	}
	return l
}

func splitWidths(total, n int) []int {
	base, extra := total/n, total%n
	out := make([]int, n)
	for i := range out {
		out[i] = base
		if i < extra {
			out[i]++
		}
	}
	return out
}

func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= w {
		return s
	}
	return ansi.Truncate(s, w-1, "") + "…"
}

// wrapFit greedily wraps s into at most n lines of w columns. The last line
// carries the ellipsis when text is left over, so a description can never wrap
// past the card it belongs to.
func wrapFit(s string, w, n int) []string {
	if w <= 0 || n <= 0 {
		return nil
	}
	words := strings.Fields(s)
	out := make([]string, 0, n)
	for len(words) > 0 && len(out) < n {
		if len(out) == n-1 {
			out = append(out, fit(strings.Join(words, " "), w))
			break
		}
		line := ""
		for len(words) > 0 {
			cand := words[0]
			if line != "" {
				cand = line + " " + words[0]
			}
			if ansi.StringWidth(cand) > w {
				break
			}
			line, words = cand, words[1:]
		}
		if line == "" { // a single word wider than the card
			line, words = fit(words[0], w), words[1:]
		}
		out = append(out, line)
	}
	return out
}

func renderFrame(w, h int, overlay bool) *grid {
	g := newGrid(w, h)
	l := newLayout(w, h)

	drawTopbar(g, l, 0, w)
	drawToolbar(g, l, 1, w)

	bodyTop := 2
	if !l.compact {
		bodyTop = 3 // one canvas row of page padding above the columns
	}
	footerY := h - 1
	bodyH := footerY - bodyTop
	drawColumns(g, l, bodyTop, bodyH, w)
	drawFooter(g, l, footerY, w)

	if overlay {
		g.dimAll()
		drawOverlay(g, w, h)
	}
	return g
}

func drawTopbar(g *grid, l layout, y, w int) {
	g.fill(0, y, w, 1, styleKey{fg: fgBase, bg: canvas})
	x := l.margin
	x += g.paintRuns(x, y, pill("kb", brand, canvas), w-x)
	x += g.paint(x, y, " "+fit(boardTitle, max(w/3, 8)), styleKey{fg: fgBase, bg: canvas, bold: true}, w-x)
	x += g.paint(x, y, "  "+boardUser, styleKey{fg: fgMuted, bg: canvas}, w-x)

	right := []run{}
	right = append(right, text(fmt.Sprintf("✓ %d shipped", shippedToday), statusOK, canvas, false)...)
	right = append(right, text("  ", fgMuted, canvas, false)...)
	// The cancelled column is toggled off; it survives as a count chip, not a panel.
	if l.compact {
		right = append(right, flatChip(fmt.Sprintf("⊘%d", cancelledCount), hueCancelled, canvas)...)
	} else {
		right = append(right, pill(fmt.Sprintf("⊘ cancelled %d", cancelledCount), hueCancelled, canvas)...)
	}
	rw := runsWidth(right)
	if x+2+rw <= w-l.margin {
		g.paintRuns(w-l.margin-rw, y, right, rw)
	}
}

func drawToolbar(g *grid, l layout, y, w int) {
	g.fill(0, y, w, 1, styleKey{fg: fgBase, bg: canvas})
	x := l.margin
	// Filter input reads as a web text field: its own surface tier, no border.
	field := "⌕ " + filterQuery
	fieldW := min(max(len(field)+2, 14), w/3)
	g.fill(x, y, fieldW, 1, styleKey{fg: fgSubtle, bg: surface})
	g.paint(x+1, y, fit(field, fieldW-2), styleKey{fg: fgBase, bg: surface}, fieldW-2)
	x += fieldW + 1

	key, value, _ := strings.Cut(filterLabel, "::")
	chip := scopedPill(key, value, labelSlot(filterLabel), canvas)
	if l.compact {
		chip = flatChip(value, labelSlot(filterLabel), canvas)
	}
	if x+runsWidth(chip) < w-l.margin {
		x += g.paintRuns(x, y, chip, w-x)
		x += g.paint(x, y, " ✕", styleKey{fg: fgMuted, bg: canvas}, w-x)
	}

	count := fmt.Sprintf("%d of %d cards", cardsShown, cardsTotal)
	if cw := ansi.StringWidth(count); x+2+cw <= w-l.margin {
		g.paint(w-l.margin-cw, y, count, styleKey{fg: fgMuted, bg: canvas}, cw)
	}
}

func drawFooter(g *grid, l layout, y, w int) {
	g.fill(0, y, w, 1, styleKey{fg: fgSubtle, bg: surface})
	x := l.margin + 1
	x += g.paint(x, y, "●", styleKey{fg: statusOK, bg: surface}, w-x)
	x += g.paint(x, y, " ready", styleKey{fg: fgSubtle, bg: surface}, w-x)

	keys := "j/k cards · h/l columns · ⏎ open · c cancelled · ? help · q quit"
	if ansi.StringWidth(keys) > w-x-4 {
		keys = "j/k · h/l · ⏎ open · ? · q"
	}
	if kw := ansi.StringWidth(keys); x+2+kw <= w-l.margin {
		g.paint(w-l.margin-1-kw, y, keys, styleKey{fg: fgMuted, bg: surface}, kw)
	}
}

// maxColumnWidth is the terminal analogue of a web max-width container: past
// this, extra terminal width becomes page margin instead of stretched cards.
const maxColumnWidth = 52

func drawColumns(g *grid, l layout, y, h, w int) {
	n := len(board)
	avail := w - 2*l.margin - (n-1)*l.gutter
	widths := splitWidths(avail, n)
	x := l.margin
	if widths[0] > maxColumnWidth {
		for i := range widths {
			widths[i] = maxColumnWidth
		}
		x = (w - (n*maxColumnWidth + (n-1)*l.gutter)) / 2
	}
	for i, col := range board {
		drawColumn(g, l, x, y, widths[i], h, col, i == focusedColumn)
		x += widths[i] + l.gutter
	}
}

func drawColumn(g *grid, l layout, x, y, w, h int, col column, focused bool) {
	if w < 6 || h < 2 {
		return
	}
	// Panel identity: the whole column is one surface tier above the canvas.
	g.fill(x, y, w, h, styleKey{fg: fgSubtle, bg: surface})

	// Header band. Focused = solid hue fill; unfocused = surface with a hue
	// rail and hue text. No border either way.
	headKey := styleKey{fg: fgOnAccent, bg: col.hue, bold: true}
	if !focused {
		headKey = styleKey{fg: col.hue, bg: surface, bold: true}
	}
	g.fill(x, y, w, 1, headKey)
	hx := x
	if !focused {
		hx += g.paint(hx, y, rail, styleKey{fg: col.hue, bg: surface}, w)
		hx += g.paint(hx, y, " ", headKey, w-(hx-x))
	} else {
		hx += g.paint(hx, y, "▸ ", headKey, w)
	}
	hx += g.paint(hx, y, fmt.Sprintf("%d ", col.index), styleKey{fg: headKey.fg, bg: headKey.bg}, w-(hx-x))
	hx += g.paint(hx, y, fit(col.label, w-(hx-x)-5), headKey, w-(hx-x))
	countTxt := fmt.Sprintf("%d ", len(col.tasks))
	g.paint(x+w-ansi.StringWidth(countTxt), y, countTxt, headKey, ansi.StringWidth(countTxt))

	cardsTop := y + 1
	if !l.compact {
		// Column meta line: panel subtitle, dropped by compaction.
		blocked := 0
		for _, t := range col.tasks {
			if t.blocked {
				blocked++
			}
		}
		meta := fmt.Sprintf("%d cards", len(col.tasks))
		if blocked > 0 {
			meta += fmt.Sprintf(" · %d blocked", blocked)
		}
		g.paint(x+2, y+1, fit(meta, w-4), styleKey{fg: fgMuted, bg: surface}, w-4)
		cardsTop = y + 2
	}

	room := y + h - cardsTop
	cy := cardsTop
	drawn := 0
	for i, t := range col.tasks {
		if cy+l.cardH > y+h {
			break
		}
		selected := focused && i == focusedRow
		// Compaction reclaims the column's side padding as well as the gutter.
		cardX, cardW := x+1, w-2
		if l.compact {
			cardX, cardW = x, w
		}
		drawCard(g, l, cardX, cy, cardW, t, selected, i%2 == 1)
		cy += l.cardH + l.cardGap
		drawn++
	}
	if drawn < len(col.tasks) && room > 0 {
		more := fmt.Sprintf("+%d more", len(col.tasks)-drawn)
		g.paint(x+2, y+h-1, fit(more, w-4), styleKey{fg: fgMuted, bg: surface}, w-4)
	}
}

func drawCard(g *grid, l layout, x, y, w int, t task, selected, alt bool) {
	bg := card
	if l.compact && alt {
		bg = zebra // zebra striping replaces the gutter row at compact density
	}
	if selected {
		bg = raised
	}
	g.fill(x, y, w, l.cardH, styleKey{fg: fgBase, bg: bg})

	// The rail is the card's only "border": half a cell wide, priority-hued,
	// thickening to a full block when the card is selected.
	railGlyph, railColor := rail, prioSlot(t.prio)
	if selected {
		railGlyph, railColor = railFull, brand
	}
	for row := 0; row < l.cardH; row++ {
		g.paint(x, y+row, railGlyph, styleKey{fg: railColor, bg: bg}, 1)
	}

	pad := 1
	if l.compact {
		pad = 0 // compact drops the card's inner left padding
	}
	inner := w - 2 - pad // rail + optional left pad + right pad
	cx := x + 1 + pad
	if inner < 6 {
		return
	}

	// Row 0: emoji, title, right-aligned #seq.
	seq := fmt.Sprintf("#%d", t.seq)
	seqW := ansi.StringWidth(seq)
	titleW := inner - seqW - 1
	head := t.emoji + " " + t.title
	g.paint(cx, y, fit(head, titleW), styleKey{fg: fgBase, bg: bg, bold: selected}, titleW)
	g.paint(cx+inner-seqW, y, seq, styleKey{fg: fgMuted, bg: bg}, seqW)

	chips := metaChips(t, bg, l.compact)
	if l.compact {
		// Compact drops the description entirely; chips take row 1.
		chips = append(chips, labelChips(t, bg, true)...)
		paintChips(g, cx, y+1, chips, inner)
		return
	}

	// Rows 1..descLines: the muted description snippet, wrapped and clipped to
	// the card's inner width.
	row := y + 1
	for _, line := range wrapFit(t.desc, inner, l.descLines) {
		g.paint(cx, row, line, styleKey{fg: fgMuted, bg: bg}, inner)
		row++
	}
	row = y + 1 + l.descLines // keep the chip rows aligned on short descriptions

	// Meta chips, then label pills; both drop right-to-left when they do not fit.
	paintChips(g, cx, row, chips, inner)
	paintChips(g, cx, row+1, labelChips(t, bg, false), inner)
}

// metaChips returns the ordered, droppable meta tokens for one card. Order is
// the compaction order: priority survives longest, labels drop first.
func metaChips(t task, on slot, compact bool) [][]run {
	out := [][]run{text(fmt.Sprintf("P%d", t.prio), prioSlot(t.prio), on, true)}
	out = append(out, text(t.age, fgMuted, on, false))
	if t.blocked {
		if compact {
			out = append(out, text("⛔", statusWarn, on, false))
		} else {
			out = append(out, pill("blocked", statusWarn, on))
		}
	}
	if t.due != "" {
		hue := statusInfo
		if t.overdue {
			hue = statusDanger
		}
		if compact {
			out = append(out, text("!"+strings.TrimPrefix(t.due, "overdue "), hue, on, false))
		} else {
			out = append(out, pill(t.due, hue, on))
		}
	}
	if t.effort != "" {
		out = append(out, text("◇"+t.effort, fgSubtle, on, false))
	}
	return out
}

func labelChips(t task, on slot, compact bool) [][]run {
	out := make([][]run, 0, len(t.tags))
	for _, tag := range t.tags {
		hue := labelSlot(tag)
		key, value, scoped := strings.Cut(tag, "::")
		switch {
		case compact && scoped:
			out = append(out, flatChip(value, hue, on))
		case compact:
			out = append(out, flatChip("#"+tag, hue, on))
		case scoped:
			out = append(out, scopedPill(key, value, hue, on))
		default:
			out = append(out, pill("#"+tag, hue, on))
		}
	}
	return out
}

func paintChips(g *grid, x, y int, chips [][]run, maxw int) {
	used := 0
	for _, c := range chips {
		cw := runsWidth(c)
		sep := 0
		if used > 0 {
			sep = 1
		}
		if used+sep+cw > maxw {
			continue // drop this chip, keep trying the shorter ones behind it
		}
		used += sep
		used += g.paintRuns(x+used, y, c, maxw-used)
	}
}

// ---------------------------------------------------------------------------
// overlay
// ---------------------------------------------------------------------------

func drawOverlay(g *grid, w, h int) {
	pw := min(72, w-8)
	ph := min(13, h-6)
	if pw < 24 || ph < 8 {
		return
	}
	px := (w - pw) / 2
	py := (h - ph) / 2

	// Drop shadow: offset one cell right and down. Elevation, again, with no
	// border involved.
	g.fill(px+1, py+ph, pw, 1, styleKey{fg: shadow, bg: shadow})
	g.fill(px+pw, py+1, 1, ph, styleKey{fg: shadow, bg: shadow})

	g.fill(px, py, pw, ph, styleKey{fg: fgBase, bg: overlaySurf})

	t := board[focusedColumn].tasks[focusedRow]

	// Header band - the overlay's own panel identity, one tier above its body.
	g.fill(px, py, pw, 1, styleKey{fg: fgOnAccent, bg: brand})
	g.paint(px+2, py, fit(t.emoji+" "+t.title, pw-14), styleKey{fg: fgOnAccent, bg: brand, bold: true}, pw-14)
	seq := fmt.Sprintf("#%d ", t.seq)
	g.paint(px+pw-ansi.StringWidth(seq), py, seq, styleKey{fg: fgOnAccent, bg: brand}, ansi.StringWidth(seq))

	chips := metaChips(t, overlaySurf, false)
	chips = append(chips, labelChips(t, overlaySurf, false)...)
	paintChips(g, px+2, py+2, chips, pw-4)

	// Section break: a shade step, not a rule.
	g.fill(px, py+3, pw, 1, styleKey{fg: fgMuted, bg: overlayBand})
	g.paint(px+2, py+3, "DETAIL", styleKey{fg: fgSubtle, bg: overlayBand, bold: true}, pw-4)

	fields := [][2]string{
		{"status", "DOING · moved 2d ago"},
		{"assignee", "ak"},
		{"created", "2026-08-13"},
		{"blocked by", "#142 drag ghost regression"},
	}
	row := py + 4
	for _, f := range fields {
		if row >= py+ph-4 {
			break
		}
		g.paint(px+2, row, f[0], styleKey{fg: fgMuted, bg: overlaySurf}, 12)
		g.paint(px+14, row, fit(f[1], pw-16), styleKey{fg: fgBase, bg: overlaySurf}, pw-16)
		row++
	}
	// The overlay shows the same description the card snippet was cut from.
	body := wrapFit(t.desc, pw-4, 2)
	row++
	for _, line := range body {
		if row >= py+ph-2 {
			break
		}
		g.paint(px+2, row, fit(line, pw-4), styleKey{fg: fgSubtle, bg: overlaySurf}, pw-4)
		row++
	}

	// Footer band.
	g.fill(px, py+ph-1, pw, 1, styleKey{fg: fgSubtle, bg: overlayBand})
	g.paint(px+2, py+ph-1, "e edit · x cancel · t move · esc close", styleKey{fg: fgSubtle, bg: overlayBand}, pw-4)
}

// ---------------------------------------------------------------------------
// 256-color honesty audit
// ---------------------------------------------------------------------------

func xterm256(c color.Color) int {
	r32, g32, b32, _ := c.RGBA()
	r, gg, b := int(r32>>8), int(g32>>8), int(b32>>8)
	best, bestDist := 0, 1<<30
	comp := func(i int) (int, int, int) {
		switch {
		case i < 16:
			lvl := [][3]int{
				{0, 0, 0}, {128, 0, 0}, {0, 128, 0}, {128, 128, 0},
				{0, 0, 128}, {128, 0, 128}, {0, 128, 128}, {192, 192, 192},
				{128, 128, 128}, {255, 0, 0}, {0, 255, 0}, {255, 255, 0},
				{0, 0, 255}, {255, 0, 255}, {0, 255, 255}, {255, 255, 255},
			}
			return lvl[i][0], lvl[i][1], lvl[i][2]
		case i < 232:
			n := i - 16
			steps := []int{0, 95, 135, 175, 215, 255}
			return steps[n/36], steps[(n/6)%6], steps[n%6]
		default:
			v := (i-232)*10 + 8
			return v, v, v
		}
	}
	for i := 0; i < 256; i++ {
		cr, cg, cb := comp(i)
		d := (cr-r)*(cr-r) + (cg-gg)*(cg-gg) + (cb-b)*(cb-b)
		if d < bestDist {
			best, bestDist = i, d
		}
	}
	return best
}

// verifyGeometry asserts that both renderings occupy exactly w columns by h
// rows. A prototype that overflows its box is a failed prototype.
func verifyGeometry(g *grid, w, h int) error {
	plainLines := strings.Split(g.renderPlain(), "\n")
	if len(plainLines) != h {
		return fmt.Errorf("plain: %d rows, want %d", len(plainLines), h)
	}
	for i, line := range plainLines {
		if got := ansi.StringWidth(line); got != w {
			return fmt.Errorf("plain row %d: width %d, want %d", i, got, w)
		}
	}
	ansiLines := strings.Split(g.renderANSI(), "\n")
	if len(ansiLines) != h {
		return fmt.Errorf("ansi: %d rows, want %d", len(ansiLines), h)
	}
	for i, line := range ansiLines {
		if got := ansi.StringWidth(ansi.Strip(line)); got != w {
			return fmt.Errorf("ansi row %d: width %d, want %d", i, got, w)
		}
	}
	return nil
}

func hexOf(name string) string {
	for i := slot(0); i < numSlots; i++ {
		if slotNames[i] == name {
			r, g, b, _ := palette[i].RGBA()
			return fmt.Sprintf("%02x%02x%02x", r>>8, g>>8, b>>8)
		}
	}
	return name
}

func audit256(out *bufio.Writer) {
	seen := map[int][]string{}
	for i := slot(0); i < numSlots; i++ {
		idx := xterm256(palette[i])
		r, g, b, _ := palette[i].RGBA()
		fmt.Fprintf(out, "%-12s #%02x%02x%02x -> 256:%3d\n", slotNames[i], r>>8, g>>8, b>>8, idx)
		seen[idx] = append(seen[idx], slotNames[i])
	}
	fmt.Fprintln(out, "\ncollisions (aliases share one hex on purpose; only")
	fmt.Fprintln(out, "distinct hexes landing on one index are real losses):")
	clean := true
	for idx, names := range seen {
		if len(names) < 2 {
			continue
		}
		alias := true
		for _, n := range names[1:] {
			if hexOf(n) != hexOf(names[0]) {
				alias = false
			}
		}
		kind := "REAL"
		if alias {
			kind = "alias"
		}
		clean = false
		fmt.Fprintf(out, "  256:%-3d %-5s <- %s\n", idx, kind, strings.Join(names, ", "))
	}
	if clean {
		fmt.Fprintln(out, "  none")
	}
}

// ---------------------------------------------------------------------------

func main() {
	width := flag.Int("w", 140, "frame width in columns")
	height := flag.Int("h", 40, "frame height in rows")
	mode := flag.String("mode", "board", "board | overlay")
	plain := flag.Bool("plain", false, "strip ANSI and print plain text")
	verify := flag.Bool("verify", false, "check that the frame fits its geometry exactly")
	audit := flag.Bool("audit256", false, "print the palette's 256-color quantization audit")
	flag.Parse()

	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()

	if *audit {
		audit256(out)
		return
	}
	if *width < 40 || *height < 10 {
		fmt.Fprintln(os.Stderr, "frame too small; minimum 40x10")
		os.Exit(1)
	}
	g := renderFrame(*width, *height, *mode == "overlay")
	if *verify {
		if err := verifyGeometry(g, *width, *height); err != nil {
			fmt.Fprintln(os.Stderr, "geometry check failed:", err)
			os.Exit(1)
		}
		return
	}
	if *plain {
		fmt.Fprintln(out, g.renderPlain())
		return
	}
	fmt.Fprintln(out, g.renderANSI())
}
