package ai

import (
	"context"
	"errors"
	"iter"
	"net/http"
	"sync"

	"github.com/RandomCodeSpace/plasmid/oneshot"
	plasmidopenai "github.com/RandomCodeSpace/plasmid/openai"
	"google.golang.org/adk/v2/model"
)

const (
	runCancelledMessage        = "the AI request was cancelled"
	runTimeoutMessage          = "the AI endpoint did not answer before the request timed out"
	runResponseTooLargeMessage = "the AI endpoint response exceeded the 1 MiB limit"
	runInvalidResponseMessage  = "the AI endpoint returned an invalid response"
	runExecutionFailedMessage  = "the AI request failed during model execution"
	runNoResponseMessage       = "the model returned no usable response"
	runRedirectMessage         = "the AI endpoint returned a disallowed redirect - check the base URL"
)

// modelObservation retains only safe failure categories around one model.
// The wrapped Plasmid model still owns response parsing and redaction; kb
// records typed provider failures before one-shot deliberately erases their
// causes, and records its own transport errors for local diagnostics.
type modelObservation struct {
	base http.RoundTripper

	mu               sync.Mutex
	status           int
	transportErr     error
	requestFailure   plasmidopenai.RequestFailure
	providerStatus   int
	responseTooLarge bool
	redirectFailure  bool
}

func (o *modelObservation) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := o.base.RoundTrip(request)
	o.mu.Lock()
	defer o.mu.Unlock()
	if err != nil {
		o.transportErr = err
		return nil, err
	}
	if response != nil {
		o.status = response.StatusCode
	}
	return response, nil
}

func (o *modelObservation) recordModelError(err error) {
	var requestErr *plasmidopenai.RequestError
	o.mu.Lock()
	defer o.mu.Unlock()
	if errors.As(err, &requestErr) {
		o.requestFailure = requestErr.Failure
		o.providerStatus = requestErr.StatusCode
	}
	o.responseTooLarge = o.responseTooLarge || errors.Is(err, &plasmidopenai.ResponseTooLargeError{})
	o.redirectFailure = o.redirectFailure || errors.Is(err, errRedirectPolicy)
}

func (o *modelObservation) snapshot() modelObservationSnapshot {
	if o == nil {
		return modelObservationSnapshot{}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return modelObservationSnapshot{
		status: o.status, transportErr: o.transportErr,
		requestFailure: o.requestFailure, providerStatus: o.providerStatus,
		responseTooLarge: o.responseTooLarge, redirectFailure: o.redirectFailure,
	}
}

type modelObservationSnapshot struct {
	status           int
	transportErr     error
	requestFailure   plasmidopenai.RequestFailure
	providerStatus   int
	responseTooLarge bool
	redirectFailure  bool
}

type observedModel struct {
	model.LLM
	observation *modelObservation
}

type toolCallRecoveryMarker interface {
	MarkToolCallRecovery(map[string]any)
}

type observedRecoveringModel struct {
	*observedModel
	recovery toolCallRecoveryMarker
}

func (m *observedRecoveringModel) MarkToolCallRecovery(tools map[string]any) {
	m.recovery.MarkToolCallRecovery(tools)
}

func (m *observedModel) GenerateContent(ctx context.Context, request *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		for response, err := range m.LLM.GenerateContent(ctx, request, stream) {
			if err != nil {
				m.observation.recordModelError(err)
			}
			if !yield(response, err) {
				return
			}
		}
	}
}

// newObservedModel clones the caller-owned guarded client and adds observation
// without changing its timeout, redirect, SSRF, or credential policy.
func (r *Runner) newObservedModel(ctx context.Context, cfg Config) (model.LLM, *modelObservation, error) {
	observation := &modelObservation{base: http.DefaultTransport}
	client := &http.Client{Transport: observation}
	if r.aiClient != nil {
		clone := *r.aiClient
		if clone.Transport != nil {
			observation.base = clone.Transport
		}
		clone.Transport = observation
		client = &clone
	}
	built, err := newModelWithClient(ctx, cfg, client)
	if err != nil {
		return nil, nil, err
	}
	observed := &observedModel{LLM: built, observation: observation}
	if recovery, ok := built.(toolCallRecoveryMarker); ok {
		return &observedRecoveringModel{observedModel: observed, recovery: recovery}, observation, nil
	}
	return observed, observation, nil
}

func runFailureMessage(err error, observation *modelObservation) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return runTimeoutMessage
	case errors.Is(err, context.Canceled), errors.Is(err, oneshot.ErrCanceled):
		return runCancelledMessage
	}

	seen := observation.snapshot()
	if seen.responseTooLarge {
		return runResponseTooLargeMessage
	}
	if seen.redirectFailure {
		return runRedirectMessage
	}
	status := seen.providerStatus
	if status == 0 && seen.status/100 != 2 {
		status = seen.status
	}
	if status != 0 {
		return runStatusMessage(status)
	}
	if seen.transportErr != nil {
		if errors.Is(seen.transportErr, errRedirectPolicy) {
			return runRedirectMessage
		}
		return probeDialMessage(seen.transportErr)
	}
	if seen.requestFailure == plasmidopenai.RequestFailureResponse {
		return runInvalidResponseMessage
	}
	if errors.Is(err, oneshot.ErrNoFinalResponse) {
		return runNoResponseMessage
	}
	if errors.Is(err, oneshot.ErrToolCallingUnsupported) {
		return ToolCallRequiredMessage
	}
	return runExecutionFailedMessage
}

func runStatusMessage(status int) string {
	if status == http.StatusTooManyRequests {
		return "the AI endpoint rate-limited the request (HTTP 429) - retry shortly"
	}
	return probeStatusMessage(status)
}
