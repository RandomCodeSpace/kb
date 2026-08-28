package tui

import (
	"slices"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

func TestSettledResizeGeometryIsReactivatedWithoutProjectionOrWorkerWork(t *testing.T) {
	model := settleGeometry(t, performanceModel(120, "", performanceWidth, performanceHeight))

	updated, command := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model = updated.(Model)
	settlePerformanceCommands(t, &model, []tea.Cmd{command})
	before := model.RenderPlanStats()

	updated, command = model.Update(tea.WindowSizeMsg{Width: performanceWidth, Height: performanceHeight})
	model = updated.(Model)
	if command != nil {
		t.Fatal("settled geometry cache hit scheduled a worker")
	}
	after := model.RenderPlanStats()
	if after.GeometryCacheHits != before.GeometryCacheHits+1 {
		t.Fatalf("geometry cache hits = %d, want %d", after.GeometryCacheHits, before.GeometryCacheHits+1)
	}
	if after.ProjectionBuilds != before.ProjectionBuilds ||
		after.ProjectionTaskVisits != before.ProjectionTaskVisits ||
		after.GeometryWorkerSlices != before.GeometryWorkerSlices {
		t.Fatalf("cache hit did projection or worker work: before=%+v after=%+v", before, after)
	}
	if after.PublishedFrames != before.PublishedFrames+1 {
		t.Fatalf("cache hit publications = %d, want 1", after.PublishedFrames-before.PublishedFrames)
	}
	assertPerformanceColdOracleParity(t, model)
}

func TestRenderGeometryLRUIsDeterministicAndTakeRemovesTheActiveHit(t *testing.T) {
	geometry := func(width int) renderGeometry {
		return renderGeometry{
			initialized: true,
			key:         renderGeometryKey{width: width},
		}
	}
	var cache renderGeometryLRU
	if accepted, evicted := cache.park(geometry(80)); !accepted || evicted {
		t.Fatalf("first park = accepted:%t evicted:%t", accepted, evicted)
	}
	cache.park(geometry(100))
	hit, ok := cache.take(renderGeometryKey{width: 80})
	if !ok || hit.key.width != 80 || cache.count != 1 || cache.entries[0].key.width != 100 {
		t.Fatalf("take = hit:%t width:%d count:%d entries:%v", ok, hit.key.width, cache.count, cacheWidths(cache))
	}
	cache.park(hit)
	if accepted, evicted := cache.park(geometry(120)); !accepted || !evicted {
		t.Fatalf("full park = accepted:%t evicted:%t", accepted, evicted)
	}
	if got := cacheWidths(cache); !slices.Equal(got, []int{120, 80}) {
		t.Fatalf("MRU/LRU widths = %v, want [120 80]", got)
	}
}

func TestRenderGeometryLRURejectsIncompleteGeometry(t *testing.T) {
	cache := renderGeometryLRU{}
	incomplete := renderGeometry{initialized: true, layoutRecords: 2, exactRecords: 1, unresolvedRecords: 1}
	if accepted, evicted := cache.park(incomplete); accepted || evicted || cache.count != 0 {
		t.Fatalf("incomplete park = accepted:%t evicted:%t count:%d", accepted, evicted, cache.count)
	}
}

func TestRenderGeometryKeyCoversTheRenderedDomain(t *testing.T) {
	stamp := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	styles := theme.New(true)
	base := renderGeometryKey{
		projectionGeneration: 7,
		width:                120,
		height:               36,
		showCancelled:        true,
		styles:               styles,
		temporalGeneration:   11,
		renderedAt:           stamp,
	}
	tests := []struct {
		name   string
		change func(*renderGeometryKey)
	}{
		{name: "projection", change: func(key *renderGeometryKey) { key.projectionGeneration++ }},
		{name: "width", change: func(key *renderGeometryKey) { key.width++ }},
		{name: "height", change: func(key *renderGeometryKey) { key.height++ }},
		{name: "cancelled", change: func(key *renderGeometryKey) { key.showCancelled = !key.showCancelled }},
		{name: "styles", change: func(key *renderGeometryKey) { key.styles = theme.New(false) }},
		{name: "temporal", change: func(key *renderGeometryKey) { key.temporalGeneration++ }},
		{name: "rendered-at", change: func(key *renderGeometryKey) { key.renderedAt = key.renderedAt.Add(time.Second) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := base
			test.change(&changed)
			if sameGeometryKey(base, changed) {
				t.Fatalf("%s change retained an exact key match", test.name)
			}
		})
	}
	equivalentLocation := base
	equivalentLocation.renderedAt = stamp.In(time.FixedZone("same-instant", 3600))
	if sameGeometryKey(base, equivalentLocation) {
		t.Fatal("renderedAt key ignored the location used by due labels and local-midnight deadlines")
	}
}

func TestRenderGeometryKeyNormalizesTerminalDimensions(t *testing.T) {
	model := performanceModel(17, "", 1, 8)
	projection := model.current.projection
	model.width = 0
	model.height = 0
	minimized := renderGeometryKeyFor(model, &projection)
	model.width = 1
	model.height = 8
	normalized := renderGeometryKeyFor(model, &projection)
	if !sameGeometryKey(minimized, normalized) || minimized.width != 1 || minimized.height != 8 {
		t.Fatalf("normalized keys differ: minimized=%+v normalized=%+v", minimized, normalized)
	}
}

func TestCachedGeometryRootsRemainImmutableWhenAnActivatedRootAdvances(t *testing.T) {
	model := settleGeometry(t, performanceModel(120, "", performanceWidth, performanceHeight))
	geometry := model.current.geometry
	columnIndex := -1
	for index, column := range geometry.columns {
		if column.index.length > 0 {
			columnIndex = index
			break
		}
	}
	if columnIndex < 0 {
		t.Fatal("fixture has no geometry records")
	}
	originalRoot := geometry.columns[columnIndex].index.root
	original := geometry.columns[columnIndex].index.recordAt(0)
	activated := geometry.deepClone()
	changed := original
	changed.height++
	activated.columns[columnIndex].index, _ = activated.columns[columnIndex].index.withRecord(0, changed)
	if activated.columns[columnIndex].index.root == originalRoot {
		t.Fatal("activated persistent root was mutated in place")
	}
	if got := geometry.columns[columnIndex].index.recordAt(0); got.height != original.height {
		t.Fatalf("inactive root height = %d, want immutable %d", got.height, original.height)
	}
}

func TestGeometryCacheActivationAllocatesFreshEpochAndRejectsCollidingStaleWorker(t *testing.T) {
	model := settleGeometry(t, performanceModel(120, "", performanceWidth, performanceHeight))
	oldEpoch := model.current.geometry.generation
	model = resizeAndSettle(t, model, 100, 30)
	plan := *model.current
	plan.worker = geometryWorkerState{inFlight: true, generation: oldEpoch, sequence: 91}
	model.current = &plan

	updated, command := model.Update(tea.WindowSizeMsg{Width: performanceWidth, Height: performanceHeight})
	model = updated.(Model)
	if command != nil || model.current.geometry.generation == oldEpoch {
		t.Fatalf("cache activation command=%t epoch=%d old=%d", command != nil, model.current.geometry.generation, oldEpoch)
	}
	activeRoot := model.current.geometry.columns[0].index.root
	before := model.RenderPlanStats()
	updated, _ = model.Update(geometryBatchMsg{generation: oldEpoch, sequence: 91})
	model = updated.(Model)
	after := model.RenderPlanStats()
	if after.StaleWorkerResults != before.StaleWorkerResults+1 ||
		model.current.geometry.columns[0].index.root != activeRoot {
		t.Fatalf("colliding stale worker was not rejected: before=%+v after=%+v", before, after)
	}
}

func TestGeometryCacheInvalidationDomainsAndSourceOnlyFastPath(t *testing.T) {
	t.Run("source-only", func(t *testing.T) {
		var scenario performanceScenario
		for _, candidate := range performanceScenarios(120) {
			if candidate.name == "refresh_nonmatching_insert" {
				scenario = candidate
				break
			}
		}
		if scenario.newModel == nil {
			t.Fatal("source-only performance scenario not found")
		}
		model := scenario.newModel()
		scenario.prepare(&model, 0)
		model = settleGeometry(t, model)
		model = resizeAndSettle(t, model, 100, 30)
		before := model.RenderPlanStats()
		beforeKeys := cacheKeys(model.current.geometryCache)
		stamps := scenario.admit(&model, 0)
		settlePerformanceStampCommands(t, &model, scenario, stamps)
		after := model.RenderPlanStats()
		if after.GeometryCacheHits != before.GeometryCacheHits ||
			after.GeometryCacheMisses != before.GeometryCacheMisses ||
			after.GeometryCacheInvalidations != before.GeometryCacheInvalidations ||
			after.GeometryCacheEntries != before.GeometryCacheEntries ||
			after.GeometryCacheOwnedBytes != before.GeometryCacheOwnedBytes ||
			!equalGeometryKeys(beforeKeys, cacheKeys(model.current.geometryCache)) {
			t.Fatalf("source-only fast path changed cache state: before=%+v after=%+v", before, after)
		}
	})

	for _, test := range []struct {
		name   string
		change func(*Model)
	}{
		{name: "projection", change: func(model *Model) {
			updated, _ := model.Update(boardLoadedMsg{board: appendPerformanceTasks(model.board, 1, true)})
			*model = updated.(Model)
		}},
		{name: "styles", change: func(model *Model) {
			model.applyStyles(theme.New(!model.isDark))
			model.rebuildRenderPlan(renderImpactAll)
		}},
		{name: "temporal", change: func(model *Model) {
			model.renderedAt = model.renderedAt.Add(time.Hour)
			model.temporalVisualGeneration++
			model.rebuildRenderPlan(renderImpactTemporal | renderImpactAppearance)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := settleGeometry(t, performanceModel(120, "keep17", performanceWidth, performanceHeight))
			model = resizeAndSettle(t, model, 100, 30)
			before := model.RenderPlanStats()
			beforeEpoch := model.current.geometry.generation
			test.change(&model)
			after := model.RenderPlanStats()
			if after.GeometryCacheInvalidations != before.GeometryCacheInvalidations+1 ||
				after.GeometryCacheEntries != 0 || after.GeometryCacheOwnedBytes != 0 {
				t.Fatalf("%s invalidation before=%+v after=%+v", test.name, before, after)
			}
			if test.name == "temporal" && model.current.geometry.generation == beforeEpoch {
				t.Fatal("temporal domain change reused the active geometry epoch")
			}
		})
	}
}

func TestGeometryCacheHitRegeneratesSelectionScrollPointerAndHitTopology(t *testing.T) {
	model := settleGeometry(t, performanceModel(120, "", performanceWidth, performanceHeight))
	model = resizeAndSettle(t, model, 100, 30)
	updated, command := model.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	model = updated.(Model)
	settlePerformanceCommands(t, &model, []tea.Cmd{command})
	column := model.boardView.column
	geometryColumn := model.current.geometry.columns[column]
	maximum := max(geometryColumn.totalRows()-geometryColumn.contentHeight, 0)
	if maximum > 0 {
		target := min(3, maximum)
		updated, command = model.Update(boardColumnScrolledMsg{
			status: geometryColumn.status, offset: target,
			anchor: geometryColumn.anchorAt(target, &model.current.projection), max: maximum,
		})
		model = updated.(Model)
		settlePerformanceCommands(t, &model, []tea.Cmd{command})
	}

	updated, command = model.Update(tea.WindowSizeMsg{Width: performanceWidth, Height: performanceHeight})
	model = updated.(Model)
	if command != nil {
		t.Fatal("settled cache activation scheduled geometry work")
	}
	cold := model.renderColdSnapshot()
	current := model.current
	if current.view.Content != cold.view.Content || current.view.AltScreen != cold.view.AltScreen ||
		current.view.MouseMode != cold.view.MouseMode || current.semantics.handler != cold.semantics.handler ||
		current.semantics.ownerEpoch != cold.semantics.ownerEpoch ||
		!current.semantics.topology.SameControls(cold.semantics.topology) ||
		!slices.Equal(current.semantics.hits, cold.semantics.hits) {
		t.Fatal("cache activation retained stale content, hits, handler, or pointer topology")
	}
	wantColumns := current.geometry.renderSemantics(model.boardView, &current.projection)
	if current.semantics.columns != wantColumns {
		t.Fatalf("cache activation retained stale selection/scroll columns: got=%+v want=%+v",
			current.semantics.columns, wantColumns)
	}
}

func TestGeometryCacheEvictionAndOwnedMemoryStayBounded(t *testing.T) {
	model := settleGeometry(t, performanceModel(1000, "", 120, 36))
	for _, size := range [][2]int{{110, 34}, {100, 32}, {90, 30}} {
		model = resizeAndSettle(t, model, size[0], size[1])
	}
	stats := model.RenderPlanStats()
	if stats.GeometryCacheEntries != inactiveRenderGeometryCapacity || stats.GeometryCacheEvictions != 1 {
		t.Fatalf("cache entries=%d evictions=%d, want 2/1", stats.GeometryCacheEntries, stats.GeometryCacheEvictions)
	}
	if stats.GeometryCacheOwnedBytes == 0 || stats.RetainedPlanOwnedBytesEstimate < stats.GeometryCacheOwnedBytes ||
		stats.RetainedPlanOwnedBytesEstimate > 64<<20 {
		t.Fatalf("cache owned=%d retained=%d, want charged and <=64MiB",
			stats.GeometryCacheOwnedBytes, stats.RetainedPlanOwnedBytesEstimate)
	}
}

func TestGeometryCacheCanActivateTheLRUWithoutEvictingItFirst(t *testing.T) {
	model := settleGeometry(t, performanceModel(120, "", 120, 36))
	model = resizeAndSettle(t, model, 110, 34)
	model = resizeAndSettle(t, model, 100, 32)
	before := model.RenderPlanStats()

	updated, command := model.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	model = updated.(Model)
	after := model.RenderPlanStats()
	if command != nil || after.GeometryCacheHits != before.GeometryCacheHits+1 ||
		after.GeometryCacheMisses != before.GeometryCacheMisses ||
		after.GeometryCacheEvictions != before.GeometryCacheEvictions {
		t.Fatalf("LRU activation command=%t hits/misses/evictions=%d/%d/%d, before=%d/%d/%d",
			command != nil, after.GeometryCacheHits, after.GeometryCacheMisses, after.GeometryCacheEvictions,
			before.GeometryCacheHits, before.GeometryCacheMisses, before.GeometryCacheEvictions)
	}
	assertPerformanceColdOracleParity(t, model)
}

func resizeAndSettle(t *testing.T, model Model, width, height int) Model {
	t.Helper()
	updated, command := model.Update(tea.WindowSizeMsg{Width: width, Height: height})
	model = updated.(Model)
	settlePerformanceCommands(t, &model, []tea.Cmd{command})
	return model
}

func cacheWidths(cache renderGeometryLRU) []int {
	widths := make([]int, cache.count)
	for index := range cache.count {
		widths[index] = cache.entries[index].key.width
	}
	return widths
}

func cacheKeys(cache renderGeometryLRU) []renderGeometryKey {
	keys := make([]renderGeometryKey, cache.count)
	for index := range cache.count {
		keys[index] = cache.entries[index].key
	}
	return keys
}

func equalGeometryKeys(left, right []renderGeometryKey) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if !sameGeometryKey(left[index], right[index]) {
			return false
		}
	}
	return true
}
