package formview

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestTerminalTextViewWrapsScrollsAndFitsTerminal(t *testing.T) {
	snapshot := "alpha beta gamma delta epsilon\nomega"
	view := TerminalTextView(snapshot, 99, 12, 5)
	lines := strings.Split(view, "\n")
	if len(lines) != 5 {
		t.Fatalf("terminal snapshot rows = %d, want 5:\n%s", len(lines), view)
	}
	for index, line := range lines {
		if width := ansi.StringWidth(line); width > 12 {
			t.Fatalf("terminal snapshot row %d width = %d, want at most 12: %q", index, width, line)
		}
	}
	if !strings.Contains(view, "omega") {
		t.Fatalf("clamped final viewport omitted the final source line:\n%s", view)
	}

	twoRows := strings.Split(TerminalTextView("only body", -4, 20, 2), "\n")
	if len(twoRows) != 2 || !strings.Contains(twoRows[0], "only body") || !strings.Contains(twoRows[1], "esc return") {
		t.Fatalf("two-row terminal snapshot = %#v", twoRows)
	}
	if got := TerminalTextView("unused", 0, 5, 1); got != "esc r" {
		t.Fatalf("single-row terminal snapshot = %q", got)
	}
}

func TestUpdateTerminalTextNavigationClampsToWrappedSnapshot(t *testing.T) {
	snapshot := strings.Join([]string{"zero", "one", "two", "three", "four", "five", "six", "seven"}, "\n")
	tests := []struct {
		name   string
		offset int
		key    string
		want   int
		exit   bool
	}{
		{name: "up at start", key: "up", want: 0},
		{name: "vim up", offset: 3, key: "k", want: 2},
		{name: "down", key: "down", want: 1},
		{name: "vim down", offset: 1, key: "j", want: 2},
		{name: "page up", offset: 5, key: "pgup", want: 3},
		{name: "page down", offset: 1, key: "pgdown", want: 3},
		{name: "home", offset: 4, key: "home", want: 0},
		{name: "vim home", offset: 4, key: "g", want: 0},
		{name: "end", key: "end", want: 6},
		{name: "vim end", key: "G", want: 6},
		{name: "unknown", offset: 4, key: "x", want: 4},
		{name: "escape", offset: 4, key: "esc", want: 0, exit: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, exit := UpdateTerminalText(snapshot, test.offset, 20, 4, test.key)
			if got != test.want || exit != test.exit {
				t.Fatalf("navigation %q from %d = (%d, %t), want (%d, %t)",
					test.key, test.offset, got, exit, test.want, test.exit)
			}
		})
	}

	if got, exit := UpdateTerminalText("wide", 9, 0, 1, "down"); got != 4 || exit {
		t.Fatalf("narrow single-row navigation = (%d, %t), want (4, false)", got, exit)
	}
}
