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
	if m.loading || m.err == nil || !strings.Contains(m.err.Error(), loadErr.Error()) {
		t.Fatalf("load error state = loading %v, err %v", m.loading, m.err)
	}
	if body := ansi.Strip(m.renderBody(40)); !strings.Contains(body, "detail error:") {
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
	m.Open(task)
	m.scroll = 1000
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
