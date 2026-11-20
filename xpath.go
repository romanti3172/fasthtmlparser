package fasthtmlparser

import (
	"bytes"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf16"
)

// XPathQuery is a compiled XPath 1.0 expression. Compile once and reuse it on any number of documents,
// from any number of goroutines; the string methods (Node.XPath, Document.XPathValue, ...) compile and cache
// expressions automatically.
//
// All axes (except namespace), predicates, '|', the operators and the XPath 1.0 functions are supported,
// plus ends-with(), lower-case() and upper-case(). HTML rules apply: names are case-insensitive, the string-value
// of an element is its visible text (as Node.Text), attribute values and text are decoded. Variables are not supported.
//
// On a node, relative paths start at the node; '/' and '//' search the whole document.
type XPathQuery struct {
	expression string
	expr       xexpr
}

// CompileXPath compiles an XPath 1.0 expression.
func CompileXPath(expression string) (q *XPathQuery, err error) {
	defer func() {
		if r := recover(); r != nil {
			e, ok := r.(xpathError)
			if !ok {
				panic(r)
			}
			q, err = nil, e
		}
	}()
	c := newXPathCompiler(expression)
	expr := c.parseExpr()
	if c.peek(0).kind != tEnd {
		c.fail("unexpected " + c.describe(c.peek(0)))
	}
	return &XPathQuery{expression: expression, expr: expr}, nil
}

// MustCompileXPath is CompileXPath that panics on an invalid expression.
func MustCompileXPath(expression string) *XPathQuery {
	q, err := CompileXPath(expression)
	if err != nil {
		panic(err)
	}
	return q
}

// String returns the source of the expression.
func (q *XPathQuery) String() string { return q.expression }

var xpathCache sync.Map
var xpathCacheSize atomic.Int32

func cachedXPath(expression string) *XPathQuery {
	if q, ok := xpathCache.Load(expression); ok {
		return q.(*XPathQuery)
	}
	q := MustCompileXPath(expression)
	if xpathCacheSize.Add(1) > 1024 { // dynamically built expressions must not grow the cache forever
		xpathCache.Clear()
		xpathCacheSize.Store(0)
	}
	xpathCache.Store(expression, q)
	return q
}

// Nodes returns the selected tags (elements and comments) in document order, evaluated on the document.
// It panics when the expression does not give a node-set (use Value).
func (q *XPathQuery) Nodes(d *Document) []*Node {
	if d == nil {
		return nil
	}
	return q.nodes(rootNode(d))
}

// NodesFrom is Nodes evaluated on a tag: relative paths start at it.
func (q *XPathQuery) NodesFrom(n *Node) []*Node {
	if n == nil {
		return nil
	}
	return q.nodes(tagNode(n))
}

// Values returns the string-values of the selected nodes, or the result of an expression that gives a
// number, string or boolean (count(//a), concat(...)) as one value.
func (q *XPathQuery) Values(d *Document) []string {
	if d == nil {
		return nil
	}
	return q.values(rootNode(d))
}

// ValuesFrom is Values evaluated on a tag.
func (q *XPathQuery) ValuesFrom(n *Node) []string {
	if n == nil {
		return nil
	}
	return q.values(tagNode(n))
}

// Value returns the string-value of the first selected node, or the result of the expression;
// false when it selects no node.
func (q *XPathQuery) Value(d *Document) (string, bool) {
	if d == nil {
		return "", false
	}
	return q.value(rootNode(d))
}

// ValueFrom is Value evaluated on a tag.
func (q *XPathQuery) ValueFrom(n *Node) (string, bool) {
	if n == nil {
		return "", false
	}
	return q.value(tagNode(n))
}

func (q *XPathQuery) eval(context xnode) any {
	return q.expr.eval(&xctx{node: context, position: 1, size: 1, state: &evalState{doc: context.doc}})
}

func (q *XPathQuery) nodes(context xnode) []*Node {
	if q.expr.typ() != xNodeSet {
		panic(fmt.Sprintf("fasthtmlparser: the XPath %q gives a %s, not tags; use XPathValue", q.expression, q.expr.typ()))
	}
	set := q.eval(context).(nodeSet)
	var tags []*Node
	for _, n := range set {
		if n.kind == xElement || n.kind == xComment {
			tags = append(tags, n.tag)
		}
	}
	return tags
}

func (q *XPathQuery) values(context xnode) []string {
	result := q.eval(context)
	set, ok := result.(nodeSet)
	if !ok {
		return []string{toString(result)}
	}
	values := make([]string, len(set))
	for i := range set {
		values[i] = stringValue(&set[i])
	}
	return values
}

func (q *XPathQuery) value(context xnode) (string, bool) {
	result := q.eval(context)
	if set, ok := result.(nodeSet); ok {
		if len(set) == 0 {
			return "", false
		}
		return stringValue(&set[0]), true
	}
	return toString(result), true
}

// XPath returns the tags the expression selects. It panics on an invalid expression or one that gives no node-set.
func (d *Document) XPath(expression string) []*Node { return cachedXPath(expression).Nodes(d) }

// XPathFirst returns the first tag the expression selects, or nil.
func (d *Document) XPathFirst(expression string) *Node { return first(d.XPath(expression)) }

// XPathValues returns the values the expression selects: decoded attributes, text(), visible text of tags,
// or the result of count(), sum(), string(), ...
func (d *Document) XPathValues(expression string) []string {
	return cachedXPath(expression).Values(d)
}

// XPathValue returns the first value the expression selects, or "".
func (d *Document) XPathValue(expression string) string {
	v, _ := cachedXPath(expression).Value(d)
	return v
}

// XPath returns the tags the expression selects, relative paths starting at n.
func (n *Node) XPath(expression string) []*Node { return cachedXPath(expression).NodesFrom(n) }

// XPathFirst returns the first tag the expression selects from n, or nil.
func (n *Node) XPathFirst(expression string) *Node { return first(n.XPath(expression)) }

// XPathValues returns the values the expression selects from n.
func (n *Node) XPathValues(expression string) []string {
	return cachedXPath(expression).ValuesFrom(n)
}

// XPathValue returns the first value the expression selects from n, or "".
func (n *Node) XPathValue(expression string) string {
	v, _ := cachedXPath(expression).ValueFrom(n)
	return v
}

// ----- the data model over the flat tag list -----

type xkind uint8

const (
	xRoot xkind = iota
	xElement
	xComment
	xText
	xAttribute
)

// xnode is a node of the XPath data model: the document root, a tag (element or comment),
// a text node (a run of source text between tags) or an attribute (positions in the opening tag).
type xnode struct {
	kind xkind
	doc  *Document
	// element / comment: the tag; text / attribute: the parent element (nil: the root)
	tag *Node
	// text: first byte and length; attribute: name start and length
	start, length int
	// attribute: value start (-1 for no value) and length
	valueStart, valueLength int
}

type nodeSet []xnode

func rootNode(d *Document) xnode { return xnode{kind: xRoot, doc: d} }

func tagNode(n *Node) xnode {
	kind := xElement
	if n.key == int16(TagComment) {
		kind = xComment
	}
	return xnode{kind: kind, doc: n.doc, tag: n}
}

// key gives document order and identity: tags and text nodes by their first byte; attributes right after
// their element, before its children.
func (x *xnode) key() int64 {
	switch x.kind {
	case xRoot:
		return -1
	case xElement, xComment:
		return int64(x.tag.start) << 32
	case xAttribute:
		return int64(x.tag.start)<<32 | int64(uint32(x.start))
	}
	return int64(x.start) << 32
}

// rangeStart and rangeEnd: the source positions the node covers (for following / preceding).
func (x *xnode) rangeStart() int {
	switch x.kind {
	case xRoot:
		return -1
	case xText:
		return x.start
	}
	return int(x.tag.start)
}

func (x *xnode) rangeEnd() int {
	switch x.kind {
	case xRoot:
		return math.MaxInt32
	case xElement, xComment:
		return int(x.tag.end)
	case xText:
		return x.start + x.length - 1
	}
	return int(x.tag.start) // attribute: the children of its element follow it
}

func (x *xnode) attrName() string { return x.doc.source[x.start : x.start+x.length] }

// ----- node tests -----

type testKind uint8

const (
	testName      testKind = iota
	testPrefix             // dc:*
	testWildcard           // *
	testNode               // node()
	testText               // text()
	testComment            // comment()
	testPI                 // processing-instruction(): never matches
	testContainer          // internal: the root and elements (// before a child or attribute step)
)

type nodeTest struct {
	kind  testKind
	name  string
	query Query
}

var (
	anyNodeTest    = &nodeTest{kind: testNode}
	containersTest = &nodeTest{kind: testContainer}
)

func newNodeTest(kind testKind, name string) *nodeTest {
	t := &nodeTest{kind: kind, name: name}
	if kind == testName {
		t.query = ByName(name)
	}
	return t
}

func (t *nodeTest) matchesText() bool { return t.kind == testNode || t.kind == testText }

func (t *nodeTest) matchesTags() bool {
	switch t.kind {
	case testName, testPrefix, testWildcard, testNode, testComment, testContainer:
		return true
	}
	return false
}

func (t *nodeTest) matchesTag(n *Node) bool {
	switch t.kind {
	case testName:
		return t.query.Matches(n)
	case testPrefix:
		name := n.Name()
		return isElement(n) && len(name) >= len(t.name) && strings.EqualFold(name[:len(t.name)], t.name)
	case testWildcard, testContainer:
		return isElement(n)
	case testNode:
		return isElement(n) || n.key == int16(TagComment)
	case testComment:
		return n.key == int16(TagComment)
	}
	return false
}

func (t *nodeTest) matchesAttribute(name string) bool {
	switch t.kind {
	case testName:
		return strings.EqualFold(name, t.name)
	case testPrefix:
		return len(name) >= len(t.name) && strings.EqualFold(name[:len(t.name)], t.name)
	case testWildcard, testNode:
		return true
	}
	return false
}

func (t *nodeTest) matches(x *xnode, attributeAxis bool) bool {
	switch x.kind {
	case xRoot:
		return t.kind == testNode || t.kind == testContainer
	case xElement, xComment:
		return t.matchesTag(x.tag)
	case xText:
		return t.matchesText()
	}
	if attributeAxis {
		return t.matchesAttribute(x.attrName())
	}
	return t.kind == testNode
}

// ----- navigation -----

// content: the tags (nodes[first:], up to end, at childDepth) and the text range of a container.
func content(x *xnode) (first int, end int32, childDepth int32, textStart, textEnd int, ok bool) {
	switch x.kind {
	case xRoot:
		return 0, math.MaxInt32, 0, 0, len(x.doc.source), true
	case xElement:
		if t := x.tag; t.innerStart > 0 && t.innerEnd > 0 {
			return int(t.index) + 1, t.end, int32(t.depth) + 1, int(t.innerStart), int(t.innerEnd) + 1, true
		}
	}
	return 0, 0, 0, 0, 0, false
}

// addChildren appends the children of the root or an element that match test, text nodes included, in document order.
func addChildren(x *xnode, test *nodeTest, out nodeSet) nodeSet {
	wantText, wantTags := test.matchesText(), test.matchesTags()
	if !wantText && !wantTags {
		return out
	}
	first, end, childDepth, p, textEnd, ok := content(x)
	if !ok {
		return out
	}
	nodes := x.doc.nodes
	var parent *Node
	if x.kind == xElement {
		parent = x.tag
	}
	for i := first; i < len(nodes); i++ {
		t := &nodes[i]
		if t.end > end {
			break
		}
		if int32(t.depth) != childDepth {
			continue
		}
		if wantText && int(t.start) > p {
			out = addText(x.doc, parent, p, min(int(t.start), textEnd), out)
		}
		if wantTags && test.matchesTag(t) {
			out = append(out, tagNode(t))
		}
		p = int(t.end) + 1
	}
	if wantText && textEnd > p {
		out = addText(x.doc, parent, p, textEnd, out)
	}
	return out
}

func addText(d *Document, parent *Node, start, end int, out nodeSet) nodeSet {
	if end > start {
		out = append(out, xnode{kind: xText, doc: d, tag: parent, start: start, length: end - start})
	}
	return out
}

// addDescendants appends the descendants of the root or an element that match test, in document order.
func addDescendants(x *xnode, test *nodeTest, out nodeSet) nodeSet {
	if test.matchesText() {
		return addDescendantsWithText(x, test, out)
	}
	if !test.matchesTags() {
		return out
	}
	first, end, _, _, _, ok := content(x)
	if !ok {
		return out
	}
	// tags only: one linear scan of the flat list
	nodes := x.doc.nodes
	for i := first; i < len(nodes); i++ {
		t := &nodes[i]
		if t.end > end {
			break
		}
		if test.matchesTag(t) {
			out = append(out, tagNode(t))
		}
	}
	return out
}

func addDescendantsWithText(x *xnode, test *nodeTest, out nodeSet) nodeSet {
	children := addChildren(x, anyNodeTest, nil)
	for i := range children {
		child := &children[i]
		if test.matches(child, false) {
			out = append(out, *child)
		}
		if child.kind == xElement {
			out = addDescendantsWithText(child, test, out)
		}
	}
	return out
}

func parentNode(x *xnode) (xnode, bool) {
	switch x.kind {
	case xRoot:
		return xnode{}, false
	case xText, xAttribute:
		if x.tag != nil {
			return tagNode(x.tag), true
		}
		return rootNode(x.doc), true
	}
	if x.tag.depth > 0 && x.tag.parent >= 0 {
		return tagNode(&x.doc.nodes[x.tag.parent]), true
	}
	return rootNode(x.doc), true
}

func addAttributes(x *xnode, test *nodeTest, out nodeSet) nodeSet {
	if x.kind != xElement {
		return out
	}
	s := attributeScanner(x.tag)
	for {
		ns, nl, vs, vl, ok := s.next()
		if !ok {
			return out
		}
		if test.matchesAttribute(s.src[ns : ns+nl]) {
			out = append(out, xnode{kind: xAttribute, doc: x.doc, tag: x.tag, start: ns, length: nl, valueStart: vs, valueLength: vl})
		}
	}
}

// attributeOf returns the raw value of the first attribute with the given name; false when the element has none.
func attributeOf(x *xnode, name string) (string, bool) {
	if x.kind != xElement {
		return "", false
	}
	s := attributeScanner(x.tag)
	for {
		ns, nl, vs, vl, ok := s.next()
		if !ok {
			return "", false
		}
		if strings.EqualFold(s.src[ns:ns+nl], name) {
			if vs < 0 {
				return "", true
			}
			return s.src[vs : vs+vl], true
		}
	}
}

// stringValue: the root and elements give their visible text (as Text()); text nodes and attributes their
// decoded value; comments their content.
func stringValue(x *xnode) string {
	switch x.kind {
	case xRoot:
		return clearTags(x.doc.source)
	case xElement:
		return x.tag.Text()
	case xAttribute:
		if x.valueStart < 0 {
			return ""
		}
		return decodeHTML(x.doc.source[x.valueStart:x.valueStart+x.valueLength], true)
	case xText:
		text := x.doc.source[x.start : x.start+x.length]
		if x.tag != nil && x.tag.key >= 0 && rawTextEnds[x.tag.key] != "" {
			return normalizeNewlines(text)
		}
		return decodeHTML(text, false)
	}
	// comment
	t := x.tag
	source := t.doc.source[t.start : t.end+1]
	source = strings.TrimPrefix(source, "<!--")
	source = strings.TrimSuffix(source, "-->")
	return source
}

// nodeName for name() / local-name(): lower-case tag or attribute name; "" for other nodes.
func nodeName(x *xnode) string {
	switch x.kind {
	case xElement:
		return x.tag.Name()
	case xAttribute:
		return strings.ToLower(x.attrName())
	}
	return ""
}

// ----- values -----

type xtype uint8

const (
	xNodeSet xtype = iota
	xNumber
	xString
	xBoolean
)

func (t xtype) String() string {
	return [...]string{"node-set", "number", "string", "boolean"}[t]
}

func toBoolean(v any) bool {
	switch v := v.(type) {
	case bool:
		return v
	case float64:
		return v != 0 && !math.IsNaN(v)
	case string:
		return v != ""
	case nodeSet:
		return len(v) > 0
	}
	return false
}

func toNumber(v any) float64 {
	switch v := v.(type) {
	case float64:
		return v
	case bool:
		if v {
			return 1
		}
		return 0
	case string:
		return stringToNumber(v)
	case nodeSet:
		if len(v) == 0 {
			return math.NaN()
		}
		return stringToNumber(stringValue(&v[0]))
	}
	return math.NaN()
}

// stringToNumber: optional '-', digits with an optional decimal point; anything else is NaN.
func stringToNumber(value string) float64 {
	s := strings.TrimSpace(value)
	body := strings.TrimPrefix(s, "-")
	digits, point := 0, 0
	for i := 0; i < len(body); i++ {
		switch c := body[i]; {
		case c >= '0' && c <= '9':
			digits++
		case c == '.':
			point++
		default:
			return math.NaN()
		}
	}
	if digits == 0 || point > 1 {
		return math.NaN()
	}
	d, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return math.NaN()
	}
	return d
}

func toString(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case bool:
		if v {
			return "true"
		}
		return "false"
	case float64:
		return numberToString(v)
	case nodeSet:
		if len(v) == 0 {
			return ""
		}
		return stringValue(&v[0])
	}
	return ""
}

// numberToString: the shortest round-trip digits, written without an exponent (XPath has none).
func numberToString(d float64) string {
	switch {
	case math.IsNaN(d):
		return "NaN"
	case math.IsInf(d, 1):
		return "Infinity"
	case math.IsInf(d, -1):
		return "-Infinity"
	case d == 0:
		return "0"
	}
	return strconv.FormatFloat(d, 'f', -1, 64)
}

// sortUnique puts the nodes in document order without duplicates.
func sortUnique(nodes nodeSet) nodeSet {
	if len(nodes) < 2 {
		return nodes
	}
	sorted := true
	for i := 1; i < len(nodes); i++ {
		if nodes[i-1].key() >= nodes[i].key() {
			sorted = false
			break
		}
	}
	if sorted {
		return nodes
	}
	slices.SortStableFunc(nodes, func(a, b xnode) int {
		ka, kb := a.key(), b.key()
		switch {
		case ka < kb:
			return -1
		case ka > kb:
			return 1
		}
		return 0
	})
	w := 1
	for i := 1; i < len(nodes); i++ {
		if nodes[i].key() != nodes[w-1].key() {
			nodes[w] = nodes[i]
			w++
		}
	}
	return nodes[:w]
}

// ----- evaluation -----

type evalState struct {
	doc *Document
	all nodeSet // every element, comment and text node in document order (for following / preceding with text())
}

func (s *evalState) allNodes() nodeSet {
	if s.all == nil {
		root := rootNode(s.doc)
		s.all = addDescendants(&root, anyNodeTest, nodeSet{})
	}
	return s.all
}

type xctx struct {
	node     xnode
	position int
	size     int
	state    *evalState
}

type xexpr interface {
	typ() xtype
	// usesPosition: whether position() or last() is used in the current context (not inside nested predicates)
	usesPosition() bool
	eval(c *xctx) any
	evalBool(c *xctx) bool
}

func evalNodes(e xexpr, c *xctx) nodeSet {
	if set, ok := e.eval(c).(nodeSet); ok {
		return set
	}
	panic("fasthtmlparser: the XPath expression does not give a node-set")
}

func evalString(e xexpr, c *xctx) string  { return toString(e.eval(c)) }
func evalNumber(e xexpr, c *xctx) float64 { return toNumber(e.eval(c)) }

// evalPredicate: a number compares with the position, anything else is converted to boolean.
func evalPredicate(e xexpr, c *xctx) bool {
	if e.typ() == xBoolean {
		return e.evalBool(c)
	}
	v := e.eval(c)
	if d, ok := v.(float64); ok {
		return d == float64(c.position)
	}
	return toBoolean(v)
}

// isPositional: whether the predicate depends on the position of the node (a number, position(), last()).
func isPositional(e xexpr) bool { return e.typ() == xNumber || e.usesPosition() }

type baseExpr struct{}

func (baseExpr) usesPosition() bool { return false }

type literalExpr struct {
	baseExpr
	value string
}

func (e *literalExpr) typ() xtype            { return xString }
func (e *literalExpr) eval(*xctx) any        { return e.value }
func (e *literalExpr) evalBool(c *xctx) bool { return e.value != "" }

type numberExpr struct {
	baseExpr
	value float64
}

func (e *numberExpr) typ() xtype            { return xNumber }
func (e *numberExpr) eval(*xctx) any        { return e.value }
func (e *numberExpr) evalBool(c *xctx) bool { return toBoolean(e.value) }

type logicExpr struct {
	and         bool
	left, right xexpr
}

func (e *logicExpr) typ() xtype         { return xBoolean }
func (e *logicExpr) usesPosition() bool { return e.left.usesPosition() || e.right.usesPosition() }
func (e *logicExpr) eval(c *xctx) any   { return e.evalBool(c) }
func (e *logicExpr) evalBool(c *xctx) bool {
	if e.and {
		return e.left.evalBool(c) && e.right.evalBool(c)
	}
	return e.left.evalBool(c) || e.right.evalBool(c)
}

type compareOp uint8

const (
	opEq compareOp = iota
	opNeq
	opLt
	opLe
	opGt
	opGe
)

type compareExpr struct {
	op          compareOp
	left, right xexpr
}

func (e *compareExpr) typ() xtype            { return xBoolean }
func (e *compareExpr) usesPosition() bool    { return e.left.usesPosition() || e.right.usesPosition() }
func (e *compareExpr) eval(c *xctx) any      { return e.evalBool(c) }
func (e *compareExpr) evalBool(c *xctx) bool { return compare(e.op, e.left.eval(c), e.right.eval(c)) }

func compare(op compareOp, a, b any) bool {
	if setA, ok := a.(nodeSet); ok {
		if setB, ok := b.(nodeSet); ok {
			if len(setA) == 0 || len(setB) == 0 {
				return false
			}
			valuesB := make([]string, len(setB))
			for i := range setB {
				valuesB[i] = stringValue(&setB[i])
			}
			for i := range setA {
				value := stringValue(&setA[i])
				for _, other := range valuesB {
					if compareAtoms(op, value, other) {
						return true
					}
				}
			}
			return false
		}
		return compareWithNodes(op, setA, b, true)
	}
	if setB, ok := b.(nodeSet); ok {
		return compareWithNodes(op, setB, a, false)
	}
	return compareAtoms(op, a, b)
}

func compareWithNodes(op compareOp, nodes nodeSet, other any, nodesOnLeft bool) bool {
	if _, ok := other.(bool); ok {
		asBool := len(nodes) > 0
		if nodesOnLeft {
			return compareAtoms(op, asBool, other)
		}
		return compareAtoms(op, other, asBool)
	}
	_, isNumber := other.(float64)
	for i := range nodes {
		var value any = stringValue(&nodes[i])
		if isNumber {
			value = stringToNumber(value.(string))
		}
		if nodesOnLeft && compareAtoms(op, value, other) || !nodesOnLeft && compareAtoms(op, other, value) {
			return true
		}
	}
	return false
}

func compareAtoms(op compareOp, a, b any) bool {
	if op == opEq || op == opNeq {
		var eq bool
		_, aBool := a.(bool)
		_, bBool := b.(bool)
		_, aNum := a.(float64)
		_, bNum := b.(float64)
		switch {
		case aBool || bBool:
			eq = toBoolean(a) == toBoolean(b)
		case aNum || bNum:
			eq = toNumber(a) == toNumber(b)
		default:
			eq = toString(a) == toString(b)
		}
		return eq == (op == opEq)
	}
	x, y := toNumber(a), toNumber(b)
	switch op {
	case opLt:
		return x < y
	case opLe:
		return x <= y
	case opGt:
		return x > y
	}
	return x >= y
}

type arithOp uint8

const (
	opAdd arithOp = iota
	opSub
	opMul
	opDiv
	opMod
)

type arithExpr struct {
	op          arithOp
	left, right xexpr
}

func (e *arithExpr) typ() xtype            { return xNumber }
func (e *arithExpr) usesPosition() bool    { return e.left.usesPosition() || e.right.usesPosition() }
func (e *arithExpr) evalBool(c *xctx) bool { return toBoolean(e.eval(c)) }
func (e *arithExpr) eval(c *xctx) any {
	x, y := evalNumber(e.left, c), evalNumber(e.right, c)
	switch e.op {
	case opAdd:
		return x + y
	case opSub:
		return x - y
	case opMul:
		return x * y
	case opDiv:
		return x / y
	}
	return math.Mod(x, y)
}

type negateExpr struct{ operand xexpr }

func (e *negateExpr) typ() xtype            { return xNumber }
func (e *negateExpr) usesPosition() bool    { return e.operand.usesPosition() }
func (e *negateExpr) eval(c *xctx) any      { return -evalNumber(e.operand, c) }
func (e *negateExpr) evalBool(c *xctx) bool { return toBoolean(e.eval(c)) }

type unionExpr struct{ left, right xexpr }

func (e *unionExpr) typ() xtype            { return xNodeSet }
func (e *unionExpr) usesPosition() bool    { return e.left.usesPosition() || e.right.usesPosition() }
func (e *unionExpr) evalBool(c *xctx) bool { return toBoolean(e.eval(c)) }
func (e *unionExpr) eval(c *xctx) any {
	result := append(nodeSet{}, evalNodes(e.left, c)...)
	result = append(result, evalNodes(e.right, c)...)
	return sortUnique(result)
}

// attributeEqualsExpr: @name = 'literal', compared in place without building a node-set.
type attributeEqualsExpr struct {
	baseExpr
	name, value string
	negate      bool
}

func (e *attributeEqualsExpr) typ() xtype       { return xBoolean }
func (e *attributeEqualsExpr) eval(c *xctx) any { return e.evalBool(c) }
func (e *attributeEqualsExpr) evalBool(c *xctx) bool {
	// '!=' is true when the attribute exists and differs (a missing attribute is an empty node-set)
	raw, ok := attributeOf(&c.node, e.name)
	if !ok {
		return false
	}
	return attrEquals(raw, e.value) != e.negate
}

// attributeTestExpr: contains/starts-with/ends-with(concat('prefix', @name, 'suffix'), 'needle') (prefix and suffix
// may be empty), tested on the attribute value in place, without a node-set and strings for every element.
type attributeTestExpr struct {
	baseExpr
	fn                           fn
	prefix, name, suffix, needle string
	needleBytes                  []byte
}

func (e *attributeTestExpr) typ() xtype       { return xBoolean }
func (e *attributeTestExpr) eval(c *xctx) any { return e.evalBool(c) }
func (e *attributeTestExpr) evalBool(c *xctx) bool {
	raw, _ := attributeOf(&c.node, e.name) // a missing attribute is an empty node-set, whose string-value is ""
	if needsDecoding(raw) {
		raw = decodeHTML(raw, true)
	}
	if e.prefix == "" && e.suffix == "" {
		return testString(e.fn, raw, e.needle)
	}
	var stack [256]byte
	joined := append(append(append(stack[:0], e.prefix...), raw...), e.suffix...)
	return testBytes(e.fn, joined, e.needle, e.needleBytes)
}

func testString(f fn, s, needle string) bool {
	switch f {
	case fnStartsWith:
		return strings.HasPrefix(s, needle)
	case fnEndsWith:
		return strings.HasSuffix(s, needle)
	}
	return strings.Contains(s, needle)
}

func testBytes(f fn, s []byte, needle string, needleBytes []byte) bool {
	switch f {
	case fnStartsWith:
		return len(s) >= len(needle) && string(s[:len(needle)]) == needle
	case fnEndsWith:
		return len(s) >= len(needle) && string(s[len(s)-len(needle):]) == needle
	}
	return bytes.Contains(s, needleBytes)
}

// attributeExistsExpr: [@name].
type attributeExistsExpr struct {
	baseExpr
	name string
}

func (e *attributeExistsExpr) typ() xtype       { return xBoolean }
func (e *attributeExistsExpr) eval(c *xctx) any { return e.evalBool(c) }
func (e *attributeExistsExpr) evalBool(c *xctx) bool {
	_, ok := attributeOf(&c.node, e.name)
	return ok
}

type axis uint8

const (
	axisChild axis = iota
	axisDescendant
	axisDescendantOrSelf
	axisSelf
	axisParent
	axisAncestor
	axisAncestorOrSelf
	axisFollowingSibling
	axisPrecedingSibling
	axisFollowing
	axisPreceding
	axisAttribute
)

type step struct {
	axis       axis
	test       *nodeTest
	predicates []xexpr
}

func (s *step) isReverse() bool {
	return s.axis == axisAncestor || s.axis == axisAncestorOrSelf || s.axis == axisPreceding || s.axis == axisPrecedingSibling
}

func (s *step) hasPositionalPredicates() bool { return slices.ContainsFunc(s.predicates, isPositional) }

func (s *step) apply(input nodeSet, state *evalState) nodeSet {
	if len(input) == 1 {
		return s.selectFrom(&input[0], nodeSet{}, state)
	}

	// Nested input nodes (//div//a on divs inside divs, //a/ancestor::* on links in one list) give the same nodes
	// again and again: n nested nodes would collect about n² nodes before the duplicates are removed. When the
	// predicates do not depend on the position, a node passes them or not whichever input node it comes from,
	// so the input nodes whose results are already collected are skipped: the work stays linear.
	if !s.hasPositionalPredicates() {
		switch s.axis {
		case axisDescendant, axisDescendantOrSelf:
			if !s.test.matchesText() && inDocumentOrder(input) {
				return sortUnique(s.selectDescendantsOfNested(input, state))
			}
		case axisAncestor, axisAncestorOrSelf:
			return sortUnique(s.selectSharedAncestors(input, state))
		}
	}

	result := nodeSet{}
	for i := range input {
		result = s.selectFrom(&input[i], result, state)
	}
	return sortUnique(result)
}

func inDocumentOrder(nodes nodeSet) bool {
	for i := 1; i < len(nodes); i++ {
		if nodes[i-1].key() >= nodes[i].key() {
			return false
		}
	}
	return true
}

// selectDescendantsOfNested: the descendant axes (tags only) on input nodes in document order. A tag that lies in
// the part of the tag list already scanned for an earlier input node is skipped: that scan has tested it and every
// tag its own scan would test.
func (s *step) selectDescendantsOfNested(input nodeSet, state *evalState) nodeSet {
	nodes := state.doc.nodes
	out := nodeSet{}
	scannedTo := -1 // the tags before this index were scanned for an earlier input node
	for i := range input {
		x := &input[i]
		switch x.kind {
		case xElement, xComment:
			t := x.tag
			if int(t.index) < scannedTo {
				continue
			}
			out = s.selectFrom(x, out, state)
			// where addDescendants stops for this tag
			if x.kind == xElement && t.innerStart > 0 && t.innerEnd > 0 {
				k := int(t.index) + 1
				for k < len(nodes) && nodes[k].end <= t.end {
					k++
				}
				scannedTo = max(scannedTo, k)
			}
		default:
			out = s.selectFrom(x, out, state)
			if x.kind == xRoot {
				scannedTo = math.MaxInt
			}
		}
	}
	return out
}

// selectSharedAncestors: the walk up from an input node stops at an ancestor already reached from an earlier one,
// because that walk has gone on to the root. Only the root and elements are marked: they are the only parents.
func (s *step) selectSharedAncestors(input nodeSet, state *evalState) nodeSet {
	visited := make(map[int64]struct{})
	visit := func(x *xnode) bool {
		k := x.key()
		if _, seen := visited[k]; seen {
			return false
		}
		visited[k] = struct{}{}
		return true
	}
	out := nodeSet{}
	for i := range input {
		start := len(out)
		current := input[i]
		if s.axis == axisAncestorOrSelf {
			if (current.kind == xRoot || current.kind == xElement) && !visit(&current) {
				continue
			}
			if s.test.matches(&current, false) {
				out = append(out, current)
			}
		}
		for {
			p, ok := parentNode(&current)
			if !ok || !visit(&p) {
				break
			}
			current = p
			if s.test.matches(&current, false) {
				out = append(out, current)
			}
		}
		if len(s.predicates) > 0 && len(out) > start {
			out = out[:start+len(filter(out[start:], s.predicates, state))]
		}
	}
	return out
}

// selectFrom appends the nodes of the axis that pass the test and the predicates, in document order.
func (s *step) selectFrom(x *xnode, out nodeSet, state *evalState) nodeSet {
	start := len(out)
	out = s.collect(x, out, state)
	if len(s.predicates) > 0 || s.isReverse() {
		selected := filter(out[start:], s.predicates, state)
		if s.isReverse() {
			slices.Reverse(selected)
		}
		out = out[:start+len(selected)]
	}
	return out
}

// collect appends the nodes of the axis that pass the node test, in axis order (reverse axes: nearest first).
func (s *step) collect(x *xnode, out nodeSet, state *evalState) nodeSet {
	test := s.test
	switch s.axis {
	case axisChild:
		return addChildren(x, test, out)
	case axisDescendant:
		return addDescendants(x, test, out)
	case axisDescendantOrSelf:
		if test.matches(x, false) {
			out = append(out, *x)
		}
		return addDescendants(x, test, out)
	case axisSelf:
		if test.matches(x, false) {
			out = append(out, *x)
		}
	case axisParent:
		if p, ok := parentNode(x); ok && test.matches(&p, false) {
			out = append(out, p)
		}
	case axisAncestor, axisAncestorOrSelf:
		current := *x
		if s.axis == axisAncestorOrSelf && test.matches(&current, false) {
			out = append(out, current)
		}
		for {
			p, ok := parentNode(&current)
			if !ok {
				break
			}
			current = p
			if test.matches(&current, false) {
				out = append(out, current)
			}
		}
	case axisFollowingSibling, axisPrecedingSibling:
		return collectSiblings(x, test, out, s.axis == axisFollowingSibling)
	case axisFollowing:
		return collectFollowing(x, test, out, state)
	case axisPreceding:
		return collectPreceding(x, test, out, state)
	case axisAttribute:
		return addAttributes(x, test, out)
	}
	return out
}

func collectSiblings(x *xnode, test *nodeTest, out nodeSet, following bool) nodeSet {
	if x.kind == xRoot || x.kind == xAttribute {
		return out
	}
	if !test.matchesText() && x.kind != xText {
		// tags only: walk the flat list; deeper tags are inside the siblings, a shallower one ends the parent
		t := x.tag
		nodes := t.doc.nodes
		stepBy := 1
		if !following {
			stepBy = -1
		}
		for i := int(t.index) + stepBy; i >= 0 && i < len(nodes); i += stepBy {
			item := &nodes[i]
			if item.depth < t.depth {
				break
			}
			if item.depth == t.depth && test.matchesTag(item) {
				out = append(out, tagNode(item))
			}
		}
		return out
	}

	parent, ok := parentNode(x)
	if !ok {
		return out
	}
	siblings := addChildren(&parent, anyNodeTest, nil)
	key := x.key()
	index := slices.IndexFunc(siblings, func(s xnode) bool { return s.key() == key })
	if index < 0 {
		return out
	}
	if following {
		for i := index + 1; i < len(siblings); i++ {
			if test.matches(&siblings[i], false) {
				out = append(out, siblings[i])
			}
		}
	} else {
		for i := index - 1; i >= 0; i-- {
			if test.matches(&siblings[i], false) {
				out = append(out, siblings[i])
			}
		}
	}
	return out
}

func collectFollowing(x *xnode, test *nodeTest, out nodeSet, state *evalState) nodeSet {
	if x.kind == xRoot {
		return out
	}
	after := x.rangeEnd()
	if test.matchesText() {
		all := state.allNodes()
		for i := range all {
			if all[i].rangeStart() > after && test.matches(&all[i], false) {
				out = append(out, all[i])
			}
		}
		return out
	}
	nodes := state.doc.nodes
	first := 0
	if x.kind == xElement || x.kind == xComment {
		first = int(x.tag.index) + 1
	}
	for i := first; i < len(nodes); i++ {
		if int(nodes[i].start) > after && test.matchesTag(&nodes[i]) {
			out = append(out, tagNode(&nodes[i]))
		}
	}
	return out
}

func collectPreceding(x *xnode, test *nodeTest, out nodeSet, state *evalState) nodeSet {
	if x.kind == xRoot {
		return out
	}
	before := x.rangeStart()
	if test.matchesText() {
		all := state.allNodes()
		for i := len(all) - 1; i >= 0; i-- {
			if all[i].rangeEnd() < before && test.matches(&all[i], false) {
				out = append(out, all[i])
			}
		}
		return out
	}
	nodes := state.doc.nodes
	last := len(nodes) - 1
	if x.kind == xElement || x.kind == xComment {
		last = int(x.tag.index) - 1
	}
	for i := last; i >= 0; i-- {
		if int(nodes[i].end) < before && test.matchesTag(&nodes[i]) {
			out = append(out, tagNode(&nodes[i]))
		}
	}
	return out
}

// filter applies the predicates one after another in place; positions are 1-based in the order of the list.
func filter(nodes nodeSet, predicates []xexpr, state *evalState) nodeSet {
	for _, p := range predicates {
		size := len(nodes)
		w := 0
		for i := 0; i < size; i++ {
			if evalPredicate(p, &xctx{node: nodes[i], position: i + 1, size: size, state: state}) {
				nodes[w] = nodes[i]
				w++
			}
		}
		nodes = nodes[:w]
	}
	return nodes
}

// pathExpr: a location path, absolute or relative, optionally starting from a filter expression: (//a)[1]/@href.
type pathExpr struct {
	start    xexpr
	absolute bool
	steps    []*step
}

func (e *pathExpr) typ() xtype { return xNodeSet }
func (e *pathExpr) usesPosition() bool {
	return e.start != nil && e.start.usesPosition()
}
func (e *pathExpr) evalBool(c *xctx) bool { return toBoolean(e.eval(c)) }
func (e *pathExpr) eval(c *xctx) any {
	var current nodeSet
	switch {
	case e.start != nil:
		current = evalNodes(e.start, c)
	case e.absolute:
		current = nodeSet{rootNode(c.state.doc)}
	default:
		current = nodeSet{c.node}
	}
	for _, s := range e.steps {
		if len(current) == 0 {
			break
		}
		current = s.apply(current, c.state)
	}
	return current
}

// filterExpr: a primary expression with predicates: (//li)[last()].
type filterExpr struct {
	primary    xexpr
	predicates []xexpr
}

func (e *filterExpr) typ() xtype            { return xNodeSet }
func (e *filterExpr) usesPosition() bool    { return e.primary.usesPosition() }
func (e *filterExpr) evalBool(c *xctx) bool { return toBoolean(e.eval(c)) }
func (e *filterExpr) eval(c *xctx) any {
	nodes := append(nodeSet{}, evalNodes(e.primary, c)...)
	return filter(nodes, e.predicates, c.state)
}

// ----- functions -----

type fn uint8

const (
	fnLast fn = iota
	fnPosition
	fnCount
	fnName
	fnLocalName
	fnString
	fnConcat
	fnStartsWith
	fnEndsWith
	fnContains
	fnSubstringBefore
	fnSubstringAfter
	fnSubstring
	fnStringLength
	fnNormalizeSpace
	fnTranslate
	fnLowerCase
	fnUpperCase
	fnNot
	fnTrue
	fnFalse
	fnBoolean
	fnNumber
	fnSum
	fnFloor
	fnCeiling
	fnRound
)

type fnInfo struct {
	fn       fn
	min, max int
	typ      xtype
}

var functions = map[string]fnInfo{
	"last":             {fnLast, 0, 0, xNumber},
	"position":         {fnPosition, 0, 0, xNumber},
	"count":            {fnCount, 1, 1, xNumber},
	"name":             {fnName, 0, 1, xString},
	"local-name":       {fnLocalName, 0, 1, xString},
	"string":           {fnString, 0, 1, xString},
	"concat":           {fnConcat, 2, math.MaxInt, xString},
	"starts-with":      {fnStartsWith, 2, 2, xBoolean},
	"ends-with":        {fnEndsWith, 2, 2, xBoolean},
	"contains":         {fnContains, 2, 2, xBoolean},
	"substring-before": {fnSubstringBefore, 2, 2, xString},
	"substring-after":  {fnSubstringAfter, 2, 2, xString},
	"substring":        {fnSubstring, 2, 3, xString},
	"string-length":    {fnStringLength, 0, 1, xNumber},
	"normalize-space":  {fnNormalizeSpace, 0, 1, xString},
	"translate":        {fnTranslate, 3, 3, xString},
	"lower-case":       {fnLowerCase, 1, 1, xString},
	"upper-case":       {fnUpperCase, 1, 1, xString},
	"not":              {fnNot, 1, 1, xBoolean},
	"true":             {fnTrue, 0, 0, xBoolean},
	"false":            {fnFalse, 0, 0, xBoolean},
	"boolean":          {fnBoolean, 1, 1, xBoolean},
	"number":           {fnNumber, 0, 1, xNumber},
	"sum":              {fnSum, 1, 1, xNumber},
	"floor":            {fnFloor, 1, 1, xNumber},
	"ceiling":          {fnCeiling, 1, 1, xNumber},
	"round":            {fnRound, 1, 1, xNumber},
}

type functionExpr struct {
	fn   fn
	args []xexpr
	t    xtype
}

func (e *functionExpr) typ() xtype { return e.t }

func (e *functionExpr) usesPosition() bool {
	return e.fn == fnPosition || e.fn == fnLast || slices.ContainsFunc(e.args, func(a xexpr) bool { return a.usesPosition() })
}

func (e *functionExpr) evalBool(c *xctx) bool { return toBoolean(e.call(c)) }
func (e *functionExpr) eval(c *xctx) any      { return e.call(c) }

func (e *functionExpr) arg(i int, c *xctx) string { return evalString(e.args[i], c) }

func (e *functionExpr) contextOrArg(c *xctx) string {
	if len(e.args) == 0 {
		return stringValue(&c.node)
	}
	return e.arg(0, c)
}

func (e *functionExpr) call(c *xctx) any {
	switch e.fn {
	case fnLast:
		return float64(c.size)
	case fnPosition:
		return float64(c.position)
	case fnCount:
		return float64(len(evalNodes(e.args[0], c)))
	case fnName, fnLocalName:
		var name string
		if len(e.args) == 0 {
			name = nodeName(&c.node)
		} else if set := evalNodes(e.args[0], c); len(set) > 0 {
			name = nodeName(&set[0])
		}
		if e.fn == fnLocalName {
			name = name[strings.LastIndexByte(name, ':')+1:]
		}
		return name
	case fnString:
		return e.contextOrArg(c)
	case fnConcat:
		var b strings.Builder
		for _, a := range e.args {
			b.WriteString(evalString(a, c))
		}
		return b.String()
	case fnStartsWith:
		return strings.HasPrefix(e.arg(0, c), e.arg(1, c))
	case fnEndsWith:
		return strings.HasSuffix(e.arg(0, c), e.arg(1, c))
	case fnContains:
		return strings.Contains(e.arg(0, c), e.arg(1, c))
	case fnSubstringBefore:
		s := e.arg(0, c)
		if i := strings.Index(s, e.arg(1, c)); i >= 0 {
			return s[:i]
		}
		return ""
	case fnSubstringAfter:
		s, t := e.arg(0, c), e.arg(1, c)
		if i := strings.Index(s, t); i >= 0 {
			return s[i+len(t):]
		}
		return ""
	case fnSubstring:
		return e.substring(c)
	case fnStringLength:
		return float64(utf16Len(e.contextOrArg(c)))
	case fnNormalizeSpace:
		return strings.Join(strings.Fields(e.contextOrArg(c)), " ")
	case fnTranslate:
		s, from, to := e.arg(0, c), []rune(e.arg(1, c)), []rune(e.arg(2, c))
		var b strings.Builder
		for _, r := range s {
			switch i := slices.Index(from, r); {
			case i < 0:
				b.WriteRune(r)
			case i < len(to):
				b.WriteRune(to[i])
			}
		}
		return b.String()
	case fnLowerCase:
		return strings.ToLower(e.arg(0, c))
	case fnUpperCase:
		return strings.ToUpper(e.arg(0, c))
	case fnNot:
		return !e.args[0].evalBool(c)
	case fnTrue:
		return true
	case fnFalse:
		return false
	case fnBoolean:
		return e.args[0].evalBool(c)
	case fnNumber:
		if len(e.args) == 0 {
			return stringToNumber(stringValue(&c.node))
		}
		return evalNumber(e.args[0], c)
	case fnSum:
		sum := 0.0
		set := evalNodes(e.args[0], c)
		for i := range set {
			sum += stringToNumber(stringValue(&set[i]))
		}
		return sum
	case fnFloor:
		return math.Floor(evalNumber(e.args[0], c))
	case fnCeiling:
		return math.Ceil(evalNumber(e.args[0], c))
	case fnRound:
		return round(evalNumber(e.args[0], c))
	}
	panic("fasthtmlparser: unknown XPath function")
}

func round(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return v
	}
	return math.Floor(v + 0.5)
}

// utf16Len: the length in UTF-16 code units, as string-length() counts in .NET.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}

// substring: XPath 1.0, the chars at 1-based positions p with round(start) <= p < round(start) + round(length).
func (e *functionExpr) substring(c *xctx) string {
	s := []rune(e.arg(0, c))
	from := round(evalNumber(e.args[1], c))
	to := math.Inf(1)
	if len(e.args) > 2 {
		to = from + round(evalNumber(e.args[2], c))
	}
	if math.IsNaN(from) || math.IsNaN(to) {
		return ""
	}
	n := float64(len(s))
	var first, end int
	switch {
	case from <= 1:
		first = 0
	case from > n:
		first = len(s)
	default:
		first = int(from) - 1
	}
	switch {
	case to >= n+1:
		end = len(s)
	case to <= 1:
		end = 0
	default:
		end = int(to) - 1
	}
	if end > first {
		return string(s[first:end])
	}
	return ""
}
