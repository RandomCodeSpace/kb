package ai

import (
	"context"
	"net/http"
	"strings"

	plasmidopenai "github.com/RandomCodeSpace/plasmid/openai"
	"google.golang.org/adk/v2/model"
)

const modelResponseLimit int64 = 1 << 20

// newModel constructs the model used by one runner invocation. The caller's
// HTTP client remains authoritative for timeouts, redirects, and dial policy.
func (r *Runner) newModel(ctx context.Context, cfg Config) (model.LLM, error) {
	return newModelWithClient(ctx, cfg, r.aiClient)
}

// newModelWithClient is the provider construction seam used by normal runs
// and by probes that wrap the guarded client to record one request's outcome.
func newModelWithClient(ctx context.Context, cfg Config, client *http.Client) (model.LLM, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, &Error{Code: http.StatusBadRequest, Message: "AI base URL not configured"}
	}
	baseURL, err := endpoint(cfg.BaseURL)
	if err != nil {
		return nil, &Error{Code: http.StatusBadRequest, Message: err.Error(), Cause: err}
	}

	tokenLimit := plasmidopenai.ChatTokenLimitMaxTokens
	if usesMaxCompletionTokens(cfg.Model) {
		tokenLimit = plasmidopenai.ChatTokenLimitMaxCompletionTokens
	}

	return plasmidopenai.New(ctx, plasmidopenai.Config{
		Protocol:         plasmidopenai.ProtocolChatCompletions,
		Model:            cfg.Model,
		BaseURL:          baseURL,
		APIKey:           cfg.Key,
		HTTPClient:       client,
		MaxResponseBytes: modelResponseLimit,
		MaxRetries:       0,
		ChatTokenLimit:   tokenLimit,
	})
}
