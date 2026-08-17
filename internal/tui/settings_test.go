package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	kbai "github.com/RandomCodeSpace/kb/internal/ai"
	"github.com/RandomCodeSpace/kb/internal/server"
	"github.com/RandomCodeSpace/kb/internal/store"
)

const (
	settingsStoredAIKey   = "stored-ai-secret"
	settingsStoredPAT     = "stored-forge-secret"
	settingsUnsavedSecret = "unsaved-secret"
)

type recordingAIProber struct {
	user        string
	config      kbai.Config
	hadDeadline bool
	err         error
}

func (p *recordingAIProber) Probe(ctx context.Context, user string, config kbai.Config) error {
	p.user = user
	p.config = config
	_, p.hadDeadline = ctx.Deadline()
	return p.err
}

type recordingForgeProber struct {
	user        string
	config      server.ForgeProbeConfig
	hadDeadline bool
	err         error
}

type faultSettingsStore struct {
	ai             store.AISettings
	sources        []store.ForgeSource
	aiErr          error
	forgeErr       error
	setAIErr       error
	setForgeErr    error
	deleteForgeErr error
}

func (s *faultSettingsStore) AISettings(string) (store.AISettings, error) {
	return s.ai, s.aiErr
}

func (s *faultSettingsStore) SetAISettings(string, *string, *string, *string) (bool, error) {
	return false, s.setAIErr
}

func (s *faultSettingsStore) ForgeSources(string) ([]store.ForgeSource, error) {
	return s.sources, s.forgeErr
}

func (s *faultSettingsStore) SetForgeSource(string, string, string, *string, *string) (bool, error) {
	return false, s.setForgeErr
}

func (s *faultSettingsStore) DeleteForgeSource(string, string) error {
	return s.deleteForgeErr
}

func (p *recordingForgeProber) Probe(ctx context.Context, user string, config server.ForgeProbeConfig) error {
	p.user, p.config = user, config
	_, p.hadDeadline = ctx.Deadline()
	return p.err
}

func newSettingsTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir()+"/kb.db", []byte("settings-test-store-key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func loadSettingsForTest(t *testing.T, model *settingsModel) {
	t.Helper()
	command := model.Init()
	if command == nil {
		t.Fatal("settings init command is nil")
	}
	model.Update(command())
	if !model.loaded {
		t.Fatalf("settings did not load: %s", model.status)
	}
}

func runSettingsCommand(t *testing.T, model *settingsModel, command tea.Cmd) {
	t.Helper()
	if command == nil {
		t.Fatal("settings command is nil")
	}
	model.Update(command())
}

func TestAISettingsUseUnsavedProbeAndStorePatchSemantics(t *testing.T) {
	st := newSettingsTestStore(t)
	storedBase, storedModel := "https://stored.example/v1", "stored-model"
	if _, err := st.SetAISettings("alice", &storedBase, &storedModel, stringPointer(settingsStoredAIKey)); err != nil {
		t.Fatal(err)
	}
	aiProbe := &recordingAIProber{}
	model := newSettingsModelWithBackends(st, aiProbe, &recordingForgeProber{}, "alice", context.Background())
	loadSettingsForTest(t, model)

	if view := model.View(100, 30); strings.Contains(view, settingsStoredAIKey) {
		t.Fatal("persisted AI key rendered in settings view")
	}
	model.aiBase.SetValue("https://candidate.example/v1")
	model.aiModel.SetValue("candidate-model")
	model.aiKey.SetValue(settingsUnsavedSecret)
	runSettingsCommand(t, model, model.startAITest())
	if aiProbe.user != "alice" || aiProbe.config != (kbai.Config{
		BaseURL: "https://candidate.example/v1",
		Model:   "candidate-model",
		Key:     settingsUnsavedSecret,
	}) || !aiProbe.hadDeadline {
		t.Fatalf("AI probe = user:%q config:%+v deadline:%v", aiProbe.user, aiProbe.config, aiProbe.hadDeadline)
	}
	if key, err := st.AIKey("alice"); err != nil || key != settingsStoredAIKey {
		t.Fatalf("unsaved probe changed stored key = %q, %v", key, err)
	}

	// Blank base URL and key are nil patches; the model changes and the saved
	// endpoint and credential remain intact.
	model.aiBase.SetValue("")
	model.aiModel.SetValue("candidate-model")
	model.aiKey.SetValue("")
	runSettingsCommand(t, model, model.startAISave())
	settings, err := st.AISettings("alice")
	if err != nil || settings.BaseURL != storedBase || settings.Model != "candidate-model" || !settings.HasKey {
		t.Fatalf("blank-preserving save = %+v, %v", settings, err)
	}
	if key, err := st.AIKey("alice"); err != nil || key != settingsStoredAIKey {
		t.Fatalf("blank key did not preserve stored key = %q, %v", key, err)
	}

	// A different origin with a blank key delegates to the store's atomic
	// credential-clearing contract.
	model.aiBase.SetValue("https://different.example/v1")
	model.aiModel.SetValue("")
	runSettingsCommand(t, model, model.startAISave())
	settings, err = st.AISettings("alice")
	if err != nil || settings.BaseURL != "https://different.example/v1" || settings.Model != "candidate-model" || settings.HasKey {
		t.Fatalf("origin-changing save = %+v, %v", settings, err)
	}
	if model.hasKey || !strings.Contains(model.status, "re-enter") {
		t.Fatalf("cleared-key UI state = hasKey:%v status:%q", model.hasKey, model.status)
	}
}

func TestSettingsNeverRenderSecretBearingErrorsAndCancelTests(t *testing.T) {
	st := newSettingsTestStore(t)
	aiProbe := &recordingAIProber{err: errors.New("upstream rejected " + settingsUnsavedSecret)}
	model := newSettingsModelWithBackends(st, aiProbe, &recordingForgeProber{}, "alice", context.Background())
	loadSettingsForTest(t, model)
	model.aiKey.SetValue(settingsUnsavedSecret)
	runSettingsCommand(t, model, model.startAITest())
	if view := model.View(80, 24); strings.Contains(view, settingsUnsavedSecret) || !strings.Contains(view, "[redacted]") {
		t.Fatalf("secret-bearing error was not redacted:\n%s", view)
	}

	model.aiKey.SetValue("another-secret")
	command := model.startAITest()
	if model.testCancel == nil {
		t.Fatal("AI test did not retain a cancellation handle")
	}
	model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if model.busy != "" || model.testCancel != nil || !strings.Contains(model.status, "cancelled") {
		t.Fatalf("cancel state = busy:%q cancel:%v status:%q", model.busy, model.testCancel != nil, model.status)
	}
	// The late result is ignored after cancellation.
	model.Update(command())
	if !strings.Contains(model.status, "cancelled") {
		t.Fatalf("late test result replaced cancellation: %q", model.status)
	}
}

func TestForgeSettingsTestSaveLockAndArmedRemoval(t *testing.T) {
	st := newSettingsTestStore(t)
	base := "https://forge.example"
	if _, err := st.SetForgeSource("alice", "primary", "gitlab", &base, stringPointer(settingsStoredPAT)); err != nil {
		t.Fatal(err)
	}
	forgeProbe := &recordingForgeProber{}
	model := newSettingsModelWithBackends(st, &recordingAIProber{}, forgeProbe, "alice", context.Background())
	loadSettingsForTest(t, model)
	row := model.rowByID("source:primary")
	if row == nil {
		t.Fatal("persisted forge row missing")
	}
	if view := model.View(100, 40); strings.Contains(view, settingsStoredPAT) {
		t.Fatal("persisted forge token rendered in settings view")
	}

	// Persisted name and kind have no focus targets and reject direct input too.
	for _, target := range model.focusTargets() {
		if target == "forge:source:primary:name" || target == "forge:source:primary:kind" {
			t.Fatalf("persisted immutable field remained focusable: %q", target)
		}
	}
	model.focus = "forge:source:primary:name"
	model.applyFocus()
	model.Update(tea.KeyPressMsg{Code: 'x'})
	if row.name.Value() != "primary" || row.kind != "gitlab" {
		t.Fatalf("persisted identity changed to %q/%q", row.name.Value(), row.kind)
	}

	row.baseURL.SetValue("https://unsaved.example")
	row.project.SetValue("group/project")
	row.token.SetValue(settingsUnsavedSecret)
	runSettingsCommand(t, model, model.startForgeTest(row))
	if forgeProbe.user != "alice" || forgeProbe.config != (server.ForgeProbeConfig{
		Name: "primary", Kind: "gitlab", BaseURL: "https://unsaved.example",
		Project: "group/project", Token: settingsUnsavedSecret, Saved: true,
	}) || !forgeProbe.hadDeadline {
		t.Fatalf("forge probe = %+v", forgeProbe)
	}
	if _, savedBase, savedPAT, err := st.ForgePAT("alice", "primary"); err != nil || savedBase != base || savedPAT != settingsStoredPAT {
		t.Fatalf("unsaved forge test changed store = %q/%q, %v", savedBase, savedPAT, err)
	}

	// Saving the changed origin with a blank token uses the store's clearing
	// behavior and leaves the row identity locked.
	row.token.SetValue("")
	runSettingsCommand(t, model, model.startForgeSave(row))
	if _, savedBase, savedPAT, err := st.ForgePAT("alice", "primary"); err != nil || savedBase != "https://unsaved.example" || savedPAT != "" {
		t.Fatalf("forge save = %q/%q, %v", savedBase, savedPAT, err)
	}
	if row.hasToken || !strings.Contains(model.status, "re-enter") {
		t.Fatalf("cleared-token UI state = hasToken:%v status:%q", row.hasToken, model.status)
	}

	model.focus = "forge:source:primary:remove"
	if command := model.activateFocus(); command != nil || model.armedRemove != row.id {
		t.Fatalf("first removal press = command:%v armed:%q", command, model.armedRemove)
	}
	model.moveFocus(1)
	if model.armedRemove != "" {
		t.Fatalf("navigation did not disarm removal: %q", model.armedRemove)
	}
	model.focus = "forge:source:primary:remove"
	if command := model.activateFocus(); command != nil {
		t.Fatal("re-arming removal returned a command")
	}
	runSettingsCommand(t, model, model.activateFocus())
	if sources, err := st.ForgeSources("alice"); err != nil || len(sources) != 0 || model.rowByID(row.id) != nil {
		t.Fatalf("confirmed removal = sources:%+v row:%v err:%v", sources, model.rowByID(row.id), err)
	}
}

func TestForgeDraftBecomesPersistedAndImmutable(t *testing.T) {
	st := newSettingsTestStore(t)
	model := newSettingsModelWithBackends(st, &recordingAIProber{}, &recordingForgeProber{}, "alice", context.Background())
	loadSettingsForTest(t, model)
	model.addForgeDraft()
	row := &model.rows[0]
	row.name.SetValue("Work-GitHub")
	row.kind = "github"
	row.baseURL.SetValue("github.example")
	row.token.SetValue(settingsUnsavedSecret)
	runSettingsCommand(t, model, model.startForgeSave(row))
	if !row.persisted || row.id != "source:work-github" || row.name.Value() != "work-github" ||
		row.kind != "github" || row.baseURL.Value() != "https://github.example" ||
		row.project.Value() != "" || row.token.Value() != "" || !row.hasToken {
		t.Fatalf("saved draft row = %+v", row)
	}
	if kind, savedBase, savedPAT, err := st.ForgePAT("alice", "work-github"); err != nil || kind != "github" || savedBase != "https://github.example" || savedPAT != settingsUnsavedSecret {
		t.Fatalf("saved draft store = %q/%q/%q, %v", kind, savedBase, savedPAT, err)
	}
	for _, target := range model.focusTargets() {
		if strings.HasSuffix(target, ":name") || strings.HasSuffix(target, ":kind") {
			t.Fatalf("saved draft identity remained editable: %q", target)
		}
	}
}

func TestForgeDraftTestsUnsavedValuesWithoutStoreMutation(t *testing.T) {
	st := newSettingsTestStore(t)
	forgeProbe := &recordingForgeProber{}
	model := newSettingsModelWithBackends(st, &recordingAIProber{}, forgeProbe, "alice", context.Background())
	loadSettingsForTest(t, model)
	model.addForgeDraft()
	row := &model.rows[0]
	row.name.SetValue("unsaved")
	row.kind = "github"
	row.baseURL.SetValue("https://candidate.example")
	row.project.SetValue("owner/project")
	row.token.SetValue(settingsUnsavedSecret)

	if !slicesContains(model.focusTargets(), "forge:"+row.id+":test") {
		t.Fatal("draft test action is not focusable")
	}
	runSettingsCommand(t, model, model.startForgeTest(row))
	want := server.ForgeProbeConfig{
		Name: "unsaved", Kind: "github", BaseURL: "https://candidate.example",
		Project: "owner/project", Token: settingsUnsavedSecret,
	}
	if forgeProbe.user != "alice" || forgeProbe.config != want || !forgeProbe.hadDeadline {
		t.Fatalf("draft forge probe = user:%q config:%+v deadline:%v", forgeProbe.user, forgeProbe.config, forgeProbe.hadDeadline)
	}
	if sources, err := st.ForgeSources("alice"); err != nil || len(sources) != 0 {
		t.Fatalf("draft probe mutated store: sources=%+v err=%v", sources, err)
	}
}

func slicesContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestSettingsViewportKeepsEveryFocusedControlVisible(t *testing.T) {
	sources := make([]store.ForgeSource, 10)
	for i := range sources {
		sources[i] = store.ForgeSource{
			Name: fmt.Sprintf("source-%02d", i), Kind: "gitlab", BaseURL: "https://forge.example",
		}
	}
	backend := &faultSettingsStore{
		ai: store.AISettings{BaseURL: "https://api.example", Model: "model"}, sources: sources,
	}
	model := newSettingsModelWithBackends(backend, &recordingAIProber{}, &recordingForgeProber{}, "alice", context.Background())
	loadSettingsForTest(t, model)

	for _, target := range model.focusTargets() {
		model.focus = target
		model.applyFocus()
		view := model.View(42, 7)
		if !strings.Contains("\n"+view, "\n>") {
			t.Fatalf("focused control %q is outside viewport:\n%s", target, view)
		}
		lines := strings.Split(view, "\n")
		if len(lines) > 7 {
			t.Fatalf("viewport height for %q = %d", target, len(lines))
		}
		for _, line := range lines {
			if ansi.StringWidth(line) > 42 {
				t.Fatalf("viewport width for %q = %d: %q", target, ansi.StringWidth(line), line)
			}
		}
	}
	if model.scroll == 0 {
		t.Fatal("long settings pane never scrolled")
	}
	model.focus = "ai:base"
	model.applyFocus()
	if view := model.View(42, 7); !strings.Contains(view, "> Base URL") {
		t.Fatalf("viewport did not scroll back to AI focus:\n%s", view)
	}
}

func TestSettingsInputCursorAndHorizontalViewport(t *testing.T) {
	input := settingsInput("placeholder", false)
	input.SetValue("0123456789abcdef")
	input.Focus()
	for _, test := range []struct {
		name     string
		position int
		want     string
	}{
		{name: "start", position: 0, want: "|012345678"},
		{name: "middle", position: 5, want: "01234|5678"},
		{name: "end", position: 16, want: "789abcdef|"},
	} {
		t.Run(test.name, func(t *testing.T) {
			input.SetCursor(test.position)
			if got := settingsInputDisplay(input, false, true, 10); got != test.want {
				t.Fatalf("cursor projection at %d = %q, want %q", test.position, got, test.want)
			}
		})
	}

	input.CursorEnd()
	end := settingsInputDisplay(input, false, true, 10)
	input, _ = input.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	left := settingsInputDisplay(input, false, true, 10)
	input, _ = input.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	right := settingsInputDisplay(input, false, true, 10)
	if end != "789abcdef|" || left != "6789abcde|" || right != end {
		t.Fatalf("horizontal cursor tracking = end:%q left:%q right:%q", end, left, right)
	}
	for _, view := range []string{end, left, right} {
		if ansi.StringWidth(view) != 10 || !strings.Contains(view, "|") {
			t.Fatalf("cursor viewport width/marker = %d/%q", ansi.StringWidth(view), view)
		}
	}
}

func TestSettingsViewStripsTerminalControlsWithoutChangingValues(t *testing.T) {
	hostile := "safe\x1b[31m-red\x1b[0m\x1b]2;owned\x07\x00\x9b31m"
	secret := "token\x1b]52;c;stolen\x07\x9b2J"
	backend := &faultSettingsStore{
		ai:      store.AISettings{BaseURL: hostile, Model: hostile, HasKey: true},
		sources: []store.ForgeSource{{Name: hostile, Kind: hostile, BaseURL: hostile, HasToken: true}},
	}
	aiProbe := &recordingAIProber{}
	forgeProbe := &recordingForgeProber{}
	model := newSettingsModelWithBackends(backend, aiProbe, forgeProbe, hostile, context.Background())
	loadSettingsForTest(t, model)
	row := &model.rows[0]
	model.aiKey.SetValue(secret)
	row.project.SetValue(hostile)
	row.token.SetValue(secret)
	model.status = hostile
	beforeAIBase, beforeAIKey := model.aiBase.Value(), model.aiKey.Value()
	beforeName, beforeKind := row.name.Value(), row.kind
	beforeBase, beforeProject, beforeToken := row.baseURL.Value(), row.project.Value(), row.token.Value()

	view := model.View(100, 30)
	for _, r := range view {
		if r == '\n' {
			continue
		}
		if r <= 0x1f || (r >= 0x7f && r <= 0x9f) {
			t.Fatalf("view contains terminal control U+%04X: %q", r, view)
		}
	}
	if strings.Contains(view, "\x1b") || strings.Contains(view, secret) {
		t.Fatalf("view contains escape or raw secret: %q", view)
	}
	if model.aiBase.Value() != beforeAIBase || model.aiKey.Value() != beforeAIKey ||
		row.name.Value() != beforeName || row.kind != beforeKind || row.baseURL.Value() != beforeBase ||
		row.project.Value() != beforeProject || row.token.Value() != beforeToken {
		t.Fatalf("render sanitization changed values: aiBase=%q aiKey=%q name=%q kind=%q base=%q project=%q token=%q",
			model.aiBase.Value(), model.aiKey.Value(), row.name.Value(), row.kind,
			row.baseURL.Value(), row.project.Value(), row.token.Value())
	}

	runSettingsCommand(t, model, model.startAITest())
	if aiProbe.config.BaseURL != beforeAIBase || aiProbe.config.Model != model.aiModel.Value() || aiProbe.config.Key != beforeAIKey {
		t.Fatalf("AI probe did not receive underlying values: %+v", aiProbe.config)
	}
	runSettingsCommand(t, model, model.startForgeTest(row))
	if forgeProbe.config.Name != beforeName || forgeProbe.config.Kind != beforeKind ||
		forgeProbe.config.BaseURL != beforeBase || forgeProbe.config.Project != beforeProject ||
		forgeProbe.config.Token != beforeToken {
		t.Fatalf("forge probe did not receive underlying values: %+v", forgeProbe.config)
	}
}

func TestRootRoutesSettingsWithoutStoppingBoardPolling(t *testing.T) {
	st := newSettingsTestStore(t)
	direct := newSettingsModel(st, "alice", context.Background())
	if direct.store != st || direct.ai == nil || direct.forge == nil {
		t.Fatal("production settings constructor did not wire direct backends")
	}
	settings := newSettingsModelWithBackends(st, &recordingAIProber{}, &recordingForgeProber{}, "alice", context.Background())
	root := NewModel(st, stubVersionReader{version: 2}, "alice")
	root.settingsNew = func() *settingsModel { return settings }
	command := updateTestModel(t, &root, tea.KeyPressMsg{Code: 's'})
	if root.settings == nil || command == nil {
		t.Fatal("s did not open and load settings")
	}
	updateTestModel(t, &root, command())
	if !strings.Contains(root.View().Content, "AI SETTINGS") {
		t.Fatalf("root did not render settings:\n%s", root.View().Content)
	}
	if poll := updateTestModel(t, &root, pollTickMsg{}); poll == nil {
		t.Fatal("settings pane stopped the board poll chain")
	}
	updateTestModel(t, &root, tea.KeyPressMsg{Code: tea.KeyEscape})
	if root.settings != nil || !strings.Contains(root.View().Content, "kb / Board / alice") {
		t.Fatal("escape did not return to board")
	}
}

func TestRootSettingsQuitAndClosedMessageRouting(t *testing.T) {
	st := newSettingsTestStore(t)
	settings := newSettingsModelWithBackends(st, &recordingAIProber{}, &recordingForgeProber{}, "alice", context.Background())
	loadSettingsForTest(t, settings)
	if command := settings.startAITest(); command == nil || settings.testCancel == nil {
		t.Fatal("settings test did not retain cancellable work")
	}
	root := NewModel(st, stubVersionReader{version: 1}, "alice")
	root.settings = settings
	root.reloadPending = true
	quit := updateTestModel(t, &root, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if quit == nil || !root.stopped || root.reloadPending || !settings.closed || settings.testCancel != nil {
		t.Fatalf("ctrl+c settings quit = command:%v stopped:%v pending:%v closed:%v cancel:%v",
			quit, root.stopped, root.reloadPending, settings.closed, settings.testCancel != nil)
	}

	closed := newSettingsModelWithBackends(st, &recordingAIProber{}, &recordingForgeProber{}, "alice", context.Background())
	closed.closed = true
	root = NewModel(st, stubVersionReader{version: 1}, "alice")
	root.settings = closed
	if command := updateTestModel(t, &root, settingsLoadedMsg{}); command != nil || root.settings != nil {
		t.Fatalf("closed settings message = command:%v settings:%v", command, root.settings)
	}
}

func TestSettingsPaneGolden(t *testing.T) {
	st := newSettingsTestStore(t)
	base, modelName := "https://api.example/v1", "gpt-example"
	if _, err := st.SetAISettings("alice", &base, &modelName, stringPointer(settingsStoredAIKey)); err != nil {
		t.Fatal(err)
	}
	forgeBase := "https://gitlab.example"
	if _, err := st.SetForgeSource("alice", "work", "gitlab", &forgeBase, stringPointer(settingsStoredPAT)); err != nil {
		t.Fatal(err)
	}
	model := newSettingsModelWithBackends(st, &recordingAIProber{}, &recordingForgeProber{}, "alice", context.Background())
	loadSettingsForTest(t, model)
	model.focus = "ai:test"
	model.applyFocus()
	output := model.View(80, 30)
	for _, secret := range []string{settingsStoredAIKey, settingsStoredPAT} {
		if strings.Contains(output, secret) {
			t.Fatalf("golden output contains persisted secret %q", secret)
		}
	}
	golden.RequireEqual(t, output)
}

func TestSettingsKeyboardAndFailureStateBranches(t *testing.T) {
	loadFailure := errors.New("load failed")
	broken := &faultSettingsStore{aiErr: loadFailure}
	model := newSettingsModelWithBackends(broken, &recordingAIProber{}, &recordingForgeProber{}, "alice", nil)
	model.Update(model.Init()())
	if model.loaded || !model.statusIsError || !strings.Contains(model.View(30, 6), "load failed") {
		t.Fatalf("AI load failure = loaded:%v error:%v status:%q", model.loaded, model.statusIsError, model.status)
	}
	model.Close() // no active cancellation is also a supported close path.

	broken = &faultSettingsStore{
		ai:       store.AISettings{BaseURL: "https://api.example", Model: "m"},
		forgeErr: loadFailure,
	}
	model = newSettingsModelWithBackends(broken, &recordingAIProber{}, &recordingForgeProber{}, "alice", context.Background())
	model.Update(model.Init()())
	if model.loaded || !strings.Contains(model.status, "load failed") {
		t.Fatalf("forge load failure = loaded:%v status:%q", model.loaded, model.status)
	}

	broken = &faultSettingsStore{
		ai: store.AISettings{BaseURL: "https://api.example", Model: "m"},
		sources: []store.ForgeSource{{
			Name: "saved", Kind: "gitlab", BaseURL: "https://forge.example",
		}},
	}
	aiProbe := &recordingAIProber{}
	forgeProbe := &recordingForgeProber{}
	model = newSettingsModelWithBackends(broken, aiProbe, forgeProbe, "alice", context.Background())
	loadSettingsForTest(t, model)

	// Every editable input is routed through the focused text input. The saved
	// row's name is deliberately absent: it is immutable after save.
	for _, target := range []string{"ai:base", "ai:model", "ai:key", "forge:source:saved:base", "forge:source:saved:project", "forge:source:saved:token"} {
		model.focus = target
		model.applyFocus()
		model.Update(tea.KeyPressMsg(tea.Key{Code: 'z', Text: "z"}))
	}
	if !strings.HasSuffix(model.aiBase.Value(), "z") || !strings.HasSuffix(model.aiModel.Value(), "z") ||
		model.aiKey.Value() != "z" || !strings.HasSuffix(model.rows[0].baseURL.Value(), "z") ||
		model.rows[0].project.Value() != "z" || model.rows[0].token.Value() != "z" {
		t.Fatalf("focused input routing failed: base=%q model=%q key=%q forge=%q project=%q token=%q",
			model.aiBase.Value(), model.aiModel.Value(), model.aiKey.Value(), model.rows[0].baseURL.Value(),
			model.rows[0].project.Value(), model.rows[0].token.Value())
	}

	model.focus = "forge:add"
	model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	draft := &model.rows[1]
	model.focus = "forge:" + draft.id + ":kind"
	model.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	if draft.kind != "github" {
		t.Fatalf("left did not toggle kind: %q", draft.kind)
	}
	model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if draft.kind != "gitlab" {
		t.Fatalf("enter did not toggle kind: %q", draft.kind)
	}
	model.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	if draft.kind != "github" {
		t.Fatalf("right did not toggle kind: %q", draft.kind)
	}
	for _, target := range []string{"forge:" + draft.id + ":name", "forge:" + draft.id + ":base", "forge:" + draft.id + ":project", "forge:" + draft.id + ":token"} {
		model.focus = target
		model.applyFocus()
		model.Update(tea.KeyPressMsg(tea.Key{Code: 'x', Text: "x"}))
	}
	if draft.name.Value() != "x" || draft.baseURL.Value() != "x" || draft.project.Value() != "x" || draft.token.Value() != "x" {
		t.Fatalf("draft input routing = %q/%q/%q/%q", draft.name.Value(), draft.baseURL.Value(), draft.project.Value(), draft.token.Value())
	}

	model.focus = "ai:test"
	runSettingsCommand(t, model, model.activateFocus())
	model.focus = "forge:source:saved:test"
	runSettingsCommand(t, model, model.activateFocus())
	if aiProbe.user != "alice" || forgeProbe.config.Name != "saved" {
		t.Fatalf("action routing missed probes: AI=%q forge=%q", aiProbe.user, forgeProbe.config.Name)
	}

	// Navigation wraps and both loaded and busy states ignore unrelated input.
	model.focus = "ai:base"
	model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}))
	if model.focus != "forge:add" {
		t.Fatalf("reverse focus wrap = %q", model.focus)
	}
	model.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if model.focus != "ai:base" {
		t.Fatalf("forward focus wrap = %q", model.focus)
	}
	model.busy = "ai:save"
	before := model.aiBase.Value()
	model.Update(tea.KeyPressMsg{Code: 'q'})
	if model.aiBase.Value() != before || model.closed {
		t.Fatal("busy settings accepted input or closed")
	}
	model.busy = ""
	model.focus = "not:a:target"
	model.applyFocus()
	model.Update(tea.KeyPressMsg{Code: 'x'})
	model.Update(struct{}{})
	_ = model.View(1, 6)
}

func TestSettingsSaveAndRemoveFailuresRemainRecoverable(t *testing.T) {
	broken := &faultSettingsStore{
		ai:          store.AISettings{BaseURL: "https://api.example", Model: "m"},
		setAIErr:    errors.New("AI write failed"),
		setForgeErr: errors.New("forge write failed"),
		sources: []store.ForgeSource{
			{Name: "one", Kind: "gitlab", BaseURL: "https://one.example"},
			{Name: "two", Kind: "github", BaseURL: "https://two.example"},
		},
	}
	model := newSettingsModelWithBackends(broken, &recordingAIProber{}, &recordingForgeProber{}, "alice", context.Background())
	loadSettingsForTest(t, model)

	model.aiBase.SetValue("not a URL")
	if command := model.startAISave(); command != nil || !model.statusIsError {
		t.Fatalf("invalid AI URL = command:%v status:%q", command, model.status)
	}
	model.aiBase.SetValue("https://api.example")
	model.aiKey.SetValue(settingsUnsavedSecret)
	runSettingsCommand(t, model, model.startAISave())
	if !model.statusIsError || !strings.Contains(model.status, "AI write failed") || model.aiKey.Value() == "" {
		t.Fatalf("AI save failure = error:%v status:%q key-cleared:%v", model.statusIsError, model.status, model.aiKey.Value() == "")
	}

	model.addForgeDraft()
	draft := &model.rows[len(model.rows)-1]
	if command := model.startForgeSave(draft); command != nil || !strings.Contains(model.status, "name") {
		t.Fatalf("missing forge name = command:%v status:%q", command, model.status)
	}
	draft.name.SetValue("one")
	draft.baseURL.SetValue("https://draft.example")
	if command := model.startForgeSave(draft); command != nil || !strings.Contains(model.status, "exists") {
		t.Fatalf("duplicate forge name = command:%v status:%q", command, model.status)
	}
	draft.name.SetValue("draft")
	draft.baseURL.SetValue("")
	if command := model.startForgeSave(draft); command != nil || !strings.Contains(model.status, "base URL") {
		t.Fatalf("missing forge base = command:%v status:%q", command, model.status)
	}
	draft.baseURL.SetValue("https://draft.example")
	draft.token.SetValue(settingsUnsavedSecret)
	runSettingsCommand(t, model, model.startForgeSave(draft))
	if !model.statusIsError || !strings.Contains(model.status, "forge write failed") || draft.token.Value() == "" {
		t.Fatalf("forge save failure = error:%v status:%q token-cleared:%v", model.statusIsError, model.status, draft.token.Value() == "")
	}

	row := model.rowByID("source:one")
	rowID := row.id
	broken.deleteForgeErr = errors.New("delete failed")
	model.armedRemove = row.id
	runSettingsCommand(t, model, model.startForgeRemove(row))
	if model.rowByID(rowID) == nil || !model.statusIsError || !strings.Contains(model.status, "delete failed") {
		t.Fatalf("remove failure lost row or status: row=%v status=%q", model.rowByID(rowID), model.status)
	}
	broken.deleteForgeErr = nil
	row = model.rowByID(rowID)
	model.armedRemove = rowID
	runSettingsCommand(t, model, model.startForgeRemove(row))
	if model.rowByID(rowID) != nil {
		t.Fatalf("successful retry retained row: busy=%q status=%q armed=%q", model.busy, model.status, model.armedRemove)
	}

	// Matching late messages for rows already removed are harmless.
	model.busy = "forge:save:missing"
	model.Update(forgeSettingsSavedMsg{id: "missing"})
	model.busy = "forge:remove:missing"
	model.Update(forgeSettingsRemovedMsg{id: "missing", err: errors.New("late failure")})
	if valueOrEmpty(nil) != "" || safeSettingsError(errors.New("   ")) != "operation failed" {
		t.Fatal("empty helper fallbacks changed")
	}
}

func stringPointer(value string) *string { return &value }

func TestSafeSettingsErrorBoundsAndNormalizes(t *testing.T) {
	message := settingsUnsavedSecret + "\n" + strings.Repeat("x", 300)
	got := safeSettingsError(errors.New(message), settingsUnsavedSecret)
	if strings.Contains(got, settingsUnsavedSecret) || strings.ContainsAny(got, "\r\n") || len([]rune(got)) > 160 {
		t.Fatalf("unsafe bounded error = %q", got)
	}
	if got := safeSettingsError(nil); got != "" {
		t.Fatalf("nil error = %q", got)
	}
	if settingsTestTimeout <= 0 {
		t.Fatal("test timeout must be positive")
	}
}
