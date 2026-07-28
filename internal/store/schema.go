package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// migrations holds one DDL script per schema version. migrate applies the
// scripts the database has not yet seen, recording progress in meta under
// "schema_version", so it is idempotent across restarts.
var migrations = []string{
	// v1: initial schema.
	`
CREATE TABLE IF NOT EXISTS tasks (
	id         TEXT PRIMARY KEY,
	user       TEXT NOT NULL,
	emoji      TEXT NOT NULL DEFAULT '',
	title      TEXT NOT NULL DEFAULT '',
	"desc"     TEXT NOT NULL DEFAULT '',
	status     TEXT NOT NULL,
	prio       INTEGER NOT NULL DEFAULT 3,
	due        TEXT NOT NULL DEFAULT '',
	effort     TEXT NOT NULL DEFAULT '',
	tags       TEXT NOT NULL DEFAULT 'null',
	checks     TEXT NOT NULL DEFAULT 'null',
	position   INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL,
	moved_at   TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS tasks_user_status_position ON tasks (user, status, position);
CREATE TABLE IF NOT EXISTS labels (
	user      TEXT NOT NULL,
	label     TEXT NOT NULL,
	last_used INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (user, label)
);
CREATE TABLE IF NOT EXISTS settings (
	user        TEXT PRIMARY KEY,
	ai_base_url TEXT NOT NULL DEFAULT '',
	ai_model    TEXT NOT NULL DEFAULT '',
	ai_key_enc  BLOB
);
`,
	// v2: blocked flag (wire token "%blocked"); the "cancelled" status needs
	// no DDL because status is stored as free text.
	`
ALTER TABLE tasks ADD COLUMN blocked INTEGER NOT NULL DEFAULT 0;
`,
}

// migrate creates the meta table and applies any pending schema versions.
func migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS meta (k TEXT PRIMARY KEY, v TEXT NOT NULL)`); err != nil {
		return fmt.Errorf("store: create meta: %w", err)
	}
	var version int
	err := db.QueryRow(`SELECT CAST(v AS INTEGER) FROM meta WHERE k = 'schema_version'`).Scan(&version)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("store: read schema version: %w", err)
	}
	for v := version; v < len(migrations); v++ {
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("store: begin migration: %w", err)
		}
		if _, err := tx.Exec(migrations[v]); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("store: migrate to v%d: %w", v+1, err)
		}
		if _, err := tx.Exec(`INSERT INTO meta (k, v) VALUES ('schema_version', ?) ON CONFLICT(k) DO UPDATE SET v = excluded.v`, v+1); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("store: record schema version %d: %w", v+1, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("store: commit migration v%d: %w", v+1, err)
		}
	}
	return nil
}
