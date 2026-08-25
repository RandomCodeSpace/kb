package mdparity

import (
	"strings"
	"testing"
)

func TestParityMarkdownKeepsOnlyFrozenScope(t *testing.T) {
	tick := string(rune(0x60))
	fence := strings.Repeat(tick, 3)
	input := strings.Join([]string{
		"## Plan **now**",
		"plain *italic* _also_ snake_case " + tick + "code" + tick + " [docs](https://example.com/x) https://example.com/y.",
		"- bullet",
		"12. ordered",
		"#### not heading",
		"> quote",
		"~~strike~~",
		"| table |",
		"<b>html</b>",
		fence + "go",
		"raw **code**",
		"~~~~",
		fence,
	}, "\n")

	got := Parity(input)
	for _, want := range []string{
		"**Plan now**",
		"*italic*",
		"*also*",
		"snake\\_case",
		tick + "code" + tick,
		"[docs](https://example.com/x)",
		"<https://example.com/y>",
		"- bullet",
		"12\\. ordered",
		"\\#\\#\\#\\# not heading",
		"\\> quote",
		// Glamour strips no "\~" pair, so the tilde travels as the character
		// reference it resolves instead (issue #211).
		"&#126;&#126;strike&#126;&#126;",
		"\\| table \\|",
		// The slash is not syntactic anywhere in the grammar, so it is no
		// longer escaped; the angle brackets around it still are.
		"\\<b\\>html\\</b\\>",
		"raw **code**\n~~~~\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("parity markdown missing %q:\n%s", want, got)
		}
	}
}

func TestInlineMarkdownRejectsMalformedAndOverlappingSyntax(t *testing.T) {
	tick := string(rune(0x60))
	input := "**bold " + tick + "wins" + tick + "** [bad](javascript:alert(1)) [empty]() [open](https://example.com snake_case _x_ * nope*"
	got := inlineMarkdown(input, false)
	for _, want := range []string{"**bold \\" + tick + "wins\\" + tick + "**", "\\[bad\\]", "\\[empty\\]", "snake\\_case", "*x*", "\\* nope\\*"} {
		if !strings.Contains(got, want) {
			t.Errorf("inline markdown missing %q: %s", want, got)
		}
	}

	if got := inlineMarkdown("**heading**", true); got != "heading" {
		t.Fatalf("bold inside flattened heading = %q", got)
	}
}

func TestBlockAndBoundaryHelpers(t *testing.T) {
	tests := []struct {
		line      string
		heading   int
		markerEnd int
		textStart int
	}{
		{line: "# one", heading: 1},
		{line: "### three", heading: 3},
		{line: "##\t  Plan", heading: 2},
		{line: "#### four"},
		{line: "#nospace"},
		{line: "1. one", markerEnd: 2, textStart: 3},
		{line: "123. many", markerEnd: 4, textStart: 5},
		{line: "12.\t  many", markerEnd: 3, textStart: 6},
		{line: "1234. too many"},
		{line: ". none"},
	}
	for _, tt := range tests {
		level, _, _ := heading(tt.line)
		if level != tt.heading {
			t.Errorf("heading(%q) = %d, want %d", tt.line, level, tt.heading)
		}
		markerEnd, textStart := orderedPrefix(tt.line)
		if markerEnd != tt.markerEnd || textStart != tt.textStart {
			t.Errorf("orderedPrefix(%q) = (%d, %d), want (%d, %d)", tt.line, markerEnd, textStart, tt.markerEnd, tt.textStart)
		}
	}

	if got := safeFence([]string{"~", "~~~~", "plain"}); got != "~~~~~" {
		t.Fatalf("safeFence = %q, want five tildes", got)
	}
	if firstRune("界x") != '界' || lastRune("x界") != '界' {
		t.Fatal("rune helpers lost Unicode")
	}
	if !wordBefore("a_", 1) || wordBefore("_", 0) || !wordAfter("_a", 1) || wordAfter("_", 1) {
		t.Fatal("word-boundary helpers returned the wrong result")
	}
}

func TestParityMarkdownAcceptsWebWhitespace(t *testing.T) {
	got := Parity("##\t Plan\n12.\t  item")
	for _, want := range []string{"**Plan**", "12\\. item"} {
		if !strings.Contains(got, want) {
			t.Fatalf("parity markdown missing %q:\n%s", want, got)
		}
	}
}

func TestFrozenJavaScriptBoundaryParity(t *testing.T) {
	got := inlineMarkdown("é_x_ _y_界 A_z_ _q_9", false)
	want := "é*x* *y*界 A\\_z\\_ \\_q\\_9"
	if got != want {
		t.Fatalf("ASCII word-boundary parity = %q, want %q", got, want)
	}

	nbsp := "\u00a0"
	got = Parity("https://a.test/x" + nbsp + "tail")
	if !strings.Contains(got, "<https://a.test/x>"+nbsp+"tail") {
		t.Fatalf("bare URL crossed JavaScript whitespace: %q", got)
	}
	got = inlineMarkdown("[bad](https://x.test/"+nbsp+"tail) [ok](https://ok.test)", false)
	if strings.Contains(got, "[bad](") || !strings.Contains(got, "[ok](https://ok.test)") {
		t.Fatalf("explicit-link whitespace parity = %q", got)
	}
	if _, _, ok := heading("#\u0085text"); ok {
		t.Fatal("U+0085 incorrectly matched JavaScript whitespace")
	}
	if marker, _ := orderedPrefix("1.\u0085text"); marker != 0 {
		t.Fatal("U+0085 incorrectly started an ordered item")
	}
}

func TestNodeDerivedLineSeparatorVectors(t *testing.T) {
	for _, tt := range []struct {
		line        string
		headingText string
		ordinalText string
	}{
		{line: "#\u2028head", headingText: "head"},
		{line: "1.\u2029item", ordinalText: "item"},
		{line: "# head\u2028tail"},
		{line: "# head\u2029tail"},
		{line: "# head\u2028"},
		{line: "1. item\u2028tail"},
		{line: "1. item\u2029tail"},
		{line: "1. item\u2029"},
	} {
		_, headingText, headingOK := heading(tt.line)
		if headingOK != (tt.headingText != "") || headingText != tt.headingText {
			t.Errorf("heading(%q) = (%q, %v), want (%q, %v)", tt.line, headingText, headingOK, tt.headingText, tt.headingText != "")
		}
		markerEnd, textStart := orderedPrefix(tt.line)
		ordinalOK := markerEnd > 0
		ordinalText := ""
		if ordinalOK {
			ordinalText = tt.line[textStart:]
		}
		if ordinalOK != (tt.ordinalText != "") || ordinalText != tt.ordinalText {
			t.Errorf("orderedPrefix(%q) = (%q, %v), want (%q, %v)", tt.line, ordinalText, ordinalOK, tt.ordinalText, tt.ordinalText != "")
		}
	}
}

// TestTaskReferenceIsAnAutolinkAndNothingElseIs is the matcher half of issue
// #212: kb's own scheme reaches glamour as an autolink, so escaping never
// touches its slashes, and the three things that only look like a reference
// stay literal text.
func TestTaskReferenceIsAnAutolinkAndNothingElseIs(t *testing.T) {
	got := inlineMarkdown("see kb://task/12, xkb://task/13, kb://task/ and https://x.test/kb://task/14", false)
	for _, want := range []string{"<kb://task/12>", "<https://x.test/kb://task/14>"} {
		if !strings.Contains(got, want) {
			t.Errorf("inline markdown missing %q: %s", want, got)
		}
	}
	for _, reject := range []string{"<kb://task/13>", "<kb://task/>", "<kb://task/14>"} {
		if strings.Contains(got, reject) {
			t.Errorf("inline markdown linked %q: %s", reject, got)
		}
	}
	if !strings.Contains(Parity("- kb://task/5\n## kb://task/6"), "- <kb://task/5>") {
		t.Errorf("a reference in a list item lost its link form:\n%s", Parity("- kb://task/5"))
	}
}

func TestParityMarkdownUnterminatedFenceAndEmptyInput(t *testing.T) {
	fence := strings.Repeat(string(rune(0x60)), 3)
	got := Parity(fence + "\ncode\n~~~")
	if !strings.Contains(got, "~~~~\ncode\n~~~\n~~~~") {
		t.Fatalf("unterminated fence was not preserved safely:\n%s", got)
	}
	if got := Parity(""); got != "" {
		t.Fatalf("empty markdown = %q", got)
	}
}
