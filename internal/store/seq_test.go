package store

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/board"
)

func TestMigrateV7BackfillsSequences(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kb.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE meta (k TEXT PRIMARY KEY, v TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if _, err := db.Exec(migrations[i]); err != nil {
			t.Fatalf("apply v%d: %v", i+1, err)
		}
	}
	// alice: creation order is b (oldest), then a and c sharing a timestamp
	// (rowid breaks the tie: a before c). bob: one task of his own, proving
	// per-board numbering.
	statements := []string{
		`INSERT INTO meta(k,v) VALUES ('schema_version','6')`,
		`INSERT INTO tasks(id,user,title,status,prio,tags,checks,created_at,moved_at) VALUES
			('task-a','alice','Second','todo',3,'null','null','2026-07-02T00:00:00Z','2026-07-02T00:00:00Z'),
			('task-b','alice','First','done',3,'null','null','2026-07-01T00:00:00Z','2026-07-01T00:00:00Z'),
			('task-c','alice','Third','todo',3,'null','null','2026-07-02T00:00:00Z','2026-07-02T00:00:00Z'),
			('task-d','bob','Only','todo',3,'null','null','2026-07-03T00:00:00Z','2026-07-03T00:00:00Z')`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("seed v6: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s := openStoreAt(t, path)
	wantSeq := map[string]int{"task-b": 1, "task-a": 2, "task-c": 3, "task-d": 1}
	for id, want := range wantSeq {
		var got int
		if err := s.db.QueryRow(`SELECT seq FROM tasks WHERE id = ?`, id).Scan(&got); err != nil || got != want {
			t.Errorf("seq(%s) = %d, %v; want %d", id, got, err, want)
		}
	}
	var next int
	if err := s.db.QueryRow(`SELECT next FROM board_sequences WHERE user = 'alice'`).Scan(&next); err != nil || next != 4 {
		t.Fatalf("alice counter = %d, %v; want 4", next, err)
	}

	// The counter picks up where the backfill left off.
	added, err := s.AddTask("alice", board.Task{Title: "Fourth"})
	if err != nil || added.Seq != 4 {
		t.Fatalf("post-migration add seq = %d, %v; want 4", added.Seq, err)
	}
}

func TestSequenceNumbersAreNeverReused(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "kb.db"), []byte("test-secret"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	var ids []string
	for i, title := range []string{"one", "two", "three"} {
		added, err := s.AddTask("u", board.Task{Title: title})
		if err != nil || added.Seq != i+1 {
			t.Fatalf("add %q seq = %d, %v; want %d", title, added.Seq, err, i+1)
		}
		ids = append(ids, added.ID)
	}
	if _, err := s.DeleteTask("u", ids[2]); err != nil {
		t.Fatal(err)
	}
	added, err := s.AddTask("u", board.Task{Title: "four"})
	if err != nil || added.Seq != 4 {
		t.Fatalf("post-delete add seq = %d, %v; want 4 (never reuse #3)", added.Seq, err)
	}

	// Addressing by sequence number, with and without the '#'.
	patched, err := s.UpdateTask("u", "2", TaskPatch{})
	if err != nil || patched.ID != ids[1] {
		t.Fatalf("resolve \"2\" = %s, %v; want %s", patched.ID, err, ids[1])
	}
	patched, err = s.UpdateTask("u", "#1", TaskPatch{})
	if err != nil || patched.ID != ids[0] {
		t.Fatalf("resolve \"#1\" = %s, %v; want %s", patched.ID, err, ids[0])
	}
	if _, err := s.UpdateTask("u", "3", TaskPatch{}); err != ErrNotFound {
		t.Fatalf("resolve deleted seq = %v, want ErrNotFound", err)
	}
}

func TestReplaceBoardPreservesSequences(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "kb.db"), []byte("test-secret"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	first, err := s.AddTask("u", board.Task{Title: "Keep"})
	if err != nil {
		t.Fatal(err)
	}
	b := board.Board{Title: "B", Tasks: []board.Task{
		{Title: "Keep", Status: board.StatusTodo, Prio: 3},
		{Title: "New", Status: board.StatusTodo, Prio: 3},
	}}
	keep := first.ID
	ids, err := s.ReplaceBoardWithTaskIDs("u", b)
	_ = keep
	if err != nil || len(ids) != 2 {
		t.Fatalf("replace: ids=%v err=%v", ids, err)
	}
	tasks, err := s.ListTasks("u", "")
	if err != nil || len(tasks) != 2 {
		t.Fatalf("list: %v, %v", tasks, err)
	}
	seqByTitle := map[string]int{}
	for _, task := range tasks {
		seqByTitle[task.Title] = task.Seq
	}
	// "Keep" was matched by title to the existing row and keeps #1; "New"
	// is numbered next, never colliding.
	if seqByTitle["Keep"] != 1 || seqByTitle["New"] != 2 {
		t.Fatalf("seq after replace = %v, want Keep:1 New:2", seqByTitle)
	}
}

func TestParseSeqRef(t *testing.T) {
	cases := []struct {
		ref  string
		want int
		ok   bool
	}{
		{"12", 12, true},
		{"#12", 12, true},
		{"1", 1, true},
		{"0", 0, false},
		{"", 0, false},
		{"#", 0, false},
		{"12a", 0, false},
		{"1e", 0, false},
		{"##1", 0, false},
		{"-3", 0, false},
	}
	for _, tt := range cases {
		if got, ok := parseSeqRef(tt.ref); got != tt.want || ok != tt.ok {
			t.Errorf("parseSeqRef(%q) = %d, %v; want %d, %v", tt.ref, got, ok, tt.want, tt.ok)
		}
	}
}
