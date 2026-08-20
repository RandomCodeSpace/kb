package tui

import (
	"strings"
	"unicode/utf8"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/pointer"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
	"github.com/RandomCodeSpace/kb/internal/tui/widget"
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
)

type settingsRenderRow struct {
	line   string
	button string
	target string
	armed  bool
	kind   settingsRowKind
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
		body = append(body, settingsRenderRow{line: "loading settings...", kind: settingsRowHint})
	} else {
		body = append(body,
			m.inputModelRow("ai:base", "Base URL", m.aiBase, false, inner),
			m.inputModelRow("ai:model", "Model", m.aiModel, false, inner),
			m.inputModelRow("ai:key", keyLabel("API key", m.hasKey), m.aiKey, true, inner),
			m.actionRow("ai:test", "Test connection", inner),
			m.actionRow("ai:save", "Save AI settings", inner),
			settingsRenderRow{line: ""},
			settingsRenderRow{line: "FORGE INTEGRATIONS", kind: settingsRowSection},
		)
		if len(m.rows) == 0 {
			body = append(body, settingsRenderRow{line: "(none configured)", kind: settingsRowHint})
		}
		for i := range m.rows {
			body = append(body, m.renderForgeRow(&m.rows[i], inner)...)
		}
		body = append(body, m.actionRow("forge:add", "+ Add integration", inner))
	}
	status := m.status
	if status == "" {
		status = "ready"
	}
	if m.statusIsError {
		status = "error: " + status
	}
	footer := settingsFit("[Close] | "+status+" | tab navigate | enter act", inner)
	footer = strings.Replace(footer, "[Close]", m.pointerState.Render(styles, settingsControlID("close"), "[Close]"), 1)

	bodyHeight := max(frame.height-2, 1)
	focusLine := -1
	for i, row := range body {
		if strings.HasPrefix(row.line, ">") {
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

// renderSettingsRow applies the token the row's role names.
func (m *settingsModel) renderSettingsRow(row settingsRenderRow, width int) string {
	styles := m.themeStyles()
	line := settingsFit(row.line, width)
	if row.kind == settingsRowButton {
		if label := settingsFit(row.button, width); strings.HasSuffix(line, label) {
			marker := strings.TrimSuffix(line, label)
			return styles.Overlay.Surf.Render(marker) + widget.Button(styles, widget.ButtonOpts{
				Text:           label,
				Selected:       m.focus == row.target,
				Armed:          row.armed,
				Pressed:        m.pointerState.IsPressed(settingsControlID(row.target)),
				UnderlineIndex: -1,
			})
		}
	}
	if row.target != "" {
		line = m.pointerState.Render(styles, settingsControlID(row.target), line)
	}
	switch row.kind {
	case settingsRowHint:
		return styles.Overlay.FieldLabel.Render(line)
	case settingsRowField:
		if m.focus == row.target {
			return styles.OnBold(theme.FgBase, theme.OverlaySurf).Render(line)
		}
		return styles.Overlay.FieldValue.Render(line)
	default:
		return styles.Overlay.Surf.Render(line)
	}
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
			settingsRenderRow{line: settingsFit("  Name: "+row.name.Value()+" (locked)", width), kind: settingsRowHint},
			settingsRenderRow{line: settingsFit("  Kind: "+row.kind+" (locked)", width), kind: settingsRowHint},
		)
	} else {
		lines = append(lines,
			m.inputRow(prefix+"kind", "Kind", row.kind, width),
			m.inputModelRow(prefix+"name", "Name", row.name, false, width),
		)
	}
	lines = append(lines,
		m.inputModelRow(prefix+"base", "Base URL", row.baseURL, false, width),
		m.inputModelRow(prefix+"project", "Project", row.project, false, width),
		m.inputModelRow(prefix+"token", keyLabel("Token", row.hasToken), row.token, true, width),
		m.actionRow(prefix+"test", "Test", width),
	)
	remove := "Remove"
	if m.armedRemove == row.id {
		remove = "Confirm remove"
	}
	removeRow := m.actionRow(prefix+"remove", remove, width)
	removeRow.armed = m.armedRemove == row.id
	return append(lines, m.actionRow(prefix+"save", "Save", width), removeRow)
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

func (m *settingsModel) inputRow(target, label, value string, width int) settingsRenderRow {
	marker := "  "
	if m.focus == target {
		marker = "> "
	}
	return settingsRenderRow{
		line:   settingsFit(marker+label+": "+value, width),
		target: target,
		kind:   settingsRowField,
	}
}

func (m *settingsModel) inputModelRow(
	target, label string,
	input textinput.Model,
	secret bool,
	width int,
) settingsRenderRow {
	marker := "  "
	if m.focus == target {
		marker = "> "
	}
	prefix := marker + label + ": "
	available := max(width-ansi.StringWidth(prefix), 1)
	value := settingsInputDisplay(input, secret, m.focus == target, available)
	return settingsRenderRow{
		line:   settingsFit(prefix+value, width),
		target: target,
		kind:   settingsRowField,
	}
}

func (m *settingsModel) actionRow(target, label string, width int) settingsRenderRow {
	marker := "  "
	if m.focus == target {
		marker = "> "
	}
	button := "[" + label + "]"
	return settingsRenderRow{
		line:   settingsFit(marker+button, width),
		button: button,
		target: target,
		kind:   settingsRowButton,
	}
}

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

func isSettingsMessage(message tea.Msg) bool {
	if pointer.IsMessage(message) {
		return true
	}
	switch message.(type) {
	case settingsLoadedMsg, aiSettingsTestedMsg, aiSettingsSavedMsg,
		forgeSettingsTestedMsg, forgeSettingsSavedMsg, forgeSettingsRemovedMsg,
		settingsPointerMsg, settingsWheelMsg:
		return true
	default:
		return false
	}
}
