package theme

import (
	"strings"

	"charm.land/bubbles/v2/progress"
	"charm.land/lipgloss/v2"
	"github.com/rivo/uniseg"
)

// Ramp names one prebuilt foreground gradient. Spec section 10.1: a gradient is
// chrome that says something about state, never decoration laid over content,
// and the budget is four surfaces and five named ramps. A fifth surface is a
// spec change, not a slice decision.
type Ramp uint8

// The five ramps of spec section 10.1.2.
const (
	GradSection       Ramp = iota // overlay section-break label, resting
	GradSectionDanger             // ... destructive pending
	GradSectionArmed              // ... armed
	GradMeter                     // progress meter fill
	GradWork                      // branded engine label; launch mark
	numRamps
)

// GradSteps is the length of every prebuilt ramp. Spec section 10.1.1: a ramp's
// interior colors are not palette slots, so no per-slot style exists for them
// and section 6.2 forbids blending one per frame. The ramp is built once at a
// fixed length and resampled by index instead. Twenty-four is roughly 2.7x the
// longest gradient-bearing run in the spec; a longer run repeats colors, which
// is correct rather than an error.
const GradSteps = 24

// rampStops is the lead and tail slot of each ramp, the normative table of spec
// section 10.1.2. Both ends are palette slots, so the section 1.7 audit covers
// every endpoint a ramp can render flat at (section 10.7.5 rule 2).
var rampStops = [numRamps][2]Slot{
	GradSection:       {FgBase, FgSubtle},
	GradSectionDanger: {TintDanger, FgSubtle},
	GradSectionArmed:  {TintDanger, StatusDanger},
	GradMeter:         {StatusInfo, StatusOK},
	GradWork:          {Brand, TintPrimary},
}

// RampStops returns the lead and tail slot of a ramp, so a caller that has to
// pair a flat run with a graded one - the meter's end caps of section 10.1.3 -
// names the ramp rather than restating its endpoints. An out-of-range ramp
// resolves to GradSection rather than panicking a render path.
func RampStops(ramp Ramp) (lead, tail Slot) {
	stops := rampStops[clampRamp(ramp)]
	return stops[0], stops[1]
}

// Grad paints text cluster by cluster along the named ramp, foreground only.
// Spec section 10.1.1: the run is split into grapheme clusters and each cluster
// is styled whole, because rune splitting recolors the inside of an emoji ZWJ
// sequence and puts an SGR change between a base character and its combining
// mark, which some terminals then draw as two cells.
//
// The caller wraps the result in SurfaceRun: per-cluster output is the worst
// case of the hazard SurfaceRun exists for, and a gradient run that is not
// wrapped in it is a bug (spec section 10.1.1).
func (s *Styles) Grad(ramp Ramp, text string) string {
	return gradRun(s.grad[clampRamp(ramp)][:], text)
}

// GradBold is Grad with the bold attribute already set.
func (s *Styles) GradBold(ramp Ramp, text string) string {
	return gradRun(s.gradBold[clampRamp(ramp)][:], text)
}

// gradRun renders one run against an already-built ramp.
func gradRun(steps []lipgloss.Style, text string) string {
	clusters := graphemes(text)
	if len(clusters) == 0 {
		return ""
	}
	var out strings.Builder
	for index, cluster := range clusters {
		out.WriteString(steps[stepIndex(index, len(clusters))].Render(cluster))
	}
	return out.String()
}

// graphemes splits text into grapheme clusters.
func graphemes(text string) []string {
	if text == "" {
		return nil
	}
	out := make([]string, 0, len(text))
	iterator := uniseg.NewGraphemes(text)
	for iterator.Next() {
		out = append(out, iterator.Str())
	}
	return out
}

// stepIndex is the ramp index of cluster index of a run of count clusters:
// round(index * (GradSteps-1) / (count-1)), which hits both endpoints exactly.
// A single-cluster run wears the ramp's lead color.
func stepIndex(index, count int) int {
	if count <= 1 {
		return 0
	}
	span := count - 1
	return (2*index*(GradSteps-1) + span) / (2 * span)
}

// clampRamp keeps an out-of-range ramp off the array bounds.
func clampRamp(ramp Ramp) Ramp {
	if ramp >= numRamps {
		return GradSection
	}
	return ramp
}

// meterModel configures the progress component of spec section 10.1.3: the
// GradMeter ramp spans the whole bar and the fill cuts it, so the bar's color
// position, not just its length, encodes how far along the work is.
//
// The fill glyph is the half block rather than the full one because the
// component doubles the ramp for it - each cell takes blend[i] as foreground
// and blend[i+1] as background - so a 24-cell meter resolves a 48-step ramp.
// The track glyph is load-bearing at ASCII: Downsample strips color and leaves
// runes, so a structure golden of the one widget whose job is position would
// assert nothing about position if fill and track were the same glyph.
//
// The percentage is off because the caller already renders i/N, and the spring
// is never engaged: Meter calls ViewAs, never SetPercent, so the component
// contributes no tick chain at all (spec section 10.2.2).
func meterModel(pal Palette) progress.Model {
	lead, tail := RampStops(GradMeter)
	built := progress.New(
		progress.WithColors(pal[lead], pal[tail]),
		progress.WithScaled(false),
		progress.WithFillCharacters(glyphRune(defaultGlyphs.Rail), glyphRune(defaultGlyphs.Track)),
		progress.WithoutPercentage(),
		progress.WithWidth(defaultMetrics.MeterCells),
	)
	built.EmptyColor = pal[FgMuted]
	return built
}

// glyphRune is the single rune of a one-cell glyph token, for the component
// options that take a rune rather than a string.
func glyphRune(glyph string) rune {
	for _, letter := range glyph {
		return letter
	}
	return ' '
}

// buildRamps blends every ramp once and caches its steps as styles. Spec
// section 6.2 is non-negotiable: every lipgloss.Style is constructed inside
// New, so 5 ramps x 24 steps x 2 weights are built here and never per frame.
func (s *Styles) buildRamps(pal Palette) {
	for ramp := Ramp(0); ramp < numRamps; ramp++ {
		stops := rampStops[ramp]
		blend := lipgloss.Blend1D(GradSteps, pal[stops[0]], pal[stops[1]])
		for step := range blend {
			s.grad[ramp][step] = s.blank.Foreground(blend[step])
			s.gradBold[ramp][step] = s.blankBold.Foreground(blend[step])
		}
	}
}
