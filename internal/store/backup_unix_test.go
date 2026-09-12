//go:build !windows

package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestBackupRejectsRelativePathsAfterWorkingDirectoryRemoved(t *testing.T) {
	source := backupSource(t)
	destination := filepath.Join(t.TempDir(), "backup")
	working := t.TempDir()
	t.Chdir(working)
	if err := os.Remove(working); err != nil {
		t.Fatal(err)
	}
	for _, paths := range [][2]string{{"relative-source", destination}, {source, "relative-destination"}} {
		if err := BackupDirectory(paths[0], paths[1]); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("removed working directory %v: %v", paths, err)
		}
	}
	// Depending on the OS, resolution or SQLite can report the missing cwd.
	// Neither path may acquire a maintenance connection.
	if lock, err := acquireBackupLock("relative-source"); err == nil || lock != nil {
		t.Fatalf("acquired backup lock with unresolved relative path: %v, %v", lock, err)
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed path resolution created destination: %v", err)
	}
	assertBackupSourceUsable(t, source)
}
