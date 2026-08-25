package widget

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/mdparity"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// wrapFields is the greedy word wrap of spec section 3.3 over a run of lines
// whose widths differ. The card's title row is the case that needs it: the
// first row gives its right end to the blocked alarm and the sequence number,
// and the rows under it take the whole content width.
//
// The rules are section 3.3's, unchanged by the wrap gaining a second row: a
// word longer than its field is hard-truncated rather than overflowing, and the
// last allotted line is the one that carries the ellipsis when text remains, so
// the wrap can never run past the card.
func wrapFields(styles *theme.Styles, text string, fields []int) []string {
	out := make([]string, 0, len(fields))
	words := strings.Fields(text)
	index := 0
	for row, width := range fields {
		if width <= 0 {
			out = append(out, "")
			continue
		}
		line := ""
		for index < len(words) {
			candidate := words[index]
			if line != "" {
				candidate = line + " " + words[index]
			}
			if ansi.StringWidth(candidate) <= width {
				line, index = candidate, index+1
				continue
			}
			if line == "" {
				line, index = truncate(styles, words[index], width), index+1
			}
			break
		}
		if row == len(fields)-1 && index < len(words) {
			line = truncate(styles, strings.TrimSpace(line+" "+strings.Join(words[index:], " ")), width)
			index = len(words)
		}
		out = append(out, line)
	}
	return out
}

// styledWord is one wrappable token of the card description: the text, the
// style its emphasis resolved to, and whether the block it belongs to starts
// here. Wrapping happens over words rather than over rendered lines because a
// style carries ANSI bytes that are not cells, and a wrap that measured them
// would break at the wrong column.
type styledWord struct {
	text  string
	style lipgloss.Style
	stop  bool // this word starts a new block, so it starts a new line
}

// descWords reduces a description to the wrappable words of spec section 3.3.
// The grammar is the mdparity package's - the same one the card detail pane
// hands to glamour - and this is the card-scale output stage for it.
//
// Emphasis is an attribute or a foreground and never a cell, so a description
// that carries markdown wraps to exactly the rows the same text would without
// it. That is what lets the row grid stay a function of density and frame
// height while the content inside it varies.
func descWords(styles *theme.Styles, source string, surface theme.Slot) []styledWord {
	base := styles.On(theme.FgMuted, surface)
	emphasis := map[mdparity.Emphasis]lipgloss.Style{
		mdparity.Plain:  base,
		mdparity.Strong: styles.OnBold(theme.FgBase, surface),
		mdparity.Slant:  styles.OnItalic(theme.FgMuted, surface),
		// The code and link foregrounds are the ones spec section 5.2 gives
		// glamour for the same two constructs, so a description reads the same
		// on the card and in the pane that opens from it.
		mdparity.Mono:   styles.On(theme.HueDoing, surface),
		mdparity.Anchor: styles.On(theme.StatusInfo, surface),
	}
	var words []styledWord
	for _, block := range mdparity.Blocks(source) {
		stop := true
		if marker := blockMarker(styles, block); marker != "" {
			words = append(words, styledWord{text: marker, style: base, stop: true})
			stop = false
		}
		for _, run := range block.Runs {
			style, ok := emphasis[run.Emphasis]
			if !ok {
				style = base
			}
			for _, field := range strings.Fields(run.Text) {
				words = append(words, styledWord{text: field, style: style, stop: stop})
				stop = false
			}
		}
	}
	return words
}

// blockMarker is the literal a block prefixes its text with. A bullet's marker
// is vocabulary and comes from the glyph table; an ordinal's is the author's own
// text and is kept exactly as they typed it, because the frozen renderer
// preserves every ordinal rather than renumbering a run of them.
//
// The bullet is one cell and East Asian Ambiguous, so it is bound by the
// section 10.4.1 adjacency rule; it is a word of its own here and the wrap puts
// a space after it, which is the column the rule asks for.
func blockMarker(styles *theme.Styles, block mdparity.Block) string {
	switch block.Kind {
	case mdparity.Bullet:
		return styles.Glyph.Bullet
	case mdparity.Ordered:
		return block.Marker
	default:
		return ""
	}
}

// wrapWords is the greedy wrap of spec section 3.3 over styled words. A word
// that starts a block starts a line, a word too wide for the field is
// truncated, and the last allotted line carries the ellipsis when words remain.
func wrapWords(styles *theme.Styles, words []styledWord, base lipgloss.Style, width, lines int) []string {
	out := make([]string, 0, lines)
	if width <= 0 {
		for len(out) < lines {
			out = append(out, "")
		}
		return out
	}
	index := 0
	for len(out) < lines {
		line, used := "", 0
		for index < len(words) {
			word := words[index]
			if used > 0 && word.stop {
				break
			}
			separator := 0
			if used > 0 {
				separator = 1
			}
			text, cells := word.text, ansi.StringWidth(word.text)
			if used == 0 && cells > width {
				text, cells = truncate(styles, word.text, width), width
			}
			if used+separator+cells > width {
				break
			}
			if separator == 1 {
				line += base.Render(" ")
			}
			line += word.style.Render(text)
			used += separator + cells
			index++
		}
		if len(out) == lines-1 && index < len(words) {
			// The ellipsis is rendered in the base style rather than appended
			// raw: a card row is a filled surface, and a tail with no
			// background would punch a hole in the shade tier.
			line = ansi.Truncate(line, max(width-1, 0), "") + base.Render(styles.Glyph.Ellipsis)
		}
		out = append(out, line)
	}
	return out
}
