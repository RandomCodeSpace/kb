package tui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
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
	if compact := m.render(); !strings.Contains(compact, "kb / Board / alice") {
		t.Fatalf("compact fallback view:\n%s", compact)
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
