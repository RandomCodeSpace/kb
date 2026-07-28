package store

import (
	"bytes"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RandomCodeSpace/kb/internal/board"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "kb.db"), []byte("test-secret"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func sptr(s string) *string                  { return &s }
func iptr(i int) *int                        { return &i }
func tags(v ...string) *[]string             { return &v }
func checks(v ...board.Check) *[]board.Check { return &v }

func TestBoardRoundTrip(t *testing.T) {
	s := newStore(t)
	in := board.Board{Title: "My Board", Tasks: []board.Task{
		{Emoji: "🚀", Title: "Ship it", Desc: "line1\nline2", Status: board.StatusTodo, Prio: 1,
			Due: "2026-08-01", Effort: "M", Tags: []string{"backend", "type::bug"},
			Checks: []board.Check{{Text: "step one", Done: true}, {Text: "step two"}}},
		{Title: "Second", Status: board.StatusTodo, Prio: 3},
		{Title: "In flight", Status: board.StatusDoing, Prio: 3},
		{Title: "Landed", Status: board.StatusDone, Prio: 2},
	}}
	if err := s.ReplaceBoard("u", in); err != nil {
		t.Fatalf("ReplaceBoard: %v", err)
	}
	got, err := s.Board("u")
	if err != nil {
		t.Fatalf("Board: %v", err)
	}
	if got.Title != "My Board" {
		t.Errorf("title = %q, want %q", got.Title, "My Board")
	}
	if len(got.Tasks) != 4 {
		t.Fatalf("len(tasks) = %d, want 4", len(got.Tasks))
	}
	wantOrder := []struct {
		title string
		st    board.Status
		pos   int
	}{
		{"Ship it", board.StatusTodo, 0},
		{"Second", board.StatusTodo, 1},
		{"In flight", board.StatusDoing, 0},
		{"Landed", board.StatusDone, 0},
	}
	for i, w := range wantOrder {
		g := got.Tasks[i]
		if g.Title != w.title || g.Status != w.st || g.Position != w.pos {
			t.Errorf("task[%d] = (%q, %s, %d), want (%q, %s, %d)", i, g.Title, g.Status, g.Position, w.title, w.st, w.pos)
		}
		if g.ID == "" || g.CreatedAt.IsZero() || g.MovedAt.IsZero() {
			t.Errorf("task[%d] missing identity: id=%q created=%v moved=%v", i, g.ID, g.CreatedAt, g.MovedAt)
		}
	}
	g := got.Tasks[0]
	w := in.Tasks[0]
	if g.Emoji != w.Emoji || g.Desc != w.Desc || g.Prio != w.Prio || g.Due != w.Due || g.Effort != w.Effort {
		t.Errorf("task fields = %+v, want %+v", g, w)
	}
	if !reflect.DeepEqual(g.Tags, w.Tags) || !reflect.DeepEqual(g.Checks, w.Checks) {
		t.Errorf("tags/checks = %v/%v, want %v/%v", g.Tags, g.Checks, w.Tags, w.Checks)
	}
	// Empty board for another user stays independent.
	other, err := s.Board("someone-else")
	if err != nil {
		t.Fatalf("Board(other): %v", err)
	}
	if other.Title != "Board" || len(other.Tasks) != 0 {
		t.Errorf("other board = %+v, want empty default", other)
	}
}

func TestReplaceBoardPreservesIdentity(t *testing.T) {
	s := newStore(t)
	seed := board.Board{Title: "B", Tasks: []board.Task{
		{Title: "Alpha", Status: board.StatusTodo, Prio: 3},
		{Title: "Beta", Status: board.StatusDoing, Prio: 3},
		{Title: "Gamma", Status: board.StatusTodo, Prio: 3},
	}}
	if err := s.ReplaceBoard("u", seed); err != nil {
		t.Fatalf("seed ReplaceBoard: %v", err)
	}
	before, err := s.Board("u")
	if err != nil {
		t.Fatalf("Board: %v", err)
	}
	byTitle := map[string]board.Task{}
	for _, task := range before.Tasks {
		byTitle[task.Title] = task
	}

	time.Sleep(5 * time.Millisecond)
	next := board.Board{Title: "B", Tasks: []board.Task{
		{Title: "Alpha", Desc: "edited", Status: board.StatusTodo, Prio: 3}, // same status: identity + MovedAt kept
		{Title: "Beta", Status: board.StatusDone, Prio: 3},                  // moved column: MovedAt bumped
		{Title: "Delta", Status: board.StatusTodo, Prio: 3},                 // brand new
	}} // Gamma dropped
	if err := s.ReplaceBoard("u", next); err != nil {
		t.Fatalf("ReplaceBoard: %v", err)
	}
	after, err := s.Board("u")
	if err != nil {
		t.Fatalf("Board: %v", err)
	}
	if len(after.Tasks) != 3 {
		t.Fatalf("len(tasks) = %d, want 3", len(after.Tasks))
	}
	got := map[string]board.Task{}
	for _, task := range after.Tasks {
		got[task.Title] = task
	}
	if _, ok := got["Gamma"]; ok {
		t.Error("Gamma should have been deleted")
	}
	a, b0 := got["Alpha"], byTitle["Alpha"]
	if a.ID != b0.ID || !a.CreatedAt.Equal(b0.CreatedAt) || !a.MovedAt.Equal(b0.MovedAt) {
		t.Errorf("Alpha identity changed: %+v vs %+v", a, b0)
	}
	if a.Desc != "edited" {
		t.Errorf("Alpha desc = %q, want %q", a.Desc, "edited")
	}
	bt, bb := got["Beta"], byTitle["Beta"]
	if bt.ID != bb.ID || !bt.CreatedAt.Equal(bb.CreatedAt) {
		t.Errorf("Beta identity changed: %+v vs %+v", bt, bb)
	}
	if bt.Status != board.StatusDone {
		t.Errorf("Beta status = %s, want done", bt.Status)
	}
	if !bt.MovedAt.After(bb.MovedAt) {
		t.Errorf("Beta MovedAt not bumped: %v <= %v", bt.MovedAt, bb.MovedAt)
	}
	d := got["Delta"]
	if d.ID == "" || d.ID == b0.ID || d.ID == bb.ID || d.ID == byTitle["Gamma"].ID {
		t.Errorf("Delta should have a fresh ID, got %q", d.ID)
	}
}

func TestTaskCRUD(t *testing.T) {
	s := newStore(t)
	t1, err := s.AddTask("u", board.Task{Title: "First"})
	if err != nil {
		t.Fatalf("AddTask: %v", err)
	}
	if t1.ID == "" || t1.Status != board.StatusTodo || t1.Prio != 3 || t1.Position != 0 {
		t.Errorf("t1 = %+v, want defaults todo/3/pos0", t1)
	}
	t2, err := s.AddTask("u", board.Task{Title: "Second", Status: board.StatusTodo})
	if err != nil {
		t.Fatalf("AddTask: %v", err)
	}
	if t2.Position != 1 {
		t.Errorf("t2.Position = %d, want 1", t2.Position)
	}

	up, err := s.UpdateTask("u", t1.ID, TaskPatch{
		Desc:   sptr("details"),
		Prio:   iptr(2),
		Tags:   tags("x"),
		Checks: checks(board.Check{Text: "c1"}),
	})
	if err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	if up.Desc != "details" || up.Prio != 2 || up.Title != "First" {
		t.Errorf("updated = %+v", up)
	}
	if !reflect.DeepEqual(up.Tags, []string{"x"}) || len(up.Checks) != 1 {
		t.Errorf("updated tags/checks = %v/%v", up.Tags, up.Checks)
	}
	if _, err := s.UpdateTask("u", t1.ID, TaskPatch{Prio: iptr(9)}); err == nil {
		t.Error("UpdateTask with prio 9 should fail")
	}

	time.Sleep(2 * time.Millisecond)
	mv, err := s.MoveTask("u", t1.ID[:8], board.StatusDoing)
	if err != nil {
		t.Fatalf("MoveTask: %v", err)
	}
	if mv.Status != board.StatusDoing || mv.Position != 0 {
		t.Errorf("moved = %+v", mv)
	}
	if !mv.MovedAt.After(t1.MovedAt) {
		t.Errorf("MovedAt not bumped: %v <= %v", mv.MovedAt, t1.MovedAt)
	}

	doing, err := s.ListTasks("u", board.StatusDoing)
	if err != nil || len(doing) != 1 || doing[0].ID != t1.ID {
		t.Errorf("ListTasks(doing) = %v, %v", doing, err)
	}
	all, err := s.ListTasks("u", "")
	if err != nil || len(all) != 2 {
		t.Errorf("ListTasks(all) = %v, %v", all, err)
	}

	// User scoping: another user cannot see or delete these tasks.
	if _, err := s.DeleteTask("intruder", t1.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-user delete err = %v, want ErrNotFound", err)
	}
	del, err := s.DeleteTask("u", t2.ID)
	if err != nil || del.Title != "Second" {
		t.Fatalf("DeleteTask = %+v, %v", del, err)
	}
	all, err = s.ListTasks("u", "")
	if err != nil || len(all) != 1 {
		t.Errorf("after delete ListTasks(all) = %v, %v", all, err)
	}
}

// TestMigrateExistingDatabase opens a database left at schema v1 by an
// earlier release and checks the pending migrations run over it, giving the
// old row the blocked default rather than failing or losing data.
func TestMigrateExistingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kb.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS meta (k TEXT PRIMARY KEY, v TEXT NOT NULL)`,
		migrations[0],
		`INSERT INTO meta (k, v) VALUES ('schema_version', '1')`,
		`INSERT INTO tasks (id, user, title, status, created_at, moved_at) VALUES ('old', 'u', 'Legacy', 'todo', '` + stamp + `', '` + stamp + `')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("seed v1 schema: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	s, err := Open(path, []byte("test-secret"))
	if err != nil {
		t.Fatalf("Open on a v1 database: %v", err)
	}
	defer s.Close()
	got, err := s.ListTasks("u", "")
	if err != nil || len(got) != 1 {
		t.Fatalf("ListTasks = %v, %v, want the legacy task", got, err)
	}
	if got[0].Title != "Legacy" || got[0].Blocked {
		t.Errorf("migrated task = %+v, want Legacy and blocked=false", got[0])
	}
}

func TestBlockedFlag(t *testing.T) {
	s := newStore(t)
	t1, err := s.AddTask("u", board.Task{Title: "Waiting", Blocked: true})
	if err != nil {
		t.Fatalf("AddTask: %v", err)
	}
	if !t1.Blocked {
		t.Error("AddTask dropped the blocked flag")
	}
	t2, err := s.AddTask("u", board.Task{Title: "Free"})
	if err != nil {
		t.Fatalf("AddTask: %v", err)
	}
	if t2.Blocked {
		t.Error("default task came back blocked")
	}
	got, err := s.ListTasks("u", "")
	if err != nil || len(got) != 2 {
		t.Fatalf("ListTasks = %v, %v", got, err)
	}
	if !got[0].Blocked || got[1].Blocked {
		t.Errorf("blocked did not persist: %v, %v", got[0].Blocked, got[1].Blocked)
	}
	no := false
	un, err := s.UpdateTask("u", t1.ID, TaskPatch{Blocked: &no})
	if err != nil || un.Blocked {
		t.Fatalf("UpdateTask(blocked=false) = %+v, %v", un, err)
	}
	// A nil patch field leaves the flag alone.
	same, err := s.UpdateTask("u", t2.ID, TaskPatch{Desc: sptr("x")})
	if err != nil || same.Blocked {
		t.Fatalf("UpdateTask without Blocked = %+v, %v", same, err)
	}
	yes := true
	if re, err := s.UpdateTask("u", t2.ID, TaskPatch{Blocked: &yes}); err != nil || !re.Blocked {
		t.Fatalf("UpdateTask(blocked=true) = %+v, %v", re, err)
	}
	// The flag survives the markdown wire.
	b, err := s.Board("u")
	if err != nil {
		t.Fatalf("Board: %v", err)
	}
	wire := board.Parse(board.Serialize(b))
	if len(wire.Tasks) != 2 || wire.Tasks[0].Blocked || !wire.Tasks[1].Blocked {
		t.Errorf("blocked lost on the wire: %+v", wire.Tasks)
	}
}

func TestCancelledStatus(t *testing.T) {
	s := newStore(t)
	t1, err := s.AddTask("u", board.Task{Title: "Dropped", Status: board.StatusCancelled})
	if err != nil {
		t.Fatalf("AddTask(cancelled): %v", err)
	}
	if t1.Status != board.StatusCancelled {
		t.Errorf("status = %q, want cancelled", t1.Status)
	}
	t2, err := s.AddTask("u", board.Task{Title: "Live"})
	if err != nil {
		t.Fatalf("AddTask: %v", err)
	}
	if _, err := s.MoveTask("u", t2.ID, board.StatusCancelled); err != nil {
		t.Fatalf("MoveTask(cancelled): %v", err)
	}
	only, err := s.ListTasks("u", board.StatusCancelled)
	if err != nil || len(only) != 2 {
		t.Fatalf("ListTasks(cancelled) = %v, %v", only, err)
	}
	// Cancelled sorts after the other columns on the board.
	if _, err := s.AddTask("u", board.Task{Title: "Todo one"}); err != nil {
		t.Fatalf("AddTask: %v", err)
	}
	b, err := s.Board("u")
	if err != nil {
		t.Fatalf("Board: %v", err)
	}
	want := []board.Status{board.StatusTodo, board.StatusCancelled, board.StatusCancelled}
	for i, w := range want {
		if b.Tasks[i].Status != w {
			t.Errorf("task %d status = %q, want %q", i, b.Tasks[i].Status, w)
		}
	}
	if !strings.Contains(board.Serialize(b), "## Cancelled\n") {
		t.Error("cancelled section missing from the wire")
	}
}

func TestPrefixResolution(t *testing.T) {
	s := newStore(t)
	a, err := s.AddTask("u", board.Task{Title: "A"})
	if err != nil {
		t.Fatalf("AddTask: %v", err)
	}
	b, err := s.AddTask("u", board.Task{Title: "B"})
	if err != nil {
		t.Fatalf("AddTask: %v", err)
	}
	// Force known IDs so prefix outcomes are deterministic.
	for id, to := range map[string]string{a.ID: "task-aaa", b.ID: "task-abb"} {
		if _, err := s.db.Exec(`UPDATE tasks SET id = ? WHERE id = ?`, to, id); err != nil {
			t.Fatalf("rewrite id: %v", err)
		}
	}
	if _, err := s.MoveTask("u", "task-", board.StatusDone); !errors.Is(err, ErrAmbiguous) {
		t.Errorf("ambiguous prefix err = %v, want ErrAmbiguous", err)
	}
	if _, err := s.UpdateTask("u", "zzz", TaskPatch{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing prefix err = %v, want ErrNotFound", err)
	}
	if _, err := s.UpdateTask("u", "", TaskPatch{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("empty prefix err = %v, want ErrNotFound", err)
	}
	got, err := s.MoveTask("u", "task-aa", board.StatusDoing)
	if err != nil || got.ID != "task-aaa" {
		t.Errorf("unique prefix = %+v, %v, want id task-aaa", got, err)
	}
	del, err := s.DeleteTask("u", "task-ab")
	if err != nil || del.ID != "task-abb" {
		t.Errorf("unique prefix delete = %+v, %v, want id task-abb", del, err)
	}
}

func TestLabelsMRU(t *testing.T) {
	s := newStore(t)
	t1, err := s.AddTask("u", board.Task{Title: "One", Tags: []string{"alpha", "beta"}})
	if err != nil {
		t.Fatalf("AddTask: %v", err)
	}
	if _, err := s.AddTask("u", board.Task{Title: "Two", Tags: []string{"gamma"}}); err != nil {
		t.Fatalf("AddTask: %v", err)
	}
	if _, err := s.UpdateTask("u", t1.ID, TaskPatch{Tags: tags("alpha")}); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	got, err := s.Labels("u")
	if err != nil {
		t.Fatalf("Labels: %v", err)
	}
	want := []string{"alpha", "gamma", "beta"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Labels = %v, want %v", got, want)
	}
	other, err := s.Labels("someone-else")
	if err != nil || len(other) != 0 {
		t.Errorf("Labels(other) = %v, %v, want empty", other, err)
	}
}

func TestAISettings(t *testing.T) {
	s := newStore(t)
	st, err := s.AISettings("u")
	if err != nil || st != (AISettings{}) {
		t.Fatalf("empty AISettings = %+v, %v", st, err)
	}
	if _, err := s.SetAISettings("u", sptr("https://api.example"), sptr("model-x"), nil); err != nil {
		t.Fatalf("SetAISettings: %v", err)
	}
	st, err = s.AISettings("u")
	if err != nil || st.BaseURL != "https://api.example" || st.Model != "model-x" || st.HasKey {
		t.Fatalf("AISettings = %+v, %v", st, err)
	}
	if key, err := s.AIKey("u"); err != nil || key != "" {
		t.Fatalf("AIKey before set = %q, %v", key, err)
	}

	const secretKey = "sk-super-secret-123"
	if _, err := s.SetAISettings("u", nil, nil, sptr(secretKey)); err != nil {
		t.Fatalf("SetAISettings(key): %v", err)
	}
	st, err = s.AISettings("u")
	if err != nil || !st.HasKey || st.BaseURL != "https://api.example" || st.Model != "model-x" {
		t.Fatalf("AISettings after key = %+v, %v (nil patches must preserve fields)", st, err)
	}
	if key, err := s.AIKey("u"); err != nil || key != secretKey {
		t.Fatalf("AIKey = %q, %v, want %q", key, err, secretKey)
	}

	// The raw column must hold ciphertext, never the plaintext key.
	var enc []byte
	if err := s.db.QueryRow(`SELECT ai_key_enc FROM settings WHERE user = 'u'`).Scan(&enc); err != nil {
		t.Fatalf("raw column: %v", err)
	}
	if len(enc) == 0 {
		t.Fatal("ai_key_enc is empty")
	}
	if bytes.Contains(enc, []byte(secretKey)) {
		t.Error("ai_key_enc contains the plaintext key")
	}

	// Empty key clears.
	if _, err := s.SetAISettings("u", nil, nil, sptr("")); err != nil {
		t.Fatalf("SetAISettings(clear): %v", err)
	}
	st, err = s.AISettings("u")
	if err != nil || st.HasKey {
		t.Fatalf("AISettings after clear = %+v, %v", st, err)
	}
	if key, err := s.AIKey("u"); err != nil || key != "" {
		t.Fatalf("AIKey after clear = %q, %v", key, err)
	}
}

func TestBaseURLChangeClearsKey(t *testing.T) {
	s := newStore(t)
	if _, err := s.SetAISettings("u", sptr("https://api.example.com/v1"), sptr("m"), sptr("sk-1")); err != nil {
		t.Fatalf("seed SetAISettings: %v", err)
	}
	// Same scheme+host, different path: key kept.
	cleared, err := s.SetAISettings("u", sptr("https://api.example.com/other"), nil, nil)
	if err != nil || cleared {
		t.Fatalf("same-origin change: cleared=%v err=%v, want false, nil", cleared, err)
	}
	if st, err := s.AISettings("u"); err != nil || !st.HasKey {
		t.Fatalf("key lost on same-origin change: %+v, %v", st, err)
	}
	// Different host without a fresh key: stored key must not follow.
	cleared, err = s.SetAISettings("u", sptr("https://evil.example.net"), nil, nil)
	if err != nil || !cleared {
		t.Fatalf("cross-host change: cleared=%v err=%v, want true, nil", cleared, err)
	}
	if st, err := s.AISettings("u"); err != nil || st.HasKey {
		t.Fatalf("key survived cross-host change: %+v, %v", st, err)
	}
	if key, err := s.AIKey("u"); err != nil || key != "" {
		t.Fatalf("AIKey after cross-host change = %q, %v, want empty", key, err)
	}
	// Different host with a fresh key in the same call: the new key sticks.
	cleared, err = s.SetAISettings("u", sptr("https://api.example.org"), nil, sptr("sk-2"))
	if err != nil || cleared {
		t.Fatalf("change with fresh key: cleared=%v err=%v, want false, nil", cleared, err)
	}
	if key, err := s.AIKey("u"); err != nil || key != "sk-2" {
		t.Fatalf("AIKey = %q, %v, want sk-2", key, err)
	}
}

func TestTaskFieldValidation(t *testing.T) {
	s := newStore(t)
	bad := []board.Task{
		{Title: ""},
		{Title: "   "},
		{Title: "\ufeff"}, // whitespace to the wire's token splitter
		{Title: "innocent\n- [x] Forged task !1 #prod"},
		{Title: "x", Emoji: "🚀\n- [x] forged"},
		{Title: "x", Due: "soon"},
		{Title: "x", Due: "2026-13-45"},
		{Title: "x", Effort: "XL"},
		{Title: "x", Tags: []string{"two words"}},
		{Title: "x", Tags: []string{"#lead"}},
		{Title: "x", Tags: []string{""}},
		{Title: "x", Emoji: "ab"},
		{Title: "x", Checks: []board.Check{{Text: "line\nbreak"}}},
	}
	for _, tt := range bad {
		if _, err := s.AddTask("u", tt); err == nil {
			t.Errorf("AddTask(%+v) should fail", tt)
		}
	}
	ok, err := s.AddTask("u", board.Task{
		Title: "ok", Due: "2026-08-01", Effort: "M",
		Tags: []string{"env::prod"}, Emoji: "🚀",
	})
	if err != nil {
		t.Fatalf("valid AddTask: %v", err)
	}
	if _, err := s.UpdateTask("u", ok.ID, TaskPatch{Title: sptr("bad\ntitle")}); err == nil {
		t.Error("UpdateTask with newline title should fail")
	}
	if _, err := s.UpdateTask("u", ok.ID, TaskPatch{Title: sptr("  ")}); err == nil {
		t.Error("UpdateTask with blank title should fail")
	}
	if _, err := s.UpdateTask("u", ok.ID, TaskPatch{Due: sptr("nope")}); err == nil {
		t.Error("UpdateTask with invalid due should fail")
	}
	if _, err := s.UpdateTask("u", ok.ID, TaskPatch{Tags: tags("two words")}); err == nil {
		t.Error("UpdateTask with whitespace tag should fail")
	}
	// ReplaceBoard rejects line-injection but tolerates wire-shaped oddities
	// (it ingests board.Parse output, which never produces newlines).
	err = s.ReplaceBoard("u", board.Board{Title: "B", Tasks: []board.Task{
		{Title: "a\nb", Status: board.StatusTodo, Prio: 3},
	}})
	if err == nil {
		t.Error("ReplaceBoard with newline title should fail")
	}
	err = s.ReplaceBoard("u", board.Board{Title: "B", Tasks: []board.Task{
		{Title: "odd", Due: "2026-13-45", Status: board.StatusTodo, Prio: 3},
	}})
	if err != nil {
		t.Errorf("ReplaceBoard with wire-shaped odd due should succeed: %v", err)
	}
}

func TestImportMarkdownDir(t *testing.T) {
	s := newStore(t)
	dir := t.TempDir()
	alice := "# Alice Board\n\n## To Do\n\n- [ ] 🚀 Ship !1 @2026-08-01 ~M #backend\n  a desc line\n  - [ ] sub step\n\n## Doing\n\n- [ ] Working\n\n## Done\n\n- [x] Finished\n"
	bob := "# Bob Board\n\n## To Do\n\n- [ ] Bobs file task\n"
	for name, content := range map[string]string{"alice.md": alice, "bob.md": bob} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	// Bob already has tasks in the DB; his file must be skipped.
	if _, err := s.AddTask("bob", board.Task{Title: "Bobs live task"}); err != nil {
		t.Fatalf("AddTask: %v", err)
	}

	n, err := s.ImportMarkdownDir(dir)
	if err != nil {
		t.Fatalf("ImportMarkdownDir: %v", err)
	}
	if n != 1 {
		t.Fatalf("imported = %d, want 1", n)
	}
	ab, err := s.Board("alice")
	if err != nil {
		t.Fatalf("Board(alice): %v", err)
	}
	if ab.Title != "Alice Board" || len(ab.Tasks) != 3 {
		t.Fatalf("alice board = %+v, want title + 3 tasks", ab)
	}
	first := ab.Tasks[0]
	if first.Emoji != "🚀" || first.Title != "Ship" || first.Prio != 1 || first.Due != "2026-08-01" ||
		first.Effort != "M" || !reflect.DeepEqual(first.Tags, []string{"backend"}) ||
		first.Desc != "a desc line" || len(first.Checks) != 1 || first.ID == "" {
		t.Errorf("alice first task = %+v", first)
	}
	if ab.Tasks[2].Status != board.StatusDone {
		t.Errorf("alice done task status = %s", ab.Tasks[2].Status)
	}
	if labels, err := s.Labels("alice"); err != nil || !reflect.DeepEqual(labels, []string{"backend"}) {
		t.Errorf("alice labels = %v, %v", labels, err)
	}
	bobTasks, err := s.ListTasks("bob", "")
	if err != nil || len(bobTasks) != 1 || bobTasks[0].Title != "Bobs live task" {
		t.Errorf("bob tasks = %v, %v, want only the live task", bobTasks, err)
	}

	// Second run imports nothing and duplicates nothing.
	if n, err := s.ImportMarkdownDir(dir); err != nil || n != 0 {
		t.Errorf("second import = %d, %v, want 0", n, err)
	}
	if ab2, err := s.Board("alice"); err != nil || len(ab2.Tasks) != 3 {
		t.Errorf("alice tasks after reimport = %d, %v, want 3", len(ab2.Tasks), err)
	}
	// Even a now-empty board is never reimported.
	if _, err := s.db.Exec(`DELETE FROM tasks WHERE user = 'alice'`); err != nil {
		t.Fatalf("clear alice: %v", err)
	}
	if n, err := s.ImportMarkdownDir(dir); err != nil || n != 0 {
		t.Errorf("import after wipe = %d, %v, want 0 (never reimport)", n, err)
	}

	// Files stay untouched.
	got, err := os.ReadFile(filepath.Join(dir, "alice.md"))
	if err != nil || string(got) != alice {
		t.Errorf("alice.md modified or missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "bob.md")); err != nil {
		t.Errorf("bob.md missing: %v", err)
	}

	// Nonexistent dir is a no-op.
	if n, err := s.ImportMarkdownDir(filepath.Join(dir, "nope")); err != nil || n != 0 {
		t.Errorf("missing dir import = %d, %v, want 0, nil", n, err)
	}
}

func TestLoadOrCreateSecret(t *testing.T) {
	t.Run("env wins", func(t *testing.T) {
		t.Setenv("KB_SECRET", "env-secret-bytes")
		got, err := LoadOrCreateSecret(t.TempDir())
		if err != nil || !bytes.Equal(got, []byte("env-secret-bytes")) {
			t.Fatalf("LoadOrCreateSecret = %q, %v, want raw env bytes", got, err)
		}
	})
	t.Run("file created and stable", func(t *testing.T) {
		t.Setenv("KB_SECRET", "") // empty counts as unset
		dataDir := filepath.Join(t.TempDir(), "data")
		s1, err := LoadOrCreateSecret(dataDir)
		if err != nil {
			t.Fatalf("LoadOrCreateSecret: %v", err)
		}
		if len(s1) != 32 {
			t.Fatalf("secret length = %d, want 32", len(s1))
		}
		fi, err := os.Stat(filepath.Join(dataDir, "secret"))
		if err != nil {
			t.Fatalf("stat secret: %v", err)
		}
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Errorf("secret mode = %o, want 600", perm)
		}
		s2, err := LoadOrCreateSecret(dataDir)
		if err != nil || !bytes.Equal(s1, s2) {
			t.Fatalf("second call = %x, %v, want same as first %x", s2, err, s1)
		}
	})
	// A zero-byte secret derives the AES key from SHA-256("") — a key anyone
	// can compute. Refuse rather than use it, and refuse rather than
	// regenerate: a new secret would orphan whatever is already encrypted.
	t.Run("short or empty file refused", func(t *testing.T) {
		for _, n := range []int{0, 1, 31} {
			dataDir := t.TempDir()
			t.Setenv("KB_SECRET", "")
			path := filepath.Join(dataDir, "secret")
			if err := os.WriteFile(path, bytes.Repeat([]byte("x"), n), 0o600); err != nil {
				t.Fatalf("write %d-byte secret: %v", n, err)
			}
			got, err := LoadOrCreateSecret(dataDir)
			if err == nil {
				t.Errorf("%d-byte secret accepted as %q, want an error", n, got)
				continue
			}
			// The file is left alone so a backup can still be restored.
			if fi, statErr := os.Stat(path); statErr != nil || fi.Size() != int64(n) {
				t.Errorf("%d-byte secret file was rewritten (size=%v, err=%v)", n, fi, statErr)
			}
		}
	})
}

// captureStderr runs fn with os.Stderr redirected and returns what it wrote.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = w
	func() {
		defer func() {
			os.Stderr = orig
			w.Close()
		}()
		fn()
	}()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	return string(out)
}

// A short KB_SECRET must not be fatal here — kb (serve) refuses to boot with
// one, but making the shared path fail would lock a CLI or MCP user out of
// the AI key already encrypted under it. It has to be loud all the same, so
// every entry point reports the same weakness rather than only serve.
func TestLoadOrCreateSecretShortEnvSecret(t *testing.T) {
	resetWarning := func(t *testing.T) {
		warnShortSecretOnce = sync.Once{}
		t.Cleanup(func() { warnShortSecretOnce = sync.Once{} })
	}

	t.Run("warns once and keeps going", func(t *testing.T) {
		resetWarning(t)
		t.Setenv("KB_SECRET", "short")
		dir := t.TempDir()
		var got, again []byte
		var err1, err2 error
		out := captureStderr(t, func() {
			got, err1 = LoadOrCreateSecret(dir)
			again, err2 = LoadOrCreateSecret(dir)
		})
		if err1 != nil || err2 != nil {
			t.Fatalf("LoadOrCreateSecret errored on a short env secret: %v, %v", err1, err2)
		}
		if !bytes.Equal(got, []byte("short")) || !bytes.Equal(again, []byte("short")) {
			t.Errorf("secret = %q / %q, want the env value both times", got, again)
		}
		if n := strings.Count(out, "warning"); n != 1 {
			t.Errorf("want exactly one warning across both loads, got %d:\n%s", n, out)
		}
		if !strings.Contains(out, "KB_SECRET") {
			t.Errorf("warning does not name KB_SECRET: %q", out)
		}
	})

	t.Run("stays quiet at the threshold", func(t *testing.T) {
		resetWarning(t)
		t.Setenv("KB_SECRET", strings.Repeat("s", EnvSecretMinBytes))
		out := captureStderr(t, func() {
			if _, err := LoadOrCreateSecret(t.TempDir()); err != nil {
				t.Errorf("LoadOrCreateSecret: %v", err)
			}
		})
		if out != "" {
			t.Errorf("unexpected stderr output for a %d-byte secret: %q", EnvSecretMinBytes, out)
		}
	})
}
