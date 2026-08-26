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
	StaleWorkerResults             uint64              `json:"stale_worker_results"`
	StaleGeometryRootResults       uint64              `json:"stale_geometry_root_results"`
	ProjectionBuilds               uint64              `json:"projection_builds"`
	ProjectionTaskVisits           uint64              `json:"projection_task_visits"`
	SourceTaskComparisons          uint64              `json:"source_task_comparisons"`
	TaskDerivations                uint64              `json:"task_derivations"`
	ProjectedTasks                 uint64              `json:"projected_tasks"`
	LayoutRecords                  uint64              `json:"layout_records"`
	ExactLayoutRecords             uint64              `json:"exact_layout_records"`
	EstimatedLayoutRecords         uint64              `json:"estimated_layout_records"`
	GeometryWorkerSlices           uint64              `json:"geometry_worker_slices"`
	MaxGeometryWorkerSliceNS       int64               `json:"max_geometry_worker_slice_ns"`
	GeometryWorkerOverruns         uint64              `json:"geometry_worker_overruns"`
	RenderedCardRecords            uint64              `json:"rendered_card_records"`
	SynchronousLayoutRecords       uint64              `json:"synchronous_layout_records"`
	SynchronousIndexNodes          uint64              `json:"synchronous_index_nodes"`
	GeometrySnapshotInstalls       uint64              `json:"geometry_snapshot_installs"`
	MaxGeometryInstallUnits        uint64              `json:"max_geometry_install_units"`
	ContentBytes                   uint64              `json:"content_bytes"`
	HitRegions                     uint64              `json:"hit_regions"`
	RetainedPlanOwnedBytesEstimate uint64              `json:"retained_plan_owned_bytes_estimate"`
}

// renderPlan is the process-local root of rendering state. Its interface is
// intentionally small: Update installs a complete snapshot, View returns it,
// and the harness reads counters. Projection records and compact card plans
// join this root in later slices without widening that interface.
type renderPlan struct {
	view       tea.View
	semantics  renderSnapshotSemantics
	projection renderProjection
	geometry   renderGeometry
	worker     geometryWorkerState
	stats      RenderPlanStats
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
	acceptedMessageID   uint64
	renderTrace         *renderVisitTrace
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
	handler renderHandlerTopology
	hits    []boardHit
	columns [len(boardStatuses)]columnRenderSemantics
}

func (p renderPlan) semanticallyMatches(snapshot coldRenderSnapshot) bool {
	return p.view.Content == snapshot.view.Content && p.view.AltScreen == snapshot.view.AltScreen &&
		p.view.MouseMode == snapshot.view.MouseMode && p.semantics.handler == snapshot.semantics.handler &&
		p.semantics.columns == snapshot.semantics.columns && slices.Equal(p.semantics.hits, snapshot.semantics.hits)
}

func (p renderPlan) rebuild(model Model, impact renderImpact, checkSource bool) renderPlan {
	var projectionChanged bool
	if prepared := model.preparedProjection; prepared != nil && prepared.matchesProjectionKey(model) {
		p.projection = *prepared
		projectionChanged = true
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
	// The cold renderer consumes the just-built immutable projection. Pointing
	// this short-lived model copy at p keeps the oracle on the same data that
	// the installed plan publishes.
	model.current = &p
	model.renderingProjection = &p.projection
	var synchronousRecords, synchronousNodes int
	p.geometry, synchronousRecords, synchronousNodes = p.geometry.rebuild(model, &p.projection, projectionChanged)
	p.stats.SynchronousLayoutRecords += uint64(synchronousRecords)
	p.stats.SynchronousIndexNodes += uint64(synchronousNodes)
	model.renderingGeometry = &p.geometry
	trace := renderVisitTrace{}
	model.renderTrace = &trace
	snapshot := model.renderRetainedSnapshot()
	p.view = snapshot.view
	p.semantics = snapshot.semantics
	p.stats.Builds++
	p.stats.PublishedFrames++
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
		p.projection.ownedBytesEstimate() + p.geometry.ownedBytesEstimate()
	return p
}

func (p *renderPlan) refreshGeometryStats() {
	p.stats.LayoutRecords = uint64(p.geometry.layoutRecords)
	p.stats.ExactLayoutRecords = uint64(p.geometry.exactRecords)
	p.stats.EstimatedLayoutRecords = p.stats.LayoutRecords - p.stats.ExactLayoutRecords
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
	m.current = &next
	m.preparedProjection = nil
	m.publishKeyboardAdmissionSnapshot()
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
		trace := renderVisitTrace{}
		model.renderTrace = &trace
		snapshot := model.renderRetainedSnapshot()
		next.stats.RenderedCardRecords += trace.cardRecords
		if !next.semanticallyMatches(snapshot) {
			next.view = snapshot.view
			next.semantics = snapshot.semantics
			next.stats.Builds++
			next.stats.PublishedFrames++
			next.stats.InstalledSnapshotID++
			next.stats.InstalledForMessageID = m.acceptedMessageID
			next.stats.ContentBytes = uint64(len(snapshot.view.Content))
			next.stats.HitRegions = uint64(len(snapshot.hitRegions))
			next.stats.RetainedPlanOwnedBytesEstimate = retainedPlanOwnedBytesEstimate(model, snapshot) +
				next.projection.ownedBytesEstimate() + next.geometry.ownedBytesEstimate()
		}
	}
	m.current = &next
	return m
}

// RenderPlanStats returns a value copy so instrumentation cannot mutate the
// frame or its revision graph.
func (m Model) RenderPlanStats() RenderPlanStats {
	if m.current == nil {
		return RenderPlanStats{
			AcceptedMessageID: m.acceptedMessageID,
			DiscardedEvents:   m.inputAdmission.discardedEvents(),
		}
	}
	stats := m.current.stats
	stats.AcceptedMessageID = m.acceptedMessageID
	stats.DiscardedEvents += m.inputAdmission.discardedEvents()
	return stats
}

func renderImpactFor(tea.Msg) renderImpact {
	// Root messages routinely cross nominal boundaries: a card click opens an
	// overlay, pointer motion can move a task, and sub-model results can change
	// both data and geometry. Narrowing by message type therefore produces
	// false negatives. Later phases may narrow impacts only from the actual
	// Update result and must prove the omitted revisions stayed independent.
	return renderImpactAll
}
