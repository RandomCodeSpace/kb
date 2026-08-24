package cardeditor

import (
	"context"
	"errors"
	"strings"
	"testing"

	"charm.land/bubbles/v2/spinner"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// rowText joins the unstyled text of body rows, the form the control-safety
// assertions read.
func rowText(rows []editorRow) string {
	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, row.plain())
	}
	return strings.Join(lines, "\n")
}

func fullEditorTask() board.Task {
	return board.Task{
		ID: "task-id", Seq: 90, Emoji: "🧭", Title: "Map the editor", Desc: "First line\nSecond line",
		Status: board.StatusDoing, Blocked: true, Prio: 1, Due: "2026-08-20", Effort: "M",
		Tags:   []string{"tui", "type::feature"},
		Checks: []board.Check{{Text: "write tests"}, {Text: "ship", Done: true}},
	}
}

func TestEditorGolden(t *testing.T) {
	model := newTestEditor(newTestStore(t), "default")
	model.OpenEdit(fullEditorTask())
	model.labels = []string{"tui", "type::feature", "release"}
	model.similar = []store.SimilarHit{
		{ID: "old", Title: "Map old editor", Status: "cancelled", Via: "killed", KilledAt: "2026-07-01", Reason: "superseded"},
		{Link: "github#12", Title: "Editor planning", Via: "import"},
	}
	model.focus = "save"
	lines := strings.Split(ansi.Strip(theme.Downsample(model.View(84, 32), theme.StructureProfile)), "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	golden.RequireEqual(t, strings.Trim(strings.Join(lines, "\n"), "\n")+"\n")
}

// TestEditorColorGolden is the palette golden of spec section 6.4: an
// ASCII-pinned golden of a design whose depth model is background color
// asserts nothing about the design, so this one pins truecolor.
func TestEditorColorGolden(t *testing.T) {
	model := newTestEditor(newTestStore(t), "default")
	model.OpenEdit(fullEditorTask())
	model.focus = "save"
	golden.RequireEqual(t, []byte(theme.Downsample(model.View(48, 14), theme.ColorProfile)))
}

func TestViewCoversErrorsSuggestionsGuardsAndControlSafety(t *testing.T) {
	model := newTestEditor(newTestStore(t), "u")
	model.OpenEdit(fullEditorTask())
	model.title.SetValue("bad\x1b[31m title")
	model.emoji.SetValue("👨‍💻")
	model.labels = []string{"alpha", "alphabet"}
	model.labelsErr = errors.New("labels\x1b[32m\nfailed")
	model.focus = "labels"
	model.label.SetValue("alp")
	model.labelsOpen = true
	model.labelHighlight = 1
	model.stale = true
	model.statusMessage = "save\x1b[31m\nrefused"
	model.statusIsError = true
	view := ansi.Strip(model.View(72, 24))
	for _, want := range []string{"EDIT CARD", "#90", "one Extended_Pictographic", "suggestions", "alphabet"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "\x1b") || strings.Contains(view, "\nrefused") {
		t.Fatalf("control sequence reached view: %q", view)
	}
	body := strings.Join(model.bodyLines(80), "\n")
	if !strings.Contains(body, "external refresh withheld") || strings.Contains(body, "\x1b") {
		t.Fatalf("scrolled status/control safety = %q", body)
	}

	model.labels = []string{"alpha"}
	model.tags = []string{"alpha"}
	if got := rowText(model.labelSuggestionRows()); !strings.Contains(got, "no label suggestions") {
		t.Fatalf("empty suggestions = %q", got)
	}
	model.similarLoading = true
	if got := rowText(model.similarRows()); !strings.Contains(got, "searching") {
		t.Fatalf("loading similar = %q", got)
	}
	model.similarLoading = false
	model.similarErr = errors.New("lookup failed")
	if got := rowText(model.similarRows()); !strings.Contains(got, "lookup failed") {
		t.Fatalf("failed similar = %q", got)
	}
	model.similarErr = nil
	model.similar = []store.SimilarHit{{Title: "plain"}, {Link: "x", Title: "imported", Via: "import"}}
	if got := rowText(model.similarRows()); !strings.Contains(got, "[card] plain") || !strings.Contains(got, "[import] imported") {
		t.Fatalf("similar variants = %q", got)
	}

	model.guardClose = true
	if got := ansi.Strip(model.View(72, 18)); !strings.Contains(got, "D discard") {
		t.Fatalf("guard footer missing:\n%s", got)
	}
	model.guardClose, model.saving = false, true
	if got := ansi.Strip(model.View(72, 18)); !strings.Contains(got, "saving card") {
		t.Fatalf("saving footer missing:\n%s", got)
	}
}

func TestAIDraftControlsRenderProgressAndControlSafePrompt(t *testing.T) {
	runner := &fakeDraftRunner{}
	model := newTestEditor(newTestStore(t), "u")
	model.SetAIRunner(runner, nil)
	model.SetAIRunner(runner, context.Background())
	model.OpenAdd(board.StatusTodo)
	model.focus = "ai-prompt"
	model.draftPrompt.SetValue("draft\x1b[31m\nthis")
	view := ansi.Strip(model.View(78, 24))
	for _, want := range []string{"Draft with AI", "fills the form", "Request", "Draft"} {
		if !strings.Contains(view, want) {
			t.Errorf("AI editor view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "\x1b") {
		t.Fatalf("AI prompt control reached view: %q", view)
	}
	model.drafting = true
	model.focus = "ai-draft"
	if got := ansi.Strip(model.View(78, 16)); !strings.Contains(got, "esc cancel") || !strings.Contains(got, "Cancel draft") {
		t.Fatalf("draft progress missing:\n%s", got)
	}
}

// TestSpinnerAdvancesOnlyWhileDraftingOrSaving is spec section 5.2: the
// editor's drafting and saving states carry the bubbles spinner instead of
// static text, and the tick loop stops as soon as nothing is in flight.
func TestSpinnerAdvancesOnlyWhileDraftingOrSaving(t *testing.T) {
	model := newTestEditor(newTestStore(t), "u")
	model.OpenAdd(board.StatusTodo)
	if model.busy() || model.spinTick(spinner.TickMsg{}) != nil {
		t.Fatal("idle editor kept a spinner tick alive")
	}
	if model.busyPrefix() == "" {
		t.Fatal("a constructed editor has no spinner frames")
	}
	bare := Model{}
	if bare.busyPrefix() != "" {
		t.Fatal("zero-value editor rendered a spinner frame")
	}
	styles := theme.New(true)
	model.SetStyles(nil)
	model.SetStyles(styles)
	if model.spin.Spinner.FPS != styles.Spinner.FPS {
		t.Fatal("SetStyles did not adopt the design system's spinner")
	}

	model.drafting = true
	if !model.busy() || model.spinTick(spinner.TickMsg{ID: model.spin.ID()}) == nil {
		t.Fatal("drafting editor dropped the spinner tick")
	}
	if got := ansi.Strip(model.View(72, 18)); !strings.Contains(got, model.busyPrefix()+"drafting card...") {
		t.Fatalf("drafting footer carried no spinner frame:\n%s", got)
	}

	model.drafting, model.saving = false, true
	if !model.busy() {
		t.Fatal("saving is a busy state")
	}
	if got := ansi.Strip(model.View(72, 18)); !strings.Contains(got, model.busyPrefix()+"saving card...") {
		t.Fatalf("saving footer carried no spinner frame:\n%s", got)
	}

	model.saving = false
	if command := model.Update(spinner.TickMsg{ID: model.spin.ID()}); command != nil {
		t.Fatal("settled editor re-armed the spinner")
	}
	if !IsMessage(spinner.TickMsg{}) {
		t.Fatal("the root does not route the editor's spinner tick")
	}
}

func TestOverlayAndTinyViewStayBounded(t *testing.T) {
	model := newTestEditor(newTestStore(t), "u")
	background := strings.Repeat("b", 30) + "\n" + strings.Repeat("b", 30)
	if got := model.Overlay(background, 30, 8); got != background || model.View(30, 8) != "" {
		t.Fatal("closed editor changed the surface")
	}
	model.OpenAdd(board.StatusTodo)
	model.title.SetValue("tiny")
	got := ansi.Strip(model.Overlay(background, 30, 10))
	if !strings.Contains(got, "CREATE CARD") || !strings.Contains(got, "bbbb") {
		t.Fatalf("overlay lost pane or background:\n%s", got)
	}
	for _, size := range [][2]int{{1, 1}, {2, 3}, {4, 4}, {20, 6}} {
		view := model.View(size[0], size[1])
		lines := strings.Split(view, "\n")
		if len(lines) > size[1] {
			t.Errorf("%dx%d has %d lines", size[0], size[1], len(lines))
		}
		for _, line := range lines {
			if width := ansi.StringWidth(line); width > size[0] {
				t.Errorf("%dx%d line width %d: %q", size[0], size[1], width, line)
			}
		}
	}
}

func TestViewHelpersCoverCursorAndChoiceEdges(t *testing.T) {
	if cursorViewport("abc", 0, 0) != "" || cursorViewport("abc", 0, 1) != "|" || cursorViewport("abcdef", 6, 4) != "def|" {
		t.Fatal("cursor viewport edge mismatch")
	}
	if effortName("") != "none" || effortName("S") != "S" || boolName(false) != "no" || boolName(true) != "yes" {
		t.Fatal("choice labels mismatch")
	}
	if safeList(nil) != "(none)" || safeList([]string{"a"}) != "[a]" {
		t.Fatal("safe list mismatch")
	}
	if got := fit("abcdef", 3); got != "abc" {
		t.Fatalf("fit = %q", got)
	}
}
