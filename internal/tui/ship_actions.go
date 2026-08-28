package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
	"github.com/RandomCodeSpace/kb/internal/tui/formview"
	"github.com/RandomCodeSpace/kb/internal/tui/pointer"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
	"github.com/RandomCodeSpace/kb/internal/tui/widget"
)

type taskActionStore interface {
	boardReader
	UpdateAndMoveTask(string, string, store.TaskPatch, *board.Status, *int, func(board.Task) error) (board.Task, error)
	UpdateAndMoveTaskIfFieldsMatch(string, string, store.TaskPatch, store.TaskPatch, *board.Status, *int, func(board.Task) error) (board.Task, error)
	UpdateTaskIfFieldsMatch(string, string, store.TaskPatch, store.TaskPatch) (board.Task, error)
	CancelTask(string, string, *string) (board.Task, error)
	DeleteCancelledTask(string, string) (board.Task, error)
}

type taskActionMode uint8

const (
	taskActionClosed taskActionMode = iota
	taskActionShip
	taskActionKill
	taskActionChecklist
	taskActionPurge
)

type shipWarning struct {
	open, total int
	blocked     bool
}

func warningForShip(task board.Task) shipWarning {
	warning := shipWarning{total: len(task.Checks), blocked: task.Blocked}
	for _, check := range task.Checks {
		if !check.Done {
			warning.open++
		}
	}
	return warning
}

func (w shipWarning) needed() bool { return w.open > 0 || w.blocked }

type taskActionState struct {
	mode       taskActionMode
	task       board.Task
	target     board.Status
	index      int
	warning    shipWarning
	choice     int
	checkIndex int
	reason     textinput.Model
	mark       formview.Mark
	armed      bool
	busy       bool
	errorText  string

	// spin is the plain tier of spec section 10.2.4. Every write this dialog
	// makes is local, and section 10.2.4 splits the tiers on latency, so the
	// branded engine is not what a card move gets.
	spin spinner.Model
}

// killReasonField is the kill prompt's field name in the select-all mark.
const killReasonField = "kill-reason"

type taskActionPointerKind uint8

const (
	taskActionPointerCancel taskActionPointerKind = iota
	taskActionPointerShipChoice
	taskActionPointerKillChoice
	taskActionPointerKillReason
	taskActionPointerCheck
	taskActionPointerPurge
)

type taskActionPointerMsg struct {
	session uint64
	taskID  string
	mode    taskActionMode
	kind    taskActionPointerKind
	index   int
}

func taskActionControlID(kind taskActionPointerKind, index int) pointer.ControlID {
	return pointer.ControlID(fmt.Sprintf("task-action:%d:%d", kind, index))
}

func newTaskActionState() taskActionState {
	reason := textinput.New()
	reason.Prompt = ""
	reason.Placeholder = "e.g. superseded by the SSO work"
	reason.CharLimit = 500
	reason.SetWidth(54)
	return taskActionState{reason: reason, spin: spinner.New()}
}

func (s taskActionState) open() bool { return s.mode != taskActionClosed }

// startActionBusy raises the write flag and opens the plain tier's tick chain.
// The glyph set and the cadence come from the theme, so the dialog's spinner and
// every other plain-tier spinner in the TUI run off the one clock of spec
// section 10.3.1.
func (m *Model) startActionBusy() tea.Cmd {
	m.action.busy = true
	m.action.spin.Spinner = m.themeStyles().Spinner
	return m.action.spin.Tick
}

func (s *taskActionState) close() {
	*s = newTaskActionState()
}

func (m *Model) openShipPrompt(task board.Task, index int) tea.Cmd {
	m.cancelMoveForAction()
	m.taskActionSession++
	m.action = newTaskActionState()
	m.action.mode = taskActionShip
	m.action.task = task
	m.action.target = board.StatusDone
	m.action.index = index
	m.action.warning = warningForShip(task)
	m.action.choice = 0
	return nil
}

func (m *Model) openKillPrompt(task board.Task) tea.Cmd {
	if task.Status == board.StatusCancelled {
		m.setActionStatus("Card is already Cancelled", true)
		return nil
	}
	m.cancelMoveForAction()
	m.taskActionSession++
	m.action = newTaskActionState()
	m.action.mode = taskActionKill
	m.action.task = task
	m.action.target = board.StatusCancelled
	m.action.choice = 2
	return m.action.reason.Focus()
}

func (m *Model) openChecklist(task board.Task) tea.Cmd {
	if len(task.Checks) == 0 {
		m.setActionStatus("Checklist is empty", true)
		return nil
	}
	m.cancelMoveForAction()
	m.taskActionSession++
	m.action = newTaskActionState()
	m.action.mode = taskActionChecklist
	m.action.task = task
	return nil
}

func (m *Model) openPurgePrompt(task board.Task) tea.Cmd {
	if task.Status != board.StatusCancelled {
		m.setActionStatus("Permanent delete is available only in Cancelled", true)
		return nil
	}
	m.cancelMoveForAction()
	m.taskActionSession++
	m.action = newTaskActionState()
	m.action.mode = taskActionPurge
	m.action.task = task
	return nil
}

func (m *Model) cancelMoveForAction() {
	if m.move.lifted == nil || m.move.saving {
		return
	}
	lift := m.move.lifted
	m.board = cloneBoard(lift.canonical)
	m.boardView.focusTask(m.filteredBoard(), lift.taskID)
	m.move.lifted = nil
	m.move.status = ""
	m.move.statusError = false
	m.move.notice = false
}

func (m *Model) setActionStatus(status string, isError bool) {
	m.actionStatus = status
	m.actionStatusError = isError
	m.actionNotice = status != ""
}

func (m *Model) handleSelectedTaskAction(key string) (bool, tea.Cmd) {
	if key != "t" && key != "x" && key != "r" && key != "D" && key != "delete" && key != "backspace" {
		return false, nil
	}
	task, ok := m.actionTask()
	if !ok {
		return true, nil
	}
	switch key {
	case "t":
		return true, m.openChecklist(task)
	case "x":
		return true, m.openKillPrompt(task)
	case "r":
		if task.Status != board.StatusCancelled {
			m.setActionStatus("Restore is available only in Cancelled", true)
			return true, nil
		}
		return true, m.startRestore(task)
	default:
		return true, m.openPurgePrompt(task)
	}
}

func (m Model) actionTask() (board.Task, bool) {
	if m.detail.IsOpen() {
		return m.taskByID(m.detail.TaskID())
	}
	return m.selectedTask()
}

func isTaskActionMessage(message tea.Msg) bool {
	switch message.(type) {
	case taskActionStoredMsg, checklistStoredMsg, autoShipCheckMsg, autoShipReadyMsg, taskActionPointerMsg:
		return true
	default:
		return false
	}
}

func (m *Model) updateTaskAction(message tea.Msg) tea.Cmd {
	switch msg := message.(type) {
	case taskActionStoredMsg:
		return m.finishTaskAction(msg)
	case checklistStoredMsg:
		return m.finishChecklistWrite(msg)
	case autoShipCheckMsg:
		if m.action.busy || m.autoShipInputOwned() {
			return nil
		}
		return m.readAutoShipCandidate(msg.taskID)
	case autoShipReadyMsg:
		return m.finishAutoShipRead(msg)
	case taskActionPointerMsg:
		return m.updateTaskActionPointer(msg)
	case spinner.TickMsg:
		// The chain terminates the moment the write lands, so an idle dialog
		// costs no wake-ups (spec section 10.2.2).
		if !m.action.busy {
			return nil
		}
		next, command := m.action.spin.Update(msg)
		m.action.spin = next
		return command
	case tea.KeyPressMsg:
		if !m.action.open() {
			return nil
		}
		return m.updateTaskActionKey(msg)
	}
	return nil
}

func (m *Model) updateTaskActionPointer(msg taskActionPointerMsg) tea.Cmd {
	if !m.action.open() || m.action.busy || msg.session != m.taskActionSession ||
		msg.taskID != m.action.task.ID || msg.mode != m.action.mode {
		return nil
	}
	m.action.errorText = ""
	switch msg.kind {
	case taskActionPointerCancel:
		m.action.close()
	case taskActionPointerShipChoice:
		return m.activateShipChoice(msg.index)
	case taskActionPointerKillChoice:
		return m.activateKillChoice(msg.index)
	case taskActionPointerKillReason:
		m.action.choice = 2
	case taskActionPointerCheck:
		if msg.index >= 0 && msg.index < len(m.action.task.Checks) {
			m.action.checkIndex = msg.index
			return m.startChecklistToggle()
		}
	case taskActionPointerPurge:
		if !m.action.armed {
			m.action.armed = true
			return nil
		}
		return m.startPurge()
	}
	return nil
}

func (m *Model) updateTaskActionKey(msg tea.KeyPressMsg) tea.Cmd {
	if msg.String() == "ctrl+c" || (m.action.busy && msg.String() == "q") {
		m.stopped = true
		m.reloadPending = false
		return tea.Quit
	}
	if m.action.busy {
		return nil
	}
	// The reason field's mark runs ahead of the dialog's Escape: a marked field
	// is typing context, so the first Escape drops the mark and the dialog
	// stays open.
	if m.action.mode == taskActionKill &&
		m.action.mark.Input(killReasonField, &m.action.reason, msg) {
		return nil
	}
	if msg.String() == "esc" {
		m.action.close()
		return nil
	}
	m.action.errorText = ""
	switch m.action.mode {
	case taskActionShip:
		return m.updateShipPrompt(msg.String())
	case taskActionKill:
		return m.updateKillPrompt(msg)
	case taskActionChecklist:
		return m.updateChecklistPrompt(msg.String())
	case taskActionPurge:
		if msg.String() == "enter" || msg.String() == "space" {
			if !m.action.armed {
				m.action.armed = true
				return nil
			}
			return m.startPurge()
		}
	}
	return nil
}

func (m *Model) updateShipPrompt(key string) tea.Cmd {
	count := 2
	if m.action.warning.open > 0 {
		count = 3
	}
	switch key {
	case "left", "h", "shift+tab":
		m.action.choice = (m.action.choice - 1 + count) % count
	case "right", "l", "tab":
		m.action.choice = (m.action.choice + 1) % count
	case "enter", "space":
		return m.activateShipChoice(m.action.choice)
	}
	return nil
}

func (m *Model) activateShipChoice(choice int) tea.Cmd {
	count := 2
	if m.action.warning.open > 0 {
		count = 3
	}
	if choice < 0 || choice >= count {
		return nil
	}
	m.action.choice = choice
	if choice == 0 {
		m.action.close()
		return nil
	}
	if m.action.warning.open > 0 && choice == 1 {
		return m.startShip(true)
	}
	return m.startShip(false)
}

func (m *Model) updateKillPrompt(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "tab":
		m.action.choice = (m.action.choice + 1) % 3
		return nil
	case "shift+tab":
		m.action.choice = (m.action.choice + 2) % 3
		return nil
	case "enter":
		return m.activateKillChoice(m.action.choice)
	}
	var command tea.Cmd
	m.action.reason, command = m.action.reason.Update(msg)
	return command
}

func (m *Model) activateKillChoice(choice int) tea.Cmd {
	if choice < 0 || choice > 2 {
		return nil
	}
	m.action.choice = choice
	switch choice {
	case 0:
		m.action.close()
		return nil
	case 1:
		return m.startKill("")
	default:
		reason := strings.TrimSpace(m.action.reason.Value())
		if reason == "" {
			m.action.errorText = "Enter a reason or choose Kill without reason"
			return nil
		}
		return m.startKill(reason)
	}
}

func (m *Model) updateChecklistPrompt(key string) tea.Cmd {
	count := len(m.action.task.Checks)
	if count == 0 {
		m.action.close()
		return nil
	}
	switch key {
	case "up", "k":
		m.action.checkIndex = (m.action.checkIndex - 1 + count) % count
	case "down", "j":
		m.action.checkIndex = (m.action.checkIndex + 1) % count
	case "enter", "space":
		return m.startChecklistToggle()
	}
	return nil
}

func completionGuard(task board.Task) error {
	if warning := store.CompletionWarning(task); warning != "" {
		return store.NewCompletionBlockedError(warning, task.ID, task.Title)
	}
	return nil
}

func autoShipGuard(task board.Task) error {
	if !shippableStatus(task.Status) {
		return fmt.Errorf("auto-ship refused because card moved to %s", task.Status)
	}
	return completionGuard(task)
}

type taskActionKind uint8

const (
	actionShip taskActionKind = iota
	actionKill
	actionRestore
	actionPurge
)

type taskActionStoredMsg struct {
	kind          taskActionKind
	taskID, title string
	from, to      board.Status
	board         board.Board
	writeErr      error
	reloadErr     error
}

func (m *Model) startShip(tickAll bool) tea.Cmd {
	if m.actionStore == nil {
		m.action.errorText = "Store does not support shipping"
		return nil
	}
	action := m.action
	startBusy := m.startActionBusy()
	checks := append([]board.Check(nil), action.task.Checks...)
	patch := store.TaskPatch{}
	if tickAll {
		for i := range checks {
			checks[i].Done = true
		}
		patch.Checks = &checks
	}
	backend, user := m.actionStore, m.user
	return tea.Batch(func() tea.Msg {
		var writeErr error
		if tickAll {
			_, writeErr = backend.UpdateAndMoveTaskIfFieldsMatch(user, action.task.ID,
				store.TaskPatch{Checks: &action.task.Checks}, patch, &action.target, &action.index, nil)
		} else {
			_, writeErr = backend.UpdateAndMoveTask(user, action.task.ID, patch, &action.target, &action.index, nil)
		}
		canonical, reloadErr := backend.Board(user)
		return taskActionStoredMsg{kind: actionShip, taskID: action.task.ID, title: action.task.Title,
			from: action.task.Status, to: action.target, board: canonical, writeErr: writeErr, reloadErr: reloadErr}
	}, startBusy)
}

func (m *Model) startKill(reason string) tea.Cmd {
	if m.actionStore == nil {
		m.action.errorText = "Store does not support deleting cards"
		return nil
	}
	action := m.action
	startBusy := m.startActionBusy()
	backend, user := m.actionStore, m.user
	return tea.Batch(func() tea.Msg {
		var killReason *string
		if reason != "" {
			killReason = &reason
		}
		_, writeErr := backend.CancelTask(user, action.task.ID, killReason)
		canonical, reloadErr := backend.Board(user)
		return taskActionStoredMsg{kind: actionKill, taskID: action.task.ID, title: action.task.Title,
			from: action.task.Status, to: action.target, board: canonical,
			writeErr: writeErr, reloadErr: reloadErr}
	}, startBusy)
}

func (m *Model) startRestore(task board.Task) tea.Cmd {
	if m.actionStore == nil {
		m.setActionStatus("Restore failed: store does not support card actions", true)
		return nil
	}
	m.action = newTaskActionState()
	startBusy := m.startActionBusy()
	backend, user := m.actionStore, m.user
	target := board.StatusTodo
	return tea.Batch(func() tea.Msg {
		_, writeErr := backend.UpdateAndMoveTask(user, task.ID, store.TaskPatch{}, &target, nil, nil)
		canonical, reloadErr := backend.Board(user)
		return taskActionStoredMsg{kind: actionRestore, taskID: task.ID, title: task.Title,
			from: task.Status, to: target, board: canonical, writeErr: writeErr, reloadErr: reloadErr}
	}, startBusy)
}

func (m *Model) startPurge() tea.Cmd {
	if m.actionStore == nil {
		m.action.errorText = "Store does not support permanent delete"
		return nil
	}
	action := m.action
	startBusy := m.startActionBusy()
	backend, user := m.actionStore, m.user
	return tea.Batch(func() tea.Msg {
		_, writeErr := backend.DeleteCancelledTask(user, action.task.ID)
		canonical, reloadErr := backend.Board(user)
		return taskActionStoredMsg{kind: actionPurge, taskID: action.task.ID, title: action.task.Title,
			from: action.task.Status, board: canonical, writeErr: writeErr, reloadErr: reloadErr}
	}, startBusy)
}

type checklistStoredMsg struct {
	taskID    string
	task      board.Task
	board     board.Board
	writeErr  error
	reloadErr error
	autoShip  bool
}

func (m *Model) startChecklistToggle() tea.Cmd {
	if m.actionStore == nil {
		m.action.errorText = "Store does not support checklist writes"
		return nil
	}
	action := m.action
	if action.checkIndex < 0 || action.checkIndex >= len(action.task.Checks) {
		return nil
	}
	before := append([]board.Check(nil), action.task.Checks...)
	after := append([]board.Check(nil), before...)
	after[action.checkIndex].Done = !after[action.checkIndex].Done
	turningOn := after[action.checkIndex].Done
	autoShip := turningOn && shippableStatus(action.task.Status) && checksComplete(after)
	startBusy := m.startActionBusy()
	backend, user := m.actionStore, m.user
	return tea.Batch(func() tea.Msg {
		updated, writeErr := backend.UpdateTaskIfFieldsMatch(user, action.task.ID,
			store.TaskPatch{Checks: &before}, store.TaskPatch{Checks: &after})
		canonical, reloadErr := backend.Board(user)
		return checklistStoredMsg{taskID: action.task.ID, task: updated, board: canonical,
			writeErr: writeErr, reloadErr: reloadErr, autoShip: autoShip && writeErr == nil}
	}, startBusy)
}

type autoShipCheckMsg struct{ taskID string }

type autoShipReadyMsg struct {
	task  board.Task
	found bool
	err   error
}

func (m Model) scheduleAutoShip(taskID string) tea.Cmd {
	return theme.Tick(m.themeStyles().Timing.AutoShipDelay, autoShipCheckMsg{taskID: taskID})
}

func (m Model) readAutoShipCandidate(taskID string) tea.Cmd {
	if m.actionStore == nil {
		return nil
	}
	backend, user := m.actionStore, m.user
	return func() tea.Msg {
		current, err := backend.Board(user)
		if err != nil {
			return autoShipReadyMsg{err: err}
		}
		task, found := boardTaskByID(current, taskID)
		return autoShipReadyMsg{task: task, found: found}
	}
}

func (m *Model) finishAutoShipRead(msg autoShipReadyMsg) tea.Cmd {
	if m.action.busy || m.autoShipInputOwned() {
		return nil
	}
	if msg.err != nil {
		m.setActionStatus("Auto-ship check failed: "+msg.err.Error(), true)
		return nil
	}
	if !msg.found || !shippableStatus(msg.task.Status) || !checksComplete(msg.task.Checks) {
		return nil
	}
	if m.action.open() && (m.action.mode != taskActionChecklist || m.action.task.ID != msg.task.ID) {
		return nil
	}
	if warningForShip(msg.task).needed() {
		index := taskCount(m.board, board.StatusDone)
		return m.openShipPrompt(msg.task, index)
	}
	m.action = newTaskActionState()
	m.action.mode = taskActionClosed
	m.action.task = msg.task
	m.action.target = board.StatusDone
	m.action.index = taskCount(m.board, board.StatusDone)
	return m.startGuardedAutoShip()
}

func (m Model) autoShipInputOwned() bool {
	detailOwns := m.detail.IsOpen()
	if detailOwns && m.action.mode == taskActionChecklist && m.action.task.ID == m.detail.TaskID() && !m.detail.OwnsInput() {
		detailOwns = false
	}
	return m.helpOpen || m.settings != nil || m.editor.IsOpen() || m.adr.IsOpen() ||
		m.issueImport.IsOpen() ||
		m.filter.focus != filterUnfocused || m.move.lifted != nil || m.move.saving ||
		detailOwns
}

func (m *Model) startGuardedAutoShip() tea.Cmd {
	action := m.action
	startBusy := m.startActionBusy()
	backend, user := m.actionStore, m.user
	return tea.Batch(func() tea.Msg {
		_, writeErr := backend.UpdateAndMoveTask(user, action.task.ID, store.TaskPatch{},
			&action.target, &action.index, autoShipGuard)
		canonical, reloadErr := backend.Board(user)
		return taskActionStoredMsg{kind: actionShip, taskID: action.task.ID, title: action.task.Title,
			from: action.task.Status, to: action.target, board: canonical, writeErr: writeErr, reloadErr: reloadErr}
	}, startBusy)
}

func shippableStatus(status board.Status) bool {
	return status == board.StatusTodo || status == board.StatusDoing
}

func checksComplete(checks []board.Check) bool {
	if len(checks) == 0 {
		return false
	}
	for _, check := range checks {
		if !check.Done {
			return false
		}
	}
	return true
}

func (m *Model) finishChecklistWrite(msg checklistStoredMsg) tea.Cmd {
	m.action.busy = false
	var adopt tea.Cmd
	if msg.writeErr != nil {
		m.action.errorText = "Checklist update failed: " + msg.writeErr.Error()
		if msg.reloadErr == nil {
			adopt = m.adoptActionBoard(msg.board, msg.taskID)
		}
		return batchCommands(adopt, m.reloadAfterActionFailure(msg.reloadErr))
	}
	if msg.reloadErr == nil {
		adopt = m.adoptActionBoard(msg.board, msg.taskID)
		if task, found := boardTaskByID(msg.board, msg.taskID); found {
			m.action.task = task
		} else {
			m.action.close()
		}
	} else {
		m.action.task = msg.task
	}
	m.setActionStatus("Checklist updated: "+msg.task.Title, false)
	commands := []tea.Cmd{adopt, m.reloadAfterActionFailure(msg.reloadErr)}
	if msg.autoShip {
		commands = append(commands, m.scheduleAutoShip(msg.taskID))
	}
	return batchCommands(commands...)
}

func (m *Model) finishTaskAction(msg taskActionStoredMsg) tea.Cmd {
	m.action.busy = false
	var adopt tea.Cmd
	if msg.writeErr != nil {
		status := actionVerb(msg.kind) + " failed for " + msg.title + ": " + msg.writeErr.Error()
		m.setActionStatus(status, true)
		if m.action.open() {
			m.action.errorText = status
		}
		if msg.reloadErr == nil {
			adopt = m.adoptActionBoard(msg.board, msg.taskID)
		}
		return batchCommands(adopt, m.reloadAfterActionFailure(msg.reloadErr))
	}

	if msg.reloadErr == nil {
		adopt = m.adoptActionBoard(msg.board, msg.taskID)
	}
	celebrate := tea.Cmd(nil)
	if msg.kind == actionShip && msg.from != board.StatusDone && msg.to == board.StatusDone {
		m.recordShipped(msg.taskID)
		m.setActionStatus("Shipped "+msg.title, false)
		celebrate = m.celebrateShip()
	} else if msg.from == board.StatusDone && msg.to != board.StatusDone {
		m.unrecordShipped(msg.taskID)
		m.setActionStatus(actionSuccess(msg.kind, msg.title), false)
	} else {
		m.setActionStatus(actionSuccess(msg.kind, msg.title), false)
	}
	m.action.close()
	var reload tea.Cmd
	if msg.reloadErr != nil {
		reload = m.reloadAfterActionFailure(msg.reloadErr)
	} else if m.reloadPending {
		reload = m.reloadAfterActionFailure(nil)
	}
	return batchCommands(adopt, m.queuePreferences(), reload, celebrate)
}

func actionVerb(kind taskActionKind) string {
	switch kind {
	case actionShip:
		return "Ship"
	case actionKill:
		return "Cancel"
	case actionRestore:
		return "Restore"
	default:
		return "Permanent delete"
	}
}

func actionSuccess(kind taskActionKind, title string) string {
	switch kind {
	case actionKill:
		return "Cancelled " + title
	case actionRestore:
		return "Restored " + title + " to To Do"
	case actionPurge:
		return "Permanently deleted " + title
	default:
		return "Shipped " + title
	}
}

func (m *Model) adoptActionBoard(current board.Board, taskID string) tea.Cmd {
	previous := m.filteredBoard()
	m.board = current
	filtered := m.filteredBoard()
	m.boardView.adoptBoard(previous, filtered)
	if !m.boardView.focusTask(filtered, taskID) {
		m.boardView.normalizeSelection(filtered)
	}
	if m.detail.IsOpen() {
		if task, found := boardTaskByID(current, m.detail.TaskID()); found {
			return m.detail.Refresh(task)
		} else {
			m.detail.Close()
		}
	}
	return nil
}

func (m *Model) reloadAfterActionFailure(reloadErr error) tea.Cmd {
	if reloadErr == nil && !m.reloadPending {
		return nil
	}
	if reloadErr != nil {
		m.setActionStatus(m.actionStatus+"; canonical reload failed: "+reloadErr.Error(), true)
	}
	m.reloadPending = false
	return m.startBoardLoad()
}

type shippedRecord struct {
	Date string   `json:"date,omitempty"`
	IDs  []string `json:"ids,omitempty"`

	visibleCount int
	dateIdentity int
	revision     uint64
	normalized   bool
}

const shippedDateLayout = "2006-01-02"

func (m *Model) normalizeShipped() {
	m.normalizeShippedAt(m.now())
}

func (m *Model) normalizeShippedAt(now time.Time) {
	today := now.Format(shippedDateLayout)
	if m.shipped.Date == "" && len(m.shipped.IDs) == 0 {
		m.shipped.visibleCount = 0
		m.shipped.dateIdentity = 0
		m.shipped.normalized = true
		return
	}
	if m.shipped.Date != today {
		m.shipped = shippedRecord{
			Date: today, dateIdentity: shippedDateIdentity(now),
			revision: m.shipped.revision + 1, normalized: true,
		}
		return
	}
	seen := make(map[string]struct{}, len(m.shipped.IDs))
	sourceLength := len(m.shipped.IDs)
	ids := m.shipped.IDs[:0]
	for _, id := range m.shipped.IDs {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	m.mutateRenderPlanStats(func(stats *RenderPlanStats) { stats.ShippedIDVisits += uint64(sourceLength) })
	if !m.shipped.normalized || len(ids) != sourceLength {
		m.shipped.revision++
	}
	m.shipped.IDs = ids
	m.shipped.visibleCount = len(ids)
	m.shipped.dateIdentity = shippedDateIdentity(now)
	m.shipped.normalized = true
}

func (m *Model) adoptShippedAt(record shippedRecord, now time.Time) {
	revision := m.shipped.revision + 1
	m.shipped = record
	m.shipped.revision = revision
	m.shipped.normalized = false
	m.normalizeShippedAt(now)
}

func (m *Model) recordShipped(taskID string) {
	m.normalizeShipped()
	if taskID == "" {
		return
	}
	if m.shipped.Date == "" {
		m.shipped.Date = m.now().Format(shippedDateLayout)
		m.shipped.dateIdentity = shippedDateIdentity(m.now())
	}
	for _, id := range m.shipped.IDs {
		if id == taskID {
			return
		}
	}
	m.shipped.IDs = append(m.shipped.IDs, taskID)
	m.shipped.visibleCount++
	m.shipped.revision++
	m.shipped.normalized = true
}

func (m *Model) unrecordShipped(taskID string) {
	m.normalizeShipped()
	for index, id := range m.shipped.IDs {
		if id == taskID {
			m.shipped.IDs = append(m.shipped.IDs[:index], m.shipped.IDs[index+1:]...)
			m.shipped.visibleCount--
			m.shipped.revision++
			return
		}
	}
}

func (m Model) shippedCount() int {
	if m.shipped.dateIdentity == 0 || m.shipped.dateIdentity != shippedDateIdentity(m.renderedAt) {
		return 0
	}
	return m.shipped.visibleCount
}

func shippedDateIdentity(at time.Time) int {
	year, month, dayOfMonth := at.Date()
	return year*10000 + int(month)*100 + dayOfMonth
}

// actionLabel is one clickable run inside a dialog row. pad widens the hit
// region by that many cells on each side, which is how a padded button's own
// filled surface stays clickable even though only its label text is searched
// for in the rendered row.
type actionLabel struct {
	text  string
	kind  taskActionPointerKind
	index int
	pad   int
}

// actionRow is one rendered dialog row and the controls it carries. Only the
// labels named on a row are ever searched for in it: titles, checklist text
// and error text are untrusted and must not be able to impersonate a control.
type actionRow struct {
	content string
	labels  []actionLabel
}

// taskActionSurface composes the task action dialog over the board. Spec
// section 4: the dialog is an elevation - a shade step and a shadow - not the
// hand-drawn box frame it used to be.
func (m Model) taskActionSurface(background string) pointer.Surface {
	if !m.action.open() {
		return pointer.Surface{Content: background}
	}
	styles := m.themeStyles()
	width, height := max(m.width, 1), max(m.height, 1)
	inset := styles.Metrics.OverlayInsetX
	paneWidth := max(min(width-4, styles.Metrics.Overlay.TaskAction), 1)
	rows := m.taskActionRows(styles, max(paneWidth-2*inset, 1))
	paneHeight := min(len(rows)+2, height)
	x := max((width-paneWidth)/2, 0)
	y := max((height-paneHeight)/2, 0)

	opts := widget.OverlayOpts{
		Title:  m.taskActionTitle(),
		Seq:    m.taskActionSeq(),
		Body:   m.actionRowContent(styles, rows, paneWidth),
		Footer: m.taskActionFooter(max(paneWidth-inset, 1)),
		Width:  paneWidth,
		Height: paneHeight,
		// Spec section 10.1.4, ratified call 6: the armed two-step is the one
		// state in the TUI that recolors a header band, and the band then wears
		// the same pair section 1.9 gives the armed button.
		Armed: m.action.armed,
	}
	background = fitActionFrame(background, width, height)
	layers := append(
		[]*lipgloss.Layer{lipgloss.NewLayer(background)},
		widget.OverlayLayers(styles, opts, x, y)...,
	)
	content := fitActionFrame(lipgloss.NewCompositor(layers...).Render(), width, height)

	var hits pointer.Map
	panel := strings.Split(widget.Overlay(styles, opts), "\n")
	for index, line := range panel {
		var labels []actionLabel
		switch {
		case index == 0:
			continue
		case index == len(panel)-1:
			labels = m.taskActionFooterLabels()
		case index-1 < len(rows):
			labels = rows[index-1].labels
		}
		plain := ansi.Strip(line)
		for _, label := range labels {
			offset := strings.Index(plain, label.text)
			if offset < 0 {
				continue
			}
			start := ansi.StringWidth(plain[:offset])
			message := taskActionPointerMsg{
				session: m.taskActionSession, taskID: m.action.task.ID, mode: m.action.mode,
				kind: label.kind, index: label.index,
			}
			hits.AddControl(
				taskActionControlID(label.kind, label.index),
				pointer.Rect{
					X0: x + start - label.pad, Y0: y + index,
					X1: x + start + ansi.StringWidth(label.text) + label.pad, Y1: y + index + 1,
				},
				func(pointer.Point) tea.Msg { return message },
			)
		}
	}
	return pointer.Surface{Content: content, Pointer: hits.Handler(), Topology: hits.Topology()}
}

// actionRowContent insets every dialog row to the panel width. A section-free
// dialog has no band rows, so every row is a body row.
func (m Model) actionRowContent(styles *theme.Styles, rows []actionRow, width int) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, widget.OverlayRow(styles, row.content, width))
	}
	return out
}

// fitActionFrame keeps a composed dialog inside the terminal cell grid.
func fitActionFrame(rendered string, width, height int) string {
	lines := strings.Split(rendered, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for index := range lines {
		lines[index] = ansi.Truncate(lines[index], width, "")
	}
	return strings.Join(lines, "\n")
}

// taskActionTitle is the header band label of the open dialog.
func (m Model) taskActionTitle() string {
	switch m.action.mode {
	case taskActionShip:
		return "SHIP CARD"
	case taskActionKill:
		return "REJECT CARD"
	case taskActionChecklist:
		return "CHECKLIST"
	case taskActionPurge:
		return "DELETE CARD"
	default:
		return "CARD"
	}
}

func (m Model) taskActionSeq() string {
	if m.action.task.Seq <= 0 {
		return ""
	}
	return fmt.Sprintf("#%d", m.action.task.Seq)
}

// taskActionFooter is the hint row, which the panel carries in its footer band
// instead of spending a body row on it. Spec section 10.4.6: the rungs are
// declared once and packed to the band, and section 10.8.4 rule 1 replaces the
// ladder's head with the busy line while a write is outstanding, leaving the
// dismissal rung live as its tail.
func (m Model) taskActionFooter(width int) string {
	styles := m.themeStyles()
	line, _ := widget.Hints(styles, m.taskActionLadder(styles, width), width)
	return m.pointerState.Render(styles, taskActionControlID(taskActionPointerCancel, 0), line)
}

func (m Model) taskActionLadder(styles *theme.Styles, width int) widget.Ladder {
	dismiss := "Esc cancel"
	if m.action.mode == taskActionChecklist {
		dismiss = "Esc close"
	}
	if m.action.busy {
		return widget.Ladder{
			Head: []string{widget.Busy(styles, widget.BusyOpts{
				Frame: styles.Work.Label.Render(m.action.spin.View()),
				Label: taskActionBusyLabel,
				On:    theme.OverlayBand,
				Width: width,
			})},
			Tail: []string{dismiss},
		}
	}
	switch m.action.mode {
	case taskActionChecklist:
		return widget.Ladder{Head: []string{"j/k choose"}, Middle: []string{"Space toggle"}, Tail: []string{dismiss}}
	case taskActionPurge:
		return widget.Ladder{
			Head:   []string{"Enter arm"},
			Middle: []string{"Enter again confirm"},
			Tail:   []string{dismiss},
		}
	default:
		return widget.Ladder{Head: []string{"Tab choose"}, Middle: []string{"Enter confirm"}, Tail: []string{dismiss}}
	}
}

// taskActionBusyLabel is the dialog's busy copy. Spec section 10.8.7 puts the
// task action dialog on the plain tier: every write it makes is local.
const taskActionBusyLabel = "saving"

func (m Model) taskActionFooterLabels() []actionLabel {
	label := "Esc cancel"
	if m.action.mode == taskActionChecklist {
		label = "Esc close"
	}
	return []actionLabel{{text: label, kind: taskActionPointerCancel}}
}

// taskActionRows renders the dialog body. huh owns the fields spec section 5.2
// assigns it - the yes/no core of a confirm prompt, the choice rows and the
// disclaimer notes - and nothing else: it exposes no pointer hit regions and
// kb's keymap is frozen by v1.0.1, so the fields render kb's state and never
// receive a message.
func (m Model) taskActionRows(styles *theme.Styles, width int) []actionRow {
	a := m.action
	// Spec section 10.8.4 rule 5: a busy state never rides on a headline. The
	// " (saving...)" suffix this dialog used to append to its own question is
	// deleted and the state lives in the footer band.
	surface := styles.Overlay.FieldValue
	switch a.mode {
	case taskActionShip:
		rows := []actionRow{{content: surface.Render(actionFit("Move "+sanitizeTerminal(a.task.Title)+" to Done?", width))}}
		if notes := shipWarningNotes(a.warning); len(notes) > 0 {
			// The note already ends in a blank row of its own.
			rows = append(rows, m.huhRows(formview.HuhNote(styles, notes, width), nil)...)
		} else {
			rows = append(rows, actionRow{})
		}
		rows = append(rows, m.shipChoiceRows(styles, width)...)
		return appendActionError(styles, rows, a.errorText, shipRetryLabel(a.warning), width)
	case taskActionKill:
		reason := m.pointerState.Render(styles, taskActionControlID(taskActionPointerKillReason, 0), "Reason:") +
			" " + settingsInputDisplay(a.reason, false, true, max(width-8, 1))
		rows := []actionRow{
			{content: surface.Render(actionFit("Why reject "+sanitizeTerminal(a.task.Title)+"?", width))},
			m.huhRow(formview.HuhNote(styles, []string{"The card moves to Cancelled. The reason is optional."}, width)),
			{
				content: formview.Selection(surface, a.mark.Active(killReasonField)).
					Render(actionFit(reason, width)),
				labels: []actionLabel{{text: "Reason:", kind: taskActionPointerKillReason}},
			},
			{},
		}
		rows = append(rows, m.choiceButtonRows(styles, killChoices(), a.choice, taskActionPointerKillChoice, width)...)
		return appendActionError(styles, rows, a.errorText, "Kill with reason", width)
	case taskActionChecklist:
		rows := []actionRow{{content: surface.Render(actionFit("Checklist: "+sanitizeTerminal(a.task.Title), width))}}
		for index, check := range a.task.Checks {
			state := widget.CheckOpen
			if check.Done {
				state = widget.CheckDone
			}
			label := actionFit(sanitizeTerminal(check.Text), max(width-2, 1))
			rows = append(rows, actionRow{
				content: m.pointerState.Render(
					styles, taskActionControlID(taskActionPointerCheck, index),
					widget.Check(styles, label, state, theme.OverlaySurf, index == a.checkIndex),
				),
				labels: []actionLabel{{text: label, kind: taskActionPointerCheck, index: index}},
			})
		}
		return appendActionError(styles, rows, a.errorText, "", width)
	case taskActionPurge:
		label := "Press Enter to arm permanent delete"
		if a.armed {
			label = "ARMED - press Enter again to delete permanently"
		}
		// The purge control is armed and fired with Enter, so the resolver of
		// spec section 10.4.2 marks no hotkey on it.
		purgeText, purgeUnderline := widget.Hotkey(actionFit(label, max(width-2*choiceButtonPad, 1)), nil)
		rows := []actionRow{
			{content: surface.Render(actionFit("Delete "+sanitizeTerminal(a.task.Title)+" permanently?", width))},
			{content: styles.Overlay.FieldLabel.Render(actionFit("The card, comments, links, and kill reason are removed for good.", width))},
			{},
			{
				content: widget.Button(styles, widget.ButtonOpts{
					Text:           purgeText,
					Variant:        theme.ButtonDanger,
					Armed:          a.armed,
					Selected:       !a.armed,
					Hovered:        m.pointerState.IsHovered(taskActionControlID(taskActionPointerPurge, 0)),
					Pressed:        m.pointerState.IsPressed(taskActionControlID(taskActionPointerPurge, 0)),
					UnderlineIndex: purgeUnderline,
					Padding:        [2]int{choiceButtonPad, choiceButtonPad},
				}),
				labels: []actionLabel{{text: label, kind: taskActionPointerPurge, pad: choiceButtonPad}},
			},
		}
		// A purge failure carries no tail: the armed control is the row directly
		// above it, and section 10.8.5 grows no second control for an action that
		// already has one.
		return appendActionError(styles, rows, a.errorText, "", width)
	default:
		return nil
	}
}

// shipChoiceRows is the ship guard's choice control. Spec section 5.2 keeps
// huh's Confirm for the yes/no core - it already renders buttons, and the theme
// dresses them in the widget's own tokens - while the three-way guard renders
// as a kb button row, because huh's Select is an option list and issue #152
// requires every dialog choice to be a visible padded button.
func (m Model) shipChoiceRows(styles *theme.Styles, width int) []actionRow {
	choices := shipChoices(m.action.warning)
	if len(choices) == 2 {
		rendered := formview.HuhConfirm(styles, choices[0].label, choices[1].label, m.action.choice == 0, width)
		return m.huhRows(rendered, shipChoiceLabels(m.action.warning))
	}
	return m.choiceButtonRows(styles, choices, m.action.choice, taskActionPointerShipChoice, width)
}

// choiceButtonRows renders a dialog's choices as visible padded buttons: one
// row when the group fits the panel, one button per row when it does not, so a
// narrow terminal loses the layout rather than the choices.
func (m Model) choiceButtonRows(
	styles *theme.Styles,
	choices []dialogChoice,
	selected int,
	kind taskActionPointerKind,
	width int,
) []actionRow {
	buttons := make([]string, 0, len(choices))
	labels := make([]actionLabel, 0, len(choices))
	group := 0
	for index, choice := range choices {
		// A dialog choice moves with left/right and commits with Enter, so the
		// resolver of spec section 10.4.2 marks no hotkey on it.
		text, underline := widget.Hotkey(choice.label, nil)
		buttons = append(buttons, widget.Button(styles, widget.ButtonOpts{
			Text:           text,
			Variant:        choice.variant,
			Selected:       index == selected,
			Hovered:        m.pointerState.IsHovered(taskActionControlID(kind, index)),
			Pressed:        m.pointerState.IsPressed(taskActionControlID(kind, index)),
			UnderlineIndex: underline,
			Padding:        [2]int{choiceButtonPad, choiceButtonPad},
		}))
		labels = append(labels, actionLabel{text: text, kind: kind, index: index, pad: choiceButtonPad})
		if index > 0 {
			group += choiceButtonGap
		}
		group += ansi.StringWidth(choice.label) + 2*choiceButtonPad
	}
	if group <= width {
		return []actionRow{{
			content: widget.ButtonGroup(styles, theme.OverlaySurf, choiceButtonGap, buttons...),
			labels:  labels,
		}}
	}
	rows := make([]actionRow, 0, len(buttons))
	for index, button := range buttons {
		rows = append(rows, actionRow{content: button, labels: []actionLabel{labels[index]}})
	}
	return rows
}

// choiceButtonPad and choiceButtonGap are the button padding and the surface
// gap between two buttons in a dialog choice row.
const (
	choiceButtonPad = 1
	choiceButtonGap = 1
)

// dialogChoice is one button of a dialog choice row: the frozen label, and
// what the choice means (issue #157). Ship anyway is Danger rather than a
// warning shade: it overrides a guard the board raised, and the variant set of
// spec section 1.9 spends no fifth token family on a single button.
type dialogChoice struct {
	label   string
	variant theme.ButtonVariant
}

// shipRetryLabel is the ship dialog's affirmative, which is the control an
// error row names as the way to run the failed write again (spec section
// 10.8.5). The guard's three-way form and huh's yes/no core carry different
// affirmatives, so the label is derived from the same warning the choices are.
func shipRetryLabel(warning shipWarning) string {
	choices := shipChoices(warning)
	return choices[len(choices)-1].label
}

func shipChoices(warning shipWarning) []dialogChoice {
	choices := []dialogChoice{{label: "Cancel"}}
	if warning.open > 0 {
		choices = append(choices, dialogChoice{label: "Tick everything", variant: theme.ButtonSuccess})
	}
	return append(choices, dialogChoice{label: "Ship anyway", variant: theme.ButtonDanger})
}

func killChoices() []dialogChoice {
	return []dialogChoice{
		{label: "Cancel"},
		{label: "Kill without reason", variant: theme.ButtonDanger},
		{label: "Kill with reason", variant: theme.ButtonDanger},
	}
}

func shipChoiceLabels(warning shipWarning) []actionLabel {
	choices := shipChoices(warning)
	labels := make([]actionLabel, 0, len(choices))
	for index, choice := range choices {
		labels = append(labels, actionLabel{text: choice.label, kind: taskActionPointerShipChoice, index: index})
	}
	return labels
}

func shipWarningNotes(warning shipWarning) []string {
	notes := []string{}
	if warning.open > 0 {
		notes = append(notes, fmt.Sprintf("%d of %d checklist items are still open.", warning.open, warning.total))
	}
	if warning.blocked {
		notes = append(notes, "This card is flagged blocked.")
	}
	return notes
}

// huhRows splits a rendered huh field into dialog rows and attaches the
// controls each row turned out to carry. A row holding a pressed control is
// rendered pressed: huh has no notion of kb pointer feedback, so the row is the
// smallest run kb can promote to the reverse-video token.
func (m Model) huhRows(rendered string, labels []actionLabel) []actionRow {
	lines := strings.Split(rendered, "\n")
	rows := make([]actionRow, 0, len(lines))
	for _, line := range lines {
		row := actionRow{content: line}
		plain := ansi.Strip(line)
		for _, label := range labels {
			if !strings.Contains(plain, label.text) {
				continue
			}
			row.labels = append(row.labels, label)
			row.content = m.pointerState.Render(m.themeStyles(), taskActionControlID(label.kind, label.index), row.content)
		}
		rows = append(rows, row)
	}
	return rows
}

func (m Model) huhRow(rendered string) actionRow {
	rows := m.huhRows(rendered, nil)
	if len(rows) == 0 {
		return actionRow{}
	}
	return rows[0]
}

// appendActionError is the failure block of spec section 10.8.5: TintDanger on
// OverlaySurf, because StatusDanger on that tier measures 2.96 and fails AA; the
// Alert glyph with a hanging indent; and up to ErrorMaxLines of wrapped message
// rather than one hard cut with no truncation mark. The tail names the dialog's
// own affirmative, which is the control that will run the operation again.
func appendActionError(styles *theme.Styles, rows []actionRow, message, retry string, width int) []actionRow {
	lines := widget.Error(styles, widget.ErrorOpts{
		Message:  message,
		Key:      retry,
		On:       theme.OverlaySurf,
		Width:    width,
		MaxLines: styles.Metrics.ErrorMaxLines,
	})
	if len(lines) == 0 {
		return rows
	}
	rows = append(rows, actionRow{})
	for _, line := range lines {
		rows = append(rows, actionRow{content: line})
	}
	return rows
}

func actionFit(line string, width int) string {
	return ansi.Truncate(line, max(width, 0), "")
}
