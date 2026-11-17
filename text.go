package fasthtmlparser

import (
	"strings"
	"unicode"
	"unicode/utf8"
	"unsafe"
)

// spaceAt reports whether s[i] starts a whitespace character (ASCII or Unicode, as .NET char.IsWhiteSpace) and its size.
func spaceAt(s string, i int) (bool, int) {
	c := s[i]
	if c < utf8.RuneSelf {
		return isSpace(c), 1
	}
	r, size := utf8.DecodeRuneInString(s[i:])
	return unicode.IsSpace(r), size
}

// skipMarkup: s[i] is '<'. Returns the index of the last byte of what is not text there: a comment, a tag,
// or a tag together with invisible content (script, style, ...) up to its end tag. -1: an unterminated tag.
func skipMarkup(s string, i int) int {
	rest := s[i+1:]
	if strings.HasPrefix(rest, "!--") {
		return findCommentEnd(s, i+4)
	}
	closeTag := strings.IndexByte(rest, '>')
	if closeTag < 0 {
		return -1
	}
	tagEnd := i + 1 + closeTag
	if endTag := invisibleContentEnd(rest[:closeTag]); endTag != "" {
		return findRawTextEnd(s, tagEnd+1, endTag) - 1
	}
	return tagEnd
}

// invisibleContentEnd: for the inside of an opening tag, the end tag ("</script") when the element's content
// is never visible text (script, style, noscript, template, noframes, noembed), otherwise "".
func invisibleContentEnd(tag string) string {
	if tag == "" {
		return ""
	}
	if c := tag[0] | 0x20; c != 's' && c != 't' && c != 'n' {
		return ""
	}
	n := 1
	for n < len(tag) && n <= 8 && (tag[n]|0x20) >= 'a' && (tag[n]|0x20) <= 'z' {
		n++
	}
	if n < len(tag) && tag[n] != '/' && !isSpace(tag[n]) {
		return ""
	}
	var end string
	switch name := tag[:n]; {
	case strings.EqualFold(name, "style"):
		end = "</style"
	case strings.EqualFold(name, "script"):
		end = "</script"
	case strings.EqualFold(name, "noembed"):
		end = "</noembed"
	case strings.EqualFold(name, "noscript"):
		end = "</noscript"
	case strings.EqualFold(name, "template"):
		end = "</template"
	case strings.EqualFold(name, "noframes"):
		end = "</noframes"
	default:
		return ""
	}
	if t := strings.TrimRight(tag, " \t\r\n\f\v"); t[len(t)-1] == '/' {
		return "" // <script src="x" />: no content
	}
	return end
}

// stripTags removes tags, comments and invisible content, collapses whitespace runs into one ' ' and trims.
func stripTags(s string, dst []byte) []byte {
	prevSpace := true // left trim
	for i := 0; i < len(s); {
		c := s[i]
		if c == '<' && isMarkupStart(s, i) {
			last := skipMarkup(s, i)
			if last < 0 {
				break
			}
			i = last + 1
			continue
		}
		if c < utf8.RuneSelf {
			if isSpace(c) {
				if !prevSpace {
					dst = append(dst, ' ')
					prevSpace = true
				}
			} else {
				dst = append(dst, c)
				prevSpace = false
			}
			i++
			continue
		}
		space, size := spaceAt(s, i)
		if space {
			if !prevSpace {
				dst = append(dst, ' ')
				prevSpace = true
			}
		} else {
			dst = append(dst, s[i:i+size]...)
			prevSpace = false
		}
		i += size
	}
	// Right trim: whitespace is collapsed, so there is at most one trailing space.
	if n := len(dst); n > 0 && dst[n-1] == ' ' {
		dst = dst[:n-1]
	}
	return dst
}

func clearTags(s string) string {
	if s == "" {
		return ""
	}
	buf := stripTags(s, make([]byte, 0, len(s)))
	if len(buf) == 0 {
		return ""
	}
	text := unsafe.String(&buf[0], len(buf)) // buf is not used again
	if strings.IndexByte(text, '&') >= 0 {
		return decodeHTML(text, false)
	}
	return text
}

// isText reports whether the visible text equals expected, comparing on the fly without allocations.
func (n *Node) isText(expected string) bool {
	value := n.Body()
	if value == "" || expected == "" {
		return false
	}
	matched := 0
	prevSpace := true     // left trim
	pendingSpace := false // a collapsed space counts only if more text follows (right trim)

	for i := 0; i < len(value); {
		c := value[i]
		if c == '<' && isMarkupStart(value, i) {
			last := skipMarkup(value, i)
			if last < 0 {
				break
			}
			i = last + 1
			continue
		}
		space, size := spaceAt(value, i)
		if space {
			if !prevSpace {
				pendingSpace = true
				prevSpace = true
			}
			i += size
			continue
		}
		if c == '&' {
			// Text with entities must be decoded before comparing: take the full path.
			return n.Text() == expected
		}
		if pendingSpace {
			if matched >= len(expected) || expected[matched] != ' ' {
				return false
			}
			matched++
			pendingSpace = false
		}
		if matched+size > len(expected) || expected[matched:matched+size] != value[i:i+size] {
			return false
		}
		matched += size
		prevSpace = false
		i += size
	}
	return matched == len(expected)
}
