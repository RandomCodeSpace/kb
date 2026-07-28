package server

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/board"
)

// rawRequest speaks HTTP/1.1 down a socket instead of going through
// http.Client, because the attacks under test live in headers a client
// normalizes away: Host must carry a name that is not the address dialed,
// and Origin/Sec-Fetch-Site must be set to values a browser would refuse to
// let script forge.
func rawRequest(t *testing.T, addr string, lines []string, body string) *http.Response {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	var sb strings.Builder
	for _, l := range lines {
		sb.WriteString(l + "\r\n")
	}
	sb.WriteString(fmt.Sprintf("Content-Length: %d\r\n", len(body)))
	sb.WriteString("Connection: close\r\n\r\n")
	sb.WriteString(body)
	if _, err := conn.Write([]byte(sb.String())); err != nil {
		t.Fatalf("write: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// newRawServer starts a real listener so tests can dial it, seeded with one
// board.
func newRawServer(t *testing.T, cfg Config) string {
	t.Helper()
	st := newTestStore(t)
	seed := board.Parse("# Secret Board\n\n## To Do\n\n- [ ] private task\n")
	if err := st.ReplaceBoard("default", seed); err != nil {
		t.Fatalf("seed board: %v", err)
	}
	srv := httptest.NewServer(New(cfg, testStatic, st))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

func readBodyString(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

// TestRebindingAndCSRFRejected reproduces the two attacks S1 closes: a page
// on an attacker-controlled name that resolves to loopback (DNS rebinding),
// and a cross-site form-style POST that needs no preflight.
func TestRebindingAndCSRFRejected(t *testing.T) {
	addr := newRawServer(t, Config{})

	t.Run("forged host is refused", func(t *testing.T) {
		// The socket is loopback; only the Host header lies. That is exactly
		// what a rebound name looks like on the wire.
		resp := rawRequest(t, addr, []string{
			"GET /api/board HTTP/1.1",
			"Host: kb.attacker.example",
		}, "")
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("rebound GET: got %d, want 403", resp.StatusCode)
		}
	})

	t.Run("forged host cannot read another user's board", func(t *testing.T) {
		resp := rawRequest(t, addr, []string{
			"GET /api/board HTTP/1.1",
			"Host: kb.attacker.example",
			"X-KB-User: default",
		}, "")
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("rebound cross-user GET: got %d, want 403", resp.StatusCode)
		}
	})

	t.Run("cross-site origin cannot write the board", func(t *testing.T) {
		resp := rawRequest(t, addr, []string{
			"PUT /api/board HTTP/1.1",
			"Host: " + addr,
			"Origin: https://evil.example.com",
			"Content-Type: text/markdown",
		}, "# wiped\n")
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("cross-origin PUT: got %d, want 403", resp.StatusCode)
		}
	})

	t.Run("cross-site fetch metadata cannot write the board", func(t *testing.T) {
		resp := rawRequest(t, addr, []string{
			"PUT /api/board HTTP/1.1",
			"Host: " + addr,
			"Sec-Fetch-Site: cross-site",
			"Content-Type: text/markdown",
		}, "# wiped\n")
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("cross-site PUT: got %d, want 403", resp.StatusCode)
		}
	})

	t.Run("text/plain POST cannot reach the AI proxy", func(t *testing.T) {
		// The CSRF primitive: text/plain is CORS-safelisted, so this request
		// crosses origins with no preflight and would otherwise decode as
		// JSON and spend the stored provider key.
		resp := rawRequest(t, addr, []string{
			"POST /api/ai/story HTTP/1.1",
			"Host: " + addr,
			"Content-Type: text/plain",
		}, `{"mode":"create","prompt":"attacker prompt"}`)
		if resp.StatusCode != http.StatusUnsupportedMediaType {
			t.Fatalf("text/plain POST: got %d, want 415", resp.StatusCode)
		}
	})

	t.Run("form POST cannot reach the AI proxy", func(t *testing.T) {
		resp := rawRequest(t, addr, []string{
			"POST /api/ai/story HTTP/1.1",
			"Host: " + addr,
			"Content-Type: application/x-www-form-urlencoded",
		}, `{"mode":"create","prompt":"p"}`)
		if resp.StatusCode != http.StatusUnsupportedMediaType {
			t.Fatalf("form POST: got %d, want 415", resp.StatusCode)
		}
	})

	t.Run("json POST cannot claim to be markdown", func(t *testing.T) {
		resp := rawRequest(t, addr, []string{
			"PUT /api/settings HTTP/1.1",
			"Host: " + addr,
			"Content-Type: text/markdown",
		}, `{"ai_model":"m"}`)
		if resp.StatusCode != http.StatusUnsupportedMediaType {
			t.Fatalf("markdown settings PUT: got %d, want 415", resp.StatusCode)
		}
	})

	t.Run("same-origin request still works", func(t *testing.T) {
		resp := rawRequest(t, addr, []string{
			"PUT /api/board HTTP/1.1",
			"Host: " + addr,
			"Origin: http://" + addr,
			"Sec-Fetch-Site: same-origin",
			"Content-Type: text/markdown; charset=utf-8",
		}, "# mine\n")
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("same-origin PUT: got %d, want 204", resp.StatusCode)
		}
		resp = rawRequest(t, addr, []string{
			"GET /api/board HTTP/1.1",
			"Host: " + addr,
		}, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("same-origin GET: got %d, want 200", resp.StatusCode)
		}
	})
}

// KB_ALLOWED_HOSTS is the documented escape hatch for serving kb on a real
// hostname; without it the same request is refused.
func TestAllowedHostsOptIn(t *testing.T) {
	addr := newRawServer(t, Config{AllowedHosts: " kb.example.com , other.example.net "})

	for _, host := range []string{"kb.example.com", "KB.Example.com", "kb.example.com:9000", "other.example.net"} {
		resp := rawRequest(t, addr, []string{"GET /api/board HTTP/1.1", "Host: " + host}, "")
		if resp.StatusCode != http.StatusOK {
			t.Errorf("allowlisted Host %q: got %d, want 200", host, resp.StatusCode)
		}
	}
	resp := rawRequest(t, addr, []string{"GET /api/board HTTP/1.1", "Host: kb.attacker.example"}, "")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("unlisted Host: got %d, want 403", resp.StatusCode)
	}
	if body := readBodyString(t, resp); !strings.Contains(body, "KB_ALLOWED_HOSTS") {
		t.Errorf("rejection body = %q, want it to name KB_ALLOWED_HOSTS", body)
	}
}

// Token and Entra requests carry a bearer credential a cross-origin page
// cannot mint, so Host is not pinned there by default: a deployment on a real
// hostname must not 403 on every request until an undocumented env var is
// found. The Origin and content-type checks still apply.
func TestAuthenticatedModesDoNotPinHost(t *testing.T) {
	addr := newRawServer(t, Config{Token: "s3cret"})

	resp := rawRequest(t, addr, []string{
		"GET /api/board HTTP/1.1",
		"Host: kb.example.com",
		"Authorization: Bearer s3cret",
		"X-KB-User: default",
	}, "")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("token mode with a real hostname: got %d, want 200", resp.StatusCode)
	}
	resp = rawRequest(t, addr, []string{
		"PUT /api/board HTTP/1.1",
		"Host: kb.example.com",
		"Origin: https://evil.example.com",
		"Authorization: Bearer s3cret",
		"X-KB-User: default",
		"Content-Type: text/markdown",
	}, "# wiped\n")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("token mode cross-origin PUT: got %d, want 403", resp.StatusCode)
	}
}

func TestHostAllowed(t *testing.T) {
	s := &server{allowedHosts: parseAllowedHosts("kb.example.com")}
	tests := []struct {
		host string
		want bool
	}{
		{"127.0.0.1:8080", true},
		{"127.0.0.1", true},
		{"127.7.7.7:8080", true},
		{"[::1]:8080", true},
		{"localhost", true},
		{"localhost:8080", true},
		{"kb.example.com", true},
		{"kb.example.com:443", true},
		{"", false},
		{"127.0.0.1.attacker.example", false},
		{"attacker.example:8080", false},
		{"192.168.1.5:8080", false},
		{"evil.localhost", false},
	}
	for _, tt := range tests {
		if got := s.hostAllowed(tt.host); got != tt.want {
			t.Errorf("hostAllowed(%q) = %v, want %v", tt.host, got, tt.want)
		}
	}
}

func TestSameOriginRequest(t *testing.T) {
	tests := []struct {
		name    string
		headers map[string]string
		want    bool
	}{
		{"no browser headers (CLI)", nil, true},
		{"same-origin fetch metadata", map[string]string{"Sec-Fetch-Site": "same-origin"}, true},
		{"navigation", map[string]string{"Sec-Fetch-Site": "none"}, true},
		{"cross-site", map[string]string{"Sec-Fetch-Site": "cross-site"}, false},
		// A sibling subdomain is same-site but not same-origin.
		{"same-site", map[string]string{"Sec-Fetch-Site": "same-site"}, false},
		{"matching origin", map[string]string{"Origin": "http://127.0.0.1:8080"}, true},
		{"other origin", map[string]string{"Origin": "https://evil.example.com"}, false},
		{"null origin", map[string]string{"Origin": "null"}, false},
		// Fetch metadata wins over a spoofable Origin.
		{"cross-site beats matching origin", map[string]string{
			"Sec-Fetch-Site": "cross-site", "Origin": "http://127.0.0.1:8080",
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("PUT", "/api/board", strings.NewReader("# b\n"))
			r.Host = "127.0.0.1:8080"
			for k, v := range tt.headers {
				r.Header.Set(k, v)
			}
			if got := sameOriginRequest(r); got != tt.want {
				t.Errorf("sameOriginRequest() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFrameGuardsOnStatic(t *testing.T) {
	h, _ := newTestServer(t, Config{})

	for _, path := range []string{"/", "/some/client/route"} {
		w := doReq(t, h, "GET", path, "", nil)
		if got := w.Header().Get("X-Frame-Options"); got != "DENY" {
			t.Errorf("GET %s X-Frame-Options = %q, want DENY", path, got)
		}
		if got := w.Header().Get("Content-Security-Policy"); !strings.Contains(got, "frame-ancestors 'none'") {
			t.Errorf("GET %s CSP = %q, want frame-ancestors 'none'", path, got)
		}
	}
}

// The CSP is the enforced half of kb's zero-telemetry promise: with it the
// SPA cannot open a connection off-origin even if a dependency tries. The
// only cross-origin host that may ever appear is the Entra one, and only
// when Azure sign-in is configured.
func TestContentSecurityPolicyOnStatic(t *testing.T) {
	// style-src carries 'unsafe-inline' deliberately, and for two independent
	// reasons: the app's own React style={{…}} attributes (drag clone
	// position, progress bar widths, tag colours) are style-src-attr and fall
	// back to style-src, and emoji-mart styles its shadow-DOM picker with a
	// <style> element it builds at runtime. Verified in Chrome — under a bare
	// style-src 'self' the picker reports a style-src-elem violation and
	// renders unstyled. Removing emoji-mart would not make this droppable.
	// Vite's own output is a linked stylesheet.
	common := []string{
		"default-src 'self'",
		"script-src 'self'",
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data:",
		"font-src 'self'",
		"object-src 'none'",
		"base-uri 'none'",
		"form-action 'none'",
		"frame-ancestors 'none'",
	}
	tests := []struct {
		name    string
		cfg     Config
		want    []string
		notWant []string
	}{
		{
			name: "open mode reaches nothing off-origin",
			cfg:  Config{},
			want: append([]string{"connect-src 'self'", "frame-src 'self'"}, common...),
			// Not merely absent from connect-src: absent from the whole policy.
			notWant: []string{entraOrigin},
		},
		{
			name: "entra mode adds only the sign-in origin",
			cfg:  Config{TenantID: "tenant", ClientID: "client"},
			want: append([]string{
				"connect-src 'self' " + entraOrigin,
				"frame-src 'self' " + entraOrigin,
			}, common...),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, _ := newTestServer(t, tt.cfg)
			// index.html and a client-side route both carry the policy.
			for _, path := range []string{"/", "/some/client/route"} {
				got := doReq(t, h, "GET", path, "", nil).Header().Get("Content-Security-Policy")
				for _, want := range tt.want {
					if !strings.Contains(got, want) {
						t.Errorf("GET %s CSP = %q, missing %q", path, got, want)
					}
				}
				for _, bad := range tt.notWant {
					if strings.Contains(got, bad) {
						t.Errorf("GET %s CSP = %q, must not mention %q", path, got, bad)
					}
				}
			}
		})
	}
}

// WebRTC is a real hole in this policy — connect-src governs no
// RTCPeerConnection in any browser, and a STUN hostname is not an http(s) URL
// either — but `webrtc 'block'` is not the fix. Measured against Chrome 151:
// a peer connection constructs and opens a data channel under it, and the
// only console output on an otherwise clean load is "Unrecognized
// Content-Security-Policy directive 'webrtc'". Shipping it would trade a
// clean console for nothing, so the guard lives in src/egress.test.ts, which
// fails the build if RTCPeerConnection appears in src/ at all.
//
// Re-measure before adding it back; this test is here so that is a decision
// rather than an accident.
func TestContentSecurityPolicyOmitsUnimplementedDirectives(t *testing.T) {
	for _, cfg := range []Config{{}, {TenantID: "tenant", ClientID: "client"}} {
		h, _ := newTestServer(t, cfg)
		got := doReq(t, h, "GET", "/", "", nil).Header().Get("Content-Security-Policy")
		if strings.Contains(got, "webrtc") {
			t.Errorf("CSP = %q, must not send the unimplemented webrtc directive", got)
		}
	}
}
