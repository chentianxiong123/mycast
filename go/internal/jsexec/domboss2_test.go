package jsexec

import (
	"testing"
	"time"

	"mycast/internal/httptool"
)

// TestDomOfficialSites 官方站/不同类型站 DOM 解析（验证通用性）
func TestDomOfficialSites(t *testing.T) {
	sites := []struct{ name, url string }{
		{"优酷(官方SPA)", "https://www.youku.com"},
		{"B站m站(官方)", "https://m.bilibili.com"},
		{"腾讯视频(官方)", "https://v.qq.com"},
	}
	hc, err := httptool.New(httptool.Config{Timeout: 15 * time.Second})
	if err != nil {
		t.Skip(err)
	}
	eng, err := New(Config{HTTP: hc, UA: "Mozilla/5.0 (Linux; Android 13)", Timeout: 10 * time.Second})
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
		// 标题 + DOM 节点数（验证 DOM 解析成功）
		title, _ := eng.WEEval(html, s.url, `document.title`)
		nodeCount, _ := eng.WEEval(html, s.url, `document.querySelectorAll('*').length`)
		// 看有没有视频相关（官方站流地址多在 JS 变量/API，不一定能直接 Eval 到）
		t.Logf("  标题: %v", title)
		t.Logf("  DOM 节点数: %v（>0 说明 DOM 解析成功）", nodeCount)
	}
}
