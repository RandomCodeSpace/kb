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
	rowChoice                 // an activatable list row: hover raises it whole
	rowWidget                 // a row a widget already rendered whole
)

// editorRow is one logical body row. The target is the symbolic pointer
// control the row activates, carried structurally instead of recovered by
// matching the rendered text: card titles, descriptions and checklist text are
// untrusted, and text matching let them impersonate a control.
//
// mark is the row's focus gutter in plain cells (spec section 10.4.3), empty on
// a static row that reserves none. It is always FocusGutterW + FocusGutterGap
// wide whichever state the row is in, which is what keeps focus from reflowing
// the text it lands on (spec section 10.4.4); renderRow draws the styled bar
// over the same cells.
//
// rendered carries an already-styled row for the three states of spec section
// 10.8 - busy, empty and error - which the widgets own end to end.
type editorRow struct {
	mark     string
	text     string
	rendered string
	button   string
	target   string
	variant  theme.ButtonVariant
	kind     rowKind
}

// plain is the row as unstyled text, the form the pointer geometry and the
// control-safety tests read.
func (r editorRow) plain() string { return r.mark + r.text }

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
func (m *Model) MouseHandler(width, height int) func(tea.MouseMsg) tea.Cmd {
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
	// Rows 6 and 9 of spec section 10.5.2: a wheel scroll or a resize moves the
	// content under a pointer that has not moved, so hover is re-resolved from
	// the retained point against the map this frame just built.
	m.pointerState = m.pointerState.Reresolve(hitMap)
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
	paneWidth, paneHeight := metrics.OverlayPane(width, height)
	inner := metrics.OverlayContent(paneWidth)
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
//
// The focus gutter is drawn here rather than baked into the row's text because
// the bar is emitted per rendered line: a textarea block wraps to several rows
// and every one of them carries the same cell, so the bar is unbroken down the
// whole control (spec section 10.4.3).
func (m *Model) renderRow(row editorRow, width int) string {
	styles := m.themeStyles()
	on := theme.OverlaySurf
	if row.kind == rowChoice {
		on = styles.RowSurface(m.hovered(row.target))
	}
	if row.mark == "" {
		return m.renderContent(row, width, on)
	}
	gutter := widget.Gutter(styles, m.focus == row.target, theme.Brand, on)
	content := m.renderContent(row, max(width-ansi.StringWidth(gutter), 0), on)
	if row.kind != rowButton {
		// The button widget owns its own pressed token; every other row takes
		// the attribute here. Spec section 10.4.4: pressed costs zero cells.
		content = m.pointerState.Render(styles, pointer.ControlID(row.target), content)
	}
	return gutter + content
}

// renderContent renders the row's own cells, the ones to the right of its
// gutter.
func (m *Model) renderContent(row editorRow, width int, on theme.Slot) string {
	styles := m.themeStyles()
	if row.rendered != "" {
		return ansi.Truncate(row.rendered, max(width, 0), "")
	}
	line := fit(row.text, width)
	switch row.kind {
	case rowButton:
		return m.renderButton(row, width)
	case rowHint:
		return styles.Overlay.FieldLabel.Render(line)
	case rowChoice:
		return styles.On(theme.FgBase, on).Render(line)
	case rowField:
		if m.focus == row.target {
			return formview.Selection(
				styles.OnBold(theme.FgBase, theme.OverlaySurf),
				m.marked(row.target),
			).Render(line)
		}
		return styles.Overlay.FieldValue.Render(line)
	default:
		// Body rows carry a textarea's own lines, so they take the mark too.
		return formview.Selection(styles.Overlay.Surf, m.marked(row.target)).Render(line)
	}
}

// renderButton draws one action row through the button widget. The label is
// resolved through widget.Hotkey (spec section 10.4.2) even though an editor
// button is driven by focus and Enter rather than by a single-rune key: the
// resolver is the one place that decides, and no surface resolves its own.
func (m *Model) renderButton(row editorRow, width int) string {
	styles := m.themeStyles()
	label := fit(row.button, max(width-2*styles.Metrics.ButtonPadX, 0))
	if label == "" {
		return styles.Overlay.Surf.Render(fit(row.text, width))
	}
	text, underline := widget.Hotkey(label, nil)
	return widget.Button(styles, widget.ButtonOpts{
		Text:           text,
		Variant:        row.variant,
		Selected:       m.focus == row.target,
		Hovered:        m.hovered(row.target),
		Pressed:        m.pointerState.IsPressed(pointer.ControlID(row.target)),
		UnderlineIndex: underline,
		Padding:        [2]int{styles.Metrics.ButtonPadX, styles.Metrics.ButtonPadX},
	})
}

// hovered reports whether the pointer is over this row's control. Spec section
// 10.5.1: hover is pointer feedback and focus is keyboard position, so it
// changes the row's fill and never its gutter.
func (m Model) hovered(target string) bool {
	return target != "" && m.pointerState.IsHovered(pointer.ControlID(target))
}

// gutterMark is the plain form of the focus gutter of spec section 10.4.3: the
// Rail glyph plus its gap when the row has the keyboard, the same count of
// spaces when it does not. Both states cost the same cells, which is the whole
// point of reserving the column.
func (m *Model) gutterMark(target string) string {
	styles := m.themeStyles()
	metrics := styles.Metrics
	gap := strings.Repeat(" ", max(metrics.FocusGutterGap, 0))
	if m.focus == target {
		return strings.Repeat(styles.Glyph.Rail, max(metrics.FocusGutterW, 0)) + gap
	}
	return strings.Repeat(" ", max(metrics.FocusGutterW, 0)) + gap
}

// gutterWidth is the reserve every focusable row spends on its left edge.
func (m *Model) gutterWidth() int {
	metrics := m.themeStyles().Metrics
	return max(metrics.FocusGutterW, 0) + max(metrics.FocusGutterGap, 0)
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

// footerLine is the footer band: the hint ladder, or the busy line that
// replaces its head while an operation runs (spec section 10.8.4 rule 1).
//
// The band never carries an error. Ratified call 12: neither Danger slot clears
// the contrast floor on OverlayBand, so a failure is reported in a body row
// above the action row and the band goes back to hints.
func (m *Model) footerLine(width int) string {
	if m.guardClose {
		line := fit("[Discard] [Keep editing] | D discard | esc keep editing", width)
		line = strings.Replace(line, "[Discard]", m.pressedLabel("discard", "[Discard]"), 1)
		line = strings.Replace(line, "[Keep editing]", m.pressedLabel("keep", "[Keep editing]"), 1)
		return line
	}
	if m.drafting {
		return m.brandedBand(width)
	}
	if m.saving {
		// A local save is not cancellable, so the ladder has no live tail to
		// keep beside the busy line (spec section 10.8.4 rule 1).
		return widget.Busy(m.themeStyles(), widget.BusyOpts{
			Frame: m.plainFrame(), Label: saveLabel, On: theme.OverlayBand, Width: width,
		})
	}
	if m.statusMessage != "" && !m.statusIsError {
		return fit("status: "+sanitize(m.statusMessage), width)
	}
	return fit("tab navigate | ctrl+s/ctrl+enter save | esc close", width)
}

// plainFrame is the plain tier's frame (spec section 10.2.4): bubbles dots on
// the band's own FgSubtle. A local store write is plumbing however important it
// is, and a branded frame spent on it would stop meaning anything.
//
// The frame is no longer stripped. Spec section 10.8.4 deletes the ansi.Strip
// at all three sites: the frame is the one part of a busy row that is supposed
// to carry a color, and the band re-arms itself around it through BandRun.
func (m Model) plainFrame() string {
	if len(m.spin.Spinner.Frames) == 0 {
		return ""
	}
	return m.spin.View()
}

// brandedBand is the branded tier's band row (spec section 10.2.5). The engine
// is frame and label in one run - a gradient label that wipes in column by
// column - so it is laid into the band whole rather than composed against a
// second label. While the engine is unmounted or still inside the birth delay
// the row is the ordinary static label, which is also what a backgrounded
// editor shows.
func (m *Model) brandedBand(width int) string {
	row := m.brand.View()
	if row == "" {
		row = widget.Busy(m.themeStyles(), widget.BusyOpts{
			Label: draftLabel, On: theme.OverlayBand, Width: width,
		})
	}
	return row + fit(" | esc cancel", max(width-ansi.StringWidth(row), 0))
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
	inner := max(width-m.gutterWidth(), 1)
	rows := []editorRow{}
	if m.runner != nil {
		rows = append(rows, editorRow{text: "Draft with AI (fills the form; review before Save)", kind: rowSection})
		rows = append(rows, m.areaBlock("ai-prompt", "Request", m.draftPrompt, inner, 2)...)
		action := "Draft"
		if m.drafting {
			action = "Cancel draft (Esc)"
		}
		rows = append(rows, m.actionRow("ai-draft", action, theme.ButtonNeutral), editorRow{})
	}
	rows = append(rows,
		m.inputRow("title", "Title", m.title, inner),
		m.inputRow("emoji", "Emoji", m.emoji, inner),
	)
	if emoji := strings.TrimSpace(m.emoji.Value()); emoji != "" && !board.IsSingleEmoji(emoji) {
		// A validation failure is fixed by editing the field the focus is
		// already in, so it carries no retry tail (spec section 10.8.5).
		rows = append(rows, m.errorRows(width,
			"one Extended_Pictographic character plus optional variation selector", "")...)
	}
	rows = append(rows, m.areaBlock("desc", "Description", m.desc, inner, 3)...)
	rows = append(rows,
		m.choiceRow("prio", "Priority", fmt.Sprintf("%d (%s)  left/right", m.prio, priorityName(m.prio))),
		m.inputRow("due", "Due", m.due, inner, "  [/] day; ctrl+x clear"),
		m.choiceRow("effort", "Effort", effortName(m.effort)+"  left/right; ctrl+x clear"),
		m.choiceRow("blocked", "Blocked", boolName(m.blocked)+"  space toggle"),
		m.inputRow("project", "Project", m.project, inner, "  required"),
		m.inputRow("labels", "Labels", m.label, inner),
		editorRow{text: "  selected: " + safeList(m.tags), kind: rowHint},
	)
	if m.focus == "labels" && m.labelsOpen {
		rows = append(rows, m.labelSuggestionRows(width)...)
	}
	if m.labelsErr != nil {
		rows = append(rows, m.errorRows(width, safeError(m.labelsErr), "")...)
	}
	rows = append(rows, m.areaBlock("checks", "Checklist (x prefix = done)", m.checks, inner, 3)...)
	rows = append(rows, m.similarRows(width)...)
	if m.stale {
		rows = append(rows, editorRow{text: "  external refresh withheld while form is dirty", kind: rowHint})
	}
	if m.statusMessage != "" {
		if m.statusIsError {
			// Pinned directly above the action row, so the error and the control
			// that will retry it are adjacent (spec section 10.8.5).
			rows = append(rows, m.errorRows(width, sanitize(m.statusMessage), m.statusTail)...)
		} else {
			rows = append(rows, editorRow{text: "  status: " + sanitize(m.statusMessage), kind: rowHint})
		}
	}
	return append(rows, editorRow{},
		m.actionRow("cancel", "Cancel", theme.ButtonNeutral),
		m.actionRow("save", "Save card", theme.ButtonPrimary))
}

// errorRows is the error block of spec section 10.8.5, one editor row per
// rendered line. The widget owns sanitize, wrap, the hanging indent and the
// retry tail; the pane only names its tier and the control that failed.
func (m *Model) errorRows(width int, message, tail string) []editorRow {
	styles := m.themeStyles()
	block := widget.Error(styles, widget.ErrorOpts{
		Message:  message,
		Key:      tail,
		On:       theme.OverlaySurf,
		Width:    width,
		MaxLines: styles.Metrics.ErrorMaxLines,
	})
	rows := make([]editorRow, 0, len(block))
	for _, line := range block {
		rows = append(rows, editorRow{text: ansi.Strip(line), rendered: line, kind: rowError})
	}
	return rows
}

func (m *Model) inputRow(target, label string, input textinput.Model, width int, suffix ...string) editorRow {
	prefix := label + ": "
	available := max(width-ansi.StringWidth(prefix), 1)
	return editorRow{
		mark:   m.gutterMark(target),
		text:   prefix + inputDisplay(input, m.focus == target, available) + strings.Join(suffix, ""),
		target: target,
		kind:   rowField,
	}
}

func (m *Model) choiceRow(target, label, value string) editorRow {
	return editorRow{
		mark:   m.gutterMark(target),
		text:   label + ": " + sanitize(value),
		target: target,
		kind:   rowField,
	}
}

// actionRow is one visible button row (issue #152). The row's plain text spells
// the button's own cells - one pad, the label, one pad - so the rendered button
// and the text the pointer geometry measures stay the same width.
func (m *Model) actionRow(target, label string, variant theme.ButtonVariant) editorRow {
	return editorRow{
		mark:    m.gutterMark(target),
		text:    buttonPadding + label + buttonPadding,
		button:  label,
		target:  target,
		variant: variant,
		kind:    rowButton,
	}
}

// buttonPadding is one cell of filled surface on each side of a button label,
// the crush ButtonOpts look of spec section 5.1.
const buttonPadding = " "

// areaBlock is a textarea and its label. Every line the area wraps to carries
// the same gutter, so the focus bar runs unbroken down the whole control
// instead of marking only the row the label sits on (spec section 10.4.3).
func (m *Model) areaBlock(target, label string, area textarea.Model, width, rows int) []editorRow {
	out := []editorRow{{
		mark:   m.gutterMark(target),
		text:   label + ":",
		target: target,
		kind:   rowField,
	}}
	for _, line := range areaDisplay(area, m.focus == target, width, rows) {
		out = append(out, editorRow{
			mark:   m.gutterMark(target),
			text:   line,
			target: target,
			kind:   rowBody,
		})
	}
	return out
}

func (m *Model) labelSuggestionRows(width int) []editorRow {
	suggestions := m.filteredLabels()
	if len(suggestions) == 0 {
		// The block renders its header whether or not it is filled, so it takes
		// the empty row of spec section 10.8.3 rather than vanishing.
		return []editorRow{m.emptyRow(width, "no label suggestions", "enter", "add typed labels")}
	}
	rows := []editorRow{{text: "  suggestions (up/down, Enter add):", kind: rowHint}}
	for i, suggestion := range suggestions {
		marker := "  "
		if i == min(m.labelHighlight, len(suggestions)-1) {
			marker = "› "
		}
		target := "label:" + suggestion
		rows = append(rows, editorRow{
			mark:   m.gutterMark(target),
			text:   "  " + marker + sanitize(suggestion),
			target: target,
			kind:   rowChoice,
		})
	}
	return rows
}

// emptyRow is the empty state of spec section 10.8.3, rendered whole by the
// widget so the pane never composes the ladder itself.
func (m *Model) emptyRow(width int, headline, key, verb string) editorRow {
	row := widget.Empty(m.themeStyles(), widget.EmptyOpts{
		Headline: headline, Key: key, Verb: verb, On: theme.OverlaySurf, Width: width,
	})
	return editorRow{text: ansi.Strip(row), rendered: row, kind: rowWidget}
}

func (m *Model) similarRows(width int) []editorRow {
	switch {
	case m.similarLoading:
		// Rule 4 of spec section 10.8.4: one motion per surface. A body busy row
		// under a busy footer band renders its label with no frame.
		frame := ""
		if !m.busy() {
			frame = m.plainFrame()
		}
		row := widget.Busy(m.themeStyles(), widget.BusyOpts{
			Frame: frame, Label: similarLabel, On: theme.OverlaySurf, Width: width,
		})
		return []editorRow{{}, {text: ansi.Strip(row), rendered: row, kind: rowWidget}}
	case m.similarErr != nil:
		// Advisory: the save is not blocked by it, so there is nothing to retry.
		return append([]editorRow{{}}, m.errorRows(width, safeError(m.similarErr), "")...)
	}
	hits := m.visibleSimilar()
	if len(hits) == 0 {
		return nil
	}
	rows := []editorRow{{}, {text: fmt.Sprintf("  similar items (%d):", len(hits)), kind: rowSection}}
	for _, hit := range hits {
		target := "similar:" + similarKey(hit)
		rows = append(rows, editorRow{
			mark:   m.gutterMark(target),
			text:   "  " + similarText(hit) + "  [Enter dismiss]",
			target: target,
			kind:   rowChoice,
		})
	}
	const dismissAll = "Dismiss all similar items"
	return append(rows, editorRow{
		mark:    m.gutterMark("similar:all"),
		text:    buttonPadding + dismissAll + buttonPadding,
		button:  dismissAll,
		target:  "similar:all",
		variant: theme.ButtonNeutral,
		kind:    rowButton,
	})
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
