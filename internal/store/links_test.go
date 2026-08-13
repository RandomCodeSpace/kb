package store

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/board"
)

func newLinkStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "kb.db"), []byte("test-secret"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	for _, title := range []string{"one", "two", "three"} {
		if _, err := s.AddTask("u", board.Task{Title: title}); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func TestLinkValidation(t *testing.T) {
	s := newLinkStore(t)

	blocker, blocked, err := s.Link("u", "1", "2")
	if err != nil || blocker.Seq != 1 || blocked.Seq != 2 {
		t.Fatalf("link = #%d blocks #%d, %v", blocker.Seq, blocked.Seq, err)
	}
	if _, _, err := s.Link("u", "1", "2"); err == nil || !strings.Contains(err.Error(), "already blocks") {
		t.Fatalf("duplicate link = %v", err)
	}
	if _, _, err := s.Link("u", "1", "1"); err == nil || !strings.Contains(err.Error(), "cannot block itself") {
		t.Fatalf("self link = %v", err)
	}
	if _, _, err := s.Link("u", "1", "9"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("dangling link = %v", err)
	}
	// 1→2, 2→3 exist; 3→1 would close the cycle.
	if _, _, err := s.Link("u", "2", "3"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Link("u", "3", "1"); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("cycle link = %v", err)
	}

	links, err := s.TaskLinks("u", taskID(t, s, "2"))
	if err != nil || len(links.Blocks) != 1 || links.Blocks[0].Seq != 3 ||
		len(links.BlockedBy) != 1 || links.BlockedBy[0].Seq != 1 {
		t.Fatalf("TaskLinks(#2) = %+v, %v", links, err)
	}

	if err := s.Unlink("u", "2", "1"); err != nil {
		t.Fatalf("unlink reversed order = %v", err)
	}
	if err := s.Unlink("u", "1", "2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unlink gone edge = %v", err)
	}
}

func TestOpenBlockersGateDone(t *testing.T) {
	s := newLinkStore(t)
	if _, _, err := s.Link("u", "1", "2"); err != nil {
		t.Fatal(err)
	}

	guard := func(board.Task) error { return nil }
	to := board.StatusDone
	if _, err := s.UpdateAndMoveTask("u", "2", TaskPatch{}, &to, guard); err == nil ||
		!strings.Contains(err.Error(), "1 open blocker (#1) still blocks #2") {
		t.Fatalf("blocked done = %v", err)
	}
	// A nil guard is a forced move: the gate does not apply.
	if _, err := s.UpdateAndMoveTask("u", "2", TaskPatch{}, &to, nil); err != nil {
		t.Fatalf("forced done = %v", err)
	}

	// Once the blocker is done (or cancelled), the gate opens.
	if _, err := s.UpdateAndMoveTask("u", "3", TaskPatch{}, &to, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Link("u", "3", "1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateAndMoveTask("u", "1", TaskPatch{}, &to, guard); err != nil {
		t.Fatalf("done with completed blocker = %v", err)
	}
}

func TestLinksFollowTaskLifecycle(t *testing.T) {
	s := newLinkStore(t)
	if _, _, err := s.Link("u", "1", "2"); err != nil {
		t.Fatal(err)
	}

	// rm of an endpoint deletes the edge.
	if _, err := s.DeleteTask("u", "1"); err != nil {
		t.Fatal(err)
	}
	links, err := s.TaskLinks("u", taskID(t, s, "2"))
	if err != nil || len(links.BlockedBy) != 0 {
		t.Fatalf("links after endpoint rm = %+v, %v", links, err)
	}

	// A replace that drops an endpoint sweeps the edge.
	if _, _, err := s.Link("u", "2", "3"); err != nil {
		t.Fatal(err)
	}
	b := board.Board{Title: "B", Tasks: []board.Task{{Title: "two", Status: board.StatusTodo, Prio: 3}}}
	if _, err := s.ReplaceBoardWithTaskIDs("u", b); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM task_links WHERE scope = 'u'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("links after replace = %d, %v", count, err)
	}
}

// taskID resolves a seq ref to the task UUID.
func taskID(t *testing.T, s *Store, ref string) string {
	t.Helper()
	task, err := s.Task("u", ref)
	if err != nil {
		t.Fatalf("resolve %q: %v", ref, err)
	}
	return task.ID
}
