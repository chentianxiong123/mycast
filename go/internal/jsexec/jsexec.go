// Package jsexec 嵌入式 JS 引擎层：执行影视站页面里的混淆脚本，取出真实流地址。
//
// 为什么不用 WebView：
//
//	页面里的取流逻辑本质是"一段 JS 代码算出一个 URL"，执行它只需要一个 JS 引擎，
//	不需要 DOM 渲染 / GPU / 多进程 / WebKitGTK。以前用 WebView 是错把"执行 JS"
//	和"渲染页面"绑在一起，才撞上一串 WebKitGTK 死结（改 UA / target=_blank /
//	注入时机 / 白屏）。这里把两者拆开：
//
//	L0 纯 HTTP + HTML 解析（sniff.Extract，已覆盖大部分站）
//	L1 静态特征判定（sniff.NeedsBrowser）
//	L2 本包：嵌入式 JS 引擎执行混淆代码 ← 新增，webview 之前的唯一执行路径
//	L3 WebView 兜底（仅剩真·DOM 渲染/指纹站）
//
// 引擎选型：go-quickjs（QuickJS-NG 的纯 Go 重写）。
//   - 零 cgo / 零 C 工具链 → android arm64 / linux 任意平台可交叉编译（CGO_ENABLED=0）
//   - 语言特性完整（class/async/Promise/Proxy/typed arrays/真 ES 正则 lookahead）
//   - test262 通过 100,277/100,319，0 失败
//   - 内建 WASM 引擎 → 站方用 wasm 混淆也能跑（其他 Go 引擎全跑不了）
//
// 安全围栏（执行不可信站点脚本必须做）：
//
//	内存上限 + 调用深度上限 + 超时 context（死循环被打断而不是挂死进程）
package jsexec

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/go-quickjs/go-quickjs"
)

// StreamHit 一次捕获到的流地址。
type StreamHit struct {
	URL    string `json:"url"`
	Source string `json:"source"` // video.src / fetch / xhr / location / global
}

// Result 执行结果。
type Result struct {
	Hits    []StreamHit `json:"hits"`    // 捕获到的流地址
	Scripts int         `json:"scripts"` // 实际执行的 script 数
	Error   string      `json:"error,omitempty"`
}

// Config 引擎配置。
type Config struct {
	Timeout  time.Duration                 // 执行超时（0=3s）。死循环在这里被打断
	Memory   int                           // 内存上限字节（0=64MB）
	MaxDepth int                           // 调用深度上限（0=512）
	HTTP     *http.Client                  // fetch stub 用的 HTTP 客户端（可带 cookie/节流/浏览器头）
	BaseURL  string                        // 页面 base（解析相对 URL + 当 fetch 的 referer）
	UA       string                        // navigator.userAgent
	Console  func(format string, a ...any) // 站点脚本的 console.log 输出到哪
}

// AndroidChromeUA 默认 UA（移动端 Chrome）。
const AndroidChromeUA = "Mozilla/5.0 (Linux; Android 13; SM-S9180) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Mobile Safari/537.36"

// Engine 一个隔离的 JS 执行环境。
// 注意：非并发安全（quickjs.Runtime 本身非并发安全）。
type Engine struct {
	rt  *quickjs.Runtime
	cfg Config
}

// New 创建引擎并注入全部 stub。
func New(cfg Config) (*Engine, error) {
	if cfg.Timeout == 0 {
		cfg.Timeout = 3 * time.Second
	}
	if cfg.Memory == 0 {
		cfg.Memory = 64 << 20
	}
	if cfg.MaxDepth == 0 {
		cfg.MaxDepth = 512
	}
	if cfg.UA == "" {
		cfg.UA = AndroidChromeUA
	}
	rt := quickjs.New(
		quickjs.WithMemoryLimit(int64(cfg.Memory)),
		quickjs.WithMaxCallDepth(cfg.MaxDepth),
	)
	e := &Engine{rt: rt, cfg: cfg}
	if err := e.installStubs(); err != nil {
		rt.Close()
		return nil, fmt.Errorf("jsexec: install stubs: %w", err)
	}
	return e, nil
}

// Close 释放运行时。
func (e *Engine) Close() { _ = e.rt.Close() }

// Exec 执行一段 JS 源码，返回脚本表达式的值（字符串化）。
func (e *Engine) Exec(src string) (string, error) {
	v, err := e.eval(src)
	if err != nil {
		return "", err
	}
	return v.String(), nil
}

// eval 带超时的执行。
func (e *Engine) eval(src string) (quickjs.Value, error) {
	ctx, cancel := context.WithTimeout(context.Background(), e.cfg.Timeout)
	defer cancel()
	v, err := e.rt.EvalContext(ctx, src)
	if err != nil {
		return v, err
	}
	// 跑一遍微任务队列（Promise.then / async-await 尾段需要它驱动）
	if e.rt.HasPendingJobs() {
		if err := e.rt.RunJobsContext(ctx); err != nil {
			return v, nil // jobs 失败不影响主返回值
		}
	}
	return v, nil
}

// ExecPage 核心方法：从整页 HTML 里抽出 <script>，在带 stub 的环境里跑，
// 观测执行过程中写入的流地址（video.src / fetch / XHR / location）。
// 这是"行为观测型"嗅探：不依赖站方把 URL 存在哪个变量，所以站点无关。
func (e *Engine) ExecPage(htmlText, baseURL string) *Result {
	res := &Result{}
	scripts := extractScripts(htmlText)
	res.Scripts = len(scripts)
	for _, s := range scripts {
		src := strings.TrimSpace(s)
		if src == "" || isExternalSrc(s) {
			continue
		}
		if _, err := e.eval(src); err != nil {
			// 单个 script 失败继续下一个（站点脚本互相依赖，逐个跑最稳）
			res.Error = err.Error()
			continue
		}
	}
	e.flush(res)
	return res
}

// flush 把 JS 侧的捕获缓冲搬到 Go 侧。
func (e *Engine) flush(res *Result) {
	// 直接取 JS 数组本身（不是 JSON.stringify 后的字符串），Decode 才能映射到 Go slice
	hits, err := e.rt.Eval("__captured")
	if err != nil {
		return
	}
	type hit struct {
		URL    string `js:"url"`
		Source string `js:"source"`
	}
	var raw []hit
	if err := hits.Decode(&raw); err != nil {
		return
	}
	for _, h := range raw {
		if h.URL == "" {
			continue
		}
		u := strings.TrimSpace(h.URL)
		// blob:/data: 不是可投的流
		if strings.HasPrefix(u, "blob:") || strings.HasPrefix(u, "data:") || u == "about:blank" {
			continue
		}
		u = resolveURL(e.cfg.BaseURL, u)
		if !looksLikeStream(u) {
			continue
		}
		res.Hits = append(res.Hits, StreamHit{URL: u, Source: h.Source})
	}
}

// isExternalSrc 判断 <script> 标签是否带 src=（外链脚本，本包不下载执行）。
func isExternalSrc(tag string) bool {
	return regexp.MustCompile(`(?i)\ssrc\s*=`).MatchString(tag)
}

// looksLikeStream 判断一个 URL 是否"像"媒体流。
// 保守判定：只看路径关键词，避免把任意静态资源误判成流。
func looksLikeStream(u string) bool {
	low := strings.ToLower(u)
	for _, k := range []string{".m3u8", ".mp4", ".ts", ".mkv", ".flv", ".webm", "index.m3u", "/hls", "/dash", ".mpd"} {
		if strings.Contains(low, k) {
			return true
		}
	}
	return false
}

// extractScripts 抽出 HTML 里所有 <script> 的内容。
// 注意：外链脚本（带 src=）跳过，因为下载执行不可信脚本风险更高。
func extractScripts(htmlText string) []string {
	re := regexp.MustCompile(`(?is)<script\b([^>]*)>(.*?)</script\s*>`)
	m := re.FindAllStringSubmatch(htmlText, -1)
	out := make([]string, 0, len(m))
	for _, g := range m {
		out = append(out, g[2])
	}
	return out
}

// resolveURL 相对 URL → 绝对 URL。
func resolveURL(base, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return raw
	}
	if raw == "" {
		return ""
	}
	if base == "" {
		return raw
	}
	basePath := base
	if i := strings.Index(basePath, "?"); i >= 0 {
		basePath = basePath[:i]
	}
	if !strings.HasSuffix(basePath, "/") {
		basePath = basePath[:strings.LastIndex(basePath, "/")+1]
	}
	if strings.HasPrefix(raw, "/") {
		// 绝对路径：相对 origin（scheme://host），不是相对 basePath
		if u, err := url.Parse(base); err == nil && u.Host != "" {
			return u.Scheme + "://" + u.Host + raw
		}
		if k := strings.LastIndex(base, "/"); k >= 0 {
			return base[:k+1] + strings.TrimPrefix(raw, "/")
		}
		return raw
	}
	return basePath + raw
}

// installStubs 注入全部 stub。
//
// 分两层：
//   - Go 侧（真跨边界的东西）：atob/btoa/fetch/crypto/random/time
//   - JS 侧（行为观测与 DOM 最小模拟）：document/window/navigator/location/
//     setTimeout/console/XMLHttpRequest，以及 __captured 捕获器
func (e *Engine) installStubs() error {
	rt := e.rt

	// ---- Go 侧 stub ----
	rt.Set("atob", func(s string) string {
		b, _ := base64.StdEncoding.DecodeString(s)
		return string(b)
	})
	rt.Set("btoa", func(s string) string {
		return base64.StdEncoding.EncodeToString([]byte(s))
	})
	rt.Set("unescape", func(s string) string { return s })
	rt.Set("escape", func(s string) string { return s })

	// fetch：转发到 Go 的 http.Client（httptool 带 cookie/节流/浏览器头）
	hc := e.cfg.HTTP
	rt.Set("fetch", func(rawurl string) string {
		u := resolveURL(e.cfg.BaseURL, rawurl)
		// 观测型捕获：站点靠 fetch 取流时，这里就拿到真实地址
		if looksLikeStream(u) {
			capture(rt, u, "fetch")
		}
		if hc == nil {
			return ""
		}
		req, rerr := http.NewRequest(http.MethodGet, u, nil)
		if rerr != nil {
			return ""
		}
		resp, err := hc.Do(req)
		if err != nil {
			return ""
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	})

	// crypto.getRandomValues（混淆代码常用来生成变量名/加盐）
	rt.Set("cryptoObj", map[string]any{
		"getRandomValues": func(b []byte) []byte {
			for i := range b {
				b[i] = byte(time.Now().UnixNano()>>uint(i%8)) ^ byte(i)
			}
			return b
		},
	})

	rt.Set("DateNow", func() int64 { return time.Now().UnixMilli() })

	// ---- JS 侧 bootstrap：捕获器 + 最小 DOM/浏览器对象 ----
	bootstrap := `
	// ===== 捕获器 =====
	var __captured = [];
	function __capture(u, src) { if (u) __captured.push({url: u, source: src}); }

	// ===== 最小 DOM =====
	function __mkEl(tag) {
	  return {
	    tagName: (tag||'div').toUpperCase(),
	    _src: '',
	    get src() { return this._src; },
	    set src(v) { this._src = v; __capture(v, 'video.src'); },
	    _href: '',
	    get href() { return this._href; },
	    set href(v) { this._href = v; __capture(v, 'a.href'); },
	    _innerHTML: '',
	    get innerHTML() { return this._innerHTML; },
	    set innerHTML(v) { this._innerHTML = String(v); },
	    textContent: '', value: '',
	    style: { setProperty: function(){}, },
	    className: '',
	    classList: { add: function(){}, remove: function(){}, contains: function(){return false;} },
	    dataset: {},
	    attributes: {},
	    children: [], childNodes: [],
	    setAttribute: function(k, v) { this.attributes[k] = v; if (k==='src'||k==='href') __capture(v, 'setAttribute'); },
	    getAttribute: function(k) { return this.attributes[k] === undefined ? null : this.attributes[k]; },
	    appendChild: function(c) { this.children.push(c); return c; },
	    removeChild: function() {},
	    insertBefore: function() {},
	    querySelector: function() { return null; },
	    querySelectorAll: function() { return []; },
	    addEventListener: function() {},
	    removeEventListener: function() {},
	    cloneNode: function() { return __mkEl(this.tagName); },
	    getBoundingClientRect: function() { return {width:360,height:240,top:0,left:0,right:360,bottom:240}; },
	    focus: function() {}, play: function(){ return Promise.resolve(); },
	    pause: function() {}, load: function() {},
	    readyState: 4,
	    content: null, contentWindow: null,
	    parentNode: null, parentElement: null, nextSibling: null,
	  };
	}
	var __body = __mkEl('body'), __html = __mkEl('html');
	__html.appendChild(__body);

	var __video = __mkEl('video');
	var document = {
	  _ready: true,
	  readyState: 'complete',
	  documentElement: __html,
	  body: __body,
	  head: __mkEl('head'),
	  title: '',
	  URL: '', documentURI: '', referrer: '',
	  cookie: '',
	  location: null,
	  createElement: function(t) { return __mkEl(String(t||'div').toLowerCase()); },
	  createDocumentFragment: function() { return __mkEl('#fragment'); },
	  createTextNode: function(t) { return { textContent: String(t), nodeType: 3 }; },
	  getElementById: function(id) { return __body.children[0] || __mkEl('div'); },
	  getElementsByTagName: function() { return [__body]; },
	  getElementsByClassName: function() { return []; },
	  querySelector: function() { return __body.children[0] || __mkEl('div'); },
	  querySelectorAll: function() { return []; },
	  addEventListener: function() {},
	  removeEventListener: function() {},
	  execCommand: function() { return true; },
	  visibilityState: 'visible',
	  hidden: false,
	  fonts: { ready: Promise.resolve() },
	};
	document.location = {
	  href: '', protocol: 'https:', host: '', hostname: '',
	  pathname: '/', search: '', hash: '', origin: '',
	  assign: function(u) { __capture(u, 'location.assign'); },
	  replace: function(u) { __capture(u, 'location.replace'); },
	  reload: function() {},
	};

	var navigator = {
	  userAgent: ` + jsQuote(e.cfg.UA) + `,
	  appVersion: '5.0',
	  appName: 'Netscape',
	  platform: 'Linux armv8l',
	  product: 'Gecko',
	  productSub: '20030107',
	  vendor: 'Google Inc.',
	  vendorSub: '',
	  language: 'zh-CN',
	  languages: ['zh-CN', 'zh', 'en'],
	  hardwareConcurrency: 8,
	  maxTouchPoints: 5,
	  cookieEnabled: true,
	  onLine: true,
	  userAgentData: { brands: [], mobile: true, platform: 'Android' },
	  connection: { effectiveType: '4g', downlink: 10, rtt: 50, saveData: false },
	  getBattery: function() { return Promise.resolve({ level: 0.8, charging: false }); },
	};

	var window = {
	  self: null, top: null, parent: null, frames: [],
	  length: 0,
	  location: document.location,
	  navigator: navigator,
	  document: document,
	  history: { pushState: function(){}, replaceState: function(){}, go: function(){}, back: function(){}, state: null, length: 1 },
	  screen: { width: 1080, height: 1920, availWidth: 1080, availHeight: 1748, colorDepth: 24, pixelDepth: 24, orientation: { type: 'portrait-primary', angle: 0 } },
	  innerWidth: 360, innerHeight: 640, outerWidth: 360, outerHeight: 640,
	  devicePixelRatio: 2.625,
	  scrollX: 0, scrollY: 0, pageXOffset: 0, pageYOffset: 0,
	  visualViewport: { width: 360, height: 640, offsetLeft: 0, offsetTop: 0 },
	  performance: { now: function(){ return Date.now(); }, timeOrigin: Date.now(), timing: { navigationStart: Date.now(), fetchStart: Date.now(), domContentLoadedEventStart: Date.now() } },
	  setTimeout: function(fn, ms) { return 1; },
	  clearTimeout: function() {},
	  setInterval: function(fn, ms) { return 1; },
	  clearInterval: function() {},
	  requestAnimationFrame: function(fn) { fn(0); return 1; },
	  cancelAnimationFrame: function() {},
	  queueMicrotask: function(fn) { try { fn(); } catch(e) {} },
	  addEventListener: function() {},
	  removeEventListener: function() {},
	  matchMedia: function() { return { matches: false, media: '', addListener: function(){}, removeListener: function(){} }; },
	  getComputedStyle: function() { return { getPropertyValue: function(){ return ''; } }; },
	  localStorage: { getItem: function(){ return null; }, setItem: function(){}, removeItem: function(){}, clear: function(){} },
	  sessionStorage: { getItem: function(){ return null; }, setItem: function(){}, removeItem: function(){}, clear: function(){} },
	  indexDB: undefined,
	  indexedDB: undefined,
	  cache: undefined,
	  caches: { open: function(){ return Promise.resolve(undefined); }, keys: function(){ return Promise.resolve([]); } },
	  crypto: {
	    getRandomValues: function(arr) { for (var i=0;i<arr.length;i++){ arr[i]=Math.floor(Math.random()*256); } return arr; },
	    randomUUID: function() { return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, function(c){ var r=Math.random()*16|0; var v = c=='x'?r:(r&0x3|0x8); return v.toString(16); }); },
	  },
	  XMLHttpRequest: function() { return __mkXHR(); },
	  Image: function() { return __mkEl('img'); },
	  Worker: undefined, SharedWorker: undefined,
	  WebSocket: undefined,
	  focus: function() {}, blur: function() {},
	  scrollTo: function() {}, scrollBy: function() {},
	  open: function() { return null; }, close: function() {},
	  print: function() {},
	  postMessage: function() {},
	  atob: atob, btoa: btoa,
	  eval: function(s) { return eval(s); },
	};
	window.self = window; window.top = window; window.parent = window;
	__body.innerHTML = '<div id="root"></div>';

	// console
	var console = {
	  log: function() {}, info: function() {}, warn: function() {}, error: function() {},
	  debug: function() {}, trace: function() {}, dir: function() {},
	  table: function() {}, assert: function() {}, clear: function() {}, count: function() {},
	};

	// XMLHttpRequest 最小实现（捕获请求 URL）
	function __mkXHR() {
	  var self = { readyState: 0, status: 0, responseText: '', responseXML: null, response: '', responseType: '', timeout: 0, withCredentials: false, upload: { addEventListener: function(){} }, headers: {} };
	  self.addEventListener = function() {};
	  self.removeEventListener = function() {};
	  self.open = function(m, u, async) { self.method = m; self.url = u; __capture(u, 'xhr'); };
	  self.send = function() { self.readyState = 4; self.status = 200; if (self.onreadystatechange) self.onreadystatechange(); };
	  self.setRequestHeader = function(k, v) { self.headers[k] = v; };
	  self.getResponseHeader = function() { return null; };
	  self.getAllResponseHeaders = function() { return ''; };
	  self.abort = function() {};
	  return self;
	}

	// 顶层别名（混淆代码常直接引用）
	var XMLSerializer = function() { return { serializeToString: function() { return ''; } }; };
	var FormData = function() { return { append: function(){}, set: function(){} }; };
	var Blob = function(parts, opts) { return { size: 0, type: opts ? (opts.type||'') : '', slice: function(){ return new Blob(); }, text: function(){ return Promise.resolve(''); } }; };
	var File = function() { return new Blob(); };
	var Event = function(t) { return { type: t, target: null, preventDefault: function(){}, stopPropagation: function(){} }; };
	var CustomEvent = function(t, o) { var e = new Event(t); e.detail = o ? o.detail : undefined; return e; };
	var TextEncoder = function() { return { encode: function(s) { return new Uint8Array(0); } }; };
	var TextDecoder = function() { return { decode: function() { return ''; } }; };
	var MutationObserver = function() { return { observe: function(){}, disconnect: function(){}, takeRecords: function(){ return []; } }; };
	var IntersectionObserver = function() { return { observe: function(){}, unobserve: function(){}, disconnect: function(){} }; };
	var ResizeObserver = function() { return { observe: function(){}, disconnect: function(){} }; };
	var History = function() {};
	var Worker = undefined;
	var MediaSource = undefined;
	var videojs = undefined;
	var DPlayer = undefined;
	var CKPlayer = undefined;
	`
	if _, err := rt.Eval(bootstrap); err != nil {
		return fmt.Errorf("bootstrap: %w", err)
	}
	return nil
}

// jsQuote 把一个 Go 字符串安全地变成 JS 字符串字面量。
func jsQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return `'` + s + `'`
}

// capture 从 Go 侧往 __captured 里塞一条（fetch stub 用）。
func capture(rt *quickjs.Runtime, u, source string) {
	src := fmt.Sprintf("__capture(%s, %s);", jsQuote(u), jsQuote(source))
	_, _ = rt.Eval(src)
}

// appendFetchLog 更新 __fetchLog（fetch stub 里用）。
func appendFetchLog(entry map[string]string) map[string]string { return entry }
