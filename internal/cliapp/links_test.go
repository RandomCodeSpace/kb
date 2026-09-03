package cliapp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLinkUnlinkCLI(t *testing.T) {
	dir := localEnv(t)
	for _, title := range []string{"Blocker", "Blocked"} {
		if _, errS, code := runCmd(t, "add", title, "--data", dir); code != 0 {
			t.Fatalf("add failed: %s", errS)
		}
	}

	out, errS, code := runCmd(t, "link", "1", "blocks", "2", "--data", dir)
	if code != 0 || out != "linked: #1 blocks #2\n" {
		t.Fatalf("link: code=%d out=%q stderr=%q", code, out, errS)
	}

	// Finishing the blocked task refuses; --force overrides.
	if _, errS, code = runCmd(t, "done", "2", "--data", dir); code != 1 ||
		!strings.Contains(errS, "1 open blocker (#1) still blocks #2") {
		t.Fatalf("gated done: code=%d stderr=%q", code, errS)
	}
	if _, _, code = runCmd(t, "done", "2", "--force", "--data", dir); code != 0 {
		t.Fatal("forced done failed")
	}

	// view shows both directions.
	out, _, _ = runCmd(t, "view", "1", "--data", dir)
	if !strings.Contains(out, "blocks: #2 (done)") {
		t.Errorf("view blocker missing edge:\n%s", out)
	}
	out, _, _ = runCmd(t, "view", "2", "--json", "--data", dir)
	if !strings.Contains(out, `"blockedBy": [`) {
		t.Errorf("view --json missing blockedBy:\n%s", out)
	}

	// Reversing while the original edge exists would close a cycle: refused.
	if _, errS, code = runCmd(t, "link", "1", "blocked-by", "2", "--data", dir); code != 1 || !strings.Contains(errS, "cycle") {
		t.Fatalf("cycle link: code=%d stderr=%q", code, errS)
	}

	// After unlinking, blocked-by reverses the direction; unlink works in
	// either argument order.
	if out, _, code = runCmd(t, "unlink", "1", "2", "--data", dir); code != 0 || out != "unlinked 1 and 2\n" {
		t.Fatalf("unlink 1 2: code=%d out=%q", code, out)
	}
	if out, _, code = runCmd(t, "link", "1", "blocked-by", "2", "--data", dir); code != 0 || out != "linked: #2 blocks #1\n" {
		t.Fatalf("blocked-by link: code=%d out=%q", code, out)
	}
	if out, _, code = runCmd(t, "unlink", "2", "1", "--json", "--data", dir); code != 0 {
		t.Fatalf("unlink 2 1 --json: code=%d out=%q", code, out)
	}
	var removed map[string]bool
	if err := json.Unmarshal([]byte(out), &removed); err != nil {
		t.Fatalf("unlink --json: %v\n%s", err, out)
	}
	if len(removed) != 1 || !removed["removed"] {
		t.Fatalf("unlink --json = %#v", removed)
	}
	if _, errS, code = runCmd(t, "unlink", "1", "2", "--data", dir); code != 1 || !strings.Contains(errS, "no link") {
		t.Fatalf("unlink absent: code=%d stderr=%q", code, errS)
	}

	// Usage errors.
	for _, args := range [][]string{
		{"link", "1", "2", "--data", dir},           // missing relation
		{"link", "1", "needs", "2", "--data", dir},  // bad relation
		{"unlink", "1", "--data", dir},              // one id
		{"link", "1", "blocks", "1", "--data", dir}, // self (runtime, but check code)
	} {
		_, _, code := runCmd(t, args...)
		if code == 0 {
			t.Errorf("kb %s succeeded, want failure", strings.Join(args, " "))
		}
	}
}

func TestLinkJSONOutput(t *testing.T) {
	dir := localEnv(t)
	// Create two tasks to link
	if _, errS, code := runCmd(t, "add", "Task A", "--data", dir); code != 0 {
		t.Fatalf("add A failed: %s", errS)
	}
	if _, errS, code := runCmd(t, "add", "Task B", "--data", dir); code != 0 {
		t.Fatalf("add B failed: %s", errS)
	}

	// link --json should produce valid JSON array with two task objects
	out, _, code := runCmd(t, "link", "1", "blocks", "2", "--json", "--data", dir)
	if code != 0 {
		t.Fatalf("link --json failed")
	}
	var linked []map[string]interface{}
	if err := json.Unmarshal([]byte(out), &linked); err != nil {
		t.Fatalf("link --json output not JSON: %v\n%s", err, out)
	}
	if len(linked) != 2 {
		t.Fatalf("link --json should return 2 tasks, got %d", len(linked))
	}
	// Check that both tasks have id field
	for i, task := range linked {
		if id, ok := task["id"]; !ok || id == "" {
			t.Fatalf("link --json task %d missing id: %v", i, task)
		}
	}
}
