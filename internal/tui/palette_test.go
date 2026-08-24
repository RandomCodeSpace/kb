package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/ai"
	"github.com/RandomCodeSpace/kb/internal/tui/action"
)

// paletteBoardModel is a board with every optional feature wired, which is the
// state that offers the whole registry.
func paletteBoardModel(t *testing.T) Model {
	t.Helper()
	backend := newSettingsTestStore(t)
	m := NewModel(backend, nil, "alice")
	m.settingsNew = func() *settingsModel { return newSettingsModel(backend, "alice", context.Background()) }
	m.configureAI(ai.NewRunner(backend, "", nil, nil), context.Background())
	completeBoardLoad(t, &m, m.Init())
	m.loading = false
	m.board = boardViewFixture(time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC))
	m.width, m.height = 100, 32
	return m
}

// paletteKey is the chord that opens the palette.
func paletteKey() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl} }

// TestHelpPaneAndPaletteReadOneTable is the ticket's whole point. Both surfaces
// are built from internal/tui/action, so a key or a description can never be
// spelled two ways: this walks the rendered help pane and asserts every row of
// it is a registry row, then asserts the palette offers those same rows.
func TestHelpPaneAndPaletteReadOneTable(t *testing.T) {
	m := paletteBoardModel(t)
	features := m.actionFeatures()
	if !features.Editor || !features.Settings || !features.ADR || !features.Issues {
		t.Fatalf("the fixture board is not fully featured: %+v", features)
	}
	table := map[string]string{}
	for _, entry := range action.All() {
		table[entry.Hint] = entry.Name
	}
	keys := m.helpKeyMap()
	rows := 0
	for _, column := range append(keys.FullHelp(), keys.ShortHelp()) {
		for _, binding := range column {
			help := binding.Help()
			name, ok := table[help.Key]
			if !ok {
				t.Errorf("help pane spells key %q, which the registry does not carry", help.Key)
				continue
			}
			if help.Desc != name {
				t.Errorf("help pane describes %q as %q, the registry says %q", help.Key, help.Desc, name)
			}
			rows++
		}
	}
	if rows != len(action.All()) {
		t.Errorf("help pane renders %d rows, the registry has %d", rows, len(action.All()))
	}
	for _, entry := range action.Listed(features) {
		if _, ok := table[entry.Hint]; !ok {
			t.Errorf("palette offers %q, which the help pane never shows", entry.Hint)
		}
	}
}

// TestHelpPaneAdvertisesThePaletteChord keeps the palette discoverable: a
// keyboard-only surface nobody can find is a surface nobody uses.
func TestHelpPaneAdvertisesThePaletteChord(t *testing.T) {
	m := paletteBoardModel(t)
	updateTestModel(t, &m, tea.KeyPressMsg{Code: '?', Text: "?"})
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, action.PaletteKey+" command palette") {
		t.Errorf("help pane does not advertise %q:\n%s", action.PaletteKey, view)
	}
}

// TestDisabledFeaturesLeaveThePaletteAndHelpAgreeing is the same contract on a
// board built without the optional backends: what the pane greys out, the
// palette does not offer.
func TestDisabledFeaturesLeaveThePaletteAndHelpAgreeing(t *testing.T) {
	m := NewModel(stubBoardReader{}, nil, "alice")
	m.loading = false
	features := m.actionFeatures()
	if features.Editor || features.Settings || features.ADR || features.Issues {
		t.Fatalf("the bare fixture reports features: %+v", features)
	}
	offered := map[string]bool{}
	for _, entry := range action.Listed(features) {
		offered[entry.Hint] = true
	}
	for _, column := range m.helpKeyMap().FullHelp() {
		for _, binding := range column {
			if binding.Enabled() {
				continue
			}
			if offered[binding.Help().Key] {
				t.Errorf("palette offers %q while the help pane reports it disabled", binding.Help().Key)
			}
		}
	}
}

// TestChordOpensAndClosesThePalette is the root wiring.
func TestChordOpensAndClosesThePalette(t *testing.T) {
	m := paletteBoardModel(t)
	updateTestModel(t, &m, paletteKey())
	if !m.palette.IsOpen() {
		t.Fatal("ctrl+k did not open the palette")
	}
	if !m.overlayOpen() {
		t.Error("the open palette does not dim the board")
	}
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "COMMAND PALETTE") {
		t.Errorf("the frame does not carry the panel:\n%s", view)
	}
	if m.View().OnMouse == nil {
		t.Error("the open palette installed no pointer handler, so a click lands on the board it covers")
	}
	updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.palette.IsOpen() {
		t.Error("escape did not close the palette")
	}
}

// TestPaletteRunsTheActionThroughTheBoardsOwnHandler is why the palette carries
// no dispatch: choosing a row replays its key, and the board's existing handler
// does the work. If this ever passes while the board handler is unchanged, the
// palette has grown a second copy of the keymap.
func TestPaletteRunsTheActionThroughTheBoardsOwnHandler(t *testing.T) {
	for _, test := range []struct {
		name  string
		query string
		want  func(Model) bool
		label string
	}{
		{
			name: "opens the editor", query: "new card", label: "card editor",
			want: func(m Model) bool { return m.editor.IsOpen() },
		},
		{
			name: "opens the detail pane", query: "open card", label: "card detail",
			want: func(m Model) bool { return m.detail.IsOpen() },
		},
		{
			name: "opens the text filter", query: "text filter", label: "filter field",
			want: func(m Model) bool { return m.filter.focus != filterUnfocused },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := paletteBoardModel(t)
			updateTestModel(t, &m, paletteKey())
			for _, letter := range test.query {
				updateTestModel(t, &m, tea.KeyPressMsg{Code: letter, Text: string(letter)})
			}
			updateTestModel(t, &m, tea.KeyPressMsg{Code: tea.KeyEnter})
			if m.palette.IsOpen() {
				t.Fatal("the palette stayed open after a choice")
			}
			if !test.want(m) {
				t.Errorf("choosing %q did not open the %s", test.query, test.label)
			}
		})
	}
}

// TestPaletteSwallowsBoardKeysWhileOpen keeps the board from acting on the query
// the user is typing into the panel over it.
func TestPaletteSwallowsBoardKeysWhileOpen(t *testing.T) {
	m := paletteBoardModel(t)
	before := m.boardView.column
	updateTestModel(t, &m, paletteKey())
	for _, letter := range "lll" {
		updateTestModel(t, &m, tea.KeyPressMsg{Code: letter, Text: string(letter)})
	}
	if m.boardView.column != before {
		t.Errorf("typing into the palette moved the board from column %d to %d", before, m.boardView.column)
	}
	if !m.palette.IsOpen() {
		t.Fatal("the palette closed while being typed into")
	}
	updateTestModel(t, &m, boardCardClickedMsg{taskID: "ignored"})
	if !m.palette.IsOpen() {
		t.Error("a board click closed the palette")
	}
}

// TestCtrlCQuitsThroughThePalette keeps the root quit contract reachable from
// inside the overlay.
func TestCtrlCQuitsThroughThePalette(t *testing.T) {
	m := paletteBoardModel(t)
	updateTestModel(t, &m, paletteKey())
	command := updateTestModel(t, &m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if command == nil {
		t.Fatal("ctrl+c inside the palette did not quit")
	}
	if !m.stopped || m.palette.IsOpen() {
		t.Errorf("stopped=%v palette open=%v", m.stopped, m.palette.IsOpen())
	}
}

// TestRunPaletteActionIgnoresAnUnreplayableKey is the guard on the one row the
// registry spells as a chord.
func TestRunPaletteActionIgnoresAnUnreplayableKey(t *testing.T) {
	m := paletteBoardModel(t)
	next, command := m.runPaletteAction(action.Action{Key: action.PaletteKey}, nil)
	if command != nil {
		t.Errorf("an unreplayable key returned command %v", command)
	}
	if next.palette.IsOpen() {
		t.Error("an unreplayable key opened the palette")
	}
}

// TestPaletteAdoptsARebuiltTheme keeps the panel on the terminal's own palette
// after a background-color change.
func TestPaletteAdoptsARebuiltTheme(t *testing.T) {
	m := paletteBoardModel(t)
	updateTestModel(t, &m, paletteKey())
	updateTestModel(t, &m, tea.BackgroundColorMsg{})
	if !m.palette.IsOpen() {
		t.Fatal("a theme rebuild closed the palette")
	}
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "COMMAND PALETTE") {
		t.Errorf("the palette stopped rendering after a theme rebuild:\n%s", view)
	}
}
