package carddetail

import (
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
	"github.com/RandomCodeSpace/kb/internal/tui/widget"
	"github.com/RandomCodeSpace/kb/internal/tui/widget/spin"
)

// The busy copy of spec section 10.8.7. Lowercase, present continuous, no
// ellipsis: the animation is the ellipsis.
const (
	driftCheckLabel    = "checking drift"
	driftBaselineLabel = "updating baseline"
	savingLabel        = "saving"
	commentsLabel      = "loading comments"
)

// brandRowWidth is the row the branded engine is fitted to: the whole branded
// row at its full label, so the frame cache is built once per mount rather than
// once per resize.
const brandRowWidth = spin.MaxLabelW + spin.EllipsisField + spin.SuffixField

// brandLabel is the drift operation's branded label, empty when no drift
// operation is running. Spec section 10.2.4 puts drift on the branded tier: it
// is a forge round trip, which is the wait a branded frame is spent announcing.
// Provenance and the check itself are two legs of one user-visible operation and
// share a label; accepting a baseline is a different sentence.
func (m Model) brandLabel() string {
	switch m.driftBusy {
	case "provenance", "check":
		return driftCheckLabel
	case "accept":
		return driftBaselineLabel
	default:
		return ""
	}
}

// brandBusy is the branded tier's gate.
func (m Model) brandBusy() bool { return m.brandLabel() != "" }

// brandStep advances the branded engine, dropping a tick whose seed or
// generation does not match and terminating the chain when the gate settles.
func (m *Model) brandStep(msg spin.StepMsg) tea.Cmd {
	return m.brand.Step(msg, m.brandBusy())
}

// clock is the injected wall clock the elapsed suffix reads through. The engine
// itself reads none (spec section 10.2.5).
func (m Model) clock() func() time.Time {
	if m.now != nil {
		return m.now
	}
	return time.Now
}

// configureBrand resolves the engine's settings for the running operation. It is
// idempotent: a repeated settings hash reuses the frame cache.
func (m *Model) configureBrand() {
	styles := m.styles
	started := m.brandStarted
	clock := m.clock()
	m.brand.Configure(styles, spin.Settings{
		Label:    m.brandLabel(),
		Seed:     spin.SeedDriftCheck,
		Scramble: true,
		Suffix:   func() string { return spin.Elapsed(clock().Sub(started)) },
	}.Fit(styles, brandRowWidth))
}

// armBrand records the operation's start and resolves the engine's settings.
// Mounting is left to the handoff below: the pane does not batch a tick chain
// into the command that carries the forge call, so the operation's own command
// keeps the shape the rest of the pane already gives it, and a pane that is not
// front-most starts nothing - the check runs, the animation does not.
func (m *Model) armBrand() {
	if !m.brandBusy() {
		return
	}
	m.brandStarted = m.clock()()
	m.configureBrand()
}

// stopBrand tears the engine down when the operation it was announcing lands.
func (m *Model) stopBrand() { m.brand.Stop() }

// SetFrontMost is the background handoff of spec section 10.2.6: a pane that is
// not front-most stops its engine and remounts at step 0 on return.
//
// It is also where a newly armed operation mounts, because the root calls it
// after every routed message. That is one rule instead of two: an engine ticks
// exactly when its surface is front-most and its gate is open, and no start path
// has to remember to open a chain of its own.
func (m *Model) SetFrontMost(front bool) tea.Cmd {
	changed := m.frontMost != front
	m.frontMost = front
	if !front {
		if changed {
			m.brand.Stop()
		}
		return nil
	}
	if !m.brandBusy() || m.brand.Mounted() {
		return nil
	}
	m.configureBrand()
	return m.brand.Start()
}

// BrandMounted reports whether this pane's branded engine is ticking.
func (m Model) BrandMounted() bool { return m.brand.Mounted() }

// plainBusy is the plain tier's gate: the local-store write. Spec section
// 10.2.4 splits the tiers on latency, and a comment or a link write completes
// inside two frames.
func (m Model) plainBusy() bool { return m.saving || m.loading }

// startPlain opens the plain tier's tick chain. The operation that begins a
// local read or write batches it onto its own command, which is how the three
// adopted plain-tier surfaces already start theirs; the branded engine is the
// one that mounts from the handoff, because only it has a concurrency ceiling
// to obey.
func (m *Model) startPlain() tea.Cmd {
	if !m.plainBusy() {
		return nil
	}
	return m.plain.Tick
}

// plainFrame is the plain tier's rendered frame, colored on the row's own
// surface. Spec section 10.8.4 deletes the ansi.Strip the adopted call sites
// wrapped their frame in: the frame is the one part of a busy row that is
// supposed to carry a color.
func (m Model) plainFrame() string {
	return m.styles.Work.Label.Render(m.plain.View())
}

// footerBusy is the busy head of the footer band's hint ladder, empty when the
// pane is idle. Spec section 10.8.4 rule 1: a busy panel replaces the head of
// its ladder with the busy line and keeps the hints that are still live, so the
// band is the only row whose content changes and the body does not reflow when
// the operation lands.
//
// Rule 4, one motion per surface: the footer wins. The branded tier is checked
// first because a drift check and a comment write cannot both be outstanding,
// and if they ever were, the network wait is the one worth announcing.
func (m Model) footerBusy(width int) string {
	if m.brandBusy() {
		// The engine renders nothing while it is unmounted or still inside the
		// birth delay, which is also the settled first paint the determinism
		// contract of spec section 10.2.2 pins. The row falls back to the static
		// label there, so a backgrounded or just-started operation still says
		// what it is doing.
		if frame := m.brand.View(); frame != "" {
			return widget.Truncate(m.styles, frame, width)
		}
		return widget.Busy(m.styles, widget.BusyOpts{
			Label: m.brandLabel(),
			On:    theme.OverlayBand,
			Width: width,
		})
	}
	if m.saving {
		return widget.Busy(m.styles, widget.BusyOpts{
			Frame: m.plainFrame(),
			Label: savingLabel,
			On:    theme.OverlayBand,
			Width: width,
		})
	}
	return ""
}

// sectionBusy is a section's own busy row, used where the footer is already
// describing what the panel as a whole is doing. Spec section 10.8.4 rule 2 puts
// it in the section's first body row; rule 4 strips its frame when the footer is
// already animating, so two things never move at once.
func (m Model) sectionBusy(label string, width int) string {
	frame := m.plainFrame()
	if m.footerBusy(width) != "" {
		frame = ""
	}
	return widget.Busy(m.styles, widget.BusyOpts{
		Frame: frame,
		Label: label,
		On:    theme.OverlaySurf,
		Width: width,
	})
}

// newPlainSpinner is the plain tier of spec section 10.2.4: bubbles' spinner.Dot
// on theme.Styles.Work.Label, whose cadence is the one clock's plain stride.
func newPlainSpinner(styles *theme.Styles) spinner.Model {
	built := spinner.New()
	built.Spinner = styles.Spinner
	built.Style = styles.Work.Label
	return built
}
