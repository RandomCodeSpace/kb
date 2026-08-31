package cliapp

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
)

func TestUsersLocal(t *testing.T) {
	dir := localEnv(t)

	if _, errS, code := runCmd(t, "add", "First", "--data", dir); code != 0 {
		t.Fatalf("add failed: %s", errS)
	}
	if _, errS, code := runCmd(t, "add", "Second", "--data", dir); code != 0 {
		t.Fatalf("add failed: %s", errS)
	}
	if _, errS, code := runCmd(t, "add", "Third", "--data", dir); code != 0 {
		t.Fatalf("add failed: %s", errS)
	}

	// Every local command writes to the single "default" namespace.
	seedNamespace(t, dir, "alice", "Foreign")

	out, errS, code := runCmd(t, "users", "--data", dir)
	if code != 0 {
		t.Fatalf("users failed (code %d): %s", code, errS)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "USER") {
		t.Fatalf("users table = %q", out)
	}
	// Sorted by name: alice before default.
	if !strings.HasPrefix(lines[1], "alice") || !strings.HasPrefix(lines[2], "default") {
		t.Fatalf("users order = %q", out)
	}

	out, errS, code = runCmd(t, "users", "--json", "--data", dir)
	if code != 0 {
		t.Fatalf("users --json failed (code %d): %s", code, errS)
	}
	var users []struct {
		User  string `json:"user"`
		Tasks int    `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(out), &users); err != nil {
		t.Fatalf("users --json output not valid JSON: %v\n%s", err, out)
	}
	if len(users) != 2 || users[0].User != "alice" || users[0].Tasks != 1 ||
		users[1].User != "default" || users[1].Tasks != 3 {
		t.Fatalf("users --json = %+v", users)
	}
}

// seedNamespace writes one task into a namespace the CLI can no longer
// address, standing in for a database written before --user was removed.
func seedNamespace(t *testing.T, dir, user, title string) {
	t.Helper()
	secret, err := store.LoadOrCreateSecret(dir)
	if err != nil {
		t.Fatalf("secret: %v", err)
	}
	st, err := store.Open(filepath.Join(dir, dbFile), secret)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if _, err := st.AddTask(user, board.Task{Title: title}); err != nil {
		t.Fatalf("seed %s: %v", user, err)
	}
}

func TestUsersJSONEmptyBoard(t *testing.T) {
	dir := localEnv(t)
	out, errS, code := runCmd(t, "users", "--json", "--data", dir)
	if code != 0 {
		t.Fatalf("users --json failed (code %d): %s", code, errS)
	}
	if strings.TrimSpace(out) != "[]" {
		t.Fatalf("empty users --json = %q, want []", out)
	}
}

func TestUsersUsageErrors(t *testing.T) {
	dir := localEnv(t)
	if _, errS, code := runCmd(t, "users", "extra", "--data", dir); code != 2 || !strings.Contains(errS, "takes no arguments") {
		t.Fatalf("positional arg = code %d, stderr %q", code, errS)
	}
	if _, errS, code := runCmd(t, "users", "--nope"); code != 2 || errS == "" {
		t.Fatalf("bad flag = code %d, stderr %q", code, errS)
	}
}

// TestKBUserEnvIgnored pins the removal: KB_USER no longer selects a board.
func TestKBUserEnvIgnored(t *testing.T) {
	dir := localEnv(t)
	t.Setenv("KB_USER", "Alice")

	if _, errS, code := runCmd(t, "add", "Env task", "--data", dir); code != 0 {
		t.Fatalf("add failed: %s", errS)
	}
	t.Setenv("KB_USER", "")
	tasks := listJSON(t, "--data", dir)
	if len(tasks) != 1 || tasks[0].Title != "Env task" {
		t.Fatalf("tasks under default = %+v", tasks)
	}
	out, _, code := runCmd(t, "users", "--json", "--data", dir)
	if code != 0 || !strings.Contains(out, `"user": "default"`) || strings.Contains(out, "alice") {
		t.Fatalf("KB_USER leaked into the store: %q", out)
	}
}

// TestUserFlagRejected pins --user as an unknown flag on every task command.
func TestUserFlagRejected(t *testing.T) {
	dir := localEnv(t)
	commands := [][]string{
		{"add", "Blocked"},
		{"list"},
		{"view", "1"},
		{"update", "1", "--prio", "2"},
		{"move", "1", "done"},
		{"done", "1"},
		{"cancel", "1"},
		{"restore", "1"},
		{"rm", "1", "--yes"},
		{"comment", "add", "1", "note"},
		{"comment", "list", "1"},
		{"comment", "rm", "c1", "--yes"},
		{"link", "1", "blocks", "2"},
		{"unlink", "1", "2"},
		{"users"},
	}
	for _, args := range commands {
		full := append(append([]string{}, args...), "--user", "alice", "--data", dir)
		_, errS, code := runCmd(t, full...)
		if code != 2 || !strings.Contains(errS, "flag provided but not defined: -user") {
			t.Errorf("kb %s: code=%d stderr=%q", strings.Join(args, " "), code, errS)
		}
	}
}

// TestOrphanedNamespaceWarning covers the migration-honesty line: a database
// holding tasks the local commands can no longer reach says so once, on
// stderr, without touching the data.
func TestOrphanedNamespaceWarning(t *testing.T) {
	dir := localEnv(t)
	if _, errS, code := runCmd(t, "add", "Mine", "--data", dir); code != 0 {
		t.Fatalf("add failed: %s", errS)
	}
	if _, errS, _ := runCmd(t, "list", "--data", dir); strings.Contains(errS, "non-default namespaces") {
		t.Fatalf("warning fired without an orphan: %q", errS)
	}

	seedNamespace(t, dir, "alice", "Hers")
	seedNamespace(t, dir, "bob", "His")
	out, errS, code := runCmd(t, "list", "--json", "--data", dir)
	if code != 0 {
		t.Fatalf("list failed (code %d): %s", code, errS)
	}
	want := `kb: warning: tasks exist under non-default namespaces (alice, bob); ` +
		`local commands only use "default" and leave that data untouched` + "\n"
	if errS != want {
		t.Fatalf("warning = %q, want %q", errS, want)
	}
	if strings.Contains(out, "Hers") || strings.Contains(out, "His") {
		t.Fatalf("orphaned tasks leaked into the default board: %s", out)
	}

	// The data is still there: nothing was migrated or deleted.
	usersOut, _, code := runCmd(t, "users", "--json", "--data", dir)
	if code != 0 || !strings.Contains(usersOut, "alice") || !strings.Contains(usersOut, "bob") {
		t.Fatalf("orphaned namespaces were not preserved: %s", usersOut)
	}
}
