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
	u := "https://www.baidu.com/s?wd=" + url.QueryEscape("我的山与海") + "&rn=20"
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
	fmt.Printf("搜索页（我的山与海）%d B\n", len(s))
	fmt.Printf("mu=:%d | data-log:%d | baidu.com/link:%d | \"mu\"::%d | data-mu:%d | data-loc:%d\n",
		strings.Count(s, "mu="), strings.Count(s, "data-log"), strings.Count(s, "baidu.com/link"),
		strings.Count(s, "\"mu\":"), strings.Count(s, "data-mu="), strings.Count(s, "data-loc"))
	// 找 mu 相关（data-mu / "mu" / data-loc）
	n := 0
	for i := 0; i < len(s) && n < 6; i++ {
		for _, pat := range []string{"data-mu=\"", "data-loc=\"", "\"mu\":"} {
			if idx := strings.Index(s[i:], pat); idx >= 0 {
				seg := s[i+idx+len(pat):]
				v := strings.SplitN(seg, "\"", 2)[0]
				if v != "" && !strings.Contains(v, "baidu.com/link") && n < 6 {
					fmt.Printf("  %s: %s\n", pat, v[:min(55, len(v))])
					n++
				}
				i += idx + len(pat)
				break
			}
		}
	}
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
