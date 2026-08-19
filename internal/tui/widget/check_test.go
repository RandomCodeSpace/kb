package widget

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

func TestCheckCarriesItsStateGlyph(t *testing.T) {
	styles := theme.New(true)
	for _, test := range []struct {
		name  string
		state CheckState
		glyph string
	}{
		{name: "open", state: CheckOpen, glyph: styles.Glyph.Check},
		{name: "done", state: CheckDone, glyph: styles.Glyph.CheckOn},
		{name: "dropped", state: CheckDropped, glyph: styles.Glyph.CheckOff},
	} {
		t.Run(test.name, func(t *testing.T) {
			plain := ansi.Strip(Check(styles, "write tests", test.state, false))
			if !strings.HasPrefix(plain, test.glyph+" ") {
				t.Errorf("row = %q, want the %s glyph", plain, test.name)
			}
			if !strings.HasSuffix(plain, "write tests") {
				t.Errorf("row = %q, want the label", plain)
			}
		})
	}
}

func TestCheckFocusIsBoldBrand(t *testing.T) {
	styles := theme.New(true)
	focused := Check(styles, "ship", CheckOpen, true)
	if focused == Check(styles, "ship", CheckOpen, false) {
		t.Error("focused checklist row renders the same as a resting one")
	}
	if plain := ansi.Strip(focused); !strings.HasSuffix(plain, "ship") {
		t.Errorf("focused row = %q", plain)
	}
}
