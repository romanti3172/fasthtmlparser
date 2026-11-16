# z3l.FastHtmlParser.Go (prototype)

Go port of the core of [z3l.FastHtmlParser](https://github.com/romanti3172/z3l.FastHtmlParser): a fast, low-allocation
HTML / XML parser for scraping. Prototype: the parser, attributes, HTML5 entities, visible text, `Find` / `FindAll`
and basic navigation. Not ported yet: XPath, CSS selectors, `TextLineBreaks`, URL helpers, the `HtmlAttribute` enum.

```go
doc := fasthtml.Parse(page)
for _, pod := range doc.FindAll(fasthtml.By(fasthtml.TagArticle).Class("product_pod")) {
    title := pod.Find(fasthtml.By(fasthtml.TagH3)).Find(fasthtml.By(fasthtml.TagA)).Attr("title")
    price := pod.Find(fasthtml.By(fasthtml.TagP).Class("price_color")).Text()
}
```

Every `*Node` method accepts nil and returns nil / "" / false, so chains never panic on a missing tag.

## Same results as the .NET version

`conformance_test.go` compares the Go port with z3l.FastHtmlParser 1.4.0 (.NET) on 111 pages, 15,304 tags:
tag names, parents, depths, positions (UTF-8 byte offsets), visible texts and 10 attribute values per tag must be
identical. The pages are the test fixtures, the broken-markup snippets of the .NET cross-check tests, extra edge cases
(entities, raw text, comments, XML, custom elements, 600-deep nesting, Unicode whitespace), and every fixture with
`</li> </p> </td> </tr>` left out, plus three copies of each with random end tags dropped, names upper-cased and the page
cut off. All pass.

The expected output in `testdata/conformance/expected` is produced by `tools/conformance`: a small .NET program
that references the z3l.FastHtmlParser package and dumps every tag as JSON (`make_inputs.py` builds the input pages).

## Benchmarks

`cd bench && go test -bench . -benchmem` (Go 1.27, AMD Ryzen 7 3800X, Windows). Medians of 3 runs.
goquery parses with golang.org/x/net/html and selects with cascadia, so its parse numbers equal x/net/html's.

Parse a page:

| Page | fasthtml | x/net/html | Ratio | Memory fasthtml | x/net/html | Allocs fasthtml | x/net/html |
|---|---:|---:|---:|---:|---:|---:|---:|
| quotes, 11 KB | 28 μs | 299 μs | 10.6x | 9.5 KB | 96 KB | 2 | 2,498 |
| laptops, 38 KB | 89 μs | 1,001 μs | 11.2x | 24.7 KB | 302 KB | 2 | 7,886 |
| books, 51 KB | 98 μs | 981 μs | 10.0x | 33 KB | 287 KB | 2 | 6,707 |
| books x10, 510 KB | 1.00 ms | 9.31 ms | 9.3x | 321 KB | 2.82 MB | 2 | 66,895 |
| books without `</li>` `</p>` | 100 μs | 916 μs | 9.1x | 33 KB | 271 KB | 2 | 6,306 |

Scrape a page (parse and extract; `TestSameRecords` checks that both return the same records):

| Task | fasthtml | goquery | Ratio | Memory fasthtml | goquery | Allocs fasthtml | goquery |
|---|---:|---:|---:|---:|---:|---:|---:|
| books: 20 × title, price, rating | 125 μs | 1,080 μs | 8.6x | 37.8 KB | 307 KB | 15 | 7,391 |
| quotes: 10 × text, author, tags | 45 μs | 335 μs | 7.4x | 13.5 KB | 109 KB | 58 | 2,921 |

## Layout

- `parse.go`: the scanner and the HTML5 optional-end-tag rules (port of `ParsingCore.cs`)
- `attr.go`, `decode.go`, `text.go`: attributes, character references, visible text
- `document.go`: `Document`, `Node`, `Query`, search and navigation
- `names_gen.go`, `entities_gen.go`: generated from the C# sources by `go run ./internal/gen <path to src/z3l.FastHtmlParser>`
- `bench/`: benchmarks in their own module, so the library has no dependencies

## License

Same terms as z3l.FastHtmlParser (see its LICENSE.md).
