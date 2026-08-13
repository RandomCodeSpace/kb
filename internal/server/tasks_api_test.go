package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/board"
)

func decodeAPITask(t *testing.T, body string) apiTask {
	t.Helper()
	var task apiTask
	if err := json.Unmarshal([]byte(body), &task); err != nil {
		t.Fatalf("decode task: %v\n%s", err, body)
	}
	return task
}

func TestTasksAPILifecycle(t *testing.T) {
	h, _ := newTestServer(t, Config{})

	// Create.
	w := doReq(t, h, "POST", "/api/tasks", `{"title":"Alpha","tags":["a"],"checks":[{"text":"step"}]}`, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", w.Code, w.Body)
	}
	created := decodeAPITask(t, w.Body.String())
	if created.Seq != 1 || created.Title != "Alpha" || created.ID == "" {
		t.Fatalf("created = %+v", created)
	}

	// List with and without filter.
	if w = doReq(t, h, "GET", "/api/tasks", "", nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"seq": 1`) && !strings.Contains(w.Body.String(), `"seq":1`) {
		t.Fatalf("list = %d %s", w.Code, w.Body)
	}
	if w = doReq(t, h, "GET", "/api/tasks?status=done", "", nil); w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("filtered list = %d %s", w.Code, w.Body)
	}
	if w = doReq(t, h, "GET", "/api/tasks?status=bogus", "", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("bad filter = %d", w.Code)
	}

	// Detail with comments and links.
	if w = doReq(t, h, "POST", "/api/tasks/1/comments", `{"body":"note"}`, nil); w.Code != http.StatusCreated {
		t.Fatalf("add comment = %d %s", w.Code, w.Body)
	}
	if w = doReq(t, h, "POST", "/api/tasks", `{"title":"Beta"}`, nil); w.Code != http.StatusCreated {
		t.Fatalf("second create = %d", w.Code)
	}
	if w = doReq(t, h, "POST", "/api/links", `{"blocker":"1","blocked":"2"}`, nil); w.Code != http.StatusCreated {
		t.Fatalf("link = %d %s", w.Code, w.Body)
	}
	w = doReq(t, h, "GET", "/api/tasks/2", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("detail = %d", w.Code)
	}
	var detail struct {
		Seq       int   `json:"seq"`
		BlockedBy []int `json:"blockedBy"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil || detail.Seq != 2 || len(detail.BlockedBy) != 1 {
		t.Fatalf("detail = %+v err=%v body=%s", detail, err, w.Body)
	}

	// The blocker gates completion: 409, force overrides.
	if w = doReq(t, h, "PATCH", "/api/tasks/2", `{"status":"done"}`, nil); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "open blocker") {
		t.Fatalf("gated done = %d %s", w.Code, w.Body)
	}
	if w = doReq(t, h, "PATCH", "/api/tasks/2", `{"status":"done","force":true}`, nil); w.Code != http.StatusOK {
		t.Fatalf("forced done = %d %s", w.Code, w.Body)
	}
	// The checklist guard answers 409 too.
	if w = doReq(t, h, "PATCH", "/api/tasks/1", `{"status":"done"}`, nil); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "checklist") {
		t.Fatalf("checklist gate = %d %s", w.Code, w.Body)
	}

	// Comment listing and deletion.
	if w = doReq(t, h, "GET", "/api/tasks/1/comments", "", nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"note"`) {
		t.Fatalf("comments = %d %s", w.Code, w.Body)
	}
	if w = doReq(t, h, "DELETE", "/api/comments/c1", "", nil); w.Code != http.StatusOK {
		t.Fatalf("delete comment = %d %s", w.Code, w.Body)
	}
	if w = doReq(t, h, "DELETE", "/api/comments/1", "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("delete gone comment = %d", w.Code)
	}

	// Unlink, then task deletion.
	if w = doReq(t, h, "DELETE", "/api/links?a=1&b=2", "", nil); w.Code != http.StatusNoContent {
		t.Fatalf("unlink = %d %s", w.Code, w.Body)
	}
	if w = doReq(t, h, "DELETE", "/api/links?a=1&b=2", "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("unlink gone = %d", w.Code)
	}
	if w = doReq(t, h, "DELETE", "/api/tasks/2", "", nil); w.Code != http.StatusOK {
		t.Fatalf("delete task = %d %s", w.Code, w.Body)
	}
	if w = doReq(t, h, "GET", "/api/tasks/2", "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("deleted detail = %d", w.Code)
	}
}

func TestTasksAPIValidation(t *testing.T) {
	h, _ := newTestServer(t, Config{})

	cases := []struct {
		name, method, path, body string
		want                     int
	}{
		{"create without title", "POST", "/api/tasks", `{}`, http.StatusBadRequest},
		{"create blank title", "POST", "/api/tasks", `{"title":"  "}`, http.StatusBadRequest},
		{"create bad status", "POST", "/api/tasks", `{"title":"x","status":"nope"}`, http.StatusBadRequest},
		{"create bad json", "POST", "/api/tasks", `{`, http.StatusBadRequest},
		{"create bad due", "POST", "/api/tasks", `{"title":"x","due":"8/1"}`, http.StatusBadRequest},
		{"patch missing", "PATCH", "/api/tasks/9", `{"prio":1}`, http.StatusNotFound},
		{"patch empty", "PATCH", "/api/tasks/1", `{}`, http.StatusBadRequest},
		{"patch blank title", "PATCH", "/api/tasks/1", `{"title":" "}`, http.StatusBadRequest},
		{"patch bad status", "PATCH", "/api/tasks/1", `{"status":"nowhere"}`, http.StatusBadRequest},
		{"comment empty body", "POST", "/api/tasks/1/comments", `{"body":" "}`, http.StatusBadRequest},
		{"comment missing task", "POST", "/api/tasks/9/comments", `{"body":"x"}`, http.StatusNotFound},
		{"bad comment id", "DELETE", "/api/comments/zero", "", http.StatusBadRequest},
		{"self link", "POST", "/api/links", `{"blocker":"1","blocked":"1"}`, http.StatusBadRequest},
		{"link missing task", "POST", "/api/links", `{"blocker":"1","blocked":"9"}`, http.StatusNotFound},
		{"unlink without params", "DELETE", "/api/links", "", http.StatusBadRequest},
	}
	if w := doReq(t, h, "POST", "/api/tasks", `{"title":"Seed"}`, nil); w.Code != http.StatusCreated {
		t.Fatalf("seed = %d", w.Code)
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if w := doReq(t, h, tt.method, tt.path, tt.body, nil); w.Code != tt.want {
				t.Errorf("%s %s = %d, want %d (%s)", tt.method, tt.path, w.Code, tt.want, w.Body)
			}
		})
	}

	// Duplicate links and cycles are 400s.
	if w := doReq(t, h, "POST", "/api/tasks", `{"title":"Second"}`, nil); w.Code != http.StatusCreated {
		t.Fatal("second seed failed")
	}
	if w := doReq(t, h, "POST", "/api/links", `{"blocker":"1","blocked":"2"}`, nil); w.Code != http.StatusCreated {
		t.Fatal("link failed")
	}
	if w := doReq(t, h, "POST", "/api/links", `{"blocker":"1","blocked":"2"}`, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("duplicate link = %d", w.Code)
	}
	if w := doReq(t, h, "POST", "/api/links", `{"blocker":"2","blocked":"1"}`, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("cycle link = %d", w.Code)
	}
}

func TestTasksAPIRequiresAuthAndIsolatesUsers(t *testing.T) {
	h, _ := newTestServer(t, Config{Token: "sekrit"})

	if w := doReq(t, h, "GET", "/api/tasks", "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated list = %d", w.Code)
	}
	auth := map[string]string{"Authorization": "Bearer sekrit", "X-KB-User": "alice"}
	if w := doReq(t, h, "POST", "/api/tasks", `{"title":"Mine"}`, auth); w.Code != http.StatusCreated {
		t.Fatalf("authed create = %d", w.Code)
	}
	// Another identity sees an empty board: per-user isolation.
	other := map[string]string{"Authorization": "Bearer sekrit", "X-KB-User": "bob"}
	w := doReq(t, h, "GET", "/api/tasks", "", other)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("cross-user list = %d %s", w.Code, w.Body)
	}
	if w = doReq(t, h, "GET", "/api/tasks/1", "", other); w.Code != http.StatusNotFound {
		t.Fatalf("cross-user detail = %d", w.Code)
	}
}

func TestTasksAPIPatchFields(t *testing.T) {
	h, st := newTestServer(t, Config{})
	if w := doReq(t, h, "POST", "/api/tasks", `{"title":"Patch me","desc":"old"}`, nil); w.Code != http.StatusCreated {
		t.Fatal("seed failed")
	}
	body := `{"title":"Patched","desc":"","emoji":"🚀","prio":2,"due":"2026-09-01","effort":"M","blocked":true,"tags":["t1"],"checks":[{"text":"c","done":true}]}`
	w := doReq(t, h, "PATCH", "/api/tasks/1", body, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("patch = %d %s", w.Code, w.Body)
	}
	got, err := st.Task("default", "1")
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("%s|%s|%s|%d|%s|%s|%v", "Patched", "", "🚀", 2, "2026-09-01", "M", true)
	have := fmt.Sprintf("%s|%s|%s|%d|%s|%s|%v", got.Title, got.Desc, got.Emoji, got.Prio, got.Due, got.Effort, got.Blocked)
	if have != want || len(got.Tags) != 1 || len(got.Checks) != 1 || !got.Checks[0].Done {
		t.Fatalf("patched task = %+v", got)
	}
	if got.Status != board.StatusTodo {
		t.Fatalf("patch moved the task to %s", got.Status)
	}
}
