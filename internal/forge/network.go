package forge

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
)

// NewHTTPClient returns the guarded forge client shared by every frontend.
func NewHTTPClient() *http.Client {
	raw := os.Getenv("KB_FORGE_ALLOW_PRIVATE")
	allowAll := raw == "1" || raw == "*"
	var allowed map[string]bool
	if !allowAll {
		allowed = parseAllowedHosts(raw)
	}
	return &http.Client{Timeout: forgeTimeout, Transport: guardedTransport(allowed, allowAll), CheckRedirect: sameHostRedirect}
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

func guardedTransport(allowHosts map[string]bool, allowAll bool) *http.Transport {
	return &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("invalid dial address %q", address)
		}
		allowPrivate := allowAll || allowHosts[normalizeGuardHost(host)]
		dialer := &net.Dialer{Control: func(_, address string, _ syscall.RawConn) error {
			if allowPrivate {
				return nil
			}
			ipHost, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(ipHost)
			if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
				return errors.New("forge endpoint resolves to a private address")
			}
			return nil
		}}
		return dialer.DialContext(ctx, network, address)
	}}
}

func sameHostRedirect(request *http.Request, via []*http.Request) error {
	origin := via[0].URL
	if origin.User != nil || request.URL.User != nil {
		return errors.New("refusing redirect with URL credentials")
	}
	if !httpURL(origin) || !httpURL(request.URL) {
		return errors.New("refusing redirect to non-HTTP(S) URL")
	}
	if normalizeGuardHost(origin.Hostname()) != normalizeGuardHost(request.URL.Hostname()) || origin.Port() != request.URL.Port() {
		return errors.New("refusing cross-host redirect")
	}
	if strings.EqualFold(origin.Scheme, "https") && strings.EqualFold(request.URL.Scheme, "http") {
		return errors.New("refusing HTTPS-to-HTTP redirect")
	}
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	return nil
}

func httpURL(value *url.URL) bool {
	return strings.EqualFold(value.Scheme, "http") || strings.EqualFold(value.Scheme, "https")
}
