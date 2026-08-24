// Package spin is the branded spinner engine of spec section 10.2.5: the
// gradient label that wipes in column by column while kb waits on a network
// round trip or a model inference.
//
// It is the branded half of the two-tier split of spec section 10.2.4. The
// plain half is bubbles' spinner.Dot on theme.Styles.Work.Label and needs no
// code here: a branded frame announces that the machine is thinking on your
// behalf and may be a while, so spending it on a local disk write devalues it.
//
// The engine obeys the determinism contract of spec section 10.2.2 without
// exception. It reads no clock - the elapsed suffix arrives through a closure
// the surface builds from its own injected now - every frame is rendered once
// at construction and indexed afterwards, and every tick chain carries a
// generation so a restart cannot leave two tickers running.
package spin

import (
	"hash/fnv"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
	"github.com/RandomCodeSpace/kb/internal/tui/widget"
)

// The engine vocabulary of spec section 10.2.5. Every duration and tick count
// the engine reads is a theme.Timing token instead; nothing here is a clock.
const (
	EllipsisStates = 4  // "", ".", "..", "..."
	EllipsisField  = 3  // columns the ellipsis always occupies, space padded
	SuffixField    = 6  // columns reserved for " 1m02s"
	MaxLabelW      = 48 // longest branded label; longer is truncated
	MaxEngines     = 1  // branded engines that may tick at once (section 10.2.6)

	// BirthGlyph is the pre-birth cell, drawn in theme.Styles.Work.Birth.
	BirthGlyph = "."

	// ScrambleRunes is the 36-rune alphabet a cell flashes through between its
	// birth step and its own rune. ASCII only, one column each.
	ScrambleRunes = "abcdefghijklmnopqrstuvwxyz0123456789"
)

// The per-surface birth seeds of spec section 10.2.4. Each is a named constant
// rather than a runtime instance id, which is what makes a birth schedule a
// fixed golden instead of a seeded reproduction: two surfaces stagger
// differently, and the same surface staggers identically in every process.
const (
	SeedEditorDraft  uint64 = 1 // card editor, AI draft
	SeedDriftCheck   uint64 = 2 // card detail, drift check
	SeedAdrPropose   uint64 = 3 // ADR split, propose stories
	SeedImportFetch  uint64 = 4 // issue import, source list and preview fetch
	SeedSettingsTest uint64 = 5 // settings, test connection
)

// ellipsisStates are the four settled states, indexed by ellipsis step.
var ellipsisStates = [EllipsisStates]string{"", ".", "..", "..."}

// StepMsg advances one engine by one tick of the one clock.
//
// Seed identifies the engine because a single Update tree routes ticks for more
// than one of them - the front overlay's, and during the background handoff of
// spec section 10.2.6 a backgrounded surface's last outstanding tick. Gen is
// the double-chain fix of spec section 10.2.5: Start and Stop both bump it, so
// a tick issued by a superseded chain is dropped on arrival instead of doubling
// the frame rate.
type StepMsg struct {
	Seed uint64
	Gen  uint32
}

// Settings is the hashed description of one mount. These are all of its fields.
//
// Suffix is deliberately outside the hash: a func has no stable identity, and
// the suffix is appended live to a cached frame rather than baked into one.
type Settings struct {
	Label    string        // the branded label, already truncated by Fit
	Width    int           // resolved label width in columns, set by Fit
	Seed     uint64        // the per-surface constant above
	Scramble bool          // false is crush's NoScramble: the wipe without the flash
	Suffix   func() string // elapsed text, built by the surface from its injected now
}

// Fit truncates Label onto the row that avail columns can hold and resolves
// Width. The label takes min(MaxLabelW, avail - EllipsisField - SuffixField)
// columns, because the ellipsis and the elapsed fields are reserved for the
// life of the mount whether or not they are showing (spec section 10.4.4).
func (s Settings) Fit(styles *theme.Styles, avail int) Settings {
	limit := MaxLabelW
	if room := avail - EllipsisField - SuffixField; room < limit {
		limit = room
	}
	if limit < 0 {
		limit = 0
	}
	s.Label = widget.Truncate(styles, s.Label, limit)
	s.Width = ansi.StringWidth(s.Label)
	return s
}

// hash is the FNV-1a 64 cache key of spec section 10.2.5. stdlib rather than
// xxh3 for one reason beyond dependency hygiene: the same hash seeds the birth
// schedule, so it has to be stable across processes or a synchronous frame
// assertion would differ run to run.
func (s Settings) hash() uint64 {
	sum := fnv.New64a()
	_, _ = sum.Write([]byte(s.Label))
	var scalars [17]byte
	putUint64(scalars[0:8], uint64(s.Width))
	putUint64(scalars[8:16], s.Seed)
	if s.Scramble {
		scalars[16] = 1
	}
	_, _ = sum.Write(scalars[:])
	return sum.Sum64()
}

// putUint64 writes value big-endian, so the key does not depend on the host.
func putUint64(out []byte, value uint64) {
	for index := range out {
		out[index] = byte(value >> (8 * (len(out) - 1 - index)))
	}
}

// Engine is one branded spinner. The zero value renders the empty string and
// arms nothing, which is the settled state of a spinner: a surface that is not
// busy shows its ordinary static text with no engine output in it at all. That
// is why the existing goldens need no edits (spec section 10.2.2, point 2).
//
// At most MaxEngines of these tick at once and it belongs to the front-most
// open surface in the spec section 4 z-order; see Stop and Start.
type Engine struct {
	styles   *theme.Styles
	settings Settings
	frames   []string
	key      uint64
	builds   int
	gen      uint32
	step     int
	mounted  bool
	graded   bool
}

// Configure resolves the styles and settings of one mount, rebuilding the frame
// cache when - and only when - the settings hash or the theme instance changed.
// A rebuild happens on a theme rebuild, on a resize that changes the label
// width, and on a new operation with a different label; nothing else.
//
// The cache is per engine. Spec section 6.2 forbids package-level mutable style
// state and a shared frame map is close enough to it to refuse, which the
// MaxEngines ceiling makes free anyway.
func (e *Engine) Configure(styles *theme.Styles, settings Settings) {
	if styles == nil {
		return
	}
	key := settings.hash()
	if e.styles == styles && e.frames != nil && e.key == key {
		e.settings.Suffix = settings.Suffix
		return
	}
	e.styles = styles
	e.settings = settings
	e.key = key
	e.graded = styles.Graded()
	e.frames = buildFrames(styles, settings, key)
	e.builds++
}

// Start mounts the engine at step zero and opens a fresh tick chain. The
// generation bump is what kills any chain still in flight.
//
// Below FidelityFull the wipe is never armed: it is a class-B effect (spec
// section 10.7.6), its information lives in the color difference between born
// and unborn cells, and a flattened wipe is a flash of punctuation. The engine
// mounts settled instead, where the ellipsis and the elapsed suffix - carried
// by glyph and text, not by hue - go on running unchanged.
func (e *Engine) Start() tea.Cmd {
	if e.styles == nil || len(e.frames) == 0 {
		return nil
	}
	e.gen++
	e.mounted = true
	e.step = 0
	if !e.graded {
		e.step = e.settledStep()
	}
	return e.tick()
}

// Step advances the chain by one tick. busy is the surface's own gate, read on
// every tick rather than mirrored into an animating flag a code path can forget
// to clear (spec section 10.2.3).
//
// A nil command is returned - and the chain therefore terminates - for a stale
// generation, a foreign seed, an unmounted engine, or a settled gate.
func (e *Engine) Step(msg StepMsg, busy bool) tea.Cmd {
	if !e.mounted || msg.Seed != e.settings.Seed || msg.Gen != e.gen {
		return nil
	}
	if !busy {
		e.Stop()
		return nil
	}
	e.step++
	return e.tick()
}

// Stop clears the mount and bumps the generation, so a tick still in flight is
// dropped on arrival. The backgrounded surface of spec section 10.2.6 calls
// this: its operation keeps running, only the animation stops.
func (e *Engine) Stop() {
	e.gen++
	e.mounted = false
	e.step = 0
}

// Mounted reports whether this engine is ticking. The root counts these across
// the open z-order stack against MaxEngines.
func (e Engine) Mounted() bool { return e.mounted }

// Gen is the current generation, for a caller that has to build a StepMsg by
// hand - a test stepping the chain synchronously.
func (e Engine) Gen() uint32 { return e.gen }

// Seed is the engine's per-surface constant.
func (e Engine) Seed() uint64 { return e.settings.Seed }

// Width is the fixed column cost of the assembled row: the label, the ellipsis
// field and the elapsed field, reserved for the life of the mount.
func (e Engine) Width() int { return e.settings.Width + EllipsisField + SuffixField }

// View is the assembled row at the current step, or the empty string while the
// engine is unmounted or still inside the birth delay. A surface that gets ""
// renders its ordinary static busy label instead.
//
// The run carries no background; the caller lays it onto its own shade tier
// with theme.Styles.SurfaceRun.
func (e Engine) View() string { return e.Frame(e.step) }

// Frame is the assembled row at an absolute step count s, ticks since the chain
// started. It is the whole render path, so a test asserts a fixed string
// against it without running a clock.
func (e Engine) Frame(step int) string {
	if !e.mounted || e.styles == nil || len(e.frames) == 0 {
		return ""
	}
	timing := e.styles.Timing
	if step < timing.BirthDelay {
		return ""
	}
	born := step - timing.BirthDelay
	settle := e.settleAt()
	suffix := e.suffixText(born, settle)
	index := born
	if born >= settle {
		index = settle + 1 + e.ellipsisStep(born, settle, suffix)
	}
	return e.frames[index] + suffixRun(e.styles, suffix)
}

// settleAt is the born-step at which every column wears its own rune.
func (e Engine) settleAt() int { return bornFrames(e.styles.Timing) - 1 }

// settledStep is the absolute step of the settled frame, which is where the
// engine mounts when the wipe is suppressed.
func (e Engine) settledStep() int { return e.styles.Timing.BirthDelay + e.settleAt() }

// ellipsisStep is the settled frame's ellipsis state. While the suffix is
// showing it is pinned to the empty state: two things never move at once, so
// before the counter appears the ellipsis is the motion and after it, the
// counter is (spec section 10.2.5).
func (e Engine) ellipsisStep(born, settle int, suffix string) int {
	stride := e.styles.Timing.EllipsisStride
	if suffix != "" || stride <= 0 {
		return 0
	}
	return ((born - settle) / stride) % EllipsisStates
}

// suffixText is the elapsed field's contents: empty until SuffixAfter ticks
// past settle, then whatever the surface's closure reports.
func (e Engine) suffixText(born, settle int) string {
	if e.settings.Suffix == nil || born < settle+e.styles.Timing.SuffixAfter {
		return ""
	}
	return e.settings.Suffix()
}

// Elapsed formats an operation's age for the suffix field: 12s below one
// minute, 1m02s at or above it, 59m+ at or above an hour. The engine never
// reads a clock itself; the surface passes the duration in.
func Elapsed(age time.Duration) string {
	seconds := age.Milliseconds() / 1000
	switch {
	case seconds < 0:
		return ""
	case seconds < 60:
		return strconv.FormatInt(seconds, 10) + "s"
	case seconds >= 3600:
		return "59m+"
	default:
		rest := seconds % 60
		pad := ""
		if rest < 10 {
			pad = "0"
		}
		return strconv.FormatInt(seconds/60, 10) + "m" + pad + strconv.FormatInt(rest, 10) + "s"
	}
}

// tick schedules the next step, or terminates the chain when the clock is off.
// A zero interval means "do not run" to a clock (spec section 10.3.9), and
// dispatching it immediately instead would spin the CPU.
func (e Engine) tick() tea.Cmd {
	interval := e.styles.Timing.Interval()
	if interval <= 0 {
		return nil
	}
	return theme.Tick(interval, StepMsg{Seed: e.settings.Seed, Gen: e.gen})
}

// bornFrames is the number of birth frames: every column has drawn its birth
// step and spent its scramble by the last one. At least one, so a collapsed
// clock mounts straight onto a settled frame instead of an empty array.
func bornFrames(timing theme.Timing) int {
	frames := timing.BirthSteps + timing.ScrambleSteps
	if frames < 1 {
		return 1
	}
	return frames
}

// buildFrames renders every frame of one mount exactly once. Spec section
// 10.3.2 is normative: a motion surface prerenders its frames at construction
// and indexes them per tick, because a kb tick re-runs the whole board render
// and there is no cell buffer to blit into. Every subsequent frame is one array
// index and one concatenation.
//
// The worst case is Width = MaxLabelW: 23 birth frames x 48 styled cells, once
// per mount.
func buildFrames(styles *theme.Styles, settings Settings, key uint64) []string {
	timing := styles.Timing
	born := bornFrames(timing)
	frames := make([]string, 0, born+EllipsisStates)
	clusters := clustersOf(settings.Label)

	// The PRNG is seeded from the settings hash alone, so the schedule and the
	// scramble runes reproduce exactly in every process (spec section 10.2.5).
	prng := rand.New(rand.NewPCG(key, 0))
	schedule := make([]int, len(clusters))
	span := timing.BirthSteps
	if span < 1 {
		span = 1
	}
	for column := range schedule {
		schedule[column] = prng.IntN(span)
	}

	labels := make([]string, born)
	for step := range born {
		labels[step] = labelFrame(styles, settings, clusters, schedule, prng, step)
	}
	// Indices 0..born-1 are the birth frames, each carrying an empty ellipsis
	// field; the last of them is fully born and is the base of the four settled
	// frames that follow, one per ellipsis state.
	for _, label := range labels {
		frames = append(frames, label+ellipsisRun(styles, settings, ""))
	}
	for state := range EllipsisStates {
		frames = append(frames, labels[born-1]+ellipsisRun(styles, settings, ellipsisStates[state]))
	}
	return frames
}

// labelFrame renders the label at one born-step, padded to the resolved width
// so the row never reflows.
func labelFrame(styles *theme.Styles, settings Settings, clusters []string, schedule []int, prng *rand.Rand, step int) string {
	var row strings.Builder
	painted := 0
	for column, cluster := range clusters {
		switch {
		case step < schedule[column]:
			row.WriteString(styles.Work.Birth.Render(BirthGlyph))
			painted++
		case settings.Scramble && step < schedule[column]+styles.Timing.ScrambleSteps:
			flash := string(ScrambleRunes[prng.IntN(len(ScrambleRunes))])
			row.WriteString(styles.GradCell(theme.GradWork, column, len(clusters), flash))
			painted++
		default:
			row.WriteString(styles.GradCell(theme.GradWork, column, len(clusters), cluster))
			painted += ansi.StringWidth(cluster)
		}
	}
	return row.String() + spaces(settings.Width-painted)
}

// ellipsisRun renders the three-column ellipsis field. The dots continue the
// label's run and wear its ramp tail rather than a color of their own.
func ellipsisRun(styles *theme.Styles, settings Settings, dots string) string {
	if dots == "" {
		return spaces(EllipsisField)
	}
	width := max(settings.Width, 1)
	return styles.GradCell(theme.GradWork, width-1, width, dots) + spaces(EllipsisField-len(dots))
}

// suffixRun renders the elapsed field, which occupies SuffixField columns
// whether or not it is present. The leading space is one of them.
func suffixRun(styles *theme.Styles, suffix string) string {
	if suffix == "" {
		return styles.Work.Suffix.Render(strings.Repeat(" ", SuffixField))
	}
	text := " " + suffix
	if len(text) > SuffixField {
		text = text[:SuffixField]
	}
	return styles.Work.Suffix.Render(text + strings.Repeat(" ", SuffixField-len(text)))
}

// spaces renders count blank columns carrying no style of their own, so the
// reserved fields inherit whatever surface the caller laid the row onto rather
// than opening an SGR run that would have to be closed again.
func spaces(count int) string {
	if count <= 0 {
		return ""
	}
	return strings.Repeat(" ", count)
}

// clustersOf splits a label into grapheme clusters before indexing, per spec
// section 10.1.1: rune splitting recolors the inside of an emoji sequence and
// puts an SGR change between a base character and its combining mark.
func clustersOf(label string) []string {
	if label == "" {
		return nil
	}
	out := make([]string, 0, len(label))
	iterator := uniseg.NewGraphemes(label)
	for iterator.Next() {
		out = append(out, iterator.Str())
	}
	return out
}
