package forge

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/ai"
	"github.com/RandomCodeSpace/kb/internal/store"
)

func TestForgeRequestFailuresAreUsefulAndSilent(t *testing.T) {
	var logs bytes.Buffer
	old := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(old) })
	for _, tc := range []struct {
		status          int
		remaining, want string
	}{
		{401, "", "authentication"}, {403, "", "permission"}, {404, "", "not found"}, {429, "", "rate limit"}, {403, "0", "rate limit"},
	} {
		t.Run(fmt.Sprint(tc.status, tc.remaining), func(t *testing.T) {
			service := New(nil, nil, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Header: http.Header{"X-Ratelimit-Remaining": {tc.remaining}}, Body: io.NopCloser(strings.NewReader("secret-response"))}, nil
			})})
			ref := forgeRef{Kind: "github", Source: store.ForgeSource{BaseURL: "https://forge.example"}, Project: "owner/repo", Issue: 1, pat: "secret-token"}
			_, err := service.fetchIssue(context.Background(), ref)
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), fmt.Sprint(tc.status)) || strings.Contains(err.Error(), "secret") {
				t.Errorf("issue error = %v", err)
			}
			ref.Issue = 0
			_, _, _, _, err = service.fetchIssues(context.Background(), ref, 20)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("first page error = %v", err)
			}
		})
	}
	if logs.Len() != 0 {
		t.Fatalf("forge wrote terminal logs: %q", logs.String())
	}
}

func TestForgePageBodyLimit(t *testing.T) {
	for _, size := range []int{2 << 20, (2 << 20) + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			service := New(nil, nil, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(strings.Repeat("x", size)))}, nil
			})})
			response, err := service.forgeGet(context.Background(), forgeRef{Kind: "github"}, "https://forge.example", "/issues", nil)
			if size > 2<<20 {
				if err == nil || !strings.Contains(err.Error(), "2 MiB") {
					t.Fatalf("oversize error = %v", err)
				}
			} else if err != nil || len(response.body) != size {
				t.Fatalf("boundary size=%d err=%v", len(response.body), err)
			}
		})
	}
}

func TestForgeRateLimitPreservesFetchedIssues(t *testing.T) {
	calls := 0
	service := New(nil, nil, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return &http.Response{StatusCode: 200, Header: http.Header{"Link": {`<next>; rel="next"`}}, Body: io.NopCloser(strings.NewReader(`[{"number":1,"title":"one"}]`))}, nil
		}
		return &http.Response{StatusCode: 429, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
	})})
	issues, _, truncated, note, err := service.fetchIssuePages(context.Background(), forgeRef{Kind: "github"}, "https://forge.example", "/issues", url.Values{}, 20)
	if err != nil || len(issues) != 1 || !truncated || !strings.Contains(note, "rate limited") {
		t.Fatalf("issues=%d truncated=%v note=%q err=%v", len(issues), truncated, note, err)
	}
}

func TestForgeRefusesPlainHTTPToken(t *testing.T) {
	for _, tc := range []struct {
		base    string
		allowed bool
	}{
		{"http://forge.example", false}, {"http://192.168.1.2", false}, {"https://forge.example", true}, {"http://127.0.0.1:1234", true}, {"http://[::1]:1234", true}, {"http://localhost:1234", true}, {"http://localhost.evil.example", false},
	} {
		t.Run(tc.base, func(t *testing.T) {
			checkForgeTokenTransport(t, tc.base, tc.allowed)
		})
	}
}

func checkForgeTokenTransport(t *testing.T, base string, allowed bool) {
	t.Helper()
	st := testStore(t)
	hits := 0
	service := New(st, nil, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		hits++
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("[]"))}, nil
	})})
	token := "secret-token"
	_, err := service.SaveSource("alice", "primary", "gitlab", &base, &token)
	if (err == nil) != allowed {
		t.Errorf("save err=%v allowed=%v", err, allowed)
	}
	if err != nil && (!strings.Contains(err.Error(), "HTTPS") || strings.Contains(err.Error(), token)) {
		t.Errorf("unsafe error %v", err)
	}
	if !allowed {
		sources, err := st.ForgeSources("alice")
		if err != nil || len(sources) != 0 {
			t.Fatalf("rejected save persisted a source: count=%d err=%v", len(sources), err)
		}
	}
	err = service.Probe(context.Background(), "alice", ForgeProbeConfig{Name: "primary", Kind: "gitlab", BaseURL: base, Token: token})
	if (err == nil) != allowed {
		t.Errorf("probe err=%v allowed=%v", err, allowed)
	}
	_, err = service.forgeGet(context.Background(), forgeRef{Kind: "gitlab", pat: token}, base, "/issues", nil)
	if (err == nil) != allowed {
		t.Errorf("fetch err=%v allowed=%v", err, allowed)
	}
	if !allowed && hits != 0 {
		t.Errorf("sent %d authenticated HTTP requests", hits)
	}
	if _, err := service.SaveSource("alice", "public", "gitlab", &base, nil); err != nil {
		t.Errorf("public HTTP save: %v", err)
	}
}

func TestForgeRefusesStoredPlainHTTPToken(t *testing.T) {
	st := testStore(t)
	assertStoredSource := func(name, wantBase, wantToken string) {
		t.Helper()
		kind, storedBase, storedToken, err := st.ForgePAT("alice", name)
		if err != nil {
			t.Fatalf("read source %q: %v", name, err)
		}
		if kind != "gitlab" || storedBase != wantBase || storedToken != wantToken {
			t.Fatalf("source %q changed: kind=%q base=%q tokenMatches=%v", name, kind, storedBase, storedToken == wantToken)
		}
	}
	base, token := "http://forge.example", "secret-token"
	if _, err := st.SetForgeSource("alice", "primary", "gitlab", &base, &token); err != nil {
		t.Fatal(err)
	}
	service := New(st, nil, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("sent stored token over HTTP"); return nil, nil })})
	if _, err := service.SaveSource("alice", "primary", "gitlab", nil, nil); err == nil {
		t.Error("saved existing unsafe token")
	}
	assertStoredSource("primary", base, token)
	if err := service.Probe(context.Background(), "alice", ForgeProbeConfig{Name: "primary", Saved: true}); err == nil {
		t.Error("probed existing unsafe token")
	}
	if _, err := service.SaveSource("alice", "primary", "gitlab", nil, ptr("")); err != nil {
		t.Fatalf("clearing token: %v", err)
	}
	assertStoredSource("primary", base, "")
	if _, err := service.SaveSource("alice", "primary", "gitlab", nil, &token); err == nil {
		t.Error("added token to existing HTTP source")
	}
	assertStoredSource("primary", base, "")
	if _, err := service.SaveSource("alice", "secure", "gitlab", ptr("https://forge.example"), &token); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SaveSource("alice", "secure", "github", &base, ptr("replacement-token")); err == nil {
		t.Error("replaced secure source with an authenticated HTTP source")
	}
	assertStoredSource("secure", "https://forge.example", token)
	if cleared, err := service.SaveSource("alice", "secure", "gitlab", &base, nil); err != nil || !cleared {
		t.Fatalf("endpoint change should clear token: cleared=%v err=%v", cleared, err)
	}
}

func TestDriftSummaryReportsSanitizedAIFailures(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"secret-provider-body secret-api-key"}}`)
	}))
	defer upstream.Close()
	st := testStore(t)
	base, model, key := upstream.URL+"/v1", "test-model", "secret-api-key"
	if _, err := st.SetAISettings("alice", &base, &model, &key); err != nil {
		t.Fatal(err)
	}
	service := New(st, ai.NewRunner(st, "", upstream.Client(), nil), nil)
	for _, canceled := range []bool{false, true} {
		t.Run(fmt.Sprint(canceled), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if canceled {
				cancel()
			}
			summary := service.driftSummary(ctx, "alice", store.NewImportBaseline("old", "body", "old-at"), store.NewImportBaseline("new", "body", "new-at"))
			if !strings.HasPrefix(summary, "AI summary unavailable: ") || strings.Contains(summary, "secret-") {
				t.Fatalf("summary=%q", summary)
			}
		})
	}
}

func TestDriftSummarySanitizesModelConfigurationFailure(t *testing.T) {
	st := testStore(t)
	base, key := "https://ai.example/v1", "secret-api-key"
	if _, err := st.SetAISettings("alice", &base, nil, &key); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("unconfigured model sent an AI request")
		return nil, nil
	})}
	service := New(st, ai.NewRunner(st, "", client, nil), nil)
	summary := service.driftSummary(t.Context(), "alice", store.NewImportBaseline("old", "body", "old-at"), store.NewImportBaseline("new", "body", "new-at"))
	if summary != "AI summary unavailable" {
		t.Fatalf("unsafe or missing configuration failure: %q", summary)
	}
}

func TestForgeRejectsMalformedAuthenticatedRequestBeforeTransport(t *testing.T) {
	service := New(nil, nil, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("malformed target sent an authenticated request")
		return nil, nil
	})})
	_, err := service.forgeGet(t.Context(), forgeRef{Kind: "gitlab", pat: "secret-token"}, "http://[", "/issues", nil)
	if err == nil || err.Error() != "invalid forge base URL" {
		t.Fatalf("malformed target error = %v", err)
	}
}
