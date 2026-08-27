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
	"github.com/RandomCodeSpace/kb/internal/tui/widget"
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
// cell every width calculation gives them. The wide mark - the blocked alarm,
// the vocabulary's only pictograph since issue #232 retired the effort squares
// - is bound for the mirror reason:
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
		m.adoptShippedAt(shippedRecord{Date: now.Format(shippedDateLayout), IDs: []string{"done-1"}}, now)
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

// TestEffortMarkerIsALetterOnItsFill is the section 3.4 effort marker as issue
// #232 rewrote it: the letter on a colored fill, three cells padded and one
// flat, with no pictograph anywhere in it. The value the marker carries is the
// letter, so a terminal that draws no emoji and a terminal that draws no color
// both still show which of S, M and L the card is.
func TestEffortMarkerIsALetterOnItsFill(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	fixture := boardViewFixture(now)
	m := newTestRootModel(stubBoardReader{board: fixture}, nil, "alice")
	m.loading = false
	m.board = fixture
	m.now = func() time.Time { return now }
	m.renderedAt = now
	styles := m.themeStyles()
	widths := map[theme.Density]string{
		theme.DensityNormal:  " M ",
		theme.DensityCompact: "M",
	}
	for density, want := range widths {
		meta := m.cardMeta(styles, fixture.Tasks[0], theme.Card, density)
		if got := plain(meta[3]); got != want {
			t.Errorf("density %v: effort marker = %q, want %q", density, got, want)
		}
		if got := ansi.StringWidth(plain(meta[3])); got != len(want) {
			t.Errorf("density %v: effort marker is %d cells, want %d", density, got, len(want))
		}
		for _, r := range plain(meta[3]) {
			if r > 0x7f {
				t.Errorf("density %v: effort marker carries a non-ASCII rune U+%04X", density, r)
			}
		}
	}
}

// TestEffortMarkerHuesTheScale pins the other half: three values, three fills,
// no two alike. The letter carries the value and the hue reinforces it, so a
// scale whose hues collided would say two values were the same at a glance.
func TestEffortMarkerHuesTheScale(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	fixture := boardViewFixture(now)
	m := newTestRootModel(stubBoardReader{board: fixture}, nil, "alice")
	m.loading = false
	m.board = fixture
	m.now = func() time.Time { return now }
	m.renderedAt = now
	styles := m.themeStyles()
	seen := map[string]string{}
	for _, effort := range []string{"S", "M", "L"} {
		task := fixture.Tasks[0]
		task.Effort = effort
		rendered := m.cardMeta(styles, task, theme.Card, theme.DensityNormal)[3]
		if got := plain(rendered); got != " "+effort+" " {
			t.Errorf("effort %s: marker = %q", effort, got)
		}
		key := sgrRun.FindString(rendered)
		if other, found := seen[key]; found {
			t.Errorf("effort %s and %s render the same fill", other, effort)
		}
		seen[key] = effort
	}
}

// TestEffortChipKeepsItsLetterOffTheScale pins the fallback: a hand-edited board
// may carry an effort value the S/M/L scale does not name, and the marker stays
// a marker - the diamond it wore before the squares, its own column after it,
// and the value beside it.
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
	if got, want := plain(meta[3]), styles.Glyph.Diamond+" XL"; got != want {
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
		if got := plain(meta[3]); got != "" {
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

// TestBlockedAlarmDoesNotShareItsRunWithTheSequence is issue #229's defect
// class, following the mark that still carries it. The effort chip kept its
// four cells and its owned column throughout and every width kb measured agreed
// with the render, but the square and the letter were emitted as one styled
// run. A terminal shapes a styled run as a unit: the square's advance is wider
// than the two columns the cell grid gives it, so the letter inside that run
// was drawn pushed right by the excess and then clipped by the run after it,
// which lands on the cell the grid gave it. Half an M is what reached the
// user's terminal.
//
// Issue #232 retired the squares, so the effort marker no longer carries a
// pictograph at all and cannot reproduce the defect. The blocked alarm can: it
// is the vocabulary's one remaining wide mark, and it now sits on the title row
// with the sequence number immediately after its owned column. The mark and
// that column are one run and the sequence is another, which is the split no
// width assertion can see.
func TestBlockedAlarmDoesNotShareItsRunWithTheSequence(t *testing.T) {
	styles := theme.New(true)
	mark := styles.Glyph.Blocked
	for _, density := range []theme.Density{theme.DensityNormal, theme.DensityCompact} {
		row := widget.Card(styles, widget.CardOpts{
			Title: "blocked card", Seq: "#7", Blocked: true,
			Width: 40, TitleLines: 1, Density: density,
		})[0]
		if !strings.Contains(plain(row), mark+" #7") {
			t.Fatalf("density %v: title row lost the alarm beside the sequence: %q", density, plain(row))
		}
		runs := styledRuns(row)
		owned := -1
		for index, run := range runs {
			if run == mark+" " {
				owned = index
			}
		}
		if owned < 0 {
			t.Errorf("density %v: runs = %q, want the alarm and its owned column in a run of their own", density, runs)
			continue
		}
		if owned+1 >= len(runs) || runs[owned+1] != "#7" {
			t.Errorf("density %v: runs = %q, want the sequence in the run after the alarm", density, runs)
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
	chip := m.cardMeta(styles, fixture.Tasks[0], theme.Card, theme.DensityNormal)[3]
	runs := styledRuns(chip)
	if want := styles.Glyph.Diamond + " XL"; len(runs) != 1 || runs[0] != want {
		t.Errorf("off-scale effort chip runs = %q, want one run %q", runs, want)
	}
}
