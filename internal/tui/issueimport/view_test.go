package issueimport

import (
	"context"
	"strings"
	"testing"

	"charm.land/bubbles/v2/spinner"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/RandomCodeSpace/kb/internal/ai"
	"github.com/RandomCodeSpace/kb/internal/forge"
	"github.com/RandomCodeSpace/kb/internal/store"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

func reviewModel(t *testing.T) Model {
	t.Helper()
	backend := &fakeBackend{
		sources: []store.ForgeSource{{Name: "primary"}, {Name: "secondary"}},
		preview: forge.Preview{Fetched: 3, Note: "rate limited", Drafts: []forge.Draft{
			{Draft: ai.Draft{Title: "import me"}},
			{Draft: ai.Draft{Title: "already here"}, Duplicate: &forge.Duplicate{Via: "link", Title: "existing"}},
		}},
	}
	m := openModel(t, backend, &fakeStore{})
	m.ref.SetValue("acme/kb")
	m.Update(runCmd(m.startPreview()))
	return m
}

// TestIssueImportGolden is the structure golden of spec section 6.4: layout,
// truncation and drop order, pinned to the colorless profile.
func TestIssueImportGolden(t *testing.T) {
	m := reviewModel(t)
	lines := strings.Split(ansi.Strip(theme.Downsample(m.View(72, 22), theme.StructureProfile)), "\n")
	for index := range lines {
		lines[index] = strings.TrimSpace(lines[index])
	}
	golden.RequireEqual(t, strings.Trim(strings.Join(lines, "\n"), "\n")+"\n")
}

// TestIssueImportColorGolden is the palette golden spec section 6.4 asks for on
// this overlay: an ASCII-pinned golden of a design whose depth model is
// background color asserts nothing about the design.
func TestIssueImportColorGolden(t *testing.T) {
	m := reviewModel(t)
	m.operation, m.queue, m.queuePos = "create", []int{0, 1}, 0
	background := strings.TrimSuffix(strings.Repeat(strings.Repeat("b", 56)+"\n", 18), "\n")
	golden.RequireEqual(t, []byte(theme.Downsample(m.Overlay(background, 56, 18), theme.ColorProfile)))
}

// TestThemeSeamAndRowKinds covers the design-system seam of spec section 6.2:
// the overlay takes a *theme.Styles, falls back to the dark reference until it
// gets one, and picks a token per row kind.
func TestThemeSeamAndRowKinds(t *testing.T) {
	m := New(&fakeStore{}, &fakeBackend{}, "alice", context.Background())
	if m.themeStyles() != fallbackStyles() {
		t.Fatal("unset styles did not fall back to the reference palette")
	}
	m.SetStyles(nil)
	if m.themeStyles() != fallbackStyles() {
		t.Fatal("nil styles replaced the palette")
	}
	styles := theme.New(true)
	m.SetStyles(styles)
	if m.themeStyles() != styles || m.spin.Spinner.FPS != styles.Spinner.FPS {
		t.Fatal("SetStyles did not adopt the design system")
	}

	for _, row := range []importRow{
		{text: "boom", kind: rowError},
		{text: "hint", kind: rowHint},
		{text: "ref", target: "ref", kind: rowField},
		{text: "plain", kind: rowBody},
	} {
		if got := m.renderRow(row, 30); ansi.Strip(got) == "" {
			t.Fatalf("row %q rendered empty", row.text)
		}
	}
	m.focus = 1
	if got := m.renderRow(importRow{text: "ref", target: "ref", kind: rowField}, 30); got == "" {
		t.Fatal("focused field rendered empty")
	}
	if m.focusTarget() != "ref" {
		t.Fatalf("focus target = %q", m.focusTarget())
	}
	m.focus = 0
	// With no configured forge there is nothing to choose between, so the row
	// stays a plain field rather than an empty inline select.
	if got := m.sourceRow(30); !strings.Contains(got.text, "none") {
		t.Fatalf("sourceless row = %q", got.text)
	}
	if m.progressRatio() != 0 {
		t.Fatal("empty queue reported progress")
	}
	if got := m.progressRow(30); got.text != "" {
		t.Fatalf("idle progress row = %q", got.text)
	}
	if cursorViewport("abc", 1, 0) != "" || cursorViewport("abc", 1, 1) != "|" ||
		!strings.Contains(cursorViewport("abcdef", 6, 4), "|") {
		t.Fatal("cursor viewport edge branches failed")
	}
	if sanitize("a\x1b[31mb\nc") != "abc" {
		t.Fatalf("sanitize = %q", sanitize("a\x1b[31mb\nc"))
	}
}

// TestSpinnerAndProgressTrackTheFetchAndWrite is spec section 5.2: the fetch
// state carries the bubbles spinner and the batch write the progress bar.
func TestSpinnerAndProgressTrackTheFetchAndWrite(t *testing.T) {
	m := New(&fakeStore{}, &fakeBackend{}, "alice", context.Background())
	if m.busyPrefix() == "" {
		t.Fatal("a constructed overlay has no spinner frames")
	}
	bare := Model{}
	if bare.busyPrefix() != "" {
		t.Fatal("zero-value overlay rendered a spinner frame")
	}
	if m.spinTick(spinner.TickMsg{}) != nil {
		t.Fatal("idle overlay kept a spinner tick alive")
	}
	m.operation = "preview"
	if m.spinTick(spinner.TickMsg{ID: m.spin.ID()}) == nil {
		t.Fatal("fetching overlay dropped the spinner tick")
	}

	review := reviewModel(t)
	review.operation, review.queue, review.queuePos = "create", []int{0, 1}, 1
	if review.progressRatio() != 1 {
		t.Fatalf("progress ratio = %v", review.progressRatio())
	}
	row := review.progressRow(40)
	if !strings.Contains(row.text, "writing 2/2") || ansi.Strip(row.rendered) == "" {
		t.Fatalf("progress row = %q", row.text)
	}
	if got := ansi.Strip(review.View(60, 20)); !strings.Contains(got, "writing 2/2") {
		t.Fatalf("write progress missing:\n%s", got)
	}
}
