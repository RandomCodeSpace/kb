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
	t.Cleanup(func() {
		openTUIStore = originalOpen
		runTUIProgram = originalRun
		tuiStderr = originalStderr
		userHomeDir = originalHome
	})
}

func TestRunTUIDiscoversLocalStoreAndUser(t *testing.T) {
	restoreTUISeams(t)
	data := t.TempDir()
	t.Setenv("KB_DATA", data)
	t.Setenv("KB_USER", " Alice ")
	var gotPath, gotUser string
	runTUIProgram = func(st *store.Store, path, user string, _ ...tea.ProgramOption) error {
		gotPath, gotUser = path, user
		if _, err := st.Board(user); err != nil {
			return err
		}
		return nil
	}
	if err := runTUI(nil); err != nil {
		t.Fatalf("runTUI: %v", err)
	}
	if gotPath != filepath.Join(data, "kb.db") || gotUser != "alice" {
		t.Fatalf("path/user = %q/%q", gotPath, gotUser)
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
	t.Setenv("KB_USER", "bad/user")
	if err := runTUI(nil); err == nil || !strings.Contains(err.Error(), "invalid character") {
		t.Fatalf("invalid user error = %v", err)
	}

	t.Setenv("KB_USER", "")
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
	t.Setenv("KB_USER", "   ")
	want := errors.New("program failed")
	runTUIProgram = func(_ *store.Store, _ string, user string, _ ...tea.ProgramOption) error {
		if user != "default" {
			t.Fatalf("user = %q, want default", user)
		}
		return want
	}
	if err := runTUI([]string{"--data", data}); !errors.Is(err, want) {
		t.Fatalf("program error = %v", err)
	}
}
