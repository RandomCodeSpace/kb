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
// editor's mandatory project field, and may be empty. version is the build
// identifier the launch screen's meta row prints (spec section 10.6.5); the
// TUI cannot reach package main's build info, so the caller passes it in and an
// empty string renders the meta row with its left slot only. Program options
// are accepted for deterministic tests; production passes none.
func Run(
	st *store.Store,
	databasePath, user, activeProject, version string,
	options ...tea.ProgramOption,
) (err error) {
	return runWithSettings(st, databasePath, user, activeProject, version, func(ctx context.Context, path string) (versionWatcher, error) {
		return OpenDataVersionWatcher(ctx, path)
	}, options...)
}

func runWithSettings(
	st *store.Store,
	databasePath string,
	user string,
	activeProject string,
	version string,
	openWatcher watcherOpener,
	options ...tea.ProgramOption,
) (err error) {
	runner := ai.NewRunner(st, filepath.Join(filepath.Dir(databasePath), "skills"), nil, nil)
	return runProgram(st, databasePath, user, activeProject, version, openWatcher, func(ctx context.Context) *settingsModel {
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
	return runProgram(st, databasePath, user, "", "", openWatcher, nil, nil, options...)
}

func runProgram(
	st boardReader,
	databasePath string,
	user string,
	activeProject string,
	version string,
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
	model.SetVersion(version)
	model.configureAI(aiRunner, ctx)
	model.SetActiveProject(activeProject)
	model.installPreferences(databasePath, user)
	if settingsNew != nil {
		model.settingsNew = func() *settingsModel { return settingsNew(ctx) }
	}
	// Pre-program wiring is the only state installation outside Update. Publish
	// it once after every setter and preference restore so the first View is the
	// same complete snapshot the event loop will retain thereafter.
	model.rebuildRenderPlan(renderImpactAll)
	keyboard := newKeyboardAdmission(nil, model.inputAdmission, model.themeStyles().Timing)
	filter := func(current tea.Model, message tea.Msg) tea.Msg {
		return keyboard.Filter(current, message)
	}
	program := tea.NewProgram(model, append(options, tea.WithFilter(filter))...)
	if _, err := program.Run(); err != nil {
		return fmt.Errorf("tui: run: %w", err)
	}
	return nil
}

// installPreferences wires the restored snapshot, the sticky notice a failed
// resolution raises, and the saver. Ambiguous legacy files are a notice, not a
// broken path: defaults are used, the legacy files are left alone, and saves
// still publish the stable file so the board can settle on one.
func (m *Model) installPreferences(databasePath, user string) {
	preferencePath, preferences, preferencePathErr := resolveTUIPreferences(databasePath, user)
	m.preferenceErr = preferencePathErr
	if preferencePathErr == nil {
		m.adoptPreferences(preferences)
	}
	saveErr := preferencePathErr
	if errors.Is(saveErr, errAmbiguousLegacyPreferences) {
		saveErr = nil
	}
	m.savePreferences = func(preferences tuiPreferences) error {
		if saveErr != nil {
			return saveErr
		}
		return saveTUIPreferences(preferencePath, preferences)
	}
}
