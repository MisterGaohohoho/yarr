package bilibili

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

const browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"
const navURL = "https://api.bilibili.com/x/web-interface/nav"
const videosURL = "https://api.vc.bilibili.com/dynamic_svr/v1/dynamic_svr/dynamic_new"

type fetchError struct{ code string }

func (e *fetchError) Error() string { return failureText(e.code) }
func failureText(code string) string {
	switch code {
	case "unconfigured":
		return "未配置 Bilibili Cookie，请在系统设置中配置。"
	case "unauthorized":
		return "Bilibili Cookie 已失效，请在系统设置中替换。"
	case "mismatch":
		return "Bilibili UID 与 Cookie 登录账号不匹配，请检查订阅链接。"
	case "invalid":
		return "Bilibili 返回内容不完整或格式异常，本次未更新。"
	case "rate_limit":
		return "Bilibili 请求受到限流，请稍后重试。"
	default:
		return "Bilibili 抓取暂时失败，请稍后重试。"
	}
}
func errorCode(err error) string {
	var e *fetchError
	if errors.As(err, &e) {
		return e.code
	}
	return "temporary"
}
func wait(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type scheduler struct {
	slot               chan struct{}
	mu                 sync.Mutex
	next, limitedUntil time.Time
}

func newScheduler() *scheduler { return &scheduler{slot: make(chan struct{}, 1)} }

var processScheduler = newScheduler()

func (g *scheduler) limit(t time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if t.After(g.limitedUntil) {
		g.limitedUntil = t
	}
}
func (g *scheduler) deadline() time.Time { g.mu.Lock(); defer g.mu.Unlock(); return g.limitedUntil }

type client struct {
	http         *http.Client
	gate         *scheduler
	now          func() time.Time
	sleep        func(context.Context, time.Duration) error
	jitter       func(time.Duration, time.Duration) time.Duration
	persistLimit func(time.Time) error
}

func newClient() *client {
	return &client{http: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, gate: processScheduler, now: time.Now, sleep: wait,
		jitter: func(a, b time.Duration) time.Duration { return a + time.Duration(rand.Int63n(int64(b-a)+1)) }}
}
func (c *client) get(ctx context.Context, endpoint, uid, cookie string, out interface{}) error {
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			if err := c.sleep(ctx, time.Duration(attempt*5)*time.Second); err != nil {
				return &fetchError{"temporary"}
			}
		}
		retry, err := c.once(ctx, endpoint, uid, cookie, out)
		if !retry || err == nil {
			return err
		}
	}
	return &fetchError{"temporary"}
}
func (c *client) once(ctx context.Context, endpoint, uid, cookie string, out interface{}) (bool, error) {
	select {
	case c.gate.slot <- struct{}{}:
	case <-ctx.Done():
		return false, &fetchError{"temporary"}
	}
	defer func() { <-c.gate.slot }()
	c.gate.mu.Lock()
	next := c.gate.next
	if c.gate.limitedUntil.After(next) {
		next = c.gate.limitedUntil
	}
	c.gate.mu.Unlock()
	if err := c.sleep(ctx, next.Sub(c.now())); err != nil {
		return false, &fetchError{"temporary"}
	}
	if ctx.Err() != nil {
		return false, &fetchError{"temporary"}
	}
	u, err := url.Parse(endpoint)
	if err != nil || (u.Host != "api.bilibili.com" && u.Host != "api.vc.bilibili.com") || u.Scheme != "https" {
		return false, &fetchError{"invalid"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false, &fetchError{"invalid"}
	}
	req.Header.Set("Cookie", cookie)
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Referer", "https://space.bilibili.com/"+uid+"/")
	defer func() {
		c.gate.mu.Lock()
		c.gate.next = c.now().Add(c.jitter(3*time.Second, 7*time.Second))
		c.gate.mu.Unlock()
	}()
	res, err := c.http.Do(req)
	if err != nil {
		return true, &fetchError{"temporary"}
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 16*1024*1024+1))
	if err != nil {
		return true, &fetchError{"temporary"}
	}
	if len(body) > 16*1024*1024 {
		return false, &fetchError{"invalid"}
	}
	switch res.StatusCode {
	case 401:
		return false, &fetchError{"unauthorized"}
	case 429:
		until := c.now().Add(retryDelay(res.Header.Get("Retry-After"), c.now()))
		c.gate.limit(until)
		if c.persistLimit != nil {
			if err := c.persistLimit(until); err != nil {
				return false, &fetchError{"temporary"}
			}
		}
		return false, &fetchError{"rate_limit"}
	}
	if res.StatusCode >= 500 {
		return true, &fetchError{"temporary"}
	}
	if res.StatusCode != 200 {
		return false, &fetchError{"temporary"}
	}
	if json.Unmarshal(body, out) != nil {
		return false, &fetchError{"invalid"}
	}
	return false, nil
}
func retryDelay(header string, now time.Time) time.Duration {
	delay := 15 * time.Minute
	if seconds, err := strconv.ParseFloat(header, 64); err == nil && seconds > 0 && !math.IsInf(seconds, 0) {
		if seconds >= float64(math.MaxInt64)/float64(time.Second) {
			return time.Duration(math.MaxInt64)
		}
		if d := time.Duration(math.Ceil(seconds * float64(time.Second))); d > delay {
			delay = d
		}
	} else if t, err := http.ParseTime(header); err == nil && t.Sub(now) > delay {
		delay = t.Sub(now)
	}
	return delay
}
