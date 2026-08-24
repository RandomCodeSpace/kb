package widget

import (
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

func TestScrollHintClampsItsPosition(t *testing.T) {
	styles := theme.New(true)
	if got := ScrollHint(styles, 3, 0, theme.OverlayBand); got != "" {
		t.Errorf("hint with no content = %q, want nothing", got)
	}
	cases := []struct {
		current, total int
		want           string
	}{
		{12, 40, "12/40"},
		{-4, 40, "0/40"},
		{99, 40, "40/40"},
	}
	for _, testCase := range cases {
		got := ansi.Strip(ScrollHint(styles, testCase.current, testCase.total, theme.OverlayBand))
		if got != testCase.want {
			t.Errorf("ScrollHint(%d, %d) = %q, want %q", testCase.current, testCase.total, got, testCase.want)
		}
	}
}

// TestScrollbarIsAbsentWhenTheBodyFits is the first row of spec section 10.3.4:
// a body that does not overflow carries no affordance, and its column is not
// reserved at all.
func TestScrollbarIsAbsentWhenTheBodyFits(t *testing.T) {
	styles := theme.New(true)
	cases := []ScrollbarOpts{
		{Total: 10, Visible: 10, Height: 10},
		{Total: 4, Visible: 10, Height: 10},
		{Total: 40, Visible: 0, Height: 10},
		{Total: 40, Visible: 10, Height: 0},
		{Total: 40, Visible: 10, Height: -2},
	}
	for index, opts := range cases {
		opts.On = theme.OverlaySurf
		if rows := Scrollbar(styles, opts); rows != nil {
			t.Errorf("case %d rendered %d rows, want none", index, len(rows))
		}
	}
	if ScrollbarShown(10, 10) || ScrollbarShown(4, 10) || ScrollbarShown(40, 0) {
		t.Error("a body that fits its viewport must not reserve the affordance column")
	}
	if !ScrollbarShown(40, 10) {
		t.Error("an overflowing body must reserve the affordance column")
	}
}

// TestScrollbarSpendsOneColumnPerRow keeps the geometry of spec section 10.4.4:
// the affordance is one cell wide on every row it covers.
func TestScrollbarSpendsOneColumnPerRow(t *testing.T) {
	styles := theme.New(true)
	rows := Scrollbar(styles, ScrollbarOpts{Total: 90, Visible: 10, Offset: 20, Height: 10, On: theme.OverlaySurf})
	if len(rows) != 10 {
		t.Fatalf("scrollbar rendered %d rows, want 10", len(rows))
	}
	for index, row := range rows {
		if got := ansi.StringWidth(row); got != ScrollbarW {
			t.Errorf("row %d is %d columns, want %d", index, got, ScrollbarW)
		}
	}
}

// TestScrollbarTintsRatherThanHides is the behavior adjustment of spec section
// 10.3.4: kb dims, it does not hide, so both states render identical geometry
// and differ only in hue.
func TestScrollbarTintsRatherThanHides(t *testing.T) {
	styles := theme.New(true)
	base := ScrollbarOpts{Total: 90, Visible: 10, Offset: 20, Height: 10, On: theme.OverlaySurf}
	settled := Scrollbar(styles, base)
	base.Active = true
	active := Scrollbar(styles, base)
	if len(settled) != len(active) {
		t.Fatalf("settled has %d rows and active %d", len(settled), len(active))
	}
	same := true
	for index := range settled {
		if ansi.Strip(settled[index]) != ansi.Strip(active[index]) {
			t.Errorf("row %d changed shape between the two states", index)
		}
		if settled[index] != active[index] {
			same = false
		}
	}
	if same {
		t.Error("the linger state must change the affordance's tint")
	}
	// Both tints are section 1.2 slots at their section 1.2 hexes, and the
	// settled form is the muted one so an overlay golden captures it on initial
	// paint with no edit.
	muted := styles.On(theme.FgMuted, theme.OverlaySurf)
	subtle := styles.On(theme.FgSubtle, theme.OverlaySurf)
	if settled[0] != muted.Render(styles.Glyph.Track) {
		t.Errorf("settled track = %q, want the FgMuted slot", settled[0])
	}
	if active[0] != subtle.Render(styles.Glyph.Track) {
		t.Errorf("active track = %q, want the FgSubtle slot", active[0])
	}
}

// TestScrollbarThumbTracksTheOffset is what the widget is for: the thumb's
// length is the visible share and its position is the scrolled share, so the
// bar states a position rather than merely announcing that one exists.
func TestScrollbarThumbTracksTheOffset(t *testing.T) {
	styles := theme.New(true)
	thumbAt := func(offset int) (start, length int) {
		rows := Scrollbar(styles, ScrollbarOpts{Total: 100, Visible: 10, Offset: offset, Height: 10, On: theme.OverlaySurf})
		start = -1
		for index, row := range rows {
			if ansi.Strip(row) != styles.Glyph.RailFull {
				continue
			}
			if start < 0 {
				start = index
			}
			length++
		}
		return start, length
	}
	top, length := thumbAt(0)
	if top != 0 || length != 1 {
		t.Errorf("thumb at the top = (%d, %d), want (0, 1)", top, length)
	}
	bottom, bottomLength := thumbAt(90)
	if bottom != 9 || bottomLength != 1 {
		t.Errorf("thumb at the bottom = (%d, %d), want (9, 1)", bottom, bottomLength)
	}
	if past, _ := thumbAt(4000); past != 9 {
		t.Errorf("an offset past the end put the thumb at %d, want it pinned to 9", past)
	}
	if before, _ := thumbAt(-40); before != 0 {
		t.Errorf("a negative offset put the thumb at %d, want 0", before)
	}
	previous := -1
	for offset := 0; offset <= 90; offset += 10 {
		start, _ := thumbAt(offset)
		if start < previous {
			t.Errorf("offset %d moved the thumb backwards to %d", offset, start)
		}
		previous = start
	}
}

// TestScrollbarThumbLengthIsTheVisibleShare keeps the thumb proportional and
// never zero: a very long body still shows where it is.
func TestScrollbarThumbLengthIsTheVisibleShare(t *testing.T) {
	cases := []struct {
		visible, total, height, want int
	}{
		{10, 20, 10, 5},
		{10, 100, 10, 1},
		{1, 10000, 20, 1},
		{19, 20, 10, 10},
		{15, 20, 8, 6},
	}
	for _, testCase := range cases {
		got := thumbLength(testCase.visible, testCase.total, testCase.height)
		if got != testCase.want {
			t.Errorf("thumbLength(%d, %d, %d) = %d, want %d",
				testCase.visible, testCase.total, testCase.height, got, testCase.want)
		}
	}
}

// TestThumbStartHandlesADegenerateTrack keeps the arithmetic total when the
// thumb fills its own track or the body has no travel left.
func TestThumbStartHandlesADegenerateTrack(t *testing.T) {
	if got := thumbStart(5, 10, 20, 4, 4); got != 0 {
		t.Errorf("a thumb filling its track starts at %d, want 0", got)
	}
	if got := thumbStart(5, 10, 10, 8, 2); got != 0 {
		t.Errorf("a body with no travel starts at %d, want 0", got)
	}
}
