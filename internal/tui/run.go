package tui

import (
	"context"
	"errors"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/store"
)

type versionWatcher interface {
	dataVersionReader
	Close() error
}

type watcherOpener func(context.Context, string) (versionWatcher, error)

// Run opens the data-version watcher and runs the full-screen program.
// Program options are accepted for deterministic tests; production passes none.
func Run(st *store.Store, databasePath, user string, options ...tea.ProgramOption) (err error) {
	return run(st, databasePath, user, func(ctx context.Context, path string) (versionWatcher, error) {
		return OpenDataVersionWatcher(ctx, path)
	}, options...)
}

func run(
	st boardReader,
	databasePath string,
	user string,
	openWatcher watcherOpener,
	options ...tea.ProgramOption,
) (err error) {
	ctx, cancel := context.WithCancel(context.Background())
	watcher, err := openWatcher(ctx, databasePath)
	if err != nil {
		cancel()
		return err
	}
	defer func() {
		cancel()
		err = errors.Join(err, watcher.Close())
	}()
	model := newModel(st, watcher, user, ctx)
	preferencePath, preferencePathErr := tuiPreferencesPath(databasePath, user)
	if preferencePathErr == nil {
		model.boardView.showCancelled, model.preferenceErr = loadCancelledPreference(preferencePath)
	} else {
		model.preferenceErr = preferencePathErr
	}
	model.saveCancelled = func(show bool) error {
		if preferencePathErr != nil {
			return preferencePathErr
		}
		return saveCancelledPreference(preferencePath, show)
	}
	if _, err := tea.NewProgram(model, options...).Run(); err != nil {
		return fmt.Errorf("tui: run: %w", err)
	}
	return nil
}
