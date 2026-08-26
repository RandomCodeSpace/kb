package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
)

const (
	perfWidth  = 211
	perfHeight = 52
)

// TestWriteLargeBoardFixture creates a disposable CLI-compatible data
// directory for terminal profiling. It is inert unless both
// KB_PERF_FIXTURE_DIR and KB_PERF_TASKS are explicit, and refuses to reuse a
// path so a profiling command cannot quietly modify real board data.
func TestWriteLargeBoardFixture(t *testing.T) {
	dir := os.Getenv("KB_PERF_FIXTURE_DIR")
	countText := os.Getenv("KB_PERF_TASKS")
	if dir == "" || countText == "" {
		t.Skip("set KB_PERF_FIXTURE_DIR and KB_PERF_TASKS")
	}
	count, err := strconv.Atoi(countText)
	if err != nil || count < 1 {
		t.Fatalf("KB_PERF_TASKS=%q is not a positive integer", countText)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("fixture path must not exist: %s", dir)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	secret, err := store.LoadOrCreateSecret(dir)
	if err != nil {
		t.Fatal(err)
	}
	database, err := store.Open(filepath.Join(dir, "kb.db"), secret)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for _, task := range performanceBoard(count).Tasks {
		task.ID = ""
		task.Seq = 0
		task.Position = 0
		if _, err := database.AddTask("default", task); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("wrote %d tasks to %s", count, dir)
}

func performanceBoard(taskCount int) board.Board {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	statuses := []board.Status{board.StatusTodo, board.StatusDoing, board.StatusDone}
	tasks := make([]board.Task, taskCount)
	positions := map[board.Status]int{}
	for i := range tasks {
		status := statuses[i%len(statuses)]
		marker := "ordinary"
		if i < 17 {
			marker = "keep17"
		}
		tasks[i] = board.Task{
			ID:        fmt.Sprintf("task-%04d", i),
			Seq:       i + 1,
			Emoji:     "🧪",
			Title:     fmt.Sprintf("%s deterministic performance card %04d with wrapping title", marker, i),
			Desc:      "A representative description with enough prose to wrap at normal column widths and exercise markdown sanitation.",
			Status:    status,
			Blocked:   i%11 == 0,
			Prio:      i%3 + 1,
			Due:       "2026-09-30",
			Effort:    []string{"S", "M", "L"}[i%3],
			Tags:      []string{"performance", fmt.Sprintf("lane::%d", i%7), "dev"},
			Checks:    []board.Check{{Text: "measure frame cost", Done: i%2 == 0}, {Text: "retain pointer geometry"}},
			Position:  positions[status],
			CreatedAt: now.Add(-time.Duration(i) * time.Hour),
			MovedAt:   now.Add(-time.Duration(i) * time.Minute),
		}
		positions[status]++
	}
	return board.Board{Title: fmt.Sprintf("Synthetic %d", taskCount), Tasks: tasks}
}

func performanceModel(taskCount int, filtered bool) Model {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	fixture := performanceBoard(taskCount)
	model := NewModel(stubBoardReader{board: fixture}, nil, "perf")
	model.loading = false
	model.haveBoardSnapshot = true
	model.board = fixture
	model.now = func() time.Time { return now }
	model.renderedAt = now
	if filtered {
		model.filter.input.SetValue("keep17")
	}
	sized, _ := model.Update(tea.WindowSizeMsg{Width: perfWidth, Height: perfHeight})
	return sized.(Model)
}

func BenchmarkLargeBoardView(b *testing.B) {
	for _, count := range []int{17, 120, 500, 1000} {
		b.Run(fmt.Sprintf("tasks_%04d", count), func(b *testing.B) {
			model := performanceModel(count, false)
			b.ReportAllocs()
			for b.Loop() {
				_ = model.View()
			}
		})
	}
}

func BenchmarkLargeBoardFilteredView(b *testing.B) {
	for _, count := range []int{120, 500, 1000} {
		b.Run(fmt.Sprintf("total_%04d_matched_0017", count), func(b *testing.B) {
			model := performanceModel(count, true)
			b.ReportAllocs()
			for b.Loop() {
				_ = model.View()
			}
		})
	}
}

func BenchmarkLargeBoardStages(b *testing.B) {
	model := performanceModel(1000, false)
	columnTasks := tasksInStatus(model.board, board.StatusTodo)
	density := model.themeStyles().Metrics.DensityFor(perfHeight, 68)

	b.Run("filter_scan_1000_to_17", func(b *testing.B) {
		filtered := performanceModel(1000, true)
		b.ReportAllocs()
		for b.Loop() {
			_ = filtered.filteredBoard()
		}
	})
	b.Run("measure_334_card_column", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = model.measureCards(columnTasks, board.StatusTodo, 68, density)
		}
	})
	b.Run("render_334_card_column", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_, _, _ = model.renderTaskLines(columnTasks, board.StatusTodo, 68, density)
		}
	})
}

func BenchmarkLargeBoardKeyRepeat(b *testing.B) {
	for _, count := range []int{120, 500, 1000} {
		b.Run(fmt.Sprintf("mid_list_%04d", count), func(b *testing.B) {
			model := performanceModel(count, false)
			model.boardView.rows[0] = 1
			b.ReportAllocs()
			for i := 0; b.Loop(); i++ {
				code := tea.KeyDown
				if i%2 == 1 {
					code = tea.KeyUp
				}
				updated, _ := model.Update(tea.KeyPressMsg{Code: code})
				model = updated.(Model)
				_ = model.View()
			}
		})
		b.Run(fmt.Sprintf("clamped_bottom_%04d", count), func(b *testing.B) {
			model := performanceModel(count, false)
			model.boardView.rows[0] = taskCount(model.board, board.StatusTodo) - 1
			b.ReportAllocs()
			for b.Loop() {
				updated, _ := model.Update(tea.KeyPressMsg{Code: tea.KeyDown})
				model = updated.(Model)
				_ = model.View()
			}
		})
	}
}

func BenchmarkLargeBoardPointer(b *testing.B) {
	for _, count := range []int{120, 500, 1000} {
		b.Run(fmt.Sprintf("motion_render_tax_%04d", count), func(b *testing.B) {
			model := performanceModel(count, false)
			b.ReportAllocs()
			for b.Loop() {
				updated, _ := model.Update(tea.MouseMotionMsg{X: 40, Y: 4, Button: tea.MouseNone})
				model = updated.(Model)
				_ = model.View()
			}
		})
		b.Run(fmt.Sprintf("wheel_action_%04d", count), func(b *testing.B) {
			model := performanceModel(count, false)
			b.ReportAllocs()
			for b.Loop() {
				view := model.View() // raw accepted mouse message render
				command := view.OnMouse(tea.MouseWheelMsg{X: 40, Y: 4, Button: tea.MouseWheelDown})
				if command != nil {
					updated, _ := model.Update(command())
					model = updated.(Model)
					_ = model.View() // derived application message render
				}
			}
		})
	}
}

func BenchmarkLargeBoardOverlayView(b *testing.B) {
	for _, count := range []int{120, 500, 1000} {
		b.Run(fmt.Sprintf("help_%04d", count), func(b *testing.B) {
			model := performanceModel(count, false)
			model.helpOpen = true
			b.ReportAllocs()
			for b.Loop() {
				_ = model.View()
			}
		})
	}
}

func BenchmarkLargeBoardSettledPoll(b *testing.B) {
	for _, count := range []int{120, 500, 1000} {
		b.Run(fmt.Sprintf("unchanged_%04d", count), func(b *testing.B) {
			model := performanceModel(count, false)
			model.watcher = stubVersionReader{version: 1}
			model.haveVersion = true
			model.dataVersion = 1
			b.ReportAllocs()
			for b.Loop() {
				updated, _ := model.Update(pollTickMsg{})
				model = updated.(Model)
				_ = model.View()
				updated, _ = model.Update(dataVersionMsg{version: 1})
				model = updated.(Model)
				_ = model.View()
			}
		})
	}
}

func BenchmarkLargeBoardFirstInteractiveFrame(b *testing.B) {
	for _, count := range []int{120, 500, 1000} {
		b.Run(fmt.Sprintf("tasks_%04d", count), func(b *testing.B) {
			database := seedPerformanceStore(b, count)
			b.ResetTimer()
			b.ReportAllocs()
			for b.Loop() {
				fixture, err := database.Board("perf")
				if err != nil {
					b.Fatal(err)
				}
				model := NewModel(stubBoardReader{board: fixture}, nil, "perf")
				model.loading = false
				model.haveBoardSnapshot = true
				model.board = fixture
				sized, _ := model.Update(tea.WindowSizeMsg{Width: perfWidth, Height: perfHeight})
				_ = sized.(Model).View()
			}
		})
	}
}

func BenchmarkLargeBoardStoreRead(b *testing.B) {
	for _, count := range []int{120, 500, 1000} {
		b.Run(fmt.Sprintf("tasks_%04d", count), func(b *testing.B) {
			database := seedPerformanceStore(b, count)
			b.ResetTimer()
			b.ReportAllocs()
			for b.Loop() {
				if _, err := database.Board("perf"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func seedPerformanceStore(b *testing.B, count int) *store.Store {
	b.Helper()
	database, err := store.Open(filepath.Join(b.TempDir(), "kb.db"), []byte("performance-test"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = database.Close() })
	for _, task := range performanceBoard(count).Tasks {
		task.ID = ""
		task.Seq = 0
		task.Position = 0
		if _, err := database.AddTask("perf", task); err != nil {
			b.Fatal(err)
		}
	}
	return database
}

// TestLargeBoardInputToFrameBudget is intentionally red on the baseline when
// any deterministic corpus misses the Wayfinder destination. It is a
// diagnostic acceptance loop, not a claim that the current implementation
// already meets the target.
func TestLargeBoardInputToFrameBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("performance acceptance test")
	}
	if os.Getenv("KB_PERF_ACCEPT") != "1" {
		t.Skip("set KB_PERF_ACCEPT=1 to run the red performance acceptance loop")
	}
	for _, count := range []int{120, 500, 1000} {
		t.Run(fmt.Sprintf("tasks_%04d", count), func(t *testing.T) {
			model := performanceModel(count, false)
			model.boardView.rows[0] = taskCount(model.board, board.StatusTodo) - 1
			_ = model.View() // warm caches owned by dependencies, not by kb
			latencies := make([]time.Duration, 31)
			for i := range latencies {
				started := time.Now()
				updated, _ := model.Update(tea.KeyPressMsg{Code: tea.KeyDown})
				model = updated.(Model)
				_ = model.View()
				latencies[i] = time.Since(started)
			}
			slices.Sort(latencies)
			p95 := latencies[(len(latencies)*95+99)/100-1]
			p99 := latencies[(len(latencies)*99+99)/100-1]
			t.Logf("clamped input-to-frame p95=%s p99=%s", p95, p99)
			if p95 >= 50*time.Millisecond || p99 >= 100*time.Millisecond {
				t.Errorf("input-to-frame budget missed: p95=%s (want <50ms), p99=%s (want <100ms)", p95, p99)
			}
		})
	}
}
