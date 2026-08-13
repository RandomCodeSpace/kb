package store

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/board"
)

func newCommentStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "kb.db"), []byte("test-secret"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestCommentLifecycle(t *testing.T) {
	s := newCommentStore(t)
	task, err := s.AddTask("u", board.Task{Title: "Discuss"})
	if err != nil {
		t.Fatal(err)
	}

	// Add by stable number; ids start at c1 and carry the task's identity.
	c1, err := s.AddComment("u", "1", "alice", "first finding")
	if err != nil || c1.ID != 1 || c1.TaskID != task.ID || c1.TaskSeq != 1 || c1.Author != "alice" {
		t.Fatalf("first comment = %+v, %v", c1, err)
	}
	c2, err := s.AddComment("u", task.ID, "bob", "second finding")
	if err != nil || c2.ID != 2 {
		t.Fatalf("second comment = %+v, %v", c2, err)
	}

	comments, err := s.Comments("u", "#1")
	if err != nil || len(comments) != 2 || comments[0].ID != 1 || comments[1].ID != 2 {
		t.Fatalf("comments = %+v, %v", comments, err)
	}

	// Empty bodies are refused; unknown tasks are not found.
	if _, err := s.AddComment("u", "1", "alice", "   "); err == nil {
		t.Fatal("empty comment body accepted")
	}
	if _, err := s.AddComment("u", "9", "alice", "text"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("comment on missing task = %v", err)
	}

	// Delete retires the id for good: the next comment is c3, not c2.
	deleted, err := s.DeleteComment("u", 2)
	if err != nil || deleted.ID != 2 || deleted.Body != "second finding" {
		t.Fatalf("delete = %+v, %v", deleted, err)
	}
	if _, err := s.DeleteComment("u", 2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("double delete = %v", err)
	}
	c3, err := s.AddComment("u", "1", "alice", "third finding")
	if err != nil || c3.ID != 3 {
		t.Fatalf("post-delete comment = %+v, %v (ids must never be reused)", c3, err)
	}
}

func TestCommentsFollowTaskLifecycle(t *testing.T) {
	s := newCommentStore(t)
	task, err := s.AddTask("u", board.Task{Title: "Keep"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddComment("u", "1", "u", "survives replace"); err != nil {
		t.Fatal(err)
	}

	// ReplaceBoard preserving the task's identity keeps its comments.
	b := board.Board{Title: "B", Tasks: []board.Task{{Title: "Keep", Status: board.StatusTodo, Prio: 3}}}
	if _, err := s.ReplaceBoardWithTaskIDs("u", b); err != nil {
		t.Fatal(err)
	}
	comments, err := s.Comments("u", "1")
	if err != nil || len(comments) != 1 {
		t.Fatalf("comments after identity-preserving replace = %+v, %v", comments, err)
	}

	// A replace that drops the task sweeps its comments with it.
	empty := board.Board{Title: "B", Tasks: []board.Task{{Title: "Different", Status: board.StatusTodo, Prio: 3}}}
	if _, err := s.ReplaceBoardWithTaskIDs("u", empty); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM comments WHERE scope = 'u'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("orphaned comments after replace = %d, %v", count, err)
	}

	// Hard-deleting a task deletes its comments in the same transaction.
	if _, err := s.AddComment("u", task2Ref(t, s), "u", "dies with rm"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteTask("u", task2Ref(t, s)); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM comments WHERE scope = 'u'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("comments after task rm = %d, %v", count, err)
	}
	_ = task
}

// task2Ref returns the ref of the sole remaining task on u's board.
func task2Ref(t *testing.T, s *Store) string {
	t.Helper()
	tasks, err := s.ListTasks("u", "")
	if err != nil || len(tasks) != 1 {
		t.Fatalf("expected exactly one task: %+v, %v", tasks, err)
	}
	return tasks[0].ID
}
