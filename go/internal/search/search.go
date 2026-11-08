// Package search 盗版站搜索：聚合 Bing/360/百度 三个中文搜索引擎（自建服务器专用）。
//
// 独立于 sniff/relay/media：只负责"搜盗版站"（拿详情页 URL），不负责解析集数（那是 sniff）。
// 策略（借鉴 SearXNG 引擎实现 + 实测验证）：
//   - Bing   直连 cn.bing.com/search → li.b_algo → h2/a href（ck/a 链接 base64url 解码）
//   - 360    先预热拿 cookie → www.so.com/s → m.so.com（移动版）→ res-list 的 data-mdurl
//   - 百度   m.baidu.com/s?word= → cosc 卡片 → data-log JSON 的 mu 字段（真实 URL）
//   - 验证码 检测 wappass/tuxing/安全验证 → 换下一引擎（fallback）
//
// 三个引擎就是最优解：搜中文盗版站只需要中文搜索引擎；海外引擎（Brave/Tavily/Kagi）
// 搜不到中文盗版站或要付费 API key，正规 API（百度千帆/博查）又过滤盗版资源。
package search

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	xhtml "golang.org/x/net/html"
)

// Result 一条搜索结果。
type Result struct {
	Engine  string `json:"engine"`           // bing / so / baidu
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content,omitempty"`
}

// Config 搜索配置。
type Config struct {
	UA      string        // 请求 User-Agent（手机 Safari，更接近真实用户）
	Timeout time.Duration // 单请求超时
	Delay   time.Duration // 引擎间退避（验证码/失败时等待）
}

// Default 默认配置：桌面 Chrome UA + 8s 超时 + 3s 引擎间退避。
func Default() Config {
	return Config{
		UA:      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		Timeout: 8 * time.Second,
		Delay:   3 * time.Second,
	}
}

// Aggregator 聚合搜索器：按序试引擎，遇验证码/空结果自动 fallback。
type Aggregator struct {
	cfg Config
	hc  *http.Client

	// CookieFn 持久化 cookie 提供者（账号固化 cookies/<engine>.json）。
	// 最稳的方式：带真实登录态 cookie 纯 HTTP 请求（像真人），WebView 仅兜底。
	// server 层注入 inst.Cookies.HeaderString（engine → cookie header）。
	CookieFn func(engine string) string

	// cookie 缓存（360/百度 预热用，SearXNG 用 EngineCache 同思路）
	cookieMu   sync.Mutex
	soCookie   string
	soCookieAt time.Time
	bdCookie   string
	bdCookieAt time.Time
}

// New 构造聚合搜索器。
func New(cfg Config) *Aggregator {
	if cfg.UA == "" {
		cfg.UA = Default().UA
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = Default().Timeout
	}
	if cfg.Delay == 0 {
		cfg.Delay = Default().Delay
	}
	return &Aggregator{
		cfg: cfg,
		hc:  &http.Client{Timeout: cfg.Timeout},
	}
}

// Search 聚合搜索：bing → so → baidu，去重，返回结果。
// 每个引擎失败（验证码/网络/空结果）自动换下一个；全部失败返回聚合错误。
func (a *Aggregator) Search(ctx context.Context, query string) ([]Result, error) {
	// 并发跑所有引擎（提速：4 引擎同时，不再串行 + 退避）
	engines := []struct {
		name string
		fn   func(context.Context, string) ([]Result, error)
	}{
		{"bing", a.searchBing},
		{"so", a.search360},
		{"baidu", a.searchBaidu},
		{"sogou", a.searchSogou},
	}
	type engRes struct {
		name string
		rs   []Result
		err  error
	}
	ch := make(chan engRes, len(engines))
	for _, eng := range engines {
		go func(eng struct {
			name string
			fn   func(context.Context, string) ([]Result, error)
		}) {
			rs, err := eng.fn(ctx, query)
			ch <- engRes{eng.name, rs, err}
		}(eng)
	}

	seen := map[string]bool{}
	var all []Result
	var errs []string
	captcha := false
	for range engines {
		res := <-ch
		if res.err != nil {
			// 验证码特征：立即标记（不死磕，收完其他在跑引擎后返回报告）。
			if errors.Is(res.err, ErrCaptcha) {
				captcha = true
			}
			errs = append(errs, res.err.Error())
		}
		for _, r := range res.rs {
			// 原样召回：不过滤正版站（用户要求不过滤，B站等原样带回），只去空/去重。
			if r.URL == "" || seen[r.URL] {
				continue
			}
			seen[r.URL] = true
			all = append(all, r)
		}
	}
	// 任一引擎触发验证码：立即返回报告（need_browser），不死磕其他引擎（已收完在跑的）。
	if captcha && len(all) == 0 {
		return nil, fmt.Errorf("%w", ErrCaptcha)
	}
	// 有结果则返回（可能部分引擎失败）；全失败才报错。
	if len(all) == 0 {
		if len(errs) > 0 {
			return nil, errors.New(strings.Join(errs, "; "))
		}
		return nil, errors.New("无结果")
	}
	return all, nil
}

// ── 公共 HTTP 请求 ──

func (a *Aggregator) get(ctx context.Context, rawURL, referer string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	a.applyBrowserHeaders(req)
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	return a.hc.Do(req)
}

// applyBrowserHeaders 补全完整浏览器头（模拟 Chrome，学 SearXNG 的请求构建）。
// 关键：Sec-Fetch-*/Accept-Encoding: br/Upgrade-Insecure-Requests 让搜索引擎不识别为爬虫。
func (a *Aggregator) applyBrowserHeaders(req *http.Request) {
	req.Header.Set("User-Agent", a.cfg.UA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	// 不显式设 Accept-Encoding：Go http.Transport 只在未设时自动加 gzip + 自动解压。
	// 显式设（如 "gzip, deflate" 或含 br）→ Go 不解压 → 拿压缩字节解析失败（152KB 无 res-list）。
	// 删掉后 Go 自动 gzip + 解压 → 360 正常出 594KB 8 res-list。
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("DNT", "1")
	req.Header.Set("Cache-Control", "max-age=0")
}

// isLegitSite 已知正版平台域名：搜盗版站时过滤掉（B站/优酷/爱奇艺/腾讯等走各自链路，不与盗版站混）。
func isLegitSite(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	// 百度新版跳转链接 baidu.com/link?url=：不是合法站（要 follow 到真实盗版站），不当合法站过滤
	if strings.Contains(u.Path, "/link") && strings.Contains(u.RawQuery, "url=") &&
		(host == "baidu.com" || strings.HasSuffix(host, ".baidu.com")) {
		return false
	}
	for _, d := range []string{
		"bilibili.com", "b23.tv", "youku.com", "iqiyi.com", "qiyi.com",
		"qq.com", "v.qq.com", "mgtv.com", "sohu.com", "douyin.com",
		"miguvideo.com", "1905.com", "cctv.com", "xigua.com", "baidu.com",
		"zhihu.com", "weibo.com", "douban.com", "sogou.com", "so.com",
	} {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// isCaptcha 验证码/安全验证检测：命中返回 true（应换引擎）。
func isCaptcha(body string) bool {
	low := strings.ToLower(body)
	// 真实验证码特征（独立词）。
	for _, k := range []string{
		"captcha/tuxing", "百度安全验证", "请输入验证码",
		"访问验证", "验证码验证", "百度智能云安全",
	} {
		if strings.Contains(low, strings.ToLower(k)) {
			return true
		}
	}
	// wappass 跳转只在指向 /verify 或 /captcha 时才是真验证码；
	// 页面里 loginUrlPrefix / passport 登录跳转不算。
	if strings.Contains(low, "wappass") &&
		(strings.Contains(low, "/verify") || strings.Contains(low, "/captcha")) {
		return true
	}
	return false
}

// ── Bing（学 SearXNG：www.bing.com/search + base64url 解码）──

func (a *Aggregator) searchBing(ctx context.Context, query string) ([]Result, error) {
	u := "https://www.bing.com/search?q=" + url.QueryEscape(query)
	resp, err := a.get(ctx, u, "https://www.bing.com/")
	if err != nil {
		return nil, fmt.Errorf("bing: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("bing: HTTP %d", resp.StatusCode)
	}
	body := readBody(resp)
	// 验证码/安全验证特征：立即返回（不死磕），handler 识别后走 WebView 兑底。
	if isCaptcha(body) {
		return nil, fmt.Errorf("bing: %w", ErrCaptcha)
	}
	doc, err := xhtml.Parse(strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("bing: 解析失败: %w", err)
	}
	var rs []Result
	walkNodes(doc, func(n *xhtml.Node) {
		if n.Type != xhtml.ElementNode || n.Data != "li" || !hasClass(n, "b_algo") {
			return
		}
		// h2 > a
		title, href := "", ""
		walkNodes(n, func(m *xhtml.Node) {
			if m.Type == xhtml.ElementNode && m.Data == "a" && href == "" {
				for _, at := range m.Attr {
					if at.Key == "href" {
						href = at.Val
					}
				}
				title = strings.TrimSpace(textOf(m))
			}
		})
		href = bingRealURL(href)
		title = cleanTitle(title)
		if title != "" && href != "" {
			rs = append(rs, Result{Engine: "bing", Title: title, URL: href})
		}
	})
	return rs, nil
}

// cleanTitle 清理标题：去 URL 混入（bing 卡片可能把域名/URL 混进文本）。
func cleanTitle(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "http"); i > 0 {
		s = strings.TrimSpace(s[:i])
	}
	return s
}

// bingRealURL Bing 跳转链接 base64url 解码：/ck/a?u=a1<base64url> → 真实 URL。
func bingRealURL(href string) string {
	if !strings.Contains(href, "/ck/a?") {
		return href
	}
	qs, err := url.Parse(href)
	if err != nil {
		return href
	}
	u := qs.Query().Get("u")
	if !strings.HasPrefix(u, "a1") {
		return href
	}
	enc := u[2:]
	// base64 填充：保证 0-3（Go % 对负数返回负，会 panic strings.Repeat 负数）。
	enc += strings.Repeat("=", (4-len(enc)%4)%4)
	b, err := base64.URLEncoding.DecodeString(enc)
	if err != nil {
		return href
	}
	return string(b)
}

// ── 360（本环境 www.so.com 强制 302→m.so.com，用移动版 m.so.com + XPath 标题）──

func (a *Aggregator) search360(ctx context.Context, query string) ([]Result, error) {
	// 预热拿 cookie（SearXNG get_cookie 思路）
	a.soCookieOrWarm(ctx)
	// 实测：m.so.com 302（手机 UA/移动版反爬），www.so.com + 桌面 UA 200 出真结果。
	// 手机「很容易验证码 360」根因 = 360 对移动端点/UA 302→验证码，桌面端点+桌面 UA 正常。
	u := "https://www.so.com/s?q=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("so: %w", err)
	}
	a.applyBrowserHeaders(req)
	req.Header.Set("Referer", "https://www.so.com/")
	// 只注入持久化账号固化 cookie（真实登录态，最稳——像真人）；
	// 不注入临时预热 cookie：实测 360 对临时 cookie 返回 152KB 无 res-list（识别为异常），
	// 无 cookie 反而 200 出 8 个 res-list。真实登录态 cookie 才稳（用户验证）。
	if a.CookieFn != nil {
		if c := a.CookieFn("so"); c != "" {
			req.Header.Set("Cookie", c)
		}
	}
	resp, err := a.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("so: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("so: HTTP %d", resp.StatusCode)
	}
	body := readBody(resp)
	if strings.TrimSpace(body) == "" {
		return nil, fmt.Errorf("so: 空响应（非中国 IP / 被拦）")
	}
	// 验证码/安全验证特征：立即返回（不死磕），handler 识别后走 WebView 兑底。
	if isCaptcha(body) {
		return nil, fmt.Errorf("so: %w", ErrCaptcha)
	}
	// XPath 语义：//li[contains(@class,"res-list")] → h3.res-title/a → @data-mdurl + 标题
	doc, err := xhtml.Parse(strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("so: 解析失败: %w", err)
	}
	var rs []Result
	walkNodes(doc, func(n *xhtml.Node) {
		if n.Type != xhtml.ElementNode || n.Data != "li" || !hasClass(n, "res-list") {
			return
		}
		aNode, urlStr, title := findResTitleA(n)
		if aNode == nil {
			return
		}
		_ = aNode
		if urlStr == "" || strings.HasPrefix(urlStr, "javascript") {
			return
		}
		title = cleanTitle(title)
		rs = append(rs, Result{Engine: "so", Title: title, URL: urlStr})
	})
	if len(rs) == 0 {
		return nil, fmt.Errorf("so: 未提取到结果")
	}
	return rs, nil
}

// soCookieOrWarm 拿 360 cookie（缓存 1h；无则预热 www.so.com 取 set-cookie）。
func (a *Aggregator) soCookieOrWarm(ctx context.Context) string {
	a.cookieMu.Lock()
	defer a.cookieMu.Unlock()
	if a.soCookie != "" && time.Since(a.soCookieAt) < time.Hour {
		return a.soCookie
	}
	// 预热：GET www.so.com/ 拿 set-cookie
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.so.com/", nil)
	if req == nil {
		return ""
	}
	a.applyBrowserHeaders(req)
	resp, err := a.hc.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	for _, c := range resp.Cookies() {
		if c.Name != "" {
			a.soCookie = c.Name + "=" + c.Value
			a.soCookieAt = time.Now()
			break
		}
	}
	return a.soCookie
}

// findResTitleA 在 li.res-list 里找 h3.res-title/a，返回 (节点, data-mdurl或href, 标题)。
func findResTitleA(n *xhtml.Node) (*xhtml.Node, string, string) {
	var res *xhtml.Node
	walkNodes(n, func(m *xhtml.Node) {
		if m.Type != xhtml.ElementNode || m.Data != "a" || res != nil {
			return
		}
		// 该 <a> 的父级是否 h3.res-title（或自身含 title class）
		if parentIsResTitle(m) || hasClass(m, "title") {
			res = m
		}
	})
	if res == nil {
		return nil, "", ""
	}
	// URL：@data-mdurl 优先，@href 兜底
	urlStr := ""
	for _, at := range res.Attr {
		if at.Key == "data-mdurl" && at.Val != "" {
			urlStr = at.Val
		}
	}
	if urlStr == "" {
		for _, at := range res.Attr {
			if at.Key == "href" {
				urlStr = at.Val
			}
		}
	}
	urlStr = html.UnescapeString(urlStr)
	return res, urlStr, textOf(res)
}

// parentIsResTitle 判断节点的父级是否 h3 且含 res-title class。
func parentIsResTitle(n *xhtml.Node) bool {
	if n.Parent == nil || n.Parent.Data != "h3" {
		return false
	}
	return hasClass(n.Parent, "res-title")
}

// ── 百度（本环境 www.baidu.com tn=json 必 302 验证码，改用移动版 m.baidu.com + data-log）──

func (a *Aggregator) searchBaidu(ctx context.Context, query string) ([]Result, error) {
	// 移动版 m.baidu.com/s（实测 200，data-log 的 mu 有结果；www tn=json 被验证码拦）
	u := "https://m.baidu.com/s?word=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("baidu: %w", err)
	}
	a.applyBrowserHeaders(req)
	req.Header.Set("Referer", "https://m.baidu.com/")
	// 持久化账号固化 cookie 优先（百度验证码 wappass 需 cookie 态）
	if a.CookieFn != nil {
		if c := a.CookieFn("baidu"); c != "" {
			req.Header.Set("Cookie", c)
		}
	}
	resp, err := a.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("baidu: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("baidu: HTTP %d", resp.StatusCode)
	}
	body := readBody(resp)
	if isCaptcha(body) {
		return nil, fmt.Errorf("baidu: %w", ErrCaptcha)
	}
	// data-log='{"mu":"真实URL"}' → mu（兼容单引号）
	var rs []Result
	for _, m := range reDataLog.FindAllStringSubmatch(body, -1) {
		mu := baiduMu(m[1])
		if mu == "" || strings.HasPrefix(mu, "javascript") {
			continue
		}
		rs = append(rs, Result{Engine: "baidu", URL: mu})
	}
	// 百度新版（2026+）：data-log 无 mu，外链全走 baidu.com/link?url= 跳转（要 follow 真实目标）
	if len(rs) == 0 {
		for _, m := range reBaiduLink.FindAllStringSubmatch(body, -1) {
			u := m[1]
			if u == "" {
				continue
			}
			rs = append(rs, Result{Engine: "baidu", URL: u})
		}
	}
	if len(rs) == 0 {
		return nil, fmt.Errorf("baidu: 未提取到结果")
	}
	return rs, nil
}

// baiduMu 从 data-log JSON 取 mu（真实 URL）。
func baiduMu(raw string) string {
	decoded := html.UnescapeString(raw)
	var obj struct {
		Mu string `json:"mu"`
	}
	if err := json.Unmarshal([]byte(decoded), &obj); err != nil {
		return ""
	}
	return obj.Mu
}

// ── 工具 ──

// ErrCaptcha 验证码/安全验证特征：搜索引擎触发反爬验证码。
// 检测到立即返回（不重试/不死磕），handler 识别后返回 need_browser 报告，前端走 WebView 兑底。
var ErrCaptcha = errors.New("触发安全验证（验证码）")

var (
	// 百度 data-log：兼容单引号 data-log='{"mu":...}'（移动版用单引号）
	reDataLog = regexp.MustCompile(`data-log=['"]([^'"]*mu[^'"]*)['"]`)
	// 百度新版（2026+）：结果链接全走 baidu.com/link?url= 跳转
	reBaiduLink = regexp.MustCompile(`href="(https?://(?:www\.)?baidu\.com/link\?url=[^"]+)"`)
	// 兑底：任意 <a href="http...">（新版百度真链，排除 baidu 域在代码里判）
	reAnyHref = regexp.MustCompile(`href="(https?://[^"]+)"`)
	// bing 结果项（已用 XPath 遍历，正则保留备查）
	reTag = regexp.MustCompile(`<[^>]+>`)
)

// hasClass 判断节点是否含指定 class。
func hasClass(n *xhtml.Node, cls string) bool {
	for _, at := range n.Attr {
		if at.Key == "class" {
			for _, c := range strings.Fields(at.Val) {
				if c == cls {
					return true
				}
			}
		}
	}
	return false
}

// textOf 提取节点纯文本。
func textOf(n *xhtml.Node) string {
	var b strings.Builder
	var walk func(*xhtml.Node)
	walk = func(m *xhtml.Node) {
		if m.Type == xhtml.TextNode {
			b.WriteString(m.Data)
		}
		for c := m.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.TrimSpace(b.String())
}

// walkNodes 深度遍历，回调每个节点（含自身）。
func walkNodes(n *xhtml.Node, fn func(*xhtml.Node)) {
	if n == nil {
		return
	}
	fn(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walkNodes(c, fn)
	}
}

// readBody 读响应体（限制 4MB 防超大）。
func readBody(resp *http.Response) string {
	buf := make([]byte, 0, 1<<20)
	tmp := make([]byte, 64<<10)
	for {
		n, err := resp.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if len(buf) > 4<<20 {
			break
		}
		if err != nil {
			break
		}
	}
	return string(buf)
}

// ParseRendered 解析 WebView 渲染后的搜索引擎结果页 HTML。
// 搜索引擎（百度移动版等）纯 HTTP 拿空壳，WebView JS 渲染后出真结果；
// 这里复用三引擎各自的解析逻辑，按来源引擎分派。
// 参数：
//   htmlText: WebView 回传的 outerHTML
//   pageURL:  渲染的页面 URL（判断引擎）
//   engine:   已判定的引擎（bing/so/baidu/webview）
func (a *Aggregator) ParseRendered(htmlText, pageURL, engine string) []Result {
	if strings.TrimSpace(htmlText) == "" {
		return nil
	}
	// 按引擎分派解析（复用各自逻辑）
	switch engine {
	case "so":
		return parseSoHTML(htmlText)
	case "baidu":
		return parseBaiduHTML(htmlText)
	case "bing":
		return parseBingHTML(htmlText)
	default:
		// 未知引擎：尝试全部解析器，去重合并
		seen := map[string]bool{}
		var out []Result
		for _, rs := range [][]Result{
			parseBingHTML(htmlText), parseSoHTML(htmlText), parseBaiduHTML(htmlText),
		} {
			for _, r := range rs {
				if r.URL == "" || seen[r.URL] || isLegitSite(r.URL) {
					continue
				}
				seen[r.URL] = true
				out = append(out, r)
			}
		}
		return out
	}
}

// parseBingHTML 解析 bing 结果页 HTML（li.b_algo → h2/a）。
func parseBingHTML(htmlText string) []Result {
	doc, err := xhtml.Parse(strings.NewReader(htmlText))
	if err != nil {
		return nil
	}
	var rs []Result
	walkNodes(doc, func(n *xhtml.Node) {
		if n.Type != xhtml.ElementNode || n.Data != "li" || !hasClass(n, "b_algo") {
			return
		}
		title, href := "", ""
		walkNodes(n, func(m *xhtml.Node) {
			if m.Type == xhtml.ElementNode && m.Data == "a" && href == "" {
				for _, at := range m.Attr {
					if at.Key == "href" {
						href = at.Val
					}
				}
				title = strings.TrimSpace(textOf(m))
			}
		})
		href = bingRealURL(href)
		title = cleanTitle(title)
		if title != "" && href != "" {
			rs = append(rs, Result{Engine: "bing", Title: title, URL: href})
		}
	})
	return rs
}

// parseSoHTML 解析 360 结果页 HTML（li.res-list → h3.res-title/a → data-mdurl + 标题）。
func parseSoHTML(htmlText string) []Result {
	doc, err := xhtml.Parse(strings.NewReader(htmlText))
	if err != nil {
		return nil
	}
	var rs []Result
	walkNodes(doc, func(n *xhtml.Node) {
		if n.Type != xhtml.ElementNode || n.Data != "li" || !hasClass(n, "res-list") {
			return
		}
		_, urlStr, title := findResTitleA(n)
		if urlStr == "" || strings.HasPrefix(urlStr, "javascript") {
			return
		}
		rs = append(rs, Result{Engine: "so", Title: cleanTitle(title), URL: urlStr})
	})
	return rs
}

// parseBaiduHTML 解析百度结果页 HTML（data-log 的 mu 字段）。
func parseBaiduHTML(htmlText string) []Result {
	var rs []Result
	// 规则1（旧版）：data-log='{"mu":"真实URL"}'
	for _, m := range reDataLog.FindAllStringSubmatch(htmlText, -1) {
		mu := baiduMu(m[1])
		if mu == "" || strings.HasPrefix(mu, "javascript") {
			continue
		}
		rs = append(rs, Result{Engine: "baidu", URL: mu})
	}
	// 规则2（新版 2026+）：结果链接全走 http(s)://www.baidu.com/link?url= 跳转（外链 0，data-log 已移除）
	// 抓 baidu.com/link?url= 出现频次最高的结果链接（同一条结果 href 重复出现，去重）
	if len(rs) == 0 {
		seen := map[string]bool{}
		for _, m := range reBaiduLink.FindAllStringSubmatch(htmlText, -1) {
			u := html.UnescapeString(m[1])
			key := u
			if i := strings.Index(u, "&"); i > 0 {
				key = u[:i] // 去重按 link?url= 核心（忽略追踪参数，同结果多条 href 合并）
			}
			if u == "" || seen[key] {
				continue
			}
			seen[key] = true
			rs = append(rs, Result{Engine: "baidu", URL: u})
		}
		// 规则3（兜底）：非 baidu 域的直链 <a href="http...">（有时结果直接给真链）
		if len(rs) == 0 {
			for _, m := range reAnyHref.FindAllStringSubmatch(htmlText, -1) {
				u := html.UnescapeString(m[1])
				lu := strings.ToLower(u)
				if strings.Contains(lu, "baidu.com") || strings.Contains(lu, "bdstatic") || strings.Contains(lu, "bcebos") || strings.Contains(lu, "hao123") {
					continue
				}
				if !strings.HasPrefix(lu, "http") || seen[u] {
					continue
				}
				seen[u] = true
				rs = append(rs, Result{Engine: "baidu", URL: u})
			}
		}
	}
	return rs
}

// IsLegitSite 导出包装（供 server 的 Chrome 兜底路径过滤正版平台）。
func IsLegitSite(rawURL string) bool { return isLegitSite(rawURL) }

// searchSogou 搜狗搜索（sogou.com）。页面结构：.vrwrap > .vr-title > a，或 h3 > a。
// 链接常为 sogou 跳转 /link?url=xxx（保留原样，前端/嗅探 follow）。
func (a *Aggregator) searchSogou(ctx context.Context, query string) ([]Result, error) {
	u := "https://www.sogou.com/web?query=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("sogou: %w", err)
	}
	a.applyBrowserHeaders(req)
	req.Header.Set("Referer", "https://www.sogou.com/")
	if a.CookieFn != nil {
		if c := a.CookieFn("sogou"); c != "" {
			req.Header.Set("Cookie", c)
		}
	}
	resp, err := a.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sogou: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("sogou: HTTP %d", resp.StatusCode)
	}
	body := readBody(resp)
	if strings.TrimSpace(body) == "" {
		return nil, fmt.Errorf("sogou: 空响应")
	}
	if isCaptcha(body) {
		return nil, fmt.Errorf("sogou: %w", ErrCaptcha)
	}
	doc, err := xhtml.Parse(strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("sogou: 解析失败: %w", err)
	}
	var rs []Result
	// .vrwrap（结果容器）内的标题链接；兜底 h3 a
	walkNodes(doc, func(n *xhtml.Node) {
		if n.Type != xhtml.ElementNode || n.Data != "div" || !hasClass(n, "vrwrap") {
			return
		}
		var href, title string
		walkNodes(n, func(m *xhtml.Node) {
			if m.Type == xhtml.ElementNode && m.Data == "a" && href == "" {
				for _, at := range m.Attr { if at.Key == "href" { href = at.Val } }
				title = textOf(m)
			}
		})
		if href == "" || strings.HasPrefix(href, "javascript") {
			return
		}
		title = cleanTitle(title)
		if title == "" {
			return
		}
		rs = append(rs, Result{Engine: "sogou", Title: title, URL: href})
	})
	// 兜底：h3 a（sogou 某些结果结构）
	if len(rs) == 0 {
		walkNodes(doc, func(n *xhtml.Node) {
			if n.Type != xhtml.ElementNode || n.Data != "h3" {
				return
			}
			var href, title string
			walkNodes(n, func(m *xhtml.Node) {
				if m.Type == xhtml.ElementNode && m.Data == "a" && href == "" {
					for _, at := range m.Attr { if at.Key == "href" { href = at.Val } }
					title = textOf(m)
				}
			})
			if href == "" || strings.HasPrefix(href, "javascript") {
				return
			}
			title = cleanTitle(title)
			if title != "" {
				rs = append(rs, Result{Engine: "sogou", Title: title, URL: href})
			}
		})
	}
	if len(rs) == 0 {
		return nil, fmt.Errorf("sogou: 未提取到结果")
	}
	return rs, nil
}
