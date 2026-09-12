package mcpserv

import (
	"context"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/store"
)

func TestMCPWriteTextLimits(t *testing.T) {
	cs, st := connectWithStore(t)
	var task taskJSON
	callOK(t, cs, "add_task", map[string]any{"title": "unchanged", "project": testProject}, &task)
	over := strings.Repeat("x", (1<<20)+1)
	for _, tc := range []struct {
		name, field string
		args        map[string]any
	}{
		{"add_task", "title", map[string]any{"title": over, "project": testProject}},
		{"add_task", "desc", map[string]any{"title": "new", "desc": over, "project": testProject}},
		{"update_task", "title", map[string]any{"id": task.ID, "title": over}},
		{"update_task", "desc", map[string]any{"id": task.ID, "title": "changed", "desc": over}},
		{"add_comment", "body", map[string]any{"id": task.ID, "body": over}},
	} {
		t.Run(tc.name+"/"+tc.field, func(t *testing.T) {
			message := callErr(t, cs, tc.name, tc.args)
			if !strings.Contains(message, tc.field) || !strings.Contains(message, "1 MiB") {
				t.Fatalf("error=%q", message)
			}
			tasks, err := st.ListTasks("tester", "")
			if err != nil || len(tasks) != 1 || tasks[0].Title != "unchanged" || tasks[0].Desc != "" {
				t.Fatalf("write mutated tasks: count=%d err=%v", len(tasks), err)
			}
			comments, err := st.Comments("tester", task.ID)
			if err != nil || len(comments) != 0 {
				t.Fatalf("write mutated comments: count=%d err=%v", len(comments), err)
			}
		})
	}
	// Non-ASCII input is bounded in bytes, as on the web API.
	callErr(t, cs, "update_task", map[string]any{"id": task.ID, "desc": strings.Repeat("é", (1<<19)+1)})
}

func TestMCPWriteTextLimitBoundary(t *testing.T) {
	_, st := connectWithStore(t)
	k := &kb{st: st, user: "tester"}
	boundary := strings.Repeat("x", 1<<20)
	_, task, err := k.addTask(context.Background(), nil, addTaskInput{Title: boundary, Desc: boundary, Project: testProject})
	if err != nil {
		t.Fatalf("add boundary: %v", err)
	}
	if _, _, err := k.updateTask(context.Background(), nil, updateTaskInput{ID: task.ID, Title: &boundary, Desc: &boundary}); err != nil {
		t.Fatalf("update boundary: %v", err)
	}
	if _, _, err := k.addComment(context.Background(), nil, addCommentInput{ID: task.ID, Body: boundary}); err != nil {
		t.Fatalf("comment boundary: %v", err)
	}
	// Omitted fields still allow editing an oversized task imported through another interface.
	bigger := boundary + "x"
	if _, err := st.UpdateTask("tester", task.ID, store.TaskPatch{Desc: &bigger}); err != nil {
		t.Fatal(err)
	}
	title := "short"
	if _, _, err := k.updateTask(context.Background(), nil, updateTaskInput{ID: task.ID, Title: &title}); err != nil {
		t.Fatalf("omitted legacy field: %v", err)
	}
	stored, err := st.Task("tester", task.ID)
	if err != nil {
		t.Fatalf("read updated task: %v", err)
	}
	if stored.Title != title || stored.Desc != bigger {
		t.Fatalf("title-only update changed omitted description: title=%q descBytes=%d", stored.Title, len(stored.Desc))
	}
}
