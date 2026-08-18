package issueimport

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/ai"
	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/forge"
	"github.com/RandomCodeSpace/kb/internal/store"
)

type fakeStore struct {
	added []board.Task
	err   error
}

func (s *fakeStore) AddTask(_ string, task board.Task) (board.Task, error) {
	if s.err != nil {
		return board.Task{}, s.err
	}
	s.added = append(s.added, task)
	task.ID = "created"
	return task, nil
}

type fakeBackend struct {
	sources      []store.ForgeSource
	sourcesErr   error
	preview      forge.Preview
	previewErr   error
	recordErr    error
	recordCalls  int
	previewCalls int
	lastRequest  forge.PreviewRequest
	store        *fakeStore
}

func (b *fakeBackend) Sources(string) ([]store.ForgeSource, error) { return b.sources, b.sourcesErr }
func (b *fakeBackend) Preview(_ context.Context, _ string, request forge.PreviewRequest) (forge.Preview, error) {
	b.previewCalls++
	b.lastRequest = request
	return b.preview, b.previewErr
}
func (b *fakeBackend) CreateTask(user, _ string, task board.Task, _ forge.LinkInput) (board.Task, error) {
	b.recordCalls++
	if b.recordErr != nil {
		return board.Task{}, b.recordErr
	}
	return b.store.AddTask(user, task)
}

func key(value string) tea.KeyPressMsg { return tea.KeyPressMsg{Code: rune(value[0]), Text: value} }

func openModel(t *testing.T, backend *fakeBackend, st *fakeStore) Model {
	t.Helper()
	backend.store = st
	m := New(st, backend, "alice", context.Background())
	command := m.Open()
	if command == nil {
		t.Fatal("Open returned nil")
	}
	m.Update(command())
	return m
}

func TestPreviewDefaultsExactDuplicatesOffAndFuzzyOn(t *testing.T) {
	backend := &fakeBackend{
		sources: []store.ForgeSource{{Name: "primary", Kind: "github"}},
		preview: forge.Preview{Fetched: 2, Truncated: true, TotalHint: 7, Note: "rate limited", Drafts: []forge.Draft{
			{Draft: ai.Draft{Title: "exact"}, Duplicate: &forge.Duplicate{Via: "link", Title: "existing"}},
			{Draft: ai.Draft{Title: "fuzzy"}, Duplicate: &forge.Duplicate{Via: "similar", Title: "maybe"}},
		}},
	}
	m := openModel(t, backend, &fakeStore{})
	m.ref.SetValue("owner/repo")
	command := m.startPreview()
	if command == nil || m.operation != "preview" {
		t.Fatal("preview did not start")
	}
	m.Update(command())
	if m.stage != stageReview || len(m.rows) != 2 || m.rows[0].include || !m.rows[1].include {
		t.Fatalf("review defaults = %+v", m.rows)
	}
	if backend.lastRequest.Source != "primary" || backend.lastRequest.Ref != "owner/repo" || backend.lastRequest.Max != defaultMax {
		t.Fatalf("preview request = %+v", backend.lastRequest)
	}
	view := m.View(70, 20)
	for _, want := range []string{"results truncated", "rate limited", "duplicate via link", "duplicate via similar"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view omitted %q:\n%s", want, view)
		}
	}
}

func TestAtomicCardProvenanceRetryDoesNotDuplicateCard(t *testing.T) {
	backend := &fakeBackend{
		sources: []store.ForgeSource{{Name: "primary", Kind: "github"}},
		preview: forge.Preview{Drafts: []forge.Draft{{
			Draft: ai.Draft{Title: "import me", Prio: 2, Checks: []ai.DraftCheck{{Text: "verify"}}},
			Link:  "github#93", ExternalKey: "github:github.com/acme/kb#93", URL: "https://github.com/acme/kb/issues/93",
		}}},
		recordErr: errors.New("disk unavailable"),
	}
	st := &fakeStore{}
	m := openModel(t, backend, st)
	m.ref.SetValue("acme/kb")
	m.Update(m.startPreview()())
	command := m.startCreate()
	m.Update(command())
	if len(st.added) != 0 || m.rows[0].created || m.ConsumeChanged() {
		t.Fatalf("failed provenance state = added %d row %+v", len(st.added), m.rows[0])
	}
	backend.recordErr = nil
	command = m.startCreate()
	m.Update(command())
	if len(st.added) != 1 || !m.rows[0].created || backend.recordCalls != 2 || !m.ConsumeChanged() {
		t.Fatalf("retry duplicated or failed: added=%d created=%t records=%d", len(st.added), m.rows[0].created, backend.recordCalls)
	}
}

func TestPreviewCancellationAndReopenRejectStaleResults(t *testing.T) {
	backend := &fakeBackend{sources: []store.ForgeSource{{Name: "primary"}}, preview: forge.Preview{Drafts: []forge.Draft{{Draft: ai.Draft{Title: "stale"}}}}}
	m := openModel(t, backend, &fakeStore{})
	m.ref.SetValue("acme/kb")
	command := m.startPreview()
	stale := command()
	m.Update(key("e"))
	if m.operation != "preview" {
		t.Fatal("ordinary input cancelled active preview")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.operation != "" || m.status != "preview cancelled" {
		t.Fatalf("cancel state = %q %q", m.operation, m.status)
	}
	m.Update(stale)
	if len(m.rows) != 0 || m.stage != stageInput {
		t.Fatal("cancelled preview mutated review")
	}
	m.Open()
	m.Update(stale)
	if len(m.rows) != 0 {
		t.Fatal("prior session result mutated reopened overlay")
	}
}

func TestInputNavigationErrorsAndTerminalSafety(t *testing.T) {
	backend := &fakeBackend{sources: []store.ForgeSource{{Name: "one"}, {Name: "two"}}}
	m := openModel(t, backend, &fakeStore{})
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.focus != 1 {
		t.Fatalf("tab focus = %d", m.focus)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	if m.max != defaultMax+1 {
		t.Fatalf("max = %d", m.max)
	}
	m.focus = 0
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	if m.sourceName() != "two" {
		t.Fatalf("source = %q", m.sourceName())
	}
	m.ref.SetValue("")
	m.startPreview()
	if !m.statusError || m.status != "reference required" {
		t.Fatalf("empty ref status = %q", m.status)
	}
	m.status = "bad\x1b[31m\x9b31m\nline"
	view := m.View(28, 8)
	if strings.Contains(view, "\nline") || strings.Contains(view, "\x1b[31m") || strings.Contains(view, "\x9b") {
		t.Fatalf("unsafe view:\n%s", view)
	}
	m.Close()
	if m.View(20, 5) != "" || m.Overlay("board", 20, 5) != "board" {
		t.Fatal("closed overlay rendered")
	}
}

func TestUnavailableSourcesAndCardFailureStayReviewable(t *testing.T) {
	m := openModel(t, &fakeBackend{sourcesErr: errors.New("closed")}, &fakeStore{})
	if !m.statusError || m.status != "sources unavailable" {
		t.Fatalf("source error = %q", m.status)
	}
	backend := &fakeBackend{sources: []store.ForgeSource{{Name: "primary"}}, preview: forge.Preview{Drafts: []forge.Draft{{Draft: ai.Draft{Title: "keep me"}}}}}
	st := &fakeStore{err: errors.New("refused")}
	m = openModel(t, backend, st)
	m.ref.SetValue("acme/kb")
	m.Update(m.startPreview()())
	m.Update(m.startCreate()())
	if m.rows[0].created || m.rows[0].err == "" || !m.open || m.stage != stageReview {
		t.Fatalf("failed card state = %+v", m.rows[0])
	}
	if !IsMessage(previewCompletedMsg{}) || !IsMessage(pointerActionMsg{}) || IsMessage(tea.KeyPressMsg{}) {
		t.Fatal("message classifier")
	}
}

func TestKeyboardAndReviewBranches(t *testing.T) {
	backend := &fakeBackend{sources: []store.ForgeSource{{Name: "one"}, {Name: "two"}}, preview: forge.Preview{Drafts: []forge.Draft{
		{Draft: ai.Draft{Title: "one"}}, {Draft: ai.Draft{Title: "two"}},
	}}}
	m := openModel(t, backend, &fakeStore{})
	if !m.IsOpen() {
		t.Fatal("model not open")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if m.focus != 2 {
		t.Fatalf("reverse tab focus = %d", m.focus)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	if m.max != defaultMax-1 {
		t.Fatalf("left max = %d", m.max)
	}
	m.focus = 0
	m.source = 0
	m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	if m.source != 1 {
		t.Fatalf("wrapped source = %d", m.source)
	}
	m.focus = 1
	m.applyFocus()
	m.Update(key("x"))
	if m.ref.Value() != "x" {
		t.Fatalf("text input = %q", m.ref.Value())
	}
	m.ref.SetValue("acme/kb")
	command := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if command == nil {
		t.Fatal("enter did not preview")
	}
	m.Update(command())
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.selection != 1 {
		t.Fatalf("down selection = %d", m.selection)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	m.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	if m.rows[0].include {
		t.Fatal("space did not untick")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.stage != stageInput {
		t.Fatal("escape did not return to input")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.open {
		t.Fatal("input escape did not close")
	}
}

func TestEdgeMessagesAndRows(t *testing.T) {
	disabled := New(nil, nil, "u", nil)
	if disabled.Enabled() || disabled.Open() != nil || disabled.Update(key("x")) != nil {
		t.Fatal("disabled model became active")
	}
	backend := &fakeBackend{sources: []store.ForgeSource{{Name: "primary"}}, preview: forge.Preview{Drafts: []forge.Draft{{Draft: ai.Draft{Title: "plain"}}}}}
	st := &fakeStore{}
	m := openModel(t, backend, st)
	m.ref.SetValue("acme/kb")
	m.Update(m.startPreview()())
	m.rows[0].draft.ExternalKey = ""
	command := m.startCreate()
	m.Update(command())
	if len(st.added) != 1 || m.operation != "" || !m.rows[0].created {
		t.Fatalf("plain created row = %+v operation=%q", m.rows[0], m.operation)
	}
	m.rows[0].include = false
	m.rows[0].created = false
	if m.startCreate() != nil || m.status != "nothing selected" {
		t.Fatal("empty selection did not stop")
	}
	staleCard := cardCreatedMsg{session: m.session + 1, generation: m.generation, row: 0}
	if m.finishCard(staleCard) != nil {
		t.Fatal("stale write returned command")
	}
	if safeError(&forge.Error{Message: "safe"}) != "safe" || safeError(errors.New("secret")) != "operation failed" {
		t.Fatal("error mapping")
	}
	empty := New(st, backend, "u", nil)
	if empty.sourceName() != "none" || empty.progress() != "" {
		t.Fatal("empty helpers")
	}
}

func TestReviewWindowAndRenderingBranches(t *testing.T) {
	drafts := make([]forge.Draft, 16)
	for index := range drafts {
		drafts[index] = forge.Draft{Draft: ai.Draft{Title: strings.Repeat("long", index+1)}}
	}
	backend := &fakeBackend{sources: []store.ForgeSource{{Name: "primary"}}, preview: forge.Preview{Fetched: 16, Drafts: drafts}}
	m := openModel(t, backend, &fakeStore{})
	inputView := m.View(10, 4)
	if inputView == "" || m.Overlay("board", 10, 4) == "board" {
		t.Fatal("open input did not render")
	}
	m.ref.SetValue("acme/kb")
	m.Update(m.startPreview()())
	m.selection = 15
	m.operation, m.queue, m.queuePos = "create", []int{0, 1}, 0
	view := m.View(44, 14)
	if !strings.Contains(view, "writing 1/2") || strings.Contains(view, "longlonglonglonglonglonglonglonglonglonglonglonglonglonglonglong") {
		t.Fatalf("review rendering:\n%s", view)
	}
	if start, end := rowWindow(2, 0, 5); start != 0 || end != 2 {
		t.Fatalf("small window = %d,%d", start, end)
	}
	if start, end := rowWindow(20, 19, 5); start != 15 || end != 20 {
		t.Fatalf("tail window = %d,%d", start, end)
	}
	if got := fit("abcdef", 1); got == "" {
		t.Fatal("tiny fit empty")
	}
}

func TestRemainingStateBranches(t *testing.T) {
	backend := &fakeBackend{sources: []store.ForgeSource{{Name: "primary"}}, previewErr: &forge.Error{Message: "preview refused"}}
	m := openModel(t, backend, &fakeStore{})
	m.focus = 2
	m.Update(key("9"))
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	if m.max != 10 {
		t.Fatalf("numeric/right max = %d", m.max)
	}
	m.ref.SetValue("acme/kb")
	m.Update(m.startPreview()())
	if m.status != "preview refused" || !m.statusError {
		t.Fatalf("preview error = %q", m.status)
	}
	backend.previewErr = nil
	backend.preview = forge.Preview{Fetched: 1, Drafts: []forge.Draft{{Draft: ai.Draft{Title: "one"}}}}
	m.Update(m.startPreview()())
	command := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if command == nil {
		t.Fatal("review enter did not start create")
	}
	m.operation = "preview"
	_, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.Close()
	if m.open || m.cancel != nil {
		t.Fatal("close did not cancel operation")
	}

	empty := openModel(t, &fakeBackend{}, &fakeStore{})
	if empty.sourceName() != "none" || !empty.statusError {
		t.Fatal("empty source state")
	}
	empty.stage = stageReview
	empty.rows = []row{{draft: forge.Draft{Draft: ai.Draft{Title: "done"}}, created: true}, {draft: forge.Draft{Draft: ai.Draft{Title: "pending"}}, err: "retry"}}
	empty.status, empty.statusError = "failed", true
	view := empty.View(60, 16)
	for _, want := range []string{"[created]", "retry", "error   failed"} {
		if !strings.Contains(view, want) {
			t.Fatalf("remaining view omitted %q:\n%s", want, view)
		}
	}
}

func TestPointerControlsFocusRowsAndActionsWithoutBackgroundMutation(t *testing.T) {
	backend := &fakeBackend{
		sources: []store.ForgeSource{{Name: "primary"}},
		preview: forge.Preview{Drafts: []forge.Draft{{Draft: ai.Draft{Title: "one"}}, {Draft: ai.Draft{Title: "two"}}}},
	}
	m := openModel(t, backend, &fakeStore{})
	m.ref.SetValue("acme/kb")
	handler := m.MouseHandler(80, 24)
	line, x := importVisibleTextPosition(t, m.View(80, 24), 80, 24, "ref")
	before := m.focus
	command := pointerRelease(handler, x, line)
	if command == nil || m.focus != before {
		t.Fatalf("pointer release mutated model before update: command=%v focus=%d", command, m.focus)
	}
	m.Update(command())
	if m.focus != 1 {
		t.Fatalf("pointer input focus=%d, want ref", m.focus)
	}
	m.Update(m.startPreview()())
	handler = m.MouseHandler(80, 24)
	line, x = importVisibleTextPosition(t, m.View(80, 24), 80, 24, "one")
	m.Update(pointerRelease(handler, x, line)())
	if m.rows[0].include {
		t.Fatal("pointer review row did not toggle")
	}
	m.rows[1].draft.Title = m.rows[0].draft.Title
	m.rows[1].include = true
	view := ansi.Strip(m.View(80, 24))
	occurrence := 0
	for y, text := range strings.Split(view, "\n") {
		if strings.Contains(text, m.rows[1].draft.Title) {
			occurrence++
			if occurrence == 2 {
				line, x = y+(24-len(strings.Split(view, "\n")))/2, strings.Index(text, m.rows[1].draft.Title)+(80-ansi.StringWidth(strings.Split(view, "\n")[0]))/2
				break
			}
		}
	}
	if occurrence != 2 {
		t.Fatal("duplicate review titles were not rendered")
	}
	handler = m.MouseHandler(80, 24)
	m.Update(pointerRelease(handler, x, line)())
	if m.rows[1].include {
		t.Fatal("pointer row identity followed title text instead of rendered row")
	}
	m.rows[1].include = true
	line, x = importVisibleTextPosition(t, m.View(80, 24), 80, 24, "Import")
	command = pointerRelease(handler, x, line)
	if command == nil {
		t.Fatal("import action had no hit region")
	}
	if m.operation != "" {
		t.Fatal("pointer action started work before update")
	}
	m.Update(command())
	if m.operation != "create" {
		t.Fatalf("pointer import operation=%q", m.operation)
	}

	m.stage = stageReview
	m.operation = ""
	m.selection = 0
	handler = m.MouseHandler(80, 24)
	line, x = importVisibleTextPosition(t, m.View(80, 24), 80, 24, "Back")
	m.Update(pointerRelease(handler, x, line)())
	if m.stage != stageInput {
		t.Fatal("pointer back did not return to input")
	}
}

func TestPointerControlsClipRowsAndRejectBusyOrStaleSnapshots(t *testing.T) {
	drafts := make([]forge.Draft, 20)
	for i := range drafts {
		drafts[i] = forge.Draft{Draft: ai.Draft{Title: fmt.Sprintf("issue-%d", i)}}
	}
	backend := &fakeBackend{sources: []store.ForgeSource{{Name: "primary"}}, preview: forge.Preview{Drafts: drafts}}
	m := openModel(t, backend, &fakeStore{})
	m.ref.SetValue("acme/kb")
	m.Update(m.startPreview()())
	handler := m.MouseHandler(50, 10)
	line, x := importVisibleTextPosition(t, m.View(50, 10), 50, 10, "issue-0")
	if command := pointerRelease(handler, x, line); command == nil {
		t.Fatal("visible issue control had no hit region")
	}
	if command := pointerRelease(handler, 3, 0); command != nil {
		t.Fatal("offscreen issue control activated")
	}
	m.operation = "create"
	if command := pointerRelease(handler, x, line); command != nil {
		if m.Update(command()) != nil || m.operation != "create" {
			t.Fatal("busy issue import exposed pointer action")
		}
	}
	m.operation = ""
	stale := m.MouseHandler(50, 10)
	m.Close()
	m.Open()
	if command := pointerRelease(stale, x, line); command != nil {
		m.Update(command())
		if m.stage != stageInput || m.operation != "" {
			t.Fatal("stale issue pointer mutated reopened session")
		}
	}
}

func TestPointerAndAsyncGuardsCoverInputEdges(t *testing.T) {
	backend := &fakeBackend{sources: []store.ForgeSource{{Name: "primary"}}, preview: forge.Preview{Drafts: []forge.Draft{{Draft: ai.Draft{Title: "one"}}}}}
	m := openModel(t, backend, &fakeStore{})

	// Results from a prior open must be ignored.
	if m.Update(sourcesLoadedMsg{session: m.session + 1, sources: backend.sources}) != nil {
		t.Fatal("stale source result returned a command")
	}
	if m.Update(pointerActionMsg{target: "source", session: m.session, generation: m.generation}) != nil || m.focus != 0 {
		t.Fatalf("source pointer focus = %d", m.focus)
	}
	if m.Update(pointerActionMsg{target: "max", session: m.session, generation: m.generation}) != nil || m.focus != 2 {
		t.Fatalf("max pointer focus = %d", m.focus)
	}

	// A preview cannot start without a configured source.
	m.sources = nil
	m.ref.SetValue("acme/kb")
	if m.startPreview() != nil || m.status != "configure a forge integration first" {
		t.Fatalf("empty-source preview status = %q", m.status)
	}
	m.sources = backend.sources
	preview := m.Update(pointerActionMsg{target: "import", session: m.session, generation: m.generation})
	if preview == nil || m.operation != "preview" {
		t.Fatal("pointer import did not start preview")
	}
	m.Update(preview())
	if m.stage != stageReview {
		t.Fatalf("preview stage = %d", m.stage)
	}

	// Invalid, out-of-range, and already-created rows are inert.
	m.rows[0].include = true
	m.rows[0].created = true
	for _, target := range []string{"row:nope", "row:-1", "row:9", "row:0"} {
		m.Update(pointerActionMsg{target: target, session: m.session, generation: m.generation})
	}
	if !m.rows[0].include {
		t.Fatal("invalid or created row pointer changed selection")
	}

	// A queued created row advances without invoking the backend.
	m.operation, m.queue, m.queuePos = "create", []int{0}, 0
	if command := m.nextWrite(); command != nil || m.operation != "" {
		t.Fatalf("created row retry state = operation %q command %v", m.operation, command)
	}

	// Preview state is rendered while the asynchronous command is active.
	m.stage, m.operation = stageInput, "preview"
	if view := m.View(60, 16); !strings.Contains(view, "fetching configured forge data") {
		t.Fatalf("preview progress missing:\n%s", view)
	}
	m.Close()
	if m.MouseHandler(60, 16) != nil {
		t.Fatal("closed import overlay exposed a pointer handler")
	}
}

func importVisibleTextPosition(t *testing.T, view string, width, height int, needle string) (int, int) {
	t.Helper()
	frameWidth, frameHeight := ansi.StringWidth(strings.Split(ansi.Strip(view), "\n")[0]), len(strings.Split(ansi.Strip(view), "\n"))
	xOffset := max((width-frameWidth)/2, 0)
	yOffset := max((height-frameHeight)/2, 0)
	for y, line := range strings.Split(ansi.Strip(view), "\n") {
		if x := strings.Index(line, needle); x >= 0 {
			return y + yOffset, x + xOffset
		}
	}
	t.Fatalf("visible import control %q missing:\n%s", needle, ansi.Strip(view))
	return 0, 0
}

func pointerRelease(handler func(tea.MouseMsg) tea.Cmd, x, y int) tea.Cmd {
	handler(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	return handler(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
}
