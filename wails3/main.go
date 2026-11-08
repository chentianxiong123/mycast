// MyCast 桌面壳（wails3 v3）：原生窗口，直载后端真实 HTTP（非 asset 桥）。
//
//	架构（照搬 mymusic 已验证模式）：
//	  - 后端 server.New() 监听 tcp4 0.0.0.0:18080（IPv4 明确：局域网电视 + 窗口 127.0.0.1 都能连）
//	  - wails3 WebviewWindowOptions.URL = http://127.0.0.1:18080/ui 直载真实后端（同源，非 asset 桥）
//	  - 不弹系统浏览器（双击即出原生窗口，像原生应用）
//
//	平台差异：桌面=本壳（wails3 原生窗口）；安卓=自写壳（android/，纯 aapt2，已在真机跑通）。
package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"mycast/internal/server"
)

// backendPort 后端 HTTP 端口（HC_PORT 覆盖）；与 server.Port 默认 18080 一致
func backendPort() string {
	if p := os.Getenv("HC_PORT"); p != "" {
		return p
	}
	return server.Port
}

// renderPage 用一个后台子 WebView 窗口渲染盗版站详情页（Cloudflare/JS 反爬），
// 拿 document.documentElement.outerHTML POST 回后端 /api/sniff/render，然后关掉子窗口。
// 主窗口 UI 不导航、不动（单窗口语义保留：用户看不到渲染过程，只看到结果）。
func renderPage(app *application.App, targetURL, apiPath string) {
	port := backendPort()
	win := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "加载中…",
		Width:            900,
		Height:           700,
		BackgroundColour: application.NewRGB(17, 17, 17),
		URL:              targetURL,
	})
	go func() {
		// 等页面渲染（Cloudflare 挑战/JS 反爬通常要几秒；轮询等 HTML 里有内容）
		time.Sleep(6 * time.Second)
		js := fmt.Sprintf(`(function(){
		  try{
		    var html = document.documentElement.outerHTML;
		    var xhr = new XMLHttpRequest();
		    xhr.open('POST', 'http://127.0.0.1:%s%s?url=' + encodeURIComponent('%s'), true);
		    xhr.setRequestHeader('Content-Type','text/plain');
		    xhr.send(html);
		  }catch(e){}
		})();`, port, apiPath, targetURL)
		win.ExecJS(js)
		// 再等回传完成，关窗
		time.Sleep(3 * time.Second)
		win.Close()
	}()
}

func main() {

	// 后端监听端口（HC_PORT 覆盖）——server.Options.Port 也要传，
	// 否则 BaseURL 用默认 18080 拼，页面显示的局域网地址与实际端口不一致。
	port := backendPort()
	inst := server.New(server.Options{Port: port})
	defer inst.Stop()

	// 后端监听 tcp4 0.0.0.0:18080（IPv4 明确，见 cmd/mycast/main.go 同因）
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

	// 桌面 WebView 兜底：need_browser（Cloudflare/JS 反爬）时开后端子 WebView 渲染详情页。
	// JS 桥：window.hc.sniffRender(url) / window.hc.searchRender(url) → fetch 本地后端触发。
	bootJS := `
	window.hc = {
	  sniffRender: function(url){ fetch('/api/sniff/render-launch?url='+encodeURIComponent(url)); },
	  searchRender: function(url){ fetch('/api/search/render-launch?url='+encodeURIComponent(url)); },
	  searchEngine: function(engine, url){ window.__se_engine=engine; fetch('/api/search/render-launch?url='+encodeURIComponent(url)); }
	};`

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "MyCast 家庭影院",
		Width:            1280,
		Height:           800,
		MinWidth:         920,
		MinHeight:        620,
		BackgroundColour: application.NewRGB(17, 17, 17),
		URL:              "http://127.0.0.1:" + port + "/ui",
		JS:               bootJS,
	})

	// 注册渲染兜底：后端 /api/sniff/render-launch 触发开子窗口。
	inst.WebviewRender = func(targetURL string) {
		renderPage(app, targetURL, "/api/sniff/render")
	}

	if err := app.Run(); err != nil {
		os.Exit(1)
	}
}
