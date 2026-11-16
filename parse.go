package fasthtml

import (
	"strings"
	"sync"
)

// A page is scanned once in fast mode: tags are matched to their end tags and nothing else.
// Well-formed markup needs nothing more. Where the HTML5 optional-end-tag rules matter (<li>one<li>two</ul>),
// tags are left open inside an element: when its end tag (or the end of the document) finds them on the stack
// and one of them is a rule target (li, p, td, tr, option, ...), only that part is scanned again in rules mode,
// from the outermost such tag up to that end tag. Everything before it was parsed right and is kept.
//
// Port of ParsingCore.cs from z3l.FastHtmlParser; positions are byte offsets into the UTF-8 source.

const (
	// Nesting deeper than this is flattened, as browsers do (Blink: 512).
	maxDepth = 512
	// The parts scanned again with the optional-end-tag rules may add up to this many times the source length.
	repairBudgetFactor = 8
)

// Per-tag data needed while parsing: matching an end tag scans these without touching the nodes.
type tagState struct {
	key        int32 // Tag, or -1 for a custom name
	depth      int32
	prevOpen   int32 // the open tags as a linked list in document order
	selfClosed bool  // <x/>: stays in the open list (a later </x> ends it), but does not nest anything
}

type customName struct {
	name string // the lower-case name, one string per document
	open int32  // how many tags with this name are in the open list
}

type parseState struct {
	doc        *Document
	src        string
	isXML      bool
	nodes      []Node
	states     []tagState
	stack      []int32 // indexes of the open tags that contain the following ones
	openByKey  []int32 // per Tag, how many are on the stack: kept only in rules mode
	openInList []int32 // per Tag, how many are in the open list: an end tag with none is skipped
	custom     map[string]*customName
	count      int
	stackTop   int
	lastOpen   int32
	openP      int // <p> tags on the stack, kept in both modes
	budget     int // bytes that may still be scanned again by repair
}

// Scratch buffers reused between parses: only the result is allocated per document.
type scratch struct {
	nodes      []Node
	states     []tagState
	stack      []int32
	openByKey  []int32
	openInList []int32
}

var scratchPool = sync.Pool{New: func() any {
	return &scratch{
		stack:      make([]int32, 64),
		openByKey:  make([]int32, tagCount),
		openInList: make([]int32, tagCount),
	}
}}

func parse(doc *Document, src string) []Node {
	// Every tag starts with its own '<', so this is an exact upper bound.
	maxTags := strings.Count(src, "<")
	if maxTags == 0 {
		return nil
	}

	sc := scratchPool.Get().(*scratch)
	if cap(sc.nodes) < maxTags {
		sc.nodes = make([]Node, maxTags)
		sc.states = make([]tagState, maxTags)
	}
	clear(sc.openInList)

	st := &parseState{
		doc:        doc,
		src:        src,
		isXML:      isXML(src),
		nodes:      sc.nodes[:maxTags],
		states:     sc.states[:maxTags],
		stack:      sc.stack,
		openByKey:  sc.openByKey,
		openInList: sc.openInList,
		lastOpen:   -1,
		budget:     repairBudgetFactor * len(src),
	}

	tagStarted, tagStart := st.scan(false, 0, len(src))

	// The end of the document ends every tag still open (a truncated page keeps its text),
	// right before an unfinished tag if the source stops inside one.
	regionEnd := len(src)
	if tagStarted {
		regionEnd = tagStart - 1
	}
	if !st.isXML {
		if s := st.firstMisnested(0, st.stackTop); s >= 0 {
			st.repair(s, regionEnd)
		}
	}
	st.truncateOpenList(0, regionEnd-1)

	result := make([]Node, st.count)
	copy(result, st.nodes[:st.count])
	for k := range result {
		if result[k].innerEnd == 0 {
			result[k].innerStart = 0 // not closed: no body
		}
	}

	clear(st.nodes[:st.count]) // do not keep the documents alive through the pool
	sc.stack = st.stack
	scratchPool.Put(sc)
	return result
}

// An XML document (RSS, Atom, sitemap, XHTML) is parsed by XML rules: elements are closed only by an end tag or "/>".
func isXML(src string) bool {
	s := strings.TrimLeft(src, asciiSpace+string(rune(0xFEFF))) // whitespace and a BOM
	return len(s) >= 5 && strings.EqualFold(s[:5], "<?xml")
}

// scan parses src[from:to] (from is outside a tag), appending tags at count.
// Tags still open at to stay on the stack.
func (st *parseState) scan(rules bool, from, to int) (tagStarted bool, tagStart int) {
	src := st.src[:to]

	i := from
	for i < to {
		if !tagStarted {
			// Outside a tag only '<' matters: jump to it (vectorized).
			lt := strings.IndexByte(src[i:], '<')
			if lt < 0 {
				break
			}
			i += lt

			if strings.HasPrefix(src[i+1:], "!--") {
				// <!-- ... -->: one "!--" tag; its content is not markup, it does not nest anything.
				commentEnd := findCommentEnd(src, i+4)
				parent := int32(-1)
				depth := int32(0)
				if st.stackTop > 0 {
					parent = st.stack[st.stackTop-1]
					depth = st.states[parent].depth + 1
				}
				st.nodes[st.count] = Node{doc: st.doc, index: int32(st.count), parent: parent, key: int16(TagComment),
					start: int32(i), end: int32(commentEnd), innerStart: int32(commentEnd + 1), depth: depth}
				st.states[st.count] = tagState{key: int32(TagComment), depth: depth, prevOpen: -1}
				st.count++
				i = commentEnd + 1
				continue
			}

			// As in HTML5, '<' starts markup only before a letter, '/', '!' or '?': "a < b" is text.
			if !isMarkupStart(src, i) {
				i++
				continue
			}

			tagStarted = true
			tagStart = i + 1
			i++
			continue
		}

		// Inside a tag only '<', '=' and '>' matter.
		next := indexTagChar(src[i:])
		if next < 0 {
			break
		}
		i += next

		switch src[i] {
		case '<':
			// An unfinished tag: start over at this '<' (it may also start a comment).
			tagStarted = false
			continue

		case '=':
			// A quoted value may contain '<' and '>' (data-x="<p>...</p>"): jump over it.
			q := i + 1
			for q < to && isSpace(src[q]) {
				q++
			}
			if q < to && (src[q] == '"' || src[q] == '\'') {
				if closeQuote := strings.IndexByte(src[q+1:], src[q]); closeQuote >= 0 {
					i = q + 1 + closeQuote
				}
			}

		default: // '>'
			if src[tagStart] == '/' {
				st.endTag(rules, src, tagStart, i)
			} else {
				i = st.startTag(rules, src, tagStart, i)
			}
			tagStarted = false
		}

		i++
	}

	return tagStarted, tagStart
}

// endTag matches the end tag src[tagStart-1 : gt+1] to the innermost open tag with that name.
func (st *parseState) endTag(rules bool, src string, tagStart, gt int) {
	name := strings.TrimSpace(src[tagStart+1 : gt])
	if name == "" {
		return
	}

	// Resolve the name once, then compare ints instead of a lookup per candidate.
	key := int32(-1)
	if k, ok := lookupTag(name); ok {
		key = int32(k)
	}

	// The rules may have ended the matched tag itself (<li><div><li>x</div>: the second <li> ends the first one
	// together with its <div>, as in a browser): the end tag is matched again, to what is open now.
	// Once: the part is not scanned a second time.
	if st.matchEnd(rules, key, name, tagStart, gt, true) {
		st.matchEnd(rules, key, name, tagStart, gt, false)
	}
}

// matchEnd ends the innermost open tag with the end tag's name. It returns true when it scanned a part again
// with the optional-end-tag rules instead, and the end tag is to be matched once more.
func (st *parseState) matchEnd(rules bool, key int32, name string, tagStart, gt int, canRepair bool) bool {
	// A name that no open tag has is a stray end tag: it is skipped without scanning the open tags.
	var closing *customName
	if key >= 0 {
		if st.openInList[key] <= 0 {
			return false
		}
	} else {
		if st.custom == nil {
			return false
		}
		if closing = st.custom[lowerKey(name)]; closing == nil || closing.open <= 0 {
			return false
		}
	}

	for j := st.lastOpen; j >= 0; j = st.states[j].prevOpen {
		if key >= 0 {
			if st.states[j].key != key {
				continue
			}
		} else if st.states[j].key >= 0 || st.nodes[j].custom != closing.name {
			continue
		}

		innerEnd := tagStart - 2

		// Tags left open inside it: if the optional-end-tag rules apply to one of them,
		// scan its part again with the rules (fast mode only; rules mode has applied them).
		if canRepair && !rules && !st.isXML && st.stackTop > 0 && st.stack[st.stackTop-1] > j {
			above := st.stackTop - 1
			for above > 0 && st.stack[above-1] > j {
				above--
			}
			if target := st.firstMisnested(above, st.stackTop); target >= 0 {
				st.repair(target, tagStart-1)
				return true
			}
		}

		st.nodes[j].innerEnd = int32(innerEnd)
		st.nodes[j].end = int32(gt)

		for st.stackTop > 0 && st.stack[st.stackTop-1] >= j {
			st.stackTop--
			popped := st.states[st.stack[st.stackTop]].key
			if popped == int32(TagP) {
				st.openP--
			}
			if rules && popped >= 0 {
				st.openByKey[popped]--
			}
		}

		// It leaves the open list with everything opened after it; the tags opened inside it and
		// never closed end right before this end tag, as in a browser.
		st.truncateOpenList(j, innerEnd)
		return false
	}
	return false
}

// startTag adds the tag src[tagStart-1 : gt+1]; returns the position to continue from (gt, or before a raw-text end tag).
func (st *parseState) startTag(rules bool, src string, tagStart, gt int) int {
	tagSource := src[tagStart:gt]
	name := extractTagName(tagSource)

	key := int32(-1)
	var custom string
	if k, ok := lookupTag(name); ok {
		key = int32(k)
	} else {
		// Custom names (svg elements, web components, ...): one string per distinct name per document.
		custom = st.internName(name)
	}

	if !st.isXML && key >= 0 {
		// Optional end tags (HTML5): <li> ends an open <li>, <td> an open <td>, ...
		if rule := impliedEndRules[key]; rule != nil {
			if rules {
				if rule.anyOpen(st.openByKey) {
					st.closeInScope(rule, tagStart-2, true)
				}
			} else if st.stackTop > 0 {
				// The common case of a left-out end tag is caught right here: the innermost open tag is a target.
				if top := st.states[st.stack[st.stackTop-1]].key; top >= 0 && rule.targets[top] {
					st.applyRuleInFastMode(rule, tagStart)
				}
			}
		}

		// A block element ends an open <p>. Usually no <p> is open and this is one compare.
		if st.openP > 0 && closesP[key] {
			if rules {
				st.closeInScope(pRule, tagStart-2, true)
			} else {
				st.applyRuleInFastMode(pRule, tagStart)
			}
		}
	}

	// the innermost open tag is the parent
	parent := int32(-1)
	depth := int32(0)
	if st.stackTop > 0 {
		parent = st.stack[st.stackTop-1]
		depth = st.states[parent].depth + 1
	}

	index := st.count
	st.nodes[index] = Node{doc: st.doc, index: int32(index), parent: parent, key: int16(key), custom: custom,
		start: int32(tagStart - 1), end: int32(gt), innerStart: int32(gt + 1), depth: depth}

	// Void elements (<br>, <img>, <meta>, ...) and declarations never contain anything.
	isVoid := !st.isXML && key >= 0 && voidTags[key]
	selfClosing := isVoid
	if !selfClosing {
		trimmed := strings.TrimRight(tagSource, " \t\r\n\f\v")
		selfClosing = len(trimmed) > 0 && trimmed[len(trimmed)-1] == '/'
	}

	if isVoid {
		st.states[index] = tagState{key: key, depth: depth, prevOpen: -1}
	} else {
		st.states[index] = tagState{key: key, depth: depth, selfClosed: selfClosing, prevOpen: st.lastOpen}
		st.lastOpen = int32(index)
		if key >= 0 {
			st.openInList[key]++
		} else {
			st.adjustCustom(custom, 1)
		}
	}
	st.count++

	if !selfClosing {
		// Deeper than maxDepth the tag is not nested: it stays in the open list for its end tag.
		if st.stackTop < maxDepth {
			if st.stackTop == len(st.stack) {
				st.stack = append(st.stack, make([]int32, len(st.stack))...)
			}
			st.stack[st.stackTop] = int32(index)
			st.stackTop++
			if key == int32(TagP) {
				st.openP++
			}
			if rules && key >= 0 {
				st.openByKey[key]++
			}
		}

		// <script>, <style>, <textarea>, <title>, ...: the content is text, not markup;
		// continue right before the matching end tag.
		if !st.isXML && key >= 0 && rawTextEnds[key] != "" {
			return findRawTextEnd(src, gt+1, rawTextEnds[key]) - 1
		}
	}
	return gt
}

// ----- implied end tags -----

// firstMisnested returns, among the tags left open at stack positions [from, to), the outermost one
// that a rule should have ended, or -1.
func (st *parseState) firstMisnested(from, to int) int {
	for s := from; s < to-1; s++ {
		key := st.states[st.stack[s]].key
		if key < 0 || !isRuleTarget[key] {
			continue
		}
		for inner := s + 1; inner < to; inner++ {
			innerKey := st.states[st.stack[inner]].key
			if innerKey >= 0 {
				if rule := impliedEndRules[innerKey]; rule != nil && rule.targets[key] {
					return s
				}
			}
		}
	}
	return -1
}

// repair scans again with the optional-end-tag rules from the tag at stack position stackPos up to regionEnd:
// the tags from it on are replaced, the ones before it are kept.
func (st *parseState) repair(stackPos, regionEnd int) {
	first := st.stack[stackPos]
	from := int(st.nodes[first].start)

	// Past the budget the part is left as it is, so that parsing stays linear on any markup.
	cost := regionEnd - from
	if cost > st.budget {
		return
	}
	st.budget -= cost

	st.truncateOpenList(first, -1)
	st.stackTop = stackPos
	st.count = int(first)

	clear(st.openByKey)
	st.openP = 0
	for s := 0; s < stackPos; s++ {
		key := st.states[st.stack[s]].key
		if key < 0 {
			continue
		}
		st.openByKey[key]++
		if key == int32(TagP) {
			st.openP++
		}
	}

	st.scan(true, from, regionEnd)
}

// truncateOpenList removes the tags from index first on from the open list (they are its tail).
// With end >= 0 the ones that have no end tag of their own end there.
func (st *parseState) truncateOpenList(first int32, end int) {
	k := st.lastOpen
	for k >= first {
		state := &st.states[k]
		node := &st.nodes[k]

		if state.key >= 0 {
			st.openInList[state.key]--
		} else {
			st.adjustCustom(node.custom, -1)
		}

		if end >= 0 && !state.selfClosed && node.innerEnd == 0 {
			node.innerEnd = int32(end)
			node.end = int32(end)
		}

		k = state.prevOpen
	}
	st.lastOpen = k
}

func (st *parseState) adjustCustom(name string, delta int32) {
	if st.custom == nil || name == "" {
		return
	}
	if c := st.custom[name]; c != nil {
		c.open += delta
	}
}

// internName returns the lower-case name as one string per document.
func (st *parseState) internName(name string) string {
	if name == "" {
		return ""
	}
	if st.custom == nil {
		st.custom = make(map[string]*customName)
	}
	lower := lowerKey(name)
	if c := st.custom[lower]; c != nil {
		return c.name
	}
	interned := strings.Clone(lower)
	st.custom[interned] = &customName{name: interned}
	return interned
}

// scopeStart returns the stack position of the outermost target of rule below the first stop tag, or -1.
func (st *parseState) scopeStart(rule *impliedEndRule) int {
	from := -1
	for s := st.stackTop - 1; s >= 0; s-- {
		k := st.states[st.stack[s]].key
		if k < 0 {
			continue
		}
		if rule.targets[k] {
			from = s
		} else if rule.stops[k] {
			break
		}
	}
	return from
}

// applyRuleInFastMode applies rule for the tag that starts at tagStart. If a tag it ends was itself misnested,
// that part is scanned again with the rules first.
func (st *parseState) applyRuleInFastMode(rule *impliedEndRule, tagStart int) {
	from := st.scopeStart(rule)
	if from < 0 {
		return
	}
	if m := st.firstMisnested(from, st.stackTop); m >= 0 {
		st.repair(m, tagStart-1)
	}
	st.closeInScope(rule, tagStart-2, false)
}

// closeInScope ends the outermost target found below the first stop tag together with everything opened inside it.
func (st *parseState) closeInScope(rule *impliedEndRule, end int, byKey bool) {
	from := st.scopeStart(rule)
	if from < 0 {
		return
	}
	first := st.stack[from]
	for st.stackTop > from {
		st.stackTop--
		key := st.states[st.stack[st.stackTop]].key
		if key == int32(TagP) {
			st.openP--
		}
		if byKey && key >= 0 {
			st.openByKey[key]--
		}
	}
	st.truncateOpenList(first, end)
}

// ----- helpers -----

var tagChar = [256]bool{'<': true, '=': true, '>': true}

func indexTagChar(s string) int {
	for j := 0; j < len(s); j++ {
		if tagChar[s[j]] {
			return j
		}
	}
	return -1
}

// isMarkupStart: as in HTML5, '<' starts markup only before an ASCII letter, '/', '!' or '?'.
func isMarkupStart(s string, i int) bool {
	if i+1 >= len(s) {
		return false
	}
	c := s[i+1]
	return (c|0x20) >= 'a' && (c|0x20) <= 'z' || c == '/' || c == '!' || c == '?'
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}

// findRawTextEnd returns the position of the '<' of the end tag (e.g. "</script"), or len(s) if there is none.
func findRawTextEnd(s string, from int, endTag string) int {
	for from < len(s) {
		found := indexFold(s[from:], endTag)
		if found < 0 {
			break
		}
		pos := from + found
		after := pos + len(endTag)
		// "</script>", "</script >", "</script/>" end it; "</scripts" does not.
		if after >= len(s) || s[after] == '>' || s[after] == '/' || isSpace(s[after]) {
			return pos
		}
		from = after
	}
	return len(s)
}

// findCommentEnd returns the position of the '>' that ends a comment whose content starts at contentStart.
// As in HTML5: "<!-->" and "<!--->" are empty comments, "-->" or "--!>" end a comment, otherwise it runs to the end.
func findCommentEnd(s string, contentStart int) int {
	content := s[min(contentStart, len(s)):]
	if strings.HasPrefix(content, ">") {
		return contentStart
	}
	if strings.HasPrefix(content, "->") {
		return contentStart + 1
	}
	for from := 0; from < len(content); {
		dashes := strings.Index(content[from:], "--")
		if dashes < 0 {
			break
		}
		pos := from + dashes + 2
		if pos < len(content) && content[pos] == '>' {
			return contentStart + pos
		}
		if pos+1 < len(content) && content[pos] == '!' && content[pos+1] == '>' {
			return contentStart + pos + 1
		}
		from = from + dashes + 1
	}
	return len(s) - 1
}

func extractTagName(tagSource string) string {
	tagSource = strings.TrimSpace(tagSource)
	if strings.HasPrefix(tagSource, "!--") {
		return tagSource[:3]
	}
	// The name ends at whitespace or '/': <br/> is "br", as <br /> is.
	i := 0
	for i < len(tagSource) && !isSpace(tagSource[i]) && tagSource[i] != '/' {
		i++
	}
	return tagSource[:i]
}

// ----- tables -----

type impliedEndRule struct {
	targets    [tagCount]bool
	stops      [tagCount]bool
	targetKeys []int32
}

func newRule(targets []Tag, stops ...[]Tag) *impliedEndRule {
	r := &impliedEndRule{}
	for _, t := range targets {
		r.targets[t] = true
		r.targetKeys = append(r.targetKeys, int32(t))
	}
	for _, list := range stops {
		for _, s := range list {
			r.stops[s] = true
		}
	}
	return r
}

// anyOpen reports whether a target is open at all: in well-formed markup it is not.
func (r *impliedEndRule) anyOpen(openByKey []int32) bool {
	for _, t := range r.targetKeys {
		if openByKey[t] > 0 {
			return true
		}
	}
	return false
}

// Elements that limit the scope of the rules (HTML5 "has an element in scope").
var scopeStops = []Tag{TagHtml, TagBody, TagTable, TagCaption, TagTd, TagTh, TagTemplate,
	TagObject, TagApplet, TagMarquee, TagButton}

// <p> is ended by the start of a block element (HTML5 "close a p element").
var pRule = newRule([]Tag{TagP}, scopeStops)

var closesP = tagSet(TagAddress, TagArticle, TagAside, TagBlockquote, TagCenter, TagDetails,
	TagDialog, TagDir, TagDiv, TagDl, TagFieldset, TagFigcaption, TagFigure,
	TagFooter, TagForm, TagH1, TagH2, TagH3, TagH4, TagH5, TagH6,
	TagHeader, TagHgroup, TagHr, TagListing, TagMain, TagMenu, TagNav,
	TagOl, TagP, TagPre, TagSearch, TagSection, TagSummary, TagTable,
	TagUl, TagLi, TagDd, TagDt, TagPlaintext, TagXmp)

var impliedEndRules = buildImpliedEndRules()

var isRuleTarget = func() (t [tagCount]bool) {
	for _, r := range impliedEndRules {
		if r != nil {
			for _, k := range r.targetKeys {
				t[k] = true
			}
		}
	}
	return
}()

func buildImpliedEndRules() (rules [tagCount]*impliedEndRule) {
	rules[TagLi] = newRule([]Tag{TagLi}, scopeStops, []Tag{TagUl, TagOl, TagMenu})

	dtdd := newRule([]Tag{TagDt, TagDd}, scopeStops, []Tag{TagDl})
	rules[TagDt], rules[TagDd] = dtdd, dtdd

	tableStops := []Tag{TagHtml, TagBody, TagTemplate, TagTable}
	cell := newRule([]Tag{TagTd, TagTh}, tableStops, []Tag{TagTr, TagTbody, TagThead, TagTfoot})
	rules[TagTd], rules[TagTh] = cell, cell
	rules[TagTr] = newRule([]Tag{TagTr, TagTd, TagTh}, tableStops, []Tag{TagTbody, TagThead, TagTfoot})
	section := newRule([]Tag{TagTbody, TagThead, TagTfoot, TagTr, TagTd, TagTh, TagCaption}, tableStops)
	rules[TagTbody], rules[TagThead], rules[TagTfoot] = section, section, section

	selectStops := []Tag{TagHtml, TagBody, TagTemplate, TagSelect, TagDatalist}
	rules[TagOption] = newRule([]Tag{TagOption}, selectStops, []Tag{TagOptgroup})
	rules[TagOptgroup] = newRule([]Tag{TagOption, TagOptgroup}, selectStops)
	return
}

// HTML5 void elements (incl. obsolete ones the parsing rules also treat as void) and declarations.
var voidTags = tagSet(TagArea, TagBase, TagBr, TagCol, TagEmbed, TagHr, TagImg, TagInput,
	TagLink, TagMeta, TagSource, TagTrack, TagWbr,
	TagBasefont, TagBgsound, TagFrame, TagKeygen, TagParam, TagIsindex,
	TagDoctype, TagXmlDeclaration)

// "</script", "</style", ... for the elements whose content is raw text (HTML5 RAWTEXT / RCDATA).
var rawTextEnds = func() (ends [tagCount]string) {
	for _, t := range []Tag{TagScript, TagStyle, TagTextarea, TagTitle, TagXmp, TagIframe, TagNoembed, TagNoframes} {
		ends[t] = "</" + tagNames[t]
	}
	return
}()

func tagSet(tags ...Tag) (set [tagCount]bool) {
	for _, t := range tags {
		set[t] = true
	}
	return
}

// Tag name → Tag, keyed by the lower-case name.
var tagLookup = func() map[string]Tag {
	m := make(map[string]Tag, tagCount)
	for i, name := range tagNames {
		m[name] = Tag(i)
	}
	return m
}()

// lookupTag finds a tag name case-insensitively without allocating.
func lookupTag(name string) (Tag, bool) {
	if len(name) == 0 || len(name) > 32 {
		return 0, false
	}
	if isLowerASCII(name) {
		t, ok := tagLookup[name]
		return t, ok
	}
	var buf [32]byte
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		buf[i] = c
	}
	t, ok := tagLookup[string(buf[:len(name)])] // no allocation: a map index with a converted []byte
	return t, ok
}

func isLowerASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= 'A' && c <= 'Z' || c >= 0x80 {
			return false
		}
	}
	return true
}

// lowerKey returns the lower-case name: the same string when it is already lower-case ASCII.
func lowerKey(s string) string {
	if isLowerASCII(s) {
		return s
	}
	return strings.ToLower(s)
}

const asciiSpace = " \t\r\n\f\v"
