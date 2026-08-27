package tui

import (
	"reflect"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/tui/pointer"
)

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

	clock.advance(20 * time.Millisecond)
	model, _ = model.updateWithCommands(pointerFlushMsg{sequence: model.pointerAdmission.sequence})
	if got := model.boardView.scrolls[column]; got != 12 {
		t.Fatalf("flushed scroll = %d, want 12", got)
	}
	if model.pointerAdmission.havePending || model.pointerAdmission.timer {
		t.Fatal("flush retained pending wheel state")
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
