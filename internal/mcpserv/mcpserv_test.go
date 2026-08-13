package mcpserv

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
)

func TestMCPDoneGuardReevaluatesAfterConcurrentUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kb.db")
	a, err := store.Open(path, []byte("test-secret"))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := store.Open(path, []byte("test-secret"))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	task, err := a.AddTask("tester", board.Task{Title: "Race"})
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	resume := make(chan struct{})
	var once sync.Once
	k := &kb{st: a, user: "tester", beforeDoneGuard: func() {
		once.Do(func() {
			close(entered)
			<-resume
		})
	}}
	result := make(chan error, 1)
	go func() {
		_, _, err := k.moveTask(context.Background(), nil, moveTaskInput{ID: task.ID, Status: "done"})
		result <- err
	}()
	<-entered
	blocked := true
	if _, err := b.UpdateTask("tester", task.ID, store.TaskPatch{Blocked: &blocked}); err != nil {
		t.Fatal(err)
	}
	close(resume)
	if err := <-result; err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("guarded move err=%v, want blocked refusal", err)
	}
	stored, err := a.ListTasks("tester", "")
	if err != nil || len(stored) != 1 || stored[0].Status != board.StatusTodo || !stored[0].Blocked {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
}

// connect spins up the MCP server over an in-memory transport against a temp
// DB and returns a connected client session.
func connect(t *testing.T) *mcp.ClientSession {
	t.Helper()
	cs, _ := connectWithStore(t)
	return cs
}

// connectWithStore also returns the real SQLite store so MCP tests can seed
// fixtures through the same write paths used by the other application surfaces.
func connectWithStore(t *testing.T) (*mcp.ClientSession, *store.Store) {
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
	return cs, st
}

// callTextOK invokes a tool, requires success, and returns its raw text content.
func callTextOK(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) string {
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
	return tc.Text
}

// callOK invokes a tool, requires success, and decodes the text content into out.
func callOK(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any, out any) {
	t.Helper()
	text := callTextOK(t, cs, name, args)
	if err := json.Unmarshal([]byte(text), out); err != nil {
		t.Fatalf("%s: decode %q: %v", name, text, err)
	}
}

// TestListToolsExposesExactlyNine keeps both advisory tools discoverable and
// preserves the rationale that tells an agent when to prefer their cheap stubs.
func TestListToolsExposesExactlyNine(t *testing.T) {
	cs := connect(t)
	res, err := cs.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	want := map[string]bool{
		"list_tasks":      true,
		"add_task":        true,
		"update_task":     true,
		"move_task":       true,
		"delete_task":     true,
		"search_similar":  true,
		"duplicate_check": true,
		"add_comment":     true,
		"list_comments":   true,
	}
	got := map[string]string{}
	for _, tool := range res.Tools {
		got[tool.Name] = tool.Description
		if !want[tool.Name] {
			t.Errorf("unexpected tool %q", tool.Name)
		}
	}
	for name := range want {
		if _, ok := got[name]; !ok {
			t.Errorf("tool %q missing; got %v", name, res.Tools)
		}
	}
	if len(res.Tools) != 9 {
		t.Errorf("got %d tools, want 9", len(res.Tools))
	}
	const why = "check before creating a card to avoid duplicating existing work; returns cheap stubs, fetch details only if needed"
	for _, name := range []string{"search_similar", "duplicate_check"} {
		if description, ok := got[name]; ok && !strings.Contains(description, why) {
			t.Errorf("tool %q description %q does not contain exact rationale %q", name, description, why)
		}
	}
}

// TestSearchSimilarClampsDefaultAndMaximumBudgets prevents an omitted-style
// zero from returning nothing and an oversized request from escaping the cap.
func TestSearchSimilarClampsDefaultAndMaximumBudgets(t *testing.T) {
	cs, st := connectWithStore(t)
	for i := 0; i < 12; i++ {
		if _, err := st.AddTask("tester", board.Task{
			Title: fmt.Sprintf("budget sentinel card %02d", i),
		}); err != nil {
			t.Fatalf("seed budget card %d: %v", i, err)
		}
	}

	for _, tt := range []struct {
		name  string
		limit int
		want  int
	}{
		{name: "zero uses the default", limit: 0, want: 3},
		{name: "oversized uses the maximum", limit: 99, want: 10},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var got struct {
				Items []json.RawMessage `json:"items"`
			}
			callOK(t, cs, "search_similar", map[string]any{
				"query": "budget sentinel",
				"limit": tt.limit,
			}, &got)
			if len(got.Items) != tt.want {
				t.Errorf("search_similar limit %d returned %d items, want %d", tt.limit, len(got.Items), tt.want)
			}
		})
	}
}

// TestSearchSimilarEmitsLowercaseOmitEmptyStubKeys guards the cheap wire shape
// so card-only and import-only fields do not leak as empty or capitalized keys.
func TestSearchSimilarEmitsLowercaseOmitEmptyStubKeys(t *testing.T) {
	cs, st := connectWithStore(t)
	if _, err := st.AddTask("tester", board.Task{Title: "shape sentinel card"}); err != nil {
		t.Fatalf("seed shape card: %v", err)
	}
	if err := st.RecordImportLinks("tester", []store.ImportLink{{
		Source:      "gitlab.example",
		Kind:        "gitlab",
		ExternalKey: "shape-import",
		Link:        "link::gitlab#2",
		URL:         "https://gitlab.example/issues/2",
		Title:       "shape sentinel import",
	}}); err != nil {
		t.Fatalf("seed shape import: %v", err)
	}

	text := callTextOK(t, cs, "search_similar", map[string]any{
		"query": "shape sentinel",
		"limit": 10,
	})
	var got struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("decode search_similar output %q: %v", text, err)
	}
	if len(got.Items) != 2 {
		t.Fatalf("search_similar returned %d shape fixtures, want 2: %s", len(got.Items), text)
	}

	byVia := make(map[string]json.RawMessage, len(got.Items))
	for _, raw := range got.Items {
		var stub struct {
			Via string `json:"via"`
		}
		if err := json.Unmarshal(raw, &stub); err != nil {
			t.Fatalf("decode stub %s: %v", raw, err)
		}
		byVia[stub.Via] = raw
	}
	assertJSONKeys(t, byVia["card"], "id", "title", "status", "via")
	assertJSONKeys(t, byVia["import"], "title", "via", "link")
}

// TestDuplicateCheckKeepsExactLinksFirstAndScopesCandidates proves the exact
// provenance hit wins without duplication while foreign tenant data stays out.
func TestDuplicateCheckKeepsExactLinksFirstAndScopesCandidates(t *testing.T) {
	cs, st := connectWithStore(t)
	exact, err := st.AddTask("tester", board.Task{
		Title: "dupesentinel exact link candidate",
		Tags:  []string{"link::gitlab#1"},
	})
	if err != nil {
		t.Fatalf("seed exact-link card: %v", err)
	}
	similar, err := st.AddTask("tester", board.Task{
		Title: "dupesentinel nearby card",
	})
	if err != nil {
		t.Fatalf("seed similar card: %v", err)
	}
	const importedTitle = "dupesentinel imported issue"
	if err := st.RecordImportLinks("tester", []store.ImportLink{{
		Source:      "gitlab.example",
		Kind:        "gitlab",
		ExternalKey: "tester-import",
		Link:        "link::gitlab#2",
		URL:         "https://gitlab.example/issues/2",
		Title:       importedTitle,
	}}); err != nil {
		t.Fatalf("seed similar import: %v", err)
	}

	foreign, err := st.AddTask("second-user", board.Task{
		Title: "dupesentinel foreign card",
		Tags:  []string{"link::gitlab#1"},
	})
	if err != nil {
		t.Fatalf("seed foreign card: %v", err)
	}
	const foreignImportTitle = "dupesentinel foreign import"
	if err := st.RecordImportLinks("second-user", []store.ImportLink{{
		Source:      "gitlab.example",
		Kind:        "gitlab",
		ExternalKey: "foreign-import",
		Link:        "link::gitlab#1",
		URL:         "https://gitlab.example/issues/1",
		Title:       foreignImportTitle,
	}}); err != nil {
		t.Fatalf("seed foreign import: %v", err)
	}

	var got struct {
		Candidates []struct {
			ID     string `json:"id"`
			Title  string `json:"title"`
			Status string `json:"status"`
			Via    string `json:"via"`
			Link   string `json:"link"`
		} `json:"candidates"`
	}
	callOK(t, cs, "duplicate_check", map[string]any{
		"title": "dupesentinel",
		"link":  "link::gitlab#1",
	}, &got)
	if len(got.Candidates) == 0 {
		t.Fatal("duplicate_check returned no candidates")
	}
	if first := got.Candidates[0]; first.ID != exact.ID || first.Link != "link::gitlab#1" || first.Via != "card" {
		t.Errorf("first candidate = %+v, want exact-link card %q", first, exact.ID)
	}

	exactCount := 0
	similarFound := false
	importFound := false
	for _, candidate := range got.Candidates {
		if candidate.ID == exact.ID {
			exactCount++
		}
		if candidate.ID == similar.ID && candidate.Via == "card" {
			similarFound = true
		}
		if candidate.Title == importedTitle && candidate.Via == "import" {
			importFound = true
		}
		if candidate.ID == foreign.ID || candidate.Title == foreignImportTitle {
			t.Errorf("foreign candidate leaked into tester scope: %+v", candidate)
		}
	}
	if exactCount != 1 {
		t.Errorf("exact-link candidate appeared %d times, want once", exactCount)
	}
	if !similarFound {
		t.Errorf("same-scope similar card %q missing: %+v", similar.ID, got.Candidates)
	}
	if !importFound {
		t.Errorf("same-scope import candidate %q missing: %+v", importedTitle, got.Candidates)
	}
}

func TestDuplicateCheckCapsMixedCandidatesAfterExactLinks(t *testing.T) {
	cs, st := connectWithStore(t)
	var exactIDs []string
	for i := 0; i < 8; i++ {
		exact, err := st.AddTask("tester", board.Task{
			Title: fmt.Sprintf("exact-only candidate %02d", i),
			Tags:  []string{"link::gitlab#mixed-limit"},
		})
		if err != nil {
			t.Fatalf("seed exact-link card %d: %v", i, err)
		}
		exactIDs = append(exactIDs, exact.ID)
	}
	for i := 0; i < 5; i++ {
		if _, err := st.AddTask("tester", board.Task{Title: fmt.Sprintf("mixed-limit similar %02d", i)}); err != nil {
			t.Fatalf("seed similar card %d: %v", i, err)
		}
	}

	var got struct {
		Candidates []struct {
			ID   string `json:"id"`
			Via  string `json:"via"`
			Link string `json:"link"`
		} `json:"candidates"`
	}
	callOK(t, cs, "duplicate_check", map[string]any{
		"title": "mixed-limit",
		"link":  "link::gitlab#mixed-limit",
	}, &got)
	if len(got.Candidates) != 10 {
		t.Fatalf("duplicate_check returned %d candidates, want 10: %+v", len(got.Candidates), got.Candidates)
	}
	for i, wantID := range exactIDs {
		candidate := got.Candidates[i]
		if candidate.ID != wantID || candidate.Via != "card" || candidate.Link != "link::gitlab#mixed-limit" {
			t.Errorf("candidate %d = %+v, want exact-link card %q", i, candidate, wantID)
		}
	}
	for _, candidate := range got.Candidates[8:] {
		if candidate.ID == "" || candidate.Via != "card" || candidate.Link != "" {
			t.Errorf("similar candidate = %+v, want card without exact-link field", candidate)
		}
	}
}

func TestDuplicateCheckKeepsThreeSimilarCandidateBudgetWithoutExactLink(t *testing.T) {
	cs, st := connectWithStore(t)
	for i := 0; i < 5; i++ {
		if _, err := st.AddTask("tester", board.Task{Title: fmt.Sprintf("similar-only budget %02d", i)}); err != nil {
			t.Fatalf("seed similar card %d: %v", i, err)
		}
	}

	var got struct {
		Candidates []json.RawMessage `json:"candidates"`
	}
	callOK(t, cs, "duplicate_check", map[string]any{
		"title": "similar-only budget",
	}, &got)
	if len(got.Candidates) != 3 {
		t.Fatalf("duplicate_check returned %d similar candidates, want 3", len(got.Candidates))
	}
}

func assertJSONKeys(t *testing.T, raw json.RawMessage, want ...string) {
	t.Helper()
	if len(raw) == 0 {
		t.Fatalf("missing stub for keys %v", want)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode stub keys from %s: %v", raw, err)
	}
	if len(got) != len(want) {
		t.Errorf("stub keys = %v, want exactly %v", got, want)
	}
	for _, key := range want {
		if _, ok := got[key]; !ok {
			t.Errorf("stub keys = %v, missing %q", got, key)
		}
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
	callOK(t, cs, "delete_task", map[string]any{"id": created.ID, "soft": false}, &deleted)
	if deleted.ID != created.ID {
		t.Errorf("delete returned %+v, want id %s", deleted, created.ID)
	}

	var list listTasksOutput
	callOK(t, cs, "list_tasks", map[string]any{}, &list)
	if len(list.Tasks) != 0 {
		t.Errorf("after delete: got %d tasks, want 0", len(list.Tasks))
	}
}

// callErr invokes a tool expecting a tool-level error and returns its text.
func callErr(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) string {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if !res.IsError {
		t.Fatalf("%s: expected a tool error, got %v", name, res.Content)
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("%s: content[0] is %T, want TextContent", name, res.Content[0])
	}
	return tc.Text
}

// TestDeleteTaskIsSoftByDefault is F11 on MCP: an omitted soft flag means the
// task is cancelled, not destroyed, and soft: false is the only row delete.
func TestDeleteTaskIsSoftByDefault(t *testing.T) {
	cs := connect(t)

	var created taskJSON
	callOK(t, cs, "add_task", map[string]any{"title": "Maybe not"}, &created)

	var deleted taskJSON
	callOK(t, cs, "delete_task", map[string]any{"id": created.ID}, &deleted)
	if deleted.Status != "cancelled" {
		t.Errorf("soft delete returned status %q, want cancelled", deleted.Status)
	}

	var list listTasksOutput
	callOK(t, cs, "list_tasks", map[string]any{"status": "cancelled"}, &list)
	if len(list.Tasks) != 1 || list.Tasks[0].ID != created.ID {
		t.Fatalf("soft-deleted task is not in the cancelled column: %+v", list.Tasks)
	}

	// It can be moved back out.
	var restored taskJSON
	callOK(t, cs, "move_task", map[string]any{"id": created.ID, "status": "todo"}, &restored)
	if restored.Status != "todo" {
		t.Errorf("restore returned status %q, want todo", restored.Status)
	}

	// soft: true is the explicit spelling of the default.
	callOK(t, cs, "delete_task", map[string]any{"id": created.ID, "soft": true}, &deleted)
	if deleted.Status != "cancelled" {
		t.Errorf("soft: true returned status %q, want cancelled", deleted.Status)
	}
	callOK(t, cs, "delete_task", map[string]any{"id": created.ID, "soft": false}, &deleted)
	callOK(t, cs, "list_tasks", map[string]any{}, &list)
	if len(list.Tasks) != 0 {
		t.Errorf("after soft: false: got %d tasks, want 0", len(list.Tasks))
	}
}

// TestMoveToDoneNeedsForce is F9/F10 on MCP: finishing a task with open
// checklist items or a blocked flag is an error naming them, unless force.
func TestMoveToDoneNeedsForce(t *testing.T) {
	cs := connect(t)

	var created taskJSON
	callOK(t, cs, "add_task", map[string]any{
		"title":  "Ship it",
		"checks": []map[string]any{{"text": "write tests", "done": true}, {"text": "update docs"}},
	}, &created)

	msg := callErr(t, cs, "move_task", map[string]any{"id": created.ID, "status": "done"})
	for _, want := range []string{"1 of 2 checklist items are still open", `"update docs"`, "force"} {
		if !strings.Contains(msg, want) {
			t.Errorf("move_task error %q does not mention %q", msg, want)
		}
	}
	var list listTasksOutput
	callOK(t, cs, "list_tasks", map[string]any{}, &list)
	if list.Tasks[0].Status != "todo" {
		t.Errorf("refused move still happened: status %q", list.Tasks[0].Status)
	}

	// Other columns are never guarded.
	var moved taskJSON
	callOK(t, cs, "move_task", map[string]any{"id": created.ID, "status": "doing"}, &moved)
	if moved.Status != "doing" {
		t.Errorf("move to doing: status %q", moved.Status)
	}

	// force: true ships it.
	callOK(t, cs, "move_task", map[string]any{"id": created.ID, "status": "done", "force": true}, &moved)
	if moved.Status != "done" {
		t.Errorf("forced move: status %q, want done", moved.Status)
	}

	// A blocked task with nothing open is still guarded, and says why.
	var blocked taskJSON
	callOK(t, cs, "add_task", map[string]any{"title": "Waiting", "blocked": true}, &blocked)
	if !blocked.Blocked {
		t.Fatalf("add_task did not set blocked: %+v", blocked)
	}
	msg = callErr(t, cs, "move_task", map[string]any{"id": blocked.ID, "status": "done"})
	if !strings.Contains(msg, "flagged blocked") {
		t.Errorf("move_task error %q does not mention the blocked flag", msg)
	}
	// Clearing the flag clears the guard.
	var unblocked taskJSON
	callOK(t, cs, "update_task", map[string]any{"id": blocked.ID, "blocked": false}, &unblocked)
	if unblocked.Blocked {
		t.Fatalf("update_task did not clear blocked: %+v", unblocked)
	}
	callOK(t, cs, "move_task", map[string]any{"id": blocked.ID, "status": "done"}, &moved)
	if moved.Status != "done" {
		t.Errorf("unblocked move: status %q, want done", moved.Status)
	}
}

// TestUpdateToDoneIsGuardedAndAtomic covers update_task's own route into done.
// The guard reads the task as the patch leaves it, and anything it refuses is
// rolled back whole — the field patch never outlives the failed move.
func TestUpdateToDoneIsGuardedAndAtomic(t *testing.T) {
	cs := connect(t)

	var created taskJSON
	callOK(t, cs, "add_task", map[string]any{
		"title":  "Ship it",
		"checks": []map[string]any{{"text": "write tests"}, {"text": "update docs"}},
	}, &created)

	// Refused: both items are still open once this patch lands. The title
	// change in the same call must not survive the refusal.
	msg := callErr(t, cs, "update_task", map[string]any{
		"id": created.ID, "status": "done", "title": "Renamed mid-flight",
	})
	for _, want := range []string{"2 of 2 checklist items are still open", "force"} {
		if !strings.Contains(msg, want) {
			t.Errorf("update_task error %q does not mention %q", msg, want)
		}
	}
	var list listTasksOutput
	callOK(t, cs, "list_tasks", map[string]any{}, &list)
	if got := list.Tasks[0]; got.Status != "todo" || got.Title != "Ship it" {
		t.Errorf("refused update_task was not rolled back: status %q title %q", got.Status, got.Title)
	}

	// An unparseable status is rejected before anything is written, rather
	// than committing the patch and then failing.
	callErr(t, cs, "update_task", map[string]any{"id": created.ID, "status": "shipped", "title": "Nope"})
	callOK(t, cs, "list_tasks", map[string]any{}, &list)
	if got := list.Tasks[0].Title; got != "Ship it" {
		t.Errorf("invalid status still persisted the patch: title %q", got)
	}

	// Allowed: the very same move succeeds when the patch closes the work,
	// because the guard judges the post-patch task.
	var done taskJSON
	callOK(t, cs, "update_task", map[string]any{
		"id":     created.ID,
		"status": "done",
		"title":  "Ship it (final)",
		"checks": []map[string]any{{"text": "write tests", "done": true}, {"text": "update docs", "done": true}},
	}, &done)
	if done.Status != "done" || done.Title != "Ship it (final)" {
		t.Errorf("patch that closes the work: status %q title %q, want done and the new title", done.Status, done.Title)
	}

	// force: true skips the guard here exactly as it does on move_task.
	var blocked taskJSON
	callOK(t, cs, "add_task", map[string]any{"title": "Waiting", "blocked": true}, &blocked)
	callOK(t, cs, "update_task", map[string]any{"id": blocked.ID, "status": "done", "force": true}, &blocked)
	if blocked.Status != "done" {
		t.Errorf("forced update_task: status %q, want done", blocked.Status)
	}
}

// TestUnknownIdOnGuardedMove keeps the id-resolution errors actionable on the
// path that resolves the task itself before moving it.
func TestUnknownIdOnGuardedMove(t *testing.T) {
	cs := connect(t)
	callOK(t, cs, "add_task", map[string]any{"title": "Only one"}, &taskJSON{})
	if msg := callErr(t, cs, "move_task", map[string]any{"id": "nope", "status": "done"}); !strings.Contains(msg, "list_tasks") {
		t.Errorf("unknown id error %q does not point at list_tasks", msg)
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
