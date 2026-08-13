package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
)

// The per-task JSON API. These endpoints are the remote CLI's backend and
// give agents the same granularity the local store offers: stable #n
// addressing, comments, and links. The markdown board GET/PUT stays for the
// SPA and bulk sync; the wire format itself remains id- and comment-free.

// apiCheck is a checklist item on the task wire.
type apiCheck struct {
	Text string `json:"text"`
	Done bool   `json:"done"`
}

// apiTask is the task shape all /api/tasks endpoints speak. It matches the
// CLI's --json shape so remote and local output are interchangeable.
type apiTask struct {
	ID        string     `json:"id"`
	Seq       int        `json:"seq,omitempty"`
	Emoji     string     `json:"emoji,omitempty"`
	Title     string     `json:"title"`
	Desc      string     `json:"desc,omitempty"`
	Status    string     `json:"status"`
	Blocked   bool       `json:"blocked,omitempty"`
	Prio      int        `json:"prio"`
	Due       string     `json:"due,omitempty"`
	Effort    string     `json:"effort,omitempty"`
	Tags      []string   `json:"tags,omitempty"`
	Checks    []apiCheck `json:"checks,omitempty"`
	Position  int        `json:"position"`
	CreatedAt string     `json:"createdAt,omitempty"`
	MovedAt   string     `json:"movedAt,omitempty"`
}

// apiComment is the comment shape of the comment endpoints.
type apiComment struct {
	ID        int    `json:"id"`
	TaskSeq   int    `json:"task,omitempty"`
	TaskID    string `json:"taskId"`
	Author    string `json:"author"`
	Body      string `json:"body"`
	CreatedAt string `json:"createdAt"`
}

func toAPITask(t board.Task) apiTask {
	out := apiTask{
		ID: t.ID, Seq: t.Seq, Emoji: t.Emoji, Title: t.Title, Desc: t.Desc,
		Status: string(t.Status), Blocked: t.Blocked, Prio: t.Prio, Due: t.Due,
		Effort: t.Effort, Tags: t.Tags, Position: t.Position,
	}
	for _, c := range t.Checks {
		out.Checks = append(out.Checks, apiCheck{Text: c.Text, Done: c.Done})
	}
	if !t.CreatedAt.IsZero() {
		out.CreatedAt = t.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	if !t.MovedAt.IsZero() {
		out.MovedAt = t.MovedAt.UTC().Format(time.RFC3339Nano)
	}
	return out
}

func toAPIComment(c store.Comment) apiComment {
	return apiComment{
		ID: c.ID, TaskSeq: c.TaskSeq, TaskID: c.TaskID,
		Author: c.Author, Body: c.Body,
		CreatedAt: c.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
}

// taskAPIError maps store errors onto HTTP statuses: unknown refs are 404,
// ambiguous or invalid input is 400, guard refusals are 409, everything
// else is a logged 500.
func taskAPIError(w http.ResponseWriter, user string, err error) {
	var blocked *store.CompletionBlockedError
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.Error(w, "task not found", http.StatusNotFound)
	case errors.Is(err, store.ErrAmbiguous):
		http.Error(w, "ambiguous task id prefix", http.StatusBadRequest)
	case errors.As(err, &blocked):
		http.Error(w, err.Error(), http.StatusConflict)
	case isBadTaskInput(err):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		log.Printf("tasks api for %s: %v", logSafe(user), err)
		http.Error(w, storageErrorMessage, http.StatusInternalServerError)
	}
}

// isBadTaskInput classifies validation refusals the store raises for caller
// mistakes (field validation, link rules, empty comment bodies).
func isBadTaskInput(err error) bool {
	msg := err.Error()
	for _, marker := range []string{
		"invalid", "must not be empty", "cannot block itself",
		"already blocks", "would create a cycle", "too long", "contains",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// decodeBody decodes a JSON request body into v with the shared size cap.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	body := http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(body).Decode(v); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return false
	}
	return true
}

// handleListTasks serves GET /api/tasks?status=&search=&tag=. Free text
// matches title, description, and tags; repeated tag params AND together.
func (s *server) handleListTasks(w http.ResponseWriter, r *http.Request, user string) {
	params := r.URL.Query()
	status := board.Status(strings.TrimSpace(params.Get("status")))
	if status != "" && !status.Valid() {
		http.Error(w, "invalid status filter", http.StatusBadRequest)
		return
	}
	filter := store.TaskFilter{
		Status: status,
		Search: strings.TrimSpace(params.Get("search")),
		Tags:   params["tag"],
	}
	tasks, err := s.store.FilterTasks(user, filter)
	if err != nil {
		taskAPIError(w, user, err)
		return
	}
	out := make([]apiTask, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, toAPITask(t))
	}
	writeJSON(w, out)
}

// taskWriteRequest is the body of POST /api/tasks and PATCH /api/tasks/{ref}.
// On PATCH only provided fields change; status moves the task, index places it
// within the destination column, and force bypasses the completion guard.
// Create ignores index: a new task always lands at the end of its column.
type taskWriteRequest struct {
	Title   *string     `json:"title"`
	Desc    *string     `json:"desc"`
	Emoji   *string     `json:"emoji"`
	Status  *string     `json:"status"`
	Index   *int        `json:"index"`
	Prio    *int        `json:"prio"`
	Due     *string     `json:"due"`
	Effort  *string     `json:"effort"`
	Blocked *bool       `json:"blocked"`
	Tags    *[]string   `json:"tags"`
	Checks  *[]apiCheck `json:"checks"`
	Force   bool        `json:"force,omitempty"`
}

// handleCreateTask serves POST /api/tasks.
func (s *server) handleCreateTask(w http.ResponseWriter, r *http.Request, user string) {
	var req taskWriteRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Title == nil || strings.TrimSpace(*req.Title) == "" {
		http.Error(w, "title must not be empty", http.StatusBadRequest)
		return
	}
	t := board.Task{Title: *req.Title}
	if req.Desc != nil {
		t.Desc = *req.Desc
	}
	if req.Emoji != nil {
		t.Emoji = *req.Emoji
	}
	if req.Status != nil {
		t.Status = board.Status(*req.Status)
	}
	if req.Prio != nil {
		t.Prio = *req.Prio
	}
	if req.Due != nil {
		t.Due = *req.Due
	}
	if req.Effort != nil {
		t.Effort = *req.Effort
	}
	if req.Blocked != nil {
		t.Blocked = *req.Blocked
	}
	if req.Tags != nil {
		t.Tags = *req.Tags
	}
	if req.Checks != nil {
		for _, c := range *req.Checks {
			t.Checks = append(t.Checks, board.Check{Text: c.Text, Done: c.Done})
		}
	}
	if t.Status != "" && !t.Status.Valid() {
		http.Error(w, "invalid status", http.StatusBadRequest)
		return
	}
	added, err := s.store.AddTask(user, t)
	if err != nil {
		taskAPIError(w, user, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, toAPITask(added))
}

// taskDetail is the GET /api/tasks/{ref} response: the task plus its
// comments and links (as stable numbers).
type taskDetail struct {
	apiTask
	Blocks    []int        `json:"blocks,omitempty"`
	BlockedBy []int        `json:"blockedBy,omitempty"`
	Comments  []apiComment `json:"comments,omitempty"`
}

// handleGetTask serves GET /api/tasks/{ref}.
func (s *server) handleGetTask(w http.ResponseWriter, r *http.Request, user string) {
	t, err := s.store.Task(user, r.PathValue("ref"))
	if err != nil {
		taskAPIError(w, user, err)
		return
	}
	comments, err := s.store.Comments(user, t.ID)
	if err != nil {
		taskAPIError(w, user, err)
		return
	}
	links, err := s.store.TaskLinks(user, t.ID)
	if err != nil {
		taskAPIError(w, user, err)
		return
	}
	out := taskDetail{apiTask: toAPITask(t)}
	for _, lt := range links.Blocks {
		out.Blocks = append(out.Blocks, lt.Seq)
	}
	for _, lt := range links.BlockedBy {
		out.BlockedBy = append(out.BlockedBy, lt.Seq)
	}
	for _, c := range comments {
		out.Comments = append(out.Comments, toAPIComment(c))
	}
	writeJSON(w, out)
}

// handlePatchTask serves PATCH /api/tasks/{ref}: a field patch plus an
// optional status move and column position, one transaction, guarded like the
// CLI unless force. An index on its own is a reorder, so it counts as a
// non-empty patch.
func (s *server) handlePatchTask(w http.ResponseWriter, r *http.Request, user string) {
	var req taskWriteRequest
	if !decodeBody(w, r, &req) {
		return
	}
	patch := store.TaskPatch{
		Title: req.Title, Desc: req.Desc, Emoji: req.Emoji,
		Prio: req.Prio, Due: req.Due, Effort: req.Effort, Blocked: req.Blocked,
	}
	if req.Title != nil && strings.TrimSpace(*req.Title) == "" {
		http.Error(w, "title must not be empty", http.StatusBadRequest)
		return
	}
	if req.Tags != nil {
		tags := append([]string(nil), (*req.Tags)...)
		patch.Tags = &tags
	}
	if req.Checks != nil {
		checks := make([]board.Check, 0, len(*req.Checks))
		for _, c := range *req.Checks {
			checks = append(checks, board.Check{Text: c.Text, Done: c.Done})
		}
		patch.Checks = &checks
	}
	var moveTo *board.Status
	if req.Status != nil {
		st := board.Status(strings.ToLower(strings.TrimSpace(*req.Status)))
		if !st.Valid() {
			http.Error(w, "invalid status", http.StatusBadRequest)
			return
		}
		moveTo = &st
	}
	if patch == (store.TaskPatch{}) && moveTo == nil && req.Index == nil {
		http.Error(w, "patch needs at least one field", http.StatusBadRequest)
		return
	}
	var guard func(board.Task) error
	if !req.Force && moveTo != nil && *moveTo == board.StatusDone {
		guard = func(t board.Task) error {
			if warn := store.CompletionWarning(t); warn != "" {
				return store.NewCompletionBlockedError(warn, taskRefLabel(t), t.Title)
			}
			return nil
		}
	}
	t, err := s.store.UpdateAndMoveTask(user, r.PathValue("ref"), patch, moveTo, req.Index, guard)
	if err != nil {
		taskAPIError(w, user, err)
		return
	}
	writeJSON(w, toAPITask(t))
}

// handleDeleteTask serves DELETE /api/tasks/{ref}: the hard delete.
func (s *server) handleDeleteTask(w http.ResponseWriter, r *http.Request, user string) {
	t, err := s.store.DeleteTask(user, r.PathValue("ref"))
	if err != nil {
		taskAPIError(w, user, err)
		return
	}
	writeJSON(w, toAPITask(t))
}

// handleTaskComments serves GET and POST /api/tasks/{ref}/comments.
func (s *server) handleListTaskComments(w http.ResponseWriter, r *http.Request, user string) {
	comments, err := s.store.Comments(user, r.PathValue("ref"))
	if err != nil {
		taskAPIError(w, user, err)
		return
	}
	out := make([]apiComment, 0, len(comments))
	for _, c := range comments {
		out = append(out, toAPIComment(c))
	}
	writeJSON(w, out)
}

func (s *server) handleAddTaskComment(w http.ResponseWriter, r *http.Request, user string) {
	var req struct {
		Body string `json:"body"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	c, err := s.store.AddComment(user, r.PathValue("ref"), user, req.Body)
	if err != nil {
		taskAPIError(w, user, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, toAPIComment(c))
}

// handleDeleteComment serves DELETE /api/comments/{id}.
func (s *server) handleDeleteComment(w http.ResponseWriter, r *http.Request, user string) {
	id, err := strconv.Atoi(strings.TrimPrefix(r.PathValue("id"), "c"))
	if err != nil || id < 1 {
		http.Error(w, "invalid comment id", http.StatusBadRequest)
		return
	}
	c, err := s.store.DeleteComment(user, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "comment not found", http.StatusNotFound)
			return
		}
		taskAPIError(w, user, err)
		return
	}
	writeJSON(w, toAPIComment(c))
}

// handleCreateLink serves POST /api/links {blocker, blocked}.
func (s *server) handleCreateLink(w http.ResponseWriter, r *http.Request, user string) {
	var req struct {
		Blocker string `json:"blocker"`
		Blocked string `json:"blocked"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	blocker, blocked, err := s.store.Link(user, req.Blocker, req.Blocked)
	if err != nil {
		taskAPIError(w, user, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, struct {
		Blocker apiTask `json:"blocker"`
		Blocked apiTask `json:"blocked"`
	}{toAPITask(blocker), toAPITask(blocked)})
}

// handleDeleteLink serves DELETE /api/links?a=&b=.
func (s *server) handleDeleteLink(w http.ResponseWriter, r *http.Request, user string) {
	a, b := r.URL.Query().Get("a"), r.URL.Query().Get("b")
	if a == "" || b == "" {
		http.Error(w, "a and b query parameters are required", http.StatusBadRequest)
		return
	}
	if err := s.store.Unlink(user, a, b); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "link not found", http.StatusNotFound)
			return
		}
		taskAPIError(w, user, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// taskRefLabel names a task for guard messages: #n when numbered.
func taskRefLabel(t board.Task) string {
	if t.Seq > 0 {
		return fmt.Sprintf("#%d", t.Seq)
	}
	return t.ID
}
