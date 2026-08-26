package tui

import (
	"reflect"
	"strings"
	"testing"

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
