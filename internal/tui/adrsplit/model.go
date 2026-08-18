// Package adrsplit implements the direct-runner ADR-to-stories review overlay.
package adrsplit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/ai"
	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
	"github.com/RandomCodeSpace/kb/internal/tui/pointer"
)

const (
	maxADRBytes    = 64 << 10
	defaultMax     = 8
	maxStories     = 20
	splitMaxTokens = 8192
)

var errADRTooLarge = errors.New("ADR is over 64 KiB")

// Runner is the shared direct-store AI runner. The overlay deliberately has
// no HTTP-shaped seam: it invokes the same package as the server adapters.
type Runner interface {
	RunSkill(context.Context, string, ai.Scope, string, string, int, int64) (ai.RunResult, error)
}

// Store is the one direct SQLite write the review step needs.
type Store interface {
	AddTask(string, board.Task) (board.Task, error)
}

type stage uint8

const (
	stageInput stage = iota
	stageReview
)

type sourceMode uint8

const (
	sourcePaste sourceMode = iota
	sourceFile
)

type storyRow struct {
	draft   ai.Draft
	include bool
	title   textinput.Model
	prio    int
	effort  string
	created bool
	err     string
}

type fileLoadedMsg struct {
	session    uint64
	generation uint64
	text       string
	err        error
}

type splitCompletedMsg struct {
	session    uint64
	generation uint64
	run        ai.RunResult
	err        error
}

type cardAddedMsg struct {
	session    uint64
	generation uint64
	row        int
	task       board.Task
	err        error
}

// Model owns the source, asynchronous split, review edits, and sequential
// batch write. Every result is scoped to both the overlay session and its
// operation generation so a cancelled or reopened dialog cannot be mutated.
type Model struct {
	store  Store
	runner Runner
	user   string
	ctx    context.Context

	open       bool
	stage      stage
	source     sourceMode
	session    uint64
	generation uint64
	focus      string
	guardClose bool
	operation  string
	cancel     context.CancelFunc
	changed    bool

	adr      textarea.Model
	filePath textinput.Model
	max      int
	dest     board.Status
	rows     []storyRow

	adding        bool
	addQueue      []int
	addPosition   int
	addGeneration uint64
	createdCount  int
	failedCount   int

	status        string
	statusIsError bool
	scroll        int
	manualScroll  bool
	pointerState  pointer.State
}

// New creates a closed overlay. Nil dependencies keep the feature unavailable
// in lightweight root-model tests.
func New(st Store, runner Runner, user string, ctx context.Context) Model {
	if ctx == nil {
		ctx = context.Background()
	}
	m := Model{store: st, runner: runner, user: user, ctx: ctx}
	m.resetInputs()
	return m
}

// Enabled reports whether both the shared runner and direct store are wired.
func (m Model) Enabled() bool { return m.store != nil && m.runner != nil }

// IsOpen reports whether this overlay owns user input and rendering.
func (m Model) IsOpen() bool { return m.open }

// Open starts a new isolated overlay session.
func (m *Model) Open() tea.Cmd {
	if !m.Enabled() {
		return nil
	}
	m.closeNow()
	m.session++
	m.generation++
	m.open, m.stage, m.source = true, stageInput, sourcePaste
	m.focus, m.guardClose, m.status, m.statusIsError = "source", false, "", false
	m.max, m.dest, m.rows = defaultMax, board.StatusTodo, nil
	m.changed, m.scroll, m.manualScroll, m.pointerState = false, 0, false, pointer.State{}
	m.resetInputs()
	return m.applyFocus()
}

// Close force-closes the overlay and cancels any in-flight read or AI run.
// A batch write already handed to SQLite is not cancellable; its completion is
// safely ignored after the session advances.
func (m *Model) Close() {
	m.closeNow()
	m.session++
}

func (m *Model) closeNow() {
	m.cancelOperation()
	m.open, m.guardClose = false, false
	m.adding = false
	m.addQueue = nil
	m.pointerState = pointer.State{}
}

// ConsumeChanged reports durable creations to the root exactly once.
func (m *Model) ConsumeChanged() bool {
	if m.adding {
		return false
	}
	changed := m.changed
	m.changed = false
	return changed
}

// IsMessage identifies overlay-owned asynchronous results.
func IsMessage(message tea.Msg) bool {
	if pointer.IsMessage(message) {
		return true
	}
	switch message.(type) {
	case fileLoadedMsg, splitCompletedMsg, cardAddedMsg, pointerActionMsg:
		return true
	default:
		return false
	}
}

// Update applies user input and scoped asynchronous results.
func (m *Model) Update(message tea.Msg) tea.Cmd {
	if !m.open {
		return nil
	}
	state, command, handled := m.pointerState.Update(message)
	if handled {
		m.pointerState = state
		return command
	}
	switch msg := message.(type) {
	case fileLoadedMsg:
		if msg.session != m.session || msg.generation != m.generation || m.operation != "reading file" {
			return nil
		}
		m.completeOperation()
		if msg.err != nil {
			m.setError(msg.err)
			return nil
		}
		return m.startRun(msg.text)
	case splitCompletedMsg:
		if msg.session != m.session || msg.generation != m.generation || m.operation != "splitting ADR" {
			return nil
		}
		m.completeOperation()
		if msg.err != nil {
			m.setError(msg.err)
			return nil
		}
		if len(msg.run.Cards) == 0 {
			m.setError(errors.New("the model returned no usable stories"))
			return nil
		}
		m.rows = rowsFromDrafts(msg.run.Cards)
		m.stage, m.focus, m.scroll = stageReview, "include:0", 0
		m.status, m.statusIsError = fmt.Sprintf("%d stories ready; review before creating", len(m.rows)), false
		if msg.run.Partial {
			m.status = fmt.Sprintf("%d partial stories ready; review before creating", len(m.rows))
		}
		return m.applyFocus()
	case cardAddedMsg:
		return m.finishAdd(msg)
	case pointerActionMsg:
		return m.updatePointer(msg)
	case tea.KeyPressMsg:
		return m.updateKey(msg)
	}
	return nil
}

func (m *Model) updatePointer(msg pointerActionMsg) tea.Cmd {
	if msg.session != m.session || msg.generation != m.generation {
		return nil
	}
	if m.guardClose {
		switch msg.target {
		case "discard":
			m.closeNow()
		case "stay", "backdrop":
			m.guardClose = false
			m.status, m.statusIsError = "close cancelled", false
		}
		return nil
	}
	if m.operation != "" || m.adding {
		return nil
	}
	if msg.target == "backdrop" {
		m.requestClose()
		return nil
	}
	if msg.target == "scroll" {
		m.scroll = min(max(m.scroll+msg.scrollDelta, 0), msg.maxScroll)
		m.manualScroll = true
		return nil
	}
	m.focus = msg.target
	switch msg.target {
	case "source":
		if m.source == sourcePaste {
			m.source = sourceFile
		} else {
			m.source = sourcePaste
		}
		m.focus = m.inputTarget()
		return m.applyFocus()
	case "adr", "file":
		return m.applyFocus()
	case "max":
		return m.updateInputKey("enter", tea.KeyPressMsg{Code: tea.KeyEnter})
	case "cancel", "split":
		return m.updateInputKey("enter", tea.KeyPressMsg{Code: tea.KeyEnter})
	case "back", "add":
		return m.updateReviewKey("enter", tea.KeyPressMsg{Code: tea.KeyEnter})
	}
	if index, field, ok := parseRowFocus(msg.target); ok {
		if field == "title" {
			return m.applyFocus()
		}
		if field == "include" {
			return m.updateReviewKey("space", tea.KeyPressMsg{Code: tea.KeySpace})
		}
		if field == "prio" || field == "effort" {
			return m.updateReviewKey("enter", tea.KeyPressMsg{Code: tea.KeyEnter})
		}
		if index >= 0 && index < len(m.rows) {
			return m.applyFocus()
		}
	}
	if msg.target == "dest" {
		return m.updateReviewKey("enter", tea.KeyPressMsg{Code: tea.KeyEnter})
	}
	return nil
}

func (m *Model) updateKey(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	if m.operation != "" {
		if key == "esc" {
			m.cancelOperation()
			m.status, m.statusIsError = "split cancelled; source preserved", false
		}
		return nil
	}
	if m.adding {
		return nil
	}
	if m.guardClose {
		switch key {
		case "d", "D":
			m.closeNow()
		case "esc":
			m.guardClose = false
			m.status, m.statusIsError = "close cancelled", false
		}
		return nil
	}
	if key == "esc" {
		m.requestClose()
		return nil
	}
	if key == "tab" {
		m.moveFocus(1)
		return nil
	}
	if key == "shift+tab" {
		m.moveFocus(-1)
		return nil
	}
	if m.stage == stageInput {
		return m.updateInputKey(key, msg)
	}
	return m.updateReviewKey(key, msg)
}

func (m *Model) updateInputKey(key string, msg tea.KeyPressMsg) tea.Cmd {
	switch m.focus {
	case "source":
		if key == "left" || key == "h" || key == "right" || key == "l" || activationKey(key) {
			if m.source == sourcePaste {
				m.source = sourceFile
			} else {
				m.source = sourcePaste
			}
			m.focus = m.inputTarget()
			return m.applyFocus()
		}
	case "adr":
		var command tea.Cmd
		m.adr, command = m.adr.Update(msg)
		return command
	case "file":
		var command tea.Cmd
		m.filePath, command = m.filePath.Update(msg)
		return command
	case "max":
		switch key {
		case "left", "h", "-":
			m.max = max(1, m.max-1)
		case "right", "l", "+", "enter", " ", "space":
			m.max = min(maxStories, m.max+1)
		}
	case "cancel":
		if activationKey(key) {
			m.requestClose()
		}
	case "split":
		if activationKey(key) {
			return m.startSplit()
		}
	}
	return nil
}

func (m *Model) updateReviewKey(key string, msg tea.KeyPressMsg) tea.Cmd {
	index, field, ok := parseRowFocus(m.focus)
	if ok && index >= 0 && index < len(m.rows) {
		row := &m.rows[index]
		if row.created {
			return nil
		}
		switch field {
		case "include":
			if activationKey(key) {
				row.include = !row.include
				row.err = ""
			}
		case "title":
			var command tea.Cmd
			row.title, command = row.title.Update(msg)
			row.err = ""
			return command
		case "prio":
			row.prio = cycleInt(row.prio, 1, 4, key)
		case "effort":
			row.effort = cycleEffort(row.effort, key)
		}
		return nil
	}
	switch m.focus {
	case "dest":
		m.dest = cycleStatus(m.dest, key)
	case "back":
		if activationKey(key) {
			m.stage, m.rows, m.focus = stageInput, nil, "source"
			m.status, m.statusIsError = "source preserved", false
			return m.applyFocus()
		}
	case "cancel":
		if activationKey(key) {
			m.requestClose()
		}
	case "add":
		if activationKey(key) {
			return m.startAdd()
		}
	}
	return nil
}

func (m *Model) startSplit() tea.Cmd {
	if m.runner == nil {
		m.setError(errors.New("AI runner unavailable"))
		return nil
	}
	if m.source == sourcePaste {
		text := m.adr.Value()
		if strings.TrimSpace(text) == "" {
			m.setError(errors.New("paste an ADR first"))
			return nil
		}
		if len([]byte(text)) > maxADRBytes {
			m.setError(errADRTooLarge)
			return nil
		}
		return m.startRun(text)
	}
	path := strings.TrimSpace(m.filePath.Value())
	if path == "" {
		m.setError(errors.New("file path required"))
		return nil
	}
	m.generation++
	generation, session := m.generation, m.session
	ctx, cancel := context.WithCancel(m.ctx)
	m.cancel, m.operation = cancel, "reading file"
	m.status, m.statusIsError = "reading ADR file...", false
	return func() tea.Msg {
		text, err := readADRFile(ctx, path)
		return fileLoadedMsg{session: session, generation: generation, text: text, err: err}
	}
}

func (m *Model) startRun(text string) tea.Cmd {
	m.generation++
	generation, session, maximum := m.generation, m.session, m.max
	ctx, cancel := context.WithCancel(m.ctx)
	m.cancel, m.operation = cancel, "splitting ADR"
	m.status, m.statusIsError = "splitting ADR...", false
	return func() tea.Msg {
		run, err := m.runner.RunSkill(ctx, m.user, ai.ScopeReadOnly, "adr-split", text, maximum, splitMaxTokens)
		return splitCompletedMsg{session: session, generation: generation, run: run, err: err}
	}
}

func (m *Model) cancelOperation() {
	if m.cancel != nil {
		m.cancel()
	}
	m.cancel = nil
	if m.operation != "" {
		m.generation++
	}
	m.operation = ""
}

func (m *Model) completeOperation() {
	if m.cancel != nil {
		m.cancel()
	}
	m.cancel = nil
	m.operation = ""
}

func (m *Model) startAdd() tea.Cmd {
	m.addQueue = m.addQueue[:0]
	m.createdCount, m.failedCount = 0, 0
	for i := range m.rows {
		row := &m.rows[i]
		if !row.include || row.created {
			continue
		}
		row.err = ""
		if strings.TrimSpace(row.title.Value()) == "" {
			row.err = "title required"
			m.failedCount++
			continue
		}
		m.addQueue = append(m.addQueue, i)
	}
	if len(m.addQueue) == 0 {
		if m.failedCount > 0 {
			m.status, m.statusIsError = "no valid selected stories; fix the reported rows", true
		} else {
			m.status, m.statusIsError = "select at least one story", true
		}
		return nil
	}
	m.adding, m.addPosition = true, 0
	m.addGeneration++
	m.status, m.statusIsError = fmt.Sprintf("creating card 1 of %d...", len(m.addQueue)), false
	return m.addNext()
}

func (m *Model) addNext() tea.Cmd {
	rowIndex := m.addQueue[m.addPosition]
	task := taskFromRow(m.rows[rowIndex], m.dest)
	session, generation := m.session, m.addGeneration
	return func() tea.Msg {
		created, err := m.store.AddTask(m.user, task)
		return cardAddedMsg{session: session, generation: generation, row: rowIndex, task: created, err: err}
	}
}

func (m *Model) finishAdd(msg cardAddedMsg) tea.Cmd {
	if msg.session != m.session || msg.generation != m.addGeneration || !m.adding ||
		m.addPosition >= len(m.addQueue) || msg.row != m.addQueue[m.addPosition] {
		return nil
	}
	row := &m.rows[msg.row]
	if msg.err != nil {
		row.err = safeError(msg.err)
		m.failedCount++
	} else {
		row.err, row.created, row.include = "", true, false
		m.createdCount++
		m.changed = true
	}
	m.addPosition++
	if m.addPosition < len(m.addQueue) {
		m.status = fmt.Sprintf("creating card %d of %d...", m.addPosition+1, len(m.addQueue))
		return m.addNext()
	}
	m.adding = false
	if m.failedCount > 0 {
		m.status = fmt.Sprintf("created %d; %d failed - review rows and retry", m.createdCount, m.failedCount)
		m.statusIsError = true
	} else {
		m.status = fmt.Sprintf("created %d cards", m.createdCount)
		m.statusIsError = false
	}
	return nil
}

func taskFromRow(row storyRow, status board.Status) board.Task {
	checks := make([]board.Check, len(row.draft.Checks))
	for i, check := range row.draft.Checks {
		checks[i] = board.Check{Text: check.Text, Done: check.Done}
	}
	return board.Task{
		Title: sanitize(strings.TrimSpace(row.title.Value())), Emoji: row.draft.Emoji,
		Desc: row.draft.Desc, Status: status, Prio: row.prio, Due: row.draft.Due,
		Effort: row.effort, Tags: append([]string(nil), row.draft.Tags...), Checks: checks,
	}
}

func rowsFromDrafts(drafts []ai.Draft) []storyRow {
	rows := make([]storyRow, len(drafts))
	for i, draft := range drafts {
		title := textinput.New()
		title.Prompt = ""
		title.SetWidth(60)
		title.SetValue(draft.Title)
		rows[i] = storyRow{draft: draft, include: true, title: title, prio: draft.Prio, effort: draft.Effort}
	}
	return rows
}

func readADRFile(ctx context.Context, path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("read ADR file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("ADR path is not a regular file")
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("read ADR file: %w", err)
	}
	defer file.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = file.Close()
		case <-done:
		}
	}()
	data, err := io.ReadAll(io.LimitReader(file, maxADRBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("read ADR file: %w", err)
	}
	if len(data) > maxADRBytes {
		return "", errADRTooLarge
	}
	if !utf8.Valid(data) {
		return "", errors.New("ADR file is not valid UTF-8")
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}
	if strings.TrimSpace(string(data)) == "" {
		return "", errors.New("ADR file is empty")
	}
	return string(data), nil
}

func (m *Model) resetInputs() {
	m.adr = textarea.New()
	m.adr.Prompt, m.adr.Placeholder, m.adr.ShowLineNumbers = "", "# ADR 0007: adopt ...", false
	m.adr.SetWidth(64)
	m.adr.SetHeight(8)
	m.filePath = textinput.New()
	m.filePath.Prompt, m.filePath.Placeholder = "", "/path/to/decision.md"
	m.filePath.SetWidth(64)
}

func (m *Model) requestClose() {
	if m.dirty() {
		m.guardClose = true
		m.status, m.statusIsError = "reviewed work would be discarded", true
		return
	}
	m.closeNow()
}

func (m Model) dirty() bool {
	return strings.TrimSpace(m.adr.Value()) != "" || strings.TrimSpace(m.filePath.Value()) != "" || len(m.rows) > 0
}

func (m *Model) setError(err error) {
	m.status, m.statusIsError = safeError(err), true
}

func safeError(err error) string {
	if err == nil {
		return ""
	}
	var aiErr *ai.Error
	message := err.Error()
	if errors.As(err, &aiErr) && strings.TrimSpace(aiErr.Message) != "" {
		message = aiErr.Message
	}
	if errors.Is(err, context.Canceled) {
		message = "split cancelled"
	}
	message = sanitize(strings.Join(strings.Fields(message), " "))
	if message == "" {
		return "operation failed"
	}
	runes := []rune(message)
	if len(runes) > 180 {
		return string(runes[:177]) + "..."
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

func (m Model) inputTarget() string {
	if m.source == sourceFile {
		return "file"
	}
	return "adr"
}

func (m Model) focusTargets() []string {
	if m.stage == stageInput {
		return []string{"source", m.inputTarget(), "max", "cancel", "split"}
	}
	targets := make([]string, 0, len(m.rows)*4+3)
	for i, row := range m.rows {
		if row.created {
			continue
		}
		for _, field := range []string{"include", "title", "prio", "effort"} {
			targets = append(targets, fmt.Sprintf("%s:%d", field, i))
		}
	}
	return append(targets, "dest", "back", "cancel", "add")
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
	m.manualScroll = false
	m.applyFocus()
}

func (m *Model) applyFocus() tea.Cmd {
	m.manualScroll = false
	m.adr.Blur()
	m.filePath.Blur()
	for i := range m.rows {
		m.rows[i].title.Blur()
	}
	switch m.focus {
	case "adr":
		return m.adr.Focus()
	case "file":
		return m.filePath.Focus()
	}
	if index, field, ok := parseRowFocus(m.focus); ok && field == "title" && index < len(m.rows) {
		return m.rows[index].title.Focus()
	}
	return nil
}

func parseRowFocus(value string) (int, string, bool) {
	var index int
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return 0, "", false
	}
	if _, err := fmt.Sscanf(parts[1], "%d", &index); err != nil {
		return 0, "", false
	}
	return index, parts[0], true
}

func cycleInt(value, low, high int, key string) int {
	switch key {
	case "left", "h", "-":
		value--
	case "right", "l", "+", "enter", " ", "space":
		value++
	default:
		return value
	}
	if value < low {
		return high
	}
	if value > high {
		return low
	}
	return value
}

func cycleEffort(value, key string) string {
	values := []string{"", "S", "M", "L"}
	index := 0
	for i, candidate := range values {
		if candidate == value {
			index = i
		}
	}
	switch key {
	case "left", "h", "-":
		index = (index - 1 + len(values)) % len(values)
	case "right", "l", "+", "enter", " ", "space":
		index = (index + 1) % len(values)
	default:
		return value
	}
	return values[index]
}

func cycleStatus(value board.Status, key string) board.Status {
	index := 0
	for i, status := range board.Statuses {
		if status == value {
			index = i
		}
	}
	switch key {
	case "left", "h", "-":
		index = (index - 1 + len(board.Statuses)) % len(board.Statuses)
	case "right", "l", "+", "enter", " ", "space":
		index = (index + 1) % len(board.Statuses)
	}
	return board.Statuses[index]
}

func activationKey(key string) bool { return key == "enter" || key == " " || key == "space" }

var _ Store = (*store.Store)(nil)
