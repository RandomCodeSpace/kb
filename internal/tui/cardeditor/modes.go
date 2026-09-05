package cardeditor

import (
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/formview"
	"github.com/RandomCodeSpace/kb/internal/tui/pointer"
)

func (m *Model) enterPreview() {
	if m.preview {
		return
	}
	m.preview = true
	m.previewFocus = m.focus
	m.previewScroll = 0
	m.invalidatePointerMode()
}

func (m *Model) leavePreview() {
	if !m.preview {
		return
	}
	m.preview = false
	if m.previewFocus != "" {
		m.focus = m.previewFocus
	}
	m.previewFocus = ""
	m.applyFocus()
	m.invalidatePointerMode()
}

func (m *Model) enterTerminalSelection() {
	snapshot := safeMarkdownSource(m.desc.Value())
	if strings.TrimSpace(snapshot) == "" {
		m.statusMessage, m.statusIsError = "description is empty", false
		return
	}
	m.terminalSelection = true
	m.terminalSnapshot = snapshot
	m.terminalOffset = 0
	m.invalidatePointerMode()
}

func (m *Model) updateTerminalSelection(key string) {
	offset, exit := formview.UpdateTerminalText(m.terminalSnapshot, m.terminalOffset, m.width, m.height, key)
	m.terminalOffset = offset
	if !exit {
		return
	}
	m.terminalSelection = false
	m.terminalSnapshot = ""
	m.terminalOffset = 0
	m.invalidatePointerMode()
}

func (m Model) TerminalSelectionActive() bool { return m.terminalSelection }

func (m Model) TerminalSelectionView(width, height int) string {
	return formview.TerminalTextView(m.terminalSnapshot, m.terminalOffset, width, height)
}

// Resize keeps terminal-native snapshot navigation aligned with the latest
// root dimensions even while the ordinary overlay renderer is bypassed.
func (m *Model) Resize(width, height int) {
	m.width, m.height = max(width, 1), max(height, 1)
}

func (m *Model) invalidatePointerMode() {
	m.pointerSession++
	m.pointerState = pointer.State{}
}

func safeMarkdownSource(source string) string {
	source = ansi.Strip(source)
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) {
			return r
		}
		return -1
	}, source)
}
