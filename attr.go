package fasthtmlparser

import "strings"

// Attr returns the decoded value of the first attribute with the given name (case-insensitive);
// "" when the attribute is missing or has no value.
func (n *Node) Attr(name string) string {
	return decodeHTML(n.attrRaw(name), true)
}

// AttrRaw returns the value as written in the source.
func (n *Node) AttrRaw(name string) string { return n.attrRaw(name) }

// HasClass reports whether one of the space-separated classes equals class (case-insensitive).
func (n *Node) HasClass(class string) bool {
	if n == nil || class == "" {
		return false
	}
	value := n.attrRaw("class")
	if needsDecoding(value) {
		value = decodeHTML(value, true)
	}
	if len(value) == len(class) {
		return strings.EqualFold(value, class)
	}
	if len(class) > len(value) {
		return false
	}
	for from := 0; from <= len(value)-len(class); {
		i := indexFold(value[from:], class)
		if i < 0 {
			return false
		}
		i += from
		after := i + len(class)
		if (i == 0 || isSpace(value[i-1])) && (after == len(value) || isSpace(value[after])) {
			return true
		}
		from = i + 1
	}
	return false
}

// Attrs calls f for every attribute of the opening tag with its name and raw value, until f returns false.
func (n *Node) Attrs(f func(name, rawValue string) bool) {
	if n == nil || n.key == int16(TagComment) {
		return
	}
	s := attributeScanner(n)
	for {
		nameStart, nameLen, valueStart, valueLen, ok := s.next()
		if !ok {
			return
		}
		value := ""
		if valueStart >= 0 {
			value = s.src[valueStart : valueStart+valueLen]
		}
		if !f(s.src[nameStart:nameStart+nameLen], value) {
			return
		}
	}
}

// attrRaw returns the raw value of the first attribute with the given name, as a browser reads the tag:
// quoted or unquoted (href=/p?a=1), "" for an attribute without a value or a missing one.
func (n *Node) attrRaw(attribute string) string {
	// a comment has no attributes, whatever its text looks like
	if n == nil || attribute == "" || n.key == int16(TagComment) {
		return ""
	}

	src := n.doc.source
	start := int(n.start)
	end := openTagEnd(n)
	open := src[start:end] // "<name attributes", without the closing '>'

	// The first place where the name stands as a word of its own. Most tags have none.
	pos := nextWord(open, attribute, 1)
	if pos < 0 {
		return ""
	}

	// That word is the attribute unless it is inside the value of another one (title="see id=5", x= id).
	// Nearly always that is ruled out at once, and the value is read in place.
	firstWord := n.key >= 0 && pos == len(tagNames[n.key])+2
	if firstWord || isNameForSure(open, pos) {
		vs, vl := readValue(src, start+pos+len(attribute), end)
		if vs < 0 {
			return ""
		}
		return src[vs : vs+vl]
	}

	// Otherwise the attributes are read one by one, up to the one that starts at that word.
	s := attributeScanner(n)
	for {
		nameStart, nameLen, valueStart, valueLen, ok := s.next()
		if !ok {
			return ""
		}
		name := nameStart - start
		// a word the scanner has passed was inside a value: the next one takes its place
		for pos < name {
			if pos = nextWord(open, attribute, pos+1); pos < 0 {
				return ""
			}
		}
		if pos == name && nameLen == len(attribute) {
			if valueStart < 0 {
				return ""
			}
			return src[valueStart : valueStart+valueLen]
		}
	}
}

// nextWord returns the next place at or after from where the attribute name stands as a word of its own, or -1.
func nextWord(open, attribute string, from int) int {
	for from < len(open) {
		found := indexFold(open[from:], attribute)
		if found < 0 {
			break
		}
		from += found
		after := from + len(attribute)
		if startsName(open[from-1]) && (after == len(open) || endsName(open[after])) {
			return from
		}
		from++
	}
	return -1
}

// isNameForSure reports whether the word at pos of an opening tag is an attribute name, judged by the bytes
// before it alone. False means "not sure".
func isNameForSure(open string, pos int) bool {
	// After whitespace, and not as the unquoted value in "x= word".
	last := pos - 1
	if !isSpace(open[last]) {
		return false
	}
	for last > 0 && isSpace(open[last]) {
		last--
	}
	if open[last] == '=' {
		return false
	}
	// Not inside a quoted value: it is enough that the last quote of each kind before the word does not open a value.
	before := open[:last+1]
	return !opensValue(before, strings.LastIndexByte(before, '"')) && !opensValue(before, strings.LastIndexByte(before, '\''))
}

// opensValue reports whether the quote at quote (or -1: none) stands right after an '='.
func opensValue(before string, quote int) bool {
	if quote < 0 {
		return false
	}
	k := quote - 1 // >= 0: the tag starts with '<'
	for k > 0 && isSpace(before[k]) {
		k--
	}
	return before[k] == '='
}

func startsName(c byte) bool { return isSpace(c) || c == '"' || c == '\'' || c == '/' || c == '=' }
func endsName(c byte) bool   { return isSpace(c) || c == '=' || c == '/' || c == '>' }

// attrEquals compares the decoded value; it allocates only when the raw value has '&' or CR.
func attrEquals(raw, value string) bool {
	if needsDecoding(raw) {
		return decodeHTML(raw, true) == value
	}
	return raw == value
}

// openTagEnd returns the position of the '>' that ends the opening tag.
func openTagEnd(n *Node) int {
	end := n.end
	if n.innerStart > 0 && n.innerEnd > 0 {
		end = n.innerStart - 1
	}
	return min(int(end), len(n.doc.source))
}

// readValue reads the value that follows an attribute name at pos; valueStart is -1 when there is no '='.
func readValue(s string, pos, end int) (valueStart, valueLen int) {
	valueStart, valueLen, _ = readValueAt(s, pos, end)
	return
}

func readValueAt(s string, pos, end int) (valueStart, valueLen, next int) {
	p := pos
	for p < end && isSpace(s[p]) {
		p++
	}
	if p >= end || s[p] != '=' {
		return -1, 0, pos
	}
	p++
	for p < end && isSpace(s[p]) {
		p++
	}
	if p < end && (s[p] == '"' || s[p] == '\'') {
		valueStart = p + 1
		closeQuote := strings.IndexByte(s[valueStart:end], s[p])
		if closeQuote < 0 {
			return valueStart, end - valueStart, end
		}
		return valueStart, closeQuote, valueStart + closeQuote + 1
	}
	valueStart = p
	for p < end && !isSpace(s[p]) {
		p++
	}
	return valueStart, p - valueStart, p
}

type attrScanner struct {
	src string
	end int
	pos int
}

func attributeScanner(n *Node) attrScanner {
	src := n.doc.source
	end := openTagEnd(n)
	i := int(n.start) + 1 // skip '<' and the name
	for i < end {
		c := src[i]
		if isSpace(c) || c == '/' || c == '>' {
			break
		}
		i++
	}
	return attrScanner{src: src, end: end, pos: i}
}

func (s *attrScanner) next() (nameStart, nameLen, valueStart, valueLen int, ok bool) {
	for {
		for s.pos < s.end && (isSpace(s.src[s.pos]) || s.src[s.pos] == '/') {
			s.pos++
		}
		if s.pos >= s.end {
			return 0, 0, 0, 0, false
		}
		nameStart = s.pos
		for s.pos < s.end {
			c := s.src[s.pos]
			if isSpace(c) || c == '=' || c == '/' || c == '>' {
				break
			}
			s.pos++
		}
		nameLen = s.pos - nameStart
		if nameLen == 0 {
			s.pos++ // a stray '=': skip it
			continue
		}
		valueStart, valueLen, s.pos = readValueAt(s.src, s.pos, s.end)
		return nameStart, nameLen, valueStart, valueLen, true
	}
}

// indexFold is strings.Index with ASCII case folding; sub must be ASCII.
func indexFold(s, sub string) int {
	if len(sub) == 0 {
		return 0
	}
	c := sub[0]
	lower, upper := c, c
	if c|0x20 >= 'a' && c|0x20 <= 'z' {
		lower, upper = c|0x20, c&^0x20
	}
	for i := 0; i+len(sub) <= len(s); {
		j := indexEither(s[i:len(s)-len(sub)+1], lower, upper)
		if j < 0 {
			return -1
		}
		i += j
		if strings.EqualFold(s[i:i+len(sub)], sub) {
			return i
		}
		i++
	}
	return -1
}

func indexEither(s string, a, b byte) int {
	if a == b {
		return strings.IndexByte(s, a)
	}
	i := strings.IndexByte(s, a)
	if i < 0 {
		return strings.IndexByte(s, b)
	}
	if j := strings.IndexByte(s[:i], b); j >= 0 {
		return j
	}
	return i
}
