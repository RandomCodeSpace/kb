package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestForgeProberUsesUnsavedValuesWithoutSaving(t *testing.T) {
	st := newTestStore(t)
	storedBase, storedPAT := "https://stored.example", "stored-token"
	if _, err := st.SetForgeSource("alice", "primary", "gitlab", &storedBase, &storedPAT); err != nil {
		t.Fatal(err)
	}

	const candidatePAT = "candidate-token"
	var requests int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/api/v4/version" || r.Header.Get("PRIVATE-TOKEN") != candidatePAT {
			t.Errorf("probe request = %s token=%q", r.URL.Path, r.Header.Get("PRIVATE-TOKEN"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	prober := &ForgeProber{store: st, client: upstream.Client()}
	if err := prober.Probe(context.Background(), "alice", "primary", upstream.URL, candidatePAT); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if requests != 1 {
		t.Fatalf("probe requests = %d", requests)
	}
	if kind, base, pat, err := st.ForgePAT("alice", "primary"); err != nil || kind != "gitlab" || base != storedBase || pat != storedPAT {
		t.Fatalf("probe changed store = %q/%q/%q, %v", kind, base, pat, err)
	}

	if err := prober.Probe(context.Background(), "alice", "primary", upstream.URL, ""); err == nil ||
		err.Error() != "enter the token to test a different endpoint" || requests != 1 {
		t.Fatalf("cross-origin stored token probe = %v requests=%d", err, requests)
	}
	if err := prober.Probe(context.Background(), "alice", "missing", "", ""); err == nil || err.Error() != "integration unavailable" {
		t.Fatalf("missing integration probe = %v", err)
	}
}

func TestForgeProberCollapsesNetworkFailuresAndHonorsCancellation(t *testing.T) {
	st := newTestStore(t)
	base := "https://forge.example"
	if _, err := st.SetForgeSource("alice", "primary", "github", &base, nil); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})}
	prober := &ForgeProber{store: st, client: client}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := prober.Probe(ctx, "alice", "primary", "", "")
	if err == nil || err.Error() != connectionFailedMessage || strings.Contains(err.Error(), base) {
		t.Fatalf("cancelled probe error = %v", err)
	}

	prober.client = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("network exposed candidate-token")
	})}
	err = prober.Probe(context.Background(), "alice", "primary", "", "candidate-token")
	if err == nil || err.Error() != connectionFailedMessage || strings.Contains(err.Error(), "candidate-token") {
		t.Fatalf("network probe error = %v", err)
	}
}
