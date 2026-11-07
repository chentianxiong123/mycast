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
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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

// Port 默认端口
const Port = "8080"

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

	// 搜索 WebView 兜底（搜索引擎 JS 渲染/验证码时，WebView 渲染结果页再解析）
	searchMu     sync.Mutex
	searchResult []search.Result
	searchQuery  string
	// WebviewRender Android 壳注入（window.hc 搜索渲染回调，渲染引擎页 JS 回传 outerHTML）。
	// 桌面 wails3 单窗口不再创建渲染窗口，此字段 = nil；/api/search 纯 cookie 固化 fetch。
	WebviewRender func(url string)

	// 最近在看（历史记录，最多 5 条）
	recentMu sync.Mutex
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
	CaptureLog() // 捕获全局 log（搜索/嗅探/拿流/投屏 → /api/logs 辅助调试）
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
	inst.Search = search.New(search.Default())
	inst.Cookies = NewCookieStore(dataDir)
	inst.Search.CookieFn = inst.Cookies.HeaderString
	inst.Library = NewLibrary(dataDir)

	mux := http.NewServeMux()
	// 根路径重定向 /ui（已砍 renderer 的 DLNA 接收端，根路径无设备描述）
	mux.HandleFunc("/", func(w http.ResponseWriter, rq *http.Request) {
		http.Redirect(w, rq, "/ui", http.StatusFound)
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
		log.Printf("[sniff] 提取集数 url=%s", u)
		// 防盗链：从请求头透传 Referer/UA（盗版站不能猜）
		cfg := sniff.Default()
		if ref := rq.Header.Get("Referer"); ref != "" {
			cfg.Referer = ref
		}
		e := sniff.New(cfg)
		eps, err := e.ExtractURL(u)
		if err != nil {
			log.Printf("[sniff] 失败 url=%s err=%v need_browser=%v", u, err, errors.Is(err, sniff.ErrNeedsBrowser))
			writeJSON(w, map[string]any{"ok": false, "msg": err.Error(), "need_browser": errors.Is(err, sniff.ErrNeedsBrowser)})
			return
		}
		log.Printf("[sniff] 成功 url=%s 集数=%d", u, len(eps))
		// 抓封面图（学 OnScreen artwork，但抓盗版站详情页图）
		poster := ""
		if htmlText, ferr := e.Fetch(u); ferr == nil {
			poster = e.ExtractPoster(htmlText, u)
		}
		writeJSON(w, map[string]any{"ok": true, "url": u, "episodes": eps, "poster": poster})
	})

	// 盗版站闭环：播放页 URL → 拿流(m3u8/mp4) → 投屏。妈妈操作 2 的最后一段。
	mux.HandleFunc("/api/cast", func(w http.ResponseWriter, rq *http.Request) {
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
		log.Printf("[cast] 拿流+投屏 url=%s title=%s", u, title)
		st, err := src.CastWith(rq.Context())
		if err != nil {
			log.Printf("[cast] 失败 url=%s err=%v", u, err)
			writeJSON(w, map[string]any{"ok": false, "msg": err.Error()})
			return
		}
		log.Printf("[cast] 成功 url=%s mime=%s ref=%s", st.URL, st.Mime, st.Headers["Referer"])
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

	// 搜索盗版站（bing/360/百度 聚合，接 /api/sniff 提取集数）
	mux.HandleFunc("/api/search", func(w http.ResponseWriter, rq *http.Request) {
		q := strings.TrimSpace(rq.URL.Query().Get("q"))
		if q == "" {
			writeJSON(w, map[string]any{"ok": false, "msg": "缺少关键词"})
			return
		}
		// 搜索引擎：webview 渲染优先（渲染窗口加载引擎页，JS fetch 回传 outerHTML），
		// 渲染不可用时走 fetch 后备。
		enabled := search.EnabledEngines() // 配置开关
		if inst.WebviewRender != nil {
			inst.searchMu.Lock()
			inst.searchResult = nil
			inst.searchMu.Unlock()
			for _, eng := range search.BrowserEngines(q, enabled) {
				inst.WebviewRender(eng.URL)
				if rs := inst.waitSearchResult(20 * time.Second); len(rs) > 0 {
					inst.recordSearch(q)
					writeJSON(w, map[string]any{"ok": true, "query": q, "results": rs, "via": "webview", "engine": eng.Name})
					return
				}
			}
		}
		// fetch 后备（webview 渲染不可用）：按配置过滤引擎。
		rs, err := inst.Search.Search(rq.Context(), q)
		if err != nil {
			writeJSON(w, map[string]any{"ok": false, "msg": err.Error(), "need_browser": true, "query": q})
			return
		}
		// 按启用引擎过滤（config 控制所有路径）
		if enabled != nil {
			en := map[string]bool{}
			for _, e := range enabled { en[e] = true }
			var f []search.Result
			for _, r := range rs {
				if en[r.Engine] { f = append(f, r) }
			}
			rs = f
		}
		inst.recordSearch(q)
		writeJSON(w, map[string]any{"ok": true, "query": q, "results": rs})
	})

	// 搜索 WebView 兜底：JS 拿搜索引擎结果页 outerHTML → POST 回来解析（学 sniff/render）
	mux.HandleFunc("/api/search/render", func(w http.ResponseWriter, rq *http.Request) {
		// CORS：搜索引擎页（so.com/baidu/bing 域）的 JS fetch 跨域回传 outerHTML 到本机后端。
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if rq.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if rq.Method != http.MethodPost {
			writeJSON(w, map[string]any{"ok": false, "msg": "需要 POST"})
			return
		}
		// HTML 来源二选一：body 文本（JS fetch 直传 outerHTML）或 form（Android Java POST）。
		htmlText := ""
		engine := rq.URL.Query().Get("engine")
		u := rq.URL.Query().Get("url")
		if strings.Contains(rq.Header.Get("Content-Type"), "text/plain") {
			b, _ := io.ReadAll(rq.Body)
			htmlText = string(b)
		} else {
			rq.ParseForm()
			htmlText = rq.PostFormValue("html")
			if u == "" {
				u = rq.PostFormValue("url")
			}
		}
		if htmlText == "" {
			writeJSON(w, map[string]any{"ok": false, "msg": "缺少 html"})
			return
		}
		// 用 search 的解析逻辑解析渲染后的 HTML（复用三引擎各自的解析器）。
		if engine == "" && u != "" {
			if strings.Contains(u, "so.com") {
				engine = "so"
			} else if strings.Contains(u, "baidu") {
				engine = "baidu"
			} else if strings.Contains(u, "bing") {
				engine = "bing"
			}
		}
		q := rq.URL.Query().Get("query")
		if errq := rq.URL.Query().Get("err"); errq != "" {
			log.Printf("[search/render] JS 错误! engine=%s err=%s", engine, errq)
		}
		log.Printf("[search/render] POST html=%dB engine=%s url=%s", len(htmlText), engine, u)
		rs := inst.Search.ParseRendered(htmlText, u, engine)
		inst.searchMu.Lock()
		inst.searchResult = rs
		inst.searchQuery = q
		inst.searchMu.Unlock()
		writeJSON(w, map[string]any{"ok": true, "count": len(rs), "engine": engine})
	})

	// 搜索 WebView 结果（前端轮询拿）
	mux.HandleFunc("/api/search/result", func(w http.ResponseWriter, _ *http.Request) {
		inst.searchMu.Lock()
		rs := inst.searchResult
		q := inst.searchQuery
		inst.searchResult = nil
		inst.searchQuery = ""
		inst.searchMu.Unlock()
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
			log.Printf("[config] 保存配置 engines=%v", cfg["search_engines"])
			writeJSON(w, map[string]any{"ok": true})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// 日志（辅助调试）：?n=条数（默认全部）&q=关键词过滤
	mux.HandleFunc("/api/logs", func(w http.ResponseWriter, rq *http.Request) {
		n := 0
		fmt.Sscanf(rq.URL.Query().Get("n"), "%d", &n)
		lines := logBuf.Logs(n)
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

// logRequests 给每个 HTTP 请求打一行访问日志（诊断 DLNA 客户端行为用）
// 跳过 UI 自己的高频轮询，避免刷屏
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, rq *http.Request) {
		p := rq.URL.Path
		quiet := p == "/ui" || p == "/favicon.ico" || strings.HasPrefix(p, "/api/")
		if quiet {
			next.ServeHTTP(w, rq)
			return
		}
		rec := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(rec, rq)
		log.Printf("[http] %s %s from %s ua=%q -> %d",
			rq.Method, rq.URL.RequestURI(), rq.RemoteAddr, rq.UserAgent(), rec.code)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.code = code
	s.ResponseWriter.WriteHeader(code)
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

// defaultConfig 默认配置：只开百度（用户要求先只开 baidu）。
var defaultConfig = map[string]any{
	"search_engines": []string{"baidu"},
}

// initConfig 确保 config.json 存在（不存在则写默认）。
func initConfig() {
	if _, err := os.Stat(configPath); err == nil {
		return // 已存在不覆盖
	}
	b, _ := json.MarshalIndent(defaultConfig, "", "  ")
	if err := os.WriteFile(configPath, b, 0644); err != nil {
		log.Printf("[config] 写入默认配置失败: %v", err)
	} else {
		log.Printf("[config] 创建默认配置 %s（只开 baidu）", configPath)
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
