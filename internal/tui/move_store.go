package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
)

type taskMoveStore interface {
	boardReader
	UpdateAndMoveTask(string, string, store.TaskPatch, *board.Status, *int, func(board.Task) error) (board.Task, error)
}

type cardMoveStoredMsg struct {
	taskID    string
	title     string
	from      board.Status
	to        board.Status
	board     board.Board
	writeErr  error
	reloadErr error
}

func (m *Model) startCardDrop() tea.Cmd {
	if m.move.lifted == nil || m.move.saving {
		return nil
	}
	lift := *m.move.lifted
	index := visibleSlotToFullColumnIndex(
		lift.canonical, lift.target, lift.taskID, lift.visibleIDs[lift.target], lift.slot,
	)
	if m.moveStore == nil {
		title := lift.title
		m.cancelCardMove("")
		m.move.status = fmt.Sprintf("Move failed for %s: store does not support card moves", title)
		m.move.statusError = true
		return nil
	}
	from := lift.target
	if task, found := boardTaskByID(lift.canonical, lift.taskID); found {
		from = task.Status
		if lift.target == board.StatusDone && from != board.StatusDone {
			if warningForShip(task).needed() {
				return m.openShipPrompt(task, index)
			}
		}
	}
	m.move.saving = true
	m.move.announcePosition("Dropping")
	moveStore := m.moveStore
	user := m.user
	var guard func(board.Task) error
	if lift.target == board.StatusDone && from != board.StatusDone {
		guard = cardCompletionGuard(lift.target)
	}
	return func() tea.Msg {
		_, writeErr := moveStore.UpdateAndMoveTask(
			user, lift.taskID, store.TaskPatch{}, &lift.target, &index, guard,
		)
		canonical, reloadErr := moveStore.Board(user)
		return cardMoveStoredMsg{
			taskID: lift.taskID, title: lift.title, from: from, to: lift.target,
			board:    canonical,
			writeErr: writeErr, reloadErr: reloadErr,
		}
	}
}

func cardCompletionGuard(target board.Status) func(board.Task) error {
	if target != board.StatusDone {
		return nil
	}
	return func(task board.Task) error {
		warning := store.CompletionWarning(task)
		if warning == "" {
			return nil
		}
		ref := task.ID
		if task.Seq > 0 {
			ref = fmt.Sprintf("#%d", task.Seq)
		}
		return store.NewCompletionBlockedError(warning, ref, task.Title)
	}
}

func (m *Model) finishCardDrop(msg cardMoveStoredMsg) tea.Cmd {
	previous := m.filteredBoard()
	lift := m.move.lifted
	fallback := m.board
	if lift != nil {
		fallback = lift.canonical
	}
	if msg.reloadErr == nil {
		m.board = msg.board
	} else if msg.writeErr != nil {
		m.board = cloneBoard(fallback)
	}
	m.move.lifted = nil
	m.move.saving = false
	m.move.statusError = msg.writeErr != nil || msg.reloadErr != nil
	m.move.notice = true
	filtered := m.filteredBoard()
	m.boardView.adoptBoard(previous, filtered)
	canonicalTask, found := boardTaskByID(m.board, msg.taskID)
	if found {
		m.boardView.focusTask(filtered, msg.taskID)
	}

	switch {
	case msg.writeErr != nil && msg.reloadErr != nil:
		m.move.status = fmt.Sprintf("Move failed for %s: %v; canonical reload failed: %v", msg.title, msg.writeErr, msg.reloadErr)
	case msg.writeErr != nil:
		m.move.status = fmt.Sprintf("Move failed for %s: %v", msg.title, msg.writeErr)
	case msg.reloadErr != nil:
		m.move.status = fmt.Sprintf("Dropped %s, but canonical reload failed: %v", msg.title, msg.reloadErr)
	case !found:
		m.move.statusError = true
		m.move.status = fmt.Sprintf("Dropped %s, but it is absent from the canonical board", msg.title)
	default:
		position := taskIndex(m.board, canonicalTask.Status, msg.taskID)
		count := taskCount(m.board, canonicalTask.Status)
		m.move.status = fmt.Sprintf("Dropped %s, %s, position %d of %d", canonicalTask.Title,
			statusLabelTitle(canonicalTask.Status), position+1, count)
	}
	preference := tea.Cmd(nil)
	if msg.writeErr == nil {
		switch {
		case msg.from != board.StatusDone && msg.to == board.StatusDone:
			m.recordShipped(msg.taskID)
			m.move.status = "Shipped " + msg.title
			preference = m.queuePreferences()
		case msg.from == board.StatusDone && msg.to != board.StatusDone:
			m.unrecordShipped(msg.taskID)
			preference = m.queuePreferences()
		}
	}

	if m.reloadPending || msg.reloadErr != nil {
		m.reloadPending = false
		return batchCommands(preference, m.startBoardLoad())
	}
	return preference
}

func boardTaskByID(current board.Board, taskID string) (board.Task, bool) {
	for _, task := range current.Tasks {
		if task.ID == taskID {
			return task, true
		}
	}
	return board.Task{}, false
}
