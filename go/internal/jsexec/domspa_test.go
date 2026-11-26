package jsexec

import (
	"fmt"
	"testing"
	"time"

	"mycast/internal/httptool"
)

// TestDomReactVueSites React/Vue SPA 站——看 go-webengine 能否"跑 JS 渲染出数据"
func TestDomReactVueSites(t *testing.T) {
	sites := []struct{ name, url string }{
		{"React官网", "https://react.dev/learn"},
		{"Vue官网", "https://vuejs.org"},
		{"ElementPlus(Vue)", "https://element-plus.org"},
		{"掘金(SPA)", "https://juejin.cn"},
		{"知乎(SPA)", "https://www.zhihu.com"},
	}
	hc, err := httptool.New(httptool.Config{Timeout: 15 * time.Second})
	if err != nil {
		t.Skip(err)
	}
	eng, err := New(Config{HTTP: hc, UA: "Mozilla/5.0 (Linux; Android 13) Chrome/120.0", Timeout: 12 * time.Second})
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
		// 执行 JS 后节点数（js.Begin 跑完 JS 再 Eval）
		afterNodes, _ := eng.WEEval(html, s.url, `document.querySelectorAll('*').length`)
		title, _ := eng.WEEval(html, s.url, `document.title`)
		t.Logf("  跑JS后节点=%v | 标题=%v | HTML=%d B", afterNodes, title, len(html))
		// 判断：节点数 > 50 说明 DOM 渲染出内容了（SPA 跑满 JS）；< 50 说明空壳/没跑满
		if n, ok := afterNodes.(int64); ok && n > 50 {
			t.Logf("  ✅ DOM 节点 %d（>50，JS 渲染出内容/或服务端有数据）", n)
		} else {
			t.Logf("  ⚠️ DOM 节点 %v（少，SPA 空壳/JS 没跑满 React）", afterNodes)
		}
		_ = fmt.Sprint
	}
}
