package tui

import (
	"fmt"

	"github.com/RandomCodeSpace/kb/internal/board"
)

type cardLift struct {
	canonical  board.Board
	taskID     string
	title      string
	target     board.Status
	slot       int
	visibleIDs map[board.Status][]string
	statuses   []board.Status
	fromMouse  bool
	dragged    bool
	mouseCell  struct {
		set          bool
		status       board.Status
		beforeTaskID string
	}
}

type cardMoveState struct {
	lifted      *cardLift
	saving      bool
	status      string
	statusError bool
}

func (s *cardMoveState) begin(current board.Board, task board.Task, statuses []board.Status, fromMouse bool) bool {
	visible := visibleTaskIDs(current, task.ID)
	ids := visible[task.Status]
	slot := 0
	for _, candidate := range tasksInStatus(current, task.Status) {
		if candidate.ID == task.ID {
			break
		}
		slot++
	}
	if slot > len(ids) {
		slot = len(ids)
	}
	s.lifted = &cardLift{
		canonical:  cloneBoard(current),
		taskID:     task.ID,
		title:      task.Title,
		target:     task.Status,
		slot:       slot,
		visibleIDs: visible,
		statuses:   append([]board.Status(nil), statuses...),
		fromMouse:  fromMouse,
	}
	s.statusError = false
	s.status = fmt.Sprintf("Lifted %s. Arrows or hjkl move; Enter/Space drop; Escape cancel.", task.Title)
	return true
}

func (s *cardMoveState) previewKey(key string) (board.Board, bool) {
	if s.lifted == nil || s.saving {
		return board.Board{}, false
	}
	lift := s.lifted
	changed := false
	switch key {
	case "up", "k":
		if lift.slot > 0 {
			lift.slot--
			changed = true
		}
	case "down", "j":
		if lift.slot < len(lift.visibleIDs[lift.target]) {
			lift.slot++
			changed = true
		}
	case "left", "h":
		changed = s.moveColumn(-1)
	case "right", "l":
		changed = s.moveColumn(1)
	default:
		return board.Board{}, false
	}
	if changed {
		lift.dragged = true
	}
	preview := previewLift(*lift)
	s.announcePosition("")
	return preview, true
}

func (s *cardMoveState) previewMouse(status board.Status, beforeTaskID string) (board.Board, bool) {
	if s.lifted == nil || s.saving || !s.lifted.fromMouse {
		return board.Board{}, false
	}
	lift := s.lifted
	if beforeTaskID == lift.taskID {
		return board.Board{}, false
	}
	if statusIndexExact(status) < 0 {
		return board.Board{}, false
	}
	if !containsStatus(lift.statuses, status) {
		return board.Board{}, false
	}
	if lift.mouseCell.set && lift.mouseCell.status == status && lift.mouseCell.beforeTaskID == beforeTaskID {
		return board.Board{}, false
	}
	lift.mouseCell.set = true
	lift.mouseCell.status = status
	lift.mouseCell.beforeTaskID = beforeTaskID
	ids := lift.visibleIDs[status]
	slot := len(ids)
	if beforeTaskID != "" {
		for i, id := range ids {
			if id == beforeTaskID {
				slot = i
				break
			}
		}
	}
	if lift.target == status && lift.slot == slot {
		return board.Board{}, false
	}
	lift.dragged = true
	lift.target, lift.slot = status, slot
	preview := previewLift(*lift)
	s.announcePosition("")
	return preview, true
}

func (s *cardMoveState) moveColumn(delta int) bool {
	lift := s.lifted
	current := -1
	for index, status := range lift.statuses {
		if status == lift.target {
			current = index
			break
		}
	}
	next := current + delta
	if current < 0 || next < 0 || next >= len(lift.statuses) {
		return false
	}
	lift.target = lift.statuses[next]
	lift.slot = min(lift.slot, len(lift.visibleIDs[lift.target]))
	return true
}

func (s *cardMoveState) cancel(reason string) board.Board {
	if s.lifted == nil {
		return board.Board{}
	}
	lift := s.lifted
	canonical := cloneBoard(lift.canonical)
	s.lifted = nil
	s.saving = false
	s.statusError = false
	if reason == "" {
		s.status = fmt.Sprintf("Move cancelled: %s restored.", lift.title)
	} else {
		s.status = fmt.Sprintf("Move cancelled: %s; %s.", lift.title, reason)
	}
	return canonical
}

func (s *cardMoveState) announcePosition(prefix string) {
	if s.lifted == nil {
		return
	}
	lift := s.lifted
	if prefix != "" {
		prefix += " "
	}
	s.status = fmt.Sprintf("%s%s, %s, position %d of %d", prefix, lift.title,
		statusLabelTitle(lift.target), lift.slot+1, len(lift.visibleIDs[lift.target])+1)
	s.statusError = false
}

func previewLift(lift cardLift) board.Board {
	index := visibleSlotToFullColumnIndex(
		lift.canonical, lift.target, lift.taskID, lift.visibleIDs[lift.target], lift.slot,
	)
	return moveTaskInBoard(lift.canonical, lift.taskID, lift.target, index)
}

// visibleSlotToFullColumnIndex maps an insertion slot in the cards currently
// visible to the store's full destination-column index. A slot after the last
// visible card appends to the full column, including when hidden cards trail
// the last match. This is the parity contract future filters must retain.
func visibleSlotToFullColumnIndex(
	current board.Board,
	status board.Status,
	movingID string,
	visibleIDs []string,
	slot int,
) int {
	full := make([]string, 0)
	for _, task := range current.Tasks {
		if task.Status == status && task.ID != movingID {
			full = append(full, task.ID)
		}
	}
	slot = max(slot, 0)
	if slot >= len(visibleIDs) {
		return len(full)
	}
	anchor := visibleIDs[slot]
	for index, id := range full {
		if id == anchor {
			return index
		}
	}
	return len(full)
}

func visibleTaskIDs(current board.Board, movingID string) map[board.Status][]string {
	visible := make(map[board.Status][]string, len(boardStatuses))
	for _, task := range current.Tasks {
		if task.ID != movingID {
			visible[task.Status] = append(visible[task.Status], task.ID)
		}
	}
	return visible
}

func moveTaskInBoard(current board.Board, taskID string, status board.Status, index int) board.Board {
	next := cloneBoard(current)
	var moved board.Task
	found := false
	remaining := make([]board.Task, 0, len(next.Tasks))
	for _, task := range next.Tasks {
		if task.ID == taskID {
			moved, found = task, true
			continue
		}
		remaining = append(remaining, task)
	}
	if !found {
		return next
	}
	moved.Status = status
	columns := make(map[board.Status][]board.Task, len(boardStatuses))
	for _, task := range remaining {
		columns[task.Status] = append(columns[task.Status], task)
	}
	destination := columns[status]
	index = min(max(index, 0), len(destination))
	destination = append(destination, board.Task{})
	copy(destination[index+1:], destination[index:])
	destination[index] = moved
	columns[status] = destination
	next.Tasks = next.Tasks[:0]
	for _, columnStatus := range boardStatuses {
		for position, task := range columns[columnStatus] {
			task.Position = position
			next.Tasks = append(next.Tasks, task)
		}
	}
	return next
}

func cloneBoard(current board.Board) board.Board {
	next := current
	next.Tasks = append([]board.Task(nil), current.Tasks...)
	return next
}

func containsStatus(statuses []board.Status, status board.Status) bool {
	for _, candidate := range statuses {
		if candidate == status {
			return true
		}
	}
	return false
}

func statusIndexExact(status board.Status) int {
	for index, candidate := range boardStatuses {
		if candidate == status {
			return index
		}
	}
	return -1
}

func statusLabelTitle(status board.Status) string {
	switch status {
	case board.StatusTodo:
		return "To Do"
	case board.StatusDoing:
		return "Doing"
	case board.StatusDone:
		return "Done"
	case board.StatusCancelled:
		return "Cancelled"
	default:
		return string(status)
	}
}
