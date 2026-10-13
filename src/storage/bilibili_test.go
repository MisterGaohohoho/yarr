package storage

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

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
	c := BilibiliCache{UID: "123", Generation: config.Generation, RSS: []byte("<rss/>"), AccessedAt: 100, ExpiresAt: 700}
	if saved, err := s.SaveBilibiliCache(c); !saved || err != nil {
		t.Fatal(saved, err)
	}
	s.TouchBilibiliCache(c.UID, c.Generation, 200)
	s.SaveBilibiliCache(c)
	if got, _ := s.GetBilibiliCache(c.UID, c.Generation); got.AccessedAt != 200 {
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
	if old, _ := s.GetBilibiliCache(c.UID, c.Generation); len(old.RSS) > 0 {
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
	c := BilibiliCache{UID: "123", Generation: config.Generation, RSS: []byte("<rss/>"), SucceededAt: 100, ExpiresAt: 400, AccessedAt: 300, Failure: "temporary", FailureAt: 301, RetryAt: 1200}
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
	got, _ := s.GetBilibiliCache(c.UID, c.Generation)
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
	if got, _ := s.GetBilibiliCache(c.UID, c.Generation); len(got.RSS) == 0 {
		t.Fatal("active cache deleted")
	}
	s.CleanBilibiliCache(301)
	if got, _ := s.GetBilibiliCache(c.UID, c.Generation); len(got.RSS) > 0 {
		t.Fatal("unused cache retained")
	}
	if c, _ := s.GetBilibiliConfig(); c.Cookie == "" || c.RateLimitUntil != 1500 {
		t.Fatal("cleanup removed config")
	}
}
