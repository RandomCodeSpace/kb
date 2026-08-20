package widget

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// TestTableAlignsColumnsAndKeepsOneLinePerRow is the contract the settings pane
// depends on: the label column is as wide as its widest cell, the gutter is the
// spec token, and a row is still exactly one line so hit regions keyed to row
// indices keep their mapping.
func TestTableAlignsColumnsAndKeepsOneLinePerRow(t *testing.T) {
	styles := theme.New(true)
	rows := [][]string{
		{"  Base URL:", "https://example"},
		{"  API key (saved):", "blank keeps saved key"},
		{"> Model:", "gpt"},
	}
	lines := Table(styles, rows)
	if len(lines) != len(rows) {
		t.Fatalf("rendered %d lines for %d rows: %q", len(lines), len(rows), lines)
	}
	gutter := styles.Metrics.TableGutter
	valueColumn := ansi.StringWidth(rows[1][0]) + gutter
	for index, line := range lines {
		if strings.Contains(line, "\n") {
			t.Fatalf("row %d spans lines: %q", index, line)
		}
		if got := strings.Index(line, rows[index][1]); got != valueColumn {
			t.Fatalf("row %d value at column %d, want %d: %q", index, got, valueColumn, line)
		}
	}
	if !strings.HasPrefix(lines[2], "> Model:") {
		t.Fatalf("focus marker moved: %q", lines[2])
	}
}

// TestTableRendersNothingWithoutRows keeps the adapter from emitting a blank
// line for a pane that has no label/value rows to lay out.
func TestTableRendersNothingWithoutRows(t *testing.T) {
	if lines := Table(theme.New(true), nil); lines != nil {
		t.Fatalf("empty table = %q", lines)
	}
}
