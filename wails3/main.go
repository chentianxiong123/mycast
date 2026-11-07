// MyCast 桌面壳（wails3 v3）:单窗口，直载后端真实 HTTP。
//
//	照搬 mymusic 已验证模式（单窗口，无渲染子窗口）：
//	  后端 8080 → wails3 WebviewWindowOptions.URL 直载 → 原生窗口（非浏览器）
//	  搜索引擎走后端 cookie 固化 fetch（/api/search），不另起窗口。
//
//	平台差异:桌面=本壳（wails3 原生窗口）；安卓=自写壳（android/，纯 aapt2）。
package main

import (
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"mycast/internal/server"
)

func backendPort() string {
	if p := os.Getenv("HC_PORT"); p != "" {
		return p
	}
	return server.Port
}

func main() {
	inst := server.New(server.Options{})
	defer inst.Stop()

	port := backendPort()
	// 后端同步 listen（建窗口前必须 listen 完成，否则 WebView 发起 GET /ui 时连接被拒 → 黑屏）。
	// serve 放 goroutine（阻塞），listen 同步返回才建窗口。
	addr := "0.0.0.0:" + port
	ln, err := net.Listen("tcp4", addr)
	if err != nil {
		log.Fatalf("[mycast] listen %s: %v", addr, err)
	}
	log.Printf("[mycast] http://127.0.0.1:%s/ui", port)
	go func() {
		if err := (&http.Server{Handler: inst.Handler, ReadHeaderTimeout: 10 * time.Second}).Serve(ln); err != nil {
			log.Printf("[mycast] serve: %v", err)
		}
	}()

	app := application.New(application.Options{
		Name:        "MyCast",
		Description: "家庭影院投屏中继（B站/盗版站 → 电视，DLNA）",
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "MyCast 家庭影院",
		Width:            1280,
		Height:           800,
		MinWidth:         920,
		MinHeight:        620,
		BackgroundColour: application.NewRGB(17, 17, 17),
		URL:              "http://127.0.0.1:" + port + "/ui",
	})

	if err := app.Run(); err != nil {
		os.Exit(1)
	}
}
