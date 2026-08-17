// Package tui implements kb's full-screen terminal interface.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/board"
)

const (
	defaultWidth   = 80
	defaultHeight  = 24
	pollInterval   = time.Second
	wideBoardWidth = 100
)

var visibleStatuses = [...]board.Status{
	board.StatusTodo,
	board.StatusDoing,
	board.StatusDone,
}

type boardReader interface {
	Board(user string) (board.Board, error)
}

type dataVersionReader interface {
	DataVersion(context.Context) (int64, error)
}

type boardLoadedMsg struct {
	board board.Board
	err   error
}

type dataVersionMsg struct {
	version int64
	err     error
}

type pollTickMsg struct{}

// Model is the single Bubble Tea root. It owns board state, focus, terminal
// dimensions, and all message routing; commands perform IO and only return
// messages, so Update remains deterministic.
type Model struct {
	store       boardReader
	watcher     dataVersionReader
	user        string
	board       board.Board
	focus       int
	width       int
	height      int
	loading     bool
	loadErr     error
	pollErr     error
	dataVersion int64
	haveVersion bool
}

// NewModel creates the root model for one local board owner.
func NewModel(store boardReader, watcher dataVersionReader, user string) Model {
	return Model{
		store:   store,
		watcher: watcher,
		user:    user,
		board:   board.Board{Title: "Board"},
		width:   defaultWidth,
		height:  defaultHeight,
		loading: true,
	}
}

// Init loads the first board snapshot and starts the external-write watcher.
func (m Model) Init() tea.Cmd {
	if m.watcher != nil {
		// Establish the connection-local baseline before loading. If these ran
		// concurrently, a commit between the stale load and a later baseline
		// could be absorbed into that baseline and never trigger a refresh.
		return m.readDataVersion()
	}
	return m.loadBoard()
}

// Update handles global messages before any future pane-specific routing.
func (m Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "left", "h", "shift+tab":
			m.focus = (m.focus + len(visibleStatuses) - 1) % len(visibleStatuses)
		case "right", "l", "tab":
			m.focus = (m.focus + 1) % len(visibleStatuses)
		}
	case tea.WindowSizeMsg:
		if msg.Width > 0 {
			m.width = msg.Width
		}
		if msg.Height > 0 {
			m.height = msg.Height
		}
	case boardLoadedMsg:
		m.loading = false
		m.loadErr = msg.err
		if msg.err == nil {
			m.board = msg.board
		}
	case pollTickMsg:
		return m, m.readDataVersion()
	case dataVersionMsg:
		if msg.err != nil {
			m.pollErr = msg.err
			if (!m.haveVersion && m.loading) || m.loadErr != nil {
				m.loading = true
				return m, tea.Batch(m.loadBoard(), schedulePoll())
			}
			return m, schedulePoll()
		}
		m.pollErr = nil
		initial := !m.haveVersion
		changed := !initial && msg.version != m.dataVersion
		m.dataVersion = msg.version
		m.haveVersion = true
		if initial || changed || m.loadErr != nil {
			m.loading = true
			return m, tea.Batch(m.loadBoard(), schedulePoll())
		}
		return m, schedulePoll()
	}
	return m, nil
}

// View renders the board frame. Cards and editing behavior arrive in later
// wayfinder slices; this scaffold deliberately renders only per-column counts.
func (m Model) View() tea.View {
	view := tea.NewView(m.render())
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	return view
}

func (m Model) loadBoard() tea.Cmd {
	return func() tea.Msg {
		loaded, err := m.store.Board(m.user)
		return boardLoadedMsg{board: loaded, err: err}
	}
}

func (m Model) readDataVersion() tea.Cmd {
	return func() tea.Msg {
		version, err := m.watcher.DataVersion(context.Background())
		return dataVersionMsg{version: version, err: err}
	}
}

func schedulePoll() tea.Cmd {
	return tea.Tick(pollInterval, func(time.Time) tea.Msg { return pollTickMsg{} })
}

func (m Model) render() string {
	width := max(m.width, 1)
	height := max(m.height, 8)
	title := strings.TrimSpace(m.board.Title)
	if title == "" {
		title = "Board"
	}
	header := fitLine(fmt.Sprintf("kb / %s / %s", title, m.user), width)

	statuses := visibleStatuses[:]
	if width < wideBoardWidth {
		statuses = visibleStatuses[m.focus : m.focus+1]
	}
	gaps := len(statuses) - 1
	columnWidth := (width - gaps) / len(statuses)
	columnHeight := max(height-4, 3)
	columns := make([]string, 0, len(statuses))
	for _, status := range statuses {
		columns = append(columns, m.renderColumn(status, columnWidth, columnHeight))
	}
	body := lipgloss.JoinHorizontal(lipgloss.Top, columns...)
	if len(columns) > 1 {
		body = lipgloss.JoinHorizontal(lipgloss.Top, intersperse(columns, " ")...)
	}

	status := "ready"
	if m.loading {
		status = "loading board..."
	}
	if m.loadErr != nil {
		status = "error: " + m.loadErr.Error()
	} else if m.pollErr != nil {
		status = "error: " + m.pollErr.Error()
	}
	footer := fitLine(status+" | q quit | tab/shift+tab focus", width)
	return strings.Join([]string{header, body, footer}, "\n")
}

func (m Model) renderColumn(status board.Status, width, height int) string {
	label := map[board.Status]string{
		board.StatusTodo:  "TO DO",
		board.StatusDoing: "DOING",
		board.StatusDone:  "DONE",
	}[status]
	if visibleStatuses[m.focus] == status {
		label = "[" + label + "]"
	}
	count := 0
	for _, task := range m.board.Tasks {
		if task.Status == status {
			count++
		}
	}
	state := "(empty)"
	if count > 0 {
		state = fmt.Sprintf("%d card(s)", count)
	}
	if width < 3 {
		return strings.Join([]string{fitLine(label, width), "", fitLine(state, width)}, "\n")
	}
	innerWidth := width - 2
	contents := strings.Join([]string{fitLine(label, innerWidth), "", fitLine(state, innerWidth)}, "\n")
	return lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		Width(innerWidth).
		Height(max(height-2, 1)).
		Render(contents)
}

func fitLine(line string, width int) string {
	return ansi.Truncate(line, max(width, 0), "")
}

func intersperse(values []string, separator string) []string {
	out := make([]string, 0, len(values)*2-1)
	for i, value := range values {
		if i > 0 {
			out = append(out, separator)
		}
		out = append(out, value)
	}
	return out
}
