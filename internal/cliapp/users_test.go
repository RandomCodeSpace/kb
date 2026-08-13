package cliapp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestUsersLocal(t *testing.T) {
	dir := localEnv(t)

	if _, errS, code := runCmd(t, "add", "First", "--data", dir); code != 0 {
		t.Fatalf("add failed: %s", errS)
	}
	if _, errS, code := runCmd(t, "add", "Second", "--data", dir, "--user", "alice"); code != 0 {
		t.Fatalf("add failed: %s", errS)
	}
	if _, errS, code := runCmd(t, "add", "Third", "--data", dir, "--user", "alice"); code != 0 {
		t.Fatalf("add failed: %s", errS)
	}

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
	if len(users) != 2 || users[0].User != "alice" || users[0].Tasks != 2 ||
		users[1].User != "default" || users[1].Tasks != 1 {
		t.Fatalf("users --json = %+v", users)
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

func TestUsersRefusesRemoteMode(t *testing.T) {
	t.Setenv("KB_SERVER", "http://127.0.0.1:1")
	_, errS, code := runCmd(t, "users")
	if code != 1 || !strings.Contains(errS, "KB_SERVER") {
		t.Fatalf("remote users = code %d, stderr %q; want refusal naming KB_SERVER", code, errS)
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

func TestKBUserEnvDefault(t *testing.T) {
	dir := localEnv(t)
	t.Setenv("KB_USER", "Alice")

	if _, errS, code := runCmd(t, "add", "Env task", "--data", dir); code != 0 {
		t.Fatalf("add failed: %s", errS)
	}

	// The env identity is sanitized exactly like an explicit flag: lowercased.
	t.Setenv("KB_USER", "")
	tasks := listJSON(t, "--data", dir, "--user", "alice")
	if len(tasks) != 1 || tasks[0].Title != "Env task" {
		t.Fatalf("tasks under alice = %+v", tasks)
	}
	if tasks = listJSON(t, "--data", dir); len(tasks) != 0 {
		t.Fatalf("tasks under default = %+v, want none", tasks)
	}

	// An explicit --user beats the environment.
	t.Setenv("KB_USER", "bob")
	if _, errS, code := runCmd(t, "add", "Flag wins", "--data", dir, "--user", "carol"); code != 0 {
		t.Fatalf("add failed: %s", errS)
	}
	t.Setenv("KB_USER", "")
	if tasks = listJSON(t, "--data", dir, "--user", "carol"); len(tasks) != 1 {
		t.Fatalf("tasks under carol = %+v", tasks)
	}
}
