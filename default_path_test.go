package main

import (
	"context"
	"io"
	"path/filepath"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/cliapp"
	"github.com/RandomCodeSpace/kb/internal/tui"
)

func TestDefaultDataPathWatcherDetectsExternalWrite(t *testing.T) {
	// os.UserHomeDir reads USERPROFILE on Windows and HOME on Unix. Resolve
	// the production default under a disposable home, without a --data override.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("KB_DATA", "")
	t.Setenv("KB_SECRET", "")
	data, err := resolveDefaultDataDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".local", "share", "kb"); data != want {
		t.Fatalf("default data directory = %q, want %q", data, want)
	}
	st, err := cliapp.OpenLocalStore("", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	watcher, err := tui.OpenDataVersionWatcher(context.Background(), filepath.Join(data, "kb.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = watcher.Close() })
	before, err := watcher.DataVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddTask(defaultBoardUser, board.Task{Title: "default path watcher"}); err != nil {
		t.Fatal(err)
	}
	after, err := watcher.DataVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if after == before {
		t.Fatal("watcher missed an external write on the default data path")
	}
}
