package tui

import (
	"time"
	"unsafe"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/project"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// cardRenderArtifact owns only immutable styled card rows and relative label
// spans. Absolute board hits, pointer handlers, snapshot routes, and tea.View
// are rebuilt for every publication and never enter this cache.
type cardRenderArtifact struct {
	lines []string
	spans [][]labelSpan
}

// cardArtifactKey names every input that can change card output. The stable
// task revision binds identity and content; the remaining fields make layout,
// appearance, temporal, selection, and per-card pointer feedback explicit.
type cardArtifactKey struct {
	renderRevision     uint64
	temporalGeneration uint64
	taskID             string
	status             board.Status
	bodyWidth          int
	columnWidth        int
	panelHeight        int
	contentHeight      int
	frameHeight        int
	metaDepth          int
	density            theme.Density
	styles             *theme.Styles
	renderedAt         time.Time
	selected           bool
	alternate          bool
	railed             bool
	cardHovered        bool
	hoveredTag         string
	pressedTag         string
}

type cardArtifactDomain struct {
	temporalGeneration uint64
	styles             *theme.Styles
	renderedAt         time.Time
	width              int
	height             int
	selectionColumn    int
	showCancelled      bool
}

type retainedCardArtifacts struct {
	initialized bool
	domain      cardArtifactDomain
	records     map[cardArtifactKey]cardRenderArtifact
	ownedBytes  uint64
}

func (a retainedCardArtifacts) ownedBytesEstimate() uint64 { return a.ownedBytes }

// cardArtifactCache is the private renderer seam. The retained renderer
// supplies a publication transaction; the cold oracle supplies nil.
type cardArtifactCache interface {
	beginPass()
	lookup(cardArtifactKey) (cardRenderArtifact, bool)
	store(cardArtifactKey, cardRenderArtifact)
}

type cardArtifactPublication struct {
	previous   retainedCardArtifacts
	domain     cardArtifactDomain
	compatible bool
	recent     map[cardArtifactKey]cardRenderArtifact
	retained   map[cardArtifactKey]cardRenderArtifact
	hits       uint64
	misses     uint64
	fallbacks  uint64
}

func newCardArtifactPublication(
	previous retainedCardArtifacts,
	domain cardArtifactDomain,
) *cardArtifactPublication {
	compatible := previous.initialized && previous.domain == domain
	publication := &cardArtifactPublication{previous: previous, domain: domain, compatible: compatible}
	if previous.initialized && !compatible {
		publication.fallbacks = 1
	}
	return publication
}

func (p *cardArtifactPublication) beginPass() {
	p.recent = p.retained
	p.retained = make(map[cardArtifactKey]cardRenderArtifact)
}

func (p *cardArtifactPublication) lookup(key cardArtifactKey) (cardRenderArtifact, bool) {
	if artifact, ok := p.recent[key]; ok {
		p.hits++
		p.retained[key] = artifact
		return artifact, true
	}
	if p.compatible {
		if artifact, ok := p.previous.records[key]; ok {
			p.hits++
			p.retained[key] = artifact
			return artifact, true
		}
	}
	p.misses++
	return cardRenderArtifact{}, false
}

func (p *cardArtifactPublication) store(key cardArtifactKey, artifact cardRenderArtifact) {
	p.retained[key] = artifact
}

func (p *cardArtifactPublication) finish() retainedCardArtifacts {
	return retainedCardArtifacts{
		initialized: true,
		domain:      p.domain,
		records:     p.retained,
		ownedBytes:  cardArtifactsOwnedBytesEstimate(p.retained),
	}
}

func cardArtifactsOwnedBytesEstimate(records map[cardArtifactKey]cardRenderArtifact) uint64 {
	// Charge two key/value records per entry for conservative map bucket and
	// overflow overhead in addition to all retained string and span payloads.
	estimate := uint64(unsafe.Sizeof(retainedCardArtifacts{}))
	entryBytes := uint64(unsafe.Sizeof(cardArtifactKey{}) + unsafe.Sizeof(cardRenderArtifact{}))
	spanBytes := uint64(unsafe.Sizeof(labelSpan{}))
	for key, artifact := range records {
		estimate += 2 * entryBytes
		estimate += uint64(len(key.taskID) + len(key.status) + len(key.hoveredTag) + len(key.pressedTag))
		estimate += uint64(cap(artifact.lines)) * uint64(unsafe.Sizeof(string("")))
		for _, line := range artifact.lines {
			estimate += uint64(len(line))
		}
		estimate += uint64(cap(artifact.spans)) * uint64(unsafe.Sizeof([]labelSpan{}))
		for _, row := range artifact.spans {
			estimate += uint64(cap(row)) * spanBytes
			for _, span := range row {
				estimate += uint64(len(span.tag))
			}
		}
	}
	return estimate
}

func (m Model) cardArtifactDomain(_ *renderProjection) cardArtifactDomain {
	return cardArtifactDomain{
		temporalGeneration: m.temporalVisualGeneration,
		styles:             m.themeStyles(),
		renderedAt:         m.renderedAt,
		width:              m.width,
		height:             m.height,
		selectionColumn:    m.boardView.column,
		showCancelled:      m.boardView.showCancelled,
	}
}

func (m Model) cardArtifactKey(
	projection *renderProjection,
	column columnLayoutGeometry,
	ordinal int,
) (cardArtifactKey, bool) {
	record := column.index.recordAt(ordinal)
	task := projection.board.Tasks[record.projectionIndex]
	revision, cacheable := projection.taskRenderRevision(task.ID)
	if !cacheable {
		return cardArtifactKey{}, false
	}
	selected := m.boardView.column == statusIndex(column.status) &&
		m.boardView.rows[statusIndex(column.status)] == ordinal
	ordered := project.Lead(task.Tags)
	hoveredTag := m.hoveredCardTag(task.ID, ordered)
	pressedTag := ""
	for _, tag := range ordered {
		if m.pointerState.IsPressed(boardCardLabelControlID(task.ID, tag)) {
			pressedTag = tag
			break
		}
	}
	return cardArtifactKey{
		renderRevision:     revision,
		temporalGeneration: m.temporalVisualGeneration,
		taskID:             task.ID,
		status:             column.status,
		bodyWidth:          column.bodyWidth,
		columnWidth:        column.width,
		panelHeight:        column.panelHeight,
		contentHeight:      column.contentHeight,
		frameHeight:        max(m.height, 8),
		metaDepth:          column.metaDepth,
		density:            column.density,
		styles:             m.themeStyles(),
		renderedAt:         m.renderedAt,
		selected:           selected,
		alternate:          column.density.Compact() && ordinal%2 == 1,
		railed:             column.railed,
		cardHovered:        m.pointerState.IsHovered(boardCardControlID(task.ID)),
		hoveredTag:         hoveredTag,
		pressedTag:         pressedTag,
	}, true
}
