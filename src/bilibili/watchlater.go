package bilibili

import (
	"context"
	"encoding/json"
	"html"
	"sort"
	"strings"
	"time"
)

const watchlaterURL = "https://api.bilibili.com/x/v2/history/toview"
const watchlaterLink = "https://www.bilibili.com/list/watchlater"

type watchlaterVideo struct {
	video
	BVID  string `json:"bvid"`
	AddAt int64  `json:"add_at"`
	Owner struct {
		Name string `json:"name"`
	} `json:"owner"`
}

func (c *client) fetchWatchlater(ctx context.Context, uid, cookie, name string) ([]byte, error) {
	var response envelope
	if err := c.get(ctx, watchlaterURL, uid, cookie, &response); err != nil {
		return nil, err
	}
	if err := apiError(response); err != nil {
		return nil, err
	}
	var data struct {
		List json.RawMessage `json:"list"`
	}
	if json.Unmarshal(response.Data, &data) != nil || len(data.List) == 0 {
		return nil, &fetchError{"invalid"}
	}
	var videos []watchlaterVideo
	if json.Unmarshal(data.List, &videos) != nil {
		return nil, &fetchError{"invalid"}
	}
	sort.SliceStable(videos, func(i, j int) bool { return videos[i].AddAt > videos[j].AddAt })
	r := rss{Version: "2.0", Channel: rssChannel{Title: name + " 稍后再看", Link: watchlaterLink, Description: "Bilibili 稍后再看"}}
	seen := map[string]bool{}
	for _, v := range videos {
		if !numericID(v.AID.String()) || v.AddAt <= 0 || strings.TrimSpace(v.Title) == "" || strings.TrimSpace(v.Owner.Name) == "" {
			return nil, &fetchError{"invalid"}
		}
		if seen[v.AID.String()] {
			continue
		}
		seen[v.AID.String()] = true
		item := videoItem(v.video, v.BVID, v.Owner.Name)
		item.PubDate = time.Unix(v.AddAt, 0).UTC().Format(time.RFC1123Z)
		link := watchlaterLink
		if bvidPattern.MatchString(v.BVID) {
			link += "?bvid=" + v.BVID
		}
		item.Description += `<p><a href="` + html.EscapeString(link) + `">在稍后再看列表中查看</a></p>`
		r.Channel.Items = append(r.Channel.Items, item)
	}
	return encodeRSS(r)
}
