package server

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/store"
)

const (
	coverageUser       = "default"
	coverageSource     = "primary"
	coverageForgeBase  = "https://forge.example"
	coverageForgeIssue = coverageForgeBase + "/group/project/-/issues/42"
)

type aiCoverageEgress struct {
	ai    int
	forge int
}

func newAIStorageCoverageServer(t *testing.T) (*server, *store.Store, string, *aiCoverageEgress) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kb.db")
	st, err := store.Open(path, []byte("test-secret"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	egress := &aiCoverageEgress{}
	s := newServer(Config{}, testStatic, st)
	s.aiClient = blockedCoverageClient(t, &egress.ai, "AI")
	s.forgeClient = blockedCoverageClient(t, &egress.forge, "forge")
	return s, st, path, egress
}

func blockedCoverageClient(t *testing.T, calls *int, name string) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		(*calls)++
		t.Errorf("unexpected %s egress", name)
		return nil, errors.New("blocked test egress")
	})}
}

func corruptCoverageCiphertext(t *testing.T, path, statement string, args ...any) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open database for corruption: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(statement, args...); err != nil {
		t.Fatalf("corrupt encrypted value: %v", err)
	}
}

func coverageForgeSources(t *testing.T, st *store.Store) []store.ForgeSource {
	t.Helper()
	sources, err := st.ForgeSources(coverageUser)
	if err != nil {
		t.Fatalf("ForgeSources: %v", err)
	}
	return sources
}

func requireNoAIStoriesSideEffects(t *testing.T, st *store.Store, before []store.ForgeSource, egress *aiCoverageEgress) {
	t.Helper()
	if egress.ai != 0 || egress.forge != 0 {
		t.Fatalf("egress calls: AI=%d forge=%d, want zero", egress.ai, egress.forge)
	}
	if after := coverageForgeSources(t, st); !reflect.DeepEqual(after, before) {
		t.Fatalf("forge sources changed: before=%+v after=%+v", before, after)
	}
	if has, err := st.HasBoard(coverageUser); err != nil || has {
		t.Fatalf("AI stories persisted a board: has=%v err=%v", has, err)
	}
}

func requireCoverageResponse(t *testing.T, response statusBody, wantStatus int, wantBody string) {
	t.Helper()
	if response.status != wantStatus || response.body != wantBody {
		t.Fatalf("response = %d %q, want %d %q", response.status, response.body, wantStatus, wantBody)
	}
}

type statusBody struct {
	status int
	body   string
}

func coveragePostAIStories(t *testing.T, s *server, body string) statusBody {
	t.Helper()
	w := doReq(t, s.handler(), http.MethodPost, "/api/ai/stories", body, nil)
	return statusBody{status: w.Code, body: w.Body.String()}
}

func coverageForgeRequest(source string) string {
	return fmt.Sprintf(`{"url":%q,"source":%q}`, coverageForgeIssue, source)
}

func TestAIStoryReturnsStorageErrorWhenStoredKeyCannotDecrypt(t *testing.T) {
	s, st, path, egress := newAIStorageCoverageServer(t)
	baseURL, model, key := "https://ai.example", "test-model", "sk-g005-corrupt-key"
	if _, err := st.SetAISettings(coverageUser, &baseURL, &model, &key); err != nil {
		t.Fatalf("seed AI settings: %v", err)
	}
	before, err := st.AISettings(coverageUser)
	if err != nil {
		t.Fatalf("AISettings before request: %v", err)
	}
	corruptCoverageCiphertext(t, path, `UPDATE settings SET ai_key_enc = ? WHERE user = ?`, []byte("corrupt"), coverageUser)

	w := doReq(t, s.handler(), http.MethodPost, "/api/ai/story", `{"mode":"create","prompt":"story"}`, nil)
	requireCoverageResponse(t, statusBody{status: w.Code, body: w.Body.String()}, http.StatusInternalServerError, storageErrorMessage+"\n")
	if egress.ai != 0 || egress.forge != 0 {
		t.Fatalf("egress calls: AI=%d forge=%d, want zero", egress.ai, egress.forge)
	}
	if after, err := st.AISettings(coverageUser); err != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("AI settings changed: before=%+v after=%+v err=%v", before, after, err)
	}
}

func TestAIStoriesRejectsInvalidForgeReferenceWithoutSideEffects(t *testing.T) {
	s, st, _, egress := newAIStorageCoverageServer(t)
	before := coverageForgeSources(t, st)
	response := coveragePostAIStories(t, s, `{"url":"://invalid","source":"primary"}`)
	requireCoverageResponse(t, response, http.StatusBadRequest, "invalid forge reference\n")
	requireNoAIStoriesSideEffects(t, st, before, egress)
}

func TestAIStoriesRejectsMissingSelectedSourceWithoutSideEffects(t *testing.T) {
	s, st, _, egress := newAIStorageCoverageServer(t)
	if _, err := st.SetForgeSource(coverageUser, coverageSource, "gitlab", stringPointer(coverageForgeBase), nil); err != nil {
		t.Fatalf("seed forge source: %v", err)
	}
	before := coverageForgeSources(t, st)
	response := coveragePostAIStories(t, s, coverageForgeRequest("missing"))
	requireCoverageResponse(t, response, http.StatusBadRequest, configuredSourceUnavailableMessage+"\n")
	requireNoAIStoriesSideEffects(t, st, before, egress)
}

func TestAIStoriesRejectsUndecryptableSelectedSourceWithoutSideEffects(t *testing.T) {
	s, st, path, egress := newAIStorageCoverageServer(t)
	pat := "glpat-stored"
	if _, err := st.SetForgeSource(coverageUser, coverageSource, "gitlab", stringPointer(coverageForgeBase), &pat); err != nil {
		t.Fatalf("seed forge source: %v", err)
	}
	corruptCoverageCiphertext(t, path, `UPDATE forge_sources SET pat_enc = ? WHERE scope = ? AND name = ?`, []byte("corrupt"), coverageUser, coverageSource)
	before := coverageForgeSources(t, st)
	response := coveragePostAIStories(t, s, coverageForgeRequest(coverageSource))
	requireCoverageResponse(t, response, http.StatusBadRequest, configuredSourceUnavailableMessage+"\n")
	requireNoAIStoriesSideEffects(t, st, before, egress)
}

func stringPointer(value string) *string { return &value }
