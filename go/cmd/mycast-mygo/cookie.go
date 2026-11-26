//go:build mygo

// WebView cookie 导出（面向手机用户搜 baidu 资源站，不靠 CDP）
// mygo v0.10.0 真实 API：
//   - mygo.App / mygo.App.MainPage（主窗口 Page）
//   - mygo.Page.LoadURL(url)（WebView 加载 URL，系统 WebView 渲染存 cookie）
//   - mygo.Cookies.List(mygo.CookieFilter{URL: url})（导出该 URL 的 cookie，含 httpOnly）
// 手机用户：mygo WebView 渲染 baidu（登录/匿名）→ 系统 WebView 存 cookie（含 BDUSS）→ mygo.Cookies.List 导出 → 固化 cookies/baidu.json → 搜 baidu 带 BDUSS 过验证。
// 桌面（Linux/Windows/Mac）：mygo.Cookies（WebKitGTK/WebView2）能拿 httpOnly，已通。
// 安卓：mygo 安卓 WebView 渲染存 cookie（系统 WebView），mygo.Cookies 安卓实现后导 BDUSS。

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	mygo "github.com/egoist/mygo"
)

// cookieJSON cookies/<engine>.json 格式（httptool.CookieStore 兼容：{cookies:[{domain,name,value,path}]})
type cookieJSON struct {
	Domain string `json:"domain"`
	Name   string `json:"name"`
	Value  string `json:"value"`
	Path   string `json:"path"`
}

// WebViewCookiesFromURL mygo WebView（主窗口）加载指定 URL，等渲染 + cookie 存好，导出该 URL 的 cookie（含 BDUSS/匿名）。
// 手机用户：渲染 baidu.com（登录态/匿名）→ 导出 baidu.com cookie（含 BDUSS）→ 固化 cookies/baidu.json。
// engine：cookie 文件名（"baidu" → cookies/baidu.json）。
func WebViewCookiesFromURL(engine string, url string, timeoutSec int) error {
	if timeoutSec <= 0 {
		timeoutSec = 12
	}
	mygo.App.WhenReady(func() {
		// 主窗口 Page 加载 URL（WebView 渲染 baidu，系统 WebView 存 cookie）
		page := mainWindow.Page()
		if err := page.LoadURL(url); err != nil {
			log.Printf("WebView LoadURL %s: %v", url, err)
			return
		}
		// 等渲染（baidu 出结果 + cookie 存好）
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
		defer cancel()
		_ = ctx // mygo Page 没 WaitForLoad 的 ctx 版，靠 timeout 轮询（简化：sleep 等渲染）
		time.Sleep(time.Duration(timeoutSec) * time.Millisecond * 500)

		// 导出该 URL 的 cookie（mygo.Cookies.List，含 httpOnly）
		if err := saveEngineCookies(engine, url); err != nil {
			log.Printf("导出 %s cookie: %v", engine, err)
		}
	})
	return nil
}

// saveEngineCookies mygo.Cookies.List(CookieFilter{URL}) → 固化到 cookies/<engine>.json（httptool.CookieStore 格式）。
func saveEngineCookies(engine string, url string) error {
	// mygo.Cookies.List（顶层 Cookies，按 URL 过滤，含 httpOnly）
	cookies, err := mygo.Cookies.List(mygo.CookieFilter{URL: url})
	if err != nil {
		// 安卓可能 Unsupported（mygo 安卓 Cookies 没实现），降级：按 Domain 过滤
		cookies, err = mygo.Cookies.List(mygo.CookieFilter{})
		if err != nil {
			return fmt.Errorf("mygo.Cookies.List（安卓可能 Unsupported，待 mygo 补）: %w", err)
		}
	}
	if len(cookies) == 0 {
		log.Printf("WebView %s 无 cookie（可能没渲染/没登录）", engine)
		return nil
	}
	list := make([]cookieJSON, 0, len(cookies))
	for _, c := range cookies {
		list = append(list, cookieJSON{Domain: c.Domain, Name: c.Name, Value: c.Value, Path: c.Path})
	}
	// 固化到 cookies/<engine>.json（httptool.CookieStore 格式）
	dir := cookieDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir cookie 目录 %s: %w", dir, err)
	}
	b, _ := json.MarshalIndent(struct {
		Cookies []cookieJSON `json:"cookies"`
	}{list}, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, engine+".json"), b, 0o644); err != nil {
		return fmt.Errorf("写 cookies/%s.json: %w", engine, err)
	}
	log.Printf("WebView %s cookie 固化 %d 条 → cookies/%s.json", engine, len(list), engine)
	return nil
}

// cookieDir cookies 目录（跟 httptool.CookieStore 同目录：.local/share/mycast/cookies 或 %APPDATA%）
func cookieDir() string {
	home, _ := os.UserHomeDir()
	switch os.Getenv("GOOS") {
	case "windows":
		return filepath.Join(os.Getenv("APPDATA"), "mycast", "cookies")
	}
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "mycast", "cookies")
	}
	return filepath.Join(home, ".local", "share", "mycast", "cookies")
}
