// Package tui implements kb's full-screen terminal interface.
package tui

import (
	"context"
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/tui/carddetail"
)

const (
	defaultWidth  = 80
	defaultHeight = 24
	pollInterval  = time.Second
)

var boardStatuses = [...]board.Status{
	board.StatusTodo,
	board.StatusDoing,
	board.StatusDone,
	board.StatusCancelled,
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
	store           boardReader
	watcher         dataVersionReader
	user            string
	board           board.Board
	boardView       boardViewState
	filter          boardFilterState
	detail          carddetail.Model
	width           int
	height          int
	loading         bool
	reloadPending   bool
	loadErr         error
	pollErr         error
	dataVersion     int64
	haveVersion     bool
	stopped         bool
	readContext     context.Context
	now             func() time.Time
	savePreferences func(tuiPreferences) error
	preferenceErr   error
	prefSaving      bool
	prefPending     *tuiPreferences
	settings        *settingsModel
	settingsNew     func() *settingsModel
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
	detailReader, _ := store.(carddetail.Reader)
	return Model{
		store:       store,
		watcher:     watcher,
		user:        user,
		board:       board.Board{Title: "Board"},
		filter:      newBoardFilterState(),
		detail:      carddetail.New(detailReader, user),
		width:       defaultWidth,
		height:      defaultHeight,
		loading:     watcher == nil,
		readContext: ctx,
		now:         time.Now,
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
	if m.settings != nil && isSettingsMessage(message) {
		command := m.settings.Update(message)
		if m.settings.closed {
			m.settings = nil
		}
		return m, command
	}
	var detailCmd tea.Cmd
	if m.detail.IsOpen() {
		switch msg := message.(type) {
		case tea.KeyPressMsg:
			switch msg.String() {
			case "esc":
				m.detail.Close()
				return m, nil
			case "q", "ctrl+c":
				// Preserve the root quit contract while the overlay is open.
			default:
				return m, m.detail.Update(message)
			}
		case boardCardClickedMsg, boardColumnClickedMsg,
			filterTextClickedMsg, filterLabelClickedMsg, filterClearClickedMsg:
			return m, nil
		default:
			detailCmd = m.detail.Update(message)
		}
	}
	switch msg := message.(type) {
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			if m.settings != nil {
				m.settings.Close()
			}
			m.stopped = true
			m.reloadPending = false
			return m, tea.Quit
		}
		if m.settings != nil {
			command := m.settings.Update(msg)
			if m.settings.closed {
				m.settings = nil
			}
			return m, command
		}
		if handled, command := m.handleFilterKey(msg); handled {
			return m, command
		}
		switch msg.String() {
		case "q":
			m.stopped = true
			m.reloadPending = false
			return m, tea.Quit
		case "s":
			if m.settingsNew != nil {
				m.settings = m.settingsNew()
				return m, m.settings.Init()
			}
		case "enter":
			if task, ok := m.selectedTask(); ok {
				m.detail.Resize(m.width, m.height)
				return m, m.detail.Open(task)
			}
			return m, nil
		default:
			if m.boardView.handleKey(msg.String(), m.filteredBoard()) == boardToggledCancelled {
				return m, m.queuePreferences()
			}
		}
	case boardCardClickedMsg:
		m.filter.blur()
		if m.boardView.focusTask(m.filteredBoard(), msg.taskID) {
			if task, ok := m.selectedTask(); ok {
				m.detail.Resize(m.width, m.height)
				return m, m.detail.Open(task)
			}
		}
	case boardColumnClickedMsg:
		m.filter.blur()
		m.boardView.focusColumn(msg.status, m.filteredBoard())
	case filterTextClickedMsg:
		if m.settings != nil {
			return m, nil
		}
		return m, m.filter.focusText()
	case filterLabelClickedMsg:
		if m.settings != nil {
			return m, nil
		}
		return m, m.mutateFilter(func(filter *boardFilterState) { filter.toggleTag(msg.tag) })
	case filterClearClickedMsg:
		if m.settings != nil {
			return m, nil
		}
		return m, m.mutateFilter(func(filter *boardFilterState) { filter.clear() })
	case preferenceSavedMsg:
		return m, m.finishPreferences(msg)
	case tea.WindowSizeMsg:
		if msg.Width > 0 {
			m.width = msg.Width
		}
		if msg.Height > 0 {
			m.height = msg.Height
		}
		m.detail.Resize(m.width, m.height)
	case boardLoadedMsg:
		next := m.finishBoardLoad(msg)
		return m, batchCommands(detailCmd, next)
	case pollTickMsg:
		return m, m.readDataVersion()
	case dataVersionMsg:
		next := m.observeDataVersion(msg)
		return m, next
	}
	return m, detailCmd
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
	var detailCmd tea.Cmd
	if msg.err == nil {
		previous := m.filteredBoard()
		m.board = msg.board
		m.boardView.adoptBoard(previous, m.filteredBoard())
		detailCmd = m.reconcileDetail()
	}
	if !m.reloadPending {
		return detailCmd
	}
	m.reloadPending = false
	return batchCommands(detailCmd, m.startBoardLoad())
}

func (m *Model) reconcileDetail() tea.Cmd {
	if !m.detail.IsOpen() {
		return nil
	}
	taskID := m.detail.TaskID()
	for _, task := range m.board.Tasks {
		if task.ID == taskID {
			return m.detail.Refresh(task)
		}
	}
	m.detail.Close()
	return nil
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

func batchCommands(commands ...tea.Cmd) tea.Cmd {
	filtered := make([]tea.Cmd, 0, len(commands))
	for _, command := range commands {
		if command != nil {
			filtered = append(filtered, command)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	if len(filtered) == 1 {
		return filtered[0]
	}
	return tea.Batch(filtered...)
}

// View renders the responsive read-only board and wires view-derived mouse hit
// regions back into the update loop. Editing behavior arrives in later slices.
func (m Model) View() tea.View {
	content, hits := m.renderBoard()
	if m.detail.IsOpen() {
		content = m.detail.Overlay(content, m.width, m.height)
		hits = nil
	}
	if m.settings != nil {
		content = m.settings.View(m.width, m.height)
		hits = nil
	}
	view := tea.NewView(content)
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	if m.settings == nil {
		view.OnMouse = boardMouseHandler(hits)
	}
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
