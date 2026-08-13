package mcpserv

import (
	"context"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestCommentToolsRoundTrip(t *testing.T) {
	cs, st := connectWithStore(t)
	if _, err := st.AddTask("tester", board.Task{Title: "Discussable"}); err != nil {
		t.Fatal(err)
	}

	var added addCommentOutput
	callOK(t, cs, "add_comment", map[string]any{"id": "#1", "body": "agent finding"}, &added)
	if added.Comment.ID != 1 || added.Comment.Task != 1 || added.Comment.Author != "tester" || added.Comment.Body != "agent finding" {
		t.Fatalf("add_comment = %+v", added.Comment)
	}

	var listed listCommentsOutput
	callOK(t, cs, "list_comments", map[string]any{"id": "1"}, &listed)
	if len(listed.Comments) != 1 || listed.Comments[0].ID != 1 {
		t.Fatalf("list_comments = %+v", listed.Comments)
	}

	// Errors surface as tool errors: missing task, empty body.
	for name, args := range map[string]map[string]any{
		"add_comment on missing task": {"id": "9", "body": "text"},
		"add_comment with empty body": {"id": "1", "body": "  "},
	} {
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "add_comment", Arguments: args})
		if err != nil || !res.IsError {
			t.Errorf("%s: err=%v isError=%v, want tool error", name, err, res != nil && res.IsError)
		}
	}
}
