package fasthtmlparser

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// TextLineBreaks returns the text of the tag with the visible line structure kept:
//   - <br> → new line (each one counts: <br><br> gives a blank line);
//   - p, h1–h6 → new paragraph (blank line);
//   - other block elements (div, li, tr, dt/dd, blockquote, ...) → new line; td/th → space;
//   - other tags are removed without splitting words; script/style/noscript contents and comments are dropped;
//   - source whitespace, including newlines, is collapsed to one space (as a browser does), lines are trimmed,
//     at most one blank line in a row; entities are decoded (&nbsp; counts as whitespace).
//
// Lines are joined with "\n" (the .NET version uses Environment.NewLine).
func (n *Node) TextLineBreaks() string {
	if n == nil || n.innerStart < 0 || n.innerEnd <= 0 {
		return ""
	}
	return clearTagsLineBreaks(n.Body())
}

// HTMLToText returns the visible text of an HTML fragment, as Node.Text does for a tag's content.
func HTMLToText(html string) string { return clearTags(html) }

// HTMLToTextLineBreaks returns the text of an HTML fragment with the visible line structure kept,
// as Node.TextLineBreaks does for a tag's content.
func HTMLToTextLineBreaks(html string) string { return clearTagsLineBreaks(html) }

type lineBreakKind uint8

const (
	breakNone        lineBreakKind = iota // inline tag (b, span, a, ...): removed, words are not split
	breakSpace                            // table cell: word separator
	breakLine                             // <br> and block elements (div, li, tr, ...): new line
	breakParagraph                        // p, h1-h6: new paragraph (blank line)
	breakSkipContent                      // script, style, noscript: contents are not text
)

var lineBreakKinds = func() (kinds [tagCount]lineBreakKind) {
	for _, t := range []Tag{TagP, TagH1, TagH2, TagH3, TagH4, TagH5, TagH6} {
		kinds[t] = breakParagraph
	}
	for _, t := range []Tag{
		TagBr, TagDiv, TagLi, TagUl, TagOl, TagDl, TagDt, TagDd,
		TagTable, TagCaption, TagTr, TagThead, TagTbody, TagTfoot,
		TagSection, TagArticle, TagAside, TagNav, TagHeader, TagFooter, TagMain,
		TagAddress, TagBlockquote, TagPre, TagHr, TagFigure, TagFigcaption,
		TagForm, TagFieldset, TagLegend, TagDetails, TagSummary, TagDialog,
		TagOption, TagHtml, TagHead, TagBody, TagTitle,
		TagHgroup, TagSearch, TagMenu, TagCenter, TagDir, TagListing,
		TagXmp, TagPlaintext, TagMulticol, TagFrameset, TagMarquee,
	} {
		kinds[t] = breakLine
	}
	kinds[TagTd], kinds[TagTh] = breakSpace, breakSpace
	for _, t := range []Tag{TagScript, TagStyle, TagNoscript, TagTemplate, TagNoframes, TagNoembed} {
		kinds[t] = breakSkipContent
	}
	return
}()

// lineWriter writes text, collapsing whitespace and turning requested breaks into trimmed lines.
type lineWriter struct {
	out           []byte
	pendingBreaks int // new lines requested since the last text char
	pendingSpace  bool
}

func (w *lineWriter) char(r rune) {
	if unicode.IsSpace(r) {
		w.pendingSpace = true
		return
	}
	if len(w.out) > 0 { // nothing before the first text char: leading breaks and spaces are trimmed
		if w.pendingBreaks > 0 {
			for k := min(w.pendingBreaks, 2); k > 0; k-- { // at most one blank line
				w.out = append(w.out, '\n')
			}
		} else if w.pendingSpace {
			w.out = append(w.out, ' ')
		}
	}
	w.pendingBreaks = 0
	w.pendingSpace = false
	w.out = utf8.AppendRune(w.out, r)
}

// lineBreak: block boundary, at least count new lines before the next text.
func (w *lineWriter) lineBreak(count int) { w.pendingBreaks = max(w.pendingBreaks, count) }

func clearTagsLineBreaks(value string) string {
	if value == "" {
		return ""
	}
	if strings.IndexByte(value, '\r') >= 0 {
		value = normalizeNewlines(value) // values are read as a browser reads the page
	}
	w := &lineWriter{out: make([]byte, 0, len(value))}
	for i := 0; i < len(value); {
		c := value[i]

		if c == '&' {
			if consumed, decoded := decodeReference(value[i+1:]); consumed > 0 {
				// decoded chars go through the same whitespace/text logic (&nbsp; collapses, &lt; is plain text)
				for _, r := range decoded {
					w.char(r)
				}
				i += 1 + consumed
				continue
			}
			w.char('&')
			i++
			continue
		}

		if c != '<' || !isMarkupStart(value, i) {
			r, size := utf8.DecodeRuneInString(value[i:])
			w.char(r)
			i += size
			continue
		}

		rest := value[i:]
		if strings.HasPrefix(rest, "<!--") {
			end := strings.Index(rest[4:], "-->")
			if end < 0 {
				break
			}
			i += 4 + end + 3
			continue
		}

		closeTag := strings.IndexByte(rest, '>')
		if closeTag < 0 {
			break // unterminated tag: the rest is markup
		}
		tag := rest[1:closeTag]
		i += closeTag + 1

		closing := tag != "" && tag[0] == '/'
		name := tag
		if closing {
			name = tag[1:]
		}
		if k := strings.IndexAny(name, " \t\r\n\f/"); k >= 0 {
			name = name[:k]
		}
		known, ok := lookupTag(name)
		if !ok {
			continue // unknown tag: just removed
		}

		switch lineBreakKinds[known] {
		case breakLine:
			if known == TagBr {
				w.pendingBreaks++ // every <br> counts
			} else {
				w.lineBreak(1)
			}
		case breakParagraph:
			w.lineBreak(2)
		case breakSpace:
			w.char(' ')
		case breakSkipContent:
			if t := strings.TrimRight(tag, asciiSpace); !closing && !strings.HasSuffix(t, "/") {
				// jump to the closing tag; it is removed on the next iteration
				end := indexFold(value[i:], "</"+tagNames[known])
				if end < 0 {
					i = len(value)
				} else {
					i += end
				}
			}
		}
	}
	return string(w.out)
}

// decodeReference decodes one character reference; s is the text right after '&'.
// It returns the number of bytes consumed after '&' (0: not a reference) and the decoded text.
func decodeReference(s string) (int, string) {
	if s != "" && s[0] == '#' {
		consumed, r := matchNumeric(s)
		if consumed == 0 {
			return 0, ""
		}
		return consumed, string(r)
	}
	return matchNamed(s, false)
}
