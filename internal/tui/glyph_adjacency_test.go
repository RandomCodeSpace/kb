package tui

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/text/width"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// The Block Elements range. Spec section 10.4.1 classifies these as East Asian
// Ambiguous like the rest of the vocabulary, but they are exempt from the
// adjacency rule: a rail that does not touch its card, a pill cap that does not
// touch its text and a half-block letterform that does not touch the row above
// it are not the same primitive any more, so a separating column would not fix
// them, it would delete them. Their cell alignment is the block-glyph risk
// section 3.6 already accepts, and a font that drew them at two cells would
// break every rail, cap, meter and brand mark at once rather than smudging one
// letter.
const (
	blockElementsLo = 0x2580
	blockElementsHi = 0x259F
)

// unpromotedMarks are the section 10.4.1 glyphs that are still written as
// literals at their render site rather than named as theme.Glyphs tokens
// (Bullet, Times, EmDash). They are display vocabulary and so they answer to
// the adjacency rule whether or not the token that will own them exists yet.
var unpromotedMarks = []rune{'·', '×', '—'}

// spacedMarks is the vocabulary the adjacency rule of spec section 10.4.1
// binds: every glyph token whose rune is East Asian Ambiguous or East Asian
// Wide, less the Block Elements carve-out above.
//
// Ambiguous marks are bound because a font may draw them wider than the one
// cell every width calculation gives them. Wide marks - the compact blocked
// mark and the effort squares of issue #223 - are bound for the mirror reason:
// the width is honest at two cells, but a pictograph is drawn by a font that
// frequently ignores the cell grid entirely, and an emoji-less font substitutes
// tofu of whatever width it has. Either way the column after the mark is the
// one that gets overdrawn, and either way the fix is the same: the mark owns it.
//
// The rule is scoped to the vocabulary rather than to every ambiguous or wide
// rune on the screen on purpose. Ambiguity is a property of a lot of ordinary
// text - every accented Latin letter is East Asian Ambiguous - and a card title
// may legitimately carry CJK or an emoji of its own, so a walk over all of it
// would fail on a card titled "cafes" spelled properly. What kb controls, and
// what this test can therefore hold, is the marks kb writes itself.
func spacedMarks(t *testing.T) map[rune]string {
	t.Helper()
	marks := map[rune]string{}
	glyphs := reflect.ValueOf(theme.New(true).Glyph)
	for index := range glyphs.NumField() {
		name := glyphs.Type().Field(index).Name
		runes := []rune(glyphs.Field(index).String())
		if len(runes) != 1 {
			continue
		}
		marks[runes[0]] = name
	}
	for _, mark := range unpromotedMarks {
		marks[mark] = "literal"
	}
	for mark := range marks {
		kind := width.LookupRune(mark).Kind()
		bound := kind == width.EastAsianAmbiguous || kind == width.EastAsianWide
		block := mark >= blockElementsLo && mark <= blockElementsHi
		if !bound || block {
			delete(marks, mark)
		}
	}
	if len(marks) == 0 {
		t.Fatal("no bound marks resolved; the walk would assert nothing")
	}
	return marks
}

// TestBoundGlyphsAreNeverAbutted is the guard for issue #218, widened to the
// wide marks by issue #223. An East Asian Ambiguous glyph is one cell to
// ansi.StringWidth and to every width calculation kb makes, but many terminal
// fonts draw it wider than the cell the cursor was advanced past, so whatever
// is written in the next column lands on top of it; a wide pictograph measures
// honestly at two cells but is drawn by font machinery that respects the cell
// grid no better. The rule is that such a glyph owns the column after it: the
// next cell is a space, or the row ends.
//
// The walk is over the board surface, which is where the vocabulary is densest:
// the top bar, the column bands and meta lines, the card rails, titles, chip
// rows and pills, the overflow cue and the footer ladder, at the sizes that
// exercise both densities and the responsive chip drop.
func TestBoundGlyphsAreNeverAbutted(t *testing.T) {
	marks := spacedMarks(t)
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	fixture := boardViewFixture(now)
	fixture.Tasks[0].Desc = "Pointer capture leaks when the column scrolls under the drag ghost"

	sizes := []struct{ width, height int }{
		{120, 40}, // normal density, four columns
		{116, 40}, // the compaction width axis, four columns
		{100, 28}, // the compaction height axis
		{60, 50},  // single column, tall
		{40, 20},  // single column, compact
		{24, 12},  // below the readable floor, chips dropping
	}
	for _, size := range sizes {
		m := newTestRootModel(stubBoardReader{board: fixture}, nil, "alice")
		m.loading = false
		m.board = fixture
		m.now = func() time.Time { return now }
		m.renderedAt = now
		// The shipped counter is the top bar's only ambiguous mark and it is
		// only drawn when the count is non-zero, so the walk seeds one.
		m.shipped = shippedRecord{Date: now.Format(shippedDateLayout), IDs: []string{"done-1"}}
		sized, _ := m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
		m = sized.(Model)
		for row, line := range strings.Split(plain(m.render()), "\n") {
			cells := []rune(line)
			for column := 0; column < len(cells)-1; column++ {
				name, marked := marks[cells[column]]
				if !marked || cells[column+1] == ' ' {
					continue
				}
				t.Errorf("%dx%d row %d column %d: %s %q is abutted by %q\n%s",
					size.width, size.height, row, column, name,
					string(cells[column]), string(cells[column+1]), line)
			}
		}
	}
}

// TestEffortChipKeepsItsColumn pins the fix issue #218 asked for at the render
// site, so the chip cannot lose its column to a refactor that leaves the walk
// above passing because the surface happened not to carry an effort chip.
func TestEffortChipKeepsItsColumn(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	fixture := boardViewFixture(now)
	m := newTestRootModel(stubBoardReader{board: fixture}, nil, "alice")
	m.loading = false
	m.board = fixture
	m.now = func() time.Time { return now }
	m.renderedAt = now
	styles := m.themeStyles()
	want := styles.Glyph.EffortM + " M"
	for _, density := range []theme.Density{theme.DensityNormal, theme.DensityCompact} {
		meta := m.cardMeta(styles, fixture.Tasks[0], theme.Card, density)
		if got := plain(meta[4]); got != want {
			t.Errorf("density %v: effort chip = %q, want %q", density, got, want)
		}
		if got := ansi.StringWidth(plain(meta[4])); got != 4 {
			t.Errorf("density %v: effort chip is %d cells, spec section 3.4 says 4", density, got)
		}
	}
}

// TestEffortChipKeepsItsLetterOffTheScale pins the fallback: a hand-edited board
// may carry an effort value the S/M/L scale does not name, and the chip stays a
// chip - the diamond it wore before the squares, its own column after it, and
// the value beside it.
func TestEffortChipKeepsItsLetterOffTheScale(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	fixture := boardViewFixture(now)
	fixture.Tasks[0].Effort = "XL"
	m := newTestRootModel(stubBoardReader{board: fixture}, nil, "alice")
	m.loading = false
	m.board = fixture
	m.now = func() time.Time { return now }
	m.renderedAt = now
	styles := m.themeStyles()
	meta := m.cardMeta(styles, fixture.Tasks[0], theme.Card, theme.DensityNormal)
	if got, want := plain(meta[4]), styles.Glyph.Diamond+" XL"; got != want {
		t.Errorf("off-scale effort chip = %q, want %q", got, want)
	}
}

// TestEffortChipIsEmptyWithoutAValue pins the no-value case: an effort a
// terminal sanitizer empties out leaves the slot empty rather than rendering a
// marker for a value that is not there.
func TestEffortChipIsEmptyWithoutAValue(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	fixture := boardViewFixture(now)
	m := newTestRootModel(stubBoardReader{board: fixture}, nil, "alice")
	m.loading = false
	m.board = fixture
	m.now = func() time.Time { return now }
	m.renderedAt = now
	styles := m.themeStyles()
	for _, effort := range []string{"", "\x07"} {
		task := fixture.Tasks[0]
		task.Effort = effort
		meta := m.cardMeta(styles, task, theme.Card, theme.DensityNormal)
		if got := plain(meta[4]); got != "" {
			t.Errorf("effort %q: chip = %q, want empty", effort, got)
		}
	}
}

// sgrRun matches one SGR sequence, the boundary a terminal shapes and places a
// run of text at.
var sgrRun = regexp.MustCompile("\x1b\\[[0-9;]*m")

// styledRuns splits rendered content into the text segments its SGR sequences
// delimit, dropping the empty ones a reset leaves behind.
func styledRuns(rendered string) []string {
	out := make([]string, 0, 4)
	for _, part := range sgrRun.Split(rendered, -1) {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// TestEffortSquareDoesNotShareItsRunWithTheLetter is issue #229. The chip kept
// its four cells and its owned column throughout, and every width kb measures
// agreed with the render, but the square and the letter were emitted as one
// styled run. A terminal shapes a styled run as a unit: the square's advance is
// wider than the two columns the cell grid gives it, so the letter inside that
// run was drawn pushed right by the excess and then clipped by the run after it,
// which lands on the cell the grid gave it. Half an M is what reached the user's
// terminal.
//
// The mark and the column section 10.4.1's adjacency rule gives it are therefore
// one run and the letter is another. This test holds that split at the render
// site, which no width assertion can see.
func TestEffortSquareDoesNotShareItsRunWithTheLetter(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	fixture := boardViewFixture(now)
	m := newTestRootModel(stubBoardReader{board: fixture}, nil, "alice")
	m.loading = false
	m.board = fixture
	m.now = func() time.Time { return now }
	m.renderedAt = now
	styles := m.themeStyles()
	for _, effort := range []string{"S", "M", "L"} {
		mark := styles.Glyph.Effort(effort)
		for _, density := range []theme.Density{theme.DensityNormal, theme.DensityCompact} {
			task := fixture.Tasks[0]
			task.Effort = effort
			chip := m.cardMeta(styles, task, theme.Card, density)[4]
			if got, want := plain(chip), mark+" "+effort; got != want {
				t.Fatalf("effort %s density %v: chip = %q, want %q", effort, density, got, want)
			}
			if got := ansi.StringWidth(plain(chip)); got != 4 {
				t.Errorf("effort %s density %v: chip is %d cells, spec section 3.4 says 4", effort, density, got)
			}
			runs := styledRuns(chip)
			if len(runs) != 2 || runs[0] != mark+" " || runs[1] != effort {
				t.Errorf("effort %s density %v: runs = %q, want the mark with its owned column in one run and the letter in another",
					effort, density, runs)
			}
		}
	}
}

// TestEffortDiamondKeepsOneRun is the other arm of the rule: the off-scale
// fallback is one East Asian Ambiguous cell carrying a real foreground, so it has
// nothing to gain from the split and a color to lose. It keeps the single run.
func TestEffortDiamondKeepsOneRun(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	fixture := boardViewFixture(now)
	fixture.Tasks[0].Effort = "XL"
	m := newTestRootModel(stubBoardReader{board: fixture}, nil, "alice")
	m.loading = false
	m.board = fixture
	m.now = func() time.Time { return now }
	m.renderedAt = now
	styles := m.themeStyles()
	chip := m.cardMeta(styles, fixture.Tasks[0], theme.Card, theme.DensityNormal)[4]
	runs := styledRuns(chip)
	if want := styles.Glyph.Diamond + " XL"; len(runs) != 1 || runs[0] != want {
		t.Errorf("off-scale effort chip runs = %q, want one run %q", runs, want)
	}
}
