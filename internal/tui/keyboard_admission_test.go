package tui

import (
	"fmt"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/board"
)

func keyboardTestModel(t testing.TB, count int) Model {
	t.Helper()
	tasks := make([]board.Task, count)
	for index := range tasks {
		tasks[index] = board.Task{
			ID:       fmt.Sprintf("task-%02d", index),
			Title:    fmt.Sprintf("Task %02d", index),
			Status:   board.StatusTodo,
			Position: index,
		}
	}
	fixture := board.Board{Title: "Keyboard admission", Tasks: tasks}
	model := newTestRootModel(stubBoardReader{board: fixture}, nil, "keyboard")
	updated, _ := model.Update(boardLoadedMsg{board: fixture})
	model = updated.(Model)
	return model
}

func newTestKeyboardAdmission(model *Model) (*keyboardAdmission, *stepClock) {
	clock := &stepClock{at: time.Unix(0, 0)}
	return newKeyboardAdmission(clock.now, model.inputAdmission, model.themeStyles().Timing), clock
}

func filterAndUpdateKeyboard(t *testing.T, admission *keyboardAdmission, model *Model, message tea.Msg) (tea.Msg, tea.Cmd) {
	t.Helper()
	filtered := admission.Filter(*model, message)
	if filtered == nil {
		return nil, nil
	}
	updated, command := model.Update(filtered)
	*model = updated.(Model)
	return filtered, command
}

func selectedKeyboardTaskID(t *testing.T, model Model) string {
	t.Helper()
	selected, ok := model.selectedTask()
	if !ok {
		t.Fatal("keyboard model has no selected task")
	}
	return selected.ID
}

func TestKeyboardAdmissionBoundsHeldNavigationAndKeepsLatestTarget(t *testing.T) {
	model := keyboardTestModel(t, 10)
	admission, clock := newTestKeyboardAdmission(&model)
	before := model.RenderPlanStats()

	if filtered, _ := filterAndUpdateKeyboard(t, admission, &model, tea.KeyPressMsg{Code: tea.KeyDown}); filtered == nil {
		t.Fatal("first down press was not immediate")
	}
	if got := selectedKeyboardTaskID(t, model); got != "task-01" {
		t.Fatalf("first target = %q, want task-01", got)
	}
	for range 2 {
		clock.advance(10 * time.Millisecond)
		if filtered, _ := filterAndUpdateKeyboard(t, admission, &model, tea.KeyPressMsg{Code: tea.KeyDown, IsRepeat: true}); filtered != nil {
			t.Fatalf("sub-window repeat admitted as %T", filtered)
		}
	}
	clock.advance(30 * time.Millisecond)
	if filtered, _ := filterAndUpdateKeyboard(t, admission, &model, tea.KeyPressMsg{Code: tea.KeyDown, IsRepeat: true}); filtered == nil {
		t.Fatal("cadence-open repeat was not admitted")
	}
	if got := selectedKeyboardTaskID(t, model); got != "task-04" {
		t.Fatalf("coalesced target = %q, want latest task-04", got)
	}

	after := model.RenderPlanStats()
	if got := after.AcceptedMessageID - before.AcceptedMessageID; got != 2 {
		t.Fatalf("accepted messages = %d, want 2", got)
	}
	if got := after.DiscardedEvents - before.DiscardedEvents; got != 2 {
		t.Fatalf("discarded events = %d, want 2", got)
	}
}

func TestKeyboardAdmissionDiscardsClampedBoundariesBeforeUpdate(t *testing.T) {
	model := keyboardTestModel(t, 3)
	admission, _ := newTestKeyboardAdmission(&model)
	before := model.RenderPlanStats()

	for _, press := range []tea.KeyPressMsg{
		{Code: tea.KeyUp},
		{Code: 'k', Text: "k"},
		{Code: tea.KeyUp, IsRepeat: true},
	} {
		if got := admission.Filter(model, press); got != nil {
			t.Fatalf("top boundary %q admitted as %T", press.String(), got)
		}
	}
	if admission.active {
		t.Fatal("top boundary retained pending navigation")
	}

	model.boardView.setCursorAtFrom(model.currentProjection(), 0, 2)
	model.rebuildRenderPlan(renderImpactAll)
	publishedAfterPosition := model.RenderPlanStats().PublishedFrames
	for _, press := range []tea.KeyPressMsg{
		{Code: tea.KeyDown},
		{Code: 'j', Text: "j"},
		{Code: tea.KeyDown, IsRepeat: true},
	} {
		if got := admission.Filter(model, press); got != nil {
			t.Fatalf("bottom boundary %q admitted as %T", press.String(), got)
		}
	}
	if admission.active {
		t.Fatal("bottom boundary retained pending navigation")
	}
	after := model.RenderPlanStats()
	if after.AcceptedMessageID != before.AcceptedMessageID || after.PublishedFrames != publishedAfterPosition {
		t.Fatalf("boundary entered Update: before=%+v after=%+v", before, after)
	}
	if got := after.DiscardedEvents - before.DiscardedEvents; got != 6 {
		t.Fatalf("boundary discarded events = %d, want 6", got)
	}
}

func TestKeyboardAdmissionReversalIsImmediateFromCommittedSelection(t *testing.T) {
	model := keyboardTestModel(t, 6)
	admission, clock := newTestKeyboardAdmission(&model)
	filterAndUpdateKeyboard(t, admission, &model, tea.KeyPressMsg{Code: tea.KeyDown})
	clock.advance(10 * time.Millisecond)
	filterAndUpdateKeyboard(t, admission, &model, tea.KeyPressMsg{Code: tea.KeyDown, IsRepeat: true})
	clock.advance(10 * time.Millisecond)

	filtered, _ := filterAndUpdateKeyboard(t, admission, &model, tea.KeyPressMsg{Code: tea.KeyUp, IsRepeat: true})
	if filtered == nil {
		t.Fatal("direction reversal waited for the cadence")
	}
	if got := selectedKeyboardTaskID(t, model); got != "task-00" {
		t.Fatalf("reversal target = %q, want committed predecessor task-00", got)
	}
}

func TestKeyboardAdmissionReleaseAndQuietNeverReplayPendingTarget(t *testing.T) {
	t.Run("enhanced release", func(t *testing.T) {
		model := keyboardTestModel(t, 6)
		admission, clock := newTestKeyboardAdmission(&model)
		filterAndUpdateKeyboard(t, admission, &model, tea.KeyPressMsg{Code: tea.KeyDown})
		clock.advance(10 * time.Millisecond)
		filterAndUpdateKeyboard(t, admission, &model, tea.KeyPressMsg{Code: tea.KeyDown, IsRepeat: true})

		if got := admission.Filter(model, tea.KeyReleaseMsg{Code: tea.KeyDown}); got != nil {
			t.Fatalf("release reached Update as %T", got)
		}
		clock.advance(time.Second)
		if got := admission.Filter(model, tea.KeyPressMsg{Code: tea.KeyDown, IsRepeat: true}); got != nil {
			t.Fatalf("post-release repeat admitted as %T", got)
		}
		if got := selectedKeyboardTaskID(t, model); got != "task-01" {
			t.Fatalf("release replayed pending target: %q", got)
		}
	})

	t.Run("legacy quiet", func(t *testing.T) {
		model := keyboardTestModel(t, 6)
		admission, clock := newTestKeyboardAdmission(&model)
		filterAndUpdateKeyboard(t, admission, &model, tea.KeyPressMsg{Code: tea.KeyDown})
		clock.advance(10 * time.Millisecond)
		filterAndUpdateKeyboard(t, admission, &model, tea.KeyPressMsg{Code: tea.KeyDown})
		clock.advance(model.themeStyles().Timing.KeyboardNavigationQuiet)

		filtered, _ := filterAndUpdateKeyboard(t, admission, &model, tea.KeyPressMsg{Code: tea.KeyDown})
		if filtered == nil {
			t.Fatal("first legacy press after quiet was not immediate")
		}
		if got := selectedKeyboardTaskID(t, model); got != "task-02" {
			t.Fatalf("quiet replayed old desired target before new press: %q", got)
		}
	})
}

func TestKeyboardAdmissionConsumesUnownedKeyReleasesBeforeUpdate(t *testing.T) {
	tests := []struct {
		name    string
		adapt   func(*Model)
		release tea.KeyReleaseMsg
	}{
		{name: "ordinary board key", release: tea.KeyReleaseMsg{Code: 'x', Text: "x"}},
		{name: "modal key", adapt: func(model *Model) { model.helpOpen = true }, release: tea.KeyReleaseMsg{Code: tea.KeyEscape}},
		{name: "text input key", adapt: func(model *Model) { model.filter.focus = filterText }, release: tea.KeyReleaseMsg{Code: 'a', Text: "a"}},
		{name: "exact action key", release: tea.KeyReleaseMsg{Code: 'q', Text: "q"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := keyboardTestModel(t, 4)
			if test.adapt != nil {
				test.adapt(&model)
				model.rebuildRenderPlan(renderImpactAll)
			}
			admission, _ := newTestKeyboardAdmission(&model)
			before := model.RenderPlanStats()

			filtered, command := filterAndUpdateKeyboard(t, admission, &model, test.release)
			after := model.RenderPlanStats()

			if filtered != nil || command != nil {
				t.Fatalf("release reached Update: filtered=%T command=%v", filtered, command)
			}
			if after.AcceptedMessageID != before.AcceptedMessageID || after.PublishedFrames != before.PublishedFrames {
				t.Fatalf("release changed render counters: before=%+v after=%+v", before, after)
			}
			if after.DiscardedEvents != before.DiscardedEvents+1 {
				t.Fatalf("discarded events = %d, want %d", after.DiscardedEvents, before.DiscardedEvents+1)
			}
		})
	}
}

func TestKeyboardAdmissionExactInputInvalidatesPendingWithoutDelay(t *testing.T) {
	model := keyboardTestModel(t, 6)
	admission, clock := newTestKeyboardAdmission(&model)
	filterAndUpdateKeyboard(t, admission, &model, tea.KeyPressMsg{Code: tea.KeyDown})
	clock.advance(10 * time.Millisecond)
	filterAndUpdateKeyboard(t, admission, &model, tea.KeyPressMsg{Code: tea.KeyDown, IsRepeat: true})

	for _, press := range []tea.KeyPressMsg{
		{Code: tea.KeyRight},
		{Code: tea.KeyTab},
	} {
		if got := admission.Filter(model, press); got != tea.Msg(press) {
			t.Fatalf("exact key %q changed to %T", press.String(), got)
		}
	}

	quit := tea.KeyPressMsg{Code: 'q', Text: "q"}
	filtered, command := filterAndUpdateKeyboard(t, admission, &model, quit)
	if filtered != tea.Msg(quit) || command == nil || !model.stopped {
		t.Fatalf("quit inside 100ms = filtered:%T command:%v stopped:%t", filtered, command, model.stopped)
	}
}

func TestKeyboardAdmissionLeavesModalMoveAndTextInputExact(t *testing.T) {
	tests := []struct {
		name  string
		adapt func(*Model)
	}{
		{name: "help modal", adapt: func(model *Model) { model.helpOpen = true }},
		{name: "filter text", adapt: func(model *Model) { model.filter.focus = filterText }},
		{name: "move preview", adapt: func(model *Model) {
			task, _ := model.selectedTask()
			model.move.beginVisible(model.board, model.filteredBoard(), task, model.boardView.visibleStatuses(), false)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := keyboardTestModel(t, 4)
			test.adapt(&model)
			model.rebuildRenderPlan(renderImpactAll)
			admission, _ := newTestKeyboardAdmission(&model)
			for _, press := range []tea.KeyPressMsg{
				{Code: tea.KeyDown, IsRepeat: true},
				{Code: 'j', Text: "j", IsRepeat: true},
			} {
				if got := admission.Filter(model, press); got != tea.Msg(press) {
					t.Fatalf("%q changed to %T", press.String(), got)
				}
			}
		})
	}
}

func TestKeyboardAdmissionRejectsStaleGenerationAndEpoch(t *testing.T) {
	model := keyboardTestModel(t, 6)
	admission, _ := newTestKeyboardAdmission(&model)
	intent, ok := admission.Filter(model, tea.KeyPressMsg{Code: tea.KeyDown}).(boardNavigationIntentMsg)
	if !ok {
		t.Fatal("first press did not produce a navigation intent")
	}

	updated, _ := model.Update(tea.WindowSizeMsg{Width: model.width + 1, Height: model.height})
	model = updated.(Model)
	before := model.RenderPlanStats()
	updated, command := model.Update(intent)
	model = updated.(Model)
	after := model.RenderPlanStats()
	if command != nil || selectedKeyboardTaskID(t, model) != "task-00" {
		t.Fatal("stale generation moved selection")
	}
	if after.AcceptedMessageID != before.AcceptedMessageID || after.PublishedFrames != before.PublishedFrames ||
		after.DiscardedEvents != before.DiscardedEvents+1 {
		t.Fatalf("stale intent accounting: before=%+v after=%+v", before, after)
	}

	intent, ok = admission.Filter(model, tea.KeyPressMsg{Code: tea.KeyDown}).(boardNavigationIntentMsg)
	if !ok {
		t.Fatal("fresh press did not produce a navigation intent")
	}
	if got := admission.Filter(model, tea.KeyPressMsg{Code: '?', Text: "?"}); got == nil {
		t.Fatal("exact modal key was swallowed")
	}
	before = model.RenderPlanStats()
	updated, _ = model.Update(intent)
	model = updated.(Model)
	after = model.RenderPlanStats()
	if after.AcceptedMessageID != before.AcceptedMessageID || after.DiscardedEvents != before.DiscardedEvents+1 {
		t.Fatalf("stale epoch entered Update: before=%+v after=%+v", before, after)
	}
}

func TestKeyboardAdmissionQueueIsConstantUnderBurst(t *testing.T) {
	model := keyboardTestModel(t, 100)
	admission, clock := newTestKeyboardAdmission(&model)
	filterAndUpdateKeyboard(t, admission, &model, tea.KeyPressMsg{Code: tea.KeyDown})
	before := model.RenderPlanStats()
	for range 1000 {
		if got := admission.Filter(model, tea.KeyPressMsg{Code: tea.KeyDown, IsRepeat: true}); got != nil {
			t.Fatalf("zero-time burst admitted %T", got)
		}
	}
	if !admission.active || admission.desiredTaskID != "task-99" {
		t.Fatalf("burst state = active:%t desired:%q", admission.active, admission.desiredTaskID)
	}
	clock.advance(model.themeStyles().Timing.KeyboardNavigationInterval)
	filtered, _ := filterAndUpdateKeyboard(t, admission, &model, tea.KeyPressMsg{Code: tea.KeyDown, IsRepeat: true})
	if filtered == nil || selectedKeyboardTaskID(t, model) != "task-99" {
		t.Fatal("cadence-open burst did not install its one latest target")
	}
	after := model.RenderPlanStats()
	if got := after.AcceptedMessageID - before.AcceptedMessageID; got != 1 {
		t.Fatalf("burst accepted messages = %d, want 1", got)
	}
	if got := after.DiscardedEvents - before.DiscardedEvents; got != 1000 {
		t.Fatalf("burst discarded events = %d, want 1000", got)
	}
}

func TestTUIViewRequestsEnhancedKeyEvents(t *testing.T) {
	model := keyboardTestModel(t, 2)
	if !model.View().KeyboardEnhancements.ReportEventTypes {
		t.Fatal("TUI view did not request repeat and release event types")
	}
}

func BenchmarkKeyboardAdmissionBoundaryBurst(b *testing.B) {
	model := keyboardTestModel(b, 1)
	admission := newKeyboardAdmission(time.Now, model.inputAdmission, model.themeStyles().Timing)
	message := tea.Msg(tea.KeyPressMsg{Code: tea.KeyDown})
	boxed := tea.Model(model)
	b.ReportAllocs()
	for b.Loop() {
		_ = admission.Filter(boxed, message)
	}
}
