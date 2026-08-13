package cliapp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestViewShowsTaskAndComments(t *testing.T) {
	dir := localEnv(t)
	if _, errS, code := runCmd(t, "add", "Inspect me", "--desc", "Two\nlines", "--prio", "2",
		"--due", "2026-09-01", "--effort", "M", "--tag", "bug", "--check", "x repro", "--check", "fix", "--data", dir); code != 0 {
		t.Fatalf("add failed: %s", errS)
	}
	if _, _, code := runCmd(t, "comment", "add", "1", "a finding", "--data", dir); code != 0 {
		t.Fatal("comment add failed")
	}

	out, errS, code := runCmd(t, "view", "1", "--data", dir)
	if code != 0 {
		t.Fatalf("view failed (code %d): %s", code, errS)
	}
	for _, want := range []string{
		"#1 Inspect me",
		"status: todo   prio: 2   blocked: no",
		"due: 2026-09-01   effort: M",
		"tags: bug",
		"Two\nlines",
		"[x] repro",
		"[ ] fix",
		"comments:",
		"c1  default",
		"a finding",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("view output missing %q:\n%s", want, out)
		}
	}

	out, _, code = runCmd(t, "view", "#1", "--json", "--data", dir)
	if code != 0 {
		t.Fatalf("view --json failed")
	}
	var v struct {
		Seq      int    `json:"seq"`
		Title    string `json:"title"`
		Comments []struct {
			ID   int    `json:"id"`
			Body string `json:"body"`
		} `json:"comments"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("view --json output: %v\n%s", err, out)
	}
	if v.Seq != 1 || v.Title != "Inspect me" || len(v.Comments) != 1 || v.Comments[0].Body != "a finding" {
		t.Fatalf("view --json = %+v", v)
	}

	// Empty comment section and errors.
	if _, _, code := runCmd(t, "add", "Bare", "--data", dir); code != 0 {
		t.Fatal("second add failed")
	}
	if out, _, _ = runCmd(t, "view", "2", "--data", dir); !strings.Contains(out, "comments: none") {
		t.Errorf("view of uncommented task:\n%s", out)
	}
	if _, errS, code := runCmd(t, "view", "9", "--data", dir); code != 1 || !strings.Contains(errS, "no task matches") {
		t.Errorf("view missing task: code=%d stderr=%q", code, errS)
	}
	if _, _, code := runCmd(t, "view", "--data", dir); code != 2 {
		t.Errorf("view without id: code=%d, want 2", code)
	}
}
