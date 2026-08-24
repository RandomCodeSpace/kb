package tui

import (
	tea "charm.land/bubbletea/v2"
)

// The concurrency ceiling and background handoff of spec section 10.2.6.
//
// spin.MaxEngines branded engines may tick at once and the one that does
// belongs to the front-most open surface in the section 4 z-order. crush pauses
// an item's animation when it scrolls out of the visible window; kb's
// equivalent of offscreen is "behind another overlay", because the z-order
// stack lets card detail, settings, the editor and issue import all be open
// with only the last one painted.
//
// The handoff is a remount rather than a resume. Resuming mid-wipe would need a
// step counter persisted across a stretch in which no ticks arrived, which is
// either a wall-clock read - determinism contract point 4 - or a frame count
// that lies. A second wipe on return reads as the surface waking up, which is
// what happened.

// surface names one entry of the z-order stack, top first.
type surface uint8

const (
	surfaceNone surface = iota
	surfacePalette
	surfaceHelp
	surfaceImport
	surfaceAction
	surfaceEditor
	surfaceADR
	surfaceSettings
	surfaceDetail
	surfaceBoard
)

// frontSurface is the top of the open z-order stack. The order is the one
// route() dispatches pointer messages in, which is the z-order itself.
func (m Model) frontSurface() surface {
	switch {
	case m.palette.IsOpen():
		return surfacePalette
	case m.helpOpen:
		return surfaceHelp
	case m.issueImport.IsOpen():
		return surfaceImport
	case m.action.open():
		return surfaceAction
	case m.editor.IsOpen():
		return surfaceEditor
	case m.adr.IsOpen():
		return surfaceADR
	case m.settings != nil:
		return surfaceSettings
	case m.detail.IsOpen():
		return surfaceDetail
	default:
		return surfaceBoard
	}
}

// syncEngines hands the branded tier to the front-most surface and takes it
// away from every other one. It is called after every routed message, so a
// surface that opens over a busy one stops that one's engine on the same
// update the overlay appeared in.
//
// Each SetFrontMost returns a nil command unless the surface actually changed
// side, so a steady stack costs one comparison per branded surface per message.
func (m *Model) syncEngines() tea.Cmd {
	front := m.frontSurface()
	return batchCommands(
		m.issueImport.SetFrontMost(front == surfaceImport),
		m.editor.SetFrontMost(front == surfaceEditor),
		m.adr.SetFrontMost(front == surfaceADR),
		m.settings.SetFrontMost(front == surfaceSettings),
		m.detail.SetFrontMost(front == surfaceDetail),
	)
}

// mountedEngines counts the branded engines ticking across the open stack. It
// may never exceed spin.MaxEngines; internal/tui/model_test.go is the assertion.
func (m Model) mountedEngines() int {
	count := 0
	for _, mounted := range []bool{
		m.issueImport.BrandMounted(),
		m.editor.BrandMounted(),
		m.adr.BrandMounted(),
		m.settings.BrandMounted(),
		m.detail.BrandMounted(),
	} {
		if mounted {
			count++
		}
	}
	return count
}
