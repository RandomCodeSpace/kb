package mdparity

import (
	"reflect"
	"strings"
	"testing"
)

// text flattens the runs of one block, so a case that cares about structure
// rather than emphasis can name what the block says in one string.
func text(block Block) string {
	var out strings.Builder
	for _, run := range block.Runs {
		out.WriteString(run.Text)
	}
	return out.String()
}

// TestBlocksRecognizesTheFrozenGrammar is the card-scale half of the parity
// contract: the same four line branches Parity feeds glamour, resolved into
// runs instead of into escaped source. A construct outside the grammar stays
// prose here exactly as it stays literal there.
func TestBlocksRecognizesTheFrozenGrammar(t *testing.T) {
	tick := string(rune(0x60))
	fence := strings.Repeat(tick, 3)
	source := strings.Join([]string{
		"## Plan",
		"prose line",
		"- bullet",
		"12. ordered",
		"  - indented is prose",
		"#### four is prose",
		"> quote is prose",
		fence,
		"code line",
		fence,
	}, "\n")

	blocks := Blocks(source)
	want := []struct {
		kind   Kind
		marker string
		text   string
	}{
		{Heading, "", "Plan"},
		{Prose, "", "prose line"},
		{Bullet, "", "bullet"},
		{Ordered, "12.", "ordered"},
		{Prose, "", "- indented is prose"},
		{Prose, "", "#### four is prose"},
		{Prose, "", "> quote is prose"},
		{Code, "", "code line"},
	}
	if len(blocks) != len(want) {
		t.Fatalf("Blocks returned %d blocks, want %d: %+v", len(blocks), len(want), blocks)
	}
	for index, expected := range want {
		block := blocks[index]
		if block.Kind != expected.kind {
			t.Errorf("block %d kind = %d, want %d", index, block.Kind, expected.kind)
		}
		if block.Marker != expected.marker {
			t.Errorf("block %d marker = %q, want %q", index, block.Marker, expected.marker)
		}
		if got := text(block); got != expected.text {
			t.Errorf("block %d text = %q, want %q", index, got, expected.text)
		}
	}
}

// TestBlocksResolvesInlineEmphasis pins the run split. The markers themselves
// never reach a run: a card renders the emphasis, it does not print the syntax
// that asked for it.
func TestBlocksResolvesInlineEmphasis(t *testing.T) {
	tick := string(rune(0x60))
	source := "plain **bold** *slant* " + tick + "mono" + tick +
		" [label](https://x.test) https://y.test kb://task/7"
	blocks := Blocks(source)
	if len(blocks) != 1 {
		t.Fatalf("Blocks returned %d blocks, want 1", len(blocks))
	}
	want := []Run{
		{Text: "plain ", Emphasis: Plain},
		{Text: "bold", Emphasis: Strong},
		{Text: " ", Emphasis: Plain},
		{Text: "slant", Emphasis: Slant},
		{Text: " ", Emphasis: Plain},
		{Text: "mono", Emphasis: Mono},
		{Text: " ", Emphasis: Plain},
		{Text: "label", Emphasis: Anchor},
		{Text: " ", Emphasis: Plain},
		{Text: "https://y.test", Emphasis: Anchor},
		{Text: " ", Emphasis: Plain},
		{Text: "kb://task/7", Emphasis: Anchor},
	}
	if !reflect.DeepEqual(blocks[0].Runs, want) {
		t.Errorf("runs =\n %+v\nwant\n %+v", blocks[0].Runs, want)
	}
	for _, run := range blocks[0].Runs {
		if strings.ContainsAny(run.Text, "*`[]()") {
			t.Errorf("run %q kept its markup", run.Text)
		}
	}
}

// TestHeadingFlattensItsEmphasis matches the frozen renderer, which draws a
// heading bold and flattens the bold inside it. A card heading is therefore one
// bold line rather than a line with a bolder stretch in the middle of it, while
// code and links inside it keep their own identity because the frozen renderer
// keeps theirs.
func TestHeadingFlattensItsEmphasis(t *testing.T) {
	tick := string(rune(0x60))
	blocks := Blocks("## plan **now** *soon* " + tick + "kb" + tick)
	if len(blocks) != 1 || blocks[0].Kind != Heading {
		t.Fatalf("Blocks returned %+v, want one heading", blocks)
	}
	want := []Run{
		{Text: "plan now soon ", Emphasis: Strong},
		{Text: "kb", Emphasis: Mono},
	}
	if !reflect.DeepEqual(blocks[0].Runs, want) {
		t.Errorf("heading runs =\n %+v\nwant\n %+v", blocks[0].Runs, want)
	}
}

// TestBlocksSkipsEmptyLines is the card's row budget talking: a blank source
// line is a row of description the reader does not get, and the card has few
// enough rows that spending one on nothing is a real loss.
func TestBlocksSkipsEmptyLines(t *testing.T) {
	blocks := Blocks("one\n\n   \n\ntwo")
	if len(blocks) != 2 {
		t.Fatalf("Blocks returned %d blocks, want 2: %+v", len(blocks), blocks)
	}
	if text(blocks[0]) != "one" || text(blocks[1]) != "two" {
		t.Errorf("blocks = %q and %q", text(blocks[0]), text(blocks[1]))
	}
	if got := Blocks(""); len(got) != 0 {
		t.Errorf("Blocks(\"\") = %+v, want none", got)
	}
}

// TestBlocksKeepsAFenceLiteral covers the grammar's one multi-line construct at
// card scale: the fence rows themselves are syntax and do not render, every
// line between them is one Mono block, and a blank code line is dropped like
// any other blank row.
func TestBlocksKeepsAFenceLiteral(t *testing.T) {
	fence := strings.Repeat(string(rune(0x60)), 3)
	blocks := Blocks(fence + "go\nkeep := **not bold**\n\n" + fence + "\nafter")
	if len(blocks) != 2 {
		t.Fatalf("Blocks returned %d blocks, want 2: %+v", len(blocks), blocks)
	}
	if blocks[0].Kind != Code || text(blocks[0]) != "keep := **not bold**" {
		t.Errorf("code block = %+v, want the source line verbatim", blocks[0])
	}
	if blocks[1].Kind != Prose || text(blocks[1]) != "after" {
		t.Errorf("block after the fence = %+v", blocks[1])
	}
	// An unterminated fence swallows the rest of the source rather than
	// spilling markdown syntax onto the card as prose.
	unterminated := Blocks(fence + "\nstill code")
	if len(unterminated) != 1 || unterminated[0].Kind != Code {
		t.Errorf("unterminated fence = %+v, want one code block", unterminated)
	}
}

// TestAppendRunMergesAdjacentEmphasis keeps the run list minimal: a wrapper
// measures and styles runs, and two neighbours it would style identically are a
// break it would have to undo.
func TestAppendRunMergesAdjacentEmphasis(t *testing.T) {
	runs := appendRun(nil, "", Plain)
	if len(runs) != 0 {
		t.Fatalf("an empty run was appended: %+v", runs)
	}
	runs = appendRun(runs, "a", Plain)
	runs = appendRun(runs, "b", Plain)
	runs = appendRun(runs, "c", Strong)
	want := []Run{{Text: "ab", Emphasis: Plain}, {Text: "c", Emphasis: Strong}}
	if !reflect.DeepEqual(runs, want) {
		t.Errorf("runs = %+v, want %+v", runs, want)
	}
}

// TestMatchEmphasisIsTotal pins the mapping for every inline kind the grammar
// can produce, including a kind it cannot: an unrecognized match falls back to
// the line's base emphasis rather than to an unstyled run.
func TestMatchEmphasisIsTotal(t *testing.T) {
	cases := []struct {
		kind inlineKind
		base Emphasis
		want Emphasis
	}{
		{inlineCode, Plain, Mono},
		{inlineCode, Strong, Mono},
		{inlineBold, Plain, Strong},
		{inlineItalic, Plain, Slant},
		{inlineItalic, Strong, Strong},
		{inlineLink, Plain, Anchor},
		{inlineAutoLink, Plain, Anchor},
		{inlineKind(99), Strong, Strong},
	}
	for _, testCase := range cases {
		if got := matchEmphasis(testCase.kind, testCase.base); got != testCase.want {
			t.Errorf("matchEmphasis(%d, %d) = %d, want %d",
				testCase.kind, testCase.base, got, testCase.want)
		}
	}
}

// TestTaskRefPatternIsTheGrammarsOwnMatcher holds the seam issue #212 opened:
// the pointer map that anchors a hit region on a rendered reference and the
// grammar that decides a reference is an autolink are one expression. A pane
// that linked a reference the map could not find, or found one the pane did not
// link, would be a fork.
func TestTaskRefPatternIsTheGrammarsOwnMatcher(t *testing.T) {
	pattern := TaskRefPattern()
	if pattern == nil {
		t.Fatal("TaskRefPattern returned nil")
	}
	if got := pattern.FindString("see kb://task/12 now"); got != "kb://task/12" {
		t.Errorf("matched %q", got)
	}
	if pattern.MatchString("kb://task/") {
		t.Error("a reference carrying no digits addresses no card and is not one")
	}
}

// TestEscapeHelpersCoverTheirEdges reaches the branches the parity corpus above
// does not: the leading colon a backslash cannot neutralize, a URL scan with
// only one of the two schemes present, and the two characters that travel as
// character references because Glamour strips no backslash pair for them.
func TestEscapeHelpersCoverTheirEdges(t *testing.T) {
	if got := escapeLeadingColon("no colon"); got != "no colon" {
		t.Errorf("escapeLeadingColon rewrote %q", got)
	}
	if got := escapeLeadingColon(": definition"); got != "&#58; definition" {
		t.Errorf("escapeLeadingColon = %q", got)
	}
	if got := nextURLStart("plain text", 0); got != -1 {
		t.Errorf("nextURLStart on text with no scheme = %d", got)
	}
	if got := nextURLStart("a http://x.test", 0); got != 2 {
		t.Errorf("nextURLStart on http alone = %d", got)
	}
	if got := nextURLStart("a https://x.test", 0); got != 2 {
		t.Errorf("nextURLStart on https alone = %d", got)
	}
	if got := escapeMarkdown("a&b~c"); got != "a&amp;b&#126;c" {
		t.Errorf("escapeMarkdown = %q", got)
	}
}
