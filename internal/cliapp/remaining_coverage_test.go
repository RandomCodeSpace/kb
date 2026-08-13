package cliapp

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
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
	if len(updated) != 1 || updated[0].Emoji != "\U0001F680" || updated[0].Due != "2026-08-02" || updated[0].Effort != "L" || len(updated[0].Tags) != 1 || updated[0].Tags[0] != "coverage" {
		t.Fatalf("updated task = %+v", updated)
	}
}

func TestRemainingRemoveCommandErrors(t *testing.T) {
	t.Run("preview list", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "list failed", http.StatusServiceUnavailable)
		}))
		defer srv.Close()
		t.Setenv("KB_SERVER", srv.URL)
		if _, stderr, code := runCmd(t, "rm", "1"); code != 1 || !strings.Contains(stderr, "list failed") {
			t.Fatalf("rm preview list failure: code=%d stderr=%q", code, stderr)
		}
	})

	t.Run("preview lookup", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, "[]")
		}))
		defer srv.Close()
		t.Setenv("KB_SERVER", srv.URL)
		if _, stderr, code := runCmd(t, "rm", "1"); code != 1 || !strings.Contains(stderr, "no task") {
			t.Fatalf("rm preview lookup failure: code=%d stderr=%q", code, stderr)
		}
	})

	t.Run("confirmed remove", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodDelete {
				http.Error(w, "remove failed", http.StatusConflict)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `[{"id":"a","seq":1,"title":"doomed","status":"todo","prio":3}]`)
		}))
		defer srv.Close()
		t.Setenv("KB_SERVER", srv.URL)
		if _, stderr, code := runCmd(t, "rm", "1", "--yes"); code != 1 || !strings.Contains(stderr, "remove failed") {
			t.Fatalf("rm confirmed failure: code=%d stderr=%q", code, stderr)
		}
	})
}

func TestOpenLocalRemainingFilesystemPaths(t *testing.T) {
	t.Run("default data succeeds", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("KB_SERVER", "")
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
