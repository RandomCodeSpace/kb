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
	runTUIProgram = func(st *store.Store, databasePath, user, activeProject, version string, options ...tea.ProgramOption) error {
		return tui.Run(st, databasePath, user, activeProject, version, options...)
	}
	activeTUIProject           = cliapp.ActiveProject
	tuiStderr        io.Writer = os.Stderr
)

// runTUI opens the same local store as the task CLI: kb tui [--data DIR]. The
// board namespace is always defaultBoardUser.
func runTUI(args []string) error {
	fs := flag.NewFlagSet("kb tui", flag.ContinueOnError)
	fs.SetOutput(tuiStderr)
	dataDir := fs.String("data", "", "board storage directory (default $KB_DATA or ~/.local/share/kb)")
	if err := fs.Parse(args); err != nil {
		return &commandFlagError{err: err}
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
	// The launch screen's meta row prints the same build identifier kb version
	// reads, so both answer a bug report with one string (spec section 10.6.5).
	version, _, _ := versionParts(readBuildInfo())
	return runTUIProgram(st, filepath.Join(resolvedData, "kb.db"), defaultBoardUser, activeProject, version)
}
