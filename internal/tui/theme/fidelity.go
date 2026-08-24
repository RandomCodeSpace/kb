package theme

import "github.com/charmbracelet/colorprofile"

// Fidelity is the terminal floor of spec section 10.7.5, resolved once from the
// detected color profile so no view ever branches on the profile itself.
// Truecolor is the reference target and everything below it is a degradation,
// never a second design.
//
// The word is deliberately not "tier": spec section 1.1 already spends that on
// the shade depth model.
type Fidelity uint8

// The three floors of spec section 10.7.5.
const (
	FidelityFlat    Fidelity = iota // no color at all: glyph, weight and geometry only
	FidelityIndexed                 // flat palette slots, no blends
	FidelityFull                    // gradients, blends and color-carried effects run
)

// FidelityFor resolves a detected profile to its floor.
//
// colorprofile.Unknown maps to FidelityFull because it only ever occurs before
// detection completes - bubbletea always resolves a profile before it sends
// ColorProfileMsg - and the reference target is the correct assumption until
// the terminal says otherwise. This mirrors spec section 6.3's rule of
// defaulting isDark to true until the background message lands.
func FidelityFor(profile colorprofile.Profile) Fidelity {
	switch profile {
	case colorprofile.TrueColor, colorprofile.Unknown:
		return FidelityFull
	case colorprofile.ANSI256, colorprofile.ANSI:
		return FidelityIndexed
	default:
		return FidelityFlat
	}
}

// Graded reports whether color-carried effects run on this terminal.
//
// Spec section 10.7.5: this may be read for exactly one purpose, deciding
// whether to *start* an effect - arm a tick chain, allocate a prerender cache.
// It may never be read to pick a color. A view that writes
// "if styles.Graded() { colorA } else { colorB }" has reintroduced the bespoke
// 256-color design the section forbids.
func (s *Styles) Graded() bool { return s.Fidelity == FidelityFull }
