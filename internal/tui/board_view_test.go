package tui

import (
	"bytes"
	"errors"
	"fmt"
	"image/color"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
	"github.com/RandomCodeSpace/kb/internal/tui/widget"
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

// TestLabelColorUsesWebHash pins the label wheel the board has always used.
// The hash and the pill vocabulary now live in the widget package (spec
// sections 1.6 and 3.5), but the colors a tag lands on are unchanged.
func TestLabelColorUsesWebHash(t *testing.T) {
	styles := theme.New(true)
	for _, tag := range []string{
		"backend",
		"🙂", // JavaScript length is two UTF-16 units.
		"",
	} {
		if got := theme.LabelSlot(widget.LabelWheel(tag)); got != theme.Label1 {
			t.Errorf("label slot(%q) = %v, want %v", tag, got, theme.Label1)
		}
	}
	for _, test := range []struct {
		tag  string
		want string
	}{
		{"type::feature", "▐type:feature▌"},
		{"backend", "▐#backend▌"},
		{"broken::", "▐#broken::▌"},
	} {
		if got := plain(widget.Label(styles, test.tag, theme.Card, false)); got != test.want {
			t.Errorf("label pill(%q) = %q, want %q", test.tag, got, test.want)
		}
	}
	if got := plain(widget.Label(styles, "type::feature", theme.Card, true)); got != "feature" {
		t.Fatalf("compact scoped label = %q", got)
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

func TestRefreshNormalizesSelectionAfterExternalHiddenMove(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	before := boardViewFixture(now)

	t.Run("clamps row in original column", func(t *testing.T) {
		state := boardViewState{}
		state.focusTask(before, "todo-2")
		after := before
		after.Tasks = append([]board.Task(nil), before.Tasks...)
		after.Tasks[1].Status = board.StatusCancelled
		state.adoptBoard(before, after)
		selected, ok := state.selectedTask(after)
		if !ok || selected.ID != "todo-1" || state.column != 0 || state.rows[0] != 0 {
			t.Fatalf("normalized selection = %+v,%v state=%+v", selected, ok, state)
		}
	})

	t.Run("moves to next non-empty visible column", func(t *testing.T) {
		state := boardViewState{}
		state.focusTask(before, "todo-2")
		after := before
		after.Tasks = append([]board.Task(nil), before.Tasks...)
		for i := range after.Tasks {
			if after.Tasks[i].Status == board.StatusTodo {
				after.Tasks[i].Status = board.StatusCancelled
			}
		}
		state.adoptBoard(before, after)
		selected, ok := state.selectedTask(after)
		if !ok || selected.ID != "doing-1" || state.column != 1 || state.rows[1] != 0 {
			t.Fatalf("next visible selection = %+v,%v state=%+v", selected, ok, state)
		}
	})

	t.Run("all visible columns empty", func(t *testing.T) {
		state := boardViewState{}
		state.focusTask(before, "todo-2")
		after := board.Board{Title: before.Title, Tasks: []board.Task{before.Tasks[1]}}
		after.Tasks[0].Status = board.StatusCancelled
		state.adoptBoard(before, after)
		if selected, ok := state.selectedTask(after); ok || state.column != 0 || state.rows[0] != 0 {
			t.Fatalf("empty visible selection = %+v,%v state=%+v", selected, ok, state)
		}
	})
}

func TestBoardRenderResponsiveFullCardsAndMouse(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	m := NewModel(stubBoardReader{}, nil, "alice")
	m.loading = false
	m.board = boardViewFixture(now)
	m.now = func() time.Time { return now }
	m.renderedAt = now
	m.width, m.height = 160, 40
	m.boardView.showCancelled = true

	content, hits := m.renderBoard()
	text := plain(content)
	for _, want := range []string{
		"▸ 1 TO DO", "● 2 DOING", "● 3 DONE", "● 4 CANCELLED", "2 cards · 1 blocked",
		"🚀 Ship terminal board", "#7", "new", "P1", "▐blocked▌", "▐today▌", "◇M", "▐#backend▌", "▐type:feature▌",
		"3h here", "shipped", "1d old", "c cancelled:on",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("wide render missing %q:\n%s", want, text)
		}
	}
	// Compaction drops the description, the meta line and the pill end caps.
	m.height = 22
	compact := plain(m.render())
	for _, want := range []string{"P1 new ⛔ !today ◇M #backend feature", "P2 3h here !tomorrow"} {
		if !strings.Contains(compact, want) {
			t.Errorf("compact render missing %q:\n%s", want, compact)
		}
	}
	if strings.Contains(compact, "cards · ") || strings.Contains(compact, "▐blocked▌") {
		t.Errorf("compact render kept normal-density chrome:\n%s", compact)
	}
	m.height = 40
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
	command := boardMouseHandler(hits, false)(tea.MouseClickMsg{X: cardHit.x0 + 1, Y: cardHit.y0, Button: tea.MouseLeft})
	if command == nil {
		t.Fatal("card click was not hit")
	}
	updateTestModel(t, &m, command())
	if selected, ok := m.selectedTask(); !ok || selected.ID != "doing-1" {
		t.Fatalf("mouse selection = %+v,%v", selected, ok)
	}
	if command := boardMouseHandler(hits, false)(tea.MouseReleaseMsg{}); command != nil {
		t.Fatalf("release produced command %v", command)
	}
	if command := boardMouseHandler(hits, false)(tea.MouseClickMsg{X: 999, Y: 999, Button: tea.MouseLeft}); command != nil {
		t.Fatalf("off-board click produced command %v", command)
	}

	m.width = 99
	m.boardView.column = 2
	narrow := plain(m.render())
	if !strings.Contains(narrow, "▸ 3 DONE") || strings.Contains(narrow, "TO DO") || strings.Contains(narrow, "CANCELLED") {
		t.Fatalf("narrow focused column:\n%s", narrow)
	}
	for _, line := range strings.Split(m.render(), "\n") {
		if ansi.StringWidth(line) > m.width {
			t.Fatalf("narrow line width %d: %q", ansi.StringWidth(line), line)
		}
	}
}

// TestBoardCardsColorGolden is the palette golden of spec section 6.4: the
// depth model is background color, so the board's second golden pins truecolor
// and records the shade tiers, the band hues, the priority rails and the pills.
func TestBoardCardsColorGolden(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	fixture := boardViewFixture(now)
	fixture.Tasks[0].Desc = "Pointer capture leaks when the column scrolls under the drag ghost"
	m := NewModel(stubBoardReader{board: fixture}, nil, "alice")
	m.loading = false
	m.board = fixture
	m.now = func() time.Time { return now }
	m.renderedAt = now
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = sized.(Model)
	tm := teatest.NewTestModel(t, m,
		teatest.WithInitialTermSize(120, 40),
		teatest.WithProgramOptions(theme.PinColor()),
	)
	t.Cleanup(func() { _ = tm.Quit() })
	var captured bytes.Buffer
	teatest.WaitFor(t, io.TeeReader(tm.Output(), &captured), func(output []byte) bool {
		return bytes.Contains(output, []byte("Ship terminal board"))
	}, teatest.WithDuration(5*time.Second), teatest.WithCheckInterval(10*time.Millisecond))
	frame, ok := finalFullScreenFrame(captured.Bytes())
	if !ok {
		t.Fatal("teatest output did not contain a full-screen frame")
	}
	grid, err := renderedCellGrid(frame, 120, 40)
	if err != nil {
		t.Fatal(err)
	}
	teatest.RequireEqualOutput(t, grid)
	tm.Send(tea.KeyPressMsg{Code: 'q'})
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))
}

// TestNarrowTallBoardGolden is the 60x50 capture ticket #141 asked for to tune
// the compaction width axis. Below the wide-frame threshold kb shows a single
// column, and the spec's MaxColumnWidth clamp holds it at 52, so a card's inner
// field is 47 cells and the width axis does not fire at this size: the frame is
// tall, the description gets its second line, and the density stays normal. The
// 22 threshold is therefore left at the spec's value; it fires between frame
// widths 100 and ~116, where four columns share the frame.
func TestNarrowTallBoardGolden(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	fixture := boardViewFixture(now)
	fixture.Tasks[0].Desc = "Pointer capture leaks when the column scrolls under the drag ghost and the row grid stays fixed"
	m := NewModel(stubBoardReader{board: fixture}, nil, "alice")
	m.loading = false
	m.board = fixture
	m.now = func() time.Time { return now }
	m.renderedAt = now
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 50})
	m = sized.(Model)
	tm := teatest.NewTestModel(t, m,
		teatest.WithInitialTermSize(60, 50),
		teatest.WithProgramOptions(theme.PinStructure()),
	)
	t.Cleanup(func() { _ = tm.Quit() })
	var captured bytes.Buffer
	teatest.WaitFor(t, io.TeeReader(tm.Output(), &captured), func(output []byte) bool {
		return bytes.Contains(output, []byte("Ship terminal"))
	}, teatest.WithDuration(5*time.Second), teatest.WithCheckInterval(10*time.Millisecond))
	frame, ok := finalFullScreenFrame(captured.Bytes())
	if !ok {
		t.Fatal("teatest output did not contain a full-screen frame")
	}
	grid, err := renderedCellGrid(frame, 60, 50)
	if err != nil {
		t.Fatal(err)
	}
	teatest.RequireEqualOutput(t, grid)
	tm.Send(tea.KeyPressMsg{Code: 'q'})
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))
}

func TestViewIsByteStableAcrossMovingWallClock(t *testing.T) {
	stamp := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	m := NewModel(stubBoardReader{}, nil, "alice")
	m.loading = false
	m.width, m.height = 100, 12
	m.renderedAt = stamp
	m.board = board.Board{Title: "Clock", Tasks: []board.Task{{
		ID: "task", Title: "Task", Status: board.StatusTodo, Due: "2026-08-18",
		CreatedAt: stamp.Add(-23 * time.Hour), MovedAt: stamp.Add(-23 * time.Hour),
	}}}
	wallReads := 0
	m.now = func() time.Time {
		wallReads++
		return stamp.Add(time.Duration(wallReads) * time.Hour)
	}

	first := m.View().Content
	second := m.View().Content
	if first != second {
		t.Fatalf("consecutive View output changed with unchanged model state:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	if wallReads != 0 {
		t.Fatalf("View read the wall clock %d times", wallReads)
	}
}

func TestPollTickRefreshesRenderTimeAtRolloverBoundary(t *testing.T) {
	before := time.Date(2026, 8, 18, 23, 59, 59, 0, time.UTC)
	after := before.Add(time.Second)
	m := NewModel(stubBoardReader{}, stubVersionReader{}, "alice")
	m.loading = false
	m.width, m.height = 100, 12
	m.renderedAt = before
	m.now = func() time.Time { return after }
	m.shipped = shippedRecord{Date: "2026-08-18", IDs: []string{"shipped"}}
	m.board = board.Board{Title: "Clock", Tasks: []board.Task{{
		ID: "task", Title: "Task", Status: board.StatusTodo, Due: "2026-08-18",
		CreatedAt: before.Add(-23*time.Hour - 59*time.Minute - 59*time.Second),
		MovedAt:   before.Add(-23*time.Hour - 59*time.Minute - 59*time.Second),
	}}}

	initial := plain(m.View().Content)
	for _, want := range []string{"new", "today", "×1 shipped today"} {
		if !strings.Contains(initial, want) {
			t.Fatalf("pre-tick render missing %q:\n%s", want, initial)
		}
	}
	command := updateTestModel(t, &m, pollTickMsg{})
	if command == nil {
		t.Fatal("poll tick did not schedule its data-version read")
	}
	if !m.renderedAt.Equal(after) {
		t.Fatalf("poll timestamp = %s, want %s", m.renderedAt, after)
	}

	rolled := plain(m.View().Content)
	// The frame is short, so the due pill is the compact "!" mark.
	for _, want := range []string{"1d old", "!1d"} {
		if !strings.Contains(rolled, want) {
			t.Fatalf("post-tick render missing %q:\n%s", want, rolled)
		}
	}
	if strings.Contains(rolled, "shipped today") {
		t.Fatalf("post-tick render retained previous-day shipped count:\n%s", rolled)
	}
}

func TestBoardFooterPrioritizesProductionSettingsActions(t *testing.T) {
	model := NewModel(stubBoardReader{}, nil, "alice")
	model.loading = false
	model.settingsNew = func() *settingsModel { return nil }
	for _, test := range []struct {
		width   int
		want    []string
		notWant []string
	}{
		{
			width: 120,
			want:  []string{"s settings", "t/x/r/D actions", "j/k cards", "h/l/tab columns", "? help", "q quit"},
		},
		{
			width: 80,
			want:  []string{"s settings", "j/k cards", "h/l/tab columns", "? help", "q quit"},
			notWant: []string{
				"1-4 jump",
			},
		},
		{
			width:   40,
			want:    []string{"s settings", "? help", "j/k h/l", "q quit"},
			notWant: []string{"1-4 jump", "c cancelled:off"},
		},
	} {
		model.width = test.width
		content, _ := model.renderBoard()
		lines := strings.Split(plain(content), "\n")
		footer := lines[len(lines)-1]
		for _, want := range test.want {
			if !strings.Contains(footer, want) {
				t.Errorf("width %d footer missing %q: %q", test.width, want, footer)
			}
		}
		for _, notWant := range test.notWant {
			if strings.Contains(footer, notWant) {
				t.Errorf("width %d footer retained lower-priority %q: %q", test.width, notWant, footer)
			}
		}
		if ansi.StringWidth(footer) > test.width {
			t.Errorf("width %d footer rendered %d cells: %q", test.width, ansi.StringWidth(footer), footer)
		}
	}
}

func TestBoardFooterWithoutSettingsKeepsBoardHints(t *testing.T) {
	model := NewModel(stubBoardReader{}, nil, "alice")
	model.loading = false
	model.width = 80
	content, _ := model.renderBoard()
	lines := strings.Split(plain(content), "\n")
	footer := lines[len(lines)-1]
	for _, want := range []string{"j/k cards", "h/l/tab columns", "1-4 jump", "? help", "q quit"} {
		if !strings.Contains(footer, want) {
			t.Errorf("board-only footer missing %q: %q", want, footer)
		}
	}
	if strings.Contains(footer, "s settings") {
		t.Fatalf("board-only footer exposed unavailable settings: %q", footer)
	}
}

func TestBoardRenderScrollsSelectionIntoShortColumn(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	m := NewModel(stubBoardReader{}, nil, "u")
	m.loading = false
	m.now = func() time.Time { return now }
	m.renderedAt = now
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
	if got := columnHue("unknown"); got != theme.HueTodo {
		t.Fatalf("unknown column hue = %v", got)
	}
	if got := plain(widget.Priority(theme.New(true), 99, theme.Card)); got != "P3" {
		t.Fatalf("invalid priority fallback = %q", got)
	}
	if got := padLine("abcdef", 3, "-"); got != "abc" {
		t.Fatalf("truncated pad = %q", got)
	}
	if got := compactDue("overdue · 2d"); got != "2d" {
		t.Fatalf("compact due = %q", got)
	}
	if got := columnMetaLine([]board.Task{{}}); got != "1 card" {
		t.Fatalf("single card meta = %q", got)
	}
	if got := hiddenCards([]string{"a", "a", "", "b"}, 2); got != 1 {
		t.Fatalf("hidden cards = %d", got)
	}
	if got := visibleCardStart([]string{"a", "", "b"}, []string{"a", "", "b"}, 1, 2); got != 1 {
		t.Fatalf("visible start = %d", got)
	}
	// A Model assembled field by field still renders: the board falls back to
	// the default dark palette when no theme was resolved for it.
	column := Model{board: board.Board{Tasks: []board.Task{{ID: "x", Title: "x", Status: board.StatusTodo}}}, boardView: boardViewState{}, renderedAt: time.Now()}.renderBoardColumn(board.StatusTodo, 2, 4)
	if len(column.lines) != 4 || plain(column.lines[0]) != "▸ " {
		t.Fatalf("tiny column = %+v", column)
	}
	if empty := (Model{}).renderBoardColumnAt(board.StatusTodo, 0, 0, theme.DensityCompact); len(empty.lines) != 0 || len(empty.hits) != 2 {
		t.Fatalf("zero-sized column = %+v", empty)
	}
}

// TestBoardStateCarriesSemanticHue pins the footer's state segment onto the
// status colors of spec section 1.5.
func TestBoardStateCarriesSemanticHue(t *testing.T) {
	base := NewModel(stubBoardReader{}, nil, "u")
	base.loading = false
	for _, test := range []struct {
		name  string
		setup func(*Model)
		want  string
		slot  theme.Slot
	}{
		{"ready", func(*Model) {}, "ready", theme.StatusOK},
		{"action ok", func(m *Model) {
			m.actionNotice, m.actionStatus = true, "shipped one card"
		}, "shipped one card", theme.StatusOK},
		{"action error", func(m *Model) {
			m.actionNotice, m.actionStatus, m.actionStatusError = true, "ship failed", true
		}, "ship failed", theme.StatusDanger},
		{"load error", func(m *Model) { m.loadErr = errors.New("gone") }, "error: gone", theme.StatusDanger},
		{"poll error", func(m *Model) { m.pollErr = errors.New("stale") }, "error: stale", theme.StatusDanger},
		{"preference error", func(m *Model) { m.preferenceErr = errors.New("disk") }, "error: disk", theme.StatusDanger},
		{"move status", func(m *Model) { m.move.status = "moved" }, "moved", theme.StatusWarn},
		{"loading", func(m *Model) { m.loading, m.haveBoardSnapshot = true, false }, "loading board...", theme.FgMuted},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := base
			test.setup(&m)
			state, slot := m.boardState()
			if state != test.want || slot != test.slot {
				t.Fatalf("board state = %q,%v want %q,%v", state, slot, test.want, test.slot)
			}
			if !strings.Contains(plain(m.render()), test.want) {
				t.Fatalf("footer dropped %q:\n%s", test.want, plain(m.render()))
			}
		})
	}
}

// TestBackgroundColorRebuildsTheme is spec section 6.3: the palette defaults to
// dark and is rebuilt exactly once, when the terminal answers.
func TestBackgroundColorRebuildsTheme(t *testing.T) {
	m := NewModel(stubBoardReader{}, nil, "u")
	m.loading = false
	before := m.styles
	if before == nil {
		t.Fatal("constructed model carries no resolved theme")
	}
	updateTestModel(t, &m, tea.BackgroundColorMsg{Color: color.White})
	if m.styles == nil || m.styles == before {
		t.Fatal("background color answer did not rebuild the theme")
	}
	if !strings.Contains(plain(m.render()), "ready") {
		t.Fatal("rebuilt theme stopped rendering the board")
	}
}

func TestCancelledPreferencePathAndIsolation(t *testing.T) {
	root := t.TempDir()
	databaseA := filepath.Join(root, "board-a", "kb.db")
	databaseB := filepath.Join(root, "board-b", "kb.db")
	databaseAlternate := filepath.Join(root, "board-a", "alternate.db")
	pathA, err := tuiPreferencesPath(databaseA, "alice")
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{pathA}
	for _, identity := range []struct {
		database string
		user     string
	}{
		{databaseA, "bob"},
		{databaseB, "alice"},
		{databaseAlternate, "alice"},
	} {
		path, pathErr := tuiPreferencesPath(identity.database, identity.user)
		if pathErr != nil {
			t.Fatal(pathErr)
		}
		paths = append(paths, path)
	}
	for i, left := range paths {
		for j, right := range paths {
			if i != j && left == right {
				t.Fatalf("preference identities %d and %d share %q", i, j, left)
			}
		}
	}
	stable, err := tuiPreferencesPath(databaseA, "alice")
	if err != nil || stable != pathA {
		t.Fatalf("stable preference path = %q,%v, want %q", stable, err, pathA)
	}
	wantRoot := filepath.Join(filepath.Dir(databaseA), ".kb-tui") + string(os.PathSeparator)
	if !strings.HasPrefix(pathA, wantRoot) {
		t.Fatalf("preference path %q is not under board data %q", pathA, wantRoot)
	}

	if got, err := loadTUIPreferences(pathA); err != nil || got.ShowCancelled || got.Filter.Text != "" || len(got.Filter.Tags) != 0 {
		t.Fatalf("missing preference = %v,%v", got, err)
	}
	want := tuiPreferences{ShowCancelled: true, Filter: boardFilter{Text: "fix", Tags: []string{"bug", "auth"}}}
	if err := saveTUIPreferences(pathA, want); err != nil {
		t.Fatal(err)
	}
	if got, err := loadTUIPreferences(pathA); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("saved preference = %v,%v", got, err)
	}
	for _, isolated := range paths[1:] {
		if got, readErr := loadTUIPreferences(isolated); readErr != nil || got.ShowCancelled || got.Filter.Text != "" || len(got.Filter.Tags) != 0 {
			t.Fatalf("isolated preference %q = %v,%v", isolated, got, readErr)
		}
	}
	if info, err := os.Stat(pathA); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("preference mode = %v,%v", info, err)
	}
	if err := saveTUIPreferences(pathA, tuiPreferences{}); err != nil {
		t.Fatal(err)
	}
	if got, err := loadTUIPreferences(pathA); err != nil || got.ShowCancelled || got.Filter.Text != "" || len(got.Filter.Tags) != 0 {
		t.Fatalf("cleared preference = %v,%v", got, err)
	}
	if err := os.WriteFile(pathA, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadTUIPreferences(pathA); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("malformed preference error = %v", err)
	}
}

func TestCancelledPreferenceCommandFailure(t *testing.T) {

	m := NewModel(stubBoardReader{}, nil, "u")
	m.boardView.showCancelled = true
	m.savePreferences = func(preferences tuiPreferences) error {
		if !preferences.ShowCancelled {
			t.Fatal("saved wrong toggle")
		}
		return errors.New("disk full")
	}
	message := m.queuePreferences()()
	updateTestModel(t, &m, message)
	if m.preferenceErr == nil || !strings.Contains(m.render(), "disk full") {
		t.Fatalf("preference failure = %+v", m.preferenceErr)
	}
	m.savePreferences = nil
	if command := m.queuePreferences(); command != nil {
		t.Fatalf("nil preference saver returned %v", command)
	}
}

type failingPreferenceTemp struct {
	*os.File
	stage   string
	failure error
}

func (f *failingPreferenceTemp) Write(data []byte) (int, error) {
	if f.stage == "write" {
		return 0, f.failure
	}
	if f.stage == "short write" {
		return len(data) - 1, nil
	}
	return f.File.Write(data)
}

func (f *failingPreferenceTemp) Sync() error {
	if f.stage == "sync" {
		return f.failure
	}
	return f.File.Sync()
}

func (f *failingPreferenceTemp) Close() error {
	err := f.File.Close()
	if f.stage == "close" && err == nil {
		return f.failure
	}
	return err
}

func TestCancelledPreferenceAtomicFailuresPreservePriorFile(t *testing.T) {
	failure := errors.New("injected preference failure")
	for _, stage := range []string{"write", "short write", "sync", "close", "rename"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "prefs.json")
			prior := tuiPreferences{Filter: boardFilter{Text: "prior", Tags: []string{"stable"}}}
			if err := saveTUIPreferences(path, prior); err != nil {
				t.Fatal(err)
			}
			ops := osPreferenceFileOps
			createdIn := ""
			ops.createTmp = func(tempDir, pattern string) (preferenceTempFile, error) {
				createdIn = tempDir
				file, err := os.CreateTemp(tempDir, pattern)
				if err != nil {
					return nil, err
				}
				return &failingPreferenceTemp{File: file, stage: stage, failure: failure}, nil
			}
			if stage == "rename" {
				ops.rename = func(string, string) error { return failure }
			}
			if err := saveTUIPreferencesWithOps(path, tuiPreferences{ShowCancelled: true, Filter: boardFilter{Text: "next"}}, ops); err == nil {
				t.Fatal("injected atomic write succeeded")
			}
			if createdIn != dir {
				t.Fatalf("temporary file directory = %q, want %q", createdIn, dir)
			}
			if got, err := loadTUIPreferences(path); err != nil || !reflect.DeepEqual(got, prior) {
				t.Fatalf("prior preference after %s = %v,%v", stage, got, err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 || entries[0].Name() != filepath.Base(path) {
				t.Fatalf("temporary file leaked after %s: %v", stage, entries)
			}
		})
	}
}

func TestCancelledPreferenceWritesSerializeLatestToggle(t *testing.T) {
	var saved []tuiPreferences
	m := NewModel(stubBoardReader{}, nil, "u")
	m.savePreferences = func(preferences tuiPreferences) error {
		saved = append(saved, preferences)
		return nil
	}

	m.boardView.handleKey("c", m.board)
	first := m.queuePreferences()
	m.boardView.handleKey("c", m.board)
	if command := m.queuePreferences(); command != nil {
		t.Fatal("overlapping toggle started a concurrent preference write")
	}
	if !m.prefSaving || m.prefPending == nil || m.prefPending.ShowCancelled {
		t.Fatalf("queued preference state = %+v", m)
	}
	second := updateTestModel(t, &m, first())
	if second == nil || !m.prefSaving || m.prefPending != nil {
		t.Fatalf("serialized successor = %+v command=%v", m, second)
	}
	if next := updateTestModel(t, &m, second()); next != nil || m.prefSaving {
		t.Fatalf("final preference state = %+v command=%v", m, next)
	}
	if !reflect.DeepEqual(saved, []tuiPreferences{{ShowCancelled: true}, {ShowCancelled: false}}) {
		t.Fatalf("saved toggles = %v", saved)
	}
}

// TestOverlayDimsTheBoardBackdrop is spec section 4 step 1: an overlay dims the
// board by re-rendering it through the dimmed *Styles, not by post-processing a
// rendered string.
func TestOverlayDimsTheBoardBackdrop(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	m := NewModel(stubBoardReader{board: boardViewFixture(now)}, nil, "alice")
	m.loading = false
	m.board = boardViewFixture(now)
	m.now = func() time.Time { return now }
	m.width, m.height = 120, 40

	if m.overlayOpen() || m.backdrop().themeStyles() != m.styles {
		t.Fatal("a bare board was rendered through the dimmed palette")
	}
	plainFrame := m.render()

	m.helpOpen = true
	if !m.overlayOpen() {
		t.Fatal("an open overlay did not claim the backdrop")
	}
	dimmed := m.backdrop()
	if dimmed.themeStyles() != m.styles.Dimmed {
		t.Fatal("an open overlay did not dim the backdrop")
	}
	dimFrame := dimmed.render()
	if dimFrame == plainFrame {
		t.Fatal("the dimmed backdrop rendered identically to the board")
	}
	// The dim is a second built palette (section 1.8), so the dimmed frame
	// carries the dimmed card surface and not the base one.
	base, dim := colorSequence(m.styles.Pal[theme.Card]), colorSequence(m.styles.Dimmed.Pal[theme.Card])
	if !strings.Contains(plainFrame, base) || strings.Contains(dimFrame, base) || !strings.Contains(dimFrame, dim) {
		t.Fatalf("dimmed frame kept the base card surface\nbase %q dim %q", base, dim)
	}
}

// colorSequence is the truecolor background escape a palette slot emits, the
// form a rendered frame carries it in.
func colorSequence(value color.Color) string {
	r, g, b, _ := value.RGBA()
	return fmt.Sprintf("48;2;%d;%d;%d", r>>8, g>>8, b>>8)
}
