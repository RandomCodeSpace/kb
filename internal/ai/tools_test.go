package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RandomCodeSpace/plasmid/oneshot"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/genai"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
)

func newToolServer(t *testing.T) (*Runner, *store.Store) {
	t.Helper()
	st := newTestStore(t)
	return NewRunner(st, "", nil, nil), st
}

func runTool(t *testing.T, tool *kbTool, input string) (string, error) {
	t.Helper()
	return tool.run(context.Background(), json.RawMessage(input))
}

func mustRunTool(t *testing.T, tool *kbTool, input string) string {
	t.Helper()
	out, err := runTool(t, tool, input)
	if err != nil {
		t.Fatalf("%s(%s): unexpected error: %v", tool.Name(), input, err)
	}
	return out
}

type nativeRunnableTool interface {
	tool.Tool
	Run(agent.Context, any) (map[string]any, error)
}

func mustRunNativeTool(t *testing.T, toolValue tool.Tool, args any) map[string]any {
	t.Helper()
	runnable, ok := toolValue.(nativeRunnableTool)
	if !ok {
		t.Fatalf("%s = %T, want a native runnable tool", toolValue.Name(), toolValue)
	}
	result, err := runnable.Run(&agent.StrictContextMock{Ctx: context.Background()}, args)
	if err != nil {
		t.Fatalf("%s(%#v): unexpected execution error: %v", toolValue.Name(), args, err)
	}
	return result
}

func requireRawChatToolResult(t *testing.T, result map[string]any) {
	t.Helper()
	if len(result) != 1 {
		t.Fatalf("raw Chat tool result = %#v, want one private marker", result)
	}
	if _, err := json.Marshal(result); err == nil {
		t.Fatalf("raw Chat tool result serialized outside Chat conversion: %#v", result)
	}
}

func TestNativeToolRejectsNonObjectArguments(t *testing.T) {
	result := mustRunNativeTool(t, ProposeCardTool(NewCardCollector(1)), "not an object")
	if got := fmt.Sprint(result["error"]); !strings.Contains(got, "expected a JSON object") {
		t.Fatalf("native tool error = %q, want object validation", got)
	}
}

func TestImportCollectorRequiresUniqueBoundedSources(t *testing.T) {
	collector := &cardCollector{max: 2, sourceCount: 2, claimedSources: make(map[int]bool)}
	proposal := proposeCardTool(collector)
	for _, input := range []string{
		`{"title":"missing"}`,
		`{"title":"fractional","source":1.5}`,
		`{"title":"past end","source":3}`,
	} {
		if _, err := runTool(t, proposal, input); err == nil || !strings.Contains(err.Error(), "source must be a unique integer from 1 to 2") {
			t.Fatalf("propose_card(%s) error = %v", input, err)
		}
	}
	if _, err := runTool(t, proposal, `{"title":"first","source":1}`); err != nil {
		t.Fatalf("first source: %v", err)
	}
	if _, err := runTool(t, proposal, `{"title":"duplicate","source":1}`); err == nil || !strings.Contains(err.Error(), "already proposed") {
		t.Fatalf("duplicate source error = %v", err)
	}
	if _, err := runTool(t, proposal, `{"title":"second","source":2}`); err != nil {
		t.Fatalf("second source: %v", err)
	}
	if got := collector.Cards(); len(got) != 2 || got[0].Source != 1 || got[1].Source != 2 {
		t.Fatalf("collected cards = %+v", got)
	}

	ordinary := &cardCollector{max: 1}
	if _, err := runTool(t, proposeCardTool(ordinary), `{"title":"ordinary"}`); err != nil || ordinary.cards[0].Source != 0 {
		t.Fatalf("ordinary skill source contract changed: cards=%+v err=%v", ordinary.cards, err)
	}
}

func addToolTask(t *testing.T, st *store.Store, task board.Task) board.Task {
	t.Helper()
	created, err := st.AddTask("default", task)
	if err != nil {
		t.Fatalf("AddTask(%q): %v", task.Title, err)
	}
	return created
}

// TestToolDefinitions keeps every constructor's wire metadata inside what the
// run loop accepts: a missing description or Run makes the whole tool set
// invalid, which fails the run rather than the tool.
func TestToolDefinitions(t *testing.T) {
	s, _ := newToolServer(t)
	nameRe := regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)
	tools := []*kbTool{
		proposeCardTool(&cardCollector{max: 1}),
		s.findSimilarTool("default"),
		s.fetchLinkTool(),
		s.listTasksTool("default"),
		s.getTaskTool("default"),
		s.updateTaskTool("default"),
	}
	want := []string{"propose_card", "find_similar", "fetch_link", "list_tasks", "get_task", "update_task"}
	if len(tools) != len(want) {
		t.Fatalf("built %d tools, want %d", len(tools), len(want))
	}
	seen := make(map[string]bool, len(tools))
	for i, tool := range tools {
		name := tool.Name()
		if name != want[i] {
			t.Errorf("tool %d name = %q, want %q", i, name, want[i])
		}
		if !nameRe.MatchString(name) {
			t.Errorf("tool %q name is not wire-legal", name)
		}
		if seen[name] {
			t.Errorf("duplicate tool name %q", name)
		}
		seen[name] = true
		if strings.TrimSpace(tool.Description()) == "" {
			t.Errorf("tool %q has no description", name)
		}
		if tool.run == nil {
			t.Errorf("tool %q has no Run", name)
		}
		declaration := tool.Declaration()
		schema, ok := declaration.ParametersJsonSchema.(map[string]any)
		if !ok || schema["type"] != "object" {
			t.Errorf("tool %q schema = %T %v, want object", name, declaration.ParametersJsonSchema, declaration.ParametersJsonSchema)
		}
	}
	request := &model.LLMRequest{}
	ctx := &agent.StrictContextMock{Ctx: context.Background()}
	for _, tool := range tools {
		if err := tool.ProcessRequest(ctx, request); err != nil {
			t.Fatalf("pack %q: %v", tool.Name(), err)
		}
	}
	if request.Config == nil || len(request.Config.Tools) != 1 {
		t.Fatalf("packed tools = %#v, want one ADK function declaration group", request.Config)
	}
	declarations := request.Config.Tools[0].FunctionDeclarations
	if len(declarations) != len(want) {
		t.Fatalf("packed declarations = %d, want %d", len(declarations), len(want))
	}
	for i, declaration := range declarations {
		if declaration.Name != want[i] {
			t.Errorf("packed declaration %d = %q, want %q", i, declaration.Name, want[i])
		}
	}
}

func TestNativeProductionToolAdapterParity(t *testing.T) {
	{
		collector := NewCardCollector(2)
		toolValue := ProposeCardTool(collector)

		result := mustRunNativeTool(t, toolValue, map[string]any{"title": "  Native proposal  "})
		if result["accepted"] != true || result["count"] != float64(1) {
			t.Fatalf("propose_card result = %#v", result)
		}
		cards := collector.Cards()
		if len(cards) != 1 || cards[0].Title != "Native proposal" || cards[0].Prio != board.PrioLow {
			t.Fatalf("collected cards = %#v, want one trimmed card with the default priority", cards)
		}
	}

	{
		runner, st := newToolServer(t)
		created := addToolTask(t, st, board.Task{Title: "Native needle"})
		toolValue := runner.FindSimilarTool("default")

		result := mustRunNativeTool(t, toolValue, map[string]any{"query": "  native needle  "})
		items, ok := result["items"].([]any)
		if !ok || len(items) != 1 {
			t.Fatalf("find_similar items = %#v, want one native result", result["items"])
		}
		item, ok := items[0].(map[string]any)
		if !ok || item["id"] != created.ID || item["title"] != created.Title {
			t.Fatalf("find_similar item = %#v, want task %s", items[0], created.ID)
		}
	}

	{
		t.Setenv("KB_LINK_ALLOW_PRIVATE", "1")
		var requests atomic.Int32
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("native\a\ntext"))
		}))
		defer upstream.Close()

		runner, _ := newToolServer(t)
		toolValue := runner.FetchLinkTool()
		result := mustRunNativeTool(t, toolValue, map[string]any{"url": "  " + upstream.URL + "/doc  "})
		requireRawChatToolResult(t, result)
		if requests.Load() != 1 {
			t.Fatalf("fetch_link requests = %d, want 1", requests.Load())
		}
	}

	{
		runner, st := newToolServer(t)
		created := addToolTask(t, st, board.Task{Title: "Native listing", Status: board.StatusTodo})
		toolValue := runner.ListTasksTool("default")

		result := mustRunNativeTool(t, toolValue, nil)
		tasks, ok := result["tasks"].([]any)
		if !ok || len(tasks) != 1 {
			t.Fatalf("list_tasks tasks = %#v, want one task with default filters", result["tasks"])
		}
		taskResult, ok := tasks[0].(map[string]any)
		if !ok || taskResult["id"] != created.ID || taskResult["title"] != created.Title {
			t.Fatalf("list_tasks task = %#v, want task %s", tasks[0], created.ID)
		}
	}

	{
		runner, st := newToolServer(t)
		created := addToolTask(t, st, board.Task{Title: "Native lookup", Desc: "kept"})
		toolValue := runner.GetTaskTool("default")

		result := mustRunNativeTool(t, toolValue, map[string]any{"ref": "  #" + strconv.Itoa(created.Seq) + "  "})
		if result["id"] != created.ID || result["title"] != created.Title || result["desc"] != "kept" {
			t.Fatalf("get_task result = %#v, want task %s", result, created.ID)
		}
	}

	{
		runner, st := newToolServer(t)
		created := addToolTask(t, st, board.Task{Title: "Before", Desc: "preserve", Prio: board.PrioMedium})
		toolValue := runner.UpdateTaskTool("default")

		result := mustRunNativeTool(t, toolValue, map[string]any{"ref": created.ID, "title": "  After  "})
		if result["title"] != "After" || result["desc"] != "preserve" || result["prio"] != float64(board.PrioMedium) {
			t.Fatalf("update_task result = %#v, want the title changed and omitted fields preserved", result)
		}
		stored, err := st.Task("default", created.ID)
		if err != nil || stored.Title != "After" || stored.Desc != "preserve" || stored.Prio != board.PrioMedium {
			t.Fatalf("stored task = %#v, %v", stored, err)
		}

		failure := mustRunNativeTool(t, toolValue, map[string]any{"ref": created.ID, "prio": 9})
		if !strings.Contains(fmt.Sprint(failure["error"]), "invalid prio 9") {
			t.Fatalf("update_task failure = %#v", failure)
		}
		stored, err = st.Task("default", created.ID)
		if err != nil || stored.Prio != board.PrioMedium || stored.Title != "After" {
			t.Fatalf("rejected update changed the task: %#v, %v", stored, err)
		}
	}
}

type scriptedNativeModel struct {
	calls atomic.Int32
	step  func(int, *model.LLMRequest) *model.LLMResponse
}

func (*scriptedNativeModel) Name() string { return "scripted-native" }

func (m *scriptedNativeModel) GenerateContent(_ context.Context, request *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		call := int(m.calls.Add(1) - 1)
		yield(m.step(call, request), nil)
	}
}

func TestNativeToolsExecuteSequentiallyInResponseOrder(t *testing.T) {
	started := make(chan string, 2)
	releaseFirst := make(chan struct{})
	var release sync.Once
	releaseTool := func() { release.Do(func() { close(releaseFirst) }) }
	t.Cleanup(releaseTool)

	makeTool := func(name string) *kbTool {
		return newKBTool(name, name+" tool", schemaObject(map[string]any{}), toolResultObject,
			func(context.Context, json.RawMessage) (string, error) {
				started <- name
				if name == "first" {
					<-releaseFirst
				}
				return `{"name":"` + name + `"}`, nil
			})
	}
	modelValue := &scriptedNativeModel{step: func(call int, _ *model.LLMRequest) *model.LLMResponse {
		if call == 0 {
			return &model.LLMResponse{Content: &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{
				{FunctionCall: &genai.FunctionCall{ID: "first-call", Name: "first", Args: map[string]any{}}},
				{FunctionCall: &genai.FunctionCall{ID: "second-call", Name: "second", Args: map[string]any{}}},
			}}}
		}
		return &model.LLMResponse{Content: genai.NewContentFromText("done", genai.RoleModel)}
	}}

	type outcome struct {
		result oneshot.Result
		err    error
	}
	finished := make(chan outcome, 1)
	go func() {
		result, err := oneshot.Run(context.Background(), oneshot.Request{
			Model: modelValue, Prompt: "run", Tools: []tool.Tool{makeTool("first"), makeTool("second")},
			MaxOutputTokens: 64, MaxReturnedTextBytes: 1024, MaxModelCalls: 2,
			MaxToolCallsPerResponse: 32, ToolExecution: oneshot.ToolExecutionSequential,
		})
		finished <- outcome{result: result, err: err}
	}()

	select {
	case name := <-started:
		if name != "first" {
			t.Fatalf("first started tool = %q", name)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first tool did not start")
	}
	select {
	case name := <-started:
		t.Fatalf("tool %q started before first completed", name)
	case <-time.After(200 * time.Millisecond):
	}
	releaseTool()
	select {
	case name := <-started:
		if name != "second" {
			t.Fatalf("second started tool = %q", name)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second tool did not start")
	}
	select {
	case run := <-finished:
		if run.err != nil || run.result.Text != "done" {
			t.Fatalf("run = %#v, %v", run.result, run.err)
		}
		if len(run.result.ToolResults) != 2 || run.result.ToolResults[0].Name != "first" || run.result.ToolResults[1].Name != "second" {
			t.Fatalf("tool results = %#v", run.result.ToolResults)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("native tool run did not finish")
	}
}

func TestNativeCollectorCapReachesTheModel(t *testing.T) {
	collector := &cardCollector{max: 1}
	var sawCap atomic.Bool
	modelValue := &scriptedNativeModel{step: func(call int, request *model.LLMRequest) *model.LLMResponse {
		if call == 0 {
			return &model.LLMResponse{Content: &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{
				{FunctionCall: &genai.FunctionCall{ID: "first", Name: "propose_card", Args: map[string]any{"title": "First"}}},
				{FunctionCall: &genai.FunctionCall{ID: "second", Name: "propose_card", Args: map[string]any{"title": "Second"}}},
			}}}
		}
		for _, content := range request.Contents {
			if content == nil {
				continue
			}
			for _, part := range content.Parts {
				if part == nil || part.FunctionResponse == nil {
					continue
				}
				failure, _ := part.FunctionResponse.Response["error"].(string)
				if strings.Contains(failure, cardLimitReachedMessage) {
					sawCap.Store(true)
				}
			}
		}
		return &model.LLMResponse{Content: genai.NewContentFromText("recovered", genai.RoleModel)}
	}}

	result, err := oneshot.Run(context.Background(), oneshot.Request{
		Model: modelValue, Prompt: "propose", Tools: []tool.Tool{proposeCardTool(collector)},
		MaxOutputTokens: 64, MaxReturnedTextBytes: 1024, MaxModelCalls: 2,
		MaxToolCallsPerResponse: 32, ToolExecution: oneshot.ToolExecutionSequential,
	})
	if err != nil || result.Text != "recovered" {
		t.Fatalf("run = %#v, %v", result, err)
	}
	if !sawCap.Load() {
		t.Fatal("collector cap did not reach the next model request")
	}
	if len(collector.cards) != 1 || collector.cards[0].Title != "First" {
		t.Fatalf("collector = %#v, want only the first proposal", collector.cards)
	}
	if result.Metadata.ToolCalls != 2 {
		t.Fatalf("tool calls = %d, want both proposals accounted", result.Metadata.ToolCalls)
	}
}

func TestProposeCardTool(t *testing.T) {
	t.Run("coerces and collects a card", func(t *testing.T) {
		collector := &cardCollector{max: 2}
		tool := proposeCardTool(collector)

		out := mustRunTool(t, tool, `{"title":"  Ship the thing  ","emoji":"🚀","desc":"why\u0007 it matters",
			"prio":9,"due":"not-a-date","effort":"m","tags":["backend","bad tag",42],
			"checks":[{"text":"step one","done":true},{"text":"  "}]}`)
		if out != `{"accepted":true,"count":1}` {
			t.Fatalf("propose_card result = %s", out)
		}
		if len(collector.cards) != 1 {
			t.Fatalf("collected %d cards, want 1", len(collector.cards))
		}
		card := collector.cards[0]
		if card.Title != "Ship the thing" {
			t.Errorf("title = %q, want the trimmed title", card.Title)
		}
		if card.Desc != "why it matters" {
			t.Errorf("desc = %q, want the control character stripped", card.Desc)
		}
		if card.Prio != board.PrioLow {
			t.Errorf("prio = %d, want %d (clamped onto the three-value scale)", card.Prio, board.PrioLow)
		}
		if card.Due != "" {
			t.Errorf("due = %q, want the unparseable date dropped", card.Due)
		}
		if card.Effort != "M" {
			t.Errorf("effort = %q, want M", card.Effort)
		}
		if len(card.Tags) != 1 || card.Tags[0] != "backend" {
			t.Errorf("tags = %v, want only backend", card.Tags)
		}
		if len(card.Checks) != 1 || card.Checks[0].Text != "step one" || !card.Checks[0].Done {
			t.Errorf("checks = %+v, want the one usable item", card.Checks)
		}
		if err := validateDraft(card); err != nil {
			t.Errorf("collected card is not wire-safe: %v", err)
		}
	})

	// A field the model may not name is a field it never fills: source has to
	// be declared, and the schema has to stay closed so nothing else is.
	t.Run("declares source without opening the schema", func(t *testing.T) {
		schema := proposeCardTool(&cardCollector{max: 1}).inputSchema
		props, ok := schema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("schema properties = %T, want a map", schema["properties"])
		}
		source, ok := props["source"].(map[string]any)
		if !ok {
			t.Fatalf("schema has no source property: %v", props)
		}
		if source["type"] != "integer" {
			t.Errorf("source type = %v, want integer", source["type"])
		}
		if schema["additionalProperties"] != false {
			t.Errorf("additionalProperties = %v, want false", schema["additionalProperties"])
		}
	})

	// Source is provenance the server resolves against the pack it built, so a
	// number that is not a positive whole one must leave the card unsourced
	// rather than pointing at an issue nobody packed.
	t.Run("lifts only a usable source number", func(t *testing.T) {
		tests := []struct {
			name  string
			input string
			want  int
		}{
			{name: "lifted", input: `{"title":"From an issue","source":3}`, want: 3},
			{name: "non-integral", input: `{"title":"From an issue","source":2.5}`, want: 0},
			{name: "zero", input: `{"title":"From an issue","source":0}`, want: 0},
			{name: "negative", input: `{"title":"From an issue","source":-1}`, want: 0},
			{name: "string", input: `{"title":"From an issue","source":"2"}`, want: 0},
			{name: "absent", input: `{"title":"From an issue"}`, want: 0},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				collector := &cardCollector{max: 1}
				mustRunTool(t, proposeCardTool(collector), tt.input)
				if len(collector.cards) != 1 {
					t.Fatalf("collected %d cards, want 1", len(collector.cards))
				}
				if got := collector.cards[0].Source; got != tt.want {
					t.Errorf("source = %d, want %d", got, tt.want)
				}
			})
		}
	})

	t.Run("rejects an unusable card and keeps the count", func(t *testing.T) {
		tests := []struct {
			name  string
			input string
			want  string
		}{
			{name: "no title", input: `{"desc":"body only"}`, want: "title"},
			{name: "blank title", input: `{"title":"   "}`, want: "title"},
			{name: "not an object", input: `["title"]`, want: "invalid input"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				collector := &cardCollector{max: 2}
				_, err := runTool(t, proposeCardTool(collector), tt.input)
				if err == nil {
					t.Fatalf("propose_card(%s) = nil error, want a rejection", tt.input)
				}
				if !strings.Contains(err.Error(), tt.want) {
					t.Errorf("error = %q, want it to name %q", err, tt.want)
				}
				if len(collector.cards) != 0 {
					t.Errorf("collected %d cards, want none", len(collector.cards))
				}
			})
		}
	})

	// The cap is visible to the model: a silently dropped proposal would leave
	// it believing it delivered work the caller never received.
	t.Run("errors at the cap instead of dropping proposals", func(t *testing.T) {
		collector := &cardCollector{max: 2}
		tool := proposeCardTool(collector)
		mustRunTool(t, tool, `{"title":"First"}`)
		mustRunTool(t, tool, `{"title":"Second"}`)
		_, err := runTool(t, tool, `{"title":"Third"}`)
		if err == nil || err.Error() != cardLimitReachedMessage {
			t.Fatalf("third propose_card error = %v, want %q", err, cardLimitReachedMessage)
		}
		if len(collector.cards) != 2 {
			t.Fatalf("collected %d cards, want the cap of 2", len(collector.cards))
		}
	})

	t.Run("accepts nothing without a budget", func(t *testing.T) {
		collector := &cardCollector{}
		if _, err := runTool(t, proposeCardTool(collector), `{"title":"Unbudgeted"}`); err == nil {
			t.Fatal("propose_card with no cap = nil error, want a refusal")
		}
	})
}

func TestFindSimilarTool(t *testing.T) {
	t.Run("returns board stubs", func(t *testing.T) {
		s, st := newToolServer(t)
		for _, title := range []string{"needle alpha", "needle beta", "needle gamma", "needle delta"} {
			addToolTask(t, st, board.Task{Title: title})
		}
		out := mustRunTool(t, s.findSimilarTool("default"), `{"query":"needle"}`)

		var got struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("find_similar result %s: %v", out, err)
		}
		if len(got.Items) != findSimilarLimit {
			t.Fatalf("items = %d, want the fixed limit %d", len(got.Items), findSimilarLimit)
		}
		for _, item := range got.Items {
			for _, key := range []string{"id", "title", "status", "via"} {
				if _, ok := item[key]; !ok {
					t.Errorf("stub %v has no %q", item, key)
				}
			}
			for _, key := range []string{"reason", "killed_at", "desc"} {
				if _, ok := item[key]; ok {
					t.Errorf("stub %v carries %q, want the cheap shape only", item, key)
				}
			}
		}
	})

	t.Run("stays inside the caller's board", func(t *testing.T) {
		s, st := newToolServer(t)
		if _, err := st.AddTask("alice", board.Task{Title: "private needle"}); err != nil {
			t.Fatalf("AddTask alice: %v", err)
		}
		out := mustRunTool(t, s.findSimilarTool("default"), `{"query":"needle"}`)
		if out != `{"items":[]}` {
			t.Fatalf("cross-board find_similar = %s, want no items", out)
		}
	})

	t.Run("rejects a query the store cannot answer", func(t *testing.T) {
		s, _ := newToolServer(t)
		tool := s.findSimilarTool("default")
		for _, input := range []string{`{"query":"ab"}`, `{"query":"   "}`, `{}`, `"nope"`} {
			if _, err := runTool(t, tool, input); err == nil {
				t.Errorf("find_similar(%s) = nil error, want a rejection", input)
			}
		}
	})

	t.Run("reports a storage failure opaquely", func(t *testing.T) {
		st := newTestStore(t)
		s := NewRunner(st, "", nil, nil)
		if err := st.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
		_, err := runTool(t, s.findSimilarTool("default"), `{"query":"needle"}`)
		if err == nil || err.Error() != storageErrorMessage {
			t.Fatalf("closed-store find_similar error = %v, want %q", err, storageErrorMessage)
		}
	})
}

func TestFetchLinkTool(t *testing.T) {
	// The client is built at newServer time, so the guard override has to be
	// in place before the server exists.
	newAllowingServer := func(t *testing.T) *Runner {
		t.Helper()
		t.Setenv("KB_LINK_ALLOW_PRIVATE", "1")
		s, _ := newToolServer(t)
		return s
	}

	t.Run("returns stripped text", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte("line one\nline two"))
		}))
		defer upstream.Close()

		s := newAllowingServer(t)
		body, _ := json.Marshal(map[string]string{"url": upstream.URL + "/doc"})
		out := mustRunTool(t, s.fetchLinkTool(), string(body))
		if out != "line one\nline two" {
			t.Fatalf("fetch_link text = %q, want the control character stripped", out)
		}
	})

	t.Run("caps the document", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/markdown")
			_, _ = w.Write([]byte(strings.Repeat("a", fetchLinkMaxBytes+4096)))
		}))
		defer upstream.Close()

		s := newAllowingServer(t)
		body, _ := json.Marshal(map[string]string{"url": upstream.URL})
		out := mustRunTool(t, s.fetchLinkTool(), string(body))
		if len(out) != fetchLinkMaxBytes {
			t.Fatalf("fetch_link read %d bytes, want the %d cap", len(out), fetchLinkMaxBytes)
		}
	})

	t.Run("refuses what it cannot read as text", func(t *testing.T) {
		tests := []struct {
			name        string
			contentType string
			status      int
		}{
			{name: "binary", contentType: "application/pdf", status: http.StatusOK},
			{name: "missing type", contentType: "", status: http.StatusOK},
			{name: "not found", contentType: "text/plain", status: http.StatusNotFound},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if tt.contentType != "" {
						w.Header().Set("Content-Type", tt.contentType)
					} else {
						w.Header()["Content-Type"] = nil
					}
					w.WriteHeader(tt.status)
					_, _ = w.Write([]byte("payload"))
				}))
				defer upstream.Close()

				s := newAllowingServer(t)
				body, _ := json.Marshal(map[string]string{"url": upstream.URL})
				out, err := runTool(t, s.fetchLinkTool(), string(body))
				if err == nil {
					t.Fatalf("fetch_link = %q, want a refusal", out)
				}
				if strings.Contains(err.Error(), upstream.URL) {
					t.Errorf("error %q echoes the endpoint", err)
				}
			})
		}
	})

	t.Run("rejects a URL it must not dial", func(t *testing.T) {
		s := newAllowingServer(t)
		tool := s.fetchLinkTool()
		for _, input := range []string{
			`{"url":"ftp://example.com/x"}`,
			`{"url":"file:///etc/passwd"}`,
			`{"url":"not a url"}`,
			`{"url":"http://user:pass@example.com/x"}`,
			`{"url":""}`,
			`{}`,
			`"http://example.com"`,
		} {
			if _, err := runTool(t, tool, input); err == nil {
				t.Errorf("fetch_link(%s) = nil error, want a rejection", input)
			}
		}
	})

	// Without the explicit override the guarded dialer refuses loopback, which
	// is what keeps a model-chosen URL off the host's private network.
	t.Run("blocks a private address without the override", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("secret"))
		}))
		defer upstream.Close()

		t.Setenv("KB_LINK_ALLOW_PRIVATE", "")
		s, _ := newToolServer(t)
		body, _ := json.Marshal(map[string]string{"url": upstream.URL})
		out, err := runTool(t, s.fetchLinkTool(), string(body))
		if err == nil {
			t.Fatalf("fetch_link to loopback = %q, want the SSRF guard to refuse", out)
		}
		if !errors.Is(err, errFetchLinkFailed) {
			t.Fatalf("error = %v, want the generic fetch failure", err)
		}
	})

	// The forge opt-in is for hosts the operator configured as sources, not
	// for URLs the model composes: an operator who allows a self-hosted forge
	// to resolve privately must not also expose the LAN and the cloud metadata
	// service to fetch_link.
	t.Run("ignores the forge private-address opt-in", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("credentials"))
		}))
		defer upstream.Close()

		t.Setenv("KB_FORGE_ALLOW_PRIVATE", "1")
		t.Setenv("KB_LINK_ALLOW_PRIVATE", "")
		s, _ := newToolServer(t)
		body, _ := json.Marshal(map[string]string{"url": upstream.URL})
		out, err := runTool(t, s.fetchLinkTool(), string(body))
		if err == nil {
			t.Fatalf("fetch_link to loopback = %q, want the SSRF guard to refuse", out)
		}
		if !errors.Is(err, errFetchLinkFailed) {
			t.Fatalf("error = %v, want the generic fetch failure", err)
		}
	})

	t.Run("refuses a body that stops early", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("Content-Length", "1024")
			_, _ = w.Write([]byte("truncated"))
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			panic(http.ErrAbortHandler)
		}))
		defer upstream.Close()

		s := newAllowingServer(t)
		body, _ := json.Marshal(map[string]string{"url": upstream.URL})
		out, err := runTool(t, s.fetchLinkTool(), string(body))
		if err == nil {
			t.Fatalf("fetch_link on a truncated body = %q, want a refusal", out)
		}
		if !errors.Is(err, errFetchLinkFailed) {
			t.Fatalf("error = %v, want the generic fetch failure", err)
		}
	})

	t.Run("reports a missing client", func(t *testing.T) {
		s, _ := newToolServer(t)
		s.linkClient = nil
		if _, err := runTool(t, s.fetchLinkTool(), `{"url":"https://example.com/doc"}`); err == nil {
			t.Fatal("fetch_link without a client = nil error, want a refusal")
		}
	})
}

func TestListTasksTool(t *testing.T) {
	s, st := newToolServer(t)
	addToolTask(t, st, board.Task{Title: "Write the parser", Status: board.StatusTodo, Tags: []string{"backend"}, Prio: 2})
	addToolTask(t, st, board.Task{Title: "Ship the parser", Status: board.StatusDoing, Tags: []string{"backend", "urgent"}})
	addToolTask(t, st, board.Task{Title: "Unrelated chore", Status: board.StatusTodo})
	if _, err := st.AddTask("alice", board.Task{Title: "Parser for alice"}); err != nil {
		t.Fatalf("AddTask alice: %v", err)
	}
	tool := s.listTasksTool("default")

	decode := func(t *testing.T, out string) []toolTask {
		t.Helper()
		var got struct {
			Tasks []toolTask `json:"tasks"`
		}
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("list_tasks result %s: %v", out, err)
		}
		return got.Tasks
	}

	t.Run("lists the whole board", func(t *testing.T) {
		tasks := decode(t, mustRunTool(t, tool, `{}`))
		if len(tasks) != 3 {
			t.Fatalf("tasks = %d, want the caller's three", len(tasks))
		}
		for _, task := range tasks {
			if task.ID == "" || task.Title == "" || task.Status == "" {
				t.Errorf("task %+v is missing wire fields", task)
			}
		}
	})

	t.Run("applies filters", func(t *testing.T) {
		tests := []struct {
			name  string
			input string
			want  int
		}{
			{name: "status", input: `{"status":"doing"}`, want: 1},
			{name: "search", input: `{"search":"parser"}`, want: 2},
			{name: "tags", input: `{"tags":["urgent"]}`, want: 1},
			{name: "combined", input: `{"status":"todo","search":"parser"}`, want: 1},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if tasks := decode(t, mustRunTool(t, tool, tt.input)); len(tasks) != tt.want {
					t.Fatalf("list_tasks(%s) = %d tasks, want %d", tt.input, len(tasks), tt.want)
				}
			})
		}
	})

	t.Run("rejects an unknown column", func(t *testing.T) {
		out, err := runTool(t, tool, `{"status":"backlog"}`)
		if err == nil {
			t.Fatalf("list_tasks with a bad status = %s, want an error", out)
		}
		if !strings.Contains(err.Error(), "todo") {
			t.Errorf("error = %q, want it to name the legal columns", err)
		}
	})

	t.Run("rejects input that is not an object", func(t *testing.T) {
		if _, err := runTool(t, tool, `"everything"`); err == nil {
			t.Fatal("list_tasks with a bare string = nil error, want a rejection")
		}
	})

	t.Run("reports a storage failure opaquely", func(t *testing.T) {
		closed := newTestStore(t)
		other := NewRunner(closed, "", nil, nil)
		if err := closed.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
		_, err := runTool(t, other.listTasksTool("default"), `{}`)
		if err == nil || err.Error() != storageErrorMessage {
			t.Fatalf("closed-store list_tasks error = %v, want %q", err, storageErrorMessage)
		}
	})
}

func TestGetTaskTool(t *testing.T) {
	s, st := newToolServer(t)
	created := addToolTask(t, st, board.Task{
		Title:  "Investigate the drift",
		Desc:   "context",
		Status: board.StatusTodo,
		Effort: "M",
		Tags:   []string{"ops"},
		Checks: []board.Check{{Text: "reproduce"}},
	})
	tool := s.getTaskTool("default")

	// Include the first hyphen so the UUID prefix can never be interpreted as
	// an all-digit stable sequence number.
	for _, ref := range []string{created.ID, created.ID[:9], strconv.Itoa(created.Seq), "#" + strconv.Itoa(created.Seq)} {
		body, _ := json.Marshal(map[string]string{"ref": ref})
		var got toolTask
		if err := json.Unmarshal([]byte(mustRunTool(t, tool, string(body))), &got); err != nil {
			t.Fatalf("get_task(%q): %v", ref, err)
		}
		if got.ID != created.ID || got.Title != created.Title || len(got.Checks) != 1 {
			t.Fatalf("get_task(%q) = %+v, want the created task", ref, got)
		}
	}

	t.Run("points a miss at list_tasks", func(t *testing.T) {
		_, err := runTool(t, tool, `{"ref":"99"}`)
		if err == nil || !strings.Contains(err.Error(), "list_tasks") {
			t.Fatalf("get_task miss error = %v, want it to name list_tasks", err)
		}
	})

	t.Run("rejects an empty reference", func(t *testing.T) {
		for _, input := range []string{`{"ref":"  "}`, `{}`, `7`} {
			if _, err := runTool(t, tool, input); err == nil {
				t.Errorf("get_task(%s) = nil error, want a rejection", input)
			}
		}
	})

	t.Run("stays inside the caller's board", func(t *testing.T) {
		other, err := st.AddTask("alice", board.Task{Title: "Alice only"})
		if err != nil {
			t.Fatalf("AddTask alice: %v", err)
		}
		body, _ := json.Marshal(map[string]string{"ref": other.ID})
		if _, err := runTool(t, tool, string(body)); err == nil {
			t.Fatal("get_task across boards = nil error, want a miss")
		}
	})
}

func TestUpdateTaskTool(t *testing.T) {
	// An absent key and an empty value are different instructions: the first
	// leaves the field alone, the second clears it.
	t.Run("distinguishes an absent field from an empty one", func(t *testing.T) {
		s, st := newToolServer(t)
		created := addToolTask(t, st, board.Task{
			Title:  "Original title",
			Desc:   "original description",
			Due:    "2026-01-01",
			Effort: "L",
			Tags:   []string{"keep"},
		})
		tool := s.updateTaskTool("default")

		body, _ := json.Marshal(map[string]any{"ref": created.ID, "title": "New title"})
		var got toolTask
		if err := json.Unmarshal([]byte(mustRunTool(t, tool, string(body))), &got); err != nil {
			t.Fatalf("update_task: %v", err)
		}
		if got.Title != "New title" {
			t.Errorf("title = %q, want the update applied", got.Title)
		}
		if got.Desc != "original description" || got.Due != "2026-01-01" ||
			got.Effort != "L" || len(got.Tags) != 1 {
			t.Fatalf("absent fields changed: %+v", got)
		}

		// A fresh value, not the one above: the cleared fields are omitted from
		// the result, so decoding over the old task would keep its values.
		var cleared toolTask
		body, _ = json.Marshal(map[string]any{"ref": created.ID, "desc": "", "due": "", "tags": []string{}})
		if err := json.Unmarshal([]byte(mustRunTool(t, tool, string(body))), &cleared); err != nil {
			t.Fatalf("clearing update_task: %v", err)
		}
		if cleared.Desc != "" || cleared.Due != "" || len(cleared.Tags) != 0 {
			t.Fatalf("empty values did not clear the fields: %+v", cleared)
		}
		if cleared.Title != "New title" || cleared.Effort != "L" {
			t.Fatalf("clearing update touched absent fields: %+v", cleared)
		}
	})

	t.Run("sanitizes model text and replaces lists", func(t *testing.T) {
		s, st := newToolServer(t)
		created := addToolTask(t, st, board.Task{Title: "Card", Tags: []string{"old"}, Checks: []board.Check{{Text: "old step"}}})
		tool := s.updateTaskTool("default")

		body, _ := json.Marshal(map[string]any{
			"ref":    created.ID,
			"title":  "  Clean title  ",
			"emoji":  "🚀 trailing",
			"tags":   []string{"new"},
			"checks": []map[string]any{{"text": "fresh step", "done": true}},
		})
		var got toolTask
		if err := json.Unmarshal([]byte(mustRunTool(t, tool, string(body))), &got); err != nil {
			t.Fatalf("update_task: %v", err)
		}
		if got.Title != "Clean title" {
			t.Errorf("title = %q, want the control character stripped", got.Title)
		}
		if got.Emoji != "🚀" {
			t.Errorf("emoji = %q, want the leading emoji only", got.Emoji)
		}
		if len(got.Tags) != 1 || got.Tags[0] != "new" {
			t.Errorf("tags = %v, want the list replaced", got.Tags)
		}
		if len(got.Checks) != 1 || got.Checks[0].Text != "fresh step" || !got.Checks[0].Done {
			t.Errorf("checks = %+v, want the list replaced", got.Checks)
		}

		body, _ = json.Marshal(map[string]any{"ref": created.ID, "prio": 1, "blocked": true, "due": "2026-03-04", "effort": "s"})
		var reprioritized toolTask
		if err := json.Unmarshal([]byte(mustRunTool(t, tool, string(body))), &reprioritized); err != nil {
			t.Fatalf("update_task: %v", err)
		}
		if reprioritized.Prio != 1 || !reprioritized.Blocked ||
			reprioritized.Due != "2026-03-04" || reprioritized.Effort != "S" {
			t.Fatalf("update_task = %+v, want prio 1, blocked, the due date, and effort S", reprioritized)
		}
	})

	t.Run("rejects input that is not an object", func(t *testing.T) {
		s, _ := newToolServer(t)
		if _, err := runTool(t, s.updateTaskTool("default"), `["ref"]`); err == nil {
			t.Fatal("update_task with an array = nil error, want a rejection")
		}
	})

	t.Run("refuses a bad field without writing", func(t *testing.T) {
		s, st := newToolServer(t)
		created := addToolTask(t, st, board.Task{Title: "Untouched", Prio: 3})
		tool := s.updateTaskTool("default")

		tests := []struct {
			name  string
			input map[string]any
			want  string
		}{
			{name: "prio", input: map[string]any{"ref": created.ID, "prio": 9}, want: "prio"},
			{name: "due", input: map[string]any{"ref": created.ID, "due": "2026-13-45"}, want: "due"},
			{name: "effort", input: map[string]any{"ref": created.ID, "effort": "XL"}, want: "effort"},
			{name: "empty check", input: map[string]any{"ref": created.ID, "checks": []map[string]any{{"text": " "}}}, want: "checks[0]"},
			{name: "blank title", input: map[string]any{"ref": created.ID, "title": "  "}, want: "title"},
			{name: "missing ref", input: map[string]any{"title": "x"}, want: "ref"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				body, _ := json.Marshal(tt.input)
				if _, err := runTool(t, tool, string(body)); err == nil {
					t.Fatalf("update_task(%s) = nil error, want a rejection", body)
				} else if !strings.Contains(err.Error(), tt.want) {
					t.Errorf("error = %q, want it to name %q", err, tt.want)
				}
			})
		}

		after, err := st.Task("default", created.ID)
		if err != nil {
			t.Fatalf("Task after refusals: %v", err)
		}
		if after.Title != "Untouched" || after.Prio != 3 || after.Due != "" || after.Effort != "" || len(after.Checks) != 0 {
			t.Fatalf("refused updates still landed: %+v", after)
		}
	})

	t.Run("reports an unresolvable reference", func(t *testing.T) {
		s, st := newToolServer(t)
		first := addToolTask(t, st, board.Task{Title: "First"})
		tool := s.updateTaskTool("default")

		body, _ := json.Marshal(map[string]any{"ref": "does-not-exist", "title": "x"})
		if _, err := runTool(t, tool, string(body)); err == nil || !strings.Contains(err.Error(), "list_tasks") {
			t.Fatalf("update_task miss error = %v, want it to name list_tasks", err)
		}
		body, _ = json.Marshal(map[string]any{"ref": first.ID, "title": "Renamed"})
		mustRunTool(t, tool, string(body))
	})
}

// TestMarshalToolResult covers the guard on the encoder: an unencodable value
// is a server bug, and the model gets a message it cannot mistake for its own.
func TestMarshalToolResult(t *testing.T) {
	out, err := marshalToolResult(make(chan int))
	if err == nil {
		t.Fatalf("marshalToolResult(chan) = %q, want an error", out)
	}
	if strings.Contains(err.Error(), "chan") {
		t.Errorf("error = %q, want no encoder detail", err)
	}
}

// TestTaskRefError keeps the store's id sentinels mapped onto advice the model
// can follow, and leaves every other failure intact.
func TestTaskRefError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "not found", err: store.ErrNotFound, want: "list_tasks"},
		{name: "ambiguous", err: store.ErrAmbiguous, want: "longer id prefix"},
		{name: "other", err: errors.New("store: invalid effort \"XL\""), want: "invalid effort"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := taskRefError(tt.err, "7"); !strings.Contains(got.Error(), tt.want) {
				t.Fatalf("taskRefError = %q, want it to contain %q", got, tt.want)
			}
		})
	}
}
