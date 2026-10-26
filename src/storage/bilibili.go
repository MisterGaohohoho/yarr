package storage

import "database/sql"

const (
	BilibiliFollowings = "followings"
	BilibiliWatchlater = "watchlater"
)

// BilibiliConfig is private server configuration, never a public settings value.
type BilibiliConfig struct {
	Cookie         string
	Generation     int64
	InvalidAt      int64
	RateLimitUntil int64
}

type BilibiliCache struct {
	Kind        string
	UID         string
	Generation  int64
	RSS         []byte
	SucceededAt int64
	ExpiresAt   int64
	AccessedAt  int64
	Failure     string
	FailureAt   int64
	RetryAt     int64
}

func m27_bilibili(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE IF NOT EXISTS bilibili_config (
 id INTEGER PRIMARY KEY CHECK(id=1), cookie TEXT NOT NULL DEFAULT '',
 generation INTEGER NOT NULL DEFAULT 0, invalid_at INTEGER NOT NULL DEFAULT 0,
 rate_limit_until INTEGER NOT NULL DEFAULT 0);
 INSERT OR IGNORE INTO bilibili_config(id) VALUES(1);
 CREATE TABLE IF NOT EXISTS bilibili_cache (
 uid TEXT NOT NULL, generation INTEGER NOT NULL, rss BLOB NOT NULL,
 succeeded_at INTEGER NOT NULL, expires_at INTEGER NOT NULL, accessed_at INTEGER NOT NULL,
 failure TEXT NOT NULL, failure_at INTEGER NOT NULL, retry_at INTEGER NOT NULL,
 PRIMARY KEY(uid,generation));
 CREATE INDEX IF NOT EXISTS bilibili_cache_access ON bilibili_cache(accessed_at);`)
	return err
}

func m28_bilibili_cache_kind(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE bilibili_cache_new (
 kind TEXT NOT NULL, uid TEXT NOT NULL, generation INTEGER NOT NULL, rss BLOB NOT NULL,
 succeeded_at INTEGER NOT NULL, expires_at INTEGER NOT NULL, accessed_at INTEGER NOT NULL,
 failure TEXT NOT NULL, failure_at INTEGER NOT NULL, retry_at INTEGER NOT NULL,
 PRIMARY KEY(kind,uid,generation));
 INSERT INTO bilibili_cache_new
 SELECT 'followings',uid,generation,rss,succeeded_at,expires_at,accessed_at,failure,failure_at,retry_at FROM bilibili_cache;
 DROP TABLE bilibili_cache;
 ALTER TABLE bilibili_cache_new RENAME TO bilibili_cache;
 CREATE INDEX bilibili_cache_access ON bilibili_cache(accessed_at);`)
	return err
}

func (s *Storage) GetBilibiliConfig() (BilibiliConfig, error) {
	var c BilibiliConfig
	err := s.db.QueryRow(`SELECT cookie,generation,invalid_at,rate_limit_until FROM bilibili_config WHERE id=1`).Scan(&c.Cookie, &c.Generation, &c.InvalidAt, &c.RateLimitUntil)
	return c, err
}

func (s *Storage) SetBilibiliCookie(cookie string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE bilibili_config SET cookie=?,generation=generation+1,invalid_at=0 WHERE id=1`, cookie); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM bilibili_cache`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Storage) GetBilibiliCache(kind, uid string, generation int64) (BilibiliCache, error) {
	c := BilibiliCache{Kind: kind, UID: uid, Generation: generation}
	err := s.db.QueryRow(`SELECT rss,succeeded_at,expires_at,accessed_at,failure,failure_at,retry_at FROM bilibili_cache WHERE kind=? AND uid=? AND generation=?`, kind, uid, generation).Scan(&c.RSS, &c.SucceededAt, &c.ExpiresAt, &c.AccessedAt, &c.Failure, &c.FailureAt, &c.RetryAt)
	if err == sql.ErrNoRows {
		err = nil
	}
	return c, err
}

// SaveBilibiliCache atomically rejects results from a replaced configuration.
func (s *Storage) SaveBilibiliCache(c BilibiliCache) (bool, error) {
	if c.RSS == nil {
		c.RSS = []byte{}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO bilibili_cache
 SELECT ?,?,?,?,?,?,?,?,?,? WHERE EXISTS(SELECT 1 FROM bilibili_config WHERE id=1 AND generation=?)
 ON CONFLICT(kind,uid,generation) DO UPDATE SET rss=excluded.rss,succeeded_at=excluded.succeeded_at,
 expires_at=excluded.expires_at,accessed_at=MAX(bilibili_cache.accessed_at,excluded.accessed_at),
 failure=excluded.failure,failure_at=excluded.failure_at,retry_at=excluded.retry_at`,
		c.Kind, c.UID, c.Generation, c.RSS, c.SucceededAt, c.ExpiresAt, c.AccessedAt, c.Failure, c.FailureAt, c.RetryAt, c.Generation)
	if err != nil {
		return false, err
	}
	count, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if count > 0 && c.Failure == "unauthorized" {
		if _, err = tx.Exec(`UPDATE bilibili_config SET invalid_at=? WHERE id=1 AND generation=?`, c.FailureAt, c.Generation); err != nil {
			return false, err
		}
	}
	return count > 0, tx.Commit()
}

func (s *Storage) TouchBilibiliCache(kind, uid string, generation, now int64) error {
	_, err := s.db.Exec(`UPDATE bilibili_cache SET accessed_at=? WHERE kind=? AND uid=? AND generation=?`, now, kind, uid, generation)
	return err
}
func (s *Storage) CleanBilibiliCache(before int64) error {
	_, err := s.db.Exec(`DELETE FROM bilibili_cache WHERE accessed_at < ?`, before)
	return err
}
func (s *Storage) SetBilibiliRateLimit(until int64) error {
	_, err := s.db.Exec(`UPDATE bilibili_config SET rate_limit_until=MAX(rate_limit_until,?) WHERE id=1`, until)
	return err
}
