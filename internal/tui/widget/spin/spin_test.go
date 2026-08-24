package spin

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// draftLabel is the reference label every fixed-string assertion here is taken
// against: thirteen ASCII columns, so a frame is readable in a failure message.
const draftLabel = "drafting card"

// newEngine is a mounted engine at the reference settings.
func newEngine(t *testing.T, styles *theme.Styles, seed uint64, scramble bool) *Engine {
	t.Helper()
	engine := &Engine{}
	engine.Configure(styles, Settings{Label: draftLabel, Seed: seed, Scramble: scramble}.Fit(styles, 80))
	engine.Start()
	return engine
}

// TestZeroValueEngineRendersNothing is obligation 1 of spec section 10.2.7 and
// the reading of determinism-contract point 2 that makes the existing goldens
// need no edits: a spinner's settled value is absence.
func TestZeroValueEngineRendersNothing(t *testing.T) {
	if (Engine{}).View() != "" {
		t.Fatal("the zero value rendered a frame")
	}
	if (Engine{}).Frame(99) != "" || (Engine{}).Mounted() {
		t.Fatal("the zero value mounted an engine")
	}
	if (&Engine{}).Start() != nil || (&Engine{}).Step(StepMsg{}, true) != nil {
		t.Fatal("the zero value armed a chain")
	}
	// A nil design system is ignored rather than resolved: styles are threaded
	// down from the root and a widget never builds one (spec section 6.2).
	engine := &Engine{}
	engine.Configure(nil, Settings{Label: draftLabel})
	if engine.Mounted() || engine.View() != "" {
		t.Fatal("a nil *Styles configured an engine")
	}
}

// TestFramesAreFixedStrings is obligation 2: the birth delay, the wipe, the
// settled frame and each of the four ellipsis states, asserted as bytes.
func TestFramesAreFixedStrings(t *testing.T) {
	styles := theme.New(true)
	timing := styles.Timing
	engine := newEngine(t, styles, SeedEditorDraft, true)
	settled := timing.BirthDelay + timing.BirthSteps + timing.ScrambleSteps - 1

	for _, test := range []struct {
		step int
		want string
	}{
		{0, ""},
		{timing.BirthDelay - 1, ""},
		{timing.BirthDelay, ".............         "},
		{settled, "drafting card         "},
		{settled + timing.EllipsisStride, "drafting card.        "},
		{settled + 2*timing.EllipsisStride, "drafting card..       "},
		{settled + 3*timing.EllipsisStride, "drafting card...      "},
		{settled + 4*timing.EllipsisStride, "drafting card         "},
	} {
		if got := ansi.Strip(engine.Frame(test.step)); got != test.want {
			t.Fatalf("Frame(%d) = %q, want %q", test.step, got, test.want)
		}
	}

	// Every row is the same width for the life of the mount (spec section
	// 10.4.4): the label, the ellipsis field and the elapsed field.
	if width := engine.Width(); width != len(draftLabel)+EllipsisField+SuffixField {
		t.Fatalf("row width = %d", width)
	}
	if got := ansi.StringWidth(engine.Frame(settled)); got != engine.Width() {
		t.Fatalf("settled frame is %d columns, want %d", got, engine.Width())
	}
	// The frame carries color: it is the one part of a busy row that is
	// supposed to (spec section 10.8.4).
	if !strings.Contains(engine.Frame(settled), "\x1b[") {
		t.Fatal("the branded frame carried no color")
	}
	if got := len(engine.frames); got != timing.BirthSteps+timing.ScrambleSteps+EllipsisStates {
		t.Fatalf("frame cache is %d entries", got)
	}
}

// TestSettledFrameIsStableAcrossAnEllipsisWindow is obligation 3, which is
// determinism-contract point 7 for this widget.
func TestSettledFrameIsStableAcrossAnEllipsisWindow(t *testing.T) {
	styles := theme.New(true)
	timing := styles.Timing
	engine := newEngine(t, styles, SeedEditorDraft, true)
	settled := timing.BirthDelay + timing.BirthSteps + timing.ScrambleSteps - 1
	want := engine.Frame(settled)
	for offset := 1; offset < timing.EllipsisStride; offset++ {
		if got := engine.Frame(settled + offset); got != want {
			t.Fatalf("Frame(settled+%d) moved under tick", offset)
		}
	}
	if engine.Frame(settled+timing.EllipsisStride) == want {
		t.Fatal("the ellipsis never advanced")
	}
}

// TestBirthScheduleIsPerSeedAndReproducible is obligation 4: the seed is a
// named per-surface constant rather than a runtime instance id, which is what
// turns a birth schedule from a seeded reproduction into a fixed golden.
func TestBirthScheduleIsPerSeedAndReproducible(t *testing.T) {
	styles := theme.New(true)
	timing := styles.Timing
	mid := timing.BirthDelay + 6

	draft := newEngine(t, styles, SeedEditorDraft, true)
	propose := newEngine(t, styles, SeedAdrPropose, true)
	if draft.Frame(mid) == propose.Frame(mid) {
		t.Fatal("two seeds staggered identically")
	}
	again := newEngine(t, styles, SeedEditorDraft, true)
	if draft.Frame(mid) != again.Frame(mid) {
		t.Fatal("one seed did not reproduce its schedule across two constructions")
	}

	// NoScramble keeps the wipe and drops the flash: a born column wears its
	// own rune straight away (crush's anim.go:115).
	plain := newEngine(t, styles, SeedEditorDraft, false)
	frame := ansi.Strip(plain.Frame(mid))
	if !strings.ContainsAny(frame, "dgc") {
		t.Fatalf("NoScramble frame carried no label runes: %q", frame)
	}
	for _, letter := range frame[:len(draftLabel)] {
		if !strings.ContainsRune(draftLabel+BirthGlyph, letter) {
			t.Fatalf("NoScramble frame flashed a scramble rune: %q", frame)
		}
	}
}

// TestFrameCacheRebuildsOnlyOnAChangedHash is obligation 5.
func TestFrameCacheRebuildsOnlyOnAChangedHash(t *testing.T) {
	styles := theme.New(true)
	settings := Settings{Label: draftLabel, Seed: SeedEditorDraft, Scramble: true}.Fit(styles, 80)
	engine := &Engine{}
	engine.Configure(styles, settings)
	engine.Configure(styles, settings)
	if engine.builds != 1 {
		t.Fatalf("a repeated settings hash rebuilt the cache %d times", engine.builds)
	}
	// A dynamic suffix is outside the hash: a func has no stable identity, so
	// swapping one may not cost a rebuild.
	withSuffix := settings
	withSuffix.Suffix = func() string { return "1s" }
	engine.Configure(styles, withSuffix)
	if engine.builds != 1 {
		t.Fatalf("the suffix closure was hashed: %d builds", engine.builds)
	}
	// A resize that changes the label width does.
	engine.Configure(styles, Settings{Label: draftLabel, Seed: SeedEditorDraft, Scramble: true}.Fit(styles, 16))
	if engine.builds != 2 {
		t.Fatalf("a width change rebuilt the cache %d times", engine.builds)
	}
	// So does a theme rebuild.
	engine.Configure(theme.New(false), settings)
	if engine.builds != 3 {
		t.Fatalf("a theme rebuild rebuilt the cache %d times", engine.builds)
	}
}

// TestStepDropsForeignAndStaleTicks is obligation 6 and the double-chain fix of
// spec section 10.2.5: a restart while an old chain is in flight must not leave
// two tickers running.
func TestStepDropsForeignAndStaleTicks(t *testing.T) {
	styles := theme.New(true)
	engine := newEngine(t, styles, SeedEditorDraft, true)
	live := StepMsg{Seed: SeedEditorDraft, Gen: engine.Gen()}
	if engine.Seed() != SeedEditorDraft {
		t.Fatal("the engine forgot its seed")
	}
	if engine.Step(StepMsg{Seed: SeedAdrPropose, Gen: live.Gen}, true) != nil {
		t.Fatal("a foreign seed advanced the chain")
	}
	if engine.Step(StepMsg{Seed: SeedEditorDraft, Gen: live.Gen - 1}, true) != nil {
		t.Fatal("a stale generation advanced the chain")
	}
	if engine.Step(live, true) == nil {
		t.Fatal("a live tick terminated the chain")
	}

	// Start bumps the generation, so the outstanding tick of the old chain is
	// dropped on arrival rather than doubling the frame rate.
	engine.Start()
	if engine.Step(live, true) != nil {
		t.Fatal("a restarted engine kept its old chain")
	}

	// The gate owns the chain: a settled surface terminates it and unmounts.
	fresh := StepMsg{Seed: SeedEditorDraft, Gen: engine.Gen()}
	if engine.Step(fresh, false) != nil || engine.Mounted() {
		t.Fatal("a settled gate kept the chain alive")
	}
	// Stop bumps again, so a tick still in flight terminates too.
	engine.Start()
	stopped := StepMsg{Seed: SeedEditorDraft, Gen: engine.Gen()}
	engine.Stop()
	if engine.Step(stopped, true) != nil || engine.Mounted() || engine.View() != "" {
		t.Fatal("Stop left the chain running")
	}
}

// TestBelowFullFidelityTheWipeIsNeverArmed is obligation 7. The wipe is a
// class-B effect of spec section 10.7.6: its information lives in the color
// difference between born and unborn cells, so a flattened wipe is a flash of
// punctuation and the engine mounts settled instead.
func TestBelowFullFidelityTheWipeIsNeverArmed(t *testing.T) {
	full := theme.New(true)
	settled := full.Timing.BirthDelay + full.Timing.BirthSteps + full.Timing.ScrambleSteps - 1

	for _, profile := range []colorprofile.Profile{colorprofile.ANSI256, colorprofile.ANSI, colorprofile.ASCII} {
		styles := theme.NewFor(true, profile)
		engine := newEngine(t, styles, SeedEditorDraft, true)
		if engine.step != settled {
			t.Fatalf("%v mounted at step %d, want the settled %d", profile, engine.step, settled)
		}
		if got, want := ansi.Strip(engine.View()), ansi.Strip(engine.Frame(settled)); got != want {
			t.Fatalf("%v mounted at %q, want %q", profile, got, want)
		}
	}
	graded := newEngine(t, theme.NewFor(true, colorprofile.TrueColor), SeedEditorDraft, true)
	if graded.step != 0 {
		t.Fatalf("truecolor mounted at step %d, want 0", graded.step)
	}
}

// TestSuffixSuppressesTheEllipsis is the dynamic suffix of spec section 10.2.5:
// two things never move at once, so before the counter appears the ellipsis is
// the motion and after it, the counter is.
func TestSuffixSuppressesTheEllipsis(t *testing.T) {
	styles := theme.New(true)
	timing := styles.Timing
	elapsed := time.Duration(0)
	engine := &Engine{}
	engine.Configure(styles, Settings{
		Label:    draftLabel,
		Seed:     SeedEditorDraft,
		Scramble: true,
		Suffix:   func() string { return Elapsed(elapsed) },
	}.Fit(styles, 80))
	engine.Start()
	settled := timing.BirthDelay + timing.BirthSteps + timing.ScrambleSteps - 1

	// Before SuffixAfter ticks past settle the field is reserved but empty and
	// the ellipsis is the motion.
	if got := ansi.Strip(engine.Frame(settled + timing.EllipsisStride)); got != "drafting card.        " {
		t.Fatalf("early suffix frame = %q", got)
	}
	elapsed = 62 * time.Second
	late := settled + timing.SuffixAfter
	if got := ansi.Strip(engine.Frame(late)); got != "drafting card    1m02s" {
		t.Fatalf("late suffix frame = %q", got)
	}
	// The ellipsis stays pinned to the empty state for the whole window.
	for offset := 1; offset < timing.EllipsisStride; offset++ {
		if got := ansi.Strip(engine.Frame(late + offset)); got != "drafting card    1m02s" {
			t.Fatalf("suffix frame at +%d = %q", offset, got)
		}
	}
	if got := ansi.StringWidth(engine.Frame(late)); got != engine.Width() {
		t.Fatalf("the suffix reflowed the row: %d columns", got)
	}
}

// TestElapsedFormats is the suffix format of spec section 10.2.5.
func TestElapsedFormats(t *testing.T) {
	for _, test := range []struct {
		age  time.Duration
		want string
	}{
		{-time.Second, ""},
		{0, "0s"},
		{12 * time.Second, "12s"},
		{59*time.Second + 999*time.Millisecond, "59s"},
		{62 * time.Second, "1m02s"},
		{11*time.Minute + 11*time.Second, "11m11s"},
		{59*time.Minute + 59*time.Second, "59m59s"},
		{time.Hour, "59m+"},
		{9 * time.Hour, "59m+"},
	} {
		if got := Elapsed(test.age); got != test.want {
			t.Fatalf("Elapsed(%v) = %q, want %q", test.age, got, test.want)
		}
	}
	// A suffix longer than the reserved field is cut rather than allowed to
	// reflow the row (spec section 10.4.4).
	styles := theme.New(true)
	if got := ansi.StringWidth(suffixRun(styles, "1234567890")); got != SuffixField {
		t.Fatalf("an overlong suffix took %d columns", got)
	}
}

// TestFitTruncatesOntoTheRow is the row arithmetic of spec section 10.2.5: the
// label takes min(MaxLabelW, avail - EllipsisField - SuffixField).
func TestFitTruncatesOntoTheRow(t *testing.T) {
	styles := theme.New(true)
	long := strings.Repeat("x", 90)
	wide := (Settings{Label: long}).Fit(styles, 200)
	if wide.Width != MaxLabelW {
		t.Fatalf("a long label fitted to %d columns, want %d", wide.Width, MaxLabelW)
	}
	narrow := (Settings{Label: long}).Fit(styles, 20)
	if narrow.Width != 20-EllipsisField-SuffixField {
		t.Fatalf("a narrow row fitted the label to %d columns", narrow.Width)
	}
	if !strings.HasSuffix(narrow.Label, styles.Glyph.Ellipsis) {
		t.Fatalf("a truncated label lost its ellipsis mark: %q", narrow.Label)
	}
	// A row with no room at all renders the reserved fields and nothing else.
	empty := &Engine{}
	empty.Configure(styles, Settings{Label: long, Seed: SeedEditorDraft}.Fit(styles, 1))
	empty.Start()
	if got := ansi.StringWidth(empty.Frame(styles.Timing.BirthDelay)); got != EllipsisField+SuffixField {
		t.Fatalf("an empty label rendered %d columns", got)
	}
}

// TestCollapsedTimingRunsNoClock is the rate/count split of spec section
// 10.3.9: zero means "do not run" to a clock, and a chain that dispatched a
// zero interval immediately would spin the CPU instead of animating.
func TestCollapsedTimingRunsNoClock(t *testing.T) {
	styles := theme.NewWith(true, theme.TimingCollapsed)
	engine := &Engine{}
	engine.Configure(styles, Settings{Label: draftLabel, Seed: SeedEditorDraft, Scramble: true}.Fit(styles, 80))
	if engine.Start() != nil {
		t.Fatal("a collapsed clock armed a tick")
	}
	if !engine.Mounted() {
		t.Fatal("a collapsed clock left the engine unmounted")
	}
	if got := ansi.Strip(engine.View()); got != "drafting card         " {
		t.Fatalf("a collapsed clock rendered %q", got)
	}
	if engine.Step(StepMsg{Seed: SeedEditorDraft, Gen: engine.Gen()}, true) != nil {
		t.Fatal("a collapsed clock re-armed the chain")
	}
}

// TestStepSchedulesAgainstTheOneClock keeps the engine on theme.Tick rather
// than a second authored interval (spec section 10.2.1).
func TestStepSchedulesAgainstTheOneClock(t *testing.T) {
	styles := theme.New(true)
	engine := newEngine(t, styles, SeedEditorDraft, true)
	command := engine.Step(StepMsg{Seed: SeedEditorDraft, Gen: engine.Gen()}, true)
	if command == nil {
		t.Fatal("a live tick terminated the chain")
	}
	var scheduled tea.Cmd = command
	message, ok := scheduled().(StepMsg)
	if !ok {
		t.Fatalf("the chain scheduled %T", scheduled())
	}
	if message.Seed != SeedEditorDraft || message.Gen != engine.Gen() {
		t.Fatalf("the chain scheduled %+v", message)
	}
}
