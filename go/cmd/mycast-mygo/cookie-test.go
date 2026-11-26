//go:build mygo

// 验证 mygo 的 CookiesModule（List 拿含 httpOnly 的 cookie，如 BDUSS）。
package main

import (
	"fmt"

	"github.com/egoist/mygo"
)

func testCookie() {
	mygo.App.WhenReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{Title: "t", Width: 600, Height: 400, URL: "https://www.baidu.com/"})
		_ = win
		// mygo 的 CookiesModule（含 httpOnly）
		cookies, err := mygo.App.Cookies().List(mygo.CookieFilter{URL: "https://www.baidu.com/"})
		if err != nil {
			fmt.Println("cookie list err:", err)
			return
		}
		fmt.Printf("mygo cookie: %d 条（含 httpOnly）\n", len(cookies))
		for _, c := range cookies {
			only := ""
			if c.HTTPOnly {
				only = " ★httpOnly"
			}
			fmt.Printf("  %s (%s)%s\n", c.Name, c.Domain, only)
		}
	})
}
