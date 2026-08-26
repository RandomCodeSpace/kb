package tui

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"unsafe"

	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/board"
)

func projectionFixture() board.Board {
	return board.Board{Title: "Projection", Tasks: []board.Task{
		{ID: "todo-alpha", Title: "Alpha API", Desc: "Unicode İ", Status: board.StatusTodo, Tags: []string{"project::alpha", "backend"}, Checks: []board.Check{{Text: "ship", Done: true}}},
		{ID: "doing-beta", Title: "Beta UI", Desc: "layout", Status: board.StatusDoing, Tags: []string{"project::beta", "frontend"}},
		{ID: "done-alpha", Title: "Done", Desc: "backend migration", Status: board.StatusDone, Tags: []string{"project::alpha", "database"}},
		{ID: "cancelled", Title: "Retired", Status: board.StatusCancelled, Tags: []string{"ops"}},
	}}
}

func TestRenderProjectionMatchesLegacyProjectAndFilterSemantics(t *testing.T) {
	tests := []struct {
		name       string
		project    projectSwitcher
		filterText string
		filterTags []string
	}{
		{name: "all"},
		{name: "project", project: projectSwitcher{name: "alpha"}},
		{name: "text", filterText: "BACKEND"},
		{name: "tag", filterTags: []string{"frontend"}},
		{name: "composed", project: projectSwitcher{name: "alpha"}, filterText: "migration", filterTags: []string{"database"}},
		{name: "empty", filterText: "absent"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := Model{board: projectionFixture(), filter: newBoardFilterState(), projects: test.project}
			model.filter.restore(boardFilter{Text: test.filterText, Tags: test.filterTags})
			want := model.filter.project(model.projectBoard())
			projection, _, _ := (renderProjection{}).rebuild(model)
			if !reflect.DeepEqual(projection.board, want) {
				t.Fatalf("projection = %#v, want legacy %#v", projection.board, want)
			}
			for status, indexes := range projection.statuses {
				for _, index := range indexes {
					if index < 0 || index >= len(projection.board.Tasks) || projection.board.Tasks[index].Status != boardStatuses[status] {
						t.Fatalf("status %q index %d points outside its exact column", boardStatuses[status], index)
					}
				}
			}
		})
	}
}

func TestRenderProjectionSnapshotsNestedTaskData(t *testing.T) {
	model := Model{board: projectionFixture(), filter: newBoardFilterState()}
	projection, _, _ := (renderProjection{}).rebuild(model)

	model.board.Tasks[0].Tags[0] = "project::mutated"
	model.board.Tasks[0].Checks[0].Text = "mutated"
	if got := projection.tasks[0].task.Tags[0]; got != "project::alpha" {
		t.Fatalf("published tag aliased mutable source: %q", got)
	}
	if got := projection.tasks[0].task.Checks[0].Text; got != "ship" {
		t.Fatalf("published check aliased mutable source: %q", got)
	}
	if projection.matchesModel(model) {
		t.Fatal("mutated source incorrectly matched the installed immutable projection")
	}
}

func TestSearchNormalizationDoesNotReuseDisplaySanitization(t *testing.T) {
	raw := "Alpha\x1b[31m"
	search := normalizeSearchValue(raw)
	display := normalizeSearchValue(sanitizeTerminal(raw))
	if search == display || !strings.Contains(search, "\x1b") || strings.Contains(display, "\x1b") {
		t.Fatalf("search=%q display=%q; search normalization was coupled to terminal sanitization", search, display)
	}
}

func TestRenderPlanRetainsOnlyTheCurrentProjection(t *testing.T) {
	model := Model{board: projectionFixture(), filter: newBoardFilterState()}
	projection, _, _ := (renderProjection{}).rebuild(model)
	model.filter.restore(boardFilter{Text: "beta"})
	projection, _, changed := projection.rebuild(model)
	if !changed || len(projection.board.Tasks) != 1 || projection.board.Tasks[0].ID != "doing-beta" {
		t.Fatalf("current projection = %#v, changed=%v", projection.board.Tasks, changed)
	}
	if len(projection.tasks) != len(model.board.Tasks) {
		t.Fatalf("task derivations = %d, want one current source snapshot of %d", len(projection.tasks), len(model.board.Tasks))
	}
}

func TestSameContentReloadRebindsProjectionSourceForOrdinaryInput(t *testing.T) {
	model := performanceModel(120, "", performanceWidth, performanceHeight)
	reloaded := cloneBoard(model.board)
	if unsafe.SliceData(reloaded.Tasks) == unsafe.SliceData(model.board.Tasks) {
		t.Fatal("test reload reused the source task backing array")
	}
	beforeReload := model.RenderPlanStats()
	updated, _ := model.Update(boardLoadedMsg{board: reloaded})
	model = updated.(Model)
	afterReload := model.RenderPlanStats()
	if afterReload.SourceTaskComparisons != beforeReload.SourceTaskComparisons+uint64(len(reloaded.Tasks)) {
		t.Fatalf("reload source comparisons=%d, want one proof over %d tasks",
			afterReload.SourceTaskComparisons-beforeReload.SourceTaskComparisons, len(reloaded.Tasks))
	}
	if !model.current.projection.matchesSourceIdentity(model.board) {
		t.Fatal("semantically identical reload did not rebind projection source identity")
	}
	beforeInput := model.RenderPlanStats()
	for index := 0; index < 8; index++ {
		code := tea.KeyDown
		if index%2 == 1 {
			code = tea.KeyUp
		}
		updated, _ = model.Update(tea.KeyPressMsg{Code: code})
		model = updated.(Model)
	}
	afterInput := model.RenderPlanStats()
	if afterInput.SourceTaskComparisons != beforeInput.SourceTaskComparisons {
		t.Fatalf("ordinary inputs rescanned %d tasks after source rebind",
			afterInput.SourceTaskComparisons-beforeInput.SourceTaskComparisons)
	}
}

func TestProjectionCachesSelectedInclusiveToolbarLabels(t *testing.T) {
	model := Model{board: projectionFixture(), filter: newBoardFilterState(), projects: projectSwitcher{all: true}}
	model.filter.restore(boardFilter{Tags: []string{"missing-but-selected"}})
	projection, _, _ := (renderProjection{}).rebuild(model)
	labels := projection.filterLabels()
	if !slices.Contains(labels, "missing-but-selected") {
		t.Fatalf("toolbar labels=%v, want selected label retained", labels)
	}
	first := unsafe.SliceData(labels)
	if allocations := testing.AllocsPerRun(1000, func() {
		if unsafe.SliceData(projection.filterLabels()) != first {
			t.Fatal("cached toolbar labels changed backing storage")
		}
	}); allocations != 0 {
		t.Fatalf("cached toolbar label reads allocated %v times", allocations)
	}
}

func TestFilterInputPerformsOneProjectionPassAndNoSourceScan(t *testing.T) {
	for _, count := range []int{120, 500, 1000} {
		t.Run(integerLabel(count), func(t *testing.T) {
			t.Run("typing", func(t *testing.T) {
				model := performanceModel(count, "", performanceWidth, performanceHeight)
				_ = model.filter.focusText()
				before := model.RenderPlanStats()
				updated, _ := model.Update(tea.KeyPressMsg{Code: 'k', Text: "keep17"})
				model = updated.(Model)
				assertSingleFilterProjectionPass(t, model, before, count)
				if got := len(model.filteredBoard().Tasks); got != 17 {
					t.Fatalf("typed projection contains %d tasks, want 17", got)
				}
			})

			t.Run("label-toggle", func(t *testing.T) {
				model := performanceModel(count, "", performanceWidth, performanceHeight)
				before := model.RenderPlanStats()
				updated, _ := model.Update(filterLabelClickedMsg{tag: "performance"})
				model = updated.(Model)
				assertSingleFilterProjectionPass(t, model, before, count)
				if got := len(model.filteredBoard().Tasks); got != count {
					t.Fatalf("label projection contains %d tasks, want %d", got, count)
				}
			})
		})
	}
}

func assertSingleFilterProjectionPass(t *testing.T, model Model, before RenderPlanStats, taskCount int) {
	t.Helper()
	after := model.RenderPlanStats()
	if delta := after.SourceTaskComparisons - before.SourceTaskComparisons; delta != 0 {
		t.Fatalf("filter input scanned %d source tasks", delta)
	}
	if delta := after.ProjectionBuilds - before.ProjectionBuilds; delta != 1 {
		t.Fatalf("filter input built %d projections, want 1", delta)
	}
	if delta := after.ProjectionTaskVisits - before.ProjectionTaskVisits; delta != uint64(taskCount) {
		t.Fatalf("filter input visited %d projection tasks, want %d", delta, taskCount)
	}
	if model.preparedProjection != nil {
		t.Fatal("consumed filter projection remained attached to the model")
	}
	if !model.current.projection.matchesProjectionKey(model) {
		t.Fatal("installed projection does not match the current filter key")
	}
}
