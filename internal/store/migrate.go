package store

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/RandomCodeSpace/kb/internal/board"
)

func importKey(user string) string { return "imported:" + user }

// ImportMarkdownDir seeds the database from legacy per-user markdown boards.
// For each <user>.md in dir the board is parsed and inserted, but only when
// that user has never been imported before and has zero tasks in the
// database; users with existing tasks are skipped (and marked so they are
// never reimported either). The original files are left untouched. It
// returns the number of boards imported.
func (s *Store) ImportMarkdownDir(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("store: read markdown dir: %w", err)
	}
	imported := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".md") {
			continue
		}
		user := strings.TrimSuffix(name, ".md")
		if user == "" {
			continue
		}
		did, err := s.importBoard(filepath.Join(dir, name), user)
		if err != nil {
			return imported, err
		}
		if did {
			imported++
		}
	}
	return imported, nil
}

// importBoard imports one markdown file inside a transaction, guarded by the
// per-user "imported" meta flag and the zero-tasks check.
func (s *Store) importBoard(path, user string) (bool, error) {
	did := false
	err := s.withTx(func(tx *sql.Tx) error {
		eligible, err := importBoardEligible(tx, user)
		if err != nil || !eligible {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("store: read %s: %w", path, err)
		}
		if err := s.insertImportedBoard(tx, user, board.Parse(string(data))); err != nil {
			return err
		}
		did = true
		return nil
	})
	return did, err
}

func importBoardEligible(tx *sql.Tx, user string) (bool, error) {
	var value string
	err := tx.QueryRow(`SELECT v FROM meta WHERE k = ?`, importKey(user)).Scan(&value)
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("store: import flag: %w", err)
	}
	var taskCount int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM tasks WHERE user = ?`, user).Scan(&taskCount); err != nil {
		return false, fmt.Errorf("store: count tasks: %w", err)
	}
	if taskCount == 0 {
		return true, nil
	}
	if err := setMeta(tx, importKey(user), "skipped"); err != nil {
		return false, err
	}
	return false, nil
}

func (s *Store) insertImportedBoard(tx *sql.Tx, user string, imported board.Board) error {
	now := time.Now().UTC()
	positions := map[board.Status]int{}
	for _, task := range imported.Tasks {
		task.ID = uuid.NewString()
		task.CreatedAt, task.MovedAt = now, now
		task.Position = positions[task.Status]
		positions[task.Status]++
		if err := insertTask(tx, user, task); err != nil {
			return err
		}
		if err := s.upsertLabels(tx, user, task.Tags); err != nil {
			return err
		}
	}
	if err := setMeta(tx, titleKey(user), imported.Title); err != nil {
		return err
	}
	return setMeta(tx, importKey(user), "imported")
}
