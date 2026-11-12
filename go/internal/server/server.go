// Package server 组装 mycast 全部后端 handler（HTTP 服务）。
// 被 cmd/mycast（独立进程）和 wails3 壳（安卓/桌面内嵌）复用。
package server

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"mycast/internal/bili"
	"mycast/internal/mcp"
	"mycast/internal/media"
	"mycast/internal/relay"
	"mycast/internal/search"
	"mycast/internal/sniff"
	"mycast/internal/ssdp"
)

//go:embed web/templates/*.html
var tplFS embed.FS

//go:embed web/static/hls.min.js
var hlsJS []byte

// Port 默认端口（小众端口：避开 8080/8000/3000/5000 常见位，防局域网冲突）
const Port = "18080"

// Instance 一次完整装配（服务端 + 后台任务），可 Stop
type Instance struct {
	Handler http.Handler
	BaseURL string
	Relay   *relay.Relay
	Sniff   *sniff.Extractor
	Search  *search.Aggregator

	// WebView 兜底回传结果（JS 拿 outerHTML → POST /api/sniff/render → 这里存）
	sniffMu     sync.Mutex
	sniffResult []sniff.Episode
	sniffURL    string

	// 纯页面渲染通道：点击搜索结果 → 子 WebView 渲染真实页 → outerHTML 存这，前端直接显示（不归一化/不去广告）
	pageMu       sync.Mutex
	pageHTML     string
	pageURL      string
	pageIsVideo  bool            // 方向B：DetectVideoPage 判断（点结果→识别影视页）
	pageEps      []sniff.Episode // 方向B：提取的集数（识别并套进去）

	// WebviewRender 宿主注入（sniff 兜底：渲染盗版站详情页，回传 outerHTML）。
	WebviewRender func(url string)
	// PageRender 纯页面渲染（点击搜索结果 → 渲染真实页，回传 outerHTML，不归一化/不去广告）。
	PageRender func(url string)
	// PageInject 方向C：可见 WebView 打开目标站 + 直接注入 JS（去广告/归一化/集数提取），不回传。
	PageInject func(url string)
	// Nav 主窗口导航（Go 侧 mainWin.SetURL，跨域 location.href 不生效，必须走 Go）。
	Nav func(url string)
	// SearchWebviewRender 搜索渲染（Hidden 子 WebView 窗口渲染引擎页，JS 抓结构化结果 → 导航回传）。
	// 桌面 wails3 壳注入；nil=HTTP 后备（Android 无窗口时）。
	SearchWebviewRender func(url string)

	// 搜索 WebView 兜底（搜索引擎 JS 渲染/验证码时，WebView 渲染结果页再解析）
	searchMu     sync.Mutex
	searchResult []search.Result
	searchQuery  string
	searchEngine string

	// 最近在看（历史记录，最多 5 条）
	recentMu   sync.Mutex
	searchHist []SearchItem

	// 账号固化（cookie 持久化，登录后 WebView 注入复用）
	Cookies *CookieStore

	// 收藏库（媒体库元信息持久化：纯流媒体源索引，不存文件）
	Library *Library
}

// RecentItem 一条最近播放记录（首页「最近在看」一键续看）
// SearchItem 搜索历史（搜索记录：点击重搜，不记播放——播放记录归媒体库）。
type SearchItem struct {
	Query string `json:"query"`
	Time  string `json:"time"`
}

// recordSearch 记一条搜索历史（同关键词去重置顶，截断 8 条）。
func (inst *Instance) recordSearch(q string) {
	if q == "" {
		return
	}
	inst.recentMu.Lock()
	defer inst.recentMu.Unlock()
	out := make([]SearchItem, 0, 8)
	out = append(out, SearchItem{Query: q, Time: time.Now().Format("01-02 15:04")})
	for _, it := range inst.searchHist {
		if len(out) >= 8 || it.Query == q {
			continue
		}
		out = append(out, it)
	}
	inst.searchHist = out
}

// Options 装配选项
type Options struct {
	Port      string // 监听端口（用于拼 URL）
	IP        string // 对外 IP（电视能访问到的地址；空=自动）
	AccessKey string // B站 access_key（可选）
	UUID      string
	Name      string
	DataDir   string // 数据目录（cookie 持久化等；空=默认）
}

// New 组装全部 handler 并启动后台服务（SSDP 广播 + NVA TCP）。
// 调用方负责把返回的 Handler 挂到 http.Server 上。
func New(opts Options) *Instance {
	CaptureLog() // 开独立日志文件（不劫持全局 log，不干扰 WebView）
	initConfig() // 保证 config.json 存在（默认只开 baidu，用户可在设置页改）
	port := opts.Port
	if port == "" {
		port = Port
	}
	ip := opts.IP
	if ip == "" {
		ip = ssdp.LocalIP()
	}

	baseURL := fmt.Sprintf("http://%s:%s", ip, port)

	// 媒体服务器（本地重封装喂电视）
	ms := media.NewServer(baseURL)

	// B站 playurl 客户端
	b := bili.New(opts.AccessKey)

	// 编排器（拿流→推电视；已不接收 B站 App 投屏）
	rl := relay.New(ms, b, baseURL)

	// 账号固化存储：数据目录（HOME/data 或当前目录）
	dataDir := opts.DataDir
	if dataDir == "" {
		dataDir = filepath.Join(os.Getenv("HOME"), "data")
	}
	inst := &Instance{
		BaseURL: baseURL,
		Relay:   rl,
		Sniff:   sniff.New(sniff.Default()),
	}
	// 搜索聚合器：注入持久化 cookie 提供者（最稳：真实登录态 cookie 纯 HTTP，WebView 仅兜底）
	inst.Search = search.New(search.Default()) // 纯 HTTP 四引擎（bing/360/baidu/sogou），不依赖库
	inst.Cookies = NewCookieStore(dataDir)
	inst.Search.CookieFn = inst.Cookies.HeaderString
	// cookie 通过 Options.CookieFn 传入（库无状态，不持 CookieFn 字段）
	inst.Library = NewLibrary(dataDir)

	mux := http.NewServeMux()
	// 根路径重定向 /ui（已砍 renderer 的 DLNA 接收端，根路径无设备描述）
	mux.HandleFunc("/", func(w http.ResponseWriter, rq *http.Request) {
		http.Redirect(w, rq, "/ui", http.StatusFound)
	})
	// mock 影视详情页（方向B 测试：模拟 sandat 结构：video meta + playlist 容器 + 集数链接）。
	// 不依赖真实站，验证 点结果→子窗口渲染→DetectVideoPage→Extract→显示集数 链路。
	mux.HandleFunc("/mock", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		var b strings.Builder
		b.WriteString(`<!DOCTYPE html><html><head><title>《真情在线观看》_mock</title>`)
		b.WriteString(`<meta name="video:class" content="香港"><meta property="og:type" content="video"></head><body>`)
		b.WriteString(`<div id="playlist1" class="tab-pane fade in active clearfix"><ul class="aa-content__playlist clearfix">`)
		for i := 1; i <= 20; i++ {
			fmt.Fprintf(&b, `<li><a href="/p/mock-1-%d.html" title="真情第%02d集">第%02d集</a></li>`, i, i, i)
		}
		b.WriteString(`</ul></div></body></html>`)
		io.WriteString(w, b.String())
	})
	mux.Handle("/m/", ms.Handler())
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/api/play", func(w http.ResponseWriter, rq *http.Request) {
		action := rq.URL.Query().Get("action")
		arg := rq.URL.Query().Get("arg")
		if action == "" {
			writeJSON(w, map[string]any{"ok": false, "msg": "缺少 action"})
			return
		}
		if err := rl.Control(action, arg); err != nil {
			writeJSON(w, map[string]any{"ok": false, "msg": err.Error()})
			return
		}
		rl.Logf("控制: %s %s", action, arg)
		writeJSON(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("/api/play/status", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"ok": true, "play": rl.PlayStatus()})
	})
	mux.HandleFunc("/api/debug", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"sessions": ms.Debug()})
	})
	// 盗版站/通用网页嗅探：输入详情页 URL → 集数列表
	mux.HandleFunc("/api/sniff", func(w http.ResponseWriter, rq *http.Request) {
		u := rq.URL.Query().Get("url")
		if u == "" {
			writeJSON(w, map[string]any{"ok": false, "msg": "缺少 url"})
			return
		}
		Logf("[sniff] 提取集数 url=%s", u)
		// 防盗链：从请求头透传 Referer/UA（盗版站不能猜）
		cfg := sniff.Default()
		if ref := rq.Header.Get("Referer"); ref != "" {
			cfg.Referer = ref
		}
		e := sniff.New(cfg)
		eps, err := e.ExtractURL(u)
		if err != nil {
			LogError("[sniff] 失败 url=%s err=%v need_browser=%v", u, err, errors.Is(err, sniff.ErrNeedsBrowser))
			writeJSON(w, map[string]any{"ok": false, "msg": err.Error(), "need_browser": errors.Is(err, sniff.ErrNeedsBrowser)})
			return
		}
		Logf("[sniff] 成功 url=%s 集数=%d", u, len(eps))
		// 抓封面图（学 OnScreen artwork，但抓盗版站详情页图）
		poster := ""
		if htmlText, ferr := e.Fetch(u); ferr == nil {
			poster = e.ExtractPoster(htmlText, u)
		}
		writeJSON(w, map[string]any{"ok": true, "url": u, "episodes": eps, "poster": poster})
	})

	// 盗版站闭环：播放页 URL → 拿流(m3u8/mp4) → 投屏。妈妈操作 2 的最后一段。
	mux.HandleFunc("/api/cast", func(w http.ResponseWriter, rq *http.Request) {
		// 注入页（站点域）跨域拿流：允许任意源（GET simple request，无需 preflight）
		w.Header().Set("Access-Control-Allow-Origin", "*")
		u := rq.URL.Query().Get("url")
		title := rq.URL.Query().Get("title")
		if u == "" {
			writeJSON(w, map[string]any{"ok": false, "msg": "缺少 url"})
			return
		}
		// 三组件解耦：拿流+投屏收敛到媒体库组件（PirateSource），不再散调 sniff+relay。
		src := &relay.PirateSource{
			Rl:      inst.Relay,
			Extract: sniff.New(sniff.Default()),
			PlayURL: u,
			Title:   title,
		}
		Logf("[cast] 拿流+投屏 url=%s title=%s", u, title)
		st, err := src.CastWith(rq.Context())
		if err != nil {
			LogError("[cast] 失败 url=%s err=%v", u, err)
			writeJSON(w, map[string]any{"ok": false, "msg": err.Error()})
			return
		}
		Logf("[cast] 成功 url=%s mime=%s ref=%s", st.URL, st.Mime, st.Headers["Referer"])
		ref := st.Headers["Referer"]
		writeJSON(w, map[string]any{"ok": true, "url": st.URL, "mime": st.Mime, "ref": ref})
	})

	// 搜索历史（搜索记录：点击重搜；播放记录不在这——归媒体库继续观看）
	mux.HandleFunc("/api/search/history", func(w http.ResponseWriter, _ *http.Request) {
		inst.recentMu.Lock()
		items := append([]SearchItem(nil), inst.searchHist...)
		inst.recentMu.Unlock()
		writeJSON(w, map[string]any{"ok": true, "items": items})
	})

	// WebView 兜底回传：JS 拿 outerHTML（挑战完成后）→ POST 这里 → 解析存结果
	mux.HandleFunc("/api/sniff/render-launch", func(w http.ResponseWriter, rq *http.Request) {
		u := rq.URL.Query().Get("url")
		if u == "" {
			writeJSON(w, map[string]any{"ok": false, "msg": "缺少 url"})
			return
		}
		LogInfo("[sniff] WebView 兑底渲染 url=%s", u)
		if inst.WebviewRender != nil {
			inst.WebviewRender(u) // 桌面壳：子窗口渲染详情页 → outerHTML POST /api/sniff/render
			writeJSON(w, map[string]any{"ok": true, "msg": "rendering"})
			return
		}
		writeJSON(w, map[string]any{"ok": false, "msg": "WebView 渲染不可用"})
	})

	mux.HandleFunc("/api/sniff/render", func(w http.ResponseWriter, rq *http.Request) {
		if rq.Method != http.MethodPost {
			writeJSON(w, map[string]any{"ok": false, "msg": "需要 POST"})
			return
		}
		rq.ParseForm()
		htmlText := rq.PostFormValue("html")
		u := rq.PostFormValue("url")
		if htmlText == "" {
			writeJSON(w, map[string]any{"ok": false, "msg": "缺少 html"})
			return
		}
		eps := inst.Sniff.Extract(htmlText, u)
		inst.sniffMu.Lock()
		inst.sniffResult = eps
		inst.sniffURL = u
		inst.sniffMu.Unlock()
		if len(eps) == 0 {
			writeJSON(w, map[string]any{"ok": true, "episodes": 0, "msg": "no episodes found"})
			return
		}
		writeJSON(w, map[string]any{"ok": true, "episodes": len(eps)})
	})

	// 前端轮询拿 WebView 兜底结果（读后清空）
	mux.HandleFunc("/api/sniff/result", func(w http.ResponseWriter, _ *http.Request) {
		inst.sniffMu.Lock()
		eps := inst.sniffResult
		u := inst.sniffURL
		inst.sniffResult = nil
		inst.sniffURL = ""
		inst.sniffMu.Unlock()
		writeJSON(w, map[string]any{"ok": true, "url": u, "episodes": eps})
	})

	// ═══════════ 主窗口导航（方向E：真跳搜索引擎/影视站，Go 侧 SetURL）═══
	mux.HandleFunc("/api/nav", func(w http.ResponseWriter, rq *http.Request) {
		u := rq.URL.Query().Get("url")
		if u == "" {
			writeJSON(w, map[string]any{"ok": false, "msg": "缺少 url"})
			return
		}
		if inst.Nav != nil {
			inst.Nav(u) // Go 侧 mainWin.SetURL（跨域 location.href 不生效，必须走 Go）
			writeJSON(w, map[string]any{"ok": true})
			return
		}
		writeJSON(w, map[string]any{"ok": false, "msg": "无壳导航"})
	})

	// ═══════════ 纯页面渲染通道（先不归一化/不去广告，直接渲染搜索结果真实页面）═══
	// render-launch：触发子 WebView 渲染任意 URL（桌面壳注入 WebviewRender）
	mux.HandleFunc("/api/page/render-launch", func(w http.ResponseWriter, rq *http.Request) {
		u := rq.URL.Query().Get("url")
		if u == "" {
			writeJSON(w, map[string]any{"ok": false, "msg": "缺少 url"})
			return
		}
		if inst.PageRender != nil {
			go inst.PageRender(u) // 子窗口渲染 → outerHTML POST /api/page/render（方向B）
			writeJSON(w, map[string]any{"ok": true, "msg": "rendering"})
			return
		}
		if inst.WebviewRender != nil {
			go inst.WebviewRender(u)
			writeJSON(w, map[string]any{"ok": true, "msg": "rendering (sniff)"})
			return
		}
		writeJSON(w, map[string]any{"ok": false, "msg": "WebView 渲染不可用"})
	})

	// inject-launch：方向C 可见 WebView 打开目标站 + 直接注入 JS（去广告/归一化/集数提取），不回传。
	mux.HandleFunc("/api/page/inject-launch", func(w http.ResponseWriter, rq *http.Request) {
		u := rq.URL.Query().Get("url")
		if u == "" {
			writeJSON(w, map[string]any{"ok": false, "msg": "缺少 url"})
			return
		}
		if inst.PageInject != nil {
			go inst.PageInject(u)
			writeJSON(w, map[string]any{"ok": true, "msg": "injecting"})
			return
		}
		writeJSON(w, map[string]any{"ok": false, "msg": "无壳注入"})
	})

	// render：子窗口 outerHTML 回传，原样存（不 Extract，保留页面结构）。
	// 兼容 POST body（Android Java 回传）和 GET 导航回传（桌面壳 location.href ?url=&html=）。
	mux.HandleFunc("/api/page/render", func(w http.ResponseWriter, rq *http.Request) {
		htmlText := rq.URL.Query().Get("html")
		u := rq.URL.Query().Get("url")
		if htmlText == "" && rq.Method == http.MethodPost {
			rq.ParseForm()
			htmlText = rq.PostFormValue("html")
			u = rq.PostFormValue("url")
		}
		if htmlText == "" {
			writeJSON(w, map[string]any{"ok": false, "msg": "缺少 html"})
			return
		}
		inst.pageMu.Lock()
		inst.pageHTML = htmlText
		inst.pageURL = u
		// 方向B：判断影视页 + 提取集数（识别并套进去）。DetectVideoPage 三路启发式（不写死站点）。
		ex := sniff.New(sniff.Default())
		inst.pageIsVideo = ex.DetectVideoPage(u, htmlText)
		inst.pageEps = nil
		if inst.pageIsVideo {
			clean := sniff.CleanAds(htmlText) // 去广告（归一化）
			eps := ex.Extract(clean, u)       // 提取集数
			sniff.SortEpisodes(eps)           // 排序归一化
			inst.pageEps = eps
		}
		inst.pageMu.Unlock()
		LogInfo("[page/render] 收到 outerHTML url=%s 大小=%d", u, len(htmlText))
		writeJSON(w, map[string]any{"ok": true, "size": len(htmlText), "url": u})
	})

	// result：前端轮询拿渲染后的页面 HTML（读后清空）
	mux.HandleFunc("/api/page/result", func(w http.ResponseWriter, _ *http.Request) {
		inst.pageMu.Lock()
		htmlText := inst.pageHTML
		u := inst.pageURL
		isVideo := inst.pageIsVideo
		eps := inst.pageEps
		inst.pageHTML = ""
		inst.pageURL = ""
		inst.pageIsVideo = false
		inst.pageEps = nil
		inst.pageMu.Unlock()
		if htmlText == "" {
			writeJSON(w, map[string]any{"ok": false, "rendering": true})
			return
		}
		// 方向B：返回 is_video + 集数（识别则前端套进去，非影视则导航真实页）
		writeJSON(w, map[string]any{"ok": true, "url": u, "is_video": isVideo, "episodes": eps})
	})


	// 搜索盗版站（bing/360/百度 聚合，接 /api/sniff 提取集数）
	mux.HandleFunc("/api/search/render", func(w http.ResponseWriter, rq *http.Request) {
		// Hidden 子窗口 JS 抓结构化结果 → 导航回传（GET ?engine=x&data=<urlencoded JSON>，绕开 mixed content）
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if rq.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		engine := rq.URL.Query().Get("engine")
		data := rq.URL.Query().Get("data") // 结构化结果 JSON（[{title,url,desc}]）
		// 兼容：POST body（Android Java 回传 JSON）
		if rq.Method == http.MethodPost {
			b, _ := io.ReadAll(rq.Body)
			data = string(b)
		}
		if data == "" {
			writeJSON(w, map[string]any{"ok": false, "msg": "缺少 data"})
			return
		}
		// 解析结构化结果 JSON
		var items []struct {
			Title string `json:"title"`
			URL   string `json:"url"`
			Desc  string `json:"desc"`
		}
		if err := json.Unmarshal([]byte(data), &items); err != nil {
			LogWarn("[search/render] data 解析失败 engine=%s err=%v", engine, err)
			writeJSON(w, map[string]any{"ok": false, "msg": "data 解析失败"})
			return
		}
		var rs []search.Result
		seen := map[string]bool{}
		for _, it := range items {
			if it.URL == "" || seen[it.URL] {
				continue
			}
			seen[it.URL] = true
			rs = append(rs, search.Result{Engine: engine, Title: it.Title, URL: it.URL})
		}
		inst.searchMu.Lock()
		if len(rs) > 0 {
			inst.searchResult = rs // 单引擎回传结果
			inst.searchQuery = rq.URL.Query().Get("query")
			inst.searchEngine = engine
		}
		inst.searchMu.Unlock()
		LogInfo("[search/render] webview 回传 engine=%s 结果=%d", engine, len(rs))
		writeJSON(w, map[string]any{"ok": true, "count": len(rs), "engine": engine})
	})

	// /api/search/result 前端轮询拿 webview 渲染结果（异步）
	mux.HandleFunc("/api/search/result", func(w http.ResponseWriter, rq *http.Request) {
		inst.searchMu.Lock()
		rs := inst.searchResult
		inst.searchResult = nil
		q := inst.searchQuery
		eng := inst.searchEngine
		inst.searchMu.Unlock()
		if len(rs) == 0 {
			writeJSON(w, map[string]any{"ok": false, "rendering": true, "query": q, "engine": eng})
			return
		}
		inst.recordSearch(q)
		writeJSON(w, map[string]any{"ok": true, "query": q, "results": rs, "via": "webview", "engine": eng})
	})

	mux.HandleFunc("/api/search", func(w http.ResponseWriter, rq *http.Request) {
		q := strings.TrimSpace(rq.URL.Query().Get("q"))
		if q == "" {
			writeJSON(w, map[string]any{"ok": false, "msg": "缺少关键词"})
			return
		}
		// 搜索引擎：WebView 渲染（单引擎，用户选）——Hidden 子窗口渲染引擎页，JS 抓结构化 → 导航回传。
		// 用户硬决策：必须走 webview；单引擎可选（engine 参数），不聚合（砍并发）。
		enabled := search.EnabledEngines()      // 配置开关
		engName := rq.URL.Query().Get("engine") // 用户选的引擎（单引擎）
		if engName == "" {
			engName = "baidu" // 默认
		}
		start := time.Now()
		LogInfo("[search] q=%s engine=%s 开始（webview 单引擎渲染）", q, engName)
		if inst.SearchWebviewRender != nil {
			// 找该引擎的 URL（用 q 构造）
			engs := search.BrowserEngines(q, enabled)
			var target *search.BrowserEngine
			for i := range engs {
				if engs[i].Name == engName {
					target = &engs[i]
					break
				}
			}
			if target == nil {
				// 引擎名无效/未启用 → 用第一个启用的
				if len(engs) > 0 {
					target = &engs[0]
					engName = engs[0].Name
				}
			}
			if target == nil {
				writeJSON(w, map[string]any{"ok": false, "msg": "无可用引擎（配置开关全关？）", "query": q})
				return
			}
			inst.searchMu.Lock()
			inst.searchResult = nil
			inst.searchQuery = q
			inst.searchEngine = engName
			inst.searchMu.Unlock()
			LogInfo("[search] 触发 webview 渲染 engine=%s url=%s", target.Name, target.URL)
			go inst.SearchWebviewRender(target.URL) // 单引擎，1 个窗口
			writeJSON(w, map[string]any{"ok": true, "query": q, "via": "webview", "rendering": true, "msg": "渲染中…", "engine": engName})
			return
		}
		// 无 webview 渲染器（Android/非桌面壳）：HTTP 四引擎后备。
		rs, err := inst.Search.Search(rq.Context(), q)
		if err != nil {
			LogWarn("[search] q=%s 失败 err=%v 耗时=%s", q, err, time.Since(start).Round(time.Millisecond))
			needBrowser := errors.Is(err, search.ErrCaptcha)
			writeJSON(w, map[string]any{"ok": false, "msg": err.Error(), "need_browser": needBrowser, "query": q})
			return
		}
		// 按启用引擎过滤（config 控制所有路径）
		if enabled != nil {
			en := map[string]bool{}
			for _, e := range enabled {
				en[e] = true
			}
			var f []search.Result
			for _, r := range rs {
				if en[r.Engine] {
					f = append(f, r)
				}
			}
			rs = f
		}
		inst.recordSearch(q)
		if len(rs) == 0 {
			LogWarn("[search] q=%s 结果=0（引擎有结果但全被过滤/触发验证码）", q)
		} else {
			LogInfo("[search] q=%s via=http 结果=%d 耗时=%s", q, len(rs), time.Since(start).Round(time.Millisecond))
		}
		writeJSON(w, map[string]any{"ok": true, "query": q, "results": rs})
	})

	// ── 账号固化（cookie 持久化）：登录后自动收集，WebView 搜索/嗅探时注入 ──

	// 已固化引擎状态
	mux.HandleFunc("/api/cookie/list", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"ok": true, "engines": inst.Cookies.List()})
	})

	// 保存某引擎 cookie（登录完成回传）
	mux.HandleFunc("/api/cookie/save", func(w http.ResponseWriter, rq *http.Request) {
		engine := rq.URL.Query().Get("engine")
		if engine == "" {
			writeJSON(w, map[string]any{"ok": false, "msg": "缺少 engine"})
			return
		}
		var cookies []CookieEntry
		if err := json.NewDecoder(rq.Body).Decode(&cookies); err != nil {
			writeJSON(w, map[string]any{"ok": false, "msg": "body 非 JSON: " + err.Error()})
			return
		}
		if err := inst.Cookies.Save(engine, cookies); err != nil {
			writeJSON(w, map[string]any{"ok": false, "msg": err.Error()})
			return
		}
		writeJSON(w, map[string]any{"ok": true, "engine": engine, "count": len(cookies)})
	})

	// 读某引擎 cookie（WebView 注入用，Android 壳搜索前取）
	mux.HandleFunc("/api/cookie/get", func(w http.ResponseWriter, rq *http.Request) {
		engine := rq.URL.Query().Get("engine")
		writeJSON(w, map[string]any{"ok": true, "engine": engine, "cookies": inst.Cookies.Load(engine)})
	})

	// 清除某引擎 cookie
	mux.HandleFunc("/api/cookie/clear", func(w http.ResponseWriter, rq *http.Request) {
		engine := rq.URL.Query().Get("engine")
		if engine == "" {
			writeJSON(w, map[string]any{"ok": false, "msg": "缺少 engine"})
			return
		}
		_ = inst.Cookies.Clear(engine)
		writeJSON(w, map[string]any{"ok": true})
	})

	// ── 收藏库（媒体库元信息持久化：纯流媒体源索引）──

	// 封面图代理（防盗链）
	mux.HandleFunc("/api/poster", inst.posterProxy)

	// 收藏列表
	mux.HandleFunc("/api/library", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"ok": true, "items": inst.Library.List()})
	})

	// 收藏/取消收藏
	mux.HandleFunc("/api/library/fav", func(w http.ResponseWriter, rq *http.Request) {
		title := rq.URL.Query().Get("title")
		u := rq.URL.Query().Get("url")
		if title == "" || u == "" {
			writeJSON(w, map[string]any{"ok": false, "msg": "缺少 title/url"})
			return
		}
		if rq.URL.Query().Get("unfav") == "1" {
			inst.Library.Unfav(u)
			writeJSON(w, map[string]any{"ok": true, "fav": false})
			return
		}
		poster := rq.URL.Query().Get("poster")
		source := rq.URL.Query().Get("source")
		inst.Library.Fav(title, u, poster, source)
		writeJSON(w, map[string]any{"ok": true, "fav": true})
	})
	// 播放进度/元数据回写媒体库（SQLite 持久化，跨设备不丢）
	mux.HandleFunc("/api/library/progress", func(w http.ResponseWriter, rq *http.Request) {
		u := rq.URL.Query().Get("url")
		if u == "" {
			writeJSON(w, map[string]any{"ok": false, "msg": "缺少 url"})
			return
		}
		pct := 0.0
		if v := rq.URL.Query().Get("progress"); v != "" {
			pct, _ = strconv.ParseFloat(v, 64)
		}
		inst.Library.UpdateProgress(u, pct,
			rq.URL.Query().Get("position"), rq.URL.Query().Get("duration"))
		writeJSON(w, map[string]any{"ok": true})
	})
	// 搜索配置（引擎开关）
	mux.HandleFunc("/api/config", func(w http.ResponseWriter, rq *http.Request) {
		switch rq.Method {
		case http.MethodGet:
			writeJSON(w, map[string]any{"ok": true, "config": getConfig()})
		case http.MethodPost:
			var cfg map[string]any
			if err := json.NewDecoder(rq.Body).Decode(&cfg); err != nil {
				writeJSON(w, map[string]any{"ok": false, "msg": "无效 JSON"})
				return
			}
			if err := saveConfig(cfg); err != nil {
				writeJSON(w, map[string]any{"ok": false, "msg": err.Error()})
				return
			}
			Logf("[config] 保存配置 engines=%v", cfg["search_engines"])
			writeJSON(w, map[string]any{"ok": true})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// 日志（辅助调试）：?n=条数（默认全部）&q=关键词过滤
	mux.HandleFunc("/api/logs", func(w http.ResponseWriter, rq *http.Request) {
		n := 0
		fmt.Sscanf(rq.URL.Query().Get("n"), "%d", &n)
		level := strings.TrimSpace(rq.URL.Query().Get("level"))
		lines := ReadLog(n, level)
		if q := strings.TrimSpace(rq.URL.Query().Get("q")); q != "" {
			var f []string
			for _, l := range lines {
				if strings.Contains(strings.ToLower(l), strings.ToLower(q)) {
					f = append(f, l)
				}
			}
			lines = f
		}
		writeJSON(w, map[string]any{"ok": true, "logs": lines})
	})
	mux.HandleFunc("/api/logs/clear", func(w http.ResponseWriter, _ *http.Request) {
		ClearLog() // 清空日志文件（旧实现只清前端 DOM，pollLogs 拉回 → 假清空）
		writeJSON(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"base_url": baseURL,
			"status":   rl.Status(),
			"tvs":      tvList(),
			"logs":     rl.GetLogs(),
		})
	})
	mux.HandleFunc("/api/tv/select", func(w http.ResponseWriter, rq *http.Request) {
		name := rq.URL.Query().Get("name")
		for _, d := range tvList() {
			if d.Name == name {
				rl.SelectTV(d)
				writeJSON(w, map[string]any{"ok": true, "tv": d.Name})
				return
			}
		}
		writeJSON(w, map[string]any{"ok": false, "msg": "设备已离线，重新扫描"})
	})
	// 模式对普通用户不可见。后端只认 auto（自动识别），
	// 忽略其它值，避免误切到 remux（手机上没有 ffmpeg）等内部选项。
	mux.HandleFunc("/api/mode", func(w http.ResponseWriter, rq *http.Request) {
		rl.SetMode(media.ModeAuto)
		writeJSON(w, map[string]any{"ok": true, "mode": "auto"})
	})
	mux.HandleFunc("/api/tv/scan", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"ok": true, "tvs": tvList()})
	})

	tpl := template.Must(template.ParseFS(tplFS, "web/templates/*.html"))
	mux.HandleFunc("/ui", func(w http.ResponseWriter, _ *http.Request) {
		// 防缓存：/ui 是 SPA 入口，改了模板浏览器必须看到最新（之前用户打开旧缓存 UI）
		w.Header().Set("Cache-Control", "no-store, max-age=0")
		w.Header().Set("Pragma", "no-cache")
		data := map[string]any{
			"BaseURL": baseURL,
			"Status":  rl.Status(),
			"TVs":     tvList(),
			"Logs":    rl.GetLogs(),
		}
		_ = tpl.ExecuteTemplate(w, "index.html", data)
	})

	inst.Handler = logRequests(mux)

	// 内置播放器依赖：hls.js（内嵌，播 m3u8；浏览器原生 <video> 不播 m3u8）
	mux.HandleFunc("/static/hls.min.js", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = w.Write(hlsJS)
	})

	// MCP 端点（Streamable HTTP）：给 AI 暴露 search/sniff/cast 工具（老人一句话投屏）
	mcpSrv := mcp.New(inst.Search, inst.Sniff, rl)
	mux.Handle("/mcp", mcpSrv.Handler())

	return inst
}

// logRequests 中间件：只做 panic 恢复，不打 http 访问日志（避免刷屏/到处塞）。
// 关键链路（搜索/嗅探/投屏/配置）各自有独立 [search]/[sniff]/[cast]/[config] 日志。
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, rq *http.Request) {
		defer func() {
			if p := recover(); p != nil {
				LogError("[panic] %s %s: %v", rq.Method, rq.URL.Path, p)
				w.WriteHeader(http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, rq)
	})
}

// newUUID 生成合法的 RFC4122 v4 UUID（部分 DLNA 客户端会校验 USN 里的 UUID 格式）
func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// 极少概率失败，退化为时间戳派生
		n := time.Now().UnixNano()
		for i := range b {
			b[i] = byte(n >> (uint(i%8) * 8))
		}
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant RFC4122
	return fmt.Sprintf("uuid:%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Stop 停后台服务（已砍 DLNA 中继：无 ad/nva 可停）
func (inst *Instance) Stop() {}

// tvList 扫描电视（每 5 秒缓存）
var lastScan time.Time
var cached []*ssdp.Device
var tvMu sync.Mutex

// tvList 电视列表：读缓存（不阻塞 /ui）。首次无缓存则后台异步扫描，返回 nil（立即）。
// 之前首次同步 ssdp.Search（等 5s 扫电视）导致 /ui 首次加载慢 5 秒+。
func tvList() []*ssdp.Device {
	tvMu.Lock()
	fresh := time.Since(lastScan) < 5*time.Second && cached != nil
	tvMu.Unlock()
	if fresh {
		return cached
	}
	// 异步扫描（不阻塞调用方，扫描结果供下次缓存）
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		res := ssdp.Search(ctx, 5*time.Second)
		tvMu.Lock()
		cached = res
		lastScan = time.Now()
		tvMu.Unlock()
	}()
	return cached // 首次可能 nil，立即返回
}

// waitSearchResult 轮询等 webview 渲染回传的搜索（JS fetch POST /api/search/render → ParseRendered 存 searchResult）。
func (s *Instance) waitSearchResult(timeout time.Duration) []search.Result {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		s.searchMu.Lock()
		rs := s.searchResult
		if rs != nil {
			s.searchResult = nil
			s.searchQuery = ""
			s.searchMu.Unlock()
			return rs
		}
		s.searchMu.Unlock()
		time.Sleep(400 * time.Millisecond)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// /api/poster?url=<封面图>&ref=<站点域名> 代理封面图（绕过防盗链：带站点 Referer）。
func (inst *Instance) posterProxy(w http.ResponseWriter, rq *http.Request) {
	u := rq.URL.Query().Get("url")
	if u == "" {
		http.Error(w, "missing url", http.StatusBadRequest)
		return
	}
	ref := rq.URL.Query().Get("ref")
	if ref == "" {
		if uu, err := url.Parse(u); err == nil {
			ref = uu.Scheme + "://" + uu.Host + "/"
		}
	}
	req, err := http.NewRequestWithContext(rq.Context(), http.MethodGet, u, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req.Header.Set("User-Agent", sniff.Default().UA)
	req.Header.Set("Referer", ref)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// configPath 搜索配置路径（与 search/browser.go 同路径）。
const configPath = "/mnt/shared/mycast/config.json"

// defaultConfig 默认配置：三引擎全开（百度验证码时 bing/so 兜底，避免 0 结果）。
var defaultConfig = map[string]any{
	"search_engines": []string{"baidu", "bing", "so"},
}

// initConfig 确保 config.json 存在（不存在则写默认）。
func initConfig() {
	if _, err := os.Stat(configPath); err == nil {
		return // 已存在不覆盖
	}
	b, _ := json.MarshalIndent(defaultConfig, "", "  ")
	if err := os.WriteFile(configPath, b, 0644); err != nil {
		LogWarn("[config] 写入默认配置失败: %v", err)
	} else {
		Logf("[config] 创建默认配置 %s（只开 baidu）", configPath)
	}
}

// getConfig 读取完整配置（JSON）。
func getConfig() map[string]any {
	b, err := os.ReadFile(configPath)
	if err != nil {
		return defaultConfig
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return defaultConfig
	}
	return m
}

// saveConfig 保存完整配置。
func saveConfig(m map[string]any) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, b, 0644)
}

// enabledEngines 读取配置的 search_engines 开关（nil=全开；替代库的 EnabledEngines，
// 库无状态不读配置文件，开关由调用方（server）管）。
func enabledEngines() []string {
	cfg := getConfig()
	arr, _ := cfg["search_engines"].([]any)
	var out []string
	for _, v := range arr {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
