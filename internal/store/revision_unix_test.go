//go:build linux || darwin

package store

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/board"
)

func TestSQLiteFilesArePrivate(t *testing.T) {
	dir := t.TempDir()
	oldUmask := syscall.Umask(0o022)
	defer syscall.Umask(oldUmask)
	path := filepath.Join(dir, "kb.db")
	s := openStoreAt(t, path)
	if _, err := s.AddTask("u", board.Task{Title: "create WAL"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{path, path + "-wal", path + "-shm"} {
		info, err := os.Stat(name)
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if got := info.Mode().Perm(); got&0o077 != 0 {
			t.Errorf("%s mode=%#o, want no group/world permissions", name, got)
		}
	}
}
