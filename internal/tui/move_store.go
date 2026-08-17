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
	target    board.Status
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
		m.board = m.move.cancel("")
		m.move.status = fmt.Sprintf("Move failed for %s: store does not support card moves", title)
		m.move.statusError = true
		return nil
	}
	m.move.saving = true
	m.move.announcePosition("Dropping")
	moveStore := m.moveStore
	user := m.user
	return func() tea.Msg {
		_, writeErr := moveStore.UpdateAndMoveTask(
			user, lift.taskID, store.TaskPatch{}, &lift.target, &index, nil,
		)
		canonical, reloadErr := moveStore.Board(user)
		return cardMoveStoredMsg{
			taskID: lift.taskID, title: lift.title,
			target: lift.target, board: canonical,
			writeErr: writeErr, reloadErr: reloadErr,
		}
	}
}

func (m *Model) finishCardDrop(msg cardMoveStoredMsg) tea.Cmd {
	previous := m.board
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
	m.boardView.adoptBoard(previous, m.board)
	found := m.boardView.focusTask(m.board, msg.taskID)

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
		position := taskIndex(m.board, msg.target, msg.taskID)
		count := taskCount(m.board, msg.target)
		m.move.status = fmt.Sprintf("Dropped %s, %s, position %d of %d", msg.title,
			statusLabelTitle(msg.target), position+1, count)
	}

	if m.reloadPending || msg.reloadErr != nil {
		m.reloadPending = false
		return m.startBoardLoad()
	}
	return nil
}
