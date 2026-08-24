package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// gracedModel is a board with one cancelled card, focused: pressing D opens the
// purge prompt, whose single arm button is the ButtonDanger affirmative spec
// section 10.3.3 graces.
func gracedModel(t *testing.T) (Model, board.Task) {
	t.Helper()
	m, _, tasks := actionTestModel(t, board.Task{Title: "Doomed", Status: board.StatusCancelled})
	m.boardView.showCancelled = true
	if !m.boardView.focusTask(m.filteredBoard(), tasks[0].ID) {
		t.Fatal("focus the cancelled card")
	}
	return m, tasks[0]
}

// openGraced drives the keystroke that opens the purge prompt without letting
// updateTestModel step the grace out from under the test.
func openGraced(t *testing.T, m *Model) tea.Cmd {
	t.Helper()
	updated, command := m.Update(tea.KeyPressMsg{Code: 'D', Text: "D"})
	*m = updated.(Model)
	if m.action.mode != taskActionPurge {
		t.Fatalf("purge prompt did not open: %v", m.action.mode)
	}
	return command
}

func sendRaw(t *testing.T, m *Model, message tea.Msg) tea.Cmd {
	t.Helper()
	updated, command := m.Update(message)
	*m = updated.(Model)
	return command
}

func TestGraceArmsOnlyOnADestructivePrompt(t *testing.T) {
	m, task := gracedModel(t)
	if got := m.graceIdentity(); got.set() {
		t.Fatalf("an idle board is graced: %#v", got)
	}
	// The checklist prompt carries no ButtonDanger affirmative.
	checklist, _, checked := actionTestModel(t, board.Task{
		Title: "Chores", Status: board.StatusTodo, Checks: []board.Check{{Text: "one"}},
	})
	checklist.boardView.focusTask(checklist.filteredBoard(), checked[0].ID)
	sendRaw(t, &checklist, tea.KeyPressMsg{Code: 't', Text: "t"})
	if checklist.grace.active {
		t.Fatal("the checklist prompt opened graced")
	}

	openGraced(t, &m)
	if !m.grace.active {
		t.Fatal("the purge prompt opened ungraced")
	}
	if m.grace.id != (graceID{slot: graceAction, cardID: task.ID}) {
		t.Fatalf("grace identity = %#v", m.grace.id)
	}
}

// TestGraceSwallowsTheCommitAndNeverTheExit is the ratified divergence from the
// donor: crush swallows every key, kb exempts the dismissal ladder.
func TestGraceSwallowsTheCommitAndNeverTheExit(t *testing.T) {
	m, _ := gracedModel(t)
	openGraced(t, &m)
	sendRaw(t, &m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.action.armed {
		t.Fatal("the grace let a queued Enter arm the purge")
	}
	if !m.grace.active {
		t.Fatal("a swallowed key ended the grace")
	}

	sendRaw(t, &m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.action.open() {
		t.Fatal("the grace swallowed the dismissal ladder")
	}
}

func TestGraceExemptsEveryRungOfTheDismissalLadder(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{
		{Code: tea.KeyEscape},
		{Code: 'q', Text: "q"},
		{Code: 'c', Mod: tea.ModCtrl},
	} {
		m, _ := gracedModel(t)
		openGraced(t, &m)
		if m.grace.swallows(key) {
			t.Fatalf("the grace swallowed %q", key.String())
		}
	}
}

func TestGraceSwallowsClicksAsWellAsKeys(t *testing.T) {
	m, _ := gracedModel(t)
	openGraced(t, &m)
	click := taskActionPointerMsg{
		session: m.taskActionSession, taskID: m.action.task.ID,
		mode: taskActionPurge, kind: taskActionPointerPurge,
	}
	sendRaw(t, &m, click)
	if m.action.armed {
		t.Fatal("the grace let a click arm the purge")
	}
	if !m.grace.swallows(settingsPointerMsg{}) {
		t.Fatal("the grace let a settings click through")
	}
	if m.grace.swallows(pollTickMsg{}) {
		t.Fatal("the grace swallowed a background tick")
	}
}

// TestGraceQuietWindowRestartsOnEverySwallow is crush's quiet rule: input has to
// stop, not merely start.
func TestGraceQuietWindowRestartsOnEverySwallow(t *testing.T) {
	m, _ := gracedModel(t)
	openGraced(t, &m)
	first := m.grace.quietTag
	sendRaw(t, &m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.grace.quietTag == first {
		t.Fatal("a swallowed key did not restart the quiet window")
	}
	// The stale quiet expiry must not end the grace the newer one owns.
	sendRaw(t, &m, graceQuietMsg{tag: first})
	if !m.grace.active {
		t.Fatal("a stale quiet expiry ended the grace")
	}
	sendRaw(t, &m, graceQuietMsg{tag: m.grace.quietTag})
	if m.grace.active {
		t.Fatal("the live quiet expiry did not end the grace")
	}
	sendRaw(t, &m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.action.armed {
		t.Fatal("input after the grace did not reach the prompt")
	}
}

// TestGraceMaxCeilingEndsAnUnquietWindow is the other half: a user who never
// stops typing still gets the prompt back.
func TestGraceMaxCeilingEndsAnUnquietWindow(t *testing.T) {
	m, _ := gracedModel(t)
	openGraced(t, &m)
	for range 3 {
		sendRaw(t, &m, tea.KeyPressMsg{Code: tea.KeyEnter})
	}
	if !m.grace.active {
		t.Fatal("continuous input ended the quiet window early")
	}
	sendRaw(t, &m, graceMaxMsg{tag: m.grace.openTag + 99})
	if !m.grace.active {
		t.Fatal("a stale ceiling ended the grace")
	}
	sendRaw(t, &m, graceMaxMsg{tag: m.grace.openTag})
	if m.grace.active {
		t.Fatal("the absolute ceiling did not end the grace")
	}
}

// TestGraceSkipsOnQuickReopenOfTheSameIdentity keeps the grace and the Armed
// two-step from compounding.
func TestGraceSkipsOnQuickReopenOfTheSameIdentity(t *testing.T) {
	m, task := gracedModel(t)
	openGraced(t, &m)
	sendRaw(t, &m, graceQuietMsg{tag: m.grace.quietTag})
	sendRaw(t, &m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.action.open() || !m.grace.reopen {
		t.Fatalf("closing the prompt left reopen=%v action=%v", m.grace.reopen, m.action.open())
	}
	if m.grace.reopenID != (graceID{slot: graceAction, cardID: task.ID}) {
		t.Fatalf("reopen identity = %#v", m.grace.reopenID)
	}

	openGraced(t, &m)
	if m.grace.active {
		t.Fatal("a quick reopen of the same prompt re-armed the grace")
	}
	sendRaw(t, &m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.action.armed {
		t.Fatal("a skipped grace still swallowed the arm")
	}
}

func TestGraceReopenWindowExpires(t *testing.T) {
	m, _ := gracedModel(t)
	openGraced(t, &m)
	sendRaw(t, &m, graceQuietMsg{tag: m.grace.quietTag})
	sendRaw(t, &m, tea.KeyPressMsg{Code: tea.KeyEscape})

	sendRaw(t, &m, graceReopenMsg{tag: m.grace.reopenTag + 99})
	if !m.grace.reopen {
		t.Fatal("a stale reopen expiry closed the window")
	}
	sendRaw(t, &m, graceReopenMsg{tag: m.grace.reopenTag})
	if m.grace.reopen {
		t.Fatal("the live reopen expiry did not close the window")
	}
	openGraced(t, &m)
	if !m.grace.active {
		t.Fatal("a reopen past the window skipped the grace")
	}
}

// TestGraceIdentityFollowsZOrder pins the identity table of spec section 10.3.3
// against the overlay z-order of spec section 4.
func TestGraceIdentityFollowsZOrder(t *testing.T) {
	m, task := gracedModel(t)
	openGraced(t, &m)
	m.settings = &settingsModel{armedRemove: "forge-1"}
	if got := m.graceIdentity(); got != (graceID{slot: graceAction, cardID: task.ID}) {
		t.Fatalf("task action outranks settings: %#v", got)
	}
	m.action.busy = true
	if got := m.graceIdentity(); got.slot != graceSettings {
		t.Fatalf("a busy prompt still graced: %#v", got)
	}
	m.settings = nil
	m.action.close()
	if got := m.graceIdentity(); got.set() {
		t.Fatalf("no prompt still graced: %#v", got)
	}
}

func TestGraceTracksTheCardDetailDeletePrompt(t *testing.T) {
	m, _, tasks := actionTestModel(t, board.Task{Title: "Detailed", Status: board.StatusTodo})
	m.boardView.focusTask(m.filteredBoard(), tasks[0].ID)
	sendRaw(t, &m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.detail.IsOpen() {
		t.Fatal("the detail overlay did not open")
	}
	if m.detail.IsDestructivePrompt() {
		t.Fatal("an idle detail overlay reports a destructive prompt")
	}
	if got := m.graceIdentity(); got.set() {
		t.Fatalf("an idle detail overlay is graced: %#v", got)
	}
}

// TestGraceCollapsesWithTiming is the collapse rule: the arming commands of a
// collapsed program dispatch immediately rather than scheduling.
func TestGraceCollapsesWithTiming(t *testing.T) {
	var state graceState
	command := state.open(graceID{slot: graceAction, cardID: "x"}, theme.TimingCollapsed)
	if command == nil {
		t.Fatal("collapsed open produced no command")
	}
	batch, ok := command().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("collapsed open = %#v, want a two-command batch", command())
	}
	for _, next := range batch {
		if !state.expire(next()) {
			t.Fatal("collapsed open scheduled something the machine does not own")
		}
	}
	if state.active {
		t.Fatal("collapsed timing left the grace armed")
	}
	if state.open(graceID{}, theme.DefaultTiming) != nil {
		t.Fatal("an unset identity armed the grace")
	}
	if state.close(graceID{}, theme.DefaultTiming) != nil {
		t.Fatal("an unset identity opened a reopen window")
	}
	if state.expire(pollTickMsg{}) {
		t.Fatal("the grace claimed a message it does not own")
	}
}

// TestGracedPromptRendersUnchanged is the byte-stability half of the
// determinism contract: swallowing input must not move a cell.
func TestGracedPromptRendersUnchanged(t *testing.T) {
	m, _ := gracedModel(t)
	m.width, m.height = 90, 24
	openGraced(t, &m)
	armed := m.View().Content
	sendRaw(t, &m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := m.View().Content; got != armed {
		t.Fatal("a swallowed key changed the frame")
	}
	sendRaw(t, &m, graceQuietMsg{tag: m.grace.quietTag})
	if got := m.View().Content; got != armed {
		t.Fatal("the quiet expiry changed the settled frame")
	}
	sendRaw(t, &m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !strings.Contains(m.View().Content, "ARMED") {
		t.Fatal("input past the grace did not arm the purge")
	}
}
