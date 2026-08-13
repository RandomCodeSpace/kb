package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/kb/internal/board"
)

// TestMigrateV4FromV3 proves the decision-graveyard schema can be added to an
// existing phase-7 database without changing its tasks or import provenance.
func TestMigrateV4FromV3(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kb.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS meta (k TEXT PRIMARY KEY, v TEXT NOT NULL)`); err != nil {
		t.Fatalf("create meta table: %v", err)
	}
	for version, migration := range migrations[:3] {
		tx, err := db.Begin()
		if err != nil {
			t.Fatalf("begin migration v%d: %v", version+1, err)
		}
		if _, err := tx.Exec(migration); err != nil {
			_ = tx.Rollback()
			t.Fatalf("apply migration v%d: %v", version+1, err)
		}
		if _, err := tx.Exec(
			`INSERT INTO meta (k, v) VALUES ('schema_version', ?)
			 ON CONFLICT(k) DO UPDATE SET v = excluded.v`,
			version+1,
		); err != nil {
			_ = tx.Rollback()
			t.Fatalf("record migration v%d: %v", version+1, err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit migration v%d: %v", version+1, err)
		}
	}

	const (
		stamp       = "2026-07-29T12:00:00Z"
		taskTitle   = "Preserve the existing task"
		importTitle = "Preserve the existing import"
	)
	if _, err := db.Exec(
		`INSERT INTO tasks (id, user, title, status, created_at, moved_at)
		 VALUES ('existing-task', 'alice', ?, 'todo', ?, ?)`,
		taskTitle, stamp, stamp,
	); err != nil {
		t.Fatalf("seed v3 task: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO import_links
		 (scope, source, kind, external_key, link, url, title, imported_at)
		 VALUES ('alice', 'github.com', 'github', 'github:github.com/acme/app#7',
		         'github#7', 'https://github.com/acme/app/issues/7', ?, ?)`,
		importTitle, stamp,
	); err != nil {
		t.Fatalf("seed v3 import link: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close v3 database: %v", err)
	}

	s, err := Open(path, []byte("test-secret"))
	if err != nil {
		t.Fatalf("Open on a v3 database: %v", err)
	}
	defer s.Close()

	var schemaVersion string
	if err := s.db.QueryRow(`SELECT v FROM meta WHERE k = 'schema_version'`).Scan(&schemaVersion); err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if schemaVersion != "8" {
		t.Fatalf("schema version = %q, want 8", schemaVersion)
	}

	var tombstonesTable string
	if err := s.db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'tombstones'`,
	).Scan(&tombstonesTable); err != nil {
		t.Fatalf("look up tombstones table: %v", err)
	}
	if tombstonesTable != "tombstones" {
		t.Errorf("tombstones table = %q, want tombstones", tombstonesTable)
	}

	baselineColumns := map[string]bool{
		"baseline_title":   false,
		"baseline_hash":    false,
		"baseline_excerpt": false,
		"baseline_at":      false,
	}
	rows, err := s.db.Query(`PRAGMA table_info(import_links)`)
	if err != nil {
		t.Fatalf("inspect import_links columns: %v", err)
	}
	for rows.Next() {
		var (
			position, notNull, primaryKey int
			name, columnType              string
			defaultValue                  sql.NullString
		)
		if err := rows.Scan(&position, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			t.Fatalf("scan import_links column: %v", err)
		}
		if _, ok := baselineColumns[name]; ok {
			baselineColumns[name] = columnType == "TEXT" &&
				notNull == 1 &&
				defaultValue.Valid &&
				defaultValue.String == "''"
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		t.Fatalf("iterate import_links columns: %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close import_links columns: %v", err)
	}
	for name, valid := range baselineColumns {
		if !valid {
			t.Errorf("import_links column %s is missing or has the wrong definition", name)
		}
	}

	var gotTaskTitle, gotTaskStatus, gotCreatedAt, gotMovedAt string
	if err := s.db.QueryRow(
		`SELECT title, status, created_at, moved_at
		 FROM tasks WHERE id = 'existing-task' AND user = 'alice'`,
	).Scan(&gotTaskTitle, &gotTaskStatus, &gotCreatedAt, &gotMovedAt); err != nil {
		t.Fatalf("read migrated task: %v", err)
	}
	if gotTaskTitle != taskTitle || gotTaskStatus != "todo" || gotCreatedAt != stamp || gotMovedAt != stamp {
		t.Errorf(
			"migrated task = (%q, %q, %q, %q), want (%q, todo, %q, %q)",
			gotTaskTitle, gotTaskStatus, gotCreatedAt, gotMovedAt, taskTitle, stamp, stamp,
		)
	}

	var (
		gotSource, gotKind, gotExternalKey, gotLink string
		gotURL, gotImportTitle, gotImportedAt       string
		baselineTitle, baselineHash                 string
		baselineExcerpt, baselineAt                 string
	)
	if err := s.db.QueryRow(
		`SELECT source, kind, external_key, link, url, title, imported_at,
		        baseline_title, baseline_hash, baseline_excerpt, baseline_at
		 FROM import_links
		 WHERE scope = 'alice' AND external_key = 'github:github.com/acme/app#7'`,
	).Scan(
		&gotSource, &gotKind, &gotExternalKey, &gotLink, &gotURL, &gotImportTitle, &gotImportedAt,
		&baselineTitle, &baselineHash, &baselineExcerpt, &baselineAt,
	); err != nil {
		t.Fatalf("read migrated import link: %v", err)
	}
	if gotSource != "github.com" ||
		gotKind != "github" ||
		gotExternalKey != "github:github.com/acme/app#7" ||
		gotLink != "github#7" ||
		gotURL != "https://github.com/acme/app/issues/7" ||
		gotImportTitle != importTitle ||
		gotImportedAt != stamp {
		t.Errorf(
			"migrated import link = (%q, %q, %q, %q, %q, %q, %q)",
			gotSource, gotKind, gotExternalKey, gotLink, gotURL, gotImportTitle, gotImportedAt,
		)
	}
	if baselineTitle != "" || baselineHash != "" || baselineExcerpt != "" || baselineAt != "" {
		t.Errorf(
			"migrated baseline = (%q, %q, %q, %q), want all empty",
			baselineTitle, baselineHash, baselineExcerpt, baselineAt,
		)
	}

	var schemaCookieBefore int
	if err := s.db.QueryRow(`PRAGMA schema_version`).Scan(&schemaCookieBefore); err != nil {
		t.Fatalf("read schema cookie before no-op migration: %v", err)
	}
	if err := migrate(s.db); err != nil {
		t.Fatalf("run migrate again: %v", err)
	}
	var schemaCookieAfter int
	if err := s.db.QueryRow(`PRAGMA schema_version`).Scan(&schemaCookieAfter); err != nil {
		t.Fatalf("read schema cookie after no-op migration: %v", err)
	}
	if schemaCookieAfter != schemaCookieBefore {
		t.Errorf("no-op migrate changed schema cookie from %d to %d", schemaCookieBefore, schemaCookieAfter)
	}
	if err := s.db.QueryRow(`SELECT v FROM meta WHERE k = 'schema_version'`).Scan(&schemaVersion); err != nil {
		t.Fatalf("read schema version after no-op migration: %v", err)
	}
	if schemaVersion != "8" {
		t.Errorf("schema version after no-op migrate = %q, want 8", schemaVersion)
	}
}

// TestRecordTombstoneIsAnUpsert prevents a repeated kill from leaving stale
// reasoning or the timestamp from the first decision.
func TestRecordTombstoneIsAnUpsert(t *testing.T) {
	s := newStore(t)
	task := addSearchTask(t, s, board.Task{Title: "Retire the legacy login", Status: board.StatusCancelled})

	if err := s.RecordTombstone("alice", task.ID, "The first reason"); err != nil {
		t.Fatalf("RecordTombstone first: %v", err)
	}
	first, found, err := s.Tombstone("alice", task.ID)
	if err != nil || !found {
		t.Fatalf("Tombstone first = %+v, %t, %v", first, found, err)
	}
	if first.Reason != "The first reason" {
		t.Fatalf("first reason = %q, want %q", first.Reason, "The first reason")
	}
	if _, err := time.Parse(time.RFC3339Nano, first.KilledAt); err != nil {
		t.Fatalf("first killed_at = %q: %v", first.KilledAt, err)
	}

	const staleKilledAt = "2000-01-01T00:00:00Z"
	if _, err := s.db.Exec(
		`UPDATE tombstones SET killed_at = ? WHERE scope = ? AND task_id = ?`,
		staleKilledAt, "alice", task.ID,
	); err != nil {
		t.Fatalf("age first tombstone: %v", err)
	}
	if err := s.RecordTombstone("alice", task.ID, "The replacement reason"); err != nil {
		t.Fatalf("RecordTombstone second: %v", err)
	}
	second, found, err := s.Tombstone("alice", task.ID)
	if err != nil || !found {
		t.Fatalf("Tombstone second = %+v, %t, %v", second, found, err)
	}
	if second.TaskID != task.ID || second.Reason != "The replacement reason" {
		t.Errorf("second tombstone = %+v, want task %q with replacement reason", second, task.ID)
	}
	if second.KilledAt == staleKilledAt {
		t.Errorf("second killed_at = stale timestamp %q", second.KilledAt)
	}
	if _, err := time.Parse(time.RFC3339Nano, second.KilledAt); err != nil {
		t.Fatalf("second killed_at = %q: %v", second.KilledAt, err)
	}
	var rows int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM tombstones WHERE scope = ? AND task_id = ?`, "alice", task.ID).Scan(&rows); err != nil {
		t.Fatalf("count replayed tombstones: %v", err)
	}
	if rows != 1 {
		t.Fatalf("replayed tombstone rows = %d, want 1", rows)
	}
}

// TestTombstoneValidationRejectsRatherThanTruncates keeps rejection reasons
// exact so a stored explanation can never silently differ from user input.
func TestTombstoneValidationRejectsRatherThanTruncates(t *testing.T) {
	s := newStore(t)
	tests := []struct {
		name   string
		reason string
	}{
		{name: "empty", reason: ""},
		{name: "over 2000 bytes", reason: strings.Repeat("x", 2001)},
		{name: "carriage return", reason: "first\rsecond"},
		{name: "line feed", reason: "first\nsecond"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			taskID := "invalid-" + tt.name
			if err := s.RecordTombstone("alice", taskID, tt.reason); err == nil {
				t.Fatalf("RecordTombstone(%q) succeeded, want validation error", tt.reason)
			}
			if got, found, err := s.Tombstone("alice", taskID); err != nil || found {
				t.Fatalf("Tombstone after rejected write = %+v, %t, %v", got, found, err)
			}
		})
	}
}

// TestSearchSimilarReportsKilledCardsWithTheirReason distinguishes a recorded
// rejection from a live match without making the reason independently searchable.
func TestSearchSimilarReportsKilledCardsWithTheirReason(t *testing.T) {
	s := newStore(t)
	killed := addSearchTask(t, s, board.Task{
		Title:  "Shared authentication marker rejected",
		Status: board.StatusCancelled,
	})
	live := addSearchTask(t, s, board.Task{
		Title:  "Shared authentication marker active",
		Status: board.StatusTodo,
	})
	const reason = "Superseded by the SAML work"
	if err := s.RecordTombstone("alice", killed.ID, reason); err != nil {
		t.Fatalf("RecordTombstone: %v", err)
	}

	hits, err := s.SearchSimilar("alice", "shared authentication marker", "", nil, 10)
	if err != nil {
		t.Fatalf("SearchSimilar: %v", err)
	}
	var killedHit, liveHit *SimilarHit
	for i := range hits {
		switch hits[i].ID {
		case killed.ID:
			killedHit = &hits[i]
		case live.ID:
			liveHit = &hits[i]
		}
	}
	if killedHit == nil || liveHit == nil {
		t.Fatalf("SearchSimilar = %+v, want killed %q and live %q", hits, killed.ID, live.ID)
	}
	if killedHit.Via != "killed" ||
		killedHit.Status != string(board.StatusCancelled) ||
		killedHit.Reason != reason ||
		killedHit.KilledAt == "" {
		t.Errorf("killed hit = %+v", *killedHit)
	}
	if _, err := time.Parse(time.RFC3339Nano, killedHit.KilledAt); err != nil {
		t.Errorf("killed hit timestamp = %q: %v", killedHit.KilledAt, err)
	}
	if liveHit.Via != "card" ||
		liveHit.Status != string(board.StatusTodo) ||
		liveHit.Reason != "" ||
		liveHit.KilledAt != "" {
		t.Errorf("live hit = %+v", *liveHit)
	}
}

// TestSearchSimilarStillReportsAGenuineKilledNearMatch prevents the relevance
// gate from hiding rejected work when its title is a real reworded duplicate.
func TestSearchSimilarStillReportsAGenuineKilledNearMatch(t *testing.T) {
	s := newStore(t)
	killed := addSearchTask(t, s, board.Task{
		Title:  "Reject legacy SSO login for the admin portal",
		Status: board.StatusCancelled,
	})
	if err := s.RecordTombstone("alice", killed.ID, "The replacement shipped elsewhere"); err != nil {
		t.Fatalf("RecordTombstone: %v", err)
	}

	hits, err := s.SearchSimilar("alice", "SSO login for admins", "", nil, 3)
	if err != nil {
		t.Fatalf("SearchSimilar: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != killed.ID || hits[0].Via != "killed" {
		t.Fatalf("SearchSimilar = %+v, want killed near-match %q", hits, killed.ID)
	}
}

// TestCancelledCardWithoutATombstoneIsStillAnOrdinaryHit avoids inventing a
// rejection reason for cancelled cards created before the graveyard existed.
func TestCancelledCardWithoutATombstoneIsStillAnOrdinaryHit(t *testing.T) {
	s := newStore(t)
	cancelled := addSearchTask(t, s, board.Task{
		Title:  "Ordinary cancelled compatibility sentinel",
		Status: board.StatusCancelled,
	})

	hits, err := s.SearchSimilar("alice", "ordinary cancelled compatibility sentinel", "", nil, 10)
	if err != nil {
		t.Fatalf("SearchSimilar: %v", err)
	}
	if len(hits) != 1 ||
		hits[0].ID != cancelled.ID ||
		hits[0].Via != "card" ||
		hits[0].Reason != "" ||
		hits[0].KilledAt != "" {
		t.Fatalf("SearchSimilar = %+v, want one ordinary cancelled card", hits)
	}
}

// TestGraveyardScopeIsolation is the structural guarantee that makes a future
// team-scope rollout additive instead of exposing one user's decisions to another.
func TestGraveyardScopeIsolation(t *testing.T) {
	s := newStore(t)
	const title = "Shared graveyard alpha beta gamma"
	scopes := []string{"alice", "bob"}
	tasks := make(map[string]board.Task, len(scopes))
	reasons := map[string]string{
		"alice": "Rejected only in alice scope",
		"bob":   "Rejected only in bob scope",
	}
	for _, scope := range scopes {
		task, err := s.AddTask(scope, board.Task{
			Title:  title,
			Status: board.StatusCancelled,
		})
		if err != nil {
			t.Fatalf("AddTask(%q): %v", scope, err)
		}
		if err := s.RecordTombstone(scope, task.ID, reasons[scope]); err != nil {
			t.Fatalf("RecordTombstone(%q): %v", scope, err)
		}
		tasks[scope] = task
	}

	for index, scope := range scopes {
		other := scopes[1-index]
		t.Run(scope, func(t *testing.T) {
			for _, query := range []string{"shared", "graveyard", "alpha", "beta", "gamma", title} {
				hits, err := s.SearchSimilar(scope, query, "", nil, 10)
				if err != nil {
					t.Fatalf("SearchSimilar(%q): %v", query, err)
				}
				own := false
				for _, hit := range hits {
					if hit.ID == tasks[other].ID || hit.Reason == reasons[other] {
						t.Fatalf("SearchSimilar(%q) leaked %q into %q: %+v", query, other, scope, hits)
					}
					if hit.ID == tasks[scope].ID && hit.Reason == reasons[scope] {
						own = true
					}
				}
				if !own {
					t.Fatalf("SearchSimilar(%q) = %+v, want scoped task %q", query, hits, tasks[scope].ID)
				}
			}
		})
	}

	if got, found, err := s.Tombstone("bob", tasks["alice"].ID); err != nil || found {
		t.Fatalf("bob Tombstone(alice task) = %+v, %t, %v, want not found", got, found, err)
	}
}

// TestRetitledCardRemovesItsOldTombstone proves full-board replacement purges
// reasons whose canonical task identity no longer exists.
func TestRetitledCardRemovesItsOldTombstone(t *testing.T) {
	s := newStore(t)
	original := addSearchTask(t, s, board.Task{
		Title:  "Original graveyard identity",
		Status: board.StatusCancelled,
	})
	if err := s.RecordTombstone("alice", original.ID, "The old title was rejected"); err != nil {
		t.Fatalf("RecordTombstone: %v", err)
	}
	if err := s.ReplaceBoard("alice", board.Board{
		Title: "Board",
		Tasks: []board.Task{{
			Title:  "Retitled graveyard identity",
			Status: board.StatusCancelled,
		}},
	}); err != nil {
		t.Fatalf("ReplaceBoard: %v", err)
	}
	current, err := s.Board("alice")
	if err != nil || len(current.Tasks) != 1 {
		t.Fatalf("Board after retitle = %+v, %v", current, err)
	}
	if current.Tasks[0].ID == original.ID {
		t.Fatalf("retitled task kept old ID %q", original.ID)
	}
	if got, found, err := s.Tombstone("alice", original.ID); err != nil || found {
		t.Fatalf("old Tombstone = %+v, found %t, err %v, want removed", got, found, err)
	}

	hits, err := s.SearchSimilar("alice", "retitled graveyard identity", "", nil, 10)
	if err != nil {
		t.Fatalf("SearchSimilar retitled card: %v", err)
	}
	if len(hits) != 1 ||
		hits[0].ID != current.Tasks[0].ID ||
		hits[0].Via != "card" ||
		hits[0].Reason != "" ||
		hits[0].KilledAt != "" {
		t.Fatalf("SearchSimilar retitled card = %+v, want an ordinary fresh hit", hits)
	}
}

// TestPurgingACardSweepsItsTombstone prevents hard deletion from leaving a
// graveyard row that can never become visible again.
func TestPurgingACardSweepsItsTombstone(t *testing.T) {
	s := newStore(t)
	task := addSearchTask(t, s, board.Task{
		Title:  "Purge graveyard sentinel",
		Status: board.StatusCancelled,
	})
	if err := s.RecordTombstone("alice", task.ID, "This reason should be purged"); err != nil {
		t.Fatalf("RecordTombstone: %v", err)
	}

	deleted, err := s.DeleteTask("alice", task.ID)
	if err != nil || deleted.ID != task.ID {
		t.Fatalf("DeleteTask = %+v, %v", deleted, err)
	}
	if got, found, err := s.Tombstone("alice", task.ID); err != nil || found {
		t.Fatalf("Tombstone after purge = %+v, %t, %v, want not found", got, found, err)
	}
}

// TestRecordTombstoneRequiresACancelledScopedTask rejects missing and active
// targets without disturbing a valid cancelled task's reason.
func TestRecordTombstoneRequiresACancelledScopedTask(t *testing.T) {
	s := newStore(t)
	live := addSearchTask(t, s, board.Task{
		Title:  "Live sweep sentinel",
		Status: board.StatusCancelled,
	})
	if err := s.RecordTombstone("alice", live.ID, "Keep this live reason"); err != nil {
		t.Fatalf("RecordTombstone live: %v", err)
	}
	active := addSearchTask(t, s, board.Task{Title: "Active sentinel", Status: board.StatusTodo})
	for _, target := range []struct {
		name, scope, id string
	}{
		{name: "missing", scope: "alice", id: "missing-task"},
		{name: "active", scope: "alice", id: active.ID},
		{name: "cross scope", scope: "bob", id: live.ID},
	} {
		t.Run(target.name, func(t *testing.T) {
			if err := s.RecordTombstone(target.scope, target.id, "Must be rejected"); !errors.Is(err, ErrTombstoneTaskNotCancelled) {
				t.Fatalf("RecordTombstone = %v, want ErrTombstoneTaskNotCancelled", err)
			}
		})
	}
	got, found, err := s.Tombstone("alice", live.ID)
	if err != nil || !found || got.Reason != "Keep this live reason" {
		t.Fatalf("valid Tombstone after rejected writes = %+v, %t, %v", got, found, err)
	}
}

func TestRestoreAndFullBoardReplacementRemoveTombstones(t *testing.T) {
	t.Run("task move restore", func(t *testing.T) {
		s := newStore(t)
		task := addSearchTask(t, s, board.Task{Title: "Restore me", Status: board.StatusCancelled})
		if err := s.RecordTombstone("alice", task.ID, "No longer wanted"); err != nil {
			t.Fatalf("RecordTombstone: %v", err)
		}
		if _, err := s.MoveTask("alice", task.ID, board.StatusTodo); err != nil {
			t.Fatalf("MoveTask restore: %v", err)
		}
		if got, found, err := s.Tombstone("alice", task.ID); err != nil || found {
			t.Fatalf("Tombstone after restore = %+v, %t, %v", got, found, err)
		}
	})

	t.Run("combined update and move restore", func(t *testing.T) {
		s := newStore(t)
		task := addSearchTask(t, s, board.Task{Title: "Restore and rename", Status: board.StatusCancelled})
		if err := s.RecordTombstone("alice", task.ID, "Old rejection"); err != nil {
			t.Fatalf("RecordTombstone: %v", err)
		}
		title := "Restored and renamed"
		to := board.StatusDoing
		updated, err := s.UpdateAndMoveTask("alice", task.ID, TaskPatch{Title: &title}, &to, nil)
		if err != nil || updated.Status != to || updated.Title != title {
			t.Fatalf("UpdateAndMoveTask = %+v, %v", updated, err)
		}
		if got, found, err := s.Tombstone("alice", task.ID); err != nil || found {
			t.Fatalf("Tombstone after combined restore = %+v, %t, %v", got, found, err)
		}
	})

	t.Run("full board restore and purge", func(t *testing.T) {
		s := newStore(t)
		restored := addSearchTask(t, s, board.Task{Title: "Restore in board", Status: board.StatusCancelled})
		purged := addSearchTask(t, s, board.Task{Title: "Purge in board", Status: board.StatusCancelled})
		for _, task := range []board.Task{restored, purged} {
			if err := s.RecordTombstone("alice", task.ID, "Old reason"); err != nil {
				t.Fatalf("RecordTombstone(%s): %v", task.ID, err)
			}
		}
		ids := []*string{&restored.ID}
		snapshot, err := s.ReadBoardSnapshot("alice")
		if err != nil {
			t.Fatalf("ReadBoardSnapshot: %v", err)
		}
		if _, _, err := s.ReplaceBoardIfRevision("alice", board.Board{Title: "Board", Tasks: []board.Task{{Title: restored.Title, Status: board.StatusTodo}}}, ids, snapshot.Revision); err != nil {
			t.Fatalf("ReplaceBoardIfRevision: %v", err)
		}
		for _, id := range []string{restored.ID, purged.ID} {
			if got, found, err := s.Tombstone("alice", id); err != nil || found {
				t.Fatalf("Tombstone(%s) after replacement = %+v, %t, %v", id, got, found, err)
			}
		}
	})
}

func TestImportProvenanceReplayKeepsOneLogicalRow(t *testing.T) {
	s := newStore(t)
	link := ImportLink{
		Source:      "primary",
		Kind:        "gitlab",
		ExternalKey: "gitlab:example.test/group/project#42",
		Link:        "gitlab#42",
		URL:         "https://example.test/group/project/-/issues/42",
		Title:       "Initial import",
	}
	if err := s.RecordImportLinks("alice", []ImportLink{link}); err != nil {
		t.Fatalf("RecordImportLinks first: %v", err)
	}
	link.Title = "Refreshed import"
	if err := s.RecordImportLinks("alice", []ImportLink{link}); err != nil {
		t.Fatalf("RecordImportLinks replay: %v", err)
	}
	var rows int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM import_links WHERE scope = ? AND external_key = ?`, "alice", link.ExternalKey).Scan(&rows); err != nil {
		t.Fatalf("count import provenance rows: %v", err)
	}
	if rows != 1 {
		t.Fatalf("replayed import provenance rows = %d, want 1", rows)
	}
	got, err := s.ImportedAs("alice", []string{link.ExternalKey})
	if err != nil || got[link.ExternalKey].Title != link.Title {
		t.Fatalf("ImportedAs after replay = %+v, %v", got, err)
	}
}
