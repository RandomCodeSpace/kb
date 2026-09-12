package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// BackupIncompleteFile remains in a destination until its complete copy is durable.
const BackupIncompleteFile = ".kb-backup-incomplete"

func checkPartialCopy(path string) error {
	marker := filepath.Join(filepath.Dir(path), BackupIncompleteFile)
	if _, err := os.Lstat(marker); err == nil {
		return fmt.Errorf("store: %s marks an incomplete backup: restore a completed whole-directory backup", marker)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("store: inspect backup marker: %w", err)
	}
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("store: inspect existing database: %w", err)
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(path + suffix); err == nil {
			return fmt.Errorf("store: %s exists but %s does not: partial copy; restore the whole data directory including kb.db", path+suffix, path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("store: inspect database sidecar: %w", err)
		}
	}
	return nil
}

func checkDatabaseIntegrity(db *sql.DB) error {
	var result string
	if err := db.QueryRow(`PRAGMA quick_check(1)`).Scan(&result); err != nil {
		return fmt.Errorf("store: database integrity check failed: %w; restore a complete backup if the database is damaged", err)
	}
	if result != "ok" {
		return fmt.Errorf("store: database integrity check failed: %s; restore a complete backup", result)
	}
	return nil
}

// prepareDatabaseFile publishes a valid empty SQLite file before other openers
// can observe it. A pre-existing zero-byte file therefore means damaged or
// incomplete data, rather than another process still creating its first board.
func prepareDatabaseFile(path string) error {
	if err := checkPartialCopy(path); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := publishEmptyDatabase(path); err != nil {
			return err
		}
		info, err = os.Stat(path)
	}
	if err != nil {
		return fmt.Errorf("store: inspect database: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("store: database %s is not a regular file", path)
	}
	if info.Size() == 0 {
		return fmt.Errorf("store: database %s is empty: possible partial copy; restore the whole data directory from a complete backup", path)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("store: chmod %s: %w", path, err)
	}
	return nil
}

func publishEmptyDatabase(path string) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".kb-db-*")
	if err != nil {
		return fmt.Errorf("store: create database: %w", err)
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	defer os.Remove(temporary + "-journal")
	if err := file.Close(); err != nil {
		return fmt.Errorf("store: close database candidate: %w", err)
	}
	dsn, err := SQLiteDSN(temporary, "synchronous(FULL)")
	if err != nil {
		return err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return err
	}
	// Let SQLite write its own empty database header. No application rows or
	// migrations are written until the final filename is published.
	_, initErr := db.Exec(`PRAGMA user_version=0`)
	if err := errors.Join(initErr, db.Close()); err != nil {
		return fmt.Errorf("store: initialize database candidate: %w", err)
	}
	if err := os.Link(temporary, path); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("store: publish database without replacing existing data: %w", err)
	}
	if err := os.Remove(temporary); err != nil {
		return fmt.Errorf("store: remove database candidate: %w", err)
	}
	return syncSecretDirectory(filepath.Dir(path))
}
