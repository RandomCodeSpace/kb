package cliapp

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
)

// dbFile is the SQLite database filename inside the data directory.
const dbFile = "kb.db"

// defaultDataDir mirrors the server default: $KB_DATA, else
// ~/.local/share/kb.
func defaultDataDir() (string, error) {
	if v := os.Getenv("KB_DATA"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory, set KB_DATA or --data: %w", err)
	}
	return filepath.Join(home, ".local", "share", "kb"), nil
}

// localBackend runs commands directly against the SQLite store.
type localBackend struct {
	st   *store.Store
	user string
}

// openLocal opens (creating if needed) <dataDir>/kb.db with the shared
// secret and seeds it from any legacy per-user markdown boards in dataDir
// (idempotent; the store never reimports a user).
func openLocal(user, dataDir string, stderr io.Writer) (backend, error) {
	if dataDir == "" {
		d, err := defaultDataDir()
		if err != nil {
			return nil, err
		}
		dataDir = d
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	secret, err := store.LoadOrCreateSecret(dataDir)
	if err != nil {
		return nil, err
	}
	st, err := store.Open(filepath.Join(dataDir, dbFile), secret)
	if err != nil {
		return nil, err
	}
	if _, err := st.ImportMarkdownDir(dataDir); err != nil {
		fmt.Fprintf(stderr, "kb: warning: legacy markdown import: %v\n", err)
	}
	return &localBackend{st: st, user: user}, nil
}

func (l *localBackend) close() error { return l.st.Close() }

func (l *localBackend) list(status board.Status) ([]item, error) {
	tasks, err := l.st.ListTasks(l.user, status)
	if err != nil {
		return nil, err
	}
	items := make([]item, 0, len(tasks))
	for _, t := range tasks {
		items = append(items, item{ref: t.ID, task: t})
	}
	return items, nil
}

func (l *localBackend) add(t board.Task) (item, error) {
	out, err := l.st.AddTask(l.user, t)
	if err != nil {
		return item{}, err
	}
	return item{ref: out.ID, task: out}, nil
}

// update applies the field patch and then, when moveTo is set, the status
// move (MoveTask stamps MovedAt, which a plain column-field update must not)
// — both in one store transaction, so a refused move rolls the patch back
// with it rather than leaving the task half-updated.
func (l *localBackend) update(ref string, p store.TaskPatch, moveTo *board.Status, force bool) (item, error) {
	var guard func(board.Task) error
	if moveTo != nil && *moveTo == board.StatusDone && !force {
		// Judged after the patch lands: closing the last checklist item and
		// moving to done in a single update is a legitimate finish.
		guard = func(t board.Task) error { return doneGuardErr(t.ID, t) }
	}
	out, err := l.st.UpdateAndMoveTask(l.user, ref, p, moveTo, guard)
	if err != nil {
		return item{}, friendlyIDErr(err, ref)
	}
	return item{ref: out.ID, task: out}, nil
}

func (l *localBackend) move(ref string, to board.Status) (item, error) {
	t, err := l.st.MoveTask(l.user, ref, to)
	if err != nil {
		return item{}, friendlyIDErr(err, ref)
	}
	return item{ref: t.ID, task: t}, nil
}

func (l *localBackend) remove(ref string) (item, error) {
	t, err := l.st.DeleteTask(l.user, ref)
	if err != nil {
		return item{}, friendlyIDErr(err, ref)
	}
	return item{ref: t.ID, task: t}, nil
}

// friendlyIDErr rewords the store's prefix-resolution sentinels with the id
// the user actually typed.
func friendlyIDErr(err error, ref string) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return fmt.Errorf("no task matches id %q", ref)
	case errors.Is(err, store.ErrAmbiguous):
		return fmt.Errorf("task id prefix %q is ambiguous; use more characters", ref)
	}
	return err
}
