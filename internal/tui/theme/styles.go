package theme

import (
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	glamour "charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
	huh "charm.land/huh/v2"
	"charm.land/lipgloss/v2"
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
	Button  ButtonStyles
	Pressed lipgloss.Style

	Input    textinput.Styles
	Area     textarea.Styles
	Help     help.Styles
	Spinner  spinner.Spinner
	Markdown glamour.StyleConfig
	Huh      *huh.Styles

	Metrics Metrics
	Glyph   Glyphs

	blank      lipgloss.Style
	blankBold  lipgloss.Style
	pressedOn  string
	pressedOff string
	surfaceOn  [numSlots]string
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
type ChipStyles struct {
	CapLeft   lipgloss.Style
	CapRight  lipgloss.Style
	Body      lipgloss.Style
	ScopedKey lipgloss.Style
	Flat      lipgloss.Style
}

// StatusStyles are the semantic status roles of spec section 1.5.
type StatusStyles struct {
	OK     lipgloss.Style
	Warn   lipgloss.Style
	Danger lipgloss.Style
	Info   lipgloss.Style
	Dot    lipgloss.Style
}

// OverlayStyles are the elevation surfaces of spec section 4.
type OverlayStyles struct {
	Surf        lipgloss.Style
	HeaderBand  lipgloss.Style
	SectionBand lipgloss.Style
	FooterBand  lipgloss.Style
	Shadow      lipgloss.Style
	FieldLabel  lipgloss.Style
	FieldValue  lipgloss.Style
}

// ButtonStyles are the states of the button widget. Armed is kb's addition for
// the purge and remove two-step; Pressed is the promoted reverse-video feedback
// that pointer.State.Render writes by hand today.
type ButtonStyles struct {
	Rest    lipgloss.Style
	Focused lipgloss.Style
	Hovered lipgloss.Style
	Armed   lipgloss.Style
	Pressed lipgloss.Style
}

// New resolves the palette for a terminal background and builds every style
// exactly once. Both the base and the dimmed variant of spec section 1.8 are
// built here, so the overlay backdrop never blends a color per frame.
//
// isDark arrives from tea.BackgroundColorMsg.IsDark(). Default to true until
// the message lands, then rebuild once.
func New(isDark bool) *Styles {
	base := resolve(isDark)
	built := build(base, isDark)
	built.Dimmed = build(base.dim(), isDark)
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

func build(table paletteRGB, isDark bool) *Styles {
	pal := table.colors()
	blank := lipgloss.NewStyle()
	built := &Styles{
		Pal:        pal,
		Metrics:    defaultMetrics,
		Glyph:      defaultGlyphs,
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
		Surf:        on(FgBase, OverlaySurf),
		HeaderBand:  onBold(FgOnAccent, Brand),
		SectionBand: onBold(FgSubtle, OverlayBand),
		FooterBand:  on(FgSubtle, OverlayBand),
		Shadow:      on(Shadow, Shadow),
		FieldLabel:  on(FgMuted, OverlaySurf),
		FieldValue:  on(FgBase, OverlaySurf),
	}
	built.Pressed = blank.Reverse(true)
	built.Button = ButtonStyles{
		Rest:    on(FgBase, Raised),
		Focused: onBold(FgOnAccent, Brand),
		Hovered: onBold(FgBase, OverlayBand),
		Armed:   onBold(FgOnAccent, StatusDanger),
		Pressed: built.Pressed,
	}

	built.Input = inputStyles(pal, on, isDark)
	built.Area = areaStyles(pal, on, isDark)
	built.Help = helpStyles(on)
	built.Spinner = spinner.Dot
	built.Markdown = markdownStyles(table)
	built.Huh = huhStyles(pal, on, onBold, isDark)
	return built
}

type styleFunc func(foreground, background Slot) lipgloss.Style

// ChipRuns returns the five runs of one pill fill over one surface. Styles.Chip
// and Styles.Label are this composition against the resting card surface; a
// chip on any other surface resolves here, which costs struct copies and never
// a style construction.
func (s *Styles) ChipRuns(fill, surface Slot) ChipStyles {
	return ChipStyles{
		CapLeft:   s.On(fill, surface),
		CapRight:  s.On(fill, surface),
		Body:      s.On(FgOnAccent, fill),
		ScopedKey: s.On(FgSubtle, Surface),
		Flat:      s.OnBold(fill, surface),
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
	built.Focused.FocusedButton = onBold(FgOnAccent, Brand).
		Padding(0, 1).MarginRight(1).MarginBackground(pal[OverlaySurf])
	built.Focused.BlurredButton = on(FgBase, OverlayBand).
		Padding(0, 1).MarginRight(1).MarginBackground(pal[OverlaySurf])
	built.Focused.NoteTitle = onBold(FgSubtle, OverlaySurf)
	built.Blurred = built.Focused
	built.Blurred.Title = on(FgSubtle, OverlaySurf)
	built.Blurred.Option = on(FgSubtle, OverlaySurf)
	built.Help = helpStyles(on)
	return built
}
