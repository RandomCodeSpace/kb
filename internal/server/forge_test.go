package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RandomCodeSpace/kb/internal/store"
)

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

func TestForgeAPIBaseDerivation(t *testing.T) {
	tests := []struct {
		name, kind, baseURL, want string
	}{
		{
			name:    "GitLab root",
			kind:    "gitlab",
			baseURL: "https://gitlab.example.com",
			want:    "https://gitlab.example.com/api/v4",
		},
		{
			name:    "GitLab subpath and trailing slash",
			kind:    "gitlab",
			baseURL: "https://gitlab.example.com/forge/",
			want:    "https://gitlab.example.com/forge/api/v4",
		},
		{
			name:    "public GitHub",
			kind:    "github",
			baseURL: "https://github.com/",
			want:    "https://api.github.com",
		},
		{
			name:    "public GitHub with explicit HTTPS port",
			kind:    "github",
			baseURL: "https://github.com:443/",
			want:    "https://api.github.com",
		},
		{
			name:    "GitHub Enterprise subpath",
			kind:    "github",
			baseURL: "https://github.example.com/forge/",
			want:    "https://github.example.com/forge/api/v3",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := forgeAPIBase(test.kind, test.baseURL)
			if err != nil {
				t.Fatalf("forgeAPIBase(%q, %q): %v", test.kind, test.baseURL, err)
			}
			if got != test.want {
				t.Fatalf("forgeAPIBase(%q, %q) = %q, want %q", test.kind, test.baseURL, got, test.want)
			}
		})
	}
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
