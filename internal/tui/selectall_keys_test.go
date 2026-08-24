package tui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// ctrlAKey is the select-all binding of issue #159.
func ctrlAKey() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl} }

// assertSelectAllContract drives the whole behaviour contract of issue #159
// against one focused field: ctrl+a marks the content, the next typed rune
// replaces it, Backspace clears it, and a navigation key drops the mark without
// changing the content while still moving the cursor.
func assertSelectAllContract(t *testing.T, send func(tea.KeyPressMsg), value func() string, marked func() bool) {
	t.Helper()
	typed := func(text string) {
		for _, r := range text {
			send(tea.KeyPressMsg{Code: r, Text: string(r)})
		}
	}

	typed("login")
	send(ctrlAKey())
	if !marked() {
		t.Fatal("ctrl+a did not mark the field")
	}
	if got := value(); got != "login" {
		t.Fatalf("marking changed the value to %q", got)
	}

	typed("x")
	if got, mark := value(), marked(); got != "x" || mark {
		t.Fatalf("replace on type = %q marked:%v, want \"x\" marked:false", got, mark)
	}

	typed("yz")
	send(ctrlAKey())
	send(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if got, mark := value(), marked(); got != "" || mark {
		t.Fatalf("clear on backspace = %q marked:%v, want empty marked:false", got, mark)
	}

	typed("login")
	send(ctrlAKey())
	send(tea.KeyPressMsg{Code: tea.KeyHome})
	if got, mark := value(), marked(); got != "login" || mark {
		t.Fatalf("navigation = %q marked:%v, want \"login\" marked:false", got, mark)
	}
	typed("<")
	if got := value(); got != "<login" {
		t.Fatalf("the navigation key was swallowed: %q", got)
	}
}

func TestFilterInputSelectAll(t *testing.T) {
	model := newTestRootModel(stubBoardReader{board: filterFixture()}, nil, "alice")
	completeBoardLoad(t, &model, model.Init())
	updateTestModel(t, &model, tea.KeyPressMsg{Code: '/', Text: "/"})
	send := func(msg tea.KeyPressMsg) { updateTestModel(t, &model, msg) }

	assertSelectAllContract(t, send,
		func() string { return model.filter.input.Value() },
		func() bool { return model.filter.mark.Active(filterMarkField) })

	// Escape drops the mark and is consumed; the field keeps focus and only the
	// second Escape blurs it.
	send(ctrlAKey())
	send(tea.KeyPressMsg{Code: tea.KeyEscape})
	if model.filter.focus != filterText || model.filter.mark.Active(filterMarkField) {
		t.Fatalf("first escape = focus:%v marked:%v", model.filter.focus, model.filter.mark.Active(filterMarkField))
	}
	if got := model.filter.input.Value(); got != "<login" {
		t.Fatalf("escape changed the value to %q", got)
	}
	send(tea.KeyPressMsg{Code: tea.KeyEscape})
	if model.filter.focus != filterUnfocused {
		t.Fatalf("second escape = focus:%v, want unfocused", model.filter.focus)
	}
}

// Clearing a marked filter is a filter change like any other, so the board is
// re-projected rather than left showing the old projection.
func TestFilterSelectAllClearReprojectsTheBoard(t *testing.T) {
	model := newTestRootModel(stubBoardReader{board: filterFixture()}, nil, "alice")
	completeBoardLoad(t, &model, model.Init())
	updateTestModel(t, &model, tea.KeyPressMsg{Code: '/', Text: "/"})
	typeRunes(t, &model, "zzzz")
	if len(model.filteredBoard().Tasks) != 0 {
		t.Fatalf("filter matched %d tasks, want none", len(model.filteredBoard().Tasks))
	}
	updateTestModel(t, &model, ctrlAKey())
	updateTestModel(t, &model, tea.KeyPressMsg{Code: tea.KeyBackspace})
	if model.filter.active() {
		t.Fatal("the filter is still active after the marked text was cleared")
	}
	if len(model.filteredBoard().Tasks) != len(model.board.Tasks) {
		t.Fatalf("board shows %d of %d tasks after the clear",
			len(model.filteredBoard().Tasks), len(model.board.Tasks))
	}
}

func TestKillReasonInputSelectAll(t *testing.T) {
	model, _, tasks := actionTestModel(t, board.Task{Title: "Lifecycle", Status: board.StatusDone})
	model.boardView.focusTask(model.filteredBoard(), tasks[0].ID)
	model.recordShipped(tasks[0].ID)
	updateTestModel(t, &model, tea.KeyPressMsg{Code: 'x', Text: "x"})
	if model.action.mode != taskActionKill {
		t.Fatalf("action mode = %v, want kill", model.action.mode)
	}
	send := func(msg tea.KeyPressMsg) { updateTestModel(t, &model, msg) }

	assertSelectAllContract(t, send,
		func() string { return model.action.reason.Value() },
		func() bool { return model.action.mark.Active(killReasonField) })

	send(ctrlAKey())
	send(tea.KeyPressMsg{Code: tea.KeyEscape})
	if model.action.mode != taskActionKill || model.action.mark.Active(killReasonField) {
		t.Fatalf("first escape = mode:%v marked:%v", model.action.mode, model.action.mark.Active(killReasonField))
	}
	send(tea.KeyPressMsg{Code: tea.KeyEscape})
	if model.action.mode != taskActionClosed {
		t.Fatalf("second escape = mode:%v, want closed", model.action.mode)
	}
}

func TestSettingsInputSelectAll(t *testing.T) {
	backend := newSettingsTestStore(t)
	settings := newSettingsModel(backend, "alice", context.Background())
	loadSettingsForTest(t, settings)
	settings.focus = "ai:base"
	settings.applyFocus()
	send := func(msg tea.KeyPressMsg) { settings.updateKey(msg) }

	assertSelectAllContract(t, send,
		func() string { return settings.aiBase.Value() },
		func() bool { return settings.mark.Active("ai:base") })

	send(ctrlAKey())
	send(tea.KeyPressMsg{Code: tea.KeyEscape})
	if settings.closed || settings.mark.Active("ai:base") {
		t.Fatalf("first escape = closed:%v marked:%v", settings.closed, settings.mark.Active("ai:base"))
	}
	send(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !settings.closed {
		t.Fatal("second escape did not close the pane")
	}
}

// An integration row input shares the settings key path but sits behind the
// focused-row lookup, so it carries the contract separately. Tab moves focus,
// which drops the mark on the way out.
func TestIntegrationRowInputSelectAll(t *testing.T) {
	backend := newSettingsTestStore(t)
	settings := newSettingsModel(backend, "alice", context.Background())
	loadSettingsForTest(t, settings)
	settings.addForgeDraft()
	row, _ := settings.focusedRow()
	if row == nil {
		t.Fatal("draft row not focused")
	}
	target := "forge:" + row.id + ":base"
	settings.focus = target
	settings.applyFocus()
	send := func(msg tea.KeyPressMsg) { settings.updateKey(msg) }

	assertSelectAllContract(t, send,
		func() string { row, _ := settings.focusedRow(); return row.baseURL.Value() },
		func() bool { return settings.mark.Active(target) })

	send(ctrlAKey())
	if !settings.mark.Active(target) {
		t.Fatal("ctrl+a did not mark the integration input")
	}
	send(tea.KeyPressMsg{Code: tea.KeyTab})
	if settings.mark.Active(target) || settings.focus == target {
		t.Fatalf("tab left the mark = focus:%q marked:%v", settings.focus, settings.mark.Active(target))
	}
	row, _ = settings.focusedRow()
	if got := row.baseURL.Value(); got != "<login" {
		t.Fatalf("tab changed the marked value to %q", got)
	}
}

// TestSettingsMarkedFieldColorGolden pins the marked state's cell grid: the
// mark is an attribute, so only a truecolor golden records it.
func TestSettingsMarkedFieldColorGolden(t *testing.T) {
	st := newSettingsTestStore(t)
	base, modelName := "https://api.example/v1", "gpt-example"
	if _, err := st.SetAISettings("alice", &base, &modelName, nil); err != nil {
		t.Fatal(err)
	}
	settings := newSettingsModelWithBackends(st, &recordingAIProber{}, &recordingForgeProber{}, "alice", context.Background())
	loadSettingsForTest(t, settings)
	settings.focus = "ai:base"
	settings.applyFocus()
	settings.updateKey(ctrlAKey())
	golden.RequireEqual(t, []byte(theme.Downsample(settings.View(48, 12), theme.ColorProfile)))
}

// TestKillPromptMarkedReasonColorGolden is the same pin for the kill dialog,
// whose reason field is the other marked surface a golden already covers.
func TestKillPromptMarkedReasonColorGolden(t *testing.T) {
	model, _, tasks := actionTestModel(t, board.Task{Title: "Lifecycle", Status: board.StatusDone})
	model.width, model.height = 60, 14
	model.openKillPrompt(tasks[0])
	model.action.reason.SetValue("superseded by the SSO work")
	updateTestModel(t, &model, ctrlAKey())
	content := model.taskActionSurface(actionBackground(model.width, model.height)).Content
	golden.RequireEqual(t, []byte(theme.Downsample(content, theme.ColorProfile)))
}
