package tui

import (
	"math/bits"
	"slices"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/board"
)

func TestLargeBoardRetainsCompactGeometryAndVisibleHits(t *testing.T) {
	model := performanceModel(120, "", performanceWidth, performanceHeight)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: performanceWidth - 1, Height: performanceHeight})
	model = updated.(Model)
	stats := model.RenderPlanStats()
	if stats.LayoutRecords != 120 {
		t.Fatalf("layout records = %d, want one compact record per projected task", stats.LayoutRecords)
	}
	if stats.ExactLayoutRecords == 0 || stats.EstimatedLayoutRecords == 0 {
		t.Fatalf("initial geometry exact=%d estimated=%d total=%d, want exact visible and estimated offscreen records",
			stats.ExactLayoutRecords, stats.EstimatedLayoutRecords, stats.LayoutRecords)
	}
	copy := model
	copy.renderingProjection = &model.current.projection
	copy.renderingGeometry = &model.current.geometry
	snapshot := copy.renderRetainedSnapshot()
	visibleTasks := map[string]struct{}{}
	for _, hit := range snapshot.hitRegions {
		if hit.taskID != "" {
			visibleTasks[hit.taskID] = struct{}{}
		}
	}
	if len(visibleTasks) == 0 || len(visibleTasks) >= int(stats.LayoutRecords) {
		t.Fatalf("visible hit tasks = %d for %d layout records", len(visibleTasks), stats.LayoutRecords)
	}
	if stats.RetainedPlanOwnedBytesEstimate <= stats.ContentBytes {
		t.Fatalf("retained memory estimate %d did not charge geometry above %d content bytes",
			stats.RetainedPlanOwnedBytesEstimate, stats.ContentBytes)
	}
}

func TestGeometryWorkerConvergesToColdRenderer(t *testing.T) {
	model := performanceModel(120, "", performanceWidth, performanceHeight)
	model = settleGeometry(t, model)
	stats := model.RenderPlanStats()
	if stats.EstimatedLayoutRecords != 0 || stats.ExactLayoutRecords != stats.LayoutRecords {
		t.Fatalf("settled geometry exact=%d estimated=%d total=%d",
			stats.ExactLayoutRecords, stats.EstimatedLayoutRecords, stats.LayoutRecords)
	}
	if stats.GeometryWorkerSlices == 0 {
		t.Fatal("offscreen geometry converged without a worker slice")
	}
	if got, want := model.View().Content, model.renderColdView().Content; got != want {
		t.Fatal("settled retained frame differs from the cold renderer")
	}
}

func TestGeometryWorkerReturnsPreparedSnapshotAndInstallIsConstantWork(t *testing.T) {
	for _, count := range []int{120, 1000} {
		t.Run(integerLabel(count), func(t *testing.T) {
			model := performanceModel(count, "", performanceWidth, performanceHeight)
			updated, _ := model.Update(tea.WindowSizeMsg{Width: performanceWidth - 1, Height: performanceHeight})
			model = updated.(Model)
			plan := *model.current
			plan.worker = geometryWorkerState{}
			model.current = &plan
			command := model.startGeometryWorker()
			if command == nil {
				t.Fatal("large board had no offscreen geometry command")
			}
			message := command().(geometryBatchMsg)
			if message.measured == 0 || message.prepared.generation != model.current.geometry.generation {
				t.Fatalf("prepared slice measured=%d generation=%d, want useful current work",
					message.measured, message.prepared.generation)
			}
			if message.prepared.layoutRecords != model.current.geometry.layoutRecords ||
				message.prepared.exactRecords != model.current.geometry.exactRecords+message.measured ||
				message.prepared.unresolvedRecords != model.current.geometry.unresolvedRecords-message.measured {
				t.Fatalf("prepared counters total=%d exact=%d unresolved=%d, before=%+v measured=%d",
					message.prepared.layoutRecords, message.prepared.exactRecords, message.prepared.unresolvedRecords,
					model.RenderPlanStats(), message.measured)
			}
			if message.prepared.ownedBytesEstimate() == 0 {
				t.Fatal("worker did not prepare the geometry memory estimate")
			}
			for _, column := range message.prepared.columns {
				if column.index.length == 0 {
					continue
				}
				for ordinal := 0; ordinal < column.index.length; ordinal++ {
					record := column.index.recordAt(ordinal)
					want := column.index.prefixAt(ordinal) + max(record.height, 0)
					if ordinal+1 < column.index.length {
						want += column.gap
					}
					if column.index.prefixAt(ordinal+1) != want {
						t.Fatalf("prepared prefix[%d]=%d, want %d", ordinal+1, column.index.prefixAt(ordinal+1), want)
					}
				}
			}

			before := model.RenderPlanStats()
			updated, _ = model.Update(message)
			model = updated.(Model)
			after := model.RenderPlanStats()
			if after.GeometrySnapshotInstalls != before.GeometrySnapshotInstalls+1 || after.MaxGeometryInstallUnits != 1 {
				t.Fatalf("geometry install counters=%+v, before=%+v; Update did record-wise install work", after, before)
			}
		})
	}
}

func TestSemanticSnapshotDetectsSameLookingTaskIdentity(t *testing.T) {
	model := performanceModel(120, "", performanceWidth, performanceHeight)
	projection := model.current.projection
	projection.board.Tasks = append([]board.Task(nil), projection.board.Tasks...)
	column := model.current.geometry.columns[statusIndexExact(board.StatusTodo)]
	if column.index.length == 0 {
		t.Fatal("fixture has no visible todo card")
	}
	index := column.index.recordAt(0).projectionIndex
	projection.board.Tasks[index].ID = "same-looking-replacement"
	copy := model
	copy.renderingProjection = &projection
	copy.renderingGeometry = &model.current.geometry
	snapshot := copy.renderRetainedSnapshot()
	if snapshot.view.Content != model.View().Content {
		t.Fatal("identity-only replacement unexpectedly changed terminal content")
	}
	if model.current.semanticallyMatches(snapshot) {
		t.Fatal("same-looking replacement reused stale task hit identities")
	}
}

func TestGeometryConvergencePublishesSameContentMaxScrollChange(t *testing.T) {
	model := performanceModel(120, "", performanceWidth, performanceHeight)
	geometry, snapshot := sameContentMaxScrollGeometry(t, model)
	if model.current.semanticallyMatches(snapshot) {
		t.Fatal("maxScroll change was omitted from snapshot semantics")
	}
	plan := *model.current
	plan.worker = geometryWorkerState{inFlight: true, generation: geometry.generation, sequence: 41}
	model.current = &plan
	sourceRootRevision := geometry.rootRevision
	geometry.rootRevision++
	before := model.RenderPlanStats()
	updated, _ := model.Update(geometryBatchMsg{
		generation: geometry.generation, sourceRootRevision: sourceRootRevision,
		sequence: 41, complete: true, prepared: geometry,
	})
	model = updated.(Model)
	after := model.RenderPlanStats()
	if after.PublishedFrames != before.PublishedFrames+1 || after.InstalledSnapshotID != before.InstalledSnapshotID+1 {
		t.Fatalf("semantic geometry publication before=%+v after=%+v", before, after)
	}
}

func TestGeometryConvergenceKeepsOffscreenOnlyProgressFrameFree(t *testing.T) {
	model := performanceModel(120, "", performanceWidth, performanceHeight)
	geometry := model.current.geometry.deepClone()
	column := &geometry.columns[statusIndexExact(board.StatusTodo)]
	if column.index.length < 4 {
		t.Fatal("fixture has too few todo cards")
	}
	last := column.index.length - 1
	record := column.index.recordAt(last)
	record.height++
	column.index, _ = column.index.withRecord(last, record)
	record = column.index.recordAt(last - 1)
	record.height--
	column.index, _ = column.index.withRecord(last-1, record)
	geometry.refreshDerived()
	copy := model
	copy.renderingProjection = &model.current.projection
	copy.renderingGeometry = &geometry
	snapshot := copy.renderRetainedSnapshot()
	if !model.current.semanticallyMatches(snapshot) {
		t.Fatal("balanced offscreen geometry changed visible or input semantics")
	}
	plan := *model.current
	plan.worker = geometryWorkerState{inFlight: true, generation: geometry.generation, sequence: 42}
	model.current = &plan
	sourceRootRevision := geometry.rootRevision
	geometry.rootRevision++
	before := model.RenderPlanStats()
	updated, _ := model.Update(geometryBatchMsg{
		generation: geometry.generation, sourceRootRevision: sourceRootRevision,
		sequence: 42, complete: true, prepared: geometry,
	})
	model = updated.(Model)
	after := model.RenderPlanStats()
	if after.PublishedFrames != before.PublishedFrames || after.GeometrySnapshotInstalls != before.GeometrySnapshotInstalls+1 {
		t.Fatalf("offscreen-only convergence frames=%d installs=%d, before=%+v",
			after.PublishedFrames-before.PublishedFrames, after.GeometrySnapshotInstalls-before.GeometrySnapshotInstalls, before)
	}
}

func TestOrdinaryInputVisitsOnlyVisibleOverscanRecords(t *testing.T) {
	for _, count := range []int{120, 500, 1000} {
		t.Run(integerLabel(count), func(t *testing.T) {
			model := performanceModel(count, "", performanceWidth, performanceHeight)
			assertBoundedRender := func(name string, message tea.Msg) {
				t.Helper()
				before := model.RenderPlanStats()
				updated, _ := model.Update(message)
				model = updated.(Model)
				after := model.RenderPlanStats()
				visited := after.RenderedCardRecords - before.RenderedCardRecords
				hits := after.NavigationArtifactHits - before.NavigationArtifactHits
				misses := after.NavigationArtifactMisses - before.NavigationArtifactMisses
				limit := visibleOverscanRecords(model)
				if visited != misses || visited > limit || visited == 0 && hits == 0 {
					t.Fatalf("%s rendered=%d hits=%d misses=%d, visible+overscan bound %d",
						name, visited, hits, misses, limit)
				}
				if after.SourceTaskComparisons != before.SourceTaskComparisons {
					t.Fatalf("%s compared %d source tasks on an ordinary input",
						name, after.SourceTaskComparisons-before.SourceTaskComparisons)
				}
			}

			assertBoundedRender("selection", tea.KeyPressMsg{Code: tea.KeyDown})
			var cardHit, scrollHit boardHit
			for _, hit := range model.current.semantics.hits {
				if cardHit.taskID == "" && hit.taskID != "" {
					cardHit = hit
				}
				if scrollHit.maxScroll == 0 && hit.taskID == "" && hit.maxScroll > 0 {
					scrollHit = hit
				}
			}
			if cardHit.taskID == "" || scrollHit.maxScroll == 0 {
				t.Fatal("fixture did not expose hover and scroll targets")
			}
			hover := tea.MouseMotionMsg{X: cardHit.x0, Y: cardHit.y0, Button: tea.MouseNone}
			if command := model.View().OnMouse(hover); command != nil {
				t.Fatal("visible card hover escaped the synchronous mailbox")
			}
			assertBoundedRender("hover", hover)
			assertBoundedRender("scroll", boardColumnScrolledMsg{
				status: scrollHit.status, offset: min(scrollHit.scroll+3, scrollHit.maxScroll), anchor: scrollHit.scrollDown,
			})
		})
	}
}

func TestVerticalNavigationReusesExactCardArtifacts(t *testing.T) {
	model := performanceModel(120, "", performanceWidth, performanceHeight)
	before := model.RenderPlanStats()

	updated, _ := model.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	model = updated.(Model)
	after := model.RenderPlanStats()

	hits := after.NavigationArtifactHits - before.NavigationArtifactHits
	misses := after.NavigationArtifactMisses - before.NavigationArtifactMisses
	rendered := after.RenderedCardRecords - before.RenderedCardRecords
	if hits == 0 || misses == 0 || rendered != misses || rendered >= uint64(visibleOverscanRecords(model)) {
		t.Fatalf("vertical artifact reuse hits=%d misses=%d rendered=%d overscan=%d",
			hits, misses, rendered, visibleOverscanRecords(model))
	}
	if after.NavigationArtifactPublications != before.NavigationArtifactPublications+1 {
		t.Fatalf("artifact publications=%d, want one", after.NavigationArtifactPublications-before.NavigationArtifactPublications)
	}
	assertPerformanceColdOracleParity(t, model)
}

func TestHorizontalNavigationFallsBackFromCardArtifacts(t *testing.T) {
	model := performanceModel(120, "", performanceWidth, performanceHeight)
	before := model.RenderPlanStats()

	updated, _ := model.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	model = updated.(Model)
	after := model.RenderPlanStats()

	if after.NavigationArtifactFallbacks != before.NavigationArtifactFallbacks+1 {
		t.Fatalf("horizontal artifact fallbacks=%d, want one",
			after.NavigationArtifactFallbacks-before.NavigationArtifactFallbacks)
	}
	if hits := after.NavigationArtifactHits - before.NavigationArtifactHits; hits != 0 {
		t.Fatalf("horizontal navigation reused %d artifacts across status selection", hits)
	}
	if misses, rendered := after.NavigationArtifactMisses-before.NavigationArtifactMisses,
		after.RenderedCardRecords-before.RenderedCardRecords; misses == 0 || rendered != misses {
		t.Fatalf("horizontal fallback misses=%d rendered=%d", misses, rendered)
	}
	assertPerformanceColdOracleParity(t, model)
}

func TestUsefulWheelReusesOverlapAndRegeneratesSnapshotTopology(t *testing.T) {
	model := performanceModel(120, "", performanceWidth, performanceHeight)
	columnIndex := statusIndexExact(board.StatusTodo)
	column := model.current.geometry.columns[columnIndex]
	start := column.windowStart(model.boardView, columnIndex, model.currentProjection())
	target := min(start+3, max(column.totalRows()-column.contentHeight, 0))
	if target == start {
		t.Fatal("wheel fixture had no useful scroll target")
	}
	previous := model.View()
	before := model.RenderPlanStats()

	updated, _ := model.Update(boardColumnScrolledMsg{
		status: board.StatusTodo,
		offset: target,
		anchor: column.anchorAt(target, model.currentProjection()),
	})
	model = updated.(Model)
	after := model.RenderPlanStats()

	if hits := after.NavigationArtifactHits - before.NavigationArtifactHits; hits == 0 {
		t.Fatal("useful wheel reused no overlapping artifacts")
	}
	if misses, rendered := after.NavigationArtifactMisses-before.NavigationArtifactMisses,
		after.RenderedCardRecords-before.RenderedCardRecords; rendered != misses {
		t.Fatalf("wheel misses=%d rendered=%d", misses, rendered)
	}
	if previous.OnMouse == nil || model.View().OnMouse == nil ||
		after.InstalledSnapshotID != before.InstalledSnapshotID+1 {
		t.Fatal("wheel did not install a fresh routed pointer snapshot")
	}
	if model.current.semantics.columns[columnIndex].windowStart == start {
		t.Fatal("wheel retained stale absolute column topology")
	}
	assertPerformanceColdOracleParity(t, model)
}

func TestCardArtifactRetentionPrunesDisjointWindows(t *testing.T) {
	model := performanceModel(1000, "", 80, 24)
	columnIndex := statusIndexExact(board.StatusTodo)
	projection := model.currentProjection()
	column := model.current.geometry.columns[columnIndex]
	maximum := max(column.totalRows()-column.contentHeight, 0)
	if maximum == 0 {
		t.Fatal("artifact pruning fixture had no disjoint window")
	}

	assertBounded := func(label string) map[string]struct{} {
		t.Helper()
		limit := int(visibleOverscanRecords(model))
		count := len(model.current.artifacts.records)
		if count == 0 || count > limit {
			t.Fatalf("%s retained %d artifacts, overscan bound %d", label, count, limit)
		}
		ids := make(map[string]struct{}, count)
		for key := range model.current.artifacts.records {
			if _, duplicate := ids[key.taskID]; duplicate {
				t.Fatalf("%s retained multiple visual variants for %q", label, key.taskID)
			}
			ids[key.taskID] = struct{}{}
		}
		if model.RenderPlanStats().RetainedPlanOwnedBytesEstimate > 64<<20 {
			t.Fatalf("%s retained estimate exceeded 64 MiB", label)
		}
		return ids
	}

	first := assertBounded("first")
	for _, target := range []int{maximum, 0, maximum} {
		updated, _ := model.Update(boardColumnScrolledMsg{
			status: board.StatusTodo,
			from:   model.boardView.scrolls[columnIndex],
			offset: target,
			anchor: column.anchorAt(target, projection),
			max:    maximum,
		})
		model = updated.(Model)
		assertPerformanceColdOracleParity(t, model)
	}
	last := assertBounded("last")
	for id := range first {
		if _, retained := last[id]; retained {
			t.Fatalf("disjoint window retained stale artifact %q", id)
		}
	}
}

func TestMatchingRefreshReusesStableTaskArtifactsWithFreshTopology(t *testing.T) {
	model := performanceModel(120, "keep17", performanceWidth, performanceHeight)
	previous := model.View()
	before := model.RenderPlanStats()
	nextBoard := appendPerformanceTasks(model.board, 1, true)

	updated, _ := model.Update(boardLoadedMsg{board: nextBoard})
	model = updated.(Model)
	after := model.RenderPlanStats()

	hits := after.NavigationArtifactHits - before.NavigationArtifactHits
	misses := after.NavigationArtifactMisses - before.NavigationArtifactMisses
	if hits == 0 || after.NavigationArtifactFallbacks != before.NavigationArtifactFallbacks ||
		after.RenderedCardRecords-before.RenderedCardRecords != misses {
		t.Fatalf("matching refresh artifacts hits=%d misses=%d fallbacks=%d rendered=%d",
			hits, misses, after.NavigationArtifactFallbacks-before.NavigationArtifactFallbacks,
			after.RenderedCardRecords-before.RenderedCardRecords)
	}
	assertArtifactColdSurfaceParity(t, model)
	if previous.OnMouse == nil || model.View().OnMouse == nil {
		t.Fatal("matching refresh did not retain fresh pointer surfaces")
	}
	raw := tea.MouseMotionMsg{X: 0, Y: 0, Button: tea.MouseNone}
	if command := previous.OnMouse(raw); command != nil {
		t.Fatal("previous refresh route escaped its mailbox")
	}
	_, oldRoute, oldResult := model.pointerMailbox.take(raw)
	if oldResult != pointerMailboxMatched || oldRoute.snapshot == after.InstalledSnapshotID {
		t.Fatalf("previous refresh route=%+v result=%v current=%d", oldRoute, oldResult, after.InstalledSnapshotID)
	}
	if command := model.View().OnMouse(raw); command != nil {
		t.Fatal("current refresh route escaped its mailbox")
	}
	_, currentRoute, currentResult := model.pointerMailbox.take(raw)
	if currentResult != pointerMailboxMatched || currentRoute.snapshot != after.InstalledSnapshotID ||
		currentRoute.snapshot == oldRoute.snapshot {
		t.Fatalf("current refresh route=%+v previous=%+v result=%v", currentRoute, oldRoute, currentResult)
	}
}

func TestChangedRefreshTaskMissesStableArtifactIdentity(t *testing.T) {
	model := performanceModel(120, "keep17", performanceWidth, performanceHeight)
	changedBoard := cloneBoard(model.board)
	visible := model.current.projection.board.Tasks[0]
	sourceIndex := model.current.projection.sourceIndexes[visible.ID]
	changedBoard.Tasks[sourceIndex].Title += " externally changed"
	beforeRevision, _ := model.current.projection.taskRenderRevision(visible.ID)
	before := model.RenderPlanStats()

	updated, _ := model.Update(boardLoadedMsg{board: changedBoard})
	model = updated.(Model)
	after := model.RenderPlanStats()
	afterRevision, ok := model.current.projection.taskRenderRevision(visible.ID)
	if !ok || afterRevision <= beforeRevision {
		t.Fatalf("changed task revision=%d, %t; before=%d", afterRevision, ok, beforeRevision)
	}
	if misses := after.NavigationArtifactMisses - before.NavigationArtifactMisses; misses == 0 ||
		after.RenderedCardRecords-before.RenderedCardRecords != misses {
		t.Fatalf("changed refresh misses=%d rendered=%d", misses,
			after.RenderedCardRecords-before.RenderedCardRecords)
	}
	if after.NavigationArtifactFallbacks != before.NavigationArtifactFallbacks {
		t.Fatal("changed refresh used an artifact fallback")
	}
	assertArtifactColdSurfaceParity(t, model)
}

func TestCompactRefreshInsertAndReorderUseActualAlternateIdentity(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(board.Board) board.Board
	}{
		{name: "insert", mutate: func(current board.Board) board.Board {
			next := cloneBoard(current)
			inserted := board.Task{ID: "compact-insert", Title: "compact insert", Status: board.StatusTodo}
			next.Tasks = append([]board.Task{inserted}, next.Tasks...)
			return next
		}},
		{name: "reorder", mutate: func(current board.Board) board.Board {
			next := cloneBoard(current)
			next.Tasks[0], next.Tasks[1] = next.Tasks[1], next.Tasks[0]
			return next
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := performanceModel(120, "", 80, 16)
			column := model.current.geometry.columns[statusIndexExact(board.StatusTodo)]
			if !column.density.Compact() {
				t.Fatal("compact artifact fixture selected a non-compact density")
			}
			before := model.RenderPlanStats()
			updated, _ := model.Update(boardLoadedMsg{board: test.mutate(model.board)})
			model = updated.(Model)
			after := model.RenderPlanStats()
			if after.NavigationArtifactFallbacks != before.NavigationArtifactFallbacks {
				t.Fatal("compact refresh fell back instead of comparing exact alternate identity")
			}
			if after.NavigationArtifactMisses == before.NavigationArtifactMisses {
				t.Fatal("compact insert/reorder reused cards whose alternate bit changed")
			}
			assertArtifactColdSurfaceParity(t, model)
		})
	}
}

func assertArtifactColdSurfaceParity(t *testing.T, model Model) {
	t.Helper()
	cold := model.renderColdSnapshot()
	retained := model.current
	if retained.view.Content != cold.view.Content || retained.view.AltScreen != cold.view.AltScreen ||
		retained.view.MouseMode != cold.view.MouseMode {
		t.Fatal("retained artifact content or terminal modes differ from cold oracle")
	}
	if retained.semantics.handler != cold.semantics.handler ||
		!retained.semantics.topology.SameControls(cold.semantics.topology) ||
		!slices.Equal(retained.semantics.hits, cold.semantics.hits) {
		t.Fatal("retained artifact hits or pointer topology differ from cold oracle")
	}
}

func TestUnsettledVisibleMissUsesPersistentBoundedPrefixUpdates(t *testing.T) {
	for _, count := range []int{120, 500, 1000} {
		t.Run(integerLabel(count), func(t *testing.T) {
			model := unsettledPerformanceModel(count)
			columnIndex := statusIndexExact(board.StatusTodo)
			column := model.current.geometry.columns[columnIndex]
			target := -1
			for ordinal := 1; ordinal+1 < column.index.length; ordinal++ {
				if !column.index.recordAt(ordinal).exact {
					target = ordinal
					break
				}
			}
			if target < 1 {
				t.Fatal("fixture had no unresolved todo window before worker convergence")
			}
			previous, ok := model.current.projection.taskAtStatus(board.StatusTodo, target-1)
			if !ok {
				t.Fatal("target predecessor missing from projection")
			}
			model.boardView.column = columnIndex
			model.boardView.rows[columnIndex] = target - 1
			model.boardView.cursors[columnIndex] = boardTaskAnchor{TaskID: previous.ID, IndexHint: target - 1}
			before := model.RenderPlanStats()
			beforeMedian := column.median
			untouchedRoot := model.current.geometry.columns[statusIndexExact(board.StatusDoing)].index.root

			updated, _ := model.Update(tea.KeyPressMsg{Code: tea.KeyDown})
			model = updated.(Model)
			after := model.RenderPlanStats()
			measured := after.SynchronousLayoutRecords - before.SynchronousLayoutRecords
			copied := after.SynchronousIndexNodes - before.SynchronousIndexNodes
			bound := 4 * visibleOverscanRecords(model)
			if measured == 0 || measured > bound {
				t.Fatalf("synchronous measurements=%d, visible+overscan convergence bound=%d", measured, bound)
			}
			maxNodes := measured * uint64(bits.Len(uint(column.index.length))+1)
			if copied == 0 || copied > maxNodes {
				t.Fatalf("persistent index copied %d nodes for %d records, logarithmic bound %d", copied, measured, maxNodes)
			}
			settled := model.current.geometry.columns[columnIndex]
			if settled.median != beforeMedian {
				t.Fatalf("main-loop visible miss recomputed median %d -> %d", beforeMedian, settled.median)
			}
			if model.current.geometry.columns[statusIndexExact(board.StatusDoing)].index.root != untouchedRoot {
				t.Fatal("visible todo miss replaced the untouched doing prefix root")
			}
			if model.current.geometry.unresolvedRecords == 0 {
				t.Fatal("test accidentally converged the whole board on Update")
			}
			start := settled.windowStart(model.boardView, columnIndex, &model.current.projection)
			first := settled.cardAtRow(start)
			last := settled.cardAtRow(max(min(start+settled.contentHeight-1, settled.totalRows()-1), 0)) + 1
			for ordinal := first; ordinal < last; ordinal++ {
				if !settled.index.recordAt(ordinal).exact {
					t.Fatalf("visible ordinal %d remained estimated after exact publication", ordinal)
				}
			}
			if after.SourceTaskComparisons != before.SourceTaskComparisons ||
				after.GeometrySnapshotInstalls != before.GeometrySnapshotInstalls {
				t.Fatalf("ordinary unsettled input source scans=%d worker installs=%d",
					after.SourceTaskComparisons-before.SourceTaskComparisons,
					after.GeometrySnapshotInstalls-before.GeometrySnapshotInstalls)
			}
		})
	}
}

func unsettledPerformanceModel(taskCount int) Model {
	fixture := performanceBoard(taskCount)
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	model := NewModel(stubBoardReader{board: fixture}, nil, "perf")
	model.now = func() time.Time { return now }
	model.renderedAt = now
	updated, _ := model.Update(boardLoadedMsg{board: fixture})
	model = updated.(Model)
	updated, _ = model.Update(tea.WindowSizeMsg{Width: performanceWidth, Height: performanceHeight})
	return updated.(Model)
}

func sameContentMaxScrollGeometry(t *testing.T, model Model) (renderGeometry, coldRenderSnapshot) {
	t.Helper()
	base := model.View().Content
	for _, status := range boardStatuses {
		index := statusIndexExact(status)
		original := model.current.geometry.columns[index]
		if original.index.length < 2 {
			continue
		}
		for delta := 1; delta <= 64; delta++ {
			geometry := model.current.geometry.deepClone()
			column := &geometry.columns[index]
			last := column.index.length - 1
			record := column.index.recordAt(last)
			record.height += delta
			column.index, _ = column.index.withRecord(last, record)
			geometry.refreshDerived()
			copy := model
			copy.renderingProjection = &model.current.projection
			copy.renderingGeometry = &geometry
			snapshot := copy.renderRetainedSnapshot()
			if snapshot.view.Content == base && snapshot.semantics.columns[index].maxScroll != model.current.semantics.columns[index].maxScroll {
				return geometry, snapshot
			}
		}
	}
	t.Fatal("fixture did not produce a same-content maxScroll change")
	return renderGeometry{}, coldRenderSnapshot{}
}

func visibleOverscanRecords(model Model) uint64 {
	var count uint64
	statuses := model.boardView.visibleStatuses()
	if model.width < model.themeStyles().Metrics.WideFrame {
		statuses = statuses[model.boardView.column : model.boardView.column+1]
	}
	for _, status := range statuses {
		index := statusIndexExact(status)
		column := model.current.geometry.columns[index]
		start := column.windowStart(model.boardView, index, &model.current.projection)
		first, last := column.overscanOrdinals(start)
		count += uint64(last - first)
	}
	return count
}

func TestStaleGeometryResultInstallsNothingAndStartsCurrentGeneration(t *testing.T) {
	model := performanceModel(120, "", performanceWidth, performanceHeight)
	// performanceModel settles its command chain; invalidate layout to create a
	// fresh generation with unresolved offscreen records.
	updated, _ := model.Update(tea.WindowSizeMsg{Width: performanceWidth - 1, Height: performanceHeight})
	model = updated.(Model)
	plan := *model.current
	plan.worker = geometryWorkerState{}
	model.current = &plan
	staleCommand := model.startGeometryWorker()
	if staleCommand == nil {
		t.Fatal("large board had no offscreen geometry command")
	}
	beforeResize := model.RenderPlanStats()
	updated, resizeCommand := model.Update(tea.WindowSizeMsg{Width: performanceWidth - 20, Height: performanceHeight})
	model = updated.(Model)
	if resizeCommand != nil {
		// The old generation still owns the sole worker token. A new command here
		// would permit two generations in flight.
		t.Fatal("resize started a second geometry generation before the old slice returned")
	}
	beforeStale := model.RenderPlanStats()
	content := model.View().Content
	staleMessage := staleCommand()
	updated, currentCommand := model.Update(staleMessage)
	model = updated.(Model)
	after := model.RenderPlanStats()
	if after.StaleWorkerResults != beforeStale.StaleWorkerResults+1 {
		t.Fatalf("stale results = %d, want %d", after.StaleWorkerResults, beforeStale.StaleWorkerResults+1)
	}
	if after.PublishedFrames != beforeStale.PublishedFrames || model.View().Content != content {
		t.Fatal("stale geometry changed the installed frame")
	}
	if currentCommand == nil {
		t.Fatal("current generation did not start after the stale slice released the worker token")
	}
	beforeDuplicate := model.RenderPlanStats()
	updated, duplicateCommand := model.Update(staleMessage)
	model = updated.(Model)
	afterDuplicate := model.RenderPlanStats()
	if afterDuplicate.StaleWorkerResults != beforeDuplicate.StaleWorkerResults+1 || duplicateCommand != nil {
		t.Fatalf("duplicate stale result count=%d command=%v; it released the current worker token",
			afterDuplicate.StaleWorkerResults, duplicateCommand != nil)
	}
	if beforeStale.InstalledSnapshotID <= beforeResize.InstalledSnapshotID {
		t.Fatal("resize itself did not publish its exact visible frame")
	}
}

func TestInteractiveRootUpdateRejectsSameGenerationWorkerResult(t *testing.T) {
	model := unsettledPerformanceModel(120)
	plan := *model.current
	plan.worker = geometryWorkerState{}
	model.current = &plan
	staleCommand := model.startGeometryWorker()
	if staleCommand == nil {
		t.Fatal("large board had no offscreen geometry command")
	}

	columnIndex := statusIndexExact(board.StatusTodo)
	column := model.current.geometry.columns[columnIndex]
	target := -1
	for ordinal := 1; ordinal+1 < column.index.length; ordinal++ {
		if !column.index.recordAt(ordinal).exact {
			target = ordinal
			break
		}
	}
	if target < 1 {
		t.Fatal("fixture had no unresolved todo window")
	}
	previous, ok := model.current.projection.taskAtStatus(board.StatusTodo, target-1)
	if !ok {
		t.Fatal("target predecessor missing from projection")
	}
	model.boardView.column = columnIndex
	model.boardView.rows[columnIndex] = target - 1
	model.boardView.cursors[columnIndex] = boardTaskAnchor{TaskID: previous.ID, IndexHint: target - 1}
	requestRootRevision := model.current.geometry.rootRevision

	updated, command := model.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	model = updated.(Model)
	if command != nil {
		t.Fatal("interactive miss started a second worker while the old root owned the token")
	}
	interactive := model.current.geometry
	if interactive.rootRevision <= requestRootRevision {
		t.Fatalf("interactive root revision=%d, request revision=%d", interactive.rootRevision, requestRootRevision)
	}
	interactiveRoot := interactive.columns[columnIndex].index.root
	if !interactive.columns[columnIndex].index.recordAt(target).exact {
		t.Fatal("interactive visible miss was not measured exactly")
	}

	before := model.RenderPlanStats()
	updated, replacement := model.Update(staleCommand())
	model = updated.(Model)
	after := model.RenderPlanStats()
	if after.StaleGeometryRootResults != before.StaleGeometryRootResults+1 ||
		after.StaleWorkerResults != before.StaleWorkerResults+1 {
		t.Fatalf("stale-root counters before=%+v after=%+v", before, after)
	}
	if model.current.geometry.rootRevision != interactive.rootRevision ||
		model.current.geometry.columns[columnIndex].index.root != interactiveRoot ||
		!model.current.geometry.columns[columnIndex].index.recordAt(target).exact {
		t.Fatal("stale same-generation worker overwrote the exact interactive root")
	}
	if replacement == nil {
		t.Fatal("stale-root result did not release the token and start current work")
	}

	for slices := 0; replacement != nil; slices++ {
		if slices > 256 {
			t.Fatal("replacement worker did not converge")
		}
		updated, replacement = model.Update(replacement())
		model = updated.(Model)
	}
	if model.current.geometry.unresolvedRecords != 0 {
		t.Fatalf("replacement worker left %d unresolved records", model.current.geometry.unresolvedRecords)
	}
	if !model.current.geometry.columns[columnIndex].index.recordAt(target).exact {
		t.Fatal("replacement convergence lost the exact interactive record")
	}
}

func TestGeometrySliceUsesDeterministicEightMillisecondBudget(t *testing.T) {
	model := performanceModel(120, "", performanceWidth, performanceHeight)
	geometry := model.current.geometry.deepClone()
	for columnIndex := range geometry.columns {
		column := &geometry.columns[columnIndex]
		for recordIndex := 0; recordIndex < column.index.length; recordIndex++ {
			record := column.index.recordAt(recordIndex)
			record.exact = false
			record.height = column.median
			column.index, _ = column.index.withRecord(recordIndex, record)
		}
	}
	projection := model.current.projection
	base := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	tick := -1
	now := func() time.Time {
		tick++
		return base.Add(time.Duration(tick) * 2 * time.Millisecond)
	}
	message := measureGeometrySlice(geometryWorkRequest{
		generation:   geometry.generation,
		rootRevision: geometry.rootRevision,
		sequence:     7,
		geometry:     &geometry,
		projection:   &projection,
		styles:       model.themeStyles(),
		height:       model.height,
		renderedAt:   model.renderedAt,
		now:          now,
	})
	if message.measured == 0 || message.complete {
		t.Fatalf("slice results=%d complete=%t, want bounded partial work", message.measured, message.complete)
	}
	timing := model.themeStyles().Timing
	if message.elapsed < timing.GeometrySliceTarget || message.elapsed > timing.GeometrySliceLimit {
		t.Fatalf("deterministic slice elapsed %s, target %s limit %s",
			message.elapsed, timing.GeometrySliceTarget, timing.GeometrySliceLimit)
	}
}

func TestPrefixCorrectionPreservesTaskAndIntraRowAnchor(t *testing.T) {
	model := performanceModel(120, "", performanceWidth, performanceHeight)
	column := model.current.geometry.columns[statusIndexExact(boardStatuses[0])]
	projection := &model.current.projection
	if column.index.length < 4 {
		t.Fatal("fixture did not produce four cards in the first column")
	}
	anchor := boardTaskAnchor{
		TaskID:    projection.board.Tasks[column.index.recordAt(3).projectionIndex].ID,
		IndexHint: 3,
		IntraRow:  1,
	}
	before := column.offsetForAnchorWithProjection(anchor, column.totalRows(), projection)
	record := column.index.recordAt(0)
	record.height += 7
	column.index, _ = column.index.withRecord(0, record)
	record = column.index.recordAt(1)
	record.height += 3
	column.index, _ = column.index.withRecord(1, record)
	after := column.offsetForAnchorWithProjection(anchor, column.totalRows(), projection)
	if after-before != 10 {
		t.Fatalf("anchor correction moved by %d rows, want the 10 rows inserted above it", after-before)
	}
	if after != column.index.prefixAt(3)+1 {
		t.Fatalf("corrected offset = %d, want task start %d plus intra-row 1", after, column.index.prefixAt(3))
	}
}

func TestCardLayoutPathReplacementKeepsReachableOwnershipStable(t *testing.T) {
	records := make([]cardLayoutRecord, 129)
	for index := range records {
		records[index].height = index%7 + 1
	}
	layout := newCardLayoutIndex(records, 1)
	wantBytes := layout.ownedBytes

	for index := range records {
		record := layout.recordAt(index)
		record.height++
		var copied int
		layout, copied = layout.withRecord(index, record)
		if copied <= 0 {
			t.Fatalf("replacement %d copied no path", index)
		}
		if layout.ownedBytes != wantBytes {
			t.Fatalf("replacement %d owned bytes = %d, want stable reachable estimate %d",
				index, layout.ownedBytes, wantBytes)
		}
	}

	var reachable func(*cardLayoutNode) int
	reachable = func(node *cardLayoutNode) int {
		if node == nil {
			return 0
		}
		return 1 + reachable(node.left) + reachable(node.right)
	}
	if got, want := reachable(layout.root), 2*len(records)-1; got != want {
		t.Fatalf("reachable nodes after path replacements = %d, want %d", got, want)
	}
}

func settleGeometry(t *testing.T, model Model) Model {
	t.Helper()
	command := model.startGeometryWorker()
	for steps := 0; command != nil; steps++ {
		if steps > 1000 {
			t.Fatal("geometry command chain did not converge")
		}
		updated, next := model.Update(command())
		model = updated.(Model)
		command = next
	}
	return model
}
