package fasthtml

import "testing"

// CR LF and a lone CR are LF in values, as a browser reads the page (a page saved on Windows);
// a CR written as a character reference stays.
func TestLineBreaksAreReadAsInABrowser(t *testing.T) {
	doc := Parse("<meta name=\"d\" content=\"one\r\ntwo\rthree&#13;four\">" +
		"<textarea>a\r\nb</textarea><pre>c\r\nd</pre><div class=\"x\r\ny\">t</div>")
	div := doc.Find(By(TagDiv))

	checks := []struct{ name, got, want string }{
		{"meta content", doc.Find(By(TagMeta)).Attr("content"), "one\ntwo\nthree\rfour"},
		{"textarea", doc.Find(By(TagTextarea)).Text(), "a\nb"},
		{"pre", doc.Find(By(TagPre)).Text(), "c\nd"},
		{"xpath text()", doc.XPathValue("//textarea/text()"), "a\nb"},
		{"xpath @content", doc.XPathValue("//meta/@content"), "one\ntwo\nthree\rfour"},
		{"raw attribute", div.AttrRaw("class"), "x\r\ny"}, // the source as written
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: %q, want %q", c.name, c.got, c.want)
		}
	}
	if !div.HasClass("y") || !div.Is(Any().Attr("class", "x\ny")) || len(doc.CSS(`div[class="x\a y"]`)) != 1 {
		t.Error("class with CR LF does not match")
	}
}
