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
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite" // database/sql driver "sqlite"

	"github.com/RandomCodeSpace/kb/internal/board"
)

// Sentinel errors for task ID prefix resolution.
var (
	ErrNotFound  = errors.New("task not found")
	ErrAmbiguous = errors.New("ambiguous task id prefix")
)

// dueRe matches the wire-format due date (calendar validity checked
// separately).
var dueRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// validateTaskLines rejects CR/LF in every field board.Serialize writes on a
// single line. Without this a title such as "x\n- [x] forged !1" would
// serialize as an extra board line and re-parse as a different task. Phase 1
// held the invariant implicitly because every write went through
// board.Parse; store-level writers (MCP, CLI, HTTP) must enforce it here.
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

// ValidateTaskFields enforces, for the direct task writers (AddTask,
// UpdateTask, and the remote-mode CLI and MCP paths that build the wire
// themselves), the field formats a task must satisfy to survive the markdown
// wire unchanged: a non-blank title, a real YYYY-MM-DD due date, S/M/L
// effort, single-token tags without a leading '#', and a single-emoji Emoji.
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
	db       *sql.DB
	aead     cipher.AEAD
	labelSeq atomic.Int64 // monotonically increasing labels.last_used values
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
	// temp_store(2) keeps sorters and temp tables in memory. The default lets
	// SQLite spill them to $TMPDIR, which would put board text outside the
	// data directory — kb promises that nothing does.
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=temp_store(2)")
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := repairAIBaseURLSuffixes(db); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{db: db, aead: aead}
	var maxSeq sql.NullInt64
	if err := db.QueryRow(`SELECT MAX(last_used) FROM labels`).Scan(&maxSeq); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: init label sequence: %w", err)
	}
	s.labelSeq.Store(maxSeq.Int64)
	return s, nil
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
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	return nil
}

// taskCols is the canonical select list matched by scanTask.
const taskCols = `id, emoji, title, "desc", status, blocked, prio, due, effort, tags, checks, position, created_at, moved_at`

// statusRank orders rows by board column order (todo, doing, done,
// cancelled).
const statusRank = `CASE status WHEN 'todo' THEN 0 WHEN 'doing' THEN 1 WHEN 'done' THEN 2 WHEN 'cancelled' THEN 3 ELSE 4 END`

func titleKey(user string) string { return "board_title:" + user }

// Board returns the user's board with tasks ordered by status column order
// then position. The title defaults to "Board" when none has been stored.
func (s *Store) Board(user string) (board.Board, error) {
	b := board.Board{Title: "Board"}
	var title string
	switch err := s.db.QueryRow(`SELECT v FROM meta WHERE k = ?`, titleKey(user)).Scan(&title); {
	case err == nil:
		b.Title = title
	case !errors.Is(err, sql.ErrNoRows):
		return board.Board{}, fmt.Errorf("store: board title: %w", err)
	}
	tasks, err := queryTasks(s.db, `SELECT `+taskCols+` FROM tasks WHERE user = ? ORDER BY `+statusRank+`, position`, user)
	if err != nil {
		return board.Board{}, err
	}
	b.Tasks = tasks
	return b, nil
}

// HasBoard reports whether the user has any tasks or has ever had a board
// saved (a stored title, written by ReplaceBoard and the markdown import).
func (s *Store) HasBoard(user string) (bool, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE user = ?`, user).Scan(&n); err != nil {
		return false, fmt.Errorf("store: has board: %w", err)
	}
	if n > 0 {
		return true, nil
	}
	var v string
	switch err := s.db.QueryRow(`SELECT v FROM meta WHERE k = ?`, titleKey(user)).Scan(&v); {
	case err == nil:
		return true, nil
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	default:
		return false, fmt.Errorf("store: has board: %w", err)
	}
}

// ReplaceBoard replaces the user's whole board (the SPA PUT path). Incoming
// tasks are matched against existing ones first by (Status, Title), then by
// Title alone; matches keep their ID and CreatedAt, and a status change
// stamps MovedAt. Unmatched incoming tasks get fresh UUIDs; existing tasks
// absent from b are deleted. Positions are recomputed from slice order per
// status and labels are upserted from all task tags.
func (s *Store) ReplaceBoard(user string, b board.Board) error {
	now := time.Now().UTC()
	return s.withTx(func(tx *sql.Tx) error {
		byStatusTitle, byTitle, err := loadExisting(tx, user)
		if err != nil {
			return err
		}
		matches := make([]*exTask, len(b.Tasks))
		for i, t := range b.Tasks {
			for _, e := range byStatusTitle[string(t.Status)+"\x00"+t.Title] {
				if !e.used {
					e.used, matches[i] = true, e
					break
				}
			}
		}
		for i, t := range b.Tasks {
			if matches[i] != nil {
				continue
			}
			for _, e := range byTitle[t.Title] {
				if !e.used {
					e.used, matches[i] = true, e
					break
				}
			}
		}
		if _, err := tx.Exec(`DELETE FROM tasks WHERE user = ?`, user); err != nil {
			return fmt.Errorf("store: clear board: %w", err)
		}
		pos := map[board.Status]int{}
		for i, t := range b.Tasks {
			if !t.Status.Valid() {
				return fmt.Errorf("store: invalid status %q", t.Status)
			}
			if err := validateTaskLines(t); err != nil {
				return err
			}
			if t.Prio < 1 || t.Prio > 4 {
				t.Prio = 3
			}
			t.Position = pos[t.Status]
			pos[t.Status]++
			if e := matches[i]; e != nil {
				t.ID, t.CreatedAt = e.id, e.created
				if e.status == t.Status {
					t.MovedAt = e.moved
				} else {
					t.MovedAt = now
				}
			} else {
				t.ID, t.CreatedAt, t.MovedAt = uuid.NewString(), now, now
			}
			if err := insertTask(tx, user, t); err != nil {
				return err
			}
			if err := s.upsertLabels(tx, user, t.Tags); err != nil {
				return err
			}
		}
		return setMeta(tx, titleKey(user), b.Title)
	})
}

// exTask is the identity slice of an existing row used by ReplaceBoard
// matching.
type exTask struct {
	id      string
	status  board.Status
	created time.Time
	moved   time.Time
	used    bool
}

// loadExisting reads the identity columns of the user's tasks and indexes
// them by (status, title) and by title.
func loadExisting(tx *sql.Tx, user string) (byStatusTitle, byTitle map[string][]*exTask, err error) {
	rows, err := tx.Query(`SELECT id, title, status, created_at, moved_at FROM tasks WHERE user = ?`, user)
	if err != nil {
		return nil, nil, fmt.Errorf("store: load existing tasks: %w", err)
	}
	defer rows.Close()
	byStatusTitle = map[string][]*exTask{}
	byTitle = map[string][]*exTask{}
	for rows.Next() {
		var e exTask
		var title, status, created, moved string
		if err := rows.Scan(&e.id, &title, &status, &created, &moved); err != nil {
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
// appending it to its column. An empty status defaults to todo; an
// out-of-range Prio defaults to 3. Field values the markdown wire cannot
// represent are rejected (see ValidateTaskFields). Labels are upserted from
// t.Tags.
func (s *Store) AddTask(user string, t board.Task) (board.Task, error) {
	if t.Status == "" {
		t.Status = board.StatusTodo
	}
	if !t.Status.Valid() {
		return board.Task{}, fmt.Errorf("store: invalid status %q", t.Status)
	}
	if t.Prio < 1 || t.Prio > 4 {
		t.Prio = 3
	}
	if err := ValidateTaskFields(t); err != nil {
		return board.Task{}, err
	}
	now := time.Now().UTC()
	t.ID = uuid.NewString()
	t.CreatedAt, t.MovedAt = now, now
	err := s.withTx(func(tx *sql.Tx) error {
		pos, err := nextPosition(tx, user, t.Status)
		if err != nil {
			return err
		}
		t.Position = pos
		if err := insertTask(tx, user, t); err != nil {
			return err
		}
		return s.upsertLabels(tx, user, t.Tags)
	})
	if err != nil {
		return board.Task{}, err
	}
	return t, nil
}

// UpdateTask applies patch to the task matching idPrefix and returns the
// updated task. The merged task must pass ValidateTaskFields. Setting Tags
// upserts labels.
func (s *Store) UpdateTask(user, idPrefix string, patch TaskPatch) (board.Task, error) {
	return s.UpdateAndMoveTask(user, idPrefix, patch, nil, nil)
}

// UpdateAndMoveTask applies patch and then, when moveTo is non-nil, moves the
// task to that column — both inside a single transaction.
//
// guard, when non-nil, is called with the post-patch task after the patch is
// written but before the move. The ordering is deliberate: a patch can itself
// be what clears the way for the move (checking off the last checklist item
// while sending the task to done in one call), so the guard must judge the
// state the caller is actually asking for, not the state it started from.
// A non-nil error from guard aborts the transaction, rolling back the patch
// too: a refused move never leaves a partial update behind.
func (s *Store) UpdateAndMoveTask(user, idPrefix string, patch TaskPatch, moveTo *board.Status, guard func(board.Task) error) (board.Task, error) {
	if moveTo != nil && !moveTo.Valid() {
		return board.Task{}, fmt.Errorf("store: invalid status %q", *moveTo)
	}
	var out board.Task
	err := s.withTx(func(tx *sql.Tx) error {
		id, err := resolveID(tx, user, idPrefix)
		if err != nil {
			return err
		}
		t, err := s.patchTask(tx, user, id, patch)
		if err != nil {
			return err
		}
		if guard != nil {
			if err := guard(t); err != nil {
				return err
			}
		}
		if moveTo != nil {
			if t, err = moveTask(tx, user, t, *moveTo); err != nil {
				return err
			}
		}
		out = t
		return nil
	})
	if err != nil {
		return board.Task{}, err
	}
	return out, nil
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
		if *patch.Prio < 1 || *patch.Prio > 4 {
			return board.Task{}, fmt.Errorf("store: invalid prio %d", *patch.Prio)
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
	return s.UpdateAndMoveTask(user, idPrefix, TaskPatch{}, &to, nil)
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
		if _, err := tx.Exec(`DELETE FROM tasks WHERE user = ? AND id = ?`, user, id); err != nil {
			return fmt.Errorf("store: delete task: %w", err)
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
	if status == "" {
		return queryTasks(s.db, `SELECT `+taskCols+` FROM tasks WHERE user = ? ORDER BY `+statusRank+`, position`, user)
	}
	if !status.Valid() {
		return nil, fmt.Errorf("store: invalid status %q", status)
	}
	return queryTasks(s.db, `SELECT `+taskCols+` FROM tasks WHERE user = ? AND status = ? ORDER BY position`, user, string(status))
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
// recently used. The store-wide sequence keeps MRU ordering strict even
// within one batch.
func (s *Store) upsertLabels(q dbtx, user string, tags []string) error {
	for _, tag := range tags {
		if tag == "" {
			continue
		}
		if _, err := q.Exec(`INSERT INTO labels (user, label, last_used) VALUES (?, ?, ?)
			ON CONFLICT(user, label) DO UPDATE SET last_used = excluded.last_used`,
			user, tag, s.labelSeq.Add(1)); err != nil {
			return fmt.Errorf("store: upsert label %q: %w", tag, err)
		}
	}
	return nil
}

// resolveID resolves an ID prefix to the full task ID for user. An exact
// match always wins; otherwise the prefix must match exactly one task.
func resolveID(q dbtx, user, prefix string) (string, error) {
	if prefix == "" {
		return "", ErrNotFound
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
	if _, err := q.Exec(`INSERT INTO tasks (id, user, emoji, title, "desc", status, blocked, prio, due, effort, tags, checks, position, created_at, moved_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, user, t.Emoji, t.Title, t.Desc, string(t.Status), boolToInt(t.Blocked), t.Prio, t.Due, t.Effort, string(tags), string(checks), t.Position,
		t.CreatedAt.UTC().Format(time.RFC3339Nano), t.MovedAt.UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("store: insert task %s: %w", t.ID, err)
	}
	return nil
}

// scanTask decodes one row produced with taskCols.
func scanTask(row interface{ Scan(dest ...any) error }) (board.Task, error) {
	var t board.Task
	var status, tags, checks, created, moved string
	var blocked int
	if err := row.Scan(&t.ID, &t.Emoji, &t.Title, &t.Desc, &status, &blocked, &t.Prio, &t.Due, &t.Effort, &tags, &checks, &t.Position, &created, &moved); err != nil {
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
