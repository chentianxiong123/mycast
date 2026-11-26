package main

import (
	"context"
	"fmt"

	"mycast/internal/search"
)

func main() {
	ag := search.New(search.Default())
	all, _ := ag.Search(context.Background(), "我的山与海")
	counts := map[string]int{}
	for _, r := range all {
		counts[r.Engine]++
	}
	for _, eng := range []string{"baidu", "so", "sogou", "bing"} {
		fmt.Printf("  %-8s: %d 条\n", eng, counts[eng])
	}
	fmt.Printf("全聚合: %d 条\n", len(all))
}
