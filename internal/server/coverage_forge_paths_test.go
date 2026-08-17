package server

import (
	"context"
	"crypto/rsa"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/kb/internal/store"
)

func forgePathResponse(status int, body string, header http.Header) *http.Response {
	if header == nil {
		header = make(http.Header)
	}
	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

type forgeFailWriter struct{ header http.Header }

func (w *forgeFailWriter) Header() http.Header     { return w.header }
func (*forgeFailWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }
func (*forgeFailWriter) WriteHeader(int)           {}

func TestFetchIssueLoadsGitLabAndGitHubDiscussion(t *testing.T) {
	tests := []struct {
		name string
		ref  forgeRef
		rt   roundTripperFunc
		want forgeIssue
	}{
		{
			name: "gitlab",
			ref: forgeRef{
				Kind: "gitlab", Project: "group/project", Issue: 7, pat: "gl-token",
				Source: store.ForgeSource{Name: "gitlab", Kind: "gitlab", BaseURL: "https://gitlab.example"},
			},
			rt: func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("PRIVATE-TOKEN") != "gl-token" {
					t.Fatalf("gitlab token = %q", r.Header.Get("PRIVATE-TOKEN"))
				}
				if strings.HasSuffix(r.URL.Path, "/notes") {
					return forgePathResponse(http.StatusOK, `[
						{"body":"human note","system":false},
						{"body":"system note","system":true}
					]`, nil), nil
				}
				return forgePathResponse(http.StatusOK, `{
					"iid":7,"title":"GitLab issue","description":"body",
					"web_url":"https://gitlab.example/group/project/-/issues/7",
					"labels":["bug"]
				}`, nil), nil
			},
			want: forgeIssue{
				Ref: "gitlab#7", Title: "GitLab issue", Body: "body",
				URL:    "https://gitlab.example/group/project/-/issues/7",
				Labels: []string{"bug"}, Comments: []string{"human note"},
			},
		},
		{
			name: "github",
			ref: forgeRef{
				Kind: "github", Project: "owner/repo", Issue: 9, pat: "gh-token",
				Source: store.ForgeSource{Name: "github", Kind: "github", BaseURL: "https://github.com"},
			},
			rt: func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("Authorization") != "Bearer gh-token" {
					t.Fatalf("github authorization = %q", r.Header.Get("Authorization"))
				}
				if strings.HasSuffix(r.URL.Path, "/comments") {
					return forgePathResponse(http.StatusOK, `[{"body":"first"},{"body":"second"}]`, nil), nil
				}
				return forgePathResponse(http.StatusOK, `{
					"number":9,"title":"GitHub issue","body":"body",
					"html_url":"https://github.com/owner/repo/issues/9",
					"labels":[{"name":"feature"},{"name":""}]
				}`, nil), nil
			},
			want: forgeIssue{
				Ref: "github#9", Title: "GitHub issue", Body: "body",
				URL:    "https://github.com/owner/repo/issues/9",
				Labels: []string{"feature"}, Comments: []string{"first", "second"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &server{forgeClient: &http.Client{Transport: tt.rt}}
			got, err := s.fetchIssue(context.Background(), tt.ref)
			if err != nil {
				t.Fatalf("fetchIssue: %v", err)
			}
			if got.Ref != tt.want.Ref || got.Title != tt.want.Title || got.Body != tt.want.Body || got.URL != tt.want.URL {
				t.Fatalf("issue = %+v, want %+v", got, tt.want)
			}
			if strings.Join(got.Labels, ",") != strings.Join(tt.want.Labels, ",") ||
				strings.Join(got.Comments, ",") != strings.Join(tt.want.Comments, ",") {
				t.Fatalf("labels/comments = %v/%v, want %v/%v", got.Labels, got.Comments, tt.want.Labels, tt.want.Comments)
			}
		})
	}
}

func TestFetchIssuesResolvesGitLabMilestoneAndPaginates(t *testing.T) {
	var listCalls int
	s := &server{forgeClient: &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/milestones/7"):
			return forgePathResponse(http.StatusOK, `{"title":"Release 1"}`, nil), nil
		case strings.HasSuffix(r.URL.Path, "/issues"):
			listCalls++
			if got := r.URL.Query().Get("milestone"); got != "Release 1" {
				t.Fatalf("milestone query = %q", got)
			}
			if listCalls == 1 {
				return forgePathResponse(http.StatusOK, `[
					{"iid":1,"title":"one","web_url":"https://gitlab.example/1"},
					{"iid":2,"title":"two","web_url":"https://gitlab.example/2"}
				]`, http.Header{"X-Total": {"3"}, "X-Next-Page": {"2"}}), nil
			}
			if got := r.URL.Query().Get("page"); got != "2" {
				t.Fatalf("page query = %q", got)
			}
			return forgePathResponse(http.StatusOK, `[
				{"iid":3,"title":"three","web_url":"https://gitlab.example/3"}
			]`, http.Header{"X-Total": {"3"}}), nil
		default:
			t.Fatalf("unexpected forge request %s", r.URL.String())
			return nil, nil
		}
	})}}
	ref := forgeRef{
		Kind: "gitlab", Project: "group/project", Milestone: 7,
		Source: store.ForgeSource{Name: "gitlab", Kind: "gitlab", BaseURL: "https://gitlab.example"},
	}
	issues, total, truncated, note, err := s.fetchIssues(context.Background(), ref, 10)
	if err != nil {
		t.Fatalf("fetchIssues: %v", err)
	}
	if len(issues) != 3 || total != 3 || truncated || note != "" || listCalls != 2 {
		t.Fatalf("issues=%d total=%d truncated=%v note=%q calls=%d", len(issues), total, truncated, note, listCalls)
	}
}

func TestFetchIssuesGitHubTruncationAndFiltering(t *testing.T) {
	ref := forgeRef{
		Kind: "github", Project: "owner/repo",
		Source: store.ForgeSource{Name: "github", Kind: "github", BaseURL: "https://github.com"},
	}
	t.Run("batch truncation filters pull requests", func(t *testing.T) {
		s := &server{forgeClient: &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			if !strings.HasSuffix(r.URL.Path, "/issues") {
				t.Fatalf("unexpected path %s", r.URL.Path)
			}
			return forgePathResponse(http.StatusOK, `[
				{"number":1,"title":"issue one","html_url":"https://github.com/owner/repo/issues/1"},
				{"number":2,"title":"pull request","pull_request":{"url":"https://api.github.com/pr/2"}},
				{"number":3,"title":"issue three","html_url":"https://github.com/owner/repo/issues/3"}
			]`, http.Header{"Link": {`<next>; rel="next"`}}), nil
		})}}
		issues, total, truncated, note, err := s.fetchIssues(context.Background(), ref, 1)
		if err != nil {
			t.Fatalf("fetchIssues: %v", err)
		}
		if len(issues) != 1 || total != 2 || !truncated || note != "" || issues[0].Ref != "github#1" {
			t.Fatalf("issues=%+v total=%d truncated=%v note=%q", issues, total, truncated, note)
		}
	})

}

func TestFetchIssueAndCommentsRejectInvalidForgePayloads(t *testing.T) {
	refs := []forgeRef{
		{Kind: "gitlab", Project: "group/project", Issue: 1, Source: store.ForgeSource{Name: "gl", Kind: "gitlab", BaseURL: "https://gitlab.example"}},
		{Kind: "github", Project: "owner/repo", Issue: 1, Source: store.ForgeSource{Name: "gh", Kind: "github", BaseURL: "https://github.com"}},
	}
	for _, ref := range refs {
		t.Run(ref.Kind, func(t *testing.T) {
			responses := []string{"{", func() string {
				if ref.Kind == "github" {
					return `{"number":1,"pull_request":{"url":"x"}}`
				}
				return `{"iid":1}`
			}()}
			for _, body := range responses {
				s := &server{forgeClient: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
					return forgePathResponse(http.StatusOK, body, nil), nil
				})}}
				_, _, _, err := s.fetchIssueSnapshot(context.Background(), ref)
				if body == "{" || ref.Kind == "github" {
					if err == nil {
						t.Fatalf("fetchIssueSnapshot(%q) returned nil error", body)
					}
				}
			}

			s := &server{forgeClient: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return forgePathResponse(http.StatusOK, `{`, nil), nil
			})}}
			if _, err := s.fetchForgeComments(context.Background(), ref, "https://forge.example", "/issue/1"); err == nil {
				t.Fatal("fetchForgeComments accepted malformed JSON")
			}
		})
	}

	invalid := forgeRef{Kind: "github", Project: "owner/repo", Issue: 0, Source: store.ForgeSource{Name: "gh", Kind: "github", BaseURL: "https://github.com"}}
	if _, _, _, err := (&server{}).fetchIssueSnapshot(context.Background(), invalid); err == nil {
		t.Fatal("fetchIssueSnapshot accepted a non-positive issue")
	}
}

func TestForgeResidualControlFlow(t *testing.T) {
	t.Run("single issue list path", func(t *testing.T) {
		ref := forgeRef{Kind: "github", Project: "owner/repo", Issue: 4, Source: store.ForgeSource{Name: "gh", Kind: "github", BaseURL: "https://github.com"}}
		s := &server{forgeClient: &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			if strings.HasSuffix(r.URL.Path, "/comments") {
				return forgePathResponse(http.StatusOK, `[]`, nil), nil
			}
			return forgePathResponse(http.StatusOK, `{"number":4,"title":"one"}`, nil), nil
		})}}
		issues, total, truncated, note, err := s.fetchIssues(context.Background(), ref, 0)
		if err != nil || len(issues) != 1 || total != 1 || truncated || note != "" {
			t.Fatalf("fetchIssues = %#v, %d, %v, %q, %v", issues, total, truncated, note, err)
		}
	})

	t.Run("comment status propagates", func(t *testing.T) {
		ref := forgeRef{Kind: "github", Project: "owner/repo", Issue: 5, Source: store.ForgeSource{Name: "gh", Kind: "github", BaseURL: "https://github.com"}}
		s := &server{forgeClient: &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			if strings.HasSuffix(r.URL.Path, "/comments") {
				return forgePathResponse(http.StatusInternalServerError, `no`, nil), nil
			}
			return forgePathResponse(http.StatusOK, `{"number":5,"title":"one"}`, nil), nil
		})}}
		if _, err := s.fetchIssue(context.Background(), ref); err == nil {
			t.Fatal("fetchIssue accepted failed comment request")
		}
	})

	t.Run("invalid snapshots and lists", func(t *testing.T) {
		invalidBase := forgeRef{Kind: "github", Project: "owner/repo", Issue: 1, Source: store.ForgeSource{Name: "gh", Kind: "github", BaseURL: ":"}}
		if _, _, _, err := (&server{}).fetchIssueSnapshot(context.Background(), invalidBase); err == nil {
			t.Fatal("snapshot accepted invalid base")
		}
		invalidProject := forgeRef{Kind: "github", Project: "owner", Issue: 1, Source: store.ForgeSource{Name: "gh", Kind: "github", BaseURL: "https://github.com"}}
		if _, _, _, err := (&server{}).fetchIssueSnapshot(context.Background(), invalidProject); err == nil {
			t.Fatal("snapshot accepted invalid project")
		}
		invalidList := invalidBase
		invalidList.Issue = 0
		if _, _, _, _, err := (&server{}).fetchIssues(context.Background(), invalidList, 1); err == nil {
			t.Fatal("list accepted invalid base")
		}
	})

	t.Run("github milestone resolution and errors", func(t *testing.T) {
		ref := forgeRef{Kind: "github", Project: "owner/repo", Milestone: 3, Source: store.ForgeSource{Name: "gh", Kind: "github", BaseURL: "https://github.com"}}
		for _, tt := range []struct {
			name   string
			status int
			body   string
			fail   bool
		}{
			{name: "success", status: http.StatusOK, body: `{"number":8}`},
			{name: "bad status", status: http.StatusNotFound, body: `{}`, fail: true},
			{name: "bad payload", status: http.StatusOK, body: `{"number":0}`, fail: true},
		} {
			t.Run(tt.name, func(t *testing.T) {
				s := &server{forgeClient: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
					return forgePathResponse(tt.status, tt.body, nil), nil
				})}}
				path, query, err := s.forgeIssuesList(context.Background(), ref, "https://api.github.com")
				if tt.fail {
					if err == nil {
						t.Fatal("forgeIssuesList returned nil error")
					}
					return
				}
				if err != nil || path != "/repos/owner/repo/issues" || query.Get("milestone") != "8" {
					t.Fatalf("path=%q query=%v err=%v", path, query, err)
				}
			})
		}
	})

	if validForgeSourceName("") || validForgeSourceName(strings.Repeat("a", 65)) {
		t.Fatal("invalid forge source name accepted")
	}
	if _, err := forgeAPIBase("other", "https://example.com"); err == nil {
		t.Fatal("invalid forge kind accepted")
	}
	if got := importRefKind(forgeRef{Issue: 1}); got != "issue" {
		t.Fatalf("issue kind = %q", got)
	}
	if got := importRefKind(forgeRef{Milestone: 1}); got != "milestone" {
		t.Fatalf("milestone kind = %q", got)
	}
	writeJSON(&forgeFailWriter{header: make(http.Header)}, map[string]bool{"ok": true})
}

func TestRejectedHandlerBodiesAndBoardJSONEdges(t *testing.T) {
	s := &server{store: newTestStore(t)}
	handlers := []struct {
		name string
		fn   func(http.ResponseWriter, *http.Request, string)
	}{
		{"tombstone", s.handleTombstone},
		{"import preview", s.handleImportPreview},
		{"import links", s.handleImportLinks},
		{"import provenance", s.handleImportProvenance},
		{"import drift", s.handleImportDrift},
		{"import drift accept", s.handleImportDriftAccept},
		{"put integration", s.handlePutIntegration},
		{"test integration", s.handleTestIntegration},
		{"ai test", s.handleAITest},
	}
	for _, tt := range handlers {
		t.Run(tt.name+" oversized", func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("x", maxBodyBytes+1)))
			r.SetPathValue("name", "primary")
			w := httptest.NewRecorder()
			tt.fn(w, r, "user")
			if w.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("status = %d", w.Code)
			}
		})
	}
	for _, tt := range []struct {
		name string
		fn   func(http.ResponseWriter, *http.Request, string)
	}{
		{"tombstone", s.handleTombstone},
		{"import preview", s.handleImportPreview},
		{"import drift", s.handleImportDrift},
		{"import drift accept", s.handleImportDriftAccept},
	} {
		t.Run(tt.name+" malformed", func(t *testing.T) {
			w := httptest.NewRecorder()
			tt.fn(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{`)), "user")
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d", w.Code)
			}
		})
	}

	invalidBoards := []string{
		`[]`,
		`{"board":`,
		`{"board":"x","board":"y","task_ids":[]}`,
		`{"board":null,"task_ids":[]}`,
		`{"board":"x","task_ids":`,
		`{"board":"x","task_ids":null}`,
		`{"board":"x","task_ids":{}}`,
		`{"board":"x","other":[]}`,
		`{"board":"x"}`,
		`{"board":"x","task_ids":[]} {}`,
	}
	for _, body := range invalidBoards {
		if _, _, err := parseBoardJSONPut([]byte(body)); err == nil {
			t.Fatalf("parseBoardJSONPut accepted %q", body)
		}
	}
}

func TestRemainingPureAndNetworkErrorBranches(t *testing.T) {
	if got := normalizeGuardHost(" [::1]. "); got != "::1" {
		t.Fatalf("normalized host = %q", got)
	}
	transport := guardedTransport(nil, false)
	if _, err := transport.DialContext(context.Background(), "tcp", "invalid-address"); err == nil {
		t.Fatal("guarded transport accepted invalid dial address")
	}
	draft := coerceDraftMap(map[string]any{"title": "x", "prio": "3", "due": "2026-01-01"})
	if draft.Prio != 3 || draft.Due != "2026-01-01" {
		t.Fatalf("draft = %+v", draft)
	}
	if got := forgeIssueADR(forgeIssue{Title: "x", Comments: []string{"one"}}); !strings.Contains(got, "- one") {
		t.Fatalf("ADR = %q", got)
	}
	comments := make([]string, 11)
	for i := range comments {
		comments[i] = "comment"
	}
	if _, count := packImportIssues([]forgeIssue{{Title: "x", Comments: comments}}); count != 1 {
		t.Fatalf("packed count = %d", count)
	}

	badKind := store.ForgeSource{Name: "bad", Kind: "other", BaseURL: "https://forge.example"}
	if _, err := parseForgeRef([]store.ForgeSource{badKind}, "bad", "https://forge.example/group/project"); err == nil {
		t.Fatal("parseForgeRef accepted invalid kind")
	}
	if _, err := parseForgeRef(nil, "", "ftp://forge.example/project"); err == nil {
		t.Fatal("parseForgeRef accepted invalid scheme")
	}
	gl := store.ForgeSource{Name: "gl", Kind: "gitlab", BaseURL: "https://gitlab.example"}
	ref, err := parseForgeRef([]store.ForgeSource{gl}, "gl", "https://gitlab.example/group/milestones/7")
	if err != nil || ref.Milestone != 7 {
		t.Fatalf("milestone ref = %+v, err=%v", ref, err)
	}

	requestRef := forgeRef{Kind: "github", Project: "owner/repo", Source: store.ForgeSource{Name: "gh"}}
	if _, err := (&server{}).forgeGet(context.Background(), requestRef, "\x7f", "", nil); err == nil {
		t.Fatal("forgeGet accepted invalid endpoint")
	}

	listRef := forgeRef{Kind: "github", Project: "owner/repo", Source: store.ForgeSource{Name: "gh", Kind: "github", BaseURL: "https://github.com"}}
	for _, tt := range []struct {
		name   string
		status int
		body   string
		link   string
		fail   bool
	}{
		{name: "bad status", status: http.StatusInternalServerError, body: `[]`, fail: true},
		{name: "malformed", status: http.StatusOK, body: `{`, fail: true},
		{name: "exact max", status: http.StatusOK, body: `[{"number":1,"title":"one"}]`, link: `<next>; rel="next"`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := &server{forgeClient: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return forgePathResponse(tt.status, tt.body, http.Header{"Link": {tt.link}}), nil
			})}}
			issues, _, truncated, _, err := s.fetchIssues(context.Background(), listRef, 1)
			if tt.fail {
				if err == nil {
					t.Fatal("fetchIssues returned nil error")
				}
				return
			}
			if err != nil || len(issues) != 1 || !truncated {
				t.Fatalf("issues=%v truncated=%v err=%v", issues, truncated, err)
			}
		})
	}

	s := &server{authenticate: func(*http.Request) (string, error) { return "../bad", nil }}
	w := httptest.NewRecorder()
	s.withAuth(func(http.ResponseWriter, *http.Request, string) { t.Fatal("unexpected authenticated call") })(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid identity status = %d", w.Code)
	}

	cache := &jwksCache{
		url: "https://jwks.invalid",
		client: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("offline")
		})},
		keys:    map[string]*rsa.PublicKey{"cached": {N: rsaKeyN(), E: 3}},
		fetched: time.Now().Add(-2 * time.Hour),
	}
	if key, err := cache.key("cached"); err != nil || key == nil {
		t.Fatalf("cached JWKS fallback = %v, %v", key, err)
	}
	if _, err := rsaKeyFromJWK("AQ", "AAM"); err != nil {
		t.Fatalf("leading-zero exponent rejected: %v", err)
	}

	t.Run("remaining request failures", func(t *testing.T) {
		ref := forgeRef{Kind: "github", Project: "owner/repo", Issue: 2, Source: store.ForgeSource{Name: "gh", Kind: "github", BaseURL: "https://github.com"}}
		offline := &server{forgeClient: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("offline")
		})}}
		if _, _, _, _, err := offline.fetchIssues(context.Background(), ref, 1); err == nil {
			t.Fatal("single issue fetch accepted transport failure")
		}
		ref.Issue, ref.Milestone = 0, 3
		if _, _, err := offline.forgeIssuesList(context.Background(), ref, "https://api.github.com"); err == nil {
			t.Fatal("milestone fetch accepted transport failure")
		}

		gitlab := forgeRef{Kind: "gitlab", Project: "group/project", Milestone: 3, Source: store.ForgeSource{Name: "gl", Kind: "gitlab", BaseURL: "https://gitlab.example"}}
		malformed := &server{forgeClient: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return forgePathResponse(http.StatusOK, `{`, nil), nil
		})}}
		if _, _, err := malformed.forgeIssuesList(context.Background(), gitlab, "https://gitlab.example/api/v4"); err == nil {
			t.Fatal("GitLab milestone accepted malformed payload")
		}
	})

	badJWKS := &jwksCache{
		url: "https://jwks.invalid",
		client: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return forgePathResponse(http.StatusOK, `{"keys":[{"kty":"RSA","kid":"bad","n":"!","e":"AQAB"}]}`, nil), nil
		})},
	}
	if err := badJWKS.fetchLocked(); err == nil {
		t.Fatal("JWKS accepted unusable RSA key")
	}

	badType := httptest.NewRequest(http.MethodPost, "/api/board", strings.NewReader("x"))
	badType.Header.Set("Content-Type", `application/json; broken`)
	if contentTypeAllowed(badType) {
		t.Fatal("malformed content type accepted")
	}

	put := httptest.NewRequest(http.MethodPut, "/api/board", strings.NewReader("# Board"))
	put.Header.Set("Content-Type", "text/markdown")
	put.Header.Set("If-Match", "*")
	put.Header.Set("Idempotency-Key", "not-a-uuid")
	putResult := httptest.NewRecorder()
	(&server{store: newTestStore(t)}).handlePutBoard(putResult, put, "user")
	if putResult.Code != http.StatusBadRequest {
		t.Fatalf("invalid idempotency status = %d", putResult.Code)
	}
}

func rsaKeyN() *big.Int { return big.NewInt(3) }
