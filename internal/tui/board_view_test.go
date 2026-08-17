package tui

import (
	"errors"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/board"
)

func boardViewFixture(now time.Time) board.Board {
	return board.Board{Title: "Roadmap", Tasks: []board.Task{
		{ID: "todo-1", Seq: 7, Emoji: "🚀", Title: "Ship terminal board", Status: board.StatusTodo, Prio: 1, Blocked: true, Due: "2026-08-17", Effort: "M", Tags: []string{"backend", "type::feature"}, CreatedAt: now.Add(-2 * time.Hour), MovedAt: now.Add(-2 * time.Hour)},
		{ID: "todo-2", Seq: 8, Title: "Second", Status: board.StatusTodo, Prio: 4, CreatedAt: now.Add(-49 * time.Hour), MovedAt: now.Add(-49 * time.Hour)},
		{ID: "doing-1", Seq: 9, Title: "Working", Status: board.StatusDoing, Prio: 2, Due: "2026-08-18", CreatedAt: now.Add(-72 * time.Hour), MovedAt: now.Add(-3 * time.Hour)},
		{ID: "done-1", Seq: 10, Title: "Released", Status: board.StatusDone, Prio: 3, Due: "2026-08-20", CreatedAt: now.Add(-96 * time.Hour), MovedAt: now.Add(-time.Hour)},
		{ID: "cancelled-1", Seq: 11, Title: "Dropped", Status: board.StatusCancelled, Prio: 3, CreatedAt: now.Add(-24 * time.Hour), MovedAt: now.Add(-time.Hour)},
	}}
}

func plain(value string) string { return ansi.Strip(value) }

func TestAgeChipParity(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		status  board.Status
		created time.Time
		moved   time.Time
		want    string
	}{
		{"todo new", board.StatusTodo, now.Add(-23 * time.Hour), now, "new"},
		{"todo old", board.StatusTodo, now.Add(-49 * time.Hour), now, "2d old"},
		{"doing minimum hour", board.StatusDoing, now.Add(-10 * day), now.Add(-10 * time.Minute), "1h here"},
		{"doing hours", board.StatusDoing, now.Add(-10 * day), now.Add(-23 * time.Hour), "23h here"},
		{"doing days", board.StatusDoing, now.Add(-10 * day), now.Add(-49 * time.Hour), "2d here"},
		{"done", board.StatusDone, now.Add(-10 * day), now, "shipped"},
		{"cancelled uses created", board.StatusCancelled, now.Add(-24 * time.Hour), now, "1d old"},
		{"future clamps", board.StatusTodo, now.Add(time.Hour), now, "new"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			task := board.Task{Status: test.status, CreatedAt: test.created, MovedAt: test.moved}
			if got := ageChip(task, now); got != test.want {
				t.Fatalf("ageChip() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestDueChipParity(t *testing.T) {
	now := time.Date(2026, 8, 17, 23, 59, 0, 0, time.FixedZone("local", -7*60*60))
	tests := []struct {
		due     string
		label   string
		overdue bool
	}{
		{"2026-08-15", "overdue · 2d", true},
		{"2026-08-17", "today", false},
		{"2026-08-18", "tomorrow", false},
		{"2026-08-20", "in 3d", false},
		{"not-a-date", "not-a-date", false},
	}
	for _, test := range tests {
		t.Run(test.due, func(t *testing.T) {
			label, overdue := dueChip(test.due, now)
			if label != test.label || overdue != test.overdue {
				t.Fatalf("dueChip() = %q,%v, want %q,%v", label, overdue, test.label, test.overdue)
			}
		})
	}
}

func TestLabelColorUsesWebHash(t *testing.T) {
	tests := []struct {
		tag  string
		want color.Color
	}{
		{"backend", lipgloss.Color("#ff7b54")},
		{"🙂", lipgloss.Color("#ff7b54")}, // JavaScript length is two UTF-16 units.
		{"", lipgloss.Color("#ff7b54")},
	}
	for _, test := range tests {
		if got := labelColor(test.tag); !reflect.DeepEqual(got, test.want) {
			t.Errorf("labelColor(%q) = %v, want %v", test.tag, got, test.want)
		}
	}
	if got := plain(labelChip("type::feature")); got != "[type:feature]" {
		t.Fatalf("scoped chip = %q", got)
	}
	if got := plain(labelChip("backend")); got != "[#backend]" {
		t.Fatalf("plain chip = %q", got)
	}
	if got := plain(labelChip("broken::")); got != "[#broken::]" {
		t.Fatalf("empty scoped value chip = %q", got)
	}
}

func TestBoardNavigationAndSelection(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	current := boardViewFixture(now)
	var state boardViewState

	if selected, ok := state.selectedTask(current); !ok || selected.ID != "todo-1" {
		t.Fatalf("initial selection = %+v, %v", selected, ok)
	}
	tests := []struct {
		key        string
		wantColumn int
		wantRow    int
		wantID     string
	}{
		{"j", 0, 1, "todo-2"},
		{"down", 0, 1, "todo-2"},
		{"k", 0, 0, "todo-1"},
		{"right", 1, 0, "doing-1"},
		{"l", 2, 0, "done-1"},
		{"tab", 0, 0, "todo-1"},
		{"h", 2, 0, "done-1"},
		{"shift+tab", 1, 0, "doing-1"},
		{"1", 0, 0, "todo-1"},
		{"4", 0, 0, "todo-1"}, // hidden Cancelled is not addressable.
	}
	for _, test := range tests {
		state.handleKey(test.key, current)
		selected, ok := state.selectedTask(current)
		if !ok || state.column != test.wantColumn || state.rows[state.column] != test.wantRow || selected.ID != test.wantID {
			t.Fatalf("after %q = state %+v selected %+v,%v", test.key, state, selected, ok)
		}
	}
	if action := state.handleKey("c", current); action != boardToggledCancelled || !state.showCancelled {
		t.Fatalf("toggle on = %+v action=%v", state, action)
	}
	state.handleKey("4", current)
	if selected, ok := state.selectedTask(current); !ok || selected.ID != "cancelled-1" {
		t.Fatalf("cancelled selection = %+v,%v", selected, ok)
	}
	state.handleKey("c", current)
	if state.column != 2 || state.showCancelled {
		t.Fatalf("toggle off did not return focus to Done: %+v", state)
	}
	if action := state.handleKey("?", current); action != boardUnchanged {
		t.Fatalf("unknown key action = %v", action)
	}
}

func TestFocusAndRefreshPreserveTaskIdentity(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	before := boardViewFixture(now)
	state := boardViewState{showCancelled: true}
	state.focusTask(before, "todo-2")
	if state.column != 0 || state.rows[0] != 1 {
		t.Fatalf("focusTask = %+v", state)
	}
	after := before
	after.Tasks = append([]board.Task(nil), before.Tasks...)
	after.Tasks[0], after.Tasks[1] = after.Tasks[1], after.Tasks[0]
	state.adoptBoard(before, after)
	if selected, ok := state.selectedTask(after); !ok || selected.ID != "todo-2" || state.rows[0] != 0 {
		t.Fatalf("selection after reorder = %+v,%v state=%+v", selected, ok, state)
	}
	state.focusColumn(board.StatusDoing, after)
	if state.column != 1 {
		t.Fatalf("focusColumn = %+v", state)
	}
	state.focusTask(after, "missing")
	state.showCancelled = false
	state.focusTask(after, "cancelled-1")
	if state.column != 1 {
		t.Fatalf("hidden cancelled task stole focus: %+v", state)
	}
	after.Tasks = nil
	state.adoptBoard(before, after)
	if _, ok := state.selectedTask(after); ok || state.rows[state.column] != 0 {
		t.Fatalf("empty selection = %+v state=%+v", after, state)
	}
}

func TestBoardRenderResponsiveFullCardsAndMouse(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	m := NewModel(stubBoardReader{}, nil, "alice")
	m.loading = false
	m.board = boardViewFixture(now)
	m.now = func() time.Time { return now }
	m.width, m.height = 160, 22
	m.boardView.showCancelled = true

	content, hits := m.renderBoard()
	text := plain(content)
	for _, want := range []string{
		"[1 TO DO  2]", "2 DOING  1", "3 DONE  1", "4 CANCELLED  1",
		"🚀 Ship terminal board", "#7", "new", "P1", "[⛔ blocked]", "[today]", "[M]", "[#backend]", "[type:feature]",
		"3h here", "shipped", "1d old", "c cancelled:on",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("wide render missing %q:\n%s", want, text)
		}
	}
	if len(hits) < 9 { // four columns and five cards.
		t.Fatalf("render hits = %+v", hits)
	}

	var cardHit boardHit
	for _, hit := range hits {
		if hit.taskID == "doing-1" {
			cardHit = hit
			break
		}
	}
	command := boardMouseHandler(hits)(tea.MouseClickMsg{X: cardHit.x0 + 1, Y: cardHit.y0, Button: tea.MouseLeft})
	if command == nil {
		t.Fatal("card click was not hit")
	}
	updateTestModel(t, &m, command())
	if selected, ok := m.selectedTask(); !ok || selected.ID != "doing-1" {
		t.Fatalf("mouse selection = %+v,%v", selected, ok)
	}
	if command := boardMouseHandler(hits)(tea.MouseReleaseMsg{}); command != nil {
		t.Fatalf("release produced command %v", command)
	}
	if command := boardMouseHandler(hits)(tea.MouseClickMsg{X: 999, Y: 999, Button: tea.MouseLeft}); command != nil {
		t.Fatalf("off-board click produced command %v", command)
	}

	m.width = 99
	m.boardView.column = 2
	narrow := plain(m.render())
	if !strings.Contains(narrow, "[3 DONE  1]") || strings.Contains(narrow, "TO DO") || strings.Contains(narrow, "CANCELLED") {
		t.Fatalf("narrow focused column:\n%s", narrow)
	}
	for _, line := range strings.Split(m.render(), "\n") {
		if ansi.StringWidth(line) > m.width {
			t.Fatalf("narrow line width %d: %q", ansi.StringWidth(line), line)
		}
	}
}

func TestBoardRenderScrollsSelectionIntoShortColumn(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	m := NewModel(stubBoardReader{}, nil, "u")
	m.loading = false
	m.now = func() time.Time { return now }
	m.width, m.height = 80, 8
	for i := 0; i < 8; i++ {
		m.board.Tasks = append(m.board.Tasks, board.Task{ID: fmt.Sprintf("t-%d", i), Title: fmt.Sprintf("task %d", i), Status: board.StatusTodo, Prio: 3, CreatedAt: now})
	}
	m.boardView.rows[0] = 7
	text := plain(m.render())
	if !strings.Contains(text, "task 7") || strings.Contains(text, "task 0") {
		t.Fatalf("selected card not scrolled into view:\n%s", text)
	}
}

func TestBoardViewSmallHelpers(t *testing.T) {
	if got := splitWidths(10, 3); !reflect.DeepEqual(got, []int{3, 3, 2}) {
		t.Fatalf("splitWidths = %v", got)
	}
	if got := splitWidths(1, 1); !reflect.DeepEqual(got, []int{1}) {
		t.Fatalf("one split = %v", got)
	}
	if statusIndex("unknown") != 0 || statusLabel("unknown") != "" {
		t.Fatal("unknown status helpers changed")
	}
	if got := plain(priorityChip(99)); got != "P3" {
		t.Fatalf("invalid priority fallback = %q", got)
	}
	if got := padLine("abcdef", 3, "-"); got != "abc" {
		t.Fatalf("truncated pad = %q", got)
	}
	if got := wrapTokens([]string{"one", "two", "verylong"}, 4); !reflect.DeepEqual(got, []string{"one", "two", "very"}) {
		t.Fatalf("wrapTokens = %q", got)
	}
	if got := visibleCardStart([]string{"a", "", "b"}, []string{"a", "", "b"}, 1, 2); got != 1 {
		t.Fatalf("visible start = %d", got)
	}
	column := Model{board: board.Board{Tasks: []board.Task{{ID: "x", Title: "x", Status: board.StatusTodo}}}, boardView: boardViewState{}, now: time.Now}.renderBoardColumn(board.StatusTodo, 2, 4)
	if len(column.lines) != 4 || !strings.Contains(column.lines[0], "TO") {
		t.Fatalf("tiny column = %+v", column)
	}
}

func TestCancelledPreferenceRoundTripAndCommand(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	if got, err := loadCancelledPreference(); err != nil || got {
		t.Fatalf("missing preference = %v,%v", got, err)
	}
	if err := saveCancelledPreference(true); err != nil {
		t.Fatal(err)
	}
	if got, err := loadCancelledPreference(); err != nil || !got {
		t.Fatalf("saved preference = %v,%v", got, err)
	}
	path := filepath.Join(config, "kb", "tui.json")
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("preference mode = %v,%v", info, err)
	}
	if err := saveCancelledPreference(false); err != nil {
		t.Fatal(err)
	}
	if got, err := loadCancelledPreference(); err != nil || got {
		t.Fatalf("cleared preference = %v,%v", got, err)
	}
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCancelledPreference(); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("malformed preference error = %v", err)
	}

	m := NewModel(stubBoardReader{}, nil, "u")
	m.boardView.showCancelled = true
	m.saveCancelled = func(show bool) error {
		if !show {
			t.Fatal("saved wrong toggle")
		}
		return errors.New("disk full")
	}
	message := m.queueCancelledPreference()()
	updateTestModel(t, &m, message)
	if m.preferenceErr == nil || !strings.Contains(m.render(), "disk full") {
		t.Fatalf("preference failure = %+v", m.preferenceErr)
	}
	m.saveCancelled = nil
	if command := m.queueCancelledPreference(); command != nil {
		t.Fatalf("nil preference saver returned %v", command)
	}
}

func TestCancelledPreferenceWritesSerializeLatestToggle(t *testing.T) {
	var saved []bool
	m := NewModel(stubBoardReader{}, nil, "u")
	m.saveCancelled = func(show bool) error {
		saved = append(saved, show)
		return nil
	}

	m.boardView.handleKey("c", m.board)
	first := m.queueCancelledPreference()
	m.boardView.handleKey("c", m.board)
	if command := m.queueCancelledPreference(); command != nil {
		t.Fatal("overlapping toggle started a concurrent preference write")
	}
	if !m.prefSaving || m.prefPending == nil || *m.prefPending {
		t.Fatalf("queued preference state = %+v", m)
	}
	second := updateTestModel(t, &m, first())
	if second == nil || !m.prefSaving || m.prefPending != nil {
		t.Fatalf("serialized successor = %+v command=%v", m, second)
	}
	if next := updateTestModel(t, &m, second()); next != nil || m.prefSaving {
		t.Fatalf("final preference state = %+v command=%v", m, next)
	}
	if !reflect.DeepEqual(saved, []bool{true, false}) {
		t.Fatalf("saved toggles = %v", saved)
	}
}
