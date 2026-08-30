package ai

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	plasmidopenai "github.com/RandomCodeSpace/plasmid/openai"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

func TestNewModelUsesExplicitChatCompletionsConfiguration(t *testing.T) {
	for _, test := range []struct {
		name        string
		model       string
		key         string
		wantAuth    string
		wantBudget  string
		otherBudget string
	}{
		{name: "max tokens without key", model: "gpt-4o", wantBudget: "max_tokens", otherBudget: "max_completion_tokens"},
		{name: "max completion tokens with key", model: "openai/gpt-5-mini", key: "stored-secret", wantAuth: "Bearer stored-secret", wantBudget: "max_completion_tokens", otherBudget: "max_tokens"},
		{name: "o series", model: "o3-mini", key: "stored-secret", wantAuth: "Bearer stored-secret", wantBudget: "max_completion_tokens", otherBudget: "max_tokens"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("OPENAI_API_KEY", "ambient-api-secret")
			t.Setenv("OPENAI_ADMIN_KEY", "ambient-admin-secret")
			t.Setenv("OPENAI_BASE_URL", "https://ambient.invalid/v1")
			t.Setenv("OPENAI_ORG_ID", "ambient-org")
			t.Setenv("OPENAI_PROJECT_ID", "ambient-project")
			t.Setenv("OPENAI_CUSTOM_HEADERS", "Authorization: Bearer ambient-header-secret\nX-Ambient: leaked")

			var (
				path    string
				headers http.Header
				body    map[string]any
			)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path = r.URL.EscapedPath()
				headers = r.Header.Clone()
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode request: %v", err)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
			}))
			defer upstream.Close()

			runner := &Runner{aiClient: upstream.Client()}
			llm, err := runner.newModel(t.Context(), Config{
				BaseURL: upstream.URL + "/gateway", Model: test.model, Key: test.key,
			})
			if err != nil {
				t.Fatalf("newModel: %v", err)
			}
			if llm.Name() != test.model {
				t.Fatalf("model name = %q, want %q", llm.Name(), test.model)
			}
			if _, err := oneProviderResponse(llm, t.Context(), 73); err != nil {
				t.Fatalf("GenerateContent: %v", err)
			}

			if path != "/gateway/v1/chat/completions" {
				t.Fatalf("request path = %q, want Chat Completions under the normalized base", path)
			}
			if got := headers.Get("Authorization"); got != test.wantAuth {
				t.Errorf("Authorization = %q, want %q", got, test.wantAuth)
			}
			for _, name := range []string{"OpenAI-Organization", "OpenAI-Project", "X-Ambient"} {
				if got := headers.Get(name); got != "" {
					t.Errorf("%s = %q, want empty", name, got)
				}
			}
			if got := body["model"]; got != test.model {
				t.Errorf("model = %#v, want %q", got, test.model)
			}
			if got := body[test.wantBudget]; got != float64(73) {
				t.Errorf("%s = %#v, want 73", test.wantBudget, got)
			}
			if _, exists := body[test.otherBudget]; exists {
				t.Errorf("request also sent %s", test.otherBudget)
			}
		})
	}
}

func TestNewModelKeepsCallerHTTPPolicyAndTypedErrors(t *testing.T) {
	policyErr := errors.New("caller policy refused request")
	client := &http.Client{Transport: providerRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, policyErr
	})}
	llm, err := newModelWithClient(t.Context(), Config{
		BaseURL: "https://provider.invalid", Model: "gpt-4o", Key: "secret",
	}, client)
	if err != nil {
		t.Fatalf("newModelWithClient: %v", err)
	}
	_, err = oneProviderResponse(llm, t.Context(), 8)
	var requestErr *plasmidopenai.RequestError
	if !errors.As(err, &requestErr) || requestErr.Failure != plasmidopenai.RequestFailureTransport {
		t.Fatalf("GenerateContent error = %T %v, want typed transport error", err, err)
	}
	if !errors.Is(err, policyErr) {
		t.Fatal("transport error does not preserve caller policy sentinel")
	}
	if strings.Contains(err.Error(), "policy refused") || strings.Contains(err.Error(), "provider.invalid") {
		t.Fatalf("transport error leaked caller or endpoint detail: %v", err)
	}

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = oneProviderResponse(llm, cancelled, 8)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled request error = %T %v, want context.Canceled", err, err)
	}
}

func TestNewModelKeepsGuardedClientSSRFPolicy(t *testing.T) {
	t.Setenv("KB_AI_ALLOW_PRIVATE", "")
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	}))
	defer upstream.Close()

	llm, err := newModelWithClient(t.Context(), Config{
		BaseURL: upstream.URL, Model: "gpt-4o",
	}, NewHTTPClient())
	if err != nil {
		t.Fatalf("newModelWithClient: %v", err)
	}
	_, err = oneProviderResponse(llm, t.Context(), 8)
	var requestErr *plasmidopenai.RequestError
	if !errors.As(err, &requestErr) || requestErr.Failure != plasmidopenai.RequestFailureTransport {
		t.Fatalf("GenerateContent error = %T %v, want typed guarded transport error", err, err)
	}
	if !errors.Is(err, ErrPrivateAddress) {
		t.Fatalf("GenerateContent error = %v, want ErrPrivateAddress", err)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("private endpoint received %d requests, want none", got)
	}
}

func TestNewModelDisablesRetriesAndPreservesProviderStatus(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "0")
		http.Error(w, `{"error":{"message":"provider secret"}}`, http.StatusTooManyRequests)
	}))
	defer upstream.Close()

	llm, err := newModelWithClient(t.Context(), Config{BaseURL: upstream.URL, Model: "gpt-4o"}, upstream.Client())
	if err != nil {
		t.Fatalf("newModelWithClient: %v", err)
	}
	_, err = oneProviderResponse(llm, t.Context(), 8)
	var requestErr *plasmidopenai.RequestError
	if !errors.As(err, &requestErr) || requestErr.Failure != plasmidopenai.RequestFailureProvider || requestErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("GenerateContent error = %#v, want typed HTTP 429 provider error", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("requests = %d, want exactly one", got)
	}
	if strings.Contains(err.Error(), "provider secret") || strings.Contains(err.Error(), upstream.URL) {
		t.Fatalf("provider error leaked response or endpoint detail: %v", err)
	}
}

func TestNewModelCapsDecompressedResponsesAtOneMiB(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		compressed := gzip.NewWriter(w)
		_ = json.NewEncoder(compressed).Encode(map[string]any{"choices": []any{map[string]any{
			"message":       map[string]any{"role": "assistant", "content": strings.Repeat("x", int(modelResponseLimit))},
			"finish_reason": "stop",
		}}})
		_ = compressed.Close()
	}))
	defer upstream.Close()

	llm, err := newModelWithClient(t.Context(), Config{BaseURL: upstream.URL, Model: "gpt-4o"}, upstream.Client())
	if err != nil {
		t.Fatalf("newModelWithClient: %v", err)
	}
	_, err = oneProviderResponse(llm, t.Context(), 8)
	var oversized *plasmidopenai.ResponseTooLargeError
	if !errors.As(err, &oversized) || oversized.Limit != modelResponseLimit {
		t.Fatalf("GenerateContent error = %T %v, want %d-byte response limit", err, err, modelResponseLimit)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("requests = %d, want exactly one", got)
	}
}

func TestNewModelValidatesConfigurationWithoutLosingTypedErrors(t *testing.T) {
	client := &http.Client{}
	for _, baseURL := range []string{"", "not a URL", "ftp://provider.example", "https://provider.example?secret=yes", "https://user:pass@provider.example"} {
		llm, err := newModelWithClient(t.Context(), Config{BaseURL: baseURL, Model: "gpt-4o"}, client)
		var aiErr *Error
		if llm != nil || !errors.As(err, &aiErr) || aiErr.Code != http.StatusBadRequest {
			t.Errorf("newModelWithClient(%q) = %v, %T %v, want kb 400", baseURL, llm, err, err)
		}
	}

	llm, err := newModelWithClient(t.Context(), Config{BaseURL: "https://provider.example", Model: ""}, client)
	var validationErr *plasmidopenai.ValidationError
	if llm != nil || !errors.As(err, &validationErr) || validationErr.Field != "model" {
		t.Fatalf("blank model = %v, %T %v, want typed model validation error", llm, err, err)
	}

	llm, err = newModelWithClient(t.Context(), Config{BaseURL: "https://provider.example", Model: "gpt-4o"}, nil)
	if llm != nil || !errors.As(err, &validationErr) || validationErr.Field != "http_client" {
		t.Fatalf("nil client = %v, %T %v, want typed client validation error", llm, err, err)
	}
}

func oneProviderResponse(llm model.LLM, ctx context.Context, maxTokens int32) (*model.LLMResponse, error) {
	request := &model.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText("hello", genai.RoleUser)},
		Config:   &genai.GenerateContentConfig{MaxOutputTokens: maxTokens},
	}
	var response *model.LLMResponse
	for current, err := range llm.GenerateContent(ctx, request, false) {
		if err != nil {
			return nil, err
		}
		if response != nil {
			return nil, errors.New("model yielded multiple responses")
		}
		response = current
	}
	return response, nil
}

type providerRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn providerRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}
