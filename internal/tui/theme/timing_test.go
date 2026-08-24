package theme_test

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// TestDefaultTimingTable pins every token of spec section 10.3.1. The table is
// normative: a value that changes here is a design change, not a refactor.
func TestDefaultTimingTable(t *testing.T) {
	timing := theme.DefaultTiming
	counts := []struct {
		name string
		got  int
		want int
	}{
		{"FPS", timing.FPS, 20},
		{"PlainStride", timing.PlainStride, 2},
		{"BirthDelay", timing.BirthDelay, 5},
		{"BirthSteps", timing.BirthSteps, 20},
		{"ScrambleSteps", timing.ScrambleSteps, 3},
		{"EllipsisStride", timing.EllipsisStride, 8},
		{"SuffixAfter", timing.SuffixAfter, 60},
		{"BrandBirthSteps", timing.BrandBirthSteps, 12},
		{"CelebrateSteps", timing.CelebrateSteps, 12},
	}
	for _, item := range counts {
		if item.got != item.want {
			t.Errorf("%s = %d, want %d", item.name, item.got, item.want)
		}
	}
	durations := []struct {
		name string
		got  time.Duration
		want time.Duration
	}{
		{"DialogGraceQuiet", timing.DialogGraceQuiet, 425 * time.Millisecond},
		{"DialogGraceMax", timing.DialogGraceMax, 1500 * time.Millisecond},
		{"DialogGraceReopen", timing.DialogGraceReopen, 500 * time.Millisecond},
		{"ScrollActiveLinger", timing.ScrollActiveLinger, 2000 * time.Millisecond},
		{"DoubleClickWindow", timing.DoubleClickWindow, 400 * time.Millisecond},
		{"InputCoalesce", timing.InputCoalesce, 16 * time.Millisecond},
		{"NoticeTTL", timing.NoticeTTL, 5000 * time.Millisecond},
		{"PollInterval", timing.PollInterval, time.Second},
		{"AutoShipDelay", timing.AutoShipDelay, 350 * time.Millisecond},
		{"SimilarDelay", timing.SimilarDelay, 400 * time.Millisecond},
	}
	for _, item := range durations {
		if item.got != item.want {
			t.Errorf("%s = %v, want %v", item.name, item.got, item.want)
		}
	}
}

// TestGraceQuietOutlivesDoubleClick pins the relationship spec section 10.3.3
// calls load-bearing: the trailing half of a double-click that opened a
// destructive prompt has to land inside the grace by construction.
func TestGraceQuietOutlivesDoubleClick(t *testing.T) {
	timing := theme.DefaultTiming
	if timing.DialogGraceQuiet <= timing.DoubleClickWindow {
		t.Fatalf("DialogGraceQuiet %v must exceed DoubleClickWindow %v",
			timing.DialogGraceQuiet, timing.DoubleClickWindow)
	}
	if timing.DialogGraceMax <= timing.DialogGraceQuiet {
		t.Fatalf("DialogGraceMax %v must exceed DialogGraceQuiet %v",
			timing.DialogGraceMax, timing.DialogGraceQuiet)
	}
}

func TestIntervalDerivesFromFPS(t *testing.T) {
	if got := theme.DefaultTiming.Interval(); got != 50*time.Millisecond {
		t.Fatalf("Interval() = %v, want 50ms", got)
	}
	if got := theme.DefaultTiming.PlainFrame(); got != 100*time.Millisecond {
		t.Fatalf("PlainFrame() = %v, want 100ms", got)
	}
}

// TestCelebrateBeatDividesItsSpan pins the derived half of the ship
// celebration's timing (issue #191): the span is the one reviewable number and
// the beat falls out of it, so shortening the flourish shortens every phase.
func TestCelebrateBeatDividesItsSpan(t *testing.T) {
	if got := theme.DefaultTiming.CelebrateBeat(); got != 3 {
		t.Fatalf("CelebrateBeat() = %d, want 3", got)
	}
	if got := theme.TimingCollapsed.CelebrateBeat(); got != 0 {
		t.Fatalf("collapsed CelebrateBeat() = %d, want 0", got)
	}
	if got := (theme.Timing{CelebrateSteps: -1}).CelebrateBeat(); got != 0 {
		t.Fatalf("negative CelebrateBeat() = %d, want 0", got)
	}
	// A span too short to divide still beats once rather than never: a floor of
	// zero would be a lit phase that never ends.
	for span := 1; span <= 4; span++ {
		if got := (theme.Timing{CelebrateSteps: span}).CelebrateBeat(); got != 1 {
			t.Fatalf("span %d: CelebrateBeat() = %d, want 1", span, got)
		}
	}
}

// TestCollapsedTimingStopsTheClock is the half of the collapse rule that keeps
// a collapsed spinner from busy-looping: zero means "do not run" to a clock.
func TestCollapsedTimingStopsTheClock(t *testing.T) {
	if got := theme.TimingCollapsed.Interval(); got != 0 {
		t.Fatalf("collapsed Interval() = %v, want 0", got)
	}
	if got := theme.TimingCollapsed.PlainFrame(); got != 0 {
		t.Fatalf("collapsed PlainFrame() = %v, want 0", got)
	}
	if (theme.Timing{FPS: -1}).Interval() != 0 {
		t.Fatal("a negative FPS must resolve to a stopped clock")
	}
}

type tickProbe struct{ id int }

// TestTickCollapsesToImmediateDispatch is the other half: zero means "run now"
// to a one-shot, and the command must not round-trip through the runtime timer.
func TestTickCollapsesToImmediateDispatch(t *testing.T) {
	for _, collapsed := range []time.Duration{0, -time.Second} {
		command := theme.Tick(collapsed, tickProbe{id: 7})
		if command == nil {
			t.Fatalf("Tick(%v) returned no command", collapsed)
		}
		message, ok := command().(tickProbe)
		if !ok || message.id != 7 {
			t.Fatalf("Tick(%v) dispatched %#v, want tickProbe{7}", collapsed, message)
		}
	}
}

// TestTickSchedulesRealDurations covers the scheduled path with the shortest
// duration the runtime can express. There is no delay to wait out - the timer
// is already due when the command runs - so the assertion is on delivery, not
// on elapsed wall clock.
func TestTickSchedulesRealDurations(t *testing.T) {
	var command tea.Cmd = theme.Tick(time.Nanosecond, tickProbe{id: 3})
	if command == nil {
		t.Fatal("Tick returned no command")
	}
	message, ok := command().(tickProbe)
	if !ok || message.id != 3 {
		t.Fatalf("scheduled Tick dispatched %#v, want tickProbe{3}", message)
	}
	if theme.Tick(time.Hour, tickProbe{id: 4}) == nil {
		t.Fatal("a long Tick returned no command")
	}
}

// TestStylesCarryInjectedTiming pins the injection rule of spec section 10.3.9:
// the timing set arrives through the cached factory and reaches the dimmed
// variant, so an overlay and the board behind it share one clock.
func TestStylesCarryInjectedTiming(t *testing.T) {
	styles := theme.New(true)
	if styles.Timing != theme.DefaultTiming {
		t.Fatal("New must carry DefaultTiming")
	}
	if styles.Dimmed == nil || styles.Dimmed.Timing != theme.DefaultTiming {
		t.Fatal("the dimmed variant must carry the same timing")
	}
	collapsed := theme.NewWith(false, theme.TimingCollapsed)
	if collapsed.Timing != theme.TimingCollapsed {
		t.Fatal("NewWith must carry the injected timing")
	}
	if collapsed.Dimmed == nil || collapsed.Dimmed.Timing != theme.TimingCollapsed {
		t.Fatal("NewWith must carry the injected timing onto the dimmed variant")
	}
}
