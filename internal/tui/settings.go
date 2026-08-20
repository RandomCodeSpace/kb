package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	kbai "github.com/RandomCodeSpace/kb/internal/ai"
	"github.com/RandomCodeSpace/kb/internal/forge"
	"github.com/RandomCodeSpace/kb/internal/store"
	"github.com/RandomCodeSpace/kb/internal/tui/formview"
	"github.com/RandomCodeSpace/kb/internal/tui/pointer"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

const settingsTestTimeout = 20 * time.Second

type settingsStore interface {
	AISettings(string) (store.AISettings, error)
	SetAISettings(string, *string, *string, *string) (bool, error)
	ForgeSources(string) ([]store.ForgeSource, error)
	SetForgeSource(string, string, string, *string, *string) (bool, error)
	DeleteForgeSource(string, string) error
}

type aiConnectionProber interface {
	Probe(context.Context, string, kbai.Config) error
}

type forgeConnectionProber interface {
	Probe(context.Context, string, forge.ForgeProbeConfig) error
}

type settingsLoadedMsg struct {
	ai      store.AISettings
	sources []store.ForgeSource
	err     error
}

type aiSettingsTestedMsg struct{ err error }
type aiSettingsSavedMsg struct {
	keySet     bool
	keyCleared bool
	err        error
}
type forgeSettingsTestedMsg struct {
	id  string
	err error
}
type forgeSettingsSavedMsg struct {
	id           string
	source       store.ForgeSource
	tokenCleared bool
	err          error
}
type forgeSettingsRemovedMsg struct {
	id  string
	err error
}
type settingsPointerMsg struct {
	owner  *settingsModel
	target string
}
type settingsWheelMsg struct{ delta int }

type integrationSettingsRow struct {
	id        string
	persisted bool
	name      textinput.Model
	kind      string
	baseURL   textinput.Model
	project   textinput.Model
	token     textinput.Model
	hasToken  bool
}

type settingsModel struct {
	ctx          context.Context
	store        settingsStore
	ai           aiConnectionProber
	forge        forgeConnectionProber
	user         string
	loaded       bool
	pointerState pointer.State

	aiBase  textinput.Model
	aiModel textinput.Model
	aiKey   textinput.Model
	hasKey  bool

	rows          []integrationSettingsRow
	nextDraft     int
	focus         string
	busy          string
	status        string
	statusIsError bool
	armedRemove   string
	scroll        int
	testCancel    context.CancelFunc
	closed        bool
	styles        *theme.Styles
	mark          formview.Mark
}

// SetStyles hands the settings pane the resolved design system. Spec section
// 6.2: the root builds it once and threads it down.
func (m *settingsModel) SetStyles(styles *theme.Styles) {
	if styles != nil {
		m.styles = styles
	}
}

// themeStyles is the resolved design system, defaulting to the dark reference
// palette until the root hands one over.
func (m *settingsModel) themeStyles() *theme.Styles {
	if m.styles == nil {
		m.styles = theme.New(true)
	}
	return m.styles
}

func newSettingsModel(st *store.Store, user string, ctx context.Context) *settingsModel {
	return newSettingsModelWithBackends(
		st,
		kbai.NewRunner(st, "", nil, nil),
		forge.NewForgeProber(st),
		user,
		ctx,
	)
}

func newSettingsModelWithBackends(
	st settingsStore,
	ai aiConnectionProber,
	forge forgeConnectionProber,
	user string,
	ctx context.Context,
) *settingsModel {
	if ctx == nil {
		ctx = context.Background()
	}
	m := &settingsModel{
		ctx:     ctx,
		store:   st,
		ai:      ai,
		forge:   forge,
		user:    user,
		aiBase:  settingsInput("https://api.openai.com/v1", false),
		aiModel: settingsInput("model", false),
		aiKey:   settingsInput("blank keeps saved key", true),
		focus:   "ai:base",
	}
	m.applyFocus()
	return m
}

func settingsInput(placeholder string, secret bool) textinput.Model {
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = placeholder
	input.SetWidth(48)
	if secret {
		input.EchoMode = textinput.EchoPassword
		input.EchoCharacter = '*'
	}
	return input
}

func (m *settingsModel) Init() tea.Cmd {
	return func() tea.Msg {
		aiSettings, err := m.store.AISettings(m.user)
		if err != nil {
			return settingsLoadedMsg{err: err}
		}
		sources, err := m.store.ForgeSources(m.user)
		return settingsLoadedMsg{ai: aiSettings, sources: sources, err: err}
	}
}

func (m *settingsModel) Close() {
	if m.testCancel != nil {
		m.testCancel()
		m.testCancel = nil
	}
	m.closed = true
}

func (m *settingsModel) Update(message tea.Msg) tea.Cmd {
	if m.closed {
		return nil
	}
	if pointer.IsMessage(message) {
		next, command, _ := m.pointerState.Update(message)
		m.pointerState = next
		return command
	}
	switch msg := message.(type) {
	case settingsLoadedMsg:
		m.finishLoad(msg)
		return nil
	case aiSettingsTestedMsg:
		if m.busy != "ai:test" {
			return nil
		}
		m.finishTest(msg.err, m.aiKey.Value())
		return nil
	case aiSettingsSavedMsg:
		if m.busy != "ai:save" {
			return nil
		}
		m.finishAISave(msg)
		return nil
	case forgeSettingsTestedMsg:
		if m.busy != "forge:test:"+msg.id {
			return nil
		}
		row := m.rowByID(msg.id)
		secret := ""
		if row != nil {
			secret = row.token.Value()
		}
		m.finishTest(msg.err, secret)
		return nil
	case forgeSettingsSavedMsg:
		if m.busy != "forge:save:"+msg.id {
			return nil
		}
		m.finishForgeSave(msg)
		return nil
	case forgeSettingsRemovedMsg:
		if m.busy != "forge:remove:"+msg.id {
			return nil
		}
		m.finishForgeRemove(msg)
		return nil
	case settingsPointerMsg:
		if msg.owner != m {
			return nil
		}
		return m.updatePointer(msg.target)
	case settingsWheelMsg:
		if m.loaded && m.busy == "" && msg.delta != 0 {
			m.moveFocus(msg.delta)
		}
		return nil
	case tea.KeyPressMsg:
		return m.updateKey(msg)
	}
	return nil
}

func (m *settingsModel) updatePointer(target string) tea.Cmd {
	if target == "close" {
		return m.updateKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	}
	if !m.loaded || m.busy != "" {
		return nil
	}
	if m.focus != target {
		m.disarmRemove()
	}
	m.focus = target
	m.applyFocus()
	if settingsPointerActivates(target) {
		return m.activateFocus()
	}
	return nil
}

func settingsPointerActivates(target string) bool {
	if target == "ai:test" || target == "ai:save" || target == "forge:add" {
		return true
	}
	return strings.HasSuffix(target, ":kind") || strings.HasSuffix(target, ":test") ||
		strings.HasSuffix(target, ":save") || strings.HasSuffix(target, ":remove")
}

func (m *settingsModel) finishLoad(msg settingsLoadedMsg) {
	if msg.err != nil {
		m.status = safeSettingsError(msg.err)
		m.statusIsError = true
		return
	}
	m.loaded = true
	m.aiBase.SetValue(msg.ai.BaseURL)
	m.aiModel.SetValue(msg.ai.Model)
	m.hasKey = msg.ai.HasKey
	m.rows = make([]integrationSettingsRow, 0, len(msg.sources))
	for _, source := range msg.sources {
		m.rows = append(m.rows, persistedIntegrationRow(source))
	}
	m.applyFocus()
}

func persistedIntegrationRow(source store.ForgeSource) integrationSettingsRow {
	row := integrationSettingsRow{
		id:        "source:" + source.Name,
		persisted: true,
		name:      settingsInput("source-name", false),
		kind:      source.Kind,
		baseURL:   settingsInput("forge.example.com", false),
		project:   settingsInput("owner/project (optional)", false),
		token:     settingsInput("blank keeps saved token", true),
		hasToken:  source.HasToken,
	}
	row.name.SetValue(source.Name)
	row.baseURL.SetValue(source.BaseURL)
	return row
}

func (m *settingsModel) updateKey(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	// The focused field's mark runs ahead of the pane's own keys, Escape
	// included: a marked field is typing context and its first Escape only
	// drops the mark.
	if m.loaded && m.busy == "" {
		if input := m.focusedInput(); input != nil && m.mark.Input(m.focus, input, msg) {
			return nil
		}
	}
	if key == "esc" {
		if strings.Contains(m.busy, ":test") && m.testCancel != nil {
			m.testCancel()
			m.testCancel = nil
			m.busy = ""
			m.status = "connection test cancelled"
			m.statusIsError = false
			return nil
		}
		if m.busy == "" {
			m.Close()
		}
		return nil
	}
	if !m.loaded || m.busy != "" {
		return nil
	}
	switch key {
	case "tab":
		m.moveFocus(1)
		return nil
	case "shift+tab":
		m.moveFocus(-1)
		return nil
	case "enter":
		return m.activateFocus()
	case "left", "right":
		if row, control := m.focusedRow(); row != nil && control == "kind" {
			m.disarmRemove()
			if row.kind == "gitlab" {
				row.kind = "github"
			} else {
				row.kind = "gitlab"
			}
			return nil
		}
	}
	m.disarmRemove()
	return m.updateFocusedInput(msg)
}

func (m *settingsModel) moveFocus(delta int) {
	m.disarmRemove()
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
	index = (index + delta + len(targets)) % len(targets)
	m.focus = targets[index]
	m.applyFocus()
}

func (m *settingsModel) focusTargets() []string {
	targets := []string{"ai:base", "ai:model", "ai:key", "ai:test", "ai:save"}
	for i := range m.rows {
		row := &m.rows[i]
		prefix := "forge:" + row.id + ":"
		if !row.persisted {
			targets = append(targets, prefix+"kind", prefix+"name")
		}
		targets = append(targets, prefix+"base", prefix+"project", prefix+"token", prefix+"test")
		targets = append(targets, prefix+"save", prefix+"remove")
	}
	return append(targets, "forge:add")
}

func (m *settingsModel) applyFocus() tea.Cmd {
	m.mark.Drop()
	m.aiBase.Blur()
	m.aiModel.Blur()
	m.aiKey.Blur()
	for i := range m.rows {
		m.rows[i].name.Blur()
		m.rows[i].baseURL.Blur()
		m.rows[i].project.Blur()
		m.rows[i].token.Blur()
	}
	switch m.focus {
	case "ai:base":
		return m.aiBase.Focus()
	case "ai:model":
		return m.aiModel.Focus()
	case "ai:key":
		return m.aiKey.Focus()
	}
	row, control := m.focusedRow()
	if row == nil {
		return nil
	}
	if row.persisted && control == "name" {
		return nil
	}
	switch control {
	case "name":
		return row.name.Focus()
	case "base":
		return row.baseURL.Focus()
	case "project":
		return row.project.Focus()
	case "token":
		return row.token.Focus()
	}
	return nil
}

// focusedInput is the text input the current focus names, or nil when focus
// sits on a control that is not one. It resolves the same targets
// updateFocusedInput dispatches to, so the select-all mark and the key dispatch
// never disagree about which field is being edited.
func (m *settingsModel) focusedInput() *textinput.Model {
	switch m.focus {
	case "ai:base":
		return &m.aiBase
	case "ai:model":
		return &m.aiModel
	case "ai:key":
		return &m.aiKey
	}
	row, control := m.focusedRow()
	if row == nil {
		return nil
	}
	switch control {
	case "name":
		if row.persisted {
			return nil
		}
		return &row.name
	case "base":
		return &row.baseURL
	case "project":
		return &row.project
	case "token":
		return &row.token
	}
	return nil
}

func (m *settingsModel) updateFocusedInput(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	switch m.focus {
	case "ai:base":
		m.aiBase, cmd = m.aiBase.Update(msg)
	case "ai:model":
		m.aiModel, cmd = m.aiModel.Update(msg)
	case "ai:key":
		m.aiKey, cmd = m.aiKey.Update(msg)
	default:
		row, control := m.focusedRow()
		if row == nil {
			return nil
		}
		if row.persisted && control == "name" {
			return nil
		}
		switch control {
		case "name":
			row.name, cmd = row.name.Update(msg)
		case "base":
			row.baseURL, cmd = row.baseURL.Update(msg)
		case "project":
			row.project, cmd = row.project.Update(msg)
		case "token":
			row.token, cmd = row.token.Update(msg)
		}
	}
	return cmd
}

func (m *settingsModel) focusedRow() (*integrationSettingsRow, string) {
	if !strings.HasPrefix(m.focus, "forge:") || m.focus == "forge:add" {
		return nil, ""
	}
	rest := strings.TrimPrefix(m.focus, "forge:")
	for i := range m.rows {
		prefix := m.rows[i].id + ":"
		if strings.HasPrefix(rest, prefix) {
			return &m.rows[i], strings.TrimPrefix(rest, prefix)
		}
	}
	return nil, ""
}

func (m *settingsModel) activateFocus() tea.Cmd {
	switch m.focus {
	case "ai:test":
		return m.startAITest()
	case "ai:save":
		return m.startAISave()
	case "forge:add":
		m.addForgeDraft()
		return nil
	}
	row, control := m.focusedRow()
	if row == nil {
		return nil
	}
	switch control {
	case "kind":
		if row.kind == "gitlab" {
			row.kind = "github"
		} else {
			row.kind = "gitlab"
		}
	case "test":
		return m.startForgeTest(row)
	case "save":
		return m.startForgeSave(row)
	case "remove":
		return m.startForgeRemove(row)
	}
	return nil
}

func (m *settingsModel) startAITest() tea.Cmd {
	m.disarmRemove()
	ctx, cancel := context.WithTimeout(m.ctx, settingsTestTimeout)
	m.testCancel = cancel
	m.busy = "ai:test"
	m.status = "testing AI connection..."
	m.statusIsError = false
	config := kbai.Config{
		BaseURL: strings.TrimSpace(m.aiBase.Value()),
		Model:   strings.TrimSpace(m.aiModel.Value()),
		Key:     m.aiKey.Value(),
	}
	return func() tea.Msg {
		defer cancel()
		return aiSettingsTestedMsg{err: m.ai.Probe(ctx, m.user, config)}
	}
}

func (m *settingsModel) finishTest(err error, secret string) {
	if m.testCancel != nil {
		m.testCancel()
		m.testCancel = nil
	}
	m.busy = ""
	m.statusIsError = err != nil
	if err == nil {
		m.status = "connection ok"
		return
	}
	m.status = safeSettingsError(err, secret)
}

func (m *settingsModel) startAISave() tea.Cmd {
	m.disarmRemove()
	base, model, key := optionalTrimmed(m.aiBase.Value()), optionalTrimmed(m.aiModel.Value()), optionalSecret(m.aiKey.Value())
	if base != nil {
		if err := kbai.ValidateBaseURL(*base); err != nil {
			m.status = safeSettingsError(err)
			m.statusIsError = true
			return nil
		}
	}
	m.busy = "ai:save"
	m.status = "saving AI settings..."
	m.statusIsError = false
	keySet := key != nil
	return func() tea.Msg {
		cleared, err := m.store.SetAISettings(m.user, base, model, key)
		return aiSettingsSavedMsg{keySet: keySet, keyCleared: cleared, err: err}
	}
}

func (m *settingsModel) finishAISave(msg aiSettingsSavedMsg) {
	m.busy = ""
	if msg.err != nil {
		m.status = safeSettingsError(msg.err, m.aiKey.Value())
		m.statusIsError = true
		return
	}
	m.hasKey = msg.keySet || (m.hasKey && !msg.keyCleared)
	m.aiKey.SetValue("")
	m.statusIsError = false
	if msg.keyCleared {
		m.status = "saved; endpoint changed, re-enter the API key"
	} else {
		m.status = "AI settings saved"
	}
}

func (m *settingsModel) addForgeDraft() {
	m.disarmRemove()
	m.nextDraft++
	id := fmt.Sprintf("draft:%d", m.nextDraft)
	m.rows = append(m.rows, integrationSettingsRow{
		id:      id,
		name:    settingsInput("work-gitlab", false),
		kind:    "gitlab",
		baseURL: settingsInput("gitlab.example.com", false),
		project: settingsInput("owner/project (optional)", false),
		token:   settingsInput("personal access token", true),
	})
	m.focus = "forge:" + id + ":name"
	m.applyFocus()
}

func (m *settingsModel) startForgeTest(row *integrationSettingsRow) tea.Cmd {
	m.disarmRemove()
	ctx, cancel := context.WithTimeout(m.ctx, settingsTestTimeout)
	m.testCancel = cancel
	m.busy = "forge:test:" + row.id
	m.status = "testing " + row.name.Value() + "..."
	m.statusIsError = false
	id := row.id
	config := forge.ForgeProbeConfig{
		Name: row.name.Value(), Kind: row.kind, BaseURL: row.baseURL.Value(),
		Project: row.project.Value(), Token: row.token.Value(), Saved: row.persisted,
	}
	return func() tea.Msg {
		defer cancel()
		err := m.forge.Probe(ctx, m.user, config)
		return forgeSettingsTestedMsg{id: id, err: err}
	}
}

func (m *settingsModel) startForgeSave(row *integrationSettingsRow) tea.Cmd {
	m.disarmRemove()
	name := strings.TrimSpace(row.name.Value())
	if name == "" {
		m.status, m.statusIsError = "integration name is required", true
		return nil
	}
	for i := range m.rows {
		other := &m.rows[i]
		if other.id != row.id && strings.EqualFold(strings.TrimSpace(other.name.Value()), name) {
			m.status, m.statusIsError = "integration name already exists", true
			return nil
		}
	}
	baseURL := optionalTrimmed(row.baseURL.Value())
	if !row.persisted && baseURL == nil {
		m.status, m.statusIsError = "forge base URL is required", true
		return nil
	}
	token := optionalSecret(row.token.Value())
	m.busy = "forge:save:" + row.id
	m.status = "saving " + name + "..."
	m.statusIsError = false
	id, kind := row.id, row.kind
	return func() tea.Msg {
		cleared, err := m.store.SetForgeSource(m.user, name, kind, baseURL, token)
		if err != nil {
			return forgeSettingsSavedMsg{id: id, tokenCleared: cleared, err: err}
		}
		sources, err := m.store.ForgeSources(m.user)
		if err != nil {
			return forgeSettingsSavedMsg{id: id, tokenCleared: cleared, err: err}
		}
		for _, source := range sources {
			if strings.EqualFold(source.Name, name) {
				return forgeSettingsSavedMsg{id: id, source: source, tokenCleared: cleared}
			}
		}
		return forgeSettingsSavedMsg{id: id, tokenCleared: cleared, err: errors.New("saved integration unavailable")}
	}
}

func (m *settingsModel) finishForgeSave(msg forgeSettingsSavedMsg) {
	m.busy = ""
	row := m.rowByID(msg.id)
	if row == nil {
		return
	}
	if msg.err != nil {
		m.status = safeSettingsError(msg.err, row.token.Value())
		m.statusIsError = true
		return
	}
	replacement := persistedIntegrationRow(msg.source)
	*row = replacement
	m.focus = "forge:" + replacement.id + ":save"
	m.applyFocus()
	m.statusIsError = false
	if msg.tokenCleared {
		m.status = "saved; endpoint changed, re-enter the token"
	} else {
		m.status = "integration saved"
	}
}

func (m *settingsModel) startForgeRemove(row *integrationSettingsRow) tea.Cmd {
	if m.armedRemove != row.id {
		m.armedRemove = row.id
		m.status = "press enter again to remove " + row.name.Value()
		m.statusIsError = false
		return nil
	}
	m.armedRemove = ""
	if !row.persisted {
		m.removeRow(row.id)
		m.status = "draft integration removed"
		m.focus = "forge:add"
		m.applyFocus()
		return nil
	}
	m.busy = "forge:remove:" + row.id
	m.status = "removing " + row.name.Value() + "..."
	id, name := row.id, row.name.Value()
	return func() tea.Msg {
		return forgeSettingsRemovedMsg{id: id, err: m.store.DeleteForgeSource(m.user, name)}
	}
}

func (m *settingsModel) finishForgeRemove(msg forgeSettingsRemovedMsg) {
	m.busy = ""
	row := m.rowByID(msg.id)
	if msg.err != nil {
		secret := ""
		if row != nil {
			secret = row.token.Value()
		}
		m.status = safeSettingsError(msg.err, secret)
		m.statusIsError = true
		return
	}
	m.removeRow(msg.id)
	m.focus = "forge:add"
	m.applyFocus()
	m.status = "integration removed"
	m.statusIsError = false
}

func (m *settingsModel) rowByID(id string) *integrationSettingsRow {
	for i := range m.rows {
		if m.rows[i].id == id {
			return &m.rows[i]
		}
	}
	return nil
}

func (m *settingsModel) removeRow(id string) {
	for i := range m.rows {
		if m.rows[i].id == id {
			m.rows = append(m.rows[:i], m.rows[i+1:]...)
			return
		}
	}
}

func (m *settingsModel) disarmRemove() { m.armedRemove = "" }

func optionalTrimmed(value string) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func optionalSecret(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func safeSettingsError(err error, secrets ...string) string {
	if err == nil {
		return ""
	}
	message := strings.TrimSpace(err.Error())
	for _, secret := range secrets {
		for _, candidate := range []string{secret, strings.TrimSpace(secret)} {
			if candidate != "" {
				message = strings.ReplaceAll(message, candidate, "[redacted]")
			}
		}
	}
	message = sanitizeTerminal(strings.Join(strings.Fields(message), " "))
	if message == "" {
		message = "operation failed"
	}
	if utf8.RuneCountInString(message) > 160 {
		message = string([]rune(message)[:160])
	}
	return message
}
