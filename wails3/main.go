// MyCast 桌面壳（wails3 v3）:原生窗口，直载后端真实 HTTP（非 asset 桥）。
//
//	架构（照搬 mymusic 已验证模式）:
//	  - 后端 server.New() 监听 tcp4 0.0.0.0:8080（IPv4 明确:局域网电视 + 窗口 127.0.0.1 都能连）
//	  - wails3 WebviewWindowOptions.URL = http://127.0.0.1:8080/ui 直载真实后端（同源，非 asset 桥）
//	  - 不弹系统浏览器（双击即出原生窗口，像原生应用）
//	  - 隐藏渲染窗口（webview 渲染搜索引擎）:SetURL(引擎URL) → WindowLoadFinished → JS 注入
//	    fetch POST outerHTML 回 /api/search/render（CORS）→ ParseRendered。无头 Chrome 弃用
//	    （用户决策:搜索引擎全走 webview，跨端统一:桌面 WebKitGTK / Android 系统 WebView）。
//
//	平台差异:桌面=本壳（wails3 原生窗口）；安卓=自写壳（android/，纯 aapt2，已在真机跑通）。
package main

import (
	"math/big"
	"crypto/x509/pkix"
	"crypto/x509"
	"crypto/tls"
	"crypto/rand"
	"crypto/elliptic"
	"crypto/ecdsa"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"mycast/internal/server"
)

// httpsPort 渲染窗口回传 https 端口（自签）
const httpsPort = "8443"

// backendPort 后端 HTTP 端口（HC_PORT 覆盖）；与 server.Port 默认 8080 一致
func backendPort() string {
	if p := os.Getenv("HC_PORT"); p != "" {
		return p
	}
	return server.Port
}

// renderJS 渲染窗口注入:页面加载完成后（WindowLoadFinished）拿 outerHTML → fetch POST 回后端。
// engine 从 host 判断（so.com/baidu/bing）；后端 CORS 允许跨域。无头 Chrome 弃用（用户决策）。
func renderJS(port string) string {
	return `(function() {
  var host = location.hostname;
  var engine = host.indexOf('so.com') >= 0 ? 'so' : host.indexOf('baidu.com') >= 0 ? 'baidu' : host.indexOf('bing.com') >= 0 ? 'bing' : '';
  if (engine) {
    setTimeout(function() {
      try {
        var html = document.documentElement.outerHTML;
        var url = encodeURIComponent(location.href);
        fetch('https://127.0.0.1:` + port + `/api/search/render?engine=' + engine + '&url=' + url, {
          method: 'POST', headers: { 'Content-Type': 'text/plain' }, body: html
        }).catch(function(e) {
          try { fetch('https://127.0.0.1:` + port + `/api/search/render?engine=' + engine + '&url=' + url + '&err=1', {method:'POST',headers:{'Content-Type':'text/plain'},body:String(e)}); } catch(e2){}
        });
      } catch (e) {
        try { fetch('https://127.0.0.1:` + port + `/api/search/render?engine=' + engine + '&err=1', {method:'POST',headers:{'Content-Type':'text/plain'},body:String(e)}); } catch(e2){}
      }
    }, 2000);
  }
})();`
}


// genSelfSignedCert 生成自签证书（CN=127.0.0.1）:渲染窗口 fetch https 回传（混合内容绕过）。
func genSelfSignedCert() (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(30 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}

// serveHTTPS 后端 https 监听（自签，渲染窗口 fetch https 回传 outerHTML，无混合内容）。
func serveHTTPS(inst *server.Instance, port string) {
	cert, err := genSelfSignedCert()
	if err != nil {
		log.Printf("[mycast] https 证书: %v", err)
		return
	}
	addr := "0.0.0.0:" + port
	ln, err := tls.Listen("tcp4", addr, &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		log.Printf("[mycast] https listen %s: %v", addr, err)
		return
	}
	log.Printf("[mycast] https://127.0.0.1:%s (渲染窗口回传)", port)
	srv := &http.Server{Handler: inst.Handler, ReadHeaderTimeout: 10 * time.Second}
	if err := srv.Serve(ln); err != nil {
		log.Printf("[mycast] https serve: %v", err)
	}
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

	// 主窗口:mycast UI（真实后端 HTTP，同源）
	mainWin := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "MyCast 家庭影院",
		Width:            1280,
		Height:           800,
		MinWidth:         920,
		MinHeight:        620,
		BackgroundColour: application.NewRGB(17, 17, 17),
		URL:              "http://127.0.0.1:" + port + "/ui",
		JS:               "window.hcEnv='desktop';",
	})
	mainWin.Show()
	mainWin.SetAlwaysOnTop(true) // 主窗口置顶，盖住渲染小窗（用户只看到主窗口）

	// 隐藏渲染窗口:webview 渲染搜索引擎（SetURL → WindowLoadFinished → JS fetch outerHTML 回后端）
	// 注意:WebKitGTK 隐藏窗口（Hide）不渲染 → 显示但移出屏幕（-2000,-2000，不打扰）。
	// 无 DISPLAY（SSH 无 X）:WebKitGTK 无法加载远程页，不注入（/api/search 走 fetch 后备）。
	if os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != "" {
		// 渲染窗口极小（WebKitGTK 可见才渲染；屏幕角落不挡主窗口），主窗口居中置顶。
		renderWin := app.Window.NewWithOptions(application.WebviewWindowOptions{
			Title:            "mycast-render",
			Width:            800,
			Height:           400,
			URL:              "about:blank",
			JS:               renderJS(httpsPort),
			BackgroundColour: application.NewRGB(17, 17, 17),
		})
		_ = renderWin // 移到屏幕外（WebKitGTK 可见才渲染）；主窗口置顶遮挡
		renderWin.SetPosition(0, 0) // 屏幕左上角小窗（WebKitGTK 可见才渲染，不挡主窗口）

		// WebviewRender 注入:/api/search 触发渲染窗口 SetURL（异步 JS 回传 → /api/search/render → ParseRendered）
		inst.WebviewRender = func(url string) {
			log.Printf("[render] SetURL %s", url)
			renderWin.SetURL(url)
		}
		renderWin.SetPosition(0, 0) // 屏幕左上角小窗（WebKitGTK 可见才渲染，不挡主窗口）
		// 临时测试:渲染窗口加载 https 本地（TLS IGNORE 信任自签 + JS 回传）
		renderWin.SetURL("https://127.0.0.1:" + httpsPort + "/ui")
		log.Printf("[mycast] webview 渲染窗口已就绪（DISPLAY=%s）", os.Getenv("DISPLAY"))
	}

	// 渲染窗口回传 https（自签，无混合内容）:so.com https 页 fetch https://127.0.0.1:8443 回传 outerHTML
	go serveHTTPS(inst, httpsPort)

	if err := app.Run(); err != nil {
		os.Exit(1)
	}
}
