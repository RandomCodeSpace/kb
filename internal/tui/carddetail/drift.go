package carddetail

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/forge"
	"github.com/RandomCodeSpace/kb/internal/store"
	"github.com/RandomCodeSpace/kb/internal/tui/widget"
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

type driftChoicePointerMsg struct{ index int }

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
	m.stopBrand()
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
	m.armBrand()
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
	case driftChoicePointerMsg:
		if m.driftMode != driftSelect || m.driftBusy != "" || msg.index < 0 || msg.index >= len(m.driftChoices) {
			return nil
		}
		m.driftSelection = msg.index
		m.rebuildBody()
		return nil
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
	m.stopBrand()
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
	m.armBrand()
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
	m.armBrand()
	return func() tea.Msg {
		at, err := backend.AcceptDrift(ctx, user, choice.Source, choice.ExternalKey, revision)
		return driftAcceptedMsg{taskID: taskID, session: session, generation: generation, baselineAt: at, err: err}
	}
}

func (m Model) driftBody(width int) string {
	lines := []string{"Upstream drift review", "kb does not sync upstream changes into the card."}
	if m.driftBusy != "" {
		// Spec section 10.8.4 rule 1 moves the busy state into the footer band
		// and deletes the body's own "<op> in progress..." line: the band is the
		// only row whose content changes, so the body does not reflow when the
		// check lands.
		return strings.Join(lines, "\n")
	}
	if m.driftMode == driftSelect {
		lines = append(lines, "", "Choose provenance:")
		for index, item := range m.driftChoices {
			cursor := "  "
			if index == m.driftSelection {
				cursor = "> "
			}
			line := fmt.Sprintf("%s%s  %s  %s", cursor, safeText(item.Source, false), safeText(item.Title, false), safeText(item.URL, false))
			lines = append(lines, m.pointerState.Render(m.styles, detailDriftControlID(index), line))
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

// driftLadder is the drift view's hint ladder. A failure never appears here:
// spec section 10.8.5 bars an error message from a band row, because neither
// danger slot clears the AA floor on OverlayBand, and the pane pins the error
// above its action row instead. A non-failure notice is still band-worthy.
func (m Model) driftLadder() widget.Ladder {
	if m.statusMessage != "" && !m.statusIsError {
		return widget.Ladder{Head: []string{"status: " + m.statusMessage}, Tail: []string{"esc back"}}
	}
	if m.driftMode == driftSelect {
		return widget.Ladder{
			Head:   []string{"up/down choose"},
			Middle: []string{"enter check"},
			Tail:   []string{"esc back"},
		}
	}
	if m.driftResult.State == "drifted" {
		return widget.Ladder{Middle: []string{"u update baseline"}, Tail: []string{"esc back"}}
	}
	return widget.Ladder{Tail: []string{"esc back"}}
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
