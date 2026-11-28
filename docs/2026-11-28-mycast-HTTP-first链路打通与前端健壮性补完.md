# HTTP-first 链路打通 + 前端健壮性补完（2026-11-28）

**日期**：2026-11-28
**仓库**：`/mnt/shared/myproject/go`
**状态**：✅ 端到端打通，纯 Web 浏览器（非 mygo）已实测全链路（含 HLS 播放）

## 背景

前一轮把架构改成 HTTP-first（搜索→嗅探→取流→投屏全走 Go，零 CGo），但用户实际点前端时反馈"结果少、点了没反应、什么反馈都没有"。这一轮不猜，直接抓运行日志 + 用真浏览器点链路，把卡点一个个挖出来修掉。

## 问题现象（日志真相）

`~/.mycast/mycast.log` 里反复出现：

```
[sniff] 失败 url=https://sikastone.com/siv/92221.html err=no episodes found
[sniff] 失败 url=https://www.jnzzsy.com/mov/208911.html err=dial tcp 0.0.0.0:443: connection refused
[sniff] 成功 url=https://119tv.com/voddetail/98751.html 集数=30
```

三个站三种死法：
- `119tv` 通（30 集）
- `sikastone` 连得上但提不出集数
- `jnzzsy` 连不上

前端点失败时只有一行不醒目的灰字，甚至有时完全没反应。

## 排查过程（按时间顺序，失败尝试也记）

### 1. 先把"360/sogou/bing cookie 怎么办"查清楚

对照 SearXNG 源码（`searx/engines/*.py`）：
- **360**：`get_cookie()` 预热 `www.so.com/s`（`allow_redirects=False`），抓 `set-cookie` 第一个 `;` 前，缓存 1h；UA 用 `gen_useragent()` 随机 Firefox（os/version 池）
- **bing**：`gen_useragent()` 随机 UA，无持久化 cookie（我这边固定 UA）
- **sogou**：跟 `antispider` 跳转（验证码）
- **baidu**：SearXNG 用 `tn=json`，但 baidu 现已把它改成验证页——**SearXNG 的 baidu 现在也抓不到**，我带 BDUSS 提 HTML 内嵌 JSON 反而更强

顺手查了 SearXNG 社区：社区很大（30k+ star），但公共实例天天被搜索引擎限流（出口 IP 被当 bot）。它是靠 Limiter（Valkey）+ 住宅代理 + 随机 UA 扛的。我这是单用户 + BDUSS 登录态，量极小，4 引擎轮转 + 低频就够，不需要那套重武器。

### 2. `jnzzsy.com` "DNS 死站"的反转

以为是域名死了，实测：
```
getent hosts www.jnzzsy.com → ::          # 本机解析器黑洞
dig @8.8.8.8 www.jnzzsy.com → 104.16.96.77, 162.159.237.184 ...（Cloudflare）
dig @1.1.1.1 / 223.5.5.5 / 119.29.29.29 → 都能解析
```
本机 `/etc/resolv.conf` 是 `nameserver fe80::a:1%wlo1`，本地解析器返回 `::` → 日志里的 `0.0.0.0:443` 就是这么来的。

但绕过 DNS 后：
```
curl --resolve www.jnzzsy.com:443:104.16.96.77 → HTTP 403 "Just a moment..."（Cloudflare Challenge）
真浏览器过验证 → HTTP 444「恭喜，站点创建成功！本页面由系统自动生成」
```
**结论**：域名没死，是本机 DNS 黑洞 + 源站已变成停放页。技术上能解析，但原内容已经没了，没救也不用救。

### 3. L2 为什么没兜底：真根因是压缩乱码

一开始以为 `EnableJS` 没开（确实默认 false）。打开后发现还是提不出，继续挖：

```
sniff.Fetch("https://sikastone.com/siv/92221.html") → 42582 字节，0 个 <a>，开头是二进制乱码
curl 同 URL → 118180 字节正常 HTML，有"第30集"
```

**根因**：`sniff.Fetch` 用的是 `httptool.HTTPClient()` 返回的**裸 `*http.Client`**，底层是 `impersonate.ChromeAndroid` transport，**它自己加 `br/gzip` 但不自动解压** → 上层拿到压缩字节。之前 `search.go` 踩过同样的坑（152KB 无结果），这里没修。

修法：
- `internal/httptool/httptool.go` `applyHeaders()` 加 `Accept-Encoding: identity`
- `internal/sniff/sniff.go Fetch()` 也显式加（裸 client 绕过 applyHeaders）

修完 Fetch: 42KB 乱码 → **118652 字节真 HTML**。

### 4. L2 接进去（真 DOM 渲染）

- `sniff.Default()` 设 `EnableJS: true` → `/api/sniff`、`/api/cast`、MCP、PirateSource 全部生效
- `jsexec/webengine.go` 加 `WERenderedHTML()`：go-webengine 跑页面 JS 后把 DOM 序列化回来
  - **坑**：`dom.OuterHTML(root)` 对 document 节点返回空（root.Type=0），要用 `dom.InnerHTML(root)`；踩过才发现
- `ExtractURL` 降级链改成：**L0 提 → L0.5 跟"立即播放"入口 → L2 JS 渲染再提 → 失败才 ErrNeedsBrowser**

sikastone 的真实形态：详情页根本没有集数列表，只有一个 `/sip/92221-1-1.html`「立即播放」；30 集在播放页里。于是加了通用规则 `followPlayEntry()`：找"立即播放/开始播放/播放全集"等入口 → 跟进播放页提集数（不写死站点）。

**坑**：一开始把 `/sip/` 加进全局 `PlayPath`，结果抓到海报角标「第30集完结」当第 1 集（1 条假集数，L2.5 就不跑了）。改成**只在跟进取到的播放页上临时放宽 PlayPath**，全局不放宽。

### 5. 批量验证命中率

写临时程序（`cmd/batch-sites-check` + `cmd/stream-check`，验完删掉），baidu 带 BDUSS 搜"我的山与海 免费在线播放" → 15 个站，测前 10 个：

| 站点 | 集数 | 耗时 |
|---|---|---|
| cncsps.com | 30 | 6.4s |
| m.hengyirong.com | 30 | 2.6s |
| m.xaruihu.com | 30 | 1.1s |
| szhk66.com | 66 | 3.1s |
| www.1dytt.com | 62 | 2.1s |
| www.acvca.com | 30 | 3.1s |
| www.gzycbdqn.com | 30 | 1.4s |

7/10 提出集数；失败 3 个：`huayaobyq.com`/`nmgxybz.com` 网络超时，`v.youku.com` 正版站（本就不该有集数）。

这 7 个站的首集 `ResolveStream()` **7/7 拿到 m3u8**，逐个 curl 验证 **7/7 HTTP 200、`#EXTM3U` 开头**，无防盗链。

### 6. 分页"页数全都有问题"

用户直接指出。查下来两个 bug：

- **后端**：baidu `rn=20` 但 `pn=页号*10` → 每页 20 条只偏移 10 条，**相邻页重复一半**。SearXNG 是 `rn=10, pn=(pageno-1)*10`，改成一致。
- **前端**：空页时 `srPn--` 后递归重搜（白等一轮），"下一页"永远可点。改成 `srNoMore` 标记：空页保留当前结果、禁用下一页、提示"已是最后一页"。

实测：第 1 页 7 条、第 2 页 7 条、**重叠 0 条**。

### 7. 前端健壮性/反馈（真浏览器点出来的 bug）

用 Playwright 真点链路，挖出这些：

1. **选集索引 bug**：`drawEpGrid` 传 `start`（页偏移）给 `selectEp`，后者又加一次页偏移 → 第 2 页点第 1 集实际选中第 41 集。改成传页内索引，用 40 集假数据实测：第 2 页第 1 个 → **第 31 集**。
2. **`refresh()`/`pollPlay()` 引用已删元素**：模板早把 `st-tv`、`hero-t`、`cast-banner`、`errbar` 删了，函数还在直接 `.textContent` → 每次抛异常 → `.catch` 吞掉误报"后端连接异常"（`_netFails` 累计 34 次，角标永久挂）。全部改 null-safe，实测 `fails=0`。
3. **假成功**：没选电视时 `/api/cast` 仍返回 `ok:true`（它只负责拿流），前端却说"已投屏"并跳遥控器。现在投屏前查 `/api/status`，没电视 → "还没选电视，点底部「设备」选一台再投"。
4. **反馈体系**：
   - 搜索：40s 超时、空页提示、过滤空页提示
   - 嗅探：45s 超时、红条 + toast（连不上 / 提不出 / JS 反爬）
   - 拿流：35s 超时、防重复点击、失败原因写进 `#ep-dt`
   - 断连角标：一次性 toast + 3s 自动重试，恢复即消失
   - 电视选择/扫描/收藏/日志清空/取消收藏：全部补成功/失败反馈

## 纯 Web 也能跑（最重要的一条）

前端模板 grep 下来**没有任何 mygo/wails/JSBridge 调用**（只有 CSS 的 `-webkit-` 前缀）；扫码登录也是后端代理二维码 PNG + 前端 `img`/`fetch` 轮询，纯 HTTP。

用**纯 Chromium（Playwright，不是 mygo）** 打开 `http://127.0.0.1:18080/ui` 实测：

```
Hls 全局 = function（/static/hls.min.js HTTP 200）
搜索 → 7 条结果 + "第1页·7条"
点第一条 → 30 集网格
选第一集 → 本地播放
video.readyState = 4，currentTime = 3.99（真的在播）
```

**mygo 只是窗口壳 + 图标**，不是搜索/嗅探/取流/投屏的依赖。Android 上的 WebView 同理，只是 UI 容器。

## 坑与教训

- **"提不出集数"先查拿到的 HTML 对不对**：这次真根因不是正则或 JS 引擎，是压缩字节没解压。看 `Fetch` 的前几个字节比看错误信息有用。
- **`dom.OuterHTML(document)` 返回空**：document 节点要用 `InnerHTML`。
- **别把站点路径 feature 加进全局规则**：`/sip/` 全局放宽会误抓海报角标；只在跟进取到的播放页放宽。
- **异步链路的假成功比报错更坑**：后端返回 ok 不代表业务成功（没电视、 relays 失败都要前端自己确认）。
- **前端老元素删了要同步清函数**：`refresh/pollPlay` 引用已删 DOM，被 `.catch` 吞成"后端断连"，排查方向完全被带偏。
- **SearXNG 的 baidu `tn=json` 已失效**（返回验证页），别再照抄；带 BDUSS 提 HTML 内嵌 JSON 才是现在能走的路。

## 补记：ArtPlayer 在 mygo(WebKitGTK) 一播放就卡死/“崩了”的真相

换 ArtPlayer 后用户在 mygo 里反馈“一播放就崩了”。别猜，用 mygo 的 `Page().Eval()` 直接在真 WebKitGTK 里复现并取回状态：

```
临时诊断（查完已删）：MYCAST_EVAL_FILE=/tmp/probe.js MYCAST_EVAL_DELAY=4000 ./mycast-mygo
```

结果（关键证据）：
```
ua: Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/60.5 Safari/605.1.15
Hls.isSupported() = true
video.canPlayType('application/vnd.apple.mpegurl') = ""   // 原生不支持 HLS，走 hls.js
before: cur=0, readyState=4, video.error=null
        err=NotAllowedError: The request is not allowed by the user agent or the platform...
```

**真相**：不是进程崩溃（mygo + WebKitWebProcess 都在，无 segfault/OOM），是 **WebKitGTK 的自动播放策略**：
`/api/cast` 是异步 fetch，等拿到流再调 `video.play()` 时**用户手势早就丢了** → `play()` 被拒 → 画面永远卡在 ArtPlayer loading 圈（`00:00 / 46:16`），看起来就像崩了。

另外我的 WebKitGTK 判断也写错了：UA 里**没有 “WebKitGTK” 字样**，只有 `AppleWebKit/605.1.15 Version/60.5`，所以给 WebKit 准备的降配（关 worker/PIP/快捷键）根本没生效。

修法：
1. 检测改成 `/AppleWebKit/i.test(ua) && !/Chrome|Chromium|Edg/i.test(ua)`（真机 UA 验证为 `true`）
2. WebKit 分支：`enableWorker=false`、`lowLatencyMode=false`、关设置面板/PIP/快捷键
3. **`play()` 拒绝时不再 `catch(()=>{})` 吞掉**：显示居中的「▶ 点这里开始播放」按钮，用户点一下补回手势再 `art.play()`
4. `video:play` 事件隐藏按钮、清错误；`closePlayer()` 也清按钮状态

真机复验：
```
修复前: cur=0, tapVisible=true, err=NotAllowedError
点兜底按钮后: cur=2.75, tapVisible=false, err=null   ✅ 正常播放
Chrome 纯 Web 无回归: cur=5.30, tapHidden=true, err=null（自动播放照旧）
```

**教训**：`play().catch(()=>{})` 是前端最坑的静默失败之一——把「被浏览器拒绝」变成「卡在 loading」。异步链路拿用户手势必须准备显式补手势入口。

## 相关文件

- `go/internal/search/search.go`：baidu `rn=10` 分页对齐
- `go/internal/httptool/httptool.go`、`go/internal/sniff/sniff.go`：identity 解压根因修复 + L0/L2/L0.5 降级链 + 前端反馈相关 API
- `go/internal/jsexec/webengine.go`：`WERenderedHTML()` 真 DOM 序列化
- `go/internal/server/web/templates/index.html`：分页、反馈、null-safe、超时重试
- `go/internal/server/server.go`：搜索记录清空 API（内存 + 落盘文件）
