package cliapp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackupCLIUsesDataDirectoryAndPreservesSource(t *testing.T) {
	dir := localEnv(t)
	if _, stderr, code := runCmd(t, "add", "Portable task", "--data", dir); code != 0 {
		t.Fatal(stderr)
	}
	t.Setenv("KB_DATA", dir)
	destination := filepath.Join(t.TempDir(), "backup")
	out, stderr, code := runCmd(t, "backup", destination, "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("backup code=%d stderr=%q", code, stderr)
	}
	var result struct {
		Path                   string `json:"path"`
		RequiresExternalSecret bool   `json:"requiresExternalSecret"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.Path != destination || !result.RequiresExternalSecret {
		t.Fatalf("backup JSON=%s", out)
	}
	if strings.Contains(out, os.Getenv("KB_SECRET")) {
		t.Fatal("backup printed external secret")
	}
	if _, err := os.Stat(filepath.Join(destination, "secret")); !os.IsNotExist(err) {
		t.Fatalf("external secret materialized: %v", err)
	}
	for _, path := range []string{dir, destination} {
		tasks := listJSON(t, "--data", path)
		if len(tasks) != 1 || tasks[0].Title != "Portable task" {
			t.Fatalf("tasks in %s=%+v", path, tasks)
		}
	}
	if _, stderr, code := runCmd(t, "backup", destination, "--data", dir); code != 4 || !strings.Contains(stderr, "already exists") {
		t.Fatalf("existing destination code=%d stderr=%q", code, stderr)
	}
}

func TestBackupCLIUsageAndMissingSource(t *testing.T) {
	for _, args := range [][]string{{"backup"}, {"backup", "one", "two"}, {"backup", "--unknown"}} {
		if _, _, code := runCmd(t, args...); code != 2 {
			t.Fatalf("%v code=%d", args, code)
		}
	}
	source := filepath.Join(t.TempDir(), "missing-source")
	destination := filepath.Join(t.TempDir(), "backup")
	if _, stderr, code := runCmd(t, "backup", destination, "--data", source); code != 1 || !strings.Contains(stderr, "source") {
		t.Fatalf("missing source code=%d stderr=%q", code, stderr)
	}
	for _, path := range []string{source, destination} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("failed command created %s: %v", path, err)
		}
	}
}
