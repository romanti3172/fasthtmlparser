package fasthtml

import (
	"os"
	"testing"
)

// The shortcuts on the books page give what the scraper code of the .NET examples expects.
func TestShortcuts(t *testing.T) {
	page, err := os.ReadFile("testdata/books/index.html")
	if err != nil {
		t.Fatal(err)
	}
	doc := ParseWithURL(string(page), "https://books.toscrape.com/index.html")

	pods := doc.FindAllByNameClass(TagArticle, "product_pod")
	if len(pods) != 20 {
		t.Fatalf("pods: %d", len(pods))
	}
	first := pods[0]
	checks := []struct{ name, got, want string }{
		{"title", first.FindByName(TagH3).FindByName(TagA).AttributeTitle(), "A Light in the Attic"},
		{"price", first.FindByNameClass(TagP, "price_color").Text(), "£51.77"},
		{"rating class", first.FindByNameClassTextStartsWith(TagP, "price_color", "£5").AttributeClass(), "price_color"},
		{"href", first.FindByName(TagA).Href(), "https://books.toscrape.com/catalogue/a-light-in-the-attic_1000/index.html"},
		{"img src", first.FindByName(TagImg).Src(), "https://books.toscrape.com/media/cache/2c/da/2cdad67c44b002e7ead0cc35693c0e8b.jpg"},
		{"parent", first.FindByName(TagImg).ParentByName(TagArticle).AttributeClass(), "product_pod"},
		{"next sibling", first.FindByName(TagH3).NextSiblingByName(TagDiv).AttributeClass(), "product_price"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: %q, want %q", c.name, c.got, c.want)
		}
	}

	if n := len(FirstLevelByName(doc.FindAll(Any()), TagHtml)); n != 1 {
		t.Errorf("first level html: %d", n)
	}
	if !first.IsNameClass(TagArticle, "product_pod") || first.IsNameClass(TagDiv, "product_pod") {
		t.Error("IsNameClass")
	}
	if doc.FindByNameTextStartsWith(TagA, "") != nil || doc.FindByID("") != nil {
		t.Error("empty criteria of the older shortcuts must give nil")
	}
	var missing *Node
	if missing.FindByNameClass(TagP, "x") != nil || missing.AttributeTitle() != "" || missing.Href() != "" {
		t.Error("a nil node must give nil and empty values")
	}
}
