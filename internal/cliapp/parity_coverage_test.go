package cliapp

import (
	"strings"
	"testing"
)

func TestDisplayRefShortensLongIDs(t *testing.T) {
	if got := displayRef("abc"); got != "abc" {
		t.Fatalf("short ref = %q", got)
	}
	if got := displayRef("0123456789abcdef"); got != "01234567" {
		t.Fatalf("long ref = %q", got)
	}
}

func TestLocalViewAndCommentsRejectUnknownRefs(t *testing.T) {
	dir := localEnv(t)
	if _, errS, code := runCmd(t, "add", "Seed", "--data", dir); code != 0 {
		t.Fatalf("seed add failed: %s", errS)
	}
	if _, errS, code := runCmd(t, "view", "9", "--data", dir); code != 1 || !strings.Contains(errS, "no task matches") {
		t.Fatalf("view unknown: code=%d stderr=%q", code, errS)
	}
	if _, errS, code := runCmd(t, "comment", "list", "9", "--data", dir); code != 1 || !strings.Contains(errS, "no task matches") {
		t.Fatalf("comment list unknown: code=%d stderr=%q", code, errS)
	}
}

func TestRemoteFullFieldWritesEndToEnd(t *testing.T) {
	remoteEnv(t)

	out, errS, code := runCmd(t, "add", "Everything",
		"--desc", "long form", "--emoji", "🚀", "--prio", "2", "--due", "2026-09-01",
		"--effort", "M", "--blocked", "--status", "doing",
		"--tag", "a", "--tag", "b", "--check", "one", "--check", "two",
		"--user", "alice")
	if code != 0 {
		t.Fatalf("full-field add failed (code %d): %s", code, errS)
	}
	if out != "added #1 Everything\n" {
		t.Fatalf("add output = %q", out)
	}

	out, errS, code = runCmd(t, "update", "1",
		"--title", "Renamed", "--desc", "new body", "--emoji", "🎯", "--prio", "3",
		"--due", "2026-10-01", "--effort", "L", "--no-blocked",
		"--tag", "c", "--check", "three",
		"--user", "alice")
	if code != 0 {
		t.Fatalf("full-field update failed (code %d): %s", code, errS)
	}
	if out != "updated #1 Renamed\n" {
		t.Fatalf("update output = %q", out)
	}

	tasks := listJSON(t, "--user", "alice")
	if len(tasks) != 1 {
		t.Fatalf("list = %+v", tasks)
	}
	got := tasks[0]
	if got.Title != "Renamed" || got.Desc != "new body" || got.Emoji != "🎯" ||
		got.Prio != 3 || got.Due != "2026-10-01" || got.Effort != "L" ||
		got.Blocked || got.Status != "doing" ||
		len(got.Tags) != 1 || got.Tags[0] != "c" ||
		len(got.Checks) != 1 || got.Checks[0].Text != "three" {
		t.Fatalf("round-tripped task = %+v", got)
	}
}

func TestRemoteErrorPathsEndToEnd(t *testing.T) {
	remoteEnv(t)
	if _, _, code := runCmd(t, "add", "Seed", "--user", "alice"); code != 0 {
		t.Fatal("seed add failed")
	}

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"view missing", []string{"view", "9"}, "404"},
		{"rm missing", []string{"rm", "9", "--yes"}, "404"},
		{"comment on missing task", []string{"comment", "add", "9", "text"}, "404"},
		{"comments of missing task", []string{"comment", "list", "9"}, "404"},
		{"delete missing comment", []string{"comment", "rm", "c9", "--yes"}, "404"},
		{"link to missing", []string{"link", "1", "blocks", "9"}, "404"},
		{"unlink without edge", []string{"unlink", "1", "1"}, "404"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			args := append(tt.args, "--user", "alice")
			_, errS, code := runCmd(t, args...)
			if code != 1 || !strings.Contains(errS, tt.want) {
				t.Fatalf("code=%d stderr=%q, want %q", code, errS, tt.want)
			}
		})
	}
}

func TestRemoteRejectsEphemeralIndexesOnEveryVerb(t *testing.T) {
	remoteEnv(t)

	cases := [][]string{
		{"view", "i2"},
		{"update", "i2", "--prio", "1"},
		{"rm", "i2", "--yes"},
		{"comment", "add", "i2", "text"},
		{"comment", "list", "i2"},
		{"link", "i2", "blocks", "1"},
		{"link", "1", "blocks", "i2"},
		{"unlink", "i2", "1"},
		{"unlink", "1", "i2"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			args := append(args, "--user", "alice")
			_, errS, code := runCmd(t, args...)
			if code != 1 || !strings.Contains(errS, "ephemeral i-N task ids are gone") {
				t.Fatalf("code=%d stderr=%q", code, errS)
			}
		})
	}
}

func TestRemoteCommentListTruncatesMultilineBodies(t *testing.T) {
	remoteEnv(t)
	if _, _, code := runCmd(t, "add", "Seed", "--user", "alice"); code != 0 {
		t.Fatal("seed add failed")
	}
	if _, errS, code := runCmd(t, "comment", "add", "1", "first line\nsecond line", "--user", "alice"); code != 0 {
		t.Fatalf("comment add failed: %s", errS)
	}
	out, _, code := runCmd(t, "comment", "list", "1", "--user", "alice")
	if code != 0 || !strings.Contains(out, "first line ...") || strings.Contains(out, "second line") {
		t.Fatalf("comment list output:\n%s", out)
	}
}

func TestViewRendersDashForMissingEffort(t *testing.T) {
	remoteEnv(t)
	if _, _, code := runCmd(t, "add", "Dated", "--due", "2026-09-01", "--user", "alice"); code != 0 {
		t.Fatal("seed add failed")
	}
	out, _, code := runCmd(t, "view", "1", "--user", "alice")
	if code != 0 || !strings.Contains(out, "due: 2026-09-01   effort: -") {
		t.Fatalf("view output:\n%s", out)
	}
}
