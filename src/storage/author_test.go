package storage

import (
	"testing"
	"time"
)

func TestAuthorPersistenceAndRefresh(t *testing.T) {
	db := testDB()
	feed := db.CreateFeed("Feed", "", "", "https://example.com/feed", nil)
	original := Item{GUID: "article", FeedId: feed.Id, Title: "Original title", Date: time.Now().UTC()}
	if !db.CreateItems([]Item{original}) {
		t.Fatal("create failed")
	}
	item := db.GetItemByGUID(feed.Id, original.GUID)
	if _, err := db.db.Exec(`UPDATE items SET author=NULL WHERE id=?`, item.Id); err != nil {
		t.Fatal(err)
	}
	if got := db.GetItem(item.Id); got == nil || got.Author != "" {
		t.Fatal("NULL author must be readable")
	}
	original.Author = "  Henry  "
	if !db.CreateItems([]Item{original}) {
		t.Fatal("backfill failed")
	}
	db.UpdateItemStatus(item.Id, READ)
	db.UpdateItemFavorite(item.Id, true)
	original.Author = "Henry renamed"
	created, ok := db.ApplyFeedRefresh(FeedRefreshUpdate{FeedID: feed.Id, Items: []Item{original}})
	if !ok || created != 0 {
		t.Fatal("author update should not insert article")
	}
	original.Author = " \n "
	if !db.CreateItems([]Item{original}) {
		t.Fatal("empty refresh failed")
	}
	for _, got := range []*Item{db.GetItem(item.Id), db.GetItemByGUID(feed.Id, original.GUID)} {
		if got == nil || got.Author != "Henry renamed" || got.Title != "Original title" || got.Status != READ || !got.Favorite {
			t.Fatal(got)
		}
	}
	for _, withContent := range []bool{false, true} {
		list := db.ListItems(ItemFilter{}, 10, true, withContent)
		if len(list) != 1 || list[0].Author != "Henry renamed" {
			t.Fatal(list)
		}
		list = db.ListItemsOrdered(ItemFilter{}, 10, true, true, withContent)
		if len(list) != 1 || list[0].Author != "Henry renamed" {
			t.Fatal(list)
		}
	}
}
