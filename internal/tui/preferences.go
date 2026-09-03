package tui

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
)

type tuiPreferences struct {
	ShowCancelled bool          `json:"show_cancelled"`
	Filter        boardFilter   `json:"filter,omitempty"`
	Shipped       shippedRecord `json:"shipped,omitempty"`
	// Project is the switcher's scope. Both fields empty means nothing was
	// stored, which restores as the active project rather than as "all": a
	// board that has never been switched opens where the CLI points.
	Project    string `json:"project,omitempty"`
	ProjectAll bool   `json:"project_all,omitempty"`
}

type preferenceSavedMsg struct {
	preferences tuiPreferences
	err         error
}

// tuiPreferencesPath keeps the single local board's display state beside its
// SQLite database without binding it to an absolute path. A cold-copy restore
// can therefore move the whole data directory and retain the same view.
func tuiPreferencesPath(databasePath, _ string) (string, error) {
	databasePath, err := filepath.Abs(databasePath)
	if err != nil {
		return "", fmt.Errorf("tui preferences: database path: %w", err)
	}
	databasePath = filepath.Clean(databasePath)
	return filepath.Join(filepath.Dir(databasePath), ".kb-tui", "preferences.json"), nil
}

// legacyTUIPreferencesPath reproduces the exact pre-portability filename.
func legacyTUIPreferencesPath(databasePath, user string) (string, error) {
	databasePath, err := filepath.Abs(databasePath)
	if err != nil {
		return "", fmt.Errorf("tui preferences: database path: %w", err)
	}
	databasePath = filepath.Clean(databasePath)
	identity := sha256.Sum256([]byte(databasePath + "\x00" + user))
	name := hex.EncodeToString(identity[:16]) + ".json"
	return filepath.Join(filepath.Dir(databasePath), ".kb-tui", name), nil
}

// errAmbiguousLegacyPreferences marks the one resolution outcome that is a
// notice rather than a broken path: several legacy hash files sit beside the
// database and none can be attributed, so defaults are used. The stable file
// is still writable, so saves must keep publishing it (spec issue #284 step 4).
var errAmbiguousLegacyPreferences = errors.New("using defaults instead of guessing")

func resolveTUIPreferences(databasePath, user string) (string, tuiPreferences, error) {
	stablePath, err := tuiPreferencesPath(databasePath, user)
	if err != nil {
		return "", tuiPreferences{}, err
	}
	if preferences, found, err := readTUIPreferences(stablePath); err != nil || found {
		return stablePath, preferences, err
	}

	exactLegacyPath, err := legacyTUIPreferencesPath(databasePath, user)
	if err != nil {
		return stablePath, tuiPreferences{}, err
	}
	if preferences, found, err := readTUIPreferences(exactLegacyPath); err != nil {
		return stablePath, tuiPreferences{}, err
	} else if found {
		return stablePath, preferences, saveTUIPreferences(stablePath, preferences)
	}

	entries, err := os.ReadDir(filepath.Dir(stablePath))
	if errors.Is(err, os.ErrNotExist) {
		return stablePath, tuiPreferences{}, nil
	}
	if err != nil {
		return stablePath, tuiPreferences{}, fmt.Errorf("tui preferences: list legacy files: %w", err)
	}
	var candidates []string
	for _, entry := range entries {
		if !entry.IsDir() && isLegacyPreferenceName(entry.Name()) {
			candidates = append(candidates, filepath.Join(filepath.Dir(stablePath), entry.Name()))
		}
	}
	if len(candidates) == 0 {
		return stablePath, tuiPreferences{}, nil
	}
	if len(candidates) > 1 {
		return stablePath, tuiPreferences{}, fmt.Errorf(
			"tui preferences: found %d legacy preference files; %w", len(candidates), errAmbiguousLegacyPreferences)
	}
	preferences, found, err := readTUIPreferences(candidates[0])
	if err != nil || !found {
		return stablePath, tuiPreferences{}, err
	}
	return stablePath, preferences, saveTUIPreferences(stablePath, preferences)
}

func isLegacyPreferenceName(name string) bool {
	if len(name) != 37 || filepath.Ext(name) != ".json" {
		return false
	}
	encoded := strings.TrimSuffix(name, ".json")
	decoded, err := hex.DecodeString(encoded)
	return err == nil && len(decoded) == 16
}

func loadTUIPreferences(path string) (tuiPreferences, error) {
	preferences, _, err := readTUIPreferences(path)
	return preferences, err
}

func readTUIPreferences(path string) (tuiPreferences, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return tuiPreferences{}, false, nil
	}
	if err != nil {
		return tuiPreferences{}, false, fmt.Errorf("tui preferences: read: %w", err)
	}
	var preferences tuiPreferences
	if err := json.Unmarshal(data, &preferences); err != nil {
		return tuiPreferences{}, false, fmt.Errorf("tui preferences: decode: %w", err)
	}
	preferences.Filter.Tags = normalizedFilterTags(preferences.Filter.Tags)
	return preferences, true, nil
}

func (m *Model) restorePreferences(path string) {
	preferences, err := loadTUIPreferences(path)
	m.preferenceErr = err
	if err != nil {
		return
	}
	m.adoptPreferences(preferences)
}

// adoptPreferences applies a loaded snapshot. The project scope resolves last
// against the active project, so a board that has never been switched opens
// where the CLI points.
func (m *Model) adoptPreferences(preferences tuiPreferences) {
	m.boardView.showCancelled = preferences.ShowCancelled
	m.filter.restore(preferences.Filter)
	m.projects.restore(projectSwitcher{name: preferences.Project, all: preferences.ProjectAll}, m.activeProject)
	m.editor.SetProjectDefault(m.projectDefault())
	m.adoptShippedAt(preferences.Shipped, m.now())
}

type preferenceTempFile interface {
	io.Writer
	Name() string
	Sync() error
	Close() error
}

type preferenceFileOps struct {
	mkdirAll  func(string, os.FileMode) error
	createTmp func(string, string) (preferenceTempFile, error)
	rename    func(string, string) error
	remove    func(string) error
}

var osPreferenceFileOps = preferenceFileOps{
	mkdirAll: os.MkdirAll,
	createTmp: func(dir, pattern string) (preferenceTempFile, error) {
		return os.CreateTemp(dir, pattern)
	},
	rename: os.Rename,
	remove: os.Remove,
}

func saveTUIPreferences(path string, preferences tuiPreferences) error {
	return saveTUIPreferencesWithOps(path, preferences, osPreferenceFileOps)
}

func saveTUIPreferencesWithOps(path string, preferences tuiPreferences, ops preferenceFileOps) error {
	dir := filepath.Dir(path)
	if err := ops.mkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("tui preferences: create directory: %w", err)
	}
	preferences.Filter.Tags = normalizedFilterTags(preferences.Filter.Tags)
	data, err := json.Marshal(preferences)
	if err != nil {
		return fmt.Errorf("tui preferences: encode: %w", err)
	}
	data = append(data, '\n')

	temporary, err := ops.createTmp(dir, ".tui-preferences-*.tmp")
	if err != nil {
		return fmt.Errorf("tui preferences: create temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	published := false
	defer func() {
		if !published {
			_ = ops.remove(temporaryPath)
		}
	}()

	written, err := temporary.Write(data)
	if err != nil {
		_ = temporary.Close()
		return fmt.Errorf("tui preferences: write temporary file: %w", err)
	}
	if written != len(data) {
		_ = temporary.Close()
		return fmt.Errorf("tui preferences: write temporary file: %w", io.ErrShortWrite)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("tui preferences: sync temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("tui preferences: close temporary file: %w", err)
	}
	if err := ops.rename(temporaryPath, path); err != nil {
		return fmt.Errorf("tui preferences: publish: %w", err)
	}
	published = true
	return nil
}

func (m *Model) preferences() tuiPreferences {
	m.normalizeShipped()
	return tuiPreferences{
		ShowCancelled: m.boardView.showCancelled,
		Filter:        m.filter.value(),
		Shipped: shippedRecord{
			Date: m.shipped.Date, IDs: append([]string(nil), m.shipped.IDs...),
		},
		Project:    m.projects.name,
		ProjectAll: m.projects.all,
	}
}

func (m *Model) queuePreferences() tea.Cmd {
	if m.savePreferences == nil {
		return nil
	}
	preferences := m.preferences()
	if m.prefSaving {
		m.prefPending = &preferences
		return nil
	}
	m.prefSaving = true
	return m.writePreferences(preferences)
}

func (m Model) writePreferences(preferences tuiPreferences) tea.Cmd {
	return func() tea.Msg {
		return preferenceSavedMsg{preferences: preferences, err: m.savePreferences(preferences)}
	}
}

func (m *Model) finishPreferences(message preferenceSavedMsg) tea.Cmd {
	m.prefSaving = false
	m.preferenceErr = message.err
	if m.prefPending == nil {
		return nil
	}
	preferences := *m.prefPending
	m.prefPending = nil
	if message.err == nil && preferencesEqual(preferences, message.preferences) {
		return nil
	}
	m.prefSaving = true
	return m.writePreferences(preferences)
}

func preferencesEqual(left, right tuiPreferences) bool {
	if left.ShowCancelled != right.ShowCancelled || left.Filter.Text != right.Filter.Text ||
		left.Project != right.Project || left.ProjectAll != right.ProjectAll ||
		left.Shipped.Date != right.Shipped.Date || len(left.Filter.Tags) != len(right.Filter.Tags) ||
		len(left.Shipped.IDs) != len(right.Shipped.IDs) {
		return false
	}
	for i := range left.Filter.Tags {
		if left.Filter.Tags[i] != right.Filter.Tags[i] {
			return false
		}
	}
	for i := range left.Shipped.IDs {
		if left.Shipped.IDs[i] != right.Shipped.IDs[i] {
			return false
		}
	}
	return true
}
