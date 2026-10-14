# Bilibili 关注视频 RSS

接口：`GET /rss/bilibili/:uid`。配置了 BasePath 时，在路径前添加该前缀，例如：

```text
https://your-yarr.example/reader/rss/bilibili/123456
```

这是登录账号所关注 UP 主发布的视频动态，不是该 UID 自己的投稿。接口无需 yarr 登录 Cookie；完整订阅链接只包含 UID，不包含 Bilibili Cookie。

## 配置

在 **系统设置 > 数据与安全 > Bilibili 关注视频** 中，粘贴浏览器请求里的完整 Cookie 值（不加 `Cookie:` 字段名），点击保存。输入框支持长文本，保存后清空，不回显已保存的 Cookie。清空按钮会删除当前凭据。

仅支持一个登录 Cookie。订阅链接中的 UID 必须与 Cookie 的登录账号一致；每次完整抓取先调用登录状态接口核对账号，并使用账号名称作为 RSS 标题。保存配置本身不访问 Bilibili，下次 RSS 请求才验证和抓取。

Cookie 保存在 SQLite 中，随完整数据库备份保存；不会进入普通设置 API、页面初始化数据、RSS、日志、错误文案或 OPML。启用访问认证时，配置接口和网页设置需要登录；公开 RSS 接口仍可访问。

配置 API：

- `GET /api/bilibili`：只返回 `{"configured": true}` 或 `{"configured": false}`。
- `PUT /api/bilibili`：提交 JSON `{"cookie": "完整 Cookie"}`。提交空字符串表示清空；缺少字段、非字符串或控制字符会返回 HTTP 400。

替换或清空 Cookie 会立即使旧内容缓存失效、取消旧任务并递增配置版本。旧任务即使稍后结束，也不能覆盖新配置的缓存。再次保存相同 Cookie 也会重新开始验证。

现有 RSSHub 的 Bilibili UID 快速添加功能仍表示“该 UP 主的投稿”，不是这个接口。在新订阅源中输入以上完整 RSS 链接即可订阅关注视频。

## 内容

按 RSSHub 的关注视频路由读取上游返回的一页全部视频，按发布时间倒序，不固定条数、不分页、不逐视频补详情，也不累积历史。视频 aid 是稳定 GUID，优先使用 BV 原文链接，缺少 BV 时使用 AV 链接。

正文依次显示不自动播放的 Bilibili 简洁嵌入播放器、HTTPS 封面、转义后的简介，最后附原视频链接。播放器使用 `https://www.bilibili.com/blackboard/html5mobileplayer.html`，iframe 尺寸为 640×360，无边框并允许全屏。封面同时写入标准 RSS `<enclosure>` 图片标签，供文章列表显示；文件大小未知时为 0，不额外下载图片查询大小。播放器是否能播放取决于阅读器的 iframe 支持和 Bilibili 的访问限制。

空视频列表是有效 RSS；缺失列表字段、异常嵌套 JSON 或不完整视频信息视为失败，不覆盖上次完整结果。

## 缓存与请求

- 完整成功后随机缓存 **5～10 分钟**。有效期内直接返回缓存，不验证 Cookie、不请求 Bilibili。
- 缓存过期后立即返回旧内容，由此次访问触发后台刷新；同一 UID 和配置版本的并发请求共用一个任务。
- 全天可抓取，无夜间暂停，也无定时抓取。首次访问最多等待 25 秒；未完成则返回 HTTP 503 和 `Retry-After: 30`，后台继续，稍后重试。
- 后台任务最多 3 分钟，包括排队、等待和重试。客户端断开不取消任务。
- 当前 yarr 进程内的 Bilibili 请求独立串行，每次结束后随机间隔 3～7 秒，携带浏览器 User-Agent 和对应账号空间 Referer；不会把 Cookie 转发到重定向地址。
- 网络错误及 5xx 最多重试两次，额外等待 5 秒、10 秒。其他失败冷却至少 15 分钟；429 按上游要求等待，更长限流截止时间持久化，重启不绕过。
- RSS 内容和故障状态跨重启保留；连续 30 天未访问的缓存在启动和每日清理时删除。HTTP 响应带 `Cache-Control: no-store`，由服务内部管理缓存。

## 提示与兼容性

未配置 Cookie、已确认登录失效、或 UID 与账号不匹配时，返回 HTTP 200 RSS 提示文章，便于阅读器显示。同一次故障使用固定 GUID。登录失效时保留同一配置下的旧视频并停止自动重试，替换 Cookie 后恢复；UID 不匹配只返回提示，不返回视频。

未知业务错误、响应格式变化和 `4100000` 不直接判定为 Cookie 失效。暂时失败时保留旧内容，无旧内容则返回 HTTP 503 和中文提示。有效缓存内不会主动发现 Cookie 失效，后台发现错误后会在下一次订阅请求中显示。

源码参考：

- [RSSHub followings-video.ts](https://github.com/DIYgod/RSSHub/blob/master/lib/routes/bilibili/followings-video.ts)

目前沿用参考代码的旧版关注动态接口，未移植 WBI 用户名查询或浏览器脚本解析。上游接口可能变化；自动化测试使用模拟响应，不要求真实 Cookie，也不证明上游在所有账号下可用。
