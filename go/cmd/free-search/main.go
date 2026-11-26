package main

import (
	"context"
	"fmt"

	"mycast/internal/search"
)

func main() {
	ag := search.New(search.Default())
	all, _ := ag.Search(context.Background(), "我的山与海 免费在线播放")
	for _, r := range all {
		fmt.Printf("  IsZiYuan=%v | %s\n", r.IsZiYuan, r.URL[:min(55, len(r.URL))])
	}
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
