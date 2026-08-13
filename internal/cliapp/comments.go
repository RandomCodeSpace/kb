package cliapp

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/RandomCodeSpace/kb/internal/store"
)

// commentJSON is the wire shape of one comment.
type commentJSON struct {
	ID        int    `json:"id"`
	TaskSeq   int    `json:"task,omitempty"`
	TaskID    string `json:"taskId"`
	Author    string `json:"author"`
	Body      string `json:"body"`
	CreatedAt string `json:"createdAt"`
}

func commentToJSON(c store.Comment) commentJSON {
	return commentJSON{
		ID: c.ID, TaskSeq: c.TaskSeq, TaskID: c.TaskID,
		Author: c.Author, Body: c.Body,
		CreatedAt: c.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
}

// resolveLocalUser applies the same normalization openBackend does, for
// commands that talk to the store directly.
func resolveLocalUser(user string) (string, error) {
	user = strings.TrimSpace(user)
	if user == "" {
		user = "default"
	}
	return store.SanitizeUser(user)
}

// commentRef parses a comment id: "7" or "c7".
func commentRef(ref string) (int, bool) {
	ref = strings.TrimPrefix(ref, "c")
	n, err := strconv.Atoi(ref)
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

// cmdComment dispatches the comment sub-verbs. Comments are local-only until
// the server API grows comment endpoints, so KB_SERVER mode is refused.
func (a *app) cmdComment(args []string) int {
	if len(args) == 0 {
		return a.usageErr(errors.New(`comment needs a sub-command: add, list, or rm`))
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "add":
		return a.cmdCommentAdd(rest)
	case "list":
		return a.cmdCommentList(rest)
	case "rm":
		return a.cmdCommentRm(rest)
	}
	return a.usageErr(fmt.Errorf("unknown comment sub-command %q (want add, list, or rm)", sub))
}

// withLocalStore refuses remote mode, opens the store, and hands fn the
// sanitized user.
func (a *app) withLocalStore(user, data string, fn func(st *store.Store, user string) error) int {
	if strings.TrimSpace(os.Getenv("KB_SERVER")) != "" {
		return a.fail(errors.New("comments are local-only for now; unset KB_SERVER (the server API has no comment endpoints yet)"))
	}
	u, err := resolveLocalUser(user)
	if err != nil {
		return a.fail(err)
	}
	st, err := openLocalStore(data, a.stderr)
	if err != nil {
		return a.fail(err)
	}
	defer st.Close()
	if err := fn(st, u); err != nil {
		return a.fail(err)
	}
	return 0
}

func (a *app) cmdCommentAdd(args []string) int {
	fs, user, data := a.newFlagSet("comment add")
	jsonF := fs.Bool("json", false, "print the comment as JSON")
	pos, err := parseInterleaved(fs, args)
	if code, done := a.parseResult(err); done {
		return code
	}
	if len(pos) != 2 {
		return a.usageErr(errors.New(`comment add needs <id> and "text" arguments`))
	}
	if strings.TrimSpace(pos[1]) == "" {
		return a.usageErr(errors.New("comment text must not be empty"))
	}
	return a.withLocalStore(*user, *data, func(st *store.Store, u string) error {
		c, err := st.AddComment(u, pos[0], u, pos[1])
		if err != nil {
			return friendlyIDErr(err, pos[0])
		}
		if *jsonF {
			return writeSingleJSON(a.stdout, commentToJSON(c))
		}
		fmt.Fprintf(a.stdout, "commented c%d on #%d\n", c.ID, c.TaskSeq)
		return nil
	})
}

func (a *app) cmdCommentList(args []string) int {
	fs, user, data := a.newFlagSet("comment list")
	jsonF := fs.Bool("json", false, "print comments as JSON")
	pos, err := parseInterleaved(fs, args)
	if code, done := a.parseResult(err); done {
		return code
	}
	if len(pos) != 1 {
		return a.usageErr(errors.New("comment list needs exactly one <id> argument"))
	}
	return a.withLocalStore(*user, *data, func(st *store.Store, u string) error {
		comments, err := st.Comments(u, pos[0])
		if err != nil {
			return friendlyIDErr(err, pos[0])
		}
		if *jsonF {
			out := make([]commentJSON, 0, len(comments))
			for _, c := range comments {
				out = append(out, commentToJSON(c))
			}
			return writeSingleJSON(a.stdout, out)
		}
		tw := tabwriter.NewWriter(a.stdout, 2, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tAUTHOR\tCREATED\tBODY")
		for _, c := range comments {
			fmt.Fprintf(tw, "c%d\t%s\t%s\t%s\n",
				c.ID, c.Author, c.CreatedAt.UTC().Format("2006-01-02"), firstLine(c.Body))
		}
		tw.Flush()
		return nil
	})
}

func (a *app) cmdCommentRm(args []string) int {
	fs, user, data := a.newFlagSet("comment rm")
	yes := fs.Bool("yes", false, "confirm deletion")
	jsonF := fs.Bool("json", false, "print the deleted comment as JSON")
	pos, err := parseInterleaved(fs, args)
	if code, done := a.parseResult(err); done {
		return code
	}
	if len(pos) != 1 {
		return a.usageErr(errors.New("comment rm needs exactly one <cid> argument"))
	}
	id, ok := commentRef(pos[0])
	if !ok {
		return a.usageErr(fmt.Errorf("invalid comment id %q (want c7 or 7)", pos[0]))
	}
	if !*yes {
		return a.fail(fmt.Errorf("refusing to delete comment c%d; re-run with --yes", id))
	}
	return a.withLocalStore(*user, *data, func(st *store.Store, u string) error {
		c, err := st.DeleteComment(u, id)
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("no comment matches id c%d", id)
		}
		if err != nil {
			return err
		}
		if *jsonF {
			return writeSingleJSON(a.stdout, commentToJSON(c))
		}
		fmt.Fprintf(a.stdout, "deleted c%d\n", c.ID)
		return nil
	})
}

// firstLine truncates a body to its first line for table display.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " ..."
	}
	return s
}
