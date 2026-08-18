package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
)

func TestPutBodyLimit(t *testing.T) {
	h, _ := newTestServer(t, Config{})

	over := strings.Repeat("x", maxBodyBytes+1)
	if w := putBoard(t, h, over, nil); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized PUT: got %d, want 413", w.Code)
	}
	exact := strings.Repeat("x", maxBodyBytes)
	if w := putBoard(t, h, exact, nil); w.Code != http.StatusNoContent {
		t.Errorf("exactly 1 MiB PUT: got %d, want 204", w.Code)
	}
}

func TestPutBoardTaskIDAcknowledgement(t *testing.T) {
	const wire = "# B\n\n## To Do\n\n- [ ] Duplicate\n  first\n- [ ] Duplicate\n  second\n"

	t.Run("application JSON returns IDs in parsed task order", func(t *testing.T) {
		h, st := newTestServer(t, Config{})
		w := putBoard(t, h, wire, map[string]string{"Accept": "application/json"})
		if w.Code != http.StatusOK {
			t.Fatalf("PUT = %d %q, want 200", w.Code, w.Body)
		}
		if w.Header().Get("ETag") == "" {
			t.Fatal("PUT returned no ETag")
		}
		if got := w.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
			t.Fatalf("Content-Type = %q, want application/json", got)
		}
		var response struct {
			TaskIDs []string `json:"task_ids"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode PUT response: %v (body=%s)", err, w.Body)
		}
		parsed := board.Parse(wire)
		stored, err := st.Board("default")
		if err != nil {
			t.Fatalf("store.Board: %v", err)
		}
		if len(response.TaskIDs) != len(parsed.Tasks) || len(stored.Tasks) != len(parsed.Tasks) {
			t.Fatalf("task IDs/stored/parsed lengths = %d/%d/%d",
				len(response.TaskIDs), len(stored.Tasks), len(parsed.Tasks))
		}
		for i := range parsed.Tasks {
			if response.TaskIDs[i] != stored.Tasks[i].ID ||
				stored.Tasks[i].Desc != parsed.Tasks[i].Desc {
				t.Errorf("task[%d] acknowledgement/stored = (%q, %q), want (%q, %q)",
					i, response.TaskIDs[i], stored.Tasks[i].Desc, stored.Tasks[i].ID, parsed.Tasks[i].Desc)
			}
		}

		// Moving the first of two equal-title cards makes ReplaceBoard reuse
		// identities by wire position. The acknowledgement must report that
		// actual assignment rather than guessing from the duplicate title.
		const movedWire = "# B\n\n## To Do\n\n- [ ] Duplicate\n  second\n\n## Cancelled\n\n- [ ] Duplicate\n  first\n"
		w = putBoard(t, h, movedWire, map[string]string{"Accept": "application/json"})
		if w.Code != http.StatusOK {
			t.Fatalf("moved PUT = %d %q, want 200", w.Code, w.Body)
		}
		var movedResponse struct {
			TaskIDs []string `json:"task_ids"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &movedResponse); err != nil {
			t.Fatalf("decode moved PUT response: %v", err)
		}
		if len(movedResponse.TaskIDs) != 2 ||
			movedResponse.TaskIDs[0] != response.TaskIDs[0] ||
			movedResponse.TaskIDs[1] != response.TaskIDs[1] {
			t.Fatalf("moved task IDs = %v, want positional identities %v",
				movedResponse.TaskIDs, response.TaskIDs)
		}

		const reason = "Rejected after save"
		w = doReq(t, h, "POST", "/api/tombstones",
			tombstoneBody(t, movedResponse.TaskIDs[1], reason), nil)
		if w.Code != http.StatusNoContent {
			t.Fatalf("POST tombstone = %d %q, want 204", w.Code, w.Body)
		}
		got, found, err := st.Tombstone("default", movedResponse.TaskIDs[1])
		if err != nil || !found || got.Reason != reason {
			t.Fatalf("Tombstone = %+v, %t, %v; want returned ID and reason", got, found, err)
		}
		hits, err := st.SearchSimilar("default", "Duplicate", "", nil, 10)
		if err != nil {
			t.Fatalf("SearchSimilar: %v", err)
		}
		var live, killed bool
		for _, hit := range hits {
			switch hit.ID {
			case movedResponse.TaskIDs[0]:
				live = hit.Via == "card" && hit.Reason == ""
			case movedResponse.TaskIDs[1]:
				killed = hit.Via == "killed" && hit.Reason == reason
			}
		}
		if !live || !killed {
			t.Fatalf("duplicate-title hits = %+v, want live first and killed second", hits)
		}
	})

	t.Run("caller without JSON Accept keeps legacy no-content response", func(t *testing.T) {
		h, _ := newTestServer(t, Config{})
		w := putBoard(t, h, wire, nil)
		if w.Code != http.StatusNoContent || w.Body.Len() != 0 {
			t.Fatalf("PUT = %d %q, want 204 with no body", w.Code, w.Body)
		}
		if w.Header().Get("ETag") == "" {
			t.Fatal("PUT returned no ETag")
		}
	})
}

// A response assembled from incidental task-slice order would pair browser ids
// with the wrong canonical cards as soon as that slice stops being status-first.
func TestBoardTaskIDsUseMarkdownWireOrder(t *testing.T) {
	b := board.Board{Tasks: []board.Task{
		{ID: "done", Title: "Duplicate", Status: board.StatusDone},
		{ID: "todo-first", Title: "Duplicate", Status: board.StatusTodo},
		{ID: "doing", Title: "Other", Status: board.StatusDoing},
		{ID: "todo-second", Title: "Duplicate", Status: board.StatusTodo},
	}}

	got := boardTaskIDs(b)
	want := []string{"todo-first", "todo-second", "doing", "done"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("boardTaskIDs = %v, want markdown wire order %v", got, want)
	}
}

// A fresh browser parse has new ids, so its first edit needs a positional
// acknowledgement from the exact markdown snapshot it loaded.
func TestGetBoardTaskIDAcknowledgement(t *testing.T) {
	const wire = "# B\n\n## To Do\n\n- [ ] Duplicate\n  first\n- [ ] Duplicate\n  second\n\n## Doing\n\n- [ ] Other\n\n## Done\n\n- [x] Duplicate\n  done\n"
	h, _ := newTestServer(t, Config{})
	put := putBoard(t, h, wire, map[string]string{"Accept": "application/json"})
	if put.Code != http.StatusOK {
		t.Fatalf("seed PUT = %d %q, want 200", put.Code, put.Body)
	}
	var putResponse struct {
		TaskIDs []string `json:"task_ids"`
	}
	if err := json.Unmarshal(put.Body.Bytes(), &putResponse); err != nil {
		t.Fatalf("decode PUT response: %v", err)
	}

	got := doReq(t, h, "GET", "/api/board", "", map[string]string{
		"Accept": "application/json",
	})
	if got.Code != http.StatusOK {
		t.Fatalf("JSON GET = %d %q, want 200", got.Code, got.Body)
	}
	if contentType := got.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("JSON GET Content-Type = %q, want application/json", contentType)
	}
	if vary := strings.Join(got.Header().Values("Vary"), ","); !strings.Contains(vary, "Accept") {
		t.Fatalf("JSON GET Vary = %q, want Accept", vary)
	}
	if got.Header().Get("ETag") != put.Header().Get("ETag") {
		t.Fatalf("JSON GET ETag = %q, want PUT token %q",
			got.Header().Get("ETag"), put.Header().Get("ETag"))
	}
	var response struct {
		Board   string   `json:"board"`
		TaskIDs []string `json:"task_ids"`
	}
	if err := json.Unmarshal(got.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode GET response: %v (body=%s)", err, got.Body)
	}
	if response.Board != wire {
		t.Errorf("JSON GET board = %q, want unchanged markdown %q", response.Board, wire)
	}
	if !reflect.DeepEqual(response.TaskIDs, putResponse.TaskIDs) {
		t.Errorf("JSON GET task IDs = %v, want PUT acknowledgement order %v",
			response.TaskIDs, putResponse.TaskIDs)
	}

	for name, accept := range map[string]string{
		"no Accept": "",
		"wildcard":  "*/*",
		"zero JSON": "application/json;q=0, text/markdown",
	} {
		t.Run(name+" keeps markdown", func(t *testing.T) {
			headers := map[string]string{}
			if accept != "" {
				headers["Accept"] = accept
			}
			legacy := doReq(t, h, "GET", "/api/board", "", headers)
			if legacy.Code != http.StatusOK || legacy.Body.String() != wire {
				t.Fatalf("GET = %d %q, want unchanged markdown", legacy.Code, legacy.Body)
			}
			if contentType := legacy.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/markdown") {
				t.Fatalf("Content-Type = %q, want text/markdown", contentType)
			}
			if vary := strings.Join(legacy.Header().Values("Vary"), ","); !strings.Contains(vary, "Accept") {
				t.Fatalf("Vary = %q, want Accept", vary)
			}
		})
	}
}

func TestHealthAndAPINotFoundRouting(t *testing.T) {
	h, _ := newTestServer(t, Config{})

	w := doReq(t, h, "GET", "/api/health", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("health: got %d, want 200", w.Code)
	}
	if w.Body.String() != `{"ok":true}` {
		t.Errorf("health body = %q", w.Body.String())
	}
	for _, tc := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/", ""},
		{http.MethodGet, "/some/client/route", ""},
		{http.MethodGet, "/assets/app.js", ""},
		{http.MethodGet, "/api", ""},
		{http.MethodGet, "/api/nope", ""},
		{http.MethodGet, "/api//health", ""},
		{http.MethodGet, "/api/nope/../health", ""},
		{http.MethodGet, "/api/health/", ""},
		{http.MethodHead, "/api/health", ""},
		{http.MethodOptions, "/api/health", ""},
		{http.MethodPost, "/api/health", ""},
		{http.MethodPost, "/api/board", "x"},
	} {
		w := doReq(t, h, tc.method, tc.path, tc.body, nil)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s %s: got %d body %q, want 404", tc.method, tc.path, w.Code, w.Body.String())
		}
		if allow := w.Header().Get("Allow"); allow != "" {
			t.Errorf("%s %s Allow = %q, want empty 404 response", tc.method, tc.path, allow)
		}
	}
	w = doReq(t, h, http.MethodPost, "/api/health", "x", map[string]string{"Content-Type": "text/plain"})
	if w.Code != http.StatusNotFound {
		t.Errorf("wrong method with rejected media type: got %d body %q, want 404", w.Code, w.Body.String())
	}
}

// Entra clients read the public IDs here. The endpoint is unauthenticated, so
// it must expose those two values and nothing else.
func TestConfigEndpoint(t *testing.T) {
	t.Run("unset yields empty strings", func(t *testing.T) {
		h, _ := newTestServer(t, Config{})
		w := doReq(t, h, "GET", "/api/config", "", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("GET config: got %d, want 200", w.Code)
		}
		var got map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("config JSON: %v (body=%s)", err, w.Body)
		}
		want := map[string]any{"azure_client_id": "", "azure_tenant_id": "", "auth_mode": "open"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("config = %v, want %v", got, want)
		}
	})

	t.Run("token mode is reported", func(t *testing.T) {
		h, _ := newTestServer(t, Config{Token: "s3cret"})
		w := doReq(t, h, "GET", "/api/config", "", nil)
		var got map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("config JSON: %v", err)
		}
		if got["auth_mode"] != "token" {
			t.Errorf("auth_mode = %v, want token", got["auth_mode"])
		}
	})

	t.Run("configured and readable without auth", func(t *testing.T) {
		const tenant = "11111111-2222-3333-4444-555555555555"
		st := newTestStore(t)
		// Token mode as well, to prove the secret never appears here and that
		// the endpoint answers with no Authorization header — the SPA needs
		// these IDs before it can log in.
		h := New(Config{TenantID: tenant, ClientID: "app-client-id", Token: "s3cret"}, st)
		w := doReq(t, h, "GET", "/api/config", "", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("GET config: got %d, want 200 (body=%s)", w.Code, w.Body)
		}
		var got map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("config JSON: %v", err)
		}
		// Entra outranks the also-set token, mirroring the auth precedence.
		want := map[string]any{"azure_client_id": "app-client-id", "azure_tenant_id": tenant, "auth_mode": "entra"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("config = %v, want exactly the two public IDs and the mode", got)
		}
		if strings.Contains(w.Body.String(), "s3cret") {
			t.Errorf("config leaks the shared secret: %s", w.Body)
		}
	})
}

// The phase-3 additions (the %blocked title token and the Cancelled section)
// have to survive the whole loop: store -> server -> wire -> client -> wire ->
// server -> store. The frozen cross-client fixture and
// internal/board/fixtures_test.go pin the client leg, so replaying that exact
// file through the API covers the rest of the loop.
func TestBlockedAndCancelledSurviveTheAPIRoundTrip(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "board", "testdata", "phase3.md"))
	if err != nil {
		t.Fatal(err)
	}
	wire := string(raw)
	h, st := newTestServer(t, Config{})

	if w := putBoard(t, h, wire, nil); w.Code != http.StatusNoContent {
		t.Fatalf("PUT: got %d, want 204 (body=%s)", w.Code, w.Body)
	}

	// The store keeps the flags as columns, not as leftover wire text.
	stored, err := st.Board("default")
	if err != nil {
		t.Fatalf("store.Board: %v", err)
	}
	byTitle := map[string]board.Task{}
	for _, task := range stored.Tasks {
		byTitle[task.Title] = task
	}
	if got := byTitle["Waiting on legal"]; !got.Blocked || got.Status != board.StatusTodo {
		t.Errorf("blocked todo stored as %+v, want blocked in todo", got)
	}
	if got := byTitle["Why %blocked matters"]; got.Blocked {
		t.Error("an escaped literal blocked token in the title set the flag through the API")
	}
	if got := byTitle["Dropped experiment"]; got.Status != board.StatusCancelled || !got.Blocked {
		t.Errorf("cancelled task stored as %+v, want blocked in cancelled", got)
	}

	// And the board the SPA reads back is byte-identical to what it sent, so
	// the next client-side parse/serialize is a no-op rather than an edit.
	w := doReq(t, h, "GET", "/api/board", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET: got %d, want 200", w.Code)
	}
	if w.Body.String() != wire {
		t.Errorf("GET body drifted from the fixture:\n--- got ---\n%s\n--- want ---\n%s", w.Body, wire)
	}

	// A second lap keeps the wire unchanged but still advances the database
	// revision because a successful replacement is a write event.
	etag := w.Header().Get("ETag")
	if w := doReq(t, h, "PUT", "/api/board", canonical(w.Body.String()),
		map[string]string{"If-Match": etag}); w.Code != http.StatusNoContent {
		t.Fatalf("second PUT: got %d, want 204 (body=%s)", w.Code, w.Body)
	}
	if again := doReq(t, h, "GET", "/api/board", "", nil); again.Body.String() != wire ||
		again.Header().Get("ETag") == etag {
		t.Errorf("second lap wire/token = (%q, %q), want unchanged wire and advanced token from %q",
			again.Body.String(), again.Header().Get("ETag"), etag)
	}
}

// A whole-board PUT would otherwise delete tasks the CLI or MCP wrote since
// the client last read. The ETag is the version token that makes the write
// conditional.
func TestBoardVersionToken(t *testing.T) {
	h, st := newTestServer(t, Config{})

	const seed = "# B\n\n## To Do\n\n- [ ] first\n"
	if w := putBoard(t, h, seed, nil); w.Code != http.StatusNoContent {
		t.Fatalf("seed PUT: got %d (body=%s)", w.Code, w.Body)
	}
	w := doReq(t, h, "GET", "/api/board", "", nil)
	etag := w.Header().Get("ETag")
	if etag == "" {
		t.Fatal("GET board returned no ETag")
	}

	// A conditional write with the token from that GET goes through.
	w = doReq(t, h, "PUT", "/api/board", "# B\n\n## To Do\n\n- [ ] first\n- [ ] second\n",
		map[string]string{"If-Match": etag})
	if w.Code != http.StatusNoContent {
		t.Fatalf("matching If-Match PUT: got %d, want 204 (body=%s)", w.Code, w.Body)
	}
	if next := w.Header().Get("ETag"); next == "" || next == etag {
		t.Errorf("PUT ETag = %q, want a new token (was %q)", next, etag)
	}

	// The stale token from the first GET must now lose.
	stale := doReq(t, h, "PUT", "/api/board", "# B\n\n## To Do\n\n- [ ] clobbered\n",
		map[string]string{"If-Match": etag})
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale If-Match PUT: got %d, want 409", stale.Code)
	}
	if stale.Header().Get("ETag") == "" {
		t.Error("409 carried no current ETag for the client to refetch against")
	}
	b, err := st.Board("default")
	if err != nil {
		t.Fatalf("store.Board: %v", err)
	}
	if len(b.Tasks) != 2 {
		t.Errorf("rejected PUT still wrote: board has %d tasks, want 2", len(b.Tasks))
	}

	// A write by another process (CLI, MCP) moves the token even though it
	// never went through this handler — which is why the token is derived
	// from content rather than from a counter this server keeps.
	current := doReq(t, h, "GET", "/api/board", "", nil).Header().Get("ETag")
	if err := st.ReplaceBoard("default", board.Parse("# B\n\n## To Do\n\n- [ ] from the CLI\n")); err != nil {
		t.Fatalf("out-of-band write: %v", err)
	}
	if doReq(t, h, "GET", "/api/board", "", nil).Header().Get("ETag") == current {
		t.Error("token did not change after an out-of-band write")
	}
	if w := doReq(t, h, "PUT", "/api/board", "# B\n", map[string]string{"If-Match": current}); w.Code != http.StatusConflict {
		t.Errorf("PUT after an out-of-band write: got %d, want 409", w.Code)
	}

	// If-Match: * only asserts existence, while a markdown PUT that carries no
	// token at all is refused: it would be a blind full-board overwrite.
	if w := doReq(t, h, "PUT", "/api/board", "# B\n\n## To Do\n\n- [ ] star\n", map[string]string{"If-Match": "*"}); w.Code != http.StatusNoContent {
		t.Errorf("If-Match * PUT: got %d, want 204", w.Code)
	}
	if w := doReq(t, h, "PUT", "/api/board", "# B\n\n## To Do\n\n- [ ] plain\n", nil); w.Code != http.StatusPreconditionRequired {
		t.Errorf("PUT without If-Match: got %d, want 428", w.Code)
	}
}

// A client whose first GET 404s must still learn a version token, or its
// first PUT is unconditional — and that write is exactly the one most likely
// to land on a board the CLI or MCP created moments earlier.
func TestBoardVersionTokenOnMissingBoard(t *testing.T) {
	h, st := newTestServer(t, Config{})

	w := doReq(t, h, "GET", "/api/board", "", map[string]string{
		"Accept": "application/json",
	})
	if w.Code != http.StatusNotFound {
		t.Fatalf("GET with no board: got %d, want 404", w.Code)
	}
	etag := w.Header().Get("ETag")
	if etag == "" {
		t.Fatal("404 carried no ETag, so the client's first PUT would be unconditional")
	}

	// Another surface creates the board between the client's GET and its PUT.
	if err := st.ReplaceBoard("default", board.Parse("# Real\n\n## To Do\n\n- [ ] important CLI task\n")); err != nil {
		t.Fatalf("out-of-band write: %v", err)
	}
	seed := "# kb\n\n## To Do\n\n- [ ] demo card\n"
	if w := doReq(t, h, "PUT", "/api/board", seed, map[string]string{"If-Match": etag}); w.Code != http.StatusConflict {
		t.Fatalf("first PUT after an out-of-band create: got %d, want 409", w.Code)
	}
	b, err := st.Board("default")
	if err != nil {
		t.Fatalf("store.Board: %v", err)
	}
	if len(b.Tasks) != 1 || b.Tasks[0].Title != "important CLI task" {
		t.Errorf("board after the rejected PUT = %+v, want the CLI task intact", b.Tasks)
	}

	// With nothing written in between, the same token still matches.
	h2, _ := newTestServer(t, Config{})
	fresh := doReq(t, h2, "GET", "/api/board", "", nil).Header().Get("ETag")
	if w := doReq(t, h2, "PUT", "/api/board", seed, map[string]string{"If-Match": fresh}); w.Code != http.StatusNoContent {
		t.Errorf("first PUT onto an empty board: got %d, want 204 (body=%s)", w.Code, w.Body)
	}
}

func TestJSONBoardPutPreservesCanonicalIdentity(t *testing.T) {
	h, st := newTestServer(t, Config{})
	const seed = "# B\n\n## To Do\n\n- [ ] Duplicate\n  first\n- [ ] Duplicate\n  second\n"
	seeded := putBoard(t, h, seed, map[string]string{"Accept": "application/json"})
	if seeded.Code != http.StatusOK {
		t.Fatalf("seed PUT = %d %q", seeded.Code, seeded.Body)
	}
	var seedAck struct {
		TaskIDs []string `json:"task_ids"`
	}
	if err := json.Unmarshal(seeded.Body.Bytes(), &seedAck); err != nil || len(seedAck.TaskIDs) != 2 {
		t.Fatalf("seed acknowledgement = %q err=%v", seeded.Body, err)
	}

	next := "# B2\n\n## To Do\n\n- [ ] Duplicate\n  second renamed\n- [ ] New\n\n## Doing\n\n- [ ] Duplicate\n  first moved\n"
	payload, err := json.Marshal(map[string]any{
		"board":    next,
		"task_ids": []any{seedAck.TaskIDs[1], nil, seedAck.TaskIDs[0]},
	})
	if err != nil {
		t.Fatal(err)
	}
	w := doReq(t, h, http.MethodPut, "/api/board", string(payload), map[string]string{
		"Content-Type":    "application/json",
		"Accept":          "application/json",
		"If-Match":        `"old-content-hash", ` + seeded.Header().Get("ETag"),
		"Idempotency-Key": "6fa459ea-ee8a-3ca4-894e-db77e160355e",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("JSON PUT = %d %q", w.Code, w.Body)
	}
	var ack struct {
		TaskIDs []string `json:"task_ids"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &ack); err != nil || len(ack.TaskIDs) != 3 {
		t.Fatalf("JSON acknowledgement = %q err=%v", w.Body, err)
	}
	if ack.TaskIDs[0] != seedAck.TaskIDs[1] || ack.TaskIDs[2] != seedAck.TaskIDs[0] || ack.TaskIDs[1] == "" {
		t.Fatalf("committed IDs = %v, want [%s new %s]", ack.TaskIDs, seedAck.TaskIDs[1], seedAck.TaskIDs[0])
	}
	stored, err := st.ReadBoardSnapshot("default")
	if err != nil || !reflect.DeepEqual(stored.TaskIDs, ack.TaskIDs) {
		t.Fatalf("stored IDs = %v err=%v, want %v", stored.TaskIDs, err, ack.TaskIDs)
	}
	legacyResponse := doReq(t, h, http.MethodPut, "/api/board", string(payload), map[string]string{
		"Content-Type":    "application/json",
		"If-Match":        w.Header().Get("ETag"),
		"Idempotency-Key": "6fa459ea-ee8a-3ca4-894e-db77e160355e",
	})
	if legacyResponse.Code != http.StatusOK || legacyResponse.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("receipt replay with legacy Accept = %d %q", legacyResponse.Code, legacyResponse.Body)
	}
}

func TestJSONBoardPutValidationIsNonDisclosingAndStaleWins(t *testing.T) {
	h, st := newTestServer(t, Config{})
	seed := putBoard(t, h, "# B\n\n## To Do\n\n- [ ] One\n- [ ] Two\n", map[string]string{"Accept": "application/json"})
	var ack struct {
		TaskIDs []string `json:"task_ids"`
	}
	if err := json.Unmarshal(seed.Body.Bytes(), &ack); err != nil || len(ack.TaskIDs) != 2 {
		t.Fatalf("seed acknowledgement = %q err=%v", seed.Body, err)
	}
	foreign, err := st.AddTask("other", board.Task{Title: "Foreign", Status: board.StatusTodo, Prio: 3})
	if err != nil {
		t.Fatal(err)
	}
	etag := seed.Header().Get("ETag")
	markdown := "# B\n\n## To Do\n\n- [ ] One\n- [ ] Two\n"
	cases := []string{
		`{"board":"` + strings.ReplaceAll(markdown, "\n", `\n`) + `","task_ids":["` + ack.TaskIDs[0] + `"]}`,
		`{"board":"` + strings.ReplaceAll(markdown, "\n", `\n`) + `","task_ids":["` + ack.TaskIDs[0] + `","` + ack.TaskIDs[0] + `"]}`,
		`{"board":"` + strings.ReplaceAll(markdown, "\n", `\n`) + `","task_ids":["not-a-uuid",null]}`,
		`{"board":"` + strings.ReplaceAll(markdown, "\n", `\n`) + `","task_ids":["00000000-0000-4000-8000-000000000000",null]}`,
		`{"board":"` + strings.ReplaceAll(markdown, "\n", `\n`) + `","task_ids":["` + foreign.ID + `",null]}`,
		`{"board":"` + strings.ReplaceAll(markdown, "\n", `\n`) + `","task_ids":[7,null]}`,
		`{"board":"` + strings.ReplaceAll(markdown, "\n", `\n`) + `","task_ids":[null,null],"extra":true}`,
		`{"board":"` + strings.ReplaceAll(markdown, "\n", `\n`) + `","board":"# Other","task_ids":[null,null]}`,
	}
	for i, payload := range cases {
		w := doReq(t, h, http.MethodPut, "/api/board", payload, map[string]string{
			"Content-Type": "application/json", "If-Match": etag,
		})
		if w.Code != http.StatusBadRequest || strings.TrimSpace(w.Body.String()) != "invalid board payload" {
			t.Errorf("case %d = %d %q, want uniform 400", i, w.Code, w.Body)
		}
	}
	invalidBoardTypes := []struct {
		name  string
		value string
	}{
		{name: "null", value: `null`},
		{name: "boolean", value: `true`},
		{name: "number", value: `7`},
		{name: "array", value: `[]`},
		{name: "object", value: `{}`},
	}
	beforeInvalidTypes, err := st.ReadBoardSnapshot("default")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range invalidBoardTypes {
		t.Run("current rejects board "+tc.name, func(t *testing.T) {
			payload := `{"board":` + tc.value + `,"task_ids":[null,null]}`
			w := doReq(t, h, http.MethodPut, "/api/board", payload, map[string]string{
				"Content-Type": "application/json", "If-Match": etag,
			})
			if w.Code != http.StatusBadRequest || strings.TrimSpace(w.Body.String()) != "invalid board payload" {
				t.Fatalf("board %s = %d %q, want uniform 400", tc.name, w.Code, w.Body)
			}
			after, err := st.ReadBoardSnapshot("default")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(after, beforeInvalidTypes) {
				t.Fatalf("board %s changed snapshot: before=%+v after=%+v", tc.name, beforeInvalidTypes, after)
			}
		})
	}

	if _, err := st.AddTask("default", board.Task{Title: "Intervening", Status: board.StatusTodo, Prio: 3}); err != nil {
		t.Fatal(err)
	}
	stale := doReq(t, h, http.MethodPut, "/api/board", cases[2], map[string]string{
		"Content-Type": "application/json", "If-Match": etag,
	})
	if stale.Code != http.StatusConflict || stale.Header().Get("ETag") == "" || stale.Header().Get("ETag") == etag {
		t.Fatalf("stale invalid PUT = %d etag=%q body=%q, want 409 with current token", stale.Code, stale.Header().Get("ETag"), stale.Body)
	}
	currentETag := stale.Header().Get("ETag")
	beforeStaleTypes, err := st.ReadBoardSnapshot("default")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range invalidBoardTypes {
		t.Run("stale rejects before board "+tc.name, func(t *testing.T) {
			payload := `{"board":` + tc.value + `,"task_ids":[null,null]}`
			w := doReq(t, h, http.MethodPut, "/api/board", payload, map[string]string{
				"Content-Type": "application/json", "If-Match": etag,
			})
			if w.Code != http.StatusConflict || w.Header().Get("ETag") != currentETag {
				t.Fatalf("stale board %s = %d etag=%q body=%q, want 409 with %q",
					tc.name, w.Code, w.Header().Get("ETag"), w.Body, currentETag)
			}
			after, err := st.ReadBoardSnapshot("default")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(after, beforeStaleTypes) {
				t.Fatalf("stale board %s changed snapshot: before=%+v after=%+v", tc.name, beforeStaleTypes, after)
			}
		})
	}
}

func TestIfMatchStarRequiresExistingBoardAndHashTokenUpgrades(t *testing.T) {
	h, _ := newTestServer(t, Config{})
	missing := doReq(t, h, http.MethodPut, "/api/board", "# B\n", map[string]string{"If-Match": "*"})
	if missing.Code != http.StatusConflict || missing.Header().Get("ETag") == "" {
		t.Fatalf("missing If-Match * = %d etag=%q", missing.Code, missing.Header().Get("ETag"))
	}
	stale := doReq(t, h, http.MethodPut, "/api/board", "# B\n", map[string]string{
		"If-Match": `"0123456789abcdef0123456789abcdef"`,
	})
	if stale.Code != http.StatusConflict || stale.Header().Get("ETag") != missing.Header().Get("ETag") {
		t.Fatalf("old hash token = %d etag=%q, want 409 current %q", stale.Code, stale.Header().Get("ETag"), missing.Header().Get("ETag"))
	}
	retry := doReq(t, h, http.MethodPut, "/api/board", "# B\n", map[string]string{"If-Match": stale.Header().Get("ETag")})
	if retry.Code != http.StatusNoContent {
		t.Fatalf("refetch/retry = %d %q", retry.Code, retry.Body)
	}
}

func TestIfMatchStarPredicateIsTransactional(t *testing.T) {
	t.Run("missing snapshot followed by concurrent create succeeds", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "kb.db")
		a, err := store.Open(path, []byte("test-secret"))
		if err != nil {
			t.Fatal(err)
		}
		defer a.Close()
		b, err := store.Open(path, []byte("test-secret"))
		if err != nil {
			t.Fatal(err)
		}
		defer b.Close()
		srv := newServer(Config{}, a)
		srv.afterConditionalBoardSnapshot = func() {
			srv.afterConditionalBoardSnapshot = nil
			if err := b.ReplaceBoard("default", board.Parse("# Peer\n")); err != nil {
				t.Errorf("concurrent create: %v", err)
			}
		}
		w := doReq(t, srv.handler(), http.MethodPut, "/api/board", "# Star\n", map[string]string{"If-Match": "*"})
		if w.Code != http.StatusNoContent {
			t.Fatalf("wildcard after concurrent create = %d %q", w.Code, w.Body)
		}
		snapshot, err := a.ReadBoardSnapshot("default")
		if err != nil || snapshot.Board.Title != "Star" {
			t.Fatalf("committed board = %+v err=%v", snapshot.Board, err)
		}
	})
	t.Run("concurrent mutation still exists", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "kb.db")
		a, err := store.Open(path, []byte("test-secret"))
		if err != nil {
			t.Fatal(err)
		}
		defer a.Close()
		b, err := store.Open(path, []byte("test-secret"))
		if err != nil {
			t.Fatal(err)
		}
		defer b.Close()
		if err := a.ReplaceBoard("default", board.Parse("# B\n\n## To Do\n\n- [ ] Seed\n")); err != nil {
			t.Fatal(err)
		}
		srv := newServer(Config{}, a)
		srv.afterConditionalBoardSnapshot = func() {
			srv.afterConditionalBoardSnapshot = nil
			if _, err := b.AddTask("default", board.Task{Title: "Concurrent"}); err != nil {
				t.Errorf("concurrent mutation: %v", err)
			}
		}
		w := doReq(t, srv.handler(), http.MethodPut, "/api/board", "# Star\n", map[string]string{"If-Match": "*"})
		if w.Code != http.StatusNoContent {
			t.Fatalf("wildcard after mutation = %d %q", w.Code, w.Body)
		}
	})
	t.Run("concurrent deletion conflicts", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "kb.db")
		a, err := store.Open(path, []byte("test-secret"))
		if err != nil {
			t.Fatal(err)
		}
		defer a.Close()
		b, err := store.Open(path, []byte("test-secret"))
		if err != nil {
			t.Fatal(err)
		}
		defer b.Close()
		if err := a.ReplaceBoard("default", board.Parse("# B\n\n## To Do\n\n- [ ] Seed\n")); err != nil {
			t.Fatal(err)
		}
		srv := newServer(Config{}, a)
		srv.afterConditionalBoardSnapshot = func() {
			srv.afterConditionalBoardSnapshot = nil
			if err := b.DeleteBoard("default"); err != nil {
				t.Errorf("concurrent delete: %v", err)
			}
		}
		w := doReq(t, srv.handler(), http.MethodPut, "/api/board", "# Star\n", map[string]string{"If-Match": "*"})
		if w.Code != http.StatusConflict {
			t.Fatalf("wildcard after deletion = %d %q", w.Code, w.Body)
		}
	})
}

func TestJSONBoardCreateReceiptReplay(t *testing.T) {
	h, st := newTestServer(t, Config{})
	seed := putBoard(t, h, "# B\n", map[string]string{"Accept": "application/json"})
	bodyBytes, err := json.Marshal(map[string]any{
		"board": "# B\n\n## To Do\n\n- [ ] New\n", "task_ids": []any{nil},
	})
	if err != nil {
		t.Fatal(err)
	}
	body := string(bodyBytes)
	headers := map[string]string{
		"Content-Type": "application/json", "Accept": "application/json",
		"If-Match":        seed.Header().Get("ETag"),
		"Idempotency-Key": "6fa459ea-ee8a-3ca4-894e-db77e160355e",
	}
	first := doReq(t, h, http.MethodPut, "/api/board", body, headers)
	if first.Code != http.StatusOK || first.Header().Get("Idempotency-Replayed") != "" {
		t.Fatalf("first = %d replay=%q body=%q", first.Code, first.Header().Get("Idempotency-Replayed"), first.Body)
	}
	before, err := st.ReadBoardSnapshot("default")
	if err != nil {
		t.Fatal(err)
	}
	replay := doReq(t, h, http.MethodPut, "/api/board", body, headers)
	if replay.Code != http.StatusOK || replay.Header().Get("Idempotency-Replayed") != "true" || replay.Header().Get("ETag") != first.Header().Get("ETag") || replay.Body.String() != first.Body.String() {
		t.Fatalf("replay = %d replay=%q etag=%q body=%q", replay.Code, replay.Header().Get("Idempotency-Replayed"), replay.Header().Get("ETag"), replay.Body)
	}
	after, err := st.ReadBoardSnapshot("default")
	if err != nil || after.Revision != before.Revision || !reflect.DeepEqual(after.TaskIDs, before.TaskIDs) {
		t.Fatalf("replay mutated board before=%+v after=%+v err=%v", before, after, err)
	}
	mismatch := doReq(t, h, http.MethodPut, "/api/board", strings.Replace(body, "New", "Other", 1), headers)
	if mismatch.Code != http.StatusBadRequest {
		t.Fatalf("mismatched replay = %d %q", mismatch.Code, mismatch.Body)
	}
	duplicateRequest := httptest.NewRequest(http.MethodPut, "/api/board", strings.NewReader(body))
	duplicateRequest.Host = "127.0.0.1:8080"
	duplicateRequest.Header.Set("Content-Type", "application/json")
	duplicateRequest.Header.Set("Accept", "application/json")
	duplicateRequest.Header.Set("If-Match", first.Header().Get("ETag"))
	duplicateRequest.Header.Add("Idempotency-Key", "6fa459ea-ee8a-3ca4-894e-db77e160355e")
	duplicateRequest.Header.Add("Idempotency-Key", "6fa459ea-ee8a-3ca4-894e-db77e160355e")
	duplicateResponse := httptest.NewRecorder()
	h.ServeHTTP(duplicateResponse, duplicateRequest)
	if duplicateResponse.Code != http.StatusBadRequest {
		t.Fatalf("repeated idempotency fields = %d %q", duplicateResponse.Code, duplicateResponse.Body)
	}
	cross := map[string]string{}
	for k, v := range headers {
		cross[k] = v
	}
	cross["X-KB-User"] = "bob"
	crossUser := doReq(t, h, http.MethodPut, "/api/board", body, cross)
	if crossUser.Code != http.StatusConflict {
		t.Fatalf("cross-user key = %d %q", crossUser.Code, crossUser.Body)
	}
}

func TestJSONBoardReceiptReplayWinsAfterPeerCommit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kb.db")
	a, err := store.Open(path, []byte("test-secret"))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := store.Open(path, []byte("test-secret"))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err := a.ReplaceBoard("default", board.Parse("# B\n")); err != nil {
		t.Fatal(err)
	}
	srv := newServer(Config{}, a)
	h2 := New(Config{}, b)
	etag := doReq(t, srv.handler(), http.MethodGet, "/api/board", "", nil).Header().Get("ETag")
	body := `{"board":"# B\n\n## To Do\n\n- [ ] New\n","task_ids":[null]}`
	headers := map[string]string{
		"Content-Type": "application/json", "Accept": "application/json",
		"If-Match": etag, "Idempotency-Key": "6fa459ea-ee8a-3ca4-894e-db77e160355e",
	}
	var peer *httptest.ResponseRecorder
	srv.afterConditionalBoardSnapshot = func() {
		srv.afterConditionalBoardSnapshot = nil
		peer = doReq(t, h2, http.MethodPut, "/api/board", body, headers)
		if peer.Code != http.StatusOK {
			t.Fatalf("peer create = %d %q", peer.Code, peer.Body)
		}
	}
	replay := doReq(t, srv.handler(), http.MethodPut, "/api/board", body, headers)
	if replay.Code != http.StatusOK || replay.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("racing replay = %d replay=%q body=%q", replay.Code, replay.Header().Get("Idempotency-Replayed"), replay.Body)
	}
	if replay.Header().Get("ETag") != peer.Header().Get("ETag") || replay.Body.String() != peer.Body.String() {
		t.Fatalf("replay acknowledgement = %q/%q, peer = %q/%q", replay.Header().Get("ETag"), replay.Body, peer.Header().Get("ETag"), peer.Body)
	}
	snapshot, err := a.ReadBoardSnapshot("default")
	if err != nil || len(snapshot.Board.Tasks) != 1 || snapshot.Board.Tasks[0].Title != "New" {
		t.Fatalf("final board = %+v err=%v", snapshot.Board, err)
	}
}

func TestIdempotencyKeyRequiresCreationBearingJSON(t *testing.T) {
	h, _ := newTestServer(t, Config{})
	seed := putBoard(t, h, "# B\n", nil)
	key := "6fa459ea-ee8a-3ca4-894e-db77e160355e"
	markdown := doReq(t, h, http.MethodPut, "/api/board", "# B\n", map[string]string{
		"Content-Type": "text/markdown", "If-Match": seed.Header().Get("ETag"), "Idempotency-Key": key,
	})
	if markdown.Code != http.StatusBadRequest || markdown.Body.String() != "invalid board payload\n" {
		t.Fatalf("markdown idempotency key = %d %q", markdown.Code, markdown.Body)
	}
	jsonNoCreate := doReq(t, h, http.MethodPut, "/api/board", `{"board":"# B\n","task_ids":[]}`, map[string]string{
		"Content-Type": "application/json", "Accept": "application/json",
		"If-Match": seed.Header().Get("ETag"), "Idempotency-Key": key,
	})
	if jsonNoCreate.Code != http.StatusBadRequest || jsonNoCreate.Body.String() != "invalid board payload\n" {
		t.Fatalf("no-create JSON idempotency key = %d %q", jsonNoCreate.Code, jsonNoCreate.Body)
	}
}

func TestConditionalBoardPutAcrossServerInstances(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kb.db")
	a, err := store.Open(path, []byte("test-secret"))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := store.Open(path, []byte("test-secret"))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	h1, h2 := New(Config{}, a), New(Config{}, b)
	if err := a.ReplaceBoard("default", board.Parse("# B\n\n## To Do\n\n- [ ] Seed\n")); err != nil {
		t.Fatal(err)
	}
	etag := doReq(t, h1, http.MethodGet, "/api/board", "", nil).Header().Get("ETag")

	start := make(chan struct{})
	results := make(chan int, 2)
	var wg sync.WaitGroup
	for _, tc := range []struct {
		handler http.Handler
		body    string
	}{{h1, "# B\n\n## To Do\n\n- [ ] Writer A\n"}, {h2, "# B\n\n## To Do\n\n- [ ] Writer B\n"}} {
		wg.Add(1)
		go func(tc struct {
			handler http.Handler
			body    string
		}) {
			defer wg.Done()
			<-start
			r := httptest.NewRequest(http.MethodPut, "/api/board", strings.NewReader(tc.body))
			r.Host = "127.0.0.1:8080"
			r.Header.Set("Content-Type", "text/markdown")
			r.Header.Set("If-Match", etag)
			w := httptest.NewRecorder()
			tc.handler.ServeHTTP(w, r)
			results <- w.Code
		}(tc)
	}
	close(start)
	wg.Wait()
	close(results)
	counts := map[int]int{}
	for code := range results {
		counts[code]++
	}
	if counts[http.StatusNoContent] != 1 || counts[http.StatusConflict] != 1 {
		t.Fatalf("concurrent conditional results = %v, want one 204 and one 409", counts)
	}
	snapshot, err := a.ReadBoardSnapshot("default")
	if err != nil || len(snapshot.Board.Tasks) != 1 || snapshot.Board.Tasks[0].Title == "Seed" {
		t.Fatalf("committed board = %+v err=%v", snapshot.Board, err)
	}
}

// A markdown PUT with no If-Match used to be a last-writer-wins full-board
// overwrite. It is now refused outright, and the refusal has to say what the
// client must send instead.
func TestMarkdownPutWithoutIfMatchIsRefused(t *testing.T) {
	h, st := newTestServer(t, Config{})
	seedToken := doReq(t, h, http.MethodGet, "/api/board", "", nil).Header().Get("ETag")
	if w := doReq(t, h, http.MethodPut, "/api/board", "# B\n\n## To Do\n\n- [ ] Seed\n", map[string]string{
		"If-Match": seedToken,
	}); w.Code != http.StatusNoContent {
		t.Fatalf("seed PUT = %d %q", w.Code, w.Body)
	}

	w := doReq(t, h, http.MethodPut, "/api/board", "# Clobber\n", map[string]string{
		"Accept": "application/json",
	})
	if w.Code != http.StatusPreconditionRequired {
		t.Fatalf("markdown PUT without If-Match = %d %q, want 428", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), "If-Match") {
		t.Errorf("428 body = %q, want it to name the If-Match header", w.Body)
	}
	if !strings.Contains(w.Body.String(), "/api/board") {
		t.Errorf("428 body = %q, want it to point at GET /api/board", w.Body)
	}
	b, err := st.Board("default")
	if err != nil {
		t.Fatalf("store.Board: %v", err)
	}
	if len(b.Tasks) != 1 || b.Tasks[0].Title != "Seed" {
		t.Fatalf("board after the refused PUT = %+v, want the seeded task intact", b.Tasks)
	}
}

// The JSON envelope replaces the whole board too, so omitting If-Match there
// must be refused exactly like markdown. A condition synthesized from the
// handler's own read would only cover the microseconds inside the request,
// leaving the client's read/edit/write interval — where the CLI and MCP
// writes land — unprotected, and a caller could buy back the last-writer-wins
// overwrite just by swapping the content type.
func TestJSONPutWithoutIfMatchIsRefused(t *testing.T) {
	h, st := newTestServer(t, Config{})
	if err := st.ReplaceBoard("default", board.Parse("# B\n\n## To Do\n\n- [ ] Seed\n")); err != nil {
		t.Fatalf("seed board: %v", err)
	}

	w := doReq(t, h, http.MethodPut, "/api/board", `{"board":"# Clobber\n","task_ids":[]}`, map[string]string{
		"Content-Type": "application/json", "Accept": "application/json",
	})
	if w.Code != http.StatusPreconditionRequired {
		t.Fatalf("JSON PUT without If-Match = %d %q, want 428", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), "If-Match") {
		t.Errorf("428 body = %q, want it to name the If-Match header", w.Body)
	}
	b, err := st.Board("default")
	if err != nil {
		t.Fatalf("store.Board: %v", err)
	}
	if len(b.Tasks) != 1 || b.Tasks[0].Title != "Seed" {
		t.Fatalf("board after the refused JSON PUT = %+v, want the seeded task intact", b.Tasks)
	}
}

// The refusal is about the missing precondition, not about the body: the
// guard answers before the body is read, so an oversized body that would
// otherwise be a 413 still gets the 428.
func TestMarkdownPutWithoutIfMatchIsRefusedBeforeReadingBody(t *testing.T) {
	h, _ := newTestServer(t, Config{})
	w := doReq(t, h, http.MethodPut, "/api/board", strings.Repeat("x", maxBodyBytes+1), nil)
	if w.Code != http.StatusPreconditionRequired {
		t.Fatalf("oversized PUT without If-Match = %d %q, want 428", w.Code, w.Body)
	}
}

// If-Match: * is the "replace whatever is there" token, and it stays usable
// for markdown once a board exists, including a board with no tasks.
func TestMarkdownPutWithStarOnEmptyBoard(t *testing.T) {
	h, st := newTestServer(t, Config{})
	created := doReq(t, h, http.MethodPut, "/api/board", "# Empty\n", map[string]string{
		"If-Match": doReq(t, h, http.MethodGet, "/api/board", "", nil).Header().Get("ETag"),
	})
	if created.Code != http.StatusNoContent {
		t.Fatalf("create PUT = %d %q", created.Code, created.Body)
	}
	w := doReq(t, h, http.MethodPut, "/api/board", "# Empty\n\n## To Do\n\n- [ ] Star\n", map[string]string{
		"If-Match": "*",
	})
	if w.Code != http.StatusNoContent {
		t.Fatalf("If-Match * PUT onto an empty board = %d %q, want 204", w.Code, w.Body)
	}
	b, err := st.Board("default")
	if err != nil {
		t.Fatalf("store.Board: %v", err)
	}
	if len(b.Tasks) != 1 || b.Tasks[0].Title != "Star" {
		t.Fatalf("board after the wildcard PUT = %+v, want the starred task", b.Tasks)
	}
}

// A conditional markdown PUT still reports the IDs and revision of its own
// commit, even when another process replaces the board straight afterwards.
func TestConditionalPutReturnsItsOwnCommittedIDsAndRevision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kb.db")
	a, err := store.Open(path, []byte("test-secret"))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := store.Open(path, []byte("test-secret"))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	srv := newServer(Config{}, a)
	h := srv.handler()
	token := doReq(t, h, http.MethodGet, "/api/board", "", nil).Header().Get("ETag")
	w := doReq(t, h, http.MethodPut, "/api/board", "# Mine\n\n## To Do\n\n- [ ] Mine\n", map[string]string{
		"Accept": "application/json", "If-Match": token,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("conditional PUT = %d %q", w.Code, w.Body)
	}
	committed, err := b.ReadBoardSnapshot("default")
	if err != nil {
		t.Fatalf("read exact commit: %v", err)
	}
	if err := b.ReplaceBoard("default", board.Parse("# External\n\n## To Do\n\n- [ ] Intervening\n")); err != nil {
		t.Fatalf("intervening replace: %v", err)
	}
	var ack struct {
		TaskIDs []string `json:"task_ids"`
	}
	if decodeErr := json.Unmarshal(w.Body.Bytes(), &ack); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if !reflect.DeepEqual(ack.TaskIDs, committed.TaskIDs) || w.Header().Get("ETag") != boardETag(committed.Revision) {
		t.Fatalf("response IDs/ETag = %v/%q, exact commit = %v/%q",
			ack.TaskIDs, w.Header().Get("ETag"), committed.TaskIDs, boardETag(committed.Revision))
	}
	current, err := a.ReadBoardSnapshot("default")
	if err != nil || current.Revision <= committed.Revision || current.Board.Title != "External" {
		t.Fatalf("current snapshot = %+v err=%v, want later external write", current, err)
	}
	stale := doReq(t, h, http.MethodPut, "/api/board", "# Clobber\n", map[string]string{
		"If-Match": w.Header().Get("ETag"),
	})
	if stale.Code != http.StatusConflict {
		t.Fatalf("follow-up with exact older commit token = %d, want 409", stale.Code)
	}
	after, err := a.ReadBoardSnapshot("default")
	if err != nil || after.Board.Title != "External" {
		t.Fatalf("external board after rejected follow-up = %+v err=%v", after.Board, err)
	}
}

// The client-supplied If-Match is evaluated inside the store transaction, not
// against the handler's preliminary read: a writer that commits between the
// two still takes the write.
func TestJSONBoardPutConflictsWithWriterAfterSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kb.db")
	st, err := store.Open(path, []byte("test-secret"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	srv := newServer(Config{}, st)
	h := srv.handler()
	seed := putBoard(t, h, "# Seed\n\n## To Do\n\n- [ ] Keep\n", map[string]string{"Accept": "application/json"})
	if seed.Code != http.StatusOK {
		t.Fatalf("seed PUT = %d %q", seed.Code, seed.Body)
	}
	var seedAck struct {
		TaskIDs []string `json:"task_ids"`
	}
	if err := json.Unmarshal(seed.Body.Bytes(), &seedAck); err != nil || len(seedAck.TaskIDs) != 1 {
		t.Fatalf("seed acknowledgement = %q err=%v", seed.Body, err)
	}

	writer, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	tx, err := writer.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE meta SET v = 'External' WHERE k = 'board_title:default'`); err != nil {
		t.Fatal(err)
	}

	payload, err := json.Marshal(map[string]any{
		"board":    "# Mine\n\n## To Do\n\n- [ ] Keep\n",
		"task_ids": seedAck.TaskIDs,
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshotTaken := make(chan struct{})
	continueWrite := make(chan struct{})
	srv.afterConditionalBoardSnapshot = func() {
		close(snapshotTaken)
		<-continueWrite
	}
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		result <- doReq(t, h, http.MethodPut, "/api/board", string(payload), map[string]string{
			"Content-Type": "application/json",
			"Accept":       "application/json",
			"If-Match":     seed.Header().Get("ETag"),
		})
	}()

	<-snapshotTaken
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	close(continueWrite)

	w := <-result
	if w.Code != http.StatusConflict {
		t.Fatalf("JSON PUT after snapshot race = %d %q, want 409", w.Code, w.Body)
	}
	committed, err := st.ReadBoardSnapshot("default")
	if err != nil {
		t.Fatal(err)
	}
	if committed.Board.Title != "External" || len(committed.Board.Tasks) != 1 || committed.Board.Tasks[0].Title != "Keep" {
		t.Fatalf("committed external snapshot = %+v, want title External with task Keep", committed.Board)
	}
	if w.Header().Get("ETag") != boardETag(committed.Revision) || w.Header().Get("ETag") == seed.Header().Get("ETag") {
		t.Fatalf("conflict ETag = %q, want current %q after seed %q",
			w.Header().Get("ETag"), boardETag(committed.Revision), seed.Header().Get("ETag"))
	}
}

func TestIfMatchCombinesRepeatedHeaderFields(t *testing.T) {
	h, _ := newTestServer(t, Config{})
	seed := putBoard(t, h, "# B\n\n## To Do\n\n- [ ] Seed\n", nil)
	current := seed.Header().Get("ETag")
	r := httptest.NewRequest(http.MethodPut, "/api/board", strings.NewReader("# B\n\n## To Do\n\n- [ ] Updated\n"))
	r.Host = "127.0.0.1:8080"
	r.Header.Set("Content-Type", "text/markdown")
	r.Header.Add("If-Match", `"stale"`)
	r.Header.Add("If-Match", current)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("repeated If-Match fields = %d %q, current token in second field was ignored", w.Code, w.Body)
	}
}
