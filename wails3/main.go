// MyCast 桌面壳（wails3 v3）：原生窗口，直载后端真实 HTTP（非 asset 桥）。
//
//	架构（照搬 mymusic 已验证模式）：
//	  - 后端 server.New() 监听 tcp4 0.0.0.0:8080（IPv4 明确：局域网电视 + 窗口 127.0.0.1 都能连）
//	  - wails3 WebviewWindowOptions.URL = http://127.0.0.1:8080/ui 直载真实后端（同源，非 asset 桥）
//	  - 不弹系统浏览器（双击即出原生窗口，像原生应用）
//
//	平台差异：桌面=本壳（wails3 原生窗口）；安卓=自写壳（android/，纯 aapt2，已在真机跑通）。
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

// backendPort 后端 HTTP 端口（HC_PORT 覆盖）；与 server.Port 默认 8080 一致
func backendPort() string {
	if p := os.Getenv("HC_PORT"); p != "" {
		return p
	}
	return server.Port
}

func main() {
	inst := server.New(server.Options{})
	defer inst.Stop()

	// 后端监听 tcp4 0.0.0.0:8080（IPv4 明确，见 cmd/mycast/main.go 同因）
	port := backendPort()
	go func() {
		addr := "0.0.0.0:" + port
		ln, err := net.Listen("tcp4", addr)
		if err != nil {
			log.Printf("[mycast] listen %s: %v", addr, err)
			return
		}
		log.Printf("[mycast] http://127.0.0.1:%s/ui", port)
		srv := &http.Server{Handler: inst.Handler, ReadHeaderTimeout: 10 * time.Second}
		if err := srv.Serve(ln); err != nil {
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
		JS:               "window.hcEnv='desktop';",
	})

	if err := app.Run(); err != nil {
		os.Exit(1)
	}
}
