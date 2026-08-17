package tui

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// Node 24 uses Unicode 17 while the Go 1.25/1.26 x/text tables use Unicode 15.
// These are the frozen Unicode 16/17 property deltas needed by final sigma.
var webUnicode17CasedAdd = []webRuneRange{
	{0x1c89, 0x1c8a},
	{0xa7cb, 0xa7cf},
	{0xa7d2, 0xa7d2},
	{0xa7d4, 0xa7d4},
	{0xa7da, 0xa7dc},
	{0xa7f1, 0xa7f1},
	{0x10d50, 0x10d65},
	{0x10d70, 0x10d85},
	{0x16ea0, 0x16eb8},
	{0x16ebb, 0x16ed3},
}

var webUnicode17CaseIgnorableAdd = []webRuneRange{
	{0x0897, 0x0897},
	{0x1acf, 0x1add},
	{0x1ae0, 0x1aeb},
	{0xa7f1, 0xa7f1},
	{0x10d4e, 0x10d4e},
	{0x10d69, 0x10d6d},
	{0x10d6f, 0x10d6f},
	{0x10ec5, 0x10ec5},
	{0x10efa, 0x10efc},
	{0x113bb, 0x113c0},
	{0x113ce, 0x113ce},
	{0x113d0, 0x113d0},
	{0x113d2, 0x113d2},
	{0x113e1, 0x113e2},
	{0x11b60, 0x11b60},
	{0x11b62, 0x11b64},
	{0x11b66, 0x11b66},
	{0x11dd9, 0x11dd9},
	{0x11f5a, 0x11f5a},
	{0x1611e, 0x16129},
	{0x1612d, 0x1612f},
	{0x16d40, 0x16d42},
	{0x16d6b, 0x16d6c},
	{0x16ff2, 0x16ff3},
	{0x1e5ee, 0x1e5ef},
	{0x1e6e3, 0x1e6e3},
	{0x1e6e6, 0x1e6e6},
	{0x1e6ee, 0x1e6ef},
	{0x1e6f5, 0x1e6f5},
	{0x1e6ff, 0x1e6ff},
}

type webRuneRange struct {
	first rune
	last  rune
}

type webRuneToken struct {
	raw   string
	rune  rune
	valid bool
}

// webLower matches Node 24 String.prototype.toLowerCase, frozen at Unicode 17.
// Invalid UTF-8 has no JavaScript equivalent; it is preserved byte-for-byte and
// acts as an uncased, non-ignorable boundary, matching x/text's prior behavior.
func webLower(value string) string {
	tokens := webRuneTokens(value)
	lower := cases.Lower(language.Und, cases.HandleFinalSigma(false))
	var result strings.Builder
	result.Grow(len(value))
	plainStart := 0
	offset := 0
	for i, token := range tokens {
		mapped, hasUnicode17Mapping := webUnicode17Lower(token.rune)
		if token.valid && token.rune != '\u03a3' && !hasUnicode17Mapping {
			offset += len(token.raw)
			continue
		}
		result.WriteString(lower.String(value[plainStart:offset]))
		switch {
		case !token.valid:
			result.WriteString(token.raw)
		case token.rune == '\u03a3':
			if webFinalSigma(tokens, i) {
				result.WriteRune('\u03c2')
			} else {
				result.WriteRune('\u03c3')
			}
		case hasUnicode17Mapping:
			result.WriteRune(mapped)
		}
		offset += len(token.raw)
		plainStart = offset
	}
	result.WriteString(lower.String(value[plainStart:]))
	return result.String()
}

func webRuneTokens(value string) []webRuneToken {
	tokens := make([]webRuneToken, 0, utf8.RuneCountInString(value))
	for offset := 0; offset < len(value); {
		r, size := utf8.DecodeRuneInString(value[offset:])
		valid := r != utf8.RuneError || size != 1
		tokens = append(tokens, webRuneToken{raw: value[offset : offset+size], rune: r, valid: valid})
		offset += size
	}
	return tokens
}

func webFinalSigma(tokens []webRuneToken, sigma int) bool {
	precededByCased := false
	for i := sigma - 1; i >= 0; i-- {
		token := tokens[i]
		if token.valid && webUnicode17CaseIgnorable(token.rune) {
			continue
		}
		precededByCased = token.valid && webUnicode17Cased(token.rune)
		break
	}
	if !precededByCased {
		return false
	}
	for i := sigma + 1; i < len(tokens); i++ {
		token := tokens[i]
		if token.valid && webUnicode17CaseIgnorable(token.rune) {
			continue
		}
		return !token.valid || !webUnicode17Cased(token.rune)
	}
	return true
}

func webUnicode17Lower(r rune) (rune, bool) {
	if 0x10d50 <= r && r <= 0x10d65 {
		return r + 0x20, true
	}
	if 0x16ea0 <= r && r <= 0x16eb8 {
		return r + 0x1b, true
	}
	switch r {
	case 0x1c89:
		return 0x1c8a, true
	case 0xa7cb:
		return 0x0264, true
	case 0xa7cc:
		return 0xa7cd, true
	case 0xa7ce:
		return 0xa7cf, true
	case 0xa7d2:
		return 0xa7d3, true
	case 0xa7d4:
		return 0xa7d5, true
	case 0xa7da:
		return 0xa7db, true
	case 0xa7dc:
		return 0x019b, true
	default:
		return 0, false
	}
}

func webUnicode17Cased(r rune) bool {
	if r == 0x0295 {
		return false
	}
	if webInRuneRanges(r, webUnicode17CasedAdd) {
		return true
	}
	return unicode.IsLower(r) || unicode.Is(unicode.Other_Lowercase, r) ||
		unicode.IsUpper(r) || unicode.Is(unicode.Other_Uppercase, r) || unicode.IsTitle(r)
}

func webUnicode17CaseIgnorable(r rune) bool {
	if r == 0x1171e {
		return false
	}
	if webInRuneRanges(r, webUnicode17CaseIgnorableAdd) {
		return true
	}
	if unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf, unicode.Lm, unicode.Sk) {
		return true
	}
	switch r {
	case 0x0027, 0x002e, 0x003a, 0x00b7, 0x0387, 0x055f, 0x05f4,
		0x2018, 0x2019, 0x2024, 0x2027, 0xfe13, 0xfe52, 0xfe55,
		0xff07, 0xff0e, 0xff1a:
		return true
	default:
		return false
	}
}

func webInRuneRanges(r rune, ranges []webRuneRange) bool {
	for _, value := range ranges {
		if r < value.first {
			return false
		}
		if r <= value.last {
			return true
		}
	}
	return false
}
