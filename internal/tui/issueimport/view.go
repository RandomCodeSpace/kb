package issueimport

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/formview"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
	"github.com/RandomCodeSpace/kb/internal/tui/widget"
)

// rowKind is the semantic role of one body row. It decides which token the row
// is rendered with; no view code composes a style of its own (spec section 6.2).
type rowKind uint8

const (
	rowBody rowKind = iota
	rowField
	rowSection
	rowHint
	rowError
)

// importButton is one action inside a row, with its column offset in the row's
// plain text so the pointer map can key a rect to it.
type importButton struct {
	label  string
	target string
	x0     int
}

// importRow is one logical body row. Targets are carried structurally rather
// than recovered by matching rendered text: a forge draft's title is untrusted
// input, and text matching let it impersonate a control.
type importRow struct {
	text     string
	rendered string
	target   string
	buttons  []importButton
	kind     rowKind
}

// importFrame is the resolved geometry of one render. The panel is as tall as
// its content: the review stage windows its own rows (reviewWindow), so the
// panel never scrolls and never pads out to the frame.
type importFrame struct {
	x, y   int
	width  int
	height int
	inner  int
	rows   []importRow
}

// Overlay composes the import panel over the board surface. Spec section 4:
// elevation is a shade step plus a shadow, never a frame.
func (m Model) Overlay(background string, width, height int) string {
	if !m.open {
		return background
	}
	width, height = max(width, 1), max(height, 1)
	background = fitBlock(background, width, height)
	frame := m.layout(width, height)
	layers := append(
		[]*lipgloss.Layer{lipgloss.NewLayer(background)},
		widget.OverlayLayers(m.themeStyles(), m.panelOpts(frame), frame.x, frame.y)...,
	)
	return fitBlock(lipgloss.NewCompositor(layers...).Render(), width, height)
}

// View renders the panel centered on the terminal, with no board behind it.
func (m Model) View(width, height int) string {
	if !m.open {
		return ""
	}
	width, height = max(width, 1), max(height, 1)
	frame := m.layout(width, height)
	panel := fitBlock(widget.Overlay(m.themeStyles(), m.panelOpts(frame)), width, height)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, panel)
}

func (m Model) panelOpts(frame importFrame) widget.OverlayOpts {
	return widget.OverlayOpts{
		Title:  "FORGE ISSUE IMPORT",
		Body:   m.visibleRows(frame),
		Footer: m.footerLine(frame.inner),
		Width:  frame.width,
		Height: frame.height,
	}
}

func (m Model) layout(width, height int) importFrame {
	width, height = max(width, 1), max(height, 1)
	metrics := m.themeStyles().Metrics
	paneWidth, paneHeight := metrics.OverlayPane(width, height)
	inner := metrics.OverlayContent(paneWidth)
	// The panel height no longer follows the row count, so the review window is
	// sized against the panel body the rows actually land in rather than the
	// frame around it.
	rows := m.bodyRows(inner, max(paneHeight-2, 1))
	return importFrame{
		x:      max((width-paneWidth)/2, 0),
		y:      max((height-paneHeight)/2, 0),
		width:  paneWidth,
		height: paneHeight,
		inner:  inner,
		rows:   rows,
	}
}

func (m Model) visibleRows(frame importFrame) []string {
	styles := m.themeStyles()
	body := max(frame.height-2, 0)
	visible := make([]string, 0, body)
	for _, row := range frame.rows[:min(body, len(frame.rows))] {
		if row.kind == rowSection {
			visible = append(visible, widget.Section(styles, fit(row.text, frame.inner), "", frame.width))
			continue
		}
		visible = append(visible, widget.OverlayRow(styles, m.renderRow(row, frame.inner), frame.width))
	}
	return visible
}

// renderRow applies the token the row's role names.
func (m Model) renderRow(row importRow, width int) string {
	styles := m.themeStyles()
	if row.rendered != "" {
		return ansi.Truncate(row.rendered, max(width, 0), "")
	}
	line := fit(row.text, width)
	switch row.kind {
	case rowError:
		return styles.On(theme.StatusDanger, theme.OverlaySurf).Render(line)
	case rowHint:
		return styles.Overlay.FieldLabel.Render(line)
	case rowField:
		if m.focusTarget() == row.target {
			return formview.Selection(
				styles.OnBold(theme.FgBase, theme.OverlaySurf),
				row.target == refMarkField && m.mark.Active(refMarkField),
			).Render(line)
		}
		return styles.Overlay.FieldValue.Render(line)
	default:
		return styles.Overlay.Surf.Render(line)
	}
}

func (m Model) bodyRows(width, height int) []importRow {
	if m.stage == stageInput {
		return m.inputRows(width)
	}
	return m.reviewRows(width, height)
}

func (m Model) inputRows(width int) []importRow {
	styles := m.themeStyles()
	rows := []importRow{
		{text: "SOURCE", kind: rowSection},
		m.sourceRow(width),
		m.refRow(width),
		m.fieldRow("max", fmt.Sprintf("max     %d", m.max)),
	}
	if m.operation == "preview" {
		rows = append(rows, importRow{}, importRow{
			text: m.busyPrefix() + "fetching configured forge data and drafting...",
			kind: rowHint,
		})
	}
	return append(rows, importRow{}, m.actionsRow(styles,
		importAction{target: "import", label: "Import", variant: theme.ButtonPrimary},
		importAction{target: "cancel", label: "Cancel"}))
}

// sourceRow is the forge choice. Spec section 5.2 assigns huh's Select to the
// Source field; the inline form keeps it on the single row the frozen v1.0.1
// layout and its per-row pointer target give it.
func (m Model) sourceRow(width int) importRow {
	styles := m.themeStyles()
	label := m.controlMark("source", m.focus == 0) + "source  "
	names := make([]string, 0, len(m.sources))
	for _, source := range m.sources {
		names = append(names, source.Name)
	}
	if len(names) == 0 {
		return m.fieldRow("source", "source  "+m.sourceName())
	}
	surface := styles.Overlay.FieldValue
	if m.focus == 0 {
		surface = styles.OnBold(theme.FgBase, theme.OverlaySurf)
	}
	field := max(width-ansi.StringWidth(label), 1)
	return importRow{
		text:     label + m.sourceName(),
		rendered: surface.Render(label) + formview.HuhInlineSelect(styles, names, m.source, field),
		target:   "source",
		kind:     rowField,
	}
}

// refMarkField is the reference input's control target, and the field name it
// carries in the select-all mark.
const refMarkField = "ref"

// refRow is the reference input. It goes through the shared form renderer for
// the same reason the other overlays do: a bubbles textinput renders its own
// escapes, and this row's own truncation used to fold them into visible text.
func (m Model) refRow(width int) importRow {
	const label = "ref     "
	prefix := m.controlMark("ref", m.focusTarget() == "ref") + label
	available := max(width-ansi.StringWidth(prefix), 1)
	return importRow{
		text:   prefix + formview.Input(m.ref, m.focus == 1, available, sanitize, cursorViewport),
		target: "ref",
		kind:   rowField,
	}
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

// sanitize strips terminal control sequences from untrusted forge text.
func sanitize(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, ansi.Strip(value))
}

func (m Model) reviewRows(width, height int) []importRow {
	styles := m.themeStyles()
	summary := fmt.Sprintf("fetched %d", m.preview.Fetched)
	if m.preview.Truncated {
		summary = fmt.Sprintf("fetched %d of about %d; results truncated", m.preview.Fetched, m.preview.TotalHint)
	}
	rows := []importRow{{text: summary, kind: rowHint}}
	if m.preview.Note != "" {
		rows = append(rows, importRow{text: "note  " + m.preview.Note, kind: rowHint})
	}
	rows = append(rows, importRow{}, importRow{text: "ISSUES", kind: rowSection})
	start, end := m.reviewWindow(reviewLimit(height))
	for index := start; index < end; index++ {
		rows = append(rows, m.issueRow(index, width))
		if m.rows[index].err != "" {
			rows = append(rows, importRow{text: "      " + m.rows[index].err, kind: rowError})
		}
	}
	if bar := m.progressRow(width); bar.text != "" {
		rows = append(rows, importRow{}, bar)
	}
	return append(rows, importRow{}, m.actionsRow(styles,
		importAction{target: "import", label: "Import", variant: theme.ButtonPrimary},
		importAction{target: "back", label: "Back"},
		importAction{target: "close", label: "Close"}))
}

// issueRow is one proposal. Spec section 5.1 assigns the checklist mark to the
// widget; the state suffix keeps the frozen vocabulary a duplicate is reported
// with.
func (m Model) issueRow(index, width int) importRow {
	styles := m.themeStyles()
	item := m.rows[index]
	target := "row:" + strconv.Itoa(index)
	cursor := m.controlMark(target, index == m.selection)
	state := ""
	switch {
	case item.created:
		state = " [created]"
	case item.draft.Duplicate != nil:
		state = fmt.Sprintf(" [duplicate via %s: %s]", item.draft.Duplicate.Via, item.draft.Duplicate.Title)
	}
	check := widget.CheckOpen
	switch {
	case item.created:
		check = widget.CheckDropped
	case item.include:
		check = widget.CheckDone
	}
	label := fit(item.draft.Title+state, max(width-ansi.StringWidth(cursor)-2, 1))
	return importRow{
		text:     cursor + checkGlyph(styles, check) + " " + label,
		rendered: styles.Overlay.Surf.Render(cursor) + widget.Check(styles, label, check, theme.OverlaySurf, index == m.selection),
		target:   target,
		kind:     rowField,
	}
}

// progressRow is the batch write indicator. Spec section 5.2 assigns bubbles'
// progress bar to this counter; the bar is drawn in full blocks so its cells
// carry a color rather than punching a hole in the panel tier.
func (m Model) progressRow(width int) importRow {
	label := m.progress()
	if label == "" {
		return importRow{}
	}
	styles := m.themeStyles()
	barWidth := max(min(width-ansi.StringWidth(label)-1, 24), 1)
	bar := progressBar(styles, barWidth).ViewAs(m.progressRatio())
	return importRow{
		text:     strings.Repeat(styles.Glyph.RailFull, barWidth) + " " + label,
		rendered: bar + styles.Overlay.FieldValue.Render(" "+label),
		kind:     rowBody,
	}
}

func (m Model) progressRatio() float64 {
	if len(m.queue) == 0 {
		return 0
	}
	return float64(min(m.queuePos+1, len(m.queue))) / float64(len(m.queue))
}

func (m Model) fieldRow(target, text string) importRow {
	return importRow{
		text:   m.controlMark(target, m.focusTarget() == target) + text,
		target: target,
		kind:   rowField,
	}
}

// importAction is one button of an action row: its target, its frozen label,
// and what it means (issue #157).
type importAction struct {
	target  string
	label   string
	variant theme.ButtonVariant
}

// actionsRow lays out the row's buttons and records where each one starts, so
// the pointer map keys a rect to the button rather than to a matched label.
func (m Model) actionsRow(styles *theme.Styles, actions ...importAction) importRow {
	const gap = "    "
	row := importRow{buttons: make([]importButton, 0, len(actions))}
	for index, action := range actions {
		if index > 0 {
			row.text += gap
			row.rendered += styles.Overlay.Surf.Render(gap)
		}
		// The row's actions are driven by focus and Enter rather than by a
		// single-rune key, so the resolver of spec section 10.4.2 marks no
		// hotkey here; it is called anyway so no surface resolves its own.
		label, underline := widget.Hotkey("[ "+action.label+" ]", nil)
		row.buttons = append(row.buttons, importButton{label: label, target: action.target, x0: ansi.StringWidth(row.text)})
		row.text += label
		row.rendered += widget.Button(styles, widget.ButtonOpts{
			Text:           label,
			Variant:        action.variant,
			Pressed:        m.pressed(action.target),
			UnderlineIndex: underline,
		})
	}
	return row
}

// footerLine is the footer band: the frozen hint ladder, or the status the
// overlay is reporting.
func (m Model) footerLine(width int) string {
	hints := "Tab fields  Left/Right change  Enter preview  Esc close"
	if m.stage == stageReview {
		hints = "Up/Down select  Space toggle  Enter import/retry  Esc back"
	}
	if m.status != "" {
		prefix := "status  "
		if m.statusError {
			prefix = "error   "
		}
		hints = prefix + m.status
	}
	return fit(hints, width)
}

// busyPrefix is the spinner frame of spec section 5.2: the fetch state that was
// static text carries the bubbles spinner instead.
func (m Model) busyPrefix() string {
	if len(m.spin.Spinner.Frames) == 0 {
		return ""
	}
	return ansi.Strip(m.spin.View())
}

// checkGlyph is the plain form of a checklist mark, for a row's text.
func checkGlyph(styles *theme.Styles, state widget.CheckState) string {
	switch state {
	case widget.CheckDone:
		return styles.Glyph.CheckOn
	case widget.CheckDropped:
		return styles.Glyph.CheckOff
	default:
		return styles.Glyph.Check
	}
}

func (m Model) controlMark(target string, active bool) string {
	if m.pressed(target) {
		return "! "
	}
	if active {
		return "> "
	}
	return "  "
}

// focusTarget names the field the keyboard focus index points at.
func (m Model) focusTarget() string {
	switch m.focus {
	case 0:
		return "source"
	case 1:
		return "ref"
	default:
		return "max"
	}
}

func (m Model) pressed(target string) bool { return m.pointerState.IsPressed(controlID(target)) }

func rowWindow(count, selection, limit int) (int, int) {
	if count <= limit {
		return 0, count
	}
	start := max(0, selection-limit/2)
	start = min(start, count-limit)
	return start, start + limit
}

// reviewLimit is how many issue rows the review stage shows in a panel body of
// this height. The body spends 8 rows on the summary, the ISSUES break, the
// progress bar and the actions row; the rest is issues, capped at 12.
func reviewLimit(bodyHeight int) int { return max(1, min(bodyHeight-8, 12)) }

func (m Model) reviewWindow(limit int) (int, int) {
	count := len(m.rows)
	if !m.manualScroll {
		return rowWindow(count, m.selection, limit)
	}
	if count <= limit {
		return 0, count
	}
	start := min(max(m.scroll, 0), count-limit)
	return start, start + limit
}

func fit(value string, width int) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value)
	if ansi.StringWidth(value) <= width {
		return value
	}
	if width <= 1 {
		return ansi.Truncate(value, width, "")
	}
	return ansi.Truncate(value, width-1, "") + "…"
}

// fitBlock keeps a composed frame inside the cell grid it was composed for.
func fitBlock(block string, width, height int) string {
	lines := strings.Split(block, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for index := range lines {
		lines[index] = ansi.Truncate(lines[index], max(width, 0), "")
	}
	return strings.Join(lines, "\n")
}
