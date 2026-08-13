package cliapp

import (
	"strings"
	"testing"
)

func seedSearchBoard(t *testing.T, extra ...string) {
	t.Helper()
	adds := [][]string{
		{"add", "Fix login timeout", "--desc", "auth token expires", "--tag", "bug", "--tag", "auth"},
		{"add", "Design landing page", "--tag", "ui"},
		{"add", "Rotate auth keys", "--tag", "auth", "--tag", "env::prod", "--status", "doing"},
	}
	for _, args := range adds {
		if _, errS, code := runCmd(t, append(args, extra...)...); code != 0 {
			t.Fatalf("seed %v failed: %s", args, errS)
		}
	}
}

func assertListTitles(t *testing.T, wantTitles []string, args ...string) {
	t.Helper()
	tasks := listJSON(t, args...)
	got := make([]string, 0, len(tasks))
	for _, task := range tasks {
		got = append(got, task.Title)
	}
	if len(got) != len(wantTitles) {
		t.Fatalf("list %v = %v, want %v", args, got, wantTitles)
	}
	for i := range wantTitles {
		if got[i] != wantTitles[i] {
			t.Fatalf("list %v = %v, want %v", args, got, wantTitles)
		}
	}
}

func TestListSearchAndTagFiltersLocal(t *testing.T) {
	dir := localEnv(t)
	seedSearchBoard(t, "--data", dir)

	assertListTitles(t, []string{"Fix login timeout", "Rotate auth keys"}, "--search", "auth", "--data", dir)
	assertListTitles(t, []string{"Fix login timeout"}, "--search", "token expir", "--data", dir)
	assertListTitles(t, []string{"Fix login timeout"}, "--tag", "auth", "--tag", "bug", "--data", dir)
	assertListTitles(t, []string{"Rotate auth keys"}, "--tag", "env::prod", "--data", dir)
	assertListTitles(t, []string{"Rotate auth keys"}, "--search", "auth", "--tag", "auth", "--status", "doing", "--data", dir)
	assertListTitles(t, []string{}, "--search", "nonexistent", "--data", dir)

	if _, errS, code := runCmd(t, "list", "--tag", " ", "--data", dir); code != 1 || !strings.Contains(errS, "tag filter") {
		t.Fatalf("blank tag: code=%d stderr=%q", code, errS)
	}
}

func TestListSearchAndTagFiltersRemote(t *testing.T) {
	remoteEnv(t)
	seedSearchBoard(t, "--user", "alice")

	assertListTitles(t, []string{"Fix login timeout", "Rotate auth keys"}, "--search", "auth", "--user", "alice")
	assertListTitles(t, []string{"Fix login timeout"}, "--tag", "auth", "--tag", "bug", "--user", "alice")
	assertListTitles(t, []string{"Rotate auth keys"}, "--tag", "env::prod", "--user", "alice")
	assertListTitles(t, []string{"Rotate auth keys"}, "--search", "auth", "--tag", "auth", "--status", "doing", "--user", "alice")

	if _, errS, code := runCmd(t, "list", "--tag", " ", "--user", "alice"); code != 1 || !strings.Contains(errS, "400") {
		t.Fatalf("remote blank tag: code=%d stderr=%q", code, errS)
	}
}
