package carddetail

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
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
	got := parityMarkdown("##\t Plan\n12.\t  item")
	for _, want := range []string{"**Plan**", "12\\. item"} {
		if !strings.Contains(got, want) {
			t.Fatalf("parity markdown missing %q:\n%s", want, got)
		}
	}
}

func TestRenderMarkdownPreservesSourceOrdinals(t *testing.T) {
	got := ansi.Strip(markdownWith(testStyles())("0. zero\n3. three\n9. nine\n001. padded", 60))
	for _, want := range []string{"0. zero", "3. three", "9. nine", "001. padded"} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered markdown missing source marker %q:\n%s", want, got)
		}
	}
	lines := "\n" + strings.TrimSpace(got) + "\n"
	if strings.Contains(lines, "\n4. three\n") || strings.Contains(lines, "\n1. padded\n") {
		t.Fatalf("rendered markdown renumbered source ordinals:\n%s", got)
	}
}

func TestFrozenJavaScriptBoundaryParity(t *testing.T) {
	got := inlineMarkdown("é_x_ _y_界 A_z_ _q_9", false)
	want := "é*x* *y*界 A\\_z\\_ \\_q\\_9"
	if got != want {
		t.Fatalf("ASCII word-boundary parity = %q, want %q", got, want)
	}

	nbsp := "\u00a0"
	got = parityMarkdown("https://a.test/x" + nbsp + "tail")
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

func TestRenderMarkdownPreservesJavaScriptControlWhitespace(t *testing.T) {
	got := ansi.Strip(markdownWith(testStyles())("*\vx*\n#\vheading\n1.\fitem", 60))
	for _, want := range []string{"* x*", "heading", "1. item"} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered control whitespace missing %q:\n%s", want, got)
		}
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
	if !strings.Contains(parityMarkdown("- kb://task/5\n## kb://task/6"), "- <kb://task/5>") {
		t.Errorf("a reference in a list item lost its link form:\n%s", parityMarkdown("- kb://task/5"))
	}
	// A rendered reference is the literal text the pointer map anchors on, so
	// glamour must not rewrite it into a label.
	rendered := ansi.Strip(markdownWith(testStyles())("see kb://task/12 now", 60))
	if !strings.Contains(rendered, "see kb://task/12 now") {
		t.Fatalf("rendered reference is not literal:\n%s", rendered)
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

// renderedText is the rendered body as the terminal shows it: styling stripped,
// blank rows dropped, and every row right-trimmed of the pad Glamour adds.
func renderedText(t *testing.T, source string) string {
	t.Helper()
	var kept []string
	for _, line := range strings.Split(ansi.Strip(markdownWith(testStyles())(source, 100)), "\n") {
		if trimmed := strings.TrimRight(line, " "); strings.TrimSpace(trimmed) != "" {
			kept = append(kept, strings.TrimLeft(trimmed, " "))
		}
	}
	return strings.Join(kept, "\n")
}

// TestRenderedMarkdownLeaksNoEscapes is the regression for issue #211. Glamour
// strips a fixed set of eighteen backslash pairs and nothing else, so an escape
// on any other character reaches the terminal as text. None of these sources
// carries a backslash, so any backslash in the rendered body is injected.
func TestRenderedMarkdownLeaksNoEscapes(t *testing.T) {
	for _, tt := range []struct {
		name   string
		source string
		want   string
	}{
		{name: "four space indent", source: "    key = value", want: "key = value"},
		{name: "eight space indent", source: "        deep: 1", want: "deep: 1"},
		{name: "tab indent", source: "\tkey = value", want: "key = value"},
		{name: "prose punctuation", source: "a, b: c = d; e? f 50% \"q\" 'r' $s ^t /u", want: "a, b: c = d; e? f 50% \"q\" 'r' $s ^t /u"},
		{name: "ampersand", source: "tea & coffee", want: "tea & coffee"},
		{name: "at sign", source: "ping @amit today", want: "ping @amit today"},
		{name: "setext equals underline", source: "heading\n=======", want: "heading\n======="},
		{name: "setext dash underline", source: "heading\n-------", want: "heading\n-------"},
		{name: "strikethrough", source: "~~strike~~", want: "~~strike~~"},
		{name: "single tilde pair", source: "~x~", want: "~x~"},
		{name: "tilde fence", source: "~~~", want: "~~~"},
		{name: "tilde fence with body", source: "~~~\nfoo\n~~~", want: "~~~\nfoo\n~~~"},
		{name: "definition list", source: "term\n: definition", want: "term\n: definition"},
		{name: "indented definition list", source: "term\n   : definition", want: "term\n: definition"},
		{name: "paren ordered marker", source: "1) one", want: "1) one"},
		{name: "indented bullet stays prose", source: "  - not a bullet", want: "- not a bullet"},
		{name: "indented heading stays prose", source: "  # not a heading", want: "# not a heading"},
		{name: "html stays literal", source: "<b>html</b>", want: "<b>html</b>"},
		{name: "table row stays literal", source: "| a | b |", want: "| a | b |"},
		{name: "www is not a link", source: "see www.example.com now", want: "see www.example.com now"},
		{name: "email is not a link", source: "mail a@b.example now", want: "mail a@b.example now"},
		{name: "character reference stays literal", source: "&#126; and &amp; and &lt;", want: "&#126; and &amp; and &lt;"},
		{name: "inside a heading", source: "## key = value & ~more~", want: "key = value & ~more~"},
		{name: "inside a bullet", source: "- key = value & ~more~", want: "\u2022 key = value & ~more~"},
		{name: "inside an ordinal", source: "3. key = value & ~more~", want: "3. key = value & ~more~"},
		{name: "inside bold", source: "**key = value & ~more~**", want: "key = value & ~more~"},
		{name: "inside italic", source: "*key = value & ~more~*", want: "key = value & ~more~"},
		{name: "inside a link label", source: "[a ~ b & c](https://x.test)", want: "a ~ b & c https://x.test"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := renderedText(t, tt.source)
			if strings.Contains(got, "\\") {
				t.Errorf("rendered body leaked an injected escape: %q", got)
			}
			if got != tt.want {
				t.Errorf("rendered %q = %q, want %q", tt.source, got, tt.want)
			}
		})
	}
}

// TestRenderedMarkdownKeepsTheFrozenScope pins the other half of the contract:
// narrowing the escape set must not let Glamour's wider grammar through, and
// the syntax the frozen renderer does understand still has to render.
func TestRenderedMarkdownKeepsTheFrozenScope(t *testing.T) {
	tick := string(rune(0x60))
	for _, tt := range []struct {
		name   string
		source string
		want   string
	}{
		{name: "bullet", source: "- item", want: "\u2022 item"},
		{name: "ordinal", source: "7. item", want: "7. item"},
		// Glamour pads a code span with no-break spaces rather than styling the
		// ticks, so the rendered span is wider than its source text.
		{name: "code span", source: "a " + tick + "b" + tick + " c", want: "a \u00a0b\u00a0 c"},
		{name: "literal backslash survives", source: "a \\ b", want: "a \\ b"},
		{name: "backslash is not an escape", source: "a \\x b", want: "a \\x b"},
		{name: "equals after a bullet", source: "- foo\n===", want: "\u2022 foo\n==="},
		{name: "blockquote stays prose", source: "> quote", want: "> quote"},
		{name: "deep heading stays prose", source: "#### four", want: "#### four"},
		{name: "thematic break stays prose", source: "---", want: "---"},
		{name: "plus bullet stays prose", source: "+ plus", want: "+ plus"},
		{name: "task box stays prose", source: "- [ ] todo", want: "\u2022 [ ] todo"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := renderedText(t, tt.source); got != tt.want {
				t.Errorf("rendered %q = %q, want %q", tt.source, got, tt.want)
			}
		})
	}
}
