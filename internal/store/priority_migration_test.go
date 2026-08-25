package store

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/board"
)

// seedPriorityDatabaseAtV9 builds a database at schema v9 - the last version
// before the priority scale collapsed - and writes one task per seeded
// priority straight through SQL, bypassing the Go write paths so values the
// three-value scale does not name actually reach the table.
func seedPriorityDatabaseAtV9(t *testing.T, path string, prios []int) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS meta (k TEXT PRIMARY KEY, v TEXT NOT NULL)`); err != nil {
		t.Fatalf("create meta table: %v", err)
	}
	for version, migration := range migrations[:9] {
		if _, err := db.Exec(migration); err != nil {
			t.Fatalf("apply migration v%d: %v", version+1, err)
		}
		if _, err := db.Exec(
			`INSERT INTO meta (k, v) VALUES ('schema_version', ?)
			 ON CONFLICT(k) DO UPDATE SET v = excluded.v`,
			version+1,
		); err != nil {
			t.Fatalf("record migration v%d: %v", version+1, err)
		}
	}
	const stamp = "2026-08-25T12:00:00Z"
	for i, prio := range prios {
		if _, err := db.Exec(
			`INSERT INTO tasks (id, user, seq, title, status, prio, position, created_at, moved_at)
			 VALUES (?, 'alice', ?, ?, 'todo', ?, ?, ?, ?)`,
			legacyTaskID(i), i+1, legacyTaskID(i), prio, i, stamp, stamp,
		); err != nil {
			t.Fatalf("seed task with prio %d: %v", prio, err)
		}
	}
}

func legacyTaskID(i int) string {
	return string(rune('a'+i)) + "-legacy-task"
}

// storedPriorities reads every seeded task's priority back by title, which the
// seed set to the task id so a row is identifiable without depending on order.
func storedPriorities(t *testing.T, s *Store, count int) []int {
	t.Helper()
	got := make([]int, count)
	for i := range got {
		var prio int
		if err := s.db.QueryRow(`SELECT prio FROM tasks WHERE id = ?`, legacyTaskID(i)).Scan(&prio); err != nil {
			t.Fatalf("read prio for %s: %v", legacyTaskID(i), err)
		}
		got[i] = prio
	}
	return got
}

// TestMigrateV10CollapsesPriorityScale proves the mapping issue #234 specifies:
// P1 stays high, P2 stays medium, P3 stays low, and P4 becomes low. Values the
// old scale never named fold onto low too, because prio is a plain INTEGER
// column with no CHECK constraint and a legacy writer could have put anything
// there.
func TestMigrateV10CollapsesPriorityScale(t *testing.T) {
	seeded := []int{1, 2, 3, 4, 0, 9, -1}
	want := []int{
		board.PrioHigh,
		board.PrioMedium,
		board.PrioLow,
		board.PrioLow, // P4 always meant lowest; the new scale has one bottom.
		board.PrioLow,
		board.PrioLow,
		board.PrioLow,
	}

	path := filepath.Join(t.TempDir(), "kb.db")
	seedPriorityDatabaseAtV9(t, path, seeded)

	s, err := Open(path, []byte("test-secret"))
	if err != nil {
		t.Fatalf("Open on a v9 database: %v", err)
	}
	defer s.Close()

	var schemaVersion string
	if err := s.db.QueryRow(`SELECT v FROM meta WHERE k = 'schema_version'`).Scan(&schemaVersion); err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if schemaVersion != "10" {
		t.Fatalf("schema version = %q, want 10", schemaVersion)
	}

	got := storedPriorities(t, s, len(seeded))
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("seeded prio %d migrated to %d, want %d", seeded[i], got[i], want[i])
		}
	}
}

// TestMigrateV10IsIdempotent reopens a migrated database and re-runs the v10
// statement directly. The ledger in meta already stops a second application,
// so this asserts the stronger property the statement itself carries: its
// predicate is false for every row it has already rewritten, which is what
// makes a replayed or hand-run migration safe.
func TestMigrateV10IsIdempotent(t *testing.T) {
	seeded := []int{1, 2, 3, 4, 9}
	path := filepath.Join(t.TempDir(), "kb.db")
	seedPriorityDatabaseAtV9(t, path, seeded)

	s, err := Open(path, []byte("test-secret"))
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	first := storedPriorities(t, s, len(seeded))
	if err := s.Close(); err != nil {
		t.Fatalf("close after first Open: %v", err)
	}

	// A second Open runs migrate again; the version ledger should make it a
	// no-op rather than a second rewrite.
	s2, err := Open(path, []byte("test-secret"))
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer s2.Close()
	second := storedPriorities(t, s2, len(seeded))
	for i := range first {
		if first[i] != second[i] {
			t.Errorf("reopen changed prio for seed %d: %d then %d", seeded[i], first[i], second[i])
		}
	}

	// Re-run the v10 statement itself. Every row is already on the scale, so
	// it must report zero rows affected.
	result, err := s2.db.Exec(migrations[9])
	if err != nil {
		t.Fatalf("re-run v10: %v", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		t.Fatalf("rows affected: %v", err)
	}
	if affected != 0 {
		t.Errorf("re-running v10 rewrote %d rows, want 0", affected)
	}
	third := storedPriorities(t, s2, len(seeded))
	for i := range second {
		if second[i] != third[i] {
			t.Errorf("re-running v10 changed prio for seed %d: %d then %d", seeded[i], second[i], third[i])
		}
	}
}

// TestMigrateV10PreservesEverythingElse proves the collapse touches priority
// and nothing else on a task it rewrites.
func TestMigrateV10PreservesEverythingElse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kb.db")
	seedPriorityDatabaseAtV9(t, path, []int{4})

	s, err := Open(path, []byte("test-secret"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	var title, status, created string
	var seq int
	if err := s.db.QueryRow(
		`SELECT title, status, seq, created_at FROM tasks WHERE id = ?`, legacyTaskID(0),
	).Scan(&title, &status, &seq, &created); err != nil {
		t.Fatalf("read migrated task: %v", err)
	}
	if title != legacyTaskID(0) {
		t.Errorf("title = %q, want %q", title, legacyTaskID(0))
	}
	if status != "todo" {
		t.Errorf("status = %q, want todo", status)
	}
	if seq != 1 {
		t.Errorf("seq = %d, want 1", seq)
	}
	if created != "2026-08-25T12:00:00Z" {
		t.Errorf("created_at = %q, want the seeded stamp", created)
	}
}

// TestWritePathsRejectPrioOffTheScale is the other half of the guarantee the
// migration makes: once the stored data is on the scale, nothing puts it back
// off. An explicit patch naming a retired value is refused rather than
// silently folded, because the caller asked for something specific.
func TestWritePathsRejectPrioOffTheScale(t *testing.T) {
	s := newStore(t)
	created, err := s.AddTask("alice", board.Task{Title: "card", Prio: board.PrioHigh})
	if err != nil {
		t.Fatalf("AddTask: %v", err)
	}
	for _, prio := range []int{0, 4, 9, -1} {
		p := prio
		if _, err := s.UpdateTask("alice", created.ID, TaskPatch{Prio: &p}); err == nil {
			t.Errorf("UpdateTask accepted prio %d, want a refusal", prio)
		}
	}
}

// TestTolerantWritePathsNormalizePrio covers the paths that take a whole task
// rather than a named field. A zero-valued Prio means "unset" on a struct
// literal, so these normalize onto low instead of refusing.
func TestTolerantWritePathsNormalizePrio(t *testing.T) {
	s := newStore(t)
	for _, prio := range []int{0, 4, 9, -1} {
		created, err := s.AddTask("alice", board.Task{Title: "card", Prio: prio})
		if err != nil {
			t.Fatalf("AddTask with prio %d: %v", prio, err)
		}
		if created.Prio != board.PrioLow {
			t.Errorf("AddTask stored prio %d as %d, want %d", prio, created.Prio, board.PrioLow)
		}
	}
	if err := s.ReplaceBoard("bob", board.Board{
		Title: "Board",
		Tasks: []board.Task{{Title: "legacy", Status: board.StatusTodo, Prio: 4}},
	}); err != nil {
		t.Fatalf("ReplaceBoard: %v", err)
	}
	replaced, err := s.Board("bob")
	if err != nil {
		t.Fatalf("Board: %v", err)
	}
	if got := replaced.Tasks[0].Prio; got != board.PrioLow {
		t.Errorf("ReplaceBoard stored prio 4 as %d, want %d", got, board.PrioLow)
	}
}
