package carddetail

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

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

func TestRenderMarkdownPreservesJavaScriptControlWhitespace(t *testing.T) {
	got := ansi.Strip(markdownWith(testStyles())("*\vx*\n#\vheading\n1.\fitem", 60))
	for _, want := range []string{"* x*", "heading", "1. item"} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered control whitespace missing %q:\n%s", want, got)
		}
	}
}

// TestRenderedTaskReferenceIsLiteral is the render half of issue #212: the
// matcher lives in the mdparity package now, but a rendered reference is the
// literal text the pointer map anchors its hit region on, so glamour must not
// rewrite it into a label.
func TestRenderedTaskReferenceIsLiteral(t *testing.T) {
	rendered := ansi.Strip(markdownWith(testStyles())("see kb://task/12 now", 60))
	if !strings.Contains(rendered, "see kb://task/12 now") {
		t.Fatalf("rendered reference is not literal:\n%s", rendered)
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
