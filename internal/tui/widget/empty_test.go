package widget

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// TestEmptyWidthLadder is spec section 10.8.3: the ladder is applied whole at
// each rung, and the headline goes first because the tail is the actionable
// half.
func TestEmptyWidthLadder(t *testing.T) {
	styles := theme.New(true)
	metrics := styles.Metrics
	opts := EmptyOpts{Headline: "no cards", Key: "n", Verb: "new card", On: theme.Surface}
	for _, test := range []struct {
		name  string
		width int
		want  string
	}{
		{name: "full", width: metrics.EmptyHeadlineMin, want: "○ no cards  n new card"},
		{name: "wide", width: 40, want: "○ no cards  n new card"},
		{name: "tail only", width: metrics.EmptyHeadlineMin - 1, want: "○ n new card"},
		{name: "action floor", width: metrics.EmptyActionMin, want: "○ n new ca"},
		{name: "glyph alone", width: metrics.EmptyActionMin - 1, want: "○"},
		{name: "one column", width: 1, want: "○"},
		{name: "no columns", width: 0, want: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			opts := opts
			opts.Width = test.width
			if got := ansi.Strip(Empty(styles, opts)); got != test.want {
				t.Errorf("Empty at width %d = %q, want %q", test.width, got, test.want)
			}
		})
	}
}

// TestEmptyNeverOverrunsItsWidth keeps the row inside the surface that asked for
// it at every rung, which is what lets a caller wrap it in OverlayRow without
// re-measuring.
func TestEmptyNeverOverrunsItsWidth(t *testing.T) {
	styles := theme.New(true)
	opts := EmptyOpts{
		Headline: "no matching actions",
		Key:      "esc",
		Verb:     "close",
		On:       theme.OverlaySurf,
	}
	for width := 0; width <= 48; width++ {
		opts.Width = width
		if got := ansi.StringWidth(Empty(styles, opts)); got > width {
			t.Errorf("width %d: row is %d cells", width, got)
		}
	}
}

// TestEmptyWithoutAnActionRendersTheHeadlineAlone is the fall-through of spec
// section 10.8.3: a surface whose candidate bindings are all disabled has no
// tail to name and says only what is missing.
func TestEmptyWithoutAnActionRendersTheHeadlineAlone(t *testing.T) {
	styles := theme.New(true)
	got := ansi.Strip(Empty(styles, EmptyOpts{Headline: "no cards", On: theme.Surface, Width: 40}))
	if got != "○ no cards" {
		t.Errorf("headline-only row = %q, want %q", got, "○ no cards")
	}
	narrow := ansi.Strip(Empty(styles, EmptyOpts{Headline: "no cards", On: theme.Surface, Width: 12}))
	if narrow != "○ no cards" {
		t.Errorf("narrow headline-only row = %q, want %q", narrow, "○ no cards")
	}
}

// TestEmptyKeyIsTheBrightestRun is the one hue rule of the row: the key is
// FgBase bold because it is the only part the user has to act on, while the
// headline and verb are FgSubtle and the glyph is FgMuted.
func TestEmptyKeyIsTheBrightestRun(t *testing.T) {
	styles := theme.New(true)
	row := Empty(styles, EmptyOpts{
		Headline: "no cards", Key: "n", Verb: "new card", On: theme.Surface, Width: 40,
	})
	key := styles.OnBold(theme.FgBase, theme.Surface).Render("n")
	if !strings.Contains(row, key) {
		t.Errorf("row does not carry the key in FgBase bold:\n%q", row)
	}
	if !strings.Contains(row, styles.On(theme.FgMuted, theme.Surface).Render(styles.Glyph.Empty)) {
		t.Errorf("row does not carry the Empty glyph in FgMuted:\n%q", row)
	}
}

// TestEmptyVerbIsOptional covers the tail with a key and no verb, which is what
// a surface whose action row owns the action renders when the button's label is
// the whole tail.
func TestEmptyVerbIsOptional(t *testing.T) {
	styles := theme.New(true)
	got := ansi.Strip(Empty(styles, EmptyOpts{
		Headline: "no issues fetched", Key: "Back", On: theme.OverlaySurf, Width: 40,
	}))
	if got != "○ no issues fetched  Back" {
		t.Errorf("verbless tail = %q", got)
	}
}
