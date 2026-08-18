// Package tui implements kb's full-screen terminal interface.
package tui

import (
	"context"
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/ai"
	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/forge"
	"github.com/RandomCodeSpace/kb/internal/store"
	"github.com/RandomCodeSpace/kb/internal/tui/adrsplit"
	"github.com/RandomCodeSpace/kb/internal/tui/carddetail"
	"github.com/RandomCodeSpace/kb/internal/tui/cardeditor"
	"github.com/RandomCodeSpace/kb/internal/tui/issueimport"
)

const (
	defaultWidth  = 80
	defaultHeight = 24
	pollInterval  = time.Second
	ctrlCKey      = "ctrl+c"
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
	store             boardReader
	moveStore         taskMoveStore
	actionStore       taskActionStore
	watcher           dataVersionReader
	user              string
	board             board.Board
	boardView         boardViewState
	filter            boardFilterState
	detail            carddetail.Model
	editor            cardeditor.Model
	adr               adrsplit.Model
	issueImport       issueimport.Model
	selectAfterLoad   string
	width             int
	height            int
	loading           bool
	reloadPending     bool
	loadErr           error
	pollErr           error
	dataVersion       int64
	haveVersion       bool
	stopped           bool
	helpOpen          bool
	readContext       context.Context
	now               func() time.Time
	savePreferences   func(tuiPreferences) error
	preferenceErr     error
	prefSaving        bool
	prefPending       *tuiPreferences
	settings          *settingsModel
	settingsNew       func() *settingsModel
	move              cardMoveState
	action            taskActionState
	actionStatus      string
	actionStatusError bool
	actionNotice      bool
	shipped           shippedRecord
}

func (m *Model) configureAI(runner *ai.Runner, ctx context.Context) {
	if runner == nil {
		return
	}
	m.editor.SetAIRunner(runner, ctx)
	adrStore, _ := m.store.(adrsplit.Store)
	m.adr = adrsplit.New(adrStore, runner, m.user, ctx)
	if direct, ok := m.store.(*store.Store); ok {
		backend := forge.New(direct, runner, nil)
		m.issueImport = issueimport.New(direct, backend, m.user, ctx)
		m.detail.SetDriftBackend(backend, ctx)
	}
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
	editorStore, _ := store.(cardeditor.Store)
	moveStore, _ := store.(taskMoveStore)
	actionStore, _ := store.(taskActionStore)
	return Model{
		store:       store,
		moveStore:   moveStore,
		actionStore: actionStore,
		watcher:     watcher,
		user:        user,
		board:       board.Board{Title: "Board"},
		filter:      newBoardFilterState(),
		detail:      carddetail.New(detailReader, user),
		editor:      cardeditor.New(editorStore, user),
		width:       defaultWidth,
		height:      defaultHeight,
		loading:     watcher == nil,
		readContext: ctx,
		now:         time.Now,
		action:      newTaskActionState(),
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
	if m.helpOpen {
		switch msg := message.(type) {
		case tea.KeyPressMsg:
			switch msg.String() {
			case "esc", "?":
				m.helpOpen = false
				return m, nil
			case "q", ctrlCKey:
				m.stopped = true
				m.reloadPending = false
				return m, tea.Quit
			default:
				return m, nil
			}
		case boardCardClickedMsg, boardColumnClickedMsg,
			filterTextClickedMsg, filterLabelClickedMsg, filterClearClickedMsg,
			boardPointerDownMsg, boardPointerMoveMsg, boardPointerUpMsg:
			return m, nil
		}
	}
	if isTaskActionMessage(message) {
		return m, m.updateTaskAction(message)
	}
	if m.action.open() || m.action.busy {
		switch message.(type) {
		case tea.KeyPressMsg:
			return m, m.updateTaskAction(message)
		case boardCardClickedMsg, boardColumnClickedMsg,
			filterTextClickedMsg, filterLabelClickedMsg, filterClearClickedMsg,
			boardPointerDownMsg, boardPointerMoveMsg, boardPointerUpMsg:
			return m, nil
		}
	}
	if carddetail.IsMutationMessage(message) {
		return m, m.updateDetail(message)
	}
	if m.issueImport.IsOpen() && issueimport.IsMessage(message) {
		command := m.issueImport.Update(message)
		if m.issueImport.ConsumeChanged() {
			return m, batchCommands(command, m.requireFreshBoard())
		}
		return m, command
	}
	if m.issueImport.IsOpen() {
		switch msg := message.(type) {
		case tea.KeyPressMsg:
			if msg.String() == ctrlCKey {
				break
			}
			command := m.issueImport.Update(msg)
			if m.issueImport.ConsumeChanged() {
				return m, batchCommands(command, m.requireFreshBoard())
			}
			return m, command
		case boardCardClickedMsg, boardColumnClickedMsg,
			filterTextClickedMsg, filterLabelClickedMsg, filterClearClickedMsg,
			boardPointerDownMsg, boardPointerMoveMsg, boardPointerUpMsg:
			return m, nil
		}
	}
	if m.actionNotice && isBoardUserInput(message) {
		m.actionNotice = false
	}
	if m.move.lifted == nil && m.move.notice && isBoardUserInput(message) {
		m.move.notice = false
	}
	if m.settings != nil && isSettingsMessage(message) {
		command := m.settings.Update(message)
		if m.settings.closed {
			m.settings = nil
		}
		return m, command
	}
	if m.editor.IsOpen() && cardeditor.IsMessage(message) {
		command := m.editor.Update(message)
		if taskID, saved := m.editor.ConsumeSaved(); saved {
			m.selectAfterLoad = taskID
			return m, batchCommands(command, m.requireFreshBoard())
		}
		return m, command
	}
	if m.editor.IsOpen() {
		switch msg := message.(type) {
		case tea.KeyPressMsg:
			if msg.String() == ctrlCKey {
				// The explicit terminal interrupt remains global.
				break
			}
			return m, m.editor.Update(msg)
		case boardCardClickedMsg, boardColumnClickedMsg,
			filterTextClickedMsg, filterLabelClickedMsg, filterClearClickedMsg,
			boardPointerDownMsg, boardPointerMoveMsg, boardPointerUpMsg:
			return m, nil
		}
	}
	if m.adr.IsOpen() && adrsplit.IsMessage(message) {
		command := m.adr.Update(message)
		if m.adr.ConsumeChanged() {
			return m, batchCommands(command, m.requireFreshBoard())
		}
		return m, command
	}
	if m.adr.IsOpen() {
		switch msg := message.(type) {
		case tea.KeyPressMsg:
			if msg.String() == ctrlCKey {
				break
			}
			return m, m.adr.Update(msg)
		case boardCardClickedMsg, boardColumnClickedMsg,
			filterTextClickedMsg, filterLabelClickedMsg, filterClearClickedMsg,
			boardPointerDownMsg, boardPointerMoveMsg, boardPointerUpMsg:
			return m, nil
		}
	}
	var detailCmd tea.Cmd
	if m.detail.IsOpen() {
		switch msg := message.(type) {
		case tea.KeyPressMsg:
			if m.detail.OwnsInput() && msg.String() != ctrlCKey {
				return m, m.updateDetail(message)
			}
			switch msg.String() {
			case "esc":
				m.detail.Close()
				return m, nil
			case "e":
				if m.editor.Enabled() {
					if task, ok := m.taskByID(m.detail.TaskID()); ok {
						return m, m.editor.OpenEdit(task)
					}
				}
				return m, nil
			case "t", "x", "r", "D", "delete", "backspace":
				if handled, command := m.handleSelectedTaskAction(msg.String()); handled {
					return m, command
				}
				return m, nil
			case "q", ctrlCKey:
				// Preserve root quit while idle detail is open.
			default:
				return m, m.updateDetail(message)
			}
		case boardCardClickedMsg, boardColumnClickedMsg,
			filterTextClickedMsg, filterLabelClickedMsg, filterClearClickedMsg,
			boardPointerDownMsg, boardPointerMoveMsg, boardPointerUpMsg:
			return m, nil
		default:
			detailCmd = m.updateDetail(message)
		}
	}
	switch msg := message.(type) {
	case tea.KeyPressMsg:
		if msg.String() == ctrlCKey {
			if m.settings != nil {
				m.settings.Close()
			}
			m.editor.CancelAsync()
			m.adr.Close()
			m.issueImport.Close()
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
		if m.move.lifted != nil {
			key := msg.String()
			if m.move.saving && key != "q" {
				return m, nil
			}
			switch key {
			case "esc":
				m.cancelCardMove("")
				return m, nil
			case "q":
				// The root quit contract remains available while a write is hung.
			case "enter", "space":
				return m, m.startCardDrop()
			case "up", "down", "left", "right", "h", "j", "k", "l":
				if preview, handled := m.move.previewKey(key); handled {
					m.board = preview
					m.boardView.focusTask(m.filteredBoard(), m.move.lifted.taskID)
				}
				return m, nil
			case "s", "c", "1", "2", "3", "4", "tab", "shift+tab", "n", "e", "a", "i", "/", "f", "x":
				m.cancelCardMove("focus changed")
			default:
				return m, nil
			}
		}
		if handled, command := m.handleFilterKey(msg); handled {
			return m, command
		}
		if handled, command := m.handleSelectedTaskAction(msg.String()); handled {
			return m, command
		}
		switch msg.String() {
		case "q":
			m.stopped = true
			m.reloadPending = false
			return m, tea.Quit
		case "?":
			m.helpOpen = true
			return m, nil
		case "s":
			if m.settingsNew != nil {
				m.settings = m.settingsNew()
				return m, m.settings.Init()
			}
		case "a":
			if m.adr.Enabled() && !m.move.saving {
				return m, m.adr.Open()
			}
		case "i":
			if m.issueImport.Enabled() && !m.writeBusy() {
				return m, m.issueImport.Open()
			}
		case "enter":
			if task, ok := m.selectedTask(); ok {
				m.detail.Resize(m.width, m.height)
				return m, m.detail.Open(task)
			}
			return m, nil
		case "n":
			if m.editor.Enabled() {
				return m, m.editor.OpenAdd(boardStatuses[m.boardView.column])
			}
		case "e":
			if m.editor.Enabled() {
				if task, ok := m.selectedTask(); ok {
					return m, m.editor.OpenEdit(task)
				}
			}
		case "space":
			if !m.loading {
				if task, ok := m.selectedTask(); ok {
					m.move.beginVisible(m.board, m.filteredBoard(), task, m.boardView.visibleStatuses(), false)
				}
			}
			return m, nil
		default:
			if m.boardView.handleKey(msg.String(), m.filteredBoard()) == boardToggledCancelled {
				return m, m.queuePreferences()
			}
		}
	case boardCardClickedMsg:
		if m.move.saving {
			return m, nil
		}
		if m.move.lifted != nil && !m.move.saving {
			m.cancelCardMove("focus changed")
		}
		m.filter.blur()
		if m.boardView.focusTask(m.filteredBoard(), msg.taskID) {
			if task, ok := m.selectedTask(); ok {
				m.detail.Resize(m.width, m.height)
				return m, m.detail.Open(task)
			}
		}
	case boardColumnClickedMsg:
		if m.move.saving {
			return m, nil
		}
		if m.move.lifted != nil && !m.move.saving {
			m.cancelCardMove("focus changed")
		}
		m.filter.blur()
		m.boardView.focusColumn(msg.status, m.filteredBoard())
	case boardPointerDownMsg:
		if m.loading || m.move.saving {
			return m, nil
		}
		if m.move.lifted != nil {
			m.cancelCardMove("focus changed")
		}
		m.filter.blur()
		if !m.boardView.focusTask(m.filteredBoard(), msg.taskID) {
			return m, nil
		}
		if task, ok := m.selectedTask(); ok {
			m.move.beginVisible(m.board, m.filteredBoard(), task, m.boardView.visibleStatuses(), true)
		}
	case boardPointerMoveMsg:
		if preview, handled := m.move.previewMouse(msg.status, msg.beforeTaskID); handled {
			m.board = preview
			m.boardView.focusTask(m.filteredBoard(), m.move.lifted.taskID)
		}
	case boardPointerUpMsg:
		if m.move.lifted == nil || !m.move.lifted.fromMouse {
			return m, nil
		}
		if !m.move.lifted.dragged {
			taskID := m.move.lifted.taskID
			m.cancelCardMove("")
			m.move.status = ""
			m.boardView.focusTask(m.filteredBoard(), taskID)
			if task, ok := m.selectedTask(); ok {
				m.detail.Resize(m.width, m.height)
				return m, m.detail.Open(task)
			}
			return m, nil
		}
		return m, m.startCardDrop()
	case cardMoveStoredMsg:
		return m, m.finishCardDrop(msg)
	case filterTextClickedMsg:
		if m.settings != nil {
			return m, nil
		}
		if m.move.saving {
			return m, nil
		}
		if m.move.lifted != nil {
			m.cancelCardMove("focus changed")
		}
		return m, m.filter.focusText()
	case filterLabelClickedMsg:
		if m.settings != nil {
			return m, nil
		}
		if m.move.saving {
			return m, nil
		}
		if m.move.lifted != nil {
			m.cancelCardMove("focus changed")
		}
		return m, m.mutateFilter(func(filter *boardFilterState) { filter.toggleTag(msg.tag) })
	case filterClearClickedMsg:
		if m.settings != nil {
			return m, nil
		}
		if m.move.saving {
			return m, nil
		}
		if m.move.lifted != nil {
			m.cancelCardMove("focus changed")
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

func (m *Model) updateDetail(message tea.Msg) tea.Cmd {
	command := m.detail.Update(message)
	if m.detail.ConsumeChanged() {
		return batchCommands(command, m.requireFreshBoard())
	}
	return command
}

func isBoardUserInput(message tea.Msg) bool {
	switch message.(type) {
	case tea.KeyPressMsg, boardCardClickedMsg, boardColumnClickedMsg,
		boardPointerDownMsg, boardPointerMoveMsg, boardPointerUpMsg,
		filterTextClickedMsg, filterLabelClickedMsg, filterClearClickedMsg:
		return true
	default:
		return false
	}
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
		filtered := m.filteredBoard()
		m.boardView.adoptBoard(previous, filtered)
		if m.selectAfterLoad != "" {
			focused := m.boardView.focusTask(filtered, m.selectAfterLoad)
			if focused || !m.reloadPending {
				m.selectAfterLoad = ""
			}
		}
		detailCmd = batchCommands(m.reconcileDetail(), m.reconcileEditor())
	}
	if !m.reloadPending {
		return detailCmd
	}
	m.reloadPending = false
	return batchCommands(detailCmd, m.startBoardLoad())
}

func (m *Model) reconcileEditor() tea.Cmd {
	if !m.editor.IsOpen() || m.editor.TaskID() == "" {
		return nil
	}
	task, found := m.taskByID(m.editor.TaskID())
	return m.editor.Refresh(task, found)
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
	if m.writeBusy() {
		m.reloadPending = true
		return nil
	}
	if m.loading {
		return nil
	}
	m.loading = true
	return m.loadBoard()
}

// requireFreshBoard records a new baseline/change obligation while a load is
// active. Multiple obligations coalesce into one serialized successor.
func (m *Model) requireFreshBoard() tea.Cmd {
	if m.writeBusy() {
		m.reloadPending = true
		return nil
	}
	if m.move.lifted != nil {
		m.cancelCardMove("board changed; refreshing")
	}
	if m.loading {
		m.reloadPending = true
		return nil
	}
	return m.startBoardLoad()
}

func (m Model) writeBusy() bool { return m.move.saving || m.action.busy }

func (m *Model) cancelCardMove(reason string) {
	if m.move.lifted == nil {
		return
	}
	taskID := m.move.lifted.taskID
	m.board = m.move.cancel(reason)
	filtered := m.filteredBoard()
	if !m.boardView.focusTask(filtered, taskID) {
		m.boardView.normalizeSelection(filtered)
	}
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
	if m.helpOpen {
		content = m.keyboardHelpOverlay(content)
		hits = nil
	}
	if m.detail.IsOpen() {
		content = m.detail.Overlay(content, m.width, m.height)
		hits = nil
	}
	if m.settings != nil {
		content = m.settings.View(m.width, m.height)
		hits = nil
	}
	if m.adr.IsOpen() {
		content = m.adr.Overlay(content, m.width, m.height)
		hits = nil
	}
	if m.editor.IsOpen() {
		content = m.editor.Overlay(content, m.width, m.height)
		hits = nil
	}
	if m.action.open() {
		content = m.taskActionOverlay(content)
		hits = nil
	}
	if m.issueImport.IsOpen() {
		content = m.issueImport.Overlay(content, m.width, m.height)
		hits = nil
	}
	view := tea.NewView(content)
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	if !m.helpOpen && m.settings == nil && !m.editor.IsOpen() && !m.adr.IsOpen() && !m.action.open() && !m.issueImport.IsOpen() && !m.detail.OwnsInput() {
		pointerActive := m.move.lifted != nil && m.move.lifted.fromMouse
		view.OnMouse = boardMouseHandler(hits, pointerActive)
	}
	return view
}

func (m Model) taskByID(id string) (board.Task, bool) {
	for _, task := range m.board.Tasks {
		if task.ID == id {
			return task, true
		}
	}
	return board.Task{}, false
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
