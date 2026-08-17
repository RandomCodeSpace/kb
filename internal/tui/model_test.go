package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/RandomCodeSpace/kb/internal/board"
)

type stubBoardReader struct {
	board board.Board
	err   error
}

func (s stubBoardReader) Board(string) (board.Board, error) { return s.board, s.err }

type stubVersionReader struct {
	version int64
	err     error
}

type boardResult struct {
	board board.Board
	err   error
}

type sequenceBoardReader struct {
	results []boardResult
	calls   int
}

func (s *sequenceBoardReader) Board(string) (board.Board, error) {
	result := s.results[s.calls]
	s.calls++
	return result.board, result.err
}

type countingVersionReader struct {
	version int64
	calls   int
}

func (s *countingVersionReader) DataVersion(context.Context) (int64, error) {
	s.calls++
	return s.version, nil
}

func (s stubVersionReader) DataVersion(context.Context) (int64, error) {
	return s.version, s.err
}

func TestModelLoadsRoutesAndRenders(t *testing.T) {
	loaded := board.Board{Title: "Work", Tasks: []board.Task{{Title: "one", Status: board.StatusTodo}}}
	m := NewModel(stubBoardReader{board: loaded}, nil, "alice")
	initial := m.Init()
	updated, command := m.Update(initial())
	m = updated.(Model)
	if command != nil || m.loading || m.board.Title != "Work" {
		t.Fatalf("loaded model = %#v, command=%v", m, command)
	}

	updated, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 20})
	m = updated.(Model)
	wide := m.View()
	if !wide.AltScreen || wide.MouseMode != tea.MouseModeCellMotion {
		t.Fatalf("view terminal modes = alt:%v mouse:%v", wide.AltScreen, wide.MouseMode)
	}
	for _, want := range []string{"kb / Work / alice", "[TO DO]", "1 card(s)", "DOING", "DONE", "ready"} {
		if !strings.Contains(wide.Content, want) {
			t.Errorf("wide view missing %q:\n%s", want, wide.Content)
		}
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m = updated.(Model)
	updated, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 0})
	m = updated.(Model)
	narrow := m.View().Content
	if !strings.Contains(narrow, "[DOING]") || strings.Contains(narrow, "TO DO") {
		t.Fatalf("narrow focused view:\n%s", narrow)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	m = updated.(Model)
	if m.focus != 0 {
		t.Fatalf("left focus = %d, want 0", m.focus)
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'h'})
	m = updated.(Model)
	if m.focus != 2 {
		t.Fatalf("wrapped focus = %d, want 2", m.focus)
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'l'})
	m = updated.(Model)
	if m.focus != 0 {
		t.Fatalf("l focus = %d, want 0", m.focus)
	}

	_, quit := m.Update(tea.KeyPressMsg{Code: 'q'})
	if quit == nil {
		t.Fatal("q did not return a quit command")
	}
	m.board.Title = "   "
	m.width = 1
	m.height = 1
	for _, line := range strings.Split(m.render(), "\n") {
		if width := ansi.StringWidth(line); width > 1 {
			t.Fatalf("one-column line width = %d: %q", width, line)
		}
	}
}

func TestRenderFitsNarrowTerminal(t *testing.T) {
	m := NewModel(stubBoardReader{}, nil, strings.Repeat("owner", 20))
	m.loading = false
	m.board = board.Board{
		Title: strings.Repeat("wide 界🙂 ", 10),
		Tasks: []board.Task{{Title: "one", Status: board.StatusTodo}},
	}
	m.loadErr = errors.New("a deliberately long database error that must not widen the terminal")
	for _, width := range []int{1, 2, 3, 8, 16, 23} {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			m.width = width
			output := m.render()
			for lineNumber, line := range strings.Split(output, "\n") {
				if got := ansi.StringWidth(line); got > width {
					t.Errorf("line %d width = %d, terminal = %d: %q", lineNumber+1, got, width, line)
				}
			}
			if width >= 3 && !strings.Contains(output, "┌") {
				t.Errorf("width %d lost the board frame:\n%s", width, output)
			}
		})
	}
}

func TestModelLoadAndPollFailures(t *testing.T) {
	want := errors.New("database unavailable")
	m := NewModel(stubBoardReader{err: want}, stubVersionReader{version: 7}, "u")
	if message := m.readDataVersion()(); message.(dataVersionMsg).version != 7 {
		t.Fatalf("data-version message = %#v", message)
	}

	updated, _ := m.Update(boardLoadedMsg{err: want})
	m = updated.(Model)
	if m.loading || !strings.Contains(m.View().Content, want.Error()) {
		t.Fatalf("load failure state/view = %#v / %q", m, m.View().Content)
	}

	updated, command := m.Update(dataVersionMsg{version: 7})
	m = updated.(Model)
	if !m.haveVersion || m.dataVersion != 7 || command == nil {
		t.Fatalf("initial version state = %#v command=%v", m, command)
	}
	updated, command = m.Update(dataVersionMsg{version: 8})
	m = updated.(Model)
	if !m.loading || m.dataVersion != 8 || command == nil {
		t.Fatalf("changed version state = %#v command=%v", m, command)
	}

	updated, command = m.Update(dataVersionMsg{err: want})
	m = updated.(Model)
	if !errors.Is(m.pollErr, want) || command == nil {
		t.Fatalf("version failure state = %#v command=%v", m, command)
	}
	updated, _ = m.Update(dataVersionMsg{version: 8})
	m = updated.(Model)
	if m.pollErr != nil {
		t.Fatalf("successful retry retained poll error: %v", m.pollErr)
	}

	updated, command = m.Update(pollTickMsg{})
	if command == nil {
		t.Fatal("poll tick did not read data_version")
	}
	if message := command(); message.(dataVersionMsg).version != 7 {
		t.Fatalf("poll result = %#v", message)
	}
}

func TestFailedBoardReloadRetriesOnUnchangedPollUntilRecovery(t *testing.T) {
	want := errors.New("transient read failure")
	reader := &sequenceBoardReader{results: []boardResult{
		{err: want},
		{err: want},
		{board: board.Board{Title: "Recovered"}},
	}}
	watcher := &countingVersionReader{version: 9}
	m := NewModel(reader, watcher, "u")
	m.loading = false
	m.haveVersion = true
	m.dataVersion = 8

	for attempt := 1; attempt <= 3; attempt++ {
		updated, poll := m.Update(pollTickMsg{})
		m = updated.(Model)
		if poll == nil {
			t.Fatalf("attempt %d: poll command is nil", attempt)
		}
		updated, reload := m.Update(poll())
		m = updated.(Model)
		if reload == nil {
			t.Fatalf("attempt %d: unchanged data_version did not retry failed load", attempt)
		}
		batch, ok := reload().(tea.BatchMsg)
		if !ok || len(batch) != 2 {
			t.Fatalf("attempt %d: reload message = %#v, want load and next poll", attempt, batch)
		}
		updated, _ = m.Update(batch[0]())
		m = updated.(Model)
		if attempt < 3 {
			if !errors.Is(m.loadErr, want) {
				t.Fatalf("attempt %d: load error = %v", attempt, m.loadErr)
			}
		} else if m.loadErr != nil || m.board.Title != "Recovered" {
			t.Fatalf("recovery state = %#v", m)
		}
	}
	if reader.calls != 3 || watcher.calls != 3 || m.dataVersion != 9 {
		t.Fatalf("calls/version = board:%d watcher:%d version:%d", reader.calls, watcher.calls, m.dataVersion)
	}
}

func TestRetryPollsDoNotStartOverlappingBoardLoads(t *testing.T) {
	originalInterval := pollInterval
	pollInterval = 0
	t.Cleanup(func() { pollInterval = originalInterval })

	want := errors.New("transient read failure")
	reader := &sequenceBoardReader{results: []boardResult{
		{err: want},
		{board: board.Board{Title: "Recovered"}},
		{board: board.Board{Title: "Newest"}},
	}}
	watcher := &countingVersionReader{version: 9}
	m := NewModel(reader, watcher, "u")
	m.loading = false
	m.haveVersion = true
	m.dataVersion = 8

	poll := func() tea.Cmd {
		t.Helper()
		updated, read := m.Update(pollTickMsg{})
		m = updated.(Model)
		updated, next := m.Update(read())
		m = updated.(Model)
		return next
	}

	first := poll()
	firstBatch := first().(tea.BatchMsg)
	updated, _ := m.Update(firstBatch[0]())
	m = updated.(Model)
	if !errors.Is(m.loadErr, want) || reader.calls != 1 {
		t.Fatalf("first load = error:%v calls:%d", m.loadErr, reader.calls)
	}

	retry := poll()
	retryBatch := retry().(tea.BatchMsg)
	if !m.loading {
		t.Fatal("retry load is not marked in flight")
	}
	for attempt := 1; attempt <= 2; attempt++ {
		if message := poll()(); message == nil {
			t.Fatalf("overlap poll %d did not schedule the next poll", attempt)
		} else if _, ok := message.(pollTickMsg); !ok {
			t.Fatalf("overlap poll %d returned %T, want only pollTickMsg", attempt, message)
		}
		if reader.calls != 1 {
			t.Fatalf("overlap poll %d started another board load: calls=%d", attempt, reader.calls)
		}
	}

	// A newer version observed while the retry is active is queued, not run
	// concurrently or forgotten.
	watcher.version = 10
	if message := poll()(); message == nil {
		t.Fatal("newer-version poll did not schedule the next poll")
	} else if _, ok := message.(pollTickMsg); !ok {
		t.Fatalf("newer-version poll returned %T, want only pollTickMsg", message)
	}
	if reader.calls != 1 || !m.reloadPending {
		t.Fatalf("queued reload = calls:%d pending:%v", reader.calls, m.reloadPending)
	}

	updated, pending := m.Update(retryBatch[0]())
	m = updated.(Model)
	if pending == nil || reader.calls != 2 || m.board.Title != "Recovered" || !m.loading {
		t.Fatalf("retry recovery = model:%#v calls:%d command:%v", m, reader.calls, pending)
	}
	updated, _ = m.Update(pending())
	m = updated.(Model)
	if reader.calls != 3 || m.loadErr != nil || m.loading || m.board.Title != "Newest" {
		t.Fatalf("queued recovery = model:%#v calls:%d", m, reader.calls)
	}
}

func TestEmptyBoardGolden(t *testing.T) {
	m := NewModel(stubBoardReader{board: board.Board{Title: "Board"}}, nil, "default")
	// Start from a loaded snapshot so the golden records the frame, not a
	// renderer-timing-dependent diff from "loading" to "ready".
	m.loading = false
	tm := teatest.NewTestModel(t, m,
		teatest.WithInitialTermSize(120, 20),
		teatest.WithProgramOptions(tea.WithColorProfile(colorprofile.ASCII)),
	)
	t.Cleanup(func() { _ = tm.Quit() })
	var captured bytes.Buffer
	teatest.WaitFor(t, io.TeeReader(tm.Output(), &captured), func(output []byte) bool {
		return bytes.Contains(output, []byte("ready | q quit"))
	}, teatest.WithDuration(2*time.Second), teatest.WithCheckInterval(10*time.Millisecond))
	teatest.RequireEqualOutput(t, captured.Bytes())
	tm.Send(tea.KeyPressMsg{Code: 'q'})
	tm.WaitFinished(t, teatest.WithFinalTimeout(time.Second))
}
