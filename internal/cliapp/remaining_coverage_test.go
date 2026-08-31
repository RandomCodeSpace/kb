package cliapp

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemainingCommandValidationAndUpdateFields(t *testing.T) {
	dir := localEnv(t)
	if _, stderr, code := runCmd(t, "list", "--status", "waiting", "--data", dir); code != 2 || !strings.Contains(stderr, "invalid status") {
		t.Fatalf("invalid list status: code=%d stderr=%q", code, stderr)
	}
	if _, stderr, code := runCmd(t, "add", "coverage task", "--data", dir); code != 0 {
		t.Fatalf("add: code=%d stderr=%q", code, stderr)
	}
	tasks := listJSON(t, "--data", dir)
	if len(tasks) != 1 {
		t.Fatalf("tasks after add = %+v", tasks)
	}
	if _, stderr, code := runCmd(t,
		"update", tasks[0].ID,
		"--emoji", "\U0001F680", "--due", "2026-08-02", "--effort", "L", "--tag", "coverage", "--data", dir,
	); code != 0 {
		t.Fatalf("update: code=%d stderr=%q", code, stderr)
	}
	updated := listJSON(t, "--data", dir)
	if len(updated) != 1 || updated[0].Emoji != "\U0001F680" || updated[0].Due != "2026-08-02" || updated[0].Effort != "L" || len(updated[0].Tags) != 2 || updated[0].Tags[0] != "coverage" || updated[0].Tags[1] != projectLabel(inboxProject) {
		t.Fatalf("updated task = %+v", updated)
	}
}

func TestOpenLocalRemainingFilesystemPaths(t *testing.T) {
	t.Run("default data succeeds", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("KB_DATA", dir)
		t.Setenv("KB_SECRET", "test-secret")
		be, err := openLocal("default", "", io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		if err := be.close(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("default data fails", func(t *testing.T) {
		t.Setenv("KB_DATA", "")
		t.Setenv("HOME", "")
		if _, err := openLocal("default", "", io.Discard); err == nil || !strings.Contains(err.Error(), "cannot determine home") {
			t.Fatalf("openLocal missing default error = %v", err)
		}
	})

	t.Run("secret load fails", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("KB_SECRET", "")
		if err := os.Mkdir(filepath.Join(dir, "secret"), 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := openLocal("default", dir, io.Discard); err == nil || !strings.Contains(err.Error(), "read secret") {
			t.Fatalf("openLocal secret error = %v", err)
		}
	})

	t.Run("store open fails", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("KB_SECRET", "test-secret")
		if err := os.Mkdir(filepath.Join(dir, dbFile), 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := openLocal("default", dir, io.Discard); err == nil {
			t.Fatal("openLocal accepted a directory as kb.db")
		}
	})

	t.Run("legacy import warns", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("KB_SECRET", "test-secret")
		if err := os.Symlink("missing-target", filepath.Join(dir, "broken.md")); err != nil {
			t.Fatal(err)
		}
		var stderr bytes.Buffer
		be, err := openLocal("default", dir, &stderr)
		if err != nil {
			t.Fatal(err)
		}
		if err := be.close(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(stderr.String(), "legacy markdown import") {
			t.Fatalf("warning = %q", stderr.String())
		}
	})
}
