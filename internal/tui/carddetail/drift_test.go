package carddetail

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/forge"
	"github.com/RandomCodeSpace/kb/internal/store"
)

type fakeDriftBackend struct {
	provenance    map[string][]store.ImportLink
	provenanceErr error
	result        forge.Drift
	checkErr      error
	acceptAt      string
	acceptErr     error
	checked       []string
	accepted      int
}

func (b *fakeDriftBackend) Provenance(_ string, link string) ([]store.ImportLink, error) {
	return b.provenance[link], b.provenanceErr
}

func (b *fakeDriftBackend) CheckDrift(_ context.Context, _, source, key string) (forge.Drift, error) {
	b.checked = append(b.checked, source+":"+key)
	return b.result, b.checkErr
}

func (b *fakeDriftBackend) AcceptDrift(_ context.Context, _, _, _, _ string) (string, error) {
	b.accepted++
	return b.acceptAt, b.acceptErr
}

func driftTask(id string) board.Task {
	return board.Task{ID: id, Title: "Imported", Status: board.StatusTodo, Tags: []string{"link::github#93", "link::gitlab#93"}}
}

func TestDriftSelectCheckAndAcceptConflict(t *testing.T) {
	backend := &fakeDriftBackend{
		provenance: map[string][]store.ImportLink{
			"github#93": {{Source: "github", ExternalKey: "gh-key", Title: "GitHub issue", URL: "https://github.test/93"}},
			"gitlab#93": {{Source: "gitlab", ExternalKey: "gl-key", Title: "GitLab issue", URL: "https://gitlab.test/93"}},
		},
		result:    forge.Drift{State: "drifted", UpstreamTitle: "Changed", BaselineTitle: "Old", Summary: "material change", Revision: strings.Repeat("a", 64)},
		acceptErr: forge.ErrUpstreamChanged,
	}
	m := New(nil, "alice", testStyles())
	m.SetDriftBackend(backend, context.Background())
	m.Open(driftTask("task-a"))
	command := m.Update(tea.KeyPressMsg{Code: 'v'})
	if command == nil || !m.OwnsInput() || m.driftBusy != "provenance" {
		t.Fatal("drift selection did not start")
	}
	m.Update(busyResult(t, command))
	if len(m.driftChoices) != 2 || m.driftMode != driftSelect {
		t.Fatalf("choices = %+v", m.driftChoices)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	command = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(busyResult(t, command))
	if m.driftMode != driftReview || m.driftResult.State != "drifted" || backend.checked[0] != "gitlab:gl-key" {
		t.Fatalf("drift result = %+v checked=%v", m.driftResult, backend.checked)
	}
	view := ansi.Strip(m.View(80, 18))
	for _, want := range []string{"kb does not sync", "material change", "Update baseline"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view omitted %q:\n%s", want, view)
		}
	}
	command = m.Update(tea.KeyPressMsg{Code: 'u'})
	m.Update(busyResult(t, command))
	if m.statusMessage != upstreamConflictCopy || !m.statusIsError || backend.accepted != 1 {
		t.Fatalf("conflict status = %q accepted=%d", m.statusMessage, backend.accepted)
	}
	// Spec section 10.8.5: a failure is the Alert glyph and a TintDanger run
	// pinned above the action row, never the "error: " text prefix the pane
	// used to write into a body line.
	if view := ansi.Strip(m.View(120, 18)); !strings.Contains(view, "▲ Upstream changed again") {
		t.Fatalf("conflict status not visible:\n%s", view)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.driftMode != driftNone || m.OwnsInput() {
		t.Fatal("escape did not close drift")
	}
}

func TestPointerDriftSelectCheckAcceptAndBack(t *testing.T) {
	backend := &fakeDriftBackend{
		provenance: map[string][]store.ImportLink{
			"github#93": {{Source: "github", ExternalKey: "gh-key", Title: "GitHub issue"}},
			"gitlab#93": {{Source: "gitlab", ExternalKey: "gl-key", Title: "GitLab issue"}},
		},
		result:   forge.Drift{State: "drifted", Revision: strings.Repeat("a", 64)},
		acceptAt: "2026-08-18T00:00:00Z",
	}
	m := New(nil, "alice", testStyles())
	m.SetDriftBackend(backend, context.Background())
	m.Open(driftTask("task-pointer"))
	m.Update(m.beginDrift()())

	surface := m.PointerSurface("board", pointerWidth, pointerHeight)
	x, y := -1, -1
	for row, line := range strings.Split(ansi.Strip(surface.Content), "\n") {
		if column := strings.Index(line, "GitLab issue"); column >= 0 {
			x, y = ansi.StringWidth(line[:column]), row
			break
		}
	}
	if x < 0 {
		t.Fatalf("second provenance is not visible:\n%s", ansi.Strip(surface.Content))
	}
	press := surface.Pointer(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	if press == nil || m.Update(busyResult(t, press)) != nil {
		t.Fatal("provenance did not enter pressed state")
	}
	pressed := m.PointerSurface("board", pointerWidth, pointerHeight)
	if !containsReverseVideo(pressed.Content) {
		t.Fatal("provenance did not render pressed feedback")
	}
	release := pressed.Pointer(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	activate := m.Update(busyResult(t, release))
	message, _ := m.ResolvePointerMessage(activate())
	m.Update(message)
	if m.driftSelection != 1 {
		t.Fatalf("pointer provenance selection = %d", m.driftSelection)
	}

	check := clickControl(t, &m, "Check selected")
	m.Update(busyResult(t, check))
	if m.driftMode != driftReview || backend.checked[0] != "gitlab:gl-key" {
		t.Fatalf("pointer drift check = mode:%v checked:%v", m.driftMode, backend.checked)
	}
	accept := clickControl(t, &m, "Update baseline")
	m.Update(busyResult(t, accept))
	if backend.accepted != 1 || m.driftResult.State != "unchanged" {
		t.Fatalf("pointer baseline update = accepted:%d result:%+v", backend.accepted, m.driftResult)
	}
	if command := clickControl(t, &m, "Back"); command != nil || m.driftMode != driftNone {
		t.Fatalf("pointer drift Back = command:%v mode:%v", command, m.driftMode)
	}
}

func TestDriftAcceptSuccessAndStaleSessionGuards(t *testing.T) {
	backend := &fakeDriftBackend{
		provenance: map[string][]store.ImportLink{"github#93": {{Source: "github", ExternalKey: "key", Title: "Issue"}}},
		result:     forge.Drift{State: "drifted", Revision: strings.Repeat("b", 64)}, acceptAt: "2026-08-18T00:00:00Z",
	}
	m := New(nil, "alice", testStyles())
	m.SetDriftBackend(backend, nil)
	task := driftTask("task-a")
	task.Tags = []string{"link::github#93"}
	m.Open(task)
	choicesCommand := m.beginDrift()
	staleChoices := choicesCommand()
	m.Close()
	m.Open(board.Task{ID: "task-b", Title: "Other", Status: board.StatusTodo, Tags: task.Tags})
	m.Update(staleChoices)
	if len(m.driftChoices) != 0 {
		t.Fatal("stale choices crossed task session")
	}
	m.Update(m.beginDrift()())
	m.Update(m.startDriftCheck()())
	m.Update(m.startDriftAccept()())
	if m.driftResult.State != "unchanged" || m.driftResult.BaselineAt != backend.acceptAt || m.statusMessage != "upstream baseline updated" {
		t.Fatalf("accept result = %+v status=%q", m.driftResult, m.statusMessage)
	}
	if view := m.View(80, 18); !strings.Contains(view, "status: upstream baseline updated") {
		t.Fatalf("accept status not visible:\n%s", view)
	}
}

// Issue #282: a legacy import with no recorded snapshot has nothing to compare
// against, so the first check records one. The pane says that in English and
// never claims the card is unchanged since import.
func TestDriftBaselineRecordedExplainsFirstCheck(t *testing.T) {
	backend := &fakeDriftBackend{
		provenance: map[string][]store.ImportLink{"github#93": {{Source: "github", ExternalKey: "key", Title: "Issue"}}},
		result: forge.Drift{
			State: "baseline_recorded", UpstreamTitle: "Current title",
			BaselineTitle: "Current title", BaselineAt: "2026-09-03T00:00:00Z",
		},
	}
	m := New(nil, "alice", testStyles())
	m.SetDriftBackend(backend, context.Background())
	task := driftTask("task-legacy")
	task.Tags = []string{"link::github#93"}
	m.Open(task)
	m.Update(m.beginDrift()())
	m.Update(m.startDriftCheck()())
	view := ansi.Strip(m.View(120, 18))
	for _, want := range []string{"state  baseline recorded", driftBaselineRecordedCopy} {
		if !strings.Contains(view, want) {
			t.Fatalf("view omitted %q:\n%s", want, view)
		}
	}
	for _, unwanted := range []string{"baseline_recorded", "unchanged"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("view leaked %q:\n%s", unwanted, view)
		}
	}
}

func TestDriftErrorsMissingLinksAndBusyInput(t *testing.T) {
	m := New(nil, "alice", testStyles())
	m.SetDriftBackend(&fakeDriftBackend{}, context.Background())
	m.Open(board.Task{ID: "plain", Title: "Plain", Status: board.StatusTodo})
	if command := m.beginDrift(); command != nil || m.statusMessage != "no imported forge link on this card" {
		t.Fatalf("missing link = cmd %v status %q", command, m.statusMessage)
	}
	backend := &fakeDriftBackend{provenanceErr: &forge.Error{Message: "provenance unavailable"}}
	m.SetDriftBackend(backend, context.Background())
	m.Open(driftTask("task"))
	m.Update(m.beginDrift()())
	if m.driftMode != driftNone || m.statusMessage != "provenance unavailable" {
		t.Fatalf("provenance error = mode %d status %q", m.driftMode, m.statusMessage)
	}
	backend.provenanceErr = nil
	backend.provenance = map[string][]store.ImportLink{}
	m.Update(m.beginDrift()())
	if m.statusMessage != "import provenance not found" {
		t.Fatalf("empty provenance status = %q", m.statusMessage)
	}
	backend.provenance = map[string][]store.ImportLink{"github#93": {{Source: "github", ExternalKey: "key"}}}
	backend.checkErr = errors.New("secret")
	m.Open(board.Task{ID: "one", Title: "One", Status: board.StatusTodo, Tags: []string{"link::github#93"}})
	m.Update(m.beginDrift()())
	command := m.startDriftCheck()
	if m.updateDrift(tea.KeyPressMsg{Code: tea.KeyEscape}) != nil || m.driftBusy != "check" {
		t.Fatal("busy escape leaked")
	}
	m.Update(busyResult(t, command))
	if m.statusMessage != "drift check failed" || !m.driftModeActive() {
		t.Fatalf("check error = %q mode=%d", m.statusMessage, m.driftMode)
	}
}

func (m Model) driftModeActive() bool { return m.driftMode != driftNone }

func TestRawImportLinksAndDriftRenderingHelpers(t *testing.T) {
	got := rawImportLinks([]string{"x", "link::github#1", "link::github#1", "link:: ", "link::gitlab#2"})
	if strings.Join(got, ",") != "github#1,gitlab#2" {
		t.Fatalf("links = %v", got)
	}
	m := New(nil, "u", testStyles())
	m.open = true
	m.driftMode, m.driftBusy = driftSelect, "check"
	// Spec section 10.8.4 rule 1: the busy state is a footer band line and the
	// body's own "<op> in progress..." row is gone, so the body does not reflow
	// when the check lands.
	if strings.Contains(m.driftBody(20), "in progress") {
		t.Fatal("busy body row survived")
	}
	if busy := m.actionFooter(40); !strings.Contains(busy, "esc cancel") {
		t.Fatalf("busy footer = %q", busy)
	}
	m.driftBusy = ""
	m.driftChoices = []store.ImportLink{{Source: "forge", Title: "title", URL: "https://example"}}
	if !strings.Contains(m.driftBody(20), "Choose provenance") || !strings.Contains(m.actionFooter(60), "enter check") {
		t.Fatal("selection rendering")
	}
	if driftError(&forge.Error{Message: "safe"}) != "safe" || driftError(errors.New("secret")) != "drift check failed" {
		t.Fatal("drift error mapping")
	}
	m.driftChoices = []store.ImportLink{{Source: "git\x1b[31mhub", Title: "title\a", URL: "https://example.test/\x9b31m"}}
	if body := m.driftBody(80); strings.ContainsAny(body, "\x1b\a\x9b") {
		t.Fatalf("selection leaked terminal controls: %q", body)
	}
	m.driftMode = driftReview
	m.driftResult = forge.Drift{State: "drifted\x1b", UpstreamTitle: "up\a", BaselineTitle: "base\x9b", Summary: "summary\x1b[31m\nline"}
	if body := m.driftBody(80); strings.ContainsAny(body, "\x1b\a\x9b") || !strings.Contains(body, "summary") {
		t.Fatalf("review leaked terminal controls: %q", body)
	}
}
