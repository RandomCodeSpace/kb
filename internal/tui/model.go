// Package tui implements kb's full-screen terminal interface.
package tui

import (
	"context"
	"errors"
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
	store         boardReader
	watcher       dataVersionReader
	user          string
	board         board.Board
	focus         int
	width         int
	height        int
	loading       bool
	reloadPending bool
	loadErr       error
	pollErr       error
	dataVersion   int64
	haveVersion   bool
	stopped       bool
	readContext   context.Context
}

// NewModel creates the root model for one local board owner.
func NewModel(store boardReader, watcher dataVersionReader, user string) Model {
	return newModel(store, watcher, user, context.Background())
}

func newModel(
	store boardReader,
	watcher dataVersionReader,
	user string,
	ctx context.Context,
) Model {
	return Model{
		store:       store,
		watcher:     watcher,
		user:        user,
		board:       board.Board{Title: "Board"},
		width:       defaultWidth,
		height:      defaultHeight,
		loading:     watcher == nil,
		readContext: ctx,
	}
}

// Init loads the first board snapshot and starts the external-write watcher.
func (m Model) Init() tea.Cmd {
	if m.stopped {
		return nil
	}
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
	if m.stopped {
		return m, nil
	}
	switch msg := message.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.stopped = true
			m.reloadPending = false
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
		next := m.finishBoardLoad(msg)
		return m, next
	case pollTickMsg:
		return m, m.readDataVersion()
	case dataVersionMsg:
		next := m.observeDataVersion(msg)
		return m, next
	}
	return m, nil
}

// observeDataVersion advances the watcher baseline and schedules exactly one
// successor poll. Baselines are opaque: only equality with the last successful
// value matters.
func (m *Model) observeDataVersion(msg dataVersionMsg) tea.Cmd {
	var load tea.Cmd
	if msg.err != nil {
		if errors.Is(msg.err, context.Canceled) && errors.Is(m.readContext.Err(), context.Canceled) {
			return nil
		}
		m.pollErr = msg.err
		if !m.haveVersion || m.loadErr != nil {
			load = m.startBoardLoad()
		}
		return pollAfter(load)
	}

	m.pollErr = nil
	initial := !m.haveVersion
	changed := !initial && msg.version != m.dataVersion
	m.dataVersion = msg.version
	m.haveVersion = true

	switch {
	case initial || changed:
		load = m.requireFreshBoard()
	case m.loadErr != nil:
		load = m.startBoardLoad()
	}
	return pollAfter(load)
}

// finishBoardLoad commits only successful snapshots. A pending freshness
// obligation starts one serialized successor without creating another poll.
func (m *Model) finishBoardLoad(msg boardLoadedMsg) tea.Cmd {
	m.loading = false
	m.loadErr = msg.err
	if msg.err == nil {
		m.board = msg.board
	}
	if !m.reloadPending {
		return nil
	}
	m.reloadPending = false
	return m.startBoardLoad()
}

// startBoardLoad starts a fallback or retry only when no load is active. The
// active load already satisfies that obligation, so it does not queue another.
func (m *Model) startBoardLoad() tea.Cmd {
	if m.loading {
		return nil
	}
	m.loading = true
	return m.loadBoard()
}

// requireFreshBoard records a new baseline/change obligation while a load is
// active. Multiple obligations coalesce into one serialized successor.
func (m *Model) requireFreshBoard() tea.Cmd {
	if m.loading {
		m.reloadPending = true
		return nil
	}
	return m.startBoardLoad()
}

func pollAfter(load tea.Cmd) tea.Cmd {
	if load == nil {
		return schedulePoll()
	}
	return tea.Batch(load, schedulePoll())
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
		version, err := m.watcher.DataVersion(m.readContext)
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
	if m.loading || (m.watcher != nil && !m.haveVersion) {
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
