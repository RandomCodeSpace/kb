package store

import (
	"fmt"
	"strings"

	"github.com/RandomCodeSpace/kb/internal/board"
)

// CompletionBlockedError is the typed refusal for finishing a task that
// still has open work: open checklist items, a blocked flag, or open
// blockers. Callers that force the move never see it; the HTTP API maps it
// to 409 and the CLI prints it verbatim.
type CompletionBlockedError struct{ msg string }

func (e *CompletionBlockedError) Error() string { return e.msg }

// NewCompletionBlockedError builds the refusal with the standard suffix.
func NewCompletionBlockedError(reason, ref, title string) *CompletionBlockedError {
	return &CompletionBlockedError{msg: fmt.Sprintf("%s on %s %q; re-run with --force to finish it anyway", reason, ref, title)}
}

// CompletionWarning explains why finishing t deserves a second look — open
// checklist items, a blocked flag, or both — and returns "" when the task
// is clear to ship. Shared by the CLI, MCP, and HTTP surfaces so the
// refusal reads identically everywhere.
func CompletionWarning(t board.Task) string {
	open := 0
	for _, c := range t.Checks {
		if !c.Done {
			open++
		}
	}
	var parts []string
	if open > 0 {
		parts = append(parts, fmt.Sprintf("%d of %d checklist items are still open", open, len(t.Checks)))
	}
	if t.Blocked {
		parts = append(parts, "the task is flagged blocked")
	}
	return strings.Join(parts, " and ")
}
