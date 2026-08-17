// Package carddetail renders the read-only full-card overlay for the TUI.
package carddetail

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
)

const maxPaneWidth = 92

// Reader is the store projection needed to enrich a board task for display.
type Reader interface {
	Comments(user, taskRef string) ([]store.Comment, error)
	TaskLinks(user, taskID string) (store.TaskLinks, error)
	Tombstone(user, taskID string) (store.Tombstone, bool, error)
}

type detailLoadedMsg struct {
	taskID    string
	comments  []store.Comment
	links     store.TaskLinks
	tombstone *store.Tombstone
	err       error
}

// Model owns the overlay's task snapshot, enriched detail, and scroll state.
type Model struct {
	reader    Reader
	user      string
	task      board.Task
	comments  []store.Comment
	links     store.TaskLinks
	tombstone *store.Tombstone
	open      bool
	loading   bool
	err       error
	scroll    int
}

// New creates a closed detail pane. A nil reader still shows board-resident
// task fields; enrichment is simply unavailable to lightweight model tests.
func New(reader Reader, user string) Model {
	return Model{reader: reader, user: user}
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
	m.loading = m.reader != nil
	m.err = nil
	m.scroll = 0
	if m.reader == nil {
		return nil
	}
	return m.load(task.ID)
}

// Close dismisses the pane and invalidates any in-flight result by clearing
// the current task identity.
func (m *Model) Close() {
	m.open = false
	m.loading = false
	m.task = board.Task{}
}

// Update handles enrichment results and overlay scrolling.
func (m *Model) Update(message tea.Msg) tea.Cmd {
	if !m.open {
		return nil
	}
	switch msg := message.(type) {
	case detailLoadedMsg:
		if msg.taskID != m.task.ID {
			return nil
		}
		m.loading = false
		m.err = msg.err
		if msg.err == nil {
			m.comments = msg.comments
			m.links = msg.links
			m.tombstone = msg.tombstone
		}
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
	return nil
}

func scrollAmount(key string) int {
	if key == "pgup" || key == "pgdown" {
		return 8
	}
	return 1
}

func (m Model) load(taskID string) tea.Cmd {
	return func() tea.Msg {
		comments, commentsErr := m.reader.Comments(m.user, taskID)
		links, linksErr := m.reader.TaskLinks(m.user, taskID)
		tombstone, found, tombstoneErr := m.reader.Tombstone(m.user, taskID)
		var killed *store.Tombstone
		if found {
			killed = &tombstone
		}
		return detailLoadedMsg{
			taskID: taskID, comments: comments, links: links, tombstone: killed,
			err: errors.Join(commentsErr, linksErr, tombstoneErr),
		}
	}
}

// View renders a centered bordered pane sized for the current terminal.
func (m Model) View(width, height int) string {
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
func (m Model) Overlay(background string, width, height int) string {
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

func (m Model) frame(width, height int) (string, int, int) {
	width = max(width, 1)
	height = max(height, 1)
	paneWidth := min(max(width-4, 12), maxPaneWidth, width)
	paneHeight := max(min(height-2, height), 5)
	paneHeight = min(paneHeight, height)
	innerWidth := max(paneWidth-4, 1)
	innerHeight := max(paneHeight-4, 1)

	body := m.renderBody(innerWidth)
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	maxScroll := max(0, len(lines)-innerHeight)
	start := min(m.scroll, maxScroll)
	end := min(start+innerHeight, len(lines))
	visible := strings.Join(lines[start:end], "\n")
	visible = lipgloss.NewStyle().Width(innerWidth).Height(innerHeight).Render(visible)
	footer := "esc close  ↑/↓ scroll"
	if maxScroll > 0 {
		footer = fmt.Sprintf("%s  %d/%d", footer, start+1, maxScroll+1)
	}
	content := visible + "\n" + ansi.Truncate(footer, innerWidth, "…")
	frame := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1).
		Width(paneWidth - 2).
		Height(paneHeight - 2).
		Render(content)
	return frame, paneWidth, paneHeight
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
	if strings.TrimSpace(m.task.Desc) != "" {
		sections = append(sections, renderMarkdown(m.task.Desc, width))
	}
	if len(m.task.Checks) > 0 {
		sections = append(sections, renderChecklist(m.task.Checks))
	}
	if refs := renderTaskLinks(m.links); refs != "" {
		sections = append(sections, refs)
	}
	if m.loading {
		sections = append(sections, "loading comments and context...")
	} else if m.err != nil {
		sections = append(sections, "detail error: "+safeText(m.err.Error(), false))
	} else if len(m.comments) > 0 {
		sections = append(sections, renderComments(m.comments, width))
	} else {
		sections = append(sections, "comments  none")
	}
	return strings.Join(sections, "\n\n")
}

func (m Model) metadata() string {
	blocked := ""
	if m.task.Blocked {
		blocked = "  blocked"
	}
	parts := []string{fmt.Sprintf("status %s", m.task.Status), fmt.Sprintf("priority %d", m.task.Prio)}
	if m.task.Due != "" {
		parts = append(parts, "due "+m.task.Due)
	}
	if m.task.Effort != "" {
		parts = append(parts, "effort "+m.task.Effort)
	}
	return strings.Join(parts, "  ") + blocked
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
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(max(width, 1)),
	)
	if err != nil {
		return safeText(source, true)
	}
	rendered, err := renderer.Render(parityMarkdown(safeText(source, true)))
	if err != nil {
		return safeText(source, true)
	}
	return strings.TrimSpace(rendered)
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
	sections := []string{fmt.Sprintf("comments  %d", len(comments))}
	for _, comment := range comments {
		date := comment.CreatedAt.UTC().Format("2 Jan 2006")
		header := fmt.Sprintf("c%d  %s  %s", comment.ID, safeText(comment.Author, false), date)
		sections = append(sections, header+"\n"+renderMarkdown(comment.Body, width))
	}
	return strings.Join(sections, "\n\n")
}

func safeText(text string, keepNewlines bool) string {
	text = ansi.Strip(text)
	return strings.Map(func(r rune) rune {
		if keepNewlines && (r == '\n' || r == '\t') {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, text)
}
