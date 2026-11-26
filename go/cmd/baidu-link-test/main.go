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
	cookie, _ := os.ReadFile("/tmp/baidu-cookie-bdu.txt")
	c := &http.Client{Timeout: 10 * time.Second}
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }

	// 从搜索页拿一个真实的 baidu.com/link
	sc := &http.Client{Timeout: 15 * time.Second}
	su := "https://www.baidu.com/s?wd=" + url.QueryEscape("我的山与海 免费在线播放") + "&rn=20"
	sreq, _ := http.NewRequest("GET", su, nil)
	sreq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/120.0.0.0 Safari/537.36")
	sreq.Header.Set("Referer", "https://www.baidu.com/")
	sreq.Header.Set("Cookie", strings.TrimSpace(string(cookie)))
	sresp, _ := sc.Do(sreq)
	sbody, _ := readAll(sresp)
	sresp.Body.Close()
	s := string(sbody)
	idx := strings.Index(s, "baidu.com/link?url=")
	if idx < 0 {
		fmt.Println("没找到 baidu.com/link")
		return
	}
	linkStart := strings.LastIndex(s[:idx], "href=\"") + len("href=\"")
	linkEnd := strings.Index(s[linkStart:], "\"")
	link := s[linkStart : linkStart+linkEnd]
	fmt.Printf("第一个 baidu.com/link: %s\n", link[:80])

	// 试几种 follow 方式
	ways := []string{
		link,
		"https://www.baidu.com/link?" + strings.SplitN(link, "?", 2)[1],
		"http://www.baidu.com/link?" + strings.SplitN(link, "?", 2)[1],
	}
	for i, w := range ways {
		req, _ := http.NewRequest("GET", w, nil)
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/120.0.0.0 Safari/537.36")
		req.Header.Set("Referer", "https://www.baidu.com/s?wd="+url.QueryEscape("我的山与海 免费在线播放"))
		req.Header.Set("Cookie", strings.TrimSpace(string(cookie)))
		resp, err := c.Do(req)
		if err != nil {
			fmt.Printf("  [%d] err: %v\n", i, err)
			continue
		}
		loc := resp.Header.Get("Location")
		resp.Body.Close()
		fmt.Printf("  [%d] %s\n      HTTP %d | Location=%q\n", i, w[:45], resp.StatusCode, loc[:min(55, len(loc))])
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
