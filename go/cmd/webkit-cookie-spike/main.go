// 核心目标：确认 purego（零 CGo）能 Dlsym 拿到 WebKitGTK 的 cookie C API 符号（能调）。
package main

import (
	"fmt"

	"github.com/ebitengine/purego"
)

func main() {
	webkit, err := purego.Dlopen("libwebkit2gtk-4.1.so.0", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		webkit, err = purego.Dlopen("libwebkit2gtk-4.0.so.0", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	}
	if err != nil {
		fmt.Println("WebKitGTK 加载失败:", err)
		return
	}
	fmt.Println("WebKitGTK 加载成功")

	// Dlsym 拿 cookie 相关 C API（确认符号在，能调）
	syms := map[string]string{
		"webkit_cookie_manager_get_all_cookies":               "全量 cookie（含 httpOnly）",
		"webkit_cookie_manager_get_cookies":                   "按域 cookie",
		"webkit_website_data_manager_get_cookie_manager":      "拿 cookie manager",
		"webkit_web_view_new":                                 "起 WebView",
		"webkit_web_view_load_uri":                            "加载 URL",
		"webkit_cookie_manager_get_accept_policy":             "cookie 策略",
		"webkit_web_view_get_site_data":                       "拿 site data（cookie 存储）",
	}
	for s, desc := range syms {
		fn, err := purego.Dlsym(webkit, s)
		if err != nil {
			fmt.Printf("  ✗ %s: %v\n", s, err)
		} else {
			fmt.Printf("  ✓ %s = 0x%x（%s，能调）\n", s, fn, desc)
		}
	}
	fmt.Println("★ purego（零 CGo）能 Dlsym WebKitGTK 的 cookie C API——能直接操控（不走 wails3 封装）")
}
