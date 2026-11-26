//go:build mygo

// mycast 的 mygo 渲染路（学 mymusic 的 web_main.go）：
// mygo 壳 + mycast 的 HTTP server（server.New），UI 走 mygo WebView，
// 用 mygo 的 CookiesModule（含 httpOnly）做登录/匿名 cookie 固化。
// 编译：CGO_ENABLED=0 go build -tags mygo -o mycast-mygo ./cmd/mycast-mygo
package main

import (
	"log"

	"github.com/egoist/mygo"

	"mycast/internal/server"
)

func main() {
	// ① mycast 的 HTTP server（跟纯 HTTP 路同一个）
	inst := server.New(server.Options{})
	defer inst.Stop()

	// ② mycast 的 http.Handler 接到 mygo 的自定义 scheme（app://localhost）
	if err := mygo.Protocol.Handle("app", inst.Handler); err != nil {
		log.Fatal("mygo Protocol.Handle: " + err.Error())
	}

	// ③ 起 mygo 主窗口（mycast UI）
	mygo.App.WhenReady(func() {
		mygo.NewWindow(mygo.WindowOptions{
			Title:     "MyCast",
			Width:     960,
			Height:    720,
			MinWidth:  720,
			MinHeight: 480,
			StateKey:  "main",
			URL:       "app://localhost/",
		})
	})

	// ④ mygo cookie 固化（搜索引擎）：mygo WebView 加载引擎页 → mygo.Cookies.List → 固化到 CookieStore
	bindMygoCookies(inst, map[string]string{
		"baidu": "https://www.baidu.com/",
		"so":    "https://www.so.com/",
		"sogou": "https://www.sogou.com/",
		"bing":  "https://www.bing.com/",
	})

	// ⑤ 跑 mygo（事件循环 + WebView）
	if err := mygo.App.Run(); err != nil {
		log.Fatal("mygo.App.Run: " + err.Error())
	}
}
