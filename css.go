package fasthtml

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"
)

// CSSQuery is a compiled CSS selector (or a comma-separated list of them). Compile once and reuse it on any
// number of documents, from any number of goroutines; the string methods (Node.CSS, Document.CSS, ...)
// compile and cache selectors automatically.
//
// Supported: type, *, #id, .class, attribute selectors ([a], =, ~=, |=, ^=, $=, *=, with the i / s flags),
// the combinators (descendant, >, +, ~), selector lists (a, b) and the pseudo-classes :first-child, :last-child,
// :only-child, :first-of-type, :last-of-type, :only-of-type, :nth-child(An+B [of S]), :nth-last-child(),
// :nth-of-type(), :nth-last-of-type(), :not(), :is(), :where(), :has(), :empty, :root, :scope, :link, :any-link,
// :checked, :disabled, :enabled, :required, :optional, and :contains("text") (the visible text contains the string).
// Pseudo-elements and namespaces are not supported.
//
// HTML rules: tag and attribute names are case-insensitive; ids, classes and attribute values are case-sensitive,
// except the values the HTML standard lists (type, rel, lang, method, ...); the i / s flags override that.
// Attribute values are compared decoded. Only elements are selected, never comments.
//
// As querySelectorAll: node.CSS(...) returns the tags inside the node, while the whole selector is matched against
// the document. :scope is the node; a selector may start with '>' for its children: ul.CSS("> li").
type CSSQuery struct {
	selector string
	list     *cssList
}

// CompileCSS compiles a CSS selector.
func CompileCSS(selector string) (q *CSSQuery, err error) {
	defer func() {
		if r := recover(); r != nil {
			e, ok := r.(cssError)
			if !ok {
				panic(r)
			}
			q, err = nil, e
		}
	}()
	c := &cssCompiler{s: selector}
	list := c.parseList(false, true)
	if c.p < len(c.s) {
		c.fail(fmt.Sprintf("unexpected '%c'", c.s[c.p]))
	}
	return &CSSQuery{selector: selector, list: list}, nil
}

// MustCompileCSS is CompileCSS that panics on an invalid selector.
func MustCompileCSS(selector string) *CSSQuery {
	q, err := CompileCSS(selector)
	if err != nil {
		panic(err)
	}
	return q
}

// String returns the source of the selector.
func (q *CSSQuery) String() string { return q.selector }

var cssCache sync.Map
var cssCacheSize atomic.Int32

func cachedCSS(selector string) *CSSQuery {
	if q, ok := cssCache.Load(selector); ok {
		return q.(*CSSQuery)
	}
	q := MustCompileCSS(selector)
	if cssCacheSize.Add(1) > 1024 { // dynamically built selectors must not grow the cache forever
		cssCache.Clear()
		cssCacheSize.Store(0)
	}
	cssCache.Store(selector, q)
	return q
}

// All returns the matching tags in the document, in document order.
func (q *CSSQuery) All(d *Document) []*Node {
	if d == nil {
		return nil
	}
	return q.scan(d, nil, 0, int32(len(d.source)), false)
}

// First returns the first matching tag in the document, or nil.
func (q *CSSQuery) First(d *Document) *Node {
	if d == nil {
		return nil
	}
	return first(q.scan(d, nil, 0, int32(len(d.source)), true))
}

// AllIn returns the matching tags inside n (as querySelectorAll).
func (q *CSSQuery) AllIn(n *Node) []*Node {
	if n == nil {
		return nil
	}
	return q.scan(n.doc, n, int(n.index)+1, n.end, false)
}

// FirstIn returns the first matching tag inside n, or nil.
func (q *CSSQuery) FirstIn(n *Node) *Node {
	if n == nil {
		return nil
	}
	return first(q.scan(n.doc, n, int(n.index)+1, n.end, true))
}

// Matches reports whether the tag matches (as element.matches(): :scope is the tag itself); false for nil and comments.
func (q *CSSQuery) Matches(n *Node) bool {
	return n != nil && isElement(n) && q.list.matches(n, &cssContext{doc: n.doc, scope: n, anchor: n})
}

func first(nodes []*Node) *Node {
	if len(nodes) == 0 {
		return nil
	}
	return nodes[0]
}

func (q *CSSQuery) scan(d *Document, scope *Node, from int, end int32, firstOnly bool) []*Node {
	ctx := &cssContext{doc: d, scope: scope, anchor: scope}
	var result []*Node
	for i := from; i < len(d.nodes); i++ {
		n := &d.nodes[i]
		if n.end > end {
			break
		}
		if q.list.matches(n, ctx) {
			result = append(result, n)
			if firstOnly {
				break
			}
		}
	}
	return result
}

// CSS returns the tags in the document that match selector. It panics on an invalid selector (see CompileCSS).
func (d *Document) CSS(selector string) []*Node { return cachedCSS(selector).All(d) }

// CSSFirst returns the first tag in the document that matches selector, or nil.
func (d *Document) CSSFirst(selector string) *Node { return cachedCSS(selector).First(d) }

// CSS returns the tags inside n that match selector. It panics on an invalid selector (see CompileCSS).
func (n *Node) CSS(selector string) []*Node { return cachedCSS(selector).AllIn(n) }

// CSSFirst returns the first tag inside n that matches selector, or nil.
func (n *Node) CSSFirst(selector string) *Node { return cachedCSS(selector).FirstIn(n) }

// IsCSS reports whether n matches selector (as element.matches()).
func (n *Node) IsCSS(selector string) bool { return cachedCSS(selector).Matches(n) }

// isElement: not a comment, the doctype or the XML declaration.
func isElement(n *Node) bool {
	if n.key >= 0 {
		return n.key != int16(TagComment) && n.key != int16(TagDoctype) && n.key != int16(TagXmlDeclaration)
	}
	return n.custom != "" && n.custom[0] != '!' && n.custom[0] != '?'
}

// ----- the tree: sibling links and positions, built once per document -----

type cssTree struct {
	nodes    []Node
	previous []int32 // previous element sibling, -1 for none
	next     []int32
	position []int32 // 1-based position among the element siblings
	fromEnd  []int32
	types    atomic.Pointer[typePositions]
}

type typePositions struct{ position, fromEnd []int32 }

func (d *Document) tree() *cssTree {
	if t := d.css.Load(); t != nil {
		return t
	}
	t := newCSSTree(d.nodes)
	if d.css.CompareAndSwap(nil, t) { // a benign race: both results are the same
		return t
	}
	return d.css.Load()
}

func newCSSTree(nodes []Node) *cssTree {
	count := len(nodes)
	t := &cssTree{nodes: nodes, previous: make([]int32, count), next: make([]int32, count),
		position: make([]int32, count), fromEnd: make([]int32, count)}

	// per parent (slot = parent index + 1, slot 0: the top level): its last element child so far and their number
	lastChild := make([]int32, count+1)
	children := make([]int32, count+1)
	for i := range lastChild {
		lastChild[i] = -1
	}
	for i := range nodes {
		t.previous[i], t.next[i] = -1, -1
		if !isElement(&nodes[i]) {
			continue
		}
		slot := nodes[i].parent + 1
		prev := lastChild[slot]
		t.previous[i] = prev
		if prev >= 0 {
			t.next[prev] = int32(i)
		}
		lastChild[slot] = int32(i)
		children[slot]++
		t.position[i] = children[slot]
	}
	for i := range nodes {
		if t.position[i] > 0 {
			t.fromEnd[i] = children[nodes[i].parent+1] - t.position[i] + 1
		}
	}
	return t
}

func (t *cssTree) at(i int32) *Node {
	if i < 0 {
		return nil
	}
	return &t.nodes[i]
}

func (t *cssTree) prev(n *Node) *Node   { return t.at(t.previous[n.index]) }
func (t *cssTree) nextOf(n *Node) *Node { return t.at(t.next[n.index]) }

type typeKey struct {
	parent int32
	key    int16
	name   string
}

func (t *cssTree) typePositions() *typePositions {
	if tp := t.types.Load(); tp != nil {
		return tp
	}
	count := len(t.nodes)
	tp := &typePositions{position: make([]int32, count), fromEnd: make([]int32, count)}
	seen := make(map[typeKey]int32)
	key := func(n *Node) typeKey {
		k := typeKey{parent: n.parent, key: n.key}
		if n.key < 0 {
			k.name = n.custom
		}
		return k
	}
	for i := range t.nodes {
		if t.position[i] == 0 {
			continue // not an element
		}
		k := key(&t.nodes[i])
		seen[k]++
		tp.position[i] = seen[k]
	}
	for i := range t.nodes {
		if tp.position[i] > 0 {
			tp.fromEnd[i] = seen[key(&t.nodes[i])] - tp.position[i] + 1
		}
	}
	t.types.CompareAndSwap(nil, tp)
	return t.types.Load()
}

// ----- matching -----

type cssCombinator uint8

const (
	combNone       cssCombinator = iota
	combDescendant               // "a b"
	combChild                    // "a > b"
	combAdjacent                 // "a + b"
	combSibling                  // "a ~ b"
)

// cssContext: the document, the tag the query was called on (:scope, nil for the document)
// and the tag a relative selector starts from (:has(> a), or node.CSS("> li"); nil: the document).
type cssContext struct {
	doc    *Document
	scope  *Node
	anchor *Node
}

func (c *cssContext) tree() *cssTree { return c.doc.tree() }

type cssList struct{ items []*cssComplex }

func (l *cssList) matches(n *Node, ctx *cssContext) bool {
	for _, item := range l.items {
		if item.matches(len(item.compounds)-1, n, ctx) {
			return true
		}
	}
	return false
}

// cssComplex: compounds joined by combinators; combinators[i] stands between compounds[i] and [i+1].
type cssComplex struct {
	leading     cssCombinator
	compounds   []*cssCompound
	combinators []cssCombinator
}

type cssCompound struct {
	key     int16  // Tag, or -1
	name    string // lower-case name not in Tag; "" with key -1: any element
	simples []cssSimple
}

func (c *cssCompound) matches(n *Node, ctx *cssContext) bool {
	switch {
	case c.key >= 0:
		if n.key != c.key {
			return false
		}
	case c.name != "":
		if n.key >= 0 || n.custom != c.name {
			return false
		}
	default:
		if !isElement(n) {
			return false
		}
	}
	for _, s := range c.simples {
		if !s.matches(n, ctx) {
			return false
		}
	}
	return true
}

func parentOf(n *Node) *Node {
	if n.parent < 0 {
		return nil
	}
	return &n.doc.nodes[n.parent]
}

// matches: n matches compound i and the part to the left of it matches the tags the combinators lead to,
// right to left as browsers do.
func (s *cssComplex) matches(i int, n *Node, ctx *cssContext) bool {
	if !s.compounds[i].matches(n, ctx) {
		return false
	}
	if i == 0 {
		return s.leading == combNone || isRelated(s.leading, ctx.anchor, n, ctx)
	}
	switch s.combinators[i-1] {
	case combChild:
		p := parentOf(n)
		return p != nil && s.matches(i-1, p, ctx)
	case combDescendant:
		for p := parentOf(n); p != nil; p = parentOf(p) {
			if s.matches(i-1, p, ctx) {
				return true
			}
		}
	case combAdjacent:
		p := ctx.tree().prev(n)
		return p != nil && s.matches(i-1, p, ctx)
	case combSibling:
		t := ctx.tree()
		for p := t.prev(n); p != nil; p = t.prev(p) {
			if s.matches(i-1, p, ctx) {
				return true
			}
		}
	}
	return false
}

// isRelated: n stands in the combinator relation to anchor (nil: the document).
func isRelated(c cssCombinator, anchor, n *Node, ctx *cssContext) bool {
	switch c {
	case combChild:
		if anchor == nil {
			return n.parent < 0
		}
		return n.parent == anchor.index
	case combDescendant:
		if anchor == nil {
			return true
		}
		for p := parentOf(n); p != nil; p = parentOf(p) {
			if p == anchor {
				return true
			}
		}
	case combAdjacent:
		return anchor != nil && ctx.tree().prev(n) == anchor
	case combSibling:
		return anchor != nil && anchor.index < n.index && anchor.parent == n.parent && isElement(anchor)
	}
	return false
}

// hasMatch (:has): whether a tag in the anchor's scope matches the relative selector.
func hasMatch(relative *cssList, anchor *Node, ctx *cssContext) bool {
	nodes := anchor.doc.nodes
	inner := &cssContext{doc: ctx.doc, scope: ctx.scope, anchor: anchor}
	for _, item := range relative.items {
		last := len(item.compounds) - 1
		i := int(anchor.index) + 1
		if item.leading == combDescendant || item.leading == combChild {
			// the tags inside the anchor
			for ; i < len(nodes) && nodes[i].end <= anchor.end; i++ {
				if isElement(&nodes[i]) && item.matches(last, &nodes[i], inner) {
					return true
				}
			}
		} else {
			// the following siblings and the tags inside them, up to the end of the parent
			for i < len(nodes) && nodes[i].end <= anchor.end {
				i++
			}
			end := int32(1<<31 - 1)
			if p := parentOf(anchor); p != nil {
				end = p.end
			}
			for ; i < len(nodes) && nodes[i].end <= end; i++ {
				if isElement(&nodes[i]) && item.matches(last, &nodes[i], inner) {
					return true
				}
			}
		}
	}
	return false
}

func isCSSSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\f' || c == '\r' }

// containsWord: whether a whitespace-separated list contains word.
func containsWord(list, word string, fold bool) bool {
	if word == "" || len(list) < len(word) {
		return false
	}
	for {
		start := 0
		for start < len(list) && isCSSSpace(list[start]) {
			start++
		}
		if start == len(list) {
			return false
		}
		list = list[start:]
		end := strings.IndexAny(list, " \t\n\f\r")
		item := list
		if end >= 0 {
			item = list[:end]
		}
		if equal(item, word, fold) {
			return true
		}
		if end < 0 {
			return false
		}
		list = list[end:]
	}
}

func equal(a, b string, fold bool) bool {
	if fold {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// hasAttribute: whether the element has the attribute, with or without a value.
func hasAttribute(n *Node, name string) bool {
	if n.attrRaw(name) != "" {
		return true
	}
	if n.key == int16(TagComment) {
		return false
	}
	s := attributeScanner(n)
	for {
		ns, nl, _, _, ok := s.next()
		if !ok {
			return false
		}
		if strings.EqualFold(s.src[ns:ns+nl], name) {
			return true
		}
	}
}

func decoded(raw string) string { return decodeHTML(raw, true) }

// ----- simple selectors -----

type cssSimple interface {
	matches(n *Node, ctx *cssContext) bool
}

type cssID struct{ id string }

func (s cssID) matches(n *Node, _ *cssContext) bool { return attrEquals(n.attrRaw("id"), s.id) }

type cssClass struct{ name string }

func (s cssClass) matches(n *Node, _ *cssContext) bool {
	raw := n.attrRaw("class")
	return raw != "" && containsWord(decoded(raw), s.name, false)
}

type attrOp uint8

const (
	opExists    attrOp = iota // [a]
	opEquals                  // [a=v]
	opIncludes                // [a~=v]
	opDash                    // [a|=v]
	opPrefix                  // [a^=v]
	opSuffix                  // [a$=v]
	opSubstring               // [a*=v]
)

type cssAttribute struct {
	name  string
	op    attrOp
	value string
	fold  bool
}

// HTML: the values of these attributes are compared ignoring ASCII case unless the selector has the 's' flag.
var caseInsensitiveValues = func() map[string]bool {
	m := map[string]bool{}
	for _, v := range strings.Fields(`accept accept-charset align alink axis bgcolor charset checked clear codetype color
		compact declare defer dir direction disabled enctype face frame hreflang http-equiv
		lang language link media method multiple nohref noresize noshade nowrap readonly
		rel rev rules scope scrolling selected shape target text type valign valuetype vlink`) {
		m[v] = true
	}
	return m
}()

func (s cssAttribute) matches(n *Node, _ *cssContext) bool {
	raw := n.attrRaw(s.name)
	if raw == "" {
		// missing or empty: only these can match an empty value
		switch s.op {
		case opExists:
			return hasAttribute(n, s.name)
		case opEquals, opDash:
			return s.value == "" && hasAttribute(n, s.name)
		}
		return false
	}
	if s.op == opExists {
		return true
	}
	actual := decoded(raw)
	switch s.op {
	case opEquals:
		return equal(actual, s.value, s.fold)
	case opIncludes:
		return !strings.ContainsAny(s.value, " \t\n\f\r") && containsWord(actual, s.value, s.fold)
	case opDash:
		return equal(actual, s.value, s.fold) ||
			len(actual) > len(s.value) && actual[len(s.value)] == '-' && equal(actual[:len(s.value)], s.value, s.fold)
	case opPrefix:
		return s.value != "" && len(actual) >= len(s.value) && equal(actual[:len(s.value)], s.value, s.fold)
	case opSuffix:
		return s.value != "" && len(actual) >= len(s.value) && equal(actual[len(actual)-len(s.value):], s.value, s.fold)
	case opSubstring:
		if s.value == "" {
			return false
		}
		if !s.fold {
			return strings.Contains(actual, s.value)
		}
		for i := 0; i+len(s.value) <= len(actual); i++ {
			if strings.EqualFold(actual[i:i+len(s.value)], s.value) {
				return true
			}
		}
	}
	return false
}

// cssNth: :nth-child(An+B [of S]) and its relatives; :first-child is :nth-child(1), and so on.
type cssNth struct {
	a, b    int
	fromEnd bool
	ofType  bool
	of      *cssList
}

func (s cssNth) matches(n *Node, ctx *cssContext) bool {
	t := ctx.tree()
	var position int
	switch {
	case s.of != nil:
		if !s.of.matches(n, ctx) {
			return false
		}
		position = 1
		step := t.prev
		if s.fromEnd {
			step = t.nextOf
		}
		for sib := step(n); sib != nil; sib = step(sib) {
			if s.of.matches(sib, ctx) {
				position++
			}
		}
	case s.ofType:
		tp := t.typePositions()
		if s.fromEnd {
			position = int(tp.fromEnd[n.index])
		} else {
			position = int(tp.position[n.index])
		}
	default:
		if s.fromEnd {
			position = int(t.fromEnd[n.index])
		} else {
			position = int(t.position[n.index])
		}
	}
	if position <= 0 {
		return false // not an element
	}
	// position = a*n + b for some n >= 0
	if s.a == 0 {
		return position == s.b
	}
	diff := position - s.b
	return diff%s.a == 0 && diff/s.a >= 0
}

type cssNot struct{ list *cssList }

func (s cssNot) matches(n *Node, ctx *cssContext) bool { return !s.list.matches(n, ctx) }

type cssIs struct{ list *cssList }

func (s cssIs) matches(n *Node, ctx *cssContext) bool { return s.list.matches(n, ctx) }

type cssHas struct{ list *cssList }

func (s cssHas) matches(n *Node, ctx *cssContext) bool { return hasMatch(s.list, n, ctx) }

type cssContains struct{ text string }

func (s cssContains) matches(n *Node, _ *cssContext) bool { return strings.Contains(n.Text(), s.text) }

type pseudoKind uint8

const (
	pseudoRoot pseudoKind = iota
	pseudoScope
	pseudoEmpty
	pseudoLink
	pseudoChecked
	pseudoDisabled
	pseudoEnabled
	pseudoRequired
	pseudoOptional
)

type cssPseudo struct{ kind pseudoKind }

func (s cssPseudo) matches(n *Node, ctx *cssContext) bool {
	name := n.Name()
	switch s.kind {
	case pseudoRoot:
		return isRoot(n, ctx)
	case pseudoScope:
		if ctx.scope != nil {
			return n == ctx.scope
		}
		return isRoot(n, ctx)
	case pseudoEmpty:
		return isEmpty(n)
	case pseudoLink:
		return (name == "a" || name == "area") && hasAttribute(n, "href")
	case pseudoChecked:
		return name == "input" && hasAttribute(n, "checked") || name == "option" && hasAttribute(n, "selected")
	case pseudoDisabled:
		return isFormControl(name) && hasAttribute(n, "disabled")
	case pseudoEnabled:
		return isFormControl(name) && !hasAttribute(n, "disabled")
	case pseudoRequired:
		return isInput(name) && hasAttribute(n, "required")
	case pseudoOptional:
		return isInput(name) && !hasAttribute(n, "required")
	}
	return false
}

// the first top-level element: <html> on a page
func isRoot(n *Node, ctx *cssContext) bool { return n.parent < 0 && ctx.tree().prev(n) == nil }

func isFormControl(name string) bool {
	switch name {
	case "button", "input", "select", "textarea", "optgroup", "option", "fieldset":
		return true
	}
	return false
}

func isInput(name string) bool { return name == "input" || name == "select" || name == "textarea" }

// isEmpty: no child elements and no text; comments are allowed.
func isEmpty(n *Node) bool {
	if n.innerStart <= 0 || n.innerEnd <= 0 {
		return true // a void or unclosed tag has no content
	}
	position := n.innerStart
	nodes := n.doc.nodes
	for i := int(n.index) + 1; i < len(nodes) && nodes[i].end <= n.end; i++ {
		child := &nodes[i]
		if isElement(child) || child.start > position {
			return false // an element, or text before a comment
		}
		position = max(position, child.end+1)
	}
	return position > n.innerEnd
}

// ----- compiler -----

type cssError struct{ msg string }

func (e cssError) Error() string { return e.msg }

// cssCompiler parses a selector list (Selectors Level 3 and the common Level 4 pseudo-classes).
// Identifiers and strings follow the CSS syntax, escapes included (#a\:b, .\31 0, [title="a\"b"]).
type cssCompiler struct {
	s string
	p int
}

func (c *cssCompiler) fail(msg string) {
	panic(cssError{fmt.Sprintf("invalid CSS selector %q at position %d: %s", c.s, c.p, msg)})
}

func (c *cssCompiler) peek(offset int) byte {
	if c.p+offset < len(c.s) {
		return c.s[c.p+offset]
	}
	return 0
}

func (c *cssCompiler) skipSpace() bool {
	start := c.p
	for c.p < len(c.s) && isCSSSpace(c.s[c.p]) {
		c.p++
	}
	return c.p > start
}

func (c *cssCompiler) expect(ch byte) {
	if c.peek(0) != ch {
		if c.p >= len(c.s) {
			c.fail(fmt.Sprintf("'%c' expected", ch))
		}
		c.fail(fmt.Sprintf("'%c' expected instead of '%c'", ch, c.s[c.p]))
	}
	c.p++
}

// selector-list: complex (',' complex)*
func (c *cssCompiler) parseList(relative, topLevel bool) *cssList {
	list := &cssList{}
	for {
		list.items = append(list.items, c.parseComplex(relative, topLevel))
		c.skipSpace()
		if c.peek(0) != ',' {
			return list
		}
		c.p++
	}
}

// complex: [combinator] compound (combinator compound)*
func (c *cssCompiler) parseComplex(relative, topLevel bool) *cssComplex {
	c.skipSpace()
	cx := &cssComplex{}
	if relative || topLevel {
		if comb := c.readCombinator(); comb != combNone {
			if topLevel && comb != combChild {
				c.fail("a selector can start only with '>' (the children of the tag it is called on)")
			}
			cx.leading = comb
		} else if relative {
			cx.leading = combDescendant
		}
	}

	cx.compounds = append(cx.compounds, c.parseCompound())
	for {
		ws := c.skipSpace()
		ch := c.peek(0)
		if ch == 0 || ch == ',' || ch == ')' {
			return cx
		}
		comb := c.readCombinator()
		if comb == combNone {
			if !ws {
				c.fail(fmt.Sprintf("unexpected '%c'", ch))
			}
			comb = combDescendant
		}
		cx.combinators = append(cx.combinators, comb)
		cx.compounds = append(cx.compounds, c.parseCompound())
	}
}

func (c *cssCompiler) readCombinator() cssCombinator {
	var comb cssCombinator
	switch c.peek(0) {
	case '>':
		comb = combChild
	case '+':
		comb = combAdjacent
	case '~':
		comb = combSibling
	default:
		return combNone
	}
	c.p++
	c.skipSpace()
	return comb
}

// compound: [type | '*'] (#id | .class | [attribute] | :pseudo)*
func (c *cssCompiler) parseCompound() *cssCompound {
	cp := &cssCompound{key: -1}
	hasType := false
	if c.peek(0) == '*' {
		c.p++
		hasType = true
	} else if c.isIdentStart(c.p) {
		name := strings.ToLower(c.readName())
		hasType = true
		if t, ok := lookupTag(name); ok {
			cp.key = int16(t)
		} else {
			cp.name = name
		}
	}
	if c.peek(0) == '|' {
		c.fail(`namespaces are not supported; escape the colon of a prefixed name: dc\:creator`)
	}

loop:
	for {
		switch c.peek(0) {
		case '#':
			c.p++
			if !c.isNameChar(c.p) {
				c.fail("an id expected after '#'")
			}
			cp.simples = append(cp.simples, cssID{c.readName()})
		case '.':
			c.p++
			if !c.isIdentStart(c.p) {
				c.fail("a class name expected after '.'")
			}
			cp.simples = append(cp.simples, cssClass{c.readName()})
		case '[':
			cp.simples = append(cp.simples, c.parseAttribute())
		case ':':
			c.parsePseudo(cp)
		default:
			break loop
		}
	}

	if !hasType && len(cp.simples) == 0 {
		if c.p >= len(c.s) {
			c.fail("a selector expected")
		}
		c.fail(fmt.Sprintf("unexpected '%c'", c.s[c.p]))
	}
	return cp
}

// '[' name [op value [i|s]] ']'
func (c *cssCompiler) parseAttribute() cssSimple {
	c.p++ // '['
	c.skipSpace()
	if !c.isIdentStart(c.p) {
		c.fail("an attribute name expected")
	}
	name := c.readName()
	c.skipSpace()
	if c.peek(0) == ']' {
		c.p++
		return cssAttribute{name: name, op: opExists}
	}

	var op attrOp
	switch ch := c.peek(0); ch {
	case '=':
		op = opEquals
		c.p++
	case '~', '|', '^', '$', '*':
		op = map[byte]attrOp{'~': opIncludes, '|': opDash, '^': opPrefix, '$': opSuffix, '*': opSubstring}[ch]
		if c.peek(1) != '=' {
			c.fail(fmt.Sprintf("'=' expected after '%c'", ch))
		}
		c.p += 2
	default:
		c.fail("'=', '~=', '|=', '^=', '$=', '*=' or ']' expected")
	}

	c.skipSpace()
	var value string
	switch {
	case c.peek(0) == '"' || c.peek(0) == '\'':
		value = c.readString()
	case c.isIdentStart(c.p):
		value = c.readName()
	default:
		c.fail("an attribute value expected (quote it if it is not a plain word)")
	}

	c.skipSpace()
	fold := caseInsensitiveValues[strings.ToLower(name)]
	if c.isIdentStart(c.p) {
		switch flag := c.readName(); {
		case strings.EqualFold(flag, "i"):
			fold = true
		case strings.EqualFold(flag, "s"):
			fold = false
		default:
			c.fail(fmt.Sprintf("unknown attribute flag '%s'", flag))
		}
		c.skipSpace()
	}
	c.expect(']')
	return cssAttribute{name: name, op: op, value: value, fold: fold}
}

func (c *cssCompiler) parsePseudo(cp *cssCompound) {
	c.p++ // ':'
	if c.peek(0) == ':' {
		c.fail("pseudo-elements (::before, ::text) select no tags")
	}
	if !c.isIdentStart(c.p) {
		c.fail("a pseudo-class name expected after ':'")
	}
	start := c.p
	name := strings.ToLower(c.readName())
	add := func(s ...cssSimple) { cp.simples = append(cp.simples, s...) }

	if c.peek(0) == '(' {
		c.p++
		c.skipSpace()
		switch name {
		case "not":
			add(cssNot{c.parseList(false, false)})
		case "is", "where", "matches", "any":
			add(cssIs{c.parseList(false, false)})
		case "has":
			add(cssHas{c.parseList(true, false)})
		case "nth-child", "nth-last-child", "nth-of-type", "nth-last-of-type":
			a, b := c.parseNth()
			ofType := strings.HasSuffix(name, "of-type")
			var of *cssList
			c.skipSpace()
			if !ofType && c.isWord("of") {
				c.p += 2
				of = c.parseList(false, false)
			}
			add(cssNth{a: a, b: b, fromEnd: strings.Contains(name, "last"), ofType: ofType, of: of})
		case "contains":
			var text string
			switch {
			case c.peek(0) == '"' || c.peek(0) == '\'':
				text = c.readString()
			case c.isIdentStart(c.p):
				text = c.readName()
			default:
				c.fail("a string expected in :contains()")
			}
			add(cssContains{text})
		default:
			c.p = start
			c.fail(fmt.Sprintf("unknown pseudo-class ':%s()'", name))
		}
		c.skipSpace()
		c.expect(')')
		return
	}

	switch name {
	case "first-child":
		add(cssNth{b: 1})
	case "last-child":
		add(cssNth{b: 1, fromEnd: true})
	case "only-child":
		add(cssNth{b: 1}, cssNth{b: 1, fromEnd: true})
	case "first-of-type":
		add(cssNth{b: 1, ofType: true})
	case "last-of-type":
		add(cssNth{b: 1, fromEnd: true, ofType: true})
	case "only-of-type":
		add(cssNth{b: 1, ofType: true}, cssNth{b: 1, fromEnd: true, ofType: true})
	case "root":
		add(cssPseudo{pseudoRoot})
	case "scope":
		add(cssPseudo{pseudoScope})
	case "empty":
		add(cssPseudo{pseudoEmpty})
	case "link", "any-link":
		add(cssPseudo{pseudoLink})
	case "checked":
		add(cssPseudo{pseudoChecked})
	case "disabled":
		add(cssPseudo{pseudoDisabled})
	case "enabled":
		add(cssPseudo{pseudoEnabled})
	case "required":
		add(cssPseudo{pseudoRequired})
	case "optional":
		add(cssPseudo{pseudoOptional})
	default:
		c.p = start
		c.fail(fmt.Sprintf("unknown pseudo-class ':%s'", name))
	}
}

// An+B: odd, even, 3, n, -n+3, 2n+1, +2n - 1
func (c *cssCompiler) parseNth() (int, int) {
	if c.isWord("odd") {
		c.p += 3
		return 2, 1
	}
	if c.isWord("even") {
		c.p += 4
		return 2, 0
	}
	start := c.p
	var text strings.Builder
	for c.p < len(c.s) {
		ch := c.s[c.p]
		if !(ch == '+' || ch == '-' || ch == 'n' || ch == 'N' || ch >= '0' && ch <= '9' || isCSSSpace(ch)) {
			break
		}
		if ch == 'N' {
			ch = 'n'
		}
		if !isCSSSpace(ch) {
			text.WriteByte(ch)
		}
		c.p++
	}
	t := text.String()
	if n := strings.IndexByte(t, 'n'); n < 0 {
		if v, ok := parseInt(t); ok {
			return 0, v
		}
	} else if strings.IndexByte(t[n+1:], 'n') < 0 {
		aText, bText := t[:n], t[n+1:]
		a, aOK := 0, true
		switch aText {
		case "", "+":
			a = 1
		case "-":
			a = -1
		default:
			a, aOK = parseInt(aText)
		}
		b, bOK := 0, true
		if bText != "" {
			bOK = (bText[0] == '+' || bText[0] == '-') && len(bText) > 1 && bText[1] >= '0' && bText[1] <= '9'
			if bOK {
				b, bOK = parseInt(bText)
			}
		}
		if aOK && bOK {
			return a, b
		}
	}
	c.p = start
	c.fail("An+B expected (odd, even, 3, 2n+1, -n+3)")
	return 0, 0
}

func parseInt(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseInt(s, 10, 32)
	return int(v), err == nil
}

// isWord: the identifier at the position is word (case-insensitive) and ends there.
func (c *cssCompiler) isWord(word string) bool {
	return c.p+len(word) <= len(c.s) && strings.EqualFold(c.s[c.p:c.p+len(word)], word) && !c.isNameChar(c.p+len(word))
}

func isNameStartByte(ch byte) bool {
	return ch|0x20 >= 'a' && ch|0x20 <= 'z' || ch == '_' || ch >= 0x80
}

func (c *cssCompiler) isEscape(p int) bool {
	return p < len(c.s) && c.s[p] == '\\' && (p+1 >= len(c.s) || c.s[p+1] != '\n' && c.s[p+1] != '\r' && c.s[p+1] != '\f')
}

func (c *cssCompiler) isNameChar(p int) bool {
	if p >= len(c.s) {
		return false
	}
	ch := c.s[p]
	return isNameStartByte(ch) || ch >= '0' && ch <= '9' || ch == '-' || c.isEscape(p)
}

func (c *cssCompiler) isIdentStart(p int) bool {
	if p >= len(c.s) {
		return false
	}
	if c.s[p] == '-' {
		p++
		return p < len(c.s) && (isNameStartByte(c.s[p]) || c.s[p] == '-' || c.isEscape(p))
	}
	return isNameStartByte(c.s[p]) || c.isEscape(p)
}

func (c *cssCompiler) readName() string {
	var b strings.Builder
	for c.isNameChar(c.p) {
		if c.s[c.p] == '\\' {
			c.readEscape(&b)
		} else {
			b.WriteByte(c.s[c.p])
			c.p++
		}
	}
	return b.String()
}

func (c *cssCompiler) readString() string {
	quote := c.s[c.p]
	start := c.p
	c.p++
	var b strings.Builder
	for {
		if c.p >= len(c.s) {
			c.p = start
			c.fail("unterminated string")
		}
		ch := c.s[c.p]
		switch {
		case ch == quote:
			c.p++
			return b.String()
		case ch == '\\':
			switch {
			case c.p+1 < len(c.s) && (c.s[c.p+1] == '\n' || c.s[c.p+1] == '\f'):
				c.p += 2 // an escaped line break continues the string
			case c.p+2 < len(c.s) && c.s[c.p+1] == '\r':
				if c.s[c.p+2] == '\n' {
					c.p += 3
				} else {
					c.p += 2
				}
			default:
				c.readEscape(&b)
			}
		default:
			b.WriteByte(ch)
			c.p++
		}
	}
}

func isHex(ch byte) bool { return ch >= '0' && ch <= '9' || ch|0x20 >= 'a' && ch|0x20 <= 'f' }

// readEscape: '\' then up to 6 hex digits and one optional whitespace, or any other char as itself.
func (c *cssCompiler) readEscape(b *strings.Builder) {
	c.p++ // '\'
	if c.p >= len(c.s) {
		b.WriteRune(utf8.RuneError)
		return
	}
	if !isHex(c.s[c.p]) {
		_, size := utf8.DecodeRuneInString(c.s[c.p:])
		b.WriteString(c.s[c.p : c.p+size])
		c.p += size
		return
	}
	code, digits := 0, 0
	for digits < 6 && c.p < len(c.s) && isHex(c.s[c.p]) {
		v, _ := strconv.ParseUint(c.s[c.p:c.p+1], 16, 8)
		code = code*16 + int(v)
		c.p++
		digits++
	}
	if c.p < len(c.s) && isCSSSpace(c.s[c.p]) {
		if c.s[c.p] == '\r' && c.peek(1) == '\n' {
			c.p += 2
		} else {
			c.p++
		}
	}
	if code == 0 || code > 0x10FFFF || code >= 0xD800 && code <= 0xDFFF {
		b.WriteRune(utf8.RuneError)
	} else {
		b.WriteRune(rune(code))
	}
}
