package webui

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/kb/internal/cliapp"
	"github.com/RandomCodeSpace/kb/internal/store"
)

// newTestHandler opens a fresh store in a temp data dir and returns the
// handler over it.
func newTestHandler(t *testing.T) (http.Handler, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := cliapp.OpenLocalStore(dir, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return NewHandler(st, "default", dir, "v-test"), st
}

// call issues one request. A string body is sent raw; any other non-nil body
// is JSON-encoded. Bodies get the JSON Content-Type unless a header
// overrides it.
func call(t *testing.T, h http.Handler, method, path string, body any, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		switch b := body.(type) {
		case string:
			reader = strings.NewReader(b)
		default:
			data, err := json.Marshal(b)
			if err != nil {
				t.Fatal(err)
			}
			reader = bytes.NewReader(data)
		}
	}
	req := httptest.NewRequest(method, path, reader)
	req.Host = "localhost"
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeMap(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return out
}

func wantStatus(t *testing.T, rec *httptest.ResponseRecorder, code int) map[string]any {
	t.Helper()
	if rec.Code != code {
		t.Fatalf("status = %d, want %d: %s", rec.Code, code, rec.Body.String())
	}
	if rec.Code == http.StatusNoContent || rec.Code == http.StatusNotModified {
		return nil
	}
	return decodeMap(t, rec)
}

func wantError(t *testing.T, rec *httptest.ResponseRecorder, code int, contains string) map[string]any {
	t.Helper()
	body := wantStatus(t, rec, code)
	msg, _ := body["error"].(string)
	if !strings.Contains(msg, contains) {
		t.Fatalf("error %q does not contain %q", msg, contains)
	}
	return body
}

// addTask creates a card in "work" unless the request names its project, as a
// field or as a project:: tag: the API has no ambient project, so every
// create spells one.
func addTask(t *testing.T, h http.Handler, in map[string]any) map[string]any {
	t.Helper()
	if _, named := in["project"]; !named && !hasProjectTag(in) {
		in["project"] = "work"
	}
	return wantStatus(t, call(t, h, "POST", "/api/tasks", in), http.StatusCreated)
}

func hasProjectTag(in map[string]any) bool {
	tags, _ := in["tags"].([]string)
	return slices.ContainsFunc(tags, func(tag string) bool { return strings.HasPrefix(tag, "project::") })
}

func TestMeta(t *testing.T) {
	h, _ := newTestHandler(t)
	addTask(t, h, map[string]any{"title": "a", "tags": []string{"x"}})
	addTask(t, h, map[string]any{"title": "b", "project": "inbox"})
	addTask(t, h, map[string]any{"title": "c", "project": "alpha"})

	body := wantStatus(t, call(t, h, "GET", "/api/meta", nil), http.StatusOK)
	if body["version"] != "v-test" {
		t.Fatalf("meta = %v", body)
	}
	if got, _ := json.Marshal(body["projects"]); string(got) != `["alpha","work","inbox"]` {
		t.Fatalf("projects = %s", got)
	}
	if got, _ := json.Marshal(body["statuses"]); string(got) != `["todo","doing","done","cancelled"]` {
		t.Fatalf("statuses = %s", got)
	}
	labels, _ := body["labels"].([]any)
	if len(labels) == 0 {
		t.Fatalf("labels = %v", body["labels"])
	}
	rec := call(t, h, "GET", "/api/meta", nil)
	if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("headers = %v", rec.Header())
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("CORS header emitted")
	}
}

func TestMetaEmptyBoard(t *testing.T) {
	h, _ := newTestHandler(t)
	rec := call(t, h, "GET", "/api/meta", nil)
	if got := rec.Body.String(); got != `{"version":"v-test","projects":[],"labels":[],"statuses":["todo","doing","done","cancelled"]}` {
		t.Fatalf("meta = %s", got)
	}
}

func TestMetaErrors(t *testing.T) {
	h, st := newTestHandler(t)
	st.Close()
	wantStatus(t, call(t, h, "GET", "/api/meta", nil), http.StatusInternalServerError)
	wantStatus(t, call(t, h, "GET", "/api/labels", nil), http.StatusInternalServerError)
}

func TestListTasks(t *testing.T) {
	h, _ := newTestHandler(t)
	addTask(t, h, map[string]any{"title": "alpha one", "tags": []string{"x"}})
	addTask(t, h, map[string]any{"title": "beta two", "status": "doing", "tags": []string{"x", "y"}})
	addTask(t, h, map[string]any{"title": "gamma", "project": "other"})

	count := func(path string) int {
		t.Helper()
		body := wantStatus(t, call(t, h, "GET", path, nil), http.StatusOK)
		tasks, _ := body["tasks"].([]any)
		return len(tasks)
	}
	if n := count("/api/tasks"); n != 3 {
		t.Fatalf("all = %d", n)
	}
	if n := count("/api/tasks?status=doing"); n != 1 {
		t.Fatalf("doing = %d", n)
	}
	if n := count("/api/tasks?tag=x&tag=y"); n != 1 {
		t.Fatalf("tags = %d", n)
	}
	if n := count("/api/tasks?project=work"); n != 2 {
		t.Fatalf("project = %d", n)
	}
	if n := count("/api/tasks?q=beta"); n != 1 {
		t.Fatalf("q = %d", n)
	}
	wantError(t, call(t, h, "GET", "/api/tasks?status=bogus", nil), http.StatusBadRequest, "invalid status")
	wantError(t, call(t, h, "GET", "/api/tasks?q="+strings.Repeat("a", 600), nil), http.StatusBadRequest, "too long")

	body := wantStatus(t, call(t, h, "GET", "/api/tasks?status=doing", nil), http.StatusOK)
	task := body["tasks"].([]any)[0].(map[string]any)
	for _, key := range []string{"id", "seq", "title", "status", "prio", "project", "position", "createdAt", "movedAt", "updatedAt", "tags"} {
		if _, ok := task[key]; !ok {
			t.Fatalf("task missing %q: %v", key, task)
		}
	}
	for _, key := range []string{"createdAt", "movedAt", "updatedAt"} {
		if _, err := time.Parse(time.RFC3339, task[key].(string)); err != nil {
			t.Fatalf("task %s = %v, want RFC3339: %v", key, task[key], err)
		}
	}
	if task["project"] != "work" || task["status"] != "doing" {
		t.Fatalf("task = %v", task)
	}
	if _, ok := task["desc"]; ok {
		t.Fatalf("empty desc not omitted: %v", task)
	}
}

func TestListTasksETag(t *testing.T) {
	h, _ := newTestHandler(t)
	addTask(t, h, map[string]any{"title": "a"})
	rec := call(t, h, "GET", "/api/tasks", nil)
	etag := rec.Header().Get("ETag")
	if rec.Code != http.StatusOK || !strings.HasPrefix(etag, `"`) || len(etag) != 66 {
		t.Fatalf("etag = %q status = %d", etag, rec.Code)
	}
	rec = call(t, h, "GET", "/api/tasks", nil, "If-None-Match", etag)
	if rec.Code != http.StatusNotModified || rec.Body.Len() != 0 || rec.Header().Get("ETag") != etag {
		t.Fatalf("304 expected, got %d %q", rec.Code, rec.Body.String())
	}
	rec = call(t, h, "GET", "/api/tasks", nil, "If-None-Match", `"stale", W/`+etag)
	if rec.Code != http.StatusNotModified {
		t.Fatalf("weak list match: %d", rec.Code)
	}
	addTask(t, h, map[string]any{"title": "b"})
	rec = call(t, h, "GET", "/api/tasks", nil, "If-None-Match", etag)
	if rec.Code != http.StatusOK || rec.Header().Get("ETag") == etag {
		t.Fatalf("changed board: %d %q", rec.Code, rec.Header().Get("ETag"))
	}
}

func TestAddTask(t *testing.T) {
	h, _ := newTestHandler(t)
	rec := call(t, h, "POST", "/api/tasks", map[string]any{
		"title": "full", "desc": "d", "status": "doing", "blocked": true, "prio": 1, "project": "work",
		"due": "2030-01-02", "effort": "M", "tags": []string{"x"}, "emoji": "🐛",
		"checks": []map[string]any{{"text": "one"}, {"text": "two", "done": true}},
	})
	task := wantStatus(t, rec, http.StatusCreated)
	if raw := rec.Body.String(); !strings.Contains(raw, `"checks":[{"text":"one"},{"text":"two","done":true}]`) {
		t.Fatalf("checks in %s", raw)
	}
	if task["blocked"] != true || task["prio"] != 1.0 || task["due"] != "2030-01-02" || task["effort"] != "M" || task["emoji"] != "🐛" || task["desc"] != "d" {
		t.Fatalf("task = %v", task)
	}
	tags, _ := json.Marshal(task["tags"])
	if string(tags) != `["x","project::work"]` {
		t.Fatalf("tags = %s", tags)
	}
	defaults := addTask(t, h, map[string]any{"title": "bare"})
	if defaults["status"] != "todo" || defaults["prio"] != 3.0 {
		t.Fatalf("defaults = %v", defaults)
	}
	explicit := addTask(t, h, map[string]any{"title": "elsewhere", "tags": []string{"project::side"}})
	if explicit["project"] != "side" {
		t.Fatalf("explicit project = %v", explicit)
	}
}

func TestAddTaskErrors(t *testing.T) {
	h, _ := newTestHandler(t)
	wantError(t, call(t, h, "POST", "/api/tasks", "{bad"), http.StatusBadRequest, "malformed JSON")
	wantError(t, call(t, h, "POST", "/api/tasks", ""), http.StatusBadRequest, "JSON object")
	wantError(t, call(t, h, "POST", "/api/tasks", map[string]any{"title": "  "}), http.StatusBadRequest, "title")
	wantError(t, call(t, h, "POST", "/api/tasks", map[string]any{"title": "x", "prio": 9}), http.StatusBadRequest, "invalid prio")
	wantError(t, call(t, h, "POST", "/api/tasks", map[string]any{"title": "x", "status": "nope"}), http.StatusBadRequest, "invalid status")
	wantError(t, call(t, h, "POST", "/api/tasks", map[string]any{"title": "x", "project": "work", "due": "tomorrow"}), http.StatusBadRequest, "invalid due date")
	wantError(t, call(t, h, "POST", "/api/tasks", map[string]any{"title": "x", "project": "bad name"}), http.StatusBadRequest, "whitespace")
	big := `{"title":"x","desc":"` + strings.Repeat("a", maxBodyBytes) + `"}`
	wantError(t, call(t, h, "POST", "/api/tasks", big), http.StatusRequestEntityTooLarge, "1 MiB")
	wantError(t, call(t, h, "POST", "/api/tasks", map[string]any{"title": "x"}), http.StatusBadRequest, "no project given")
	wantError(t, call(t, h, "POST", "/api/tasks", map[string]any{"title": "x", "tags": []string{"a"}}), http.StatusBadRequest, "no project given")
}

func TestGetTask(t *testing.T) {
	h, _ := newTestHandler(t)
	a := addTask(t, h, map[string]any{"title": "a"})
	b := addTask(t, h, map[string]any{"title": "b"})
	wantStatus(t, call(t, h, "POST", "/api/tasks/1/comments", map[string]any{"body": "hi"}), http.StatusCreated)
	wantStatus(t, call(t, h, "POST", "/api/links", map[string]any{"blocker": "1", "blocked": "2"}), http.StatusCreated)

	// The prefix keeps the first dash so an all-digit hex run can never be
	// read as a sequence number.
	for _, ref := range []string{"1", "#1", a["id"].(string), a["id"].(string)[:9]} {
		body := wantStatus(t, call(t, h, "GET", "/api/tasks/"+ref, nil), http.StatusOK)
		task := body["task"].(map[string]any)
		if task["id"] != a["id"] {
			t.Fatalf("ref %q resolved to %v", ref, task)
		}
		comments := body["comments"].([]any)
		if len(comments) != 1 || comments[0].(map[string]any)["body"] != "hi" {
			t.Fatalf("comments = %v", comments)
		}
		links := body["links"].(map[string]any)
		if blocks := links["blocks"].([]any); len(blocks) != 1 || blocks[0].(map[string]any)["id"] != b["id"] {
			t.Fatalf("blocks = %v", links)
		}
		if blockedBy := links["blockedBy"].([]any); len(blockedBy) != 0 {
			t.Fatalf("blockedBy = %v", links)
		}
	}
	wantError(t, call(t, h, "GET", "/api/tasks/99", nil), http.StatusNotFound, `no task matches id "99"`)
	wantError(t, call(t, h, "GET", "/api/tasks/"+ambiguousPrefix(t, h), nil), http.StatusBadRequest, "ambiguous")
}

// ambiguousPrefix adds tasks until two share a first hex letter and returns
// it. A leading digit would read as a sequence number, so only a-f count;
// six letters mean the seventh letter-led id must collide.
func ambiguousPrefix(t *testing.T, h http.Handler) string {
	t.Helper()
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		task := addTask(t, h, map[string]any{"title": "filler"})
		first := task["id"].(string)[:1]
		if first < "a" {
			continue
		}
		if seen[first] {
			return first
		}
		seen[first] = true
	}
	t.Fatal("no shared prefix after 200 tasks")
	return ""
}

func TestPatchTask(t *testing.T) {
	h, _ := newTestHandler(t)
	addTask(t, h, map[string]any{"title": "a", "tags": []string{"x"}})
	addTask(t, h, map[string]any{"title": "b"})

	task := wantStatus(t, call(t, h, "PATCH", "/api/tasks/1", map[string]any{
		"title": "renamed", "desc": "d", "blocked": true, "prio": 2, "due": "2030-05-05",
		"effort": "L", "emoji": "🔥", "checks": []map[string]any{{"text": "c", "done": true}},
	}), http.StatusOK)
	if task["title"] != "renamed" || task["prio"] != 2.0 || task["effort"] != "L" || task["blocked"] != true {
		t.Fatalf("patched = %v", task)
	}
	if tags, _ := json.Marshal(task["tags"]); string(tags) != `["x","project::work"]` {
		t.Fatalf("tags after field patch = %s", tags)
	}
	// Replacing tags keeps the project; naming a project moves the task.
	task = wantStatus(t, call(t, h, "PATCH", "/api/tasks/1", map[string]any{"tags": []string{"y"}}), http.StatusOK)
	if tags, _ := json.Marshal(task["tags"]); string(tags) != `["y","project::work"]` {
		t.Fatalf("tags after replace = %s", tags)
	}
	task = wantStatus(t, call(t, h, "PATCH", "/api/tasks/1", map[string]any{"project": "side"}), http.StatusOK)
	if task["project"] != "side" {
		t.Fatalf("project move = %v", task)
	}
	// Clearing fields with empty strings.
	task = wantStatus(t, call(t, h, "PATCH", "/api/tasks/1", map[string]any{"desc": "", "due": "", "effort": "", "emoji": "", "blocked": false}), http.StatusOK)
	for _, key := range []string{"desc", "due", "effort", "emoji", "blocked"} {
		if _, ok := task[key]; ok {
			t.Fatalf("%s not cleared: %v", key, task)
		}
	}
	// Status plus index moves within the destination column.
	task = wantStatus(t, call(t, h, "PATCH", "/api/tasks/2", map[string]any{"status": "doing", "index": 0}), http.StatusOK)
	if task["status"] != "doing" || task["position"] != 0.0 {
		t.Fatalf("moved = %v", task)
	}
	// Reorder without a status change.
	task = wantStatus(t, call(t, h, "PATCH", "/api/tasks/2", map[string]any{"index": 0}), http.StatusOK)
	if task["status"] != "doing" {
		t.Fatalf("reordered = %v", task)
	}
}

func TestPatchTaskErrors(t *testing.T) {
	h, _ := newTestHandler(t)
	addTask(t, h, map[string]any{"title": "a", "checks": []map[string]any{{"text": "open"}}})
	wantError(t, call(t, h, "PATCH", "/api/tasks/1", "{"), http.StatusBadRequest, "malformed JSON")
	wantError(t, call(t, h, "PATCH", "/api/tasks/1", map[string]any{"prio": 0}), http.StatusBadRequest, "invalid prio")
	wantError(t, call(t, h, "PATCH", "/api/tasks/1", map[string]any{"status": "later"}), http.StatusBadRequest, "invalid status")
	wantError(t, call(t, h, "PATCH", "/api/tasks/1", map[string]any{"due": "never"}), http.StatusBadRequest, "invalid due date")
	wantError(t, call(t, h, "PATCH", "/api/tasks/1", map[string]any{"index": -1}), http.StatusBadRequest, "invalid index")
	wantError(t, call(t, h, "PATCH", "/api/tasks/9", map[string]any{"title": "x"}), http.StatusNotFound, "no task matches")
	wantError(t, call(t, h, "PATCH", "/api/tasks/9", map[string]any{"project": "p"}), http.StatusNotFound, "no task matches")
	wantError(t, call(t, h, "PATCH", "/api/tasks/1", map[string]any{"project": "bad name"}), http.StatusBadRequest, "whitespace")
	wantError(t, call(t, h, "PATCH", "/api/tasks/1", map[string]any{"project": "p", "tags": []string{"project::q"}}), http.StatusBadRequest, "contradicts")

	body := wantError(t, call(t, h, "PATCH", "/api/tasks/1", map[string]any{"status": "done"}), http.StatusConflict, "checklist items are still open")
	if body["completionBlocked"] != true {
		t.Fatalf("409 body = %v", body)
	}
	if task := wantStatus(t, call(t, h, "GET", "/api/tasks/1", nil), http.StatusOK)["task"].(map[string]any); task["status"] != "todo" {
		t.Fatalf("refused move persisted: %v", task)
	}
	// Closing the last item in the same call clears the guard.
	task := wantStatus(t, call(t, h, "PATCH", "/api/tasks/1", map[string]any{"status": "done", "checks": []map[string]any{{"text": "open", "done": true}}}), http.StatusOK)
	if task["status"] != "done" {
		t.Fatalf("finish = %v", task)
	}
	// A blocked flag also refuses unless forced.
	addTask(t, h, map[string]any{"title": "b", "blocked": true})
	wantError(t, call(t, h, "PATCH", "/api/tasks/2", map[string]any{"status": "done"}), http.StatusConflict, "flagged blocked")
	task = wantStatus(t, call(t, h, "PATCH", "/api/tasks/2", map[string]any{"status": "done", "force": true}), http.StatusOK)
	if task["status"] != "done" {
		t.Fatalf("forced = %v", task)
	}
}

func TestMoveTask(t *testing.T) {
	h, _ := newTestHandler(t)
	addTask(t, h, map[string]any{"title": "a"})
	addTask(t, h, map[string]any{"title": "b", "status": "doing"})
	addTask(t, h, map[string]any{"title": "c", "checks": []map[string]any{{"text": "open"}}})

	task := wantStatus(t, call(t, h, "POST", "/api/tasks/1/move", map[string]any{"status": "doing", "index": 0}), http.StatusOK)
	if task["status"] != "doing" || task["position"] != 0.0 {
		t.Fatalf("moved = %v", task)
	}
	task = wantStatus(t, call(t, h, "POST", "/api/tasks/2/move", map[string]any{"status": "done"}), http.StatusOK)
	if task["status"] != "done" {
		t.Fatalf("done = %v", task)
	}
	body := wantError(t, call(t, h, "POST", "/api/tasks/3/move", map[string]any{"status": "done"}), http.StatusConflict, "still open")
	if body["completionBlocked"] != true {
		t.Fatalf("409 body = %v", body)
	}
	wantStatus(t, call(t, h, "POST", "/api/tasks/3/move", map[string]any{"status": "done", "force": true}), http.StatusOK)

	// An open blocker is the store's own gate; it reads the same on the wire.
	addTask(t, h, map[string]any{"title": "d"})
	addTask(t, h, map[string]any{"title": "e"})
	wantStatus(t, call(t, h, "POST", "/api/links", map[string]any{"blocker": "4", "blocked": "5"}), http.StatusCreated)
	wantError(t, call(t, h, "POST", "/api/tasks/5/move", map[string]any{"status": "done"}), http.StatusConflict, "still blocks")

	wantError(t, call(t, h, "POST", "/api/tasks/1/move", map[string]any{"status": "sideways"}), http.StatusBadRequest, "invalid status")
	wantError(t, call(t, h, "POST", "/api/tasks/1/move", ""), http.StatusBadRequest, "JSON object")
	wantError(t, call(t, h, "POST", "/api/tasks/1/move", "nope"), http.StatusBadRequest, "malformed")
	wantError(t, call(t, h, "POST", "/api/tasks/42/move", map[string]any{"status": "doing"}), http.StatusNotFound, "no task matches")
}

func TestCancelRestoreDelete(t *testing.T) {
	h, _ := newTestHandler(t)
	addTask(t, h, map[string]any{"title": "a"})
	addTask(t, h, map[string]any{"title": "b"})

	wantError(t, call(t, h, "DELETE", "/api/tasks/1", nil), http.StatusConflict, "only cancelled")
	task := wantStatus(t, call(t, h, "POST", "/api/tasks/1/cancel", nil), http.StatusOK)
	if task["status"] != "cancelled" {
		t.Fatalf("cancel = %v", task)
	}
	task = wantStatus(t, call(t, h, "POST", "/api/tasks/2/cancel", map[string]any{"reason": "dupe"}), http.StatusOK)
	if task["status"] != "cancelled" {
		t.Fatalf("cancel with reason = %v", task)
	}
	task = wantStatus(t, call(t, h, "POST", "/api/tasks/2/restore", nil), http.StatusOK)
	if task["status"] != "todo" {
		t.Fatalf("restore = %v", task)
	}
	wantStatus(t, call(t, h, "POST", "/api/tasks/2/cancel", map[string]any{"reason": "  "}), http.StatusOK)
	wantError(t, call(t, h, "POST", "/api/tasks/2/cancel", map[string]any{"reason": "a\nb"}), http.StatusBadRequest, "line break")
	wantError(t, call(t, h, "POST", "/api/tasks/2/cancel", "{"), http.StatusBadRequest, "malformed")
	wantError(t, call(t, h, "POST", "/api/tasks/9/cancel", nil), http.StatusNotFound, "no task matches")
	wantError(t, call(t, h, "POST", "/api/tasks/9/restore", nil), http.StatusNotFound, "no task matches")

	task = wantStatus(t, call(t, h, "DELETE", "/api/tasks/1", nil), http.StatusOK)
	if task["title"] != "a" {
		t.Fatalf("delete = %v", task)
	}
	body := wantError(t, call(t, h, "DELETE", "/api/tasks/1", nil), http.StatusNotFound, "no task matches")
	if _, ok := body["completionBlocked"]; ok {
		t.Fatalf("plain error carries completionBlocked: %v", body)
	}
}

func TestComments(t *testing.T) {
	h, _ := newTestHandler(t)
	addTask(t, h, map[string]any{"title": "a"})
	c := wantStatus(t, call(t, h, "POST", "/api/tasks/#1/comments", map[string]any{"body": "first"}), http.StatusCreated)
	if c["id"] != 1.0 || c["taskSeq"] != 1.0 || c["author"] != "default" || c["body"] != "first" || c["createdAt"] == "" {
		t.Fatalf("comment = %v", c)
	}
	wantError(t, call(t, h, "POST", "/api/tasks/1/comments", map[string]any{"body": " "}), http.StatusBadRequest, "must not be empty")
	wantError(t, call(t, h, "POST", "/api/tasks/1/comments", "{"), http.StatusBadRequest, "malformed")
	wantError(t, call(t, h, "POST", "/api/tasks/7/comments", map[string]any{"body": "x"}), http.StatusNotFound, "no task matches")

	body := wantStatus(t, call(t, h, "GET", "/api/tasks/1/comments", nil), http.StatusOK)
	if list := body["comments"].([]any); len(list) != 1 {
		t.Fatalf("comments = %v", body)
	}
	wantError(t, call(t, h, "GET", "/api/tasks/7/comments", nil), http.StatusNotFound, "no task matches")

	edited := wantStatus(t, call(t, h, "PUT", "/api/comments/c1", map[string]any{"body": " first, revised "}), http.StatusOK)
	if edited["id"] != 1.0 || edited["body"] != "first, revised" || edited["createdAt"] != c["createdAt"] {
		t.Fatalf("edited = %v", edited)
	}
	wantError(t, call(t, h, "PUT", "/api/comments/1", map[string]any{"body": " "}), http.StatusBadRequest, "must not be empty")
	wantError(t, call(t, h, "PUT", "/api/comments/1", "{"), http.StatusBadRequest, "malformed")
	wantError(t, call(t, h, "PUT", "/api/comments/abc", map[string]any{"body": "x"}), http.StatusBadRequest, "invalid comment id")
	wantError(t, call(t, h, "PUT", "/api/comments/9", map[string]any{"body": "x"}), http.StatusNotFound, "no comment matches")

	wantError(t, call(t, h, "DELETE", "/api/comments/abc", nil), http.StatusBadRequest, "invalid comment id")
	wantError(t, call(t, h, "DELETE", "/api/comments/0", nil), http.StatusBadRequest, "invalid comment id")
	deleted := wantStatus(t, call(t, h, "DELETE", "/api/comments/c1", nil), http.StatusOK)
	if deleted["body"] != "first, revised" {
		t.Fatalf("deleted = %v", deleted)
	}
	wantError(t, call(t, h, "DELETE", "/api/comments/1", nil), http.StatusNotFound, "no comment matches")
}

func TestLinks(t *testing.T) {
	h, _ := newTestHandler(t)
	addTask(t, h, map[string]any{"title": "a"})
	addTask(t, h, map[string]any{"title": "b"})

	body := wantStatus(t, call(t, h, "POST", "/api/links", map[string]any{"blocker": "1", "blocked": "2"}), http.StatusCreated)
	if body["blocker"].(map[string]any)["seq"] != 1.0 || body["blocked"].(map[string]any)["seq"] != 2.0 {
		t.Fatalf("link = %v", body)
	}
	wantError(t, call(t, h, "POST", "/api/links", map[string]any{"blocker": "1", "blocked": "2"}), http.StatusBadRequest, "already blocks")
	wantError(t, call(t, h, "POST", "/api/links", map[string]any{"blocker": "1", "blocked": "1"}), http.StatusBadRequest, "cannot block itself")
	wantError(t, call(t, h, "POST", "/api/links", map[string]any{"blocker": "2", "blocked": "1"}), http.StatusBadRequest, "cycle")
	wantError(t, call(t, h, "POST", "/api/links", map[string]any{"blocker": "1", "blocked": "9"}), http.StatusNotFound, "no task matches")
	wantError(t, call(t, h, "POST", "/api/links", "{"), http.StatusBadRequest, "malformed")

	detail := wantStatus(t, call(t, h, "GET", "/api/tasks/2", nil), http.StatusOK)
	if blockedBy := detail["links"].(map[string]any)["blockedBy"].([]any); len(blockedBy) != 1 {
		t.Fatalf("blockedBy = %v", detail)
	}
	rec := call(t, h, "DELETE", "/api/links", map[string]any{"a": "2", "b": "1"})
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("unlink = %d %q", rec.Code, rec.Body.String())
	}
	wantError(t, call(t, h, "DELETE", "/api/links", map[string]any{"a": "1", "b": "2"}), http.StatusNotFound, "no task matches")
	wantError(t, call(t, h, "DELETE", "/api/links", "{"), http.StatusBadRequest, "malformed")
}

func TestSimilar(t *testing.T) {
	h, st := newTestHandler(t)
	addTask(t, h, map[string]any{"title": "fix login bug"})
	body := wantStatus(t, call(t, h, "GET", "/api/similar?q=fix+login+bug", nil), http.StatusOK)
	items := body["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["title"] != "fix login bug" || items[0].(map[string]any)["status"] != "todo" {
		t.Fatalf("items = %v", items)
	}
	body = wantStatus(t, call(t, h, "GET", "/api/similar?q=fix+login+bug&limit=50", nil), http.StatusOK)
	if len(body["items"].([]any)) != 1 {
		t.Fatalf("capped limit items = %v", body)
	}
	body = wantStatus(t, call(t, h, "GET", "/api/similar", nil), http.StatusOK)
	if got, _ := json.Marshal(body); string(got) != `{"items":[]}` {
		t.Fatalf("empty query = %s", got)
	}
	wantError(t, call(t, h, "GET", "/api/similar?q=x&limit=abc", nil), http.StatusBadRequest, "invalid limit")
	wantError(t, call(t, h, "GET", "/api/similar?q=x&limit=0", nil), http.StatusBadRequest, "invalid limit")
	st.Close()
	wantStatus(t, call(t, h, "GET", "/api/similar?q=x", nil), http.StatusBadRequest)
}

func TestLabels(t *testing.T) {
	h, _ := newTestHandler(t)
	body := wantStatus(t, call(t, h, "GET", "/api/labels", nil), http.StatusOK)
	if got, _ := json.Marshal(body); string(got) != `{"labels":[]}` {
		t.Fatalf("empty labels = %s", got)
	}
	addTask(t, h, map[string]any{"title": "a", "tags": []string{"type::bug"}})
	body = wantStatus(t, call(t, h, "GET", "/api/labels", nil), http.StatusOK)
	labels, _ := json.Marshal(body["labels"])
	if !strings.Contains(string(labels), `"type::bug"`) || !strings.Contains(string(labels), `"project::work"`) {
		t.Fatalf("labels = %s", labels)
	}
}

func TestRequestSafety(t *testing.T) {
	h, _ := newTestHandler(t)
	addTask(t, h, map[string]any{"title": "a"})
	in := map[string]any{"title": "b", "project": "work"}

	wantStatus(t, call(t, h, "POST", "/api/tasks", in, "Origin", "http://localhost"), http.StatusCreated)
	wantStatus(t, call(t, h, "POST", "/api/tasks", in, "Origin", "http://LOCALHOST"), http.StatusCreated)
	wantError(t, call(t, h, "POST", "/api/tasks", in, "Origin", "http://evil.example"), http.StatusForbidden, "cross-origin")
	wantError(t, call(t, h, "POST", "/api/tasks", in, "Origin", "null"), http.StatusForbidden, "cross-origin")
	wantError(t, call(t, h, "POST", "/api/tasks", in, "Origin", "http://[::1"), http.StatusForbidden, "cross-origin")
	wantStatus(t, call(t, h, "GET", "/api/tasks", nil, "Origin", "http://evil.example"), http.StatusOK)

	wantError(t, call(t, h, "POST", "/api/tasks", in, "Content-Type", "text/plain"), http.StatusUnsupportedMediaType, "application/json")
	wantError(t, call(t, h, "POST", "/api/tasks", in, "Content-Type", ""), http.StatusUnsupportedMediaType, "application/json")
	wantStatus(t, call(t, h, "POST", "/api/tasks", in, "Content-Type", "application/json; charset=utf-8"), http.StatusCreated)
	// No body means no Content-Type requirement.
	wantStatus(t, call(t, h, "POST", "/api/tasks/1/cancel", nil), http.StatusOK)
	wantStatus(t, call(t, h, "POST", "/api/tasks/1/restore", nil), http.StatusOK)

	rec := call(t, h, "PUT", "/api/tasks", nil)
	wantError(t, rec, http.StatusMethodNotAllowed, "PUT")
	if !strings.Contains(rec.Header().Get("Allow"), "GET") || !strings.Contains(rec.Header().Get("Allow"), "POST") {
		t.Fatalf("Allow = %q", rec.Header().Get("Allow"))
	}
	wantError(t, call(t, h, "PATCH", "/api/meta", nil), http.StatusMethodNotAllowed, "PATCH")
	wantError(t, call(t, h, "GET", "/api/nothing", nil), http.StatusNotFound, "no such endpoint")
	rec = call(t, h, "GET", "/api/nothing", nil)
	if rec.Header().Get("Content-Type") != "application/json" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("404 headers = %v", rec.Header())
	}
}

func TestRequestSafetyRejectsRebindingHost(t *testing.T) {
	h, _ := newTestHandler(t)
	for _, route := range []struct{ method, path string }{
		{"GET", "/"},
		{"GET", "/api/tasks"},
		{"GET", "/api/events"},
		{"PUT", "/api/settings/ai"},
	} {
		t.Run(route.method+route.path, func(t *testing.T) {
			req := httptest.NewRequest(route.method, "http://attacker.example:4321"+route.path, nil)
			req.Header.Set("Origin", "http://attacker.example:4321")
			ctx, cancel := context.WithCancel(req.Context())
			cancel() // An unguarded SSE route must not leave this regression test waiting.
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req.WithContext(ctx))
			wantError(t, rec, http.StatusForbidden, "Host")
		})
	}
}

func TestRequestSafetyPreventsFraming(t *testing.T) {
	h, _ := newTestHandler(t)
	for _, path := range []string{"/", "/app.js", "/api/meta", "/api/nothing"} {
		rec := call(t, h, "GET", path, nil)
		if got := rec.Header().Get("Content-Security-Policy"); got != "frame-ancestors 'none'" {
			t.Errorf("%s: Content-Security-Policy = %q", path, got)
		}
	}
}

func TestRequestSafetyAllowsLoopbackHosts(t *testing.T) {
	h, _ := newTestHandler(t)
	for _, host := range []string{"localhost", "LOCALHOST:4321", "127.0.0.1", "127.0.0.2:4321", "[::1]", "[::1]:4321"} {
		req := httptest.NewRequest("GET", "/api/meta", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("Host %q: status %d, body %s", host, rec.Code, rec.Body.String())
		}
	}
}

func TestWriteJSONEncodeFailure(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusOK, make(chan int))
	wantError(t, rec, http.StatusInternalServerError, "encode response")
}
