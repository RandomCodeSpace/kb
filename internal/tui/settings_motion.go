package tui

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/tui/widget/spin"
)

// The settings pane's share of the two-tier busy split of spec section 10.2.4.
//
// A connection test is a network round trip to a model endpoint or a forge, so
// it takes the branded tier: that is the wait a branded frame is spent
// announcing. Loading the pane and writing it back are local store work, which
// is plumbing however important the write is, so they keep bubbles' dots.

const (
	// loadSettingsLabel is the plain tier's label for the first read. Spec
	// section 10.8.4: lowercase, present continuous, and no ellipsis of its
	// own, because the animation is the ellipsis.
	loadSettingsLabel = "loading settings"

	// testConnectionLabel is the branded tier's label. One label covers the AI
	// probe and a forge probe: both are the same round trip from the user's
	// side, and the row the animation replaces already names which one.
	testConnectionLabel = "testing connection"

	saveSettingsLabel      = "saving settings"
	saveIntegrationLabel   = "saving integration"
	removeIntegrationLabel = "removing integration"
)

// brandRowWidth is the row the branded engine is fitted to: the whole row at
// its full label, so the frame cache is built once per mount rather than once
// per resize. A pane too narrow for it truncates in the band, which is where
// every other overlong band row is already cut.
const brandRowWidth = spin.MaxLabelW + spin.EllipsisField + spin.SuffixField

// brandBusy is the branded tier's gate.
func (m *settingsModel) brandBusy() bool {
	return m.loaded && (m.busy == "ai:test" || strings.HasPrefix(m.busy, "forge:test:"))
}

// plainBusy is the plain tier's gate: the first read and every store write.
func (m *settingsModel) plainBusy() bool {
	return !m.loaded || (m.busy != "" && !m.brandBusy())
}

// busyLabel names the operation in flight, empty when the pane is settled.
func (m *settingsModel) busyLabel() string {
	switch {
	case !m.loaded:
		return loadSettingsLabel
	case m.brandBusy():
		return testConnectionLabel
	case m.busy == "ai:save":
		return saveSettingsLabel
	case strings.HasPrefix(m.busy, "forge:save:"):
		return saveIntegrationLabel
	case strings.HasPrefix(m.busy, "forge:remove:"):
		return removeIntegrationLabel
	default:
		return ""
	}
}

// spinTick advances the plain busy indicator. The loop stops as soon as nothing
// is in flight, so a settled pane costs no timers.
func (m *settingsModel) spinTick(msg spinner.TickMsg) tea.Cmd {
	if !m.plainBusy() {
		return nil
	}
	var command tea.Cmd
	m.spin, command = m.spin.Update(msg)
	return command
}

// startSpinner is the command that begins the plain tick loop.
func (m *settingsModel) startSpinner() tea.Cmd { return m.spin.Tick }

// plainFrame is the plain tier's rendered frame. It is handed to widget.Busy
// unstripped: the frame is the one part of a busy row that is supposed to carry
// a color (spec section 10.8.4).
func (m *settingsModel) plainFrame() string {
	if len(m.spin.Spinner.Frames) == 0 {
		return ""
	}
	return m.spin.View()
}

// brandStep advances the branded engine. The gate is read on every tick rather
// than mirrored into a flag, and the engine drops a tick whose generation or
// seed does not match (spec section 10.2.3).
func (m *settingsModel) brandStep(msg spin.StepMsg) tea.Cmd {
	return m.brand.Step(msg, m.brandBusy())
}

// startBrand mounts the branded engine for a new connection test and opens its
// chain. A backgrounded pane starts nothing: the probe runs, the animation does
// not (spec section 10.2.6).
func (m *settingsModel) startBrand() tea.Cmd {
	m.brandStarted = m.clock()()
	m.configureBrand()
	if !m.frontMost {
		return nil
	}
	return m.brand.Start()
}

// clock is the pane's injected now, defaulted here so a zero-value pane and a
// test that never sets one both read a clock that exists. Determinism contract
// point 4 of spec section 10.2.2: the engine never reads a clock of its own,
// the surface hands it one through a closure.
func (m *settingsModel) clock() func() time.Time {
	if m.now == nil {
		m.now = time.Now
	}
	return m.now
}

// configureBrand resolves the engine's settings. It is idempotent: a repeated
// settings hash reuses the frame cache rather than rebuilding it.
func (m *settingsModel) configureBrand() {
	styles := m.themeStyles()
	started := m.brandStarted
	now := m.clock()
	m.brand.Configure(styles, spin.Settings{
		Label:    testConnectionLabel,
		Seed:     spin.SeedSettingsTest,
		Scramble: true,
		Suffix:   func() string { return spin.Elapsed(now().Sub(started)) },
	}.Fit(styles, brandRowWidth))
}

// SetFrontMost is the background handoff of spec section 10.2.6: a pane behind
// another overlay stops its engine - the probe keeps running, only the
// animation stops - and remounts at step 0 when it comes back to front.
func (m *settingsModel) SetFrontMost(front bool) tea.Cmd {
	if m == nil || m.frontMost == front {
		return nil
	}
	m.frontMost = front
	if !front {
		m.brand.Stop()
		return nil
	}
	if !m.brandBusy() {
		return nil
	}
	return m.brand.Start()
}

// BrandMounted reports whether the pane's branded engine is ticking.
func (m *settingsModel) BrandMounted() bool {
	return m != nil && m.brand.Mounted()
}
