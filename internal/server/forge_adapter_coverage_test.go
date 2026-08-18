package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/forge"
)

func TestRejectedForgeHandlerBodiesAndBoardJSONEdges(t *testing.T) {
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
	for _, test := range handlers {
		t.Run(test.name+" oversized", func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("x", maxBodyBytes+1)))
			request.SetPathValue("name", "primary")
			response := httptest.NewRecorder()
			test.fn(response, request, "user")
			if response.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("status = %d", response.Code)
			}
		})
	}
	for _, test := range []struct {
		name string
		fn   func(http.ResponseWriter, *http.Request, string)
	}{
		{"tombstone", s.handleTombstone},
		{"import preview", s.handleImportPreview},
		{"import drift", s.handleImportDrift},
		{"import drift accept", s.handleImportDriftAccept},
	} {
		t.Run(test.name+" malformed", func(t *testing.T) {
			response := httptest.NewRecorder()
			test.fn(response, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{`)), "user")
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d", response.Code)
			}
		})
	}

	for _, body := range []string{
		`[]`, `{"board":`, `{"board":"x","board":"y","task_ids":[]}`,
		`{"board":null,"task_ids":[]}`, `{"board":"x","task_ids":`,
		`{"board":"x","task_ids":null}`, `{"board":"x","task_ids":{}}`,
		`{"board":"x","other":[]}`, `{"board":"x"}`,
		`{"board":"x","task_ids":[]} {}`,
	} {
		if _, _, err := parseBoardJSONPut([]byte(body)); err == nil {
			t.Fatalf("parseBoardJSONPut accepted %q", body)
		}
	}
}

func TestSharedForgeAdapterAndAISanitizerBranches(t *testing.T) {
	config := aiConfig{baseURL: "stored-base", model: "stored-model", key: "stored-key"}.merge(aiTestRequest{
		BaseURL: " new-base ", Model: " new-model ", Key: " new-key ",
	})
	if config.baseURL != "new-base" || config.model != "new-model" || config.key != "new-key" {
		t.Fatalf("merged config = %+v", config)
	}
	if got := stripControlKeepLines("one\r\ntwo\t\u009b"); got != "one\ntwo" {
		t.Fatalf("line sanitizer = %q", got)
	}
	if serverForgeError(nil) != nil {
		t.Fatal("nil forge error changed")
	}
	plain := errors.New("plain")
	if serverForgeError(plain) != plain {
		t.Fatal("uncategorized forge error changed")
	}
	var mapped *aiError
	if err := serverForgeError(&forge.Error{Code: http.StatusUnprocessableEntity, Message: "invalid"}); !errors.As(err, &mapped) || mapped.code != http.StatusUnprocessableEntity {
		t.Fatalf("categorized forge error = %#v", err)
	}
	if NewForgeProber(newTestStore(t)) == nil {
		t.Fatal("forge prober constructor returned nil")
	}
	if got := normalizeGuardHost(" [::1]. "); got != "::1" {
		t.Fatalf("normalized host = %q", got)
	}
	if _, err := guardedTransport(nil, false).DialContext(context.Background(), "tcp", "invalid-address"); err == nil {
		t.Fatal("guarded transport accepted invalid dial address")
	}
	request := httptest.NewRequest(http.MethodPost, "/api/board", nil)
	request.Header.Set("Content-Type", `application/json; broken`)
	if contentTypeAllowed(request) {
		t.Fatal("malformed content type accepted")
	}
}
