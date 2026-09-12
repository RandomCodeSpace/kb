//go:build !windows

package cliapp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBackupCLIRejectsRelativeDestinationAfterWorkingDirectoryRemoved(t *testing.T) {
	source := localEnv(t)
	if _, stderr, code := runCmd(t, "add", "Retained task", "--data", source); code != 0 {
		t.Fatal(stderr)
	}
	working := t.TempDir()
	t.Chdir(working)
	if err := os.Remove(working); err != nil {
		t.Fatal(err)
	}
	if _, stderr, code := runCmd(t, "backup", "relative", "--data", source); code != 1 || stderr == "" {
		t.Fatalf("unresolved destination: code=%d stderr=%q", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(source, "relative")); !os.IsNotExist(err) {
		t.Fatalf("failed backup created destination under source: %v", err)
	}
	tasks := listJSON(t, "--data", source)
	if len(tasks) != 1 || tasks[0].Title != "Retained task" {
		t.Fatalf("unresolved destination changed source: %+v", tasks)
	}
}
