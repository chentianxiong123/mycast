package jsexec

import (
	"testing"
	"time"

	"mycast/internal/httptool"
)

// TestDomBaiduSearch 用 go-webengine 操控百度搜影视，看搜索结果 + 哪些站能进
func TestDomBaiduSearch(t *testing.T) {
	hc, err := httptool.New(httptool.Config{Timeout: 15 * time.Second})
	if err != nil {
		t.Skip(err)
	}
	eng, err := New(Config{HTTP: hc, UA: "Mozilla/5.0 (Linux; Android 13) Chrome/120.0", Timeout: 12 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	// 百度搜"我的山与海"
	searchURL := "https://www.baidu.com/s?wd=%E6%88%91%E7%9A%84%E5%B1%B1%E4%B8%8E%E6%B5%B7"
	html, ferr := hc.Get(searchURL)
	if ferr != nil {
		t.Logf("⚠️ 百度搜不到: %v", ferr)
		return
	}
	t.Logf("百度搜索结果页 HTML %d B", len(html))
	title, _ := eng.WEEval(html, searchURL, `document.title`)
	nodes, _ := eng.WEEval(html, searchURL, `document.querySelectorAll('*').length`)
	t.Logf("标题: %v | DOM 节点: %v", title, nodes)
	// 提取搜索结果里的链接（<a href>，看哪些影视站能进）
	links, _ := eng.WEEval(html, searchURL, `
		[...document.querySelectorAll('.result a[href], h3 a[href], .c-container a[href]')]
			.map(a => ({h: a.href, t: a.textContent.trim()}))
			.filter(o => o.h && o.h.startsWith("http") && o.t.length > 2)
	`)
	t.Logf("结果链接: %v", links)
}
