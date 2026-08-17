// Package carddetail renders the read-only full-card overlay for the TUI.
package carddetail

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
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

// Model owns the overlay's task snapshot, enriched detail, and scroll state.
type Model struct {
	reader         Reader
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
}

// New creates a closed detail pane. A nil reader still shows board-resident
// task fields; enrichment is simply unavailable to lightweight model tests.
func New(reader Reader, user string) Model {
	return Model{
		reader: reader, user: user, width: defaultWidth, height: defaultHeight,
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
	m.generation++
	m.open = false
	m.loading = false
	m.reloadPending = false
	m.task = board.Task{}
	m.bodyLines = nil
	m.bodyWidth = 0
	m.scroll = 0
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
		m.rebuildBody()
	case tea.KeyPressMsg:
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
	if !m.open {
		return background
	}
	width = max(width, 1)
	height = max(height, 1)
	frame, paneWidth, paneHeight := m.frame(width, height)
	x := max((width-paneWidth)/2, 0)
	y := max((height-paneHeight)/2, 0)
	return lipgloss.NewCompositor(
		lipgloss.NewLayer(background),
		lipgloss.NewLayer(frame).X(x).Y(y).Z(1),
	).Render()
}

func (m *Model) frame(width, height int) (string, int, int) {
	width = max(width, 1)
	height = max(height, 1)
	m.ensureBody(width, height)
	innerWidth, innerHeight, paneHeight := paneGeometry(width, height)

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
	footer := "esc close  ↑/↓ scroll"
	if maxScroll > 0 {
		footer = fmt.Sprintf("%s  %d/%d", footer, start+1, maxScroll+1)
	}
	content := visible + "\n" + ansi.Truncate(footer, innerWidth, "…")
	frame := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1).
		Width(innerWidth).
		Height(paneHeight - 2).
		Render(content)
	frame = fitTerminal(frame, width, height)
	return frame, lipgloss.Width(frame), lipgloss.Height(frame)
}

func paneGeometry(width, height int) (innerWidth, innerHeight, paneHeight int) {
	width = max(width, 1)
	height = max(height, 1)
	paneWidth := min(max(width-4, 12), maxPaneWidth, width)
	paneHeight = min(max(min(height-2, height), 5), height)
	return max(paneWidth-4, 1), max(paneHeight-4, 1), paneHeight
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
