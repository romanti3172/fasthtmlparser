package fasthtmlparser

import (
	"math/rand"
	"strings"
	"testing"
	"unsafe"
)

func indexTagCharNaive(s string) int { return strings.IndexAny(s, "<=>") }

func TestIndexTagChar(t *testing.T) {
	alphabet := []byte{'a', ' ', '"', '<', '=', '>', ';', '<' - 1, '>' + 1, 0x80, 0xBC, 0xFF, 0x00}
	r := rand.New(rand.NewSource(1))
	for n := 0; n < 200000; n++ {
		b := make([]byte, r.Intn(40))
		for i := range b {
			if r.Intn(6) == 0 {
				b[i] = alphabet[r.Intn(len(alphabet))]
			} else {
				b[i] = byte('a' + r.Intn(26))
			}
		}
		s := string(b)
		if got, want := indexTagChar(s), indexTagCharNaive(s); got != want {
			t.Fatalf("indexTagChar(%q) = %d, want %d", s, got, want)
		}
	}
}

func FuzzIndexTagChar(f *testing.F) {
	f.Add(`a href="x" class=y>`)
	f.Fuzz(func(t *testing.T, s string) {
		if got, want := indexTagChar(s), indexTagCharNaive(s); got != want {
			t.Fatalf("indexTagChar(%q) = %d, want %d", s, got, want)
		}
	})
}

func TestLookupTag(t *testing.T) {
	for i, name := range tagNames {
		for _, s := range []string{name, strings.ToUpper(name)} {
			if got, ok := lookupTag(s); !ok || got != Tag(i) {
				t.Fatalf("lookupTag(%q) = %v, %v; want %v", s, got, ok, Tag(i))
			}
		}
	}
	for _, s := range []string{"", "x", "divv", "di", "custom-tag", "dİv", strings.Repeat("a", 33), "\x00"} {
		if got, ok := lookupTag(s); ok {
			t.Fatalf("lookupTag(%q) = %v, want not found", s, got)
		}
	}
}

func TestNodeSize(t *testing.T) {
	if size := unsafe.Sizeof(Node{}); size != 40 {
		t.Fatalf("Node is %d bytes, want 40", size)
	}
}

// The tag list is sized by counting '<' and "</": a page must never produce more tags than that.
func FuzzParse(f *testing.F) {
	for _, s := range []string{`<a href="x">t</a>`, `<li>a<li>b</ul>`, `<<<</</a<b</`, `<!--<a>--><p>x</p`, `<?xml ?><r><x/></r>`, `a < b </ c> <x`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		doc := Parse(s)
		for i := 0; i < doc.Len(); i++ {
			_ = doc.Node(i).Name()
		}
	})
}
