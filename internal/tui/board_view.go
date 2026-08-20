package tui

import (
	"fmt"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/tui/formview"
	"github.com/RandomCodeSpace/kb/internal/tui/pointer"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
	"github.com/RandomCodeSpace/kb/internal/tui/widget"
)

// fallbackStyles is the theme a zero-value Model renders through. The root
// model resolves its own on construction and again on tea.BackgroundColorMsg
// (spec section 6.3); this is built once, never mutated, and exists only so a
// Model assembled field by field still has a palette to draw with.
var fallbackStyles = sync.OnceValue(func() *theme.Styles { return theme.New(true) })

func (m Model) themeStyles() *theme.Styles {
	if m.styles != nil {
		return m.styles
	}
	return fallbackStyles()
}

// columnHue maps a board status onto its column identity hue (spec 1.3).
func columnHue(status board.Status) theme.Slot {
	switch status {
	case board.StatusDoing:
		return theme.HueDoing
	case board.StatusDone:
		return theme.HueDone
	case board.StatusCancelled:
		return theme.HueCancelled
	default:
		return theme.HueTodo
	}
}

type boardAction uint8

const (
	boardUnchanged boardAction = iota
	boardChanged
	boardToggledCancelled
)

// boardViewState is the complete read-only board interaction state. Keeping it
// together gives detail overlays one stable selectedTask seam without making
// board rendering part of the root model's refresh machinery.
type boardViewState struct {
	column        int
	rows          [len(boardStatuses)]int
	scrolls       [len(boardStatuses)]int
	manualScroll  [len(boardStatuses)]bool
	showCancelled bool
}

type boardCardClickedMsg struct{ taskID string }
type boardColumnClickedMsg struct{ status board.Status }
type filterTextClickedMsg struct{}
type filterLabelClickedMsg struct{ tag string }
type filterClearClickedMsg struct{}
type boardFooterClickedMsg struct{ key string }
type boardPointerDownMsg struct{ taskID string }
type boardPointerMoveMsg struct {
	status       board.Status
	beforeTaskID string
}
type boardPointerUpMsg struct{}
type boardColumnScrolledMsg struct {
	status board.Status
	offset int
}

type boardHitKind uint8

const (
	boardHitDefault boardHitKind = iota
	boardHitFilterText
	boardHitFilterLabel
	boardHitFilterClear
	boardHitFooterAction
	boardHitColumnHeading
)

type boardHit struct {
	x0, x1    int
	y0, y1    int
	status    board.Status
	taskID    string
	kind      boardHitKind
	tag       string
	key       string
	scroll    int
	maxScroll int
}

type renderedColumn struct {
	lines []string
	hits  []boardHit
}

type labelSpan struct {
	x0, x1 int
	tag    string
}

func (s boardViewState) visibleStatuses() []board.Status {
	limit := len(boardStatuses)
	if !s.showCancelled {
		limit--
	}
	return boardStatuses[:limit]
}

func (s *boardViewState) handleKey(key string, current board.Board) boardAction {
	switch key {
	case "c":
		s.showCancelled = !s.showCancelled
		if !s.showCancelled && s.column == len(boardStatuses)-1 {
			s.column--
		}
		s.clampRow(current)
		s.manualScroll[s.column] = false
		return boardToggledCancelled
	case "left", "h", "shift+tab":
		s.moveColumn(-1, current)
		s.manualScroll[s.column] = false
		return boardChanged
	case "right", "l", "tab":
		s.moveColumn(1, current)
		s.manualScroll[s.column] = false
		return boardChanged
	case "up", "k":
		if s.rows[s.column] > 0 {
			s.rows[s.column]--
		}
		s.manualScroll[s.column] = false
		return boardChanged
	case "down", "j":
		count := taskCount(current, boardStatuses[s.column])
		if s.rows[s.column]+1 < count {
			s.rows[s.column]++
		}
		s.manualScroll[s.column] = false
		return boardChanged
	case "1", "2", "3", "4":
		at := int(key[0] - '1')
		if at < len(s.visibleStatuses()) {
			s.column = at
			s.clampRow(current)
			s.manualScroll[s.column] = false
			return boardChanged
		}
	}
	return boardUnchanged
}

func (s *boardViewState) moveColumn(delta int, current board.Board) {
	count := len(s.visibleStatuses())
	s.column = (s.column + delta + count) % count
	s.clampRow(current)
}

func (s *boardViewState) clampRow(current board.Board) {
	count := taskCount(current, boardStatuses[s.column])
	if count == 0 {
		s.rows[s.column] = 0
	} else if s.rows[s.column] >= count {
		s.rows[s.column] = count - 1
	}
}

func (s *boardViewState) focusColumn(status board.Status, current board.Board) {
	for i, candidate := range s.visibleStatuses() {
		if candidate == status {
			s.column = i
			s.clampRow(current)
			s.manualScroll[s.column] = false
			return
		}
	}
}

func (s *boardViewState) focusTask(current board.Board, id string) bool {
	for _, task := range current.Tasks {
		if task.ID != id {
			continue
		}
		if task.Status == board.StatusCancelled && !s.showCancelled {
			return false
		}
		for i, status := range boardStatuses {
			if status == task.Status {
				s.column = i
				s.rows[i] = taskIndex(current, task.Status, id)
				s.manualScroll[i] = false
				return true
			}
		}
	}
	return false
}

func (s *boardViewState) adoptBoard(previous, current board.Board) {
	selected, hadSelection := s.selectedTask(previous)
	if hadSelection {
		for _, task := range current.Tasks {
			if task.ID == selected.ID {
				if s.focusTask(current, task.ID) {
					return
				}
				break
			}
		}
	}
	s.normalizeSelection(current)
}

// normalizeSelection keeps refresh focus on a selectable visible card when
// one exists. Ordinary column navigation may still focus an empty column.
func (s *boardViewState) normalizeSelection(current board.Board) {
	visible := s.visibleStatuses()
	if taskCount(current, boardStatuses[s.column]) > 0 {
		s.clampRow(current)
		return
	}
	for offset := 1; offset < len(visible); offset++ {
		column := (s.column + offset) % len(visible)
		if taskCount(current, visible[column]) == 0 {
			continue
		}
		s.column = column
		s.clampRow(current)
		return
	}
	s.clampRow(current)
}

func (s boardViewState) selectedTask(current board.Board) (board.Task, bool) {
	status := boardStatuses[s.column]
	want := s.rows[s.column]
	at := 0
	for _, task := range current.Tasks {
		if task.Status != status {
			continue
		}
		if at == want {
			return task, true
		}
		at++
	}
	return board.Task{}, false
}

// selectedTask is the narrow handoff used by the card-detail overlay.
func (m Model) selectedTask() (board.Task, bool) {
	return m.boardView.selectedTask(m.filteredBoard())
}

func taskCount(current board.Board, status board.Status) int {
	count := 0
	for _, task := range current.Tasks {
		if task.Status == status {
			count++
		}
	}
	return count
}

func taskIndex(current board.Board, status board.Status, id string) int {
	index := 0
	for _, task := range current.Tasks {
		if task.Status != status {
			continue
		}
		if task.ID == id {
			return index
		}
		index++
	}
	return 0
}

func (m Model) renderBoard() (string, []boardHit) {
	styles := m.themeStyles()
	metrics := styles.Metrics
	width := max(m.width, 1)
	height := max(m.height, 8)

	statuses := m.boardView.visibleStatuses()
	if width < metrics.WideFrame {
		statuses = statuses[m.boardView.column : m.boardView.column+1]
	}
	layout := boardColumnLayout(metrics, width, len(statuses))
	density := metrics.DensityFor(height, layout.inner)

	rows := []string{m.renderTopBar(styles, width)}
	filterLine, filterHits := m.renderFilterBar(width)
	rows = append(rows, strings.Split(filterLine, "\n")...)
	for range metrics.PagePad(density) {
		rows = append(rows, fillRow(styles.Board.PagePad, "", width))
	}

	chromeTop := len(rows)
	bodyHeight := max(height-chromeTop-1, 1)
	columns := make([]renderedColumn, 0, len(statuses))
	for i, status := range statuses {
		columns = append(columns, m.renderBoardColumnAt(status, layout.widths[i], bodyHeight, density))
	}
	body, hits := joinColumns(styles, columns, layout, width)
	for i := range hits {
		hits[i].y0 += chromeTop
		hits[i].y1 += chromeTop
	}
	hits = append(filterHits, hits...)
	rows = append(rows, body...)

	state, stateSlot := m.boardState()
	cancelled := "off"
	if m.boardView.showCancelled {
		cancelled = "on"
	}
	help := "j/k cards | h/l/tab columns | 1-4 jump | ? help | q quit"
	if width >= metrics.WideFrame {
		help = "j/k cards | h/l/tab columns | 1-4 jump | c cancelled:" + cancelled + " | ? help | q quit"
	}
	if m.editor.Enabled() {
		help = "n new | e edit | " + help
	}
	footer := ""
	switch {
	case m.noticeOwnsFooter():
		footer = fitLine(state, width)
	case m.settingsNew != nil:
		footer = settingsBoardFooter(state, cancelled, m.editor.Enabled(), m.adr.Enabled(), width)
		if m.issueImport.Enabled() && width >= 24 {
			footer = fitLine("i import | "+footer, width)
		}
		hits = append(hits, boardFooterHits(footer, height-1, width)...)
	default:
		footer = fitLine(state+" | "+help, width)
		hits = append(hits, boardFooterHits(footer, height-1, width)...)
	}
	rows = append(rows, m.renderFooter(styles, footer, stateSlot, width))
	return strings.Join(rows, "\n"), hits
}

// noticeOwnsFooter reports whether a transient move or action notice replaces
// the whole hint ladder, which is also why that footer carries no hit regions.
func (m Model) noticeOwnsFooter() bool {
	moveActive := m.move.lifted != nil || m.move.saving
	if (moveActive || m.move.notice) && m.move.status != "" {
		return true
	}
	if m.actionNotice {
		return true
	}
	return m.move.status != "" && m.loadErr == nil && m.pollErr == nil && m.preferenceErr == nil
}

// boardState resolves the footer's state segment and the semantic hue it
// carries (spec section 1.5).
func (m Model) boardState() (string, theme.Slot) {
	moveActive := m.move.lifted != nil || m.move.saving
	switch {
	case (moveActive || m.move.notice) && m.move.status != "":
		return sanitizeTerminal(m.move.status), theme.StatusWarn
	case m.actionNotice && m.actionStatus != "":
		if m.actionStatusError {
			return sanitizeTerminal(m.actionStatus), theme.StatusDanger
		}
		return sanitizeTerminal(m.actionStatus), theme.StatusOK
	case m.loadErr != nil:
		return "error: " + m.loadErr.Error(), theme.StatusDanger
	case m.pollErr != nil:
		return "error: " + m.pollErr.Error(), theme.StatusDanger
	case m.preferenceErr != nil:
		return "error: " + m.preferenceErr.Error(), theme.StatusDanger
	case m.move.status != "":
		return sanitizeTerminal(m.move.status), theme.StatusWarn
	case (m.loading && !m.haveBoardSnapshot) || (m.watcher != nil && !m.haveVersion):
		return "loading board...", theme.FgMuted
	default:
		return "ready", theme.StatusOK
	}
}

// renderTopBar is the Canvas row of spec section 2.1: the wordmark in the brand
// hue, the board identity, and the shipped counter in the success hue.
func (m Model) renderTopBar(styles *theme.Styles, width int) string {
	title := strings.TrimSpace(sanitizeTerminal(m.board.Title))
	if title == "" {
		title = "Board"
	}
	line := styles.OnBold(theme.Brand, theme.Canvas).Render("kb") +
		styles.Board.TopBar.Render(" / "+title+" / "+sanitizeTerminal(m.user))
	if shipped := m.shippedCount(); shipped > 0 {
		line += styles.On(theme.StatusOK, theme.Canvas).Render(fmt.Sprintf(" / ×%d shipped today", shipped))
	}
	return fillRow(styles.Board.Canvas, fitLine(line, width), width)
}

// boardLayout is the resolved column geometry of spec section 2.5: a page
// margin on a wide frame and the whole remaining width split proportionally
// across the visible columns, so a wide terminal becomes a wide board instead
// of a centered strip with dead canvas either side of it.
type boardLayout struct {
	margin int   // left page margin; the first column starts here
	widths []int // panel width per visible column
	inner  int   // narrowest card inner width, for the compaction threshold
}

func boardColumnLayout(metrics theme.Metrics, width, count int) boardLayout {
	margin := metrics.PageMargin(width)
	widths := splitWidths(max(width-2*margin, 1), count)
	if widths[len(widths)-1] < metrics.MinColumnWidth {
		// A column narrower than the floor stops being a column, so the floor
		// wins and the overflow is clipped at the frame edge rather than
		// shrinking every panel into unreadability.
		for i := range widths {
			widths[i] = metrics.MinColumnWidth
		}
	}
	narrowest := widths[len(widths)-1]
	return boardLayout{
		margin: margin,
		widths: widths,
		inner:  metrics.CardInner(max(narrowest-2*metrics.ColumnPadX, 0), theme.DensityNormal),
	}
}

// renderFooter draws the status band of spec section 2.1: one Surface row whose
// leading segment carries the state hue and whose action hints keep the pointer
// identities the footer has always had.
func (m Model) renderFooter(styles *theme.Styles, text string, state theme.Slot, width int) string {
	parts := strings.Split(text, " | ")
	separator := styles.On(theme.FgMuted, theme.Surface).Render(" | ")
	rendered := make([]string, 0, len(parts))
	for index, part := range parts {
		key := boardFooterKey(part)
		style := styles.Board.Footer
		if index == 0 && key == "" {
			style = styles.On(state, theme.Surface)
		}
		content := style.Render(part)
		if key != "" {
			content = m.pointerState.Render(styles, boardFooterControlID(key), content)
		}
		rendered = append(rendered, content)
	}
	return fillRow(styles.Board.Footer, fitLine(strings.Join(rendered, separator), width), width)
}

func boardFooterHits(footer string, row, width int) []boardHit {
	parts := strings.Split(footer, " | ")
	hits := make([]boardHit, 0, len(parts))
	x := 0
	for _, part := range parts {
		key := boardFooterKey(part)
		partWidth := ansi.StringWidth(part)
		if key != "" && x < width {
			hits = append(hits, boardHit{x0: x, x1: min(x+partWidth, width), y0: row, y1: row + 1, kind: boardHitFooterAction, key: key})
		}
		x += partWidth + 3
	}
	return hits
}

func boardFooterKey(part string) string {
	switch {
	case part == "s" || strings.HasPrefix(part, "s settings"):
		return "s"
	case strings.HasPrefix(part, "? help"):
		return "?"
	case strings.HasPrefix(part, "q quit"):
		return "q"
	case strings.HasPrefix(part, "n new"):
		return "n"
	case strings.HasPrefix(part, "e edit"):
		return "e"
	case strings.HasPrefix(part, "a split ADR"):
		return "a"
	case strings.HasPrefix(part, "i import"):
		return "i"
	case strings.HasPrefix(part, "c cancelled:"):
		return "c"
	default:
		return ""
	}
}

func boardFooterControlID(key string) pointer.ControlID {
	return pointer.ControlID("board-footer:" + key)
}

func boardHitControlID(hit boardHit) pointer.ControlID {
	switch hit.kind {
	case boardHitFooterAction:
		return boardFooterControlID(hit.key)
	case boardHitFilterText:
		return pointer.ControlID("board-filter:text")
	case boardHitFilterLabel:
		if hit.taskID != "" {
			return boardCardLabelControlID(hit.taskID, hit.tag)
		}
		return pointer.ControlID("board-filter:label:" + hit.tag)
	case boardHitFilterClear:
		return pointer.ControlID("board-filter:clear")
	case boardHitColumnHeading:
		return pointer.ControlID("board-column:" + string(hit.status))
	}
	return ""
}

func boardCardLabelControlID(taskID, tag string) pointer.ControlID {
	return pointer.ControlID("board-card-label:" + taskID + ":" + tag)
}

// renderFilterBar draws the two toolbar rows on the Canvas tier with the filter
// field on Surface (spec section 2.1). The row's text vocabulary is unchanged
// from v1.0.1: the [+ tag] / [x tag] markers are the filter's state affordance
// and every click region is keyed to their plain-text widths, so this slice
// restyles the tiers and hues and leaves the glyphs alone.
func (m Model) renderFilterBar(width int) (string, []boardHit) {
	styles := m.themeStyles()
	width = max(width, 1)
	canvas := styles.Board.Canvas
	separator := styles.On(theme.FgMuted, theme.Canvas).Render(" | ")
	hits := make([]boardHit, 0, 2+len(m.filterLabels()))
	lines := [2][]string{}
	appendPart := func(row int, part string, style lipgloss.Style, kind boardHitKind, tag string) {
		x := ansi.StringWidth(strings.Join(lines[row], ""))
		if len(lines[row]) > 0 {
			lines[row] = append(lines[row], separator)
			x += 3
		}
		start := x
		partWidth := ansi.StringWidth(part)
		hit := boardHit{x0: start, x1: min(start+partWidth, width), y0: row + 1, y1: row + 2, kind: kind, tag: tag}
		lines[row] = append(lines[row], m.pointerState.Render(styles, boardHitControlID(hit), style.Render(part)))
		if kind != boardHitDefault && start < width {
			hits = append(hits, hit)
		}
	}

	value := sanitizeTerminal(m.filter.input.Value())
	if value == "" {
		value = "Filter cards"
	}
	text := "/ " + value
	if m.filter.focus == filterText {
		text = "> " + settingsInputDisplay(m.filter.input, false, true, max(min(width-2, 40), 1))
	}
	labels := m.filterLabels()
	focusTag := ""
	if m.filter.focus == filterLabels && len(labels) > 0 {
		focusTag = labels[min(max(m.filter.labelIndex, 0), len(labels)-1)]
	}
	labelStyle := func(tag string) lipgloss.Style {
		if !m.filter.hasTag(tag) {
			return styles.On(theme.FgMuted, theme.Canvas)
		}
		return styles.OnBold(theme.LabelSlot(widget.LabelWheel(tag)), theme.Canvas)
	}
	appendLabel := func(row int, tag string) {
		marker := "+"
		if m.filter.hasTag(tag) {
			marker = "x"
		}
		label := "[" + marker + " " + sanitizeTerminal(tag) + "]"
		if tag == focusTag {
			label = ">" + label + "<"
		}
		appendPart(row, label, labelStyle(tag), boardHitFilterLabel, tag)
	}
	if focusTag != "" {
		appendLabel(0, focusTag)
	}
	appendPart(0, text, formview.Selection(
		styles.On(theme.FgBase, theme.Surface),
		m.filter.focus == filterText && m.filter.mark.Active(filterMarkField),
	), boardHitFilterText, "")
	if m.filter.active() {
		count := fmt.Sprintf("%d of %d cards", len(m.filteredBoard().Tasks), len(m.board.Tasks))
		appendPart(1, count, styles.On(theme.FgMuted, theme.Canvas), boardHitDefault, "")
	}
	for _, tag := range labels {
		if tag != focusTag && m.filter.hasTag(tag) {
			appendLabel(1, tag)
		}
	}
	if m.filter.active() {
		appendPart(1, "[clear]", styles.On(theme.StatusDanger, theme.Canvas), boardHitFilterClear, "")
	}
	for _, tag := range labels {
		if tag != focusTag && !m.filter.hasTag(tag) {
			appendLabel(1, tag)
		}
	}
	return strings.Join([]string{
		fillRow(canvas, fitLine(strings.Join(lines[0], ""), width), width),
		fillRow(canvas, fitLine(strings.Join(lines[1], ""), width), width),
	}, "\n"), hits
}

func settingsBoardFooter(state, cancelled string, editorEnabled, adrEnabled bool, width int) string {
	candidates := [][]string{
		{"s settings", "t/x/r/D actions", "j/k cards", "h/l/tab columns", "c cancelled:" + cancelled, "q quit"},
		{"s settings", "j/k cards", "h/l/tab columns", "1-4 jump", "c cancelled:" + cancelled, "q quit"},
		{"s settings", "j/k cards", "h/l/tab columns", "c cancelled:" + cancelled, "q quit"},
		{"s settings", "j/k cards", "h/l/tab columns", "q quit"},
		{"s settings", "j/k", "h/l/tab", "q quit"},
		{"s settings", "j/k h/l", "q quit"},
		{"s", "j/k h/l", "q quit"},
		{"s", "nav", "q quit"},
		{"s", "q quit"},
		{"q quit"},
	}
	if editorEnabled {
		candidates = append([][]string{
			{"s settings", "n new", "e edit", "t/x/r/D actions", "j/k cards", "h/l/tab columns", "c cancelled:" + cancelled, "q quit"},
			{"s settings", "n new", "e edit", "j/k cards", "h/l/tab columns", "1-4 jump", "c cancelled:" + cancelled, "q quit"},
			{"s settings", "n new", "e edit", "j/k cards", "h/l/tab columns", "c cancelled:" + cancelled, "q quit"},
			{"s settings", "n new", "e edit", "j/k cards", "c cancelled:" + cancelled, "q quit"},
			{"s settings", "n new", "e edit", "j/k cards", "h/l/tab columns", "q quit"},
		}, candidates...)
	}
	if adrEnabled {
		for i := range candidates {
			candidates[i] = append([]string{"a split ADR"}, candidates[i]...)
		}
	}
	for index := range candidates {
		last := len(candidates[index]) - 1
		withHelp := make([]string, 0, len(candidates[index])+1)
		withHelp = append(withHelp, candidates[index][:last]...)
		withHelp = append(withHelp, "? help", candidates[index][last])
		candidates[index] = withHelp
	}
	if width <= 40 {
		compact := "s settings | ? help | j/k h/l | q quit"
		if ansi.StringWidth(compact) <= width {
			return compact
		}
	}
	minimumStateWidth := min(ansi.StringWidth(state), 5)
	for _, candidate := range candidates {
		help := strings.Join(candidate, " | ")
		stateWidth := width - ansi.StringWidth(help) - 3
		if stateWidth < minimumStateWidth {
			continue
		}
		return fitLine(state, stateWidth) + " | " + help
	}
	for _, candidate := range candidates {
		help := strings.Join(candidate, " | ")
		if ansi.StringWidth(help) <= width {
			return help
		}
	}
	return fitLine("q quit", width)
}

func (m Model) render() string {
	content, _ := m.renderBoard()
	return content
}

func splitWidths(total, count int) []int {
	if count <= 1 {
		return []int{max(total, 1)}
	}
	usable := max(total-(count-1), count)
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

func (m Model) renderBoardColumn(status board.Status, width, height int) renderedColumn {
	metrics := m.themeStyles().Metrics
	inner := metrics.CardInner(max(width-2*metrics.ColumnPadX, 0), theme.DensityNormal)
	return m.renderBoardColumnAt(status, width, height, metrics.DensityFor(max(m.height, 8), inner))
}

// renderBoardColumnAt draws one column panel at the density the frame resolved
// to. Depth is the shade step from the band to the panel body to the cards;
// there is no border anywhere in it (spec section 2.2).
func (m Model) renderBoardColumnAt(status board.Status, width, height int, density theme.Density) renderedColumn {
	styles := m.themeStyles()
	metrics := styles.Metrics
	index := statusIndex(status)
	tasks := tasksInStatus(m.filteredBoard(), status)
	hits := []boardHit{
		{x1: width, y1: height, status: status},
		{x1: width, y1: 1, status: status, kind: boardHitColumnHeading},
	}
	if width <= 0 || height <= 0 {
		return renderedColumn{hits: hits}
	}

	meta := ""
	metaRows := 0
	if !density.Compact() {
		if meta = columnMetaLine(tasks); meta != "" {
			metaRows = 1
		}
	}
	inset := metrics.ColumnPad(density)
	cardLines, owners, spans := m.renderTaskLines(tasks, status, max(width-2*inset, 0), density)

	contentHeight := max(height-1-metaRows, 0)
	if len(cardLines) > contentHeight && contentHeight > 0 {
		contentHeight-- // the overflow cue of spec section 3.7 owns the last row
	}
	maxScroll := max(len(cardLines)-contentHeight, 0)
	start := visibleCardStart(cardLines, owners, m.boardView.rows[index], contentHeight)
	if m.boardView.manualScroll[index] {
		start = min(max(m.boardView.scrolls[index], 0), maxScroll)
	}
	hits[0].scroll = start
	hits[0].maxScroll = maxScroll

	body := make([]string, 0, contentHeight)
	for row := 0; row < contentHeight; row++ {
		source := start + row
		line := ""
		if source < len(cardLines) {
			line = cardLines[source]
		}
		body = append(body, line)
		y := 1 + metaRows + row
		if source < len(owners) && owners[source] != "" {
			hits = append(hits, boardHit{x1: width, y0: y, y1: y + 1, status: status, taskID: owners[source]})
		}
		if source < len(spans) {
			for _, span := range spans[source] {
				hits = append(hits, boardHit{
					x0: inset + span.x0, x1: min(inset+span.x1, width),
					y0: y, y1: y + 1, status: status,
					kind: boardHitFilterLabel, tag: span.tag, taskID: owners[source],
				})
			}
		}
	}

	lines := widget.Panel(styles, widget.PanelOpts{
		Header: widget.BandOpts{
			Index:   index + 1,
			Label:   statusLabel(status),
			Count:   len(tasks),
			Hue:     columnHue(status),
			Focused: m.boardView.column == index,
		},
		Meta:    meta,
		Body:    body,
		More:    hiddenCards(owners, start+contentHeight),
		Width:   width,
		Height:  height,
		Density: density,
	})
	// The band is the column's click target, so the pressed state wraps the row
	// the panel drew rather than the panel wrapping the pointer.
	lines[0] = m.pointerState.Render(styles, pointer.ControlID("board-column:"+string(status)), lines[0])
	return renderedColumn{lines: lines, hits: hits}
}

// columnMetaLine is the row under the band (spec section 2.3). The blocked
// segment appears only when the count is non-zero.
func columnMetaLine(tasks []board.Task) string {
	blocked := 0
	for _, task := range tasks {
		if task.Blocked {
			blocked++
		}
	}
	line := fmt.Sprintf("%d cards", len(tasks))
	if len(tasks) == 1 {
		line = "1 card"
	}
	if blocked > 0 {
		line += fmt.Sprintf(" · %d blocked", blocked)
	}
	return line
}

// hiddenCards counts the cards whose rows start below the visible window.
func hiddenCards(owners []string, from int) int {
	count, previous := 0, ""
	for index, owner := range owners {
		if owner != "" && owner != previous && index >= from {
			count++
		}
		previous = owner
	}
	return count
}

func (m Model) renderTaskLines(tasks []board.Task, status board.Status, width int, density theme.Density) ([]string, []string, [][]labelSpan) {
	styles := m.themeStyles()
	if len(tasks) == 0 {
		return []string{styles.Column.Meta.Render("(empty)")}, []string{""}, [][]labelSpan{nil}
	}
	index := statusIndex(status)
	focused := m.boardView.column == index
	selected := m.boardView.rows[index]
	descLines := styles.Metrics.DescLines(max(m.height, 8), density)
	gap := styles.Metrics.CardGapRows(density)
	lines := make([]string, 0, len(tasks)*5)
	owners := make([]string, 0, len(tasks)*5)
	spans := make([][]labelSpan, 0, len(tasks)*5)
	for i, task := range tasks {
		isSelected := focused && i == selected
		alternate := density.Compact() && i%2 == 1
		surface := styles.Surface(isSelected, alternate)
		tags := make([]string, 0, len(task.Tags))
		for _, tag := range task.Tags {
			tags = append(tags, sanitizeTerminal(tag))
		}
		rows, cardSpans := widget.CardWithSpans(styles, widget.CardOpts{
			Title:     sanitizeTerminal(task.Title),
			Emoji:     sanitizeTerminal(task.Emoji),
			Seq:       seqLabel(task),
			Desc:      sanitizeTerminal(task.Desc),
			Meta:      m.cardMeta(styles, task, surface, density),
			Labels:    tags,
			Priority:  task.Prio,
			Selected:  isSelected,
			Alt:       alternate,
			Width:     width,
			DescLines: descLines,
			Density:   density,
		})
		rowSpans := make([][]labelSpan, len(rows))
		for _, span := range cardSpans {
			// The span reports its position in CardOpts.Labels, so the hit keeps
			// the task's exact tag while the card renders the sanitized one.
			rowSpans[span.Row] = append(rowSpans[span.Row],
				labelSpan{x0: span.X0, x1: span.X1, tag: task.Tags[span.Index]})
		}
		for rowIndex, line := range rows {
			for spanIndex := len(rowSpans[rowIndex]) - 1; spanIndex >= 0; spanIndex-- {
				span := rowSpans[rowIndex][spanIndex]
				line = ansi.Cut(line, 0, span.x0) +
					m.pointerState.Render(styles, boardCardLabelControlID(task.ID, span.tag), ansi.Cut(line, span.x0, span.x1)) +
					ansi.Cut(line, span.x1, ansi.StringWidth(line))
			}
			lines = append(lines, line)
			owners = append(owners, task.ID)
			spans = append(spans, rowSpans[rowIndex])
		}
		if i+1 < len(tasks) {
			for range gap {
				lines = append(lines, "")
				owners = append(owners, "")
				spans = append(spans, nil)
			}
		}
	}
	return lines, owners, spans
}

// seqLabel is the right-aligned card sequence of spec section 3.2.
func seqLabel(task board.Task) string {
	if task.Seq <= 0 {
		return ""
	}
	return fmt.Sprintf("#%d", task.Seq)
}

// cardMeta is the chip row of spec section 3.4, in survival order: priority,
// age, blocked, due, effort. Compact degrades the pills to flat marks.
func (m Model) cardMeta(styles *theme.Styles, task board.Task, surface theme.Slot, density theme.Density) []string {
	flat := density.Compact()
	meta := []string{
		widget.Priority(styles, task.Prio, surface),
		styles.On(theme.FgMuted, surface).Render(ageChip(task, m.renderedAt)),
	}
	if task.Blocked {
		if flat {
			meta = append(meta, styles.OnBold(theme.StatusWarn, surface).Render(styles.Glyph.Blocked))
		} else {
			meta = append(meta, widget.Chip(styles, widget.ChipOpts{Text: "blocked", Fill: theme.StatusWarn, On: surface}))
		}
	}
	if task.Due != "" {
		label, overdue := dueChip(sanitizeTerminal(task.Due), m.renderedAt)
		fill := theme.StatusInfo
		if overdue {
			fill = theme.StatusDanger
		}
		if flat {
			meta = append(meta, styles.OnBold(fill, surface).Render("!"+compactDue(label)))
		} else {
			meta = append(meta, widget.Chip(styles, widget.ChipOpts{Text: label, Fill: fill, On: surface}))
		}
	}
	if task.Effort != "" {
		meta = append(meta, styles.On(theme.FgSubtle, surface).Render(styles.Glyph.Diamond+sanitizeTerminal(task.Effort)))
	}
	return meta
}

// compactDue drops the "overdue · " prefix the pill spells out in full.
func compactDue(label string) string {
	if _, rest, found := strings.Cut(label, "· "); found {
		return rest
	}
	return label
}

func visibleCardStart(lines, owners []string, selected, height int) int {
	if len(lines) <= height || height <= 0 {
		return 0
	}
	seen, selectedLine := -1, 0
	lastOwner := ""
	for i, owner := range owners {
		if owner != "" && owner != lastOwner {
			seen++
			if seen == selected {
				selectedLine = i
				break
			}
		}
		lastOwner = owner
	}
	return min(max(selectedLine-1, 0), len(lines)-height)
}

// joinColumns lays the panels out side by side with a Canvas gutter and the
// page margin of spec section 2.5, and moves every hit region onto the frame.
func joinColumns(styles *theme.Styles, columns []renderedColumn, layout boardLayout, width int) ([]string, []boardHit) {
	if len(columns) == 0 {
		return nil, nil
	}
	canvas := styles.Board.Canvas
	gutter := styles.Metrics.ColumnGutter
	height := len(columns[0].lines)
	lines := make([]string, height)
	for row := range lines {
		line := pad(canvas, layout.margin)
		for index, column := range columns {
			if index > 0 {
				line += pad(canvas, gutter)
			}
			if row < len(column.lines) {
				line += column.lines[row]
			}
		}
		lines[row] = fillRow(canvas, fitLine(line, width), width)
	}
	hits := make([]boardHit, 0)
	x := layout.margin
	for index, column := range columns {
		if index > 0 {
			x += gutter
		}
		for _, hit := range column.hits {
			hit.x0 += x
			hit.x1 = min(hit.x1+x, width)
			hits = append(hits, hit)
		}
		x += layout.widths[index]
	}
	return lines, hits
}

func boardMouseHandler(hits []boardHit, active ...bool) func(tea.MouseMsg) tea.Cmd {
	pointerActive := len(active) > 0 && active[0]
	return func(message tea.MouseMsg) tea.Cmd {
		mouse := message.Mouse()
		if _, wheel := message.(tea.MouseWheelMsg); wheel {
			delta := 0
			switch mouse.Button {
			case tea.MouseWheelUp:
				delta = -3
			case tea.MouseWheelDown:
				delta = 3
			default:
				return nil
			}
			for i := len(hits) - 1; i >= 0; i-- {
				hit := hits[i]
				if hit.kind != boardHitDefault || hit.taskID != "" ||
					mouse.X < hit.x0 || mouse.X >= hit.x1 || mouse.Y < hit.y0 || mouse.Y >= hit.y1 {
					continue
				}
				offset := min(max(hit.scroll+delta, 0), hit.maxScroll)
				if offset == hit.scroll {
					return nil
				}
				return func() tea.Msg { return boardColumnScrolledMsg{status: hit.status, offset: offset} }
			}
			return nil
		}
		if _, release := message.(tea.MouseReleaseMsg); release {
			if mouse.Button == tea.MouseLeft || (mouse.Button == tea.MouseNone && pointerActive) {
				return func() tea.Msg { return boardPointerUpMsg{} }
			}
			return nil
		}
		if mouse.Button != tea.MouseLeft {
			return nil
		}
		var matched *boardHit
		var dragAnchor *boardHit
		for i := len(hits) - 1; i >= 0; i-- {
			hit := hits[i]
			if mouse.X < hit.x0 || mouse.X >= hit.x1 || mouse.Y < hit.y0 || mouse.Y >= hit.y1 {
				continue
			}
			if matched == nil {
				matched = &hit
			}
			if dragAnchor == nil && hit.kind == boardHitDefault {
				dragAnchor = &hit
			}
		}
		switch message.(type) {
		case tea.MouseClickMsg:
			if matched == nil {
				return nil
			}
			switch matched.kind {
			case boardHitFilterText:
				return func() tea.Msg { return filterTextClickedMsg{} }
			case boardHitFilterLabel:
				return func() tea.Msg { return filterLabelClickedMsg{tag: matched.tag} }
			case boardHitFilterClear:
				return func() tea.Msg { return filterClearClickedMsg{} }
			case boardHitFooterAction:
				return func() tea.Msg { return boardFooterClickedMsg{key: matched.key} }
			}
			if matched.taskID != "" {
				return func() tea.Msg { return boardPointerDownMsg{taskID: matched.taskID} }
			}
			return func() tea.Msg { return boardColumnClickedMsg{status: matched.status} }
		case tea.MouseMotionMsg:
			if dragAnchor != nil {
				return func() tea.Msg {
					return boardPointerMoveMsg{status: dragAnchor.status, beforeTaskID: dragAnchor.taskID}
				}
			}
		}
		return nil
	}
}

func boardMouseHandlerWithFeedback(hits []boardHit, active bool, state pointer.State) func(tea.MouseMsg) tea.Cmd {
	base := boardMouseHandler(hits, active)
	var controls pointer.Map
	gesture := state.Active()
	for _, hit := range hits {
		id := boardHitControlID(hit)
		if id == "" {
			continue
		}
		if state.IsPressed(id) {
			gesture = true
		}
		message := boardControlMessage(hit)
		controls.AddControl(id, pointer.Rect{X0: hit.x0, Y0: hit.y0, X1: hit.x1, Y1: hit.y1}, func(pointer.Point) tea.Msg {
			return message
		})
	}
	controlHandler := controls.Handler()
	controlHit := func(mouse tea.Mouse) bool {
		for index := len(hits) - 1; index >= 0; index-- {
			hit := hits[index]
			if boardHitControlID(hit) != "" && mouse.X >= hit.x0 && mouse.X < hit.x1 && mouse.Y >= hit.y0 && mouse.Y < hit.y1 {
				return true
			}
		}
		return false
	}
	return func(message tea.MouseMsg) tea.Cmd {
		mouse := message.Mouse()
		switch message.(type) {
		case tea.MouseClickMsg:
			if controlHit(mouse) {
				gesture = true
				return controlHandler(message)
			}
		case tea.MouseMotionMsg:
			if gesture {
				return controlHandler(message)
			}
		case tea.MouseReleaseMsg:
			if gesture {
				gesture = false
				if command := controlHandler(message); command != nil {
					return command
				}
				return pointer.Cancel()
			}
		}
		return base(message)
	}
}

func boardControlMessage(hit boardHit) tea.Msg {
	switch hit.kind {
	case boardHitFooterAction:
		return boardFooterClickedMsg{key: hit.key}
	case boardHitFilterText:
		return filterTextClickedMsg{}
	case boardHitFilterLabel:
		return filterLabelClickedMsg{tag: hit.tag}
	case boardHitFilterClear:
		return filterClearClickedMsg{}
	default:
		return boardColumnClickedMsg{status: hit.status}
	}
}

func tasksInStatus(current board.Board, status board.Status) []board.Task {
	tasks := make([]board.Task, 0)
	for _, task := range current.Tasks {
		if task.Status == status {
			tasks = append(tasks, task)
		}
	}
	return tasks
}

func statusIndex(status board.Status) int {
	for i, candidate := range boardStatuses {
		if candidate == status {
			return i
		}
	}
	return 0
}

func statusLabel(status board.Status) string {
	return map[board.Status]string{
		board.StatusTodo:      "TO DO",
		board.StatusDoing:     "DOING",
		board.StatusDone:      "DONE",
		board.StatusCancelled: "CANCELLED",
	}[status]
}

const day = 24 * time.Hour

func ageChip(task board.Task, now time.Time) string {
	if task.Status == board.StatusDone {
		return "shipped"
	}
	reference := task.CreatedAt
	if task.Status == board.StatusDoing {
		reference = task.MovedAt
	}
	elapsed := now.Sub(reference)
	if elapsed < 0 {
		elapsed = 0
	}
	if elapsed < day {
		if task.Status == board.StatusDoing {
			return fmt.Sprintf("%dh here", max(1, int(elapsed/time.Hour)))
		}
		return "new"
	}
	suffix := "old"
	if task.Status == board.StatusDoing {
		suffix = "here"
	}
	return fmt.Sprintf("%dd %s", int(elapsed/day), suffix)
}

func dueChip(due string, now time.Time) (string, bool) {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	dueDate, err := time.Parse("2006-01-02", due)
	if err != nil {
		return due, false
	}
	days := int(dueDate.Sub(today) / day)
	switch {
	case days == 0:
		return "today", false
	case days == 1:
		return "tomorrow", false
	case days > 1:
		return fmt.Sprintf("in %dd", days), false
	default:
		return fmt.Sprintf("overdue · %dd", -days), true
	}
}

// pad renders width cells of one shade tier, so a padded row carries its
// background all the way to the edge instead of punching a hole in it.
func pad(style lipgloss.Style, width int) string {
	if width <= 0 {
		return ""
	}
	return style.Render(strings.Repeat(" ", width))
}

// fillRow right-pads already-styled content to an exact frame width.
func fillRow(style lipgloss.Style, content string, width int) string {
	return content + pad(style, width-ansi.StringWidth(content))
}

func fitLine(line string, width int) string {
	return ansi.Truncate(line, max(width, 0), "")
}

func padLine(line string, width int, fill string) string {
	line = fitLine(line, width)
	return line + strings.Repeat(fill, max(width-ansi.StringWidth(line), 0))
}
