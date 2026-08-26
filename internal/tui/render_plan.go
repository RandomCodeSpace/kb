package tui

import (
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
	ProjectionBuilds               uint64              `json:"projection_builds"`
	TaskDerivations                uint64              `json:"task_derivations"`
	ProjectedTasks                 uint64              `json:"projected_tasks"`
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
	projection renderProjection
	stats      RenderPlanStats
}

// retainedRenderView is embedded in Model so its small value receiver is the
// root's View method. Returning a field through a Model value receiver makes
// Go heap-allocate the entire root model; promotion copies only this small
// adapter and keeps View at zero allocations.
type retainedRenderView struct {
	current             *renderPlan
	renderingProjection *renderProjection
	acceptedMessageID   uint64
}

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
}

func (p renderPlan) rebuild(model Model, impact renderImpact) renderPlan {
	var projectionChanged bool
	p.projection, _, projectionChanged = p.projection.rebuild(model)
	if projectionChanged {
		p.stats.ProjectionBuilds++
	}
	// The cold renderer consumes the just-built immutable projection. Pointing
	// this short-lived model copy at p keeps the oracle on the same data that
	// the installed plan publishes.
	model.current = &p
	model.renderingProjection = &p.projection
	snapshot := model.renderColdSnapshot()
	p.view = snapshot.view
	p.stats.Builds++
	p.stats.PublishedFrames++
	p.stats.InstalledSnapshotID++
	p.stats.InstalledForMessageID = model.acceptedMessageID
	p.stats.Revisions.advance(impact)
	p.stats.ContentBytes = uint64(len(snapshot.view.Content))
	p.stats.HitRegions = uint64(len(snapshot.hitRegions))
	p.stats.TaskDerivations = uint64(len(p.projection.tasks))
	p.stats.ProjectedTasks = uint64(len(p.projection.board.Tasks))
	p.stats.RetainedPlanOwnedBytesEstimate = retainedPlanOwnedBytesEstimate(model, snapshot) +
		p.projection.ownedBytesEstimate()
	return p
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
	var current renderPlan
	if m.current != nil {
		current = *m.current
	}
	next := current.rebuild(*m, impact)
	m.current = &next
}

// RenderPlanStats returns a value copy so instrumentation cannot mutate the
// frame or its revision graph.
func (m Model) RenderPlanStats() RenderPlanStats {
	if m.current == nil {
		return RenderPlanStats{AcceptedMessageID: m.acceptedMessageID}
	}
	stats := m.current.stats
	stats.AcceptedMessageID = m.acceptedMessageID
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
