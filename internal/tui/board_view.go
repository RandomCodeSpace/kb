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
	"github.com/RandomCodeSpace/kb/internal/project"
	"github.com/RandomCodeSpace/kb/internal/tui/action"
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

// boardTaskAnchor is the durable position vocabulary for a board column. The
// ordinal is only a deterministic fallback when the identity disappears;
// IntraRow is used by scroll anchors and is clamped against current geometry.
type boardTaskAnchor struct {
	TaskID    string
	IndexHint int
	IntraRow  int
}

// boardViewState is the complete read-only board interaction state. Keeping it
// together gives detail overlays one stable selectedTask seam without making
// board rendering part of the root model's refresh machinery.
type boardViewState struct {
	column        int
	rows          [len(boardStatuses)]int // cached ordinals; task identity is authoritative
	cursors       [len(boardStatuses)]boardTaskAnchor
	scrolls       [len(boardStatuses)]int // compatibility/raw fallback until an anchor exists
	scrollAnchors [len(boardStatuses)]boardTaskAnchor
	manualScroll  [len(boardStatuses)]bool
	showCancelled bool
}

type boardCardClickedMsg struct{ taskID string }
type boardColumnClickedMsg struct{ status board.Status }
type filterTextClickedMsg struct{}
type filterLabelClickedMsg struct{ tag string }
type filterClearClickedMsg struct{}
type filterProjectClickedMsg struct{}
type boardFooterClickedMsg struct{ key string }
type boardPointerDownMsg struct{ taskID string }
type boardPointerMoveMsg struct {
	status       board.Status
	beforeTaskID string
}
type boardPointerUpMsg struct {
	resolved     bool
	valid        bool
	status       board.Status
	beforeTaskID string
}
type boardColumnScrolledMsg struct {
	status board.Status
	from   int
	offset int
	anchor boardTaskAnchor
	max    int
}

type boardHitKind uint8

const (
	boardHitDefault boardHitKind = iota
	boardHitFilterText
	boardHitFilterLabel
	boardHitFilterClear
	boardHitFooterAction
	boardHitColumnHeading
	boardHitProject
)

type boardHit struct {
	x0, x1     int
	y0, y1     int
	status     board.Status
	taskID     string
	kind       boardHitKind
	tag        string
	key        string
	scroll     int
	maxScroll  int
	scrollUp   boardTaskAnchor
	scrollDown boardTaskAnchor
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

type boardNavigation interface {
	statusCount(board.Status) int
	taskAtStatus(board.Status, int) (board.Task, bool)
	ordinalForTask(board.Status, string) (int, bool)
	taskByID(string) (board.Task, bool)
}

type scannedBoardNavigation struct{ board board.Board }

func (n scannedBoardNavigation) statusCount(status board.Status) int {
	return taskCount(n.board, status)
}

func (n scannedBoardNavigation) taskAtStatus(status board.Status, ordinal int) (board.Task, bool) {
	return taskAtStatusIndex(n.board, status, ordinal)
}

func (n scannedBoardNavigation) ordinalForTask(status board.Status, id string) (int, bool) {
	return taskIndexFound(n.board, status, id)
}

func (n scannedBoardNavigation) taskByID(id string) (board.Task, bool) {
	for _, task := range n.board.Tasks {
		if task.ID == id {
			return task, true
		}
	}
	return board.Task{}, false
}

func (s *boardViewState) handleKey(key string, current board.Board) boardAction {
	return s.handleNavigationKey(key, scannedBoardNavigation{current})
}

func (s *boardViewState) handleProjectionKey(key string, current *renderProjection) boardAction {
	return s.handleNavigationKey(key, current)
}

func (s *boardViewState) handleNavigationKey(key string, current boardNavigation) boardAction {
	s.restoreColumnCursorFrom(current, s.column)
	switch key {
	case "c":
		s.showCancelled = !s.showCancelled
		if !s.showCancelled && s.column == len(boardStatuses)-1 {
			s.column--
		}
		s.clampRowFrom(current)
		return boardToggledCancelled
	case "left", "h", "shift+tab":
		s.moveColumnFrom(-1, current)
		return boardChanged
	case "right", "l", "tab":
		s.moveColumnFrom(1, current)
		return boardChanged
	case "up", "k":
		if s.rows[s.column] > 0 {
			s.rows[s.column]--
		}
		s.setCursorAtFrom(current, s.column, s.rows[s.column])
		s.manualScroll[s.column] = false
		return boardChanged
	case "down", "j":
		count := current.statusCount(boardStatuses[s.column])
		if s.rows[s.column]+1 < count {
			s.rows[s.column]++
		}
		s.setCursorAtFrom(current, s.column, s.rows[s.column])
		s.manualScroll[s.column] = false
		return boardChanged
	case "1", "2", "3", "4":
		at := int(key[0] - '1')
		if at < len(s.visibleStatuses()) {
			s.column = at
			s.clampRowFrom(current)
			return boardChanged
		}
	}
	return boardUnchanged
}

func (s *boardViewState) moveColumn(delta int, current board.Board) {
	s.moveColumnFrom(delta, scannedBoardNavigation{current})
}

func (s *boardViewState) moveColumnFrom(delta int, current boardNavigation) {
	count := len(s.visibleStatuses())
	s.column = (s.column + delta + count) % count
	s.clampRowFrom(current)
}

func (s *boardViewState) clampRow(current board.Board) {
	s.clampRowFrom(scannedBoardNavigation{current})
}

func (s *boardViewState) clampRowFrom(current boardNavigation) {
	s.restoreColumnCursorFrom(current, s.column)
}

func (s *boardViewState) focusColumn(status board.Status, current board.Board) {
	s.focusColumnFrom(status, scannedBoardNavigation{current})
}

func (s *boardViewState) focusProjectionColumn(status board.Status, current *renderProjection) {
	s.focusColumnFrom(status, current)
}

func (s *boardViewState) focusColumnFrom(status board.Status, current boardNavigation) {
	for i, candidate := range s.visibleStatuses() {
		if candidate == status {
			s.column = i
			s.clampRowFrom(current)
			return
		}
	}
}

func (s *boardViewState) focusTask(current board.Board, id string) bool {
	return s.focusTaskWithScroll(current, id, true)
}

func (s *boardViewState) focusTaskWithScroll(current board.Board, id string, resetScroll bool) bool {
	return s.focusTaskWithScrollFrom(scannedBoardNavigation{current}, id, resetScroll)
}

func (s *boardViewState) focusProjectionTask(current *renderProjection, id string) bool {
	return s.focusTaskWithScrollFrom(current, id, true)
}

func (s *boardViewState) focusTaskWithScrollFrom(current boardNavigation, id string, resetScroll bool) bool {
	task, ok := current.taskByID(id)
	if !ok || task.Status == board.StatusCancelled && !s.showCancelled {
		return false
	}
	for i, status := range boardStatuses {
		if status != task.Status {
			continue
		}
		at, ok := current.ordinalForTask(task.Status, id)
		if !ok {
			return false
		}
		s.column = i
		s.rows[i] = at
		s.cursors[i] = boardTaskAnchor{TaskID: id, IndexHint: at}
		if resetScroll {
			s.scrollAnchors[i] = boardTaskAnchor{TaskID: id, IndexHint: at}
			s.manualScroll[i] = false
		}
		return true
	}
	return false
}

func (s *boardViewState) adoptBoard(previous, current board.Board) {
	selected, hadSelection := s.selectedTask(previous)
	for column, status := range boardStatuses {
		s.captureColumnAnchors(previous, column, status)
		s.restoreColumnCursor(current, column)
		s.restoreColumnScroll(current, column)
	}
	if hadSelection {
		for _, task := range current.Tasks {
			if task.ID == selected.ID && s.focusTaskWithScroll(current, selected.ID, task.Status != selected.Status) {
				return
			}
		}
	}
	s.normalizeSelection(current)
}

// normalizeSelection keeps refresh focus on a selectable visible card when
// one exists. Ordinary column navigation may still focus an empty column.
func (s *boardViewState) normalizeSelection(current board.Board) {
	s.normalizeSelectionFrom(scannedBoardNavigation{current})
}

func (s *boardViewState) normalizeSelectionFrom(current boardNavigation) {
	visible := s.visibleStatuses()
	if current.statusCount(boardStatuses[s.column]) > 0 {
		s.clampRowFrom(current)
		return
	}
	for offset := 1; offset < len(visible); offset++ {
		column := (s.column + offset) % len(visible)
		if current.statusCount(visible[column]) == 0 {
			continue
		}
		s.column = column
		s.clampRowFrom(current)
		return
	}
	s.clampRowFrom(current)
}

func (s boardViewState) selectedTask(current board.Board) (board.Task, bool) {
	return s.selectedTaskFrom(scannedBoardNavigation{current})
}

func (s boardViewState) selectedProjectionTask(current *renderProjection) (board.Task, bool) {
	return s.selectedTaskFrom(current)
}

func (s boardViewState) selectedTaskFrom(current boardNavigation) (board.Task, bool) {
	status := boardStatuses[s.column]
	if id := s.cursors[s.column].TaskID; id != "" {
		if at, ok := current.ordinalForTask(status, id); ok && at == s.rows[s.column] {
			return current.taskAtStatus(status, at)
		}
	}
	return current.taskAtStatus(status, s.rows[s.column])
}

func (s *boardViewState) captureColumnAnchors(current board.Board, column int, status board.Status) {
	if at, ok := taskIndexFound(current, status, s.cursors[column].TaskID); ok {
		s.rows[column] = at
		s.cursors[column].IndexHint = at
	} else if task, ok := taskAtStatusIndex(current, status, s.rows[column]); ok {
		s.cursors[column] = boardTaskAnchor{TaskID: task.ID, IndexHint: s.rows[column]}
	}
	if at, ok := taskIndexFound(current, status, s.scrollAnchors[column].TaskID); ok {
		s.scrollAnchors[column].IndexHint = at
	}
}

func (s *boardViewState) restoreColumnCursor(current board.Board, column int) {
	s.restoreColumnCursorFrom(scannedBoardNavigation{current}, column)
}

func (s *boardViewState) restoreColumnCursorFrom(current boardNavigation, column int) {
	status := boardStatuses[column]
	if at, ok := current.ordinalForTask(status, s.cursors[column].TaskID); ok {
		s.rows[column] = at
		s.cursors[column].IndexHint = at
		return
	}
	count := current.statusCount(status)
	if count == 0 {
		s.rows[column] = 0
		return
	}
	hint := s.cursors[column].IndexHint
	if s.cursors[column].TaskID == "" {
		hint = s.rows[column]
	}
	s.setCursorAtFrom(current, column, min(max(hint, 0), count-1))
}

func (s *boardViewState) restoreColumnScroll(current board.Board, column int) {
	anchor := &s.scrollAnchors[column]
	status := boardStatuses[column]
	if at, ok := taskIndexFound(current, status, anchor.TaskID); ok {
		anchor.IndexHint = at
		return
	}
	count := taskCount(current, status)
	if count == 0 || anchor.TaskID == "" {
		return
	}
	at := min(max(anchor.IndexHint, 0), count-1)
	if task, ok := taskAtStatusIndex(current, status, at); ok {
		*anchor = boardTaskAnchor{TaskID: task.ID, IndexHint: at, IntraRow: anchor.IntraRow}
	}
}

func (s *boardViewState) setCursorAt(current board.Board, column, row int) {
	s.setCursorAtFrom(scannedBoardNavigation{current}, column, row)
}

func (s *boardViewState) setCursorAtFrom(current boardNavigation, column, row int) {
	status := boardStatuses[column]
	task, ok := current.taskAtStatus(status, row)
	if !ok {
		s.rows[column] = 0
		return
	}
	s.rows[column] = row
	s.cursors[column] = boardTaskAnchor{TaskID: task.ID, IndexHint: row}
}

func (s boardViewState) selectedIndex(tasks []board.Task, status board.Status) int {
	column := statusIndex(status)
	if id := s.cursors[column].TaskID; id != "" {
		for i, task := range tasks {
			if task.ID == id && i == s.rows[column] {
				return i
			}
		}
	}
	if len(tasks) == 0 {
		return 0
	}
	return min(max(s.rows[column], 0), len(tasks)-1)
}

func taskAtStatusIndex(current board.Board, status board.Status, want int) (board.Task, bool) {
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

func taskIndexFound(current board.Board, status board.Status, id string) (int, bool) {
	if id == "" {
		return 0, false
	}
	index := 0
	for _, task := range current.Tasks {
		if task.Status != status {
			continue
		}
		if task.ID == id {
			return index, true
		}
		index++
	}
	return 0, false
}

// selectedTask is the narrow handoff used by the card-detail overlay.
func (m Model) selectedTask() (board.Task, bool) {
	if projection := m.currentProjection(); projection != nil {
		selected, ok := m.boardView.selectedProjectionTask(projection)
		if !ok {
			return board.Task{}, false
		}
		if current, found := projection.sourceTaskByID(m.board, selected.ID); found {
			return current, true
		}
		return selected, true
	}
	return m.boardView.selectedTask(m.filteredBoard())
}

func (m *Model) handleBoardNavigationKey(key string) boardAction {
	if projection := m.currentProjection(); projection != nil {
		return m.boardView.handleProjectionKey(key, projection)
	}
	return m.boardView.handleKey(key, m.filteredBoard())
}

func (m *Model) focusBoardTask(id string) bool {
	if projection := m.currentProjection(); projection != nil {
		return m.boardView.focusProjectionTask(projection, id)
	}
	return m.boardView.focusTask(m.filteredBoard(), id)
}

func (m *Model) focusBoardColumn(status board.Status) {
	if projection := m.currentProjection(); projection != nil {
		m.boardView.focusProjectionColumn(status, projection)
		return
	}
	m.boardView.focusColumn(status, m.filteredBoard())
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
		footer = widget.Truncate(styles, state, width)
	case m.settingsNew != nil:
		footer = settingsBoardFooter(styles, state, cancelled, m.editor.Enabled(), m.adr.Enabled(), width)
		if m.issueImport.Enabled() && width >= 24 {
			footer = fitLine("i import | "+footer, width)
		}
		hits = append(hits, boardFooterHits(footer, height-1, width)...)
	default:
		footer = fitLine(boardStateLadder(styles, state, help, width), width)
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
// carries (spec section 1.5), across the three first-class states of section
// 10.8: busy, failed, and settled.
//
// The busy rows come first because the operation in flight is what the segment
// is describing; the move notice it displaces is the summary of the operation
// that just finished. Failure takes the alert mark of section 10.8.5 and the
// board tiers keep StatusDanger, which is the pair section 10.9 call 12
// measured at 5.75 against Surface.
func (m Model) boardState() (string, theme.Slot) {
	moveActive := m.move.lifted != nil || m.move.saving
	switch {
	case m.move.saving:
		return m.busyState()
	case (moveActive || m.move.notice) && m.move.status != "":
		// move.statusError is written in five places and was read in none, so a
		// failed write was indistinguishable from a successful drop. It is read
		// here (spec section 10.8.1, finding under "Board, card move").
		if m.move.statusError {
			return m.alertState(m.move.status)
		}
		return sanitizeTerminal(m.move.status), theme.StatusWarn
	case m.action.busy:
		return m.busyState()
	case m.actionNotice && m.actionStatus != "":
		if m.actionStatusError {
			return m.alertState(m.actionStatus)
		}
		return sanitizeTerminal(m.actionStatus), theme.StatusOK
	case m.loadErr != nil:
		return m.alertState(m.loadErr.Error())
	case m.pollErr != nil:
		return m.alertState(m.pollErr.Error())
	case m.preferenceErr != nil:
		return m.alertState(m.preferenceErr.Error())
	case m.move.status != "":
		return sanitizeTerminal(m.move.status), theme.StatusWarn
	case m.boardLoading():
		return m.busyState()
	default:
		return "ready", theme.StatusOK
	}
}

// busyState is the plain tier in the state segment: frame, BusyGap, label. The
// frame is plain text because the segment is rendered as one styled run and a
// frame with a color of its own would drop the segment for every cell after it.
func (m Model) busyState() (string, theme.Slot) {
	label := m.boardBusyLabel()
	frame := m.busyFrameText()
	if frame == "" {
		return label, theme.FgSubtle
	}
	return frame + strings.Repeat(" ", m.themeStyles().Metrics.BusyGap) + label, theme.FgSubtle
}

// alertState is the error row of spec section 10.8.5 at board scope: the alert
// mark, then the sanitized message. The mark is what tells an ASCII terminal
// that the row failed, where the hue does not survive.
func (m Model) alertState(message string) (string, theme.Slot) {
	return m.themeStyles().Glyph.Alert + " " + sanitizeTerminal(message), theme.StatusDanger
}

// renderTopBar is the Canvas row of spec section 2.1: the accent rail and
// wordmark, the board identity, and the shipped counter in the success hue.
//
// The leading three columns are the per-project accent of spec sections 10.6.4
// and 10.7.3: a rail names the thing to its right, so the rail and the bold
// wordmark read as one mark. Neither a ramp nor a filled pill is used here. A
// two-cell text run cannot carry a legible gradient, and a fixed brand ramp
// would overwrite the one hue on the row that identifies which board this is.
// The launch screen is the place the mark gets to be a mark; the top bar is the
// place identity gets to be a color.
func (m Model) renderTopBar(styles *theme.Styles, width int) string {
	title := strings.TrimSpace(sanitizeTerminal(m.board.Title))
	if title == "" {
		title = "Board"
	}
	accent := theme.AccentSlot(title)
	line := styles.On(accent, theme.Canvas).Render(styles.Glyph.Rail) +
		styles.OnBold(accent, theme.Canvas).Render("kb") +
		styles.Board.TopBar.Render(" / "+title+" / "+sanitizeTerminal(m.user))
	if shipped := m.shippedCount(); shipped > 0 {
		// The space after the multiplication sign is the section 10.4.1
		// adjacency rule: U+00D7 is East Asian Ambiguous, so a digit written
		// straight after it is drawn over a glyph the font took more than its
		// advertised cell to draw.
		line += styles.On(theme.StatusOK, theme.Canvas).Render(fmt.Sprintf(" / × %d shipped today", shipped))
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
	case boardHitProject:
		return pointer.ControlID("board-filter:project")
	case boardHitColumnHeading:
		return pointer.ControlID("board-column:" + string(hit.status))
	}
	return ""
}

func boardCardLabelControlID(taskID, tag string) pointer.ControlID {
	return pointer.ControlID("board-card-label:" + taskID + ":" + tag)
}

// renderFilterBar draws the two toolbar rows on the Canvas tier with the filter
// field on Surface (spec section 2.1). The labels are the section 3.6 pills the
// board cards carry (issue #206), which makes them multi-run strings: every hit
// region is measured from the rendered run with ansi.StringWidth rather than
// from a plain-text length, and appendRun takes content that has already been
// styled so a pill is not re-wrapped in a style that would flatten its fills.
func (m Model) renderFilterBar(width int) (string, []boardHit) {
	styles := m.themeStyles()
	width = max(width, 1)
	canvas := styles.Board.Canvas
	separator := styles.On(theme.FgMuted, theme.Canvas).Render(" | ")
	hits := make([]boardHit, 0, 2+len(m.filterLabels()))
	lines := [2][]string{}
	appendRun := func(row int, content string, kind boardHitKind, tag string) {
		x := ansi.StringWidth(strings.Join(lines[row], ""))
		if len(lines[row]) > 0 {
			lines[row] = append(lines[row], separator)
			x += 3
		}
		start := x
		partWidth := ansi.StringWidth(content)
		hit := boardHit{x0: start, x1: min(start+partWidth, width), y0: row + 1, y1: row + 2, kind: kind, tag: tag}
		lines[row] = append(lines[row], m.pointerState.Render(styles, boardHitControlID(hit), content))
		if kind != boardHitDefault && start < width {
			hits = append(hits, hit)
		}
	}
	appendPart := func(row int, part string, style lipgloss.Style, kind boardHitKind, tag string) {
		appendRun(row, style.Render(part), kind, tag)
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
	// A filter label is a pill with three orthogonal states: the toggle is the
	// hue-filled pill against the hue-tinted one, keyboard focus bolds the body,
	// and the pointer underlines it. None of the three changes
	// a cell count, so
	// toggling or traversing the row never reflows the toolbar (section 10.4.4).
	appendLabel := func(row int, tag string) {
		hit := boardHit{kind: boardHitFilterLabel, tag: tag}
		appendRun(row, widget.FilterLabel(styles, sanitizeTerminal(tag), theme.Canvas,
			m.filter.hasTag(tag),
			tag == focusTag,
			m.pointerState.IsHovered(boardHitControlID(hit)),
		), boardHitFilterLabel, tag)
	}
	if focusTag != "" {
		appendLabel(0, focusTag)
	}
	appendPart(0, text, formview.Selection(
		styles.On(theme.FgBase, theme.Surface),
		m.filter.focus == filterText && m.filter.mark.Active(filterMarkField),
	), boardHitFilterText, "")
	// The project switcher sits at the end of the field row: it is the same
	// axis as the filter, it carries the brand hue while the board is scoped to
	// one project, and it is the toolbar's only always-present state readout.
	projectStyle := styles.On(theme.FgMuted, theme.Canvas)
	if !m.projects.all && m.projects.name != "" {
		projectStyle = styles.OnBold(theme.Brand, theme.Canvas)
	}
	appendPart(0, "[project: "+sanitizeTerminal(m.projects.label())+"]", projectStyle, boardHitProject, "")
	if m.filter.active() {
		var visible, projected int
		if projection := m.currentProjection(); projection != nil {
			visible, projected = len(projection.board.Tasks), projection.projected
		} else {
			visible, projected = len(m.filteredBoard().Tasks), len(m.projectBoard().Tasks)
		}
		count := fmt.Sprintf("%d of %d cards", visible, projected)
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

// boardStateLadder joins the state segment to the hint ladder. The state is cut
// with the section 3.3 primitive rather than by the row's own bare truncation,
// which is spec section 10.8.5 rule 5: the board footer is one row and stays one
// row, and the message it carries is ellipsized rather than wrapped.
//
// The state is measured against the whole frame, not against what the ladder
// leaves: the ladder is what gets cut when the two do not both fit, because a
// user reading a failure needs the failure and already knows the keys.
func boardStateLadder(styles *theme.Styles, state, help string, width int) string {
	return widget.Truncate(styles, state, width) + footerSeparator + help
}

// footerSeparator is the rung separator the footer's segments are split on.
const footerSeparator = " | "

func settingsBoardFooter(styles *theme.Styles, state, cancelled string, editorEnabled, adrEnabled bool, width int) string {
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
		return widget.Truncate(styles, state, stateWidth) + footerSeparator + help
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
	if m.renderingGeometry != nil && m.renderingProjection != nil {
		column := m.renderingGeometry.columns[statusIndex(status)]
		if column.status == status && column.width == width && column.panelHeight == height && column.density == density {
			return m.renderVirtualColumn(m.renderingProjection, column)
		}
	}
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
		if meta = columnMetaLine(styles, tasks); meta != "" {
			metaRows = 1
		}
	}
	inset := metrics.ColumnPad(density)
	bodyWidth := max(width-2*inset, 0)
	// Measure before render. Spec section 2.5 as issue #243 re-cut it: a card's
	// height is content-sized under the section 2.6 ceilings, so the column has
	// no grid to reserve. It measures the cards it is about to draw, at the
	// width it is about to draw them at, and packs against that.
	//
	// The affordance column is still decided before the cards are rendered,
	// because reserving it narrows them. Spec sections 10.3.4 and 10.4.4: kb
	// composes strings, so a column that appeared and disappeared with activity
	// would reflow the body measure of every row under it, twice, for a cue - it
	// is reserved for the whole time the body overflows and the tint is what
	// moves.
	stack := m.columnStackHeight(m.measureCards(tasks, status, bodyWidth, density), density)
	contentHeight := max(height-1-metaRows, 0)
	if stack > contentHeight && contentHeight > 0 {
		contentHeight-- // the overflow cue of spec section 3.7 owns the last row
	}
	railed := widget.ScrollbarShown(stack, contentHeight) && bodyWidth > widget.ScrollbarW
	if railed {
		// The affordance narrows every card, and a narrower card wraps onto more
		// rows, so the stack is measured again at the width the cards will
		// actually take. Narrowing can only make a card taller, so the second
		// measure can never take the affordance back and there is no third pass.
		bodyWidth -= widget.ScrollbarW
		stack = m.columnStackHeight(m.measureCards(tasks, status, bodyWidth, density), density)
	}
	cardLines, owners, spans := m.renderTaskLines(tasks, status, bodyWidth, density)

	maxScroll := max(len(cardLines)-contentHeight, 0)
	start := visibleCardStart(cardLines, owners, m.boardView.selectedIndex(tasks, status), contentHeight)
	if m.boardView.manualScroll[index] {
		if anchor := m.boardView.scrollAnchors[index]; anchor.TaskID != "" {
			start = scrollOffsetForAnchor(owners, anchor, maxScroll)
		} else {
			start = min(max(m.boardView.scrolls[index], 0), maxScroll)
		}
	}
	hits[0].scroll = start
	hits[0].maxScroll = maxScroll
	hits[0].scrollUp = scrollAnchorAt(owners, min(max(start-3, 0), maxScroll))
	hits[0].scrollDown = scrollAnchorAt(owners, min(max(start+3, 0), maxScroll))
	end := visibleCardEnd(owners, start, contentHeight)

	rail := m.columnScrollbar(styles, railed, index, stack, contentHeight, start)
	body := make([]string, 0, contentHeight)
	for row := 0; row < contentHeight; row++ {
		source := start + row
		line := ""
		if source < end {
			line = cardLines[source]
		}
		if railed {
			cell := ""
			if row < len(rail) {
				cell = rail[row]
			}
			line = fillRow(styles.Column.Panel, line, bodyWidth) + cell
		}
		body = append(body, line)
		if source >= end {
			continue
		}
		y := 1 + metaRows + row
		if owners[source] != "" {
			hits = append(hits, boardHit{x1: width, y0: y, y1: y + 1, status: status, taskID: owners[source]})
		}
		for _, span := range spans[source] {
			hits = append(hits, boardHit{
				x0: inset + span.x0, x1: min(inset+span.x1, width),
				y0: y, y1: y + 1, status: status,
				kind: boardHitFilterLabel, tag: span.tag, taskID: owners[source],
			})
		}
	}

	lines := widget.Panel(styles, widget.PanelOpts{
		Header: widget.BandOpts{
			Index:   index + 1,
			Label:   statusLabel(status),
			Count:   len(tasks),
			Hue:     columnHue(status),
			Focused: m.boardView.column == index,
			Hovered: m.pointerState.IsHovered(boardColumnControlID(status)),
		},
		Meta:    meta,
		MetaLit: m.celebrateLit(status),
		Body:    body,
		More:    hiddenCards(owners, end),
		Width:   width,
		Height:  height,
		Density: density,
	})
	// The band is the column's click target, so the pressed state wraps the row
	// the panel drew rather than the panel wrapping the pointer.
	lines[0] = m.pointerState.Render(styles, boardColumnControlID(status), lines[0])
	return renderedColumn{lines: lines, hits: hits}
}

// columnStackHeight is the number of body rows the card stack will occupy: the
// measured heights plus the inter-card gutters between them.
//
// Issue #243 replaced the reservation this used to compute. A card is
// content-sized now, so there is no per-card constant to multiply by: the
// column measures each card at the width it will draw it and sums what came
// back. Measure and render are the same pure function of (task content, column
// width, density, frame height), so the sum here is exactly the number of rows
// renderTaskLines goes on to emit.
func (m Model) columnStackHeight(heights []int, density theme.Density) int {
	if len(heights) == 0 {
		return 1 // the empty or busy row of spec section 10.8
	}
	rows := m.themeStyles().Metrics.CardGapRows(density) * (len(heights) - 1)
	for _, height := range heights {
		rows += height
	}
	return rows
}

// measureCards is the measure half of the measure-before-render rule: the row
// count every card in the column will draw at this body width, without drawing
// any of them. It builds the same options the render pass builds, so the two
// cannot answer differently.
func (m Model) measureCards(tasks []board.Task, status board.Status, width int, density theme.Density) []int {
	styles := m.themeStyles()
	opts, _ := m.cardOptsFor(styles, tasks, status, width, density)
	heights := make([]int, 0, len(opts))
	for _, card := range opts {
		heights = append(heights, widget.CardHeight(styles, card))
	}
	return heights
}

// columnScrollbar is the affordance of spec section 10.3.4 at column scope. It
// is measured against the same stack height the affordance column was decided
// from, so the column and the bar can never disagree about whether the body
// overflows.
func (m Model) columnScrollbar(styles *theme.Styles, railed bool, index, total, visible, offset int) []string {
	if !railed {
		return nil
	}
	return widget.Scrollbar(styles, widget.ScrollbarOpts{
		Total:   total,
		Visible: visible,
		Offset:  offset,
		Height:  visible,
		Active:  m.scroll.lingering(index),
		On:      theme.Surface,
	})
}

// boardColumnControlID keys one column band across renders, so both its pressed
// and its hovered feedback survive a redraw.
func boardColumnControlID(status board.Status) pointer.ControlID {
	return pointer.ControlID("board-column:" + string(status))
}

// boardCardControlID keys one card for hover. Spec section 10.9 call 9 scopes
// the board's pointer response to an affordance cue: the card is hoverable, the
// board cursor still never follows the pointer, and the card's click is the
// drag source it has always been rather than a control press.
func boardCardControlID(taskID string) pointer.ControlID {
	return pointer.ControlID("board-card:" + taskID)
}

// columnMetaLine is the row under the band (spec section 2.3). The blocked
// segment appears only when the count is non-zero. The separator is the
// section 10.4.1 Bullet token rather than a literal: the mark is vocabulary,
// and the card description's list marker is now spelled from the same field.
func columnMetaLine(styles *theme.Styles, tasks []board.Task) string {
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
		line += fmt.Sprintf(" %s %d blocked", styles.Glyph.Bullet, blocked)
	}
	return line
}

// hiddenCards counts the cards the body did not draw whole - every card with a
// row at or below the last one the window reached. Under issue #243's
// content-sized cards the cue has to count what the reader cannot finish
// reading rather than what starts off-screen: card heights vary now, so a card
// showing only its title row is indistinguishable from a short card, and a cue
// that left it out would be counting a card the reader cannot see the rest of.
//
// Cards scrolled off the top are not counted: their last row is above the
// window, and the scroll affordance is what says they exist.
func hiddenCards(owners []string, from int) int {
	count, previous := 0, ""
	for index, owner := range owners {
		if owner != "" && owner != previous && cardLastRow(owners, index) >= from {
			count++
		}
		previous = owner
	}
	return count
}

// cardLastRow is the index of the final row belonging to the card that starts
// at first.
func cardLastRow(owners []string, first int) int {
	last := first
	for last+1 < len(owners) && owners[last+1] == owners[first] {
		last++
	}
	return last
}

// visibleCardEnd is one past the last row the column body draws: the end of the
// last card that fits whole inside the window.
//
// Issue #243's cards are content-sized, so a clipped card is ambiguous in a way
// it never was under a fixed grid - three rows of a twelve-row card and a
// three-row card are the same three rows. The bottom of the stack therefore
// drops a card that does not fit instead of clipping it, and the "+N more" cue
// of section 3.7 counts it. The rows it would have taken stay panel surface.
//
// The one exception is a window too short to hold even one whole card, where
// there is nothing to drop to: it clips, because an empty body under a "+N
// more" says less than a partial card does.
func visibleCardEnd(owners []string, start, height int) int {
	limit := min(start+height, len(owners))
	end := start
	for row := start; row < limit; {
		if owners[row] == "" {
			row++
			end = row
			continue
		}
		last := cardLastRow(owners, row)
		if last >= limit {
			break
		}
		row = last + 1
		end = row
	}
	if end == start {
		return limit
	}
	return end
}

// cardOptsFor builds one CardOpts per task at a body width, plus each task's
// tags in the survival order the card renders them in. Both the measure pass
// and the render pass go through it, so the height the column packed against is
// the height the card draws (spec section 2.5 as issue #243 re-cut it).
func (m Model) cardOptsFor(styles *theme.Styles, tasks []board.Task, status board.Status, width int, density theme.Density) ([]widget.CardOpts, [][]string) {
	depth := m.cardMetaDepth(tasks, width, density)
	opts := make([]widget.CardOpts, 0, len(tasks))
	orders := make([][]string, 0, len(tasks))
	for i := range tasks {
		card, ordered := m.cardOptsAt(styles, tasks, status, width, density, i, depth)
		opts = append(opts, card)
		orders = append(orders, ordered)
	}
	return opts, orders
}

// cardOptsAt builds the render options for one card. The cold renderer calls
// it for every task; the retained renderer and geometry worker call it only
// for the bounded records they actually need. Keeping one builder is the
// differential contract -- virtualizing a second approximation would be fast
// right up to the first wrong pointer row.
func (m Model) cardOptsAt(
	styles *theme.Styles,
	tasks []board.Task,
	status board.Status,
	width int,
	density theme.Density,
	ordinal int,
	depth int,
) (widget.CardOpts, []string) {
	if ordinal < 0 || ordinal >= len(tasks) {
		return widget.CardOpts{}, nil
	}
	focused := m.boardView.column == statusIndex(status)
	selected := m.boardView.selectedIndex(tasks, status)
	return m.cardOptsForTask(styles, tasks[ordinal], width, density, ordinal, depth,
		focused && ordinal == selected)
}

func (m Model) cardOptsForTask(
	styles *theme.Styles,
	task board.Task,
	width int,
	density theme.Density,
	ordinal int,
	depth int,
	selected bool,
) (widget.CardOpts, []string) {
	frameHeight := max(m.height, 8)
	surface := styles.Surface(selected, density.Compact() && ordinal%2 == 1)
	meta := m.cardMeta(styles, task, surface, density)
	depth = min(max(depth, 1), len(meta))

	// The project pill leads the label row: the row is rendered in survival
	// order, so the card's mandatory scope is the label that stays on it when
	// the row runs out of width (spec section 3.5).
	ordered := project.Lead(task.Tags)
	tags := make([]string, 0, len(ordered))
	for _, tag := range ordered {
		tags = append(tags, sanitizeTerminal(tag))
	}
	return widget.CardOpts{
		Title:      sanitizeTerminal(task.Title),
		Emoji:      sanitizeTerminal(task.Emoji),
		Seq:        seqLabel(task),
		Desc:       sanitizeTerminalText(task.Desc),
		Meta:       meta[:depth],
		Labels:     tags,
		Priority:   task.Prio,
		Blocked:    task.Blocked,
		Selected:   selected,
		Alt:        density.Compact() && ordinal%2 == 1,
		Width:      width,
		TitleLines: styles.Metrics.TitleRows(frameHeight, density),
		DescLines:  styles.Metrics.DescLines(frameHeight, density),
		LabelRows:  styles.Metrics.LabelRows(frameHeight, density),
		PadRows:    styles.Metrics.InnerPadRows(frameHeight, density),
		Density:    density,
		Hovered:    m.pointerState.IsHovered(boardCardControlID(task.ID)),
		HoverTag:   m.hoveredCardTag(task.ID, ordered),
	}, ordered
}

// cardMetaDepth resolves the column-wide survival depth without styling every
// offscreen card. ANSI attributes do not occupy cells; the widths below are
// the plain anatomy costs of the same widgets cardMeta later draws for the
// bounded render window.
func (m Model) cardMetaDepth(tasks []board.Task, width int, density theme.Density) int {
	return m.cardMetaDepthFor(width, density, len(tasks), func(index int) board.Task { return tasks[index] })
}

func (m Model) cardMetaDepthFor(
	width int,
	density theme.Density,
	count int,
	taskAt func(int) board.Task,
) int {
	inner := m.themeStyles().Metrics.CardInner(width, density)
	depth := cardMetaSlots
	for depth > 1 {
		fits := true
		for index := 0; index < count; index++ {
			task := taskAt(index)
			if metaWidthPrefix(m.cardMetaWidths(task, density), depth) > inner {
				fits = false
				break
			}
		}
		if fits {
			break
		}
		depth--
	}
	return depth
}

func (m Model) cardMetaWidths(task board.Task, density theme.Density) [cardMetaSlots]int {
	styles := m.themeStyles()
	flat := density.Compact()
	pad := 2
	if flat {
		pad = 0
	}
	widths := [cardMetaSlots]int{
		1 + pad,
		ansi.StringWidth(ageChip(task, m.renderedAt)),
		0,
		0,
	}
	if task.Due != "" {
		label, overdue := dueChip(sanitizeTerminal(task.Due), m.renderedAt)
		widths[2] = ansi.StringWidth(label)
		if overdue {
			widths[2] = ansi.StringWidth(styles.Glyph.MarkDue+label) + pad
		}
	}
	effort := sanitizeTerminal(task.Effort)
	if effort != "" {
		if _, onScale := theme.EffortSlot(effort); onScale {
			widths[3] = ansi.StringWidth(effort) + pad
		} else {
			widths[3] = ansi.StringWidth(styles.Glyph.Diamond + " " + effort)
		}
	}
	return widths
}

func metaWidthPrefix(widths [cardMetaSlots]int, depth int) int {
	width := 0
	for _, entry := range widths[:min(max(depth, 0), len(widths))] {
		if entry == 0 {
			continue
		}
		if width > 0 {
			width++
		}
		width += entry
	}
	return width
}

func (m Model) renderTaskLines(tasks []board.Task, status board.Status, width int, density theme.Density) ([]string, []string, [][]labelSpan) {
	styles := m.themeStyles()
	if len(tasks) == 0 {
		return []string{m.columnPlaceholder(styles, width)}, []string{""}, [][]labelSpan{nil}
	}
	cards, orders := m.cardOptsFor(styles, tasks, status, width, density)
	gap := styles.Metrics.CardGapRows(density)
	lines := make([]string, 0, len(tasks)*5)
	owners := make([]string, 0, len(tasks)*5)
	spans := make([][]labelSpan, 0, len(tasks)*5)
	for i, task := range tasks {
		ordered := orders[i]
		rows, cardSpans := widget.CardWithSpans(styles, cards[i])
		rowSpans := make([][]labelSpan, len(rows))
		for _, span := range cardSpans {
			// The span reports its position in CardOpts.Labels, so the hit keeps
			// the task's exact tag while the card renders the sanitized one.
			rowSpans[span.Row] = append(rowSpans[span.Row],
				labelSpan{x0: span.X0, x1: span.X1, tag: ordered[span.Index]})
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

// columnPlaceholder is the empty and loading anatomy of spec section 10.8 at
// column scope. Loading beats empty (section 10.8.4): until the first snapshot
// lands every column says it is loading, instead of the most-looked-at surface
// in the app telling the user the board is empty every time it starts. There is
// no error state at column scope - a load failure is the footer's row.
func (m Model) columnPlaceholder(styles *theme.Styles, width int) string {
	if m.boardLoading() {
		return widget.Busy(styles, widget.BusyOpts{
			Frame: m.busyFrame(styles, theme.Surface),
			Label: "loading",
			On:    theme.Surface,
			Width: width,
		})
	}
	headline, key, verb := m.columnEmptyRow()
	return widget.Empty(styles, widget.EmptyOpts{
		Headline: headline,
		Key:      key,
		Verb:     verb,
		On:       theme.Surface,
		Width:    width,
	})
}

// columnEmptyRow is the copy of spec section 10.8.7's two board-column rows.
// The tail is taken from the action registry and never from a literal, so a
// binding this board was built without is not offered: n falls through to i,
// and a board with neither renders the headline alone.
func (m Model) columnEmptyRow() (headline, key, verb string) {
	features := m.actionFeatures()
	if m.filter.active() {
		key, verb = actionTail(action.FilterClear, features)
		return "no matches", key, verb
	}
	for _, candidate := range []action.ID{action.NewCard, action.ImportIssue} {
		if key, verb = actionTail(candidate, features); key != "" {
			return "no cards", key, verb
		}
	}
	return "no cards", "", ""
}

// actionTail is one registry row as an empty state's action tail, or a pair of
// empty strings when this board cannot offer it.
func actionTail(id action.ID, features action.Features) (string, string) {
	entry, found := action.Lookup(id)
	if !found || !entry.Enabled(features) {
		return "", ""
	}
	return entry.Hint, entry.Name
}

// hoveredCardTag is the label pill under the pointer on one card, in the form
// the card rendered it, or the empty string for none.
func (m Model) hoveredCardTag(taskID string, tags []string) string {
	for _, tag := range tags {
		if m.pointerState.IsHovered(boardCardLabelControlID(taskID, tag)) {
			return sanitizeTerminal(tag)
		}
	}
	return ""
}

// cardMetaSlots is the number of chip categories in spec section 3.4's row:
// priority, age, due and effort. Issue #232 moved blocked out of it and onto
// the title row beside the sequence number.
const cardMetaSlots = 4

// metaDepth is how many of section 3.4's chip categories every card in one
// column renders. The drop is a property of the column rather than of a card:
// a category that does not fit on the widest card in the column is dropped from
// all of them, so a narrow board loses the same information everywhere instead
// of showing a due chip on one card and not on the card below it.
//
// The categories are dropped in reverse survival order, which is the order
// section 3.4 already fixes. Priority is never dropped: it is two cells, it is
// never a pill, and it is the chip the section says survives longest.
func metaDepth(metas [][]string, inner int) int {
	depth := cardMetaSlots
	for depth > 1 && !metaRowsFit(metas, depth, inner) {
		depth--
	}
	return depth
}

func metaRowsFit(metas [][]string, depth, inner int) bool {
	for _, meta := range metas {
		if metaRowWidth(meta[:min(depth, len(meta))]) > inner {
			return false
		}
	}
	return true
}

// metaRowWidth is what the chip row costs, one space between neighbours, with
// an absent category costing nothing at all.
func metaRowWidth(entries []string) int {
	width := 0
	for _, entry := range entries {
		if entry == "" {
			continue
		}
		if width > 0 {
			width++
		}
		width += ansi.StringWidth(entry)
	}
	return width
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
//
// The row is a fixed cardMetaSlots-long slice with an empty string for a
// category the card does not carry, so the column-wide drop of metaDepth cuts
// the same categories off every card rather than the same count of chips.
func (m Model) cardMeta(styles *theme.Styles, task board.Task, surface theme.Slot, density theme.Density) []string {
	flat := density.Compact()
	meta := make([]string, cardMetaSlots)
	meta[0] = widget.Priority(styles, task.Prio, surface, flat)
	meta[1] = styles.On(theme.FgMuted, surface).Render(ageChip(task, m.renderedAt))
	if task.Due != "" {
		// Issue #232 split the due chip in two. A deadline still ahead is plain
		// muted text - "today", "in 2d" - because a card that is merely
		// scheduled is not an alarm and does not earn a fill on a row that has
		// three other things to say. A deadline behind is the compact MarkDue
		// form on the danger fill: the mark and the bare elapsed count, which
		// is the whole of what the seven-cell word "overdue" and the two pill
		// padding cells used to spell.
		//
		// The mark and not the color is what carries the fact, which is section
		// 1.9's floor: "!" says the deadline has passed on a terminal with no
		// hue at all, and the two forms are different strings before they are
		// different colors.
		label, overdue := dueChip(sanitizeTerminal(task.Due), m.renderedAt)
		if overdue {
			meta[2] = widget.Chip(styles, widget.ChipOpts{
				Text: styles.Glyph.MarkDue + label,
				Fill: theme.StatusDanger,
				On:   surface,
				Flat: flat,
			})
		} else {
			meta[2] = styles.On(theme.FgMuted, surface).Render(label)
		}
	}
	// The effort marker is the letter on its own fill (issue #232), three cells
	// padded and one cell flat, or the Diamond fallback and the column section
	// 10.4.1's adjacency rule gives it for a value off the S/M/L scale.
	// metaRowWidth measures the chip rather than assuming a width, so the
	// survival order and the column-wide drop of metaDepth carry whichever form
	// it took without a constant to maintain.
	meta[3] = widget.Effort(styles, sanitizeTerminal(task.Effort), surface, flat)
	return meta
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
	// The window opens one row above the selected card, and then drops far
	// enough for the whole of it to fit. Issue #243 made that second clamp load-
	// bearing: cards are content-sized, so the selected one can be taller than
	// the slack the first clamp left, and the bottom of the stack drops a card
	// it cannot draw whole rather than clipping it.
	start := max(selectedLine-1, 0)
	if end := cardLastRow(owners, selectedLine) + 1; end > start+height {
		start = end - height
	}
	return min(max(start, 0), len(lines)-height)
}

func scrollAnchorAt(owners []string, offset int) boardTaskAnchor {
	if len(owners) == 0 {
		return boardTaskAnchor{}
	}
	offset = min(max(offset, 0), len(owners)-1)
	ownerRow := offset
	for ownerRow >= 0 && owners[ownerRow] == "" {
		ownerRow--
	}
	if ownerRow < 0 {
		ownerRow = offset
		for ownerRow < len(owners) && owners[ownerRow] == "" {
			ownerRow++
		}
		if ownerRow == len(owners) {
			return boardTaskAnchor{}
		}
		offset = ownerRow
	}
	id := owners[ownerRow]
	first := ownerRow
	for first > 0 && owners[first-1] == id {
		first--
	}
	hint := 0
	last := ""
	for _, owner := range owners[:first] {
		if owner != "" && owner != last {
			hint++
		}
		last = owner
	}
	return boardTaskAnchor{TaskID: id, IndexHint: hint, IntraRow: offset - first}
}

func scrollOffsetForAnchor(owners []string, anchor boardTaskAnchor, maxScroll int) int {
	first := -1
	for row, owner := range owners {
		if owner == anchor.TaskID {
			first = row
			break
		}
	}
	if first < 0 {
		first, end, ok := scrollOrdinalSpan(owners, anchor.IndexHint)
		if !ok {
			return 0
		}
		intra := min(max(anchor.IntraRow, 0), end-first-1)
		return min(max(first+intra, 0), maxScroll)
	}
	end := first + 1
	for end < len(owners) && (owners[end] == anchor.TaskID || owners[end] == "") {
		end++
	}
	intra := min(max(anchor.IntraRow, 0), end-first-1)
	return min(max(first+intra, 0), maxScroll)
}

// scrollOrdinalSpan resolves a task ordinal through the current terminal-row
// geometry. The ordinal is clamped so a removed task chooses its successor, or
// its predecessor when it was the final task in the column. The span includes
// trailing separator rows because scrollAnchorAt assigns those rows to the
// preceding task.
func scrollOrdinalSpan(owners []string, ordinal int) (first, end int, ok bool) {
	ordinal = max(ordinal, 0)
	candidate, lastStart, current := -1, -1, -1
	for row, owner := range owners {
		if owner == "" || (row > 0 && owners[row-1] == owner) {
			continue
		}
		if candidate >= 0 {
			return candidate, row, true
		}
		current++
		lastStart = row
		if current == ordinal {
			candidate = row
		}
	}
	if candidate >= 0 {
		return candidate, len(owners), true
	}
	if lastStart < 0 {
		return 0, 0, false
	}
	return lastStart, len(owners), true
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
	_ = active // capture is authoritative in Model, not in a rendered closure.
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
				anchor := hit.scrollDown
				if delta < 0 {
					anchor = hit.scrollUp
				}
				return func() tea.Msg {
					return boardColumnScrolledMsg{status: hit.status, from: hit.scroll,
						offset: offset, anchor: anchor, max: hit.maxScroll}
				}
			}
			return nil
		}
		if _, release := message.(tea.MouseReleaseMsg); release {
			if mouse.Button == tea.MouseLeft || mouse.Button == tea.MouseNone {
				result := boardPointerUpMsg{resolved: true}
				for index := len(hits) - 1; index >= 0; index-- {
					hit := hits[index]
					if hit.kind != boardHitDefault || mouse.X < hit.x0 || mouse.X >= hit.x1 ||
						mouse.Y < hit.y0 || mouse.Y >= hit.y1 {
						continue
					}
					result.valid = true
					result.status = hit.status
					result.beforeTaskID = hit.taskID
					break
				}
				return func() tea.Msg { return result }
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
			case boardHitProject:
				return func() tea.Msg { return filterProjectClickedMsg{} }
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
	return boardPointerSurface(hits, active, state).Pointer
}

func boardPointerSurface(hits []boardHit, active bool, state pointer.State) pointer.Surface {
	base := boardMouseHandler(hits, active)
	var controls, hovers pointer.Map
	topology := pointer.Topology{}
	gesture := state.Active()
	for _, hit := range hits {
		rect := pointer.Rect{X0: hit.x0, Y0: hit.y0, X1: hit.x1, Y1: hit.y1}
		if id := boardCardHoverID(hit); id != "" {
			// A card opts into hover and into nothing else. Its press stays on
			// the drag path of boardMouseHandler, because the card is the board's
			// drag source and the anchor every board keybinding resolves against
			// (spec section 10.9 call 9); routing it as a control would turn a
			// lift into a button press.
			hovers.AddControl(id, rect, hoverOnly)
		}
		id := boardHitControlID(hit)
		if id == "" {
			continue
		}
		if state.IsPressed(id) {
			gesture = true
		}
		message := boardControlMessage(hit)
		deliver := func(pointer.Point) tea.Msg { return message }
		controls.AddControl(id, rect, deliver)
		hovers.AddControl(id, rect, deliver)
	}
	topology = controls.Topology().Merge(hovers.Topology())
	for _, hit := range hits {
		if hit.kind != boardHitDefault || hit.taskID != "" || hit.maxScroll <= 0 {
			continue
		}
		current := hit
		intent := pointer.WheelIntent{Key: "board:" + string(current.status), Current: current.scroll,
			Target: current.scroll, Min: 0, Max: current.maxScroll}
		topology = topology.WithWheel(intent, func(target int) tea.Msg {
			target = min(max(target, 0), current.maxScroll)
			return boardColumnScrolledMsg{status: current.status, from: current.scroll,
				offset: target, max: current.maxScroll}
		})
	}
	controlHandler := controls.Handler()
	hoverHandler := hovers.Handler()
	controlHit := func(mouse tea.Mouse) bool {
		for index := len(hits) - 1; index >= 0; index-- {
			hit := hits[index]
			if boardHitControlID(hit) != "" && mouse.X >= hit.x0 && mouse.X < hit.x1 && mouse.Y >= hit.y0 && mouse.Y < hit.y1 {
				return true
			}
		}
		return false
	}
	handler := func(message tea.MouseMsg) tea.Cmd {
		mouse := message.Mouse()
		switch message.(type) {
		case tea.MouseWheelMsg:
			command := base(message)
			if gesture {
				gesture = false
				return pointer.CancelWith(command)
			}
			return command
		case tea.MouseClickMsg:
			if controlHit(mouse) {
				gesture = true
				return controlHandler(message)
			}
		case tea.MouseMotionMsg:
			if gesture {
				return controlHandler(message)
			}
			// Bare motion is the hover step of spec section 10.5.1. It resolves
			// against the hover list, which is the control list plus the cards,
			// and it produces a hover message or a clear and nothing else - the
			// board cursor never follows the pointer.
			if mouse.Button != tea.MouseLeft {
				return hoverHandler(message)
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
	return pointer.Surface{Pointer: handler, Topology: topology}
}

// boardCardHoverID is the hover identity of one card body row, empty for a hit
// that is not one. The label spans inside a card carry their own control ids and
// are added after the row they sit on, so the topmost-wins scan of the hover
// list resolves a pill before the card under it.
func boardCardHoverID(hit boardHit) pointer.ControlID {
	if hit.kind != boardHitDefault || hit.taskID == "" {
		return ""
	}
	return boardCardControlID(hit.taskID)
}

// hoverOnly is the action of a region that exists to be hovered. The hover list
// is only ever handed motion messages, so it is never called; it is not nil
// because the region scan skips a region without one.
func hoverOnly(pointer.Point) tea.Msg { return nil }

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
	case boardHitProject:
		return filterProjectClickedMsg{}
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

// ageChip is position 2 of the section 3.4 meta row. Issue #232 cut the word
// off it: "3d old" and "6h here" spent four and five cells saying which clock
// the number came from, on a row whose whole job is to be scannable. The column
// the card sits in already says it - a card in DOING is measured from when it
// arrived there and a card anywhere else from when it was made - so the number
// and its unit are the whole chip.
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
			return fmt.Sprintf("%dh", max(1, int(elapsed/time.Hour)))
		}
		return "new"
	}
	return fmt.Sprintf("%dd", int(elapsed/day))
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
		// Issue #232: the word "overdue" is gone. A bare elapsed count behind
		// the "!" mark is the overdue form, and the three readings stay
		// distinguishable as text - "today", "in 2d", "5d" - so the hue is
		// reinforcement rather than the only carrier of the fact.
		return fmt.Sprintf("%dd", -days), true
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
