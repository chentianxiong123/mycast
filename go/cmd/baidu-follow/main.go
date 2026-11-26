package main

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func main() {
	cookie, _ := os.ReadFile("/tmp/baidu-cookie-bdu.txt") // 77 条含 BDUSS
	c := &http.Client{Timeout: 10 * time.Second}
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }
	// 搜"我的山与海 免费在线播放"的 baidu.com/link（用真 cookie）
	u := "https://www.baidu.com/s?wd=" + url.QueryEscape("我的山与海 免费在线播放") + "&rn=20"
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://www.baidu.com/")
	if len(cookie) > 0 {
		req.Header.Set("Cookie", strings.TrimSpace(string(cookie)))
	}
	resp, err := c.Do(req)
	if err != nil {
		fmt.Println("搜索 err:", err)
		return
	}
	body, _ := readAll(resp)
	s := string(body)
	fmt.Printf("搜索（真 cookie 77 条含 BDUSS）: HTTP %d | %d B | 验证=%v\n", resp.StatusCode, len(s), strings.Contains(s, "安全验证"))
	// 找 baidu.com/link
	links := 0
	for i := 0; i < len(s) && links < 3; i++ {
		if idx := strings.Index(s[i:], "baidu.com/link?url="); idx >= 0 {
			links++
			// 提取完整 link
			start := i + idx
			seg := s[start : start+200]
			end := strings.Index(seg, `"`)
			if end < 0 {
				end = min(100, len(seg))
			}
			// 直接 follow（带真 cookie）
			freq, _ := http.NewRequest("GET", "http://www.baidu.com/"+seg[9:end], nil)
			freq.Header.Set("User-Agent", req.Header.Get("User-Agent"))
			freq.Header.Set("Cookie", strings.TrimSpace(string(cookie)))
			fresp, ferr := c.Do(freq)
			if ferr != nil {
				fmt.Printf("  follow err: %v\n", ferr)
			} else {
				fmt.Printf("  follow: HTTP %d | Location=%q\n", fresp.StatusCode, fresp.Header.Get("Location"))
				fresp.Body.Close()
			}
			break
		} else {
			i++
		}
	}
}
func readAll(resp *http.Response) ([]byte, error) {
	buf := make([]byte, 0)
	tmp := make([]byte, 4096)
	for {
		n, e := resp.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if e != nil {
			break
		}
	}
	return buf, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
