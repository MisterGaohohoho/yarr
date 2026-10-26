package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nkanaev/yarr/src/server/auth"
	"github.com/nkanaev/yarr/src/storage"
)

func TestBilibiliConfigDoesNotExposeCookie(t *testing.T) {
	db := testServerDB(t)
	s := NewServer(db, "")
	handler := s.handler()
	cookie := "SESSDATA=" + strings.Repeat("private-cookie", 1000)
	body, _ := json.Marshal(map[string]string{"cookie": cookie})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("PUT", "/api/bilibili", bytes.NewReader(body)))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if config, _ := db.GetBilibiliConfig(); config.Cookie != cookie {
		t.Fatal("large cookie not saved")
	}
	for _, path := range []string{"/api/bilibili", "/api/settings", "/", "/opml/export"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || strings.Contains(w.Body.String(), "private-cookie") {
			t.Fatal("credential leaked", path, w.Code)
		}
		if path == "/api/bilibili" && (w.Header().Get("Cache-Control") != "no-store" || strings.TrimSpace(w.Body.String()) != `{"configured":true}`) {
			t.Fatal(w.Body.String())
		}
	}
	for _, bad := range []string{`{}`, `{"cookie":null}`, `{"cookie":"secret\r\nHeader: x"}`, `{"cookie":123}`, `garbage`} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("PUT", "/api/bilibili", strings.NewReader(bad)))
		if w.Code != 400 || strings.Contains(w.Body.String(), "secret") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("PUT", "/api/bilibili", strings.NewReader(`{"cookie":""}`)))
	if configured, _ := s.bilibili.Configured(); w.Code != 200 || configured {
		t.Fatal("clear failed")
	}
}
func TestBilibiliRSSPublicBasePathAndProtectedConfig(t *testing.T) {
	for _, base := range []string{"", "/reader"} {
		t.Run(base, func(t *testing.T) {
			db := testServerDB(t)
			db.SetAuthConfig(true, "user", "password")
			s := NewServer(db, "")
			s.BasePath = base
			handler := s.handler()
			for _, tc := range []struct {
				method, path string
				status       int
			}{
				{"GET", "/rss/bilibili/123", 200},
				{"POST", "/rss/bilibili/123", 405},
				{"GET", "/rss/bilibili/0", 400},
				{"GET", "/rss/bilibili/123/extra", 404},
				{"GET", "/rss/bilibili/watchlater/123", 200},
				{"POST", "/rss/bilibili/watchlater/123", 405},
				{"GET", "/rss/bilibili/watchlater/0", 400},
				{"GET", "/rss/bilibili/watchlater/abc", 400},
				{"GET", "/rss/bilibili/watchlater/123/extra", 404},
				{"GET", "/api/bilibili", 401},
				{"PUT", "/api/bilibili", 401},
				{"GET", "/api/settings", 401},
			} {
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, httptest.NewRequest(tc.method, base+tc.path, nil))
				if w.Code != tc.status {
					t.Fatal(tc, w.Code, w.Body.String())
				}
				if tc.status == 200 && (!strings.Contains(w.Body.String(), "未配置") || w.Header().Get("Cache-Control") != "no-store") {
					t.Fatal(w.Body.String())
				}
				if tc.status == 405 && w.Header().Get("Allow") != "GET" {
					t.Fatal("missing allowed method", tc.path)
				}
			}
			login := httptest.NewRecorder()
			auth.Authenticate(login, "user", "password", base)
			request := httptest.NewRequest("PUT", base+"/api/bilibili", strings.NewReader(`{"cookie":"SESSDATA=private-fixture"}`))
			request.AddCookie(login.Result().Cookies()[0])
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, request)
			if w.Code != 200 {
				t.Fatal(w.Code)
			}
			config, _ := db.GetBilibiliConfig()
			cached := map[string][]byte{}
			for _, kind := range []string{storage.BilibiliFollowings, storage.BilibiliWatchlater} {
				rss := []byte(`<rss version="2.0"><channel><title>` + kind + `</title><link>https://www.bilibili.com</link><description>cached</description></channel></rss>`)
				if saved, err := db.SaveBilibiliCache(storage.BilibiliCache{Kind: kind, UID: "123", Generation: config.Generation, RSS: rss, ExpiresAt: time.Now().Add(10 * time.Minute).Unix(), AccessedAt: time.Now().Unix()}); !saved || err != nil {
					t.Fatal(saved, err)
				}
				path := "/rss/bilibili/123"
				if kind == storage.BilibiliWatchlater {
					path = "/rss/bilibili/watchlater/123"
				}
				cached[path] = rss
			}
			for path, rss := range cached {
				w = httptest.NewRecorder()
				handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, base+path, nil))
				if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), rss) || !strings.Contains(w.Header().Get("Content-Type"), "application/rss+xml") {
					t.Fatal(path, w.Code, w.Body.String())
				}
			}
		})
	}
}
