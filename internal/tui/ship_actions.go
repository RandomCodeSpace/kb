package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
)

const autoShipDelay = 350 * time.Millisecond

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
	armed      bool
	busy       bool
	errorText  string
}

func newTaskActionState() taskActionState {
	reason := textinput.New()
	reason.Prompt = ""
	reason.Placeholder = "e.g. superseded by the SSO work"
	reason.CharLimit = 500
	reason.SetWidth(54)
	return taskActionState{reason: reason}
}

func (s taskActionState) open() bool { return s.mode != taskActionClosed }

func (s *taskActionState) close() {
	*s = newTaskActionState()
}

func (m *Model) openShipPrompt(task board.Task, index int) tea.Cmd {
	m.cancelMoveForAction()
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
	case taskActionStoredMsg, checklistStoredMsg, autoShipCheckMsg, autoShipReadyMsg:
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
	case tea.KeyPressMsg:
		if !m.action.open() {
			return nil
		}
		return m.updateTaskActionKey(msg)
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
		choice := m.action.choice
		if choice == 0 {
			m.action.close()
			return nil
		}
		if m.action.warning.open > 0 && choice == 1 {
			return m.startShip(true)
		}
		return m.startShip(false)
	}
	return nil
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
		switch m.action.choice {
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
	var command tea.Cmd
	m.action.reason, command = m.action.reason.Update(msg)
	return command
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
	m.action.busy = true
	checks := append([]board.Check(nil), action.task.Checks...)
	patch := store.TaskPatch{}
	if tickAll {
		for i := range checks {
			checks[i].Done = true
		}
		patch.Checks = &checks
	}
	backend, user := m.actionStore, m.user
	return func() tea.Msg {
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
	}
}

func (m *Model) startKill(reason string) tea.Cmd {
	if m.actionStore == nil {
		m.action.errorText = "Store does not support deleting cards"
		return nil
	}
	action := m.action
	m.action.busy = true
	backend, user := m.actionStore, m.user
	return func() tea.Msg {
		var killReason *string
		if reason != "" {
			killReason = &reason
		}
		_, writeErr := backend.CancelTask(user, action.task.ID, killReason)
		canonical, reloadErr := backend.Board(user)
		return taskActionStoredMsg{kind: actionKill, taskID: action.task.ID, title: action.task.Title,
			from: action.task.Status, to: action.target, board: canonical,
			writeErr: writeErr, reloadErr: reloadErr}
	}
}

func (m *Model) startRestore(task board.Task) tea.Cmd {
	if m.actionStore == nil {
		m.setActionStatus("Restore failed: store does not support card actions", true)
		return nil
	}
	m.action = newTaskActionState()
	m.action.busy = true
	backend, user := m.actionStore, m.user
	target := board.StatusTodo
	return func() tea.Msg {
		_, writeErr := backend.UpdateAndMoveTask(user, task.ID, store.TaskPatch{}, &target, nil, nil)
		canonical, reloadErr := backend.Board(user)
		return taskActionStoredMsg{kind: actionRestore, taskID: task.ID, title: task.Title,
			from: task.Status, to: target, board: canonical, writeErr: writeErr, reloadErr: reloadErr}
	}
}

func (m *Model) startPurge() tea.Cmd {
	if m.actionStore == nil {
		m.action.errorText = "Store does not support permanent delete"
		return nil
	}
	action := m.action
	m.action.busy = true
	backend, user := m.actionStore, m.user
	return func() tea.Msg {
		_, writeErr := backend.DeleteCancelledTask(user, action.task.ID)
		canonical, reloadErr := backend.Board(user)
		return taskActionStoredMsg{kind: actionPurge, taskID: action.task.ID, title: action.task.Title,
			from: action.task.Status, board: canonical, writeErr: writeErr, reloadErr: reloadErr}
	}
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
	m.action.busy = true
	backend, user := m.actionStore, m.user
	return func() tea.Msg {
		updated, writeErr := backend.UpdateTaskIfFieldsMatch(user, action.task.ID,
			store.TaskPatch{Checks: &before}, store.TaskPatch{Checks: &after})
		canonical, reloadErr := backend.Board(user)
		return checklistStoredMsg{taskID: action.task.ID, task: updated, board: canonical,
			writeErr: writeErr, reloadErr: reloadErr, autoShip: autoShip && writeErr == nil}
	}
}

type autoShipCheckMsg struct{ taskID string }

type autoShipReadyMsg struct {
	task  board.Task
	found bool
	err   error
}

func (m Model) scheduleAutoShip(taskID string) tea.Cmd {
	return tea.Tick(autoShipDelay, func(time.Time) tea.Msg { return autoShipCheckMsg{taskID: taskID} })
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
	return m.settings != nil || m.editor.IsOpen() || m.adr.IsOpen() ||
		m.filter.focus != filterUnfocused || m.move.lifted != nil || m.move.saving ||
		detailOwns
}

func (m *Model) startGuardedAutoShip() tea.Cmd {
	action := m.action
	m.action.busy = true
	backend, user := m.actionStore, m.user
	return func() tea.Msg {
		_, writeErr := backend.UpdateAndMoveTask(user, action.task.ID, store.TaskPatch{},
			&action.target, &action.index, autoShipGuard)
		canonical, reloadErr := backend.Board(user)
		return taskActionStoredMsg{kind: actionShip, taskID: action.task.ID, title: action.task.Title,
			from: action.task.Status, to: action.target, board: canonical, writeErr: writeErr, reloadErr: reloadErr}
	}
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
	if msg.kind == actionShip && msg.from != board.StatusDone && msg.to == board.StatusDone {
		m.recordShipped(msg.taskID)
		m.setActionStatus("Shipped "+msg.title, false)
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
	return batchCommands(adopt, m.queuePreferences(), reload)
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
}

func (m *Model) normalizeShipped() {
	today := m.now().Format("2006-01-02")
	if m.shipped.Date == "" && len(m.shipped.IDs) == 0 {
		return
	}
	if m.shipped.Date != today {
		m.shipped = shippedRecord{Date: today}
		return
	}
	seen := make(map[string]struct{}, len(m.shipped.IDs))
	source := append([]string(nil), m.shipped.IDs...)
	ids := m.shipped.IDs[:0]
	for _, id := range source {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	m.shipped.IDs = ids
}

func (m *Model) recordShipped(taskID string) {
	m.normalizeShipped()
	if m.shipped.Date == "" {
		m.shipped.Date = m.now().Format("2006-01-02")
	}
	for _, id := range m.shipped.IDs {
		if id == taskID {
			return
		}
	}
	m.shipped.IDs = append(m.shipped.IDs, taskID)
}

func (m *Model) unrecordShipped(taskID string) {
	m.normalizeShipped()
	for index, id := range m.shipped.IDs {
		if id == taskID {
			m.shipped.IDs = append(m.shipped.IDs[:index], m.shipped.IDs[index+1:]...)
			return
		}
	}
}

func (m Model) shippedCount() int {
	m.normalizeShipped()
	return len(m.shipped.IDs)
}

func (m Model) taskActionOverlay(background string) string {
	if !m.action.open() {
		return background
	}
	width, height := max(m.width, 1), max(m.height, 1)
	frame := m.renderTaskAction(max(min(width-4, 72), 1))
	frame = fitActionFrame(frame, width, height)
	paneWidth := min(ansi.StringWidth(strings.Split(frame, "\n")[0]), width)
	paneHeight := min(len(strings.Split(frame, "\n")), height)
	return lipgloss.NewCompositor(
		lipgloss.NewLayer(background),
		lipgloss.NewLayer(frame).X(max((width-paneWidth)/2, 0)).Y(max((height-paneHeight)/2, 0)).Z(3),
	).Render()
}

func (m Model) renderTaskAction(width int) string {
	width = max(width, 1)
	if width < 5 {
		lines := m.taskActionLines(1)
		if len(lines) == 0 {
			return ""
		}
		return ansi.Truncate(lines[0], width, "")
	}
	inner := max(width-4, 1)
	lines := m.taskActionLines(inner)
	for i := range lines {
		lines[i] = "│ " + padLine(ansi.Truncate(lines[i], inner, ""), inner, " ") + " │"
	}
	return "┌" + strings.Repeat("─", width-2) + "┐\n" + strings.Join(lines, "\n") +
		"\n└" + strings.Repeat("─", width-2) + "┘"
}

func fitActionFrame(frame string, width, height int) string {
	lines := strings.Split(frame, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for index := range lines {
		lines[index] = ansi.Truncate(lines[index], width, "")
	}
	return strings.Join(lines, "\n")
}

func (m Model) taskActionLines(width int) []string {
	a := m.action
	busy := ""
	if a.busy {
		busy = " (saving...)"
	}
	switch a.mode {
	case taskActionShip:
		lines := []string{"Move " + sanitizeTerminal(a.task.Title) + " to Done?" + busy}
		if a.warning.open > 0 {
			lines = append(lines, fmt.Sprintf("%d of %d checklist items are still open.", a.warning.open, a.warning.total))
		}
		if a.warning.blocked {
			lines = append(lines, "This card is flagged blocked.")
		}
		choices := []string{"Cancel"}
		if a.warning.open > 0 {
			choices = append(choices, "Tick everything")
		}
		choices = append(choices, "Ship anyway")
		lines = append(lines, "", actionChoices(choices, a.choice), "Tab choose  Enter confirm  Esc cancel")
		return appendActionError(lines, a.errorText)
	case taskActionKill:
		lines := []string{"Why reject " + sanitizeTerminal(a.task.Title) + "?" + busy,
			"The card moves to Cancelled. The reason is optional.",
			"Reason: " + settingsInputDisplay(a.reason, false, true, max(width-8, 1)), "",
			actionChoices([]string{"Cancel", "Kill without reason", "Kill with reason"}, a.choice),
			"Tab choose  Enter confirm  Esc cancel"}
		return appendActionError(lines, a.errorText)
	case taskActionChecklist:
		lines := []string{"Checklist: " + sanitizeTerminal(a.task.Title) + busy}
		for index, check := range a.task.Checks {
			marker := "  "
			if index == a.checkIndex {
				marker = "> "
			}
			box := "[ ]"
			if check.Done {
				box = "[x]"
			}
			lines = append(lines, marker+box+" "+sanitizeTerminal(check.Text))
		}
		lines = append(lines, "", "j/k choose  Space toggle  Esc close")
		return appendActionError(lines, a.errorText)
	case taskActionPurge:
		lines := []string{"Delete " + sanitizeTerminal(a.task.Title) + " permanently?" + busy,
			"The card, comments, links, and kill reason are removed for good."}
		if a.armed {
			lines = append(lines, "", "ARMED - press Enter again to delete permanently")
		} else {
			lines = append(lines, "", "Press Enter to arm permanent delete")
		}
		lines = append(lines, "Esc cancel")
		return appendActionError(lines, a.errorText)
	default:
		return nil
	}
}

func actionChoices(choices []string, selected int) string {
	parts := make([]string, len(choices))
	for index, choice := range choices {
		if index == selected {
			parts[index] = ">[" + choice + "]<"
		} else {
			parts[index] = "[" + choice + "]"
		}
	}
	return strings.Join(parts, " ")
}

func appendActionError(lines []string, message string) []string {
	if message == "" {
		return lines
	}
	return append(lines, "", "error: "+sanitizeTerminal(message))
}
