package forge

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RandomCodeSpace/kb/internal/ai"
	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

type failingReadCloser struct {
	readErr  error
	closeErr error
}

func (f failingReadCloser) Read([]byte) (int, error) {
	if f.readErr == nil {
		return 0, io.EOF
	}
	return 0, f.readErr
}
func (f failingReadCloser) Close() error { return f.closeErr }

func testStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "kb.db"), []byte("test-secret"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestReferenceParsingAndPaths(t *testing.T) {
	sources := []store.ForgeSource{
		{Name: "github", Kind: "github", BaseURL: "https://github.com"},
		{Name: "gitlab", Kind: "gitlab", BaseURL: "https://gitlab.example/forge"},
	}
	tests := []struct {
		source, raw, kind, project string
		issue, milestone           int
	}{
		{"github", "owner/repo", "github", "owner/repo", 0, 0},
		{"github", "https://github.com/owner/repo/issues/7", "github", "owner/repo", 7, 0},
		{"github", "https://github.com/owner/repo/milestone/3", "github", "owner/repo", 0, 3},
		{"gitlab", "https://gitlab.example/forge/group/sub/project/-/issues/8", "gitlab", "group/sub/project", 8, 0},
		{"gitlab", "https://gitlab.example/forge/group/sub/project/-/milestones/4", "gitlab", "group/sub/project", 0, 4},
	}
	for _, test := range tests {
		ref, err := parseForgeRef(sources, test.source, test.raw)
		if err != nil || ref.Kind != test.kind || ref.Project != test.project || ref.Issue != test.issue || ref.Milestone != test.milestone {
			t.Errorf("parse %q = %+v, %v", test.raw, ref, err)
		}
	}
	invalid := []string{"", "ftp://github.com/owner/repo", "https://evil.example/owner/repo", "https://github.com/owner", "https://github.com/owner/repo/issues/0", "https://github.com/owner/repo/issues/x"}
	for _, raw := range invalid {
		if _, err := parseForgeRef(sources, "github", raw); err == nil {
			t.Errorf("parseForgeRef(%q) succeeded", raw)
		}
	}
	if _, err := parseGitHubRef(sources[0], ""); err == nil {
		t.Fatal("empty GitHub path accepted")
	}
	if _, err := parseGitLabRef(sources[1], "group/-/x"); err == nil {
		t.Fatal("unscoped GitLab dash accepted")
	}
	if _, err := parseForgeRef(sources, "gitlab", "https://gitlab.example/forge"); err == nil {
		t.Fatal("source root accepted as a project")
	}
	github := forgeRef{Kind: "github", Project: "owner/repo", Issue: 7, Milestone: 3}
	gitlab := forgeRef{Kind: "gitlab", Project: "group/project", Issue: 8, Milestone: 4}
	for name, call := range map[string]func() (string, error){
		"gh project":   func() (string, error) { return forgeProjectPath(github) },
		"gh issue":     func() (string, error) { return forgeIssuePath(github) },
		"gh milestone": func() (string, error) { return forgeMilestonePath(github) },
		"gl project":   func() (string, error) { return forgeProjectPath(gitlab) },
		"gl issue":     func() (string, error) { return forgeIssuePath(gitlab) },
		"gl milestone": func() (string, error) { return forgeMilestonePath(gitlab) },
	} {
		if value, err := call(); err != nil || value == "" {
			t.Errorf("%s = %q, %v", name, value, err)
		}
	}
	for _, ref := range []forgeRef{{Kind: "other", Project: "x"}, {Kind: "github", Project: "owner"}} {
		if _, err := forgeProjectPath(ref); err == nil {
			t.Errorf("invalid project path %+v", ref)
		}
	}
	if id, err := forgeRefID("42"); err != nil || id != 42 {
		t.Fatalf("forgeRefID = %d, %v", id, err)
	}
	for _, raw := range []string{"", "0", "-1", "x"} {
		if _, err := forgeRefID(raw); err == nil {
			t.Errorf("forgeRefID(%q) accepted", raw)
		}
	}
}

func TestGuardedClientAndRedirectPolicy(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	t.Setenv("KB_FORGE_ALLOW_PRIVATE", "")
	blocked := NewHTTPClient()
	if blocked.Timeout != forgeTimeout {
		t.Fatalf("timeout = %v", blocked.Timeout)
	}
	if response, err := blocked.Get(upstream.URL); err == nil {
		response.Body.Close()
		t.Fatal("loopback reached without allowlist")
	}
	t.Setenv("KB_FORGE_ALLOW_PRIVATE", "127.0.0.1")
	allowed := NewHTTPClient()
	response, err := allowed.Get(upstream.URL)
	if err != nil {
		t.Fatalf("allowlisted get: %v", err)
	}
	response.Body.Close()
	if hits.Load() != 1 {
		t.Fatalf("hits = %d", hits.Load())
	}
	base, _ := url.Parse("https://forge.example/start")
	same, _ := http.NewRequest(http.MethodGet, "https://forge.example/next", nil)
	cross, _ := http.NewRequest(http.MethodGet, "https://evil.example/next", nil)
	down, _ := http.NewRequest(http.MethodGet, "http://forge.example/next", nil)
	credential, _ := http.NewRequest(http.MethodGet, "https://user@forge.example/next", nil)
	via := []*http.Request{{URL: base}}
	if sameHostRedirect(same, via) != nil || sameHostRedirect(cross, via) == nil || sameHostRedirect(down, via) == nil || sameHostRedirect(credential, via) == nil {
		t.Fatal("redirect policy mismatch")
	}
	ftp, _ := http.NewRequest(http.MethodGet, "ftp://forge.example/next", nil)
	ten := make([]*http.Request, 10)
	for index := range ten {
		ten[index] = &http.Request{URL: base}
	}
	if sameHostRedirect(ftp, via) == nil || sameHostRedirect(same, ten) == nil {
		t.Fatal("redirect bounds mismatch")
	}
	transport := guardedTransport(nil, false)
	if _, err := transport.DialContext(context.Background(), "tcp", "invalid"); err == nil {
		t.Fatal("invalid dial address accepted")
	}
	if got := normalizeGuardHost("[::1]"); got != "::1" {
		t.Fatalf("normalized host = %q", got)
	}
}

func TestFetchGitHubAndGitLabIssues(t *testing.T) {
	var githubAuth, gitlabAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.EscapedPath() {
		case "/api/v3/repos/owner/repo/issues/7":
			githubAuth = r.Header.Get("Authorization")
			_, _ = io.WriteString(w, `{"number":7,"title":"GitHub title","body":"body","html_url":"https://github.test/7","labels":[{"name":"bug"}]}`)
		case "/api/v3/repos/owner/repo/issues/7/comments":
			_, _ = io.WriteString(w, `[{"body":"one"},{"body":"two"}]`)
		case "/api/v4/projects/group%2Fproject/issues/8":
			gitlabAuth = r.Header.Get("PRIVATE-TOKEN")
			_, _ = io.WriteString(w, `{"iid":8,"title":"GitLab title","description":"body","web_url":"https://gitlab.test/8","labels":["feature"]}`)
		case "/api/v4/projects/group%2Fproject/issues/8/notes":
			_, _ = io.WriteString(w, `[{"body":"note","system":false},{"body":"ignored","system":true}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	service := New(testStore(t), nil, upstream.Client())
	github := forgeRef{Source: store.ForgeSource{Name: "gh", Kind: "github", BaseURL: upstream.URL}, Kind: "github", Project: "owner/repo", Issue: 7, pat: "gh-token"}
	gitlab := forgeRef{Source: store.ForgeSource{Name: "gl", Kind: "gitlab", BaseURL: upstream.URL}, Kind: "gitlab", Project: "group/project", Issue: 8, pat: "gl-token"}
	for _, ref := range []forgeRef{github, gitlab} {
		issue, err := service.fetchIssue(context.Background(), ref)
		if err != nil || issue.Title == "" || len(issue.Comments) != 1 && ref.Kind == "gitlab" || len(issue.Comments) != 2 && ref.Kind == "github" {
			t.Fatalf("fetch %+v = %+v, %v", ref, issue, err)
		}
	}
	if githubAuth != "Bearer gh-token" || gitlabAuth != "gl-token" {
		t.Fatalf("auth github=%q gitlab=%q", githubAuth, gitlabAuth)
	}
	for _, ref := range []forgeRef{{Kind: "other"}, {Kind: "github", Source: store.ForgeSource{BaseURL: ":"}, Project: "owner/repo", Issue: 1}} {
		if _, err := service.fetchIssue(context.Background(), ref); err == nil {
			t.Errorf("invalid fetch %+v succeeded", ref)
		}
	}
}

func TestFetchPaginationTruncationAndRateLimit(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		page := r.URL.Query().Get("page")
		if page == "" || page == "1" {
			w.Header().Set("X-Total", "3")
			w.Header().Set("Link", `<next>; rel="next"`)
			_, _ = io.WriteString(w, `[{"number":1,"title":"one"},{"number":2,"title":"two","pull_request":{}}]`)
			return
		}
		_, _ = io.WriteString(w, `[{"number":3,"title":"three"}]`)
	}))
	defer upstream.Close()
	service := New(testStore(t), nil, upstream.Client())
	ref := forgeRef{Source: store.ForgeSource{Name: "gh", Kind: "github", BaseURL: upstream.URL}, Kind: "github", Project: "owner/repo"}
	issues, total, truncated, note, err := service.fetchIssues(context.Background(), ref, 2)
	if err != nil || len(issues) != 2 || total != 2 || truncated || note != "" || calls.Load() != 2 {
		t.Fatalf("pages = %d/%d truncated=%t note=%q calls=%d err=%v", len(issues), total, truncated, note, calls.Load(), err)
	}
	state := forgeIssuePageState{}
	state.observeTotal("github", http.Header{"X-Total": []string{"bad"}})
	state.markRateLimited()
	if _, _, truncated, note, err := state.result(); err != nil || !truncated || note == "" {
		t.Fatalf("rate state = %t %q %v", truncated, note, err)
	}
	if forgeTotalHint(http.Header{}) != -1 || !forgeHasNextPage("gitlab", http.Header{"X-Next-Page": []string{"2"}}) {
		t.Fatal("pagination helpers")
	}
}

func TestFetchPaginationHasFiniteNoProgressCap(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Link", `<next>; rel="next"`)
		_, _ = io.WriteString(w, `[{"number":1,"title":"pull request","pull_request":{}}]`)
	}))
	defer upstream.Close()
	service := New(testStore(t), nil, upstream.Client())
	ref := forgeRef{Source: store.ForgeSource{Name: "gh", Kind: "github", BaseURL: upstream.URL}, Kind: "github", Project: "owner/repo"}
	issues, _, truncated, note, err := service.fetchIssues(context.Background(), ref, 1)
	if err != nil || len(issues) != 0 || !truncated || !strings.Contains(note, "pagination limit") || calls.Load() != maxForgePages {
		t.Fatalf("capped pages = issues=%d truncated=%t note=%q calls=%d err=%v", len(issues), truncated, note, calls.Load(), err)
	}
}

type scriptedAI struct {
	calls   int
	partial bool
}

func (s *scriptedAI) handler(w http.ResponseWriter, r *http.Request) {
	s.calls++
	w.Header().Set("Content-Type", "application/json")
	message := map[string]any{"role": "assistant", "content": "done"}
	if s.calls == 1 {
		message["content"] = ""
		message["tool_calls"] = []any{map[string]any{
			"id": "call-1", "type": "function",
			"function": map[string]any{"name": "propose_card", "arguments": `{"title":"Imported card","source":1,"prio":2}`},
		}}
	}
	finish := "stop"
	if s.partial && s.calls == 2 {
		finish = "length"
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finish}}})
}

func TestServicePreviewProvenanceAndDuplicateDefaults(t *testing.T) {
	forgeUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.EscapedPath() {
		case "/api/v3/repos/owner/repo/issues":
			_, _ = io.WriteString(w, `[{"number":93,"title":"Upstream issue","body":"body","html_url":"https://github.test/owner/repo/issues/93"}]`)
		case "/api/v3/repos/owner/repo/issues/93":
			_, _ = io.WriteString(w, `{"number":93,"title":"Upstream issue","body":"body","html_url":"https://github.test/owner/repo/issues/93"}`)
		case "/api/v3/repos/owner/repo/issues/93/comments":
			_, _ = io.WriteString(w, `[]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer forgeUpstream.Close()
	model := &scriptedAI{}
	aiUpstream := httptest.NewServer(http.HandlerFunc(model.handler))
	defer aiUpstream.Close()
	t.Setenv("KB_AI_ALLOW_PRIVATE", "1")
	st := testStore(t)
	base, token := forgeUpstream.URL, "forge-token"
	if _, err := st.SetForgeSource("alice", "primary", "github", &base, &token); err != nil {
		t.Fatal(err)
	}
	aiBase, aiModel, aiKey := aiUpstream.URL, "gpt-4o", "ai-key"
	if _, err := st.SetAISettings("alice", &aiBase, &aiModel, &aiKey); err != nil {
		t.Fatal(err)
	}
	runner := ai.NewRunner(st, "", aiUpstream.Client(), nil)
	service := New(st, runner, forgeUpstream.Client())
	if sources, err := service.Sources("alice"); err != nil || len(sources) != 1 {
		t.Fatalf("sources = %+v, %v", sources, err)
	}
	resolved, link, rawURL, err := service.ResolveIssueDocument(context.Background(), "alice", "primary", forgeUpstream.URL+"/owner/repo/issues/93")
	if err != nil || resolved.Title == "" || link != "github#93" || rawURL == "" {
		t.Fatalf("resolved = %+v %q %q, %v", resolved, link, rawURL, err)
	}
	preview, err := service.Preview(context.Background(), "alice", PreviewRequest{Source: "primary", Ref: "owner/repo", Max: 1})
	if err != nil || len(preview.Drafts) != 1 || preview.Drafts[0].ExternalKey == "" || preview.Drafts[0].Link != "github#93" || model.calls != 2 {
		t.Fatalf("preview = %+v calls=%d err=%v cause=%v", preview, model.calls, err, errors.Unwrap(err))
	}
	model.calls, model.partial = 0, true
	partial, err := service.Preview(context.Background(), "alice", PreviewRequest{Source: "primary", Ref: "owner/repo", Max: 1})
	if err != nil || len(partial.Drafts) != 1 || !strings.Contains(partial.Note, "stopped early") {
		t.Fatalf("partial preview = %+v, %v", partial, err)
	}
	model.partial = false
	draft := preview.Drafts[0]
	if err := service.RecordLinks("alice", "primary", []LinkInput{{ExternalKey: draft.ExternalKey, Link: draft.Link, URL: draft.URL, Title: draft.Title}}); err != nil {
		t.Fatal(err)
	}
	links, err := service.Provenance("alice", draft.Link)
	if err != nil || len(links) != 1 || links[0].ExternalKey != draft.ExternalKey {
		t.Fatalf("provenance = %+v, %v", links, err)
	}
	if _, err := st.AddTask("alice", board.Task{Title: draft.Title, Tags: append([]string(nil), draft.Tags...)}); err != nil {
		t.Fatal(err)
	}
	authorized, err := service.authorizeRef("alice", "primary", "owner/repo")
	if err != nil {
		t.Fatal(err)
	}
	issues := []forgeIssue{{Ref: draft.Link, Title: draft.Title}}
	duplicates, err := service.duplicates("alice", authorized, issues)
	if err != nil || duplicates[0] == nil || duplicates[0].Via != "link" {
		t.Fatalf("duplicates = %+v, %v", duplicates, err)
	}
	if _, err := st.AddTask("alice", board.Task{Title: "Fuzzy candidate"}); err != nil {
		t.Fatal(err)
	}
	duplicates, err = service.duplicates("alice", authorized, []forgeIssue{{Ref: "github#94", Title: "Fuzzy candidate"}})
	if err != nil || duplicates[0] == nil || duplicates[0].Via != "similar" {
		t.Fatalf("fuzzy duplicates = %+v, %v", duplicates, err)
	}
	if summary := service.driftSummary(context.Background(), "alice", store.NewImportBaseline("old", "body", "one"), store.NewImportBaseline("new", "body", "two")); summary != "done" {
		t.Fatalf("drift summary = %q", summary)
	}
	tooMany := make([]LinkInput, maxImportLinks+1)
	for _, call := range []func() error{
		func() error { return service.RecordLinks("alice", "primary", tooMany) },
		func() error { return service.RecordLinks("alice", "primary", []LinkInput{{ExternalKey: "key"}}) },
		func() error { return service.RecordLinks("alice", "missing", nil) },
	} {
		if err := call(); err == nil {
			t.Fatal("invalid link journal accepted")
		}
	}
	for _, call := range []func() error{
		func() error { return service.RecordLinks("alice", "", nil) },
		func() error { _, err := service.Provenance("alice", ""); return err },
		func() error { _, err := service.Provenance("alice", "missing"); return err },
	} {
		if err := call(); err == nil {
			t.Fatal("invalid provenance operation succeeded")
		}
	}
}

func TestPreviewDisclosesPromptPackLoss(t *testing.T) {
	large := strings.Repeat("x", maxImportIssueBodyBytes)
	issues := make([]map[string]any, 5)
	for index := range issues {
		issues[index] = map[string]any{"number": index + 1, "title": "issue", "body": large, "html_url": "https://github.test/issue"}
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/issues") {
			_ = json.NewEncoder(w).Encode(issues)
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()
	fake := &scriptedAI{}
	aiUpstream := httptest.NewServer(http.HandlerFunc(fake.handler))
	defer aiUpstream.Close()
	t.Setenv("KB_AI_ALLOW_PRIVATE", "1")
	st := testStore(t)
	base := upstream.URL
	if _, err := st.SetForgeSource("alice", "primary", "github", &base, nil); err != nil {
		t.Fatal(err)
	}
	aiBase, model, key := aiUpstream.URL, "gpt-4o", "key"
	if _, err := st.SetAISettings("alice", &aiBase, &model, &key); err != nil {
		t.Fatal(err)
	}
	service := New(st, ai.NewRunner(st, "", aiUpstream.Client(), nil), upstream.Client())
	preview, err := service.Preview(context.Background(), "alice", PreviewRequest{Source: "primary", Ref: "owner/repo", Max: 5})
	if err != nil || preview.Fetched != 5 || !preview.Truncated || !strings.Contains(preview.Note, "assistant input limit") || len(preview.Drafts) != 1 {
		t.Fatalf("pack preview = %+v, %v", preview, err)
	}
}

func TestPreviewHandlesEmptySelectionAndAIConfigurationFailure(t *testing.T) {
	var withIssue atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/issues") {
			http.NotFound(w, r)
			return
		}
		if withIssue.Load() {
			_, _ = io.WriteString(w, `[{"number":1,"title":"issue","html_url":"https://example.test/1"}]`)
			return
		}
		_, _ = io.WriteString(w, `[]`)
	}))
	defer upstream.Close()
	st := testStore(t)
	base := upstream.URL
	if _, err := st.SetForgeSource("alice", "primary", "github", &base, nil); err != nil {
		t.Fatal(err)
	}
	service := New(st, nil, upstream.Client())
	if _, err := service.Preview(context.Background(), "alice", PreviewRequest{Source: "missing", Ref: "owner/repo"}); err == nil {
		t.Fatal("preview with missing source succeeded")
	}
	preview, err := service.Preview(context.Background(), "alice", PreviewRequest{Source: "primary", Ref: "owner/repo", Max: 1})
	if err != nil || preview.Fetched != 0 || len(preview.Drafts) != 0 {
		t.Fatalf("empty preview = %+v, %v", preview, err)
	}
	withIssue.Store(true)
	if _, err := service.Preview(context.Background(), "alice", PreviewRequest{Source: "primary", Ref: "owner/repo", Max: 1}); err == nil {
		t.Fatal("preview without AI configuration succeeded")
	}
}

func TestQualifiedIdentityPreventsCrossForgeExactDuplicates(t *testing.T) {
	issue := Issue{Ref: "github#93", Title: "different work"}
	refA := Ref{Source: store.ForgeSource{Name: "a", Kind: "github", BaseURL: "https://forge.test/a"}, Kind: "github", Project: "owner/repo", Issue: 93}
	refB := Ref{Source: store.ForgeSource{Name: "b", Kind: "github", BaseURL: "https://forge.test/b"}, Kind: "github", Project: "owner/repo", Issue: 93}
	_, keyA := issueProvenance(refA, issue)
	_, keyB := issueProvenance(refB, issue)
	if keyA == keyB || !strings.Contains(keyA, "/a/") || !strings.Contains(keyB, "/b/") {
		t.Fatalf("qualified keys = %q %q", keyA, keyB)
	}
	st := testStore(t)
	if _, err := st.AddTask("alice", board.Task{Title: "existing", Tags: []string{importTagPrefix + keyA}}); err != nil {
		t.Fatal(err)
	}
	service := New(st, nil, nil)
	exact, err := service.duplicates("alice", refA, []Issue{issue})
	if err != nil || exact[0] == nil || exact[0].Via != "link" {
		t.Fatalf("exact = %+v, %v", exact, err)
	}
	other, err := service.duplicates("alice", refB, []Issue{issue})
	if err != nil || other[0] != nil {
		t.Fatalf("cross-forge duplicate = %+v, %v", other, err)
	}
}

func TestLegacyShortLinkDuplicateRequiresMatchingQualifiedProvenance(t *testing.T) {
	issueA := Issue{Ref: "github#93", Title: "different work", URL: "https://forge.test/a/owner/repo/issues/93"}
	issueB := Issue{Ref: "github#93", Title: "different work", URL: "https://forge.test/b/owner/repo/issues/93"}
	refA := Ref{Source: store.ForgeSource{Name: "a", Kind: "github", BaseURL: "https://forge.test/a"}, Kind: "github", Project: "owner/repo"}
	refB := Ref{Source: store.ForgeSource{Name: "b", Kind: "github", BaseURL: "https://forge.test/b"}, Kind: "github", Project: "owner/repo"}
	st := testStore(t)
	legacyTask, err := st.AddTask("alice", board.Task{Title: "legacy existing", Tags: []string{linkTagPrefix + issueA.Ref}})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RecordImportLinks("alice", []store.ImportLink{{
		Source: refA.Source.Name, Kind: refA.Kind, ExternalKey: "github:forge.test/owner/repo#93",
		Link: issueA.Ref, URL: issueA.URL, Title: legacyTask.Title,
	}}); err != nil {
		t.Fatal(err)
	}
	service := New(st, nil, nil)
	exact, err := service.duplicates("alice", refA, []Issue{issueA})
	if err != nil || exact[0] == nil || exact[0].ID != legacyTask.ID || exact[0].Via != "link" {
		t.Fatalf("legacy exact = %+v, %v", exact, err)
	}
	other, err := service.duplicates("alice", refB, []Issue{issueB})
	if err != nil || (other[0] != nil && other[0].Via == "link") {
		t.Fatalf("cross-source legacy duplicate = %+v, %v", other, err)
	}
	reconfigured := refB
	reconfigured.Source.Name = refA.Source.Name
	other, err = service.duplicates("alice", reconfigured, []Issue{issueB})
	if err != nil || (other[0] != nil && other[0].Via == "link") {
		t.Fatalf("reconfigured-path legacy duplicate = %+v, %v", other, err)
	}
	_, keyB := issueProvenance(refB, issueB)
	deleted, err := st.AddTaskWithImportLink("alice", board.Task{Title: "deleted B", Tags: []string{linkTagPrefix + issueB.Ref, importTagPrefix + keyB}}, store.ImportLink{
		Source: refB.Source.Name, Kind: refB.Kind, ExternalKey: keyB, Link: issueB.Ref, URL: issueB.URL, Title: "deleted B",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CancelTask("alice", deleted.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DeleteCancelledTask("alice", deleted.ID); err != nil {
		t.Fatal(err)
	}
	other, err = service.duplicates("alice", refB, []Issue{issueB})
	if err != nil || (other[0] != nil && other[0].Via == "link") {
		t.Fatalf("deleted-card provenance cross-joined surviving card = %+v, %v", other, err)
	}
}

func TestServiceSourceValidationAndAtomicCreate(t *testing.T) {
	st := testStore(t)
	service := New(st, nil, nil)
	base, token := "https://github.example", "token"
	for _, call := range []func() error{
		func() error { _, err := service.SaveSource("alice", "bad name", "github", &base, nil); return err },
		func() error { _, err := service.SaveSource("alice", "primary", "other", &base, nil); return err },
		func() error { _, err := service.SaveSource("alice", "primary", "github", nil, nil); return err },
		func() error {
			_, err := service.SaveSource("alice", "primary", "github", ptr("https://github.example?bad=1"), nil)
			return err
		},
		func() error { return service.DeleteSource("alice", "bad name") },
	} {
		if err := call(); err == nil {
			t.Fatal("invalid source operation succeeded")
		}
	}
	if cleared, err := service.SaveSource("alice", "primary", "github", &base, &token); err != nil || cleared {
		t.Fatalf("save = %t, %v", cleared, err)
	}
	gitlabBase := "https://gitlab.example"
	if cleared, err := service.SaveSource("alice", "primary", "gitlab", &gitlabBase, nil); err != nil || !cleared {
		t.Fatalf("kind change = %t, %v", cleared, err)
	}
	if _, err := service.CreateTask("alice", "missing", board.Task{Title: "card"}, LinkInput{}); err == nil {
		t.Fatal("missing source created task")
	}
	item := LinkInput{ExternalKey: "gitlab:primary@gitlab.example/acme/kb#1", Link: "gitlab#1", URL: "https://gitlab.example/acme/kb/-/issues/1", Title: "card"}
	if _, err := service.CreateTask("alice", "primary", board.Task{Title: "card"}, LinkInput{}); err == nil {
		t.Fatal("missing provenance created task")
	}
	created, err := service.CreateTask("alice", "primary", board.Task{Title: "card", Tags: []string{"import::" + item.ExternalKey}}, item)
	if err != nil || created.ID == "" {
		t.Fatalf("create = %+v, %v", created, err)
	}
	if found, err := st.ImportedAs("alice", []string{item.ExternalKey}); err != nil || found[item.ExternalKey].Title != item.Title {
		t.Fatalf("atomic provenance = %+v, %v", found, err)
	}
	oversized := item
	oversized.URL = strings.Repeat("x", 2049)
	if err := service.RecordLinks("alice", "primary", []LinkInput{oversized}); err == nil {
		t.Fatal("oversized provenance recorded")
	}
	if _, err := service.CreateTask("alice", "primary", board.Task{Title: "card"}, oversized); err == nil {
		t.Fatal("oversized provenance created task")
	}
	if _, err := service.CreateTask("alice", "primary", board.Task{Title: "card", Status: board.Status("invalid")}, item); err == nil {
		t.Fatal("invalid task created")
	}
	if err := service.DeleteSource("alice", "primary"); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Sources("alice"); err == nil {
		t.Fatal("closed store sources succeeded")
	}
	if _, err := service.SaveSource("alice", "primary", "github", &base, nil); err == nil {
		t.Fatal("closed store save succeeded")
	}
	if err := service.DeleteSource("alice", "primary"); err == nil {
		t.Fatal("closed store delete succeeded")
	}
}

func ptr(value string) *string { return &value }

func TestDriftLifecycleAndRevisionConflict(t *testing.T) {
	var title atomic.Value
	title.Store("Initial")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/issues/42"):
			_ = json.NewEncoder(w).Encode(map[string]any{"number": 42, "title": title.Load().(string), "body": "body", "html_url": upstreamURL(r) + "/owner/repo/issues/42"})
		case strings.HasSuffix(r.URL.Path, "/issues/42/comments"):
			_, _ = io.WriteString(w, `[]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	st := testStore(t)
	base := upstream.URL
	if _, err := st.SetForgeSource("alice", "primary", "github", &base, nil); err != nil {
		t.Fatal(err)
	}
	key := "github:" + strings.TrimPrefix(upstream.URL, "http://") + "/owner/repo#42"
	if err := st.RecordImportLinks("alice", []store.ImportLink{{Source: "primary", Kind: "github", ExternalKey: key, Link: "github#42", URL: upstream.URL + "/owner/repo/issues/42", Title: "Initial"}}); err != nil {
		t.Fatal(err)
	}
	service := New(st, nil, upstream.Client())
	first, err := service.CheckDrift(context.Background(), "alice", "primary", key)
	if err != nil || first.State != "baseline_recorded" {
		t.Fatalf("first = %+v, %v", first, err)
	}
	unchanged, err := service.CheckDrift(context.Background(), "alice", "primary", key)
	if err != nil || unchanged.State != "unchanged" {
		t.Fatalf("unchanged = %+v, %v", unchanged, err)
	}
	if _, _, err := service.authorizeDrift("alice", "wrong", key); err == nil {
		t.Fatal("wrong drift source accepted")
	}
	title.Store("Changed")
	drifted, err := service.CheckDrift(context.Background(), "alice", "primary", key)
	if err != nil || drifted.State != "drifted" || drifted.Revision == "" {
		t.Fatalf("drift = %+v, %v", drifted, err)
	}
	title.Store("Changed again")
	if _, err := service.AcceptDrift(context.Background(), "alice", "primary", key, drifted.Revision); !errors.Is(err, ErrUpstreamChanged) {
		t.Fatalf("stale accept = %v", err)
	}
	title.Store("Changed")
	at, err := service.AcceptDrift(context.Background(), "alice", "primary", key, drifted.Revision)
	if err != nil || at == "" {
		t.Fatalf("accept = %q, %v", at, err)
	}
	retry, err := service.AcceptDrift(context.Background(), "alice", "primary", key, drifted.Revision)
	if err != nil || retry != at {
		t.Fatalf("retry = %q, %v", retry, err)
	}
	if _, err := service.AcceptDrift(context.Background(), "alice", "primary", key, "bad"); err == nil {
		t.Fatal("bad revision accepted")
	}
	missingBaselineKey := "github:" + strings.TrimPrefix(upstream.URL, "http://") + "/owner/repo#43"
	if err := st.RecordImportLinks("alice", []store.ImportLink{{Source: "primary", Kind: "github", ExternalKey: missingBaselineKey, Link: "github#43", URL: upstream.URL + "/owner/repo/issues/43", Title: "Initial"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AcceptDrift(context.Background(), "alice", "primary", missingBaselineKey, strings.Repeat("a", 64)); err == nil {
		t.Fatal("missing baseline accepted")
	}
	if _, err := New(st, nil, nil).AcceptDrift(context.Background(), "alice", "primary", key, strings.Repeat("a", 64)); err == nil {
		t.Fatal("drift fetch without client succeeded")
	}
}

func upstreamURL(r *http.Request) string { return "http://" + r.Host }

func TestProbeAndSourceLifecycle(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/user" || r.URL.Path == "/api/v3/repos/owner/repo" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()
	st := testStore(t)
	base, token := upstream.URL, "token"
	if _, err := st.SetForgeSource("alice", "primary", "github", &base, &token); err != nil {
		t.Fatal(err)
	}
	prober := NewForgeProberWithClient(st, upstream.Client())
	if err := prober.Probe(context.Background(), "alice", ForgeProbeConfig{Name: "primary", Saved: true}); err != nil {
		t.Fatal(err)
	}
	if err := prober.Probe(context.Background(), "alice", ForgeProbeConfig{Name: "draft", Kind: "github", BaseURL: base, Project: "owner/repo", Token: "token"}); err != nil {
		t.Fatal(err)
	}
	for _, config := range []ForgeProbeConfig{{}, {Name: "missing", Saved: true}, {Name: "draft", Kind: "other", BaseURL: base}, {Name: "draft", Kind: "github", BaseURL: "ftp://forge.test"}} {
		if err := prober.Probe(context.Background(), "alice", config); err == nil {
			t.Errorf("probe %+v succeeded", config)
		}
	}
	failingProber := NewForgeProberWithClient(st, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("offline")
	})})
	if err := failingProber.Probe(context.Background(), "alice", ForgeProbeConfig{Name: "draft", Kind: "github", BaseURL: base}); err == nil {
		t.Fatal("failed connection probe succeeded")
	}
	service := New(st, nil, upstream.Client())
	if sources, err := service.Sources("alice"); err != nil || len(sources) != 1 {
		t.Fatalf("sources = %+v, %v", sources, err)
	}
	if cleared, err := service.SaveSource("alice", "new", "github", &base, nil); err != nil || cleared {
		t.Fatalf("save source = %t, %v", cleared, err)
	}
	if err := service.Probe(context.Background(), "alice", ForgeProbeConfig{Name: "new", Saved: true}); err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteSource("alice", "new"); err != nil {
		t.Fatal(err)
	}
}

func TestParsingAndResponseHelpers(t *testing.T) {
	if got := appendImportNote("one", "two"); got != "one; two" {
		t.Fatalf("note = %q", got)
	}
	if got := truncateImportText("界界", 4); got != "界" {
		t.Fatalf("truncate = %q", got)
	}
	comments, err := parseForgeComments("gitlab", []byte(`[{"body":"one","system":false},{"body":"two","system":true}]`))
	if err != nil || len(comments) != 1 {
		t.Fatalf("comments = %v, %v", comments, err)
	}
	comments, err = parseForgeComments("github", []byte(`[{"body":"one"}]`))
	if err != nil || len(comments) != 1 {
		t.Fatalf("github comments = %v, %v", comments, err)
	}
	if _, err := parseForgeComments("other", nil); err == nil {
		t.Fatal("invalid comments kind")
	}
	issues, err := parseForgeIssueList("github", []byte(`[{"number":1,"title":"issue"},{"number":2,"title":"pr","pull_request":{}}]`))
	if err != nil || len(issues) != 1 {
		t.Fatalf("issues = %+v, %v", issues, err)
	}
	if _, err := parseForgeIssueList("other", nil); err == nil {
		t.Fatal("invalid issue kind")
	}
	query := url.Values{}
	if !setForgeMilestoneQuery("gitlab", query, []byte(`{"title":"release"}`)) || query.Get("milestone") != "release" {
		t.Fatal("gitlab milestone query")
	}
	if !setForgeMilestoneQuery("github", query, []byte(`{"number":7}`)) || query.Get("milestone") != "7" {
		t.Fatal("github milestone query")
	}
	if setForgeMilestoneQuery("other", query, nil) {
		t.Fatal("invalid milestone query")
	}
	response := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok"))}
	if err := drainForgeResponse(response); err != nil {
		t.Fatal(err)
	}
	if err := executeForgeTest(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 500, Body: io.NopCloser(strings.NewReader("bad"))}, nil
	})}, httptest.NewRequest(http.MethodGet, "https://forge.test", nil)); err == nil {
		t.Fatal("bad status accepted")
	}

	if !ValidSourceName("primary") || ValidSourceName("bad name") || ValidSourceName("") {
		t.Fatal("public source-name validation")
	}
	if adr := IssueADR(Issue{Title: "one", Body: "body", Comments: []string{"note"}}); !strings.Contains(adr, "## Discussion") {
		t.Fatalf("adr = %q", adr)
	}
	if tags := StripLinkTags([]string{"one", "link::github#1"}); len(tags) != 1 || tags[0] != "one" {
		t.Fatalf("stripped tags = %v", tags)
	}
	revision := BaselineRevision(store.NewImportBaseline("title", "body", "now"))
	if !ValidRevision(revision) || ValidRevision("bad") {
		t.Fatalf("revision validation = %q", revision)
	}
	publicErr := &Error{Code: 400, Message: "bad", Cause: errors.New("cause")}
	if publicErr.Error() != "bad" || publicErr.Unwrap() == nil {
		t.Fatal("error helpers")
	}
	if refKind(Ref{}) != "project" || refKind(Ref{Milestone: 1}) != "milestone" {
		t.Fatal("kind helpers")
	}
	if skillBudget(999999) != ai.SkillBudget(999999) || logSafe("a\nb\u009bb") != "abb" {
		t.Fatal("compatibility helpers")
	}
	if adr := forgeIssueADR(forgeIssue{Title: strings.Repeat("x", 70<<10)}); len(adr) > 64<<10 {
		t.Fatalf("bounded adr = %d", len(adr))
	}
	if adr := forgeIssueADR(forgeIssue{Title: "title", Body: "body", Comments: []string{strings.Repeat("x", 70<<10)}}); len(adr) > 64<<10 || !strings.Contains(adr, "Discussion") {
		t.Fatalf("bounded discussion = %d", len(adr))
	}
	_ = newForgeClient()
	_ = NewForgeProber(testStore(t))
	_ = NewForgeProberWithClient(testStore(t), nil)
}

func TestAuthorizeDriftRejectsMismatchedProvenance(t *testing.T) {
	st := testStore(t)
	base := "https://github.example"
	if _, err := st.SetForgeSource("alice", "primary", "github", &base, nil); err != nil {
		t.Fatal(err)
	}
	links := []store.ImportLink{
		{Source: "primary", Kind: "gitlab", ExternalKey: "wrong-kind", Link: "github#1", URL: base + "/owner/repo/issues/1", Title: "wrong"},
		{Source: "primary", Kind: "github", ExternalKey: "project", Link: "github#project", URL: base + "/owner/repo", Title: "project"},
		{Source: "primary", Kind: "github", ExternalKey: "wrong-host", Link: "github#2", URL: "https://evil.example/owner/repo/issues/2", Title: "wrong"},
	}
	if err := st.RecordImportLinks("alice", links); err != nil {
		t.Fatal(err)
	}
	service := New(st, nil, nil)
	for _, test := range []struct{ source, key string }{
		{"primary", "missing"}, {"other", "wrong-kind"}, {"primary", "wrong-kind"}, {"primary", "project"}, {"primary", "wrong-host"},
	} {
		if _, _, err := service.authorizeDrift("alice", test.source, test.key); err == nil {
			t.Errorf("authorize drift %+v succeeded", test)
		}
	}
	if _, _, _, err := service.ResolveIssueDocument(context.Background(), "alice", "missing", base+"/owner/repo/issues/1"); err == nil {
		t.Fatal("missing source document resolved")
	}
}

func TestMilestonePaginationRateLimitAndFetchErrors(t *testing.T) {
	var mode atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch mode.Load() {
		case 1:
			w.WriteHeader(http.StatusInternalServerError)
			return
		case 2:
			_, _ = io.WriteString(w, `{`)
			return
		case 3:
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		switch r.URL.EscapedPath() {
		case "/api/v4/projects/group%2Frepo/milestones/4":
			_, _ = io.WriteString(w, `{"title":"Release"}`)
		case "/api/v4/projects/group%2Frepo/issues":
			if r.URL.Query().Get("milestone") != "Release" || r.URL.Query().Get("state") != "opened" {
				http.Error(w, "bad query", http.StatusBadRequest)
				return
			}
			w.Header().Set("X-Total", "1")
			_, _ = io.WriteString(w, `[{"iid":8,"title":"issue"}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	service := New(testStore(t), nil, upstream.Client())
	ref := forgeRef{Source: store.ForgeSource{Name: "gl", Kind: "gitlab", BaseURL: upstream.URL}, Kind: "gitlab", Project: "group/repo", Milestone: 4}
	issues, total, truncated, note, err := service.fetchIssues(context.Background(), ref, 20)
	if err != nil || len(issues) != 1 || total != 1 || truncated || note != "" {
		t.Fatalf("milestone = %d/%d truncated=%t note=%q err=%v", len(issues), total, truncated, note, err)
	}
	project := ref
	project.Milestone = 0
	for _, current := range []int32{1, 2} {
		mode.Store(current)
		if _, _, _, _, err := service.fetchIssues(context.Background(), project, 1); err == nil {
			t.Errorf("fetch mode %d succeeded", current)
		}
	}
	mode.Store(3)
	issues, _, truncated, note, err = service.fetchIssues(context.Background(), project, 1)
	if err != nil || len(issues) != 0 || !truncated || !strings.Contains(note, "rate limited") {
		t.Fatalf("rate limit = %+v %t %q %v", issues, truncated, note, err)
	}
}

func TestFetchIssueRejectsBadStatusCommentsAndPullRequests(t *testing.T) {
	var mode atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/comments") {
			if mode.Load() == 1 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_, _ = io.WriteString(w, `{`)
			return
		}
		switch mode.Load() {
		case 2:
			w.WriteHeader(http.StatusNotFound)
		case 3:
			_, _ = io.WriteString(w, `{`)
		case 4:
			_, _ = io.WriteString(w, `{"number":7,"title":"pull","pull_request":{}}`)
		default:
			_, _ = io.WriteString(w, `{"number":7,"title":"issue"}`)
		}
	}))
	defer upstream.Close()
	service := New(testStore(t), nil, upstream.Client())
	ref := forgeRef{Source: store.ForgeSource{Name: "gh", Kind: "github", BaseURL: upstream.URL}, Kind: "github", Project: "owner/repo", Issue: 7}
	for _, current := range []int32{0, 1, 2, 3, 4} {
		mode.Store(current)
		if _, err := service.fetchIssue(context.Background(), ref); err == nil {
			t.Errorf("issue mode %d succeeded", current)
		}
	}
	badKind := ref
	badKind.Kind = "other"
	if _, err := service.fetchIssue(context.Background(), badKind); err == nil {
		t.Fatal("bad kind issue fetched")
	}
	mode.Store(3)
	gitlab := ref
	gitlab.Kind = "gitlab"
	gitlab.Source.Kind = "gitlab"
	gitlab.Project = "group/repo"
	if _, _, _, err := service.fetchIssueSnapshot(context.Background(), gitlab); err == nil {
		t.Fatal("invalid GitLab issue fetched")
	}
}

func TestPackAndPathBoundaryBranches(t *testing.T) {
	comments := make([]string, 12)
	for index := range comments {
		comments[index] = strings.Repeat("x", maxImportCommentBytes+1)
	}
	packed, count := packImportIssues([]forgeIssue{{Title: "one", Ref: "github#1", Comments: comments}, {Title: strings.Repeat("x", maxImportPackBytes), Ref: "github#2"}})
	if count != 1 || !strings.Contains(packed, "Source 1") || strings.Contains(packed, "Source 2") {
		t.Fatalf("packed count=%d length=%d", count, len(packed))
	}
	if truncateImportText("x", 0) != "" || forgeURLPort(&url.URL{Scheme: "https"}) != "443" || forgeURLPort(&url.URL{Scheme: "http"}) != "80" {
		t.Fatal("boundary helpers")
	}
	state := forgeIssuePageState{}
	state.observeTotal("gitlab", http.Header{"X-Total": []string{"3"}})
	if state.totalHint != 3 || !state.hasGitLabTotal {
		t.Fatalf("observed total = %+v", state)
	}
	if !state.appendBatch([]forgeIssue{{Title: "one"}, {Title: "two"}}, 1, true) || len(state.issues) != 1 || !state.truncated {
		t.Fatalf("batch truncation = %+v", state)
	}
}

func TestGitLabReferenceAndFetchDispatchBranches(t *testing.T) {
	source := store.ForgeSource{Name: "gl", Kind: "gitlab", BaseURL: "https://gitlab.example"}
	tests := []struct {
		raw       string
		project   string
		issue     int
		milestone int
	}{
		{"group/project", "group/project", 0, 0},
		{"group/project/issues/8", "group/project", 8, 0},
		{"group/project/milestones/4", "group/project", 0, 4},
		{"group/project/-/boards/2", "group/project", 0, 0},
	}
	for _, test := range tests {
		ref, err := parseGitLabRef(source, test.raw)
		if err != nil || ref.Project != test.project || ref.Issue != test.issue || ref.Milestone != test.milestone {
			t.Errorf("parse %q = %+v, %v", test.raw, ref, err)
		}
	}
	for _, raw := range []string{"", "group//project", "group/-/unknown/1", "-/issues/1"} {
		if _, err := parseGitLabRef(source, raw); err == nil {
			t.Errorf("invalid GitLab ref %q accepted", raw)
		}
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/notes"):
			_, _ = io.WriteString(w, `[]`)
		case strings.HasSuffix(r.URL.Path, "/issues/8"):
			_, _ = io.WriteString(w, `{"iid":8,"title":"issue"}`)
		default:
			_, _ = io.WriteString(w, `[]`)
		}
	}))
	defer upstream.Close()
	service := New(testStore(t), nil, upstream.Client())
	issueRef := forgeRef{Source: store.ForgeSource{Name: "gl", Kind: "gitlab", BaseURL: upstream.URL}, Kind: "gitlab", Project: "group/project", Issue: 8}
	issues, total, truncated, note, err := service.fetchIssues(context.Background(), issueRef, 0)
	if err != nil || len(issues) != 1 || total != 1 || truncated || note != "" {
		t.Fatalf("single issue = %+v total=%d truncated=%t note=%q err=%v", issues, total, truncated, note, err)
	}
	project := issueRef
	project.Issue = 0
	if issues, _, _, _, err = service.fetchIssues(context.Background(), project, maxImportIssues+1); err != nil || len(issues) != 0 {
		t.Fatalf("project fetch = %+v, %v", issues, err)
	}
	bad := project
	bad.Kind = "other"
	if _, _, _, _, err := service.fetchIssues(context.Background(), bad, 1); err == nil {
		t.Fatal("invalid kind fetched")
	}
}

func TestForgeHTTPAndProbeFailureBranches(t *testing.T) {
	ref := forgeRef{Source: store.ForgeSource{Name: "gh", Kind: "github", BaseURL: "https://forge.test"}, Kind: "github", Project: "owner/repo", Issue: 1}
	path := "/repos/owner/repo/issues/1"
	if _, err := (&Service{}).forgeGet(context.Background(), ref, "https://forge.test/api/v3", path, nil); err == nil {
		t.Fatal("nil client accepted")
	}
	transportErr := errors.New("transport failed")
	service := &Service{forgeClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, transportErr
	})}}
	if _, err := service.forgeGet(context.Background(), ref, "https://forge.test/api/v3", path, nil); err == nil {
		t.Fatal("transport failure ignored")
	}
	service.forgeClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"X-Test": {"yes"}}, Body: failingReadCloser{readErr: errors.New("read")}}, nil
	})}
	if _, err := service.forgeGet(context.Background(), ref, "https://forge.test/api/v3", path, nil); err == nil {
		t.Fatal("body read failure ignored")
	}
	badKind := ref
	badKind.Kind = "other"
	service.forgeClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok"))}, nil
	})}
	if _, err := service.forgeGet(context.Background(), badKind, "https://forge.test", path, nil); err == nil {
		t.Fatal("invalid kind accepted")
	}

	request, err := http.NewRequest(http.MethodGet, "https://forge.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := executeForgeTest(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, transportErr
	})}, request); !errors.Is(err, transportErr) {
		t.Fatalf("execute transport error = %v", err)
	}
	if err := executeForgeTest(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: failingReadCloser{readErr: errors.New("read"), closeErr: errors.New("close")}}, nil
	})}, request); err == nil {
		t.Fatal("execute drain failure ignored")
	}
	if err := executeForgeTest(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusBadGateway, Body: io.NopCloser(strings.NewReader("bad"))}, nil
	})}, request); err == nil {
		t.Fatal("execute bad status ignored")
	}
	if err := drainForgeResponse(&http.Response{Body: failingReadCloser{closeErr: errors.New("close")}}); err == nil {
		t.Fatal("close failure ignored")
	}

	for _, raw := range []string{"", "ftp://forge.test", "https://user:pass@forge.test", "https://forge.test?x=1", "https://forge.test#fragment"} {
		if _, err := normalizeForgeProbeBase(raw); err == nil {
			t.Errorf("invalid probe base %q accepted", raw)
		}
	}
	base := "https://other.test"
	if _, err := resolveForgeTestTarget("https://forge.test", "stored", forgeTestProbe{BaseURL: &base}); err == nil {
		t.Fatal("stored token crossed origins")
	}
	pat := " supplied "
	target, err := resolveForgeTestTarget("https://forge.test", "stored", forgeTestProbe{BaseURL: &base, PAT: &pat})
	if err != nil || target.pat != "supplied" {
		t.Fatalf("supplied target = %+v, %v", target, err)
	}
	for _, test := range []struct{ kind, base, project string }{
		{"gitlab", "https://gitlab.test", ""},
		{"github", "https://github.test", ""},
		{"github", "https://github.test", "bad"},
		{"other", "https://forge.test", ""},
	} {
		request, err := newForgeTestRequest(context.Background(), test.kind, forgeTestTarget{baseURL: test.base}, test.project)
		if (test.kind == "other" || test.project == "bad") != (err != nil) {
			t.Errorf("request %+v = %v, %v", test, request, err)
		}
	}

	tags := stripModelLinkTags([]string{"normal", linkTagPrefix + "github#1", importTagPrefix + "qualified"})
	if len(tags) != 1 || tags[0] != "normal" {
		t.Fatalf("filtered tags = %v", tags)
	}
}

func TestRemainingReferenceMilestoneAndBoundedBranches(t *testing.T) {
	gitlab := store.ForgeSource{Name: "gl", Kind: "gitlab", BaseURL: "https://gitlab.example"}
	for _, test := range []struct {
		sources []store.ForgeSource
		name    string
		raw     string
	}{
		{nil, "", "owner/repo"},
		{nil, "missing", "owner/repo"},
		{[]store.ForgeSource{gitlab}, "gl", "owner/repo"},
		{[]store.ForgeSource{{Name: "bad", Kind: "other", BaseURL: "https://bad.example"}}, "bad", "https://bad.example/project"},
	} {
		if _, err := parseForgeRef(test.sources, test.name, test.raw); err == nil {
			t.Errorf("invalid reference %+v accepted", test)
		}
	}
	for name, call := range map[string]func() (string, error){
		"issue":     func() (string, error) { return forgeIssuePath(forgeRef{Kind: "github"}) },
		"milestone": func() (string, error) { return forgeMilestonePath(forgeRef{Kind: "github"}) },
		"list":      func() (string, error) { return forgeProjectIssuesPath(forgeRef{Kind: "github"}) },
	} {
		if _, err := call(); err == nil {
			t.Errorf("invalid %s path accepted", name)
		}
	}
	if _, _, err := forgeIssueListRequest(forgeRef{Kind: "other", Project: "project"}); err == nil {
		t.Fatal("invalid list kind accepted")
	}
	for _, test := range []struct {
		kind string
		body string
	}{
		{"gitlab", `{}`}, {"gitlab", `{`}, {"github", `{}`}, {"github", `{`},
	} {
		if setForgeMilestoneQuery(test.kind, url.Values{}, []byte(test.body)) {
			t.Errorf("bad milestone %s %q accepted", test.kind, test.body)
		}
	}
	if _, err := parseForgeComments("gitlab", []byte(`{`)); err == nil {
		t.Fatal("bad GitLab comments accepted")
	}
	if _, err := parseForgeComments("github", []byte(`{`)); err == nil {
		t.Fatal("bad GitHub comments accepted")
	}
	long := strings.Repeat("界", maxForgeCommentLen)
	if got := appendBoundedForgeComment(nil, long); len(got) != 1 || len(got[0]) > maxForgeCommentLen {
		t.Fatalf("bounded comment bytes = %d", len(got[0]))
	}
	state := forgeIssuePageState{issues: []forgeIssue{{Title: "one"}}, totalHint: 2, hasGitLabTotal: true}
	if _, _, truncated, _, _ := state.result(); !truncated {
		t.Fatal("total hint did not mark truncation")
	}
	if validImportDriftRevision(strings.Repeat("g", sha256.Size*2)) {
		t.Fatal("non-hex revision accepted")
	}
	_, key := importIssueProvenance(forgeRef{Source: store.ForgeSource{Name: "gh", BaseURL: ":"}, Kind: "github", Project: "owner/repo"}, forgeIssue{Ref: "github#1"})
	if strings.Contains(key, "@") {
		t.Fatalf("invalid base included in key: %q", key)
	}

	var mode atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mode.Load() == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		if r.URL.EscapedPath() == "/api/v3/repos/owner/repo/milestones/3" {
			if mode.Load() == 2 {
				_, _ = io.WriteString(w, `{`)
				return
			}
			_, _ = io.WriteString(w, `{"number":3}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()
	service := New(testStore(t), nil, upstream.Client())
	ref := forgeRef{Source: store.ForgeSource{Name: "gh", Kind: "github", BaseURL: upstream.URL}, Kind: "github", Project: "owner/repo", Milestone: 3}
	apiBase, err := forgeAPIBase("github", upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	path, query, err := service.forgeIssuesList(context.Background(), ref, apiBase)
	if err != nil || path == "" || query.Get("milestone") != "3" {
		t.Fatalf("GitHub milestone list = %q %v, %v", path, query, err)
	}
	for _, current := range []int32{1, 2} {
		mode.Store(current)
		if _, _, err := service.forgeIssuesList(context.Background(), ref, apiBase); err == nil {
			t.Errorf("milestone mode %d succeeded", current)
		}
	}
	badMilestone := ref
	badMilestone.Project = ""
	if _, _, err := service.forgeIssuesList(context.Background(), badMilestone, apiBase); err == nil {
		t.Fatal("invalid milestone path accepted")
	}
}

func TestServiceStorageFailuresRemainPublicErrors(t *testing.T) {
	st := testStore(t)
	base := "https://github.example"
	if _, err := st.SetForgeSource("alice", "primary", "github", &base, nil); err != nil {
		t.Fatal(err)
	}
	service := New(st, nil, nil)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	item := LinkInput{ExternalKey: "qualified", Link: "github#1", URL: base + "/owner/repo/issues/1", Title: "issue"}
	if _, err := service.duplicates("alice", Ref{Source: store.ForgeSource{Name: "primary", Kind: "github", BaseURL: base}, Kind: "github", Project: "owner/repo"}, []Issue{{Ref: "github#1", Title: "issue"}}); err == nil {
		t.Fatal("closed-store duplicate lookup succeeded")
	}
	checks := []func() error{
		func() error { return service.RecordLinks("alice", "primary", []LinkInput{item}) },
		func() error {
			_, err := service.CreateTask("alice", "primary", board.Task{Title: "issue"}, item)
			return err
		},
		func() error { _, err := service.Provenance("alice", "github#1"); return err },
		func() error {
			_, _, _, err := service.ResolveIssueDocument(context.Background(), "alice", "primary", base+"/owner/repo/issues/1")
			return err
		},
		func() error {
			_, err := service.CheckDrift(context.Background(), "alice", "primary", "qualified")
			return err
		},
		func() error {
			_, err := service.AcceptDrift(context.Background(), "alice", "primary", "qualified", strings.Repeat("a", sha256.Size*2))
			return err
		},
	}
	for index, check := range checks {
		var public *Error
		if err := check(); err == nil || !errors.As(err, &public) || public.Code != http.StatusInternalServerError {
			t.Errorf("check %d = %v", index, err)
		}
	}
}

func TestRemainingServiceAuthorizationAndFetchErrors(t *testing.T) {
	st := testStore(t)
	root, nested := "https://forge.test", "https://forge.test/nested"
	if _, err := st.SetForgeSource("alice", "root", "github", &root, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetForgeSource("alice", "nested", "github", &nested, nil); err != nil {
		t.Fatal(err)
	}
	transportErr := errors.New("offline")
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, transportErr
	})}
	service := New(st, nil, client)
	if _, err := service.authorizeRef("alice", "root", nested+"/owner/repo/issues/1"); err == nil {
		t.Fatal("reference selected a different configured source")
	}
	if _, _, _, err := service.ResolveIssueDocument(context.Background(), "alice", "root", root+"/owner/repo/issues/1"); err == nil {
		t.Fatal("document fetch transport failure succeeded")
	}
	if _, err := service.Preview(context.Background(), "alice", PreviewRequest{Source: "root", Ref: "owner/repo", Max: 1}); err == nil {
		t.Fatal("preview fetch transport failure succeeded")
	}
	if err := st.RecordImportLinks("alice", []store.ImportLink{{Source: "root", Kind: "github", ExternalKey: "qualified", Link: "github#1", URL: root + "/owner/repo/issues/1", Title: "issue"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CheckDrift(context.Background(), "alice", "root", "qualified"); err == nil {
		t.Fatal("drift fetch transport failure succeeded")
	}
	ref := forgeRef{Source: store.ForgeSource{Name: "root", Kind: "github", BaseURL: root}, Kind: "github", Project: "owner/repo"}
	if _, _, _, err := service.fetchIssueSnapshot(context.Background(), ref); err == nil {
		t.Fatal("snapshot without issue succeeded")
	}
	badIssue := ref
	badIssue.Issue, badIssue.Project = 1, "owner"
	if _, _, _, _, err := service.fetchIssues(context.Background(), badIssue, 1); err == nil {
		t.Fatal("issue with invalid project fetched")
	}
	ref.Milestone = 1
	apiBase, err := forgeAPIBase("github", root)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.forgeIssuesList(context.Background(), ref, apiBase); err == nil {
		t.Fatal("milestone transport failure succeeded")
	}
	if _, err := service.fetchForgeComments(context.Background(), ref, apiBase, "/repos/owner/repo/issues/1"); err == nil {
		t.Fatal("comment transport failure succeeded")
	}
	if _, err := parseForgeIssueList("gitlab", []byte(`{`)); err == nil {
		t.Fatal("invalid GitLab issue list accepted")
	}
	badBase := "ftp://forge.test"
	if _, err := resolveForgeTestTarget("", "", forgeTestProbe{BaseURL: &badBase}); err == nil {
		t.Fatal("invalid test target accepted")
	}
}

func TestAcceptDriftConcurrentCASIsIdempotent(t *testing.T) {
	var calls atomic.Int32
	var releaseOnce sync.Once
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/issues/1") {
			http.NotFound(w, r)
			return
		}
		if calls.Add(1) == 2 {
			releaseOnce.Do(func() { close(release) })
		}
		<-release
		_, _ = io.WriteString(w, `{"number":1,"title":"new","body":"body"}`)
	}))
	defer upstream.Close()
	st := testStore(t)
	base := upstream.URL
	if _, err := st.SetForgeSource("alice", "primary", "github", &base, nil); err != nil {
		t.Fatal(err)
	}
	key := "qualified"
	if err := st.RecordImportLinks("alice", []store.ImportLink{{Source: "primary", Kind: "github", ExternalKey: key, Link: "github#1", URL: base + "/owner/repo/issues/1", Title: "old"}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.CreateImportBaseline("alice", key, store.NewImportBaseline("old", "body", "old-at")); err != nil {
		t.Fatal(err)
	}
	revision := importDriftRevision(store.NewImportBaseline("new", "body", "new-at"))
	services := []*Service{New(st, nil, upstream.Client()), New(st, nil, upstream.Client())}
	results := make(chan error, len(services))
	for _, service := range services {
		go func(service *Service) {
			_, err := service.AcceptDrift(context.Background(), "alice", "primary", key, revision)
			results <- err
		}(service)
	}
	for range services {
		if err := <-results; err != nil {
			t.Fatalf("concurrent accept = %v", err)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("fetch calls = %d", calls.Load())
	}
}

func TestAcceptDriftRejectsConcurrentDifferentBaseline(t *testing.T) {
	arrived, release := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/issues/1") {
			http.NotFound(w, r)
			return
		}
		close(arrived)
		<-release
		_, _ = io.WriteString(w, `{"number":1,"title":"new","body":"body"}`)
	}))
	defer upstream.Close()
	st := testStore(t)
	base := upstream.URL
	if _, err := st.SetForgeSource("alice", "primary", "github", &base, nil); err != nil {
		t.Fatal(err)
	}
	key := "qualified"
	if err := st.RecordImportLinks("alice", []store.ImportLink{{Source: "primary", Kind: "github", ExternalKey: key, Link: "github#1", URL: base + "/owner/repo/issues/1", Title: "old"}}); err != nil {
		t.Fatal(err)
	}
	old := store.NewImportBaseline("old", "body", "old-at")
	if _, _, err := st.CreateImportBaseline("alice", key, old); err != nil {
		t.Fatal(err)
	}
	revision := importDriftRevision(store.NewImportBaseline("new", "body", "new-at"))
	result := make(chan error, 1)
	go func() {
		_, err := New(st, nil, upstream.Client()).AcceptDrift(context.Background(), "alice", "primary", key, revision)
		result <- err
	}()
	select {
	case <-arrived:
	case <-time.After(time.Second):
		t.Fatal("accept did not reach upstream")
	}
	if err := st.SetImportBaseline("alice", key, store.NewImportBaseline("other", "body", "other-at")); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case err := <-result:
		if !errors.Is(err, ErrUpstreamChanged) {
			t.Fatalf("concurrent different baseline = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("accept did not finish")
	}
}

func TestAcceptDriftReportsCASStorageFailure(t *testing.T) {
	arrived, release := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/issues/1") {
			http.NotFound(w, r)
			return
		}
		close(arrived)
		<-release
		_, _ = io.WriteString(w, `{"number":1,"title":"new","body":"body"}`)
	}))
	defer upstream.Close()
	st := testStore(t)
	base := upstream.URL
	if _, err := st.SetForgeSource("alice", "primary", "github", &base, nil); err != nil {
		t.Fatal(err)
	}
	key := "qualified"
	if err := st.RecordImportLinks("alice", []store.ImportLink{{Source: "primary", Kind: "github", ExternalKey: key, Link: "github#1", URL: base + "/owner/repo/issues/1", Title: "old"}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.CreateImportBaseline("alice", key, store.NewImportBaseline("old", "body", "old-at")); err != nil {
		t.Fatal(err)
	}
	revision := importDriftRevision(store.NewImportBaseline("new", "body", "new-at"))
	result := make(chan error, 1)
	go func() {
		_, err := New(st, nil, upstream.Client()).AcceptDrift(context.Background(), "alice", "primary", key, revision)
		result <- err
	}()
	select {
	case <-arrived:
	case <-time.After(time.Second):
		t.Fatal("accept did not reach upstream")
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case err := <-result:
		var public *Error
		if !errors.As(err, &public) || public.Code != http.StatusInternalServerError {
			t.Fatalf("CAS storage failure = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("accept did not finish")
	}
}
