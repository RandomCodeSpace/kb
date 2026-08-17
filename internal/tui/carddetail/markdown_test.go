package carddetail

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

	got := parityMarkdown(input)
	for _, want := range []string{
		"**Plan now**",
		"*italic*",
		"*also*",
		"snake\\_case",
		tick + "code" + tick,
		"[docs](https://example.com/x)",
		"<https://example.com/y>",
		"- bullet",
		"12. ordered",
		"\\#\\#\\#\\# not heading",
		"\\> quote",
		"\\~\\~strike\\~\\~",
		"\\| table \\|",
		"\\<b\\>html\\<\\/b\\>",
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
	got := parityMarkdown("##\t Plan\n12.\t  item")
	for _, want := range []string{"**Plan**", "12. item"} {
		if !strings.Contains(got, want) {
			t.Fatalf("parity markdown missing %q:\n%s", want, got)
		}
	}
}

func TestParityMarkdownUnterminatedFenceAndEmptyInput(t *testing.T) {
	fence := strings.Repeat(string(rune(0x60)), 3)
	got := parityMarkdown(fence + "\ncode\n~~~")
	if !strings.Contains(got, "~~~~\ncode\n~~~\n~~~~") {
		t.Fatalf("unterminated fence was not preserved safely:\n%s", got)
	}
	if got := parityMarkdown(""); got != "" {
		t.Fatalf("empty markdown = %q", got)
	}
}
