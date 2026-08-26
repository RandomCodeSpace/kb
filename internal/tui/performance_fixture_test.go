package tui

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
)

const (
	performanceWidth  = 211
	performanceHeight = 52
)

var performanceCorpora = [...]int{17, 120, 500, 1000}

// TestWriteLargeBoardFixture creates a disposable CLI-compatible data
// directory for physical-terminal profiling. Both variables are mandatory and
// the destination must not exist, so the harness cannot append to KB_DATA by
// accident.
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
	writePerformanceStore(t, filepath.Join(dir, "kb.db"), secret, "default", count)
	t.Logf("wrote %d tasks to %s", count, dir)
}

func performanceTaskID(index int) string {
	return fmt.Sprintf("00000000-0000-4000-8000-%012x", index+1)
}

func performanceBoard(taskCount int) board.Board {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	statuses := []board.Status{board.StatusTodo, board.StatusDoing, board.StatusDone}
	tasks := make([]board.Task, 0, taskCount)
	positions := map[board.Status]int{}
	for statusIndex, status := range statuses {
		for i := statusIndex; i < taskCount; i += len(statuses) {
			marker := "ordinary"
			if i < 17 {
				marker = "keep17"
			}
			description := "A representative description with enough prose to wrap at normal column widths and exercise markdown sanitation."
			if i%13 == 0 {
				description = "Unicode keeps its geometry: cafe\u0301, 日本語, and 🧪.\nA second line prevents convenient rectangles."
			}
			tasks = append(tasks, board.Task{
				ID:        performanceTaskID(i),
				Seq:       len(tasks) + 1,
				Emoji:     "🧪",
				Title:     fmt.Sprintf("%s deterministic performance card %04d with wrapping title", marker, i),
				Desc:      description,
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
			})
			positions[status]++
		}
	}
	return board.Board{Title: fmt.Sprintf("Synthetic %d", taskCount), Tasks: tasks}
}

func performanceModel(taskCount int, filter string, width, height int) Model {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	fixture := performanceBoard(taskCount)
	model := NewModel(stubBoardReader{board: fixture}, nil, "perf")
	model.now = func() time.Time { return now }
	model.renderedAt = now
	updated, command := model.Update(boardLoadedMsg{board: fixture})
	model = updated.(Model)
	if filter != "" {
		model.filter.input.SetValue(filter)
	}
	updated, resizeCommand := model.Update(tea.WindowSizeMsg{Width: width, Height: height})
	model = updated.(Model)
	command = batchCommands(command, resizeCommand)
	for command != nil {
		updated, command = model.Update(command())
		model = updated.(Model)
	}
	return model
}

func seedPerformanceStore(t testing.TB, count int) (string, []byte) {
	t.Helper()
	databasePath := filepath.Join(t.TempDir(), "kb.db")
	secret := []byte("deterministic-performance-secret")
	writePerformanceStore(t, databasePath, secret, "perf", count)
	return databasePath, secret
}

func writePerformanceStore(t testing.TB, databasePath string, secret []byte, user string, count int) {
	t.Helper()
	database, err := store.Open(databasePath, secret)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.ReplaceBoard(user, performanceBoard(count)); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	normalizePerformanceTaskIdentities(t, databasePath, user, performanceBoard(count).Tasks)
}

// normalizePerformanceTaskIdentities uses the SQLite external-write seam that
// the production watcher supports. Store creation and board validation still
// run through store.Store; this test-only pass removes UUID and wall-clock
// entropy from the persisted corpus without changing a product write contract.
func normalizePerformanceTaskIdentities(t testing.TB, databasePath, user string, tasks []board.Task) {
	t.Helper()
	database, err := sql.Open("sqlite", "file:"+databasePath+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()
	for _, task := range tasks {
		result, err := transaction.Exec(`UPDATE tasks SET id = ?, created_at = ?, moved_at = ? WHERE user = ? AND seq = ?`,
			task.ID, task.CreatedAt.UTC().Format(time.RFC3339Nano), task.MovedAt.UTC().Format(time.RFC3339Nano), user, task.Seq)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := result.RowsAffected()
		if err != nil || rows != 1 {
			t.Fatalf("normalize task #%d: rows=%d err=%v", task.Seq, rows, err)
		}
	}
	// The tasks_fts update trigger deliberately ignores identity-only updates.
	// Rebuild this scope inside the same fixture transaction so FTS rows never
	// retain the random UUIDs assigned by ReplaceBoard.
	if _, err := transaction.Exec(`DELETE FROM tasks_fts WHERE scope = ?`, user); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(`
		INSERT INTO tasks_fts(id, scope, title, body, tags)
		SELECT id, user, title, "desc", tags FROM tasks WHERE user = ?`, user); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestPerformanceStoreFixtureMatchesSyntheticBoard(t *testing.T) {
	databasePath, secret := seedPerformanceStore(t, 17)
	database, err := store.Open(databasePath, secret)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	got, err := database.Board("perf")
	if err != nil {
		t.Fatal(err)
	}
	want := performanceBoard(17)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("persisted performance corpus differs from synthetic oracle\ngot:  %#v\nwant: %#v", got, want)
	}
	hits, err := database.SearchSimilar("perf", want.Tasks[0].Title, "", nil, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].ID != want.Tasks[0].ID {
		t.Fatalf("FTS fixture identity = %+v, want first hit %s", hits, want.Tasks[0].ID)
	}
}

func TestPerformanceFilterScenariosReachTheirNamedProjection(t *testing.T) {
	model := performanceModel(120, "", 80, 24)
	_ = model.filter.focusText()
	updated, _ := model.Update(tea.KeyPressMsg{Code: 'k', Text: "keep17"})
	model = updated.(Model)
	if got := len(model.filteredBoard().Tasks); got != 17 {
		t.Fatalf("keep17 projection contains %d tasks, want 17", got)
	}

	model.filter.input.SetValue("")
	_ = model.filter.focusText()
	updated, _ = model.Update(tea.KeyPressMsg{Code: 'z', Text: "matches-nothing"})
	model = updated.(Model)
	if got := len(model.filteredBoard().Tasks); got != 0 {
		t.Fatalf("empty projection contains %d tasks", got)
	}
}
