package tui

import (
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
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

// ambiguousMarks is the vocabulary the adjacency rule of spec section 10.4.1
// binds: every glyph token whose rune is East Asian Ambiguous, less the Block
// Elements carve-out above.
//
// The rule is scoped to the vocabulary rather than to every ambiguous rune on
// the screen on purpose. Ambiguity is a property of a lot of ordinary text -
// every accented Latin letter is East Asian Ambiguous - so a walk over all of
// it would fail on a card titled "cafes" spelled properly. What kb controls,
// and what this test can therefore hold, is the marks kb writes itself.
func ambiguousMarks(t *testing.T) map[rune]string {
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
		ambiguous := width.LookupRune(mark).Kind() == width.EastAsianAmbiguous
		block := mark >= blockElementsLo && mark <= blockElementsHi
		if !ambiguous || block {
			delete(marks, mark)
		}
	}
	if len(marks) == 0 {
		t.Fatal("no ambiguous marks resolved; the walk would assert nothing")
	}
	return marks
}

// TestAmbiguousGlyphsAreNeverAbutted is the guard for issue #218. An East Asian
// Ambiguous glyph is one cell to ansi.StringWidth and to every width
// calculation kb makes, but many terminal fonts draw it wider than the cell the
// cursor was advanced past, so whatever is written in the next column lands on
// top of it. The rule is that such a glyph owns the column after it: the next
// cell is a space, or the row ends.
//
// The walk is over the board surface, which is where the vocabulary is densest:
// the top bar, the column bands and meta lines, the card rails, titles, chip
// rows and pills, the overflow cue and the footer ladder, at the sizes that
// exercise both densities and the responsive chip drop.
func TestAmbiguousGlyphsAreNeverAbutted(t *testing.T) {
	marks := ambiguousMarks(t)
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
	want := styles.Glyph.Diamond + " M"
	for _, density := range []theme.Density{theme.DensityNormal, theme.DensityCompact} {
		meta := m.cardMeta(styles, fixture.Tasks[0], theme.Card, density)
		if got := plain(meta[4]); got != want {
			t.Errorf("density %v: effort chip = %q, want %q", density, got, want)
		}
	}
}
