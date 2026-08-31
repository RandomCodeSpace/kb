// Package board holds the kanban board model and its markdown wire codec.
//
// The markdown grammar preserves the frozen wire format used by older clients.
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

// The priority scale, collapsed to three values by issue #234. The stored
// representation stays the integer the tasks.prio column has always held: the
// card renders the digit (spec section 3.4), the board sorts on it, and the
// frozen markdown and JSON wires carry it. Names are a surface vocabulary, not
// a storage change.
const (
	PrioHigh   = 1
	PrioMedium = 2
	PrioLow    = 3

	// PrioDefault is the priority a task takes when none is given.
	PrioDefault = PrioLow
)

// PrioNames maps each priority to its canonical name, low to high urgency.
var PrioNames = map[int]string{
	PrioHigh:   "high",
	PrioMedium: "medium",
	PrioLow:    "low",
}

// ValidPrio reports whether p is one of the three priorities.
func ValidPrio(p int) bool { return p >= PrioHigh && p <= PrioLow }

// NormalizePrio folds any value the three-value scale does not name onto
// PrioLow. It is the read-side counterpart of the schema v10 migration: a
// legacy 4 meant low before the collapse and means low after it, and an unset
// or corrupt value takes the same default a task with no priority takes.
func NormalizePrio(p int) int {
	if ValidPrio(p) {
		return p
	}
	return PrioLow
}

// PrioName is the canonical name of p, normalized onto the scale first.
func PrioName(p int) string { return PrioNames[NormalizePrio(p)] }

// Check is a single checklist item on a task.
type Check struct {
	Text string
	Done bool
}

// Task is one card on the board.
//
// Due is "YYYY-MM-DD" or empty; Effort is "S", "M", "L", or empty; Prio is
// 1..3 - 1 high, 2 medium, 3 low - with 3 the default (a Prio of 3 is omitted
// from the wire format). Issue #234 collapsed the scale from four values; the
// wire reader still accepts a legacy !4 and normalizes it to 3, which is what
// it always meant.
// Blocked marks a task as blocked and rides the wire as the "%blocked"
// title-line token (written only when true). Tags are plain ("backend") or
// scoped ("type::bug"). Position is the 0-based ordinal within the task's
// column and, like ID, Seq, CreatedAt, and MovedAt, is metadata not carried
// by the wire format. Seq is the task's stable per-board sequence number
// (#n): assigned once on creation, never reused, 0 when unknown, such as a
// task parsed from legacy Markdown.
type Task struct {
	ID        string
	Seq       int
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
