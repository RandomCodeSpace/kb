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
// activeProject is the project local commands default to, resolved by the
// caller that owns the data directory; it opens the switcher and defaults the
// editor's mandatory project field, and may be empty. Program options are
// accepted for deterministic tests; production passes none.
func Run(st *store.Store, databasePath, user, activeProject string, options ...tea.ProgramOption) (err error) {
	return runWithSettings(st, databasePath, user, activeProject, func(ctx context.Context, path string) (versionWatcher, error) {
		return OpenDataVersionWatcher(ctx, path)
	}, options...)
}

func runWithSettings(
	st *store.Store,
	databasePath string,
	user string,
	activeProject string,
	openWatcher watcherOpener,
	options ...tea.ProgramOption,
) (err error) {
	runner := ai.NewRunner(st, filepath.Join(filepath.Dir(databasePath), "skills"), nil, nil)
	return runProgram(st, databasePath, user, activeProject, openWatcher, func(ctx context.Context) *settingsModel {
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
	return runProgram(st, databasePath, user, "", openWatcher, nil, nil, options...)
}

func runProgram(
	st boardReader,
	databasePath string,
	user string,
	activeProject string,
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
	model.configureAI(aiRunner, ctx)
	model.SetActiveProject(activeProject)
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
