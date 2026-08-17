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
		if r.URL.EscapedPath() != "/api/v4/projects/group%2Fproject" || r.Header.Get("PRIVATE-TOKEN") != candidatePAT {
			t.Errorf("probe request = %s token=%q", r.URL.EscapedPath(), r.Header.Get("PRIVATE-TOKEN"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	prober := &ForgeProber{store: st, client: upstream.Client()}
	draft := ForgeProbeConfig{
		Name: "unsaved", Kind: "gitlab", BaseURL: upstream.URL,
		Project: "group/project", Token: candidatePAT,
	}
	if err := prober.Probe(context.Background(), "alice", draft); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if requests != 1 {
		t.Fatalf("probe requests = %d", requests)
	}
	if err := prober.Probe(context.Background(), "alice", ForgeProbeConfig{
		Name: "primary", Kind: "gitlab", BaseURL: upstream.URL,
		Project: "group/project", Token: candidatePAT, Saved: true,
	}); err != nil {
		t.Fatalf("saved candidate Probe: %v", err)
	}
	if requests != 2 {
		t.Fatalf("saved candidate probe requests = %d", requests)
	}
	if kind, base, pat, err := st.ForgePAT("alice", "primary"); err != nil || kind != "gitlab" || base != storedBase || pat != storedPAT {
		t.Fatalf("probe changed store = %q/%q/%q, %v", kind, base, pat, err)
	}
	if sources, err := st.ForgeSources("alice"); err != nil || len(sources) != 1 || sources[0].Name != "primary" {
		t.Fatalf("probe changed source list = %+v, %v", sources, err)
	}

	if err := prober.Probe(context.Background(), "alice", ForgeProbeConfig{
		Name: "primary", Kind: "gitlab", BaseURL: upstream.URL, Saved: true,
	}); err == nil ||
		err.Error() != "enter the token to test a different endpoint" || requests != 2 {
		t.Fatalf("cross-origin stored token probe = %v requests=%d", err, requests)
	}
	if err := prober.Probe(context.Background(), "alice", ForgeProbeConfig{Name: "missing", Saved: true}); err == nil || err.Error() != "integration unavailable" {
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
	err := prober.Probe(ctx, "alice", ForgeProbeConfig{Name: "primary", Saved: true})
	if err == nil || err.Error() != connectionFailedMessage || strings.Contains(err.Error(), base) {
		t.Fatalf("cancelled probe error = %v", err)
	}

	prober.client = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("network exposed candidate-token")
	})}
	err = prober.Probe(context.Background(), "alice", ForgeProbeConfig{Name: "primary", Token: "candidate-token", Saved: true})
	if err == nil || err.Error() != connectionFailedMessage || strings.Contains(err.Error(), "candidate-token") {
		t.Fatalf("network probe error = %v", err)
	}
}
