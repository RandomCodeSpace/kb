package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/store"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
	"github.com/RandomCodeSpace/kb/internal/tui/widget/spin"
)

func newLoadedSettings(t *testing.T) (*settingsModel, *recordingAIProber) {
	t.Helper()
	probe := &recordingAIProber{}
	backend := &faultSettingsStore{
		ai:      store.AISettings{BaseURL: "https://api.example", Model: "model-a"},
		sources: []store.ForgeSource{{Name: "work", Kind: "gitlab", BaseURL: "https://forge.example"}},
	}
	model := newSettingsModelWithBackends(backend, probe, &recordingForgeProber{}, "alice", context.Background())
	loadSettingsForTest(t, model)
	return model, probe
}

// TestSettingsPlainTierCarriesTheLocalWork is the plain half of the tier split
// of spec section 10.2.4: the first read and every store write are plumbing, so
// they keep bubbles' dots and the tick loop stops as soon as nothing is in
// flight.
func TestSettingsPlainTierCarriesTheLocalWork(t *testing.T) {
	model, _ := newLoadedSettings(t)
	if model.plainBusy() || model.brandBusy() || model.busyLabel() != "" {
		t.Fatalf("a settled pane reported busy: %q", model.busyLabel())
	}
	if model.spinTick(spinner.TickMsg{}) != nil {
		t.Fatal("a settled pane kept a spinner tick alive")
	}
	if model.plainFrame() == "" {
		t.Fatal("a constructed pane has no spinner frames")
	}
	if (&settingsModel{}).plainFrame() != "" {
		t.Fatal("a zero-value pane rendered a spinner frame")
	}

	for _, test := range []struct {
		busy string
		want string
	}{
		{busy: "ai:save", want: saveSettingsLabel},
		{busy: "forge:save:source:work", want: saveIntegrationLabel},
		{busy: "forge:remove:source:work", want: removeIntegrationLabel},
	} {
		model.busy = test.busy
		if !model.plainBusy() || model.busyLabel() != test.want {
			t.Fatalf("%q label = %q, want %q", test.busy, model.busyLabel(), test.want)
		}
		if model.spinTick(spinner.TickMsg{ID: model.spin.ID()}) == nil {
			t.Fatalf("%q dropped the spinner tick", test.busy)
		}
		if got := ansi.Strip(model.View(80, 24)); !strings.Contains(got, test.want) {
			t.Fatalf("%q band = %s", test.busy, got)
		}
	}
	model.busy = ""

	// Spec section 10.8.4 deletes the ansi.Strip: the frame is the one part of
	// a busy row that is supposed to carry a color, and the band re-arms itself
	// around it through BandRun.
	model.busy = "ai:save"
	band := model.busyBand(saveSettingsLabel, 40)
	if !strings.Contains(band, model.plainFrame()) {
		t.Fatal("the plain band stripped its spinner frame")
	}
	if strings.Contains(ansi.Strip(band), saveSettingsLabel+"...") {
		t.Fatal("the plain busy label kept its ellipsis")
	}
}

// TestSettingsLoadingRowIsTheSectionsOwn is spec section 10.8.7: the AI block's
// first read is a plain-tier busy row under its own band, never a static line
// and never the footer.
func TestSettingsLoadingRowIsTheSectionsOwn(t *testing.T) {
	backend := &faultSettingsStore{ai: store.AISettings{BaseURL: "https://api.example"}}
	model := newSettingsModelWithBackends(backend, &recordingAIProber{}, &recordingForgeProber{}, "alice", context.Background())
	if !model.plainBusy() || model.busyLabel() != loadSettingsLabel {
		t.Fatalf("an unloaded pane reported %q", model.busyLabel())
	}
	view := ansi.Strip(model.View(80, 24))
	if !strings.Contains(view, loadSettingsLabel) {
		t.Fatalf("loading row missing:\n%s", view)
	}
	if strings.Contains(view, loadSettingsLabel+"...") {
		t.Fatal("the loading label kept its ellipsis")
	}
	row := model.busyRow(loadSettingsLabel, 40)
	if row.rendered == "" || !strings.Contains(row.rendered, model.plainFrame()) {
		t.Fatal("the loading row stripped its spinner frame")
	}
}

// TestSettingsBrandedEngineDrivesTheConnectionTest is the branded half of the
// split and the pane's share of the test obligations of spec section 10.2.7:
// the gate owns the chain, the root routes the step, a settled surface does not
// re-arm, and the settled frame is byte stable under tick.
func TestSettingsBrandedEngineDrivesTheConnectionTest(t *testing.T) {
	model, _ := newLoadedSettings(t)
	if !isSettingsMessage(spin.StepMsg{}) || !isSettingsMessage(spinner.TickMsg{}) {
		t.Fatal("the root does not route the pane's busy messages")
	}
	if model.brandBusy() || model.BrandMounted() {
		t.Fatal("a settled pane mounted the branded engine")
	}
	if model.brandStep(spin.StepMsg{Seed: spin.SeedSettingsTest}) != nil {
		t.Fatal("a settled pane kept the branded chain alive")
	}

	base := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	elapsed := time.Duration(0)
	model.now = func() time.Time { return base.Add(elapsed) }
	// The root hands the front-most surface the engine (spec section 10.2.6).
	// The probe itself is left unrun: this is the animation under test, and
	// running it would settle the gate that owns the chain.
	model.SetFrontMost(true)
	if model.startAITest() == nil {
		t.Fatal("the connection test dispatched no work")
	}
	if !model.brandBusy() || !model.BrandMounted() {
		t.Fatal("a testing pane did not mount the branded engine")
	}
	if model.plainBusy() || model.spinTick(spinner.TickMsg{ID: model.spin.ID()}) != nil {
		t.Fatal("the branded test drove the plain tier as well")
	}
	timing := model.themeStyles().Timing

	// The static label carries the birth delay, then the engine takes over.
	if got := ansi.Strip(model.View(80, 24)); !strings.Contains(got, testConnectionLabel) {
		t.Fatalf("pre-birth band = %s", got)
	}
	if model.brand.View() != "" {
		t.Fatal("the engine rendered a run inside the birth delay")
	}
	if model.brandStep(spin.StepMsg{Seed: spin.SeedSettingsTest, Gen: model.brand.Gen() - 1}) != nil {
		t.Fatal("a stale generation kept the branded chain alive")
	}
	if model.brandStep(spin.StepMsg{Seed: spin.SeedImportFetch, Gen: model.brand.Gen()}) != nil {
		t.Fatal("a foreign seed kept the branded chain alive")
	}

	step := spin.StepMsg{Seed: spin.SeedSettingsTest, Gen: model.brand.Gen()}
	elapsed = 62 * time.Second
	for range timing.BirthDelay + timing.BirthSteps + timing.ScrambleSteps - 1 {
		if model.Update(step) == nil {
			t.Fatal("a testing pane dropped the branded step")
		}
	}
	run := model.brand.View()
	if run == "" {
		t.Fatal("the born engine rendered no run")
	}
	if !strings.Contains(model.View(80, 24), model.themeStyles().BandRun(theme.BandFooter, run)) {
		t.Fatal("the band did not carry the engine's own run")
	}

	// Contract point 7: the settled frame is invariant across a full ellipsis
	// window, so a golden of it does not depend on when it was taken.
	for range timing.SuffixAfter {
		model.Update(step)
	}
	if !strings.Contains(ansi.Strip(model.View(80, 24)), "1m02s") {
		t.Fatal("the band carried no elapsed counter")
	}
	settled := model.View(80, 24)
	for range timing.EllipsisStride - 1 {
		model.Update(step)
		if model.View(80, 24) != settled {
			t.Fatal("the settled branded frame moved under tick")
		}
	}

	model.finishTest(nil, "")
	if model.Update(step) != nil || model.BrandMounted() {
		t.Fatal("a settled pane re-armed the branded chain")
	}
}

// TestSettingsBrandedEngineFollowsTheZOrder is the background handoff of spec
// section 10.2.6: a pane behind another overlay stops its engine and remounts
// at step 0 when it comes back to front.
func TestSettingsBrandedEngineFollowsTheZOrder(t *testing.T) {
	model, _ := newLoadedSettings(t)
	model.SetFrontMost(true)
	model.startAITest()
	if model.SetFrontMost(true) != nil {
		t.Fatal("an unchanged z-order rearmed the branded chain")
	}
	if model.SetFrontMost(false) != nil || model.BrandMounted() {
		t.Fatal("a backgrounded pane kept its engine")
	}
	if got := ansi.Strip(model.View(80, 24)); !strings.Contains(got, testConnectionLabel) {
		t.Fatalf("a backgrounded pane dropped its static busy label:\n%s", got)
	}
	if model.SetFrontMost(true) == nil || !model.BrandMounted() {
		t.Fatal("a refronted pane did not remount its engine")
	}
	model.finishTest(nil, "")
	model.SetFrontMost(false)
	if model.SetFrontMost(true) != nil {
		t.Fatal("a settled pane mounted an engine on refront")
	}

	// A backgrounded pane starts nothing at all: the probe runs, the animation
	// does not.
	model.SetFrontMost(false)
	model.busy = "ai:test"
	if model.startBrand() != nil || model.BrandMounted() {
		t.Fatal("a backgrounded pane armed a chain")
	}
	// A nil pane is a closed pane, which the z-order sync still asks.
	var closed *settingsModel
	if closed.SetFrontMost(true) != nil || closed.BrandMounted() {
		t.Fatal("a closed pane took the engine")
	}
}

// TestSettingsClockDefaultsToWallTime covers the zero-value pane the elapsed
// suffix reads through. Determinism contract point 4 of spec section 10.2.2:
// the engine reads no clock, the surface hands it one.
func TestSettingsClockDefaultsToWallTime(t *testing.T) {
	model, _ := newLoadedSettings(t)
	model.now = nil
	if model.clock() == nil || model.now == nil {
		t.Fatal("the pane resolved no clock")
	}
	if model.clock()().IsZero() {
		t.Fatal("the pane's clock reads no time")
	}
}

// TestSettingsErrorLeavesTheBandAndNamesItsControl is ratified call 12 and spec
// section 10.8.5: the error moves out of the footer band into a body row above
// the action row, in TintDanger, with a tail naming the button that failed.
func TestSettingsErrorLeavesTheBandAndNamesItsControl(t *testing.T) {
	model, probe := newLoadedSettings(t)
	probe.err = errors.New("dial tcp 10.0.0.4:443: connection refused")
	model.startAITest()
	model.Update(aiSettingsTestedMsg{err: probe.err})
	if !model.statusIsError || model.statusTail != "Test connection" {
		t.Fatalf("failed test = error:%v tail:%q", model.statusIsError, model.statusTail)
	}

	view := model.View(90, 30)
	plain := ansi.Strip(view)
	if !strings.Contains(plain, "▲ dial tcp") {
		t.Fatalf("error row missing its alert glyph:\n%s", plain)
	}
	if !strings.Contains(plain, "Test connection") {
		t.Fatalf("error row named no control:\n%s", plain)
	}
	styles := model.themeStyles()
	if !strings.Contains(view, openingSequence(styles.On(theme.TintDanger, theme.OverlaySurf))) {
		t.Fatal("the error row is not TintDanger on the panel tier")
	}
	// The band is back to hints: neither Danger slot clears the floor on it.
	lines := strings.Split(plain, "\n")
	if band := lines[len(lines)-2]; strings.Contains(band, "dial tcp") {
		t.Fatalf("the footer band carried the error: %q", band)
	}

	// A failure with nothing to retry names no control.
	broken := &faultSettingsStore{aiErr: errors.New("load failed")}
	unloaded := newSettingsModelWithBackends(broken, &recordingAIProber{}, &recordingForgeProber{}, "alice", context.Background())
	unloaded.Update(unloaded.Init()())
	if !unloaded.statusIsError || unloaded.statusTail != "" {
		t.Fatalf("load failure tail = %q", unloaded.statusTail)
	}
	if got := ansi.Strip(unloaded.View(44, 8)); !strings.Contains(got, "▲ load failed") ||
		strings.Contains(got, loadSettingsLabel) {
		t.Fatalf("a failed load still reported loading:\n%s", got)
	}
}

// TestSettingsEmptyIntegrationsNamesItsButton is spec section 10.8.3: a section
// that renders its band whether or not it is filled takes the empty row, and
// the tail is the label of the button its action row already carries.
func TestSettingsEmptyIntegrationsNamesItsButton(t *testing.T) {
	backend := &faultSettingsStore{ai: store.AISettings{BaseURL: "https://api.example"}}
	model := newSettingsModelWithBackends(backend, &recordingAIProber{}, &recordingForgeProber{}, "alice", context.Background())
	loadSettingsForTest(t, model)
	got := ansi.Strip(model.View(90, 30))
	if !strings.Contains(got, "○ no integrations  + Add integration") {
		t.Fatalf("empty integrations row missing:\n%s", got)
	}
	if strings.Contains(got, "(none configured)") {
		t.Fatal("the pane kept its parenthesised placeholder")
	}
}

// TestSettingsFocusNeverReflowsThePane is spec section 10.4.4 at pane scope:
// the focus gutter is reserved in every state, so moving the keyboard across
// the pane changes colors and attributes and never a cell.
func TestSettingsFocusNeverReflowsThePane(t *testing.T) {
	model, _ := newLoadedSettings(t)
	targets := model.focusTargets()
	if len(targets) < 2 {
		t.Fatalf("pane exposes %d focus targets", len(targets))
	}
	model.focus = targets[0]
	model.applyFocus()
	widths := lineWidths(ansi.Strip(model.View(90, 40)))
	for _, target := range targets[1:] {
		model.focus = target
		model.applyFocus()
		if got := lineWidths(ansi.Strip(model.View(90, 40))); !sameWidths(got, widths) {
			t.Fatalf("focusing %q reflowed the pane: %v vs %v", target, got, widths)
		}
	}
}

// openingSequence is the SGR prefix a style opens with, which is what a row
// rendered through one Render call carries at its front.
func openingSequence(style lipgloss.Style) string {
	const sentinel = "\x00"
	opening, _, _ := strings.Cut(style.Render(sentinel), sentinel)
	return opening
}

func lineWidths(view string) []int {
	lines := strings.Split(view, "\n")
	widths := make([]int, len(lines))
	for index, line := range lines {
		widths[index] = ansi.StringWidth(line)
	}
	return widths
}

func sameWidths(got, want []int) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}
