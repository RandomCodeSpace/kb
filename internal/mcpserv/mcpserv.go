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
	"strings"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
)

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
	err = newServer(st, user).Run(context.Background(), &mcp.StdioTransport{})
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
	st   *store.Store
	user string
}

// newServer builds the MCP server with all five task tools registered.
func newServer(st *store.Store, user string) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "kb", Title: "kb kanban board", Version: "1.0.0"}, nil)
	k := &kb{st: st, user: user}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_tasks",
		Description: "List kanban tasks on the board, ordered by column (todo, doing, done) then position. Optionally filter to a single column. Returns each task's id, title, status, prio, due, effort, tags, checks, and desc.",
	}, k.listTasks)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "add_task",
		Description: "Add a new task to the kanban board. Only title is required; status defaults to \"todo\" and prio to 3. Returns the created task including its id.",
	}, k.addTask)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "update_task",
		Description: "Update fields of an existing task identified by its id (a unique id prefix is accepted). Only the provided fields change; tags and checks replace the whole list when given. Returns the updated task.",
	}, k.updateTask)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "move_task",
		Description: "Move a task identified by its id (a unique id prefix is accepted) to another column: todo, doing, or done. The task is appended to the end of the target column. Returns the moved task.",
	}, k.moveTask)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "delete_task",
		Description: "Delete a task identified by its id (a unique id prefix is accepted). Returns the deleted task.",
	}, k.deleteTask)
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
	ID     string   `json:"id"`
	Emoji  string   `json:"emoji,omitempty"`
	Title  string   `json:"title"`
	Desc   string   `json:"desc,omitempty"`
	Status string   `json:"status"`
	Prio   int      `json:"prio"`
	Due    string   `json:"due,omitempty"`
	Effort string   `json:"effort,omitempty"`
	Tags   []string `json:"tags,omitempty"`
	Checks []check  `json:"checks,omitempty"`
}

func toTaskJSON(t board.Task) taskJSON {
	out := taskJSON{
		ID:     t.ID,
		Emoji:  t.Emoji,
		Title:  t.Title,
		Desc:   t.Desc,
		Status: string(t.Status),
		Prio:   t.Prio,
		Due:    t.Due,
		Effort: t.Effort,
		Tags:   t.Tags,
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

// --- tool inputs/outputs ---

type listTasksInput struct {
	Status string `json:"status,omitempty" jsonschema:"optional column filter: todo, doing, or done; omit for all columns"`
}

type listTasksOutput struct {
	Tasks []taskJSON `json:"tasks"`
}

type addTaskInput struct {
	Title  string   `json:"title" jsonschema:"task title (required, non-empty)"`
	Desc   string   `json:"desc,omitempty" jsonschema:"longer markdown description"`
	Status string   `json:"status,omitempty" jsonschema:"column: todo, doing, or done (default todo)"`
	Prio   int      `json:"prio,omitempty" jsonschema:"priority 1 (highest) to 4 (lowest); default 3"`
	Due    string   `json:"due,omitempty" jsonschema:"due date as YYYY-MM-DD"`
	Effort string   `json:"effort,omitempty" jsonschema:"effort estimate: S, M, or L"`
	Tags   []string `json:"tags,omitempty" jsonschema:"labels, plain (backend) or scoped (type::bug)"`
	Checks []check  `json:"checks,omitempty" jsonschema:"checklist items"`
	Emoji  string   `json:"emoji,omitempty" jsonschema:"single emoji shown on the card"`
}

type updateTaskInput struct {
	ID     string    `json:"id" jsonschema:"task id or unique id prefix"`
	Title  *string   `json:"title,omitempty" jsonschema:"new title"`
	Desc   *string   `json:"desc,omitempty" jsonschema:"new markdown description (empty string clears it)"`
	Status *string   `json:"status,omitempty" jsonschema:"new column: todo, doing, or done"`
	Prio   *int      `json:"prio,omitempty" jsonschema:"new priority 1 (highest) to 4 (lowest)"`
	Due    *string   `json:"due,omitempty" jsonschema:"new due date as YYYY-MM-DD (empty string clears it)"`
	Effort *string   `json:"effort,omitempty" jsonschema:"new effort estimate: S, M, or L (empty string clears it)"`
	Tags   *[]string `json:"tags,omitempty" jsonschema:"replacement label list"`
	Checks *[]check  `json:"checks,omitempty" jsonschema:"replacement checklist"`
	Emoji  *string   `json:"emoji,omitempty" jsonschema:"new emoji (empty string clears it)"`
}

type moveTaskInput struct {
	ID     string `json:"id" jsonschema:"task id or unique id prefix"`
	Status string `json:"status" jsonschema:"target column: todo, doing, or done"`
}

type deleteTaskInput struct {
	ID string `json:"id" jsonschema:"task id or unique id prefix"`
}

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

func (k *kb) addTask(_ context.Context, _ *mcp.CallToolRequest, in addTaskInput) (*mcp.CallToolResult, taskJSON, error) {
	if strings.TrimSpace(in.Title) == "" {
		return nil, taskJSON{}, errors.New("title must not be empty")
	}
	t := board.Task{
		Emoji:  in.Emoji,
		Title:  in.Title,
		Desc:   in.Desc,
		Prio:   in.Prio,
		Due:    in.Due,
		Effort: in.Effort,
		Tags:   in.Tags,
		Checks: toBoardChecks(in.Checks),
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
		Emoji:  in.Emoji,
		Title:  in.Title,
		Desc:   in.Desc,
		Due:    in.Due,
		Effort: in.Effort,
		Prio:   in.Prio,
		Tags:   in.Tags,
	}
	if in.Prio != nil && (*in.Prio < 1 || *in.Prio > 4) {
		return nil, taskJSON{}, fmt.Errorf("invalid prio %d: must be 1 (highest) to 4 (lowest)", *in.Prio)
	}
	if in.Checks != nil {
		bc := toBoardChecks(*in.Checks)
		patch.Checks = &bc
	}
	t, err := k.st.UpdateTask(k.user, in.ID, patch)
	if err != nil {
		return nil, taskJSON{}, k.idError(err, in.ID)
	}
	if in.Status != nil {
		st, err := parseStatus(*in.Status)
		if err != nil {
			return nil, taskJSON{}, err
		}
		if t, err = k.st.MoveTask(k.user, t.ID, st); err != nil {
			return nil, taskJSON{}, k.idError(err, in.ID)
		}
	}
	return nil, toTaskJSON(t), nil
}

func (k *kb) moveTask(_ context.Context, _ *mcp.CallToolRequest, in moveTaskInput) (*mcp.CallToolResult, taskJSON, error) {
	st, err := parseStatus(in.Status)
	if err != nil {
		return nil, taskJSON{}, err
	}
	t, err := k.st.MoveTask(k.user, in.ID, st)
	if err != nil {
		return nil, taskJSON{}, k.idError(err, in.ID)
	}
	return nil, toTaskJSON(t), nil
}

func (k *kb) deleteTask(_ context.Context, _ *mcp.CallToolRequest, in deleteTaskInput) (*mcp.CallToolResult, taskJSON, error) {
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
		return "", fmt.Errorf("invalid status %q: must be todo, doing, or done", s)
	}
	return st, nil
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
