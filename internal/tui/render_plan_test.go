package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/board"
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
