package tui

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

func TestStartupSequencesWatcherBaselineVisibleFrameThenPollingAndGeometry(t *testing.T) {
	fixture := performanceBoard(120)
	reader := &sequenceBoardReader{results: []boardResult{{board: fixture}}}
	model := newTestRootModel(reader, stubVersionReader{version: 7}, "alice")

	baseline := singleCommandMessage(t, model.Init())
	if _, ok := baseline.(dataVersionMsg); !ok || reader.calls != 0 {
		t.Fatalf("startup first result = %T, board reads=%d; want watcher baseline before board load", baseline, reader.calls)
	}
	model, commands := model.updateWithCommands(baseline)
	if commands.followUp == nil || commands.geometry != nil || model.pollStarted {
		t.Fatalf("baseline commands = follow-up:%v geometry:%v polling:%t", commands.followUp, commands.geometry, model.pollStarted)
	}
	loaded, ok := singleCommandMessage(t, commands.followUp).(boardLoadedMsg)
	if !ok || loaded.generation == 0 || reader.calls != 1 {
		t.Fatalf("baseline follow-up = %#v, board reads=%d", loaded, reader.calls)
	}

	before := model.RenderPlanStats()
	model, commands = model.updateWithCommands(loaded)
	after := model.RenderPlanStats()
	if after.PublishedFrames != before.PublishedFrames+1 || !strings.Contains(model.View().Content, fixture.Title) {
		t.Fatalf("first board result did not install exact visible frame: before=%+v after=%+v", before, after)
	}
	if !model.pollStarted || commands.followUp == nil {
		t.Fatalf("polling did not start after first visible frame: started=%t command=%v", model.pollStarted, commands.followUp)
	}
	if model.current.geometry.unresolvedRecords > 0 && commands.geometry == nil {
		t.Fatal("offscreen worker did not start after first visible frame")
	}
}

func TestSettledWatcherLifecycleUsesFastPathWithoutChangingFrameOrRoutes(t *testing.T) {
	model := performanceModel(120, "", performanceWidth, performanceHeight)
	model.applyStyles(theme.NewWith(model.isDark, theme.TimingCollapsed))
	model.watcher = stubVersionReader{version: 7}
	model.haveVersion = true
	model.dataVersion = 7
	model.haveBoardSnapshot = true
	model.pollStarted = true
	model.loading = false
	model.rebuildRenderPlan(renderImpactAll)
	current := *model.current
	current.geometry.unresolvedRecords = max(current.geometry.unresolvedRecords, 1)
	current.worker.inFlight = false
	model.current = &current
	model.haveTemporalScheduleInput = false
	model.temporalDeadline = time.Time{}
	model.temporalSchedule = func(_ time.Duration, message temporalTickMsg) tea.Cmd {
		return func() tea.Msg { return message }
	}

	before := model.RenderPlanStats()
	beforeView := model.View()
	beforeRoute := model.pointerRoute()
	beforeKeyboard, beforeKeyboardOK := model.inputAdmission.keyboardSnapshot()

	model, pollCommands := model.updateWithCommands(pollTickMsg{})
	afterPoll := model.RenderPlanStats()
	if pollCommands.followUp == nil || pollCommands.geometry == nil || pollCommands.temporal == nil {
		t.Fatalf("watcher poll fast path commands = follow:%v geometry:%v temporal:%v", pollCommands.followUp,
			pollCommands.geometry, pollCommands.temporal)
	}
	if afterPoll.WatcherPollFastPaths != before.WatcherPollFastPaths+1 ||
		afterPoll.RenderImpactClassifications != before.RenderImpactClassifications ||
		afterPoll.WatcherLifecycleFallbacks != before.WatcherLifecycleFallbacks ||
		afterPoll.PublishedFrames != before.PublishedFrames {
		t.Fatalf("watcher poll counters/frame: before=%+v after=%+v", before, afterPoll)
	}
	version, ok := pollCommands.followUp().(dataVersionMsg)
	if !ok || version.err != nil || version.version != 7 {
		t.Fatalf("watcher poll result = %#v", version)
	}

	model, versionCommands := model.updateWithCommands(version)
	afterVersion := model.RenderPlanStats()
	if versionCommands.followUp == nil {
		t.Fatal("watcher version fast path dropped the poll successor")
	}
	if afterVersion.WatcherVersionFastPaths != before.WatcherVersionFastPaths+1 ||
		afterVersion.RenderImpactClassifications != before.RenderImpactClassifications ||
		afterVersion.WatcherLifecycleFallbacks != before.WatcherLifecycleFallbacks ||
		afterVersion.PublishedFrames != before.PublishedFrames {
		t.Fatalf("watcher version counters/frame: before=%+v after=%+v", before, afterVersion)
	}
	if model.View().Content != beforeView.Content || model.pointerRoute() != beforeRoute {
		t.Fatal("watcher fast path changed the immutable view or pointer route identity")
	}
	afterKeyboard, afterKeyboardOK := model.inputAdmission.keyboardSnapshot()
	if afterKeyboardOK != beforeKeyboardOK || !reflect.DeepEqual(afterKeyboard, beforeKeyboard) {
		t.Fatal("watcher fast path changed the keyboard admission identity")
	}
}

func TestWatcherLifecycleFastPathFallsBackOutsideSettledPlainBoard(t *testing.T) {
	tests := []struct {
		name    string
		message dataVersionMsg
		prepare func(*Model)
	}{
		{name: "result error", message: dataVersionMsg{err: errors.New("watcher failed")}},
		{name: "initial baseline", message: dataVersionMsg{version: 2}, prepare: func(model *Model) {
			model.haveVersion = false
		}},
		{name: "no watcher", message: dataVersionMsg{version: 2}, prepare: func(model *Model) {
			model.watcher = nil
		}},
		{name: "poll not started", message: dataVersionMsg{version: 2}, prepare: func(model *Model) {
			model.pollStarted = false
		}},
		{name: "no snapshot", message: dataVersionMsg{version: 2}, prepare: func(model *Model) {
			model.haveBoardSnapshot = false
		}},
		{name: "load error", message: dataVersionMsg{version: 2}, prepare: func(model *Model) {
			model.loadErr = errors.New("load failed")
		}},
		{name: "poll error recovery", message: dataVersionMsg{version: 2}, prepare: func(model *Model) {
			model.pollErr = errors.New("poll failed")
		}},
		{name: "lifted move", message: dataVersionMsg{version: 2}, prepare: func(model *Model) {
			if !model.move.begin(model.board, model.board.Tasks[0], boardStatuses[:], false) {
				t.Fatal("fixture could not lift a card")
			}
		}},
		{name: "write busy", message: dataVersionMsg{version: 2}, prepare: func(model *Model) {
			model.move.saving = true
		}},
		{name: "overlay", message: dataVersionMsg{version: 2}, prepare: func(model *Model) {
			model.helpOpen = true
		}},
		{name: "detail", message: dataVersionMsg{version: 2}, prepare: func(model *Model) {
			_ = model.detail.Open(model.board.Tasks[0])
		}},
		{name: "launch", message: dataVersionMsg{version: 2}, prepare: func(model *Model) {
			model.loading = true
			model.haveBoardSnapshot = false
		}},
		{name: "prepared projection", message: dataVersionMsg{version: 2}, prepare: func(model *Model) {
			prepared := model.current.projection
			model.preparedProjection = &prepared
		}},
		{name: "stopped", message: dataVersionMsg{version: 2}, prepare: func(model *Model) {
			model.stopped = true
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := performanceModel(120, "", performanceWidth, performanceHeight)
			model.applyStyles(theme.NewWith(model.isDark, theme.TimingCollapsed))
			model.watcher = stubVersionReader{version: 2}
			model.haveVersion = true
			model.dataVersion = 1
			model.haveBoardSnapshot = true
			model.pollStarted = true
			model.loading = false
			model.rebuildRenderPlan(renderImpactAll)
			if test.prepare != nil {
				test.prepare(&model)
			}
			before := model.RenderPlanStats()

			model, _ = model.updateWithCommands(test.message)
			after := model.RenderPlanStats()
			if after.WatcherLifecycleFallbacks != before.WatcherLifecycleFallbacks+1 ||
				after.WatcherPollFastPaths != before.WatcherPollFastPaths ||
				after.WatcherVersionFastPaths != before.WatcherVersionFastPaths ||
				after.RenderImpactClassifications != before.RenderImpactClassifications+1 {
				t.Fatalf("fallback/classifier counters: before=%+v after=%+v", before, after)
			}
		})
	}
}

func TestStrictlyStaleWatcherLoadUsesFastPathAndStartsNewestGeneration(t *testing.T) {
	currentBoard := performanceBoard(120)
	newestBoard := cloneBoard(currentBoard)
	newestBoard.Title = "Newest"
	reader := &sequenceBoardReader{results: []boardResult{{board: newestBoard}}}
	model := performanceModel(120, "", performanceWidth, performanceHeight)
	model.store = reader
	model.watcher = stubVersionReader{version: 26}
	model.haveVersion = true
	model.dataVersion = 26
	model.haveBoardSnapshot = true
	model.pollStarted = true
	model.loading = true
	model.loadGeneration = 26
	model.activeLoadGen = 2
	model.reloadPending = true
	model.rebuildRenderPlan(renderImpactAll)
	before := model.RenderPlanStats()
	beforeBoard := model.board
	beforeRoute := model.pointerRoute()
	beforeKeyboard, beforeKeyboardOK := model.inputAdmission.keyboardSnapshot()

	model, commands := model.updateWithCommands(boardLoadedMsg{board: currentBoard, generation: 2})
	after := model.RenderPlanStats()
	if after.WatcherStaleLoadFastPaths != before.WatcherStaleLoadFastPaths+1 ||
		after.WatcherLifecycleFallbacks != before.WatcherLifecycleFallbacks ||
		after.RenderImpactClassifications != before.RenderImpactClassifications ||
		after.PublishedFrames != before.PublishedFrames {
		t.Fatalf("stale-load fast-path counters/frame: before=%+v after=%+v", before, after)
	}
	if !model.loading || model.activeLoadGen != 26 || model.loadGeneration != 26 || model.reloadPending ||
		!sameBoardSourceIdentity(model.board, beforeBoard) {
		t.Fatalf("stale-load successor state = loading:%t active:%d requested:%d pending:%t board-same:%t",
			model.loading, model.activeLoadGen, model.loadGeneration, model.reloadPending,
			sameBoardSourceIdentity(model.board, beforeBoard))
	}
	if commands.followUp == nil {
		t.Fatal("stale-load fast path dropped the newest load successor")
	}
	loaded, ok := commands.followUp().(boardLoadedMsg)
	if !ok || loaded.generation != 26 || loaded.err != nil || !sameBoardSourceIdentity(loaded.board, newestBoard) {
		t.Fatalf("stale-load successor result = %#v", loaded)
	}
	if model.pointerRoute() != beforeRoute {
		t.Fatal("stale-load fast path changed pointer route identity")
	}
	afterKeyboard, afterKeyboardOK := model.inputAdmission.keyboardSnapshot()
	if afterKeyboardOK != beforeKeyboardOK || !reflect.DeepEqual(afterKeyboard, beforeKeyboard) {
		t.Fatal("stale-load fast path changed keyboard admission identity")
	}
}

func TestStrictlyStaleWatcherLoadFastPathPreservesDiscardSemantics(t *testing.T) {
	tests := []struct {
		name           string
		prepare        func(*Model)
		wantSuccessor  bool
		wantReloadPend bool
	}{
		{name: "stale error", prepare: func(model *Model) {
			model.loadErr = errors.New("existing load error")
		}, wantSuccessor: true},
		{name: "lifted move", prepare: func(model *Model) {
			if !model.move.begin(model.board, model.board.Tasks[0], boardStatuses[:], false) {
				t.Fatal("fixture could not lift a card")
			}
		}, wantSuccessor: true},
		{name: "write busy", prepare: func(model *Model) {
			model.move.saving = true
		}, wantReloadPend: true},
		{name: "no snapshot", prepare: func(model *Model) {
			model.haveBoardSnapshot = false
		}, wantSuccessor: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := performanceModel(17, "", performanceWidth, performanceHeight)
			model.store = stubBoardReader{board: cloneBoard(model.board)}
			model.loading = true
			model.loadGeneration = 4
			model.activeLoadGen = 2
			model.reloadPending = true
			if test.prepare != nil {
				test.prepare(&model)
			}
			before := model.RenderPlanStats()
			beforeBoard := model.board
			beforeLift := model.move.lifted

			model, commands := model.updateWithCommands(boardLoadedMsg{
				board: board.Board{Title: "discard me"}, err: errors.New("discard me"), generation: 2,
			})
			after := model.RenderPlanStats()
			if after.WatcherStaleLoadFastPaths != before.WatcherStaleLoadFastPaths+1 ||
				after.WatcherLifecycleFallbacks != before.WatcherLifecycleFallbacks ||
				after.RenderImpactClassifications != before.RenderImpactClassifications ||
				!sameBoardSourceIdentity(model.board, beforeBoard) || model.move.lifted != beforeLift {
				t.Fatalf("stale discard semantics: before=%+v after=%+v board-same:%t lift-same:%t",
					before, after, sameBoardSourceIdentity(model.board, beforeBoard), model.move.lifted == beforeLift)
			}
			if (commands.followUp != nil) != test.wantSuccessor || model.reloadPending != test.wantReloadPend {
				t.Fatalf("stale successor = command:%v pending:%t, want command:%t pending:%t",
					commands.followUp, model.reloadPending, test.wantSuccessor, test.wantReloadPend)
			}
		})
	}
}

func TestStaleWatcherLoadFastPathRejectsUnprovenLifecycleState(t *testing.T) {
	tests := []struct {
		name       string
		generation uint64
		prepare    func(*Model)
	}{
		{name: "zero generation", generation: 0},
		{name: "current generation", generation: 4},
		{name: "active mismatch", generation: 2, prepare: func(model *Model) { model.activeLoadGen = 3 }},
		{name: "not loading", generation: 2, prepare: func(model *Model) { model.loading = false }},
		{name: "stopped", generation: 2, prepare: func(model *Model) { model.stopped = true }},
		{name: "help surface", generation: 2, prepare: func(model *Model) { model.helpOpen = true }},
		{name: "detail surface", generation: 2, prepare: func(model *Model) {
			_ = model.detail.Open(model.board.Tasks[0])
		}},
		{name: "prepared projection", generation: 2, prepare: func(model *Model) {
			prepared := model.current.projection
			model.preparedProjection = &prepared
		}},
		{name: "no current plan", generation: 2, prepare: func(model *Model) { model.current = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := performanceModel(17, "", performanceWidth, performanceHeight)
			model.loading = true
			model.loadGeneration = 4
			model.activeLoadGen = 2
			model.reloadPending = true
			if test.prepare != nil {
				test.prepare(&model)
			}
			before := model.RenderPlanStats()

			model, _ = model.updateWithCommands(boardLoadedMsg{board: model.board, generation: test.generation})
			after := model.RenderPlanStats()
			if after.WatcherStaleLoadFastPaths != before.WatcherStaleLoadFastPaths ||
				after.RenderImpactClassifications != before.RenderImpactClassifications+1 {
				t.Fatalf("unproven stale load entered fast path: before=%+v after=%+v", before, after)
			}
		})
	}
}

func TestRefreshDropsOlderGenerationAndStartsOneNewestSuccessor(t *testing.T) {
	v1 := board.Board{Title: "V1", Tasks: []board.Task{{ID: "one", Title: "V1", Status: board.StatusTodo}}}
	v2 := board.Board{Title: "V2", Tasks: []board.Task{{ID: "one", Title: "V2", Status: board.StatusTodo}}}
	v3 := board.Board{Title: "V3", Tasks: []board.Task{{ID: "one", Title: "V3", Status: board.StatusTodo}}}
	reader := &sequenceBoardReader{results: []boardResult{{board: v2}, {board: v3}}}
	model := newTestRootModel(reader, stubVersionReader{}, "alice")
	model.board = v1
	model.haveBoardSnapshot = true
	model.haveVersion = true
	model.dataVersion = 1
	model.loading = false
	model.rebuildRenderPlan(renderImpactAll)

	model, commands := model.updateWithCommands(dataVersionMsg{version: 2})
	if commands.followUp == nil {
		t.Fatal("version 2 did not start a board load")
	}
	v2Result, ok := singleCommandMessage(t, commands.followUp).(boardLoadedMsg)
	if !ok || v2Result.generation == 0 {
		t.Fatalf("version 2 result = %#v", v2Result)
	}

	model, commands = model.updateWithCommands(dataVersionMsg{version: 3})
	if commands.followUp != nil || !model.reloadPending {
		t.Fatalf("version 3 while loading = command:%v pending:%t", commands.followUp, model.reloadPending)
	}
	beforeStale := model.RenderPlanStats()
	model, commands = model.updateWithCommands(v2Result)
	afterStale := model.RenderPlanStats()
	if model.board.Title != "V1" || afterStale.PublishedFrames != beforeStale.PublishedFrames {
		t.Fatalf("older result installed: board=%q before=%+v after=%+v", model.board.Title, beforeStale, afterStale)
	}
	if commands.followUp == nil || !model.loading || model.reloadPending {
		t.Fatalf("older result successor = command:%v loading:%t pending:%t", commands.followUp, model.loading, model.reloadPending)
	}

	newest, ok := singleCommandMessage(t, commands.followUp).(boardLoadedMsg)
	if !ok || newest.generation <= v2Result.generation {
		t.Fatalf("newest result = %#v, older generation=%d", newest, v2Result.generation)
	}
	beforeNewest := model.RenderPlanStats()
	model, _ = model.updateWithCommands(newest)
	afterNewest := model.RenderPlanStats()
	if model.board.Title != "V3" || afterNewest.PublishedFrames != beforeNewest.PublishedFrames+1 {
		t.Fatalf("newest result = board:%q before=%+v after=%+v", model.board.Title, beforeNewest, afterNewest)
	}
}

func TestOutOfScopeNonmatchingRefreshReconcilesSourceWithoutPublishing(t *testing.T) {
	for _, count := range []int{120, 500, 1000} {
		t.Run(integerLabel(count), func(t *testing.T) {
			model := performanceModel(count, "keep17", performanceWidth, performanceHeight)
			alphaBoard := cloneBoard(model.board)
			for index := range alphaBoard.Tasks {
				alphaBoard.Tasks[index].Tags = append(alphaBoard.Tasks[index].Tags, "project::alpha")
			}
			model.board = alphaBoard
			model.projects = projectSwitcher{name: "alpha"}
			model.rebuildRenderPlan(renderImpactAll)
			before := model.RenderPlanStats()
			beforeView := model.View()
			beforeGeometry := model.current.geometry.generation
			beforeTags := &model.current.projection.tasks[0].task.Tags[0]

			nextBoard := cloneBoard(model.board)
			nextBoard.Tasks = append(nextBoard.Tasks, board.Task{
				ID: "nonmatching", Title: "ordinary external insert", Desc: "outside active query",
				Status: board.StatusTodo, Tags: []string{"performance", "dev", "project::beta"},
			})
			updated, command := model.Update(boardLoadedMsg{board: nextBoard})
			model = updated.(Model)
			after := model.RenderPlanStats()
			if command != nil {
				t.Fatalf("nonmatching refresh scheduled %v", command)
			}
			if after.PublishedFrames != before.PublishedFrames || after.InstalledSnapshotID != before.InstalledSnapshotID {
				t.Fatalf("nonmatching refresh published: before=%+v after=%+v", before, after)
			}
			if model.View().Content != beforeView.Content || model.current.geometry.generation != beforeGeometry {
				t.Fatal("nonmatching refresh changed current view or geometry")
			}
			if !model.current.projection.matchesSourceIdentity(nextBoard) || len(model.current.projection.tasks) != count+1 {
				t.Fatal("nonmatching refresh did not reconcile the source projection")
			}
			if &model.current.projection.tasks[0].task.Tags[0] != beforeTags || after.TaskDerivationBuilds != before.TaskDerivationBuilds+1 {
				t.Fatalf("derivation reuse/builds = reused:%t builds:%d", &model.current.projection.tasks[0].task.Tags[0] == beforeTags,
					after.TaskDerivationBuilds-before.TaskDerivationBuilds)
			}
		})
	}
}

func TestNonmatchingRefreshPublishesRenderedProjectionMetadataChanges(t *testing.T) {
	for _, count := range []int{120, 500, 1000} {
		t.Run(integerLabel(count), func(t *testing.T) {
			t.Run("projected-total", func(t *testing.T) {
				model := performanceModel(count, "keep17", performanceWidth, performanceHeight)
				nextBoard := cloneBoard(model.board)
				nextBoard.Tasks = append(nextBoard.Tasks, board.Task{
					ID: "nonmatching-total", Title: "ordinary external insert", Desc: "outside active query",
					Status: board.StatusTodo, Tags: []string{"performance", "dev"},
				})
				before := model.RenderPlanStats()
				updated, _ := model.Update(boardLoadedMsg{board: nextBoard})
				model = updated.(Model)
				after := model.RenderPlanStats()
				if after.PublishedFrames != before.PublishedFrames+1 {
					t.Fatalf("total metadata refresh frames = %d, want 1", after.PublishedFrames-before.PublishedFrames)
				}
				want := "17 of " + integerLabel(count+1) + " cards"
				if !strings.Contains(model.View().Content, want) {
					t.Fatalf("total metadata refresh missing %q", want)
				}
			})

			t.Run("toolbar-label", func(t *testing.T) {
				model := performanceModel(count, "keep17", performanceWidth, performanceHeight)
				nextBoard := cloneBoard(model.board)
				changed := false
				for index := range nextBoard.Tasks {
					if !strings.Contains(nextBoard.Tasks[index].Title, "keep17") {
						nextBoard.Tasks[index].Tags = append(nextBoard.Tasks[index].Tags, "new-filter-label")
						changed = true
						break
					}
				}
				if !changed {
					t.Fatal("fixture has no task excluded by the active text filter")
				}
				before := model.RenderPlanStats()
				updated, _ := model.Update(boardLoadedMsg{board: nextBoard})
				model = updated.(Model)
				after := model.RenderPlanStats()
				if after.PublishedFrames != before.PublishedFrames+1 {
					t.Fatalf("label metadata refresh frames = %d, want 1", after.PublishedFrames-before.PublishedFrames)
				}
				if !strings.Contains(model.View().Content, "new-filter-label") {
					t.Fatal("label metadata refresh did not publish the new toolbar label")
				}
			})
		})
	}
}

func TestDisappearingSelectedLabelReconcilesWithoutPublishingUnchangedToolbar(t *testing.T) {
	fixture := board.Board{Title: "Labels", Tasks: []board.Task{
		{ID: "rare-source", Title: "ordinary one", Status: board.StatusTodo, Tags: []string{"rare", "dev"}},
		{ID: "other", Title: "ordinary two", Status: board.StatusDoing, Tags: []string{"dev"}},
	}}
	model := NewModel(stubBoardReader{board: fixture}, nil, "alice")
	updated, _ := model.Update(boardLoadedMsg{board: fixture})
	model = updated.(Model)
	model.filter.restore(boardFilter{Text: "matches-nothing", Tags: []string{"rare"}})
	model.rebuildRenderPlan(renderImpactAll)
	if !slices.Contains(model.current.projection.labels, "rare") ||
		!slices.Contains(model.current.projection.toolbarLabels, "rare") {
		t.Fatal("fixture did not expose the selected source label in the toolbar")
	}

	nextBoard := cloneBoard(fixture)
	nextBoard.Tasks[0].Tags = []string{"dev"}
	before := model.RenderPlanStats()
	beforeView := model.View()
	beforeHits := append([]boardHit(nil), model.current.semantics.hits...)
	beforeGeometry := model.current.geometry.generation
	updated, command := model.Update(boardLoadedMsg{board: nextBoard})
	model = updated.(Model)
	after := model.RenderPlanStats()

	if command != nil {
		t.Fatalf("selected-label source refresh scheduled %v", command)
	}
	if after.PublishedFrames != before.PublishedFrames || after.InstalledSnapshotID != before.InstalledSnapshotID {
		t.Fatalf("selected-label source refresh published: before=%+v after=%+v", before, after)
	}
	if model.View().Content != beforeView.Content || model.current.geometry.generation != beforeGeometry ||
		!reflect.DeepEqual(model.current.semantics.hits, beforeHits) {
		t.Fatal("selected-label source refresh changed retained visible, hit, or geometry state")
	}
	if slices.Contains(model.current.projection.labels, "rare") {
		t.Fatal("reconciled projection retained the disappeared raw source label")
	}
	if !slices.Contains(model.current.projection.toolbarLabels, "rare") {
		t.Fatal("reconciled projection dropped the still-selected toolbar label")
	}
	if !model.current.projection.matchesSourceIdentity(nextBoard) {
		t.Fatal("selected-label source refresh did not reconcile source identity")
	}
}

func TestMatchingRefreshPublishesOnceAndPreservesTaskAnchor(t *testing.T) {
	for _, count := range []int{120, 500, 1000} {
		t.Run(integerLabel(count), func(t *testing.T) {
			model := performanceModel(count, "keep17", performanceWidth, performanceHeight)
			selected := model.current.projection.board.Tasks[min(2, len(model.current.projection.board.Tasks)-1)]
			if !model.boardView.focusTask(model.current.projection.board, selected.ID) {
				t.Fatalf("could not focus %q", selected.ID)
			}
			model.rebuildRenderPlan(renderImpactAll)
			before := model.RenderPlanStats()

			nextBoard := cloneBoard(model.board)
			for index := range nextBoard.Tasks {
				if nextBoard.Tasks[index].ID == selected.ID {
					nextBoard.Tasks[index].Title = "keep17 matching external change"
					break
				}
			}
			updated, _ := model.Update(boardLoadedMsg{board: nextBoard})
			model = updated.(Model)
			after := model.RenderPlanStats()
			focused, ok := model.selectedTask()
			if after.PublishedFrames != before.PublishedFrames+1 || !ok || focused.ID != selected.ID {
				t.Fatalf("matching refresh = frames:%d focused:%+v,%t", after.PublishedFrames-before.PublishedFrames, focused, ok)
			}
			if after.TaskDerivationBuilds != before.TaskDerivationBuilds+1 {
				t.Fatalf("matching refresh derived %d tasks, want 1", after.TaskDerivationBuilds-before.TaskDerivationBuilds)
			}
		})
	}
}

func TestTwentyFiveTaskRefreshBurstSettlesNewestGenerationOnce(t *testing.T) {
	for _, count := range []int{120, 500, 1000} {
		t.Run(integerLabel(count), func(t *testing.T) {
			model := performanceModel(count, "keep17", performanceWidth, performanceHeight)
			selected := model.current.projection.board.Tasks[min(2, len(model.current.projection.board.Tasks)-1)]
			if !model.boardView.focusTask(model.current.projection.board, selected.ID) {
				t.Fatalf("could not focus %q", selected.ID)
			}
			model.rebuildRenderPlan(renderImpactAll)

			boards := make([]board.Board, 25)
			for inserted := range boards {
				boards[inserted] = cloneBoard(model.board)
				for index := 0; index <= inserted; index++ {
					boards[inserted].Tasks = append(boards[inserted].Tasks, board.Task{
						ID: "burst-" + integerLabel(index), Title: "keep17 burst insert " + integerLabel(index),
						Status: board.StatusTodo, Tags: []string{"performance", "dev"},
					})
				}
			}
			reader := &sequenceBoardReader{results: []boardResult{{board: boards[0]}, {board: boards[len(boards)-1]}}}
			model.store = reader
			model.watcher = stubVersionReader{}
			model.haveVersion = true
			model.dataVersion = 1
			model.pollStarted = true
			before := model.RenderPlanStats()

			model, commands := model.updateWithCommands(dataVersionMsg{version: 2})
			firstLoad := boardLoadFromBatch(t, commands.followUp)
			for version := int64(3); version <= 26; version++ {
				model, _ = model.updateWithCommands(dataVersionMsg{version: version})
			}
			if got := model.RenderPlanStats().PublishedFrames; got != before.PublishedFrames {
				t.Fatalf("version burst published %d frames", got-before.PublishedFrames)
			}

			stale, ok := firstLoad().(boardLoadedMsg)
			if !ok {
				t.Fatalf("first load result = %T", firstLoad())
			}
			model, commands = model.updateWithCommands(stale)
			if got := model.RenderPlanStats().PublishedFrames; got != before.PublishedFrames {
				t.Fatalf("stale burst result published %d frames", got-before.PublishedFrames)
			}
			if commands.followUp == nil || reader.calls != 1 {
				t.Fatalf("stale burst successor = command:%v reads:%d", commands.followUp, reader.calls)
			}

			newest, ok := singleCommandMessage(t, commands.followUp).(boardLoadedMsg)
			if !ok {
				t.Fatalf("newest load result = %T", newest)
			}
			model, _ = model.updateWithCommands(newest)
			after := model.RenderPlanStats()
			focused, found := model.selectedTask()
			if after.PublishedFrames != before.PublishedFrames+1 || reader.calls != 2 {
				t.Fatalf("burst settlement = frames:%d reads:%d", after.PublishedFrames-before.PublishedFrames, reader.calls)
			}
			if !found || focused.ID != selected.ID {
				t.Fatalf("burst settlement focus = %+v,%t; want %q", focused, found, selected.ID)
			}
			if after.TaskDerivationBuilds != before.TaskDerivationBuilds+25 {
				t.Fatalf("burst settlement derived %d tasks, want 25", after.TaskDerivationBuilds-before.TaskDerivationBuilds)
			}
			if after.WatcherVersionFastPaths != before.WatcherVersionFastPaths+25 ||
				after.WatcherPollFastPaths != before.WatcherPollFastPaths ||
				after.WatcherStaleLoadFastPaths != before.WatcherStaleLoadFastPaths+1 ||
				after.WatcherLifecycleFallbacks != before.WatcherLifecycleFallbacks ||
				after.RenderImpactClassifications != before.RenderImpactClassifications+1 {
				t.Fatalf("burst lifecycle/classifier counters: before=%+v after=%+v", before, after)
			}
			if !model.current.projection.matchesSourceIdentity(boards[len(boards)-1]) {
				t.Fatal("burst settlement did not install newest source generation")
			}
		})
	}
}
