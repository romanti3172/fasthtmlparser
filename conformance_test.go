package fasthtmlparser

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The expected output comes from z3l.FastHtmlParser (.NET) for the same pages: tag names, parents, depths,
// positions (as UTF-8 byte offsets), visible texts and attribute values. The Go port must give exactly the same.
// Inputs: the test fixtures, the broken-markup snippets of the .NET cross-check tests, extra edge cases,
// and fixture pages with end tags left out, names upper-cased and the page cut off at random.
type expectedTag struct {
	Name       string            `json:"name"`
	Parent     int32             `json:"parent"`
	Depth      int32             `json:"depth"`
	Start      int32             `json:"start"`
	End        int32             `json:"end"`
	InnerStart int32             `json:"innerStart"`
	InnerEnd   int32             `json:"innerEnd"`
	Text       string            `json:"text"`
	Attrs      map[string]string `json:"attrs"`
	More       tagMore
}

// tagMore: the other per-tag results (text with line breaks, navigation, links).
type tagMore struct {
	TextLineBreaks  string `json:"textLineBreaks"`
	Source          string `json:"source"`
	Next            int    `json:"next"`
	Previous        int    `json:"previous"`
	NextSibling     int    `json:"nextSibling"`
	PreviousSibling int    `json:"previousSibling"`
	Children        string `json:"children"`
	Href            string `json:"href"`
	Src             string `json:"src"`
	MailTo          string `json:"mailto"`
	Tel             string `json:"tel"`
	TextAfter       string `json:"textAfter"`
	TextNoTagsAfter string `json:"textNoTagsAfter"`
}

// the page URL the .NET dump uses (tools/conformance/Program.cs)
const conformancePageURL = "https://Example.com:443/catalog/page-1.html?sort=asc#top"

func nodeIndex(n *Node) int {
	if n == nil {
		return -1
	}
	return n.Index()
}

func moreOf(n *Node) tagMore {
	children := make([]string, 0)
	for _, c := range n.Children() {
		children = append(children, fmt.Sprint(c.Index()))
	}
	return tagMore{
		TextLineBreaks: n.TextLineBreaks(), Source: n.Source(),
		Next: nodeIndex(n.Next()), Previous: nodeIndex(n.Previous()),
		NextSibling: nodeIndex(n.NextSibling()), PreviousSibling: nodeIndex(n.PreviousSibling()),
		Children: strings.Join(children, ","),
		Href:     n.Href(), Src: n.Src(), MailTo: n.HrefMailTo(), Tel: n.HrefTelephone(),
		TextAfter: n.TextAfterTagUntilNextOne(), TextNoTagsAfter: n.TextNoTagsAfterTagUntilNextOne(),
	}
}

// tagPos is everything but the attributes: comparable with ==.
type tagPos struct {
	Name                                            string
	Parent, Depth, Start, End, InnerStart, InnerEnd int32
	Text                                            string
}

func TestConformanceWithDotNet(t *testing.T) {
	inputs, err := filepath.Glob("testdata/conformance/input/*.html")
	if err != nil || len(inputs) == 0 {
		t.Fatal("no inputs", err)
	}
	for _, input := range inputs {
		name := strings.TrimSuffix(filepath.Base(input), ".html")
		t.Run(name, func(t *testing.T) {
			source, err := os.ReadFile(input)
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join("testdata/conformance/expected", name+".json"))
			if err != nil {
				t.Fatal(err)
			}
			var expected []expectedTag
			if err := json.Unmarshal(data, &expected); err != nil {
				t.Fatal(err)
			}
			var more []tagMore // the same rows, read again for the other fields
			if err := json.Unmarshal(data, &more); err != nil {
				t.Fatal(err)
			}

			doc := ParseWithURL(string(source), conformancePageURL)
			var problems []string
			if doc.Len() != len(expected) {
				problems = append(problems, fmt.Sprintf("tags: %d, .NET: %d", doc.Len(), len(expected)))
			}
			for i := 0; i < min(doc.Len(), len(expected)) && len(problems) < 10; i++ {
				n, e := doc.Node(i), expected[i]
				got := tagPos{n.Name(), n.parent, n.depth, n.start, n.end, n.innerStart, n.innerEnd, n.Text()}
				want := tagPos{e.Name, e.Parent, e.Depth, e.Start, e.End, e.InnerStart, e.InnerEnd, e.Text}
				if got != want {
					problems = append(problems, fmt.Sprintf("[%d]\n  go:   %+v\n  .net: %+v", i, short(got), short(want)))
					continue
				}
				for attr, value := range expected[i].Attrs {
					if v := n.Attr(attr); v != value {
						problems = append(problems, fmt.Sprintf("[%d] <%s> @%s: %q, .NET: %q", i, n.Name(), attr, v, value))
					}
				}
				if got := moreOf(n); got != more[i] {
					problems = append(problems, fmt.Sprintf("[%d] <%s>\n  go:   %+v\n  .net: %+v", i, n.Name(), got, more[i]))
				}
			}
			if len(problems) > 0 {
				t.Error(strings.Join(problems, "\n"))
			}
		})
	}
}

func short(e tagPos) tagPos {
	if len(e.Text) > 80 {
		e.Text = e.Text[:80] + "..."
	}
	return e
}
