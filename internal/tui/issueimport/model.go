// Package issueimport implements the direct-store forge import review overlay.
package issueimport

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/forge"
	"github.com/RandomCodeSpace/kb/internal/store"
)

const (
	defaultMax = 8
	maxIssues  = 20
)

type Store interface {
	AddTask(string, board.Task) (board.Task, error)
}

type Backend interface {
	Sources(string) ([]store.ForgeSource, error)
	Preview(context.Context, string, forge.PreviewRequest) (forge.Preview, error)
	CreateTask(string, string, board.Task, forge.LinkInput) (board.Task, error)
}

type stage uint8

const (
	stageInput stage = iota
	stageReview
)

type row struct {
	draft   forge.Draft
	include bool
	created bool
	err     string
}

type sourcesLoadedMsg struct {
	session uint64
	sources []store.ForgeSource
	err     error
}

type previewCompletedMsg struct {
	session    uint64
	generation uint64
	preview    forge.Preview
	err        error
}

type cardCreatedMsg struct {
	session    uint64
	generation uint64
	row        int
	err        error
}

type Model struct {
	store   Store
	backend Backend
	user    string
	ctx     context.Context

	open       bool
	stage      stage
	session    uint64
	generation uint64
	cancel     context.CancelFunc
	operation  string
	changed    bool

	sources     []store.ForgeSource
	source      int
	ref         textinput.Model
	max         int
	focus       int
	preview     forge.Preview
	rows        []row
	selection   int
	queue       []int
	queuePos    int
	status      string
	statusError bool
	scroll      int
}

func New(st Store, backend Backend, user string, ctx context.Context) Model {
	if ctx == nil {
		ctx = context.Background()
	}
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = "owner/repo or configured forge URL"
	input.SetWidth(56)
	return Model{store: st, backend: backend, user: user, ctx: ctx, ref: input, max: defaultMax}
}

func (m Model) Enabled() bool { return m.store != nil && m.backend != nil }
func (m Model) IsOpen() bool  { return m.open }

func IsMessage(message tea.Msg) bool {
	switch message.(type) {
	case sourcesLoadedMsg, previewCompletedMsg, cardCreatedMsg:
		return true
	default:
		return false
	}
}

func (m *Model) Open() tea.Cmd {
	if !m.Enabled() {
		return nil
	}
	m.closeNow()
	m.session++
	m.generation++
	m.open, m.stage, m.max, m.focus = true, stageInput, defaultMax, 0
	m.sources, m.source, m.rows, m.queue = nil, 0, nil, nil
	m.preview, m.selection, m.queuePos = forge.Preview{}, 0, 0
	m.status, m.statusError, m.changed, m.scroll = "", false, false, 0
	m.ref.SetValue("")
	m.ref.Blur()
	session := m.session
	return func() tea.Msg {
		sources, err := m.backend.Sources(m.user)
		return sourcesLoadedMsg{session: session, sources: sources, err: err}
	}
}

func (m *Model) Close() { m.closeNow() }

func (m *Model) closeNow() {
	if m.cancel != nil {
		m.cancel()
	}
	m.cancel = nil
	m.generation++
	m.open, m.operation = false, ""
	m.ref.Blur()
}

func (m *Model) ConsumeChanged() bool {
	changed := m.changed
	m.changed = false
	return changed
}

func (m *Model) Update(message tea.Msg) tea.Cmd {
	if !m.open {
		return nil
	}
	switch msg := message.(type) {
	case sourcesLoadedMsg:
		if msg.session != m.session {
			return nil
		}
		if msg.err != nil {
			m.setStatus("sources unavailable", true)
			return nil
		}
		m.sources = msg.sources
		if len(m.sources) == 0 {
			m.setStatus("no forge integrations configured", true)
		}
	case previewCompletedMsg:
		if msg.session != m.session || msg.generation != m.generation || m.operation != "preview" {
			return nil
		}
		m.cancel, m.operation = nil, ""
		if msg.err != nil {
			m.setStatus(safeError(msg.err), true)
			return nil
		}
		m.preview, m.stage = msg.preview, stageReview
		m.rows = make([]row, len(msg.preview.Drafts))
		for index, draft := range msg.preview.Drafts {
			include := draft.Duplicate == nil || draft.Duplicate.Via != "link"
			m.rows[index] = row{draft: draft, include: include}
		}
		m.selection, m.scroll = 0, 0
		m.setStatus("review proposals; exact duplicates start unticked", false)
	case cardCreatedMsg:
		return m.finishCard(msg)
	case tea.KeyPressMsg:
		if m.operation != "" {
			if msg.String() == "esc" && m.operation == "preview" {
				m.cancelOperation("preview cancelled")
			}
			return nil
		}
		if m.stage == stageInput {
			return m.updateInput(msg)
		}
		return m.updateReview(msg)
	}
	return nil
}

func (m *Model) updateInput(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	switch key {
	case "esc":
		m.Close()
		return nil
	case "tab", "shift+tab":
		delta := 1
		if key == "shift+tab" {
			delta = -1
		}
		m.focus = (m.focus + delta + 3) % 3
		m.applyFocus()
		return nil
	case "enter":
		return m.startPreview()
	case "left", "h":
		if m.focus == 0 && len(m.sources) > 0 {
			m.source = (m.source - 1 + len(m.sources)) % len(m.sources)
		} else if m.focus == 2 {
			m.max = max(1, m.max-1)
		}
		return nil
	case "right", "l":
		if m.focus == 0 && len(m.sources) > 0 {
			m.source = (m.source + 1) % len(m.sources)
		} else if m.focus == 2 {
			m.max = min(maxIssues, m.max+1)
		}
		return nil
	}
	if m.focus == 1 {
		var command tea.Cmd
		m.ref, command = m.ref.Update(msg)
		return command
	}
	if m.focus == 2 && len(key) == 1 && key[0] >= '0' && key[0] <= '9' {
		value, _ := strconv.Atoi(key)
		m.max = min(maxIssues, max(1, value))
	}
	return nil
}

func (m *Model) applyFocus() tea.Cmd {
	if m.focus == 1 {
		return m.ref.Focus()
	}
	m.ref.Blur()
	return nil
}

func (m *Model) startPreview() tea.Cmd {
	if len(m.sources) == 0 {
		m.setStatus("configure a forge integration first", true)
		return nil
	}
	raw := strings.TrimSpace(m.ref.Value())
	if raw == "" {
		m.setStatus("reference required", true)
		return nil
	}
	m.generation++
	generation, session := m.generation, m.session
	ctx, cancel := context.WithCancel(m.ctx)
	m.cancel, m.operation = cancel, "preview"
	m.setStatus("fetching and drafting...", false)
	request := forge.PreviewRequest{Source: m.sources[m.source].Name, Ref: raw, Max: m.max}
	return func() tea.Msg {
		preview, err := m.backend.Preview(ctx, m.user, request)
		return previewCompletedMsg{session: session, generation: generation, preview: preview, err: err}
	}
}

func (m *Model) updateReview(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.stage, m.rows, m.status = stageInput, nil, ""
		m.applyFocus()
	case "up", "k":
		m.selection = max(0, m.selection-1)
	case "down", "j":
		m.selection = min(len(m.rows)-1, m.selection+1)
	case "space":
		if len(m.rows) > 0 && !m.rows[m.selection].created {
			m.rows[m.selection].include = !m.rows[m.selection].include
		}
	case "enter":
		return m.startCreate()
	}
	return nil
}

func (m *Model) startCreate() tea.Cmd {
	m.queue = m.queue[:0]
	for index := range m.rows {
		if m.rows[index].include && !m.rows[index].created {
			m.queue = append(m.queue, index)
		}
	}
	if len(m.queue) == 0 {
		m.setStatus("nothing selected", false)
		return nil
	}
	m.generation++
	m.queuePos, m.operation = 0, "create"
	return m.nextWrite()
}

func (m *Model) nextWrite() tea.Cmd {
	if m.queuePos >= len(m.queue) {
		m.operation = ""
		m.setStatus("import complete", false)
		return nil
	}
	index := m.queue[m.queuePos]
	if m.rows[index].created {
		m.queuePos++
		return m.nextWrite()
	}
	draft := m.rows[index].draft
	task := board.Task{Title: draft.Title, Emoji: draft.Emoji, Desc: draft.Desc, Status: board.StatusTodo, Prio: draft.Prio, Due: draft.Due, Effort: draft.Effort, Tags: append([]string(nil), draft.Tags...)}
	for _, check := range draft.Checks {
		task.Checks = append(task.Checks, board.Check{Text: check.Text, Done: check.Done})
	}
	session, generation := m.session, m.generation
	source := m.sources[m.source].Name
	item := forge.LinkInput{ExternalKey: draft.ExternalKey, Link: draft.Link, URL: draft.URL, Title: draft.Title}
	return func() tea.Msg {
		_, err := m.backend.CreateTask(m.user, source, task, item)
		return cardCreatedMsg{session: session, generation: generation, row: index, err: err}
	}
}

func (m *Model) finishCard(msg cardCreatedMsg) tea.Cmd {
	if msg.session != m.session || msg.generation != m.generation || m.operation != "create" || m.queuePos >= len(m.queue) || m.queue[m.queuePos] != msg.row {
		return nil
	}
	if msg.err != nil {
		m.rows[msg.row].err = safeError(msg.err)
		m.queuePos++
		return m.nextWrite()
	}
	m.rows[msg.row].created = true
	m.rows[msg.row].err = ""
	m.changed = true
	m.queuePos++
	return m.nextWrite()
}

func (m *Model) cancelOperation(status string) {
	if m.cancel != nil {
		m.cancel()
	}
	m.cancel = nil
	m.generation++
	m.operation = ""
	m.setStatus(status, false)
}

func (m *Model) setStatus(message string, isError bool) {
	m.status, m.statusError = message, isError
}

func safeError(err error) string {
	var categorized *forge.Error
	if errors.As(err, &categorized) {
		return categorized.Message
	}
	return "operation failed"
}

func (m Model) sourceName() string {
	if len(m.sources) == 0 {
		return "none"
	}
	return m.sources[m.source].Name
}

func (m Model) progress() string {
	if m.operation != "create" {
		return ""
	}
	return fmt.Sprintf("writing %d/%d", min(m.queuePos+1, len(m.queue)), len(m.queue))
}
