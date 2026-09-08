package webui

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/cliapp"
	"github.com/RandomCodeSpace/kb/internal/project"
	"github.com/RandomCodeSpace/kb/internal/store"
)

// maxBodyBytes caps request bodies; the largest legitimate payload is a task
// with a long markdown description.
const maxBodyBytes = 1 << 20

const (
	taskPattern     = "/api/tasks/{ref}"
	contentType     = "Content-Type"
	jsonContentType = "application/json"
)

// featureRoutes collects the endpoints that feature files (settings, AI,
// forge, board operations) contribute from their init functions, so each
// feature owns its own file and the core route table stays put.
var featureRoutes []func(s *server) []route

// server holds the per-process API state: one store, one fixed user, the data
// directory the active project is resolved from, and the hub that pushes board
// changes to /api/events.
type server struct {
	st      *store.Store
	user    string
	dataDir string
	version string
	events  *eventHub
}

// route is one method+path registration; routes sharing a path also get a
// method-less catch-all so an unsupported method answers 405 in the JSON
// error shape instead of the mux's plain-text default.
type route struct {
	method, pattern string
	handler         http.HandlerFunc
}

func (s *server) routes(mux *http.ServeMux) {
	rs := []route{
		{"GET", "/api/meta", s.meta},
		{"GET", "/api/tasks", s.listTasks},
		{"POST", "/api/tasks", s.addTask},
		{"GET", taskPattern, s.getTask},
		{"PATCH", taskPattern, s.patchTask},
		{"DELETE", taskPattern, s.deleteTask},
		{"POST", "/api/tasks/{ref}/move", s.moveTask},
		{"POST", "/api/tasks/{ref}/cancel", s.cancelTask},
		{"POST", "/api/tasks/{ref}/restore", s.restoreTask},
		{"GET", "/api/tasks/{ref}/comments", s.listComments},
		{"POST", "/api/tasks/{ref}/comments", s.addComment},
		{"PUT", "/api/comments/{id}", s.updateComment},
		{"DELETE", "/api/comments/{id}", s.deleteComment},
		{"POST", "/api/links", s.link},
		{"DELETE", "/api/links", s.unlink},
		{"GET", "/api/similar", s.similar},
		{"GET", "/api/labels", s.labels},
	}
	for _, feature := range featureRoutes {
		rs = append(rs, feature(s)...)
	}
	allowed := map[string][]string{}
	for _, r := range rs {
		mux.HandleFunc(r.method+" "+r.pattern, r.handler)
		allowed[r.pattern] = append(allowed[r.pattern], r.method)
	}
	for pattern, methods := range allowed {
		allow := strings.Join(methods, ", ")
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Allow", allow)
			writeError(w, http.StatusMethodNotAllowed, "method "+r.Method+" not allowed")
		})
	}
}

// --- middleware ---

// secure applies the request-safety rules from docs/web-api.md: same-host
// Origin and a JSON Content-Type on mutating requests, a body cap, no-store
// on the API, nosniff everywhere.
func secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		if isMutating(r.Method) {
			if !originAllowed(r) {
				writeError(w, http.StatusForbidden, "cross-origin request refused")
				return
			}
			if r.ContentLength != 0 && !isJSONContentType(r.Header.Get(contentType)) {
				writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
}

func isMutating(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete:
		return true
	}
	return false
}

// originAllowed accepts a missing Origin (non-browser clients, same-origin
// fetches in older browsers) or one whose host matches the request Host.
// Anything else is a cross-site request and is refused.
func originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return u.Host != "" && strings.EqualFold(u.Host, r.Host)
}

func isJSONContentType(ct string) bool {
	mt, _, err := mime.ParseMediaType(ct)
	return err == nil && mt == jsonContentType
}

// --- wire types ---

// checkJSON is a checklist item on the wire, for input and output.
type checkJSON struct {
	Text string `json:"text"`
	Done bool   `json:"done,omitempty"`
}

// taskJSON mirrors mcpserv's task shape plus project, position and the
// timestamps the board view needs.
type taskJSON struct {
	ID        string      `json:"id"`
	Seq       int         `json:"seq,omitempty"`
	Emoji     string      `json:"emoji,omitempty"`
	Title     string      `json:"title"`
	Desc      string      `json:"desc,omitempty"`
	Status    string      `json:"status"`
	Blocked   bool        `json:"blocked,omitempty"`
	Prio      int         `json:"prio"`
	Due       string      `json:"due,omitempty"`
	Effort    string      `json:"effort,omitempty"`
	Tags      []string    `json:"tags,omitempty"`
	Checks    []checkJSON `json:"checks,omitempty"`
	Project   string      `json:"project"`
	Position  int         `json:"position"`
	CreatedAt string      `json:"createdAt"`
	MovedAt   string      `json:"movedAt"`
	UpdatedAt string      `json:"updatedAt"`
}

func toTaskJSON(t board.Task) taskJSON {
	out := taskJSON{
		ID:        t.ID,
		Seq:       t.Seq,
		Emoji:     t.Emoji,
		Title:     t.Title,
		Desc:      t.Desc,
		Status:    string(t.Status),
		Blocked:   t.Blocked,
		Prio:      t.Prio,
		Due:       t.Due,
		Effort:    t.Effort,
		Tags:      t.Tags,
		Project:   project.Of(t.Tags),
		Position:  t.Position,
		CreatedAt: t.CreatedAt.UTC().Format(time.RFC3339),
		MovedAt:   t.MovedAt.UTC().Format(time.RFC3339),
		UpdatedAt: t.UpdatedAt.UTC().Format(time.RFC3339),
	}
	for _, c := range t.Checks {
		out.Checks = append(out.Checks, checkJSON{Text: c.Text, Done: c.Done})
	}
	return out
}

func toTaskList(tasks []board.Task) []taskJSON {
	out := make([]taskJSON, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, toTaskJSON(t))
	}
	return out
}

func toBoardChecks(cs []checkJSON) []board.Check {
	out := make([]board.Check, 0, len(cs))
	for _, c := range cs {
		out = append(out, board.Check{Text: c.Text, Done: c.Done})
	}
	return out
}

type commentJSON struct {
	ID        int    `json:"id"`
	TaskID    string `json:"taskId"`
	TaskSeq   int    `json:"taskSeq"`
	Author    string `json:"author"`
	Body      string `json:"body"`
	CreatedAt string `json:"createdAt"`
}

func toCommentJSON(c store.Comment) commentJSON {
	return commentJSON{
		ID:        c.ID,
		TaskID:    c.TaskID,
		TaskSeq:   c.TaskSeq,
		Author:    c.Author,
		Body:      c.Body,
		CreatedAt: c.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func toCommentList(cs []store.Comment) []commentJSON {
	out := make([]commentJSON, 0, len(cs))
	for _, c := range cs {
		out = append(out, toCommentJSON(c))
	}
	return out
}

type linksJSON struct {
	Blocks    []taskJSON `json:"blocks"`
	BlockedBy []taskJSON `json:"blockedBy"`
}

type similarJSON struct {
	ID     string `json:"id,omitempty"`
	Title  string `json:"title,omitempty"`
	Status string `json:"status,omitempty"`
	Via    string `json:"via,omitempty"`
	Link   string `json:"link,omitempty"`
}

type addTaskInput struct {
	Title   string      `json:"title"`
	Desc    string      `json:"desc"`
	Status  string      `json:"status"`
	Blocked bool        `json:"blocked"`
	Prio    int         `json:"prio"`
	Due     string      `json:"due"`
	Effort  string      `json:"effort"`
	Tags    []string    `json:"tags"`
	Checks  []checkJSON `json:"checks"`
	Emoji   string      `json:"emoji"`
	Project string      `json:"project"`
}

// patchTaskInput uses pointers so an omitted field is left alone and an
// explicit empty string clears it.
type patchTaskInput struct {
	Title   *string      `json:"title"`
	Desc    *string      `json:"desc"`
	Blocked *bool        `json:"blocked"`
	Prio    *int         `json:"prio"`
	Due     *string      `json:"due"`
	Effort  *string      `json:"effort"`
	Tags    *[]string    `json:"tags"`
	Checks  *[]checkJSON `json:"checks"`
	Emoji   *string      `json:"emoji"`
	Project string       `json:"project"`
	Status  *string      `json:"status"`
	Index   *int         `json:"index"`
	Force   bool         `json:"force"`
}

type moveTaskInput struct {
	Status string `json:"status"`
	Index  *int   `json:"index"`
	Force  bool   `json:"force"`
}

// --- JSON plumbing ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "encode response: "+err.Error())
		return
	}
	w.Header().Set(contentType, jsonContentType)
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// writeError emits the contract's error shape.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

// decode reads one JSON value from the body. An empty body surfaces as
// io.EOF so endpoints with optional bodies can accept it.
func decode(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

// badBody turns a decode failure into the right status: an oversized body
// is 413, everything else (including an empty body where one is required)
// is 400.
func badBody(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, "request body exceeds 1 MiB")
		return
	}
	if errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "request body must be a JSON object")
		return
	}
	writeError(w, http.StatusBadRequest, "malformed JSON: "+err.Error())
}

// fail maps a store or resolution error onto the contract's status table.
// Store validation errors keep their text; the default is 400 because every
// store failure a request can provoke is a bad input.
func fail(w http.ResponseWriter, err error, ref string) {
	var blocked *store.CompletionBlockedError
	switch {
	case errors.As(err, &blocked):
		// The flag is what lets the UI offer a force retry; a plain 409
		// (deleting a task that is not cancelled) has no such retry.
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "completionBlocked": true})
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, fmt.Sprintf("no task matches id %q", ref))
	case errors.Is(err, store.ErrAmbiguous):
		writeError(w, http.StatusBadRequest, fmt.Sprintf("task id prefix %q is ambiguous; use more characters", ref))
	case errors.Is(err, store.ErrTaskNotCancelled):
		writeError(w, http.StatusConflict, "only cancelled tasks can be deleted permanently")
	default:
		writeError(w, http.StatusBadRequest, err.Error())
	}
}

// parseStatus validates a wire status string.
func parseStatus(s string) (board.Status, error) {
	st := board.Status(s)
	if !st.Valid() {
		return "", fmt.Errorf("invalid status %q: must be todo, doing, done, or cancelled", s)
	}
	return st, nil
}

// doneGuard is the checklist/blocked refusal every local surface applies
// when a task heads to done without force; the store adds the open-blocker
// gate whenever a guard is present.
func doneGuard(force bool, moveTo *board.Status) func(board.Task) error {
	if force || moveTo == nil || *moveTo != board.StatusDone {
		return nil
	}
	return func(t board.Task) error {
		warn := store.CompletionWarning(t)
		if warn == "" {
			return nil
		}
		return store.NewCompletionBlockedError(warn, "#"+strconv.Itoa(t.Seq), t.Title)
	}
}

// --- handlers ---

type metaJSON struct {
	Version       string   `json:"version"`
	ActiveProject string   `json:"activeProject"`
	Projects      []string `json:"projects"`
	Labels        []string `json:"labels"`
	Statuses      []string `json:"statuses"`
}

func (s *server) meta(w http.ResponseWriter, _ *http.Request) {
	active, _, err := cliapp.ActiveProject(s.dataDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	tasks, err := s.st.FilterTasks(s.user, store.TaskFilter{})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	labels, err := s.st.Labels(s.user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	statuses := make([]string, 0, len(board.Statuses))
	for _, st := range board.Statuses {
		statuses = append(statuses, string(st))
	}
	writeJSON(w, http.StatusOK, metaJSON{
		Version:       s.version,
		ActiveProject: active,
		Projects:      projectNames(tasks, active),
		Labels:        nonNil(labels),
		Statuses:      statuses,
	})
}

// projectNames lists the distinct projects on the board plus the active one,
// sorted with inbox last so the catch-all never shadows a real project.
func projectNames(tasks []board.Task, active string) []string {
	seen := map[string]bool{}
	if active != "" {
		seen[active] = true
	}
	for _, t := range tasks {
		named, _ := project.SplitTags(t.Tags)
		for _, name := range named {
			seen[name] = true
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		if name != project.Inbox {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if seen[project.Inbox] {
		names = append(names, project.Inbox)
	}
	return names
}

func nonNil(ss []string) []string {
	if ss == nil {
		return []string{}
	}
	return ss
}

func (s *server) listTasks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := store.TaskFilter{Search: q.Get("q"), Tags: q["tag"]}
	if raw := q.Get("status"); raw != "" {
		st, err := parseStatus(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		filter.Status = st
	}
	if p := q.Get("project"); p != "" {
		filter.Tags = append(slices.Clone(filter.Tags), project.Label(p))
	}
	tasks, err := s.st.FilterTasks(s.user, filter)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	body, _ := json.Marshal(map[string][]taskJSON{"tasks": toTaskList(tasks)})
	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:]) + `"`
	w.Header().Set("ETag", etag)
	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set(contentType, jsonContentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// etagMatches compares a (possibly weak, possibly list-valued) If-None-Match
// header against the strong etag the listing computed.
func etagMatches(header, etag string) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimPrefix(strings.TrimSpace(candidate), "W/")
		if candidate == etag {
			return true
		}
	}
	return false
}

func (s *server) addTask(w http.ResponseWriter, r *http.Request) {
	var in addTaskInput
	if err := decode(r, &in); err != nil {
		badBody(w, err)
		return
	}
	if strings.TrimSpace(in.Title) == "" {
		writeError(w, http.StatusBadRequest, "title must not be empty")
		return
	}
	if in.Prio != 0 && !board.ValidPrio(in.Prio) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid prio %d: must be 1 high, 2 medium, or 3 low", in.Prio))
		return
	}
	t := board.Task{
		Emoji:   in.Emoji,
		Title:   in.Title,
		Desc:    in.Desc,
		Blocked: in.Blocked,
		Prio:    in.Prio,
		Due:     in.Due,
		Effort:  in.Effort,
		Checks:  toBoardChecks(in.Checks),
	}
	if in.Status != "" {
		st, err := parseStatus(in.Status)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		t.Status = st
	}
	tags, err := cliapp.ProjectTags(in.Tags, in.Project, s.dataDir, "")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	t.Tags = tags
	created, err := s.st.AddTask(s.user, t)
	if err != nil {
		fail(w, err, "")
		return
	}
	writeJSON(w, http.StatusCreated, toTaskJSON(created))
}

type taskDetailJSON struct {
	Task     taskJSON      `json:"task"`
	Comments []commentJSON `json:"comments"`
	Links    linksJSON     `json:"links"`
}

func (s *server) getTask(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("ref")
	t, err := s.st.Task(s.user, ref)
	if err != nil {
		fail(w, err, ref)
		return
	}
	comments, err := s.st.Comments(s.user, t.ID)
	if err != nil {
		fail(w, err, ref)
		return
	}
	links, err := s.st.TaskLinks(s.user, t.ID)
	if err != nil {
		fail(w, err, ref)
		return
	}
	writeJSON(w, http.StatusOK, taskDetailJSON{
		Task:     toTaskJSON(t),
		Comments: toCommentList(comments),
		Links:    linksJSON{Blocks: toTaskList(links.Blocks), BlockedBy: toTaskList(links.BlockedBy)},
	})
}

func (s *server) patchTask(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("ref")
	var in patchTaskInput
	if err := decode(r, &in); err != nil {
		badBody(w, err)
		return
	}
	if in.Prio != nil && !board.ValidPrio(*in.Prio) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid prio %d: must be 1 high, 2 medium, or 3 low", *in.Prio))
		return
	}
	patch := store.TaskPatch{
		Emoji:   in.Emoji,
		Title:   in.Title,
		Desc:    in.Desc,
		Due:     in.Due,
		Effort:  in.Effort,
		Blocked: in.Blocked,
		Prio:    in.Prio,
		Tags:    in.Tags,
	}
	if in.Checks != nil {
		bc := toBoardChecks(*in.Checks)
		patch.Checks = &bc
	}
	// Status is parsed before anything is written so a bad status rejects
	// the whole call instead of persisting the field patch and then failing.
	var moveTo *board.Status
	if in.Status != nil {
		st, err := parseStatus(*in.Status)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		moveTo = &st
	}
	if err := s.applyProjectPatch(ref, &patch, in.Project); err != nil {
		fail(w, err, ref)
		return
	}
	t, err := s.st.UpdateAndMoveTask(s.user, ref, patch, moveTo, in.Index, doneGuard(in.Force, moveTo))
	if err != nil {
		fail(w, err, ref)
		return
	}
	writeJSON(w, http.StatusOK, toTaskJSON(t))
}

// applyProjectPatch holds the one-project invariant across an update, the
// way mcpserv does: the task is read only when labels are actually being
// rewritten (tags replaced, or project moves the task), so an update that
// touches neither leaves the project alone.
func (s *server) applyProjectPatch(ref string, patch *store.TaskPatch, projectName string) error {
	if patch.Tags == nil && strings.TrimSpace(projectName) == "" {
		return nil
	}
	t, err := s.st.Task(s.user, ref)
	if err != nil {
		return err
	}
	var base []string
	if patch.Tags != nil {
		base = *patch.Tags
	} else {
		_, base = project.SplitTags(t.Tags)
	}
	tags, err := cliapp.ProjectTags(base, projectName, s.dataDir, cliapp.CurrentProjectOf(t))
	if err != nil {
		return err
	}
	patch.Tags = &tags
	return nil
}

func (s *server) moveTask(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("ref")
	var in moveTaskInput
	if err := decode(r, &in); err != nil {
		badBody(w, err)
		return
	}
	st, err := parseStatus(in.Status)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	t, err := s.st.UpdateAndMoveTask(s.user, ref, store.TaskPatch{}, &st, in.Index, doneGuard(in.Force, &st))
	if err != nil {
		fail(w, err, ref)
		return
	}
	writeJSON(w, http.StatusOK, toTaskJSON(t))
}

func (s *server) cancelTask(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("ref")
	var in struct {
		Reason string `json:"reason"`
	}
	// The body is optional: a bare POST cancels without a reason.
	if err := decode(r, &in); err != nil && !errors.Is(err, io.EOF) {
		badBody(w, err)
		return
	}
	var reason *string
	if strings.TrimSpace(in.Reason) != "" {
		reason = &in.Reason
	}
	t, err := s.st.CancelTask(s.user, ref, reason)
	if err != nil {
		fail(w, err, ref)
		return
	}
	writeJSON(w, http.StatusOK, toTaskJSON(t))
}

func (s *server) restoreTask(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("ref")
	todo := board.StatusTodo
	t, err := s.st.UpdateAndMoveTask(s.user, ref, store.TaskPatch{}, &todo, nil, nil)
	if err != nil {
		fail(w, err, ref)
		return
	}
	writeJSON(w, http.StatusOK, toTaskJSON(t))
}

func (s *server) deleteTask(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("ref")
	t, err := s.st.DeleteCancelledTask(s.user, ref)
	if err != nil {
		fail(w, err, ref)
		return
	}
	writeJSON(w, http.StatusOK, toTaskJSON(t))
}

func (s *server) listComments(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("ref")
	comments, err := s.st.Comments(s.user, ref)
	if err != nil {
		fail(w, err, ref)
		return
	}
	writeJSON(w, http.StatusOK, map[string][]commentJSON{"comments": toCommentList(comments)})
}

func (s *server) addComment(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("ref")
	var in struct {
		Body string `json:"body"`
	}
	if err := decode(r, &in); err != nil {
		badBody(w, err)
		return
	}
	c, err := s.st.AddComment(s.user, ref, s.user, in.Body)
	if err != nil {
		fail(w, err, ref)
		return
	}
	writeJSON(w, http.StatusCreated, toCommentJSON(c))
}

// commentID parses a comment path id ("c3" or "3"); zero means invalid.
func commentID(raw string) int {
	id, err := strconv.Atoi(strings.TrimPrefix(raw, "c"))
	if err != nil || id <= 0 {
		return 0
	}
	return id
}

func (s *server) updateComment(w http.ResponseWriter, r *http.Request) {
	raw := r.PathValue("id")
	id := commentID(raw)
	if id == 0 {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid comment id %q", raw))
		return
	}
	var in struct {
		Body string `json:"body"`
	}
	if err := decode(r, &in); err != nil {
		badBody(w, err)
		return
	}
	c, err := s.st.UpdateComment(s.user, id, in.Body)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, fmt.Sprintf("no comment matches id %q", raw))
		return
	}
	if err != nil {
		fail(w, err, raw)
		return
	}
	writeJSON(w, http.StatusOK, toCommentJSON(c))
}

func (s *server) deleteComment(w http.ResponseWriter, r *http.Request) {
	raw := r.PathValue("id")
	id := commentID(raw)
	if id == 0 {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid comment id %q", raw))
		return
	}
	c, err := s.st.DeleteComment(s.user, id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, fmt.Sprintf("no comment matches id %q", raw))
		return
	}
	if err != nil {
		fail(w, err, raw)
		return
	}
	writeJSON(w, http.StatusOK, toCommentJSON(c))
}

func (s *server) link(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Blocker string `json:"blocker"`
		Blocked string `json:"blocked"`
	}
	if err := decode(r, &in); err != nil {
		badBody(w, err)
		return
	}
	blocker, blocked, err := s.st.Link(s.user, in.Blocker, in.Blocked)
	if err != nil {
		fail(w, err, in.Blocker+"/"+in.Blocked)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]taskJSON{"blocker": toTaskJSON(blocker), "blocked": toTaskJSON(blocked)})
}

func (s *server) unlink(w http.ResponseWriter, r *http.Request) {
	var in struct {
		A string `json:"a"`
		B string `json:"b"`
	}
	if err := decode(r, &in); err != nil {
		badBody(w, err)
		return
	}
	if err := s.st.Unlink(s.user, in.A, in.B); err != nil {
		fail(w, err, in.A+"/"+in.B)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) similar(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 3
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid limit %q", raw))
			return
		}
		limit = min(n, 10)
	}
	hits, err := s.st.SearchSimilar(s.user, q.Get("q"), "", nil, limit)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	items := make([]similarJSON, 0, len(hits))
	for _, h := range hits {
		items = append(items, similarJSON{ID: h.ID, Title: h.Title, Status: h.Status, Via: h.Via, Link: h.Link})
	}
	writeJSON(w, http.StatusOK, map[string][]similarJSON{"items": items})
}

func (s *server) labels(w http.ResponseWriter, _ *http.Request) {
	labels, err := s.st.Labels(s.user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string][]string{"labels": nonNil(labels)})
}
