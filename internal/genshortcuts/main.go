// Command genshortcuts writes shortcuts_gen.go: the one-call shortcuts of z3l.FastHtmlParser (FindByNameClass,
// FindAllByNameItemprop, IsNameClass, ParentByName, ...). Each one builds a Query and calls Find, FindAll, Is
// or a navigation method; Find and FindAll shortcuts are written for both *Document and *Node.
//
//	go run ./internal/genshortcuts
package main

import (
	"bytes"
	"fmt"
	"go/format"
	"os"
)

// shortcut: name, Go parameters, the query, and an optional guard (a condition that gives nil / false).
type shortcut struct {
	name, params, query, guard, doc string
}

var finds = []shortcut{
	{"FindByID", "id string", "Any().ID(id)", `id == ""`, "the first tag with the id"},
	{"FindByName", "name Tag", "By(name)", "", "the first tag with the name"},
	{"FindByNameBody", "name Tag, body string", "By(name).Body(body)", "", "the first tag with the name and the inner HTML"},
	{"FindByNameAttribute", "name Tag, attribute, value string", "By(name).Attr(attribute, value)", "", "the first tag with the name and the decoded attribute value"},
	{"FindByNameClass", "name Tag, class string", "By(name).Class(class)", "", "the first tag with the name and the class"},
	{"FindByNameClassBody", "name Tag, class, body string", "By(name).Class(class).Body(body)", "", "the first tag with the name, the class and the inner HTML"},
	{"FindByNameClassText", "name Tag, class, text string", "optText(optClass(By(name), class), text)", "", "the first tag with the name, and the class and visible text when they are not empty"},
	{"FindByNameIDText", "name Tag, id, text string", "optText(optID(By(name), id), text)", "", "the first tag with the name, and the id and visible text when they are not empty"},
	{"FindByNameText", "name Tag, text string", "By(name).Text(text)", "", "the first tag with the name and the visible text"},
	{"FindByNameID", "name Tag, id string", "By(name).ID(id)", "", "the first tag with the name and the id"},
	{"FindByNameIDStartsWith", "name Tag, idPrefix string", "By(name).IDStartsWith(idPrefix)", "", "the first tag with the name whose id starts with the prefix"},
	{"FindByNameBodyStartsWith", "name Tag, bodyPrefix string", "By(name).BodyStartsWith(bodyPrefix)", "", "the first tag with the name whose inner HTML starts with the prefix"},
	{"FindByNameTextStartsWith", "name Tag, textPrefix string", "By(name).TextStartsWith(textPrefix)", `textPrefix == ""`, "the first tag with the name whose visible text starts with the prefix"},
	{"FindByNameClassTextStartsWith", "name Tag, class, textPrefix string", "By(name).Class(class).TextStartsWith(textPrefix)", `textPrefix == ""`, "the first tag with the name and the class whose visible text starts with the prefix"},
	{"FindByNameItemprop", "name Tag, itemprop string", "By(name).Attr(AttrItemprop, itemprop)", "", "the first tag with the name and the itemprop"},
}

var findAlls = []shortcut{
	{"FindAllByName", "name Tag", "By(name)", "", "all tags with the name"},
	{"FindAllByNameAttribute", "name Tag, attribute, value string", "By(name).Attr(attribute, value)", "", "all tags with the name and the decoded attribute value"},
	{"FindAllByNameClass", "name Tag, class string", "By(name).Class(class)", "", "all tags with the name and the class"},
	{"FindAllByNameID", "name Tag, id string", "By(name).ID(id)", "", "all tags with the name and the id"},
	{"FindAllByNameIDStartsWith", "name Tag, idPrefix string", "By(name).IDStartsWith(idPrefix)", "", "all tags with the name whose id starts with the prefix"},
	{"FindAllByNameItemprop", "name Tag, itemprop string", "By(name).Attr(AttrItemprop, itemprop)", "", "all tags with the name and the itemprop"},
	{"FindAllByNameBodyStartsWith", "name Tag, bodyPrefix string", "By(name).BodyStartsWith(bodyPrefix)", "", "all tags with the name whose inner HTML starts with the prefix"},
}

var iss = []shortcut{
	{"IsNameAttribute", "name Tag, attribute, value string", "By(name).Attr(attribute, value)", "", "the tag has the name and the decoded attribute value"},
	{"IsNameBody", "name Tag, body string", "By(name).Body(body)", "", "the tag has the name and the inner HTML"},
	{"IsNameText", "name Tag, text string", "By(name).Text(text)", "", "the tag has the name and the visible text"},
	{"IsNameClass", "name Tag, class string", "By(name).Class(class)", "", "the tag has the name and the class"},
	{"IsNameClassBody", "name Tag, class, body string", "By(name).Class(class).Body(body)", "", "the tag has the name, the class and the inner HTML"},
	{"IsNameClassIDBody", "name Tag, class, id, body string", "optBody(optID(optClass(By(name), class), id), body)", "", "the tag has the name, and the class, id and inner HTML when they are not empty"},
	{"IsNameClassText", "name Tag, class, text string", "optText(optClass(By(name), class), text)", "", "the tag has the name, and the class and visible text when they are not empty"},
	{"IsNameIDText", "name Tag, id, text string", "optText(optID(By(name), id), text)", "", "the tag has the name, and the id and visible text when they are not empty"},
	{"IsNameID", "name Tag, id string", "By(name).ID(id)", "", "the tag has the name and the id"},
	{"IsNameTextStartsWith", "name Tag, textPrefix string", "By(name).TextStartsWith(textPrefix)", "", "the tag has the name and its visible text starts with the prefix"},
	{"IsNameBodyStartsWith", "name Tag, bodyPrefix string", "By(name).BodyStartsWith(bodyPrefix)", "", "the tag has the name and its inner HTML starts with the prefix"},
	{"IsNameClassTextStartsWith", "name Tag, class, textPrefix string", "By(name).Class(class).TextStartsWith(textPrefix)", "", "the tag has the name and the class and its visible text starts with the prefix"},
	{"IsNameIDStartsWith", "name Tag, idPrefix string", "By(name).IDStartsWith(idPrefix)", "", "the tag has the name and its id starts with the prefix"},
	{"IsNameItemprop", "name Tag, itemprop string", "By(name).Attr(AttrItemprop, itemprop)", "", "the tag has the name and the itemprop"},
	{"IsNameSource", "name Tag, source string", "By(name).Source(source)", "", "the tag has the name and its opening tag (without '<' and '>') equals source"},
}

// navigation: name, params, the call on n, the result type, doc
type nav struct{ name, params, call, result, doc string }

var navs = []nav{
	{"ParentByName", "name Tag", "n.ParentMatching(By(name))", "*Node", "the nearest enclosing tag with the name"},
	{"ParentByNameClass", "name Tag, class string", "n.ParentMatching(By(name).Class(class))", "*Node", "the nearest enclosing tag with the name and the class"},
	{"NextByName", "name Tag", "n.NextMatching(By(name))", "*Node", "the first tag with the name after the end of n"},
	{"NextByNameClass", "name Tag, class string", "n.NextMatching(By(name).Class(class))", "*Node", "the first tag with the name and the class after the end of n"},
	{"NextSiblingByName", "name Tag", "n.NextSiblingMatching(By(name))", "*Node", "the next sibling with the name"},
	{"NextSiblingByNameClass", "name Tag, class string", "n.NextSiblingMatching(By(name).Class(class))", "*Node", "the next sibling with the name and the class"},
	{"PreviousSiblingByName", "name Tag", "n.PreviousSiblingMatching(By(name))", "*Node", "the nearest previous sibling with the name"},
	{"TextAfterUntilTagByName", "name Tag", "n.TextAfterUntil(By(name))", "string", "the source between the end of n and the next tag with the name"},
	{"TextAfterUntilTagByNameClass", "name Tag, class string", "n.TextAfterUntil(By(name).Class(class))", "string", "the source between the end of n and the next tag with the name and the class"},
}

var attributes = []struct{ method, constant string }{
	{"AttributeAlt", "AttrAlt"}, {"AttributeClass", "AttrClass"}, {"AttributeContent", "AttrContent"},
	{"AttributeID", "AttrId"}, {"AttributeItemProp", "AttrItemprop"}, {"AttributeName", "AttrName"},
	{"AttributeStyle", "AttrStyle"}, {"AttributeTitle", "AttrTitle"}, {"AttributeValue", "AttrValue"},
}

func main() {
	var b bytes.Buffer
	b.WriteString("// Code generated by internal/genshortcuts; DO NOT EDIT.\n\npackage fasthtml\n\n")

	for _, recv := range []struct{ name, typ, scope string }{{"d", "*Document", "in the document"}, {"n", "*Node", "inside the tag"}} {
		for _, s := range finds {
			fmt.Fprintf(&b, "// %s returns %s %s, or nil.\nfunc (%s %s) %s(%s) *Node {\n", s.name, s.doc, recv.scope, recv.name, recv.typ, s.name, s.params)
			if s.guard != "" {
				fmt.Fprintf(&b, "\tif %s {\n\t\treturn nil\n\t}\n", s.guard)
			}
			fmt.Fprintf(&b, "\treturn %s.Find(%s)\n}\n\n", recv.name, s.query)
		}
		for _, s := range findAlls {
			fmt.Fprintf(&b, "// %s returns %s %s.\nfunc (%s %s) %s(%s) []*Node {\n\treturn %s.FindAll(%s)\n}\n\n",
				s.name, s.doc, recv.scope, recv.name, recv.typ, s.name, s.params, recv.name, s.query)
		}
	}
	for _, s := range iss {
		fmt.Fprintf(&b, "// %s reports whether %s.\nfunc (n *Node) %s(%s) bool {\n\treturn n.Is(%s)\n}\n\n", s.name, s.doc, s.name, s.params, s.query)
	}
	for _, s := range navs {
		fmt.Fprintf(&b, "// %s returns %s.\nfunc (n *Node) %s(%s) %s {\n\treturn %s\n}\n\n", s.name, s.doc, s.name, s.params, s.result, s.call)
	}
	for _, a := range attributes {
		fmt.Fprintf(&b, "// %s returns the decoded value of the %s attribute.\nfunc (n *Node) %s() string {\n\treturn n.Attr(%s)\n}\n\n",
			a.method, a.constant, a.method, a.constant)
	}

	code, err := format.Source(b.Bytes())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile("shortcuts_gen.go", code, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
