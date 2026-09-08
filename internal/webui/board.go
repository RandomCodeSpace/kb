package webui

import (
	"net/http"
	"strings"
	"time"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/project"
	"github.com/RandomCodeSpace/kb/internal/store"
)

func init() { featureRoutes = append(featureRoutes, (*server).boardRoutes) }

// The board-level half of the API: the things the TUI does to a whole board
// rather than to one card — the project switcher (p/P), the command palette's
// action list (ctrl+k), the graveyard reason behind a cancelled card, the
// forge link lookup, and the shipped-today tally the celebration counts.
//
// Everything here is read-only. The selected project is the browser's own
// view state: there is no ambient project on the server, so every card the
// web creates names its project in the request.

const (
	projectsPattern = "/api/projects"
	// byLinkPattern sits beside /api/similar rather than under /api/tasks/
	// because a literal /api/tasks/by-link and the method-less 405 catch-all
	// the route table registers for GET /api/tasks/{ref} are an ambiguous pair
	// that net/http refuses to serve.
	byLinkPattern = "/api/by-link"
	// shippedDateLayout is the TUI's shipped-record date format
	// (internal/tui/ship_actions.go), so both surfaces name a day the same way.
	shippedDateLayout = "2006-01-02"
)

func (s *server) boardRoutes() []route {
	return []route{
		{"GET", "/api/actions", s.listActions},
		{"GET", projectsPattern, s.listProjects},
		{"GET", byLinkPattern, s.tasksByLink},
		{"GET", "/api/tasks/{ref}/tombstone", s.taskTombstone},
		{"GET", "/api/shipped", s.shipped},
	}
}

// --- actions ---

// actionJSON is one row of the keyboard action registry as the web palette
// reads it. endpoint names the API call the web UI runs for that row, which
// the TUI has no need of because it replays the key press instead.
type actionJSON struct {
	ID       string `json:"id"`
	Group    string `json:"group"`
	Key      string `json:"key"`
	Hint     string `json:"hint"`
	Name     string `json:"name"`
	Enabled  bool   `json:"enabled"`
	Endpoint string `json:"endpoint,omitempty"`
}

// webActions is the web's copy of the TUI's action registry
// (internal/tui/action/action.go: `registry`), in the same order, plus the
// endpoint each row maps to on this API.
//
// It is a copy rather than an import because that package depends on
// bubbletea, and the HTTP server has no business linking a terminal UI
// framework in to spell out a key table. TestActionsMirrorTUIRegistry
// compares the two row by row, so a rebind or a reword in the registry fails
// this package's tests instead of silently drifting.
//
// Enabled reports whether this API can perform the action, not whether the
// TUI can, and is the web's answer to the registry's feature gates. Every row
// is enabled today: the navigation rows are client-side, and the four gated
// rows all have an endpoint (card editor, settings, ai/split, forge/import).
// A row whose endpoint goes away is reported disabled rather than omitted, the
// way the TUI's help pane dims a row the board was not built with.
var webActions = []actionJSON{
	{"openCard", "navigate", "enter", "enter", "open card", true, "GET /api/tasks/{ref}"},
	{"liftCard", "navigate", "space", "space", "lift or drop card", true, "POST /api/tasks/{ref}/move"},
	{"selectCard", "navigate", "j", "j/k", "select card", true, ""},
	{"selectColumn", "navigate", "h", "h/l", "select column", true, ""},
	{"jumpColumn", "navigate", "1", "1-4", "jump to column", true, ""},
	{"filterText", "navigate", "/", "/", "text filter", true, "GET /api/tasks?q="},
	{"filterLabel", "navigate", "f", "f", "label filter", true, "GET /api/tasks?tag="},
	{"filterClear", "navigate", "X", "X", "clear filter", true, "GET /api/tasks"},
	{"switchProject", "navigate", "p", "p/P", "switch project", true, "GET /api/projects"},
	{"openPalette", "navigate", "ctrl+k", "ctrl+k", "command palette", true, "GET /api/actions"},

	{"shipCard", "act", "t", "t", "ship card", true, "POST /api/tasks/{ref}/move"},
	{"cancelCard", "act", "x", "x", "cancel card", true, "POST /api/tasks/{ref}/cancel"},
	{"restoreCard", "act", "r", "r", "restore card", true, "POST /api/tasks/{ref}/restore"},
	{"purgeCard", "act", "D", "D", "permanently delete", true, "DELETE /api/tasks/{ref}"},
	{"newCard", "act", "n", "n", "new card", true, "POST /api/tasks"},
	{"editCard", "act", "e", "e", "edit card", true, "PATCH /api/tasks/{ref}"},
	{"openSettings", "act", "s", "s", "settings", true, "GET /api/settings"},
	{"splitADR", "act", "a", "a", "split ADR", true, "POST /api/ai/split"},
	{"importIssue", "act", "i", "i", "import forge issue", true, "POST /api/forge/import"},

	{"closeHelp", "dismiss", "?", "? or esc", "close help", true, ""},
	{"quit", "dismiss", "q", "q", "quit", true, ""},
}

// listActions serves the whole registry, dismiss rows included. The palette
// itself lists what the TUI's action.Listed does: the enabled rows outside the
// dismiss group, minus openPalette.
func (s *server) listActions(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string][]actionJSON{"actions": webActions})
}

// --- projects ---

// statusCountsJSON is one project's cards per column, cancelled included, the
// way the TUI's project switcher and `kb project list` count them.
type statusCountsJSON struct {
	Todo      int `json:"todo"`
	Doing     int `json:"doing"`
	Done      int `json:"done"`
	Cancelled int `json:"cancelled"`
}

func (c *statusCountsJSON) add(status board.Status) {
	switch status {
	case board.StatusTodo:
		c.Todo++
	case board.StatusDoing:
		c.Doing++
	case board.StatusDone:
		c.Done++
	case board.StatusCancelled:
		c.Cancelled++
	}
}

type projectJSON struct {
	Name   string           `json:"name"`
	Counts statusCountsJSON `json:"counts"`
}

// listProjects mirrors `kb project list` and the TUI's switcher: every project
// on the board. A task carrying two project labels — only possible on a board
// a foreign writer touched — counts under both, as it does in the CLI.
func (s *server) listProjects(w http.ResponseWriter, _ *http.Request) {
	tasks, err := s.st.FilterTasks(s.user, store.TaskFilter{})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	counts := map[string]*statusCountsJSON{}
	for _, t := range tasks {
		named, _ := project.SplitTags(t.Tags)
		for _, name := range named {
			row, seen := counts[name]
			if !seen {
				row = &statusCountsJSON{}
				counts[name] = row
			}
			row.add(t.Status)
		}
	}
	names := projectNames(tasks)
	rows := make([]projectJSON, 0, len(names))
	for _, name := range names {
		row := projectJSON{Name: name}
		if c := counts[name]; c != nil {
			row.Counts = *c
		}
		rows = append(rows, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": rows})
}

// --- graveyard and provenance ---

type tombstoneJSON struct {
	TaskID   string `json:"taskId"`
	Reason   string `json:"reason"`
	KilledAt string `json:"killedAt"`
}

// taskTombstone serves the reason a card was cancelled with, which the card
// detail view shows under a cancelled card. A cancelled card with no recorded
// reason is a 404, not an empty reason.
func (s *server) taskTombstone(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("ref")
	t, err := s.st.Task(s.user, ref)
	if err != nil {
		fail(w, err, ref)
		return
	}
	tomb, found, err := s.st.Tombstone(s.user, t.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "no cancellation reason recorded for task "+ref)
		return
	}
	writeJSON(w, http.StatusOK, tombstoneJSON{TaskID: tomb.TaskID, Reason: tomb.Reason, KilledAt: tomb.KilledAt})
}

// tasksByLink answers "is this forge item already on the board?" with the
// cards carrying link as one whole tag, in the same stub shape /api/similar
// uses.
func (s *server) tasksByLink(w http.ResponseWriter, r *http.Request) {
	link := strings.TrimSpace(r.URL.Query().Get("link"))
	if link == "" {
		writeError(w, http.StatusBadRequest, "link must not be empty")
		return
	}
	hits, err := s.st.TasksByLink(s.user, link)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]similarJSON, 0, len(hits))
	for _, h := range hits {
		items = append(items, similarJSON{ID: h.ID, Title: h.Title, Status: h.Status, Via: h.Via, Link: h.Link})
	}
	writeJSON(w, http.StatusOK, map[string][]similarJSON{"items": items})
}

// --- shipped ---

type shippedJSON struct {
	Date  string `json:"date"`
	Count int    `json:"count"`
	Seqs  []int  `json:"seqs"`
}

// shipped is the tally the TUI celebrates: the cards that reached done on one
// local day. The TUI keeps its own list in its preference file because it
// counts the ships made in that session; this one is derived from the board,
// so it survives a reload and needs no state of its own.
func (s *server) shipped(w http.ResponseWriter, r *http.Request) {
	date := strings.TrimSpace(r.URL.Query().Get("date"))
	if date == "" {
		date = time.Now().Format(shippedDateLayout)
	}
	if _, err := time.Parse(shippedDateLayout, date); err != nil {
		writeError(w, http.StatusBadRequest, "invalid date "+date+": must be YYYY-MM-DD")
		return
	}
	tasks, err := s.st.FilterTasks(s.user, store.TaskFilter{Status: board.StatusDone})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := shippedJSON{Date: date, Seqs: []int{}}
	for _, t := range tasks {
		if t.MovedAt.Local().Format(shippedDateLayout) == date {
			out.Seqs = append(out.Seqs, t.Seq)
		}
	}
	out.Count = len(out.Seqs)
	writeJSON(w, http.StatusOK, out)
}
