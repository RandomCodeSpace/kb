package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/ai"
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
	return runWithSettings(st, databasePath, user, func(ctx context.Context, path string) (versionWatcher, error) {
		return OpenDataVersionWatcher(ctx, path)
	}, options...)
}

func runWithSettings(
	st *store.Store,
	databasePath string,
	user string,
	openWatcher watcherOpener,
	options ...tea.ProgramOption,
) (err error) {
	runner := ai.NewRunner(st, filepath.Join(filepath.Dir(databasePath), "skills"), nil, nil)
	return runProgram(st, databasePath, user, openWatcher, func(ctx context.Context) *settingsModel {
		return newSettingsModel(st, user, ctx)
	}, runner, options...)
}

func run(
	st boardReader,
	databasePath string,
	user string,
	openWatcher watcherOpener,
	options ...tea.ProgramOption,
) (err error) {
	return runProgram(st, databasePath, user, openWatcher, nil, nil, options...)
}

func runProgram(
	st boardReader,
	databasePath string,
	user string,
	openWatcher watcherOpener,
	settingsNew func(context.Context) *settingsModel,
	aiRunner *ai.Runner,
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
	// The board file sits in the data directory, beside the state.json the
	// active project lives in.
	model.dataDir = filepath.Dir(databasePath)
	model.configureAI(aiRunner, ctx)
	preferencePath, preferencePathErr := tuiPreferencesPath(databasePath, user)
	if preferencePathErr == nil {
		model.restorePreferences(preferencePath)
	} else {
		model.preferenceErr = preferencePathErr
	}
	model.savePreferences = func(preferences tuiPreferences) error {
		if preferencePathErr != nil {
			return preferencePathErr
		}
		return saveTUIPreferences(preferencePath, preferences)
	}
	if settingsNew != nil {
		model.settingsNew = func() *settingsModel { return settingsNew(ctx) }
	}
	if _, err := tea.NewProgram(model, options...).Run(); err != nil {
		return fmt.Errorf("tui: run: %w", err)
	}
	return nil
}
