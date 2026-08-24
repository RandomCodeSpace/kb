package cmdpalette

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/action"
	"github.com/RandomCodeSpace/kb/internal/tui/formview"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
	"github.com/RandomCodeSpace/kb/internal/tui/widget"
)

// paletteTitle is the header band of spec section 4 step 4.
const paletteTitle = "COMMAND PALETTE"

// footerHints is the dismissal ladder of the footer band. Hints only: a band
// re-arms its own style around its content, so a button in a band row would
// drop the band background for the rest of the row.
const footerHints = "enter run | up/down move | esc close"

// frame is the resolved panel geometry for one render.
type frame struct {
	x, y          int
	width, height int
	inner         int // static body row measure
	focus         int // focusable body row measure, two columns narrower
	rows          int // body rows the panel has
}

// rowKind distinguishes the two things a result list is made of.
type rowKind uint8

const (
	rowSection rowKind = iota
	rowEntry
)

// listRow is one rendered line of the result list. Section bands are rows in
// the same slice as entries because the panel scrolls by slicing that slice;
// entry is the index into the model's entries, or a negative value for a band.
type listRow struct {
	kind  rowKind
	group action.Group
	entry int
}

// Overlay composes the palette over the board surface. Spec section 4:
// elevation is a shade step plus a shadow, never a frame.
func (m Model) Overlay(background string, width, height int) string {
	if !m.open {
		return background
	}
	width, height = max(width, 1), max(height, 1)
	background = fitBlock(background, width, height)
	panel := m.layout(width, height)
	layers := append(
		[]*lipgloss.Layer{lipgloss.NewLayer(background)},
		widget.OverlayLayers(m.themeStyles(), m.panelOpts(panel), panel.x, panel.y)...,
	)
	return fitBlock(lipgloss.NewCompositor(layers...).Render(), width, height)
}

// View renders the panel centered on the terminal with no board behind it.
func (m Model) View(width, height int) string {
	if !m.open {
		return ""
	}
	width, height = max(width, 1), max(height, 1)
	panel := m.layout(width, height)
	rendered := fitBlock(widget.Overlay(m.themeStyles(), m.panelOpts(panel)), width, height)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, rendered)
}

// layout resolves the proportional panel of spec section 4 against the frame.
// Nothing here is a literal: the geometry is the theme's.
func (m Model) layout(width, height int) frame {
	metrics := m.themeStyles().Metrics
	paneWidth, paneHeight := metrics.OverlayPane(width, height)
	return frame{
		x:      max((width-paneWidth)/2, 0),
		y:      max((height-paneHeight)/2, 0),
		width:  paneWidth,
		height: paneHeight,
		inner:  metrics.OverlayContent(paneWidth),
		focus:  metrics.OverlayFocusContent(paneWidth),
		rows:   max(paneHeight-2, 0),
	}
}

func (m Model) panelOpts(panel frame) widget.OverlayOpts {
	return widget.OverlayOpts{
		Title:  paletteTitle,
		Body:   m.bodyRows(panel),
		Footer: footerHints,
		Hint:   m.scrollHint(),
		Width:  panel.width,
		Height: panel.height,
	}
}

// scrollHint states the cursor's position in the result list, which is the one
// thing a windowed list cannot say for itself.
func (m Model) scrollHint() string {
	if len(m.entries) == 0 {
		return ""
	}
	return widget.ScrollHint(m.themeStyles(), m.cursor+1, len(m.entries), theme.OverlayBand)
}

// bodyRows is the query field followed by the windowed result list, or by the
// empty state when nothing matched. Spec section 10.8.3: a surface with no
// content renders that row, never a bare blank panel.
func (m Model) bodyRows(panel frame) []string {
	styles := m.themeStyles()
	if panel.rows <= 0 {
		return nil
	}
	rows := []string{m.queryRow(panel)}
	visible := panel.rows - len(rows)
	if visible <= 0 {
		return rows
	}
	if len(m.entries) == 0 {
		return append(rows, widget.OverlayRow(styles, widget.Empty(styles, widget.EmptyOpts{
			Headline: "no matching actions",
			Key:      "esc",
			Verb:     "close",
			On:       theme.OverlaySurf,
			Width:    panel.inner,
		}), panel.width))
	}
	list := m.listRows()
	for _, row := range window(list, m.cursorRow(list), visible) {
		rows = append(rows, m.renderListRow(panel, row))
	}
	return rows
}

// queryRow is the search field. It is a focusable row and always has the
// keyboard, so it carries the focus gutter of spec section 10.4.3 in its
// focused state; reserving the same two columns on every row below it is what
// keeps the left edge from ragging.
func (m Model) queryRow(panel frame) string {
	styles := m.themeStyles()
	gutter := widget.Gutter(styles, true, theme.Brand, theme.OverlaySurf)
	field := formview.Input(m.query, true, max(panel.focus, 1), sanitize, cursorViewport)
	return widget.OverlayRow(styles, gutter+field, panel.width)
}

// listRows expands the entries into rendered lines, inserting a section band
// ahead of each group when the list is unfiltered and so genuinely grouped.
func (m Model) listRows() []listRow {
	rows := make([]listRow, 0, len(m.entries)+2)
	grouped := Grouped(sanitize(m.query.Value()))
	seen := false
	var current action.Group
	for index, entry := range m.entries {
		if grouped && (!seen || entry.Action.Group != current) {
			rows = append(rows, listRow{kind: rowSection, group: entry.Action.Group, entry: -1})
			current, seen = entry.Action.Group, true
		}
		rows = append(rows, listRow{kind: rowEntry, entry: index})
	}
	return rows
}

// cursorRow is the display line the cursor sits on, so the scroll window can
// follow it past the section bands it does not select.
func (m Model) cursorRow(list []listRow) int {
	for index, row := range list {
		if row.kind == rowEntry && row.entry == m.cursor {
			return index
		}
	}
	return 0
}

// window slices at most size lines around focus, keeping the focused line
// inside and the window flush against whichever end it has reached.
func window(list []listRow, focus, size int) []listRow {
	if size <= 0 || len(list) == 0 {
		return nil
	}
	if len(list) <= size {
		return list
	}
	start := min(max(focus-size/2, 0), len(list)-size)
	return list[start : start+size]
}

// renderListRow draws one line of the result list.
func (m Model) renderListRow(panel frame, row listRow) string {
	styles := m.themeStyles()
	if row.kind == rowSection {
		return widget.Section(styles, row.group.Label(), "", panel.width)
	}
	entry := m.entries[row.entry]
	focused := row.entry == m.cursor
	gutter := widget.Gutter(styles, focused, theme.Brand, theme.OverlaySurf)
	return widget.OverlayRow(styles, gutter+m.entryText(entry, focused, panel.focus), panel.width)
}

// entryText is one action's name with its matched runs highlighted, and its key
// hint right-aligned in the row's remaining columns.
//
// The name is cut to the columns it has before the highlight is resolved, and
// the match offsets are cut with it: styling a run that truncation removed
// would put the cue on whatever text slid into its place.
func (m Model) entryText(entry Entry, focused bool, width int) string {
	styles := m.themeStyles()
	hint := entry.Action.Hint
	hintWidth := ansi.StringWidth(hint)
	nameWidth := max(width-hintWidth-1, 1)
	if hintWidth+1 >= width {
		hint, hintWidth = "", 0
		nameWidth = width
	}
	name := fit(entry.Action.Name, nameWidth)
	base := styles.On(theme.FgBase, theme.OverlaySurf)
	if focused {
		base = styles.OnBold(theme.FgBase, theme.OverlaySurf)
	}
	rendered := widget.Highlight(base, styles.OnBold(theme.Brand, theme.OverlaySurf), name, within(entry.Matched, name))
	gap := max(width-ansi.StringWidth(name)-hintWidth, 0)
	surface := styles.On(theme.FgBase, theme.OverlaySurf)
	if hint == "" {
		return rendered + surface.Render(strings.Repeat(" ", gap))
	}
	return rendered +
		surface.Render(strings.Repeat(" ", gap)) +
		styles.On(theme.FgMuted, theme.OverlaySurf).Render(hint)
}

// within drops the match offsets truncation removed.
func within(matched []int, name string) []int {
	kept := make([]int, 0, len(matched))
	for _, offset := range matched {
		if offset >= 0 && offset < len(name) {
			kept = append(kept, offset)
		}
	}
	return kept
}

// fit is the plain-text truncation of spec section 3.3, applied before styling
// so the result can still be measured and offset into.
func fit(value string, width int) string {
	if width <= 0 {
		return ""
	}
	value = sanitize(value)
	if ansi.StringWidth(value) <= width {
		return value
	}
	return ansi.Truncate(value, width, "")
}

// cursorViewport is the text-cursor window of the shared form renderer.
func cursorViewport(value string, position, width int) string {
	if width <= 0 {
		return ""
	}
	if width == 1 {
		return "|"
	}
	runes := []rune(value)
	position = min(max(position, 0), len(runes))
	before, after := string(runes[:position]), string(runes[position:])
	content := width - 1
	column := ansi.StringWidth(before)
	visible := ansi.Cut(before, max(column-content, 0), column)
	return visible + "|" + ansi.Truncate(after, max(content-ansi.StringWidth(visible), 0), "")
}

// fitBlock pads or cuts a rendered block to exactly width by height.
func fitBlock(block string, width, height int) string {
	lines := strings.Split(block, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	for index, line := range lines {
		if ansi.StringWidth(line) > width {
			line = ansi.Truncate(line, width, "")
		}
		lines[index] = line + strings.Repeat(" ", max(width-ansi.StringWidth(line), 0))
	}
	return strings.Join(lines, "\n")
}
