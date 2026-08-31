package ai

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"
)

const (
	aiTimeout        = 60 * time.Second
	linkFetchTimeout = 20 * time.Second
)

// NewHTTPClient returns the guarded client used for model endpoints.
func NewHTTPClient() *http.Client {
	allowPrivateSetting := os.Getenv("KB_AI_ALLOW_PRIVATE")
	allowPrivate := allowPrivateSetting == "" || allowPrivateSetting == "1"
	return &http.Client{Timeout: aiTimeout, Transport: guardedTransport(nil, allowPrivate), CheckRedirect: sameHostRedirect}
}

// NewLinkClient returns the separately guarded client used by fetch_link.
func NewLinkClient() *http.Client {
	raw := os.Getenv("KB_LINK_ALLOW_PRIVATE")
	allowAll := raw == "1" || raw == "*"
	var allowHosts map[string]bool
	if !allowAll {
		allowHosts = parseAllowedHosts(raw)
	}
	return &http.Client{Timeout: linkFetchTimeout, Transport: guardedTransport(allowHosts, allowAll), CheckRedirect: sameHostRedirect}
}

func parseAllowedHosts(raw string) map[string]bool {
	hosts := make(map[string]bool)
	for _, host := range strings.Split(raw, ",") {
		if host = normalizeGuardHost(host); host != "" {
			hosts[host] = true
		}
	}
	return hosts
}

func normalizeGuardHost(host string) string {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.String()
	}
	return host
}

// ErrPrivateAddress is the SSRF guard refusing a dial. It is a sentinel rather
// than an anonymous error because the message is the single most actionable
// thing the connection test can say - a local model server is the common case -
// and by the time the failure reaches the probe it has been through the dialer
// and transport, neither of which preserves error text.
var ErrPrivateAddress = errors.New("AI endpoint resolves to a private address (set KB_AI_ALLOW_PRIVATE=1 for local model servers)")

var errRedirectPolicy = errors.New("AI endpoint redirect violates policy")

func redirectPolicyError(message string) error {
	return fmt.Errorf("%w: %s", errRedirectPolicy, message)
}

func rejectPrivateAddress(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid dial address %q", address)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("non-IP dial address %q", address)
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return ErrPrivateAddress
	}
	return nil
}

func guardedTransport(allowHosts map[string]bool, allowAll bool) *http.Transport {
	normalized := make(map[string]bool, len(allowHosts))
	for host, allowed := range allowHosts {
		if allowed {
			normalized[normalizeGuardHost(host)] = true
		}
	}
	return &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("invalid dial address %q", address)
		}
		allowPrivate := allowAll || normalized[normalizeGuardHost(host)]
		dialer := &net.Dialer{Control: func(network, address string, raw syscall.RawConn) error {
			if allowPrivate {
				return nil
			}
			return rejectPrivateAddress(network, address, raw)
		}}
		return dialer.DialContext(ctx, network, address)
	}}
}

func sameHostRedirect(req *http.Request, via []*http.Request) error {
	origin := via[0].URL
	if origin.User != nil || req.URL.User != nil {
		return redirectPolicyError("refusing redirect with URL credentials")
	}
	if !isHTTPURL(origin) || !isHTTPURL(req.URL) {
		return redirectPolicyError("refusing redirect to non-HTTP(S) URL")
	}
	if normalizeGuardHost(req.URL.Hostname()) != normalizeGuardHost(origin.Hostname()) || req.URL.Port() != origin.Port() {
		return redirectPolicyError("refusing cross-host redirect")
	}
	if strings.EqualFold(origin.Scheme, "https") && strings.EqualFold(req.URL.Scheme, "http") {
		return redirectPolicyError("refusing HTTPS-to-HTTP redirect")
	}
	if len(via) >= 10 {
		return redirectPolicyError("stopped after 10 redirects")
	}
	return nil
}

func isHTTPURL(u *url.URL) bool {
	return strings.EqualFold(u.Scheme, "http") || strings.EqualFold(u.Scheme, "https")
}
