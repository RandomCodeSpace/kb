package carddetail

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/forge"
	"github.com/RandomCodeSpace/kb/internal/store"
)

const upstreamConflictCopy = "Upstream changed again. Check upstream before updating the card."

type DriftBackend interface {
	Provenance(string, string) ([]store.ImportLink, error)
	CheckDrift(context.Context, string, string, string) (forge.Drift, error)
	AcceptDrift(context.Context, string, string, string, string) (string, error)
}

type driftMode uint8

const (
	driftNone driftMode = iota
	driftSelect
	driftReview
)

type driftChoicesLoadedMsg struct {
	taskID     string
	session    uint64
	generation uint64
	choices    []store.ImportLink
	err        error
}

type driftCheckedMsg struct {
	taskID     string
	session    uint64
	generation uint64
	result     forge.Drift
	err        error
}

type driftAcceptedMsg struct {
	taskID     string
	session    uint64
	generation uint64
	baselineAt string
	err        error
}

func (m *Model) SetDriftBackend(backend DriftBackend, ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	m.driftBackend, m.driftContext = backend, ctx
}

func (m *Model) cancelDrift() {
	if m.driftCancel != nil {
		m.driftCancel()
	}
	m.driftCancel = nil
	m.driftGeneration++
	m.driftMode, m.driftBusy = driftNone, ""
	m.driftChoices, m.driftSelection, m.driftResult = nil, 0, forge.Drift{}
}

func (m *Model) beginDrift() tea.Cmd {
	if m.driftBackend == nil || m.driftBusy != "" {
		return nil
	}
	links := rawImportLinks(m.task.Tags)
	if len(links) == 0 {
		m.setStatus("no imported forge link on this card", false)
		m.rebuildBody()
		return nil
	}
	m.driftMode, m.driftBusy = driftSelect, "provenance"
	m.driftSession++
	m.driftGeneration++
	session, generation, taskID := m.driftSession, m.driftGeneration, m.task.ID
	ctx, cancel := context.WithCancel(m.driftContext)
	m.driftCancel = cancel
	backend, user := m.driftBackend, m.user
	return func() tea.Msg {
		var choices []store.ImportLink
		for _, link := range links {
			found, err := backend.Provenance(user, link)
			if err != nil {
				return driftChoicesLoadedMsg{taskID: taskID, session: session, generation: generation, err: err}
			}
			choices = append(choices, found...)
		}
		select {
		case <-ctx.Done():
			return driftChoicesLoadedMsg{taskID: taskID, session: session, generation: generation, err: ctx.Err()}
		default:
			return driftChoicesLoadedMsg{taskID: taskID, session: session, generation: generation, choices: choices}
		}
	}
}

func (m *Model) updateDrift(message tea.Msg) tea.Cmd {
	switch msg := message.(type) {
	case driftChoicesLoadedMsg:
		if !m.currentDrift(msg.taskID, msg.session, msg.generation, "provenance") {
			return nil
		}
		m.finishDriftOperation()
		if msg.err != nil {
			m.cancelDrift()
			m.setStatus(driftError(msg.err), true)
			m.rebuildBody()
			return nil
		}
		m.driftChoices = msg.choices
		if len(msg.choices) == 0 {
			m.cancelDrift()
			m.setStatus("import provenance not found", true)
			m.rebuildBody()
			return nil
		}
		m.driftSelection = 0
		m.rebuildBody()
	case driftCheckedMsg:
		if !m.currentDrift(msg.taskID, msg.session, msg.generation, "check") {
			return nil
		}
		m.finishDriftOperation()
		if msg.err != nil {
			m.setStatus(driftError(msg.err), true)
			m.rebuildBody()
			return nil
		}
		m.driftMode, m.driftResult = driftReview, msg.result
		m.rebuildBody()
	case driftAcceptedMsg:
		if !m.currentDrift(msg.taskID, msg.session, msg.generation, "accept") {
			return nil
		}
		m.finishDriftOperation()
		if msg.err != nil {
			if errors.Is(msg.err, forge.ErrUpstreamChanged) {
				m.setStatus(upstreamConflictCopy, true)
			} else {
				m.setStatus(driftError(msg.err), true)
			}
			m.rebuildBody()
			return nil
		}
		m.driftResult.State, m.driftResult.BaselineAt, m.driftResult.Revision = "unchanged", msg.baselineAt, ""
		m.setStatus("upstream baseline updated", false)
		m.rebuildBody()
	case tea.KeyPressMsg:
		if m.driftMode == driftNone {
			return nil
		}
		if msg.String() == "esc" && m.driftBusy == "" {
			m.cancelDrift()
			m.rebuildBody()
			return nil
		}
		if m.driftBusy != "" {
			return nil
		}
		if m.driftMode == driftSelect {
			switch msg.String() {
			case "up", "k":
				m.driftSelection = max(0, m.driftSelection-1)
			case "down", "j":
				m.driftSelection = min(len(m.driftChoices)-1, m.driftSelection+1)
			case "enter":
				return m.startDriftCheck()
			}
			m.rebuildBody()
			return nil
		}
		if msg.String() == "u" && m.driftResult.State == "drifted" && m.driftResult.Revision != "" {
			return m.startDriftAccept()
		}
	}
	return nil
}

func (m *Model) currentDrift(taskID string, session, generation uint64, operation string) bool {
	return m.open && m.task.ID == taskID && m.driftSession == session && m.driftGeneration == generation && m.driftBusy == operation
}

func (m *Model) finishDriftOperation() {
	if m.driftCancel != nil {
		m.driftCancel()
	}
	m.driftCancel, m.driftBusy = nil, ""
}

func (m *Model) startDriftCheck() tea.Cmd {
	if m.driftSelection < 0 || m.driftSelection >= len(m.driftChoices) {
		return nil
	}
	choice := m.driftChoices[m.driftSelection]
	m.driftGeneration++
	ctx, cancel := context.WithCancel(m.driftContext)
	m.driftCancel, m.driftBusy = cancel, "check"
	backend, user := m.driftBackend, m.user
	taskID, session, generation := m.task.ID, m.driftSession, m.driftGeneration
	return func() tea.Msg {
		result, err := backend.CheckDrift(ctx, user, choice.Source, choice.ExternalKey)
		return driftCheckedMsg{taskID: taskID, session: session, generation: generation, result: result, err: err}
	}
}

func (m *Model) startDriftAccept() tea.Cmd {
	choice := m.driftChoices[m.driftSelection]
	revision := m.driftResult.Revision
	m.driftGeneration++
	ctx, cancel := context.WithCancel(m.driftContext)
	m.driftCancel, m.driftBusy = cancel, "accept"
	backend, user := m.driftBackend, m.user
	taskID, session, generation := m.task.ID, m.driftSession, m.driftGeneration
	return func() tea.Msg {
		at, err := backend.AcceptDrift(ctx, user, choice.Source, choice.ExternalKey, revision)
		return driftAcceptedMsg{taskID: taskID, session: session, generation: generation, baselineAt: at, err: err}
	}
}

func (m Model) driftBody(width int) string {
	lines := []string{"Upstream drift review", "kb does not sync upstream changes into the card."}
	if m.driftBusy != "" {
		return strings.Join(append(lines, "", m.driftBusy+" in progress..."), "\n")
	}
	if m.driftMode == driftSelect {
		lines = append(lines, "", "Choose provenance:")
		for index, item := range m.driftChoices {
			cursor := "  "
			if index == m.driftSelection {
				cursor = "> "
			}
			lines = append(lines, fmt.Sprintf("%s%s  %s  %s", cursor, safeText(item.Source, false), safeText(item.Title, false), safeText(item.URL, false)))
		}
		return strings.Join(lines, "\n")
	}
	result := m.driftResult
	lines = append(lines, "", "state  "+safeText(result.State, false), "upstream  "+safeText(result.UpstreamTitle, false))
	if result.BaselineTitle != "" {
		lines = append(lines, "baseline  "+safeText(result.BaselineTitle, false))
	}
	if result.Summary != "" {
		lines = append(lines, "", "summary  "+safeText(result.Summary, true))
	}
	return strings.Join(lines, "\n")
}

func (m Model) driftFooter() string {
	if m.driftBusy != "" {
		return "check in progress | input locked"
	}
	if m.driftMode == driftSelect {
		return "up/down choose | enter check | esc back"
	}
	if m.driftResult.State == "drifted" {
		return "u update baseline | esc back"
	}
	return "esc back"
}

func rawImportLinks(tags []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, tag := range tags {
		if !strings.HasPrefix(tag, "link::") {
			continue
		}
		link := strings.TrimSpace(strings.TrimPrefix(tag, "link::"))
		if link != "" && !seen[link] {
			seen[link] = true
			result = append(result, link)
		}
	}
	return result
}

func driftError(err error) string {
	var categorized *forge.Error
	if errors.As(err, &categorized) {
		return categorized.Message
	}
	return "drift check failed"
}
