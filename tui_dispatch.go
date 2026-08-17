package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/cliapp"
	"github.com/RandomCodeSpace/kb/internal/store"
	"github.com/RandomCodeSpace/kb/internal/tui"
)

var (
	openTUIStore  = cliapp.OpenLocalStore
	runTUIProgram = func(st *store.Store, databasePath, user string, options ...tea.ProgramOption) error {
		return tui.Run(st, databasePath, user, options...)
	}
	tuiStderr io.Writer = os.Stderr
)

// runTUI opens the same local store as the task CLI: kb tui [--data DIR]
// [--user NAME]. Remote KB_SERVER mode is deliberately not part of this
// direct-SQLite wayfinder slice.
func runTUI(args []string) error {
	fs := flag.NewFlagSet("kb tui", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dataDir := fs.String("data", "", "board storage directory (default $KB_DATA or ~/.local/share/kb)")
	user := fs.String("user", envOr("KB_USER", "default"), "board owner (env KB_USER)")
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
	owner := strings.TrimSpace(*user)
	if owner == "" {
		owner = "default"
	}
	owner, err := store.SanitizeUser(owner)
	if err != nil {
		return err
	}
	st, err := openTUIStore(resolvedData, tuiStderr)
	if err != nil {
		return err
	}
	defer st.Close()
	return runTUIProgram(st, filepath.Join(resolvedData, "kb.db"), owner)
}
