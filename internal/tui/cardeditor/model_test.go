package cardeditor

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
)

type faultStore struct {
	*store.Store
	addErr, updateErr, labelsErr, similarErr error
	queries                                  []similarQuery
}

type similarQuery struct {
	user, title, exclude string
	links                []string
	limit                int
}

func (s *faultStore) AddTask(user string, task board.Task) (board.Task, error) {
	if s.addErr != nil {
		return board.Task{}, s.addErr
	}
	return s.Store.AddTask(user, task)
}

func (s *faultStore) UpdateTask(user, id string, patch store.TaskPatch) (board.Task, error) {
	if s.updateErr != nil {
		return board.Task{}, s.updateErr
	}
	return s.Store.UpdateTask(user, id, patch)
}

func (s *faultStore) Labels(user string) ([]string, error) {
	if s.labelsErr != nil {
		return nil, s.labelsErr
	}
	return s.Store.Labels(user)
}

func (s *faultStore) SearchSimilar(user, title, exclude string, links []string, limit int) ([]store.SimilarHit, error) {
	s.queries = append(s.queries, similarQuery{user: user, title: title, exclude: exclude, links: append([]string(nil), links...), limit: limit})
	if s.similarErr != nil {
		return nil, s.similarErr
	}
	return s.Store.SearchSimilar(user, title, exclude, links, limit)
}

func newTestStore(t *testing.T) *faultStore {
	t.Helper()
	st, err := store.Open(t.TempDir()+"/kb.db", []byte("card-editor-test-key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return &faultStore{Store: st}
}

func run(t *testing.T, model *Model, command tea.Cmd) {
	t.Helper()
	if command == nil {
		t.Fatal("command is nil")
	}
	model.Update(command())
}

func press(code rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code} }

func TestCreatePersistsEveryFieldAndAcknowledgesClose(t *testing.T) {
	backend := newTestStore(t)
	model := New(backend, "alice")
	if !model.Enabled() || model.IsOpen() || model.TaskID() != "" || model.Dirty() {
		t.Fatalf("initial state = enabled:%v open:%v id:%q dirty:%v", model.Enabled(), model.IsOpen(), model.TaskID(), model.Dirty())
	}
	run(t, &model, model.OpenAdd(board.StatusDoing))
	model.title.SetValue("  Ship direct editor  ")
	model.emoji.SetValue("🧭")
	model.desc.SetValue("  first line\nsecond line  ")
	model.prio = 1
	model.due.SetValue("2026-08-31")
	model.effort = "M"
	model.blocked = true
	model.tags = []string{"tui", "type::feature"}
	model.checks.SetValue("write test\nx ship it\n\n")
	if !model.Dirty() {
		t.Fatal("populated create form is not dirty")
	}
	save := model.startSave()
	if !model.saving || save == nil {
		t.Fatalf("save state = saving:%v command:%v", model.saving, save)
	}
	model.Update(save())
	if model.IsOpen() || !model.ConsumeSaved() || model.ConsumeSaved() {
		t.Fatalf("acknowledged state = open:%v first/second saved consumption invalid", model.IsOpen())
	}
	got, err := backend.Board("alice")
	if err != nil || len(got.Tasks) != 1 {
		t.Fatalf("board = %+v, %v", got, err)
	}
	task := got.Tasks[0]
	if task.Title != "Ship direct editor" || task.Emoji != "🧭" || task.Desc != "first line\nsecond line" ||
		task.Status != board.StatusDoing || task.Prio != 1 || task.Due != "2026-08-31" || task.Effort != "M" ||
		!task.Blocked || strings.Join(task.Tags, ",") != "tui,type::feature" || len(task.Checks) != 2 ||
		task.Checks[0].Done || !task.Checks[1].Done || task.Checks[1].Text != "ship it" {
		t.Fatalf("created task = %+v", task)
	}
}

func TestEditClearSemanticsAndRefusedSavePreserveForm(t *testing.T) {
	backend := newTestStore(t)
	created, err := backend.Store.AddTask("alice", board.Task{
		Title: "Original", Status: board.StatusTodo, Prio: 2, Due: "2026-08-30", Effort: "L",
	})
	if err != nil {
		t.Fatal(err)
	}
	model := New(backend, "alice")
	model.OpenEdit(created)
	model.title.SetValue("Edited")
	model.due.SetValue("")
	model.effort = ""
	backend.updateErr = errors.New("database refused\x1b[31m\nretry")
	save := model.startSave()
	model.Update(save())
	if !model.IsOpen() || model.saving || model.title.Value() != "Edited" || model.due.Value() != "" || model.effort != "" {
		t.Fatalf("refusal destroyed state: %+v", model.currentSnapshot())
	}
	if !strings.Contains(model.statusMessage, "save refused") || strings.ContainsAny(model.statusMessage, "\x1b\n") {
		t.Fatalf("unsafe refusal = %q", model.statusMessage)
	}
	stored, _ := backend.Board("alice")
	if stored.Tasks[0].Title != "Original" || stored.Tasks[0].Due == "" || stored.Tasks[0].Effort == "" {
		t.Fatalf("refused write mutated store: %+v", stored.Tasks[0])
	}

	backend.updateErr = nil
	model.Update(model.startSave()())
	stored, _ = backend.Board("alice")
	if stored.Tasks[0].Title != "Edited" || stored.Tasks[0].Due != "" || stored.Tasks[0].Effort != "" {
		t.Fatalf("clears did not persist: %+v", stored.Tasks[0])
	}
}

func TestWireValidationStaysOpenForUnicodeAndDateErrors(t *testing.T) {
	model := New(newTestStore(t), "u")
	model.OpenAdd(board.StatusTodo)
	model.title.SetValue("Card")
	for _, test := range []struct {
		emoji, due, want string
	}{
		{emoji: "👨‍💻", want: "invalid emoji"},
		{emoji: "🇯🇵", want: "invalid emoji"},
		{emoji: "👍🏽", want: "invalid emoji"},
		{emoji: "🧭", due: "2026-02-30", want: "not a real date"},
	} {
		model.emoji.SetValue(test.emoji)
		model.due.SetValue(test.due)
		if command := model.startSave(); command != nil || !model.IsOpen() || model.saving || !strings.Contains(model.statusMessage, test.want) {
			t.Fatalf("validation %q/%q = command:%v open:%v saving:%v status:%q", test.emoji, test.due, command, model.IsOpen(), model.saving, model.statusMessage)
		}
	}
	model.emoji.SetValue("☀️")
	model.due.SetValue("")
	if command := model.startSave(); command == nil {
		t.Fatal("wire-valid emoji did not save")
	}
}

func TestUnsavedGuardAndWatcherRefreshNeverOverwriteDirtyFields(t *testing.T) {
	backend := newTestStore(t)
	task, _ := backend.Store.AddTask("u", board.Task{Title: "Original", Status: board.StatusTodo, Prio: 3})
	model := New(backend, "u")
	model.OpenEdit(task)
	cleanUpdate := task
	cleanUpdate.Title = "Fresh"
	model.Refresh(cleanUpdate, true)
	if model.title.Value() != "Fresh" || model.Dirty() || model.stale {
		t.Fatalf("clean refresh = title:%q dirty:%v stale:%v", model.title.Value(), model.Dirty(), model.stale)
	}
	model.title.SetValue("My edits")
	remote := cleanUpdate
	remote.Title = "Remote edits"
	model.Refresh(remote, true)
	if model.title.Value() != "My edits" || !model.stale || !model.statusIsError {
		t.Fatalf("dirty refresh = title:%q stale:%v status:%q", model.title.Value(), model.stale, model.statusMessage)
	}
	model.Refresh(board.Task{}, false)
	if !model.IsOpen() || !strings.Contains(model.statusMessage, "disappeared") {
		t.Fatalf("dirty deletion closed editor: open:%v status:%q", model.IsOpen(), model.statusMessage)
	}

	model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !model.guardClose || !model.IsOpen() {
		t.Fatal("first escape bypassed unsaved guard")
	}
	model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if model.guardClose || !model.IsOpen() {
		t.Fatal("guard escape did not return to editing")
	}
	model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	model.Update(press('d'))
	if model.IsOpen() {
		t.Fatal("explicit discard did not close")
	}

	model.OpenEdit(task)
	model.Refresh(board.Task{}, false)
	if model.IsOpen() {
		t.Fatal("clean remotely deleted editor stayed open")
	}
}

func TestSimilarDebounceExclusionsDismissalsAndKilledContext(t *testing.T) {
	backend := newTestStore(t)
	edited, _ := backend.Store.AddTask("u", board.Task{Title: "Duplicate work item", Status: board.StatusTodo, Prio: 3, Tags: []string{"link::github#90"}})
	killed, _ := backend.Store.AddTask("u", board.Task{Title: "Duplicate work item old", Status: board.StatusCancelled, Prio: 3})
	if err := backend.Store.RecordTombstone("u", killed.ID, "superseded by issue 90"); err != nil {
		t.Fatal(err)
	}
	model := New(backend, "u")
	model.OpenEdit(edited)
	query := strings.TrimSpace(model.title.Value())
	model.similarGen = 7
	search := model.Update(similarDebounceMsg{generation: 7, query: query})
	if search == nil || !model.similarLoading {
		t.Fatal("eligible title did not start direct search")
	}
	model.Update(search())
	if len(backend.queries) != 1 || backend.queries[0].exclude != edited.ID || strings.Join(backend.queries[0].links, ",") != "github#90" || backend.queries[0].limit != similarLimit {
		t.Fatalf("search query = %+v", backend.queries)
	}
	if len(model.similar) == 0 {
		t.Fatal("expected similar store results")
	}
	if command := model.scheduleSimilar(); command != nil {
		t.Fatal("unchanged successful query scheduled duplicate search")
	}
	view := ansi.Strip(model.View(100, 40))
	if !strings.Contains(view, "superseded by issue 90") || !strings.Contains(view, "killed") {
		t.Fatalf("killed chip lost context:\n%s", view)
	}
	first := model.visibleSimilar()[0]
	model.focus = "similar:" + similarKey(first)
	model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(model.visibleSimilar()) != len(model.similar)-1 {
		t.Fatal("row dismissal did not persist")
	}
	model.focus = "similar:all"
	model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(model.visibleSimilar()) != 0 {
		t.Fatal("dismiss all retained rows")
	}

	model.title.SetValue("ab")
	if command := model.scheduleSimilar(); command != nil || model.similar != nil || model.similarLoading {
		t.Fatalf("short title retained search state: command:%v hits:%v loading:%v", command, model.similar, model.similarLoading)
	}
	model.Update(similarLoadedMsg{generation: 7, query: query, hits: []store.SimilarHit{{Title: "stale"}}})
	if model.similar != nil {
		t.Fatal("stale similar response was adopted")
	}
}

func TestLabelComboboxAndPickerControls(t *testing.T) {
	model := New(newTestStore(t), "u")
	model.OpenAdd(board.StatusTodo)
	model.Update(labelsLoadedMsg{labels: []string{"alpha", "alphabet", "beta"}})
	model.focus = "labels"
	model.applyFocus()
	model.label.SetValue("alp")
	model.labelsOpen = true
	model.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(model.tags) != 1 || model.tags[0] != "alphabet" {
		t.Fatalf("highlighted suggestion = %v", model.tags)
	}
	model.label.SetValue("#free scope::value free")
	model.labelsOpen = false
	model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if strings.Join(model.tags, ",") != "alphabet,free,scope::value" {
		t.Fatalf("free labels = %v", model.tags)
	}
	model.label.SetValue("")
	model.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if strings.Join(model.tags, ",") != "alphabet,free" {
		t.Fatalf("empty backspace = %v", model.tags)
	}
	model.Update(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
	if len(model.tags) != 0 {
		t.Fatalf("clear labels = %v", model.tags)
	}

	model.focus = "due"
	model.now = func() time.Time { return time.Date(2026, 8, 17, 22, 0, 0, 0, time.FixedZone("x", 3600)) }
	model.Update(press(']'))
	if model.due.Value() != "2026-08-18" {
		t.Fatalf("date picker from today = %q", model.due.Value())
	}
	model.Update(press('['))
	model.Update(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
	if model.due.Value() != "" {
		t.Fatalf("due clear = %q", model.due.Value())
	}
	model.focus = "effort"
	model.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	model.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	if model.effort != "M" {
		t.Fatalf("effort picker = %q", model.effort)
	}
	model.Update(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
	if model.effort != "" {
		t.Fatalf("effort clear = %q", model.effort)
	}
}

func TestFailureLoadsBusyRoutingAndHelperEdges(t *testing.T) {
	backend := newTestStore(t)
	backend.labelsErr = errors.New("labels\nfailed")
	model := New(backend, "u")
	run(t, &model, model.OpenAdd(board.Status("bad")))
	if model.status != board.StatusTodo || model.labelsErr == nil {
		t.Fatalf("fallback/load state = status:%q err:%v", model.status, model.labelsErr)
	}
	backend.similarErr = errors.New("similar failed")
	model.title.SetValue("query title")
	model.similarGen = 1
	search := model.Update(similarDebounceMsg{generation: 1, query: "query title"})
	model.Update(search())
	if model.similarErr == nil || model.similarLoading {
		t.Fatalf("similar failure = err:%v loading:%v", model.similarErr, model.similarLoading)
	}
	model.saving = true
	before := model.title.Value()
	model.Update(press('z'))
	if model.title.Value() != before {
		t.Fatal("busy editor accepted conflicting input")
	}
	model.saving = false

	checks := textToChecks("x done\nopen\n X not-done\n")
	if checksToText(checks) != "x done\nopen\nX not-done" || len(checks) != 3 || checks[2].Done {
		t.Fatalf("check conversion = %+v / %q", checks, checksToText(checks))
	}
	if got := adjustDate("2026-12-31", 1, time.Time{}); got != "2027-01-01" {
		t.Fatalf("date rollover = %q", got)
	}
	if safeError(nil) != "" || safeError(errors.New(" \n\t")) != "operation failed" || len([]rune(safeError(errors.New(strings.Repeat("a", 300))))) != 180 {
		t.Fatal("safe error normalization failed")
	}
	if IsMessage(press('x')) || !IsMessage(labelsLoadedMsg{}) {
		t.Fatal("message ownership mismatch")
	}
	disabled := New(nil, "u")
	if disabled.Enabled() || disabled.OpenAdd(board.StatusTodo) != nil || disabled.OpenEdit(board.Task{ID: "x"}) != nil {
		t.Fatal("nil backend exposed editor")
	}
}

func TestKeyboardRoutesEveryFieldAndAction(t *testing.T) {
	backend := newTestStore(t)
	model := New(backend, "u")
	model.OpenAdd(board.StatusTodo)
	typeText := func(text string) {
		for _, char := range text {
			model.Update(tea.KeyPressMsg{Code: char, Text: string(char)})
		}
	}
	typeText("Card")
	if model.title.Value() != "Card" {
		t.Fatalf("title input = %q", model.title.Value())
	}
	model.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	typeText("🧭")
	model.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	typeText("description")
	model.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	for _, key := range []tea.KeyPressMsg{{Code: tea.KeyRight}, {Code: tea.KeyLeft}, {Code: '+'}, {Code: '-'}, {Code: tea.KeyEnter}} {
		model.Update(key)
	}
	model.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	model.Update(tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModAlt})
	model.Update(tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModAlt})
	model.Update(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
	typeText("2026-08-20")
	model.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	model.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	model.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	model.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	model.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	model.Update(labelsLoadedMsg{labels: []string{"alpha", "beta"}})
	model.labelsOpen = true
	model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !model.IsOpen() || model.labelsOpen {
		t.Fatal("label escape leaked to editor close")
	}
	model.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	model.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	typeText("x done")
	model.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if model.focus != "cancel" {
		t.Fatalf("focus after fields = %q", model.focus)
	}
	model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !model.guardClose {
		t.Fatal("cancel action bypassed dirty guard")
	}
	model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}))
	if model.focus != "checks" {
		t.Fatalf("reverse focus = %q", model.focus)
	}
	model.focus = "save"
	save := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if save == nil {
		t.Fatalf("keyboard save rejected: %s", model.statusMessage)
	}
	model.Update(save())
	if model.IsOpen() {
		t.Fatal("keyboard save did not close")
	}
}

func TestFocusTargetsHelpersAndCleanCloseBranches(t *testing.T) {
	model := New(newTestStore(t), "u")
	model.OpenAdd(board.StatusTodo)
	if model.TaskID() != "" {
		t.Fatalf("add task id = %q", model.TaskID())
	}
	model.requestClose()
	if model.IsOpen() {
		t.Fatal("clean request close stayed open")
	}
	model.OpenAdd(board.StatusTodo)
	model.similar = []store.SimilarHit{
		{ID: "id", Title: "id hit"},
		{Link: "link", Title: "link hit"},
		{Title: "title hit"},
	}
	targets := strings.Join(model.focusTargets(), ",")
	for _, want := range []string{"similar:id:id", "similar:link:link", "similar:title:title hit", "similar:all"} {
		if !strings.Contains(targets, want) {
			t.Errorf("targets %q missing %q", targets, want)
		}
	}
	model.focus = "missing"
	model.moveFocus(1)
	if model.focus != "emoji" {
		t.Fatalf("unknown focus fallback = %q", model.focus)
	}
	model.dismissedAll = true
	if strings.Contains(strings.Join(model.focusTargets(), ","), "similar:") {
		t.Fatal("dismissed panel retained focus targets")
	}
	if links := importLinks([]string{"link::a", "link::a", "link:: ", "other"}); strings.Join(links, ",") != "a" {
		t.Fatalf("import links = %v", links)
	}
	if got := batch(nil, func() tea.Msg { return "one" }); got == nil || got().(string) != "one" {
		t.Fatal("single command batch failed")
	}
	if got := batch(func() tea.Msg { return "one" }, func() tea.Msg { return "two" }); got == nil {
		t.Fatal("multi command batch failed")
	}
}
