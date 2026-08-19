// Command minimal-frame is a throwaway visual prototype for kb ticket #137
// (map #136: TUI visual redesign).
//
// Variant: minimal-frame. No borders anywhere except a rounded border on the
// single focused card. Columns are separated by whitespace gutters and a thin
// header rule. Hierarchy is carried by color and weight, not chrome. Selection
// in a non-focused column is a colored left-edge bar; focus is the rounded
// border plus a thicker, accented column rule.
//
// It renders one static frame from hardcoded fake data at a given geometry and
// prints it. Nothing here is wired to the real board; it exists to be reacted
// to and then deleted.
//
// Usage:
//
//	go run ./prototypes/minimal-frame -width 140 -height 40
//	go run ./prototypes/minimal-frame -width 140 -height 40 -mode overlay
//	go run ./prototypes/minimal-frame -width 80 -height 24 -plain
package main

import (
	"flag"
	"fmt"
	"image/color"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// ---------------------------------------------------------------------------
// Tokens: semantic palette -> cached style factory.
//
// Per the #138 research: colors are named by role, never by hue; the palette is
// resolved once through lipgloss.LightDark and fed to a single style factory
// whose result is cached. No style is constructed inside a render path.
// ---------------------------------------------------------------------------

type palette struct {
	bgBase     color.Color
	bgSurface  color.Color
	bgRaised   color.Color
	fgBase     color.Color
	fgSubtle   color.Color
	fgMuted    color.Color
	fgFaint    color.Color
	accent     color.Color
	accentDim  color.Color
	success    color.Color
	warning    color.Color
	danger     color.Color
	priority   [5]color.Color
	labelHues  [5]color.Color
	separator  color.Color
	onAccentFg color.Color
}

// newPalette resolves the semantic palette for the terminal background. Only
// the dark side is designed (map decision: truecolor-dark is the reference);
// the light side exists so the seam is real from day one.
func newPalette(isDark bool) palette {
	ld := lipgloss.LightDark(isDark)
	return palette{
		bgBase:    ld(lipgloss.Color("#ffffff"), lipgloss.Color("#0d1117")),
		bgSurface: ld(lipgloss.Color("#f2f4f8"), lipgloss.Color("#161b22")),
		bgRaised:  ld(lipgloss.Color("#e6e9ef"), lipgloss.Color("#21262d")),
		fgBase:    ld(lipgloss.Color("#111418"), lipgloss.Color("#e6edf3")),
		fgSubtle:  ld(lipgloss.Color("#3d444d"), lipgloss.Color("#b8c0cc")),
		fgMuted:   ld(lipgloss.Color("#5c6672"), lipgloss.Color("#8b949e")),
		fgFaint:   ld(lipgloss.Color("#8a939f"), lipgloss.Color("#4d545c")),
		accent:    ld(lipgloss.Color("#1f6feb"), lipgloss.Color("#4f8ef7")),
		accentDim: ld(lipgloss.Color("#7aa7f0"), lipgloss.Color("#2f5da8")),
		success:   ld(lipgloss.Color("#1a7f37"), lipgloss.Color("#3fb950")),
		warning:   ld(lipgloss.Color("#9a6700"), lipgloss.Color("#d29922")),
		danger:    ld(lipgloss.Color("#cf222e"), lipgloss.Color("#f85149")),
		priority: [5]color.Color{
			nil,
			ld(lipgloss.Color("#cf222e"), lipgloss.Color("#ff5a48")),
			ld(lipgloss.Color("#9a6700"), lipgloss.Color("#ffb020")),
			ld(lipgloss.Color("#1f6feb"), lipgloss.Color("#4f8ef7")),
			ld(lipgloss.Color("#6e7781"), lipgloss.Color("#8b949e")),
		},
		labelHues: [5]color.Color{
			ld(lipgloss.Color("#bc4c00"), lipgloss.Color("#ff7b54")),
			ld(lipgloss.Color("#1f6feb"), lipgloss.Color("#4f8ef7")),
			ld(lipgloss.Color("#1a7f37"), lipgloss.Color("#3f9d58")),
			ld(lipgloss.Color("#8250df"), lipgloss.Color("#b98af7")),
			ld(lipgloss.Color("#9a6700"), lipgloss.Color("#ffb020")),
		},
		separator:  ld(lipgloss.Color("#d0d7de"), lipgloss.Color("#30363d")),
		onAccentFg: ld(lipgloss.Color("#ffffff"), lipgloss.Color("#0d1117")),
	}
}

// scrim derives the backgrounded palette used behind an overlay. Deriving it
// from the base palette is the point: one set of tokens, two resolved planes.
func (p palette) scrim() palette {
	dim := func(c color.Color) color.Color { return lipgloss.Darken(c, 0.62) }
	out := p
	out.fgBase = dim(p.fgBase)
	out.fgSubtle = dim(p.fgSubtle)
	out.fgMuted = dim(p.fgMuted)
	out.fgFaint = dim(p.fgFaint)
	out.accent = dim(p.accent)
	out.accentDim = dim(p.accentDim)
	out.success = dim(p.success)
	out.warning = dim(p.warning)
	out.danger = dim(p.danger)
	out.separator = dim(p.separator)
	out.bgSurface = lipgloss.Darken(p.bgSurface, 0.35)
	out.bgRaised = lipgloss.Darken(p.bgRaised, 0.35)
	for i := 1; i < len(out.priority); i++ {
		out.priority[i] = dim(p.priority[i])
	}
	for i := range out.labelHues {
		out.labelHues[i] = dim(p.labelHues[i])
	}
	return out
}

type styles struct {
	brand      lipgloss.Style
	brandSlash lipgloss.Style
	boardName  lipgloss.Style
	user       lipgloss.Style
	shipped    lipgloss.Style

	filterWord lipgloss.Style
	clearHint  lipgloss.Style
	hiddenCol  lipgloss.Style

	colIndex      lipgloss.Style
	colName       lipgloss.Style
	colNameFocus  lipgloss.Style
	colCount      lipgloss.Style
	colCountFocus lipgloss.Style
	rule          lipgloss.Style
	ruleFocus     lipgloss.Style

	barIdle lipgloss.Style
	barSel  lipgloss.Style

	title     lipgloss.Style
	titleDone lipgloss.Style
	desc      lipgloss.Style
	descDone  lipgloss.Style
	seq       lipgloss.Style
	age       lipgloss.Style
	ageShip   lipgloss.Style

	prio    [5]lipgloss.Style
	blocked lipgloss.Style
	dueSoon lipgloss.Style
	dueOver lipgloss.Style
	effort  lipgloss.Style

	labelKey  lipgloss.Style
	labelVal  [5]lipgloss.Style
	plainTag  [5]lipgloss.Style
	focusCard lipgloss.Style

	empty    lipgloss.Style
	more     lipgloss.Style
	state    lipgloss.Style
	footKey  lipgloss.Style
	footWord lipgloss.Style
	footSep  lipgloss.Style

	overlayFrame lipgloss.Style
	overlayEyer  lipgloss.Style
	overlayTitle lipgloss.Style
	overlayField lipgloss.Style
	overlayBody  lipgloss.Style
	overlayRule  lipgloss.Style
}

// newStyles is the style factory. Called once per palette plane, never per
// frame. Every component style in the prototype comes from here.
func newStyles(p palette) styles {
	base := lipgloss.NewStyle()
	pill := base.Background(p.bgRaised).Padding(0, 1)

	s := styles{
		brand:      base.Foreground(p.accent).Bold(true),
		brandSlash: base.Foreground(p.fgFaint),
		boardName:  base.Foreground(p.fgBase).Bold(true),
		user:       base.Foreground(p.fgMuted),
		shipped:    base.Foreground(p.success),

		filterWord: base.Foreground(p.fgMuted),
		clearHint:  base.Foreground(p.fgFaint),
		hiddenCol:  base.Foreground(p.fgFaint),

		colIndex:      base.Foreground(p.fgFaint),
		colName:       base.Foreground(p.fgSubtle).Bold(true),
		colNameFocus:  base.Foreground(p.accent).Bold(true),
		colCount:      base.Foreground(p.fgFaint),
		colCountFocus: base.Foreground(p.fgMuted),
		rule:          base.Foreground(p.separator),
		ruleFocus:     base.Foreground(p.accent),

		barIdle: base.Foreground(p.separator),
		barSel:  base.Foreground(p.accentDim),

		title:     base.Foreground(p.fgBase).Bold(true),
		titleDone: base.Foreground(p.fgMuted),
		desc:      base.Foreground(p.fgMuted),
		descDone:  base.Foreground(p.fgFaint),
		seq:       base.Foreground(p.fgFaint),
		age:       base.Foreground(p.fgMuted),
		ageShip:   base.Foreground(p.success),

		blocked: pill.Foreground(p.warning),
		dueSoon: pill.Foreground(p.fgSubtle),
		dueOver: pill.Foreground(p.danger).Bold(true),
		effort:  base.Foreground(p.fgFaint),

		labelKey: base.Background(p.bgRaised).Foreground(p.fgMuted),

		empty:    base.Foreground(p.fgFaint).Italic(true),
		more:     base.Foreground(p.fgFaint),
		state:    base.Foreground(p.fgSubtle),
		footKey:  base.Foreground(p.fgSubtle).Bold(true),
		footWord: base.Foreground(p.fgFaint),
		footSep:  base.Foreground(p.separator),

		overlayFrame: base.Border(lipgloss.RoundedBorder()).
			BorderForeground(p.accent).
			Background(p.bgSurface).
			Padding(1, 2),
		overlayEyer:  base.Background(p.bgSurface).Foreground(p.accent).Bold(true),
		overlayTitle: base.Background(p.bgSurface).Foreground(p.fgBase).Bold(true),
		overlayField: base.Background(p.bgSurface).Foreground(p.fgMuted),
		overlayBody:  base.Background(p.bgSurface).Foreground(p.fgSubtle),
		overlayRule:  base.Background(p.bgSurface).Foreground(p.separator),
	}
	for i := 1; i < len(p.priority); i++ {
		s.prio[i] = base.Foreground(p.priority[i]).Bold(true)
	}
	for i, hue := range p.labelHues {
		s.labelVal[i] = base.Background(p.bgRaised).Foreground(hue).Bold(true)
		s.plainTag[i] = base.Background(p.bgRaised).Foreground(hue)
	}
	// The one border on the board plane: the focused card.
	s.focusCard = base.Border(lipgloss.RoundedBorder()).
		BorderForeground(p.accent).
		Padding(0, 1)
	return s
}

// ---------------------------------------------------------------------------
// Fake data. Chip density matches internal/tui/board_view.go: emoji, title,
// #seq, age, P1-P4, blocked/due/effort, scoped and plain label pills.
// ---------------------------------------------------------------------------

type card struct {
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
	name  string
	cards []card
}

func fakeBoard() []column {
	return []column{
		{index: 1, name: "TO DO", cards: []card{
			{emoji: "🐛", title: "Drag ghost on resize", seq: 142, age: "2d old", prio: 1,
				desc: "The drag ghost keeps the pre-resize column width, so the " +
					"card lands one column left of the drop target.",
				blocked: true, due: "overdue 1d", overdue: true, effort: "M",
				tags: []string{"type::bug", "area::tui"}},
			{emoji: "✨", title: "Design tokens", seq: 139, age: "new", prio: 2,
				desc: "Name every color by role and resolve the palette once at " +
					"startup. No literal hex outside the token table.",
				effort: "L", tags: []string{"type::feature", "github#12"}},
			{emoji: "📐", title: "Adaptive compaction", seq: 144, age: "5d old", prio: 3,
				desc: "Drop description, then age, then effort as the viewport " +
					"narrows, so cards degrade in one predictable order.",
				due: "in 3d", effort: "S", tags: []string{"type::design"}},
			{emoji: "🧪", title: "Golden regen", seq: 147, age: "9d old", prio: 4,
				desc: "Regenerate the golden frames after the card layout change " +
					"and review the diff by hand once.",
				tags: []string{"type::chore", "area::tests"}},
		}},
		{index: 2, name: "DOING", cards: []card{
			{emoji: "🚀", title: "Board language variants", seq: 137, age: "6h here", prio: 1,
				desc: "Three throwaway renders of the same board data, one per " +
					"visual language. The winner binds the token ticket.",
				due: "today", effort: "M", tags: []string{"type::spike", "github#137"}},
			{emoji: "🔧", title: "Overlay separation", seq: 140, age: "2d here", prio: 2,
				desc: "Composite the detail pane over a scrimmed board plane so " +
					"the layers never share a style table.",
				effort: "M", tags: []string{"type::feature"}},
			{emoji: "📝", title: "Lipgloss research", seq: 138, age: "1d here", prio: 3,
				desc: "Write up what v2 changes for us: canvas compositing, " +
					"LightDark resolution, and style caching.",
				blocked: true, tags: []string{"type::research"}},
		}},
		{index: 3, name: "DONE", cards: []card{
			{emoji: "✅", title: "Filter persists in URL", seq: 133, age: "shipped", prio: 2,
				desc: "Board filters now round-trip through the query string, so " +
					"a shared link opens the same view.",
				effort: "S", tags: []string{"type::feature", "github#77"}},
			{emoji: "✅", title: "Web UI hardening", seq: 131, age: "shipped", prio: 1,
				desc: "Closed the last pre-freeze gaps in the web board before it " +
					"was retired in favour of the TUI.",
				tags: []string{"type::fix"}},
			{emoji: "✅", title: "Bump rig v0.2.0", seq: 129, age: "shipped", prio: 4,
				desc: "Routine dependency bump; no behaviour change beyond the " +
					"new retry defaults.",
				tags: []string{"type::chore"}},
		}},
	}
}

const (
	cancelledIndex  = 4
	cancelledName   = "CANCELLED"
	cancelledCount  = 2
	focusedColumn   = 1 // DOING
	shippedToday    = 2
	gutter          = 2
	chromeRows      = 6 // brand, filter, air, column head, rule, footer
	minColumnWidth  = 18
	overlayMaxWidth = 66

	// Density thresholds. Below either one the board switches to compact and
	// the description - the first thing to go - is dropped entirely.
	compactColumnWidth = 34
	compactHeight      = 28
	// A second description line is only affordable in a wide column.
	twoLineDescWidth = 48
)

// selection per column; the focused column's selection is the focused card.
var selection = [3]int{0, 0, 1}

// ---------------------------------------------------------------------------
// Chips
// ---------------------------------------------------------------------------

func labelHue(tag string) int {
	sum := 0
	for _, r := range tag {
		sum += int(r)
	}
	return sum % 5
}

func (s styles) labelPill(tag string) string {
	hue := labelHue(tag)
	key, value, scoped := strings.Cut(tag, "::")
	if !scoped || key == "" || value == "" {
		return s.plainTag[hue].Render(" " + tag + " ")
	}
	return s.labelKey.Render(" "+key+":") + s.labelVal[hue].Render(value+" ")
}

func (s styles) metaChips(c card) []string {
	prio := c.prio
	if prio < 1 || prio > 4 {
		prio = 3
	}
	chips := []string{s.prio[prio].Render(fmt.Sprintf("P%d", prio))}
	if c.blocked {
		chips = append(chips, s.blocked.Render("⛔ blocked"))
	}
	if c.due != "" {
		style := s.dueSoon
		if c.overdue {
			style = s.dueOver
		}
		chips = append(chips, style.Render(c.due))
	}
	if c.effort != "" {
		chips = append(chips, s.effort.Render("◇"+c.effort))
	}
	for _, tag := range c.tags {
		chips = append(chips, s.labelPill(tag))
	}
	return chips
}

func (s styles) headingChips(c card, done bool) []string {
	titleStyle := s.title
	ageStyle := s.age
	if done {
		titleStyle = s.titleDone
		ageStyle = s.ageShip
	}
	chips := make([]string, 0, 8)
	if c.emoji != "" {
		chips = append(chips, c.emoji)
	}
	for _, word := range strings.Fields(c.title) {
		chips = append(chips, titleStyle.Render(word))
	}
	chips = append(chips, s.seq.Render(fmt.Sprintf("#%d", c.seq)))
	chips = append(chips, ageStyle.Render(c.age))
	return chips
}

// ---------------------------------------------------------------------------
// Layout primitives
// ---------------------------------------------------------------------------

func pad(line string, width int) string {
	w := ansi.StringWidth(line)
	if w > width {
		return ansi.Truncate(line, width, "")
	}
	return line + strings.Repeat(" ", width-w)
}

func wrapChips(chips []string, width int) []string {
	if width <= 0 {
		return []string{""}
	}
	lines := make([]string, 0, 3)
	line := ""
	for _, chip := range chips {
		if chip == "" {
			continue
		}
		candidate := chip
		if line != "" {
			candidate = line + " " + chip
		}
		if ansi.StringWidth(candidate) <= width {
			line = candidate
			continue
		}
		if line != "" {
			lines = append(lines, line)
		}
		line = ansi.Truncate(chip, width, "")
	}
	if line != "" || len(lines) == 0 {
		lines = append(lines, line)
	}
	return lines
}

// descLines lays the description out inside `inner` cells. It never wraps past
// the card width and never runs longer than `limit` rows: the overflow is
// folded into the last kept row and truncated with an ellipsis.
func descLines(text string, inner, limit int) []string {
	if text == "" || inner <= 0 || limit <= 0 {
		return nil
	}
	wrapped := strings.Split(ansi.Wrap(text, inner, ""), "\n")
	if len(wrapped) <= limit {
		return wrapped
	}
	kept := wrapped[:limit]
	kept[limit-1] = ansi.Truncate(strings.Join(wrapped[limit-1:], " "), inner, "…")
	return kept
}

func splitWidths(total, count int) []int {
	usable := max(total-gutter*(count-1), count*minColumnWidth)
	base, extra := usable/count, usable%count
	widths := make([]int, count)
	for i := range widths {
		widths[i] = base
		if i < extra {
			widths[i]++
		}
	}
	return widths
}

// ---------------------------------------------------------------------------
// Card rendering
// ---------------------------------------------------------------------------

// renderCard emits exactly `width` display cells per line.
//
// Idle and selected cards carry zero frame: a 1-cell left-edge bar plus one
// cell of air. The focused card is the single exception - a rounded border,
// which GetFrameSize prices at 2 rows and 4 columns (border + padding).
func (s styles) renderCard(c card, width int, sel, focus, done, compact bool) []string {
	if focus {
		// GetFrameSize prices the border + padding; the body is sized to what
		// is left so the card still lands on the column grid.
		fw, _ := s.focusCard.GetFrameSize()
		inner := max(width-fw, 4)
		body := s.cardBody(c, inner, done, compact)
		return strings.Split(s.focusCard.Render(strings.Join(body, "\n")), "\n")
	}
	bar, glyph := s.barIdle, "▏"
	if sel {
		bar, glyph = s.barSel, "▌"
	}
	inner := max(width-2, 4)
	out := make([]string, 0, 6)
	for _, line := range s.cardBody(c, inner, done, compact) {
		out = append(out, bar.Render(glyph)+" "+pad(line, inner))
	}
	return out
}

func (s styles) cardBody(c card, inner int, done, compact bool) []string {
	lines := wrapChips(s.headingChips(c, done), inner)
	if !compact {
		limit := 1
		if inner >= twoLineDescWidth {
			limit = 2
		}
		descStyle := s.desc
		if done {
			descStyle = s.descDone
		}
		for _, line := range descLines(c.desc, inner, limit) {
			lines = append(lines, descStyle.Render(line))
		}
	}
	lines = append(lines, wrapChips(s.metaChips(c), inner)...)
	for i, line := range lines {
		lines[i] = pad(line, inner)
	}
	return lines
}

// ---------------------------------------------------------------------------
// Column rendering
// ---------------------------------------------------------------------------

func (s styles) renderColumnHead(col column, width int, focus bool) (string, string) {
	name, count := s.colName, s.colCount
	if focus {
		name, count = s.colNameFocus, s.colCountFocus
	}
	head := s.colIndex.Render(fmt.Sprintf("%d ", col.index)) +
		name.Render(col.name) + " " +
		count.Render(fmt.Sprintf("%d", len(col.cards)))
	rule := s.rule.Render(strings.Repeat("─", width))
	if focus {
		rule = s.ruleFocus.Render(strings.Repeat("━", width))
	}
	return pad(head, width), rule
}

func (s styles) renderColumnBody(col column, width, height int, focus bool, sel int, compact bool) []string {
	if height <= 0 {
		return nil
	}
	lines := make([]string, 0, height)
	ends := make([]int, 0, len(col.cards))
	done := col.name == "DONE"
	for i, c := range col.cards {
		if i > 0 {
			lines = append(lines, strings.Repeat(" ", width))
		}
		lines = append(lines, s.renderCard(c, width, i == sel, focus && i == sel, done, compact)...)
		ends = append(ends, len(lines))
	}
	if len(col.cards) == 0 {
		lines = append(lines, pad(s.empty.Render("nothing here"), width))
	}
	if len(lines) > height {
		hidden := 0
		for _, end := range ends {
			if end > height-1 {
				hidden++
			}
		}
		lines = lines[:height-1]
		if hidden > 0 {
			lines = append(lines, pad(s.more.Render(fmt.Sprintf("↓ %d more", hidden)), width))
		} else {
			lines = append(lines, strings.Repeat(" ", width))
		}
	}
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", width))
	}
	return lines
}

// ---------------------------------------------------------------------------
// Frame
// ---------------------------------------------------------------------------

func (s styles) brandLine(width int) string {
	left := s.brand.Render("kb") +
		s.brandSlash.Render(" / ") +
		s.boardName.Render("kb roadmap") +
		s.brandSlash.Render(" / ") +
		s.user.Render("aksOps")
	right := s.shipped.Render(fmt.Sprintf("×%d shipped today", shippedToday))
	return spread(left, right, width)
}

func (s styles) filterLine(width int) string {
	left := s.filterWord.Render("filter") + "  " +
		s.labelPill("type::feature") + " " +
		s.labelPill("github#12") + "  " +
		s.clearHint.Render("✕ clear")
	right := s.hiddenCol.Render(fmt.Sprintf("%d %s %d hidden · c", cancelledIndex, cancelledName, cancelledCount))
	return spread(left, right, width)
}

func spread(left, right string, width int) string {
	lw, rw := ansi.StringWidth(left), ansi.StringWidth(right)
	if lw+1+rw > width {
		if lw > width {
			return ansi.Truncate(left, width, "")
		}
		return pad(left, width)
	}
	return left + strings.Repeat(" ", width-lw-rw) + right
}

func (s styles) footerLine(width int) string {
	pairs := [][2]string{
		{"j/k", "cards"}, {"h/l", "columns"}, {"1-4", "jump"},
		{"c", "cancelled:off"}, {"enter", "open"}, {"?", "help"}, {"q", "quit"},
	}
	out := s.state.Render("ready")
	for _, pair := range pairs {
		next := out + s.footSep.Render("  ·  ") + s.footKey.Render(pair[0]) + " " + s.footWord.Render(pair[1])
		if ansi.StringWidth(next) > width {
			break
		}
		out = next
	}
	return pad(out, width)
}

func (s styles) board(width, height int) string {
	columns := fakeBoard()
	widths := splitWidths(width, len(columns))
	bodyHeight := max(height-chromeRows, 1)
	compact := height < compactHeight || widths[0] < compactColumnWidth

	heads := make([]string, len(columns))
	rules := make([]string, len(columns))
	bodies := make([][]string, len(columns))
	for i, col := range columns {
		focus := i == focusedColumn
		heads[i], rules[i] = s.renderColumnHead(col, widths[i], focus)
		bodies[i] = s.renderColumnBody(col, widths[i], bodyHeight, focus, selection[i], compact)
	}

	gap := strings.Repeat(" ", gutter)
	lines := make([]string, 0, height)
	lines = append(lines, s.brandLine(width))
	lines = append(lines, s.filterLine(width))
	lines = append(lines, strings.Repeat(" ", width))
	lines = append(lines, pad(strings.Join(heads, gap), width))
	lines = append(lines, pad(strings.Join(rules, gap), width))
	for row := 0; row < bodyHeight; row++ {
		parts := make([]string, len(columns))
		for i := range columns {
			parts[i] = bodies[i][row]
		}
		lines = append(lines, pad(strings.Join(parts, gap), width))
	}
	lines = append(lines, s.footerLine(width))
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------------------
// Overlay: the card-detail pane, composited over a scrimmed board.
// ---------------------------------------------------------------------------

func (s styles) overlay(width, height int) string {
	fw, fh := s.overlayFrame.GetFrameSize()
	boxWidth := min(overlayMaxWidth, max(width-8, 24))
	inner := boxWidth - fw
	c := fakeBoard()[focusedColumn].cards[selection[focusedColumn]]

	rows := []string{
		s.overlayEyer.Render("DOING") + s.overlayField.Render("  ·  card ") +
			s.overlayTitle.Render(fmt.Sprintf("#%d", c.seq)),
		"",
		s.overlayTitle.Render(c.emoji + " " + c.title),
		"",
	}
	rows = append(rows, wrapChips(s.metaChips(c), inner)...)
	rows = append(rows,
		"",
		s.overlayRule.Render(strings.Repeat("─", inner)),
		"",
		s.overlayField.Render("assignee  ")+s.overlayBody.Render("aksOps"),
		s.overlayField.Render("moved     ")+s.overlayBody.Render("2026-08-19 06:12 · from TO DO"),
		"",
	)
	for _, line := range strings.Split(ansi.Wrap(c.desc, inner, ""), "\n") {
		rows = append(rows, s.overlayBody.Render(line))
	}
	rows = append(rows,
		"",
		s.overlayField.Render("esc close  ·  e edit  ·  m move  ·  o open in browser"),
	)

	if limit := max(height-4-fh, 1); len(rows) > limit {
		rows = rows[:limit]
	}
	for i, row := range rows {
		rows[i] = s.overlayField.Render(pad(row, inner))
	}
	return s.overlayFrame.Render(strings.Join(rows, "\n"))
}

func renderOverlayFrame(base, scrimStyles styles, width, height int) string {
	board := scrimStyles.board(width, height)
	panel := base.overlay(width, height)
	pw, ph := lipgloss.Width(panel), lipgloss.Height(panel)
	x := max((width-pw)/2, 0)
	y := max((height-ph)/2, 0)

	canvas := lipgloss.NewCanvas(width, height)
	canvas.Compose(lipgloss.NewCompositor(
		lipgloss.NewLayer(board).X(0).Y(0).Z(0).ID("board"),
		lipgloss.NewLayer(panel).X(x).Y(y).Z(1).ID("card-detail"),
	))
	out := strings.Split(canvas.Render(), "\n")
	for len(out) < height {
		out = append(out, "")
	}
	return strings.Join(out[:height], "\n")
}

// ---------------------------------------------------------------------------

func main() {
	width := flag.Int("width", 140, "frame width in cells")
	height := flag.Int("height", 40, "frame height in rows")
	mode := flag.String("mode", "board", "board | overlay")
	plain := flag.Bool("plain", false, "strip ANSI and print plain text")
	out := flag.String("out", "", "write the frame to this file instead of stdout")
	dark := flag.Bool("dark", true, "resolve the dark side of the palette")
	flag.Parse()

	if *width < 40 || *height < 12 {
		fmt.Fprintln(os.Stderr, "minimal-frame: need at least 40x12")
		os.Exit(2)
	}

	base := newStyles(newPalette(*dark))
	var frame string
	switch *mode {
	case "board":
		frame = base.board(*width, *height)
	case "overlay":
		frame = renderOverlayFrame(base, newStyles(newPalette(*dark).scrim()), *width, *height)
	default:
		fmt.Fprintf(os.Stderr, "minimal-frame: unknown mode %q\n", *mode)
		os.Exit(2)
	}
	if *plain {
		frame = ansi.Strip(frame)
	}
	if *out != "" {
		if err := os.WriteFile(*out, []byte(frame+"\n"), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "minimal-frame:", err)
			os.Exit(1)
		}
		return
	}
	fmt.Println(frame)
}
