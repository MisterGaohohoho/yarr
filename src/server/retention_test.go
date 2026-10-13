package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nkanaev/yarr/src/storage"
)

func TestReadRetentionSettings(t *testing.T) {
	db := testServerDB(t)
	handler := NewServer(db, "127.0.0.1:8000").handler()
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/settings", nil))
	var settings map[string]interface{}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &settings) != nil || settings["read_items_keep_limit"] != float64(1000) {
		t.Fatalf("default settings: %d %s", w.Code, w.Body.String())
	}
	feed := db.CreateFeed("feed", "", "", "https://example.com/feed.xml", nil)
	db.CreateItems([]storage.Item{
		{GUID: "old", FeedId: feed.Id, Date: time.Now().Add(-time.Hour)},
		{GUID: "new", FeedId: feed.Id, Date: time.Now()},
	})
	db.MarkItemsRead(storage.MarkFilter{FeedID: &feed.Id})
	for _, value := range []string{"0", "-1", "1.5", "null", `"1000"`, "true", "9007199254740992"} {
		w = httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("PUT", "/api/settings", bytes.NewBufferString(`{"read_items_keep_limit":`+value+`,"backup_enabled":true}`)))
		if w.Code != http.StatusBadRequest || db.GetSettingsValueBool("backup_enabled") || db.GetSettingsValueInt64("read_items_keep_limit") != 1000 || db.CountItems(storage.ItemFilter{FeedID: &feed.Id}) != 2 {
			t.Fatalf("invalid limit %s changed settings or articles: %d", value, w.Code)
		}
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("PUT", "/api/settings", bytes.NewBufferString(`{"read_items_keep_limit":1}`)))
	if w.Code != http.StatusOK || db.GetSettingsValueInt64("read_items_keep_limit") != 1 || db.GetItemByGUID(feed.Id, "old") != nil || db.GetItemByGUID(feed.Id, "new") == nil {
		t.Fatal("save did not synchronously clean old articles")
	}
	if !db.SetAuthConfig(true, "user", "password") {
		t.Fatal("auth setup failed")
	}
	for _, method := range []string{"GET", "PUT"} {
		w = httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(method, "/api/settings", bytes.NewBufferString(`{"read_items_keep_limit":2}`)))
		if w.Code == http.StatusOK || db.GetSettingsValueInt64("read_items_keep_limit") != 1 {
			t.Fatal("settings endpoint bypassed authentication")
		}
	}
}
