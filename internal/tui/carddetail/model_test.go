package carddetail

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
)

type stubReader struct {
	comments     []store.Comment
	links        store.TaskLinks
	tombstone    store.Tombstone
	found        bool
	commentsErr  error
	linksErr     error
	tombstoneErr error
}

func (s stubReader) Comments(string, string) ([]store.Comment, error) {
	return s.comments, s.commentsErr
}

func (s stubReader) TaskLinks(string, string) (store.TaskLinks, error) {
	return s.links, s.linksErr
}

func (s stubReader) Tombstone(string, string) (store.Tombstone, bool, error) {
	return s.tombstone, s.found, s.tombstoneErr
}

func fullTask() board.Task {
	return board.Task{
		ID: "task-1", Seq: 7, Emoji: "🧭", Title: "Map it", Desc: "## Plan\n- **first**\nhttps://example.com",
		Status: board.StatusCancelled, Blocked: true, Prio: 1, Due: "2026-08-19", Effort: "M",
		Tags:   []string{"type::feature", "link::github#86", "link::github#86", "link::"},
		Checks: []board.Check{{Text: "done", Done: true}, {Text: "left"}},
	}
}

func TestModelLoadsAndRendersFullDetail(t *testing.T) {
	stamp := time.Date(2026, time.August, 17, 12, 0, 0, 0, time.UTC)
	reader := stubReader{
		comments: []store.Comment{{ID: 3, Author: "alice", Body: "looks **good**", CreatedAt: stamp}},
		links: store.TaskLinks{
			Blocks:    []board.Task{{Seq: 9, Status: board.StatusTodo}},
			BlockedBy: []board.Task{{ID: "legacy", Status: board.StatusDone}},
		},
		tombstone: store.Tombstone{TaskID: "task-1", Reason: "superseded", KilledAt: stamp.Format(time.RFC3339Nano)},
		found:     true,
	}
	m := New(reader, "alice")
	if m.IsOpen() || m.TaskID() != "" || m.View(80, 24) != "" {
		t.Fatal("new detail pane was not closed")
	}
	command := m.Open(fullTask())
	if command == nil || !m.IsOpen() || m.TaskID() != "task-1" || !m.loading {
		t.Fatalf("open state = %+v, command nil=%v", m, command == nil)
	}
	if body := ansi.Strip(m.renderBody(72)); !strings.Contains(body, "loading comments and context") {
		t.Fatalf("loading body:\n%s", body)
	}
	m.Update(command())
	body := ansi.Strip(m.renderBody(72))
	for _, want := range []string{
		"🧭 Map it  #7", "status cancelled", "priority 1", "due 2026-08-19", "effort M", "blocked",
		"[type::feature]", "[github#86]", "killed 17 Aug 2026", "superseded", "Plan", "first",
		"checklist", "☑ done", "☐ left", "blocks      [#9 todo]", "blocked by  [legacy done]",
		"comments  1", "c3  alice  17 Aug 2026", "looks", "good",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("detail body missing %q:\n%s", want, body)
		}
	}
	if strings.Count(body, "[github#86]") != 1 {
		t.Errorf("link chips were not deduplicated:\n%s", body)
	}

	view := m.View(80, 20)
	for _, line := range strings.Split(view, "\n") {
		if ansi.StringWidth(line) > 80 {
			t.Fatalf("overlay line wider than terminal: %d: %q", ansi.StringWidth(line), line)
		}
	}
}

func TestModelHandlesErrorsStaleLoadsScrollAndClose(t *testing.T) {
	loadErr := errors.New("comments broke")
	m := New(stubReader{commentsErr: loadErr, linksErr: errors.New("links broke")}, "u")
	command := m.Open(board.Task{ID: "current", Title: "Current", Status: board.StatusTodo})
	m.Update(detailLoadedMsg{taskID: "stale", comments: []store.Comment{{ID: 1}}})
	if !m.loading {
		t.Fatal("stale result changed loading state")
	}
	m.Update(command())
	if m.loading || !errors.Is(m.commentsErr, loadErr) || m.linksErr == nil {
		t.Fatalf("load error state = loading %v, comments %v, links %v", m.loading, m.commentsErr, m.linksErr)
	}
	if body := ansi.Strip(m.renderBody(40)); !strings.Contains(body, "comments error:") || !strings.Contains(body, "blocker links error:") {
		t.Fatalf("error body:\n%s", body)
	}

	for _, key := range []tea.KeyPressMsg{
		{Code: tea.KeyDown}, {Code: 'j'}, {Code: tea.KeyPgDown},
		{Code: tea.KeyUp}, {Code: 'k'}, {Code: tea.KeyPgUp},
	} {
		m.Update(key)
	}
	m.scroll = 10
	m.Update(tea.KeyPressMsg{Code: tea.KeyHome})
	if m.scroll != 0 {
		t.Fatalf("home scroll = %d", m.scroll)
	}
	m.scroll = 10
	m.Update(tea.KeyPressMsg{Code: 'g'})
	if m.scroll != 0 || scrollAmount("pgdown") != 8 || scrollAmount("down") != 1 {
		t.Fatal("scroll controls returned the wrong amount")
	}

	m.Close()
	if m.IsOpen() || m.TaskID() != "" || m.Update(command()) != nil {
		t.Fatal("closed pane accepted a late load")
	}
}

func TestModelRendersIndependentEnrichmentResults(t *testing.T) {
	stamp := time.Date(2026, time.August, 17, 12, 0, 0, 0, time.UTC)
	task := board.Task{ID: "task", Title: "Task", Status: board.StatusTodo}

	commentsFailed := New(stubReader{
		comments:    []store.Comment{{ID: 8, Author: "partial", Body: "must not render", CreatedAt: stamp}},
		commentsErr: errors.New("comments unavailable"),
		links:       store.TaskLinks{Blocks: []board.Task{{Seq: 9, Status: board.StatusDoing}}},
		tombstone:   store.Tombstone{TaskID: "task", Reason: "replaced", KilledAt: stamp.Format(time.RFC3339Nano)},
		found:       true,
	}, "u")
	commentsFailed.Update(commentsFailed.Open(task)())
	body := ansi.Strip(commentsFailed.renderBody(60))
	for _, want := range []string{"blocks      [#9 doing]", "killed 17 Aug 2026", "comments error: comments unavailable"} {
		if !strings.Contains(body, want) {
			t.Errorf("comments failure hid %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "comments  none") || strings.Contains(body, "must not render") {
		t.Fatalf("failed comments read rendered a successful comments state:\n%s", body)
	}

	contextFailed := New(stubReader{
		comments:     []store.Comment{{ID: 1, Author: "alice", Body: "available", CreatedAt: stamp}},
		linksErr:     errors.New("links unavailable"),
		tombstoneErr: errors.New("killed unavailable"),
	}, "u")
	contextFailed.Update(contextFailed.Open(task)())
	body = ansi.Strip(contextFailed.renderBody(60))
	for _, want := range []string{"available", "blocker links error: links unavailable", "killed context error: killed unavailable"} {
		if !strings.Contains(body, want) {
			t.Errorf("context failure hid %q:\n%s", want, body)
		}
	}
}

func TestModelRejectsStaleLoadForReopenedTask(t *testing.T) {
	m := New(stubReader{}, "u")
	task := board.Task{ID: "same", Title: "Same", Status: board.StatusTodo}
	first := m.Open(task)
	second := m.Open(task)

	m.Update(first())
	if !m.loading {
		t.Fatal("stale same-task result changed loading state")
	}
	m.Update(second())
	if m.loading {
		t.Fatal("current same-task result did not finish loading")
	}
}

type countingDetailReader struct{ loads int }

func (r *countingDetailReader) Comments(string, string) ([]store.Comment, error) {
	r.loads++
	return []store.Comment{{ID: r.loads, Author: "load", Body: fmt.Sprintf("version %d", r.loads)}}, nil
}

func (*countingDetailReader) TaskLinks(string, string) (store.TaskLinks, error) {
	return store.TaskLinks{}, nil
}

func (*countingDetailReader) Tombstone(string, string) (store.Tombstone, bool, error) {
	return store.Tombstone{}, false, nil
}

func TestRefreshCoalescesEnrichmentLoads(t *testing.T) {
	reader := &countingDetailReader{}
	m := New(reader, "u")
	first := m.Open(board.Task{ID: "same", Title: "first", Status: board.StatusTodo})
	if command := m.Refresh(board.Task{ID: "same", Title: "second", Status: board.StatusDoing}); command != nil {
		t.Fatal("refresh overlapped the active enrichment load")
	}
	if command := m.Refresh(board.Task{ID: "same", Title: "latest", Status: board.StatusDone}); command != nil {
		t.Fatal("second refresh overlapped the active enrichment load")
	}

	successor := m.Update(first())
	if successor == nil || !m.loading || m.reloadPending || len(m.comments) != 0 {
		t.Fatalf("coalesced first result = loading %v pending %v comments %v command %v", m.loading, m.reloadPending, m.comments, successor)
	}
	m.Update(successor())
	if m.loading || reader.loads != 2 || m.task.Title != "latest" || len(m.comments) != 1 || m.comments[0].Body != "version 2" {
		t.Fatalf("coalesced successor = model %+v loads %d", m, reader.loads)
	}

	late := m.Refresh(board.Task{ID: "same", Title: "closed", Status: board.StatusDone})
	m.Close()
	if command := m.Update(late()); command != nil || m.IsOpen() {
		t.Fatal("closed detail restarted a pending refresh")
	}
}

func TestNilReaderAndRenderingHelpers(t *testing.T) {
	m := New(nil, "u")
	task := board.Task{ID: "id", Title: "Bare", Status: board.StatusTodo, Prio: 3}
	if command := m.Open(task); command != nil || m.loading {
		t.Fatalf("nil-reader open = command %v loading %v", command, m.loading)
	}
	body := ansi.Strip(m.renderBody(40))
	if !strings.Contains(body, "comments  none") {
		t.Fatalf("nil-reader body:\n%s", body)
	}
	if got := m.View(1, 1); got == "" {
		t.Fatal("tiny overlay was empty")
	}

	if got := regularTags([]string{"a", "link::x"}); len(got) != 1 || got[0] != "[a]" {
		t.Fatalf("regularTags = %v", got)
	}
	if got := importLinks([]string{"link:: x ", "link::x", "link::"}); len(got) != 1 || got[0] != "[x]" {
		t.Fatalf("importLinks = %v", got)
	}
	if got := killedContext(store.Tombstone{Reason: "why", KilledAt: "bad"}); got != "killed bad\nwhy" {
		t.Fatalf("killedContext invalid date = %q", got)
	}
	if got := renderChecklist(nil); got != "checklist" {
		t.Fatalf("empty checklist = %q", got)
	}
	if got := renderTaskLinks(store.TaskLinks{}); got != "" {
		t.Fatalf("empty task links = %q", got)
	}
	if got := taskChips([]board.Task{{ID: "id", Status: board.StatusDoing}}); got != "[id doing]" {
		t.Fatalf("fallback task chip = %q", got)
	}
	if got := renderComments([]store.Comment{{ID: 1, Author: "a", Body: "body"}}, 0); !strings.Contains(ansi.Strip(got), "1 Jan 0001") {
		t.Fatalf("zero-time comment = %q", ansi.Strip(got))
	}
	if got := safeText("ok\x1b[31m red\a\nnext", true); got != "ok red\nnext" {
		t.Fatalf("safeText = %q", got)
	}
}

func TestViewClampsScrollToContent(t *testing.T) {
	m := New(nil, "u")
	task := fullTask()
	task.Desc = strings.Repeat("line\n", 40)
	m.Resize(40, 10)
	m.Open(task)
	for range 100 {
		m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	}
	if m.scroll != m.maxScroll() {
		t.Fatalf("stored scroll = %d, max = %d", m.scroll, m.maxScroll())
	}
	before := m.scroll
	m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	if m.scroll != before-1 {
		t.Fatalf("up from bottom = %d, want %d", m.scroll, before-1)
	}
	m.Resize(40, 100)
	if m.scroll != m.maxScroll() {
		t.Fatalf("resize scroll = %d, max = %d", m.scroll, m.maxScroll())
	}
	m.Refresh(board.Task{ID: task.ID, Title: task.Title, Desc: "short", Status: task.Status})
	if m.scroll != 0 {
		t.Fatalf("shorter content scroll = %d", m.scroll)
	}
	view := ansi.Strip(m.View(40, 10))
	if !strings.Contains(view, "esc close") || !strings.Contains(view, "/") {
		t.Fatalf("scrolled overlay footer missing:\n%s", view)
	}
}

func TestCardDetailGolden(t *testing.T) {
	m := New(nil, "default")
	task := fullTask()
	task.Status = board.StatusDoing
	m.Open(task)
	lines := strings.Split(ansi.Strip(m.View(60, 20)), "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	golden.RequireEqual(t, strings.Trim(strings.Join(lines, "\n"), "\n")+"\n")
}

func TestOverlayKeepsBoardAroundPane(t *testing.T) {
	m := New(nil, "u")
	background := strings.Repeat("b", 30) + "\n" + strings.Repeat("b", 30)
	if got := m.Overlay(background, 30, 2); got != background {
		t.Fatalf("closed overlay changed background: %q", got)
	}
	m.Open(board.Task{ID: "id", Title: "detail", Status: board.StatusTodo})
	got := ansi.Strip(m.Overlay(background, 30, 8))
	if !strings.Contains(got, "detail") || !strings.Contains(got, "bbbb") {
		t.Fatalf("composed overlay lost pane or board:\n%s", got)
	}
}

func TestViewFitsTinyTerminal(t *testing.T) {
	m := New(nil, "u")
	m.Open(board.Task{ID: "id", Title: "detail", Status: board.StatusTodo})
	for _, size := range [][2]int{{1, 1}, {2, 3}, {4, 3}} {
		view := m.View(size[0], size[1])
		lines := strings.Split(view, "\n")
		if len(lines) > size[1] {
			t.Fatalf("%dx%d view has %d lines", size[0], size[1], len(lines))
		}
		for _, line := range lines {
			if got := ansi.StringWidth(line); got > size[0] {
				t.Fatalf("%dx%d view line width = %d: %q", size[0], size[1], got, line)
			}
		}
	}
}

func TestHostileFencedTabsStayInsideBorder(t *testing.T) {
	m := New(nil, "u")
	m.Open(board.Task{
		ID: "id", Title: "tabs", Status: board.StatusTodo,
		Desc: "```\n\t~~~~~~\t界界界界界\n```",
	})
	view := ansi.Strip(m.View(20, 20))
	if strings.ContainsRune(view, '\t') {
		t.Fatalf("view retained a tab:\n%s", view)
	}
	if !strings.Contains(view, "~~~~") {
		t.Fatalf("hostile fenced code was not visible:\n%s", view)
	}
	lines := strings.Split(view, "\n")
	if len(lines) > 20 {
		t.Fatalf("view has %d lines:\n%s", len(lines), view)
	}
	for _, line := range lines {
		if width := ansi.StringWidth(line); width > 20 {
			t.Fatalf("line width = %d:\n%s", width, view)
		}
		if strings.HasPrefix(line, "│") && !strings.HasSuffix(line, "│") {
			t.Fatalf("content crossed the right border: %q", line)
		}
	}
}

func TestScrollAndViewReuseRenderedMarkdown(t *testing.T) {
	renders := 0
	m := New(nil, "u")
	m.renderMarkdown = func(source string, _ int) string {
		renders++
		return source
	}
	m.Resize(40, 10)
	m.Open(board.Task{ID: "id", Title: "cached", Desc: strings.Repeat("line\n", 40), Status: board.StatusTodo})
	initial := renders
	if initial == 0 {
		t.Fatal("open did not render the markdown cache")
	}
	for range 100 {
		m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
		m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	}
	for range 10 {
		_ = m.View(40, 10)
	}
	if renders != initial {
		t.Fatalf("scroll/view rerendered markdown: before %d after %d", initial, renders)
	}
	m.Resize(40, 20)
	if renders != initial {
		t.Fatalf("height-only resize rerendered markdown: before %d after %d", initial, renders)
	}
	m.Resize(30, 10)
	if renders != initial+1 {
		t.Fatalf("width change renders = %d, want %d", renders, initial+1)
	}
}
