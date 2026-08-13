package cliapp

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/RandomCodeSpace/kb/internal/store"
)

// viewJSON is the --json shape of kb view: the task plus its comments.
type viewJSON struct {
	taskJSON
	Comments []commentJSON `json:"comments,omitempty"`
}

// cmdView shows one task in full, with its comments inline. Task fields work
// in both local and remote mode; comments are local-only until the server
// API grows comment endpoints.
func (a *app) cmdView(args []string) int {
	fs, user, data := a.newFlagSet("view")
	jsonF := fs.Bool("json", false, "print the task and its comments as JSON")
	pos, err := parseInterleaved(fs, args)
	if code, done := a.parseResult(err); done {
		return code
	}
	if len(pos) != 1 {
		return a.usageErr(errors.New("view needs exactly one <id> argument"))
	}
	ref := pos[0]

	if strings.TrimSpace(os.Getenv("KB_SERVER")) != "" {
		return a.withBackend(*user, *data, func(be backend) error {
			items, err := be.list("")
			if err != nil {
				return err
			}
			it, err := findItem(items, ref)
			if err != nil {
				return err
			}
			return a.renderView(it, nil, false, *jsonF)
		})
	}

	return a.withLocalStore(*user, *data, func(st *store.Store, u string) error {
		t, err := st.Task(u, ref)
		if err != nil {
			return friendlyIDErr(err, ref)
		}
		comments, err := st.Comments(u, t.ID)
		if err != nil {
			return err
		}
		return a.renderView(item{ref: t.ID, task: t}, comments, true, *jsonF)
	})
}

func (a *app) renderView(it item, comments []store.Comment, commentsAvailable, asJSON bool) error {
	if asJSON {
		out := viewJSON{taskJSON: itemJSON(it)}
		for _, c := range comments {
			out.Comments = append(out.Comments, commentToJSON(c))
		}
		return writeSingleJSON(a.stdout, out)
	}
	writeTaskView(a.stdout, it, comments, commentsAvailable)
	return nil
}

// writeTaskView renders the human form of one task.
func writeTaskView(w io.Writer, it item, comments []store.Comment, commentsAvailable bool) {
	t := it.task
	title := t.Title
	if t.Emoji != "" {
		title = t.Emoji + " " + title
	}
	fmt.Fprintf(w, "%s %s\n", displayID(it), title)
	blocked := "no"
	if t.Blocked {
		blocked = "yes"
	}
	fmt.Fprintf(w, "status: %s   prio: %d   blocked: %s\n", t.Status, t.Prio, blocked)
	if t.Due != "" || t.Effort != "" {
		fmt.Fprintf(w, "due: %s   effort: %s\n", orDash(t.Due), orDash(t.Effort))
	}
	if len(t.Tags) > 0 {
		fmt.Fprintf(w, "tags: %s\n", strings.Join(t.Tags, ", "))
	}
	if t.ID != it.ref || len(t.ID) > 8 {
		fmt.Fprintf(w, "id: %s\n", t.ID)
	}
	if !t.CreatedAt.IsZero() {
		fmt.Fprintf(w, "created: %s   moved: %s\n",
			t.CreatedAt.UTC().Format(time.RFC3339), t.MovedAt.UTC().Format(time.RFC3339))
	}
	if strings.TrimSpace(t.Desc) != "" {
		fmt.Fprintf(w, "\n%s\n", t.Desc)
	}
	if len(t.Checks) > 0 {
		fmt.Fprintf(w, "\nchecklist:\n")
		for _, c := range t.Checks {
			box := " "
			if c.Done {
				box = "x"
			}
			fmt.Fprintf(w, "  [%s] %s\n", box, c.Text)
		}
	}
	switch {
	case !commentsAvailable:
		fmt.Fprintf(w, "\ncomments: unavailable in remote mode (see kb help)\n")
	case len(comments) == 0:
		fmt.Fprintf(w, "\ncomments: none\n")
	default:
		fmt.Fprintf(w, "\ncomments:\n")
		for _, c := range comments {
			fmt.Fprintf(w, "  c%d  %s  %s\n", c.ID, c.Author, c.CreatedAt.UTC().Format("2006-01-02 15:04"))
			for _, line := range strings.Split(c.Body, "\n") {
				fmt.Fprintf(w, "      %s\n", line)
			}
		}
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
