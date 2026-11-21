# fasthtmlparser

[![CI](https://github.com/romanti3172/fasthtmlparser/actions/workflows/ci.yml/badge.svg)](https://github.com/romanti3172/fasthtmlparser/actions/workflows/ci.yml)

**Website:** [fasthtmlparser.com](https://fasthtmlparser.com): try the parser on any page, documentation and every language.

Go port of [z3l.FastHtmlParser](https://github.com/romanti3172/z3l.FastHtmlParser): a fast, low-allocation
HTML / XML parser for scraping. The whole API is ported: the parser, attributes, HTML5 entities, visible text and text
with line breaks, `Find` / `FindAll`, navigation, links, **XPath 1.0**, **CSS selectors** and the one-call shortcuts.
Parsing from a pooled `char[]` buffer and `Dispose` of the .NET version have no Go counterpart, and need none:
`Parse` works on the string it is given without copying it, and every value it returns (`Text`, `Attr`, `Body`, ...)
is a substring of it, so a buffer must not be reused while they are in use; the garbage collector frees the rest.

```
go get github.com/romanti3172/fasthtmlparser
```

Go 1.23 or later, no dependencies.

```go
import "github.com/romanti3172/fasthtmlparser"

doc := fasthtmlparser.ParseWithURL(page, "https://example.com/catalog/") // or fasthtmlparser.Parse(page)

// search by name, class, id, attribute, text
for _, pod := range doc.FindAll(fasthtmlparser.By(fasthtmlparser.TagArticle).Class("product_pod")) {
    title := pod.Find(fasthtmlparser.By(fasthtmlparser.TagH3)).Find(fasthtmlparser.By(fasthtmlparser.TagA)).Attr("title")
    price := pod.Find(fasthtmlparser.By(fasthtmlparser.TagP).Class("price_color")).Text()
}

// CSS selectors, as querySelectorAll / querySelector / matches
doc.CSS("article.product_pod h3 > a")
doc.CSSFirst("ul#menu > li:last-child a").Attr("href")
doc.CSS("tr:has(> td.price)")
card.CSS("> span") // the children of card

// XPath 1.0
doc.XPath("//div[@class='card']//span[@class='price']")
doc.XPathValues("//a[contains(@href, '/product/')]/@href")
doc.XPathValue("//dt[.='Price']/following-sibling::dd[1]")
doc.XPathValue("count(//table//tr)") // "12"

// compile once, reuse on any number of pages and goroutines
var titles = fasthtmlparser.MustCompileXPath("//article//h3/a/@title")
titles.Values(doc)

// one-call shortcuts (FindByName*, FindAllByName*, IsName*, ParentByName, NextSiblingByName, Attribute*, ...)
pod := doc.FindByNameClass(fasthtmlparser.TagArticle, "product_pod")
pod.FindByNameItemprop(fasthtmlparser.TagSpan, "price").Text()
doc.FindByNameText(fasthtmlparser.TagDt, "Price").NextSiblingByName(fasthtmlparser.TagDd).Text()

// reading a tag
tag.Text()           // visible text, entities decoded
tag.TextLineBreaks() // <br> and block elements → new line, <p> and headings → paragraph
tag.Attr(fasthtmlparser.AttrDataSrc) // decoded value; AttrRaw: as written
tag.Href()           // absolute URL, resolved against the page URL; Src() for src
tag.HrefMailTo()     // the address of a mailto: link; HrefTelephone() for tel:
tag.Source()         // the opening tag without '<' and '>'

// navigation
tag.Parent(); tag.Children(); tag.NextSibling(); tag.PreviousSibling(); tag.Next(); tag.Previous()
tag.ParentMatching(fasthtmlparser.By(fasthtmlparser.TagTable))
tag.TextAfterUntil(fasthtmlparser.By(fasthtmlparser.TagBr)) // the source between the tag and the next <br>
```

Queries: `By(tag)`, `ByName("custom-tag")` or `Any()`, then `.Class`, `.ID`, `.Attr`, `.Text`, `.IDStartsWith`,
`.TextStartsWith`, `.Body`, `.BodyStartsWith`, `.Source`. The shortcuts take a `Tag`; for other names use a query:
`doc.Find(fasthtmlparser.ByName("my-widget").Class("x"))`. `Tag*` and `Attr*` constants cover about 250 tag and 230 attribute
names (HTML, SVG, MathML, RSS, Atom, sitemap); any other attribute name works as a string.

Every `*Node` method accepts nil and returns nil / "" / false, so chains never panic on a missing tag.
The string methods (`CSS`, `XPath`, ...) compile and cache the query and panic on an invalid one, as
`regexp.MustCompile` does; `CompileCSS` / `CompileXPath` return an error instead.

CSS: type, `*`, `#id`, `.class`, attribute selectors with all operators and the `i` / `s` flags, the four combinators,
selector lists, `:first-child`, `:last-child`, `:only-child`, `:*-of-type`, `:nth-child(An+B [of S])` and its relatives,
`:not()`, `:is()`, `:where()`, `:has()`, `:empty`, `:root`, `:scope`, `:link`, `:checked`, `:disabled`, `:enabled`,
`:required`, `:optional`, `:contains("text")`. XPath: all axes except namespace, predicates, `|`, the operators and all
XPath 1.0 functions, plus `ends-with()`, `lower-case()`, `upper-case()`. HTML rules apply: names are case-insensitive,
the string-value of an element is its visible text, attribute values and text are decoded.

## Same results as the .NET version

The conformance tests compare the Go port with z3l.FastHtmlParser 1.4.1 (.NET) on 121 pages: the test fixtures,
the broken-markup snippets of the .NET cross-check tests, extra edge cases (entities, raw text, comments, XML, custom
elements, 600-deep nesting, Unicode whitespace, links of every kind, line breaks), and every fixture with
`</li> </p> </td> </tr>` left out, plus three copies of each with random end tags dropped, names upper-cased and the page
cut off, and copies saved with CR LF and with a lone CR.

- `conformance_test.go`: every tag (over 15,000): names, parents, depths, positions (UTF-8 byte offsets), visible text,
  text with line breaks, 10 attribute values, the opening tag source, next / previous tag and sibling, children, the
  resolved href and src (against a page URL with an upper-case host and its default port), mailto: and tel: values,
  and the text after the tag must be identical.
- `conformance_query_test.go`: 569 queries, every string of the .NET XPath and CSS tests plus extra ones for the axes,
  functions and selectors. Whether each one compiles must be the same (119 are invalid); the 228 XPath and 222 CSS
  queries that compile are run on every page, on the document and from three tags of the page, and every CSS selector is
  also matched against every tag. Selected tags and values must be identical.

All pass, with one documented exception (`knownDifferences`): on the page nested 600 levels deep, where the .NET result
depends on the order of an unstable sort.

Two intended differences: `TextLineBreaks` joins lines with `"\n"` on every OS (the .NET version uses
`Environment.NewLine`, "\r\n" on Windows; the conformance data is compared with "\n"), and a missing value is `""`
where the .NET version returns null. Links resolved against the page are written as .NET's `System.Uri` writes them
(host in lower case, no default port, readable escapes); absolute links are returned as written, as in .NET.

The expected output in `testdata/conformance/expected` is produced by `tools/conformance`: a small .NET program that
references the z3l.FastHtmlParser package; `make_inputs.py` and `make_queries.py` build the pages and the queries.

## Benchmarks

`cd bench && go test -bench . -benchmem` (Go 1.27, AMD Ryzen 7 3800X, Windows). Medians of 3 runs.
goquery parses with golang.org/x/net/html and selects with cascadia; antchfx/htmlquery runs XPath on x/net/html.

Parse a page:

| Page | fasthtmlparser | x/net/html | Ratio | Memory fasthtmlparser | x/net/html | Allocs fasthtmlparser | x/net/html |
|---|---:|---:|---:|---:|---:|---:|---:|
| quotes, 11 KB | 26 μs | 285 μs | 11.1x | 6.2 KB | 96 KB | 2 | 2,498 |
| laptops, 38 KB | 76 μs | 976 μs | 12.9x | 18.6 KB | 302 KB | 2 | 7,886 |
| books, 51 KB | 88 μs | 971 μs | 11.1x | 24.7 KB | 287 KB | 2 | 6,707 |
| books x10, 510 KB | 0.85 ms | 9.55 ms | 11.3x | 232 KB | 2.82 MB | 2 | 66,895 |
| books without `</li>` `</p>` | 88 μs | 903 μs | 10.2x | 24.7 KB | 271 KB | 2 | 6,306 |

Scrape a page: parse and extract the records (`TestSameRecords*` check that every variant returns the same records).
Books: 20 × title, price, rating; quotes: 10 × text, author, tags.

| Task | fasthtmlparser | Other | Ratio | Memory fasthtmlparser | Other | Allocs fasthtmlparser | Other |
|---|---:|---:|---:|---:|---:|---:|---:|
| books, `Find` / goquery | 113 μs | 1,072 μs | 9.5x | 29.3 KB | 307 KB | 15 | 7,391 |
| books, CSS / goquery | 118 μs | 1,072 μs | 9.1x | 31.4 KB | 307 KB | 136 | 7,391 |
| books, XPath / htmlquery | 250 μs | 1,194 μs | 4.8x | 119 KB | 362 KB | 1,943 | 9,158 |
| quotes, `Find` / goquery | 41 μs | 336 μs | 8.1x | 10.2 KB | 109 KB | 58 | 2,921 |
| quotes, CSS / goquery | 45 μs | 333 μs | 7.5x | 11.1 KB | 109 KB | 109 | 2,921 |

The XPath engine builds node-sets per step; that is where its allocations come from, and the first thing to optimize.

## Layout

- `parse.go`: the scanner and the HTML5 optional-end-tag rules (port of `ParsingCore.cs`)
- `attr.go`, `decode.go`, `text.go`, `textlines.go`: attributes, character references, visible text, text with line breaks
- `document.go`: `Document`, `Node`, `Query`, search and navigation; `links.go`: href / src, mailto:, tel:
- `css.go`: CSS selectors; `xpath.go`, `xpath_compile.go`: XPath 1.0
- `shortcuts.go`, `shortcuts_gen.go`: the one-call shortcuts, generated by `go generate` (`internal/genshortcuts`)
- `names_gen.go`, `attributes_gen.go`, `entities_gen.go`: generated from the C# sources by
  `go run ./internal/gen <path to src/z3l.FastHtmlParser>`
- `bench/`: benchmarks in their own module, so the library has no dependencies
- `tools/conformance/`: builds the conformance data from the .NET version

## Other languages

The same parser is available for:

| Language | Package | Source |
|---|---|---|
| C# (.NET 9 and 10) | [z3l.FastHtmlParser](https://www.nuget.org/packages/z3l.FastHtmlParser) on NuGet | [romanti3172/z3l.FastHtmlParser](https://github.com/romanti3172/z3l.FastHtmlParser) |
| Go | `github.com/romanti3172/fasthtmlparser` | [romanti3172/fasthtmlparser](https://github.com/romanti3172/fasthtmlparser) |
| Rust | [fasthtmlparser](https://crates.io/crates/fasthtmlparser) on crates.io | [romanti3172/fasthtmlparser-rs](https://github.com/romanti3172/fasthtmlparser-rs) |
| JavaScript / TypeScript | [z3l-fasthtmlparser](https://www.npmjs.com/package/z3l-fasthtmlparser) on npm | [romanti3172/fasthtmlparser-js](https://github.com/romanti3172/fasthtmlparser-js) |
| Python | [fasthtmlparser](https://pypi.org/project/fasthtmlparser/) on PyPI | [romanti3172/fasthtmlparser-py](https://github.com/romanti3172/fasthtmlparser-py) |

## License

Same terms as z3l.FastHtmlParser (see LICENSE.md).
