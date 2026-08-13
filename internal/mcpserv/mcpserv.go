// Package mcpserv exposes a user's kb board as an MCP server named "kb"
// over stdio, so agent harnesses can list, add, update, move, and delete
// tasks against the local SQLite store.
package mcpserv

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
)

var serveMCP = func(srv *mcp.Server) error {
	return srv.Run(context.Background(), &mcp.StdioTransport{})
}

// Run opens (creating if needed) the store at <dataDir>/kb.db, imports any
// legacy markdown boards in dataDir, and serves the MCP tools for user over
// stdio until the client disconnects.
func Run(dataDir, user string) error {
	user, err := normalizeUser(user)
	if err != nil {
		return fmt.Errorf("mcpserv: %w", err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("mcpserv: create data dir: %w", err)
	}
	secret, err := store.LoadOrCreateSecret(dataDir)
	if err != nil {
		return err
	}
	st, err := store.Open(filepath.Join(dataDir, "kb.db"), secret)
	if err != nil {
		return err
	}
	defer st.Close()
	if _, err := st.ImportMarkdownDir(dataDir); err != nil {
		return err
	}
	err = serveMCP(newServer(st, user))
	if isClientDisconnect(err) {
		return nil
	}
	return err
}

// normalizeUser maps the --user/KB_USER value onto the same storage key the
// HTTP server and CLI use (trimmed, "default" when empty, then
// store.SanitizeUser: lowercase + charset check), so agent edits land on
// the board the other surfaces show instead of a phantom one.
func normalizeUser(user string) (string, error) {
	user = strings.TrimSpace(user)
	if user == "" {
		return "default", nil
	}
	return store.SanitizeUser(user)
}

// isClientDisconnect reports whether err is the normal end of an MCP stdio
// session: the client closed our stdin (io.EOF) or the SDK's jsonrpc layer
// reported "server is closing" (its unexported ErrServerClosing, wire code
// -32004). Harnesses end sessions this way, so it is not a failure.
func isClientDisconnect(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) {
		return true
	}
	var wire *jsonrpc.Error
	return errors.As(err, &wire) && wire.Code == -32004
}

// kb holds the per-process tool state: one store, one fixed user.
type kb struct {
	st              *store.Store
	user            string
	beforeDoneGuard func()
}

// newServer builds the MCP server with all ten board tools registered.
func newServer(st *store.Store, user string) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "kb", Title: "kb kanban board", Version: "1.0.0"}, nil)
	k := &kb{st: st, user: user}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_tasks",
		Description: "List kanban tasks on the board, ordered by column (todo, doing, done, cancelled) then position. Optionally filter to a single column. Cancelled tasks are soft-deleted ones; they are included unless a status filter excludes them. Returns each task's id, title, status, blocked, prio, due, effort, tags, checks, and desc.",
	}, k.listTasks)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "add_task",
		Description: "Add a new task to the kanban board. Only title is required; status defaults to \"todo\", prio to 3, and blocked to false. Returns the created task including its id.",
	}, k.addTask)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "update_task",
		Description: "Update fields of an existing task identified by its id (a unique id prefix is accepted). Only the provided fields change; tags and checks replace the whole list when given. Setting status to done fails, changing nothing at all, when checklist items are open or the task is blocked after this update is applied, unless force is true. Returns the updated task.",
	}, k.updateTask)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "move_task",
		Description: "Move a task identified by its id (a unique id prefix is accepted) to another column: todo, doing, done, or cancelled. The task is appended to the end of the target column. Moving to done fails with an error naming the open checklist items, or reporting the blocked flag, unless force is true. Returns the moved task.",
	}, k.moveTask)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "delete_task",
		Description: "Delete a task identified by its id (a unique id prefix is accepted). By default this is a soft delete: the task moves to the cancelled column and can be moved back. Pass soft: false to remove the row permanently, which cannot be undone. Returns the deleted task.",
	}, k.deleteTask)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "search_similar",
		Description: "Search existing cards and import history; check before creating a card to avoid duplicating existing work; returns cheap stubs, fetch details only if needed.",
	}, k.searchSimilar)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "duplicate_check",
		Description: "Check a proposed title, body, and exact provenance link; check before creating a card to avoid duplicating existing work; returns cheap stubs, fetch details only if needed.",
	}, k.duplicateCheck)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_task",
		Description: "Fetch one task in full — every field plus its comments — identified by its stable number (12 or #12), UUID, or unique UUID prefix.",
	}, k.getTask)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "add_comment",
		Description: "Append a comment to a task identified by its stable number (12 or #12), UUID, or unique UUID prefix. Comments are an append-only log with stable per-board ids (c1, c2, ...) that are never reused. Returns the created comment.",
	}, k.addComment)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_comments",
		Description: "List a task's comments oldest-first. The task is identified by its stable number (12 or #12), UUID, or unique UUID prefix. Returns each comment's id, author, body, and creation time.",
	}, k.listComments)
	return srv
}

// --- wire types ---

// check is a checklist item as seen on the wire, for both input and output.
type check struct {
	Text string `json:"text" jsonschema:"checklist item text"`
	Done bool   `json:"done,omitempty" jsonschema:"true when the item is checked off"`
}

// taskJSON is the task shape returned by every tool.
type taskJSON struct {
	ID      string   `json:"id"`
	Seq     int      `json:"seq,omitempty" jsonschema:"stable per-board task number, displayed #n"`
	Emoji   string   `json:"emoji,omitempty"`
	Title   string   `json:"title"`
	Desc    string   `json:"desc,omitempty"`
	Status  string   `json:"status"`
	Blocked bool     `json:"blocked,omitempty"`
	Prio    int      `json:"prio"`
	Due     string   `json:"due,omitempty"`
	Effort  string   `json:"effort,omitempty"`
	Tags    []string `json:"tags,omitempty"`
	Checks  []check  `json:"checks,omitempty"`
}

func toTaskJSON(t board.Task) taskJSON {
	out := taskJSON{
		ID:      t.ID,
		Seq:     t.Seq,
		Emoji:   t.Emoji,
		Title:   t.Title,
		Desc:    t.Desc,
		Status:  string(t.Status),
		Blocked: t.Blocked,
		Prio:    t.Prio,
		Due:     t.Due,
		Effort:  t.Effort,
		Tags:    t.Tags,
	}
	for _, c := range t.Checks {
		out.Checks = append(out.Checks, check{Text: c.Text, Done: c.Done})
	}
	return out
}

func toBoardChecks(cs []check) []board.Check {
	out := make([]board.Check, 0, len(cs))
	for _, c := range cs {
		out = append(out, board.Check{Text: c.Text, Done: c.Done})
	}
	return out
}

func toSimilarStub(hit store.SimilarHit) similarStub {
	return similarStub{
		ID:     hit.ID,
		Title:  hit.Title,
		Status: hit.Status,
		Via:    hit.Via,
		Link:   hit.Link,
	}
}

// --- tool inputs/outputs ---

type listTasksInput struct {
	Status string `json:"status,omitempty" jsonschema:"optional column filter: todo, doing, done, or cancelled; omit for all columns"`
}

type listTasksOutput struct {
	Tasks []taskJSON `json:"tasks"`
}

type addTaskInput struct {
	Title   string   `json:"title" jsonschema:"task title (required, non-empty)"`
	Desc    string   `json:"desc,omitempty" jsonschema:"longer markdown description"`
	Status  string   `json:"status,omitempty" jsonschema:"column: todo, doing, done, or cancelled (default todo)"`
	Blocked bool     `json:"blocked,omitempty" jsonschema:"true when the task is blocked by something else; default false"`
	Prio    int      `json:"prio,omitempty" jsonschema:"priority 1 (highest) to 4 (lowest); default 3"`
	Due     string   `json:"due,omitempty" jsonschema:"due date as YYYY-MM-DD"`
	Effort  string   `json:"effort,omitempty" jsonschema:"effort estimate: S, M, or L"`
	Tags    []string `json:"tags,omitempty" jsonschema:"labels, plain (backend) or scoped (type::bug)"`
	Checks  []check  `json:"checks,omitempty" jsonschema:"checklist items"`
	Emoji   string   `json:"emoji,omitempty" jsonschema:"single emoji shown on the card"`
}

type updateTaskInput struct {
	ID      string    `json:"id" jsonschema:"task id or unique id prefix"`
	Title   *string   `json:"title,omitempty" jsonschema:"new title"`
	Desc    *string   `json:"desc,omitempty" jsonschema:"new markdown description (empty string clears it)"`
	Status  *string   `json:"status,omitempty" jsonschema:"new column: todo, doing, done, or cancelled"`
	Blocked *bool     `json:"blocked,omitempty" jsonschema:"true to flag the task as blocked, false to clear the flag"`
	Prio    *int      `json:"prio,omitempty" jsonschema:"new priority 1 (highest) to 4 (lowest)"`
	Due     *string   `json:"due,omitempty" jsonschema:"new due date as YYYY-MM-DD (empty string clears it)"`
	Effort  *string   `json:"effort,omitempty" jsonschema:"new effort estimate: S, M, or L (empty string clears it)"`
	Tags    *[]string `json:"tags,omitempty" jsonschema:"replacement label list"`
	Checks  *[]check  `json:"checks,omitempty" jsonschema:"replacement checklist"`
	Emoji   *string   `json:"emoji,omitempty" jsonschema:"new emoji (empty string clears it)"`
	Force   bool      `json:"force,omitempty" jsonschema:"set true to move to done even when checklist items are open or the task is blocked"`
}

type moveTaskInput struct {
	ID     string `json:"id" jsonschema:"task id or unique id prefix"`
	Status string `json:"status" jsonschema:"target column: todo, doing, done, or cancelled"`
	Force  bool   `json:"force,omitempty" jsonschema:"set true to move to done even when checklist items are open or the task is blocked"`
}

type deleteTaskInput struct {
	ID   string `json:"id" jsonschema:"task id or unique id prefix"`
	Soft *bool  `json:"soft,omitempty" jsonschema:"true (the default) moves the task to the cancelled column; false deletes the row permanently and cannot be undone"`
}

type searchSimilarInput struct {
	Query string `json:"query" jsonschema:"free text matched against card titles, descriptions, tags and import history"`
	Limit int    `json:"limit,omitempty" jsonschema:"max stubs to return; default 3, max 10"`
}

type similarStub struct {
	ID     string `json:"id,omitempty"`
	Title  string `json:"title,omitempty"`
	Status string `json:"status,omitempty"`
	Via    string `json:"via,omitempty"`
	Link   string `json:"link,omitempty"`
}

type searchSimilarOutput struct {
	Items []similarStub `json:"items"`
}

type duplicateCheckInput struct {
	Title string `json:"title" jsonschema:"proposed card title"`
	Body  string `json:"body,omitempty"`
	Link  string `json:"link,omitempty" jsonschema:"provenance tag e.g. link::gitlab#123, checked exactly first"`
}

type duplicateCheckOutput struct {
	Candidates []similarStub `json:"candidates"`
}

const duplicateCheckMaxCandidates = 10

// --- handlers ---

func (k *kb) listTasks(_ context.Context, _ *mcp.CallToolRequest, in listTasksInput) (*mcp.CallToolResult, listTasksOutput, error) {
	var status board.Status
	if in.Status != "" {
		st, err := parseStatus(in.Status)
		if err != nil {
			return nil, listTasksOutput{}, err
		}
		status = st
	}
	tasks, err := k.st.ListTasks(k.user, status)
	if err != nil {
		return nil, listTasksOutput{}, err
	}
	out := listTasksOutput{Tasks: make([]taskJSON, 0, len(tasks))}
	for _, t := range tasks {
		out.Tasks = append(out.Tasks, toTaskJSON(t))
	}
	return nil, out, nil
}

func (k *kb) searchSimilar(_ context.Context, _ *mcp.CallToolRequest, in searchSimilarInput) (*mcp.CallToolResult, searchSimilarOutput, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = 3
	}
	if limit > 10 {
		limit = 10
	}
	hits, err := k.st.SearchSimilar(k.user, in.Query, "", nil, limit)
	if err != nil {
		return nil, searchSimilarOutput{}, err
	}
	out := searchSimilarOutput{Items: make([]similarStub, 0, len(hits))}
	for _, hit := range hits {
		out.Items = append(out.Items, toSimilarStub(hit))
	}
	return nil, out, nil
}

func (k *kb) duplicateCheck(_ context.Context, _ *mcp.CallToolRequest, in duplicateCheckInput) (*mcp.CallToolResult, duplicateCheckOutput, error) {
	out := duplicateCheckOutput{Candidates: make([]similarStub, 0)}
	seenIDs := make(map[string]struct{})

	// Exact provenance is stronger evidence, so its cards stay ahead of text
	// matches and win when the same task appears through both paths.
	if in.Link != "" {
		hits, err := k.st.TasksByLink(k.user, in.Link)
		if err != nil {
			return nil, duplicateCheckOutput{}, err
		}
		out.Candidates = appendDuplicateCandidates(out.Candidates, hits, seenIDs)
	}
	if remaining := duplicateCheckMaxCandidates - len(out.Candidates); remaining > 0 {
		similarLimit := remaining
		if similarLimit > 3 {
			similarLimit = 3
		}
		query := strings.TrimSpace(in.Title + " " + in.Body)
		hits, err := k.st.SearchSimilar(k.user, query, "", nil, similarLimit)
		if err != nil {
			return nil, duplicateCheckOutput{}, err
		}
		out.Candidates = appendDuplicateCandidates(out.Candidates, hits, seenIDs)
	}
	return nil, out, nil
}

func appendDuplicateCandidates(candidates []similarStub, hits []store.SimilarHit, seenIDs map[string]struct{}) []similarStub {
	for _, hit := range hits {
		if len(candidates) >= duplicateCheckMaxCandidates {
			return candidates
		}
		if hit.ID != "" {
			if _, seen := seenIDs[hit.ID]; seen {
				continue
			}
			seenIDs[hit.ID] = struct{}{}
		}
		candidates = append(candidates, toSimilarStub(hit))
	}
	return candidates
}

func (k *kb) addTask(_ context.Context, _ *mcp.CallToolRequest, in addTaskInput) (*mcp.CallToolResult, taskJSON, error) {
	if strings.TrimSpace(in.Title) == "" {
		return nil, taskJSON{}, errors.New("title must not be empty")
	}
	t := board.Task{
		Emoji:   in.Emoji,
		Title:   in.Title,
		Desc:    in.Desc,
		Blocked: in.Blocked,
		Prio:    in.Prio,
		Due:     in.Due,
		Effort:  in.Effort,
		Tags:    in.Tags,
		Checks:  toBoardChecks(in.Checks),
	}
	if in.Status != "" {
		st, err := parseStatus(in.Status)
		if err != nil {
			return nil, taskJSON{}, err
		}
		t.Status = st
	}
	if in.Prio != 0 && (in.Prio < 1 || in.Prio > 4) {
		return nil, taskJSON{}, fmt.Errorf("invalid prio %d: must be 1 (highest) to 4 (lowest)", in.Prio)
	}
	created, err := k.st.AddTask(k.user, t)
	if err != nil {
		return nil, taskJSON{}, err
	}
	return nil, toTaskJSON(created), nil
}

func (k *kb) updateTask(_ context.Context, _ *mcp.CallToolRequest, in updateTaskInput) (*mcp.CallToolResult, taskJSON, error) {
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
	if in.Prio != nil && (*in.Prio < 1 || *in.Prio > 4) {
		return nil, taskJSON{}, fmt.Errorf("invalid prio %d: must be 1 (highest) to 4 (lowest)", *in.Prio)
	}
	if in.Checks != nil {
		bc := toBoardChecks(*in.Checks)
		patch.Checks = &bc
	}
	// Status is parsed before anything is written: a bad status now rejects
	// the whole call instead of persisting the field patch and then failing.
	var moveTo *board.Status
	if in.Status != nil {
		st, err := parseStatus(*in.Status)
		if err != nil {
			return nil, taskJSON{}, err
		}
		moveTo = &st
	}
	// The same done guard move_task applies, evaluated on the post-patch task
	// so that checking off the last item and moving to done in one call
	// succeeds. A refusal rolls the patch back with the move.
	var guard func(board.Task) error
	if moveTo != nil && *moveTo == board.StatusDone && !in.Force {
		guard = func(t board.Task) error {
			if warn := doneWarning(t); warn != "" {
				return fmt.Errorf("refusing to finish %q: %s — resolve it first, or call update_task again with force: true", t.Title, warn)
			}
			return nil
		}
	}
	t, err := k.st.UpdateAndMoveTask(k.user, in.ID, patch, moveTo, guard)
	if err != nil {
		return nil, taskJSON{}, k.idError(err, in.ID)
	}
	return nil, toTaskJSON(t), nil
}

func (k *kb) moveTask(_ context.Context, _ *mcp.CallToolRequest, in moveTaskInput) (*mcp.CallToolResult, taskJSON, error) {
	st, err := parseStatus(in.Status)
	if err != nil {
		return nil, taskJSON{}, err
	}
	var guard func(board.Task) error
	if st == board.StatusDone && !in.Force {
		guard = func(t board.Task) error {
			if k.beforeDoneGuard != nil {
				k.beforeDoneGuard()
			}
			if warn := doneWarning(t); warn != "" {
				return fmt.Errorf("refusing to finish %q: %s — resolve it first, or call move_task again with force: true", t.Title, warn)
			}
			return nil
		}
	}
	t, err := k.st.UpdateAndMoveTask(k.user, in.ID, store.TaskPatch{}, &st, guard)
	if err != nil {
		return nil, taskJSON{}, k.idError(err, in.ID)
	}
	return nil, toTaskJSON(t), nil
}

// deleteTask soft-deletes by default: the task moves to the cancelled column
// so an agent cannot destroy work by reflex. soft: false is the only path to
// a row delete.
func (k *kb) deleteTask(_ context.Context, _ *mcp.CallToolRequest, in deleteTaskInput) (*mcp.CallToolResult, taskJSON, error) {
	if in.Soft == nil || *in.Soft {
		t, err := k.st.MoveTask(k.user, in.ID, board.StatusCancelled)
		if err != nil {
			return nil, taskJSON{}, k.idError(err, in.ID)
		}
		return nil, toTaskJSON(t), nil
	}
	t, err := k.st.DeleteTask(k.user, in.ID)
	if err != nil {
		return nil, taskJSON{}, k.idError(err, in.ID)
	}
	return nil, toTaskJSON(t), nil
}

// --- helpers ---

// parseStatus validates a wire status string.
func parseStatus(s string) (board.Status, error) {
	st := board.Status(s)
	if !st.Valid() {
		return "", fmt.Errorf("invalid status %q: must be todo, doing, done, or cancelled", s)
	}
	return st, nil
}

// doneWarning names what makes finishing t questionable — the open checklist
// items, a blocked flag, or both — and returns "" when the task is clear to
// ship.
func doneWarning(t board.Task) string {
	var open []string
	for _, c := range t.Checks {
		if !c.Done {
			open = append(open, strconv.Quote(c.Text))
		}
	}
	var parts []string
	if len(open) > 0 {
		parts = append(parts, fmt.Sprintf("%d of %d checklist items are still open (%s)",
			len(open), len(t.Checks), strings.Join(open, ", ")))
	}
	if t.Blocked {
		parts = append(parts, "the task is flagged blocked")
	}
	return strings.Join(parts, "; ")
}

// findTask resolves an id prefix to the task it names with the store's rules
// (an exact id wins, otherwise the prefix must match exactly one task),
// without mutating anything.
func (k *kb) findTask(prefix string) (board.Task, error) {
	if prefix == "" {
		return board.Task{}, k.idError(store.ErrNotFound, prefix)
	}
	tasks, err := k.st.ListTasks(k.user, "")
	if err != nil {
		return board.Task{}, err
	}
	var match board.Task
	n := 0
	for _, t := range tasks {
		if t.ID == prefix {
			return t, nil
		}
		if strings.HasPrefix(t.ID, prefix) {
			match, n = t, n+1
		}
	}
	switch n {
	case 0:
		return board.Task{}, k.idError(store.ErrNotFound, prefix)
	case 1:
		return match, nil
	}
	return board.Task{}, k.idError(store.ErrAmbiguous, prefix)
}

// idError rewrites store ID-resolution sentinels into actionable tool errors;
// for an ambiguous prefix it lists the candidate tasks.
func (k *kb) idError(err error, prefix string) error {
	switch {
	case errors.Is(err, store.ErrAmbiguous):
		if tasks, lerr := k.st.ListTasks(k.user, ""); lerr == nil {
			var cands []string
			for _, t := range tasks {
				if strings.HasPrefix(t.ID, prefix) {
					cands = append(cands, fmt.Sprintf("%s (%s)", t.ID, t.Title))
				}
			}
			return fmt.Errorf("task id prefix %q is ambiguous, matching: %s — retry with a longer prefix", prefix, strings.Join(cands, "; "))
		}
		return fmt.Errorf("task id prefix %q is ambiguous — retry with a longer prefix", prefix)
	case errors.Is(err, store.ErrNotFound):
		return fmt.Errorf("no task matches id %q — call list_tasks to see current task ids", prefix)
	}
	return err
}
