package mcpserv

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/RandomCodeSpace/kb/internal/store"
)

// commentJSON is the comment shape returned by the comment tools.
type commentJSON struct {
	ID        int    `json:"id" jsonschema:"stable per-board comment id, displayed c<n>, never reused"`
	Task      int    `json:"task,omitempty" jsonschema:"owning task's stable #n number"`
	TaskID    string `json:"taskId" jsonschema:"owning task's UUID"`
	Author    string `json:"author"`
	Body      string `json:"body"`
	CreatedAt string `json:"createdAt"`
}

func toCommentJSON(c store.Comment) commentJSON {
	return commentJSON{
		ID: c.ID, Task: c.TaskSeq, TaskID: c.TaskID,
		Author: c.Author, Body: c.Body,
		CreatedAt: c.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
}

type addCommentInput struct {
	ID   string `json:"id" jsonschema:"task to comment on: stable number (12 or #12), UUID, or unique UUID prefix"`
	Body string `json:"body" jsonschema:"comment text (markdown)"`
}

type addCommentOutput struct {
	Comment commentJSON `json:"comment"`
}

type listCommentsInput struct {
	ID string `json:"id" jsonschema:"task whose comments to list: stable number (12 or #12), UUID, or unique UUID prefix"`
}

type listCommentsOutput struct {
	Comments []commentJSON `json:"comments"`
}

type getTaskInput struct {
	ID string `json:"id" jsonschema:"task to fetch: stable number (12 or #12), UUID, or unique UUID prefix"`
}

type getTaskOutput struct {
	Task      taskJSON      `json:"task"`
	Blocks    []int         `json:"blocks,omitempty" jsonschema:"stable numbers of tasks this task blocks"`
	BlockedBy []int         `json:"blockedBy,omitempty" jsonschema:"stable numbers of tasks blocking this task"`
	Comments  []commentJSON `json:"comments,omitempty"`
}

func (k *kb) getTask(_ context.Context, _ *mcp.CallToolRequest, in getTaskInput) (*mcp.CallToolResult, getTaskOutput, error) {
	t, err := k.st.Task(k.user, in.ID)
	if err != nil {
		return nil, getTaskOutput{}, err
	}
	comments, err := k.st.Comments(k.user, t.ID)
	if err != nil {
		return nil, getTaskOutput{}, err
	}
	links, err := k.st.TaskLinks(k.user, t.ID)
	if err != nil {
		return nil, getTaskOutput{}, err
	}
	out := getTaskOutput{Task: toTaskJSON(t)}
	for _, lt := range links.Blocks {
		out.Blocks = append(out.Blocks, lt.Seq)
	}
	for _, lt := range links.BlockedBy {
		out.BlockedBy = append(out.BlockedBy, lt.Seq)
	}
	for _, c := range comments {
		out.Comments = append(out.Comments, toCommentJSON(c))
	}
	return nil, out, nil
}

type linkTasksInput struct {
	Blocker string `json:"blocker" jsonschema:"task doing the blocking: stable number (12 or #12), UUID, or unique prefix"`
	Blocked string `json:"blocked" jsonschema:"task being blocked: stable number (12 or #12), UUID, or unique prefix"`
}

type linkTasksOutput struct {
	Blocker taskJSON `json:"blocker"`
	Blocked taskJSON `json:"blocked"`
}

func (k *kb) linkTasks(_ context.Context, _ *mcp.CallToolRequest, in linkTasksInput) (*mcp.CallToolResult, linkTasksOutput, error) {
	blocker, blocked, err := k.st.Link(k.user, in.Blocker, in.Blocked)
	if err != nil {
		return nil, linkTasksOutput{}, err
	}
	return nil, linkTasksOutput{Blocker: toTaskJSON(blocker), Blocked: toTaskJSON(blocked)}, nil
}

type unlinkTasksInput struct {
	A string `json:"a" jsonschema:"one linked task: stable number, UUID, or unique prefix"`
	B string `json:"b" jsonschema:"the other linked task: stable number, UUID, or unique prefix"`
}

type unlinkTasksOutput struct {
	Removed bool `json:"removed"`
}

func (k *kb) unlinkTasks(_ context.Context, _ *mcp.CallToolRequest, in unlinkTasksInput) (*mcp.CallToolResult, unlinkTasksOutput, error) {
	if err := k.st.Unlink(k.user, in.A, in.B); err != nil {
		return nil, unlinkTasksOutput{}, err
	}
	return nil, unlinkTasksOutput{Removed: true}, nil
}

func (k *kb) addComment(_ context.Context, _ *mcp.CallToolRequest, in addCommentInput) (*mcp.CallToolResult, addCommentOutput, error) {
	c, err := k.st.AddComment(k.user, in.ID, k.user, in.Body)
	if err != nil {
		return nil, addCommentOutput{}, err
	}
	return nil, addCommentOutput{Comment: toCommentJSON(c)}, nil
}

func (k *kb) listComments(_ context.Context, _ *mcp.CallToolRequest, in listCommentsInput) (*mcp.CallToolResult, listCommentsOutput, error) {
	comments, err := k.st.Comments(k.user, in.ID)
	if err != nil {
		return nil, listCommentsOutput{}, err
	}
	out := listCommentsOutput{Comments: make([]commentJSON, 0, len(comments))}
	for _, c := range comments {
		out.Comments = append(out.Comments, toCommentJSON(c))
	}
	return nil, out, nil
}
