package worker

import (
	"strings"
	"testing"

	"github.com/nkanaev/yarr/src/parser"
	"github.com/nkanaev/yarr/src/storage"
)

func TestConvertItemsPreservesParsedAuthor(t *testing.T) {
	for _, xml := range []string{
		`<rss version="2.0" xmlns:dc="http://purl.org/dc/elements/1.1/"><channel><title>Feed</title><item><guid>1</guid><title>Original</title><dc:creator>Henry</dc:creator></item></channel></rss>`,
		`<feed xmlns="http://www.w3.org/2005/Atom"><title>Feed</title><entry><id>1</id><title>Original</title><author><name>Henry</name><uri>ignored</uri></author></entry></feed>`,
	} {
		feed, err := parser.Parse(strings.NewReader(xml))
		if err != nil {
			t.Fatal(err)
		}
		items := ConvertItems(feed.Items, storage.Feed{Id: 42})
		if len(items) != 1 || items[0].Author != "Henry" || items[0].Title != "Original" || items[0].FeedId != 42 {
			t.Fatal(items)
		}
	}
}
