package storage

import "testing"

func TestBulkReadingSettingsRollback(t *testing.T) {
	for _, table := range []string{"feeds", "settings"} {
		t.Run(table, func(t *testing.T) {
			db := testDB()
			t.Cleanup(func() { db.db.Close() })
			folder := db.CreateFolder("folder")
			feed := db.CreateFeed("feed", "", "", "https://example.com/feed", &folder.Id)
			operation := "update"
			if table == "settings" {
				operation = "insert"
			}
			_, err := db.db.Exec(`create trigger fail_bulk before ` + operation + ` on ` + table + ` begin select raise(abort, 'write failed'); end`)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.BulkUpdateReadingSetting(nil, "unread_first", false); err == nil {
				t.Fatal("expected write failure")
			}
			if !db.ListFolders()[0].UnreadFirst || !db.GetFeed(feed.Id).UnreadFirst || !db.GetSettingsValueBool("unread_first") {
				t.Fatal("failed transaction changed settings")
			}
		})
	}
}
