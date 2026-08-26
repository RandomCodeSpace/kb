package tui

import (
	"slices"
	"strings"
	"unsafe"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/project"
)

// taskSearchIndex is deliberately unrelated to terminal sanitization. Search
// keeps the web-compatible case folding contract; display safety remains a
// renderer concern and cannot change which cards a query matches.
type taskSearchIndex struct {
	title string
	desc  string
	tags  []string
}

// taskDerivation is an immutable, render-relevant task snapshot. Strings are
// canonical immutable values, while nested slices are copied so a store or a
// test cannot mutate a published plan through an aliased backing array.
type taskDerivation struct {
	task   board.Task
	search taskSearchIndex
}

type projectionKey struct {
	filter      boardFilter
	projectName string
	projectAll  bool
}

// renderProjection owns exactly one source derivation and one current
// project/filter projection. It is not a general cache and retains no prior
// query results.
type renderProjection struct {
	initialized bool
	title       string
	tasks       []taskDerivation
	key         projectionKey
	board       board.Board
	statuses    [len(boardStatuses)][]int
}

func (p renderProjection) rebuild(model Model) (renderProjection, bool, bool) {
	tasksChanged := !p.matchesSource(model.board)
	if tasksChanged {
		p.title = model.board.Title
		p.tasks = deriveTasks(model.board.Tasks)
	}

	key := projectionKey{
		filter:      model.filter.value(),
		projectName: model.projects.name,
		projectAll:  model.projects.all,
	}
	projectionChanged := tasksChanged || !p.initialized || !sameProjectionKey(p.key, key)
	if projectionChanged {
		p.key = key
		p.board, p.statuses = buildCurrentProjection(p.title, p.tasks, key)
	}
	p.initialized = true
	return p, tasksChanged, projectionChanged
}

func deriveTasks(tasks []board.Task) []taskDerivation {
	derived := make([]taskDerivation, len(tasks))
	for i, source := range tasks {
		snapshot := source
		snapshot.Tags = append([]string(nil), source.Tags...)
		snapshot.Checks = append([]board.Check(nil), source.Checks...)
		tags := make([]string, len(snapshot.Tags))
		for j, tag := range snapshot.Tags {
			tags[j] = normalizeSearchValue(tag)
		}
		derived[i] = taskDerivation{
			task: snapshot,
			search: taskSearchIndex{
				title: normalizeSearchValue(snapshot.Title),
				desc:  normalizeSearchValue(snapshot.Desc),
				tags:  tags,
			},
		}
	}
	return derived
}

func normalizeSearchValue(value string) string { return webLower(value) }

func buildCurrentProjection(title string, tasks []taskDerivation, key projectionKey) (board.Board, [len(boardStatuses)][]int) {
	current := board.Board{Title: title, Tasks: make([]board.Task, 0, len(tasks))}
	var statuses [len(boardStatuses)][]int
	needle := normalizeSearchValue(strings.TrimSpace(key.filter.Text))
	for _, derived := range tasks {
		if !projectMatches(derived.task, key) || !filterMatches(derived, key.filter.Tags, needle) {
			continue
		}
		at := len(current.Tasks)
		current.Tasks = append(current.Tasks, derived.task)
		if status := statusIndexExact(derived.task.Status); status >= 0 {
			statuses[status] = append(statuses[status], at)
		}
	}
	return current, statuses
}

func projectMatches(task board.Task, key projectionKey) bool {
	return key.projectAll || key.projectName == "" || project.Of(task.Tags) == key.projectName
}

func filterMatches(task taskDerivation, selected []string, needle string) bool {
	for _, wanted := range selected {
		if !slices.Contains(task.task.Tags, wanted) {
			return false
		}
	}
	if needle == "" {
		return true
	}
	if strings.Contains(task.search.title, needle) || strings.Contains(task.search.desc, needle) {
		return true
	}
	for _, tag := range task.search.tags {
		if strings.Contains(tag, needle) {
			return true
		}
	}
	return false
}

func (p renderProjection) matchesModel(model Model) bool {
	if !p.initialized || !p.matchesSource(model.board) {
		return false
	}
	return p.key.projectName == model.projects.name && p.key.projectAll == model.projects.all &&
		p.key.filter.Text == model.filter.input.Value() && slices.Equal(p.key.filter.Tags, model.filter.tags)
}

func (p renderProjection) matchesSource(current board.Board) bool {
	if !p.initialized || p.title != current.Title || len(p.tasks) != len(current.Tasks) {
		return false
	}
	for i := range current.Tasks {
		if !sameTask(p.tasks[i].task, current.Tasks[i]) {
			return false
		}
	}
	return true
}

func sameTask(left, right board.Task) bool {
	return left.ID == right.ID && left.Seq == right.Seq && left.Emoji == right.Emoji &&
		left.Title == right.Title && left.Desc == right.Desc && left.Status == right.Status &&
		left.Blocked == right.Blocked && left.Prio == right.Prio && left.Due == right.Due &&
		left.Effort == right.Effort && slices.Equal(left.Tags, right.Tags) &&
		slices.Equal(left.Checks, right.Checks) && left.Position == right.Position &&
		left.CreatedAt.Equal(right.CreatedAt) && left.MovedAt.Equal(right.MovedAt)
}

func sameProjectionKey(left, right projectionKey) bool {
	return left.projectName == right.projectName && left.projectAll == right.projectAll &&
		left.filter.Text == right.filter.Text && slices.Equal(left.filter.Tags, right.filter.Tags)
}

func (p renderProjection) ownedBytesEstimate() uint64 {
	estimate := uint64(cap(p.tasks)) * uint64(unsafe.Sizeof(taskDerivation{}))
	estimate += uint64(cap(p.board.Tasks)) * uint64(unsafe.Sizeof(board.Task{}))
	for _, task := range p.tasks {
		estimate += uint64(cap(task.task.Tags)) * uint64(unsafe.Sizeof(""))
		estimate += uint64(cap(task.task.Checks)) * uint64(unsafe.Sizeof(board.Check{}))
		estimate += uint64(cap(task.search.tags)) * uint64(unsafe.Sizeof(""))
		estimate += uint64(len(task.search.title) + len(task.search.desc))
		for _, tag := range task.search.tags {
			estimate += uint64(len(tag))
		}
	}
	for _, indexes := range p.statuses {
		estimate += uint64(cap(indexes)) * uint64(unsafe.Sizeof(int(0)))
	}
	return estimate
}
