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
	fmt.Printf("含 data-log=%d | \"mu\":=%d | baidu.com/link=%d | 真实站外链(youku/vod/tvplay)=%d\n",
		strings.Count(s, "data-log"), strings.Count(s, "\"mu\":"), strings.Count(s, "baidu.com/link"),
		strings.Count(s, "youku")+strings.Count(s, "vod/")+strings.Count(s, "tvplay"))
	// 找 mu（data-log 的 mu）
	n := 0
	for i := 0; i < len(s) && n < 6; i++ {
		if idx := strings.Index(s[i:], "\"mu\":\""); idx >= 0 {
			seg := s[i+idx+6 : i+idx+min(120, len(s)-i-idx-6)]
			mu := strings.SplitN(seg, "\"", 2)[0]
			if mu != "" {
				fmt.Printf("  mu: %s\n", mu[:min(55, len(mu))])
				n++
			}
			i += idx + 10
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
