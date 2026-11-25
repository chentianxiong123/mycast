package jsexec

import (
	"fmt"
	"testing"
	"time"

	"mycast/internal/httptool"
)

// TestDomMultiSite 各种站 DOM 解析测试（go-webengine dom+js，不渲染）
func TestDomMultiSite(t *testing.T) {
	sites := []struct{ name, url string }{
		{"xaruihu播放页(苹果CMS)", "https://m.xaruihu.com/vodplay/94275-1-1.html"},
		{"vipjuji播放页(苹果CMS)", "https://www.vipjuji.com/vodplay/67381-1-1.html"},
		{"xaruihu分类页(苹果CMS)", "https://m.xaruihu.com/vodtype/2.html"},
	}
	hc, err := httptool.New(httptool.Config{Timeout: 15 * time.Second})
	if err != nil {
		t.Skip(err)
	}
	eng, err := New(Config{HTTP: hc, UA: "Mozilla/5.0 (Linux; Android 13)", Timeout: 8 * time.Second})
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
		// 标题
		title, _ := eng.WEEval(html, s.url, `document.title`)
		// 流
		stream, _, _ := eng.WEResolveStream(html, s.url)
		// 集数/链接数
		epCount, _ := eng.WEEval(html, s.url, `[...document.querySelectorAll('a')].filter(a=>/play|ep|vodplay|集/.test(a.href+a.textContent)).length`)
		t.Logf("  标题: %v", title)
		t.Logf("  链接/集数数: %v", epCount)
		if stream != "" {
			t.Logf("  ✅ 流: %s", stream)
		} else {
			t.Logf("  (无流: 分类页/无 player_aaaa)")
		}
		_ = fmt.Sprint
	}
}
