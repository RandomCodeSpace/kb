package cliapp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// runCmd invokes Run capturing both streams.
func runCmd(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errb bytes.Buffer
	code = Run(args, &out, &errb)
	return out.String(), errb.String(), code
}

// localEnv forces local mode into a fresh temp data dir.
func localEnv(t *testing.T) string {
	t.Helper()
	t.Setenv("KB_SERVER", "")
	t.Setenv("KB_SERVER_TOKEN", "")
	t.Setenv("KB_SECRET", "test-secret")
	return t.TempDir()
}

// jsonTask mirrors the CLI's --json output shape.
type jsonTask struct {
	ID     string   `json:"id"`
	Emoji  string   `json:"emoji"`
	Title  string   `json:"title"`
	Desc   string   `json:"desc"`
	Status string   `json:"status"`
	Prio   int      `json:"prio"`
	Due    string   `json:"due"`
	Effort string   `json:"effort"`
	Tags   []string `json:"tags"`
	Checks []struct {
		Text string `json:"text"`
		Done bool   `json:"done"`
	} `json:"checks"`
	Position int `json:"position"`
}

// listJSON runs `kb list --json` and decodes the result.
func listJSON(t *testing.T, extra ...string) []jsonTask {
	t.Helper()
	args := append([]string{"list", "--json"}, extra...)
	out, errS, code := runCmd(t, args...)
	if code != 0 {
		t.Fatalf("list --json failed (code %d): %s", code, errS)
	}
	var tasks []jsonTask
	if err := json.Unmarshal([]byte(out), &tasks); err != nil {
		t.Fatalf("list --json output not valid JSON: %v\n%s", err, out)
	}
	return tasks
}

var addedRe = regexp.MustCompile(`^added [0-9a-f]{8} Write the docs\n$`)

func TestLocalLifecycle(t *testing.T) {
	dir := localEnv(t)

	out, errS, code := runCmd(t, "add", "Write the docs", "--data", dir,
		"--desc", "Cover the CLI", "--prio", "2", "--due", "2026-08-01",
		"--effort", "m", "--tag", "docs", "--tag", "cli", "--check", "outline", "--emoji", "\U0001F4DA")
	if code != 0 {
		t.Fatalf("add failed (code %d): %s", code, errS)
	}
	if !addedRe.MatchString(out) {
		t.Fatalf("add output = %q, want match of %s", out, addedRe)
	}

	if _, errS, code = runCmd(t, "add", "Fix login bug", "--data", dir, "--status", "doing", "--tag", "bug"); code != 0 {
		t.Fatalf("second add failed (code %d): %s", code, errS)
	}

	tasks := listJSON(t, "--data", dir)
	if len(tasks) != 2 {
		t.Fatalf("got %d tasks, want 2: %+v", len(tasks), tasks)
	}
	t1, t2 := tasks[0], tasks[1] // listing order: todo before doing
	if t1.Title != "Write the docs" || t1.Status != "todo" || t1.Prio != 2 ||
		t1.Due != "2026-08-01" || t1.Effort != "M" || t1.Emoji != "\U0001F4DA" ||
		t1.Desc != "Cover the CLI" {
		t.Errorf("task 1 fields wrong: %+v", t1)
	}
	if strings.Join(t1.Tags, ",") != "docs,cli" || len(t1.Checks) != 1 || t1.Checks[0].Text != "outline" || t1.Checks[0].Done {
		t.Errorf("task 1 tags/checks wrong: %+v", t1)
	}
	if t2.Title != "Fix login bug" || t2.Status != "doing" || t2.Prio != 3 {
		t.Errorf("task 2 fields wrong: %+v", t2)
	}

	// Golden table (ids substituted; widths from tabwriter minwidth 2,
	// tabwidth 4, padding 2).
	out, _, code = runCmd(t, "list", "--data", dir)
	if code != 0 {
		t.Fatalf("list failed: code %d", code)
	}
	want := "ID        STATUS  PRIO  TITLE           TAGS\n" +
		t1.ID[:8] + "  todo    2     Write the docs  docs,cli\n" +
		t2.ID[:8] + "  doing   3     Fix login bug   bug\n"
	if out != want {
		t.Errorf("list table:\n%q\nwant:\n%q", out, want)
	}

	// Patch by unique id prefix: only provided flags change.
	out, errS, code = runCmd(t, "update", t1.ID[:8], "--data", dir, "--title", "Write the docs v2", "--prio", "1")
	if code != 0 {
		t.Fatalf("update failed (code %d): %s", code, errS)
	}
	if want := "updated " + t1.ID[:8] + " Write the docs v2\n"; out != want {
		t.Errorf("update output = %q, want %q", out, want)
	}
	got := listJSON(t, "--data", dir)[0]
	if got.Title != "Write the docs v2" || got.Prio != 1 || got.Desc != "Cover the CLI" || got.Due != "2026-08-01" {
		t.Errorf("patch semantics broken: %+v", got)
	}

	// update --status moves the task.
	if _, errS, code = runCmd(t, "update", t1.ID[:8], "--data", dir, "--status", "doing"); code != 0 {
		t.Fatalf("update --status failed (code %d): %s", code, errS)
	}
	if n := len(listJSON(t, "--data", dir, "--status", "doing")); n != 2 {
		t.Errorf("after update --status doing: %d doing tasks, want 2", n)
	}

	// move and done.
	out, errS, code = runCmd(t, "move", t2.ID[:8], "done", "--data", dir)
	if code != 0 {
		t.Fatalf("move failed (code %d): %s", code, errS)
	}
	if want := "moved " + t2.ID[:8] + " -> done\n"; out != want {
		t.Errorf("move output = %q, want %q", out, want)
	}
	if out, _, code = runCmd(t, "done", t1.ID[:8], "--data", dir); code != 0 || out != "moved "+t1.ID[:8]+" -> done\n" {
		t.Errorf("done: code %d output %q", code, out)
	}
	if n := len(listJSON(t, "--data", dir, "--status", "done")); n != 2 {
		t.Errorf("got %d done tasks, want 2", n)
	}

	// rm refuses without --yes, deletes with it.
	_, errS, code = runCmd(t, "rm", t2.ID[:8], "--data", dir)
	if code != 1 || !strings.Contains(errS, "--yes") {
		t.Errorf("rm without --yes: code %d stderr %q, want code 1 mentioning --yes", code, errS)
	}
	if n := len(listJSON(t, "--data", dir)); n != 2 {
		t.Fatalf("rm without --yes deleted something: %d tasks left", n)
	}
	out, errS, code = runCmd(t, "rm", t2.ID[:8], "--yes", "--data", dir)
	if code != 0 {
		t.Fatalf("rm --yes failed (code %d): %s", code, errS)
	}
	if want := "deleted " + t2.ID[:8] + " Fix login bug\n"; out != want {
		t.Errorf("rm output = %q, want %q", out, want)
	}
	if n := len(listJSON(t, "--data", dir)); n != 1 {
		t.Errorf("after rm: %d tasks, want 1", n)
	}
}

func TestLocalPrefixErrors(t *testing.T) {
	dir := localEnv(t)
	if _, errS, code := runCmd(t, "add", "Solo", "--data", dir); code != 0 {
		t.Fatalf("add failed (code %d): %s", code, errS)
	}

	// UUIDs are hex, so "zz" can never match.
	_, errS, code := runCmd(t, "update", "zz", "--prio", "2", "--data", dir)
	if code != 1 || !strings.Contains(errS, "no task matches") {
		t.Errorf("unknown prefix: code %d stderr %q", code, errS)
	}

	// 17 more tasks guarantee two ids share a first hex char (16 possible).
	for i := 0; i < 17; i++ {
		if _, errS, code := runCmd(t, "add", fmt.Sprintf("Task %d", i), "--data", dir); code != 0 {
			t.Fatalf("add %d failed (code %d): %s", i, code, errS)
		}
	}
	seen := map[byte]bool{}
	prefix := ""
	for _, task := range listJSON(t, "--data", dir) {
		c := task.ID[0]
		if seen[c] {
			prefix = string(c)
			break
		}
		seen[c] = true
	}
	if prefix == "" {
		t.Fatal("no shared first hex char across 18 UUIDs (impossible)")
	}
	_, errS, code = runCmd(t, "move", prefix, "done", "--data", dir)
	if code != 1 || !strings.Contains(errS, "ambiguous") {
		t.Errorf("ambiguous prefix: code %d stderr %q", code, errS)
	}
}

func TestUsageErrors(t *testing.T) {
	dir := localEnv(t)
	cases := [][]string{
		{"add", "--data", dir},                             // missing title
		{"add", "", "--data", dir},                         // empty title
		{"add", "x", "--prio", "9", "--data", dir},         // prio out of range
		{"add", "x", "--due", "8/1", "--data", dir},        // malformed due
		{"add", "x", "--due", "2026-13-99", "--data", dir}, // impossible due
		{"add", "x", "--status", "blocked", "--data", dir}, // bad status
		{"add", "x", "--effort", "XL", "--data", dir},      // bad effort
		{"add", "x", "--bogus", "y", "--data", dir},        // unknown flag
		{"update", "abc", "--data", dir},                   // no field flags
		{"update", "abc", "--title", "", "--data", dir},    // empty title patch
		{"move", "abc", "--data", dir},                     // missing status
		{"move", "abc", "nowhere", "--data", dir},          // bad status
		{"list", "extra", "--data", dir},                   // stray positional
		{"frobnicate"},                                     // unknown command
	}
	for _, args := range cases {
		if _, _, code := runCmd(t, args...); code != 2 {
			t.Errorf("kb %s: code %d, want 2", strings.Join(args, " "), code)
		}
	}

	out, _, code := runCmd(t, "help")
	if code != 0 || !strings.Contains(out, "usage: kb") {
		t.Errorf("help: code %d output %q", code, out)
	}
}

// --- remote mode ---

// wireServer is a minimal in-memory kb server speaking the markdown wire.
type wireServer struct {
	mu       sync.Mutex
	doc      string
	has      bool
	lastAuth string
	lastUser string
}

func (s *wireServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/board", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.lastAuth = r.Header.Get("Authorization")
		s.lastUser = r.Header.Get("X-KB-User")
		switch r.Method {
		case http.MethodGet:
			if !s.has {
				http.Error(w, "no board saved", http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
			_, _ = io.WriteString(w, s.doc)
		case http.MethodPut:
			b, _ := io.ReadAll(r.Body)
			s.doc, s.has = string(b), true
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	return mux
}

func (s *wireServer) snapshot() (doc, auth, user string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.doc, s.lastAuth, s.lastUser
}

func TestRemoteLifecycle(t *testing.T) {
	ws := &wireServer{}
	srv := httptest.NewServer(ws.handler())
	defer srv.Close()
	t.Setenv("KB_SERVER", srv.URL)
	t.Setenv("KB_SERVER_TOKEN", "sekrit")

	// First add starts from a 404 (no board saved yet).
	out, errS, code := runCmd(t, "add", "Alpha", "--tag", "a", "--user", "Alice")
	if code != 0 {
		t.Fatalf("remote add failed (code %d): %s", code, errS)
	}
	if out != "added i1 Alpha\n" {
		t.Errorf("remote add output = %q, want %q", out, "added i1 Alpha\n")
	}
	doc, auth, user := ws.snapshot()
	wantDoc := "# Board\n\n## To Do\n\n- [ ] Alpha #a\n\n## Doing\n\n\n## Done\n\n"
	if doc != wantDoc {
		t.Errorf("doc after add:\n%q\nwant:\n%q", doc, wantDoc)
	}
	if auth != "Bearer sekrit" {
		t.Errorf("Authorization = %q, want %q", auth, "Bearer sekrit")
	}
	if user != "alice" { // CLI lowercases to match server identity sanitization
		t.Errorf("X-KB-User = %q, want %q", user, "alice")
	}

	if out, errS, code = runCmd(t, "add", "Beta", "--status", "doing", "--tag", "b"); code != 0 || out != "added i2 Beta\n" {
		t.Fatalf("remote add Beta: code %d output %q stderr %s", code, out, errS)
	}

	// Table with ephemeral ids.
	out, _, code = runCmd(t, "list")
	if code != 0 {
		t.Fatalf("remote list failed: code %d", code)
	}
	want := "ID  STATUS  PRIO  TITLE  TAGS\n" +
		"i1  todo    3     Alpha  a\n" +
		"i2  doing   3     Beta   b\n"
	if out != want {
		t.Errorf("remote list:\n%q\nwant:\n%q", out, want)
	}
	tasks := listJSON(t)
	if len(tasks) != 2 || tasks[0].ID != "i1" || tasks[1].ID != "i2" {
		t.Errorf("remote list --json ids wrong: %+v", tasks)
	}

	// Patch round-trips through the wire markdown.
	if out, errS, code = runCmd(t, "update", "i1", "--prio", "1", "--desc", "notes"); code != 0 || out != "updated i1 Alpha\n" {
		t.Fatalf("remote update: code %d output %q stderr %s", code, out, errS)
	}
	doc, _, _ = ws.snapshot()
	wantDoc = "# Board\n\n## To Do\n\n- [ ] Alpha !1 #a\n  notes\n\n## Doing\n\n- [ ] Beta #b\n\n## Done\n\n"
	if doc != wantDoc {
		t.Errorf("doc after update:\n%q\nwant:\n%q", doc, wantDoc)
	}

	// Move appends to the target column; the id reported is the new index.
	if out, errS, code = runCmd(t, "move", "i1", "doing"); code != 0 || out != "moved i2 -> doing\n" {
		t.Fatalf("remote move: code %d output %q stderr %s", code, out, errS)
	}
	doc, _, _ = ws.snapshot()
	if !strings.Contains(doc, "## Doing\n\n- [ ] Beta #b\n- [ ] Alpha !1 #a\n  notes\n") {
		t.Errorf("doc after move lacks reordered Doing column:\n%q", doc)
	}

	// done = move done; Alpha is i2 within doing.
	if out, errS, code = runCmd(t, "done", "i2"); code != 0 || out != "moved i2 -> done\n" {
		t.Fatalf("remote done: code %d output %q stderr %s", code, out, errS)
	}
	doc, _, _ = ws.snapshot()
	if !strings.Contains(doc, "## Done\n\n- [x] Alpha !1 #a\n  notes\n") {
		t.Errorf("doc after done lacks completed Alpha:\n%q", doc)
	}

	// rm accepts a bare index and reports the normalized id.
	if out, errS, code = runCmd(t, "rm", "1", "--yes"); code != 0 || out != "deleted i1 Beta\n" {
		t.Fatalf("remote rm: code %d output %q stderr %s", code, out, errS)
	}
	if doc, _, _ = ws.snapshot(); strings.Contains(doc, "Beta") {
		t.Errorf("Beta still present after rm:\n%q", doc)
	}

	// Out-of-range and malformed ids are runtime errors.
	if _, errS, code = runCmd(t, "move", "i9", "todo"); code != 1 || !strings.Contains(errS, "no task") {
		t.Errorf("remote bad index: code %d stderr %q", code, errS)
	}
	if _, errS, code = runCmd(t, "done", "ix"); code != 1 || !strings.Contains(errS, "invalid remote task id") {
		t.Errorf("remote malformed id: code %d stderr %q", code, errS)
	}
}
