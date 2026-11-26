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
	for i := 1; i <= 5; i++ {
		u := "https://www.baidu.com/s?wd=" + url.QueryEscape("我的山与海 免费在线播放") + "&rn=20"
		req, _ := http.NewRequest("GET", u, nil)
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/120.0.0.0 Safari/537.36")
		req.Header.Set("Referer", "https://www.baidu.com/")
		req.Header.Set("Cookie", strings.TrimSpace(string(cookie)))
		resp, err := c.Do(req)
		if err != nil {
			fmt.Printf("  [%d] 请求失败: %v\n", i, err)
			time.Sleep(2 * time.Second)
			continue
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
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
		nURL := 0
		zcount := 0
		for j := 0; j < len(s); j++ {
			if idx := strings.Index(s[j:], `"url":"http`); idx >= 0 {
				seg := s[j+idx+9:]
				v := strings.SplitN(seg, `"`, 2)[0]
				if !strings.Contains(v, "baidu") && !strings.Contains(v, "bdstatic") && !strings.Contains(v, "bcebos") {
					nURL++
					for _, k := range []string{"vodplay", "voddetail", "/vod/", "tvplay"} {
						if strings.Contains(v, k) {
							zcount++
						}
					}
				}
				j += idx + 10
			}
		}
		status := "正常"
		if strings.Contains(s, "安全验证") {
			status = "验证页"
		}
		fmt.Printf("  [%d] HTTP %d | %d B | %s | 真实URL %d | 资源站 %d\n", i, resp.StatusCode, len(s), status, nURL, zcount)
		time.Sleep(2 * time.Second)
	}
}
