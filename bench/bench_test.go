// Benchmarks of the Go port against golang.org/x/net/html and goquery (x/net/html + cascadia CSS selectors)
// on the same pages as the .NET benchmarks.
//
//	cd bench && go test -bench . -benchmem
package bench

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	fh "github.com/romanti3172/fasthtmlparser"
	"golang.org/x/net/html"
)

func read(name string) string {
	data, err := os.ReadFile("../testdata/" + name)
	if err != nil {
		panic(err)
	}
	return string(data)
}

var (
	quotes  = read("quotes/page1.html")
	laptops = read("webscraper/laptops.html")
	books   = read("books/index.html")
	pages   = []struct{ name, html string }{
		{"quotes_11KB", quotes},
		{"laptops_38KB", laptops},
		{"books_51KB", books},
		{"books_x10_510KB", strings.Repeat(books, 10)},
		{"books_no_end_tags", strings.NewReplacer("</li>", "", "</p>", "").Replace(books)},
	}
)

// ----- parse a page -----

func BenchmarkParse(b *testing.B) {
	for _, p := range pages {
		b.Run(p.name+"/fasthtmlparser", func(b *testing.B) {
			b.SetBytes(int64(len(p.html)))
			for b.Loop() {
				fh.Parse(p.html)
			}
		})
		b.Run(p.name+"/x-net-html", func(b *testing.B) {
			b.SetBytes(int64(len(p.html)))
			for b.Loop() {
				if _, err := html.Parse(strings.NewReader(p.html)); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(p.name+"/goquery", func(b *testing.B) {
			b.SetBytes(int64(len(p.html)))
			for b.Loop() {
				if _, err := goquery.NewDocumentFromReader(strings.NewReader(p.html)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// ----- scrape: parse and extract the records -----

type book struct{ title, price, rating string }
type quote struct{ text, author, tags string }

func lastWord(s string) string { return s[strings.LastIndexByte(s, ' ')+1:] }

func booksFast(page string) []book {
	var result []book
	for _, pod := range fh.Parse(page).FindAll(fh.By(fh.TagArticle).Class("product_pod")) {
		result = append(result, book{
			pod.Find(fh.By(fh.TagH3)).Find(fh.By(fh.TagA)).Attr("title"),
			pod.Find(fh.By(fh.TagP).Class("price_color")).Text(),
			lastWord(pod.Find(fh.By(fh.TagP).Class("star-rating")).Attr("class")),
		})
	}
	return result
}

func booksGoquery(page string) []book {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(page))
	if err != nil {
		panic(err)
	}
	var result []book
	doc.Find("article.product_pod").Each(func(_ int, pod *goquery.Selection) {
		title, _ := pod.Find("h3 a").Attr("title")
		rating, _ := pod.Find("p.star-rating").Attr("class")
		result = append(result, book{title, strings.TrimSpace(pod.Find("p.price_color").Text()), lastWord(rating)})
	})
	return result
}

func quotesFast(page string) []quote {
	var result []quote
	for _, q := range fh.Parse(page).FindAll(fh.By(fh.TagDiv).Class("quote")) {
		var tags []string
		for _, a := range q.FindAll(fh.By(fh.TagA).Class("tag")) {
			tags = append(tags, a.Text())
		}
		result = append(result, quote{
			q.Find(fh.By(fh.TagSpan).Class("text")).Text(),
			q.Find(fh.By(fh.TagSmall).Class("author")).Text(),
			strings.Join(tags, ","),
		})
	}
	return result
}

func quotesGoquery(page string) []quote {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(page))
	if err != nil {
		panic(err)
	}
	var result []quote
	doc.Find("div.quote").Each(func(_ int, q *goquery.Selection) {
		tags := q.Find("a.tag").Map(func(_ int, a *goquery.Selection) string { return strings.TrimSpace(a.Text()) })
		result = append(result, quote{
			strings.TrimSpace(q.Find("span.text").Text()),
			strings.TrimSpace(q.Find("small.author").Text()),
			strings.Join(tags, ","),
		})
	})
	return result
}

// TestSameRecords checks that both libraries extract exactly the same data.
func TestSameRecords(t *testing.T) {
	if a, b := booksFast(books), booksGoquery(books); len(a) != 20 || !slices.Equal(a, b) {
		t.Fatalf("books differ:\n%v\n%v", a, b)
	}
	if a, b := quotesFast(quotes), quotesGoquery(quotes); len(a) != 10 || !slices.Equal(a, b) {
		t.Fatalf("quotes differ:\n%v\n%v", a, b)
	}
	fmt.Println(booksFast(books)[0], quotesFast(quotes)[0].author)
}

func BenchmarkScrape(b *testing.B) {
	b.Run("books/fasthtmlparser", func(b *testing.B) {
		for b.Loop() {
			booksFast(books)
		}
	})
	b.Run("books/goquery", func(b *testing.B) {
		for b.Loop() {
			booksGoquery(books)
		}
	})
	b.Run("quotes/fasthtmlparser", func(b *testing.B) {
		for b.Loop() {
			quotesFast(quotes)
		}
	})
	b.Run("quotes/goquery", func(b *testing.B) {
		for b.Loop() {
			quotesGoquery(quotes)
		}
	})
}
