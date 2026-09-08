package carddetail

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/formview"
)

type selectableRow struct {
	row   int
	text  string
	width int
	block int
}

type textPoint struct {
	row  int
	cell int
}

type textSelection struct {
	active   bool
	anchor   textPoint
	head     textPoint
	moved    bool
	clickSeq int
}

type selectionStartMsg struct {
	point textPoint
	seq   int
}

type selectionMoveMsg struct{ point textPoint }
type selectionEndMsg struct{ point textPoint }

func selectableRows(start, block, panelWidth, inset int, rows []string) []selectableRow {
	out := make([]selectableRow, 0, len(rows))
	field := max(panelWidth-2*inset, 0)
	for index, rendered := range rows {
		plain := ansi.Strip(rendered)
		text := strings.TrimRight(ansi.Cut(plain, inset, inset+field), " ")
		out = append(out, selectableRow{row: start + index, text: text, width: ansi.StringWidth(text), block: block})
	}
	return out
}

func (m Model) selectablePoint(logicalRow, panelCell int) (textPoint, bool) {
	for _, row := range m.selectable {
		if row.row != logicalRow || row.width == 0 {
			continue
		}
		cell := min(max(panelCell-m.styles.Metrics.OverlayInsetX, 0), row.width-1)
		return textPoint{row: logicalRow, cell: cell}, true
	}
	return textPoint{}, false
}

func (m Model) nearestSelectablePoint(logicalRow, panelCell int) (textPoint, bool) {
	if point, ok := m.selectablePoint(logicalRow, panelCell); ok {
		return point, true
	}
	best := -1
	for index, row := range m.selectable {
		if row.width == 0 {
			continue
		}
		if best < 0 || abs(row.row-logicalRow) < abs(m.selectable[best].row-logicalRow) {
			best = index
		}
	}
	if best < 0 {
		return textPoint{}, false
	}
	row := m.selectable[best]
	cell := min(max(panelCell-m.styles.Metrics.OverlayInsetX, 0), row.width-1)
	return textPoint{row: row.row, cell: cell}, true
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func orderedPoints(left, right textPoint) (textPoint, textPoint) {
	if left.row > right.row || left.row == right.row && left.cell > right.cell {
		return right, left
	}
	return left, right
}

func (m Model) selectedText() string {
	if !m.textSelection.active || !m.textSelection.moved {
		return ""
	}
	first, last := orderedPoints(m.textSelection.anchor, m.textSelection.head)
	var lines []string
	previous := -1
	for _, row := range m.selectable {
		if row.row < first.row || row.row > last.row {
			continue
		}
		start, end := 0, row.width
		if row.row == first.row {
			start = first.cell
		}
		if row.row == last.row {
			end = min(last.cell+1, row.width)
		}
		if end <= start {
			continue
		}
		if previous >= 0 && row.row-previous > 1 {
			lines = append(lines, "")
		}
		lines = append(lines, ansi.Cut(row.text, start, end))
		previous = row.row
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

func (m Model) allSelectableText() string {
	var lines []string
	previous, block := -1, -1
	for _, row := range m.selectable {
		if previous >= 0 && (row.block != block || row.row-previous > 1) {
			lines = append(lines, "")
		}
		lines = append(lines, row.text)
		previous, block = row.row, row.block
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func (m *Model) clearTextSelection() bool {
	if !m.textSelection.active {
		return false
	}
	m.textSelection = textSelection{}
	m.rebuildBody()
	return true
}

func (m *Model) markTextSelection() {
	if !m.textSelection.active || !m.textSelection.moved {
		return
	}
	first, last := orderedPoints(m.textSelection.anchor, m.textSelection.head)
	for _, row := range m.selectable {
		if row.row < first.row || row.row > last.row || row.row >= len(m.bodyLines) {
			continue
		}
		start, end := 0, row.width
		if row.row == first.row {
			start = first.cell
		}
		if row.row == last.row {
			end = min(last.cell+1, row.width)
		}
		if end <= start {
			continue
		}
		left := m.styles.Metrics.OverlayInsetX + start
		right := m.styles.Metrics.OverlayInsetX + end
		line := m.bodyLines[row.row]
		m.bodyLines[row.row] = ansi.Cut(line, 0, left) +
			m.styles.PressedRun(ansi.Cut(line, left, right)) +
			ansi.Cut(line, right, ansi.StringWidth(line))
	}
}

func (m *Model) ClearSelection() bool { return m.clearTextSelection() }

func (m *Model) selectAllText() bool {
	first, last := -1, -1
	for index, row := range m.selectable {
		if row.width == 0 {
			continue
		}
		if first < 0 {
			first = index
		}
		last = index
	}
	if first < 0 {
		return false
	}
	m.textSelection = textSelection{
		active: true, moved: true,
		anchor: textPoint{row: m.selectable[first].row},
		head:   textPoint{row: m.selectable[last].row, cell: m.selectable[last].width - 1},
	}
	m.rebuildBody()
	return true
}

func (m *Model) copySelection() tea.Cmd {
	text := m.selectedText()
	if text == "" {
		m.setStatus("Select description or comment text first", false)
		m.rebuildBody()
		return nil
	}
	m.setStatus("Copy requested", false)
	m.rebuildBody()
	return tea.SetClipboard(text)
}

func (m *Model) enterTerminalSelection() {
	snapshot := m.selectedText()
	if snapshot == "" {
		snapshot = m.allSelectableText()
	}
	if snapshot == "" {
		m.setStatus("No description or comment text", false)
		m.rebuildBody()
		return
	}
	m.resetPointerSession()
	m.terminalSelection = true
	m.terminalSnapshot = ansi.Strip(snapshot)
	m.terminalOffset = 0
}

func (m Model) TerminalSelectionActive() bool { return m.terminalSelection }

func (m Model) TerminalSelectionView(width, height int) string {
	return formview.TerminalTextView(m.terminalSnapshot, m.terminalOffset, width, height)
}

func (m *Model) updateTerminalSelection(key string) {
	offset, exit := formview.UpdateTerminalText(m.terminalSnapshot, m.terminalOffset, m.width, m.height, key)
	m.terminalOffset = offset
	if exit {
		m.resetPointerSession()
		m.terminalSelection = false
		m.terminalSnapshot = ""
	}
}
