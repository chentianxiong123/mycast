// Package baidulogin 百度扫码登录（纯 HTTP，不靠 WebView）。
//
// 流程（逆向自 passport.baidu.com，多来源验证 BDUSS 与浏览器登录同性质）：
//   ① GET /v2/api/getqrcode → {imgurl, sign}（二维码图片 URL + 唯一 sign）
//   ② 轮询 GET /channel/unicast?channel_id=<sign> → 扫码后返回临时 BDUSS（channel_v.v）
//   ③ GET /v3/login/main/qrbdusslogin?bduss=<临时BDUSS> → Set-Cookie 拿真 BDUSS/PTOKEN/STOKEN
//
// 拿到的 BDUSS 与浏览器登录 BDUSS 完全通用（百度系全站，含 www.baidu.com/s 搜索验证）。
package baidulogin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	getQRCodeURL = "https://passport.baidu.com/v2/api/getqrcode"
	unicastURL   = "https://passport.baidu.com/channel/unicast"
	loginURL     = "https://passport.baidu.com/v3/login/main/qrbdusslogin"
)

var callbackRe = regexp.MustCompile(`\((\{.*\})\)`)

// QRCode 二维码信息（前端显示用）。
type QRCode struct {
	ImgURL string `json:"imgurl"` // 二维码图片 URL
	Sign   string `json:"sign"`   // 唯一 sign（轮询用）
}

// Result 登录结果（扫码成功后的 cookie）。
type Result struct {
	BDUSS  string `json:"bduss"`
	PTOKEN string `json:"ptoken"`
	STOKEN string `json:"stoken"`
}

// Client 百度扫码登录客户端（持 cookie jar，保持会话）。
type Client struct {
	hc  *http.Client
	jar *cookieJar
}

// New 创建客户端（带 cookie jar，扫码登录要 session cookie）。
func New() *Client {
	jar := newCookieJar()
	return &Client{
		hc: &http.Client{
			Timeout: 15 * time.Second,
			Jar:     jar,
		},
		jar: jar,
	}
}

// GetQRCode 拿二维码（imgurl + sign）。
func (c *Client) GetQRCode(ctx context.Context) (*QRCode, error) {
	u := getQRCodeURL + "?lp=pc&qrloginfrom=pc&gid=" + randGID() + "&apiver=v3&tt=" + nowMs()
	req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	req.Header.Set("User-Agent", desktopUA)
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("getqrcode: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var r struct {
		ImgURL string `json:"imgurl"`
		Sign   string `json:"sign"`
		Errno  int    `json:"errno"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("getqrcode 解析: %w", err)
	}
	if r.Errno != 0 || r.Sign == "" {
		return nil, fmt.Errorf("getqrcode errno=%d", r.Errno)
	}
	// imgurl 可能相对（passport.baidu.com/...），补全 https://
	imgurl := r.ImgURL
	if !strings.HasPrefix(imgurl, "http://") && !strings.HasPrefix(imgurl, "https://") {
		imgurl = "https://" + imgurl
	}
	return &QRCode{ImgURL: imgurl, Sign: r.Sign}, nil
}

// Poll 轮询扫码状态。返回 (临时BDUSS, 状态, error)。
// 状态：0=未扫码/等待，1=已扫码待确认，2=已确认（拿到临时 BDUSS）。
func (c *Client) Poll(ctx context.Context, sign string) (string, int, error) {
	u := unicastURL + "?channel_id=" + sign + "&callback="
	req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	req.Header.Set("User-Agent", desktopUA)
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	// 响应是 jsonp: callback({...})，提取 {...}
	m := callbackRe.FindSubmatch(body)
	if m == nil {
		return "", 0, nil // 未扫码/超时
	}
	var r struct {
		Errno    int    `json:"errno"`
		ChannelV string `json:"channel_v"`
	}
	if err := json.Unmarshal(m[1], &r); err != nil {
		return "", 0, nil
	}
	if r.Errno != 0 || r.ChannelV == "" {
		return "", 0, nil // 未扫码
	}
	// channel_v 是嵌套 JSON 字符串：{"status":0,"v":"<临时BDUSS>"}
	var cv struct {
		Status int    `json:"status"`
		V      string `json:"v"`
	}
	if err := json.Unmarshal([]byte(r.ChannelV), &cv); err != nil {
		return "", 1, nil // 已扫码待确认
	}
	if cv.Status == 0 && cv.V != "" {
		return cv.V, 2, nil // 已确认，拿到临时 BDUSS
	}
	return "", 1, nil // 已扫码待确认
}

// Login 用临时 BDUSS 正式登录，拿真 BDUSS/PTOKEN/STOKEN（Set-Cookie）。
func (c *Client) Login(ctx context.Context, tempBDUSS string) (*Result, error) {
	u := loginURL + "?bduss=" + tempBDUSS + "&u=https%3A%2F%2Fwww.baidu.com%2F&loginVersion=v4&qrcode=1&apiver=v3&tt=" + nowMs()
	req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	req.Header.Set("User-Agent", desktopUA)
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("qrbdusslogin: %w", err)
	}
	defer resp.Body.Close()
	// 从 Set-Cookie 拿 BDUSS/PTOKEN/STOKEN
	var r Result
	for _, sc := range resp.Header.Values("Set-Cookie") {
		if v := extractCookie(sc, "BDUSS"); v != "" {
			r.BDUSS = v
		}
		if v := extractCookie(sc, "PTOKEN"); v != "" {
			r.PTOKEN = v
		}
		if v := extractCookie(sc, "STOKEN"); v != "" {
			r.STOKEN = v
		}
	}
	// 兜底：从 cookie jar 拿（有些 Set-Cookie 走 jar）
	if r.BDUSS == "" {
		if v := c.jar.Get("BDUSS"); v != "" {
			r.BDUSS = v
		}
	}
	if r.BDUSS == "" {
		return nil, fmt.Errorf("登录失败：没拿到 BDUSS")
	}
	return &r, nil
}

// extractCookie 从 Set-Cookie 字符串提取指定 cookie 值。
func extractCookie(setCookie, name string) string {
	prefix := name + "="
	for _, part := range strings.Split(setCookie, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, prefix) {
			return part[len(prefix):]
		}
	}
	return ""
}

const desktopUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

func nowMs() string { return fmt.Sprintf("%d", time.Now().UnixMilli()) }

// randGID 生成随机 gid（百度 gid 格式：8-4-4-4-12）。
func randGID() string {
	b := make([]byte, 16)
	now := time.Now().UnixNano()
	for i := range b {
		b[i] = byte(now >> (uint(i%8) * 8))
	}
	return fmt.Sprintf("%02X%02X%02X%02X-%02X%02X-%02X%02X-%02X%02X-%02X%02X%02X%02X%02X%02X",
		b[0], b[1], b[2], b[3], b[4], b[5], b[6], b[7], b[8], b[9], b[10], b[11], b[12], b[13], b[14], b[15])
}

// cookieJar 极简 cookie jar（记 name→value，扫码登录够用）。
type cookieJar struct {
	m map[string]string
}

func newCookieJar() *cookieJar { return &cookieJar{m: map[string]string{}} }
func (j *cookieJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	for _, c := range cookies {
		j.m[c.Name] = c.Value
	}
}
func (j *cookieJar) Cookies(u *url.URL) []*http.Cookie {
	var out []*http.Cookie
	for k, v := range j.m {
		out = append(out, &http.Cookie{Name: k, Value: v})
	}
	return out
}
func (j *cookieJar) Get(name string) string { return j.m[name] }