package bilibili

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nkanaev/yarr/src/storage"
)

const watchlaterFixture = `{"code":0,"data":{"list":[
 {"aid":20,"title":"先加入的新投稿","bvid":"invalid?bvid","pubdate":1791882000,"add_at":1791882100,"owner":{"name":"UP 主"}},
 {"aid":10,"title":"后加入的旧投稿","bvid":"BVwatchlater","pubdate":1791795600,"add_at":1791882200,"pic":"http://i0.hdslb.com/cover.jpg","desc":"简介 <script>\n第二行","owner":{"name":"稍后 UP"}},
 {"aid":10,"title":"重复视频","add_at":1791882190,"owner":{"name":"稍后 UP"}}
]}}`

func normalWatchlater(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/x/v2/history/toview" {
		io.WriteString(w, watchlaterFixture)
		return
	}
	normal(w, r)
}

func TestWatchlaterRSSAndStableIdentity(t *testing.T) {
	var calls atomic.Int32
	var readded atomic.Bool
	s, clock, _ := setup(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if readded.Load() && r.URL.Path == "/x/v2/history/toview" {
			io.WriteString(w, strings.ReplaceAll(watchlaterFixture, "1791882200", "1791882300"))
			return
		}
		normalWatchlater(w, r)
	})
	got := s.GetWatchlater(context.Background(), "123")
	if got.Status != 200 {
		t.Fatal(got)
	}
	r := readRSS(t, got.Body)
	if r.Channel.Title != "读者 稍后再看" || r.Channel.Link != watchlaterLink || len(r.Channel.Items) != 2 {
		t.Fatal(r.Channel)
	}
	item := r.Channel.Items[0]
	if item.GUID.Value != "10" || item.GUID.IsPermaLink != "false" || item.Author != "稍后 UP" || item.PubDate != time.Unix(1791882200, 0).UTC().Format(time.RFC1123Z) || item.Link != "https://www.bilibili.com/video/BVwatchlater" {
		t.Fatal(item)
	}
	if item.Enclosure == nil || item.Enclosure.URL != "https://i0.hdslb.com/cover.jpg" {
		t.Fatal("missing HTTPS cover", item.Enclosure)
	}
	for _, want := range []string{"<iframe ", "&lt;script&gt;", "<br>", `href="https://www.bilibili.com/list/watchlater?bvid=BVwatchlater"`, "打开原视频", "在稍后再看列表中查看"} {
		if !strings.Contains(item.Description, want) {
			t.Fatal("missing content", want, item.Description)
		}
	}
	if strings.Contains(item.Description, "autoplay=") || r.Channel.Items[1].Link != "https://www.bilibili.com/video/av20" || !strings.Contains(r.Channel.Items[1].Description, `href="`+watchlaterLink+`"`) {
		t.Fatal("invalid playback or fallback links", r.Channel.Items)
	}
	if again := s.GetWatchlater(context.Background(), "123"); !bytes.Equal(again.Body, got.Body) || calls.Load() != 2 {
		t.Fatal("fresh cache missed", calls.Load())
	}
	readded.Store(true)
	clock.set(clock.now().Add(11 * time.Minute))
	if stale := s.GetWatchlater(context.Background(), "123"); !bytes.Equal(stale.Body, got.Body) {
		t.Fatal("stale content not returned")
	}
	finish(s)
	updated := readRSS(t, s.GetWatchlater(context.Background(), "123").Body).Channel.Items[0]
	if updated.GUID != item.GUID || updated.PubDate == item.PubDate {
		t.Fatal("readding changed article identity or lost its new time", updated)
	}
}

func TestWatchlaterResponseCompleteness(t *testing.T) {
	for _, tc := range []struct {
		name, data string
		status     int
	}{
		{"empty", `{"list":[]}`, 200},
		{"null", `{"list":null}`, 200},
		{"missing", `{}`, 503},
		{"invalid list", `{"list":{}}`, 503},
		{"invalid field type", `{"list":[{"aid":1,"title":"video","add_at":"invalid","owner":{"name":"UP"}}]}`, 503},
		{"missing aid", `{"list":[{"title":"video","add_at":1,"owner":{"name":"UP"}}]}`, 503},
		{"missing title", `{"list":[{"aid":1,"add_at":1,"owner":{"name":"UP"}}]}`, 503},
		{"missing author", `{"list":[{"aid":1,"title":"video","add_at":1}]}`, 503},
		{"invalid time", `{"list":[{"aid":1,"title":"video","add_at":0,"owner":{"name":"UP"}}]}`, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _ := setup(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/x/v2/history/toview" {
					io.WriteString(w, `{"code":0,"data":`+tc.data+`}`)
					return
				}
				normal(w, r)
			})
			got := s.GetWatchlater(context.Background(), "123")
			if got.Status != tc.status {
				t.Fatal(got)
			}
			if tc.status == 200 && len(readRSS(t, got.Body).Channel.Items) != 0 {
				t.Fatal("empty response contains articles")
			}
		})
	}
}

func TestWatchlaterPartialRefreshKeepsCompleteCache(t *testing.T) {
	var partial atomic.Bool
	s, clock, db := setup(t, func(w http.ResponseWriter, r *http.Request) {
		if partial.Load() && r.URL.Path == "/x/v2/history/toview" {
			io.WriteString(w, strings.ReplaceAll(watchlaterFixture, `"title":"先加入的新投稿",`, ""))
			return
		}
		normalWatchlater(w, r)
	})
	old := s.GetWatchlater(context.Background(), "123")
	partial.Store(true)
	clock.set(clock.now().Add(11 * time.Minute))
	s.GetWatchlater(context.Background(), "123")
	finish(s)
	config, _ := db.GetBilibiliConfig()
	cached, err := db.GetBilibiliCache(storage.BilibiliWatchlater, "123", config.Generation)
	if err != nil || cached.Failure != "invalid" || !bytes.Equal(cached.RSS, old.Body) {
		t.Fatal("partial result replaced complete cache", cached.Failure, err)
	}
	if got := s.GetWatchlater(context.Background(), "123"); !bytes.Equal(got.Body, old.Body) {
		t.Fatal("complete result no longer served")
	}
}

func TestBilibiliFeedKindsShareConfiguration(t *testing.T) {
	var fail atomic.Bool
	var calls atomic.Int32
	s, clock, db := setup(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if fail.Load() && r.URL.Path == "/x/v2/history/toview" {
			io.WriteString(w, `{"code":-6}`)
			return
		}
		normalWatchlater(w, r)
	})
	followings := s.Get(context.Background(), "123")
	watchlater := s.GetWatchlater(context.Background(), "123")
	restarted, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	restarted.client = s.client
	if got := restarted.GetWatchlater(context.Background(), "123"); !bytes.Equal(got.Body, watchlater.Body) || calls.Load() != 4 {
		t.Fatal("watchlater cache lost on restart", calls.Load())
	}
	fail.Store(true)
	clock.set(clock.now().Add(11 * time.Minute))
	s.GetWatchlater(context.Background(), "123")
	finish(s)
	a := readRSS(t, s.Get(context.Background(), "123").Body)
	b := readRSS(t, s.GetWatchlater(context.Background(), "123").Body)
	if calls.Load() != 6 || len(a.Channel.Items) != 3 || len(b.Channel.Items) != 3 || !strings.Contains(a.Channel.Items[0].Title, "已失效") || !strings.Contains(b.Channel.Items[0].Title, "已失效") || a.Channel.Items[0].GUID == b.Channel.Items[0].GUID {
		t.Fatal("cookie failure not shared or notices collided", a, b, calls.Load())
	}
	if a.Channel.Title == b.Channel.Title || b.Channel.Items[0].Link != watchlaterLink {
		t.Fatal("notice metadata mixed between kinds", a.Channel, b.Channel)
	}
	if err := s.Configure("SESSDATA=replaced"); err != nil {
		t.Fatal(err)
	}
	config, _ := db.GetBilibiliConfig()
	for _, kind := range []string{storage.BilibiliFollowings, storage.BilibiliWatchlater} {
		cached, err := db.GetBilibiliCache(kind, "123", config.Generation-1)
		if err != nil || len(cached.RSS) > 0 || cached.Failure != "" {
			t.Fatal("rotation retained old cache", kind, err)
		}
	}
	fail.Store(false)
	if got := s.Get(context.Background(), "123"); !bytes.Equal(got.Body, followings.Body) {
		t.Fatal("following videos did not recover")
	}
	if got := s.GetWatchlater(context.Background(), "123"); !bytes.Equal(got.Body, watchlater.Body) {
		t.Fatal("watchlater did not recover")
	}
	mismatch := readRSS(t, s.GetWatchlater(context.Background(), "456").Body)
	if len(mismatch.Channel.Items) != 1 || !strings.Contains(mismatch.Channel.Items[0].Title, "不匹配") || calls.Load() != 11 {
		t.Fatal("mismatched UID fetched watchlater content", mismatch)
	}
	if err := s.Configure(""); err != nil {
		t.Fatal(err)
	}
	unconfigured := readRSS(t, s.GetWatchlater(context.Background(), "123").Body)
	if unconfigured.Channel.Title != "Bilibili 稍后再看" || len(unconfigured.Channel.Items) != 1 || !strings.Contains(unconfigured.Channel.Items[0].Title, "未配置") || calls.Load() != 11 {
		t.Fatal("clear did not discard watchlater content", unconfigured)
	}
}

func TestBilibiliFeedKindsHaveSeparateConcurrentJobs(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	s, _, _ := setup(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		normalWatchlater(w, r)
	})
	s.initialWait = 5 * time.Millisecond
	first := make(chan Result, 1)
	go func() { first <- s.Get(context.Background(), "123") }()
	<-entered
	for i := 0; i < 2; i++ {
		if got := s.GetWatchlater(context.Background(), "123"); got.Status != 503 {
			t.Fatal(got)
		}
	}
	<-first
	close(release)
	finish(s)
	followings := readRSS(t, s.Get(context.Background(), "123").Body)
	watchlater := readRSS(t, s.GetWatchlater(context.Background(), "123").Body)
	if calls.Load() != 4 || followings.Channel.Title != "读者 关注视频动态" || watchlater.Channel.Title != "读者 稍后再看" {
		t.Fatal("jobs merged across kinds or duplicate requests were not merged", calls.Load(), followings, watchlater)
	}
}
