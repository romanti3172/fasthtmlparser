package fasthtml

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// Descendant and ancestor steps on nodes inside one another: n nested input nodes used to collect about n² nodes
// before the duplicates were removed (minutes and gigabytes on these pages). Now they stay linear.
func TestXPathOnNestedNodesIsLinear(t *testing.T) {
	const many = 100_000
	pages := map[string]string{
		"unclosed nesting":           strings.Repeat("<div><span>", many/2) + `<a href="/x">link</a>`,
		"many links in deep nesting": strings.Repeat("<span>", 1000) + strings.Repeat(`<a href="/x">x</a>`, many),
		"nested lists":               strings.Repeat(`<div><ul><li><a href="/x">x</a>`, many/4),
	}
	for name, page := range pages {
		t.Run(name, func(t *testing.T) {
			doc := Parse(page)
			links := doc.FindAll(By(TagA))

			start := time.Now()
			descendants := len(doc.XPath("//*//*//a"))
			inDiv := len(doc.XPath("//div//a[@href]"))
			ancestors := doc.XPathValue("count(//a/ancestor::*)")
			self := len(doc.XPath("//a/ancestor-or-self::*[@href]"))
			if elapsed := time.Since(start); elapsed > 5*time.Second {
				t.Errorf("took %v", elapsed)
			}

			// the expected values, from the tag list
			wantDescendants, wantInDiv := 0, 0
			seen := map[*Node]bool{}
			for _, a := range links {
				if a.Depth() >= 2 {
					wantDescendants++
				}
				if a.ParentMatching(By(TagDiv)) != nil {
					wantInDiv++
				}
				for p := a.Parent(); p != nil && !seen[p]; p = p.Parent() {
					seen[p] = true
				}
			}
			if descendants != wantDescendants || inDiv != wantInDiv || ancestors != strconv.Itoa(len(seen)) || self != len(links) {
				t.Errorf("got %d %d %s %d, want %d %d %d %d",
					descendants, inDiv, ancestors, self, wantDescendants, wantInDiv, len(seen), len(links))
			}
		})
	}
}
