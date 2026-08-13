package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/board"
)

func TestCreateTaskAcceptsEveryField(t *testing.T) {
	h, _ := newTestServer(t, Config{})
	body := `{"title":"Full","desc":"body","emoji":"🚀","status":"doing","prio":2,"due":"2026-09-01","effort":"M","blocked":true,"tags":["a","b"],"checks":[{"text":"one","done":true}]}`
	w := doReq(t, h, "POST", "/api/tasks", body, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", w.Code, w.Body)
	}
	created := decodeAPITask(t, w.Body.String())
	if created.Desc != "body" || created.Emoji != "🚀" || created.Status != "doing" ||
		created.Prio != 2 || created.Due != "2026-09-01" || created.Effort != "M" ||
		!created.Blocked || len(created.Tags) != 2 || len(created.Checks) != 1 || !created.Checks[0].Done {
		t.Fatalf("created = %+v", created)
	}
}

func TestTasksAPIAdditionalErrorPaths(t *testing.T) {
	h, st := newTestServer(t, Config{})
	if w := doReq(t, h, "POST", "/api/tasks", `{"title":"Seed"}`, nil); w.Code != http.StatusCreated {
		t.Fatal("seed failed")
	}
	if w := doReq(t, h, "POST", "/api/tasks/1/comments", `{"body":"note"}`, nil); w.Code != http.StatusCreated {
		t.Fatal("seed comment failed")
	}

	// A clean task passes the done guard without force.
	if w := doReq(t, h, "PATCH", "/api/tasks/1", `{"status":"done"}`, nil); w.Code != http.StatusOK {
		t.Fatalf("clean done = %d %s", w.Code, w.Body)
	}

	// Detail from the blocker's side carries blocks and comments.
	if w := doReq(t, h, "POST", "/api/tasks", `{"title":"Blocked"}`, nil); w.Code != http.StatusCreated {
		t.Fatal("second seed failed")
	}
	if w := doReq(t, h, "POST", "/api/links", `{"blocker":"1","blocked":"2"}`, nil); w.Code != http.StatusCreated {
		t.Fatal("link failed")
	}
	w := doReq(t, h, "GET", "/api/tasks/1", "", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"blocks"`) || !strings.Contains(w.Body.String(), `"note"`) {
		t.Fatalf("blocker detail = %d %s", w.Code, w.Body)
	}

	cases := []struct {
		name, method, path, body string
		want                     int
	}{
		{"delete missing task", "DELETE", "/api/tasks/9", "", http.StatusNotFound},
		{"comments of missing task", "GET", "/api/tasks/9/comments", "", http.StatusNotFound},
		{"comment bad json", "POST", "/api/tasks/1/comments", `{`, http.StatusBadRequest},
		{"patch bad json", "PATCH", "/api/tasks/1", `{`, http.StatusBadRequest},
		{"link bad json", "POST", "/api/links", `{`, http.StatusBadRequest},
		{"unlink unknown refs", "DELETE", "/api/links?a=zz&b=yy", "", http.StatusNotFound},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if w := doReq(t, h, tt.method, tt.path, tt.body, nil); w.Code != tt.want {
				t.Errorf("%s %s = %d, want %d (%s)", tt.method, tt.path, w.Code, tt.want, w.Body)
			}
		})
	}

	// A closed store turns every remaining branch into a logged 500.
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	broken := []struct {
		name, method, path, body string
	}{
		{"list", "GET", "/api/tasks", ""},
		{"detail", "GET", "/api/tasks/1", ""},
		{"delete comment", "DELETE", "/api/comments/c1", ""},
		{"unlink", "DELETE", "/api/links?a=1&b=2", ""},
	}
	for _, tt := range broken {
		t.Run("broken store "+tt.name, func(t *testing.T) {
			w := doReq(t, h, tt.method, tt.path, tt.body, nil)
			if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), storageErrorMessage) {
				t.Errorf("%s %s = %d %s", tt.method, tt.path, w.Code, w.Body)
			}
		})
	}
}

func TestGetTaskRejectsAmbiguousPrefix(t *testing.T) {
	h, _ := newTestServer(t, Config{})
	// UUIDs are random, so create tasks until two share a letter first
	// character; a letter prefix cannot be mistaken for a sequence number.
	seen := map[byte]bool{}
	prefix := ""
	for i := 0; i < 100 && prefix == ""; i++ {
		w := doReq(t, h, "POST", "/api/tasks", `{"title":"Collide"}`, nil)
		if w.Code != http.StatusCreated {
			t.Fatalf("create = %d", w.Code)
		}
		c := decodeAPITask(t, w.Body.String()).ID[0]
		if c >= 'a' && c <= 'f' {
			if seen[c] {
				prefix = string(c)
			}
			seen[c] = true
		}
	}
	if prefix == "" {
		t.Fatal("no letter-prefix collision in 100 tasks")
	}
	w := doReq(t, h, "GET", "/api/tasks/"+prefix, "", nil)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "ambiguous") {
		t.Fatalf("ambiguous prefix = %d %s", w.Code, w.Body)
	}
}

func TestTaskRefLabelFallsBackToUUID(t *testing.T) {
	if got := taskRefLabel(board.Task{Seq: 4, ID: "abc"}); got != "#4" {
		t.Fatalf("with seq = %q", got)
	}
	if got := taskRefLabel(board.Task{ID: "abc"}); got != "abc" {
		t.Fatalf("without seq = %q", got)
	}
}
