package tui

import (
	"slices"
	"time"
	"unsafe"

	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/tui/pointer"
)

// renderImpact is the typed invalidation vocabulary owned by the root render
// plan. Later slices can narrow an update to the records it actually changes;
// the foundation deliberately rebuilds conservatively while the cold renderer
// remains the correctness oracle.
type renderImpact uint16

const (
	renderImpactTasks renderImpact = 1 << iota
	renderImpactProjection
	renderImpactLayout
	renderImpactAppearance
	renderImpactOverlay
	renderImpactTemporal
	renderImpactPointer

	renderImpactAll = renderImpactTasks | renderImpactProjection | renderImpactLayout |
		renderImpactAppearance | renderImpactOverlay | renderImpactTemporal | renderImpactPointer
)

// RenderPlanRevisions identifies the immutable inputs used to build the
// retained frame. Separate revisions keep layout invalidation independent from
// appearance invalidation instead of hiding both behind an unkeyed cache.
type RenderPlanRevisions struct {
	Tasks      uint64 `json:"tasks"`
	Projection uint64 `json:"projection"`
	Layout     uint64 `json:"layout"`
	Appearance uint64 `json:"appearance"`
	Overlay    uint64 `json:"overlay"`
	Temporal   uint64 `json:"temporal"`
	Pointer    uint64 `json:"pointer"`
}

// RenderPlanStats is the instrumentation seam used by the regression harness.
// DiscardedEvents and StaleWorkerResults are zero until their bounded-admission
// and worker slices land; reserving them here keeps one report contract.
type RenderPlanStats struct {
	Revisions                      RenderPlanRevisions `json:"revisions"`
	AcceptedMessageID              uint64              `json:"accepted_message_id"`
	InstalledSnapshotID            uint64              `json:"installed_snapshot_id"`
	InstalledForMessageID          uint64              `json:"installed_for_message_id"`
	Builds                         uint64              `json:"builds"`
	PublishedFrames                uint64              `json:"published_frames"`
	DiscardedEvents                uint64              `json:"discarded_events"`
	PointerEventsAccepted          uint64              `json:"pointer_events_accepted"`
	PointerEventsDeferred          uint64              `json:"pointer_events_deferred"`
	PointerTrailingFlushes         uint64              `json:"pointer_trailing_flushes"`
	PointerIntentPending           bool                `json:"pointer_intent_pending"`
	StaleWorkerResults             uint64              `json:"stale_worker_results"`
	StaleGeometryRootResults       uint64              `json:"stale_geometry_root_results"`
	ProjectionBuilds               uint64              `json:"projection_builds"`
	ProjectionTaskVisits           uint64              `json:"projection_task_visits"`
	SourceTaskComparisons          uint64              `json:"source_task_comparisons"`
	TaskDerivations                uint64              `json:"task_derivations"`
	TaskDerivationBuilds           uint64              `json:"task_derivation_builds"`
	ProjectedTasks                 uint64              `json:"projected_tasks"`
	LayoutRecords                  uint64              `json:"layout_records"`
	ExactLayoutRecords             uint64              `json:"exact_layout_records"`
	EstimatedLayoutRecords         uint64              `json:"estimated_layout_records"`
	GeometryWorkerSlices           uint64              `json:"geometry_worker_slices"`
	MaxGeometryWorkerSliceNS       int64               `json:"max_geometry_worker_slice_ns"`
	GeometryWorkerOverruns         uint64              `json:"geometry_worker_overruns"`
	GeometryCacheHits              uint64              `json:"geometry_cache_hits"`
	GeometryCacheMisses            uint64              `json:"geometry_cache_misses"`
	GeometryCacheEvictions         uint64              `json:"geometry_cache_evictions"`
	GeometryCacheInvalidations     uint64              `json:"geometry_cache_invalidations"`
	GeometryCacheEntries           uint64              `json:"geometry_cache_entries"`
	GeometryCacheOwnedBytes        uint64              `json:"geometry_cache_owned_bytes"`
	RenderedCardRecords            uint64              `json:"rendered_card_records"`
	NavigationArtifactHits         uint64              `json:"navigation_artifact_hits"`
	NavigationArtifactMisses       uint64              `json:"navigation_artifact_misses"`
	NavigationArtifactPublications uint64              `json:"navigation_artifact_publications"`
	NavigationArtifactFallbacks    uint64              `json:"navigation_artifact_fallbacks"`
	WatcherPollFastPaths           uint64              `json:"watcher_poll_fast_paths"`
	WatcherVersionFastPaths        uint64              `json:"watcher_version_fast_paths"`
	WatcherStaleLoadFastPaths      uint64              `json:"watcher_stale_load_fast_paths"`
	WatcherLifecycleFallbacks      uint64              `json:"watcher_lifecycle_fallbacks"`
	RenderImpactClassifications    uint64              `json:"render_impact_classifications"`
	SynchronousLayoutRecords       uint64              `json:"synchronous_layout_records"`
	SynchronousIndexNodes          uint64              `json:"synchronous_index_nodes"`
	GeometrySnapshotInstalls       uint64              `json:"geometry_snapshot_installs"`
	MaxGeometryInstallUnits        uint64              `json:"max_geometry_install_units"`
	ContentBytes                   uint64              `json:"content_bytes"`
	HitRegions                     uint64              `json:"hit_regions"`
	RetainedPlanOwnedBytesEstimate uint64              `json:"retained_plan_owned_bytes_estimate"`
	NormalBaseBuilds               uint64              `json:"normal_base_builds"`
	DimmedBaseBuilds               uint64              `json:"dimmed_base_builds"`
	OverlayCompositions            uint64              `json:"overlay_compositions"`
	TemporalScheduledTicks         uint64              `json:"temporal_scheduled_ticks"`
	TemporalStaleTicks             uint64              `json:"temporal_stale_ticks"`
	TemporalTaskVisits             uint64              `json:"temporal_task_visits"`
	TemporalRecordsRefreshed       uint64              `json:"temporal_records_refreshed"`
	TemporalIndexNodesVisited      uint64              `json:"temporal_index_nodes_visited"`
	ShippedIDVisits                uint64              `json:"shipped_id_visits"`
}

// renderPlan is the process-local root of rendering state. Its interface is
// intentionally small: Update installs a complete snapshot, View returns it,
// and the harness reads counters. Projection records and compact card plans
// join this root in later slices without widening that interface.
type renderPlan struct {
	view            tea.View
	resolver        func(tea.MouseMsg) tea.Cmd
	semantics       renderSnapshotSemantics
	bases           retainedBoardBases
	pointer         pointer.State
	projection      renderProjection
	geometry        renderGeometry
	geometryCache   renderGeometryLRU
	geometryEpoch   uint64
	worker          geometryWorkerState
	artifacts       retainedCardArtifacts
	dimmedArtifacts retainedCardArtifacts
	stats           RenderPlanStats
}

type retainedBoardBases struct {
	generation uint64
	normal     boardRenderBase
	dimmed     boardRenderBase
	haveNormal bool
	haveDimmed bool
}

// retainedRenderView is embedded in Model so its small value receiver is the
// root's View method. Returning a field through a Model value receiver makes
// Go heap-allocate the entire root model; promotion copies only this small
// adapter and keeps View at zero allocations.
type retainedRenderView struct {
	current             *renderPlan
	renderingProjection *renderProjection
	renderingGeometry   *renderGeometry
	preparedProjection  *renderProjection
	preparedDerivations int
	preparedComparisons int
	acceptedMessageID   uint64
	renderTrace         *renderVisitTrace
	cardArtifacts       cardArtifactCache
}

type renderVisitTrace struct{ cardRecords uint64 }

// View returns the complete immutable frame most recently installed by Update.
// Rendering, hit-map construction, and overlay composition happen only at the
// render-plan publication seam.
func (r retainedRenderView) View() tea.View {
	if r.current == nil {
		return tea.View{}
	}
	return r.current.view
}

type coldRenderSnapshot struct {
	view       tea.View
	hitRegions []boardHit
	semantics  renderSnapshotSemantics
}

type renderHandlerTopology uint8

const (
	renderHandlerNone renderHandlerTopology = iota
	renderHandlerBoard
	renderHandlerHelp
	renderHandlerDetail
	renderHandlerSettings
	renderHandlerADR
	renderHandlerEditor
	renderHandlerAction
	renderHandlerIssueImport
	renderHandlerPalette
)

type columnRenderSemantics struct {
	present       bool
	status        string
	width         int
	panelHeight   int
	bodyWidth     int
	contentHeight int
	totalRows     int
	windowStart   int
	maxScroll     int
	visibleFirst  int
	visibleLast   int
	railed        bool
}

type renderSnapshotSemantics struct {
	handler    renderHandlerTopology
	ownerEpoch uint64
	topology   pointer.Topology
	hits       []boardHit
	columns    [len(boardStatuses)]columnRenderSemantics
}

func (p renderPlan) semanticallyMatches(snapshot coldRenderSnapshot) bool {
	return p.view.Content == snapshot.view.Content && p.view.AltScreen == snapshot.view.AltScreen &&
		p.view.MouseMode == snapshot.view.MouseMode && p.semantics.handler == snapshot.semantics.handler &&
		p.semantics.ownerEpoch == snapshot.semantics.ownerEpoch &&
		p.semantics.topology.SameControls(snapshot.semantics.topology) &&
		p.semantics.columns == snapshot.semantics.columns && slices.Equal(p.semantics.hits, snapshot.semantics.hits)
}

func (p renderPlan) rebuild(model Model, impact renderImpact, checkSource bool) renderPlan {
	desiredOwner, desiredEpoch := model.renderOwnerIdentity()
	if p.stats.InstalledSnapshotID != 0 &&
		(p.semantics.handler != desiredOwner || p.semantics.ownerEpoch != desiredEpoch) {
		model.pointerState = model.pointerState.ClearCapture().ClearHoverObservation()
	}
	baseImpact := impact&^renderImpactOverlay != 0 || !p.bases.haveNormal
	if !baseImpact && desiredOwner == renderHandlerBoard && p.bases.normal.pointer != model.pointerState {
		// An overlay pointer owner may leave feedback identities that a board
		// base never rendered. Rebuild instead of resurrecting stale styling.
		baseImpact = true
	}
	if baseImpact {
		var projectionChanged bool
		if prepared := model.preparedProjection; prepared != nil && prepared.matchesProjectionKey(model) {
			p.projection = *prepared
			projectionChanged = p.projection.generation != model.currentProjectionGeneration()
			p.stats.TaskDerivationBuilds += uint64(model.preparedDerivations)
			p.stats.SourceTaskComparisons += uint64(model.preparedComparisons)
		} else {
			if checkSource && p.projection.initialized {
				p.stats.SourceTaskComparisons += uint64(len(model.board.Tasks))
			}
			p.projection, _, projectionChanged = p.projection.rebuildSource(model, checkSource)
		}
		if projectionChanged {
			p.stats.ProjectionBuilds++
			p.stats.ProjectionTaskVisits += uint64(len(p.projection.tasks))
		}
		model.current = &p
		model.renderingProjection = &p.projection
		var synchronousRecords, synchronousNodes int
		synchronousRecords, synchronousNodes = p.rebuildGeometry(model, projectionChanged)
		p.stats.SynchronousLayoutRecords += uint64(synchronousRecords)
		p.stats.SynchronousIndexNodes += uint64(synchronousNodes)
		p.stats.TemporalRecordsRefreshed += uint64(p.geometry.temporalRecordsRefreshed)
		p.stats.TemporalIndexNodesVisited += uint64(p.geometry.temporalIndexNodesVisited)
		model.renderingGeometry = &p.geometry
	}
	model.current = &p
	model.renderingProjection = &p.projection
	model.renderingGeometry = &p.geometry
	var artifactPublication *cardArtifactPublication
	if baseImpact {
		artifactPublication = newCardArtifactPublication(p.artifacts, model.cardArtifactDomain(&p.projection))
		model.cardArtifacts = artifactPublication
	}
	trace := renderVisitTrace{}
	model.renderTrace = &trace
	if baseImpact {
		base := model.renderBoardBase(false)
		base = model.reresolveBoardHoverBase(base, false)
		p.bases.generation++
		p.bases.normal = base
		p.bases.haveNormal = true
		p.bases.haveDimmed = false
		p.stats.NormalBaseBuilds++
		p.artifacts = artifactPublication.finish()
		p.stats.NavigationArtifactHits += artifactPublication.hits
		p.stats.NavigationArtifactMisses += artifactPublication.misses
		p.stats.NavigationArtifactPublications++
		p.stats.NavigationArtifactFallbacks += artifactPublication.fallbacks
	}
	base := p.bases.normal
	if model.overlayOpen() {
		if !p.bases.haveDimmed {
			p.bases.dimmed = p.renderDimmedBoardBase(model)
			p.bases.haveDimmed = true
			p.stats.DimmedBaseBuilds++
		}
		base = p.bases.dimmed
	}
	snapshot := model.composeSnapshot(base)
	p.view = snapshot.view
	p.semantics = snapshot.semantics
	p.pointer = model.pointerState
	p.stats.Builds++
	p.stats.PublishedFrames++
	p.stats.OverlayCompositions++
	p.stats.InstalledSnapshotID++
	p.stats.InstalledForMessageID = model.acceptedMessageID
	p.stats.Revisions.advance(impact)
	p.stats.ContentBytes = uint64(len(snapshot.view.Content))
	p.stats.HitRegions = uint64(len(snapshot.hitRegions))
	p.stats.RenderedCardRecords += trace.cardRecords
	p.stats.TaskDerivations = uint64(len(p.projection.tasks))
	p.stats.ProjectedTasks = uint64(len(p.projection.board.Tasks))
	p.refreshGeometryStats()
	p.stats.RetainedPlanOwnedBytesEstimate = retainedPlanOwnedBytesEstimate(model, snapshot) +
		p.projection.ownedBytesEstimate() + p.geometry.ownedBytesEstimate() + p.bases.ownedBytesEstimate() +
		p.artifacts.ownedBytesEstimate() + p.dimmedArtifacts.ownedBytesEstimate() +
		p.geometryCache.ownedBytesEstimate()
	return p
}

func (p *renderPlan) renderDimmedBoardBase(model Model) boardRenderBase {
	dimmedModel := model
	if styles := model.themeStyles().Dimmed; styles != nil {
		dimmedModel.styles = styles
	}
	publication := newCardArtifactPublication(
		p.dimmedArtifacts, dimmedModel.cardArtifactDomain(&p.projection),
	)
	model.cardArtifacts = publication
	base := model.renderBoardBase(true)
	p.dimmedArtifacts = publication.finish()
	p.stats.NavigationArtifactHits += publication.hits
	p.stats.NavigationArtifactMisses += publication.misses
	p.stats.NavigationArtifactPublications++
	p.stats.NavigationArtifactFallbacks += publication.fallbacks
	return base
}

func (b retainedBoardBases) ownedBytesEstimate() uint64 {
	var estimate uint64
	if b.haveNormal {
		estimate += b.normal.ownedBytesEstimate()
	}
	if b.haveDimmed {
		estimate += b.dimmed.ownedBytesEstimate()
	}
	return estimate
}

func (p *renderPlan) refreshGeometryStats() {
	p.stats.LayoutRecords = uint64(p.geometry.layoutRecords)
	p.stats.ExactLayoutRecords = uint64(p.geometry.exactRecords)
	p.stats.EstimatedLayoutRecords = p.stats.LayoutRecords - p.stats.ExactLayoutRecords
	p.stats.GeometryCacheEntries = uint64(p.geometryCache.count)
	p.stats.GeometryCacheOwnedBytes = p.geometryCache.ownedBytesEstimate()
}

func (p *renderPlan) allocateGeometryEpoch() uint64 {
	p.geometryEpoch++
	if p.geometryEpoch == 0 {
		// Generation zero is never installed. Exhaustion is not recoverable, but
		// failing closed here prevents an ancient worker token from becoming live.
		panic("render geometry epoch exhausted")
	}
	return p.geometryEpoch
}

func (p *renderPlan) rebuildGeometry(model Model, projectionChanged bool) (int, int) {
	key := renderGeometryKeyFor(model, &p.projection)
	current := p.geometry
	if current.initialized && (!sameGeometryCacheDomain(current.key, key) || projectionChanged) {
		if p.geometryCache.clear() {
			p.stats.GeometryCacheInvalidations++
		}
	}

	if current.initialized && !projectionChanged && sameGeometryKey(current.key, key) {
		var records, nodes int
		p.geometry, records, nodes = current.ensureInteractiveGeometry(model, &p.projection)
		return records, nodes
	}

	if current.initialized && !projectionChanged && sameGeometryCacheDomain(current.key, key) {
		if cached, ok := p.geometryCache.take(key); ok {
			// Remove the target before parking the old active root. Otherwise a
			// full cache could evict an LRU target immediately before taking it.
			p.geometryCache.park(current)
			p.stats.GeometryCacheHits++
			cached.generation = p.allocateGeometryEpoch()
			cached.temporalRecordsRefreshed = 0
			cached.temporalIndexNodesVisited = 0
			p.geometry = cached
			return 0, 0
		}
		p.stats.GeometryCacheMisses++
		if accepted, evicted := p.geometryCache.park(current); accepted && evicted {
			p.stats.GeometryCacheEvictions++
		}
	}

	generation := p.allocateGeometryEpoch()
	var records, nodes int
	p.geometry, records, nodes = current.rebuild(model, &p.projection, generation, key)
	return records, nodes
}

// conservativePointerRegion mirrors the owned fields in pointer.Map's region
// records. The estimate adds a separate closure charge because Go does not
// expose closure-environment sizes for exact accounting.
type conservativePointerRegion struct {
	rect   pointer.Rect
	id     pointer.ControlID
	action pointer.Action
}

const (
	conservativeClosureBytes       = 256
	conservativeHandlerBytes       = 4 << 10
	conservativeOverlayRegionsCell = 4
)

// retainedPlanOwnedBytesEstimate is a conservative budget charge for data the
// installed plan owns: frame content, immutable board-hit topology, generated
// control identities, pointer-map records, and closure environments. It is not
// heap usage or RSS. Transient publication allocations and external-process RSS
// remain separate performance report fields.
func retainedPlanOwnedBytesEstimate(model Model, snapshot coldRenderSnapshot) uint64 {
	estimate := uint64(unsafe.Sizeof(renderPlan{})) + uint64(unsafe.Sizeof(snapshot.view)) +
		uint64(len(snapshot.view.Content))
	hitSize := uint64(unsafe.Sizeof(boardHit{}))
	regionSize := uint64(unsafe.Sizeof(conservativePointerRegion{}))
	var regions uint64
	for _, hit := range snapshot.hitRegions {
		estimate += hitSize
		// Charge payload bytes even when they alias canonical model strings. The
		// plan's hit and action closures keep those identities reachable.
		estimate += uint64(len(hit.status) + len(hit.taskID) + len(hit.tag) + len(hit.key))
		if id := boardCardHoverID(hit); id != "" {
			regions++
			estimate += uint64(len(id))
		}
		if id := boardHitControlID(hit); id != "" {
			// The control identity is retained by both control and hover maps.
			regions += 2
			estimate += 2 * uint64(len(id))
			// Its action closure captures the domain message and its identity.
			estimate += uint64(len(hit.status) + len(hit.taskID) + len(hit.tag) + len(hit.key))
		}
	}
	if snapshot.view.OnMouse == nil {
		return estimate
	}
	if regions == 0 {
		// Overlay helpers do not expose their internal pointer maps. Bound their
		// topology by four independently overlapping regions per terminal cell;
		// content bytes are already charged above.
		cells := uint64(max(model.width, 1)) * uint64(max(model.height, 1))
		regions = cells * conservativeOverlayRegionsCell
	}
	estimate += regions * (regionSize + conservativeClosureBytes)
	estimate += conservativeHandlerBytes
	return estimate
}

func (r *RenderPlanRevisions) advance(impact renderImpact) {
	if impact&renderImpactTasks != 0 {
		r.Tasks++
	}
	if impact&renderImpactProjection != 0 {
		r.Projection++
	}
	if impact&renderImpactLayout != 0 {
		r.Layout++
	}
	if impact&renderImpactAppearance != 0 {
		r.Appearance++
	}
	if impact&renderImpactOverlay != 0 {
		r.Overlay++
	}
	if impact&renderImpactTemporal != 0 {
		r.Temporal++
	}
	if impact&renderImpactPointer != 0 {
		r.Pointer++
	}
}

func (m *Model) rebuildRenderPlan(impact renderImpact) {
	m.rebuildRenderPlanAfterUpdate(impact, true)
}

func (m *Model) rebuildRenderPlanAfterUpdate(impact renderImpact, checkSource bool) {
	var current renderPlan
	if m.current != nil {
		current = *m.current
	}
	next := current.rebuild(*m, impact, checkSource)
	m.pointerState = next.pointer
	m.installPointerHandler(&next)
	m.current = &next
	m.preparedProjection = nil
	m.preparedDerivations = 0
	m.preparedComparisons = 0
	m.publishKeyboardAdmissionSnapshot()
}

func (m *Model) currentProjectionGeneration() uint64 {
	if m.current == nil {
		return 0
	}
	return m.current.projection.generation
}

func (m *Model) adoptPreparedProjectionWithoutFrame() {
	if m.current == nil || m.preparedProjection == nil || !m.preparedProjection.matchesProjectionKey(*m) {
		m.preparedProjection = nil
		m.preparedDerivations = 0
		m.preparedComparisons = 0
		return
	}
	next := *m.current
	oldOwned := next.projection.ownedBytesEstimate()
	next.projection = *m.preparedProjection
	next.stats.TaskDerivationBuilds += uint64(m.preparedDerivations)
	next.stats.SourceTaskComparisons += uint64(m.preparedComparisons)
	next.stats.TaskDerivations = uint64(len(next.projection.tasks))
	next.stats.ProjectedTasks = uint64(len(next.projection.board.Tasks))
	newOwned := next.projection.ownedBytesEstimate()
	if next.stats.RetainedPlanOwnedBytesEstimate >= oldOwned {
		next.stats.RetainedPlanOwnedBytesEstimate -= oldOwned
		next.stats.RetainedPlanOwnedBytesEstimate += newOwned
	}
	m.current = &next
	m.preparedProjection = nil
	m.preparedDerivations = 0
	m.preparedComparisons = 0
}

func (m *Model) installPointerHandler(plan *renderPlan) {
	if plan == nil {
		return
	}
	if m.pointerOwner != plan.semantics.handler || m.pointerOwnerEpoch != plan.semantics.ownerEpoch || m.pointerOwnerSeq == 0 {
		m.pointerMailbox.reset()
		m.pointerAdmission.resetAll()
		m.pointerOwner = plan.semantics.handler
		m.pointerOwnerEpoch = plan.semantics.ownerEpoch
		m.pointerOwnerSeq++
		m.pointerState = m.pointerState.ClearCapture().ClearHoverObservation()
	}
	if plan.view.OnMouse == nil {
		plan.resolver = nil
		return
	}
	route := pointerRouteIdentity{
		owner: m.pointerOwner, ownerSession: m.pointerOwnerSeq,
		geometry: plan.geometry.generation, snapshot: plan.stats.InstalledSnapshotID,
	}
	plan.resolver = plan.view.OnMouse
	plan.view.OnMouse = m.pointerMailbox.wrap(plan.resolver, route)
}

func (m Model) renderOwnerIdentity() (renderHandlerTopology, uint64) {
	handler := renderHandlerNone
	var epoch uint64
	if m.helpOpen {
		handler = renderHandlerHelp
	}
	if m.detail.IsOpen() {
		handler, epoch = renderHandlerDetail, m.detail.PointerSession()
	}
	if m.settings != nil {
		handler, epoch = renderHandlerSettings, 0
	}
	if m.adr.IsOpen() {
		handler, epoch = renderHandlerADR, m.adr.PointerSession()
	}
	if m.editor.IsOpen() {
		handler, epoch = renderHandlerEditor, m.editor.PointerSession()
	}
	if m.action.open() {
		handler, epoch = renderHandlerAction, m.taskActionSession
	}
	if m.issueImport.IsOpen() {
		handler, epoch = renderHandlerIssueImport, m.issueImport.PointerSession()
	}
	if m.palette.IsOpen() {
		handler, epoch = renderHandlerPalette, m.palette.PointerSession()
	}
	if handler == renderHandlerNone && !m.detail.OwnsInput() && !m.launching() {
		handler = renderHandlerBoard
	}
	return handler, epoch
}

// startGeometryWorker serializes the offscreen command chain. A superseding
// layout keeps the old token until its bounded slice returns stale; only then
// may the current generation start, so two generations never accumulate.
func (m *Model) startGeometryWorker() tea.Cmd {
	if m.current == nil || m.current.worker.inFlight || m.current.geometry.unresolvedRecords == 0 {
		return nil
	}
	next := *m.current
	if next.worker.generation != next.geometry.generation {
		next.worker.cursor = 0
	}
	next.worker.inFlight = true
	next.worker.generation = next.geometry.generation
	next.worker.sequence++

	geometry := next.geometry
	projection := next.projection
	request := geometryWorkRequest{
		generation: geometry.generation, rootRevision: geometry.rootRevision,
		sequence: next.worker.sequence, cursor: next.worker.cursor,
		geometry: &geometry, projection: &projection, styles: m.themeStyles(),
		height: max(m.height, 8), renderedAt: m.renderedAt, now: time.Now,
	}
	m.current = &next
	return request.command()
}

func (m Model) installGeometryBatch(message geometryBatchMsg) Model {
	if m.current == nil {
		return m
	}
	next := *m.current
	worker := next.worker
	if !worker.inFlight || worker.sequence != message.sequence || worker.generation != message.generation {
		next.stats.StaleWorkerResults++
		m.current = &next
		return m
	}
	// This result owns the active token. Release it even when its generation was
	// superseded; an unrelated duplicate result above must not release the
	// current command and accidentally start a second one.
	next.worker.inFlight = false
	if message.generation != next.geometry.generation {
		next.stats.StaleWorkerResults++
		next.worker.cursor = 0
		m.current = &next
		return m
	}
	if message.sourceRootRevision != next.geometry.rootRevision {
		next.stats.StaleWorkerResults++
		next.stats.StaleGeometryRootResults++
		next.worker.cursor = 0
		m.current = &next
		return m
	}

	if message.prepared.generation != message.generation ||
		message.prepared.rootRevision != message.sourceRootRevision+1 {
		next.stats.StaleWorkerResults++
		m.current = &next
		return m
	}
	next.geometry = message.prepared
	next.stats.GeometrySnapshotInstalls++
	next.stats.MaxGeometryInstallUnits = max(next.stats.MaxGeometryInstallUnits, 1)
	next.worker.cursor = message.next
	next.stats.GeometryWorkerSlices++
	next.stats.MaxGeometryWorkerSliceNS = max(next.stats.MaxGeometryWorkerSliceNS, message.elapsed.Nanoseconds())
	if message.elapsed > m.themeStyles().Timing.GeometrySliceLimit {
		next.stats.GeometryWorkerOverruns++
	}
	next.refreshGeometryStats()

	// Intermediate offscreen progress owns no frame. At convergence, publish
	// only when exact total geometry changed visible content (normally the
	// scrollbar); otherwise preserve the last-flushed content and hit map.
	if message.complete || next.geometry.unresolvedRecords == 0 {
		next.worker.cursor = 0
		model := m
		model.current = &next
		model.renderingProjection = &next.projection
		model.renderingGeometry = &next.geometry
		artifactPublication := newCardArtifactPublication(
			next.artifacts, model.cardArtifactDomain(&next.projection),
		)
		model.cardArtifacts = artifactPublication
		trace := renderVisitTrace{}
		model.renderTrace = &trace
		base := model.renderBoardBase(false)
		base = model.reresolveBoardHoverBase(base, false)
		next.bases.generation++
		next.bases.normal = base
		next.bases.haveNormal = true
		next.bases.haveDimmed = false
		next.stats.NormalBaseBuilds++
		next.artifacts = artifactPublication.finish()
		next.stats.NavigationArtifactHits += artifactPublication.hits
		next.stats.NavigationArtifactMisses += artifactPublication.misses
		next.stats.NavigationArtifactPublications++
		next.stats.NavigationArtifactFallbacks += artifactPublication.fallbacks
		if model.overlayOpen() {
			next.bases.dimmed = next.renderDimmedBoardBase(model)
			next.bases.haveDimmed = true
			next.stats.DimmedBaseBuilds++
			base = next.bases.dimmed
		}
		snapshot := model.composeSnapshot(base)
		next.stats.OverlayCompositions++
		next.pointer = model.pointerState
		next.stats.RenderedCardRecords += trace.cardRecords
		next.stats.RetainedPlanOwnedBytesEstimate = retainedPlanOwnedBytesEstimate(model, snapshot) +
			next.projection.ownedBytesEstimate() + next.geometry.ownedBytesEstimate() +
			next.bases.ownedBytesEstimate() + next.artifacts.ownedBytesEstimate() +
			next.dimmedArtifacts.ownedBytesEstimate() + next.geometryCache.ownedBytesEstimate()
		if !next.semanticallyMatches(snapshot) {
			next.view = snapshot.view
			next.semantics = snapshot.semantics
			next.stats.Builds++
			next.stats.PublishedFrames++
			next.stats.InstalledSnapshotID++
			next.stats.InstalledForMessageID = m.acceptedMessageID
			next.stats.ContentBytes = uint64(len(snapshot.view.Content))
			next.stats.HitRegions = uint64(len(snapshot.hitRegions))
			m.installPointerHandler(&next)
		}
	}
	m.current = &next
	m.pointerState = next.pointer
	return m
}

func (m *Model) reresolveBoardHover(snapshot coldRenderSnapshot) coldRenderSnapshot {
	if snapshot.semantics.handler != renderHandlerBoard {
		return snapshot
	}
	point, ok := m.pointerState.HoverPoint()
	if !ok {
		return snapshot
	}
	next := m.pointerState.Hover(boardHoverControlAt(snapshot.hitRegions, point), point)
	if next.Hovered() == m.pointerState.Hovered() {
		return snapshot
	}
	m.pointerState = next
	return m.renderRetainedSnapshot()
}

func (m *Model) reresolveBoardHoverBase(base boardRenderBase, dimmed bool) boardRenderBase {
	owner, _ := m.renderOwnerIdentity()
	if owner != renderHandlerBoard {
		return base
	}
	point, ok := m.pointerState.HoverPoint()
	if !ok {
		return base
	}
	next := m.pointerState.Hover(boardHoverControlAt(base.hits, point), point)
	if next.Hovered() == m.pointerState.Hovered() {
		return base
	}
	m.pointerState = next
	return m.renderBoardBase(dimmed)
}

func boardHoverControlAt(hits []boardHit, point pointer.Point) pointer.ControlID {
	for index := len(hits) - 1; index >= 0; index-- {
		hit := hits[index]
		if point.X < hit.x0 || point.X >= hit.x1 || point.Y < hit.y0 || point.Y >= hit.y1 {
			continue
		}
		if id := boardHitControlID(hit); id != "" {
			return id
		}
		if id := boardCardHoverID(hit); id != "" {
			return id
		}
	}
	return ""
}

// RenderPlanStats returns a value copy so instrumentation cannot mutate the
// frame or its revision graph.
func (m Model) RenderPlanStats() RenderPlanStats {
	if m.current == nil {
		return RenderPlanStats{
			AcceptedMessageID:           m.acceptedMessageID,
			DiscardedEvents:             m.inputAdmission.discardedEvents() + m.pointerAdmission.discarded,
			PointerEventsAccepted:       m.pointerAdmission.accepted,
			PointerEventsDeferred:       m.pointerAdmission.deferred,
			PointerTrailingFlushes:      m.pointerAdmission.flushes,
			PointerIntentPending:        m.pointerAdmission.havePending,
			WatcherPollFastPaths:        m.watcherPollFastPaths,
			WatcherVersionFastPaths:     m.watcherVersionFastPaths,
			WatcherStaleLoadFastPaths:   m.watcherStaleLoadFastPaths,
			WatcherLifecycleFallbacks:   m.watcherLifecycleFallbacks,
			RenderImpactClassifications: m.renderImpactClassifications,
		}
	}
	stats := m.current.stats
	stats.AcceptedMessageID = m.acceptedMessageID
	stats.DiscardedEvents += m.inputAdmission.discardedEvents() + m.pointerAdmission.discarded
	stats.PointerEventsAccepted = m.pointerAdmission.accepted
	stats.PointerEventsDeferred = m.pointerAdmission.deferred
	stats.PointerTrailingFlushes = m.pointerAdmission.flushes
	stats.PointerIntentPending = m.pointerAdmission.havePending
	stats.WatcherPollFastPaths = m.watcherPollFastPaths
	stats.WatcherVersionFastPaths = m.watcherVersionFastPaths
	stats.WatcherStaleLoadFastPaths = m.watcherStaleLoadFastPaths
	stats.WatcherLifecycleFallbacks = m.watcherLifecycleFallbacks
	stats.RenderImpactClassifications = m.renderImpactClassifications
	return stats
}

func renderImpactAfterUpdate(message tea.Msg, before, after Model) renderImpact {
	// A zero impact is earned from the post-update state, never guessed from a
	// raw event. Unknown messages remain conservative because root messages can
	// cross nominal boundaries through focused sub-models and follow-up results.
	switch message.(type) {
	case pollTickMsg:
		return 0
	case temporalTickMsg:
		return renderImpactTemporal | renderImpactAppearance
	case boardLoadedMsg:
		loaded := message.(boardLoadedMsg)
		if loaded.generation != 0 && loaded.generation < after.loadGeneration &&
			sameBoardSourceIdentity(before.board, after.board) &&
			sameRenderError(before.loadErr, after.loadErr) &&
			before.boardBusyLabel() == after.boardBusyLabel() {
			return 0
		}
		if after.preparedProjection != nil && before.current != nil &&
			after.preparedProjection.generation == before.current.projection.generation &&
			before.boardView == after.boardView && !before.overlayOpen() && !after.overlayOpen() &&
			sameRenderError(before.loadErr, after.loadErr) && sameRenderError(before.pollErr, after.pollErr) &&
			before.boardBusyLabel() == after.boardBusyLabel() {
			return 0
		}
	case dataVersionMsg:
		if sameRenderError(before.pollErr, after.pollErr) &&
			before.boardBusyLabel() == after.boardBusyLabel() &&
			sameBoardSourceIdentity(before.board, after.board) &&
			before.move.lifted == after.move.lifted && before.move.status == after.move.status &&
			before.move.statusError == after.move.statusError {
			return 0
		}
	case tea.WindowSizeMsg:
		if before.width == after.width && before.height == after.height {
			return 0
		}
	case tea.ColorProfileMsg:
		if before.profile == after.profile {
			return 0
		}
	case tea.BackgroundColorMsg:
		if before.isDark == after.isDark {
			return 0
		}
	}
	if (before.overlayOpen() || after.overlayOpen()) && boardBaseStableAcross(&before, &after) {
		return renderImpactOverlay
	}
	return renderImpactAll
}

func boardBaseStableAcross(before, after *Model) bool {
	if before.current == nil || !before.current.projection.matchesProjectionKey(*after) {
		return false
	}
	if before.width != after.width || before.height != after.height || before.styles != after.styles ||
		!before.renderedAt.Equal(after.renderedAt) || before.temporalVisualGeneration != after.temporalVisualGeneration ||
		before.boardView != after.boardView || before.loading != after.loading ||
		before.haveBoardSnapshot != after.haveBoardSnapshot || before.stopped != after.stopped ||
		before.brandFrame != after.brandFrame || before.brandGen != after.brandGen ||
		before.version != after.version || before.scroll != after.scroll || before.celebrate != after.celebrate ||
		before.boardBusyLabel() != after.boardBusyLabel() || before.busyFrameText() != after.busyFrameText() ||
		before.actionNotice != after.actionNotice || before.actionStatus != after.actionStatus ||
		before.actionStatusError != after.actionStatusError || before.noticeSeq != after.noticeSeq ||
		before.move.lifted != after.move.lifted || before.move.saving != after.move.saving ||
		before.move.status != after.move.status || before.move.statusError != after.move.statusError ||
		before.move.notice != after.move.notice ||
		before.filter.visualIdentity() != after.filter.visualIdentity() ||
		!sameRenderError(before.loadErr, after.loadErr) || !sameRenderError(before.pollErr, after.pollErr) ||
		!sameRenderError(before.preferenceErr, after.preferenceErr) || before.shipped.Date != after.shipped.Date ||
		before.shipped.revision != after.shipped.revision ||
		before.shipped.visibleCount != after.shipped.visibleCount {
		return false
	}
	return true
}

func sameRenderError(left, right error) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Error() == right.Error()
}
