package tui

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/board"
)

func filterFixture() board.Board {
	return board.Board{Title: "Filter", Tasks: []board.Task{
		{ID: "login", Title: "Fix login timeout", Desc: "auth token expires", Status: board.StatusTodo, Tags: []string{"bug", "auth"}},
		{ID: "landing", Title: "Design landing page", Status: board.StatusTodo, Tags: []string{"ui"}},
		{ID: "rotate", Title: "Rotate keys", Desc: "quarterly", Status: board.StatusDoing, Tags: []string{"auth", "env::prod"}},
		{ID: "billing", Title: "Fix billing", Status: board.StatusTodo, Tags: []string{"bug"}},
		{ID: "cancelled", Title: "Retired login", Status: board.StatusCancelled, Tags: []string{"bug"}},
	}}
}

func TestFilterMatchesWebSemantics(t *testing.T) {
	current := filterFixture()
	for _, test := range []struct {
		name   string
		filter boardFilter
		want   []string
	}{
		{"inactive", boardFilter{}, []string{"login", "landing", "rotate", "billing", "cancelled"}},
		{"title case insensitive", boardFilter{Text: "LOGIN"}, []string{"login", "cancelled"}},
		{"description substring", boardFilter{Text: "TOKEN"}, []string{"login"}},
		{"tag substring", boardFilter{Text: "PROD"}, []string{"rotate"}},
		{"exact tag", boardFilter{Tags: []string{"env::prod"}}, []string{"rotate"}},
		{"not partial tag", boardFilter{Tags: []string{"prod"}}, []string{}},
		{"tag case sensitive", boardFilter{Tags: []string{"BUG"}}, []string{}},
		{"AND tags", boardFilter{Tags: []string{"bug", "auth"}}, []string{"login"}},
		{"text and tags", boardFilter{Text: "quarter", Tags: []string{"auth"}}, []string{"rotate"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := newBoardFilterState()
			state.restore(test.filter)
			gotBoard := state.project(current)
			got := make([]string, len(gotBoard.Tasks))
			for i, task := range gotBoard.Tasks {
				got[i] = task.ID
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("filtered ids = %v, want %v", got, test.want)
			}
		})
	}
}

func TestWebLowerMatchesFrozenJavaScriptVectors(t *testing.T) {
	// Expected values were captured from Node 24 String.prototype.toLowerCase.
	for _, test := range []struct {
		input string
		want  string
	}{
		{"İ", "i\u0307"},
		{"Iİ", "ii\u0307"},
		{"ΟΣ", "ος"},
		{"Σ", "σ"},
		{"ẞ", "ß"},
	} {
		if got := webLower(test.input); got != test.want {
			t.Errorf("webLower(%q) = %q, want %q", test.input, got, test.want)
		}
	}
	state := newBoardFilterState()
	state.restore(boardFilter{Text: "İ"})
	if state.matches(board.Task{Title: "i"}) {
		t.Fatal("U+0130 query matched plain i unlike JavaScript")
	}
	if !state.matches(board.Task{Title: "i\u0307"}) {
		t.Fatal("U+0130 query did not match JavaScript expanded lowercase")
	}
}

func TestFilterKeyboardRoutingPersistenceAndClear(t *testing.T) {
	m := NewModel(stubBoardReader{}, nil, "alice")
	m.loading = false
	m.board = filterFixture()
	var saved []tuiPreferences
	m.savePreferences = func(preferences tuiPreferences) error {
		saved = append(saved, preferences)
		return nil
	}

	focus := updateTestModel(t, &m, tea.KeyPressMsg{Code: '/'})
	if m.filter.focus != filterText || focus == nil {
		t.Fatalf("slash focus = %v command=%v", m.filter.focus, focus)
	}
	updateTestModel(t, &m, focus())
	save := updateTestModel(t, &m, tea.KeyPressMsg(tea.Key{Code: 'q', Text: "q"}))
	if m.stopped || m.filter.input.Value() != "q" || save == nil {
		t.Fatalf("typed q = stopped:%v value:%q command:%v", m.stopped, m.filter.input.Value(), save)
	}
	finishPreferenceCommand(t, &m, save)
	updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.filter.focus != filterUnfocused {
		t.Fatalf("escape focus = %v", m.filter.focus)
	}

	updateTestModel(t, &m, tea.KeyPressMsg{Code: 'f'})
	if m.filter.focus != filterLabels || m.filterLabels()[m.filter.labelIndex] != "auth" {
		t.Fatalf("label focus = %v index=%d labels=%v", m.filter.focus, m.filter.labelIndex, m.filterLabels())
	}
	toggle := updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if toggle == nil || !reflect.DeepEqual(m.filter.tags, []string{"auth"}) {
		t.Fatalf("keyboard tag toggle = %v command=%v", m.filter.tags, toggle)
	}
	finishPreferenceCommand(t, &m, toggle)
	m.boardView.showCancelled = true
	clear := updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if clear != nil {
		t.Fatalf("escape returned %v", clear)
	}
	clear = updateTestModel(t, &m, tea.KeyPressMsg{Code: 'X', Text: "X"})
	if clear == nil || m.filter.active() || !m.boardView.showCancelled {
		t.Fatalf("clear = filter:%+v cancelled:%v command:%v", m.filter.value(), m.boardView.showCancelled, clear)
	}
	finishPreferenceCommand(t, &m, clear)
	if len(saved) != 3 || saved[2].ShowCancelled != true || saved[2].Filter.Text != "" || len(saved[2].Filter.Tags) != 0 {
		t.Fatalf("saved snapshots = %+v", saved)
	}
}

func TestFilterClearDoesNotShadowCancelCard(t *testing.T) {
	m := NewModel(stubBoardReader{}, nil, "alice")
	m.loading = false
	m.board = filterFixture()
	m.filter.restore(boardFilter{Text: "login"})
	m.boardView.normalizeSelection(m.filteredBoard())
	m.savePreferences = func(tuiPreferences) error { return nil }

	updateTestModel(t, &m, tea.KeyPressMsg{Code: 'x', Text: "x"})
	if !m.action.open() || !m.filter.active() {
		t.Fatalf("lowercase x = action:%#v filter:%+v", m.action, m.filter.value())
	}
	m.action.close()

	clear := updateTestModel(t, &m, tea.KeyPressMsg{Code: 'X', Text: "X"})
	if clear == nil || m.action.open() || m.filter.active() {
		t.Fatalf("uppercase X = action:%#v filter:%+v command:%v", m.action, m.filter.value(), clear)
	}
}

func TestFilterLabelFocusUsesDistinctCancelAndClearKeys(t *testing.T) {
	m := NewModel(stubBoardReader{}, nil, "alice")
	m.loading = false
	m.board = filterFixture()
	m.filter.restore(boardFilter{Tags: []string{"bug"}})
	m.filter.focus = filterLabels
	m.savePreferences = func(tuiPreferences) error { return nil }

	if handled, command := m.handleFilterKey(tea.KeyPressMsg{Code: 'x', Text: "x"}); handled || command != nil {
		t.Fatalf("lowercase x stayed in label filter: handled=%v command=%v", handled, command)
	}
	if handled, command := m.handleFilterKey(tea.KeyPressMsg{Code: 'X', Text: "X"}); !handled || command == nil || m.filter.active() {
		t.Fatalf("uppercase X did not clear label filter: handled=%v command=%v filter=%+v", handled, command, m.filter.value())
	}

	m.filter.focus = filterLabels
	if handled, command := m.handleFilterKey(tea.KeyPressMsg{Code: 'X', Text: "X"}); !handled || command != nil {
		t.Fatalf("inactive uppercase X = handled:%v command:%v", handled, command)
	}
}

func finishPreferenceCommand(t *testing.T, model *Model, command tea.Cmd) {
	t.Helper()
	message := command()
	if batch, ok := message.(tea.BatchMsg); ok {
		for _, nested := range batch {
			if saved, ok := nested().(preferenceSavedMsg); ok {
				updateTestModel(t, model, saved)
				return
			}
		}
		t.Fatal("batch did not contain a preference save")
	}
	if _, ok := message.(preferenceSavedMsg); !ok {
		t.Fatalf("preference command returned %T", message)
	}
	updateTestModel(t, model, message)
}

func TestFilteredNavigationMouseAndDetailUseVisibleTasks(t *testing.T) {
	m := NewModel(stubDetailBoardReader{stubBoardReader{board: filterFixture()}}, nil, "alice")
	completeBoardLoad(t, &m, m.Init())
	m.width, m.height = 160, 22
	m.filter.restore(boardFilter{Text: "fix", Tags: []string{"bug"}})
	m.boardView.rows[0] = 1
	if selected, ok := m.selectedTask(); !ok || selected.ID != "billing" {
		t.Fatalf("filtered selection = %+v,%v", selected, ok)
	}
	if command := updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEnter}); command == nil || !m.detail.IsOpen() || m.detail.TaskID() != "billing" {
		t.Fatalf("filtered detail = open:%v task:%q command:%v", m.detail.IsOpen(), m.detail.TaskID(), command)
	}
	before := m.filter.value()
	for _, message := range []tea.Msg{filterTextClickedMsg{}, filterLabelClickedMsg{tag: "ui"}, filterClearClickedMsg{}} {
		if command := updateTestModel(t, &m, message); command != nil || !reflect.DeepEqual(m.filter.value(), before) {
			t.Fatalf("%T leaked behind detail: filter=%+v command=%v", message, m.filter.value(), command)
		}
	}
	m.detail.Close()

	m.filter.restore(boardFilter{})
	_, hits := m.renderBoard()
	var labelHit boardHit
	for _, hit := range hits {
		if hit.kind == boardHitFilterLabel && hit.tag == "bug" && hit.y0 > 1 {
			labelHit = hit
			break
		}
	}
	if labelHit.x1 <= labelHit.x0 {
		t.Fatalf("card label hit missing: %+v", hits)
	}
	command := boardMouseHandler(hits)(tea.MouseClickMsg{X: labelHit.x0, Y: labelHit.y0, Button: tea.MouseLeft})
	if command == nil {
		t.Fatal("card label click was not hit")
	}
	updateTestModel(t, &m, command())
	if m.detail.IsOpen() || !reflect.DeepEqual(m.filter.tags, []string{"bug"}) {
		t.Fatalf("label click = detail:%v filter:%v", m.detail.IsOpen(), m.filter.tags)
	}
}

func TestFilterMouseMessagesDoNotLeakBehindSettings(t *testing.T) {
	m := NewModel(stubBoardReader{}, nil, "u")
	m.board = filterFixture()
	m.filter.restore(boardFilter{Text: "fix", Tags: []string{"bug"}})
	m.settings = &settingsModel{}
	before := m.filter.value()
	for _, message := range []tea.Msg{filterTextClickedMsg{}, filterLabelClickedMsg{tag: "ui"}, filterClearClickedMsg{}} {
		if command := updateTestModel(t, &m, message); command != nil || !reflect.DeepEqual(m.filter.value(), before) {
			t.Fatalf("%T leaked behind settings: filter=%+v command=%v", message, m.filter.value(), command)
		}
	}
}

func TestFilterCountAndNarrowLayout(t *testing.T) {
	m := NewModel(stubBoardReader{}, nil, "u")
	m.loading = false
	m.board = filterFixture()
	m.filter.restore(boardFilter{Text: "login"})
	wide := ansi.Strip(m.render())
	if !strings.Contains(wide, "2 of 5 cards") || strings.Contains(wide, "Design landing page") {
		t.Fatalf("filtered count/view:\n%s", wide)
	}
	m.filter.input.SetValue(strings.Repeat("long-query-", 8))
	m.filter.focusText()
	m.width = 23
	countLine, _ := m.renderFilterBar(m.width)
	countLines := strings.Split(ansi.Strip(countLine), "\n")
	if len(countLines) != 2 || !strings.HasPrefix(countLines[1], "0 of 5 cards") {
		t.Fatalf("narrow active count was not prioritized: %q", ansi.Strip(countLine))
	}
	if !strings.HasPrefix(countLines[0], "> ") {
		t.Fatalf("narrow active text focus was not visible: %q", ansi.Strip(countLine))
	}
	m.filter.restore(boardFilter{})
	m.filter.focus = filterLabels
	m.filter.labelIndex = len(m.filterLabels()) - 1
	m.width = 16
	focusedLine, _ := m.renderFilterBar(m.width)
	if !strings.HasPrefix(ansi.Strip(focusedLine), ">[+ ui]<") {
		t.Fatalf("focused label is outside the viewport: %q", ansi.Strip(focusedLine))
	}
	m.filter.restore(boardFilter{Text: "login"})
	for _, width := range []int{1, 2, 3, 8, 16, 23, 99} {
		m.width = width
		for lineNumber, line := range strings.Split(m.render(), "\n") {
			if got := ansi.StringWidth(line); got > width {
				t.Fatalf("width %d line %d rendered %d cells: %q", width, lineNumber+1, got, line)
			}
		}
	}
}

func TestFilterBarSanitizesTerminalControlsWithoutChangingState(t *testing.T) {
	hostileText := "safe\x1b[31m-red\x1b[0m\x1b]2;owned\x07\x00\x9b31m"
	hostileTag := "tag\x1bPpayload\x1b\\\x1b]52;c;stolen\x07\x1f"
	m := NewModel(stubBoardReader{}, nil, "u")
	m.board = board.Board{Tasks: []board.Task{{ID: "x", Status: board.StatusTodo, Tags: []string{hostileTag}}}}
	m.filter.restore(boardFilter{Text: hostileText, Tags: []string{hostileTag}})
	storedText := m.filter.input.Value()
	m.filter.focus = filterLabels
	view, hits := m.renderFilterBar(160)
	for _, r := range view {
		if r == '\n' {
			continue
		}
		if r <= 0x1f || (r >= 0x7f && r <= 0x9f) {
			t.Fatalf("filter bar contains terminal control U+%04X: %q", r, view)
		}
	}
	if strings.Contains(view, "payload") || strings.Contains(view, "stolen") {
		t.Fatalf("filter bar retained control-sequence payload: %q", view)
	}
	if m.filter.input.Value() != storedText || !reflect.DeepEqual(m.filter.tags, []string{hostileTag}) || m.board.Tasks[0].Tags[0] != hostileTag {
		t.Fatal("render sanitization mutated filter or board state")
	}
	foundOriginalHit := false
	for _, hit := range hits {
		if hit.kind == boardHitFilterLabel && hit.tag == hostileTag {
			foundOriginalHit = true
		}
	}
	if !foundOriginalHit {
		t.Fatal("sanitized label lost its exact filter identity")
	}
}

func TestBoardMouseFocusLeavesTheFilter(t *testing.T) {
	m := NewModel(stubDetailBoardReader{stubBoardReader{board: filterFixture()}}, nil, "u")
	completeBoardLoad(t, &m, m.Init())
	m.filter.focusText()
	updateTestModel(t, &m, boardColumnClickedMsg{status: board.StatusDoing})
	if m.filter.focus != filterUnfocused || m.filter.input.Focused() {
		t.Fatalf("column click retained filter focus: %+v", m.filter)
	}
	m.filter.focusText()
	command := updateTestModel(t, &m, boardCardClickedMsg{taskID: "login"})
	if command == nil || !m.detail.IsOpen() || m.filter.focus != filterUnfocused || m.filter.input.Focused() {
		t.Fatalf("card click = detail:%v filter:%+v command:%v", m.detail.IsOpen(), m.filter, command)
	}
}

func TestPreferenceLegacyDecodeAndReadFailures(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "preferences.json")
	if err := os.WriteFile(path, []byte("{\"show_cancelled\":true}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := loadTUIPreferences(path)
	if err != nil || !got.ShowCancelled || got.Filter.Text != "" || len(got.Filter.Tags) != 0 {
		t.Fatalf("legacy preference = %+v,%v", got, err)
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := loadTUIPreferences(path); err == nil && os.Geteuid() != 0 {
		t.Fatal("unreadable preference loaded")
	}

	root := NewModel(stubBoardReader{}, nil, "u")
	root.board = filterFixture()
	root.savePreferences = func(tuiPreferences) error { return errors.New("disk full") }
	command := root.mutateFilter(func(filter *boardFilterState) { filter.toggleTag("bug") })
	updateTestModel(t, &root, command())
	if !root.filter.matches(root.board.Tasks[0]) || root.preferenceErr == nil {
		t.Fatalf("failed persistence changed in-memory filter: filter=%+v err=%v", root.filter.value(), root.preferenceErr)
	}
}

func TestPreferenceRestoreAndSetupFailures(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "preferences.json")
	want := tuiPreferences{
		ShowCancelled: true,
		Filter:        boardFilter{Text: "login", Tags: []string{"bug", "bug", "  auth  "}},
	}
	if err := saveTUIPreferences(path, want); err != nil {
		t.Fatal(err)
	}
	m := NewModel(stubBoardReader{}, nil, "alice")
	m.restorePreferences(path)
	if m.preferenceErr != nil || !m.boardView.showCancelled || m.filter.input.Value() != "login" || !reflect.DeepEqual(m.filter.tags, []string{"bug", "auth"}) {
		t.Fatalf("restored model = cancelled:%v filter:%+v err:%v", m.boardView.showCancelled, m.filter.value(), m.preferenceErr)
	}

	failure := errors.New("injected setup failure")
	ops := osPreferenceFileOps
	ops.mkdirAll = func(string, os.FileMode) error { return failure }
	if err := saveTUIPreferencesWithOps(path, tuiPreferences{}, ops); err == nil || !strings.Contains(err.Error(), "create directory") {
		t.Fatalf("mkdir failure = %v", err)
	}
	ops = osPreferenceFileOps
	ops.createTmp = func(string, string) (preferenceTempFile, error) { return nil, failure }
	if err := saveTUIPreferencesWithOps(path, tuiPreferences{}, ops); err == nil || !strings.Contains(err.Error(), "create temporary") {
		t.Fatalf("temporary-file failure = %v", err)
	}

	malformed := filepath.Join(dir, "malformed.json")
	if err := os.WriteFile(malformed, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.restorePreferences(malformed)
	if m.preferenceErr == nil || m.boardView.showCancelled != true || m.filter.input.Value() != "login" {
		t.Fatalf("failed restore discarded last good state: cancelled:%v filter:%+v err:%v", m.boardView.showCancelled, m.filter.value(), m.preferenceErr)
	}
}

func TestFilterInteractionBranches(t *testing.T) {
	m := NewModel(stubBoardReader{}, nil, "u")
	if changed := m.filter.clear(); changed {
		t.Fatal("empty filter reported a clear")
	}
	if m.filter.toggleTag("") || m.filter.hasTag("missing") {
		t.Fatal("empty/missing tag changed filter")
	}
	m.filter.toggleTag("stale")
	if !m.filter.hasTag("stale") || !m.filter.toggleTag("stale") || m.filter.hasTag("stale") {
		t.Fatal("tag removal failed")
	}
	if command := m.mutateFilter(func(*boardFilterState) {}); command != nil {
		t.Fatalf("no-op mutation returned %v", command)
	}

	// Text focus on a label-free board can only return to the board.
	updateTestModel(t, &m, tea.KeyPressMsg{Code: '/'})
	if handled, command := m.handleFilterKey(tea.KeyPressMsg{Code: tea.KeyTab}); !handled || command != nil || m.filter.focus != filterUnfocused {
		t.Fatalf("label-free tab = handled:%v command:%v focus:%v", handled, command, m.filter.focus)
	}
	if handled, _ := m.handleFilterKey(tea.KeyPressMsg{Code: 'f'}); !handled || m.filter.focus != filterUnfocused {
		t.Fatalf("label-free f = handled:%v focus:%v", handled, m.filter.focus)
	}
	if handled, command := m.handleFilterKey(tea.KeyPressMsg{Code: 'x'}); handled || command != nil {
		t.Fatalf("inactive clear = handled:%v command:%v", handled, command)
	}

	m.board = filterFixture()
	updateTestModel(t, &m, tea.KeyPressMsg{Code: '/'})
	if handled, _ := m.handleFilterKey(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}); !handled || m.filter.focus != filterLabels || m.filter.labelIndex != len(m.filterLabels())-1 {
		t.Fatalf("shift-tab label focus = handled:%v focus:%v index:%d", handled, m.filter.focus, m.filter.labelIndex)
	}
	last := m.filter.labelIndex
	for _, key := range []tea.KeyPressMsg{{Code: tea.KeyLeft}, {Code: 'h'}, {Code: tea.KeyTab, Mod: tea.ModShift}} {
		if handled, command := m.handleFilterKey(key); !handled || command != nil {
			t.Fatalf("previous label key %q = handled:%v command:%v", key.String(), handled, command)
		}
	}
	if m.filter.labelIndex == last {
		t.Fatal("previous label keys did not move focus")
	}
	for _, key := range []tea.KeyPressMsg{{Code: tea.KeyRight}, {Code: 'l'}, {Code: tea.KeyTab}} {
		if handled, command := m.handleFilterKey(key); !handled || command != nil {
			t.Fatalf("next label key %q = handled:%v command:%v", key.String(), handled, command)
		}
	}
	if handled, command := m.handleFilterKey(tea.KeyPressMsg{Code: '?'}); !handled || command != nil {
		t.Fatalf("unknown label key = handled:%v command:%v", handled, command)
	}
	if handled, command := m.handleFilterKey(tea.KeyPressMsg{Code: '/'}); !handled || command == nil || m.filter.focus != filterText {
		t.Fatalf("label-to-text = handled:%v command:%v focus:%v", handled, command, m.filter.focus)
	}
	if handled, command := m.handleFilterKey(tea.KeyPressMsg{Code: tea.KeyLeft}); !handled || command != nil {
		t.Fatalf("text cursor key = handled:%v command:%v", handled, command)
	}
	if handled, command := m.handleFilterKey(tea.KeyPressMsg{Code: tea.KeyEnter}); !handled || command != nil || m.filter.focus != filterUnfocused {
		t.Fatalf("text enter = handled:%v command:%v focus:%v", handled, command, m.filter.focus)
	}

	m.filter.restore(boardFilter{Tags: []string{"removed-from-board"}})
	labels := m.filterLabels()
	if labels[len(labels)-1] != "ui" || !containsString(labels, "removed-from-board") {
		t.Fatalf("labels with stale selection = %v", labels)
	}
	m.filter.focus = filterLabels
	m.filter.labelIndex = 999
	if handled, command := m.handleFilterKey(tea.KeyPressMsg{Code: tea.KeyEscape}); !handled || command != nil || m.filter.focus != filterUnfocused {
		t.Fatalf("label escape = handled:%v command:%v focus:%v", handled, command, m.filter.focus)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestFilterMouseControlsAndPreferenceEquality(t *testing.T) {
	hits := []boardHit{
		{x0: 0, x1: 5, y0: 1, y1: 2, kind: boardHitFilterText},
		{x0: 5, x1: 10, y0: 1, y1: 2, kind: boardHitFilterClear},
		{x0: 0, x1: 10, y0: 2, y1: 3, status: board.StatusDoing},
	}
	for _, test := range []struct {
		x, y int
		want any
	}{
		{1, 1, filterTextClickedMsg{}},
		{6, 1, filterClearClickedMsg{}},
		{1, 2, boardColumnClickedMsg{status: board.StatusDoing}},
	} {
		command := boardMouseHandler(hits)(tea.MouseClickMsg{X: test.x, Y: test.y, Button: tea.MouseLeft})
		if command == nil || !reflect.DeepEqual(command(), test.want) {
			t.Fatalf("mouse %d,%d = %#v, want %#v", test.x, test.y, command, test.want)
		}
	}
	if command := boardMouseHandler(hits)(tea.MouseClickMsg{X: 1, Y: 1, Button: tea.MouseRight}); command != nil {
		t.Fatalf("right click returned %v", command)
	}

	base := tuiPreferences{ShowCancelled: true, Filter: boardFilter{Text: "x", Tags: []string{"a"}}}
	if !preferencesEqual(base, base) || preferencesEqual(base, tuiPreferences{}) ||
		preferencesEqual(base, tuiPreferences{ShowCancelled: true, Filter: boardFilter{Text: "y", Tags: []string{"a"}}}) ||
		preferencesEqual(base, tuiPreferences{ShowCancelled: true, Filter: boardFilter{Text: "x", Tags: []string{"b"}}}) {
		t.Fatal("preference equality branches changed")
	}

	m := NewModel(stubBoardReader{}, nil, "u")
	m.savePreferences = func(tuiPreferences) error { return nil }
	m.prefSaving = true
	pending := base
	m.prefPending = &pending
	if command := m.finishPreferences(preferenceSavedMsg{preferences: base}); command != nil || m.prefSaving {
		t.Fatalf("equal pending snapshot retried: command=%v saving=%v", command, m.prefSaving)
	}
}

func TestMixedPreferenceWritesSerializeLatestSnapshot(t *testing.T) {
	var saved []tuiPreferences
	m := NewModel(stubBoardReader{}, nil, "u")
	m.board = filterFixture()
	m.savePreferences = func(preferences tuiPreferences) error {
		saved = append(saved, preferences)
		return nil
	}
	m.boardView.handleKey("c", m.filteredBoard())
	first := m.queuePreferences()
	if command := m.mutateFilter(func(filter *boardFilterState) { filter.toggleTag("bug") }); command != nil {
		t.Fatalf("overlapping filter write returned %v", command)
	}
	m.boardView.handleKey("c", m.filteredBoard())
	if command := m.queuePreferences(); command != nil {
		t.Fatalf("overlapping cancelled write returned %v", command)
	}
	successor := updateTestModel(t, &m, first())
	if successor == nil || m.prefPending != nil || !m.prefSaving {
		t.Fatalf("latest snapshot was not scheduled: model=%+v command=%v", m, successor)
	}
	if command := updateTestModel(t, &m, successor()); command != nil || m.prefSaving {
		t.Fatalf("successor did not finish: command=%v saving=%v", command, m.prefSaving)
	}
	want := []tuiPreferences{
		{ShowCancelled: true},
		{Filter: boardFilter{Tags: []string{"bug"}}},
	}
	if !reflect.DeepEqual(saved, want) {
		t.Fatalf("serialized snapshots = %+v, want %+v", saved, want)
	}
}

func TestFailedPreferenceWriteRetriesEqualPendingSnapshot(t *testing.T) {
	want := tuiPreferences{ShowCancelled: true, Filter: boardFilter{Text: "same", Tags: []string{"bug"}}}
	writes := 0
	m := NewModel(stubBoardReader{}, nil, "u")
	m.savePreferences = func(got tuiPreferences) error {
		writes++
		if !preferencesEqual(got, want) {
			t.Fatalf("retry wrote %+v, want %+v", got, want)
		}
		return nil
	}
	m.prefSaving = true
	pending := want
	m.prefPending = &pending
	retry := m.finishPreferences(preferenceSavedMsg{preferences: want, err: errors.New("disk full")})
	if retry == nil || !m.prefSaving || m.prefPending != nil {
		t.Fatalf("failed equal snapshot was not retried: model=%+v command=%v", m, retry)
	}
	if next := updateTestModel(t, &m, retry()); next != nil || m.prefSaving || m.preferenceErr != nil || writes != 1 {
		t.Fatalf("retry completion = saving:%v err:%v writes:%d command:%v", m.prefSaving, m.preferenceErr, writes, next)
	}
}
