package tui

import (
	"reflect"
	"sort"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/board"
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

type boardFilterState struct {
	input      textinput.Model
	tags       []string
	focus      filterFocus
	labelIndex int
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
	s.input.SetValue(value.Text)
	s.tags = normalizedFilterTags(value.Tags)
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
	s.input.Blur()
}

func (s *boardFilterState) clear() bool {
	if !s.active() {
		return false
	}
	s.input.SetValue("")
	s.tags = nil
	return true
}

func (s *boardFilterState) toggleTag(tag string) bool {
	if tag == "" {
		return false
	}
	for i, selected := range s.tags {
		if selected == tag {
			s.tags = append(s.tags[:i], s.tags[i+1:]...)
			return true
		}
	}
	s.tags = append(s.tags, tag)
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

func boardLabels(current board.Board) []string {
	seen := make(map[string]struct{})
	for _, task := range current.Tasks {
		for _, tag := range task.Tags {
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

func (m Model) filteredBoard() board.Board {
	return m.filter.project(m.board)
}

func (m *Model) mutateFilter(change func(*boardFilterState)) tea.Cmd {
	previous := m.filteredBoard()
	before := m.filter.value()
	change(&m.filter)
	if reflect.DeepEqual(before, m.filter.value()) {
		return nil
	}
	m.boardView.adoptBoard(previous, m.filteredBoard())
	return m.queuePreferences()
}

func (m *Model) handleFilterKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	key := msg.String()
	labels := m.filterLabels()
	switch m.filter.focus {
	case filterText:
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
		previous := m.filteredBoard()
		before := m.filter.input.Value()
		updated, inputCmd := m.filter.input.Update(msg)
		m.filter.input = updated
		if before == m.filter.input.Value() {
			return true, inputCmd
		}
		m.boardView.adoptBoard(previous, m.filteredBoard())
		return true, batchCommands(inputCmd, m.queuePreferences())
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

func (m Model) filterLabels() []string {
	labels := boardLabels(m.board)
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
