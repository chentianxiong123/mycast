package main

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/andybalholm/brotli"
)

func main() {
	cookie, _ := os.ReadFile("/tmp/baidu-cookie-bdu.txt")
	c := &http.Client{Timeout: 15 * time.Second}
	u := "https://www.baidu.com/s?wd=" + url.QueryEscape("我的山与海 免费在线播放") + "&rn=20"
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://www.baidu.com/")
	req.Header.Set("Cookie", strings.TrimSpace(string(cookie)))
	resp, _ := c.Do(req)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var s string
	switch resp.Header.Get("Content-Encoding") {
	case "gzip":
		gz, _ := gzip.NewReader(bytes.NewReader(raw))
		b2, _ := io.ReadAll(gz)
		s = string(b2)
	case "br":
		out := bytes.NewBuffer(nil)
		io.Copy(out, brotli.NewReader(bytes.NewReader(raw)))
		s = out.String()
	default:
		s = string(raw)
	}
	fmt.Printf("搜索页 %d B\n", len(s))
	// 找所有 href（看直接真实 URL 还是 baidu.com/link）
	zyCount, linkCount := 0, 0
	for i := 0; i < len(s); i++ {
		if idx := strings.Index(s[i:], "href=\"http"); idx >= 0 {
			seg := s[i+idx+8 : i+idx+min(80, len(s)-i-idx)]
			hv := strings.SplitN(seg, "\"", 2)[0]
			if strings.Contains(hv, "baidu.com/link") {
				linkCount++
			} else if strings.Contains(hv, "youku") || strings.Contains(hv, "2sctc") || strings.Contains(hv, "zimg") || strings.Contains(hv, "vod") || strings.Contains(hv, "tvplay") || strings.Contains(hv, "play") {
				zyCount++
				if zyCount <= 8 {
					fmt.Printf("  资源站外链: %s\n", hv[:min(55, len(hv))])
				}
			}
			i += idx + 10
		}
	}
	fmt.Printf("\n直接资源站外链 %d 个 | baidu.com/link 跳转 %d 个\n", zyCount, linkCount)
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
