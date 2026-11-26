package jsexec

import (
	"testing"
	"time"

	"mycast/internal/httptool"
)

// TestDomBilibili B站各页 DOM 解析（B站是"部分SSR+部分SPA"，看 go-webengine 能拿到什么）
func TestDomBilibili(t *testing.T) {
	sites := []struct{ name, url string }{
		{"B站m站首页", "https://m.bilibili.com"},
		{"B站PC首页", "https://www.bilibili.com"},
		{"B站视频页(示例)", "https://m.bilibili.com/video/BV1GJ411x7h7"},
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
		t.Logf("=== %s (%s) ===", s.name, s.url)
		html, ferr := hc.Get(s.url)
		if ferr != nil {
			t.Logf("  ⚠️ 抓不到: %v", ferr)
			continue
		}
		t.Logf("  HTML %d B", len(html))
		title, _ := eng.WEEval(html, s.url, `document.title`)
		nodes, _ := eng.WEEval(html, s.url, `document.querySelectorAll('*').length`)
		// B站视频页：看有没有流地址（B站用 DASH，数据在 window.__playInfo 或 API）
		stream, _ := eng.WEEval(html, s.url, `(function(){try{ if(window.__playInfo__ && window.__playInfo__.data) return "has __playInfo__"; if(window.__INITIAL_STATE__ && window.__INITIAL_STATE__.playInfo) return "has __INITIAL_STATE__.playInfo"; return "no playInfo global"; }catch(e){return "err:"+e}})()`)
		// B站视频标题（SSR 会渲染进 DOM）
		vidTitle, _ := eng.WEEval(html, s.url, `document.querySelector('h1') ? document.querySelector('h1').textContent : document.title`)
		t.Logf("  标题: %v", title)
		t.Logf("  DOM 节点: %v | h1/视频标题: %v", nodes, vidTitle)
		t.Logf("  流数据出口: %v", stream)
	}
}
