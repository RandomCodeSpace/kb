package server

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestSameHostRedirectPolicy(t *testing.T) {
	tests := []struct {
		name    string
		from    string
		to      string
		wantErr string
	}{
		{name: "same origin", from: "https://forge.example/start", to: "https://forge.example/final"},
		{name: "hostname case", from: "https://FORGE.example/start", to: "https://forge.EXAMPLE/final"},
		{name: "normalized trailing dot", from: "https://forge.example./start", to: "https://forge.example/final"},
		{name: "same host upgrade", from: "http://forge.example/start", to: "https://forge.example/final"},
		{name: "host change", from: "https://forge.example/start", to: "https://other.example/final", wantErr: "cross-host"},
		{name: "subdomain change", from: "https://forge.example/start", to: "https://api.forge.example/final", wantErr: "cross-host"},
		{name: "port change", from: "https://forge.example:443/start", to: "https://forge.example:8443/final", wantErr: "cross-host"},
		{name: "explicit default port change", from: "https://forge.example/start", to: "https://forge.example:443/final", wantErr: "cross-host"},
		{name: "same host downgrade", from: "https://forge.example/start", to: "http://forge.example/final", wantErr: "HTTPS-to-HTTP"},
		{name: "userinfo", from: "https://forge.example/start", to: "https://user:secret@forge.example/final", wantErr: "URL credentials"},
		{name: "origin userinfo", from: "https://user:secret@forge.example/start", to: "https://forge.example/final", wantErr: "URL credentials"},
		{name: "non HTTP scheme", from: "https://forge.example/start", to: "ftp://forge.example/final", wantErr: "non-HTTP(S)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			from := mustParseRedirectURL(t, tt.from)
			to := mustParseRedirectURL(t, tt.to)
			via := []*http.Request{{URL: from}}
			err := sameHostRedirect(&http.Request{URL: to}, via)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("sameHostRedirect() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("sameHostRedirect() error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestSameHostRedirectPolicyKeepsRedirectLimit(t *testing.T) {
	requestURL := mustParseRedirectURL(t, "https://forge.example/final")
	via := make([]*http.Request, 10)
	for i := range via {
		via[i] = &http.Request{URL: mustParseRedirectURL(t, "https://forge.example/previous")}
	}
	if err := sameHostRedirect(&http.Request{URL: requestURL}, via); err == nil || !strings.Contains(err.Error(), "10 redirects") {
		t.Fatalf("sameHostRedirect() error = %v, want redirect-limit error", err)
	}
}

func TestAIAndForgeClientsEnforceRedirectPolicyWithoutLosingCredentials(t *testing.T) {
	t.Setenv("KB_AI_ALLOW_PRIVATE", "")
	t.Setenv("KB_FORGE_ALLOW_PRIVATE", "")
	clients := map[string]*http.Client{
		"AI":    newAIClient(),
		"forge": newForgeClient(),
	}
	for name, client := range clients {
		t.Run(name, func(t *testing.T) {
			t.Cleanup(client.CloseIdleConnections)
			assertRedirectRequest(t, client, "https://upstream.example/start", "https://upstream.example/final", 2)
			assertRedirectRequest(t, client, "http://upstream.example/start", "https://upstream.example/final", 2)
			assertRedirectRequest(t, client, "https://upstream.example/start", "http://upstream.example/final", 1)
			assertRedirectRequest(t, client, "https://upstream.example/start", "https://other.example/final", 1)
			assertRedirectRequest(t, client, "https://upstream.example/start", "https://user:secret@upstream.example/final", 1)
			assertRedirectRequest(t, client, "https://upstream.example/start", "ftp://upstream.example/final", 1)
		})
	}
}

func assertRedirectRequest(t *testing.T, client *http.Client, from, to string, wantRequests int) {
	t.Helper()
	requests := 0
	client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		if requests == 1 {
			return &http.Response{
				StatusCode: http.StatusTemporaryRedirect,
				Header:     http.Header{"Location": []string{to}},
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    req,
			}, nil
		}
		if got := req.Header.Get("Authorization"); got != "Bearer secret" {
			t.Fatalf("redirected Authorization = %q, want bearer token preserved", got)
		}
		if got := req.Header.Get("X-KB-User"); got != "alice" {
			t.Fatalf("redirected X-KB-User = %q, want identity header preserved", got)
		}
		return &http.Response{
			StatusCode: http.StatusNoContent,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    req,
		}, nil
	})
	req, err := http.NewRequest(http.MethodGet, from, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("X-KB-User", "alice")
	response, err := client.Do(req)
	if response != nil {
		_ = response.Body.Close()
	}
	if wantRequests == 2 && err != nil {
		t.Fatalf("allowed redirect error = %v", err)
	}
	if wantRequests == 1 && err == nil {
		t.Fatal("rejected redirect returned no error")
	}
	if requests != wantRequests {
		t.Fatalf("transport requests = %d, want %d", requests, wantRequests)
	}
}

func mustParseRedirectURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
