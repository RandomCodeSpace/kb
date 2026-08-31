// Package store persists kanban boards, labels, and per-user AI settings in
// a single SQLite database via modernc.org/sqlite (pure Go, WAL mode).
//
// Every operation is scoped by user. Task lookups accept a unique ID prefix;
// ErrNotFound and ErrAmbiguous report failed resolution. API keys are stored
// AES-GCM encrypted with the secret handed to Open.
package store

import (
	"crypto/cipher"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	sqliteDriver "modernc.org/sqlite" // database/sql driver "sqlite"

	"github.com/RandomCodeSpace/kb/internal/board"
)

// Sentinel errors for task ID prefix resolution.
var (
	ErrNotFound         = errors.New("task not found")
	ErrAmbiguous        = errors.New("ambiguous task id prefix")
	ErrTaskNotCancelled = errors.New("task is not cancelled")
)

// TaskFieldsConflictError reports task fields that no longer match a
// caller's expected values. Callers should keep their local edits and refresh
// the task instead of retrying the stale patch unconditionally.
type TaskFieldsConflictError struct {
	Fields []string
}

func (e *TaskFieldsConflictError) Error() string {
	return "store: task changed in " + strings.Join(e.Fields, ", ")
}

// BoardSnapshot is one transactionally consistent view of a user's board.
type BoardSnapshot struct {
	Board    board.Board
	Exists   bool
	TaskIDs  []string
	Revision int64
}

// dueRe matches the wire-format due date (calendar validity checked
// separately).
var dueRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// validateTaskLines rejects CR/LF in every field board.Serialize writes on a
// single line. Without this a title such as "x\n- [x] forged !1" would
// serialize as an extra board line and re-parse as a different task. Phase 1
// held the invariant implicitly because every write went through
// board.Parse; direct store writers must enforce it here.
func validateTaskLines(t board.Task) error {
	for _, f := range []struct{ name, v string }{
		{"title", t.Title}, {"emoji", t.Emoji}, {"due", t.Due}, {"effort", t.Effort},
	} {
		if strings.ContainsAny(f.v, "\r\n") {
			return fmt.Errorf("store: %s must not contain newlines", f.name)
		}
	}
	for _, tag := range t.Tags {
		if strings.ContainsAny(tag, "\r\n") {
			return fmt.Errorf("store: tag %q must not contain newlines", tag)
		}
	}
	for _, c := range t.Checks {
		if strings.ContainsAny(c.Text, "\r\n") {
			return errors.New("store: checklist text must not contain newlines")
		}
	}
	return nil
}

// ValidateTaskFields enforces, for direct task writers such as AddTask and
// UpdateTask, the field formats a task must satisfy to survive the Markdown
// codec unchanged: a non-blank title, a real YYYY-MM-DD due date, S/M/L effort,
// single-token tags without a leading '#', and a single-emoji Emoji.
// ReplaceBoard deliberately skips these checks: it ingests board.Parse
// output, which is defined to be tolerant of odd-but-representable values.
func ValidateTaskFields(t board.Task) error {
	if err := validateTaskLines(t); err != nil {
		return err
	}
	// A blank title serializes to a bare "- [ ] " line that board.Parse reads
	// back as description text, silently losing the task and grafting its
	// desc and checks onto the task before it.
	if board.IsBlank(t.Title) {
		return errors.New("store: title must not be empty")
	}
	if t.Due != "" {
		if !dueRe.MatchString(t.Due) {
			return fmt.Errorf("store: invalid due date %q (want YYYY-MM-DD)", t.Due)
		}
		if _, err := time.Parse("2006-01-02", t.Due); err != nil {
			return fmt.Errorf("store: invalid due date %q: not a real date", t.Due)
		}
	}
	switch t.Effort {
	case "", "S", "M", "L":
	default:
		return fmt.Errorf("store: invalid effort %q (want S, M, or L)", t.Effort)
	}
	for _, tag := range t.Tags {
		switch {
		case tag == "":
			return errors.New("store: tag must not be empty")
		case board.ContainsSpace(tag):
			return fmt.Errorf("store: invalid tag %q: must not contain whitespace", tag)
		case tag[0] == '#':
			return fmt.Errorf("store: invalid tag %q: must not start with '#'", tag)
		}
	}
	if t.Emoji != "" && !board.IsSingleEmoji(t.Emoji) {
		return fmt.Errorf("store: invalid emoji %q (want a single emoji)", t.Emoji)
	}
	return nil
}

// TaskPatch is a partial task update; nil fields are left unchanged.
type TaskPatch struct {
	Emoji, Title, Desc, Due, Effort *string
	Prio                            *int
	Blocked                         *bool
	Tags                            *[]string
	Checks                          *[]board.Check
}

// Store is a SQLite-backed board store. It is safe for concurrent use; the
// pool is capped at one connection so writers serialize instead of hitting
// SQLITE_BUSY.
type Store struct {
	db         *sql.DB
	aead       cipher.AEAD
	randomRead func([]byte) (int, error)
}

// dbtx is the subset of *sql.DB and *sql.Tx the query helpers need.
type dbtx interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// Open opens (creating if needed) the SQLite database at path, applies
// pending schema migrations, and prepares the AES-GCM cipher derived from
// secret (any length; the key is its SHA-256).
func Open(path string, secret []byte) (*Store, error) {
	aead, err := newAEAD(secret)
	if err != nil {
		return nil, err
	}
	// Create the database with private permissions before SQLite opens it. An
	// existing database is tightened too; SQLite derives WAL/SHM permissions
	// from the main database file.
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("store: create %s: %w", path, err)
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("store: chmod %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("store: close %s: %w", path, err)
	}
	// temp_store(2) keeps sorters and temp tables in memory. The default lets
	// SQLite spill them to $TMPDIR, which would put board text outside the
	// data directory — kb promises that nothing does.
	// journal_mode is deliberately not a DSN initialization pragma. On a fresh
	// database it takes a write lock, so two sql.Open calls could otherwise fail
	// while acquiring their first connection, before migrate's BEGIN IMMEDIATE
	// has a chance to serialize them. busy_timeout is connection-local and is
	// installed first; WAL is enabled after the migration lock is released.
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=temp_store(2)&_pragma=synchronous(FULL)")
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := enableWAL(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := repairAIBaseURLSuffixes(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := chmodSQLiteFiles(path); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{db: db, aead: aead}
	return s, nil
}

func enableWAL(db *sql.DB) error {
	for attempt := 0; ; attempt++ {
		var mode string
		err := db.QueryRow(`PRAGMA journal_mode=WAL`).Scan(&mode)
		if err == nil {
			if !strings.EqualFold(mode, "wal") {
				return fmt.Errorf("store: enable WAL: journal mode is %q", mode)
			}
			return nil
		}
		if !isSQLiteBusy(err) || attempt >= 9 {
			return fmt.Errorf("store: enable WAL: %w", err)
		}
		time.Sleep(time.Duration(attempt+1) * time.Millisecond)
	}
}

func chmodSQLiteFiles(path string) error {
	for _, name := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Chmod(name, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("store: chmod %s: %w", name, err)
		}
	}
	return nil
}

// repairAIBaseURLSuffixes removes pre-validation URL query and fragment
// suffixes from settings left by older releases. It runs after every migration
// in one transaction, so it also repairs databases already at the current
// schema without creating another schema version.
func repairAIBaseURLSuffixes(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("store: begin URL suffix repair: %w", err)
	}
	if _, err := tx.Exec(`UPDATE settings SET ai_base_url = CASE
		WHEN instr(ai_base_url, '?') > 0 AND (instr(ai_base_url, '#') = 0 OR instr(ai_base_url, '?') < instr(ai_base_url, '#')) THEN substr(ai_base_url, 1, instr(ai_base_url, '?') - 1)
		WHEN instr(ai_base_url, '#') > 0 THEN substr(ai_base_url, 1, instr(ai_base_url, '#') - 1)
		ELSE ai_base_url END
		WHERE instr(ai_base_url, '?') > 0 OR instr(ai_base_url, '#') > 0`); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("store: repair AI URL suffixes: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit URL suffix repair: %w", err)
	}
	return nil
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

// withTx runs fn inside a transaction, committing on nil and rolling back on
// error.
func (s *Store) withTx(fn func(tx *sql.Tx) error) error {
	for attempt := 0; ; attempt++ {
		tx, err := s.db.Begin()
		if err != nil {
			return fmt.Errorf("store: begin: %w", err)
		}
		if err := fn(tx); err != nil {
			_ = tx.Rollback()
			if isSQLiteBusy(err) && attempt < 9 {
				time.Sleep(time.Duration(attempt+1) * time.Millisecond)
				continue
			}
			return err
		}
		if err := tx.Commit(); err != nil {
			_ = tx.Rollback()
			if isSQLiteBusy(err) && attempt < 9 {
				time.Sleep(time.Duration(attempt+1) * time.Millisecond)
				continue
			}
			return fmt.Errorf("store: commit: %w", err)
		}
		return nil
	}
}

func isSQLiteBusy(err error) bool {
	var sqliteErr *sqliteDriver.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	code := sqliteErr.Code() & 0xff
	return code == 5 || code == 6 // SQLITE_BUSY or SQLITE_LOCKED
}

// taskCols is the canonical select list matched by scanTask.
const taskCols = `id, seq, emoji, title, "desc", status, blocked, prio, due, effort, tags, checks, position, created_at, moved_at`

// statusRank orders rows by board column order (todo, doing, done,
// cancelled).
const statusRank = `CASE status WHEN 'todo' THEN 0 WHEN 'doing' THEN 1 WHEN 'done' THEN 2 WHEN 'cancelled' THEN 3 ELSE 4 END`

func titleKey(user string) string { return "board_title:" + user }

// Board returns the user's board with tasks ordered by status column order
// then position. The title defaults to "Board" when none has been stored.
func (s *Store) Board(user string) (board.Board, error) {
	snapshot, err := s.ReadBoardSnapshot(user)
	if err != nil {
		return board.Board{}, err
	}
	return snapshot.Board, nil
}

// ReadBoardSnapshot returns the board, existence flag, canonical task IDs,
// and revision from one SQLite read transaction. A missing board has the
// default in-memory title, no IDs, Exists false, and revision zero unless a
// prior board was deleted (in which case its revision remains monotonic).
func (s *Store) ReadBoardSnapshot(user string) (BoardSnapshot, error) {
	return s.readBoardSnapshot(user, nil)
}

func (s *Store) readBoardSnapshot(user string, afterTasks func()) (BoardSnapshot, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return BoardSnapshot{}, fmt.Errorf("store: begin board snapshot: %w", err)
	}
	defer tx.Rollback()

	b := board.Board{Title: "Board"}
	exists := false
	var title string
	switch err := tx.QueryRow(`SELECT v FROM meta WHERE k = ?`, titleKey(user)).Scan(&title); {
	case err == nil:
		b.Title = title
		exists = true
	case !errors.Is(err, sql.ErrNoRows):
		return BoardSnapshot{}, fmt.Errorf("store: board title: %w", err)
	}
	tasks, err := queryTasks(tx, `SELECT `+taskCols+` FROM tasks WHERE user = ? ORDER BY `+statusRank+`, position`, user)
	if err != nil {
		return BoardSnapshot{}, err
	}
	b.Tasks = tasks
	if len(tasks) > 0 {
		exists = true
	}
	if afterTasks != nil {
		afterTasks()
	}
	ids := make([]string, len(tasks))
	for i := range tasks {
		ids[i] = tasks[i].ID
	}
	var revision int64
	switch err := tx.QueryRow(`SELECT revision FROM board_revisions WHERE user = ?`, user).Scan(&revision); {
	case err == nil:
	case errors.Is(err, sql.ErrNoRows):
		revision = 0
	default:
		return BoardSnapshot{}, fmt.Errorf("store: board revision: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return BoardSnapshot{}, fmt.Errorf("store: commit board snapshot: %w", err)
	}
	return BoardSnapshot{Board: b, Exists: exists, TaskIDs: ids, Revision: revision}, nil
}

// HasBoard reports whether the user has any tasks or has ever had a board
// saved (a stored title, written by ReplaceBoard and the markdown import).
func (s *Store) HasBoard(user string) (bool, error) {
	snapshot, err := s.ReadBoardSnapshot(user)
	return snapshot.Exists, err
}

// ReplaceBoard replaces the user's whole board and discards the committed
// task IDs. See ReplaceBoardWithTaskIDs for replacement semantics.
func (s *Store) ReplaceBoard(user string, b board.Board) error {
	_, err := s.ReplaceBoardWithTaskIDs(user, b)
	return err
}

// ReplaceBoardWithTaskIDs replaces the user's whole board for legacy import
// compatibility and returns each committed task ID in b.Tasks order. Incoming tasks are
// matched against existing ones first by (Status, Title), then by Title alone;
// matches keep their ID and CreatedAt, and a status change stamps MovedAt.
// Unmatched incoming tasks get fresh UUIDs; existing tasks absent from b are
// deleted. Positions are recomputed from slice order per status and labels are
// upserted from all task tags.
func (s *Store) ReplaceBoardWithTaskIDs(user string, b board.Board) ([]string, error) {
	var taskIDs []string
	err := s.withTx(func(tx *sql.Tx) error {
		var err error
		taskIDs, err = s.replaceBoardTx(tx, user, b)
		return err
	})
	return taskIDs, err
}

func (s *Store) replaceBoardTx(tx *sql.Tx, user string, b board.Board) ([]string, error) {
	now := time.Now().UTC()
	matches, err := matchLegacyReplacementTasksTx(tx, user, b.Tasks)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM tasks WHERE user = ?`, user); err != nil {
		return nil, fmt.Errorf("store: clear board: %w", err)
	}
	taskIDs, err := s.writeReplacementTasksTx(tx, user, b.Tasks, matches, now)
	if err != nil {
		return nil, err
	}
	if err := setMeta(tx, titleKey(user), b.Title); err != nil {
		return nil, err
	}
	if err := reconcileBoardTombstonesTx(tx, user); err != nil {
		return nil, err
	}
	if err := reconcileCommentsTx(tx, user); err != nil {
		return nil, err
	}
	if err := reconcileLinksTx(tx, user); err != nil {
		return nil, err
	}
	return taskIDs, nil
}

func matchLegacyReplacementTasksTx(tx *sql.Tx, user string, tasks []board.Task) ([]*exTask, error) {
	byStatusTitle, byTitle, err := loadExisting(tx, user)
	if err != nil {
		return nil, err
	}
	matches := make([]*exTask, len(tasks))
	for i, task := range tasks {
		matches[i] = takeUnusedExisting(byStatusTitle[string(task.Status)+"\x00"+task.Title])
	}
	for i, task := range tasks {
		if matches[i] == nil {
			matches[i] = takeUnusedExisting(byTitle[task.Title])
		}
	}
	return matches, nil
}

func takeUnusedExisting(candidates []*exTask) *exTask {
	for _, candidate := range candidates {
		if !candidate.used {
			candidate.used = true
			return candidate
		}
	}
	return nil
}

func (s *Store) writeReplacementTasksTx(tx *sql.Tx, user string, tasks []board.Task, matches []*exTask, now time.Time) ([]string, error) {
	taskIDs := make([]string, len(tasks))
	positions := map[board.Status]int{}
	for i, task := range tasks {
		prepared, err := prepareReplacementTask(task, matches[i], positions, now)
		if err != nil {
			return nil, err
		}
		if prepared.Seq == 0 {
			seq, err := nextSeq(tx, user)
			if err != nil {
				return nil, err
			}
			prepared.Seq = seq
		}
		if err := insertTask(tx, user, prepared); err != nil {
			return nil, err
		}
		if err := s.upsertLabels(tx, user, prepared.Tags); err != nil {
			return nil, err
		}
		taskIDs[i] = prepared.ID
	}
	return taskIDs, nil
}

func prepareReplacementTask(task board.Task, match *exTask, positions map[board.Status]int, now time.Time) (board.Task, error) {
	if !task.Status.Valid() {
		return board.Task{}, fmt.Errorf("store: invalid status %q", task.Status)
	}
	if err := validateTaskLines(task); err != nil {
		return board.Task{}, err
	}
	task.Prio = board.NormalizePrio(task.Prio)
	task.Position = positions[task.Status]
	positions[task.Status]++
	if match == nil {
		// Seq stays 0 here; writeReplacementTasksTx allocates a fresh number
		// for tasks with no preserved identity.
		task.ID, task.CreatedAt, task.MovedAt = uuid.NewString(), now, now
		task.Seq = 0
		return task, nil
	}
	task.ID, task.Seq, task.CreatedAt = match.id, match.seq, match.created
	if match.status == task.Status {
		task.MovedAt = match.moved
	} else {
		task.MovedAt = now
	}
	return task, nil
}

// Full-board replacement is also the restore/purge transaction. Keep a
// reason only when its canonical task still exists and is still cancelled.
func reconcileBoardTombstonesTx(tx *sql.Tx, user string) error {
	if _, err := tx.Exec(`
		DELETE FROM tombstones
		WHERE scope = ? AND task_id NOT IN (
			SELECT id FROM tasks WHERE user = ? AND status = 'cancelled'
		)`, user, user); err != nil {
		return fmt.Errorf("store: reconcile board tombstones: %w", err)
	}
	return nil
}

// exTask is the identity slice of an existing row used by ReplaceBoard
// matching.
type exTask struct {
	id      string
	seq     int
	status  board.Status
	created time.Time
	moved   time.Time
	used    bool
}

// loadExisting reads the identity columns of the user's tasks and indexes
// them by (status, title) and by title.
func loadExisting(tx *sql.Tx, user string) (byStatusTitle, byTitle map[string][]*exTask, err error) {
	rows, err := tx.Query(
		`SELECT id, seq, title, status, created_at, moved_at FROM tasks WHERE user = ? ORDER BY `+statusRank+`, position`,
		user,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("store: load existing tasks: %w", err)
	}
	defer rows.Close()
	byStatusTitle = map[string][]*exTask{}
	byTitle = map[string][]*exTask{}
	for rows.Next() {
		var e exTask
		var title, status, created, moved string
		if err := rows.Scan(&e.id, &e.seq, &title, &status, &created, &moved); err != nil {
			return nil, nil, fmt.Errorf("store: scan existing task: %w", err)
		}
		e.status = board.Status(status)
		if e.created, err = time.Parse(time.RFC3339Nano, created); err != nil {
			return nil, nil, fmt.Errorf("store: task %s created_at: %w", e.id, err)
		}
		if e.moved, err = time.Parse(time.RFC3339Nano, moved); err != nil {
			return nil, nil, fmt.Errorf("store: task %s moved_at: %w", e.id, err)
		}
		p := &e
		byStatusTitle[status+"\x00"+title] = append(byStatusTitle[status+"\x00"+title], p)
		byTitle[title] = append(byTitle[title], p)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("store: load existing tasks: %w", err)
	}
	return byStatusTitle, byTitle, nil
}

// AddTask inserts t for user, assigning a fresh UUID and timestamps and
// appending it to its column. An empty status defaults to todo; a Prio
// outside the three-value scale defaults to 3 (low). Field values the markdown wire cannot
// represent are rejected (see ValidateTaskFields). Labels are upserted from
// t.Tags.
func (s *Store) AddTask(user string, t board.Task) (board.Task, error) {
	var err error
	t, err = prepareNewTask(t)
	if err != nil {
		return board.Task{}, err
	}
	err = s.withTx(func(tx *sql.Tx) error { return s.addTaskTx(tx, user, &t) })
	if err != nil {
		return board.Task{}, err
	}
	return t, nil
}

// AddTaskWithImportLink inserts one task and its provenance in the same
// transaction. A crash or failed provenance write therefore cannot leave an
// imported card that the next preview mistakes for safe work to recreate.
func (s *Store) AddTaskWithImportLink(user string, t board.Task, link ImportLink) (board.Task, error) {
	if err := validateImportLink(link); err != nil {
		return board.Task{}, err
	}
	var err error
	t, err = prepareNewTask(t)
	if err != nil {
		return board.Task{}, err
	}
	err = s.withTx(func(tx *sql.Tx) error {
		if err := s.addTaskTx(tx, user, &t); err != nil {
			return err
		}
		return recordImportLinksTx(tx, user, []ImportLink{link}, time.Now().UTC().Format(time.RFC3339Nano))
	})
	if err != nil {
		return board.Task{}, err
	}
	return t, nil
}

func prepareNewTask(t board.Task) (board.Task, error) {
	if t.Status == "" {
		t.Status = board.StatusTodo
	}
	if !t.Status.Valid() {
		return board.Task{}, fmt.Errorf("store: invalid status %q", t.Status)
	}
	t.Prio = board.NormalizePrio(t.Prio)
	if err := ValidateTaskFields(t); err != nil {
		return board.Task{}, err
	}
	now := time.Now().UTC()
	t.ID = uuid.NewString()
	t.CreatedAt, t.MovedAt = now, now
	return t, nil
}

func (s *Store) addTaskTx(tx *sql.Tx, user string, t *board.Task) error {
	pos, err := nextPosition(tx, user, t.Status)
	if err != nil {
		return err
	}
	t.Position = pos
	seq, err := nextSeq(tx, user)
	if err != nil {
		return err
	}
	t.Seq = seq
	if err := insertTask(tx, user, *t); err != nil {
		return err
	}
	return s.upsertLabels(tx, user, t.Tags)
}

// UpdateTask applies patch to the task matching idPrefix and returns the
// updated task. The merged task must pass ValidateTaskFields. Setting Tags
// upserts labels.
func (s *Store) UpdateTask(user, idPrefix string, patch TaskPatch) (board.Task, error) {
	return s.UpdateAndMoveTask(user, idPrefix, patch, nil, nil, nil)
}

// UpdateTaskIfFieldsMatch applies patch only when every non-nil field in
// expected still matches the stored task. The comparison and patch happen in
// one transaction. Fields omitted from expected remain mergeable, so a
// concurrent update to an unrelated field is preserved.
func (s *Store) UpdateTaskIfFieldsMatch(user, idPrefix string, expected, patch TaskPatch) (board.Task, error) {
	var out board.Task
	err := s.withTx(func(tx *sql.Tx) error {
		id, err := resolveID(tx, user, idPrefix)
		if err != nil {
			return err
		}
		current, err := getTask(tx, user, id)
		if err != nil {
			return err
		}
		if fields := taskFieldConflicts(current, expected); len(fields) > 0 {
			return &TaskFieldsConflictError{Fields: fields}
		}
		out, err = s.patchTask(tx, user, id, patch)
		return err
	})
	if err != nil {
		return board.Task{}, err
	}
	return out, nil
}

// UpdateAndMoveTaskIfFieldsMatch applies patch and move only when every
// non-nil expected field still matches. The comparison, patch, guard, and move
// share one transaction, so a stale modal cannot overwrite a concurrent edit.
func (s *Store) UpdateAndMoveTaskIfFieldsMatch(
	user, idPrefix string,
	expected, patch TaskPatch,
	moveTo *board.Status,
	index *int,
	guard func(board.Task) error,
) (board.Task, error) {
	if moveTo != nil && !moveTo.Valid() {
		return board.Task{}, fmt.Errorf("store: invalid status %q", *moveTo)
	}
	if index != nil && *index < 0 {
		return board.Task{}, fmt.Errorf("store: invalid index %d", *index)
	}
	var out board.Task
	err := s.withTx(func(tx *sql.Tx) error {
		id, err := resolveID(tx, user, idPrefix)
		if err != nil {
			return err
		}
		current, err := getTask(tx, user, id)
		if err != nil {
			return err
		}
		if fields := taskFieldConflicts(current, expected); len(fields) > 0 {
			return &TaskFieldsConflictError{Fields: fields}
		}
		out, err = s.updateAndMoveTaskTx(tx, user, id, patch, moveTo, index, guard)
		return err
	})
	if err != nil {
		return board.Task{}, err
	}
	return out, nil
}

func taskFieldConflicts(task board.Task, expected TaskPatch) []string {
	fields := make([]string, 0, 9)
	if expected.Emoji != nil && task.Emoji != *expected.Emoji {
		fields = append(fields, "emoji")
	}
	if expected.Title != nil && task.Title != *expected.Title {
		fields = append(fields, "title")
	}
	if expected.Desc != nil && task.Desc != *expected.Desc {
		fields = append(fields, "description")
	}
	if expected.Blocked != nil && task.Blocked != *expected.Blocked {
		fields = append(fields, "blocked")
	}
	if expected.Prio != nil && task.Prio != *expected.Prio {
		fields = append(fields, "priority")
	}
	if expected.Due != nil && task.Due != *expected.Due {
		fields = append(fields, "due")
	}
	if expected.Effort != nil && task.Effort != *expected.Effort {
		fields = append(fields, "effort")
	}
	if expected.Tags != nil && !slices.Equal(task.Tags, *expected.Tags) {
		fields = append(fields, "labels")
	}
	if expected.Checks != nil && !slices.Equal(task.Checks, *expected.Checks) {
		fields = append(fields, "checklist")
	}
	return fields
}

// UpdateAndMoveTask applies patch and then, when moveTo is non-nil, moves the
// task to that column — both inside a single transaction.
//
// index, when non-nil, is the target slot within the destination column,
// clamped to [0, column length]; a negative index is an error. With moveTo it
// replaces the default append; without moveTo it reorders the task inside its
// current column and leaves MovedAt alone, matching ReplaceBoard, where a task
// that stays in its status keeps its MovedAt. An index paired with a moveTo
// naming the column the task already occupies is that same reorder, so a client
// that always sends the destination column does not reset MovedAt by dragging a
// card within its column. A patch carrying nothing but an index is a reorder,
// not an empty update.
//
// guard, when non-nil, is called with the post-patch task after the patch is
// written but before the move. The ordering is deliberate: a patch can itself
// be what clears the way for the move (checking off the last checklist item
// while sending the task to done in one call), so the guard must judge the
// state the caller is actually asking for, not the state it started from.
// A non-nil error from guard aborts the transaction, rolling back the patch
// and any repositioning too: a refused move never leaves a partial update
// behind.
func (s *Store) UpdateAndMoveTask(user, idPrefix string, patch TaskPatch, moveTo *board.Status, index *int, guard func(board.Task) error) (board.Task, error) {
	if moveTo != nil && !moveTo.Valid() {
		return board.Task{}, fmt.Errorf("store: invalid status %q", *moveTo)
	}
	if index != nil && *index < 0 {
		return board.Task{}, fmt.Errorf("store: invalid index %d", *index)
	}
	var out board.Task
	err := s.withTx(func(tx *sql.Tx) error {
		var err error
		out, err = s.updateAndMoveTaskTx(tx, user, idPrefix, patch, moveTo, index, guard)
		return err
	})
	if err != nil {
		return board.Task{}, err
	}
	return out, nil
}

func (s *Store) updateAndMoveTaskTx(tx *sql.Tx, user, idPrefix string, patch TaskPatch, moveTo *board.Status, index *int, guard func(board.Task) error) (board.Task, error) {
	id, err := resolveID(tx, user, idPrefix)
	if err != nil {
		return board.Task{}, err
	}
	task, err := s.patchTask(tx, user, id, patch)
	if err != nil {
		return board.Task{}, err
	}
	if guard != nil {
		// A non-nil guard means the caller did not force the move, so the
		// blocker gate applies alongside the caller's checklist/flag guard.
		// Judged inside the transaction, like the rest of the guard, so a
		// refusal persists nothing.
		if moveTo != nil && *moveTo == board.StatusDone {
			open, err := openBlockersTx(tx, user, id)
			if err != nil {
				return board.Task{}, err
			}
			if len(open) > 0 {
				return board.Task{}, &CompletionBlockedError{msg: fmt.Sprintf(
					"%s still blocks %s %q; re-run with --force to finish it anyway",
					describeBlockers(open), displayTaskRef(task), task.Title)}
			}
		}
		if err := guard(task); err != nil {
			return board.Task{}, err
		}
	}
	if moveTo == nil {
		if index == nil {
			return task, nil
		}
		// Same-column reorder: position changes, MovedAt does not.
		pos, err := repositionTask(tx, user, task.Status, task.ID, *index)
		if err != nil {
			return board.Task{}, err
		}
		task.Position = pos
		return task, nil
	}
	from := task.Status
	// A positional move into the column the task already occupies is the same
	// reorder as the moveTo == nil path: repositionTask below does the work and
	// MovedAt survives it.
	if index == nil || *moveTo != from {
		task, err = moveTask(tx, user, task, *moveTo)
		if err != nil {
			return board.Task{}, err
		}
	}
	if index != nil {
		pos, err := repositionTask(tx, user, task.Status, task.ID, *index)
		if err != nil {
			return board.Task{}, err
		}
		task.Position = pos
		if from != task.Status {
			// The task left a hole behind; close it so both columns stay 0..n-1.
			if err := compactColumn(tx, user, from); err != nil {
				return board.Task{}, err
			}
		}
	}
	if *moveTo != board.StatusCancelled {
		if err := deleteRestoredTaskTombstoneTx(tx, user, task.ID); err != nil {
			return board.Task{}, err
		}
	}
	return task, nil
}

func deleteRestoredTaskTombstoneTx(tx *sql.Tx, user, taskID string) error {
	if _, err := tx.Exec(`DELETE FROM tombstones WHERE scope = ? AND task_id = ?`, user, taskID); err != nil {
		return fmt.Errorf("store: delete restored task tombstone: %w", err)
	}
	return nil
}

// patchTask merges patch into the task with id and writes it back, returning
// the post-patch task. An empty patch is a plain read: nothing is written.
func (s *Store) patchTask(tx *sql.Tx, user, id string, patch TaskPatch) (board.Task, error) {
	t, err := getTask(tx, user, id)
	if err != nil {
		return board.Task{}, err
	}
	if patch == (TaskPatch{}) {
		return t, nil
	}
	if patch.Emoji != nil {
		t.Emoji = *patch.Emoji
	}
	if patch.Title != nil {
		t.Title = *patch.Title
	}
	if patch.Desc != nil {
		t.Desc = *patch.Desc
	}
	if patch.Due != nil {
		t.Due = *patch.Due
	}
	if patch.Effort != nil {
		t.Effort = *patch.Effort
	}
	if patch.Blocked != nil {
		t.Blocked = *patch.Blocked
	}
	if patch.Prio != nil {
		if !board.ValidPrio(*patch.Prio) {
			return board.Task{}, fmt.Errorf("store: invalid prio %d (want 1 high, 2 medium, or 3 low)", *patch.Prio)
		}
		t.Prio = *patch.Prio
	}
	if patch.Tags != nil {
		t.Tags = *patch.Tags
	}
	if patch.Checks != nil {
		t.Checks = *patch.Checks
	}
	if err := ValidateTaskFields(t); err != nil {
		return board.Task{}, err
	}
	tags, err := json.Marshal(t.Tags)
	if err != nil {
		return board.Task{}, fmt.Errorf("store: marshal tags: %w", err)
	}
	checks, err := json.Marshal(t.Checks)
	if err != nil {
		return board.Task{}, fmt.Errorf("store: marshal checks: %w", err)
	}
	if _, err := tx.Exec(`UPDATE tasks SET emoji = ?, title = ?, "desc" = ?, blocked = ?, prio = ?, due = ?, effort = ?, tags = ?, checks = ? WHERE user = ? AND id = ?`,
		t.Emoji, t.Title, t.Desc, boolToInt(t.Blocked), t.Prio, t.Due, t.Effort, string(tags), string(checks), user, id); err != nil {
		return board.Task{}, fmt.Errorf("store: update task: %w", err)
	}
	if patch.Tags != nil {
		if err := s.upsertLabels(tx, user, t.Tags); err != nil {
			return board.Task{}, err
		}
	}
	return t, nil
}

// MoveTask moves the task matching idPrefix to status to, appending it to
// that column and stamping MovedAt.
func (s *Store) MoveTask(user, idPrefix string, to board.Status) (board.Task, error) {
	return s.UpdateAndMoveTask(user, idPrefix, TaskPatch{}, &to, nil, nil)
}

// CancelTask soft-deletes a task and optionally records its kill reason in one
// transaction. The status transition happens before the tombstone insert, as
// required by the tombstone invariant, but neither write can escape alone.
func (s *Store) CancelTask(user, idPrefix string, reason *string) (board.Task, error) {
	if reason != nil {
		trimmed := strings.TrimSpace(*reason)
		if err := validateTombstoneReason(trimmed); err != nil {
			return board.Task{}, err
		}
		reason = &trimmed
	}
	var out board.Task
	err := s.withTx(func(tx *sql.Tx) error {
		cancelled := board.StatusCancelled
		var err error
		out, err = s.updateAndMoveTaskTx(tx, user, idPrefix, TaskPatch{}, &cancelled, nil, nil)
		if err != nil {
			return err
		}
		if reason != nil {
			return recordTombstoneTx(tx, user, out.ID, *reason)
		}
		return nil
	})
	if err != nil {
		return board.Task{}, err
	}
	return out, nil
}

// repositionTask splices id into column st at index, clamped to the column
// length, and rewrites that column's positions to 0..n-1. Columns hold a
// handful of tasks, so a full rewrite is cheaper to reason about than sparse
// gaps.
func repositionTask(tx *sql.Tx, user string, st board.Status, id string, index int) (int, error) {
	ids, err := columnTaskIDs(tx, user, st, id)
	if err != nil {
		return 0, err
	}
	if index > len(ids) {
		index = len(ids)
	}
	ordered := make([]string, 0, len(ids)+1)
	ordered = append(ordered, ids[:index]...)
	ordered = append(ordered, id)
	ordered = append(ordered, ids[index:]...)
	if err := writePositions(tx, user, ordered); err != nil {
		return 0, err
	}
	return index, nil
}

// compactColumn rewrites column st's positions to 0..n-1.
func compactColumn(tx *sql.Tx, user string, st board.Status) error {
	ids, err := columnTaskIDs(tx, user, st, "")
	if err != nil {
		return err
	}
	return writePositions(tx, user, ids)
}

// columnTaskIDs lists a column's task ids in position order, skipping exclude.
func columnTaskIDs(q dbtx, user string, st board.Status, exclude string) ([]string, error) {
	rows, err := q.Query(`SELECT id FROM tasks WHERE user = ? AND status = ? ORDER BY position, seq`, user, string(st))
	if err != nil {
		return nil, fmt.Errorf("store: list column: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("store: scan column: %w", err)
		}
		if id == exclude {
			continue
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list column: %w", err)
	}
	return ids, nil
}

// writePositions stamps ids with positions 0..len(ids)-1.
func writePositions(tx *sql.Tx, user string, ids []string) error {
	for i, id := range ids {
		if _, err := tx.Exec(`UPDATE tasks SET position = ? WHERE user = ? AND id = ?`, i, user, id); err != nil {
			return fmt.Errorf("store: set position: %w", err)
		}
	}
	return nil
}

// moveTask appends t to column to and stamps MovedAt, returning the moved
// task.
func moveTask(tx *sql.Tx, user string, t board.Task, to board.Status) (board.Task, error) {
	pos, err := nextPosition(tx, user, to)
	if err != nil {
		return board.Task{}, err
	}
	now := time.Now().UTC()
	if _, err := tx.Exec(`UPDATE tasks SET status = ?, position = ?, moved_at = ? WHERE user = ? AND id = ?`,
		string(to), pos, now.Format(time.RFC3339Nano), user, t.ID); err != nil {
		return board.Task{}, fmt.Errorf("store: move task: %w", err)
	}
	t.Status, t.Position, t.MovedAt = to, pos, now
	return t, nil
}

// DeleteTask removes the task matching idPrefix and returns it.
func (s *Store) DeleteTask(user, idPrefix string) (board.Task, error) {
	return s.deleteTask(user, idPrefix, false)
}

// DeleteCancelledTask permanently deletes a task only while it is in the
// Cancelled column. This is the direct-store hard-delete seam used by the TUI:
// checking the status and removing the row happen in one transaction, so a
// concurrent restore cannot race an already-confirmed purge.
func (s *Store) DeleteCancelledTask(user, idPrefix string) (board.Task, error) {
	return s.deleteTask(user, idPrefix, true)
}

func (s *Store) deleteTask(user, idPrefix string, requireCancelled bool) (board.Task, error) {
	var out board.Task
	err := s.withTx(func(tx *sql.Tx) error {
		id, err := resolveID(tx, user, idPrefix)
		if err != nil {
			return err
		}
		t, err := getTask(tx, user, id)
		if err != nil {
			return err
		}
		if requireCancelled && t.Status != board.StatusCancelled {
			return ErrTaskNotCancelled
		}
		if _, err := tx.Exec(`DELETE FROM tasks WHERE user = ? AND id = ?`, user, id); err != nil {
			return fmt.Errorf("store: delete task: %w", err)
		}
		if _, err := tx.Exec(`DELETE FROM tombstones WHERE scope = ? AND task_id = ?`, user, id); err != nil {
			return fmt.Errorf("store: delete task tombstone: %w", err)
		}
		if _, err := tx.Exec(`DELETE FROM comments WHERE scope = ? AND task_id = ?`, user, id); err != nil {
			return fmt.Errorf("store: delete task comments: %w", err)
		}
		if _, err := tx.Exec(`DELETE FROM task_links WHERE scope = ? AND (blocker_id = ? OR blocked_id = ?)`, user, id, id); err != nil {
			return fmt.Errorf("store: delete task links: %w", err)
		}
		out = t
		return nil
	})
	if err != nil {
		return board.Task{}, err
	}
	return out, nil
}

// ListTasks returns the user's tasks in status then position order; an empty
// status means all statuses.
func (s *Store) ListTasks(user string, status board.Status) ([]board.Task, error) {
	return s.FilterTasks(user, TaskFilter{Status: status})
}

// TaskFilter narrows a task listing. The zero value lists every task.
type TaskFilter struct {
	// Status keeps one column only.
	Status board.Status
	// Search is free text matched against title, description, and tags via
	// the FTS index: every word must appear, the last one as a prefix.
	Search string
	// Tags are exact label matches; a task must carry every one (AND).
	Tags []string
}

const maxSearchBytes = 500

// FilterTasks lists the user's tasks matching every condition in f, in board
// order (columns in status order, tasks by position).
func (s *Store) FilterTasks(user string, f TaskFilter) ([]board.Task, error) {
	query := `SELECT ` + taskCols + ` FROM tasks WHERE user = ?`
	args := []any{user}
	if f.Status != "" {
		if !f.Status.Valid() {
			return nil, fmt.Errorf("store: invalid status %q", f.Status)
		}
		query += ` AND status = ?`
		args = append(args, string(f.Status))
	}
	if len(f.Search) > maxSearchBytes {
		return nil, errors.New("store: search query too long")
	}
	if match := ftsSearchQuery(f.Search); match != "" {
		query += ` AND id IN (SELECT id FROM tasks_fts WHERE tasks_fts MATCH ? AND scope = ?)`
		args = append(args, match, user)
	}
	for _, tag := range f.Tags {
		if strings.TrimSpace(tag) == "" {
			return nil, errors.New("store: tag filter must not be empty")
		}
		query += ` AND EXISTS (SELECT 1 FROM json_each(tasks.tags) WHERE json_each.value = ?)`
		args = append(args, tag)
	}
	if f.Status != "" {
		query += ` ORDER BY position`
	} else {
		query += ` ORDER BY ` + statusRank + `, position`
	}
	return queryTasks(s.db, query, args...)
}

// Labels returns the user's distinct labels, most recently used first.
func (s *Store) Labels(user string) ([]string, error) {
	rows, err := s.db.Query(`SELECT label FROM labels WHERE user = ? ORDER BY last_used DESC, label`, user)
	if err != nil {
		return nil, fmt.Errorf("store: labels: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			return nil, fmt.Errorf("store: scan label: %w", err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: labels: %w", err)
	}
	return out, nil
}

// upsertLabels records tags as labels; later tags in the slice count as more
// recently used. Sequence allocation happens in the caller's transaction, so
// two Store handles cannot reuse or reorder process-local counter values.
func (s *Store) upsertLabels(q dbtx, user string, tags []string) error {
	for _, tag := range tags {
		if tag == "" {
			continue
		}
		if _, err := q.Exec(`UPDATE label_sequence SET value = value + 1 WHERE id = 1`); err != nil {
			return fmt.Errorf("store: advance label sequence: %w", err)
		}
		var sequence int64
		if err := q.QueryRow(`SELECT value FROM label_sequence WHERE id = 1`).Scan(&sequence); err != nil {
			return fmt.Errorf("store: read label sequence: %w", err)
		}
		if _, err := q.Exec(`INSERT INTO labels (user, label, last_used) VALUES (?, ?, ?)
			ON CONFLICT(user, label) DO UPDATE SET last_used = excluded.last_used`,
			user, tag, sequence); err != nil {
			return fmt.Errorf("store: upsert label %q: %w", tag, err)
		}
	}
	return nil
}

// resolveID resolves a task reference to the full task ID for user. A
// digits-only reference (with or without a leading '#') is a stable sequence
// number and never falls through to UUID-prefix matching — an all-digit hex
// prefix is reachable only via a longer, non-ambiguous form. Otherwise an
// exact match always wins and the prefix must match exactly one task.
func resolveID(q dbtx, user, prefix string) (string, error) {
	if prefix == "" {
		return "", ErrNotFound
	}
	if seq, ok := parseSeqRef(prefix); ok {
		var id string
		switch err := q.QueryRow(`SELECT id FROM tasks WHERE user = ? AND seq = ?`, user, seq).Scan(&id); {
		case errors.Is(err, sql.ErrNoRows):
			return "", ErrNotFound
		case err != nil:
			return "", fmt.Errorf("store: resolve seq: %w", err)
		}
		return id, nil
	}
	rows, err := q.Query(`SELECT id FROM tasks WHERE user = ?`, user)
	if err != nil {
		return "", fmt.Errorf("store: resolve id: %w", err)
	}
	defer rows.Close()
	match, n := "", 0
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", fmt.Errorf("store: resolve id: %w", err)
		}
		if id == prefix {
			return id, nil
		}
		if strings.HasPrefix(id, prefix) {
			match = id
			n++
		}
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("store: resolve id: %w", err)
	}
	switch n {
	case 0:
		return "", ErrNotFound
	case 1:
		return match, nil
	}
	return "", ErrAmbiguous
}

// parseSeqRef reports whether ref addresses a task by stable sequence
// number: all digits, optionally after one leading '#'.
func parseSeqRef(ref string) (int, bool) {
	ref = strings.TrimPrefix(ref, "#")
	if ref == "" {
		return 0, false
	}
	for _, r := range ref {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(ref)
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

// getTask fetches one task by exact ID.
func getTask(q dbtx, user, id string) (board.Task, error) {
	return scanTask(q.QueryRow(`SELECT `+taskCols+` FROM tasks WHERE user = ? AND id = ?`, user, id))
}

// nextPosition returns the append position for the user's status column.
func nextPosition(q dbtx, user string, st board.Status) (int, error) {
	var n sql.NullInt64
	if err := q.QueryRow(`SELECT MAX(position) FROM tasks WHERE user = ? AND status = ?`, user, string(st)).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: next position: %w", err)
	}
	if !n.Valid {
		return 0, nil
	}
	return int(n.Int64) + 1, nil
}

// boolToInt renders a Go bool as the 0/1 SQLite stores in an INTEGER column.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// insertTask inserts one fully populated task row.
func insertTask(q dbtx, user string, t board.Task) error {
	tags, err := json.Marshal(t.Tags)
	if err != nil {
		return fmt.Errorf("store: marshal tags: %w", err)
	}
	checks, err := json.Marshal(t.Checks)
	if err != nil {
		return fmt.Errorf("store: marshal checks: %w", err)
	}
	if _, err := q.Exec(`INSERT INTO tasks (id, seq, user, emoji, title, "desc", status, blocked, prio, due, effort, tags, checks, position, created_at, moved_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Seq, user, t.Emoji, t.Title, t.Desc, string(t.Status), boolToInt(t.Blocked), t.Prio, t.Due, t.Effort, string(tags), string(checks), t.Position,
		t.CreatedAt.UTC().Format(time.RFC3339Nano), t.MovedAt.UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("store: insert task %s: %w", t.ID, err)
	}
	return nil
}

// nextSeq allocates the next per-board sequence number for user inside the
// caller's transaction. The counter only ever advances, so a number is never
// reused — deleting the newest task does not resurrect its #n.
func nextSeq(q dbtx, user string) (int, error) {
	if _, err := q.Exec(`INSERT INTO board_sequences(user, next) VALUES (?, 2)
		ON CONFLICT(user) DO UPDATE SET next = next + 1`, user); err != nil {
		return 0, fmt.Errorf("store: advance board sequence: %w", err)
	}
	var next int
	if err := q.QueryRow(`SELECT next FROM board_sequences WHERE user = ?`, user).Scan(&next); err != nil {
		return 0, fmt.Errorf("store: read board sequence: %w", err)
	}
	return next - 1, nil
}

// scanTask decodes one row produced with taskCols.
func scanTask(row interface{ Scan(dest ...any) error }) (board.Task, error) {
	var t board.Task
	var status, tags, checks, created, moved string
	var blocked int
	if err := row.Scan(&t.ID, &t.Seq, &t.Emoji, &t.Title, &t.Desc, &status, &blocked, &t.Prio, &t.Due, &t.Effort, &tags, &checks, &t.Position, &created, &moved); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return board.Task{}, ErrNotFound
		}
		return board.Task{}, fmt.Errorf("store: scan task: %w", err)
	}
	t.Status = board.Status(status)
	t.Blocked = blocked != 0
	if err := json.Unmarshal([]byte(tags), &t.Tags); err != nil {
		return board.Task{}, fmt.Errorf("store: task %s tags: %w", t.ID, err)
	}
	if err := json.Unmarshal([]byte(checks), &t.Checks); err != nil {
		return board.Task{}, fmt.Errorf("store: task %s checks: %w", t.ID, err)
	}
	var err error
	if t.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return board.Task{}, fmt.Errorf("store: task %s created_at: %w", t.ID, err)
	}
	if t.MovedAt, err = time.Parse(time.RFC3339Nano, moved); err != nil {
		return board.Task{}, fmt.Errorf("store: task %s moved_at: %w", t.ID, err)
	}
	return t, nil
}

// queryTasks runs a taskCols query and decodes all rows.
func queryTasks(q dbtx, query string, args ...any) ([]board.Task, error) {
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: query tasks: %w", err)
	}
	defer rows.Close()
	var out []board.Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: query tasks: %w", err)
	}
	return out, nil
}

// setMeta upserts one meta key.
func setMeta(q dbtx, k, v string) error {
	if _, err := q.Exec(`INSERT INTO meta (k, v) VALUES (?, ?) ON CONFLICT(k) DO UPDATE SET v = excluded.v`, k, v); err != nil {
		return fmt.Errorf("store: set meta %s: %w", k, err)
	}
	return nil
}
