package server

import (
	"database/sql"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/store"
)

const (
	forgeCoverageUser        = "default"
	forgeCoverageSource      = "primary"
	forgeCoverageKey         = "gitlab:local/group/project#42"
	forgeCoverageStorageBody = "storage error\n"
)

func newForgeCoverageStore(t *testing.T) (*store.Store, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kb.db")
	st, err := store.Open(path, []byte("test-secret"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open fault-injection database: %v", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		t.Fatalf("ping fault-injection database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return st, db
}

func execForgeCoverageSQL(t *testing.T, db *sql.DB, query string) {
	t.Helper()
	if _, err := db.Exec(query); err != nil {
		t.Fatalf("inject store fault: %v", err)
	}
}

func seedForgeCoverageSource(t *testing.T, st *store.Store, baseURL, pat string) {
	t.Helper()
	if _, err := st.SetForgeSource(forgeCoverageUser, forgeCoverageSource, "gitlab", &baseURL, &pat); err != nil {
		t.Fatalf("seed forge source: %v", err)
	}
}

func seedForgeCoverageDrift(t *testing.T, st *store.Store, baseURL, pat string) {
	t.Helper()
	seedImportDrift(t, st, importDriftSeed{
		scope: forgeCoverageUser, source: forgeCoverageSource, kind: "gitlab", baseURL: baseURL,
		pat: pat, externalKey: forgeCoverageKey, storedURL: baseURL + "/group/project/-/issues/42",
	})
}

func requireForgeCoverageResponse(t *testing.T, response *httptest.ResponseRecorder, code int, body string) {
	t.Helper()
	if response.Code != code || response.Body.String() != body {
		t.Fatalf("response = %d %q, want %d %q", response.Code, response.Body.String(), code, body)
	}
}

func newForgeCoverageUpstream(t *testing.T, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if strings.HasSuffix(r.URL.Path, "/notes") {
			_, _ = io.WriteString(w, `[]`)
			return
		}
		_, _ = io.WriteString(w, `{"iid":42,"title":"Current","description":"body"}`)
	}))
	t.Cleanup(upstream.Close)
	return upstream
}

func replaceImportLinksWithView(t *testing.T, db *sql.DB, projection string) {
	t.Helper()
	execForgeCoverageSQL(t, db, "ALTER TABLE import_links RENAME TO import_links_backing")
	execForgeCoverageSQL(t, db, "CREATE VIEW import_links AS SELECT "+projection+" FROM import_links_backing")
}

func TestForgeIssueListRequestPropagatesInvalidKind(t *testing.T) {
	path, query, err := forgeIssueListRequest(forgeRef{Kind: "invalid", Project: "owner/repo"})
	if path != "" || query != nil || err == nil || err.Error() != "invalid forge kind" {
		t.Fatalf("invalid-kind list request = (%q, %v, %v)", path, query, err)
	}
}

func TestSetForgeMilestoneQueryRejectsInvalidKind(t *testing.T) {
	if setForgeMilestoneQuery("invalid", nil, nil) {
		t.Fatal("invalid forge kind produced a milestone query")
	}
}

func TestParseForgeCommentsRejectsInvalidKind(t *testing.T) {
	comments, err := parseForgeComments("invalid", []byte(`[]`))
	if comments != nil || err == nil || err.Error() != "invalid forge kind" {
		t.Fatalf("invalid-kind comments = (%v, %v)", comments, err)
	}
}

func TestImportPreviewRejectsUnavailableConfiguredCredentialWithoutEgress(t *testing.T) {
	st, db := newForgeCoverageStore(t)
	seedForgeCoverageSource(t, st, "https://forge.example", "secret")
	execForgeCoverageSQL(t, db, "UPDATE forge_sources SET pat_enc = X'00'")
	var calls atomic.Int32
	s := &server{store: st, forgeClient: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("forbidden egress")
	})}}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/import/preview",
		strings.NewReader(`{"source":"primary","ref":"https://forge.example/group/project"}`))
	s.handleImportPreview(response, request, forgeCoverageUser)
	requireForgeCoverageResponse(t, response, http.StatusBadRequest, configuredSourceUnavailableMessage+"\n")
	if calls.Load() != 0 {
		t.Fatalf("rejected preview made %d forge calls", calls.Load())
	}
}

func TestImportPreviewReturnsStorageErrorWhenDuplicateLookupFails(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, `[{"iid":42,"title":"Current","description":"body"}]`)
	}))
	t.Cleanup(upstream.Close)
	st, db := newForgeCoverageStore(t)
	seedForgeCoverageSource(t, st, upstream.URL, "")
	execForgeCoverageSQL(t, db, "DROP TABLE tasks")
	s := &server{store: st, forgeClient: upstream.Client()}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/import/preview",
		strings.NewReader(`{"source":"primary","ref":"`+upstream.URL+`/group/project"}`))
	s.handleImportPreview(response, request, forgeCoverageUser)
	requireForgeCoverageResponse(t, response, http.StatusInternalServerError, forgeCoverageStorageBody)
	if calls.Load() != 1 {
		t.Fatalf("duplicate lookup failure made %d forge calls, want 1", calls.Load())
	}
}

func TestImportLinksReturnsStorageErrorWhenWriteFails(t *testing.T) {
	st, db := newForgeCoverageStore(t)
	seedForgeCoverageSource(t, st, "https://forge.example", "")
	execForgeCoverageSQL(t, db, `CREATE TRIGGER fail_import_write BEFORE INSERT ON import_links BEGIN SELECT RAISE(ABORT, 'failed'); END`)
	s := &server{store: st}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/import/links", strings.NewReader(
		`{"source":"primary","items":[{"external_key":"key","link":"gitlab#42","url":"https://forge.example/42","title":"title"}]}`))
	s.handleImportLinks(response, request, forgeCoverageUser)
	requireForgeCoverageResponse(t, response, http.StatusInternalServerError, forgeCoverageStorageBody)
	found, err := st.ImportedAs(forgeCoverageUser, []string{"key"})
	if err != nil || len(found) != 0 {
		t.Fatalf("failed import write left state = (%v, %v)", found, err)
	}
}

func TestImportDriftReturnsStorageErrorWhenBaselineLookupFails(t *testing.T) {
	var calls atomic.Int32
	upstream := newForgeCoverageUpstream(t, &calls)
	st, db := newForgeCoverageStore(t)
	seedForgeCoverageDrift(t, st, upstream.URL, "")
	replaceImportLinksWithView(t, db, "scope, source, kind, external_key, link, url, title")
	s := &server{store: st, forgeClient: upstream.Client()}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/import/drift",
		strings.NewReader(`{"source":"primary","external_key":"`+forgeCoverageKey+`"}`))
	s.handleImportDrift(response, request, forgeCoverageUser)
	requireForgeCoverageResponse(t, response, http.StatusInternalServerError, forgeCoverageStorageBody)
	if calls.Load() != 2 {
		t.Fatalf("baseline lookup failure made %d forge calls, want 2", calls.Load())
	}
}

func TestImportDriftAcceptReturnsStorageErrorWhenBaselineLookupFails(t *testing.T) {
	var calls atomic.Int32
	upstream := newForgeCoverageUpstream(t, &calls)
	st, db := newForgeCoverageStore(t)
	seedForgeCoverageDrift(t, st, upstream.URL, "")
	replaceImportLinksWithView(t, db, "scope, source, kind, external_key, link, url, title")
	s := &server{store: st, forgeClient: upstream.Client()}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/import/drift/accept", strings.NewReader(
		`{"source":"primary","external_key":"`+forgeCoverageKey+`","revision":"`+strings.Repeat("a", 64)+`"}`))
	s.handleImportDriftAccept(response, request, forgeCoverageUser)
	requireForgeCoverageResponse(t, response, http.StatusInternalServerError, forgeCoverageStorageBody)
	if calls.Load() != 0 {
		t.Fatalf("baseline lookup failure made %d forge calls", calls.Load())
	}
}

func TestImportDriftAcceptReturnsStorageErrorWhenBaselineUpdateFails(t *testing.T) {
	var calls atomic.Int32
	upstream := newForgeCoverageUpstream(t, &calls)
	st, db := newForgeCoverageStore(t)
	seedForgeCoverageDrift(t, st, upstream.URL, "")
	old := store.NewImportBaseline("Old", "old body", "2026-01-01T00:00:00Z")
	if err := st.SetImportBaseline(forgeCoverageUser, forgeCoverageKey, old); err != nil {
		t.Fatalf("seed baseline: %v", err)
	}
	replaceImportLinksWithView(t, db, "scope, source, kind, external_key, link, url, title, imported_at, baseline_title, baseline_hash, baseline_excerpt, baseline_at")
	s := &server{store: st, forgeClient: upstream.Client()}
	current := store.NewImportBaseline("Current", "body", "")
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/import/drift/accept", strings.NewReader(
		`{"source":"primary","external_key":"`+forgeCoverageKey+`","revision":"`+importDriftRevision(current)+`"}`))
	s.handleImportDriftAccept(response, request, forgeCoverageUser)
	requireForgeCoverageResponse(t, response, http.StatusInternalServerError, forgeCoverageStorageBody)
	if calls.Load() != 1 {
		t.Fatalf("baseline update failure made %d forge calls, want 1", calls.Load())
	}
	var title string
	if err := db.QueryRow("SELECT baseline_title FROM import_links_backing WHERE external_key = ?", forgeCoverageKey).Scan(&title); err != nil || title != old.Title {
		t.Fatalf("failed baseline update left title = %q, err=%v", title, err)
	}
}

func TestAuthorizeImportDriftTargetReturnsStorageErrorWhenSourceListFails(t *testing.T) {
	st, db := newForgeCoverageStore(t)
	seedForgeCoverageDrift(t, st, "https://forge.example", "")
	execForgeCoverageSQL(t, db, "DROP TABLE forge_sources")
	response := httptest.NewRecorder()
	_, _, ok := (&server{store: st}).authorizeImportDriftTarget(response, forgeCoverageUser, forgeCoverageSource, forgeCoverageKey)
	if ok {
		t.Fatal("source-list failure authorized drift")
	}
	requireForgeCoverageResponse(t, response, http.StatusInternalServerError, forgeCoverageStorageBody)
}

func TestAuthorizeImportDriftTargetRejectsMissingSelectedSource(t *testing.T) {
	st, db := newForgeCoverageStore(t)
	seedForgeCoverageDrift(t, st, "https://forge.example", "")
	execForgeCoverageSQL(t, db, "UPDATE import_links SET source = ''")
	response := httptest.NewRecorder()
	_, _, ok := (&server{store: st}).authorizeImportDriftTarget(response, forgeCoverageUser, "", forgeCoverageKey)
	if ok {
		t.Fatal("missing selected source authorized drift")
	}
	requireForgeCoverageResponse(t, response, http.StatusBadRequest, configuredSourceUnavailableMessage+"\n")
}

func TestAuthorizeImportDriftTargetRejectsUnavailableCredential(t *testing.T) {
	st, db := newForgeCoverageStore(t)
	seedForgeCoverageDrift(t, st, "https://forge.example", "secret")
	execForgeCoverageSQL(t, db, "UPDATE forge_sources SET pat_enc = X'00'")
	response := httptest.NewRecorder()
	_, _, ok := (&server{store: st}).authorizeImportDriftTarget(response, forgeCoverageUser, forgeCoverageSource, forgeCoverageKey)
	if ok {
		t.Fatal("unavailable credential authorized drift")
	}
	requireForgeCoverageResponse(t, response, http.StatusBadRequest, configuredSourceUnavailableMessage+"\n")
}

func TestIntegrationProbeReportsRequestBuildErrorWithoutEgress(t *testing.T) {
	st, db := newForgeCoverageStore(t)
	seedForgeCoverageSource(t, st, "https://forge.example", "")
	execForgeCoverageSQL(t, db, "UPDATE forge_sources SET base_url = ''")
	var calls atomic.Int32
	s := &server{store: st, forgeClient: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("forbidden egress")
	})}}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/integrations/primary/test", nil)
	request.SetPathValue("name", forgeCoverageSource)
	s.handleTestIntegration(response, request, forgeCoverageUser)
	var result forgeTestResponse
	decodeForgeJSON(t, response, &result)
	if response.Code != http.StatusOK || result.OK || result.Error != "invalid forge base URL" || calls.Load() != 0 {
		t.Fatalf("request-build response = %d %+v, calls=%d", response.Code, result, calls.Load())
	}
}

func TestNewForgeTestRequestRejectsNilContextBeforeEgress(t *testing.T) {
	//lint:ignore SA1012 Deliberately exercise the request constructor's nil-context error.
	request, err := newForgeTestRequest(nil, "gitlab", forgeTestTarget{baseURL: "https://gitlab.example.test"})
	if request != nil || err == nil || err.Error() != "invalid forge base URL" {
		t.Fatalf("nil-context request = (%v, %v), want nil request and construction error", request, err)
	}
}

func TestForgeConnectionOKRejectsResponseDrainError(t *testing.T) {
	request, err := http.NewRequest(http.MethodGet, "https://forge.example/api/v4/version", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	s := &server{forgeClient: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: coverageReadCloser{readErr: errors.New("read failed")}}, nil
	})}}
	if s.forgeConnectionOK(request, forgeCoverageUser) {
		t.Fatal("response drain error passed the integration probe")
	}
}
