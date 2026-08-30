package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/RandomCodeSpace/plasmid/oneshot"
	"github.com/RandomCodeSpace/rig"

	"github.com/RandomCodeSpace/kb/internal/board"
)

type fakeToolCall struct {
	name string
	args string
}

type fakeReply struct {
	content string
	calls   []fakeToolCall
	finish  string
	status  int
}

type scriptedOpenAI struct {
	mu      sync.Mutex
	replies []fakeReply
	reqs    [][]byte
}

func (f *scriptedOpenAI) handler(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.reqs = append(f.reqs, body)
	i := len(f.reqs) - 1
	if i >= len(f.replies) {
		i = len(f.replies) - 1
	}
	reply := fakeReply{content: "done"}
	if i >= 0 {
		reply = f.replies[i]
	}
	f.mu.Unlock()
	if reply.status != 0 {
		http.Error(w, "upstream failure", reply.status)
		return
	}
	message := map[string]any{"role": "assistant", "content": reply.content}
	if len(reply.calls) > 0 {
		calls := make([]any, 0, len(reply.calls))
		for i, call := range reply.calls {
			calls = append(calls, map[string]any{
				"id": fmt.Sprintf("call-%d", i), "type": "function",
				"function": map[string]any{"name": call.name, "arguments": call.args},
			})
		}
		message["tool_calls"] = calls
	}
	finish := reply.finish
	if finish == "" {
		finish = "stop"
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{
		"index": 0, "message": message, "finish_reason": finish,
	}}})
}

func (f *scriptedOpenAI) requests() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]byte(nil), f.reqs...)
}

func configuredRunner(t *testing.T, fake *scriptedOpenAI, model string) (*Runner, func()) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(fake.handler))
	st := newTestStore(t)
	base, key := upstream.URL, "sk-test"
	if _, err := st.SetAISettings("default", &base, &model, &key); err != nil {
		t.Fatalf("SetAISettings: %v", err)
	}
	runner := NewRunner(st, "", upstream.Client(), upstream.Client())
	return runner, upstream.Close
}

func TestRunnerRunsSkillsDirectlyOnTheStore(t *testing.T) {
	fake := &scriptedOpenAI{replies: []fakeReply{
		{calls: []fakeToolCall{{name: "propose_card", args: `{"title":"Ship it"}`}}},
		{content: "Proposed one card."},
	}}
	runner, closeUpstream := configuredRunner(t, fake, "gpt-4o")
	defer closeUpstream()

	result, err := runner.RunSkill(context.Background(), "default", ScopeFull, "adr-split", "# ADR", 2, 4096)
	if err != nil {
		t.Fatalf("RunSkill: %v", err)
	}
	if len(result.Cards) != 1 || result.Cards[0].Title != "Ship it" || result.Commentary != "Proposed one card." {
		t.Fatalf("result = %+v", result)
	}
	request := string(fake.requests()[0])
	for _, want := range []string{"propose_card", "find_similar", "list_tasks", "get_task", "fetch_link", "update_task", "load_skill", "Skill: adr-split"} {
		if !strings.Contains(request, want) {
			t.Errorf("request missing %q", want)
		}
	}
}

func TestRunnerReadOnlyScopeOmitsMutatingTools(t *testing.T) {
	fake := &scriptedOpenAI{replies: []fakeReply{{content: "done"}}}
	runner, closeUpstream := configuredRunner(t, fake, "gpt-5-mini")
	defer closeUpstream()
	if _, err := runner.RunSkill(context.Background(), "default", ScopeReadOnly, "adr-split", "# ADR", 1, 99999); err != nil {
		t.Fatalf("RunSkill: %v", err)
	}
	request := string(fake.requests()[0])
	var sent struct {
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.Unmarshal([]byte(request), &sent); err != nil {
		t.Fatal(err)
	}
	for _, tool := range sent.Tools {
		if tool.Function.Name == "fetch_link" || tool.Function.Name == "update_task" {
			t.Errorf("read-only request offered %q", tool.Function.Name)
		}
	}
	if strings.Contains(request, `"max_tokens"`) {
		t.Errorf("reasoning request used max_tokens: %s", request)
	}
	if !strings.Contains(request, `"max_completion_tokens":8192`) {
		t.Errorf("request did not clamp and rename budget: %s", request)
	}
}

func TestRunnerRunTextKeepsConfigurationPrivate(t *testing.T) {
	fake := &scriptedOpenAI{replies: []fakeReply{{content: "plain summary"}}}
	runner, closeUpstream := configuredRunner(t, fake, "gpt-4o")
	defer closeUpstream()
	result, err := runner.RunText(context.Background(), "default", "system instruction", "prompt text", 123)
	if err != nil || result != "plain summary" {
		t.Fatalf("RunText = %q, %v", result, err)
	}
	requests := fake.requests()
	if len(requests) != 1 {
		t.Fatalf("requests = %d", len(requests))
	}
	request := string(requests[0])
	for _, want := range []string{"system instruction", "prompt text", `"max_tokens":123`} {
		if !strings.Contains(request, want) {
			t.Errorf("request missing %q: %s", want, request)
		}
	}
	if strings.Contains(request, `"tools"`) {
		t.Fatalf("tool-free request exposed tools: %s", request)
	}

	failing := &scriptedOpenAI{replies: []fakeReply{{status: http.StatusInternalServerError}}}
	failingRunner, closeFailing := configuredRunner(t, failing, "gpt-4o")
	defer closeFailing()
	if _, err := failingRunner.RunText(context.Background(), "default", "system", "prompt", 10); err == nil {
		t.Fatal("upstream RunText failure succeeded")
	}

	unconfigured := NewRunner(newTestStore(t), "", nil, nil)
	if _, err := unconfigured.RunText(context.Background(), "missing", "system", "prompt", 10); err == nil {
		t.Fatal("unconfigured RunText succeeded")
	}
}

func TestRunnerErrorsAndPartialResults(t *testing.T) {
	t.Run("unknown skill", func(t *testing.T) {
		fake := &scriptedOpenAI{}
		runner, closeUpstream := configuredRunner(t, fake, "m")
		defer closeUpstream()
		_, err := runner.RunSkill(context.Background(), "default", ScopeReadOnly, "missing", "input", 1, 10)
		assertAIError(t, err, http.StatusNotFound, UnknownSkillMessage)
	})

	t.Run("broken override", func(t *testing.T) {
		fake := &scriptedOpenAI{}
		runner, closeUpstream := configuredRunner(t, fake, "m")
		defer closeUpstream()
		dir := t.TempDir()
		if err := osWriteFile(dir+"/broken.md", []byte("not a skill")); err != nil {
			t.Fatal(err)
		}
		runner.skillsDir = dir
		_, err := runner.RunSkill(context.Background(), "default", ScopeReadOnly, "adr-split", "input", 1, 10)
		assertAIError(t, err, http.StatusInternalServerError, SkillsUnavailableMessage)
	})

	t.Run("output limit with card", func(t *testing.T) {
		fake := &scriptedOpenAI{replies: []fakeReply{
			{calls: []fakeToolCall{{name: "propose_card", args: `{"title":"Partial"}`}}},
			{content: "cut", finish: "length"},
		}}
		runner, closeUpstream := configuredRunner(t, fake, "m")
		defer closeUpstream()
		result, err := runner.RunSkill(context.Background(), "default", ScopeReadOnly, "adr-split", "input", 1, 10)
		if err != nil || !result.Partial || len(result.Cards) != 1 || result.Commentary != TruncatedReplyMessage {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	})

	t.Run("output limit without card", func(t *testing.T) {
		fake := &scriptedOpenAI{replies: []fakeReply{{content: "cut", finish: "length"}}}
		runner, closeUpstream := configuredRunner(t, fake, "m")
		defer closeUpstream()
		_, err := runner.RunSkill(context.Background(), "default", ScopeReadOnly, "adr-split", "input", 1, 10)
		assertAIError(t, err, http.StatusUnprocessableEntity, TruncatedReplyMessage)
	})

	t.Run("iteration limit with card", func(t *testing.T) {
		fake := &scriptedOpenAI{replies: []fakeReply{
			{calls: []fakeToolCall{{name: "propose_card", args: `{"title":"Partial"}`}}},
			{calls: []fakeToolCall{{name: "list_tasks", args: `{}`}}},
		}}
		runner, closeUpstream := configuredRunner(t, fake, "m")
		defer closeUpstream()
		result, err := runner.RunSkill(context.Background(), "default", ScopeReadOnly, "adr-split", "input", 1, 10)
		if err != nil || !result.Partial || result.Commentary != SkillIterationLimitMessage || len(fake.requests()) != SkillMaxIterations {
			t.Fatalf("result=%+v rounds=%d err=%v", result, len(fake.requests()), err)
		}
	})

	t.Run("upstream failure", func(t *testing.T) {
		fake := &scriptedOpenAI{replies: []fakeReply{{status: http.StatusInternalServerError}}}
		runner, closeUpstream := configuredRunner(t, fake, "m")
		defer closeUpstream()
		_, err := runner.RunSkill(context.Background(), "default", ScopeReadOnly, "adr-split", "input", 1, 10)
		assertAIError(t, err, http.StatusBadGateway, "upstream request failed")
	})
}

func assertAIError(t *testing.T, err error, code int, message string) {
	t.Helper()
	var aiErr *Error
	if !errors.As(err, &aiErr) || aiErr.Code != code || aiErr.Message != message {
		t.Fatalf("error = %#v, want %d %q", err, code, message)
	}
}

func osWriteFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}

func TestRunnerHelpers(t *testing.T) {
	if err := ValidateBaseURL("https://example.com"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateDraft(Draft{Title: "card", Prio: 3}); err != nil {
		t.Fatal(err)
	}
	coerced := CoerceDraft(map[string]any{"title": " card ", "prio": float64(9)})
	if coerced.Title != "card" || coerced.Prio != board.PrioLow || ClampPriority(0) != board.PrioHigh || NormalizeStoryCount(0) != defaultStoryCount {
		t.Fatalf("public draft helpers = %+v", coerced)
	}
	for _, test := range []struct {
		ask  int64
		want int64
	}{{-1, 1}, {42, 42}, {9000, maxTokensCeiling}} {
		if got := SkillBudget(test.ask); got != test.want {
			t.Errorf("SkillBudget(%d) = %d", test.ask, got)
		}
	}
	for _, test := range []struct {
		model string
		want  bool
	}{{"o1", true}, {"openai/o3-mini", true}, {"GPT-5", true}, {"gpt-4o", false}, {"olmo", false}} {
		if got := usesMaxCompletionTokens(test.model); got != test.want {
			t.Errorf("usesMaxCompletionTokens(%q) = %t", test.model, got)
		}
	}
	skill, others, found := SplitSkills([]Skill{{Name: "a"}, {Name: "b"}}, " b ")
	if !found || skill.Name != "b" || len(others) != 1 || !strings.Contains(RunnerSystem(skill, others), "Skill: b") {
		t.Fatalf("split/system = %+v %+v %t", skill, others, found)
	}
	if _, _, found := splitSkills([]Skill{{Name: "a"}}, "missing"); found {
		t.Fatal("missing skill found")
	}
	for input, want := range map[int]int{0: 8, -1: 1, 1: 1, 999: 20} {
		if got := normalizeStoryCount(input); got != want {
			t.Errorf("normalizeStoryCount(%d) = %d", input, got)
		}
	}
	for input, want := range map[int]int{-1: board.PrioHigh, 2: board.PrioMedium, 9: board.PrioLow} {
		if got := clampPrio(input); got != want {
			t.Errorf("clampPrio(%d) = %d", input, got)
		}
	}
}

func TestPublicToolConstructors(t *testing.T) {
	runner, st := newToolServer(t)
	collector := NewCardCollector(1)
	if _, err := ProposeCardTool(collector).Run(context.Background(), json.RawMessage(`{"title":"Public"}`)); err != nil {
		t.Fatal(err)
	}
	if cards := collector.Cards(); len(cards) != 1 || cards[0].Title != "Public" {
		t.Fatalf("cards = %+v", cards)
	}
	addToolTask(t, st, board.Task{Title: "Existing"})
	for _, tool := range []rig.Tool{
		runner.FindSimilarTool("default"), runner.FetchLinkTool(), runner.ListTasksTool("default"),
		runner.GetTaskTool("default"), runner.UpdateTaskTool("default"),
	} {
		if tool.Name == "" || tool.Run == nil {
			t.Fatalf("invalid public tool: %+v", tool)
		}
	}
}

func TestClientConfigurationAndBudgetTransport(t *testing.T) {
	runner := NewRunner(newTestStore(t), "", &http.Client{}, &http.Client{})
	assertClientRejectsBadBaseURLs(t, runner)
	assertBudgetFieldRename(t)
	assertBudgetTransport(t)
}

func assertClientRejectsBadBaseURLs(t *testing.T, runner *Runner) {
	t.Helper()
	for _, base := range []string{"", "not a url", "ftp://example.com", "https://example.com?q=1", "https://u:p@example.com"} {
		client, err := runner.Client(Config{BaseURL: base, Model: "m"})
		if client != nil || err == nil {
			t.Errorf("Client(%q) = %v, %v", base, client, err)
		}
	}
	if got, err := endpoint("https://example.com/api"); err != nil || got != "https://example.com/api/v1/" {
		t.Fatalf("endpoint = %q, %v", got, err)
	}
}

func assertBudgetFieldRename(t *testing.T) {
	t.Helper()
	if got, ok := renameMaxTokens([]byte(`{"max_tokens":7,"model":"m"}`)); !ok || !strings.Contains(string(got), "max_completion_tokens") {
		t.Fatalf("rename = %s, %t", got, ok)
	}
	for _, body := range []string{"[]", `{}`, `{"max_tokens":1,"max_completion_tokens":2}`} {
		if _, ok := renameMaxTokens([]byte(body)); ok {
			t.Errorf("renameMaxTokens(%s) unexpectedly rewrote", body)
		}
	}
}

func assertBudgetTransport(t *testing.T) {
	t.Helper()
	seen := make(chan *http.Request, 1)
	transport := budgetFieldTransport{base: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		seen <- req
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok"))}, nil
	})}
	req := httptest.NewRequest(http.MethodPost, "https://example.com", bytes.NewBufferString(`{"max_tokens":7}`))
	if _, err := transport.RoundTrip(req); err != nil {
		t.Fatal(err)
	}
	rewritten := <-seen
	body, _ := io.ReadAll(rewritten.Body)
	if rewritten.ContentLength != int64(len(body)) || !strings.Contains(string(body), "max_completion_tokens") || rewritten.GetBody == nil {
		t.Fatalf("rewritten request = len %d body %s", rewritten.ContentLength, body)
	}
	noBody := httptest.NewRequest(http.MethodGet, "https://example.com", nil)
	if _, err := transport.RoundTrip(noBody); err != nil {
		t.Fatal(err)
	}
	badRead := httptest.NewRequest(http.MethodPost, "https://example.com", nil)
	badRead.Body = failingReader{}
	if _, err := transport.RoundTrip(badRead); err == nil {
		t.Fatal("unreadable body accepted")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }
func (failingReader) Close() error             { return nil }

func TestProbeUsesDirectStoreConfiguration(t *testing.T) {
	t.Run("success and supplied overlay", func(t *testing.T) {
		fake := &scriptedOpenAI{replies: []fakeReply{{calls: []fakeToolCall{{name: "plasmid_ping", args: `{"marker":"plasmid-probe-v1"}`}}}}}
		runner, closeUpstream := configuredRunner(t, fake, "stored-model")
		defer closeUpstream()
		if err := runner.Probe(context.Background(), "default", Config{Model: "candidate-model"}); err != nil {
			t.Fatalf("Probe: %v", err)
		}
		if !strings.Contains(string(fake.requests()[0]), "candidate-model") {
			t.Fatalf("probe request = %s", fake.requests()[0])
		}
	})

	t.Run("requires tool calling", func(t *testing.T) {
		fake := &scriptedOpenAI{replies: []fakeReply{{content: "pong"}}}
		runner, closeUpstream := configuredRunner(t, fake, "m")
		defer closeUpstream()
		assertAIError(t, runner.Probe(context.Background(), "default", Config{}), http.StatusBadRequest, ToolCallRequiredMessage)
	})

	t.Run("protects stored key", func(t *testing.T) {
		fake := &scriptedOpenAI{}
		runner, closeUpstream := configuredRunner(t, fake, "m")
		defer closeUpstream()
		assertAIError(t, runner.Probe(context.Background(), "default", Config{BaseURL: "https://other.example"}), http.StatusBadRequest, "enter the API key to test a different endpoint")
	})

	t.Run("supplied endpoint and key", func(t *testing.T) {
		fake := &scriptedOpenAI{replies: []fakeReply{{calls: []fakeToolCall{{name: "plasmid_ping", args: `{"marker":"plasmid-probe-v1"}`}}}}}
		runner, closeUpstream := configuredRunner(t, fake, "stored")
		defer closeUpstream()
		stored := runner.aiClient
		candidate := httptest.NewServer(http.HandlerFunc(fake.handler))
		defer candidate.Close()
		runner.aiClient = candidate.Client()
		if err := runner.Probe(context.Background(), "default", Config{BaseURL: candidate.URL, Model: "candidate", Key: "new-key"}); err != nil {
			t.Fatalf("Probe candidate: %v", err)
		}
		runner.aiClient = stored
	})

	t.Run("maps output and upstream errors", func(t *testing.T) {
		assertAIError(t, probeError(oneshot.ErrOutputTruncated, nil), http.StatusUnprocessableEntity, TruncatedReplyMessage)
		assertAIError(t, probeError(errors.New("network"), nil), http.StatusBadGateway, ProbeOpaqueMessage)
		if err := probeError(nil, nil); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("closed store", func(t *testing.T) {
		st := newTestStore(t)
		runner := NewRunner(st, "", &http.Client{}, &http.Client{})
		_ = st.Close()
		assertAIError(t, runner.Probe(context.Background(), "default", Config{}), http.StatusInternalServerError, "storage error")
	})
}

func TestErrorContract(t *testing.T) {
	cause := errors.New("cause")
	err := &Error{Code: 500, Message: "safe", Cause: cause}
	if err.Error() != "safe" || !errors.Is(err, cause) {
		t.Fatalf("error contract = %v", err)
	}
}

func TestSameHostRedirectPolicy(t *testing.T) {
	tests := []struct{ from, to, want string }{
		{"https://example.com/a", "https://example.com/b", ""},
		{"http://example.com/a", "https://example.com/b", ""},
		{"https://example.com/a", "https://other.example/b", "cross-host"},
		{"https://example.com/a", "http://example.com/b", "HTTPS-to-HTTP"},
		{"https://example.com/a", "ftp://example.com/b", "non-HTTP"},
		{"https://example.com/a", "https://u:p@example.com/b", "credentials"},
	}
	for _, test := range tests {
		from, _ := url.Parse(test.from)
		to, _ := url.Parse(test.to)
		err := sameHostRedirect(&http.Request{URL: to}, []*http.Request{{URL: from}})
		if test.want == "" && err != nil || test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)) {
			t.Errorf("redirect %s -> %s = %v", test.from, test.to, err)
		}
	}
	from, _ := url.Parse("https://u:p@example.com/a")
	to, _ := url.Parse("https://example.com/b")
	if err := sameHostRedirect(&http.Request{URL: to}, []*http.Request{{URL: from}}); err == nil {
		t.Fatal("origin credentials accepted")
	}
	via := make([]*http.Request, 10)
	for i := range via {
		via[i] = &http.Request{URL: to}
	}
	if err := sameHostRedirect(&http.Request{URL: to}, via); err == nil {
		t.Fatal("redirect limit accepted")
	}
}

func TestNetworkGuardBranches(t *testing.T) {
	if got := normalizeGuardHost(" [::1] "); got != "::1" {
		t.Fatalf("normalized host = %q", got)
	}
	if hosts := parseAllowedHosts(" Example.COM., ,127.0.0.1 "); !hosts["example.com"] || !hosts["127.0.0.1"] {
		t.Fatalf("hosts = %v", hosts)
	}
	for _, address := range []string{"invalid", "host.test:80", "127.0.0.1:80"} {
		if err := rejectPrivateAddress("tcp", address, nil); err == nil {
			t.Errorf("rejectPrivateAddress(%q) = nil", address)
		}
	}
	if err := rejectPrivateAddress("tcp", "8.8.8.8:53", nil); err != nil {
		t.Fatalf("public address rejected: %v", err)
	}
	transport := guardedTransport(nil, false)
	if _, err := transport.DialContext(context.Background(), "tcp", "invalid"); err == nil {
		t.Fatal("invalid dial address accepted")
	}
	allowed := guardedTransport(map[string]bool{"127.0.0.1": true}, false)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	conn, err := allowed.DialContext(context.Background(), "tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("allowlisted dial: %v", err)
	}
	_ = conn.Close()
	all := guardedTransport(nil, true)
	conn, err = all.DialContext(context.Background(), "tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("allow-all dial: %v", err)
	}
	_ = conn.Close()
	t.Setenv("KB_LINK_ALLOW_PRIVATE", "*")
	client := NewLinkClient()
	client.CloseIdleConnections()
}
