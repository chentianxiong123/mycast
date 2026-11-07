// mycast 主程序：B站 投屏中继（手机当中间件，既接收又发送）。
//
//	B站 App ──SSDP/SOAP/NVA──> 本程序(接收端)
//	                              │ 取流 + 本地重封装
//	                              ▼
//	                          本地HTTP ──DLNA──> 电视(发送端)
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"time"

	"mycast/internal/server"
)

func main() {
	noOpen := flag.Bool("no-open", false, "不自动打开浏览器（服务器模式/后台）")
	flag.Parse()

	port := envStr("HCRELAY_PORT", server.Port)
	inst := server.New(server.Options{
		Port:      port,
		IP:        os.Getenv("HCRELAY_IP"),
		AccessKey: os.Getenv("HCRELAY_ACCESS_KEY"),
	})
	defer inst.Stop()

	// 必须 tcp4（IPv4 明确）：Go 的 "tcp"+"0.0.0.0:port" 实际绑 IPv6 [::]:port（双栈），
	// 手机 App 里 IPv4 映射未开 → WebView 连 127.0.0.1:8080 失败（白屏）。
	// tcp4 绑 IPv4 0.0.0.0:8080：桌面局域网电视 + App 内 127.0.0.1 都能连。
	addr := fmt.Sprintf("0.0.0.0:%s", port)
	ln, err := net.Listen("tcp4", addr)
	if err != nil {
		log.Fatalf("[mycast] listen %s: %v", addr, err)
	}
	srv := &http.Server{
		Handler:           inst.Handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("[mycast] base=%s", inst.BaseURL)
	log.Printf("[mycast] open %s/ui", inst.BaseURL)

	// 桌面版：启动后自动打开浏览器（双击即用，像原生应用）。-no-open 关掉（服务器模式）。
	if !*noOpen && hasDesktop() {
		go func() {
			time.Sleep(800 * time.Millisecond) // 等服务就绪
			if err := openBrowser(inst.BaseURL + "/ui"); err != nil {
				log.Printf("[mycast] 自动打开浏览器失败: %v", err)
			}
		}()
	}

	log.Fatal(srv.Serve(ln))
}

// hasDesktop 是否桌面环境（有 DISPLAY/WAYLAND）。无则纯服务器（SSH/容器）。
func hasDesktop() bool {
	if os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != "" {
		return true
	}
	return false
}

// openBrowser 跨平台打开浏览器：Linux xdg-open / macOS open / Windows cmd start。
func openBrowser(u string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", u)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", u)
	default: // linux 等
		cmd = exec.Command("xdg-open", u)
	}
	return cmd.Start()
}

func envStr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func envInt(k string, d int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return d
}
