package adrsplit

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/RandomCodeSpace/kb/internal/ai"
	"github.com/RandomCodeSpace/kb/internal/board"
)

func normalizedView(model *Model, width, height int) string {
	lines := strings.Split(ansi.Strip(model.View(width, height)), "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	return strings.Trim(strings.Join(lines, "\n"), "\n") + "\n"
}

func TestADRSplitInputGolden(t *testing.T) {
	m, _, _ := newTestModel()
	m.adr.SetValue("# ADR 0007\n\nUse the local store directly.")
	m.focus = "split"
	golden.RequireEqual(t, normalizedView(m, 84, 28))
}

func TestViewsCoverFileReviewProgressErrorsAndNarrowTerminals(t *testing.T) {
	closed := Model{}
	if closed.View(80, 24) != "" || closed.Overlay("board", 80, 24) != "board" {
		t.Fatal("closed overlay rendered")
	}

	m, _, _ := newTestModel()
	m.focus = "adr"
	if got := ansi.Strip(m.View(60, 16)); !strings.Contains(got, "ADR markdown") {
		t.Fatalf("focused paste view missing:\n%s", got)
	}
	m.source, m.focus = sourceFile, "file"
	m.filePath.SetValue("bad\x1b[31m/path.md")
	m.status, m.statusIsError = "read\x1b[32m\nfailed", true
	fileView := ansi.Strip(m.View(72, 18))
	if !strings.Contains(fileView, "ADR file") || !strings.Contains(fileView, "bounded") || strings.Contains(fileView, "\x1b") || strings.Contains(fileView, "\nfailed") {
		t.Fatalf("unsafe or incomplete file view:\n%s", fileView)
	}

	m.operation = "splitting ADR"
	if got := ansi.Strip(m.View(30, 8)); !strings.Contains(got, "splitting ADR") || len(strings.Split(got, "\n")) > 8 {
		t.Fatalf("narrow progress view:\n%s", got)
	}
	m.operation, m.guardClose = "", true
	if got := ansi.Strip(m.View(50, 12)); !strings.Contains(got, "D discard") {
		t.Fatalf("guard footer missing:\n%s", got)
	}

	m.guardClose, m.stage = false, stageReview
	m.rows = rowsFromDrafts([]ai.Draft{testDraft("one"), testDraft("two"), testDraft("three")})
	m.rows[0].created = true
	m.rows[0].include = false
	m.rows[1].err = "sqlite\x1b[31m\nrefused"
	m.rows[2].include = false
	m.focus, m.dest = "title:1", board.StatusCancelled
	m.applyFocus()
	review := ansi.Strip(m.View(92, 34))
	for _, want := range []string{"REVIEW PROPOSED STORIES", "created", "error: sqliterefused", "Cancelled", "Add selected (1)"} {
		if !strings.Contains(review, want) {
			t.Errorf("review missing %q:\n%s", want, review)
		}
	}
	if strings.Contains(review, "\x1b") || strings.Contains(review, "\nrefused") {
		t.Fatalf("control reached review:\n%s", review)
	}
	background := strings.Repeat("b", 40)
	if overlay := ansi.Strip(m.Overlay(background, 40, 10)); overlay == background || len(strings.Split(overlay, "\n")) > 10 {
		t.Fatalf("overlay did not compose or fit:\n%s", overlay)
	}

	m.adding, m.status = true, "creating card 1 of 2..."
	if got := ansi.Strip(m.View(60, 16)); !strings.Contains(got, "creating card") {
		t.Fatalf("batch progress footer missing:\n%s", got)
	}
}

func TestViewHelpersCoverCursorPlaceholdersAndLabels(t *testing.T) {
	input := textinput.New()
	input.Placeholder = "placeholder"
	if got := inputDisplay(input, false, 5); got != "place" {
		t.Fatalf("truncated placeholder = %q", got)
	}
	input.SetValue("abcdef")
	input.SetCursor(3)
	if got := inputDisplay(input, true, 4); !strings.Contains(got, "|") || ansi.StringWidth(got) > 4 {
		t.Fatalf("focused input = %q", got)
	}
	if cursorViewport("abc", 1, 0) != "" || cursorViewport("abc", 1, 1) != "|" || !strings.Contains(cursorViewport("abcdef", 6, 4), "|") {
		t.Fatal("cursor viewport edge branches failed")
	}

	area := textarea.New()
	area.Placeholder = "line one\nline two"
	if got := areaDisplay(area, false, 20, 3); len(got) != 3 || !strings.Contains(got[0], "line one") {
		t.Fatalf("placeholder area = %#v", got)
	}
	area.SetValue("one\ntwo\nthree")
	if got := areaDisplay(area, true, 8, 2); len(got) != 2 || !strings.Contains(strings.Join(got, ""), "|") {
		t.Fatalf("focused area = %#v", got)
	}

	if focusedLine([]string{"a", "> b"}) != 1 || focusedLine([]string{"a"}) != 0 {
		t.Fatal("focused-line helper failed")
	}
	for status, want := range map[board.Status]string{
		board.StatusTodo: "To Do", board.StatusDoing: "Doing", board.StatusDone: "Done",
		board.StatusCancelled: "Cancelled", board.Status("bad\x1b[31m"): "bad",
	} {
		if got := statusName(status); got != want {
			t.Errorf("statusName(%q) = %q, want %q", status, got, want)
		}
	}
	if effortName("") != "none" || effortName("L") != "L" || fit("abcdef", 3) != "abc" {
		t.Fatal("small rendering helpers failed")
	}
	if got := fitBlock("one\ntwo\nthree", 2, 2); got != "on\ntw" {
		t.Fatalf("fitBlock = %q", got)
	}
}
