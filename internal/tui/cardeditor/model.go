// Package cardeditor implements the direct-store create and edit overlay.
package cardeditor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/ai"
	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/project"
	"github.com/RandomCodeSpace/kb/internal/store"
	"github.com/RandomCodeSpace/kb/internal/tui/formview"
	"github.com/RandomCodeSpace/kb/internal/tui/pointer"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

const (
	similarLimit   = 10
	draftMaxTokens = 4096
)

// SkillRunner is the shared direct-store AI runner used by the editor. The
// narrow interface keeps model tests deterministic without an HTTP adapter.
type SkillRunner interface {
	RunSkill(context.Context, string, ai.Scope, string, string, int, int64) (ai.RunResult, error)
}

// Store is the direct SQLite projection used by the editor. It deliberately
// mirrors the store package instead of introducing an HTTP-shaped adapter.
type Store interface {
	AddTask(string, board.Task) (board.Task, error)
	UpdateTaskIfFieldsMatch(string, string, store.TaskPatch, store.TaskPatch) (board.Task, error)
	Labels(string) ([]string, error)
	SearchSimilar(string, string, string, []string, int) ([]store.SimilarHit, error)
}

type mode uint8

const (
	modeAdd mode = iota
	modeEdit
)

type labelsLoadedMsg struct {
	session uint64
	labels  []string
	err     error
}

type similarDebounceMsg struct {
	generation uint64
	query      string
	exclusions string
}

type similarLoadedMsg struct {
	generation uint64
	query      string
	exclusions string
	hits       []store.SimilarHit
	err        error
}

type saveCompletedMsg struct {
	session uint64
	task    board.Task
	err     error
}

type draftCompletedMsg struct {
	session    uint64
	generation uint64
	draft      ai.Draft
	err        error
}

// pointerClickMsg is emitted by MouseHandler after a rendered editor control
// is clicked. Keeping the target symbolic lets pointer and keyboard activation
// share the same focus and action paths.
type pointerClickMsg struct {
	session uint64
	target  string
}

type pointerWheelMsg struct {
	session   uint64
	delta     int
	maxScroll int
}

type snapshot struct {
	title, emoji, desc, due, effort, checks string
	prio                                    int
	blocked                                 bool
	tags                                    string
	project                                 string
}

type editedFields struct {
	title, emoji, desc, due, effort, prio, blocked, tags, checks bool
}

// Model owns every mutable editor field and all asynchronous generations.
type Model struct {
	store  Store
	user   string
	now    func() time.Time
	runner SkillRunner
	ctx    context.Context

	open           bool
	mode           mode
	status         board.Status
	base           board.Task
	canonical      board.Task
	canonicalFound bool
	initial        snapshot
	session        uint64
	focus          string
	guardClose     bool
	saving         bool
	savedTaskID    string
	stale          bool
	drafting       bool
	draftCancel    context.CancelFunc
	draftGen       uint64

	title       textinput.Model
	emoji       textinput.Model
	project     textinput.Model
	desc        textarea.Model
	due         textinput.Model
	label       textinput.Model
	checks      textarea.Model
	draftPrompt textarea.Model
	mark        formview.Mark
	prio        int
	effort      string
	blocked     bool
	tags        []string

	// defaultProject is the project a card opened without one starts in: the
	// board's switcher selection, else the active project. Empty leaves the
	// field blank, and the editor refuses to save until it is filled.
	defaultProject string

	labels            []string
	labelsOpen        bool
	labelHighlight    int
	labelsErr         error
	similar           []store.SimilarHit
	similarCache      map[string][]store.SimilarHit
	dismissed         map[string]struct{}
	dismissedAll      bool
	similarLoading    bool
	similarErr        error
	similarQuery      string
	similarExclusions string
	similarGen        uint64
	statusMessage     string
	statusIsError     bool
	scroll            int
	manualScroll      bool
	pointerState      pointer.State
	styles            *theme.Styles
	spin              spinner.Model
}

// New creates a closed editor. A nil store keeps the feature unavailable in
// lightweight root-model tests.
func New(st Store, user string) Model {
	m := Model{store: st, user: user, now: time.Now, ctx: context.Background(), styles: theme.New(true)}
	m.spin = spinner.New(spinner.WithSpinner(m.styles.Spinner))
	m.resetInputs()
	return m
}

// SetStyles hands the editor the resolved design system. Spec section 6.2: the
// root builds it once per terminal background and threads it down; the editor
// never constructs one per frame.
func (m *Model) SetStyles(styles *theme.Styles) {
	if styles != nil {
		m.styles = styles
		m.spin.Spinner = styles.Spinner
	}
}

// themeStyles is the resolved design system, defaulting to the dark reference
// palette for a zero-value editor that no root has handed one to.
func (m *Model) themeStyles() *theme.Styles {
	if m.styles == nil {
		m.styles = theme.New(true)
	}
	return m.styles
}

// SetAIRunner wires the shared runner and the root program context. A nil
// runner hides the draft controls while leaving ordinary editing available.
func (m *Model) SetAIRunner(runner SkillRunner, ctx context.Context) {
	m.cancelDraft()
	m.runner = runner
	if ctx == nil {
		ctx = context.Background()
	}
	m.ctx = ctx
}

// SetProjectDefault hands the editor the project a new card belongs to. The
// board keeps it current as the switcher moves; an open form is left alone,
// because the project on screen is the one the user is editing.
func (m *Model) SetProjectDefault(name string) {
	m.defaultProject = strings.TrimSpace(name)
}

// Enabled reports whether the root has a writable direct-store backend.
func (m Model) Enabled() bool { return m.store != nil }

// IsOpen reports whether the overlay owns input and rendering.
func (m Model) IsOpen() bool { return m.open }

// CancelAsync stops work whose result no longer has a live program to receive
// it. It is used by the root shutdown path and is otherwise idempotent.
func (m *Model) CancelAsync() { m.cancelDraft() }

// TaskID returns the edited durable task id, or empty for add/closed modes.
func (m Model) TaskID() string {
	if !m.open || m.mode != modeEdit {
		return ""
	}
	return m.base.ID
}

// Dirty reports whether the current fields differ from the last store-backed
// snapshot. Returning to the original values disarms the close guard.
func (m Model) Dirty() bool { return m.open && m.currentSnapshot() != m.initial }

// ConsumeSaved reports one acknowledged mutation and its durable task id to
// the root exactly once.
func (m *Model) ConsumeSaved() (string, bool) {
	id := m.savedTaskID
	m.savedTaskID = ""
	return id, id != ""
}

// IsMessage identifies editor-owned asynchronous results without exporting
// implementation messages into the root package.
func IsMessage(message tea.Msg) bool {
	if pointer.IsMessage(message) {
		return true
	}
	switch message.(type) {
	case labelsLoadedMsg, similarDebounceMsg, similarLoadedMsg, saveCompletedMsg, draftCompletedMsg,
		pointerClickMsg, pointerWheelMsg, spinner.TickMsg:
		return true
	default:
		return false
	}
}

// busy reports whether a spinner-worthy operation is in flight. Spec section
// 5.2 names the editor's drafting and saving states for the bubbles spinner.
func (m Model) busy() bool { return m.drafting || m.saving }

// spinTick advances the busy indicator. The tick loop stops as soon as nothing
// is in flight, so an idle editor costs no timers.
func (m *Model) spinTick(msg spinner.TickMsg) tea.Cmd {
	if !m.busy() {
		return nil
	}
	var command tea.Cmd
	m.spin, command = m.spin.Update(msg)
	return command
}

// startSpinner is the command that begins the tick loop for a new operation.
func (m Model) startSpinner() tea.Cmd { return m.spin.Tick }

// OpenAdd resets the form for a card appended to status.
func (m *Model) OpenAdd(status board.Status) tea.Cmd {
	if m.store == nil {
		return nil
	}
	if !status.Valid() {
		status = board.StatusTodo
	}
	m.openForm(modeAdd, board.Task{Status: status, Prio: 3})
	return m.loadLabels()
}

// OpenEdit resets the form from task and starts label and similar loads.
func (m *Model) OpenEdit(task board.Task) tea.Cmd {
	if m.store == nil || task.ID == "" {
		return nil
	}
	m.openForm(modeEdit, task)
	return batch(m.loadLabels(), m.scheduleSimilar())
}

func (m *Model) openForm(nextMode mode, task board.Task) {
	m.cancelDraft()
	m.session++
	m.mode, m.base, m.canonical, m.status = nextMode, task, task, task.Status
	m.canonicalFound = nextMode == modeEdit
	m.open, m.guardClose, m.saving, m.stale, m.drafting = true, false, false, false, false
	m.savedTaskID = ""
	m.labels, m.similar = nil, nil
	m.dismissed = make(map[string]struct{})
	m.dismissedAll = false
	m.labelsOpen, m.labelHighlight = false, 0
	m.labelsErr, m.similarErr = nil, nil
	m.similarLoading, m.similarQuery, m.similarExclusions = false, "", ""
	m.similarCache = make(map[string][]store.SimilarHit)
	m.statusMessage, m.statusIsError, m.scroll, m.manualScroll = "", false, 0, false
	m.pointerState = pointer.State{}
	m.draftGen++
	m.resetInputs()
	m.applyTask(task)
	m.focus = "title"
	m.applyFocus()
	m.initial = m.currentSnapshot()
}

func (m *Model) resetInputs() {
	m.title = editorInput("What needs doing?")
	m.emoji = editorInput("optional")
	m.due = editorInput("YYYY-MM-DD")
	m.label = editorInput("label or scope::value")
	m.project = editorInput("project name")
	m.desc = editorArea("Description", 4)
	m.checks = editorArea("one per line; prefix x when done", 4)
	m.draftPrompt = editorArea("Describe what to draft or change", 3)
	m.prio = 3
	m.effort = ""
	m.blocked = false
	m.tags = nil
}

func editorInput(placeholder string) textinput.Model {
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = placeholder
	input.SetWidth(48)
	return input
}

func editorArea(placeholder string, height int) textarea.Model {
	area := textarea.New()
	area.Prompt = ""
	area.Placeholder = placeholder
	area.ShowLineNumbers = false
	area.SetWidth(48)
	area.SetHeight(height)
	return area
}

func (m *Model) applyTask(task board.Task) {
	m.title.SetValue(task.Title)
	m.emoji.SetValue(task.Emoji)
	m.desc.SetValue(task.Desc)
	m.due.SetValue(task.Due)
	m.prio = task.Prio
	if m.prio < 1 || m.prio > 4 {
		m.prio = 3
	}
	m.effort = task.Effort
	m.blocked = task.Blocked
	// The project rides in the tags but is edited in its own mandatory field,
	// so the label field never carries it and cannot grow a second one.
	named, rest := project.SplitTags(task.Tags)
	m.tags = append([]string(nil), rest...)
	name := ""
	if len(named) > 0 {
		name = named[0]
	}
	if name == "" {
		name = m.defaultProject
	}
	m.project.SetValue(name)
	m.checks.SetValue(checksToText(task.Checks))
}

// Refresh adopts an external store snapshot only while the form is clean.
// Dirty input remains authoritative and receives a scoped stale warning.
func (m *Model) Refresh(task board.Task, found bool) tea.Cmd {
	if !m.open || m.mode != modeEdit {
		return nil
	}
	if m.Dirty() {
		m.canonical, m.canonicalFound = task, found
		m.stale = true
		if found {
			m.statusMessage = "card changed outside the editor; current edits were preserved"
		} else {
			m.statusMessage = "card disappeared outside the editor; current edits were preserved"
		}
		m.statusIsError = true
		return nil
	}
	if !found {
		m.canonicalFound = false
		m.cancelDraft()
		m.open = false
		return nil
	}
	m.base, m.canonical, m.canonicalFound = task, task, true
	m.applyTask(task)
	m.initial = m.currentSnapshot()
	m.stale = false
	m.statusMessage, m.statusIsError = "card refreshed", false
	return m.scheduleSimilar()
}

// Update handles form input, store results, and stale-result generations.
func (m *Model) Update(message tea.Msg) tea.Cmd {
	state, command, handled := m.pointerState.Update(message)
	if handled {
		m.pointerState = state
		if !m.open {
			m.pointerState = pointer.State{}
			return nil
		}
		return command
	}
	if !m.open {
		return nil
	}
	switch msg := message.(type) {
	case spinner.TickMsg:
		return m.spinTick(msg)
	case labelsLoadedMsg:
		if msg.session != m.session {
			return nil
		}
		m.labelsErr = msg.err
		if msg.err == nil {
			m.labels = unionLabels(msg.labels, m.tags)
		}
		return nil
	case similarDebounceMsg:
		if msg.generation != m.similarGen || strings.TrimSpace(m.title.Value()) != msg.query ||
			m.currentExclusions() != msg.exclusions || runeCount(msg.query) < 3 {
			return nil
		}
		m.similarLoading, m.similarErr = true, nil
		return m.searchSimilar(msg.generation, msg.query, msg.exclusions)
	case similarLoadedMsg:
		if msg.generation != m.similarGen || strings.TrimSpace(m.title.Value()) != msg.query ||
			m.currentExclusions() != msg.exclusions {
			return nil
		}
		m.similarLoading = false
		m.similarErr = msg.err
		if msg.err == nil {
			m.similar = cloneHits(msg.hits)
			m.similarCache[similarCacheKey(msg.query, msg.exclusions)] = cloneHits(msg.hits)
			m.similarQuery, m.similarExclusions = msg.query, msg.exclusions
			m.dismissed = make(map[string]struct{})
			m.dismissedAll = false
		}
		return nil
	case saveCompletedMsg:
		if msg.session != m.session {
			return nil
		}
		m.saving = false
		if msg.err != nil {
			m.statusMessage = "save refused: " + safeError(msg.err)
			m.statusIsError = true
			return nil
		}
		m.base, m.canonical, m.canonicalFound = msg.task, msg.task, true
		m.initial = m.currentSnapshot()
		m.cancelDraft()
		m.savedTaskID, m.open = msg.task.ID, false
		return nil
	case draftCompletedMsg:
		if msg.session != m.session || msg.generation != m.draftGen {
			return nil
		}
		if m.draftCancel != nil {
			m.draftCancel()
		}
		m.drafting, m.draftCancel = false, nil
		if msg.err != nil {
			m.statusMessage = "AI draft failed: " + safeError(msg.err)
			m.statusIsError = true
			return nil
		}
		m.applyDraft(msg.draft)
		m.statusMessage, m.statusIsError = "AI draft applied; review before saving", false
		return m.scheduleSimilar()
	case pointerClickMsg:
		return m.updatePointer(msg)
	case pointerWheelMsg:
		if msg.session != m.session || m.saving || m.guardClose {
			return nil
		}
		m.manualScroll = true
		m.scroll = min(max(m.scroll+msg.delta, 0), msg.maxScroll)
		return nil
	case tea.KeyPressMsg:
		return m.updateKey(msg)
	}
	return nil
}

func (m *Model) updatePointer(message pointerClickMsg) tea.Cmd {
	if m.saving || message.target == "" || message.session != m.session {
		return nil
	}
	if m.guardClose {
		switch message.target {
		case "discard":
			return m.updateKey(tea.KeyPressMsg(tea.Key{Code: 'd', Text: "d"}))
		case "keep":
			return m.updateKey(tea.KeyPressMsg{Code: tea.KeyEscape})
		default:
			return nil
		}
	}
	if strings.HasPrefix(message.target, "similar:") {
		m.focus = message.target
		m.applyFocus()
		m.activateSimilar()
		return nil
	}
	m.manualScroll = false
	if strings.HasPrefix(message.target, "label:") {
		m.focus = "labels"
		m.applyFocus()
		m.label.SetValue(strings.TrimPrefix(message.target, "label:"))
		cmd, _ := m.updateLabels("enter", tea.KeyPressMsg{Code: tea.KeyEnter})
		return cmd
	}
	m.focus = message.target
	m.applyFocus()
	if message.target == "ai-draft" && m.drafting {
		return m.updateKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	}
	if message.target == "prio" || message.target == "effort" || message.target == "blocked" {
		return m.updateKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	}
	if message.target == "cancel" || message.target == "save" || message.target == "ai-draft" {
		return m.updateKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	}
	return nil
}

func (m *Model) updateKey(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	m.manualScroll = false
	if m.drafting {
		if key == "esc" {
			m.cancelDraft()
			m.statusMessage, m.statusIsError = "AI draft cancelled", false
		}
		return nil
	}
	if m.saving {
		return nil
	}
	if m.guardClose {
		switch key {
		case "d", "D":
			m.cancelDraft()
			m.open, m.guardClose = false, false
		case "esc":
			m.guardClose = false
			m.statusMessage, m.statusIsError = "close cancelled", false
		}
		return nil
	}
	// The focused field's mark runs ahead of every editor key, Escape included:
	// a marked field is typing context, so its first Escape drops the mark
	// rather than closing the form.
	if m.markKey(msg) {
		return nil
	}
	if key == "ctrl+s" || key == "ctrl+enter" {
		return m.startSave()
	}
	if key == "esc" {
		if m.focus == "labels" && m.labelsOpen {
			m.labelsOpen = false
			return nil
		}
		m.requestClose()
		return nil
	}
	switch key {
	case "tab":
		m.moveFocus(1)
		return nil
	case "shift+tab":
		m.moveFocus(-1)
		return nil
	}
	if strings.HasPrefix(m.focus, "similar:") {
		if key == "enter" || key == " " {
			m.activateSimilar()
		}
		return nil
	}
	switch m.focus {
	case "ai-draft":
		if key == "enter" || key == " " || key == "space" {
			return m.startDraft()
		}
		return nil
	case "prio":
		return m.updatePriority(key)
	case "effort":
		return m.updateEffort(key)
	case "blocked":
		if key == "enter" || key == " " {
			m.blocked = !m.blocked
		}
		return nil
	case "due":
		if command, handled := m.updateDuePicker(key); handled {
			return command
		}
	case "labels":
		if command, handled := m.updateLabels(key, msg); handled {
			return command
		}
	case "cancel":
		if key == "enter" || key == " " {
			m.requestClose()
		}
		return nil
	case "save":
		if key == "enter" || key == " " {
			return m.startSave()
		}
		return nil
	}
	return m.updateFocusedInput(msg)
}

func (m *Model) startDraft() tea.Cmd {
	if m.runner == nil || m.drafting {
		return nil
	}
	prompt := strings.TrimSpace(m.draftPrompt.Value())
	if prompt == "" {
		m.statusMessage, m.statusIsError = "AI draft request is required", true
		return nil
	}
	input := "Create a new kanban card for this request:\n" + prompt
	if m.mode == modeEdit {
		input = "Update the kanban card according to this request:\n" + prompt
		if current, err := m.currentCardJSON(); err == nil {
			input += "\n\nCurrent card JSON:\n" + string(current)
		}
	}
	m.draftGen++
	generation, session := m.draftGen, m.session
	ctx, cancel := context.WithCancel(m.ctx)
	m.draftCancel, m.drafting = cancel, true
	m.statusMessage, m.statusIsError = "drafting card...", false
	return tea.Batch(m.startSpinner(), func() tea.Msg {
		run, err := m.runner.RunSkill(ctx, m.user, ai.ScopeReadOnly, "story-draft", input, 1, draftMaxTokens)
		if err == nil && len(run.Cards) == 0 {
			err = errors.New("the model returned no usable card")
		}
		var draft ai.Draft
		if len(run.Cards) > 0 {
			draft = run.Cards[0]
		}
		return draftCompletedMsg{session: session, generation: generation, draft: draft, err: err}
	})
}

func (m *Model) currentCardJSON() ([]byte, error) {
	checks := textToChecks(m.checks.Value())
	type wireCheck struct {
		Text string `json:"text"`
		Done bool   `json:"done"`
	}
	wireChecks := make([]wireCheck, len(checks))
	for i, check := range checks {
		wireChecks[i] = wireCheck{Text: check.Text, Done: check.Done}
	}
	return json.Marshal(struct {
		Title  string      `json:"title"`
		Desc   string      `json:"desc"`
		Prio   int         `json:"prio"`
		Due    string      `json:"due"`
		Effort string      `json:"effort"`
		Tags   []string    `json:"tags"`
		Checks []wireCheck `json:"checks"`
	}{
		Title: strings.TrimSpace(m.title.Value()), Desc: strings.TrimSpace(m.desc.Value()),
		Prio: m.prio, Due: strings.TrimSpace(m.due.Value()), Effort: m.effort,
		Tags: append([]string(nil), m.tags...), Checks: wireChecks,
	})
}

func (m *Model) applyDraft(draft ai.Draft) {
	if draft.Title != "" {
		m.title.SetValue(draft.Title)
	}
	m.emoji.SetValue(draft.Emoji)
	m.desc.SetValue(draft.Desc)
	m.prio, m.effort = draft.Prio, draft.Effort
	m.due.SetValue(draft.Due)
	// A drafted project:: label lands in the project field; the card still
	// carries exactly one, whatever the model returned.
	named, rest := project.SplitTags(draft.Tags)
	m.tags = append([]string(nil), rest...)
	if len(named) > 0 && named[0] != "" {
		m.project.SetValue(named[0])
	}
	checks := make([]board.Check, len(draft.Checks))
	for i, check := range draft.Checks {
		checks[i] = board.Check{Text: check.Text, Done: check.Done}
	}
	m.checks.SetValue(checksToText(checks))
}

func (m *Model) cancelDraft() {
	if m.draftCancel != nil {
		m.draftCancel()
	}
	m.draftCancel = nil
	if m.drafting {
		m.draftGen++
	}
	m.drafting = false
}

func (m *Model) requestClose() {
	if m.Dirty() {
		m.guardClose = true
		m.statusMessage = "unsaved changes: D discard, Esc keep editing"
		m.statusIsError = true
		return
	}
	m.cancelDraft()
	m.open = false
}

func (m *Model) updatePriority(key string) tea.Cmd {
	delta := 0
	switch key {
	case "left", "h", "-":
		delta = -1
	case "right", "l", "+", "enter", " ":
		delta = 1
	}
	if delta != 0 {
		m.prio = (m.prio-1+delta+4)%4 + 1
	}
	return nil
}

func (m *Model) updateEffort(key string) tea.Cmd {
	values := [...]string{"", "S", "M", "L"}
	index := 0
	for i, value := range values {
		if value == m.effort {
			index = i
		}
	}
	switch key {
	case "left", "h", "-":
		index = (index - 1 + len(values)) % len(values)
	case "right", "l", "+", "enter", " ":
		index = (index + 1) % len(values)
	case "backspace", "delete", "ctrl+x":
		index = 0
	default:
		return nil
	}
	m.effort = values[index]
	return nil
}

// updateDuePicker steps the date while the due input has focus. The stepper
// deliberately owns only [ and ]: alt+left and alt+right are the word motions
// the bubbles text input binds by default, and a focused field outranks a pane
// shortcut that collides with it.
func (m *Model) updateDuePicker(key string) (tea.Cmd, bool) {
	switch key {
	case "[":
		m.due.SetValue(adjustDate(m.due.Value(), -1, m.now()))
		return nil, true
	case "]":
		m.due.SetValue(adjustDate(m.due.Value(), 1, m.now()))
		return nil, true
	case "ctrl+x":
		m.due.SetValue("")
		return nil, true
	}
	return nil, false
}

func (m *Model) updateLabels(key string, msg tea.KeyPressMsg) (tea.Cmd, bool) {
	suggestions := m.filteredLabels()
	beforeExclusions := m.currentExclusions()
	switch key {
	case "down":
		m.labelsOpen = len(suggestions) > 0
		if len(suggestions) > 0 {
			m.labelHighlight = (m.labelHighlight + 1) % len(suggestions)
		}
		return nil, true
	case "up":
		m.labelsOpen = len(suggestions) > 0
		if len(suggestions) > 0 {
			m.labelHighlight = (m.labelHighlight - 1 + len(suggestions)) % len(suggestions)
		}
		return nil, true
	case "enter":
		if m.labelsOpen && len(suggestions) > 0 {
			m.addLabels(suggestions[min(m.labelHighlight, len(suggestions)-1)])
		} else if strings.TrimSpace(m.label.Value()) != "" {
			m.addLabels(m.label.Value())
		} else {
			return nil, false
		}
		return m.rescheduleIfExclusionsChanged(beforeExclusions), true
	case "backspace":
		if m.label.Value() == "" && len(m.tags) > 0 {
			m.tags = m.tags[:len(m.tags)-1]
			m.labelsOpen = true
			return m.rescheduleIfExclusionsChanged(beforeExclusions), true
		}
	case "ctrl+x":
		m.label.SetValue("")
		m.tags = nil
		m.labelsOpen = true
		return m.rescheduleIfExclusionsChanged(beforeExclusions), true
	}
	before := m.label.Value()
	var cmd tea.Cmd
	m.label, cmd = m.label.Update(msg)
	if m.label.Value() != before {
		m.labelsOpen, m.labelHighlight = true, 0
	}
	return cmd, true
}

func (m *Model) addLabels(raw string) {
	for _, candidate := range strings.Fields(raw) {
		candidate = strings.TrimLeft(candidate, "#")
		if candidate == "" {
			continue
		}
		// A project:: label typed here is as explicit as one typed in the
		// project field, and a card has room for exactly one: it moves the
		// card rather than joining the label list.
		if name, ok := strings.CutPrefix(candidate, project.LabelPrefix); ok {
			m.project.SetValue(name)
			continue
		}
		if contains(m.tags, candidate) {
			continue
		}
		m.tags = append(m.tags, candidate)
	}
	m.label.SetValue("")
	m.labelHighlight = 0
	m.labelsOpen = true
}

// markKey routes a key through the select-all mark of the focused text field.
// It reports whether the mark consumed it; every other focus target has no
// field to mark and passes straight through.
func (m *Model) markKey(msg tea.KeyPressMsg) bool {
	switch m.focus {
	case "title":
		return m.mark.Input(m.focus, &m.title, msg)
	case "emoji":
		return m.mark.Input(m.focus, &m.emoji, msg)
	case "due":
		return m.mark.Input(m.focus, &m.due, msg)
	case "project":
		return m.mark.Input(m.focus, &m.project, msg)
	case "labels":
		return m.mark.Input(m.focus, &m.label, msg)
	case "desc":
		return m.mark.Area(m.focus, &m.desc, msg)
	case "checks":
		return m.mark.Area(m.focus, &m.checks, msg)
	case "ai-prompt":
		return m.mark.Area(m.focus, &m.draftPrompt, msg)
	}
	return false
}

// marked reports whether a body row belongs to the field the mark is on.
func (m *Model) marked(target string) bool {
	return target != "" && target == m.focus && m.mark.Active(target)
}

func (m *Model) updateFocusedInput(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	switch m.focus {
	case "title":
		before := m.title.Value()
		m.title, cmd = m.title.Update(msg)
		if before != m.title.Value() {
			return batch(cmd, m.scheduleSimilar())
		}
	case "emoji":
		m.emoji, cmd = m.emoji.Update(msg)
	case "desc":
		m.desc, cmd = m.desc.Update(msg)
	case "due":
		m.due, cmd = m.due.Update(msg)
	case "project":
		m.project, cmd = m.project.Update(msg)
	case "checks":
		m.checks, cmd = m.checks.Update(msg)
	case "ai-prompt":
		m.draftPrompt, cmd = m.draftPrompt.Update(msg)
	}
	return cmd
}

func (m *Model) moveFocus(delta int) {
	targets := m.focusTargets()
	if len(targets) == 0 {
		return
	}
	index := 0
	for i, target := range targets {
		if target == m.focus {
			index = i
			break
		}
	}
	m.focus = targets[(index+delta+len(targets))%len(targets)]
	m.guardClose = false
	m.applyFocus()
}

func (m Model) focusTargets() []string {
	targets := make([]string, 0, 16)
	if m.runner != nil {
		targets = append(targets, "ai-prompt", "ai-draft")
	}
	targets = append(targets, "title", "emoji", "desc", "prio", "due", "effort", "blocked",
		"project", "labels", "checks")
	if !m.dismissedAll {
		for _, hit := range m.visibleSimilar() {
			targets = append(targets, "similar:"+similarKey(hit))
		}
		if len(m.visibleSimilar()) > 0 {
			targets = append(targets, "similar:all")
		}
	}
	return append(targets, "cancel", "save")
}

func (m *Model) applyFocus() tea.Cmd {
	m.mark.Drop()
	m.title.Blur()
	m.emoji.Blur()
	m.desc.Blur()
	m.due.Blur()
	m.project.Blur()
	m.label.Blur()
	m.checks.Blur()
	m.draftPrompt.Blur()
	switch m.focus {
	case "title":
		return m.title.Focus()
	case "emoji":
		return m.emoji.Focus()
	case "desc":
		return m.desc.Focus()
	case "due":
		return m.due.Focus()
	case "project":
		return m.project.Focus()
	case "labels":
		m.labelsOpen = true
		return m.label.Focus()
	case "checks":
		return m.checks.Focus()
	case "ai-prompt":
		return m.draftPrompt.Focus()
	}
	return nil
}

func (m *Model) activateSimilar() {
	if m.focus == "similar:all" {
		m.dismissedAll = true
		m.focus = "save"
		m.applyFocus()
		return
	}
	key := strings.TrimPrefix(m.focus, "similar:")
	m.dismissed[key] = struct{}{}
	m.focus = "title"
	m.applyFocus()
}

func (m *Model) startSave() tea.Cmd {
	task, patch, err := m.buildSave()
	if err != nil {
		m.statusMessage, m.statusIsError = safeError(err), true
		return nil
	}
	m.saving = true
	m.statusMessage, m.statusIsError = "saving card...", false
	session := m.session
	if m.mode == modeAdd {
		return tea.Batch(m.startSpinner(), func() tea.Msg {
			created, saveErr := m.store.AddTask(m.user, task)
			return saveCompletedMsg{session: session, task: created, err: saveErr}
		})
	}
	id := m.base.ID
	expected := expectedTaskFields(m.canonical, m.changedFields())
	return tea.Batch(m.startSpinner(), func() tea.Msg {
		updated, saveErr := m.store.UpdateTaskIfFieldsMatch(m.user, id, expected, patch)
		return saveCompletedMsg{session: session, task: updated, err: saveErr}
	})
}

func expectedTaskFields(task board.Task, changed editedFields) store.TaskPatch {
	var expected store.TaskPatch
	if changed.emoji {
		value := task.Emoji
		expected.Emoji = &value
	}
	if changed.title {
		value := task.Title
		expected.Title = &value
	}
	if changed.desc {
		value := task.Desc
		expected.Desc = &value
	}
	if changed.due {
		value := task.Due
		expected.Due = &value
	}
	if changed.effort {
		value := task.Effort
		expected.Effort = &value
	}
	if changed.prio {
		value := task.Prio
		expected.Prio = &value
	}
	if changed.blocked {
		value := task.Blocked
		expected.Blocked = &value
	}
	if changed.tags {
		value := append([]string(nil), task.Tags...)
		expected.Tags = &value
	}
	if changed.checks {
		value := append([]board.Check(nil), task.Checks...)
		expected.Checks = &value
	}
	return expected
}

func (m Model) buildSave() (board.Task, store.TaskPatch, error) {
	task := m.base
	if m.mode == modeAdd {
		task = board.Task{Status: m.status}
	}
	task.Title = strings.TrimSpace(m.title.Value())
	task.Emoji = strings.TrimSpace(m.emoji.Value())
	task.Desc = strings.TrimSpace(m.desc.Value())
	task.Prio, task.Due, task.Effort = m.prio, strings.TrimSpace(m.due.Value()), m.effort
	task.Blocked = m.blocked
	// The one-project invariant, held here because this is the editor's only
	// write path: no card reaches the store carrying zero or two projects.
	tags, err := project.Ensure(m.tags, m.project.Value())
	if err != nil {
		return board.Task{}, store.TaskPatch{}, fmt.Errorf("%w; every card belongs to exactly one project", err)
	}
	task.Tags = tags
	task.Checks = textToChecks(m.checks.Value())
	if err := store.ValidateTaskFields(task); err != nil {
		return board.Task{}, store.TaskPatch{}, err
	}
	if m.mode == modeEdit {
		if !m.canonicalFound {
			return board.Task{}, store.TaskPatch{}, errors.New("card disappeared outside the editor; copy the edits before closing")
		}
		patch, err := selectiveTaskPatch(m.base, m.canonical, task, m.changedFields())
		return task, patch, err
	}
	patch := store.TaskPatch{
		Emoji: &task.Emoji, Title: &task.Title, Desc: &task.Desc,
		Due: &task.Due, Effort: &task.Effort, Prio: &task.Prio,
		Blocked: &task.Blocked, Tags: &task.Tags, Checks: &task.Checks,
	}
	return task, patch, nil
}

func selectiveTaskPatch(original, canonical, desired board.Task, changed editedFields) (store.TaskPatch, error) {
	var patch store.TaskPatch
	var conflicts []string
	if changed.emoji {
		setStringPatch("emoji", original.Emoji, canonical.Emoji, desired.Emoji, &patch.Emoji, &conflicts)
	}
	if changed.title {
		setStringPatch("title", original.Title, canonical.Title, desired.Title, &patch.Title, &conflicts)
	}
	if changed.desc {
		setStringPatch("description", original.Desc, canonical.Desc, desired.Desc, &patch.Desc, &conflicts)
	}
	if changed.due {
		setStringPatch("due", original.Due, canonical.Due, desired.Due, &patch.Due, &conflicts)
	}
	if changed.effort {
		setStringPatch("effort", original.Effort, canonical.Effort, desired.Effort, &patch.Effort, &conflicts)
	}
	if changed.prio {
		if canonical.Prio != original.Prio && desired.Prio != canonical.Prio {
			conflicts = append(conflicts, "priority")
		} else if desired.Prio != canonical.Prio {
			patch.Prio = &desired.Prio
		}
	}
	if changed.blocked {
		if desired.Blocked != canonical.Blocked {
			patch.Blocked = &desired.Blocked
		}
	}
	if changed.tags {
		if !stringSlicesEqual(canonical.Tags, original.Tags) && !stringSlicesEqual(desired.Tags, canonical.Tags) {
			conflicts = append(conflicts, "labels")
		} else if !stringSlicesEqual(desired.Tags, canonical.Tags) {
			patch.Tags = &desired.Tags
		}
	}
	if changed.checks {
		if !checksEqual(canonical.Checks, original.Checks) && !checksEqual(desired.Checks, canonical.Checks) {
			conflicts = append(conflicts, "checklist")
		} else if !checksEqual(desired.Checks, canonical.Checks) {
			patch.Checks = &desired.Checks
		}
	}
	if len(conflicts) > 0 {
		return store.TaskPatch{}, fmt.Errorf(
			"card changed outside the editor in %s; match the refreshed value or reopen the editor",
			strings.Join(conflicts, ", "),
		)
	}
	return patch, nil
}

func (m Model) changedFields() editedFields {
	current := m.currentSnapshot()
	return editedFields{
		title: current.title != m.initial.title, emoji: current.emoji != m.initial.emoji,
		desc: current.desc != m.initial.desc, due: current.due != m.initial.due,
		effort: current.effort != m.initial.effort, prio: current.prio != m.initial.prio,
		blocked: current.blocked != m.initial.blocked,
		// The project is stored as a label, so moving a card between projects
		// is a label change to every path downstream of here.
		tags:   current.tags != m.initial.tags || current.project != m.initial.project,
		checks: current.checks != m.initial.checks,
	}
}

func setStringPatch(name, original, canonical, desired string, target **string, conflicts *[]string) {
	if desired == original {
		return
	}
	if canonical != original && desired != canonical {
		*conflicts = append(*conflicts, name)
		return
	}
	if desired != canonical {
		*target = &desired
	}
}

func stringSlicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func checksEqual(left, right []board.Check) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func (m *Model) loadLabels() tea.Cmd {
	session := m.session
	return func() tea.Msg {
		labels, err := m.store.Labels(m.user)
		return labelsLoadedMsg{session: session, labels: labels, err: err}
	}
}

func (m *Model) scheduleSimilar() tea.Cmd {
	m.similarGen++
	generation := m.similarGen
	query := strings.TrimSpace(m.title.Value())
	exclusions := m.currentExclusions()
	if runeCount(query) < 3 {
		m.similar, m.similarErr, m.similarQuery, m.similarExclusions = nil, nil, "", ""
		m.similarLoading, m.dismissedAll = false, false
		m.dismissed = make(map[string]struct{})
		return nil
	}
	if cached, ok := m.similarCache[similarCacheKey(query, exclusions)]; ok {
		m.similar = cloneHits(cached)
		m.similarQuery, m.similarExclusions = query, exclusions
		m.similarLoading, m.similarErr = false, nil
		m.dismissed, m.dismissedAll = make(map[string]struct{}), false
		return nil
	}
	return theme.Tick(m.themeStyles().Timing.SimilarDelay,
		similarDebounceMsg{generation: generation, query: query, exclusions: exclusions})
}

func (m *Model) searchSimilar(generation uint64, query, exclusions string) tea.Cmd {
	excludeID := ""
	if m.mode == modeEdit {
		excludeID = m.base.ID
	}
	links := importLinks(m.tags)
	return func() tea.Msg {
		hits, err := m.store.SearchSimilar(m.user, query, excludeID, links, similarLimit)
		return similarLoadedMsg{generation: generation, query: query, exclusions: exclusions, hits: hits, err: err}
	}
}

func (m *Model) rescheduleIfExclusionsChanged(before string) tea.Cmd {
	if before == m.currentExclusions() {
		return nil
	}
	return m.scheduleSimilar()
}

func (m Model) currentExclusions() string {
	links := importLinks(m.tags)
	sort.Strings(links)
	return strings.Join(links, "\x00")
}

func similarCacheKey(query, exclusions string) string { return query + "\x00" + exclusions }

func cloneHits(hits []store.SimilarHit) []store.SimilarHit {
	return append([]store.SimilarHit(nil), hits...)
}

func (m Model) currentSnapshot() snapshot {
	tags := append([]string(nil), m.tags...)
	return snapshot{
		title: m.title.Value(), emoji: m.emoji.Value(), desc: m.desc.Value(),
		due: m.due.Value(), effort: m.effort, checks: m.checks.Value(),
		prio: m.prio, blocked: m.blocked, tags: strings.Join(tags, "\x00"),
		project: m.project.Value(),
	}
}

func checksToText(checks []board.Check) string {
	lines := make([]string, 0, len(checks))
	for _, check := range checks {
		prefix := ""
		if check.Done {
			prefix = "x "
		}
		lines = append(lines, prefix+check.Text)
	}
	return strings.Join(lines, "\n")
}

func textToChecks(source string) []board.Check {
	var checks []board.Check
	for _, raw := range strings.Split(source, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		check := board.Check{Text: line}
		if strings.HasPrefix(line, "x ") {
			check.Done = true
			check.Text = strings.TrimSpace(strings.TrimPrefix(line, "x "))
		}
		checks = append(checks, check)
	}
	return checks
}

func (m Model) filteredLabels() []string {
	selected := make(map[string]struct{}, len(m.tags))
	for _, tag := range m.tags {
		selected[tag] = struct{}{}
	}
	query := strings.ToLower(strings.TrimSpace(m.label.Value()))
	starts, containsMatches := make([]string, 0, 8), make([]string, 0, 8)
	for _, label := range m.labels {
		if _, exists := selected[label]; exists {
			continue
		}
		lower := strings.ToLower(label)
		switch {
		case strings.HasPrefix(lower, query):
			starts = append(starts, label)
		case strings.Contains(lower, query):
			containsMatches = append(containsMatches, label)
		}
	}
	out := append(starts, containsMatches...)
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

func (m Model) visibleSimilar() []store.SimilarHit {
	if m.dismissedAll {
		return nil
	}
	visible := make([]store.SimilarHit, 0, len(m.similar))
	for _, hit := range m.similar {
		if _, dismissed := m.dismissed[similarKey(hit)]; !dismissed {
			visible = append(visible, hit)
		}
	}
	return visible
}

func similarKey(hit store.SimilarHit) string {
	switch {
	case hit.ID != "":
		return "id:" + hit.ID
	case hit.Link != "":
		return "link:" + hit.Link
	default:
		return "title:" + hit.Title
	}
}

func importLinks(tags []string) []string {
	seen := make(map[string]struct{})
	var links []string
	for _, tag := range tags {
		if !strings.HasPrefix(tag, "link::") {
			continue
		}
		link := strings.TrimSpace(strings.TrimPrefix(tag, "link::"))
		if link == "" {
			continue
		}
		if _, exists := seen[link]; exists {
			continue
		}
		seen[link] = struct{}{}
		links = append(links, link)
	}
	return links
}

func unionLabels(a, b []string) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	for _, label := range append(append([]string(nil), a...), b...) {
		if label != "" {
			seen[label] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for label := range seen {
		out = append(out, label)
	}
	sort.Strings(out)
	return out
}

func adjustDate(raw string, days int, now time.Time) string {
	date, err := time.Parse("2006-01-02", strings.TrimSpace(raw))
	if err != nil {
		date = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	}
	return date.AddDate(0, 0, days).Format("2006-01-02")
}

func safeError(err error) string {
	if err == nil {
		return ""
	}
	message := sanitize(strings.Join(strings.Fields(err.Error()), " "))
	if message == "" {
		return "operation failed"
	}
	runes := []rune(message)
	if len(runes) > 180 {
		message = string(runes[:179]) + "…"
	}
	return message
}

func sanitize(value string) string {
	value = ansi.Strip(value)
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
}

func batch(commands ...tea.Cmd) tea.Cmd {
	filtered := make([]tea.Cmd, 0, len(commands))
	for _, command := range commands {
		if command != nil {
			filtered = append(filtered, command)
		}
	}
	switch len(filtered) {
	case 0:
		return nil
	case 1:
		return filtered[0]
	default:
		return tea.Batch(filtered...)
	}
}

func runeCount(value string) int { return len([]rune(value)) }

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
