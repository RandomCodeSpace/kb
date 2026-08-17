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
	visibleAt  map[board.Status]map[string]int
	fullIDs    map[board.Status][]string
	fullAt     map[board.Status]map[string]int
	preview    board.Board
	previewAt  map[string]int
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
	notice      bool
}

func (s *cardMoveState) begin(current board.Board, task board.Task, statuses []board.Status, fromMouse bool) bool {
	visible := visibleTaskIDs(current, task.ID)
	visibleSlots := taskSlots(visible)
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
		visibleAt:  visibleSlots,
		fullIDs:    visible,
		fullAt:     visibleSlots,
		preview:    cloneBoard(current),
		previewAt:  boardTaskSlots(current),
		statuses:   append([]board.Status(nil), statuses...),
		fromMouse:  fromMouse,
	}
	s.statusError = false
	s.notice = false
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
	preview := repositionLiftPreview(lift)
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
		if at, ok := lift.visibleAt[status][beforeTaskID]; ok {
			slot = at
		}
	}
	if lift.target == status && lift.slot == slot {
		return board.Board{}, false
	}
	lift.dragged = true
	lift.target, lift.slot = status, slot
	preview := repositionLiftPreview(lift)
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
	s.notice = true
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
	s.notice = false
}

func previewLift(lift cardLift) board.Board {
	index := visibleSlotToFullColumnIndex(
		lift.canonical, lift.target, lift.taskID, lift.visibleIDs[lift.target], lift.slot,
	)
	return moveTaskInBoard(lift.canonical, lift.taskID, lift.target, index)
}

// repositionLiftPreview applies the current target to the lift's existing
// preview slice. The indexes are built once at lift time; each transition then
// rotates only the tasks between the old and new slots and allocates nothing.
func repositionLiftPreview(lift *cardLift) board.Board {
	fullIndex := len(lift.fullIDs[lift.target])
	if lift.slot < len(lift.visibleIDs[lift.target]) {
		anchor := lift.visibleIDs[lift.target][lift.slot]
		if at, ok := lift.fullAt[lift.target][anchor]; ok {
			fullIndex = at
		}
	}
	repositionPreviewTask(lift, lift.target, fullIndex)
	return lift.preview
}

func repositionPreviewTask(lift *cardLift, target board.Status, fullIndex int) {
	tasks := lift.preview.Tasks
	source, ok := lift.previewAt[lift.taskID]
	if !ok || source < 0 || source >= len(tasks) {
		return
	}

	destination := len(tasks)
	targetIDs := lift.fullIDs[target]
	switch {
	case fullIndex < len(targetIDs):
		destination = lift.previewAt[targetIDs[fullIndex]]
	case len(targetIDs) > 0:
		destination = lift.previewAt[targetIDs[len(targetIDs)-1]] + 1
	default:
		targetOrder := statusIndexExact(target)
		for _, status := range boardStatuses {
			ids := lift.fullIDs[status]
			if statusIndexExact(status) > targetOrder && len(ids) > 0 {
				destination = lift.previewAt[ids[0]]
				break
			}
		}
	}
	if source < destination {
		destination--
	}
	if destination == source {
		tasks[source].Status = target
		tasks[source].Position = fullIndex
		return
	}

	moving := tasks[source]
	moving.Status = target
	moving.Position = fullIndex
	if source < destination {
		copy(tasks[source:destination], tasks[source+1:destination+1])
		tasks[destination] = moving
		for index := source; index <= destination; index++ {
			lift.previewAt[tasks[index].ID] = index
		}
	} else {
		copy(tasks[destination+1:source+1], tasks[destination:source])
		tasks[destination] = moving
		for index := destination; index <= source; index++ {
			lift.previewAt[tasks[index].ID] = index
		}
	}
}

func taskSlots(columns map[board.Status][]string) map[board.Status]map[string]int {
	slots := make(map[board.Status]map[string]int, len(columns))
	for status, ids := range columns {
		column := make(map[string]int, len(ids))
		for index, id := range ids {
			column[id] = index
		}
		slots[status] = column
	}
	return slots
}

func boardTaskSlots(current board.Board) map[string]int {
	slots := make(map[string]int, len(current.Tasks))
	for index, task := range current.Tasks {
		slots[task.ID] = index
	}
	return slots
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
