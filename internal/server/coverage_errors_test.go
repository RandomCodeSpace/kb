package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
	if _, err := s.runSkill(context.Background(), "u", skillScopeReadOnly, "adr-split", "in", 1, aiStoriesMaxTokens); err == nil {
		t.Fatal("runSkill accepted a closed store")
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
