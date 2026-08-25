package carddetail

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/pointer"
)

// taskRefScheme is the prefix a rendered row must contain before it is worth
// walking. Glamour renders the reference literally (issue #212), so the scan
// that anchors the hit regions is a substring search on the rendered body, not
// a second pass over the source markdown.
const taskRefScheme = "kb://task/"

// OpenTaskRefMsg asks the root to open the card a rendered kb://task reference
// addresses. The pane cannot resolve a sequence number itself: the board is the
// root's, and so is the card-opening path a click on a board card already
// takes, so the reference reuses it rather than growing a second one.
//
// It crosses the session guard of ResolvePointerMessage like every other detail
// pointer message, so a release queued from a pane that has since been reopened
// on another card opens nothing.
type OpenTaskRefMsg struct{ Seq int }

// taskRefHit is one rendered reference: the logical body row it landed on, the
// bytes it occupies in that row's rendered form, the cells it occupies on
// screen, and the card it addresses. The byte span is what pointer feedback is
// substituted into; the cell span is what the hit region is anchored at.
type taskRefHit struct {
	row       int
	byteStart int
	byteEnd   int
	column    int
	width     int
	seq       int
}

// detailTaskRefControlID identifies one rendered reference. The id is built
// from the logical row and column rather than from the screen position, so it
// survives scrolling: the same reference keeps its identity while the content
// moves under the pointer, which is what the re-resolve of spec section 10.5.2
// row 6 needs to stay lit on the run it was lit on.
func detailTaskRefControlID(hit taskRefHit) pointer.ControlID {
	return pointer.ControlID("detail.taskref." + strconv.Itoa(hit.row) + "." + strconv.Itoa(hit.column))
}

// taskRefHits finds every rendered reference in a body. Rows carrying no
// reference cost one substring search.
func taskRefHits(lines []string) []taskRefHit {
	var hits []taskRefHit
	for row, line := range lines {
		if !strings.Contains(line, taskRefScheme) {
			continue
		}
		hits = appendTaskRefHits(hits, row, line)
	}
	return hits
}

// appendTaskRefHits walks one rendered row, collecting the printable text with
// the byte offset and cell column every printable byte started at, and matches
// the reference pattern against that text alone. The walk is what keeps the
// scan honest: glamour wraps a link in an OSC 8 hyperlink whose parameters
// repeat the reference verbatim, and a naive search would anchor a region on
// an escape sequence that occupies no cells.
func appendTaskRefHits(hits []taskRefHit, row int, line string) []taskRefHit {
	text, offsets, columns := visibleRow(line)
	for _, index := range taskRefPattern.FindAllStringIndex(text, -1) {
		start, end := index[0], index[1]
		// A sequence number too large to be a card is not one, and skipping it
		// here is the only place that decision is cheap: the reference still
		// renders as a link, it simply addresses nothing. Every number that
		// parses gets a region, including the ones no card carries - the notice
		// that says so is the point.
		seq, err := strconv.Atoi(text[start+len(taskRefScheme) : end])
		if err != nil {
			continue
		}
		// The pattern is ASCII by construction, so one matched byte is one
		// cell and the run is contiguous in the rendered bytes.
		hits = append(hits, taskRefHit{
			row: row, byteStart: offsets[start], byteEnd: offsets[end-1] + 1,
			column: columns[start], width: end - start, seq: seq,
		})
	}
	return hits
}

// visibleRow decodes one rendered row into its printable text plus, for each of
// that text's bytes, the byte offset it came from and the cell column it lands
// on. Escape sequences contribute no text, no offset and no column.
func visibleRow(line string) (text string, offsets, columns []int) {
	var built strings.Builder
	state := byte(0)
	column := 0
	for cursor := 0; cursor < len(line); {
		sequence, width, size, next := ansi.DecodeSequence(line[cursor:], state, nil)
		state = next
		if width > 0 {
			built.WriteString(sequence)
			for offset := range len(sequence) {
				offsets = append(offsets, cursor+offset)
				columns = append(columns, column)
			}
			column += width
		}
		// A byte the decoder reports no progress on is still one byte of the
		// row: advancing past it is what keeps an undecodable tail from
		// spinning here forever.
		cursor += max(size, 1)
	}
	return built.String(), offsets, columns
}
