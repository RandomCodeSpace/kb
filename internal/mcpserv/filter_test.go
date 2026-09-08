package mcpserv

import (
	"testing"
	"time"
)

func TestListTasksSearchAndTagFilters(t *testing.T) {
	cs := connect(t)

	var created taskJSON
	callOK(t, cs, "add_task", map[string]any{
		"title": "Fix login timeout", "project": testProject, "desc": "auth token expires",
		"tags": []string{"bug", "auth"},
	}, &created)
	callOK(t, cs, "add_task", map[string]any{
		"title": "Rotate auth keys", "project": testProject, "tags": []string{"auth", "env::prod"},
	}, &created)
	callOK(t, cs, "add_task", map[string]any{"title": "Design landing page", "project": testProject, "tags": []string{"ui"}}, &created)
	if _, err := time.Parse(time.RFC3339, created.UpdatedAt); err != nil {
		t.Fatalf("add_task updatedAt = %q, want RFC3339: %v", created.UpdatedAt, err)
	}

	var list listTasksOutput
	callOK(t, cs, "list_tasks", map[string]any{"search": "auth"}, &list)
	if len(list.Tasks) != 2 {
		t.Fatalf("search=auth = %+v", list.Tasks)
	}
	callOK(t, cs, "list_tasks", map[string]any{"tags": []string{"auth", "bug"}}, &list)
	if len(list.Tasks) != 1 || list.Tasks[0].Title != "Fix login timeout" {
		t.Fatalf("tags AND = %+v", list.Tasks)
	}
	callOK(t, cs, "list_tasks", map[string]any{"search": "rotat", "tags": []string{"env::prod"}}, &list)
	if len(list.Tasks) != 1 || list.Tasks[0].Title != "Rotate auth keys" {
		t.Fatalf("combined = %+v", list.Tasks)
	}
}
