package mcpserv

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/cliapp"
	"github.com/RandomCodeSpace/kb/internal/store"
)

// projectEnv builds the tool state the way Run does — a store beside a
// state.json in the same data directory — with no project resolving until a
// test says so.
func projectEnv(t *testing.T) (*kb, string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("KB_PROJECT", "")
	st, err := store.Open(filepath.Join(dir, "kb.db"), []byte("test-secret"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return &kb{st: st, user: "tester", dataDir: dir}, dir
}

// storeActiveProject writes the state.json kb project use writes.
func storeActiveProject(t *testing.T, dir, name string) {
	t.Helper()
	body := []byte(`{"active_project":"` + name + `"}` + "\n")
	if err := os.WriteFile(filepath.Join(dir, "state.json"), body, 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}
}

func projectOf(t *testing.T, tags []string) string {
	t.Helper()
	var found []string
	for _, tag := range tags {
		if name, ok := strings.CutPrefix(tag, "project::"); ok {
			found = append(found, name)
		}
	}
	if len(found) != 1 {
		t.Fatalf("tags %v carry %d project labels, want exactly 1", tags, len(found))
	}
	return found[0]
}

func TestAddTaskResolvesProject(t *testing.T) {
	ctx := context.Background()
	for _, tt := range []struct {
		name    string
		stored  string
		env     string
		input   addTaskInput
		want    string
		wantErr string
	}{
		{
			name:  "stored active project is the default",
			input: addTaskInput{Title: "stored"},
			want:  "kbwork", stored: "kbwork",
		},
		{
			name:  "KB_PROJECT beats the stored project",
			input: addTaskInput{Title: "env"},
			want:  "envproj", stored: "kbwork", env: "envproj",
		},
		{
			name:  "the project argument beats both",
			input: addTaskInput{Title: "arg", Project: "explicit"},
			want:  "explicit", stored: "kbwork", env: "envproj",
		},
		{
			name:  "a project label spelled in tags is honoured",
			input: addTaskInput{Title: "tagged", Tags: []string{"docs", "project::spelled"}},
			want:  "spelled", stored: "kbwork",
		},
		{
			name:    "nothing resolves is a refusal naming both fixes",
			input:   addTaskInput{Title: "orphan"},
			wantErr: `kb project use`,
		},
		{
			name:    "the project argument is validated",
			input:   addTaskInput{Title: "bad", Project: "two words"},
			wantErr: "whitespace",
		},
		{
			name:    "two spelled project labels are refused",
			input:   addTaskInput{Title: "double", Tags: []string{"project::a", "project::b"}},
			wantErr: "exactly one project:: label",
		},
		{
			name:    "an argument contradicting a spelled label is refused",
			input:   addTaskInput{Title: "clash", Project: "one", Tags: []string{"project::other"}},
			wantErr: "contradicts",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			k, dir := projectEnv(t)
			if tt.stored != "" {
				storeActiveProject(t, dir, tt.stored)
			}
			if tt.env != "" {
				t.Setenv("KB_PROJECT", tt.env)
			}
			_, created, err := k.addTask(ctx, nil, tt.input)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("addTask error = %v, want it to mention %q", err, tt.wantErr)
				}
				tasks, listErr := k.st.ListTasks(k.user, "")
				if listErr != nil || len(tasks) != 0 {
					t.Fatalf("refused add wrote %d tasks (err %v)", len(tasks), listErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("addTask: %v", err)
			}
			if got := projectOf(t, created.Tags); got != tt.want {
				t.Fatalf("project = %q, want %q (tags %v)", got, tt.want, created.Tags)
			}
			stored, err := k.st.ListTasks(k.user, "")
			if err != nil || len(stored) != 1 {
				t.Fatalf("stored tasks = %d (err %v)", len(stored), err)
			}
			if got := projectOf(t, stored[0].Tags); got != tt.want {
				t.Fatalf("persisted project = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestUpdateTaskHoldsTheProjectInvariant(t *testing.T) {
	ctx := context.Background()
	newPrio := 1
	empty := []string{}
	for _, tt := range []struct {
		name    string
		input   updateTaskInput
		want    string
		wantErr string
	}{
		{
			name:  "replacing tags keeps the task in its project",
			input: updateTaskInput{Tags: &[]string{"release"}},
			want:  "seeded",
		},
		{
			name:  "clearing tags keeps the project",
			input: updateTaskInput{Tags: &empty},
			want:  "seeded",
		},
		{
			name:  "the project argument moves the task",
			input: updateTaskInput{Project: "moved"},
			want:  "moved",
		},
		{
			name:  "a patch touching neither leaves the project alone",
			input: updateTaskInput{Prio: &newPrio},
			want:  "seeded",
		},
		{
			name:  "tags and project together land as one project",
			input: updateTaskInput{Tags: &[]string{"release"}, Project: "moved"},
			want:  "moved",
		},
		{
			name:    "an invalid project argument is refused",
			input:   updateTaskInput{Project: "#nope"},
			wantErr: "must not start with",
		},
		{
			name:    "a project label spelled in tags contradicting the argument is refused",
			input:   updateTaskInput{Tags: &[]string{"project::a"}, Project: "b"},
			wantErr: "contradicts",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			k, _ := projectEnv(t)
			seeded, err := k.st.AddTask(k.user, board.Task{Title: "seeded", Tags: []string{"docs", "project::seeded"}})
			if err != nil {
				t.Fatal(err)
			}
			in := tt.input
			in.ID = seeded.ID
			_, updated, err := k.updateTask(ctx, nil, in)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("updateTask error = %v, want it to mention %q", err, tt.wantErr)
				}
				after, listErr := k.st.ListTasks(k.user, "")
				if listErr != nil || len(after) != 1 || projectOf(t, after[0].Tags) != "seeded" {
					t.Fatalf("refused update changed the board: %+v (err %v)", after, listErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("updateTask: %v", err)
			}
			if got := projectOf(t, updated.Tags); got != tt.want {
				t.Fatalf("project = %q, want %q (tags %v)", got, tt.want, updated.Tags)
			}
		})
	}
}

// TestUpdateTaskRejectsUnknownIDBeforeWriting pins that the project read
// resolves the same id the update does: a bad id fails as an id error rather
// than as a project one.
func TestUpdateTaskRejectsUnknownIDBeforeWriting(t *testing.T) {
	k, dir := projectEnv(t)
	storeActiveProject(t, dir, "kbwork")
	_, _, err := k.updateTask(context.Background(), nil, updateTaskInput{ID: "nope", Project: "kbwork"})
	if err == nil || !strings.Contains(err.Error(), "no task matches") {
		t.Fatalf("updateTask error = %v, want an id refusal", err)
	}
}

// TestMoveAndDeleteKeepTheProject pins the paths that write no labels: they
// cannot drop the project because they never rewrite the tag list.
func TestMoveAndDeleteKeepTheProject(t *testing.T) {
	ctx := context.Background()
	k, _ := projectEnv(t)
	seeded, err := k.st.AddTask(k.user, board.Task{Title: "seeded", Tags: []string{"project::seeded"}})
	if err != nil {
		t.Fatal(err)
	}
	_, moved, err := k.moveTask(ctx, nil, moveTaskInput{ID: seeded.ID, Status: "doing"})
	if err != nil {
		t.Fatalf("moveTask: %v", err)
	}
	if got := projectOf(t, moved.Tags); got != "seeded" {
		t.Fatalf("project after move = %q", got)
	}
	_, deleted, err := k.deleteTask(ctx, nil, deleteTaskInput{ID: seeded.ID})
	if err != nil {
		t.Fatalf("deleteTask: %v", err)
	}
	if got := projectOf(t, deleted.Tags); got != "seeded" {
		t.Fatalf("project after soft delete = %q", got)
	}
}

// TestAddTaskProjectOverTheWire pins that the project argument is part of the
// tool's declared input, not just the Go handler's.
func TestAddTaskProjectOverTheWire(t *testing.T) {
	cs, st := connectWithStore(t)
	var created taskJSON
	callOK(t, cs, "add_task", map[string]any{"title": "wired", "project": "wire"}, &created)
	if got := projectOf(t, created.Tags); got != "wire" {
		t.Fatalf("project = %q, want wire", got)
	}
	tasks, err := st.ListTasks("tester", "")
	if err != nil || len(tasks) != 1 {
		t.Fatalf("stored tasks = %d (err %v)", len(tasks), err)
	}
	if !slices.Contains(tasks[0].Tags, "project::wire") {
		t.Fatalf("stored tags = %v", tasks[0].Tags)
	}
}

// TestRunBackfillsProjects pins that kb mcp does not serve a board the
// invariant is untrue of: tasks that predate mandatory projects are labelled
// when the server opens.
func TestRunBackfillsProjects(t *testing.T) {
	dir := t.TempDir()
	secret, err := store.LoadOrCreateSecret(dir)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dir, "kb.db"), secret)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := st.AddTask("default", board.Task{Title: "legacy"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	original := serveMCP
	t.Cleanup(func() { serveMCP = original })
	serveMCP = func(*mcp.Server) error { return nil }
	if err := Run(dir, "default"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	reopened, err := store.Open(filepath.Join(dir, "kb.db"), secret)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.Close() })
	tasks, err := reopened.ListTasks("default", "")
	if err != nil || len(tasks) != 1 || tasks[0].ID != legacy.ID {
		t.Fatalf("tasks = %+v (err %v)", tasks, err)
	}
	if got := projectOf(t, tasks[0].Tags); got != "inbox" {
		t.Fatalf("backfilled project = %q, want inbox", got)
	}
}

// TestRunReportsABackfillFailure pins that a board the pass could not repair
// is not served: the failure reaches the caller instead of being swallowed.
func TestRunReportsABackfillFailure(t *testing.T) {
	originalServe, originalBackfill := serveMCP, backfillProjects
	t.Cleanup(func() { serveMCP, backfillProjects = originalServe, originalBackfill })
	serveMCP = func(*mcp.Server) error {
		t.Fatal("served a board the backfill could not repair")
		return nil
	}
	backfillProjects = func(cliapp.ProjectBackfiller, string) (int, error) {
		return 0, errors.New("backfill refused")
	}
	if err := Run(t.TempDir(), "default"); err == nil || !strings.Contains(err.Error(), "backfill refused") {
		t.Fatalf("Run error = %v, want the backfill failure", err)
	}
}
