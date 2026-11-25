package jsexec

import (
	"testing"
	"time"

	"mycast/internal/httptool"
)

// TestRealSiteL2Resolve 真实站 L2 取流验证（命门）。
// 拿 m.xaruihu.com 的播放页，jsexec 执行 JS 看能否 hook 出 m3u8/mp4。
func TestRealSiteL2Resolve(t *testing.T) {
	// 先直接抓播放页 HTML 看看里面有什么（诊断）
	hc, err := httptool.New(httptool.Config{Timeout: 15 * time.Second})
	if err != nil {
		t.Skip(err)
	}
	playURL := "https://m.xaruihu.com/vodplay/94275-1-1.html"
	html, err := hc.Get(playURL)
	if err != nil {
		t.Fatalf("抓播放页失败: %v", err)
	}
	t.Logf("播放页 HTML %d B", len(html))
	// 诊断：HTML 里有没有 m3u8/mp4（服务端直接写？还是 JS 算？）
	containsStream := containsAny(html, ".m3u8", "mp4", "playurl", "player_data")
	t.Logf("HTML 直接含流特征: %v（true=服务端渲染，false=需 JS 执行）", containsStream)
	// 找 <video> 或 player 容器
	hasVideoTag := containsAny(html, "<video", "videoPlayer", "player")
	t.Logf("HTML 含播放器: %v", hasVideoTag)

	// 跑 jsexec（L2）：hook fetch/XHR/video.src 看能不能拿到
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
	t.Logf("jsexec: Scripts=%d Hits=%d Error=%q Media=%v",
		res.Scripts, len(res.Hits), res.Error, res.Media != nil)
	for i, h := range res.Hits {
		t.Logf("  Hit[%d] %s (via %s)", i, h.URL, h.Source)
	}
	if len(res.Hits) == 0 {
		t.Logf("⚠️ L2 未取到流地址（可能纯服务端渲染，需 L0 直接解析 HTML 的 <video src>）")
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
	}
	return false
}
