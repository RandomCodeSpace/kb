package theme

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// Timing is the clock of spec section 10. Every duration the TUI schedules
// against is named here; nothing under internal/tui writes one inline. Rates
// and tick counts are int; one-shots are time.Duration.
//
// Spec section 10.3.1 is the normative table and this struct is its Go shape.
// Two hazards bound the whole family and both are enforced rather than
// documented: a duration may never be read on a paint path, and every effect
// must collapse to zero. TimingCollapsed is the second one; theme/seam_test.go
// is the first.
type Timing struct {
	FPS         int
	PlainStride int

	BirthDelay      int
	BirthSteps      int
	ScrambleSteps   int
	EllipsisStride  int
	SuffixAfter     int
	BrandBirthSteps int
	// CelebrateSteps is the span of the ship celebration (issue #191). Spec
	// section 10.3.1 names no such token, so this is the twentieth and the one
	// the section's own rule sends back here rather than to a call site. It is
	// a tick count for the same reason BrandBirthSteps is: both index a
	// class-B effect that collapses to nothing at zero.
	CelebrateSteps int

	DialogGraceQuiet           time.Duration
	DialogGraceMax             time.Duration
	DialogGraceReopen          time.Duration
	ScrollActiveLinger         time.Duration
	DoubleClickWindow          time.Duration
	InputCoalesce              time.Duration
	KeyboardNavigationInterval time.Duration
	KeyboardNavigationQuiet    time.Duration
	NoticeTTL                  time.Duration

	PollInterval  time.Duration
	AutoShipDelay time.Duration
	SimilarDelay  time.Duration

	// GeometrySliceTarget is the preferred amount of offscreen layout work per
	// worker command. GeometrySliceLimit is the hard ceiling used to report an
	// overrun. Both are worker budgets, not paint-path timing.
	GeometrySliceTarget time.Duration
	GeometrySliceLimit  time.Duration
}

// DefaultTiming is the table of spec section 10.3.1. crush is the donor for
// every value: each governs when a message is scheduled rather than how a frame
// is drawn, so the donor's cell-buffer architecture does not reach this set.
//
// DialogGraceQuiet is deliberately longer than DoubleClickWindow, so the
// trailing half of a double-click that opened a destructive prompt lands inside
// the grace by construction. Neither may be reduced without the other.
var DefaultTiming = Timing{
	FPS:         20,
	PlainStride: 2,

	BirthDelay:      5,
	BirthSteps:      20,
	ScrambleSteps:   3,
	EllipsisStride:  8,
	SuffixAfter:     60,
	BrandBirthSteps: 12,
	CelebrateSteps:  12,

	DialogGraceQuiet:           425 * time.Millisecond,
	DialogGraceMax:             1500 * time.Millisecond,
	DialogGraceReopen:          500 * time.Millisecond,
	ScrollActiveLinger:         2000 * time.Millisecond,
	DoubleClickWindow:          400 * time.Millisecond,
	InputCoalesce:              16 * time.Millisecond,
	KeyboardNavigationInterval: 50 * time.Millisecond,
	KeyboardNavigationQuiet:    100 * time.Millisecond,
	NoticeTTL:                  5000 * time.Millisecond,

	PollInterval:  1000 * time.Millisecond,
	AutoShipDelay: 350 * time.Millisecond,
	SimilarDelay:  400 * time.Millisecond,

	GeometrySliceTarget: 8 * time.Millisecond,
	GeometrySliceLimit:  16 * time.Millisecond,
}

// TimingCollapsed is the test configuration: the struct zero value. Every
// one-shot fires immediately and no frame clock runs. It is not "fast" timing,
// it is no timing, which is the only kind a golden can assert against.
//
// The rate/count split against the duration split is what makes this work: zero
// means "do not run" to a clock and "run now" to a one-shot, and conflating the
// two produces a test that spins the CPU.
var TimingCollapsed = Timing{}

// Interval is the tick period of the one clock, or 0 when motion is off.
func (t Timing) Interval() time.Duration { return framePeriod(t.FPS) }

// PlainFrame is the tick period of a plain-tier spinner.
func (t Timing) PlainFrame() time.Duration {
	return time.Duration(t.PlainStride) * t.Interval()
}

// celebrateBeats is the number of phases the ship celebration divides its span
// into: lit, dark, lit, dark. Two pulses read as a flourish; one reads as a
// repaint, and a third is a surface asking for attention it has already had.
const celebrateBeats = 4

// CelebrateBeat is the tick length of one phase of the ship celebration, or 0
// when the effect is collapsed. The beat is derived rather than named so the
// span stays the one reviewable number: shortening the flourish shortens every
// phase of it by construction.
func (t Timing) CelebrateBeat() int {
	if t.CelebrateSteps <= 0 {
		return 0
	}
	return max(t.CelebrateSteps/celebrateBeats, 1)
}

func framePeriod(fps int) time.Duration {
	if fps <= 0 {
		return 0
	}
	return time.Second / time.Duration(fps)
}

// Tick schedules msg after d. A non-positive d dispatches immediately instead
// of scheduling: tea.Tick(0, ...) still round-trips through the runtime timer,
// which is a wall-clock dependency wearing a zero. Every tea.Tick call site
// under internal/tui goes through here.
func Tick(d time.Duration, msg tea.Msg) tea.Cmd {
	if d <= 0 {
		return func() tea.Msg { return msg }
	}
	return tea.Tick(d, func(time.Time) tea.Msg { return msg })
}
