package cardeditor

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
	"github.com/RandomCodeSpace/kb/internal/tui/formview"
	"github.com/RandomCodeSpace/kb/internal/tui/pointer"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
	"github.com/RandomCodeSpace/kb/internal/tui/widget"
)

// rowKind is the semantic role of one editor body row. It decides which token
// the row is rendered with; no view code composes a style of its own.
type rowKind uint8

const (
	rowBody    rowKind = iota // plain content on the overlay panel tier
	rowField                  // a labelled control row
	rowSection                // a section break band
	rowButton                 // an action, rendered by the button widget
	rowHint                   // secondary text
	rowError                  // an error line
)

// editorRow is one logical body row. The target is the symbolic pointer
// control the row activates, carried structurally instead of recovered by
// matching the rendered text: card titles, descriptions and checklist text are
// untrusted, and text matching let them impersonate a control.
type editorRow struct {
	text   string
	button string
	target string
	kind   rowKind
}

// plain is the row as unstyled text, the form the pointer geometry and the
// control-safety tests read.
func (r editorRow) plain() string { return r.text }

// editorFrame is the resolved geometry of one render: where the panel sits,
// how much body fits, and which logical rows the window shows.
type editorFrame struct {
	x, y       int
	width      int
	height     int
	inner      int
	bodyHeight int
	rows       []editorRow
	scroll     int
}

type pointerHit struct {
	x0, x1 int
	y0, y1 int
	target string
}

// MouseHandler maps clicks in the rendered editor to symbolic control targets.
// The root model can install this callback while the editor owns the screen;
// the resulting message is routed through Update just like a key press.
func (m Model) MouseHandler(width, height int) func(tea.MouseMsg) tea.Cmd {
	if !m.open {
		return nil
	}
	session := m.session
	var hitMap pointer.Map
	for _, hit := range m.pointerHits(width, height) {
		target := hit.target
		hitMap.AddControl(pointer.ControlID(target), pointer.Rect{X0: hit.x0, Y0: hit.y0, X1: hit.x1, Y1: hit.y1}, func(pointer.Point) tea.Msg {
			return pointerClickMsg{session: session, target: target}
		})
	}
	frame := m.layout(width, height)
	maxScroll := max(len(frame.rows)-frame.bodyHeight, 0)
	hitMap.AddWheel(pointer.Rect{X0: frame.x, Y0: frame.y, X1: frame.x + frame.width, Y1: frame.y + frame.height}, func(delta int) tea.Msg {
		return pointerWheelMsg{session: session, delta: delta, maxScroll: maxScroll}
	})
	return hitMap.Handler()
}

func (m Model) pointerHits(width, height int) []pointerHit {
	frame := m.layout(width, height)
	hits := make([]pointerHit, 0, len(frame.rows))
	for index, row := range frame.rows {
		if row.target == "" {
			continue
		}
		y := frame.y + 1 + index - frame.scroll
		if y < frame.y+1 || y >= frame.y+1+frame.bodyHeight {
			continue
		}
		hits = append(hits, pointerHit{
			x0: frame.x + 1, x1: frame.x + frame.width - 1,
			y0: y, y1: y + 1,
			target: row.target,
		})
	}
	if m.guardClose {
		footerY := frame.y + frame.height - 1
		footerX := frame.x + m.themeStyles().Metrics.OverlayInsetX
		for _, target := range []struct {
			label  string
			target string
		}{
			{label: "[Discard]", target: "discard"},
			{label: "[Keep editing]", target: "keep"},
		} {
			start := strings.Index(ansi.Strip(m.footerLine(frame.inner)), target.label)
			if start < 0 {
				continue
			}
			hits = append(hits, pointerHit{
				x0: footerX + start, x1: footerX + start + ansi.StringWidth(target.label),
				y0: footerY, y1: footerY + 1, target: target.target,
			})
		}
	}
	return hits
}

// View renders the editor pane centered on the terminal. The panel carries no
// shadow here: a shadow needs something to fall on, and this path has no board
// behind it.
func (m *Model) View(width, height int) string {
	if !m.open {
		return ""
	}
	width, height = max(width, 1), max(height, 1)
	frame := m.layout(width, height)
	panel := fitTerminal(widget.Overlay(m.themeStyles(), m.panelOpts(frame)), width, height)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, panel)
}

// Overlay composes the editor over the board/detail surface. Spec section 4:
// the panel is an elevation over what is behind it, never a frame, so it takes
// the shade step and the shadow with it.
func (m *Model) Overlay(background string, width, height int) string {
	if !m.open {
		return background
	}
	width, height = max(width, 1), max(height, 1)
	background = fitTerminal(background, width, height)
	frame := m.layout(width, height)
	layers := append(
		[]*lipgloss.Layer{lipgloss.NewLayer(background)},
		widget.OverlayLayers(m.themeStyles(), m.panelOpts(frame), frame.x, frame.y)...,
	)
	return fitTerminal(lipgloss.NewCompositor(layers...).Render(), width, height)
}

func (m *Model) panelOpts(frame editorFrame) widget.OverlayOpts {
	return widget.OverlayOpts{
		Title:  m.headerTitle(),
		Seq:    m.sequenceTag(),
		Body:   m.visibleRows(frame),
		Footer: m.footerLine(frame.inner),
		Hint:   m.scrollHint(frame),
		Width:  frame.width,
		Height: frame.height,
	}
}

// scrollHint is the section 5.1 scroll indicator, shown only while the body
// does not fit the panel.
func (m *Model) scrollHint(frame editorFrame) string {
	if len(frame.rows) <= frame.bodyHeight {
		return ""
	}
	return widget.ScrollHint(m.themeStyles(), frame.scroll+frame.bodyHeight, len(frame.rows), theme.OverlayBand)
}

// fitTerminal keeps a composed frame inside the cell grid it was composed for.
func fitTerminal(rendered string, width, height int) string {
	lines := strings.Split(rendered, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for index := range lines {
		lines[index] = ansi.Truncate(lines[index], width, "")
	}
	return strings.Join(lines, "\n")
}

// layout resolves the panel geometry and scrolls the focused row into view.
func (m *Model) layout(width, height int) editorFrame {
	width, height = max(width, 1), max(height, 1)
	metrics := m.themeStyles().Metrics
	paneWidth := min(max(width-4, 18), metrics.Overlay.Editor, width)
	paneHeight := min(max(height-2, 7), height)
	inner := max(paneWidth-2*metrics.OverlayInsetX, 1)
	bodyHeight := max(paneHeight-2, 1)

	rows := m.bodyRows(inner)
	focusLine := 0
	for index, row := range rows {
		if row.target != "" && row.target == m.focus {
			focusLine = index
			break
		}
	}
	maxScroll := max(len(rows)-bodyHeight, 0)
	if !m.manualScroll {
		if focusLine < m.scroll {
			m.scroll = focusLine
		}
		if focusLine >= m.scroll+bodyHeight {
			m.scroll = focusLine - bodyHeight + 1
		}
	}
	m.scroll = min(max(m.scroll, 0), maxScroll)
	return editorFrame{
		x:          max((width-paneWidth)/2, 0),
		y:          max((height-paneHeight)/2, 0),
		width:      paneWidth,
		height:     paneHeight,
		inner:      inner,
		bodyHeight: bodyHeight,
		rows:       rows,
		scroll:     m.scroll,
	}
}

// visibleRows renders the window of body rows the panel shows, as panel-width
// rows.
func (m *Model) visibleRows(frame editorFrame) []string {
	styles := m.themeStyles()
	end := min(frame.scroll+frame.bodyHeight, len(frame.rows))
	visible := make([]string, 0, frame.bodyHeight)
	for _, row := range frame.rows[frame.scroll:end] {
		if row.kind == rowSection {
			visible = append(visible, widget.Section(styles, fit(row.text, frame.inner), "", frame.width))
			continue
		}
		visible = append(visible, widget.OverlayRow(styles, m.renderRow(row, frame.inner), frame.width))
	}
	return visible
}

// renderRow applies the token the row's role names. Spec section 6.2: the view
// takes a *theme.Styles and never constructs one.
func (m *Model) renderRow(row editorRow, width int) string {
	styles := m.themeStyles()
	line := fit(row.text, width)
	switch row.kind {
	case rowButton:
		if label := fit(row.button, width); strings.HasSuffix(line, label) {
			marker := strings.TrimSuffix(line, label)
			return styles.Overlay.Surf.Render(marker) + widget.Button(styles, widget.ButtonOpts{
				Text:           label,
				Selected:       m.focus == row.target,
				Pressed:        m.pointerState.IsPressed(pointer.ControlID(row.target)),
				UnderlineIndex: -1,
			})
		}
		return styles.Overlay.Surf.Render(line)
	case rowError:
		return styles.On(theme.StatusDanger, theme.OverlaySurf).Render(line)
	case rowHint:
		return styles.Overlay.FieldLabel.Render(line)
	case rowField:
		if m.focus == row.target {
			return styles.OnBold(theme.FgBase, theme.OverlaySurf).Render(line)
		}
		return styles.Overlay.FieldValue.Render(line)
	default:
		return styles.Overlay.Surf.Render(line)
	}
}

// title2 is the header band title of spec section 4 step 4.
func (m *Model) headerTitle() string {
	if m.mode == modeEdit {
		return "EDIT CARD"
	}
	return "CREATE CARD / " + string(m.status)
}

func (m *Model) sequenceTag() string {
	if m.mode == modeEdit && m.base.Seq > 0 {
		return fmt.Sprintf("#%d", m.base.Seq)
	}
	return ""
}

func (m *Model) footerLine(width int) string {
	footer := "tab navigate | ctrl+s/ctrl+enter save | esc close"
	if m.statusMessage != "" {
		prefix := "status: "
		if m.statusIsError {
			prefix = "error: "
		}
		footer = prefix + sanitize(m.statusMessage)
	}
	if m.guardClose {
		line := fit("[Discard] [Keep editing] | D discard | esc keep editing", width)
		line = strings.Replace(line, "[Discard]", m.pressedLabel("discard", "[Discard]"), 1)
		line = strings.Replace(line, "[Keep editing]", m.pressedLabel("keep", "[Keep editing]"), 1)
		return line
	}
	if m.drafting {
		footer = "drafting card... | esc cancel"
	} else if m.saving {
		footer = "saving card..."
	}
	return fit(footer, width)
}

func (m Model) pressedLabel(target, label string) string {
	if !m.pointerState.IsPressed(pointer.ControlID(target)) {
		return label
	}
	return m.themeStyles().Pressed.Render(label)
}

// bodyLines is the body as unstyled text. Pointer geometry reads the rows, not
// these lines; the control-safety tests read them.
func (m *Model) bodyLines(width int) []string {
	rows := m.bodyRows(width)
	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, row.plain())
	}
	return lines
}

func (m *Model) bodyRows(width int) []editorRow {
	rows := []editorRow{}
	if m.runner != nil {
		rows = append(rows, editorRow{text: "Draft with AI (fills the form; review before Save)", kind: rowSection})
		rows = append(rows, m.areaBlock("ai-prompt", "Request", m.draftPrompt, width, 2)...)
		action := "Draft"
		if m.drafting {
			action = "Cancel draft (Esc)"
		}
		rows = append(rows, m.actionRow("ai-draft", action), editorRow{})
	}
	rows = append(rows,
		m.inputRow("title", "Title", m.title, width),
		m.inputRow("emoji", "Emoji", m.emoji, width),
	)
	if emoji := strings.TrimSpace(m.emoji.Value()); emoji != "" && !board.IsSingleEmoji(emoji) {
		rows = append(rows, editorRow{
			text: "  error: one Extended_Pictographic character plus optional variation selector",
			kind: rowError,
		})
	}
	rows = append(rows, m.areaBlock("desc", "Description", m.desc, width, 3)...)
	rows = append(rows,
		m.choiceRow("prio", "Priority", fmt.Sprintf("%d (%s)  left/right", m.prio, priorityName(m.prio))),
		m.inputRow("due", "Due", m.due, width, "  [/] day; ctrl+x clear"),
		m.choiceRow("effort", "Effort", effortName(m.effort)+"  left/right; ctrl+x clear"),
		m.choiceRow("blocked", "Blocked", boolName(m.blocked)+"  space toggle"),
		m.inputRow("labels", "Labels", m.label, width),
		editorRow{text: "  selected: " + safeList(m.tags), kind: rowHint},
	)
	if m.focus == "labels" && m.labelsOpen {
		rows = append(rows, m.labelSuggestionRows()...)
	}
	if m.labelsErr != nil {
		rows = append(rows, editorRow{text: "  labels error: " + safeError(m.labelsErr), kind: rowError})
	}
	rows = append(rows, m.areaBlock("checks", "Checklist (x prefix = done)", m.checks, width, 3)...)
	rows = append(rows, m.similarRows()...)
	if m.stale {
		rows = append(rows, editorRow{text: "  external refresh withheld while form is dirty", kind: rowHint})
	}
	if m.statusMessage != "" {
		prefix, kind := "  status: ", rowHint
		if m.statusIsError {
			prefix, kind = "  error: ", rowError
		}
		rows = append(rows, editorRow{text: prefix + sanitize(m.statusMessage), kind: kind})
	}
	return append(rows, editorRow{}, m.actionRow("cancel", "Cancel"), m.actionRow("save", "Save card"))
}

func (m *Model) inputRow(target, label string, input textinput.Model, width int, suffix ...string) editorRow {
	marker := m.controlMarker(target)
	prefix := marker + label + ": "
	available := max(width-ansi.StringWidth(prefix), 1)
	return editorRow{
		text:   prefix + inputDisplay(input, m.focus == target, available) + strings.Join(suffix, ""),
		target: target,
		kind:   rowField,
	}
}

func (m *Model) choiceRow(target, label, value string) editorRow {
	return editorRow{
		text:   m.controlMarker(target) + label + ": " + sanitize(value),
		target: target,
		kind:   rowField,
	}
}

func (m *Model) actionRow(target, label string) editorRow {
	button := "[" + label + "]"
	return editorRow{
		text:   m.controlMarker(target) + button,
		button: button,
		target: target,
		kind:   rowButton,
	}
}

func (m *Model) areaBlock(target, label string, area textarea.Model, width, rows int) []editorRow {
	out := []editorRow{{
		text:   m.controlMarker(target) + label + ":",
		target: target,
		kind:   rowField,
	}}
	for _, line := range areaDisplay(area, m.focus == target, width, rows) {
		out = append(out, editorRow{text: line, target: target, kind: rowBody})
	}
	return out
}

func (m Model) controlMarker(target string) string {
	if m.pointerState.IsPressed(pointer.ControlID(target)) {
		return "! "
	}
	if m.focus == target {
		return "> "
	}
	return "  "
}

func (m Model) labelSuggestionRows() []editorRow {
	suggestions := m.filteredLabels()
	if len(suggestions) == 0 {
		return []editorRow{{text: "    (no label suggestions; Enter adds typed labels)", kind: rowHint}}
	}
	rows := []editorRow{{text: "    suggestions (up/down, Enter add):", kind: rowHint}}
	for i, suggestion := range suggestions {
		marker := "  "
		if m.pointerState.IsPressed(pointer.ControlID("label:" + suggestion)) {
			marker = "! "
		} else if i == min(m.labelHighlight, len(suggestions)-1) {
			marker = "› "
		}
		rows = append(rows, editorRow{
			text:   "    " + marker + sanitize(suggestion),
			target: "label:" + suggestion,
			kind:   rowBody,
		})
	}
	return rows
}

func (m Model) similarRows() []editorRow {
	switch {
	case m.similarLoading:
		return []editorRow{{}, {text: "  similar items: searching...", kind: rowHint}}
	case m.similarErr != nil:
		return []editorRow{{}, {text: "  similar items error: " + safeError(m.similarErr), kind: rowError}}
	}
	hits := m.visibleSimilar()
	if len(hits) == 0 {
		return nil
	}
	rows := []editorRow{{}, {text: fmt.Sprintf("  similar items (%d):", len(hits)), kind: rowSection}}
	for _, hit := range hits {
		target := "similar:" + similarKey(hit)
		rows = append(rows, editorRow{
			text:   m.similarMarker(target) + similarText(hit) + "  [Enter dismiss]",
			target: target,
			kind:   rowBody,
		})
	}
	return append(rows, editorRow{
		text:   m.similarMarker("similar:all") + "[Dismiss all similar items]",
		button: "[Dismiss all similar items]",
		target: "similar:all",
		kind:   rowButton,
	})
}

func (m Model) similarMarker(target string) string {
	switch {
	case m.pointerState.IsPressed(pointer.ControlID(target)):
		return "!   "
	case m.focus == target:
		return ">   "
	default:
		return "    "
	}
}

func similarText(hit store.SimilarHit) string {
	via := hit.Via
	if via == "" {
		via = "card"
	}
	if hit.Via == "killed" {
		context := "killed"
		if hit.KilledAt != "" {
			context += " " + hit.KilledAt
		}
		if reason := strings.TrimSpace(hit.Reason); reason != "" {
			context += " — " + reason
		}
		return "[" + sanitize(context) + "] " + sanitize(hit.Title)
	}
	return "[" + sanitize(via) + "] " + sanitize(hit.Title)
}

func inputDisplay(input textinput.Model, focused bool, width int) string {
	return formview.Input(input, focused, width, sanitize, cursorViewport)
}

func areaDisplay(area textarea.Model, focused bool, width, rows int) []string {
	return formview.Area(area, focused, width, rows, sanitize, cursorViewport)
}

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
	contentWidth := width - 1
	cursorColumn := ansi.StringWidth(before)
	left := max(cursorColumn-contentWidth, 0)
	visibleBefore := ansi.Cut(before, left, cursorColumn)
	remaining := max(contentWidth-ansi.StringWidth(visibleBefore), 0)
	return visibleBefore + "|" + ansi.Truncate(after, remaining, "")
}

func priorityName(priority int) string {
	return map[int]string{1: "urgent", 2: "high", 3: "normal", 4: "low"}[priority]
}

func effortName(effort string) string {
	if effort == "" {
		return "none"
	}
	return effort
}

func boolName(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func safeList(values []string) string {
	if len(values) == 0 {
		return "(none)"
	}
	safe := make([]string, len(values))
	for i, value := range values {
		safe[i] = "[" + sanitize(value) + "]"
	}
	return strings.Join(safe, " ")
}

func fit(line string, width int) string {
	return ansi.Truncate(sanitize(line), max(width, 0), "")
}
