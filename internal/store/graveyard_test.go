package store

import (
	"database/sql"
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
	if schemaVersion != "4" {
		t.Fatalf("schema version = %q, want 4", schemaVersion)
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
	if schemaVersion != "4" {
		t.Errorf("schema version after no-op migrate = %q, want 4", schemaVersion)
	}
}

// TestRecordTombstoneIsAnUpsert prevents a repeated kill from leaving stale
// reasoning or the timestamp from the first decision.
func TestRecordTombstoneIsAnUpsert(t *testing.T) {
	s := newStore(t)
	task := addSearchTask(t, s, board.Task{Title: "Retire the legacy login"})

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

	hits, err := s.SearchSimilar("alice", "shared authentication marker", "", 10)
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

// TestCancelledCardWithoutATombstoneIsStillAnOrdinaryHit avoids inventing a
// rejection reason for cancelled cards created before the graveyard existed.
func TestCancelledCardWithoutATombstoneIsStillAnOrdinaryHit(t *testing.T) {
	s := newStore(t)
	cancelled := addSearchTask(t, s, board.Task{
		Title:  "Ordinary cancelled compatibility sentinel",
		Status: board.StatusCancelled,
	})

	hits, err := s.SearchSimilar("alice", "ordinary cancelled compatibility sentinel", "", 10)
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
