package mdparity

import "strings"

// Kind is the block a source line reduced to under the frozen grammar. The
// board card draws each kind at card scale; the card detail pane reaches the
// same set through glamour. Spec section 3.3 (issue #232).
type Kind uint8

const (
	// Prose is any line the grammar does not claim as one of the others.
	Prose Kind = iota
	// Heading is a level 1-3 ATX heading. Its marker is not rendered.
	Heading
	// Bullet is a "- " list item. Its marker is a glyph the render site owns.
	Bullet
	// Ordered is an "N. " list item. Its marker is source text and is kept
	// literal: the frozen renderer preserves every ordinal rather than
	// renumbering runs of them.
	Ordered
	// Code is a line inside a fence. Nothing inside it is inline syntax.
	Code
)

// Emphasis is the inline treatment one run of a line carries.
type Emphasis uint8

const (
	// Plain is unemphasized prose.
	Plain Emphasis = iota
	// Strong is **bold**.
	Strong
	// Slant is *italic* or _italic_.
	Slant
	// Mono is a `code` span.
	Mono
	// Anchor is a link, an autolink or a kb task reference. The rendered text
	// is the label for an explicit link and the target itself otherwise; the
	// card has no width for a href beside its label.
	Anchor
)

// Run is one stretch of a line carrying a single emphasis.
type Run struct {
	Text     string
	Emphasis Emphasis
}

// Block is one reduced source line: its kind, the literal marker the grammar
// requires be kept (an ordinal, and nothing else), and its inline runs.
type Block struct {
	Kind   Kind
	Marker string
	Runs   []Run
}

// Blocks reduces a description to the card-scale form of the frozen grammar:
// the same recognizers Parity feeds glamour, resolved into runs a renderer can
// style directly instead of into Markdown source.
//
// The two forms differ only in their output. Parity has to hand glamour a
// document, so it escapes everything outside the grammar and spends a blank
// line between blocks; a card has neither the rows for blank lines nor a second
// parser to defend itself from, so it takes the runs. What each recognizes is
// the same code, which is the point of the package.
//
// A blank source line yields no block: the card's row budget is small enough
// that spending one on nothing is a row of description the reader does not get.
func Blocks(source string) []Block {
	lines := strings.Split(source, "\n")
	out := make([]Block, 0, len(lines))
	for index := 0; index < len(lines); index++ {
		raw := lines[index]
		if strings.HasPrefix(raw, fenceMarker) {
			for index++; index < len(lines) && !strings.HasPrefix(lines[index], fenceMarker); index++ {
				if text := lines[index]; strings.TrimSpace(text) != "" {
					out = append(out, Block{Kind: Code, Runs: []Run{{Text: text, Emphasis: Mono}}})
				}
			}
			continue
		}
		if block, ok := cardBlock(raw); ok {
			out = append(out, block)
		}
	}
	return out
}

// fenceMarker is the three-backtick opener of the frozen grammar's only
// multi-line construct.
var fenceMarker = strings.Repeat(string(rune(0x60)), 3)

// cardBlock is parityLine's other half: the same four branches, resolved into
// runs rather than into escaped source.
func cardBlock(raw string) (Block, bool) {
	if _, text, ok := heading(raw); ok {
		return Block{Kind: Heading, Runs: emphasize(text, Strong)}, true
	}
	if text, found := strings.CutPrefix(raw, "- "); found {
		return Block{Kind: Bullet, Runs: emphasize(text, Plain)}, true
	}
	if markerEnd, textStart := orderedPrefix(raw); markerEnd > 0 {
		return Block{Kind: Ordered, Marker: raw[:markerEnd], Runs: emphasize(raw[textStart:], Plain)}, true
	}
	// Indentation is not syntax, exactly as it is not for Parity: the frozen
	// renderer matched its markers at column zero only and sent every other
	// line out as prose in a block that collapsed the leading run away.
	text := strings.TrimLeft(raw, " \t")
	if text == "" {
		return Block{}, false
	}
	return Block{Kind: Prose, Runs: emphasize(text, Plain)}, true
}

// emphasize splits one line into runs at the inline matches of the frozen
// grammar. base is the emphasis unmatched text carries, which is Strong inside
// a heading: the frozen renderer draws a heading bold and flattens the bold
// inside it, so a card heading is one bold line rather than a line with a
// bolder stretch in it.
func emphasize(line string, base Emphasis) []Run {
	runs := make([]Run, 0, 4)
	last := 0
	for _, match := range inlineMatches(line) {
		if match.start < last {
			continue
		}
		runs = appendRun(runs, line[last:match.start], base)
		runs = appendRun(runs, match.text, matchEmphasis(match.kind, base))
		last = match.end
	}
	return appendRun(runs, line[last:], base)
}

// matchEmphasis maps one inline match onto its card emphasis. Inside a heading
// every run keeps the heading's own emphasis for anything the frozen renderer
// flattens there - bold - while code and links keep their identity, because the
// frozen renderer keeps theirs.
func matchEmphasis(kind inlineKind, base Emphasis) Emphasis {
	switch kind {
	case inlineCode:
		return Mono
	case inlineBold:
		return Strong
	case inlineItalic:
		if base == Strong {
			return Strong
		}
		return Slant
	case inlineLink, inlineAutoLink:
		return Anchor
	default:
		return base
	}
}

// appendRun drops empty text and merges a run into its predecessor when the two
// carry the same emphasis, so a line never yields two adjacent runs a renderer
// would style identically and a wrapper would have to rejoin.
func appendRun(runs []Run, text string, emphasis Emphasis) []Run {
	if text == "" {
		return runs
	}
	if last := len(runs) - 1; last >= 0 && runs[last].Emphasis == emphasis {
		runs[last].Text += text
		return runs
	}
	return append(runs, Run{Text: text, Emphasis: emphasis})
}
