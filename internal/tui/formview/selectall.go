package formview

import (
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Select-all emulation (issue #159, map #136). charm's textinput and textarea
// carry no selection model, so kb composes one: ctrl+a marks the whole content
// of the focused field, the next typed rune replaces it, Backspace or Delete
// clears it, any other key drops the mark and goes on to do its usual work.
//
// The emulation lives here, wrapped around the bubbles field models, rather
// than in the widget package: widget holds pure render helpers that keep no
// state of their own, and a mark is state plus key handling. formview is
// already the shared seam every overlay drives its text fields through.
//
// A surface routes a key through the mark BEFORE its own handling, so a field
// with a live mark is typing context and beats the pane's shortcuts. Escape is
// the one deliberate nuance of the contract: it drops the mark and is consumed,
// so the first Escape after ctrl+a does not also close the overlay.

// SelectAllKey is the binding, stated once so a surface can name it without
// spelling the string again.
const SelectAllKey = "ctrl+a"

// FieldModel is the part of a bubbles field model the mark drives. Both
// *textinput.Model and *textarea.Model satisfy it.
type FieldModel interface {
	Value() string
	SetValue(string)
}

// Mark is the select-all state of one surface. It holds the identifier of the
// marked field rather than a bare flag, so a mark never leaks onto a sibling
// field: a key routed for a different field finds no mark and behaves normally.
//
// The zero value is an unmarked surface.
type Mark struct {
	field string
}

// Active reports whether field currently carries the mark.
func (m *Mark) Active(field string) bool {
	return field != "" && m.field == field
}

// Drop clears the mark. Surfaces call it when focus moves by a route that never
// reaches the key handler, a pointer click being the one that matters.
func (m *Mark) Drop() {
	m.field = ""
}

// Input applies the emulation to a focused text input ahead of the field's own
// key handling. It reports whether the key was consumed; an unconsumed key must
// still be dispatched, because that is how a typed rune lands in the field the
// mark just emptied.
func (m *Mark) Input(field string, input *textinput.Model, msg tea.KeyPressMsg) bool {
	return m.apply(field, input, msg)
}

// Area is Input for a textarea.
func (m *Mark) Area(field string, area *textarea.Model, msg tea.KeyPressMsg) bool {
	return m.apply(field, area, msg)
}

func (m *Mark) apply(field string, model FieldModel, msg tea.KeyPressMsg) bool {
	if field == "" || model == nil {
		return false
	}
	key := msg.String()
	if key == SelectAllKey {
		// Marking an empty field marks nothing, but the key is still consumed:
		// ctrl+a is kb's select-all everywhere, never the line-start motion
		// bubbles binds it to by default (Home owns that, issue #158).
		m.field = ""
		if model.Value() != "" {
			m.field = field
		}
		return true
	}
	if !m.Active(field) {
		return false
	}
	m.field = ""
	switch key {
	case "esc":
		return true
	case "backspace", "delete":
		model.SetValue("")
		return true
	}
	if msg.Text != "" {
		// A printable key replaces the marked content: the field is emptied
		// here and the key itself is left to the field, which inserts it.
		model.SetValue("")
	}
	return false
}

// Selection applies the marked treatment to the style a view paints a field row
// with. The mark is the theme's pressed token - reverse video - set as an
// attribute on the row's own style rather than wrapped around it, which is the
// form widget.Button uses for the same token: a wrapping run is cancelled by
// the first reset the content emits, and the mark would vanish mid-row.
//
// Views that compose a field value into a line some other style paints use
// theme.Styles.PressedRun on the value instead; that form carries no reset and
// so survives composition.
func Selection(style lipgloss.Style, marked bool) lipgloss.Style {
	if !marked {
		return style
	}
	return style.Reverse(true)
}
