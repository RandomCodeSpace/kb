package webui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/ai"
	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/forge"
	"github.com/RandomCodeSpace/kb/internal/store"
)

// --- fakes ---

// forgeFake stands in for the GitHub REST API the forge client calls. Only
// the three paths one project preview and one drift check touch are served.
type forgeFake struct {
	mu      sync.Mutex
	url     string
	fail    bool
	drifted bool
}

func (f *forgeFake) set(fail, drifted bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail, f.drifted = fail, drifted
}

func (f *forgeFake) issue(number int, title string) string {
	return fmt.Sprintf(`{"number":%d,"title":%q,"body":"body %d","html_url":"%s/owner/repo/issues/%d"}`,
		number, title, number, f.url, number)
}

func (f *forgeFake) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	fail, drifted := f.fail, f.drifted
	f.mu.Unlock()
	if fail {
		http.Error(w, "upstream is unwell", http.StatusInternalServerError)
		return
	}
	switch r.URL.EscapedPath() {
	case "/api/v3/repos/owner/repo/issues":
		fmt.Fprintf(w, "[%s,%s]", f.issue(93, "Upstream issue"), f.issue(94, "Fuzzy candidate title"))
	case "/api/v3/repos/owner/repo/issues/94":
		title := "Fuzzy candidate title"
		if drifted {
			title = "Renamed upstream"
		}
		_, _ = io.WriteString(w, f.issue(94, title))
	case "/api/v3/repos/owner/repo/issues/94/comments":
		_, _ = io.WriteString(w, `[]`)
	default:
		http.NotFound(w, r)
	}
}

// forgeModelFake is an OpenAI-compatible endpoint that answers the import
// transform with one propose_card call per packed source and every other run
// with plain text.
type forgeModelFake struct {
	mu       sync.Mutex
	proposed bool
}

func (m *forgeModelFake) serve(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	body := string(raw)
	m.mu.Lock()
	transform := strings.Contains(body, "Transform these numbered forge issues") && !m.proposed
	if transform {
		m.proposed = true
	}
	m.mu.Unlock()

	message := map[string]any{"role": "assistant", "content": "upstream summary"}
	if transform {
		var calls []any
		for i := 1; i <= maxForgeMax; i++ {
			if !strings.Contains(body, fmt.Sprintf("Source %d", i)) {
				break
			}
			calls = append(calls, map[string]any{
				"id": fmt.Sprintf("call-%d", i), "type": "function",
				"function": map[string]any{
					"name":      "propose_card",
					"arguments": fmt.Sprintf(`{"title":"Imported %d","source":%d,"prio":2,"desc":"drafted"}`, i, i),
				},
			})
		}
		message["content"], message["tool_calls"] = "", calls
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": "stop"}},
	})
}

// --- harness ---

// newForgeTestHandler wires a handler whose forge service talks to local
// httptest upstreams: the guarded production clients refuse loopback.
func newForgeTestHandler(t *testing.T) (http.Handler, *store.Store, *forgeFake) {
	t.Helper()
	fake := &forgeFake{}
	upstream := httptest.NewServer(http.HandlerFunc(fake.serve))
	t.Cleanup(upstream.Close)
	fake.url = upstream.URL

	model := &forgeModelFake{}
	aiUpstream := httptest.NewServer(http.HandlerFunc(model.serve))
	t.Cleanup(aiUpstream.Close)
	t.Setenv("KB_AI_ALLOW_PRIVATE", "1")

	previous := newForgeService
	newForgeService = func(s *server) *forge.Service {
		return forge.New(s.st, ai.NewRunner(s.st, "", aiUpstream.Client(), nil), upstream.Client())
	}
	t.Cleanup(func() { newForgeService = previous })

	h, st := newTestHandler(t)
	base := upstream.URL
	if _, err := st.SetForgeSource("default", "primary", "github", &base, nil); err != nil {
		t.Fatal(err)
	}
	aiBase, aiModel, aiKey := aiUpstream.URL, "gpt-4o", "ai-key"
	if _, err := st.SetAISettings("default", &aiBase, &aiModel, &aiKey); err != nil {
		t.Fatal(err)
	}
	return h, st, fake
}

// forgeKey is the provenance key the service derives for one upstream issue.
func forgeKey(fake *forgeFake, number int) string {
	return fmt.Sprintf("github:primary@%s/owner/repo#%d", fake.url, number)
}

func previewDrafts(t *testing.T, h http.Handler, body any) []map[string]any {
	t.Helper()
	out := wantStatus(t, call(t, h, "POST", "/api/forge/preview", body), http.StatusOK)
	raw, ok := out["drafts"].([]any)
	if !ok {
		t.Fatalf("preview has no drafts: %v", out)
	}
	drafts := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		draft, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("draft is not an object: %v", item)
		}
		drafts = append(drafts, draft)
	}
	return drafts
}

// importBody turns one previewed draft into the import request the reviewed
// card is committed with.
func importBody(draft map[string]any) map[string]any {
	return map[string]any{
		"source": "primary",
		"task": map[string]any{
			"title": draft["title"], "desc": draft["desc"], "prio": draft["prio"], "tags": draft["tags"], "project": "work",
		},
		"link": map[string]any{
			"externalKey": draft["externalKey"], "link": draft["link"],
			"url": draft["url"], "title": draft["title"], "baseline": draft["baseline"],
		},
	}
}

// --- tests ---

// TestForgePreviewMarksDuplicatesAndImports walks the overlay's whole path:
// preview with both dedupe markers, import of the reviewed card, its
// provenance, and the drift check the card detail pane runs afterwards.
func TestForgePreviewMarksDuplicatesAndImports(t *testing.T) {
	h, st, fake := newForgeTestHandler(t)
	// #93 is already on the board under its provenance tag; #94 only has a
	// same-titled card, which is the fuzzy hit.
	if _, err := st.AddTask("default", board.Task{
		Title: "Already imported", Tags: []string{"project::work", "import::" + forgeKey(fake, 93)},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddTask("default", board.Task{Title: "Fuzzy candidate title", Tags: []string{"project::work"}}); err != nil {
		t.Fatal(err)
	}

	drafts := previewDrafts(t, h, map[string]any{"source": "primary", "ref": "owner/repo", "max": 2})
	if len(drafts) != 2 {
		t.Fatalf("drafts = %v", drafts)
	}
	imported, _ := drafts[0]["duplicate"].(map[string]any)
	similar, _ := drafts[1]["duplicate"].(map[string]any)
	if imported["via"] != "link" || imported["title"] != "Already imported" {
		t.Fatalf("duplicate marker = %v", drafts[0]["duplicate"])
	}
	if similar["via"] != "similar" || similar["title"] != "Fuzzy candidate title" {
		t.Fatalf("similar marker = %v", drafts[1]["duplicate"])
	}
	draft := drafts[1]
	if draft["link"] != "github#94" || draft["externalKey"] != forgeKey(fake, 94) || draft["source"] != "primary" {
		t.Fatalf("draft provenance = %v", draft)
	}
	baseline, _ := draft["baseline"].(map[string]any)
	if baseline["title"] != "Fuzzy candidate title" || baseline["hash"] == "" || baseline["at"] == "" {
		t.Fatalf("draft baseline = %v", draft["baseline"])
	}
	tags, _ := draft["tags"].([]any)
	if !containsString(tags, "link::github#94") || !containsString(tags, "import::"+forgeKey(fake, 94)) {
		t.Fatalf("draft tags = %v", draft["tags"])
	}

	created := wantStatus(t, call(t, h, "POST", "/api/forge/import", importBody(draft)), http.StatusCreated)
	if created["title"] != "Imported 2" || created["project"] != "work" || created["status"] != "todo" {
		t.Fatalf("created = %v", created)
	}
	createdTags, _ := created["tags"].([]any)
	if !containsString(createdTags, "link::github#94") {
		t.Fatalf("created tags = %v", created["tags"])
	}
	if baseline, present, err := st.ImportBaseline("default", forgeKey(fake, 94)); err != nil || !present || baseline.Title != "Fuzzy candidate title" {
		t.Fatalf("stored baseline = %+v, %t, %v", baseline, present, err)
	}

	// A card the preview marked as already imported is not refused: the marker
	// is advisory and the reviewer decides, exactly as the overlay's unticked
	// row does.
	again := importBody(drafts[0])
	again["task"].(map[string]any)["status"] = "doing"
	repeat := wantStatus(t, call(t, h, "POST", "/api/forge/import", again), http.StatusCreated)
	if repeat["status"] != "doing" {
		t.Fatalf("re-import = %v", repeat)
	}

	links := wantStatus(t, call(t, h, "GET", "/api/forge/provenance?link="+url.QueryEscape("github#94"), nil), http.StatusOK)
	found, _ := links["links"].([]any)
	if len(found) != 1 {
		t.Fatalf("provenance = %v", links)
	}
	link, _ := found[0].(map[string]any)
	if link["externalKey"] != forgeKey(fake, 94) || link["kind"] != "github" || link["source"] != "primary" {
		t.Fatalf("provenance link = %v", link)
	}

	// The drift pane's three states, in the order a user meets them.
	selection := map[string]any{"source": "primary", "externalKey": forgeKey(fake, 94)}
	unchanged := wantStatus(t, call(t, h, "POST", "/api/forge/drift/check", selection), http.StatusOK)
	if unchanged["state"] != "unchanged" || unchanged["link"] != "github#94" {
		t.Fatalf("unchanged drift = %v", unchanged)
	}
	fake.set(false, true)
	drifted := wantStatus(t, call(t, h, "POST", "/api/forge/drift/check", selection), http.StatusOK)
	revision, _ := drifted["revision"].(string)
	if drifted["state"] != "drifted" || revision == "" || drifted["upstreamTitle"] != "Renamed upstream" ||
		drifted["titleChanged"] != true || drifted["summary"] != "upstream summary" {
		t.Fatalf("drifted = %v", drifted)
	}
	accept := map[string]any{"source": "primary", "externalKey": forgeKey(fake, 94), "revision": revision}
	accepted := wantStatus(t, call(t, h, "POST", "/api/forge/drift/accept", accept), http.StatusOK)
	if at, _ := accepted["baselineAt"].(string); at == "" {
		t.Fatalf("accepted = %v", accepted)
	}
	if baseline, _, err := st.ImportBaseline("default", forgeKey(fake, 94)); err != nil || baseline.Title != "Renamed upstream" {
		t.Fatalf("accepted baseline = %+v, %v", baseline, err)
	}
}

func containsString(values []any, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// TestForgePreviewDefaultsAndRefusals covers the input gate and the upstream
// failure mapping around the preview step.
func TestForgePreviewDefaultsAndRefusals(t *testing.T) {
	h, _, fake := newForgeTestHandler(t)
	// No max: the stepper default is applied and both issues still arrive.
	drafts := previewDrafts(t, h, map[string]any{"source": "primary", "ref": "owner/repo"})
	if len(drafts) != 2 {
		t.Fatalf("default max drafts = %v", drafts)
	}
	// An over-large max is clamped to the stepper ceiling rather than refused.
	// The scripted model proposes once per handler, so this second preview
	// legitimately draws no drafts.
	if got := previewDrafts(t, h, map[string]any{"source": "primary", "ref": "owner/repo", "max": 500}); len(got) != 0 {
		t.Fatalf("clamped max drafts = %v", got)
	}

	wantError(t, call(t, h, "POST", "/api/forge/preview", "{"), http.StatusBadRequest, "malformed JSON")
	wantError(t, call(t, h, "POST", "/api/forge/preview", map[string]any{"source": "primary"}), http.StatusBadRequest, "ref must not be empty")
	wantError(t, call(t, h, "POST", "/api/forge/preview", map[string]any{"source": "nope", "ref": "owner/repo"}), http.StatusNotFound, "unknown forge source")
	wantError(t, call(t, h, "POST", "/api/forge/preview", map[string]any{"source": "primary", "ref": "not a reference"}), http.StatusBadRequest, "forge reference")

	fake.set(true, false)
	wantError(t, call(t, h, "POST", "/api/forge/preview", map[string]any{"source": "primary", "ref": "owner/repo"}), http.StatusBadGateway, "forge HTTP 500: upstream request failed")
}

// TestForgeImportRejectsBadCards keeps the card gate identical to POST
// /api/tasks: title, prio, status and project are refused here, not in the
// store.
func TestForgeImportRejectsBadCards(t *testing.T) {
	h, _, fake := newForgeTestHandler(t)
	link := map[string]any{
		"externalKey": forgeKey(fake, 94), "link": "github#94",
		"url": fake.url + "/owner/repo/issues/94", "title": "Fuzzy candidate title",
		"baseline": map[string]any{"title": "Fuzzy candidate title", "hash": "abc", "excerpt": "body 94", "at": "2026-01-01T00:00:00Z"},
	}
	body := func(task map[string]any) map[string]any {
		return map[string]any{"source": "primary", "task": task, "link": link}
	}
	for _, tc := range []struct {
		name    string
		body    any
		code    int
		message string
	}{
		{"bad json", "{", http.StatusBadRequest, "malformed JSON"},
		{"unknown source", map[string]any{"source": "nope", "task": map[string]any{"title": "x"}}, http.StatusNotFound, "unknown forge source"},
		{"no title", body(map[string]any{}), http.StatusBadRequest, "title must not be empty"},
		{"bad prio", body(map[string]any{"title": "x", "prio": 9}), http.StatusBadRequest, "invalid prio"},
		{"bad status", body(map[string]any{"title": "x", "status": "later"}), http.StatusBadRequest, "invalid status"},
		{"bad project", body(map[string]any{"title": "x", "project": "not a project"}), http.StatusBadRequest, "project"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantError(t, call(t, h, "POST", "/api/forge/import", tc.body), tc.code, tc.message)
		})
	}
}

// TestForgeImportRejectsBadProvenance covers the service's own refusals: a
// link the configured source cannot own, and a card with no baseline.
func TestForgeImportRejectsBadProvenance(t *testing.T) {
	h, _, fake := newForgeTestHandler(t)
	task := map[string]any{"title": "Imported card", "project": "work"}
	baseline := map[string]any{"title": "Fuzzy candidate title", "hash": "abc", "excerpt": "body 94", "at": "2026-01-01T00:00:00Z"}
	foreign := map[string]any{
		"externalKey": forgeKey(fake, 94), "link": "github#94",
		"url": "https://github.test/owner/repo/issues/94", "title": "t", "baseline": baseline,
	}
	incomplete := map[string]any{
		"externalKey": forgeKey(fake, 94), "link": "github#94",
		"url": fake.url + "/owner/repo/issues/94", "baseline": baseline,
	}
	noBaseline := map[string]any{
		"externalKey": forgeKey(fake, 94), "link": "github#94",
		"url": fake.url + "/owner/repo/issues/94", "title": "Fuzzy candidate title",
	}
	for _, tc := range []struct {
		name    string
		link    map[string]any
		message string
	}{
		{"foreign host", foreign, "preview again"},
		{"missing title", incomplete, "import link fields required"},
		{"missing baseline", noBaseline, "invalid import baseline"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := call(t, h, "POST", "/api/forge/import", map[string]any{"source": "primary", "task": task, "link": tc.link})
			wantError(t, rec, http.StatusBadRequest, tc.message)
		})
	}
}

// TestForgeProvenanceAndDriftRefusals covers the lookup and drift gates.
func TestForgeProvenanceAndDriftRefusals(t *testing.T) {
	h, _, fake := newForgeTestHandler(t)
	wantError(t, call(t, h, "GET", "/api/forge/provenance", nil), http.StatusBadRequest, "invalid import link")
	wantError(t, call(t, h, "GET", "/api/forge/provenance?link=github%23404", nil), http.StatusNotFound, "import link not found")

	wantError(t, call(t, h, "POST", "/api/forge/drift/check", "{"), http.StatusBadRequest, "malformed JSON")
	wantError(t, call(t, h, "POST", "/api/forge/drift/check", map[string]any{"source": "primary"}), http.StatusBadRequest, "externalKey must not be empty")
	wantError(t, call(t, h, "POST", "/api/forge/drift/check", map[string]any{"source": "nope", "externalKey": "k"}), http.StatusNotFound, "unknown forge source")
	wantError(t, call(t, h, "POST", "/api/forge/drift/check", map[string]any{"source": "primary", "externalKey": forgeKey(fake, 94)}), http.StatusNotFound, "import link not found")

	wantError(t, call(t, h, "POST", "/api/forge/drift/accept", map[string]any{"source": "nope", "externalKey": "k", "revision": "x"}), http.StatusNotFound, "unknown forge source")
	wantError(t, call(t, h, "POST", "/api/forge/drift/accept", map[string]any{"source": "primary", "externalKey": forgeKey(fake, 94), "revision": "nope"}), http.StatusBadRequest, "invalid revision")
}

// TestForgeMethodsAndStorageFailures exercises the routes without the client
// seam, so the production service constructor runs, and the storage failure
// every endpoint shares.
func TestForgeMethodsAndStorageFailures(t *testing.T) {
	h, st := newTestHandler(t)
	for _, path := range []string{"/api/forge/preview", "/api/forge/import", "/api/forge/drift/check", "/api/forge/drift/accept"} {
		wantError(t, call(t, h, "GET", path, nil), http.StatusMethodNotAllowed, "not allowed")
	}
	wantError(t, call(t, h, "POST", "/api/forge/provenance", map[string]any{}), http.StatusMethodNotAllowed, "not allowed")
	wantError(t, call(t, h, "POST", "/api/forge/preview", map[string]any{"source": "primary", "ref": "owner/repo"}),
		http.StatusNotFound, "unknown forge source")

	st.Close()
	wantError(t, call(t, h, "POST", "/api/forge/preview", map[string]any{"source": "primary", "ref": "owner/repo"}),
		http.StatusInternalServerError, "storage error")
}

// TestWriteForgeErrorMapping pins the status table for failures that are
// impractical to provoke through a live upstream.
func TestWriteForgeErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		code    int
		message string
	}{
		{"forge category", &forge.Error{Code: http.StatusBadGateway, Message: "forge request failed"}, http.StatusBadGateway, "forge request failed"},
		{"ai category", &ai.Error{Code: http.StatusBadRequest, Message: "AI is not configured"}, http.StatusBadRequest, "AI is not configured"},
		{"nonsense code", &forge.Error{Code: 42, Message: "odd"}, http.StatusBadGateway, "odd"},
		{"upstream changed", fmt.Errorf("%w", forge.ErrUpstreamChanged), http.StatusConflict, "upstream changed"},
		{"deadline", fmt.Errorf("fetch: %w", context.DeadlineExceeded), http.StatusGatewayTimeout, "timed out"},
		{"uncategorized", errors.New("secret upstream detail"), http.StatusBadGateway, "forge request failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			writeForgeError(rec, tc.err)
			if rec.Code != tc.code || !strings.Contains(rec.Body.String(), tc.message) {
				t.Fatalf("status = %d body = %q, want %d containing %q", rec.Code, rec.Body.String(), tc.code, tc.message)
			}
		})
	}
	if rec := httptest.NewRecorder(); func() bool {
		writeForgeError(rec, errors.New("secret upstream detail"))
		return strings.Contains(rec.Body.String(), "secret")
	}() {
		t.Fatal("uncategorized error text was echoed to the client")
	}
}

// TestForgeDraftWireShape covers the draft fields no scripted model run
// produces on its own.
func TestForgeDraftWireShape(t *testing.T) {
	draft := forge.Draft{
		Link: "github#7", ExternalKey: "github:primary@https://example.test/owner/repo#7",
		URL: "https://example.test/owner/repo/issues/7", SourceName: "primary",
		Baseline:  store.ImportBaseline{Title: "t", Hash: "h", Excerpt: "e", At: "a"},
		Duplicate: &forge.Duplicate{ID: "id", Title: "dupe", Via: "link"},
	}
	draft.Title, draft.Emoji, draft.Desc, draft.Prio = "Card", "🐛", "desc", 2
	draft.Due, draft.Effort, draft.Tags = "2026-01-01", "M", []string{"a"}
	draft.Checks = []ai.DraftCheck{{Text: "step", Done: true}}

	out := toForgePreviewJSON(forge.Preview{Kind: "project", TotalHint: 3, Fetched: 1, Truncated: true, Note: "n", Drafts: []forge.Draft{draft}})
	if len(out.Drafts) != 1 || out.Kind != "project" || out.TotalHint != 3 || !out.Truncated {
		t.Fatalf("preview = %+v", out)
	}
	got := out.Drafts[0]
	if len(got.Checks) != 1 || got.Checks[0].Text != "step" || !got.Checks[0].Done {
		t.Fatalf("checks = %+v", got.Checks)
	}
	if got.Duplicate == nil || got.Duplicate.Via != "link" || got.Baseline.Hash != "h" || got.Source != "primary" {
		t.Fatalf("draft = %+v", got)
	}
}
