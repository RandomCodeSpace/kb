// Package cardeditor implements the direct-store create and edit overlay.
package cardeditor

import (
	"sort"
	"strings"
	"time"
	"unicode"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
)

const (
	similarDelay = 400 * time.Millisecond
	similarLimit = 10
)

// Store is the direct SQLite projection used by the editor. It deliberately
// mirrors the store package instead of introducing an HTTP-shaped adapter.
type Store interface {
	AddTask(string, board.Task) (board.Task, error)
	UpdateTask(string, string, store.TaskPatch) (board.Task, error)
	Labels(string) ([]string, error)
	SearchSimilar(string, string, string, []string, int) ([]store.SimilarHit, error)
}

type mode uint8

const (
	modeAdd mode = iota
	modeEdit
)

type labelsLoadedMsg struct {
	labels []string
	err    error
}

type similarDebounceMsg struct {
	generation uint64
	query      string
}

type similarLoadedMsg struct {
	generation uint64
	query      string
	hits       []store.SimilarHit
	err        error
}

type saveCompletedMsg struct {
	task board.Task
	err  error
}

type snapshot struct {
	title, emoji, desc, due, effort, checks string
	prio                                    int
	blocked                                 bool
	tags                                    string
}

// Model owns every mutable editor field and all asynchronous generations.
type Model struct {
	store Store
	user  string
	now   func() time.Time

	open       bool
	mode       mode
	status     board.Status
	base       board.Task
	initial    snapshot
	focus      string
	guardClose bool
	saving     bool
	saved      bool
	stale      bool

	title   textinput.Model
	emoji   textinput.Model
	desc    textarea.Model
	due     textinput.Model
	label   textinput.Model
	checks  textarea.Model
	prio    int
	effort  string
	blocked bool
	tags    []string

	labels         []string
	labelsOpen     bool
	labelHighlight int
	labelsErr      error
	similar        []store.SimilarHit
	dismissed      map[string]struct{}
	dismissedAll   bool
	similarLoading bool
	similarErr     error
	similarQuery   string
	similarGen     uint64
	statusMessage  string
	statusIsError  bool
	scroll         int
}

// New creates a closed editor. A nil store keeps the feature unavailable in
// lightweight root-model tests.
func New(st Store, user string) Model {
	m := Model{store: st, user: user, now: time.Now}
	m.resetInputs()
	return m
}

// Enabled reports whether the root has a writable direct-store backend.
func (m Model) Enabled() bool { return m.store != nil }

// IsOpen reports whether the overlay owns input and rendering.
func (m Model) IsOpen() bool { return m.open }

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

// ConsumeSaved reports one acknowledged mutation to the root exactly once.
func (m *Model) ConsumeSaved() bool {
	saved := m.saved
	m.saved = false
	return saved
}

// IsMessage identifies editor-owned asynchronous results without exporting
// implementation messages into the root package.
func IsMessage(message tea.Msg) bool {
	switch message.(type) {
	case labelsLoadedMsg, similarDebounceMsg, similarLoadedMsg, saveCompletedMsg:
		return true
	default:
		return false
	}
}

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
	m.mode, m.base, m.status = nextMode, task, task.Status
	m.open, m.guardClose, m.saving, m.saved, m.stale = true, false, false, false, false
	m.labels, m.similar = nil, nil
	m.dismissed = make(map[string]struct{})
	m.dismissedAll = false
	m.labelsOpen, m.labelHighlight = false, 0
	m.labelsErr, m.similarErr = nil, nil
	m.similarLoading, m.similarQuery = false, ""
	m.statusMessage, m.statusIsError, m.scroll = "", false, 0
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
	m.desc = editorArea("Description", 4)
	m.checks = editorArea("one per line; prefix x when done", 4)
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
	m.tags = append([]string(nil), task.Tags...)
	m.checks.SetValue(checksToText(task.Checks))
}

// Refresh adopts an external store snapshot only while the form is clean.
// Dirty input remains authoritative and receives a scoped stale warning.
func (m *Model) Refresh(task board.Task, found bool) tea.Cmd {
	if !m.open || m.mode != modeEdit {
		return nil
	}
	if m.Dirty() {
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
		m.open = false
		return nil
	}
	m.base = task
	m.applyTask(task)
	m.initial = m.currentSnapshot()
	m.stale = false
	m.statusMessage, m.statusIsError = "card refreshed", false
	return m.scheduleSimilar()
}

// Update handles form input, store results, and stale-result generations.
func (m *Model) Update(message tea.Msg) tea.Cmd {
	if !m.open {
		return nil
	}
	switch msg := message.(type) {
	case labelsLoadedMsg:
		m.labelsErr = msg.err
		if msg.err == nil {
			m.labels = unionLabels(msg.labels, m.tags)
		}
		return nil
	case similarDebounceMsg:
		if msg.generation != m.similarGen || strings.TrimSpace(m.title.Value()) != msg.query || runeCount(msg.query) < 3 {
			return nil
		}
		m.similarLoading, m.similarErr = true, nil
		return m.searchSimilar(msg.generation, msg.query)
	case similarLoadedMsg:
		if msg.generation != m.similarGen || strings.TrimSpace(m.title.Value()) != msg.query {
			return nil
		}
		m.similarLoading = false
		m.similarErr = msg.err
		if msg.err == nil {
			m.similar = msg.hits
			m.similarQuery = msg.query
			m.dismissed = make(map[string]struct{})
			m.dismissedAll = false
		}
		return nil
	case saveCompletedMsg:
		m.saving = false
		if msg.err != nil {
			m.statusMessage = "save refused: " + safeError(msg.err)
			m.statusIsError = true
			return nil
		}
		m.base = msg.task
		m.initial = m.currentSnapshot()
		m.saved, m.open = true, false
		return nil
	case tea.KeyPressMsg:
		return m.updateKey(msg)
	}
	return nil
}

func (m *Model) updateKey(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	if m.saving {
		return nil
	}
	if m.guardClose {
		switch key {
		case "d", "D":
			m.open, m.guardClose = false, false
		case "esc":
			m.guardClose = false
			m.statusMessage, m.statusIsError = "close cancelled", false
		}
		return nil
	}
	if key == "ctrl+s" {
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

func (m *Model) requestClose() {
	if m.Dirty() {
		m.guardClose = true
		m.statusMessage = "unsaved changes: D discard, Esc keep editing"
		m.statusIsError = true
		return
	}
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

func (m *Model) updateDuePicker(key string) (tea.Cmd, bool) {
	switch key {
	case "alt+left", "[":
		m.due.SetValue(adjustDate(m.due.Value(), -1, m.now()))
		return nil, true
	case "alt+right", "]":
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
		return nil, true
	case "backspace":
		if m.label.Value() == "" && len(m.tags) > 0 {
			m.tags = m.tags[:len(m.tags)-1]
			m.labelsOpen = true
			return nil, true
		}
	case "ctrl+x":
		m.label.SetValue("")
		m.tags = nil
		m.labelsOpen = true
		return nil, true
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
		if candidate == "" || contains(m.tags, candidate) {
			continue
		}
		m.tags = append(m.tags, candidate)
	}
	m.label.SetValue("")
	m.labelHighlight = 0
	m.labelsOpen = true
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
	case "checks":
		m.checks, cmd = m.checks.Update(msg)
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
	targets := []string{"title", "emoji", "desc", "prio", "due", "effort", "blocked", "labels", "checks"}
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
	m.title.Blur()
	m.emoji.Blur()
	m.desc.Blur()
	m.due.Blur()
	m.label.Blur()
	m.checks.Blur()
	switch m.focus {
	case "title":
		return m.title.Focus()
	case "emoji":
		return m.emoji.Focus()
	case "desc":
		return m.desc.Focus()
	case "due":
		return m.due.Focus()
	case "labels":
		m.labelsOpen = true
		return m.label.Focus()
	case "checks":
		return m.checks.Focus()
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
	if m.mode == modeAdd {
		return func() tea.Msg {
			created, saveErr := m.store.AddTask(m.user, task)
			return saveCompletedMsg{task: created, err: saveErr}
		}
	}
	id := m.base.ID
	return func() tea.Msg {
		updated, saveErr := m.store.UpdateTask(m.user, id, patch)
		return saveCompletedMsg{task: updated, err: saveErr}
	}
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
	task.Tags = append([]string(nil), m.tags...)
	task.Checks = textToChecks(m.checks.Value())
	if err := store.ValidateTaskFields(task); err != nil {
		return board.Task{}, store.TaskPatch{}, err
	}
	patch := store.TaskPatch{
		Emoji: &task.Emoji, Title: &task.Title, Desc: &task.Desc,
		Due: &task.Due, Effort: &task.Effort, Prio: &task.Prio,
		Blocked: &task.Blocked, Tags: &task.Tags, Checks: &task.Checks,
	}
	return task, patch, nil
}

func (m *Model) loadLabels() tea.Cmd {
	return func() tea.Msg {
		labels, err := m.store.Labels(m.user)
		return labelsLoadedMsg{labels: labels, err: err}
	}
}

func (m *Model) scheduleSimilar() tea.Cmd {
	m.similarGen++
	generation := m.similarGen
	query := strings.TrimSpace(m.title.Value())
	if runeCount(query) < 3 {
		m.similar, m.similarErr, m.similarQuery = nil, nil, ""
		m.similarLoading, m.dismissedAll = false, false
		m.dismissed = make(map[string]struct{})
		return nil
	}
	if query == strings.TrimSpace(m.similarQuery) {
		return nil
	}
	return tea.Tick(similarDelay, func(time.Time) tea.Msg {
		return similarDebounceMsg{generation: generation, query: query}
	})
}

func (m *Model) searchSimilar(generation uint64, query string) tea.Cmd {
	excludeID := ""
	if m.mode == modeEdit {
		excludeID = m.base.ID
	}
	links := importLinks(m.tags)
	return func() tea.Msg {
		hits, err := m.store.SearchSimilar(m.user, query, excludeID, links, similarLimit)
		return similarLoadedMsg{generation: generation, query: query, hits: hits, err: err}
	}
}

func (m Model) currentSnapshot() snapshot {
	tags := append([]string(nil), m.tags...)
	return snapshot{
		title: m.title.Value(), emoji: m.emoji.Value(), desc: m.desc.Value(),
		due: m.due.Value(), effort: m.effort, checks: m.checks.Value(),
		prio: m.prio, blocked: m.blocked, tags: strings.Join(tags, "\x00"),
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
		date = now.UTC()
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
