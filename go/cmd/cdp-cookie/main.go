// cdp-cookie：复制浏览器 profile → 起 CDP 浏览器 → 导航各搜索引擎 → 抓 cookie → 存 cookies/<engine>.json。
// 用途：给搜索引擎"登录 cookie 固化"（带 BDUSS 等登录态 cookie，过反爬/验证码）。
// 用法：mycast cdp-cookie 子命令（或 go run ./cmd/cdp-cookie）
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// EngineCookie 引擎 → 导航 URL（激活该引擎 cookie 域）。
var engineNavURLs = map[string]string{
	"baidu": "https://www.baidu.com/s?word=test",
	"so":    "https://www.so.com/s?q=test",
	"sogou": "https://www.sogou.com/web?query=test",
	"bing":  "https://cn.bing.com/search?q=test",
}

// CookieMap cookies/<engine>.json 格式（与 httptool.SaveCookies 兼容）。
type CookieMap map[string]string

func main() {
	// 参数：[user-data-dir] [port]
	profile := "/tmp/edge-profile-cdp" // 复制的 profile（含 77 条 baidu cookie）
	port := "9334"
	if len(os.Args) > 1 {
		profile = os.Args[1]
	}
	if len(os.Args) > 2 {
		port = os.Args[2]
	}

	// ① 复制 profile（若没复制）
	if _, err := os.Stat(profile); err != nil {
		edgeProfile := filepath.Join(os.Getenv("HOME"), ".config/microsoft-edge")
		fmt.Printf("复制 profile %s → %s ...\n", edgeProfile, profile)
		exec.Command("cp", "-r", edgeProfile, profile).Run()
	}
	// 清 Singleton 锁
	os.RemoveAll(filepath.Join(profile, "SingletonLock"))
	os.RemoveAll(filepath.Join(profile, "SingletonSocket"))
	os.RemoveAll(filepath.Join(profile, "SingletonCookie"))

	// ② 起 CDP Edge（复制 profile）
	fmt.Printf("起 CDP Edge（%s, port %s）...\n", profile, port)
	cmd := exec.Command("/usr/bin/microsoft-edge",
		"--remote-debugging-port="+port,
		"--user-data-dir="+profile,
		"--remote-allow-origins=*",
		"--no-first-run",
		"--no-default-browser-check",
		"about:blank")
	cmd.Start()
	time.Sleep(5 * time.Second)
	// 确认 CDP 通
	ok := false
	for i := 0; i < 10; i++ {
		if resp, err := http.Get("http://127.0.0.1:" + port + "/json/version"); err == nil {
			resp.Body.Close()
			ok = true
			break
		}
		time.Sleep(1 * time.Second)
	}
	if !ok {
		fmt.Println("CDP 没起来（9334 占用？）")
		os.Exit(1)
	}
	fmt.Println("CDP 起来")

	// ③ 各引擎导航 + 抓 cookie（Node CDP ws，复用 node -e）
	cookieDir := "cookies"
	os.MkdirAll(cookieDir, 0755)
	// 抓全部 cookie（一次 CDP 会话），按引擎域筛
	allCookies := fetchAllCookies(port)
	cookieMap := CookieMap{}
	for eng, nav := range engineNavURLs {
		_ = nav
		// 筛该引擎域 cookie
		domain := engToDomain(eng)
		var engCookies []string
		for _, c := range allCookies {
			if strings.Contains(c.Domain, domain) {
				engCookies = append(engCookies, c.Name+"="+c.Value)
			}
		}
		if len(engCookies) > 0 {
			cookieMap[eng] = strings.Join(engCookies, "; ")
			fn := filepath.Join(cookieDir, eng+".json")
			jsonData, _ := json.MarshalIndent(CookieMap{eng: strings.Join(engCookies, "; ")}, "", "  ")
			os.WriteFile(fn, jsonData, 0644)
			fmt.Printf("  [%s] %d 条 cookie → %s\n", eng, len(engCookies), fn)
		} else {
			fmt.Printf("  [%s] 0 条（域 %s 没 cookie）\n", eng, domain)
		}
	}
	fmt.Println("完成。cookie 存到 cookies/<engine>.json")
}

type cdpCookie struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Domain string `json:"domain"`
}

// fetchAllCookies 用 Node CDP 抓全部 cookie（一次会话）。
func fetchAllCookies(port string) []cdpCookie {
	// node 自带 WebSocket：先导航各引擎页（激活 cookie 域），再 getAllCookies。
	navURLs := []string{
		"https://www.baidu.com/s?word=test",
		"https://passport.baidu.com/",
		"https://www.so.com/s?q=test",
		"https://www.sogou.com/web?query=test",
		"https://cn.bing.com/search?q=test",
	}
	var navJS strings.Builder
	for _, u := range navURLs {
		navJS.WriteString(fmt.Sprintf("ws.send(JSON.stringify({method:'Page.navigate',params:{url:'%s'}}));await new Promise(r=>setTimeout(r,2000));\n", u))
	}
	nodeScript := fmt.Sprintf(`
const http = require('http');
http.get('http://127.0.0.1:%s/json', res => {
  let d=''; res.on('data',c=>d+=c); res.on('end',async()=>{
    const t = JSON.parse(d);
    if (!t.length) { console.log('[]'); process.exit(0); }
    const ws = new WebSocket(t[0].webSocketDebuggerUrl);
    await new Promise(r=>ws.onopen=r);
%s
    ws.send(JSON.stringify({id:1, method:'Network.getAllCookies'}));
    ws.onmessage = (m) => {
      const r = JSON.parse(m.data);
      if (r.id === 1) {
        const cs = (r.result.cookies||[]).map(c=>({name:c.name, value:c.value, domain:c.domain}));
        console.log(JSON.stringify(cs));
        ws.close(); process.exit(0);
      }
    };
  });
});
`, port, navJS.String())
	out, err := exec.Command("node", "-e", nodeScript).Output()
	if err != nil {
		fmt.Println("node 抓 cookie 失败:", err, "output:", string(out[:min(200, len(out))]))
		return nil
	}
	var cs []cdpCookie
	json.Unmarshal(out, &cs)
	return cs
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func engToDomain(eng string) string {
	switch eng {
	case "baidu":
		return "baidu"
	case "so":
		return "so.com"
	case "sogou":
		return "sogou"
	case "bing":
		return "bing"
	default:
		return eng
	}
}
