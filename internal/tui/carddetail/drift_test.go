package carddetail

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

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
	m := New(nil, "alice")
	m.SetDriftBackend(backend, context.Background())
	m.Open(driftTask("task-a"))
	command := m.Update(tea.KeyPressMsg{Code: 'v'})
	if command == nil || !m.OwnsInput() || m.driftBusy != "provenance" {
		t.Fatal("drift selection did not start")
	}
	m.Update(command())
	if len(m.driftChoices) != 2 || m.driftMode != driftSelect {
		t.Fatalf("choices = %+v", m.driftChoices)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	command = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(command())
	if m.driftMode != driftReview || m.driftResult.State != "drifted" || backend.checked[0] != "gitlab:gl-key" {
		t.Fatalf("drift result = %+v checked=%v", m.driftResult, backend.checked)
	}
	view := m.View(80, 18)
	for _, want := range []string{"kb does not sync", "material change", "u update baseline"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view omitted %q:\n%s", want, view)
		}
	}
	command = m.Update(tea.KeyPressMsg{Code: 'u'})
	m.Update(command())
	if m.statusMessage != upstreamConflictCopy || !m.statusIsError || backend.accepted != 1 {
		t.Fatalf("conflict status = %q accepted=%d", m.statusMessage, backend.accepted)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.driftMode != driftNone || m.OwnsInput() {
		t.Fatal("escape did not close drift")
	}
}

func TestDriftAcceptSuccessAndStaleSessionGuards(t *testing.T) {
	backend := &fakeDriftBackend{
		provenance: map[string][]store.ImportLink{"github#93": {{Source: "github", ExternalKey: "key", Title: "Issue"}}},
		result:     forge.Drift{State: "drifted", Revision: strings.Repeat("b", 64)}, acceptAt: "2026-08-18T00:00:00Z",
	}
	m := New(nil, "alice")
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
}

func TestDriftErrorsMissingLinksAndBusyInput(t *testing.T) {
	m := New(nil, "alice")
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
	m.Update(command())
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
	m := New(nil, "u")
	m.open = true
	m.driftMode, m.driftBusy = driftSelect, "check"
	if !strings.Contains(m.driftBody(20), "check in progress") || m.driftFooter() != "check in progress | input locked" {
		t.Fatal("busy rendering")
	}
	m.driftBusy = ""
	m.driftChoices = []store.ImportLink{{Source: "forge", Title: "title", URL: "https://example"}}
	if !strings.Contains(m.driftBody(20), "Choose provenance") || !strings.Contains(m.driftFooter(), "enter check") {
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
