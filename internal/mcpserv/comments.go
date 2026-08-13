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
