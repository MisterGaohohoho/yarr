package storage

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestBilibiliCacheKindMigrationAndIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	raw, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	for version := int64(1); version <= 27; version++ {
		if err := migrateVersion(version, raw); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := raw.Exec(`UPDATE bilibili_config SET cookie='fixture',generation=1,invalid_at=17,rate_limit_until=2000;
 INSERT INTO bilibili_cache VALUES('123',1,'<rss/>',100,700,150,'temporary',160,1000)`); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	old := BilibiliCache{Kind: BilibiliFollowings, UID: "123", Generation: 1, RSS: []byte("<rss/>"), SucceededAt: 100, ExpiresAt: 700, AccessedAt: 150, Failure: "temporary", FailureAt: 160, RetryAt: 1000}
	if got, err := s.GetBilibiliCache(old.Kind, old.UID, old.Generation); err != nil || !reflect.DeepEqual(got, old) {
		t.Fatal("migration lost cache", got, err)
	}
	if config, err := s.GetBilibiliConfig(); err != nil || config != (BilibiliConfig{Cookie: "fixture", Generation: 1, InvalidAt: 17, RateLimitUntil: 2000}) {
		t.Fatal("migration changed shared configuration", config, err)
	}
	watchlater := old
	watchlater.Kind = BilibiliWatchlater
	watchlater.RSS = []byte("watchlater")
	if saved, err := s.SaveBilibiliCache(watchlater); !saved || err != nil {
		t.Fatal(saved, err)
	}
	if err := s.TouchBilibiliCache(watchlater.Kind, watchlater.UID, watchlater.Generation, 300); err != nil {
		t.Fatal(err)
	}
	watchlater.AccessedAt = 300
	for _, want := range []BilibiliCache{old, watchlater} {
		if got, err := s.GetBilibiliCache(want.Kind, want.UID, want.Generation); err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("cache kinds collided", got, err)
		}
	}
}

func TestBilibiliMigrationConfigurationAndGenerationGuard(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	raw, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	for version := int64(1); version <= 26; version++ {
		if err := migrateVersion(version, raw); err != nil {
			t.Fatal(err)
		}
	}
	raw.Close()
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	initial, err := s.GetBilibiliConfig()
	if err != nil || initial.Cookie != "" || initial.Generation != 0 {
		t.Fatal("invalid initial config", err)
	}
	if err := s.SetBilibiliCookie("SESSDATA=private-fixture"); err != nil {
		t.Fatal(err)
	}
	config, err := s.GetBilibiliConfig()
	if err != nil || config.Generation != 1 {
		t.Fatal("config not saved", err)
	}
	c := BilibiliCache{Kind: BilibiliFollowings, UID: "123", Generation: config.Generation, RSS: []byte("<rss/>"), AccessedAt: 100, ExpiresAt: 700}
	if saved, err := s.SaveBilibiliCache(c); !saved || err != nil {
		t.Fatal(saved, err)
	}
	s.TouchBilibiliCache(c.Kind, c.UID, c.Generation, 200)
	s.SaveBilibiliCache(c)
	if got, _ := s.GetBilibiliCache(c.Kind, c.UID, c.Generation); got.AccessedAt != 200 {
		t.Fatal("refresh undid touch")
	}
	c.Failure = "unauthorized"
	c.FailureAt = 150
	if saved, err := s.SaveBilibiliCache(c); !saved || err != nil {
		t.Fatal(saved, err)
	}
	if current, _ := s.GetBilibiliConfig(); current.InvalidAt != 150 {
		t.Fatal("cookie failure not persisted")
	}
	s.SetBilibiliRateLimit(1000)
	s.SetBilibiliRateLimit(500)
	if err := s.SetBilibiliCookie("SESSDATA=next-fixture"); err != nil {
		t.Fatal(err)
	}
	if current, _ := s.GetBilibiliConfig(); current.Generation != 2 || current.InvalidAt != 0 || current.RateLimitUntil != 1000 {
		t.Fatal("rotation lost state")
	}
	if old, _ := s.GetBilibiliCache(c.Kind, c.UID, c.Generation); len(old.RSS) > 0 {
		t.Fatal("old cache retained")
	}
	if saved, err := s.SaveBilibiliCache(c); saved || err != nil {
		t.Fatal("old result was accepted", err)
	}
	if current, _ := s.GetBilibiliConfig(); current.InvalidAt != 0 {
		t.Fatal("old failure marked new cookie invalid")
	}
	settings, _ := json.Marshal(s.GetSettings())
	if strings.Contains(string(settings), "fixture") {
		t.Fatal("secret exposed by settings")
	}
	if err := s.SetBilibiliCookie(""); err != nil {
		t.Fatal(err)
	}
	if current, _ := s.GetBilibiliConfig(); current.Cookie != "" || current.Generation != 3 {
		t.Fatal("clear did not advance version")
	}
}
func TestBilibiliPersistenceBackupAndCleanup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	s.SetBilibiliCookie("SESSDATA=private-fixture")
	config, _ := s.GetBilibiliConfig()
	c := BilibiliCache{Kind: BilibiliFollowings, UID: "123", Generation: config.Generation, RSS: []byte("<rss/>"), SucceededAt: 100, ExpiresAt: 400, AccessedAt: 300, Failure: "temporary", FailureAt: 301, RetryAt: 1200}
	s.SaveBilibiliCache(c)
	s.SetBilibiliRateLimit(1500)
	backupPath := filepath.Join(dir, "backup.db")
	if err := s.BackupTo(backupPath); err != nil {
		t.Fatal(err)
	}
	s.db.Close()
	s, err = New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	current, _ := s.GetBilibiliConfig()
	got, _ := s.GetBilibiliCache(c.Kind, c.UID, c.Generation)
	if current.Cookie != "SESSDATA=private-fixture" || current.Generation != config.Generation || got.Failure != "temporary" || got.RetryAt != 1200 || current.RateLimitUntil != 1500 {
		t.Fatal("persistence lost")
	}
	snapshot, err := New(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.db.Close()
	if saved, _ := snapshot.GetBilibiliConfig(); saved.Cookie != current.Cookie {
		t.Fatal("backup excluded configuration")
	}
	s.CleanBilibiliCache(300)
	if got, _ := s.GetBilibiliCache(c.Kind, c.UID, c.Generation); len(got.RSS) == 0 {
		t.Fatal("active cache deleted")
	}
	s.CleanBilibiliCache(301)
	if got, _ := s.GetBilibiliCache(c.Kind, c.UID, c.Generation); len(got.RSS) > 0 {
		t.Fatal("unused cache retained")
	}
	if c, _ := s.GetBilibiliConfig(); c.Cookie == "" || c.RateLimitUntil != 1500 {
		t.Fatal("cleanup removed config")
	}
}
