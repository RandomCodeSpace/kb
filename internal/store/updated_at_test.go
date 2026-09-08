package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/kb/internal/board"
)

// TestMigrateV11BackfillsUpdatedAtFromMovedAt seeds a v10 database whose task
// rows predate the column and proves the migration copies moved_at, the only
// modification signal an old row carries, rather than leaving the ALTER
// default behind.
func TestMigrateV11BackfillsUpdatedAtFromMovedAt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kb.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE meta (k TEXT PRIMARY KEY, v TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if _, err := db.Exec(migrations[i]); err != nil {
			t.Fatalf("apply v%d: %v", i+1, err)
		}
	}
	statements := []string{
		`INSERT INTO meta(k,v) VALUES ('schema_version','10')`,
		`INSERT INTO tasks(id,user,seq,title,status,prio,tags,checks,created_at,moved_at) VALUES
			('task-a','alice',1,'Edited later','doing',3,'null','null','2026-07-01T00:00:00Z','2026-07-05T09:30:00Z'),
			('task-b','alice',2,'Never moved','todo',3,'null','null','2026-07-02T00:00:00Z','2026-07-02T00:00:00Z')`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("seed v10: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s := openStoreAt(t, path)
	for _, id := range []string{"task-a", "task-b"} {
		var moved, updated string
		if err := s.db.QueryRow(`SELECT moved_at, updated_at FROM tasks WHERE id = ?`, id).Scan(&moved, &updated); err != nil {
			t.Fatal(err)
		}
		if updated != moved {
			t.Errorf("updated_at(%s) = %q, want moved_at %q", id, updated, moved)
		}
	}
	tasks, err := s.ListTasks("alice", "")
	if err != nil {
		t.Fatalf("ListTasks after migration: %v", err)
	}
	for _, task := range tasks {
		if !task.UpdatedAt.Equal(task.MovedAt) {
			t.Errorf("%s UpdatedAt = %v, want MovedAt %v", task.Title, task.UpdatedAt, task.MovedAt)
		}
	}
}

// TestUpdatedAtAdvancesOnEveryRowWrite walks each write path that rewrites a
// task row and checks that the stamp on the returned struct advances, that it
// is what the row holds, and that MovedAt only follows on a column change.
func TestUpdatedAtAdvancesOnEveryRowWrite(t *testing.T) {
	s := seedPositionBoard(t)
	read := func(id string) board.Task {
		t.Helper()
		tasks, err := s.ListTasks("u", "")
		if err != nil {
			t.Fatal(err)
		}
		for _, task := range tasks {
			if task.ID == id {
				return task
			}
		}
		t.Fatalf("task %s missing", id)
		return board.Task{}
	}
	a := taskByTitle(t, s, "A")
	if !a.UpdatedAt.Equal(a.CreatedAt) {
		t.Fatalf("fresh task UpdatedAt = %v, want CreatedAt %v", a.UpdatedAt, a.CreatedAt)
	}

	steps := []struct {
		name      string
		write     func() (board.Task, error)
		wantMoved bool
	}{
		{"field patch", func() (board.Task, error) {
			return s.UpdateTask("u", a.ID, TaskPatch{Desc: sptr("edited")})
		}, false},
		{"same-column reorder", func() (board.Task, error) {
			index := 2
			return s.UpdateAndMoveTask("u", a.ID, TaskPatch{}, nil, &index, nil)
		}, false},
		{"move", func() (board.Task, error) {
			return s.MoveTask("u", a.ID, board.StatusDoing)
		}, true},
		{"cancel", func() (board.Task, error) {
			return s.CancelTask("u", a.ID, nil)
		}, true},
		{"restore", func() (board.Task, error) {
			return s.MoveTask("u", a.ID, board.StatusTodo)
		}, true},
	}
	previous := a
	for _, step := range steps {
		time.Sleep(2 * time.Millisecond)
		got, err := step.write()
		if err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		if !got.UpdatedAt.After(previous.UpdatedAt) {
			t.Fatalf("%s: UpdatedAt = %v, want later than %v", step.name, got.UpdatedAt, previous.UpdatedAt)
		}
		if stored := read(a.ID); !stored.UpdatedAt.Equal(got.UpdatedAt) {
			t.Fatalf("%s: stored UpdatedAt = %v, returned %v", step.name, stored.UpdatedAt, got.UpdatedAt)
		}
		if step.wantMoved != !got.MovedAt.Equal(previous.MovedAt) {
			t.Fatalf("%s: MovedAt %v -> %v, want changed=%v", step.name, previous.MovedAt, got.MovedAt, step.wantMoved)
		}
		previous = got
	}
}

// TestUpdatedAtStampsDisplacedNeighbours checks that closing the hole a moved
// card leaves behind stamps the neighbours whose positions shift, with the
// same value the moved card received.
func TestUpdatedAtStampsDisplacedNeighbours(t *testing.T) {
	s := seedPositionBoard(t)
	a, c := taskByTitle(t, s, "A"), taskByTitle(t, s, "C")
	time.Sleep(2 * time.Millisecond)
	doing, index := board.StatusDoing, 0
	moved, err := s.UpdateAndMoveTask("u", a.ID, TaskPatch{}, &doing, &index, nil)
	if err != nil {
		t.Fatal(err)
	}
	// B and C slide up one slot in todo; X, Y, Z slide down one slot in doing.
	for _, title := range []string{"B", "C", "X", "Y", "Z"} {
		if got := taskByTitle(t, s, title); !got.UpdatedAt.Equal(moved.UpdatedAt) {
			t.Errorf("%s UpdatedAt = %v, want the move stamp %v", title, got.UpdatedAt, moved.UpdatedAt)
		}
	}
	if got := taskByTitle(t, s, "C"); !got.MovedAt.Equal(c.MovedAt) {
		t.Errorf("C MovedAt = %v, want untouched %v", got.MovedAt, c.MovedAt)
	}
}

// TestReplaceBoardStampsUpdatedAt covers the replacement path: a matched task
// keeps its identity and MovedAt but is rewritten, so it carries a new
// UpdatedAt.
func TestReplaceBoardStampsUpdatedAt(t *testing.T) {
	s := newStore(t)
	before, err := s.AddTask("u", board.Task{Title: "Keep"})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	if err := s.ReplaceBoard("u", board.Board{Tasks: []board.Task{{Title: "Keep", Status: board.StatusTodo, Desc: "edited"}}}); err != nil {
		t.Fatal(err)
	}
	after := taskByTitle(t, s, "Keep")
	if after.ID != before.ID {
		t.Fatalf("replacement lost identity: %s -> %s", before.ID, after.ID)
	}
	if !after.UpdatedAt.After(before.UpdatedAt) || !after.MovedAt.Equal(before.MovedAt) {
		t.Fatalf("replaced task stamps: updated %v (was %v), moved %v (was %v)", after.UpdatedAt, before.UpdatedAt, after.MovedAt, before.MovedAt)
	}
}

func TestScanTaskRejectsCorruptUpdatedAt(t *testing.T) {
	s := newStore(t)
	task, err := s.AddTask("u", board.Task{Title: "task"})
	if err != nil {
		t.Fatal(err)
	}
	mustExecCoverage(t, s, `UPDATE tasks SET updated_at = 'garbage' WHERE id = '`+task.ID+`'`)
	if _, err := s.ListTasks("u", ""); err == nil || !strings.Contains(err.Error(), "updated_at") {
		t.Fatalf("ListTasks err = %v, want updated_at parse failure", err)
	}
}
