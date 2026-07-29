package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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
