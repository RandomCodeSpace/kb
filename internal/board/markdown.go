package board

import (
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// This parser preserves the frozen Markdown wire format. Token syntax, header
// fallback by position, trimming, and checkbox prefixes are compatibility
// contracts.

var (
	dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	// prioRe still admits !4 even though issue #234 collapsed the scale to
	// three values. The wire format is frozen and legacy boards on disk carry
	// the token; narrowing the pattern would silently demote a !4 to a title
	// word rather than reading it as the low priority it always meant.
	// parseTitleLine normalizes the value; titleLine never writes a 4.
	prioRe   = regexp.MustCompile(`^![1-4]$`)
	effortRe = regexp.MustCompile(`^~[SML]$`)
	// descCheckboxRe matches description lines that would re-parse as
	// checklist items; Serialize prefixes them with one backslash (which
	// Parse strips). The leading \* keeps already-escaped lines stable
	// across repeated round trips.
	descCheckboxRe = regexp.MustCompile(`^\\*- \[[ xX]\] `)
)

// blockedToken is the title-line flag for a blocked task. It is written only
// when Task.Blocked is true.
const blockedToken = "%blocked"

// Serialize renders a board in canonical markdown form: "# Title", one
// "## To Do"/"## Doing"/"## Done"/"## Cancelled" section per status, and per
// task a "- [ ] emoji title !p @due ~E %blocked #tag" line followed by
// two-space-indented description lines and checklist items. Tasks keep their
// slice order within each section. The Cancelled section is a phase-3
// addition and is written only when it has tasks, so legacy three-section
// boards stay byte-identical on the wire.
func Serialize(b Board) string {
	var out strings.Builder
	out.WriteString("# " + b.Title + "\n")
	for _, status := range Statuses {
		if status == StatusCancelled && !hasStatus(b, StatusCancelled) {
			continue
		}
		out.WriteString("\n## " + statusLabel[status] + "\n\n")
		for _, t := range b.Tasks {
			if t.Status != status {
				continue
			}
			box := " "
			if status == StatusDone {
				box = "x"
			}
			out.WriteString("- [" + box + "] " + titleLine(t) + "\n")
			for _, line := range strings.Split(t.Desc, "\n") {
				if trimmed := jsTrim(line); trimmed != "" {
					if descCheckboxRe.MatchString(trimmed) {
						trimmed = `\` + trimmed
					}
					out.WriteString("  " + trimmed + "\n")
				}
			}
			for _, c := range t.Checks {
				cb := " "
				if c.Done {
					cb = "x"
				}
				out.WriteString("  - [" + cb + "] " + c.Text + "\n")
			}
		}
	}
	return out.String()
}

// hasStatus reports whether any task on b sits in the given column.
func hasStatus(b Board, status Status) bool {
	for _, t := range b.Tasks {
		if t.Status == status {
			return true
		}
	}
	return false
}

// titleLine renders the single-line form of a task: emoji, title, then the
// !prio (omitted when 3), @due, ~effort, %blocked (omitted when false), and
// #tag tokens in that order. Title words that Parse would lift into metadata
// are backslash-escaped.
func titleLine(t Task) string {
	s := escapeTitle(t.Title)
	if t.Emoji != "" {
		s = t.Emoji + " " + s
	}
	// Normalize before writing so Serialize never emits a token Parse only
	// tolerates for legacy input: a hand-built Task carrying the retired 4
	// serializes as the low-priority card it is, which is the omitted default.
	if prio := NormalizePrio(t.Prio); prio != PrioDefault {
		s += " !" + strconv.Itoa(prio)
	}
	if t.Due != "" {
		s += " @" + t.Due
	}
	if t.Effort != "" {
		s += " ~" + t.Effort
	}
	if t.Blocked {
		s += " " + blockedToken
	}
	for _, tag := range t.Tags {
		s += " #" + tag
	}
	return s
}

// escapeTitle renders a title for the wire with every metadata-shaped word
// (!1..!4, ~S/~M/~L, @YYYY-MM-DD, #tag, %blocked) — and every word already
// starting with the escape character — prefixed by one backslash so Parse
// keeps it as title text. Whitespace runs collapse to single spaces, matching
// what Parse does on read.
func escapeTitle(title string) string {
	fields := strings.FieldsFunc(title, isJSSpace)
	for i, tok := range fields {
		if escapeNeeded(tok) {
			fields[i] = `\` + tok
		}
	}
	return strings.Join(fields, " ")
}

// escapeNeeded reports whether a whitespace-delimited title word would be
// consumed as metadata (or as an escape) by parseTitleLine.
func escapeNeeded(tok string) bool {
	switch {
	case strings.HasPrefix(tok, `\`):
		return true
	case tok == blockedToken:
		return true
	case prioRe.MatchString(tok), effortRe.MatchString(tok):
		return true
	case strings.HasPrefix(tok, "@") && dateRe.MatchString(tok[1:]):
		return true
	}
	return len(tok) > 1 && tok[0] == '#'
}

// Parse decodes markdown into a Board. It is content-preserving and
// infallible: unknown constructs degrade into title/description text rather
// than errors. IDs are left empty (the wire format carries none); CreatedAt
// and MovedAt are set to the parse time; Position is the task's 0-based
// ordinal within its column.
func Parse(input string) Board {
	b := Board{Title: "Board"}
	sawTitle := false
	var status Status
	haveStatus := false
	cur := -1
	headerIdx := -1
	var descs [][]string
	now := time.Now()

	for _, raw := range strings.Split(input, "\n") {
		line := strings.TrimSuffix(raw, "\r")
		if !sawTitle && strings.HasPrefix(line, "# ") {
			b.Title = jsTrim(line[2:])
			sawTitle = true
			continue
		}
		if strings.HasPrefix(line, "## ") {
			headerIdx++
			label := strings.ToLower(jsTrim(line[3:]))
			st, ok := statusForLabel(label)
			if !ok {
				// Unknown headers fall back to their position, saturating at
				// the last column, so legacy three-section files keep their
				// old mapping and a fourth unknown section lands in cancelled.
				st = Statuses[min(len(Statuses)-1, headerIdx)]
			}
			status = st
			haveStatus = true
			cur = -1
			continue
		}
		if !haveStatus || jsTrim(line) == "" {
			continue
		}

		first, _ := utf8.DecodeRuneInString(line)
		indented := isJSSpace(first)
		done, rest, isCheck := stripCheckbox(jsTrim(line))

		if !indented && isCheck {
			b.Tasks = append(b.Tasks, parseTitleLine(rest, status, done, now))
			descs = append(descs, nil)
			cur = len(b.Tasks) - 1
			continue
		}
		if cur < 0 {
			continue
		}
		if isCheck && indented {
			b.Tasks[cur].Checks = append(b.Tasks[cur].Checks, Check{Text: rest, Done: done})
		} else {
			text := jsTrim(line)
			if strings.HasPrefix(text, `\`) && descCheckboxRe.MatchString(text[1:]) {
				text = text[1:]
			}
			descs[cur] = append(descs[cur], text)
		}
	}
	pos := map[Status]int{}
	for i := range b.Tasks {
		b.Tasks[i].Desc = strings.Join(descs[i], "\n")
		b.Tasks[i].Position = pos[b.Tasks[i].Status]
		pos[b.Tasks[i].Status]++
	}
	return b
}

// statusForLabel matches a lowercased H2 label against the canonical
// section labels.
func statusForLabel(label string) (Status, bool) {
	for _, s := range Statuses {
		if strings.ToLower(statusLabel[s]) == label {
			return s, true
		}
	}
	return "", false
}

// stripCheckbox splits a "- [ ] rest" / "- [x] rest" / "- [X] rest" prefix
// off an already-trimmed line. The rest is not re-trimmed.
func stripCheckbox(s string) (done bool, rest string, ok bool) {
	if strings.HasPrefix(s, "- [ ] ") {
		return false, s[6:], true
	}
	if strings.HasPrefix(s, "- [x] ") || strings.HasPrefix(s, "- [X] ") {
		return true, s[6:], true
	}
	return false, "", false
}

// parseTitleLine decodes a task title line: optional leading
// extended-pictographic emoji (with optional VS16), then whitespace-split
// tokens where !1..!3 sets priority (a legacy !4 is read as !3), @YYYY-MM-DD sets due, ~S/~M/~L sets
// effort, %blocked sets blocked, #x adds a tag, and everything else stays in
// the title.
// A "- [x]" checkbox forces status done regardless of section.
func parseTitleLine(raw string, status Status, done bool, now time.Time) Task {
	rest := jsTrim(raw)
	emoji := ""
	if e := matchEmoji(rest); e != "" {
		emoji = e
		rest = jsTrim(rest[len(e):])
	}
	if done {
		status = StatusDone
	}
	t := Task{Emoji: emoji, Status: status, Prio: 3, CreatedAt: now, MovedAt: now}
	var words []string
	for _, tok := range strings.FieldsFunc(rest, isJSSpace) {
		switch {
		case strings.HasPrefix(tok, `\`):
			// Escaped word: strip one backslash, keep it as title text.
			words = append(words, tok[1:])
		case prioRe.MatchString(tok):
			t.Prio = NormalizePrio(int(tok[1] - '0'))
		case strings.HasPrefix(tok, "@") && dateRe.MatchString(tok[1:]):
			t.Due = tok[1:]
		case effortRe.MatchString(tok):
			t.Effort = tok[1:]
		case tok == blockedToken:
			t.Blocked = true
		case len(tok) > 1 && tok[0] == '#':
			t.Tags = append(t.Tags, tok[1:])
		default:
			words = append(words, tok)
		}
	}
	t.Title = strings.Join(words, " ")
	return t
}

// matchEmoji returns the leading emoji of s: one Extended_Pictographic rune
// plus an optional U+FE0F variation selector (the TS regex
// /^\p{Extended_Pictographic}(?:️)?/u), or "" if s does not start
// with one.
func matchEmoji(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	if size == 0 || !unicode.Is(extPict, r) {
		return ""
	}
	if r2, size2 := utf8.DecodeRuneInString(s[size:]); r2 == '\ufe0f' {
		return s[:size+size2]
	}
	return s[:size]
}

// LeadingEmoji returns the leading emoji token recognized by the markdown
// grammar, or an empty string when s does not start with one.
func LeadingEmoji(s string) string {
	return matchEmoji(s)
}

// ContainsSpace reports whether s contains a rune the wire format's token
// splitter treats as whitespace (JavaScript's \s class). Fields serialized
// as single tokens (tags) must not contain any.
func ContainsSpace(s string) bool {
	return strings.IndexFunc(s, isJSSpace) >= 0
}

// IsBlank reports whether s is empty or holds nothing but runes the wire
// format's token splitter treats as whitespace. A blank title serializes to
// a bare "- [ ] " line, which Parse reads back as description text rather
// than as a task.
func IsBlank(s string) bool {
	return jsTrim(s) == ""
}

// IsSingleEmoji reports whether s is exactly the leading-emoji token the
// title-line grammar recognizes: one Extended_Pictographic rune plus an
// optional U+FE0F variation selector. Anything else would not survive a
// Serialize/Parse round trip as the emoji field.
func IsSingleEmoji(s string) bool {
	return s != "" && matchEmoji(s) == s
}

// isJSSpace mirrors JavaScript's \s class (ECMAScript WhiteSpace plus
// LineTerminator): it includes U+FEFF and excludes U+0085, unlike Go's
// unicode.IsSpace.
func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ',
		'\u00a0', '\u1680', '\u2028', '\u2029', '\u202f', '\u205f', '\u3000', '\ufeff':
		return true
	}
	return r >= '\u2000' && r <= '\u200a'
}

// jsTrim mirrors String.prototype.trim (trims the isJSSpace set).
func jsTrim(s string) string {
	return strings.TrimFunc(s, isJSSpace)
}

// extPict is the Unicode Extended_Pictographic property
// (emoji-data.txt), matching what /\p{Extended_Pictographic}/u matches
// in JavaScript. Go's regexp has no such class, so the table is inlined.
var extPict = &unicode.RangeTable{
	R16: []unicode.Range16{
		{Lo: 0x00a9, Hi: 0x00a9, Stride: 1},
		{Lo: 0x00ae, Hi: 0x00ae, Stride: 1},
		{Lo: 0x203c, Hi: 0x203c, Stride: 1},
		{Lo: 0x2049, Hi: 0x2049, Stride: 1},
		{Lo: 0x2122, Hi: 0x2122, Stride: 1},
		{Lo: 0x2139, Hi: 0x2139, Stride: 1},
		{Lo: 0x2194, Hi: 0x2199, Stride: 1},
		{Lo: 0x21a9, Hi: 0x21aa, Stride: 1},
		{Lo: 0x231a, Hi: 0x231b, Stride: 1},
		{Lo: 0x2328, Hi: 0x2328, Stride: 1},
		{Lo: 0x2388, Hi: 0x2388, Stride: 1},
		{Lo: 0x23cf, Hi: 0x23cf, Stride: 1},
		{Lo: 0x23e9, Hi: 0x23f3, Stride: 1},
		{Lo: 0x23f8, Hi: 0x23fa, Stride: 1},
		{Lo: 0x24c2, Hi: 0x24c2, Stride: 1},
		{Lo: 0x25aa, Hi: 0x25ab, Stride: 1},
		{Lo: 0x25b6, Hi: 0x25b6, Stride: 1},
		{Lo: 0x25c0, Hi: 0x25c0, Stride: 1},
		{Lo: 0x25fb, Hi: 0x25fe, Stride: 1},
		{Lo: 0x2600, Hi: 0x2605, Stride: 1},
		{Lo: 0x2607, Hi: 0x2612, Stride: 1},
		{Lo: 0x2614, Hi: 0x2685, Stride: 1},
		{Lo: 0x2690, Hi: 0x2705, Stride: 1},
		{Lo: 0x2708, Hi: 0x2712, Stride: 1},
		{Lo: 0x2714, Hi: 0x2714, Stride: 1},
		{Lo: 0x2716, Hi: 0x2716, Stride: 1},
		{Lo: 0x271d, Hi: 0x271d, Stride: 1},
		{Lo: 0x2721, Hi: 0x2721, Stride: 1},
		{Lo: 0x2728, Hi: 0x2728, Stride: 1},
		{Lo: 0x2733, Hi: 0x2734, Stride: 1},
		{Lo: 0x2744, Hi: 0x2744, Stride: 1},
		{Lo: 0x2747, Hi: 0x2747, Stride: 1},
		{Lo: 0x274c, Hi: 0x274c, Stride: 1},
		{Lo: 0x274e, Hi: 0x274e, Stride: 1},
		{Lo: 0x2753, Hi: 0x2755, Stride: 1},
		{Lo: 0x2757, Hi: 0x2757, Stride: 1},
		{Lo: 0x2763, Hi: 0x2767, Stride: 1},
		{Lo: 0x2795, Hi: 0x2797, Stride: 1},
		{Lo: 0x27a1, Hi: 0x27a1, Stride: 1},
		{Lo: 0x27b0, Hi: 0x27b0, Stride: 1},
		{Lo: 0x27bf, Hi: 0x27bf, Stride: 1},
		{Lo: 0x2934, Hi: 0x2935, Stride: 1},
		{Lo: 0x2b05, Hi: 0x2b07, Stride: 1},
		{Lo: 0x2b1b, Hi: 0x2b1c, Stride: 1},
		{Lo: 0x2b50, Hi: 0x2b50, Stride: 1},
		{Lo: 0x2b55, Hi: 0x2b55, Stride: 1},
		{Lo: 0x3030, Hi: 0x3030, Stride: 1},
		{Lo: 0x303d, Hi: 0x303d, Stride: 1},
		{Lo: 0x3297, Hi: 0x3297, Stride: 1},
		{Lo: 0x3299, Hi: 0x3299, Stride: 1},
	},
	R32: []unicode.Range32{
		{Lo: 0x1f000, Hi: 0x1f0ff, Stride: 1},
		{Lo: 0x1f10d, Hi: 0x1f10f, Stride: 1},
		{Lo: 0x1f12f, Hi: 0x1f12f, Stride: 1},
		{Lo: 0x1f16c, Hi: 0x1f171, Stride: 1},
		{Lo: 0x1f17e, Hi: 0x1f17f, Stride: 1},
		{Lo: 0x1f18e, Hi: 0x1f18e, Stride: 1},
		{Lo: 0x1f191, Hi: 0x1f19a, Stride: 1},
		{Lo: 0x1f1ad, Hi: 0x1f1e5, Stride: 1},
		{Lo: 0x1f201, Hi: 0x1f20f, Stride: 1},
		{Lo: 0x1f21a, Hi: 0x1f21a, Stride: 1},
		{Lo: 0x1f22f, Hi: 0x1f22f, Stride: 1},
		{Lo: 0x1f232, Hi: 0x1f23a, Stride: 1},
		{Lo: 0x1f23c, Hi: 0x1f23f, Stride: 1},
		{Lo: 0x1f249, Hi: 0x1f3fa, Stride: 1},
		{Lo: 0x1f400, Hi: 0x1f53d, Stride: 1},
		{Lo: 0x1f546, Hi: 0x1f64f, Stride: 1},
		{Lo: 0x1f680, Hi: 0x1f6ff, Stride: 1},
		{Lo: 0x1f774, Hi: 0x1f77f, Stride: 1},
		{Lo: 0x1f7d5, Hi: 0x1f7ff, Stride: 1},
		{Lo: 0x1f80c, Hi: 0x1f80f, Stride: 1},
		{Lo: 0x1f848, Hi: 0x1f84f, Stride: 1},
		{Lo: 0x1f85a, Hi: 0x1f85f, Stride: 1},
		{Lo: 0x1f888, Hi: 0x1f88f, Stride: 1},
		{Lo: 0x1f8ae, Hi: 0x1f8ff, Stride: 1},
		{Lo: 0x1f90c, Hi: 0x1f93a, Stride: 1},
		{Lo: 0x1f93c, Hi: 0x1f945, Stride: 1},
		{Lo: 0x1f947, Hi: 0x1faff, Stride: 1},
		{Lo: 0x1fc00, Hi: 0x1fffd, Stride: 1},
	},
	LatinOffset: 2,
}
