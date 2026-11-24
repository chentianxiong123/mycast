# 项目重启：HTTP-first + 嵌入式 JS 引擎降级链

> 本文是项目仓库内的重启记录，与 README 的「取流降级链」章节配套。
> 核心命题：**webview 不是「执行 JS」的必要条件，它只是「渲染 DOM」的必要条件。而取流脚本只需要前者。**

日期：2026-11-24
分支：`restart/http-first-jsengine`（已从 master 切出，仓库已 unarchive）
结论一句话：**执行取流那段 JS 只需要一个 JS 引擎，不需要 DOM 渲染 / GPU / WebKitGTK。** 以前把它们绑在 WebView 上，才撞上一整串 WebKitGTK 死结。

---

## 1. 目标与动机

mycast 在 2026-11-12 归档冻结，冻结原因就是桌面「套皮浏览器」方向（方向 E）撞死在 wails3 / WebKitGTK 上。归档文档 `docs/2026-11-12-项目归档-冻结-资产与死结全记录.md` 里列了 5 条死结：

| # | 死结 | 本质 |
|---|---|---|
| 1 | 改手机 UA 没公开字段 | WebKitGTK 封装限制，要 fork 改源码 |
| 2 | `target=_blank` 点击无反应 | wails3 没连 WebKitGTK 的 `create` 信号 |
| 3 | 跨域导航不生效 | 必须 Go 侧 `SetURL()` |
| 4 | 注入时机丢 | `load-finished` 不触发，导航后 injectJS 丢失 |
| 5 | 裸 WebKitGTK 壳白屏 | GPU/DMABUF 渲染问题 |

复盘时一个关键观察：**这 5 条死结，一条都不在投屏核心价值链上。** 它们是「套皮浏览器」这个附加体验的代价。

那真正的卡点是什么？核心链路是「B站/盗版站 → 拿流 → DLNA 投电视」。拿流这一步，以前遇到 JS 混淆的站就 `return ErrNeedsBrowser`，甩给 WebView 渲染。

于是这次重启就一个命题：**能不能不用 WebView 拿到那些混淆站点的流地址？**

---

## 2. 核心认知 —— JS 混淆 ≠ 需要浏览器

这是整个项目的转折点。

JS 混淆的实质是「**代码被搅乱了，执行后才能拿到真实 URL**」。要执行 JS，只需要一个 **JS 引擎**：不要 DOM，不要 GPU，不要多进程，不要 WebKitGTK。

以前的问题是把两件事绑死了：

```
┌─────────────────────────────────────────────┐
│  WebView 同时承担了                            │
│   (a) 执行取流 JS    ← 其实只要 JS 引擎      │
│   (b) 渲染整页 DOM    ← 才真需要浏览器内核    │
└─────────────────────────────────────────────┘
```

拆开之后降级链变成 4 层，WebView 从「必需」退到「兜底」：

```
L0  纯 HTTP + 正则        ← sniff.ResolveStream 原有 5 级，覆盖大部分站
L1  静态特征判定          ← sniff.NeedsBrowser（JS 挑战/指纹守卫特征）
L2  嵌入式 JS 引擎        ← 新增。跑混淆代码算出 URL，不渲染任何页面
L3  WebView 兜底          ← 只剩真·DOM 渲染/指纹站（canvas/WebGL 指纹）
```

对 mycast 的目标场景（影视站嗅探 → 拿流 → 投屏），**95% 落在 L2 能解的范围**：

| 场景 | L2 能否解 |
|---|---|
| 字符串混淆 / 数组映射 / 十六进制 → 真实 m3u8 | ✅ 最常见 |
| JS 挑战算 cookie（`acw_tc` / `__jsluid` / 瑞数 / 创宇盾） | ✅ 执行脚本拿 cookie 值 |
| 动态签名（token / md5 / timestamp） | ✅ |
| 依赖 `document` / `window` / `navigator` | ⚠️ 需手写最小 DOM stub |
| canvas / WebGL / TLS 指纹检测 | ❌ 只有真浏览器 |
| WebSocket / CDP 取流地址 | ❌ Go 自己起 WS client |

---

## 3. 引擎选型：兼容性到底怎么比

「兼容性」要拆成两层看，混在一起会选错：

| 层面 | 冠军 |
|---|---|
| **语言特性**（能跑多少 ES 语法） | QuickJS（ES2025，test262 近 100%） |
| **工程嵌入**（能进 Go 且跨平台） | go-quickjs（纯 Go）≈ goja（纯 Go）> cgo 系 |
| **Web API**（fetch / DOM / crypto） | ⚠️ 全都一样：都不提供，必须自己 stub |

### 3.1 候选对比（实测/查证，2026-11-24）

| 引擎 | ES 版本 | 语言 | CGo | test262 | 备注 |
|---|---|---|---|---|---|
| **go-quickjs** | ES2020+ 基本完整 | **纯 Go** | **无** | **100,277 / 100,319 通过，0 失败** | QuickJS-NG 的纯 Go 重写；性能 goja 的 10–26×；带 WASM 引擎 |
| QuickJS（bellard，C） | ES2025 | C | 有 | 近 100% | 语言最全，但要 cgo / 交叉编译 |
| quickjs-go / buke（cgo） | ES2020 | Go + cgo | 有 | — | API 不全，cgo 破坏 Android |
| **goja** | **ES5.1 完整 + 部分 ES6（进行中）** | 纯 Go | 无 | 通过已实现的 ES5 部分 | 成熟（3956 项目依赖），但**语言版本落后** |
| Duktape | ES5.1 + 部分 ES2015/16 | C | 有 | — | 极小，适合 IoT，语言老 |

### 3.2 为什么选 go-quickjs 而不是当时第一推荐 goja

我最初推荐的是 **goja**，理由是纯 Go + 零 CGo。但查文档后发现 goja 的定位是：

> `ECMAScript 5.1(+) implementation in Go` … `Most of ES6 functionality, still work in progress`

而影视站 2023 年后的混淆代码大量用现代语法（可选链 `?.`、解构、class 私有成员、`Promise`、`async/await`）。goja 的语言版本会成为覆盖率瓶颈。

**go-quickjs**（`github.com/go-quickjs/go-quickjs` v0.43.0）是 QuickJS-NG 的纯 Go 重写，README 原话：

> `A JavaScript engine for Go, written in pure Go` … `No cgo, no C toolchain, and no C engine compiled to WebAssembly, the JIT included. It cross-compiles anywhere Go does; the JIT runs on linux/amd64, windows/amd64, darwin/arm64 and linux/arm64`

语言层：class 继承 / 私有成员、解构、generators、`async/await`、`Promise`、真 ES 正则、模块（含 top-level await）、Proxy、typed arrays、显式资源管理、proper tail calls 全有。

三个对 mycast 特别加分的点：

1. **纯 Go 零 cgo** → Android arm64 交叉编译零障碍，连 JIT 都在 `linux/arm64` 支持
2. **带 WASM + WASI 引擎** → 影视站用 wasm 混淆也能跑。这是**决定性差异**，其他 Go 引擎全跑不了
3. **带 `jsregexp`（真 ES 正则）** → 混淆代码常用 lookahead / lookbehind，**Go 的 RE2 表达不了**，这是隐性雷

依赖极轻：`go-quickjs v0.43.0` + `go-intl v0.9.0` + `golang.org/x/sys`，没有 C 库。

---

## 4. Spike：先验证「能拿到什么」再动项目

**教训：别凭 README 下结论，先在真机器上跑通。**

临时目录 `/tmp/js-spike`，`go get github.com/go-quickjs/go-quickjs` 后写了 4 个 Part：

```
======== Part A: 混淆代码 -> 拿到真实 m3u8 地址 ========
  混淆代码算出的流地址 = https://cdn.example.com/v/1234/index.m3u8 (err=<nil>)
======== Part B: fetch stub -> Go http.Client ========
  引擎内 fetch 拿 m3u8 -> 解析出首个分片 = /seg/0.ts (err=<nil>)
======== Part C: ES 正则 lookahead/lookbehind ========
  前瞻 (?=)      = 2
  后顾 (?<=)     = 123
  否定前瞻 (?!)= 123
======== Part D: 安全围栏（死循环/资源限制）========
  死循环超时打断: interrupted=true (耗时 2.001s)
  深递归被打断  : interrupted=true
```

**Part D 是安全关键**：执行的是**不可信站点脚本**，没有围栏会被死循环挂死整个投屏进程。go-quickjs 提供三重围栏：

```go
rt := quickjs.New(
    quickjs.WithMemoryLimit(cfg.Memory),   // 内存上限
    quickjs.WithMaxCallDepth(cfg.MaxDepth), // 调用深度上限
)
// 超时：context 由解释器在执行中检查，死循环被打断而不是挂死
ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
rt.EvalContext(ctx, src)
```

实测死循环**精确在 2.001s 被打断**（不是挂死）。

### 4.1 交叉编译验证（这条路线的根基）

```
$ CGO_ENABLED=0 GOOS=android GOARCH=arm64 go build ./...
  android/arm64  OK  (ELF 64-bit LSB pie, ARM aarch64)
  linux/amd64    OK  (statically linked)
  linux/arm64    OK  (statically linked)
  windows/amd64  OK  (PE32+)
```

`CGO_ENABLED=0` 全程生效，linux 目标是**纯静态链接**（`ldd` 报「不是动态可执行文件」）。这条路线和 mycast 已有的 `modernc.org/sqlite`（纯 Go SQLite）风格完全一致——**零 CGo 是硬约束，因为要交叉编译到 Android 真机跑**。

---

## 5. 落地实现

### 5.1 新增 `internal/jsexec/` —— 关键设计是「行为观测型」嗅探

最容易走偏的思路是「**还原混淆**」：分析混淆代码，还原出真实 URL。这条路的坑是**每个站的混淆手法都不同**，要为每站写规则，站点一改就失效。

采用的思路是**行为观测**：不还原，直接**执行**，然后 hook 住执行过程中「URL 被写到哪」：

```go
// 观测点（hook 位置）
video.src setter      ← 最主流的写入点
el.setAttribute('src') ← 播放器初始化常用
fetch(url)            ← 站点靠 fetch 二次请求取流
XHR.open(method,url)  ← 老播放器
location.assign/replace ← 跳转取流
```

这样**站点无关**：不管站方把 URL 存在哪个变量、经过几层函数调用，只要最终写入上面任一处，就能捕获到。

`ExecPage` 的核心动作：从 HTML 抽 `<script>` → 在带 stub 的环境里逐个执行 → `RunJobs()` 驱动微任务队列（`Promise.then` / `async-await` 尾段需要它）→ flush `__captured` 到 Go 侧。

### 5.2 stub 注入分两层

- **Go 侧**（真跨边界的东西）：`atob` / `btoa` / `fetch`（转发到 Go 的 http.Client）/ `crypto`
- **JS 侧**（行为观测 + 最小 DOM 模拟）：`document` / `window` / `navigator` / `location` / `setTimeout` / `console` / `XMLHttpRequest`，以及 `__captured` 捕获器

一个细节：`__mkEl(tag)` 返回的对象用**对象字面量的 getter/setter** 定义 `src`，所以 `_0xv.src = url` 会真正触发 setter 而不是简单赋值：

```js
get src() { return this._src; },
set src(v) { this._src = v; __capture(v, 'video.src'); },
```

### 5.3 新增 `internal/httptool/` —— 抗野请求封控

纯 HTTP 路线的成败关键就是「像不像真人」。野请求封控三大判据与对策：

| 判据 | 对策 |
|---|---|
| 请求频率规律 | **间隔随机抖动**：实际等待 = `Delay * (1 + rand()*Jitter)` |
| 请求头残缺 | 完整浏览器头（含 `Sec-Ch-Ua` / `Sec-Fetch-*` / `Upgrade-Insecure-Requests`） |
| 无会话态 | **Cookie 按域持久化** `cookies/<host>.json`（沿用 `server/cookies.go` 的 `CookieStore` 思路），真实登录态复用 |

另外连接复用（`MaxIdleConnsPerHost: 20` + `IdleConnTimeout: 90s`）——真实浏览器复用 TCP/TLS 连接，每次新建连接本身就是可疑特征。

### 5.4 `sniff.ResolveStream` 加第 6 级降级

原有 5 级是纯 HTTP + 正则（绝对 m3u8 → 绝对 mp4 → player 配置 → 混淆 base64 变体 → iframe 子页），第 6 级插在最后：

```go
// 6. L2 层：嵌入式 JS 引擎执行页面混淆脚本，观测流地址（替代旧 WebView 路径）。
// 纯 HTTP + 正则拿不到时才启用——真实站点大量用混淆 JS 动态算 URL。
if e.cfg.EnableJS {
    if s, mime, err := e.resolveStreamJS(htmlText, playURL); err == nil {
        return s, mime, nil
    }
}
return "", "", errors.New("播放页未找到流地址（m3u8/mp4）")
```

`EnableJS` 默认关，避免给普通站增加开销。

---

## 6. 踩坑记录

这次踩的坑都很具体，记下来省后人时间。

### 坑 1：`JSON.stringify` 之后再 Decode 到 Go slice —— 类型对不上

**现象**：`__captured` 明明有数据（用 `Exec` 内联读能看到），但 `ExecPage` 返回 0 个 hit。

**根因**：flush 里写的是

```go
hits, _ := e.rt.Eval("JSON.stringify(__captured)")  // 拿到的是「字符串」
hits.Decode(&raw)                                     // 解码「字符串」到 []hit → 失败
```

`JSON.stringify` 返回的是**字符串值**，Decode 到 `[]hit` 时类型不匹配，而我静默吞掉了 error。

**解法**：直接取 JS 数组本身，不 stringify：

```go
hits, _ := e.rt.Eval("__captured")  // 拿到 array Value
hits.Decode(&raw)                    // 直接映射到 Go slice
```

### 坑 2：`window` 对象字面量里自引用

**现象**：`TypeError: cannot read property "getComputedStyle" of undefined`

**根因**：

```js
var window = {
  ...
  getComputedStyle: window.getComputedStyle,  // ← 此时 window 还是 undefined！
};
```

对象字面量求值期间 `window` 尚未绑定完成，自引用读不到。

**解法**：删掉这行。`window` 内部已经定义了 `getComputedStyle`，别名没意义。

### 坑 3：相对 URL 解析语义错（绝对路径 vs basePath）

**现象**：base `https://x.example.com/vod/detail/7777` + 相对路径 `/stream/7777/index.m3u8` → 拼出 `https://x.example.com/vod/detail//stream/7777/index.m3u8`（多了个 `/`）

**根因**：RFC 3986 里 `/xxx` 是**绝对路径**，应相对 **origin**（`scheme://host`），不是相对 basePath。

**解法**：

```go
if strings.HasPrefix(raw, "/") {
    if u, err := url.Parse(base); err == nil && u.Host != "" {
        return u.Scheme + "://" + u.Host + raw   // 相对 origin
    }
    ...
}
```

### 坑 4：base64 测试数据写错，导致「误判引擎有问题」

**现象**：测试断言 `.../vod/1234/index.m3u8`，实际捕获 `.../v/1234/index.m3u8`（少了 `od`）；另一版得到 `index.m358`（`u` 变 `5`），甚至被 `looksLikeStream` 正确过滤掉（不含 `.m3u8`）。

**根因**：手写的 base64 片段和期望值不一致（`Y2NuLnJlYWwuZXhhbXBsZS5jb20vdi8=` 解码是 `cdn.real.example.com/v/` 而非 `/vod/`）。

**解法**：用工具生成，别手抄：

```bash
printf '%s' "https://cdn.real.example.com/vod/" | base64 -w0
# aHR0cHM6Ly9jZG4ucmVhbC5leGFtcGxlLmNvbS92b2Qv
```

**这条坑最花时间**：我一度怀疑引擎的 `flush` 有 bug，实际上引擎完全正常，是测试数据本身错。

### 坑 5：`sed` 分隔符冲突

**现象**：`sed -i 's/旧/新/g'` 静默不生效，grep 一看原文没变。

**根因**：替换串里含 `/`（base64 内容不含，但 URL 类内容常含），和 `s/` 的分隔符冲突。

**解法**：换分隔符 `s|旧|新|g`。

### 坑 6：`quickjs.WithMemoryLimit` 要 int64

**现象**：`cannot use cfg.Memory (variable of type int) as int64 value`

**解法**：`Config.Memory` 声明成 `int64`（Go 的 `64 << 20` 是 untyped const，赋给 `int` 字段也会触发这个错）。

### 坑 7：unarchive 仓库 + `gh repo edit --unarchive` 不存在

**现象**：`gh repo edit --unarchive` 报 `unknown flag`；GraphQL `updateRepository(archived:false)` 报 `argumentNotAccepted`。

**解法**：用 REST PATCH（意外可用）：

```bash
gh api -X PATCH repos/chentianxiong123/mycast -f archived=false
```

返回体里 `"archived":false` 即成功。

### 坑 8：git clone GitHub 直连超时

**现象**：`RPC 失败。curl 28 Failed to connect to github.com port 443 after 133448 ms`

**根因**：网络抖动 / IPv6 尝试。`curl -4` 测 `github.com` 是 200（0.5s）。

**解法**：重试并强制 HTTP/1.1 + IPv4：

```bash
git -c http.version=HTTP/1.1 clone https://github.com/... 
```

---

## 7. 验证成果

### 7.1 单元测试（`go test ./...` 全绿）

```
ok  	mycast/internal/bili	0.027s
ok  	mycast/internal/jsexec	0.555s      ← 新增 5 个测试
ok  	mycast/internal/mcp	0.032s
ok  	mycast/internal/media	0.028s
ok  	mycast/internal/medialib	0.005s
ok  	mycast/internal/relay	0.012s
ok  	mycast/internal/server	0.038s
ok  	mycast/internal/sniff	0.029s      ← 新增 L2 集成测试
ok  	mycast/internal/upnp	0.014s
```

jsexec 的 5 个测试：
- `TestExecPageConfusedVideoSrc` — base64 数组 + eval + `video.src` + `setAttribute` 双写入点
- `TestExecPageFetchStub` — 引擎内 fetch → Go http.Client → 解析 m3u8 取分片
- `TestExecPageRelativeURL` — 相对路径按 base 解析为绝对 URL
- `TestExecPageSecurityFence` — 死循环 500ms 被打断
- `TestExecPageCookiePersist` — httptool cookie 目录可写

### 7.2 L2 集成测试（最有说服力的一条）

测试页面里**源码不存在完整 URL 字面量**，只有 base64 片段：

```js
var _0xarr = ["aHR0cHM6Ly9jZG4ucmVhbC5leGFtcGxlLmNvbS92b2Qv", "MTIzNC9pbmRleC5tM3U4"];
function _0xget(n) { return atob(_0xarr[n]); }
var _0xurl = eval('_0xget(0)+_0xget(1)');
var _0xv = document.createElement('video');
_0xv.src = _0xurl;
```

L0 层的 5 级正则会全部落空（源码里没有 `.m3u8` 字面量）。L2 层输出：

```
js_layer_test.go:36: L2 层成功取出流地址: https://cdn.real.example.com/vod/1234/index.m3u8 (mime=application/vnd.apple.mpegurl)
--- PASS
```

这正是以前「只能靠 WebView 渲染整页」才拿得到的东西。

### 7.3 交叉编译

```
CGO_ENABLED=0  android/arm64  OK   linux/amd64  OK (纯静态)
CGO_ENABLED=0  linux/arm64    OK   windows/amd64 OK (PE32+)
```

---

## 8. 决策记录

### 8.1 为什么选 go-quickjs 而不是 goja（当时的第一推荐）

| | goja | go-quickjs |
|---|---|---|
| 语言 | 纯 Go | 纯 Go |
| CGo | 无 | 无 |
| ES 版本 | ES5.1 完整 + 部分 ES6（**进行中**） | ES2020+ 基本完整 |
| test262 | 通过已实现部分 | 100,277 / 100,319，0 失败 |
| WASM | ❌ | ✅（站方 wasm 混淆也能跑） |
| 真 ES 正则 | ❌（RE2 系） | ✅ lookahead / lookbehind |
| 成熟度 | **高**（3956 项目依赖） | 较低（新） |

goja 胜在成熟，但**语言版本会成为影视站混淆代码的覆盖率瓶颈**。go-quickjs 是"纯 Go + 现代语言特性"的双重满足，且 WASM 引擎是决定性差异。

**风险对冲**：以 go-quickjs 为首选，goja 作为兜底方案（双引擎抽象）。若 go-quickjs 在真实站点上出问题，切换成本是一个 Config 字段。

### 8.2 为什么用「行为观测」而不是「还原混淆」

| 思路 | 优点 | 缺点 |
|---|---|---|
| 还原混淆（反混淆规则） | 不需要执行环境 | **每个站手法都不同**，规则爆炸，站方一改版就失效 |
| **行为观测（执行 + hook）** | 站点无关，一次实现通吃 | 需要 DOM stub，执行不可信代码需围栏 |

选了后者。**这是本次重启最重要的技术决策**：把"理解站点逻辑"这个无底洞，换成"观测站点行为"这个有限问题。

### 8.3 为什么 `EnableJS` 默认关

L0 正则路径对普通站（macCMS / 苹果CMS 系）已经够用且零开销。JS 引擎每次 `New` 要建 Runtime、注入 stub，有固定成本。所以只在 `NeedsBrowser` 命中（或 L0 全落空）时才启用。

---

## 9. 未解之谜 / 下一步岔路

### 9.1 ⚠️ 最大未验证项：真实站点命中率

**目前所有测试都是合成页面，没有一个真实盗版站被 L2 打过。**

`looksLikeStream` 的判定是关键词匹配（`.m3u8` / `.mp4` / `.ts` / `.mkv` / `/hls` / `/dash` / `.mpd`），可能对真实站点的流路径（比如 `/api/play?id=xxx` 无扩展名、或 token 化路径）漏判。

下一步最该做的：**拿一两个之前确实只能靠 WebView 搞定的真实站点跑 L2**，统计命中率。建议做成可跑的评测脚本（输入 URL 列表，输出 L2 命中率报告），这样才有量化数据支撑"95% 落在 L2"这个判断。

### 9.2 Web API stub 的边界没摸清

现在 stub 了 `atob` / `btoa` / `fetch` / `navigator` / 最小 `document` / `window` / `crypto`。但真实混淆代码可能用到：

- `XMLHttpRequest` 的完整生命周期（现在只 stub 了 `open`/`send`，`readyState` 状态机没做）
- `document.cookie` 读写（现在是空串，如果站点靠 cookie 传参就断了）
- `localStorage` 持久化（现在是不落盘的 stub）
- `performance.now()` 时间一致性

**这些没测过。** 遇到真站点报错时再补，属于"按需 stub"。

### 9.3 WASM 混淆路径完全没验证

go-quickjs 带 WASM + WASI，理论上站方用 wasm 反混淆也能跑。但**我一行 wasm 混淆代码都没测过**。这是 go-quickjs 相对 goja 的最大理论优势，却是零验证的。

### 9.4 三条岔路（来自归档文档 §4）

| 路线 | 状态 |
|---|---|
| **A. 只做投屏中继** | ← 本次重启走的就是这条，已推进 |
| B. 换技术栈做套皮浏览器（CEF / Tauri / fork wails3） | 未动，桌面套皮浏览器方向仍冻结 |
| C. Android 优先 | 未动，`android/` 壳可继续 |

**注意**：路线 A 不等于放弃套皮浏览器。有了 L2，「影视页自动清洗归一化」可以在纯 HTTP + JS 引擎上做到 80%（集数提取、去广告、拿流），只剩"点搜索结果真跳"这种强 UI 交互才需要真浏览器。这其实是个值得重新评估的方向。

### 9.5 引擎生命周期开销未测

每次 `resolveStreamJS` 都 `jsexec.New` → 建 Runtime → 注入 stub → `Close`。对一个页面这是合理的，但如果是**批量处理**（比如嗅探整季 50 集），重复建 Runtime 的开销需要 benchmark。可考虑 Runtime 池化或复用。

---

## 10. 关键代码位置索引

```
go/internal/jsexec/jsexec.go          ← 新增。JS 引擎层（stub 注入 + 观测型嗅探 + 安全围栏）
go/internal/jsexec/jsexec_test.go     ← 新增。5 个测试
go/internal/httptool/httptool.go      ← 新增。CookieJar + 持久化 + 抗封控节流
go/internal/sniff/sniff.go            ← 改。Config.EnableJS / resolveStreamJS / 第 6 级降级
go/internal/sniff/js_layer_test.go    ← 新增。L2 集成测试
go/internal/server/cookies.go         ← 未动。原有 CookieStore（httptool 沿用了它的思路）
go/internal/mcp/mcp.go                ← 未动。sniff_episodes / cast_to_tv 工具
docs/2026-11-12-项目归档-冻结-资产与死结全记录.md  ← 冻结时的资产与死结清单
```

分支：`restart/http-first-jsengine`，已 push。PR 入口：
`https://github.com/chentianxiong123/mycast/pull/new/restart/http-first-jsengine`

---

## 一句话总结

**webview 不是「执行 JS」的必要条件，它只是「渲染 DOM」的必要条件。** 而取流脚本只需要前者。把两者拆开，5 条 WebKitGTK 死结一条都不用碰。