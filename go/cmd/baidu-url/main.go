package main

import (
	"context"
	"fmt"

	"mycast/internal/search"
)

func main() {
	ag := search.New(search.Default())
	all, _ := ag.Search(context.Background(), "我的山与海")
	for _, r := range all {
		if r.Engine == "baidu" {
			fmt.Printf("  [baidu] %s\n", r.URL)
		}
	}
}
