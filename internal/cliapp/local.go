package cliapp

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
)

// dbFile is the SQLite database filename inside the data directory.
const dbFile = "kb.db"

// defaultDataDir resolves $KB_DATA, else ~/.local/share/kb.
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
	st              *store.Store
	user            string
	beforeDoneGuard func()
}

// openLocal opens (creating if needed) <dataDir>/kb.db with the shared
// secret and seeds it from any legacy per-user markdown boards in dataDir
// (idempotent; the store never reimports a user).
func openLocal(user, dataDir string, stderr io.Writer) (*localBackend, error) {
	st, err := openLocalStore(dataDir, stderr)
	if err != nil {
		return nil, err
	}
	return &localBackend{st: st, user: user}, nil
}

// openLocalStore opens the SQLite store itself for commands that are not
// scoped to one board owner (users). Same resolution and legacy markdown
// seeding as openLocal.
func openLocalStore(dataDir string, stderr io.Writer) (*store.Store, error) {
	return OpenLocalStore(dataDir, stderr)
}

// OpenLocalStore opens the local SQLite store with the same data-directory,
// secret, and legacy-import behavior used by the task CLI. Other local human
// interfaces use this rather than quietly inventing a second startup path.
func OpenLocalStore(dataDir string, stderr io.Writer) (*store.Store, error) {
	dataDir, err := resolveDataDir(dataDir)
	if err != nil {
		return nil, err
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
	// Tasks that predate mandatory projects are labelled here, on the one
	// startup path every local surface shares, so the invariant is already
	// true by the time any command can observe it. Idempotent and silent once
	// the board is clean.
	if _, err := BackfillProjects(st, defaultUser); err != nil {
		fmt.Fprintf(stderr, "kb: warning: project backfill: %v\n", err)
	}
	warnOrphanedNamespaces(st, stderr)
	return st, nil
}

// warnOrphanedNamespaces prints one stderr line when the database still holds
// tasks under a namespace other than defaultUser. The --user/KB_USER surface
// is gone, so the local commands can no longer reach those rows; nothing is
// deleted.
func warnOrphanedNamespaces(st *store.Store, stderr io.Writer) {
	users, err := st.Users()
	if err != nil {
		return
	}
	orphans := make([]string, 0, len(users))
	for _, u := range users {
		if u.User != defaultUser && u.Tasks > 0 {
			orphans = append(orphans, u.User)
		}
	}
	if len(orphans) == 0 {
		return
	}
	fmt.Fprintf(stderr, "kb: warning: tasks exist under non-default namespaces (%s); local commands only use %q and leave that data untouched\n",
		strings.Join(orphans, ", "), defaultUser)
}

func (l *localBackend) close() error { return l.st.Close() }

func (l *localBackend) list(filter store.TaskFilter) ([]item, error) {
	tasks, err := l.st.FilterTasks(l.user, filter)
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
	out, err := l.st.UpdateAndMoveTask(l.user, ref, p, moveTo, nil, guard)
	if err != nil {
		return item{}, friendlyIDErr(err, ref)
	}
	return item{ref: out.ID, task: out}, nil
}

func (l *localBackend) move(ref string, to board.Status, force bool) (item, error) {
	var guard func(board.Task) error
	if to == board.StatusDone && !force {
		guard = func(t board.Task) error {
			if l.beforeDoneGuard != nil {
				l.beforeDoneGuard()
			}
			return doneGuardErr(t.ID, t)
		}
	}
	t, err := l.st.UpdateAndMoveTask(l.user, ref, store.TaskPatch{}, &to, nil, guard)
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

func (l *localBackend) view(ref string) (item, []store.Comment, store.TaskLinks, error) {
	t, err := l.st.Task(l.user, ref)
	if err != nil {
		return item{}, nil, store.TaskLinks{}, friendlyIDErr(err, ref)
	}
	comments, err := l.st.Comments(l.user, t.ID)
	if err != nil {
		return item{}, nil, store.TaskLinks{}, err
	}
	links, err := l.st.TaskLinks(l.user, t.ID)
	if err != nil {
		return item{}, nil, store.TaskLinks{}, err
	}
	return item{ref: t.ID, task: t}, comments, links, nil
}

func (l *localBackend) commentAdd(ref, body string) (store.Comment, error) {
	c, err := l.st.AddComment(l.user, ref, l.user, body)
	if err != nil {
		return store.Comment{}, friendlyIDErr(err, ref)
	}
	return c, nil
}

func (l *localBackend) comments(ref string) ([]store.Comment, error) {
	out, err := l.st.Comments(l.user, ref)
	if err != nil {
		return nil, friendlyIDErr(err, ref)
	}
	return out, nil
}

func (l *localBackend) commentRm(id int) (store.Comment, error) {
	return l.st.DeleteComment(l.user, id)
}

func (l *localBackend) link(blockerRef, blockedRef string) (board.Task, board.Task, error) {
	return l.st.Link(l.user, blockerRef, blockedRef)
}

func (l *localBackend) unlink(aRef, bRef string) error {
	return l.st.Unlink(l.user, aRef, bRef)
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
