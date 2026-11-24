# mycast — DLNA 投屏中继（手机当中间件）

> **🔄 项目已重启（2026-11-24，分支 `restart/http-first-jsengine`）**
>
> 2026-11-12 冻结的根因是桌面「套皮浏览器」方向撞死在 wails3 / WebKitGTK 上。**复盘发现那 5 条死结一条都不在投屏核心价值链上**——它们是附加体验的代价。
>
> **重启的关键认知：执行取流那段 JS 只需要一个 JS 引擎，不需要 DOM 渲染 / GPU / WebKitGTK。** 以前把「执行 JS」和「渲染页面」绑在 WebView 上，才撞出那串死结。现在拆开：引入嵌入式 JS 引擎（`go-quickjs`，纯 Go 零 CGo），**WebView 从「必需」退到「兜底」**。
>
> 详细选型与踩坑：[`docs/2026-11-24-项目重启-HTTP-first与JS引擎降级链.md`](docs/2026-11-24-项目重启-HTTP-first与JS引擎降级链.md)

把「App 投屏 → 电视」这条经常「越播越慢 / 播不了」的链路，改成手机当中间件：

```
App(B站/百度/抖音/盗版站) ──SSDP发现/SOAP/NVA──> mycast(手机上) ──本地HTTP──> 电视(DLNA)
                                                      │
                                                      ├─ 探测上游(Content-Type/防盗链)
                                                      ├─ HLS: 重写 m3u8 分片到本机代理
                                                      └─ 以本地URL喂电视，电视只拉这条干净的流
```

中间件同时扮演两个角色：

- **对 App**：一台 DLNA MediaRenderer（接收端），App 会 SSDP 搜到我、把播放 URL/命令发给我
- **对电视**：一个 DLNA Controller（发送端），我把取到的流 URL 推给电视，电视自己来拉

## 为什么能解决「越来越慢 / 播不了」

普通投屏卡/慢/黑屏，根因通常是这几类，中间件全都能治：

| 病因 | 中间件怎么治 |
|---|---|
| CDN 要 Referer/UA 防盗链，电视没带 → 403 | proxy 反代：补 Referer/UA + Range |
| HLS 分片是相对路径，电视解析成 `/m/000.ts` → 404 | 分片绝对 URL 用 base64 编码，重写到本机代理 |
| 抖音等 URL 不带 `.m3u8` 后缀 → 电视当 MP4 解析失败 | 按上游 Content-Type 探测识别 HLS |
| AES 加密 / fMP4 初始化段（`EXT-X-KEY`/`EXT-X-MAP`） | 密钥/初始化段也走本机代理 |
| master playlist 多码率 | 子列表走 `/pl/` 递归重写 |
| 山寨/网页客户端发畸形 SOAP（未转义 `&`） | SOAP 解析容错 + 正则兜底 |
| 源流 PTS 漂移 / 时间戳错 → 慢动作 | remux：ffmpeg `-c copy` 重封装成 MPEG-TS |

## 取流降级链（本次重启的核心结构）

以前遇到 JS 混淆的站点就 `return ErrNeedsBrowser` 甩给 WebView 渲染。现在拆成 4 层，WebView 只在最后一层：

```
L0  纯 HTTP + 正则      sniff.ResolveStream 的 5 级（绝对 m3u8 → 绝对 mp4 →
                               player 配置 → 混淆 base64 变体 → iframe 子页）
L1  静态特征判定        sniff.NeedsBrowser（JS 挑战 / 指纹守卫特征）
L2  嵌入式 JS 引擎      执行页面混淆脚本 + 行为观测（hook video.src / fetch / XHR）
L3  WebView 兜底        仅剩真·DOM 渲染 / canvas·WebGL 指纹站
```

**L2 是本次重启的增量**，覆盖范围（影视站嗅探 → 拿流 → 投屏场景）：

| 场景 | L2 能否解 |
|---|---|
| 字符串混淆 / 数组映射 / 十六进制 → 真实 m3u8 | ✅ 最常见 |
| JS 挑战算 cookie（`acw_tc` / `__jsluid` / 瑞数 / 创宇盾） | ✅ |
| 动态签名（token / md5 / timestamp） | ✅ |
| 依赖 `document` / `window` / `navigator` | ⚠️ 已注入最小 DOM stub |
| canvas / WebGL / TLS 指纹检测 | ❌ 只有真浏览器 → L3 |

L2 用**行为观测**而非「还原混淆」：不分析站点怎么算出 URL，而是 hook 住 URL 最终被写到哪（`video.src` setter / `setAttribute` / `fetch` / `XHR.open` / `location.assign`）。**站点无关**，所以不用为每个站写规则。

## 目录结构

```
mycast/
├── go/                  # 业务核心（server.New() 全量 handler，可被壳复用）
│   ├── cmd/mycast/     # 独立进程入口
│   └── internal/
│       ├── jsexec/     # ★ 新增：嵌入式 JS 引擎层（L2，观测型嗅探 + 安全围栏）
│       ├── httptool/   # ★ 新增：CookieJar + 按域持久化 + 抗野请求封控节流
│       ├── ssdp/ renderer/ nva/ upnp/ bili/ media/ medialib/
│       ├── tv/ relay/ sniff/ search/ server/ mcp/
├── android/             # 自写原生 WebView 壳（零 Gradle，纯 aapt2 打包）
├── wails3/              # 桌面壳（L3 WebView 兜底通道，不再做套皮浏览器）
├── archive/wails3/      # 已弃用的旧 wails3 壳（归档保留，不再维护）
├── docs/                # 技术分析与排查记录
└── README.md
```

## 运行

### 手机上（Android，推荐）
用 `android/` 壳打包成 APK 装到手机（见「安卓打包」），或 Termux 直跑：

```bash
# Termux
pkg install golang
cd /path/to/mycast/go
go build -o mycast ./cmd/mycast && ./mycast
```

### 电脑/盒子上
```bash
cd go && go build -o mycast ./cmd/mycast && ./mycast
```

### 交叉编译（零 CGo）
`jsexec` 依赖的 `go-quickjs` 是纯 Go（QuickJS-NG 重写），所以整条链路 **CGO_ENABLED=0** 可编译，linux 目标是纯静态链接：

```bash
CGO_ENABLED=0 GOOS=android GOARCH=arm64 go build ./cmd/mycast   # 手机
CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build ./cmd/mycast   # 盒子/桌面
```

### 环境变量
| 变量 | 默认 | 说明 |
|---|---|---|
| `HCRELAY_PORT` | 8080 | 主 HTTP 端口 |
| `HCRELAY_NVA_PORT` | 9958 | 哔哩必连 TCP 端口 |
| `HCRELAY_IP` | 自动 | 对外 IP（电视能访问到的地址） |
| `HCRELAY_ACCESS_KEY` | 空 | B站 access_key（会员高清可选） |

## 安卓打包（自写 WebView 壳）

壳在 `android/`，产物 `android/dist/mycast-all.apk`（arm64-v8a + x86_64，零 Gradle）。

```bash
cd android
./build-apk.sh            # 纯 aapt2 打包，双 ABI
```

> 桌面壳（`wails3/`）不再承担「套皮浏览器」职责，只做 L3 兜底通道。

安卓已适配：
- `MainActivity.acquireMulticastLock()`：Android 默认丢多播包，SSDP 发现电视必须拿 `WifiManager.MulticastLock`
- 权限：`INTERNET` + `CHANGE_WIFI_MULTICAST_STATE` + `ACCESS_WIFI_STATE`
- `usesCleartextTraffic="true"`：WebView 走 127.0.0.1，电视走局域网 IP 拉 /m/ 流
- 安卓端后端绑 `0.0.0.0`（电视要能拉到本机媒体流）
- JS 桥 `window.hc.sniffRender(url)` + `window.hc.renderDone(html,url)`：L3 渲染回传

细节见 `android/` 源码。

## 打开控制台
浏览器访问 `http://<手机IP>:8080/ui`（移动端四页 SPA）：

- **首页**：状态卡 + 实时状态（目标电视/播放中/速度）
- **操作台**：进度条 + 大播放/暂停按钮 + 音量 + 传输统计
- **设备**：电视气泡选择
- **日志**：实时日志
- 底部 tab 导航，投屏成功后自动进操作台

> 播放模式是**全自动**的（`mode=auto`），后端按上游流自动决定处理方式，
> 普通用户不需要选择。

## 使用流程
1. 手机装 mycast 并打开（SSDP 广播中）
2. 打开 App，投屏 → 应该能搜到「HomeCast Relay」
3. 在 mycast 界面「设备」页把电视设为目标
4. App 开始投屏 → mycast 接住 → 探测/重写 → 推给电视

> 支持的入口：
> - 标准 DLNA（百度、B站普通投屏、抖音等走 SOAP SetAVTransportURI）
> - B站「极速投屏」走哔哩必连(NVA) 私有协议
>
> 不支持的（协议层面，不是中继能解决的）：
> - Google Cast（Chrome 投放按钮，走 Chromecast 协议）
> - Miracast（系统无线投屏 = 屏幕镜像）
> - DRM/Widevine 加密流
> - DASH (.mpd) —— 明确报错，不做假支持

## 测试
```bash
cd go && go test ./...
```
- `TestRelayEndToEnd`：模拟 App SetAVTransportURI → 中间件 → 模拟电视收到本地 URL + Play
- `TestGenericHLSFromAnySource`：7 种任意来源 HLS（无后缀/带query/fMP4/AES/master/绝对路径）
- `TestPirateSiteRefererPassthrough`：盗版站防盗链 Referer 透传端到端
- `TestParseSoapRobust`：畸形 SOAP（未转义 `&`）容错
- `TestRemuxProducesTS`：真实 mp4 → ffmpeg 重封装 → 有效 MPEG-TS（0x47 开头）
- **`TestExecPageConfusedVideoSrc`**（新增）：base64 数组 + eval + `video.src` + `setAttribute` 双写入点，L2 观测型捕获
- **`TestExecPageFetchStub`**（新增）：引擎内 fetch → Go http.Client → 解析 m3u8 取分片
- **`TestExecPageSecurityFence`**（新增）：不可信脚本的死循环被 context 超时打断，而不是挂死进程
- **`TestResolveStreamL2JSEngine`**（新增）：L2 集成——源码里不存在完整 URL 字面量，只有 base64 片段，L0 正则全落空、L2 取出真实地址

## 文档
见 `docs/`：
- `2026-11-24-项目重启-HTTP-first与JS引擎降级链.md` — ★ 本次重启的选型、踩坑、验证
- `2026-11-12-项目归档-冻结-资产与死结全记录.md` — 冻结时的资产与死结清单（历史）
- `2026-10-29-DLNA投屏HLS全404排查修复.md` — HLS 分片 404 根因与修复
- `2026-10-29-手机CPU转码能力实测评估.md` — 手机转码能撑多少码率（实测+估算）

## 说明
- 纯 Go 标准库 + goupnp（控制电视）+ go-quickjs（JS 引擎）；**全部零 CGo**，交叉编译到 Android/arm64 通过
- proxy 模式是默认，零依赖零 CPU（字节搬运）；remux 需 ffmpeg（可选）
- 数据不经过手机解码/转码路径 → 手机只是字节搬运，CPU 占用≈0