package tui

import (
	"reflect"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/tui/pointer"
)

type admissionWheelMsg struct {
	intent pointer.WheelIntent
	target int
}

func (m admissionWheelMsg) PointerWheelIntent() pointer.WheelIntent { return m.intent }

func (m admissionWheelMsg) PointerWheelTarget(target int) tea.Msg {
	m.target = target
	return m
}

func pointerTestModel(t *testing.T, count int) (Model, *stepClock) {
	t.Helper()
	model := performanceModel(count, "", performanceWidth, performanceHeight)
	clock := &stepClock{at: time.Unix(0, 0)}
	model.now = clock.now
	model.rebuildRenderPlan(renderImpactAll)
	return model, clock
}

func TestPointerDragReleaseOutsideCurrentHitsRestoresCanonicalBoard(t *testing.T) {
	store := &moveTestStore{board: moveFixture()}
	model := loadedMoveModel(store)
	model.width, model.height = performanceWidth, performanceHeight
	model.rebuildRenderPlan(renderImpactAll)
	canonical := cloneBoard(model.board)
	source := pointerBoardHit(t, model, func(hit boardHit) bool {
		return hit.kind == boardHitDefault && hit.taskID == "a"
	})
	model, _ = dispatchRenderedPointer(t, model,
		tea.MouseClickMsg{X: source.x0, Y: source.y0, Button: tea.MouseLeft})
	target := pointerBoardHit(t, model, func(hit boardHit) bool {
		return hit.kind == boardHitDefault && hit.taskID == "" && hit.status == board.StatusDoing
	})
	model, _ = dispatchRenderedPointer(t, model,
		tea.MouseMotionMsg{X: target.x0, Y: target.y0, Button: tea.MouseLeft})
	if model.move.lifted == nil || !model.move.lifted.dragged {
		t.Fatal("fixture did not produce a drag preview")
	}

	model, _ = dispatchRenderedPointer(t, model,
		tea.MouseReleaseMsg{X: 0, Y: 0, Button: tea.MouseNone})
	if model.move.lifted != nil || model.move.saving {
		t.Fatal("invalid release retained capture or started persistence")
	}
	if !reflect.DeepEqual(model.board, canonical) {
		t.Fatal("invalid release retained the preview instead of restoring the canonical board")
	}
	if store.target != "" {
		t.Fatalf("invalid release persisted target %q", store.target)
	}
}

func TestPointerDragReleaseUsesCurrentHitAsFinalTarget(t *testing.T) {
	store := &moveTestStore{board: moveFixture()}
	model := loadedMoveModel(store)
	model.width, model.height = performanceWidth, performanceHeight
	model.rebuildRenderPlan(renderImpactAll)
	source := pointerBoardHit(t, model, func(hit boardHit) bool {
		return hit.kind == boardHitDefault && hit.taskID == "a"
	})
	model, _ = dispatchRenderedPointer(t, model,
		tea.MouseClickMsg{X: source.x0, Y: source.y0, Button: tea.MouseLeft})
	intermediate := pointerBoardHit(t, model, func(hit boardHit) bool {
		return hit.kind == boardHitDefault && hit.taskID == "c"
	})
	model, _ = dispatchRenderedPointer(t, model,
		tea.MouseMotionMsg{X: intermediate.x0, Y: intermediate.y0, Button: tea.MouseLeft})
	release := pointerBoardHit(t, model, func(hit boardHit) bool {
		return hit.kind == boardHitDefault && hit.taskID == "" && hit.status == board.StatusDoing
	})
	model, commands := dispatchRenderedPointer(t, model,
		tea.MouseReleaseMsg{X: release.x0, Y: release.y0, Button: tea.MouseNone})
	if !model.move.saving || commands.followUp == nil {
		t.Fatalf("release did not synchronously start exact drop: saving=%t command=%v", model.move.saving, commands.followUp)
	}
	settlePerformanceCommands(t, &model, []tea.Cmd{commands.followUp})
	if store.target != board.StatusDoing {
		t.Fatalf("stored target = %s, want Doing", store.target)
	}
}

func pointerBoardHit(t *testing.T, model Model, match func(boardHit) bool) boardHit {
	t.Helper()
	for _, hit := range model.current.semantics.hits {
		if hit.x0 < hit.x1 && hit.y0 < hit.y1 && match(hit) {
			return hit
		}
	}
	t.Fatal("matching board pointer hit not found")
	return boardHit{}
}

func dispatchFlushedPointer(
	t *testing.T,
	model Model,
	handler func(tea.MouseMsg) tea.Cmd,
	raw tea.MouseMsg,
) (Model, modelUpdateCommands) {
	t.Helper()
	if command := handler(raw); command != nil {
		t.Fatal("flushed resolver escaped the synchronous mailbox")
	}
	next, commands := model.updateWithCommands(raw)
	return next, commands
}

func TestOldFlushedHandlerAdmitsWheelBurstAcrossNewerPlans(t *testing.T) {
	model, clock := pointerTestModel(t, 120)
	hit := pointerBoardHit(t, model, func(hit boardHit) bool {
		return hit.kind == boardHitDefault && hit.taskID == "" && hit.maxScroll >= 9
	})
	handler := model.View().OnMouse
	raw := tea.MouseWheelMsg{X: hit.x0, Y: hit.y0, Button: tea.MouseWheelDown}
	model, _ = dispatchFlushedPointer(t, model, handler, raw)
	clock.advance(time.Millisecond)
	model, _ = dispatchFlushedPointer(t, model, handler, raw)
	model, _ = dispatchFlushedPointer(t, model, handler, raw)
	if !model.pointerAdmission.havePending || model.pointerAdmission.pending.target.position != 9 {
		t.Fatalf("old-handler pending wheel = %+v, want absolute 9", model.pointerAdmission.pending.target)
	}
	clock.advance(20 * time.Millisecond)
	model, _ = model.updateWithCommands(pointerFlushMsg{sequence: model.pointerAdmission.sequence})
	if got := model.boardView.scrolls[statusIndex(hit.status)]; got != 9 {
		t.Fatalf("old-handler burst flushed to %d, want 9", got)
	}
}

func TestOldFlushedHandlerPendingHoverTranslatesOnlyStableTarget(t *testing.T) {
	for _, removePending := range []bool{false, true} {
		t.Run(map[bool]string{false: "stable", true: "removed"}[removePending], func(t *testing.T) {
			model, clock := pointerTestModel(t, 120)
			handler := model.View().OnMouse
			var hits []boardHit
			seen := map[string]bool{}
			for _, hit := range model.current.semantics.hits {
				if hit.taskID != "" && !seen[hit.taskID] {
					seen[hit.taskID] = true
					hits = append(hits, hit)
					if len(hits) == 2 {
						break
					}
				}
			}
			model, _ = dispatchFlushedPointer(t, model, handler,
				tea.MouseMotionMsg{X: hits[0].x0, Y: hits[0].y0})
			clock.advance(time.Millisecond)
			model, _ = dispatchFlushedPointer(t, model, handler,
				tea.MouseMotionMsg{X: hits[1].x0, Y: hits[1].y0})
			pendingID := boardCardHoverID(hits[1])
			if removePending {
				filtered := model.board.Tasks[:0]
				for _, task := range model.board.Tasks {
					if task.ID != hits[1].taskID {
						filtered = append(filtered, task)
					}
				}
				model.board.Tasks = filtered
			}
			model.rebuildRenderPlan(renderImpactAll)
			clock.advance(20 * time.Millisecond)
			model, _ = model.updateWithCommands(pointerFlushMsg{sequence: model.pointerAdmission.sequence})
			if removePending && model.pointerState.Hovered() == pendingID {
				t.Fatal("removed pending hover target was installed")
			}
			if !removePending && model.pointerState.Hovered() != pendingID {
				t.Fatalf("stable pending hover = %q, want %q", model.pointerState.Hovered(), pendingID)
			}
		})
	}
}

func TestPrePressBoardHandlerCarriesAuthoritativeReleaseTarget(t *testing.T) {
	store := &moveTestStore{board: moveFixture()}
	model := loadedMoveModel(store)
	model.width, model.height = performanceWidth, performanceHeight
	model.rebuildRenderPlan(renderImpactAll)
	handler := model.View().OnMouse
	source := pointerBoardHit(t, model, func(hit boardHit) bool {
		return hit.kind == boardHitDefault && hit.taskID == "a"
	})
	target := pointerBoardHit(t, model, func(hit boardHit) bool {
		return hit.kind == boardHitDefault && hit.taskID == "" && hit.status == board.StatusDoing
	})
	model, _ = dispatchFlushedPointer(t, model, handler,
		tea.MouseClickMsg{X: source.x0, Y: source.y0, Button: tea.MouseLeft})
	model, _ = dispatchFlushedPointer(t, model, handler,
		tea.MouseMotionMsg{X: source.x0, Y: source.y0, Button: tea.MouseLeft})
	model, commands := dispatchFlushedPointer(t, model, handler,
		tea.MouseReleaseMsg{X: target.x0, Y: target.y0, Button: tea.MouseNone})
	if !model.move.saving || commands.followUp == nil {
		t.Fatal("pre-press handler release did not start exact drop")
	}
	settlePerformanceCommands(t, &model, []tea.Cmd{commands.followUp})
	if store.target != board.StatusDoing {
		t.Fatalf("pre-press handler stored %s, want Doing", store.target)
	}
}

func TestPointerWheelCancelsTrackedPressBeforeRelease(t *testing.T) {
	model := openFilteredPalette(t)
	x, y := paletteRowCell(t, &model)
	handler := model.View().OnMouse
	model, _ = dispatchFlushedPointer(t, model, handler,
		tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	if model.pointerAdmission.captureKey == "" {
		t.Fatal("palette row did not acquire capture")
	}
	model, _ = dispatchFlushedPointer(t, model, handler,
		tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelDown})
	if model.pointerAdmission.captureKey != "" {
		t.Fatal("wheel did not synchronously cancel capture")
	}
	model, _ = dispatchFlushedPointer(t, model, handler,
		tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	if !model.palette.IsOpen() {
		t.Fatal("release after wheel activated the previously pressed row")
	}
}

func TestBoundaryWheelClearsTrackedControlPressOnce(t *testing.T) {
	model := performanceModel(120, "", performanceWidth, performanceHeight)
	control := pointerControlHit(t, model, board.StatusDoing)
	boundary := pointerBoardHit(t, model, func(hit boardHit) bool {
		return hit.kind == boardHitDefault && hit.taskID == "" && hit.status == board.StatusDoing && hit.scroll == 0
	})
	initialColumn := model.boardView.column
	baseline := model.View().Content
	handler := model.View().OnMouse
	press := tea.MouseClickMsg{X: control.x0, Y: control.y0, Button: tea.MouseLeft}
	if command := handler(press); command != nil {
		t.Fatal("board control press escaped the synchronous mailbox")
	}
	model, _ = model.updateWithCommands(press)
	if model.View().Content == baseline || !model.pointerState.Active() || model.pointerAdmission.captureKey == "" {
		t.Fatalf("board control press did not publish tracked pressed feedback: changed=%t active=%t capture=%q", model.View().Content != baseline, model.pointerState.Active(), model.pointerAdmission.captureKey)
	}

	before := model.RenderPlanStats()
	wheel := tea.MouseWheelMsg{X: boundary.x0, Y: boundary.y0, Button: tea.MouseWheelUp}
	if command := handler(wheel); command != nil {
		t.Fatal("stale boundary wheel escaped the synchronous mailbox")
	}
	model, _ = model.updateWithCommands(wheel)
	after := model.RenderPlanStats()
	if model.pointerState.Active() || model.pointerAdmission.captureKey != "" {
		t.Fatal("boundary wheel retained the tracked control press")
	}
	if model.View().Content != baseline {
		t.Fatal("boundary wheel did not publish the cleared visual")
	}
	if after.PublishedFrames != before.PublishedFrames+1 {
		t.Fatalf("boundary rejection published %d frames, want exactly 1", after.PublishedFrames-before.PublishedFrames)
	}
	release := tea.MouseReleaseMsg{X: control.x0, Y: control.y0, Button: tea.MouseLeft}
	if command := model.View().OnMouse(release); command != nil {
		t.Fatal("release escaped the synchronous mailbox")
	}
	model, _ = model.updateWithCommands(release)
	if model.boardView.column != initialColumn {
		t.Fatal("release after boundary rejection activated the old board control")
	}
	if got := model.RenderPlanStats().PublishedFrames; got != after.PublishedFrames {
		t.Fatalf("release after rejection published %d additional frames", got-after.PublishedFrames)
	}
}

func TestMailboxMismatchRestoresBoardAndPublishesFailClosedFrame(t *testing.T) {
	store := &moveTestStore{board: moveFixture()}
	model := loadedMoveModel(store)
	model.width, model.height = performanceWidth, performanceHeight
	model.rebuildRenderPlan(renderImpactAll)
	canonical := cloneBoard(model.board)
	handler := model.View().OnMouse
	source := pointerBoardHit(t, model, func(hit boardHit) bool { return hit.taskID == "a" })
	target := pointerBoardHit(t, model, func(hit boardHit) bool { return hit.taskID == "y" })
	model, _ = dispatchFlushedPointer(t, model, handler,
		tea.MouseClickMsg{X: source.x0, Y: source.y0, Button: tea.MouseLeft})
	model, _ = dispatchFlushedPointer(t, model, handler,
		tea.MouseMotionMsg{X: target.x0, Y: target.y0, Button: tea.MouseLeft})
	if !model.move.lifted.dragged {
		t.Fatal("fixture did not create preview")
	}
	before := model.RenderPlanStats().InstalledSnapshotID
	deposited := tea.MouseMotionMsg{X: target.x0, Y: target.y0, Button: tea.MouseLeft}
	if command := handler(deposited); command != nil {
		t.Fatal("resolver escaped mailbox")
	}
	mismatch := tea.MouseMotionMsg{X: target.x0 + 1, Y: target.y0, Button: tea.MouseLeft}
	model, _ = model.updateWithCommands(mismatch)
	if model.move.lifted != nil || model.pointerAdmission.captureKey != "" || !reflect.DeepEqual(model.board, canonical) {
		t.Fatal("mismatch retained preview or capture")
	}
	if model.RenderPlanStats().InstalledSnapshotID <= before || model.View().OnMouse == nil {
		t.Fatal("fail-closed reset did not publish a fresh usable frame")
	}
}

func TestOldFlushedBoundaryWheelCarriesSignedReversal(t *testing.T) {
	model, clock := pointerTestModel(t, 120)
	hit := pointerBoardHit(t, model, func(hit boardHit) bool {
		return hit.kind == boardHitDefault && hit.taskID == "" && hit.maxScroll >= 9
	})
	column := statusIndex(hit.status)
	model.boardView.scrolls[column] = hit.maxScroll
	model.boardView.scrollAnchors[column] = model.boardScrollAnchor(hit.status, hit.maxScroll)
	model.boardView.manualScroll[column] = true
	model.rebuildRenderPlan(renderImpactAll)
	hit = pointerBoardHit(t, model, func(candidate boardHit) bool {
		return candidate.kind == boardHitDefault && candidate.taskID == "" && candidate.status == hit.status
	})
	handler := model.View().OnMouse
	up := tea.MouseWheelMsg{X: hit.x0, Y: hit.y0, Button: tea.MouseWheelUp}
	down := tea.MouseWheelMsg{X: hit.x0, Y: hit.y0, Button: tea.MouseWheelDown}
	model, _ = dispatchFlushedPointer(t, model, handler, up)
	clock.advance(time.Millisecond)
	model, _ = dispatchFlushedPointer(t, model, handler, up)
	model, _ = dispatchFlushedPointer(t, model, handler, down)
	clock.advance(20 * time.Millisecond)
	model, _ = model.updateWithCommands(pointerFlushMsg{sequence: model.pointerAdmission.sequence})
	if got := model.boardView.scrolls[column]; got != hit.maxScroll-3 {
		t.Fatalf("boundary reversal flushed %d, want last published %d", got, hit.maxScroll-3)
	}
}

func TestOldFlushedBoundaryWheelRejectsMismatchedCell(t *testing.T) {
	model, clock := pointerTestModel(t, 120)
	hit := pointerBoardHit(t, model, func(hit boardHit) bool {
		return hit.kind == boardHitDefault && hit.taskID == "" && hit.maxScroll >= 9
	})
	column := statusIndex(hit.status)
	model.boardView.scrolls[column] = hit.maxScroll
	model.boardView.scrollAnchors[column] = model.boardScrollAnchor(hit.status, hit.maxScroll)
	model.boardView.manualScroll[column] = true
	model.rebuildRenderPlan(renderImpactAll)
	hit = pointerBoardHit(t, model, func(candidate boardHit) bool {
		return candidate.kind == boardHitDefault && candidate.taskID == "" && candidate.status == hit.status
	})
	handler := model.View().OnMouse
	up := tea.MouseWheelMsg{X: hit.x0, Y: hit.y0, Button: tea.MouseWheelUp}
	model, _ = dispatchFlushedPointer(t, model, handler, up)
	clock.advance(time.Millisecond)
	model, _ = dispatchFlushedPointer(t, model, handler, up)
	if !model.pointerAdmission.havePending {
		t.Fatal("fixture did not create a pending wheel intent")
	}

	// The stale handler is still at the boundary, but this raw cell is not the
	// cell that produced the retained wheel intent. It must not inherit it.
	otherCell := tea.MouseWheelMsg{X: hit.x0 + 1, Y: hit.y0, Button: tea.MouseWheelDown}
	model, _ = dispatchFlushedPointer(t, model, handler, otherCell)
	if got := model.boardView.scrolls[column]; got != hit.maxScroll-3 {
		t.Fatalf("mismatched-cell wheel changed scroll to %d, want %d", got, hit.maxScroll-3)
	}
}

func TestOldFlushedBoundaryWheelRejectsMismatchedAxis(t *testing.T) {
	model, clock := pointerTestModel(t, 120)
	hit := pointerBoardHit(t, model, func(hit boardHit) bool {
		return hit.kind == boardHitDefault && hit.taskID == "" && hit.maxScroll >= 9
	})
	column := statusIndex(hit.status)
	model.boardView.scrolls[column] = hit.maxScroll
	model.boardView.scrollAnchors[column] = model.boardScrollAnchor(hit.status, hit.maxScroll)
	model.boardView.manualScroll[column] = true
	model.rebuildRenderPlan(renderImpactAll)
	hit = pointerBoardHit(t, model, func(candidate boardHit) bool {
		return candidate.kind == boardHitDefault && candidate.taskID == "" && candidate.status == hit.status
	})
	handler := model.View().OnMouse
	up := tea.MouseWheelMsg{X: hit.x0, Y: hit.y0, Button: tea.MouseWheelUp}
	model, _ = dispatchFlushedPointer(t, model, handler, up)
	clock.advance(time.Millisecond)
	model, _ = dispatchFlushedPointer(t, model, handler, up)
	if !model.pointerAdmission.havePending {
		t.Fatal("fixture did not create a pending wheel intent")
	}

	// Horizontal wheel input is a different axis and cannot reuse a vertical
	// intent, even when it lands on the same terminal cell.
	horizontal := tea.MouseWheelMsg{X: hit.x0, Y: hit.y0, Button: tea.MouseWheelRight}
	model, _ = dispatchFlushedPointer(t, model, handler, horizontal)
	if got := model.boardView.scrolls[column]; got != hit.maxScroll-3 {
		t.Fatalf("mismatched-axis wheel changed scroll to %d, want %d", got, hit.maxScroll-3)
	}
}

func TestOldFlushedBoundaryWheelRejectsDifferentHandlerGeneration(t *testing.T) {
	model, _ := pointerTestModel(t, 120)
	hit := pointerBoardHit(t, model, func(hit boardHit) bool {
		return hit.kind == boardHitDefault && hit.taskID == "" && hit.maxScroll >= 9
	})
	column := statusIndex(hit.status)
	model.boardView.scrolls[column] = hit.maxScroll
	model.boardView.scrollAnchors[column] = model.boardScrollAnchor(hit.status, hit.maxScroll)
	model.boardView.manualScroll[column] = true
	model.rebuildRenderPlan(renderImpactAll)
	hit = pointerBoardHit(t, model, func(candidate boardHit) bool {
		return candidate.kind == boardHitDefault && candidate.taskID == "" && candidate.status == hit.status
	})
	raw := tea.MouseWheelMsg{X: hit.x0, Y: hit.y0, Button: tea.MouseWheelDown, Mod: tea.ModCtrl}
	route := model.pointerRoute()
	staleRoute := route
	staleRoute.snapshot--
	model.pointerAdmission.pending = boundaryWheelCandidate(hit, staleRoute, raw)
	model.pointerAdmission.havePending = true
	model.pointerAdmission.sequence++
	model.pointerAdmission.timer = true
	sequence := model.pointerAdmission.sequence
	before := model.RenderPlanStats().InstalledSnapshotID
	model, commands := dispatchFlushedPointer(t, model, model.View().OnMouse, raw)
	if got := model.boardView.scrolls[column]; got != hit.maxScroll {
		t.Fatalf("different-generation fallback changed scroll to %d, want boundary %d", got, hit.maxScroll)
	}
	if commands.followUp != nil {
		t.Fatal("different-generation boundary fallback scheduled a synthesized flush")
	}
	if got := model.RenderPlanStats().InstalledSnapshotID; got != before {
		t.Fatalf("different-generation boundary fallback published %d frames", got-before)
	}
	model, _ = model.updateWithCommands(pointerFlushMsg{sequence: sequence})
	if got := model.boardView.scrolls[column]; got != hit.maxScroll || model.pointerAdmission.havePending {
		t.Fatalf("different-generation pending flush scroll=%d pending=%t", got, model.pointerAdmission.havePending)
	}
}

func TestRenderedStaleWideWheelCannotReuseAcrossNarrowSourceGeneration(t *testing.T) {
	const oldScroll = 6
	const narrowWidth = 120
	var model Model
	var oldStatus board.Status
	var wideHandler func(tea.MouseMsg) tea.Cmd
	var raw tea.MouseWheelMsg
	var column int
	var commands modelUpdateCommands
	found := false
	for _, status := range boardStatuses {
		candidateModel, _ := pointerTestModel(t, 120)
		var candidate boardHit
		var candidateOK bool
		for _, current := range candidateModel.current.semantics.hits {
			if current.kind == boardHitDefault && current.taskID == "" && current.status == status && current.maxScroll >= 9 {
				candidate, candidateOK = current, true
				break
			}
		}
		if !candidateOK {
			continue
		}
		column = statusIndex(status)
		candidateModel.boardView.scrolls[column] = oldScroll
		candidateModel.boardView.scrollAnchors[column] = candidateModel.boardScrollAnchor(status, oldScroll)
		candidateModel.boardView.manualScroll[column] = true
		candidateModel.rebuildRenderPlan(renderImpactAll)
		for _, current := range candidateModel.current.semantics.hits {
			if current.kind == boardHitDefault && current.taskID == "" && current.status == status &&
				current.scroll == oldScroll && current.maxScroll >= 9 {
				candidate = current
				break
			}
		}
		candidateWideHandler := candidateModel.View().OnMouse
		narrow := candidateModel
		narrow, commands = narrow.updateWithCommands(tea.WindowSizeMsg{Width: narrowWidth, Height: performanceHeight})
		settlePerformanceGeometryCommand(t, &narrow, commands.geometry)
		for y := candidate.y0; y < candidate.y1 && !found; y++ {
			for x := candidate.x0; x < candidate.x1 && x < narrowWidth; x++ {
				for _, current := range narrow.current.semantics.hits {
					if current.kind != boardHitDefault || current.taskID != "" || current.status == status ||
						x < current.x0 || x >= current.x1 || y < current.y0 || y >= current.y1 || current.scroll != 0 {
						continue
					}
					raw = tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelUp}
					oldStatus, model, wideHandler = status, candidateModel, candidateWideHandler
					found = true
					break
				}
			}
		}
		if found {
			break
		}
	}
	if !found {
		t.Fatal("wide-to-narrow fixture has no cross-status boundary coordinate")
	}

	model, commands = model.updateWithCommands(tea.WindowSizeMsg{Width: narrowWidth, Height: performanceHeight})
	settlePerformanceGeometryCommand(t, &model, commands.geometry)
	beforeAccepted := model.pointerAdmission.accepted
	model, commands = dispatchFlushedPointer(t, model, wideHandler, raw)
	if model.pointerAdmission.accepted != beforeAccepted+1 || model.boardView.scrolls[column] != oldScroll-3 {
		t.Fatalf("stale wide wheel for %s accepted=%d old-status scroll=%d, want accepted 1 and scroll %d",
			oldStatus,
			model.pointerAdmission.accepted-beforeAccepted, model.boardView.scrolls[column], oldScroll-3)
	}
	if !model.pointerAdmission.haveLast || model.pointerAdmission.lastIntent.sourceRoute == model.pointerAdmission.lastIntent.route {
		t.Fatalf("translated wheel did not retain distinct source/current routes: lastIntent=%+v", model.pointerAdmission.lastIntent)
	}
	settlePerformanceGeometryCommand(t, &model, commands.geometry)

	narrowHandler := model.View().OnMouse
	if command := narrowHandler(raw); command != nil {
		t.Fatal("narrow boundary handler unexpectedly resolved a wheel command")
	}
	beforePublished := model.RenderPlanStats().PublishedFrames
	beforeOldScroll := model.boardView.scrolls[column]
	model, _ = model.updateWithCommands(raw)
	if model.pointerAdmission.accepted != beforeAccepted+1 ||
		model.RenderPlanStats().PublishedFrames != beforePublished ||
		model.boardView.scrolls[column] != beforeOldScroll {
		t.Fatalf("current narrow boundary reused old source: accepted=%d published=%d old-status scroll=%d",
			model.pointerAdmission.accepted-beforeAccepted,
			model.RenderPlanStats().PublishedFrames-beforePublished,
			model.boardView.scrolls[column])
	}
}

func TestBoundaryWheelReuseRebindsExpandedCurrentBoundsBeforeAdvancing(t *testing.T) {
	model, _ := pointerTestModel(t, 120)
	hit := pointerBoardHit(t, model, func(hit boardHit) bool {
		return hit.kind == boardHitDefault && hit.taskID == "" && hit.maxScroll >= 6
	})
	column := statusIndex(hit.status)
	const oldMax = 3
	const currentMax = 6
	model.boardView.scrolls[column] = oldMax
	model.boardView.scrollAnchors[column] = model.boardScrollAnchor(hit.status, oldMax)
	model.boardView.manualScroll[column] = true
	model.rebuildRenderPlan(renderImpactAll)

	sourceRoute := model.pointerRoute()
	raw := tea.MouseWheelMsg{X: hit.x0, Y: hit.y0, Button: tea.MouseWheelDown}
	candidate := pointerIntent{
		message: boardColumnScrolledMsg{
			status: hit.status,
			from:   oldMax,
			offset: oldMax,
			max:    oldMax,
		},
		route:       sourceRoute,
		sourceRoute: sourceRoute,
		raw:         raw,
		target: pointerTarget{
			kind:      pointerTargetWheel,
			wheelKey:  "board:" + string(hit.status),
			position:  oldMax,
			wheelStep: 3,
		},
		wheelDelta: 3,
		advanced:   true,
	}
	model.pointerAdmission.pending = candidate
	model.pointerAdmission.havePending = true

	currentIntent := pointer.WheelIntent{
		Key:     candidate.target.wheelKey,
		Current: oldMax,
		Target:  oldMax,
		Min:     0,
		Max:     currentMax,
	}
	nextPlan := *model.current
	nextPlan.geometry.generation++
	nextPlan.semantics.topology = pointer.Topology{}.WithWheel(currentIntent, func(target int) tea.Msg {
		return boardColumnScrolledMsg{
			status: hit.status,
			from:   oldMax,
			offset: min(max(target, 0), currentMax),
			max:    currentMax,
		}
	})
	model.current = &nextPlan

	model, _, handled := model.reuseBoundaryWheel(raw, sourceRoute)
	if !handled {
		t.Fatal("expanded current bounds rejected a now-valid boundary wheel")
	}
	if got := model.boardView.scrolls[column]; got != currentMax {
		t.Fatalf("expanded-boundary wheel scroll=%d, want current max %d", got, currentMax)
	}
}

func TestOldFlushedBoundaryWheelRejectsDifferentModifiers(t *testing.T) {
	model, _ := pointerTestModel(t, 120)
	hit := pointerBoardHit(t, model, func(hit boardHit) bool {
		return hit.kind == boardHitDefault && hit.taskID == "" && hit.maxScroll >= 9
	})
	column := statusIndex(hit.status)
	model.boardView.scrolls[column] = hit.maxScroll
	model.boardView.scrollAnchors[column] = model.boardScrollAnchor(hit.status, hit.maxScroll)
	model.boardView.manualScroll[column] = true
	model.rebuildRenderPlan(renderImpactAll)
	hit = pointerBoardHit(t, model, func(candidate boardHit) bool {
		return candidate.kind == boardHitDefault && candidate.taskID == "" && candidate.status == hit.status
	})
	route := model.pointerRoute()
	retainedRaw := tea.MouseWheelMsg{X: hit.x0, Y: hit.y0, Button: tea.MouseWheelDown, Mod: tea.ModCtrl}
	model.pointerAdmission.pending = boundaryWheelCandidate(hit, route, retainedRaw)
	model.pointerAdmission.havePending = true
	model.pointerAdmission.sequence++
	model.pointerAdmission.timer = true
	sequence := model.pointerAdmission.sequence
	currentRaw := tea.MouseWheelMsg{X: hit.x0, Y: hit.y0, Button: tea.MouseWheelDown, Mod: tea.ModShift}
	model, commands := dispatchFlushedPointer(t, model, model.View().OnMouse, currentRaw)
	if got := model.boardView.scrolls[column]; got != hit.maxScroll {
		t.Fatalf("different-modifier fallback changed scroll to %d, want boundary %d", got, hit.maxScroll)
	}
	if commands.followUp != nil {
		t.Fatal("different-modifier boundary fallback scheduled a synthesized flush")
	}
	model, _ = model.updateWithCommands(pointerFlushMsg{sequence: sequence})
	if got := model.boardView.scrolls[column]; got != hit.maxScroll || model.pointerAdmission.havePending {
		t.Fatalf("different-modifier pending flush scroll=%d pending=%t", got, model.pointerAdmission.havePending)
	}
}

func boundaryWheelCandidate(hit boardHit, route pointerRouteIdentity, raw tea.MouseMsg) pointerIntent {
	position := hit.maxScroll - 6
	message := boardColumnScrolledMsg{
		status: hit.status,
		from:   position - 3,
		offset: position,
		max:    hit.maxScroll,
	}
	return pointerIntent{
		message: message,
		route:   route,
		raw:     raw,
		target: pointerTarget{
			kind:      pointerTargetWheel,
			wheelKey:  "board:" + string(hit.status),
			position:  position,
			wheelStep: 3,
		},
		wheelDelta: 3,
		advanced:   true,
	}
}

func TestPointerWheelBurstFlushesOneLatestAbsoluteTarget(t *testing.T) {
	model, clock := pointerTestModel(t, 120)
	hit := pointerBoardHit(t, model, func(hit boardHit) bool {
		return hit.kind == boardHitDefault && hit.taskID == "" && hit.maxScroll >= 12
	})
	raw := tea.MouseWheelMsg{X: hit.x0, Y: hit.y0, Button: tea.MouseWheelDown}

	model, _ = dispatchRenderedPointer(t, model, raw)
	column := statusIndex(hit.status)
	if got := model.boardView.scrolls[column]; got != 3 {
		t.Fatalf("first wheel target = %d, want 3", got)
	}
	clock.advance(time.Millisecond)
	for range 3 {
		model, _ = dispatchRenderedPointer(t, model, raw)
	}
	if !model.pointerAdmission.havePending || model.pointerAdmission.pending.target.position != 12 {
		t.Fatalf("pending target = %+v, want absolute 12", model.pointerAdmission.pending.target)
	}
	if got := model.boardView.scrolls[column]; got != 3 {
		t.Fatalf("pre-flush scroll = %d, want 3", got)
	}
	model, _ = model.admitResolvedPointer(boardColumnScrolledMsg{
		status: hit.status,
		from:   3,
		offset: 6,
		max:    12,
	}, model.pointerRoute(), raw)
	if !model.pointerAdmission.havePending || model.pointerAdmission.pending.target.position != 12 {
		t.Fatalf("boundary event retired valid pending target = %+v, want absolute 12",
			model.pointerAdmission.pending.target)
	}
	if got := model.boardView.scrolls[column]; got != 3 {
		t.Fatalf("boundary event published scroll = %d, want deferred 3", got)
	}

	clock.advance(20 * time.Millisecond)
	model, _ = model.updateWithCommands(pointerFlushMsg{sequence: model.pointerAdmission.sequence})
	if got := model.boardView.scrolls[column]; got != 12 {
		t.Fatalf("flushed scroll = %d, want 12", got)
	}
	if model.pointerAdmission.havePending || model.pointerAdmission.timer {
		t.Fatal("flush retained pending wheel state")
	}
}

func TestPointerWheelRetainsStepAndRebuildsWrappedFollowups(t *testing.T) {
	model, _ := pointerTestModel(t, 12)
	route := model.pointerRoute()
	key := "board:" + string(board.StatusTodo)
	raw := tea.MouseWheelMsg{X: 4, Y: 5, Button: tea.MouseWheelDown, Mod: tea.ModCtrl}
	model.pointerAdmission.pending = pointerIntent{
		route:  route,
		target: pointerTarget{kind: pointerTargetWheel, wheelKey: key, wheelStep: 3},
	}
	model.pointerAdmission.havePending = true
	intent, ok := model.pointerIntent(boardColumnScrolledMsg{status: board.StatusTodo, from: 5, offset: 5, max: 20}, route, raw)
	if !ok || intent.target.wheelStep != 3 || intent.wheelDelta != 3 {
		t.Fatalf("previous pending wheel step = (%+v, %t), want signed step 3", intent, ok)
	}
	if got := model.previousWheelStep(route, key); got != 3 {
		t.Fatalf("pending wheel step = %d, want 3", got)
	}

	model.pointerAdmission.havePending = false
	model.pointerAdmission.haveLast = true
	model.pointerAdmission.lastRoute = route
	model.pointerAdmission.last = pointerTarget{kind: pointerTargetWheel, wheelKey: key, wheelStep: 4}
	if got := model.previousWheelStep(route, key); got != 4 {
		t.Fatalf("published wheel step = %d, want 4", got)
	}
	if got := model.previousWheelStep(route, "missing"); got != 0 {
		t.Fatalf("unrelated wheel inherited step %d", got)
	}

	wrappedBoard := pointer.CancelWith(func() tea.Msg {
		return boardColumnScrolledMsg{status: board.StatusTodo, from: 2, offset: 5, max: 6}
	})()
	wheel, rebuild, ok := model.resolveWheelIntent(wrappedBoard)
	if !ok || wheel.Target != 5 {
		t.Fatalf("wrapped board wheel = (%+v, %t)", wheel, ok)
	}
	rebuilt := rebuild(99)
	observed, ok := pointer.ObserveInteraction(rebuilt)
	boardFollowup, boardOK := observed.Followup.(boardColumnScrolledMsg)
	if !ok || !boardOK || boardFollowup.offset != 6 {
		t.Fatalf("wrapped board rebuild = observed:%+v board:%+v", observed, boardFollowup)
	}

	custom := admissionWheelMsg{intent: pointer.WheelIntent{Key: "custom", Current: 2, Target: 3, Min: 0, Max: 7}}
	wrappedCustom := pointer.CancelWith(func() tea.Msg { return custom })()
	_, rebuild, ok = model.resolveWheelIntent(wrappedCustom)
	if !ok {
		t.Fatal("wrapped custom wheel was not resolved")
	}
	rebuilt = rebuild(6)
	observed, ok = pointer.ObserveInteraction(rebuilt)
	customFollowup, customOK := observed.Followup.(admissionWheelMsg)
	if !ok || !customOK || customFollowup.target != 6 {
		t.Fatalf("wrapped custom rebuild = observed:%+v custom:%+v", observed, customFollowup)
	}
	if _, _, ok := model.resolveWheelIntent(admissionWheelMsg{}); ok {
		t.Fatal("empty wheel key was admitted")
	}
}

func TestPointerFlushInsideCoalesceWindowReschedulesPendingIntent(t *testing.T) {
	model, clock := pointerTestModel(t, 12)
	route := model.pointerRoute()
	intent := pointerIntent{
		message: boardColumnScrolledMsg{status: board.StatusTodo, from: 0, offset: 3, max: 9},
		route:   route,
		target: pointerTarget{kind: pointerTargetWheel, wheelKey: "board:" + string(board.StatusTodo),
			position: 3, wheelStep: 3},
		advanced: true,
	}
	model.pointerAdmission.pending = intent
	model.pointerAdmission.havePending = true
	model.pointerAdmission.timer = true
	model.pointerAdmission.sequence = 4
	model.pointerAdmission.haveLast = true
	model.pointerAdmission.lastAt = clock.at
	beforeFlushes := model.pointerAdmission.flushes

	next, commands := model.flushPointerIntent(pointerFlushMsg{sequence: 4})
	if !next.pointerAdmission.timer || !next.pointerAdmission.havePending || next.pointerAdmission.sequence != 5 ||
		next.pointerAdmission.flushes != beforeFlushes || commands.followUp == nil {
		t.Fatalf("early flush = timer:%t pending:%t sequence:%d flushes:%d followup:%v",
			next.pointerAdmission.timer, next.pointerAdmission.havePending, next.pointerAdmission.sequence,
			next.pointerAdmission.flushes-beforeFlushes, commands.followUp)
	}
}

func TestPointerWheelReversalRetiresOppositePendingTravel(t *testing.T) {
	model, clock := pointerTestModel(t, 120)
	hit := pointerBoardHit(t, model, func(hit boardHit) bool {
		return hit.kind == boardHitDefault && hit.taskID == "" && hit.maxScroll >= 6
	})
	down := tea.MouseWheelMsg{X: hit.x0, Y: hit.y0, Button: tea.MouseWheelDown}
	up := tea.MouseWheelMsg{X: hit.x0, Y: hit.y0, Button: tea.MouseWheelUp}
	model, _ = dispatchRenderedPointer(t, model, down)
	clock.advance(time.Millisecond)
	model, _ = dispatchRenderedPointer(t, model, down)
	model, _ = dispatchRenderedPointer(t, model, up)

	clock.advance(20 * time.Millisecond)
	model, _ = model.updateWithCommands(pointerFlushMsg{sequence: model.pointerAdmission.sequence})
	if got := model.boardView.scrolls[statusIndex(hit.status)]; got != 3 {
		t.Fatalf("reversed scroll = %d, want last published target 3", got)
	}
	if model.pointerAdmission.havePending || model.pointerAdmission.timer {
		t.Fatal("reversal retained opposite-direction pending travel")
	}
}

func TestPointerHoverKeepsLatestTargetAndSameTargetIsFrameFree(t *testing.T) {
	model, clock := pointerTestModel(t, 120)
	var hits []boardHit
	seen := map[string]bool{}
	for _, hit := range model.current.semantics.hits {
		if hit.kind == boardHitDefault && hit.taskID != "" && !seen[hit.taskID] {
			seen[hit.taskID] = true
			hits = append(hits, hit)
			if len(hits) == 2 {
				break
			}
		}
	}
	if len(hits) != 2 {
		t.Fatal("need two visible card hover targets")
	}
	first := tea.MouseMotionMsg{X: hits[0].x0, Y: hits[0].y0}
	second := tea.MouseMotionMsg{X: hits[1].x0, Y: hits[1].y0}
	model, _ = dispatchRenderedPointer(t, model, first)
	clock.advance(time.Millisecond)
	model, _ = dispatchRenderedPointer(t, model, second)
	if !model.pointerAdmission.havePending {
		t.Fatal("second hover target was not deferred")
	}
	clock.advance(20 * time.Millisecond)
	model, _ = model.updateWithCommands(pointerFlushMsg{sequence: model.pointerAdmission.sequence})
	if got := model.pointerState.Hovered(); got != boardCardHoverID(hits[1]) {
		t.Fatalf("flushed hover = %q, want second card", got)
	}

	before := model.RenderPlanStats().PublishedFrames
	pointX := hits[1].x0
	if hits[1].x1-hits[1].x0 > 1 {
		pointX++
	}
	model, _ = dispatchRenderedPointer(t, model, tea.MouseMotionMsg{X: pointX, Y: hits[1].y0})
	if got := model.RenderPlanStats().PublishedFrames; got != before {
		t.Fatalf("same-target hover published %d frames", got-before)
	}
	if point, ok := model.pointerState.HoverPoint(); !ok || point.X != pointX || point.Y != hits[1].y0 {
		t.Fatalf("retained hover point = (%+v, %t)", point, ok)
	}
}

func TestPointerWheelAtBoundaryIsDiscardedWithoutPendingState(t *testing.T) {
	model, _ := pointerTestModel(t, 120)
	hit := pointerBoardHit(t, model, func(hit boardHit) bool {
		return hit.kind == boardHitDefault && hit.taskID == "" && hit.maxScroll > 0
	})
	column := statusIndex(hit.status)
	model.boardView.scrolls[column] = hit.maxScroll
	model.boardView.scrollAnchors[column] = model.boardScrollAnchor(hit.status, hit.maxScroll)
	model.boardView.manualScroll[column] = true
	model.rebuildRenderPlan(renderImpactAll)
	hit = pointerBoardHit(t, model, func(candidate boardHit) bool {
		return candidate.kind == boardHitDefault && candidate.taskID == "" && candidate.status == hit.status
	})
	before := model.RenderPlanStats().PublishedFrames
	for range 5 {
		model, _ = dispatchRenderedPointer(t, model,
			tea.MouseWheelMsg{X: hit.x0, Y: hit.y0, Button: tea.MouseWheelDown})
	}
	if got := model.RenderPlanStats().PublishedFrames; got != before {
		t.Fatalf("boundary wheel published %d frames", got-before)
	}
	if model.pointerAdmission.havePending || model.pointerAdmission.timer {
		t.Fatal("boundary wheel retained pending state")
	}
}

func TestPointerPendingIntentRetiresAcrossOwnerAndSnapshotChanges(t *testing.T) {
	for _, change := range []struct {
		name  string
		apply func(*Model)
	}{
		{name: "owner", apply: func(model *Model) {
			model.helpOpen = true
			model.rebuildRenderPlan(renderImpactAll)
		}},
		{name: "snapshot", apply: func(model *Model) {
			model.rebuildRenderPlan(renderImpactAll)
		}},
	} {
		t.Run(change.name, func(t *testing.T) {
			model, clock := pointerTestModel(t, 120)
			var hits []boardHit
			seen := map[string]bool{}
			for _, hit := range model.current.semantics.hits {
				if hit.kind == boardHitDefault && hit.taskID != "" && !seen[hit.taskID] {
					seen[hit.taskID] = true
					hits = append(hits, hit)
					if len(hits) == 2 {
						break
					}
				}
			}
			model, _ = dispatchRenderedPointer(t, model,
				tea.MouseMotionMsg{X: hits[0].x0, Y: hits[0].y0})
			clock.advance(time.Millisecond)
			model, _ = dispatchRenderedPointer(t, model,
				tea.MouseMotionMsg{X: hits[1].x0, Y: hits[1].y0})
			sequence := model.pointerAdmission.sequence
			if !model.pointerAdmission.havePending {
				t.Fatal("fixture did not create pending hover")
			}
			change.apply(&model)
			clock.advance(20 * time.Millisecond)
			model, _ = model.updateWithCommands(pointerFlushMsg{sequence: sequence})
			if model.pointerAdmission.havePending || model.pointerAdmission.timer {
				t.Fatal("stale pending pointer intent survived")
			}
		})
	}
}

func TestGeometryStaleDirectHoverCannotApplyRemovedStableTarget(t *testing.T) {
	model, _ := pointerTestModel(t, 120)
	hit := pointerBoardHit(t, model, func(hit boardHit) bool {
		return hit.kind == boardHitDefault && hit.taskID != ""
	})
	raw := tea.MouseMotionMsg{X: hit.x0, Y: hit.y0}
	command := model.current.resolver(raw)
	if command == nil {
		t.Fatal("old geometry did not resolve hover")
	}
	message := command()
	staleRoute := model.pointerRoute()
	nextPlan := *model.current
	nextPlan.geometry.generation++
	nextPlan.semantics.topology = pointer.Topology{}
	model.current = &nextPlan
	before := model.pointerAdmission.accepted
	model, _ = model.admitResolvedPointer(message, staleRoute, raw)
	if model.pointerAdmission.accepted != before || model.pointerState.Hovered() != "" {
		t.Fatalf("removed geometry-stale hover accepted=%d hovered=%q", model.pointerAdmission.accepted, model.pointerState.Hovered())
	}
}

func TestGeometryStaleDirectWheelRebindsCurrentAbsoluteBounds(t *testing.T) {
	model, _ := pointerTestModel(t, 120)
	hit := pointerBoardHit(t, model, func(hit boardHit) bool {
		return hit.kind == boardHitDefault && hit.taskID == "" && hit.maxScroll >= 12
	})
	staleRoute := model.pointerRoute()
	message := boardColumnScrolledMsg{status: hit.status, from: 0, offset: 12, max: hit.maxScroll}
	raw := tea.MouseWheelMsg{X: hit.x0, Y: hit.y0, Button: tea.MouseWheelDown}

	const currentMax = 3
	currentIntent := pointer.WheelIntent{Key: "board:" + string(hit.status), Current: 0, Target: 0, Min: 0, Max: currentMax}
	currentTopology := pointer.Topology{}.WithWheel(currentIntent, func(target int) tea.Msg {
		return boardColumnScrolledMsg{status: hit.status, from: 0,
			offset: min(max(target, 0), currentMax), max: currentMax}
	})
	nextPlan := *model.current
	nextPlan.geometry.generation++
	nextPlan.semantics.topology = currentTopology
	model.current = &nextPlan

	model, _ = model.admitResolvedPointer(message, staleRoute, raw)
	if got := model.boardView.scrolls[statusIndex(hit.status)]; got != currentMax {
		t.Fatalf("geometry-stale wheel target=%d, want current max %d", got, currentMax)
	}
}

func TestGeometryStaleWheelClampsPendingCadenceToContractedBounds(t *testing.T) {
	for _, test := range []struct {
		name          string
		button        tea.MouseButton
		pending       int
		oldTarget     int
		wantScroll    int
		wantPublished uint64
	}{
		{name: "inward", button: tea.MouseWheelUp, pending: 9, oldTarget: 6, wantScroll: 3, wantPublished: 1},
		{name: "outward clamped", button: tea.MouseWheelDown, pending: 9, oldTarget: 9, wantScroll: 6, wantPublished: 0},
		{name: "outward redundant", button: tea.MouseWheelDown, pending: 6, oldTarget: 9, wantScroll: 6, wantPublished: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			model, _ := pointerTestModel(t, 120)
			hit := pointerBoardHit(t, model, func(hit boardHit) bool {
				return hit.kind == boardHitDefault && hit.taskID == "" && hit.maxScroll >= 9
			})
			column := statusIndex(hit.status)
			const oldMax = 9
			const currentMax = 6
			model.boardView.scrolls[column] = currentMax
			model.boardView.scrollAnchors[column] = model.boardScrollAnchor(hit.status, currentMax)
			model.boardView.manualScroll[column] = true
			model.rebuildRenderPlan(renderImpactAll)

			staleRoute := model.pointerRoute()
			raw := tea.MouseWheelMsg{X: hit.x0, Y: hit.y0, Button: test.button}
			model.pointerAdmission.pending = pointerIntent{
				message: boardColumnScrolledMsg{
					status: hit.status,
					from:   max(test.pending-3, 0),
					offset: test.pending,
					max:    oldMax,
				},
				route:       staleRoute,
				sourceRoute: staleRoute,
				raw:         raw,
				target: pointerTarget{
					kind:      pointerTargetWheel,
					wheelKey:  "board:" + string(hit.status),
					position:  test.pending,
					wheelStep: 3,
				},
				wheelDelta: 3,
				advanced:   true,
			}
			model.pointerAdmission.havePending = true

			currentIntent := pointer.WheelIntent{
				Key:     "board:" + string(hit.status),
				Current: currentMax,
				Target:  currentMax,
				Min:     0,
				Max:     currentMax,
			}
			nextPlan := *model.current
			nextPlan.geometry.generation++
			nextPlan.semantics.topology = pointer.Topology{}.WithWheel(currentIntent, func(target int) tea.Msg {
				return boardColumnScrolledMsg{
					status: hit.status,
					from:   currentMax,
					offset: min(max(target, 0), currentMax),
					max:    currentMax,
				}
			})
			model.current = &nextPlan
			before := model.RenderPlanStats().InstalledSnapshotID

			message := boardColumnScrolledMsg{
				status: hit.status,
				from:   oldMax,
				offset: test.oldTarget,
				max:    oldMax,
			}
			model, _ = model.admitResolvedPointer(message, staleRoute, raw)
			if got := model.boardView.scrolls[column]; got != test.wantScroll {
				t.Fatalf("contracted-bound wheel scroll=%d, want %d", got, test.wantScroll)
			}
			if got := model.RenderPlanStats().InstalledSnapshotID - before; got != test.wantPublished {
				t.Fatalf("contracted-bound wheel published %d frames, want %d", got, test.wantPublished)
			}
			if model.pointerAdmission.havePending || model.pointerAdmission.timer {
				t.Fatal("contracted-bound wheel retained stale pending travel")
			}
		})
	}
}

func TestGeometryStaleWheelUsesCurrentPositionInsteadOfPriorGenerationLast(t *testing.T) {
	model, _ := pointerTestModel(t, 120)
	hit := pointerBoardHit(t, model, func(hit boardHit) bool {
		return hit.kind == boardHitDefault && hit.taskID == "" && hit.maxScroll >= 12
	})
	column := statusIndex(hit.status)
	const oldPosition = 9
	const currentPosition = 6
	const currentMax = 12
	model.boardView.scrolls[column] = currentPosition
	model.boardView.scrollAnchors[column] = model.boardScrollAnchor(hit.status, currentPosition)
	model.boardView.manualScroll[column] = true
	model.rebuildRenderPlan(renderImpactAll)

	staleRoute := model.pointerRoute()
	wheelKey := "board:" + string(hit.status)
	model.pointerAdmission.last = pointerTarget{
		kind:      pointerTargetWheel,
		wheelKey:  wheelKey,
		position:  oldPosition,
		wheelStep: 3,
	}
	model.pointerAdmission.haveLast = true
	model.pointerAdmission.lastRoute = staleRoute

	currentIntent := pointer.WheelIntent{
		Key:     wheelKey,
		Current: currentPosition,
		Target:  currentPosition,
		Min:     0,
		Max:     currentMax,
	}
	nextPlan := *model.current
	nextPlan.geometry.generation++
	nextPlan.semantics.topology = pointer.Topology{}.WithWheel(currentIntent, func(target int) tea.Msg {
		return boardColumnScrolledMsg{
			status: hit.status,
			from:   currentPosition,
			offset: min(max(target, 0), currentMax),
			max:    currentMax,
		}
	})
	model.current = &nextPlan

	raw := tea.MouseWheelMsg{X: hit.x0, Y: hit.y0, Button: tea.MouseWheelUp}
	message := boardColumnScrolledMsg{
		status: hit.status,
		from:   oldPosition,
		offset: oldPosition - 3,
		max:    currentMax,
	}
	model, _ = model.admitResolvedPointer(message, staleRoute, raw)
	if got := model.boardView.scrolls[column]; got != currentPosition-3 {
		t.Fatalf("generation-stale last position produced scroll=%d, want current-relative %d",
			got, currentPosition-3)
	}
}

func TestGeometryStaleDirectDragCannotApplyRemovedStableTarget(t *testing.T) {
	model, _ := pointerTestModel(t, 120)
	source := pointerBoardHit(t, model, func(hit boardHit) bool {
		return hit.kind == boardHitDefault && hit.taskID != ""
	})
	target := pointerBoardHit(t, model, func(hit boardHit) bool {
		return hit.kind == boardHitDefault && hit.taskID != "" && hit.taskID != source.taskID
	})
	staleRoute := model.pointerRoute()
	model.pointerAdmission.captureKey = "board-card:" + source.taskID
	model.pointerAdmission.captureOwnerSession = staleRoute.ownerSession
	model.move.beginVisible(model.board, model.board, model.board.Tasks[0], model.boardView.visibleStatuses(), true)

	nextPlan := *model.current
	nextPlan.geometry.generation++
	kept := nextPlan.semantics.hits[:0]
	for _, hit := range nextPlan.semantics.hits {
		if hit.taskID != target.taskID {
			kept = append(kept, hit)
		}
	}
	nextPlan.semantics.hits = kept
	model.current = &nextPlan
	raw := tea.MouseMotionMsg{X: target.x0, Y: target.y0, Button: tea.MouseLeft}
	model, _ = model.admitResolvedPointer(
		boardPointerMoveMsg{status: target.status, beforeTaskID: target.taskID}, staleRoute, raw,
	)
	if model.pointerAdmission.captureKey != "" || model.move.lifted != nil {
		t.Fatal("removed geometry-stale drag retained capture or preview")
	}
}

func TestDeferredFlushValidatesGeometryAsPartOfFullGeneration(t *testing.T) {
	model, clock := pointerTestModel(t, 120)
	var hits []boardHit
	seen := map[string]bool{}
	for _, hit := range model.current.semantics.hits {
		if hit.kind == boardHitDefault && hit.taskID != "" && !seen[hit.taskID] {
			seen[hit.taskID] = true
			hits = append(hits, hit)
			if len(hits) == 2 {
				break
			}
		}
	}
	if len(hits) != 2 {
		t.Fatal("fixture did not expose two distinct stable hover targets")
	}
	model, _ = dispatchRenderedPointer(t, model, tea.MouseMotionMsg{X: hits[0].x0, Y: hits[0].y0})
	clock.advance(time.Millisecond)
	model, _ = dispatchRenderedPointer(t, model, tea.MouseMotionMsg{X: hits[1].x0, Y: hits[1].y0})
	sequence := model.pointerAdmission.sequence
	if !model.pointerAdmission.havePending {
		t.Fatal("fixture did not create pending hover")
	}
	nextPlan := *model.current
	nextPlan.geometry.generation++
	nextPlan.semantics.topology = pointer.Topology{}
	model.current = &nextPlan
	clock.advance(20 * time.Millisecond)
	model, _ = model.updateWithCommands(pointerFlushMsg{sequence: sequence})
	if model.pointerAdmission.havePending || model.pointerAdmission.timer ||
		model.pointerState.Hovered() == boardCardHoverID(hits[1]) {
		t.Fatal("geometry-stale deferred hover bypassed full-generation validation")
	}
}

func TestPointerStopClearsMailboxCadenceAndCapture(t *testing.T) {
	model, _ := pointerTestModel(t, 120)
	hit := pointerControlHit(t, model, board.StatusDoing)
	raw := tea.MouseClickMsg{X: hit.x0, Y: hit.y0, Button: tea.MouseLeft}
	model, _ = dispatchRenderedPointer(t, model, raw)
	if !model.pointerState.Active() || model.pointerAdmission.captureKey == "" {
		t.Fatal("fixture did not acquire pointer capture")
	}
	updated, _ := model.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	model = updated.(Model)
	if !model.stopped || model.pointerAdmission.havePending || model.pointerAdmission.timer ||
		model.pointerAdmission.captureKey != "" || model.pointerState.Active() {
		t.Fatal("stop retained pointer pipeline state")
	}
	next, commands := model.updateWithCommands(raw)
	if commands.followUp != nil || commands.geometry != nil || next.pointerState.Active() {
		t.Fatal("raw input entered a stopped model")
	}
}

func TestStationaryBoardHoverReresolvesAfterWheelScroll(t *testing.T) {
	model, clock := pointerTestModel(t, 120)
	column := pointerBoardHit(t, model, func(hit boardHit) bool {
		return hit.kind == boardHitDefault && hit.taskID == "" && hit.maxScroll > 3
	})
	hit := pointerBoardHit(t, model, func(hit boardHit) bool {
		return hit.kind == boardHitDefault && hit.taskID != "" && hit.status == column.status
	})
	point := tea.MouseMotionMsg{X: hit.x0, Y: hit.y0}
	model, _ = dispatchRenderedPointer(t, model, point)
	before := model.pointerState.Hovered()
	clock.advance(20 * time.Millisecond)
	column = pointerBoardHit(t, model, func(candidate boardHit) bool {
		return candidate.kind == boardHitDefault && candidate.taskID == "" && candidate.status == hit.status
	})
	model, _ = dispatchRenderedPointer(t, model,
		tea.MouseWheelMsg{X: column.x0, Y: column.y0, Button: tea.MouseWheelDown})
	clock.advance(time.Millisecond)
	for range 4 {
		model, _ = dispatchRenderedPointer(t, model,
			tea.MouseWheelMsg{X: column.x0, Y: column.y0, Button: tea.MouseWheelDown})
	}
	clock.advance(20 * time.Millisecond)
	model, _ = model.updateWithCommands(pointerFlushMsg{sequence: model.pointerAdmission.sequence})
	after := model.pointerState.Hovered()
	if after == before {
		t.Fatalf("stationary hover remained on scrolled card %q", before)
	}
	if point, ok := model.pointerState.HoverPoint(); !ok || point.X != hit.x0 || point.Y != hit.y0 {
		t.Fatalf("hover point moved during re-resolution: (%+v, %t)", point, ok)
	}
}

func TestGeometryStaleBoardPressAndUnresolvedReleaseKeepCaptureOrdered(t *testing.T) {
	store := &moveTestStore{board: moveFixture()}
	model := loadedMoveModel(store)
	model.width, model.height = performanceWidth, performanceHeight
	model.rebuildRenderPlan(renderImpactAll)
	handler := model.View().OnMouse
	source := pointerBoardHit(t, model, func(hit boardHit) bool {
		return hit.kind == boardHitDefault && hit.taskID == "a"
	})
	model.rebuildRenderPlan(renderImpactAll)

	model, _ = dispatchFlushedPointer(t, model, handler,
		tea.MouseClickMsg{X: source.x0, Y: source.y0, Button: tea.MouseLeft})
	if model.pointerAdmission.captureKey != "board-card:a" || model.move.lifted == nil {
		t.Fatalf("stale press capture=%q lifted=%v", model.pointerAdmission.captureKey, model.move.lifted)
	}

	model, _ = dispatchFlushedPointer(t, model, handler,
		tea.MouseReleaseMsg{X: 0, Y: 0, Button: tea.MouseNone})
	if model.pointerAdmission.captureKey != "" || model.move.lifted != nil || model.move.saving {
		t.Fatalf("stale unresolved release capture=%q lifted=%v saving=%t",
			model.pointerAdmission.captureKey, model.move.lifted, model.move.saving)
	}
}

func TestGeometryStaleTrackedCancelClearsCapture(t *testing.T) {
	model, _ := pointerTestModel(t, 120)
	hit := pointerControlHit(t, model, board.StatusDoing)
	handler := model.View().OnMouse
	model, _ = dispatchFlushedPointer(t, model, handler,
		tea.MouseClickMsg{X: hit.x0, Y: hit.y0, Button: tea.MouseLeft})
	if model.pointerAdmission.captureKey == "" || !model.pointerState.Active() {
		t.Fatal("fixture did not acquire tracked capture")
	}

	model, _ = dispatchFlushedPointer(t, model, handler,
		tea.MouseMotionMsg{X: hit.x0, Y: hit.y0, Button: tea.MouseLeft})
	if model.pointerAdmission.captureKey != "" || model.pointerState.Active() {
		t.Fatal("geometry-stale cancel retained tracked capture")
	}
}

func TestGeometryStaleUnstableBoardClickFailsClosed(t *testing.T) {
	model, _ := pointerTestModel(t, 12)
	var raw tea.MouseClickMsg
	found := false
	for _, hit := range model.current.semantics.hits {
		if hit.kind != boardHitDefault || hit.taskID != "" {
			continue
		}
		for y := hit.y0; y < hit.y1 && !found; y++ {
			for x := hit.x0; x < hit.x1; x++ {
				covered := false
				for _, other := range model.current.semantics.hits {
					if other.kind == boardHitDefault && other.taskID == "" {
						continue
					}
					if x >= other.x0 && x < other.x1 && y >= other.y0 && y < other.y1 {
						covered = true
						break
					}
				}
				if covered {
					continue
				}
				raw = tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatal("fixture did not expose an untracked board column cell")
	}
	handler := model.View().OnMouse
	model.rebuildRenderPlan(renderImpactAll)
	beforeAccepted := model.pointerAdmission.accepted
	beforeDiscarded := model.pointerAdmission.discarded
	beforeColumn := model.boardView.column

	model, _ = dispatchFlushedPointer(t, model, handler, raw)
	if model.pointerAdmission.accepted != beforeAccepted ||
		model.pointerAdmission.discarded != beforeDiscarded+1 || model.boardView.column != beforeColumn {
		t.Fatalf("unstable stale click accepted=%d discarded=%d column=%d, want %d",
			model.pointerAdmission.accepted-beforeAccepted,
			model.pointerAdmission.discarded-beforeDiscarded, model.boardView.column, beforeColumn)
	}
}

func TestPointerDragRequiresCapture(t *testing.T) {
	t.Run("missing capture", func(t *testing.T) {
		model, _ := pointerTestModel(t, 120)
		hit := pointerBoardHit(t, model, func(hit boardHit) bool {
			return hit.kind == boardHitDefault && hit.taskID != ""
		})
		before := model.pointerAdmission.discarded
		model, _ = dispatchRenderedPointer(t, model,
			tea.MouseMotionMsg{X: hit.x0, Y: hit.y0, Button: tea.MouseLeft})
		if model.pointerAdmission.discarded != before+1 || model.move.lifted != nil {
			t.Fatalf("uncaptured drag discarded=%d lifted=%v",
				model.pointerAdmission.discarded-before, model.move.lifted)
		}
	})
}

func TestPointerTargetSameDestination(t *testing.T) {
	tests := []struct {
		name  string
		left  pointerTarget
		right pointerTarget
		want  bool
	}{
		{name: "equal drag insertion", left: pointerTarget{kind: pointerTargetDrag, status: board.StatusDoing, beforeTaskID: "b"},
			right: pointerTarget{kind: pointerTargetDrag, status: board.StatusDoing, beforeTaskID: "b"}, want: true},
		{name: "different drag status", left: pointerTarget{kind: pointerTargetDrag, status: board.StatusDoing, beforeTaskID: "b"},
			right: pointerTarget{kind: pointerTargetDrag, status: board.StatusDone, beforeTaskID: "b"}},
		{name: "different drag anchor", left: pointerTarget{kind: pointerTargetDrag, status: board.StatusDoing, beforeTaskID: "b"},
			right: pointerTarget{kind: pointerTargetDrag, status: board.StatusDoing, beforeTaskID: "c"}},
		{name: "unknown kind", left: pointerTarget{}, right: pointerTarget{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.left.sameDestination(test.right); got != test.want {
				t.Fatalf("sameDestination(%+v, %+v) = %t, want %t", test.left, test.right, got, test.want)
			}
		})
	}
}

func TestPointerCadenceExpiryRetiresPendingDestination(t *testing.T) {
	model, clock := pointerTestModel(t, 120)
	var hits []boardHit
	seen := map[string]bool{}
	for _, hit := range model.current.semantics.hits {
		if hit.kind == boardHitDefault && hit.taskID != "" && !seen[hit.taskID] {
			seen[hit.taskID] = true
			hits = append(hits, hit)
			if len(hits) == 3 {
				break
			}
		}
	}
	if len(hits) != 3 {
		t.Fatal("fixture did not expose three card destinations")
	}
	model, _ = dispatchRenderedPointer(t, model, tea.MouseMotionMsg{X: hits[0].x0, Y: hits[0].y0})
	clock.advance(time.Millisecond)
	model, _ = dispatchRenderedPointer(t, model, tea.MouseMotionMsg{X: hits[1].x0, Y: hits[1].y0})
	if !model.pointerAdmission.havePending {
		t.Fatal("fixture did not defer the second hover")
	}
	before := model.pointerAdmission.discarded
	clock.advance(20 * time.Millisecond)
	model, _ = dispatchRenderedPointer(t, model, tea.MouseMotionMsg{X: hits[2].x0, Y: hits[2].y0})
	if model.pointerAdmission.havePending || model.pointerAdmission.timer ||
		model.pointerAdmission.discarded != before+1 || model.pointerState.Hovered() != boardCardHoverID(hits[2]) {
		t.Fatalf("expired cadence pending=%t timer=%t discarded=%d hovered=%q",
			model.pointerAdmission.havePending, model.pointerAdmission.timer,
			model.pointerAdmission.discarded-before, model.pointerState.Hovered())
	}
}

func TestRetainedBoundaryWheelDoesNotInventPastMaximum(t *testing.T) {
	model, _ := pointerTestModel(t, 120)
	hit := pointerBoardHit(t, model, func(hit boardHit) bool {
		return hit.kind == boardHitDefault && hit.taskID == "" && hit.maxScroll >= 6
	})
	column := statusIndex(hit.status)
	model.boardView.scrolls[column] = hit.maxScroll
	model.boardView.scrollAnchors[column] = model.boardScrollAnchor(hit.status, hit.maxScroll)
	model.boardView.manualScroll[column] = true
	model.rebuildRenderPlan(renderImpactAll)
	hit = pointerBoardHit(t, model, func(candidate boardHit) bool {
		return candidate.kind == boardHitDefault && candidate.taskID == "" && candidate.status == hit.status
	})
	raw := tea.MouseWheelMsg{X: hit.x0, Y: hit.y0, Button: tea.MouseWheelDown}
	candidate := boundaryWheelCandidate(hit, model.pointerRoute(), raw)
	candidate.message = boardColumnScrolledMsg{status: hit.status, from: hit.maxScroll - 3,
		offset: hit.maxScroll, max: hit.maxScroll}
	candidate.target.position = hit.maxScroll
	model.pointerAdmission.pending = candidate
	model.pointerAdmission.havePending = true
	before := model.pointerAdmission.discarded

	model, commands := dispatchRenderedPointer(t, model, raw)
	if got := model.boardView.scrolls[column]; got != hit.maxScroll {
		t.Fatalf("retained boundary wheel scrolled to %d, want %d", got, hit.maxScroll)
	}
	if model.pointerAdmission.havePending || commands.followUp != nil ||
		model.pointerAdmission.discarded != before+1 {
		t.Fatalf("retained boundary pending=%t followup=%v discarded=%d",
			model.pointerAdmission.havePending, commands.followUp,
			model.pointerAdmission.discarded-before)
	}
}

func TestPointerWheelCellAxisIdentity(t *testing.T) {
	left := tea.MouseWheelMsg{X: 4, Y: 7, Button: tea.MouseWheelLeft, Mod: tea.ModCtrl}
	right := tea.MouseWheelMsg{X: 4, Y: 7, Button: tea.MouseWheelRight, Mod: tea.ModCtrl}
	down := tea.MouseWheelMsg{X: 4, Y: 7, Button: tea.MouseWheelDown, Mod: tea.ModCtrl}
	if !sameWheelCellAxisAndModifiers(left, right) {
		t.Fatal("horizontal reversal lost cell and axis identity")
	}
	if sameWheelCellAxisAndModifiers(left, down) {
		t.Fatal("horizontal and vertical wheels shared an axis")
	}
	if got := wheelAxis(tea.MouseLeft); got != 0 {
		t.Fatalf("non-wheel button axis = %d, want 0", got)
	}
	if direction, ok := rawWheelDirection(tea.MouseMotionMsg{X: 4, Y: 7}); ok || direction != 0 {
		t.Fatalf("motion wheel direction = (%d, %t), want rejected", direction, ok)
	}
}
