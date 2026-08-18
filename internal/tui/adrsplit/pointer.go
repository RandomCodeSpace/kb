package adrsplit

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/RandomCodeSpace/kb/internal/tui/pointer"
	"github.com/charmbracelet/x/ansi"
)

type pointerActionMsg struct {
	target      string
	session     uint64
	generation  uint64
	scrollDelta int
	maxScroll   int
}

// MouseHandler returns a release-only immutable map derived from the current
// rendered frame. Updating the model remains the caller's responsibility.
func (m *Model) MouseHandler(width, height int) func(tea.MouseMsg) tea.Cmd {
	if !m.open {
		return nil
	}
	snapshot := *m
	frame, frameWidth, frameHeight := snapshot.frame(width, height)
	regions := snapshot.pointerRegions(width, height, frameWidth, frameHeight)
	var hitMap pointer.Map
	session, generation := m.session, m.generation
	for _, region := range regions {
		region := region
		hitMap.AddControl(controlID(region.target), region.Rect, func(pointer.Point) tea.Msg {
			return pointerActionMsg{target: region.target, session: session, generation: generation}
		})
	}
	pane := pointer.Rect{X0: max((max(width, 1)-frameWidth)/2, 0), Y0: max((max(height, 1)-frameHeight)/2, 0), X1: max((max(width, 1)-frameWidth)/2, 0) + frameWidth, Y1: max((max(height, 1)-frameHeight)/2, 0) + frameHeight}
	if snapshot.guardClose {
		footerRow := len(strings.Split(frame, "\n")) - 2
		for _, control := range []struct {
			target string
			label  string
		}{{"discard", "[ Discard ]"}, {"stay", "[ Stay ]"}} {
			pressed := strings.Replace(strings.Replace(control.label, "[ ", "[>", 1), " ]", "<]", 1)
			if rect, ok := controlRectAtRow(frame, pane, footerRow, control.label, pressed); ok {
				target := control.target
				hitMap.AddControl(controlID(target), rect, func(pointer.Point) tea.Msg {
					return pointerActionMsg{target: target, session: session, generation: generation}
				})
			}
		}
	}
	if snapshot.operation == "" && !snapshot.adding {
		hitMap.AddBackdrop(pointer.Rect{X1: max(width, 1), Y1: max(height, 1)}, pane, func(pointer.Point) tea.Msg {
			return pointerActionMsg{target: "backdrop", session: session, generation: generation}
		})
	}
	if snapshot.stage == stageReview && snapshot.operation == "" && !snapshot.adding && !snapshot.guardClose {
		bodyHeight := max(frameHeight-4, 1)
		maxScroll := max(len(snapshot.bodyLines(max(frameWidth-4, 1)))-bodyHeight, 0)
		body := pointer.Rect{X0: pane.X0, Y0: pane.Y0 + 1, X1: pane.X1, Y1: max(pane.Y1-1, pane.Y0+1)}
		hitMap.AddWheel(body, func(delta int) tea.Msg {
			return pointerActionMsg{target: "scroll", session: session, generation: generation, scrollDelta: delta * 3, maxScroll: maxScroll}
		})
	}
	return hitMap.Handler()
}

func controlID(target string) pointer.ControlID { return pointer.ControlID("adrsplit." + target) }

func controlRectAtRow(frame string, pane pointer.Rect, row int, needles ...string) (pointer.Rect, bool) {
	lines := strings.Split(ansi.Strip(frame), "\n")
	if row < 0 || row >= len(lines) {
		return pointer.Rect{}, false
	}
	for _, needle := range needles {
		if start := strings.Index(lines[row], needle); start >= 0 {
			return pointer.Rect{X0: pane.X0 + start, Y0: pane.Y0 + row, X1: pane.X0 + start + ansi.StringWidth(needle), Y1: pane.Y0 + row + 1}, true
		}
	}
	return pointer.Rect{}, false
}

type adrsplitRegion struct {
	pointer.Rect
	target string
}

func (m *Model) pointerRegions(width, height, frameWidth, frameHeight int) []adrsplitRegion {
	width, height = max(width, 1), max(height, 1)
	x0 := max((width-frameWidth)/2, 0)
	y0 := max((height-frameHeight)/2, 0)
	body := m.bodyLines(max(frameWidth-4, 1))
	targets := m.controlRows()
	regions := make([]adrsplitRegion, 0, len(targets))
	for line, target := range targets {
		if line < m.scroll || line >= m.scroll+len(body) || line-m.scroll >= frameHeight-2 {
			continue
		}
		rect := pointer.Rect{X0: x0, Y0: y0 + 1 + line - m.scroll, X1: x0 + frameWidth, Y1: y0 + 2 + line - m.scroll}
		if rect.X0 < 0 {
			rect.X0 = 0
		}
		if rect.Y0 < 0 {
			rect.Y0 = 0
		}
		if rect.X1 > width {
			rect.X1 = width
		}
		if rect.Y1 > height {
			rect.Y1 = height
		}
		if rect.X0 >= rect.X1 || rect.Y0 >= rect.Y1 {
			continue
		}
		regions = append(regions, adrsplitRegion{Rect: rect, target: target})
	}
	return regions
}

func (m Model) controlRows() map[int]string {
	targets := make(map[int]string)
	line := 0
	if m.stage == stageInput {
		line += 2
		targets[line] = "source"
		line++
		if m.source == sourcePaste {
			for row := 0; row < 9; row++ {
				targets[line+row] = "adr"
			}
			line += 9
			line++
		} else {
			targets[line] = "file"
			line += 2
		}
		targets[line] = "max"
		line += 2
		targets[line], targets[line+1] = "cancel", "split"
		return targets
	}
	line = 3
	for i, row := range m.rows {
		if row.created {
			line += 6
			continue
		}
		targets[line] = fmt.Sprintf("include:%d", i)
		targets[line+1] = fmt.Sprintf("title:%d", i)
		targets[line+2] = fmt.Sprintf("prio:%d", i)
		targets[line+3] = fmt.Sprintf("effort:%d", i)
		line += 4
		if row.err != "" {
			line++
		}
		line++
	}
	targets[line] = "dest"
	targets[line+2] = "back"
	targets[line+3] = "cancel"
	targets[line+4] = "add"
	return targets
}
