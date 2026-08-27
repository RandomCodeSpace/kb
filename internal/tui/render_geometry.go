package tui

import (
	"slices"
	"strconv"
	"strings"
	"time"
	"unsafe"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
	"github.com/RandomCodeSpace/kb/internal/tui/widget"
)

const secondsPerHour = 60 * 60

// cardLayoutRecord is deliberately dull: it refers back to the immutable
// projection and owns only terminal-row geometry. Canonical strings, styles,
// and rendered ANSI remain in their respective owners instead of being copied
// into a cache with a more flattering name.
type cardLayoutRecord struct {
	projectionIndex int
	height          int
	exact           bool
	nextTemporal    time.Time
	metaOverflow    [cardMetaSlots + 1]bool
}

// cardLayoutIndex is an immutable prefix-sum tree. A visible miss replaces one
// leaf and the O(log n) path to it; old snapshots and the in-flight worker keep
// their roots without requiring an O(total cards) defensive clone. Each node
// also carries exact-count and row aggregates, so prefix lookup, row lookup,
// and geometry statistics stay logarithmic or constant.
type cardLayoutIndex struct {
	root       *cardLayoutNode
	length     int
	gap        int
	ownedBytes uint64
}

type cardLayoutNode struct {
	left         *cardLayoutNode
	right        *cardLayoutNode
	record       cardLayoutRecord
	count        int
	rows         int
	exact        int
	minTemporal  time.Time
	metaOverflow [cardMetaSlots + 1]int
}

func newCardLayoutIndex(records []cardLayoutRecord, gap int) cardLayoutIndex {
	index := cardLayoutIndex{length: len(records), gap: gap}
	index.root = buildCardLayoutNode(records, 0, len(records), gap)
	if len(records) > 0 {
		index.ownedBytes = uint64(2*len(records)-1) * uint64(unsafe.Sizeof(cardLayoutNode{}))
	}
	return index
}

func buildCardLayoutNode(records []cardLayoutRecord, first, last, gap int) *cardLayoutNode {
	if first >= last {
		return nil
	}
	if last-first == 1 {
		rows := max(records[first].height, 0)
		if first+1 < len(records) {
			rows += gap
		}
		exact := 0
		if records[first].exact {
			exact = 1
		}
		node := &cardLayoutNode{
			record: records[first], count: 1, rows: rows, exact: exact,
			minTemporal: records[first].nextTemporal,
		}
		for depth := 1; depth <= cardMetaSlots; depth++ {
			if records[first].metaOverflow[depth] {
				node.metaOverflow[depth] = 1
			}
		}
		return node
	}
	middle := first + (last-first)/2
	return combineCardLayoutNodes(
		buildCardLayoutNode(records, first, middle, gap),
		buildCardLayoutNode(records, middle, last, gap),
	)
}

func combineCardLayoutNodes(left, right *cardLayoutNode) *cardLayoutNode {
	node := &cardLayoutNode{
		left: left, right: right,
		count: cardNodeCount(left) + cardNodeCount(right),
		rows:  cardNodeRows(left) + cardNodeRows(right),
		exact: cardNodeExact(left) + cardNodeExact(right),
	}
	node.minTemporal = earlierNonzeroTime(cardNodeMinTemporal(left), cardNodeMinTemporal(right))
	for depth := 1; depth <= cardMetaSlots; depth++ {
		node.metaOverflow[depth] = cardNodeMetaOverflow(left, depth) + cardNodeMetaOverflow(right, depth)
	}
	return node
}

func earlierNonzeroTime(left, right time.Time) time.Time {
	if left.IsZero() || !right.IsZero() && right.Before(left) {
		return right
	}
	return left
}

func cardNodeMinTemporal(node *cardLayoutNode) time.Time {
	if node == nil {
		return time.Time{}
	}
	return node.minTemporal
}

func cardNodeMetaOverflow(node *cardLayoutNode, depth int) int {
	if node == nil || depth < 0 || depth >= len(node.metaOverflow) {
		return 0
	}
	return node.metaOverflow[depth]
}

func cardNodeCount(node *cardLayoutNode) int {
	if node == nil {
		return 0
	}
	return node.count
}

func cardNodeRows(node *cardLayoutNode) int {
	if node == nil {
		return 0
	}
	return node.rows
}

func cardNodeExact(node *cardLayoutNode) int {
	if node == nil {
		return 0
	}
	return node.exact
}

func (i cardLayoutIndex) recordAt(ordinal int) cardLayoutRecord {
	if ordinal < 0 || ordinal >= i.length {
		return cardLayoutRecord{}
	}
	node := i.root
	for node != nil && node.count > 1 {
		leftCount := cardNodeCount(node.left)
		if ordinal < leftCount {
			node = node.left
		} else {
			ordinal -= leftCount
			node = node.right
		}
	}
	if node == nil {
		return cardLayoutRecord{}
	}
	return node.record
}

func (i cardLayoutIndex) withRecord(ordinal int, record cardLayoutRecord) (cardLayoutIndex, int) {
	if ordinal < 0 || ordinal >= i.length {
		return i, 0
	}
	root, copied := replaceCardLayoutRecord(i.root, ordinal, record, ordinal+1 < i.length, i.gap)
	next := i
	next.root = root
	return next, copied
}

func replaceCardLayoutRecord(
	node *cardLayoutNode,
	ordinal int,
	record cardLayoutRecord,
	hasSuccessor bool,
	gap int,
) (*cardLayoutNode, int) {
	if node.count == 1 {
		rows := max(record.height, 0)
		if hasSuccessor {
			rows += gap
		}
		exact := 0
		if record.exact {
			exact = 1
		}
		next := &cardLayoutNode{
			record: record, count: 1, rows: rows, exact: exact, minTemporal: record.nextTemporal,
		}
		for depth := 1; depth <= cardMetaSlots; depth++ {
			if record.metaOverflow[depth] {
				next.metaOverflow[depth] = 1
			}
		}
		return next, 1
	}
	leftCount := cardNodeCount(node.left)
	if ordinal < leftCount {
		left, copied := replaceCardLayoutRecord(node.left, ordinal, record, hasSuccessor, gap)
		return combineCardLayoutNodes(left, node.right), copied + 1
	}
	right, copied := replaceCardLayoutRecord(node.right, ordinal-leftCount, record, hasSuccessor, gap)
	return combineCardLayoutNodes(node.left, right), copied + 1
}

func (i cardLayoutIndex) prefixAt(ordinal int) int {
	ordinal = min(max(ordinal, 0), i.length)
	return cardLayoutPrefix(i.root, ordinal)
}

func cardLayoutPrefix(node *cardLayoutNode, count int) int {
	if node == nil || count <= 0 {
		return 0
	}
	if count >= node.count {
		return node.rows
	}
	leftCount := cardNodeCount(node.left)
	if count <= leftCount {
		return cardLayoutPrefix(node.left, count)
	}
	return cardNodeRows(node.left) + cardLayoutPrefix(node.right, count-leftCount)
}

func (i cardLayoutIndex) ordinalAtRow(row int) int {
	if i.length == 0 {
		return 0
	}
	row = min(max(row, 0), max(cardNodeRows(i.root)-1, 0))
	node := i.root
	ordinal := 0
	for node.count > 1 {
		leftRows := cardNodeRows(node.left)
		if row < leftRows {
			node = node.left
			continue
		}
		row -= leftRows
		ordinal += cardNodeCount(node.left)
		node = node.right
	}
	return min(ordinal, i.length-1)
}

func (i cardLayoutIndex) records() []cardLayoutRecord {
	records := make([]cardLayoutRecord, 0, i.length)
	appendCardLayoutRecords(i.root, &records)
	return records
}

func appendCardLayoutRecords(node *cardLayoutNode, records *[]cardLayoutRecord) {
	if node == nil {
		return
	}
	if node.count == 1 {
		*records = append(*records, node.record)
		return
	}
	appendCardLayoutRecords(node.left, records)
	appendCardLayoutRecords(node.right, records)
}

// columnLayoutGeometry is one prefix-height index. prefix[i] is the first row
// owned by cards[i]; prefix[len(cards)] is the complete stack height. Gaps are
// charged to the preceding card, matching boardTaskAnchor.IntraRow.
type columnLayoutGeometry struct {
	status        board.Status
	width         int
	panelHeight   int
	bodyWidth     int
	contentHeight int
	metaDepth     int
	metaOverflow  [cardMetaSlots + 1]int
	median        int
	gap           int
	density       theme.Density
	railed        bool
	index         cardLayoutIndex
}

type renderGeometryKey struct {
	width              int
	height             int
	showCancelled      bool
	styles             *theme.Styles
	temporalGeneration uint64
}

// renderGeometry retains only the current layout generation. A command may
// still hold the immediately superseded value while its bounded slice exits;
// renderPlan's worker token prevents a second command from starting meanwhile.
type renderGeometry struct {
	initialized               bool
	generation                uint64
	rootRevision              uint64
	key                       renderGeometryKey
	renderedAt                time.Time
	columns                   [len(boardStatuses)]columnLayoutGeometry
	layoutRecords             int
	exactRecords              int
	unresolvedRecords         int
	ownedBytes                uint64
	temporalRecordsRefreshed  int
	temporalIndexNodesVisited int
}

type geometryBatchMsg struct {
	generation         uint64
	sourceRootRevision uint64
	sequence           uint64
	next               int
	complete           bool
	elapsed            time.Duration
	measured           int
	prepared           renderGeometry
}

type geometryWorkerState struct {
	inFlight   bool
	generation uint64
	sequence   uint64
	cursor     int
}

type geometryWorkRequest struct {
	generation   uint64
	rootRevision uint64
	sequence     uint64
	cursor       int
	geometry     *renderGeometry
	projection   *renderProjection
	styles       *theme.Styles
	height       int
	renderedAt   time.Time
	now          func() time.Time
}

func (g renderGeometry) rebuild(model Model, projection *renderProjection, projectionChanged bool) (renderGeometry, int, int) {
	g.temporalRecordsRefreshed = 0
	g.temporalIndexNodesVisited = 0
	key := renderGeometryKey{
		width:              max(model.width, 1),
		height:             max(model.height, 8),
		showCancelled:      model.boardView.showCancelled,
		styles:             model.themeStyles(),
		temporalGeneration: model.temporalVisualGeneration,
	}
	if g.initialized && !projectionChanged && sameGeometryBaseKey(g.key, key) && g.key.temporalGeneration != key.temporalGeneration {
		return g.rebuildTemporal(model, projection, key)
	}
	if !g.initialized || projectionChanged || g.key != key {
		g = newRenderGeometry(model, projection, g.generation+1, key)
	}
	return g.ensureInteractiveGeometry(model, projection)
}

func sameGeometryBaseKey(left, right renderGeometryKey) bool {
	return left.width == right.width && left.height == right.height &&
		left.showCancelled == right.showCancelled && left.styles == right.styles
}

func newRenderGeometry(model Model, projection *renderProjection, generation uint64, key renderGeometryKey) renderGeometry {
	g := renderGeometry{
		initialized: true, generation: generation, rootRevision: 1, key: key, renderedAt: model.renderedAt,
	}
	styles := model.themeStyles()
	metrics := styles.Metrics
	statuses := model.boardView.visibleStatuses()
	visibleCount := len(statuses)
	if key.width < metrics.WideFrame {
		visibleCount = 1
	}
	layout := boardColumnLayout(metrics, key.width, visibleCount)
	density := metrics.DensityFor(key.height, layout.inner)
	chromeTop := 1 + 2 + metrics.PagePad(density) // top bar plus the two-row filter bar
	panelHeight := max(key.height-chromeTop-1, 1)

	for statusIndex, status := range model.boardView.visibleStatuses() {
		widthIndex := statusIndex
		if visibleCount == 1 {
			widthIndex = 0
		}
		g.columns[statusIndexExact(status)] = newColumnLayoutGeometry(
			model, projection, status, layout.widths[widthIndex], panelHeight, density,
		)
	}
	g.refreshDerived()
	return g
}

func (g renderGeometry) rebuildTemporal(
	model Model,
	projection *renderProjection,
	key renderGeometryKey,
) (renderGeometry, int, int) {
	next := g
	next.key = key
	next.renderedAt = model.renderedAt
	metaDepthChanged := false
	for columnIndex := range next.columns {
		column := next.columns[columnIndex]
		if column.index.length == 0 {
			continue
		}
		inner := model.themeStyles().Metrics.CardInner(column.bodyWidth, column.density)
		root, refreshed, visited, changed := refreshTemporalLayoutNode(
			column.index.root, projection, model, column.density, inner,
		)
		next.temporalRecordsRefreshed += refreshed
		next.temporalIndexNodesVisited += visited
		if !changed {
			continue
		}
		column.index.root = root
		column.metaOverflow = root.metaOverflow
		metaDepth := metaDepthFromOverflow(column.metaOverflow)
		if metaDepth != column.metaDepth {
			column.metaDepth = metaDepth
			metaDepthChanged = true
		}
		next.columns[columnIndex] = column
	}
	if next.temporalRecordsRefreshed > 0 {
		next.rootRevision++
	}
	if metaDepthChanged {
		next.generation++
	}
	return next, 0, 0
}

func refreshTemporalLayoutNode(
	node *cardLayoutNode,
	projection *renderProjection,
	model Model,
	density theme.Density,
	inner int,
) (*cardLayoutNode, int, int, bool) {
	if node == nil {
		return nil, 0, 0, false
	}
	visited := 1
	if node.minTemporal.IsZero() || node.minTemporal.After(model.renderedAt) {
		return node, 0, visited, false
	}
	if node.count == 1 {
		record := node.record
		task := projection.board.Tasks[record.projectionIndex]
		record.nextTemporal, _ = nextTaskTemporalBoundary(task, model.renderedAt)
		widths := model.cardMetaWidths(task, density)
		for depth := 1; depth <= cardMetaSlots; depth++ {
			record.metaOverflow[depth] = metaWidthPrefix(widths, depth) > inner
		}
		next := *node
		next.record = record
		next.minTemporal = record.nextTemporal
		for depth := 1; depth <= cardMetaSlots; depth++ {
			next.metaOverflow[depth] = 0
			if record.metaOverflow[depth] {
				next.metaOverflow[depth] = 1
			}
		}
		return &next, 1, visited, true
	}
	left, leftRecords, leftVisited, leftChanged := refreshTemporalLayoutNode(
		node.left, projection, model, density, inner,
	)
	right, rightRecords, rightVisited, rightChanged := refreshTemporalLayoutNode(
		node.right, projection, model, density, inner,
	)
	visited += leftVisited + rightVisited
	if !leftChanged && !rightChanged {
		return node, 0, visited, false
	}
	return combineCardLayoutNodes(left, right), leftRecords + rightRecords, visited, true
}

func newColumnLayoutGeometry(
	model Model,
	projection *renderProjection,
	status board.Status,
	width int,
	panelHeight int,
	density theme.Density,
) columnLayoutGeometry {
	styles := model.themeStyles()
	metrics := styles.Metrics
	index := statusIndexExact(status)
	projectionIndexes := projection.statuses[index]
	metaRows := 0
	if !density.Compact() {
		metaRows = 1
	}
	baseContentHeight := max(panelHeight-1-metaRows, 0)
	inset := metrics.ColumnPad(density)
	baseBodyWidth := max(width-2*inset, 0)
	gap := metrics.CardGapRows(density)
	minimumStack := 1
	if len(projectionIndexes) > 0 {
		minimumStack = len(projectionIndexes) + gap*max(len(projectionIndexes)-1, 0)
	}
	overflowCertain := len(projectionIndexes) > 0 &&
		(baseContentHeight <= 0 || minimumStack > baseContentHeight)
	railed := overflowCertain && baseBodyWidth > widget.ScrollbarW
	bodyWidth := baseBodyWidth
	if railed {
		bodyWidth -= widget.ScrollbarW
	}

	c := columnLayoutGeometry{
		status:        status,
		width:         width,
		panelHeight:   panelHeight,
		bodyWidth:     bodyWidth,
		contentHeight: baseContentHeight,
		gap:           gap,
		density:       density,
		railed:        railed,
		median:        defaultCardHeightEstimate(model, density),
	}
	records := make([]cardLayoutRecord, len(projectionIndexes))
	for ordinal, projectionIndex := range projectionIndexes {
		records[ordinal] = cardLayoutRecord{projectionIndex: projectionIndex, height: c.median}
	}
	prepareTemporalLayoutRecords(model, projection, records, bodyWidth, density)
	c.metaDepth, c.metaOverflow = metaStateFromRecords(records)
	c.index = newCardLayoutIndex(records, gap)

	// If even one-row cards fit, the rail decision is not knowable from a
	// conservative bound. Such columns are viewport-sized, so measuring the
	// whole small set synchronously is both exact and bounded.
	if !overflowCertain {
		for ordinal := range records {
			records[ordinal].height = measureCardLayout(model, projection, c, ordinal)
			records[ordinal].exact = true
		}
		c.index = newCardLayoutIndex(records, gap)
		c.refreshMedian()
		if c.totalRows() > baseContentHeight && baseContentHeight > 0 {
			c.contentHeight--
			if baseBodyWidth > widget.ScrollbarW {
				c.railed = true
				c.bodyWidth = baseBodyWidth - widget.ScrollbarW
				records = c.index.records()
				prepareTemporalLayoutRecords(model, projection, records, c.bodyWidth, density)
				c.metaDepth, c.metaOverflow = metaStateFromRecords(records)
				for ordinal := range records {
					records[ordinal].height = measureCardLayout(model, projection, c, ordinal)
				}
				c.index = newCardLayoutIndex(records, gap)
				c.refreshMedian()
			}
		}
	} else if c.contentHeight > 0 {
		c.contentHeight-- // the overflow cue owns the final panel row
	}
	return c
}

func prepareTemporalLayoutRecords(
	model Model,
	projection *renderProjection,
	records []cardLayoutRecord,
	width int,
	density theme.Density,
) {
	inner := model.themeStyles().Metrics.CardInner(width, density)
	for index := range records {
		task := projection.board.Tasks[records[index].projectionIndex]
		records[index].nextTemporal, _ = nextTaskTemporalBoundary(task, model.renderedAt)
		widths := model.cardMetaWidths(task, density)
		for depth := 1; depth <= cardMetaSlots; depth++ {
			records[index].metaOverflow[depth] = metaWidthPrefix(widths, depth) > inner
		}
	}
}

func metaStateFromRecords(records []cardLayoutRecord) (int, [cardMetaSlots + 1]int) {
	var overflow [cardMetaSlots + 1]int
	for _, record := range records {
		for depth := 1; depth <= cardMetaSlots; depth++ {
			if record.metaOverflow[depth] {
				overflow[depth]++
			}
		}
	}
	return metaDepthFromOverflow(overflow), overflow
}

func metaDepthFromOverflow(overflow [cardMetaSlots + 1]int) int {
	for depth := cardMetaSlots; depth > 1; depth-- {
		if overflow[depth] == 0 {
			return depth
		}
	}
	return 1
}

func defaultCardHeightEstimate(model Model, density theme.Density) int {
	if density.Compact() {
		return 2
	}
	ceiling := model.themeStyles().Metrics.CardRows(max(model.height, 8), density)
	return min(max(ceiling/2, 3), 6)
}

func projectionMetaDepth(
	model Model,
	projection *renderProjection,
	cards []cardLayoutRecord,
	width int,
	density theme.Density,
) int {
	depth, _ := projectionMetaState(model, projection, cards, width, density)
	return depth
}

func projectionMetaState(
	model Model,
	projection *renderProjection,
	cards []cardLayoutRecord,
	width int,
	density theme.Density,
) (int, [cardMetaSlots + 1]int) {
	inner := model.themeStyles().Metrics.CardInner(width, density)
	var overflow [cardMetaSlots + 1]int
	for _, card := range cards {
		widths := model.cardMetaWidths(projection.board.Tasks[card.projectionIndex], density)
		for depth := 1; depth <= cardMetaSlots; depth++ {
			if metaWidthPrefix(widths, depth) > inner {
				overflow[depth]++
			}
		}
	}
	for depth := cardMetaSlots; depth > 1; depth-- {
		if overflow[depth] == 0 {
			return depth, overflow
		}
	}
	return 1, overflow
}

func measureCardLayout(model Model, projection *renderProjection, column columnLayoutGeometry, ordinal int) int {
	if ordinal < 0 || ordinal >= column.index.length {
		return 0
	}
	record := column.index.recordAt(ordinal)
	task := projection.board.Tasks[record.projectionIndex]
	selected := model.boardView.column == statusIndex(column.status) && model.boardView.rows[statusIndex(column.status)] == ordinal
	opts, _ := model.cardOptsForTask(model.themeStyles(), task, column.bodyWidth, column.density,
		ordinal, column.metaDepth, selected)
	return widget.CardHeight(model.themeStyles(), opts)
}

func (g renderGeometry) ensureInteractiveGeometry(model Model, projection *renderProjection) (renderGeometry, int, int) {
	changed := false
	next := g
	measured := 0
	copiedNodes := 0
	for _, status := range model.boardView.visibleStatuses() {
		if model.width < model.themeStyles().Metrics.WideFrame && status != boardStatuses[model.boardView.column] {
			continue
		}
		index := statusIndexExact(status)
		column := next.columns[index]
		for range 4 { // estimates may move a boundary; convergence is monotone
			start := column.windowStart(model.boardView, index, projection)
			first, last := column.overscanOrdinals(start)
			missing := false
			for ordinal := first; ordinal < last; ordinal++ {
				if !column.index.recordAt(ordinal).exact {
					missing = true
					break
				}
			}
			if !missing {
				break
			}
			changed = true
			for ordinal := first; ordinal < last; ordinal++ {
				record := column.index.recordAt(ordinal)
				if record.exact {
					continue
				}
				record.height = measureCardLayout(model, projection, column, ordinal)
				record.exact = true
				var copied int
				column.index, copied = column.index.withRecord(ordinal, record)
				copiedNodes += copied
				measured++
			}
			next.columns[index] = column
		}
	}
	if changed {
		next.rootRevision++
		next.refreshDerived()
	}
	return next, measured, copiedNodes
}

// deepClone is now a shallow root copy because cardLayoutIndex nodes are
// immutable. Tests use it to fork geometry; production worker and Update can
// safely retain either root while the other advances persistently.
func (g renderGeometry) deepClone() renderGeometry {
	return g
}

func (c *columnLayoutGeometry) refreshMedian() {
	records := c.index.records()
	values := make([]int, 0, len(records))
	for _, record := range records {
		if record.exact {
			values = append(values, record.height)
		}
	}
	if len(values) == 0 {
		return
	}
	slices.Sort(values)
	c.median = values[len(values)/2]
	for index := range records {
		if !records[index].exact {
			records[index].height = c.median
		}
	}
	c.index = newCardLayoutIndex(records, c.gap)
}

func (c columnLayoutGeometry) totalRows() int {
	if c.index.length == 0 {
		return 1
	}
	return max(cardNodeRows(c.index.root), 1)
}

func (c columnLayoutGeometry) windowStart(view boardViewState, column int, projection *renderProjection) int {
	height := c.contentHeight
	if c.index.length == 0 || height <= 0 || c.totalRows() <= height {
		return 0
	}
	maxScroll := max(c.totalRows()-height, 0)
	if view.manualScroll[column] {
		anchor := view.scrollAnchors[column]
		if anchor.TaskID != "" {
			return c.offsetForAnchorWithProjection(anchor, maxScroll, projection)
		}
		return min(max(view.scrolls[column], 0), maxScroll)
	}
	selected := min(max(view.rows[column], 0), c.index.length-1)
	record := c.index.recordAt(selected)
	start := max(c.index.prefixAt(selected)-1, 0)
	end := c.index.prefixAt(selected) + record.height
	if end > start+height {
		start = end - height
	}
	return min(max(start, 0), maxScroll)
}

// overscanOrdinals returns the cards intersecting the visible rows plus one
// additional viewport split around it. The total styled row budget is thus at
// most two viewports, not an unbounded card-count guess.
func (c columnLayoutGeometry) overscanOrdinals(start int) (int, int) {
	if c.index.length == 0 {
		return 0, 0
	}
	before := c.contentHeight / 2
	after := c.contentHeight - before
	firstRow := max(start-before, 0)
	lastRow := min(start+c.contentHeight+after, c.totalRows())
	first := c.cardAtRow(firstRow)
	last := c.cardAtRow(max(lastRow-1, 0)) + 1
	return min(max(first, 0), c.index.length), min(max(last, 0), c.index.length)
}

func (c columnLayoutGeometry) cardAtRow(row int) int {
	if c.index.length == 0 {
		return 0
	}
	return c.index.ordinalAtRow(row)
}

func (c columnLayoutGeometry) anchorAt(offset int, projection *renderProjection) boardTaskAnchor {
	if c.index.length == 0 {
		return boardTaskAnchor{}
	}
	ordinal := c.cardAtRow(offset)
	record := c.index.recordAt(ordinal)
	span := record.height
	if ordinal+1 < c.index.length {
		span += c.gap
	}
	return boardTaskAnchor{
		TaskID:    projection.board.Tasks[record.projectionIndex].ID,
		IndexHint: ordinal,
		IntraRow:  min(max(offset-c.index.prefixAt(ordinal), 0), max(span-1, 0)),
	}
}

func (c columnLayoutGeometry) offsetForAnchor(anchor boardTaskAnchor, maxScroll int) int {
	if c.index.length == 0 {
		return 0
	}
	// Identity is resolved by offsetForAnchorWithProjection. This method is the
	// deterministic ordinal fallback after that identity disappears.
	ordinal := min(max(anchor.IndexHint, 0), c.index.length-1)
	record := c.index.recordAt(ordinal)
	span := record.height
	if ordinal+1 < c.index.length {
		span += c.gap
	}
	intra := min(max(anchor.IntraRow, 0), max(span-1, 0))
	return min(max(c.index.prefixAt(ordinal)+intra, 0), maxScroll)
}

func (c columnLayoutGeometry) offsetForAnchorWithProjection(
	anchor boardTaskAnchor,
	maxScroll int,
	projection *renderProjection,
) int {
	if ordinal, ok := projection.ordinalForTask(c.status, anchor.TaskID); ok && ordinal < c.index.length {
		record := c.index.recordAt(ordinal)
		span := record.height
		if ordinal+1 < c.index.length {
			span += c.gap
		}
		intra := min(max(anchor.IntraRow, 0), max(span-1, 0))
		return min(max(c.index.prefixAt(ordinal)+intra, 0), maxScroll)
	}
	return c.offsetForAnchor(anchor, maxScroll)
}

func (g *renderGeometry) refreshDerived() {
	g.layoutRecords = 0
	g.exactRecords = 0
	estimate := uint64(unsafe.Sizeof(*g))
	for _, column := range g.columns {
		g.layoutRecords += column.index.length
		g.exactRecords += cardNodeExact(column.index.root)
		estimate += column.index.ownedBytes
	}
	g.unresolvedRecords = g.layoutRecords - g.exactRecords
	g.ownedBytes = estimate
}

func (g renderGeometry) ownedBytesEstimate() uint64 {
	return g.ownedBytes
}

func (g renderGeometry) renderSemantics(view boardViewState, projection *renderProjection) [len(boardStatuses)]columnRenderSemantics {
	var result [len(boardStatuses)]columnRenderSemantics
	statuses := view.visibleStatuses()
	if g.key.width < g.key.styles.Metrics.WideFrame {
		statuses = statuses[view.column : view.column+1]
	}
	for _, status := range statuses {
		index := statusIndexExact(status)
		column := g.columns[index]
		start := column.windowStart(view, index, projection)
		maximum := max(column.totalRows()-column.contentHeight, 0)
		first, last := 0, 0
		if column.index.length > 0 {
			first = column.cardAtRow(start)
			last = column.cardAtRow(max(min(start+column.contentHeight-1, column.totalRows()-1), 0)) + 1
		}
		result[index] = columnRenderSemantics{
			present: true, status: string(status), width: column.width, panelHeight: column.panelHeight,
			bodyWidth: column.bodyWidth, contentHeight: column.contentHeight, totalRows: column.totalRows(),
			windowStart: start, maxScroll: maximum, visibleFirst: first, visibleLast: last, railed: column.railed,
		}
	}
	return result
}

// renderVirtualColumn draws from exact records in the visible window while
// limiting card styling to that window plus one viewport of overscan. The
// overscan rows are deliberately absent from hits; invisible controls are a
// surprisingly efficient way to make a mouse feel haunted.
func (m Model) renderVirtualColumn(
	projection *renderProjection,
	column columnLayoutGeometry,
) renderedColumn {
	styles := m.themeStyles()
	index := statusIndexExact(column.status)
	hits := []boardHit{
		{x1: column.width, y1: column.panelHeight, status: column.status},
		{x1: column.width, y1: 1, status: column.status, kind: boardHitColumnHeading},
	}
	if column.width <= 0 || column.panelHeight <= 0 {
		return renderedColumn{hits: hits}
	}

	meta := ""
	metaRows := 0
	if !column.density.Compact() {
		meta = projectedColumnMetaLine(styles, projection, column)
		metaRows = 1
	}
	inset := styles.Metrics.ColumnPad(column.density)
	if column.index.length == 0 {
		body := make([]string, column.contentHeight)
		if len(body) > 0 {
			body[0] = m.columnPlaceholder(styles, column.bodyWidth)
		}
		lines := widget.Panel(styles, widget.PanelOpts{
			Header: widget.BandOpts{
				Index: index + 1, Label: statusLabel(column.status), Hue: columnHue(column.status),
				Focused: m.boardView.column == index,
				Hovered: m.pointerState.IsHovered(boardColumnControlID(column.status)),
			},
			Meta: meta, Body: body, Width: column.width, Height: column.panelHeight, Density: column.density,
		})
		lines[0] = m.pointerState.Render(styles, boardColumnControlID(column.status), lines[0])
		return renderedColumn{lines: lines, hits: hits}
	}

	start := column.windowStart(m.boardView, index, projection)
	maxScroll := max(column.totalRows()-column.contentHeight, 0)
	hits[0].scroll = start
	hits[0].maxScroll = maxScroll
	hits[0].scrollUp = column.anchorAt(min(max(start-3, 0), maxScroll), projection)
	hits[0].scrollDown = column.anchorAt(min(max(start+3, 0), maxScroll), projection)
	end := column.visibleEnd(start)

	first, last := column.overscanOrdinals(start)
	rendered := make([]virtualCardRows, last-first)
	for ordinal := first; ordinal < last; ordinal++ {
		rendered[ordinal-first] = m.renderVirtualCard(projection, column, ordinal)
	}

	rail := m.columnScrollbar(styles, column.railed, index, column.totalRows(), column.contentHeight, start)
	body := make([]string, 0, column.contentHeight)
	for row := 0; row < column.contentHeight; row++ {
		source := start + row
		line := ""
		ordinal := column.cardAtRow(source)
		record := column.index.recordAt(ordinal)
		cardStart := column.index.prefixAt(ordinal)
		cardEnd := cardStart + record.height
		if source < end && source >= cardStart && source < cardEnd && ordinal >= first && ordinal < last {
			card := rendered[ordinal-first]
			cardRow := source - cardStart
			if cardRow >= 0 && cardRow < len(card.lines) {
				line = card.lines[cardRow]
				y := 1 + metaRows + row
				task := projection.board.Tasks[record.projectionIndex]
				hits = append(hits, boardHit{x1: column.width, y0: y, y1: y + 1, status: column.status, taskID: task.ID})
				for _, span := range card.spans[cardRow] {
					hits = append(hits, boardHit{
						x0: inset + span.x0, x1: min(inset+span.x1, column.width),
						y0: y, y1: y + 1, status: column.status, taskID: task.ID,
						kind: boardHitFilterLabel, tag: span.tag,
					})
				}
			}
		}
		if column.railed {
			cell := ""
			if row < len(rail) {
				cell = rail[row]
			}
			line = fillRow(styles.Column.Panel, line, column.bodyWidth) + cell
		}
		body = append(body, line)
	}

	lines := widget.Panel(styles, widget.PanelOpts{
		Header: widget.BandOpts{
			Index: index + 1, Label: statusLabel(column.status), Count: column.index.length,
			Hue: columnHue(column.status), Focused: m.boardView.column == index,
			Hovered: m.pointerState.IsHovered(boardColumnControlID(column.status)),
		},
		Meta: meta, MetaLit: m.celebrateLit(column.status), Body: body,
		More: column.hiddenCards(end, start), Width: column.width, Height: column.panelHeight,
		Density: column.density,
	})
	lines[0] = m.pointerState.Render(styles, boardColumnControlID(column.status), lines[0])
	return renderedColumn{lines: lines, hits: hits}
}

type virtualCardRows struct {
	lines []string
	spans [][]labelSpan
}

func (m Model) renderVirtualCard(
	projection *renderProjection,
	column columnLayoutGeometry,
	ordinal int,
) virtualCardRows {
	if m.renderTrace != nil {
		m.renderTrace.cardRecords++
	}
	record := column.index.recordAt(ordinal)
	task := projection.board.Tasks[record.projectionIndex]
	selected := m.boardView.column == statusIndex(column.status) && m.boardView.rows[statusIndex(column.status)] == ordinal
	opts, ordered := m.cardOptsForTask(m.themeStyles(), task, column.bodyWidth, column.density,
		ordinal, column.metaDepth, selected)
	lines, spans := widget.CardWithSpans(m.themeStyles(), opts)
	rowSpans := make([][]labelSpan, len(lines))
	for _, span := range spans {
		rowSpans[span.Row] = append(rowSpans[span.Row], labelSpan{x0: span.X0, x1: span.X1, tag: ordered[span.Index]})
	}
	for rowIndex, line := range lines {
		for spanIndex := len(rowSpans[rowIndex]) - 1; spanIndex >= 0; spanIndex-- {
			span := rowSpans[rowIndex][spanIndex]
			line = ansiCutWithPointer(m, task.ID, span, line)
		}
		lines[rowIndex] = line
	}
	return virtualCardRows{lines: lines, spans: rowSpans}
}

func ansiCutWithPointer(m Model, taskID string, span labelSpan, line string) string {
	width := ansi.StringWidth(line)
	return ansi.Cut(line, 0, span.x0) +
		m.pointerState.Render(m.themeStyles(), boardCardLabelControlID(taskID, span.tag), ansi.Cut(line, span.x0, span.x1)) +
		ansi.Cut(line, span.x1, width)
}

func projectedColumnMetaLine(styles *theme.Styles, projection *renderProjection, column columnLayoutGeometry) string {
	blocked := projection.summaries[statusIndexExact(column.status)].blocked
	line := strings.TrimSpace(cardCountLabel(column.index.length))
	if blocked > 0 {
		line += " " + styles.Glyph.Bullet + " " + integerLabel(blocked) + " blocked"
	}
	return line
}

func cardCountLabel(count int) string {
	if count == 1 {
		return "1 card"
	}
	return integerLabel(count) + " cards"
}

func integerLabel(value int) string { return strconv.Itoa(value) }

func (c columnLayoutGeometry) visibleEnd(start int) int {
	limit := min(start+c.contentHeight, c.totalRows())
	end := start
	for row := start; row < limit; {
		ordinal := c.cardAtRow(row)
		record := c.index.recordAt(ordinal)
		cardStart := c.index.prefixAt(ordinal)
		cardEnd := cardStart + record.height
		next := c.index.prefixAt(ordinal + 1)
		if row >= cardEnd {
			row = min(next, limit)
			end = row
			continue
		}
		if cardEnd > limit {
			break
		}
		row = cardEnd
		end = row
		if next > row && next <= limit {
			row = next
			end = row
		}
	}
	if end == start {
		return limit
	}
	return end
}

func (c columnLayoutGeometry) hiddenCards(end, start int) int {
	if c.index.length == 0 || end >= c.totalRows() {
		return 0
	}
	ordinal := c.cardAtRow(max(end, start))
	if c.index.prefixAt(ordinal)+c.index.recordAt(ordinal).height <= end {
		ordinal++
	}
	return max(c.index.length-ordinal, 0)
}

func (r geometryWorkRequest) command() tea.Cmd {
	return func() tea.Msg { return measureGeometrySlice(r) }
}

func measureGeometrySlice(request geometryWorkRequest) geometryBatchMsg {
	now := request.now
	if now == nil {
		now = time.Now
	}
	started := now()
	message := geometryBatchMsg{
		generation: request.generation, sourceRootRevision: request.rootRevision,
		sequence: request.sequence, next: request.cursor,
	}
	prepared := request.geometry.deepClone()
	flat := 0
	affected := [len(boardStatuses)]bool{}
	model := Model{styles: request.styles, height: request.height, renderedAt: request.renderedAt}
	stop := false
	for columnIndex := range prepared.columns {
		column := &prepared.columns[columnIndex]
		for ordinal := 0; ordinal < column.index.length; ordinal++ {
			record := column.index.recordAt(ordinal)
			if flat < request.cursor {
				flat++
				continue
			}
			flat++
			message.next = flat
			if record.exact {
				continue
			}
			height := measureCardLayout(model, request.projection, *column, ordinal)
			record.height = max(height, 1)
			record.exact = true
			column.index, _ = column.index.withRecord(ordinal, record)
			affected[columnIndex] = true
			message.measured++
			if now().Sub(started) >= request.styles.Timing.GeometrySliceTarget {
				stop = true
				break
			}
		}
		if stop {
			break
		}
	}
	for index, changed := range affected {
		if !changed {
			continue
		}
		prepared.columns[index].refreshMedian()
	}
	prepared.refreshDerived()
	prepared.rootRevision = request.rootRevision + 1
	message.prepared = prepared
	message.complete = !stop && prepared.unresolvedRecords == 0
	message.elapsed = now().Sub(started)
	return message
}
