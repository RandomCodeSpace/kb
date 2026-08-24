package cliapp

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/project"
	"github.com/RandomCodeSpace/kb/internal/store"
)

// Every task carries exactly one project, spelled as the scoped label
// "project::<name>". Projects slice one board rather than splitting it into
// several: the label is ordinary task data, so nothing about the schema, the
// wire format, or the server changes.
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
const projectFlagUsage = "project for this task (overrides KB_PROJECT and the active project)"

// cliStateFile holds the CLI's own state — currently just the active project
// — inside the data directory, beside kb.db and the TUI's preference files.
//
// The active project lives here rather than in the database because it is
// client state, not board data: it has to resolve identically in remote mode
// (KB_SERVER), where no local store is ever opened, and it must not travel to
// whoever the board is served to. A file also keeps the map's "no schema
// change" promise literal — the settings table gains no column.
const cliStateFile = "state.json"

// cliState is the on-disk shape of state.json.
type cliState struct {
	ActiveProject string `json:"active_project,omitempty"`
}

// ErrNoProject is the refusal every mandatory-project path shares. It names
// both fixes because the command that hits it is the one being written.
var ErrNoProject = errors.New(`no project set: run "kb project use <name>" to pick one, or pass -p <name> (KB_PROJECT also works)`)

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

// loadCLIState reads state.json; a missing file is the zero state.
func loadCLIState(dataDir string) (cliState, error) {
	data, err := os.ReadFile(filepath.Join(dataDir, cliStateFile))
	if errors.Is(err, os.ErrNotExist) {
		return cliState{}, nil
	}
	if err != nil {
		return cliState{}, fmt.Errorf("read cli state: %w", err)
	}
	var state cliState
	if err := json.Unmarshal(data, &state); err != nil {
		return cliState{}, fmt.Errorf("decode cli state: %w", err)
	}
	return state, nil
}

// stateTempFile is the part of a temp file saveCLIState uses, so the atomic
// write can be exercised without a real filesystem.
type stateTempFile interface {
	io.Writer
	Name() string
	Sync() error
	Close() error
}

// stateFileOps is the filesystem seam under saveCLIState, mirroring the one
// the TUI's preference writer uses for the same reason: every failure of an
// atomic write is a real path that has to be tested.
type stateFileOps struct {
	mkdirAll  func(string, os.FileMode) error
	createTmp func(string, string) (stateTempFile, error)
	rename    func(string, string) error
	remove    func(string) error
}

var osStateFileOps = stateFileOps{
	mkdirAll: os.MkdirAll,
	createTmp: func(dir, pattern string) (stateTempFile, error) {
		return os.CreateTemp(dir, pattern)
	},
	rename: os.Rename,
	remove: os.Remove,
}

// saveCLIState writes state.json atomically: a temp file in the same
// directory, then a rename, so a crash never leaves a half-written state
// behind to fail every later project command.
func saveCLIState(dataDir string, state cliState) error {
	return saveCLIStateWithOps(dataDir, state, osStateFileOps)
}

func saveCLIStateWithOps(dataDir string, state cliState, ops stateFileOps) error {
	if err := ops.mkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode cli state: %w", err)
	}
	data = append(data, '\n')
	tmp, err := ops.createTmp(dataDir, ".state-*.tmp")
	if err != nil {
		return fmt.Errorf("create cli state temp file: %w", err)
	}
	published := false
	defer func() {
		if !published {
			_ = ops.remove(tmp.Name())
		}
	}()
	if err := writeStateFile(tmp, data); err != nil {
		return err
	}
	if err := ops.rename(tmp.Name(), filepath.Join(dataDir, cliStateFile)); err != nil {
		return fmt.Errorf("publish cli state: %w", err)
	}
	published = true
	return nil
}

// writeStateFile writes, syncs, and closes the temp state file. os.File.Write
// already reports a short write as an error, so there is nothing to check
// beyond it.
func writeStateFile(tmp stateTempFile, data []byte) error {
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write cli state: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync cli state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close cli state: %w", err)
	}
	return nil
}

// projectSource names where a resolved project came from, for kb project
// current --json and for error messages.
type projectSource string

const (
	sourceFlag   projectSource = "flag"
	sourceEnv    projectSource = "env"
	sourceStored projectSource = "stored"
	sourceTask   projectSource = "task"
)

// resolveActiveProject applies the resolution order: -p/--project beats
// KB_PROJECT, which beats the project stored by kb project use. ok is false
// when none of the three names a project.
func resolveActiveProject(flagValue, dataDir string) (name string, source projectSource, ok bool, err error) {
	if strings.TrimSpace(flagValue) != "" {
		name, err := ValidateProjectName(flagValue)
		if err != nil {
			return "", "", false, err
		}
		return name, sourceFlag, true, nil
	}
	if raw := strings.TrimSpace(os.Getenv("KB_PROJECT")); raw != "" {
		name, err := ValidateProjectName(raw)
		if err != nil {
			return "", "", false, fmt.Errorf("KB_PROJECT: %w", err)
		}
		return name, sourceEnv, true, nil
	}
	dir, err := resolveDataDir(dataDir)
	if err != nil {
		return "", "", false, err
	}
	state, err := loadCLIState(dir)
	if err != nil {
		return "", "", false, err
	}
	if state.ActiveProject == "" {
		return "", "", false, nil
	}
	return state.ActiveProject, sourceStored, true, nil
}

// ProjectTags returns tags carrying exactly one project:: label — the
// invariant every task-mutating path funnels through, on every surface. The
// CLI passes its -p value as flagValue; surfaces with their own project
// argument (the MCP tools) pass that, and surfaces with none (the TUI
// overlays) pass "" and take the active project.
//
// A project:: label spelled directly in --tag is as explicit as -p, so it
// beats KB_PROJECT and the stored active project; contradicting an explicit
// -p is an error rather than a silent winner. current is the project the task
// already has ("" when it has none or when the task is new): editing labels
// on an existing task keeps it in its project instead of dragging it into
// whatever happens to be active.
func ProjectTags(tags []string, flagValue, dataDir, current string) ([]string, error) {
	named, rest := SplitProjectTags(tags)
	for _, name := range named {
		if _, err := ValidateProjectName(name); err != nil {
			return nil, err
		}
	}
	if len(named) > 1 {
		return nil, fmt.Errorf("a task carries exactly one project:: label, got %d (%s)",
			len(named), strings.Join(named, ", "))
	}
	chosen, err := chooseProject(named, flagValue, dataDir, current)
	if err != nil {
		return nil, err
	}
	return append(rest, projectLabel(chosen)), nil
}

// chooseProject picks the one project for a task from the spelled-out label,
// the flag, and the resolution order behind them.
func chooseProject(named []string, flagValue, dataDir, current string) (string, error) {
	resolved, source, ok, err := resolveActiveProject(flagValue, dataDir)
	if err != nil {
		return "", err
	}
	if len(named) == 1 {
		if source == sourceFlag && named[0] != resolved {
			return "", fmt.Errorf("-%s %s contradicts --tag %s; pass one of them",
				projectFlagName, resolved, projectLabel(named[0]))
		}
		return named[0], nil
	}
	if source == sourceFlag {
		return resolved, nil
	}
	if current != "" {
		return current, nil
	}
	if !ok {
		return "", ErrNoProject
	}
	return resolved, nil
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
func applyProjectPatch(be backend, ref string, p *store.TaskPatch, flagValue, dataDir string, hasProject bool) error {
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
	tags, err := ProjectTags(base, flagValue, dataDir, CurrentProjectOf(it.task))
	if err != nil {
		return err
	}
	p.Tags = &tags
	return nil
}

// CurrentProjectOf returns the single project a task carries, or "" when it
// carries none or more than one (both of which the caller then replaces).
func CurrentProjectOf(t board.Task) string { return project.Of(t.Tags) }

// ActiveProject resolves the project local surfaces default to when no flag
// names one: KB_PROJECT, else the project stored by kb project use. The TUI
// takes its opening scope and its editor default from it, which is why the
// resolution is exported rather than copied.
func ActiveProject(dataDir string) (string, bool, error) {
	name, _, ok, err := resolveActiveProject("", dataDir)
	return name, ok, err
}

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

const projectUsage = `usage: kb project <use|current|list>

  kb project use <name>   set the active project for later commands
  kb project current      print the project commands default to
  kb project list         list every project on the board with task counts
`

func (a *app) cmdProject(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(a.stderr, projectUsage)
		return 2
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "use":
		return a.cmdProjectUse(rest)
	case "current":
		return a.cmdProjectCurrent(rest)
	case "list":
		return a.cmdProjectList(rest)
	case "help", "-h", "--help":
		fmt.Fprint(a.stdout, projectUsage)
		return 0
	}
	fmt.Fprintf(a.stderr, "kb: unknown project subcommand %q\n\n%s", sub, projectUsage)
	return 2
}

func (a *app) cmdProjectUse(args []string) int {
	fs, data := a.newFlagSet("project use")
	pos, err := parseInterleaved(fs, args)
	if code, done := a.parseResult(err); done {
		return code
	}
	if len(pos) != 1 {
		return a.usageErr(errors.New("project use needs exactly one <name> argument"))
	}
	name, err := ValidateProjectName(pos[0])
	if err != nil {
		return a.usageErr(err)
	}
	dir, err := resolveDataDir(*data)
	if err != nil {
		return a.fail(err)
	}
	state, err := loadCLIState(dir)
	if err != nil {
		return a.fail(err)
	}
	state.ActiveProject = name
	if err := saveCLIState(dir, state); err != nil {
		return a.fail(err)
	}
	fmt.Fprintf(a.stdout, "active project: %s\n", name)
	return 0
}

// projectCurrentJSON is the --json shape of kb project current.
type projectCurrentJSON struct {
	Project string `json:"project"`
	Source  string `json:"source"`
}

func (a *app) cmdProjectCurrent(args []string) int {
	fs, data := a.newFlagSet("project current")
	projectF := registerProjectFlag(fs)
	jsonF := fs.Bool("json", false, "print the resolved project as JSON")
	pos, err := parseInterleaved(fs, args)
	if code, done := a.parseResult(err); done {
		return code
	}
	if len(pos) != 0 {
		return a.usageErr(fmt.Errorf("project current takes no arguments, got %q", pos[0]))
	}
	name, source, ok, err := resolveActiveProject(*projectF, *data)
	if err != nil {
		return a.fail(err)
	}
	if !ok {
		return a.fail(ErrNoProject)
	}
	if *jsonF {
		if err := writeSingleJSON(a.stdout, projectCurrentJSON{Project: name, Source: string(source)}); err != nil {
			return a.fail(err)
		}
		return 0
	}
	fmt.Fprintln(a.stdout, name)
	return 0
}

// projectCountJSON is one row of kb project list --json.
type projectCountJSON struct {
	Project string `json:"project"`
	Tasks   int    `json:"tasks"`
	Active  bool   `json:"active,omitempty"`
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
	active, _, _, err := resolveActiveProject("", *data)
	if err != nil {
		return a.fail(err)
	}
	return a.withBackend(*data, func(be backend) error {
		items, err := be.list(store.TaskFilter{})
		if err != nil {
			return err
		}
		rows := projectCounts(items, active)
		if *jsonF {
			return writeSingleJSON(a.stdout, rows)
		}
		writeProjectTable(a.stdout, rows)
		return nil
	})
}

// projectCounts tallies tasks per project name, cancelled ones included, and
// marks the active project. A task carrying several project labels — only
// possible on a board a foreign writer touched — counts once per label.
func projectCounts(items []item, active string) []projectCountJSON {
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
	if active != "" && !slices.Contains(names, active) {
		names = append(names, active)
		sort.Strings(names)
	}
	rows := make([]projectCountJSON, 0, len(names))
	for _, name := range names {
		rows = append(rows, projectCountJSON{Project: name, Tasks: counts[name], Active: name == active})
	}
	return rows
}

func writeProjectTable(w io.Writer, rows []projectCountJSON) {
	tw := tabwriter.NewWriter(w, 2, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "PROJECT\tTASKS\tACTIVE")
	for _, r := range rows {
		mark := "-"
		if r.Active {
			mark = "*"
		}
		fmt.Fprintf(tw, "%s\t%d\t%s\n", r.Project, r.Tasks, mark)
	}
	tw.Flush()
}
