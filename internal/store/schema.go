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
	// v3: forge configuration and durable import provenance. The FTS tables are
	// plain tables rather than external-content tables because tasks has a TEXT
	// primary key and implicit rowids are not stable across VACUUM.
	//
	// FTS calls its searchable description column body because desc is reserved
	// inside fts5(). Triggers maintain both indexes in the writer's transaction,
	// so every process stays current without background work. import_links has no
	// task_id because ReplaceBoard regenerates IDs; the link:: tag is durable.
	`
CREATE TABLE forge_sources (
  scope      TEXT NOT NULL,
  name       TEXT NOT NULL,
  kind       TEXT NOT NULL,
  base_url   TEXT NOT NULL,
  pat_enc    BLOB,
  created_at TEXT NOT NULL,
  PRIMARY KEY (scope, name)
);
CREATE TABLE import_links (
  scope        TEXT NOT NULL,
  source       TEXT NOT NULL,
  kind         TEXT NOT NULL,
  external_key TEXT NOT NULL,
  link         TEXT NOT NULL,
  url          TEXT NOT NULL,
  title        TEXT NOT NULL,
  imported_at  TEXT NOT NULL,
  PRIMARY KEY (scope, external_key)
);
CREATE VIRTUAL TABLE tasks_fts USING fts5(
  title, body, tags, id UNINDEXED, scope UNINDEXED,
  tokenize = 'unicode61 remove_diacritics 2'
);
CREATE VIRTUAL TABLE import_links_fts USING fts5(
  title, external_key UNINDEXED, scope UNINDEXED,
  tokenize = 'unicode61 remove_diacritics 2'
);
CREATE TRIGGER tasks_fts_ai AFTER INSERT ON tasks BEGIN
  INSERT INTO tasks_fts(id, scope, title, body, tags)
  VALUES (new.id, new.user, new.title, new."desc", new.tags);
END;
CREATE TRIGGER tasks_fts_ad AFTER DELETE ON tasks BEGIN
  DELETE FROM tasks_fts WHERE id = old.id;
END;
CREATE TRIGGER tasks_fts_au AFTER UPDATE OF title, "desc", tags ON tasks BEGIN
  DELETE FROM tasks_fts WHERE id = old.id;
  INSERT INTO tasks_fts(id, scope, title, body, tags)
  VALUES (new.id, new.user, new.title, new."desc", new.tags);
END;
CREATE TRIGGER import_links_fts_ai AFTER INSERT ON import_links BEGIN
  INSERT INTO import_links_fts(title, external_key, scope)
  VALUES (new.title, new.external_key, new.scope);
END;
CREATE TRIGGER import_links_fts_au AFTER UPDATE ON import_links BEGIN
  DELETE FROM import_links_fts WHERE external_key = old.external_key AND scope = old.scope;
  INSERT INTO import_links_fts(title, external_key, scope)
  VALUES (new.title, new.external_key, new.scope);
END;
CREATE TRIGGER import_links_fts_ad AFTER DELETE ON import_links BEGIN
  DELETE FROM import_links_fts WHERE external_key = old.external_key AND scope = old.scope;
END;
INSERT INTO tasks_fts(id, scope, title, body, tags)
  SELECT id, user, title, "desc", tags FROM tasks;
	`,
	// v4: decision-graveyard reasons and lazy import-drift baselines. Tombstones
	// deliberately have no FTS mirror, triggers, or index because searches use
	// the card title already in tasks_fts and only join a reason onto that hit.
	// The scoped task ID key follows ReplaceBoard's stable identity for matched
	// cards; a retitle instead leaves a harmless orphan for the bounded sweep.
	// There is deliberately no tasks foreign key because ReplaceBoard deletes
	// and reinserts its rows, which would otherwise cascade a reason away.
	// Empty baseline defaults preserve existing imports as "not recorded yet".
	`
CREATE TABLE tombstones (
  scope     TEXT NOT NULL,
  task_id   TEXT NOT NULL,
  reason    TEXT NOT NULL,
  killed_at TEXT NOT NULL,
  PRIMARY KEY (scope, task_id)
);
ALTER TABLE import_links ADD COLUMN baseline_title TEXT NOT NULL DEFAULT '';
ALTER TABLE import_links ADD COLUMN baseline_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE import_links ADD COLUMN baseline_excerpt TEXT NOT NULL DEFAULT '';
ALTER TABLE import_links ADD COLUMN baseline_at TEXT NOT NULL DEFAULT '';
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
