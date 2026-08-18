package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/forge"
	"github.com/RandomCodeSpace/kb/internal/store"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func closeForgeResponse(t *testing.T, response *http.Response) {
	t.Helper()
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		t.Fatalf("drain forge response: %v", err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatalf("close forge response: %v", err)
	}
}

// The forge client captures its private-host policy once while preserving the AI client's timeout contract.
func TestForgeClientCapturesAllowlistAndKeepsTimeouts(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	t.Setenv("KB_FORGE_ALLOW_PRIVATE", "")
	blocked := newForgeClient()
	t.Cleanup(blocked.CloseIdleConnections)
	if blocked.Timeout != 20*time.Second {
		t.Fatalf("forge timeout = %v, want 20s", blocked.Timeout)
	}
	ai := newAIClient()
	t.Cleanup(ai.CloseIdleConnections)
	if ai.Timeout != AITimeout {
		t.Fatalf("AI timeout = %v, want %v", ai.Timeout, AITimeout)
	}

	// Changing the environment after construction must not relax an existing client.
	t.Setenv("KB_FORGE_ALLOW_PRIVATE", "127.0.0.1")
	response, err := blocked.Get(upstream.URL)
	if response != nil {
		_ = response.Body.Close()
	}
	if err == nil {
		t.Fatal("forge client constructed without an allowlist reached loopback")
	}
	if hits.Load() != 0 {
		t.Fatalf("blocked loopback was hit %d times", hits.Load())
	}

	allowed := newForgeClient()
	t.Cleanup(allowed.CloseIdleConnections)
	t.Setenv("KB_FORGE_ALLOW_PRIVATE", "")
	response, err = allowed.Get(upstream.URL)
	if err != nil {
		t.Fatalf("exact hostname allowlist request failed: %v", err)
	}
	closeForgeResponse(t, response)
	if hits.Load() != 1 {
		t.Fatalf("allowlisted loopback hits = %d, want 1", hits.Load())
	}
}

// Both documented allow-all values permit a loopback forge endpoint.
func TestForgeClientAllowAllValuesPermitLoopback(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	for _, test := range []struct{ name, value string }{{"one", "1"}, {"star", "*"}} {
		t.Run(test.name, func(t *testing.T) {
			hits.Store(0)
			t.Setenv("KB_FORGE_ALLOW_PRIVATE", test.value)
			client := newForgeClient()
			t.Cleanup(client.CloseIdleConnections)
			response, err := client.Get(upstream.URL)
			if err != nil {
				t.Fatalf("allow-all value %q failed: %v", test.value, err)
			}
			closeForgeResponse(t, response)
			if hits.Load() != 1 {
				t.Fatalf("allow-all value %q produced %d hits", test.value, hits.Load())
			}
		})
	}
}

// Forge redirects may change paths on one host but may never cross to another origin.
func TestForgeClientRedirectsStayOnTheConfiguredHost(t *testing.T) {
	var relativeHits, targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetHits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start":
			http.Redirect(w, r, "/final", http.StatusFound)
		case "/final":
			relativeHits.Add(1)
			w.WriteHeader(http.StatusNoContent)
		case "/cross":
			http.Redirect(w, r, target.URL+"/landing", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer source.Close()

	t.Setenv("KB_FORGE_ALLOW_PRIVATE", "127.0.0.1")
	client := newForgeClient()
	t.Cleanup(client.CloseIdleConnections)
	response, err := client.Get(source.URL + "/start")
	if err != nil {
		t.Fatalf("same-host relative redirect failed: %v", err)
	}
	closeForgeResponse(t, response)
	if relativeHits.Load() != 1 {
		t.Fatalf("same-host redirect final hits = %d, want 1", relativeHits.Load())
	}

	response, err = client.Get(source.URL + "/cross")
	if response != nil {
		_ = response.Body.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "refusing cross-host redirect") {
		t.Fatal("cross-host redirect did not return the refusal error")
	}
	if targetHits.Load() != 0 {
		t.Fatalf("cross-host redirect target was hit %d times", targetHits.Load())
	}
}

// The resolved-address predicate accepts public IPs and rejects every non-public address class.
func TestResolvedAddressPredicateRejectsNonPublicRanges(t *testing.T) {
	tests := []struct {
		name, address string
		wantErr       bool
	}{
		{"public IPv4", "8.8.8.8:443", false},
		{"public IPv6", "[2606:4700:4700::1111]:443", false},
		{"loopback IPv4", "127.0.0.1:443", true},
		{"loopback IPv6", "[::1]:443", true},
		{"private 10", "10.0.0.1:443", true},
		{"private 172", "172.16.0.1:443", true},
		{"private 192", "192.168.1.1:443", true},
		{"private IPv6", "[fd00::1]:443", true},
		{"link-local IPv4", "169.254.1.1:443", true},
		{"link-local IPv6", "[fe80::1]:443", true},
		{"link-local multicast IPv4", "224.0.0.1:443", true},
		{"link-local multicast IPv6", "[ff02::1]:443", true},
		{"unspecified IPv4", "0.0.0.0:443", true},
		{"unspecified IPv6", "[::]:443", true},
		{"invalid address", "not-an-address", true},
		{"unresolved hostname", "example.com:443", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := rejectPrivateAddress("tcp", test.address, nil)
			if (err != nil) != test.wantErr {
				t.Fatalf("rejectPrivateAddress(%q) error = %v, wantErr %v", test.address, err, test.wantErr)
			}
		})
	}
}

type integrationsListResponse struct {
	Sources []struct {
		Name     string `json:"name"`
		Kind     string `json:"kind"`
		BaseURL  string `json:"base_url"`
		HasToken bool   `json:"has_token"`
	} `json:"sources"`
}

type integrationTestResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

func decodeForgeJSON(t *testing.T, response *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if contentType := response.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json (body bytes=%d)", contentType, response.Body.Len())
	}
	if err := json.Unmarshal(response.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode JSON response: %v (body bytes=%d)", err, response.Body.Len())
	}
}

func newIntegrationsHandler(t *testing.T) (http.Handler, *store.Store) {
	t.Helper()
	st := newTestStore(t)
	s := newServer(Config{}, testStatic, st)
	if s.forgeClient == nil {
		t.Fatal("newServer did not construct a forge client")
	}
	t.Cleanup(s.forgeClient.CloseIdleConnections)
	return s.handler(), st
}

func TestIntegrationsCRUDAndPATRedaction(t *testing.T) {
	t.Setenv("KB_FORGE_ALLOW_PRIVATE", "")
	h, st := newIntegrationsHandler(t)

	w := doReq(t, h, http.MethodPut, "/api/integrations/primary",
		`{"kind":"gitlab","base_url":"gitlab.example.com","pat":"stored-token"}`, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("create integration: got %d, want 204 (body bytes=%d)", w.Code, w.Body.Len())
	}

	w = doReq(t, h, http.MethodGet, "/api/integrations", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list integrations: got %d, want 200 (body bytes=%d)", w.Code, w.Body.Len())
	}
	if body := strings.ToLower(w.Body.String()); strings.Contains(body, "pat") {
		t.Fatal("list response contains forbidden PAT field substring")
	}
	var rawList struct {
		Sources []map[string]any `json:"sources"`
	}
	decodeForgeJSON(t, w, &rawList)
	if len(rawList.Sources) != 1 {
		t.Fatalf("listed sources = %d, want 1", len(rawList.Sources))
	}
	source := rawList.Sources[0]
	if len(source) != 4 {
		t.Fatalf("source fields = %v, want exactly name/kind/base_url/has_token", source)
	}
	for _, field := range []string{"name", "kind", "base_url", "has_token"} {
		if _, ok := source[field]; !ok {
			t.Fatalf("source is missing %q: %v", field, source)
		}
	}
	if source["name"] != "primary" || source["kind"] != "gitlab" ||
		source["base_url"] != "https://gitlab.example.com" || source["has_token"] != true {
		t.Fatalf("source = %v, want normalized primary GitLab source with has_token=true", source)
	}

	// Omitted pointer fields preserve both the endpoint and token.
	w = doReq(t, h, http.MethodPut, "/api/integrations/primary", `{"kind":"gitlab"}`, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("patch with omitted fields: got %d, want 204 (body bytes=%d)", w.Code, w.Body.Len())
	}
	kind, baseURL, pat, err := st.ForgePAT("default", "primary")
	if err != nil {
		t.Fatalf("ForgePAT after keep patch: %v", err)
	}
	if kind != "gitlab" || baseURL != "https://gitlab.example.com" || pat != "stored-token" {
		t.Fatalf("omitted-field patch did not preserve source values (kind=%q base=%q PAT match=%v)", kind, baseURL, pat == "stored-token")
	}

	// Moving origins without a PAT atomically clears the old credential and
	// tells the caller to request it again.
	w = doReq(t, h, http.MethodPut, "/api/integrations/primary",
		`{"kind":"gitlab","base_url":"https://other.example.com"}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("origin-change patch: got %d, want 200 (body bytes=%d)", w.Code, w.Body.Len())
	}
	var cleared map[string]bool
	decodeForgeJSON(t, w, &cleared)
	if len(cleared) != 1 || !cleared["token_cleared"] {
		t.Fatalf("origin-change response = %v, want only token_cleared=true", cleared)
	}
	_, baseURL, pat, err = st.ForgePAT("default", "primary")
	if err != nil {
		t.Fatalf("ForgePAT after origin change: %v", err)
	}
	if baseURL != "https://other.example.com" || pat != "" {
		t.Fatalf("origin-change source base = %q, PAT empty = %v; want new base and empty PAT", baseURL, pat == "")
	}

	// A supplied PAT updates, while an explicit empty PAT clears.
	w = doReq(t, h, http.MethodPut, "/api/integrations/primary",
		`{"kind":"gitlab","pat":"replacement-token"}`, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("PAT patch: got %d, want 204 (body bytes=%d)", w.Code, w.Body.Len())
	}
	_, _, pat, err = st.ForgePAT("default", "primary")
	if err != nil || pat != "replacement-token" {
		t.Fatalf("replacement PAT match = %v, error = %v; want true and nil", pat == "replacement-token", err)
	}
	w = doReq(t, h, http.MethodPut, "/api/integrations/primary",
		`{"kind":"gitlab","pat":""}`, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("PAT clear: got %d, want 204 (body bytes=%d)", w.Code, w.Body.Len())
	}
	_, _, pat, err = st.ForgePAT("default", "primary")
	if err != nil || pat != "" {
		t.Fatalf("PAT empty after explicit clear = %v, error = %v; want true and nil", pat == "", err)
	}

	w = doReq(t, h, http.MethodPut, "/api/integrations/bad%20name",
		`{"kind":"gitlab","base_url":"https://gitlab.example.com"}`, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid source name: got %d, want 400 (body bytes=%d)", w.Code, w.Body.Len())
	}

	w = doReq(t, h, http.MethodDelete, "/api/integrations/primary", "", nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete integration: got %d, want 204 (body bytes=%d)", w.Code, w.Body.Len())
	}
	w = doReq(t, h, http.MethodGet, "/api/integrations", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list after delete: got %d, want 200 (body bytes=%d)", w.Code, w.Body.Len())
	}
	var empty integrationsListResponse
	decodeForgeJSON(t, w, &empty)
	if len(empty.Sources) != 0 {
		t.Fatalf("sources after delete = %+v, want empty", empty.Sources)
	}
}

func TestIntegrationsRejectBaseURLQueryAndFragment(t *testing.T) {
	h, st := newIntegrationsHandler(t)
	for _, baseURL := range []string{
		"https://gitlab.example.com?x=y",
		"https://gitlab.example.com#fragment",
	} {
		body, err := json.Marshal(map[string]string{"kind": "gitlab", "base_url": baseURL})
		if err != nil {
			t.Fatal("marshal integration request")
		}
		w := doReq(t, h, http.MethodPut, "/api/integrations/primary", string(body), nil)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("PUT integration with URL component: got %d, want 400", w.Code)
		}
		if strings.Contains(w.Body.String(), baseURL) {
			t.Fatal("integration validation response echoed the URL")
		}
	}
	if sources, err := st.ForgeSources("default"); err != nil || len(sources) != 0 {
		t.Fatalf("rejected integrations persisted sources=%d err=%v", len(sources), err)
	}
}

func TestIntegrationsAreScopedByAuthenticatedUser(t *testing.T) {
	t.Setenv("KB_FORGE_ALLOW_PRIVATE", "")
	h, _ := newIntegrationsHandler(t)
	alice := map[string]string{"X-KB-User": "Alice"}

	w := doReq(t, h, http.MethodPut, "/api/integrations/team",
		`{"kind":"github","base_url":"https://github.example.com"}`, alice)
	if w.Code != http.StatusNoContent {
		t.Fatalf("Alice create: got %d, want 204 (body bytes=%d)", w.Code, w.Body.Len())
	}

	w = doReq(t, h, http.MethodGet, "/api/integrations", "", nil)
	var defaults integrationsListResponse
	decodeForgeJSON(t, w, &defaults)
	if w.Code != http.StatusOK || len(defaults.Sources) != 0 {
		t.Fatalf("default list: got %d sources=%+v, want 200 and empty", w.Code, defaults.Sources)
	}

	// A same-name delete in another scope must not affect Alice.
	w = doReq(t, h, http.MethodDelete, "/api/integrations/team", "", nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("default delete: got %d, want 204 (body bytes=%d)", w.Code, w.Body.Len())
	}
	w = doReq(t, h, http.MethodGet, "/api/integrations", "", alice)
	var aliceList integrationsListResponse
	decodeForgeJSON(t, w, &aliceList)
	if w.Code != http.StatusOK || len(aliceList.Sources) != 1 || aliceList.Sources[0].Name != "team" {
		t.Fatalf("Alice list after default delete: got %d sources=%+v, want team", w.Code, aliceList.Sources)
	}
}

func TestIntegrationsConnectionTestProtectsStoredPAT(t *testing.T) {
	var targetHits atomic.Int32
	stored := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer stored.Close()
	different := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer different.Close()

	t.Setenv("KB_FORGE_ALLOW_PRIVATE", "127.0.0.1")
	h, st := newIntegrationsHandler(t)
	baseURL, pat := stored.URL, "stored-token"
	if _, err := st.SetForgeSource("default", "protected", "gitlab", &baseURL, &pat); err != nil {
		t.Fatalf("seed protected source: %v", err)
	}

	body, err := json.Marshal(map[string]string{"base_url": different.URL})
	if err != nil {
		t.Fatalf("marshal probe: %v", err)
	}
	w := doReq(t, h, http.MethodPost, "/api/integrations/protected/test", string(body), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("different-origin test: got %d, want 200 (body bytes=%d)", w.Code, w.Body.Len())
	}
	var result integrationTestResponse
	decodeForgeJSON(t, w, &result)
	if result.OK || result.Error != "enter the token to test a different endpoint" {
		t.Fatalf("different-origin result = %+v, want exact credential-boundary error", result)
	}
	if targetHits.Load() != 0 {
		t.Fatalf("different-origin target was hit %d times without a supplied PAT", targetHits.Load())
	}
	if strings.Contains(w.Body.String(), pat) {
		t.Fatal("test response exposed stored PAT")
	}
}

func TestIntegrationsConnectionTestUsesOnlySuppliedPATForDifferentOrigin(t *testing.T) {
	var storedHits atomic.Int32
	stored := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		storedHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer stored.Close()

	const replacementPAT = "replacement-token"
	requests := make(chan []string, 1)
	different := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Header.Values("PRIVATE-TOKEN")
		w.WriteHeader(http.StatusOK)
	}))
	defer different.Close()

	t.Setenv("KB_FORGE_ALLOW_PRIVATE", "127.0.0.1")
	h, st := newIntegrationsHandler(t)
	baseURL, storedPAT := stored.URL, "stored-token"
	if _, err := st.SetForgeSource("default", "protected", "gitlab", &baseURL, &storedPAT); err != nil {
		t.Fatalf("seed protected source: %v", err)
	}
	body, err := json.Marshal(map[string]string{"base_url": different.URL, "pat": replacementPAT})
	if err != nil {
		t.Fatalf("marshal probe: %v", err)
	}

	w := doReq(t, h, http.MethodPost, "/api/integrations/protected/test", string(body), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("different-origin test: got %d, want 200 (body bytes=%d)", w.Code, w.Body.Len())
	}
	var result integrationTestResponse
	decodeForgeJSON(t, w, &result)
	if !result.OK || result.Error != "" {
		t.Fatalf("different-origin result = %+v, want ok=true without error", result)
	}
	if storedHits.Load() != 0 {
		t.Fatalf("stored origin was hit %d times", storedHits.Load())
	}
	select {
	case tokens := <-requests:
		if len(tokens) != 1 || tokens[0] != replacementPAT {
			t.Fatalf("PRIVATE-TOKEN values = %q, want only supplied replacement PAT", tokens)
		}
	default:
		t.Fatal("different origin did not receive the connection-test request")
	}
}

func TestIntegrationProbeRejectsCredentialURLWithoutEgress(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	t.Setenv("KB_FORGE_ALLOW_PRIVATE", "127.0.0.1")
	h, st := newIntegrationsHandler(t)
	baseURL := upstream.URL
	if _, err := st.SetForgeSource("default", "legacy", "gitlab", &baseURL, nil); err != nil {
		t.Fatal("seed legacy integration")
	}
	legacyBaseURL := upstream.URL + "?x=y"
	body, err := json.Marshal(map[string]string{"base_url": legacyBaseURL})
	if err != nil {
		t.Fatal("marshal legacy URL probe")
	}

	w := doReq(t, h, http.MethodPost, "/api/integrations/legacy/test", string(body), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("legacy URL probe: got %d, want 200", w.Code)
	}
	var result integrationTestResponse
	decodeForgeJSON(t, w, &result)
	if result.OK || result.Error != "forge base URL must not contain query or fragment" {
		t.Fatal("legacy URL probe returned the wrong validation result")
	}
	if strings.Contains(w.Body.String(), legacyBaseURL) {
		t.Fatal("legacy URL probe echoed the URL")
	}
	if hits.Load() != 0 {
		t.Fatal("legacy URL probe reached the upstream")
	}
}

func TestIntegrationsConnectionTestSendsHeaderOnlyCredentials(t *testing.T) {
	tests := []struct {
		name, source, kind, basePath, pat, wantPath string
	}{
		{
			name:     "GitLab",
			source:   "gitlab-main",
			kind:     "gitlab",
			basePath: "/forge",
			pat:      "gitlab-test-token",
			wantPath: "/forge/api/v4/version",
		},
		{
			name:     "GitHub Enterprise",
			source:   "github-main",
			kind:     "github",
			basePath: "/enterprise",
			pat:      "github-test-token",
			wantPath: "/enterprise/api/v3/user",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			type requestRecord struct {
				method, path, rawQuery, privateToken, authorization, accept string
			}
			requests := make(chan requestRecord, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests <- requestRecord{
					method:        r.Method,
					path:          r.URL.Path,
					rawQuery:      r.URL.RawQuery,
					privateToken:  r.Header.Get("PRIVATE-TOKEN"),
					authorization: r.Header.Get("Authorization"),
					accept:        r.Header.Get("Accept"),
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{}`)
			}))
			defer upstream.Close()

			t.Setenv("KB_FORGE_ALLOW_PRIVATE", "127.0.0.1")
			h, st := newIntegrationsHandler(t)
			baseURL := upstream.URL + test.basePath
			if _, err := st.SetForgeSource("default", test.source, test.kind, &baseURL, &test.pat); err != nil {
				t.Fatalf("seed %s source: %v", test.kind, err)
			}

			w := doReq(t, h, http.MethodPost, "/api/integrations/"+test.source+"/test", "", nil)
			if w.Code != http.StatusOK {
				t.Fatalf("connection test: got %d, want 200 (body bytes=%d)", w.Code, w.Body.Len())
			}
			var result integrationTestResponse
			decodeForgeJSON(t, w, &result)
			if !result.OK || result.Error != "" {
				t.Fatalf("connection result = %+v, want ok=true without error", result)
			}
			if strings.Contains(w.Body.String(), test.pat) {
				t.Fatal("connection response exposed PAT")
			}

			select {
			case request := <-requests:
				if request.method != http.MethodGet || request.path != test.wantPath {
					t.Fatalf("upstream request = %s %s, want GET %s", request.method, request.path, test.wantPath)
				}
				if request.rawQuery != "" {
					t.Fatalf("upstream RawQuery = %q, want empty", request.rawQuery)
				}
				switch test.kind {
				case "gitlab":
					if request.privateToken != test.pat || request.authorization != "" {
						t.Fatal("GitLab request did not carry only the expected PRIVATE-TOKEN value")
					}
				case "github":
					if request.authorization != "Bearer "+test.pat || request.privateToken != "" {
						t.Fatal("GitHub request did not carry only the expected Bearer token")
					}
					if request.accept != "application/vnd.github+json" {
						t.Fatalf("GitHub Accept = %q, want application/vnd.github+json", request.accept)
					}
				}
			default:
				t.Fatal("forge endpoint did not receive the connection-test request")
			}
		})
	}
}

func TestIntegrationsConnectionTestCollapsesUpstreamFailures(t *testing.T) {
	statusFailure := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "upstream-internal-detail", http.StatusServiceUnavailable)
	}))
	defer statusFailure.Close()

	networkFailure := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	networkFailureURL := networkFailure.URL
	networkFailure.Close()

	tests := []struct {
		name, baseURL, forbidden string
	}{
		{"status", statusFailure.URL, "upstream-internal-detail"},
		{"network", networkFailureURL, "connection refused"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("KB_FORGE_ALLOW_PRIVATE", "127.0.0.1")
			h, st := newIntegrationsHandler(t)
			pat := "failure-test-token"
			if _, err := st.SetForgeSource("default", "failing", "gitlab", &test.baseURL, &pat); err != nil {
				t.Fatalf("seed failing source: %v", err)
			}

			w := doReq(t, h, http.MethodPost, "/api/integrations/failing/test", "", nil)
			if w.Code != http.StatusOK {
				t.Fatalf("failed connection test: got %d, want 200 (body bytes=%d)", w.Code, w.Body.Len())
			}
			var result integrationTestResponse
			decodeForgeJSON(t, w, &result)
			if result.OK || result.Error != "connection failed" {
				t.Fatalf("failed connection result = %+v, want exact opaque error", result)
			}
			for _, forbidden := range []struct {
				label, value string
			}{
				{"upstream detail", test.forbidden},
				{"PAT", pat},
				{"base URL", test.baseURL},
				{"upstream status", "503"},
			} {
				if strings.Contains(w.Body.String(), forbidden.value) {
					t.Fatalf("failed connection response exposed %s", forbidden.label)
				}
			}
		})
	}
}

// Configured references select the most specific forge endpoint and preserve the
// project identity required by the later fetch phase.

func TestImportPreviewTransformsConfiguredForgeIssues(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/forge/api/v4/projects/group%2Fproject/issues" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `[
			{"iid":42,"title":"Linked issue","description":"body","web_url":"https://forge.example/issues/42","labels":["team::auth"]},
			{"iid":43,"title":"Similar issue title","description":"body","web_url":"https://forge.example/issues/43","labels":["team::auth"]}
		]`)
	}))
	defer upstream.Close()
	// The cards arrive through propose_card, each carrying the Source number of
	// the issue it came from; the closing prose is the runner's and is dropped.
	fakeAI := &scriptedOpenAI{replies: []fakeReply{
		{toolCalls: []fakeToolCall{
			{name: "propose_card", args: `{"title":"do linked","tags":["team::auth","link::evil#1"],"source":1}`},
			{name: "propose_card", args: `{"title":"do similar","source":2}`},
			{name: "propose_card", args: `{"title":"unlinked","tags":["link::evil#99","team::ops"],"source":99}`},
		}},
		{content: "Proposed three cards."},
	}}
	aiUpstream := httptest.NewServer(fakeAI.handler())
	defer aiUpstream.Close()

	t.Setenv("KB_FORGE_ALLOW_PRIVATE", "127.0.0.1")
	t.Setenv("KB_AI_ALLOW_PRIVATE", "1")
	h, st := newTestServer(t, Config{})
	configureAI(t, h, aiUpstream.URL, "test-model", "sk-import")
	baseURL, pat := upstream.URL+"/forge", "glpat-import"
	if _, err := st.SetForgeSource("default", "gitlab-main", "gitlab", &baseURL, &pat); err != nil {
		t.Fatalf("seed forge source: %v", err)
	}
	qualified42 := "gitlab:gitlab-main@" + baseURL + "/group/project#42"
	linked, err := st.AddTask("default", board.Task{Title: "Existing linked", Tags: []string{"link::gitlab#42", "import::" + qualified42}})
	if err != nil {
		t.Fatalf("seed linked task: %v", err)
	}
	similar, err := st.AddTask("default", board.Task{Title: "Similar issue title"})
	if err != nil {
		t.Fatalf("seed similar task: %v", err)
	}

	w := doReq(t, h, http.MethodPost, "/api/import/preview", fmt.Sprintf(`{"source":"gitlab-main","ref":%q,"max":99}`, upstream.URL+"/forge/group/project"), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("POST import preview: got %d body=%s", w.Code, w.Body)
	}
	var response struct {
		Kind      string `json:"kind"`
		TotalHint int    `json:"total_hint"`
		Fetched   int    `json:"fetched"`
		Drafts    []struct {
			Title       string   `json:"title"`
			Tags        []string `json:"tags"`
			Link        string   `json:"link"`
			ExternalKey string   `json:"external_key"`
			URL         string   `json:"url"`
			DuplicateOf *struct {
				ID, Title, Via string
			} `json:"duplicate_of"`
		} `json:"drafts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if rounds := len(fakeAI.requests()); rounds != 2 || response.Kind != "project" || response.TotalHint != 2 || response.Fetched != 2 || len(response.Drafts) != 3 {
		t.Fatalf("preview metadata = %+v AI rounds=%d", response, len(fakeAI.requests()))
	}
	var raw map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw preview: %v", err)
	}
	for _, draft := range raw["drafts"].([]any) {
		if _, ok := draft.(map[string]any)["source"]; ok {
			t.Fatalf("preview draft exposes deprecated source field: %v", draft)
		}
	}
	// The runner's closing prose is not part of this endpoint's contract.
	if _, ok := raw["commentary"]; ok {
		t.Fatalf("preview response leaked the skill commentary: %v", raw)
	}
	first, second, unlinked := response.Drafts[0], response.Drafts[1], response.Drafts[2]
	if first.Link != "gitlab#42" || first.ExternalKey != qualified42 || first.URL == "" || first.DuplicateOf == nil || first.DuplicateOf.ID != linked.ID || first.DuplicateOf.Title != linked.Title || first.DuplicateOf.Via != "link" || strings.Join(first.Tags, ",") != "team::auth,link::gitlab#42,import::"+qualified42 {
		t.Fatalf("linked draft = %+v", first)
	}
	if second.Link != "gitlab#43" || second.DuplicateOf == nil || second.DuplicateOf.ID != similar.ID || second.DuplicateOf.Via != "similar" || !strings.Contains(strings.Join(second.Tags, ","), "link::gitlab#43") {
		t.Fatalf("similar draft = %+v", second)
	}
	if unlinked.Link != "" || unlinked.ExternalKey != "" || unlinked.URL != "" || unlinked.DuplicateOf != nil || strings.Join(unlinked.Tags, ",") != "team::ops" {
		t.Fatalf("unlinked draft = %+v", unlinked)
	}
}

// A run that spends every round on tool calls still returns the cards it did
// propose, and this endpoint drops the commentary that says so. Without the
// note the short draft list is a 200 that reads as a complete import.
func TestImportPreviewNotesATransformCutShort(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/forge/api/v4/projects/group%2Fproject/issues" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `[
			{"iid":42,"title":"First issue","description":"body","web_url":"https://forge.example/issues/42"},
			{"iid":43,"title":"Second issue","description":"body","web_url":"https://forge.example/issues/43"}
		]`)
	}))
	defer upstream.Close()
	// One card, then a model that never stops researching: the last scripted
	// reply repeats, so the loop runs out of rounds with work already done.
	fakeAI := &scriptedOpenAI{replies: []fakeReply{
		{toolCalls: []fakeToolCall{{name: "propose_card", args: `{"title":"Fix the first issue","source":1}`}}},
		{toolCalls: []fakeToolCall{{name: "find_similar", args: `{"query":"second issue"}`}}},
	}}
	aiUpstream := httptest.NewServer(fakeAI.handler())
	defer aiUpstream.Close()

	t.Setenv("KB_FORGE_ALLOW_PRIVATE", "127.0.0.1")
	t.Setenv("KB_AI_ALLOW_PRIVATE", "1")
	h, st := newTestServer(t, Config{})
	configureAI(t, h, aiUpstream.URL, "test-model", "sk-import")
	baseURL, pat := upstream.URL+"/forge", "glpat-import"
	if _, err := st.SetForgeSource("default", "gitlab-main", "gitlab", &baseURL, &pat); err != nil {
		t.Fatalf("seed forge source: %v", err)
	}

	w := doReq(t, h, http.MethodPost, "/api/import/preview", fmt.Sprintf(`{"source":"gitlab-main","ref":%q}`, upstream.URL+"/forge/group/project"), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("POST import preview: got %d body=%s", w.Code, w.Body)
	}
	var response importPreviewResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if rounds := len(fakeAI.requests()); rounds != skillMaxIterations {
		t.Fatalf("upstream rounds = %d, want the iteration cap %d", rounds, skillMaxIterations)
	}
	if len(response.Drafts) != 1 || response.Drafts[0].Title != "Fix the first issue" {
		t.Fatalf("drafts = %+v, want the one card the run managed", response.Drafts)
	}
	if response.Note != importPartialTransformNote {
		t.Fatalf("note = %q, want %q", response.Note, importPartialTransformNote)
	}
}

// A rate-limited fetch and a transform cut short are independent, so the
// second note joins the first rather than replacing it.

func TestImportPreviewLinksEachForgeIssueOnce(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/forge/api/v4/projects/group%2Fproject/issues" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `[
			{"iid":42,"title":"First issue","description":"body","web_url":"https://forge.example/issues/42"},
			{"iid":43,"title":"Second issue","description":"body","web_url":"https://forge.example/issues/43"}
		]`)
	}))
	defer upstream.Close()
	fakeAI := &scriptedOpenAI{replies: []fakeReply{
		{toolCalls: []fakeToolCall{
			{name: "propose_card", args: `{"title":"Split the second issue, part one","source":2}`},
			{name: "propose_card", args: `{"title":"Split the second issue, part two","source":2}`},
		}},
		{content: "Proposed two cards."},
	}}
	aiUpstream := httptest.NewServer(fakeAI.handler())
	defer aiUpstream.Close()

	t.Setenv("KB_FORGE_ALLOW_PRIVATE", "127.0.0.1")
	t.Setenv("KB_AI_ALLOW_PRIVATE", "1")
	h, st := newTestServer(t, Config{})
	configureAI(t, h, aiUpstream.URL, "test-model", "sk-import")
	baseURL, pat := upstream.URL+"/forge", "glpat-import"
	if _, err := st.SetForgeSource("default", "gitlab-main", "gitlab", &baseURL, &pat); err != nil {
		t.Fatalf("seed forge source: %v", err)
	}
	qualified43 := "gitlab:gitlab-main@" + baseURL + "/group/project#43"
	if _, err := st.AddTask("default", board.Task{Title: "Existing linked", Tags: []string{"link::gitlab#43", "import::" + qualified43}}); err != nil {
		t.Fatalf("seed linked task: %v", err)
	}

	w := doReq(t, h, http.MethodPost, "/api/import/preview", fmt.Sprintf(`{"source":"gitlab-main","ref":%q}`, upstream.URL+"/forge/group/project"), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("POST import preview: got %d body=%s", w.Code, w.Body)
	}
	var response importPreviewResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if len(response.Drafts) != 2 {
		t.Fatalf("drafts = %+v, want both proposals", response.Drafts)
	}
	first, second := response.Drafts[0], response.Drafts[1]
	if first.Link != "gitlab#43" || first.ExternalKey == "" || first.DuplicateOf == nil {
		t.Fatalf("first draft = %+v, want the issue provenance", first)
	}
	if second.Link != "" || second.ExternalKey != "" || second.URL != "" || second.DuplicateOf != nil {
		t.Fatalf("repeat draft = %+v, want no provenance", second)
	}
	if strings.Contains(strings.Join(second.Tags, ","), linkTagPrefix) {
		t.Fatalf("repeat draft carries a link tag: %+v", second.Tags)
	}
}

func TestImportPreviewReturnsEmptyDraftsWithoutAIEgressWhenForgeIsEmpty(t *testing.T) {
	var forgeCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/forge/api/v4/projects/group%2Fproject/issues" {
			http.NotFound(w, r)
			return
		}
		forgeCalls.Add(1)
		_, _ = io.WriteString(w, `[]`)
	}))
	defer upstream.Close()
	fakeAI := &scriptedOpenAI{replies: []fakeReply{{content: "unexpected"}}}
	aiUpstream := httptest.NewServer(fakeAI.handler())
	defer aiUpstream.Close()

	t.Setenv("KB_FORGE_ALLOW_PRIVATE", "127.0.0.1")
	t.Setenv("KB_AI_ALLOW_PRIVATE", "1")
	h, st := newTestServer(t, Config{})
	configureAI(t, h, aiUpstream.URL, "test-model", "sk-import")
	baseURL := upstream.URL + "/forge"
	if _, err := st.SetForgeSource("default", "gitlab-main", "gitlab", &baseURL, nil); err != nil {
		t.Fatalf("seed forge source: %v", err)
	}

	w := doReq(t, h, http.MethodPost, "/api/import/preview", fmt.Sprintf(`{"source":"gitlab-main","ref":%q}`, upstream.URL+"/forge/group/project"), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("empty preview = %d body=%q, want 200", w.Code, w.Body.String())
	}
	var response importPreviewResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode empty preview: %v", err)
	}
	if response.Drafts == nil || len(response.Drafts) != 0 {
		t.Fatalf("empty preview drafts = %#v, want initialized empty slice", response.Drafts)
	}
	if forgeCalls.Load() != 1 {
		t.Fatalf("empty preview made %d forge calls, want 1", forgeCalls.Load())
	}
	if rounds := len(fakeAI.requests()); rounds != 0 {
		t.Fatalf("empty preview made %d AI calls, want 0", rounds)
	}
}

// A card the collector refuses never becomes a draft: the model is told why and
// the run finishes with nothing, which must still be a 200 with no provenance
// rather than a proposal the wire contract cannot carry.
func TestImportPreviewReturnsEmptyDraftsWithoutProvenanceWhenAllGeneratedStoriesAreInvalid(t *testing.T) {
	var forgeCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/forge/api/v4/projects/group%2Fproject/issues" {
			http.NotFound(w, r)
			return
		}
		forgeCalls.Add(1)
		_, _ = io.WriteString(w, `[{"iid":42,"title":"Issue title","description":"body","web_url":"https://forge.example/issues/42"}]`)
	}))
	defer upstream.Close()
	fakeAI := &scriptedOpenAI{replies: []fakeReply{
		{toolCalls: []fakeToolCall{{name: "propose_card", args: `{"title":"   ","source":1}`}}},
		{toolCalls: []fakeToolCall{{name: "propose_card", args: `not an object`}}},
		{content: "Nothing here merits a card."},
	}}
	aiUpstream := httptest.NewServer(fakeAI.handler())
	defer aiUpstream.Close()

	t.Setenv("KB_FORGE_ALLOW_PRIVATE", "127.0.0.1")
	t.Setenv("KB_AI_ALLOW_PRIVATE", "1")
	h, st := newTestServer(t, Config{})
	configureAI(t, h, aiUpstream.URL, "test-model", "sk-import")
	baseURL, pat := upstream.URL+"/forge", "glpat-import"
	if _, err := st.SetForgeSource("default", "gitlab-main", "gitlab", &baseURL, &pat); err != nil {
		t.Fatalf("seed forge source: %v", err)
	}

	w := doReq(t, h, http.MethodPost, "/api/import/preview", fmt.Sprintf(`{"source":"gitlab-main","ref":%q}`, upstream.URL+"/forge/group/project"), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("all-invalid preview = %d body=%q, want 200", w.Code, w.Body.String())
	}
	var response importPreviewResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode all-invalid preview: %v", err)
	}
	if response.Drafts == nil || len(response.Drafts) != 0 {
		t.Fatalf("all-invalid preview drafts = %#v, want initialized empty slice", response.Drafts)
	}
	if response.Fetched != 1 {
		t.Fatalf("all-invalid preview fetched = %d, want 1", response.Fetched)
	}
	if forgeCalls.Load() != 1 {
		t.Fatalf("all-invalid preview made %d forge calls, want 1", forgeCalls.Load())
	}
	if rounds := len(fakeAI.requests()); rounds != 3 {
		t.Fatalf("all-invalid preview made %d AI rounds, want 3", rounds)
	}
	host := strings.TrimPrefix(upstream.URL, "http://")
	key := "gitlab:" + host + "/group/project#42"
	if found, err := st.ImportedAs("default", []string{key}); err != nil || len(found) != 0 {
		t.Fatalf("all-invalid preview persisted provenance: %+v, %v", found, err)
	}
}

// A selected source is an authorization boundary, not merely a hint for bare
// GitHub references. Absolute refs must not switch to another configured
// source before any credential lookup, forge request, or AI request.
func TestImportPreviewRejectsSelectedSourceMismatchBeforeEgress(t *testing.T) {
	var forgeAHits, forgeBHits atomic.Int32
	forgeA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		forgeAHits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer forgeA.Close()
	forgeB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		forgeBHits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer forgeB.Close()
	fakeAI := &scriptedOpenAI{replies: []fakeReply{{content: "unexpected"}}}
	aiUpstream := httptest.NewServer(fakeAI.handler())
	defer aiUpstream.Close()

	t.Setenv("KB_FORGE_ALLOW_PRIVATE", "127.0.0.1")
	t.Setenv("KB_AI_ALLOW_PRIVATE", "1")
	h, st := newTestServer(t, Config{})
	configureAI(t, h, aiUpstream.URL, "test-model", "sk-import")
	baseA, baseB := forgeA.URL, forgeB.URL
	if _, err := st.SetForgeSource("default", "source-a", "gitlab", &baseA, nil); err != nil {
		t.Fatalf("seed source A: %v", err)
	}
	if _, err := st.SetForgeSource("default", "source-b", "gitlab", &baseB, nil); err != nil {
		t.Fatalf("seed source B: %v", err)
	}

	for _, test := range []struct {
		name, source, wantBody string
	}{
		{"mismatched selected source", "SOURCE-A", "reference does not match selected source"},
		{"missing selected source", "", "configured source unavailable"},
		{"unknown selected source", "missing", "configured source unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := doReq(t, h, http.MethodPost, "/api/import/preview", fmt.Sprintf(`{"source":%q,"ref":%q}`, test.source, baseB+"/group/project"), nil)
			if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), test.wantBody) {
				t.Fatalf("POST import preview: got %d body=%q, want 400 containing %q", w.Code, w.Body.String(), test.wantBody)
			}
			if aiRounds := len(fakeAI.requests()); forgeAHits.Load() != 0 || forgeBHits.Load() != 0 || aiRounds != 0 {
				t.Fatalf("rejected preview made egress: forge A=%d forge B=%d AI=%d", forgeAHits.Load(), forgeBHits.Load(), aiRounds)
			}
			found, err := st.ImportedAs("default", []string{"unexpected"})
			if err != nil || len(found) != 0 {
				t.Fatalf("rejected preview recorded provenance: %+v, %v", found, err)
			}
		})
	}
}

// A model source number is authorized only when that complete numbered issue
// was included in the bounded prompt. An omitted oversized issue must remain
// a draft without server-owned provenance even if the model claims its index.
func TestImportPreviewDoesNotAuthorizeOmittedPackedSource(t *testing.T) {
	firstBody := strings.Repeat("a", maxImportIssueBodyBytes)
	secondTitle := strings.Repeat("z", maxImportPackBytes)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/forge/api/v4/projects/group%2Fproject/issues" {
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprintf(w, `[
			{"iid":1,"title":"first","description":%q,"web_url":"https://forge.example/issues/1"},
			{"iid":2,"title":%q,"description":"second body","web_url":"https://forge.example/issues/2"}
		]`, firstBody, secondTitle)
	}))
	defer upstream.Close()
	fakeAI := &scriptedOpenAI{replies: []fakeReply{
		{toolCalls: []fakeToolCall{
			{name: "propose_card", args: `{"title":"from first","source":1}`},
			{name: "propose_card", args: `{"title":"from second","source":2}`},
		}},
		{content: "Proposed two cards."},
	}}
	aiUpstream := httptest.NewServer(fakeAI.handler())
	defer aiUpstream.Close()

	t.Setenv("KB_FORGE_ALLOW_PRIVATE", "127.0.0.1")
	t.Setenv("KB_AI_ALLOW_PRIVATE", "1")
	h, st := newTestServer(t, Config{})
	configureAI(t, h, aiUpstream.URL, "test-model", "sk-import")
	baseURL, pat := upstream.URL+"/forge", "glpat-import"
	if _, err := st.SetForgeSource("default", "gitlab-main", "gitlab", &baseURL, &pat); err != nil {
		t.Fatalf("seed forge source: %v", err)
	}

	w := doReq(t, h, http.MethodPost, "/api/import/preview", fmt.Sprintf(`{"source":"gitlab-main","ref":%q}`, upstream.URL+"/forge/group/project"), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("POST import preview: got %d body=%s", w.Code, w.Body)
	}
	var response struct {
		Fetched int `json:"fetched"`
		Drafts  []struct {
			Title       string `json:"title"`
			Link        string `json:"link"`
			ExternalKey string `json:"external_key"`
			URL         string `json:"url"`
		} `json:"drafts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if rounds := len(fakeAI.requests()); rounds != 2 || response.Fetched != 2 || len(response.Drafts) != 2 {
		t.Fatalf("preview metadata = %+v AI rounds=%d", response, len(fakeAI.requests()))
	}
	if first := response.Drafts[0]; first.Title != "from first" || first.Link == "" || first.ExternalKey == "" || first.URL == "" {
		t.Fatalf("complete packed source lost provenance: %+v", first)
	}
	if second := response.Drafts[1]; second.Title != "from second" || second.Link != "" || second.ExternalKey != "" || second.URL != "" {
		t.Fatalf("omitted source received provenance: %+v", second)
	}
	reqs := fakeAI.requests()
	request := decodeAIRequest(t, reqs[0])
	if len(request.Messages) != 2 {
		t.Fatalf("AI messages = %d, want system and user", len(request.Messages))
	}
	for i, body := range reqs {
		if got := decodeAIRequest(t, body).budget(); got != aiImportMaxTokens {
			t.Fatalf("import preview round %d output budget = %d, want %d", i, got, aiImportMaxTokens)
		}
	}
	if names := toolNames(t, reqs[0]); !hasTool(names, "propose_card") || hasTool(names, "update_task") || hasTool(names, "fetch_link") {
		// Forge issue text is third-party input, so the run stays read-only.
		t.Fatalf("import preview tools = %v, want propose_card without the writing tools", names)
	}
	userMessage := request.Messages[1].Content
	if !strings.Contains(userMessage, "Source 1\n") || strings.Contains(userMessage, "Source 2\n") {
		t.Fatalf("AI user message contains an incomplete source record: %q", userMessage)
	}
}

func TestImportLinksRecordsCanonicalProvenanceAndUpserts(t *testing.T) {
	h, st := newIntegrationsHandler(t)
	baseURL := "https://forge.example"
	if _, err := st.SetForgeSource("default", "GitLab.Main", "gitlab", &baseURL, nil); err != nil {
		t.Fatalf("seed forge source: %v", err)
	}

	post := func(body string) {
		t.Helper()
		w := doReq(t, h, http.MethodPost, "/api/import/links", body, nil)
		if w.Code != http.StatusNoContent || w.Body.Len() != 0 {
			t.Fatalf("POST import links: got %d body=%q, want empty 204", w.Code, w.Body.String())
		}
	}
	post(`{"source":"GITLAB.MAIN","items":[{"external_key":"gitlab:forge.example/group/project#42","link":"gitlab#42","url":"https://forge.example/group/project/-/issues/42","title":"First import title"}]}`)

	key := "gitlab:forge.example/group/project#42"
	got, err := st.ImportedAs("default", []string{key})
	if err != nil {
		t.Fatalf("ImportedAs first import: %v", err)
	}
	want := store.ImportLink{
		Source: "gitlab.main", Kind: "gitlab", ExternalKey: key, Link: "gitlab#42",
		URL: "https://forge.example/group/project/-/issues/42", Title: "First import title",
	}
	if len(got) != 1 || got[key] != want {
		t.Fatalf("first import = %+v, want %+v", got, want)
	}

	// Re-recording provenance is still import journaling, not implicit drift
	// acceptance. Only the revision-bound accept endpoint advances this value.
	baseline := store.NewImportBaseline("Compared title", "Compared body", "2026-07-29T10:00:00Z")
	if err := st.SetImportBaseline("default", key, baseline); err != nil {
		t.Fatalf("seed import baseline: %v", err)
	}
	post(`{"source":"gitlab.main","items":[{"external_key":"gitlab:forge.example/group/project#42","link":"gitlab#42-refreshed","url":"https://forge.example/group/project/-/issues/42?refresh=1","title":"Refreshed import title"}]}`)
	want.Link = "gitlab#42-refreshed"
	want.URL = "https://forge.example/group/project/-/issues/42?refresh=1"
	want.Title = "Refreshed import title"
	got, err = st.ImportedAs("default", []string{key})
	if err != nil || len(got) != 1 || got[key] != want {
		t.Fatalf("upsert import = %+v, %v; want %+v", got, err, want)
	}
	after, found, err := st.ImportBaseline("default", key)
	if err != nil || !found || after != baseline {
		t.Fatalf("baseline after provenance upsert = (%+v, %t, %v), want unchanged %+v", after, found, err, baseline)
	}
}

func TestImportLinksRejectsBadBatchesWithoutPartialWrites(t *testing.T) {
	h, st := newIntegrationsHandler(t)
	baseURL := "https://forge.example"
	if _, err := st.SetForgeSource("default", "primary", "gitlab", &baseURL, nil); err != nil {
		t.Fatalf("seed forge source: %v", err)
	}
	valid := map[string]string{
		"external_key": "valid", "link": "gitlab#1", "url": "https://forge.example/1", "title": "valid title",
	}
	tooMany := make([]map[string]string, 101)
	for i := range tooMany {
		tooMany[i] = valid
	}
	tooManyBody, err := json.Marshal(map[string]any{"source": "primary", "items": tooMany})
	if err != nil {
		t.Fatalf("marshal oversized batch: %v", err)
	}
	cases := []struct {
		name, body string
		wantCode   int
	}{
		{"more than 100", string(tooManyBody), http.StatusBadRequest},
		{"malformed JSON", `{"source":`, http.StatusBadRequest},
		{"missing source", `{"items":[{"external_key":"valid","link":"gitlab#1","url":"https://forge.example/1","title":"valid title"}]}`, http.StatusBadRequest},
		{"missing item field", `{"source":"primary","items":[{"external_key":"valid","link":"gitlab#1","url":"https://forge.example/1","title":"valid title"},{"external_key":"missing-title","link":"gitlab#2","url":"https://forge.example/2"}]}`, http.StatusBadRequest},
		{"oversized field", fmt.Sprintf(`{"source":"primary","items":[{"external_key":"valid","link":"gitlab#1","url":"https://forge.example/1","title":"valid title"},{"external_key":%q,"link":"gitlab#2","url":"https://forge.example/2","title":"bad"}]}`, strings.Repeat("k", 2049)), http.StatusBadRequest},
		{"CRLF field", `{"source":"primary","items":[{"external_key":"valid","link":"gitlab#1","url":"https://forge.example/1","title":"valid title"},{"external_key":"bad\nkey","link":"gitlab#2","url":"https://forge.example/2","title":"bad"}]}`, http.StatusBadRequest},
		{"unknown source", `{"source":"missing","items":[{"external_key":"valid","link":"gitlab#1","url":"https://forge.example/1","title":"valid title"}]}`, http.StatusBadRequest},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			w := doReq(t, h, http.MethodPost, "/api/import/links", tt.body, nil)
			if w.Code != tt.wantCode {
				t.Fatalf("POST import links: got %d body=%q, want %d", w.Code, w.Body.String(), tt.wantCode)
			}
			got, err := st.ImportedAs("default", []string{"valid"})
			if err != nil || len(got) != 0 {
				t.Fatalf("rejected batch wrote rows: %+v, %v", got, err)
			}
		})
	}
}

func TestImportLinksAcceptsExactly100Items(t *testing.T) {
	h, st := newIntegrationsHandler(t)
	baseURL := "https://forge.example"
	if _, err := st.SetForgeSource("default", "primary", "gitlab", &baseURL, nil); err != nil {
		t.Fatalf("seed forge source: %v", err)
	}
	items := make([]map[string]string, 100)
	keys := make([]string, len(items))
	for i := range items {
		keys[i] = fmt.Sprintf("key-%d", i)
		items[i] = map[string]string{
			"external_key": keys[i], "link": fmt.Sprintf("gitlab#%d", i),
			"url": fmt.Sprintf("https://forge.example/%d", i), "title": fmt.Sprintf("title %d", i),
		}
	}
	body, err := json.Marshal(map[string]any{"source": "primary", "items": items})
	if err != nil {
		t.Fatalf("marshal 100-item batch: %v", err)
	}
	w := doReq(t, h, http.MethodPost, "/api/import/links", string(body), nil)
	if w.Code != http.StatusNoContent || w.Body.Len() != 0 {
		t.Fatalf("100-item import: got %d body=%q, want empty 204", w.Code, w.Body.String())
	}
	got, err := st.ImportedAs("default", keys)
	if err != nil || len(got) != len(items) {
		t.Fatalf("ImportedAs 100-item batch = %d rows, %v; want %d", len(got), err, len(items))
	}
}

func TestImportLinksStorageFailuresStayGeneric(t *testing.T) {
	h, st := newIntegrationsHandler(t)
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	w := doReq(t, h, http.MethodPost, "/api/import/links", `{"source":"primary","items":[]}`, nil)
	if w.Code != http.StatusInternalServerError || strings.TrimSpace(w.Body.String()) != "storage error" {
		t.Fatalf("closed-store import: got %d body=%q, want generic 500", w.Code, w.Body.String())
	}
}

func TestImportLinksAreScopedByAuthenticatedUser(t *testing.T) {
	h, st := newIntegrationsHandler(t)
	baseURL := "https://forge.example"
	for _, source := range []struct {
		scope, kind string
	}{
		{"alice", "gitlab"},
		{"bob", "github"},
	} {
		if _, err := st.SetForgeSource(source.scope, "shared", source.kind, &baseURL, nil); err != nil {
			t.Fatalf("seed %s source: %v", source.scope, err)
		}
	}
	if _, err := st.SetForgeSource("bob", "bob-only", "github", &baseURL, nil); err != nil {
		t.Fatalf("seed Bob-only source: %v", err)
	}
	key := "same-external-key"
	for _, user := range []struct {
		name, kind, title string
	}{
		{"Alice", "gitlab", "Alice import"},
		{"Bob", "github", "Bob import"},
	} {
		body := fmt.Sprintf(`{"source":"SHARED","items":[{"external_key":%q,"link":"%s#1","url":"https://forge.example/1","title":%q}]}`, key, user.kind, user.title)
		w := doReq(t, h, http.MethodPost, "/api/import/links", body, map[string]string{"X-KB-User": user.name})
		if w.Code != http.StatusNoContent {
			t.Fatalf("%s import: got %d body=%q", user.name, w.Code, w.Body.String())
		}
	}
	for _, want := range []struct {
		scope, kind, title string
	}{
		{"alice", "gitlab", "Alice import"},
		{"bob", "github", "Bob import"},
	} {
		got, err := st.ImportedAs(want.scope, []string{key})
		expected := store.ImportLink{Source: "shared", Kind: want.kind, ExternalKey: key, Link: want.kind + "#1", URL: "https://forge.example/1", Title: want.title}
		if err != nil || len(got) != 1 || got[key] != expected {
			t.Fatalf("ImportedAs(%q) = %+v, %v; want %+v", want.scope, got, err, expected)
		}
	}
	w := doReq(t, h, http.MethodPost, "/api/import/links", `{"source":"bob-only","items":[{"external_key":"blocked","link":"github#2","url":"https://forge.example/2","title":"blocked"}]}`, map[string]string{"X-KB-User": "Alice"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("Alice using Bob-only source: got %d body=%q, want 400", w.Code, w.Body.String())
	}
	if found, err := st.ImportedAs("alice", []string{"blocked"}); err != nil || len(found) != 0 {
		t.Fatalf("Bob-only source wrote Alice provenance: %+v, %v", found, err)
	}
}

// Regression: provenance lookup must keep every exact scoped candidate, even
// when different projects share the short forge link, without making egress.
func TestImportProvenanceReturnsAllScopedCandidatesWithoutEgress(t *testing.T) {
	st := newTestStore(t)
	s := newServer(Config{}, testStatic, st)
	var egress atomic.Int32
	denyEgress := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		egress.Add(1)
		return nil, errors.New("unexpected egress")
	})
	s.forgeClient = &http.Client{Transport: denyEgress}
	s.aiClient = &http.Client{Transport: denyEgress}
	h := s.handler()

	const sharedLink = "gitlab#42"
	if err := st.RecordImportLinks("alice", []store.ImportLink{
		{Source: "primary", Kind: "gitlab", ExternalKey: "gitlab:gitlab.example/acme/zeta#42", Link: sharedLink, URL: "https://gitlab.example/acme/zeta/-/issues/42", Title: "Zeta issue"},
		{Source: "primary", Kind: "gitlab", ExternalKey: "gitlab:gitlab.example/acme/alpha#42", Link: sharedLink, URL: "https://gitlab.example/acme/alpha/-/issues/42", Title: "Alpha issue"},
		{Source: "primary", Kind: "gitlab", ExternalKey: "gitlab:gitlab.example/acme/only-alice#99", Link: "gitlab#99", URL: "https://gitlab.example/acme/only-alice/-/issues/99", Title: "Alice only"},
	}); err != nil {
		t.Fatalf("seed Alice provenance: %v", err)
	}
	if err := st.RecordImportLinks("bob", []store.ImportLink{{
		Source: "other", Kind: "gitlab", ExternalKey: "gitlab:gitlab.example/other/project#42", Link: sharedLink, URL: "https://gitlab.example/other/project/-/issues/42", Title: "Bob issue",
	}}); err != nil {
		t.Fatalf("seed Bob provenance: %v", err)
	}

	w := doReq(t, h, http.MethodPost, "/api/import/provenance", `{"link":" gitlab#42 "}`, map[string]string{"X-KB-User": "Alice"})
	if w.Code != http.StatusOK {
		t.Fatalf("Alice provenance lookup = %d body=%q, want 200", w.Code, w.Body.String())
	}
	var response importProvenanceResponse
	decodeForgeJSON(t, w, &response)
	want := []importProvenanceItem{
		{Source: "primary", ExternalKey: "gitlab:gitlab.example/acme/alpha#42", Title: "Alpha issue", URL: "https://gitlab.example/acme/alpha/-/issues/42"},
		{Source: "primary", ExternalKey: "gitlab:gitlab.example/acme/zeta#42", Title: "Zeta issue", URL: "https://gitlab.example/acme/zeta/-/issues/42"},
	}
	if len(response.Items) != len(want) {
		t.Fatalf("Alice provenance items = %+v, want %+v", response.Items, want)
	}
	for i := range want {
		if response.Items[i] != want[i] {
			t.Fatalf("Alice provenance item %d = %+v, want %+v", i, response.Items[i], want[i])
		}
	}
	var raw struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw provenance response: %v", err)
	}
	for _, item := range raw.Items {
		if len(item) != 4 || item["source"] == nil || item["external_key"] == nil || item["title"] == nil || item["url"] == nil ||
			item["link"] != nil || item["kind"] != nil || item["pat"] != nil {
			t.Fatalf("provenance item exposed non-public fields: %+v", item)
		}
	}
	if egress.Load() != 0 {
		t.Fatalf("provenance lookup made %d egress requests, want 0", egress.Load())
	}

	bob := doReq(t, h, http.MethodPost, "/api/import/provenance", `{"link":"gitlab#99"}`, map[string]string{"X-KB-User": "Bob"})
	if bob.Code != http.StatusNotFound || egress.Load() != 0 {
		t.Fatalf("Bob cross-scope lookup = %d body=%q egress=%d, want 404 and 0", bob.Code, bob.Body.String(), egress.Load())
	}
}

// Regression: malformed or absent links must fail before a scoped store lookup.
func TestImportProvenanceRejectsMalformedOrMissingLinks(t *testing.T) {
	h, _ := newIntegrationsHandler(t)
	for _, test := range []struct {
		name, body string
	}{
		{name: "malformed JSON", body: `{"link":`},
		{name: "missing link", body: `{}`},
		{name: "blank link", body: `{"link":"   "}`},
		{name: "too long", body: fmt.Sprintf(`{"link":%q}`, strings.Repeat("x", 2049))},
		{name: "line feed", body: `{"link":"gitlab#42\n"}`},
		{name: "carriage return", body: `{"link":"gitlab#42\r"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := doReq(t, h, http.MethodPost, "/api/import/provenance", test.body, nil)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("POST import provenance = %d body=%q, want 400", w.Code, w.Body.String())
			}
		})
	}

	missing := doReq(t, h, http.MethodPost, "/api/import/provenance", `{"link":"gitlab#404"}`, nil)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing import provenance = %d body=%q, want 404", missing.Code, missing.Body.String())
	}
}

// Regression: failed local provenance reads disclose neither store detail nor
// an alternate lookup result.
func TestImportProvenanceStorageFailuresStayGeneric(t *testing.T) {
	h, st := newIntegrationsHandler(t)
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	w := doReq(t, h, http.MethodPost, "/api/import/provenance", `{"link":"gitlab#42"}`, nil)
	if w.Code != http.StatusInternalServerError || strings.TrimSpace(w.Body.String()) != "storage error" {
		t.Fatalf("closed-store provenance = %d body=%q, want generic 500", w.Code, w.Body.String())
	}
}

type decodedImportDriftResponse struct {
	State         string `json:"state"`
	Link          string `json:"link"`
	URL           string `json:"url"`
	TitleChanged  *bool  `json:"title_changed"`
	UpstreamTitle string `json:"upstream_title"`
	BaselineTitle string `json:"baseline_title"`
	BaselineAt    string `json:"baseline_at"`
	CheckedAt     string `json:"checked_at"`
	Summary       string `json:"summary"`
	Revision      string `json:"revision"`
}

type decodedImportDriftAcceptResponse struct {
	BaselineAt string `json:"baseline_at"`
}

type importDriftIssueState struct {
	Title string
	Body  string
}

func postImportDrift(t *testing.T, h http.Handler, source, externalKey string, headers map[string]string) (*httptest.ResponseRecorder, decodedImportDriftResponse) {
	t.Helper()
	body, err := json.Marshal(map[string]string{"source": source, "external_key": externalKey})
	if err != nil {
		t.Fatalf("marshal drift request: %v", err)
	}
	w := doReq(t, h, http.MethodPost, "/api/import/drift", string(body), headers)
	var response decodedImportDriftResponse
	if w.Code == http.StatusOK {
		decodeForgeJSON(t, w, &response)
	}
	return w, response
}

func postImportDriftAccept(t *testing.T, h http.Handler, source, externalKey, revision string, headers map[string]string) (*httptest.ResponseRecorder, decodedImportDriftAcceptResponse) {
	t.Helper()
	body, err := json.Marshal(importDriftAcceptRequest{Source: source, ExternalKey: externalKey, Revision: revision})
	if err != nil {
		t.Fatalf("marshal drift accept request: %v", err)
	}
	w := doReq(t, h, http.MethodPost, "/api/import/drift/accept", string(body), headers)
	var response decodedImportDriftAcceptResponse
	if w.Code == http.StatusOK {
		decodeForgeJSON(t, w, &response)
	}
	return w, response
}

type importDriftSeed struct {
	scope       string
	source      string
	kind        string
	baseURL     string
	pat         string
	externalKey string
	storedURL   string
}

func seedImportDrift(t *testing.T, st *store.Store, seed importDriftSeed) {
	t.Helper()
	if _, err := st.SetForgeSource(seed.scope, seed.source, seed.kind, &seed.baseURL, &seed.pat); err != nil {
		t.Fatalf("seed drift forge source: %v", err)
	}
	if err := st.RecordImportLinks(seed.scope, []store.ImportLink{{
		Source: seed.source, Kind: seed.kind, ExternalKey: seed.externalKey,
		Link: seed.kind + "#42", URL: seed.storedURL, Title: "Imported title",
	}}); err != nil {
		t.Fatalf("seed drift provenance: %v", err)
	}
}

// Regression: the first check must establish one server-authoritative baseline,
// later reads must compare against it, and drift reads must never advance it.
func TestImportDriftBaselineLifecycleAndCorrectPAT(t *testing.T) {
	const pat = "glpat-drift-secret"
	var forgeCalls atomic.Int32
	var badPAT atomic.Bool
	var issueState atomic.Value
	issueState.Store(importDriftIssueState{Title: "Initial title", Body: "Initial body"})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forgeCalls.Add(1)
		if r.Header.Get("PRIVATE-TOKEN") != pat {
			badPAT.Store(true)
		}
		if strings.HasSuffix(r.URL.Path, "/notes") {
			_, _ = io.WriteString(w, `[]`)
			return
		}
		issue := issueState.Load().(importDriftIssueState)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"iid": 42, "title": issue.Title, "description": issue.Body,
			"web_url": "https://upstream-response.invalid/changed",
		})
	}))
	defer upstream.Close()

	t.Setenv("KB_FORGE_ALLOW_PRIVATE", "127.0.0.1")
	h, st := newIntegrationsHandler(t)
	key := "gitlab:local/group/project#42"
	storedURL := upstream.URL + "/group/project/-/issues/42"
	seedImportDrift(t, st, importDriftSeed{
		scope: "default", source: "Primary", kind: "gitlab", baseURL: upstream.URL,
		pat: pat, externalKey: key, storedURL: storedURL,
	})

	first, recorded := postImportDrift(t, h, "PRIMARY", key, nil)
	if first.Code != http.StatusOK || recorded.State != "baseline_recorded" || recorded.TitleChanged != nil ||
		recorded.Link != "gitlab#42" || recorded.URL != storedURL || recorded.UpstreamTitle != "Initial title" ||
		recorded.BaselineTitle != "Initial title" || recorded.BaselineAt != recorded.CheckedAt || recorded.Summary != "" {
		t.Fatalf("first drift response = %d %+v", first.Code, recorded)
	}
	if _, err := time.Parse(time.RFC3339Nano, recorded.CheckedAt); err != nil {
		t.Fatalf("checked_at %q is not RFC3339Nano: %v", recorded.CheckedAt, err)
	}
	baseline, found, err := st.ImportBaseline("default", key)
	wantBaseline := store.NewImportBaseline("Initial title", "Initial body", recorded.CheckedAt)
	if err != nil || !found || baseline != wantBaseline {
		t.Fatalf("persisted first baseline = (%+v, %t, %v), want %+v", baseline, found, err, wantBaseline)
	}

	second, unchanged := postImportDrift(t, h, "primary", key, nil)
	if second.Code != http.StatusOK || unchanged.State != "unchanged" || unchanged.TitleChanged == nil || *unchanged.TitleChanged ||
		unchanged.BaselineTitle != "Initial title" || unchanged.BaselineAt != recorded.CheckedAt {
		t.Fatalf("unchanged drift response = %d %+v", second.Code, unchanged)
	}

	issueState.Store(importDriftIssueState{Title: "Initial title", Body: "Changed body"})
	third, bodyDrift := postImportDrift(t, h, "primary", key, nil)
	if third.Code != http.StatusOK || bodyDrift.State != "drifted" || bodyDrift.TitleChanged == nil || *bodyDrift.TitleChanged {
		t.Fatalf("body drift response = %d %+v", third.Code, bodyDrift)
	}

	issueState.Store(importDriftIssueState{Title: "Changed title", Body: "Changed body"})
	fourth, titleDrift := postImportDrift(t, h, "primary", key, nil)
	if fourth.Code != http.StatusOK || titleDrift.State != "drifted" || titleDrift.TitleChanged == nil || !*titleDrift.TitleChanged {
		t.Fatalf("title drift response = %d %+v", fourth.Code, titleDrift)
	}
	fifth, repeated := postImportDrift(t, h, "primary", key, nil)
	if fifth.Code != http.StatusOK || repeated.State != "drifted" || repeated.BaselineAt != recorded.CheckedAt {
		t.Fatalf("repeated drift response = %d %+v", fifth.Code, repeated)
	}
	after, found, err := st.ImportBaseline("default", key)
	if err != nil || !found || after != baseline {
		t.Fatalf("baseline after repeated drift = (%+v, %t, %v), want unchanged %+v", after, found, err, baseline)
	}
	if forgeCalls.Load() != 10 {
		t.Fatalf("forge requests = %d, want issue+notes for five checks", forgeCalls.Load())
	}
	if badPAT.Load() {
		t.Fatal("forge request did not carry the configured PAT")
	}
}

// Regression: only a drifted response exposes the server-issued revision; it
// binds both title and full body and supports a single-read, idempotent accept.
func TestImportDriftRevisionAndAccept(t *testing.T) {
	const pat = "glpat-accept-secret"
	var forgeCalls atomic.Int32
	var issueState atomic.Value
	issueState.Store(importDriftIssueState{Title: "Initial title", Body: "Initial body"})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("forge method = %s, want GET", r.Method)
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		forgeCalls.Add(1)
		if strings.HasSuffix(r.URL.Path, "/notes") {
			_, _ = io.WriteString(w, `[]`)
			return
		}
		issue := issueState.Load().(importDriftIssueState)
		_ = json.NewEncoder(w).Encode(map[string]any{"iid": 42, "title": issue.Title, "description": issue.Body})
	}))
	defer upstream.Close()

	t.Setenv("KB_FORGE_ALLOW_PRIVATE", "127.0.0.1")
	st := newTestStore(t)
	s := newServer(Config{}, testStatic, st)
	t.Cleanup(s.forgeClient.CloseIdleConnections)
	var aiCalls atomic.Int32
	s.aiClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		aiCalls.Add(1)
		return nil, errors.New("unexpected AI request")
	})}
	h := s.handler()
	key := "gitlab:local/group/project#42"
	seedImportDrift(t, st, importDriftSeed{
		scope: "default", source: "primary", kind: "gitlab", baseURL: upstream.URL,
		pat: pat, externalKey: key, storedURL: upstream.URL + "/group/project/-/issues/42",
	})

	first, recorded := postImportDrift(t, h, "primary", key, nil)
	if first.Code != http.StatusOK || recorded.State != "baseline_recorded" || recorded.Revision != "" || strings.Contains(first.Body.String(), `"revision"`) {
		t.Fatalf("baseline response = %d %+v body=%q, want no revision", first.Code, recorded, first.Body.String())
	}
	issueState.Store(importDriftIssueState{Title: "Initial title", Body: "Changed body"})
	second, bodyDrift := postImportDrift(t, h, "primary", key, nil)
	if second.Code != http.StatusOK || bodyDrift.State != "drifted" || !forge.ValidRevision(bodyDrift.Revision) {
		t.Fatalf("body drift response = %d %+v", second.Code, bodyDrift)
	}
	issueState.Store(importDriftIssueState{Title: "Changed title", Body: "Changed body"})
	third, titleDrift := postImportDrift(t, h, "primary", key, nil)
	if third.Code != http.StatusOK || titleDrift.State != "drifted" || !forge.ValidRevision(titleDrift.Revision) || titleDrift.Revision == bodyDrift.Revision {
		t.Fatalf("title drift response = %d %+v, body revision=%q", third.Code, titleDrift, bodyDrift.Revision)
	}

	beforeStale := forgeCalls.Load()
	stale, _ := postImportDriftAccept(t, h, "primary", key, bodyDrift.Revision, nil)
	if stale.Code != http.StatusConflict || forgeCalls.Load() != beforeStale+1 {
		t.Fatalf("stale accept = %d body=%q forge=%d->%d, want 409 and one snapshot GET", stale.Code, stale.Body.String(), beforeStale, forgeCalls.Load())
	}
	baseline, found, err := st.ImportBaseline("default", key)
	if err != nil || !found || baseline.Title != "Initial title" || baseline.Hash != store.NewImportBaseline("Initial title", "Initial body", "").Hash {
		t.Fatalf("baseline after stale accept = (%+v, %t, %v), want initial", baseline, found, err)
	}

	accepted, advanced := postImportDriftAccept(t, h, "primary", key, titleDrift.Revision, nil)
	if accepted.Code != http.StatusOK || advanced.BaselineAt == "" {
		t.Fatalf("matching accept = %d %+v body=%q", accepted.Code, advanced, accepted.Body.String())
	}
	afterAccept, found, err := st.ImportBaseline("default", key)
	if err != nil || !found || afterAccept.At != advanced.BaselineAt || forge.BaselineRevision(afterAccept) != titleDrift.Revision {
		t.Fatalf("accepted baseline = (%+v, %t, %v), want revision=%q at=%q", afterAccept, found, err, titleDrift.Revision, advanced.BaselineAt)
	}
	next, unchanged := postImportDrift(t, h, "primary", key, nil)
	if next.Code != http.StatusOK || unchanged.State != "unchanged" || unchanged.Revision != "" || strings.Contains(next.Body.String(), `"revision"`) {
		t.Fatalf("next drift response = %d %+v body=%q, want unchanged without revision", next.Code, unchanged, next.Body.String())
	}
	beforeRetry := forgeCalls.Load()
	retry, retried := postImportDriftAccept(t, h, "primary", key, titleDrift.Revision, nil)
	if retry.Code != http.StatusOK || retried.BaselineAt != advanced.BaselineAt || forgeCalls.Load() != beforeRetry {
		t.Fatalf("retry accept = %d %+v forge=%d->%d, want idempotent no egress", retry.Code, retried, beforeRetry, forgeCalls.Load())
	}
	if aiCalls.Load() != 0 {
		t.Fatalf("drift accept made %d AI calls, want 0", aiCalls.Load())
	}
}

// Regression: rejected accepts fail before forge egress where possible and
// cannot move another user's or a mismatched target's baseline.
func TestImportDriftAcceptRejectsInvalidTargetsAndPreservesBaseline(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("forge method = %s, want GET", r.Method)
		}
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"iid": 42, "title": "Current", "description": "Current body"})
	}))
	defer upstream.Close()

	t.Setenv("KB_FORGE_ALLOW_PRIVATE", "127.0.0.1")
	h, st := newIntegrationsHandler(t)
	const key = "gitlab:local/group/project#42"
	seedImportDrift(t, st, importDriftSeed{
		scope: "default", source: "primary", kind: "gitlab", baseURL: upstream.URL,
		pat: "secret", externalKey: key, storedURL: upstream.URL + "/group/project/-/issues/42",
	})
	old := store.NewImportBaseline("Old", "Old body", "2026-07-29T10:00:00Z")
	if err := st.SetImportBaseline("default", key, old); err != nil {
		t.Fatalf("seed baseline: %v", err)
	}
	seedImportDrift(t, st, importDriftSeed{
		scope: "default", source: "primary", kind: "gitlab", baseURL: upstream.URL,
		pat: "secret", externalKey: "no-baseline", storedURL: upstream.URL + "/group/project/-/issues/42",
	})
	seedImportDrift(t, st, importDriftSeed{
		scope: "alice", source: "primary", kind: "gitlab", baseURL: upstream.URL,
		pat: "secret", externalKey: "alice-key", storedURL: upstream.URL + "/group/project/-/issues/42",
	})
	if err := st.RecordImportLinks("default", []store.ImportLink{
		{Source: "primary", Kind: "github", ExternalKey: "kind-mismatch", Link: "github#42", URL: upstream.URL + "/group/project/-/issues/42", Title: "wrong kind"},
		{Source: "primary", Kind: "gitlab", ExternalKey: "host-mismatch", Link: "gitlab#42", URL: "https://unconfigured.invalid/group/project/-/issues/42", Title: "wrong host"},
	}); err != nil {
		t.Fatalf("seed rejected accept provenance: %v", err)
	}

	validButStale := forge.BaselineRevision(store.NewImportBaseline("Different", "body", ""))
	tests := []struct {
		name, source, externalKey, revision string
		headers                             map[string]string
		wantCode                            int
		wantEgress                          bool
	}{
		{name: "tampered revision", source: "primary", externalKey: key, revision: strings.ToUpper(validButStale), wantCode: http.StatusBadRequest},
		{name: "malformed revision", source: "primary", externalKey: key, revision: "short", wantCode: http.StatusBadRequest},
		{name: "cross scope", source: "primary", externalKey: "alice-key", revision: validButStale, wantCode: http.StatusNotFound},
		{name: "source mismatch", source: "other", externalKey: key, revision: validButStale, wantCode: http.StatusBadRequest},
		{name: "kind mismatch", source: "primary", externalKey: "kind-mismatch", revision: validButStale, wantCode: http.StatusBadRequest},
		{name: "host mismatch", source: "primary", externalKey: "host-mismatch", revision: validButStale, wantCode: http.StatusBadRequest},
		{name: "absent baseline", source: "primary", externalKey: "no-baseline", revision: validButStale, wantCode: http.StatusConflict},
		{name: "stale revision", source: "primary", externalKey: key, revision: validButStale, wantCode: http.StatusConflict, wantEgress: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := calls.Load()
			w, _ := postImportDriftAccept(t, h, test.source, test.externalKey, test.revision, test.headers)
			gotEgress := calls.Load() != before
			if w.Code != test.wantCode || gotEgress != test.wantEgress {
				t.Fatalf("rejected accept = %d body=%q egress=%t, want %d/%t", w.Code, w.Body.String(), gotEgress, test.wantCode, test.wantEgress)
			}
		})
	}
	after, found, err := st.ImportBaseline("default", key)
	if err != nil || !found || after != old {
		t.Fatalf("baseline after rejected accepts = (%+v, %t, %v), want %+v", after, found, err, old)
	}
}

// Regression: acceptance maps forge and store failures to the same opaque,
// generic errors as the drift read; neither path may expose implementation
// details in a client response.
func TestImportDriftAcceptFailuresStayOpaque(t *testing.T) {
	t.Run("forge upstream", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "sensitive upstream detail", http.StatusInternalServerError)
		}))
		defer upstream.Close()
		t.Setenv("KB_FORGE_ALLOW_PRIVATE", "127.0.0.1")
		h, st := newIntegrationsHandler(t)
		key := "gitlab:local/group/project#42"
		seedImportDrift(t, st, importDriftSeed{
			scope: "default", source: "primary", kind: "gitlab", baseURL: upstream.URL,
			pat: "", externalKey: key, storedURL: upstream.URL + "/group/project/-/issues/42",
		})
		if err := st.SetImportBaseline("default", key, store.NewImportBaseline("Old", "Old", "2026-07-29T10:00:00Z")); err != nil {
			t.Fatalf("seed baseline: %v", err)
		}
		w, _ := postImportDriftAccept(t, h, "primary", key, forge.BaselineRevision(store.NewImportBaseline("Current", "Current", "")), nil)
		if w.Code != http.StatusBadGateway || strings.TrimSpace(w.Body.String()) != "connection failed" {
			t.Fatalf("forge accept failure = %d %q, want opaque 502", w.Code, w.Body.String())
		}
	})

	t.Run("store", func(t *testing.T) {
		h, st := newIntegrationsHandler(t)
		if err := st.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
		w, _ := postImportDriftAccept(t, h, "primary", "key", strings.Repeat("a", 64), nil)
		if w.Code != http.StatusInternalServerError || strings.TrimSpace(w.Body.String()) != "storage error" {
			t.Fatalf("store accept failure = %d %q, want generic 500", w.Code, w.Body.String())
		}
	})
}

// Regression: an optional drift summary gets one bounded plain-text call and
// must never disclose credentials or either forge URL to the model.
func TestImportDriftAISummaryIsSingleBoundedSecretFreeCall(t *testing.T) {
	const pat = "glpat-never-send"
	body := "Current body"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/notes") {
			_, _ = io.WriteString(w, `[]`)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"iid": 42, "title": "Current title", "description": body,
			"web_url": "https://upstream-response.invalid/secret-path",
		})
	}))
	defer upstream.Close()
	fakeAI := &fakeOpenAI{content: strings.Repeat("é", maxImportCommentBytes)}
	aiUpstream := httptest.NewServer(fakeAI.handler())
	defer aiUpstream.Close()

	t.Setenv("KB_FORGE_ALLOW_PRIVATE", "127.0.0.1")
	t.Setenv("KB_AI_ALLOW_PRIVATE", "1")
	h, st := newIntegrationsHandler(t)
	key := "gitlab:local/group/project#42"
	storedURL := upstream.URL + "/group/project/-/issues/42"
	seedImportDrift(t, st, importDriftSeed{
		scope: "default", source: "primary", kind: "gitlab", baseURL: upstream.URL,
		pat: pat, externalKey: key, storedURL: storedURL,
	})
	if err := st.SetImportBaseline("default", key, store.NewImportBaseline("Old title", "Old body", "2026-07-29T10:00:00Z")); err != nil {
		t.Fatalf("seed baseline: %v", err)
	}
	configureAI(t, h, aiUpstream.URL, "drift-model", "sk-ai-secret")

	w, response := postImportDrift(t, h, "primary", key, nil)
	if w.Code != http.StatusOK || response.State != "drifted" || fakeAI.calls != 1 {
		t.Fatalf("AI drift response = %d %+v, calls=%d", w.Code, response, fakeAI.calls)
	}
	if len(response.Summary) != maxImportCommentBytes || !utf8.ValidString(response.Summary) {
		t.Fatalf("summary length/UTF-8 = %d/%t, want %d/true",
			len(response.Summary), utf8.ValidString(response.Summary), maxImportCommentBytes)
	}
	request := decodeAIRequest(t, fakeAI.reqBody)
	if request.ResponseFormat != nil {
		t.Fatal("drift summary requested JSON mode instead of plain text")
	}
	// The summary compares two pieces of text the server already holds. A tool
	// would only be another way for third-party issue text to reach the board,
	// and offering none is also what keeps the run at a single round trip.
	if len(request.Tools) != 0 {
		t.Fatalf("drift summary offered %+v, want a tool-less run", request.Tools)
	}
	if *request.MaxTokens != aiDriftMaxTokens {
		t.Fatalf("drift summary max_tokens = %d, want %d", *request.MaxTokens, aiDriftMaxTokens)
	}
	for _, secret := range []string{pat, storedURL, "https://upstream-response.invalid/secret-path", "sk-ai-secret"} {
		if strings.Contains(string(fakeAI.reqBody), secret) {
			t.Fatalf("AI drift prompt disclosed forbidden value %q", secret)
		}
	}
	if len(request.Messages) != 2 || len(request.Messages[1].Content) > maxImportPackBytes {
		t.Fatalf("AI drift prompt messages/bytes = %d/%d, want 2/<=%d", len(request.Messages), len(request.Messages[1].Content), maxImportPackBytes)
	}
	for _, expected := range []string{"Old title", "Old body", "Current title", "Current body"} {
		if !strings.Contains(request.Messages[1].Content, expected) {
			t.Fatalf("AI drift prompt omitted comparison text %q", expected)
		}
	}
}

// Regression: AI is best-effort; failure or absent configuration cannot turn a
// valid drift comparison into an error or trigger an unconfigured request.
func TestImportDriftAIFailureDegradesAndUnconfiguredAICallsNothing(t *testing.T) {
	for _, test := range []struct {
		name          string
		configure     bool
		storedBaseURL string
		status        int
		wantAICalls   int
	}{
		{name: "upstream failure", configure: true, status: http.StatusInternalServerError, wantAICalls: 1},
		{name: "unconfigured", wantAICalls: 0},
		// A stored base URL no client can be built for is the user's own
		// settings, not an upstream outcome: it degrades the same way, and it
		// does so before anything is dialed.
		{name: "unusable base URL", storedBaseURL: "ftp://ai.invalid", wantAICalls: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/notes") {
					_, _ = io.WriteString(w, `[]`)
					return
				}
				_, _ = io.WriteString(w, `{"iid":42,"title":"new","description":"new body"}`)
			}))
			defer upstream.Close()
			fakeAI := &fakeOpenAI{status: test.status}
			aiUpstream := httptest.NewServer(fakeAI.handler())
			defer aiUpstream.Close()

			t.Setenv("KB_FORGE_ALLOW_PRIVATE", "127.0.0.1")
			t.Setenv("KB_AI_ALLOW_PRIVATE", "1")
			h, st := newIntegrationsHandler(t)
			key := "gitlab:local/group/project#42"
			seedImportDrift(t, st, importDriftSeed{
				scope: "default", source: "primary", kind: "gitlab", baseURL: upstream.URL,
				pat: "", externalKey: key, storedURL: upstream.URL + "/group/project/-/issues/42",
			})
			if err := st.SetImportBaseline("default", key, store.NewImportBaseline("old", "old body", "2026-07-29T10:00:00Z")); err != nil {
				t.Fatalf("seed baseline: %v", err)
			}
			if test.configure {
				configureAI(t, h, aiUpstream.URL, "drift-model", "")
			}
			if test.storedBaseURL != "" {
				base, model := test.storedBaseURL, "drift-model"
				if _, err := st.SetAISettings("default", &base, &model, nil); err != nil {
					t.Fatalf("seed AI settings: %v", err)
				}
			}

			w, response := postImportDrift(t, h, "primary", key, nil)
			if w.Code != http.StatusOK || response.State != "drifted" || response.Summary != "" || fakeAI.calls != test.wantAICalls {
				t.Fatalf("best-effort AI response = %d %+v, calls=%d", w.Code, response, fakeAI.calls)
			}
		})
	}
}

// Regression: provenance, source, and issue validation must complete before
// credentials are assigned, so rejected checks make zero forge requests.
func TestImportDriftRejectsInvalidOrUnauthorizedChecksWithoutEgress(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	}))
	defer upstream.Close()
	t.Setenv("KB_FORGE_ALLOW_PRIVATE", "127.0.0.1")
	h, st := newIntegrationsHandler(t)
	seedImportDrift(t, st, importDriftSeed{
		scope: "alice", source: "primary", kind: "gitlab", baseURL: upstream.URL,
		pat: "secret", externalKey: "alice-key", storedURL: upstream.URL + "/group/project/-/issues/42",
	})
	seedImportDrift(t, st, importDriftSeed{
		scope: "default", source: "primary", kind: "gitlab", baseURL: upstream.URL,
		pat: "secret", externalKey: "unconfigured-url", storedURL: "https://unconfigured.invalid/group/project/-/issues/42",
	})
	if err := st.RecordImportLinks("default", []store.ImportLink{
		{Source: "primary", Kind: "gitlab", ExternalKey: "project-only", Link: "gitlab", URL: upstream.URL + "/group/project", Title: "project"},
		{Source: "different", Kind: "gitlab", ExternalKey: "source-mismatch", Link: "gitlab#42", URL: upstream.URL + "/group/project/-/issues/42", Title: "source"},
		{Source: "primary", Kind: "github", ExternalKey: "kind-mismatch", Link: "github#42", URL: upstream.URL + "/group/project/-/issues/42", Title: "kind"},
	}); err != nil {
		t.Fatalf("seed rejected provenance: %v", err)
	}

	tests := []struct {
		name, source, key string
		headers           map[string]string
		wantCode          int
	}{
		{name: "unknown key", source: "primary", key: "missing", wantCode: http.StatusNotFound},
		{name: "Bob cannot read Alice", source: "primary", key: "alice-key", headers: map[string]string{"X-KB-User": "Bob"}, wantCode: http.StatusNotFound},
		{name: "unconfigured stored URL", source: "primary", key: "unconfigured-url", wantCode: http.StatusBadRequest},
		{name: "source mismatch", source: "primary", key: "source-mismatch", wantCode: http.StatusBadRequest},
		{name: "kind mismatch", source: "primary", key: "kind-mismatch", wantCode: http.StatusBadRequest},
		{name: "non-issue reference", source: "primary", key: "project-only", wantCode: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := calls.Load()
			w, _ := postImportDrift(t, h, test.source, test.key, test.headers)
			if w.Code != test.wantCode || calls.Load() != before {
				t.Fatalf("rejected drift = %d body=%q, forge calls %d -> %d", w.Code, w.Body.String(), before, calls.Load())
			}
		})
	}
}

// Regression: forge failures are opaque while storage failures remain generic.
func TestImportDriftFailuresAreOpaque(t *testing.T) {
	t.Run("forge upstream", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "sensitive upstream detail", http.StatusInternalServerError)
		}))
		defer upstream.Close()
		t.Setenv("KB_FORGE_ALLOW_PRIVATE", "127.0.0.1")
		h, st := newIntegrationsHandler(t)
		key := "gitlab:local/group/project#42"
		seedImportDrift(t, st, importDriftSeed{
			scope: "default", source: "primary", kind: "gitlab", baseURL: upstream.URL,
			pat: "", externalKey: key, storedURL: upstream.URL + "/group/project/-/issues/42",
		})
		w, _ := postImportDrift(t, h, "primary", key, nil)
		if w.Code != http.StatusBadGateway || strings.TrimSpace(w.Body.String()) != "connection failed" {
			t.Fatalf("forge failure = %d %q, want opaque 502", w.Code, w.Body.String())
		}
	})

	t.Run("store", func(t *testing.T) {
		h, st := newIntegrationsHandler(t)
		if err := st.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
		w, _ := postImportDrift(t, h, "primary", "key", nil)
		if w.Code != http.StatusInternalServerError || strings.TrimSpace(w.Body.String()) != "storage error" {
			t.Fatalf("store failure = %d %q, want generic 500", w.Code, w.Body.String())
		}
	})
}

// Import references cannot select an unconfigured host, and a configured
// private source still obeys the forge client's opaque SSRF guard.
func TestImportPreviewRejectsUnconfiguredAndPrivateForgeHosts(t *testing.T) {
	t.Run("unconfigured host", func(t *testing.T) {
		h, _ := newTestServer(t, Config{})
		w := doReq(t, h, http.MethodPost, "/api/import/preview", `{"source":"missing","ref":"https://unknown.example/group/project"}`, nil)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "no configured source for host unknown.example") {
			t.Fatalf("unconfigured response = %d %q", w.Code, w.Body.String())
		}
	})

	t.Run("configured but private", func(t *testing.T) {
		upstream := httptest.NewServer(http.NotFoundHandler())
		defer upstream.Close()
		t.Setenv("KB_FORGE_ALLOW_PRIVATE", "")
		h, st := newTestServer(t, Config{})
		baseURL := upstream.URL
		if _, err := st.SetForgeSource("default", "private", "gitlab", &baseURL, nil); err != nil {
			t.Fatalf("seed private source: %v", err)
		}
		w := doReq(t, h, http.MethodPost, "/api/import/preview", fmt.Sprintf(`{"source":"private","ref":%q}`, upstream.URL+"/group/project"), nil)
		if w.Code != http.StatusBadGateway || strings.TrimSpace(w.Body.String()) != "connection failed" {
			t.Fatalf("private response = %d %q", w.Code, w.Body.String())
		}
	})
}
