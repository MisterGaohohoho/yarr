// Package bilibili provides cached RSS for one configured account's followed videos.
package bilibili

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/nkanaev/yarr/src/storage"
)

type cacheStore interface {
	GetBilibiliConfig() (storage.BilibiliConfig, error)
	SetBilibiliCookie(string) error
	GetBilibiliCache(string, int64) (storage.BilibiliCache, error)
	SaveBilibiliCache(storage.BilibiliCache) (bool, error)
	TouchBilibiliCache(string, int64, int64) error
	CleanBilibiliCache(int64) error
	SetBilibiliRateLimit(int64) error
}
type job struct {
	done   chan struct{}
	cancel context.CancelFunc
	err    error
}
type Service struct {
	store                    cacheStore
	client                   *client
	mu                       sync.Mutex
	jobs                     map[string]*job
	initialWait, taskTimeout time.Duration
}
type Result struct {
	Body               []byte
	Status, RetryAfter int
}

func unavailable(text string, retry int) Result {
	return Result{[]byte(text), http.StatusServiceUnavailable, retry}
}
func New(store cacheStore) (*Service, error) {
	config, err := store.GetBilibiliConfig()
	if err != nil {
		return nil, err
	}
	c := newClient()
	c.gate.limit(time.Unix(config.RateLimitUntil, 0))
	c.persistLimit = func(t time.Time) error { return store.SetBilibiliRateLimit(t.Unix() + 1) }
	if err := store.CleanBilibiliCache(c.now().Add(-30 * 24 * time.Hour).Unix()); err != nil {
		return nil, err
	}
	return &Service{store: store, client: c, jobs: map[string]*job{}, initialWait: 25 * time.Second, taskTimeout: 3 * time.Minute}, nil
}
func (s *Service) Cleanup(ctx context.Context) {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.mu.Lock()
			_ = s.store.CleanBilibiliCache(s.client.now().Add(-30 * 24 * time.Hour).Unix())
			s.mu.Unlock()
		}
	}
}
func ValidCookie(cookie string) bool {
	for _, b := range []byte(cookie) {
		if b < 32 || b == 127 {
			return false
		}
	}
	return true
}
func (s *Service) Configure(cookie string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.store.SetBilibiliCookie(strings.TrimSpace(cookie)); err != nil {
		return err
	}
	for _, j := range s.jobs {
		j.cancel()
	}
	return nil
}
func (s *Service) Configured() (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.store.GetBilibiliConfig()
	return c.Cookie != "", err
}
func (s *Service) Get(ctx context.Context, uid string) Result {
	if !numericID(uid) {
		return Result{Body: []byte("Bilibili UID 必须为有效的数字 ID。"), Status: http.StatusBadRequest}
	}
	s.mu.Lock()
	config, err := s.store.GetBilibiliConfig()
	if err != nil {
		s.mu.Unlock()
		return unavailable("Bilibili 配置暂时不可用。", 30)
	}
	if config.Cookie == "" {
		s.mu.Unlock()
		return notice(nil, uid, config.Generation, "unconfigured", 0)
	}
	cached, err := s.store.GetBilibiliCache(uid, config.Generation)
	if err != nil {
		s.mu.Unlock()
		return unavailable("RSS 缓存暂时不可用。", 30)
	}
	now := s.client.now()
	cached.AccessedAt = now.Unix()
	if err = s.store.TouchBilibiliCache(uid, config.Generation, now.Unix()); err != nil {
		s.mu.Unlock()
		return unavailable("RSS 缓存暂时不可用。", 30)
	}
	if config.InvalidAt > 0 {
		cached.Failure = "unauthorized"
		cached.FailureAt = config.InvalidAt
	}
	if config.InvalidAt > 0 || cached.Failure == "mismatch" || (len(cached.RSS) > 0 && now.Unix() < cached.ExpiresAt) || now.Unix() < cached.RetryAt {
		s.mu.Unlock()
		return response(cached, s.client.now())
	}
	key := fmt.Sprintf("%s:%d", uid, config.Generation)
	running := s.jobs[key]
	if running == nil {
		if _, err = s.store.SaveBilibiliCache(cached); err != nil {
			s.mu.Unlock()
			return unavailable("RSS 缓存暂时不可用。", 30)
		}
		taskCtx, cancel := context.WithTimeout(context.Background(), s.taskTimeout)
		running = &job{done: make(chan struct{}), cancel: cancel}
		s.jobs[key] = running
		go s.refresh(taskCtx, key, uid, config, running)
	}
	s.mu.Unlock()
	if len(cached.RSS) > 0 {
		return response(cached, s.client.now())
	}
	timer := time.NewTimer(s.initialWait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return unavailable("首次内容正在准备，请稍后重试。", 30)
	case <-timer.C:
		return unavailable("首次内容正在准备，请稍后重试。", 30)
	case <-running.done:
		if running.err != nil {
			return unavailable("RSS 缓存暂时不可用。", 30)
		}
		s.mu.Lock()
		current, err := s.store.GetBilibiliConfig()
		if err != nil {
			s.mu.Unlock()
			return unavailable("Bilibili 配置暂时不可用。", 30)
		}
		if current.Generation != config.Generation {
			s.mu.Unlock()
			return unavailable("Bilibili Cookie 配置已更新，请重试。", 1)
		}
		cached, err = s.store.GetBilibiliCache(uid, config.Generation)
		s.mu.Unlock()
		if err != nil {
			return unavailable("RSS 缓存暂时不可用。", 30)
		}
		return response(cached, s.client.now())
	}
}
func notice(old []byte, uid string, generation int64, code string, at int64) Result {
	b, err := noticeRSS(old, uid, generation, code, at)
	if err != nil {
		return unavailable("RSS 缓存暂时不可用。", 30)
	}
	return Result{Body: b, Status: http.StatusOK}
}
func response(c storage.BilibiliCache, now time.Time) Result {
	if c.Failure == "unauthorized" {
		return notice(c.RSS, c.UID, c.Generation, c.Failure, c.FailureAt)
	}
	if c.Failure == "mismatch" {
		return notice(nil, c.UID, c.Generation, c.Failure, c.FailureAt)
	}
	if len(c.RSS) > 0 {
		return Result{Body: c.RSS, Status: http.StatusOK}
	}
	retry := 30
	if c.RetryAt > now.Unix() {
		retry = int(c.RetryAt - now.Unix())
	}
	return unavailable(failureText(c.Failure), retry)
}
func (s *Service) refresh(ctx context.Context, key, uid string, config storage.BilibiliConfig, j *job) {
	defer j.cancel()
	body, fetchErr := s.client.fetch(ctx, uid, config.Cookie)
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() { delete(s.jobs, key); close(j.done) }()
	current, err := s.store.GetBilibiliConfig()
	if err != nil {
		j.err = err
		return
	}
	if current.Generation != config.Generation {
		return
	}
	c, err := s.store.GetBilibiliCache(uid, config.Generation)
	if err != nil {
		j.err = err
		return
	}
	now := s.client.now()
	if c.AccessedAt == 0 {
		c.AccessedAt = now.Unix()
	}
	if fetchErr == nil {
		c.RSS = body
		c.SucceededAt = now.Unix()
		c.ExpiresAt = now.Add(s.client.jitter(5*time.Minute, 10*time.Minute)).Unix()
		c.Failure = ""
		c.FailureAt = 0
		c.RetryAt = 0
	} else {
		code := errorCode(fetchErr)
		if c.Failure != code {
			c.FailureAt = now.Unix()
		}
		c.Failure = code
		c.RetryAt = now.Add(15 * time.Minute).Unix()
		if deadline := s.client.gate.deadline().Unix() + 1; deadline > c.RetryAt {
			c.RetryAt = deadline
		}
	}
	_, j.err = s.store.SaveBilibiliCache(c)
}
