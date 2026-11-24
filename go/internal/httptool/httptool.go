// Package httptool 提供"像真人"的 HTTP 客户端：Cookie Jar 持久化 + 请求头伪装 + 请求节流抖动。
//
// 设计动机（项目重启路线：HTTP-first）：
//   - 以前依赖 WebView 渲染拿内容，现在是纯 HTTP。纯 HTTP 的成败关键就是"像不像真人"。
//   - 野请求封控的三大判据：①请求频率规律 ②请求头残缺 ③无会话态。
//     本包从三点应对：随机节流、完整浏览器头、Cookie 持久化（真实登录态复用）。
//
// Cookie 持久化格式：`<CookieDir>/<host>.json`，与 internal/server/cookies.go 的
// `cookies/<engine>.json` 思路一致，但这里按 host 存（HTTP 层天然按域隔离）。
package httptool

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// DefaultUA 移动端 Chrome UA（与 sniff.Config.UA 默认值一致，保持全局统一）。
const DefaultUA = "Mozilla/5.0 (Linux; Android 13; SM-S9180) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Mobile Safari/537.36"

// Config httptool 配置。
type Config struct {
	UA      string        // User-Agent（空=DefaultUA）
	Referer string        // 固定 Referer（空=按 URL 同源）
	Timeout time.Duration // 单次请求超时（0=30s）

	// 节流：两次请求之间的最小间隔 + 抖动系数（抗野请求封控的关键）。
	// 实际等待 = Delay * (1 + rand()*Jitter)。Delay=0 表示不节流。
	Delay  time.Duration // 基础间隔（建议 500~1500ms）
	Jitter float64       // 抖动比例（0.3 = 在 Delay 上再加 0~30% 随机）

	CookieDir   string // Cookie 持久化目录（空=不持久化）
	MaxBytes    int64  // 响应体读取上限（0=64MB）
	TLSInsecure bool   // 忽略证书错误（部分盗版站证书链不全）
}

// Client 带 CookieJar + 持久化 + 节流 + 请求头伪装的 HTTP 客户端。
// 零 CGo，可交叉编译到 android/linux 任意平台。
type Client struct {
	cfg Config
	hc  *http.Client
	jar *cookiejar.Jar

	mu   sync.Mutex
	last time.Time // 上次请求时间（节流用）
}

// New 构造 Client。
func New(cfg Config) (*Client, error) {
	if cfg.UA == "" {
		cfg.UA = DefaultUA
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.MaxBytes == 0 {
		cfg.MaxBytes = 64 << 20
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("httptool: cookiejar: %w", err)
	}

	tr := &http.Transport{
		// 连接复用是"像真人"的重要一环：真实浏览器复用 TCP/TLS 连接。
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: cfg.TLSInsecure},
		// 显式 HTTP/2 由 Go 自动协商（ALPN）；此处不强制降级。
	}
	c := &Client{
		cfg: cfg,
		hc: &http.Client{
			Timeout:   cfg.Timeout,
			Jar:       jar,
			Transport: tr,
			// 302 重定向默认跟 10 次（浏览器行为），不设为 0。
		},
		jar: jar,
	}
	if cfg.CookieDir != "" {
		c.LoadCookies()
	}
	return c, nil
}

// HTTPClient 返回底层 *http.Client（供调用方直接 Do，如 relay 需要自定义）。
func (c *Client) HTTPClient() *http.Client { return c.hc }

// Throttle 按配置节流：保证两次请求之间有 Delay*(1+rand*Jitter) 的间隔。
// 抗野请求封控：固定频率的机器流量是风控最易识别的特征，抖动把它打散。
func (c *Client) Throttle() {
	if c.cfg.Delay <= 0 {
		return
	}
	c.mu.Lock()
	elapsed := time.Since(c.last)
	c.mu.Unlock()
	if elapsed >= c.cfg.Delay {
		return
	}
	extra := time.Duration(float64(c.cfg.Delay) * rand.Float64() * c.cfg.Jitter)
	wait := c.cfg.Delay - elapsed + extra
	time.Sleep(wait)
	c.mu.Lock()
	c.last = time.Now()
	c.mu.Unlock()
}

// markRequest 记录一次请求（内部）。
func (c *Client) markRequest() {
	c.mu.Lock()
	c.last = time.Now()
	c.mu.Unlock()
}

// Do 执行一次请求：应用浏览器头 + 节流 + Cookie Jar（自动带域内 cookie）+ 保存 cookie。
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	c.Throttle()
	c.applyHeaders(req)
	resp, err := c.hc.Do(req)
	c.markRequest()
	if err != nil {
		return nil, err
	}
	c.SaveCookies(req.URL.String()) // 把这次请求收到的新 cookie 落盘（登录态固化）
	return resp, nil
}

// Get 便捷方法：GET + 读完整 body（限 MaxBytes）。
func (c *Client) Get(rawURL string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", &HTTPError{Code: resp.StatusCode}
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, c.cfg.MaxBytes))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// applyHeaders 补齐完整浏览器头（风控常检查这些是否残缺）。
func (c *Client) applyHeaders(req *http.Request) {
	h := req.Header
	h.Set("User-Agent", c.cfg.UA)
	h.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	h.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	// 不显式设 Accept-Encoding：Go http.Transport 只在未设时自动加 gzip + 自动解压。
	// 显式设 → Go 不解压 → 拿到压缩字节解析失败（同 search.go 踩过的 152KB 无结果坑）。
	h.Set("Upgrade-Insecure-Requests", "1")
	h.Set("Connection", "keep-alive")
	h.Set("Sec-Fetch-Dest", "document")
	h.Set("Sec-Fetch-Mode", "navigate")
	h.Set("Sec-Fetch-Site", "none")
	h.Set("Sec-Fetch-User", "?1")
	h.Set("Sec-Ch-Ua", `"Android Chrome";v="120", "Chromium";v="120", "?Not_A_Brand";v="99"`)
	h.Set("Sec-Ch-Ua-Mobile", "?1")
	h.Set("Sec-Ch-Ua-Platform", `"Android"`)
	// Referer：固定配置优先，否则同源（防盗链常校验同源）。
	if c.cfg.Referer != "" {
		h.Set("Referer", c.cfg.Referer)
	} else if u, err := url.Parse(req.URL.String()); err == nil && u.Host != "" {
		h.Set("Referer", u.Scheme+"://"+u.Host+"/")
	}
}

// ---------- Cookie 持久化 ----------

// cookieEntry 落盘格式。
type cookieEntry struct {
	Name     string    `json:"name"`
	Value    string    `json:"value"`
	Path     string    `json:"path"`
	Domain   string    `json:"domain"`
	Secure   bool      `json:"secure"`
	HttpOnly bool      `json:"httpOnly"`
	Expires  time.Time `json:"expires"` // 零值=会话 cookie
}

// SaveCookies 把 jar 里某 URL 所属域的 cookie 写到磁盘。
func (c *Client) SaveCookies(rawURL string) {
	if c.cfg.CookieDir == "" {
		return
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return
	}
	host := u.Host
	if strings.HasSuffix(host, ":443") {
		host = host[:len(host)-4]
	} else if strings.HasSuffix(host, ":80") {
		host = host[:len(host)-3]
	}
	cookies := c.jar.Cookies(u)
	if len(cookies) == 0 {
		return
	}
	if err := os.MkdirAll(c.cfg.CookieDir, 0o755); err != nil {
		return
	}
	entries := make([]cookieEntry, 0, len(cookies))
	for _, ck := range cookies {
		entries = append(entries, cookieEntry{
			Name: ck.Name, Value: ck.Value, Path: ck.Path,
			Domain: ck.Domain, Secure: ck.Secure, HttpOnly: ck.HttpOnly,
			Expires: ck.Expires,
		})
	}
	b, _ := json.MarshalIndent(entries, "", "  ")
	_ = os.WriteFile(filepath.Join(c.cfg.CookieDir, host+".json"), b, 0o644)
}

// LoadCookies 启动时从磁盘恢复全部 cookie（真实登录态复用）。
func (c *Client) LoadCookies() {
	if c.cfg.CookieDir == "" {
		return
	}
	items, err := os.ReadDir(c.cfg.CookieDir)
	if err != nil {
		return
	}
	for _, it := range items {
		if it.IsDir() || filepath.Ext(it.Name()) != ".json" {
			continue
		}
		host := it.Name()[:len(it.Name())-len(".json")]
		// 用该域的 URL 作为 jar 的索引（cookiejar 按域匹配）
		u, err := url.Parse("https://" + host + "/")
		if err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join(c.cfg.CookieDir, it.Name()))
		if err != nil {
			continue
		}
		var entries []cookieEntry
		if err := json.Unmarshal(b, &entries); err != nil {
			continue
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
		cks := make([]*http.Cookie, 0, len(entries))
		for _, e := range entries {
			cks = append(cks, &http.Cookie{
				Name: e.Name, Value: e.Value, Path: e.Path,
				Domain: e.Domain, Secure: e.Secure,
				HttpOnly: e.HttpOnly, Expires: e.Expires,
			})
		}
		c.jar.SetCookies(u, cks)
	}
}

// HTTPError HTTP 状态码错误。
type HTTPError struct{ Code int }

func (e *HTTPError) Error() string { return fmt.Sprintf("httptool: http status %d", e.Code) }
