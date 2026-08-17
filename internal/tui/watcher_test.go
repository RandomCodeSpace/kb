package tui

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
)

func TestDataVersionWatcherDetectsAnotherConnection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kb.db")
	st, err := store.Open(path, []byte("test-secret"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	watcher, err := OpenDataVersionWatcher(context.Background(), path)
	if err != nil {
		t.Fatalf("open watcher: %v", err)
	}
	t.Cleanup(func() { _ = watcher.Close() })
	before, err := watcher.DataVersion(context.Background())
	if err != nil {
		t.Fatalf("initial data_version: %v", err)
	}
	if _, err := st.AddTask("alice", board.Task{Title: "external", Status: board.StatusTodo, Prio: 3}); err != nil {
		t.Fatalf("external write: %v", err)
	}
	after, err := watcher.DataVersion(context.Background())
	if err != nil {
		t.Fatalf("updated data_version: %v", err)
	}
	if after == before {
		t.Fatalf("data_version did not change after other-connection commit: %d", after)
	}
}

func TestRunStartsAndQuits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kb.db")
	st, err := store.Open(path, []byte("test-secret"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := Run(st, path, "default",
		tea.WithInput(bytes.NewBufferString("q")),
		tea.WithOutput(io.Discard),
		tea.WithoutSignals(),
		tea.WithWindowSize(80, 24),
	); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestDataVersionWatcherErrors(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := OpenDataVersionWatcher(cancelled, filepath.Join(t.TempDir(), "kb.db")); err == nil {
		t.Fatal("cancelled context unexpectedly pinned a connection")
	}

	path := filepath.Join(t.TempDir(), "kb.db")
	st, err := store.Open(path, []byte("test-secret"))
	if err != nil {
		t.Fatal(err)
	}
	watcher, err := OpenDataVersionWatcher(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := watcher.conn.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := watcher.DataVersion(context.Background()); err == nil {
		t.Fatal("closed connection unexpectedly returned data_version")
	}
	_ = watcher.db.Close()
	_ = st.Close()
}
