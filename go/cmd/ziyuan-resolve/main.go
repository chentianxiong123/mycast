package main

import (
	"fmt"

	"mycast/internal/sniff"
)

func main() {
	// 搜"我的山与海 免费在线播放"出的资源站
	ziyuan := []string{
		"http://v.zimg.cn/vod/tv/QbJsbX7nTz8rMn.html",
		"http://www.2sctc.com/tvplay/1202598-1-1.html",
	}
	ext := sniff.New(sniff.Default())
	for _, u := range ziyuan {
		fmt.Printf("\n=== %s ===\n", u[:min(50, len(u))])
		eps, err := ext.ExtractURL(u)
		if err != nil {
			fmt.Printf("  L0 取流: %v\n", err)
		} else if len(eps) > 0 {
			fmt.Printf("  L0 取流成功：%d 集\n", len(eps))
			for i, e := range eps[:min(3, len(eps))] {
				fmt.Printf("    [%d] %s → %s\n", i, e.Title[:min(20, len(e.Title))], e.URL[:min(50, len(e.URL))])
			}
		} else {
			fmt.Printf("  L0 取流空\n")
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
