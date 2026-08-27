package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/board"
)

var temporalCommandSink tea.Cmd

func TestNextTaskTemporalBoundary(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		task board.Task
		want time.Time
		ok   bool
	}{
		{name: "todo first day", task: board.Task{Status: board.StatusTodo, CreatedAt: now.Add(-23 * time.Hour)}, want: now.Add(time.Hour), ok: true},
		{name: "todo daily", task: board.Task{Status: board.StatusTodo, CreatedAt: now.Add(-48 * time.Hour)}, want: now.Add(24 * time.Hour), ok: true},
		{name: "cancelled daily", task: board.Task{Status: board.StatusCancelled, CreatedAt: now.Add(-25 * time.Hour)}, want: now.Add(23 * time.Hour), ok: true},
		{name: "doing first two hours", task: board.Task{Status: board.StatusDoing, MovedAt: now.Add(-time.Hour)}, want: now.Add(time.Hour), ok: true},
		{name: "doing hourly through day", task: board.Task{Status: board.StatusDoing, MovedAt: now.Add(-23*time.Hour - 30*time.Minute)}, want: now.Add(30 * time.Minute), ok: true},
		{name: "doing daily after day", task: board.Task{Status: board.StatusDoing, MovedAt: now.Add(-24 * time.Hour)}, want: now.Add(24 * time.Hour), ok: true},
		{name: "done has no age", task: board.Task{Status: board.StatusDone, CreatedAt: now.Add(-time.Hour)}, ok: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := nextTaskAgeBoundary(test.task, now)
			if ok != test.ok || ok && !got.Equal(test.want) {
				t.Fatalf("boundary = %s, %t; want %s, %t", got, ok, test.want, test.ok)
			}
		})
	}
}

func TestNextBoardTemporalBoundaryUsesRenderedProjectionAndLocalMidnight(t *testing.T) {
	location := time.FixedZone("local", -7*60*60)
	now := time.Date(2026, 8, 27, 23, 30, 0, 0, location)
	model := newTestRootModel(stubBoardReader{}, nil, "alice")
	model.loading = false
	model.renderedAt = now
	model.board = board.Board{Title: "Clock", Tasks: []board.Task{
		{ID: "visible", Title: "Visible", Status: board.StatusTodo, Due: "2026-08-28", CreatedAt: now.Add(-23 * time.Hour)},
		{ID: "filtered", Title: "Hidden", Status: board.StatusDoing, MovedAt: now.Add(-time.Hour), Tags: []string{"hidden"}},
	}}
	model.filter.input.SetValue("Visible")
	model.adoptShippedAt(shippedRecord{Date: now.Format(shippedDateLayout), IDs: []string{"visible"}}, now)
	rebuildTestView(&model)

	got, ok := model.nextBoardTemporalBoundary()
	want := time.Date(2026, 8, 28, 0, 0, 0, 0, location)
	if !ok || !got.Equal(want) {
		t.Fatalf("deadline = %s, %t; want local midnight %s", got, ok, want)
	}
}

func TestNextLocalMidnightPreservesLocationAcrossDST(t *testing.T) {
	location, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 3, 7, 23, 30, 0, 0, location)
	want := time.Date(2026, 3, 8, 0, 0, 0, 0, location)
	if got := nextLocalMidnight(at); !got.Equal(want) || got.Location() != location {
		t.Fatalf("spring midnight = %s (%v), want %s (%v)", got, got.Location(), want, location)
	}
	at = time.Date(2026, 3, 8, 0, 30, 0, 0, location)
	want = time.Date(2026, 3, 9, 0, 0, 0, 0, location)
	if got := nextLocalMidnight(at); !got.Equal(want) || got.Sub(at) != 22*time.Hour+30*time.Minute {
		t.Fatalf("DST-day midnight = %s in %s, want %s in 22h30m", got, got.Sub(at), want)
	}
}

func TestTemporalSchedulerGenerationAndStaleTick(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	model := newTestRootModel(stubBoardReader{}, nil, "alice")
	model.loading = false
	model.now = func() time.Time { return now }
	model.renderedAt = now
	model.board = board.Board{Title: "Clock", Tasks: []board.Task{{
		ID: "one", Title: "One", Status: board.StatusTodo, CreatedAt: now.Add(-23 * time.Hour),
	}}}
	var scheduled []temporalTickMsg
	model.temporalSchedule = func(_ time.Duration, message temporalTickMsg) tea.Cmd {
		scheduled = append(scheduled, message)
		return func() tea.Msg { return message }
	}
	rebuildTestView(&model)

	command := model.reconcileTemporalSchedule()
	if command == nil || len(scheduled) != 1 {
		t.Fatalf("first arm = %v, scheduled=%v", command, scheduled)
	}
	first := scheduled[0]
	if duplicate := model.reconcileTemporalSchedule(); duplicate != nil || len(scheduled) != 1 {
		t.Fatalf("identical deadline duplicated: command=%v scheduled=%v", duplicate, scheduled)
	}

	before := model.RenderPlanStats()
	stale := first
	stale.generation--
	next, commands := model.updateWithCommands(stale)
	if commands.followUp != nil || commands.geometry != nil || commands.temporal != nil {
		t.Fatal("stale temporal tick armed work")
	}
	if !next.renderedAt.Equal(now) || next.temporalGeneration != model.temporalGeneration ||
		next.RenderPlanStats().PublishedFrames != before.PublishedFrames ||
		next.RenderPlanStats().TemporalStaleTicks != before.TemporalStaleTicks+1 {
		t.Fatalf("stale tick changed semantic state: before=%+v after=%+v", before, next.RenderPlanStats())
	}

	now = first.deadline
	next, commands = model.updateWithCommands(first)
	for steps := 0; commands.geometry != nil; steps++ {
		if steps > 4096 {
			t.Fatal("temporal geometry did not settle")
		}
		message := commands.geometry()
		next, commands = next.updateWithCommands(message)
	}
	if !next.renderedAt.Equal(first.deadline) || next.RenderPlanStats().PublishedFrames != before.PublishedFrames+1 {
		t.Fatalf("matching tick did not publish exactly once: rendered=%s stats=%+v", next.renderedAt, next.RenderPlanStats())
	}
	if commands.temporal == nil || len(scheduled) != 2 || !scheduled[1].deadline.After(first.deadline) {
		t.Fatalf("matching tick successor = %v scheduled=%v", commands.temporal, scheduled)
	}
	if got, want := next.View(), next.renderColdView(); got.Content != want.Content ||
		got.AltScreen != want.AltScreen || got.MouseMode != want.MouseMode {
		t.Fatal("settled temporal frame differs from cold oracle")
	}
}

func TestPollTickOnlyReadsDataVersion(t *testing.T) {
	now := time.Date(2026, 8, 27, 23, 59, 59, 0, time.UTC)
	reader := &countingVersionReader{version: 7}
	model := newTestRootModel(stubBoardReader{}, reader, "alice")
	model.renderedAt = now
	model.adoptShippedAt(shippedRecord{Date: now.Format(shippedDateLayout), IDs: []string{"one"}}, now)
	model.now = func() time.Time { return now.Add(time.Hour) }
	before := model.RenderPlanStats()

	next, commands := model.updateWithCommands(pollTickMsg{})
	if !next.renderedAt.Equal(now) || next.shipped.Date != model.shipped.Date || len(next.shipped.IDs) != 1 {
		t.Fatalf("poll mutated temporal state: rendered=%s shipped=%+v", next.renderedAt, next.shipped)
	}
	if next.RenderPlanStats().PublishedFrames != before.PublishedFrames || commands.followUp == nil {
		t.Fatalf("poll publication/command = stats %+v command=%v", next.RenderPlanStats(), commands.followUp)
	}
	if _, ok := commands.followUp().(dataVersionMsg); !ok || reader.calls != 1 {
		t.Fatalf("poll follow-up did not read data_version: calls=%d", reader.calls)
	}
}

func TestUnchangedTemporalInputsStayScanFree(t *testing.T) {
	model := performanceModel(120, "", 120, 36)
	_ = model.reconcileTemporalSchedule()
	before := model.RenderPlanStats()
	for _, message := range []tea.Msg{
		tea.KeyPressMsg{Code: tea.KeyDown},
		tea.KeyPressMsg{Code: '?', Text: "?"},
		tea.KeyPressMsg{Code: 'x', Text: "x"},
		tea.KeyPressMsg{Code: '?', Text: "?"},
	} {
		next, commands := model.updateWithCommands(message)
		model = next
		if commands.temporal != nil {
			t.Fatalf("unchanged %T duplicated temporal timer", message)
		}
	}
	after := model.RenderPlanStats()
	if after.TemporalTaskVisits != before.TemporalTaskVisits ||
		after.TemporalScheduledTicks != before.TemporalScheduledTicks {
		t.Fatalf("unchanged temporal inputs scanned/rearmed: before=%+v after=%+v", before, after)
	}
}

func TestUnchangedTemporalInputsDoNotScanOrAllocateForLargeShippedState(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	model := performanceModel(1000, "", performanceWidth, performanceHeight)
	model.now = func() time.Time { return now }
	model.renderedAt = now
	ids := make([]string, 20000)
	for index := range ids {
		ids[index] = fmt.Sprintf("shipped-%05d", index/2)
	}
	model.adoptShippedAt(shippedRecord{Date: now.Format(shippedDateLayout), IDs: ids}, now)
	model.haveTemporalScheduleInput = false
	_ = model.reconcileTemporalSchedule()

	if allocations := testing.AllocsPerRun(100, func() {
		temporalCommandSink = model.reconcileTemporalSchedule()
	}); allocations != 0 {
		t.Fatalf("unchanged temporal reconciliation allocations = %v, want zero", allocations)
	}
	if temporalCommandSink != nil {
		t.Fatal("unchanged temporal input rearmed a timer")
	}
}

func TestTemporalDeadlineIndexRefreshesOneLeafLogarithmically(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	tasks := make([]board.Task, 1000)
	for index := range tasks {
		tasks[index] = board.Task{
			ID: fmt.Sprintf("task-%04d", index), Title: "Task", Status: board.StatusTodo,
			Position: index, CreatedAt: now.Add(-23*time.Hour + time.Duration(index)*time.Second),
		}
	}
	fixture := board.Board{Title: "Indexed clock", Tasks: tasks}
	model := NewModel(stubBoardReader{board: fixture}, nil, "alice")
	model.now = func() time.Time { return now }
	model.renderedAt = now
	model, commands := model.updateWithCommands(boardLoadedMsg{board: fixture})
	settlePerformanceGeometryCommand(t, &model, commands.geometry)
	model, commands = model.updateWithCommands(tea.WindowSizeMsg{Width: 120, Height: 36})
	settlePerformanceGeometryCommand(t, &model, commands.geometry)
	deadline := model.temporalDeadline
	if deadline.IsZero() {
		t.Fatal("indexed board armed no temporal deadline")
	}
	model.now = func() time.Time { return deadline }
	before := model.RenderPlanStats()
	model, commands = model.updateWithCommands(temporalTickMsg{
		generation: model.temporalGeneration, deadline: deadline,
	})
	settlePerformanceGeometryCommand(t, &model, commands.geometry)
	after := model.RenderPlanStats()
	if after.PublishedFrames-before.PublishedFrames != 1 ||
		after.TemporalRecordsRefreshed-before.TemporalRecordsRefreshed != 1 ||
		after.TemporalIndexNodesVisited-before.TemporalIndexNodesVisited > 32 ||
		after.SynchronousLayoutRecords != before.SynchronousLayoutRecords ||
		after.TemporalTaskVisits != before.TemporalTaskVisits {
		t.Fatalf("single temporal boundary work: before=%+v after=%+v", before, after)
	}
	assertPerformanceColdOracleParity(t, model)
}

func TestTemporalDeadlineIndexRefreshesMidnightFanoutWithoutLayoutWork(t *testing.T) {
	location := time.FixedZone("local", -7*60*60)
	now := time.Date(2026, 8, 27, 23, 59, 0, 0, location)
	tasks := make([]board.Task, 1000)
	for index := range tasks {
		tasks[index] = board.Task{
			ID: fmt.Sprintf("due-%04d", index), Title: "Due", Status: board.StatusDone,
			Position: index, Due: "2026-08-28", CreatedAt: now.Add(-day),
		}
	}
	fixture := board.Board{Title: "Midnight", Tasks: tasks}
	model := NewModel(stubBoardReader{board: fixture}, nil, "alice")
	model.now = func() time.Time { return now }
	model.renderedAt = now
	model, commands := model.updateWithCommands(boardLoadedMsg{board: fixture})
	settlePerformanceGeometryCommand(t, &model, commands.geometry)
	deadline := model.temporalDeadline
	model.now = func() time.Time { return deadline }
	before := model.RenderPlanStats()
	model, commands = model.updateWithCommands(temporalTickMsg{
		generation: model.temporalGeneration, deadline: deadline,
	})
	settlePerformanceGeometryCommand(t, &model, commands.geometry)
	after := model.RenderPlanStats()
	if after.PublishedFrames-before.PublishedFrames != 1 ||
		after.TemporalRecordsRefreshed-before.TemporalRecordsRefreshed != 1000 ||
		after.SynchronousLayoutRecords != before.SynchronousLayoutRecords {
		t.Fatalf("midnight temporal fanout: before=%+v after=%+v", before, after)
	}
	assertPerformanceColdOracleParity(t, model)
}

func TestTemporalBoundaryReflowsRailedMetadataAndKeepsCardTargets(t *testing.T) {
	now := time.Date(2026, 8, 27, 23, 0, 0, 0, time.UTC)
	tasks := make([]board.Task, 20)
	for index := range tasks {
		tasks[index] = board.Task{
			ID: fmt.Sprintf("clock-%02d", index), Seq: index + 1, Title: fmt.Sprintf("Clock %02d", index),
			Status: board.StatusDoing, Position: index, MovedAt: now.Add(-23 * time.Hour),
			Due: "2026-08-28", Effort: "XX",
		}
	}
	model := temporalGeometryModel(t, board.Board{Title: "Clock", Tasks: tasks}, now, 16, 20)
	beforeText := ansi.Strip(model.View().Content)
	before := model.RenderPlanStats()
	if !strings.Contains(beforeText, "23h") || strings.Contains(beforeText, "tomorrow") {
		t.Fatalf("pre-boundary metadata did not stop before the due category:\n%s", beforeText)
	}
	if !strings.Contains(beforeText, model.themeStyles().Glyph.Track) {
		t.Fatalf("compact overflow fixture rendered no scrollbar track:\n%s", beforeText)
	}

	deadline := model.temporalDeadline
	wantDeadline := time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)
	if !deadline.Equal(wantDeadline) {
		t.Fatalf("temporal deadline = %s, want %s", deadline, wantDeadline)
	}
	model.now = func() time.Time { return deadline }
	model, commands := model.updateWithCommands(temporalTickMsg{
		generation: model.temporalGeneration,
		deadline:   deadline,
	})
	settlePerformanceGeometryCommand(t, &model, commands.geometry)

	afterText := ansi.Strip(model.View().Content)
	after := model.RenderPlanStats()
	if !strings.Contains(afterText, "1d") || !strings.Contains(afterText, "today") ||
		strings.Contains(afterText, "XX") {
		t.Fatalf("post-boundary metadata did not expose exactly the due category:\n%s", afterText)
	}
	if after.TemporalRecordsRefreshed-before.TemporalRecordsRefreshed != uint64(len(tasks)) ||
		after.PublishedFrames != before.PublishedFrames+1 {
		t.Fatalf("temporal geometry was not invalidated once: before=%+v after=%+v", before, after)
	}
	pressRenderedTask(t, &model, tasks[1])
}

func temporalGeometryModel(t *testing.T, fixture board.Board, now time.Time, width, height int) Model {
	t.Helper()
	model := NewModel(stubBoardReader{board: fixture}, nil, "clock")
	model.now = func() time.Time { return now }
	model.renderedAt = now
	var commands modelUpdateCommands
	model, commands = model.updateWithCommands(boardLoadedMsg{board: fixture})
	settlePerformanceGeometryCommand(t, &model, commands.geometry)
	model, commands = model.updateWithCommands(tea.WindowSizeMsg{Width: width, Height: height})
	settlePerformanceGeometryCommand(t, &model, commands.geometry)
	return model
}
