package widget

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// ErrorOpts describes one panel-scope error block. Spec section 10.8.6.
//
// On is the surface slot the block sits on, which is what selects the hue: the
// caller names its tier and never its slot. Key and Verb are the retry tail,
// which names the control that started the operation rather than growing a
// Retry button of its own; an error with no retryable trigger leaves them
// empty.
type ErrorOpts struct {
	Message  string
	Key      string
	Verb     string
	On       theme.Slot
	Width    int
	MaxLines int
}

// Error renders the error block of spec section 10.8.5 as one to MaxLines rows.
//
// It owns the whole treatment: sanitize, greedy wrap to the caller's measure,
// the hanging indent that makes the block read as one object, the truncation
// mark of spec section 3.3 - never the bare ansi.Truncate the fit helpers use -
// and the retry tail.
//
// The hue is the message's foreground and never a filled chip. Spec section
// 10.8.5 measured the pairs: StatusDanger fails AA on OverlaySurf at 2.96, so
// a panel takes TintDanger at 4.91 and the board tiers keep StatusDanger.
func Error(styles *theme.Styles, opts ErrorOpts) []string {
	if opts.Width <= 0 {
		return nil
	}
	metrics := styles.Metrics
	maxLines := max(opts.MaxLines, 1)
	hue := styles.On(errorSlot(opts.On), opts.On)
	indent := ansi.StringWidth(styles.Glyph.Alert) + 1
	tail := errorTail(styles, opts)
	tailCost := 0
	if tail != "" {
		tailCost = metrics.ActionGap + ansi.StringWidth(tail)
	}
	measure := max(opts.Width-indent, 1)
	wrapped := wrapError(styles, sanitizeError(opts.Message), measure, maxLines, tailCost)
	if len(wrapped) == 0 {
		return nil
	}
	rows := make([]string, 0, len(wrapped))
	for index, line := range wrapped {
		head := pad(hue, indent)
		if index == 0 {
			head = hue.Render(styles.Glyph.Alert + " ")
		}
		rows = append(rows, clip(head+hue.Render(line), opts.Width))
	}
	if tail == "" || len(rows) == 0 {
		return rows
	}
	last := len(rows) - 1
	room := opts.Width - ansi.StringWidth(rows[last]) - metrics.ActionGap
	if room < ansi.StringWidth(tail) {
		return rows
	}
	rows[last] += pad(styles.On(theme.FgBase, opts.On), metrics.ActionGap) + tail
	return rows
}

// errorSlot is the hue the message takes on the tier it sits on. Ratified call
// 12 of the spec: no error renders in a band row, so a band slot never reaches
// here and the split is between the panel tier and the board tiers.
func errorSlot(on theme.Slot) theme.Slot {
	if on == theme.OverlaySurf || on == theme.OverlayBand {
		return theme.TintDanger
	}
	return theme.StatusDanger
}

// errorTail is the retry tail: the key or button label that will run the
// operation again, rendered like an empty state's tail because it means the
// same thing.
func errorTail(styles *theme.Styles, opts ErrorOpts) string {
	if opts.Key == "" {
		return ""
	}
	tail := styles.OnBold(theme.FgBase, opts.On).Render(opts.Key)
	if opts.Verb == "" {
		return tail
	}
	return tail + styles.On(theme.FgSubtle, opts.On).Render(" "+opts.Verb)
}

// sanitizeError is step 1 of the wrapping rule: control characters to spaces
// and every newline collapsed, so an embedded newline never reaches the row
// grid. Errors carry store paths, URLs and wrapped Go error chains, and any of
// those can arrive with a newline in it.
func sanitizeError(message string) string {
	return strings.Map(func(r rune) rune {
		if r <= 0x1f || (r >= 0x7f && r <= 0x9f) {
			return ' '
		}
		return r
	}, ansi.Strip(message))
}

// wrapError wraps greedily to measure, at most maxLines lines. A word longer
// than the measure is hard-truncated rather than overflowing, and the last
// allotted line carries the ellipsis when text remains.
//
// tailCost is reserved on the line that would be the last one the cap allows,
// because that is where a tail lands whenever the message fills the block. A
// message that ends earlier has its own final line trimmed by the caller if the
// tail does not fit beside it.
func wrapError(styles *theme.Styles, message string, measure, maxLines, tailCost int) []string {
	words := strings.Fields(message)
	if len(words) == 0 {
		return nil
	}
	lines := make([]string, 0, maxLines)
	for index := 0; index < maxLines && len(words) > 0; index++ {
		if index == maxLines-1 {
			// The cap's last line takes whatever is left, marked when it does
			// not fit: this is where the ellipsis of spec section 3.3 belongs.
			lines = append(lines, truncate(styles, strings.Join(words, " "), max(measure-tailCost, 1)))
			break
		}
		line, rest := greedyLine(styles, words, measure)
		lines = append(lines, line)
		words = rest
	}
	return lines
}

// greedyLine takes as many whole words as room holds, hard-truncating a single
// word that cannot fit on a line of its own.
func greedyLine(styles *theme.Styles, words []string, room int) (string, []string) {
	line := ""
	for len(words) > 0 {
		candidate := words[0]
		if line != "" {
			candidate = line + " " + words[0]
		}
		if ansi.StringWidth(candidate) > room {
			break
		}
		line, words = candidate, words[1:]
	}
	if line == "" {
		return truncate(styles, words[0], room), words[1:]
	}
	return line, words
}
