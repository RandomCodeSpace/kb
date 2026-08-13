package store

import (
	"errors"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/board"
)

func TestCompletionBlockedErrorMessage(t *testing.T) {
	err := NewCompletionBlockedError("2 open blockers (#1, #3)", "#2", "Beta")
	want := `2 open blockers (#1, #3) on #2 "Beta"; re-run with --force to finish it anyway`
	if err.Error() != want {
		t.Fatalf("Error() = %q, want %q", err.Error(), want)
	}
}

func TestCompletionWarningCombinations(t *testing.T) {
	cases := []struct {
		name string
		task board.Task
		want string
	}{
		{"clean", board.Task{}, ""},
		{"done checks only", board.Task{Checks: []board.Check{{Text: "a", Done: true}}}, ""},
		{"open checks", board.Task{Checks: []board.Check{{Text: "a"}, {Text: "b", Done: true}}},
			"1 of 2 checklist items are still open"},
		{"blocked flag", board.Task{Blocked: true}, "the task is flagged blocked"},
		{"both", board.Task{Blocked: true, Checks: []board.Check{{Text: "a"}}},
			"1 of 1 checklist items are still open and the task is flagged blocked"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := CompletionWarning(tt.task); got != tt.want {
				t.Fatalf("CompletionWarning = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSortTasksBySeqOrdersInPlace(t *testing.T) {
	tasks := []board.Task{{Seq: 3}, {Seq: 1}, {Seq: 2}}
	sortTasksBySeq(tasks)
	if tasks[0].Seq != 1 || tasks[1].Seq != 2 || tasks[2].Seq != 3 {
		t.Fatalf("sorted = %v", tasks)
	}
}

func TestDisplayTaskRefFallsBackToUUID(t *testing.T) {
	if got := displayTaskRef(board.Task{ID: "abc", Seq: 4}); got != "#4" {
		t.Fatalf("with seq = %q", got)
	}
	if got := displayTaskRef(board.Task{ID: "abc"}); got != "abc" {
		t.Fatalf("without seq = %q", got)
	}
}

func TestDescribeBlockersPluralizes(t *testing.T) {
	one := describeBlockers([]board.Task{{Seq: 1}})
	if one != "1 open blocker (#1)" {
		t.Fatalf("singular = %q", one)
	}
	two := describeBlockers([]board.Task{{Seq: 1}, {Seq: 2}})
	if two != "2 open blockers (#1, #2)" {
		t.Fatalf("plural = %q", two)
	}
}

func TestLinkAndUnlinkRejectBadReferences(t *testing.T) {
	s := newStore(t)
	if _, err := s.AddTask("u", board.Task{Title: "Only"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Link("u", "9", "1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bad blocker ref = %v", err)
	}
	if _, _, err := s.Link("u", "1", "9"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bad blocked ref = %v", err)
	}
	if err := s.Unlink("u", "9", "1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unlink bad first ref = %v", err)
	}
	if err := s.Unlink("u", "1", "9"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unlink bad second ref = %v", err)
	}
}

func TestLinkStoreReportsDatabaseFailures(t *testing.T) {
	seed := func(t *testing.T) *Store {
		s := newStore(t)
		if _, err := s.AddTask("u", board.Task{Title: "A"}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.AddTask("u", board.Task{Title: "B"}); err != nil {
			t.Fatal(err)
		}
		return s
	}

	t.Run("link insert", func(t *testing.T) {
		s := seed(t)
		mustExecCoverage(t, s, `DROP TABLE task_links`)
		if _, _, err := s.Link("u", "1", "2"); err == nil {
			t.Fatal("Link survived a missing task_links table")
		}
	})

	t.Run("unlink delete", func(t *testing.T) {
		s := seed(t)
		mustExecCoverage(t, s, `DROP TABLE task_links`)
		if err := s.Unlink("u", "1", "2"); err == nil {
			t.Fatal("Unlink survived a missing task_links table")
		}
	})

	t.Run("task links query", func(t *testing.T) {
		s := seed(t)
		task, err := s.Task("u", "1")
		if err != nil {
			t.Fatal(err)
		}
		mustExecCoverage(t, s, `DROP TABLE task_links`)
		if _, err := s.TaskLinks("u", task.ID); err == nil {
			t.Fatal("TaskLinks survived a missing task_links table")
		}
	})

	t.Run("dangling endpoint", func(t *testing.T) {
		s := seed(t)
		task, err := s.Task("u", "1")
		if err != nil {
			t.Fatal(err)
		}
		mustExecCoverage(t, s, `INSERT INTO task_links(scope, blocker_id, blocked_id) VALUES ('u', 'ghost', '`+task.ID+`')`)
		if _, err := s.TaskLinks("u", task.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("dangling link endpoint = %v", err)
		}
	})

	t.Run("reconcile on replace", func(t *testing.T) {
		s := seed(t)
		mustExecCoverage(t, s, `DROP TABLE task_links`)
		if err := s.ReplaceBoard("u", board.Board{Title: "b"}); err == nil {
			t.Fatal("ReplaceBoard survived a missing task_links table")
		}
	})

	t.Run("delete task sweeps links", func(t *testing.T) {
		s := seed(t)
		mustExecCoverage(t, s, `DROP TABLE task_links`)
		if _, err := s.DeleteTask("u", "1"); err == nil {
			t.Fatal("DeleteTask survived a missing task_links table")
		}
	})
}

func TestCommentStoreReportsDatabaseFailures(t *testing.T) {
	seed := func(t *testing.T) *Store {
		s := newStore(t)
		if _, err := s.AddTask("u", board.Task{Title: "A"}); err != nil {
			t.Fatal(err)
		}
		return s
	}

	t.Run("advance sequence", func(t *testing.T) {
		s := seed(t)
		mustExecCoverage(t, s, `DROP TABLE comment_sequences`)
		if _, err := s.AddComment("u", "1", "u", "note"); err == nil {
			t.Fatal("AddComment survived a missing comment_sequences table")
		}
	})

	t.Run("list query", func(t *testing.T) {
		s := seed(t)
		mustExecCoverage(t, s, `DROP TABLE comments`)
		if _, err := s.Comments("u", "1"); err == nil {
			t.Fatal("Comments survived a missing comments table")
		}
	})

	t.Run("unparseable created_at", func(t *testing.T) {
		s := seed(t)
		task, err := s.Task("u", "1")
		if err != nil {
			t.Fatal(err)
		}
		mustExecCoverage(t, s, `INSERT INTO comments(scope, id, task_id, author, body, created_at) VALUES ('u', 7, '`+task.ID+`', 'u', 'note', 'garbage')`)
		if _, err := s.Comments("u", "1"); err == nil || !strings.Contains(err.Error(), "created_at") {
			t.Fatalf("Comments accepted a corrupt timestamp: %v", err)
		}
		if _, err := s.DeleteComment("u", 7); err == nil || !strings.Contains(err.Error(), "created_at") {
			t.Fatalf("DeleteComment accepted a corrupt timestamp: %v", err)
		}
	})

	t.Run("orphaned comment still deletes", func(t *testing.T) {
		s := seed(t)
		mustExecCoverage(t, s, `INSERT INTO comments(scope, id, task_id, author, body, created_at) VALUES ('u', 9, 'ghost', 'u', 'orphan', '2026-01-01T00:00:00Z')`)
		c, err := s.DeleteComment("u", 9)
		if err != nil || c.TaskSeq != 0 || c.Body != "orphan" {
			t.Fatalf("DeleteComment(orphan) = %+v, %v", c, err)
		}
	})

	t.Run("reconcile on replace", func(t *testing.T) {
		s := seed(t)
		mustExecCoverage(t, s, `DROP TABLE comments`)
		if err := s.ReplaceBoard("u", board.Board{Title: "b"}); err == nil {
			t.Fatal("ReplaceBoard survived a missing comments table")
		}
	})

	t.Run("delete task sweeps comments", func(t *testing.T) {
		s := seed(t)
		mustExecCoverage(t, s, `DROP TABLE comments`)
		if _, err := s.DeleteTask("u", "1"); err == nil {
			t.Fatal("DeleteTask survived a missing comments table")
		}
	})
}

func TestSequenceAllocationReportsDatabaseFailures(t *testing.T) {
	t.Run("task sequence", func(t *testing.T) {
		s := newStore(t)
		mustExecCoverage(t, s, `DROP TABLE board_sequences`)
		if _, err := s.AddTask("u", board.Task{Title: "A"}); err == nil {
			t.Fatal("AddTask survived a missing board_sequences table")
		}
	})

	t.Run("task insert", func(t *testing.T) {
		s := newStore(t)
		mustExecCoverage(t, s, `DROP TABLE tasks`)
		if _, err := s.AddTask("u", board.Task{Title: "A"}); err == nil {
			t.Fatal("AddTask survived a missing tasks table")
		}
	})
}

func TestUsersReportsDatabaseFailures(t *testing.T) {
	t.Run("query", func(t *testing.T) {
		s := newStore(t)
		mustExecCoverage(t, s, `DROP TABLE meta`)
		if _, err := s.Users(); err == nil {
			t.Fatal("Users survived a missing meta table")
		}
	})

	t.Run("unscannable row", func(t *testing.T) {
		s := newStore(t)
		mustExecCoverage(t, s, `DROP TABLE tasks`)
		mustExecCoverage(t, s, `CREATE TABLE tasks (user TEXT, id TEXT)`)
		mustExecCoverage(t, s, `INSERT INTO tasks(user, id) VALUES (NULL, 'x')`)
		if _, err := s.Users(); err == nil {
			t.Fatal("Users accepted an unscannable row")
		}
	})
}

func TestTaskLookupRejectsUnknownReference(t *testing.T) {
	s := newStore(t)
	if _, err := s.Task("u", "9"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Task(unknown) = %v", err)
	}
}

func TestCommentQueriesRejectUnscannableRows(t *testing.T) {
	s := newStore(t)
	task, err := s.AddTask("u", board.Task{Title: "A"})
	if err != nil {
		t.Fatal(err)
	}
	mustExecCoverage(t, s, `DROP TABLE comments`)
	mustExecCoverage(t, s, `CREATE TABLE comments (scope TEXT, id INTEGER, task_id TEXT, author TEXT, body TEXT, created_at TEXT)`)
	mustExecCoverage(t, s, `INSERT INTO comments(scope, id, task_id, author, body, created_at) VALUES ('u', 3, '`+task.ID+`', NULL, 'x', '2026-01-01T00:00:00Z')`)
	if _, err := s.Comments("u", "1"); err == nil {
		t.Fatal("Comments accepted an unscannable row")
	}
	if _, err := s.DeleteComment("u", 3); err == nil {
		t.Fatal("DeleteComment accepted an unscannable row")
	}
}

func TestLinkQueriesRejectUnscannableRows(t *testing.T) {
	s := newStore(t)
	for _, title := range []string{"A", "B"} {
		if _, err := s.AddTask("u", board.Task{Title: title}); err != nil {
			t.Fatal(err)
		}
	}
	mustExecCoverage(t, s, `DROP TABLE task_links`)
	mustExecCoverage(t, s, `CREATE TABLE task_links (scope TEXT, blocker_id TEXT, blocked_id TEXT)`)
	mustExecCoverage(t, s, `INSERT INTO task_links(scope, blocker_id, blocked_id) VALUES ('u', NULL, NULL)`)
	// reaches loads every edge for the cycle check and must refuse the NULL row.
	if _, _, err := s.Link("u", "1", "2"); err == nil {
		t.Fatal("Link accepted an unscannable edge row")
	}
	task, err := s.Task("u", "1")
	if err != nil {
		t.Fatal(err)
	}
	mustExecCoverage(t, s, `UPDATE task_links SET blocked_id = '`+task.ID+`'`)
	if _, err := s.TaskLinks("u", task.ID); err == nil {
		t.Fatal("TaskLinks accepted an unscannable edge row")
	}
}

func TestDoneGateReportsBrokenLinkState(t *testing.T) {
	s := newStore(t)
	task, err := s.AddTask("u", board.Task{Title: "A"})
	if err != nil {
		t.Fatal(err)
	}
	mustExecCoverage(t, s, `INSERT INTO task_links(scope, blocker_id, blocked_id) VALUES ('u', 'ghost', '`+task.ID+`')`)
	done := board.StatusDone
	guard := func(board.Task) error { return nil }
	if _, err := s.UpdateAndMoveTask("u", "1", TaskPatch{}, &done, nil, guard); err == nil {
		t.Fatal("guarded done survived a dangling blocker edge")
	}
}
