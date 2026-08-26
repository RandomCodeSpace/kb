//go:build prototype

// Command kb-render-prototype is a disposable large-board rendering experiment.
// It deliberately does not read KB_DATA or persist changes.
package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
	"github.com/RandomCodeSpace/kb/internal/tui/widget"
)

const (
	defaultWidth  = 160
	defaultHeight = 44
	columnCount   = 4
	frameInterval = 50 * time.Millisecond
	workSlice     = 8 * time.Millisecond
)

var statusNames = [columnCount]string{"TODO", "DOING", "REVIEW", "DONE"}

var retainedViewSink tea.View

type task struct {
	id         int
	seq        string
	title      string
	desc       string
	labels     []string
	searchText string
	status     int
	priority   int
}

type cardHit struct {
	taskID int
	column int
	row    int
	x0     int
	x1     int
	y0     int
	y1     int
}

type selectMsg struct {
	column int
	row    int
}

type scrollMsg struct {
	column int
	delta  int
}

type hoverMsg struct{ taskID int }

type measuredBatchMsg struct {
	generation int
	next       int
	heights    map[int]int
}

type model struct {
	records    []task
	projected  [columnCount][]int
	offsets    [columnCount]int
	styles     *theme.Styles
	width      int
	height     int
	focusCol   int
	focusRow   int
	hoverID    int
	query      string
	filtering  bool
	helpOpen   bool
	generation int
	heights    map[int]int
	visible    map[int]struct{}
	base       tea.View
	cached     tea.View
	lastBuild  time.Duration
	builds     uint64
	discarded  uint64
	lastNav    time.Time
}

func main() {
	count := flag.Int("tasks", 120, "number of deterministic synthetic tasks")
	bench := flag.Bool("bench", false, "run the 120/500/1000 headless comparison")
	flag.Parse()
	if *bench {
		runBenchmarks()
		return
	}
	if *count < 1 {
		fmt.Fprintln(os.Stderr, "tasks must be at least 1")
		os.Exit(2)
	}
	m := newModel(*count, defaultWidth, defaultHeight)
	program := tea.NewProgram(m, tea.WithFilter(inputFilter))
	if _, err := program.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "prototype: %v\n", err)
		os.Exit(1)
	}
}

func newModel(count, width, height int) *model {
	m := &model{
		records:    makeTasks(count),
		styles:     theme.New(true),
		width:      width,
		height:     height,
		hoverID:    -1,
		heights:    make(map[int]int, count),
		visible:    make(map[int]struct{}),
		generation: 1,
	}
	m.project("")
	m.rebuild()
	return m
}

func (m *model) Init() tea.Cmd { return m.measureCommand(0, m.generation) }

func (m *model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		if msg.Width <= 0 || msg.Height <= 0 || (msg.Width == m.width && msg.Height == m.height) {
			return m, nil
		}
		m.width, m.height = msg.Width, msg.Height
		m.generation++
		m.heights = make(map[int]int, len(m.records))
		m.clampFocus()
		m.rebuild()
		return m, m.measureCommand(0, m.generation)
	case measuredBatchMsg:
		if msg.generation != m.generation {
			return m, nil
		}
		for id, height := range msg.heights {
			m.heights[id] = height
		}
		if msg.next < len(m.records) {
			return m, m.measureCommand(msg.next, msg.generation)
		}
		return m, nil
	case selectMsg:
		if msg.column < 0 || msg.column >= columnCount || msg.row < 0 || msg.row >= len(m.projected[msg.column]) {
			return m, nil
		}
		m.focusCol, m.focusRow = msg.column, msg.row
		m.ensureFocusVisible()
		m.rebuild()
		return m, nil
	case scrollMsg:
		if !m.scroll(msg.column, msg.delta) {
			m.discarded++
			return m, nil
		}
		m.rebuild()
		return m, nil
	case hoverMsg:
		if msg.taskID == m.hoverID {
			return m, nil
		}
		m.hoverID = msg.taskID
		m.rebuild()
		return m, nil
	case tea.KeyPressMsg:
		return m.updateKey(msg)
	}
	return m, nil
}

func (m *model) View() tea.View { return m.cached }

func (m *model) updateKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.filtering {
		switch key {
		case "esc":
			m.filtering = false
			m.rebuild()
		case "enter":
			m.filtering = false
			m.rebuild()
		case "backspace":
			if m.query != "" {
				m.query = strings.TrimSuffix(m.query, string([]rune(m.query)[len([]rune(m.query))-1]))
				m.project(m.query)
				m.rebuild()
			}
		default:
			if msg.Text != "" {
				m.query += msg.Text
				m.project(m.query)
				m.rebuild()
			}
		}
		return m, nil
	}
	if m.helpOpen {
		if key == "?" || key == "esc" || key == "q" {
			m.helpOpen = false
			m.cached = m.base
		}
		return m, nil
	}
	switch key {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "/":
		m.filtering = true
		m.rebuild()
	case "?":
		m.helpOpen = true
		m.rebuildOverlay()
	case "up", "k":
		if !m.moveVertical(-1) {
			m.discarded++
			return m, nil
		}
		m.rebuild()
	case "down", "j":
		if !m.moveVertical(1) {
			m.discarded++
			return m, nil
		}
		m.rebuild()
	case "left", "h":
		if !m.moveHorizontal(-1) {
			m.discarded++
			return m, nil
		}
		m.rebuild()
	case "right", "l":
		if !m.moveHorizontal(1) {
			m.discarded++
			return m, nil
		}
		m.rebuild()
	}
	return m, nil
}

func inputFilter(current tea.Model, message tea.Msg) tea.Msg {
	m, ok := current.(*model)
	if !ok || m.filtering || m.helpOpen {
		return message
	}
	key, ok := message.(tea.KeyPressMsg)
	if !ok {
		return message
	}
	direction := 0
	switch key.String() {
	case "up", "k":
		direction = -1
	case "down", "j":
		direction = 1
	default:
		return message
	}
	if m.verticalBoundary(direction) {
		m.discarded++
		return nil
	}
	now := time.Now()
	if key.IsRepeat || (!m.lastNav.IsZero() && now.Sub(m.lastNav) < frameInterval) {
		if now.Sub(m.lastNav) < frameInterval {
			m.discarded++
			return nil
		}
	}
	m.lastNav = now
	return message
}

func (m *model) project(query string) {
	for column := range m.projected {
		m.projected[column] = m.projected[column][:0]
		m.offsets[column] = 0
	}
	needle := strings.ToLower(strings.TrimSpace(query))
	for i := range m.records {
		if needle == "" || strings.Contains(m.records[i].searchText, needle) {
			column := m.records[i].status
			m.projected[column] = append(m.projected[column], i)
		}
	}
	m.clampFocus()
}

func (m *model) clampFocus() {
	if m.focusCol < 0 || m.focusCol >= columnCount {
		m.focusCol = 0
	}
	if len(m.projected[m.focusCol]) == 0 {
		for column := range m.projected {
			if len(m.projected[column]) > 0 {
				m.focusCol = column
				break
			}
		}
	}
	if m.focusRow >= len(m.projected[m.focusCol]) {
		m.focusRow = max(0, len(m.projected[m.focusCol])-1)
	}
}

func (m *model) moveVertical(delta int) bool {
	if m.verticalBoundary(delta) {
		return false
	}
	m.focusRow += delta
	m.ensureFocusVisible()
	return true
}

func (m *model) verticalBoundary(delta int) bool {
	if len(m.projected[m.focusCol]) == 0 {
		return true
	}
	next := m.focusRow + delta
	return next < 0 || next >= len(m.projected[m.focusCol])
}

func (m *model) moveHorizontal(delta int) bool {
	for column := m.focusCol + delta; column >= 0 && column < columnCount; column += delta {
		if len(m.projected[column]) == 0 {
			continue
		}
		m.focusCol = column
		m.focusRow = min(m.focusRow, len(m.projected[column])-1)
		m.ensureFocusVisible()
		return true
	}
	return false
}

func (m *model) ensureFocusVisible() {
	column := m.focusCol
	if m.focusRow < m.offsets[column] {
		m.offsets[column] = m.focusRow
	}
	last := m.lastVisibleRow(column)
	if m.focusRow > last {
		m.offsets[column] = m.focusRow
	}
}

func (m *model) lastVisibleRow(column int) int {
	last := m.offsets[column] - 1
	for _, hit := range m.currentHits() {
		if hit.column == column {
			last = max(last, hit.row)
		}
	}
	return last
}

func (m *model) scroll(column, delta int) bool {
	if column < 0 || column >= columnCount || len(m.projected[column]) == 0 {
		return false
	}
	next := min(max(m.offsets[column]+delta, 0), len(m.projected[column])-1)
	if next == m.offsets[column] {
		return false
	}
	m.offsets[column] = next
	return true
}

func (m *model) rebuild() {
	started := time.Now()
	width := max(m.width, 40)
	height := max(m.height, 10)
	gutter := 1
	columnWidth := max(8, (width-(columnCount-1)*gutter)/columnCount)
	bodyRows := max(1, height-3)
	columns := make([][]string, columnCount)
	hits := make([]cardHit, 0, columnCount*8)
	visible := make(map[int]struct{})
	for column := range columns {
		rows := make([]string, 0, bodyRows+1)
		header := fmt.Sprintf(" %s  %d ", statusNames[column], len(m.projected[column]))
		rows = append(rows, fit(m.styles.OnBold(theme.FgBase, theme.Canvas).Render(header), columnWidth))
		used := 0
		for row := m.offsets[column]; row < len(m.projected[column]) && used < bodyRows; row++ {
			taskIndex := m.projected[column][row]
			record := m.records[taskIndex]
			opts := m.cardOptions(record, columnWidth, taskIndex == m.selectedTaskIndex(), record.id == m.hoverID)
			cardRows, _ := widget.CardWithSpans(m.styles, opts)
			m.heights[record.id] = len(cardRows)
			if used+len(cardRows) > bodyRows && used > 0 {
				break
			}
			visible[record.id] = struct{}{}
			top := 2 + used
			for _, cardRow := range cardRows {
				if used >= bodyRows {
					break
				}
				rows = append(rows, fit(cardRow, columnWidth))
				used++
			}
			x0 := column * (columnWidth + gutter)
			hits = append(hits, cardHit{taskID: record.id, column: column, row: row, x0: x0, x1: x0 + columnWidth - 1, y0: top, y1: 1 + used})
		}
		for used < bodyRows {
			rows = append(rows, strings.Repeat(" ", columnWidth))
			used++
		}
		columns[column] = rows
	}
	boardRows := make([]string, bodyRows+1)
	for row := range boardRows {
		parts := make([]string, 0, columnCount*2-1)
		for column := range columns {
			if column > 0 {
				parts = append(parts, strings.Repeat(" ", gutter))
			}
			parts = append(parts, columns[column][row])
		}
		boardRows[row] = lipgloss.JoinHorizontal(lipgloss.Top, parts...)
	}
	top := fit(m.styles.OnBold(theme.FgBase, theme.Canvas).Render(" KB RENDER PIPELINE PROTOTYPE  synthetic only"), width)
	mode := "READY"
	if m.filtering {
		mode = "FILTER /" + m.query
	} else if m.query != "" {
		mode = "FILTERED /" + m.query
	}
	footer := fit(m.styles.On(theme.FgMuted, theme.Canvas).Render(
		fmt.Sprintf(" %s | arrows/hjkl move | wheel scroll | / filter | ? help | q quit | build %s",
			mode, m.lastBuild.Round(time.Microsecond))), width)
	contentRows := append([]string{top}, boardRows...)
	contentRows = append(contentRows, footer)
	content := strings.Join(contentRows, "\n")
	view := tea.NewView(content)
	view.AltScreen = true
	view.MouseMode = tea.MouseModeAllMotion
	view.OnMouse = pointerHandler(hits, m.offsets, m.projected)
	m.base = view
	m.cached = m.base
	m.visible = visible
	m.lastBuild = time.Since(started)
	m.builds++
	if m.helpOpen {
		m.rebuildOverlay()
	}
}

func (m *model) rebuildOverlay() {
	view := tea.NewView(m.overlay(m.base.Content, max(m.width, 40), max(m.height, 10)))
	view.AltScreen = true
	view.MouseMode = tea.MouseModeAllMotion
	m.cached = view
}

func (m *model) overlay(backdrop string, width, height int) string {
	lines := strings.Split(backdrop, "\n")
	panel := []string{
		"RENDER PIPELINE PROTOTYPE",
		"",
		"View returns a retained complete frame.",
		"Only visible cards are styled and hit-tested.",
		"Offscreen geometry is measured in 8ms commands.",
		"Held vertical navigation is capped at 20 frames/sec.",
		"Boundary repeats and boundary wheel events are discarded.",
		"",
		"Press ? or Esc to close.",
	}
	panelWidth := min(66, max(30, width-8))
	x := max(0, (width-panelWidth)/2)
	y := max(1, (height-len(panel))/2)
	for i, text := range panel {
		if y+i >= len(lines) {
			break
		}
		line := fit(m.styles.On(theme.FgBase, theme.Raised).Render(" "+text), panelWidth)
		lines[y+i] = splice(lines[y+i], line, x, panelWidth)
	}
	return strings.Join(lines, "\n")
}

func (m *model) cardOptions(record task, width int, selected, hovered bool) widget.CardOpts {
	density := m.styles.Metrics.DensityFor(m.height, m.styles.Metrics.CardInner(width, theme.DensityNormal))
	return widget.CardOpts{
		Title: record.title, Seq: record.seq, Desc: record.desc, Labels: record.labels,
		Priority: record.priority, Selected: selected, Hovered: hovered, Width: width, Density: density,
		TitleLines: m.styles.Metrics.TitleRows(m.height, density),
		DescLines:  m.styles.Metrics.DescLines(m.height, density),
		LabelRows:  m.styles.Metrics.LabelRows(m.height, density),
		PadRows:    m.styles.Metrics.InnerPadRows(m.height, density),
		Meta:       []string{"P" + fmt.Sprint(record.priority), "2d"},
	}
}

func (m *model) selectedTaskIndex() int {
	if m.focusCol < 0 || m.focusCol >= columnCount || m.focusRow < 0 || m.focusRow >= len(m.projected[m.focusCol]) {
		return -1
	}
	return m.projected[m.focusCol][m.focusRow]
}

func (m *model) currentHits() []cardHit {
	// The immutable pointer closure owns the actual hit map. This reconstruction
	// is used only to keep keyboard focus within the visible card window.
	last := make([]cardHit, 0, len(m.visible))
	for column := range m.projected {
		row := m.offsets[column]
		for row < len(m.projected[column]) {
			id := m.records[m.projected[column][row]].id
			if _, ok := m.visible[id]; !ok {
				break
			}
			last = append(last, cardHit{column: column, row: row})
			row++
		}
	}
	return last
}

func pointerHandler(hits []cardHit, offsets [columnCount]int, projected [columnCount][]int) func(tea.MouseMsg) tea.Cmd {
	immutableHits := append([]cardHit(nil), hits...)
	return func(message tea.MouseMsg) tea.Cmd {
		mouse := message.Mouse()
		column := -1
		for i := range immutableHits {
			hit := immutableHits[i]
			if mouse.X >= hit.x0 && mouse.X <= hit.x1 {
				column = hit.column
			}
			if mouse.X >= hit.x0 && mouse.X <= hit.x1 && mouse.Y >= hit.y0 && mouse.Y <= hit.y1 {
				switch message.(type) {
				case tea.MouseMotionMsg:
					return func() tea.Msg { return hoverMsg{taskID: hit.taskID} }
				case tea.MouseClickMsg:
					return func() tea.Msg { return selectMsg{column: hit.column, row: hit.row} }
				}
			}
		}
		if _, motion := message.(tea.MouseMotionMsg); motion {
			return func() tea.Msg { return hoverMsg{taskID: -1} }
		}
		wheel, ok := message.(tea.MouseWheelMsg)
		if !ok || column < 0 {
			return nil
		}
		delta := 0
		switch wheel.Button {
		case tea.MouseWheelUp:
			delta = -1
		case tea.MouseWheelDown:
			delta = 1
		default:
			return nil
		}
		next := offsets[column] + delta
		if next < 0 || next >= len(projected[column]) {
			return nil
		}
		return func() tea.Msg { return scrollMsg{column: column, delta: delta} }
	}
}

func (m *model) measureCommand(start, generation int) tea.Cmd {
	records := m.records
	width := max(8, (max(m.width, 40)-(columnCount-1))/columnCount)
	height := m.height
	styles := m.styles
	return func() tea.Msg {
		began := time.Now()
		heights := make(map[int]int)
		next := start
		for next < len(records) {
			record := records[next]
			density := styles.Metrics.DensityFor(height, styles.Metrics.CardInner(width, theme.DensityNormal))
			opts := widget.CardOpts{
				Title: record.title, Seq: record.seq, Desc: record.desc, Labels: record.labels,
				Priority: record.priority, Width: width, Density: density,
				TitleLines: styles.Metrics.TitleRows(height, density), DescLines: styles.Metrics.DescLines(height, density),
				LabelRows: styles.Metrics.LabelRows(height, density), PadRows: styles.Metrics.InnerPadRows(height, density),
				Meta: []string{"P" + fmt.Sprint(record.priority), "2d"},
			}
			heights[record.id] = widget.CardHeight(styles, opts)
			next++
			if time.Since(began) >= workSlice {
				break
			}
		}
		return measuredBatchMsg{generation: generation, next: next, heights: heights}
	}
}

func makeTasks(count int) []task {
	titles := []string{
		"Keep keyboard navigation responsive under sustained input",
		"Reconcile external board changes without repainting idle frames",
		"Preserve pointer targets across filtered projections",
		"Render long descriptions and labels at terminal width",
		"Ship the boring path before inventing another abstraction",
	}
	descriptions := []string{
		"A deterministic fixture with enough prose to exercise wrapping and card geometry.",
		"This record intentionally varies its content so every card is not the same convenient rectangle.",
		"No database is touched. The prototype exists to invalidate architectural assumptions cheaply.",
		"",
	}
	labelSets := [][]string{{"tui", "performance"}, {"bug"}, {"architecture", "rendering", "large-board"}, nil}
	records := make([]task, count)
	for i := range records {
		title := fmt.Sprintf("%s %d", titles[i%len(titles)], i+1)
		desc := descriptions[(i/3)%len(descriptions)]
		labels := append([]string(nil), labelSets[(i/7)%len(labelSets)]...)
		seq := fmt.Sprintf("%d", i+1)
		search := strings.ToLower(strings.Join(append([]string{seq, title, desc}, labels...), " "))
		records[i] = task{id: i, seq: seq, title: title, desc: desc, labels: labels, searchText: search, status: i % columnCount, priority: i%4 + 1}
	}
	return records
}

func runBenchmarks() {
	fmt.Println("tasks startup_p95 measure_total max_slice input_p95 filter_p95 overlay_p95 retained_view heap_delta")
	for _, count := range []int{120, 500, 1000} {
		startup := make([]time.Duration, 15)
		for i := range startup {
			started := time.Now()
			_ = newModel(count, defaultWidth, defaultHeight)
			startup[i] = time.Since(started)
		}
		m := newModel(count, defaultWidth, defaultHeight)
		measureTotal, maxSlice := completeMeasurements(m)
		inputs := make([]time.Duration, 100)
		for i := range inputs {
			key := tea.KeyDown
			if i%2 == 1 {
				key = tea.KeyUp
			}
			started := time.Now()
			_, _ = m.Update(tea.KeyPressMsg{Code: key})
			_ = m.View()
			inputs[i] = time.Since(started)
		}
		filters := make([]time.Duration, 50)
		for i := range filters {
			query := "performance"
			if i%2 == 1 {
				query = "rendering"
			}
			started := time.Now()
			m.query = query
			m.project(query)
			m.rebuild()
			filters[i] = time.Since(started)
		}
		overlays := make([]time.Duration, 50)
		for i := range overlays {
			started := time.Now()
			m.helpOpen = true
			m.rebuildOverlay()
			m.helpOpen = false
			m.cached = m.base
			overlays[i] = time.Since(started)
		}
		started := time.Now()
		for range 100000 {
			retainedViewSink = m.View()
		}
		retained := time.Since(started) / 100000
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		memoryModel := newModel(count, defaultWidth, defaultHeight)
		_, _ = completeMeasurements(memoryModel)
		runtime.GC()
		runtime.ReadMemStats(&after)
		runtime.KeepAlive(memoryModel)
		heapDelta := int64(after.HeapAlloc) - int64(before.HeapAlloc)
		fmt.Printf("%4d %11s %13s %9s %9s %10s %11s %13s %9s\n", count, percentile(startup, 95), measureTotal, maxSlice, percentile(inputs, 95), percentile(filters, 95), percentile(overlays, 95), retained, bytes(heapDelta))
	}
}

func completeMeasurements(m *model) (time.Duration, time.Duration) {
	command := m.Init()
	var total time.Duration
	var maximum time.Duration
	for command != nil {
		started := time.Now()
		message := command()
		elapsed := time.Since(started)
		total += elapsed
		maximum = max(maximum, elapsed)
		_, command = m.Update(message)
	}
	return total, maximum
}

func percentile(values []time.Duration, percent int) time.Duration {
	ordered := append([]time.Duration(nil), values...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	index := (len(ordered)*percent + 99) / 100
	return ordered[min(max(index-1, 0), len(ordered)-1)]
}

func bytes(value int64) string {
	if value < 1024 {
		return fmt.Sprintf("%dB", value)
	}
	return fmt.Sprintf("%.1fMiB", float64(value)/(1024*1024))
}

func fit(content string, width int) string {
	if width <= 0 {
		return ""
	}
	content = ansi.Truncate(content, width, "")
	return content + strings.Repeat(" ", max(0, width-ansi.StringWidth(content)))
}

func splice(background, foreground string, x, width int) string {
	plain := ansi.Strip(background)
	left := ansi.Truncate(plain, x, "")
	right := ""
	if ansi.StringWidth(plain) > x+width {
		right = ansi.Cut(plain, x+width, ansi.StringWidth(plain))
	}
	return fit(left, x) + foreground + right
}
