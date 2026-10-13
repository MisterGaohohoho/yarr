package server

import (
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/nkanaev/yarr/src/parser"
	"github.com/nkanaev/yarr/src/worker"
)

func TestArticleAPIsIncludeAuthor(t *testing.T) {
	db := testServerDB(t)
	feed := db.CreateFeed("Feed", "", "", "https://example.com/feed", nil)
	parsed, err := parser.Parse(strings.NewReader(`<rss version="2.0" xmlns:dc="http://purl.org/dc/elements/1.1/"><channel><title>Feed</title><item><guid>author-test</guid><title>Title</title><dc:creator><name>henrybit</name><uri>ignored</uri></dc:creator></item></channel></rss>`))
	if err != nil {
		t.Fatal(err)
	}
	if !db.CreateItems(worker.ConvertItems(parsed.Items, *feed)) {
		t.Fatal("store failed")
	}
	item := db.GetItemByGUID(feed.Id, "author-test")
	handler := NewServer(db, "").handler()
	for _, path := range []string{"/api/items", "/api/items/" + strconv.FormatInt(item.Id, 10)} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
		var body map[string]interface{}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if path == "/api/items" {
			body = body["list"].([]interface{})[0].(map[string]interface{})
		}
		if body["author"] != "henrybit" || body["title"] != "Title" {
			t.Fatal(body)
		}
	}
}
