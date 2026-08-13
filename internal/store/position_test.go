package store

import (
	"errors"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/board"
)

var errPositionRefused = errors.New("refused")

// seedPositionBoard builds two populated columns: todo A,B,C and doing X,Y,Z.
func seedPositionBoard(t *testing.T) *Store {
	t.Helper()
	s := newStore(t)
	for _, task := range []board.Task{
		{Title: "A", Status: board.StatusTodo},
		{Title: "B", Status: board.StatusTodo},
		{Title: "C", Status: board.StatusTodo},
		{Title: "X", Status: board.StatusDoing},
		{Title: "Y", Status: board.StatusDoing},
		{Title: "Z", Status: board.StatusDoing},
	} {
		if _, err := s.AddTask("u", task); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// columnTitles returns a column's titles in position order and fails when the
// stored positions are not compact 0..n-1.
func columnTitles(t *testing.T, s *Store, st board.Status) []string {
	t.Helper()
	tasks, err := s.ListTasks("u", st)
	if err != nil {
		t.Fatalf("ListTasks(%s): %v", st, err)
	}
	titles := make([]string, 0, len(tasks))
	for i, task := range tasks {
		if task.Position != i {
			t.Fatalf("%s position[%d] = %d, want %d", st, i, task.Position, i)
		}
		titles = append(titles, task.Title)
	}
	return titles
}

func taskByTitle(t *testing.T, s *Store, title string) board.Task {
	t.Helper()
	tasks, err := s.ListTasks("u", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if task.Title == title {
			return task
		}
	}
	t.Fatalf("no task titled %q", title)
	return board.Task{}
}

func TestUpdateAndMoveTaskIndexAcrossColumns(t *testing.T) {
	doing := board.StatusDoing

	cases := []struct {
		name      string
		index     int
		wantDoing []string
	}{
		{"front", 0, []string{"A", "X", "Y", "Z"}},
		{"middle", 2, []string{"X", "Y", "A", "Z"}},
		{"end", 3, []string{"X", "Y", "Z", "A"}},
		{"clamped beyond end", 99, []string{"X", "Y", "Z", "A"}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			s := seedPositionBoard(t)
			moved, err := s.UpdateAndMoveTask("u", taskByTitle(t, s, "A").ID, TaskPatch{}, &doing, &tt.index, nil)
			if err != nil {
				t.Fatalf("UpdateAndMoveTask: %v", err)
			}
			wantPos := tt.index
			if wantPos > 3 {
				wantPos = 3
			}
			if moved.Status != doing || moved.Position != wantPos {
				t.Fatalf("moved = %s/%d, want %s/%d", moved.Status, moved.Position, doing, wantPos)
			}
			if got := strings.Join(columnTitles(t, s, doing), ","); got != strings.Join(tt.wantDoing, ",") {
				t.Fatalf("doing = %s, want %s", got, strings.Join(tt.wantDoing, ","))
			}
			// The source column closes the gap the task left behind.
			if got := strings.Join(columnTitles(t, s, board.StatusTodo), ","); got != "B,C" {
				t.Fatalf("todo = %s, want B,C", got)
			}
		})
	}
}

func TestUpdateAndMoveTaskIndexStampsMovedAt(t *testing.T) {
	s := seedPositionBoard(t)
	before := taskByTitle(t, s, "A")
	doing, index := board.StatusDoing, 0
	moved, err := s.UpdateAndMoveTask("u", before.ID, TaskPatch{}, &doing, &index, nil)
	if err != nil {
		t.Fatalf("UpdateAndMoveTask: %v", err)
	}
	if !moved.MovedAt.After(before.MovedAt) {
		t.Fatalf("MovedAt = %v, want later than %v", moved.MovedAt, before.MovedAt)
	}
}

func TestUpdateAndMoveTaskIndexReordersInPlace(t *testing.T) {
	cases := []struct {
		name     string
		title    string
		index    int
		wantTodo []string
	}{
		{"down", "A", 2, []string{"B", "C", "A"}},
		{"up", "C", 0, []string{"C", "A", "B"}},
		{"clamped beyond end", "A", 99, []string{"B", "C", "A"}},
		{"no-op", "B", 1, []string{"A", "B", "C"}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			s := seedPositionBoard(t)
			before := taskByTitle(t, s, tt.title)
			got, err := s.UpdateAndMoveTask("u", before.ID, TaskPatch{}, nil, &tt.index, nil)
			if err != nil {
				t.Fatalf("UpdateAndMoveTask: %v", err)
			}
			if got.Status != board.StatusTodo {
				t.Fatalf("status = %s, want todo", got.Status)
			}
			// A reorder is not a move: MovedAt must survive it.
			if !got.MovedAt.Equal(before.MovedAt) {
				t.Fatalf("MovedAt = %v, want %v", got.MovedAt, before.MovedAt)
			}
			if got := strings.Join(columnTitles(t, s, board.StatusTodo), ","); got != strings.Join(tt.wantTodo, ",") {
				t.Fatalf("todo = %s, want %s", got, strings.Join(tt.wantTodo, ","))
			}
			// The other column is untouched.
			if got := strings.Join(columnTitles(t, s, board.StatusDoing), ","); got != "X,Y,Z" {
				t.Fatalf("doing = %s, want X,Y,Z", got)
			}
		})
	}
}

func TestUpdateAndMoveTaskSameStatusIndexReorders(t *testing.T) {
	s := seedPositionBoard(t)
	before := taskByTitle(t, s, "C")
	todo, index := board.StatusTodo, 0
	got, err := s.UpdateAndMoveTask("u", before.ID, TaskPatch{}, &todo, &index, nil)
	if err != nil {
		t.Fatalf("UpdateAndMoveTask: %v", err)
	}
	// Sending the destination column a card never left is a reorder, not a
	// move: MovedAt must survive it.
	if !got.MovedAt.Equal(before.MovedAt) {
		t.Fatalf("MovedAt = %v, want %v", got.MovedAt, before.MovedAt)
	}
	if got.Position != 0 {
		t.Fatalf("position = %d, want 0", got.Position)
	}
	if got := strings.Join(columnTitles(t, s, board.StatusTodo), ","); got != "C,A,B" {
		t.Fatalf("todo = %s, want C,A,B", got)
	}
}

func TestUpdateAndMoveTaskIndexReorderWithPatch(t *testing.T) {
	s := seedPositionBoard(t)
	index := 0
	got, err := s.UpdateAndMoveTask("u", taskByTitle(t, s, "C").ID, TaskPatch{Title: sptr("C2")}, nil, &index, nil)
	if err != nil {
		t.Fatalf("UpdateAndMoveTask: %v", err)
	}
	if got.Title != "C2" || got.Position != 0 {
		t.Fatalf("got = %q/%d, want C2/0", got.Title, got.Position)
	}
	if got := strings.Join(columnTitles(t, s, board.StatusTodo), ","); got != "C2,A,B" {
		t.Fatalf("todo = %s, want C2,A,B", got)
	}
}

func TestUpdateAndMoveTaskRejectsNegativeIndex(t *testing.T) {
	s := seedPositionBoard(t)
	doing := board.StatusDoing

	cases := []struct {
		name   string
		moveTo *board.Status
	}{
		{"with move", &doing},
		{"reorder only", nil},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			index := -1
			_, err := s.UpdateAndMoveTask("u", taskByTitle(t, s, "A").ID, TaskPatch{}, tt.moveTo, &index, nil)
			if err == nil || !strings.Contains(err.Error(), "store: invalid index -1") {
				t.Fatalf("err = %v, want invalid index", err)
			}
			if got := strings.Join(columnTitles(t, s, board.StatusTodo), ","); got != "A,B,C" {
				t.Fatalf("todo = %s, want A,B,C", got)
			}
		})
	}
}

func TestUpdateAndMoveTaskIndexGuardRollsBack(t *testing.T) {
	s := seedPositionBoard(t)
	done, index := board.StatusDone, 0
	refuse := func(board.Task) error { return errPositionRefused }
	if _, err := s.UpdateAndMoveTask("u", taskByTitle(t, s, "A").ID, TaskPatch{Title: sptr("A2")}, &done, &index, refuse); err != errPositionRefused {
		t.Fatalf("err = %v, want %v", err, errPositionRefused)
	}
	if got := strings.Join(columnTitles(t, s, board.StatusTodo), ","); got != "A,B,C" {
		t.Fatalf("todo = %s, want A,B,C", got)
	}
	if got := columnTitles(t, s, board.StatusDone); len(got) != 0 {
		t.Fatalf("done = %v, want empty", got)
	}
}

// TestPositionHelpersReportReadFailures drops the tasks table inside a
// transaction so the position helpers' read paths surface database errors.
func TestPositionHelpersReportReadFailures(t *testing.T) {
	s := seedPositionBoard(t)
	target := taskByTitle(t, s, "A")

	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DROP TABLE tasks`); err != nil {
		t.Fatal(err)
	}

	if _, err := columnTaskIDs(tx, "u", board.StatusTodo, ""); err == nil || !strings.Contains(err.Error(), "list column") {
		t.Fatalf("columnTaskIDs err = %v, want list column failure", err)
	}
	if _, err := repositionTask(tx, "u", board.StatusTodo, target.ID, 0); err == nil || !strings.Contains(err.Error(), "list column") {
		t.Fatalf("repositionTask err = %v, want list column failure", err)
	}
	if err := compactColumn(tx, "u", board.StatusTodo); err == nil || !strings.Contains(err.Error(), "list column") {
		t.Fatalf("compactColumn err = %v, want list column failure", err)
	}
	if _, err := moveTask(tx, "u", target, board.StatusDoing); err == nil {
		t.Fatal("moveTask succeeded without a tasks table")
	}
}

// TestPositionHelpersReportWriteFailures installs a trigger that refuses
// UPDATEs on tasks, so reads succeed while the write half of each helper
// fails.
func TestPositionHelpersReportWriteFailures(t *testing.T) {
	s := seedPositionBoard(t)
	target := taskByTitle(t, s, "A")
	mustExecCoverage(t, s, `CREATE TRIGGER refuse_task_updates BEFORE UPDATE ON tasks BEGIN SELECT RAISE(ABORT, 'update refused'); END`)

	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := writePositions(tx, "u", []string{target.ID}); err == nil || !strings.Contains(err.Error(), "set position") {
		t.Fatalf("writePositions err = %v, want set position failure", err)
	}
	if _, err := repositionTask(tx, "u", board.StatusTodo, target.ID, 0); err == nil || !strings.Contains(err.Error(), "set position") {
		t.Fatalf("repositionTask err = %v, want set position failure", err)
	}
	if err := compactColumn(tx, "u", board.StatusTodo); err == nil || !strings.Contains(err.Error(), "set position") {
		t.Fatalf("compactColumn err = %v, want set position failure", err)
	}
	if _, err := moveTask(tx, "u", target, board.StatusDoing); err == nil || !strings.Contains(err.Error(), "move task") {
		t.Fatalf("moveTask err = %v, want move task failure", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	doing, index := board.StatusDoing, 0
	if _, err := s.UpdateAndMoveTask("u", target.ID, TaskPatch{}, &doing, &index, nil); err == nil {
		t.Fatal("UpdateAndMoveTask succeeded with updates refused")
	}
	index = 1
	if _, err := s.UpdateAndMoveTask("u", target.ID, TaskPatch{}, nil, &index, nil); err == nil {
		t.Fatal("same-column reorder succeeded with updates refused")
	}
}
