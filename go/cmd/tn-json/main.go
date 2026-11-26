package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"github.com/andybalholm/brotli"
)

func main() {
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Timeout: 15 * time.Second, Jar: jar}
	// SearXNG 正解：image.baidu.com cookie 预热（过 wappass 验证）
	c.Get("https://image.baidu.com/")
	time.Sleep(500 * time.Millisecond)
	// tn=json
	u := "https://www.baidu.com/s?wd=" + url.QueryEscape("我的山与海 免费在线播放") + "&rn=10&pn=0&tn=json"
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json, text/javascript, */*; q=0.01")
	req.Header.Set("Referer", "https://www.baidu.com/")
	resp, err := c.Do(req)
	if err != nil {
		fmt.Println("err:", err)
		return
	}
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
	t := strings.TrimSpace(s)
	fmt.Printf("tn=json + image.baidu.com 预热: HTTP %d | %d B | 验证=%v\n", resp.StatusCode, len(s), strings.Contains(s, "安全验证"))
	if strings.HasPrefix(t, "{") {
		var parsed struct {
			Data struct {
				AntiFlag int `json:"antiFlag"`
				Feed     struct {
					Entry []struct {
						Title string `json:"title"`
						URL   string `json:"url"`
						Abs   string `json:"abs"`
					} `json:"entry"`
				} `json:"feed"`
			} `json:"data"`
		}
		if jerr := json.Unmarshal([]byte(t), &parsed); jerr == nil {
			fmt.Printf("  tn=json 通！antiFlag=%d | entry %d 条\n", parsed.Data.AntiFlag, len(parsed.Data.Feed.Entry))
			for i, e := range parsed.Data.Feed.Entry[:min(8, len(parsed.Data.Feed.Entry))] {
				kind := "其他"
				for _, k := range []string{"youku", "iqiyi", "zimg", "2sctc", "vod", "tvplay"} {
					if strings.Contains(e.URL, k) {
						kind = k
					}
				}
				fmt.Printf("    [%d] (%s) %s → %s\n", i, kind, e.Title[:min(30, len(e.Title))], e.URL[:min(50, len(e.URL))])
			}
		}
	} else {
		fmt.Printf("  非 JSON，前 80: %s\n", t[:min(80, len(t))])
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
