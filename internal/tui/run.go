package tui

import (
	"context"
	"errors"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/store"
)

// Run opens the data-version watcher and runs the full-screen program.
// Program options are accepted for deterministic tests; production passes none.
func Run(st *store.Store, databasePath, user string, options ...tea.ProgramOption) (err error) {
	watcher, err := OpenDataVersionWatcher(context.Background(), databasePath)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, watcher.Close()) }()
	if _, err := tea.NewProgram(NewModel(st, watcher, user), options...).Run(); err != nil {
		return fmt.Errorf("tui: run: %w", err)
	}
	return nil
}
