package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/store"
)

func restoreTUISeams(t *testing.T) {
	t.Helper()
	originalOpen := openTUIStore
	originalRun := runTUIProgram
	originalStderr := tuiStderr
	originalHome := userHomeDir
	originalProject := activeTUIProject
	t.Cleanup(func() {
		openTUIStore = originalOpen
		runTUIProgram = originalRun
		tuiStderr = originalStderr
		userHomeDir = originalHome
		activeTUIProject = originalProject
	})
}

func TestRunTUIDiscoversLocalStoreAndUser(t *testing.T) {
	restoreTUISeams(t)
	data := t.TempDir()
	t.Setenv("KB_DATA", data)
	// KB_USER is dead: the TUI always opens the "default" board.
	t.Setenv("KB_USER", " Alice ")
	t.Setenv("KB_PROJECT", "kb")
	var gotPath, gotUser, gotProject string
	runTUIProgram = func(st *store.Store, path, user, activeProject, version string, _ ...tea.ProgramOption) error {
		gotPath, gotUser, gotProject = path, user, activeProject
		if version == "" {
			t.Fatal("runTUI passed no build version to the launch screen")
		}
		if _, err := st.Board(user); err != nil {
			return err
		}
		return nil
	}
	if err := runTUI(nil); err != nil {
		t.Fatalf("runTUI: %v", err)
	}
	if gotPath != filepath.Join(data, "kb.db") || gotUser != "default" {
		t.Fatalf("path/user = %q/%q", gotPath, gotUser)
	}
	// The board opens on the project the task CLI resolves, so a card created
	// in the TUI lands where kb add would have put it.
	if gotProject != "kb" {
		t.Fatalf("active project = %q, want kb", gotProject)
	}
	for _, name := range []string{"secret", "kb.db"} {
		if _, err := os.Stat(filepath.Join(data, name)); err != nil {
			t.Fatalf("startup artifact %s: %v", name, err)
		}
	}
}

func TestRunTUIErrors(t *testing.T) {
	restoreTUISeams(t)
	t.Setenv("KB_DATA", t.TempDir())
	if err := runTUI([]string{"--user", "alice"}); err == nil ||
		!strings.Contains(err.Error(), "flag provided but not defined: -user") {
		t.Fatalf("--user should be rejected: %v", err)
	}

	if err := runTUI([]string{"extra"}); err == nil || !strings.Contains(err.Error(), "positional") {
		t.Fatalf("positional error = %v", err)
	}
	if err := runTUI([]string{"--unknown"}); err == nil {
		t.Fatal("unknown flag unexpectedly succeeded")
	}

	want := errors.New("open failed")
	openTUIStore = func(string, io.Writer) (*store.Store, error) { return nil, want }
	if err := runTUI(nil); !errors.Is(err, want) {
		t.Fatalf("open error = %v", err)
	}

	t.Setenv("KB_DATA", "")
	userHomeDir = func() (string, error) { return "", want }
	if err := runTUI(nil); !errors.Is(err, want) || !strings.Contains(err.Error(), "cannot determine home") {
		t.Fatalf("home error = %v", err)
	}
}

func TestRunTUIPropagatesProgramFailureAndDefaultsBlankUser(t *testing.T) {
	restoreTUISeams(t)
	data := t.TempDir()
	t.Setenv("KB_DATA", data)
	want := errors.New("program failed")
	runTUIProgram = func(_ *store.Store, _ string, user string, _, _ string, _ ...tea.ProgramOption) error {
		if user != "default" {
			t.Fatalf("user = %q, want default", user)
		}
		return want
	}
	if err := runTUI([]string{"--data", data}); !errors.Is(err, want) {
		t.Fatalf("program error = %v", err)
	}
}

// TestRunTUIProgramDefaultStartsTheBoard exercises the production seam itself
// — the one line that hands the resolved project to the TUI — rather than only
// the stub the other dispatch tests install over it.
func TestRunTUIProgramDefaultStartsTheBoard(t *testing.T) {
	restoreTUISeams(t)
	data := t.TempDir()
	path := filepath.Join(data, "kb.db")
	st, err := store.Open(path, []byte("tui-dispatch-test-key"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := runTUIProgram(st, path, "default", "kb", "1.2.0",
		tea.WithInput(strings.NewReader("q")),
		tea.WithOutput(io.Discard),
		tea.WithoutSignals(),
		tea.WithWindowSize(80, 24),
	); err != nil {
		t.Fatalf("runTUIProgram: %v", err)
	}
}

// TestRunTUIPropagatesActiveProjectFailure pins that an unreadable active
// project stops the board instead of silently opening it unscoped: an unscoped
// board would let a card be created outside every project.
func TestRunTUIPropagatesActiveProjectFailure(t *testing.T) {
	restoreTUISeams(t)
	data := t.TempDir()
	t.Setenv("KB_DATA", data)
	want := errors.New("state unreadable")
	activeTUIProject = func(string) (string, bool, error) { return "", false, want }
	runTUIProgram = func(*store.Store, string, string, string, string, ...tea.ProgramOption) error {
		t.Fatal("the program ran without a resolved project")
		return nil
	}
	if err := runTUI(nil); !errors.Is(err, want) {
		t.Fatalf("active project error = %v", err)
	}
}
