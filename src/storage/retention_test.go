package storage

import (
	"fmt"
	"testing"
	"time"
)

func TestDeleteOldItemsConfiguredLimit(t *testing.T) {
	db := testDB()
	if !db.UpdateSettings(map[string]interface{}{"read_items_keep_limit": 2}) {
		t.Fatal("save failed")
	}
	date := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	var feeds []*Feed
	for n := 0; n < 2; n++ {
		feed := db.CreateFeed("feed", "", "", fmt.Sprintf("https://example.com/%d.xml", n), nil)
		feeds = append(feeds, feed)
		var items []Item
		for i := 0; i < 6; i++ {
			title := "outdated"
			if i >= 4 {
				title = "kept"
			}
			items = append(items, Item{GUID: fmt.Sprint(i), FeedId: feed.Id, Title: title, Date: date})
		}
		items = append(items, Item{GUID: "unread", FeedId: feed.Id, Date: date.Add(-time.Hour)}, Item{GUID: "favorite", FeedId: feed.Id, Date: date.Add(-2 * time.Hour)})
		if !db.CreateItems(items) {
			t.Fatal("create failed")
		}
		db.MarkItemsRead(MarkFilter{FeedID: &feed.Id})
		db.UpdateItemStatus(db.GetItemByGUID(feed.Id, "unread").Id, UNREAD)
		db.UpdateItemFavorite(db.GetItemByGUID(feed.Id, "favorite").Id, true)
	}
	db.DeleteOldItems()
	for _, feed := range feeds {
		if count := db.CountItems(ItemFilter{FeedID: &feed.Id}); count != 4 {
			t.Fatalf("feed %d count = %d, want 4", feed.Id, count)
		}
		for _, guid := range []string{"4", "5", "unread", "favorite"} {
			if db.GetItemByGUID(feed.Id, guid) == nil {
				t.Fatalf("feed %d lost %s", feed.Id, guid)
			}
		}
		if db.GetItemByGUID(feed.Id, "unread").Status != UNREAD || !db.GetItemByGUID(feed.Id, "favorite").Favorite {
			t.Fatal("protected article changed")
		}
	}
	search := "outdated"
	if count := db.CountItems(ItemFilter{Search: &search}); count != 0 {
		t.Fatalf("deleted articles still searchable: %d", count)
	}
	for _, limit := range []int{1, 5} {
		db.UpdateSettings(map[string]interface{}{"read_items_keep_limit": limit})
		db.DeleteOldItems()
		for _, feed := range feeds {
			if count := db.CountItems(ItemFilter{FeedID: &feed.Id}); count != 3 {
				t.Fatalf("limit %d: feed count = %d", limit, count)
			}
		}
	}
}

func TestDeleteOldItemsInvalidLimitProtectsHistory(t *testing.T) {
	db := testDB()
	feed := db.CreateFeed("feed", "", "", "https://example.com/feed.xml", nil)
	db.CreateItems([]Item{{GUID: "read", FeedId: feed.Id, Date: time.Now()}})
	db.MarkItemsRead(MarkFilter{FeedID: &feed.Id})
	setRawSetting(t, db, "read_items_keep_limit", "0")
	db.DeleteOldItems()
	if count := db.CountItems(ItemFilter{FeedID: &feed.Id}); count != 1 {
		t.Fatal("invalid setting deleted history")
	}
}
