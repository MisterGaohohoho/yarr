package bilibili

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nkanaev/yarr/src/parser"
	"github.com/nkanaev/yarr/src/storage"
)

type fakeClock struct {
	mu    sync.Mutex
	t     time.Time
	waits []time.Duration
}

func (f *fakeClock) now() time.Time  { f.mu.Lock(); defer f.mu.Unlock(); return f.t }
func (f *fakeClock) set(t time.Time) { f.mu.Lock(); defer f.mu.Unlock(); f.t = t }
func (f *fakeClock) sleep(ctx context.Context, d time.Duration) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if d > 0 {
		f.waits = append(f.waits, d)
		f.t = f.t.Add(d)
	}
	return nil
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func setup(t *testing.T, h http.HandlerFunc) (*Service, *fakeClock, *storage.Storage) {
	t.Helper()
	db, err := storage.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Configure("SESSDATA=private-test-cookie"); err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{t: time.Date(2026, 10, 13, 15, 0, 0, 0, time.UTC)} // 23:00 Beijing: no night restriction.
	s.client.now = clock.now
	s.client.sleep = clock.sleep
	s.client.jitter = func(a, b time.Duration) time.Duration { return a }
	s.client.gate = newScheduler()
	s.client.http.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		if (r.URL.Host != "api.bilibili.com" && r.URL.Host != "api.vc.bilibili.com") || r.Header.Get("Cookie") == "" || !strings.HasPrefix(r.UserAgent(), "Mozilla/") || r.Header.Get("Referer") != "https://space.bilibili.com/123/" && r.Header.Get("Referer") != "https://space.bilibili.com/456/" {
			t.Error("invalid upstream headers")
		}
		w := httptest.NewRecorder()
		h(w, r)
		return w.Result(), nil
	})
	return s, clock, db
}
func normal(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/nav") {
		io.WriteString(w, `{"code":0,"data":{"isLogin":true,"mid":123,"uname":"读者"}}`)
		return
	}
	videos := []map[string]interface{}{}
	for _, v := range []struct {
		aid         int
		title, bvid string
		date        int64
	}{{20, "新视频", "BVtestNew", 1791882000}, {10, "旧视频", "", 1791795600}, {20, "重复视频", "BVtestNew", 1791882000}} {
		card, _ := json.Marshal(map[string]interface{}{"aid": v.aid, "title": v.title, "pubdate": v.date, "pic": "//i0.hdslb.com/test.jpg?x=1&y=2", "desc": "intro <script>\nsecond line"})
		videos = append(videos, map[string]interface{}{"card": string(card), "desc": map[string]interface{}{"bvid": v.bvid, "user_profile": map[string]interface{}{"info": map[string]string{"uname": "UP 主"}}}})
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"code": 0, "data": map[string]interface{}{"cards": videos}})
}
func readRSS(t *testing.T, b []byte) rss {
	t.Helper()
	var r rss
	if err := xml.Unmarshal(b, &r); err != nil {
		t.Fatal(err, string(b))
	}
	return r
}
func finish(s *Service) {
	s.mu.Lock()
	jobs := make([]*job, 0, len(s.jobs))
	for _, j := range s.jobs {
		jobs = append(jobs, j)
	}
	s.mu.Unlock()
	for _, j := range jobs {
		<-j.done
	}
}

func TestRSSAndFreshCacheAtNight(t *testing.T) {
	var calls atomic.Int32
	s, clock, db := setup(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); normal(w, r) })
	got := s.Get(context.Background(), "123")
	if got.Status != 200 {
		t.Fatal(got)
	}
	rss := readRSS(t, got.Body)
	if rss.Channel.Title != "读者 关注视频动态" || len(rss.Channel.Items) != 2 {
		t.Fatal(rss)
	}
	item := rss.Channel.Items[0]
	if item.GUID.Value != "20" || item.GUID.IsPermaLink != "false" || item.Author != "UP 主" || item.Link != "https://www.bilibili.com/video/BVtestNew" || rss.Channel.Items[1].Link != "https://www.bilibili.com/video/av10" {
		t.Fatal(rss.Channel.Items)
	}
	for _, want := range []string{"<iframe ", "player.bilibili.com", "autoplay=0", "video-wrapper", "sandbox=", "<img ", "&lt;script&gt;", "<br>"} {
		if !strings.Contains(item.Description, want) {
			t.Errorf("missing %s: %s", want, item.Description)
		}
	}
	if strings.Contains(item.Description, "<script>") || strings.Contains(string(got.Body), "private-test-cookie") {
		t.Fatal("unsafe RSS")
	}
	parsed, err := parser.Parse(bytes.NewReader(got.Body))
	if err != nil || len(parsed.Items) != 2 {
		t.Fatal(err)
	}
	if len(parsed.Items[0].MediaLinks) != 1 || parsed.Items[0].MediaLinks[0].Type != "image" || parsed.Items[0].MediaLinks[0].URL != "https://i0.hdslb.com/test.jpg?x=1&y=2" {
		t.Fatal(parsed.Items[0].MediaLinks)
	}
	if again := s.Get(context.Background(), "123"); !bytes.Equal(got.Body, again.Body) || calls.Load() != 2 {
		t.Fatal("cache miss")
	}
	config, _ := db.GetBilibiliConfig()
	cache, _ := db.GetBilibiliCache("123", config.Generation)
	if cache.ExpiresAt-cache.SucceededAt != 300 || cache.AccessedAt != clock.now().Unix() {
		t.Fatal(cache)
	}
	saved, _ := json.Marshal(cache)
	if strings.Contains(string(saved), "private-test-cookie") {
		t.Fatal("cookie in cache")
	}
}
func TestEmptyFeedAndMalformedResponse(t *testing.T) {
	for _, payload := range []string{`{"code":0,"data":{"cards":[]}}`, `{"code":0,"data":{}}`, `{"code":0,"data":{"cards":[{"card":"bad"}]}}`, `{"code":4100000,"message":"private-test-cookie"}`} {
		t.Run(payload, func(t *testing.T) {
			s, _, _ := setup(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/nav") {
					normal(w, r)
				} else {
					io.WriteString(w, payload)
				}
			})
			got := s.Get(context.Background(), "123")
			if strings.Contains(payload, `"cards":[]`) {
				if got.Status != 200 || len(readRSS(t, got.Body).Channel.Items) != 0 {
					t.Fatal(got)
				}
			} else if got.Status != 503 || strings.Contains(string(got.Body), "失效") || strings.Contains(string(got.Body), "private-test-cookie") {
				t.Fatal(got)
			}
		})
	}
}
func TestUnconfiguredInvalidAndMismatchedUID(t *testing.T) {
	var calls atomic.Int32
	s, _, _ := setup(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); normal(w, r) })
	for _, uid := range []string{"abc", "0", "01", "18446744073709551616"} {
		if got := s.Get(context.Background(), uid); got.Status != 400 {
			t.Fatal(got)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid input sent upstream")
	}
	if got := s.Get(context.Background(), "456"); got.Status != 200 || len(readRSS(t, got.Body).Channel.Items) != 1 || !strings.Contains(string(got.Body), "不匹配") {
		t.Fatal(got)
	}
	s.Get(context.Background(), "456")
	if calls.Load() != 1 {
		t.Fatal("mismatch retried")
	}
	if err := s.Configure(""); err != nil {
		t.Fatal(err)
	}
	a := s.Get(context.Background(), "123")
	b := s.Get(context.Background(), "123")
	if a.Status != 200 || !bytes.Equal(a.Body, b.Body) || !strings.Contains(string(a.Body), "未配置") || calls.Load() != 1 {
		t.Fatal(a)
	}
}
func TestInvalidCookieNoticePersistsAndBlocksOtherUIDs(t *testing.T) {
	var fail atomic.Bool
	var calls atomic.Int32
	s, clock, db := setup(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if fail.Load() {
			io.WriteString(w, `{"code":-101}`)
		} else {
			normal(w, r)
		}
	})
	old := s.Get(context.Background(), "123")
	fail.Store(true)
	clock.set(clock.now().Add(11 * time.Minute))
	stale := s.Get(context.Background(), "123")
	if !bytes.Equal(stale.Body, old.Body) {
		t.Fatal("did not return old content")
	}
	finish(s)
	failed := s.Get(context.Background(), "123")
	items := readRSS(t, failed.Body).Channel.Items
	if len(items) != 3 || !strings.Contains(items[0].Title, "已失效") {
		t.Fatal(items)
	}
	if again := s.Get(context.Background(), "123"); !bytes.Equal(failed.Body, again.Body) || calls.Load() != 3 {
		t.Fatal("repeated failed fetch")
	}
	if got := s.Get(context.Background(), "456"); len(readRSS(t, got.Body).Channel.Items) != 1 || calls.Load() != 3 {
		t.Fatal("invalid config sent upstream")
	}
	restarted, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	restarted.client = s.client
	if got := restarted.Get(context.Background(), "123"); !bytes.Equal(got.Body, failed.Body) || calls.Load() != 3 {
		t.Fatal("restart lost failure")
	}
	fail.Store(false)
	if err = s.Configure("SESSDATA=new-cookie"); err != nil {
		t.Fatal(err)
	}
	if got := s.Get(context.Background(), "123"); got.Status != 200 || len(readRSS(t, got.Body).Channel.Items) != 2 || calls.Load() != 5 {
		t.Fatal(got)
	}
}
func TestConfigurationChangeRejectsInflightResult(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	s, _, db := setup(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		normal(w, r)
	})
	previous, _ := db.GetBilibiliConfig()
	first := make(chan Result, 1)
	go func() { first <- s.Get(context.Background(), "123") }()
	<-entered
	if err := s.Configure("SESSDATA=new-cookie"); err != nil {
		t.Fatal(err)
	}
	close(release)
	if got := <-first; got.Status != 503 || !strings.Contains(string(got.Body), "配置已更新") {
		t.Fatal(got)
	}
	old, _ := db.GetBilibiliCache("123", previous.Generation)
	if len(old.RSS) != 0 || old.Failure != "" {
		t.Fatal("old task wrote back", old)
	}
	got := s.Get(context.Background(), "123")
	if got.Status != 200 || len(readRSS(t, got.Body).Channel.Items) != 2 {
		t.Fatal(got)
	}
}
func TestConcurrentColdRequestsShareTask(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	s, _, _ := setup(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		normal(w, r)
	})
	s.initialWait = 5 * time.Millisecond
	first := make(chan Result, 1)
	go func() { first <- s.Get(context.Background(), "123") }()
	<-entered
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got := s.Get(context.Background(), "123")
			if got.Status != 503 || got.RetryAfter != 30 {
				t.Error(got)
			}
		}()
	}
	wg.Wait()
	if got := <-first; got.Status != 503 || calls.Load() != 1 {
		t.Fatal(got, calls.Load())
	}
	close(release)
	finish(s)
	if got := s.Get(context.Background(), "123"); got.Status != 200 || calls.Load() != 2 {
		t.Fatal(got, calls.Load())
	}
}
func TestRetriesCooldownAndRateLimitRestart(t *testing.T) {
	var calls atomic.Int32
	s, clock, db := setup(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) })
	got := s.Get(context.Background(), "123")
	if got.Status != 503 || calls.Load() != 3 || got.RetryAfter < 900 {
		t.Fatal(got, calls.Load())
	}
	s.Get(context.Background(), "123")
	if calls.Load() != 3 || len(clock.waits) != 2 || clock.waits[0] != 5*time.Second || clock.waits[1] != 10*time.Second {
		t.Fatal(clock.waits)
	}
	s.Configure("SESSDATA=replaced")
	s.client.http.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		w.Header().Set("Retry-After", "7200")
		w.WriteHeader(429)
		return w.Result(), nil
	})
	got = s.Get(context.Background(), "123")
	config, _ := db.GetBilibiliConfig()
	if got.Status != 503 || got.RetryAfter < 7200 || config.RateLimitUntil < clock.now().Add(2*time.Hour).Unix() {
		t.Fatal(got, config.RateLimitUntil)
	}
	previous := processScheduler
	processScheduler = newScheduler()
	defer func() { processScheduler = previous }()
	restarted, err := New(db)
	if err != nil || restarted.client.gate.deadline().Unix() != config.RateLimitUntil {
		t.Fatal("rate limit lost", err)
	}
}
func TestRestartCacheAndTaskTimeout(t *testing.T) {
	var calls atomic.Int32
	s, _, db := setup(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); normal(w, r) })
	old := s.Get(context.Background(), "123")
	restarted, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	restarted.client = s.client
	if got := restarted.Get(context.Background(), "123"); !bytes.Equal(got.Body, old.Body) || calls.Load() != 2 {
		t.Fatal("cache lost")
	}
	s.Configure("SESSDATA=other")
	s.taskTimeout = 5 * time.Millisecond
	s.client.gate.slot <- struct{}{}
	got := s.Get(context.Background(), "123")
	<-s.client.gate.slot
	if got.Status != 503 || calls.Load() != 2 {
		t.Fatal(got, calls.Load())
	}
}
func TestRedirectAndInvalidLoginShape(t *testing.T) {
	for _, status := range []int{200, 302} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			s, _, _ := setup(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if status == 302 {
					w.Header().Set("Location", "https://example.com/collect")
				}
				w.WriteHeader(status)
				io.WriteString(w, `{"code":0,"data":{}}`)
			})
			got := s.Get(context.Background(), "123")
			if got.Status != 503 || calls.Load() != 1 {
				t.Fatal(got, calls.Load())
			}
		})
	}
}
func TestBilibiliScheduleRangesAndCookieValidation(t *testing.T) {
	c := newClient()
	for i := 0; i < 100; i++ {
		for _, bounds := range [][2]time.Duration{{5 * time.Minute, 10 * time.Minute}, {3 * time.Second, 7 * time.Second}} {
			d := c.jitter(bounds[0], bounds[1])
			if d < bounds[0] || d > bounds[1] {
				t.Fatal(d)
			}
		}
	}
	if !ValidCookie("SESSDATA=a; DedeUserID=123") || ValidCookie("a\nInjected: b") || ValidCookie("a\x00b") {
		t.Fatal("invalid Cookie validation")
	}
}

func TestPartialRefreshKeepsCompleteCache(t *testing.T) {
	var broken atomic.Bool
	var calls atomic.Int32
	s, clock, db := setup(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if broken.Load() && !strings.HasSuffix(r.URL.Path, "/nav") {
			io.WriteString(w, `{"code":0,"data":{"cards":[{"card":"invalid"}]}}`)
		} else {
			normal(w, r)
		}
	})
	old := s.Get(context.Background(), "123")
	clock.set(clock.now().Add(11 * time.Minute))
	broken.Store(true)
	if got := s.Get(context.Background(), "123"); !bytes.Equal(got.Body, old.Body) {
		t.Fatal("old content did not return immediately")
	}
	finish(s)
	config, _ := db.GetBilibiliConfig()
	cache, _ := db.GetBilibiliCache("123", config.Generation)
	if cache.Failure != "invalid" || !bytes.Equal(cache.RSS, old.Body) {
		t.Fatal("partial result replaced old RSS")
	}
	s.Get(context.Background(), "123")
	if calls.Load() != 4 {
		t.Fatal("cooldown bypassed")
	}
}
func TestGlobalQueueSpacesDifferentUIDs(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls, active atomic.Int32
	var clock *fakeClock
	var mu sync.Mutex
	var times []time.Time
	s, c, _ := setup(t, func(w http.ResponseWriter, r *http.Request) {
		if active.Add(1) != 1 {
			t.Error("overlapping requests")
		}
		defer active.Add(-1)
		mu.Lock()
		times = append(times, clock.now())
		mu.Unlock()
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		normal(w, r)
	})
	clock = c
	results := make(chan Result, 2)
	go func() { results <- s.Get(context.Background(), "123") }()
	<-entered
	go func() { results <- s.Get(context.Background(), "456") }()
	close(release)
	for i := 0; i < 2; i++ {
		if got := <-results; got.Status != 200 {
			t.Fatal(got)
		}
	}
	if calls.Load() != 3 {
		t.Fatal(calls.Load())
	}
	for i := 1; i < len(times); i++ {
		if times[i].Sub(times[i-1]) < 3*time.Second {
			t.Fatal("interval too short")
		}
	}
}
func TestLoggedOutStateAndAuthenticationCodes(t *testing.T) {
	for _, body := range []string{`{"code":0,"data":{"isLogin":false}}`, `{"code":-6}`, `{"code":-101}`} {
		s, _, db := setup(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) })
		got := s.Get(context.Background(), "123")
		config, _ := db.GetBilibiliConfig()
		if got.Status != 200 || !strings.Contains(string(got.Body), "Cookie 已失效") || config.InvalidAt == 0 {
			t.Fatal(got)
		}
	}
}
