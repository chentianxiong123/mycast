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
	    if(window.__seDone) return; // 防重入：同一页面只回传一次（百度页刷新会重新执行注入 JS）
	    window.__seDone = true;
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
	win := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "search-render",
		Width:            1,
		Height:           1,
		BackgroundColour: application.NewRGB(17, 17, 17),
		URL:              targetURL,
		JS:               js,
		Hidden:           true, // 用户无感
	})
	// 修 bug：抓取回传后关窗。否则子窗口不死，百度页周期刷新→重新执行注入 JS→每 22 秒回传一次（结果=0）。
	// waitAndGrab 上限 15s，16s 后关窗兜底（覆盖抓取周期）。
	go func() {
		time.Sleep(16 * time.Second)
		win.Close()
	}()
}

// openInjectPage 用可见 WebView 打开目标站 + 直接注入 JS（去广告/归一化/集数提取）。
// 不做子窗口回传、不用 node/curl：页面内 JS 直接清洗面板。
// 注入用 WebviewWindowOptions.JS（页面加载自动注入，像 searchRenderPage 验证过的机制），
// 不用 win.ExecJS（固定 sleep 后手动注入，页面加载中易静默失败）。
func openInjectPage(app *application.App, targetURL string) {
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "注入测试",
		Width:            1000,
		Height:           800,
		BackgroundColour: application.NewRGB(17, 17, 17),
		URL:              targetURL,
		JS:               injectJS, // 页面加载自动注入（wails3 在 DOM 就绪后执行）
		Hidden:           false,     // 可见：用户能看真页 + 注入后集数面板
	})
}

// injectJS 方向C：JS 版去广告 + 归一化 + 集数提取 + 显示面板（对应 sniff.go 的 Go 逻辑）。
// 页面内直接做：删广告节点/脚本/弹窗 → 提集数（第N集，排除上一/下一集）→ 排序归一化 → 浮层面板。
const injectJS = `(function(){
  try{
    // ── 提取集数（页面内 JS，非 HTTP）──
    var eps=[],seen={};
    document.querySelectorAll('a').forEach(function(a){
      var href=(a.getAttribute('href')||'').trim(), text=(a.textContent||'').trim();
      if(!href||href.indexOf('javascript:')===0||href==='#')return;
      var clean=text.replace(/[\uE000-\uF8FF]/g,'');
      if(/^(上一|下一|上|下|前一|后一)[集期话]/.test(clean))return;
      if(/第\s*\d+\s*[集期话]/.test(text)){
        var full; try{full=new URL(href,location.href).href;}catch(e){return;}
        if(!seen[full]){seen[full]=1;eps.push({t:text,u:full});}
      }
    });
    eps.sort(function(a,b){return (parseInt((a.t.match(/\d+/)||[0])[0])||0)-(parseInt((b.t.match(/\d+/)||[0])[0])||0);});
    // ── 屏蔽站点：body 整个替换成我们的格式（标题 + 播放器 + 集数卡片）──
    var title=(document.title||'').replace(/_.*/,'').replace(/在线观看.*/,'').replace(/ -.*/,'').trim()||'剧集';
    document.body.innerHTML=''
      +'<div style="max-width:960px;margin:0 auto;padding:18px;font-family:system-ui,sans-serif;color:#e4e9f2;background:#0f1115;min-height:100vh">'
      +'<h2 style="margin:0 0 14px;font-size:20px">'+title+' <span style="color:#ff4d6d;font-size:13px;font-weight:400">集数 '+eps.length+'</span></h2>'
      +'<video id="mc-video" controls playsinline style="width:100%;background:#000;border-radius:10px;margin-bottom:16px;aspect-ratio:16/9"></video>'
      +'<div style="color:#8a93a6;font-size:13px;margin-bottom:10px">点集数 → 嗅探拿流 → 本播放器播放</div>'
      +'<div id="mc-grid" style="display:flex;flex-wrap:wrap;gap:8px;max-height:56vh;overflow-y:auto"></div>'
      +'</div>';
    var grid=document.getElementById('mc-grid');
    if(!eps.length){ grid.innerHTML='<div style="color:#ff4d6d">未识别到集数（可能非影视页/注入时机）</div>'; return; }
    eps.forEach(function(e){
      var c=document.createElement('button');c.textContent=e.t;
      c.style.cssText='padding:8px 12px;border-radius:6px;background:#1a1e2a;color:#e4e9f2;border:1px solid #222838;cursor:pointer;font-size:13px';
      c.onclick=function(){
        var v=document.getElementById('mc-video');
        fetch('http://127.0.0.1:18080/api/cast?url='+encodeURIComponent(e.u)+'&title='+encodeURIComponent(e.t),{mode:'cors'})
          .then(function(r){return r.json();})
          .then(function(d){
            if(d.ok){
              if(window.Hls&&Hls.isSupported()&&/m3u8/.test(d.url)){ if(v.__hls)v.__hls.destroy(); v.__hls=new Hls(); v.__hls.loadSource(d.url); v.__hls.attachMedia(v); }
              else { v.src=d.url; }
              v.play();
            } else alert(d.msg||'拿流失败');
          }).catch(function(){alert('拿流失败');});
      };
      grid.appendChild(c);
    });
    console.log('[mc-inject] 转化完成: '+eps.length+' 集');
  }catch(e){console.log('[mc-inject] 错误: '+e.message);}
})();`

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
	  openInject: function(url){ fetch('/api/page/inject-launch?url='+encodeURIComponent(url)); },
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
	// 方向C：可见 WebView 打开目标站 + 直接注入 JS（去广告/归一化/集数提取），不回传。
	inst.PageInject = func(targetURL string) {
		log.Printf("[main] PageInject 触发: %s", targetURL)
		openInjectPage(app, targetURL)
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
