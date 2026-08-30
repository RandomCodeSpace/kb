package ai

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/RandomCodeSpace/plasmid/oneshot"
	"google.golang.org/adk/v2/model"
)

// Probe failure messages. A connection test that cannot say what failed is a
// test the operator cannot act on, so each of these names both the thing that
// went wrong and the field that fixes it. They are shown verbatim in the
// settings overlay, so they stay one short sentence with no secret in them.
const (
	probeMaxOutputTokens     = int32(256)
	ProbeModelMissingMessage = "AI model not configured - fill in the model field"
	ProbeTimeoutMessage      = "the AI endpoint did not answer before the test timed out"
	ProbeCancelledMessage    = "the connection test was cancelled"
	ProbeDNSMessage          = "the AI base URL host does not resolve - check the base URL"
	ProbeTLSMessage          = "the AI endpoint's TLS handshake failed - check the scheme and the certificate"
	ProbeUnreachableMessage  = "could not connect to the AI endpoint - check the base URL and that the server is running"
	ProbeOpaqueMessage       = "upstream request failed"
)

// probeTransport records what one probe's single round trip actually did.
//
// Plasmid reduces model failures to safe classifications. That is right for the
// HTTP surface, which collapses every 502 to one message, and insufficient for
// the local settings overlay, which must say whether the operator mistyped the
// endpoint or key. Recording kb's transport outcome recovers the status or dial
// error without parsing provider-controlled text.
//
// The model factory disables retries, so one probe is one round trip and the
// recorded outcome is unambiguous.
type probeTransport struct {
	base   http.RoundTripper
	status int
	err    error
}

func (t *probeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	res, err := t.base.RoundTrip(req)
	if err != nil {
		t.err = err
		return nil, err
	}
	t.status = res.StatusCode
	return res, nil
}

// probeModel builds the Plasmid model one probe uses, wrapping the guarded
// transport in a recorder. The clone keeps the client's timeout, redirect
// policy and SSRF guard; only the observation is added.
func (r *Runner) probeModel(ctx context.Context, cfg Config) (model.LLM, *probeTransport, error) {
	recorder := &probeTransport{base: http.DefaultTransport}
	client := &http.Client{}
	if base := r.aiClient; base != nil {
		clone := *base
		if clone.Transport != nil {
			recorder.base = clone.Transport
		}
		clone.Transport = recorder
		client = &clone
	} else {
		client.Transport = recorder
	}
	built, err := newModelWithClient(ctx, cfg, client)
	if err != nil {
		return nil, nil, err
	}
	return built, recorder, nil
}

// probeError turns a probe failure into something the operator can act on.
// seen may be nil, which selects the message set that needs no round trip.
//
// Every outcome that describes the network or the upstream keeps code 502.
// That is not a detail: the HTTP probe handler redacts exactly the 502s, so
// the added detail reaches the local settings overlay through Message while
// the remote surface still answers with one opaque sentence. Only a failure
// that describes the caller's own settings - a model field left blank, a
// model that cannot call tools - carries a code that passes through.
func probeError(err error, seen *probeTransport) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, oneshot.ErrToolCallingUnsupported), errors.Is(err, oneshot.ErrToolCallLimit):
		return &Error{Code: http.StatusBadRequest, Message: ToolCallRequiredMessage, Cause: err}
	case errors.Is(err, oneshot.ErrOutputTruncated):
		return &Error{Code: http.StatusUnprocessableEntity, Message: TruncatedReplyMessage, Cause: err}
	case errors.Is(err, context.Canceled):
		return upstreamError(ProbeCancelledMessage, err)
	case errors.Is(err, context.DeadlineExceeded):
		return upstreamError(ProbeTimeoutMessage, err)
	}
	if seen != nil {
		if seen.status != 0 && seen.status/100 != 2 {
			return upstreamError(probeStatusMessage(seen.status), err)
		}
		if seen.err != nil {
			return upstreamError(probeDialMessage(seen.err), err)
		}
	}
	return upstreamError(ProbeOpaqueMessage, err)
}

func upstreamError(message string, cause error) error {
	return &Error{Code: http.StatusBadGateway, Message: message, Cause: cause}
}

// probeStatusMessage names what an upstream status means for this form. The
// status travels in the text because "401" is the string the operator will find
// in their provider's documentation.
func probeStatusMessage(status int) string {
	switch {
	case status == http.StatusUnauthorized:
		return fmt.Sprintf("the AI endpoint rejected the API key (HTTP %d) - check the key", status)
	case status == http.StatusForbidden:
		return fmt.Sprintf("the AI endpoint refused the API key (HTTP %d) - check the key's permissions", status)
	case status == http.StatusNotFound:
		return fmt.Sprintf("the AI endpoint returned HTTP %d - check the base URL and the model name", status)
	case status == http.StatusTooManyRequests:
		return fmt.Sprintf("the AI endpoint rate-limited the test (HTTP %d) - retry shortly", status)
	case status >= 500:
		return fmt.Sprintf("the AI endpoint returned a server error (HTTP %d)", status)
	case status >= 400:
		return fmt.Sprintf("the AI endpoint rejected the request (HTTP %d) - check the model name", status)
	}
	return fmt.Sprintf("the AI endpoint returned HTTP %d", status)
}

// probeDialMessage names a failure that never reached a response. The SSRF
// guard's own text is passed through because it is the one message that also
// says how to allow the thing it just refused.
func probeDialMessage(err error) string {
	if errors.Is(err, ErrPrivateAddress) {
		return ErrPrivateAddress.Error()
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return ProbeDNSMessage
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return ProbeTimeoutMessage
	}
	if isTLSError(err) {
		return ProbeTLSMessage
	}
	return ProbeUnreachableMessage
}

func isTLSError(err error) bool {
	var recordErr tls.RecordHeaderError
	var certErr *tls.CertificateVerificationError
	var authorityErr x509.UnknownAuthorityError
	var hostErr x509.HostnameError
	switch {
	case errors.As(err, &recordErr), errors.As(err, &certErr),
		errors.As(err, &authorityErr), errors.As(err, &hostErr):
		return true
	}
	// http.Transport reports a handshake refused by the peer as a plain alert
	// with no typed error behind it.
	return strings.Contains(err.Error(), "tls:")
}
