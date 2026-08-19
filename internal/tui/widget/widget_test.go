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
