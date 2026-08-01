package store

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"syscall"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/board"
)

func openStoreAt(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(path, []byte("test-secret"))
	if err != nil {
		t.Fatalf("Open(%s): %v", path, err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestMigrateV5FromV4PreservesData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kb.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE meta (k TEXT PRIMARY KEY, v TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if _, err := db.Exec(migrations[i]); err != nil {
			t.Fatalf("apply v%d: %v", i+1, err)
		}
	}
	stamp := "2026-07-31T12:00:00Z"
	statements := []string{
		`INSERT INTO meta(k,v) VALUES ('schema_version','4'), ('board_title:alice','Legacy Board')`,
		`INSERT INTO tasks(id,user,title,status,prio,tags,checks,created_at,moved_at) VALUES ('legacy-task','alice','Keep me','todo',3,'["legacy"]','null','` + stamp + `','` + stamp + `')`,
		`INSERT INTO settings(user,ai_base_url,ai_model,ai_key_enc) VALUES ('alice','https://example.test/v1','model',x'010203')`,
		`INSERT INTO import_links(scope,source,kind,external_key,link,url,title,imported_at,baseline_title,baseline_hash,baseline_excerpt,baseline_at) VALUES ('alice','src','github','key','link','https://example.test','Issue','` + stamp + `','base','hash','excerpt','` + stamp + `')`,
		`INSERT INTO tombstones(scope,task_id,reason,killed_at) VALUES ('alice','legacy-task','obsolete','` + stamp + `')`,
		`INSERT INTO labels(user,label,last_used) VALUES ('alice','legacy',41)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("seed v4: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s := openStoreAt(t, path)
	snapshot, err := s.ReadBoardSnapshot("alice")
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Exists || snapshot.Board.Title != "Legacy Board" || len(snapshot.Board.Tasks) != 1 || snapshot.Board.Tasks[0].ID != "legacy-task" {
		t.Fatalf("migrated snapshot = %+v", snapshot)
	}
	var version, model, externalKey, reason string
	var key []byte
	if err := s.db.QueryRow(`SELECT v FROM meta WHERE k='schema_version'`).Scan(&version); err != nil || version != "6" {
		t.Fatalf("version=%q err=%v", version, err)
	}
	if err := s.db.QueryRow(`SELECT ai_model, ai_key_enc FROM settings WHERE user='alice'`).Scan(&model, &key); err != nil || model != "model" || !reflect.DeepEqual(key, []byte{1, 2, 3}) {
		t.Fatalf("settings model=%q key=%v err=%v", model, key, err)
	}
	if err := s.db.QueryRow(`SELECT external_key FROM import_links WHERE scope='alice'`).Scan(&externalKey); err != nil || externalKey != "key" {
		t.Fatalf("import=%q err=%v", externalKey, err)
	}
	if err := s.db.QueryRow(`SELECT reason FROM tombstones WHERE scope='alice'`).Scan(&reason); err != nil || reason != "obsolete" {
		t.Fatalf("tombstone=%q err=%v", reason, err)
	}
	var seq int64
	if err := s.db.QueryRow(`SELECT value FROM label_sequence WHERE id=1`).Scan(&seq); err != nil || seq != 41 {
		t.Fatalf("label sequence=%d err=%v", seq, err)
	}
}

func TestConcurrentOpenSerializesMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kb.db")
	start := make(chan struct{})
	const openers = 4
	errs := make(chan error, openers)
	var wg sync.WaitGroup
	for i := 0; i < openers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			s, err := Open(path, []byte("test-secret"))
			if err == nil {
				err = s.Close()
			}
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Open: %v", err)
		}
	}
	s := openStoreAt(t, path)
	var version string
	if err := s.db.QueryRow(`SELECT v FROM meta WHERE k='schema_version'`).Scan(&version); err != nil || version != "6" {
		t.Fatalf("version=%q err=%v", version, err)
	}
}

func TestBoardRevisionCASAcrossHandles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kb.db")
	a := openStoreAt(t, path)
	b := openStoreAt(t, path)
	seed := board.Board{Title: "Board", Tasks: []board.Task{{Title: "Seed", Status: board.StatusTodo, Prio: 3}}}
	if err := a.ReplaceBoard("u", seed); err != nil {
		t.Fatal(err)
	}
	before, err := a.ReadBoardSnapshot("u")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.AddTask("u", board.Task{Title: "Intervening"}); err != nil {
		t.Fatal(err)
	}
	_, _, err = a.ReplaceBoardIfRevision("u", board.Board{Title: "Stale", Tasks: seed.Tasks}, nil, before.Revision)
	var conflict *RevisionConflictError
	if !errors.As(err, &conflict) || conflict.CurrentRevision <= before.Revision {
		t.Fatalf("CAS err=%v conflict=%+v before=%d", err, conflict, before.Revision)
	}
	after, err := a.ReadBoardSnapshot("u")
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Board.Tasks) != 2 || after.Board.Title != "Board" {
		t.Fatalf("stale replacement changed board: %+v", after.Board)
	}
}

func TestConditionalCanonicalReplacement(t *testing.T) {
	s := newStore(t)
	if err := s.ReplaceBoard("u", board.Board{Title: "B", Tasks: []board.Task{{Title: "Old", Status: board.StatusTodo, Prio: 3}}}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.ReadBoardSnapshot("u")
	if err != nil {
		t.Fatal(err)
	}
	id := snapshot.TaskIDs[0]
	ids, revision, err := s.ReplaceBoardIfRevision("u", board.Board{Title: "B2", Tasks: []board.Task{
		{Title: "Renamed", Status: board.StatusDoing, Prio: 2},
		{Title: "New", Status: board.StatusTodo, Prio: 3},
	}}, []*string{&id, nil}, snapshot.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != id || ids[1] == "" || ids[1] == id || revision <= snapshot.Revision {
		t.Fatalf("ids=%v revision=%d snapshot=%+v", ids, revision, snapshot)
	}
	current, err := s.ReadBoardSnapshot("u")
	if err != nil {
		t.Fatal(err)
	}
	bad := "not-a-uuid"
	if _, err := s.AddTask("u", board.Task{Title: "Intervening"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ReplaceBoardIfRevision("u", current.Board, []*string{&bad, nil}, current.Revision); !errors.As(err, new(*RevisionConflictError)) {
		t.Fatalf("stale invalid IDs err=%v, want revision conflict first", err)
	}
	current, err = s.ReadBoardSnapshot("u")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ReplaceBoardIfRevision("u", current.Board, []*string{&bad, nil}, current.Revision); !errors.Is(err, ErrInvalidTaskIDs) {
		t.Fatalf("invalid ID err=%v", err)
	}
	unchanged, _ := s.ReadBoardSnapshot("u")
	if unchanged.Revision != current.Revision {
		t.Fatalf("invalid IDs changed revision: %d -> %d", current.Revision, unchanged.Revision)
	}
}

func TestReplaceBoardIfExistsUsesTransactionalExistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kb.db")
	a := openStoreAt(t, path)
	b := openStoreAt(t, path)
	if err := a.ReplaceBoard("u", board.Board{Title: "B", Tasks: []board.Task{{Title: "Seed", Status: board.StatusTodo, Prio: 3}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.AddTask("u", board.Task{Title: "Concurrent"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.ReplaceBoardIfExists("u", board.Board{Title: "Star", Tasks: []board.Task{{Title: "Replacement", Status: board.StatusTodo, Prio: 3}}}, nil); err != nil {
		t.Fatalf("existing board after concurrent mutation: %v", err)
	}
	current, err := a.ReadBoardSnapshot("u")
	if err != nil || current.Board.Title != "Star" {
		t.Fatalf("replacement = %+v err=%v", current, err)
	}
	beforeDelete, err := a.ReadBoardSnapshot("u")
	if err != nil {
		t.Fatal(err)
	}
	if err := b.DeleteBoard("u"); err != nil {
		t.Fatal(err)
	}
	afterDelete, err := a.ReadBoardSnapshot("u")
	if err != nil || afterDelete.Exists || afterDelete.Revision <= beforeDelete.Revision {
		t.Fatalf("deleted board = %+v err=%v, before revision=%d", afterDelete, err, beforeDelete.Revision)
	}
	if _, _, err := a.ReplaceBoardIfExists("u", board.Board{Title: "No", Tasks: nil}, nil); !errors.As(err, new(*RevisionConflictError)) {
		t.Fatalf("missing wildcard replacement err=%v, want conflict", err)
	}
}

func TestBoardWriteReceiptIsAtomicScopedAndReplaySafe(t *testing.T) {
	s := newStore(t)
	seed := board.Board{Title: "B", Tasks: []board.Task{{Title: "Seed", Status: board.StatusTodo, Prio: 3}}}
	if err := s.ReplaceBoard("alice", seed); err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.ReadBoardSnapshot("alice")
	if err != nil {
		t.Fatal(err)
	}
	id := snapshot.TaskIDs[0]
	next := board.Board{Title: "B", Tasks: []board.Task{
		{Title: "Seed", Status: board.StatusTodo, Prio: 3},
		{Title: "New", Status: board.StatusTodo, Prio: 3},
	}}
	ids, revision, replayed, err := s.ReplaceBoardIfRevisionWithReceipt("alice", next, []*string{&id, nil}, snapshot.Revision, "op", "hash-a")
	if err != nil || replayed || len(ids) != 2 {
		t.Fatalf("commit ids=%v rev=%d replay=%v err=%v", ids, revision, replayed, err)
	}
	replayedIDs, replayedRevision, replayed, err := s.ReplaceBoardIfRevisionWithReceipt("alice", next, []*string{&id, nil}, snapshot.Revision, "op", "hash-a")
	if err != nil || !replayed || replayedRevision != revision || !reflect.DeepEqual(replayedIDs, ids) {
		t.Fatalf("replay ids=%v rev=%d replay=%v err=%v", replayedIDs, replayedRevision, replayed, err)
	}
	if _, _, _, err := s.ReplaceBoardIfRevisionWithReceipt("alice", next, []*string{&id, nil}, revision, "op", "hash-b"); !errors.Is(err, ErrInvalidTaskIDs) {
		t.Fatalf("mismatched replay err=%v", err)
	}
	if _, found, err := s.BoardWriteReceipt("bob", "op"); err != nil || found {
		t.Fatalf("cross-user receipt found=%v err=%v", found, err)
	}
}

func TestConcurrentBoardWriteReceiptClosesDuplicateCreateRace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kb.db")
	a := openStoreAt(t, path)
	b := openStoreAt(t, path)
	if err := a.ReplaceBoard("u", board.Board{Title: "B"}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := a.ReadBoardSnapshot("u")
	if err != nil {
		t.Fatal(err)
	}
	next := board.Board{Title: "B", Tasks: []board.Task{{Title: "New", Status: board.StatusTodo, Prio: 3}}}
	type result struct {
		ids      []string
		revision int64
		replayed bool
		err      error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for _, handle := range []*Store{a, b} {
		go func(s *Store) {
			<-start
			ids, revision, replayed, err := s.ReplaceBoardIfRevisionWithReceipt("u", next, []*string{nil}, snapshot.Revision, "same-op", "same-hash")
			results <- result{ids, revision, replayed, err}
		}(handle)
	}
	close(start)
	first, second := <-results, <-results
	if first.err != nil || second.err != nil {
		t.Fatalf("concurrent receipts err=%v/%v", first.err, second.err)
	}
	if first.replayed == second.replayed || first.revision != second.revision || !reflect.DeepEqual(first.ids, second.ids) {
		t.Fatalf("concurrent receipts = %+v / %+v", first, second)
	}
	current, err := a.ReadBoardSnapshot("u")
	if err != nil || len(current.TaskIDs) != 1 || current.TaskIDs[0] != first.ids[0] {
		t.Fatalf("current = %+v err=%v", current, err)
	}
}

func TestSnapshotIsUntornAcrossWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kb.db")
	reader := openStoreAt(t, path)
	writer := openStoreAt(t, path)
	if err := reader.ReplaceBoard("u", board.Board{Title: "Before", Tasks: []board.Task{{Title: "A", Status: board.StatusTodo, Prio: 3}}}); err != nil {
		t.Fatal(err)
	}
	before, _ := reader.ReadBoardSnapshot("u")
	snapshot, err := reader.readBoardSnapshot("u", func() {
		if _, err := writer.AddTask("u", board.Task{Title: "B"}); err != nil {
			t.Fatalf("intervening writer: %v", err)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Revision != before.Revision || len(snapshot.Board.Tasks) != 1 || len(snapshot.TaskIDs) != 1 {
		t.Fatalf("torn snapshot: before=%+v got=%+v", before, snapshot)
	}
	after, _ := reader.ReadBoardSnapshot("u")
	if after.Revision <= snapshot.Revision || len(after.Board.Tasks) != 2 {
		t.Fatalf("writer did not become visible after snapshot: %+v", after)
	}
}

func TestRevisionTriggersObserveLegacyWriters(t *testing.T) {
	s := newStore(t)
	revision := func() int64 {
		t.Helper()
		var value int64
		if err := s.db.QueryRow(`SELECT revision FROM board_revisions WHERE user='u'`).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	assertAdvanced := func(before int64) int64 {
		t.Helper()
		after := revision()
		if after <= before {
			t.Fatalf("revision did not advance: %d -> %d", before, after)
		}
		return after
	}
	if _, err := s.db.Exec(`INSERT INTO meta(k,v) VALUES (?,?)`, titleKey("u"), "One"); err != nil {
		t.Fatal(err)
	}
	r := revision()
	if _, err := s.db.Exec(`UPDATE meta SET v='Two' WHERE k=?`, titleKey("u")); err != nil {
		t.Fatal(err)
	}
	r = assertAdvanced(r)
	task, err := s.AddTask("u", board.Task{Title: "Task"})
	if err != nil {
		t.Fatal(err)
	}
	r = assertAdvanced(r)
	if _, err := s.UpdateTask("u", task.ID, TaskPatch{Title: sptr("Updated")}); err != nil {
		t.Fatal(err)
	}
	r = assertAdvanced(r)
	if _, err := s.MoveTask("u", task.ID, board.StatusDoing); err != nil {
		t.Fatal(err)
	}
	r = assertAdvanced(r)
	if _, err := s.DeleteTask("u", task.ID); err != nil {
		t.Fatal(err)
	}
	r = assertAdvanced(r)
	if _, err := s.db.Exec(`DELETE FROM meta WHERE k=?`, titleKey("u")); err != nil {
		t.Fatal(err)
	}
	assertAdvanced(r)
}

func TestRevisionTriggersObserveOwnerAndTitleKeyRenames(t *testing.T) {
	s := newStore(t)
	revision := func(user string) int64 {
		t.Helper()
		var value int64
		if err := s.db.QueryRow(`SELECT revision FROM board_revisions WHERE user=?`, user).Scan(&value); err != nil {
			t.Fatalf("revision(%s): %v", user, err)
		}
		return value
	}

	if _, err := s.db.Exec(`INSERT INTO meta(k,v) VALUES (?,?)`, titleKey("old-title"), "Old"); err != nil {
		t.Fatal(err)
	}
	oldTitleBefore := revision("old-title")
	if _, err := s.db.Exec(`UPDATE meta SET k=? WHERE k=?`, titleKey("new-title"), titleKey("old-title")); err != nil {
		t.Fatal(err)
	}
	if revision("old-title") <= oldTitleBefore || revision("new-title") == 0 {
		t.Fatal("title-key rename did not advance both owners")
	}

	if _, err := s.db.Exec(`INSERT INTO meta(k,v) VALUES ('ordinary-key','value')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE meta SET k=? WHERE k='ordinary-key'`, titleKey("promoted")); err != nil {
		t.Fatal(err)
	}
	if revision("promoted") == 0 {
		t.Fatal("non-title to title rename did not create a revision")
	}

	stamp := "2026-07-31T12:00:00Z"
	if _, err := s.db.Exec(`INSERT INTO tasks(id,user,title,status,prio,tags,checks,created_at,moved_at) VALUES ('owner-move','from-user','Task','todo',3,'null','null',?,?)`, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	fromBefore := revision("from-user")
	if _, err := s.db.Exec(`UPDATE tasks SET user='to-user' WHERE id='owner-move'`); err != nil {
		t.Fatal(err)
	}
	if revision("from-user") <= fromBefore || revision("to-user") == 0 {
		t.Fatal("task ownership reassignment did not advance both owners")
	}
}

func TestSQLiteFilesArePrivate(t *testing.T) {
	dir := t.TempDir()
	oldUmask := syscall.Umask(0o022)
	defer syscall.Umask(oldUmask)
	path := filepath.Join(dir, "kb.db")
	s := openStoreAt(t, path)
	if _, err := s.AddTask("u", board.Task{Title: "create WAL"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{path, path + "-wal", path + "-shm"} {
		info, err := os.Stat(name)
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if got := info.Mode().Perm(); got&0o077 != 0 {
			t.Errorf("%s mode=%#o, want no group/world permissions", name, got)
		}
	}
}

func TestLabelMRUAcrossHandles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kb.db")
	a := openStoreAt(t, path)
	b := openStoreAt(t, path)
	start := make(chan struct{})
	errs := make(chan error, 2)
	go func() {
		<-start
		_, err := a.AddTask("u", board.Task{Title: "A", Tags: []string{"alpha"}})
		errs <- err
	}()
	go func() { <-start; _, err := b.AddTask("u", board.Task{Title: "B", Tags: []string{"beta"}}); errs <- err }()
	close(start)
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	rows, err := a.db.Query(`SELECT label, last_used FROM labels WHERE user='u' ORDER BY last_used DESC`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var labels []string
	var sequences []int64
	for rows.Next() {
		var label string
		var sequence int64
		if err := rows.Scan(&label, &sequence); err != nil {
			t.Fatal(err)
		}
		labels = append(labels, label)
		sequences = append(sequences, sequence)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(labels) != 2 || sequences[0] == sequences[1] || sequences[0] < sequences[1] {
		t.Fatalf("labels=%v sequences=%v", labels, sequences)
	}
	got, err := a.Labels("u")
	if err != nil || !reflect.DeepEqual(got, labels) {
		t.Fatalf("Labels=%v err=%v want=%v", got, err, labels)
	}
}

func TestDoneGuardCannotRaceConcurrentBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kb.db")
	a := openStoreAt(t, path)
	b := openStoreAt(t, path)
	task, err := a.AddTask("u", board.Task{Title: "Race"})
	if err != nil {
		t.Fatal(err)
	}
	guardEntered := make(chan struct{})
	continueGuard := make(chan struct{})
	moveErr := make(chan error, 1)
	done := board.StatusDone
	var firstGuard sync.Once
	go func() {
		_, err := a.UpdateAndMoveTask("u", task.ID, TaskPatch{}, &done, func(task board.Task) error {
			firstGuard.Do(func() {
				// The first attempt pauses at the former read/move race boundary.
				close(guardEntered)
				<-continueGuard
			})
			if task.Blocked {
				return errors.New("blocked")
			}
			return nil
		})
		moveErr <- err
	}()
	<-guardEntered
	blocked := true
	if _, err := b.UpdateTask("u", task.ID, TaskPatch{Blocked: &blocked}); err != nil {
		t.Fatal(err)
	}
	close(continueGuard)
	if err := <-moveErr; err == nil {
		t.Fatal("stale guarded move unexpectedly committed")
	}
	stored, err := a.ListTasks("u", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || stored[0].Status != board.StatusTodo || !stored[0].Blocked {
		t.Fatalf("final task=%+v", stored)
	}
}

func TestRevisionConflictErrorCarriesCurrentRevision(t *testing.T) {
	err := &RevisionConflictError{CurrentRevision: 7}
	if err.Error() == "" || err.CurrentRevision != 7 {
		t.Fatal(err)
	}
}
