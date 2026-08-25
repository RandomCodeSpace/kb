package carddetail

import (
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

type inlineKind uint8

const (
	inlineCode inlineKind = iota
	inlineBold
	inlineItalic
	inlineLink
	inlineAutoLink
)

type inlineMatch struct {
	start    int
	end      int
	priority int
	kind     inlineKind
	text     string
	href     string
}

var (
	codePattern = regexp.MustCompile("\\x60([^\\x60\\r\\n]+)\\x60")
	boldPattern = regexp.MustCompile("\\*\\*([^*\\r\\n]+)\\*\\*")
)

// parityMarkdown reduces a description to the frozen web renderer's grammar
// before Glamour sees it. Glamour deliberately understands much more
// Markdown; neutralizing everything outside this allowlist keeps that extra
// syntax literal instead of quietly widening the product contract.
//
// The grammar is per line and starts at column zero: a heading, a bullet or an
// ordinal is only ever the first thing on a line, indentation carries no
// meaning of its own, and no construct spans two lines except a fence. Every
// other line is prose. Neutralizing has to be invisible, which rules out the
// backslash for characters Glamour does not strip one from - see
// escapeMarkdown.
func parityMarkdown(source string) string {
	lines := strings.Split(source, "\n")
	out := make([]string, 0, len(lines)*2)
	for i := 0; i < len(lines); i++ {
		raw := lines[i]
		if strings.HasPrefix(raw, strings.Repeat(string(rune(0x60)), 3)) {
			var code []string
			for i++; i < len(lines) && !strings.HasPrefix(lines[i], strings.Repeat(string(rune(0x60)), 3)); i++ {
				code = append(code, lines[i])
			}
			out = append(out, safeFence(code), strings.Join(code, "\n"), safeFence(code), "")
			continue
		}

		line, block := parityLine(raw)
		out = append(out, line)
		if !block {
			out = append(out, "")
		}
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}

func parityLine(raw string) (line string, listItem bool) {
	if _, text, ok := heading(raw); ok {
		return "**" + inlineMarkdown(text, true) + "**", false
	}
	if strings.HasPrefix(raw, "- ") {
		return "- " + inlineMarkdown(raw[2:], false), true
	}
	if markerEnd, textStart := orderedPrefix(raw); markerEnd > 0 {
		// Keep the source ordinal literal. Glamour renumbers adjacent ordered
		// list items, while the frozen web renderer preserves every marker.
		return escapeMarkdown(raw[:markerEnd]) + " " + inlineMarkdown(raw[textStart:], false), false
	}
	// Indentation is not syntax. The frozen renderer matched its bullet,
	// heading and ordinal markers at column zero only - every other line went
	// out as prose in a block that collapsed the leading run away - so the
	// three branches above read the raw line and this one drops the indent.
	// Dropping it also closes the last context where Glamour parsed a source
	// line as an indented code block and printed the escapes below verbatim.
	return escapeLeadingColon(inlineMarkdown(strings.TrimLeft(raw, " \t"), false)), false
}

// escapeLeadingColon guards the one construct a backslash cannot. Glamour
// enables definition lists, which turn a leading ": " into a description of the
// line above even across the blank line parityMarkdown inserts, and "\:" is not
// an escape pair Glamour strips. A character reference is, so the colon travels
// as one and arrives as itself.
func escapeLeadingColon(line string) string {
	if after, found := strings.CutPrefix(line, ":"); found {
		return "&#58;" + after
	}
	return line
}

func heading(raw string) (int, string, bool) {
	level := 0
	for level < len(raw) && level < 4 && raw[level] == '#' {
		level++
	}
	if level == 0 || level > 3 || level >= len(raw) || !jsWhitespace(firstRune(raw[level:])) {
		return 0, "", false
	}
	textStart := level
	for textStart < len(raw) {
		r, size := utf8.DecodeRuneInString(raw[textStart:])
		if !jsWhitespace(r) {
			break
		}
		textStart += size
	}
	if strings.ContainsAny(raw[textStart:], "\r\n\u2028\u2029") {
		return 0, "", false
	}
	return level, raw[textStart:], true
}

func orderedPrefix(raw string) (markerEnd, textStart int) {
	digits := 0
	for digits < len(raw) && digits < 3 && raw[digits] >= '0' && raw[digits] <= '9' {
		digits++
	}
	if digits == 0 || digits+1 >= len(raw) || raw[digits] != '.' || !jsWhitespace(firstRune(raw[digits+1:])) {
		return 0, 0
	}
	textStart = digits + 1
	for textStart < len(raw) {
		r, size := utf8.DecodeRuneInString(raw[textStart:])
		if !jsWhitespace(r) {
			break
		}
		textStart += size
	}
	if strings.ContainsAny(raw[textStart:], "\r\n\u2028\u2029") {
		return 0, 0
	}
	return digits + 1, textStart
}

func safeFence(lines []string) string {
	longest := 2
	for _, line := range lines {
		for _, run := range strings.FieldsFunc(line, func(r rune) bool { return r != '~' }) {
			longest = max(longest, utf8.RuneCountInString(run))
		}
	}
	return strings.Repeat("~", longest+1)
}

func inlineMarkdown(line string, insideHeading bool) string {
	matches := inlineMatches(line)
	var out strings.Builder
	last := 0
	for _, match := range matches {
		if match.start < last {
			continue
		}
		out.WriteString(escapeMarkdown(line[last:match.start]))
		switch match.kind {
		case inlineCode:
			out.WriteRune(0x60)
			out.WriteString(match.text)
			out.WriteRune(0x60)
		case inlineBold:
			if insideHeading {
				out.WriteString(escapeMarkdown(match.text))
			} else {
				out.WriteString("**")
				out.WriteString(escapeMarkdown(match.text))
				out.WriteString("**")
			}
		case inlineItalic:
			out.WriteByte('*')
			out.WriteString(escapeMarkdown(match.text))
			out.WriteByte('*')
		case inlineLink:
			out.WriteByte('[')
			out.WriteString(escapeMarkdown(match.text))
			out.WriteString("](")
			out.WriteString(match.href)
			out.WriteByte(')')
		case inlineAutoLink:
			out.WriteByte('<')
			out.WriteString(match.href)
			out.WriteByte('>')
		}
		last = match.end
	}
	out.WriteString(escapeMarkdown(line[last:]))
	return out.String()
}

func inlineMatches(line string) []inlineMatch {
	var found []inlineMatch
	found = appendPattern(found, line, codePattern, 0, inlineCode)
	found = appendPattern(found, line, boldPattern, 1, inlineBold)
	found = append(found, starItalicMatches(line)...)
	found = append(found, underscoreMatches(line)...)
	found = append(found, linkMatches(line)...)
	found = append(found, bareURLMatches(line)...)
	slices.SortFunc(found, func(left, right inlineMatch) int {
		if left.start != right.start {
			return left.start - right.start
		}
		return left.priority - right.priority
	})
	return found
}

func starItalicMatches(line string) []inlineMatch {
	var found []inlineMatch
	for cursor := 0; cursor < len(line); {
		relative := strings.IndexByte(line[cursor:], '*')
		if relative < 0 {
			break
		}
		open := cursor + relative
		closeRelative := strings.IndexByte(line[open+1:], '*')
		if closeRelative < 0 {
			break
		}
		close := open + 1 + closeRelative
		content := line[open+1 : close]
		if content != "" && !jsWhitespace(firstRune(content)) {
			found = append(found, inlineMatch{
				start: open, end: close + 1, priority: 2, kind: inlineItalic, text: content,
			})
			cursor = close + 1
			continue
		}
		cursor = open + 1
	}
	return found
}

func appendPattern(found []inlineMatch, line string, pattern *regexp.Regexp, priority int, kind inlineKind) []inlineMatch {
	for _, index := range pattern.FindAllStringSubmatchIndex(line, -1) {
		found = append(found, inlineMatch{
			start: index[0], end: index[1], priority: priority, kind: kind,
			text: line[index[2]:index[3]],
		})
	}
	return found
}

func underscoreMatches(line string) []inlineMatch {
	var found []inlineMatch
	for start := 0; start < len(line); {
		relative := strings.IndexByte(line[start:], '_')
		if relative < 0 {
			break
		}
		open := start + relative
		closeRelative := strings.IndexByte(line[open+1:], '_')
		if closeRelative < 0 {
			break
		}
		close := open + 1 + closeRelative
		content := line[open+1 : close]
		if content != "" && !jsWhitespace(firstRune(content)) && !jsWhitespace(lastRune(content)) &&
			!wordBefore(line, open) && !wordAfter(line, close+1) {
			found = append(found, inlineMatch{
				start: open, end: close + 1, priority: 2, kind: inlineItalic, text: content,
			})
			start = close + 1
			continue
		}
		start = open + 1
	}
	return found
}

func linkMatches(line string) []inlineMatch {
	var found []inlineMatch
	for cursor := 0; cursor < len(line); {
		startRelative := strings.IndexByte(line[cursor:], '[')
		if startRelative < 0 {
			break
		}
		start := cursor + startRelative
		endRelative := strings.IndexByte(line[start+1:], ']')
		if endRelative < 0 {
			break
		}
		labelEnd := start + 1 + endRelative
		if labelEnd == start+1 || labelEnd+2 >= len(line) || line[labelEnd+1] != '(' {
			cursor = labelEnd + 1
			continue
		}
		hrefStart := labelEnd + 2
		if !strings.HasPrefix(line[hrefStart:], "http://") && !strings.HasPrefix(line[hrefStart:], "https://") {
			cursor = labelEnd + 1
			continue
		}
		hrefEnd := hrefStart
		for hrefEnd < len(line) && line[hrefEnd] != ')' {
			r, size := utf8.DecodeRuneInString(line[hrefEnd:])
			if jsWhitespace(r) {
				break
			}
			hrefEnd += size
		}
		if hrefEnd >= len(line) || line[hrefEnd] != ')' {
			cursor = hrefEnd + 1
			continue
		}
		found = append(found, inlineMatch{
			start: start, end: hrefEnd + 1, priority: 3, kind: inlineLink,
			text: line[start+1 : labelEnd], href: line[hrefStart:hrefEnd],
		})
		cursor = hrefEnd + 1
	}
	return found
}

func bareURLMatches(line string) []inlineMatch {
	var found []inlineMatch
	for cursor := 0; cursor < len(line); {
		start := nextURLStart(line, cursor)
		if start < 0 {
			break
		}
		end := start
		for end < len(line) {
			r, size := utf8.DecodeRuneInString(line[end:])
			if jsWhitespace(r) || strings.ContainsRune("<>()\"'`", r) {
				break
			}
			end += size
		}
		url := strings.TrimRight(line[start:end], ".,;:!?")
		found = append(found, inlineMatch{
			start: start, end: start + len(url), priority: 4,
			kind: inlineAutoLink, text: url, href: url,
		})
		cursor = max(end, start+1)
	}
	return found
}

func nextURLStart(line string, cursor int) int {
	http := strings.Index(line[cursor:], "http://")
	https := strings.Index(line[cursor:], "https://")
	switch {
	case http < 0 && https < 0:
		return -1
	case http < 0:
		return cursor + https
	case https < 0:
		return cursor + http
	default:
		return cursor + min(http, https)
	}
}

// escapableRunes are the characters a backslash can neutralize. Glamour strips
// a fixed set of eighteen escape pairs and passes every other backslash through
// to the terminal as text, so escaping the whole of ASCII punctuation printed
// "key \= value" for any line Glamour did not run inline parsing over. Listed
// here are the members of that set which are also syntactic under the GFM
// superset Glamour parses: the inline delimiters, the leaf-block markers a line
// can start with, and the backslash itself.
const escapableRunes = "\\`*_[]!<>#-+.)|"

// escapeMarkdown reduces a run of source text to inert prose. Anything outside
// the frozen grammar has to arrive as itself, and the two characters that carry
// meaning but sit outside Glamour's escape set travel as character references,
// which Glamour resolves: '~' opens a tilde fence and delimits strikethrough,
// and '&' has to follow it so a reference the author typed stays their text
// rather than becoming the character it names.
func escapeMarkdown(text string) string {
	var out strings.Builder
	for _, r := range text {
		switch {
		case r == '&':
			out.WriteString("&amp;")
		case r == '~':
			out.WriteString("&#126;")
		case strings.ContainsRune(escapableRunes, r):
			out.WriteByte('\\')
			out.WriteRune(r)
		default:
			out.WriteRune(r)
		}
	}
	return out.String()
}

func firstRune(text string) rune {
	r, _ := utf8.DecodeRuneInString(text)
	return r
}

func lastRune(text string) rune {
	r, _ := utf8.DecodeLastRuneInString(text)
	return r
}

func wordBefore(text string, index int) bool {
	if index == 0 {
		return false
	}
	r, _ := utf8.DecodeLastRuneInString(text[:index])
	return asciiWord(r)
}

func wordAfter(text string, index int) bool {
	if index >= len(text) {
		return false
	}
	r, _ := utf8.DecodeRuneInString(text[index:])
	return asciiWord(r)
}

func asciiWord(r rune) bool {
	return r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
}

func jsWhitespace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', '\u00a0', '\u1680', '\u2000', '\u2001', '\u2002', '\u2003',
		'\u2004', '\u2005', '\u2006', '\u2007', '\u2008', '\u2009', '\u200a', '\u2028', '\u2029', '\u202f',
		'\u205f', '\u3000', '\ufeff':
		return true
	default:
		return false
	}
}
