package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/board"
)

func TestPutBodyLimit(t *testing.T) {
	h, _ := newTestServer(t, Config{})

	over := strings.Repeat("x", maxBodyBytes+1)
	if w := doReq(t, h, "PUT", "/api/board", over, nil); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized PUT: got %d, want 413", w.Code)
	}
	exact := strings.Repeat("x", maxBodyBytes)
	if w := doReq(t, h, "PUT", "/api/board", exact, nil); w.Code != http.StatusNoContent {
		t.Errorf("exactly 1 MiB PUT: got %d, want 204", w.Code)
	}
}

func TestPutBoardTaskIDAcknowledgement(t *testing.T) {
	const wire = "# B\n\n## To Do\n\n- [ ] Duplicate\n  first\n- [ ] Duplicate\n  second\n"

	t.Run("application JSON returns IDs in parsed task order", func(t *testing.T) {
		h, st := newTestServer(t, Config{})
		w := doReq(t, h, "PUT", "/api/board", wire, map[string]string{
			"Accept": "application/json",
		})
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
		w = doReq(t, h, "PUT", "/api/board", movedWire, map[string]string{
			"Accept": "application/json",
		})
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
		w := doReq(t, h, "PUT", "/api/board", wire, nil)
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
	put := doReq(t, h, "PUT", "/api/board", wire, map[string]string{
		"Accept": "application/json",
	})
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

func TestHealthAndStatic(t *testing.T) {
	h, _ := newTestServer(t, Config{})

	w := doReq(t, h, "GET", "/api/health", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("health: got %d, want 200", w.Code)
	}
	if w.Body.String() != `{"ok":true}` {
		t.Errorf("health body = %q", w.Body.String())
	}
	if w := doReq(t, h, "GET", "/", "", nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "spa") {
		t.Errorf("GET /: got %d body %q", w.Code, w.Body.String())
	}
	// SPA fallback for unknown non-/api paths.
	if w := doReq(t, h, "GET", "/some/client/route", "", nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "spa") {
		t.Errorf("SPA fallback: got %d body %q", w.Code, w.Body.String())
	}
	// Unknown /api paths must 404, not fall back to index.html.
	if w := doReq(t, h, "GET", "/api/nope", "", nil); w.Code != http.StatusNotFound {
		t.Errorf("GET /api/nope: got %d, want 404", w.Code)
	}
	// Wrong method on /api/board falls through to the catch-all, whose /api
	// guard rejects it rather than serving the SPA.
	if w := doReq(t, h, "POST", "/api/board", "x", nil); w.Code != http.StatusNotFound {
		t.Errorf("POST /api/board: got %d, want 404", w.Code)
	}
}

// The released binary is built without VITE_* values, so the SPA reads the
// Entra IDs here. Both are public by design, but the endpoint is
// unauthenticated, so it must expose those two and nothing else.
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
		want := map[string]any{"azure_client_id": "", "azure_tenant_id": ""}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("config = %v, want %v", got, want)
		}
	})

	t.Run("configured and readable without auth", func(t *testing.T) {
		const tenant = "11111111-2222-3333-4444-555555555555"
		st := newTestStore(t)
		// Token mode as well, to prove the secret never appears here and that
		// the endpoint answers with no Authorization header — the SPA needs
		// these IDs before it can log in.
		h := New(Config{TenantID: tenant, ClientID: "app-client-id", Token: "s3cret"}, testStatic, st)
		w := doReq(t, h, "GET", "/api/config", "", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("GET config: got %d, want 200 (body=%s)", w.Code, w.Body)
		}
		var got map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("config JSON: %v", err)
		}
		want := map[string]any{"azure_client_id": "app-client-id", "azure_tenant_id": tenant}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("config = %v, want exactly the two public IDs", got)
		}
		if strings.Contains(w.Body.String(), "s3cret") {
			t.Errorf("config leaks the shared secret: %s", w.Body)
		}
	})
}

// The phase-3 additions (the %blocked title token and the Cancelled section)
// have to survive the whole loop: store -> server -> wire -> client -> wire ->
// server -> store. The client leg is the shared codec fixture that
// src/lib/markdown.test.ts and internal/board/fixtures_test.go both pin, so
// replaying that exact file through the API covers the rest of the loop.
func TestBlockedAndCancelledSurviveTheAPIRoundTrip(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "board", "testdata", "phase3.md"))
	if err != nil {
		t.Fatal(err)
	}
	wire := string(raw)
	h, st := newTestServer(t, Config{})

	if w := doReq(t, h, "PUT", "/api/board", wire, nil); w.Code != http.StatusNoContent {
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

	// A second lap (the client re-serializing what it read) changes nothing,
	// including the version token.
	etag := w.Header().Get("ETag")
	if w := doReq(t, h, "PUT", "/api/board", canonical(w.Body.String()),
		map[string]string{"If-Match": etag}); w.Code != http.StatusNoContent {
		t.Fatalf("second PUT: got %d, want 204 (body=%s)", w.Code, w.Body)
	}
	if again := doReq(t, h, "GET", "/api/board", "", nil); again.Body.String() != wire ||
		again.Header().Get("ETag") != etag {
		t.Errorf("second lap changed the board or its token: etag %q -> %q",
			etag, again.Header().Get("ETag"))
	}
}

// A whole-board PUT would otherwise delete tasks the CLI or MCP wrote since
// the client last read. The ETag is the version token that makes the write
// conditional.
func TestBoardVersionToken(t *testing.T) {
	h, st := newTestServer(t, Config{})

	const seed = "# B\n\n## To Do\n\n- [ ] first\n"
	if w := doReq(t, h, "PUT", "/api/board", seed, nil); w.Code != http.StatusNoContent {
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
	if again := doReq(t, h, "GET", "/api/board", "", nil).Header().Get("ETag"); again == current {
		t.Error("token did not change after an out-of-band write")
	}
	if w := doReq(t, h, "PUT", "/api/board", "# B\n", map[string]string{"If-Match": current}); w.Code != http.StatusConflict {
		t.Errorf("PUT after an out-of-band write: got %d, want 409", w.Code)
	}

	// If-Match: * only asserts existence, and an unconditional PUT keeps the
	// old last-writer-wins behavior for clients that send no token.
	if w := doReq(t, h, "PUT", "/api/board", "# B\n\n## To Do\n\n- [ ] star\n", map[string]string{"If-Match": "*"}); w.Code != http.StatusNoContent {
		t.Errorf("If-Match * PUT: got %d, want 204", w.Code)
	}
	if w := doReq(t, h, "PUT", "/api/board", "# B\n\n## To Do\n\n- [ ] plain\n", nil); w.Code != http.StatusNoContent {
		t.Errorf("unconditional PUT: got %d, want 204", w.Code)
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
