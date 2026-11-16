package fasthtml

import (
	"strings"
	"unicode/utf8"
)

// HTML5 character reference decoder (WHATWG "character reference state"): all 2125 named references,
// the legacy names without ';', numeric references with or without ';', and the spec replacements
// (0, surrogates, > U+10FFFF → U+FFFD; 0x80–0x9F → Windows-1252).

// DecodeHTML decodes character references in text.
func DecodeHTML(s string) string { return decodeHTML(s, false) }

// decodeHTML: with inAttribute, a legacy name without ';' followed by '=' or an alphanumeric is left as is
// (so "?a=1&copy=2" stays intact). Without '&' the input is returned as is.
func decodeHTML(s string, inAttribute bool) string {
	amp := strings.IndexByte(s, '&')
	if amp < 0 {
		return s
	}

	out := make([]byte, 0, len(s))
	pos := 0
	for amp >= 0 {
		out = append(out, s[pos:amp]...)
		rest := s[amp+1:]
		consumed := 0

		if len(rest) > 0 && rest[0] == '#' {
			var r rune
			if consumed, r = matchNumeric(rest); consumed > 0 {
				out = utf8.AppendRune(out, r)
			}
		} else {
			var value string
			if consumed, value = matchNamed(rest, inAttribute); consumed > 0 {
				out = append(out, value...)
			}
		}
		if consumed == 0 {
			out = append(out, '&') // not a reference: keep '&'
		}

		pos = amp + 1 + consumed
		next := strings.IndexByte(s[pos:], '&')
		if next < 0 {
			break
		}
		amp = pos + next
	}
	out = append(out, s[pos:]...)
	return string(out)
}

// matchNumeric: s starts with '#'. Returns the number of bytes consumed (0: not a reference).
func matchNumeric(s string) (int, rune) {
	i := 1
	hex := i < len(s) && s[i]|0x20 == 'x'
	if hex {
		i++
	}
	digits := i
	var value uint32
	for ; i < len(s); i++ {
		c := s[i]
		var d uint32
		switch {
		case c >= '0' && c <= '9':
			d = uint32(c - '0')
		case hex && c|0x20 >= 'a' && c|0x20 <= 'f':
			d = uint32(c|0x20-'a') + 10
		default:
			goto done
		}
		if hex {
			value = value*16 + d
		} else {
			value = value*10 + d
		}
		if value > 0x10FFFF {
			value = 0x110000 // saturate: no overflow on long digit runs
		}
	}
done:
	if i == digits {
		return 0, 0 // "&#" / "&#x" without digits is plain text
	}
	if i < len(s) && s[i] == ';' {
		i++
	}
	switch {
	case value == 0 || value > 0x10FFFF || value-0xD800 <= 0x7FF:
		value = 0xFFFD
	case value-0x80 <= 0x1F:
		if mapped := windows1252[value-0x80]; mapped != 0 {
			value = uint32(mapped)
		}
	}
	return i, rune(value)
}

// matchNamed: s is the text right after '&'. Returns the number of bytes consumed (0: not a reference).
func matchNamed(s string, inAttribute bool) (int, string) {
	limit := min(len(s), entityMaxName+1)
	n := 0
	for n < limit && isAlnum(s[n]) {
		n++
	}
	if n < 2 {
		return 0, ""
	}
	// Full match: "&name;".
	if n < len(s) && s[n] == ';' {
		if e, ok := entityTable[s[:n]]; ok {
			return n + 1, e.value
		}
	}
	// Longest legacy prefix without ';': "&copy2024" → "©2024", "&notit;" → "¬it;".
	for k := min(n, entityMaxLegacyName); k >= 2; k-- {
		if e, ok := entityTable[s[:k]]; ok && e.legacy {
			if inAttribute && k < len(s) && (s[k] == '=' || isAlnum(s[k])) {
				break
			}
			return k, e.value
		}
	}
	return 0, ""
}

func isAlnum(c byte) bool {
	return c >= '0' && c <= '9' || c|0x20 >= 'a' && c|0x20 <= 'z'
}

// Replacements for numeric references 0x80..0x9F; 0 = keep the code point as is.
var windows1252 = [32]rune{
	'€', 0, '‚', 'ƒ', '„', '…', '†', '‡', 'ˆ', '‰', 'Š', '‹', 'Œ', 0, 'Ž', 0,
	0, '‘', '’', '“', '”', '•', '–', '—', '˜', '™', 'š', '›', 'œ', 0, 'ž', 'Ÿ',
}
