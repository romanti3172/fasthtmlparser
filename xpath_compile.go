package fasthtmlparser

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// Tokenizer and recursive-descent parser for XPath 1.0 expressions.

type tokenKind uint8

const (
	tEnd tokenKind = iota
	tSlash
	tDoubleSlash
	tLParen
	tRParen
	tLBracket
	tRBracket
	tDot
	tDotDot
	tAt
	tComma
	tColonColon
	tPipe
	tPlus
	tMinus
	tEq
	tNeq
	tLt
	tLe
	tGt
	tGe
	tStar     // name test *
	tMultiply // operator *
	tAnd
	tOr
	tDiv
	tMod
	tName
	tLiteral
	tNumber
	tDollar
)

type token struct {
	kind     tokenKind
	text     string
	number   float64
	position int // in runes
}

type xpathError struct{ msg string }

func (e xpathError) Error() string { return e.msg }

type xpathCompiler struct {
	source []rune
	tokens []token
	index  int
}

func newXPathCompiler(s string) *xpathCompiler {
	c := &xpathCompiler{source: []rune(s)}
	c.tokenize()
	return c
}

func (c *xpathCompiler) failAt(msg string, position int) {
	panic(xpathError{fmt.Sprintf("invalid XPath %q at position %d: %s", string(c.source), position, msg)})
}

func (c *xpathCompiler) fail(msg string) { c.failAt(msg, c.peek(0).position) }

func isXNameStart(r rune) bool { return unicode.IsLetter(r) || r == '_' }

func isXNameChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.'
}

func isDigit(r rune) bool { return r >= '0' && r <= '9' }

func (c *xpathCompiler) tokenize() {
	s := c.source
	i := 0
	at := func(k int) rune {
		if k < len(s) {
			return s[k]
		}
		return 0
	}
	for {
		for i < len(s) && unicode.IsSpace(s[i]) {
			i++
		}
		if i >= len(s) {
			c.tokens = append(c.tokens, token{kind: tEnd, position: i})
			return
		}
		start := i
		ch, next := s[i], at(i+1)

		// XPath 1.0 lexical rule: after a token that can end an operand, '*' and and/or/div/mod are operators
		operatorExpected := false
		if n := len(c.tokens); n > 0 {
			switch c.tokens[n-1].kind {
			case tRParen, tRBracket, tDot, tDotDot, tStar, tName, tLiteral, tNumber:
				operatorExpected = true
			}
		}

		tok := token{position: start}
		switch {
		case ch == '/':
			if next == '/' {
				tok.kind, i = tDoubleSlash, i+2
			} else {
				tok.kind, i = tSlash, i+1
			}
		case ch == '(':
			tok.kind, i = tLParen, i+1
		case ch == ')':
			tok.kind, i = tRParen, i+1
		case ch == '[':
			tok.kind, i = tLBracket, i+1
		case ch == ']':
			tok.kind, i = tRBracket, i+1
		case ch == '@':
			tok.kind, i = tAt, i+1
		case ch == ',':
			tok.kind, i = tComma, i+1
		case ch == '|':
			tok.kind, i = tPipe, i+1
		case ch == '+':
			tok.kind, i = tPlus, i+1
		case ch == '-':
			tok.kind, i = tMinus, i+1
		case ch == '=':
			tok.kind, i = tEq, i+1
		case ch == '$':
			tok.kind, i = tDollar, i+1
		case ch == '*':
			tok.kind, i = tStar, i+1
			if operatorExpected {
				tok.kind = tMultiply
			}
		case ch == '!':
			if next != '=' {
				c.failAt("'!' must be followed by '='", i)
			}
			tok.kind, i = tNeq, i+2
		case ch == '<':
			if next == '=' {
				tok.kind, i = tLe, i+2
			} else {
				tok.kind, i = tLt, i+1
			}
		case ch == '>':
			if next == '=' {
				tok.kind, i = tGe, i+2
			} else {
				tok.kind, i = tGt, i+1
			}
		case ch == ':':
			if next != ':' {
				c.failAt("unexpected ':'", i)
			}
			tok.kind, i = tColonColon, i+2
		case ch == '"' || ch == '\'':
			end := -1
			for k := i + 1; k < len(s); k++ {
				if s[k] == ch {
					end = k
					break
				}
			}
			if end < 0 {
				c.failAt("unterminated string literal", i)
			}
			tok.kind, tok.text, i = tLiteral, string(s[i+1:end]), end+1
		case ch == '.' && next == '.':
			tok.kind, i = tDotDot, i+2
		case ch == '.' && !isDigit(next):
			tok.kind, i = tDot, i+1
		case isDigit(ch) || ch == '.':
			for i < len(s) && isDigit(s[i]) {
				i++
			}
			if i < len(s) && s[i] == '.' {
				i++
				for i < len(s) && isDigit(s[i]) {
					i++
				}
			}
			tok.kind = tNumber
			tok.number, _ = strconv.ParseFloat(string(s[start:i]), 64)
		case isXNameStart(ch):
			i++
			for i < len(s) {
				if isXNameChar(s[i]) {
					i++
				} else if s[i] == ':' && i+1 < len(s) && s[i+1] != ':' && (isXNameStart(s[i+1]) || s[i+1] == '*') {
					// a prefixed name (dc:creator) or prefix:*
					i += 2
					if s[i-1] == '*' {
						break
					}
				} else {
					break
				}
			}
			tok.kind, tok.text = tName, string(s[start:i])
			if operatorExpected {
				switch tok.text {
				case "and":
					tok.kind = tAnd
				case "or":
					tok.kind = tOr
				case "div":
					tok.kind = tDiv
				case "mod":
					tok.kind = tMod
				default:
					c.failAt(fmt.Sprintf("expected an operator, found '%s'", tok.text), start)
				}
			}
		default:
			c.failAt(fmt.Sprintf("unexpected character '%c'", ch), i)
		}
		c.tokens = append(c.tokens, tok)
	}
}

// ----- parser helpers -----

func (c *xpathCompiler) peek(offset int) token {
	return c.tokens[min(c.index+offset, len(c.tokens)-1)]
}

func (c *xpathCompiler) advance() token {
	t := c.tokens[c.index]
	c.index++
	return t
}

func (c *xpathCompiler) accept(kind tokenKind) bool {
	if c.peek(0).kind != kind {
		return false
	}
	c.index++
	return true
}

func (c *xpathCompiler) expect(kind tokenKind, what string) {
	if !c.accept(kind) {
		c.fail(fmt.Sprintf("expected %s, found %s", what, c.describe(c.peek(0))))
	}
}

func (c *xpathCompiler) describe(t token) string {
	switch t.kind {
	case tEnd:
		return "the end of the expression"
	case tName, tLiteral:
		return "'" + t.text + "'"
	case tNumber:
		return numberToString(t.number)
	}
	end := min(t.position+2, len(c.source))
	return "'" + strings.TrimRight(string(c.source[t.position:end]), " \t\r\n") + "'"
}

// ----- grammar (XPath 1.0, section 3) -----

func (c *xpathCompiler) parseExpr() xexpr { return c.parseOr() }

func (c *xpathCompiler) parseOr() xexpr {
	left := c.parseAnd()
	for c.accept(tOr) {
		left = &logicExpr{and: false, left: left, right: c.parseAnd()}
	}
	return left
}

func (c *xpathCompiler) parseAnd() xexpr {
	left := c.parseEquality()
	for c.accept(tAnd) {
		left = &logicExpr{and: true, left: left, right: c.parseEquality()}
	}
	return left
}

func (c *xpathCompiler) parseEquality() xexpr {
	left := c.parseRelational()
	for {
		var op compareOp
		switch {
		case c.accept(tEq):
			op = opEq
		case c.accept(tNeq):
			op = opNeq
		default:
			return left
		}
		left = makeEquality(op, left, c.parseRelational())
	}
}

// makeEquality: @name = 'literal' is compared in place; everything else by the general rules.
func makeEquality(op compareOp, left, right xexpr) xexpr {
	if name, ok := attributeName(left); ok {
		if lit, ok := right.(*literalExpr); ok {
			return &attributeEqualsExpr{name: name, value: lit.value, negate: op == opNeq}
		}
	}
	if name, ok := attributeName(right); ok {
		if lit, ok := left.(*literalExpr); ok {
			return &attributeEqualsExpr{name: name, value: lit.value, negate: op == opNeq}
		}
	}
	return &compareExpr{op: op, left: left, right: right}
}

// attributeName: whether the expression is a bare @name (one attribute step, no predicates).
func attributeName(e xexpr) (string, bool) {
	p, ok := e.(*pathExpr)
	if !ok || p.start != nil || p.absolute || len(p.steps) != 1 {
		return "", false
	}
	s := p.steps[0]
	if s.axis != axisAttribute || len(s.predicates) > 0 || s.test.kind != testName {
		return "", false
	}
	return s.test.name, true
}

func (c *xpathCompiler) parseRelational() xexpr {
	left := c.parseAdditive()
	for {
		var op compareOp
		switch {
		case c.accept(tLt):
			op = opLt
		case c.accept(tLe):
			op = opLe
		case c.accept(tGt):
			op = opGt
		case c.accept(tGe):
			op = opGe
		default:
			return left
		}
		left = &compareExpr{op: op, left: left, right: c.parseAdditive()}
	}
}

func (c *xpathCompiler) parseAdditive() xexpr {
	left := c.parseMultiplicative()
	for {
		switch {
		case c.accept(tPlus):
			left = &arithExpr{op: opAdd, left: left, right: c.parseMultiplicative()}
		case c.accept(tMinus):
			left = &arithExpr{op: opSub, left: left, right: c.parseMultiplicative()}
		default:
			return left
		}
	}
}

func (c *xpathCompiler) parseMultiplicative() xexpr {
	left := c.parseUnary()
	for {
		switch {
		case c.accept(tMultiply):
			left = &arithExpr{op: opMul, left: left, right: c.parseUnary()}
		case c.accept(tDiv):
			left = &arithExpr{op: opDiv, left: left, right: c.parseUnary()}
		case c.accept(tMod):
			left = &arithExpr{op: opMod, left: left, right: c.parseUnary()}
		default:
			return left
		}
	}
}

func (c *xpathCompiler) parseUnary() xexpr {
	if c.accept(tMinus) {
		return &negateExpr{c.parseUnary()}
	}
	return c.parseUnion()
}

func (c *xpathCompiler) parseUnion() xexpr {
	left := c.parsePath()
	for c.peek(0).kind == tPipe {
		position := c.advance().position
		right := c.parsePath()
		if left.typ() != xNodeSet || right.typ() != xNodeSet {
			c.failAt("'|' needs node-sets on both sides", position)
		}
		left = &unionExpr{left, right}
	}
	return left
}

var nodeTypes = map[string]bool{"node": true, "text": true, "comment": true, "processing-instruction": true}

func (c *xpathCompiler) parsePath() xexpr {
	t := c.peek(0)
	isFilter := t.kind == tLiteral || t.kind == tNumber || t.kind == tLParen || t.kind == tDollar ||
		t.kind == tName && c.peek(1).kind == tLParen && !nodeTypes[t.text]
	if !isFilter {
		return c.parseLocationPath()
	}

	position := t.position
	primary := c.parsePrimary()
	if predicates := c.parsePredicates(); len(predicates) > 0 {
		if primary.typ() != xNodeSet {
			c.failAt("a predicate needs a node-set", position)
		}
		primary = &filterExpr{primary, predicates}
	}
	if k := c.peek(0).kind; k != tSlash && k != tDoubleSlash {
		return primary
	}
	if primary.typ() != xNodeSet {
		c.failAt("'/' needs a node-set on the left", position)
	}
	steps := c.parseRelativeSteps(nil, true)
	return &pathExpr{start: primary, steps: optimize(steps)}
}

func (c *xpathCompiler) parseLocationPath() xexpr {
	var steps []*step
	absolute := false
	switch {
	case c.accept(tSlash):
		absolute = true
		if canStartStep(c.peek(0)) {
			steps = c.parseRelativeSteps(steps, false)
		}
	case c.peek(0).kind == tDoubleSlash:
		absolute = true
		steps = c.parseRelativeSteps(steps, true)
	default:
		if !canStartStep(c.peek(0)) {
			c.fail("unexpected " + c.describe(c.peek(0)))
		}
		steps = c.parseRelativeSteps(steps, false)
	}
	return &pathExpr{absolute: absolute, steps: optimize(steps)}
}

func canStartStep(t token) bool {
	switch t.kind {
	case tDot, tDotDot, tAt, tStar, tName:
		return true
	}
	return false
}

// parseRelativeSteps: steps separated by '/' or '//'; afterSlash: the next token is a '/' or '//' to read first.
func (c *xpathCompiler) parseRelativeSteps(steps []*step, afterSlash bool) []*step {
	if !afterSlash {
		steps = append(steps, c.parseStep())
	}
	for {
		switch {
		case c.accept(tSlash):
			steps = append(steps, c.parseStep())
		case c.accept(tDoubleSlash):
			steps = append(steps, &step{axis: axisDescendantOrSelf, test: anyNodeTest})
			steps = append(steps, c.parseStep())
		default:
			return steps
		}
	}
}

var axes = map[string]axis{
	"child":              axisChild,
	"descendant":         axisDescendant,
	"descendant-or-self": axisDescendantOrSelf,
	"self":               axisSelf,
	"parent":             axisParent,
	"ancestor":           axisAncestor,
	"ancestor-or-self":   axisAncestorOrSelf,
	"following-sibling":  axisFollowingSibling,
	"preceding-sibling":  axisPrecedingSibling,
	"following":          axisFollowing,
	"preceding":          axisPreceding,
	"attribute":          axisAttribute,
}

func (c *xpathCompiler) parseStep() *step {
	if c.accept(tDot) {
		return &step{axis: axisSelf, test: anyNodeTest}
	}
	if c.accept(tDotDot) {
		return &step{axis: axisParent, test: anyNodeTest}
	}
	a := axisChild
	if c.accept(tAt) {
		a = axisAttribute
	} else if c.peek(0).kind == tName && c.peek(1).kind == tColonColon {
		name := c.advance()
		var ok bool
		if a, ok = axes[name.text]; !ok {
			if name.text == "namespace" {
				c.failAt("the namespace axis is not supported", name.position)
			}
			c.failAt(fmt.Sprintf("unknown axis '%s'", name.text), name.position)
		}
		c.advance()
	}
	test := c.parseNodeTest()
	return &step{axis: a, test: test, predicates: c.parsePredicates()}
}

func (c *xpathCompiler) parseNodeTest() *nodeTest {
	if c.accept(tStar) {
		return newNodeTest(testWildcard, "")
	}
	t := c.peek(0)
	if t.kind != tName {
		c.fail("expected a node test, found " + c.describe(t))
	}
	c.advance()
	name := t.text
	if c.peek(0).kind == tLParen && nodeTypes[name] {
		c.advance()
		if name == "processing-instruction" {
			c.accept(tLiteral)
		}
		c.expect(tRParen, "')'")
		switch name {
		case "node":
			return anyNodeTest
		case "text":
			return newNodeTest(testText, "")
		case "comment":
			return newNodeTest(testComment, "")
		}
		return newNodeTest(testPI, "")
	}
	if strings.HasSuffix(name, ":*") {
		return newNodeTest(testPrefix, name[:len(name)-1])
	}
	return newNodeTest(testName, name)
}

func (c *xpathCompiler) parsePredicates() []xexpr {
	var predicates []xexpr
	for c.accept(tLBracket) {
		p := c.parseExpr()
		c.expect(tRBracket, "']'")
		// [@name]: an existence check instead of a node-set
		if name, ok := attributeName(p); ok {
			p = &attributeExistsExpr{name: name}
		}
		predicates = append(predicates, p)
	}
	return predicates
}

func (c *xpathCompiler) parsePrimary() xexpr {
	t := c.advance()
	switch t.kind {
	case tLiteral:
		return &literalExpr{value: t.text}
	case tNumber:
		return &numberExpr{value: t.number}
	case tLParen:
		e := c.parseExpr()
		c.expect(tRParen, "')'")
		return e
	case tDollar:
		c.failAt("variables are not supported", t.position)
	}

	// function call
	info, ok := functions[t.text]
	if !ok {
		c.failAt(fmt.Sprintf("unknown function '%s()'", t.text), t.position)
	}
	c.expect(tLParen, "'('")
	var args []xexpr
	if !c.accept(tRParen) {
		for {
			args = append(args, c.parseExpr())
			if !c.accept(tComma) {
				break
			}
		}
		c.expect(tRParen, "')'")
	}
	if len(args) < info.min || len(args) > info.max {
		c.failAt(fmt.Sprintf("wrong number of arguments for %s()", t.text), t.position)
	}
	switch info.fn {
	case fnCount, fnSum, fnName, fnLocalName:
		if len(args) > 0 && args[0].typ() != xNodeSet {
			c.failAt(fmt.Sprintf("%s() needs a node-set argument", t.text), t.position)
		}
	}
	return makeAttributeTest(info.fn, args, info.typ)
}

// makeAttributeTest: contains(@a, 'x'), starts-with(...), ends-with(...), also with the attribute wrapped in literals
// (contains(concat(' ', @class, ' '), ' x '), the usual class test), are evaluated on the attribute in place.
func makeAttributeTest(f fn, args []xexpr, t xtype) xexpr {
	if f == fnContains || f == fnStartsWith || f == fnEndsWith {
		if needle, ok := args[1].(*literalExpr); ok {
			if name, ok := attributeName(args[0]); ok {
				return &attributeTestExpr{fn: f, name: name, needle: needle.value, needleBytes: []byte(needle.value)}
			}
			if concat, ok := args[0].(*functionExpr); ok && concat.fn == fnConcat && len(concat.args) == 3 {
				prefix, ok1 := concat.args[0].(*literalExpr)
				suffix, ok2 := concat.args[2].(*literalExpr)
				if name, ok := attributeName(concat.args[1]); ok && ok1 && ok2 {
					return &attributeTestExpr{fn: f, prefix: prefix.value, name: name, suffix: suffix.value, needle: needle.value, needleBytes: []byte(needle.value)}
				}
			}
		}
	}
	return &functionExpr{fn: f, args: args, t: t}
}

// optimize: //x is descendant-or-self::node()/child::x. Without positional predicates on x it is the same
// as descendant::x: one linear scan of the tag list instead of the children of every node.
func optimize(steps []*step) []*step {
	for i := 0; i < len(steps)-1; i++ {
		s := steps[i]
		if s.axis != axisDescendantOrSelf || s.test != anyNodeTest || len(s.predicates) > 0 {
			continue
		}
		next := steps[i+1]
		if next.axis == axisChild && !next.hasPositionalPredicates() {
			steps[i] = &step{axis: axisDescendant, test: next.test, predicates: next.predicates}
			steps = append(steps[:i+1], steps[i+2:]...)
		} else if next.axis == axisChild || next.axis == axisAttribute {
			// only the root and elements have children or attributes: skip the text nodes
			steps[i] = &step{axis: axisDescendantOrSelf, test: containersTest}
		}
	}
	return steps
}
