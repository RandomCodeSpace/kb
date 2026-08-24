package carddetail

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/board"
	"github.com/RandomCodeSpace/kb/internal/store"
	"github.com/RandomCodeSpace/kb/internal/tui/pointer"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
	"github.com/RandomCodeSpace/kb/internal/tui/widget/spin"
)

// TestSectionRampFollowsTheDestructiveMode is spec section 10.1.4: the mode is a
// property of the overlay, the lead tint is the same in both destructive states,
// and arming deepens the tail rather than re-tinting the label.
func TestSectionRampFollowsTheDestructiveMode(t *testing.T) {
	m := Model{styles: testStyles()}
	if got := m.sectionRamp(); got != theme.GradSection {
		t.Errorf("idle ramp = %v, want GradSection", got)
	}
	m.action = actionDeleteComment
	if got := m.sectionRamp(); got != theme.GradSectionDanger {
		t.Errorf("destructive pending ramp = %v, want GradSectionDanger", got)
	}
	m.confirm = true
	if got := m.sectionRamp(); got != theme.GradSectionArmed {
		t.Errorf("armed ramp = %v, want GradSectionArmed", got)
	}
	leadDanger, tailDanger := theme.RampStops(theme.GradSectionDanger)
	leadArmed, tailArmed := theme.RampStops(theme.GradSectionArmed)
	if leadDanger != leadArmed {
		t.Errorf("arming re-tinted the lead: %v then %v", leadDanger, leadArmed)
	}
	if tailDanger == tailArmed {
		t.Error("arming did not deepen the tail")
	}
	// A write in flight is not a destructive prompt: the pane is past the
	// decision and the frame has nothing left to warn about.
	m.saving = true
	if got := m.sectionRamp(); got != theme.GradSection {
		t.Errorf("in-flight ramp = %v, want GradSection", got)
	}
}

// TestArmedRecolorsTheHeaderBandAndNothingElse is ratified call 6: the armed
// two-step is the only state in the TUI that recolors a header band, and a
// destructive prompt that is merely pending leaves the frame alone.
func TestArmedRecolorsTheHeaderBandAndNothingElse(t *testing.T) {
	styles := testStyles()
	st := &actionStore{comments: []store.Comment{{ID: 4, Author: "a", Body: "first"}}}
	m := openActionModel(t, st)
	m.Resize(90, 24)

	pending := m.View(90, 24)
	if strings.Contains(pending, styles.Overlay.HeaderBandArmed.Render(" ")) {
		t.Fatal("an idle pane rendered the armed header band")
	}

	m.Update(key('d'))
	if !m.IsDestructivePrompt() || m.Armed() {
		t.Fatalf("delete prompt = destructive:%v armed:%v", m.IsDestructivePrompt(), m.Armed())
	}
	if armed := m.View(90, 24); strings.Contains(armed, styles.Overlay.HeaderBandArmed.Render(" ")) {
		t.Fatal("destructive pending recolored the header band")
	}

	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.Armed() {
		t.Fatal("enter did not arm the delete prompt")
	}
	if got := m.View(90, 24); !strings.Contains(got, styles.Overlay.HeaderBandArmed.Render(" ")) {
		t.Fatalf("armed pane did not re-fill the header band:\n%s", got)
	}
}

// TestBrandedDriftBusyLivesInTheFooterBand is spec section 10.8.7: the drift
// check takes the branded tier in the footer band, the ladder tail keeps the
// cancel hint, and the body carries no busy row of its own.
func TestBrandedDriftBusyLivesInTheFooterBand(t *testing.T) {
	m := New(stubReader{}, "u", testStyles())
	m.SetClock(func() time.Time { return time.Unix(0, 0).UTC() })
	m.SetDriftBackend(&fakeDriftBackend{provenance: map[string][]store.ImportLink{
		"github#1": {{Source: "github", ExternalKey: "1", Title: "One"}},
	}}, context.Background())
	m.Update(busyResult(t, m.Open(board.Task{ID: "one", Seq: 1, Title: "One", Tags: []string{"link::github#1"}})))
	m.Resize(100, 24)

	if command := m.Update(key('v')); command == nil {
		t.Fatal("drift did not start")
	}
	if !m.brandBusy() || m.brandLabel() != driftCheckLabel {
		t.Fatalf("brand gate = busy:%v label:%q", m.brandBusy(), m.brandLabel())
	}
	footer := ansi.Strip(m.actionFooter(60))
	if !strings.Contains(footer, driftCheckLabel) || !strings.Contains(footer, "esc cancel") {
		t.Fatalf("busy footer = %q", footer)
	}
	if body := ansi.Strip(m.driftBody(60)); strings.Contains(body, "in progress") {
		t.Fatalf("busy body row survived: %q", body)
	}

	// The handoff is what mounts the engine, and only for a front-most pane.
	if command := m.SetFrontMost(true); command == nil || !m.BrandMounted() {
		t.Fatalf("front-most handoff did not mount the engine: command=%v mounted=%v",
			command, m.BrandMounted())
	}
	if again := m.SetFrontMost(true); again != nil {
		t.Error("a mounted engine remounted on an unchanged handoff")
	}
	if command := m.brandStep(spin.StepMsg{Seed: spin.SeedDriftCheck, Gen: m.brand.Gen()}); command == nil {
		t.Error("branded step did not continue the chain")
	}
	if command := m.SetFrontMost(false); command != nil || m.BrandMounted() {
		t.Fatalf("backgrounding did not stop the engine: command=%v mounted=%v",
			command, m.BrandMounted())
	}
}

// TestDriftBusyFramesAreByteStable is the determinism contract of spec section
// 10.2.2: the settled first paint is what a golden captures, an injected step
// renders the same bytes every time, and the pane reads no clock of its own.
func TestDriftBusyFramesAreByteStable(t *testing.T) {
	build := func() Model {
		m := New(stubReader{}, "u", testStyles())
		m.SetClock(func() time.Time { return time.Unix(1_700_000_000, 0).UTC() })
		m.SetDriftBackend(&fakeDriftBackend{provenance: map[string][]store.ImportLink{
			"github#1": {{Source: "github", ExternalKey: "1", Title: "One"}},
		}}, context.Background())
		m.Update(busyResult(t, m.Open(board.Task{ID: "one", Seq: 1, Title: "One", Tags: []string{"link::github#1"}})))
		m.Resize(100, 24)
		m.Update(key('v'))
		m.SetFrontMost(true)
		return m
	}
	first, second := build(), build()
	if first.actionFooter(60) != second.actionFooter(60) {
		t.Error("the settled busy row is not byte-stable across two builds")
	}
	for step := range 6 {
		if first.brand.Frame(step) != second.brand.Frame(step) {
			t.Fatalf("step %d is not byte-stable", step)
		}
	}
	// The settled paint carries no frame of its own: the engine renders nothing
	// inside the birth delay, so every existing golden keeps the bytes it has.
	if got := ansi.Strip(first.actionFooter(60)); !strings.HasPrefix(got, driftCheckLabel) {
		t.Errorf("settled busy row = %q", got)
	}
}

// TestBaselineUpdateCarriesItsOwnBrandedLabel covers the second branded leg: the
// two are one user-visible operation apart, so they are not one sentence.
func TestBaselineUpdateCarriesItsOwnBrandedLabel(t *testing.T) {
	m := Model{styles: testStyles(), driftBusy: "accept"}
	if got := m.brandLabel(); got != driftBaselineLabel {
		t.Errorf("accept label = %q, want %q", got, driftBaselineLabel)
	}
	m.driftBusy = "provenance"
	if got := m.brandLabel(); got != driftCheckLabel {
		t.Errorf("provenance label = %q, want %q", got, driftCheckLabel)
	}
	m.driftBusy = ""
	if m.brandBusy() {
		t.Error("an idle pane reported a branded gate")
	}
	if command := m.brandStep(spin.StepMsg{Seed: spin.SeedDriftCheck}); command != nil {
		t.Error("an idle pane continued the branded chain")
	}
}

// TestPlainWriteBusyReplacesTheLadderHead is spec section 10.8.7 for the local
// write: the plain tier in the footer band, and the hint that is still live
// survives as the tail.
func TestPlainWriteBusyReplacesTheLadderHead(t *testing.T) {
	st := &actionStore{}
	m := openActionModel(t, st)
	m.Update(key('c'))
	m.commentInput.SetValue("body")
	if command := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModCtrl}); command == nil {
		t.Fatal("comment save did not start")
	}
	if !m.saving || !m.plainBusy() {
		t.Fatalf("write gate = saving:%v plain:%v", m.saving, m.plainBusy())
	}
	footer := ansi.Strip(m.actionFooter(60))
	if !strings.Contains(footer, savingLabel) || !strings.Contains(footer, "esc stays here") {
		t.Fatalf("write footer = %q", footer)
	}
	if command := m.startPlain(); command == nil {
		t.Error("the write did not open a plain tick chain")
	}
	m.saving, m.loading = false, false
	if command := m.startPlain(); command != nil {
		t.Error("an idle pane opened a plain tick chain")
	}
}

// TestCommentsSectionRendersLoadingThenEmpty is spec section 10.8.4's "loading
// beats empty" and section 10.8.3's row: a section waiting on its first content
// says so, and a section that landed with nothing names the control that fills
// it.
func TestCommentsSectionRendersLoadingThenEmpty(t *testing.T) {
	m := New(stubReader{}, "u", testStyles())
	command := m.Open(board.Task{ID: "one", Seq: 1, Title: "One"})
	if body := ansi.Strip(m.renderBody(60)); !strings.Contains(body, commentsLabel) {
		t.Fatalf("loading body:\n%s", body)
	}
	if strings.Contains(ansi.Strip(m.renderBody(60)), "no comments") {
		t.Fatal("a loading section rendered its empty row")
	}
	m.Update(busyResult(t, command))
	body := ansi.Strip(m.renderBody(60))
	if !strings.Contains(body, "○ no comments  c comment") {
		t.Fatalf("empty comments row:\n%s", body)
	}
}

// TestOneMotionPerSurface is rule 4 of spec section 10.8.4: the footer wins, and
// a body busy row under a busy footer renders its label with no frame.
func TestOneMotionPerSurface(t *testing.T) {
	m := New(stubReader{}, "u", testStyles())
	m.styles = testStyles()
	m.loading = true
	quiet := ansi.Strip(m.sectionBusy(commentsLabel, 40))
	m.saving = true
	underBusyFooter := ansi.Strip(m.sectionBusy(commentsLabel, 40))
	if quiet == underBusyFooter {
		t.Fatalf("the body row kept its frame under a busy footer: %q", underBusyFooter)
	}
	if underBusyFooter != commentsLabel {
		t.Fatalf("suppressed body row = %q, want the bare label", underBusyFooter)
	}
}

// TestPanelErrorsAreTintDangerAboveTheActionRow is ratified call 12 and spec
// section 10.8.5: a failure is a TintDanger body row pinned above the action
// row, never a band row and never the old "error: " text prefix.
func TestPanelErrorsAreTintDangerAboveTheActionRow(t *testing.T) {
	styles := testStyles()
	m := New(stubReader{}, "u", testStyles())
	m.open = true
	m.task = board.Task{ID: "one", Seq: 1, Title: "One"}
	m.Resize(90, 20)
	m.action = actionAddComment
	m.setStatus("write refused: disk full", true)
	m.rebuildBody()

	rows := m.pinnedErrorRows(80)
	if len(rows) != 1 {
		t.Fatalf("pinned rows = %d, want 1", len(rows))
	}
	if !strings.Contains(rows[0], styles.On(theme.TintDanger, theme.OverlaySurf).Render("write refused: disk full")) {
		t.Errorf("panel error is not TintDanger on OverlaySurf:\n%q", rows[0])
	}
	plain := ansi.Strip(rows[0])
	if !strings.Contains(plain, "▲ write refused: disk full") || !strings.Contains(plain, "Save comment") {
		t.Errorf("error row = %q", plain)
	}
	if strings.Contains(ansi.Strip(m.actionFooter(60)), "write refused") {
		t.Error("the footer band carried an error message")
	}
	if strings.Contains(ansi.Strip(m.renderBody(80)), "write refused") {
		t.Error("the scrolling body carried the pinned error")
	}

	// A pane with no action mode has no retryable trigger and carries no tail.
	m.action = actionNone
	tailless := ansi.Strip(strings.Join(m.pinnedErrorRows(80), "\n"))
	if !strings.Contains(tailless, "▲ write refused: disk full") || strings.Contains(tailless, "Save comment") {
		t.Errorf("tail-less error row = %q", tailless)
	}
	// A non-failure notice is ordinary body text.
	m.setStatus("upstream baseline updated", false)
	if rows := m.pinnedErrorRows(80); rows != nil {
		t.Errorf("a notice was pinned as an error: %v", rows)
	}
}

// TestRetryLabelNamesTheControlThatFailed keeps the tail on the button the
// operation belongs to, which is what spec section 10.8.5 puts there instead of
// a second Retry control.
func TestRetryLabelNamesTheControlThatFailed(t *testing.T) {
	for _, test := range []struct {
		name  string
		model Model
		want  string
	}{
		{name: "idle", model: Model{}, want: ""},
		{name: "comment", model: Model{action: actionAddComment}, want: "Save comment"},
		{name: "link", model: Model{action: actionAddLink}, want: "Add link"},
		{name: "delete comment", model: Model{action: actionDeleteComment}, want: "Delete"},
		{name: "delete link", model: Model{action: actionDeleteLink}, want: "Delete"},
		{name: "drift", model: Model{driftMode: driftSelect}, want: "Check selected"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.model.retryLabel(); got != test.want {
				t.Errorf("retryLabel = %q, want %q", got, test.want)
			}
		})
	}
}

// TestHoverRaisesAChoiceRowWithoutMovingACell is spec section 10.5.1 and the
// no-reflow assertion of section 10.5.4: hover is a tier step, and the two
// renders are identical once the profile strips color.
func TestHoverRaisesAChoiceRowWithoutMovingACell(t *testing.T) {
	m := openActionModel(t, &actionStore{
		comments: []store.Comment{{ID: 4, Author: "a", Body: "first"}, {ID: 9, Author: "b", Body: "second"}},
	})
	m.Resize(90, 24)
	m.Update(key('d'))
	if m.action != actionDeleteComment {
		t.Fatalf("delete prompt did not open: action=%v", m.action)
	}

	resting := m.renderBody(80)
	hovered := detailActionChoiceControlID(actionDeleteComment, 1)
	m.pointerState = m.pointerState.Hover(hovered, pointer.Point{X: 20, Y: 10})
	lit := m.renderBody(80)

	if resting == lit {
		t.Fatal("hover changed nothing at all")
	}
	restLines, litLines := strings.Split(resting, "\n"), strings.Split(lit, "\n")
	if len(restLines) != len(litLines) {
		t.Fatalf("hover changed the row count: %d then %d", len(restLines), len(litLines))
	}
	for index := range restLines {
		if ansi.StringWidth(restLines[index]) != ansi.StringWidth(litLines[index]) {
			t.Fatalf("hover changed row %d width", index)
		}
	}
	if ansi.Strip(resting) != ansi.Strip(lit) {
		t.Fatal("hover moved a cell")
	}
	// Section 10.5.1 reuses the pair section 1.9 measured as the Neutral hovered
	// button, so a hovered row and a hovered button in the same panel are the
	// same surface.
	if testStyles().RowSurface(true) != theme.OverlayBand {
		t.Fatalf("hovered row surface = %v", testStyles().RowSurface(true))
	}
	if !strings.Contains(lit, "48;2") {
		t.Error("the hovered row carries no background at all")
	}
}

// TestHoverIsReresolvedAgainstTheNewFrame is row 6 and row 9 of the machine of
// spec section 10.5.2: the pointer can stand still while the content moves under
// it, and a hover that no longer resolves is cleared rather than left lit.
func TestHoverIsReresolvedAgainstTheNewFrame(t *testing.T) {
	m := openActionModel(t, &actionStore{
		comments: []store.Comment{{ID: 4, Author: "a", Body: "first"}, {ID: 9, Author: "b", Body: "second"}},
	})
	m.Resize(90, 24)
	m.Update(key('d'))
	m.PointerSurface("", 90, 24)

	m.pointerState = m.pointerState.Hover(detailActionChoiceControlID(actionDeleteComment, 0), pointer.Point{X: 400, Y: 400})
	m.PointerSurface("", 90, 24)
	if m.pointerState.Hovered() != "" {
		t.Fatalf("a hover outside every region survived: %q", m.pointerState.Hovered())
	}
}

// TestHoveredButtonWearsItsVariantTint is the button half of spec section
// 10.5.1: a button has no cursor to adopt, so it runs the machine's first six
// rows and wears the hovered token of section 1.9.
func TestHoveredButtonWearsItsVariantTint(t *testing.T) {
	styles := testStyles()
	m := New(stubReader{}, "u", styles)
	m.Update(busyResult(t, m.Open(board.Task{ID: "one", Seq: 1, Title: "One"})))
	m.Resize(90, 24)
	controls := m.pointerFooterControls(80)
	if len(controls) == 0 {
		t.Fatal("no action row controls")
	}
	resting := m.actionButtonRow(controls)
	m.pointerState = m.pointerState.Hover(detailFooterControlID(controls[0]), pointer.Point{})
	hovered := m.actionButtonRow(controls)
	if resting == hovered {
		t.Fatal("hover did not reach the action row")
	}
	if ansi.Strip(resting) != ansi.Strip(hovered) {
		t.Fatal("a hovered button moved a cell")
	}
}

// TestEveryActionButtonStatesItsHotkey is the coverage half of ratified call 10
// and spec section 10.4.2: every button whose action carries a single-rune
// hotkey passes it to the resolver, the cue is the underline attribute alone,
// and it survives every state section 1.9 defines.
func TestEveryActionButtonStatesItsHotkey(t *testing.T) {
	m := New(stubReader{}, "u", testStyles())
	m.Update(busyResult(t, m.Open(board.Task{ID: "one", Seq: 1, Title: "One"})))
	m.Resize(120, 30)

	controls := m.pointerFooterControls(m.actionRowWidth(110))
	if len(controls) == 0 {
		t.Fatal("no action row controls")
	}
	marked := 0
	for _, control := range controls {
		label, underline := detailButtonLabel(control)
		press, ok := control.message.(tea.KeyPressMsg)
		if !ok || press.Mod != 0 || len([]rune(press.Text)) != 1 {
			if underline >= 0 {
				t.Errorf("%q marked a hotkey its message does not send", control.label)
			}
			continue
		}
		marked++
		if underline < 0 || underline >= len([]rune(label)) {
			t.Errorf("%q underline %d is off the label %q", control.label, underline, label)
		}
		if got := strings.ToLower(string([]rune(label)[underline])); got != strings.ToLower(press.Text) {
			t.Errorf("%q underlines %q, the key is %q", control.label, got, press.Text)
		}
	}
	if marked == 0 {
		t.Fatal("no action button carried a hotkey at all")
	}

	// The cue is the underline attribute and nothing else: no second color and
	// no bold run laid over a label section 1.9 already measured.
	row := m.actionButtonRow(controls)
	if strings.Count(row, ";4m") < marked {
		t.Errorf("the action row carries %d underline runs for %d hotkeys:\n%q",
			strings.Count(row, ";4m"), marked, row)
	}
	if ansi.Strip(row) != ansi.Strip(m.actionButtonRow(controls)) {
		t.Error("the hotkey cue is not stable across renders")
	}
}

// TestSetStylesRebuildsBothTiers is the section 6.3 hook: a rebuilt palette has
// to reach the spinner glyph set and a running branded engine, or the frame
// renders two palettes at once.
func TestSetStylesRebuildsBothTiers(t *testing.T) {
	m := New(stubReader{}, "u", testStyles())
	m.driftBusy = "check"
	m.SetStyles(nil)
	m.SetStyles(theme.New(false))
	if m.styles.Spinner.FPS != m.plain.Spinner.FPS {
		t.Error("the plain tier kept the old cadence")
	}
	if m.now != nil {
		t.Error("SetStyles wrote the injected clock")
	}
	m.SetClock(func() time.Time { return time.Unix(7, 0).UTC() })
	if m.clock()().Unix() != 7 {
		t.Error("SetClock did not reach the elapsed suffix")
	}
}
