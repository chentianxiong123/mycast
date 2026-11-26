//go:build mygo

// 拿 mygo 抓的匿名 cookie（mygo.Cookies.List）去搜 baidu（注入匿名 cookie，看通不通）。
package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/egoist/mygo"
)

func main() {
	mygo.App.WhenReady(func() {
		mygo.NewWindow(mygo.WindowOptions{
			Title:    "cookie search",
			Width:    800,
			Height:   600,
			StateKey: "search",
			URL:      "https://www.baidu.com/",
		})
		ticker := time.NewTicker(8 * time.Second)
		go func() {
			<-ticker.C
			mygo.RunOnMain(func() {
				// ① 拿 mygo 的匿名 cookie（7 条 baidu）
				cookies, err := mygo.Cookies.List(mygo.CookieFilter{URL: "https://www.baidu.com/"})
				if err != nil {
					fmt.Println("mygo.Cookies.List err:", err)
					mygo.App.Quit()
					return
				}
				parts := make([]string, 0, len(cookies))
				for _, c := range cookies {
					parts = append(parts, c.Name+"="+c.Value)
				}
				cookieStr := strings.Join(parts, "; ")
				fmt.Printf("mygo 匿名 cookie（%d 条）: %s\n", len(cookies), cookieStr[:min(80, len(cookieStr))])

				// ② 拿这 7 条匿名 cookie 搜 baidu（注入）
				u := "https://www.baidu.com/s?wd=" + url.QueryEscape("我的山与海") + "&rn=20"
				req, _ := http.NewRequest("GET", u, nil)
				req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
				req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
				req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
				req.Header.Set("Referer", "https://www.baidu.com/")
				if cookieStr != "" {
					req.Header.Set("Cookie", cookieStr)
				}
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					fmt.Println("搜 baidu err:", err)
					mygo.App.Quit()
					return
				}
				defer resp.Body.Close()
				body, _ := io.ReadAll(resp.Body)
				s := string(body)
				fmt.Printf("\n带 mygo 匿名 cookie 搜 baidu: HTTP %d | %d B | 验证页=%v\n", resp.StatusCode, len(s), strings.Contains(s, "安全验证"))
				links := strings.Count(s, "baidu.com/link?url=")
				fmt.Printf("  baidu.com/link 结果: %d 个（匿名 cookie 搜索通不通）\n", links)
				mygo.App.Quit()
			})
		}()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
