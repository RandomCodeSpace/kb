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
	"strings"
	"time"

	"github.com/google/uuid"
	sqliteDriver "modernc.org/sqlite" // database/sql driver "sqlite"

	"github.com/RandomCodeSpace/kb/internal/board"
)

// Sentinel errors for task ID prefix resolution.
var (
	ErrNotFound       = errors.New("task not found")
	ErrAmbiguous      = errors.New("ambiguous task id prefix")
	ErrInvalidTaskIDs = errors.New("invalid canonical task ids")
)

// RevisionConflictError reports a failed board compare-and-swap. Revisions
// are opaque monotonic tokens; callers should return CurrentRevision and make
// the client refetch rather than infer a missing count.
type RevisionConflictError struct {
	CurrentRevision int64
}

func (e *RevisionConflictError) Error() string {
	return fmt.Sprintf("store: board revision conflict (current %d)", e.CurrentRevision)
}

// BoardSnapshot is one transactionally consistent view of a user's board.
type BoardSnapshot struct {
	Board    board.Board
	Exists   bool
	TaskIDs  []string
	Revision int64
}

// BoardWriteReceipt is the durable acknowledgement of one creation-bearing
// JSON board replacement. RequestHash is a digest of the exact accepted body.
type BoardWriteReceipt struct {
	RequestHash string
	TaskIDs     []string
	Revision    int64
}

// BoardWriteCondition is the store-owned form of an HTTP If-Match predicate.
// When Present is false the replacement is unconditional. Star requires an
// existing board; otherwise any revision in Revisions satisfies the predicate.
type BoardWriteCondition struct {
	Present   bool
	Star      bool
	Revisions []int64
}

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
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=temp_store(2)")
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
const taskCols = `id, emoji, title, "desc", status, blocked, prio, due, effort, tags, checks, position, created_at, moved_at`

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

// DeleteBoard removes the board while preserving its monotonic revision.
func (s *Store) DeleteBoard(user string) error {
	return s.withTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM tasks WHERE user = ?`, user); err != nil {
			return fmt.Errorf("store: delete board tasks: %w", err)
		}
		if _, err := tx.Exec(`DELETE FROM meta WHERE k = ?`, titleKey(user)); err != nil {
			return fmt.Errorf("store: delete board title: %w", err)
		}
		return nil
	})
}

// ReplaceBoard replaces the user's whole board and discards the committed
// task IDs. See ReplaceBoardWithTaskIDs for replacement semantics.
func (s *Store) ReplaceBoard(user string, b board.Board) error {
	_, err := s.ReplaceBoardWithTaskIDs(user, b)
	return err
}

// ReplaceBoardWithTaskIDs replaces the user's whole board (the SPA PUT path)
// and returns each committed task ID in b.Tasks order. Incoming tasks are
// matched against existing ones first by (Status, Title), then by Title alone;
// matches keep their ID and CreatedAt, and a status change stamps MovedAt.
// Unmatched incoming tasks get fresh UUIDs; existing tasks absent from b are
// deleted. Positions are recomputed from slice order per status and labels are
// upserted from all task tags.
func (s *Store) ReplaceBoardWithTaskIDs(user string, b board.Board) ([]string, error) {
	taskIDs, _, err := s.ReplaceBoardWithTaskIDsAndRevision(user, b)
	return taskIDs, err
}

// ReplaceBoardWithTaskIDsAndRevision performs the same unconditional legacy
// replacement and returns the revision committed by that exact transaction.
// Callers must not pair the returned IDs with a revision read afterwards:
// another process may replace the board between those two observations.
func (s *Store) ReplaceBoardWithTaskIDsAndRevision(user string, b board.Board) ([]string, int64, error) {
	var taskIDs []string
	var revision int64
	err := s.withTx(func(tx *sql.Tx) error {
		var err error
		taskIDs, err = s.replaceBoardTx(tx, user, b, nil)
		if err != nil {
			return err
		}
		if err := tx.QueryRow(`SELECT revision FROM board_revisions WHERE user = ?`, user).Scan(&revision); err != nil {
			return fmt.Errorf("store: committed board revision: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	return taskIDs, revision, nil
}

// ReplaceBoardIfRevision atomically replaces a board only when expected is
// still current. canonicalIDs is in b.Tasks order: nil entries create tasks;
// non-nil entries must be full UUIDs owned by user. A nil slice selects the
// legacy title-matching behavior. Revision mismatch is checked before any ID
// validation and returns *RevisionConflictError.
func (s *Store) ReplaceBoardIfRevision(user string, b board.Board, canonicalIDs []*string, expected int64) ([]string, int64, error) {
	return s.replaceBoardConditional(user, b, canonicalIDs, BoardWriteCondition{Present: true, Revisions: []int64{expected}})
}

// ReplaceBoardIfExists atomically evaluates the wildcard existence predicate
// and performs the replacement in the same transaction. A concurrent edit is
// allowed; a concurrent deletion returns a revision conflict.
func (s *Store) ReplaceBoardIfExists(user string, b board.Board, canonicalIDs []*string) ([]string, int64, error) {
	return s.replaceBoardConditional(user, b, canonicalIDs, BoardWriteCondition{Present: true, Star: true})
}

// ReplaceBoardIfRevisionWithReceipt is ReplaceBoardIfRevision plus a durable
// idempotency receipt inserted in the replacement transaction. An exact
// operation/hash replay returns the original acknowledgement without writing.
func (s *Store) ReplaceBoardIfRevisionWithReceipt(user string, b board.Board, canonicalIDs []*string, expected int64, operationID, requestHash string) ([]string, int64, bool, error) {
	return s.ReplaceBoardConditionalWithReceipt(user, b, canonicalIDs, BoardWriteCondition{Present: true, Revisions: []int64{expected}}, operationID, requestHash, true)
}

// ReplaceBoardIfExistsWithReceipt is the wildcard counterpart of
// ReplaceBoardIfRevisionWithReceipt.
func (s *Store) ReplaceBoardIfExistsWithReceipt(user string, b board.Board, canonicalIDs []*string, operationID, requestHash string) ([]string, int64, bool, error) {
	return s.ReplaceBoardConditionalWithReceipt(user, b, canonicalIDs, BoardWriteCondition{Present: true, Star: true}, operationID, requestHash, true)
}

// ReplaceBoardConditionalWithReceipt evaluates receipt replay, request-hash
// identity, If-Match, replacement, and receipt insertion in one transaction.
// allowNewReceipt must only be true for a parsed creation-bearing JSON write.
func (s *Store) ReplaceBoardConditionalWithReceipt(user string, b board.Board, canonicalIDs []*string, condition BoardWriteCondition, operationID, requestHash string, allowNewReceipt bool) ([]string, int64, bool, error) {
	return s.replaceBoardConditionalReceipt(user, b, canonicalIDs, condition, operationID, requestHash, allowNewReceipt)
}

// CheckBoardWriteCondition preserves conditional-response precedence for an
// invalid payload. It never authorizes a later write; valid writes re-evaluate
// the same predicate inside their replacement transaction.
func (s *Store) CheckBoardWriteCondition(user string, condition BoardWriteCondition) (int64, error) {
	var revision int64
	err := s.withTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO board_revisions(user, revision) VALUES (?, 0) ON CONFLICT(user) DO NOTHING`, user); err != nil {
			return fmt.Errorf("store: initialize board revision: %w", err)
		}
		if err := tx.QueryRow(`SELECT revision FROM board_revisions WHERE user = ?`, user).Scan(&revision); err != nil {
			return fmt.Errorf("store: current board revision: %w", err)
		}
		if condition.Star {
			var exists int
			if err := tx.QueryRow(`SELECT CASE WHEN EXISTS(SELECT 1 FROM tasks WHERE user = ?) OR EXISTS(SELECT 1 FROM meta WHERE k = ?) THEN 1 ELSE 0 END`, user, titleKey(user)).Scan(&exists); err != nil {
				return fmt.Errorf("store: inspect board existence: %w", err)
			}
			if exists == 0 {
				return &RevisionConflictError{CurrentRevision: revision}
			}
			return nil
		}
		if condition.Present {
			for _, allowed := range condition.Revisions {
				if revision == allowed {
					return nil
				}
			}
			return &RevisionConflictError{CurrentRevision: revision}
		}
		return nil
	})
	return revision, err
}

// BoardWriteReceipt returns a user's receipt. Operation IDs are scoped by the
// user column, so another identity cannot observe or replay it.
func (s *Store) BoardWriteReceipt(user, operationID string) (BoardWriteReceipt, bool, error) {
	var receipt BoardWriteReceipt
	var encoded string
	err := s.db.QueryRow(`SELECT request_hash, task_ids, revision FROM board_write_receipts WHERE user = ? AND operation_id = ?`, user, operationID).
		Scan(&receipt.RequestHash, &encoded, &receipt.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return BoardWriteReceipt{}, false, nil
	}
	if err != nil {
		return BoardWriteReceipt{}, false, fmt.Errorf("store: read board write receipt: %w", err)
	}
	if err := json.Unmarshal([]byte(encoded), &receipt.TaskIDs); err != nil {
		return BoardWriteReceipt{}, false, fmt.Errorf("store: decode board write receipt: %w", err)
	}
	return receipt, true, nil
}

func (s *Store) replaceBoardConditional(user string, b board.Board, canonicalIDs []*string, condition BoardWriteCondition) ([]string, int64, error) {
	ids, revision, _, err := s.replaceBoardConditionalReceipt(user, b, canonicalIDs, condition, "", "", false)
	return ids, revision, err
}

func (s *Store) replaceBoardConditionalReceipt(user string, b board.Board, canonicalIDs []*string, condition BoardWriteCondition, operationID, requestHash string, allowNewReceipt bool) ([]string, int64, bool, error) {
	var taskIDs []string
	var revision int64
	var replayed bool
	err := s.withTx(func(tx *sql.Tx) error {
		if operationID != "" {
			var storedHash, encoded string
			err := tx.QueryRow(`SELECT request_hash, task_ids, revision FROM board_write_receipts WHERE user = ? AND operation_id = ?`, user, operationID).
				Scan(&storedHash, &encoded, &revision)
			switch {
			case err == nil:
				if storedHash != requestHash || json.Unmarshal([]byte(encoded), &taskIDs) != nil {
					return ErrInvalidTaskIDs
				}
				replayed = true
				return nil
			case !errors.Is(err, sql.ErrNoRows):
				return fmt.Errorf("store: read board write receipt: %w", err)
			}
		}
		if _, err := tx.Exec(`INSERT INTO board_revisions(user, revision) VALUES (?, 0) ON CONFLICT(user) DO NOTHING`, user); err != nil {
			return fmt.Errorf("store: initialize board revision: %w", err)
		}
		if condition.Star {
			var exists int
			if err := tx.QueryRow(`SELECT CASE WHEN EXISTS(SELECT 1 FROM tasks WHERE user = ?) OR EXISTS(SELECT 1 FROM meta WHERE k = ?) THEN 1 ELSE 0 END`, user, titleKey(user)).Scan(&exists); err != nil {
				return fmt.Errorf("store: inspect board existence: %w", err)
			}
			if exists == 0 {
				if err := tx.QueryRow(`SELECT revision FROM board_revisions WHERE user = ?`, user).Scan(&revision); err != nil {
					return fmt.Errorf("store: current board revision: %w", err)
				}
				return &RevisionConflictError{CurrentRevision: revision}
			}
		} else if condition.Present {
			var current int64
			if err := tx.QueryRow(`SELECT revision FROM board_revisions WHERE user = ?`, user).Scan(&current); err != nil {
				return fmt.Errorf("store: current board revision: %w", err)
			}
			matched := false
			for _, allowed := range condition.Revisions {
				if current == allowed {
					matched = true
					break
				}
			}
			if !matched {
				revision = current
				return &RevisionConflictError{CurrentRevision: revision}
			}
			result, err := tx.Exec(`UPDATE board_revisions SET revision = revision + 1 WHERE user = ? AND revision = ?`, user, current)
			if err != nil {
				return fmt.Errorf("store: claim board revision: %w", err)
			}
			n, err := result.RowsAffected()
			if err != nil {
				return fmt.Errorf("store: inspect board revision claim: %w", err)
			}
			if n != 1 {
				if err := tx.QueryRow(`SELECT revision FROM board_revisions WHERE user = ?`, user).Scan(&revision); err != nil {
					return fmt.Errorf("store: current board revision: %w", err)
				}
				return &RevisionConflictError{CurrentRevision: revision}
			}
		}
		if operationID != "" && !allowNewReceipt {
			return ErrInvalidTaskIDs
		}
		if allowNewReceipt && operationID == "" {
			return ErrInvalidTaskIDs
		}
		var err error
		taskIDs, err = s.replaceBoardTx(tx, user, b, canonicalIDs)
		if err != nil {
			return err
		}
		if err := tx.QueryRow(`SELECT revision FROM board_revisions WHERE user = ?`, user).Scan(&revision); err != nil {
			return fmt.Errorf("store: committed board revision: %w", err)
		}
		if operationID != "" {
			encoded, err := json.Marshal(taskIDs)
			if err != nil {
				return fmt.Errorf("store: encode board write receipt: %w", err)
			}
			if _, err := tx.Exec(`INSERT INTO board_write_receipts(user, operation_id, request_hash, task_ids, revision) VALUES (?, ?, ?, ?, ?)`, user, operationID, requestHash, string(encoded), revision); err != nil {
				return fmt.Errorf("store: insert board write receipt: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, revision, false, err
	}
	return taskIDs, revision, replayed, nil
}

func (s *Store) replaceBoardTx(tx *sql.Tx, user string, b board.Board, canonicalIDs []*string) ([]string, error) {
	now := time.Now().UTC()
	matches := make([]*exTask, len(b.Tasks))
	if canonicalIDs == nil {
		byStatusTitle, byTitle, err := loadExisting(tx, user)
		if err != nil {
			return nil, err
		}
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
	} else {
		if len(canonicalIDs) != len(b.Tasks) {
			return nil, ErrInvalidTaskIDs
		}
		existing, err := loadExistingByID(tx, user)
		if err != nil {
			return nil, err
		}
		seen := make(map[string]struct{}, len(canonicalIDs))
		for i, id := range canonicalIDs {
			if id == nil {
				continue
			}
			if _, err := uuid.Parse(*id); err != nil {
				return nil, ErrInvalidTaskIDs
			}
			if _, duplicate := seen[*id]; duplicate {
				return nil, ErrInvalidTaskIDs
			}
			seen[*id] = struct{}{}
			e, ok := existing[*id]
			if !ok {
				return nil, ErrInvalidTaskIDs
			}
			matches[i] = e
		}
	}
	if _, err := tx.Exec(`DELETE FROM tasks WHERE user = ?`, user); err != nil {
		return nil, fmt.Errorf("store: clear board: %w", err)
	}
	taskIDs := make([]string, len(b.Tasks))
	pos := map[board.Status]int{}
	for i, t := range b.Tasks {
		if !t.Status.Valid() {
			return nil, fmt.Errorf("store: invalid status %q", t.Status)
		}
		if err := validateTaskLines(t); err != nil {
			return nil, err
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
			return nil, err
		}
		taskIDs[i] = t.ID
		if err := s.upsertLabels(tx, user, t.Tags); err != nil {
			return nil, err
		}
	}
	if err := setMeta(tx, titleKey(user), b.Title); err != nil {
		return nil, err
	}
	// Full-board replacement is also the restore/purge transaction. Keep a
	// reason only when its canonical task still exists and is still cancelled.
	if _, err := tx.Exec(`
		DELETE FROM tombstones
		WHERE scope = ? AND task_id NOT IN (
			SELECT id FROM tasks WHERE user = ? AND status = 'cancelled'
		)`, user, user); err != nil {
		return nil, fmt.Errorf("store: reconcile board tombstones: %w", err)
	}
	return taskIDs, nil
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
	rows, err := tx.Query(
		`SELECT id, title, status, created_at, moved_at FROM tasks WHERE user = ? ORDER BY `+statusRank+`, position`,
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

func loadExistingByID(tx *sql.Tx, user string) (map[string]*exTask, error) {
	rows, err := tx.Query(`SELECT id, status, created_at, moved_at FROM tasks WHERE user = ?`, user)
	if err != nil {
		return nil, fmt.Errorf("store: load canonical tasks: %w", err)
	}
	defer rows.Close()
	out := map[string]*exTask{}
	for rows.Next() {
		var e exTask
		var status, created, moved string
		if err := rows.Scan(&e.id, &status, &created, &moved); err != nil {
			return nil, fmt.Errorf("store: scan canonical task: %w", err)
		}
		e.status = board.Status(status)
		if e.created, err = time.Parse(time.RFC3339Nano, created); err != nil {
			return nil, fmt.Errorf("store: task %s created_at: %w", e.id, err)
		}
		if e.moved, err = time.Parse(time.RFC3339Nano, moved); err != nil {
			return nil, fmt.Errorf("store: task %s moved_at: %w", e.id, err)
		}
		out[e.id] = &e
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: load canonical tasks: %w", err)
	}
	return out, nil
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
			if *moveTo != board.StatusCancelled {
				if _, err := tx.Exec(`DELETE FROM tombstones WHERE scope = ? AND task_id = ?`, user, t.ID); err != nil {
					return fmt.Errorf("store: delete restored task tombstone: %w", err)
				}
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
		if _, err := tx.Exec(`DELETE FROM tombstones WHERE scope = ? AND task_id = ?`, user, id); err != nil {
			return fmt.Errorf("store: delete task tombstone: %w", err)
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
