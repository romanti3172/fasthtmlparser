// Package fasthtml is a fast, low-allocation HTML / XML parser for scraping: a Go port of z3l.FastHtmlParser.
//
// A document is a flat list of tags in document order; each tag holds only positions in the source.
// Text, attributes and entities are read lazily, when they are asked for. Documents are read-only
// and safe for concurrent reads.
//
// Every method of *Node accepts a nil receiver and returns nil / "" / false, so chains never panic:
//
//	price := doc.Find(fasthtml.By(fasthtml.TagDiv).ID("main")).Find(fasthtml.By(fasthtml.TagSpan).Class("price")).Text()
package fasthtml

import (
	"strings"
	"sync/atomic"
)

// Document is a parsed page.
type Document struct {
	source string
	nodes  []Node
	css    atomic.Pointer[cssTree] // sibling links and positions, built by the first selector that needs them
}

// Node is one tag of a document. Positions are byte offsets into the document source.
type Node struct {
	doc        *Document
	custom     string // lower-case name for tags not in Tag
	index      int32
	parent     int32 // index of the enclosing tag, -1 at the top level
	start      int32 // the opening '<'
	end        int32 // the last '>': of the end tag when there is one, otherwise of the opening tag
	innerStart int32 // right after the opening tag; 0 when the tag is not closed
	innerEnd   int32 // the last byte before the end tag; 0 when the tag is not closed
	depth      int32
	key        int16 // Tag, or -1
}

// Parse parses an HTML or XML page. The document keeps a reference to source and returns substrings of it.
func Parse(source string) *Document {
	doc := &Document{source: source}
	doc.nodes = parse(doc, source)
	return doc
}

// ParseBytes parses a page from bytes (copied once into a string).
func ParseBytes(source []byte) *Document { return Parse(string(source)) }

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
	return n.custom
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

// Children returns the direct children.
func (n *Node) Children() []*Node {
	if n == nil {
		return nil
	}
	var children []*Node
	nodes := n.doc.nodes
	for i := int(n.index) + 1; i < len(nodes) && nodes[i].end <= n.end; i++ {
		if nodes[i].parent == n.index {
			children = append(children, &nodes[i])
		}
	}
	return children
}

// NextSibling returns the next tag with the same parent, or nil.
func (n *Node) NextSibling() *Node {
	if n == nil {
		return nil
	}
	nodes := n.doc.nodes
	for i := int(n.index) + 1; i < len(nodes); i++ {
		if nodes[i].depth < n.depth {
			return nil
		}
		if nodes[i].parent == n.parent {
			return &nodes[i]
		}
	}
	return nil
}

// NextSiblingMatching returns the next sibling that matches q, or nil.
func (n *Node) NextSiblingMatching(q Query) *Node {
	for s := n.NextSibling(); s != nil; s = s.NextSibling() {
		if q.Matches(s) {
			return s
		}
	}
	return nil
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

// Query selects tags by name and, optionally, class, id, attribute value and visible text.
// It is a small value: build it inline, searching allocates nothing.
type Query struct {
	key       int16 // Tag+1, 0: by name or any
	name      string
	class     string
	id        string
	attr      string
	attrValue string
	text      string
	has       uint8
}

const (
	hasClass uint8 = 1 << iota
	hasID
	hasAttr
	hasText
)

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
		if n.key != q.key-1 && (n.key >= 0 || q.name == "" || !strings.EqualFold(n.custom, q.name)) {
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
	// the text last: it is the most expensive check
	if q.has&hasText != 0 && !n.isText(q.text) {
		return false
	}
	return true
}
