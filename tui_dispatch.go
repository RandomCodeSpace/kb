package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/cliapp"
	"github.com/RandomCodeSpace/kb/internal/store"
	"github.com/RandomCodeSpace/kb/internal/tui"
)

var (
	openTUIStore  = cliapp.OpenLocalStore
	runTUIProgram = func(st *store.Store, databasePath, user, activeProject string, options ...tea.ProgramOption) error {
		return tui.Run(st, databasePath, user, activeProject, options...)
	}
	activeTUIProject           = cliapp.ActiveProject
	tuiStderr        io.Writer = os.Stderr
)

// runTUI opens the same local store as the task CLI: kb tui [--data DIR]. The
// board namespace is always defaultBoardUser; remote KB_SERVER mode is
// deliberately not part of this direct-SQLite wayfinder slice.
func runTUI(args []string) error {
	fs := flag.NewFlagSet("kb tui", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dataDir := fs.String("data", "", "board storage directory (default $KB_DATA or ~/.local/share/kb)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("tui takes no positional arguments")
	}
	resolvedData := *dataDir
	if resolvedData == "" {
		var err error
		resolvedData, err = resolveDefaultDataDir()
		if err != nil {
			return fmt.Errorf("cannot determine home directory, set KB_DATA or --data: %w", err)
		}
	}
	st, err := openTUIStore(resolvedData, tuiStderr)
	if err != nil {
		return err
	}
	defer st.Close()
	// The board opens on the project the task CLI would use. A board with no
	// active project simply opens unscoped: the TUI cannot set one, and the
	// editor asks for a project when a card is saved.
	activeProject, _, err := activeTUIProject(resolvedData)
	if err != nil {
		return err
	}
	return runTUIProgram(st, filepath.Join(resolvedData, "kb.db"), defaultBoardUser, activeProject)
}
