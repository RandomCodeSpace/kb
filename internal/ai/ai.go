// Package ai implements kb's model probe, skill runner, and board tools.
// It depends on the store directly so local UIs do not need an HTTP
// server to use AI features.
package ai

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/RandomCodeSpace/plasmid/oneshot"
	plasmidopenai "github.com/RandomCodeSpace/plasmid/openai"

	"github.com/RandomCodeSpace/kb/internal/store"
)

const (
	maxTokensCeiling        = 8192
	defaultStoryCount       = 8
	maxStoryCount           = 20
	TruncatedReplyMessage   = "the model's reply hit the output limit and was cut off — ask for less in one request"
	ToolCallRequiredMessage = "model must support tool calling"
)

// Error carries the caller-facing status category without depending on the
// server package. HTTP adapters may use Code directly; local callers can use
// Error.As and Error.Unwrap without standing up a server.
type Error struct {
	Code    int
	Message string
	Cause   error
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.Cause }

// Config is one resolved OpenAI-compatible endpoint configuration.
type Config struct {
	BaseURL string
	Model   string
	Key     string
}

// Runner owns the direct store and clients used by AI runs.
type Runner struct {
	store      *store.Store
	skillsDir  string
	aiClient   *http.Client
	linkClient *http.Client
}

// NewRunner constructs a direct-store runner. Nil clients select the guarded
// defaults; explicit clients are a test and embedding seam.
func NewRunner(st *store.Store, skillsDir string, aiClient, linkClient *http.Client) *Runner {
	if aiClient == nil {
		aiClient = NewHTTPClient()
	}
	if linkClient == nil {
		linkClient = NewLinkClient()
	}
	return &Runner{store: st, skillsDir: skillsDir, aiClient: aiClient, linkClient: linkClient}
}

func (r *Runner) storedConfig(user string) (Config, error) {
	set, err := r.store.AISettings(user)
	if err != nil {
		log.Printf("ai: settings for %s: %v", logSafe(user), err)
		return Config{}, &Error{Code: http.StatusInternalServerError, Message: "storage error", Cause: err}
	}
	key, err := r.store.AIKey(user)
	if err != nil {
		log.Printf("ai: key for %s: %v", logSafe(user), err)
		return Config{}, &Error{Code: http.StatusInternalServerError, Message: "storage error", Cause: err}
	}
	return Config{BaseURL: set.BaseURL, Model: set.Model, Key: key}, nil
}

// Probe validates either the stored AI configuration or supplied form values.
// Blank supplied fields retain the stored value. A stored key never travels
// to a newly supplied origin.
func (r *Runner) Probe(ctx context.Context, user string, supplied Config) error {
	cfg, err := r.storedConfig(user)
	if err != nil {
		return err
	}
	base := strings.TrimSpace(supplied.BaseURL)
	if strings.TrimSpace(supplied.Key) == "" && base != "" && cfg.Key != "" &&
		!store.SameAIOrigin(cfg.BaseURL, base) {
		return &Error{Code: http.StatusBadRequest, Message: "enter the API key to test a different endpoint"}
	}
	if base != "" {
		cfg.BaseURL = base
	}
	if model := strings.TrimSpace(supplied.Model); model != "" {
		cfg.Model = model
	}
	if key := strings.TrimSpace(supplied.Key); key != "" {
		cfg.Key = key
	}
	modelValue, recorder, err := r.probeModel(ctx, cfg)
	if err != nil {
		// The provider validates the base URL before the model. Remap only its
		// model validation so the settings overlay keeps the existing message.
		var validationErr *plasmidopenai.ValidationError
		if errors.As(err, &validationErr) && validationErr.Field == "model" {
			return &Error{Code: http.StatusBadRequest, Message: ProbeModelMissingMessage, Cause: err}
		}
		return err
	}
	_, err = oneshot.Probe(ctx, oneshot.ProbeRequest{Model: modelValue, MaxOutputTokens: probeMaxOutputTokens})
	return probeError(err, recorder)
}

func endpoint(base string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil || u.Host == "" {
		return "", errors.New("invalid AI base URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("AI base URL scheme must be http or https")
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", errors.New("AI base URL must not contain query or fragment")
	}
	if u.User != nil {
		return "", errors.New("AI base URL must not contain a username or password — put the key in the API key field")
	}
	p := strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(p, "/v1") {
		p += "/v1"
	}
	u.Path = p + "/"
	u.RawQuery, u.Fragment = "", ""
	return u.String(), nil
}

// ValidateBaseURL applies the same endpoint validation used by model runs
// without constructing a client or reading a credential.
func ValidateBaseURL(base string) error {
	_, err := endpoint(base)
	return err
}

func usesMaxCompletionTokens(model string) bool {
	name := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	return strings.HasPrefix(name, "gpt-5") || reasoningSeriesRe.MatchString(name)
}

var reasoningSeriesRe = regexp.MustCompile(`^o\d+(-|$)`)
