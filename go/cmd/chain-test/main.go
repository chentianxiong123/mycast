package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"mycast/internal/search"
	"mycast/internal/sniff"
)

func main() {
	ag := search.New(search.Default())
	all, _ := ag.Search(context.Background(), "我的山与海")
	var baiduRes []search.Result
	for _, r := range all {
		if r.Engine == "baidu" {
			baiduRes = append(baiduRes, r)
		}
	}
	fmt.Printf("① 搜索: %d 条 baidu 结果\n", len(baiduRes))

	client := &http.Client{Timeout: 15 * time.Second}
	var realURLs []string
	for _, r := range baiduRes[:min(5, len(baiduRes))] {
		u := r.URL
		if strings.Contains(u, "baidu.com/link") {
			req, _ := http.NewRequest("GET", "https://"+u, nil)
			req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/120.0.0.0 Safari/537.36")
			if resp, err := client.Do(req); err == nil {
				realURLs = append(realURLs, resp.Header.Get("Location"))
				resp.Body.Close()
			}
		} else {
			realURLs = append(realURLs, u)
		}
	}
	fmt.Printf("② follow 真实 URL（前 5）:\n")
	for i, u := range realURLs[:min(5, len(realURLs))] {
		fmt.Printf("   [%d] %s\n", i, u[:min(70, len(u))])
	}

	if len(realURLs) > 0 {
		fmt.Printf("③ L0 取流（sniff.ExtractURL，试前 3 个真实 URL）:\n")
		ext := sniff.New(sniff.Default())
		for i, u := range realURLs[:min(3, len(realURLs))] {
			if strings.TrimSpace(u) == "" {
				continue
			}
			eps, err := ext.ExtractURL(u)
			if err != nil {
				fmt.Printf("   [%d] %s → 取流失败: %v\n", i, u[:min(40, len(u))], err)
			} else if len(eps) == 0 {
				fmt.Printf("   [%d] %s → L0 取流空（L0 没识别，要 L2/jsexec）\n", i, u[:min(40, len(u))])
			} else {
				fmt.Printf("   [%d] %s → ✓ L0 取流成功（%d 集）\n", i, u[:min(40, len(u))], len(eps))
			}
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
