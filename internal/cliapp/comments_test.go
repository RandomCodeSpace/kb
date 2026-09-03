package cliapp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCommentLifecycleCLI(t *testing.T) {
	dir := localEnv(t)
	if _, errS, code := runCmd(t, "add", "Discussable", "--data", dir); code != 0 {
		t.Fatalf("add failed: %s", errS)
	}

	out, errS, code := runCmd(t, "comment", "add", "1", "first note", "--data", dir)
	if code != 0 || out != "commented c1 on #1\n" {
		t.Fatalf("comment add: code=%d out=%q stderr=%q", code, out, errS)
	}

	out, _, code = runCmd(t, "comment", "add", "1", "second note", "--json", "--data", dir)
	if code != 0 {
		t.Fatalf("comment add --json failed")
	}
	var c struct {
		ID     int    `json:"id"`
		Task   int    `json:"task"`
		Author string `json:"author"`
		Body   string `json:"body"`
	}
	if err := json.Unmarshal([]byte(out), &c); err != nil {
		t.Fatalf("comment add --json output: %v\n%s", err, out)
	}
	if c.ID != 2 || c.Task != 1 || c.Author != "default" || c.Body != "second note" {
		t.Fatalf("comment add --json = %+v", c)
	}

	out, _, code = runCmd(t, "comment", "list", "1", "--data", dir)
	if code != 0 {
		t.Fatalf("comment list failed")
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[1], "c1") || !strings.Contains(lines[1], "first note") {
		t.Fatalf("comment list table:\n%s", out)
	}

	// comment list --json should produce valid JSON array
	listOut, _, code := runCmd(t, "comment", "list", "1", "--json", "--data", dir)
	if code != 0 {
		t.Fatalf("comment list --json failed")
	}
	var comments []map[string]interface{}
	if err := json.Unmarshal([]byte(listOut), &comments); err != nil {
		t.Fatalf("comment list --json output not JSON: %v\n%s", err, listOut)
	}
	if len(comments) != 2 {
		t.Fatalf("comment list --json should return 2 comments, got %d", len(comments))
	}
	if id, ok := comments[0]["id"]; !ok || id == nil {
		t.Fatalf("comment list --json missing id: %v", comments[0])
	}

	// rm demands --yes, accepts the c-form, and reports the deletion.
	if _, errS, code = runCmd(t, "comment", "rm", "c1", "--data", dir); code != 1 || !strings.Contains(errS, "--yes") {
		t.Fatalf("comment rm without --yes: code=%d stderr=%q", code, errS)
	}
	if out, _, code = runCmd(t, "comment", "rm", "c1", "--yes", "--data", dir); code != 0 || out != "deleted c1\n" {
		t.Fatalf("comment rm: code=%d out=%q", code, out)
	}

	// comment rm --json should produce valid JSON
	rmOut, _, code := runCmd(t, "comment", "rm", "c2", "--yes", "--json", "--data", dir)
	if code != 0 {
		t.Fatalf("comment rm --json failed")
	}
	var deleted map[string]interface{}
	if err := json.Unmarshal([]byte(rmOut), &deleted); err != nil {
		t.Fatalf("comment rm --json output not JSON: %v\n%s", err, rmOut)
	}
	if id, ok := deleted["id"]; !ok || id == nil {
		t.Fatalf("comment rm --json missing id: %v", deleted)
	}
	if _, errS, code = runCmd(t, "comment", "rm", "1", "--yes", "--data", dir); code != 1 || !strings.Contains(errS, "no comment matches") {
		t.Fatalf("comment rm gone: code=%d stderr=%q", code, errS)
	}
}

func TestCommentUsageErrors(t *testing.T) {
	dir := localEnv(t)
	cases := [][]string{
		{"comment", "--data", dir},                      // no sub-command
		{"comment", "edit", "1", "--data", dir},         // unknown sub-command
		{"comment", "add", "1", "--data", dir},          // missing text
		{"comment", "add", "1", " ", "--data", dir},     // blank text
		{"comment", "list", "--data", dir},              // missing id
		{"comment", "rm", "x9", "--yes", "--data", dir}, // malformed cid
	}
	for _, args := range cases {
		if _, _, code := runCmd(t, args...); code != 2 {
			t.Errorf("kb %s: code %d, want 2", strings.Join(args, " "), code)
		}
	}

	// Commenting on a missing task is a runtime error with the friendly id.
	if _, errS, code := runCmd(t, "comment", "add", "9", "text", "--data", dir); code != 1 || !strings.Contains(errS, "no task matches") {
		t.Errorf("comment on missing task: code=%d stderr=%q", code, errS)
	}
}
