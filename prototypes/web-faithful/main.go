// Command web-faithful renders one static frame of a kb board in the
// "web-faithful" visual language: rounded-border card surfaces with horizontal
// padding, panel-framed columns with a distinct header band, semantic accent
// colors, and a focused card that gets a brighter border plus a background tint.
//
// This is a throwaway prototype for ticket #137 (map #136). It is not wired to
// the real board model and nothing here is imported by internal/tui.
//
// Design-system posture follows docs/research/lipgloss-design-system.md:
//   - one semantic palette (roles, not hues) feeding one cached style factory
//   - lipgloss.LightDark for adaptive resolution, truecolor-dark as reference
//   - GetFrameSize/GetHorizontalBorderSize for layout math, never hardcoded 2s
//   - zero style construction inside the per-card render loop
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
// Semantic palette
// ---------------------------------------------------------------------------

// tokens is the semantic color layer: roles, not hues. Every style in the
// factory below reads from here and nothing reads a raw hex.
type tokens struct {
	bgBase      color.Color // terminal canvas behind the panels
	bgPanel     color.Color // inside a column panel, behind the card stack
	bgBand      color.Color // column header band
	bgBandFocus color.Color // column header band, focused column
	bgCard      color.Color // card surface
	bgCardFocus color.Color // card surface, focused card
	bgChrome    color.Color // app header and footer bars
	bgShadow    color.Color // overlay drop shadow

	borderPanel color.Color
	borderCard  color.Color
	borderFocus color.Color
	rule        color.Color

	fg       color.Color
	fgSubtle color.Color
	fgMuted  color.Color
	fgOn     color.Color // text on a saturated fill

	accent color.Color
	danger color.Color
	warn   color.Color
	ok     color.Color

	// priority scale, index 1..4
	prioFg [5]color.Color
	prioBg [5]color.Color

	// per-status tint used on the column dot and header band accent
	tintTodo      color.Color
	tintDoing     color.Color
	tintDone      color.Color
	tintCancelled color.Color

	// label pill rotation, matching internal/tui's five-color wheel
	labelFg [5]color.Color
	labelBg [5]color.Color
}

// newTokens resolves the palette for the active terminal background. The dark
// side is the reference design (map #136); the light side exists so the theme
// package has a real LightDark seam from day one.
func newTokens(isDark bool) tokens {
	ld := lipgloss.LightDark(isDark)
	c := lipgloss.Color

	t := tokens{
		bgBase:      ld(c("#eef1f6"), c("#0d1017")),
		bgPanel:     ld(c("#f7f9fc"), c("#12161f")),
		bgBand:      ld(c("#e6eaf2"), c("#1a1f2b")),
		bgBandFocus: ld(c("#dbe5fb"), c("#22314d")),
		bgCard:      ld(c("#ffffff"), c("#161b25")),
		bgCardFocus: ld(c("#eef4ff"), c("#1e2637")),
		bgChrome:    ld(c("#e2e7f0"), c("#161b25")),
		bgShadow:    ld(c("#c8ced9"), c("#05070b")),

		borderPanel: ld(c("#c6cddb"), c("#252c3a")),
		borderCard:  ld(c("#d5dbe6"), c("#2e3646")),
		borderFocus: ld(c("#2f6fe4"), c("#4f8ef7")),
		rule:        ld(c("#d5dbe6"), c("#222836")),

		fg:       ld(c("#1b2029"), c("#d7dce5")),
		fgSubtle: ld(c("#5a6273"), c("#8b93a3")),
		fgMuted:  ld(c("#828b9c"), c("#5c6474")),
		fgOn:     ld(c("#ffffff"), c("#0d1017")),

		accent: ld(c("#2f6fe4"), c("#4f8ef7")),
		danger: ld(c("#d13a2a"), c("#ff5a48")),
		warn:   ld(c("#a06a00"), c("#ffb020")),
		ok:     ld(c("#2f7d43"), c("#9ece6a")),

		tintTodo:      ld(c("#2f6fe4"), c("#7aa2f7")),
		tintDoing:     ld(c("#a06a00"), c("#e0af68")),
		tintDone:      ld(c("#2f7d43"), c("#9ece6a")),
		tintCancelled: ld(c("#828b9c"), c("#6b7280")),
	}

	t.prioFg = [5]color.Color{
		nil,
		ld(c("#a8291a"), c("#ff8a7a")),
		ld(c("#8a5a00"), c("#ffc46b")),
		ld(c("#2f6fe4"), c("#8ab4f8")),
		ld(c("#6d7686"), c("#a3adbe")),
	}
	t.prioBg = [5]color.Color{
		nil,
		ld(c("#fbe4e0"), c("#3a1a18")),
		ld(c("#faeed3"), c("#3a2c12")),
		ld(c("#e2ecfd"), c("#16283f")),
		ld(c("#e7eaf0"), c("#232833")),
	}
	t.labelFg = [5]color.Color{
		ld(c("#a0431f"), c("#ff9d72")),
		ld(c("#2f6fe4"), c("#8ab4f8")),
		ld(c("#2f7d43"), c("#84d69a")),
		ld(c("#6f42c1"), c("#c8a6fb")),
		ld(c("#8a5a00"), c("#ffc46b")),
	}
	t.labelBg = [5]color.Color{
		ld(c("#fbe6dc"), c("#33211a")),
		ld(c("#e2ecfd"), c("#16283f")),
		ld(c("#e0f2e5"), c("#16301f")),
		ld(c("#efe6fd"), c("#251c3a")),
		ld(c("#faeed3"), c("#33290f")),
	}
	return t
}

// ---------------------------------------------------------------------------
// Style factory (built once, never inside a render loop)
// ---------------------------------------------------------------------------

type styles struct {
	tok tokens

	gap    lipgloss.Style // one column of canvas between panels
	chrome lipgloss.Style
	brand  lipgloss.Style
	crumb  lipgloss.Style
	meta   lipgloss.Style
	metaHi lipgloss.Style

	filterBar   lipgloss.Style
	filterHint  lipgloss.Style
	filterOn    lipgloss.Style
	filterOff   lipgloss.Style
	filterCount lipgloss.Style

	panelEdge      lipgloss.Style
	panelEdgeFocus lipgloss.Style
	panelBody      lipgloss.Style
	band           lipgloss.Style
	bandFocus      lipgloss.Style
	bandTitle      lipgloss.Style
	bandTitleFocus lipgloss.Style
	bandCount      lipgloss.Style
	bandCountFocus lipgloss.Style
	dotTodo        lipgloss.Style
	dotDoing       lipgloss.Style
	dotDone        lipgloss.Style
	dotCancelled   lipgloss.Style

	card      lipgloss.Style
	cardFocus lipgloss.Style
	title     lipgloss.Style
	titleHi   lipgloss.Style
	seq       lipgloss.Style
	seqHi     lipgloss.Style
	age       lipgloss.Style
	sep       lipgloss.Style
	sepHi     lipgloss.Style
	body      lipgloss.Style
	bodyHi    lipgloss.Style
	desc      lipgloss.Style
	descHi    lipgloss.Style
	more      lipgloss.Style
	empty     lipgloss.Style

	prio     [5]lipgloss.Style
	prioHi   [5]lipgloss.Style
	blocked  lipgloss.Style
	due      lipgloss.Style
	overdue  lipgloss.Style
	effort   lipgloss.Style
	labelKey lipgloss.Style
	labelVal [5]lipgloss.Style
	labelRaw [5]lipgloss.Style

	rail      lipgloss.Style
	railCount lipgloss.Style
	railLabel lipgloss.Style

	shadow         lipgloss.Style
	overlay        lipgloss.Style
	overlayTitle   lipgloss.Style
	overlayEyebrow lipgloss.Style
	overlayRule    lipgloss.Style
	overlayText    lipgloss.Style
	overlayDim     lipgloss.Style
	overlayKey     lipgloss.Style
	overlayAct     lipgloss.Style

	footer    lipgloss.Style
	footerKey lipgloss.Style
	footerSep lipgloss.Style
}

// newStyles builds every style once. Callers cache the result; nothing below
// constructs a style per frame, per column, or per card.
func newStyles(isDark bool) styles {
	t := newTokens(isDark)
	s := styles{tok: t}

	base := lipgloss.NewStyle()
	pill := base.Padding(0, 1)

	s.gap = base.Background(t.bgBase)
	s.chrome = base.Background(t.bgChrome).Foreground(t.fgSubtle)
	s.brand = base.Background(t.bgChrome).Foreground(t.accent).Bold(true)
	s.crumb = base.Background(t.bgChrome).Foreground(t.fg)
	s.meta = base.Background(t.bgChrome).Foreground(t.fgMuted)
	s.metaHi = base.Background(t.bgChrome).Foreground(t.fgSubtle)

	s.filterBar = base.Background(t.bgBase).Foreground(t.fgMuted)
	s.filterHint = base.Background(t.bgBase).Foreground(t.fgSubtle)
	s.filterOn = pill.Background(t.accent).Foreground(t.fgOn).Bold(true)
	s.filterOff = pill.Background(t.bgChrome).Foreground(t.fgSubtle)
	s.filterCount = base.Background(t.bgBase).Foreground(t.fgMuted)

	s.panelEdge = base.Background(t.bgPanel).Foreground(t.borderPanel)
	s.panelEdgeFocus = base.Background(t.bgPanel).Foreground(t.borderFocus)
	s.panelBody = base.Background(t.bgPanel)
	s.band = base.Background(t.bgBand)
	s.bandFocus = base.Background(t.bgBandFocus)
	s.bandTitle = base.Background(t.bgBand).Foreground(t.fgSubtle).Bold(true)
	s.bandTitleFocus = base.Background(t.bgBandFocus).Foreground(t.fg).Bold(true)
	s.bandCount = base.Background(t.bgBand).Foreground(t.fgMuted)
	s.bandCountFocus = base.Background(t.bgBandFocus).Foreground(t.accent).Bold(true)
	s.dotTodo = base.Background(t.bgBand).Foreground(t.tintTodo)
	s.dotDoing = base.Background(t.bgBand).Foreground(t.tintDoing)
	s.dotDone = base.Background(t.bgBand).Foreground(t.tintDone)
	s.dotCancelled = base.Background(t.bgBand).Foreground(t.tintCancelled)

	s.card = base.
		Border(lipgloss.RoundedBorder()).
		BorderForeground(t.borderCard).
		BorderBackground(t.bgPanel).
		Background(t.bgCard).
		Padding(0, 1)
	s.cardFocus = s.card.
		BorderForeground(t.borderFocus).
		Background(t.bgCardFocus)

	s.title = base.Background(t.bgCard).Foreground(t.fg).Bold(true)
	s.titleHi = base.Background(t.bgCardFocus).Foreground(t.fg).Bold(true)
	s.seq = base.Background(t.bgCard).Foreground(t.fgMuted)
	s.seqHi = base.Background(t.bgCardFocus).Foreground(t.accent)
	s.age = base.Background(t.bgCard).Foreground(t.fgMuted)
	s.sep = base.Background(t.bgCard).Foreground(t.rule)
	s.sepHi = base.Background(t.bgCardFocus).Foreground(t.rule)
	s.body = base.Background(t.bgCard).Foreground(t.fgSubtle)
	s.bodyHi = base.Background(t.bgCardFocus).Foreground(t.fgSubtle)
	s.desc = base.Background(t.bgCard).Foreground(t.fgMuted)
	s.descHi = base.Background(t.bgCardFocus).Foreground(t.fgMuted)
	s.more =base.Background(t.bgPanel).Foreground(t.fgMuted).Italic(true)
	s.empty = base.Background(t.bgPanel).Foreground(t.fgMuted).Italic(true)

	for i := 1; i <= 4; i++ {
		s.prio[i] = base.Background(t.prioBg[i]).Foreground(t.prioFg[i]).Bold(true)
		s.prioHi[i] = s.prio[i]
	}
	s.blocked = base.Background(lipgloss.Color("#3a2a12")).Foreground(t.warn)
	s.due = base.Background(lipgloss.Color("#16301f")).Foreground(t.ok)
	s.overdue = base.Background(lipgloss.Color("#3a1a18")).Foreground(t.danger).Bold(true)
	s.effort = base.Background(lipgloss.Color("#232833")).Foreground(t.fgSubtle)
	s.labelKey = base.Background(t.bgBand).Foreground(t.fgSubtle)
	for i := range s.labelVal {
		s.labelVal[i] = base.Background(t.labelBg[i]).Foreground(t.labelFg[i])
		s.labelRaw[i] = base.Background(t.labelBg[i]).Foreground(t.labelFg[i])
	}

	s.rail = base.Background(t.bgPanel).Foreground(t.tintCancelled)
	s.railCount = base.Background(t.bgBand).Foreground(t.fgSubtle).Bold(true)
	s.railLabel = base.Background(t.bgPanel).Foreground(t.fgMuted)

	s.shadow = base.Background(t.bgShadow)
	s.overlay = base.
		Border(lipgloss.RoundedBorder()).
		BorderForeground(t.borderFocus).
		BorderBackground(t.bgBase).
		Background(t.bgCard).
		Padding(0, 2)
	s.overlayEyebrow = base.Background(t.bgCard).Foreground(t.accent).Bold(true)
	s.overlayTitle = base.Background(t.bgCard).Foreground(t.fg).Bold(true)
	s.overlayRule = base.Background(t.bgCard).Foreground(t.rule)
	s.overlayText = base.Background(t.bgCard).Foreground(t.fg)
	s.overlayDim = base.Background(t.bgCard).Foreground(t.fgMuted)
	s.overlayKey = base.Background(t.bgCard).Foreground(t.fgSubtle)
	s.overlayAct = base.Background(t.bgBand).Foreground(t.fgSubtle)

	s.footer = base.Background(t.bgChrome).Foreground(t.fgMuted)
	s.footerKey = base.Background(t.bgChrome).Foreground(t.fgSubtle).Bold(true)
	s.footerSep = base.Background(t.bgChrome).Foreground(t.rule)
	return s
}

// ---------------------------------------------------------------------------
// Fake board data, at real chip density
// ---------------------------------------------------------------------------

type card struct {
	emoji   string
	title   string
	seq     int
	age     string
	prio    int
	blocked bool
	due     string
	overdue bool
	effort  string
	tags    []string
	// desc is the card body. The board shows a truncated snippet of it under
	// the title; the detail overlay wraps it in full.
	desc string
}

type column struct {
	name  string
	key   string
	cards []card
}

func board() []column {
	return []column{
		{name: "TO DO", key: "todo", cards: []card{
			{
				emoji: "🐛", title: "Pointer hit-test drifts on wrapped meta", seq: 142,
				age: "3d old", prio: 1, blocked: true, due: "overdue 2d", overdue: true, effort: "M",
				tags: []string{"type::bug", "area::tui", "github#12"},
				desc: "Label spans are computed before the meta line wraps, so the second row of chips reports the x-range of the first. Clicking a pill on row two filters by the wrong tag.",
			},
			{
				emoji: "✨", title: "Design tokens package", seq: 139,
				age: "1d old", prio: 2, due: "in 3d", effort: "L",
				tags: []string{"type::feature", "area::tui"},
				desc: "Extract the semantic palette into internal/theme so every view reads roles instead of raw hex. Ships with a LightDark seam and one cached style factory.",
			},
			{
				emoji: "🔒", title: "Redact remote tokens in debug log", seq: 144,
				age: "new", prio: 2, effort: "S",
				tags: []string{"type::chore", "risk::high"},
				desc: "The forge client logs its Authorization header verbatim when KB_DEBUG is set. Mask everything after the scheme before the line reaches the writer.",
			},
			{
				emoji: "📝", title: "Document overlay z-order", seq: 147,
				age: "6d old", prio: 4,
				tags: []string{"type::docs"},
				desc: "Write down which layers the compositor stacks and in what order, so the next overlay does not have to be reverse-engineered from the render call.",
			},
		}},
		{name: "DOING", key: "doing", cards: []card{
			{
				emoji: "🚀", title: "Panel-framed board columns", seq: 140,
				age: "5h here", prio: 1, due: "today", effort: "L",
				tags: []string{"type::feature", "area::tui"},
				desc: "Give each status column a real box with its own header band, count, and rule. The card stack sits inset by one gutter column on each side.",
			},
			{
				emoji: "🧪", title: "Golden regen for restyle slices", seq: 141,
				age: "2d here", prio: 3, effort: "M",
				tags: []string{"type::test"},
				desc: "Every restyle slice invalidates the golden frames. Add a regen target that rewrites them in one pass and fails loudly on an unreviewed diff.",
			},
			{
				emoji: "🔧", title: "GetFrameSize layout math", seq: 143,
				age: "1d here", prio: 2, blocked: true,
				tags: []string{"type::chore", "github#12"},
				desc: "Replace the hardcoded border and padding constants with GetFrameSize calls so width math survives a border-style change.",
			},
		}},
		{name: "DONE", key: "done", cards: []card{
			{
				emoji: "📦", title: "lipgloss v2 design-system research", seq: 138,
				age: "shipped", prio: 2, effort: "M",
				tags: []string{"type::spike"},
				desc: "Surveyed the v2 style, layer, and canvas APIs and wrote up the posture this board follows: semantic tokens, cached styles, no per-frame construction.",
			},
			{
				emoji: "✨", title: "LightDark background probe", seq: 135,
				age: "shipped", prio: 3,
				tags: []string{"type::feature"},
				desc: "Query the terminal for its background at startup and resolve the palette once, with a flag to force either side when the probe times out.",
			},
			{
				emoji: "🐛", title: "Fix column scroll clamp", seq: 131,
				age: "shipped", prio: 1,
				tags: []string{"type::bug", "github#9"},
				desc: "Scrolling past the last card left the viewport pinned one row below the stack, so the bottom border vanished until the column was re-entered.",
			},
		}},
	}
}

const cancelledCount = 6

// focus: column 0, card 0.
const (
	focusColumn = 0
	focusCard   = 0
)

// ---------------------------------------------------------------------------
// Layout constants
// ---------------------------------------------------------------------------

const (
	railWidth      = 5 // collapsed CANCELLED rail
	colGap         = 1
	chromeRows     = 3  // header + filter bar + footer
	compactBelow   = 28 // terminal height under which cards drop to two rows
	panelChromeRow = 4  // top border + band + band rule + bottom border
	descLines      = 2  // description snippet rows on a normal-density card
)

// ---------------------------------------------------------------------------
// Small render helpers
// ---------------------------------------------------------------------------

func w(s string) int { return ansi.StringWidth(s) }

func repeat(s string, n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat(s, n)
}

// padTo pads a pre-styled segment out to width using filler, which must already
// carry the surrounding background. Over-wide segments are truncated: nothing in
// this prototype is allowed to overflow its box.
func padTo(seg string, width int, fill lipgloss.Style) string {
	if w(seg) > width {
		return ansi.Truncate(seg, width, "")
	}
	return seg + fill.Render(repeat(" ", width-w(seg)))
}

// fitParts keeps the longest prefix of parts that still fits beside left and
// right, dropping trailing parts rather than clipping them mid-glyph.
func fitParts(left string, parts []string, right string, width int, fill lipgloss.Style) string {
	kept := left
	for _, part := range parts {
		if w(kept)+w(part)+w(right) > width {
			break
		}
		kept += part
	}
	return row(kept, right, width, fill)
}

// row assembles left and right segments with filler between them.
func row(left, right string, width int, fill lipgloss.Style) string {
	space := width - w(left) - w(right)
	if space < 1 {
		return ansi.Truncate(left, max(width-w(right), 0), "") + right
	}
	return left + fill.Render(repeat(" ", space)) + right
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// pack greedily packs pre-styled chips into lines of at most width, separated by
// one space of the surrounding background. Overflow past maxLines collapses into
// a trailing "+N" counter.
func pack(chips []string, width, maxLines int, fill lipgloss.Style) []string {
	if len(chips) == 0 {
		return nil
	}
	sep := fill.Render(" ")
	lines := []string{}
	cur, curW := "", 0
	for i, chip := range chips {
		cw := w(chip)
		switch {
		case cur == "":
			cur, curW = chip, cw
		case curW+1+cw <= width:
			cur, curW = cur+sep+chip, curW+1+cw
		default:
			if len(lines)+1 == maxLines {
				rest := fmt.Sprintf("+%d", len(chips)-i)
				if curW+1+w(rest) <= width {
					cur = cur + sep + fill.Render(rest)
				}
				lines = append(lines, cur)
				return lines
			}
			lines = append(lines, cur)
			cur, curW = chip, cw
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

// snippet wraps text to width and keeps at most maxLines of it. When the text
// does not fit, the last kept line ends in an ellipsis so the truncation is
// visible. No returned line is ever wider than width.
func snippet(text string, width, maxLines int) []string {
	if text == "" || width < 2 || maxLines < 1 {
		return nil
	}
	wrapped := strings.Split(ansi.Wrap(text, width, " -"), "\n")
	if len(wrapped) <= maxLines {
		return wrapped
	}
	out := wrapped[:maxLines]
	last := strings.TrimRight(ansi.Truncate(out[maxLines-1], width-1, ""), " ")
	out[maxLines-1] = last + "…"
	return out
}

// ---------------------------------------------------------------------------
// Chips
// ---------------------------------------------------------------------------

func labelIndex(tag string) int {
	sum := 0
	for _, r := range tag {
		sum += int(r)
	}
	return sum % 5
}

// shortAge collapses an age chip to its compact form for narrow cards.
func shortAge(age string) string {
	switch {
	case age == "shipped":
		return "✓"
	case age == "new":
		return "new"
	default:
		return strings.SplitN(age, " ", 2)[0]
	}
}

// shortDue collapses a due chip to its compact form for narrow cards.
func shortDue(due string, overdue bool) string {
	switch {
	case overdue:
		return strings.TrimPrefix(due, "overdue ") + "!"
	case due == "tomorrow":
		return "1d"
	case strings.HasPrefix(due, "in "):
		return strings.TrimPrefix(due, "in ")
	default:
		return due
	}
}

// chips returns the meta chips of a card in board order: priority, blocked,
// due, effort. Label pills are returned separately so the card can give them
// their own row when there is height for it.
func (s styles) chips(c card, compact bool) []string {
	out := []string{s.prio[c.prio].Render(fmt.Sprintf(" P%d ", c.prio))}
	if c.blocked {
		label := " ⛔ blocked "
		if compact {
			label = " ⛔ "
		}
		out = append(out, s.blocked.Render(label))
	}
	if c.due != "" {
		style := s.due
		if c.overdue {
			style = s.overdue
		}
		label := " ◷ " + c.due + " "
		if compact {
			label = " ◷" + shortDue(c.due, c.overdue) + " "
		}
		out = append(out, style.Render(label))
	}
	if c.effort != "" {
		label := " ◇ " + c.effort + " "
		if compact {
			label = " " + c.effort + " "
		}
		out = append(out, s.effort.Render(label))
	}
	return out
}

func (s styles) pills(c card, compact bool) []string {
	out := make([]string, 0, len(c.tags))
	for _, tag := range c.tags {
		i := labelIndex(tag)
		key, value, scoped := strings.Cut(tag, "::")
		if !scoped || key == "" || value == "" {
			out = append(out, s.labelRaw[i].Render(" #"+tag+" "))
			continue
		}
		if compact {
			out = append(out, s.labelVal[i].Render(" "+value+" "))
			continue
		}
		out = append(out, s.labelKey.Render(" "+key+":")+s.labelVal[i].Render(value+" "))
	}
	return out
}

// ---------------------------------------------------------------------------
// Card surface
// ---------------------------------------------------------------------------

// cardStyles is the per-column cached pair of card styles, already width-bound.
type cardStyles struct {
	normal  lipgloss.Style
	focused lipgloss.Style
	content int
}

// cardStyles binds the cached card styles to one column's width. lipgloss v2
// Width() is total-inclusive (border + padding + content), so the content
// budget is the outer width less GetFrameSize.
func (s styles) cardStyles(outer int) cardStyles {
	frame, _ := s.card.GetFrameSize()
	return cardStyles{
		normal:  s.card.Width(outer),
		focused: s.cardFocus.Width(outer),
		content: max(outer-frame, 1),
	}
}

func (s styles) renderCard(c card, cs cardStyles, focused, compact bool) []string {
	fill := s.body
	titleStyle, seqStyle, sepStyle, descStyle := s.title, s.seq, s.sep, s.desc
	if focused {
		fill, titleStyle, seqStyle, sepStyle, descStyle =
			s.bodyHi, s.titleHi, s.seqHi, s.sepHi, s.descHi
	}
	width := cs.content
	seq := seqStyle.Render(fmt.Sprintf("#%d", c.seq))

	var lines []string
	if compact {
		head := c.emoji + " " + c.title
		avail := width - w(seq) - 1
		lines = append(lines, row(titleStyle.Render(ansi.Truncate(head, max(avail, 1), "…")), seq, width, fill))
		age := fill.Render(shortAge(c.age))
		meta := append(s.chips(c, true), s.pills(c, true)...)
		for _, line := range pack(meta, max(width-w(age)-1, 1), 1, fill) {
			lines = append(lines, row(line, age, width, fill))
		}
	} else {
		head := c.emoji + " " + c.title
		wrapped := strings.Split(ansi.Wrap(head, width, " -"), "\n")
		for i, line := range wrapped {
			if i == 2 {
				break
			}
			lines = append(lines, padTo(titleStyle.Render(line), width, fill))
		}
		lines = append(lines, row(seq, fill.Render(c.age), width, fill))
		lines = append(lines, sepStyle.Render(repeat("╌", width)))
		for _, line := range snippet(c.desc, width, descLines) {
			lines = append(lines, padTo(descStyle.Render(line), width, fill))
		}
		for _, line := range pack(s.chips(c, false), width, 2, fill) {
			lines = append(lines, padTo(line, width, fill))
		}
		for _, line := range pack(s.pills(c, false), width, 2, fill) {
			lines = append(lines, padTo(line, width, fill))
		}
	}

	style := cs.normal
	if focused {
		style = cs.focused
	}
	return strings.Split(style.Render(strings.Join(lines, "\n")), "\n")
}

// ---------------------------------------------------------------------------
// Column panel
// ---------------------------------------------------------------------------

func (s styles) dot(key string) lipgloss.Style {
	switch key {
	case "doing":
		return s.dotDoing
	case "done":
		return s.dotDone
	case "cancelled":
		return s.dotCancelled
	default:
		return s.dotTodo
	}
}

// renderPanel draws one column: a full box, a header band with its own
// background, a rule that separates the band from the stack, then the card
// stack inset by one gutter column on each side.
func (s styles) renderPanel(col column, index, width, height int, compact bool) []string {
	focused := index == focusColumn
	edge := s.panelEdge
	band, bandTitle, bandCount := s.band, s.bandTitle, s.bandCount
	if focused {
		edge = s.panelEdgeFocus
		band, bandTitle, bandCount = s.bandFocus, s.bandTitleFocus, s.bandCountFocus
	}
	inner := width - 2

	dot := s.dot(col.key)
	if focused {
		dot = dot.Background(s.tok.bgBandFocus)
	}
	head := dot.Render("●") + bandTitle.Render(" "+col.name)
	count := bandCount.Render(fmt.Sprintf("%d ", len(col.cards)))
	bandLine := edge.Render("│") + row(band.Render(" ")+head, count, inner, band) + edge.Render("│")

	lines := []string{
		edge.Render("╭" + repeat("─", inner) + "╮"),
		bandLine,
		edge.Render("├" + repeat("─", inner) + "┤"),
	}

	bodyRows := height - panelChromeRow
	gutter := s.panelBody.Render(" ")
	blank := edge.Render("│") + s.panelBody.Render(repeat(" ", inner)) + edge.Render("│")
	wrap := func(content string) string {
		return edge.Render("│") + gutter + content + gutter + edge.Render("│")
	}

	cs := s.cardStyles(inner - 2)
	stack := []string{}
	shown := 0
	for i, c := range col.cards {
		block := s.renderCard(c, cs, focused && i == focusCard, compact)
		need := len(block)
		if i > 0 {
			need++
		}
		remaining := bodyRows - len(stack)
		if i < len(col.cards)-1 {
			remaining-- // keep a row for the "+N more" counter
		}
		if need > remaining {
			break
		}
		if i > 0 {
			stack = append(stack, "")
		}
		stack = append(stack, block...)
		shown++
	}

	body := make([]string, 0, bodyRows)
	for _, line := range stack {
		if line == "" {
			body = append(body, blank)
			continue
		}
		body = append(body, wrap(line))
	}
	if shown < len(col.cards) {
		note := s.more.Render(fmt.Sprintf("+%d more", len(col.cards)-shown))
		body = append(body, wrap(padTo(note, cs.content+4, s.panelBody)))
	}
	if len(col.cards) == 0 {
		body = append(body, wrap(padTo(s.empty.Render("nothing here"), cs.content+4, s.panelBody)))
	}
	for len(body) < bodyRows {
		body = append(body, blank)
	}
	lines = append(lines, body[:bodyRows]...)
	lines = append(lines, edge.Render("╰"+repeat("─", inner)+"╯"))
	return lines
}

// renderRail draws the collapsed CANCELLED column: toggled off, still present,
// still carrying its count.
func (s styles) renderRail(width, height int) []string {
	edge := s.panelEdge
	inner := width - 2
	lines := []string{
		edge.Render("╭" + repeat("─", inner) + "╮"),
		edge.Render("│") + centered(s.rail.Background(s.tok.bgBand).Render("⊘"), inner, s.band) + edge.Render("│"),
		edge.Render("├" + repeat("─", inner) + "┤"),
	}
	bodyRows := height - panelChromeRow
	letters := []string{"C", "A", "N", "C", "E", "L", "L", "E", "D"}
	pad := max((bodyRows-len(letters))/2, 1)
	for i := 0; i < bodyRows; i++ {
		g, style := "", s.railLabel
		switch {
		case i == 0:
			g, style = fmt.Sprintf("%d", cancelledCount), s.railCount.Background(s.tok.bgPanel)
		case i >= pad && i-pad < len(letters):
			g = letters[i-pad]
		}
		lines = append(lines, edge.Render("│")+centered(style.Render(g), inner, s.panelBody)+edge.Render("│"))
	}
	lines = append(lines, edge.Render("╰"+repeat("─", inner)+"╯"))
	return lines
}

func centered(seg string, width int, fill lipgloss.Style) string {
	space := width - w(seg)
	if space <= 0 {
		return ansi.Truncate(seg, width, "")
	}
	left := space / 2
	return fill.Render(repeat(" ", left)) + seg + fill.Render(repeat(" ", space-left))
}

// ---------------------------------------------------------------------------
// Chrome
// ---------------------------------------------------------------------------

func (s styles) renderHeader(width int, cols []column) string {
	open := 0
	for _, c := range cols {
		if c.key != "done" {
			open += len(c.cards)
		}
	}
	left := s.brand.Render("▌kb") + s.crumb.Render("  board") + s.meta.Render(" · ") + s.crumb.Render("kb/main")
	right := s.chrome.Render(" ")
	parts := []string{
		s.meta.Render("   ") + s.metaHi.Render(fmt.Sprintf("%d open", open)),
		s.meta.Render("  ·  ") + s.metaHi.Render(fmt.Sprintf("%d cancelled hidden", cancelledCount)),
		s.meta.Render("  ·  ") + s.metaHi.Render("truecolor"),
	}
	return fitParts(left, parts, right, width, s.chrome)
}

func (s styles) renderFilterBar(width int) string {
	left := s.filterHint.Render(" ⌕ ") + s.filterBar.Render("filter cards")
	parts := []string{
		s.filterBar.Render("   ") + s.filterOn.Render("x area::tui"),
		s.filterBar.Render(" ") + s.filterOff.Render("+ type::bug"),
		s.filterBar.Render(" ") + s.filterOff.Render("+ github#12"),
		s.filterBar.Render(" ") + s.filterOff.Render("+ risk::high"),
	}
	right := s.filterCount.Render("10 of 16 cards ")
	return fitParts(left, parts, right, width, s.filterBar)
}

func (s styles) renderFooter(width int) string {
	keys := []struct{ k, l string }{
		{"j/k", "cards"}, {"h/l", "columns"}, {"enter", "open"}, {"n", "new"},
		{"c", "cancelled:off"}, {"/", "filter"}, {"?", "help"}, {"q", "quit"},
	}
	sep := s.footerSep.Render(" │ ")
	parts := make([]string, 0, len(keys))
	for i, k := range keys {
		part := s.footerKey.Render(k.k) + s.footer.Render(" "+k.l)
		if i > 0 {
			part = sep + part
		}
		parts = append(parts, part)
	}
	right := s.footer.Render("web-faithful · #137 ")
	return fitParts(s.footer.Render(" "), parts, right, width, s.footer)
}

// ---------------------------------------------------------------------------
// Board frame
// ---------------------------------------------------------------------------

func (s styles) renderBoard(width, height int) []string {
	cols := board()
	compact := height < compactBelow

	boardRows := height - chromeRows
	usable := width - railWidth - colGap*len(cols)
	panelW := usable / len(cols)
	extra := usable % len(cols)

	rendered := make([][]string, 0, len(cols)+1)
	widths := make([]int, 0, len(cols)+1)
	for i, col := range cols {
		pw := panelW
		if i < extra {
			pw++
		}
		rendered = append(rendered, s.renderPanel(col, i, pw, boardRows, compact))
		widths = append(widths, pw)
	}
	rendered = append(rendered, s.renderRail(railWidth, boardRows))
	widths = append(widths, railWidth)

	gap := s.gap.Render(repeat(" ", colGap))
	lines := []string{s.renderHeader(width, cols), s.renderFilterBar(width)}
	for r := 0; r < boardRows; r++ {
		parts := make([]string, 0, len(rendered)*2)
		for i, panel := range rendered {
			if i > 0 {
				parts = append(parts, gap)
			}
			line := ""
			if r < len(panel) {
				line = panel[r]
			}
			parts = append(parts, padTo(line, widths[i], s.gap))
		}
		lines = append(lines, padTo(strings.Join(parts, ""), width, s.gap))
	}
	lines = append(lines, s.renderFooter(width))
	return lines
}

// ---------------------------------------------------------------------------
// Card-detail overlay
// ---------------------------------------------------------------------------

func (s styles) renderOverlay(width, height int) []string {
	c := board()[focusColumn].cards[focusCard]

	ow := min(width-10, 74)
	oh := min(height-6, 20)
	frameW, frameH := s.overlay.GetFrameSize()
	inner := ow - frameW
	contentRows := oh - frameH

	fill := s.overlayText
	lines := []string{
		row(s.overlayEyebrow.Render("CARD DETAIL"), s.overlayDim.Render("esc close"), inner, fill),
		s.overlayRule.Render(repeat("─", inner)),
		row(s.overlayTitle.Render(ansi.Truncate(c.emoji+" "+c.title, inner-8, "…")),
			s.overlayEyebrow.Render(fmt.Sprintf("#%d", c.seq)), inner, fill),
	}
	for _, line := range pack(append(s.chips(c, false), s.pills(c, false)...), inner, 2, fill) {
		lines = append(lines, padTo(line, inner, fill))
	}
	lines = append(lines, fill.Render(repeat(" ", inner)))
	for _, line := range strings.Split(ansi.Wrap(c.desc, inner, " -"), "\n") {
		lines = append(lines, padTo(s.overlayKey.Render(line), inner, fill))
	}
	lines = append(lines, fill.Render(repeat(" ", inner)))
	lines = append(lines, padTo(s.overlayDim.Render("created 3d ago  ·  moved 2d ago  ·  assignee @aksOps  ·  status todo"), inner, fill))

	for len(lines) < contentRows-1 {
		lines = append(lines, fill.Render(repeat(" ", inner)))
	}
	lines = lines[:max(contentRows-1, 0)]
	actions := s.overlayAct.Render(" e edit ") + fill.Render(" ") +
		s.overlayAct.Render(" x toggle blocked ") + fill.Render(" ") +
		s.overlayAct.Render(" m move ") + fill.Render(" ") +
		s.overlayAct.Render(" D delete ")
	lines = append(lines, padTo(actions, inner, fill))

	box := strings.Split(s.overlay.Width(ow).Render(strings.Join(lines, "\n")), "\n")

	// Compose: board, drop shadow, overlay.
	x := (width - ow) / 2
	y := (height - oh) / 2
	shadowLines := make([]string, len(box))
	for i := range shadowLines {
		shadowLines[i] = s.shadow.Render(repeat(" ", ow))
	}

	canvas := lipgloss.NewCanvas(width, height)
	canvas.Compose(lipgloss.NewCompositor(
		lipgloss.NewLayer(strings.Join(s.renderBoard(width, height), "\n")).ID("board"),
		lipgloss.NewLayer(strings.Join(shadowLines, "\n")).X(x+2).Y(y+1).Z(1).ID("shadow"),
		lipgloss.NewLayer(strings.Join(box, "\n")).X(x).Y(y).Z(2).ID("detail"),
	))

	out := strings.Split(canvas.Render(), "\n")
	for len(out) < height {
		out = append(out, "")
	}
	out = out[:height]
	for i, line := range out {
		out[i] = padTo(line, width, s.gap)
	}
	return out
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

func main() {
	width := flag.Int("width", 140, "frame width in cells")
	height := flag.Int("height", 40, "frame height in cells")
	overlay := flag.Bool("overlay", false, "render with the card-detail overlay open")
	light := flag.Bool("light", false, "resolve the palette for a light terminal background")
	plain := flag.Bool("plain", false, "strip ANSI from the output")
	check := flag.String("check", "", "measure an existing capture instead of rendering")
	flag.Parse()

	if *check != "" {
		measure(*check, *width, *height)
		return
	}

	s := newStyles(!*light)
	var lines []string
	if *overlay {
		lines = s.renderOverlay(*width, *height)
	} else {
		lines = s.renderBoard(*width, *height)
	}
	out := strings.Join(lines, "\n")
	if *plain {
		out = ansi.Strip(out)
	}
	fmt.Println(out)
}

// measure verifies a capture fits its target box: exact line count, no line
// wider than the target width.
func measure(path string, width, height int) {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	widest, at := 0, 0
	for i, line := range lines {
		if lw := w(line); lw > widest {
			widest, at = lw, i+1
		}
	}
	status := "ok"
	code := 0
	if len(lines) != height || widest > width {
		status = "FAIL"
		code = 1
	}
	fmt.Printf("%-34s lines=%d/%d widest=%d/%d (line %d) %s\n",
		path, len(lines), height, widest, width, at, status)
	os.Exit(code)
}
