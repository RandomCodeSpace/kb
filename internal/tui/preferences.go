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

	tea "charm.land/bubbletea/v2"
)

type tuiPreferences struct {
	ShowCancelled bool          `json:"show_cancelled"`
	Filter        boardFilter   `json:"filter,omitempty"`
	Shipped       shippedRecord `json:"shipped,omitempty"`
}

type preferenceSavedMsg struct {
	preferences tuiPreferences
	err         error
}

// tuiPreferencesPath keeps display state beside its SQLite board. Hashing the
// canonical configured database path together with the sanitized board owner
// isolates both alternate --data paths and KB_USER namespaces without putting
// user-controlled text in a filename.
func tuiPreferencesPath(databasePath, user string) (string, error) {
	databasePath, err := filepath.Abs(databasePath)
	if err != nil {
		return "", fmt.Errorf("tui preferences: database path: %w", err)
	}
	databasePath = filepath.Clean(databasePath)
	identity := sha256.Sum256([]byte(databasePath + "\x00" + user))
	name := hex.EncodeToString(identity[:16]) + ".json"
	return filepath.Join(filepath.Dir(databasePath), ".kb-tui", name), nil
}

func loadTUIPreferences(path string) (tuiPreferences, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return tuiPreferences{}, nil
	}
	if err != nil {
		return tuiPreferences{}, fmt.Errorf("tui preferences: read: %w", err)
	}
	var preferences tuiPreferences
	if err := json.Unmarshal(data, &preferences); err != nil {
		return tuiPreferences{}, fmt.Errorf("tui preferences: decode: %w", err)
	}
	preferences.Filter.Tags = normalizedFilterTags(preferences.Filter.Tags)
	return preferences, nil
}

func (m *Model) restorePreferences(path string) {
	preferences, err := loadTUIPreferences(path)
	m.preferenceErr = err
	if err != nil {
		return
	}
	m.boardView.showCancelled = preferences.ShowCancelled
	m.filter.restore(preferences.Filter)
	m.shipped = preferences.Shipped
	m.normalizeShipped()
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
		Shipped:       m.shipped,
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
