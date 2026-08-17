package tui

import (
	"encoding/json"
	"errors"
	"fmt"
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

func tuiPreferencesPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("tui preferences: config directory: %w", err)
	}
	return filepath.Join(dir, "kb", "tui.json"), nil
}

func loadCancelledPreference() (bool, error) {
	path, err := tuiPreferencesPath()
	if err != nil {
		return false, err
	}
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

func saveCancelledPreference(show bool) error {
	path, err := tuiPreferencesPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("tui preferences: create directory: %w", err)
	}
	data, err := json.Marshal(tuiPreferences{ShowCancelled: show})
	if err != nil {
		return fmt.Errorf("tui preferences: encode: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("tui preferences: write: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("tui preferences: permissions: %w", err)
	}
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
