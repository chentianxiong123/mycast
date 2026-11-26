package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"mycast/internal/search"
	"mycast/internal/sniff"
)

// 一条路②③④⑤：搜索（cookies/baidu.json BDUSS）→ 出结果（资源站）→ 嗅探（L0）→ 投屏（DLNA）
func main() {
	cookieDir := "/home/a1/.local/share/mycast/cookies"
	cookieHeader := readCookieHeader(cookieDir, "baidu")
	if cookieHeader == "" {
		fmt.Println("cookies/baidu.json 没 cookie（先跑 fetch-cookie 固化 BDUSS）")
		return
	}
	eng := search.New(search.Config{
		UA:        "Mozilla/5.0 (Linux; Android 13; SM-S9180) AppleWebKit/537.36 Chrome/120.0 Mobile Safari/537.36",
		CookieDir: cookieDir,
	})
	eng.CookieFn = func(engine string) string { return cookieHeader }  // 读 cookies/baidu.json（BDUSS）
	fmt.Printf("② 搜索 baidu（带 BDUSS）: ")
	res, err := eng.Search(context.Background(), "我的山与海 免费在线播放")
	if err != nil {
		fmt.Println("err:", err)
		return
	}
	fmt.Printf("%d 结果（资源站 %d）\n", len(res), countZY(res))
	for i, r := range res[:min(5, len(res))] {
		mk := ""
		if r.IsZiYuan {
			mk = " ★资源站"
		}
		fmt.Printf("   [%d]%s %s\n", i, mk, r.URL[:min(55, len(r.URL))])
	}
	if zurl := firstZY(res); zurl != "" {
		fmt.Printf("\n③ 嗅探资源站 → L0: %s\n", zurl[:min(45, len(zurl))])
		sn := sniff.New(sniff.Config{})
		stream, mime, err := sn.ResolveStream(zurl)
		if err != nil {
			fmt.Printf("   L0 取流: %v（试 L2）\n", err)
			sn2 := sniff.New(sniff.Config{})
			if html, herr := sn2.Fetch(zurl); herr == nil {
				stream, mime, err = sn2.ResolveStream(zurl)
				_ = html
			}
		}
		if err == nil {
			fmt.Printf("   ✓ 取流成功: %s (%s)\n", stream[:min(55, len(stream))], mime)
		}
	}
	fmt.Printf("\n④ 投屏：DLNA 中继（UI/DLNA 页投屏）\n")
}

func readCookieHeader(dir, engine string) string {
	b, _ := os.ReadFile(dir + "/" + engine + ".json")
	if len(b) == 0 {
		return ""
	}
	var entries []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal(b, &entries); err != nil {
		return ""
	}
	var parts []string
	for _, e := range entries {
		parts = append(parts, e.Name+"="+e.Value)
	}
	return strings.Join(parts, "; ")
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func countZY(r []search.Result) int {
	n := 0
	for _, x := range r {
		if x.IsZiYuan {
			n++
		}
	}
	return n
}
func firstZY(r []search.Result) string {
	for _, x := range r {
		if x.IsZiYuan {
			return x.URL
		}
	}
	return ""
}
