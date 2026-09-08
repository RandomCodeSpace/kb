package webui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/RandomCodeSpace/kb/internal/tui/action"
)

// ref is the task reference the API path wants, taken from a created task.
func ref(task map[string]any) string {
	return fmt.Sprint(task["id"])
}

// remarshal re-decodes one field of a decoded response into a typed value, so
// assertions compare fields rather than map key order.
func remarshal(t *testing.T, value any, out any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatal(err)
	}
}

func wantProjects(t *testing.T, body map[string]any, want ...projectJSON) {
	t.Helper()
	var got []projectJSON
	remarshal(t, body["projects"], &got)
	if !slices.Equal(got, want) {
		t.Fatalf("projects = %+v, want %+v", got, want)
	}
}

func TestActions(t *testing.T) {
	h, _ := newTestHandler(t)
	body := wantStatus(t, call(t, h, "GET", "/api/actions", nil), http.StatusOK)
	var got []actionJSON
	remarshal(t, body["actions"], &got)
	if len(got) != len(webActions) {
		t.Fatalf("actions = %d rows, want %d", len(got), len(webActions))
	}
	if got[0] != (actionJSON{"openCard", "navigate", "enter", "enter", "open card", true, "GET /api/tasks/{ref}"}) {
		t.Fatalf("first action = %+v", got[0])
	}
	seen := map[string]bool{}
	for _, a := range got {
		if a.ID == "" || a.Key == "" || a.Name == "" || seen[a.ID] {
			t.Fatalf("bad action row %+v", a)
		}
		seen[a.ID] = true
		if !a.Enabled {
			t.Fatalf("action %q is disabled", a.ID)
		}
	}
	wantError(t, call(t, h, "POST", "/api/actions", map[string]any{}), http.StatusMethodNotAllowed, "not allowed")
}

// The web palette is a copy of the TUI's registry, so it has to be compared
// against it: a rebind or a reword there must fail here rather than drift.
func TestActionsMirrorTUIRegistry(t *testing.T) {
	rows := action.All()
	if len(rows) != len(webActions) {
		t.Fatalf("registry has %d rows, web copy has %d", len(rows), len(webActions))
	}
	groups := map[action.Group]string{
		action.Navigate: "navigate",
		action.Act:      "act",
		action.Dismiss:  "dismiss",
	}
	for i, want := range rows {
		got := webActions[i]
		if got.Key != want.Key || got.Hint != want.Hint || got.Name != want.Name || got.Group != groups[want.Group] {
			t.Errorf("row %d = %+v, want key %q hint %q name %q group %q",
				i, got, want.Key, want.Hint, want.Name, groups[want.Group])
		}
	}
}

func TestListProjects(t *testing.T) {
	h, _ := newTestHandler(t)
	addTask(t, h, map[string]any{"title": "a", "project": "alpha"})
	boxed := addTask(t, h, map[string]any{"title": "b", "project": "inbox"})
	killed := addTask(t, h, map[string]any{"title": "c"})
	shipped := addTask(t, h, map[string]any{"title": "d", "project": "alpha"})
	wantStatus(t, call(t, h, "POST", "/api/tasks/"+ref(boxed)+"/move", map[string]any{"status": "doing"}), http.StatusOK)
	wantStatus(t, call(t, h, "POST", "/api/tasks/"+ref(shipped)+"/move", map[string]any{"status": "done"}), http.StatusOK)
	wantStatus(t, call(t, h, "POST", "/api/tasks/"+ref(killed)+"/cancel", nil), http.StatusOK)

	body := wantStatus(t, call(t, h, "GET", "/api/projects", nil), http.StatusOK)
	if _, reported := body["active"]; reported {
		t.Fatalf("no ambient project, yet active = %v", body["active"])
	}
	wantProjects(t, body,
		projectJSON{Name: "alpha", Counts: statusCountsJSON{Todo: 1, Done: 1}},
		projectJSON{Name: "work", Counts: statusCountsJSON{Cancelled: 1}},
		projectJSON{Name: "inbox", Counts: statusCountsJSON{Doing: 1}},
	)
	wantError(t, call(t, h, "DELETE", "/api/projects", nil), http.StatusMethodNotAllowed, "not allowed")
}

// A project exists only through the cards that carry its label: with no
// ambient project there is nothing to list on an empty board, and the web
// switcher is the only place a selection lives.
func TestListProjectsEmptyBoard(t *testing.T) {
	h, st := newTestHandler(t)
	body := wantStatus(t, call(t, h, "GET", "/api/projects", nil), http.StatusOK)
	wantProjects(t, body)
	wantError(t, call(t, h, "PUT", "/api/projects/active", map[string]any{"name": "web"}), http.StatusNotFound, "no such endpoint")
	st.Close()
	wantStatus(t, call(t, h, "GET", "/api/projects", nil), http.StatusInternalServerError)
}

func TestTombstone(t *testing.T) {
	h, _ := newTestHandler(t)
	killed := addTask(t, h, map[string]any{"title": "dead end"})
	wantStatus(t, call(t, h, "POST", "/api/tasks/"+ref(killed)+"/cancel",
		map[string]any{"reason": "duplicate of #1"}), http.StatusOK)

	body := wantStatus(t, call(t, h, "GET", "/api/tasks/"+ref(killed)+"/tombstone", nil), http.StatusOK)
	if body["taskId"] != killed["id"] || body["reason"] != "duplicate of #1" {
		t.Fatalf("tombstone = %v", body)
	}
	if killedAt, _ := body["killedAt"].(string); killedAt == "" {
		t.Fatalf("tombstone has no killedAt: %v", body)
	}

	quiet := addTask(t, h, map[string]any{"title": "no reason"})
	wantStatus(t, call(t, h, "POST", "/api/tasks/"+ref(quiet)+"/cancel", nil), http.StatusOK)
	wantError(t, call(t, h, "GET", "/api/tasks/"+ref(quiet)+"/tombstone", nil),
		http.StatusNotFound, "no cancellation reason")

	wantError(t, call(t, h, "GET", "/api/tasks/9999/tombstone", nil), http.StatusNotFound, "no task matches")
	wantError(t, call(t, h, "POST", "/api/tasks/"+ref(quiet)+"/tombstone", map[string]any{}),
		http.StatusMethodNotAllowed, "not allowed")
}

func TestTasksByLink(t *testing.T) {
	h, _ := newTestHandler(t)
	link := "import::github:owner/repo#12"
	linked := addTask(t, h, map[string]any{"title": "imported", "tags": []string{link}})
	addTask(t, h, map[string]any{"title": "unrelated"})

	body := wantStatus(t, call(t, h, "GET", "/api/by-link?link="+url.QueryEscape(link), nil), http.StatusOK)
	var got []similarJSON
	remarshal(t, body["items"], &got)
	want := []similarJSON{{ID: fmt.Sprint(linked["id"]), Title: "imported", Status: "todo", Via: "card", Link: link}}
	if !slices.Equal(got, want) {
		t.Fatalf("items = %+v, want %+v", got, want)
	}
	empty := wantStatus(t, call(t, h, "GET", "/api/by-link?link=import::nothing", nil), http.StatusOK)
	if items, _ := empty["items"].([]any); len(items) != 0 {
		t.Fatalf("items = %v", empty["items"])
	}
	wantError(t, call(t, h, "GET", "/api/by-link", nil), http.StatusBadRequest, "link must not be empty")
	wantError(t, call(t, h, "GET", "/api/by-link?link=+", nil), http.StatusBadRequest, "link must not be empty")
	wantError(t, call(t, h, "POST", "/api/by-link", map[string]any{}),
		http.StatusMethodNotAllowed, "not allowed")
}

func TestShipped(t *testing.T) {
	h, _ := newTestHandler(t)
	shipped := addTask(t, h, map[string]any{"title": "done deal"})
	addTask(t, h, map[string]any{"title": "still going"})
	moved := wantStatus(t, call(t, h, "POST", "/api/tasks/"+ref(shipped)+"/move",
		map[string]any{"status": "done"}), http.StatusOK)

	body := wantStatus(t, call(t, h, "GET", "/api/shipped", nil), http.StatusOK)
	if body["date"] != time.Now().Format(shippedDateLayout) || body["count"] != float64(1) {
		t.Fatalf("shipped = %v", body)
	}
	var seqs []int
	remarshal(t, body["seqs"], &seqs)
	if want := int(moved["seq"].(float64)); len(seqs) != 1 || seqs[0] != want {
		t.Fatalf("seqs = %v, want [%d]", seqs, want)
	}

	old := wantStatus(t, call(t, h, "GET", "/api/shipped?date=2001-02-03", nil), http.StatusOK)
	if old["date"] != "2001-02-03" || old["count"] != float64(0) {
		t.Fatalf("shipped on an empty day = %v", old)
	}
	var empty []int
	remarshal(t, old["seqs"], &empty)
	if empty == nil || len(empty) != 0 {
		t.Fatalf("seqs = %v, want an empty list", empty)
	}
	wantError(t, call(t, h, "GET", "/api/shipped?date=yesterday", nil), http.StatusBadRequest, "YYYY-MM-DD")
	wantError(t, call(t, h, "POST", "/api/shipped", map[string]any{}),
		http.StatusMethodNotAllowed, "not allowed")
}

// Every board endpoint that reads the store reports a dead store as a 500,
// except the tombstone lookup, whose task resolution fails first and answers
// in the shared task-error shape.
func TestBoardEndpointsReportStoreFailure(t *testing.T) {
	h, st := newTestHandler(t)
	killed := addTask(t, h, map[string]any{"title": "gone"})
	st.Close()
	wantStatus(t, call(t, h, "GET", "/api/projects", nil), http.StatusInternalServerError)
	wantStatus(t, call(t, h, "GET", "/api/by-link?link=x", nil), http.StatusInternalServerError)
	wantStatus(t, call(t, h, "GET", "/api/shipped", nil), http.StatusInternalServerError)
	wantStatus(t, call(t, h, "GET", "/api/tasks/"+ref(killed)+"/tombstone", nil), http.StatusBadRequest)
}
