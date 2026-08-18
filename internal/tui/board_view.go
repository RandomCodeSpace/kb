package tui

import (
	"fmt"
	"image/color"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/board"
)

const wideBoardWidth = 100

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
	showCancelled bool
}

type boardCardClickedMsg struct{ taskID string }
type boardColumnClickedMsg struct{ status board.Status }
type filterTextClickedMsg struct{}
type filterLabelClickedMsg struct{ tag string }
type filterClearClickedMsg struct{}
type boardPointerDownMsg struct{ taskID string }
type boardPointerMoveMsg struct {
	status       board.Status
	beforeTaskID string
}
type boardPointerUpMsg struct{}

type boardHitKind uint8

const (
	boardHitDefault boardHitKind = iota
	boardHitFilterText
	boardHitFilterLabel
	boardHitFilterClear
)

type boardHit struct {
	x0, x1 int
	y0, y1 int
	status board.Status
	taskID string
	kind   boardHitKind
	tag    string
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
		return boardToggledCancelled
	case "left", "h", "shift+tab":
		s.moveColumn(-1, current)
		return boardChanged
	case "right", "l", "tab":
		s.moveColumn(1, current)
		return boardChanged
	case "up", "k":
		if s.rows[s.column] > 0 {
			s.rows[s.column]--
		}
		return boardChanged
	case "down", "j":
		count := taskCount(current, boardStatuses[s.column])
		if s.rows[s.column]+1 < count {
			s.rows[s.column]++
		}
		return boardChanged
	case "1", "2", "3", "4":
		at := int(key[0] - '1')
		if at < len(s.visibleStatuses()) {
			s.column = at
			s.clampRow(current)
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
	width := max(m.width, 1)
	height := max(m.height, 8)
	title := strings.TrimSpace(m.board.Title)
	if title == "" {
		title = "Board"
	}
	header := fitLine(fmt.Sprintf("kb / %s / %s", title, m.user), width)
	if shipped := m.shippedCount(); shipped > 0 {
		header = fitLine(fmt.Sprintf("kb / %s / %s / ×%d shipped today", title, m.user, shipped), width)
	}
	filterLine, filterHits := m.renderFilterBar(width)

	statuses := m.boardView.visibleStatuses()
	if width < wideBoardWidth {
		statuses = statuses[m.boardView.column : m.boardView.column+1]
	}
	bodyHeight := height - 4
	columnWidths := splitWidths(width, len(statuses))
	columns := make([]renderedColumn, 0, len(statuses))
	for i, status := range statuses {
		columns = append(columns, m.renderBoardColumn(status, columnWidths[i], bodyHeight))
	}
	body, hits := joinColumns(columns)
	for i := range hits {
		hits[i].y0 += 3
		hits[i].y1 += 3
	}
	hits = append(filterHits, hits...)

	state := "ready"
	moveActive := m.move.lifted != nil || m.move.saving
	movePriority := moveActive || m.move.notice
	showMoveStatus := movePriority && m.move.status != ""
	if showMoveStatus {
		state = sanitizeTerminal(m.move.status)
	} else if m.actionNotice && m.actionStatus != "" {
		state = sanitizeTerminal(m.actionStatus)
	} else if m.loadErr != nil {
		state = "error: " + m.loadErr.Error()
	} else if m.pollErr != nil {
		state = "error: " + m.pollErr.Error()
	} else if m.preferenceErr != nil {
		state = "error: " + m.preferenceErr.Error()
	} else if m.move.status != "" {
		state = sanitizeTerminal(m.move.status)
	} else if m.loading || (m.watcher != nil && !m.haveVersion) {
		state = "loading board..."
	}
	cancelled := "off"
	if m.boardView.showCancelled {
		cancelled = "on"
	}
	help := "j/k cards | h/l/tab columns | 1-4 jump | c cancelled:" + cancelled + " | q quit"
	if m.editor.Enabled() {
		help = "n new | e edit | " + help
	}
	if showMoveStatus || m.actionNotice || (m.move.status != "" && m.loadErr == nil && m.pollErr == nil && m.preferenceErr == nil) {
		footer := fitLine(state, width)
		return strings.Join([]string{header, filterLine, body, footer}, "\n"), hits
	}
	if m.settingsNew != nil {
		footer := settingsBoardFooter(state, cancelled, m.editor.Enabled(), m.adr.Enabled(), width)
		return strings.Join([]string{header, filterLine, body, footer}, "\n"), hits
	}
	footer := fitLine(state+" | "+help, width)
	return strings.Join([]string{header, filterLine, body, footer}, "\n"), hits
}

func (m Model) renderFilterBar(width int) (string, []boardHit) {
	width = max(width, 1)
	hits := make([]boardHit, 0, 2+len(m.filterLabels()))
	lines := [2][]string{}
	appendPart := func(row int, part string, kind boardHitKind, tag string) {
		x := ansi.StringWidth(strings.Join(lines[row], ""))
		if len(lines[row]) > 0 {
			lines[row] = append(lines[row], " | ")
			x += 3
		}
		start := x
		lines[row] = append(lines[row], part)
		x += ansi.StringWidth(part)
		if kind != boardHitDefault && start < width {
			hits = append(hits, boardHit{x0: start, x1: min(x, width), y0: row + 1, y1: row + 2, kind: kind, tag: tag})
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
	appendLabel := func(tag string) {
		marker := "+"
		if m.filter.hasTag(tag) {
			marker = "x"
		}
		label := "[" + marker + " " + sanitizeTerminal(tag) + "]"
		if tag == focusTag {
			label = ">" + label + "<"
		}
		appendPart(0, label, boardHitFilterLabel, tag)
	}
	if focusTag != "" {
		appendLabel(focusTag)
	}
	appendPart(0, text, boardHitFilterText, "")
	if m.filter.active() {
		appendPart(1, fmt.Sprintf("%d of %d cards", len(m.filteredBoard().Tasks), len(m.board.Tasks)), boardHitDefault, "")
	}
	appendLabelOnControls := func(tag string) {
		marker := "+"
		if m.filter.hasTag(tag) {
			marker = "x"
		}
		appendPart(1, "["+marker+" "+sanitizeTerminal(tag)+"]", boardHitFilterLabel, tag)
	}
	for _, tag := range labels {
		if tag != focusTag && m.filter.hasTag(tag) {
			appendLabelOnControls(tag)
		}
	}
	if m.filter.active() {
		appendPart(1, "[clear]", boardHitFilterClear, "")
	}
	for _, tag := range labels {
		if tag != focusTag && !m.filter.hasTag(tag) {
			appendLabelOnControls(tag)
		}
	}
	return strings.Join([]string{
		fitLine(strings.Join(lines[0], ""), width),
		fitLine(strings.Join(lines[1], ""), width),
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
	minimumStateWidth := min(ansi.StringWidth(state), 5)
	for _, candidate := range candidates {
		help := strings.Join(candidate, " | ")
		stateWidth := width - ansi.StringWidth(help) - 3
		if stateWidth < minimumStateWidth {
			continue
		}
		return fitLine(state, stateWidth) + " | " + help
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
	if width < 3 {
		label := statusLabel(status)
		lines := make([]string, height)
		lines[0] = fitLine(label, width)
		return renderedColumn{lines: lines, hits: []boardHit{{x1: width, y1: height, status: status}}}
	}
	inner := width - 2
	tasks := tasksInStatus(m.filteredBoard(), status)
	focused := m.boardView.column == statusIndex(status)
	heading := fmt.Sprintf("%d %s  %d", statusIndex(status)+1, statusLabel(status), len(tasks))
	if focused {
		heading = "[" + heading + "]"
	}
	lines := []string{"┌" + padLine(heading, inner, "─") + "┐"}
	hits := []boardHit{{x1: width, y1: height, status: status}}

	contentHeight := max(height-2, 0)
	cardLines, owners, labelSpans := m.renderTaskLines(tasks, status, inner)
	start := visibleCardStart(cardLines, owners, m.boardView.rows[statusIndex(status)], contentHeight)
	for row := 0; row < contentHeight; row++ {
		source := start + row
		line := ""
		if source < len(cardLines) {
			line = cardLines[source]
		}
		lines = append(lines, "│"+padLine(line, inner, " ")+"│")
		if source < len(owners) && owners[source] != "" {
			hits = append(hits, boardHit{x1: width, y0: row + 1, y1: row + 2, status: status, taskID: owners[source]})
		}
		if source < len(labelSpans) {
			for _, span := range labelSpans[source] {
				hits = append(hits, boardHit{
					x0: 1 + span.x0, x1: min(1+span.x1, width-1),
					y0: row + 1, y1: row + 2, status: status,
					kind: boardHitFilterLabel, tag: span.tag,
				})
			}
		}
	}
	lines = append(lines, "└"+strings.Repeat("─", inner)+"┘")
	return renderedColumn{lines: lines, hits: hits}
}

func (m Model) renderTaskLines(tasks []board.Task, status board.Status, width int) ([]string, []string, [][]labelSpan) {
	if len(tasks) == 0 {
		return []string{"(empty)"}, []string{""}, [][]labelSpan{nil}
	}
	lines := make([]string, 0, len(tasks)*3)
	owners := make([]string, 0, len(tasks)*3)
	spans := make([][]labelSpan, 0, len(tasks)*3)
	selected := m.boardView.rows[statusIndex(status)]
	for i, task := range tasks {
		marker := "  "
		if m.boardView.column == statusIndex(status) && i == selected {
			marker = "› "
		}
		if m.move.lifted != nil && task.ID == m.move.lifted.taskID {
			marker = "↕ "
		}
		first := cardHeading(task, m.now())
		for lineIndex, line := range wrapTokens(first, max(width-2, 1)) {
			prefix := "  "
			if lineIndex == 0 {
				prefix = marker
			}
			lines = append(lines, prefix+line)
			owners = append(owners, task.ID)
			spans = append(spans, nil)
		}
		metaLines, metaSpans := wrapMeta(cardMetaEntries(task, m.now()), max(width-2, 1))
		for lineIndex, line := range metaLines {
			lines = append(lines, "  "+line)
			owners = append(owners, task.ID)
			lineSpans := make([]labelSpan, len(metaSpans[lineIndex]))
			for spanIndex, span := range metaSpans[lineIndex] {
				span.x0 += 2
				span.x1 += 2
				lineSpans[spanIndex] = span
			}
			spans = append(spans, lineSpans)
		}
		if i+1 < len(tasks) {
			lines = append(lines, "")
			owners = append(owners, "")
			spans = append(spans, nil)
		}
	}
	return lines, owners, spans
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

func joinColumns(columns []renderedColumn) (string, []boardHit) {
	if len(columns) == 0 {
		return "", nil
	}
	height := len(columns[0].lines)
	lines := make([]string, height)
	hits := make([]boardHit, 0)
	x := 0
	for columnIndex, column := range columns {
		if columnIndex > 0 {
			for row := range lines {
				lines[row] += " "
			}
			x++
		}
		for row := range lines {
			if row < len(column.lines) {
				lines[row] += column.lines[row]
			}
		}
		for _, hit := range column.hits {
			hit.x0 += x
			hit.x1 += x
			hits = append(hits, hit)
		}
		if len(column.lines) > 0 {
			x += ansi.StringWidth(column.lines[0])
		}
	}
	return strings.Join(lines, "\n"), hits
}

func boardMouseHandler(hits []boardHit, active ...bool) func(tea.MouseMsg) tea.Cmd {
	pointerActive := len(active) > 0 && active[0]
	return func(message tea.MouseMsg) tea.Cmd {
		mouse := message.Mouse()
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

func cardHeading(task board.Task, now time.Time) []string {
	tokens := make([]string, 0, 5)
	if task.Emoji != "" {
		tokens = append(tokens, task.Emoji)
	}
	tokens = append(tokens, task.Title)
	if task.Seq > 0 {
		tokens = append(tokens, fmt.Sprintf("#%d", task.Seq))
	}
	tokens = append(tokens, ageChip(task, now))
	return tokens
}

type metaEntry struct {
	text string
	tag  string
}

func cardMetaEntries(task board.Task, now time.Time) []metaEntry {
	tokens := []metaEntry{{text: priorityChip(task.Prio)}}
	if task.Blocked {
		tokens = append(tokens, metaEntry{text: chip("⛔ blocked", lipgloss.Color("#ffb020"))})
	}
	if task.Due != "" {
		label, overdue := dueChip(task.Due, now)
		color := lipgloss.Color("#e2f4e0")
		if overdue {
			color = lipgloss.Color("#ffe0dc")
		}
		tokens = append(tokens, metaEntry{text: chip(label, color)})
	}
	if task.Effort != "" {
		tokens = append(tokens, metaEntry{text: "[" + task.Effort + "]"})
	}
	for _, tag := range task.Tags {
		tokens = append(tokens, metaEntry{text: labelChip(tag), tag: tag})
	}
	return tokens
}

func wrapMeta(entries []metaEntry, width int) ([]string, [][]labelSpan) {
	if width <= 0 {
		return []string{""}, [][]labelSpan{nil}
	}
	lines := make([]string, 0, 2)
	spans := make([][]labelSpan, 0, 2)
	line := ""
	lineSpans := make([]labelSpan, 0)
	flush := func() {
		lines = append(lines, line)
		spans = append(spans, lineSpans)
		line = ""
		lineSpans = nil
	}
	for _, entry := range entries {
		if entry.text == "" {
			continue
		}
		separator := ""
		if line != "" {
			separator = " "
		}
		if line != "" && ansi.StringWidth(line+separator+entry.text) > width {
			flush()
			separator = ""
		}
		start := ansi.StringWidth(line) + ansi.StringWidth(separator)
		visible := ansi.Truncate(entry.text, max(width-start, 0), "")
		line += separator + visible
		if entry.tag != "" && ansi.StringWidth(visible) > 0 {
			lineSpans = append(lineSpans, labelSpan{x0: start, x1: start + ansi.StringWidth(visible), tag: entry.tag})
		}
	}
	if line != "" || len(lines) == 0 {
		flush()
	}
	return lines, spans
}

var priorityColors = map[int]color.Color{
	1: lipgloss.Color("#ff5a48"),
	2: lipgloss.Color("#ffb020"),
	3: lipgloss.Color("#4f8ef7"),
	4: lipgloss.Color("#b8bdc7"),
}

func priorityChip(priority int) string {
	if priority < 1 || priority > 4 {
		priority = 3
	}
	return lipgloss.NewStyle().Foreground(priorityColors[priority]).Bold(true).Render(fmt.Sprintf("P%d", priority))
}

func chip(label string, fill color.Color) string {
	return lipgloss.NewStyle().Background(fill).Foreground(lipgloss.Color("#20242c")).Render("[" + label + "]")
}

var labelColors = [...]color.Color{
	lipgloss.Color("#ff7b54"),
	lipgloss.Color("#4f8ef7"),
	lipgloss.Color("#3f9d58"),
	lipgloss.Color("#b98af7"),
	lipgloss.Color("#ffb020"),
}

func labelColor(tag string) color.Color {
	first, _ := utf8.DecodeRuneInString(tag)
	if first == utf8.RuneError && tag == "" {
		first = 0
	}
	units := 0
	for _, r := range tag {
		units += utf16.RuneLen(r)
	}
	return labelColors[(units+int(first))%len(labelColors)]
}

func labelChip(tag string) string {
	color := labelColor(tag)
	key, value, scoped := strings.Cut(tag, "::")
	if !scoped || key == "" || value == "" {
		return chip("#"+tag, color)
	}
	keyPart := lipgloss.NewStyle().Background(lipgloss.Color("#20242c")).Foreground(lipgloss.Color("#ffffff")).Render("[" + key + ":")
	valuePart := lipgloss.NewStyle().Background(color).Foreground(lipgloss.Color("#20242c")).Render(value + "]")
	return keyPart + valuePart
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

func wrapTokens(tokens []string, width int) []string {
	if width <= 0 {
		return []string{""}
	}
	lines := make([]string, 0, 2)
	line := ""
	for _, token := range tokens {
		if token == "" {
			continue
		}
		candidate := token
		if line != "" {
			candidate = line + " " + token
		}
		if ansi.StringWidth(candidate) <= width {
			line = candidate
			continue
		}
		if line != "" {
			lines = append(lines, line)
		}
		line = ansi.Truncate(token, width, "")
	}
	if line != "" || len(lines) == 0 {
		lines = append(lines, line)
	}
	return lines
}

func fitLine(line string, width int) string {
	return ansi.Truncate(line, max(width, 0), "")
}

func padLine(line string, width int, fill string) string {
	line = fitLine(line, width)
	return line + strings.Repeat(fill, max(width-ansi.StringWidth(line), 0))
}
