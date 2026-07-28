// Package cliapp implements the kb command-line interface: add, list,
// update, move, done, and rm against either the local SQLite store or, when
// KB_SERVER is set, a remote kb server over the markdown wire API
// (GET/PUT /api/board). In remote mode task ids are ephemeral listing
// indexes ("i1", "i2", ...); in local mode they are UUIDs addressable by
// unique prefix.
package cliapp

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
)

const usageText = `usage: kb <command> [flags]

commands:
  add "title"            add a task
  list                   list tasks
  update <id>            patch a task (only provided flags change)
  move <id> <status>     move a task to todo, doing, or done
  done <id>              shorthand for: move <id> done
  rm <id>                delete a task (requires --yes)
  help                   show this help

common flags (every command):
  --user name    board owner (default "default")
  --data dir     data directory (default $KB_DATA or ~/.local/share/kb)

card flags (add and update):
  --desc text    description
  --status s     todo, doing, or done
  --prio n       priority 1-4 (default 3)
  --due date     due date, YYYY-MM-DD
  --effort e     effort: S, M, or L
  --emoji e      leading emoji
  --tag t        tag (repeat for several)
  --check text   checklist item (repeat for several)
  --title text   new title (update only; add takes the title as argument)

list flags:
  --status s     show one column only
  --json         print full tasks as JSON

remote mode:
  KB_SERVER=http://host:port operates over the HTTP API instead of the
  local database; KB_SERVER_TOKEN adds a bearer token. Task ids are then
  ephemeral listing indexes (i1, i2, ...) valid against the current board.
`

// Run executes one kb CLI invocation. args starts with the subcommand,
// e.g. ["add", "Fix bug", "--prio", "2"]. It returns the process exit code:
// 0 on success, 1 on runtime errors, 2 on usage errors.
func Run(args []string, stdout, stderr io.Writer) int {
	a := &app{stdout: stdout, stderr: stderr}
	if len(args) == 0 {
		fmt.Fprint(stderr, usageText)
		return 2
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usageText)
		return 0
	case "add":
		return a.cmdAdd(rest)
	case "list":
		return a.cmdList(rest)
	case "update":
		return a.cmdUpdate(rest)
	case "move":
		return a.cmdMove(rest)
	case "done":
		return a.cmdDone(rest)
	case "rm":
		return a.cmdRm(rest)
	}
	fmt.Fprintf(stderr, "kb: unknown command %q\n\n%s", cmd, usageText)
	return 2
}

// app carries the output streams through one invocation.
type app struct {
	stdout, stderr io.Writer
}

func (a *app) fail(err error) int {
	fmt.Fprintf(a.stderr, "kb: %v\n", err)
	return 1
}

func (a *app) usageErr(err error) int {
	fmt.Fprintf(a.stderr, "kb: %v\n", err)
	return 2
}

// parseResult maps a flag-parse error to an exit code; done reports whether
// the command should stop with code.
func (a *app) parseResult(err error) (code int, done bool) {
	if err == nil {
		return 0, false
	}
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprint(a.stdout, usageText)
		return 0, true
	}
	return a.usageErr(err), true
}

// withBackend opens the backend for user/data, runs fn, and maps its error
// to the exit code.
func (a *app) withBackend(user, data string, fn func(backend) error) int {
	be, err := openBackend(user, data, a.stderr)
	if err != nil {
		return a.fail(err)
	}
	defer be.close()
	if err := fn(be); err != nil {
		return a.fail(err)
	}
	return 0
}

// backend is the storage abstraction shared by local (SQLite) and remote
// (HTTP wire markdown) modes.
type backend interface {
	list(status board.Status) ([]item, error)
	add(t board.Task) (item, error)
	update(ref string, p store.TaskPatch, moveTo *board.Status) (item, error)
	move(ref string, to board.Status) (item, error)
	remove(ref string) (item, error)
	close() error
}

// item pairs a task with the id the CLI prints and accepts for it: the full
// UUID in local mode, an ephemeral listing index ("i1", ...) in remote mode.
type item struct {
	ref  string
	task board.Task
}

// openBackend picks remote mode when KB_SERVER is set, local otherwise. The
// user name is normalized with the exact rules of the server's identity
// sanitization (store.SanitizeUser: trimmed, lowercased, charset-checked),
// so CLI, MCP, and SPA hit the same board.
func openBackend(user, dataDir string, stderr io.Writer) (backend, error) {
	user = strings.TrimSpace(user)
	if user == "" {
		user = "default"
	}
	user, err := store.SanitizeUser(user)
	if err != nil {
		return nil, err
	}
	if base := strings.TrimSpace(os.Getenv("KB_SERVER")); base != "" {
		return newRemote(strings.TrimRight(base, "/"), os.Getenv("KB_SERVER_TOKEN"), user), nil
	}
	return openLocal(user, dataDir, stderr)
}

// --- flag plumbing ---

// stringList is a repeatable string flag.
type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

// newFlagSet builds a silent FlagSet with the common --user/--data flags.
func (a *app) newFlagSet(name string) (fs *flag.FlagSet, user, data *string) {
	fs = flag.NewFlagSet("kb "+name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	user = fs.String("user", "default", `board owner`)
	data = fs.String("data", "", "data directory (default $KB_DATA or ~/.local/share/kb)")
	return fs, user, data
}

// cardFlags holds the card-field flags shared by add and update.
type cardFlags struct {
	title, desc, status, due, effort, emoji string
	prio                                    int
	tags, checks                            stringList
}

// registerCardFlags wires the card-field flags. withTitle additionally
// registers --title (update only; add takes the title positionally).
func registerCardFlags(fs *flag.FlagSet, c *cardFlags, withTitle bool) {
	if withTitle {
		fs.StringVar(&c.title, "title", "", "task title")
	}
	fs.StringVar(&c.desc, "desc", "", "description")
	fs.StringVar(&c.status, "status", "", "todo|doing|done")
	fs.IntVar(&c.prio, "prio", 0, "priority 1-4")
	fs.StringVar(&c.due, "due", "", "due date YYYY-MM-DD")
	fs.StringVar(&c.effort, "effort", "", "effort S|M|L")
	fs.StringVar(&c.emoji, "emoji", "", "leading emoji")
	fs.Var(&c.tags, "tag", "tag (repeatable)")
	fs.Var(&c.checks, "check", "checklist item (repeatable)")
}

// parseInterleaved parses args allowing flags and positional arguments in
// any order (Go's flag package alone stops at the first positional).
func parseInterleaved(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

// setFlags reports which flags were explicitly provided.
func setFlags(fs *flag.FlagSet) map[string]bool {
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	return set
}

// --- field validation ---

var dueRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func parseStatus(s string) (board.Status, error) {
	st := board.Status(strings.ToLower(strings.TrimSpace(s)))
	if !st.Valid() {
		return "", fmt.Errorf("invalid status %q (want todo, doing, or done)", s)
	}
	return st, nil
}

// parseDue validates a due date; empty is allowed (clears / unset).
func parseDue(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	if !dueRe.MatchString(s) {
		return "", fmt.Errorf("invalid due date %q (want YYYY-MM-DD)", s)
	}
	if _, err := time.Parse("2006-01-02", s); err != nil {
		return "", fmt.Errorf("invalid due date %q: not a real date", s)
	}
	return s, nil
}

// parseEffort validates and uppercases an effort; empty is allowed.
func parseEffort(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	e := strings.ToUpper(strings.TrimSpace(s))
	if e != "S" && e != "M" && e != "L" {
		return "", fmt.Errorf("invalid effort %q (want S, M, or L)", s)
	}
	return e, nil
}

// --- commands ---

func (a *app) cmdAdd(args []string) int {
	fs, user, data := a.newFlagSet("add")
	var cf cardFlags
	registerCardFlags(fs, &cf, false)
	pos, err := parseInterleaved(fs, args)
	if code, done := a.parseResult(err); done {
		return code
	}
	if len(pos) != 1 {
		return a.usageErr(errors.New(`add needs exactly one "title" argument`))
	}
	title := pos[0]
	if strings.TrimSpace(title) == "" {
		return a.usageErr(errors.New("title must not be empty"))
	}
	set := setFlags(fs)
	t := board.Task{Title: title, Desc: cf.desc, Emoji: cf.emoji}
	if cf.status != "" {
		st, err := parseStatus(cf.status)
		if err != nil {
			return a.usageErr(err)
		}
		t.Status = st
	}
	if set["prio"] {
		if cf.prio < 1 || cf.prio > 4 {
			return a.usageErr(fmt.Errorf("invalid prio %d (want 1-4)", cf.prio))
		}
		t.Prio = cf.prio
	}
	due, err := parseDue(cf.due)
	if err != nil {
		return a.usageErr(err)
	}
	t.Due = due
	eff, err := parseEffort(cf.effort)
	if err != nil {
		return a.usageErr(err)
	}
	t.Effort = eff
	t.Tags = append([]string(nil), cf.tags...)
	for _, c := range cf.checks {
		t.Checks = append(t.Checks, board.Check{Text: c})
	}
	return a.withBackend(*user, *data, func(be backend) error {
		it, err := be.add(t)
		if err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "added %s %s\n", displayRef(it.ref), it.task.Title)
		return nil
	})
}

func (a *app) cmdList(args []string) int {
	fs, user, data := a.newFlagSet("list")
	statusF := fs.String("status", "", "show one column only")
	jsonF := fs.Bool("json", false, "print full tasks as JSON")
	pos, err := parseInterleaved(fs, args)
	if code, done := a.parseResult(err); done {
		return code
	}
	if len(pos) != 0 {
		return a.usageErr(fmt.Errorf("list takes no arguments, got %q", pos[0]))
	}
	var st board.Status
	if *statusF != "" {
		s, err := parseStatus(*statusF)
		if err != nil {
			return a.usageErr(err)
		}
		st = s
	}
	return a.withBackend(*user, *data, func(be backend) error {
		items, err := be.list(st)
		if err != nil {
			return err
		}
		if *jsonF {
			return writeJSON(a.stdout, items)
		}
		writeTable(a.stdout, items)
		return nil
	})
}

func (a *app) cmdUpdate(args []string) int {
	fs, user, data := a.newFlagSet("update")
	var cf cardFlags
	registerCardFlags(fs, &cf, true)
	pos, err := parseInterleaved(fs, args)
	if code, done := a.parseResult(err); done {
		return code
	}
	if len(pos) != 1 {
		return a.usageErr(errors.New("update needs exactly one <id-prefix> argument"))
	}
	set := setFlags(fs)
	var p store.TaskPatch
	if set["title"] {
		if strings.TrimSpace(cf.title) == "" {
			return a.usageErr(errors.New("title must not be empty"))
		}
		p.Title = &cf.title
	}
	if set["desc"] {
		p.Desc = &cf.desc
	}
	if set["emoji"] {
		p.Emoji = &cf.emoji
	}
	if set["prio"] {
		if cf.prio < 1 || cf.prio > 4 {
			return a.usageErr(fmt.Errorf("invalid prio %d (want 1-4)", cf.prio))
		}
		p.Prio = &cf.prio
	}
	if set["due"] {
		d, err := parseDue(cf.due)
		if err != nil {
			return a.usageErr(err)
		}
		p.Due = &d
	}
	if set["effort"] {
		e, err := parseEffort(cf.effort)
		if err != nil {
			return a.usageErr(err)
		}
		p.Effort = &e
	}
	if set["tag"] {
		tags := append([]string(nil), cf.tags...)
		p.Tags = &tags
	}
	if set["check"] {
		checks := make([]board.Check, 0, len(cf.checks))
		for _, c := range cf.checks {
			checks = append(checks, board.Check{Text: c})
		}
		p.Checks = &checks
	}
	var moveTo *board.Status
	if set["status"] {
		st, err := parseStatus(cf.status)
		if err != nil {
			return a.usageErr(err)
		}
		moveTo = &st
	}
	if p == (store.TaskPatch{}) && moveTo == nil {
		return a.usageErr(errors.New("update needs at least one field flag"))
	}
	return a.withBackend(*user, *data, func(be backend) error {
		it, err := be.update(pos[0], p, moveTo)
		if err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "updated %s %s\n", displayRef(it.ref), it.task.Title)
		return nil
	})
}

func (a *app) cmdMove(args []string) int {
	fs, user, data := a.newFlagSet("move")
	pos, err := parseInterleaved(fs, args)
	if code, done := a.parseResult(err); done {
		return code
	}
	if len(pos) != 2 {
		return a.usageErr(errors.New("move needs <id-prefix> and <todo|doing|done> arguments"))
	}
	st, err := parseStatus(pos[1])
	if err != nil {
		return a.usageErr(err)
	}
	return a.moveTo(*user, *data, pos[0], st)
}

func (a *app) cmdDone(args []string) int {
	fs, user, data := a.newFlagSet("done")
	pos, err := parseInterleaved(fs, args)
	if code, done := a.parseResult(err); done {
		return code
	}
	if len(pos) != 1 {
		return a.usageErr(errors.New("done needs exactly one <id-prefix> argument"))
	}
	return a.moveTo(*user, *data, pos[0], board.StatusDone)
}

// moveTo is the shared body of move and done.
func (a *app) moveTo(user, data, ref string, to board.Status) int {
	return a.withBackend(user, data, func(be backend) error {
		it, err := be.move(ref, to)
		if err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "moved %s -> %s\n", displayRef(it.ref), it.task.Status)
		return nil
	})
}

func (a *app) cmdRm(args []string) int {
	fs, user, data := a.newFlagSet("rm")
	yes := fs.Bool("yes", false, "confirm deletion")
	pos, err := parseInterleaved(fs, args)
	if code, done := a.parseResult(err); done {
		return code
	}
	if len(pos) != 1 {
		return a.usageErr(errors.New("rm needs exactly one <id-prefix> argument"))
	}
	ref := pos[0]
	return a.withBackend(*user, *data, func(be backend) error {
		if !*yes {
			items, err := be.list("")
			if err != nil {
				return err
			}
			it, err := findItem(items, ref)
			if err != nil {
				return err
			}
			return fmt.Errorf("refusing to delete %s %q; re-run with --yes", displayRef(it.ref), it.task.Title)
		}
		it, err := be.remove(ref)
		if err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "deleted %s %s\n", displayRef(it.ref), it.task.Title)
		return nil
	})
}

// findItem locates ref among items without mutating anything (rm preview):
// exact ref match first (remote "i2", full UUIDs), then a bare remote index,
// then a unique local prefix.
func findItem(items []item, ref string) (item, error) {
	for _, it := range items {
		if it.ref == ref {
			return it, nil
		}
	}
	for _, it := range items {
		if it.ref == "i"+ref {
			return it, nil
		}
	}
	var match item
	n := 0
	for _, it := range items {
		if strings.HasPrefix(it.ref, ref) {
			match = it
			n++
		}
	}
	switch n {
	case 0:
		return item{}, fmt.Errorf("no task matches id %q", ref)
	case 1:
		return match, nil
	}
	return item{}, fmt.Errorf("task id prefix %q is ambiguous; use more characters", ref)
}

// --- output ---

// displayRef shortens local UUIDs to 8 characters; remote refs pass through.
func displayRef(ref string) string {
	if len(ref) > 8 {
		return ref[:8]
	}
	return ref
}

// writeTable renders items as an aligned table: id, status, prio, title,
// tags.
func writeTable(w io.Writer, items []item) {
	tw := tabwriter.NewWriter(w, 2, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATUS\tPRIO\tTITLE\tTAGS")
	for _, it := range items {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\n",
			displayRef(it.ref), it.task.Status, it.task.Prio, it.task.Title, strings.Join(it.task.Tags, ","))
	}
	tw.Flush()
}

type checkJSON struct {
	Text string `json:"text"`
	Done bool   `json:"done"`
}

type taskJSON struct {
	ID        string      `json:"id"`
	Emoji     string      `json:"emoji,omitempty"`
	Title     string      `json:"title"`
	Desc      string      `json:"desc,omitempty"`
	Status    string      `json:"status"`
	Prio      int         `json:"prio"`
	Due       string      `json:"due,omitempty"`
	Effort    string      `json:"effort,omitempty"`
	Tags      []string    `json:"tags,omitempty"`
	Checks    []checkJSON `json:"checks,omitempty"`
	Position  int         `json:"position"`
	CreatedAt string      `json:"createdAt,omitempty"`
	MovedAt   string      `json:"movedAt,omitempty"`
}

// writeJSON renders items as a JSON array of full tasks. Remote tasks carry
// no UUID, so their ephemeral ref becomes the id.
func writeJSON(w io.Writer, items []item) error {
	out := make([]taskJSON, 0, len(items))
	for _, it := range items {
		t := it.task
		j := taskJSON{
			ID: t.ID, Emoji: t.Emoji, Title: t.Title, Desc: t.Desc,
			Status: string(t.Status), Prio: t.Prio, Due: t.Due, Effort: t.Effort,
			Tags: t.Tags, Position: t.Position,
		}
		if j.ID == "" {
			j.ID = it.ref
		}
		for _, c := range t.Checks {
			j.Checks = append(j.Checks, checkJSON{Text: c.Text, Done: c.Done})
		}
		if !t.CreatedAt.IsZero() {
			j.CreatedAt = t.CreatedAt.UTC().Format(time.RFC3339Nano)
		}
		if !t.MovedAt.IsZero() {
			j.MovedAt = t.MovedAt.UTC().Format(time.RFC3339Nano)
		}
		out = append(out, j)
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("encode json: %w", err)
	}
	b = append(b, '\n')
	_, err = w.Write(b)
	return err
}
