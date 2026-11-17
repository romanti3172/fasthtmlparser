package fasthtmlparser

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// The XPath and CSS results of z3l.FastHtmlParser (.NET) for every query of testdata/conformance/queries.json
// on every conformance page: on the document, from three tags of the page, and (CSS) matches() on every tag.
// Results are stored as [count, hash] (see tools/conformance), "throws" when .NET throws.

func TestConformanceCompile(t *testing.T) {
	var expected map[string]map[string]string
	readJSON(t, "testdata/conformance/expected/queries/compile.json", &expected)
	for x, want := range expected["xpath"] {
		_, err := CompileXPath(x)
		if got := okOrError(err); got != want {
			t.Errorf("XPath %q: %s, .NET: %s (%v)", x, got, want, err)
		}
	}
	for c, want := range expected["css"] {
		_, err := CompileCSS(c)
		if got := okOrError(err); got != want {
			t.Errorf("CSS %q: %s, .NET: %s (%v)", c, got, want, err)
		}
	}
}

func okOrError(err error) string {
	if err != nil {
		return "error"
	}
	return "ok"
}

type queryResults struct {
	Scopes []int                                 `json:"scopes"`
	XPath  map[string]map[string]json.RawMessage `json:"xpath"`
	CSS    map[string]map[string]json.RawMessage `json:"css"`
}

// knownDifferences: page and query where the .NET result depends on the order of an unstable sort.
//
// snip039 nests 600 divs, deeper than the 512 levels that are kept; the flattened tags overlap text nodes,
// and 88 text nodes start where a tag starts, so both have the same document-order key. Sorting with duplicates
// removed keeps one of each pair: .NET's List.Sort is unstable and keeps either, the Go port (a stable sort)
// keeps the first. Both give 602 nodes.
var knownDifferences = map[string]bool{
	"snip039 //div/node()": true,
}

func TestConformanceQueries(t *testing.T) {
	files, _ := filepath.Glob("testdata/conformance/expected/queries/*.json")
	if len(files) < 100 {
		t.Fatal("no query results")
	}
	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), ".json")
		if name == "compile" {
			continue
		}
		t.Run(name, func(t *testing.T) {
			var expected queryResults
			readJSON(t, file, &expected)
			source, err := os.ReadFile(filepath.Join("testdata/conformance/input", name+".html"))
			if err != nil {
				t.Fatal(err)
			}
			doc := ParseBytes(source)
			problems := 0
			check := func(what string, want json.RawMessage, got any) {
				if problems >= 10 {
					return
				}
				var w any
				if err := json.Unmarshal(want, &w); err != nil {
					t.Fatal(err)
				}
				if g := normalize(got); !reflect.DeepEqual(g, w) {
					problems++
					t.Errorf("%s\n  go:   %v\n  .net: %v", what, shortValue(g), shortValue(w))
				}
			}

			for x, want := range expected.XPath {
				if knownDifferences[name+" "+x] {
					continue
				}
				q := MustCompileXPath(x)
				check("XPath tags "+x, want["tags"], result(func() []string { return indexes(q.Nodes(doc)) }))
				check("XPath values "+x, want["values"], result(func() []string { return q.Values(doc) }))
				check("XPath value "+x, want["value"], result(func() []string {
					if v, ok := q.Value(doc); ok {
						return []string{v}
					}
					return []string{"<null>"}
				}))
				scoped := []any{}
				for _, s := range expected.Scopes {
					n := doc.Node(s)
					scoped = append(scoped, result(func() []string {
						return append(append(indexes(q.NodesFrom(n)), "|"), q.ValuesFrom(n)...)
					}))
				}
				check("XPath scoped "+x, want["scoped"], scoped)
			}

			for c, want := range expected.CSS {
				q := MustCompileCSS(c)
				check("CSS all "+c, want["all"], result(func() []string { return indexes(q.All(doc)) }))
				check("CSS first "+c, want["first"], result(func() []string {
					if f := q.First(doc); f != nil {
						return []string{strconv.Itoa(f.Index())}
					}
					return []string{"<null>"}
				}))
				check("CSS matches "+c, want["matches"], result(func() []string {
					var found []*Node
					for i := 0; i < doc.Len(); i++ {
						if q.Matches(doc.Node(i)) {
							found = append(found, doc.Node(i))
						}
					}
					return indexes(found)
				}))
				scoped := []any{}
				for _, s := range expected.Scopes {
					n := doc.Node(s)
					scoped = append(scoped, result(func() []string { return indexes(q.AllIn(n)) }))
				}
				check("CSS scoped "+c, want["scoped"], scoped)
			}
		})
	}
}

// result is [count, hash] of the values, or "throws" when the evaluation panics (.NET throws).
// With FASTHTML_FULL=1 it is the values themselves, to compare with a --full dump.
func result(values func() []string) (r any) {
	defer func() {
		if p := recover(); p != nil {
			r = "throws"
		}
	}()
	v := values()
	if os.Getenv("FASTHTML_FULL") != "" {
		return v
	}
	sum := sha256.Sum256([]byte(strings.Join(v, "\x01")))
	return []string{strconv.Itoa(len(v)), hex.EncodeToString(sum[:])[:16]}
}

func indexes(nodes []*Node) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = strconv.Itoa(n.Index())
	}
	return out
}

// normalize turns a Go value into what encoding/json gives for the same JSON.
func normalize(v any) any {
	data, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(data, &out)
	return out
}

func shortValue(v any) string {
	s := fmt.Sprint(v)
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatal(err)
	}
}
