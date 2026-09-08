package cliapp

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/project"
	"github.com/RandomCodeSpace/kb/internal/store"
)

// Every task carries exactly one project, spelled as the scoped label
// "project::<name>". Projects slice one board rather than splitting it into
// several: the label is ordinary task data, so nothing about the schema
// changes.
//
// The vocabulary itself lives in internal/project because the TUI writes tasks
// too; these names are the CLI's local spelling of it.
const (
	projectScope       = project.Scope
	projectLabelPrefix = project.LabelPrefix
	// inboxProject is where tasks that never named a project end up.
	inboxProject = project.Inbox
)

const projectFlagName = "p"
const projectFlagUsage = "project for this task (required on add)"

// ErrNoProject is the refusal every mandatory-project path shares. The project
// is always spelled on the command: there is no ambient default to fall back
// to, so two shells or two agents on one data directory cannot file a task
// into each other's project.
var ErrNoProject = errors.New(`no project given: pass -p <name> or --tag project::<name>`)

// resolveDataDir applies the --data flag, falling back to $KB_DATA and then
// ~/.local/share/kb.
func resolveDataDir(dataDir string) (string, error) {
	if dataDir != "" {
		return dataDir, nil
	}
	return defaultDataDir()
}

// ValidateProjectName checks a project name by the rule already applied to
// label values: non-empty, no whitespace, no leading '#'. It additionally
// rejects "::" so a name can never smuggle a second scope into the label.
func ValidateProjectName(name string) (string, error) { return project.ValidateName(name) }

// projectLabel renders the scoped label for a project name.
func projectLabel(name string) string { return project.Label(name) }

// SplitProjectTags separates the project names a tag list carries from the
// tags that are not project labels, both in their original order.
func SplitProjectTags(tags []string) (projects, rest []string) { return project.SplitTags(tags) }

// ProjectTags returns tags carrying exactly one project:: label — the
// invariant every task-mutating path funnels through, on every surface. The
// CLI passes its -p value as flagValue; surfaces with their own project
// argument (the MCP tools, the web API) pass that.
//
// A project:: label spelled directly in --tag is as explicit as -p, and
// contradicting an explicit -p is an error rather than a silent winner.
// current is the project the task already has ("" when it has none or when
// the task is new): editing labels on an existing task keeps it in its
// project instead of demanding the flag again. A new task with no project
// anywhere is refused.
func ProjectTags(tags []string, flagValue, current string) ([]string, error) {
	named, rest := SplitProjectTags(tags)
	for _, name := range named {
		if _, err := ValidateProjectName(name); err != nil {
			return nil, err
		}
	}
	if len(named) > 1 {
		return nil, fmt.Errorf("a task carries exactly one project:: label, got %d: %s", len(named), strings.Join(named, ", "))
	}
	chosen, err := chooseProject(named, flagValue, current)
	if err != nil {
		return nil, err
	}
	return append(rest, projectLabel(chosen)), nil
}

// chooseProject picks the one project for a task from the spelled-out label,
// the flag, and the project the task already carries, in that order.
func chooseProject(named []string, flagValue, current string) (string, error) {
	flagged := ""
	if strings.TrimSpace(flagValue) != "" {
		name, err := ValidateProjectName(flagValue)
		if err != nil {
			return "", err
		}
		flagged = name
	}
	if len(named) == 1 {
		if flagged != "" && named[0] != flagged {
			return "", fmt.Errorf("-%s %s contradicts --tag %s; pass one of them",
				projectFlagName, flagged, projectLabel(named[0]))
		}
		return named[0], nil
	}
	if flagged != "" {
		return flagged, nil
	}
	if current != "" {
		return current, nil
	}
	return "", ErrNoProject
}

// registerProjectFlag wires -p and its long spelling --project onto one
// value, so either form reads the same on every command that takes it.
func registerProjectFlag(fs *flag.FlagSet) *string {
	value := fs.String(projectFlagName, "", projectFlagUsage)
	fs.StringVar(value, projectScope, "", projectFlagUsage)
	return value
}

// applyProjectPatch holds the one-project invariant across an update. It
// reads the task only when the update actually rewrites labels — --tag
// replaced them, or -p moved the task — and rewrites the patch so what lands
// carries exactly one project:: label, never zero and never two.
func applyProjectPatch(be *localBackend, ref string, p *store.TaskPatch, flagValue string, hasProject bool) error {
	if p.Tags == nil && !hasProject {
		return nil
	}
	it, _, _, err := be.view(ref)
	if err != nil {
		return err
	}
	// Without --tag the caller spelled no labels at all, so the project the
	// task already carries is a fallback rather than a competing choice: it
	// must not read as a contradiction of an explicit -p.
	var base []string
	if p.Tags != nil {
		base = *p.Tags
	} else {
		_, base = SplitProjectTags(it.task.Tags)
	}
	tags, err := ProjectTags(base, flagValue, CurrentProjectOf(it.task))
	if err != nil {
		return err
	}
	p.Tags = &tags
	return nil
}

// CurrentProjectOf returns the single project a task carries, or "" when it
// carries none or more than one (both of which the caller then replaces).
func CurrentProjectOf(t board.Task) string { return project.Of(t.Tags) }

// ProjectBackfiller is the slice of the store the backfill needs: *store.Store
// satisfies it, and a stub can make either half fail.
type ProjectBackfiller interface {
	FilterTasks(user string, f store.TaskFilter) ([]board.Task, error)
	UpdateTask(user, ref string, p store.TaskPatch) (board.Task, error)
}

// BackfillProjects gives every task without exactly one project:: label the
// invariant it is missing: none becomes project::inbox, several collapse to
// the first. It is idempotent — a second pass finds nothing to do — and
// returns the number of tasks it rewrote.
//
// It runs automatically whenever a local store is opened rather than behind
// an explicit command: the mandatory-project rule is only true if it holds
// for the tasks that predate it, and OpenLocalStore is the one startup path
// every local surface already shares (it is where the legacy markdown import
// lives too). Nothing is written once the board is clean.
func BackfillProjects(st ProjectBackfiller, user string) (int, error) {
	tasks, err := st.FilterTasks(user, store.TaskFilter{})
	if err != nil {
		return 0, err
	}
	changed := 0
	for _, t := range tasks {
		named, rest := SplitProjectTags(t.Tags)
		if len(named) == 1 {
			continue
		}
		name := inboxProject
		if len(named) > 1 {
			name = named[0]
		}
		tags := append(rest, projectLabel(name))
		if _, err := st.UpdateTask(user, t.ID, store.TaskPatch{Tags: &tags}); err != nil {
			return changed, err
		}
		changed++
	}
	return changed, nil
}

// --- kb project ---

const projectUsage = `usage: kb project list

  kb project list         list every project on the board with task counts

A task's project is always named on the command that creates it: kb add
-p <name>. There is no stored default and no environment override.
`

func (a *app) cmdProject(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(a.stderr, projectUsage)
		return 2
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		return a.cmdProjectList(rest)
	case "help", "-h", "--help":
		fmt.Fprint(a.stdout, projectUsage)
		return 0
	}
	fmt.Fprintf(a.stderr, "kb: unknown project subcommand %q\n\n%s", sub, projectUsage)
	return 2
}

// projectCountJSON is one row of kb project list --json.
type projectCountJSON struct {
	Project string `json:"project"`
	Tasks   int    `json:"tasks"`
}

func (a *app) cmdProjectList(args []string) int {
	fs, data := a.newFlagSet("project list")
	jsonF := fs.Bool("json", false, "print projects as JSON")
	pos, err := parseInterleaved(fs, args)
	if code, done := a.parseResult(err); done {
		return code
	}
	if len(pos) != 0 {
		return a.usageErr(fmt.Errorf("project list takes no arguments, got %q", pos[0]))
	}
	return a.withLocal(*data, func(be *localBackend) error {
		items, err := be.list(store.TaskFilter{})
		if err != nil {
			return err
		}
		rows := projectCounts(items)
		if *jsonF {
			return writeSingleJSON(a.stdout, rows)
		}
		writeProjectTable(a.stdout, rows)
		return nil
	})
}

// projectCounts tallies tasks per project name, cancelled ones included. A
// task carrying several project labels — only possible on a board a foreign
// writer touched — counts once per label.
func projectCounts(items []item) []projectCountJSON {
	counts := map[string]int{}
	for _, it := range items {
		named, _ := SplitProjectTags(it.task.Tags)
		for _, name := range named {
			counts[name]++
		}
	}
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Strings(names)
	rows := make([]projectCountJSON, 0, len(names))
	for _, name := range names {
		rows = append(rows, projectCountJSON{Project: name, Tasks: counts[name]})
	}
	return rows
}

func writeProjectTable(w io.Writer, rows []projectCountJSON) {
	tw := tabwriter.NewWriter(w, 2, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "PROJECT\tTASKS")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%d\n", r.Project, r.Tasks)
	}
	tw.Flush()
}
