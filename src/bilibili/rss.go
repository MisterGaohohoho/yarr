package bilibili

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nkanaev/yarr/src/content/sanitizer"
)

type envelope struct {
	Code *int            `json:"code"`
	Data json.RawMessage `json:"data"`
}
type rss struct {
	XMLName xml.Name   `xml:"rss"`
	Version string     `xml:"version,attr"`
	Channel rssChannel `xml:"channel"`
}
type rssChannel struct {
	Title       string    `xml:"title"`
	Link        string    `xml:"link"`
	Description string    `xml:"description"`
	Items       []rssItem `xml:"item"`
}
type enclosure struct {
	URL    string `xml:"url,attr"`
	Type   string `xml:"type,attr"`
	Length int    `xml:"length,attr"`
}
type guid struct {
	IsPermaLink string `xml:"isPermaLink,attr"`
	Value       string `xml:",chardata"`
}
type rssItem struct {
	Title       string     `xml:"title"`
	Link        string     `xml:"link"`
	GUID        guid       `xml:"guid"`
	PubDate     string     `xml:"pubDate,omitempty"`
	Author      string     `xml:"http://purl.org/dc/elements/1.1/ creator,omitempty"`
	Description string     `xml:"description"`
	Enclosure   *enclosure `xml:"enclosure,omitempty"`
}

func encodeRSS(r rss) ([]byte, error) {
	b, err := xml.Marshal(r)
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), b...), nil
}
func numericID(id string) bool {
	if len(id) == 0 || len(id) > 20 {
		return false
	}
	for _, ch := range id {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	n, err := strconv.ParseUint(id, 10, 64)
	return err == nil && n > 0 && strconv.FormatUint(n, 10) == id
}
func apiError(e envelope) error {
	if e.Code == nil {
		return &fetchError{"invalid"}
	}
	switch *e.Code {
	case 0:
		return nil
	case -101, -6:
		return &fetchError{"unauthorized"}
	default:
		return &fetchError{"temporary"}
	}
}

type dynamic struct {
	Card string `json:"card"`
	Desc struct {
		BVID        string `json:"bvid"`
		UserProfile struct {
			Info struct {
				Uname string `json:"uname"`
			} `json:"info"`
		} `json:"user_profile"`
	} `json:"desc"`
}
type video struct {
	AID         json.Number `json:"aid"`
	Title       string      `json:"title"`
	Pic         string      `json:"pic"`
	Description string      `json:"desc"`
	Pubdate     int64       `json:"pubdate"`
}

var bvidPattern = regexp.MustCompile(`^BV[0-9A-Za-z]+$`)

func (c *client) fetch(ctx context.Context, uid, cookie string) ([]byte, error) {
	var login envelope
	if err := c.get(ctx, navURL, uid, cookie, &login); err != nil {
		return nil, err
	}
	if err := apiError(login); err != nil {
		return nil, err
	}
	var account struct {
		IsLogin *bool       `json:"isLogin"`
		MID     json.Number `json:"mid"`
		Uname   string      `json:"uname"`
	}
	if json.Unmarshal(login.Data, &account) != nil || account.IsLogin == nil {
		return nil, &fetchError{"invalid"}
	}
	if !*account.IsLogin {
		return nil, &fetchError{"unauthorized"}
	}
	if !numericID(account.MID.String()) {
		return nil, &fetchError{"invalid"}
	}
	if account.MID.String() != uid {
		return nil, &fetchError{"mismatch"}
	}
	if strings.TrimSpace(account.Uname) == "" {
		return nil, &fetchError{"invalid"}
	}
	var response envelope
	endpoint := videosURL + "?" + url.Values{"uid": {uid}, "type": {"8"}}.Encode()
	if err := c.get(ctx, endpoint, uid, cookie, &response); err != nil {
		return nil, err
	}
	if err := apiError(response); err != nil {
		return nil, err
	}
	var data struct {
		Cards []dynamic `json:"cards"`
	}
	if json.Unmarshal(response.Data, &data) != nil || data.Cards == nil {
		return nil, &fetchError{"invalid"}
	}
	r := rss{Version: "2.0", Channel: rssChannel{Title: account.Uname + " 关注视频动态", Link: "https://t.bilibili.com/?tab=8", Description: "Bilibili 关注视频动态"}}
	seen := map[string]bool{}
	type datedItem struct {
		item rssItem
		date int64
	}
	items := make([]datedItem, 0, len(data.Cards))
	for _, card := range data.Cards {
		var v video
		if json.Unmarshal([]byte(card.Card), &v) != nil || !numericID(v.AID.String()) || v.Pubdate <= 0 || strings.TrimSpace(v.Title) == "" || strings.TrimSpace(card.Desc.UserProfile.Info.Uname) == "" {
			return nil, &fetchError{"invalid"}
		}
		if seen[v.AID.String()] {
			continue
		}
		seen[v.AID.String()] = true
		item := videoItem(v, card.Desc.BVID, card.Desc.UserProfile.Info.Uname)
		items = append(items, datedItem{item, v.Pubdate})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].date != items[j].date {
			return items[i].date > items[j].date
		}
		a, _ := strconv.ParseUint(items[i].item.GUID.Value, 10, 64)
		b, _ := strconv.ParseUint(items[j].item.GUID.Value, 10, 64)
		return a > b
	})
	for _, item := range items {
		r.Channel.Items = append(r.Channel.Items, item.item)
	}
	return encodeRSS(r)
}
func videoItem(v video, bvid, author string) rssItem {
	aid := v.AID.String()
	link := "https://www.bilibili.com/video/av" + aid
	// The mobile player enables autoplay for any nonempty autoplay parameter, including "0".
	player := url.Values{"aid": {aid}}
	if bvidPattern.MatchString(bvid) {
		link = "https://www.bilibili.com/video/" + bvid
		player.Set("bvid", bvid)
	}
	item := rssItem{Title: v.Title, Link: link, GUID: guid{"false", aid}, PubDate: time.Unix(v.Pubdate, 0).UTC().Format(time.RFC1123Z), Author: author}
	cover := coverURL(v.Pic)
	body := `<iframe width="640" height="360" src="` + html.EscapeString("https://www.bilibili.com/blackboard/html5mobileplayer.html?"+player.Encode()) + `" frameborder="0" allowfullscreen></iframe>`
	if cover != "" {
		body += `<p><img src="` + html.EscapeString(cover) + `" alt="` + html.EscapeString(v.Title) + `"></p>`
		contentType := "image/jpeg"
		switch strings.ToLower(path.Ext(strings.SplitN(cover, "?", 2)[0])) {
		case ".png":
			contentType = "image/png"
		case ".webp":
			contentType = "image/webp"
		case ".gif":
			contentType = "image/gif"
		case ".avif":
			contentType = "image/avif"
		}
		item.Enclosure = &enclosure{URL: cover, Type: contentType}
	}
	body += `<p>` + strings.ReplaceAll(html.EscapeString(v.Description), "\n", "<br>\n") + `</p>`
	body += `<p><a href="` + html.EscapeString(link) + `">打开原视频</a></p>`
	item.Description = sanitizer.Sanitize(link, body)
	return item
}
func coverURL(raw string) string {
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return ""
	}
	u.Scheme = "https"
	return u.String()
}
func noticeRSS(old []byte, uid string, generation int64, code string, at int64) ([]byte, error) {
	r := rss{Version: "2.0", Channel: rssChannel{Title: "Bilibili 关注视频动态", Link: "https://t.bilibili.com/?tab=8", Description: "Bilibili 订阅状态"}}
	if len(old) > 0 {
		if err := xml.Unmarshal(old, &r); err != nil {
			return nil, err
		}
	}
	item := rssItem{Title: failureText(code), Description: failureText(code), Link: r.Channel.Link, GUID: guid{"false", fmt.Sprintf("bilibili-notice:%s:%d:%s:%d", uid, generation, code, at)}}
	if at > 0 {
		item.PubDate = time.Unix(at, 0).UTC().Format(time.RFC1123Z)
	}
	r.Channel.Items = append([]rssItem{item}, r.Channel.Items...)
	return encodeRSS(r)
}
