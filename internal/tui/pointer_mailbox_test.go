package tui

import (
	"context"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

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

func TestRenderedOverlayWheelsPublishAbsoluteOwnerTargets(t *testing.T) {
	const width, height = 84, 10
	tests := []struct {
		name        string
		owner       renderHandlerTopology
		wheelKey    string
		open        func(*Model, *storeFixture)
		ownerView   func(*Model) string
		assertAfter func(*testing.T, *Model, string)
	}{
		{
			name: "palette", owner: renderHandlerPalette, wheelKey: "palette",
			open:      func(m *Model, _ *storeFixture) { _ = m.openPalette() },
			ownerView: func(m *Model) string { return m.palette.View(m.width, m.height) },
			assertAfter: func(t *testing.T, m *Model, before string) {
				after := ansi.Strip(m.palette.View(m.width, m.height))
				if !strings.Contains(ansi.Strip(before), "1/") || !strings.Contains(after, "2/") {
					t.Fatalf("palette wheel hint did not move exactly 1/ to 2/:\n%s", after)
				}
			},
		},
		{
			name: "settings", owner: renderHandlerSettings, wheelKey: "settings",
			open: func(m *Model, fixture *storeFixture) {
				m.settings = newSettingsModel(fixture.store, "alice", context.Background())
				loadSettingsForTest(t, m.settings)
			},
			ownerView: func(m *Model) string { return m.settings.View(m.width, m.height) },
			assertAfter: func(t *testing.T, m *Model, _ string) {
				if m.settings.focus != "ai:model" {
					t.Fatalf("settings wheel focus = %q, want ai:model", m.settings.focus)
				}
				if view := ansi.Strip(m.settings.View(m.width, m.height)); !strings.Contains(view, settingsFocusBar()+"Model:") {
					t.Fatalf("settings wheel target is not visibly focused:\n%s", view)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := pointerOwnerModel(t, test.open)
			model.width, model.height = width, height
			model.rebuildRenderPlan(renderImpactAll)
			if model.pointerOwner != test.owner {
				t.Fatalf("published owner = %v, want %v", model.pointerOwner, test.owner)
			}
			ownerSession, ownerEpoch := model.pointerOwnerSeq, model.pointerOwnerEpoch
			beforeOwner, beforeRoot := test.ownerView(&model), model.View().Content
			model, _ = dispatchRenderedPointer(t, model, tea.MouseWheelMsg{
				X: width / 2, Y: height / 2, Button: tea.MouseWheelDown,
			})
			last := model.pointerAdmission.last
			if !model.pointerAdmission.haveLast || last.kind != pointerTargetWheel ||
				last.wheelKey != test.wheelKey || last.position != 1 {
				t.Fatalf("published wheel target = %+v, want %s at absolute 1", last, test.wheelKey)
			}
			if model.pointerOwnerSeq != ownerSession || model.pointerOwnerEpoch != ownerEpoch {
				t.Fatalf("owner session changed (%d,%d) -> (%d,%d)", ownerSession, ownerEpoch,
					model.pointerOwnerSeq, model.pointerOwnerEpoch)
			}
			if after := test.ownerView(&model); after == beforeOwner || model.View().Content == beforeRoot {
				t.Fatal("wheel target did not change both owner and published root render")
			}
			test.assertAfter(t, &model, beforeOwner)
		})
	}
}

func TestNaturallyClosedEditorPublishesNoPointerSurface(t *testing.T) {
	const width, height = 84, 10
	model := pointerOwnerModel(t, func(m *Model, _ *storeFixture) {
		_ = m.editor.OpenAdd(board.StatusTodo)
	})
	model.width, model.height = width, height
	model.rebuildRenderPlan(renderImpactAll)
	if surface := model.editor.PointerSurface(width, height); surface.Pointer == nil {
		t.Fatal("open editor published no pointer surface")
	}
	model, _ = model.updateWithCommands(tea.KeyPressMsg{Code: tea.KeyEscape})
	surface := model.editor.PointerSurface(width, height)
	if model.editor.IsOpen() || surface.Content != "" || surface.Pointer != nil ||
		!surface.Topology.SameControls(pointer.Topology{}) {
		t.Fatal("naturally closed editor retained a pointer surface")
	}
}

func TestRenderedBoardWheelCancelsLiftAndAdvancesColumn(t *testing.T) {
	model := performanceModel(120, "", performanceWidth, performanceHeight)
	source := pointerBoardHit(t, model, func(hit boardHit) bool { return hit.taskID != "" })
	column := pointerBoardHit(t, model, func(hit boardHit) bool {
		return hit.kind == boardHitDefault && hit.taskID == "" && hit.scroll == 0 && hit.maxScroll >= 3
	})
	handler := model.View().OnMouse
	press := tea.MouseClickMsg{X: source.x0, Y: source.y0, Button: tea.MouseLeft}
	if command := handler(press); command != nil {
		t.Fatal("stale handler press escaped the synchronous mailbox")
	}
	model, _ = model.updateWithCommands(press)
	if model.move.lifted == nil || model.pointerAdmission.captureKey == "" {
		t.Fatal("board press did not acquire lift and capture")
	}
	beforeAccepted := model.pointerAdmission.accepted
	wheel := tea.MouseWheelMsg{X: column.x0, Y: column.y0, Button: tea.MouseWheelDown}
	if command := handler(wheel); command != nil {
		t.Fatal("stale handler wheel escaped the synchronous mailbox")
	}
	var commands modelUpdateCommands
	model, commands = model.updateWithCommands(wheel)
	if model.move.lifted != nil || model.pointerAdmission.captureKey != "" || model.pointerState.Active() {
		t.Fatal("board wheel retained lift or capture")
	}
	if model.pointerAdmission.accepted != beforeAccepted+1 {
		t.Fatalf("wheel accepted %d events, want exactly 1", model.pointerAdmission.accepted-beforeAccepted)
	}
	if got := model.boardView.scrolls[statusIndex(column.status)]; got != 3 {
		t.Fatalf("column scroll = %d, want absolute 3", got)
	}
	if !model.move.notice || model.noticeSeq == 0 || commands.followUp == nil {
		t.Fatalf("wheel cancellation notice=%t sequence=%d command=%v", model.move.notice, model.noticeSeq, commands.followUp)
	}
	assertPerformanceColdOracleParity(t, model)
}

func TestRenderedBoardCurrentHandlerWheelCancelsLiftOnce(t *testing.T) {
	model := performanceModel(120, "", performanceWidth, performanceHeight)
	source := pointerBoardHit(t, model, func(hit boardHit) bool { return hit.taskID != "" })
	column := pointerBoardHit(t, model, func(hit boardHit) bool {
		return hit.kind == boardHitDefault && hit.taskID == "" && hit.scroll == 0 && hit.maxScroll >= 3
	})
	model, _ = dispatchRenderedPointer(t, model,
		tea.MouseClickMsg{X: source.x0, Y: source.y0, Button: tea.MouseLeft})
	if model.move.lifted == nil || model.pointerAdmission.captureKey == "" {
		t.Fatal("board press did not acquire lift and capture")
	}

	beforeAccepted := model.pointerAdmission.accepted
	model, _ = dispatchRenderedPointer(t, model,
		tea.MouseWheelMsg{X: column.x0, Y: column.y0, Button: tea.MouseWheelDown})
	if model.move.lifted != nil || model.pointerAdmission.captureKey != "" || model.pointerState.Active() ||
		model.pointerAdmission.accepted != beforeAccepted+1 ||
		model.boardView.scrolls[statusIndex(column.status)] != 3 {
		t.Fatalf("current-handler wheel state: lifted=%v capture=%q active=%t accepted=%d scroll=%d",
			model.move.lifted, model.pointerAdmission.captureKey, model.pointerState.Active(),
			model.pointerAdmission.accepted-beforeAccepted,
			model.boardView.scrolls[statusIndex(column.status)])
	}
}

func TestRenderedBoardStaleWheelTranslationFailureCancelsDrag(t *testing.T) {
	model := performanceModel(120, "", performanceWidth, performanceHeight)
	canonical := cloneBoard(model.board)
	source := pointerBoardHit(t, model, func(hit boardHit) bool { return hit.taskID != "" })
	target := pointerBoardHit(t, model, func(hit boardHit) bool {
		return hit.taskID != "" && hit.status != source.status
	})
	column := pointerBoardHit(t, model, func(hit boardHit) bool {
		return hit.kind == boardHitDefault && hit.taskID == "" && hit.status != target.status && hit.maxScroll >= 3
	})
	handler := model.View().OnMouse
	for _, raw := range []tea.MouseMsg{
		tea.MouseClickMsg{X: source.x0, Y: source.y0, Button: tea.MouseLeft},
		tea.MouseMotionMsg{X: target.x0, Y: target.y0, Button: tea.MouseLeft},
	} {
		if command := handler(raw); command != nil {
			t.Fatal("stale drag handler escaped the synchronous mailbox")
		}
		model, _ = model.updateWithCommands(raw)
	}
	if model.move.lifted == nil || !model.move.lifted.dragged || reflect.DeepEqual(model.board, canonical) {
		t.Fatal("fixture did not publish a dragged preview")
	}

	model, _ = model.updateWithCommands(tea.WindowSizeMsg{Width: 80, Height: performanceHeight})
	if model.move.lifted == nil || model.move.lifted.fromMouse == false {
		t.Fatal("resize unexpectedly cancelled the mouse lift")
	}
	beforeAccepted := model.pointerAdmission.accepted
	beforeDiscarded := model.pointerAdmission.discarded
	wheel := tea.MouseWheelMsg{X: column.x0, Y: column.y0, Button: tea.MouseWheelDown}
	if command := handler(wheel); command != nil {
		t.Fatal("stale wheel handler escaped the synchronous mailbox")
	}
	model, _ = model.updateWithCommands(wheel)
	if model.move.lifted != nil || model.pointerAdmission.captureKey != "" || model.pointerState.Active() ||
		model.pointerAdmission.accepted != beforeAccepted+1 ||
		model.pointerAdmission.discarded != beforeDiscarded+1 ||
		!reflect.DeepEqual(model.board, canonical) || !model.current.projection.matchesSource(canonical) ||
		!model.move.notice {
		t.Fatalf("failed translation state: lifted=%v capture=%q active=%t accepted=%d discarded=%d canonical=%t source=%t notice=%t",
			model.move.lifted, model.pointerAdmission.captureKey, model.pointerState.Active(),
			model.pointerAdmission.accepted-beforeAccepted,
			model.pointerAdmission.discarded-beforeDiscarded,
			reflect.DeepEqual(model.board, canonical), model.current.projection.matchesSource(canonical),
			model.move.notice)
	}
}

func TestRenderedBoardDraggedWheelRestoresCanonicalBeforeAbsoluteScroll(t *testing.T) {
	model := performanceModel(120, "", performanceWidth, performanceHeight)
	canonical := cloneBoard(model.board)
	source := pointerBoardHit(t, model, func(hit boardHit) bool { return hit.taskID != "" })
	target := pointerBoardHit(t, model, func(hit boardHit) bool {
		return hit.taskID != "" && hit.status != source.status
	})
	column := pointerBoardHit(t, model, func(hit boardHit) bool {
		return hit.kind == boardHitDefault && hit.taskID == "" && hit.scroll == 0 && hit.maxScroll >= 3
	})
	handler := model.View().OnMouse
	press := tea.MouseClickMsg{X: source.x0, Y: source.y0, Button: tea.MouseLeft}
	motion := tea.MouseMotionMsg{X: target.x0, Y: target.y0, Button: tea.MouseLeft}
	wheel := tea.MouseWheelMsg{X: column.x0, Y: column.y0, Button: tea.MouseWheelDown}
	var pendingGeometry tea.Cmd
	for _, raw := range []tea.MouseMsg{press, motion} {
		if command := handler(raw); command != nil {
			t.Fatal("stale drag handler escaped the synchronous mailbox")
		}
		var next modelUpdateCommands
		model, next = model.updateWithCommands(raw)
		if next.geometry != nil {
			pendingGeometry = next.geometry
		}
	}
	if model.move.lifted == nil || !model.move.lifted.dragged || reflect.DeepEqual(model.board, canonical) {
		t.Fatal("fixture did not publish a dragged preview")
	}
	beforeAccepted := model.pointerAdmission.accepted
	if command := handler(wheel); command != nil {
		t.Fatal("stale wheel handler escaped the synchronous mailbox")
	}
	model, commands := model.updateWithCommands(wheel)
	if commands.geometry != nil {
		pendingGeometry = commands.geometry
	}
	if pendingGeometry == nil {
		t.Fatal("drag sequence did not retain its geometry worker")
	}
	settlePerformanceGeometryCommand(t, &model, pendingGeometry)

	oracle := performanceModel(120, "", performanceWidth, performanceHeight)
	if !oracle.boardView.focusTask(oracle.filteredBoard(), source.taskID) {
		t.Fatal("canonical oracle could not focus the source card")
	}
	oracle, oracleCommands := oracle.updateWithCommands(boardColumnScrolledMsg{
		status: column.status, from: column.scroll, offset: 3, max: column.maxScroll,
	})
	settlePerformanceGeometryCommand(t, &oracle, oracleCommands.geometry)

	selected, selectedOK := model.selectedTask()
	if model.pointerAdmission.accepted != beforeAccepted+1 || model.move.lifted != nil ||
		!reflect.DeepEqual(model.board, canonical) || !selectedOK || selected.ID != source.taskID ||
		model.boardView.scrolls != oracle.boardView.scrolls ||
		model.boardView.scrollAnchors != oracle.boardView.scrollAnchors ||
		!model.current.projection.matchesSource(canonical) {
		t.Fatalf("dragged wheel state: accepted=%d lifted=%v canonical=%t selected=(%q,%t) scrolls=%v anchors=%+v source=%t",
			model.pointerAdmission.accepted-beforeAccepted, model.move.lifted,
			reflect.DeepEqual(model.board, canonical), selected.ID, selectedOK,
			model.boardView.scrolls, struct {
				Got, Want [len(boardStatuses)]boardTaskAnchor
			}{model.boardView.scrollAnchors, oracle.boardView.scrollAnchors},
			model.current.projection.matchesSource(canonical))
	}
	assertPerformanceColdOracleParity(t, model)
}

func TestRenderedBoardBoundaryWheelCancelsDragPreview(t *testing.T) {
	store := &moveTestStore{board: moveFixture()}
	model := loadedMoveModel(store)
	model.width, model.height = performanceWidth, performanceHeight
	model.rebuildRenderPlan(renderImpactAll)
	canonical := cloneBoard(model.board)
	source := pointerBoardHit(t, model, func(hit boardHit) bool { return hit.taskID == "a" })
	target := pointerBoardHit(t, model, func(hit boardHit) bool { return hit.taskID == "y" })

	model, _ = dispatchRenderedPointer(t, model,
		tea.MouseClickMsg{X: source.x0, Y: source.y0, Button: tea.MouseLeft})
	model, _ = dispatchRenderedPointer(t, model,
		tea.MouseMotionMsg{X: target.x0, Y: target.y0, Button: tea.MouseLeft})
	if model.move.lifted == nil || !model.move.lifted.dragged || reflect.DeepEqual(model.board, canonical) {
		t.Fatal("fixture did not publish a mouse drag preview")
	}
	boundary := pointerBoardHit(t, model, func(hit boardHit) bool {
		return hit.kind == boardHitDefault && hit.taskID == "" && hit.status == source.status && hit.scroll == 0
	})

	beforeAccepted := model.pointerAdmission.accepted
	beforeDiscarded := model.pointerAdmission.discarded
	beforeNoticeSeq := model.noticeSeq
	model, commands := dispatchRenderedPointer(t, model,
		tea.MouseWheelMsg{X: boundary.x0, Y: boundary.y0, Button: tea.MouseWheelUp})
	selected, selectedOK := model.selectedTask()
	geometrySource := pointerBoardHit(t, model, func(hit boardHit) bool { return hit.taskID == source.taskID })
	if model.move.lifted != nil || model.pointerAdmission.captureKey != "" || model.pointerState.Active() ||
		!reflect.DeepEqual(model.board, canonical) || !model.current.projection.matchesSource(canonical) ||
		!selectedOK || selected.ID != source.taskID || selected.Status != source.status ||
		geometrySource.status != source.status || model.pointerAdmission.accepted != beforeAccepted+1 ||
		model.pointerAdmission.discarded != beforeDiscarded+1 ||
		!model.move.notice || model.noticeSeq != beforeNoticeSeq+1 || commands.followUp == nil {
		t.Fatalf("boundary wheel state: lifted=%v capture=%q active=%t canonical=%t source=%t focus=(%q,%s,%t) geometry=%s, want restored %q in %s",
			model.move.lifted, model.pointerAdmission.captureKey, model.pointerState.Active(),
			reflect.DeepEqual(model.board, canonical), model.current.projection.matchesSource(canonical),
			selected.ID, selected.Status, selectedOK, geometrySource.status, source.taskID, source.status)
	}
	expiry := model.noticeSeq
	model, _ = model.updateWithCommands(noticeExpiredMsg{seq: expiry})
	if model.move.notice || model.noticeOwnsFooter() {
		t.Fatalf("boundary wheel cancellation notice did not expire: notice=%t saving=%t owns=%t seq=%d",
			model.move.notice, model.move.saving, model.noticeOwnsFooter(), model.noticeSeq)
	}
	assertPerformanceColdOracleParity(t, model)
}

func TestBoardWheelLeavesKeyboardLiftInProgress(t *testing.T) {
	model := performanceModel(120, "", performanceWidth, performanceHeight)
	source := pointerBoardHit(t, model, func(hit boardHit) bool { return hit.taskID != "" })
	if !model.focusBoardTask(source.taskID) {
		t.Fatal("fixture could not focus keyboard lift source")
	}
	task, ok := model.selectedTask()
	if !ok || !model.move.beginVisible(model.board, model.filteredBoard(), task, model.boardView.visibleStatuses(), false) {
		t.Fatal("fixture could not begin keyboard lift")
	}
	column := pointerBoardHit(t, model, func(hit boardHit) bool {
		return hit.kind == boardHitDefault && hit.taskID == "" && hit.maxScroll >= 3
	})
	model, _ = model.updateWithCommands(boardColumnScrolledMsg{
		status: column.status, from: column.scroll, offset: column.scroll + 3, max: column.maxScroll,
	})
	if model.move.lifted == nil || model.move.lifted.fromMouse ||
		model.boardView.scrolls[statusIndex(column.status)] != column.scroll+3 {
		t.Fatal("board wheel cancelled or failed to scroll during keyboard lift")
	}
}
