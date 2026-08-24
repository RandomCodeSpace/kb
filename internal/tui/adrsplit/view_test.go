package adrsplit

import (
	"errors"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/RandomCodeSpace/kb/internal/ai"
	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/tui/pointer"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
	"github.com/RandomCodeSpace/kb/internal/tui/widget/spin"
)

// normalizedView is the structure golden form: layout, truncation and drop
// order, pinned to the colorless profile of spec section 6.4.
func normalizedView(model *Model, width, height int) string {
	rendered := theme.Downsample(model.View(width, height), theme.StructureProfile)
	lines := strings.Split(ansi.Strip(rendered), "\n")
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

// TestADRSplitReviewColorGolden is the palette golden of spec section 6.4: an
// ASCII-pinned golden of a design whose depth model is background color asserts
// nothing about the design, so this one pins truecolor over a board background.
func TestADRSplitReviewColorGolden(t *testing.T) {
	m, _, _ := newTestModel()
	m.stage = stageReview
	m.rows = rowsFromDrafts([]ai.Draft{testDraft("one"), testDraft("two")})
	m.rows[1].include = false
	m.focus = "include:0"
	background := strings.TrimSuffix(strings.Repeat(strings.Repeat("b", 56)+"\n", 16), "\n")
	golden.RequireEqual(t, []byte(theme.Downsample(m.Overlay(background, 56, 16), theme.ColorProfile)))
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

	m.operation = opSplitADR
	if got := ansi.Strip(m.View(30, 8)); !strings.Contains(got, opSplitADR) || len(strings.Split(got, "\n")) > 8 {
		t.Fatalf("narrow progress view:\n%s", got)
	}
	m.operation, m.guardClose = "", true
	if got := ansi.Strip(m.View(50, 12)); !strings.Contains(got, "[ Discard ]") || !strings.Contains(got, "[ Stay ]") {
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
	for _, want := range []string{"REVIEW PROPOSED STORIES", "created", "sqliterefused", "Cancelled", "Add selected (1)"} {
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
	// Spec section 10.8.4: the band names the operation, lowercase and present
	// continuous, and the count lives in the determinate row rather than in a
	// label the animation is already standing in for.
	if got := ansi.Strip(m.View(60, 16)); !strings.Contains(got, addLabel) {
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

// TestThemeSeamRowKindsAndChoiceEdges covers the design-system seam of spec
// section 6.2: the overlay takes a *theme.Styles, falls back to the dark
// reference until it gets one, and picks a token per row kind.
func TestThemeSeamRowKindsAndChoiceEdges(t *testing.T) {
	m, _, _ := newTestModel()
	if m.themeStyles() != fallbackStyles() {
		t.Fatal("unset styles did not fall back to the reference palette")
	}
	styles := theme.New(true)
	m.SetStyles(nil)
	if m.themeStyles() != fallbackStyles() {
		t.Fatal("nil styles replaced the palette")
	}
	m.SetStyles(styles)
	if m.themeStyles() != styles || m.spin.Spinner.FPS != styles.Spinner.FPS {
		t.Fatal("SetStyles did not adopt the design system")
	}

	for _, row := range []splitRow{
		{text: "boom", kind: rowError},
		{text: "hint", kind: rowHint},
		{text: "field", target: "max", kind: rowField},
		{text: "plain", kind: rowBody},
		{text: "  [ Cancel ]", button: "[ Cancel ]", target: "cancel", kind: rowButton},
	} {
		if got := m.renderRow(row, 40); ansi.Strip(got) == "" {
			t.Fatalf("row %q rendered empty", row.text)
		}
	}
	// A button whose label no longer fits falls back to the panel surface.
	if got := m.renderRow(splitRow{text: "  [ Cancel ]", button: "[ Cancel ]", kind: rowButton}, 4); ansi.Strip(got) == "" {
		t.Fatal("clipped button rendered empty")
	}
	m.focus = "max"
	if focused := m.renderRow(splitRow{text: "field", target: "max", kind: rowField}, 40); focused == "" {
		t.Fatal("focused field rendered empty")
	}

	if effortIndex("nope") != 0 || statusIndex(board.Status("nope")) != 0 {
		t.Fatal("unknown choice values did not fall back to the first option")
	}
	if got := effortChoices(); got[0] != "none" || len(got) != 4 {
		t.Fatalf("effort choices = %v", got)
	}
	if got := storyCountChoices(); len(got) != maxStories || got[0] != "1" {
		t.Fatalf("story count choices = %v", got)
	}
	if got := m.choiceRow("max", "Max stories", nil, 0, "", 20); got.text != "> Max stories: " {
		t.Fatalf("empty choice row = %q", got.text)
	}
}

// TestSpinnerAdvancesOnlyWhileBusy is the tier split of spec section 10.2.4:
// the file read and the card writes are plumbing and keep the bubbles dots,
// and the tick loop stops as soon as nothing is in flight.
func TestSpinnerAdvancesOnlyWhileBusy(t *testing.T) {
	m, _, _ := newTestModel()
	if m.busy() || m.plainBusy() || m.spinTick(spinner.TickMsg{}) != nil {
		t.Fatal("idle overlay kept a spinner tick alive")
	}
	m.operation = opReadFile
	if !m.busy() || !m.plainBusy() || m.spinTick(spinner.TickMsg{ID: m.spin.ID()}) == nil {
		t.Fatal("busy overlay dropped the spinner tick")
	}
	// Spec section 10.8.4 deletes the ansi.Strip: the frame is the one part of
	// a busy row that is supposed to carry a color.
	if !strings.Contains(m.View(60, 16), m.themeStyles().BandRun(theme.BandFooter, m.plainBand(opReadFile, 40))) {
		t.Fatal("busy footer carried no rendered spinner frame")
	}
	if got := ansi.Strip(m.View(60, 16)); !strings.Contains(got, opReadFile) {
		t.Fatalf("busy footer = %q", got)
	}
	m.operation = opSplitADR
	if m.plainBusy() || m.spinTick(spinner.TickMsg{ID: m.spin.ID()}) != nil {
		t.Fatal("the branded split drove the plain tier as well")
	}
	m.operation, m.adding = "", true
	if !m.busy() || !m.plainBusy() {
		t.Fatal("batch write is a plain busy state")
	}
	m.adding = false
	if command := m.Update(spinner.TickMsg{ID: m.spin.ID()}); command != nil {
		t.Fatal("settled overlay re-armed the spinner")
	}
}

// TestBrandedEngineDrivesTheSplitFooter is the overlay's share of the test
// obligations of spec section 10.2.7.
func TestBrandedEngineDrivesTheSplitFooter(t *testing.T) {
	m, _, _ := newTestModel()
	if !IsMessage(spin.StepMsg{}) {
		t.Fatal("the root does not route the overlay's branded step")
	}
	if m.brandBusy() || m.BrandMounted() || m.brandStep(spin.StepMsg{Seed: spin.SeedAdrPropose}) != nil {
		t.Fatal("an idle overlay kept the branded chain alive")
	}

	m.operation = opSplitADR
	if m.startBrand() == nil || !m.BrandMounted() {
		t.Fatal("a splitting overlay did not mount the branded engine")
	}
	timing := m.themeStyles().Timing
	// Spec section 10.8.4: the static label carries no ellipsis of its own,
	// because the animation is the ellipsis.
	if got := ansi.Strip(m.View(60, 16)); !strings.Contains(got, opSplitADR+" | esc cancel") {
		t.Fatalf("pre-birth footer = %q", got)
	}
	step := spin.StepMsg{Seed: spin.SeedAdrPropose, Gen: m.brand.Gen()}
	if m.brandStep(spin.StepMsg{Seed: spin.SeedImportFetch, Gen: step.Gen}) != nil {
		t.Fatal("a foreign seed kept the branded chain alive")
	}
	for range timing.BirthDelay + timing.BirthSteps + timing.ScrambleSteps - 1 {
		if m.Update(step) == nil {
			t.Fatal("a splitting overlay dropped the branded step")
		}
	}
	settled := m.View(60, 16)
	for range timing.EllipsisStride - 1 {
		m.Update(step)
		if m.View(60, 16) != settled {
			t.Fatal("the settled branded frame moved under tick")
		}
	}

	m.operation = ""
	if m.Update(step) != nil || m.BrandMounted() {
		t.Fatal("a settled overlay re-armed the branded chain")
	}
}

// TestBrandedSuffixCountsTheSplit is the dynamic suffix of spec section
// 10.2.5, read through the overlay's own injected clock.
func TestBrandedSuffixCountsTheSplit(t *testing.T) {
	m, _, _ := newTestModel()
	if (Model{}).clock() == nil {
		t.Fatal("a zero-value overlay resolved no clock")
	}
	base := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	elapsed := time.Duration(0)
	m.now = func() time.Time { return base.Add(elapsed) }

	m.operation = opSplitADR
	m.startBrand()
	timing := m.themeStyles().Timing
	step := spin.StepMsg{Seed: spin.SeedAdrPropose, Gen: m.brand.Gen()}
	elapsed = 12 * time.Second
	for range timing.BirthDelay + timing.BirthSteps + timing.ScrambleSteps + timing.SuffixAfter {
		m.Update(step)
	}
	if got := ansi.Strip(m.View(60, 16)); !strings.Contains(got, "12s") {
		t.Fatalf("the split footer carried no elapsed counter: %q", got)
	}
	m.brand.Stop()
	m.frontMost = false
	if m.startBrand() != nil || m.BrandMounted() {
		t.Fatal("a backgrounded overlay armed a chain")
	}
}

// TestBrandedEngineFollowsTheZOrder is the background handoff of spec section
// 10.2.6.
func TestBrandedEngineFollowsTheZOrder(t *testing.T) {
	m, _, _ := newTestModel()
	m.operation = opSplitADR
	m.startBrand()
	if m.SetFrontMost(true) != nil {
		t.Fatal("an unchanged z-order rearmed the branded chain")
	}
	if m.SetFrontMost(false) != nil || m.BrandMounted() {
		t.Fatal("a backgrounded overlay kept its engine")
	}
	if got := ansi.Strip(m.View(60, 16)); !strings.Contains(got, opSplitADR+" | esc cancel") {
		t.Fatalf("a backgrounded overlay dropped its static busy label: %q", got)
	}
	if m.SetFrontMost(true) == nil || !m.BrandMounted() {
		t.Fatal("a refronted overlay did not remount its engine")
	}
	m.operation = ""
	m.SetFrontMost(false)
	if m.SetFrontMost(true) != nil {
		t.Fatal("a settled overlay mounted an engine on refront")
	}
}

// TestPointerRegionsClipToTheTerminalGrid keeps a panel that overhangs the
// frame from claiming cells outside it.
func TestPointerRegionsClipToTheTerminalGrid(t *testing.T) {
	if _, ok := clipRect(pointer.Rect{X0: 5, Y0: 5, X1: 4, Y1: 6}, 10, 10); ok {
		t.Fatal("inverted rect survived clipping")
	}
	if _, ok := clipRect(pointer.Rect{X0: -4, Y0: -4, X1: 40, Y1: 40}, 10, 10); !ok {
		t.Fatal("overhanging rect was dropped instead of clipped")
	}
	m, _, _ := newTestModel()
	frame := m.layout(80, 24)
	if regions := m.pointerRegions(frame, 0, 0); len(regions) != 0 {
		t.Fatalf("zero-size terminal exposed %d regions", len(regions))
	}
}

// TestReviewWithoutStoriesRendersTheEmptyRow is spec section 10.8.3: the
// STORIES band renders whether or not it is filled, so the section takes the
// empty row rather than showing a band over nothing.
func TestReviewWithoutStoriesRendersTheEmptyRow(t *testing.T) {
	m, _, _ := newTestModel()
	m.stage, m.rows = stageReview, nil
	got := ansi.Strip(m.View(80, 24))
	if !strings.Contains(got, "\u25cb no stories proposed  Back to source") {
		t.Fatalf("empty review row missing:\n%s", got)
	}
}

// TestPanelErrorLeavesTheBand is ratified call 12 and spec section 10.8.5: the
// error moves out of the footer band into a body row above the action row, and
// the row names the control that will run the operation again.
func TestPanelErrorLeavesTheBand(t *testing.T) {
	m, _, _ := newTestModel()
	m.setError(errors.New("model refused the request"))
	if m.statusTail != "Propose stories" {
		t.Fatalf("input-stage tail = %q", m.statusTail)
	}
	got := ansi.Strip(m.View(80, 24))
	if !strings.Contains(got, "\u25b2 model refused the request") || !strings.Contains(got, "Propose stories") {
		t.Fatalf("error row missing:\n%s", got)
	}
	lines := strings.Split(got, "\n")
	if band := lines[len(lines)-2]; strings.Contains(band, "model refused") {
		t.Fatalf("the footer band carried the error: %q", band)
	}

	m.stage = stageReview
	m.rows = rowsFromDrafts([]ai.Draft{testDraft("one")})
	m.setError(errors.New("store refused the write"))
	if m.statusTail != "Add selected (1)" {
		t.Fatalf("review-stage tail = %q", m.statusTail)
	}
	if review := ansi.Strip(m.View(92, 34)); !strings.Contains(review, "\u25b2 store refused the write") {
		t.Fatalf("review error row missing:\n%s", review)
	}
}
