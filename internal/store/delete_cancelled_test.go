package store

import (
	"errors"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/board"
)

func TestDeleteCancelledTaskEnforcesStatusInsideTheWrite(t *testing.T) {
	s := newStore(t)
	live, err := s.AddTask("alice", board.Task{Title: "Still live", Status: board.StatusTodo})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteCancelledTask("alice", live.ID); !errors.Is(err, ErrTaskNotCancelled) {
		t.Fatalf("DeleteCancelledTask(live) = %v, want ErrTaskNotCancelled", err)
	}
	if _, err := s.UpdateTask("alice", live.ID, TaskPatch{}); err != nil {
		t.Fatalf("refused purge removed live task: %v", err)
	}

	cancelled, err := s.MoveTask("alice", live.ID, board.StatusCancelled)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordTombstone("alice", cancelled.ID, "superseded"); err != nil {
		t.Fatal(err)
	}
	deleted, err := s.DeleteCancelledTask("alice", cancelled.ID)
	if err != nil || deleted.ID != cancelled.ID {
		t.Fatalf("DeleteCancelledTask(cancelled) = %+v, %v", deleted, err)
	}
	if _, err := s.UpdateTask("alice", cancelled.ID, TaskPatch{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("purged task lookup = %v, want ErrNotFound", err)
	}
	if _, found, err := s.Tombstone("alice", cancelled.ID); err != nil || found {
		t.Fatalf("purged tombstone = found %v, err %v", found, err)
	}
}

func TestCancelTaskMovesAndTombstonesAtomically(t *testing.T) {
	s := newStore(t)
	task, err := s.AddTask("alice", board.Task{Title: "Reject me", Status: board.StatusTodo})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_atomic_tombstone BEFORE INSERT ON tombstones BEGIN SELECT RAISE(ABORT, 'no tombstone'); END`); err != nil {
		t.Fatal(err)
	}
	reason := "superseded"
	if _, err := s.CancelTask("alice", task.ID, &reason); err == nil {
		t.Fatal("CancelTask succeeded while tombstone insert failed")
	}
	current, err := s.Task("alice", task.ID)
	if err != nil || current.Status != board.StatusTodo {
		t.Fatalf("failed atomic cancel persisted status = %s, %v", current.Status, err)
	}
	if _, found, err := s.Tombstone("alice", task.ID); err != nil || found {
		t.Fatalf("failed atomic cancel persisted tombstone = %v, %v", found, err)
	}
	if _, err := s.db.Exec(`DROP TRIGGER fail_atomic_tombstone`); err != nil {
		t.Fatal(err)
	}

	cancelled, err := s.CancelTask("alice", task.ID, &reason)
	if err != nil || cancelled.Status != board.StatusCancelled {
		t.Fatalf("CancelTask = %+v, %v", cancelled, err)
	}
	tombstone, found, err := s.Tombstone("alice", task.ID)
	if err != nil || !found || tombstone.Reason != reason {
		t.Fatalf("atomic tombstone = %+v, %v, %v", tombstone, found, err)
	}
}

func TestUpdateAndMoveTaskIfFieldsMatchIsAtomic(t *testing.T) {
	s := newStore(t)
	original := []board.Check{{Text: "only"}}
	task, err := s.AddTask("alice", board.Task{Title: "CAS move", Status: board.StatusTodo, Checks: original})
	if err != nil {
		t.Fatal(err)
	}
	doneChecks := []board.Check{{Text: "only", Done: true}}
	target := board.StatusDone
	index := 0

	stale := []board.Check{{Text: "stale"}}
	if _, err := s.UpdateAndMoveTaskIfFieldsMatch("alice", task.ID,
		TaskPatch{Checks: &stale}, TaskPatch{Checks: &doneChecks}, &target, &index, nil); err == nil {
		t.Fatal("stale field match succeeded")
	} else {
		var conflict *TaskFieldsConflictError
		if !errors.As(err, &conflict) {
			t.Fatalf("stale field match = %T %v", err, err)
		}
	}

	guardErr := errors.New("guard refused")
	if _, err := s.UpdateAndMoveTaskIfFieldsMatch("alice", task.ID,
		TaskPatch{Checks: &original}, TaskPatch{Checks: &doneChecks}, &target, &index,
		func(board.Task) error { return guardErr }); !errors.Is(err, guardErr) {
		t.Fatalf("guarded CAS move = %v", err)
	}
	unchanged, err := s.Task("alice", task.ID)
	if err != nil || unchanged.Status != board.StatusTodo || unchanged.Checks[0].Done {
		t.Fatalf("refused CAS move persisted = %+v, %v", unchanged, err)
	}

	moved, err := s.UpdateAndMoveTaskIfFieldsMatch("alice", task.ID,
		TaskPatch{Checks: &original}, TaskPatch{Checks: &doneChecks}, &target, &index, nil)
	if err != nil || moved.Status != board.StatusDone || !moved.Checks[0].Done {
		t.Fatalf("CAS move = %+v, %v", moved, err)
	}

	invalidStatus := board.Status("invalid")
	if _, err := s.UpdateAndMoveTaskIfFieldsMatch("alice", task.ID,
		TaskPatch{}, TaskPatch{}, &invalidStatus, nil, nil); err == nil {
		t.Fatal("invalid CAS move status succeeded")
	}
	negative := -1
	if _, err := s.UpdateAndMoveTaskIfFieldsMatch("alice", task.ID,
		TaskPatch{}, TaskPatch{}, nil, &negative, nil); err == nil {
		t.Fatal("negative CAS move index succeeded")
	}
	if _, err := s.UpdateAndMoveTaskIfFieldsMatch("alice", "missing",
		TaskPatch{}, TaskPatch{}, nil, nil, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing CAS move = %v", err)
	}
}

func TestRestorePreservesContextAndPurgeRemovesIt(t *testing.T) {
	s := newStore(t)
	target, err := s.AddTask("alice", board.Task{Title: "Target", Status: board.StatusTodo})
	if err != nil {
		t.Fatal(err)
	}
	blocker, err := s.AddTask("alice", board.Task{Title: "Blocker", Status: board.StatusTodo})
	if err != nil {
		t.Fatal(err)
	}
	blocked, err := s.AddTask("alice", board.Task{Title: "Blocked", Status: board.StatusTodo})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Link("alice", target.ID, blocked.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Link("alice", blocker.ID, target.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddComment("alice", target.ID, "alice", "keep this context"); err != nil {
		t.Fatal(err)
	}
	reason := "not now"
	if _, err := s.CancelTask("alice", target.ID, &reason); err != nil {
		t.Fatal(err)
	}
	restored, err := s.MoveTask("alice", target.ID, board.StatusTodo)
	if err != nil || restored.Status != board.StatusTodo {
		t.Fatalf("restore = %+v, %v", restored, err)
	}
	comments, err := s.Comments("alice", target.ID)
	if err != nil || len(comments) != 1 {
		t.Fatalf("restore comments = %d, %v", len(comments), err)
	}
	links, err := s.TaskLinks("alice", target.ID)
	if err != nil || len(links.Blocks) != 1 || len(links.BlockedBy) != 1 {
		t.Fatalf("restore links = %+v, %v", links, err)
	}
	if _, found, err := s.Tombstone("alice", target.ID); err != nil || found {
		t.Fatalf("restore retained tombstone = %v, %v", found, err)
	}

	if _, err := s.CancelTask("alice", target.ID, &reason); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteCancelledTask("alice", target.ID); err != nil {
		t.Fatal(err)
	}
	for table, query := range map[string]string{
		"comments":   `SELECT COUNT(*) FROM comments WHERE scope='alice' AND task_id=?`,
		"links":      `SELECT COUNT(*) FROM task_links WHERE scope='alice' AND (blocker_id=? OR blocked_id=?)`,
		"tombstones": `SELECT COUNT(*) FROM tombstones WHERE scope='alice' AND task_id=?`,
	} {
		var count int
		var queryErr error
		if table == "links" {
			queryErr = s.db.QueryRow(query, target.ID, target.ID).Scan(&count)
		} else {
			queryErr = s.db.QueryRow(query, target.ID).Scan(&count)
		}
		if queryErr != nil || count != 0 {
			t.Errorf("purge %s rows = %d, %v", table, count, queryErr)
		}
	}
}
