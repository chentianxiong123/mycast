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
	"strings"
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
		Hidden:           true, // 用户无感：不弹新窗口，结果在主窗口 iframe 显示
	})
	go func() {
		// 等页面渲染（Cloudflare 挑战/JS 反爬通常要几秒；轮询等 HTML 里有内容）。
		// 百度 link?url= 是 302 跳转：等跳转到目标站（title 不再是百度安全验证）+ 页面有内容。
		time.Sleep(6 * time.Second)
		js := fmt.Sprintf(`(function(){
		  try{
		    var html = document.documentElement.outerHTML;
		    // form POST 回传（绕开 XHR 跨源限制；html 是整页几十KB，用 POST body 而不是 URL）
		    var f=document.createElement('form');
		    f.method='POST'; f.action='http://127.0.0.1:%s%s';
		    f.style.display='none';
		    var i1=document.createElement('input'); i1.name='html'; i1.value=html; f.appendChild(i1);
		    var i2=document.createElement('input'); i2.name='url'; i2.value=%q; f.appendChild(i2);
		    document.body.appendChild(f); f.submit();
		  }catch(e){}
		})();`, port, apiPath, targetURL)
		win.ExecJS(js)
		// 再等回传完成，关窗
		time.Sleep(3 * time.Second)
		win.Close()
	}()
}

// searchRenderPage 用 Hidden 子 WebView 窗口渲染搜索引擎页（用户无感），
// 页面 onload 后 JS 抓结构化结果（标题+URL）→ 导航回传 /api/search/render?engine=x&data=JSON。
// 绕开：Go→窗口死结（JS 自回传）+ mixed content（导航非 XHR）+ bindings（无 Go 桥）。
// 子窗口 Hidden:true（wv3hide 验证：能渲染 + 抓 + 回传，零崩溃）。
func searchRenderPage(app *application.App, targetURL string) {
	port := backendPort()
	engine := ""
	if strings.Contains(targetURL, "so.com") {
		engine = "so"
	} else if strings.Contains(targetURL, "baidu") {
		engine = "baidu"
	} else if strings.Contains(targetURL, "bing") {
		engine = "bing"
	} else if strings.Contains(targetURL, "sogou") {
		engine = "sogou"
	}
	// JS：轮询等结果 DOM（最多 15s）→ 抓前 15 条（标题+URL）→ 导航回传（绕开 mixed content）
	js := fmt.Sprintf(`(function(){
	  var waited=0;
	  function hasRes(){
	    var sels=['h3 a','h3','.result a','[class*="title"] a','.vrwrap a','li a'];
	    for(var si=0;si<sels.length;si++){ if(document.querySelectorAll(sels[si]).length>0) return true; }
	    return false;
	  }
	  function grab(){
	    try{
	      var items=[]; var seen={};
	      var sels=['h3 a','h3','.result a','[class*="title"] a','.vrwrap a','li a','a'];
	      for(var si=0; si<sels.length && items.length<15; si++){
	        var nodes=document.querySelectorAll(sels[si]);
	        for(var i=0;i<nodes.length && items.length<15;i++){
	          var a=nodes[i]; var href=(a.href||'').trim(); var t=(a.textContent||'').trim();
	          if(href.indexOf('http')===0 && t.length>4 && !seen[href]){ seen[href]=1; items.push({title:t,url:href}); }
	        }
	      }
	      location.href='http://127.0.0.1:%s/api/search/render?engine=%s&data='+encodeURIComponent(JSON.stringify(items));
	    }catch(e){
	      location.href='http://127.0.0.1:%s/api/search/render?engine=%s&data='+encodeURIComponent(JSON.stringify([]));
	    }
	  }
	  function waitAndGrab(){
	    if(document.readyState==='complete' && hasRes()){ grab(); return; }
	    if(waited>=15000){ grab(); return; } // 超时也抓（可能拿到部分/空）
	    waited+=1000; setTimeout(waitAndGrab, 1000);
	  }
	  if(document.readyState==='complete') setTimeout(waitAndGrab, 1000);
	  else window.addEventListener('load', function(){ setTimeout(waitAndGrab, 1000); });
	})();`, port, engine, port, engine)
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "search-render",
		Width:            1,
		Height:           1,
		BackgroundColour: application.NewRGB(17, 17, 17),
		URL:              targetURL,
		JS:               js,
		Hidden:           true, // 用户无感
	})
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
	  pageRender: function(url){ fetch('/api/page/render-launch?url='+encodeURIComponent(url)); },
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
	// 纯页面渲染（点击搜索结果 → 真实页，不归一化/不去广告）：POST /api/page/render
	inst.PageRender = func(targetURL string) {
		log.Printf("[main] PageRender 触发: %s", targetURL)
		renderPage(app, targetURL, "/api/page/render")
	}
	// 搜索渲染：Hidden 子 WebView 窗口渲染引擎页 + JS 抓结构化 + 导航回传（用户无感）。
	inst.SearchWebviewRender = func(targetURL string) {
		log.Printf("[main] SearchWebviewRender 触发: %s", targetURL)
		searchRenderPage(app, targetURL)
	}
	log.Printf("[main] SearchWebviewRender 已注入 (wails3 壳)")

	if err := app.Run(); err != nil {
		os.Exit(1)
	}
}
