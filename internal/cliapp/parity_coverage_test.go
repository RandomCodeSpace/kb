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

func TestViewRendersDashForMissingEffort(t *testing.T) {
	dir := localEnv(t)
	if _, _, code := runCmd(t, "add", "Dated", "--due", "2026-09-01", "--data", dir); code != 0 {
		t.Fatal("seed add failed")
	}
	out, _, code := runCmd(t, "view", "1", "--data", dir)
	if code != 0 || !strings.Contains(out, "due: 2026-09-01   effort: -") {
		t.Fatalf("view output:\n%s", out)
	}
}
