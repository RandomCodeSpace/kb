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
	ShowCancelled bool `json:"show_cancelled"`
}

type cancelledPreferenceSavedMsg struct {
	show bool
	err  error
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

func loadCancelledPreference(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("tui preferences: read: %w", err)
	}
	var preferences tuiPreferences
	if err := json.Unmarshal(data, &preferences); err != nil {
		return false, fmt.Errorf("tui preferences: decode: %w", err)
	}
	return preferences.ShowCancelled, nil
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

func saveCancelledPreference(path string, show bool) error {
	return saveCancelledPreferenceWithOps(path, show, osPreferenceFileOps)
}

func saveCancelledPreferenceWithOps(path string, show bool, ops preferenceFileOps) error {
	dir := filepath.Dir(path)
	if err := ops.mkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("tui preferences: create directory: %w", err)
	}
	data, err := json.Marshal(tuiPreferences{ShowCancelled: show})
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

func (m *Model) queueCancelledPreference() tea.Cmd {
	if m.saveCancelled == nil {
		return nil
	}
	show := m.boardView.showCancelled
	if m.prefSaving {
		m.prefPending = &show
		return nil
	}
	m.prefSaving = true
	return m.writeCancelledPreference(show)
}

func (m Model) writeCancelledPreference(show bool) tea.Cmd {
	return func() tea.Msg {
		return cancelledPreferenceSavedMsg{show: show, err: m.saveCancelled(show)}
	}
}

func (m *Model) finishCancelledPreference(message cancelledPreferenceSavedMsg) tea.Cmd {
	m.prefSaving = false
	m.preferenceErr = message.err
	if m.prefPending == nil {
		return nil
	}
	show := *m.prefPending
	m.prefPending = nil
	if show == message.show {
		return nil
	}
	m.prefSaving = true
	return m.writeCancelledPreference(show)
}
