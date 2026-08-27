package tui

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
)

type cancelAwareWatcher struct {
	started  chan struct{}
	finished chan struct{}
	closed   chan struct{}
}

func (w *cancelAwareWatcher) DataVersion(ctx context.Context) (int64, error) {
	close(w.started)
	<-ctx.Done()
	close(w.finished)
	return 0, fmt.Errorf("blocked data_version: %w", ctx.Err())
}

func (w *cancelAwareWatcher) Close() error {
	// sql.Conn.Close has the same relevant contract: it waits for an active
	// query. This blocks forever if Run closes without cancelling first.
	<-w.finished
	close(w.closed)
	return nil
}

type quitAfterReadStarts struct {
	started <-chan struct{}
	sent    bool
}

func (r *quitAfterReadStarts) Read(buffer []byte) (int, error) {
	if r.sent {
		return 0, io.EOF
	}
	<-r.started
	r.sent = true
	buffer[0] = 'q'
	return 1, nil
}

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

func TestUnchangedSQLiteWatcherPollIsFrameAndTemporalFree(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kb.db")
	st, err := store.Open(path, []byte("test-secret"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	watcher, err := OpenDataVersionWatcher(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = watcher.Close() })
	version, err := watcher.DataVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := st.Board("alice")
	if err != nil {
		t.Fatal(err)
	}
	model := newTestRootModel(st, watcher, "alice")
	stamp := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	model.now = func() time.Time { return stamp.Add(12 * time.Hour) }
	model.renderedAt = stamp
	model.board = fixture
	model.loading = false
	model.haveBoardSnapshot = true
	model.haveVersion = true
	model.pollStarted = true
	model.dataVersion = version
	rebuildTestView(&model)
	before := model.RenderPlanStats()

	model, commands := model.updateWithCommands(pollTickMsg{})
	if commands.followUp == nil || commands.temporal != nil || !model.renderedAt.Equal(stamp) {
		t.Fatalf("poll command/clock = follow=%v temporal=%v rendered=%s", commands.followUp, commands.temporal, model.renderedAt)
	}
	message, ok := commands.followUp().(dataVersionMsg)
	if !ok || message.err != nil || message.version != version {
		t.Fatalf("real watcher poll = %#v", message)
	}
	model, commands = model.updateWithCommands(message)
	after := model.RenderPlanStats()
	if commands.followUp == nil || commands.temporal != nil ||
		after.PublishedFrames != before.PublishedFrames || after.TemporalScheduledTicks != before.TemporalScheduledTicks ||
		after.TemporalTaskVisits != before.TemporalTaskVisits {
		t.Fatalf("unchanged real watcher changed frame/temporal state: before=%+v after=%+v", before, after)
	}
}

func TestRunStartsAndQuits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kb.db")
	st, err := store.Open(path, []byte("test-secret"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := Run(st, path, "default", "kb", "1.2.0",
		tea.WithInput(bytes.NewBufferString("q")),
		tea.WithOutput(io.Discard),
		tea.WithoutSignals(),
		tea.WithWindowSize(80, 24),
	); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestRunCancelsInFlightWatcherBeforeClose(t *testing.T) {
	watcher := &cancelAwareWatcher{
		started:  make(chan struct{}),
		finished: make(chan struct{}),
		closed:   make(chan struct{}),
	}
	input := &quitAfterReadStarts{started: watcher.started}
	result := make(chan error, 1)
	go func() {
		result <- run(stubBoardReader{}, "unused", "default",
			func(context.Context, string) (versionWatcher, error) { return watcher, nil },
			tea.WithInput(input),
			tea.WithOutput(io.Discard),
			tea.WithoutSignals(),
			tea.WithWindowSize(80, 24),
		)
	}()

	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run blocked while cancelling the in-flight watcher read")
	}
	select {
	case <-watcher.closed:
	default:
		t.Fatal("watcher was not closed after its read observed cancellation")
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
