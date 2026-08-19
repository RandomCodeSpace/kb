package formview

import (
	"strings"
	"testing"

	huh "charm.land/huh/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

func TestHuhFieldsRenderThemedChoicesNotesAndConfirms(t *testing.T) {
	styles := theme.New(true)

	confirm := ansi.Strip(HuhConfirm(styles, "Cancel", "Ship anyway", true, 40))
	if !strings.Contains(confirm, "Cancel") || !strings.Contains(confirm, "Ship anyway") {
		t.Fatalf("confirm = %q", confirm)
	}

	if got := HuhSelect(styles, nil, 0, 20); got != "" {
		t.Fatalf("empty select = %q", got)
	}
	list := ansi.Strip(HuhSelect(styles, []string{"one", "two"}, 1, 20))
	if !strings.Contains(list, "one") || !strings.Contains(list, "two") {
		t.Fatalf("select = %q", list)
	}

	if got := HuhInlineSelect(styles, nil, 0, 20); got != "" {
		t.Fatalf("empty inline select = %q", got)
	}
	if got := HuhInlineSelect(styles, []string{"one"}, 0, 0); got != "" {
		t.Fatalf("zero-width inline select = %q", got)
	}
	// The inline form is the one that fits a frozen single-row choice field: it
	// renders the current option between the two indicators, and nothing else.
	inline := HuhInlineSelect(styles, []string{"paste", "file"}, 5, 24)
	plain := ansi.Strip(inline)
	if strings.Count(plain, "\n") != 0 || !strings.Contains(plain, "file") || ansi.StringWidth(plain) != 24 {
		t.Fatalf("inline select = %q", plain)
	}

	note := ansi.Strip(HuhNote(styles, []string{"Nothing is created until Add selected."}, 44))
	if !strings.Contains(note, "Nothing is created") {
		t.Fatalf("note = %q", note)
	}
}

func TestHuhViewRendersTheSettledField(t *testing.T) {
	styles := theme.New(true)
	field := huh.NewNote().Description("plain")
	field.WithTheme(styles.HuhTheme())
	field.WithWidth(20)
	if got := ansi.Strip(HuhView(field)); !strings.Contains(got, "plain") {
		t.Fatalf("settled view = %q", got)
	}
}
