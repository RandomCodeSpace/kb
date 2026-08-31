package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RandomCodeSpace/kb/internal/board"
)

var coverageDriverSequence atomic.Uint64

type coverageFailureDriver struct{}

func (coverageFailureDriver) Open(name string) (driver.Conn, error) {
	return &coverageFailureConn{scenario: name}, nil
}

type coverageFailureConn struct {
	scenario string
	queries  int
}

func (*coverageFailureConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (*coverageFailureConn) Close() error              { return nil }
func (*coverageFailureConn) Begin() (driver.Tx, error) { return coverageFailureTx{}, nil }
func (*coverageFailureConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return coverageResult{}, nil
}
func (c *coverageFailureConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	c.queries++
	if c.scenario == "second-query" && c.queries == 1 {
		return &coverageFailureRows{}, nil
	}
	return &coverageFailureRows{nextErr: errors.New("injected rows failure")}, nil
}

type coverageFailureTx struct{}

func (coverageFailureTx) Commit() error   { return nil }
func (coverageFailureTx) Rollback() error { return nil }

type coverageFailureRows struct{ nextErr error }

func (*coverageFailureRows) Columns() []string { return []string{"value"} }
func (*coverageFailureRows) Close() error      { return nil }
func (r *coverageFailureRows) Next([]driver.Value) error {
	if r.nextErr != nil {
		err := r.nextErr
		r.nextErr = nil
		return err
	}
	return io.EOF
}

func newCoverageFailureStore(t *testing.T, scenario string) *Store {
	t.Helper()
	name := "webtui-coverage-driver-" + strconv.FormatUint(coverageDriverSequence.Add(1), 10)
	sql.Register(name, coverageFailureDriver{})
	db, err := sql.Open(name, scenario)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &Store{db: db}
}

func TestSanitizeUserAcceptsCanonicalIdentityAndRejectsUnsafeValues(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "canonicalizes case", in: "Alice.Example+invalid", wantErr: true},
		{name: "accepts shared identity alphabet", in: "Alice_1@example.com", want: "alice_1@example.com"},
		{name: "rejects empty", wantErr: true},
		{name: "rejects traversal prefix", in: ".hidden", wantErr: true},
		{name: "rejects slash", in: "a/b", wantErr: true},
		{name: "rejects unicode", in: "förge", wantErr: true},
		{name: "rejects overlong", in: strings.Repeat("a", 251), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SanitizeUser(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("SanitizeUser(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("SanitizeUser(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestHasBoardTracksLocalBoardState(t *testing.T) {
	s := newStore(t)
	const user = "coverage-user"

	exists, err := s.HasBoard(user)
	if err != nil || exists {
		t.Fatalf("HasBoard(empty) = %v, %v, want false, nil", exists, err)
	}
	b := board.Board{Title: "Coverage", Tasks: []board.Task{{Title: "one", Status: board.StatusTodo}}}
	if err := s.ReplaceBoard(user, b); err != nil {
		t.Fatalf("ReplaceBoard: %v", err)
	}
	exists, err = s.HasBoard(user)
	if err != nil || !exists {
		t.Fatalf("HasBoard(saved) = %v, %v, want true, nil", exists, err)
	}
}

func TestDeleteTombstoneRemovesScopedReason(t *testing.T) {
	s := newStore(t)
	task, err := s.AddTask("u", board.Task{Title: "cancel me"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MoveTask("u", task.ID, board.StatusCancelled); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordTombstone("u", task.ID, "obsolete"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTombstone("u", task.ID); err != nil {
		t.Fatalf("DeleteTombstone: %v", err)
	}
	if _, found, err := s.Tombstone("u", task.ID); err != nil || found {
		t.Fatalf("Tombstone after delete = found %v err %v", found, err)
	}
}

func TestStoreMethodsReturnErrorsAfterDatabaseClose(t *testing.T) {
	s := newStore(t)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	tests := []struct {
		name string
		call func() error
	}{
		{name: "AISettings", call: func() error { _, err := s.AISettings("u"); return err }},
		{name: "SetAISettings", call: func() error { _, err := s.SetAISettings("u", nil, nil, nil); return err }},
		{name: "AIKey", call: func() error { _, err := s.AIKey("u"); return err }},
		{name: "ForgeSources", call: func() error { _, err := s.ForgeSources("u"); return err }},
		{name: "DeleteForgeSource", call: func() error { return s.DeleteForgeSource("u", "primary") }},
		{name: "ForgePAT", call: func() error { _, _, _, err := s.ForgePAT("u", "primary"); return err }},
		{name: "DeleteTombstone", call: func() error { return s.DeleteTombstone("u", "id") }},
		{name: "HasBoard", call: func() error { _, err := s.HasBoard("u"); return err }},
		{name: "Board", call: func() error { _, err := s.Board("u"); return err }},
		{name: "ReadBoardSnapshot", call: func() error { _, err := s.ReadBoardSnapshot("u"); return err }},
		{name: "ReplaceBoard", call: func() error { return s.ReplaceBoard("u", board.Board{}) }},
		{name: "ReplaceBoardWithTaskIDs", call: func() error { _, err := s.ReplaceBoardWithTaskIDs("u", board.Board{}); return err }},
		{name: "AddTask", call: func() error { _, err := s.AddTask("u", board.Task{Title: "task"}); return err }},
		{name: "UpdateTask", call: func() error { _, err := s.UpdateTask("u", "id", TaskPatch{}); return err }},
		{name: "UpdateAndMoveTask", call: func() error { _, err := s.UpdateAndMoveTask("u", "id", TaskPatch{}, nil, nil, nil); return err }},
		{name: "MoveTask", call: func() error { _, err := s.MoveTask("u", "id", board.StatusDoing); return err }},
		{name: "DeleteTask", call: func() error { _, err := s.DeleteTask("u", "id"); return err }},
		{name: "ListTasks", call: func() error { _, err := s.ListTasks("u", ""); return err }},
		{name: "Labels", call: func() error { _, err := s.Labels("u"); return err }},
		{name: "SearchSimilar", call: func() error { _, err := s.SearchSimilar("u", "query", "", nil, 3); return err }},
		{name: "RecordTombstone", call: func() error { return s.RecordTombstone("u", "id", "reason") }},
		{name: "Tombstone", call: func() error { _, _, err := s.Tombstone("u", "id"); return err }},
		{name: "TasksByLink", call: func() error { _, err := s.TasksByLink("u", "link::x"); return err }},
		{name: "ImportBaseline", call: func() error { _, _, err := s.ImportBaseline("u", "key"); return err }},
		{name: "SetImportBaseline", call: func() error {
			return s.SetImportBaseline("u", "key", NewImportBaseline("title", "body", "2026-01-01T00:00:00Z"))
		}},
		{name: "ImportedAs", call: func() error { _, err := s.ImportedAs("u", []string{"key"}); return err }},
		{name: "ImportLinksByLink", call: func() error { _, err := s.ImportLinksByLink("u", "link"); return err }},
		{name: "RecordImportLinks", call: func() error {
			return s.RecordImportLinks("u", []ImportLink{{Source: "s", Kind: "gitlab", ExternalKey: "key", Link: "link", URL: "https://example.test", Title: "title"}})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); err == nil {
				t.Fatalf("%s after Close returned nil error", tt.name)
			}
		})
	}
}

func TestMigrationAndStoreHelpersRejectBrokenDatabaseState(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "broken.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := migrate(db); err == nil {
		t.Fatal("migrate accepted a closed database")
	}
	if err := enableWAL(db); err == nil {
		t.Fatal("enableWAL accepted a closed database")
	}
	if err := repairAIBaseURLSuffixes(db); err == nil {
		t.Fatal("repairAIBaseURLSuffixes accepted a closed database")
	}
	if isSQLiteBusy(errors.New("other")) {
		t.Fatal("isSQLiteBusy classified a non-SQLite error as busy")
	}
	if _, err := scanTask(coverageScanner{err: errors.New("scan failed")}); err == nil {
		t.Fatal("scanTask accepted a scanner error")
	}
}

type coverageScanner struct{ err error }

func (s coverageScanner) Scan(...any) error { return s.err }

type coverageTaskScanner struct {
	tags, checks, created, moved string
}

func (s coverageTaskScanner) Scan(dest ...any) error {
	values := []any{"id", 1, "", "title", "", "todo", 0, 3, "", "", s.tags, s.checks, 0, s.created, s.moved}
	for i, value := range values {
		d := reflect.ValueOf(dest[i])
		d.Elem().Set(reflect.ValueOf(value).Convert(d.Elem().Type()))
	}
	return nil
}

type coverageExecFailDB struct{ err error }

func (q coverageExecFailDB) Exec(string, ...any) (sql.Result, error) { return nil, q.err }
func (q coverageExecFailDB) Query(string, ...any) (*sql.Rows, error) { return nil, q.err }
func (q coverageExecFailDB) QueryRow(string, ...any) *sql.Row        { return nil }

type coverageResult struct{}

func (coverageResult) LastInsertId() (int64, error) { return 0, nil }
func (coverageResult) RowsAffected() (int64, error) { return 1, nil }

type coverageQueryRowFailDB struct{ db *sql.DB }

func (q coverageQueryRowFailDB) Exec(string, ...any) (sql.Result, error) {
	return coverageResult{}, nil
}
func (q coverageQueryRowFailDB) Query(string, ...any) (*sql.Rows, error) {
	return q.db.Query(`SELECT 1`)
}
func (q coverageQueryRowFailDB) QueryRow(string, ...any) *sql.Row {
	return q.db.QueryRow(`SELECT 1`)
}

func TestLowLevelStoreHelpersMapMalformedRowsAndDatabaseErrors(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, tt := range []struct {
		name                         string
		tags, checks, created, moved string
	}{
		{name: "tags", tags: "{", checks: "[]", created: now, moved: now},
		{name: "checks", tags: "[]", checks: "{", created: now, moved: now},
		{name: "created time", tags: "[]", checks: "[]", created: "bad", moved: now},
		{name: "moved time", tags: "[]", checks: "[]", created: now, moved: "bad"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := scanTask(coverageTaskScanner{tags: tt.tags, checks: tt.checks, created: tt.created, moved: tt.moved}); err == nil {
				t.Fatal("scanTask returned nil error")
			}
		})
	}
	if _, err := scanTask(coverageScanner{err: sql.ErrNoRows}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("scanTask(sql.ErrNoRows) = %v", err)
	}
	fail := coverageExecFailDB{err: errors.New("db failed")}
	if err := insertTask(fail, "u", board.Task{ID: "id", Title: "title"}); err == nil {
		t.Fatal("insertTask returned nil error")
	}
	if err := setMeta(fail, "k", "v"); err == nil {
		t.Fatal("setMeta returned nil error")
	}
	if _, err := queryTasks(fail, "query"); err == nil {
		t.Fatal("queryTasks returned nil error")
	}
	s := newStore(t)
	if err := s.upsertLabels(fail, "u", []string{"tag"}); err == nil {
		t.Fatal("upsertLabels returned nil error")
	}
}

func TestOpenRejectsInvalidPathsAndSchemas(t *testing.T) {
	if _, err := Open(t.TempDir(), []byte("secret")); err == nil {
		t.Fatal("Open accepted a directory as a database path")
	}
	path := filepath.Join(t.TempDir(), "kb.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE meta (k TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path, []byte("secret")); err == nil {
		t.Fatal("Open accepted a nonnumeric schema version")
	}

	for _, rawVersion := range []string{"-1", "not-a-number", "01", strconv.Itoa(len(migrations) + 1)} {
		t.Run("ledger_"+rawVersion, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "kb.db")
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`CREATE TABLE meta (k TEXT PRIMARY KEY, v TEXT NOT NULL)`); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO meta(k, v) VALUES ('schema_version', ?)`, rawVersion); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(path, []byte("secret")); err == nil ||
				!strings.Contains(err.Error(), "newer kb binary or restore a compatible backup") {
				t.Fatalf("Open schema version %q error = %v", rawVersion, err)
			}
			db, err = sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var got string
			if err := db.QueryRow(`SELECT v FROM meta WHERE k = 'schema_version'`).Scan(&got); err != nil || got != rawVersion {
				t.Fatalf("rejected ledger changed from %q to %q: %v", rawVersion, got, err)
			}
			var taskTable int
			if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='tasks'`).Scan(&taskTable); err != nil || taskTable != 0 {
				t.Fatalf("rejected ledger ran migrations: tasks=%d err=%v", taskTable, err)
			}
		})
	}
}

func TestSameAIOriginRejectsMalformedURLs(t *testing.T) {
	if SameAIOrigin("%", "https://example.test") || SameAIOrigin("https://example.test", "%") {
		t.Fatal("SameAIOrigin accepted a malformed URL")
	}
	if !SameAIOrigin("HTTPS://EXAMPLE.TEST/path", "https://example.test/other") {
		t.Fatal("SameAIOrigin rejected equal scheme and host")
	}
}

func TestImportMarkdownDirReportsFileAndDatabaseFailures(t *testing.T) {
	s := newStore(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broken.md"), []byte("# Board"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportMarkdownDir(dir); err == nil {
		t.Fatal("ImportMarkdownDir accepted a closed database")
	}
	if got := importKey("alice"); got != "imported:alice" {
		t.Fatalf("importKey = %q", got)
	}
}

func TestSecretCreationReportsDeterministicFilesystemAndEntropyFailures(t *testing.T) {
	dir := t.TempDir()
	if _, err := createSecret(dir, filepath.Join(dir, "secret"), coverageErrorReader{}, nil); err == nil {
		t.Fatal("createSecret accepted failed entropy")
	}
	missing := filepath.Join(dir, "missing")
	if _, err := createSecret(missing, filepath.Join(missing, "secret"), strings.NewReader(strings.Repeat("x", secretFileBytes)), nil); err == nil {
		t.Fatal("createSecret accepted a missing data directory")
	}
	if _, err := createSecret(dir, filepath.Join(dir, "missing", "secret"), strings.NewReader(strings.Repeat("x", secretFileBytes)), nil); err == nil {
		t.Fatal("createSecret accepted an unpublishable final path")
	}
	secretDir := filepath.Join(dir, "secret-dir")
	if err := os.Mkdir(secretDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(secretDir, "secret"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateSecret(secretDir); err == nil {
		t.Fatal("LoadOrCreateSecret accepted a directory at the secret path")
	}
}

type coverageErrorReader struct{}

func (coverageErrorReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

type coverageSecretTemp struct {
	writeN   int
	writeErr error
	chmodErr error
	syncErr  error
	closeErr error
}

func (*coverageSecretTemp) Name() string { return "/tmp/webtui-coverage-secret" }
func (f *coverageSecretTemp) Write(p []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	if f.writeN != 0 {
		return f.writeN, nil
	}
	return len(p), nil
}
func (f *coverageSecretTemp) Chmod(os.FileMode) error { return f.chmodErr }
func (f *coverageSecretTemp) Sync() error             { return f.syncErr }
func (f *coverageSecretTemp) Close() error            { return f.closeErr }

type coverageSecretDir struct {
	syncErr, closeErr error
}

func (d *coverageSecretDir) Sync() error  { return d.syncErr }
func (d *coverageSecretDir) Close() error { return d.closeErr }

func installCoverageSecretOps(t *testing.T) {
	t.Helper()
	originalCreate := createSecretTemp
	originalRemove := removeSecretFile
	originalLink := linkSecretFile
	originalRead := readSecretFile
	originalOpen := openSecretDir
	t.Cleanup(func() {
		createSecretTemp = originalCreate
		removeSecretFile = originalRemove
		linkSecretFile = originalLink
		readSecretFile = originalRead
		openSecretDir = originalOpen
	})
}

func TestCreateSecretReportsInjectedDurabilityFailures(t *testing.T) {
	random := func() io.Reader { return strings.NewReader(strings.Repeat("x", secretFileBytes)) }
	failure := errors.New("injected failure")

	t.Run("create temp", func(t *testing.T) {
		installCoverageSecretOps(t)
		createSecretTemp = func(string, string) (secretTempFile, error) { return nil, failure }
		if _, err := createSecret("dir", "path", random(), nil); err == nil {
			t.Fatal("createSecret returned nil error")
		}
	})

	for _, tt := range []struct {
		name string
		file *coverageSecretTemp
	}{
		{name: "write", file: &coverageSecretTemp{writeErr: failure}},
		{name: "short write", file: &coverageSecretTemp{writeN: 1}},
		{name: "chmod", file: &coverageSecretTemp{chmodErr: failure}},
		{name: "sync", file: &coverageSecretTemp{syncErr: failure}},
		{name: "close", file: &coverageSecretTemp{closeErr: failure}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			installCoverageSecretOps(t)
			createSecretTemp = func(string, string) (secretTempFile, error) { return tt.file, nil }
			removeSecretFile = func(string) error { return nil }
			if _, err := createSecret("dir", "path", random(), nil); err == nil {
				t.Fatal("createSecret returned nil error")
			}
		})
	}

	t.Run("remove losing temp", func(t *testing.T) {
		installCoverageSecretOps(t)
		createSecretTemp = func(string, string) (secretTempFile, error) { return &coverageSecretTemp{}, nil }
		linkSecretFile = func(string, string) error { return os.ErrExist }
		removeSecretFile = func(string) error { return failure }
		if _, err := createSecret("dir", "path", random(), nil); err == nil {
			t.Fatal("createSecret returned nil error")
		}
	})

	t.Run("read winning secret", func(t *testing.T) {
		installCoverageSecretOps(t)
		createSecretTemp = func(string, string) (secretTempFile, error) { return &coverageSecretTemp{}, nil }
		linkSecretFile = func(string, string) error { return os.ErrExist }
		removeSecretFile = func(string) error { return nil }
		readSecretFile = func(string) ([]byte, error) { return nil, failure }
		if _, err := createSecret("dir", "path", random(), nil); err == nil {
			t.Fatal("createSecret returned nil error")
		}
	})

	t.Run("short winning secret", func(t *testing.T) {
		installCoverageSecretOps(t)
		createSecretTemp = func(string, string) (secretTempFile, error) { return &coverageSecretTemp{}, nil }
		linkSecretFile = func(string, string) error { return os.ErrExist }
		removeSecretFile = func(string) error { return nil }
		readSecretFile = func(string) ([]byte, error) { return []byte("short"), nil }
		if _, err := createSecret("dir", "path", random(), nil); err == nil {
			t.Fatal("createSecret accepted a short winning secret")
		}
	})

	for _, tt := range []struct {
		name    string
		openErr error
		dir     *coverageSecretDir
		remove  error
	}{
		{name: "open data directory", openErr: failure},
		{name: "sync data directory", dir: &coverageSecretDir{syncErr: failure}},
		{name: "close data directory", dir: &coverageSecretDir{closeErr: failure}},
		{name: "remove published temp", dir: &coverageSecretDir{}, remove: failure},
	} {
		t.Run(tt.name, func(t *testing.T) {
			installCoverageSecretOps(t)
			createSecretTemp = func(string, string) (secretTempFile, error) { return &coverageSecretTemp{}, nil }
			linkSecretFile = func(string, string) error { return nil }
			openSecretDir = func(string) (secretDirectory, error) { return tt.dir, tt.openErr }
			removeSecretFile = func(string) error { return tt.remove }
			if _, err := createSecret("dir", "path", random(), nil); err == nil {
				t.Fatal("createSecret returned nil error")
			}
		})
	}
}

func TestEncryptionReportsInjectedEntropyFailure(t *testing.T) {
	s := newStore(t)
	s.randomRead = func([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
	key := "secret"
	if _, err := s.SetAISettings("u", nil, nil, &key); err == nil {
		t.Fatal("SetAISettings accepted failed entropy")
	}
	base := "https://forge.example"
	if _, err := s.SetForgeSource("u", "primary", "gitlab", &base, &key); err == nil {
		t.Fatal("SetForgeSource accepted failed entropy")
	}
}

func TestQueryMethodsReportDriverIteratorFailures(t *testing.T) {
	tests := []struct {
		name, scenario string
		call           func(*Store) error
	}{
		{name: "forge sources", call: func(s *Store) error { _, err := s.ForgeSources("u"); return err }},
		{name: "task similarity", call: func(s *Store) error { _, err := s.SearchSimilar("u", "query", "", nil, 3); return err }},
		{name: "import similarity", scenario: "second-query", call: func(s *Store) error { _, err := s.SearchSimilar("u", "query", "", nil, 3); return err }},
		{name: "tasks by link", call: func(s *Store) error { _, err := s.TasksByLink("u", "link"); return err }},
		{name: "imported as", call: func(s *Store) error { _, err := s.ImportedAs("u", []string{"key"}); return err }},
		{name: "imports by link", call: func(s *Store) error { _, err := s.ImportLinksByLink("u", "link"); return err }},
		{name: "labels", call: func(s *Store) error { _, err := s.Labels("u"); return err }},
		{name: "list tasks", call: func(s *Store) error { _, err := s.ListTasks("u", ""); return err }},
		{name: "resolve ID", call: func(s *Store) error { _, err := resolveID(s.db, "u", "prefix"); return err }},
		{name: "legacy identity loader", call: func(s *Store) error {
			return s.ReplaceBoard("u", board.Board{})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newCoverageFailureStore(t, tt.scenario)
			if err := tt.call(s); err == nil {
				t.Fatal("query method returned nil error")
			}
		})
	}
}

func TestCorruptTablesExerciseTransactionalErrorBoundaries(t *testing.T) {
	tests := []struct {
		name, table string
		call        func(*Store) error
	}{
		{name: "snapshot tasks", table: "tasks", call: func(s *Store) error { _, err := s.ReadBoardSnapshot("u"); return err }},
		{name: "snapshot meta", table: "meta", call: func(s *Store) error { _, err := s.ReadBoardSnapshot("u"); return err }},
		{name: "snapshot revisions", table: "board_revisions", call: func(s *Store) error { _, err := s.ReadBoardSnapshot("u"); return err }},
		{name: "replace tasks", table: "tasks", call: func(s *Store) error { return s.ReplaceBoard("u", board.Board{Title: "x"}) }},
		{name: "replace meta", table: "meta", call: func(s *Store) error { return s.ReplaceBoard("u", board.Board{Title: "x"}) }},
		{name: "add tasks", table: "tasks", call: func(s *Store) error { _, err := s.AddTask("u", board.Task{Title: "x"}); return err }},
		{name: "list tasks", table: "tasks", call: func(s *Store) error { _, err := s.ListTasks("u", ""); return err }},
		{name: "labels", table: "labels", call: func(s *Store) error { _, err := s.Labels("u"); return err }},
		{name: "search tasks", table: "tasks_fts", call: func(s *Store) error { _, err := s.SearchSimilar("u", "query", "", nil, 3); return err }},
		{name: "tombstone insert", table: "tombstones", call: func(s *Store) error { return s.RecordTombstone("u", "id", "reason") }},
		{name: "tasks by link", table: "tasks", call: func(s *Store) error { _, err := s.TasksByLink("u", "link"); return err }},
		{name: "imported as", table: "import_links", call: func(s *Store) error { _, err := s.ImportedAs("u", []string{"key"}); return err }},
		{name: "imports by link", table: "import_links", call: func(s *Store) error { _, err := s.ImportLinksByLink("u", "link"); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newStore(t)
			if _, err := s.db.Exec("DROP TABLE " + tt.table); err != nil {
				t.Fatalf("drop %s: %v", tt.table, err)
			}
			if err := tt.call(s); err == nil {
				t.Fatal("operation returned nil error")
			}
		})
	}
}

func TestCorruptEncryptedSettingsAreRejected(t *testing.T) {
	s := newStore(t)
	if _, err := s.db.Exec(`INSERT INTO settings(user, ai_base_url, ai_model, ai_key_enc) VALUES (?, ?, ?, ?)`,
		"u", "https://ai.example", "model", []byte{1, 2, 3}); err != nil {
		t.Fatalf("seed corrupt setting: %v", err)
	}
	settings, err := s.AISettings("u")
	if err != nil || !settings.HasKey {
		t.Fatalf("AISettings = %+v, %v, want HasKey", settings, err)
	}
	if _, err := s.AIKey("u"); err == nil {
		t.Fatal("AIKey accepted corrupt ciphertext")
	}
}

func TestForgeSourcesRejectCorruptCreationTimestamp(t *testing.T) {
	s := newStore(t)
	if _, err := s.db.Exec(`INSERT INTO forge_sources(scope, name, kind, base_url, pat_enc, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		"u", "primary", "gitlab", "https://forge.example", nil, "not-a-time"); err != nil {
		t.Fatalf("seed corrupt forge source: %v", err)
	}
	if _, err := s.ForgeSources("u"); err == nil {
		t.Fatal("ForgeSources accepted a corrupt creation timestamp")
	}
}

func TestReplaceBoardReportsCorruptExistingIdentityTimes(t *testing.T) {
	for _, tt := range []struct {
		name, column string
	}{
		{name: "legacy created time", column: "created_at"},
		{name: "legacy moved time", column: "moved_at"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := newStore(t)
			seed, err := s.AddTask("u", board.Task{Title: "one", Status: board.StatusTodo})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec("UPDATE tasks SET "+tt.column+" = 'bad' WHERE id = ?", seed.ID); err != nil {
				t.Fatal(err)
			}
			if err := s.ReplaceBoard("u", board.Board{Tasks: []board.Task{{Title: "one", Status: board.StatusTodo}}}); err == nil {
				t.Fatal("replacement accepted a corrupt identity timestamp")
			}
		})
	}
}

func TestReplaceBoardReportsTransactionalWriteFailures(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*testing.T, *Store)
		call    func(*Store) error
	}{
		{
			name: "clear tasks",
			prepare: func(t *testing.T, s *Store) {
				if _, err := s.AddTask("u", board.Task{Title: "old"}); err != nil {
					t.Fatal(err)
				}
				mustExecCoverage(t, s, `CREATE TRIGGER coverage_fail_clear BEFORE DELETE ON tasks BEGIN SELECT RAISE(ABORT, 'clear failed'); END`)
			},
			call: func(s *Store) error { return s.ReplaceBoard("u", board.Board{}) },
		},
		{
			name: "insert task",
			prepare: func(t *testing.T, s *Store) {
				mustExecCoverage(t, s, `CREATE TRIGGER coverage_fail_task BEFORE INSERT ON tasks BEGIN SELECT RAISE(ABORT, 'task failed'); END`)
			},
			call: func(s *Store) error {
				return s.ReplaceBoard("u", board.Board{Tasks: []board.Task{{Title: "new", Status: board.StatusTodo}}})
			},
		},
		{
			name: "upsert label",
			prepare: func(t *testing.T, s *Store) {
				mustExecCoverage(t, s, `CREATE TRIGGER coverage_fail_label BEFORE INSERT ON labels BEGIN SELECT RAISE(ABORT, 'label failed'); END`)
			},
			call: func(s *Store) error {
				return s.ReplaceBoard("u", board.Board{Tasks: []board.Task{{Title: "new", Status: board.StatusTodo, Tags: []string{"tag"}}}})
			},
		},
		{
			name: "set title",
			prepare: func(t *testing.T, s *Store) {
				mustExecCoverage(t, s, `CREATE TRIGGER coverage_fail_meta BEFORE INSERT ON meta BEGIN SELECT RAISE(ABORT, 'meta failed'); END`)
			},
			call: func(s *Store) error { return s.ReplaceBoard("u", board.Board{Title: "new"}) },
		},
		{
			name: "reconcile tombstone",
			prepare: func(t *testing.T, s *Store) {
				task, err := s.AddTask("u", board.Task{Title: "old"})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := s.MoveTask("u", task.ID, board.StatusCancelled); err != nil {
					t.Fatal(err)
				}
				if err := s.RecordTombstone("u", task.ID, "reason"); err != nil {
					t.Fatal(err)
				}
				mustExecCoverage(t, s, `CREATE TRIGGER coverage_fail_tombstone BEFORE DELETE ON tombstones BEGIN SELECT RAISE(ABORT, 'tombstone failed'); END`)
			},
			call: func(s *Store) error { return s.ReplaceBoard("u", board.Board{}) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newStore(t)
			tt.prepare(t, s)
			if err := tt.call(s); err == nil {
				t.Fatal("replacement returned nil error")
			}
		})
	}
}

func TestSearchAndImportWritesReportSQLiteFailures(t *testing.T) {
	t.Run("secondary FTS query", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.AddTask("u", board.Task{Title: "query", Status: board.StatusTodo}); err != nil {
			t.Fatal(err)
		}
		mustExecCoverage(t, s, `DROP TABLE import_links_fts`)
		if _, err := s.SearchSimilar("u", "query", "", nil, 3); err == nil {
			t.Fatal("SearchSimilar returned nil error")
		}
	})

	t.Run("record import link", func(t *testing.T) {
		s := newStore(t)
		mustExecCoverage(t, s, `CREATE TRIGGER coverage_fail_import BEFORE INSERT ON import_links BEGIN SELECT RAISE(ABORT, 'import failed'); END`)
		err := s.RecordImportLinks("u", []ImportLink{{Source: "source", Kind: "gitlab", ExternalKey: "key", Link: "link", URL: "https://example.test", Title: "title"}})
		if err == nil {
			t.Fatal("RecordImportLinks returned nil error")
		}
	})

	t.Run("missing markdown file", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.importBoard(filepath.Join(t.TempDir(), "missing.md"), "u"); err == nil {
			t.Fatal("importBoard returned nil error")
		}
	})
}

func TestForgeReadsReportMalformedRowsAndRepairFailures(t *testing.T) {
	t.Run("scan source", func(t *testing.T) {
		s := newStore(t)
		mustExecCoverage(t, s, `DROP TABLE forge_sources`)
		mustExecCoverage(t, s, `CREATE TABLE forge_sources (scope TEXT, name TEXT, kind TEXT, base_url TEXT, pat_enc BLOB, created_at TEXT)`)
		mustExecCoverage(t, s, `INSERT INTO forge_sources(scope, name, kind, base_url, pat_enc, created_at) VALUES ('u', NULL, 'gitlab', 'https://forge.example', NULL, '2026-01-01T00:00:00Z')`)
		if _, err := s.ForgeSources("u"); err == nil {
			t.Fatal("ForgeSources accepted an unscannable row")
		}
	})

	for _, tt := range []struct {
		name string
		call func(*Store) error
	}{
		{name: "source list repair", call: func(s *Store) error { _, err := s.ForgeSources("u"); return err }},
		{name: "PAT repair", call: func(s *Store) error { _, _, _, err := s.ForgePAT("u", "primary"); return err }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := newStore(t)
			mustExecCoverage(t, s, `INSERT INTO forge_sources(scope, name, kind, base_url, pat_enc, created_at) VALUES ('u', 'primary', 'gitlab', 'https://forge.example?legacy=1', NULL, '2026-01-01T00:00:00Z')`)
			mustExecCoverage(t, s, `CREATE TRIGGER coverage_fail_forge_repair BEFORE UPDATE ON forge_sources BEGIN SELECT RAISE(ABORT, 'repair failed'); END`)
			if err := tt.call(s); err == nil {
				t.Fatal("forge read returned nil error")
			}
		})
	}
}

func mustExecCoverage(t *testing.T, s *Store, query string) {
	t.Helper()
	if _, err := s.db.Exec(query); err != nil {
		t.Fatalf("exec coverage fixture: %v", err)
	}
}

func TestTaskMutationValidationAndRollbackBoundaries(t *testing.T) {
	s := newStore(t)
	if _, err := s.AddTask("u", board.Task{Title: "bad", Status: board.Status("invalid")}); err == nil {
		t.Fatal("AddTask accepted an invalid status")
	}
	invalid := board.Status("invalid")
	if _, err := s.UpdateAndMoveTask("u", "missing", TaskPatch{}, &invalid, nil, nil); err == nil {
		t.Fatal("UpdateAndMoveTask accepted an invalid status")
	}
	if _, err := s.ListTasks("u", invalid); err == nil {
		t.Fatal("ListTasks accepted an invalid status")
	}
	if err := validateTaskLines(board.Task{Title: "valid", Tags: []string{"bad\ntag"}}); err == nil {
		t.Fatal("validateTaskLines accepted a multiline tag")
	}
	if err := s.upsertLabels(s.db, "u", []string{""}); err != nil {
		t.Fatalf("upsertLabels(empty tag): %v", err)
	}

	task, err := s.AddTask("u", board.Task{Title: "task"})
	if err != nil {
		t.Fatal(err)
	}
	emoji, effort := "🚀", "S"
	if _, err := s.UpdateTask("u", task.ID, TaskPatch{Emoji: &emoji, Effort: &effort}); err != nil {
		t.Fatalf("UpdateTask optional fields: %v", err)
	}
}

func TestTaskMutationsReportSQLiteWriteFailures(t *testing.T) {
	tests := []struct {
		name, trigger string
		call          func(*Store, board.Task) error
	}{
		{
			name:    "patch task",
			trigger: `CREATE TRIGGER coverage_fail_patch BEFORE UPDATE ON tasks BEGIN SELECT RAISE(ABORT, 'patch failed'); END`,
			call: func(s *Store, task board.Task) error {
				title := "changed"
				_, err := s.UpdateTask("u", task.ID, TaskPatch{Title: &title})
				return err
			},
		},
		{
			name:    "move task",
			trigger: `CREATE TRIGGER coverage_fail_move BEFORE UPDATE ON tasks BEGIN SELECT RAISE(ABORT, 'move failed'); END`,
			call: func(s *Store, task board.Task) error {
				_, err := s.MoveTask("u", task.ID, board.StatusDoing)
				return err
			},
		},
		{
			name:    "delete task",
			trigger: `CREATE TRIGGER coverage_fail_delete BEFORE DELETE ON tasks BEGIN SELECT RAISE(ABORT, 'delete failed'); END`,
			call:    func(s *Store, task board.Task) error { _, err := s.DeleteTask("u", task.ID); return err },
		},
		{
			name:    "delete task tombstone",
			trigger: `CREATE TRIGGER coverage_fail_delete_tombstone BEFORE DELETE ON tombstones BEGIN SELECT RAISE(ABORT, 'tombstone failed'); END`,
			call:    func(s *Store, task board.Task) error { _, err := s.DeleteTask("u", task.ID); return err },
		},
		{
			name:    "delete restored tombstone",
			trigger: `CREATE TRIGGER coverage_fail_restore_tombstone BEFORE DELETE ON tombstones BEGIN SELECT RAISE(ABORT, 'restore failed'); END`,
			call: func(s *Store, task board.Task) error {
				_, err := s.MoveTask("u", task.ID, board.StatusDoing)
				return err
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newStore(t)
			task, err := s.AddTask("u", board.Task{Title: "task"})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(tt.name, "tombstone") {
				if _, err := s.MoveTask("u", task.ID, board.StatusCancelled); err != nil {
					t.Fatal(err)
				}
				if err := s.RecordTombstone("u", task.ID, "reason"); err != nil {
					t.Fatal(err)
				}
			}
			mustExecCoverage(t, s, tt.trigger)
			if err := tt.call(s, task); err == nil {
				t.Fatal("mutation returned nil error")
			}
		})
	}
}

func TestStoreQueriesRejectUnscannableRows(t *testing.T) {
	t.Run("list task", func(t *testing.T) {
		s := newStore(t)
		mustExecCoverage(t, s, `INSERT INTO tasks(id, user, emoji, title, "desc", status, blocked, prio, due, effort, tags, checks, position, created_at, moved_at) VALUES ('id', 'u', '', 'title', '', 'todo', 0, 3, '', '', '{', '[]', 0, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)
		if _, err := s.ListTasks("u", ""); err == nil {
			t.Fatal("ListTasks accepted a malformed row")
		}
	})

	t.Run("label", func(t *testing.T) {
		s := newStore(t)
		mustExecCoverage(t, s, `DROP TABLE labels`)
		mustExecCoverage(t, s, `CREATE TABLE labels (user TEXT, label TEXT, last_used INTEGER)`)
		mustExecCoverage(t, s, `INSERT INTO labels(user, label, last_used) VALUES ('u', NULL, 1)`)
		if _, err := s.Labels("u"); err == nil {
			t.Fatal("Labels accepted an unscannable row")
		}
	})

	t.Run("resolve ID", func(t *testing.T) {
		s := newStore(t)
		mustExecCoverage(t, s, `DROP TABLE tasks`)
		mustExecCoverage(t, s, `CREATE TABLE tasks (user TEXT, id TEXT)`)
		mustExecCoverage(t, s, `INSERT INTO tasks(user, id) VALUES ('u', NULL)`)
		if _, err := resolveID(s.db, "u", "prefix"); err == nil {
			t.Fatal("resolveID accepted an unscannable row")
		}
	})

	t.Run("task by link", func(t *testing.T) {
		s := newStore(t)
		mustExecCoverage(t, s, `DROP TABLE tasks`)
		mustExecCoverage(t, s, `CREATE TABLE tasks (user TEXT, id TEXT, title TEXT, status TEXT, tags TEXT, position INTEGER)`)
		mustExecCoverage(t, s, `INSERT INTO tasks(user, id, title, status, tags, position) VALUES ('u', NULL, 'title', 'todo', '["link"]', 0)`)
		if _, err := s.TasksByLink("u", "link"); err == nil {
			t.Fatal("TasksByLink accepted an unscannable row")
		}
	})

	for _, tt := range []struct {
		name string
		call func(*Store) error
	}{
		{name: "imported as", call: func(s *Store) error { _, err := s.ImportedAs("u", []string{"key"}); return err }},
		{name: "imports by link", call: func(s *Store) error { _, err := s.ImportLinksByLink("u", "link"); return err }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := newStore(t)
			mustExecCoverage(t, s, `DROP TABLE import_links`)
			mustExecCoverage(t, s, `CREATE TABLE import_links (scope TEXT, source TEXT, kind TEXT, external_key TEXT, link TEXT, url TEXT, title TEXT)`)
			mustExecCoverage(t, s, `INSERT INTO import_links(scope, source, kind, external_key, link, url, title) VALUES ('u', NULL, 'gitlab', 'key', 'link', 'https://example.test', 'title')`)
			if err := tt.call(s); err == nil {
				t.Fatal("import query accepted an unscannable row")
			}
		})
	}
}

func TestSettingsAndForgeWritesReportDatabaseAndEntropyFailures(t *testing.T) {
	t.Run("AI settings read", func(t *testing.T) {
		s := newStore(t)
		mustExecCoverage(t, s, `DROP TABLE settings`)
		if _, err := s.SetAISettings("u", nil, nil, nil); err == nil {
			t.Fatal("SetAISettings returned nil error")
		}
	})
	t.Run("AI settings write", func(t *testing.T) {
		s := newStore(t)
		mustExecCoverage(t, s, `CREATE TRIGGER coverage_fail_settings BEFORE INSERT ON settings BEGIN SELECT RAISE(ABORT, 'settings failed'); END`)
		if _, err := s.SetAISettings("u", nil, nil, nil); err == nil {
			t.Fatal("SetAISettings returned nil error")
		}
	})
	t.Run("AI key absent", func(t *testing.T) {
		s := newStore(t)
		if key, err := s.AIKey("u"); err != nil || key != "" {
			t.Fatalf("AIKey = %q, %v", key, err)
		}
	})
	for _, tt := range []struct {
		name string
		call func(*Store) error
	}{
		{name: "forge source read", call: func(s *Store) error {
			base := "https://forge.example"
			_, err := s.SetForgeSource("u", "primary", "gitlab", &base, nil)
			return err
		}},
		{name: "delete forge", call: func(s *Store) error { return s.DeleteForgeSource("u", "primary") }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := newStore(t)
			mustExecCoverage(t, s, `DROP TABLE forge_sources`)
			if err := tt.call(s); err == nil {
				t.Fatal("forge operation returned nil error")
			}
		})
	}
	t.Run("forge source write", func(t *testing.T) {
		s := newStore(t)
		mustExecCoverage(t, s, `CREATE TRIGGER coverage_fail_forge_write BEFORE INSERT ON forge_sources BEGIN SELECT RAISE(ABORT, 'forge failed'); END`)
		base := "https://forge.example"
		if _, err := s.SetForgeSource("u", "primary", "gitlab", &base, nil); err == nil {
			t.Fatal("SetForgeSource returned nil error")
		}
	})
	if err := new(Store).DeleteForgeSource("u", "bad name"); err == nil {
		t.Fatal("DeleteForgeSource accepted an invalid name")
	}
	if _, _, _, err := new(Store).ForgePAT("u", "bad name"); err == nil {
		t.Fatal("ForgePAT accepted an invalid name")
	}
}

func TestMarkdownImportSkipsIrrelevantEntriesAndReportsDatabaseFailures(t *testing.T) {
	t.Run("irrelevant entries", func(t *testing.T) {
		s := newStore(t)
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, "folder.md"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignored"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".md"), []byte("ignored"), 0o600); err != nil {
			t.Fatal(err)
		}
		if got, err := s.ImportMarkdownDir(dir); err != nil || got != 0 {
			t.Fatalf("ImportMarkdownDir = %d, %v", got, err)
		}
	})

	for _, tt := range []struct {
		name, table string
	}{
		{name: "import flag", table: "meta"},
		{name: "task count", table: "tasks"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := newStore(t)
			path := filepath.Join(t.TempDir(), "u.md")
			if err := os.WriteFile(path, []byte("# Board\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			mustExecCoverage(t, s, "DROP TABLE "+tt.table)
			if _, err := s.importBoard(path, "u"); err == nil {
				t.Fatal("importBoard returned nil error")
			}
		})
	}
}

func TestSQLiteLifecycleHelpersReportReachableFailures(t *testing.T) {
	if _, err := Open(filepath.Join("/proc", "webtui-coverage.db"), []byte("secret")); err == nil {
		t.Fatal("Open created a database under procfs")
	}
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := enableWAL(db); err == nil {
		t.Fatal("enableWAL accepted SQLite memory journal mode")
	}
	if _, err := db.Exec(`CREATE TABLE settings (ai_base_url TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TABLE settings`); err != nil {
		t.Fatal(err)
	}
	if err := repairAIBaseURLSuffixes(db); err == nil {
		t.Fatal("repairAIBaseURLSuffixes accepted a missing settings table")
	}
	if err := chmodSQLiteFiles("\x00"); err == nil {
		t.Fatal("chmodSQLiteFiles accepted a path containing NUL")
	}

	s := newStore(t)
	if err := s.withTx(func(tx *sql.Tx) error { return tx.Commit() }); err == nil {
		t.Fatal("withTx accepted a transaction committed by its callback")
	}
}

func TestPatchTaskMapsMissingTask(t *testing.T) {
	s := newStore(t)
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	title := "changed"
	if _, err := s.patchTask(tx, "u", "missing", TaskPatch{Title: &title}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("patchTask error = %v, want ErrNotFound", err)
	}
}

func TestReplaceBoardRejectsInvalidStatus(t *testing.T) {
	t.Run("replacement status", func(t *testing.T) {
		s := newStore(t)
		err := s.ReplaceBoard("u", board.Board{Tasks: []board.Task{{Title: "bad", Status: board.Status("invalid")}}})
		if err == nil {
			t.Fatal("replacement accepted an invalid status")
		}
	})
}

func TestExistingTaskLoadersRejectUnscannableRows(t *testing.T) {
	s := newStore(t)
	task, err := s.AddTask("u", board.Task{Title: "task"})
	if err != nil {
		t.Fatal(err)
	}
	mustExecCoverage(t, s, `PRAGMA ignore_check_constraints = ON`)
	if _, err := s.db.Exec(`UPDATE tasks SET created_at = NULL WHERE id = ?`, task.ID); err != nil {
		// NOT NULL remains enforced even when CHECK constraints are ignored.
		mustExecCoverage(t, s, `DROP TABLE tasks`)
		mustExecCoverage(t, s, `CREATE TABLE tasks (id TEXT, user TEXT, title TEXT, status TEXT, created_at TEXT, moved_at TEXT)`)
		mustExecCoverage(t, s, `INSERT INTO tasks(id, user, title, status, created_at, moved_at) VALUES ('00000000-0000-4000-8000-000000000001', 'u', 'task', 'todo', NULL, '2026-01-01T00:00:00Z')`)
	}
	if err := s.ReplaceBoard("u", board.Board{Tasks: []board.Task{{Title: "task", Status: board.StatusTodo}}}); err == nil {
		t.Fatal("replacement accepted an unscannable identity row")
	}
}

func TestLowLevelQueryFailuresAreMapped(t *testing.T) {
	fail := coverageExecFailDB{err: errors.New("query failed")}
	if _, err := resolveID(fail, "u", "prefix"); err == nil {
		t.Fatal("resolveID returned nil error")
	}
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s := newStore(t)
	if err := s.upsertLabels(coverageQueryRowFailDB{db: db}, "u", []string{"tag"}); err == nil {
		t.Fatal("upsertLabels returned nil error")
	}
}

func TestForgeSourcePatchReportsLegacyURLRepairFailure(t *testing.T) {
	s := newStore(t)
	mustExecCoverage(t, s, `INSERT INTO forge_sources(scope, name, kind, base_url, pat_enc, created_at) VALUES ('u', 'primary', 'gitlab', 'https://forge.example?legacy=1', NULL, '2026-01-01T00:00:00Z')`)
	mustExecCoverage(t, s, `CREATE TRIGGER coverage_fail_forge_patch_repair BEFORE UPDATE ON forge_sources BEGIN SELECT RAISE(ABORT, 'repair failed'); END`)
	if _, err := s.SetForgeSource("u", "primary", "gitlab", nil, nil); err == nil {
		t.Fatal("SetForgeSource returned nil error")
	}
}

func TestMarkdownImportReportsTransactionalWriteFailures(t *testing.T) {
	tests := []struct {
		name, trigger, markdown string
	}{
		{name: "insert task", trigger: `CREATE TRIGGER coverage_fail_import_task BEFORE INSERT ON tasks BEGIN SELECT RAISE(ABORT, 'task failed'); END`, markdown: "# Board\n\n## Todo\n- [ ] task\n"},
		{name: "upsert label", trigger: `CREATE TRIGGER coverage_fail_import_label BEFORE INSERT ON labels BEGIN SELECT RAISE(ABORT, 'label failed'); END`, markdown: "# Board\n\n## Todo\n- [ ] task #tag\n"},
		{name: "set title", trigger: `CREATE TRIGGER coverage_fail_import_title BEFORE INSERT ON meta WHEN NEW.k LIKE 'board_title:%' BEGIN SELECT RAISE(ABORT, 'title failed'); END`, markdown: "# Board\n"},
		{name: "set import flag", trigger: `CREATE TRIGGER coverage_fail_import_flag BEFORE INSERT ON meta WHEN NEW.k LIKE 'imported:%' BEGIN SELECT RAISE(ABORT, 'flag failed'); END`, markdown: "# Board\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testMarkdownImportRollback(t, tt.trigger, tt.markdown)
		})
	}
}

func testMarkdownImportRollback(t *testing.T, trigger, markdown string) {
	t.Helper()
	s := newStore(t)
	path := filepath.Join(t.TempDir(), "u.md")
	if err := os.WriteFile(path, []byte(markdown), 0o600); err != nil {
		t.Fatal(err)
	}
	mustExecCoverage(t, s, trigger)
	if _, err := s.importBoard(path, "u"); err == nil {
		t.Fatal("importBoard returned nil error")
	}
	requireEmptyMarkdownImportState(t, s)
}

func requireEmptyMarkdownImportState(t *testing.T, s *Store) {
	t.Helper()
	var tasks, labels, metadata int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE user = 'u'`).Scan(&tasks); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM labels WHERE user = 'u'`).Scan(&labels); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM meta WHERE k IN ('board_title:u', 'imported:u')`).Scan(&metadata); err != nil {
		t.Fatal(err)
	}
	if tasks != 0 || labels != 0 || metadata != 0 {
		t.Fatalf("failed import left tasks=%d labels=%d metadata=%d", tasks, labels, metadata)
	}
}

func TestSimilaritySearchRejectsUnscannableFTSRows(t *testing.T) {
	t.Run("task result", func(t *testing.T) {
		s := newStore(t)
		mustExecCoverage(t, s, `DROP TABLE tasks`)
		mustExecCoverage(t, s, `CREATE TABLE tasks (id TEXT, user TEXT, status TEXT)`)
		mustExecCoverage(t, s, `INSERT INTO tasks(id, user, status) VALUES ('id', 'u', NULL)`)
		mustExecCoverage(t, s, `INSERT INTO tasks_fts(title, body, tags, id, scope) VALUES ('query', '', '', 'id', 'u')`)
		if _, err := s.SearchSimilar("u", "query", "", nil, 3); err == nil {
			t.Fatal("SearchSimilar accepted an unscannable task result")
		}
	})

	t.Run("import result", func(t *testing.T) {
		s := newStore(t)
		mustExecCoverage(t, s, `DROP TABLE import_links`)
		mustExecCoverage(t, s, `CREATE TABLE import_links (scope TEXT, external_key TEXT, link TEXT)`)
		mustExecCoverage(t, s, `INSERT INTO import_links(scope, external_key, link) VALUES ('u', 'key', NULL)`)
		mustExecCoverage(t, s, `INSERT INTO import_links_fts(title, external_key, scope) VALUES ('query', 'key', 'u')`)
		if _, err := s.SearchSimilar("u", "query", "", nil, 3); err == nil {
			t.Fatal("SearchSimilar accepted an unscannable import result")
		}
	})
}

func TestImportMarkdownDirRejectsNonDirectoryPath(t *testing.T) {
	s := newStore(t)
	path := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(path, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportMarkdownDir(path); err == nil {
		t.Fatal("ImportMarkdownDir accepted a regular file")
	}
}
