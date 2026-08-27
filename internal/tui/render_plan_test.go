package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/tui/pointer"
)

var renderPlanViewSink tea.View

func TestUpdatePublishesColdEquivalentRetainedView(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	fixture := boardViewFixture(now)
	model := NewModel(stubBoardReader{board: fixture}, nil, "alice")
	updated, _ := model.Update(boardLoadedMsg{board: fixture})
	model = updated.(Model)

	want := model.renderColdView()
	got := model.View()
	if got.Content != want.Content {
		t.Fatal("retained content differs from the cold renderer")
	}
	if got.AltScreen != want.AltScreen || got.MouseMode != want.MouseMode {
		t.Fatalf("retained terminal modes = alt:%t mouse:%v, want alt:%t mouse:%v",
			got.AltScreen, got.MouseMode, want.AltScreen, want.MouseMode)
	}
	if got.OnMouse == nil || want.OnMouse == nil {
		t.Fatal("board snapshot did not publish content and pointer hits together")
	}

	hit := boardHitFor(t, model, func(hit boardHit) bool {
		return hit.kind == boardHitDefault && hit.taskID == fixture.Tasks[0].ID
	})
	message := tea.MouseClickMsg{X: hit.x0, Y: hit.y0, Button: tea.MouseLeft}
	gotCommand := got.OnMouse(message)
	wantCommand := want.OnMouse(message)
	if gotCommand != nil || wantCommand == nil {
		t.Fatal("retained pointer map escaped its mailbox or cold map missed a visible card")
	}
	wantPress, wantOK := wantCommand().(boardPointerDownMsg)
	if !wantOK {
		t.Fatalf("cold pointer result = %#v", wantCommand())
	}
	model, _ = model.updateWithCommands(message)
	if model.move.lifted == nil || model.move.lifted.taskID != wantPress.taskID {
		t.Fatalf("retained pointer selected %#v, cold pointer result = %#v", model.move.lifted, wantPress)
	}
}

func TestViewDoesNotRebuildTheRenderPlan(t *testing.T) {
	fixture := board.Board{Title: "Board", Tasks: []board.Task{{
		ID: "one", Title: "One", Status: board.StatusTodo,
	}}}
	model := NewModel(stubBoardReader{board: fixture}, nil, "alice")
	updated, _ := model.Update(boardLoadedMsg{board: fixture})
	model = updated.(Model)

	before := model.RenderPlanStats()
	first := model.View()
	second := model.View()
	after := model.RenderPlanStats()
	if first.Content != second.Content {
		t.Fatal("an unchanged retained view changed content")
	}
	if after.Builds != before.Builds || after.PublishedFrames != before.PublishedFrames {
		t.Fatalf("View rebuilt the plan: before=%+v after=%+v", before, after)
	}
}

func TestProductionUpdatePublishesAcceptedMessageSnapshot(t *testing.T) {
	fixture := board.Board{Title: "Board", Tasks: []board.Task{{
		ID: "one", Title: "One", Status: board.StatusTodo,
	}}}
	model := NewModel(stubBoardReader{board: fixture}, nil, "alice")
	before := model.RenderPlanStats()

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model = updated.(Model)
	after := model.RenderPlanStats()

	if after.AcceptedMessageID != before.AcceptedMessageID+1 {
		t.Fatalf("accepted message id = %d, want %d", after.AcceptedMessageID, before.AcceptedMessageID+1)
	}
	if after.InstalledSnapshotID != before.InstalledSnapshotID+1 {
		t.Fatalf("installed snapshot id = %d, want %d", after.InstalledSnapshotID, before.InstalledSnapshotID+1)
	}
	if after.InstalledForMessageID != after.AcceptedMessageID {
		t.Fatalf("snapshot installed for message %d, latest accepted is %d",
			after.InstalledForMessageID, after.AcceptedMessageID)
	}
	if after.PublishedFrames != before.PublishedFrames+1 || model.View().Content == "" {
		t.Fatalf("production Update did not publish a complete frame: before=%+v after=%+v", before, after)
	}
}

func TestSemanticNoOpUpdatesDoNotPublishFrames(t *testing.T) {
	fixture := board.Board{Title: "Board", Tasks: []board.Task{{
		ID: "one", Title: "One", Status: board.StatusTodo,
	}}}
	model := NewModel(stubBoardReader{board: fixture}, stubVersionReader{version: 7}, "alice")
	updated, _ := model.Update(boardLoadedMsg{board: fixture})
	model = updated.(Model)
	model.haveVersion = true
	model.dataVersion = 7
	model.rebuildRenderPlan(renderImpactAll)

	for _, message := range []tea.Msg{
		tea.WindowSizeMsg{Width: model.width, Height: model.height},
		pollTickMsg{},
		dataVersionMsg{version: 7},
	} {
		before := model.RenderPlanStats()
		updated, command := model.Update(message)
		model = updated.(Model)
		after := model.RenderPlanStats()
		if after.AcceptedMessageID != before.AcceptedMessageID+1 {
			t.Fatalf("%T accepted message id = %d, want %d", message, after.AcceptedMessageID, before.AcceptedMessageID+1)
		}
		if after.PublishedFrames != before.PublishedFrames || after.InstalledSnapshotID != before.InstalledSnapshotID {
			t.Fatalf("%T published a semantic no-op: before=%+v after=%+v", message, before, after)
		}
		if command == nil && (message == (pollTickMsg{}) || message == (dataVersionMsg{version: 7})) {
			t.Fatalf("%T dropped its follow-up command", message)
		}
	}
}

func TestUnknownMessagesRemainConservative(t *testing.T) {
	type unknownMessage struct{}
	model := NewModel(stubBoardReader{}, nil, "alice")
	before := model.RenderPlanStats()
	updated, _ := model.Update(unknownMessage{})
	after := updated.(Model).RenderPlanStats()
	if after.PublishedFrames != before.PublishedFrames+1 {
		t.Fatalf("unknown message frames = %d, want %d", after.PublishedFrames, before.PublishedFrames+1)
	}
	want := before.Revisions
	want.advance(renderImpactAll)
	if after.Revisions != want {
		t.Fatalf("unknown message revisions = %+v, want %+v", after.Revisions, want)
	}
}

func TestUpdateImpactsRemainConservative(t *testing.T) {
	fixture := board.Board{Title: "Board", Tasks: []board.Task{{
		ID: "one", Title: "One", Status: board.StatusTodo,
	}}}
	model := NewModel(stubBoardReader{board: fixture}, nil, "alice")
	before := model.RenderPlanStats().Revisions
	updated, _ := model.Update(boardCardClickedMsg{taskID: fixture.Tasks[0].ID})
	after := updated.(Model).RenderPlanStats().Revisions

	want := before
	want.advance(renderImpactAll)
	if after != want {
		t.Fatalf("card-click revisions = %+v, want conservative %+v", after, want)
	}
}

func TestRetainedPlanOwnedBytesEstimateIncludesCapturedIdentity(t *testing.T) {
	statsForID := func(id string) RenderPlanStats {
		fixture := board.Board{Title: "Board", Tasks: []board.Task{{
			ID: id, Title: "Same visible title", Status: board.StatusTodo,
		}}}
		model := NewModel(stubBoardReader{board: fixture}, nil, "alice")
		updated, _ := model.Update(boardLoadedMsg{board: fixture})
		model = updated.(Model)
		updated, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
		return updated.(Model).RenderPlanStats()
	}

	short := statsForID("a")
	long := statsForID(strings.Repeat("identity-", 128))
	if short.RetainedPlanOwnedBytesEstimate <= short.ContentBytes {
		t.Fatalf("retained estimate %d did not charge topology above %d content bytes",
			short.RetainedPlanOwnedBytesEstimate, short.ContentBytes)
	}
	if long.RetainedPlanOwnedBytesEstimate <= short.RetainedPlanOwnedBytesEstimate {
		t.Fatalf("captured identity was not charged: short=%d long=%d",
			short.RetainedPlanOwnedBytesEstimate, long.RetainedPlanOwnedBytesEstimate)
	}
}

func TestViewAllocatesNothing(t *testing.T) {
	fixture := board.Board{Title: "Board", Tasks: []board.Task{{
		ID: "one", Title: "One", Status: board.StatusTodo,
	}}}
	model := NewModel(stubBoardReader{board: fixture}, nil, "alice")
	updated, _ := model.Update(boardLoadedMsg{board: fixture})
	model = updated.(Model)

	if allocations := testing.AllocsPerRun(1000, func() {
		renderPlanViewSink = model.View()
	}); allocations != 0 {
		t.Fatalf("View allocations = %v, want zero", allocations)
	}
}

func TestOverlayPublicationsReuseLazyBoardBases(t *testing.T) {
	model := performanceModel(17, "", 120, 36)
	_ = model.reconcileTemporalSchedule()
	base := model.RenderPlanStats()

	model, openCommands := model.updateWithCommands(tea.KeyPressMsg{Code: '?', Text: "?"})
	opened := model.RenderPlanStats()
	if openCommands.geometry != nil || opened.NormalBaseBuilds != base.NormalBaseBuilds ||
		opened.DimmedBaseBuilds != base.DimmedBaseBuilds+1 ||
		opened.OverlayCompositions != base.OverlayCompositions+1 {
		t.Fatalf("overlay open work = before=%+v after=%+v", base, opened)
	}
	assertPerformanceColdOracleParity(t, model)
	openView := model.View()

	model, updateCommands := model.updateWithCommands(tea.KeyPressMsg{Code: 'x', Text: "x"})
	updated := model.RenderPlanStats()
	if updateCommands.geometry != nil || updated.NormalBaseBuilds != opened.NormalBaseBuilds ||
		updated.DimmedBaseBuilds != opened.DimmedBaseBuilds ||
		updated.RenderedCardRecords != opened.RenderedCardRecords ||
		updated.ProjectionBuilds != opened.ProjectionBuilds ||
		updated.ProjectionTaskVisits != opened.ProjectionTaskVisits ||
		updated.SynchronousLayoutRecords != opened.SynchronousLayoutRecords ||
		updated.OverlayCompositions != opened.OverlayCompositions+1 {
		t.Fatalf("overlay cache update did base work: before=%+v after=%+v", opened, updated)
	}
	if model.View().OnMouse == nil || openView.OnMouse == nil ||
		updated.InstalledSnapshotID != opened.InstalledSnapshotID+1 {
		t.Fatal("cached overlay publication did not install a fresh pointer snapshot")
	}

	model, closeCommands := model.updateWithCommands(tea.KeyPressMsg{Code: '?', Text: "?"})
	closed := model.RenderPlanStats()
	if closeCommands.geometry != nil || closed.NormalBaseBuilds != updated.NormalBaseBuilds ||
		closed.DimmedBaseBuilds != updated.DimmedBaseBuilds ||
		closed.RenderedCardRecords != updated.RenderedCardRecords ||
		closed.OverlayCompositions != updated.OverlayCompositions+1 {
		t.Fatalf("overlay close did base work: before=%+v after=%+v", updated, closed)
	}
	assertPerformanceColdOracleParity(t, model)

	model, _ = model.updateWithCommands(tea.KeyPressMsg{Code: '?', Text: "?"})
	reopened := model.RenderPlanStats()
	if reopened.DimmedBaseBuilds != closed.DimmedBaseBuilds ||
		reopened.NormalBaseBuilds != closed.NormalBaseBuilds {
		t.Fatalf("overlay reopen missed retained bases: before=%+v after=%+v", closed, reopened)
	}
}

func TestBaseImpactInvalidatesLazyDimmedBase(t *testing.T) {
	model := performanceModel(17, "", 120, 36)
	model, _ = model.updateWithCommands(tea.KeyPressMsg{Code: '?', Text: "?"})
	model, _ = model.updateWithCommands(tea.KeyPressMsg{Code: '?', Text: "?"})
	before := model.RenderPlanStats()

	model, _ = model.updateWithCommands(tea.WindowSizeMsg{Width: 100, Height: 30})
	afterResize := model.RenderPlanStats()
	if afterResize.NormalBaseBuilds != before.NormalBaseBuilds+1 {
		t.Fatalf("resize normal base builds = %d, want %d", afterResize.NormalBaseBuilds, before.NormalBaseBuilds+1)
	}
	model, _ = model.updateWithCommands(tea.KeyPressMsg{Code: '?', Text: "?"})
	afterOpen := model.RenderPlanStats()
	if afterOpen.DimmedBaseBuilds != afterResize.DimmedBaseBuilds+1 {
		t.Fatalf("post-resize dim base builds = %d, want %d", afterOpen.DimmedBaseBuilds, afterResize.DimmedBaseBuilds+1)
	}
	assertPerformanceColdOracleParity(t, model)
}

func TestCachedOverlayPublicationRejectsPreviousSnapshotRoute(t *testing.T) {
	model := performanceModel(17, "", 120, 36)
	model, _ = model.updateWithCommands(tea.KeyPressMsg{Code: '?', Text: "?"})
	previous := model.View()
	model, _ = model.updateWithCommands(tea.KeyPressMsg{Code: 'x', Text: "x"})
	before := model.RenderPlanStats()
	if previous.OnMouse == nil || model.View().OnMouse == nil {
		t.Fatal("overlay publications did not install pointer handlers")
	}
	raw := tea.MouseMotionMsg{X: 0, Y: 0, Button: tea.MouseNone}
	if command := previous.OnMouse(raw); command != nil {
		t.Fatal("previous snapshot resolver escaped its mailbox")
	}
	_, previousRoute, result := model.pointerMailbox.take(raw)
	if result != pointerMailboxMatched || previousRoute.snapshot == before.InstalledSnapshotID {
		t.Fatalf("previous handler route = %+v result=%v, current snapshot=%d", previousRoute, result, before.InstalledSnapshotID)
	}
	if command := model.View().OnMouse(raw); command != nil {
		t.Fatal("current snapshot resolver escaped its mailbox")
	}
	_, currentRoute, result := model.pointerMailbox.take(raw)
	if result != pointerMailboxMatched || currentRoute.snapshot != before.InstalledSnapshotID ||
		currentRoute.snapshot == previousRoute.snapshot {
		t.Fatalf("fresh handler route = %+v previous=%+v result=%v", currentRoute, previousRoute, result)
	}
}

func TestTaskActionPointerStateDoesNotResurrectThroughCachedBoardBase(t *testing.T) {
	model := performanceModel(17, "", 120, 36)
	model, _ = model.updateWithCommands(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if !model.action.open() || !model.current.bases.haveDimmed {
		t.Fatal("task action did not open over a retained dimmed base")
	}
	surface := model.taskActionSurface(model.current.bases.dimmed.content)
	var point pointer.Point
	var hover tea.Msg
	for y := 0; y < model.height && hover == nil; y++ {
		for x := 0; x < model.width; x++ {
			command := surface.Pointer(tea.MouseMotionMsg{X: x, Y: y, Button: tea.MouseNone})
			if command == nil {
				continue
			}
			message := command()
			interaction, ok := pointer.ObserveInteraction(message)
			if ok && interaction.Kind == pointer.InteractionHover && interaction.ID != "" {
				point, hover = interaction.Point, message
				break
			}
		}
	}
	if hover == nil {
		t.Fatal("task action pointer surface exposed no hoverable control")
	}
	beforeHover := model.RenderPlanStats()
	model, _ = model.applyResolvedPointer(hover, true)
	afterHover := model.RenderPlanStats()
	if afterHover.NormalBaseBuilds != beforeHover.NormalBaseBuilds ||
		afterHover.DimmedBaseBuilds != beforeHover.DimmedBaseBuilds ||
		afterHover.RenderedCardRecords != beforeHover.RenderedCardRecords {
		t.Fatalf("task action hover rebuilt board base: before=%+v after=%+v", beforeHover, afterHover)
	}
	pressCommand := surface.Pointer(tea.MouseClickMsg{X: point.X, Y: point.Y, Button: tea.MouseLeft})
	if pressCommand == nil {
		t.Fatal("task action control exposed no press message")
	}
	press := pressCommand()
	interaction, ok := pointer.ObserveInteraction(press)
	if !ok || interaction.Kind != pointer.InteractionPress {
		t.Fatalf("task action click resolved to %#v", press)
	}
	model, _ = model.applyResolvedPointer(press, true)
	if !model.pointerState.Active() || model.pointerState.Hovered() == "" {
		t.Fatalf("task action pointer feedback was not installed: %+v", model.pointerState)
	}
	beforeClose := model.RenderPlanStats()
	model, _ = model.updateWithCommands(tea.KeyPressMsg{Code: tea.KeyEscape})
	afterClose := model.RenderPlanStats()
	if model.pointerState.Active() || model.pointerState.Hovered() != "" {
		t.Fatalf("task action pointer feedback survived owner close: %+v", model.pointerState)
	}
	if afterClose.NormalBaseBuilds != beforeClose.NormalBaseBuilds ||
		afterClose.DimmedBaseBuilds != beforeClose.DimmedBaseBuilds ||
		afterClose.RenderedCardRecords != beforeClose.RenderedCardRecords {
		t.Fatalf("task action close rebuilt cached board base: before=%+v after=%+v", beforeClose, afterClose)
	}
	assertPerformanceColdOracleParity(t, model)
}

func TestOverlayHoverSurvivesBoardBaseRebuildAndGeometryConvergence(t *testing.T) {
	model := performanceModel(1000, "", 120, 36)
	model, _ = model.updateWithCommands(tea.KeyPressMsg{Code: '?', Text: "?"})
	hovered, point := hoverHelpControl(t, &model)
	previous := model.View()

	model, commands := model.updateWithCommands(tea.WindowSizeMsg{Width: 100, Height: 30})
	if got := model.pointerState.Hovered(); got != hovered {
		t.Fatalf("overlay hover after base rebuild = %q, want %q", got, hovered)
	}
	settlePerformanceGeometryCommand(t, &model, commands.geometry)
	if got := model.pointerState.Hovered(); got != hovered {
		t.Fatalf("overlay hover after geometry convergence = %q, want %q", got, hovered)
	}
	if got, ok := model.pointerState.HoverPoint(); !ok || got != point {
		t.Fatalf("overlay hover point after convergence = %+v, %t; want %+v", got, ok, point)
	}
	if model.current.semantics.handler != renderHandlerHelp || !model.current.bases.haveNormal ||
		!model.current.bases.haveDimmed {
		t.Fatalf("overlay/base state after convergence = handler:%v normal:%t dimmed:%t",
			model.current.semantics.handler, model.current.bases.haveNormal, model.current.bases.haveDimmed)
	}
	assertPerformanceColdOracleParity(t, model)
	assertFreshPointerRoute(t, &model, previous)
}

func TestOverlayHoverSurvivesBoardRefresh(t *testing.T) {
	model := performanceModel(1000, "", 120, 36)
	model, _ = model.updateWithCommands(tea.KeyPressMsg{Code: '?', Text: "?"})
	hovered, point := hoverHelpControl(t, &model)
	previous := model.View()
	refreshed := appendPerformanceTasks(model.board, 1, true)

	model, commands := model.updateWithCommands(boardLoadedMsg{board: refreshed})
	if got := model.pointerState.Hovered(); got != hovered {
		t.Fatalf("overlay hover after board refresh = %q, want %q", got, hovered)
	}
	settlePerformanceGeometryCommand(t, &model, commands.geometry)
	if got := model.pointerState.Hovered(); got != hovered {
		t.Fatalf("overlay hover after refreshed geometry = %q, want %q", got, hovered)
	}
	if got, ok := model.pointerState.HoverPoint(); !ok || got != point {
		t.Fatalf("overlay hover point after refresh = %+v, %t; want %+v", got, ok, point)
	}
	if model.current.semantics.handler != renderHandlerHelp || !model.current.bases.haveNormal ||
		!model.current.bases.haveDimmed {
		t.Fatal("board refresh did not retain complete overlay bases")
	}
	assertPerformanceColdOracleParity(t, model)
	assertFreshPointerRoute(t, &model, previous)
}

func TestOverlayHoverSurvivesTemporalTick(t *testing.T) {
	model := performanceModel(1000, "", 120, 36)
	model.temporalSchedule = func(_ time.Duration, message temporalTickMsg) tea.Cmd {
		return func() tea.Msg { return message }
	}
	if model.temporalDeadline.IsZero() {
		if command := model.reconcileTemporalSchedule(); command == nil {
			t.Fatal("temporal scheduler did not arm")
		}
	}
	model, _ = model.updateWithCommands(tea.KeyPressMsg{Code: '?', Text: "?"})
	hovered, point := hoverHelpControl(t, &model)
	previous := model.View()
	deadline, generation := model.temporalDeadline, model.temporalGeneration
	model.now = func() time.Time { return deadline }

	model, commands := model.updateWithCommands(temporalTickMsg{generation: generation, deadline: deadline})
	if got := model.pointerState.Hovered(); got != hovered {
		t.Fatalf("overlay hover after temporal tick = %q, want %q", got, hovered)
	}
	settlePerformanceGeometryCommand(t, &model, commands.geometry)
	if got, ok := model.pointerState.HoverPoint(); !ok || got != point || model.pointerState.Hovered() != hovered {
		t.Fatalf("overlay hover after temporal settlement = %q %+v %t; want %q %+v",
			model.pointerState.Hovered(), got, ok, hovered, point)
	}
	if model.current.semantics.handler != renderHandlerHelp || !model.current.bases.haveNormal ||
		!model.current.bases.haveDimmed {
		t.Fatal("temporal tick did not retain complete overlay bases")
	}
	assertPerformanceColdOracleParity(t, model)
	assertFreshPointerRoute(t, &model, previous)
}

func TestOverlayClassifierUsesConstantTimeBoardAndShippedIdentity(t *testing.T) {
	model := performanceModel(1000, "", performanceWidth, performanceHeight)
	ids := make([]string, 20000)
	for index := range ids {
		ids[index] = "shipped-" + string(rune(index/2+1))
	}
	model.adoptShippedAt(shippedRecord{
		Date: model.renderedAt.Format(shippedDateLayout), IDs: ids,
	}, model.renderedAt)
	model.rebuildRenderPlan(renderImpactAppearance)

	beforeIdentity, afterIdentity := model, model
	if !boardBaseStableAcross(&beforeIdentity, &afterIdentity) {
		t.Fatal("identical immutable inputs did not preserve the board base")
	}

	baseline := model.RenderPlanStats()
	model, _ = model.updateWithCommands(tea.KeyPressMsg{Code: '?', Text: "?"})
	_, _ = hoverHelpControl(t, &model)
	model, _ = model.updateWithCommands(tea.KeyPressMsg{Code: 'x', Text: "x"})
	after := model.RenderPlanStats()
	if after.SourceTaskComparisons != baseline.SourceTaskComparisons ||
		after.ShippedIDVisits != baseline.ShippedIDVisits {
		t.Fatalf("overlay identity path performed board/shipped work: before=%+v after=%+v", baseline, after)
	}
}

func hoverHelpControl(t *testing.T, model *Model) (pointer.ControlID, pointer.Point) {
	t.Helper()
	if model.current == nil || !model.helpOpen || !model.current.bases.haveDimmed {
		t.Fatal("help overlay has no retained dimmed base")
	}
	surface := model.keyboardHelpSurface(model.current.bases.dimmed.content)
	for y := 0; y < model.height; y++ {
		for x := 0; x < model.width; x++ {
			command := surface.Pointer(tea.MouseMotionMsg{X: x, Y: y, Button: tea.MouseNone})
			if command == nil {
				continue
			}
			message := command()
			interaction, ok := pointer.ObserveInteraction(message)
			if !ok || interaction.Kind != pointer.InteractionHover || interaction.ID == "" {
				continue
			}
			var commands modelUpdateCommands
			*model, commands = model.applyResolvedPointer(message, true)
			if commands.followUp != nil || commands.geometry != nil {
				t.Fatal("help hover returned work")
			}
			return interaction.ID, interaction.Point
		}
	}
	t.Fatal("help overlay exposed no hoverable control")
	return "", pointer.Point{}
}

func assertFreshPointerRoute(t *testing.T, model *Model, previous tea.View) {
	t.Helper()
	if previous.OnMouse == nil || model.View().OnMouse == nil {
		t.Fatal("overlay publications did not install pointer handlers")
	}
	raw := tea.MouseMotionMsg{X: 0, Y: 0, Button: tea.MouseNone}
	if command := previous.OnMouse(raw); command != nil {
		t.Fatal("previous snapshot resolver escaped its mailbox")
	}
	_, previousRoute, result := model.pointerMailbox.take(raw)
	if result != pointerMailboxMatched {
		t.Fatalf("previous route result = %v", result)
	}
	if command := model.View().OnMouse(raw); command != nil {
		t.Fatal("current snapshot resolver escaped its mailbox")
	}
	_, currentRoute, result := model.pointerMailbox.take(raw)
	if result != pointerMailboxMatched || currentRoute.snapshot == previousRoute.snapshot ||
		currentRoute.snapshot != model.RenderPlanStats().InstalledSnapshotID {
		t.Fatalf("pointer routes current=%+v previous=%+v result=%v", currentRoute, previousRoute, result)
	}
}
