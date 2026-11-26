//go:build mygo

// mycast 的 mygo 渲染路（学 mymusic 的 web_main.go）：
// mygo 壳 + mycast 的 HTTP server（server.New），UI 走 mygo WebView。
// 用 mygo.Cookies（v0.10.0，顶层 CookiesModule，含 httpOnly）做登录/匿名 cookie 固化。
// 面向手机用户：mygo WebView 渲染 baidu（登录/匿名）→ 系统 WebView 存 cookie（含 BDUSS）→
// mygo.Cookies.List 导出 → 固化 cookies/baidu.json → 搜 baidu 带 BDUSS 过验证 → 资源站 JSON 直解。
// 桌面（Linux/Windows/Mac）：mygo.Cookies（WebKitGTK/WebView2）能拿 httpOnly，已通。
// 安卓：mygo WebView 渲染存 cookie（系统 WebView），mygo.Cookies 安卓实现后导 BDUSS。
// 编译：CGO_ENABLED=0 go build -tags mygo -o mycast-mygo ./cmd/mycast-mygo

package main

import (
	"log"

	mygo "github.com/egoist/mygo"

	"mycast/internal/server"
)

// mainWindow 主窗口（cookie.go 用 mainWindow.Page().LoadURL 渲染 baidu 存 cookie）
var mainWindow *mygo.Window

func main() {
	// ① mycast 的 HTTP server（跟纯 HTTP 路同一个）
	inst := server.New(server.Options{})
	defer inst.Stop()

	// ② mycast 的 http.Handler 接到 mygo 的自定义 scheme（app://localhost）
	if err := mygo.Protocol.Handle("app", inst.Handler); err != nil {
		log.Fatal("mygo Protocol.Handle: " + err.Error())
	}

	// ③ 起 mygo 主窗口（mycast UI），存 mainWindow（cookie.go 用）
	mygo.App.WhenReady(func() {
		mainWindow = mygo.NewWindow(mygo.WindowOptions{
			Title:  "mycast - 投屏中继",
			Width:  1000,
			Height: 700,
			URL:    "app://localhost/",
		})
	})
}
