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
		var v string
		switch err := tx.QueryRow(`SELECT v FROM meta WHERE k = ?`, importKey(user)).Scan(&v); {
		case err == nil:
			return nil // already handled once; never reimport
		case !errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("store: import flag: %w", err)
		}
		var n int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM tasks WHERE user = ?`, user).Scan(&n); err != nil {
			return fmt.Errorf("store: count tasks: %w", err)
		}
		if n > 0 {
			return setMeta(tx, importKey(user), "skipped")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("store: read %s: %w", path, err)
		}
		b := board.Parse(string(data))
		now := time.Now().UTC()
		pos := map[board.Status]int{}
		for _, t := range b.Tasks {
			t.ID = uuid.NewString()
			t.CreatedAt, t.MovedAt = now, now
			t.Position = pos[t.Status]
			pos[t.Status]++
			if err := insertTask(tx, user, t); err != nil {
				return err
			}
			if err := s.upsertLabels(tx, user, t.Tags); err != nil {
				return err
			}
		}
		if err := setMeta(tx, titleKey(user), b.Title); err != nil {
			return err
		}
		if err := setMeta(tx, importKey(user), "imported"); err != nil {
			return err
		}
		did = true
		return nil
	})
	return did, err
}
