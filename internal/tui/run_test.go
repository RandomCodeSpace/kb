package tui

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Ambiguous legacy files used to poison savePreferences for the whole session,
// so the stable file could never be written and the board was stuck on the
// notice forever (issue #291). The wiring must raise the notice once, keep the
// legacy files untouched, and still publish the stable file on the next save.
func TestRunAmbiguousLegacyPreferencesStillSavesStableFile(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "kb.db")
	preferenceDir := filepath.Join(root, ".kb-tui")
	var legacyPaths []string
	for _, identity := range []string{"first", "second"} {
		otherPath, err := legacyTUIPreferencesPath(filepath.Join(t.TempDir(), "kb.db"), identity)
		if err != nil {
			t.Fatal(err)
		}
		candidate := filepath.Join(preferenceDir, filepath.Base(otherPath))
		if err := saveTUIPreferences(candidate, tuiPreferences{Project: identity}); err != nil {
			t.Fatal(err)
		}
		legacyPaths = append(legacyPaths, candidate)
	}
	legacyBefore := make([][]byte, len(legacyPaths))
	for index, path := range legacyPaths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		legacyBefore[index] = data
	}

	// The control resolves against an empty directory, so it carries exactly
	// the state a board with no stored preferences opens with.
	control := newTestRootModel(stubBoardReader{}, nil, "default")
	control.installPreferences(filepath.Join(t.TempDir(), "kb.db"), "default")

	m := newTestRootModel(stubBoardReader{}, nil, "default")
	m.installPreferences(databasePath, "default")
	if !errors.Is(m.preferenceErr, errAmbiguousLegacyPreferences) ||
		!strings.Contains(m.preferenceErr.Error(), "using defaults instead of guessing") {
		t.Fatalf("ambiguous legacy notice = %v", m.preferenceErr)
	}
	if m.boardView.showCancelled != control.boardView.showCancelled ||
		m.projects != control.projects || m.filter.value().Text != control.filter.value().Text {
		t.Fatalf("ambiguous legacy files guessed a scope: cancelled:%v projects:%+v filter:%q",
			m.boardView.showCancelled, m.projects, m.filter.value().Text)
	}

	want := tuiPreferences{ShowCancelled: true, Project: "chosen"}
	if err := m.savePreferences(want); err != nil {
		t.Fatalf("save with ambiguous legacy files: %v", err)
	}
	stablePath, err := tuiPreferencesPath(databasePath, "default")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := loadTUIPreferences(stablePath); err != nil || !preferencesEqual(got, want) {
		t.Fatalf("stable preference = %+v, %v", got, err)
	}
	for index, path := range legacyPaths {
		data, err := os.ReadFile(path)
		if err != nil || !reflect.DeepEqual(data, legacyBefore[index]) {
			t.Fatalf("legacy file %s changed: %q, %v", path, data, err)
		}
	}

	// The sticky alert clears the way every other preference failure does: the
	// next completed save replaces it.
	if command := m.finishPreferences(preferenceSavedMsg{preferences: want}); command != nil {
		t.Fatalf("finished save queued %v", command)
	}
	if m.preferenceErr != nil {
		t.Fatalf("notice survived a successful save: %v", m.preferenceErr)
	}
}
