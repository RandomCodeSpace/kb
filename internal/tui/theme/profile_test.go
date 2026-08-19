package theme

import (
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
)

func TestGoldenProfilesMatchTheSpec(t *testing.T) {
	if ColorProfile != colorprofile.TrueColor {
		t.Errorf("palette goldens must pin truecolor, got %v", ColorProfile)
	}
	if StructureProfile != colorprofile.ASCII {
		t.Errorf("structure goldens must stay ASCII-pinned, got %v", StructureProfile)
	}
}

func TestPinHelpersProduceProgramOptions(t *testing.T) {
	if PinProfile(colorprofile.ANSI256) == nil {
		t.Error("PinProfile returned no program option")
	}
	if PinColor() == nil {
		t.Error("PinColor returned no program option")
	}
	if PinStructure() == nil {
		t.Error("PinStructure returned no program option")
	}
}

func TestDownsampleStripsColorForAsciiGoldens(t *testing.T) {
	styles := New(true)
	rendered := styles.On(FgBase, Card).Render("card")
	if !strings.Contains(rendered, "\x1b[") {
		t.Fatalf("expected a colored render, got %q", rendered)
	}
	if got := Downsample(rendered, StructureProfile); strings.Contains(got, "38;2;") {
		t.Errorf("ASCII downsample kept truecolor sequences: %q", got)
	}
	if got := Downsample(rendered, ColorProfile); got != rendered {
		t.Errorf("truecolor downsample must pass through, got %q", got)
	}
	if got := Downsample(rendered, colorprofile.ANSI256); strings.Contains(got, "38;2;") {
		t.Errorf("256 downsample kept truecolor sequences: %q", got)
	}
}

func TestDownsampleLeavesContentAloneOnAnUnknownProfile(t *testing.T) {
	const content = "plain"
	if got := Downsample(content, colorprofile.Profile(99)); got != content {
		t.Errorf("unknown profile returned %q, want the content untouched", got)
	}
}
