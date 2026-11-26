//go:build mygo

// mycast-mygo 的 mygo cookie 固化：mygo WebView 加载引擎页 → mygo.Cookies.List 拿匿名/登录 cookie → 固化到 CookieStore（cookies/<engine>.json）→ 搜索注入。
package main

import (
	"time"

	"github.com/egoist/mygo"

	"mycast/internal/server"
)

// bindMygoCookies 把 mygo WebView 的 cookie（含 httpOnly）固化到 mycast 的 CookieStore（搜索引擎用）。
// engines：要抓 cookie 的引擎 → 加载 URL（激活 cookie 域）。
func bindMygoCookies(inst *server.Instance, engines map[string]string) {
	mygo.App.WhenReady(func() {
		// 每个引擎开一个 mygo 窗口加载（激活该引擎域 cookie）
		for eng, u := range engines {
			mygo.NewWindow(mygo.WindowOptions{
				Title:    "cookie " + eng,
				Width:    600,
				Height:   400,
				StateKey: "cookie-" + eng,
				URL:      u,
			})
		}
		// 等 cookie 进 jar（不阻塞事件循环，用 RunOnMain 定时），再 mygo.Cookies.List → 固化
		go func() {
			<-time.After(10 * time.Second)
			mygo.RunOnMain(func() {
				for eng, u := range engines {
					cookies, err := mygo.Cookies.List(mygo.CookieFilter{URL: u})
					if err != nil {
						continue
					}
					entries := make([]server.CookieEntry, 0, len(cookies))
					for _, c := range cookies {
						entries = append(entries, server.CookieEntry{Name: c.Name, Value: c.Value, Domain: c.Domain})
					}
					_ = inst.Cookies.Save(eng, entries) // 固化到 cookies/<eng>.json
				}
			})
		}()
	})
}
