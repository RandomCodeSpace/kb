package ai

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/RandomCodeSpace/plasmid/oneshot"
)

// probeRunner is a runner whose stored configuration points at handler and
// whose AI transport is the recorded one under test.
func probeRunner(t *testing.T, handler http.HandlerFunc) *Runner {
	t.Helper()
	upstream := httptest.NewServer(handler)
	t.Cleanup(upstream.Close)
	st := newTestStore(t)
	base, model, key := upstream.URL, "probe-model", "sk-test"
	if _, err := st.SetAISettings("default", &base, &model, &key); err != nil {
		t.Fatalf("SetAISettings: %v", err)
	}
	return NewRunner(st, "", upstream.Client(), upstream.Client())
}

// TestProbeNamesTheUpstreamStatus is the defect of issue 231: every upstream
// refusal used to arrive at the settings overlay as "upstream request failed",
// which tells the operator nothing about which field to fix. The code stays
// 502 so the HTTP surface keeps redacting it; the detail rides on Message.
func TestProbeNamesTheUpstreamStatus(t *testing.T) {
	for _, tc := range []struct {
		status  int
		wantSub string
	}{
		{http.StatusUnauthorized, "rejected the API key (HTTP 401)"},
		{http.StatusForbidden, "refused the API key (HTTP 403)"},
		{http.StatusNotFound, "HTTP 404 - check the base URL and the model name"},
		{http.StatusTooManyRequests, "rate-limited the test (HTTP 429)"},
		{http.StatusBadRequest, "rejected the request (HTTP 400)"},
		{http.StatusInternalServerError, "server error (HTTP 500)"},
		{http.StatusBadGateway, "server error (HTTP 502)"},
	} {
		t.Run(fmt.Sprintf("status %d", tc.status), func(t *testing.T) {
			runner := probeRunner(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"error":{"message":"nope"}}`))
			})
			err := runner.Probe(context.Background(), "default", Config{})
			var aiErr *Error
			if !errors.As(err, &aiErr) {
				t.Fatalf("Probe = %#v, want *Error", err)
			}
			if aiErr.Code != http.StatusBadGateway || !strings.Contains(aiErr.Message, tc.wantSub) {
				t.Fatalf("Probe = %d %q, want 502 containing %q", aiErr.Code, aiErr.Message, tc.wantSub)
			}
			if aiErr.Message == ProbeOpaqueMessage {
				t.Fatal("failure reported without a cause")
			}
		})
	}
}

// TestProbeStatusMessageFallback covers a non-2xx below 400, which no provider
// should send but which must still name itself rather than fall back.
func TestProbeStatusMessageFallback(t *testing.T) {
	if message := probeStatusMessage(http.StatusFound); message != "the AI endpoint returned HTTP 302" {
		t.Fatalf("probeStatusMessage(302) = %q", message)
	}
}

func TestProbeReportsMissingModel(t *testing.T) {
	st := newTestStore(t)
	base, model, key := "https://api.example.com/v1", "", "sk-test"
	if _, err := st.SetAISettings("default", &base, &model, &key); err != nil {
		t.Fatalf("SetAISettings: %v", err)
	}
	runner := NewRunner(st, "", &http.Client{}, &http.Client{})
	assertAIError(t, runner.Probe(context.Background(), "default", Config{}), http.StatusBadRequest, ProbeModelMissingMessage)
}

func TestProbeReportsBaseURLBeforeMissingModel(t *testing.T) {
	st := newTestStore(t)
	base, model, key := "ftp://api.example.com", "", "sk-test"
	if _, err := st.SetAISettings("default", &base, &model, &key); err != nil {
		t.Fatalf("SetAISettings: %v", err)
	}
	runner := NewRunner(st, "", &http.Client{}, &http.Client{})
	assertAIError(t, runner.Probe(t.Context(), "default", Config{}), http.StatusBadRequest, "AI base URL scheme must be http or https")
}

// TestProbeReportsPrivateEndpoint is the message the SSRF guard writes and that
// nothing downstream used to preserve. A local model server is the common
// reason a connection test fails, so the test must say how to allow it.
func TestProbeReportsPrivateEndpoint(t *testing.T) {
	t.Setenv("KB_AI_ALLOW_PRIVATE", "")
	st := newTestStore(t)
	base, model, key := "http://127.0.0.1:11434/v1", "llama", ""
	if _, err := st.SetAISettings("default", &base, &model, &key); err != nil {
		t.Fatalf("SetAISettings: %v", err)
	}
	runner := NewRunner(st, "", NewHTTPClient(), NewLinkClient())
	err := runner.Probe(context.Background(), "default", Config{})
	assertAIError(t, err, http.StatusBadGateway, ErrPrivateAddress.Error())
	if !strings.Contains(err.Error(), "KB_AI_ALLOW_PRIVATE=1") {
		t.Fatalf("Probe = %q, want the allow-list hint", err)
	}
}

func TestProbeDialMessages(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"dns", &net.DNSError{Err: "no such host", Name: "nope.invalid", IsNotFound: true}, ProbeDNSMessage},
		{"timeout", &net.OpError{Op: "dial", Err: timeoutError{}}, ProbeTimeoutMessage},
		{"tls record", tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"}, ProbeTLSMessage},
		{"tls alert", errors.New("remote error: tls: unknown certificate authority"), ProbeTLSMessage},
		{"refused", &net.OpError{Op: "dial", Err: errors.New("connection refused")}, ProbeUnreachableMessage},
		{"private", &net.OpError{Op: "dial", Err: ErrPrivateAddress}, ErrPrivateAddress.Error()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if message := probeDialMessage(tc.err); message != tc.want {
				t.Fatalf("probeDialMessage = %q, want %q", message, tc.want)
			}
			// The same error routed through probeError must keep the message.
			seen := &probeTransport{err: tc.err}
			assertAIError(t, probeError(errors.New("ai backend: request failed"), seen), http.StatusBadGateway, tc.want)
		})
	}
}

func TestProbeErrorClassifiesContext(t *testing.T) {
	assertAIError(t, probeError(fmt.Errorf("ai backend: %w", context.Canceled), nil), http.StatusBadGateway, ProbeCancelledMessage)
	assertAIError(t, probeError(fmt.Errorf("ai backend: %w", context.DeadlineExceeded), nil), http.StatusBadGateway, ProbeTimeoutMessage)
	// A recorded 2xx with a failure after it is a reply kb could not use, not a
	// transport problem: it stays opaque rather than blaming the endpoint.
	assertAIError(t, probeError(errors.New("boom"), &probeTransport{status: http.StatusOK}), http.StatusBadGateway, ProbeOpaqueMessage)
}

func TestProbeCancellationMakesOneRequest(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{}, 1)
	runner := probeRunner(t, func(_ http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		<-request.Context().Done()
	})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- runner.Probe(ctx, "default", Config{}) }()
	<-started
	cancel()
	err := <-done
	assertAIError(t, err, http.StatusBadGateway, ProbeCancelledMessage)
	if got := calls.Load(); got != 1 {
		t.Fatalf("upstream calls = %d, want 1", got)
	}
}

// TestProbeModelPreservesGuardedTransport keeps the recorder from becoming a
// hole in the SSRF guard: the clone must dial through the client it wraps.
func TestProbeModelPreservesGuardedTransport(t *testing.T) {
	st := newTestStore(t)
	runner := NewRunner(st, "", nil, nil)
	modelValue, recorder, err := runner.probeModel(t.Context(), Config{BaseURL: "https://api.example.com/v1", Model: "m"})
	if err != nil {
		t.Fatalf("probeModel: %v", err)
	}
	if modelValue == nil || recorder == nil || recorder.base == nil {
		t.Fatal("probeModel returned an unwired recorder")
	}
	if _, ok := recorder.base.(*http.Transport); !ok {
		t.Fatalf("recorder wraps %T, want the guarded *http.Transport", recorder.base)
	}
}

// TestProbeModelWithoutHTTPClient covers a runner whose AI client is nil, the
// embedding seam NewRunner fills but a hand-built Runner may not.
func TestProbeModelWithoutHTTPClient(t *testing.T) {
	runner := &Runner{}
	_, recorder, err := runner.probeModel(t.Context(), Config{BaseURL: "https://api.example.com/v1", Model: "m"})
	if err != nil {
		t.Fatalf("probeModel: %v", err)
	}
	if recorder.base != http.DefaultTransport {
		t.Fatalf("recorder.base = %#v, want http.DefaultTransport", recorder.base)
	}
}

func TestProbeModelRejectsBadConfiguration(t *testing.T) {
	runner := &Runner{}
	if _, _, err := runner.probeModel(t.Context(), Config{Model: "m"}); err == nil {
		t.Fatal("blank base URL accepted")
	}
	if _, _, err := runner.probeModel(t.Context(), Config{BaseURL: "ftp://example.com", Model: "m"}); err == nil {
		t.Fatal("non-HTTP base URL accepted")
	}
}

func TestProbeErrorMapsPlasmidSentinels(t *testing.T) {
	assertAIError(t, probeError(oneshot.ErrToolCallingUnsupported, nil), http.StatusBadRequest, ToolCallRequiredMessage)
	assertAIError(t, probeError(oneshot.ErrToolCallLimit, nil), http.StatusBadRequest, ToolCallRequiredMessage)
	assertAIError(t, probeError(oneshot.ErrOutputTruncated, nil), http.StatusUnprocessableEntity, TruncatedReplyMessage)
	assertAIError(t, probeError(oneshot.ErrExecutionFailed, &probeTransport{status: http.StatusOK}), http.StatusBadGateway, ProbeOpaqueMessage)
}

// TestProbeTransportRecordsOutcome exercises the recorder directly, including
// the transport error branch a served response never reaches.
func TestProbeTransportRecordsOutcome(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	boom := errors.New("boom")
	failing := &probeTransport{base: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, boom })}
	if _, err := failing.RoundTrip(req); !errors.Is(err, boom) || !errors.Is(failing.err, boom) {
		t.Fatalf("RoundTrip = %v, recorded %v", err, failing.err)
	}
	ok := &probeTransport{base: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusTeapot, Body: http.NoBody}, nil
	})}
	if _, err := ok.RoundTrip(req); err != nil || ok.status != http.StatusTeapot {
		t.Fatalf("RoundTrip = %v, status %d", err, ok.status)
	}
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }
