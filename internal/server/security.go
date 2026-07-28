package server

import (
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// Open mode has no authentication and binds loopback, which is only a
// boundary if the browser cannot be tricked into crossing it. Two attacks do
// exactly that, so every /api/* request is screened before it reaches auth:
//
//  1. DNS rebinding — an attacker-controlled name resolving to 127.0.0.1
//     makes any page the user visits same-origin with the API. The Host
//     header still carries the attacker's name, so hostAllowed pins it to
//     loopback (or an operator-configured KB_ALLOWED_HOSTS entry).
//  2. CSRF — json.NewDecoder happily reads a text/plain body, which is a
//     CORS-safelisted simple request and therefore not preflighted. So
//     state-changing methods must look same-origin (Sec-Fetch-Site/Origin)
//     and must declare the content type their handler actually parses.
//
// The Host check is enforced in open mode, where it is the whole defense,
// and in any mode once KB_ALLOWED_HOSTS is set. It is off by default under
// token and Entra auth: those requests carry a bearer credential a
// cross-origin page cannot mint — there are no ambient cookies to ride — so
// rebinding buys an attacker nothing there, while pinning Host would 403
// every deployment on a real hostname until an env var is discovered. The
// Origin and content-type checks apply in every mode.
//
// The checks run before authentication so a rejected cross-site request
// never touches the store or spends the stored provider key.
func (s *server) withAPIGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isAPIPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if s.pinHost && !s.hostAllowed(r.Host) {
			http.Error(w, "host not allowed: set KB_ALLOWED_HOSTS to serve kb on this hostname", http.StatusForbidden)
			return
		}
		if isStateChanging(r.Method) {
			if !sameOriginRequest(r) {
				http.Error(w, "cross-site request rejected", http.StatusForbidden)
				return
			}
			if !contentTypeAllowed(r) {
				http.Error(w, "unsupported content type", http.StatusUnsupportedMediaType)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func isAPIPath(p string) bool {
	return p == "/api" || strings.HasPrefix(p, "/api/")
}

func isStateChanging(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}

// parseAllowedHosts splits the comma-separated KB_ALLOWED_HOSTS value into a
// lookup set. Entries are matched with or without a port.
func parseAllowedHosts(raw string) map[string]bool {
	set := make(map[string]bool)
	for _, h := range strings.Split(raw, ",") {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			set[h] = true
		}
	}
	return set
}

// hostAllowed reports whether the Host header names this server: loopback
// literals and "localhost" on any port, plus every KB_ALLOWED_HOSTS entry
// (matched with or without a port). Matching the header — not the resolved
// address — is the point: a rebinding name resolves to 127.0.0.1 but still
// carries the attacker's Host. Whether the predicate is consulted at all is
// pinHost's decision, made once in newServer.
func (s *server) hostAllowed(host string) bool {
	if host == "" {
		return false
	}
	h := strings.ToLower(host)
	if s.allowedHosts[h] {
		return true
	}
	name := h
	if hostOnly, _, err := net.SplitHostPort(h); err == nil {
		name = hostOnly
	}
	name = strings.Trim(name, "[]")
	if s.allowedHosts[name] || name == "localhost" {
		return true
	}
	ip := net.ParseIP(name)
	return ip != nil && ip.IsLoopback()
}

// sameOriginRequest reports whether a state-changing request looks like it
// came from this origin. Sec-Fetch-Site is authoritative when present;
// otherwise Origin must match Host. A request with neither header is not
// browser-initiated (CLI, curl, MCP) and is allowed — those clients carry no
// ambient credentials for an attacker to ride.
func sameOriginRequest(r *http.Request) bool {
	switch strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site"))) {
	case "same-origin", "none":
		return true
	case "":
		// Older browser or a non-browser client: fall through to Origin.
	default:
		// same-site and cross-site: a sibling subdomain must not write.
		return false
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		// Includes the literal "null" origin (sandboxed frame, file://).
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

// contentTypeAllowed requires a body to declare the format its handler
// parses. This is what closes the simple-request CSRF hole: a cross-site
// form or no-preflight fetch can only send text/plain, multipart/form-data
// or application/x-www-form-urlencoded, none of which are accepted.
func contentTypeAllowed(r *http.Request) bool {
	ct := strings.TrimSpace(r.Header.Get("Content-Type"))
	if ct == "" {
		// No declared type is fine only for a bodyless request such as
		// POST /api/ai/test.
		return r.ContentLength == 0
	}
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return false
	}
	if r.URL.Path == "/api/board" {
		// The board wire format is markdown, not JSON.
		return mt == "text/markdown"
	}
	return mt == "application/json"
}

// setFrameGuards blocks framing of the SPA. Clickjacking the board would let
// a hostile page drive same-origin, already-authenticated actions, so both
// the modern directive and the legacy header are sent.
func setFrameGuards(h http.Header) {
	h.Set("Content-Security-Policy", "frame-ancestors 'none'")
	h.Set("X-Frame-Options", "DENY")
}
