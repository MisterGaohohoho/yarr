package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBulkReadingSettings(t *testing.T) {
	for _, field := range []string{"unread_first", "auto_read_scroll"} {
		for _, value := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", field, value), func(t *testing.T) {
				db := testServerDB(t)
				folder := db.CreateFolder("target")
				other := db.CreateFolder("other")
				empty := db.CreateFolder("empty")
				db.CreateFeed("inside", "", "", "https://example.com/inside", &folder.Id)
				db.CreateFeed("outside", "", "", "https://example.com/outside", &other.Id)
				db.CreateFeed("unfiled", "", "", "https://example.com/unfiled", nil)
				if err := db.BulkUpdateReadingSetting(nil, field, !value); err != nil {
					t.Fatal(err)
				}
				handler := NewServer(db, "127.0.0.1:8000").handler()
				update := func(folderID *int64) {
					t.Helper()
					payload := map[string]interface{}{"field": field, "value": value}
					if folderID != nil {
						payload["folder_id"] = *folderID
					}
					body, _ := json.Marshal(payload)
					recorder := httptest.NewRecorder()
					handler.ServeHTTP(recorder, httptest.NewRequest("PUT", "/api/settings/bulk", bytes.NewReader(body)))
					if recorder.Code != http.StatusNoContent {
						t.Fatalf("update got %d", recorder.Code)
					}
				}
				check := func(all, emptyUpdated bool) {
					t.Helper()
					for _, f := range db.ListFolders() {
						want := !value
						if all || f.Id == folder.Id || (emptyUpdated && f.Id == empty.Id) {
							want = value
						}
						got := f.UnreadFirst
						if field == "auto_read_scroll" {
							got = f.AutoReadScroll
						}
						if got != want || !f.SortNewestFirst || !f.IsExpanded {
							t.Fatalf("unexpected folder: %#v", f)
						}
					}
					for _, f := range db.ListFeeds() {
						want := !value
						if all || (f.FolderId != nil && *f.FolderId == folder.Id) {
							want = value
						}
						got := f.UnreadFirst
						if field == "auto_read_scroll" {
							got = f.AutoReadScroll
						}
						if got != want || !f.SortNewestFirst {
							t.Fatalf("unexpected feed: %#v", f)
						}
					}
					wantGlobal := !value
					if all {
						wantGlobal = value
					}
					if got := db.GetSettingsValueBool(field); got != wantGlobal {
						t.Fatalf("global value = %t", got)
					}
				}
				update(&folder.Id)
				check(false, false)
				update(&empty.Id)
				check(false, true)
				update(nil)
				check(true, true)
			})
		}
	}
}

func TestBulkReadingSettingsInvalidRequests(t *testing.T) {
	db := testServerDB(t)
	folder := db.CreateFolder("unchanged")
	feed := db.CreateFeed("unchanged", "", "", "https://example.com/feed", &folder.Id)
	handler := NewServer(db, "127.0.0.1:8000").handler()
	for _, body := range []string{
		`{`, `{}`, `null`, `{"field":"unread_first"}`, `{"field":"unread_first","value":null}`,
		`{"field":"unread_first","value":"false"}`, `{"field":"unread_first","value":0}`,
		`{"field":"sort_newest_first","value":false}`, `{"field":"unread_first = 0","value":false}`,
		`{"folder_id":0,"field":"unread_first","value":false}`,
		`{"folder_id":-1,"field":"unread_first","value":false}`,
		`{"folder_id":"1","field":"unread_first","value":false}`,
		`{"field":"unread_first","value":false,"title":"changed"}`,
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest("PUT", "/api/settings/bulk", bytes.NewBufferString(body)))
		if recorder.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d", body, recorder.Code)
		}
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest("PUT", "/api/settings/bulk", bytes.NewBufferString(`{"folder_id":9999,"field":"unread_first","value":false}`)))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("missing folder got %d", recorder.Code)
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest("GET", "/api/settings/bulk", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("unsupported method got %d", recorder.Code)
	}
	if !db.GetFeed(feed.Id).UnreadFirst || !db.ListFolders()[0].UnreadFirst || !db.GetSettingsValueBool("unread_first") {
		t.Fatal("invalid request changed settings")
	}
}
