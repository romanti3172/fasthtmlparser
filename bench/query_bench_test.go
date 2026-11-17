package bench

import (
	"slices"
	"strings"
	"testing"

	"github.com/antchfx/htmlquery"
	fh "github.com/romanti3172/fasthtmlparser"
)

// The same tasks as BenchmarkScrape, with XPath (fasthtmlparser and antchfx/htmlquery, XPath on x/net/html)
// and CSS selectors (fasthtmlparser and goquery). Queries are compiled once, as a scraper would.

var (
	podsXPath   = fh.MustCompileXPath("//article[contains(concat(' ', @class, ' '), ' product_pod ')]")
	titleXPath  = fh.MustCompileXPath(".//h3/a/@title")
	priceXPath  = fh.MustCompileXPath(".//p[contains(concat(' ', @class, ' '), ' price_color ')]")
	ratingXPath = fh.MustCompileXPath(".//p[contains(concat(' ', @class, ' '), ' star-rating ')]/@class")

	podsCSS   = fh.MustCompileCSS("article.product_pod")
	titleCSS  = fh.MustCompileCSS("h3 a")
	priceCSS  = fh.MustCompileCSS("p.price_color")
	ratingCSS = fh.MustCompileCSS("p.star-rating")

	quoteCSS  = fh.MustCompileCSS("div.quote")
	textCSS   = fh.MustCompileCSS("span.text")
	authorCSS = fh.MustCompileCSS("small.author")
	tagCSS    = fh.MustCompileCSS("a.tag")
)

func value(q *fh.XPathQuery, n *fh.Node) string {
	v, _ := q.ValueFrom(n)
	return v
}

func booksFastXPath(page string) []book {
	var result []book
	for _, pod := range podsXPath.Nodes(fh.Parse(page)) {
		result = append(result, book{value(titleXPath, pod), value(priceXPath, pod), lastWord(value(ratingXPath, pod))})
	}
	return result
}

func booksFastCSS(page string) []book {
	var result []book
	for _, pod := range podsCSS.All(fh.Parse(page)) {
		result = append(result, book{
			titleCSS.FirstIn(pod).Attr("title"),
			priceCSS.FirstIn(pod).Text(),
			lastWord(ratingCSS.FirstIn(pod).Attr("class")),
		})
	}
	return result
}

func booksHtmlquery(page string) []book {
	doc, err := htmlquery.Parse(strings.NewReader(page))
	if err != nil {
		panic(err)
	}
	var result []book
	for _, pod := range htmlquery.Find(doc, "//article[contains(concat(' ', @class, ' '), ' product_pod ')]") {
		title := htmlquery.SelectAttr(htmlquery.FindOne(pod, ".//h3/a"), "title")
		price := strings.TrimSpace(htmlquery.InnerText(htmlquery.FindOne(pod, ".//p[contains(concat(' ', @class, ' '), ' price_color ')]")))
		rating := htmlquery.SelectAttr(htmlquery.FindOne(pod, ".//p[contains(concat(' ', @class, ' '), ' star-rating ')]"), "class")
		result = append(result, book{title, price, lastWord(rating)})
	}
	return result
}

func quotesFastCSS(page string) []quote {
	var result []quote
	for _, q := range quoteCSS.All(fh.Parse(page)) {
		var tags []string
		for _, a := range tagCSS.AllIn(q) {
			tags = append(tags, a.Text())
		}
		result = append(result, quote{textCSS.FirstIn(q).Text(), authorCSS.FirstIn(q).Text(), strings.Join(tags, ",")})
	}
	return result
}

func TestSameRecordsQueries(t *testing.T) {
	want := booksGoquery(books)
	for name, got := range map[string][]book{
		"fasthtmlparser XPath": booksFastXPath(books),
		"fasthtmlparser CSS":   booksFastCSS(books),
		"htmlquery":            booksHtmlquery(books),
	} {
		if len(got) != 20 || !slices.Equal(got, want) {
			t.Fatalf("%s: books differ:\n%v\n%v", name, got, want)
		}
	}
	if got, want := quotesFastCSS(quotes), quotesGoquery(quotes); len(got) != 10 || !slices.Equal(got, want) {
		t.Fatalf("quotes differ:\n%v\n%v", got, want)
	}
}

func BenchmarkScrapeQueries(b *testing.B) {
	for _, c := range []struct {
		name string
		run  func()
	}{
		{"books/fasthtml_xpath", func() { booksFastXPath(books) }},
		{"books/htmlquery_xpath", func() { booksHtmlquery(books) }},
		{"books/fasthtml_css", func() { booksFastCSS(books) }},
		{"books/goquery_css", func() { booksGoquery(books) }},
		{"quotes/fasthtml_css", func() { quotesFastCSS(quotes) }},
		{"quotes/goquery_css", func() { quotesGoquery(quotes) }},
	} {
		b.Run(c.name, func(b *testing.B) {
			for b.Loop() {
				c.run()
			}
		})
	}
}
