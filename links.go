package fasthtmlparser

import (
	"net/url"
	"strings"
	"unicode/utf8"
)

// Href returns the link a browser follows: the href attribute decoded and made absolute against the page URL
// (see ParseWithURL). An empty or missing href gives the page URL, as in a browser.
func (n *Node) Href() string {
	if n == nil {
		return ""
	}
	return correctLink(n.attrRaw(AttrHref), n.doc.url, n.doc.base)
}

// HrefFrom is Href resolved against baseURL instead of the page URL.
func (n *Node) HrefFrom(baseURL string) string {
	if n == nil {
		return ""
	}
	return correctLink(n.attrRaw(AttrHref), baseURL, absoluteURL(baseURL))
}

// Src returns the src attribute decoded and made absolute against the page URL.
func (n *Node) Src() string {
	if n == nil {
		return ""
	}
	return correctLink(n.attrRaw(AttrSrc), n.doc.url, n.doc.base)
}

// SrcFrom is Src resolved against baseURL instead of the page URL.
func (n *Node) SrcFrom(baseURL string) string {
	if n == nil {
		return ""
	}
	return correctLink(n.attrRaw(AttrSrc), baseURL, absoluteURL(baseURL))
}

// HrefMailTo returns the address of a mailto: link (everything after the first ':'), or "" for other links.
func (n *Node) HrefMailTo() string { return n.hrefAfterScheme("mailto") }

// HrefTelephone returns the number of a tel: link (everything after the first ':'), or "" for other links.
func (n *Node) HrefTelephone() string { return n.hrefAfterScheme("tel") }

func (n *Node) hrefAfterScheme(scheme string) string {
	href := strings.TrimSpace(decodeHTML(n.attrRaw(AttrHref), true))
	if len(href) < len(scheme) || !strings.EqualFold(href[:len(scheme)], scheme) {
		return ""
	}
	if i := strings.IndexByte(href, ':'); i >= 0 {
		return href[i+1:]
	}
	return ""
}

// correctLink: the raw attribute value with its character references decoded and made absolute against
// the page. Percent-escapes are part of the URL and are kept. An empty link gives the page URL.
func correctLink(raw, pageURL string, base *url.URL) string {
	if raw == "" {
		return pageURL
	}
	link := decodeHTML(raw, true)
	if base == nil || isAbsoluteLink(link) {
		return link
	}
	ref, err := url.Parse(strings.TrimSpace(link))
	if err != nil {
		return link
	}
	return displayString(base.ResolveReference(ref))
}

// displayString writes a resolved URL as the .NET version does (System.Uri.ToString): the scheme and host in lower
// case, no default port, an empty path as "/", and percent-escapes decoded except those of the characters that
// delimit URL parts ("%2F" stays) and of control characters; non-ASCII text is written as is.
func displayString(u *url.URL) string {
	var b strings.Builder
	b.WriteString(strings.ToLower(u.Scheme))
	b.WriteByte(':')
	if u.Opaque != "" {
		b.WriteString(u.Opaque)
	} else {
		if u.Host != "" || u.User != nil {
			b.WriteString("//")
			if u.User != nil {
				b.WriteString(u.User.String())
				b.WriteByte('@')
			}
			b.WriteString(strings.ToLower(u.Hostname()))
			if port := u.Port(); port != "" && port != defaultPorts[strings.ToLower(u.Scheme)] {
				b.WriteByte(':')
				b.WriteString(port)
			}
		}
		path := u.EscapedPath()
		if path == "" && u.Host != "" {
			path = "/"
		}
		b.WriteString(unescapeForDisplay(path))
	}
	if u.RawQuery != "" || u.ForceQuery {
		b.WriteByte('?')
		b.WriteString(unescapeForDisplay(u.RawQuery))
	}
	if u.Fragment != "" {
		b.WriteByte('#')
		b.WriteString(unescapeForDisplay(u.EscapedFragment()))
	}
	return b.String()
}

var defaultPorts = map[string]string{"http": "80", "https": "443", "ftp": "21", "ws": "80", "wss": "443"}

// unescapeForDisplay decodes %XX escapes except those of reserved and control characters;
// escaped bytes of non-ASCII text are decoded when they form valid UTF-8.
func unescapeForDisplay(s string) string {
	if strings.IndexByte(s, '%') < 0 {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '%' || i+2 >= len(s) || !isHex(s[i+1]) || !isHex(s[i+2]) {
			b.WriteByte(s[i])
			i++
			continue
		}
		c := unhex(s[i+1])<<4 | unhex(s[i+2])
		if c < 0x80 {
			if c < 0x20 || c == 0x7F || strings.IndexByte(`%/?#[]@:!$&'()*+,;=`, c) >= 0 {
				b.WriteString(s[i : i+3])
			} else {
				b.WriteByte(c)
			}
			i += 3
			continue
		}
		// a run of escaped bytes >= 0x80: decoded together when it is valid UTF-8
		j := i
		var run []byte
		for j+2 < len(s) && s[j] == '%' && isHex(s[j+1]) && isHex(s[j+2]) {
			v := unhex(s[j+1])<<4 | unhex(s[j+2])
			if v < 0x80 {
				break
			}
			run = append(run, v)
			j += 3
		}
		if utf8.Valid(run) {
			b.Write(run)
		} else {
			b.WriteString(s[i:j])
		}
		i = j
	}
	return b.String()
}

func unhex(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	}
	return c - 'A' + 10
}

// absoluteURL parses a page URL with its own scheme and host; nil for anything else.
func absoluteURL(s string) *url.URL {
	if s == "" {
		return nil
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" || u.Host == "" && u.Opaque == "" && !strings.EqualFold(u.Scheme, "file") {
		return nil
	}
	return u
}

// isAbsoluteLink: a link with its own scheme ("https://…", "mailto:…", "file:…"). "/path" and "//host/path"
// are relative to the page.
func isAbsoluteLink(link string) bool {
	u, err := url.Parse(strings.TrimSpace(link))
	return err == nil && u.Scheme != ""
}
