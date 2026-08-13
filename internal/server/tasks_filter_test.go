package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestListTasksSearchAndTagFilters(t *testing.T) {
	h, _ := newTestServer(t, Config{})
	seeds := []string{
		`{"title":"Fix login timeout","desc":"auth token expires","tags":["bug","auth"]}`,
		`{"title":"Design landing page","tags":["ui"]}`,
		`{"title":"Rotate auth keys","tags":["auth","env::prod"],"status":"doing"}`,
	}
	for _, body := range seeds {
		if w := doReq(t, h, "POST", "/api/tasks", body, nil); w.Code != http.StatusCreated {
			t.Fatalf("seed = %d %s", w.Code, w.Body)
		}
	}

	titles := func(path string) []string {
		w := doReq(t, h, "GET", path, "", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s = %d %s", path, w.Code, w.Body)
		}
		var tasks []struct {
			Title string `json:"title"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &tasks); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		out := make([]string, 0, len(tasks))
		for _, task := range tasks {
			out = append(out, task.Title)
		}
		return out
	}

	if got := titles("/api/tasks?search=auth"); len(got) != 2 {
		t.Fatalf("search=auth = %v", got)
	}
	if got := titles("/api/tasks?search=token+expir"); len(got) != 1 || got[0] != "Fix login timeout" {
		t.Fatalf("prefix search = %v", got)
	}
	if got := titles("/api/tasks?tag=auth&tag=bug"); len(got) != 1 || got[0] != "Fix login timeout" {
		t.Fatalf("tag AND = %v", got)
	}
	if got := titles("/api/tasks?tag=env%3A%3Aprod"); len(got) != 1 || got[0] != "Rotate auth keys" {
		t.Fatalf("scoped tag = %v", got)
	}
	if got := titles("/api/tasks?search=auth&tag=auth&status=doing"); len(got) != 1 || got[0] != "Rotate auth keys" {
		t.Fatalf("combined = %v", got)
	}

	// Validation surfaces as 400s.
	if w := doReq(t, h, "GET", "/api/tasks?tag=%20", "", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("blank tag = %d", w.Code)
	}
	if w := doReq(t, h, "GET", "/api/tasks?search="+strings.Repeat("x", 501), "", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("oversized search = %d %s", w.Code, w.Body)
	}
}
