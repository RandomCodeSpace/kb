package carddetail

import (
	"regexp"
	"slices"
	"strings"
	"unicode"
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
	codePattern   = regexp.MustCompile("\\x60([^\\x60\\r\\n]+)\\x60")
	boldPattern   = regexp.MustCompile("\\*\\*([^*\\r\\n]+)\\*\\*")
	italicPattern = regexp.MustCompile("\\*([^*\\s][^*\\r\\n]*)\\*")
	urlPattern    = regexp.MustCompile("https?://[^\\s<>()\\\"'\\x60]+")
)

// parityMarkdown reduces a description to the frozen web renderer's grammar
// before Glamour sees it. Glamour deliberately understands much more
// Markdown; escaping everything outside this allowlist keeps that extra syntax
// literal instead of quietly widening the product contract.
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
		return raw[:markerEnd] + " " + inlineMarkdown(raw[textStart:], false), true
	}
	return inlineMarkdown(raw, false), false
}

func heading(raw string) (int, string, bool) {
	level := 0
	for level < len(raw) && level < 4 && raw[level] == '#' {
		level++
	}
	if level == 0 || level > 3 || level >= len(raw) || !unicode.IsSpace(firstRune(raw[level:])) {
		return 0, "", false
	}
	textStart := level
	for textStart < len(raw) {
		r, size := utf8.DecodeRuneInString(raw[textStart:])
		if !unicode.IsSpace(r) {
			break
		}
		textStart += size
	}
	return level, raw[textStart:], true
}

func orderedPrefix(raw string) (markerEnd, textStart int) {
	digits := 0
	for digits < len(raw) && digits < 3 && raw[digits] >= '0' && raw[digits] <= '9' {
		digits++
	}
	if digits == 0 || digits+1 >= len(raw) || raw[digits] != '.' || !unicode.IsSpace(firstRune(raw[digits+1:])) {
		return 0, 0
	}
	textStart = digits + 1
	for textStart < len(raw) {
		r, size := utf8.DecodeRuneInString(raw[textStart:])
		if !unicode.IsSpace(r) {
			break
		}
		textStart += size
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
			out.WriteString(escapeLinkLabel(match.text))
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

func escapeLinkLabel(text string) string {
	var out strings.Builder
	for _, r := range text {
		switch r {
		case '\\', '[', ']', '*', '_', '~', '<', '>', 0x60:
			out.WriteByte('\\')
		}
		out.WriteRune(r)
	}
	return out.String()
}

func inlineMatches(line string) []inlineMatch {
	var found []inlineMatch
	found = appendPattern(found, line, codePattern, 0, inlineCode)
	found = appendPattern(found, line, boldPattern, 1, inlineBold)
	found = appendPattern(found, line, italicPattern, 2, inlineItalic)
	found = append(found, underscoreMatches(line)...)
	found = append(found, linkMatches(line)...)
	for _, index := range urlPattern.FindAllStringIndex(line, -1) {
		url := strings.TrimRight(line[index[0]:index[1]], ".,;:!?")
		found = append(found, inlineMatch{
			start: index[0], end: index[0] + len(url), priority: 4,
			kind: inlineAutoLink, text: url, href: url,
		})
	}
	slices.SortFunc(found, func(left, right inlineMatch) int {
		if left.start != right.start {
			return left.start - right.start
		}
		return left.priority - right.priority
	})
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
		if content != "" && !unicode.IsSpace(firstRune(content)) && !unicode.IsSpace(lastRune(content)) &&
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
		for hrefEnd < len(line) && line[hrefEnd] != ')' && !unicode.IsSpace(rune(line[hrefEnd])) {
			hrefEnd++
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

func escapeMarkdown(text string) string {
	var out strings.Builder
	for _, r := range text {
		if r >= '!' && r <= '~' && (unicode.IsPunct(r) || unicode.IsSymbol(r)) {
			out.WriteByte('\\')
		}
		out.WriteRune(r)
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
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func wordAfter(text string, index int) bool {
	if index >= len(text) {
		return false
	}
	r, _ := utf8.DecodeRuneInString(text[index:])
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
