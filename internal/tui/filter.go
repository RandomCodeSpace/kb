package tui

import (
	"reflect"
	"slices"
	"sort"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/project"
	"github.com/RandomCodeSpace/kb/internal/tui/formview"
)

type boardFilter struct {
	Text string   `json:"text,omitempty"`
	Tags []string `json:"tags,omitempty"`
}

type filterFocus uint8

const (
	filterUnfocused filterFocus = iota
	filterText
	filterLabels
)

// filterMarkField is the board filter's field name in the select-all mark.
const filterMarkField = "filter"

type boardFilterState struct {
	input              textinput.Model
	tags               []string
	projectionRevision uint64
	focus              filterFocus
	labelIndex         int
	mark               formview.Mark
}

type boardFilterVisualIdentity struct {
	focus         filterFocus
	labelIndex    int
	position      int
	inputFocused  bool
	virtualCursor bool
	marked        bool
}

func (s *boardFilterState) visualIdentity() boardFilterVisualIdentity {
	return boardFilterVisualIdentity{
		focus: s.focus, labelIndex: s.labelIndex, position: s.input.Position(),
		inputFocused: s.input.Focused(), virtualCursor: s.input.VirtualCursor(),
		marked: s.mark.Active(filterMarkField),
	}
}

func newBoardFilterState() boardFilterState {
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = "Filter cards"
	input.SetWidth(40)
	return boardFilterState{input: input}
}

func (s boardFilterState) value() boardFilter {
	return boardFilter{Text: s.input.Value(), Tags: append([]string(nil), s.tags...)}
}

func (s *boardFilterState) restore(value boardFilter) {
	beforeText := s.input.Value()
	beforeTags := s.tags
	tags := normalizedFilterTags(value.Tags)
	s.input.SetValue(value.Text)
	s.tags = tags
	if beforeText != value.Text || !slices.Equal(beforeTags, tags) {
		s.projectionRevision++
	}
}

func normalizedFilterTags(tags []string) []string {
	result := make([]string, 0, len(tags))
	seen := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		result = append(result, tag)
	}
	return result
}

func (s boardFilterState) active() bool {
	return strings.TrimSpace(s.input.Value()) != "" || len(s.tags) > 0
}

func (s *boardFilterState) focusText() tea.Cmd {
	s.focus = filterText
	return s.input.Focus()
}

func (s *boardFilterState) blur() {
	s.focus = filterUnfocused
	s.mark.Drop()
	s.input.Blur()
}

func (s *boardFilterState) clear() bool {
	if !s.active() {
		return false
	}
	s.input.SetValue("")
	s.tags = nil
	s.projectionRevision++
	return true
}

func (s *boardFilterState) toggleTag(tag string) bool {
	if tag == "" {
		return false
	}
	for i, selected := range s.tags {
		if selected == tag {
			s.tags = append(s.tags[:i], s.tags[i+1:]...)
			s.projectionRevision++
			return true
		}
	}
	s.tags = append(s.tags, tag)
	s.projectionRevision++
	return true
}

func (s boardFilterState) hasTag(tag string) bool {
	for _, selected := range s.tags {
		if selected == tag {
			return true
		}
	}
	return false
}

func (s boardFilterState) matches(task board.Task) bool {
	for _, selected := range s.tags {
		found := false
		for _, tag := range task.Tags {
			if tag == selected {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	needle := webLower(strings.TrimSpace(s.input.Value()))
	if needle == "" {
		return true
	}
	if strings.Contains(webLower(task.Title), needle) ||
		strings.Contains(webLower(task.Desc), needle) {
		return true
	}
	for _, tag := range task.Tags {
		if strings.Contains(webLower(tag), needle) {
			return true
		}
	}
	return false
}

func (s boardFilterState) project(current board.Board) board.Board {
	if !s.active() {
		return current
	}
	filtered := current
	filtered.Tasks = make([]board.Task, 0, len(current.Tasks))
	for _, task := range current.Tasks {
		if s.matches(task) {
			filtered.Tasks = append(filtered.Tasks, task)
		}
	}
	return filtered
}

// boardLabels is the filter's label vocabulary. Project labels are left out of
// it: the switcher owns that axis, and offering a project:: chip beside it
// would let the two contradict each other.
func boardLabels(current board.Board) []string {
	seen := make(map[string]struct{})
	for _, task := range current.Tasks {
		_, tags := project.SplitTags(task.Tags)
		for _, tag := range tags {
			if tag != "" {
				seen[tag] = struct{}{}
			}
		}
	}
	labels := make([]string, 0, len(seen))
	for tag := range seen {
		labels = append(labels, tag)
	}
	sort.Strings(labels)
	return labels
}

// filteredBoard is what the board renders: the project scope first, then the
// text and label filter over what it left.
func (m Model) filteredBoard() board.Board {
	if m.renderingProjection != nil {
		return m.renderingProjection.board
	}
	if m.preparedProjection != nil && m.preparedProjection.matchesProjectionKey(m) {
		return m.preparedProjection.board
	}
	if m.current != nil && m.current.projection.matchesProjectionKey(m) {
		return m.current.projection.board
	}
	return m.filter.project(m.projectBoard())
}

// currentProjection is the O(1) navigation/render lookup. The source identity
// check rejects board replacements made during Update; deep source comparison
// remains at explicit render-plan rebuild boundaries where data actually
// changed.
func (m Model) currentProjection() *renderProjection {
	if m.renderingProjection != nil {
		return m.renderingProjection
	}
	if m.preparedProjection != nil && m.preparedProjection.matchesProjectionKey(m) {
		return m.preparedProjection
	}
	if m.current != nil && m.current.projection.matchesProjectionKey(m) {
		return &m.current.projection
	}
	return nil
}

// prepareFilterProjection performs the one projection pass inherently required
// by a changed query. rebuildRenderPlanAfterUpdate consumes the prepared value
// instead of pointlessly walking the same derivations again.
func (m *Model) prepareFilterProjection() *renderProjection {
	if m.current == nil || !m.current.projection.matchesSourceIdentity(m.board) {
		m.preparedProjection = nil
		m.preparedDerivations = 0
		m.preparedComparisons = 0
		return nil
	}
	prepared, _, _ := m.current.projection.rebuildSource(*m, false)
	m.preparedProjection = &prepared
	m.preparedDerivations = 0
	m.preparedComparisons = 0
	return &prepared
}

func (m *Model) mutateFilter(change func(*boardFilterState)) tea.Cmd {
	previous := m.filteredBoard()
	before := m.filter.value()
	beforeRevision := m.filter.projectionRevision
	change(&m.filter)
	if reflect.DeepEqual(before, m.filter.value()) {
		return nil
	}
	if m.filter.projectionRevision == beforeRevision {
		m.filter.projectionRevision++
	}
	if prepared := m.prepareFilterProjection(); prepared != nil {
		m.boardView.adoptBoard(previous, prepared.board)
	} else {
		m.boardView.adoptBoard(previous, m.filteredBoard())
	}
	return m.queuePreferences()
}

func (m *Model) handleFilterKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	key := msg.String()
	labels := m.filterLabels()
	switch m.filter.focus {
	case filterText:
		previous := m.filteredBoard()
		before := m.filter.input.Value()
		// The mark runs ahead of the pane's own keys: a filter field with a
		// live mark is typing context, and its Escape drops the mark instead
		// of closing the field.
		if m.filter.mark.Input(filterMarkField, &m.filter.input, msg) {
			return true, m.filterTextChanged(previous, before, nil)
		}
		switch key {
		case "esc", "enter":
			m.filter.blur()
			return true, nil
		case "tab", "shift+tab":
			m.filter.input.Blur()
			if len(labels) == 0 {
				m.filter.focus = filterUnfocused
			} else {
				m.filter.focus = filterLabels
				if key == "shift+tab" {
					m.filter.labelIndex = len(labels) - 1
				}
			}
			return true, nil
		}
		updated, inputCmd := m.filter.input.Update(msg)
		m.filter.input = updated
		return true, m.filterTextChanged(previous, before, inputCmd)
	case filterLabels:
		if len(labels) == 0 {
			m.filter.blur()
			return true, nil
		}
		m.filter.labelIndex = min(max(m.filter.labelIndex, 0), len(labels)-1)
		switch key {
		case "esc":
			m.filter.blur()
			return true, nil
		case "/":
			return true, m.filter.focusText()
		case "left", "h", "shift+tab":
			m.filter.labelIndex = (m.filter.labelIndex - 1 + len(labels)) % len(labels)
			return true, nil
		case "right", "l", "tab":
			m.filter.labelIndex = (m.filter.labelIndex + 1) % len(labels)
			return true, nil
		case "enter", " ":
			tag := labels[m.filter.labelIndex]
			return true, m.mutateFilter(func(filter *boardFilterState) { filter.toggleTag(tag) })
		case "x":
			return false, nil
		case "X":
			if m.filter.active() {
				return true, m.mutateFilter(func(filter *boardFilterState) { filter.clear() })
			}
		}
		return true, nil
	default:
		switch key {
		case "/":
			return true, m.filter.focusText()
		case "f":
			if len(labels) > 0 {
				m.filter.focus = filterLabels
				m.filter.input.Blur()
			}
			return true, nil
		case "X":
			if m.filter.active() {
				return true, m.mutateFilter(func(filter *boardFilterState) { filter.clear() })
			}
		}
	}
	return false, nil
}

// filterTextChanged settles a key that reached the filter text field: the board
// is re-projected and the preference write queued only when the value actually
// moved, whether it moved by typing or by the select-all mark clearing it.
func (m *Model) filterTextChanged(previous board.Board, before string, inputCmd tea.Cmd) tea.Cmd {
	if before == m.filter.input.Value() {
		return inputCmd
	}
	m.filter.projectionRevision++
	if prepared := m.prepareFilterProjection(); prepared != nil {
		m.boardView.adoptBoard(previous, prepared.board)
	} else {
		m.boardView.adoptBoard(previous, m.filteredBoard())
	}
	return batchCommands(inputCmd, m.queuePreferences())
}

func (m Model) filterLabels() []string {
	if projection := m.currentProjection(); projection != nil {
		return projection.filterLabels()
	}
	labels := boardLabels(m.projectBoard())
	seen := make(map[string]struct{}, len(labels))
	for _, label := range labels {
		seen[label] = struct{}{}
	}
	for _, selected := range m.filter.tags {
		if _, ok := seen[selected]; !ok {
			labels = append(labels, selected)
		}
	}
	sort.Strings(labels)
	return labels
}
