package tui

import (
	"reflect"
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
	task           board.Task
	search         taskSearchIndex
	renderRevision uint64
}

type projectionKey struct {
	filter         boardFilter
	filterRevision uint64
	projectName    string
	projectAll     bool
}

type projectionStatusSummary struct {
	count   int
	blocked int
}

type projectionDelta struct {
	SourceChanged  bool
	CurrentChanged bool
	DerivedTasks   int
}

// renderProjection owns exactly one source derivation and one current
// project/filter projection. It is not a general cache and retains no prior
// query results.
type renderProjection struct {
	initialized        bool
	sourceGeneration   uint64
	generation         uint64
	nextRenderRevision uint64
	title              string
	sourceData         *board.Task
	sourceLen          int
	tasks              []taskDerivation
	key                projectionKey
	board              board.Board
	statuses           [len(boardStatuses)][]int
	summaries          [len(boardStatuses)]projectionStatusSummary
	ordinals           [len(boardStatuses)]map[string]int
	taskIndexes        map[string]int
	sourceIndexes      map[string]int
	labels             []string
	toolbarLabels      []string
	projected          int
	ownedBytes         uint64
}

func (p renderProjection) rebuild(model Model) (renderProjection, bool, bool) {
	return p.rebuildSource(model, true)
}

// rebuildSource lets the root render plan skip an O(total tasks) source
// comparison when Update did not replace the board snapshot. Explicit rebuilds
// retain the deep comparison above; tests and cold callers therefore keep the
// mutation-detection contract without taxing every navigation event.
func (p renderProjection) rebuildSource(model Model, checkSource bool) (renderProjection, bool, bool) {
	tasksChanged := !p.initialized
	if p.initialized && checkSource {
		if p.matchesSource(model.board) {
			// A store reload commonly allocates a fresh but semantically identical
			// board. Rebind the identity after the one deep proof so every later
			// ordinary input returns to the O(1) source check.
			p.title = model.board.Title
			p.sourceData = unsafe.SliceData(model.board.Tasks)
			p.sourceLen = len(model.board.Tasks)
		} else {
			tasksChanged = true
		}
	}
	if tasksChanged {
		p.sourceGeneration++
		p.title = model.board.Title
		p.sourceData = unsafe.SliceData(model.board.Tasks)
		p.sourceLen = len(model.board.Tasks)
		p.tasks = make([]taskDerivation, len(model.board.Tasks))
		for index, source := range model.board.Tasks {
			p.tasks[index] = deriveTask(source)
			p.tasks[index].renderRevision = p.allocateRenderRevision()
		}
		p.sourceIndexes = make(map[string]int, len(model.board.Tasks))
		for index, task := range model.board.Tasks {
			p.sourceIndexes[task.ID] = index
		}
	}

	key := projectionKey{
		filter:         model.filter.value(),
		filterRevision: model.filter.projectionRevision,
		projectName:    model.projects.name,
		projectAll:     model.projects.all,
	}
	projectionChanged := tasksChanged || !p.initialized || !sameProjectionKey(p.key, key)
	if projectionChanged {
		p.generation++
		p.key = key
		p.board, p.statuses, p.summaries, p.ordinals, p.taskIndexes, p.labels, p.projected =
			buildCurrentProjection(p.title, p.tasks, key)
		p.toolbarLabels = selectedInclusiveLabels(p.labels, key.filter.Tags)
		p.refreshOwnedBytes()
	}
	p.initialized = true
	return p, tasksChanged, projectionChanged
}

// reconcileSource replaces the immutable source snapshot by task identity.
// Unchanged derivations retain their nested storage; only new or changed tasks
// pay normalization and snapshot costs. A source-only change may therefore
// advance without changing the current filter/project projection generation.
func (p renderProjection) reconcileSource(model Model) (renderProjection, projectionDelta) {
	if !p.initialized {
		next, sourceChanged, currentChanged := p.rebuildSource(model, true)
		return next, projectionDelta{
			SourceChanged: sourceChanged, CurrentChanged: currentChanged, DerivedTasks: len(next.tasks),
		}
	}
	if p.matchesSourceIdentity(model.board) {
		return p, projectionDelta{}
	}

	delta := projectionDelta{SourceChanged: p.title != model.board.Title || len(p.tasks) != len(model.board.Tasks)}
	tasks := make([]taskDerivation, len(model.board.Tasks))
	sourceIndexes := make(map[string]int, len(model.board.Tasks))
	for index, source := range model.board.Tasks {
		sourceIndexes[source.ID] = index
		previous, found := p.sourceIndexes[source.ID]
		if found && previous >= 0 && previous < len(p.tasks) && sameTask(p.tasks[previous].task, source) {
			tasks[index] = p.tasks[previous]
			if previous != index {
				delta.SourceChanged = true
			}
			continue
		}
		tasks[index] = deriveTask(source)
		tasks[index].renderRevision = p.allocateRenderRevision()
		delta.SourceChanged = true
		delta.DerivedTasks++
	}

	p.title = model.board.Title
	p.sourceData = unsafe.SliceData(model.board.Tasks)
	p.sourceLen = len(model.board.Tasks)
	p.sourceIndexes = sourceIndexes
	if !delta.SourceChanged {
		return p, delta
	}
	p.sourceGeneration++
	p.tasks = tasks
	key := projectionKey{
		filter: model.filter.value(), filterRevision: model.filter.projectionRevision,
		projectName: model.projects.name, projectAll: model.projects.all,
	}
	boardProjection, statuses, summaries, ordinals, taskIndexes, labels, projected :=
		buildCurrentProjection(p.title, p.tasks, key)
	toolbarLabels := selectedInclusiveLabels(labels, key.filter.Tags)
	delta.CurrentChanged = !sameCurrentProjection(
		p, key, boardProjection, statuses, summaries, ordinals, taskIndexes, toolbarLabels, projected,
	)
	if delta.CurrentChanged {
		p.generation++
	}
	// Raw labels are source metadata, not publication identity. Install the
	// fully reconciled projection even when its rendered toolbar and cards are
	// unchanged so a later filter edit starts from the newest source facts.
	p.key = key
	p.board = boardProjection
	p.statuses = statuses
	p.summaries = summaries
	p.ordinals = ordinals
	p.taskIndexes = taskIndexes
	p.labels = labels
	p.toolbarLabels = toolbarLabels
	p.projected = projected
	p.refreshOwnedBytes()
	return p, delta
}

func sameCurrentProjection(
	current renderProjection,
	key projectionKey,
	projectedBoard board.Board,
	statuses [len(boardStatuses)][]int,
	summaries [len(boardStatuses)]projectionStatusSummary,
	ordinals [len(boardStatuses)]map[string]int,
	taskIndexes map[string]int,
	toolbarLabels []string,
	projected int,
) bool {
	if !sameProjectionKey(current.key, key) || current.board.Title != projectedBoard.Title ||
		len(current.board.Tasks) != len(projectedBoard.Tasks) || current.summaries != summaries ||
		current.projected != projected || !slices.Equal(current.toolbarLabels, toolbarLabels) {
		return false
	}
	for index := range current.board.Tasks {
		if !sameTask(current.board.Tasks[index], projectedBoard.Tasks[index]) {
			return false
		}
	}
	return reflect.DeepEqual(current.statuses, statuses) && reflect.DeepEqual(current.ordinals, ordinals) &&
		reflect.DeepEqual(current.taskIndexes, taskIndexes)
}

func deriveTask(source board.Task) taskDerivation {
	snapshot := source
	snapshot.Tags = append([]string(nil), source.Tags...)
	snapshot.Checks = append([]board.Check(nil), source.Checks...)
	tags := make([]string, len(snapshot.Tags))
	for index, tag := range snapshot.Tags {
		tags[index] = normalizeSearchValue(tag)
	}
	return taskDerivation{
		task: snapshot,
		search: taskSearchIndex{
			title: normalizeSearchValue(snapshot.Title),
			desc:  normalizeSearchValue(snapshot.Desc),
			tags:  tags,
		},
	}
}

// allocateRenderRevision returns a projection-local monotonic identity for one
// immutable task render snapshot. Exhaustion fails closed: zero revisions are
// never cacheable, so wrapping cannot make changed content alias old output.
func (p *renderProjection) allocateRenderRevision() uint64 {
	if p.nextRenderRevision == ^uint64(0) {
		return 0
	}
	p.nextRenderRevision++
	return p.nextRenderRevision
}

func (p renderProjection) taskRenderRevision(taskID string) (uint64, bool) {
	index, ok := p.sourceIndexes[taskID]
	if !ok || index < 0 || index >= len(p.tasks) || p.tasks[index].task.ID != taskID {
		return 0, false
	}
	revision := p.tasks[index].renderRevision
	return revision, revision != 0
}

func normalizeSearchValue(value string) string { return webLower(value) }

func buildCurrentProjection(
	title string,
	tasks []taskDerivation,
	key projectionKey,
) (
	board.Board,
	[len(boardStatuses)][]int,
	[len(boardStatuses)]projectionStatusSummary,
	[len(boardStatuses)]map[string]int,
	map[string]int,
	[]string,
	int,
) {
	current := board.Board{Title: title, Tasks: make([]board.Task, 0, len(tasks))}
	var statuses [len(boardStatuses)][]int
	var summaries [len(boardStatuses)]projectionStatusSummary
	var ordinals [len(boardStatuses)]map[string]int
	for index := range ordinals {
		ordinals[index] = make(map[string]int)
	}
	taskIndexes := make(map[string]int, len(tasks))
	labelSet := make(map[string]struct{})
	projected := 0
	needle := normalizeSearchValue(strings.TrimSpace(key.filter.Text))
	for _, derived := range tasks {
		if !projectMatches(derived.task, key) {
			continue
		}
		projected++
		_, tags := project.SplitTags(derived.task.Tags)
		for _, tag := range tags {
			if tag != "" {
				labelSet[tag] = struct{}{}
			}
		}
		if !filterMatches(derived, key.filter.Tags, needle) {
			continue
		}
		at := len(current.Tasks)
		current.Tasks = append(current.Tasks, derived.task)
		taskIndexes[derived.task.ID] = at
		if status := statusIndexExact(derived.task.Status); status >= 0 {
			ordinal := len(statuses[status])
			statuses[status] = append(statuses[status], at)
			ordinals[status][derived.task.ID] = ordinal
			summaries[status].count++
			if derived.task.Blocked {
				summaries[status].blocked++
			}
		}
	}
	labels := make([]string, 0, len(labelSet))
	for label := range labelSet {
		labels = append(labels, label)
	}
	slices.Sort(labels)
	return current, statuses, summaries, ordinals, taskIndexes, labels, projected
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

func (p renderProjection) matchesProjectionKey(model Model) bool {
	return p.matchesSourceIdentity(model.board) &&
		p.key.projectName == model.projects.name && p.key.projectAll == model.projects.all &&
		p.key.filterRevision == model.filter.projectionRevision
}

func (p renderProjection) matchesSourceIdentity(current board.Board) bool {
	return p.initialized && p.title == current.Title && p.sourceLen == len(current.Tasks) &&
		p.sourceData == unsafe.SliceData(current.Tasks)
}

func (p renderProjection) sourceTaskByID(current board.Board, id string) (board.Task, bool) {
	index, ok := p.sourceIndexes[id]
	if !ok || index < 0 || index >= len(current.Tasks) || current.Tasks[index].ID != id {
		return board.Task{}, false
	}
	return current.Tasks[index], true
}

func (p renderProjection) statusCount(status board.Status) int {
	index := statusIndexExact(status)
	if index < 0 {
		return 0
	}
	return p.summaries[index].count
}

func (p renderProjection) taskAtStatus(status board.Status, ordinal int) (board.Task, bool) {
	index := statusIndexExact(status)
	if index < 0 || ordinal < 0 || ordinal >= len(p.statuses[index]) {
		return board.Task{}, false
	}
	return p.board.Tasks[p.statuses[index][ordinal]], true
}

func (p renderProjection) ordinalForTask(status board.Status, id string) (int, bool) {
	index := statusIndexExact(status)
	if index < 0 || id == "" {
		return 0, false
	}
	ordinal, ok := p.ordinals[index][id]
	return ordinal, ok
}

func (p renderProjection) taskByID(id string) (board.Task, bool) {
	index, ok := p.taskIndexes[id]
	if !ok || index < 0 || index >= len(p.board.Tasks) {
		return board.Task{}, false
	}
	return p.board.Tasks[index], true
}

func selectedInclusiveLabels(labels, selected []string) []string {
	result := append([]string(nil), labels...)
	seen := make(map[string]struct{}, len(labels)+len(selected))
	for _, label := range labels {
		seen[label] = struct{}{}
	}
	for _, label := range selected {
		if _, ok := seen[label]; ok {
			continue
		}
		seen[label] = struct{}{}
		result = append(result, label)
	}
	slices.Sort(result)
	return result
}

func (p renderProjection) filterLabels() []string { return p.toolbarLabels }

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

func sameBoardSourceIdentity(left, right board.Board) bool {
	return left.Title == right.Title && len(left.Tasks) == len(right.Tasks) &&
		unsafe.SliceData(left.Tasks) == unsafe.SliceData(right.Tasks)
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
	return left.filterRevision == right.filterRevision &&
		left.projectName == right.projectName && left.projectAll == right.projectAll &&
		left.filter.Text == right.filter.Text && slices.Equal(left.filter.Tags, right.filter.Tags)
}

func (p *renderProjection) refreshOwnedBytes() {
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
	for _, ordinals := range p.ordinals {
		for id := range ordinals {
			estimate += uint64(len(id)) + uint64(unsafe.Sizeof(int(0)))
		}
	}
	for id := range p.taskIndexes {
		estimate += uint64(len(id)) + uint64(unsafe.Sizeof(int(0)))
	}
	for id := range p.sourceIndexes {
		estimate += uint64(len(id)) + uint64(unsafe.Sizeof(int(0)))
	}
	for _, label := range p.labels {
		estimate += uint64(len(label)) + uint64(unsafe.Sizeof(""))
	}
	for _, label := range p.toolbarLabels {
		estimate += uint64(len(label)) + uint64(unsafe.Sizeof(""))
	}
	p.ownedBytes = estimate
}

func (p renderProjection) ownedBytesEstimate() uint64 { return p.ownedBytes }
