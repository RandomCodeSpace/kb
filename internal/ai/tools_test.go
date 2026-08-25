package ai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/rig"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
)

func newToolServer(t *testing.T) (*Runner, *store.Store) {
	t.Helper()
	st := newTestStore(t)
	return NewRunner(st, "", nil, nil), st
}

func runTool(t *testing.T, tool rig.Tool, input string) (string, error) {
	t.Helper()
	return tool.Run(context.Background(), json.RawMessage(input))
}

func mustRunTool(t *testing.T, tool rig.Tool, input string) string {
	t.Helper()
	out, err := runTool(t, tool, input)
	if err != nil {
		t.Fatalf("%s(%s): unexpected error: %v", tool.Name, input, err)
	}
	return out
}

func addToolTask(t *testing.T, st *store.Store, task board.Task) board.Task {
	t.Helper()
	created, err := st.AddTask("default", task)
	if err != nil {
		t.Fatalf("AddTask(%q): %v", task.Title, err)
	}
	return created
}

// TestToolDefinitions keeps every constructor's wire metadata inside what the
// run loop accepts: a missing description or Run makes the whole tool set
// invalid, which fails the run rather than the tool.
func TestToolDefinitions(t *testing.T) {
	s, _ := newToolServer(t)
	nameRe := regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)
	tools := []rig.Tool{
		proposeCardTool(&cardCollector{max: 1}),
		s.findSimilarTool("default"),
		s.fetchLinkTool(),
		s.listTasksTool("default"),
		s.getTaskTool("default"),
		s.updateTaskTool("default"),
	}
	want := []string{"propose_card", "find_similar", "fetch_link", "list_tasks", "get_task", "update_task"}
	if len(tools) != len(want) {
		t.Fatalf("built %d tools, want %d", len(tools), len(want))
	}
	seen := make(map[string]bool, len(tools))
	for i, tool := range tools {
		if tool.Name != want[i] {
			t.Errorf("tool %d name = %q, want %q", i, tool.Name, want[i])
		}
		if !nameRe.MatchString(tool.Name) {
			t.Errorf("tool %q name is not wire-legal", tool.Name)
		}
		if seen[tool.Name] {
			t.Errorf("duplicate tool name %q", tool.Name)
		}
		seen[tool.Name] = true
		if strings.TrimSpace(tool.Description) == "" {
			t.Errorf("tool %q has no description", tool.Name)
		}
		if tool.Run == nil {
			t.Errorf("tool %q has no Run", tool.Name)
		}
		if tool.InputSchema["type"] != "object" {
			t.Errorf("tool %q schema type = %v, want object", tool.Name, tool.InputSchema["type"])
		}
	}
}

func TestProposeCardTool(t *testing.T) {
	t.Run("coerces and collects a card", func(t *testing.T) {
		collector := &cardCollector{max: 2}
		tool := proposeCardTool(collector)

		out := mustRunTool(t, tool, `{"title":"  Ship the thing  ","emoji":"🚀","desc":"why\u0007 it matters",
			"prio":9,"due":"not-a-date","effort":"m","tags":["backend","bad tag",42],
			"checks":[{"text":"step one","done":true},{"text":"  "}]}`)
		if out != `{"accepted":true,"count":1}` {
			t.Fatalf("propose_card result = %s", out)
		}
		if len(collector.cards) != 1 {
			t.Fatalf("collected %d cards, want 1", len(collector.cards))
		}
		card := collector.cards[0]
		if card.Title != "Ship the thing" {
			t.Errorf("title = %q, want the trimmed title", card.Title)
		}
		if card.Desc != "why it matters" {
			t.Errorf("desc = %q, want the control character stripped", card.Desc)
		}
		if card.Prio != board.PrioLow {
			t.Errorf("prio = %d, want %d (clamped onto the three-value scale)", card.Prio, board.PrioLow)
		}
		if card.Due != "" {
			t.Errorf("due = %q, want the unparseable date dropped", card.Due)
		}
		if card.Effort != "M" {
			t.Errorf("effort = %q, want M", card.Effort)
		}
		if len(card.Tags) != 1 || card.Tags[0] != "backend" {
			t.Errorf("tags = %v, want only backend", card.Tags)
		}
		if len(card.Checks) != 1 || card.Checks[0].Text != "step one" || !card.Checks[0].Done {
			t.Errorf("checks = %+v, want the one usable item", card.Checks)
		}
		if err := validateDraft(card); err != nil {
			t.Errorf("collected card is not wire-safe: %v", err)
		}
	})

	// A field the model may not name is a field it never fills: source has to
	// be declared, and the schema has to stay closed so nothing else is.
	t.Run("declares source without opening the schema", func(t *testing.T) {
		schema := proposeCardTool(&cardCollector{max: 1}).InputSchema
		props, ok := schema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("schema properties = %T, want a map", schema["properties"])
		}
		source, ok := props["source"].(map[string]any)
		if !ok {
			t.Fatalf("schema has no source property: %v", props)
		}
		if source["type"] != "integer" {
			t.Errorf("source type = %v, want integer", source["type"])
		}
		if schema["additionalProperties"] != false {
			t.Errorf("additionalProperties = %v, want false", schema["additionalProperties"])
		}
	})

	// Source is provenance the server resolves against the pack it built, so a
	// number that is not a positive whole one must leave the card unsourced
	// rather than pointing at an issue nobody packed.
	t.Run("lifts only a usable source number", func(t *testing.T) {
		tests := []struct {
			name  string
			input string
			want  int
		}{
			{name: "lifted", input: `{"title":"From an issue","source":3}`, want: 3},
			{name: "non-integral", input: `{"title":"From an issue","source":2.5}`, want: 0},
			{name: "zero", input: `{"title":"From an issue","source":0}`, want: 0},
			{name: "negative", input: `{"title":"From an issue","source":-1}`, want: 0},
			{name: "string", input: `{"title":"From an issue","source":"2"}`, want: 0},
			{name: "absent", input: `{"title":"From an issue"}`, want: 0},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				collector := &cardCollector{max: 1}
				mustRunTool(t, proposeCardTool(collector), tt.input)
				if len(collector.cards) != 1 {
					t.Fatalf("collected %d cards, want 1", len(collector.cards))
				}
				if got := collector.cards[0].Source; got != tt.want {
					t.Errorf("source = %d, want %d", got, tt.want)
				}
			})
		}
	})

	t.Run("rejects an unusable card and keeps the count", func(t *testing.T) {
		tests := []struct {
			name  string
			input string
			want  string
		}{
			{name: "no title", input: `{"desc":"body only"}`, want: "title"},
			{name: "blank title", input: `{"title":"   "}`, want: "title"},
			{name: "not an object", input: `["title"]`, want: "invalid input"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				collector := &cardCollector{max: 2}
				_, err := runTool(t, proposeCardTool(collector), tt.input)
				if err == nil {
					t.Fatalf("propose_card(%s) = nil error, want a rejection", tt.input)
				}
				if !strings.Contains(err.Error(), tt.want) {
					t.Errorf("error = %q, want it to name %q", err, tt.want)
				}
				if len(collector.cards) != 0 {
					t.Errorf("collected %d cards, want none", len(collector.cards))
				}
			})
		}
	})

	// The cap is visible to the model: a silently dropped proposal would leave
	// it believing it delivered work the caller never received.
	t.Run("errors at the cap instead of dropping proposals", func(t *testing.T) {
		collector := &cardCollector{max: 2}
		tool := proposeCardTool(collector)
		mustRunTool(t, tool, `{"title":"First"}`)
		mustRunTool(t, tool, `{"title":"Second"}`)
		_, err := runTool(t, tool, `{"title":"Third"}`)
		if err == nil || err.Error() != cardLimitReachedMessage {
			t.Fatalf("third propose_card error = %v, want %q", err, cardLimitReachedMessage)
		}
		if len(collector.cards) != 2 {
			t.Fatalf("collected %d cards, want the cap of 2", len(collector.cards))
		}
	})

	t.Run("accepts nothing without a budget", func(t *testing.T) {
		collector := &cardCollector{}
		if _, err := runTool(t, proposeCardTool(collector), `{"title":"Unbudgeted"}`); err == nil {
			t.Fatal("propose_card with no cap = nil error, want a refusal")
		}
	})
}

func TestFindSimilarTool(t *testing.T) {
	t.Run("returns board stubs", func(t *testing.T) {
		s, st := newToolServer(t)
		for _, title := range []string{"needle alpha", "needle beta", "needle gamma", "needle delta"} {
			addToolTask(t, st, board.Task{Title: title})
		}
		out := mustRunTool(t, s.findSimilarTool("default"), `{"query":"needle"}`)

		var got struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("find_similar result %s: %v", out, err)
		}
		if len(got.Items) != findSimilarLimit {
			t.Fatalf("items = %d, want the fixed limit %d", len(got.Items), findSimilarLimit)
		}
		for _, item := range got.Items {
			for _, key := range []string{"id", "title", "status", "via"} {
				if _, ok := item[key]; !ok {
					t.Errorf("stub %v has no %q", item, key)
				}
			}
			for _, key := range []string{"reason", "killed_at", "desc"} {
				if _, ok := item[key]; ok {
					t.Errorf("stub %v carries %q, want the cheap shape only", item, key)
				}
			}
		}
	})

	t.Run("stays inside the caller's board", func(t *testing.T) {
		s, st := newToolServer(t)
		if _, err := st.AddTask("alice", board.Task{Title: "private needle"}); err != nil {
			t.Fatalf("AddTask alice: %v", err)
		}
		out := mustRunTool(t, s.findSimilarTool("default"), `{"query":"needle"}`)
		if out != `{"items":[]}` {
			t.Fatalf("cross-board find_similar = %s, want no items", out)
		}
	})

	t.Run("rejects a query the store cannot answer", func(t *testing.T) {
		s, _ := newToolServer(t)
		tool := s.findSimilarTool("default")
		for _, input := range []string{`{"query":"ab"}`, `{"query":"   "}`, `{}`, `"nope"`} {
			if _, err := runTool(t, tool, input); err == nil {
				t.Errorf("find_similar(%s) = nil error, want a rejection", input)
			}
		}
	})

	t.Run("reports a storage failure opaquely", func(t *testing.T) {
		st := newTestStore(t)
		s := NewRunner(st, "", nil, nil)
		if err := st.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
		_, err := runTool(t, s.findSimilarTool("default"), `{"query":"needle"}`)
		if err == nil || err.Error() != storageErrorMessage {
			t.Fatalf("closed-store find_similar error = %v, want %q", err, storageErrorMessage)
		}
	})
}

func TestFetchLinkTool(t *testing.T) {
	// The client is built at newServer time, so the guard override has to be
	// in place before the server exists.
	newAllowingServer := func(t *testing.T) *Runner {
		t.Helper()
		t.Setenv("KB_LINK_ALLOW_PRIVATE", "1")
		s, _ := newToolServer(t)
		return s
	}

	t.Run("returns stripped text", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte("line one\nline two"))
		}))
		defer upstream.Close()

		s := newAllowingServer(t)
		body, _ := json.Marshal(map[string]string{"url": upstream.URL + "/doc"})
		out := mustRunTool(t, s.fetchLinkTool(), string(body))
		if out != "line one\nline two" {
			t.Fatalf("fetch_link text = %q, want the control character stripped", out)
		}
	})

	t.Run("caps the document", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/markdown")
			_, _ = w.Write([]byte(strings.Repeat("a", fetchLinkMaxBytes+4096)))
		}))
		defer upstream.Close()

		s := newAllowingServer(t)
		body, _ := json.Marshal(map[string]string{"url": upstream.URL})
		out := mustRunTool(t, s.fetchLinkTool(), string(body))
		if len(out) != fetchLinkMaxBytes {
			t.Fatalf("fetch_link read %d bytes, want the %d cap", len(out), fetchLinkMaxBytes)
		}
	})

	t.Run("refuses what it cannot read as text", func(t *testing.T) {
		tests := []struct {
			name        string
			contentType string
			status      int
		}{
			{name: "binary", contentType: "application/pdf", status: http.StatusOK},
			{name: "missing type", contentType: "", status: http.StatusOK},
			{name: "not found", contentType: "text/plain", status: http.StatusNotFound},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if tt.contentType != "" {
						w.Header().Set("Content-Type", tt.contentType)
					} else {
						w.Header()["Content-Type"] = nil
					}
					w.WriteHeader(tt.status)
					_, _ = w.Write([]byte("payload"))
				}))
				defer upstream.Close()

				s := newAllowingServer(t)
				body, _ := json.Marshal(map[string]string{"url": upstream.URL})
				out, err := runTool(t, s.fetchLinkTool(), string(body))
				if err == nil {
					t.Fatalf("fetch_link = %q, want a refusal", out)
				}
				if strings.Contains(err.Error(), upstream.URL) {
					t.Errorf("error %q echoes the endpoint", err)
				}
			})
		}
	})

	t.Run("rejects a URL it must not dial", func(t *testing.T) {
		s := newAllowingServer(t)
		tool := s.fetchLinkTool()
		for _, input := range []string{
			`{"url":"ftp://example.com/x"}`,
			`{"url":"file:///etc/passwd"}`,
			`{"url":"not a url"}`,
			`{"url":"http://user:pass@example.com/x"}`,
			`{"url":""}`,
			`{}`,
			`"http://example.com"`,
		} {
			if _, err := runTool(t, tool, input); err == nil {
				t.Errorf("fetch_link(%s) = nil error, want a rejection", input)
			}
		}
	})

	// Without the explicit override the guarded dialer refuses loopback, which
	// is what keeps a model-chosen URL off the host's private network.
	t.Run("blocks a private address without the override", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("secret"))
		}))
		defer upstream.Close()

		t.Setenv("KB_LINK_ALLOW_PRIVATE", "")
		s, _ := newToolServer(t)
		body, _ := json.Marshal(map[string]string{"url": upstream.URL})
		out, err := runTool(t, s.fetchLinkTool(), string(body))
		if err == nil {
			t.Fatalf("fetch_link to loopback = %q, want the SSRF guard to refuse", out)
		}
		if !errors.Is(err, errFetchLinkFailed) {
			t.Fatalf("error = %v, want the generic fetch failure", err)
		}
	})

	// The forge opt-in is for hosts the operator configured as sources, not
	// for URLs the model composes: an operator who allows a self-hosted forge
	// to resolve privately must not also expose the LAN and the cloud metadata
	// service to fetch_link.
	t.Run("ignores the forge private-address opt-in", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("credentials"))
		}))
		defer upstream.Close()

		t.Setenv("KB_FORGE_ALLOW_PRIVATE", "1")
		t.Setenv("KB_LINK_ALLOW_PRIVATE", "")
		s, _ := newToolServer(t)
		body, _ := json.Marshal(map[string]string{"url": upstream.URL})
		out, err := runTool(t, s.fetchLinkTool(), string(body))
		if err == nil {
			t.Fatalf("fetch_link to loopback = %q, want the SSRF guard to refuse", out)
		}
		if !errors.Is(err, errFetchLinkFailed) {
			t.Fatalf("error = %v, want the generic fetch failure", err)
		}
	})

	t.Run("refuses a body that stops early", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("Content-Length", "1024")
			_, _ = w.Write([]byte("truncated"))
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			panic(http.ErrAbortHandler)
		}))
		defer upstream.Close()

		s := newAllowingServer(t)
		body, _ := json.Marshal(map[string]string{"url": upstream.URL})
		out, err := runTool(t, s.fetchLinkTool(), string(body))
		if err == nil {
			t.Fatalf("fetch_link on a truncated body = %q, want a refusal", out)
		}
		if !errors.Is(err, errFetchLinkFailed) {
			t.Fatalf("error = %v, want the generic fetch failure", err)
		}
	})

	t.Run("reports a missing client", func(t *testing.T) {
		s, _ := newToolServer(t)
		s.linkClient = nil
		if _, err := runTool(t, s.fetchLinkTool(), `{"url":"https://example.com/doc"}`); err == nil {
			t.Fatal("fetch_link without a client = nil error, want a refusal")
		}
	})
}

func TestListTasksTool(t *testing.T) {
	s, st := newToolServer(t)
	addToolTask(t, st, board.Task{Title: "Write the parser", Status: board.StatusTodo, Tags: []string{"backend"}, Prio: 2})
	addToolTask(t, st, board.Task{Title: "Ship the parser", Status: board.StatusDoing, Tags: []string{"backend", "urgent"}})
	addToolTask(t, st, board.Task{Title: "Unrelated chore", Status: board.StatusTodo})
	if _, err := st.AddTask("alice", board.Task{Title: "Parser for alice"}); err != nil {
		t.Fatalf("AddTask alice: %v", err)
	}
	tool := s.listTasksTool("default")

	decode := func(t *testing.T, out string) []toolTask {
		t.Helper()
		var got struct {
			Tasks []toolTask `json:"tasks"`
		}
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("list_tasks result %s: %v", out, err)
		}
		return got.Tasks
	}

	t.Run("lists the whole board", func(t *testing.T) {
		tasks := decode(t, mustRunTool(t, tool, `{}`))
		if len(tasks) != 3 {
			t.Fatalf("tasks = %d, want the caller's three", len(tasks))
		}
		for _, task := range tasks {
			if task.ID == "" || task.Title == "" || task.Status == "" {
				t.Errorf("task %+v is missing wire fields", task)
			}
		}
	})

	t.Run("applies filters", func(t *testing.T) {
		tests := []struct {
			name  string
			input string
			want  int
		}{
			{name: "status", input: `{"status":"doing"}`, want: 1},
			{name: "search", input: `{"search":"parser"}`, want: 2},
			{name: "tags", input: `{"tags":["urgent"]}`, want: 1},
			{name: "combined", input: `{"status":"todo","search":"parser"}`, want: 1},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if tasks := decode(t, mustRunTool(t, tool, tt.input)); len(tasks) != tt.want {
					t.Fatalf("list_tasks(%s) = %d tasks, want %d", tt.input, len(tasks), tt.want)
				}
			})
		}
	})

	t.Run("rejects an unknown column", func(t *testing.T) {
		out, err := runTool(t, tool, `{"status":"backlog"}`)
		if err == nil {
			t.Fatalf("list_tasks with a bad status = %s, want an error", out)
		}
		if !strings.Contains(err.Error(), "todo") {
			t.Errorf("error = %q, want it to name the legal columns", err)
		}
	})

	t.Run("rejects input that is not an object", func(t *testing.T) {
		if _, err := runTool(t, tool, `"everything"`); err == nil {
			t.Fatal("list_tasks with a bare string = nil error, want a rejection")
		}
	})

	t.Run("reports a storage failure opaquely", func(t *testing.T) {
		closed := newTestStore(t)
		other := NewRunner(closed, "", nil, nil)
		if err := closed.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
		_, err := runTool(t, other.listTasksTool("default"), `{}`)
		if err == nil || err.Error() != storageErrorMessage {
			t.Fatalf("closed-store list_tasks error = %v, want %q", err, storageErrorMessage)
		}
	})
}

func TestGetTaskTool(t *testing.T) {
	s, st := newToolServer(t)
	created := addToolTask(t, st, board.Task{
		Title:  "Investigate the drift",
		Desc:   "context",
		Status: board.StatusTodo,
		Effort: "M",
		Tags:   []string{"ops"},
		Checks: []board.Check{{Text: "reproduce"}},
	})
	tool := s.getTaskTool("default")

	// Include the first hyphen so the UUID prefix can never be interpreted as
	// an all-digit stable sequence number.
	for _, ref := range []string{created.ID, created.ID[:9], strconv.Itoa(created.Seq), "#" + strconv.Itoa(created.Seq)} {
		body, _ := json.Marshal(map[string]string{"ref": ref})
		var got toolTask
		if err := json.Unmarshal([]byte(mustRunTool(t, tool, string(body))), &got); err != nil {
			t.Fatalf("get_task(%q): %v", ref, err)
		}
		if got.ID != created.ID || got.Title != created.Title || len(got.Checks) != 1 {
			t.Fatalf("get_task(%q) = %+v, want the created task", ref, got)
		}
	}

	t.Run("points a miss at list_tasks", func(t *testing.T) {
		_, err := runTool(t, tool, `{"ref":"99"}`)
		if err == nil || !strings.Contains(err.Error(), "list_tasks") {
			t.Fatalf("get_task miss error = %v, want it to name list_tasks", err)
		}
	})

	t.Run("rejects an empty reference", func(t *testing.T) {
		for _, input := range []string{`{"ref":"  "}`, `{}`, `7`} {
			if _, err := runTool(t, tool, input); err == nil {
				t.Errorf("get_task(%s) = nil error, want a rejection", input)
			}
		}
	})

	t.Run("stays inside the caller's board", func(t *testing.T) {
		other, err := st.AddTask("alice", board.Task{Title: "Alice only"})
		if err != nil {
			t.Fatalf("AddTask alice: %v", err)
		}
		body, _ := json.Marshal(map[string]string{"ref": other.ID})
		if _, err := runTool(t, tool, string(body)); err == nil {
			t.Fatal("get_task across boards = nil error, want a miss")
		}
	})
}

func TestUpdateTaskTool(t *testing.T) {
	// An absent key and an empty value are different instructions: the first
	// leaves the field alone, the second clears it.
	t.Run("distinguishes an absent field from an empty one", func(t *testing.T) {
		s, st := newToolServer(t)
		created := addToolTask(t, st, board.Task{
			Title:  "Original title",
			Desc:   "original description",
			Due:    "2026-01-01",
			Effort: "L",
			Tags:   []string{"keep"},
		})
		tool := s.updateTaskTool("default")

		body, _ := json.Marshal(map[string]any{"ref": created.ID, "title": "New title"})
		var got toolTask
		if err := json.Unmarshal([]byte(mustRunTool(t, tool, string(body))), &got); err != nil {
			t.Fatalf("update_task: %v", err)
		}
		if got.Title != "New title" {
			t.Errorf("title = %q, want the update applied", got.Title)
		}
		if got.Desc != "original description" || got.Due != "2026-01-01" ||
			got.Effort != "L" || len(got.Tags) != 1 {
			t.Fatalf("absent fields changed: %+v", got)
		}

		// A fresh value, not the one above: the cleared fields are omitted from
		// the result, so decoding over the old task would keep its values.
		var cleared toolTask
		body, _ = json.Marshal(map[string]any{"ref": created.ID, "desc": "", "due": "", "tags": []string{}})
		if err := json.Unmarshal([]byte(mustRunTool(t, tool, string(body))), &cleared); err != nil {
			t.Fatalf("clearing update_task: %v", err)
		}
		if cleared.Desc != "" || cleared.Due != "" || len(cleared.Tags) != 0 {
			t.Fatalf("empty values did not clear the fields: %+v", cleared)
		}
		if cleared.Title != "New title" || cleared.Effort != "L" {
			t.Fatalf("clearing update touched absent fields: %+v", cleared)
		}
	})

	t.Run("sanitizes model text and replaces lists", func(t *testing.T) {
		s, st := newToolServer(t)
		created := addToolTask(t, st, board.Task{Title: "Card", Tags: []string{"old"}, Checks: []board.Check{{Text: "old step"}}})
		tool := s.updateTaskTool("default")

		body, _ := json.Marshal(map[string]any{
			"ref":    created.ID,
			"title":  "  Clean title  ",
			"emoji":  "🚀 trailing",
			"tags":   []string{"new"},
			"checks": []map[string]any{{"text": "fresh step", "done": true}},
		})
		var got toolTask
		if err := json.Unmarshal([]byte(mustRunTool(t, tool, string(body))), &got); err != nil {
			t.Fatalf("update_task: %v", err)
		}
		if got.Title != "Clean title" {
			t.Errorf("title = %q, want the control character stripped", got.Title)
		}
		if got.Emoji != "🚀" {
			t.Errorf("emoji = %q, want the leading emoji only", got.Emoji)
		}
		if len(got.Tags) != 1 || got.Tags[0] != "new" {
			t.Errorf("tags = %v, want the list replaced", got.Tags)
		}
		if len(got.Checks) != 1 || got.Checks[0].Text != "fresh step" || !got.Checks[0].Done {
			t.Errorf("checks = %+v, want the list replaced", got.Checks)
		}

		body, _ = json.Marshal(map[string]any{"ref": created.ID, "prio": 1, "blocked": true, "due": "2026-03-04", "effort": "s"})
		var reprioritized toolTask
		if err := json.Unmarshal([]byte(mustRunTool(t, tool, string(body))), &reprioritized); err != nil {
			t.Fatalf("update_task: %v", err)
		}
		if reprioritized.Prio != 1 || !reprioritized.Blocked ||
			reprioritized.Due != "2026-03-04" || reprioritized.Effort != "S" {
			t.Fatalf("update_task = %+v, want prio 1, blocked, the due date, and effort S", reprioritized)
		}
	})

	t.Run("rejects input that is not an object", func(t *testing.T) {
		s, _ := newToolServer(t)
		if _, err := runTool(t, s.updateTaskTool("default"), `["ref"]`); err == nil {
			t.Fatal("update_task with an array = nil error, want a rejection")
		}
	})

	t.Run("refuses a bad field without writing", func(t *testing.T) {
		s, st := newToolServer(t)
		created := addToolTask(t, st, board.Task{Title: "Untouched", Prio: 3})
		tool := s.updateTaskTool("default")

		tests := []struct {
			name  string
			input map[string]any
			want  string
		}{
			{name: "prio", input: map[string]any{"ref": created.ID, "prio": 9}, want: "prio"},
			{name: "due", input: map[string]any{"ref": created.ID, "due": "2026-13-45"}, want: "due"},
			{name: "effort", input: map[string]any{"ref": created.ID, "effort": "XL"}, want: "effort"},
			{name: "empty check", input: map[string]any{"ref": created.ID, "checks": []map[string]any{{"text": " "}}}, want: "checks[0]"},
			{name: "blank title", input: map[string]any{"ref": created.ID, "title": "  "}, want: "title"},
			{name: "missing ref", input: map[string]any{"title": "x"}, want: "ref"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				body, _ := json.Marshal(tt.input)
				if _, err := runTool(t, tool, string(body)); err == nil {
					t.Fatalf("update_task(%s) = nil error, want a rejection", body)
				} else if !strings.Contains(err.Error(), tt.want) {
					t.Errorf("error = %q, want it to name %q", err, tt.want)
				}
			})
		}

		after, err := st.Task("default", created.ID)
		if err != nil {
			t.Fatalf("Task after refusals: %v", err)
		}
		if after.Title != "Untouched" || after.Prio != 3 || after.Due != "" || after.Effort != "" || len(after.Checks) != 0 {
			t.Fatalf("refused updates still landed: %+v", after)
		}
	})

	t.Run("reports an unresolvable reference", func(t *testing.T) {
		s, st := newToolServer(t)
		first := addToolTask(t, st, board.Task{Title: "First"})
		tool := s.updateTaskTool("default")

		body, _ := json.Marshal(map[string]any{"ref": "does-not-exist", "title": "x"})
		if _, err := runTool(t, tool, string(body)); err == nil || !strings.Contains(err.Error(), "list_tasks") {
			t.Fatalf("update_task miss error = %v, want it to name list_tasks", err)
		}
		body, _ = json.Marshal(map[string]any{"ref": first.ID, "title": "Renamed"})
		mustRunTool(t, tool, string(body))
	})
}

// TestMarshalToolResult covers the guard on the encoder: an unencodable value
// is a server bug, and the model gets a message it cannot mistake for its own.
func TestMarshalToolResult(t *testing.T) {
	out, err := marshalToolResult(make(chan int))
	if err == nil {
		t.Fatalf("marshalToolResult(chan) = %q, want an error", out)
	}
	if strings.Contains(err.Error(), "chan") {
		t.Errorf("error = %q, want no encoder detail", err)
	}
}

// TestTaskRefError keeps the store's id sentinels mapped onto advice the model
// can follow, and leaves every other failure intact.
func TestTaskRefError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "not found", err: store.ErrNotFound, want: "list_tasks"},
		{name: "ambiguous", err: store.ErrAmbiguous, want: "longer id prefix"},
		{name: "other", err: errors.New("store: invalid effort \"XL\""), want: "invalid effort"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := taskRefError(tt.err, "7"); !strings.Contains(got.Error(), tt.want) {
				t.Fatalf("taskRefError = %q, want it to contain %q", got, tt.want)
			}
		})
	}
}
