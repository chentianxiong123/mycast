# MyCast 最终总结：HTTP-first 全链路与停维归档

> 状态：**停止维护（冻结）**。本文是最终交付说明，后续不再新增功能。
> 代码最终提交：`3cec996`（其后为最后一批 UI/缓存收尾改动）。

---

## 1. 这个项目最终做成了什么

一个**纯 Go、零 CGo** 的 DLNA 投屏中继 + 资源聚合器：

```
百度扫码登录 → 4 引擎搜索 → 点结果进详情 → 选集
                                      ├─ 📱 本地播放（ArtPlayer + hls.js）
                                      └─ ▶ 投屏（选电视 → 真 URL 直推 → 遥控器）
                                             ↓
                                    ⭐ 手动收藏 → 媒体库（封面/简介/进度持久化）
```

桌面用 **mygo**（WebKitGTK 容器），安卓用同一个 Go 核心 + WebView 容器；
**搜索/嗅探/取流/投屏全部不依赖 WebView**，纯浏览器打开 `/ui` 也能跑完整链路。

---

## 2. 最终架构（HTTP-first）

| 层 | 实现 | 关键路径 |
|---|---|---|
| 登录 | 百度扫码 HTTP 协议，后端代理二维码 PNG + 轮询 BDUSS | `internal/baidulogin` |
| 搜索 | 4 引擎（baidu/bing/360/sogou）直连 HTTP，对齐 SearXNG 源码；baidu 带 BDUSS 解 HTML 内嵌 JSON | `internal/search/search.go` |
| 嗅探 | L0 正则 → L0.5 跟「立即播放」入口 → L2 go-webengine 真 DOM → `ErrNeedsBrowser` | `internal/sniff/sniff.go` |
| 元数据 | JSON-LD / meta itemprop / 苹果CMS 提取，**落盘缓存** `~/data/media-cache/<sha256>.json` | `internal/meta` |
| 取流 | `/api/cast` 只拿流（本地播放用）；`/api/cast/push` 拿流+直推电视 | `internal/relay/pirate.go` |
| 投屏 | 真 m3u8/mp4 URL 直接 `SetAVTransportURI` → DLNA 电视，**无本地 `/m/` 中转会话** | `internal/tv/controller.go` |
| 本地播放 | `/api/stream` 轻量代理（CORS + 防盗链），m3u8 走路径式 `/api/stream/s/<token>/...` | `internal/server/server.go` |
| 持久化 | SQLite（modernc.org/sqlite，纯 Go）：收藏、进度、搜索历史 | `internal/server/library.go` |
| 封面 | `/api/poster` 代理 + 落盘 `~/data/poster-cache/<sha256>` | `internal/server/server.go` |
| UI | 单模板 + ArtPlayer 本地内置，6 个普通 page + 3 个层叠页，响应式 | `internal/server/web/templates/index.html` |

---

## 3. 页面结构（最终）

底部导航只有 4 项：**搜索 / 媒体库 / 设置 / 日志**。
层叠页（无 tab，靠流程进出，带「← 返回」）：**详情 / 设备 / 遥控器**。

详情页布局：顶部「左封面 2:3 + 右元数据」同一行 → 操作胶囊条 → 集数网格，遵守统一页面标题/卡片/按钮规范。

---

## 4. 关键决策与踩坑（沉淀）

1. **SearXNG 对齐**：只维护 baidu/bing/360/sogou；其 `tn=json` 已失效，改用 `www.baidu.com/s` 桌面 HTML + BDUSS 直解 JSON `"url"`。
2. **轻头纯 HTTP**：资源站只用 UA，全套 impersonate 头会触发 WAF 403；压缩必须 `Accept-Encoding: identity`（否则 br/gzip 字节不解压 → 乱码）。
3. **不造假**：进度为 0 就不画进度条；元数据抓不到就不显示；收藏只由用户点 ⭐；投屏 SOAP 失败就红条报错，不假成功。
4. **投屏 controlURL 反转**：国产电视（HappyCast）description.xml 给 `/_urn:...AVTransport_control`，看着错其实对；盲目换候选路径反而 `Invalid Action`。最终：原始地址优先 + 连接失败重试 3 次。
5. **本地播放 CORS**：砍掉 `/m/` 中转后 hls.js 直接拉远程 ts 被 CORS 拦死；自定义 loader subclass 静默失效；最终用路径式代理让相对片段自然落在代理路径下。
6. **WebKitGTK 自动播放**：异步拿流后用户手势丢失 → `play()` `NotAllowedError` → 卡在 loading 圈。降配（关 worker/PIP/快捷键）+ 显式「点这里播放」兜底。
7. **接口拆分**：本地播放和投屏共用 `/api/cast` 会互相污染，必须拆成「只拿流」和「拿流+直推」。

---

## 5. 验证结论（最后一次全量）

- `go test ./internal/...` 全部通过（含 jsexec/meta/sniff 长测）
- Android arm64 零 CGO 构建通过，约 80MB
- 真浏览器端到端：搜索 7 条 → 30 集 → 本地播放 `currentTime=10.96s / readyState=4`
- 封面缓存：miss → hit；元数据缓存：重启后 `cached=true`、重复 sniff 缓存文件 mtime 不变
- 纯 Web 浏览器可跑完整链路，无 mygo 专有 API

---

## 6. 已知限制（停维原因）

- 资源站是第三方站点，结构/WAF 随时变；个别站超时（如 Cloudflare 后面的慢站）只能换结果，不是本项目的 bug
- 投屏为「真 URL 直推」，部分强防盗链站电视可能拉不到流；这是无中转方案的固有取舍
- 安卓 APK 未做真机全量回归，仅保证交叉编译通过
- 不再接受新需求与重构

---

## 7. 如何运行

```bash
cd go
# 桌面 HTTP 版
go build -o mycast ./cmd/mycast && ./mycast -no-open
# 桌面 mygo 版
go build -tags mygo -o mycast-mygo ./cmd/mycast-mygo && ./mycast-mygo
# 安卓
CGO_ENABLED=0 GOOS=android GOARCH=arm64 go build -o mycast.apk ./cmd/mycast
```

Web 入口：`http://127.0.0.1:18080/ui`（mygo 内为 `app://localhost/`）。
数据目录：`~/data/`（cookies / library.db / poster-cache / media-cache / search-hist.json）。
