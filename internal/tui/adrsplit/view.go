package adrsplit

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/tui/formview"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
	"github.com/RandomCodeSpace/kb/internal/tui/widget"
)

// rowKind is the semantic role of one body row. It decides which token the row
// is rendered with; no view code composes a style of its own (spec section 6.2).
type rowKind uint8

const (
	rowBody    rowKind = iota // plain content on the overlay panel tier
	rowField                  // a labelled control row
	rowSection                // a section break band
	rowButton                 // an action, rendered by the button widget
	rowHint                   // secondary text
	rowError                  // an error line
)

// splitRow is one logical body row. The target is the symbolic pointer control
// the row activates, carried structurally instead of recovered by matching the
// rendered text: an ADR is untrusted input, and text matching let it
// impersonate a control.
//
// rendered holds the already-styled form for rows composed of widgets - a huh
// field, a checklist mark - whose content cannot be recovered from plain text.
type splitRow struct {
	text     string
	rendered string
	button   string
	target   string
	variant  theme.ButtonVariant
	kind     rowKind
}

// plain is the row as unstyled text, the form the pointer geometry and the
// control-safety tests read.
func (r splitRow) plain() string { return r.text }

// splitFrame is the resolved geometry of one render: where the panel sits, how
// much body fits, and which logical rows the window shows.
type splitFrame struct {
	x, y       int
	width      int
	height     int
	inner      int
	bodyHeight int
	rows       []splitRow
	scroll     int
}

// View renders the overlay without its board background.
func (m *Model) View(width, height int) string {
	if !m.open {
		return ""
	}
	width, height = max(width, 1), max(height, 1)
	frame := m.layout(width, height)
	panel := fitBlock(widget.Overlay(m.themeStyles(), m.panelOpts(frame)), width, height)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, panel)
}

// Overlay composes the split review over the current board/detail surface.
// Spec section 4: the panel is an elevation over what is behind it, never a
// frame, so it takes the shade step and the shadow with it.
func (m *Model) Overlay(background string, width, height int) string {
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

func (m *Model) panelOpts(frame splitFrame) widget.OverlayOpts {
	return widget.OverlayOpts{
		Title:  m.headerTitle(),
		Body:   m.visibleRows(frame),
		Footer: m.footerLine(frame.inner),
		Hint:   m.scrollHint(frame),
		Width:  frame.width,
		Height: frame.height,
	}
}

// headerTitle is the header band title of spec section 4 step 4.
func (m Model) headerTitle() string {
	if m.stage == stageInput {
		return "SPLIT ADR INTO STORIES"
	}
	return "REVIEW PROPOSED STORIES"
}

// scrollHint is the section 5.1 scroll indicator, shown only while the body
// does not fit the panel.
func (m *Model) scrollHint(frame splitFrame) string {
	if len(frame.rows) <= frame.bodyHeight {
		return ""
	}
	return widget.ScrollHint(m.themeStyles(), frame.scroll+frame.bodyHeight, len(frame.rows), theme.OverlayBand)
}

// layout resolves the panel geometry and scrolls the focused row into view.
func (m *Model) layout(width, height int) splitFrame {
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
	return splitFrame{
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
func (m *Model) visibleRows(frame splitFrame) []string {
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

// renderRow applies the token the row's role names.
func (m *Model) renderRow(row splitRow, width int) string {
	styles := m.themeStyles()
	if row.rendered != "" {
		return ansi.Truncate(row.rendered, max(width, 0), "")
	}
	line := fit(row.text, width)
	switch row.kind {
	case rowButton:
		if label := fit(row.button, width); strings.HasSuffix(line, label) {
			marker := strings.TrimSuffix(line, label)
			// A split row is driven by focus and Enter, so the resolver of
			// spec section 10.4.2 marks no hotkey on it.
			text, underline := widget.Hotkey(label, nil)
			return styles.Overlay.Surf.Render(marker) + widget.Button(styles, widget.ButtonOpts{
				Text:           text,
				Variant:        row.variant,
				Selected:       m.focus == row.target,
				Pressed:        m.pressed(row.target),
				UnderlineIndex: underline,
			})
		}
		return styles.Overlay.Surf.Render(line)
	case rowError:
		// Ratified call 12: StatusDanger fails AA on the panel tier at 2.96, so
		// an error inside a panel is TintDanger.
		return styles.On(theme.TintDanger, theme.OverlaySurf).Render(line)
	case rowHint:
		return styles.Overlay.FieldLabel.Render(line)
	case rowField:
		if m.focus == row.target {
			return formview.Selection(
				styles.OnBold(theme.FgBase, theme.OverlaySurf),
				m.marked(row.target),
			).Render(line)
		}
		return styles.Overlay.FieldValue.Render(line)
	default:
		// Body rows carry the paste area's own lines, so they take the mark too.
		return formview.Selection(styles.Overlay.Surf, m.marked(row.target)).Render(line)
	}
}

func (m *Model) bodyRows(width int) []splitRow {
	if m.stage == stageInput {
		return m.inputRows(width)
	}
	return m.reviewRows(width)
}

func (m *Model) inputRows(width int) []splitRow {
	rows := []splitRow{
		{text: "SOURCE", kind: rowSection},
		m.choiceRow("source", "Source", sourceChoices(), int(m.source), "", width),
	}
	if m.source == sourcePaste {
		rows = append(rows, m.areaBlock("adr", "ADR markdown", m.adr, width, 8)...)
		rows = append(rows, splitRow{
			text: fmt.Sprintf("  UTF-8 bytes: %d / %d", len([]byte(m.adr.Value())), maxADRBytes),
			kind: rowHint,
		})
	} else {
		rows = append(rows, m.inputRow("file", "ADR file", m.filePath, width))
		rows = append(rows, m.noteRows("read is bounded before AI receives the file", width)...)
	}
	rows = append(rows, m.choiceRow("max", "Max stories", storyCountChoices(), m.max-1, "  (1-20)", width))
	rows = append(rows, m.errorRows(width)...)
	return append(rows,
		splitRow{},
		m.actionRow("cancel", "Cancel", theme.ButtonNeutral),
		m.actionRow("split", "Propose stories", theme.ButtonPrimary),
	)
}

func (m *Model) reviewRows(width int) []splitRow {
	rows := m.noteRows("Nothing is created until Add selected.", width)
	rows = append(rows, splitRow{text: "STORIES", kind: rowSection})
	if len(m.rows) == 0 {
		// The band renders whether or not it is filled, so the section takes
		// the empty row of spec section 10.8.3 rather than showing nothing.
		rows = append(rows, m.emptyRow(width, "no stories proposed", "Back to source"))
	}
	for index := range m.rows {
		row := &m.rows[index]
		story := []splitRow{
			m.includeRow(index, row),
			m.inputRow(fmt.Sprintf("title:%d", index), "  Title", row.title, width),
			m.choiceRow(fmt.Sprintf("prio:%d", index), "  Priority", priorityChoices(), row.prio-1, "", width),
			m.choiceRow(fmt.Sprintf("effort:%d", index), "  Effort", effortChoices(), effortIndex(row.effort), "", width),
		}
		if row.created {
			// A written story is out of the batch: it keeps its rows so the
			// review still reads as a list, and loses every control target so
			// neither the keyboard cycle nor a click can reopen it.
			for i := range story {
				story[i].target = ""
			}
		}
		rows = append(rows, story...)
		switch {
		case row.created:
			rows = append(rows, splitRow{text: "    created", kind: rowHint})
		case row.err != "":
			// A per-item error stays inline under its own row: one line, no
			// glyph and no tail (spec section 10.8.5).
			rows = append(rows, splitRow{text: "    " + sanitize(row.err), kind: rowError})
		}
		rows = append(rows, splitRow{})
	}
	rows = append(rows, m.choiceRow("dest", "Destination", statusChoices(), statusIndex(m.dest), "", width))
	rows = append(rows, m.errorRows(width)...)
	return append(rows,
		splitRow{},
		m.actionRow("back", "Back to source", theme.ButtonNeutral),
		m.actionRow("cancel", "Close", theme.ButtonNeutral),
		m.actionRow("add", fmt.Sprintf("Add selected (%d)", m.selectedCount()), theme.ButtonPrimary),
	)
}

// includeRow is the checklist row of spec section 5.1: a created story is out
// of the batch for good, a selected one is in it, and the mark says which.
func (m *Model) includeRow(index int, row *storyRow) splitRow {
	state := widget.CheckOpen
	switch {
	case row.created:
		state = widget.CheckDropped
	case row.include:
		state = widget.CheckDone
	}
	target := fmt.Sprintf("include:%d", index)
	styles := m.themeStyles()
	label := strconv.Itoa(index+1) + " include"
	prefix := m.controlPrefix(target)
	mark := widget.Check(styles, label, state, theme.OverlaySurf, m.focus == target)
	return splitRow{
		text:     prefix + checkGlyph(styles, state) + " " + label,
		rendered: styles.Overlay.Surf.Render(prefix) + mark,
		target:   target,
		kind:     rowField,
	}
}

// checkGlyph is the plain form of a checklist mark, for the row's text.
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

// noteRows renders a disclaimer block. Spec section 5.2 assigns huh's Note to
// the AI disclaimer blocks in this overlay.
func (m *Model) noteRows(text string, width int) []splitRow {
	rendered := formview.HuhNote(m.themeStyles(), []string{text}, width)
	lines := strings.Split(rendered, "\n")
	rows := make([]splitRow, 0, len(lines))
	for _, line := range lines {
		plain := ansi.Strip(line)
		rows = append(rows, splitRow{text: "  " + strings.TrimSpace(plain), rendered: line, kind: rowBody})
	}
	return rows
}

func (m *Model) inputRow(target, label string, input textinput.Model, width int) splitRow {
	prefix := m.controlPrefix(target) + label + ": "
	available := max(width-ansi.StringWidth(prefix), 1)
	return splitRow{
		text:   prefix + inputDisplay(input, m.focus == target, available),
		target: target,
		kind:   rowField,
	}
}

// choiceRow is one single-row choice field. Spec section 5.2 assigns huh's
// Select to the Source, Max stories, Priority and Effort fields; the inline
// form is the one that keeps a choice on the single row the frozen v1.0.1
// layout and its per-row pointer target give it.
func (m *Model) choiceRow(target, label string, choices []string, selected int, suffix string, width int) splitRow {
	styles := m.themeStyles()
	prefix := m.controlPrefix(target) + label + ": "
	value := ""
	if selected >= 0 && selected < len(choices) {
		value = choices[selected]
	}
	// The inline field pads to its own width, so it is sized to the widest
	// option plus its two indicators rather than to the rest of the row: a
	// trailing hint has to sit next to the value, not at the panel edge.
	field := min(max(width-ansi.StringWidth(prefix)-ansi.StringWidth(suffix), 1), inlineWidth(choices))
	surface := styles.Overlay.Surf
	if m.focus == target {
		surface = styles.OnBold(theme.FgBase, theme.OverlaySurf)
	}
	return splitRow{
		text:     prefix + value + suffix,
		rendered: surface.Render(prefix) + formview.HuhInlineSelect(styles, choices, selected, field) + surface.Render(suffix),
		target:   target,
		kind:     rowField,
	}
}

func (m *Model) actionRow(target, label string, variant theme.ButtonVariant) splitRow {
	button := "[ " + sanitize(label) + " ]"
	return splitRow{
		text:    m.controlPrefix(target) + button,
		button:  button,
		target:  target,
		variant: variant,
		kind:    rowButton,
	}
}

func (m *Model) areaBlock(target, label string, area textarea.Model, width, rows int) []splitRow {
	out := []splitRow{{
		text:   m.controlPrefix(target) + label + ":",
		target: target,
		kind:   rowField,
	}}
	for _, line := range areaDisplay(area, m.focus == target, width, rows) {
		out = append(out, splitRow{text: line, target: target, kind: rowBody})
	}
	return out
}

// footerLine is the footer band content: the frozen hint ladder, the operation
// in progress, or the close guard.
// footerLine is the footer band: the hint ladder, or the busy line that
// replaces its head while an operation runs (spec section 10.8.4 rule 1).
//
// It never carries an error. Ratified call 12: neither Danger slot clears the
// contrast floor on OverlayBand, so a failure is reported in a body row above
// the action row and the band goes back to hints.
func (m *Model) footerLine(width int) string {
	footer := "tab navigate | esc close"
	switch {
	case m.guardClose:
		return m.confirmFooter()
	case m.brandBusy():
		return m.busyBand(m.brandRow(width), "esc cancel", width)
	case m.operation != "":
		return m.busyBand(m.plainBand(m.operation, width), "esc cancel", width)
	case m.adding:
		return m.busyBand(m.plainBand(addLabel, width), "", width)
	case m.status != "" && !m.statusIsError:
		footer = "status: " + sanitize(m.status)
	}
	return fit(footer, width)
}

// busyBand appends the hints that are still live to a busy head, so the band is
// the only row whose content changes while an operation runs.
func (m *Model) busyBand(head, tail string, width int) string {
	if tail == "" {
		return head
	}
	return head + fit(" | "+tail, max(width-ansi.StringWidth(head), 0))
}

// plainBand is the plain tier's busy line (spec section 10.2.4): bubbles dots
// on the band's own FgSubtle. The file read and the card writes are plumbing
// and keep it.
//
// The frame is no longer stripped. Spec section 10.8.4 deletes the ansi.Strip
// at all three sites: the frame is the one part of a busy row that is supposed
// to carry a color, and the band re-arms itself around it through BandRun.
func (m *Model) plainBand(label string, width int) string {
	return widget.Busy(m.themeStyles(), widget.BusyOpts{
		Frame: m.spin.View(), Label: label, On: theme.OverlayBand, Width: width,
	})
}

// brandRow is the branded tier's band row (spec section 10.2.5). The engine is
// frame and label in one run, so it is laid in whole; while it is unmounted or
// still inside the birth delay the row is the ordinary static label, which is
// also what a backgrounded overlay shows.
func (m *Model) brandRow(width int) string {
	if row := m.brand.View(); row != "" {
		return row
	}
	return widget.Busy(m.themeStyles(), widget.BusyOpts{
		Label: opSplitADR, On: theme.OverlayBand, Width: width,
	})
}

// emptyRow is the empty state of spec section 10.8.3, rendered whole by the
// widget so the panel never composes the ladder itself.
func (m *Model) emptyRow(width int, headline, key string) splitRow {
	row := widget.Empty(m.themeStyles(), widget.EmptyOpts{
		Headline: headline, Key: key, On: theme.OverlaySurf, Width: width,
	})
	return splitRow{text: ansi.Strip(row), rendered: row, kind: rowHint}
}

// errorRows is the error block of spec section 10.8.5, pinned directly above
// the action row so the failure and the control that will retry it are
// adjacent. It is empty while the panel has nothing to report.
func (m *Model) errorRows(width int) []splitRow {
	if !m.statusIsError || m.status == "" {
		return nil
	}
	styles := m.themeStyles()
	block := widget.Error(styles, widget.ErrorOpts{
		Message:  sanitize(m.status),
		Key:      m.statusTail,
		On:       theme.OverlaySurf,
		Width:    width,
		MaxLines: styles.Metrics.ErrorMaxLines,
	})
	rows := make([]splitRow, 0, len(block)+1)
	rows = append(rows, splitRow{})
	for _, line := range block {
		rows = append(rows, splitRow{text: ansi.Strip(line), rendered: line, kind: rowError})
	}
	return rows
}

func (m Model) selectedCount() int {
	count := 0
	for _, row := range m.rows {
		if row.include && !row.created && strings.TrimSpace(row.title.Value()) != "" {
			count++
		}
	}
	return count
}

func (m Model) controlPrefix(target string) string {
	if m.pressed(target) {
		return "! "
	}
	if m.focus == target {
		return "> "
	}
	return "  "
}

func (m Model) pressed(target string) bool { return m.pointerState.IsPressed(controlID(target)) }

// confirmFooter is the close guard. The labels are the vocabulary the frozen
// v1.0.1 pointer flow addresses; the pressed feedback is theme.Styles.Pressed.
func (m Model) confirmFooter() string {
	styles := m.themeStyles()
	return m.pointerState.Render(styles, controlID("discard"), guardDiscard) +
		"  " + m.pointerState.Render(styles, controlID("stay"), guardStay)
}

const (
	guardDiscard = "[ Discard ]"
	guardStay    = "[ Stay ]"
)

// inlineWidth is the cells an inline choice field needs: the widest option plus
// its previous and next indicators.
func inlineWidth(choices []string) int {
	widest := 0
	for _, choice := range choices {
		widest = max(widest, ansi.StringWidth(choice))
	}
	return widest + 4
}

func sourceChoices() []string { return []string{"paste", "file"} }

func priorityChoices() []string { return []string{"1", "2", "3", "4"} }

func effortChoices() []string {
	values := effortValues()
	choices := make([]string, 0, len(values))
	for _, value := range values {
		choices = append(choices, effortName(value))
	}
	return choices
}

func effortIndex(value string) int {
	for index, candidate := range effortValues() {
		if candidate == value {
			return index
		}
	}
	return 0
}

func storyCountChoices() []string {
	choices := make([]string, 0, maxStories)
	for value := 1; value <= maxStories; value++ {
		choices = append(choices, strconv.Itoa(value))
	}
	return choices
}

func statusChoices() []string {
	choices := make([]string, 0, len(board.Statuses))
	for _, status := range board.Statuses {
		choices = append(choices, statusName(status))
	}
	return choices
}

func statusIndex(value board.Status) int {
	for index, status := range board.Statuses {
		if status == value {
			return index
		}
	}
	return 0
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
	start := max(position-width+2, 0)
	end := min(start+width-1, len(runes))
	visible := append([]rune(nil), runes[start:end]...)
	cursor := position - start
	if cursor >= len(visible) {
		visible = append(visible, '|')
	} else {
		visible = append(visible[:cursor], append([]rune{'|'}, visible[cursor:]...)...)
	}
	return ansi.Truncate(string(visible), width, "")
}

func statusName(status board.Status) string {
	switch status {
	case board.StatusTodo:
		return "To Do"
	case board.StatusDoing:
		return "Doing"
	case board.StatusDone:
		return "Done"
	case board.StatusCancelled:
		return "Cancelled"
	default:
		return sanitize(string(status))
	}
}

func effortName(value string) string {
	if value == "" {
		return "none"
	}
	return value
}

func fit(line string, width int) string {
	return ansi.Truncate(sanitize(line), max(width, 0), "")
}

func fitBlock(block string, width, height int) string {
	lines := strings.Split(block, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], max(width, 0), "")
	}
	return strings.Join(lines, "\n")
}
