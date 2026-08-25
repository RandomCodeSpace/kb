// Package theme owns kb's TUI design tokens: the semantic palette of
// docs/design/tui-design-spec.md section 1, the cached style factory of
// section 6, and the layout metrics and glyphs of section 2.
//
// Nothing outside this package names a hex. Nothing outside this package
// constructs a lipgloss style; a seam test enforces that rule across
// internal/tui.
package theme

import (
	"image/color"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
)

// Slot names one semantic role in the palette. Roles, not hues: a slot says
// what a color is for, never what it looks like.
type Slot uint8

// The palette slots of spec section 1. Order is depth tiers, foreground scale,
// brand and column hues, priority scale, status colors, label wheel.
const (
	// Depth tiers (section 1.1). Depth is carried entirely by background
	// shade; there are no box-drawing borders on cards or columns.
	Shadow      Slot = iota // #05070a x256 232 overlay drop shadow
	Canvas                  // #0b0e14 x256 233 page ground
	Surface                 // #171d27 x256 234 column panel body, footer bar, filter field
	Zebra                   // #1e2632 x256 235 alternating card tier, compact density only
	Card                    // #252f3d x256 236 card surface
	Raised                  // #35404f x256 238 selected card surface and the unfocused band
	OverlaySurf             // #3c495c x256 239 overlay panel body
	OverlayBand             // #4a5970 x256  59 overlay header/footer/section bands

	// Foreground scale (section 1.2).
	FgBase       // #e3e9f2 x256 255 primary text
	FgSubtle     // #9aa5b6 x256 248 secondary text
	FgMuted      // #6b7686 x256 243 tertiary text
	FgOnAccent   // #0b0e14 x256 233 text on a saturated fill
	Brand        // #4f8ef7 x256  69 wordmark pill, overlay header band, focus accent
	HueTodo      // #7aa2f7 x256 111 TO DO column identity
	HueDoing     // #f2a33c x256 215 DOING column identity
	HueDone      // #3fbf7f x256  72 DONE column identity
	HueCancelled // #7b8494 x256 102 CANCELLED column, hidden-count chip

	// Priority scale (section 1.4).
	Prio1 // #ff5a48 x256 203
	Prio2 // #ffb020 x256 214
	Prio3 // #4f8ef7 x256  69 low, and the fallback for any unknown priority

	// Status colors (section 1.5).
	StatusOK     // #3fbf7f x256  72
	StatusWarn   // #ffb020 x256 214
	StatusDanger // #ff5a48 x256 203
	StatusInfo   // #4f8ef7 x256  69
	StatusAlarm  // #b31f14 x256 124 armed two-step fill

	// Button variant tints (section 1.9): the readable-on-Raised form of a
	// variant's hue, and the fill its hovered state carries.
	TintPrimary // #a8b6ff x256 147
	TintSuccess // #7fe0b0 x256 115
	TintDanger  // #ffa7a0 x256 217

	// Label pill wheel (section 1.6), selected by the labelColor hash.
	Label1 // #ff7b54 x256 209
	Label2 // #4f8ef7 x256  69
	Label3 // #3f9d58 x256  71
	Label4 // #b98af7 x256 141
	Label5 // #ffb020 x256 214

	numSlots
)

// BandRest is the unfocused column header band tier. Spec section 1.1 puts the
// band one step above the card surface and names that tier Raised, so BandRest
// is a same-hex alias and not a collision: selection exists only in the focused
// column, whose band is a solid hue fill, so a Raised card and a BandRest band
// can never share a column.
const BandRest = Raised

// PrioritySlot maps a task priority onto its palette slot. Spec section 1.4:
// the scale is three values - high, medium, low - so exact match on 1 and 2,
// and everything else is low.
//
// Issue #234 migrated the store onto the same three values, so the fourth hue
// is gone from the palette and the four-value data this seam was written to
// tolerate no longer reaches it: migrate runs inside store.Open before any
// caller can read a task, and the write paths reject a value off the scale.
// The default arm stays because Slot must be total - a zero-valued struct that
// never reached the store still has to render something, and low is what an
// unset priority means everywhere else.
func PrioritySlot(priority int) Slot {
	switch priority {
	case 1:
		return Prio1
	case 2:
		return Prio2
	default:
		return Prio3
	}
}

// LabelSlot maps a label wheel position onto its palette slot. Spec section
// 1.6: five colors, selected by the label hash the board already uses.
func LabelSlot(index int) Slot {
	return Label1 + Slot(((index%LabelWheel)+LabelWheel)%LabelWheel)
}

// LabelWheel is the size of the label pill wheel of spec section 1.6.
const LabelWheel = 5

// WheelIndex is the label color hash of spec section 1.6: the hash the board
// has always used, carried over unchanged so existing label colors stay stable
// relative to each other. It lives here beside LabelSlot because the per-board
// accent of section 10.7.2 derives from the same wheel, and the derivation must
// not fork the hash.
func WheelIndex(tag string) int {
	first, _ := utf8.DecodeRuneInString(tag)
	if first == utf8.RuneError && tag == "" {
		first = 0
	}
	units := 0
	for _, letter := range tag {
		units += utf16.RuneLen(letter)
	}
	return (units + int(first)) % LabelWheel
}

// unnamedBoard is the title board.Parse defaults to, and the title the zero
// model carries. Spec section 10.7.2: hashing it would hand every unnamed board
// the same arbitrary hue and call it identity.
const unnamedBoard = "board"

// AccentSlot is the per-project accent hue of spec section 10.7.2: a
// deterministic recognition cue for the board the user named, and nothing else.
// No state, count, severity or affordance may ever be encoded in it.
//
// The accent derives from the section 1.6 label wheel rather than from free
// HSL, so it is total over {Brand, Label1..Label5} and can emit no hex the
// section 1.7 audit has not already cleared. The default and empty titles
// resolve to Brand: a board that declared no name looks exactly like kb looks
// without one.
func AccentSlot(title string) Slot {
	name := strings.ToLower(strings.TrimSpace(title))
	if name == "" || name == unnamedBoard {
		return Brand
	}
	return LabelSlot(WheelIndex(name))
}

// Palette resolves every slot to a terminal color.
type Palette [numSlots]color.Color

// rgb is an 8-bit-per-channel color. The palette is authored in rgb so the
// dimmed variant and the 256-color audit can do arithmetic on it.
type rgb struct {
	R uint8
	G uint8
	B uint8
}

// paletteRGB is the palette before it is handed to lipgloss.
type paletteRGB [numSlots]rgb

// darkPalette is the reference design: truecolor on a dark background. It is
// the only column that is reviewed, audited or goldened.
var darkPalette = paletteRGB{
	Shadow:       {0x05, 0x07, 0x0a},
	Canvas:       {0x0b, 0x0e, 0x14},
	Surface:      {0x17, 0x1d, 0x27},
	Zebra:        {0x1e, 0x26, 0x32},
	Card:         {0x25, 0x2f, 0x3d},
	Raised:       {0x35, 0x40, 0x4f},
	OverlaySurf:  {0x3c, 0x49, 0x5c},
	OverlayBand:  {0x4a, 0x59, 0x70},
	FgBase:       {0xe3, 0xe9, 0xf2},
	FgSubtle:     {0x9a, 0xa5, 0xb6},
	FgMuted:      {0x6b, 0x76, 0x86},
	FgOnAccent:   {0x0b, 0x0e, 0x14},
	Brand:        {0x4f, 0x8e, 0xf7},
	HueTodo:      {0x7a, 0xa2, 0xf7},
	HueDoing:     {0xf2, 0xa3, 0x3c},
	HueDone:      {0x3f, 0xbf, 0x7f},
	HueCancelled: {0x7b, 0x84, 0x94},
	Prio1:        {0xff, 0x5a, 0x48},
	Prio2:        {0xff, 0xb0, 0x20},
	Prio3:        {0x4f, 0x8e, 0xf7},
	StatusOK:     {0x3f, 0xbf, 0x7f},
	StatusWarn:   {0xff, 0xb0, 0x20},
	StatusDanger: {0xff, 0x5a, 0x48},
	StatusInfo:   {0x4f, 0x8e, 0xf7},
	StatusAlarm:  {0xb3, 0x1f, 0x14},
	TintPrimary:  {0xa8, 0xb6, 0xff},
	TintSuccess:  {0x7f, 0xe0, 0xb0},
	TintDanger:   {0xff, 0xa7, 0xa0},
	Label1:       {0xff, 0x7b, 0x54},
	Label2:       {0x4f, 0x8e, 0xf7},
	Label3:       {0x3f, 0x9d, 0x58},
	Label4:       {0xb9, 0x8a, 0xf7},
	Label5:       {0xff, 0xb0, 0x20},
}

// lightPalette is the light-background column of the LightDark seam. Map #136
// leaves light-background adaptation as fog, so it deliberately mirrors the
// dark column: the seam exists and is exercised, the design does not yet.
// Populating it is a separate slice, not an invention for this one.
var lightPalette = darkPalette

// dimFactor is the fraction of Canvas blended into every slot to build the
// dimmed variant used behind an overlay (spec section 1.8).
const dimFactor = 0.66

// resolve picks the palette column for the terminal background through the
// lipgloss LightDark seam the spec requires from the first commit. The compat
// package is deliberately not imported: it costs a 2s import-time terminal
// query and collapses indexed colors (research issue #138, hazard 2).
func resolve(isDark bool) paletteRGB {
	pick := lipgloss.LightDark(isDark)
	var out paletteRGB
	for slot := range out {
		picked, _ := pick(lightPalette[slot], darkPalette[slot]).(rgb)
		out[slot] = picked
	}
	return out
}

// RGBA makes an authored palette entry a color.Color so it can travel through
// the lipgloss LightDark seam without losing its channels.
func (c rgb) RGBA() (r, g, b, a uint32) {
	return uint32(c.R) * 0x101, uint32(c.G) * 0x101, uint32(c.B) * 0x101, 0xffff
}

// colors converts an authored palette into the lipgloss-facing form.
func (p paletteRGB) colors() Palette {
	var out Palette
	for slot := range p {
		out[slot] = lipgloss.Color(p[slot].hex())
	}
	return out
}

// dim blends every slot dimFactor of the way toward Canvas. Spec section 1.8:
// built once beside the base palette, never computed per frame.
func (p paletteRGB) dim() paletteRGB {
	ground := p[Canvas]
	var out paletteRGB
	for slot := range p {
		out[slot] = p[slot].blend(ground, dimFactor)
	}
	return out
}

// blend mixes toward other by amount, per channel, rounding half up.
func (c rgb) blend(other rgb, amount float64) rgb {
	return rgb{
		R: mixChannel(c.R, other.R, amount),
		G: mixChannel(c.G, other.G, amount),
		B: mixChannel(c.B, other.B, amount),
	}
}

func mixChannel(from, to uint8, amount float64) uint8 {
	value := float64(from)*(1-amount) + float64(to)*amount + 0.5
	if value <= 0 {
		return 0
	}
	if value >= 255 {
		return 255
	}
	return uint8(value)
}

const hexDigits = "0123456789abcdef"

// hex renders the color as the #rrggbb string lipgloss parses.
func (c rgb) hex() string {
	out := []byte("#000000")
	channels := [3]uint8{c.R, c.G, c.B}
	for index, channel := range channels {
		out[1+index*2] = hexDigits[channel>>4]
		out[2+index*2] = hexDigits[channel&0x0f]
	}
	return string(out)
}

// index256 quantizes a color onto the xterm-256 palette by nearest RGB, the
// algorithm the prototype's -audit256 used to produce the indices recorded in
// spec section 1.7. Ties resolve to the lower index.
func index256(c rgb) uint8 {
	best := 0
	bestDistance := -1
	for index := 0; index < 256; index++ {
		candidate := xterm256(index)
		distance := candidate.distance(c)
		if bestDistance < 0 || distance < bestDistance {
			best = index
			bestDistance = distance
		}
	}
	return uint8(best)
}

func (c rgb) distance(other rgb) int {
	deltaR := int(c.R) - int(other.R)
	deltaG := int(c.G) - int(other.G)
	deltaB := int(c.B) - int(other.B)
	return deltaR*deltaR + deltaG*deltaG + deltaB*deltaB
}

// systemColors are the sixteen fixed xterm entries below the color cube.
var systemColors = [16]rgb{
	{0x00, 0x00, 0x00}, {0x80, 0x00, 0x00}, {0x00, 0x80, 0x00}, {0x80, 0x80, 0x00},
	{0x00, 0x00, 0x80}, {0x80, 0x00, 0x80}, {0x00, 0x80, 0x80}, {0xc0, 0xc0, 0xc0},
	{0x80, 0x80, 0x80}, {0xff, 0x00, 0x00}, {0x00, 0xff, 0x00}, {0xff, 0xff, 0x00},
	{0x00, 0x00, 0xff}, {0xff, 0x00, 0xff}, {0x00, 0xff, 0xff}, {0xff, 0xff, 0xff},
}

// cubeLevels are the six channel steps of the xterm 6x6x6 color cube.
var cubeLevels = [6]uint8{0, 95, 135, 175, 215, 255}

// xterm256 returns the RGB value of one xterm-256 palette index.
func xterm256(index int) rgb {
	switch {
	case index < 16:
		return systemColors[index]
	case index < 232:
		offset := index - 16
		return rgb{
			R: cubeLevels[offset/36],
			G: cubeLevels[(offset/6)%6],
			B: cubeLevels[offset%6],
		}
	default:
		level := uint8(8 + 10*(index-232))
		return rgb{R: level, G: level, B: level}
	}
}
