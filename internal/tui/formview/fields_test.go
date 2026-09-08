package formview

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	"github.com/charmbracelet/x/ansi"
)

func TestInputValuePlaceholderFocusAndWidth(t *testing.T) {
	input := textinput.New()
	input.Placeholder = "placeholder"
	clean := strings.ToUpper
	cursor := func(value string, position, width int) string {
		return fmt.Sprintf("%s|%d|%d", value, position, width)
	}
	if got := Input(input, false, 5, clean, cursor); got != "PLACE" {
		t.Fatalf("placeholder = %q", got)
	}
	if got := Input(input, false, 0, clean, cursor); got != "" {
		t.Fatalf("zero-width input = %q", got)
	}
	input.SetValue("value")
	if got := Input(input, true, 7, clean, cursor); got != "VALUE|5|7" {
		t.Fatalf("focused value = %q", got)
	}
}

func TestAreaValuePlaceholderFocusAndPadding(t *testing.T) {
	area := textarea.New()
	area.Prompt = ""
	area.ShowLineNumbers = false
	area.Placeholder = "placeholder"
	clean := strings.ToUpper
	cursor := func(value string, _, _ int) string {
		return "|" + value
	}
	if got := Area(&area, false, 20, 2, clean, cursor); len(got) != 2 || !strings.Contains(got[0], "PLACEHOLDER") || got[1] != "    " {
		t.Fatalf("placeholder area = %#v", got)
	}
	area.SetValue("line one\nline two")
	area.Focus()
	got := Area(&area, true, 12, 2, clean, cursor)
	if len(got) != 2 || !strings.Contains(got[1], "|TWO") {
		t.Fatalf("focused area = %#v", got)
	}
}

func TestAreaSoftWrapsLongMarkdownWithoutChangingSource(t *testing.T) {
	area := textarea.New()
	area.Prompt = ""
	area.ShowLineNumbers = false
	source := "alpha beta https://example.com/a/very/long/path 🧠 tail"
	area.SetValue(source)
	area.MoveToBegin()

	got := Area(&area, false, 16, 6, strings.ToUpper, nil)
	if area.Value() != source {
		t.Fatalf("render changed source to %q", area.Value())
	}
	for index, line := range got {
		if width := ansi.StringWidth(line); width > 16 {
			t.Fatalf("row %d width = %d: %q", index, width, line)
		}
	}
	joined := strings.ReplaceAll(strings.Join(got, ""), " ", "")
	for _, want := range []string{"ALPHABETA", "HTTPS://EXAMPLE.COM/A/VERY/LONG/PATH", "🧠TAIL"} {
		if !strings.Contains(joined, want) {
			t.Errorf("wrapped area missing %q: %#v", want, got)
		}
	}
}

func TestAreaKeepsCursorVisibleAcrossSoftWrapViewport(t *testing.T) {
	area := textarea.New()
	area.Prompt = ""
	area.ShowLineNumbers = false
	area.SetValue("one two three four five six seven eight nine 🧠")
	area.Focus()
	wantLine, wantColumn := area.Line(), area.Column()

	got := Area(&area, true, 12, 2, strings.ToUpper, func(value string, position, width int) string {
		return value[:position] + "|" + value[position:]
	})
	if len(got) != 2 {
		t.Fatalf("area rows = %d: %#v", len(got), got)
	}
	if !strings.Contains(strings.Join(got, "\n"), "|") {
		t.Fatalf("soft-wrapped cursor is not visible: %#v line=%d col=%d offset=%d cursor=%#v", got,
			area.Line(), area.Column(), area.ScrollYOffset(), area.Cursor())
	}
	for index, line := range got {
		if width := ansi.StringWidth(line); width > 12 {
			t.Fatalf("row %d width = %d: %q", index, width, line)
		}
	}
	if area.Value() != "one two three four five six seven eight nine 🧠" ||
		area.Line() != wantLine || area.Column() != wantColumn {
		t.Fatalf("render changed textarea state: value=%q line=%d column=%d", area.Value(), area.Line(), area.Column())
	}
	if area.Width() != 8 || area.Height() != 2 {
		t.Fatalf("textarea dimensions = %dx%d, want 8x2", area.Width(), area.Height())
	}
	offset := area.ScrollYOffset()
	again := Area(&area, true, 12, 2, strings.ToUpper, func(value string, position, width int) string {
		return value[:position] + "|" + value[position:]
	})
	if strings.Join(again, "\n") != strings.Join(got, "\n") || area.ScrollYOffset() != offset {
		t.Fatalf("repeated render reset viewport: first=%#v/%d second=%#v/%d", got, offset, again, area.ScrollYOffset())
	}
}
