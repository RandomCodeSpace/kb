package widget

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

func TestScrollHintIsEmptyWithoutContent(t *testing.T) {
	styles := theme.New(true)
	if got := ScrollHint(styles, 3, 0, theme.OverlaySurf); got != "" {
		t.Errorf("empty scroll hint rendered %q", got)
	}
}

func TestScrollHintClampsToTheRange(t *testing.T) {
	styles := theme.New(true)
	cases := []struct {
		current int
		want    string
	}{
		{12, "12/40"},
		{-4, "0/40"},
		{99, "40/40"},
	}
	for _, testCase := range cases {
		got := ansi.Strip(ScrollHint(styles, testCase.current, 40, theme.OverlaySurf))
		if got != testCase.want {
			t.Errorf("ScrollHint(%d, 40) = %q, want %q", testCase.current, got, testCase.want)
		}
	}
}

func TestScrollHintCarriesTheSurfaceItSitsOn(t *testing.T) {
	styles := theme.New(true)
	if ScrollHint(styles, 1, 2, theme.OverlaySurf) == ScrollHint(styles, 1, 2, theme.Card) {
		t.Error("the scroll hint must carry the surface behind it")
	}
}

func TestTruncateUsesTheEllipsisPrimitive(t *testing.T) {
	styles := theme.New(true)
	if got := truncate(styles, "abcdef", 0); got != "" {
		t.Errorf("zero-width truncate = %q", got)
	}
	if got := truncate(styles, "abcdef", 1); got != "…" {
		t.Errorf("one-cell truncate = %q, want the ellipsis alone", got)
	}
	if got := truncate(styles, "abc", 6); got != "abc" {
		t.Errorf("fitting content was rewritten to %q", got)
	}
	if got := truncate(styles, "abcdef", 4); got != "abc…" {
		t.Errorf("truncate = %q, want abc…", got)
	}
	// The exported form is the same primitive, for the spin subpackage, which
	// composes rows the same way the widgets here do.
	if Truncate(styles, "abcdef", 4) != truncate(styles, "abcdef", 4) {
		t.Error("the exported truncate diverged from the internal one")
	}
}

func TestPadAndFillCarryTheSurface(t *testing.T) {
	styles := theme.New(true)
	surface := styles.On(theme.FgBase, theme.Card)
	if got := pad(surface, 0); got != "" {
		t.Errorf("zero pad = %q", got)
	}
	if got := ansi.StringWidth(pad(surface, 4)); got != 4 {
		t.Errorf("pad width = %d, want 4", got)
	}
	if !strings.Contains(pad(surface, 1), "\x1b[") {
		t.Error("padding must carry the surface color")
	}
	if got := ansi.StringWidth(fill(surface, "ab", 6)); got != 6 {
		t.Errorf("fill width = %d, want 6", got)
	}
	if got := ansi.StringWidth(fill(surface, "abcdef", 3)); got != 6 {
		t.Error("fill must never shorten content, that is clip's job")
	}
}

func TestJoinSkipsEntriesIndividually(t *testing.T) {
	styles := theme.New(true)
	surface := styles.On(theme.FgBase, theme.Card)
	entries := []string{"P1", "", "verylongchip", "3d"}
	got := ansi.Strip(join(surface, entries, 8))
	if got != "P1 3d" {
		t.Errorf("join = %q, want the oversized entry skipped and the short one kept", got)
	}
}

func TestJoinWithoutRoomRendersNothing(t *testing.T) {
	styles := theme.New(true)
	surface := styles.On(theme.FgBase, theme.Card)
	if got := join(surface, []string{"P1"}, 0); got != "" {
		t.Errorf("join into no width = %q", got)
	}
}

func TestDensityAliasesTheThemeTokens(t *testing.T) {
	if DensityNormal != theme.DensityNormal || DensityCompact != theme.DensityCompact {
		t.Error("the widget density aliases must be the theme tokens")
	}
}

// markRunSegments splits rendered content at its SGR sequences, which is where a
// terminal restarts shaping and places the next glyph on the cell the grid gave
// it rather than on the pen the last glyph left behind.
func markRunSegments(rendered string) []string {
	out := make([]string, 0, 4)
	for _, part := range strings.Split(rendered, "\x1b[") {
		if index := strings.IndexByte(part, 'm'); index >= 0 {
			part = part[index+1:]
		}
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// TestMarkRunSplitsAWidePictographOffTheTextBesideIt is issue #229. A two-cell
// mark is a color pictograph whose advance is wider than the two columns the
// cell grid gives it; a terminal shapes one styled run as a unit, so text left
// inside that run is drawn pushed right by the excess and its last glyph is then
// clipped by the run after it. The mark and the column spec section 10.4.1 gives
// it are their own run, and the text beside them starts a fresh one.
func TestMarkRunSplitsAWidePictographOffTheTextBesideIt(t *testing.T) {
	styles := theme.New(true)
	style := styles.On(theme.FgSubtle, theme.Card)
	// The blocked alarm is the vocabulary's one remaining pictograph after
	// issue #232 retired the effort squares; the defect class is the mark's,
	// not the chip's, so the guard follows it onto the title row.
	square := styles.Glyph.Blocked
	got := MarkRun(styles, square, square+" #7", style, theme.Card)
	if want := []string{square + " ", "#7"}; !equalStrings(markRunSegments(got), want) {
		t.Errorf("wide mark runs = %q, want %q", markRunSegments(got), want)
	}
	if plain := ansi.Strip(got); plain != square+" #7" {
		t.Errorf("wide mark content = %q, want %q", plain, square+" #7")
	}
	if width := ansi.StringWidth(ansi.Strip(got)); width != 5 {
		t.Errorf("wide mark run is %d cells, want 5", width)
	}
}

// TestMarkRunKeepsOneRunWhereTheSplitBuysNothing covers the arms that must not
// split: a one-cell mark has a real foreground and an advance that fits, an empty
// mark has no column to own, and a run the caller already truncated past its own
// mark no longer starts with it.
func TestMarkRunKeepsOneRunWhereTheSplitBuysNothing(t *testing.T) {
	styles := theme.New(true)
	style := styles.On(theme.FgSubtle, theme.Card)
	cases := []struct {
		name    string
		mark    string
		content string
	}{
		{"one cell", styles.Glyph.Diamond, styles.Glyph.Diamond + " XL"},
		{"no mark", "", "3d"},
		{"truncated past the mark", styles.Glyph.Blocked, "…"},
	}
	for _, testCase := range cases {
		got := MarkRun(styles, testCase.mark, testCase.content, style, theme.Card)
		if segments := markRunSegments(got); len(segments) != 1 || segments[0] != testCase.content {
			t.Errorf("%s: runs = %q, want one run %q", testCase.name, segments, testCase.content)
		}
	}
}

// TestMarkRunDropsAnEmptyTailRun pins the truncation edge where a wide mark and
// its column are all that survived the field: the mark run is emitted and no
// empty run is spent behind it.
func TestMarkRunDropsAnEmptyTailRun(t *testing.T) {
	styles := theme.New(true)
	square := styles.Glyph.Blocked
	got := MarkRun(styles, square, square+" ", styles.On(theme.FgSubtle, theme.Card), theme.Card)
	if segments := markRunSegments(got); len(segments) != 1 || segments[0] != square+" " {
		t.Errorf("runs = %q, want the mark and its column alone", segments)
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}
