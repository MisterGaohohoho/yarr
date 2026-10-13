package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/nkanaev/yarr/src/bilibili"
	"github.com/nkanaev/yarr/src/server/router"
)

func (s *Server) handleBilibiliConfig(c *router.Context) {
	c.Out.Header().Set("Cache-Control", "no-store")
	if s.bilibili == nil {
		http.Error(c.Out, "Bilibili 配置暂时不可用。", http.StatusServiceUnavailable)
		return
	}
	switch c.Req.Method {
	case http.MethodGet:
		configured, err := s.bilibili.Configured()
		if err != nil {
			http.Error(c.Out, "Bilibili 配置暂时不可用。", http.StatusServiceUnavailable)
			return
		}
		c.JSON(http.StatusOK, map[string]bool{"configured": configured})
	case http.MethodPut:
		var body struct {
			Cookie *string `json:"cookie"`
		}
		if json.NewDecoder(c.Req.Body).Decode(&body) != nil || body.Cookie == nil || !bilibili.ValidCookie(*body.Cookie) {
			http.Error(c.Out, "Cookie 格式无效，不能包含换行或控制字符。", http.StatusBadRequest)
			return
		}
		if err := s.bilibili.Configure(strings.TrimSpace(*body.Cookie)); err != nil {
			http.Error(c.Out, "Bilibili 配置保存失败，请重试。", http.StatusServiceUnavailable)
			return
		}
		c.Out.WriteHeader(http.StatusOK)
	default:
		c.Out.Header().Set("Allow", "GET, PUT")
		c.Out.WriteHeader(http.StatusMethodNotAllowed)
	}
}
func (s *Server) handleBilibiliRSS(c *router.Context) {
	c.Out.Header().Set("Cache-Control", "no-store")
	if c.Req.Method != http.MethodGet {
		c.Out.Header().Set("Allow", "GET")
		c.Out.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.bilibili == nil {
		http.Error(c.Out, "Bilibili RSS 暂时不可用。", http.StatusServiceUnavailable)
		return
	}
	result := s.bilibili.Get(c.Req.Context(), c.Vars["uid"])
	if result.Status == http.StatusOK {
		c.Out.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	} else {
		c.Out.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	if result.RetryAfter > 0 {
		c.Out.Header().Set("Retry-After", strconv.Itoa(result.RetryAfter))
	}
	c.Out.WriteHeader(result.Status)
	c.Out.Write(result.Body)
}
