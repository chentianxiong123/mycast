//go:build mygo

// 验证 mygo.Cookies（加载 baidu 后 List 拿 cookie，含匿名 + httpOnly）。
// 不 Sleep 阻塞事件循环（mygo 要事件循环跑 WebView 才加载）。
package main

import (
	"fmt"
	"log"
	"time"

	"github.com/egoist/mygo"
)

func main() {
	mygo.App.WhenReady(func() {
		mygo.NewWindow(mygo.WindowOptions{
			Title:    "cookie test",
			Width:    800,
			Height:   600,
			StateKey: "cookie",
			URL:      "https://www.baidu.com/",
		})
		// 不 Sleep（阻塞事件循环），用 mygo.RunOnMain 定时（事件循环里）
		ticker := time.NewTicker(8 * time.Second)
		go func() {
			<-ticker.C
			mygo.RunOnMain(func() {
				cookies, err := mygo.Cookies.List(mygo.CookieFilter{URL: "https://www.baidu.com/"})
				if err != nil {
					fmt.Println("mygo.Cookies.List err:", err)
					return
				}
				anon, httpOnly := 0, 0
				for _, c := range cookies {
					if c.HTTPOnly {
						httpOnly++
					} else {
						anon++
					}
					fmt.Printf("  %s (%s) httpOnly=%v\n", c.Name, c.Domain, c.HTTPOnly)
				}
				fmt.Printf("\n★ mygo.Cookies.List: 总 %d 条 | 匿名 %d | httpOnly %d\n", len(cookies), anon, httpOnly)
				if httpOnly > 0 {
					fmt.Println("  能拿 httpOnly cookie（登录态如 BDUSS）")
				}
				mygo.App.Quit()
			})
		}()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
