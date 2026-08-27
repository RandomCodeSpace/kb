package tui

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/board"
)

func anchorFixture() board.Board {
	return board.Board{Title: "Anchors", Tasks: []board.Task{
		{ID: "todo-1", Title: "Todo one", Status: board.StatusTodo},
		{ID: "todo-2", Title: "Todo two", Status: board.StatusTodo},
		{ID: "review-1", Title: "Review one", Status: board.StatusDoing},
		{ID: "review-2", Title: "Review two", Status: board.StatusDoing},
		{ID: "review-3", Title: "Review three", Status: board.StatusDoing},
		{ID: "done-1", Title: "Done one", Status: board.StatusDone},
		{ID: "done-2", Title: "Done two", Status: board.StatusDone},
		{ID: "done-3", Title: "Done three", Status: board.StatusDone},
	}}
}

func TestHorizontalNavigationRestoresEachColumnsTaskAnchor(t *testing.T) {
	current := anchorFixture()
	var state boardViewState
	if !state.focusTask(current, "done-2") {
		t.Fatal("failed to focus Done's second task")
	}
	state.focusColumn(board.StatusDoing, current)
	for range 10 {
		state.handleKey("down", current)
	}
	if selected, ok := state.selectedTask(current); !ok || selected.ID != "review-3" {
		t.Fatalf("Review bottom selection = %+v,%v", selected, ok)
	}
	state.handleKey("right", current)
	if selected, ok := state.selectedTask(current); !ok || selected.ID != "done-2" {
		t.Fatalf("return to Done = %+v,%v, want retained second task", selected, ok)
	}
}

func TestInactiveColumnCursorSurvivesInsertionBeforeItsTask(t *testing.T) {
	previous := anchorFixture()
	var state boardViewState
	if !state.focusTask(previous, "done-2") {
		t.Fatal("failed to focus Done's second task")
	}
	state.handleKey("left", previous)
	state.handleKey("down", previous)

	current := cloneBoard(previous)
	current.Tasks = slices.Insert(current.Tasks, 5, board.Task{ID: "done-0", Title: "Done zero", Status: board.StatusDone})
	state.adoptBoard(previous, current)
	state.handleKey("right", current)

	selected, ok := state.selectedTask(current)
	done := statusIndex(board.StatusDone)
	if !ok || selected.ID != "done-2" || state.rows[done] != 2 || state.cursors[done].TaskID != "done-2" {
		t.Fatalf("Done cursor after inactive insertion = %+v,%v state=%+v", selected, ok, state)
	}
}

func TestBoardAdoptionRetainsIdentityThenUsesNearestSurvivor(t *testing.T) {
	previous := anchorFixture()
	var state boardViewState
	state.focusTask(previous, "done-2")

	inserted := previous
	inserted.Tasks = append([]board.Task{{ID: "done-0", Title: "Done zero", Status: board.StatusDone}}, previous.Tasks...)
	state.adoptBoard(previous, inserted)
	if selected, ok := state.selectedTask(inserted); !ok || selected.ID != "done-2" {
		t.Fatalf("selection after insertion = %+v,%v", selected, ok)
	}

	removed := inserted
	removed.Tasks = make([]board.Task, 0, len(inserted.Tasks)-1)
	for _, task := range inserted.Tasks {
		if task.ID != "done-2" {
			removed.Tasks = append(removed.Tasks, task)
		}
	}
	state.adoptBoard(inserted, removed)
	if selected, ok := state.selectedTask(removed); !ok || selected.ID != "done-3" {
		t.Fatalf("nearest survivor = %+v,%v, want deterministic ordinal successor", selected, ok)
	}
}

func TestBoardAdoptionFollowsSelectedTaskMoveAndRepairsSourceColumn(t *testing.T) {
	previous := anchorFixture()
	var state boardViewState
	state.focusTask(previous, "todo-2")
	current := cloneBoard(previous)
	for i := range current.Tasks {
		if current.Tasks[i].ID == "todo-2" {
			current.Tasks[i].Status = board.StatusDone
		}
	}
	state.adoptBoard(previous, current)
	if selected, ok := state.selectedTask(current); !ok || selected.ID != "todo-2" || state.column != statusIndex(board.StatusDone) {
		t.Fatalf("moved selection = %+v,%v column=%d", selected, ok, state.column)
	}
	state.handleKey("left", current)
	state.handleKey("left", current)
	if selected, ok := state.selectedTask(current); !ok || selected.ID != "todo-1" {
		t.Fatalf("source fallback = %+v,%v", selected, ok)
	}
}

func TestTaskRowScrollAnchorRoundTripsAndClampsIntraRow(t *testing.T) {
	owners := []string{"a", "a", "", "b", "b", "b"}
	anchor := scrollAnchorAt(owners, 2)
	if anchor.TaskID != "a" || anchor.IntraRow != 2 {
		t.Fatalf("gap anchor = %+v", anchor)
	}
	if got := scrollOffsetForAnchor(owners, anchor, len(owners)-1); got != 2 {
		t.Fatalf("round-trip offset = %d, want 2", got)
	}
	shorter := []string{"a", "", "b", "b"}
	if got := scrollOffsetForAnchor(shorter, anchor, len(shorter)-1); got != 1 {
		t.Fatalf("clamped intra-row offset = %d, want 1", got)
	}
}

func TestDeletedTaskScrollAnchorResolvesOrdinalThroughCurrentGeometry(t *testing.T) {
	tests := []struct {
		name    string
		before  []string
		offset  int
		current []string
		want    int
	}{
		{
			name:    "successor after middle task is deleted",
			before:  []string{"before", "before", "", "deleted", "deleted", "deleted", "deleted", "", "after", "after", "after"},
			offset:  6,
			current: []string{"before", "before", "", "after", "after", "after"},
			want:    5,
		},
		{
			name:    "predecessor before final task is deleted",
			before:  []string{"before", "before", "", "deleted", "deleted", "deleted"},
			offset:  5,
			current: []string{"before", "before", ""},
			want:    2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			anchor := scrollAnchorAt(tt.before, tt.offset)
			if got := scrollOffsetForAnchor(tt.current, anchor, len(tt.current)-1); got != tt.want {
				t.Fatalf("deleted anchor %+v offset = %d, want %d", anchor, got, tt.want)
			}
		})
	}
}

func TestDeletedTaskScrollAnchorInEmptyColumnReturnsTop(t *testing.T) {
	anchor := scrollAnchorAt([]string{"deleted", "deleted", ""}, 1)
	if got := scrollOffsetForAnchor(nil, anchor, 0); got != 0 {
		t.Fatalf("empty-column offset = %d, want 0", got)
	}
}

func TestDeletedScrollAnchorRetainsIntraRowOnNearestSurvivor(t *testing.T) {
	tests := []struct {
		name      string
		deletedID string
		hint      int
		intra     int
		wantID    string
		owners    []string
		want      int
	}{
		{
			name:      "successor after middle task is deleted",
			deletedID: "done-2",
			hint:      1,
			intra:     3,
			wantID:    "done-3",
			owners:    []string{"done-1", "done-1", "", "done-3", "done-3"},
			want:      4,
		},
		{
			name:      "predecessor before final task is deleted",
			deletedID: "done-3",
			hint:      2,
			intra:     2,
			wantID:    "done-2",
			owners:    []string{"done-1", "done-1", "", "done-2", "done-2", "done-2", "done-2"},
			want:      5,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			current := anchorFixture()
			current.Tasks = slices.DeleteFunc(current.Tasks, func(task board.Task) bool {
				return task.ID == tt.deletedID
			})
			var state boardViewState
			done := statusIndex(board.StatusDone)
			state.scrollAnchors[done] = boardTaskAnchor{TaskID: tt.deletedID, IndexHint: tt.hint, IntraRow: tt.intra}
			state.restoreColumnScroll(current, done)
			anchor := state.scrollAnchors[done]
			if anchor.TaskID != tt.wantID || anchor.IntraRow != tt.intra {
				t.Fatalf("restored anchor = %+v, want task %q intra-row %d", anchor, tt.wantID, tt.intra)
			}
			if got := scrollOffsetForAnchor(tt.owners, anchor, len(tt.owners)-1); got != tt.want {
				t.Fatalf("restored anchor %+v offset = %d, want %d", anchor, got, tt.want)
			}
		})
	}
}

func TestBoardAdoptionPreservesSurvivingManualScrollAnchor(t *testing.T) {
	previous := anchorFixture()
	var state boardViewState
	state.focusTask(previous, "done-2")
	done := statusIndex(board.StatusDone)
	state.manualScroll[done] = true
	state.scrollAnchors[done] = boardTaskAnchor{TaskID: "done-1", IndexHint: 0, IntraRow: 4}

	current := cloneBoard(previous)
	current.Tasks = append([]board.Task{{ID: "todo-0", Title: "Inserted", Status: board.StatusTodo}}, current.Tasks...)
	state.adoptBoard(previous, current)
	if !state.manualScroll[done] || state.scrollAnchors[done].TaskID != "done-1" || state.scrollAnchors[done].IntraRow != 4 {
		t.Fatalf("surviving scroll anchor = manual:%v anchor:%+v", state.manualScroll[done], state.scrollAnchors[done])
	}
}

func TestDeletedTaskStaleScrollAnchorFallsForwardThroughCurrentGeometry(t *testing.T) {
	model := performanceModel(120, "", 80, 24)
	columnIndex := statusIndexExact(board.StatusTodo)
	column := model.current.geometry.columns[columnIndex]
	const deletedOrdinal = 10
	if column.index.length <= deletedOrdinal+1 {
		t.Fatalf("fixture has %d todo cards, want more than %d", column.index.length, deletedOrdinal+1)
	}
	offset := column.index.prefixAt(deletedOrdinal)
	anchor := column.anchorAt(offset, model.currentProjection())
	if anchor.IndexHint != deletedOrdinal || anchor.IntraRow != 0 {
		t.Fatalf("stale anchor = %+v, want ordinal %d at card start", anchor, deletedOrdinal)
	}
	deletedTitle := taskTitleByID(t, model.board, anchor.TaskID)

	refreshed := cloneBoard(model.board)
	refreshed.Tasks = slices.DeleteFunc(refreshed.Tasks, func(task board.Task) bool {
		return task.ID == anchor.TaskID
	})
	model, commands := model.updateWithCommands(boardLoadedMsg{board: refreshed})
	settlePerformanceGeometryCommand(t, &model, commands.geometry)

	var successor board.Task
	ordinal := 0
	for _, task := range refreshed.Tasks {
		if task.Status != board.StatusTodo {
			continue
		}
		if ordinal == deletedOrdinal {
			successor = task
			break
		}
		ordinal++
	}
	if successor.ID == "" {
		t.Fatal("refresh fixture has no ordinal successor")
	}

	// A delayed wheel intent can still carry the task identity from the frame
	// that existed before the refresh. Update must resolve its ordinal hint
	// against the current projection rather than resurrecting the deleted ID.
	model, commands = model.updateWithCommands(boardColumnScrolledMsg{
		status: board.StatusTodo,
		from:   model.boardView.scrolls[columnIndex],
		offset: offset,
		anchor: anchor,
		max:    max(column.totalRows()-column.contentHeight, 0),
	})
	settlePerformanceGeometryCommand(t, &model, commands.geometry)
	if got := model.boardView.scrollAnchors[columnIndex]; got != anchor {
		t.Fatalf("stale scroll anchor = %+v, want authoritative %+v", got, anchor)
	}
	assertPerformanceColdOracleParity(t, model)

	text := ansi.Strip(model.View().Content)
	if !strings.Contains(text, successor.Title) {
		t.Fatalf("fallback frame does not show ordinal successor %q:\n%s", successor.Title, text)
	}
	if strings.Contains(text, deletedTitle) {
		t.Fatalf("fallback frame still shows deleted task %q:\n%s", deletedTitle, text)
	}
	pressRenderedTask(t, &model, successor)
}

func TestUnanchoredAbsoluteScrollClampsToBottomClickTopology(t *testing.T) {
	model := performanceModel(120, "", 80, 24)
	columnIndex := statusIndexExact(board.StatusTodo)
	column := model.current.geometry.columns[columnIndex]
	maximum := max(column.totalRows()-column.contentHeight, 0)
	if maximum == 0 {
		t.Fatal("absolute-scroll fixture has no overflow")
	}
	var first, last board.Task
	for _, task := range model.board.Tasks {
		if task.Status != board.StatusTodo {
			continue
		}
		if first.ID == "" {
			first = task
		}
		last = task
	}

	model, commands := model.updateWithCommands(boardColumnScrolledMsg{
		status: board.StatusTodo,
		from:   0,
		offset: maximum + 1000,
		max:    maximum,
	})
	settlePerformanceGeometryCommand(t, &model, commands.geometry)
	assertPerformanceColdOracleParity(t, model)
	text := ansi.Strip(model.View().Content)
	if !strings.Contains(text, last.Title) {
		t.Fatalf("clamped bottom frame does not show final todo card %q:\n%s", last.Title, text)
	}
	if strings.Contains(text, first.Title) {
		t.Fatalf("clamped bottom frame still shows first todo card %q:\n%s", first.Title, text)
	}
	pressRenderedTask(t, &model, last)
}

func taskTitleByID(t *testing.T, current board.Board, taskID string) string {
	t.Helper()
	for _, task := range current.Tasks {
		if task.ID == taskID {
			return task.Title
		}
	}
	t.Fatalf("board has no task %q", taskID)
	return ""
}

// pressRenderedTask derives the coordinate from the immutable frame the user
// can see, dispatches through that frame's published pointer handler, and then
// verifies the board selected the exact task identity behind those cells.
func pressRenderedTask(t *testing.T, model *Model, task board.Task) {
	t.Helper()
	view := model.View()
	for y, line := range strings.Split(ansi.Strip(view.Content), "\n") {
		index := strings.Index(line, task.Title)
		if index < 0 {
			continue
		}
		x := ansi.StringWidth(line[:index])
		updateRootPointerTest(t, model, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
		selected, ok := model.selectedTask()
		if !ok || selected.ID != task.ID {
			t.Fatalf("press on rendered task %q selected %+v, %t", task.ID, selected, ok)
		}
		return
	}
	t.Fatalf("published frame has no visible title for task %q:\n%s", task.ID, ansi.Strip(view.Content))
}
