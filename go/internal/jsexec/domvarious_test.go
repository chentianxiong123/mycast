package jsexec

import (
	"testing"
	"time"

	"mycast/internal/httptool"
)

// TestDomVariousSites 不限影视，测各种类型站的 DOM 解析能力
func TestDomVariousSites(t *testing.T) {
	sites := []struct{ name, url string }{
		// 新闻
		{"新浪新闻(服务端)", "https://news.sina.com.cn"},
		{"网易新闻(服务端)", "https://www.163.com"},
		// 工具/文档
		{"GitHub(官方HTML)", "https://github.com"},
		{"MDN文档(服务端)", "https://developer.mozilla.org/zh-CN/docs/Web/HTML"},
		// 电商
		{"淘宝(官方SPA?)", "https://www.taobao.com"},
		{"京东(服务端)", "https://www.jd.com"},
		// 论坛/社区
		{"知乎(官方SPA?)", "https://www.zhihu.com"},
		{"V2EX(服务端)", "https://v2ex.com"},
		// 技术博客(自建)
		{"掘金(官方)", "https://juejin.cn"},
		// SPA
		{"React官网(SPA)", "https://react.dev"},
	}
	hc, err := httptool.New(httptool.Config{Timeout: 15 * time.Second})
	if err != nil {
		t.Skip(err)
	}
	eng, err := New(Config{HTTP: hc, UA: "Mozilla/5.0 (Linux; Android 13) Chrome/120.0", Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	for _, s := range sites {
		t.Logf("=== %s ===", s.name)
		html, ferr := hc.Get(s.url)
		if ferr != nil {
			t.Logf("  ⚠️ 抓不到: %v", ferr)
			continue
		}
		t.Logf("  HTML %d B", len(html))
		// 标题 + DOM 节点数 + 链接数（验证 DOM 解析成功）
		title, _ := eng.WEEval(html, s.url, `document.title`)
		nodes, _ := eng.WEEval(html, s.url, `document.querySelectorAll('*').length`)
		links, _ := eng.WEEval(html, s.url, `document.querySelectorAll('a').length`)
		t.Logf("  标题: %v", title)
		t.Logf("  DOM 节点: %v | 链接: %v", nodes, links)
	}
}
