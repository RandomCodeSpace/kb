// Package carddetail renders the full-card overlay and its direct-store
// comment and blocker-link actions.
package carddetail

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/forge"
	"github.com/RandomCodeSpace/kb/internal/store"
	"github.com/RandomCodeSpace/kb/internal/tui/pointer"
)

const (
	maxPaneWidth  = 92
	defaultWidth  = 80
	defaultHeight = 24
)

// Reader is the store projection needed to enrich a board task for display.
type Reader interface {
	Comments(user, taskRef string) ([]store.Comment, error)
	TaskLinks(user, taskID string) (store.TaskLinks, error)
	Tombstone(user, taskID string) (store.Tombstone, bool, error)
}

type detailLoadedMsg struct {
	taskID       string
	generation   uint64
	comments     []store.Comment
	links        store.TaskLinks
	tombstone    *store.Tombstone
	commentsErr  error
	linksErr     error
	tombstoneErr error
}

type markdownRenderer func(source string, width int) string

type mouseScrollMsg struct{ delta int }
type mouseDismissMsg struct{}

// Model owns the overlay's task snapshot, enriched detail, and scroll state.
type Model struct {
	reader         Reader
	writer         Writer
	user           string
	task           board.Task
	comments       []store.Comment
	links          store.TaskLinks
	tombstone      *store.Tombstone
	open           bool
	loading        bool
	reloadPending  bool
	commentsErr    error
	linksErr       error
	tombstoneErr   error
	scroll         int
	generation     uint64
	width          int
	height         int
	renderMarkdown markdownRenderer
	bodyLines      []string
	bodyWidth      int

	action        actionMode
	actionSession uint64
	commentInput  textarea.Model
	linkInput     textinput.Model
	currentBlocks bool
	selection     int
	confirm       bool
	saving        bool
	changed       bool
	statusMessage string
	statusIsError bool

	driftBackend    DriftBackend
	driftContext    context.Context
	driftMode       driftMode
	driftSession    uint64
	driftGeneration uint64
	driftBusy       string
	driftCancel     context.CancelFunc
	driftChoices    []store.ImportLink
	driftSelection  int
	driftResult     forge.Drift
}

// New creates a closed detail pane. A nil reader still shows board-resident
// task fields; enrichment is simply unavailable to lightweight model tests.
func New(reader Reader, user string) Model {
	writer, _ := reader.(Writer)
	return Model{
		reader: reader, writer: writer, user: user, width: defaultWidth, height: defaultHeight,
		renderMarkdown: renderMarkdown,
	}
}

// IsOpen reports whether the overlay currently owns input and rendering.
func (m Model) IsOpen() bool { return m.open }

// TaskID returns the displayed task's durable ID, or empty while closed.
func (m Model) TaskID() string {
	if !m.open {
		return ""
	}
	return m.task.ID
}

// Open resets the pane to task and returns the asynchronous enrichment load.
func (m *Model) Open(task board.Task) tea.Cmd {
	m.cancelDrift()
	m.actionSession++
	m.task = task
	m.comments = nil
	m.links = store.TaskLinks{}
	m.tombstone = nil
	m.open = true
	m.loading = false
	m.reloadPending = false
	m.commentsErr = nil
	m.linksErr = nil
	m.tombstoneErr = nil
	m.scroll = 0
	m.action = actionNone
	m.selection = 0
	m.confirm = false
	m.saving = false
	m.changed = false
	m.statusMessage = ""
	m.statusIsError = false
	if m.reader == nil {
		m.rebuildBody()
		return nil
	}
	return m.startLoad()
}

// Refresh replaces the board-resident snapshot and serializes enrichment IO.
// Repeated refreshes during a load collapse into one successor for the latest
// task snapshot.
func (m *Model) Refresh(task board.Task) tea.Cmd {
	if !m.open || task.ID != m.task.ID {
		return nil
	}
	m.task = task
	if m.reader == nil {
		m.rebuildBody()
		return nil
	}
	if m.loading {
		m.reloadPending = true
		m.rebuildBody()
		return nil
	}
	return m.startLoad()
}

// Close dismisses the pane and invalidates any in-flight result by clearing
// the current task identity.
func (m *Model) Close() {
	m.cancelDrift()
	m.generation++
	m.actionSession++
	m.open = false
	m.loading = false
	m.reloadPending = false
	m.task = board.Task{}
	m.bodyLines = nil
	m.bodyWidth = 0
	m.scroll = 0
	m.action = actionNone
	m.selection = 0
	m.confirm = false
	m.saving = false
	m.changed = false
	m.statusMessage = ""
	m.statusIsError = false
}

// Resize updates the viewport used to bound persistent scroll state.
func (m *Model) Resize(width, height int) {
	m.width = max(width, 1)
	m.height = max(height, 1)
	innerWidth, _, _ := paneGeometry(m.width, m.height)
	if m.open && (m.bodyLines == nil || m.bodyWidth != innerWidth) {
		m.rebuildBody()
		return
	}
	m.clampScroll()
}

// Update handles enrichment results and overlay scrolling.
func (m *Model) Update(message tea.Msg) tea.Cmd {
	if !m.open {
		return nil
	}
	switch msg := message.(type) {
	case driftChoicesLoadedMsg, driftCheckedMsg, driftAcceptedMsg:
		return m.updateDrift(msg)
	case mutationCompletedMsg:
		return m.finishMutation(msg)
	case detailLoadedMsg:
		if msg.taskID != m.task.ID || msg.generation != m.generation {
			return nil
		}
		if m.reloadPending {
			m.reloadPending = false
			return m.startLoad()
		}
		m.loading = false
		m.comments = msg.comments
		m.links = msg.links
		m.tombstone = msg.tombstone
		m.commentsErr = msg.commentsErr
		m.linksErr = msg.linksErr
		m.tombstoneErr = msg.tombstoneErr
		m.reconcileDeleteActionAfterRefresh()
		m.rebuildBody()
	case mouseScrollMsg:
		m.scroll += msg.delta
	case mouseDismissMsg:
		m.Close()
		return nil
	case tea.KeyPressMsg:
		if m.driftMode != driftNone {
			return m.updateDrift(msg)
		}
		if m.action != actionNone {
			return m.updateActionKey(msg)
		}
		if m.statusMessage != "" {
			m.statusMessage = ""
			m.statusIsError = false
			m.rebuildBody()
		}
		switch msg.String() {
		case "v":
			return m.beginDrift()
		case "c":
			return m.beginAction(actionAddComment)
		case "d":
			return m.beginAction(actionDeleteComment)
		case "b":
			return m.beginAction(actionAddLink)
		case "u":
			return m.beginAction(actionDeleteLink)
		}
		switch msg.String() {
		case "up", "k", "pgup":
			m.scroll = max(0, m.scroll-scrollAmount(msg.String()))
		case "down", "j", "pgdown":
			m.scroll += scrollAmount(msg.String())
		case "home", "g":
			m.scroll = 0
		}
	}
	m.clampScroll()
	return nil
}

// MouseHandler routes wheel scrolling within the pane and left-click dismissal
// outside it. The root disables this handler while nested detail input is active.
func (m Model) MouseHandler(width, height int) func(tea.MouseMsg) tea.Cmd {
	if !m.open {
		return nil
	}
	paneWidth, paneHeight := paneSize(width, height)
	x0 := max((max(width, 1)-paneWidth)/2, 0)
	y0 := max((max(height, 1)-paneHeight)/2, 0)
	return func(message tea.MouseMsg) tea.Cmd {
		mouse := message.Mouse()
		inside := mouse.X >= x0 && mouse.X < x0+paneWidth && mouse.Y >= y0 && mouse.Y < y0+paneHeight
		if _, wheel := message.(tea.MouseWheelMsg); wheel {
			if !inside {
				return nil
			}
			delta := 0
			switch mouse.Button {
			case tea.MouseWheelUp:
				delta = -3
			case tea.MouseWheelDown:
				delta = 3
			default:
				return nil
			}
			return func() tea.Msg { return mouseScrollMsg{delta: delta} }
		}
		if _, click := message.(tea.MouseClickMsg); click && mouse.Button == tea.MouseLeft && !inside {
			return func() tea.Msg { return mouseDismissMsg{} }
		}
		return nil
	}
}

func (m *Model) reconcileDeleteActionAfterRefresh() {
	count, noun := 0, ""
	switch m.action {
	case actionDeleteComment:
		count, noun = len(m.comments), "comments"
		if m.commentsErr != nil {
			m.cancelDeleteActionAfterRefresh("comments unavailable; deletion cancelled", true)
			return
		}
	case actionDeleteLink:
		count, noun = len(m.linkChoices()), "blocker links"
		if m.linksErr != nil {
			m.cancelDeleteActionAfterRefresh("blocker links unavailable; deletion cancelled", true)
			return
		}
	default:
		return
	}
	if count == 0 {
		m.cancelDeleteActionAfterRefresh(noun+" changed; none remain to remove", false)
		return
	}
	m.selection = min(max(m.selection, 0), count-1)
	if m.confirm {
		m.confirm = false
		m.setStatus(noun+" changed; review the selection and confirm again", false)
	}
}

func (m *Model) cancelDeleteActionAfterRefresh(status string, isError bool) {
	m.action = actionNone
	m.selection = 0
	m.confirm = false
	m.setStatus(status, isError)
}

func scrollAmount(key string) int {
	if key == "pgup" || key == "pgdown" {
		return 8
	}
	return 1
}

func (m Model) load(taskID string, generation uint64) tea.Cmd {
	return func() tea.Msg {
		comments, commentsErr := m.reader.Comments(m.user, taskID)
		links, linksErr := m.reader.TaskLinks(m.user, taskID)
		tombstone, found, tombstoneErr := m.reader.Tombstone(m.user, taskID)
		var killed *store.Tombstone
		if found {
			killed = &tombstone
		}
		return detailLoadedMsg{
			taskID: taskID, generation: generation, comments: comments, links: links, tombstone: killed,
			commentsErr: commentsErr, linksErr: linksErr, tombstoneErr: tombstoneErr,
		}
	}
}

func (m *Model) startLoad() tea.Cmd {
	m.generation++
	m.loading = true
	m.commentsErr = nil
	m.linksErr = nil
	m.tombstoneErr = nil
	m.rebuildBody()
	return m.load(m.task.ID, m.generation)
}

// View renders a centered bordered pane sized for the current terminal.
func (m *Model) View(width, height int) string {
	if !m.open {
		return ""
	}
	width = max(width, 1)
	height = max(height, 1)
	frame, _, _ := m.frame(width, height)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, frame)
}

// Overlay composes the pane over the board without making carddetail own the
// board renderer. Input routing remains the root model's responsibility.
func (m *Model) Overlay(background string, width, height int) string {
	return m.PointerSurface(background, width, height).Content
}

// PointerSurface renders the detail overlay and returns the immutable pointer
// map produced from this exact layout pass. The root selects it as the active
// top-most surface, so carddetail never exposes board hit regions while open.
func (m *Model) PointerSurface(background string, width, height int) pointer.Surface {
	if !m.open {
		return pointer.Surface{Content: background}
	}
	width = max(width, 1)
	height = max(height, 1)
	background = fitTerminal(background, width, height)
	frame, paneWidth, _ := m.frame(width, height)
	_, paneHeight := paneSize(width, height)
	x := max((width-paneWidth)/2, 0)
	y := max((height-paneHeight)/2, 0)
	content := fitTerminal(lipgloss.NewCompositor(
		lipgloss.NewLayer(background),
		lipgloss.NewLayer(frame).X(x).Y(y).Z(1),
	).Render(), width, height)

	bounds := pointer.Rect{X0: 0, Y0: 0, X1: width, Y1: height}
	pane := pointer.Rect{X0: x, Y0: y, X1: x + paneWidth, Y1: y + paneHeight}
	var hitMap pointer.Map
	hitMap.AddWheel(pane, func(delta int) tea.Msg { return mouseScrollMsg{delta: delta * 3} })
	if m.action == actionNone && m.driftMode == driftNone {
		hitMap.AddBackdrop(bounds, pane, func(pointer.Point) tea.Msg { return mouseDismissMsg{} })
	}

	innerWidth, _, _ := paneGeometry(width, height)
	displayWidth := max(innerWidth-2, 1)
	footerY := y + paneHeight - 3
	xCursor := x + 2
	for _, control := range m.pointerFooterControls(displayWidth) {
		label := "[" + control.label + "]"
		labelWidth := ansi.StringWidth(label)
		if xCursor+labelWidth > x+paneWidth-2 || footerY < y || footerY >= y+paneHeight {
			break
		}
		rect := pointer.Rect{X0: xCursor, Y0: footerY, X1: xCursor + labelWidth, Y1: footerY + 1}
		message := control.message
		hitMap.Add(rect, func(pointer.Point) tea.Msg { return message })
		xCursor += labelWidth + 1
	}
	return pointer.Surface{Content: content, Pointer: hitMap.Handler()}
}

func (m *Model) frame(width, height int) (string, int, int) {
	width = max(width, 1)
	height = max(height, 1)
	m.ensureBody(width, height)
	innerWidth, innerHeight, paneHeight := paneGeometry(width, height)
	displayWidth := max(innerWidth-2, 1)

	lines := m.bodyLines
	maxScroll := max(0, len(lines)-innerHeight)
	start := min(m.scroll, maxScroll)
	end := min(start+innerHeight, len(lines))
	visibleLines := append([]string(nil), lines[start:end]...)
	for i := range visibleLines {
		visibleLines[i] = ansi.Truncate(visibleLines[i], innerWidth, "")
	}
	visible := strings.Join(visibleLines, "\n")
	visible = lipgloss.NewStyle().Width(innerWidth).Height(innerHeight).Render(visible)
	footer := m.actionFooter(displayWidth)
	if maxScroll > 0 {
		footer = fmt.Sprintf("%s  %d/%d", footer, start+1, maxScroll+1)
	}
	content := visible + "\n" + fitDetailLine(footer, displayWidth)
	frame := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1).
		Width(innerWidth).
		Height(paneHeight - 2).
		Render(content)
	frame = fitTerminal(frame, width, height)
	return frame, lipgloss.Width(frame), lipgloss.Height(frame)
}

func fitDetailLine(line string, width int) string {
	width = max(width, 0)
	if ansi.StringWidth(line) <= width {
		return line
	}
	if width <= 1 {
		return ansi.Cut("…", 0, width)
	}
	return ansi.Cut(line, 0, width-1) + "…"
}

func paneGeometry(width, height int) (innerWidth, innerHeight, paneHeight int) {
	paneWidth, paneHeight := paneSize(width, height)
	return max(paneWidth-4, 1), max(paneHeight-4, 1), paneHeight
}

func paneSize(width, height int) (paneWidth, paneHeight int) {
	width = max(width, 1)
	height = max(height, 1)
	return min(max(width-4, 12), maxPaneWidth, width), min(max(min(height-2, height), 5), height)
}

func (m *Model) clampScroll() {
	if !m.open {
		m.scroll = 0
		return
	}
	m.scroll = min(max(m.scroll, 0), m.maxScroll())
}

func (m Model) maxScroll() int {
	_, innerHeight, _ := paneGeometry(m.width, m.height)
	return max(0, len(m.bodyLines)-innerHeight)
}

func (m *Model) ensureBody(width, height int) {
	m.width = max(width, 1)
	m.height = max(height, 1)
	innerWidth, _, _ := paneGeometry(m.width, m.height)
	if m.bodyLines == nil || m.bodyWidth != innerWidth {
		m.rebuildBody()
		return
	}
	m.clampScroll()
}

func (m *Model) rebuildBody() {
	if !m.open {
		m.bodyLines = nil
		m.bodyWidth = 0
		m.scroll = 0
		return
	}
	innerWidth, _, _ := paneGeometry(m.width, m.height)
	body := m.renderBody(innerWidth)
	m.bodyLines = strings.Split(strings.TrimRight(body, "\n"), "\n")
	m.bodyWidth = innerWidth
	m.clampScroll()
}

func fitTerminal(rendered string, width, height int) string {
	lines := strings.Split(rendered, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], width, "")
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderBody(width int) string {
	if m.driftMode != driftNone {
		return m.driftBody(width)
	}
	if m.action != actionNone {
		return m.actionBody(width)
	}
	title := strings.TrimSpace(safeText(m.task.Title, false))
	if m.task.Emoji != "" {
		title = safeText(m.task.Emoji, false) + " " + title
	}
	if m.task.Seq > 0 {
		title += fmt.Sprintf("  #%d", m.task.Seq)
	}
	sections := []string{lipgloss.NewStyle().Bold(true).Render(title)}
	sections = append(sections, m.metadata())
	if tags := regularTags(m.task.Tags); len(tags) > 0 {
		sections = append(sections, "labels  "+strings.Join(tags, "  "))
	}
	if links := importLinks(m.task.Tags); len(links) > 0 {
		sections = append(sections, "links   "+strings.Join(links, "  "))
	}
	if m.tombstone != nil {
		sections = append(sections, killedContext(*m.tombstone))
	}
	if m.tombstoneErr != nil {
		sections = append(sections, "killed context error: "+safeText(m.tombstoneErr.Error(), false))
	}
	if strings.TrimSpace(m.task.Desc) != "" {
		sections = append(sections, m.markdown(m.task.Desc, width))
	}
	if len(m.task.Checks) > 0 {
		sections = append(sections, renderChecklist(m.task.Checks))
	}
	if refs := renderTaskLinks(m.links); refs != "" {
		sections = append(sections, refs)
	}
	sections = append(sections, renderCompletionGate(m.task, m.links, m.loading, m.linksErr))
	if m.linksErr != nil {
		sections = append(sections, "blocker links error: "+safeText(m.linksErr.Error(), false))
	}
	if m.loading {
		sections = append(sections, "loading comments and context...")
	} else if m.commentsErr != nil {
		sections = append(sections, "comments error: "+safeText(m.commentsErr.Error(), false))
	} else {
		if len(m.comments) > 0 {
			sections = append(sections, renderCommentsWith(m.comments, width, m.markdown))
		} else {
			sections = append(sections, "comments  none")
		}
	}
	if m.statusMessage != "" {
		prefix := "status: "
		if m.statusIsError {
			prefix = "error: "
		}
		sections = append(sections, prefix+m.statusMessage)
	}
	return strings.Join(sections, "\n\n")
}

func (m Model) markdown(source string, width int) string {
	if m.renderMarkdown == nil {
		return renderMarkdown(source, width)
	}
	return m.renderMarkdown(source, width)
}

func (m Model) metadata() string {
	primary := []string{fmt.Sprintf("status %s", m.task.Status), fmt.Sprintf("priority %d", m.task.Prio)}
	if m.task.Blocked {
		primary = append(primary, "blocked")
	}
	secondary := make([]string, 0, 2)
	if m.task.Due != "" {
		secondary = append(secondary, "due "+m.task.Due)
	}
	if m.task.Effort != "" {
		secondary = append(secondary, "effort "+m.task.Effort)
	}
	if len(secondary) == 0 {
		return strings.Join(primary, "  ")
	}
	return strings.Join(primary, "  ") + "\n" + strings.Join(secondary, "  ")
}

func regularTags(tags []string) []string {
	var out []string
	for _, tag := range tags {
		if !strings.HasPrefix(tag, "link::") {
			out = append(out, "["+safeText(tag, false)+"]")
		}
	}
	return out
}

func importLinks(tags []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, tag := range tags {
		if !strings.HasPrefix(tag, "link::") {
			continue
		}
		link := strings.TrimSpace(strings.TrimPrefix(tag, "link::"))
		if link != "" && !seen[link] {
			seen[link] = true
			out = append(out, "["+safeText(link, false)+"]")
		}
	}
	return out
}

func killedContext(tombstone store.Tombstone) string {
	date := tombstone.KilledAt
	if stamp, err := time.Parse(time.RFC3339Nano, tombstone.KilledAt); err == nil {
		date = stamp.UTC().Format("2 Jan 2006")
	}
	return fmt.Sprintf("killed %s\n%s", safeText(date, false), safeText(tombstone.Reason, false))
}

func renderMarkdown(source string, width int) string {
	style := styles.DarkStyleConfig
	zero := uint(0)
	style.Document.Margin = &zero
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStyles(style),
		glamour.WithWordWrap(max(width, 1)),
	)
	if err != nil {
		return safeText(source, true)
	}
	rendered, err := renderer.Render(parityMarkdown(safeText(source, true)))
	if err != nil {
		return safeText(source, true)
	}
	return strings.Trim(rendered, "\r\n")
}

func renderChecklist(checks []board.Check) string {
	lines := []string{"checklist"}
	for _, check := range checks {
		mark := "☐"
		if check.Done {
			mark = "☑"
		}
		lines = append(lines, fmt.Sprintf("  %s %s", mark, safeText(check.Text, false)))
	}
	return strings.Join(lines, "\n")
}

func renderTaskLinks(links store.TaskLinks) string {
	var lines []string
	if len(links.Blocks) > 0 {
		lines = append(lines, "blocks      "+taskChips(links.Blocks))
	}
	if len(links.BlockedBy) > 0 {
		lines = append(lines, "blocked by  "+taskChips(links.BlockedBy))
	}
	return strings.Join(lines, "\n")
}

func renderCompletionGate(task board.Task, links store.TaskLinks, loading bool, linksErr error) string {
	var reasons []string
	if warning := store.CompletionWarning(task); warning != "" {
		reasons = append(reasons, warning)
	}
	var open []board.Task
	for _, blocker := range links.BlockedBy {
		if blocker.Status != board.StatusDone && blocker.Status != board.StatusCancelled {
			open = append(open, blocker)
		}
	}
	if len(open) > 0 {
		noun := "open linked blocker"
		if len(open) != 1 {
			noun += "s"
		}
		reasons = append(reasons, fmt.Sprintf("%d %s %s", len(open), noun, taskChips(open)))
	}
	unknown := ""
	if loading {
		unknown = "linked blockers loading"
	} else if linksErr != nil {
		unknown = "linked blockers unavailable"
	}
	if len(reasons) > 0 {
		if unknown != "" {
			reasons = append(reasons, unknown)
		}
		return "completion gate  blocked: " + strings.Join(reasons, "; ")
	}
	if unknown != "" {
		return "completion gate  unknown: " + unknown
	}
	return "completion gate  clear"
}

func taskChips(tasks []board.Task) string {
	chips := make([]string, 0, len(tasks))
	for _, task := range tasks {
		ref := task.ID
		if task.Seq > 0 {
			ref = fmt.Sprintf("#%d", task.Seq)
		}
		chips = append(chips, fmt.Sprintf("[%s %s]", ref, task.Status))
	}
	return strings.Join(chips, "  ")
}

func renderComments(comments []store.Comment, width int) string {
	return renderCommentsWith(comments, width, renderMarkdown)
}

func renderCommentsWith(comments []store.Comment, width int, renderer markdownRenderer) string {
	sections := []string{fmt.Sprintf("comments  %d", len(comments))}
	for _, comment := range comments {
		date := comment.CreatedAt.UTC().Format("2 Jan 2006")
		header := fmt.Sprintf("c%d  %s  %s", comment.ID, safeText(comment.Author, false), date)
		sections = append(sections, header+"\n"+renderer(comment.Body, width))
	}
	return strings.Join(sections, "\n\n")
}

func safeText(text string, keepNewlines bool) string {
	text = ansi.Strip(text)
	var out strings.Builder
	for _, r := range text {
		if r == '\t' {
			out.WriteString("    ")
			continue
		}
		if r == '\v' || r == '\f' || r == '\r' {
			out.WriteByte(' ')
			continue
		}
		if keepNewlines && r == '\n' {
			out.WriteRune(r)
			continue
		}
		if unicode.IsControl(r) {
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}
