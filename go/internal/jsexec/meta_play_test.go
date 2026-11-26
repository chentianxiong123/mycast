package jsexec

import (
	"testing"
	"time"

	"mycast/internal/httptool"
	"mycast/internal/meta"
)

// TestRealSiteJSMeta 真实站 jsexec 取播放元数据（JS 引擎执行页面脚本 hook 全局/fetch）
func TestRealSiteJSMeta(t *testing.T) {
	hc, err := httptool.New(httptool.Config{Timeout: 15 * time.Second})
	if err != nil {
		t.Skip(err)
	}
	playURL := "https://m.xaruihu.com/vodplay/94275-1-1.html"
	html, _ := hc.Get(playURL)
	t.Logf("播放页 %d B", len(html))

	eng, err := New(Config{
		HTTP:    hc,
		UA:      "Mozilla/5.0 (Linux; Android 13) Chrome/120.0",
		Timeout: 8 * time.Second,
		BaseURL: playURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	res := eng.ExecPage(html, playURL)
	t.Logf("jsexec: Scripts=%d Hits=%d Error=%q", res.Scripts, len(res.Hits), res.Error)
	// JS 引擎能拿到什么？
	if res.Media != nil {
		t.Logf("✅ jsexec 取到元数据: Title=%q Actors=%q Director=%q Region=%v",
			res.Media.Title, res.Media.Actors, res.Media.Director, res.Media.Region)
	} else {
		t.Logf("⚠️ jsexec 没取到元数据（xaruihu 是服务端渲染，元数据在 HTML 不在 JS 变量 → L0 正则够，JS 引擎用不上）")
	}
	// 对比：L0 正则（meta.FromHTML）能拿多少
	m := meta.FromHTML(html)
	t.Logf("对比 L0 正则: Title=%q Actors=%q Director=%q Region=%v",
		m.Title, m.Actors, m.Director, m.Region)
}
