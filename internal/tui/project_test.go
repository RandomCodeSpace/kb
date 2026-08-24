package tui

import (
	"bytes"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// projectFixture is a board sliced by two projects, plus one card a foreign
// writer left with no project at all.
func projectFixture() board.Board {
	return board.Board{Title: "Projects", Tasks: []board.Task{
		{ID: "kb1", Title: "Ship switcher", Status: board.StatusTodo, Tags: []string{"tui", "project::kb"}},
		{ID: "kb2", Title: "Ship filter", Status: board.StatusDoing, Tags: []string{"project::kb"}},
		{ID: "web1", Title: "Fix landing", Status: board.StatusTodo, Tags: []string{"ui", "project::web"}},
		{ID: "loose", Title: "Unprojected", Status: board.StatusTodo, Tags: []string{"bug"}},
	}}
}

func projectModel() Model {
	m := NewModel(stubBoardReader{board: projectFixture()}, nil, "u")
	m.loading = false
	m.board = projectFixture()
	return m
}

// TestProjectScopeSelectsOneProjectOrAll pins the read side of the mandatory
// label: a named project shows only its own cards, and a card carrying none is
// reachable only under "all".
func TestProjectScopeSelectsOneProjectOrAll(t *testing.T) {
	m := projectModel()
	for _, test := range []struct {
		name     string
		switcher projectSwitcher
		want     []string
	}{
		{"all", projectSwitcher{all: true}, []string{"kb1", "kb2", "web1", "loose"}},
		{"unchosen is unscoped", projectSwitcher{}, []string{"kb1", "kb2", "web1", "loose"}},
		{"one project", projectSwitcher{name: "kb"}, []string{"kb1", "kb2"}},
		{"other project", projectSwitcher{name: "web"}, []string{"web1"}},
		{"unknown project", projectSwitcher{name: "ghost"}, []string{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			m.projects = test.switcher
			got := make([]string, 0, len(m.projectBoard().Tasks))
			for _, task := range m.projectBoard().Tasks {
				got = append(got, task.ID)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("scoped ids = %v, want %v", got, test.want)
			}
		})
	}
}

// TestProjectScopeRunsBeforeTheTextFilter is the composition rule: the filter
// narrows what the project scope left, and the toolbar count says so.
func TestProjectScopeRunsBeforeTheTextFilter(t *testing.T) {
	m := projectModel()
	m.projects = projectSwitcher{name: "kb"}
	m.filter.restore(boardFilter{Text: "ship"})
	ids := make([]string, 0, 2)
	for _, task := range m.filteredBoard().Tasks {
		ids = append(ids, task.ID)
	}
	if !reflect.DeepEqual(ids, []string{"kb1", "kb2"}) {
		t.Fatalf("filtered ids = %v", ids)
	}
	m.filter.restore(boardFilter{Text: "landing"})
	if got := len(m.filteredBoard().Tasks); got != 0 {
		t.Fatalf("a card outside the project survived the filter: %d", got)
	}
	// The denominator is the project, not the board: "0 of 2" beats "0 of 4"
	// when the board is scoped.
	line, _ := m.renderFilterBar(160)
	if !strings.Contains(ansi.Strip(line), "0 of 2 cards") {
		t.Fatalf("filter count = %q", ansi.Strip(line))
	}
}

// TestProjectLabelsLeaveTheFilterChips keeps the two axes from contradicting
// each other: the switcher owns projects, the chips own everything else.
func TestProjectLabelsLeaveTheFilterChips(t *testing.T) {
	m := projectModel()
	for _, label := range m.filterLabels() {
		if strings.HasPrefix(label, "project::") {
			t.Fatalf("filter labels offered a project chip: %v", m.filterLabels())
		}
	}
	m.projects = projectSwitcher{name: "kb"}
	if got := m.filterLabels(); !reflect.DeepEqual(got, []string{"tui"}) {
		t.Fatalf("scoped filter labels = %v, want only the scoped board's labels", got)
	}
}

func TestProjectSwitcherRestoreOrder(t *testing.T) {
	for _, test := range []struct {
		name   string
		stored projectSwitcher
		active string
		want   projectSwitcher
	}{
		{"stored project wins", projectSwitcher{name: "web"}, "kb", projectSwitcher{name: "web"}},
		{"stored all wins", projectSwitcher{all: true}, "kb", projectSwitcher{all: true}},
		{"nothing stored falls back to active", projectSwitcher{}, "kb", projectSwitcher{name: "kb"}},
		{"nothing at all is all", projectSwitcher{}, "", projectSwitcher{all: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var switcher projectSwitcher
			switcher.restore(test.stored, test.active)
			if switcher != test.want {
				t.Fatalf("restore = %+v, want %+v", switcher, test.want)
			}
		})
	}
}

// TestProjectCycleWalksAllAndEveryProject pins p/P: forward from "all" through
// the board's projects and back, backward the other way round.
func TestProjectCycleWalksAllAndEveryProject(t *testing.T) {
	m := projectModel()
	m.projects = projectSwitcher{all: true}
	labels := []string{}
	for range 4 {
		updateTestModel(t, &m, tea.KeyPressMsg{Code: 'p'})
		labels = append(labels, m.projects.label())
	}
	if !reflect.DeepEqual(labels, []string{"kb", "web", "all", "kb"}) {
		t.Fatalf("forward cycle = %v", labels)
	}
	m.projects = projectSwitcher{all: true}
	labels = labels[:0]
	for range 3 {
		updateTestModel(t, &m, tea.KeyPressMsg{Code: 'P'})
		labels = append(labels, m.projects.label())
	}
	if !reflect.DeepEqual(labels, []string{"web", "kb", "all"}) {
		t.Fatalf("backward cycle = %v", labels)
	}
}

// TestProjectCycleKeepsTheSelectedCardAndPersists covers the two side effects
// of a switch: the selection follows the card while the new scope still holds
// it, and the scope is written to the per-board preferences.
func TestProjectCycleKeepsTheSelectedCardAndPersists(t *testing.T) {
	var saved []tuiPreferences
	m := projectModel()
	m.savePreferences = func(preferences tuiPreferences) error {
		saved = append(saved, preferences)
		return nil
	}
	m.projects = projectSwitcher{all: true}
	m.boardView.focusTask(m.filteredBoard(), "web1")
	command := updateTestModel(t, &m, tea.KeyPressMsg{Code: 'p'})
	if command == nil {
		t.Fatal("switching project queued no preference write")
	}
	updateTestModel(t, &m, command())
	if m.projects.label() != "kb" {
		t.Fatalf("switcher = %q", m.projects.label())
	}
	selected, ok := m.selectedTask()
	if !ok || selected.ID != "kb1" {
		t.Fatalf("selection after a scope change = %+v ok:%v", selected, ok)
	}
	if len(saved) != 1 || saved[0].Project != "kb" || saved[0].ProjectAll {
		t.Fatalf("persisted preferences = %+v", saved)
	}
	updateTestModel(t, &m, tea.KeyPressMsg{Code: 'p'})
	updateTestModel(t, &m, tea.KeyPressMsg{Code: 'p'})
	if !m.projects.all || m.projects.name != "" {
		t.Fatalf("switcher did not return to all: %+v", m.projects)
	}
}

// TestProjectSwitcherSurvivesAPreferenceRoundTrip is the cancelled toggle's
// contract applied to the scope: what the board wrote is what it reopens with.
func TestProjectSwitcherSurvivesAPreferenceRoundTrip(t *testing.T) {
	m := projectModel()
	m.SetActiveProject("web")
	if m.projects.label() != "web" {
		t.Fatalf("active project did not open the board: %+v", m.projects)
	}
	m.projects = projectSwitcher{name: "kb"}
	preferences := m.preferences()

	reopened := projectModel()
	reopened.activeProject = "web"
	reopened.adoptPreferences(preferences)
	if reopened.projects.label() != "kb" {
		t.Fatalf("stored scope lost to the active project: %+v", reopened.projects)
	}

	unscoped := projectModel()
	unscoped.activeProject = "web"
	unscoped.adoptPreferences(tuiPreferences{ProjectAll: true})
	if !unscoped.projects.all {
		t.Fatalf("stored all lost to the active project: %+v", unscoped.projects)
	}
}

// TestProjectSwitcherRendersAndClicks covers the toolbar affordance: the
// segment always says what the board is scoped to, and clicking it cycles.
func TestProjectSwitcherRendersAndClicks(t *testing.T) {
	m := projectModel()
	m.projects = projectSwitcher{all: true}
	line, hits := m.renderFilterBar(160)
	if !strings.Contains(ansi.Strip(line), "[project: all]") {
		t.Fatalf("toolbar without a project segment: %q", ansi.Strip(line))
	}
	var segment *boardHit
	for index, hit := range hits {
		if hit.kind == boardHitProject {
			segment = &hits[index]
		}
	}
	if segment == nil {
		t.Fatal("project segment has no click region")
	}
	if id := boardHitControlID(*segment); id != "board-filter:project" {
		t.Fatalf("project control id = %q", id)
	}
	if got := boardControlMessage(*segment); got != (filterProjectClickedMsg{}) {
		t.Fatalf("project control message = %#v", got)
	}
	updateTestModel(t, &m, filterProjectClickedMsg{})
	if m.projects.label() != "kb" {
		t.Fatalf("click did not cycle the switcher: %+v", m.projects)
	}
	scoped, _ := m.renderFilterBar(160)
	if !strings.Contains(ansi.Strip(scoped), "[project: kb]") {
		t.Fatalf("scoped toolbar = %q", ansi.Strip(scoped))
	}
}

// TestProjectSwitcherIgnoresAnEmptyBoard: with no projects anywhere there is
// nothing to cycle to, so no scope change and no preference write.
func TestProjectSwitcherIgnoresAnEmptyBoard(t *testing.T) {
	m := NewModel(stubBoardReader{}, nil, "u")
	m.projects = projectSwitcher{}
	if command := m.cycleProject(1); command != nil {
		t.Fatalf("cycling an unprojected board queued %v", command)
	}
	if m.projects.chosen() {
		t.Fatalf("cycling invented a selection: %+v", m.projects)
	}
	m.projects = projectSwitcher{all: true}
	if command := m.cycleProject(1); command != nil || !m.projects.all {
		t.Fatalf("cycling from all with nothing to cycle to = %v, %+v", command, m.projects)
	}
}

// TestProjectClickIsInertBehindAnOverlayOrALiveMove mirrors the other toolbar
// controls: a scope change is user input, so it waits behind settings, is
// dropped while a move is being written, and cancels a lifted card.
func TestProjectClickIsInertBehindAnOverlayOrALiveMove(t *testing.T) {
	m := projectModel()
	m.projects = projectSwitcher{all: true}
	m.settings = &settingsModel{}
	if command := updateTestModel(t, &m, filterProjectClickedMsg{}); command != nil || !m.projects.all {
		t.Fatalf("project click leaked behind settings: %v %+v", command, m.projects)
	}
	m.settings = nil

	m.move.saving = true
	if command := updateTestModel(t, &m, filterProjectClickedMsg{}); command != nil || !m.projects.all {
		t.Fatalf("project click ran during a move write: %v %+v", command, m.projects)
	}
	m.move.saving = false

	task, ok := m.selectedTask()
	if !ok {
		t.Fatal("fixture has no selectable card")
	}
	m.move.beginVisible(m.board, m.filteredBoard(), task, m.boardView.visibleStatuses(), false)
	updateTestModel(t, &m, filterProjectClickedMsg{})
	if m.move.lifted != nil || m.projects.label() != "kb" {
		t.Fatalf("project click did not cancel the lift: lifted:%v scope:%q", m.move.lifted != nil, m.projects.label())
	}
}

// TestProjectSegmentClickRoutesThroughTheBoardMouseHandler covers the pointer
// path the toolbar segment shares with the filter chips.
func TestProjectSegmentClickRoutesThroughTheBoardMouseHandler(t *testing.T) {
	m := projectModel()
	m.width, m.height = 120, 40
	_, hits := m.renderBoard()
	var segment boardHit
	for _, hit := range hits {
		if hit.kind == boardHitProject {
			segment = hit
			break
		}
	}
	if segment.x1 <= segment.x0 {
		t.Fatalf("project segment hit missing: %+v", hits)
	}
	command := boardMouseHandler(hits)(tea.MouseClickMsg{X: segment.x0, Y: segment.y0, Button: tea.MouseLeft})
	if command == nil {
		t.Fatal("project segment click was not hit")
	}
	if got := command(); got != (filterProjectClickedMsg{}) {
		t.Fatalf("project segment click message = %#v", got)
	}
}

// TestProjectSwitcherReachesTheActiveProjectWithNoCards keeps a brand-new
// project cyclable before it has a single card.
func TestProjectSwitcherReachesTheActiveProjectWithNoCards(t *testing.T) {
	m := projectModel()
	m.SetActiveProject("fresh")
	if got := m.boardProjects(); !reflect.DeepEqual(got, []string{"fresh", "kb", "web"}) {
		t.Fatalf("board projects = %v", got)
	}
	if m.projects.label() != "fresh" || len(m.filteredBoard().Tasks) != 0 {
		t.Fatalf("empty active project = %+v with %d cards", m.projects, len(m.filteredBoard().Tasks))
	}
}

// TestProjectPillLeadsTheLabelRow is spec section 3.5 applied to the mandatory
// label: the project is the pill that survives a narrow card.
func TestProjectPillLeadsTheLabelRow(t *testing.T) {
	m := projectModel()
	m.width, m.height = 120, 40
	view := ansi.Strip(m.render())
	kbLine := ""
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "tui") && strings.Contains(line, "kb") {
			kbLine = line
			break
		}
	}
	if kbLine == "" {
		t.Fatalf("no label row on the board:\n%s", view)
	}
	if strings.Index(kbLine, "project:kb") > strings.Index(kbLine, "tui") {
		t.Fatalf("project pill does not lead the label row: %q", kbLine)
	}
}

// TestProjectScopedBoardGolden captures the whole surface at once: the toolbar
// segment naming the scope, only that project's cards on the board, and the
// project pill leading each label row.
func TestProjectScopedBoardGolden(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	m := projectModel()
	m.SetActiveProject("kb")
	m.now = func() time.Time { return now }
	m.renderedAt = now
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = sized.(Model)
	tm := teatest.NewTestModel(t, m,
		teatest.WithInitialTermSize(120, 40),
		teatest.WithProgramOptions(theme.PinColor()),
	)
	t.Cleanup(func() { _ = tm.Quit() })
	var captured bytes.Buffer
	teatest.WaitFor(t, io.TeeReader(tm.Output(), &captured), func(output []byte) bool {
		return bytes.Contains(output, []byte("project: kb"))
	}, teatest.WithDuration(5*time.Second), teatest.WithCheckInterval(10*time.Millisecond))
	frame, ok := finalFullScreenFrame(captured.Bytes())
	if !ok {
		t.Fatal("teatest output did not contain a full-screen frame")
	}
	grid, err := renderedCellGrid(frame, 120, 40)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(grid, []byte("Fix landing")) || bytes.Contains(grid, []byte("Unprojected")) {
		t.Fatalf("a card outside the project reached the scoped board:\n%s", grid)
	}
	teatest.RequireEqualOutput(t, grid)
	tm.Send(tea.KeyPressMsg{Code: 'q'})
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))
}

// TestProjectDefaultFollowsTheSwitcher is the editor handoff: a card created
// while the board is scoped belongs to that project, and under "all" it falls
// back to the active one.
func TestProjectDefaultFollowsTheSwitcher(t *testing.T) {
	m := projectModel()
	m.SetActiveProject("web")
	if got := m.projectDefault(); got != "web" {
		t.Fatalf("default from the active project = %q", got)
	}
	m.projects = projectSwitcher{name: "kb"}
	if got := m.projectDefault(); got != "kb" {
		t.Fatalf("default from the switcher = %q", got)
	}
	m.projects = projectSwitcher{all: true}
	if got := m.projectDefault(); got != "web" {
		t.Fatalf("default under all = %q", got)
	}
	m.activeProject = ""
	if got := m.projectDefault(); got != "" {
		t.Fatalf("default with nothing resolved = %q", got)
	}
}
