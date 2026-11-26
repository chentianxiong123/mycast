//go:build mygo

// 测 mygo WebView 渲染顺不顺：直接加载几个真网站（baidu/sogou/bing），看 mygo 能不能顺。
package main

import (
	"log"

	"github.com/egoist/mygo"
)

func main() {
	mygo.App.WhenReady(func() {
		// 几个真网站（看 mygo WebView 渲染顺不顺）
		mygo.NewWindow(mygo.WindowOptions{
			Title:    "baidu",
			Width:    900,
			Height:   700,
			StateKey: "baidu",
			URL:      "https://www.baidu.com/",
		})
		mygo.NewWindow(mygo.WindowOptions{
			Title:    "sogou",
			Width:    900,
			Height:   700,
			StateKey: "sogou",
			URL:      "https://www.sogou.com/",
		})
		mygo.NewWindow(mygo.WindowOptions{
			Title:    "bing",
			Width:    900,
			Height:   700,
			StateKey: "bing",
			URL:      "https://www.bing.com/",
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
