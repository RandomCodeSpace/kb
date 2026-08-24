package tui

import (
	"sort"

	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/project"
)

// allProjectsLabel is what the switcher shows while the board is unscoped.
const allProjectsLabel = "all"

// projectSwitcher is the board's project scope: exactly one project, or all of
// them. It is the read side of the mandatory project label — the board never
// writes it — and it persists in the per-board preferences beside the cancelled
// toggle, so a board reopens scoped the way it was left.
//
// The zero value is "nothing chosen yet", which restore resolves to the active
// project the CLI would default to, or to all when there is none.
type projectSwitcher struct {
	name string // the selected project; "" with all false means unchosen
	all  bool
}

// chosen reports whether the switcher carries a selection of its own.
func (s projectSwitcher) chosen() bool { return s.all || s.name != "" }

// label is the switcher's value as the toolbar and footer spell it.
func (s projectSwitcher) label() string {
	if s.all || s.name == "" {
		return allProjectsLabel
	}
	return s.name
}

// scope narrows a board to the selected project. A task carrying no project
// (or two, which only a foreign writer can produce) belongs to no named
// project and is visible only under "all".
func (s projectSwitcher) scope(current board.Board) board.Board {
	if s.all || s.name == "" {
		return current
	}
	scoped := current
	scoped.Tasks = make([]board.Task, 0, len(current.Tasks))
	for _, task := range current.Tasks {
		if project.Of(task.Tags) == s.name {
			scoped.Tasks = append(scoped.Tasks, task)
		}
	}
	return scoped
}

// restore adopts the stored selection, falling back to the project the CLI
// would default to and finally to all.
func (s *projectSwitcher) restore(stored projectSwitcher, active string) {
	switch {
	case stored.all:
		*s = projectSwitcher{all: true}
	case stored.name != "":
		*s = projectSwitcher{name: stored.name}
	case active != "":
		*s = projectSwitcher{name: active}
	default:
		*s = projectSwitcher{all: true}
	}
}

// cycle moves the selection by delta through "all" followed by every project
// name, wrapping at both ends.
func (s *projectSwitcher) cycle(names []string, delta int) {
	options := append([]string{""}, names...)
	index := 0
	if !s.all && s.name != "" {
		for i, name := range options {
			if name == s.name {
				index = i
				break
			}
		}
	}
	next := options[((index+delta)%len(options)+len(options))%len(options)]
	*s = projectSwitcher{name: next, all: next == ""}
}

// boardProjects lists every project the whole board carries, plus the selected
// one and the active one when the board has no card under them yet, so the
// switcher can always cycle back to where it is.
func (m Model) boardProjects() []string {
	seen := make(map[string]struct{})
	for _, task := range m.board.Tasks {
		for _, name := range projectNames(task) {
			seen[name] = struct{}{}
		}
	}
	for _, name := range []string{m.projects.name, m.activeProject} {
		if name != "" {
			seen[name] = struct{}{}
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// projectNames is every project label on one task. A task with two of them is
// broken data, not a filter question: it is listed under both.
func projectNames(task board.Task) []string {
	named, _ := project.SplitTags(task.Tags)
	return named
}

// projectBoard is the board narrowed to the switcher's scope, before the text
// and label filter runs over it.
func (m Model) projectBoard() board.Board { return m.projects.scope(m.board) }

// projectDefault is the project a card created from this board belongs to: the
// switcher's selection while it names one, else the active project. Under
// "all" with no active project it is empty, and the editor refuses to save
// until one is typed.
func (m Model) projectDefault() string {
	if !m.projects.all && m.projects.name != "" {
		return m.projects.name
	}
	return m.activeProject
}

// cycleProject moves the switcher and re-projects the board, keeping the
// selected card where the new scope still holds it.
func (m *Model) cycleProject(delta int) tea.Cmd {
	names := m.boardProjects()
	if len(names) == 0 && !m.projects.chosen() {
		return nil
	}
	previous := m.filteredBoard()
	before := m.projects
	m.projects.cycle(names, delta)
	if m.projects == before {
		return nil
	}
	m.boardView.adoptBoard(previous, m.filteredBoard())
	m.editor.SetProjectDefault(m.projectDefault())
	return m.queuePreferences()
}
