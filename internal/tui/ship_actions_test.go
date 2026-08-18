package tui

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

func actionTestModel(t *testing.T, tasks ...board.Task) (Model, *store.Store, []board.Task) {
	t.Helper()
	backend := newSettingsTestStore(t)
	created := make([]board.Task, len(tasks))
	for index, task := range tasks {
		if task.Prio == 0 {
			task.Prio = 3
		}
		var err error
		created[index], err = backend.AddTask("alice", task)
		if err != nil {
			t.Fatal(err)
		}
	}
	m := NewModel(backend, nil, "alice")
	completeBoardLoad(t, &m, m.Init())
	return m, backend, created
}

func finishActionCommand(t *testing.T, m *Model, command tea.Cmd) tea.Cmd {
	t.Helper()
	if command == nil {
		t.Fatal("action command is nil")
	}
	return updateTestModel(t, m, command())
}

func liftToDone(t *testing.T, m *Model, taskID string) tea.Cmd {
	t.Helper()
	if !m.boardView.focusTask(m.filteredBoard(), taskID) {
		t.Fatalf("focus task %s", taskID)
	}
	updateTestModel(t, m, tea.KeyPressMsg{Code: tea.KeySpace})
	updateTestModel(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
	updateTestModel(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
	return updateTestModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
}

func TestOrdinaryDoneDropUsesTransactionalCompletionGuard(t *testing.T) {
	m, backend, tasks := actionTestModel(t,
		board.Task{Title: "Open blocker", Status: board.StatusTodo},
		board.Task{Title: "Clear card", Status: board.StatusTodo},
	)
	blocker, target := tasks[0], tasks[1]
	if _, _, err := backend.Link("alice", blocker.ID, target.ID); err != nil {
		t.Fatal(err)
	}
	// Refresh the model after the direct fixture write; the task snapshots are
	// unchanged, but the guard reads the link in the same transaction as move.
	command := liftToDone(t, &m, target.ID)
	finishActionCommand(t, &m, command)
	current, err := backend.Task("alice", target.ID)
	if err != nil || current.Status != board.StatusTodo {
		t.Fatalf("guarded drop persisted = %s, %v", current.Status, err)
	}
	if !m.move.statusError || !strings.Contains(m.move.status, "still blocks") {
		t.Fatalf("guard refusal status = error %v, %q", m.move.statusError, m.move.status)
	}
}

func TestShipPromptTickAllAndShipAnywayAreExplicitForcedChoices(t *testing.T) {
	for _, test := range []struct {
		name        string
		choice      int
		wantChecked bool
	}{
		{name: "tick everything", choice: 1, wantChecked: true},
		{name: "ship anyway", choice: 2, wantChecked: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			m, backend, tasks := actionTestModel(t,
				board.Task{Title: "Open blocker", Status: board.StatusTodo},
				board.Task{Title: "Warned card", Status: board.StatusTodo, Blocked: true,
					Checks: []board.Check{{Text: "unfinished"}}},
			)
			blocker, target := tasks[0], tasks[1]
			if _, _, err := backend.Link("alice", blocker.ID, target.ID); err != nil {
				t.Fatal(err)
			}
			if command := liftToDone(t, &m, target.ID); command != nil {
				t.Fatalf("warned drop wrote before confirmation: %v", command)
			}
			if m.action.mode != taskActionShip || m.move.lifted != nil ||
				!strings.Contains(ansi.Strip(m.View().Content), "Tick everything") {
				t.Fatalf("ship prompt state = action %#v move %#v\n%s", m.action, m.move, m.View().Content)
			}
			m.action.choice = test.choice
			command := updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEnter})
			finishActionCommand(t, &m, command)

			current, err := backend.Task("alice", target.ID)
			if err != nil || current.Status != board.StatusDone || current.Checks[0].Done != test.wantChecked {
				t.Fatalf("confirmed ship = %+v, %v", current, err)
			}
			links, err := backend.TaskLinks("alice", target.ID)
			if err != nil || len(links.BlockedBy) != 1 {
				t.Fatalf("forced ship changed blocker link = %+v, %v", links, err)
			}
			if m.shippedCount() != 1 || !strings.Contains(ansi.Strip(m.View().Content), "×1 shipped today") ||
				m.actionStatus != "Shipped Warned card" {
				t.Fatalf("ship feedback = count %d status %q\n%s", m.shippedCount(), m.actionStatus, m.View().Content)
			}
		})
	}
}

func TestTickEverythingRefusesStaleChecklist(t *testing.T) {
	m, backend, tasks := actionTestModel(t, board.Task{
		Title: "Concurrent checklist", Status: board.StatusTodo, Blocked: true,
		Checks: []board.Check{{Text: "original"}},
	})
	task := tasks[0]
	m.openShipPrompt(task, 0)
	concurrent := []board.Check{{Text: "original"}, {Text: "added elsewhere"}}
	if _, err := backend.UpdateTask("alice", task.ID, store.TaskPatch{Checks: &concurrent}); err != nil {
		t.Fatal(err)
	}
	finishActionCommand(t, &m, m.startShip(true))

	current, err := backend.Task("alice", task.ID)
	if err != nil || current.Status != board.StatusTodo || len(current.Checks) != 2 || current.Checks[1].Text != "added elsewhere" {
		t.Fatalf("stale tick-all persisted = %+v, %v", current, err)
	}
	if !strings.Contains(m.action.errorText, "checklist") {
		t.Fatalf("stale tick-all feedback = %q", m.action.errorText)
	}
}

func TestChecklistLastTickAutoShipsAfterCanonicalRecheck(t *testing.T) {
	m, backend, tasks := actionTestModel(t, board.Task{
		Title: "Auto ship", Status: board.StatusDoing,
		Checks: []board.Check{{Text: "first", Done: true}, {Text: "last"}},
	})
	task := tasks[0]
	if !m.boardView.focusTask(m.filteredBoard(), task.ID) {
		t.Fatal("focus task")
	}
	updateTestModel(t, &m, tea.KeyPressMsg{Code: 't'})
	m.action.checkIndex = 1
	write := updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeySpace})
	afterWrite := finishActionCommand(t, &m, write)
	if afterWrite == nil {
		t.Fatal("last tick did not schedule auto-ship")
	}
	check := afterWrite()
	read := updateTestModel(t, &m, check)
	ready := read()
	ship := updateTestModel(t, &m, ready)
	finishActionCommand(t, &m, ship)
	current, err := backend.Task("alice", task.ID)
	if err != nil || current.Status != board.StatusDone || !checksComplete(current.Checks) {
		t.Fatalf("auto-ship = %+v, %v", current, err)
	}
	if m.actionStatus != "Shipped Auto ship" || m.shippedCount() != 1 {
		t.Fatalf("auto-ship feedback = %q, %d", m.actionStatus, m.shippedCount())
	}
}

func TestAutoShipRecheckObservesUndoBeforeTimer(t *testing.T) {
	m, backend, tasks := actionTestModel(t, board.Task{
		Title: "Undo tick", Status: board.StatusTodo, Checks: []board.Check{{Text: "only"}},
	})
	task := tasks[0]
	m.boardView.focusTask(m.filteredBoard(), task.ID)
	updateTestModel(t, &m, tea.KeyPressMsg{Code: 't'})
	first := updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeySpace})
	timer := finishActionCommand(t, &m, first)
	undo := updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeySpace})
	finishActionCommand(t, &m, undo)
	read := updateTestModel(t, &m, timer())
	if command := updateTestModel(t, &m, read()); command != nil {
		t.Fatalf("undone checklist still auto-shipped: %v", command)
	}
	current, err := backend.Task("alice", task.ID)
	if err != nil || current.Status != board.StatusTodo || current.Checks[0].Done {
		t.Fatalf("undo recheck = %+v, %v", current, err)
	}
}

func TestAutoShipWaitsForInFlightChecklistWrite(t *testing.T) {
	m, backend, tasks := actionTestModel(t, board.Task{
		Title: "In-flight undo", Status: board.StatusTodo, Checks: []board.Check{{Text: "only"}},
	})
	task := tasks[0]
	m.boardView.focusTask(m.filteredBoard(), task.ID)
	updateTestModel(t, &m, tea.KeyPressMsg{Code: 't'})
	tick := updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeySpace})
	timer := finishActionCommand(t, &m, tick)
	undo := updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeySpace})
	if !m.action.busy {
		t.Fatal("undo write did not enter busy state")
	}
	if command := updateTestModel(t, &m, timer()); command != nil {
		t.Fatalf("timer read started during undo: %v", command)
	}
	if command := m.finishAutoShipRead(autoShipReadyMsg{task: m.action.task, found: true}); command != nil {
		t.Fatalf("stale auto-ship readiness started during undo: %v", command)
	}
	finishActionCommand(t, &m, undo)
	current, err := backend.Task("alice", task.ID)
	if err != nil || current.Status != board.StatusTodo || current.Checks[0].Done {
		t.Fatalf("in-flight undo = %+v, %v", current, err)
	}
}

func TestAutoShipTransactionRefusesConcurrentCancellation(t *testing.T) {
	m, backend, tasks := actionTestModel(t, board.Task{
		Title: "Cancelled elsewhere", Status: board.StatusTodo,
		Checks: []board.Check{{Text: "done", Done: true}},
	})
	task := tasks[0]
	ship := m.finishAutoShipRead(autoShipReadyMsg{task: task, found: true})
	if ship == nil {
		t.Fatal("eligible auto-ship did not start")
	}
	if _, err := backend.MoveTask("alice", task.ID, board.StatusCancelled); err != nil {
		t.Fatal(err)
	}
	finishActionCommand(t, &m, ship)
	current, err := backend.Task("alice", task.ID)
	if err != nil || current.Status != board.StatusCancelled {
		t.Fatalf("auto-ship resurrected cancellation = %+v, %v", current, err)
	}
	if !strings.Contains(m.actionStatus, "auto-ship refused") {
		t.Fatalf("concurrent cancellation feedback = %q", m.actionStatus)
	}
}

func TestCancelledTaskCannotOpenKillPrompt(t *testing.T) {
	m, _, tasks := actionTestModel(t, board.Task{Title: "Already gone", Status: board.StatusCancelled})
	m.openKillPrompt(tasks[0])
	if m.action.open() || m.actionStatus != "Card is already Cancelled" || !m.actionStatusError {
		t.Fatalf("cancelled kill prompt = action:%#v status:%q error:%v", m.action, m.actionStatus, m.actionStatusError)
	}
}

func TestPointerShipChoiceUsesExistingWritePath(t *testing.T) {
	m, backend, tasks := actionTestModel(t, board.Task{
		Title: "Pointer ship", Status: board.StatusTodo,
		Checks: []board.Check{{Text: "still open"}},
	})
	task := tasks[0]
	m.openShipPrompt(task, 1)
	command := updateTestModel(t, &m, pointerCommandForLabel(t, &m, "Ship anyway")())
	if command == nil {
		t.Fatal("pointer ship did not start the existing write path")
	}
	finishActionCommand(t, &m, command)
	stored, err := backend.Task("alice", task.ID)
	if err != nil || stored.Status != board.StatusDone || stored.Checks[0].Done {
		t.Fatalf("pointer ship result = %+v, %v", stored, err)
	}
}

func TestTaskTitleCannotImpersonatePointerShipChoice(t *testing.T) {
	m, backend, tasks := actionTestModel(t, board.Task{
		Title: "Ship anyway", Status: board.StatusTodo,
		Checks: []board.Check{{Text: "still open"}},
	})
	m.openShipPrompt(tasks[0], 1)
	m.width, m.height = 80, 24
	background := strings.Repeat(strings.Repeat(" ", m.width)+"\n", m.height-1) + strings.Repeat(" ", m.width)
	surface := m.taskActionSurface(background)
	for row, line := range strings.Split(ansi.Strip(surface.Content), "\n") {
		if !strings.Contains(line, "Move Ship anyway to Done?") {
			continue
		}
		x := ansi.StringWidth(line[:strings.Index(line, "Ship anyway")])
		if press := surface.Pointer(tea.MouseClickMsg{X: x, Y: row, Button: tea.MouseLeft}); press != nil {
			if command := updateTestModel(t, &m, press()); command != nil {
				t.Fatal("untrusted title produced a ship domain command")
			}
		}
		if release := m.taskActionSurface(background).Pointer(tea.MouseReleaseMsg{X: x, Y: row, Button: tea.MouseNone}); release != nil {
			if command := updateTestModel(t, &m, release()); command != nil {
				t.Fatal("untrusted title release produced a ship domain command")
			}
		}
		stored, err := backend.Task("alice", tasks[0].ID)
		if err != nil || stored.Status != board.StatusTodo {
			t.Fatalf("title click changed task = %+v, %v", stored, err)
		}
		return
	}
	t.Fatalf("ship title was not rendered:\n%s", ansi.Strip(surface.Content))
}

func TestPointerKillReasonUsesExistingWritePath(t *testing.T) {
	m, backend, tasks := actionTestModel(t, board.Task{Title: "Pointer kill", Status: board.StatusTodo})
	task := tasks[0]
	m.openKillPrompt(task)
	updateTestModel(t, &m, pointerCommandForLabel(t, &m, "Reason:")())
	for _, value := range "mouse reason" {
		updateTestModel(t, &m, tea.KeyPressMsg{Code: value, Text: string(value)})
	}
	command := updateTestModel(t, &m, pointerCommandForLabel(t, &m, "Kill with reason")())
	if command == nil {
		t.Fatal("pointer kill did not start the existing write path")
	}
	finishActionCommand(t, &m, command)
	stored, err := backend.Task("alice", task.ID)
	if err != nil || stored.Status != board.StatusCancelled {
		t.Fatalf("pointer kill result = %+v, %v", stored, err)
	}
}

func TestPointerChecklistUsesExistingWritePath(t *testing.T) {
	m, backend, tasks := actionTestModel(t, board.Task{
		Title: "Pointer checklist", Status: board.StatusTodo,
		Checks: []board.Check{{Text: "click this check"}, {Text: "leave open"}},
	})
	task := tasks[0]
	m.openChecklist(task)
	command := updateTestModel(t, &m, pointerCommandForLabel(t, &m, "click this check")())
	if command == nil {
		t.Fatal("pointer checklist did not start the existing write path")
	}
	finishActionCommand(t, &m, command)
	stored, err := backend.Task("alice", task.ID)
	if err != nil || !stored.Checks[0].Done || stored.Checks[1].Done {
		t.Fatalf("pointer checklist result = %+v, %v", stored, err)
	}
}

func TestPointerPurgeRequiresTwoVisibleActivations(t *testing.T) {
	m, backend, tasks := actionTestModel(t, board.Task{Title: "Pointer purge", Status: board.StatusCancelled})
	task := tasks[0]
	m.openPurgePrompt(task)
	if command := updateTestModel(t, &m, pointerCommandForLabel(t, &m, "Press Enter to arm permanent delete")()); command != nil {
		t.Fatalf("first pointer purge activation started write: %v", command)
	}
	if !m.action.armed {
		t.Fatal("first pointer purge activation did not arm")
	}
	command := updateTestModel(t, &m, pointerCommandForLabel(t, &m, "ARMED - press Enter again to delete permanently")())
	if command == nil {
		t.Fatal("second pointer purge activation did not start write")
	}
	finishActionCommand(t, &m, command)
	if _, err := backend.Task("alice", task.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("pointer purge retained task: %v", err)
	}
}

func TestPointerDetailExposesTaskLifecycleActions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		task   board.Task
		label  string
		mode   taskActionMode
		closed bool
	}{
		{name: "checklist", task: board.Task{Title: "Todo", Status: board.StatusTodo, Checks: []board.Check{{Text: "open"}}}, label: "Check", mode: taskActionChecklist},
		{name: "cancel", task: board.Task{Title: "Todo", Status: board.StatusTodo}, label: "Kill", mode: taskActionKill},
		{name: "restore", task: board.Task{Title: "Gone", Status: board.StatusCancelled}, label: "Restore", closed: true},
		{name: "purge", task: board.Task{Title: "Gone", Status: board.StatusCancelled}, label: "Purge", mode: taskActionPurge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, backend, tasks := actionTestModel(t, tc.task)
			task := tasks[0]
			load := m.detail.Open(task)
			if load != nil {
				m.detail.Update(load())
			}
			command := pointerCommandForLabel(t, &m, tc.label)
			next := updateTestModel(t, &m, command())
			if tc.closed {
				if m.action.open() || next == nil {
					t.Fatalf("pointer %s did not start restore: action=%#v command=%v", tc.label, m.action, next)
				}
				finishActionCommand(t, &m, next)
				stored, err := backend.Task("alice", task.ID)
				if err != nil || stored.Status != board.StatusTodo {
					t.Fatalf("pointer restore result = %+v, %v", stored, err)
				}
				return
			}
			if m.action.mode != tc.mode {
				t.Fatalf("pointer %s mode = %v, want %v", tc.label, m.action.mode, tc.mode)
			}
		})
	}
}

func TestTaskActionRejectsPointerReleaseFromPriorPrompt(t *testing.T) {
	m, _, tasks := actionTestModel(t, board.Task{Title: "First prompt", Status: board.StatusTodo})
	task := tasks[0]
	m.openKillPrompt(task)
	stale := pointerCommandForLabel(t, &m, "Kill without reason")
	m.action.close()
	m.openKillPrompt(task)
	if command := updateTestModel(t, &m, stale()); command != nil || m.action.busy {
		t.Fatalf("stale task-action release crossed prompt: command=%v busy=%v", command, m.action.busy)
	}
}

func TestAutoShipStopsWhenCandidateDisappears(t *testing.T) {
	m := NewModel(stubBoardReader{}, nil, "u")
	eligible := board.Task{Status: board.StatusTodo, Checks: []board.Check{{Text: "done", Done: true}}}
	if command := m.finishAutoShipRead(autoShipReadyMsg{task: eligible, found: false}); command != nil || m.action.open() || m.action.busy {
		t.Fatalf("missing auto-ship candidate = command:%v action:%#v", command, m.action)
	}
}

func TestSameTaskChecklistRetainsDelayedAutoShip(t *testing.T) {
	m, _, tasks := actionTestModel(t, board.Task{
		Title: "Same detail", Status: board.StatusTodo, Checks: []board.Check{{Text: "done", Done: true}},
	})
	task := tasks[0]
	if command := m.detail.Open(task); command == nil {
		t.Fatal("detail did not schedule its load")
	}
	m.action.mode = taskActionChecklist
	m.action.task = task
	if !m.detail.IsOpen() || m.detail.OwnsInput() || m.autoShipInputOwned() {
		t.Fatalf("same-task checklist ownership = detail:%v input:%v blocked:%v",
			m.detail.IsOpen(), m.detail.OwnsInput(), m.autoShipInputOwned())
	}
}

func TestKillRestoreAndArmedPurgeRouting(t *testing.T) {
	m, backend, tasks := actionTestModel(t, board.Task{Title: "Lifecycle", Status: board.StatusDone})
	task := tasks[0]
	m.boardView.focusTask(m.filteredBoard(), task.ID)
	m.recordShipped(task.ID)

	updateTestModel(t, &m, tea.KeyPressMsg{Code: 'x'})
	if m.action.mode != taskActionKill {
		t.Fatalf("x action = %v, want kill", m.action.mode)
	}
	m.action.reason.SetValue("superseded")
	kill := updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEnter})
	finishActionCommand(t, &m, kill)
	cancelled, err := backend.Task("alice", task.ID)
	if err != nil || cancelled.Status != board.StatusCancelled || m.shippedCount() != 0 {
		t.Fatalf("kill = %+v, %v, shipped=%d", cancelled, err, m.shippedCount())
	}
	if tombstone, found, err := backend.Tombstone("alice", task.ID); err != nil || !found || tombstone.Reason != "superseded" {
		t.Fatalf("kill tombstone = %+v, %v, %v", tombstone, found, err)
	}

	m.boardView.showCancelled = true
	if !m.boardView.focusTask(m.filteredBoard(), task.ID) {
		t.Fatal("focus cancelled task")
	}
	restore := updateTestModel(t, &m, tea.KeyPressMsg{Code: 'r'})
	finishActionCommand(t, &m, restore)
	restored, err := backend.Task("alice", task.ID)
	if err != nil || restored.Status != board.StatusTodo {
		t.Fatalf("restore = %+v, %v", restored, err)
	}
	if _, found, err := backend.Tombstone("alice", task.ID); err != nil || found {
		t.Fatalf("restore tombstone = %v, %v", found, err)
	}
	if command := updateTestModel(t, &m, tea.KeyPressMsg{Code: 'D', Text: "D"}); command != nil || m.action.open() {
		t.Fatalf("live purge opened = command %v action %#v", command, m.action)
	}

	if _, err := backend.CancelTask("alice", task.ID, nil); err != nil {
		t.Fatal(err)
	}
	completeBoardLoad(t, &m, m.startBoardLoad())
	m.boardView.showCancelled = true
	m.boardView.focusTask(m.filteredBoard(), task.ID)
	updateTestModel(t, &m, tea.KeyPressMsg{Code: 'D', Text: "D"})
	if m.action.mode != taskActionPurge || m.action.armed {
		t.Fatalf("purge prompt = %#v", m.action)
	}
	if command := updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEnter}); command != nil || !m.action.armed {
		t.Fatalf("first purge confirmation = command %v armed %v", command, m.action.armed)
	}
	purge := updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEnter})
	finishActionCommand(t, &m, purge)
	if _, err := backend.Task("alice", task.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("purged task lookup = %v", err)
	}
}

func TestShippedRecordPersistenceRolloverAndIdentity(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	m := NewModel(stubBoardReader{}, nil, "alice")
	m.now = func() time.Time { return now }
	m.renderedAt = now
	m.shipped = shippedRecord{Date: "2026-08-18", IDs: []string{"a", "a", "", "b"}}
	if got := m.shippedCount(); got != 2 {
		t.Fatalf("normalized shipped count = %d", got)
	}
	preferences := m.preferences()
	if !preferencesEqual(preferences, tuiPreferences{Shipped: shippedRecord{Date: "2026-08-18", IDs: []string{"a", "b"}}}) {
		t.Fatalf("shipped preferences = %+v", preferences)
	}
	m.recordShipped("c")
	m.unrecordShipped("a")
	if got := m.shippedCount(); got != 2 {
		t.Fatalf("identity tally = %d", got)
	}
	now = now.Add(24 * time.Hour)
	m.renderedAt = now
	if got := m.shippedCount(); got != 0 || m.shipped.Date != "2026-08-18" {
		// shippedCount uses a value receiver so rendering cannot mutate state;
		// the next actual record operation performs the rollover.
		t.Fatalf("value rollover = count %d record %+v", got, m.shipped)
	}
	m.recordShipped("next")
	if m.shipped.Date != "2026-08-19" || len(m.shipped.IDs) != 1 || m.shipped.IDs[0] != "next" {
		t.Fatalf("record rollover = %+v", m.shipped)
	}
}

func TestTaskActionOverlaySanitizesAndStaysBounded(t *testing.T) {
	m := NewModel(stubBoardReader{}, nil, "alice")
	m.width, m.height = 24, 9
	task := board.Task{ID: "x", Title: "bad\x1b[2Jtitle", Status: board.StatusTodo,
		Checks: []board.Check{{Text: "line\x1b]8;;https://evil.invalid\a"}}}
	m.openChecklist(task)
	view := m.View().Content
	if strings.Contains(view, "\x1b[2J") || strings.Contains(view, "evil.invalid") {
		t.Fatalf("action overlay leaked control sequence: %q", view)
	}
	for _, line := range strings.Split(view, "\n") {
		if ansi.StringWidth(line) > m.width {
			t.Fatalf("action overlay width = %d: %q", ansi.StringWidth(line), line)
		}
	}
}

type faultActionStore struct {
	*store.Store
	boardErr, moveErr, updateErr, cancelErr, deleteErr error
}

func (s *faultActionStore) Board(user string) (board.Board, error) {
	if s.boardErr != nil {
		return board.Board{}, s.boardErr
	}
	return s.Store.Board(user)
}

func (s *faultActionStore) UpdateAndMoveTask(user, id string, patch store.TaskPatch, target *board.Status, index *int, guard func(board.Task) error) (board.Task, error) {
	if s.moveErr != nil {
		return board.Task{}, s.moveErr
	}
	return s.Store.UpdateAndMoveTask(user, id, patch, target, index, guard)
}

func (s *faultActionStore) UpdateAndMoveTaskIfFieldsMatch(user, id string, expected, patch store.TaskPatch, target *board.Status, index *int, guard func(board.Task) error) (board.Task, error) {
	if s.moveErr != nil {
		return board.Task{}, s.moveErr
	}
	return s.Store.UpdateAndMoveTaskIfFieldsMatch(user, id, expected, patch, target, index, guard)
}

func (s *faultActionStore) UpdateTaskIfFieldsMatch(user, id string, expected, patch store.TaskPatch) (board.Task, error) {
	if s.updateErr != nil {
		return board.Task{}, s.updateErr
	}
	return s.Store.UpdateTaskIfFieldsMatch(user, id, expected, patch)
}

func (s *faultActionStore) CancelTask(user, id string, reason *string) (board.Task, error) {
	if s.cancelErr != nil {
		return board.Task{}, s.cancelErr
	}
	return s.Store.CancelTask(user, id, reason)
}

func (s *faultActionStore) DeleteCancelledTask(user, id string) (board.Task, error) {
	if s.deleteErr != nil {
		return board.Task{}, s.deleteErr
	}
	return s.Store.DeleteCancelledTask(user, id)
}

func TestTaskActionKeyRoutingEdges(t *testing.T) {
	m, _, tasks := actionTestModel(t, board.Task{
		Title: "Keys", Status: board.StatusTodo,
		Checks: []board.Check{{Text: "first"}, {Text: "second"}},
	})
	task := tasks[0]
	m.boardView.focusTask(m.filteredBoard(), task.ID)

	if handled, command := m.handleSelectedTaskAction("unknown"); handled || command != nil {
		t.Fatalf("unknown action = %v, %v", handled, command)
	}
	if handled, _ := m.handleSelectedTaskAction("r"); !handled || !m.actionStatusError {
		t.Fatal("live restore was not refused")
	}
	empty := task
	empty.Checks = nil
	m.board.Tasks[0] = empty
	if handled, _ := m.handleSelectedTaskAction("t"); !handled || !strings.Contains(m.actionStatus, "empty") {
		t.Fatal("empty checklist was not refused")
	}
	m.board.Tasks[0] = task

	m.openShipPrompt(task, 0)
	for _, key := range []string{"left", "right", "tab", "shift+tab", "h", "l"} {
		m.updateShipPrompt(key)
	}
	m.action.choice = 0
	if command := m.updateShipPrompt("enter"); command != nil || m.action.open() {
		t.Fatalf("ship cancel = %v, %#v", command, m.action)
	}

	m.openKillPrompt(task)
	for _, key := range []tea.KeyPressMsg{
		{Code: tea.KeyTab},
		{Code: tea.KeyTab, Mod: tea.ModShift},
		{Code: 'a', Text: "a"},
	} {
		m.updateKillPrompt(key)
	}
	m.action.reason.SetValue("")
	m.action.choice = 2
	if command := m.updateKillPrompt(tea.KeyPressMsg{Code: tea.KeyEnter}); command != nil || m.action.errorText == "" {
		t.Fatalf("blank kill reason = command %v error %q", command, m.action.errorText)
	}
	m.action.choice = 0
	m.updateKillPrompt(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.action.open() {
		t.Fatal("kill cancel stayed open")
	}

	m.openChecklist(task)
	m.updateChecklistPrompt("up")
	if m.action.checkIndex != 1 {
		t.Fatalf("checklist up wrap = %d", m.action.checkIndex)
	}
	m.updateChecklistPrompt("down")
	if m.action.checkIndex != 0 {
		t.Fatalf("checklist down wrap = %d", m.action.checkIndex)
	}
	m.action.task.Checks = nil
	m.updateChecklistPrompt("space")
	if m.action.open() {
		t.Fatal("empty open checklist stayed open")
	}

	m.openPurgePrompt(board.Task{ID: "cancelled", Title: "Gone", Status: board.StatusCancelled})
	if command := m.updateTaskActionKey(tea.KeyPressMsg{Code: tea.KeyEscape}); command != nil || m.action.open() {
		t.Fatalf("escape action = %v, %#v", command, m.action)
	}
	m.action.busy = true
	if command := m.updateTaskActionKey(tea.KeyPressMsg{Code: 'q'}); command == nil || !m.stopped {
		t.Fatal("hung action did not preserve q quit")
	}
}

func TestUnsupportedActionStoreAndAutoShipEdges(t *testing.T) {
	task := board.Task{ID: "task", Title: "Unsupported", Status: board.StatusTodo, Checks: []board.Check{{Text: "one"}}}
	m := NewModel(stubBoardReader{board: board.Board{Tasks: []board.Task{task}}}, nil, "alice")
	completeBoardLoad(t, &m, m.Init())

	m.openShipPrompt(task, 0)
	if command := m.startShip(false); command != nil || m.action.errorText == "" {
		t.Fatal("unsupported ship was not reported")
	}
	m.openKillPrompt(task)
	if command := m.startKill(""); command != nil || m.action.errorText == "" {
		t.Fatal("unsupported kill was not reported")
	}
	m.action.close()
	if command := m.startRestore(board.Task{ID: "c", Title: "c", Status: board.StatusCancelled}); command != nil || !m.actionStatusError {
		t.Fatal("unsupported restore was not reported")
	}
	m.openPurgePrompt(board.Task{ID: "c", Title: "c", Status: board.StatusCancelled})
	if command := m.startPurge(); command != nil || m.action.errorText == "" {
		t.Fatal("unsupported purge was not reported")
	}
	m.openChecklist(task)
	if command := m.startChecklistToggle(); command != nil || m.action.errorText == "" {
		t.Fatal("unsupported checklist was not reported")
	}
	if command := m.readAutoShipCandidate(task.ID); command != nil {
		t.Fatalf("unsupported auto-ship read = %v", command)
	}

	for _, candidate := range []autoShipReadyMsg{
		{err: errors.New("read failed")},
		{},
		{found: true, task: board.Task{Status: board.StatusDone, Checks: []board.Check{{Done: true}}}},
		{found: true, task: board.Task{Status: board.StatusTodo, Checks: []board.Check{{Done: false}}}},
	} {
		if command := m.finishAutoShipRead(candidate); command != nil {
			t.Fatalf("noncandidate auto-ship = %v for %#v", command, candidate)
		}
	}
	other := task
	other.ID = "other"
	m.openKillPrompt(other)
	ready := autoShipReadyMsg{found: true, task: board.Task{ID: task.ID, Status: board.StatusTodo, Checks: []board.Check{{Done: true}}}}
	if command := m.finishAutoShipRead(ready); command != nil || m.action.task.ID != other.ID {
		t.Fatal("auto-ship replaced unrelated action")
	}
}

func TestActionFailureReloadAndViewEdges(t *testing.T) {
	m, backend, tasks := actionTestModel(t, board.Task{Title: "Faults", Status: board.StatusTodo, Checks: []board.Check{{Text: "one"}}})
	task := tasks[0]
	faults := &faultActionStore{Store: backend}
	m.actionStore = faults

	for kind, want := range map[taskActionKind]string{
		actionShip: "Ship", actionKill: "Cancel", actionRestore: "Restore", actionPurge: "Permanent delete",
	} {
		m.action = newTaskActionState()
		m.action.mode = taskActionKill
		m.action.task = task
		m.action.busy = true
		m.finishTaskAction(taskActionStoredMsg{kind: kind, taskID: task.ID, title: task.Title,
			board: m.board, writeErr: errors.New("refused")})
		if !strings.Contains(m.actionStatus, want+" failed") || m.action.errorText == "" {
			t.Fatalf("%v failure = %q / %q", kind, m.actionStatus, m.action.errorText)
		}
	}

	m.action = newTaskActionState()
	m.action.mode = taskActionChecklist
	m.action.task = task
	m.action.busy = true
	m.finishChecklistWrite(checklistStoredMsg{taskID: task.ID, board: m.board, writeErr: errors.New("stale")})
	if !strings.Contains(m.action.errorText, "Checklist update failed") {
		t.Fatalf("checklist failure = %q", m.action.errorText)
	}
	m.action.busy = true
	load := m.finishChecklistWrite(checklistStoredMsg{taskID: task.ID, task: task, reloadErr: errors.New("reload")})
	if load == nil || !strings.Contains(m.actionStatus, "canonical reload failed") {
		t.Fatalf("checklist reload failure = %v, %q", load, m.actionStatus)
	}
	m.loading = false
	m.reloadPending = true
	if command := m.reloadAfterActionFailure(nil); command == nil || !m.loading || m.reloadPending {
		t.Fatalf("pending action reload = %v loading %v pending %v", command, m.loading, m.reloadPending)
	}
	m.loading = false

	views := []taskActionState{
		{mode: taskActionShip, task: task, warning: shipWarning{blocked: true}, busy: true, errorText: "no"},
		{mode: taskActionKill, task: task, reason: newTaskActionState().reason, busy: true, errorText: "no"},
		{mode: taskActionChecklist, task: task, busy: true, errorText: "no"},
		{mode: taskActionPurge, task: task, armed: true, busy: true, errorText: "no"},
	}
	for _, action := range views {
		m.action = action
		if rendered := m.renderTaskAction(60); !strings.Contains(rendered, "saving") || !strings.Contains(rendered, "error") {
			t.Fatalf("action view %v = %q", action.mode, rendered)
		}
	}
	if got := m.renderTaskAction(1); ansi.StringWidth(got) > 1 {
		t.Fatalf("one-cell action = %q", got)
	}
	if got := fitActionFrame("12345\nabcde\nlast", 3, 2); got != "123\nabc" {
		t.Fatalf("fitActionFrame = %q", got)
	}
}

func TestActionSuccessDetailAndDispatchEdges(t *testing.T) {
	task := board.Task{ID: "task", Title: "Detail task", Status: board.StatusTodo, Checks: []board.Check{{Done: true}}}
	reader := &mutableDetailReader{board: board.Board{Tasks: []board.Task{task}}}
	m := NewModel(reader, nil, "alice")
	completeBoardLoad(t, &m, m.Init())
	detailLoad := updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEnter})
	updateTestModel(t, &m, detailLoad())
	if selected, ok := m.actionTask(); !ok || selected.ID != task.ID {
		t.Fatalf("detail action task = %+v, %v", selected, ok)
	}
	refresh := m.adoptActionBoard(board.Board{Tasks: []board.Task{task}}, task.ID)
	if refresh == nil {
		t.Fatal("detail adoption did not refresh enrichment")
	}
	updateTestModel(t, &m, refresh())
	if reader.commentLoads < 2 {
		t.Fatalf("detail refresh loads = %d", reader.commentLoads)
	}
	m.adoptActionBoard(board.Board{}, task.ID)
	if m.detail.IsOpen() {
		t.Fatal("purged detail stayed open")
	}

	for kind, want := range map[taskActionKind]string{
		actionShip:    "Shipped X",
		actionKill:    "Cancelled X",
		actionRestore: "Restored X to To Do",
		actionPurge:   "Permanently deleted X",
	} {
		if got := actionSuccess(kind, "X"); got != want {
			t.Errorf("actionSuccess(%v) = %q, want %q", kind, got, want)
		}
	}
	if err := completionGuard(board.Task{ID: "clear", Title: "Clear"}); err != nil {
		t.Fatalf("clear completion guard = %v", err)
	}
	if err := completionGuard(board.Task{ID: "open", Title: "Open", Checks: []board.Check{{Text: "x"}}}); err == nil {
		t.Fatal("open completion guard succeeded")
	}
	m.recordShipped("same")
	m.recordShipped("same")
	if m.shippedCount() != 1 {
		t.Fatalf("duplicate shipped record = %+v", m.shipped)
	}

	for _, message := range []tea.Msg{
		tea.KeyPressMsg{Code: tea.KeyEscape},
		taskActionStoredMsg{}, checklistStoredMsg{}, autoShipCheckMsg{}, autoShipReadyMsg{}, struct{}{},
	} {
		m.action.close()
		_ = m.updateTaskAction(message)
	}
}

func TestMoveOutOfDoneRemovesShippedIdentity(t *testing.T) {
	m, backend, tasks := actionTestModel(t, board.Task{Title: "Reopen", Status: board.StatusDone})
	task := tasks[0]
	m.recordShipped(task.ID)
	m.boardView.focusTask(m.filteredBoard(), task.ID)
	updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeySpace})
	updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyLeft})
	drop := updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEnter})
	finishActionCommand(t, &m, drop)
	current, err := backend.Task("alice", task.ID)
	if err != nil || current.Status != board.StatusDoing || m.shippedCount() != 0 {
		t.Fatalf("reopen = %+v, %v, shipped=%d", current, err, m.shippedCount())
	}
}
