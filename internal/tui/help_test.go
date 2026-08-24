package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/RandomCodeSpace/kb/internal/ai"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// goldenHelpModel is the ? overlay with every optional feature available, which
// is the state that renders both columns of the bubbles/help body at full
// length.
func goldenHelpModel(t *testing.T) Model {
	t.Helper()
	backend := newSettingsTestStore(t)
	m := newTestRootModel(backend, nil, "alice")
	m.settingsNew = func() *settingsModel { return newSettingsModel(backend, "alice", context.Background()) }
	m.configureAI(ai.NewRunner(backend, "", nil, nil), context.Background())
	completeBoardLoad(t, &m, m.Init())
	m.width, m.height = 72, 18
	m.helpOpen = true
	return m
}

// TestHelpOverlayGolden is the structure golden of the adopted help pane: the
// two key.Binding columns, the header band and the responsive footer ladder.
func TestHelpOverlayGolden(t *testing.T) {
	m := goldenHelpModel(t)
	content := m.keyboardHelpOverlay(actionBackground(m.width, m.height))
	golden.RequireEqual(t, []byte(ansi.Strip(theme.Downsample(content, theme.StructureProfile))))
}

// TestHelpOverlayColorGolden pins the palette. It is the check that bubbles
// renders its own runs but leaves the cells between them plain: every cell of
// the panel has to carry a kb surface token, which is what theme.SurfaceRun is
// for.
func TestHelpOverlayColorGolden(t *testing.T) {
	m := goldenHelpModel(t)
	content := m.keyboardHelpOverlay(actionBackground(m.width, m.height))
	golden.RequireEqual(t, []byte(theme.Downsample(content, theme.ColorProfile)))
}

// TestHelpKeyMapDisablesUnavailableFeatures is the self-managing keymap of the
// bubbles help contract: a feature the board was built without is a disabled
// binding, which the component then declines to render.
func TestHelpKeyMapDisablesUnavailableFeatures(t *testing.T) {
	bare := newTestRootModel(stubBoardReader{}, nil, "alice")
	for index, binding := range bare.helpKeyMap().actions {
		if index >= 4 && binding.Enabled() {
			t.Fatalf("optional binding %q enabled on a bare board", binding.Help().Key)
		}
		if index < 4 && !binding.Enabled() {
			t.Fatalf("core binding %q disabled", binding.Help().Key)
		}
	}
	full := goldenHelpModel(t).helpKeyMap()
	for _, binding := range full.actions {
		if !binding.Enabled() {
			t.Fatalf("binding %q disabled on a fully built board", binding.Help().Key)
		}
	}
	if len(full.ShortHelp()) != 2 || len(full.FullHelp()) != 2 {
		t.Fatalf("registry shape = %d short, %d columns", len(full.ShortHelp()), len(full.FullHelp()))
	}
}

// TestHelpFooterLadderKeepsTheCloseControl pins the one rung the band never
// drops: clicking the control is a frozen v1.0.1 dismissal.
func TestHelpFooterLadderKeepsTheCloseControl(t *testing.T) {
	m := goldenHelpModel(t)
	styles, keys := m.themeStyles(), m.helpKeyMap()
	full := ansi.Strip(m.helpFooter(styles, keys, 60))
	if !strings.Contains(full, "? or esc close help | q quit") {
		t.Fatalf("wide footer = %q", full)
	}
	// Spec section 10.4.6 step 2: the ellipsis rung costs its own cell plus a
	// separator, so a band that can seat one dismissal alongside the mark says
	// there is more rather than cutting a rung mid-word.
	if wide := ansi.Strip(m.helpFooter(styles, keys, 33)); wide != helpCloseLabel+" | ? or esc close help | …" {
		t.Fatalf("marked footer = %q", wide)
	}
	// Step 3 as amended after the #187 dogfood: a band too narrow for the mark
	// alongside a rung suppresses the mark instead, because the pane's only
	// dismissal hint fits in exactly the cells the mark was spending.
	if middle := ansi.Strip(m.helpFooter(styles, keys, 30)); middle != helpCloseLabel+" | ? or esc close help" {
		t.Fatalf("medium footer = %q", middle)
	}
	if narrow := ansi.Strip(m.helpFooter(styles, keys, 8)); narrow != helpCloseLabel {
		t.Fatalf("narrow footer = %q", narrow)
	}
}
