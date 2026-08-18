package formview

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
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
	area.Placeholder = "placeholder"
	clean := strings.ToUpper
	cursor := func(value string, _, _ int) string {
		return "|" + value
	}
	if got := Area(area, false, 20, 2, clean, cursor); len(got) != 2 || !strings.Contains(got[0], "PLACEHOLDER") || got[1] != "    " {
		t.Fatalf("placeholder area = %#v", got)
	}
	area.SetValue("line one\nline two")
	got := Area(area, true, 12, 2, clean, cursor)
	if len(got) != 2 || !strings.Contains(got[1], "|LINE TW") {
		t.Fatalf("focused area = %#v", got)
	}
}
