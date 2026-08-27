package tui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/ai"
	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
	"github.com/RandomCodeSpace/kb/internal/tui/issueimport"
	"github.com/RandomCodeSpace/kb/internal/tui/pointer"
)

func pointerControlHit(t *testing.T, model Model, status board.Status) boardHit {
	t.Helper()
	for _, hit := range model.current.semantics.hits {
		if hit.kind == boardHitColumnHeading && hit.status == status && hit.x0 < hit.x1 && hit.y0 < hit.y1 {
			return hit
		}
	}
	t.Fatalf("column control for %s not found", status)
	return boardHit{}
}

func dispatchRenderedPointer(t *testing.T, model Model, raw tea.MouseMsg) (Model, modelUpdateCommands) {
	t.Helper()
	handler := model.View().OnMouse
	if handler == nil {
		t.Fatal("rendered view has no pointer handler")
	}
	if command := handler(raw); command != nil {
		t.Fatal("rendered pointer resolver returned an asynchronous command")
	}
	next, commands := model.updateWithCommands(raw)
	return next, commands
}

func TestRenderedPointerPressAndReleaseStayOrderedAcrossHandlerReplacement(t *testing.T) {
	model := performanceModel(120, "", performanceWidth, performanceHeight)
	hit := pointerControlHit(t, model, board.StatusDoing)
	press := tea.MouseClickMsg{X: hit.x0, Y: hit.y0, Button: tea.MouseLeft}
	release := tea.MouseReleaseMsg{X: hit.x0, Y: hit.y0, Button: tea.MouseLeft}

	model, commands := dispatchRenderedPointer(t, model, press)
	if commands.followUp != nil || !model.pointerState.Active() {
		t.Fatalf("press commands=%#v active=%t, want synchronous capture only", commands, model.pointerState.Active())
	}

	// The press publishes a new frame and therefore replaces OnMouse. Release
	// must still resolve against the current frame and activate the capture in
	// this raw Update, not on a command goroutine.
	model, commands = dispatchRenderedPointer(t, model, release)
	if commands.followUp != nil || model.pointerState.Active() {
		t.Fatalf("release commands=%#v active=%t, want synchronous release", commands, model.pointerState.Active())
	}
	if got := model.boardView.column; got != statusIndex(board.StatusDoing) {
		t.Fatalf("selected column = %d, want Doing", got)
	}
}

func TestRenderedPointerRawMismatchFailsClosed(t *testing.T) {
	model := performanceModel(120, "", performanceWidth, performanceHeight)
	hit := pointerControlHit(t, model, board.StatusDoing)
	handler := model.View().OnMouse
	if command := handler(tea.MouseClickMsg{X: hit.x0, Y: hit.y0, Button: tea.MouseLeft}); command != nil {
		t.Fatal("resolver returned an asynchronous command")
	}

	before := model.RenderPlanStats().InstalledSnapshotID
	next, commands := model.updateWithCommands(tea.MouseClickMsg{X: hit.x0 + 1, Y: hit.y0, Button: tea.MouseLeft})
	if commands.followUp != nil || commands.geometry != nil || next.pointerState.Active() {
		t.Fatalf("mismatched raw input escaped: commands=%#v active=%t", commands, next.pointerState.Active())
	}
	if got := next.RenderPlanStats().InstalledSnapshotID; got != before {
		t.Fatalf("idle mismatch published %d frames, want 0", got-before)
	}
}

func TestRenderedPointerMailboxOverflowFailsClosed(t *testing.T) {
	model := performanceModel(120, "", performanceWidth, performanceHeight)
	hit := pointerControlHit(t, model, board.StatusDoing)
	handler := model.View().OnMouse
	first := tea.MouseClickMsg{X: hit.x0, Y: hit.y0, Button: tea.MouseLeft}
	second := tea.MouseClickMsg{X: hit.x0 + 1, Y: hit.y0, Button: tea.MouseLeft}
	if handler(first) != nil || handler(second) != nil {
		t.Fatal("resolver returned an asynchronous command")
	}

	next, commands := model.updateWithCommands(second)
	if commands.followUp != nil || commands.geometry != nil || next.pointerState.Active() {
		t.Fatalf("overflowed pointer input escaped: commands=%#v active=%t", commands, next.pointerState.Active())
	}
}

func TestModelCopiesShareRenderedPointerMailbox(t *testing.T) {
	model := performanceModel(120, "", performanceWidth, performanceHeight)
	copyOfModel := model
	hit := pointerControlHit(t, model, board.StatusDoing)
	press := tea.MouseClickMsg{X: hit.x0, Y: hit.y0, Button: tea.MouseLeft}
	if command := model.View().OnMouse(press); command != nil {
		t.Fatal("resolver returned an asynchronous command")
	}

	next, _ := copyOfModel.updateWithCommands(press)
	if !next.pointerState.Active() {
		t.Fatal("model copy did not consume the shared mailbox result")
	}
}

func TestOldVisibleHandlerStableBoardControlSurvivesNewerPlan(t *testing.T) {
	model := performanceModel(120, "", performanceWidth, performanceHeight)
	hit := pointerBoardHit(t, model, func(hit boardHit) bool {
		return hit.kind == boardHitFilterLabel && hit.taskID != "" && hit.tag != ""
	})
	handler := model.View().OnMouse
	press := tea.MouseClickMsg{X: hit.x0, Y: hit.y0, Button: tea.MouseLeft}
	release := tea.MouseReleaseMsg{X: hit.x0, Y: hit.y0, Button: tea.MouseLeft}
	model, _ = dispatchFlushedPointer(t, model, handler, press)
	model.rebuildRenderPlan(renderImpactAll)
	model, _ = dispatchFlushedPointer(t, model, handler, release)
	if !model.filter.hasTag(hit.tag) {
		t.Fatalf("stable old-generation control did not activate label %q", hit.tag)
	}
}

func TestOldVisibleHandlerRemovedControlAtReusedCoordinateDoesNotActivate(t *testing.T) {
	model := performanceModel(120, "", performanceWidth, performanceHeight)
	hit := pointerBoardHit(t, model, func(hit boardHit) bool {
		return hit.kind == boardHitFilterLabel && hit.taskID != "" && hit.tag != ""
	})
	handler := model.View().OnMouse
	press := tea.MouseClickMsg{X: hit.x0, Y: hit.y0, Button: tea.MouseLeft}
	release := tea.MouseReleaseMsg{X: hit.x0, Y: hit.y0, Button: tea.MouseLeft}
	model, _ = dispatchFlushedPointer(t, model, handler, press)
	for index := range model.board.Tasks {
		if model.board.Tasks[index].ID == hit.taskID {
			model.board.Tasks[index].ID = "replacement-task-at-the-same-cell"
			break
		}
	}
	model.rebuildRenderPlan(renderImpactAll)
	current := pointerBoardHit(t, model, func(candidate boardHit) bool {
		return candidate.x0 == hit.x0 && candidate.y0 == hit.y0 && candidate.kind == hit.kind
	})
	if current.taskID == hit.taskID {
		t.Fatal("fixture did not replace the control identity at the reused coordinate")
	}
	model, _ = dispatchFlushedPointer(t, model, handler, release)
	if model.filter.hasTag(hit.tag) {
		t.Fatalf("removed old-generation control activated label %q", hit.tag)
	}
	if model.pointerState.Active() || model.pointerAdmission.captureKey != "" {
		t.Fatal("removed old-generation control retained capture")
	}
}

func TestPrePressHandlerReleaseStillOpensSameStableTask(t *testing.T) {
	model := performanceModel(120, "", performanceWidth, performanceHeight)
	hit := pointerBoardHit(t, model, func(hit boardHit) bool {
		return hit.kind == boardHitDefault && hit.taskID != ""
	})
	handler := model.View().OnMouse
	model, _ = dispatchFlushedPointer(t, model, handler,
		tea.MouseClickMsg{X: hit.x0, Y: hit.y0, Button: tea.MouseLeft})
	model, _ = dispatchFlushedPointer(t, model, handler,
		tea.MouseReleaseMsg{X: hit.x0, Y: hit.y0, Button: tea.MouseLeft})
	if !model.detail.IsOpen() || model.detail.TaskID() != hit.taskID {
		t.Fatalf("pre-press release detail open=%t task=%q, want %q", model.detail.IsOpen(), model.detail.TaskID(), hit.taskID)
	}
}

func TestPrePressHandlerRemovedTaskDoesNotOpenCoordinateReplacement(t *testing.T) {
	model := performanceModel(120, "", performanceWidth, performanceHeight)
	hit := pointerBoardHit(t, model, func(hit boardHit) bool {
		return hit.kind == boardHitDefault && hit.taskID != ""
	})
	handler := model.View().OnMouse
	model, _ = dispatchFlushedPointer(t, model, handler,
		tea.MouseClickMsg{X: hit.x0, Y: hit.y0, Button: tea.MouseLeft})
	for index := range model.board.Tasks {
		if model.board.Tasks[index].ID == hit.taskID {
			model.board.Tasks[index].ID = "replacement-card-at-the-same-cell"
			break
		}
	}
	model.rebuildRenderPlan(renderImpactAll)
	model, _ = dispatchFlushedPointer(t, model, handler,
		tea.MouseReleaseMsg{X: hit.x0, Y: hit.y0, Button: tea.MouseLeft})
	if model.detail.IsOpen() {
		t.Fatalf("removed task activated detail for %q", model.detail.TaskID())
	}
	if model.move.lifted != nil || model.pointerAdmission.captureKey != "" {
		t.Fatal("removed task retained pointer capture")
	}
}

func TestMotionMailboxMismatchClearsHoverAndPublishesCorrection(t *testing.T) {
	model := performanceModel(120, "", performanceWidth, performanceHeight)
	hit := pointerBoardHit(t, model, func(hit boardHit) bool {
		return hit.kind == boardHitDefault && hit.taskID != ""
	})
	model, _ = dispatchRenderedPointer(t, model, tea.MouseMotionMsg{X: hit.x0, Y: hit.y0})
	if model.pointerState.Hovered() == "" {
		t.Fatal("fixture did not install a visible hover")
	}
	handler := model.View().OnMouse
	deposited := tea.MouseMotionMsg{X: hit.x0, Y: hit.y0}
	if command := handler(deposited); command != nil {
		t.Fatal("resolver escaped mailbox")
	}
	before := model.RenderPlanStats().InstalledSnapshotID
	model, _ = model.updateWithCommands(tea.MouseMotionMsg{X: hit.x0 + 1, Y: hit.y0})
	if model.pointerState.Hovered() != "" {
		t.Fatalf("mismatched motion retained hover %q", model.pointerState.Hovered())
	}
	if _, ok := model.pointerState.HoverPoint(); ok {
		t.Fatal("mismatched motion retained the old hover observation")
	}
	if got := model.RenderPlanStats().InstalledSnapshotID; got != before+1 {
		t.Fatalf("hover correction published %d frames, want 1", got-before)
	}
}

func trackedControlCell(t *testing.T, model Model) (int, int, pointer.ControlID) {
	t.Helper()
	if model.current == nil || model.current.resolver == nil {
		t.Fatal("published owner has no pointer resolver")
	}
	for y := 0; y < model.height; y++ {
		for x := 0; x < model.width; x++ {
			command := model.current.resolver(tea.MouseMotionMsg{X: x, Y: y})
			if command == nil {
				continue
			}
			interaction, ok := pointer.ObserveInteraction(command())
			if ok && interaction.Kind == pointer.InteractionHover && interaction.ID != "" &&
				model.current.semantics.topology.HasControl(interaction.ID) {
				return x, y, interaction.ID
			}
		}
	}
	t.Fatalf("owner %d published no tracked control", model.current.semantics.handler)
	return 0, 0, ""
}

func pointerOwnerModel(t *testing.T, open func(*Model, *storeFixture)) Model {
	t.Helper()
	fixture := &storeFixture{store: newSettingsTestStore(t)}
	model := newTestRootModel(fixture.store, nil, "alice")
	model.width, model.height = 100, 30
	completeBoardLoad(t, &model, model.Init())
	open(&model, fixture)
	model.rebuildRenderPlan(renderImpactAll)
	return model
}

type storeFixture struct{ store *store.Store }

func TestEveryPublishedOwnerRebindsStableReleaseAfterHarmlessRerender(t *testing.T) {
	owners := []struct {
		name string
		open func(*Model, *storeFixture)
	}{
		{name: "help", open: func(m *Model, _ *storeFixture) { m.helpOpen = true }},
		{name: "detail", open: func(m *Model, _ *storeFixture) {
			_ = m.detail.Open(board.Task{ID: "detail", Title: "Detail", Status: board.StatusTodo})
		}},
		{name: "settings", open: func(m *Model, fixture *storeFixture) {
			m.settings = newSettingsModel(fixture.store, "alice", context.Background())
			loadSettingsForTest(t, m.settings)
		}},
		{name: "ADR", open: func(m *Model, fixture *storeFixture) {
			m.configureAI(ai.NewRunner(fixture.store, "", nil, nil), context.Background())
			_ = m.adr.Open()
		}},
		{name: "editor", open: func(m *Model, _ *storeFixture) {
			_ = m.editor.OpenAdd(board.StatusTodo)
		}},
		{name: "task action", open: func(m *Model, _ *storeFixture) {
			m.openShipPrompt(board.Task{ID: "ship", Title: "Ship", Status: board.StatusTodo}, 0)
		}},
		{name: "issue import", open: func(m *Model, _ *storeFixture) {
			m.issueImport = issueimport.New(&rootImportStore{}, rootImportBackend{}, "alice", context.Background())
			_ = m.issueImport.Open()
		}},
		{name: "palette", open: func(m *Model, _ *storeFixture) { _ = m.openPalette() }},
	}
	for _, owner := range owners {
		t.Run(owner.name, func(t *testing.T) {
			model := pointerOwnerModel(t, owner.open)
			x, y, id := trackedControlCell(t, model)
			handler := model.View().OnMouse
			model, _ = dispatchFlushedPointer(t, model, handler,
				tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
			if model.pointerAdmission.captureKey != string(id) {
				t.Fatalf("capture=%q, want %q", model.pointerAdmission.captureKey, id)
			}
			// This frame has not been flushed to the terminal. The visible handler
			// remains authoritative, while its stable action must be rebound from
			// the current immutable topology.
			model.rebuildRenderPlan(renderImpactAppearance)
			before := model.pointerAdmission.accepted
			model, _ = dispatchFlushedPointer(t, model, handler,
				tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
			if model.pointerAdmission.accepted != before+1 {
				t.Fatalf("stable stale release accepted=%d, want %d", model.pointerAdmission.accepted, before+1)
			}
			if model.pointerAdmission.captureKey != "" || model.pointerState.Active() {
				t.Fatal("stable stale release retained capture")
			}
		})
	}
}

func TestEveryPublishedOwnerFailsClosedWhenStableControlIsRemoved(t *testing.T) {
	owners := []struct {
		name string
		open func(*Model, *storeFixture)
	}{
		{name: "help", open: func(m *Model, _ *storeFixture) { m.helpOpen = true }},
		{name: "detail", open: func(m *Model, _ *storeFixture) {
			_ = m.detail.Open(board.Task{ID: "detail", Title: "Detail", Status: board.StatusTodo})
		}},
		{name: "settings", open: func(m *Model, fixture *storeFixture) {
			m.settings = newSettingsModel(fixture.store, "alice", context.Background())
			loadSettingsForTest(t, m.settings)
		}},
		{name: "ADR", open: func(m *Model, fixture *storeFixture) {
			m.configureAI(ai.NewRunner(fixture.store, "", nil, nil), context.Background())
			_ = m.adr.Open()
		}},
		{name: "editor", open: func(m *Model, _ *storeFixture) { _ = m.editor.OpenAdd(board.StatusTodo) }},
		{name: "task action", open: func(m *Model, _ *storeFixture) {
			m.openShipPrompt(board.Task{ID: "ship", Title: "Ship", Status: board.StatusTodo}, 0)
		}},
		{name: "issue import", open: func(m *Model, _ *storeFixture) {
			m.issueImport = issueimport.New(&rootImportStore{}, rootImportBackend{}, "alice", context.Background())
			_ = m.issueImport.Open()
		}},
		{name: "palette", open: func(m *Model, _ *storeFixture) { _ = m.openPalette() }},
	}
	for _, owner := range owners {
		t.Run(owner.name, func(t *testing.T) {
			model := pointerOwnerModel(t, owner.open)
			x, y, _ := trackedControlCell(t, model)
			handler := model.View().OnMouse
			model, _ = dispatchFlushedPointer(t, model, handler,
				tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
			next := *model.current
			next.stats.InstalledSnapshotID++
			next.semantics.topology = pointer.Topology{}
			model.current = &next
			before := model.pointerAdmission.accepted
			model, _ = dispatchFlushedPointer(t, model, handler,
				tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
			if model.pointerAdmission.accepted != before {
				t.Fatalf("removed control release was accepted: %d -> %d", before, model.pointerAdmission.accepted)
			}
			if model.pointerAdmission.captureKey != "" || model.pointerState.Active() {
				t.Fatal("removed control retained capture")
			}
		})
	}
}
