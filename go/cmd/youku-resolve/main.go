package main

import (
	"context"
	"fmt"

	"mycast/internal/jsexec"
	"mycast/internal/sniff"
)

func main() {
	ykURL := "http://v.youku.com/v_show/id_XNjUxNjk2NjkwOA==.html"
	// L0 取流（sniff.ExtractURL）
	ext := sniff.New(sniff.Default())
	eps, err := ext.ExtractURL(ykURL)
	if err != nil {
		fmt.Printf("L0 取流: %v\n", err)
	} else if len(eps) > 0 {
		fmt.Printf("L0 取流成功：%d 集\n", len(eps))
	}
	// L2 取流（jsexec.WEResolveStream：先 Fetch 优酷 HTML，再真 DOM 执行）
	youkuHTML, ferr := ext.Fetch(ykURL)
	if ferr != nil {
		fmt.Printf("优酷 Fetch: %v\n", ferr)
	} else {
		jse, _ := jsexec.New(jsexec.Config{})
		defer jse.Close()
		stream, globals, err := jse.WEResolveStream(youkuHTML, ykURL)
		if err != nil {
			fmt.Printf("L2(jsexec) 取流: %v\n", err)
		} else if stream != "" {
			fmt.Printf("L2(jsexec) 取流成功:\n  流地址(前 80): %s\n", stream[:min(80, len(stream))])
		} else {
			fmt.Printf("L2(jsexec) 取流空（优酷 SPA 需登录态/JS 重，jsexec 没解析到流）globals=%d\n", len(globals))
		}
		_ = context.Background()
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
