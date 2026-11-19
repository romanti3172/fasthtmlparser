// Package fasthtmlparser is a fast, low-allocation HTML / XML parser for scraping: a Go port of z3l.FastHtmlParser.
//
// A document is a flat list of tags in document order; each tag holds only positions in the source.
// Text, attributes and entities are read lazily, when they are asked for. Documents are read-only
// and safe for concurrent reads.
//
// Every method of *Node accepts a nil receiver and returns nil / "" / false, so chains never panic:
//
//	price := doc.Find(fasthtmlparser.By(fasthtmlparser.TagDiv).ID("main")).Find(fasthtmlparser.By(fasthtmlparser.TagSpan).Class("price")).Text()
package fasthtmlparser

import (
	"net/url"
	"strings"
	"sync/atomic"
)

// Document is a parsed page.
type Document struct {
	source      string
	nodes       []Node
	customNames []string                // lower-case names of the tags not in Tag; Node.custom is an index + 1
	css         atomic.Pointer[cssTree] // sibling links and positions, built by the first selector that needs them
	url         string
	base        *url.URL // url parsed once, for resolving relative links; nil when there is none
}

// Node is one tag of a document. Positions are byte offsets into the document source.
// 40 bytes: the tag list of a page is one allocation, and a smaller node means less memory to fill and walk.
type Node struct {
	doc        *Document
	index      int32
	parent     int32 // index of the enclosing tag, -1 at the top level
	start      int32 // the opening '<'
	end        int32 // the last '>': of the end tag when there is one, otherwise of the opening tag
	innerStart int32 // right after the opening tag; 0 when the tag is not closed
	innerEnd   int32 // the last byte before the end tag; 0 when the tag is not closed
	custom     int32 // for tags not in Tag: index + 1 into Document.customNames; 0 otherwise
	depth      int16 // at most maxDepth: deeper tags are not nested
	key        int16 // Tag, or -1
}

// customName returns the lower-case name of a tag not in Tag, or "".
func (n *Node) customName() string {
	if n.custom == 0 {
		return ""
	}
	return n.doc.customNames[n.custom-1]
}

// Parse parses an HTML or XML page. The document keeps a reference to source and returns substrings of it.
func Parse(source string) *Document {
	doc := &Document{source: source}
	doc.nodes = parse(doc, source)
	return doc
}

// ParseBytes parses a page from bytes (copied once into a string).
func ParseBytes(source []byte) *Document { return Parse(string(source)) }

// ParseWithURL parses a page and records its URL: relative links (Href, Src) are resolved against it.
func ParseWithURL(source, pageURL string) *Document {
	doc := Parse(source)
	doc.url = pageURL
	doc.base = absoluteURL(pageURL)
	return doc
}

// URL returns the page URL given to ParseWithURL, or "".
func (d *Document) URL() string { return d.url }

// Source returns the page source.
func (d *Document) Source() string { return d.source }

// Len returns the number of tags (comments included).
func (d *Document) Len() int { return len(d.nodes) }

// Node returns the tag at index i in document order.
func (d *Document) Node(i int) *Node { return &d.nodes[i] }

// ----- reading a tag -----

// Tag returns the tag as a Tag and true, or false for a name that is not one of the known tags.
func (n *Node) Tag() (Tag, bool) {
	if n == nil || n.key < 0 {
		return 0, false
	}
	return Tag(n.key), true
}

// Name returns the lower-case tag name ("div", "!--" for comments, "custom-tag", ...).
func (n *Node) Name() string {
	if n == nil {
		return ""
	}
	if n.key >= 0 {
		return tagNames[n.key]
	}
	return n.customName()
}

// Depth returns the nesting depth: 0 for top-level tags.
func (n *Node) Depth() int {
	if n == nil {
		return 0
	}
	return int(n.depth)
}

// Index returns the position of the tag in the document.
func (n *Node) Index() int {
	if n == nil {
		return -1
	}
	return int(n.index)
}

// Pos returns the byte offsets of the opening '<' and of the last '>'.
func (n *Node) Pos() (start, end int) {
	if n == nil {
		return -1, -1
	}
	return int(n.start), int(n.end)
}

// Body returns the inner HTML (trimmed); "" for tags that are not closed.
func (n *Node) Body() string {
	if n == nil || n.innerStart < 0 || n.innerEnd <= 0 {
		return ""
	}
	return strings.TrimSpace(n.doc.source[n.innerStart : n.innerEnd+1])
}

// OuterHTML returns the source of the whole element; "" for tags that are not closed.
func (n *Node) OuterHTML() string {
	if n == nil || n.innerStart <= 0 || n.innerEnd <= 0 {
		return ""
	}
	return strings.TrimSpace(n.doc.source[n.start : n.end+1])
}

// Text returns the visible text with entities decoded. With child tags: tags, comments and the content of
// script, style, noscript, template, noframes, noembed removed, whitespace collapsed; without: the inner text as is (trimmed).
func (n *Node) Text() string {
	if n == nil {
		return ""
	}
	body := n.Body()

	// No child tags and no '<' at all: the body is the text, except in script, style, textarea, title, ...
	if !n.HasChildren() && (strings.IndexByte(body, '<') < 0 || n.key >= 0 && rawTextEnds[n.key] != "") {
		return decodeHTML(body, false)
	}
	return clearTags(body)
}

// HasChildren reports whether another tag starts inside this one.
func (n *Node) HasChildren() bool {
	if n == nil || int(n.index)+1 >= len(n.doc.nodes) {
		return false
	}
	return n.doc.nodes[n.index+1].start < n.end
}

// ----- navigation -----

// Parent returns the enclosing tag, or nil.
func (n *Node) Parent() *Node {
	if n == nil || n.parent < 0 {
		return nil
	}
	return &n.doc.nodes[n.parent]
}

// Children returns the direct children: the tags inside n one level deeper.
func (n *Node) Children() []*Node {
	if n == nil {
		return nil
	}
	var children []*Node
	nodes := n.doc.nodes
	for i := int(n.index) + 1; i < len(nodes) && nodes[i].end <= n.end; i++ {
		if nodes[i].depth == n.depth+1 {
			children = append(children, &nodes[i])
		}
	}
	return children
}

// Next returns the tag that follows n in document order (its first child, if it has one), or nil.
func (n *Node) Next() *Node {
	if n == nil || int(n.index)+1 >= len(n.doc.nodes) {
		return nil
	}
	return &n.doc.nodes[n.index+1]
}

// NextMatching returns the first tag after the end of n that matches q (tags inside n are skipped), or nil.
func (n *Node) NextMatching(q Query) *Node {
	if n == nil {
		return nil
	}
	nodes := n.doc.nodes
	for i := int(n.index) + 1; i < len(nodes); i++ {
		if nodes[i].start > n.end && q.Matches(&nodes[i]) {
			return &nodes[i]
		}
	}
	return nil
}

// Previous returns the tag before n in document order, or nil.
func (n *Node) Previous() *Node {
	if n == nil || n.index < 1 {
		return nil
	}
	return &n.doc.nodes[n.index-1]
}

// NextSibling returns the next tag at the same level inside the same parent, or nil.
func (n *Node) NextSibling() *Node { return n.NextSiblingMatching(Any()) }

// NextSiblingMatching returns the next sibling that matches q, or nil.
func (n *Node) NextSiblingMatching(q Query) *Node {
	if n == nil {
		return nil
	}
	// deeper tags are inside the following siblings; a shallower one ends the parent
	for next := n.Next(); next != nil && next.depth >= n.depth; next = next.Next() {
		if next.depth == n.depth && q.Matches(next) {
			return next
		}
	}
	return nil
}

// PreviousSibling returns the previous tag at the same level inside the same parent, or nil.
func (n *Node) PreviousSibling() *Node { return n.PreviousSiblingMatching(Any()) }

// PreviousSiblingMatching returns the nearest previous sibling that matches q, or nil.
func (n *Node) PreviousSiblingMatching(q Query) *Node {
	if n == nil {
		return nil
	}
	// deeper tags are inside the previous siblings; a shallower one is the parent
	for prev := n.Previous(); prev != nil && prev.depth >= n.depth; prev = prev.Previous() {
		if prev.depth == n.depth && q.Matches(prev) {
			return prev
		}
	}
	return nil
}

// TextAfterTagUntilNextOne returns the source text right after n up to the next '<' (or the end of the page).
func (n *Node) TextAfterTagUntilNextOne() string {
	if n == nil {
		return ""
	}
	from := int(n.end) + 1
	if from >= len(n.doc.source) {
		return ""
	}
	rest := n.doc.source[from:]
	if next := strings.IndexByte(rest, '<'); next >= 0 {
		return rest[:next]
	}
	return rest
}

// TextNoTagsAfterTagUntilNextOne is TextAfterTagUntilNextOne as visible text: entities decoded, whitespace collapsed.
func (n *Node) TextNoTagsAfterTagUntilNextOne() string {
	return clearTags(n.TextAfterTagUntilNextOne())
}

// TextAfterUntil returns the source between the end of n and the next tag that matches q;
// "" when there is none.
func (n *Node) TextAfterUntil(q Query) string {
	until := n.NextMatching(q)
	if until == nil {
		return ""
	}
	return n.doc.source[n.end+1 : until.start]
}

// Source returns the opening tag without '<' and '>': the name and the attributes.
func (n *Node) Source() string {
	if n == nil {
		return ""
	}
	src := n.doc.source
	start, end := int(n.start), int(n.end)
	var length int
	switch {
	case n.innerStart > 0 && n.innerEnd > 0:
		length = int(n.innerStart) - start - 2
	case end > 0 && src[end-1] == '/':
		length = end - start - 2
	default:
		length = end - start - 1
	}
	return strings.TrimSpace(src[start+1 : start+1+length])
}

// ParentMatching returns the nearest enclosing tag that matches q, or nil.
func (n *Node) ParentMatching(q Query) *Node {
	for p := n.Parent(); p != nil; p = p.Parent() {
		if q.Matches(p) {
			return p
		}
	}
	return nil
}

// ----- search -----

// Find returns the first tag in the document that matches q, or nil.
func (d *Document) Find(q Query) *Node {
	if d == nil {
		return nil
	}
	return find(d.nodes, 0, int32(len(d.source)), q)
}

// FindAll returns all tags in the document that match q.
func (d *Document) FindAll(q Query) []*Node {
	if d == nil {
		return nil
	}
	return findAll(d.nodes, 0, int32(len(d.source)), q)
}

// Find returns the first tag inside n (a descendant) that matches q, or nil.
func (n *Node) Find(q Query) *Node {
	if n == nil {
		return nil
	}
	return find(n.doc.nodes, int(n.index)+1, n.end, q)
}

// FindAll returns all tags inside n (descendants) that match q.
func (n *Node) FindAll(q Query) []*Node {
	if n == nil {
		return nil
	}
	return findAll(n.doc.nodes, int(n.index)+1, n.end, q)
}

// Is reports whether the tag matches q.
func (n *Node) Is(q Query) bool { return n != nil && q.Matches(n) }

func find(nodes []Node, first int, end int32, q Query) *Node {
	for i := first; i < len(nodes); i++ {
		n := &nodes[i]
		if n.end > end {
			break
		}
		if q.Matches(n) {
			return n
		}
	}
	return nil
}

func findAll(nodes []Node, first int, end int32, q Query) []*Node {
	var result []*Node
	for i := first; i < len(nodes); i++ {
		n := &nodes[i]
		if n.end > end {
			break
		}
		if q.Matches(n) {
			result = append(result, n)
		}
	}
	return result
}

// Query selects tags by name and, optionally, class, id, attribute value, visible text, inner HTML and
// the source of the opening tag. It is a small value: build it inline, searching allocates nothing.
type Query struct {
	key       int16 // Tag+1, 0: by name or any
	name      string
	class     string
	id        string
	attr      string
	attrValue string
	text      string
	rare      *rareCriteria // the criteria shortcuts use now and then
	has       uint8
}

type rareCriteria struct {
	idStartsWith, textStartsWith, body, bodyStartsWith, source string
	has                                                        uint8
}

const (
	hasClass uint8 = 1 << iota
	hasID
	hasAttr
	hasText
	hasRare
)

const (
	hasIDStartsWith uint8 = 1 << iota
	hasTextStartsWith
	hasBody
	hasBodyStartsWith
	hasSource
)

func (q Query) withRare(set func(r *rareCriteria)) Query {
	r := rareCriteria{}
	if q.rare != nil {
		r = *q.rare // a copy: queries are values
	}
	set(&r)
	q.rare = &r
	q.has |= hasRare
	return q
}

// IDStartsWith adds a criterion: the decoded id starts with prefix.
func (q Query) IDStartsWith(prefix string) Query {
	return q.withRare(func(r *rareCriteria) { r.idStartsWith = prefix; r.has |= hasIDStartsWith })
}

// TextStartsWith adds a criterion: the visible text starts with prefix.
func (q Query) TextStartsWith(prefix string) Query {
	return q.withRare(func(r *rareCriteria) { r.textStartsWith = prefix; r.has |= hasTextStartsWith })
}

// Body adds a criterion: the inner HTML (trimmed) equals body.
func (q Query) Body(body string) Query {
	return q.withRare(func(r *rareCriteria) { r.body = body; r.has |= hasBody })
}

// BodyStartsWith adds a criterion: the inner HTML (trimmed) starts with prefix.
func (q Query) BodyStartsWith(prefix string) Query {
	return q.withRare(func(r *rareCriteria) { r.bodyStartsWith = prefix; r.has |= hasBodyStartsWith })
}

// Source adds a criterion: the opening tag without '<' and '>' (see Node.Source) equals source.
func (q Query) Source(source string) Query {
	return q.withRare(func(r *rareCriteria) { r.source = source; r.has |= hasSource })
}

// By selects tags with a known name.
func By(tag Tag) Query { return Query{key: int16(tag) + 1} }

// ByName selects tags by name, case-insensitively; it works for names that are not in Tag.
func ByName(name string) Query {
	if t, ok := lookupTag(name); ok {
		return Query{key: int16(t) + 1, name: name}
	}
	return Query{name: name}
}

// Any selects every tag.
func Any() Query { return Query{} }

// Class adds a class criterion: one of the space-separated classes, case-insensitive.
func (q Query) Class(class string) Query { q.class = class; q.has |= hasClass; return q }

// ID adds an id criterion.
func (q Query) ID(id string) Query { q.id = id; q.has |= hasID; return q }

// Attr adds an attribute criterion: the decoded value equals value.
func (q Query) Attr(name, value string) Query {
	q.attr, q.attrValue = name, value
	q.has |= hasAttr
	return q
}

// Text adds a visible-text criterion (see Node.Text).
func (q Query) Text(text string) Query { q.text = text; q.has |= hasText; return q }

// Matches reports whether n matches every criterion.
func (q Query) Matches(n *Node) bool {
	if n == nil {
		return false
	}
	if q.key != 0 {
		if n.key != q.key-1 && (n.key >= 0 || q.name == "" || !strings.EqualFold(n.customName(), q.name)) {
			return false
		}
	} else if q.name != "" && !strings.EqualFold(n.Name(), q.name) {
		return false
	}
	if q.has == 0 {
		return true
	}
	if q.has&hasID != 0 && (q.id == "" || !attrEquals(n.attrRaw("id"), q.id)) {
		return false
	}
	if q.has&hasAttr != 0 && !attrEquals(n.attrRaw(q.attr), q.attrValue) {
		return false
	}
	if q.has&hasClass != 0 && !n.HasClass(q.class) {
		return false
	}
	r := q.rare
	if r != nil {
		if r.has&hasIDStartsWith != 0 && !attrStartsWith(n.attrRaw(AttrId), r.idStartsWith) {
			return false
		}
		if r.has&hasSource != 0 && n.Source() != r.source {
			return false
		}
		if r.has&hasBody != 0 && n.Body() != r.body {
			return false
		}
		if r.has&hasBodyStartsWith != 0 && !strings.HasPrefix(n.Body(), r.bodyStartsWith) {
			return false
		}
	}
	// the text last: it is the most expensive check
	if q.has&hasText != 0 && !n.isText(q.text) {
		return false
	}
	if r != nil && r.has&hasTextStartsWith != 0 && !strings.HasPrefix(n.Text(), r.textStartsWith) {
		return false
	}
	return true
}

// attrStartsWith: whether the decoded value starts with prefix; it allocates only when the raw value has '&' or CR.
func attrStartsWith(raw, prefix string) bool {
	if needsDecoding(raw) {
		raw = decodeHTML(raw, true)
	}
	return strings.HasPrefix(raw, prefix)
}
