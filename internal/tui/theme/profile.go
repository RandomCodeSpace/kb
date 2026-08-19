package theme

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
)

// Color profile pinning for goldens, spec section 6.4. The renderer downsamples
// per cell against the detected profile, so a golden cell grid depends on the
// profile the program ran under. Production pins nothing; tests pin explicitly.
//
// ColorProfile is the profile for palette goldens. An ASCII-pinned golden of a
// design whose entire depth model is background color asserts nothing about the
// design, so every board and overlay color golden pins truecolor.
const ColorProfile = colorprofile.TrueColor

// StructureProfile is the profile for goldens that assert layout, truncation
// and drop order rather than color. They strip ANSI anyway and stay cheap.
const StructureProfile = colorprofile.ASCII

// PinProfile returns the program option that pins a golden's color profile.
func PinProfile(profile colorprofile.Profile) tea.ProgramOption {
	return tea.WithColorProfile(profile)
}

// PinColor pins a program to the truecolor profile of the reference design.
func PinColor() tea.ProgramOption { return PinProfile(ColorProfile) }

// PinStructure pins a program to the colorless profile used by layout goldens.
func PinStructure() tea.ProgramOption { return PinProfile(StructureProfile) }

// Downsample rewrites rendered content as the given profile would emit it. It
// is the string-level counterpart of PinProfile, for widget goldens that render
// directly instead of running a program. An unrecognized profile leaves the
// content untouched rather than dropping it.
func Downsample(content string, profile colorprofile.Profile) string {
	var out strings.Builder
	writer := &colorprofile.Writer{Forward: &out, Profile: profile}
	if _, err := writer.WriteString(content); err != nil {
		return content
	}
	return out.String()
}
