# MyCast

> **项目状态：停止维护（冻结，2026-11-28）。** 最终交付见 [`docs/2026-11-28-mycast-最终总结-HTTP-first全链路与停维归档.md`](docs/2026-11-28-mycast-最终总结-HTTP-first全链路与停维归档.md)。

**一句话简介**：纯 Go、零 CGo 的 HTTP-first 影视资源聚合 + DLNA 投屏中继——扫码登录、四引擎搜索、真站嗅探取流、本地播放或真 URL 直推电视，收藏/封面/简介/进度全部本地持久化。

桌面通过 mygo（WebKitGTK）承载 UI，安卓通过 WebView 承载 UI；**搜索、嗅探、取流、投屏全走 Go HTTP，不依赖 WebView**。纯浏览器打开 `/ui` 也能跑完整链路。

## 主链路

```
百度扫码登录 → 搜索（baidu / bing / 360 / sogou）
     → 点结果进详情页（真封面 + 真元数据 + 集数网格）
           ├─ 📱 本地播放：ArtPlayer + hls.js + /api/stream 代理
           └─ ▶ 投屏：选电视 → 真 m3u8/mp4 URL 直推 DLNA → 遥控器页
                              ↓
                     ⭐ 手动收藏 → 媒体库（继续观看 + 海报墙）
```

页面：底部导航 **搜索 / 媒体库 / 设置 / 日志**；层叠页 **详情 / 设备 / 遥控器**（从流程进入，带返回）。

## 关键特性

- **HTTP-first 搜索**：4 引擎严格对齐 SearXNG；baidu 带 BDUSS 直解结果页 JSON，无 WebView 渲染
- **嗅探降级链**：L0 正则 → L0.5 跟播放入口 → L2 go-webengine 真 DOM → `ErrNeedsBrowser`
- **无中转直推**：拿流后把真 URL 直接 `SetAVTransportURI` 给电视，不再有本地 `/m/` 会话
- **不造假**：进度为 0 不画进度条；元数据抓不到就不显示；投屏失败红条报错；只有用户点 ⭐ 才进媒体库
- **全量本地缓存**：SQLite 收藏/进度/搜索历史；封面落 `~/data/poster-cache/`；元数据落 `~/data/media-cache/`
- **现代播放器**：本地内置 ArtPlayer 5.2.5 + hls.js，WebKitGTK 自动播放被拒时有补手势按钮
- **响应式**：手机窄屏收缩封面/海报/集数列数，页面结构不变

## 快速开始

```bash
cd go
go build -o mycast ./cmd/mycast && ./mycast -no-open       # HTTP 版，浏览器开 http://127.0.0.1:18080/ui
go build -tags mygo -o mycast-mygo ./cmd/mycast-mygo && ./mycast-mygo  # mygo 桌面版
```

零 CGo 交叉编译（Android arm64，约 80MB）：

```bash
CGO_ENABLED=0 GOOS=android GOARCH=arm64 go build -o mycast-android-arm64 ./cmd/mycast
```

> 注意：`go build ./...` 在 Android 目标下会因 `purego` 的 cgo 符号失败；项目实际构建目标是 `./cmd/mycast`。

数据目录：`~/data/` —— `cookies/`、`library.db`、`search-hist.json`、`poster-cache/`、`media-cache/`。

## 目录结构

```
mycast/
├── go/                       # 全部业务核心（零 CGo）
│   ├── cmd/mycast/           # 独立进程入口（桌面/安卓共用）
│   ├── cmd/mycast-mygo/      # mygo 桌面壳入口
│   ├── assets/               # 图标
│   └── internal/
│       ├── baidulogin/       # 百度扫码 HTTP 协议
│       ├── search/           # 4 引擎 HTTP 搜索
│       ├── sniff/ jsexec/    # 嗅探 + L2 JS 引擎
│       ├── meta/             # JSON-LD/meta/苹果CMS 元数据提取
│       ├── relay/ tv/ upnp/  # 拿流 + DLNA 直推
│       ├── media/ httptool/  # 流代理工具
│       ├── server/           # HTTP handler + 单模板 UI + SQLite
│       └── ssdp/ bili/ medialib/ mcp/
├── docs/                     # 技术分析与排查记录（索引见 docs/README.md）
└── README.md
```

## 验证

- `go test ./internal/...` 全量通过
- 真浏览器端到端：搜索 → 30 集 → 本地播放 `readyState=4`、进度推进
- 纯 Web（无 mygo 专有 API）：完整链路可用
- Android arm64 零 CGo 构建通过

## 限制

- 第三方资源站结构/WAF 随时变化；个别站超时只能换结果
- 真 URL 直推方案对强防盗链站有固有取舍（电视需能直接拉到流）
- 安卓 APK 未做真机全量回归，仅保证交叉编译

## 文档

从 [文档索引](docs/README.md) 开始；推荐先读 [最终总结](docs/2026-11-28-mycast-最终总结-HTTP-first全链路与停维归档.md)。
