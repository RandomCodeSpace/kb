package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/store"
)

type coverageReadCloser struct {
	readErr  error
	closeErr error
}

func (r coverageReadCloser) Read([]byte) (int, error) { return 0, r.readErr }
func (r coverageReadCloser) Close() error             { return r.closeErr }

func TestConditionalHeaderHelpersMatchWholeTags(t *testing.T) {
	if !ifMatchContainsStar(`"r1", *, "r2"`) {
		t.Fatal("ifMatchContainsStar did not find a complete wildcard tag")
	}
	if ifMatchContainsStar(`"r1*"`) {
		t.Fatal("ifMatchContainsStar matched a partial wildcard")
	}
	if !etagMatches(`"r1", "r2"`, `"r2"`) {
		t.Fatal("etagMatches did not match a complete listed ETag")
	}
	if etagMatches(`"r12"`, `"r1"`) {
		t.Fatal("etagMatches matched an ETag prefix")
	}
}

func TestEnsureJSONEOFRejectsExtraAndMalformedValues(t *testing.T) {
	for _, tt := range []struct {
		name string
		tail string
	}{
		{name: "second value", tail: `{} {}`},
		{name: "malformed tail", tail: `{} {`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			decoder := json.NewDecoder(strings.NewReader(tt.tail))
			var first any
			if err := decoder.Decode(&first); err != nil {
				t.Fatalf("decode first value: %v", err)
			}
			if err := ensureJSONEOF(decoder); err == nil {
				t.Fatal("ensureJSONEOF accepted trailing input")
			}
		})
	}
}

func TestReadBodyDistinguishesOversizeAndReaderFailure(t *testing.T) {
	oversize := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("x", maxBodyBytes+1)))
	w := httptest.NewRecorder()
	if _, ok := readBody(w, oversize); ok || w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize read = ok %v status %d", ok, w.Code)
	}

	broken := httptest.NewRequest(http.MethodPost, "/", nil)
	broken.Body = coverageReadCloser{readErr: errors.New("read failed")}
	w = httptest.NewRecorder()
	if _, ok := readBody(w, broken); ok || w.Code != http.StatusBadRequest {
		t.Fatalf("broken read = ok %v status %d", ok, w.Code)
	}
}

func TestForgeGetMapsTransportBodyAndCloseFailures(t *testing.T) {
	ref := forgeRef{Kind: "gitlab", pat: "secret", Source: store.ForgeSource{Name: "primary"}}
	tests := []struct {
		name   string
		client *http.Client
		ref    forgeRef
	}{
		{name: "nil client", ref: ref},
		{name: "invalid kind", ref: forgeRef{Kind: "other", Source: ref.Source}, client: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) { t.Fatal("unexpected egress"); return nil, nil })}},
		{name: "transport failure", ref: ref, client: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") })}},
		{name: "body read failure", ref: ref, client: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: coverageReadCloser{readErr: errors.New("read failed")}}, nil
		})}},
		{name: "body close failure", ref: ref, client: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: coverageReadCloser{readErr: io.EOF, closeErr: errors.New("close failed")}}, nil
		})}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &server{forgeClient: tt.client}
			if _, err := s.forgeGet(context.Background(), tt.ref, "https://forge.invalid", "/issues", nil); err == nil {
				t.Fatal("forgeGet returned nil error")
			}
		})
	}
}

func TestForgeGetBuildsBoundedAuthenticatedRequest(t *testing.T) {
	var got *http.Request
	s := &server{forgeClient: &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		got = r.Clone(r.Context())
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"X-Total": []string{"1"}}, Body: io.NopCloser(strings.NewReader(`[]`))}, nil
	})}}
	ref := forgeRef{Kind: "github", pat: "token", Source: store.ForgeSource{Name: "primary"}}
	response, err := s.forgeGet(context.Background(), ref, "https://forge.invalid/api", "/issues", url.Values{"page": []string{"2"}})
	if err != nil {
		t.Fatalf("forgeGet: %v", err)
	}
	if response.status != http.StatusOK || got == nil || got.URL.RawQuery != "page=2" {
		t.Fatalf("response/request = %+v / %+v", response, got)
	}
	if got.Header.Get("Authorization") != "Bearer token" || got.Header.Get("Accept") != "application/vnd.github+json" {
		t.Fatalf("forge headers = %v", got.Header)
	}
}

func TestForgeParsersRejectMalformedPayloadsAndKinds(t *testing.T) {
	for _, kind := range []string{"gitlab", "github", "other"} {
		t.Run(kind, func(t *testing.T) {
			if _, err := parseForgeIssueList(kind, []byte(`{`)); err == nil {
				t.Fatalf("parseForgeIssueList(%q) accepted malformed payload", kind)
			}
		})
	}
	if _, err := forgeProjectPath(forgeRef{Kind: "github", Project: "missing-repo"}); err == nil {
		t.Fatal("forgeProjectPath accepted an incomplete GitHub project")
	}
	if _, err := forgeProjectPath(forgeRef{Kind: "other", Project: "owner/repo"}); err == nil {
		t.Fatal("forgeProjectPath accepted an invalid kind")
	}
	if got := forgeTotalHint(http.Header{"X-Total": []string{"invalid"}}); got != -1 {
		t.Fatalf("forgeTotalHint(invalid) = %d", got)
	}
}

func TestForgeReferenceHelpersRejectMalformedPaths(t *testing.T) {
	gitlab := store.ForgeSource{Name: "gl", Kind: "gitlab", BaseURL: "https://gitlab.example"}
	github := store.ForgeSource{Name: "gh", Kind: "github", BaseURL: "https://github.example"}
	for _, path := range []string{"", "group//project", "/", "group/project/-/unknown/1", "group/project/-/issues/0", "group/project/-/issues/nope", "-/issues/1"} {
		if _, err := parseGitLabRef(gitlab, path); err == nil {
			t.Errorf("parseGitLabRef(%q) returned nil error", path)
		}
	}
	for _, path := range []string{"", "owner", "owner/repo/pulls/1", "owner/repo/issues/0", "owner//issues/1"} {
		if _, err := parseGitHubRef(github, path); err == nil {
			t.Errorf("parseGitHubRef(%q) returned nil error", path)
		}
	}
	for _, ref := range []forgeRef{{}, {Kind: "other", Project: "x"}, {Kind: "github", Project: "owner"}} {
		if _, err := forgeIssuePath(ref); err == nil {
			t.Errorf("forgeIssuePath(%+v) returned nil error", ref)
		}
		if _, err := forgeMilestonePath(ref); err == nil {
			t.Errorf("forgeMilestonePath(%+v) returned nil error", ref)
		}
		if _, err := forgeProjectIssuesPath(ref); err == nil {
			t.Errorf("forgeProjectIssuesPath(%+v) returned nil error", ref)
		}
	}
	for _, raw := range []string{"", "0", "-1", "x"} {
		if _, err := forgeRefID(raw); err == nil {
			t.Errorf("forgeRefID(%q) returned nil error", raw)
		}
	}
	for _, raw := range []string{"", " ", "ftp://forge.example", "https://user@forge.example", "https://forge.example?q=1", "https://forge.example#x"} {
		if _, err := normalizeForgeProbeBase(raw); err == nil {
			t.Errorf("normalizeForgeProbeBase(%q) returned nil error", raw)
		}
	}
	if got := forgeURLPort(&url.URL{Scheme: "https", Host: "x"}); got != "443" {
		t.Errorf("HTTPS default port = %q", got)
	}
	if got := forgeURLPort(&url.URL{Scheme: "http", Host: "x"}); got != "80" {
		t.Errorf("HTTP default port = %q", got)
	}
	if got := forgeURLPort(&url.URL{Scheme: "https", Host: "x:8443"}); got != "8443" {
		t.Errorf("explicit port = %q", got)
	}
}

func TestForgeListAndCommentHelpersMapInvalidResponses(t *testing.T) {
	deny := roundTripperFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") })
	s := &server{forgeClient: &http.Client{Transport: deny}}
	ctx := context.Background()
	for _, ref := range []forgeRef{{Kind: "other", Project: "x"}, {Kind: "github", Project: "bad"}} {
		if _, _, err := s.forgeIssuesList(ctx, ref, "https://forge.invalid"); err == nil {
			t.Errorf("forgeIssuesList(%+v) returned nil error", ref)
		}
	}
	if _, err := s.fetchForgeComments(ctx, forgeRef{Kind: "other", Project: "x", Source: store.ForgeSource{Name: "x"}}, "https://forge.invalid", "/issues/1"); err == nil {
		t.Fatal("fetchForgeComments accepted invalid kind")
	}
	comments := make([]string, maxForgeComments)
	if got := appendBoundedForgeComment(comments, "extra"); len(got) != maxForgeComments {
		t.Fatalf("bounded comments len = %d", len(got))
	}
	long := strings.Repeat("界", maxForgeCommentLen)
	if got := appendBoundedForgeComment(nil, long); len(got) != 1 || len(got[0]) > maxForgeCommentLen {
		t.Fatalf("bounded unicode comment = %#v", got)
	}
}

func TestRSAKeyFromJWKRejectsMalformedComponents(t *testing.T) {
	for _, tt := range []struct{ n, e string }{
		{n: "%", e: "AQAB"},
		{n: "AQ", e: "%"},
		{n: "", e: "AQAB"},
		{n: "AQ", e: ""},
		{n: "AQ", e: "AQ"},
		{n: "AQ", e: "AQIDBAU"},
	} {
		if _, err := rsaKeyFromJWK(tt.n, tt.e); err == nil {
			t.Errorf("rsaKeyFromJWK(%q,%q) returned nil error", tt.n, tt.e)
		}
	}
}

func TestChatMapsTransportReadAndPayloadFailures(t *testing.T) {
	jsonResponse := func(status int, body string) *http.Response {
		return &http.Response{
			StatusCode: status,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}
	}
	tests := []struct {
		name      string
		maxTokens int64
		rt        roundTripperFunc
	}{
		{name: "transport", maxTokens: 10, rt: func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") }},
		{name: "read", maxTokens: 10, rt: func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       coverageReadCloser{readErr: errors.New("read failed")},
			}, nil
		}},
		{name: "status", maxTokens: 10, rt: func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusTeapot, `{"error":{"message":"no"}}`), nil
		}},
		{name: "invalid json", maxTokens: 10, rt: func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, "{"), nil
		}},
		// An unstated budget is never sent as one: the floor applies instead.
		{name: "no choices", rt: func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, `{"choices":[]}`), nil
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &server{aiClient: &http.Client{Transport: tt.rt}}
			call := chatCall{msgs: []chatMessage{{Role: "user", Content: "hi"}}, maxTokens: tt.maxTokens}
			if _, err := s.chat("u", aiConfig{baseURL: "https://ai.invalid", model: "m"}, call); err == nil {
				t.Fatal("chat returned nil error")
			}
		})
	}
}

func TestStoredAIConfigAndSettingsHandlersMapClosedStore(t *testing.T) {
	st := newTestStore(t)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	s := &server{store: st, aiClient: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("unexpected egress")
		return nil, nil
	})}}
	if _, err := s.storedAIConfig("u"); err == nil {
		t.Fatal("storedAIConfig accepted a closed store")
	}
	if _, err := s.chatCompletion("u", nil, 10, false); err == nil {
		t.Fatal("chatCompletion accepted a closed store")
	}

	for _, handler := range []struct {
		name string
		call func(http.ResponseWriter)
	}{
		{name: "labels", call: func(w http.ResponseWriter) { s.handleLabels(w, httptest.NewRequest(http.MethodGet, "/", nil), "u") }},
		{name: "settings", call: func(w http.ResponseWriter) {
			s.handleGetSettings(w, httptest.NewRequest(http.MethodGet, "/", nil), "u")
		}},
	} {
		t.Run(handler.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			handler.call(w)
			if w.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500", w.Code)
			}
		})
	}
}

func TestIntegrationHandlersMapValidationAndClosedStoreErrors(t *testing.T) {
	st := newTestStore(t)
	s := &server{store: st, forgeClient: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("unexpected egress")
		return nil, nil
	})}}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	get := httptest.NewRecorder()
	s.handleGetIntegrations(get, httptest.NewRequest(http.MethodGet, "/", nil), "u")
	if get.Code != http.StatusInternalServerError {
		t.Fatalf("get integrations status = %d", get.Code)
	}

	putReq := httptest.NewRequest(http.MethodPut, "/api/integrations/primary", strings.NewReader(`{"kind":"gitlab","base_url":"https://forge.example"}`))
	putReq.SetPathValue("name", "primary")
	put := httptest.NewRecorder()
	s.handlePutIntegration(put, putReq, "u")
	if put.Code != http.StatusInternalServerError {
		t.Fatalf("put integration status = %d body=%s", put.Code, put.Body.String())
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/api/integrations/primary", nil)
	delReq.SetPathValue("name", "primary")
	del := httptest.NewRecorder()
	s.handleDeleteIntegration(del, delReq, "u")
	if del.Code != http.StatusInternalServerError {
		t.Fatalf("delete integration status = %d", del.Code)
	}

	testReq := httptest.NewRequest(http.MethodPost, "/api/integrations/primary/test", nil)
	testReq.SetPathValue("name", "primary")
	tested := httptest.NewRecorder()
	s.handleTestIntegration(tested, testReq, "u")
	if tested.Code != http.StatusOK || !strings.Contains(tested.Body.String(), "integration unavailable") {
		t.Fatalf("test integration status/body = %d %s", tested.Code, tested.Body.String())
	}
}

func TestIntegrationHandlersRejectMalformedRequestsBeforeStorage(t *testing.T) {
	s := &server{forgeClient: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("unexpected egress")
		return nil, nil
	})}}
	for _, tt := range []struct {
		name, method, pathName, body string
		call                         func(*server, http.ResponseWriter, *http.Request)
	}{
		{name: "put invalid name", method: http.MethodPut, pathName: "bad name", body: `{}`, call: func(s *server, w http.ResponseWriter, r *http.Request) { s.handlePutIntegration(w, r, "u") }},
		{name: "put invalid json", method: http.MethodPut, pathName: "primary", body: `{`, call: func(s *server, w http.ResponseWriter, r *http.Request) { s.handlePutIntegration(w, r, "u") }},
		{name: "put invalid kind", method: http.MethodPut, pathName: "primary", body: `{"kind":"other"}`, call: func(s *server, w http.ResponseWriter, r *http.Request) { s.handlePutIntegration(w, r, "u") }},
		{name: "put invalid base", method: http.MethodPut, pathName: "primary", body: `{"kind":"gitlab","base_url":"ftp://forge.example"}`, call: func(s *server, w http.ResponseWriter, r *http.Request) { s.handlePutIntegration(w, r, "u") }},
		{name: "delete invalid name", method: http.MethodDelete, pathName: "bad name", call: func(s *server, w http.ResponseWriter, r *http.Request) { s.handleDeleteIntegration(w, r, "u") }},
		{name: "test invalid name", method: http.MethodPost, pathName: "bad name", call: func(s *server, w http.ResponseWriter, r *http.Request) { s.handleTestIntegration(w, r, "u") }},
		{name: "test invalid json", method: http.MethodPost, pathName: "primary", body: `{`, call: func(s *server, w http.ResponseWriter, r *http.Request) { s.handleTestIntegration(w, r, "u") }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(tt.method, "/", strings.NewReader(tt.body))
			r.SetPathValue("name", tt.pathName)
			w := httptest.NewRecorder()
			tt.call(s, w, r)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestJWKSFetchRejectsTransportStatusBodyAndDocuments(t *testing.T) {
	tests := []struct {
		name string
		rt   roundTripperFunc
	}{
		{name: "transport", rt: func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") }},
		{name: "status", rt: func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusBadGateway, Body: io.NopCloser(strings.NewReader("bad"))}, nil
		}},
		{name: "read", rt: func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: coverageReadCloser{readErr: errors.New("read failed")}}, nil
		}},
		{name: "invalid json", rt: func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("{"))}, nil
		}},
		{name: "no usable keys", rt: func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewBufferString(`{"keys":[{"kty":"EC","kid":"x"}]}`))}, nil
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := &jwksCache{url: "https://idp.invalid/keys", client: &http.Client{Transport: tt.rt}}
			if err := cache.fetchLocked(); err == nil {
				t.Fatal("fetchLocked returned nil error")
			}
		})
	}
}

func TestDecodeJSONObjectRejectsNonObjectsAndMalformedObjects(t *testing.T) {
	for _, body := range []string{`[]`, `{bad}`} {
		if _, err := decodeJSONObject(body); err == nil {
			t.Fatalf("decodeJSONObject(%q) returned nil error", body)
		}
	}
}

func TestClosedStoreHandlersReturnServerErrorsWithoutEgress(t *testing.T) {
	st := newTestStore(t)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	s := &server{store: st, aiClient: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("unexpected AI egress")
		return nil, nil
	})}, forgeClient: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("unexpected forge egress")
		return nil, nil
	})}}
	tests := []struct {
		name, body string
		call       func(*server, http.ResponseWriter, *http.Request)
	}{
		{name: "get board", call: func(s *server, w http.ResponseWriter, r *http.Request) { s.handleGetBoard(w, r, "u") }},
		{name: "put board", body: "# Board\n", call: func(s *server, w http.ResponseWriter, r *http.Request) {
			r.Header.Set("Content-Type", "text/markdown")
			r.Header.Set("If-Match", "*")
			s.handlePutBoard(w, r, "u")
		}},
		{name: "tombstone", body: `{"task_id":"id","reason":"reason"}`, call: func(s *server, w http.ResponseWriter, r *http.Request) { s.handleTombstone(w, r, "u") }},
		{name: "put settings", body: `{"ai_model":"m"}`, call: func(s *server, w http.ResponseWriter, r *http.Request) { s.handlePutSettings(w, r, "u") }},
		{name: "AI story", body: `{"mode":"create","prompt":"story"}`, call: func(s *server, w http.ResponseWriter, r *http.Request) { s.handleAIStory(w, r, "u") }},
		{name: "AI stories", body: `{"adr":"stories"}`, call: func(s *server, w http.ResponseWriter, r *http.Request) { s.handleAIStories(w, r, "u") }},
		{name: "AI stories URL", body: `{"url":"https://forge.example/group/project","source":"primary"}`, call: func(s *server, w http.ResponseWriter, r *http.Request) { s.handleAIStories(w, r, "u") }},
		{name: "import preview", body: `{"source":"primary","ref":"group/project"}`, call: func(s *server, w http.ResponseWriter, r *http.Request) { s.handleImportPreview(w, r, "u") }},
		{name: "import links", body: `{"source":"primary","items":[]}`, call: func(s *server, w http.ResponseWriter, r *http.Request) { s.handleImportLinks(w, r, "u") }},
		{name: "import provenance", body: `{"link":"gitlab#1"}`, call: func(s *server, w http.ResponseWriter, r *http.Request) { s.handleImportProvenance(w, r, "u") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tt.body))
			w := httptest.NewRecorder()
			tt.call(s, w, r)
			if w.Code != http.StatusInternalServerError && w.Code != http.StatusBadGateway {
				t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestForgeDrainReportsReadAndCloseFailures(t *testing.T) {
	for _, body := range []io.ReadCloser{
		coverageReadCloser{readErr: errors.New("read failed")},
		coverageReadCloser{readErr: io.EOF, closeErr: errors.New("close failed")},
	} {
		if err := drainForgeResponse(&http.Response{Body: body}); err == nil {
			t.Fatal("drainForgeResponse returned nil error")
		}
	}
}

func TestAIValueCoercionAndTextBoundsCoverDefensiveBranches(t *testing.T) {
	if got, err := coerceDraft(`{"title":""}`); err != nil || got.Title != "" {
		t.Fatalf("coerceDraft empty title = %+v, %v", got, err)
	}
	if _, err := coerceDrafts(`{"stories":"bad"}`, 2); err == nil {
		t.Fatal("coerceDrafts accepted a non-array")
	}
	if got, err := coerceDrafts(`{"stories":[]}`, 2); err != nil || len(got) != 0 {
		t.Fatalf("coerceDrafts empty = %+v, %v", got, err)
	}
	if got := truncateImportText("hello", 0); got != "" {
		t.Fatalf("truncate max zero = %q", got)
	}
	if got := truncateImportText("hello", 10); got != "hello" {
		t.Fatalf("truncate short = %q", got)
	}
	if got := truncateImportText("界界", 4); len(got) > 4 {
		t.Fatalf("truncate unicode = %q", got)
	}
	if got := stripControlKeepLines("a\x00b\r\nc\td"); got != "ab\ncd" {
		t.Fatalf("stripControlKeepLines = %q", got)
	}
	for in, want := range map[int]int{-1: 1, 0: 1, 3: 3, 9: 4} {
		if got := clampPrio(in); got != want {
			t.Errorf("clampPrio(%d) = %d, want %d", in, got, want)
		}
	}
}
