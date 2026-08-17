// Package cliapp implements the kb command-line interface: add, list,
// update, move, done, cancel, restore, and rm against either the local
// SQLite store or, when KB_SERVER is set, a remote kb server over the
// markdown wire API (GET/PUT /api/board). In remote mode task ids are
// ephemeral listing indexes ("i1", "i2", ...); in local mode they are stable
// per-board sequence numbers ("#12", bare "12" works too) with UUIDs still
// addressable by unique prefix.
package cliapp

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
)

const noBlockedFlagName = "no-blocked"
const forceFlagUsage = "finish a task with open checklist items or a blocked flag"
const jsonFlagUsage = "print the affected task as JSON"

const usageText = `usage: kb <command> [flags]

commands:
  add "title"            add a task
  list                   list tasks
  view <id>              show one task in full, comments included
  update <id>            patch a task (only provided flags change)
  move <id> <status>     move a task to todo, doing, done, or cancelled
  done <id>              shorthand for: move <id> done
  cancel <id>            soft delete: move a task to cancelled, undo with
                         restore (this is the one you usually want)
  restore <id>           move a cancelled task back to todo
  rm <id>                hard delete: erase a task for good, no undo
                         (requires --yes)
  users                  list board owners and their task counts (local
                         database only; --json for machine output)
  link <a> blocks <b>    record that task a blocks task b (also accepts
                         blocked-by for the reverse direction). Finishing
                         a task with open blockers is refused like open
                         checklist items; --force overrides.
  unlink <a> <b>         remove the link between two tasks
  comment add <id> "text"
                         append a comment to a task
  comment list <id>      list a task's comments oldest-first
  comment rm <cid>       delete a comment for good (requires --yes);
                         comment ids are c1, c2, ... per board, stable and
                         never reused. Comments are local-only for now.
  help                   show this help

other modes (not task commands):
  kb                     with no command: serve the web UI (kb --help
                         lists the serve flags)
  kb tui                 open the local board in a full-screen terminal UI
  kb mcp                 serve the board to AI agents over MCP stdio

common flags (every command):
  --user name    board owner (default $KB_USER or "default")
  --data dir     data directory (default $KB_DATA or ~/.local/share/kb)
  --json         machine output: list prints an array of full tasks, every
                 other command prints the affected task as one JSON object

card flags (add and update):
  --desc text    description
  --status s     todo, doing, done, or cancelled
  --prio n       priority 1-4 (default 3)
  --due date     due date, YYYY-MM-DD
  --effort e     effort: S, M, or L
  --emoji e      leading emoji
  --tag t        tag (repeat for several)
  --check text   checklist item (repeat for several). Prefix "x " to add it
                 already ticked: --check "x reproduce locally", the same
                 convention the card's checklist box uses.
                 The space is required, so "xml parser fails" stays an open
                 item with its text intact; write \x for an item that really
                 does start with "x ".
                 On update the list is replaced, so pass every item you want
                 to keep, ticked or not.
  --blocked      flag the task as blocked
  --no-blocked   clear the blocked flag
  --title text   new title (update only; add takes the title as argument)

list flags:
  --status s     show one column only
  --search text  free text over title, description, and tags; every word
                 must appear, the last as a prefix
  --tag t        keep tasks carrying this label; repeatable, all must match
  --all          include cancelled tasks (hidden by default)

move, done, and update flags:
  --force        finish a task that still has open checklist items or is
                 flagged blocked; without it kb refuses (it never prompts).
                 Applies to done <id>, move <id> done, and update <id>
                 --status done alike. The check reads the task as it will be
                 once the update lands, so closing the last item and
                 finishing in one update needs no --force; when it does
                 refuse, nothing is written at all.

rm flags:
  --yes          confirm the delete. rm erases the row: the task leaves the
                 board and restore cannot bring it back. cancel is the
                 reversible alternative and is not a synonym for rm.

task ids:
  Tasks carry stable numbers: #1, #2, ... per board, assigned once and
  never reused. Commands take the number with or without the # (kb done 12
  and kb done '#12' are the same task). UUID prefixes still work, but a
  digits-only id always means the number. --json exposes both ("seq" and
  "id"). The same ids work locally and against a server.

remote mode:
  KB_SERVER=http://host:port operates over the HTTP API instead of the
  local database; KB_SERVER_TOKEN adds a bearer token. Every command works
  remotely except users, which reads the local database (the server API
  serves one board per identity). The old ephemeral i-N indexes are gone;
  use the stable #n numbers.
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
	case "cancel":
		return a.cmdCancel(rest)
	case "restore":
		return a.cmdRestore(rest)
	case "rm":
		return a.cmdRm(rest)
	case "users":
		return a.cmdUsers(rest)
	case "comment":
		return a.cmdComment(rest)
	case "view":
		return a.cmdView(rest)
	case "link":
		return a.cmdLink(rest)
	case "unlink":
		return a.cmdUnlink(rest)
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
	list(filter store.TaskFilter) ([]item, error)
	add(t board.Task) (item, error)
	// update applies the field patch and the optional status move as one
	// atomic step. Moving to done is guarded exactly as move and done are:
	// refused unless force, judged on the post-patch task, and rolled back
	// whole so a refusal persists neither the move nor the patch.
	update(ref string, p store.TaskPatch, moveTo *board.Status, force bool) (item, error)
	move(ref string, to board.Status, force bool) (item, error)
	remove(ref string) (item, error)
	// view fetches one task with its comments and links.
	view(ref string) (item, []store.Comment, store.TaskLinks, error)
	commentAdd(ref, body string) (store.Comment, error)
	comments(ref string) ([]store.Comment, error)
	commentRm(id int) (store.Comment, error)
	link(blockerRef, blockedRef string) (blocker, blocked board.Task, err error)
	unlink(aRef, bRef string) error
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

// envUser resolves the default board owner: KB_USER if set, else "default" —
// the same environment contract kb mcp honors, so both agent surfaces land
// on the same board without per-command flags.
func envUser() string {
	if v := strings.TrimSpace(os.Getenv("KB_USER")); v != "" {
		return v
	}
	return "default"
}

// newFlagSet builds a silent FlagSet with the common --user/--data flags.
func (a *app) newFlagSet(name string) (fs *flag.FlagSet, user, data *string) {
	fs = flag.NewFlagSet("kb "+name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	user = fs.String("user", envUser(), `board owner (env KB_USER)`)
	data = fs.String("data", "", "data directory (default $KB_DATA or ~/.local/share/kb)")
	return fs, user, data
}

// cardFlags holds the card-field flags shared by add and update.
type cardFlags struct {
	title, desc, status, due, effort, emoji string
	prio                                    int
	blocked, noBlocked                      bool
	tags, checks                            stringList
}

// registerCardFlags wires the card-field flags. withTitle additionally
// registers --title (update only; add takes the title positionally).
func registerCardFlags(fs *flag.FlagSet, c *cardFlags, withTitle bool) {
	if withTitle {
		fs.StringVar(&c.title, "title", "", "task title")
	}
	fs.StringVar(&c.desc, "desc", "", "description")
	fs.StringVar(&c.status, "status", "", "todo|doing|done|cancelled")
	fs.IntVar(&c.prio, "prio", 0, "priority 1-4")
	fs.StringVar(&c.due, "due", "", "due date YYYY-MM-DD")
	fs.StringVar(&c.effort, "effort", "", "effort S|M|L")
	fs.StringVar(&c.emoji, "emoji", "", "leading emoji")
	fs.BoolVar(&c.blocked, "blocked", false, "flag the task as blocked")
	fs.BoolVar(&c.noBlocked, noBlockedFlagName, false, "clear the blocked flag")
	fs.Var(&c.tags, "tag", "tag (repeatable)")
	fs.Var(&c.checks, "check", "checklist item (repeatable)")
}

// resolveBlocked folds the --blocked/--no-blocked pair into one tri-state:
// nil when neither flag was given, so update leaves the field alone.
func resolveBlocked(set map[string]bool, c *cardFlags) (*bool, error) {
	switch {
	case set["blocked"] && set[noBlockedFlagName]:
		return nil, errors.New("--blocked and --no-blocked cannot be combined")
	case set["blocked"]:
		v := c.blocked
		return &v, nil
	case set[noBlockedFlagName]:
		v := !c.noBlocked
		return &v, nil
	}
	return nil, nil
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
		return "", fmt.Errorf("invalid status %q (want todo, doing, done, or cancelled)", s)
	}
	return st, nil
}

// doneGuardErr is the refusal for finishing t under ref, or nil when the task
// is clear to ship. Callers evaluate it against the *post-patch* task and
// inside whatever transaction persists the change, so a refusal leaves
// nothing written — and so a patch that closes the last checklist item on its
// way to done is allowed rather than refused. The warning text lives in
// store.CompletionWarning so every surface phrases the refusal identically.
func doneGuardErr(ref string, t board.Task) error {
	warn := store.CompletionWarning(t)
	if warn == "" {
		return nil
	}
	return store.NewCompletionBlockedError(warn, displayID(item{ref: ref, task: t}), t.Title)
}

// parseCheckFlag decodes one --check value with the convention the card
// modal's checklist box already teaches ("one per line, prefix \"x \" when
// done"): "x reproduce locally" is a ticked item, "write failing test" is an
// open one. It mirrors src/components/CardModal.tsx's textToChecks, and the
// "- [x]" form the markdown wire uses underneath.
//
// The space after the x is required, so an item that merely starts with the
// letter ("xml parser fails") keeps its text and stays open. A leading
// backslash escapes the marker the same way it escapes metadata-shaped words
// in a title: `\x ray` is the open item "x ray".
func parseCheckFlag(s string) board.Check {
	s = strings.TrimSpace(s)
	if rest, ok := strings.CutPrefix(s, `\`); ok {
		return board.Check{Text: strings.TrimSpace(rest)}
	}
	if len(s) > 1 && (s[0] == 'x' || s[0] == 'X') && s[1] == ' ' {
		return board.Check{Text: strings.TrimSpace(s[2:]), Done: true}
	}
	return board.Check{Text: s}
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

func taskFromAddFlags(title string, cf cardFlags, set map[string]bool) (board.Task, error) {
	t := board.Task{Title: title, Desc: cf.desc, Emoji: cf.emoji}
	if cf.status != "" {
		st, err := parseStatus(cf.status)
		if err != nil {
			return board.Task{}, err
		}
		t.Status = st
	}
	if set["prio"] {
		if cf.prio < 1 || cf.prio > 4 {
			return board.Task{}, fmt.Errorf("invalid prio %d (want 1-4)", cf.prio)
		}
		t.Prio = cf.prio
	}
	var err error
	if t.Due, err = parseDue(cf.due); err != nil {
		return board.Task{}, err
	}
	if t.Effort, err = parseEffort(cf.effort); err != nil {
		return board.Task{}, err
	}
	blocked, err := resolveBlocked(set, &cf)
	if err != nil {
		return board.Task{}, err
	}
	if blocked != nil {
		t.Blocked = *blocked
	}
	t.Tags = append([]string(nil), cf.tags...)
	for _, c := range cf.checks {
		t.Checks = append(t.Checks, parseCheckFlag(c))
	}
	return t, nil
}

func (a *app) cmdAdd(args []string) int {
	fs, user, data := a.newFlagSet("add")
	var cf cardFlags
	registerCardFlags(fs, &cf, false)
	jsonF := fs.Bool("json", false, jsonFlagUsage)
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
	t, err := taskFromAddFlags(title, cf, setFlags(fs))
	if err != nil {
		return a.usageErr(err)
	}
	return a.withBackend(*user, *data, func(be backend) error {
		it, err := be.add(t)
		if err != nil {
			return err
		}
		if *jsonF {
			return writeJSONItem(a.stdout, it)
		}
		fmt.Fprintf(a.stdout, "added %s %s\n", displayID(it), it.task.Title)
		return nil
	})
}

func outputList(be backend, filter store.TaskFilter, all, asJSON bool, stdout io.Writer) error {
	items, err := be.list(filter)
	if err != nil {
		return err
	}
	// Cancelled tasks are soft-deleted: they stay on the board but out of
	// the way until asked for by --all or by name.
	if filter.Status == "" && !all {
		kept := make([]item, 0, len(items))
		for _, it := range items {
			if it.task.Status != board.StatusCancelled {
				kept = append(kept, it)
			}
		}
		items = kept
	}
	if asJSON {
		return writeJSON(stdout, items)
	}
	writeTable(stdout, items)
	return nil
}

// cmdUsers lists every board owner in the local database with their task
// count. It deliberately has no --user flag: the listing is global. Remote
// mode is refused because the wire API serves exactly one board per
// authenticated identity.
func (a *app) cmdUsers(args []string) int {
	fs := flag.NewFlagSet("kb users", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	data := fs.String("data", "", "data directory (default $KB_DATA or ~/.local/share/kb)")
	jsonF := fs.Bool("json", false, "print users as JSON")
	pos, err := parseInterleaved(fs, args)
	if code, done := a.parseResult(err); done {
		return code
	}
	if len(pos) != 0 {
		return a.usageErr(fmt.Errorf("users takes no arguments, got %q", pos[0]))
	}
	if strings.TrimSpace(os.Getenv("KB_SERVER")) != "" {
		return a.fail(errors.New("users reads the local database only; unset KB_SERVER (the server API serves one board per identity)"))
	}
	st, err := openLocalStore(*data, a.stderr)
	if err != nil {
		return a.fail(err)
	}
	defer st.Close()
	users, err := st.Users()
	if err != nil {
		return a.fail(err)
	}
	if *jsonF {
		enc := json.NewEncoder(a.stdout)
		enc.SetIndent("", "  ")
		if users == nil {
			users = []store.UserTasks{}
		}
		if err := enc.Encode(users); err != nil {
			return a.fail(err)
		}
		return 0
	}
	tw := tabwriter.NewWriter(a.stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "USER\tTASKS")
	for _, u := range users {
		fmt.Fprintf(tw, "%s\t%d\n", u.User, u.Tasks)
	}
	tw.Flush()
	return 0
}

func (a *app) cmdList(args []string) int {
	fs, user, data := a.newFlagSet("list")
	statusF := fs.String("status", "", "show one column only")
	searchF := fs.String("search", "", "free text over title, description, and tags")
	var tagsF stringList
	fs.Var(&tagsF, "tag", "keep tasks carrying this label; repeatable, all must match")
	allF := fs.Bool("all", false, "include cancelled tasks")
	jsonF := fs.Bool("json", false, "print full tasks as JSON")
	pos, err := parseInterleaved(fs, args)
	if code, done := a.parseResult(err); done {
		return code
	}
	if len(pos) != 0 {
		return a.usageErr(fmt.Errorf("list takes no arguments, got %q", pos[0]))
	}
	filter := store.TaskFilter{Search: strings.TrimSpace(*searchF), Tags: tagsF}
	if *statusF != "" {
		s, err := parseStatus(*statusF)
		if err != nil {
			return a.usageErr(err)
		}
		filter.Status = s
	}
	return a.withBackend(*user, *data, func(be backend) error {
		return outputList(be, filter, *allF, *jsonF, a.stdout)
	})
}

func setUpdateTextFields(p *store.TaskPatch, cf *cardFlags, set map[string]bool) error {
	if set["title"] {
		if strings.TrimSpace(cf.title) == "" {
			return errors.New("title must not be empty")
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
			return fmt.Errorf("invalid prio %d (want 1-4)", cf.prio)
		}
		p.Prio = &cf.prio
	}
	return nil
}

func setUpdateMetadataFields(p *store.TaskPatch, cf *cardFlags, set map[string]bool) error {
	if set["due"] {
		d, err := parseDue(cf.due)
		if err != nil {
			return err
		}
		p.Due = &d
	}
	if set["effort"] {
		e, err := parseEffort(cf.effort)
		if err != nil {
			return err
		}
		p.Effort = &e
	}
	blocked, err := resolveBlocked(set, cf)
	if err != nil {
		return err
	}
	p.Blocked = blocked
	if set["tag"] {
		tags := append([]string(nil), cf.tags...)
		p.Tags = &tags
	}
	if set["check"] {
		checks := make([]board.Check, 0, len(cf.checks))
		for _, c := range cf.checks {
			checks = append(checks, parseCheckFlag(c))
		}
		p.Checks = &checks
	}
	return nil
}

func updateMoveTo(cf *cardFlags, set map[string]bool) (*board.Status, error) {
	if !set["status"] {
		return nil, nil
	}
	st, err := parseStatus(cf.status)
	if err != nil {
		return nil, err
	}
	return &st, nil
}

func updatePatch(cf *cardFlags, set map[string]bool) (store.TaskPatch, *board.Status, error) {
	var p store.TaskPatch
	if err := setUpdateTextFields(&p, cf, set); err != nil {
		return store.TaskPatch{}, nil, err
	}
	if err := setUpdateMetadataFields(&p, cf, set); err != nil {
		return store.TaskPatch{}, nil, err
	}
	moveTo, err := updateMoveTo(cf, set)
	if err != nil {
		return store.TaskPatch{}, nil, err
	}
	if p == (store.TaskPatch{}) && moveTo == nil {
		return store.TaskPatch{}, nil, errors.New("update needs at least one field flag")
	}
	return p, moveTo, nil
}

func (a *app) cmdUpdate(args []string) int {
	fs, user, data := a.newFlagSet("update")
	var cf cardFlags
	registerCardFlags(fs, &cf, true)
	force := fs.Bool("force", false, forceFlagUsage)
	jsonF := fs.Bool("json", false, jsonFlagUsage)
	pos, err := parseInterleaved(fs, args)
	if code, done := a.parseResult(err); done {
		return code
	}
	if len(pos) != 1 {
		return a.usageErr(errors.New("update needs exactly one <id-prefix> argument"))
	}
	p, moveTo, err := updatePatch(&cf, setFlags(fs))
	if err != nil {
		return a.usageErr(err)
	}
	return a.withBackend(*user, *data, func(be backend) error {
		it, err := be.update(pos[0], p, moveTo, *force)
		if err != nil {
			return err
		}
		if *jsonF {
			return writeJSONItem(a.stdout, it)
		}
		fmt.Fprintf(a.stdout, "updated %s %s\n", displayID(it), it.task.Title)
		return nil
	})
}

func (a *app) cmdMove(args []string) int {
	fs, user, data := a.newFlagSet("move")
	force := fs.Bool("force", false, forceFlagUsage)
	jsonF := fs.Bool("json", false, jsonFlagUsage)
	pos, err := parseInterleaved(fs, args)
	if code, done := a.parseResult(err); done {
		return code
	}
	if len(pos) != 2 {
		return a.usageErr(errors.New("move needs <id-prefix> and <todo|doing|done|cancelled> arguments"))
	}
	st, err := parseStatus(pos[1])
	if err != nil {
		return a.usageErr(err)
	}
	return a.moveTo(*user, *data, pos[0], st, *force, *jsonF)
}

func (a *app) cmdDone(args []string) int {
	fs, user, data := a.newFlagSet("done")
	force := fs.Bool("force", false, forceFlagUsage)
	jsonF := fs.Bool("json", false, jsonFlagUsage)
	pos, err := parseInterleaved(fs, args)
	if code, done := a.parseResult(err); done {
		return code
	}
	if len(pos) != 1 {
		return a.usageErr(errors.New("done needs exactly one <id-prefix> argument"))
	}
	return a.moveTo(*user, *data, pos[0], board.StatusDone, *force, *jsonF)
}

// cmdCancel soft-deletes a task: it moves to the cancelled column and stays
// on the board, unlike rm --yes which deletes the row for good.
func (a *app) cmdCancel(args []string) int {
	fs, user, data := a.newFlagSet("cancel")
	jsonF := fs.Bool("json", false, jsonFlagUsage)
	pos, err := parseInterleaved(fs, args)
	if code, done := a.parseResult(err); done {
		return code
	}
	if len(pos) != 1 {
		return a.usageErr(errors.New("cancel needs exactly one <id-prefix> argument"))
	}
	return a.moveTo(*user, *data, pos[0], board.StatusCancelled, false, *jsonF)
}

// cmdRestore undoes a cancel, putting the task back at the end of To Do.
func (a *app) cmdRestore(args []string) int {
	fs, user, data := a.newFlagSet("restore")
	jsonF := fs.Bool("json", false, jsonFlagUsage)
	pos, err := parseInterleaved(fs, args)
	if code, done := a.parseResult(err); done {
		return code
	}
	if len(pos) != 1 {
		return a.usageErr(errors.New("restore needs exactly one <id-prefix> argument"))
	}
	return a.moveTo(*user, *data, pos[0], board.StatusTodo, false, *jsonF)
}

// moveTo is the shared body of move, done, cancel, and restore. Moving to
// done with open checklist items or a blocked flag is refused unless force
// is set: the CLI has no interactive confirmation, so it errors out rather
// than shipping something the user may not have meant to.
func (a *app) moveTo(user, data, ref string, to board.Status, force, asJSON bool) int {
	return a.withBackend(user, data, func(be backend) error {
		it, err := be.move(ref, to, force)
		if err != nil {
			return err
		}
		if asJSON {
			return writeJSONItem(a.stdout, it)
		}
		fmt.Fprintf(a.stdout, "moved %s -> %s\n", displayID(it), it.task.Status)
		return nil
	})
}

func (a *app) cmdRm(args []string) int {
	fs, user, data := a.newFlagSet("rm")
	yes := fs.Bool("yes", false, "confirm deletion")
	jsonF := fs.Bool("json", false, jsonFlagUsage)
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
			items, err := be.list(store.TaskFilter{})
			if err != nil {
				return err
			}
			it, err := findItem(items, ref)
			if err != nil {
				return err
			}
			return fmt.Errorf("refusing to delete %s %q; re-run with --yes", displayID(it), it.task.Title)
		}
		it, err := be.remove(ref)
		if err != nil {
			return err
		}
		if *jsonF {
			return writeJSONItem(a.stdout, it)
		}
		fmt.Fprintf(a.stdout, "deleted %s %s\n", displayID(it), it.task.Title)
		return nil
	})
}

// findItem locates ref among items without mutating anything (rm preview):
// exact ref match first (remote "i2", full UUIDs), then a bare remote index,
// then a stable sequence number, then a unique local prefix.
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
	// Digits-only refs are sequence numbers and never fall through to
	// UUID-prefix matching, mirroring the store's resolution rule.
	if n, ok := seqRef(ref); ok {
		for _, it := range items {
			if it.task.Seq == n {
				return it, nil
			}
		}
		return item{}, fmt.Errorf("no task matches id %q", ref)
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

// seqRef reports whether ref addresses a task by stable sequence number:
// all digits, optionally after one leading '#'.
func seqRef(ref string) (int, bool) {
	ref = strings.TrimPrefix(ref, "#")
	if ref == "" {
		return 0, false
	}
	for _, r := range ref {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(ref)
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

// displayID renders the id agents type back: the stable #n when the task
// carries one, else the shortened ref (an i-N index in remote mode, where
// tasks have no sequence number).
func displayID(it item) string {
	if it.task.Seq > 0 {
		return "#" + strconv.Itoa(it.task.Seq)
	}
	return displayRef(it.ref)
}

// displayRef shortens local UUIDs to 8 characters; remote refs pass through.
func displayRef(ref string) string {
	if len(ref) > 8 {
		return ref[:8]
	}
	return ref
}

// writeTable renders items as an aligned table: id, status, prio, blocked
// marker, title, tags.
func writeTable(w io.Writer, items []item) {
	tw := tabwriter.NewWriter(w, 2, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATUS\tPRIO\tBLOCKED\tTITLE\tTAGS")
	for _, it := range items {
		blocked := "-"
		if it.task.Blocked {
			blocked = "yes"
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\t%s\n",
			displayID(it), it.task.Status, it.task.Prio, blocked, it.task.Title, strings.Join(it.task.Tags, ","))
	}
	tw.Flush()
}

type checkJSON struct {
	Text string `json:"text"`
	Done bool   `json:"done"`
}

type taskJSON struct {
	ID        string      `json:"id"`
	Seq       int         `json:"seq,omitempty"`
	Emoji     string      `json:"emoji,omitempty"`
	Title     string      `json:"title"`
	Desc      string      `json:"desc,omitempty"`
	Status    string      `json:"status"`
	Blocked   bool        `json:"blocked,omitempty"`
	Prio      int         `json:"prio"`
	Due       string      `json:"due,omitempty"`
	Effort    string      `json:"effort,omitempty"`
	Tags      []string    `json:"tags,omitempty"`
	Checks    []checkJSON `json:"checks,omitempty"`
	Position  int         `json:"position"`
	CreatedAt string      `json:"createdAt,omitempty"`
	MovedAt   string      `json:"movedAt,omitempty"`
}

// itemJSON builds the wire shape of one task. Remote tasks carry no UUID,
// so their ephemeral ref becomes the id.
func itemJSON(it item) taskJSON {
	t := it.task
	j := taskJSON{
		ID: t.ID, Seq: t.Seq, Emoji: t.Emoji, Title: t.Title, Desc: t.Desc,
		Status: string(t.Status), Blocked: t.Blocked, Prio: t.Prio, Due: t.Due,
		Effort: t.Effort, Tags: t.Tags, Position: t.Position,
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
	return j
}

// writeSingleJSON renders v as indented JSON with a trailing newline.
func writeSingleJSON(w io.Writer, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encode json: %w", err)
	}
	b = append(b, '\n')
	_, err = w.Write(b)
	return err
}

// writeJSON renders items as a JSON array of full tasks.
func writeJSON(w io.Writer, items []item) error {
	out := make([]taskJSON, 0, len(items))
	for _, it := range items {
		out = append(out, itemJSON(it))
	}
	return writeSingleJSON(w, out)
}

// writeJSONItem renders one affected task as a single JSON object — the
// --json form of every mutation verb's confirmation line.
func writeJSONItem(w io.Writer, it item) error {
	return writeSingleJSON(w, itemJSON(it))
}
