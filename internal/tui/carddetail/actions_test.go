package carddetail

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
)

type actionStore struct {
	comments  []store.Comment
	links     store.TaskLinks
	addErr    error
	deleteErr error
	linkErr   error
	unlinkErr error

	addedBody               string
	addedTask, addedAuthor  string
	deletedID               int
	blockerRef, blockedRef  string
	unlinkA, unlinkB        string
	commentLoads, linkLoads int
}

func (s *actionStore) Comments(_ string, taskRef string) ([]store.Comment, error) {
	s.commentLoads++
	out := make([]store.Comment, 0, len(s.comments))
	for _, comment := range s.comments {
		if comment.TaskID == "" || comment.TaskID == taskRef {
			out = append(out, comment)
		}
	}
	return out, nil
}

func (s *actionStore) TaskLinks(string, string) (store.TaskLinks, error) {
	s.linkLoads++
	return store.TaskLinks{
		Blocks:    append([]board.Task(nil), s.links.Blocks...),
		BlockedBy: append([]board.Task(nil), s.links.BlockedBy...),
	}, nil
}

func (*actionStore) Tombstone(string, string) (store.Tombstone, bool, error) {
	return store.Tombstone{}, false, nil
}

func (s *actionStore) AddComment(_ string, taskRef, author, body string) (store.Comment, error) {
	s.addedTask, s.addedAuthor, s.addedBody = taskRef, author, body
	if s.addErr != nil {
		return store.Comment{}, s.addErr
	}
	created := store.Comment{
		ID: len(s.comments) + 1, TaskID: taskRef, TaskSeq: 7, Author: author, Body: body,
		CreatedAt: time.Date(2026, time.August, 18, 1, 0, 0, 0, time.UTC),
	}
	s.comments = append(s.comments, created)
	return created, nil
}

func (s *actionStore) DeleteComment(_ string, id int) (store.Comment, error) {
	s.deletedID = id
	if s.deleteErr != nil {
		return store.Comment{}, s.deleteErr
	}
	for i, comment := range s.comments {
		if comment.ID == id {
			s.comments = append(s.comments[:i], s.comments[i+1:]...)
			return comment, nil
		}
	}
	return store.Comment{}, store.ErrNotFound
}

func (s *actionStore) Link(_ string, blockerRef, blockedRef string) (board.Task, board.Task, error) {
	s.blockerRef, s.blockedRef = blockerRef, blockedRef
	if s.linkErr != nil {
		return board.Task{}, board.Task{}, s.linkErr
	}
	blocker := board.Task{ID: blockerRef, Seq: 2, Status: board.StatusDoing}
	blocked := board.Task{ID: blockedRef, Seq: 7, Status: board.StatusTodo}
	return blocker, blocked, nil
}

func (s *actionStore) Unlink(_ string, aRef, bRef string) error {
	s.unlinkA, s.unlinkB = aRef, bRef
	return s.unlinkErr
}

func openActionModel(t *testing.T, st *actionStore) *Model {
	t.Helper()
	m := New(st, "alice")
	load := m.Open(board.Task{ID: "task-7", Seq: 7, Title: "Seven", Status: board.StatusTodo, Prio: 3})
	if load == nil {
		t.Fatal("detail open did not load enrichment")
	}
	if command := m.Update(load()); command != nil {
		t.Fatalf("initial load returned command %v", command)
	}
	return &m
}

func key(code rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code, Text: string(code)} }

func TestCommentAddPreservesRefusedInputAndReloadsAcknowledgedWrite(t *testing.T) {
	st := &actionStore{addErr: errors.New("refused\x1b[31m\nunsafe")}
	m := openActionModel(t, st)
	if command := m.Update(key('c')); command != nil || m.action == actionNone || !m.OwnsInput() {
		t.Fatalf("comment action = command:%v action:%v owned:%v", command, m.action, m.OwnsInput())
	}
	if command := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}); command != nil || !m.statusIsError {
		t.Fatalf("empty comment save = command:%v status:%q", command, m.statusMessage)
	}

	m.commentInput.SetValue("hello\x1b[31m red\nnext")
	preserved := m.commentInput.Value()
	save := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if save == nil || !m.saving {
		t.Fatalf("comment save = command:%v busy:%v", save, m.saving)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.action == actionNone || !m.saving {
		t.Fatal("Escape dismissed an in-flight comment write")
	}
	if reload := m.Update(save()); reload != nil || m.action == actionNone || m.saving || !m.statusIsError {
		t.Fatalf("refused write = reload:%v action:%v busy:%v error:%v", reload, m.action, m.saving, m.statusIsError)
	}
	if got := m.commentInput.Value(); got != preserved {
		t.Fatalf("refused write changed input %q", got)
	}
	if strings.Contains(m.statusMessage, "\x1b") || strings.Contains(m.statusMessage, "\n") {
		t.Fatalf("unsafe error reached status %q", m.statusMessage)
	}

	st.addErr = nil
	save = m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	reload := m.Update(save())
	if reload == nil || m.action != actionNone || m.saving || !m.ConsumeChanged() || m.ConsumeChanged() {
		t.Fatalf("acknowledged write = reload:%v action:%v busy:%v", reload, m.action, m.saving)
	}
	if st.addedTask != "task-7" || st.addedAuthor != "alice" || st.addedBody != "hello[31m red\nnext" {
		t.Fatalf("AddComment args = task:%q author:%q body:%q", st.addedTask, st.addedAuthor, st.addedBody)
	}
	if command := m.Update(reload()); command != nil || len(m.comments) != 1 || m.comments[0].Body != "hello[31m red\nnext" {
		t.Fatalf("post-write reload = command:%v comments:%+v", command, m.comments)
	}
}

func TestCommentDeleteRequiresConfirmationAndEscapeDisarms(t *testing.T) {
	st := &actionStore{comments: []store.Comment{{ID: 4, Author: "a", Body: "first"}, {ID: 9, Author: "b", Body: "second"}}}
	m := openActionModel(t, st)
	m.Update(key('d'))
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.confirm || m.selection != 1 {
		t.Fatalf("delete selection = confirm:%v selection:%d", m.confirm, m.selection)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.confirm || m.action == actionNone {
		t.Fatal("first Escape did not disarm only the confirmation")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	remove := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if remove == nil || st.deletedID != 0 {
		t.Fatalf("delete command = %v, store called synchronously with %d", remove, st.deletedID)
	}
	reload := m.Update(remove())
	if reload == nil || st.deletedID != 9 || m.action != actionNone {
		t.Fatalf("delete result = reload:%v id:%d action:%v", reload, st.deletedID, m.action)
	}
	m.Update(reload())
	if len(m.comments) != 1 || m.comments[0].ID != 4 {
		t.Fatalf("comments after delete = %+v", m.comments)
	}
}

func TestRefreshDisarmsConfirmedCommentAndLinkDeletion(t *testing.T) {
	t.Run("comment", func(t *testing.T) {
		st := &actionStore{comments: []store.Comment{{ID: 4, Body: "first"}, {ID: 9, Body: "second"}}}
		m := openActionModel(t, st)
		m.Update(key('d'))
		m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

		m.Update(detailLoadedMsg{
			taskID: m.task.ID, generation: m.generation,
			comments: []store.Comment{{ID: 9, Body: "second"}, {ID: 4, Body: "first"}},
		})
		if m.confirm || m.action != actionDeleteComment || !strings.Contains(m.statusMessage, "confirm again") {
			t.Fatalf("refresh retained confirmation: action=%v confirm=%v status=%q", m.action, m.confirm, m.statusMessage)
		}
		if command := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); command != nil || !m.confirm || st.deletedID != 0 {
			t.Fatalf("first Enter after refresh mutated: command=%v confirm=%v deleted=%d", command, m.confirm, st.deletedID)
		}
	})

	t.Run("link", func(t *testing.T) {
		st := &actionStore{links: store.TaskLinks{Blocks: []board.Task{
			{ID: "task-8", Seq: 8}, {ID: "task-9", Seq: 9},
		}}}
		m := openActionModel(t, st)
		m.Update(key('u'))
		m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

		m.Update(detailLoadedMsg{
			taskID: m.task.ID, generation: m.generation,
			links: store.TaskLinks{Blocks: []board.Task{
				{ID: "task-9", Seq: 9}, {ID: "task-8", Seq: 8},
			}},
		})
		if m.confirm || m.action != actionDeleteLink || !strings.Contains(m.statusMessage, "confirm again") {
			t.Fatalf("refresh retained confirmation: action=%v confirm=%v status=%q", m.action, m.confirm, m.statusMessage)
		}
		if command := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); command != nil || !m.confirm || st.unlinkA != "" {
			t.Fatalf("first Enter after refresh mutated: command=%v confirm=%v unlink=%q", command, m.confirm, st.unlinkA)
		}
	})

	for _, test := range []struct {
		name      string
		store     actionStore
		key       rune
		loaded    detailLoadedMsg
		want      string
		wantError bool
	}{
		{
			name: "comments disappear", store: actionStore{comments: []store.Comment{{ID: 4}}}, key: 'd',
			loaded: detailLoadedMsg{}, want: "none remain",
		},
		{
			name: "comments unavailable", store: actionStore{comments: []store.Comment{{ID: 4}}}, key: 'd',
			loaded: detailLoadedMsg{commentsErr: errors.New("read failed")}, want: "comments unavailable", wantError: true,
		},
		{
			name: "links unavailable", store: actionStore{links: store.TaskLinks{Blocks: []board.Task{{ID: "task-8"}}}}, key: 'u',
			loaded: detailLoadedMsg{linksErr: errors.New("read failed")}, want: "links unavailable", wantError: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := openActionModel(t, &test.store)
			m.Update(key(test.key))
			m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			test.loaded.taskID, test.loaded.generation = m.task.ID, m.generation
			m.Update(test.loaded)
			if m.action != actionNone || m.confirm || !strings.Contains(m.statusMessage, test.want) || m.statusIsError != test.wantError {
				t.Fatalf("refresh cancellation = action:%v confirm:%v error:%v status:%q", m.action, m.confirm, m.statusIsError, m.statusMessage)
			}
		})
	}
}

func TestDirectionalLinkAddAndConfirmedUnlink(t *testing.T) {
	st := &actionStore{linkErr: errors.New("cycle refused")}
	m := openActionModel(t, st)
	m.Update(key('b'))
	m.linkInput.SetValue("2")
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.currentBlocks {
		t.Fatal("Tab did not switch link direction")
	}
	link := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if link == nil {
		t.Fatal("link input did not start a write")
	}
	if reload := m.Update(link()); reload != nil || m.action == actionNone || m.linkInput.Value() != "2" || !m.statusIsError {
		t.Fatalf("refused link = reload:%v action:%v target:%q error:%v", reload, m.action, m.linkInput.Value(), m.statusIsError)
	}
	if st.blockerRef != "2" || st.blockedRef != "task-7" {
		t.Fatalf("blocked-by direction = %q -> %q", st.blockerRef, st.blockedRef)
	}

	st.linkErr = nil
	link = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	reload := m.Update(link())
	if reload == nil || m.action != actionNone || !strings.Contains(m.statusMessage, "#2 now blocks #7") {
		t.Fatalf("link success = reload:%v action:%v status:%q", reload, m.action, m.statusMessage)
	}
	m.Update(reload())

	st.links = store.TaskLinks{
		Blocks:    []board.Task{{ID: "task-8", Seq: 8, Status: board.StatusTodo}},
		BlockedBy: []board.Task{{ID: "task-2", Seq: 2, Status: board.StatusDoing}},
	}
	m.links = st.links
	m.Update(key('u'))
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	unlink := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if unlink == nil {
		t.Fatal("unlink confirmation did not start write")
	}
	reload = m.Update(unlink())
	if reload == nil || st.unlinkA != "task-7" || st.unlinkB != "task-2" {
		t.Fatalf("Unlink args = %q, %q reload:%v", st.unlinkA, st.unlinkB, reload)
	}
}

func TestStaleMutationCannotCrossDetailSession(t *testing.T) {
	st := &actionStore{}
	m := openActionModel(t, st)
	m.Update(key('c'))
	m.commentInput.SetValue("old session")
	save := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if save == nil {
		t.Fatal("old session save was nil")
	}
	newLoad := m.Open(board.Task{ID: "task-8", Seq: 8, Title: "Eight", Status: board.StatusDoing})
	if command := m.Update(save()); command != nil || m.TaskID() != "task-8" || m.ConsumeChanged() {
		t.Fatalf("stale mutation changed new session: command:%v task:%q", command, m.TaskID())
	}
	m.Update(newLoad())
	if len(m.comments) != 0 {
		t.Fatalf("new session adopted old task comments: %+v", m.comments)
	}
}

func TestMutationDuringEnrichmentQueuesFreshSuccessor(t *testing.T) {
	st := &actionStore{}
	m := New(st, "alice")
	initial := m.Open(board.Task{ID: "task-7", Seq: 7, Title: "Seven", Status: board.StatusTodo})
	if initial == nil || !m.loading {
		t.Fatal("initial detail load did not start")
	}
	m.Update(key('c'))
	m.commentInput.SetValue("new comment")
	save := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if save == nil {
		t.Fatal("comment write did not start")
	}
	if command := m.Update(save()); command != nil || !m.reloadPending || !m.loading {
		t.Fatalf("write during load = command:%v pending:%v loading:%v", command, m.reloadPending, m.loading)
	}
	successor := m.Update(detailLoadedMsg{
		taskID: "task-7", generation: m.generation,
		comments: []store.Comment{{ID: 99, TaskID: "task-7", Body: "stale"}},
	})
	if successor == nil || len(m.comments) != 0 || m.reloadPending || !m.loading {
		t.Fatalf("stale load adoption = successor:%v comments:%+v pending:%v loading:%v", successor, m.comments, m.reloadPending, m.loading)
	}
	if command := m.Update(successor()); command != nil || len(m.comments) != 1 || m.comments[0].Body != "new comment" {
		t.Fatalf("fresh successor = command:%v comments:%+v", command, m.comments)
	}
}

func TestCardDetailActionGolden(t *testing.T) {
	m := openActionModel(t, &actionStore{})
	m.Update(key('c'))
	m.commentInput.SetValue("First line\nSecond line")
	m.rebuildBody()
	lines := strings.Split(ansi.Strip(m.View(60, 20)), "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	golden.RequireEqual(t, strings.Trim(strings.Join(lines, "\n"), "\n")+"\n")
}

func TestCompletionGateAndActionViewsAreTerminalSafe(t *testing.T) {
	gate := renderCompletionGate(
		board.Task{Blocked: true, Checks: []board.Check{{Text: "open"}}},
		store.TaskLinks{BlockedBy: []board.Task{
			{ID: "one", Seq: 1, Status: board.StatusTodo},
			{ID: "two", Seq: 2, Status: board.StatusDone},
		}}, false, nil,
	)
	for _, want := range []string{"1 of 1 checklist", "flagged blocked", "1 open linked blocker", "[#1 todo]"} {
		if !strings.Contains(gate, want) {
			t.Errorf("completion gate missing %q: %s", want, gate)
		}
	}
	if clear := renderCompletionGate(board.Task{}, store.TaskLinks{}, false, nil); clear != "completion gate  clear" {
		t.Fatalf("clear gate = %q", clear)
	}
	if unknown := renderCompletionGate(board.Task{}, store.TaskLinks{}, true, nil); unknown != "completion gate  unknown: linked blockers loading" {
		t.Fatalf("loading gate = %q", unknown)
	}
	if unknown := renderCompletionGate(board.Task{}, store.TaskLinks{}, false, errors.New("broken")); unknown != "completion gate  unknown: linked blockers unavailable" {
		t.Fatalf("failed gate = %q", unknown)
	}

	comments := make([]store.Comment, 20)
	for i := range comments {
		comments[i] = store.Comment{ID: i + 1, Author: "bad\x1b[31m\a", Body: strings.Repeat("wide 界 ", 20)}
	}
	m := openActionModel(t, &actionStore{comments: comments})
	m.Resize(20, 8)
	m.Update(key('d'))
	for range 19 {
		m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	view := m.View(20, 8)
	if strings.Contains(view, "\x1b[31m") || !strings.Contains(ansi.Strip(view), "c20") {
		t.Fatalf("unsafe or invisible selected action:\n%s", view)
	}
	for _, line := range strings.Split(view, "\n") {
		if ansi.StringWidth(line) > 20 {
			t.Fatalf("action line wider than terminal: %q", line)
		}
	}
	if start, end := selectionWindow(20, 19, 3); start != 17 || end != 20 {
		t.Fatalf("selection window = %d:%d", start, end)
	}
}

func TestActionEdgeStatesAndKeyboardEditing(t *testing.T) {
	readOnly := New(stubReader{}, "u")
	readOnly.Open(board.Task{ID: "read-only", Status: board.StatusTodo})
	if command := readOnly.beginAction(actionAddComment); command != nil || readOnly.action != actionNone {
		t.Fatalf("read-only action = command:%v action:%v", command, readOnly.action)
	}
	if IsMutationMessage(struct{}{}) || !IsMutationMessage(mutationCompletedMsg{}) {
		t.Fatal("mutation message classifier returned the wrong ownership")
	}

	st := &actionStore{}
	m := New(st, "u")
	load := m.Open(board.Task{ID: "task", Seq: 1, Status: board.StatusTodo})
	m.beginAction(actionDeleteComment)
	if m.action != actionNone || m.statusMessage != "comments are still loading" {
		t.Fatalf("loading comments action = action:%v status:%q", m.action, m.statusMessage)
	}
	m.beginAction(actionDeleteLink)
	if m.action != actionNone || m.statusMessage != "blocker links are still loading" {
		t.Fatalf("loading links action = action:%v status:%q", m.action, m.statusMessage)
	}
	m.Update(load())
	m.commentsErr = errors.New("comments failed")
	m.beginAction(actionDeleteComment)
	if !m.statusIsError || !strings.Contains(m.statusMessage, "unavailable") {
		t.Fatalf("comments error action = error:%v status:%q", m.statusIsError, m.statusMessage)
	}
	m.commentsErr = nil
	m.linksErr = errors.New("links failed")
	m.beginAction(actionDeleteLink)
	if !m.statusIsError || !strings.Contains(m.statusMessage, "unavailable") {
		t.Fatalf("links error action = error:%v status:%q", m.statusIsError, m.statusMessage)
	}
	m.linksErr = nil
	m.beginAction(actionDeleteComment)
	if m.statusMessage != "no comments to delete" {
		t.Fatalf("empty comments action status = %q", m.statusMessage)
	}
	m.beginAction(actionDeleteLink)
	if m.statusMessage != "no blocker links to remove" {
		t.Fatalf("empty links action status = %q", m.statusMessage)
	}

	m.beginAction(actionAddComment)
	for _, char := range "ab" {
		m.updateActionKey(tea.KeyPressMsg{Code: char, Text: string(char)})
	}
	if m.commentInput.Value() != "ab" {
		t.Fatalf("comment keyboard input = %q", m.commentInput.Value())
	}
	m.cancelAction()
	if m.action != actionNone {
		t.Fatal("plain action cancellation did not return to detail")
	}

	m.beginAction(actionAddLink)
	for _, char := range "42" {
		m.updateActionKey(tea.KeyPressMsg{Code: char, Text: string(char)})
	}
	if m.linkInput.Value() != "42" {
		t.Fatalf("link keyboard input = %q", m.linkInput.Value())
	}
	for _, key := range []tea.KeyPressMsg{
		{Code: tea.KeyLeft}, {Code: tea.KeyRight}, {Code: tea.KeyTab, Mod: tea.ModShift},
	} {
		m.updateActionKey(key)
	}
	m.linkInput.SetValue("")
	if command := m.startAddLink(); command != nil || !m.statusIsError {
		t.Fatalf("empty link = command:%v error:%v", command, m.statusIsError)
	}
	m.linkInput.SetValue("2")
	m.currentBlocks = true
	link := m.startAddLink()
	if link == nil {
		t.Fatal("outgoing link command was nil")
	}
	result := link().(mutationCompletedMsg)
	if st.blockerRef != "task" || st.blockedRef != "2" || result.err != nil {
		t.Fatalf("outgoing link = %q -> %q, %v", st.blockerRef, st.blockedRef, result.err)
	}
	m.saving = false
	m.cancelAction()

	if command := m.startDeleteComment(); command != nil {
		t.Fatalf("empty direct comment delete returned %v", command)
	}
	if command := m.startDeleteLink(); command != nil {
		t.Fatalf("empty direct link delete returned %v", command)
	}
	if got := taskActionRef(board.Task{ID: "legacy"}); got != "legacy" {
		t.Fatalf("legacy task ref = %q", got)
	}
	if start, end := selectionWindow(0, 9, 3); start != 0 || end != 0 {
		t.Fatalf("empty selection window = %d:%d", start, end)
	}
	if start, end := selectionWindow(2, -3, 99); start != 0 || end != 2 {
		t.Fatalf("clamped selection window = %d:%d", start, end)
	}

	footerModel := Model{}
	for _, test := range []struct {
		mode  actionMode
		width int
		want  string
	}{
		{actionNone, 80, "c add"},
		{actionNone, 30, "esc close"},
		{actionNone, 10, "e c d"},
		{actionAddComment, 80, "add comment"},
		{actionDeleteComment, 80, "enter delete"},
		{actionAddLink, 80, "direction"},
		{actionDeleteLink, 80, "enter remove"},
	} {
		footerModel.action = test.mode
		if got := footerModel.actionFooter(test.width); !strings.Contains(got, test.want) {
			t.Errorf("footer(%v,%d) = %q, want %q", test.mode, test.width, got, test.want)
		}
	}
	footerModel.confirm = true
	if got := footerModel.actionFooter(80); !strings.Contains(got, "confirm") {
		t.Fatalf("confirm footer = %q", got)
	}
	footerModel.saving = true
	if got := footerModel.actionFooter(80); !strings.Contains(got, "progress") {
		t.Fatalf("saving footer = %q", got)
	}
	footerModel.saving = false
	footerModel.action = actionNone
	footerModel.statusMessage = "visible result"
	if got := footerModel.actionFooter(80); got != "status: visible result" {
		t.Fatalf("status footer = %q", got)
	}
	footerModel.statusIsError = true
	if got := footerModel.actionFooter(80); got != "error: visible result" {
		t.Fatalf("error footer = %q", got)
	}
	footerModel.open = true
	footerModel.bodyLines = []string{"one", "two"}
	footerModel.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if footerModel.statusMessage != "" || footerModel.statusIsError {
		t.Fatal("normal detail input did not clear the prior status line")
	}
	footerModel.action = actionAddComment
	footerModel.saving = true
	footerModel.updateActionKey(key('z'))
}

func TestDeleteFailuresKeepSelectorsAndInputs(t *testing.T) {
	st := &actionStore{
		comments:  []store.Comment{{ID: 4, Body: "keep"}},
		links:     store.TaskLinks{Blocks: []board.Task{{ID: "other", Seq: 2, Status: board.StatusTodo}}},
		deleteErr: errors.New("delete refused"),
		unlinkErr: errors.New("unlink refused"),
	}
	m := openActionModel(t, st)
	m.beginAction(actionDeleteComment)
	m.updateDeleteKey("enter")
	remove := m.updateDeleteKey("enter")
	if remove == nil {
		t.Fatal("comment delete command was nil")
	}
	if reload := m.Update(remove()); reload != nil || m.action != actionDeleteComment || !m.statusIsError {
		t.Fatalf("failed comment delete = reload:%v action:%v error:%v", reload, m.action, m.statusIsError)
	}
	m.cancelAction() // disarm confirmation retained across the refused write.
	m.cancelAction()

	m.links = st.links
	m.beginAction(actionDeleteLink)
	m.updateDeleteKey("enter")
	remove = m.updateDeleteKey("enter")
	if remove == nil {
		t.Fatal("link delete command was nil")
	}
	if reload := m.Update(remove()); reload != nil || m.action != actionDeleteLink || !m.statusIsError {
		t.Fatalf("failed unlink = reload:%v action:%v error:%v", reload, m.action, m.statusIsError)
	}
	m.saving = true
	m.cancelAction()
	if m.action != actionDeleteLink || !strings.Contains(m.statusMessage, "progress") {
		t.Fatalf("busy cancellation = action:%v status:%q", m.action, m.statusMessage)
	}
}
