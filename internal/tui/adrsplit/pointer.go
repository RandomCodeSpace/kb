package adrsplit

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/pointer"
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
	frame := snapshot.layout(max(width, 1), max(height, 1))
	var hitMap pointer.Map
	session, generation := m.session, m.generation
	action := func(target string) pointer.Action {
		return func(pointer.Point) tea.Msg {
			return pointerActionMsg{target: target, session: session, generation: generation}
		}
	}
	for _, region := range snapshot.pointerRegions(frame, width, height) {
		hitMap.AddControl(controlID(region.target), region.Rect, action(region.target))
	}
	pane := pointer.Rect{X0: frame.x, Y0: frame.y, X1: frame.x + frame.width, Y1: frame.y + frame.height}
	if snapshot.guardClose {
		// The guard's controls live in the footer band, and their positions come
		// from the band the view itself built: an ADR is untrusted text and must
		// never be able to impersonate a control.
		footer := ansi.Strip(snapshot.confirmFooter())
		inset := snapshot.themeStyles().Metrics.OverlayInsetX
		for _, control := range []struct{ target, label string }{
			{target: "discard", label: guardDiscard},
			{target: "stay", label: guardStay},
		} {
			start := strings.Index(footer, control.label)
			if start < 0 {
				continue
			}
			rect := pointer.Rect{
				X0: frame.x + inset + start,
				Y0: frame.y + frame.height - 1,
				X1: frame.x + inset + start + ansi.StringWidth(control.label),
				Y1: frame.y + frame.height,
			}
			if clipped, ok := clipRect(rect, width, height); ok {
				hitMap.AddControl(controlID(control.target), clipped, action(control.target))
			}
		}
	}
	if snapshot.operation == "" && !snapshot.adding {
		hitMap.AddBackdrop(pointer.Rect{X1: max(width, 1), Y1: max(height, 1)}, pane, action("backdrop"))
	}
	if snapshot.stage == stageReview && snapshot.operation == "" && !snapshot.adding && !snapshot.guardClose {
		maxScroll := max(len(frame.rows)-frame.bodyHeight, 0)
		body := pointer.Rect{X0: pane.X0, Y0: pane.Y0 + 1, X1: pane.X1, Y1: max(pane.Y1-1, pane.Y0+1)}
		hitMap.AddWheel(body, func(delta int) tea.Msg {
			return pointerActionMsg{target: "scroll", session: session, generation: generation, scrollDelta: delta * 3, maxScroll: maxScroll}
		})
	}
	return hitMap.Handler()
}

func controlID(target string) pointer.ControlID { return pointer.ControlID("adrsplit." + target) }

type adrsplitRegion struct {
	pointer.Rect
	target string
}

// pointerRegions projects the rows the panel shows onto terminal cells. The
// target comes from the row itself, so a control is what the view says it is.
func (m *Model) pointerRegions(frame splitFrame, width, height int) []adrsplitRegion {
	regions := make([]adrsplitRegion, 0, len(frame.rows))
	for index, row := range frame.rows {
		if row.target == "" {
			continue
		}
		y := frame.y + 1 + index - frame.scroll
		if y < frame.y+1 || y >= frame.y+1+frame.bodyHeight {
			continue
		}
		rect, ok := clipRect(pointer.Rect{
			X0: frame.x, Y0: y,
			X1: frame.x + frame.width, Y1: y + 1,
		}, width, height)
		if !ok {
			continue
		}
		regions = append(regions, adrsplitRegion{Rect: rect, target: row.target})
	}
	return regions
}

// clipRect keeps a region inside the terminal grid it was measured against.
func clipRect(rect pointer.Rect, width, height int) (pointer.Rect, bool) {
	width, height = max(width, 1), max(height, 1)
	rect.X0 = max(rect.X0, 0)
	rect.Y0 = max(rect.Y0, 0)
	rect.X1 = min(rect.X1, width)
	rect.Y1 = min(rect.Y1, height)
	if rect.X0 >= rect.X1 || rect.Y0 >= rect.Y1 {
		return pointer.Rect{}, false
	}
	return rect, true
}
