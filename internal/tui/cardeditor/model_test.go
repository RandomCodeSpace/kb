package cardeditor

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/ai"
	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
)

type faultStore struct {
	*store.Store
	addErr, updateErr, labelsErr, similarErr error
	beforeUpdate                             func()
	queries                                  []similarQuery
}

type similarQuery struct {
	user, title, exclude string
	links                []string
	limit                int
}

type draftRunnerCall struct {
	ctx                context.Context
	user, skill, input string
	scope              ai.Scope
	maxCards           int
	maxTokens          int64
}

type fakeDraftRunner struct {
	run   ai.RunResult
	err   error
	calls []draftRunnerCall
}

func (r *fakeDraftRunner) RunSkill(ctx context.Context, user string, scope ai.Scope, skill, input string, maxCards int, maxTokens int64) (ai.RunResult, error) {
	r.calls = append(r.calls, draftRunnerCall{ctx: ctx, user: user, scope: scope, skill: skill, input: input, maxCards: maxCards, maxTokens: maxTokens})
	return r.run, r.err
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

func (s *faultStore) UpdateTaskIfFieldsMatch(user, id string, expected, patch store.TaskPatch) (board.Task, error) {
	if s.updateErr != nil {
		return board.Task{}, s.updateErr
	}
	if s.beforeUpdate != nil {
		beforeUpdate := s.beforeUpdate
		s.beforeUpdate = nil
		beforeUpdate()
	}
	return s.Store.UpdateTaskIfFieldsMatch(user, id, expected, patch)
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
	createdID, saved := model.ConsumeSaved()
	_, savedAgain := model.ConsumeSaved()
	if model.IsOpen() || !saved || createdID == "" || savedAgain {
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

func TestEditMergesConcurrentUnrelatedStoreChanges(t *testing.T) {
	database := t.TempDir() + "/kb.db"
	secret := []byte("concurrent-editor-test-key")
	editorStore, err := store.Open(database, secret)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = editorStore.Close() })
	concurrentStore, err := store.Open(database, secret)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = concurrentStore.Close() })

	created, err := editorStore.AddTask("u", board.Task{
		Title: "Original", Desc: "old description", Status: board.StatusTodo, Prio: 3,
		Due: "2026-08-20", Tags: []string{"old"}, Checks: []board.Check{{Text: "old check"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	model := New(editorStore, "u")
	model.OpenEdit(created)
	model.title.SetValue("Local title")

	remoteDescription := "remote description"
	remoteDue := "2026-09-01"
	remoteBlocked := true
	remoteTags := []string{"remote", "link::github#90"}
	remoteChecks := []board.Check{{Text: "remote check", Done: true}}
	remote, err := concurrentStore.UpdateTask("u", created.ID, store.TaskPatch{
		Desc: &remoteDescription, Due: &remoteDue, Blocked: &remoteBlocked,
		Tags: &remoteTags, Checks: &remoteChecks,
	})
	if err != nil {
		t.Fatal(err)
	}
	model.Refresh(remote, true)
	save := model.startSave()
	if save == nil {
		t.Fatalf("unrelated concurrent changes blocked save: %s", model.statusMessage)
	}
	model.Update(save())

	latest, err := concurrentStore.Board("u")
	if err != nil || len(latest.Tasks) != 1 {
		t.Fatalf("board = %+v, %v", latest, err)
	}
	got := latest.Tasks[0]
	if got.Title != "Local title" || got.Desc != remoteDescription || got.Due != remoteDue ||
		!got.Blocked || !stringSlicesEqual(got.Tags, remoteTags) || !checksEqual(got.Checks, remoteChecks) {
		t.Fatalf("selective patch lost concurrent fields: %+v", got)
	}
}

func TestEditRejectsSameFieldConflictAfterDirtyRefresh(t *testing.T) {
	backend := newTestStore(t)
	created, err := backend.Store.AddTask("u", board.Task{Title: "Original", Status: board.StatusTodo, Prio: 3})
	if err != nil {
		t.Fatal(err)
	}
	model := New(backend, "u")
	model.OpenEdit(created)
	model.title.SetValue("Local title")
	remoteTitle := "Remote title"
	remote, err := backend.Store.UpdateTask("u", created.ID, store.TaskPatch{Title: &remoteTitle})
	if err != nil {
		t.Fatal(err)
	}
	model.Refresh(remote, true)
	if command := model.startSave(); command != nil || !model.IsOpen() || model.saving ||
		!strings.Contains(model.statusMessage, "title") {
		t.Fatalf("same-field conflict = command:%v open:%v saving:%v status:%q", command, model.IsOpen(), model.saving, model.statusMessage)
	}
	latest, _ := backend.Board("u")
	if latest.Tasks[0].Title != remoteTitle || model.title.Value() != "Local title" {
		t.Fatalf("conflict mutated store or form: stored:%q form:%q", latest.Tasks[0].Title, model.title.Value())
	}
}

func TestEditSaveCASRejectsLateSameFieldWriteAndPreservesLateUnrelatedWrite(t *testing.T) {
	t.Run("same field", func(t *testing.T) {
		backend := newTestStore(t)
		created, err := backend.Store.AddTask("u", board.Task{
			Title: "Original", Desc: "original description", Status: board.StatusTodo, Prio: 3,
		})
		if err != nil {
			t.Fatal(err)
		}
		model := New(backend, "u")
		model.OpenEdit(created)
		model.title.SetValue("Local title")
		backend.beforeUpdate = func() {
			remoteTitle := "Remote title"
			if _, updateErr := backend.Store.UpdateTask("u", created.ID, store.TaskPatch{Title: &remoteTitle}); updateErr != nil {
				t.Fatalf("late remote update: %v", updateErr)
			}
		}

		save := model.startSave()
		if save == nil {
			t.Fatalf("start save: %s", model.statusMessage)
		}
		model.Update(save())
		latest, readErr := backend.Store.Task("u", created.ID)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if latest.Title != "Remote title" || !model.IsOpen() || model.saving ||
			!strings.Contains(model.statusMessage, "title") || model.title.Value() != "Local title" {
			t.Fatalf("late conflict = stored:%q open:%v saving:%v status:%q form:%q",
				latest.Title, model.IsOpen(), model.saving, model.statusMessage, model.title.Value())
		}
	})

	t.Run("converged field diverges before transaction", func(t *testing.T) {
		backend := newTestStore(t)
		created, err := backend.Store.AddTask("u", board.Task{
			Title: "Original", Status: board.StatusTodo, Prio: 3,
		})
		if err != nil {
			t.Fatal(err)
		}
		model := New(backend, "u")
		model.OpenEdit(created)
		model.title.SetValue("Local title")
		convergedTitle := "Local title"
		converged, err := backend.Store.UpdateTask("u", created.ID, store.TaskPatch{Title: &convergedTitle})
		if err != nil {
			t.Fatal(err)
		}
		model.Refresh(converged, true)
		if _, patch, buildErr := model.buildSave(); buildErr != nil || patch != (store.TaskPatch{}) {
			t.Fatalf("converged save = patch:%+v err:%v", patch, buildErr)
		}
		backend.beforeUpdate = func() {
			divergedTitle := "Diverged title"
			if _, updateErr := backend.Store.UpdateTask("u", created.ID, store.TaskPatch{Title: &divergedTitle}); updateErr != nil {
				t.Fatalf("late divergent update: %v", updateErr)
			}
		}

		save := model.startSave()
		model.Update(save())
		latest, readErr := backend.Store.Task("u", created.ID)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if latest.Title != "Diverged title" || !model.IsOpen() || model.saving ||
			!strings.Contains(model.statusMessage, "title") || model.title.Value() != "Local title" {
			t.Fatalf("converge/diverge conflict = stored:%q open:%v saving:%v status:%q form:%q",
				latest.Title, model.IsOpen(), model.saving, model.statusMessage, model.title.Value())
		}
	})

	t.Run("unrelated field", func(t *testing.T) {
		backend := newTestStore(t)
		created, err := backend.Store.AddTask("u", board.Task{
			Title: "Original", Desc: "original description", Status: board.StatusTodo, Prio: 3,
		})
		if err != nil {
			t.Fatal(err)
		}
		model := New(backend, "u")
		model.OpenEdit(created)
		model.title.SetValue("Local title")
		backend.beforeUpdate = func() {
			remoteDescription := "Remote description"
			if _, updateErr := backend.Store.UpdateTask("u", created.ID, store.TaskPatch{Desc: &remoteDescription}); updateErr != nil {
				t.Fatalf("late remote update: %v", updateErr)
			}
		}

		save := model.startSave()
		model.Update(save())
		latest, readErr := backend.Store.Task("u", created.ID)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if latest.Title != "Local title" || latest.Desc != "Remote description" || model.IsOpen() {
			t.Fatalf("late merge = %+v, open:%v status:%q", latest, model.IsOpen(), model.statusMessage)
		}
	})
}

func TestSaveCompletionIsScopedToEditorSession(t *testing.T) {
	backend := newTestStore(t)
	model := New(backend, "u")
	model.OpenAdd(board.StatusTodo)
	model.title.SetValue("Old session")
	oldSave := model.startSave()
	oldSession := model.session

	model.OpenAdd(board.StatusDoing)
	model.title.SetValue("New session")
	model.Update(oldSave())
	if model.session == oldSession || !model.IsOpen() || model.title.Value() != "New session" || model.saving {
		t.Fatalf("stale save changed new session: session:%d open:%v title:%q saving:%v",
			model.session, model.IsOpen(), model.title.Value(), model.saving)
	}
	if id, saved := model.ConsumeSaved(); saved || id != "" {
		t.Fatalf("stale save acknowledged in new session: id:%q saved:%v", id, saved)
	}
}

func TestSelectiveTaskPatchCoversEveryEditableField(t *testing.T) {
	original := board.Task{
		Emoji: "🧭", Title: "old", Desc: "old desc", Due: "2026-08-20", Effort: "S",
		Prio: 3, Tags: []string{"old"}, Checks: []board.Check{{Text: "old"}},
	}
	desired := board.Task{
		Emoji: "🧪", Title: "new", Desc: "new desc", Due: "", Effort: "L",
		Prio: 1, Blocked: true, Tags: []string{"new"}, Checks: []board.Check{{Text: "new", Done: true}},
	}
	allChanged := editedFields{
		emoji: true, title: true, desc: true, due: true, effort: true,
		prio: true, blocked: true, tags: true, checks: true,
	}
	patch, err := selectiveTaskPatch(original, original, desired, allChanged)
	if err != nil || patch.Emoji == nil || patch.Title == nil || patch.Desc == nil || patch.Due == nil ||
		patch.Effort == nil || patch.Prio == nil || patch.Blocked == nil || patch.Tags == nil || patch.Checks == nil {
		t.Fatalf("complete selective patch = %+v, %v", patch, err)
	}
	expected := expectedTaskFields(original, allChanged)
	if expected.Emoji == nil || *expected.Emoji != original.Emoji ||
		expected.Title == nil || *expected.Title != original.Title ||
		expected.Desc == nil || *expected.Desc != original.Desc ||
		expected.Due == nil || *expected.Due != original.Due ||
		expected.Effort == nil || *expected.Effort != original.Effort ||
		expected.Prio == nil || *expected.Prio != original.Prio ||
		expected.Blocked == nil || *expected.Blocked != original.Blocked ||
		expected.Tags == nil || !stringSlicesEqual(*expected.Tags, original.Tags) ||
		expected.Checks == nil || !checksEqual(*expected.Checks, original.Checks) {
		t.Fatalf("complete expected fields = %+v", expected)
	}
	if empty := expectedTaskFields(original, editedFields{}); empty != (store.TaskPatch{}) {
		t.Fatalf("empty patch produced expectations = %+v", empty)
	}

	alreadyCanonical, err := selectiveTaskPatch(original, desired, desired, allChanged)
	if err != nil || alreadyCanonical != (store.TaskPatch{}) {
		t.Fatalf("already-applied changes produced patch = %+v, %v", alreadyCanonical, err)
	}

	canonical := original
	canonical.Emoji = "🔧"
	canonical.Title = "remote"
	canonical.Desc = "remote desc"
	canonical.Due = "2026-09-02"
	canonical.Effort = "M"
	canonical.Prio = 2
	canonical.Tags = []string{"remote"}
	canonical.Checks = []board.Check{{Text: "remote"}}
	if _, err := selectiveTaskPatch(original, canonical, desired, allChanged); err == nil ||
		!strings.Contains(err.Error(), "emoji, title, description, due, effort, priority, labels, checklist") {
		t.Fatalf("multi-field conflict = %v", err)
	}

	if stringSlicesEqual([]string{"a"}, []string{"a", "b"}) || stringSlicesEqual([]string{"a"}, []string{"b"}) ||
		checksEqual([]board.Check{{Text: "a"}}, []board.Check{{Text: "a"}, {Text: "b"}}) ||
		checksEqual([]board.Check{{Text: "a"}}, []board.Check{{Text: "b"}}) {
		t.Fatal("slice comparison accepted unequal values")
	}

	model := New(newTestStore(t), "u")
	model.OpenEdit(board.Task{ID: "task", Title: "  untouched title  ", Desc: "old", Status: board.StatusTodo, Prio: 3})
	model.desc.SetValue("new")
	_, whitespacePatch, err := model.buildSave()
	if err != nil || whitespacePatch.Title != nil || whitespacePatch.Desc == nil {
		t.Fatalf("normalization patched untouched field: %+v, %v", whitespacePatch, err)
	}
}

func TestSimilarCacheReturnClearsInflightLoadingAndRejectsOldResults(t *testing.T) {
	model := New(newTestStore(t), "u")
	model.OpenAdd(board.StatusTodo)
	model.title.SetValue("alpha query")
	model.similarGen = 1
	alpha := []store.SimilarHit{{ID: "alpha", Title: "Alpha cached"}}
	model.Update(similarLoadedMsg{generation: 1, query: "alpha query", hits: alpha})

	model.title.SetValue("beta query")
	betaTick := model.scheduleSimilar()
	if betaTick == nil {
		t.Fatal("uncached beta query did not debounce")
	}
	betaSearch := model.Update(betaTick())
	if betaSearch == nil || !model.similarLoading {
		t.Fatal("beta query did not enter loading state")
	}

	model.title.SetValue("alpha query")
	if command := model.scheduleSimilar(); command != nil || model.similarLoading || model.similarErr != nil ||
		len(model.similar) != 1 || model.similar[0].ID != "alpha" {
		t.Fatalf("cached alpha restore = command:%v loading:%v err:%v hits:%+v", command, model.similarLoading, model.similarErr, model.similar)
	}
	model.Update(similarLoadedMsg{
		generation: 2, query: "beta query", hits: []store.SimilarHit{{ID: "beta", Title: "Late beta"}},
	})
	if model.similarLoading || len(model.similar) != 1 || model.similar[0].ID != "alpha" {
		t.Fatalf("late beta displaced cached alpha: loading:%v hits:%+v", model.similarLoading, model.similar)
	}
}

func TestLabelLoadsAreScopedToEditorSession(t *testing.T) {
	model := New(newTestStore(t), "u")
	model.OpenAdd(board.StatusTodo)
	oldSession := model.session
	model.requestClose()
	model.OpenAdd(board.StatusTodo)
	currentSession := model.session
	model.Update(labelsLoadedMsg{session: currentSession, labels: []string{"current"}})
	model.Update(labelsLoadedMsg{session: oldSession, labels: []string{"stale"}})
	model.Update(labelsLoadedMsg{session: oldSession, err: errors.New("stale error")})
	if strings.Join(model.labels, ",") != "current" || model.labelsErr != nil {
		t.Fatalf("old label result leaked into reopened editor: labels:%v err:%v", model.labels, model.labelsErr)
	}
}

func TestDuePickerUsesInjectedLocalCalendarDay(t *testing.T) {
	for _, test := range []struct {
		name string
		now  time.Time
		raw  string
		want string
	}{
		{
			name: "east of UTC after local midnight",
			now:  time.Date(2026, 8, 18, 0, 15, 0, 0, time.FixedZone("UTC+14", 14*60*60)),
			want: "2026-08-19",
		},
		{
			name: "west of UTC before local midnight invalid input",
			now:  time.Date(2026, 8, 17, 23, 45, 0, 0, time.FixedZone("UTC-10", -10*60*60)),
			raw:  "not-a-date",
			want: "2026-08-18",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := adjustDate(test.raw, 1, test.now); got != test.want {
				t.Fatalf("adjustDate(%q, local %s) = %q, want %q", test.raw, test.now, got, test.want)
			}
		})
	}
}

func TestLinkLabelChangesInvalidateSimilarSearchExclusions(t *testing.T) {
	backend := newTestStore(t)
	model := New(backend, "u")
	model.OpenAdd(board.StatusTodo)
	model.title.SetValue("same title")
	model.tags = []string{"link::a"}
	oldTick := model.scheduleSimilar()
	oldSearch := model.Update(oldTick())
	if oldSearch == nil || !model.similarLoading {
		t.Fatal("old exclusion query did not start")
	}

	model.focus = "labels"
	model.label.SetValue("")
	model.Update(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
	model.label.SetValue("link::b")
	newTick := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if newTick == nil || model.similarGen < 3 {
		t.Fatalf("link label change did not invalidate search: generation:%d command:%v", model.similarGen, newTick)
	}
	model.Update(oldSearch())
	if len(model.similar) != 0 {
		t.Fatalf("old-exclusion result was adopted: %+v", model.similar)
	}
	newSearch := model.Update(newTick())
	if newSearch == nil {
		t.Fatal("new exclusion query did not start")
	}
	model.Update(newSearch())
	if len(backend.queries) < 2 || strings.Join(backend.queries[len(backend.queries)-1].links, ",") != "b" {
		t.Fatalf("latest similar exclusions = %+v", backend.queries)
	}

	generation := model.similarGen
	model.label.SetValue("ordinary")
	if command := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); command != nil || model.similarGen != generation {
		t.Fatalf("non-link label needlessly reran search: generation:%d command:%v", model.similarGen, command)
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
	exclusions := model.currentExclusions()
	search := model.Update(similarDebounceMsg{generation: 7, query: query, exclusions: exclusions})
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
	model.Update(similarLoadedMsg{generation: 7, query: query, exclusions: exclusions, hits: []store.SimilarHit{{Title: "stale"}}})
	if model.similar != nil {
		t.Fatal("stale similar response was adopted")
	}
}

func TestLabelComboboxAndPickerControls(t *testing.T) {
	model := New(newTestStore(t), "u")
	model.OpenAdd(board.StatusTodo)
	model.Update(labelsLoadedMsg{session: model.session, labels: []string{"alpha", "alphabet", "beta"}})
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
	search := model.Update(similarDebounceMsg{generation: 1, query: "query title", exclusions: model.currentExclusions()})
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
	model.Update(labelsLoadedMsg{session: model.session, labels: []string{"alpha", "beta"}})
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

func TestAIDraftCreateUsesReadOnlyRunnerAndFillsFormForReview(t *testing.T) {
	runner := &fakeDraftRunner{run: ai.RunResult{Cards: []ai.Draft{{
		Title: "Drafted card", Emoji: "🧭", Desc: "generated", Prio: 1,
		Due: "2026-08-30", Effort: "L", Tags: []string{"ai", "type::feature"},
		Checks: []ai.DraftCheck{{Text: "review"}, {Text: "ship", Done: true}},
	}}}}
	model := New(newTestStore(t), "alice")
	model.SetAIRunner(runner, context.Background())
	run(t, &model, model.OpenAdd(board.StatusDoing))
	model.draftPrompt.SetValue("write the release task")
	model.focus = "ai-draft"
	command := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if command == nil || !model.drafting || model.statusMessage != "drafting card..." {
		t.Fatalf("draft start command=%v drafting=%v status=%q", command, model.drafting, model.statusMessage)
	}
	model.Update(commandMsgForEditor(t, command))
	if len(runner.calls) != 1 {
		t.Fatalf("runner calls = %d", len(runner.calls))
	}
	call := runner.calls[0]
	if call.user != "alice" || call.scope != ai.ScopeReadOnly || call.skill != "story-draft" || call.maxCards != 1 || call.maxTokens != draftMaxTokens || !strings.HasPrefix(call.input, "Create a new kanban card") {
		t.Fatalf("runner call = %+v", call)
	}
	if !errors.Is(call.ctx.Err(), context.Canceled) {
		t.Fatalf("completed draft context = %v", call.ctx.Err())
	}
	if model.title.Value() != "Drafted card" || model.emoji.Value() != "🧭" || model.desc.Value() != "generated" || model.prio != 1 || model.due.Value() != "2026-08-30" || model.effort != "L" || model.blocked || !reflect.DeepEqual(model.tags, []string{"ai", "type::feature"}) || model.checks.Value() != "review\nx ship" {
		t.Fatalf("applied form title=%q emoji=%q desc=%q prio=%d due=%q effort=%q blocked=%v tags=%v checks=%q", model.title.Value(), model.emoji.Value(), model.desc.Value(), model.prio, model.due.Value(), model.effort, model.blocked, model.tags, model.checks.Value())
	}
	if !model.IsOpen() || !model.Dirty() || model.drafting || model.statusIsError || !strings.Contains(model.statusMessage, "review") {
		t.Fatalf("post-draft open=%v dirty=%v drafting=%v status=%q error=%v", model.open, model.Dirty(), model.drafting, model.statusMessage, model.statusIsError)
	}
}

func TestAIDraftEditCarriesCurrentFormJSONAndPreservesBlocked(t *testing.T) {
	runner := &fakeDraftRunner{run: ai.RunResult{Cards: []ai.Draft{{
		Title: "Updated", Desc: "new", Prio: 4, Tags: []string{}, Checks: []ai.DraftCheck{},
	}}}}
	model := New(newTestStore(t), "u")
	model.SetAIRunner(runner, context.Background())
	run(t, &model, model.OpenEdit(fullEditorTask()))
	model.title.SetValue("locally edited")
	model.draftPrompt.SetValue("make it smaller")
	command := model.startDraft()
	model.Update(commandMsgForEditor(t, command))
	if !strings.HasPrefix(runner.calls[0].input, "Update the kanban card") || !strings.Contains(runner.calls[0].input, "Current card JSON") {
		t.Fatalf("edit prompt = %q", runner.calls[0].input)
	}
	jsonText := strings.Split(runner.calls[0].input, "Current card JSON:\n")[1]
	var current map[string]any
	if err := json.Unmarshal([]byte(jsonText), &current); err != nil {
		t.Fatal(err)
	}
	if current["title"] != "locally edited" || current["desc"] != fullEditorTask().Desc || current["prio"] != float64(1) {
		t.Fatalf("current JSON = %#v", current)
	}
	if _, found := current["emoji"]; found {
		t.Fatalf("wire current card unexpectedly included emoji: %#v", current)
	}
	if model.title.Value() != "Updated" || !model.blocked {
		t.Fatalf("application title=%q blocked=%v", model.title.Value(), model.blocked)
	}
}

func TestAIDraftCancellationErrorsAndExternalDeleteCannotReviveEditor(t *testing.T) {
	runner := &fakeDraftRunner{run: ai.RunResult{Cards: []ai.Draft{{Title: "late", Prio: 3}}}}
	model := New(newTestStore(t), "u")
	model.SetAIRunner(runner, context.Background())
	run(t, &model, model.OpenEdit(fullEditorTask()))
	model.draftPrompt.SetValue("draft")
	command := model.startDraft()
	generation := model.draftGen
	model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if model.drafting || model.draftGen == generation || model.statusMessage != "AI draft cancelled" {
		t.Fatalf("cancel state drafting=%v gen=%d status=%q", model.drafting, model.draftGen, model.statusMessage)
	}
	message := commandMsgForEditor(t, command)
	if !errors.Is(runner.calls[0].ctx.Err(), context.Canceled) {
		t.Fatal("draft context was not cancelled")
	}
	model.Update(message)
	if model.title.Value() == "late" {
		t.Fatal("cancelled result applied")
	}

	model.draftPrompt.SetValue("again")
	command = model.startDraft()
	model.Refresh(board.Task{}, false)
	if model.IsOpen() || model.drafting {
		t.Fatalf("external delete open=%v drafting=%v", model.open, model.drafting)
	}
	model.Update(commandMsgForEditor(t, command))
	if len(runner.calls) != 2 {
		t.Fatalf("external delete runner calls=%d, want 2", len(runner.calls))
	}
	if err := runner.calls[1].ctx.Err(); !errors.Is(err, context.Canceled) {
		t.Fatalf("external delete runner context=%v", err)
	}
	if model.IsOpen() || model.title.Value() == "late" {
		t.Fatal("late result revived deleted editor")
	}

	runner.err = errors.New("upstream\x1b[31m\nfailed")
	runner.run.Cards = nil
	run(t, &model, model.OpenAdd(board.StatusTodo))
	model.draftPrompt.SetValue("fail")
	model.Update(commandMsgForEditor(t, model.startDraft()))
	if !model.statusIsError || strings.Contains(model.statusMessage, "\x1b") || strings.Contains(model.statusMessage, "\nfailed") {
		t.Fatalf("unsafe error status = %q", model.statusMessage)
	}
	runner.err = nil
	model.Update(commandMsgForEditor(t, model.startDraft()))
	if !strings.Contains(model.statusMessage, "no usable card") {
		t.Fatalf("empty proposal status = %q", model.statusMessage)
	}
}

func TestAIDraftUnavailableBlankStaleAndShutdownBranches(t *testing.T) {
	model := New(newTestStore(t), "u")
	run(t, &model, model.OpenAdd(board.StatusTodo))
	if command := model.startDraft(); command != nil {
		t.Fatal("missing runner started draft")
	}
	runner := &fakeDraftRunner{run: ai.RunResult{Cards: []ai.Draft{{Title: "ok", Prio: 3}}}}
	model.SetAIRunner(runner, nil)
	if command := model.startDraft(); command != nil || !model.statusIsError || !strings.Contains(model.statusMessage, "required") {
		t.Fatalf("blank request command=%v status=%q", command, model.statusMessage)
	}
	model.draftPrompt.SetValue("draft")
	command := model.startDraft()
	model.CancelAsync()
	if model.drafting || model.draftCancel != nil {
		t.Fatal("shutdown left draft active")
	}
	model.Update(commandMsgForEditor(t, command))

	model.Update(draftCompletedMsg{session: model.session + 1, generation: model.draftGen, draft: ai.Draft{Title: "stale"}})
	model.Update(draftCompletedMsg{session: model.session, generation: model.draftGen + 1, draft: ai.Draft{Title: "stale"}})
	if model.title.Value() == "stale" {
		t.Fatal("stale result applied")
	}
	model.SetAIRunner(nil, context.Background())
}

func commandMsgForEditor(t *testing.T, command tea.Cmd) tea.Msg {
	t.Helper()
	if command == nil {
		t.Fatal("command is nil")
	}
	return command()
}
