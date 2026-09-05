package cardeditor

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
)

func TestPreviewAndTerminalSelectionPreserveUnsavedDescriptionState(t *testing.T) {
	m := newTestEditor(newTestStore(t), "alice")
	m.OpenEdit(board.Task{ID: "task", Title: "Preview", Desc: "# Heading\n\n**unsaved** https://example.com/long/path", Status: board.StatusTodo, Prio: 3})
	m.focus = "desc"
	m.applyFocus()
	m.desc.SetCursorColumn(5)
	m.scroll = 4
	_ = m.View(38, 10)

	wantValue := m.desc.Value()
	wantLine, wantColumn, wantScroll, firstSession := m.desc.Line(), m.desc.Column(), m.scroll, m.pointerSession
	m.Update(tea.KeyPressMsg{Code: tea.KeyF2})
	if !m.preview || m.pointerSession == firstSession || m.PointerSession() != m.pointerSession {
		t.Fatalf("preview state = %t pointer session %d/%d", m.preview, m.pointerSession, firstSession)
	}
	plain := ansi.Strip(m.View(38, 10))
	for _, want := range []string{"Heading", "unsaved", "Edit source", "Select text"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("preview missing %q:\n%s", want, plain)
		}
	}
	for _, size := range [][2]int{{68, 12}, {80, 18}} {
		view := m.View(size[0], size[1])
		lines := strings.Split(view, "\n")
		if len(lines) > size[1] {
			t.Fatalf("preview %dx%d emitted %d rows", size[0], size[1], len(lines))
		}
		for index, line := range lines {
			if width := ansi.StringWidth(line); width > size[0] {
				t.Fatalf("preview %dx%d row %d width = %d", size[0], size[1], index, width)
			}
		}
	}
	assertDescriptionState(t, &m, wantValue, wantLine, wantColumn, wantScroll)

	previewSession := m.pointerSession
	m.Update(tea.KeyPressMsg{Code: tea.KeyF2})
	if m.preview || m.pointerSession == previewSession || m.focus != "desc" {
		t.Fatalf("source return = preview:%t focus:%q session:%d/%d", m.preview, m.focus, m.pointerSession, previewSession)
	}
	assertDescriptionState(t, &m, wantValue, wantLine, wantColumn, wantScroll)

	sourceSession := m.pointerSession
	m.Update(tea.KeyPressMsg{Code: tea.KeyF3})
	if !m.TerminalSelectionActive() || m.pointerSession == sourceSession || m.terminalSnapshot != wantValue {
		t.Fatalf("terminal selection = active:%t session:%d/%d snapshot:%q", m.TerminalSelectionActive(), m.pointerSession, sourceSession, m.terminalSnapshot)
	}
	m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if m.desc.Value() != wantValue {
		t.Fatal("terminal selection accepted editor input")
	}
	if view := m.TerminalSelectionView(18, 5); !strings.Contains(view, "# Heading") {
		t.Fatalf("terminal snapshot omitted Markdown source:\n%s", view)
	}
	terminalSession := m.pointerSession
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.TerminalSelectionActive() || m.pointerSession == terminalSession || m.focus != "desc" {
		t.Fatalf("terminal return = active:%t focus:%q session:%d/%d", m.TerminalSelectionActive(), m.focus, m.pointerSession, terminalSession)
	}
	assertDescriptionState(t, &m, wantValue, wantLine, wantColumn, wantScroll)
}

func TestTerminalSelectionProcessesCurrentAsyncResultsWithoutChangingSnapshot(t *testing.T) {
	m := newTestEditor(newTestStore(t), "alice")
	m.OpenEdit(board.Task{ID: "task", Title: "Preview", Desc: "frozen source", Status: board.StatusTodo, Prio: 3})
	m.similarGen = 7
	m.similarLoading = true
	query := strings.TrimSpace(m.title.Value())
	exclusions := m.currentExclusions()
	m.Resize(40, 8)
	m.Update(tea.KeyPressMsg{Code: tea.KeyF3})
	snapshot := m.terminalSnapshot

	m.Update(similarLoadedMsg{
		generation: 7,
		query:      query,
		exclusions: exclusions,
		hits:       []store.SimilarHit{{ID: "other", Title: "Related"}},
	})
	if m.similarLoading || len(m.similar) != 1 || m.terminalSnapshot != snapshot || !m.TerminalSelectionActive() {
		t.Fatalf("async result in terminal mode = loading:%t similar:%+v snapshot:%q active:%t",
			m.similarLoading, m.similar, m.terminalSnapshot, m.TerminalSelectionActive())
	}
}

func assertDescriptionState(t *testing.T, m *Model, value string, line, column, scroll int) {
	t.Helper()
	if m.desc.Value() != value || m.desc.Line() != line || m.desc.Column() != column || m.scroll != scroll {
		t.Fatalf("description changed: value=%q line=%d column=%d scroll=%d", m.desc.Value(), m.desc.Line(), m.desc.Column(), m.scroll)
	}
}

func TestPreviewRejectsPointerMessagesFromSourceFrame(t *testing.T) {
	m := newTestEditor(newTestStore(t), "alice")
	m.OpenEdit(board.Task{ID: "task", Title: "Preview", Desc: "body", Status: board.StatusTodo, Prio: 3})
	stale := pointerClickMsg{session: m.pointerSession, target: "save"}
	m.Update(tea.KeyPressMsg{Code: tea.KeyF2})
	m.Update(stale)
	if !m.IsOpen() || !m.preview || m.saving {
		t.Fatalf("stale source pointer escaped into preview: open=%t preview=%t saving=%t", m.IsOpen(), m.preview, m.saving)
	}
}

func TestPreviewNavigationKeepsItsOwnScrollAndModeIdentity(t *testing.T) {
	m := newTestEditor(newTestStore(t), "alice")
	m.OpenEdit(board.Task{
		ID: "task", Title: "Preview", Status: board.StatusTodo, Prio: 3,
		Desc: strings.Repeat("paragraph with enough words to wrap\n\n", 12),
	})
	m.focus = "desc"
	m.applyFocus()
	m.enterPreview()
	previewSession := m.pointerSession
	m.enterPreview()
	if m.pointerSession != previewSession {
		t.Fatal("entering an active preview changed pointer identity")
	}

	for _, key := range []tea.KeyPressMsg{
		{Code: tea.KeyDown}, {Code: 'j', Text: "j"}, {Code: tea.KeyPgDown}, {Code: tea.KeyEnd},
		{Code: tea.KeyUp}, {Code: 'k', Text: "k"}, {Code: tea.KeyPgUp}, {Code: tea.KeyHome},
	} {
		m.Update(key)
	}
	if m.previewScroll != 0 || m.scroll != 0 {
		t.Fatalf("preview/source scroll = %d/%d after home", m.previewScroll, m.scroll)
	}
	m.Update(pointerWheelMsg{session: m.pointerSession, preview: true, target: 5, maxScroll: 20})
	if m.previewScroll != 5 || m.scroll != 0 {
		t.Fatalf("preview wheel changed scrolls to %d/%d", m.previewScroll, m.scroll)
	}
	m.Update(pointerWheelMsg{session: m.pointerSession, preview: false, target: 9, maxScroll: 20})
	if m.previewScroll != 5 {
		t.Fatalf("stale source wheel changed preview scroll to %d", m.previewScroll)
	}

	m.leavePreview()
	sourceSession := m.pointerSession
	m.leavePreview()
	if m.preview || m.focus != "desc" || m.pointerSession != sourceSession {
		t.Fatalf("preview return = preview:%t focus:%q session:%d/%d",
			m.preview, m.focus, m.pointerSession, sourceSession)
	}
}

func TestTerminalSelectionRejectsEmptyAndSanitizesSnapshot(t *testing.T) {
	m := newTestEditor(newTestStore(t), "alice")
	m.OpenEdit(board.Task{ID: "task", Title: "Preview", Status: board.StatusTodo, Prio: 3})
	m.Update(tea.KeyPressMsg{Code: tea.KeyF3})
	if m.TerminalSelectionActive() || m.statusMessage != "description is empty" || m.statusIsError {
		t.Fatalf("empty terminal selection = active:%t status:%q error:%t",
			m.TerminalSelectionActive(), m.statusMessage, m.statusIsError)
	}

	source := "\x1b[31mred\x1b[0m\x00bad\r\n\tkept"
	if got := safeMarkdownSource(source); got != "redbad\n\tkept" {
		t.Fatalf("safe Markdown source = %q", got)
	}
	m.desc.SetValue(strings.Repeat("line\n", 12) + source)
	m.Resize(12, 4)
	m.Update(tea.KeyPressMsg{Code: tea.KeyF3})
	if !m.TerminalSelectionActive() || strings.Contains(m.terminalSnapshot, "\x1b") || strings.Contains(m.terminalSnapshot, "\x00") {
		t.Fatalf("sanitized terminal selection = active:%t snapshot:%q", m.TerminalSelectionActive(), m.terminalSnapshot)
	}
	m.Update(pointerClickMsg{session: m.pointerSession, target: "save"})
	m.Update(pointerWheelMsg{session: m.pointerSession, target: 3, maxScroll: 9})
	m.Update(tea.MouseMotionMsg{X: 1, Y: 1, Button: tea.MouseNone})
	if !m.TerminalSelectionActive() {
		t.Fatal("pointer input exited terminal selection")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	if m.terminalOffset == 0 {
		t.Fatal("terminal snapshot end key did not scroll")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.TerminalSelectionActive() {
		t.Fatal("escape did not exit terminal selection")
	}
}

func TestPreviewAndTerminalControlsRouteThroughPointerAndKeyboard(t *testing.T) {
	m := newTestEditor(newTestStore(t), "alice")
	if surface := m.PointerSurface(80, 20); surface.Pointer != nil {
		t.Fatal("closed editor exposed a pointer surface")
	}
	m.OpenEdit(board.Task{ID: "task", Title: "Controls", Desc: "preview me", Status: board.StatusTodo, Prio: 3})

	m.Update(pointerClickMsg{session: m.pointerSession, target: "source-preview"})
	if !m.preview {
		t.Fatal("preview pointer control did not enter preview")
	}
	targets := map[string]bool{}
	for _, hit := range m.pointerHits(80, 20) {
		targets[hit.target] = true
	}
	if !targets["source-preview"] || !targets["terminal-select"] {
		t.Fatalf("preview footer pointer targets = %v", targets)
	}
	m.Update(pointerClickMsg{session: m.pointerSession, target: "source-preview"})
	if m.preview {
		t.Fatal("preview pointer control did not return to source")
	}

	m.focus = "source-preview"
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.preview {
		t.Fatal("preview keyboard control did not enter preview")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m.focus = "terminal-select"
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.TerminalSelectionActive() {
		t.Fatal("terminal keyboard control did not enter terminal selection")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	m.Update(pointerClickMsg{session: m.pointerSession, target: "terminal-select"})
	if !m.TerminalSelectionActive() {
		t.Fatal("terminal pointer control did not enter terminal selection")
	}
}

func TestPreviewRowsNameAnEmptyDescription(t *testing.T) {
	m := newTestEditor(newTestStore(t), "alice")
	m.OpenEdit(board.Task{ID: "task", Title: "Empty", Status: board.StatusTodo, Prio: 3})
	rows := m.previewRows(40)
	if len(rows) != 1 || rows[0].text != "Description is empty" || rows[0].kind != rowHint {
		t.Fatalf("empty preview rows = %+v", rows)
	}
}
