package tui

import (
	"context"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/RandomCodeSpace/kb/internal/ai"
	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/tui/issueimport"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
	"github.com/RandomCodeSpace/kb/internal/tui/widget/spin"
)

// mountImportEngine opens the issue-import overlay and starts a forge preview
// through the ordinary key path, which is the only way a branded engine ever
// mounts: the operation's own busy gate arms it.
func mountImportEngine(t *testing.T, model *Model) {
	t.Helper()
	model.issueImport = issueimport.New(&rootImportStore{}, rootImportBackend{}, "alice", context.Background())
	if command := updateTestModel(t, model, tea.KeyPressMsg{Code: 'i'}); command != nil {
		updateTestModel(t, model, command())
	}
	updateTestModel(t, model, tea.KeyPressMsg{Code: tea.KeyTab})
	for _, letter := range "acme/kb" {
		updateTestModel(t, model, tea.KeyPressMsg{Code: letter, Text: string(letter)})
	}
	updateTestModel(t, model, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !model.issueImport.BrandMounted() {
		t.Fatal("the forge preview did not mount a branded engine")
	}
}

// TestAtMostOneBrandedEngineTicks is obligation 11 of spec section 10.2.7 and
// the concurrency ceiling of spec section 10.2.6: the branded tier belongs to
// the front-most open surface and to nothing behind it.
func TestAtMostOneBrandedEngineTicks(t *testing.T) {
	model := newTestRootModelCtx(stubBoardReader{}, nil, "alice", context.Background())
	if model.frontSurface() != surfaceBoard || model.mountedEngines() != 0 {
		t.Fatal("an idle board mounted a branded engine")
	}

	mountImportEngine(t, &model)
	if model.frontSurface() != surfaceImport {
		t.Fatalf("front surface = %d, want issue import", model.frontSurface())
	}
	if model.mountedEngines() != spin.MaxEngines {
		t.Fatalf("mounted engines = %d, want %d", model.mountedEngines(), spin.MaxEngines)
	}

	// A surface opens over it. The fetch keeps running; only the animation
	// stops, and the ceiling holds.
	model.helpOpen = true
	if command := model.syncEngines(); command != nil {
		t.Fatal("backgrounding a surface armed a chain")
	}
	if model.issueImport.BrandMounted() || model.mountedEngines() != 0 {
		t.Fatalf("a backgrounded overlay kept %d engines", model.mountedEngines())
	}

	// It comes back to front and remounts at step 0.
	model.helpOpen = false
	if command := model.syncEngines(); command == nil {
		t.Fatal("refronting a busy surface armed no chain")
	}
	if !model.issueImport.BrandMounted() {
		t.Fatal("the overlay did not take the engine back")
	}
	if model.mountedEngines() != spin.MaxEngines {
		t.Fatalf("mounted engines = %d after the handoff", model.mountedEngines())
	}

	// Cancelling settles the gate, and the gate owns the chain: the animation
	// stops with the operation rather than one frame after it.
	updateTestModel(t, &model, tea.KeyPressMsg{Code: tea.KeyEscape})
	if model.issueImport.BrandMounted() || model.mountedEngines() != 0 {
		t.Fatalf("a cancelled fetch left %d engines", model.mountedEngines())
	}
	if !model.issueImport.IsOpen() {
		t.Fatal("cancelling the fetch closed the overlay")
	}
	updateTestModel(t, &model, tea.KeyPressMsg{Code: tea.KeyEscape})
	if model.frontSurface() != surfaceBoard || model.mountedEngines() != 0 {
		t.Fatalf("a closed overlay left %d engines", model.mountedEngines())
	}
}

// TestFrontSurfaceIsTheZOrder pins the stack the handoff reads, top first. It
// is the order route() already dispatches pointer messages in.
func TestFrontSurfaceIsTheZOrder(t *testing.T) {
	st := newSettingsTestStore(t)
	model := newTestRootModel(st, nil, "alice")
	if got := model.frontSurface(); got != surfaceBoard {
		t.Fatalf("an empty stack fronted %d", got)
	}
	for _, test := range []struct {
		name string
		open func(*Model)
		want surface
	}{
		{"detail", func(m *Model) {
			_ = m.detail.Open(board.Task{ID: "detail", Title: "Detail", Status: board.StatusTodo})
		}, surfaceDetail},
		{"settings", func(m *Model) {
			m.settings = newSettingsModel(st, "alice", context.Background())
		}, surfaceSettings},
		{"ADR", func(m *Model) {
			m.configureAI(ai.NewRunner(st, "", nil, nil), context.Background())
			_ = m.adr.Open()
		}, surfaceADR},
		{"editor", func(m *Model) { _ = m.editor.OpenAdd(board.StatusTodo) }, surfaceEditor},
		{"task action", func(m *Model) {
			m.openShipPrompt(board.Task{ID: "ship", Title: "Ship", Status: board.StatusTodo}, 0)
		}, surfaceAction},
		{"issue import", func(m *Model) {
			m.issueImport = issueimport.New(&rootImportStore{}, rootImportBackend{}, "alice", context.Background())
			_ = m.issueImport.Open()
		}, surfaceImport},
		{"help", func(m *Model) { m.helpOpen = true }, surfaceHelp},
		{"command palette", func(m *Model) { _ = m.openPalette() }, surfacePalette},
	} {
		test.open(&model)
		if got := model.frontSurface(); got != test.want {
			t.Fatalf("with %s open the front surface is %d, want %d", test.name, got, test.want)
		}
		// Nothing behind the front surface may hold an engine.
		if command := model.syncEngines(); command != nil {
			t.Fatalf("opening %s armed a chain on an idle stack", test.name)
		}
		if model.mountedEngines() != 0 {
			t.Fatalf("opening %s mounted %d engines", test.name, model.mountedEngines())
		}
	}
}

// TestColorProfileRebuildsTheDesignSystem is the second rebuild trigger of spec
// section 10.7.5: bubbletea sends the profile at startup and again if the
// terminal upgrades to truecolor through a capability report.
func TestColorProfileRebuildsTheDesignSystem(t *testing.T) {
	model := newTestRootModel(stubBoardReader{board: boardViewFixture(time.Now())}, nil, "alice")
	if !model.themeStyles().Graded() {
		t.Fatal("the reference target is not the startup assumption")
	}
	updateTestModel(t, &model, tea.ColorProfileMsg{Profile: colorprofile.ANSI256})
	if model.themeStyles().Graded() || model.themeStyles().Fidelity != theme.FidelityIndexed {
		t.Fatal("the profile message did not rebuild the design system")
	}
	before := model.themeStyles()
	updateTestModel(t, &model, tea.ColorProfileMsg{Profile: colorprofile.ANSI256})
	if model.themeStyles() != before {
		t.Fatal("an unchanged profile rebuilt the design system")
	}
	updateTestModel(t, &model, tea.ColorProfileMsg{Profile: colorprofile.TrueColor})
	if !model.themeStyles().Graded() {
		t.Fatal("a terminal upgrade did not restore the reference target")
	}
	// The background answer keeps the resolved floor rather than dropping it.
	updateTestModel(t, &model, tea.BackgroundColorMsg{})
	if model.themeStyles().Fidelity != theme.FidelityFull {
		t.Fatal("the background rebuild dropped the terminal floor")
	}
}

// BenchmarkBoardView is the budget gate of spec section 10.3.2. The slice that
// lands the first FPS-driven surface carries it: a kb tick re-runs the whole
// board render, overlay included, so if a full View() at the 211x52 reference
// frame exceeds 25ms - half the frame period, leaving headroom for the terminal
// write - the FPS token drops to 10.
func BenchmarkBoardView(b *testing.B) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	fixture := boardViewFixture(now)
	model := NewModel(stubBoardReader{board: fixture}, nil, "alice")
	model.loading = false
	model.haveBoardSnapshot = true
	model.board = fixture
	model.now = func() time.Time { return now }
	model.renderedAt = now
	sized, _ := model.Update(tea.WindowSizeMsg{Width: 211, Height: 52})
	model = sized.(Model)
	b.ReportAllocs()
	for b.Loop() {
		_ = model.View()
	}
}
