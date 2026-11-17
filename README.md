# z3l.FastHtmlParser.Go (prototype)

Go port of [z3l.FastHtmlParser](https://github.com/romanti3172/z3l.FastHtmlParser): a fast, low-allocation
HTML / XML parser for scraping. Ported: the parser, attributes, HTML5 entities, visible text, `Find` / `FindAll`,
navigation, **XPath 1.0** and **CSS selectors**. Not ported yet: `TextLineBreaks`, URL helpers, the `HtmlAttribute` enum
and the one-call shortcuts.

```go
doc := fasthtml.Parse(page)

// search by name, class, id, attribute, text
for _, pod := range doc.FindAll(fasthtml.By(fasthtml.TagArticle).Class("product_pod")) {
    title := pod.Find(fasthtml.By(fasthtml.TagH3)).Find(fasthtml.By(fasthtml.TagA)).Attr("title")
    price := pod.Find(fasthtml.By(fasthtml.TagP).Class("price_color")).Text()
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
var titles = fasthtml.MustCompileXPath("//article//h3/a/@title")
titles.Values(doc)
```

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

The conformance tests compare the Go port with z3l.FastHtmlParser (.NET, 1.4.0 with the fixes not yet released: linear XPath on nested nodes, CR LF read as LF) on 115 pages: the test fixtures,
the broken-markup snippets of the .NET cross-check tests, extra edge cases (entities, raw text, comments, XML, custom
elements, 600-deep nesting, Unicode whitespace), and every fixture with `</li> </p> </td> </tr>` left out, plus three
copies of each with random end tags dropped, names upper-cased and the page cut off, and copies saved with CR LF and with a lone CR.

- `conformance_test.go`: every tag (over 15,000): names, parents, depths, positions (UTF-8 byte offsets), visible texts and
  10 attribute values per tag must be identical.
- `conformance_query_test.go`: 569 queries, every string of the .NET XPath and CSS tests plus extra ones for the axes,
  functions and selectors. Whether each one compiles must be the same (119 are invalid); the 228 XPath and 222 CSS
  queries that compile are run on every page, on the document and from three tags of the page, and every CSS selector is
  also matched against every tag. Selected tags and values must be identical.

All pass, with one documented exception (`knownDifferences`): on the page nested 600 levels deep, where the .NET result
depends on the order of an unstable sort.

The expected output in `testdata/conformance/expected` is produced by `tools/conformance`: a small .NET program that
references the z3l.FastHtmlParser package; `make_inputs.py` and `make_queries.py` build the pages and the queries.

## Benchmarks

`cd bench && go test -bench . -benchmem` (Go 1.27, AMD Ryzen 7 3800X, Windows). Medians of 3 runs.
goquery parses with golang.org/x/net/html and selects with cascadia; antchfx/htmlquery runs XPath on x/net/html.

Parse a page:

| Page | fasthtml | x/net/html | Ratio | Memory fasthtml | x/net/html | Allocs fasthtml | x/net/html |
|---|---:|---:|---:|---:|---:|---:|---:|
| quotes, 11 KB | 28 μs | 299 μs | 10.6x | 9.5 KB | 96 KB | 2 | 2,498 |
| laptops, 38 KB | 89 μs | 1,001 μs | 11.2x | 24.7 KB | 302 KB | 2 | 7,886 |
| books, 51 KB | 98 μs | 981 μs | 10.0x | 33 KB | 287 KB | 2 | 6,707 |
| books x10, 510 KB | 1.00 ms | 9.31 ms | 9.3x | 321 KB | 2.82 MB | 2 | 66,895 |
| books without `</li>` `</p>` | 100 μs | 916 μs | 9.1x | 33 KB | 271 KB | 2 | 6,306 |

Scrape a page: parse and extract the records (`TestSameRecords*` check that every variant returns the same records).
Books: 20 × title, price, rating; quotes: 10 × text, author, tags.

| Task | fasthtml | Other | Ratio | Memory fasthtml | Other | Allocs fasthtml | Other |
|---|---:|---:|---:|---:|---:|---:|---:|
| books, `Find` / goquery | 122 μs | 1,089 μs | 8.9x | 37.8 KB | 307 KB | 15 | 7,391 |
| books, CSS / goquery | 128 μs | 1,080 μs | 8.4x | 39.8 KB | 307 KB | 136 | 7,391 |
| books, XPath / htmlquery | 260 μs | 1,171 μs | 4.5x | 128 KB | 362 KB | 1,943 | 9,158 |
| quotes, `Find` / goquery | 43 μs | 330 μs | 7.7x | 13.5 KB | 109 KB | 58 | 2,921 |
| quotes, CSS / goquery | 47 μs | 322 μs | 6.9x | 14.5 KB | 109 KB | 109 | 2,921 |

The XPath engine builds node-sets per step; that is where its allocations come from, and the first thing to optimize.

## Layout

- `parse.go`: the scanner and the HTML5 optional-end-tag rules (port of `ParsingCore.cs`)
- `attr.go`, `decode.go`, `text.go`: attributes, character references, visible text
- `document.go`: `Document`, `Node`, `Query`, search and navigation
- `css.go`: CSS selectors; `xpath.go`, `xpath_compile.go`: XPath 1.0
- `names_gen.go`, `entities_gen.go`: generated from the C# sources by `go run ./internal/gen <path to src/z3l.FastHtmlParser>`
- `bench/`: benchmarks in their own module, so the library has no dependencies
- `tools/conformance/`: builds the conformance data from the .NET version

## License

Same terms as z3l.FastHtmlParser (see LICENSE.md).
