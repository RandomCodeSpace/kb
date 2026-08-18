package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "kb.db"), []byte("test-secret"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func newTestServer(t *testing.T, cfg Config) (http.Handler, *store.Store) {
	t.Helper()
	st := newTestStore(t)
	return New(cfg, st), st
}

// doReq drives a request the way a legitimate client does: a loopback Host
// and the content type the target actually parses, both of which the /api/*
// guard requires. Either can be overridden through hdr ("Host" is lifted onto
// the request, since Go keeps it out of the header map) so tests can forge
// them.
func doReq(t *testing.T, h http.Handler, method, target, body string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
		if strings.HasPrefix(target, "/api/board") {
			r.Header.Set("Content-Type", "text/markdown")
		} else {
			r.Header.Set("Content-Type", "application/json")
		}
	}
	r.Host = "127.0.0.1:8080"
	for k, v := range hdr {
		if strings.EqualFold(k, "Host") {
			r.Host = v
			continue
		}
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// putBoard performs a markdown board PUT, filling in the current version
// token when the caller supplied no If-Match: markdown PUTs without one are
// refused with 428. The token comes from a GET carrying the caller's own
// headers, so authenticated tests stay authenticated.
func putBoard(t *testing.T, h http.Handler, body string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	if _, ok := hdr["If-Match"]; ok {
		return doReq(t, h, http.MethodPut, "/api/board", body, hdr)
	}
	merged := map[string]string{"If-Match": doReq(t, h, http.MethodGet, "/api/board", "", hdr).Header().Get("ETag")}
	for k, v := range hdr {
		merged[k] = v
	}
	return doReq(t, h, http.MethodPut, "/api/board", body, merged)
}

// canonical is the markdown the server returns for a stored board: the wire
// format survives a Parse/Serialize round trip, not raw bytes.
func canonical(md string) string {
	return board.Serialize(board.Parse(md))
}

func TestSanitizeUser(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"Alice@Example.COM", "alice@example.com", false},
		{"bob", "bob", false},
		{"first.last_1-2", "first.last_1-2", false},
		{"AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE", "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", false},
		// Chars outside [a-z0-9._@-] are rejected, never substituted:
		// substitution would map distinct identities to the same board.
		{"user name+tag", "", true},
		{"foo/bar", "", true},
		{`foo\..\bar`, "", true},
		{"héllo", "", true},
		{"../../etc/passwd", "", true},
		{"..", "", true},
		{".", "", true},
		{".hidden", "", true},
		{"", "", true},
		{strings.Repeat("a", 300), "", true},
	}
	for _, tt := range tests {
		got, err := sanitizeUser(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("sanitizeUser(%q) = %q, want error", tt.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("sanitizeUser(%q) unexpected error: %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("sanitizeUser(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestOpenModeRoundTrip(t *testing.T) {
	h, st := newTestServer(t, Config{})

	if w := doReq(t, h, "GET", "/api/board", "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("GET before save: got %d, want 404", w.Code)
	}
	const in = "# My Board\n\n## To Do\n\n- [ ] 🚀 thing !1 @2026-08-01 ~M #backend\n  detail line\n  - [ ] substep\n"
	if w := putBoard(t, h, in, nil); w.Code != http.StatusNoContent {
		t.Fatalf("PUT: got %d, want 204 (body=%s)", w.Code, w.Body)
	}
	w := doReq(t, h, "GET", "/api/board", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET after save: got %d, want 200", w.Code)
	}
	if want := canonical(in); w.Body.String() != want {
		t.Errorf("GET body = %q, want %q", w.Body.String(), want)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/markdown") {
		t.Errorf("Content-Type = %q, want text/markdown", ct)
	}
	// The board lives in the store now, keyed by user.
	b, err := st.Board("default")
	if err != nil {
		t.Fatalf("store.Board: %v", err)
	}
	if b.Title != "My Board" || len(b.Tasks) != 1 || b.Tasks[0].Title != "thing" {
		t.Errorf("stored board = %q with %d tasks, want My Board with 1", b.Title, len(b.Tasks))
	}

	// Named user gets an independent board under the sanitized identity.
	hdr := map[string]string{"X-KB-User": "Alice"}
	if w := putBoard(t, h, "# alice\n", hdr); w.Code != http.StatusNoContent {
		t.Fatalf("PUT as Alice: got %d, want 204", w.Code)
	}
	if w := doReq(t, h, "GET", "/api/board", "", hdr); w.Body.String() != canonical("# alice\n") {
		t.Errorf("GET as Alice = %q, want %q", w.Body.String(), canonical("# alice\n"))
	}
	if w := doReq(t, h, "GET", "/api/board", "", nil); !strings.Contains(w.Body.String(), "thing") {
		t.Errorf("default board lost after Alice write: %q", w.Body.String())
	}
	if has, err := st.HasBoard("alice"); err != nil || !has {
		t.Errorf("HasBoard(alice) = %v, %v; want true", has, err)
	}
}

func TestTokenMode(t *testing.T) {
	h, _ := newTestServer(t, Config{Token: "s3cret"})

	if w := doReq(t, h, "GET", "/api/board", "", nil); w.Code != http.StatusUnauthorized {
		t.Errorf("no auth: got %d, want 401", w.Code)
	}
	wrong := map[string]string{"Authorization": "Bearer wrong", "X-KB-User": "bob"}
	if w := doReq(t, h, "GET", "/api/board", "", wrong); w.Code != http.StatusUnauthorized {
		t.Errorf("wrong token: got %d, want 401", w.Code)
	}
	noUser := map[string]string{"Authorization": "Bearer s3cret"}
	if w := doReq(t, h, "PUT", "/api/board", "# b\n", noUser); w.Code != http.StatusBadRequest {
		t.Errorf("missing X-KB-User: got %d, want 400", w.Code)
	}
	good := map[string]string{"Authorization": "Bearer s3cret", "X-KB-User": "bob"}
	if w := putBoard(t, h, "# b\n", good); w.Code != http.StatusNoContent {
		t.Fatalf("PUT with token: got %d, want 204 (body=%s)", w.Code, w.Body)
	}
	w := doReq(t, h, "GET", "/api/board", "", good)
	if w.Code != http.StatusOK || w.Body.String() != canonical("# b\n") {
		t.Errorf("GET with token: got %d body %q", w.Code, w.Body.String())
	}
	// Health stays unauthenticated even in token mode.
	if w := doReq(t, h, "GET", "/api/health", "", nil); w.Code != http.StatusOK {
		t.Errorf("health: got %d, want 200", w.Code)
	}
	// Every new endpoint sits behind the same auth check.
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/api/labels", ""},
		{"GET", "/api/similar?q=needle", ""},
		{"POST", "/api/tombstones", `{"task_id":"x","reason":"why"}`},
		{"GET", "/api/settings", ""},
		{"PUT", "/api/settings", `{"ai_model":"m"}`},
		{"POST", "/api/ai/test", ""},
		{"POST", "/api/ai/story", `{"mode":"create","prompt":"p"}`},
	} {
		if w := doReq(t, h, tc.method, tc.path, tc.body, nil); w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without auth: got %d, want 401", tc.method, tc.path, w.Code)
		}
	}
}

func TestSimilarEndpoint(t *testing.T) {
	t.Run("caps excludes and uses lowercase keys without Origin", func(t *testing.T) {
		h, st := newTestServer(t, Config{})
		var excluded board.Task
		for _, title := range []string{"needle alpha", "needle beta", "needle gamma", "needle delta", "needle epsilon"} {
			task, err := st.AddTask("default", board.Task{Title: title})
			if err != nil {
				t.Fatalf("AddTask: %v", err)
			}
			if excluded.ID == "" {
				excluded = task
			}
		}

		w := doReq(t, h, "GET", "/api/similar?q=needle&exclude="+excluded.ID, "", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("GET similar: got %d, want 200 (body=%s)", w.Code, w.Body)
		}
		var got struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("similar JSON: %v", err)
		}
		if len(got.Items) != 3 {
			t.Fatalf("similar items = %d, want fixed limit 3", len(got.Items))
		}
		for _, item := range got.Items {
			if item["id"] == excluded.ID {
				t.Fatal("excluded card appeared in similar results")
			}
			if len(item) != 4 {
				t.Errorf("card similar item keys = %v, want only lowercase id/title/status/via", item)
			}
			for _, key := range []string{"id", "title", "status", "via"} {
				if _, ok := item[key]; !ok {
					t.Errorf("similar item has no lowercase %q key: %v", key, item)
				}
			}
		}
		if err := st.RecordImportLinks("default", []store.ImportLink{{
			ExternalKey: "archive-import", Link: "link::gitlab#7", Title: "archive provenance import",
		}}); err != nil {
			t.Fatalf("RecordImportLinks: %v", err)
		}
		w = doReq(t, h, "GET", "/api/similar?q=archive", "", nil)
		var importGot struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &importGot); err != nil || len(importGot.Items) != 1 {
			t.Fatalf("linked similar JSON = %s (err=%v)", w.Body, err)
		}
		if len(importGot.Items[0]) != 3 || importGot.Items[0]["link"] != "link::gitlab#7" ||
			importGot.Items[0]["title"] != "archive provenance import" || importGot.Items[0]["via"] != "import" {
			t.Errorf("import similar item = %v, want lowercase title/link/via only", importGot.Items[0])
		}
	})

	// Repeated provenance exclusions prevent both imported self-match rows while
	// an unknown link remains a harmless no-op.
	t.Run("honors repeated import exclusions and ignores unknown links", func(t *testing.T) {
		h, st := newTestServer(t, Config{})
		const title = "shared imported duplicate"
		card, err := st.AddTask("default", board.Task{Title: title})
		if err != nil {
			t.Fatalf("AddTask: %v", err)
		}
		if err := st.RecordImportLinks("default", []store.ImportLink{
			{ExternalKey: "one", Link: "gitlab#1", Title: title},
			{ExternalKey: "two", Link: "gitlab#2", Title: title},
			{ExternalKey: "three", Link: "gitlab#3", Title: title},
		}); err != nil {
			t.Fatalf("RecordImportLinks: %v", err)
		}
		params := url.Values{"q": {title}}
		params.Add("exclude_link", "gitlab#1")
		params.Add("exclude_link", "gitlab#2")
		params.Add("exclude_link", "unknown#99")

		w := doReq(t, h, http.MethodGet, "/api/similar?"+params.Encode(), "", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("GET similar: got %d, want 200 (body=%s)", w.Code, w.Body)
		}
		var got similarResponse
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("similar JSON: %v", err)
		}
		if len(got.Items) != 2 {
			t.Fatalf("similar items = %+v, want card and unexcluded import", got.Items)
		}
		foundCard, foundImport := false, false
		for _, item := range got.Items {
			if item.Link == "gitlab#1" || item.Link == "gitlab#2" {
				t.Fatalf("excluded import appeared in similar results: %+v", item)
			}
			foundCard = foundCard || item.ID == card.ID
			foundImport = foundImport || item.Link == "gitlab#3"
		}
		if !foundCard || !foundImport {
			t.Fatalf("similar items = %+v, want card %q and gitlab#3", got.Items, card.ID)
		}
	})

	// The repeated query list is capped before it reaches the store; a value
	// beyond that boundary cannot expand per-request filtering work.
	t.Run("caps repeated import exclusions", func(t *testing.T) {
		h, st := newTestServer(t, Config{})
		const title = "bounded provenance sentinel"
		if err := st.RecordImportLinks("default", []store.ImportLink{{
			ExternalKey: "target",
			Link:        "gitlab#target",
			Title:       title,
		}}); err != nil {
			t.Fatalf("RecordImportLinks: %v", err)
		}
		params := url.Values{"q": {title}}
		for i := range maxSimilarExcludeLinks {
			params.Add("exclude_link", "unknown#"+strconv.Itoa(i))
		}
		params.Add("exclude_link", "gitlab#target")

		w := doReq(t, h, http.MethodGet, "/api/similar?"+params.Encode(), "", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("GET similar: got %d, want 200 (body=%s)", w.Code, w.Body)
		}
		var got similarResponse
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("similar JSON: %v", err)
		}
		if len(got.Items) != 1 || got.Items[0].Link != "gitlab#target" {
			t.Fatalf("similar items = %+v, want target beyond exclusion cap", got.Items)
		}
	})

	t.Run("returns initialized empty items without querying a closed store for fewer than three runes", func(t *testing.T) {
		st := newTestStore(t)
		h := New(Config{}, st)
		if err := st.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}

		w := doReq(t, h, "GET", "/api/similar?q=%C3%A9%C3%A9", "", nil)
		if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"items":[]}` {
			t.Fatalf("short similar = %d %q, want 200 initialized empty items", w.Code, w.Body)
		}
		if w = doReq(t, h, "GET", "/api/similar?q=three", "", nil); w.Code != http.StatusInternalServerError {
			t.Fatalf("closed store similar = %d, want 500", w.Code)
		}
	})
}

func tombstoneBody(t *testing.T, taskID, reason string) string {
	t.Helper()
	body, err := json.Marshal(map[string]string{"task_id": taskID, "reason": reason})
	if err != nil {
		t.Fatalf("marshal tombstone body: %v", err)
	}
	return string(body)
}

// TestTombstoneEndpoint keeps recording advisory and scoped while preserving
// the ordinary similarity response shape for cards without a reason.
func TestTombstoneEndpoint(t *testing.T) {
	t.Run("records and surfaces a killed reason without changing live items", func(t *testing.T) {
		h, st := newTestServer(t, Config{})
		killed, err := st.AddTask("default", board.Task{
			Title:  "Advisory marker rejected",
			Status: board.StatusCancelled,
		})
		if err != nil {
			t.Fatalf("AddTask killed: %v", err)
		}
		live, err := st.AddTask("default", board.Task{
			Title:  "Advisory marker active",
			Status: board.StatusTodo,
		})
		if err != nil {
			t.Fatalf("AddTask live: %v", err)
		}
		const reason = "Superseded by the SSO work"
		w := doReq(t, h, "POST", "/api/tombstones", tombstoneBody(t, killed.ID, reason), nil)
		if w.Code != http.StatusNoContent || w.Body.Len() != 0 {
			t.Fatalf("POST tombstone = %d %q, want 204 with no body", w.Code, w.Body)
		}
		recorded, found, err := st.Tombstone("default", killed.ID)
		if err != nil || !found || recorded.Reason != reason || recorded.KilledAt == "" {
			t.Fatalf("stored Tombstone = %+v, %t, %v", recorded, found, err)
		}

		w = doReq(t, h, "GET", "/api/similar?q=advisory+marker", "", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("GET similar = %d %q", w.Code, w.Body)
		}
		var response struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode similar response: %v", err)
		}
		var killedItem, liveItem map[string]any
		for _, item := range response.Items {
			switch item["id"] {
			case killed.ID:
				killedItem = item
			case live.ID:
				liveItem = item
			}
		}
		if killedItem == nil || liveItem == nil {
			t.Fatalf("similar items = %+v, want killed %q and live %q", response.Items, killed.ID, live.ID)
		}
		if killedItem["via"] != "killed" ||
			killedItem["reason"] != reason ||
			killedItem["killed_at"] != recorded.KilledAt {
			t.Errorf("killed similar item = %+v", killedItem)
		}
		if _, ok := liveItem["reason"]; ok {
			t.Errorf("live similar item includes reason: %+v", liveItem)
		}
		if _, ok := liveItem["killed_at"]; ok {
			t.Errorf("live similar item includes killed_at: %+v", liveItem)
		}
	})

	t.Run("rejects missing empty oversized and multiline fields", func(t *testing.T) {
		h, _ := newTestServer(t, Config{})
		tests := []struct {
			name string
			body string
		}{
			{name: "missing task id", body: `{"reason":"why"}`},
			{name: "empty reason", body: tombstoneBody(t, "task", "")},
			{name: "oversized reason", body: tombstoneBody(t, "task", strings.Repeat("x", 2001))},
			{name: "line feed", body: tombstoneBody(t, "task", "first\nsecond")},
			{name: "carriage return", body: tombstoneBody(t, "task", "first\rsecond")},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				w := doReq(t, h, "POST", "/api/tombstones", test.body, nil)
				if w.Code != http.StatusBadRequest {
					t.Fatalf("POST tombstone = %d %q, want 400", w.Code, w.Body)
				}
			})
		}
	})

	t.Run("rejects unknown and active task ids without disclosing state", func(t *testing.T) {
		h, st := newTestServer(t, Config{})
		active, err := st.AddTask("default", board.Task{Title: "Still active", Status: board.StatusTodo})
		if err != nil {
			t.Fatalf("AddTask active: %v", err)
		}
		otherUser, err := st.AddTask("alice", board.Task{Title: "Private cancellation", Status: board.StatusCancelled})
		if err != nil {
			t.Fatalf("AddTask other user: %v", err)
		}
		var bodies []string
		for _, id := range []string{"not-saved-yet", active.ID, otherUser.ID} {
			w := doReq(t, h, "POST", "/api/tombstones", tombstoneBody(t, id, "A stale reason"), nil)
			if w.Code != http.StatusConflict {
				t.Fatalf("POST tombstone %q = %d %q, want 409", id, w.Code, w.Body)
			}
			bodies = append(bodies, w.Body.String())
		}
		if bodies[0] != bodies[1] || bodies[0] != bodies[2] {
			t.Fatalf("missing, active, and cross-user errors differ: %q", bodies)
		}
	})

	t.Run("keeps a posted reason inside the authenticated scope", func(t *testing.T) {
		h, st := newTestServer(t, Config{})
		const title = "Scoped advisory sentinel"
		alice, err := st.AddTask("alice", board.Task{Title: title, Status: board.StatusCancelled})
		if err != nil {
			t.Fatalf("AddTask alice: %v", err)
		}
		bob, err := st.AddTask("bob", board.Task{Title: title, Status: board.StatusCancelled})
		if err != nil {
			t.Fatalf("AddTask bob: %v", err)
		}
		const bobReason = "Rejected only for bob"
		bobHeaders := map[string]string{"X-KB-User": "Bob"}
		w := doReq(t, h, "POST", "/api/tombstones", tombstoneBody(t, bob.ID, bobReason), bobHeaders)
		if w.Code != http.StatusNoContent {
			t.Fatalf("POST bob tombstone = %d %q", w.Code, w.Body)
		}

		aliceHeaders := map[string]string{"X-KB-User": "Alice"}
		w = doReq(t, h, "GET", "/api/similar?q=scoped+advisory+sentinel", "", aliceHeaders)
		var aliceResponse similarResponse
		if err := json.Unmarshal(w.Body.Bytes(), &aliceResponse); err != nil {
			t.Fatalf("decode alice similar response: %v", err)
		}
		if len(aliceResponse.Items) != 1 ||
			aliceResponse.Items[0].ID != alice.ID ||
			aliceResponse.Items[0].Reason != "" ||
			aliceResponse.Items[0].KilledAt != "" {
			t.Fatalf("alice similar response = %+v", aliceResponse)
		}

		w = doReq(t, h, "GET", "/api/similar?q=scoped+advisory+sentinel", "", bobHeaders)
		var bobResponse similarResponse
		if err := json.Unmarshal(w.Body.Bytes(), &bobResponse); err != nil {
			t.Fatalf("decode bob similar response: %v", err)
		}
		if len(bobResponse.Items) != 1 ||
			bobResponse.Items[0].ID != bob.ID ||
			bobResponse.Items[0].Reason != bobReason ||
			bobResponse.Items[0].Via != "killed" {
			t.Fatalf("bob similar response = %+v", bobResponse)
		}
	})
}
