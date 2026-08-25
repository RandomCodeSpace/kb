package tui

import (
	"strings"
	"unicode/utf8"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/formview"
	"github.com/RandomCodeSpace/kb/internal/tui/pointer"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
	"github.com/RandomCodeSpace/kb/internal/tui/widget"
	"github.com/RandomCodeSpace/kb/internal/tui/widget/spin"
)

// settingsRowKind is the semantic role of one settings row, which decides the
// token it renders with. Spec section 6.2: the view takes a *theme.Styles and
// never builds one.
type settingsRowKind uint8

const (
	settingsRowBody settingsRowKind = iota
	settingsRowField
	settingsRowSection
	settingsRowButton
	settingsRowHint
	settingsRowWidget // a row one of the section 10.8 widgets rendered whole
)

// settingsRenderRow is one rendered pane row. A row with a label is a table
// row: its two cells are laid out by lipgloss/v2 table against every other
// label/value row in the pane, so the values share one column instead of each
// starting wherever its own label ended. Everything else - sections, buttons,
// blanks - carries its line directly.
//
// A field whose value is a live text input resolves late: the value column's
// width is only known once every label in the pane has been seen, and the input
// display has to be cut to it.
type settingsRenderRow struct {
	line     string
	rendered string
	label    string
	value    string
	input    *textinput.Model
	secret   bool
	button   string
	target   string
	variant  theme.ButtonVariant
	armed    bool
	kind     settingsRowKind
}

func settingsControlID(target string) pointer.ControlID {
	return pointer.ControlID("settings:" + target)
}

func (m *settingsModel) View(width, height int) string {
	return m.Surface("", width, height).Content
}

// settingsFrame is the resolved panel geometry of spec section 4: a share of
// the frame, centered, or the whole frame when the terminal is too small for a
// panel to leave a usable backdrop.
type settingsFrame struct {
	x, y     int
	width    int
	height   int
	inset    int
	inner    int
	elevated bool
}

func settingsLayout(metrics theme.Metrics, width, height int) settingsFrame {
	paneWidth, paneHeight := metrics.OverlayPane(width, height)
	elevated := metrics.OverlayElevated(paneWidth, paneHeight)
	if !elevated {
		paneWidth, paneHeight = width, height
	}
	inset := min(metrics.OverlayInsetX, paneWidth/2)
	return settingsFrame{
		x:        max((width-paneWidth)/2, 0),
		y:        max((height-paneHeight)/2, 0),
		width:    paneWidth,
		height:   paneHeight,
		inset:    inset,
		inner:    max(min(paneWidth-2*inset, metrics.Overlay.ContentMax), 1),
		elevated: elevated,
	}
}

func (m *settingsModel) Surface(background string, width, height int) pointer.Surface {
	width = max(width, 1)
	height = max(height, 3)
	styles := m.themeStyles()
	frame := settingsLayout(styles.Metrics, width, height)
	inset, inner := frame.inset, frame.inner
	inputWidth := max(inner-18, 8)
	m.aiBase.SetWidth(inputWidth)
	m.aiModel.SetWidth(inputWidth)
	m.aiKey.SetWidth(inputWidth)
	for i := range m.rows {
		m.rows[i].name.SetWidth(inputWidth)
		m.rows[i].baseURL.SetWidth(inputWidth)
		m.rows[i].project.SetWidth(inputWidth)
		m.rows[i].token.SetWidth(inputWidth)
	}

	body := []settingsRenderRow{{line: ""}, {line: "AI SETTINGS", kind: settingsRowSection}}
	if !m.loaded {
		// Spec section 10.8.7: the AI block's first read is a plain-tier busy
		// row under its own band, not a static line and not the footer. A read
		// that failed is no longer loading, so the error takes the row instead -
		// the pane has no action row to pin it above yet.
		if m.statusIsError {
			body = append(body, m.errorRows(inner)...)
		} else {
			body = append(body, m.busyRow(loadSettingsLabel, inner))
		}
	} else {
		body = append(body,
			m.inputModelRow("ai:base", "Base URL", &m.aiBase, false),
			m.inputModelRow("ai:model", "Model", &m.aiModel, false),
			m.inputModelRow("ai:key", keyLabel("API key", m.hasKey), &m.aiKey, true),
			m.actionRow("ai:test", "Test connection", theme.ButtonNeutral, inner),
			m.actionRow("ai:save", "Save AI settings", theme.ButtonPrimary, inner),
			settingsRenderRow{line: ""},
			settingsRenderRow{line: "FORGE INTEGRATIONS", kind: settingsRowSection},
		)
		if len(m.rows) == 0 {
			body = append(body, m.emptyRow("no integrations", "+ Add integration", inner))
		}
		for i := range m.rows {
			body = append(body, m.renderForgeRow(&m.rows[i], inner)...)
		}
		body = append(body, m.actionRow("forge:add", "+ Add integration", theme.ButtonNeutral, inner))
	}
	if m.loaded && m.statusIsError {
		// Ratified call 12: an error leaves the band and lands in a body row
		// pinned directly above the action row, so the failure and the control
		// that will retry it are adjacent.
		body = append(body, settingsRenderRow{line: ""})
		body = append(body, m.errorRows(inner)...)
	}
	m.layoutSettingsTable(body, inner)
	footer := m.footerLine(inner)

	bodyHeight := max(frame.height-2, 1)
	focusLine := -1
	for i, row := range body {
		// The focus marker used to be a glyph in the row's own text; the gutter
		// of spec section 10.4.3 reserves the same cells in every state, so the
		// focused row is found structurally instead.
		if row.target != "" && row.target == m.focus && row.kind != settingsRowSection {
			focusLine = i
			break
		}
	}
	maxScroll := max(len(body)-bodyHeight, 0)
	if focusLine >= 0 && focusLine < m.scroll {
		m.scroll = focusLine
	}
	if focusLine >= m.scroll+bodyHeight {
		m.scroll = focusLine - bodyHeight + 1
	}
	m.scroll = min(max(m.scroll, 0), maxScroll)
	end := min(m.scroll+bodyHeight, len(body))
	visible := body[m.scroll:end]
	var hitMap pointer.Map
	viewport := pointer.Viewport{
		Rect:   pointer.Rect{X0: frame.x, Y0: frame.y + 1, X1: frame.x + frame.width, Y1: frame.y + frame.height - 1},
		Scroll: m.scroll,
	}
	for logicalRow, row := range body {
		if row.target == "" {
			continue
		}
		if rect, ok := viewport.Row(logicalRow, 0, frame.width); ok {
			target := row.target
			hitMap.AddControl(settingsControlID(target), rect, func(pointer.Point) tea.Msg { return settingsPointerMsg{owner: m, target: target} })
		}
	}
	rendered := make([]string, 0, bodyHeight)
	for _, row := range visible {
		if row.kind == settingsRowSection {
			rendered = append(rendered, widget.Section(styles, settingsFit(row.line, inner), "", frame.width))
			continue
		}
		rendered = append(rendered, widget.OverlayRow(styles, m.renderSettingsRow(row, inner), frame.width))
	}
	opts := widget.OverlayOpts{
		Title:  sanitizeTerminal("kb / settings / " + m.user),
		Body:   rendered,
		Footer: footer,
		Hint:   settingsScrollHint(styles, m.scroll+bodyHeight, len(body)),
		Width:  frame.width,
		Height: frame.height,
	}
	content := settingsCompose(styles, opts, background, frame, width, height)

	footerY := frame.y + frame.height - 1
	hitMap.AddWheel(viewport.Rect, func(delta int) tea.Msg { return settingsWheelMsg{delta: delta} })
	hitMap.AddControl(
		settingsControlID("close"),
		pointer.Rect{X0: frame.x + inset, Y0: footerY, X1: min(frame.x+frame.width, frame.x+inset+7), Y1: footerY + 1},
		func(pointer.Point) tea.Msg { return settingsPointerMsg{owner: m, target: "close"} },
	)
	// Rows 6 and 9 of spec section 10.5.2: the pointer can stand still while the
	// content moves under it, so a wheel scroll or a resize re-resolves hover
	// from the retained point against the map this render just built. A point
	// that no longer lands on a region clears hover and mouse mode with it.
	m.pointerState = m.pointerState.Reresolve(hitMap)
	return pointer.Surface{Content: content, Pointer: hitMap.Handler()}
}

// settingsCompose elevates the panel over the dimmed board. Spec section 4: an
// overlay is a shade step plus a shadow over what is behind it. A frame too
// small for a panel keeps the v1.0.1 full-frame pane and casts no shadow.
func settingsCompose(styles *theme.Styles, opts widget.OverlayOpts, background string, frame settingsFrame, width, height int) string {
	panel := widget.Overlay(styles, opts)
	if !frame.elevated {
		return panel
	}
	if background == "" {
		// No board behind it: Place centers the panel on the same coordinates
		// settingsLayout resolved, which is what the hit regions were built for.
		return fitActionFrame(lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, panel), width, height)
	}
	layers := append(
		[]*lipgloss.Layer{lipgloss.NewLayer(fitActionFrame(background, width, height))},
		widget.OverlayLayers(styles, opts, frame.x, frame.y)...,
	)
	return fitActionFrame(lipgloss.NewCompositor(layers...).Render(), width, height)
}

// footerLine is the footer band. Spec section 10.8.4 rule 1: a busy pane
// replaces the head of its hint ladder with the busy line and the hints that
// are still live survive as the ladder's tail, so the band is the only row
// whose content changes while an operation runs.
//
// It never carries an error. Ratified call 12: neither Danger slot clears the
// contrast floor on OverlayBand, so a failure is reported in a body row.
func (m *settingsModel) footerLine(width int) string {
	styles := m.themeStyles()
	head := "[Close]"
	if label := m.busyLabel(); label != "" && m.loaded {
		head = m.busyBand(label, width)
	}
	tail := " | tab navigate | enter act"
	if m.statusIsError {
		tail = " | esc close"
	} else if m.status != "" {
		tail = " | " + sanitizeTerminal(m.status) + tail
	}
	line := head + settingsFit(tail, max(width-ansi.StringWidth(head), 0))
	if strings.HasPrefix(line, "[Close]") {
		line = strings.Replace(line, "[Close]", m.pointerState.Render(styles, settingsControlID("close"), "[Close]"), 1)
	}
	return line
}

// busyBand is the band's busy head. The branded engine is frame and label in
// one run, so it is laid in whole; the plain tier composes frame, BusyGap and
// label through the widget.
func (m *settingsModel) busyBand(label string, width int) string {
	if m.brandBusy() {
		if row := m.brand.View(); row != "" {
			return row
		}
	}
	frame := ""
	if !m.brandBusy() {
		frame = m.plainFrame()
	}
	return widget.Busy(m.themeStyles(), widget.BusyOpts{
		Frame: frame, Label: label, On: theme.OverlayBand, Width: width,
	})
}

// busyRow is a section's own busy row, for content that is still arriving while
// the footer is describing the panel as a whole (spec section 10.8.4 rule 2).
func (m *settingsModel) busyRow(label string, width int) settingsRenderRow {
	row := widget.Busy(m.themeStyles(), widget.BusyOpts{
		Frame: m.plainFrame(), Label: label, On: theme.OverlaySurf, Width: width,
	})
	return settingsRenderRow{line: ansi.Strip(row), rendered: row, kind: settingsRowWidget}
}

// emptyRow is the empty state of spec section 10.8.3. The tail is the button's
// own label, because the pane's action row owns the action.
func (m *settingsModel) emptyRow(headline, key string, width int) settingsRenderRow {
	row := widget.Empty(m.themeStyles(), widget.EmptyOpts{
		Headline: headline, Key: key, On: theme.OverlaySurf, Width: width,
	})
	return settingsRenderRow{line: ansi.Strip(row), rendered: row, kind: settingsRowWidget}
}

// errorRows is the error block of spec section 10.8.5, one pane row per
// rendered line, empty while the pane has nothing to report.
func (m *settingsModel) errorRows(width int) []settingsRenderRow {
	if !m.statusIsError || m.status == "" {
		return nil
	}
	styles := m.themeStyles()
	block := widget.Error(styles, widget.ErrorOpts{
		Message:  sanitizeTerminal(m.status),
		Key:      m.statusTail,
		On:       theme.OverlaySurf,
		Width:    width,
		MaxLines: styles.Metrics.ErrorMaxLines,
	})
	rows := make([]settingsRenderRow, 0, len(block))
	for _, line := range block {
		rows = append(rows, settingsRenderRow{line: ansi.Strip(line), rendered: line, kind: settingsRowWidget})
	}
	return rows
}

// renderSettingsRow applies the token the row's role names.
//
// A row that carries a control spends its first FocusGutterW + FocusGutterGap
// cells on the focus gutter of spec section 10.4.3. The reserve is in the row's
// plain text in every state, so the table's column arithmetic is the same
// whichever row has the keyboard and focus never reflows the pane.
func (m *settingsModel) renderSettingsRow(row settingsRenderRow, width int) string {
	styles := m.themeStyles()
	if row.rendered != "" {
		return ansi.Truncate(row.rendered, max(width, 0), "")
	}
	line := settingsFit(row.line, width)
	if row.target == "" {
		return m.renderSettingsContent(row, line, theme.OverlaySurf)
	}
	on := theme.OverlaySurf
	if row.kind == settingsRowField {
		// Spec section 10.5.1: a settings key/value row is activatable, so
		// hover raises the whole row by one tier.
		on = styles.RowSurface(m.hovered(row.target))
	}
	gutter := widget.Gutter(styles, m.focus == row.target, theme.Brand, on)
	content := m.renderSettingsContent(row, trimGutter(styles, line), on)
	if row.kind != settingsRowButton {
		// The button widget owns its own pressed token; every other row takes
		// the attribute here, which costs no cells (spec section 10.4.4).
		content = m.pointerState.Render(styles, settingsControlID(row.target), content)
	}
	return gutter + content
}

// renderSettingsContent renders the cells to the right of a row's gutter.
func (m *settingsModel) renderSettingsContent(row settingsRenderRow, line string, on theme.Slot) string {
	styles := m.themeStyles()
	switch row.kind {
	case settingsRowButton:
		// A settings button is driven by focus and Enter, so the resolver of
		// spec section 10.4.2 marks no hotkey on it; it is called anyway so no
		// surface resolves its own.
		label := settingsFit(row.button, max(ansi.StringWidth(line)-2*styles.Metrics.ButtonPadX, 0))
		if label == "" {
			return styles.Overlay.Surf.Render(line)
		}
		text, underline := widget.Hotkey(label, nil)
		return widget.Button(styles, widget.ButtonOpts{
			Text:           text,
			Variant:        row.variant,
			Selected:       m.focus == row.target,
			Hovered:        m.hovered(row.target),
			Armed:          row.armed,
			Pressed:        m.pointerState.IsPressed(settingsControlID(row.target)),
			UnderlineIndex: underline,
			Padding:        [2]int{styles.Metrics.ButtonPadX, styles.Metrics.ButtonPadX},
		})
	case settingsRowHint:
		return styles.Overlay.FieldLabel.Render(line)
	case settingsRowField:
		if m.focus == row.target {
			return formview.Selection(
				styles.OnBold(theme.FgBase, on),
				m.mark.Active(row.target),
			).Render(line)
		}
		// Hover raises the tier and never re-hues (spec section 10.5.1), so the
		// pair is Overlay.FieldValue's own foreground on whichever surface the
		// row resolved to.
		return styles.On(theme.FgBase, on).Render(line)
	default:
		return styles.On(theme.FgBase, on).Render(line)
	}
}

// hovered reports whether the pointer is over this row's control.
func (m *settingsModel) hovered(target string) bool {
	return target != "" && m.pointerState.IsHovered(settingsControlID(target))
}

// trimGutter drops the gutter reserve a row carries in its plain text, which
// renderSettingsRow replaces with the styled bar.
func trimGutter(styles *theme.Styles, line string) string {
	metrics := styles.Metrics
	reserve := max(metrics.FocusGutterW, 0) + max(metrics.FocusGutterGap, 0)
	if ansi.StringWidth(line) <= reserve {
		return ""
	}
	return ansi.Cut(line, reserve, ansi.StringWidth(line))
}

func (m *settingsModel) renderForgeRow(row *integrationSettingsRow, width int) []settingsRenderRow {
	prefix := "forge:" + row.id + ":"
	marker := "new"
	if row.persisted {
		marker = "saved"
	}
	rowTarget := prefix + "kind"
	if row.persisted {
		rowTarget = prefix + "base"
	}
	lines := []settingsRenderRow{
		{line: ""},
		{line: settingsFit(row.name.Value()+" ("+marker+")", width), target: rowTarget, kind: settingsRowSection},
	}
	if row.persisted {
		lines = append(lines,
			m.lockedRow("Name", row.name.Value()),
			m.lockedRow("Kind", row.kind),
		)
	} else {
		lines = append(lines,
			m.inputRow(prefix+"kind", "Kind", row.kind),
			m.inputModelRow(prefix+"name", "Name", &row.name, false),
		)
	}
	lines = append(lines,
		m.inputModelRow(prefix+"base", "Base URL", &row.baseURL, false),
		m.inputModelRow(prefix+"project", "Project", &row.project, false),
		m.inputModelRow(prefix+"token", keyLabel("Token", row.hasToken), &row.token, true),
		m.actionRow(prefix+"test", "Test", theme.ButtonNeutral, width),
	)
	remove := "Remove"
	if m.armedRemove == row.id {
		remove = "Confirm remove"
	}
	removeRow := m.actionRow(prefix+"remove", remove, theme.ButtonDanger, width)
	removeRow.armed = m.armedRemove == row.id
	return append(lines, m.actionRow(prefix+"save", "Save", theme.ButtonPrimary, width), removeRow)
}

func settingsInputDisplay(input textinput.Model, secret, focused bool, width int) string {
	value := input.Value()
	if value == "" {
		value = input.Placeholder
	}
	raw := []rune(value)
	position := min(max(input.Position(), 0), len(raw))
	safe := sanitizeTerminal(value)
	safePosition := utf8.RuneCountInString(sanitizeTerminal(string(raw[:position])))
	safePosition = min(safePosition, utf8.RuneCountInString(safe))
	if secret && input.Value() != "" {
		safe = strings.Repeat("*", max(utf8.RuneCountInString(safe), 1))
		safePosition = min(safePosition, utf8.RuneCountInString(safe))
	}
	if !focused {
		return ansi.Truncate(safe, max(width, 0), "")
	}
	return settingsCursorViewport(safe, safePosition, width)
}

func settingsCursorViewport(value string, position, width int) string {
	if width <= 0 {
		return ""
	}
	const cursor = "|"
	if width == 1 {
		return cursor
	}
	runes := []rune(value)
	position = min(max(position, 0), len(runes))
	before, after := string(runes[:position]), string(runes[position:])
	contentWidth := width - 1
	cursorColumn := ansi.StringWidth(before)
	left := max(cursorColumn-contentWidth, 0)
	visibleBefore := ansi.Cut(before, left, cursorColumn)
	remaining := max(contentWidth-ansi.StringWidth(visibleBefore), 0)
	visibleAfter := ansi.Truncate(after, remaining, "")
	return visibleBefore + cursor + visibleAfter
}

func keyLabel(label string, saved bool) string {
	if saved {
		return label + " (saved)"
	}
	return label
}

// settingsLabelCell is the label column of a table row: the focus marker, the
// label and its colon. The colon stays on the label so the table's own padding
// falls between the two columns rather than inside the label.
func (m *settingsModel) settingsLabelCell(target, label string) string {
	return settingsGutterReserve(m.themeStyles()) + label + ":"
}

// settingsGutterReserve is the focus gutter's cells, in plain text. It is the
// same count in every state (spec section 10.4.4), so the table lays a focused
// row and a blurred one out identically and the styled bar of spec section
// 10.4.3 drops straight onto it at render time.
func settingsGutterReserve(styles *theme.Styles) string {
	metrics := styles.Metrics
	return strings.Repeat(" ", max(metrics.FocusGutterW, 0)+max(metrics.FocusGutterGap, 0))
}

func (m *settingsModel) inputRow(target, label, value string) settingsRenderRow {
	return settingsRenderRow{
		label:  m.settingsLabelCell(target, label),
		value:  value,
		target: target,
		kind:   settingsRowField,
	}
}

func (m *settingsModel) inputModelRow(
	target, label string,
	input *textinput.Model,
	secret bool,
) settingsRenderRow {
	return settingsRenderRow{
		label:  m.settingsLabelCell(target, label),
		input:  input,
		secret: secret,
		target: target,
		kind:   settingsRowField,
	}
}

// lockedRow is a persisted forge field the pane shows but does not edit.
func (m *settingsModel) lockedRow(label, value string) settingsRenderRow {
	return settingsRenderRow{
		label: "  " + label + ":",
		value: value + " (locked)",
		kind:  settingsRowHint,
	}
}

// layoutSettingsTable resolves every label/value row in the pane through one
// lipgloss table. Rows keep their index, so the pointer hit regions keyed to
// logical rows are untouched by the adoption.
func (m *settingsModel) layoutSettingsTable(body []settingsRenderRow, width int) {
	styles := m.themeStyles()
	labelWidth, indices := 0, make([]int, 0, len(body))
	for index := range body {
		if body[index].label == "" {
			continue
		}
		labelWidth = max(labelWidth, ansi.StringWidth(body[index].label))
		indices = append(indices, index)
	}
	if len(indices) == 0 {
		return
	}
	valueWidth := max(width-labelWidth-styles.Metrics.TableGutter, 1)
	cells := make([][]string, 0, len(indices))
	for _, index := range indices {
		row := &body[index]
		if row.input != nil {
			row.value = settingsInputDisplay(*row.input, row.secret, m.focus == row.target, valueWidth)
		}
		cells = append(cells, []string{row.label, settingsFit(row.value, valueWidth)})
	}
	for position, line := range widget.Table(styles, cells) {
		body[indices[position]].line = settingsFit(line, width)
	}
}

func (m *settingsModel) actionRow(target, label string, variant theme.ButtonVariant, width int) settingsRenderRow {
	return settingsRenderRow{
		line:    settingsFit(settingsGutterReserve(m.themeStyles())+settingsButtonPad+label+settingsButtonPad, width),
		button:  label,
		target:  target,
		variant: variant,
		kind:    settingsRowButton,
	}
}

// settingsButtonPad is one cell of filled surface on each side of a button
// label, the crush ButtonOpts look of spec section 5.1 (issue #152).
const settingsButtonPad = " "

// settingsScrollHint is the section 5.1 scroll indicator, shown only while the
// pane has more rows than the window.
func settingsScrollHint(styles *theme.Styles, shown, total int) string {
	if total <= 0 || shown >= total {
		return ""
	}
	return widget.ScrollHint(styles, shown, total, theme.OverlayBand)
}

func settingsFit(line string, width int) string {
	return ansi.Truncate(sanitizeTerminal(line), max(width, 0), "")
}

func sanitizeTerminal(value string) string {
	value = ansi.Strip(value)
	return strings.Map(func(r rune) rune {
		if r <= 0x1f || (r >= 0x7f && r <= 0x9f) {
			return -1
		}
		return r
	}, value)
}

// sanitizeTerminalText is sanitizeTerminal for a value whose line structure is
// content rather than an accident of storage. Issue #241: a description's
// newlines are part of what the author wrote, and the frozen grammar of spec
// section 3.3 reads them - every source line is its own block, on the card and
// in the detail pane alike. Stripping them as control characters joined the
// author's separate lines into one paragraph on the card while the pane, which
// never ran them through this, kept them: a fork of the product contract over
// the same bytes.
//
// Every other control character still goes, because a description is drawn into
// a composed frame and an escape or a bare carriage return in it would move the
// cursor off the cell the grid gave it. A CRLF source collapses to the newline
// the grammar splits on rather than losing the break with its carriage return.
func sanitizeTerminalText(value string) string {
	value = strings.ReplaceAll(ansi.Strip(value), "\r\n", "\n")
	return strings.Map(func(r rune) rune {
		if r == '\n' {
			return r
		}
		if r <= 0x1f || (r >= 0x7f && r <= 0x9f) {
			return -1
		}
		return r
	}, value)
}

func isSettingsMessage(message tea.Msg) bool {
	if pointer.IsMessage(message) {
		return true
	}
	switch message.(type) {
	case settingsLoadedMsg, aiSettingsTestedMsg, aiSettingsSavedMsg,
		forgeSettingsTestedMsg, forgeSettingsSavedMsg, forgeSettingsRemovedMsg,
		settingsPointerMsg, settingsWheelMsg, spinner.TickMsg, spin.StepMsg:
		return true
	default:
		return false
	}
}
