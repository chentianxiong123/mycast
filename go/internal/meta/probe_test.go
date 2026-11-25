package meta

import (
	"testing"
	"time"

	"mycast/internal/httptool"
)

// TestProbeRealSources 实测 11 个苹果CMS资源站（fnmedia-ext 的实测列表），
// 搜"我的山与海"，验证 meta.FromFetchResponse 归一化。
func TestProbeRealSources(t *testing.T) {
	sources := []struct{ name, api string }{
		{"360", "https://360zy.com/api.php/provide/vod/at/json"},
		{"优酷", "https://api.ukuapi88.com/api.php/provide/vod/at/json"},
		{"光速", "https://api.guangsuapi.com/api.php/provide/vod/at/json"},
		{"极速", "https://jszyapi.com/api.php/provide/vod/at/json"},
		{"暴风", "https://bfzyapi.com/api.php/provide/vod/at/json"},
		{"非凡", "https://cj.ffzyapi.com/api.php/provide/vod/at/json"},
	}
	hc, err := httptool.New(httptool.Config{Timeout: 10 * time.Second, Delay: 400 * time.Millisecond})
	if err != nil {
		t.Skipf("httptool.New: %v", err)
	}
	wd := "%E6%88%91%E7%9A%84%E5%B1%B1%E4%B8%8E%E6%B5%B7"
	okCount := 0
	for _, src := range sources {
		body, err := hc.Get(src.api + "?ac=detail&wd=" + wd)
		if err != nil {
			t.Logf("  %-8s ❌ %v", src.name, shortErr(err))
			continue
		}
		if len(body) < 10 {
			t.Logf("  %-8s ❌ 空 (%d B)", src.name, len(body))
			continue
		}
		if len(body) > 200 && (contains(body, "Just a moment") || contains(body, "challenge-platform")) {
			t.Logf("  %-8s ⚠️ Cloudflare 拦 (%d B)", src.name, len(body))
			continue
		}
		m := FromFetchResponse(body)
		if m.Empty() {
			t.Logf("  %-8s ⚠️ 无苹果CMS字段 (%d B): %.60s", src.name, len(body), truncate(body, 60))
			continue
		}
		okCount++
		t.Logf("  %-8s ✅ %s | 主演=%s | 导演=%s | 地区=%v | 年份=%s | 评分=%s",
			src.name, m.Title, m.Actors, m.Director, m.Region, m.Year, m.Score)
	}
	t.Logf("=== 命中 %d/%d 站 ===", okCount, len(sources))
}

func shortErr(err error) string {
	s := err.Error()
	if len(s) > 60 {
		return s[:60] + "..."
	}
	return s
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
