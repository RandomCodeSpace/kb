// Package cmdpalette renders kb's ctrl+k command palette: a fuzzy search over
// the action registry, elevated as the overlay of spec section 4.
//
// The palette owns no actions of its own. It reads internal/tui/action, and
// when the user picks a row it hands the root model back the key press that row
// stands for, so the board's existing handler runs the action. That is what
// keeps the palette from becoming a second, divergent dispatch: there is one
// keymap, one help pane and one palette, all reading one table.
package cmdpalette

import (
	"strings"
	"sync"
	"unicode"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/action"
	"github.com/RandomCodeSpace/kb/internal/tui/formview"
	"github.com/RandomCodeSpace/kb/internal/tui/pointer"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// queryMarkField is the query field's name in the select-all mark. The mark
// holds a field name rather than a flag so it can never leak onto a sibling.
const queryMarkField = "query"

// queryWidth is the query input's own buffer width. The rendered field is
// clipped to the panel by the view; this only bounds the component's internal
// viewport.
const queryWidth = 64

// Model is the palette. The zero value is a closed palette on a board with no
// optional features, which is a usable fallback rather than a crash.
type Model struct {
	open   bool
	query  textinput.Model
	mark   formview.Mark
	styles *theme.Styles

	features action.Features
	entries  []Entry
	cursor   int
	offset   int

	pointerState pointer.State
	generation   uint64

	choice action.Action
	chosen bool
}

// New builds a closed palette.
func New() Model {
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = "search actions"
	input.SetWidth(queryWidth)
	return Model{query: input}
}

// fallbackStyles is the palette's own theme for a caller that never handed it
// one, resolved once.
var fallbackStyles = sync.OnceValue(func() *theme.Styles { return theme.New(true) })

// SetStyles adopts the root model's resolved theme.
func (m *Model) SetStyles(styles *theme.Styles) {
	if styles == nil {
		return
	}
	m.styles = styles
}

// SetFeatures records what this board was built with, so the palette offers the
// same actions the help pane reports as enabled.
func (m *Model) SetFeatures(features action.Features) {
	m.features = features
	if m.open {
		m.refresh()
	}
}

func (m Model) themeStyles() *theme.Styles {
	if m.styles != nil {
		return m.styles
	}
	return fallbackStyles()
}

// IsOpen reports whether the palette is on screen.
func (m Model) IsOpen() bool { return m.open }

// Open clears the query and shows the palette. Opening is always a fresh
// search: a palette that reopened onto the last query would make the second
// use of the day guess what the first one was looking for.
func (m *Model) Open() tea.Cmd {
	m.open = true
	m.chosen = false
	m.choice = action.Action{}
	m.cursor, m.offset = 0, 0
	m.mark.Drop()
	m.query.SetValue("")
	m.query.Focus()
	m.refresh()
	return nil
}

// Close hides the palette and drops its state.
func (m *Model) Close() {
	m.open = false
	m.query.Blur()
	m.mark.Drop()
	m.entries = nil
	m.cursor, m.offset = 0, 0
	m.pointerState = pointer.State{}
	m.generation++
}

// ConsumeChoice reports the action the user ran, exactly once. The root model
// calls it after every Update and replays the key of whatever comes back.
func (m *Model) ConsumeChoice() (action.Action, bool) {
	if !m.chosen {
		return action.Action{}, false
	}
	m.chosen = false
	choice := m.choice
	m.choice = action.Action{}
	return choice, true
}

// Update handles one message while the palette is open.
func (m *Model) Update(message tea.Msg) tea.Cmd {
	if !m.open {
		return nil
	}
	if pointer.IsMessage(message) {
		next, command, _ := m.pointerState.Update(message)
		m.pointerState = next
		return command
	}
	if activation, ok := message.(pointerActionMsg); ok {
		return m.pointerAction(activation)
	}
	if wheel, ok := message.(pointerWheelMsg); ok {
		if wheel.generation == m.generation && len(m.entries) > 0 {
			m.cursor = min(max(wheel.target, 0), len(m.entries)-1)
		}
		return nil
	}
	msg, ok := message.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	// Spec section 10.5.2 rows 7 and 8, in that order: a motion key adopts the
	// hovered anchor and then moves from it, so down on a hovered row 7 lands on
	// row 8 rather than on cursor+1; any other key runs unadopted against the
	// keyboard cursor, because a key typed without looking at the mouse must not
	// be redirected by it. Both turn mouse mode off.
	m.cursor, m.pointerState = m.machine().Adopt(m.pointerState, isMotionKey(msg.String()))
	if m.mark.Input(queryMarkField, &m.query, msg) {
		m.refresh()
		return nil
	}
	switch msg.String() {
	case "esc", action.PaletteKey:
		m.Close()
		return nil
	case "enter":
		return m.choose()
	case "up", "ctrl+p":
		m.move(-1)
		return nil
	case "down", "ctrl+n":
		m.move(1)
		return nil
	}
	before := m.query.Value()
	var command tea.Cmd
	m.query, command = m.query.Update(msg)
	if m.query.Value() != before {
		m.refresh()
	}
	return command
}

// choose commits the highlighted row. An empty result list has nothing to run
// and enter is a no-op rather than a close, because closing on enter would
// punish a typo with a lost search.
func (m *Model) choose() tea.Cmd {
	if m.cursor < 0 || m.cursor >= len(m.entries) {
		return nil
	}
	m.choice = m.entries[m.cursor].Action
	m.chosen = true
	m.Close()
	return nil
}

// move walks the cursor and clamps it at both ends. The list does not wrap:
// wrapping a ranked list sends the arrow key that was reaching for the best
// match to the worst one.
func (m *Model) move(delta int) {
	if len(m.entries) == 0 {
		m.cursor = 0
		return
	}
	m.cursor = min(max(m.cursor+delta, 0), len(m.entries)-1)
}

// refresh reruns the search and re-seats the cursor at the best match. It also
// retires the pointer map: spec section 10.5.2 row 9, a re-render with a changed
// region set re-resolves hover, and a filtered list has no point to re-resolve
// the old rows from because the rows themselves are new actions.
func (m *Model) refresh() {
	m.entries = Filter(action.Listed(m.features), sanitize(m.query.Value()))
	m.cursor, m.offset = 0, 0
	m.pointerState = m.pointerState.ClearHover()
	m.generation++
}

// query text is user input echoed back into a rendered frame, so control
// sequences are stripped before it is measured or drawn.
func sanitize(value string) string {
	return strings.Map(func(letter rune) rune {
		if unicode.IsControl(letter) {
			return -1
		}
		return letter
	}, ansi.Strip(value))
}

// isMotionKey reports whether a key moves the palette's own cursor, which is
// the arrow-key precondition of spec section 10.5.2 row 7.
func isMotionKey(key string) bool {
	switch key {
	case "up", "down", "ctrl+p", "ctrl+n":
		return true
	default:
		return false
	}
}

// IsMessage reports whether a non-key message belongs to the palette: pointer
// feedback, or one of its own pointer activations. Key presses are routed by
// the caller's own open-overlay branch, which owns the interrupt ladder, so
// they are deliberately not claimed here.
func IsMessage(message tea.Msg) bool {
	if pointer.IsMessage(message) {
		return true
	}
	switch message.(type) {
	case pointerActionMsg, pointerWheelMsg:
		return true
	default:
		return false
	}
}
