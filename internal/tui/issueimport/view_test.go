package issueimport

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/RandomCodeSpace/kb/internal/ai"
	"github.com/RandomCodeSpace/kb/internal/forge"
	"github.com/RandomCodeSpace/kb/internal/store"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
	"github.com/RandomCodeSpace/kb/internal/tui/widget/spin"
)

// TestBrandedEngineDrivesTheFetchRow is the overlay's share of the test
// obligations of spec section 10.2.7. The fetch is a forge round trip and a
// model inference, which is the branded tier of spec section 10.2.4.
func TestBrandedEngineDrivesTheFetchRow(t *testing.T) {
	m := openModel(t, &fakeBackend{sources: []store.ForgeSource{{Name: "primary"}}}, &fakeStore{})
	if !IsMessage(spin.StepMsg{}) {
		t.Fatal("the root does not route the overlay's branded step")
	}
	if m.brandBusy() || m.BrandMounted() || m.brandStep(spin.StepMsg{Seed: spin.SeedImportFetch}) != nil {
		t.Fatal("an idle overlay kept the branded chain alive")
	}

	m.operation = opPreview
	if m.startBrand() == nil || !m.BrandMounted() {
		t.Fatal("a fetching overlay did not mount the branded engine")
	}
	timing := m.themeStyles().Timing
	if got := ansi.Strip(m.View(90, 22)); !strings.Contains(got, previewLabel+"...") {
		t.Fatalf("pre-birth body row = %s", got)
	}
	step := spin.StepMsg{Seed: spin.SeedImportFetch, Gen: m.brand.Gen()}
	if m.brandStep(spin.StepMsg{Seed: spin.SeedAdrPropose, Gen: step.Gen}) != nil {
		t.Fatal("a foreign seed kept the branded chain alive")
	}
	for range timing.BirthDelay + timing.BirthSteps + timing.ScrambleSteps - 1 {
		if m.Update(step) == nil {
			t.Fatal("a fetching overlay dropped the branded step")
		}
	}
	settled := m.View(90, 22)
	if !strings.Contains(settled, "\x1b[") {
		t.Fatal("the branded body row carried no color")
	}
	for range timing.EllipsisStride - 1 {
		m.Update(step)
		if m.View(90, 22) != settled {
			t.Fatal("the settled branded frame moved under tick")
		}
	}

	m.operation = ""
	if m.Update(step) != nil || m.BrandMounted() {
		t.Fatal("a settled overlay re-armed the branded chain")
	}
}

// TestBrandedSuffixCountsTheFetch is the dynamic suffix of spec section
// 10.2.5, read through the overlay's own injected clock.
func TestBrandedSuffixCountsTheFetch(t *testing.T) {
	m := openModel(t, &fakeBackend{sources: []store.ForgeSource{{Name: "primary"}}}, &fakeStore{})
	if (Model{}).clock() == nil {
		t.Fatal("a zero-value overlay resolved no clock")
	}
	base := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	elapsed := time.Duration(0)
	m.now = func() time.Time { return base.Add(elapsed) }

	m.operation = opPreview
	m.startBrand()
	timing := m.themeStyles().Timing
	step := spin.StepMsg{Seed: spin.SeedImportFetch, Gen: m.brand.Gen()}
	elapsed = time.Hour
	for range timing.BirthDelay + timing.BirthSteps + timing.ScrambleSteps + timing.SuffixAfter {
		m.Update(step)
	}
	if got := ansi.Strip(m.View(90, 22)); !strings.Contains(got, "59m+") {
		t.Fatalf("the fetch row carried no elapsed counter:\n%s", got)
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
	m := openModel(t, &fakeBackend{sources: []store.ForgeSource{{Name: "primary"}}}, &fakeStore{})
	m.operation = opPreview
	m.startBrand()
	if m.SetFrontMost(true) != nil {
		t.Fatal("an unchanged z-order rearmed the branded chain")
	}
	if m.SetFrontMost(false) != nil || m.BrandMounted() {
		t.Fatal("a backgrounded overlay kept its engine")
	}
	if got := ansi.Strip(m.View(90, 22)); !strings.Contains(got, previewLabel+"...") {
		t.Fatalf("a backgrounded overlay dropped its static busy label: %s", got)
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
	if m.themeStyles() != styles {
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

// TestProgressTracksTheBatchWrite is spec section 10.8.4 rule 3: an operation
// that knows its denominator takes the determinate meter, never a spinner. It
// is why this overlay carries no plain tier at all - the fetch is branded and
// the write is a meter, so there are no bubbles dots left here.
func TestProgressTracksTheBatchWrite(t *testing.T) {
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
