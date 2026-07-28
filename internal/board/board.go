// Package board holds the kanban board model and its markdown wire codec.
//
// The markdown grammar mirrors src/lib/markdown.ts exactly; the two codecs
// must stay in lockstep.
package board

import "time"

// Status is a board column: "todo", "doing", "done", or "cancelled".
type Status string

// The valid statuses, in column order.
const (
	StatusTodo      Status = "todo"
	StatusDoing     Status = "doing"
	StatusDone      Status = "done"
	StatusCancelled Status = "cancelled"
)

// Statuses lists the valid statuses in canonical column order.
var Statuses = []Status{StatusTodo, StatusDoing, StatusDone, StatusCancelled}

// Valid reports whether s is one of the known statuses.
func (s Status) Valid() bool {
	switch s {
	case StatusTodo, StatusDoing, StatusDone, StatusCancelled:
		return true
	}
	return false
}

// statusLabel maps a status to its markdown H2 section header text.
var statusLabel = map[Status]string{
	StatusTodo:      "To Do",
	StatusDoing:     "Doing",
	StatusDone:      "Done",
	StatusCancelled: "Cancelled",
}

// Check is a single checklist item on a task.
type Check struct {
	Text string
	Done bool
}

// Task is one card on the board.
//
// Due is "YYYY-MM-DD" or empty; Effort is "S", "M", "L", or empty; Prio is
// 1..4 with 3 the default (a Prio of 3 is omitted from the wire format).
// Blocked marks a task as blocked and rides the wire as the "%blocked"
// title-line token (written only when true). Tags are plain ("backend") or
// scoped ("type::bug"). Position is the 0-based ordinal within the task's
// column and, like ID, CreatedAt, and MovedAt, is metadata not carried by
// the wire format.
type Task struct {
	ID        string
	Emoji     string
	Title     string
	Desc      string
	Status    Status
	Blocked   bool
	Prio      int
	Due       string
	Effort    string
	Tags      []string
	Checks    []Check
	Position  int
	CreatedAt time.Time
	MovedAt   time.Time
}

// Board is a titled collection of tasks.
type Board struct {
	Title string
	Tasks []Task
}
