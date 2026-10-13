package parser

import (
	"strings"
	"testing"
)

func TestRSSAuthorSourcesAndStructuredNames(t *testing.T) {
	for _, tc := range []struct{ name, tags, want string }{
		{"creator priority", `<author>plain</author><dc:author>other</dc:author><dc:creator>creator</dc:creator>`, "creator"},
		{"reordered priority", `<dc:creator>creator</dc:creator><dc:author>other</dc:author><author>plain</author>`, "creator"},
		{"plain priority", `<dc:creator> </dc:creator><dc:author>other</dc:author><author>plain</author>`, "plain"},
		{"DC author fallback", `<dc:author>other</dc:author>`, "other"},
		{"different prefix", `<other:creator>same URI</other:creator>`, "same URI"},
		{"unknown namespace", `<unknown:author>ignored</unknown:author>`, ""},
		{"structured plain", `<author><name>henrybit</name><uri>https://www.v2ex.com/member/henrybit</uri></author>`, "henrybit"},
		{"structured creator", `<dc:creator>prefix<profile><name>Henry <b>Bit</b></name></profile><uri>ignored</uri></dc:creator>`, "Henry Bit"},
		{"structured DC author", `<dc:author><name>Henry</name><uri>ignored</uri></dc:author>`, "Henry"},
		{"empty name then name", `<author><name> </name><name>second</name><name>third</name></author>`, "second"},
		{"mixed fallback", `<author>before <b>bold</b> after</author>`, "before bold after"},
		{"blank name fallback", `<author><name> </name><label>fallback</label></author>`, "fallback"},
		{"entities", `<author>A &amp; B &lt;literal&gt;</author>`, "A & B <literal>"},
		{"one decode", `<author>&amp;lt;literal&amp;gt;</author>`, "&lt;literal&gt;"},
		{"CDATA", `<author><![CDATA[A <B> C]]></author>`, "A <B> C"},
		{"comments and attributes", `<author hidden="not text"><!-- ignored --><b> Henry </b> Bit </author>`, "Henry Bit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := `<rss version="2.0" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:other="http://purl.org/dc/elements/1.1/" xmlns:unknown="https://example.com/unknown"><channel><title>Feed</title><item><guid>1</guid><title>Title</title>` + tc.tags + `</item></channel></rss>`
			feed, err := Parse(strings.NewReader(text))
			if err != nil {
				t.Fatal(err)
			}
			if got := feed.Items[0].Author; got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
func TestAtomAuthorSelection(t *testing.T) {
	for _, tc := range []struct{ name, feed, entry, want string }{
		{"entry", `<author><name>feed</name></author>`, `<author><name>henrybit</name><uri>ignored</uri></author>`, "henrybit"},
		{"feed fallback", `<author><name>feed</name></author>`, "", "feed"},
		{"empty entry fallback", `<author><name>feed</name></author>`, `<author> </author>`, "feed"},
		{"multiple entry", `<author><name>feed</name></author>`, `<author> </author><author><name>first</name></author><author><name>second</name></author>`, "first"},
		{"multiple feed", `<author> </author><author><name>first</name></author><author><name>second</name></author>`, "", "first"},
		{"fallback text", "", `<author><profile>Henry <b>Bit</b></profile></author>`, "Henry Bit"},
		{"CDATA name", "", `<author><name><![CDATA[A <B> C]]></name><uri>ignored</uri></author>`, "A <B> C"},
		{"wrong namespace", `<other:author><name>ignored</name></other:author>`, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			xml := `<feed xmlns="http://www.w3.org/2005/Atom" xmlns:other="https://example.com/other"><title>Feed</title>` + tc.feed + `<entry><id>1</id><title>Title</title>` + tc.entry + `</entry></feed>`
			feed, err := Parse(strings.NewReader(xml))
			if err != nil {
				t.Fatal(err)
			}
			if got := feed.Items[0].Author; got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
