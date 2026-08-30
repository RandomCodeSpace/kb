package ai

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RandomCodeSpace/plasmid/oneshot"
)

const parityProbeMaxOutputTokens = int32(256)

// TestPlasmidOneShotAPI freezes the published API this migration is allowed
// to target. It does not switch the runtime while Rig remains the reference.
func TestPlasmidOneShotAPI(t *testing.T) {
	request := oneshot.ProbeRequest{MaxOutputTokens: parityProbeMaxOutputTokens}
	if request.MaxOutputTokens != parityProbeMaxOutputTokens {
		t.Fatalf("probe output budget = %d, want %d", request.MaxOutputTokens, parityProbeMaxOutputTokens)
	}
	var probe func(context.Context, oneshot.ProbeRequest) (oneshot.Result, error) = oneshot.Probe
	if probe == nil {
		t.Fatal("oneshot.Probe is nil")
	}
}

func TestMigrationParityCapsDecompressedModelResponsesAtOneMiB(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		compressed := gzip.NewWriter(w)
		_ = json.NewEncoder(compressed).Encode(map[string]any{"choices": []any{map[string]any{
			"index": 0,
			"message": map[string]any{
				"role":    "assistant",
				"content": strings.Repeat("x", (1<<20)+1024),
			},
			"finish_reason": "stop",
		}}})
		_ = compressed.Close()
	}))
	defer upstream.Close()

	runner := parityRunner(t, upstream.URL, upstream.Client(), upstream.Client())
	_, err := runner.RunText(context.Background(), "default", "system", "prompt", 64)
	assertAIError(t, err, http.StatusBadGateway, "upstream request failed")
	if strings.Contains(err.Error(), upstream.URL) {
		t.Fatalf("response-limit error leaked the endpoint: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("upstream calls = %d, want 1", got)
	}
}

func TestMigrationParityDisablesImplicitRetriesForRuns(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "0")
		http.Error(w, "try again", http.StatusTooManyRequests)
	}))
	defer upstream.Close()
	runner := parityRunner(t, upstream.URL, upstream.Client(), upstream.Client())

	if _, err := runner.RunText(context.Background(), "default", "system", "prompt", 64); err == nil {
		t.Fatal("RunText accepted an upstream failure")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("RunText upstream calls = %d, want 1", got)
	}
	if _, err := runner.RunSkill(context.Background(), "default", ScopeReadOnly, "adr-split", "input", 1, 64); err == nil {
		t.Fatal("RunSkill accepted an upstream failure")
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("combined upstream calls = %d, want 2", got)
	}
}

func TestMigrationParityUsesExactToolScopes(t *testing.T) {
	tests := []struct {
		name  string
		scope Scope
		want  []string
	}{
		{
			name:  "read only",
			scope: ScopeReadOnly,
			want:  []string{"propose_card", "find_similar", "list_tasks", "get_task", "load_skill"},
		},
		{
			name:  "full",
			scope: ScopeFull,
			want:  []string{"propose_card", "find_similar", "list_tasks", "get_task", "fetch_link", "update_task", "load_skill"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := &scriptedOpenAI{replies: []fakeReply{{content: "done"}}}
			runner, closeUpstream := configuredRunner(t, fake, "test-model")
			defer closeUpstream()

			if _, err := runner.RunSkill(context.Background(), "default", test.scope, "adr-split", "input", 1, 64); err != nil {
				t.Fatalf("RunSkill: %v", err)
			}
			requests := fake.requests()
			if len(requests) != 1 {
				t.Fatalf("model requests = %d, want 1", len(requests))
			}
			got := requestToolNames(t, requests[0])
			if strings.Join(got, ",") != strings.Join(test.want, ",") {
				t.Fatalf("tools = %v, want exact ordered set %v", got, test.want)
			}
		})
	}
}

func TestMigrationParityRunTextAllowsOneToolFreeModelCall(t *testing.T) {
	fake := &scriptedOpenAI{replies: []fakeReply{{calls: []fakeToolCall{{name: "undeclared", args: `{}`}}}}}
	runner, closeUpstream := configuredRunner(t, fake, "test-model")
	defer closeUpstream()

	_, err := runner.RunText(context.Background(), "default", "system", "prompt", 64)
	assertAIError(t, err, http.StatusUnprocessableEntity, SkillIterationLimitMessage)
	requests := fake.requests()
	if len(requests) != 1 {
		t.Fatalf("model requests = %d, want 1", len(requests))
	}
	if got := requestToolNames(t, requests[0]); len(got) != 0 {
		t.Fatalf("RunText tools = %v, want none", got)
	}
}

func TestMigrationParityAllows32ToolCallsInOneResponse(t *testing.T) {
	var toolCalls atomic.Int32
	links := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		toolCalls.Add(1)
		_, _ = io.WriteString(w, "ok")
	}))
	defer links.Close()
	calls := fetchLinkCalls(32, links.URL)
	fake := &scriptedOpenAI{replies: []fakeReply{{calls: calls}, {content: "done"}}}
	model := httptest.NewServer(http.HandlerFunc(fake.handler))
	defer model.Close()
	runner := parityRunner(t, model.URL, model.Client(), links.Client())

	result, err := runner.RunSkill(context.Background(), "default", ScopeFull, "adr-split", "input", 1, 64)
	if err != nil || result.Commentary != "done" {
		t.Fatalf("RunSkill = %+v, %v", result, err)
	}
	if got := len(fake.requests()); got != 2 {
		t.Fatalf("upstream calls = %d, want 2", got)
	}
	if got := toolCalls.Load(); got != 32 {
		t.Fatalf("executed tool calls = %d, want 32", got)
	}
}

func TestMigrationParityRejectsMoreThan32ToolCallsBeforeExecution(t *testing.T) {
	var toolCalls atomic.Int32
	links := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		toolCalls.Add(1)
		_, _ = io.WriteString(w, "ok")
	}))
	defer links.Close()
	calls := fetchLinkCalls(33, links.URL)
	fake := &scriptedOpenAI{replies: []fakeReply{{calls: calls}}}
	model := httptest.NewServer(http.HandlerFunc(fake.handler))
	defer model.Close()
	runner := parityRunner(t, model.URL, model.Client(), links.Client())

	_, err := runner.RunSkill(context.Background(), "default", ScopeFull, "adr-split", "input", 1, 64)
	assertAIError(t, err, http.StatusUnprocessableEntity, SkillIterationLimitMessage)
	if got := len(fake.requests()); got != 1 {
		t.Fatalf("upstream calls = %d, want 1", got)
	}
	if got := toolCalls.Load(); got != 0 {
		t.Fatalf("executed tool calls = %d, want 0", got)
	}
}

func TestMigrationParityReturnsToolFailuresToTheModel(t *testing.T) {
	fake := &scriptedOpenAI{replies: []fakeReply{
		{calls: []fakeToolCall{{name: "get_task", args: `{}`}}},
		{content: "Recovered from the bad tool input."},
	}}
	runner, closeUpstream := configuredRunner(t, fake, "test-model")
	defer closeUpstream()

	result, err := runner.RunSkill(context.Background(), "default", ScopeReadOnly, "adr-split", "input", 1, 64)
	if err != nil || result.Commentary != "Recovered from the bad tool input." {
		t.Fatalf("RunSkill = %+v, %v", result, err)
	}
	requests := fake.requests()
	if len(requests) != 2 || !strings.Contains(string(requests[1]), `"role":"tool"`) ||
		!strings.Contains(string(requests[1]), "error:") {
		t.Fatalf("second request did not return the tool failure: %s", requests[1])
	}
}

func TestMigrationParityExecutesToolCallsSequentiallyInResponseOrder(t *testing.T) {
	firstEntered := make(chan struct{}, 1)
	secondEntered := make(chan struct{}, 1)
	releaseFirst := make(chan struct{})
	links := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/first":
			firstEntered <- struct{}{}
			<-releaseFirst
		case "/second":
			secondEntered <- struct{}{}
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer links.Close()

	fake := &scriptedOpenAI{replies: []fakeReply{
		{calls: []fakeToolCall{
			{name: "fetch_link", args: `{"url":` + jsonQuote(links.URL+"/first") + `}`},
			{name: "fetch_link", args: `{"url":` + jsonQuote(links.URL+"/second") + `}`},
		}},
		{content: "done"},
	}}
	model := httptest.NewServer(http.HandlerFunc(fake.handler))
	defer model.Close()
	runner := parityRunner(t, model.URL, model.Client(), links.Client())

	done := make(chan error, 1)
	go func() {
		_, err := runner.RunSkill(context.Background(), "default", ScopeFull, "adr-split", "input", 1, 64)
		done <- err
	}()

	select {
	case <-firstEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("first tool call did not start")
	}
	select {
	case <-secondEntered:
		close(releaseFirst)
		t.Fatal("second tool call started before the first completed")
	case <-time.After(200 * time.Millisecond):
		close(releaseFirst)
	}
	select {
	case <-secondEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("second tool call did not start after the first completed")
	}
	if err := <-done; err != nil {
		t.Fatalf("RunSkill: %v", err)
	}
}

func TestMigrationParityNormalizesCallIDsAndStripsLeakedReasoning(t *testing.T) {
	secondRequest := make(chan []byte, 1)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if calls.Add(1) == 1 {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"choices":[{"index":0,"message":{"role":"assistant","content":"<think>hidden</think>working","tool_calls":[`+
				`{"id":"","function":{"name":"list_tasks","arguments":""}},`+
				`{"id":"dup","type":"function","function":{"name":"list_tasks","arguments":"{}"}},`+
				`{"id":"dup","type":"function","function":{"name":"list_tasks","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`)
			return
		}
		secondRequest <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"index":0,"message":{"role":"assistant","content":"<think>secret</think>answer"},"finish_reason":"stop"}]}`)
	}))
	defer upstream.Close()

	runner := parityRunner(t, upstream.URL, upstream.Client(), upstream.Client())
	result, err := runner.RunSkill(context.Background(), "default", ScopeReadOnly, "adr-split", "input", 1, 64)
	if err != nil || result.Commentary != "answer" {
		t.Fatalf("RunSkill = %+v, %v", result, err)
	}

	var request struct {
		Messages []struct {
			Role       string `json:"role"`
			Content    string `json:"content"`
			ToolCallID string `json:"tool_call_id"`
			ToolCalls  []struct {
				ID string `json:"id"`
			} `json:"tool_calls"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(<-secondRequest, &request); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, 3)
	toolIDs := make([]string, 0, 3)
	for _, message := range request.Messages {
		if message.Role == "assistant" {
			if strings.Contains(message.Content, "think") || strings.Contains(message.Content, "hidden") {
				t.Fatalf("assistant history leaked reasoning: %q", message.Content)
			}
			for _, call := range message.ToolCalls {
				ids = append(ids, call.ID)
			}
		}
		if message.Role == "tool" {
			toolIDs = append(toolIDs, message.ToolCallID)
		}
	}
	if len(ids) != 3 || len(toolIDs) != 3 {
		t.Fatalf("call ids = %v, tool ids = %v", ids, toolIDs)
	}
	seen := make(map[string]bool, len(ids))
	for i, id := range ids {
		if id == "" || seen[id] || toolIDs[i] != id {
			t.Fatalf("normalized call ids = %v, tool ids = %v", ids, toolIDs)
		}
		seen[id] = true
	}
}

func parityRunner(t *testing.T, baseURL string, modelClient, linkClient *http.Client) *Runner {
	t.Helper()
	st := newTestStore(t)
	model, key := "test-model", "sk-test"
	if _, err := st.SetAISettings("default", &baseURL, &model, &key); err != nil {
		t.Fatalf("SetAISettings: %v", err)
	}
	return NewRunner(st, "", modelClient, linkClient)
}

func jsonQuote(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func fetchLinkCalls(count int, target string) []fakeToolCall {
	calls := make([]fakeToolCall, count)
	args := `{"url":` + jsonQuote(target) + `}`
	for i := range calls {
		calls[i] = fakeToolCall{name: "fetch_link", args: args}
	}
	return calls
}

func requestToolNames(t *testing.T, body []byte) []string {
	t.Helper()
	var request struct {
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(request.Tools))
	for _, tool := range request.Tools {
		names = append(names, tool.Function.Name)
	}
	return names
}
