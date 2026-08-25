package theme

import (
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	glamour "charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
	huh "charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

// Styles is the whole design system, resolved once. Spec section 6.2: every
// lipgloss style in here is constructed inside New, exactly once. New is called
// on program start and on tea.BackgroundColorMsg, nowhere else. The result is
// threaded down from the root model; there is no package-level mutable style
// state and no use of Style.Inherit, which silently skips padding and margins.
type Styles struct {
	Pal    Palette
	Dimmed *Styles // nil on the dimmed instance itself; spec section 4 step 1

	Board   BoardStyles
	Column  ColumnStyles
	Card    CardStyles
	Rail    [5]lipgloss.Style // resting card rail by priority; index 0 unused
	RailSel [5]lipgloss.Style // the same rail on a selected card's Raised surface
	Chip    ChipStyles
	Label   [5]ChipStyles // the section 1.6 wheel
	Status  StatusStyles
	Overlay OverlayStyles
	Button  ButtonSet
	Pressed lipgloss.Style

	Table    TableStyles
	Input    textinput.Styles
	Area     textarea.Styles
	Help     help.Styles
	Spinner  spinner.Spinner
	Progress progress.Model
	Markdown glamour.StyleConfig
	Huh      *huh.Styles

	Work WorkStyles

	Metrics  Metrics
	Glyph    Glyphs
	Timing   Timing
	Fidelity Fidelity

	blank      lipgloss.Style
	blankBold  lipgloss.Style
	pressedOn  string
	pressedOff string
	surfaceOn  [numSlots]string
	bandOn     [numBands]string
	grad       [numRamps][GradSteps]lipgloss.Style
	gradBold   [numRamps][GradSteps]lipgloss.Style
}

// TableStyles are the cell styles of an adopted lipgloss/v2 table. They carry
// layout only - the column gutter of spec section 2.5 and nothing else - so a
// table lays out plain text that the view then paints with the token its row
// role names. A cell style that carried a color would fight the surface the
// row is composed onto.
type TableStyles struct {
	Cell lipgloss.Style // every column but the last: carries the gutter
	Last lipgloss.Style // the last column: no trailing gutter to spend
}

// Column returns the cell style for column index of a row that has count
// columns.
func (t TableStyles) Column(index, count int) lipgloss.Style {
	if index >= count-1 {
		return t.Last
	}
	return t.Cell
}

// BoardStyles are the page-level surfaces of spec section 2.1.
type BoardStyles struct {
	Canvas  lipgloss.Style // page ground
	TopBar  lipgloss.Style // brand row
	Toolbar lipgloss.Style // filter and action row
	Footer  lipgloss.Style // status bar
	PagePad lipgloss.Style // the canvas row between the toolbar and the columns
}

// ColumnStyles are the panel and header band surfaces of spec section 2.2.
// The hued halves of a band are composed with On and OnBold: BandRest carries
// the unfocused band's surface and BandFocus the focused band's foreground, so
// the four column hues do not each need their own cached style.
type ColumnStyles struct {
	Panel     lipgloss.Style // panel body
	BandRest  lipgloss.Style // unfocused band, non-hued cells
	BandFocus lipgloss.Style // focused band on the brand hue
	BandLabel lipgloss.Style // band label on the unfocused surface
	Meta      lipgloss.Style // the "N cards - N blocked" row
	More      lipgloss.Style // the "+N more" overflow cue
}

// CardStyles are the card surfaces and text roles of spec section 3. Rest,
// Zebra and Raised are the three surfaces a card can sit on; the text roles are
// cached against Rest and recomposed with On for the other two.
type CardStyles struct {
	Rest     lipgloss.Style
	Zebra    lipgloss.Style
	Raised   lipgloss.Style
	Title    lipgloss.Style
	TitleSel lipgloss.Style
	Desc     lipgloss.Style
	Seq      lipgloss.Style
}

// ChipStyles are the runs of one pill (spec section 3.6): two half-block end
// caps carrying the fill color as foreground over the surface behind, the text
// body on the fill, the dark half of a scoped pill, and the compact flat form.
//
// BodyHover and FlatHover are the hovered forms of spec section 10.5.1. A pill
// is a saturated fill with two half-block end caps: there is no tier left to
// raise and the caps cannot grow without costing columns, so the cue is an
// underline on the body run. It costs zero cells, survives 256-color
// quantization and the ASCII structure profile, and changes no color, so it
// cannot move the pair's contrast. Bold is unavailable because section 2.6
// already spends it on the compact flat chip, and a hue swap would break the
// label wheel's identity.
type ChipStyles struct {
	CapLeft   lipgloss.Style
	CapRight  lipgloss.Style
	ScopedCap lipgloss.Style
	Body      lipgloss.Style
	BodyHover lipgloss.Style
	ScopedKey lipgloss.Style
	Flat      lipgloss.Style
	FlatHover lipgloss.Style
}

// WorkStyles are the busy-state text roles of spec section 10.2. They are
// foreground only: the branded engine composes a run of them and the caller
// lays that run onto its own shade tier with SurfaceRun, so a style that
// carried a background would fight the surface behind the row.
type WorkStyles struct {
	Label  lipgloss.Style // plain-tier spinner and static busy text
	Birth  lipgloss.Style // the branded engine's pre-birth cells
	Suffix lipgloss.Style // the elapsed-time field right of the label
}

// StatusStyles are the semantic status roles of spec section 1.5.
type StatusStyles struct {
	OK     lipgloss.Style
	Warn   lipgloss.Style
	Danger lipgloss.Style
	Info   lipgloss.Style
	Dot    lipgloss.Style
}

// Band names one overlay band row, for the re-arming run of BandRun.
type Band uint8

// The bands of spec section 4, plus the armed header of section 10.1.4.
const (
	BandHeader Band = iota
	BandHeaderArmed
	BandSection
	BandFooter
	numBands
)

// OverlayStyles are the elevation surfaces of spec section 4.
//
// HeaderBandArmed is the one state in the TUI that recolors a header band
// (spec section 10.1.4, ratified call 6): an armed two-step re-fills the band
// to StatusAlarm with FgBase bold, the same pair section 1.9 gives the armed
// button, so the frame and the button say the same thing in the same color. A
// destructive prompt that is merely pending does not reach it - it re-ramps the
// section label and leaves the frame alone.
type OverlayStyles struct {
	Surf            lipgloss.Style
	HeaderBand      lipgloss.Style
	HeaderBandArmed lipgloss.Style
	SectionBand     lipgloss.Style
	FooterBand      lipgloss.Style
	Shadow          lipgloss.Style
	FieldLabel      lipgloss.Style
	FieldValue      lipgloss.Style
}

// ButtonStyles are the states of one button variant. Armed is kb's addition
// for the purge and remove two-step; Pressed is the promoted reverse-video
// feedback that pointer.State.Render writes by hand today.
type ButtonStyles struct {
	Rest    lipgloss.Style
	Focused lipgloss.Style
	Hovered lipgloss.Style
	Armed   lipgloss.Style
	Pressed lipgloss.Style
}

// ButtonVariant names what a button does, never what it looks like. Spec
// section 5.4 assigns one to every button surface in the TUI.
type ButtonVariant uint8

// The variants of spec section 1.9. Neutral is the zero value: a caller that
// states no meaning gets the calmest surface, not an accidental accent.
const (
	ButtonNeutral ButtonVariant = iota // dismissal, navigation, a side action
	ButtonPrimary                      // the pane's main affirmative
	ButtonSuccess                      // the state-advancing action
	ButtonDanger                       // the destructive action
	numButtonVariants
)

// ButtonSet is the button token matrix: one ButtonStyles per variant, built
// once beside every other style.
type ButtonSet [numButtonVariants]ButtonStyles

// Variant returns the styles of one variant. An out-of-range variant resolves
// to Neutral rather than panicking a render path.
func (b ButtonSet) Variant(variant ButtonVariant) ButtonStyles {
	if variant >= numButtonVariants {
		return b[ButtonNeutral]
	}
	return b[variant]
}

// buttonToken is one cell of the variant matrix of spec section 1.9: the
// foreground, the fill behind it, and whether the state is bold.
type buttonToken struct {
	fg   Slot
	bg   Slot
	bold bool
}

// buttonVariantTokens is one variant's four states. Pressed is not here: it is
// the reverse-video attribute, shared by every variant.
type buttonVariantTokens struct {
	rest    buttonToken
	hovered buttonToken
	focused buttonToken
	armed   buttonToken
}

// armedToken is the two-step arm state, the same for every variant: arming is
// only ever destructive, and it must not be mistaken for a focused danger
// button, so it carries its own deeper fill (spec section 1.9).
var armedToken = buttonToken{fg: FgBase, bg: StatusAlarm, bold: true}

// buttonTokens is the normative variant matrix of spec section 1.9. The hue
// carries the meaning and the state carries the elevation: a blurred button
// wears its variant as a tint on the resting surface, a hovered one wears the
// tint as a fill, and a focused one wears the saturated hue. Neutral has no hue
// to spend, so its hovered state is the surface step the widget always used.
//
// Every pair here is contrast-audited in truecolor and at 256 colors
// (audit_test.go); a pair below the readability floor fails the build.
var buttonTokens = [numButtonVariants]buttonVariantTokens{
	ButtonNeutral: {
		rest:    buttonToken{fg: FgBase, bg: Raised},
		hovered: buttonToken{fg: FgBase, bg: OverlayBand, bold: true},
		focused: buttonToken{fg: FgOnAccent, bg: FgSubtle, bold: true},
		armed:   armedToken,
	},
	ButtonPrimary: {
		rest:    buttonToken{fg: TintPrimary, bg: Raised},
		hovered: buttonToken{fg: FgOnAccent, bg: TintPrimary},
		focused: buttonToken{fg: FgOnAccent, bg: Brand, bold: true},
		armed:   armedToken,
	},
	ButtonSuccess: {
		rest:    buttonToken{fg: TintSuccess, bg: Raised},
		hovered: buttonToken{fg: FgOnAccent, bg: TintSuccess},
		focused: buttonToken{fg: FgOnAccent, bg: StatusOK, bold: true},
		armed:   armedToken,
	},
	ButtonDanger: {
		rest:    buttonToken{fg: TintDanger, bg: Raised},
		hovered: buttonToken{fg: FgOnAccent, bg: TintDanger},
		focused: buttonToken{fg: FgOnAccent, bg: StatusDanger, bold: true},
		armed:   armedToken,
	},
}

// New resolves the palette for a terminal background and builds every style
// exactly once. Both the base and the dimmed variant of spec section 1.8 are
// built here, so the overlay backdrop never blends a color per frame.
//
// isDark arrives from tea.BackgroundColorMsg.IsDark(). Default to true until
// the message lands, then rebuild once.
func New(isDark bool) *Styles { return NewWith(isDark, DefaultTiming) }

// NewFor is New with the terminal floor of spec section 10.7.5 resolved from a
// detected color profile. New is NewFor at the truecolor reference target, so
// the huh ThemeFunc seam of spec section 6.3 keeps its exact signature.
func NewFor(isDark bool, profile colorprofile.Profile) *Styles {
	return newStyles(isDark, DefaultTiming, FidelityFor(profile))
}

// NewWith is New with the timing set of spec section 10.3 injected. Injection
// follows the cached factory of spec section 6.2 rather than mutating a built
// *Styles: the same Timing lands on the base instance and on Dimmed, so an
// overlay schedules against the same clock as the board behind it.
//
// Production calls New. Tests that must collapse timing call NewWith with
// TimingCollapsed, which is the only configuration a golden can assert against.
func NewWith(isDark bool, timing Timing) *Styles {
	return newStyles(isDark, timing, FidelityFull)
}

// newStyles is the one constructor every exported form funnels through: a
// palette, a clock and a terminal floor, resolved once.
func newStyles(isDark bool, timing Timing, fidelity Fidelity) *Styles {
	base := resolve(isDark)
	built := build(base, isDark, timing, fidelity)
	built.Dimmed = build(base.dim(), isDark, timing, fidelity)
	return built
}

// The two forms of an SGR reset a composed run can carry. lipgloss emits the
// short form; glamour and huh emit the explicit one. Both cancel the pressed
// attribute, so both are re-armed after.
const (
	shortReset    = ansi.ResetStyle
	explicitReset = "\x1b[0m"
)

// PressedRun wraps an already-composed run in the Pressed token. Spec section
// 9.1: the reverse-video feedback is a theme token, not a raw escape written by
// the pointer package.
//
// A themed control renders its own styles inside this run, and every one of
// them ends in a reset that would cancel the feedback for the rest of the row,
// so the attribute is re-armed after each reset. The run closes by clearing the
// attribute alone rather than resetting the whole style, because the caller may
// be substituting it into the middle of a styled line.
func (s *Styles) PressedRun(content string) string {
	rearmed := strings.ReplaceAll(content, shortReset, shortReset+s.pressedOn)
	rearmed = strings.ReplaceAll(rearmed, explicitReset, explicitReset+s.pressedOn)
	return s.pressedOn + rearmed + s.pressedOff
}

// BandRun lays an already-composed run into an overlay band row. An overlay
// band is rendered as one styled run over the whole row, so a colored fragment
// inside it - the branded spinner frame of spec section 10.2.5, which is the
// one part of a busy row that is supposed to carry a color - would close with a
// reset and drop the band's own foreground and background for every cell after
// it. Each reset in the run is followed by the band's own sequence instead.
//
// Content with no reset in it is returned untouched, so a band row of plain
// text renders exactly the bytes it always did.
func (s *Styles) BandRun(band Band, content string) string {
	if band >= numBands {
		return content
	}
	on := s.bandOn[band]
	if on == "" || (!strings.Contains(content, shortReset) && !strings.Contains(content, explicitReset)) {
		return content
	}
	rearmed := strings.ReplaceAll(content, shortReset, shortReset+on)
	return strings.ReplaceAll(rearmed, explicitReset, explicitReset+on)
}

// rearmSequence is the SGR prefix a style opens with, recovered once at build
// time so BandRun re-arms exactly the bytes lipgloss would have written. A
// style always renders the argument it was handed, so the sentinel is always
// found and what precedes it is always the opening sequence.
func rearmSequence(style lipgloss.Style) string {
	const sentinel = "\x00"
	opening, _, _ := strings.Cut(style.Render(sentinel), sentinel)
	return opening
}

// SurfaceRun lays an already-composed run onto a surface slot. An adopted charm
// component paints its own runs and closes each one with a reset, which drops
// the panel surface for every cell the component itself did not paint - the
// single space bubbles/help writes between its key and description columns is
// the case this exists for. The background is armed once at the front and
// re-armed after every reset, so a component's output carries the surface edge
// to edge without kb reformatting what the component rendered.
//
// The run closes with a full reset because it is laid down as a whole row, not
// substituted into the middle of one the way PressedRun is.
func (s *Styles) SurfaceRun(surface Slot, content string) string {
	on := s.surfaceOn[surface]
	rearmed := strings.ReplaceAll(content, shortReset, shortReset+on)
	rearmed = strings.ReplaceAll(rearmed, explicitReset, explicitReset+on)
	return on + rearmed + shortReset
}

// On returns the cached blank style carrying a foreground and background slot.
// The widget API of spec section 5.1 is slot-parameterized (chip fills, column
// hues), so the surface a run lands on is only known at render time; resolving
// it here keeps lipgloss.NewStyle out of every render path while still costing
// nothing but a struct copy.
func (s *Styles) On(foreground, background Slot) lipgloss.Style {
	return s.blank.Foreground(s.Pal[foreground]).Background(s.Pal[background])
}

// OnBold is On with the bold attribute already set.
func (s *Styles) OnBold(foreground, background Slot) lipgloss.Style {
	return s.blankBold.Foreground(s.Pal[foreground]).Background(s.Pal[background])
}

// Fg returns the cached blank style carrying a foreground slot only, for runs
// that inherit the surface they are composed onto.
func (s *Styles) Fg(foreground Slot) lipgloss.Style {
	return s.blank.Foreground(s.Pal[foreground])
}

// Surface returns the card surface for a card in this state. Spec section 2.1:
// selection steps Card to Raised, and the alternating tier is compact-only.
func (s *Styles) Surface(selected, alternate bool) Slot {
	switch {
	case selected:
		return Raised
	case alternate:
		return Zebra
	default:
		return Card
	}
}

// RowSurface returns the surface for one overlay choice row. Spec section
// 10.5.1: hover raises the whole row one tier in the section 1.1 depth order,
// panel edge to panel edge, and OverlayBand under FgBase is the pair section
// 1.9 already measured as the Neutral hovered button, so a hovered row and a
// hovered Neutral button in the same panel read as one system.
//
// The row does not bold: bolding a full-width run is a shout where bolding a
// six-cell label is a nudge. A row's own selected state is the focus gutter
// glyph of section 10.4.3, which does not spend the tier step, so the full-row
// raise collides with nothing.
func (s *Styles) RowSurface(hovered bool) Slot {
	if hovered {
		return OverlayBand
	}
	return OverlaySurf
}

func build(table paletteRGB, isDark bool, timing Timing, fidelity Fidelity) *Styles {
	pal := table.colors()
	blank := lipgloss.NewStyle()
	built := &Styles{
		Pal:        pal,
		Metrics:    defaultMetrics,
		Glyph:      defaultGlyphs,
		Timing:     timing,
		Fidelity:   fidelity,
		blank:      blank,
		blankBold:  blank.Bold(true),
		pressedOn:  ansi.Style{}.Reverse(true).String(),
		pressedOff: ansi.Style{}.Reverse(false).String(),
	}
	for slot := Slot(0); slot < numSlots; slot++ {
		built.surfaceOn[slot] = ansi.Style{}.BackgroundColor(pal[slot]).String()
	}
	on := func(foreground, background Slot) lipgloss.Style {
		return blank.Foreground(pal[foreground]).Background(pal[background])
	}
	onBold := func(foreground, background Slot) lipgloss.Style {
		return on(foreground, background).Bold(true)
	}

	built.Board = BoardStyles{
		Canvas:  on(FgBase, Canvas),
		TopBar:  on(FgBase, Canvas),
		Toolbar: on(FgSubtle, Canvas),
		Footer:  on(FgSubtle, Surface),
		PagePad: on(FgBase, Canvas),
	}
	built.Column = ColumnStyles{
		Panel:     on(FgBase, Surface),
		BandRest:  onBold(FgSubtle, BandRest),
		BandFocus: onBold(FgOnAccent, Brand),
		BandLabel: onBold(FgBase, BandRest),
		Meta:      on(FgMuted, Surface),
		More:      on(FgMuted, Surface),
	}
	built.Card = CardStyles{
		Rest:     on(FgBase, Card),
		Zebra:    on(FgBase, Zebra),
		Raised:   on(FgBase, Raised),
		Title:    on(FgBase, Card),
		TitleSel: onBold(FgBase, Raised),
		Desc:     on(FgMuted, Card),
		Seq:      on(FgMuted, Card),
	}
	for priority := 1; priority < len(built.Rail); priority++ {
		hue := PrioritySlot(priority)
		built.Rail[priority] = on(hue, Card)
		built.RailSel[priority] = on(hue, Raised)
	}
	built.Chip = built.ChipRuns(Brand, Card)
	for index := range built.Label {
		built.Label[index] = built.ChipRuns(LabelSlot(index), Card)
	}
	built.Status = StatusStyles{
		OK:     on(StatusOK, Card),
		Warn:   on(StatusWarn, Card),
		Danger: on(StatusDanger, Card),
		Info:   on(StatusInfo, Card),
		Dot:    on(StatusOK, Surface),
	}
	built.Overlay = OverlayStyles{
		Surf:            on(FgBase, OverlaySurf),
		HeaderBand:      onBold(FgOnAccent, Brand),
		HeaderBandArmed: onBold(FgBase, StatusAlarm),
		SectionBand:     onBold(FgSubtle, OverlayBand),
		FooterBand:      on(FgSubtle, OverlayBand),
		Shadow:          on(Shadow, Shadow),
		FieldLabel:      on(FgMuted, OverlaySurf),
		FieldValue:      on(FgBase, OverlaySurf),
	}
	built.bandOn = [numBands]string{
		BandHeader:      rearmSequence(built.Overlay.HeaderBand),
		BandHeaderArmed: rearmSequence(built.Overlay.HeaderBandArmed),
		BandSection:     rearmSequence(built.Overlay.SectionBand),
		BandFooter:      rearmSequence(built.Overlay.FooterBand),
	}
	built.Pressed = blank.Reverse(true)
	token := func(token buttonToken) lipgloss.Style {
		if token.bold {
			return onBold(token.fg, token.bg)
		}
		return on(token.fg, token.bg)
	}
	for variant := ButtonVariant(0); variant < numButtonVariants; variant++ {
		tokens := buttonTokens[variant]
		built.Button[variant] = ButtonStyles{
			Rest:    token(tokens.rest),
			Focused: token(tokens.focused),
			Hovered: token(tokens.hovered),
			Armed:   token(tokens.armed),
			Pressed: built.Pressed,
		}
	}

	built.Table = TableStyles{
		Cell: blank.PaddingRight(defaultMetrics.TableGutter),
		Last: blank,
	}
	built.buildRamps(pal, fidelity)
	built.Input = inputStyles(pal, on, isDark)
	built.Area = areaStyles(pal, on, isDark)
	built.Help = helpStyles(on)
	built.Work = WorkStyles{
		Label:  blank.Foreground(pal[FgSubtle]),
		Birth:  blank.Foreground(pal[FgMuted]),
		Suffix: blank.Foreground(pal[FgSubtle]),
	}
	// Spec section 10.2.1: the plain tier keeps spinner.Dot's glyphs and takes
	// its cadence from the one clock. Leaving FPS at the bubbles default would
	// be a second authored interval by another name.
	built.Spinner = spinner.Dot
	built.Spinner.FPS = timing.PlainFrame()
	built.Progress = meterModel(pal, fidelity)
	built.Markdown = markdownStyles(table)
	built.Huh = huhStyles(pal, on, onBold, isDark)
	return built
}

type styleFunc func(foreground, background Slot) lipgloss.Style

// ChipRuns returns the runs of one pill fill over one surface. Styles.Chip and
// Styles.Label are this composition against the resting card surface; a chip on
// any other surface resolves here, which costs struct copies and never a style
// construction.
func (s *Styles) ChipRuns(fill, surface Slot) ChipStyles {
	body := s.On(FgOnAccent, fill)
	flat := s.OnBold(fill, surface)
	return ChipStyles{
		CapLeft:   s.On(fill, surface),
		CapRight:  s.On(fill, surface),
		ScopedCap: s.On(Surface, surface),
		Body:      body,
		BodyHover: body.Underline(true),
		ScopedKey: s.On(FgSubtle, Surface),
		Flat:      flat,
		FlatHover: flat.Underline(true),
	}
}

// ChipRunsDim returns the runs of one inactive pill over one surface: the same
// section 3.6 anatomy with the wheel hue withdrawn. The fill drops to Surface -
// the tier the scoped pill's dark half already uses - and the text drops from
// FgOnAccent to the section 1.2 tertiary and secondary roles, so an unselected
// filter label reads as an offer rather than as a state.
//
// The dim form keeps the two-tone split of the scoped pill: the key run stays
// one step below the body, so the key/value hierarchy survives the withdrawal
// of the hue rather than collapsing into one gray. No cell count changes, which
// is what lets the filter bar toggle a pill in place (section 10.4.4).
func (s *Styles) ChipRunsDim(surface Slot) ChipStyles {
	body := s.On(FgSubtle, Surface)
	flat := s.OnBold(FgMuted, surface)
	return ChipStyles{
		CapLeft:   s.On(Surface, surface),
		CapRight:  s.On(Surface, surface),
		ScopedCap: s.On(Surface, surface),
		Body:      body,
		BodyHover: body.Underline(true),
		ScopedKey: s.On(FgMuted, Surface),
		Flat:      flat,
		FlatHover: flat.Underline(true),
	}
}

// HuhTheme hands the already-built huh styles to a huh field. Spec section 6.3
// registers the factory itself as huh.ThemeFunc; a field rendered from a
// *Styles that New has already resolved must not rebuild the palette per frame,
// so the func closes over the built styles and ignores the background argument
// it was resolved for.
func (s *Styles) HuhTheme() huh.Theme {
	return huh.ThemeFunc(func(bool) *huh.Styles { return s.Huh })
}

func inputStyles(pal Palette, on styleFunc, isDark bool) textinput.Styles {
	built := textinput.DefaultStyles(isDark)
	built.Focused = textinput.StyleState{
		Text:        on(FgBase, Surface),
		Placeholder: on(FgMuted, Surface),
		Suggestion:  on(FgMuted, Surface),
		Prompt:      on(Brand, Surface),
	}
	built.Blurred = textinput.StyleState{
		Text:        on(FgSubtle, Surface),
		Placeholder: on(FgMuted, Surface),
		Suggestion:  on(FgMuted, Surface),
		Prompt:      on(FgMuted, Surface),
	}
	built.Cursor.Color = pal[Brand]
	return built
}

func areaStyles(pal Palette, on styleFunc, isDark bool) textarea.Styles {
	built := textarea.DefaultStyles(isDark)
	focused := textarea.StyleState{
		Base:             on(FgBase, Surface),
		Text:             on(FgBase, Surface),
		LineNumber:       on(FgMuted, Surface),
		CursorLineNumber: on(FgSubtle, Surface),
		CursorLine:       on(FgBase, Zebra),
		EndOfBuffer:      on(FgMuted, Surface),
		Placeholder:      on(FgMuted, Surface),
		Prompt:           on(Brand, Surface),
	}
	blurred := focused
	blurred.Text = on(FgSubtle, Surface)
	blurred.CursorLine = on(FgSubtle, Surface)
	blurred.Prompt = on(FgMuted, Surface)
	built.Focused = focused
	built.Blurred = blurred
	built.Cursor.Color = pal[Brand]
	return built
}

func helpStyles(on styleFunc) help.Styles {
	return help.Styles{
		Ellipsis:       on(FgMuted, OverlaySurf),
		ShortKey:       on(FgBase, OverlaySurf),
		ShortDesc:      on(FgSubtle, OverlaySurf),
		ShortSeparator: on(FgMuted, OverlaySurf),
		FullKey:        on(FgBase, OverlaySurf),
		FullDesc:       on(FgSubtle, OverlaySurf),
		FullSeparator:  on(FgMuted, OverlaySurf),
	}
}

// markdownStyles derives glamour's config from the palette. Spec section 5.2:
// the hardcoded DarkStyleConfig clone in carddetail becomes this token.
func markdownStyles(table paletteRGB) glamour.StyleConfig {
	hexOf := func(slot Slot) *string {
		hex := table[slot].hex()
		return &hex
	}
	built := styles.DarkStyleConfig
	zero := uint(0)
	built.Document.Margin = &zero
	built.Document.Color = hexOf(FgBase)
	// Markdown is only ever rendered inside an overlay, and glamour pads its
	// wrapped lines: without the surface on the document block those pad cells
	// punch a hole through the panel's shade tier (spec section 4).
	built.Document.BackgroundColor = hexOf(OverlaySurf)
	built.Paragraph.Color = hexOf(FgBase)
	built.Text.Color = hexOf(FgBase)
	built.BlockQuote.Color = hexOf(FgMuted)
	built.Heading.Color = hexOf(Brand)
	built.H1.Color = hexOf(FgOnAccent)
	built.H1.BackgroundColor = hexOf(Brand)
	built.H2.Color = hexOf(Brand)
	built.H3.Color = hexOf(Brand)
	built.H4.Color = hexOf(FgSubtle)
	built.H5.Color = hexOf(FgSubtle)
	built.H6.Color = hexOf(FgMuted)
	built.Link.Color = hexOf(StatusInfo)
	built.LinkText.Color = hexOf(Brand)
	built.Item.Color = hexOf(FgBase)
	built.Enumeration.Color = hexOf(FgSubtle)
	built.HorizontalRule.Color = hexOf(FgMuted)
	built.Code.Color = hexOf(HueDoing)
	built.Code.BackgroundColor = hexOf(Surface)
	built.CodeBlock.Color = hexOf(FgBase)
	return built
}

// huhStyles hands the same palette to huh's fields. Spec section 6.3: huh's
// ThemeFunc signature matches New exactly, so a caller registers
// huh.ThemeFunc(func(d bool) *huh.Styles { return theme.New(d).Huh }).
// The dependency is declared and themed here; no flow is wired to it yet.
func huhStyles(pal Palette, on, onBold styleFunc, isDark bool) *huh.Styles {
	built := huh.ThemeBase(isDark)
	built.Focused.Base = on(FgBase, OverlaySurf)
	built.Focused.Title = onBold(Brand, OverlaySurf)
	built.Focused.Description = on(FgMuted, OverlaySurf)
	built.Focused.ErrorIndicator = on(StatusDanger, OverlaySurf)
	built.Focused.ErrorMessage = on(StatusDanger, OverlaySurf)
	// huh reads the selector as a SetString, not as a rendered run: replacing
	// the style without restoring the string leaves the selected option with no
	// cursor at all. Card is the surface a Note is drawn on and carries the base
	// theme's thick left border until it is replaced here.
	built.Focused.Card = built.Focused.Base
	built.Focused.SelectSelector = on(Brand, OverlaySurf).SetString(defaultGlyphs.Focus + " ")
	built.Focused.Option = on(FgBase, OverlaySurf)
	built.Focused.MultiSelectSelector = on(Brand, OverlaySurf).SetString(defaultGlyphs.Focus + " ")
	built.Focused.SelectedOption = on(StatusOK, OverlaySurf)
	built.Focused.UnselectedOption = on(FgSubtle, OverlaySurf)
	// huh joins the buttons of a confirm horizontally with no separation of its
	// own, so the padding and the gap are part of the token. The margin carries
	// the panel tier so the gap does not punch a hole in the surface.
	// The two button states are the widget's own tokens (spec section 5.1) so a
	// huh Confirm and a kb Button read as the same control: issue #152 makes
	// every dialog choice a visible padded button.
	//
	// The variant is Neutral, and that is a limitation, not a preference: huh
	// exposes one button pair per Confirm, so both choices wear the same token
	// and the pair spans two meanings (Cancel and Ship anyway). Painting them
	// with either meaning would lie about the other, so the confirm carries the
	// calmest variant and the hued treatment lives in the ButtonGroup form of
	// the same guard (spec section 5.4).
	neutral := buttonTokens[ButtonNeutral]
	confirmButton := func(token buttonToken) lipgloss.Style {
		style := on(token.fg, token.bg)
		if token.bold {
			style = style.Bold(true)
		}
		return style.Padding(0, 1).MarginRight(1).MarginBackground(pal[OverlaySurf])
	}
	built.Focused.FocusedButton = confirmButton(neutral.focused)
	built.Focused.BlurredButton = confirmButton(neutral.rest)
	built.Focused.NoteTitle = onBold(FgSubtle, OverlaySurf)
	built.Blurred = built.Focused
	built.Blurred.Title = on(FgSubtle, OverlaySurf)
	built.Blurred.Option = on(FgSubtle, OverlaySurf)
	built.Help = helpStyles(on)
	return built
}
