package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
	"github.com/RandomCodeSpace/kb/internal/tui/widget"
)

// brandStepMsg advances the launch reveal by one tick of the one clock. Gen is
// the generation guard of spec section 10.2.5: a tick issued by a superseded
// chain is dropped on arrival rather than doubling the frame rate.
type brandStepMsg struct{ gen uint32 }

// launching reports whether the launch screen of spec section 10.6.7 owns the
// frame: a load is in flight and no board snapshot has ever landed.
//
// It is deliberately the first arm of boardState's loading case and not the
// whole disjunction. A reload has a snapshot behind it and keeps the board on
// screen, and a load that failed has already cleared the flag, so the error
// reaches the user on the board's own footer rather than under a brand mark.
func (m Model) launching() bool {
	return m.loading && !m.haveBoardSnapshot
}

// brandSettled reports whether the reveal has reached the frame at which every
// column wears its ramp color.
func (m Model) brandSettled() bool {
	return m.brandFrame >= m.themeStyles().Timing.BrandBirthSteps
}

// brandReveal arms the launch reveal, or returns nil when it must not run.
//
// The reveal is a class-B effect of spec section 10.7.6: its only signal is the
// FgMuted to ramp step, so below FidelityFull the frame counter is seeded at
// the settled step and no tick is ever scheduled. That is also what keeps the
// ASCII-pinned structure goldens from ever capturing a mid-reveal frame - at
// that profile there is no mid-reveal frame to capture.
func (m Model) brandReveal() tea.Cmd {
	styles := m.themeStyles()
	if !m.launching() || !styles.Graded() || m.brandSettled() {
		return nil
	}
	return theme.Tick(styles.Timing.Interval(), brandStepMsg{gen: m.brandGen})
}

// stepBrand advances the reveal by one frame and re-arms, or terminates the
// chain. It returns nil - and the chain therefore ends - on a stale generation,
// on the settled frame, and the moment the launch screen stops owning the frame
// (spec section 10.6.6, termination).
func (m *Model) stepBrand(msg brandStepMsg) tea.Cmd {
	if msg.gen != m.brandGen || !m.launching() {
		return nil
	}
	if m.brandSettled() {
		return nil
	}
	m.brandFrame++
	if m.brandSettled() {
		return nil
	}
	return m.brandReveal()
}

// settleBrand pins the reveal at its settled frame and kills any chain still in
// flight. It runs on every theme rebuild, because the profile that decides
// whether the effect may run at all arrives after the model is built.
func (m *Model) settleBrand(styles *theme.Styles) {
	if styles.Graded() {
		return
	}
	m.brandGen++
	m.brandFrame = styles.Timing.BrandBirthSteps
}

// renderLaunch is the launch screen of spec section 10.6.7: the mark and the
// meta row centered on an otherwise bare Canvas frame. It costs no steady-state
// rows because the screen exists only while there is nothing else to draw.
func (m Model) renderLaunch() string {
	styles := m.themeStyles()
	width := max(m.width, 1)
	height := max(m.height, 1)
	status, statusSlot := m.boardState()
	block := widget.Brand(widget.BrandOpts{
		Styles:     styles,
		Width:      width,
		Height:     height,
		Stretch:    m.brandStretch,
		Frame:      m.brandFrame,
		Seed:       m.brandSeed,
		Status:     status,
		StatusSlot: statusSlot,
		Version:    m.version,
		On:         theme.Canvas,
	})
	blank := styles.SurfaceRun(theme.Canvas, strings.Repeat(" ", width))
	above := max((height-len(block))/2, 0)
	rows := make([]string, 0, height)
	for range above {
		rows = append(rows, blank)
	}
	rows = append(rows, block...)
	for len(rows) < height {
		rows = append(rows, blank)
	}
	return strings.Join(rows[:height], "\n")
}
