// Package server 组装 mycast 全部后端 handler（HTTP 服务）。
// 被 cmd/mycast（独立进程）和 wails3 壳（安卓/桌面内嵌）复用。
package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"mycast/internal/baidulogin"
	"mycast/internal/bili"
	"mycast/internal/mcp"
	"mycast/internal/media"
	"mycast/internal/meta"
	"mycast/internal/relay"
	"mycast/internal/search"
	"mycast/internal/sniff"
	"mycast/internal/ssdp"
)

//go:embed web/templates/*.html
var tplFS embed.FS

//go:embed web/static/hls.min.js
var hlsJS []byte

//go:embed web/static/artplayer.min.js
var artPlayerJS []byte

// Port 默认端口（小众端口：避开 8080/8000/3000/5000 常见位，防局域网冲突）
const Port = "18080"

// Instance 一次完整装配（服务端 + 后台任务），可 Stop
type Instance struct {
	Handler http.Handler
	BaseURL string
	DataDir string
	Relay   *relay.Relay
	Sniff   *sniff.Extractor
	Search  *search.Aggregator

	// 异步封面/元数据缓存（url→media）
	sniffMu    sync.Mutex
	sniffMedia map[string]meta.Media

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
	Logf("[recordSearch] q=%s DataDir=%s", q, inst.DataDir)
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
	// 落盘（持久化，重启不丢）
	if inst.DataDir != "" {
		_ = os.MkdirAll(inst.DataDir, 0o755) // 确保数据目录存在
		data, _ := json.Marshal(out)
		if err := os.WriteFile(filepath.Join(inst.DataDir, "search-hist.json"), data, 0o644); err != nil {
			Logf("[search-hist] 落盘失败: %v", err)
		} else {
			Logf("[search-hist] 落盘成功: %s (%d 条)", filepath.Join(inst.DataDir, "search-hist.json"), len(out))
		}
	}
}

// loadSearchHist 启动时加载搜索记录（持久化）
func (inst *Instance) loadSearchHist() {
	if inst.DataDir == "" {
		return
	}
	data, err := os.ReadFile(filepath.Join(inst.DataDir, "search-hist.json"))
	if err != nil {
		return
	}
	var items []SearchItem
	if json.Unmarshal(data, &items) == nil {
		inst.recentMu.Lock()
		inst.searchHist = items
		inst.recentMu.Unlock()
	}
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
		DataDir: dataDir,
		Relay:   rl,
		Sniff:   sniff.New(sniff.Default()),
	}
	// 搜索聚合器：注入持久化 cookie 提供者（最稳：真实登录态 cookie 纯 HTTP，WebView 仅兜底）
	inst.Search = search.New(search.Default()) // 纯 HTTP 四引擎（bing/360/baidu/sogou），不依赖库
	inst.Cookies = NewCookieStore(dataDir)
	inst.Search.CookieFn = inst.Cookies.HeaderString
	// cookie 通过 Options.CookieFn 传入（库无状态，不持 CookieFn 字段）
	inst.Library = NewLibrary(dataDir)
	inst.loadSearchHist() // 启动加载搜索记录（持久化）

	mux := http.NewServeMux()
	tpl := template.Must(template.ParseFS(tplFS, "web/templates/*.html"))
	// 根路径直接返回 UI（mygo 自定义 scheme 不自动跟随 302 重定向，会显示 Found 白屏）
	mux.HandleFunc("/", func(w http.ResponseWriter, rq *http.Request) {
		// 直接返回 UI（mygo 自定义 scheme 不自动跟随 302 重定向，会显示 Found 白屏）
		if rq.URL.Path == "/" {
			w.Header().Set("Cache-Control", "no-store, max-age=0")
			w.Header().Set("Pragma", "no-cache")
			data := map[string]any{
				"BaseURL": baseURL,
				"Status":  rl.Status(),
				"TVs":     tvList(),
				"Logs":    rl.GetLogs(),
			}
			_ = tpl.ExecuteTemplate(w, "index.html", data)
			return
		}
		http.NotFound(w, rq)
	})
	// 无内部中转：拿流后把真 URL 直推电视，不再有 /m/ 本地会话。
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
		// 先返回集数（快），封面/元数据异步补（不阻塞）
		writeJSON(w, map[string]any{"ok": true, "url": u, "episodes": eps})
		// 后台抓封面 + 元数据（慢，不阻塞响应）
		go func() {
			if htmlText, ferr := e.Fetch(u); ferr == nil {
				media := meta.FromHTML(htmlText)
				if media.Poster == "" {
					media.Poster = e.ExtractPoster(htmlText, u)
				}
				// 存到 inst.sniffMedia[u] 供后续 /api/sniff/media 取（或前端二次拉）
				inst.sniffMu.Lock()
				if inst.sniffMedia == nil {
					inst.sniffMedia = map[string]meta.Media{}
				}
				inst.sniffMedia[u] = media
				inst.sniffMu.Unlock()
			}
		}()
	})

	// 嗅探元数据/封面异步补齐后从这里取：只返回真从页面提取到的，取不到就空（不造假）
	mux.HandleFunc("/api/sniff/media", func(w http.ResponseWriter, rq *http.Request) {
		u := rq.URL.Query().Get("url")
		inst.sniffMu.Lock()
		m, ok := inst.sniffMedia[u]
		inst.sniffMu.Unlock()
		writeJSON(w, map[string]any{"ok": ok, "media": m})
	})

	// 本地播放器专用流代理：解决 hls.js/mp4 跨域被拦（不是投屏中转，无会话）。
	// 路径式代理：/api/stream/s/<base64url(真m3u8 URL)>/<片段名>。
	// m3u8 里的相对片段（0000000.ts）会被浏览器解析到同一路径前缀下，
	// 不需要 hls.js 自定义 loader（实测 subclass loader 会静默失效）。
	mux.HandleFunc("/api/stream/s/", func(w http.ResponseWriter, rq *http.Request) {
		parts := strings.SplitN(strings.TrimPrefix(rq.URL.Path, "/api/stream/s/"), "/", 2)
		if len(parts) != 2 {
			http.Error(w, "bad stream path", http.StatusBadRequest)
			return
		}
		raw, err := base64.RawURLEncoding.DecodeString(parts[0])
		if err != nil {
			http.Error(w, "bad stream token", http.StatusBadRequest)
			return
		}
		target := string(raw)
		if parts[1] != "index.m3u8" {
			if u, e := url.Parse(target); e == nil {
				u.Path = path.Join(path.Dir(u.Path), parts[1])
				target = u.String()
			}
		}
		inst.proxyMedia(w, rq, target, rq.URL.Query().Get("ref"), false)
	})
	mux.HandleFunc("/api/stream", func(w http.ResponseWriter, rq *http.Request) {
		target := rq.URL.Query().Get("url")
		if target == "" {
			http.Error(w, "missing url", http.StatusBadRequest)
			return
		}
		inst.proxyMedia(w, rq, target, rq.URL.Query().Get("ref"), true)
	})

	// 拿流（本地播放用）：解析播放页 → m3u8/mp4，**不碰电视、不需要选设备**。
	mux.HandleFunc("/api/cast", func(w http.ResponseWriter, rq *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		u := rq.URL.Query().Get("url")
		title := rq.URL.Query().Get("title")
		if u == "" {
			writeJSON(w, map[string]any{"ok": false, "msg": "缺少 url"})
			return
		}
		src := &relay.PirateSource{
			Rl:      inst.Relay,
			Extract: sniff.New(sniff.Default()),
			PlayURL: u,
			Title:   title,
		}
		Logf("[cast] 拿流(本地播放) url=%s", u)
		st, err := src.Resolve(rq.Context())
		if err != nil {
			LogError("[cast] 拿流失败 url=%s err=%v", u, err)
			writeJSON(w, map[string]any{"ok": false, "msg": err.Error()})
			return
		}
		writeJSON(w, map[string]any{"ok": true, "url": st.URL, "mime": st.Mime, "ref": st.Headers["Referer"]})
	})

	// 投屏（真投电视用）：拿流 + 真 URL 直推电视，同步返回 SOAP 结果。
	mux.HandleFunc("/api/cast/push", func(w http.ResponseWriter, rq *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		u := rq.URL.Query().Get("url")
		title := rq.URL.Query().Get("title")
		if u == "" {
			writeJSON(w, map[string]any{"ok": false, "msg": "缺少 url"})
			return
		}
		src := &relay.PirateSource{
			Rl:      inst.Relay,
			Extract: sniff.New(sniff.Default()),
			PlayURL: u,
			Title:   title,
		}
		Logf("[cast/push] 拿流+直推 url=%s title=%s", u, title)
		st, err := src.CastWith(rq.Context())
		if err != nil {
			LogError("[cast/push] 失败 url=%s err=%v", u, err)
			writeJSON(w, map[string]any{"ok": false, "msg": err.Error()})
			return
		}
		Logf("[cast/push] 已直推电视 url=%s mime=%s", st.URL, st.Mime)
		writeJSON(w, map[string]any{"ok": true, "url": st.URL, "mime": st.Mime, "direct": true})
	})

	// 搜索历史（搜索记录：点击重搜；播放记录不在这——归媒体库继续观看）
	mux.HandleFunc("/api/search/history", func(w http.ResponseWriter, _ *http.Request) {
		inst.recentMu.Lock()
		items := append([]SearchItem(nil), inst.searchHist...)
		inst.recentMu.Unlock()
		writeJSON(w, map[string]any{"ok": true, "items": items})
	})

	// 清空搜索历史（内存 + 落盘文件一起清，重启不会复活）
	mux.HandleFunc("/api/search/history/clear", func(w http.ResponseWriter, _ *http.Request) {
		inst.recentMu.Lock()
		inst.searchHist = nil
		inst.recentMu.Unlock()
		if inst.DataDir != "" {
			if err := os.Remove(filepath.Join(inst.DataDir, "search-hist.json")); err != nil && !os.IsNotExist(err) {
				Logf("[search-hist] 清除失败: %v", err)
			}
		}
		writeJSON(w, map[string]any{"ok": true})
	})

	mux.HandleFunc("/api/search", func(w http.ResponseWriter, rq *http.Request) {
		q := strings.TrimSpace(rq.URL.Query().Get("q"))
		if q == "" {
			writeJSON(w, map[string]any{"ok": false, "msg": "缺少关键词"})
			return
		}
		// HTTP-first：纯 HTTP 搜搜索引擎（baidu 带 BDUSS 过验证 → JSON 直解资源站 URL）
		engName := rq.URL.Query().Get("engine") // 用户选的引擎（单引擎）
		if engName == "" {
			engName = "baidu" // 默认
		}
		pn := 0
		if pv := rq.URL.Query().Get("pn"); pv != "" {
			n, _ := strconv.Atoi(pv)
			pn = n
		}
		start := time.Now()
		LogInfo("[search] q=%s engine=%s pn=%d 开始（HTTP-first）", q, engName, pn)
		inst.recordSearch(q) // 记历史（不管成败，用户搜了就记）
		// HTTP 搜索（纯 Go，不靠 WebView）
		rs, err := inst.Search.SearchOne(rq.Context(), q, engName, pn)
		if err != nil {
			LogWarn("[search] q=%s 失败 err=%v 耗时=%s", q, err, time.Since(start).Round(time.Millisecond))
			needBrowser := errors.Is(err, search.ErrCaptcha)
			writeJSON(w, map[string]any{"ok": false, "msg": err.Error(), "need_browser": needBrowser, "query": q})
			return
		}
		// 后端全量返回（不过滤）——过滤放前端（开关随时开）
		if len(rs) == 0 {
			LogWarn("[search] q=%s 结果=0（引擎有结果但全被过滤/触发验证码）", q)
			// 搜不到就报错（前端显示明确提示），不再静默返回空
			writeJSON(w, map[string]any{"ok": false, "msg": "搜不到结果（未登录 BDUSS 或引擎被拦），请先扫码登录", "query": q})
			return
		}
		LogInfo("[search] q=%s via=http 结果=%d 耗时=%s", q, len(rs), time.Since(start).Round(time.Millisecond))
		writeJSON(w, map[string]any{"ok": true, "query": q, "results": rs})
	})

	// ── 百度扫码登录（纯 HTTP，不靠 WebView）：前端显示二维码 → 轮询 → 拿 BDUSS 固化 ──
	var loginClient = baidulogin.New()
	mux.HandleFunc("/api/login/qrcode", func(w http.ResponseWriter, rq *http.Request) {
		qr, err := loginClient.GetQRCode(rq.Context())
		if err != nil {
			writeJSON(w, map[string]any{"ok": false, "msg": err.Error()})
			return
		}
		writeJSON(w, map[string]any{"ok": true, "imgurl": qr.ImgURL, "sign": qr.Sign})
	})

	// 二维码图片代理（带 BDUSS cookie 抓 baidu 二维码 PNG，前端 img 走这个绕过跨域+cookie）
	mux.HandleFunc("/api/login/qrcode/img", func(w http.ResponseWriter, rq *http.Request) {
		sign := rq.URL.Query().Get("sign")
		if sign == "" {
			http.Error(w, "缺少 sign", http.StatusBadRequest)
			return
		}
		u := "https://passport.baidu.com/v2/api/qrcode?sign=" + sign + "&lp=pc"
		req, _ := http.NewRequestWithContext(rq.Context(), "GET", u, nil)
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
		if c := inst.Cookies.HeaderString("baidu"); c != "" {
			req.Header.Set("Cookie", c)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "max-age=10")
		_, _ = io.Copy(w, resp.Body)
	})
	mux.HandleFunc("/api/login/poll", func(w http.ResponseWriter, rq *http.Request) {
		sign := rq.URL.Query().Get("sign")
		if sign == "" {
			writeJSON(w, map[string]any{"ok": false, "msg": "缺少 sign"})
			return
		}
		tempBDUSS, status, err := loginClient.Poll(rq.Context(), sign)
		if err != nil {
			writeJSON(w, map[string]any{"ok": false, "msg": err.Error()})
			return
		}
		if status < 2 {
			// 未扫码(0) / 已扫码待确认(1)
			writeJSON(w, map[string]any{"ok": true, "status": status, "msg": map[int]string{0: "等待扫码", 1: "已扫码，请在手机上确认"}[status]})
			return
		}
		// 已确认：正式登录拿真 BDUSS
		res, err := loginClient.Login(rq.Context(), tempBDUSS)
		if err != nil {
			writeJSON(w, map[string]any{"ok": false, "msg": err.Error()})
			return
		}
		// 固化到 cookies/baidu.json（server.CookieEntry 格式）
		_ = inst.Cookies.Save("baidu", []CookieEntry{{Name: "BDUSS", Value: res.BDUSS, Domain: ".baidu.com", Path: "/"}})
		LogInfo("[login] 百度扫码登录成功，BDUSS 已固化")
		writeJSON(w, map[string]any{"ok": true, "status": 2, "bduss": res.BDUSS[:8] + "..."})
	})

	// 登录状态（前端查看 baidu 是否已固化 BDUSS）
	mux.HandleFunc("/api/login/status", func(w http.ResponseWriter, _ *http.Request) {
		engines := inst.Cookies.List()
		n := engines["baidu"]
		status := "logged_out"
		if n > 0 {
			status = "logged_in"
		}
		writeJSON(w, map[string]any{"ok": true, "login_status": status, "cookie_count": n})
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
		u := rq.URL.Query().Get("url")
		if u == "" {
			writeJSON(w, map[string]any{"ok": false, "msg": "缺少 url"})
			return
		}
		if rq.URL.Query().Get("unfav") == "1" {
			inst.Library.Unfav(u)
			writeJSON(w, map[string]any{"ok": true, "fav": false})
			return
		}
		title := rq.URL.Query().Get("title")
		if title == "" {
			writeJSON(w, map[string]any{"ok": false, "msg": "缺少 title"})
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
	mux.HandleFunc("/static/artplayer.min.js", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = w.Write(artPlayerJS)
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

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// /api/poster?url=<封面图>&ref=<站点域名> 代理封面图（绕过防盗链：带站点 Referer）。
// proxyMedia 轻量本地流代理（CORS + 防盗链），rewriteM3U8 控制是否把清单相对片段改成绝对 URL。
func (inst *Instance) proxyMedia(w http.ResponseWriter, rq *http.Request, target, ref string, rewriteM3U8 bool) {
	req, err := http.NewRequestWithContext(rq.Context(), http.MethodGet, target, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if rg := rq.Header.Get("Range"); rg != "" {
		req.Header.Set("Range", rg)
	}
	req.Header.Set("User-Agent", sniff.Default().UA)
	if ref == "" {
		if uu, err := url.Parse(target); err == nil {
			ref = uu.Scheme + "://" + uu.Host + "/"
		}
	}
	req.Header.Set("Referer", ref)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-store")
	for _, h := range []string{"Content-Type", "Content-Length", "Accept-Ranges", "Content-Range"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	// m3u8 清单里的片段是相对路径（0000000.ts），经代理后会被浏览器解析到
	// 本地 /0000000.ts（404）。改写成绝对远程 URL，前端 loader 再逐条包回代理。
	isM3U8 := strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "mpegurl") || strings.HasSuffix(strings.ToLower(target), ".m3u8")
	if rewriteM3U8 && isM3U8 && resp.StatusCode == http.StatusOK {
		body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		base, _ := url.Parse(target)
		var sb strings.Builder
		for _, line := range strings.Split(string(body), "\n") {
			t := strings.TrimRight(line, "\r")
			if t != "" && !strings.HasPrefix(t, "#") && base != nil {
				if ref, e := url.Parse(strings.TrimSpace(t)); e == nil {
					t = base.ResolveReference(ref).String()
				}
			}
			sb.WriteString(t + "\n")
		}
		w.Header().Del("Content-Length")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, sb.String())
		return
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)

}

func (inst *Instance) posterProxy(w http.ResponseWriter, rq *http.Request) {
	u := rq.URL.Query().Get("url")
	if u == "" {
		http.Error(w, "missing url", http.StatusBadRequest)
		return
	}
	// 封面落盘缓存：收藏后即使原站挂了/防盗链变了，媒体库仍能显示真封面。
	// key = sha256(海报URL)，文件存 dataDir/poster-cache/<hex>，meta 存 Content-Type。
	cacheDir := filepath.Join(inst.DataDir, "poster-cache")
	sum := sha256.Sum256([]byte(u))
	key := hex.EncodeToString(sum[:])
	imgPath := filepath.Join(cacheDir, key)
	metaPath := imgPath + ".type"
	if b, err := os.ReadFile(metaPath); err == nil {
		if f, ferr := os.Open(imgPath); ferr == nil {
			defer f.Close()
			if ct := strings.TrimSpace(string(b)); ct != "" {
				w.Header().Set("Content-Type", ct)
			}
			w.Header().Set("Cache-Control", "public, max-age=604800")
			w.Header().Set("X-Poster-Cache", "hit")
			_, _ = io.Copy(w, f)
			return
		}
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
	if resp.StatusCode != http.StatusOK {
		http.Error(w, "upstream "+resp.Status, http.StatusBadGateway)
		return
	}
	ct := resp.Header.Get("Content-Type")
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20)) // 封面最多 8MB
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if len(body) > 0 && strings.HasPrefix(ct, "image/") {
		_ = os.MkdirAll(cacheDir, 0o755)
		_ = os.WriteFile(imgPath+".tmp", body, 0o644)
		_ = os.Rename(imgPath+".tmp", imgPath)
		_ = os.WriteFile(metaPath, []byte(ct), 0o644)
	}
	if ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("Cache-Control", "public, max-age=604800")
	w.Header().Set("X-Poster-Cache", "miss")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// configPath 搜索配置路径（config.json）。
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
