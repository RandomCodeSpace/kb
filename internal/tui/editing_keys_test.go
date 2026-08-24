package tui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/board"
)

// typeRunes sends one KeyPressMsg per rune through the root model, the way a
// terminal delivers typing.
func typeRunes(t *testing.T, model *Model, text string) {
	t.Helper()
	for _, r := range text {
		updateTestModel(t, model, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

// assertLineEditing drives the readline motions every bubbles text field binds
// by default. Home parks the cursor at the line start and End at the line end,
// so the sentinel runes bracket the typed text when the focused field - not a
// global shortcut - received the keys.
func assertLineEditing(t *testing.T, model *Model, value func() string, typed string) {
	t.Helper()
	typeRunes(t, model, typed)
	if got := value(); got != typed {
		t.Fatalf("typed value = %q, want %q", got, typed)
	}
	updateTestModel(t, model, tea.KeyPressMsg{Code: tea.KeyHome})
	typeRunes(t, model, "<")
	if got := value(); got != "<"+typed {
		t.Fatalf("after home = %q, want %q", got, "<"+typed)
	}
	updateTestModel(t, model, tea.KeyPressMsg{Code: tea.KeyEnd})
	typeRunes(t, model, ">")
	if got := value(); got != "<"+typed+">" {
		t.Fatalf("after end = %q, want %q", got, "<"+typed+">")
	}
}

func TestFilterInputReceivesLineEditingKeys(t *testing.T) {
	model := newTestRootModel(stubBoardReader{board: filterFixture()}, nil, "alice")
	completeBoardLoad(t, &model, model.Init())
	updateTestModel(t, &model, tea.KeyPressMsg{Code: '/', Text: "/"})
	if model.filter.focus != filterText {
		t.Fatalf("filter focus = %v, want text", model.filter.focus)
	}
	assertLineEditing(t, &model, func() string { return model.filter.input.Value() }, "login")
}

func TestKillReasonInputReceivesLineEditingKeys(t *testing.T) {
	model, _, tasks := actionTestModel(t, board.Task{Title: "Lifecycle", Status: board.StatusDone})
	model.boardView.focusTask(model.filteredBoard(), tasks[0].ID)
	model.recordShipped(tasks[0].ID)
	updateTestModel(t, &model, tea.KeyPressMsg{Code: 'x', Text: "x"})
	if model.action.mode != taskActionKill {
		t.Fatalf("action mode = %v, want kill", model.action.mode)
	}
	assertLineEditing(t, &model, func() string { return model.action.reason.Value() }, "stale")
}

func TestSettingsInputReceivesLineEditingKeys(t *testing.T) {
	backend := newSettingsTestStore(t)
	model := newTestRootModel(backend, nil, "alice")
	completeBoardLoad(t, &model, model.Init())
	model.settingsNew = func() *settingsModel {
		return newSettingsModel(backend, "alice", context.Background())
	}
	updateTestModel(t, &model, tea.KeyPressMsg{Code: 's', Text: "s"})
	if model.settings == nil {
		t.Fatal("settings did not open")
	}
	loadSettingsForTest(t, model.settings)
	model.settings.focus = "ai:base"
	model.settings.applyFocus()
	assertLineEditing(t, &model, func() string { return model.settings.aiBase.Value() }, "host")
}

// An integration row shares the settings key path with the AI block, but adds
// a kind control that owns left and right. That stepper must not reach the row
// text inputs, which need the arrows for cursor motion.
func TestIntegrationRowInputReceivesLineEditingKeys(t *testing.T) {
	backend := newSettingsTestStore(t)
	settings := newSettingsModel(backend, "alice", context.Background())
	loadSettingsForTest(t, settings)
	settings.addForgeDraft()
	row, _ := settings.focusedRow()
	if row == nil {
		t.Fatal("draft row not focused")
	}
	settings.focus = "forge:" + row.id + ":base"
	settings.applyFocus()

	for _, r := range "kb.example" {
		settings.updateKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	settings.updateKey(tea.KeyPressMsg{Code: tea.KeyHome})
	settings.updateKey(tea.KeyPressMsg{Code: '<', Text: "<"})
	settings.updateKey(tea.KeyPressMsg{Code: tea.KeyEnd})
	settings.updateKey(tea.KeyPressMsg{Code: '>', Text: ">"})
	settings.updateKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	settings.updateKey(tea.KeyPressMsg{Code: '!', Text: "!"})

	row, _ = settings.focusedRow()
	if got := row.baseURL.Value(); got != "<kb.example!>" {
		t.Fatalf("integration base = %q, want %q", got, "<kb.example!>")
	}
	if row.kind != "gitlab" {
		t.Fatalf("row kind = %q, want gitlab unchanged", row.kind)
	}
}
