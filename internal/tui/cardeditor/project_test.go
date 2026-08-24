package cardeditor

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/ai"
	"github.com/RandomCodeSpace/kb/internal/board"
)

// TestCreateRefusesWithoutAProject is the mandatory half of the invariant: a
// card with no project never reaches the store, and the refusal names the
// field the user has to fill.
func TestCreateRefusesWithoutAProject(t *testing.T) {
	backend := newTestStore(t)
	model := New(backend, "u")
	model.OpenAdd(board.StatusTodo)
	model.title.SetValue("Unprojected card")
	if command := model.startSave(); command != nil || model.saving || !model.IsOpen() {
		t.Fatalf("save without a project = command:%v saving:%v open:%v", command, model.saving, model.IsOpen())
	}
	if !strings.Contains(model.statusMessage, "exactly one project") || !model.statusIsError {
		t.Fatalf("refusal = %q (error:%v)", model.statusMessage, model.statusIsError)
	}
	if got, _ := backend.Board("u"); len(got.Tasks) != 0 {
		t.Fatalf("refused card reached the store: %+v", got.Tasks)
	}
	// An unusable name is refused the same way, by the same rule the CLI uses.
	model.project.SetValue("two words")
	if command := model.startSave(); command != nil || !strings.Contains(model.statusMessage, "whitespace") {
		t.Fatalf("invalid project name = command:%v status:%q", command, model.statusMessage)
	}
	model.project.SetValue("kb")
	if command := model.startSave(); command == nil {
		t.Fatalf("named project did not save: %q", model.statusMessage)
	}
}

// TestCreateDefaultsToTheHandedProject covers the other half: the board hands
// the editor its scope, and a card created without touching the field lands
// there carrying exactly one project label.
func TestCreateDefaultsToTheHandedProject(t *testing.T) {
	backend := newTestStore(t)
	model := New(backend, "u")
	model.SetProjectDefault("  atlas  ")
	model.OpenAdd(board.StatusTodo)
	if model.project.Value() != "atlas" {
		t.Fatalf("default project = %q", model.project.Value())
	}
	model.title.SetValue("Card")
	model.tags = []string{"tui"}
	model.Update(commandMsgForEditor(t, model.startSave()))
	got, err := backend.Board("u")
	if err != nil || len(got.Tasks) != 1 {
		t.Fatalf("board = %+v, %v", got, err)
	}
	if want := "tui,project::atlas"; strings.Join(got.Tasks[0].Tags, ",") != want {
		t.Fatalf("created tags = %v, want %q", got.Tasks[0].Tags, want)
	}
}

// TestEditKeepsTheCardsOwnProject: opening an existing card shows the project
// it is in, not the board's current scope, so editing a title cannot move a
// card between projects by accident.
func TestEditKeepsTheCardsOwnProject(t *testing.T) {
	backend := newTestStore(t)
	created, err := backend.Store.AddTask("u", board.Task{
		Title: "Existing", Status: board.StatusTodo, Prio: 3, Tags: []string{"tui", "project::web"},
	})
	if err != nil {
		t.Fatal(err)
	}
	model := New(backend, "u")
	model.SetProjectDefault("kb")
	model.OpenEdit(created)
	if model.project.Value() != "web" {
		t.Fatalf("edited project = %q, want the card's own", model.project.Value())
	}
	if strings.Join(model.tags, ",") != "tui" {
		t.Fatalf("label field carried the project label: %v", model.tags)
	}
	if model.Dirty() {
		t.Fatal("splitting the project out of the labels made a clean form dirty")
	}
	model.title.SetValue("Renamed")
	model.Update(commandMsgForEditor(t, model.startSave()))
	stored, _ := backend.Board("u")
	if want := "tui,project::web"; strings.Join(stored.Tasks[0].Tags, ",") != want {
		t.Fatalf("stored tags = %v, want %q", stored.Tasks[0].Tags, want)
	}
}

// TestEditMovesTheCardBetweenProjects pins the write: changing the field moves
// the card, is seen as a label change by the compare-and-set, and still leaves
// exactly one project label.
func TestEditMovesTheCardBetweenProjects(t *testing.T) {
	backend := newTestStore(t)
	created, err := backend.Store.AddTask("u", board.Task{
		Title: "Existing", Status: board.StatusTodo, Prio: 3, Tags: []string{"tui", "project::web"},
	})
	if err != nil {
		t.Fatal(err)
	}
	model := New(backend, "u")
	model.OpenEdit(created)
	model.project.SetValue("kb")
	if !model.Dirty() || !model.changedFields().tags {
		t.Fatalf("moving a card is not a label change: dirty:%v changed:%+v", model.Dirty(), model.changedFields())
	}
	model.Update(commandMsgForEditor(t, model.startSave()))
	stored, _ := backend.Board("u")
	if want := "tui,project::kb"; strings.Join(stored.Tasks[0].Tags, ",") != want {
		t.Fatalf("moved tags = %v, want %q", stored.Tasks[0].Tags, want)
	}
}

// TestTypedProjectLabelMovesTheCardInsteadOfStacking: a project:: label typed
// into the label field is the CLI's "--tag project::x counts as explicit"
// rule, and a card can still only carry one.
func TestTypedProjectLabelMovesTheCardInsteadOfStacking(t *testing.T) {
	model := New(newTestStore(t), "u")
	model.SetProjectDefault("kb")
	model.OpenAdd(board.StatusTodo)
	model.title.SetValue("Card")
	model.addLabels("tui project::web #area")
	if model.project.Value() != "web" {
		t.Fatalf("typed project label did not move the card: %q", model.project.Value())
	}
	if strings.Join(model.tags, ",") != "tui,area" {
		t.Fatalf("labels = %v, want the project label removed", model.tags)
	}
	task, _, err := model.buildSave()
	if err != nil {
		t.Fatal(err)
	}
	if want := "tui,area,project::web"; strings.Join(task.Tags, ",") != want {
		t.Fatalf("saved tags = %v, want %q", task.Tags, want)
	}
}

// TestDraftedProjectLabelLandsInTheField keeps the AI draft inside the
// invariant: whatever the model returns, the card carries one project.
func TestDraftedProjectLabelLandsInTheField(t *testing.T) {
	model := New(newTestStore(t), "u")
	model.SetProjectDefault("kb")
	model.OpenAdd(board.StatusTodo)
	model.applyDraft(ai.Draft{Title: "Drafted", Tags: []string{"project::web", "tui"}})
	if model.project.Value() != "web" || strings.Join(model.tags, ",") != "tui" {
		t.Fatalf("draft = project %q labels %v", model.project.Value(), model.tags)
	}
	model.applyDraft(ai.Draft{Title: "Drafted again", Tags: []string{"tui"}})
	if model.project.Value() != "web" {
		t.Fatalf("a draft without a project cleared the field: %q", model.project.Value())
	}
}

// TestProjectFieldIsReachableAndRendered covers the overlay surface: the row
// is focusable by pointer, says it is required, and shows the value.
func TestProjectFieldIsReachableAndRendered(t *testing.T) {
	model := New(newTestStore(t), "u")
	model.SetProjectDefault("kb")
	model.OpenAdd(board.StatusTodo)
	found := false
	for _, line := range model.bodyLines(60) {
		if strings.Contains(line, "Project: kb") && strings.Contains(line, "required") {
			found = true
		}
	}
	if !found {
		t.Fatalf("project row missing:\n%s", strings.Join(model.bodyLines(60), "\n"))
	}
	model.Update(pointerClickMsg{session: model.session, target: "project"})
	if model.focus != "project" || !model.project.Focused() {
		t.Fatalf("project row not focusable: focus=%q focused=%v", model.focus, model.project.Focused())
	}
	model.Update(tea.KeyPressMsg{Code: 'z', Text: "z"})
	if model.project.Value() != "kbz" {
		t.Fatalf("typing into the project field = %q", model.project.Value())
	}
}
