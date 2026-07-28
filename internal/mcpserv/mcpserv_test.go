package mcpserv

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/RandomCodeSpace/kb/internal/store"
)

// connect spins up the MCP server over an in-memory transport against a temp
// DB and returns a connected client session.
func connect(t *testing.T) *mcp.ClientSession {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "kb.db"), []byte("test-secret"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	serverT, clientT := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := newServer(st, "tester").Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { ss.Wait() })

	client := mcp.NewClient(&mcp.Implementation{Name: "kb-test", Version: "0.0.1"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

// callOK invokes a tool, requires success, and decodes the text content into out.
func callOK(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any, out any) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("%s returned tool error: %v", name, res.Content)
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("%s: content[0] is %T, want TextContent", name, res.Content[0])
	}
	if err := json.Unmarshal([]byte(tc.Text), out); err != nil {
		t.Fatalf("%s: decode %q: %v", name, tc.Text, err)
	}
}

func TestListToolsExposesAllFive(t *testing.T) {
	cs := connect(t)
	res, err := cs.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	got := map[string]bool{}
	for _, tool := range res.Tools {
		got[tool.Name] = true
	}
	for _, want := range []string{"list_tasks", "add_task", "update_task", "move_task", "delete_task"} {
		if !got[want] {
			t.Errorf("tool %q missing; got %v", want, res.Tools)
		}
	}
	if len(res.Tools) != 5 {
		t.Errorf("got %d tools, want 5", len(res.Tools))
	}
}

func TestAddListRoundTrip(t *testing.T) {
	cs := connect(t)

	var created taskJSON
	callOK(t, cs, "add_task", map[string]any{
		"title":  "Write MCP docs",
		"desc":   "cover all five tools",
		"status": "doing",
		"prio":   2,
		"due":    "2026-08-01",
		"effort": "M",
		"tags":   []string{"docs", "mcp"},
		"checks": []map[string]any{{"text": "outline", "done": true}, {"text": "draft"}},
		"emoji":  "📝",
	}, &created)
	if created.ID == "" {
		t.Fatal("created task has no id")
	}
	if created.Title != "Write MCP docs" || created.Status != "doing" || created.Prio != 2 {
		t.Errorf("created task mismatch: %+v", created)
	}

	var list listTasksOutput
	callOK(t, cs, "list_tasks", map[string]any{}, &list)
	if len(list.Tasks) != 1 {
		t.Fatalf("got %d tasks, want 1: %+v", len(list.Tasks), list.Tasks)
	}
	got := list.Tasks[0]
	if got.ID != created.ID || got.Title != created.Title || got.Status != "doing" ||
		got.Prio != 2 || got.Due != "2026-08-01" || got.Effort != "M" ||
		got.Desc != "cover all five tools" || got.Emoji != "📝" ||
		len(got.Tags) != 2 || got.Tags[0] != "docs" || got.Tags[1] != "mcp" ||
		len(got.Checks) != 2 || !got.Checks[0].Done || got.Checks[1].Done {
		t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", got, created)
	}

	// Status filter excludes the task.
	callOK(t, cs, "list_tasks", map[string]any{"status": "todo"}, &list)
	if len(list.Tasks) != 0 {
		t.Errorf("todo filter: got %d tasks, want 0", len(list.Tasks))
	}
}

func TestUpdateMoveDelete(t *testing.T) {
	cs := connect(t)

	var created taskJSON
	callOK(t, cs, "add_task", map[string]any{"title": "Ship it"}, &created)

	var updated taskJSON
	callOK(t, cs, "update_task", map[string]any{"id": created.ID[:8], "prio": 1, "tags": []string{"release"}}, &updated)
	if updated.Prio != 1 || len(updated.Tags) != 1 || updated.Tags[0] != "release" {
		t.Errorf("update mismatch: %+v", updated)
	}

	var moved taskJSON
	callOK(t, cs, "move_task", map[string]any{"id": created.ID[:8], "status": "done"}, &moved)
	if moved.Status != "done" {
		t.Errorf("move mismatch: %+v", moved)
	}

	var deleted taskJSON
	callOK(t, cs, "delete_task", map[string]any{"id": created.ID}, &deleted)
	if deleted.ID != created.ID {
		t.Errorf("delete returned %+v, want id %s", deleted, created.ID)
	}

	var list listTasksOutput
	callOK(t, cs, "list_tasks", map[string]any{}, &list)
	if len(list.Tasks) != 0 {
		t.Errorf("after delete: got %d tasks, want 0", len(list.Tasks))
	}
}

func TestNormalizeUser(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", "default", false},
		{"   ", "default", false},
		{"Alice", "alice", false}, // must match server/CLI normalization
		{"User@Example.COM", "user@example.com", false},
		{"bad name", "", true},
		{"foo/bar", "", true},
	}
	for _, tt := range tests {
		got, err := normalizeUser(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("normalizeUser(%q) = %q, want error", tt.in, got)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("normalizeUser(%q) = %q, %v, want %q", tt.in, got, err, tt.want)
		}
	}
}

func TestToolErrorsAreActionable(t *testing.T) {
	cs := connect(t)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "move_task", Arguments: map[string]any{"id": "nope", "status": "done"},
	})
	if err != nil {
		t.Fatalf("move_task: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected tool error for unknown id")
	}
}
